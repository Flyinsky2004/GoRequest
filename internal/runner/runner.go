package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"gorequest/internal/core"
)

// redactorGuard makes the run-level Redactor safe to mutate from row/step
// goroutines.
type redactorGuard struct {
	mu sync.Mutex
	r  *core.Redactor
}

func (g *redactorGuard) RegisterSecret(v string) {
	if g == nil || v == "" {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.r.RegisterSecret(v)
}

func (g *redactorGuard) RedactString(s string) string {
	if g == nil {
		return s
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.r.RedactString(s)
}

type rowState struct {
	data    map[string]string
	vars    map[string]string
	outputs map[string]core.StepOutput
	mu      sync.RWMutex
	jar     *core.CookieJar
	gen     *rowGenerator
}

func (rs *rowState) snapshotOutputs() map[string]core.StepOutput {
	rs.mu.RLock()
	defer rs.mu.RUnlock()
	out := make(map[string]core.StepOutput, len(rs.outputs))
	for k, v := range rs.outputs {
		out[k] = v
	}
	return out
}

func (rs *rowState) publish(stepID string, outputs map[string]core.StepOutput) {
	if len(outputs) == 0 {
		return
	}
	rs.mu.Lock()
	defer rs.mu.Unlock()
	for k, v := range outputs {
		rs.outputs[stepID+"."+k] = v
	}
}

// Run executes the suite. A preflight failure returns a reportable config-error
// result together with the error; no request has been sent in that case.
func (r *Runner) Run(ctx context.Context) (*RunResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	// A canceled context never triggers a preflight dry-build or network call.
	if err := ctx.Err(); err != nil {
		if r.suite != nil {
			if err := r.suite.ValidateStructure(); err == nil {
				r.order = nil
				for i := range r.suite.Steps {
					r.order = append(r.order, &StepPlan{Step: &r.suite.Steps[i]})
				}
			}
		}
		r.runID = newRunID(r.clock())
		r.events = newEventBus(r.runID, r.clock, r.opts.Emit)
		r.events.SetRedactor(r.red.RedactString)
		res := r.baseResult()
		res.Status = StatusCanceled
		res.Canceled = true
		res.Failure = newFailure(FailureCanceled, KindOther, "run canceled before preflight")
		res.StartedAt = r.clock()
		res.FinishedAt = r.clock()
		if r.data == nil {
			r.data = &dataset{rows: []DataRow{{Index: 0, Vars: map[string]string{}}}}
		}
		canceled := r.canceledRow(r.data.rows[0])
		for _, sp := range r.order {
			canceled.Steps = append(canceled.Steps, StepResult{
				ID: sp.Step.ID, Name: sp.Step.Name, Request: sp.Step.Request,
				Status: StatusSkipped, Reason: "run canceled before preflight",
			})
		}
		res.Rows = []RowResult{canceled}
		res.finalize()
		r.events.send(Event{Kind: EventRunStarted, Status: StatusCanceled})
		r.events.send(Event{Kind: EventRunFinished, Status: StatusCanceled})
		r.redactReport(res)
		return res, nil
	}
	if err := r.Prepare(ctx); err != nil {
		f := r.preflightFailure(err)
		res := r.baseResult()
		res.Status = StatusConfigError
		res.Failure = f
		res.FinishedAt = r.clock()
		res.DurationMs = 0
		res.finalize()
		r.redactReport(res)
		return res, err
	}

	res := r.baseResult()
	res.StartedAt = r.clock()
	r.events.SetRedactor(r.red.RedactString)
	r.events.send(Event{Kind: EventRunStarted, Status: StatusPassed})

	startedReal := time.Now()
	rows := r.data.rows
	res.Rows = make([]RowResult, len(rows))
	var stop atomic.Bool

	limit := r.opts.RowConcurrency
	if limit < 1 {
		limit = 1
	}
	sem := make(chan struct{}, limit)
	var wg sync.WaitGroup
	for pos := range rows {
		if stop.Load() {
			res.Rows[pos] = r.skippedRow(rows[pos], "stopped after an earlier row failed")
			continue
		}
		sem <- struct{}{}
		wg.Add(1)
		go func(pos int, dr DataRow) {
			defer wg.Done()
			defer func() { <-sem }()
			if stop.Load() {
				res.Rows[pos] = r.skippedRow(dr, "stopped after an earlier row failed")
				return
			}
			row := r.runRow(ctx, pos, dr)
			res.Rows[pos] = row
			if r.suite.OnFailure == OnFailureStop && rowFailed(row.Status) {
				stop.Store(true)
			}
		}(pos, rows[pos])
	}
	wg.Wait()

	res.FinishedAt = r.clock()
	res.DurationMs = time.Since(startedReal).Milliseconds()
	res.finalize()
	res.Status = deriveRunStatus(res, ctx)
	res.Canceled = res.Status == StatusCanceled
	if res.Status != StatusPassed && res.Failure == nil {
		switch res.Status {
		case StatusConfigError:
			res.Failure = newFailure(FailureConfig, KindDefinition, "suite contains configuration errors")
		case StatusInternalError:
			res.Failure = newFailure(FailureInternal, KindOther, "suite contains internal errors")
		case StatusCanceled:
			res.Failure = newFailure(FailureCanceled, KindOther, "run canceled")
		default:
			res.Failure = newFailure(FailureRun, KindOther, "suite contains failed steps")
		}
	}
	r.events.send(Event{Kind: EventRunFinished, Status: res.Status, DurationMs: res.DurationMs})
	r.redactReport(res)
	return res, nil
}

func (r *Runner) baseResult() *RunResult {
	res := &RunResult{
		FormatVersion:   ReportFormatVersion,
		SchemaVersion:   r.suite.SchemaVersion,
		SuiteID:         r.suite.ID,
		SuiteName:       r.suite.Name,
		RunID:           r.runID,
		Snapshot:        r.snap,
		Environment:     r.opts.Environment,
		Seed:            r.opts.Seed,
		StepConcurrency: r.opts.StepConcurrency,
		RowConcurrency:  r.opts.RowConcurrency,
		Status:          StatusPassed,
		Rows:            []RowResult{},
	}
	if r.project != nil && r.project.File != nil {
		res.ProjectID = r.project.File.ID
		res.ProjectName = r.project.File.Name
	}
	if r.data != nil && r.data.info != nil {
		info := *r.data.info
		info.Fields = append([]string(nil), info.Fields...)
		res.Dataset = &info
	}
	return res
}

func (r *Runner) preflightFailure(err error) *Failure {
	kind := KindDefinition
	if pe, ok := err.(*preflightError); ok {
		kind = pe.kind
	}
	msg := r.red.RedactString(err.Error())
	return newFailure(FailureConfig, kind, msg)
}

func (r *Runner) skippedRow(dr DataRow, reason string) RowResult {
	row := RowResult{
		Index:  dr.Index,
		Status: StatusSkipped,
		Reason: reason,
		Data:   copyVars(dr.Vars),
		Steps:  []StepResult{},
	}
	for _, sp := range r.order {
		row.Steps = append(row.Steps, r.skippedStep(sp, reason))
	}
	return row
}

func (r *Runner) skippedStep(sp *StepPlan, reason string) StepResult {
	return StepResult{
		ID:       sp.Step.ID,
		Name:     stepName(sp),
		Request:  sp.Entry.RelPath,
		Status:   StatusSkipped,
		Reason:   reason,
		Attempts: []AttemptResult{},
	}
}

func (r *Runner) runRow(ctx context.Context, pos int, dr DataRow) RowResult {
	started := r.clock()
	realStart := time.Now()
	row := RowResult{
		Index:     dr.Index,
		Status:    StatusPassed,
		Data:      copyVars(dr.Vars),
		StartedAt: started,
		Steps:     []StepResult{},
	}
	r.events.send(Event{Kind: EventRowStarted, Row: dr.Index})
	rs := &rowState{
		data:    copyVars(dr.Vars),
		vars:    copyVars(r.suite.Variables),
		outputs: map[string]core.StepOutput{},
		jar:     core.NewCookieJar(fmt.Sprintf("%s/row-%d", r.runID, dr.Index)),
		gen:     newRowGenerator(r.opts.Seed + int64(pos)*7919),
	}

	if err := executeActions(r.suite.Before, rs.vars, rs.data, rs.snapshotOutputs(),
		r.opts.Secrets, rs.gen, r.clock); err != nil {
		f := r.actionFailure(err)
		row.Status = f.StatusFor()
		row.Reason = f.Message
		for _, sp := range r.order {
			row.Steps = append(row.Steps, r.skippedStep(sp, "suite before actions failed"))
		}
		row.DurationMs = time.Since(realStart).Milliseconds()
		r.emitRowFinished(row)
		return row
	}

	results := r.runDAG(ctx, rs, dr.Index)
	for _, sp := range r.order {
		res, ok := results[sp.Step.ID]
		if !ok {
			skipped := r.skippedStep(sp, "not started")
			res = &skipped
		}
		row.Steps = append(row.Steps, *res)
	}

	if ctx.Err() == nil {
		if err := executeActions(r.suite.After, rs.vars, rs.data, rs.snapshotOutputs(),
			r.opts.Secrets, rs.gen, r.clock); err != nil {
			f := r.actionFailure(err)
			if row.Status == StatusPassed {
				row.Status = f.StatusFor()
				row.Reason = "suite after actions: " + f.Message
			} else {
				row.Reason = strings.TrimSpace(row.Reason + "; suite after actions: " + f.Message)
			}
		}
	}

	if row.Status == StatusPassed {
		row.Status = deriveRowStatus(row.Steps, ctx.Err() != nil)
	}
	row.DurationMs = time.Since(realStart).Milliseconds()
	r.emitRowFinished(row)
	return row
}

func (r *Runner) emitRowFinished(row RowResult) {
	r.events.send(Event{Kind: EventRowFinished, Row: row.Index, Status: row.Status, DurationMs: row.DurationMs})
}

// runDAG schedules steps whose dependencies passed, up to StepConcurrency at a
// time. A failed step never produces a dependent success: dependents are
// skipped, and with onFailure=stop no new step starts.
func (r *Runner) runDAG(ctx context.Context, rs *rowState, rowNo int) map[string]*StepResult {
	results := map[string]*StepResult{}
	pending := map[string]bool{}
	for _, sp := range r.order {
		pending[sp.Step.ID] = true
	}
	ch := make(chan StepResult, len(r.order))
	running := 0
	stopNew := false

	for len(pending) > 0 || running > 0 {
		// A stop (or cancellation) must take effect before any scheduling
		// decision in this iteration, even if a concurrent step just finished
		// and set the flag at the end of the previous iteration.
		if stopNew || ctx.Err() != nil {
			for _, sp := range r.order {
				id := sp.Step.ID
				if !pending[id] {
					continue
				}
				reason := "stopped after a failure"
				if ctx.Err() != nil {
					reason = "run canceled before this step started"
				}
				res := r.skippedStep(sp, reason)
				results[id] = &res
				delete(pending, id)
				r.events.send(Event{Kind: EventStepFinished, Row: rowNo, StepID: id, Status: StatusSkipped, Message: reason})
			}
		}
		// Cascade skips for steps whose dependencies did not pass.
		for progress := true; progress; {
			progress = false
			for _, sp := range r.order {
				id := sp.Step.ID
				if !pending[id] {
					continue
				}
				reason := ""
				for _, dep := range sp.Step.Needs {
					depRes := results[dep]
					if depRes == nil {
						reason = ""
						break
					}
					if depRes.Status != StatusPassed {
						reason = fmt.Sprintf("dependency %q %s", dep, depRes.Status)
						break
					}
				}
				if reason == "" {
					continue
				}
				res := r.skippedStep(sp, reason)
				results[id] = &res
				delete(pending, id)
				progress = true
				r.events.send(Event{Kind: EventStepFinished, Row: rowNo, StepID: id, Status: StatusSkipped, Message: reason})
			}
		}

		canceled := ctx.Err() != nil
		if !stopNew && !canceled {
			for running < r.opts.StepConcurrency {
				startedAny := false
				for _, sp := range r.order {
					id := sp.Step.ID
					if !pending[id] || !r.depsPassed(sp, results) {
						continue
					}
					delete(pending, id)
					running++
					startedAny = true
					r.events.send(Event{Kind: EventStepStarted, Row: rowNo, StepID: id})
					go func(sp *StepPlan) {
						ch <- r.runStep(ctx, rs, rowNo, sp)
					}(sp)
					if running >= r.opts.StepConcurrency {
						break
					}
				}
				if !startedAny {
					break
				}
			}
		}
		if stopNew || canceled {
			// Drain steps already launched, but never launch another one.
			if running == 0 {
				// Any pending step was skipped above; wait for nothing.
				break
			}
			res := <-ch
			running--
			results[res.ID] = &res
			r.events.send(Event{
				Kind: EventStepFinished, Row: rowNo, StepID: res.ID, Status: res.Status,
				DurationMs: res.DurationMs, Message: failureMessage(res.Failure),
			})
			continue
		}

		if running == 0 {
			if len(pending) == 0 {
				break
			}
			// Defensive: every remaining step has unfinished dependencies.
			for _, sp := range r.order {
				id := sp.Step.ID
				if !pending[id] {
					continue
				}
				res := StepResult{
					ID: id, Name: stepName(sp), Request: sp.Entry.RelPath,
					Status:  StatusInternalError,
					Reason:  "scheduler could not resolve dependencies",
					Failure: newFailure(FailureInternal, KindOther, "scheduler could not resolve dependencies"),
				}
				results[id] = &res
				delete(pending, id)
			}
			break
		}

		res := <-ch
		running--
		results[res.ID] = &res
		r.events.send(Event{
			Kind: EventStepFinished, Row: rowNo, StepID: res.ID, Status: res.Status,
			DurationMs: res.DurationMs, Message: failureMessage(res.Failure),
		})
		if res.Status != StatusPassed && res.Status != StatusSkipped && r.suite.OnFailure == OnFailureStop {
			stopNew = true
		}
	}
	return results
}

func (r *Runner) depsPassed(sp *StepPlan, results map[string]*StepResult) bool {
	for _, dep := range sp.Step.Needs {
		res := results[dep]
		if res == nil || res.Status != StatusPassed {
			return false
		}
	}
	return true
}

func (r *Runner) runStep(ctx context.Context, rs *rowState, rowNo int, sp *StepPlan) StepResult {
	started := r.clock()
	realStart := time.Now()
	sr := StepResult{
		ID:        sp.Step.ID,
		Name:      stepName(sp),
		Request:   sp.Entry.RelPath,
		Status:    StatusPassed,
		StartedAt: started,
		Attempts:  []AttemptResult{},
	}
	stepVars := copyVars(rs.vars)
	if err := executeActions(sp.Step.Before, stepVars, rs.data, rs.snapshotOutputs(),
		r.opts.Secrets, rs.gen, r.clock); err != nil {
		f := r.actionFailure(err)
		sr.Status = f.StatusFor()
		sr.Failure = f
		sr.Reason = f.Message
		sr.DurationMs = time.Since(realStart).Milliseconds()
		return sr
	}

	outputs := rs.snapshotOutputs()
	resolverVars := core.MergeVars(rs.vars, stepVars)
	resolver := core.NewResolver(resolverVars, r.opts.Secrets).
		WithDataVars(rs.data).
		WithStepOutputs(outputs)
	retry := sp.Step.Retry
	maxAttempts := retry.EffectiveMaxAttempts()
	stepStart := time.Now()
	var lastFail *Failure
	lastStatus := StatusPassed

	for attemptNo := 1; attemptNo <= maxAttempts; attemptNo++ {
		attempt := AttemptResult{Number: attemptNo, StartedAt: r.clock()}
		attemptReal := time.Now()
		if ctx.Err() != nil {
			attempt.Status = StatusCanceled
			attempt.Failure = newFailure(FailureCanceled, KindOther, "run canceled")
			attempt.DurationMs = time.Since(attemptReal).Milliseconds()
			sr.Attempts = append(sr.Attempts, attempt)
			lastFail, lastStatus = attempt.Failure, StatusCanceled
			break
		}

		plan, stepRed, auth, vars, err := r.buildAttemptPlan(ctx, sp, rs, stepVars, outputs)
		if err != nil {
			f := r.redactFailure(stepRed, classifyError(err))
			attempt.Status = statusForFailure(f)
			attempt.Failure = f
			attempt.DurationMs = time.Since(attemptReal).Milliseconds()
			sr.Attempts = append(sr.Attempts, attempt)
			lastFail, lastStatus = f, attempt.Status
			break // config errors are never retried
		}
		execOpts := r.executeOptions(stepRed, auth, rs)
		if r.opts.PreparePlan != nil {
			r.opts.PreparePlan(sp.Entry.Request, plan, stepRed)
		}

		r.events.send(Event{Kind: EventAttemptStarted, Row: rowNo, StepID: sp.Step.ID, Attempt: attemptNo})
		// OAuth/OIDC token acquisition happens before the business request. A
		// failure here never sends the request and is never retried.
		if auth != nil && isOAuthScheme(auth) {
			authReq := AuthRequest{
				StepID:         sp.Step.ID,
				SchemeID:       oauthSchemeID(sp, auth),
				SchemeType:     strings.ToLower(strings.TrimSpace(auth.Type)),
				Scheme:         core.CloneAuthScheme(auth),
				Variables:      vars,
				Secrets:        r.opts.Secrets,
				ExecuteOptions: r.preflightNetworkOptions(stepRed, auth),
				EnvID:          r.opts.Environment,
				Redactor:       stepRed,
			}
			var authRes AuthResult
			var authErr error
			if r.opts.Auth == nil {
				authErr = fmt.Errorf("OAuth/OIDC auth provider is missing")
			} else {
				authRes, authErr = r.opts.Auth.ApplyAuth(ctx, plan, authReq)
			}
			if authErr != nil {
				f := r.redactFailure(stepRed, classifyAuthError(authErr))
				if ctx.Err() != nil {
					f = newFailure(FailureCanceled, KindOther, "run canceled")
				}
				attempt.Status = statusForFailure(f)
				attempt.Failure = f
				attempt.DurationMs = time.Since(attemptReal).Milliseconds()
				sr.Attempts = append(sr.Attempts, attempt)
				lastFail, lastStatus = f, attempt.Status
				r.events.send(Event{
					Kind: EventAttemptFinished, Row: rowNo, StepID: sp.Step.ID, Attempt: attemptNo,
					Status: attempt.Status, DurationMs: attempt.DurationMs, Message: f.Message,
				})
				break
			}
			for _, secret := range authRes.Secrets {
				if secret == "" {
					continue
				}
				stepRed.RegisterSecret(secret)
				r.red.RegisterSecret(secret)
				r.rememberSecrets(secret)
			}
		}
		execRes, execErr := r.executeAttempt(ctx, sp, plan, vars, &core.ResolveOptions{DataVars: rs.data, StepOutputs: outputs}, execOpts, stepRed)
		if execErr != nil {
			if execRes != nil {
				registerResponseSecrets(stepRed, execRes)
				attempt.HTTPStatus = execRes.Status
				attempt.Proto = execRes.Proto
				attempt.ResponseBytes = execRes.BodySize
				attempt.ContentType = execRes.ContentType
				attempt.Truncated = execRes.Truncated
				fillResponseFacts(&attempt, stepRed, execRes)
			}
			f := r.redactFailure(stepRed, classifyError(execErr))
			if ctx.Err() != nil {
				f = newFailure(FailureCanceled, KindOther, "run canceled")
			}
			attempt.Status = statusForFailure(f)
			attempt.Failure = f
			attempt.DurationMs = time.Since(attemptReal).Milliseconds()
			sr.Attempts = append(sr.Attempts, attempt)
			lastFail, lastStatus = f, attempt.Status
			r.events.send(Event{
				Kind: EventAttemptFinished, Row: rowNo, StepID: sp.Step.ID, Attempt: attemptNo,
				Status: attempt.Status, DurationMs: attempt.DurationMs, Message: f.Message,
			})
			if r.shouldRetry(retry, attemptNo, f, 0, stepStart, ctx) {
				if err := r.backoff(ctx, retry, attemptNo, rs.gen); err != nil {
					lastFail, lastStatus = newFailure(FailureCanceled, KindOther, "run canceled"), StatusCanceled
					break
				}
				continue
			}
			break
		}

		registerResponseSecrets(stepRed, execRes)
		ev := &evalContext{
			result:      execRes,
			red:         stepRed,
			resolver:    resolver,
			schema:      r.opts.Schema,
			onSensitive: r.red.RegisterSecret,
		}
		assertionResults, assertFail := evaluateAssertions(sp.Assertions, ev)
		ext := extractAll(sp.Extractions, ev)
		assertionResults = redactAssertionResults(assertionResults, stepRed.RedactString)
		attempt.Status = StatusPassed
		attempt.Assertions = assertionResults
		attempt.HTTPStatus = execRes.Status
		attempt.Proto = execRes.Proto
		attempt.ResponseBytes = execRes.BodySize
		attempt.Truncated = execRes.Truncated
		attempt.ContentType = execRes.ContentType
		fillResponseFacts(&attempt, stepRed, execRes)
		if ext.failure == nil && assertFail == nil {
			for _, r := range ext.results {
				if r.Published {
					attempt.Extracted = append(attempt.Extracted, r.Name)
				}
			}
			attempt.DurationMs = time.Since(attemptReal).Milliseconds()
			sr.Attempts = append(sr.Attempts, attempt)
			rs.publish(sp.Step.ID, ext.outputs)
			sr.Assertions = assertionResults
			sr.Extractions = ext.results
			lastFail, lastStatus = nil, StatusPassed
			for _, extResult := range ext.results {
				if extResult.Published {
					r.events.send(Event{
						Kind: EventExtraction, Row: rowNo, StepID: sp.Step.ID, Attempt: attemptNo,
						Extraction: extResult.Name, Sensitive: extResult.Sensitive, Status: StatusPassed,
					})
				}
			}
			r.events.send(Event{
				Kind: EventAttemptFinished, Row: rowNo, StepID: sp.Step.ID, Attempt: attemptNo,
				Status: StatusPassed, HTTPStatus: execRes.Status, DurationMs: attempt.DurationMs,
			})
			break
		}

		f := assertFail
		if f == nil {
			f = ext.failure
		}
		f = r.redactFailure(stepRed, f)
		attempt.Status = statusForFailure(f)
		attempt.Failure = f
		attempt.DurationMs = time.Since(attemptReal).Milliseconds()
		sr.Assertions = assertionResults
		sr.Extractions = markNotPublished(ext.results)
		sr.Attempts = append(sr.Attempts, attempt)
		lastFail, lastStatus = f, attempt.Status
		if f.Category == FailureConfig || f.Category == FailureInternal {
			r.events.send(Event{
				Kind: EventAttemptFinished, Row: rowNo, StepID: sp.Step.ID, Attempt: attemptNo,
				Status: attempt.Status, HTTPStatus: execRes.Status, DurationMs: attempt.DurationMs, Message: f.Message,
			})
			break
		}
		r.events.send(Event{
			Kind: EventAttemptFinished, Row: rowNo, StepID: sp.Step.ID, Attempt: attemptNo,
			Status: attempt.Status, HTTPStatus: execRes.Status, DurationMs: attempt.DurationMs, Message: f.Message,
		})
		if r.shouldRetry(retry, attemptNo, f, execRes.Status, stepStart, ctx) {
			if err := r.backoff(ctx, retry, attemptNo, rs.gen); err != nil {
				lastFail, lastStatus = newFailure(FailureCanceled, KindOther, "run canceled"), StatusCanceled
				break
			}
			continue
		}
		break
	}

	sr.Status = lastStatus
	sr.Failure = lastFail
	if lastFail != nil {
		sr.Reason = lastFail.Message
	}
	if ctx.Err() != nil && sr.Status == StatusPassed {
		sr.Status = StatusCanceled
		sr.Reason = "run canceled"
		sr.Failure = newFailure(FailureCanceled, KindOther, "run canceled")
	}

	if ctx.Err() == nil {
		if err := executeActions(sp.Step.After, stepVars, rs.data, rs.snapshotOutputs(),
			r.opts.Secrets, rs.gen, r.clock); err != nil {
			f := r.actionFailure(err)
			if sr.Status == StatusPassed {
				sr.Status = f.StatusFor()
				sr.Failure = f
				sr.Reason = "step after actions: " + f.Message
			} else {
				sr.Reason = strings.TrimSpace(sr.Reason + "; step after actions: " + f.Message)
			}
		}
	}
	// Final masking pass over every report-facing field, including failure
	// messages discovered after assertions/extractions.
	sr.Reason = r.red.RedactString(sr.Reason)
	if sr.Failure != nil {
		sr.Failure = r.redactFailure(nil, sr.Failure)
	}
	sr.Assertions = redactAssertionResults(sr.Assertions, r.red.RedactString)
	for i := range sr.Attempts {
		at := &sr.Attempts[i]
		if at.Failure != nil {
			at.Failure = r.redactFailure(nil, at.Failure)
		}
		at.Assertions = redactAssertionResults(at.Assertions, r.red.RedactString)
		for j := range at.Headers {
			at.Headers[j].Value = r.red.RedactString(at.Headers[j].Value)
		}
		at.FinalURL = r.red.RedactString(at.FinalURL)
	}
	sr.DurationMs = time.Since(realStart).Milliseconds()
	return sr
}

// PreloadSecrets lets the caller register additional bound secret values with
// the run-level redactor before any reports are emitted. Names are never
// included; only values are used for masking.
func (r *Runner) PreloadSecrets(values []string) {
	r.rememberSecrets(values...)
	for _, v := range values {
		if v != "" {
			r.red.RegisterSecret(v)
		}
	}
}

func (r *Runner) buildAttemptPlan(ctx context.Context, sp *StepPlan, rs *rowState, stepVars map[string]string, outputs map[string]core.StepOutput) (*core.Plan, *core.Redactor, *core.AuthScheme, map[string]string, error) {
	opts := &core.ResolveOptions{
		RunVars:     rs.vars,
		StepVars:    stepVars,
		DataVars:    rs.data,
		StepOutputs: outputs,
	}
	vars, _, err := r.project.ResolveVarsWith(sp.Entry, r.opts.Environment, opts)
	if err != nil {
		return nil, core.NewRedactor(), nil, nil, err
	}
	eff, err := r.project.ResolveEffectiveAuth(sp.Entry, nil)
	if err != nil {
		return nil, core.NewRedactor(), nil, nil, err
	}
	var auth *core.AuthScheme
	if eff != nil {
		auth = eff.Scheme
	}
	plan, red, err := core.BuildTrackedPlanWithOptions(ctx, sp.Entry.Request, vars, r.opts.Secrets,
		auth, opts, r.opts.PlanOptions)
	if err != nil {
		if red == nil {
			red = core.NewRedactor()
		}
		return nil, red, auth, vars, err
	}
	for _, s := range r.knownSecretSnapshot() {
		red.RegisterSecret(s)
	}
	return plan, red, auth, vars, nil
}

// executeAttempt dispatches one attempt to the protocol adapter or the HTTP
// executor. Preflight guarantees that a non-HTTP request never reaches here
// without an adapter.
func (r *Runner) executeAttempt(ctx context.Context, sp *StepPlan, plan *core.Plan, vars map[string]string, resolve *core.ResolveOptions, opts core.ExecuteOptions, red *core.Redactor) (*core.ExecResult, error) {
	if isNonHTTPProtocol(sp.Entry.Request) {
		if r.opts.Protocol == nil {
			return nil, &core.ConfigError{Err: errors.New("protocol executor is not configured")}
		}
		return r.opts.Protocol.ExecuteProtocol(ctx, ProtocolRequest{
			ResolveOptions: resolve,
			Variables:      vars,
			StepID:         sp.Step.ID,
			Entry:          sp.Entry,
			Plan:           plan,
			ExecuteOptions: opts,
			Redactor:       red,
		})
	}
	if r.opts.Executor == nil {
		return nil, &core.ConfigError{Err: errors.New("executor is not configured")}
	}
	return r.opts.Executor.Execute(ctx, plan, opts)
}

func (r *Runner) executeOptions(red *core.Redactor, auth *core.AuthScheme, rs *rowState) core.ExecuteOptions {
	opts := r.opts.ExecuteOptions
	// Non-interactive run context: a confirm-required production target must
	// fail instead of being auto-approved.
	opts.Interactive = false
	opts.Confirmed = false
	opts.EnvID = r.opts.Environment
	opts.AuthScheme = auth
	opts.Redactor = red
	opts.Secrets = r.opts.Secrets
	if opts.ProjectRoot == "" {
		opts.ProjectRoot = r.opts.PlanOptions.ProjectRoot
	}
	if opts.ProjectFiles == nil {
		opts.ProjectFiles = r.opts.PlanOptions.ProjectFiles
	}
	if opts.LocalFiles == nil {
		opts.LocalFiles = r.opts.PlanOptions.LocalFiles
	}
	if !opts.DisableCookies {
		// Row isolation wins over any template jar: one jar per data row.
		opts.CookieJar = rs.jar
	}
	return opts
}

func (r *Runner) redactFailure(red *core.Redactor, f *Failure) *Failure {
	if f == nil {
		return nil
	}
	cp := *f
	if red != nil {
		cp.Message = red.RedactString(cp.Message)
	}
	cp.Message = r.red.RedactString(cp.Message)
	return &cp
}

func (r *Runner) actionFailure(err error) *Failure {
	msg := r.red.RedactString(err.Error())
	if core.IsConfig(err) || errors.Is(err, context.Canceled) {
		if errors.Is(err, context.Canceled) {
			return newFailure(FailureCanceled, KindOther, msg)
		}
		return newFailure(FailureConfig, KindDefinition, msg)
	}
	return newFailure(FailureInternal, KindOther, msg)
}

func (r *Runner) shouldRetry(p *RetryPolicy, attempt int, f *Failure, httpStatus int, stepStart time.Time, ctx context.Context) bool {
	if p == nil || attempt >= p.EffectiveMaxAttempts() {
		return false
	}
	if ctx.Err() != nil {
		return false
	}
	if f == nil || f.Category == FailureConfig || f.Category == FailureInternal || f.Category == FailureCanceled {
		return false
	}
	if p.DeadlineMs > 0 && time.Since(stepStart) >= time.Duration(p.DeadlineMs)*time.Millisecond {
		return false
	}
	reasons := p.On
	if len(reasons) == 0 {
		reasons = []string{RetryNetwork}
	}
	for _, reason := range reasons {
		switch reason {
		case RetryNetwork:
			if f.Category == FailureRun {
				return true
			}
		case RetryTimeout:
			if f.Kind == KindTimeout {
				return true
			}
		case RetryStatus:
			if httpStatus == 0 {
				continue
			}
			if len(p.Statuses) > 0 {
				for _, s := range p.Statuses {
					if s == httpStatus {
						return true
					}
				}
			} else if httpStatus >= 500 {
				return true
			}
		case RetryAssertion:
			if f.Category == FailureAssertion {
				return true
			}
		}
	}
	return false
}

func (r *Runner) backoff(ctx context.Context, p *RetryPolicy, attempt int, gen *rowGenerator) error {
	delay := time.Duration(p.DelayMs) * time.Millisecond
	if p.Backoff > 1 && attempt > 1 {
		mult := 1.0
		for i := 1; i < attempt; i++ {
			mult *= p.Backoff
		}
		delay = time.Duration(float64(delay) * mult)
	}
	if delay > MaxBackoff {
		delay = MaxBackoff
	}
	if p.JitterMs > 0 {
		delay += time.Duration(gen.randomInt(0, p.JitterMs)) * time.Millisecond
	}
	if delay <= 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			return nil
		}
	}
	t := time.NewTimer(delay)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func fillResponseFacts(attempt *AttemptResult, red *core.Redactor, res *core.ExecResult) {
	if res == nil {
		return
	}
	safe := red.SanitizeExecResult(res)
	if safe == nil {
		return
	}
	attempt.FinalURL = safe.FinalURL
	if res.Proto == "websocket" && json.Valid(safe.Body) {
		attempt.ProtocolFacts = append(json.RawMessage(nil), safe.Body...)
	}
	attempt.Headers = make([]HeaderKV, 0, len(safe.Headers))
	for _, h := range safe.Headers {
		attempt.Headers = append(attempt.Headers, HeaderKV{Name: h[0], Value: h[1]})
	}
}

func statusForFailure(f *Failure) Status {
	if f == nil {
		return StatusPassed
	}
	switch f.Category {
	case FailureConfig:
		return StatusConfigError
	case FailureInternal:
		return StatusInternalError
	case FailureCanceled:
		return StatusCanceled
	default:
		return StatusFailed
	}
}

func (f *Failure) StatusFor() Status { return statusForFailure(f) }

func failureMessage(f *Failure) string {
	if f == nil {
		return ""
	}
	return f.Message
}

func stepName(sp *StepPlan) string {
	if sp.Step.Name != "" {
		return sp.Step.Name
	}
	return sp.Step.ID
}

func copyVars(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func rowFailed(s Status) bool {
	switch s {
	case StatusFailed, StatusConfigError, StatusInternalError, StatusCanceled:
		return true
	}
	return false
}

func deriveRowStatus(steps []StepResult, canceled bool) Status {
	status := StatusPassed
	anyRun := false
	allSkipped := true
	for _, s := range steps {
		if s.Status != StatusSkipped {
			allSkipped = false
		}
		anyRun = true
		switch s.Status {
		case StatusInternalError:
			status = StatusInternalError
		case StatusConfigError:
			if status != StatusInternalError {
				status = StatusConfigError
			}
		case StatusFailed:
			if status != StatusInternalError && status != StatusConfigError {
				status = StatusFailed
			}
		case StatusCanceled:
			if canceled && status == StatusPassed {
				status = StatusCanceled
			}
		}
	}
	if canceled && status == StatusPassed && anyRun {
		status = StatusCanceled
	}
	if status == StatusPassed && allSkipped && len(steps) > 0 {
		return StatusSkipped
	}
	return status
}

func deriveRunStatus(res *RunResult, ctx context.Context) Status {
	if ctx.Err() != nil {
		return StatusCanceled
	}
	if res.Summary.ConfigErrors > 0 {
		return StatusConfigError
	}
	if res.Summary.InternalErrors > 0 {
		return StatusInternalError
	}
	if res.Summary.Failed > 0 {
		return StatusFailed
	}
	if res.Summary.StepsTotal > 0 && res.Summary.Passed == 0 && res.Summary.Skipped == res.Summary.StepsTotal {
		return StatusSkipped
	}
	return StatusPassed
}

func (r *Runner) canceledRow(dr DataRow) RowResult {
	return RowResult{
		Index: dr.Index, Status: StatusCanceled, Reason: "run canceled before preflight",
		Data: copyVars(dr.Vars), StartedAt: r.clock(), Steps: []StepResult{},
	}
}

// registerResponseSecrets records credential material found in response
// headers and cookies with the step redactor. This keeps body assertions from
// echoing a Bearer/Basic token or session cookie that also appeared in a
// header, even when the operator did not declare it as a secret.
func registerResponseSecrets(red *core.Redactor, res *core.ExecResult) {
	if red == nil || res == nil {
		return
	}
	for _, h := range res.Headers {
		name := strings.ToLower(strings.TrimSpace(h[0]))
		if !sensitiveHeaderNames[name] {
			continue
		}
		v := strings.TrimSpace(h[1])
		if v == "" {
			continue
		}
		red.RegisterSecret(v)
		if parts := strings.Fields(v); len(parts) == 2 {
			red.RegisterSecret(parts[1])
		}
		for _, segment := range strings.Split(v, ";") {
			segment = strings.TrimSpace(segment)
			if kv, value, ok := strings.Cut(segment, "="); ok && kv != "" && value != "" {
				red.RegisterSecret(value)
			}
		}
	}
	for _, ck := range res.Cookies {
		if ck.Value != "" {
			red.RegisterSecret(ck.Value)
		}
	}
}
