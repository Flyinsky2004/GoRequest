package diagnostics

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"gopkg.in/yaml.v3"

	"gorequest/internal/core"
)

// DefaultCoreVersion identifies the diagnostics engine contract revision.
const DefaultCoreVersion = "1.0-c-phase1"

// Executor executes one resolved core.Plan. Defaults to core.ExecuteWithOptions.
type Executor interface {
	Execute(ctx context.Context, plan *core.Plan, opts core.ExecuteOptions) (*core.ExecResult, error)
}

// ExecutorFunc adapts a function to the Executor interface.
type ExecutorFunc func(ctx context.Context, plan *core.Plan, opts core.ExecuteOptions) (*core.ExecResult, error)

// Execute calls f(ctx, plan, opts).
func (f ExecutorFunc) Execute(ctx context.Context, plan *core.Plan, opts core.ExecuteOptions) (*core.ExecResult, error) {
	return f(ctx, plan, opts)
}

// Options configures a single diagnostic sampling run.
type Options struct {
	RequestRef     string
	Environment    string
	Config         SamplingConfig
	Secrets        core.SecretProvider
	ExecuteOptions core.ExecuteOptions
	PlanOptions    core.PlanOptions
	Executor       Executor
	Emit           Emitter
	Clock          func() time.Time
	Sleep          func(ctx context.Context, d time.Duration) error
	CoreVersion    string
}

type redactorGuard struct {
	mu sync.Mutex
	r  *core.Redactor
}

func newRedactorGuard() *redactorGuard {
	return &redactorGuard{r: core.NewRedactor()}
}

func (g *redactorGuard) RegisterSecret(s string) {
	if g == nil || s == "" {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.r.RegisterSecret(s)
}

func (g *redactorGuard) RegisterBasicPair(user, pass string) {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.r.RegisterBasicPair(user, pass)
}

func (g *redactorGuard) RegisterSensitiveKey(loc, name string) {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.r.RegisterSensitiveKey(loc, name)
}

func (g *redactorGuard) RedactString(s string) string {
	if g == nil {
		return s
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.r.RedactString(s)
}

func (g *redactorGuard) RedactError(err error) error {
	if g == nil || err == nil {
		return err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.r.RedactError(err)
}

func (g *redactorGuard) cloneRedactor() *core.Redactor {
	g.mu.Lock()
	defer g.mu.Unlock()
	cp := core.NewRedactor()
	for _, s := range g.secretsSnapshotLocked() {
		cp.RegisterSecret(s)
	}
	return cp
}

func (g *redactorGuard) secretsSnapshotLocked() []string {
	return nil
}

// Engine prepares and runs bounded HTTP performance diagnostics for one saved request.
type Engine struct {
	project *core.Project
	opts    Options
	cfg     SamplingConfig
	clock   func() time.Time
	sleep   func(ctx context.Context, d time.Duration) error

	entry        *core.RequestEntry
	vars         map[string]string
	auth         *core.AuthScheme
	dryPlan      *core.Plan
	red          *redactorGuard
	knownSecrets []string
	snapDigest   string
	runID        string

	prepared      bool
	prepareErr    error
	policyBlocked bool
	seq           atomic.Int64
}

// New creates a new diagnostic Engine. Validation and preflight happen in Prepare.
func New(project *core.Project, opts Options) *Engine {
	clock := opts.Clock
	if clock == nil {
		clock = time.Now
	}
	sleepFn := opts.Sleep
	if sleepFn == nil {
		sleepFn = defaultContextSleep
	}
	if strings.TrimSpace(opts.CoreVersion) == "" {
		opts.CoreVersion = DefaultCoreVersion
	}
	return &Engine{
		project: project,
		opts:    opts,
		clock:   clock,
		sleep:   sleepFn,
		red:     newRedactorGuard(),
	}
}

func defaultContextSleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (e *Engine) rememberSecret(val string) {
	if val == "" {
		return
	}
	e.red.RegisterSecret(val)
	for _, existing := range e.knownSecrets {
		if existing == val {
			return
		}
	}
	e.knownSecrets = append(e.knownSecrets, val)
}

func (e *Engine) newSampleRedactor() *core.Redactor {
	r := core.NewRedactor()
	for _, s := range e.knownSecrets {
		r.RegisterSecret(s)
	}
	return r
}

// Prepare validates the sampling configuration, resolves the target request from
// the project snapshot, registers all bound secrets for redaction, and verifies
// target/production policies before any network traffic is sent.
func (e *Engine) Prepare(ctx context.Context) error {
	if e.prepared {
		return e.prepareErr
	}
	e.prepared = true
	e.registerAllBoundSecrets()
	if err := e.prepare(ctx); err != nil {
		e.prepareErr = e.red.RedactError(err)
		return e.prepareErr
	}
	return nil
}

func (e *Engine) registerAllBoundSecrets() {
	if e.opts.Secrets == nil {
		return
	}
	switch sp := e.opts.Secrets.(type) {
	case interface{ SecretNames() []string }:
		for _, name := range sp.SecretNames() {
			if v, ok := e.opts.Secrets.Get(name); ok && v != "" {
				e.rememberSecret(v)
			}
		}
	case core.MapSecrets:
		for name, v := range sp {
			_ = name
			if v != "" {
				e.rememberSecret(v)
			}
		}
	}
	if e.project != nil {
		for _, env := range e.project.Environments {
			if env == nil {
				continue
			}
			for _, name := range env.Secrets {
				if v, ok := e.opts.Secrets.Get(strings.TrimSpace(name)); ok && v != "" {
					e.rememberSecret(v)
				}
			}
		}
	}
	if sec := strings.TrimSpace(e.opts.ExecuteOptions.Proxy.PasswordSecret); sec != "" {
		if v, ok := e.opts.Secrets.Get(sec); ok && v != "" {
			e.rememberSecret(v)
		}
	}
	if e.opts.ExecuteOptions.Proxy.Password != "" {
		e.rememberSecret(e.opts.ExecuteOptions.Proxy.Password)
	}
}

func (e *Engine) prepare(ctx context.Context) error {
	if ctx != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	cfg, err := e.opts.Config.ValidateAndNormalize()
	if err != nil {
		return fmt.Errorf("invalid sampling config: %w", err)
	}
	e.cfg = cfg

	if e.project == nil {
		return errors.New("diagnostics requires a loaded project")
	}
	ref := strings.TrimSpace(e.opts.RequestRef)
	if ref == "" {
		return errors.New("diagnostics requires a target request selection")
	}
	entry := findRequestEntry(e.project, ref)
	if entry == nil || entry.Request == nil || entry.Request.HTTP == nil {
		return fmt.Errorf("request %q was not found in saved project snapshot", ref)
	}
	e.entry = entry

	if e.opts.Environment != "" {
		if _, ok := e.project.Environments[e.opts.Environment]; !ok {
			return fmt.Errorf("environment %q was not found in saved project snapshot", e.opts.Environment)
		}
	}

	effAuth, err := e.project.ResolveEffectiveAuth(entry, entry.Request)
	if err != nil {
		return fmt.Errorf("resolving request auth: %w", err)
	}
	if effAuth != nil {
		e.auth = effAuth.Scheme
	}
	if e.auth != nil && (strings.EqualFold(e.auth.Type, "oauth2") || strings.EqualFold(e.auth.Type, "oidc")) {
		return errors.New("OAuth2/OIDC interactive token acquisition is not supported in batch performance diagnostics; no requests sent")
	}

	vars, _, err := e.project.ResolveVarsWith(entry, e.opts.Environment, nil)
	if err != nil {
		return fmt.Errorf("resolving variables: %w", err)
	}
	e.vars = vars

	// Collect all template references in the request and auth scheme to register
	// secrets and fail early on any undefined variable or missing secret.
	for _, tmplStr := range collectRequestStrings(entry.Request, e.auth, vars) {
		for _, name := range core.TemplateNames(tmplStr) {
			if after, ok := strings.CutPrefix(name, "secret."); ok {
				secName := strings.TrimSpace(after)
				if e.opts.Secrets != nil {
					if v, ok := e.opts.Secrets.Get(secName); ok && v != "" {
						e.rememberSecret(v)
					}
				}
			}
		}
		if undef := core.UndefinedTemplatesWithContext(tmplStr, vars, e.opts.Secrets, nil); len(undef) > 0 {
			return fmt.Errorf("undefined template(s) in request: %s", strings.Join(undef, ", "))
		}
	}

	plan, planRed, err := core.BuildTrackedPlanWithOptions(ctx, entry.Request, vars, e.opts.Secrets, e.auth, nil, e.opts.PlanOptions)
	if err != nil {
		return fmt.Errorf("building request plan: %w", err)
	}
	// Merge secrets tracked during plan construction into the engine guard.
	if e.auth != nil {
		r := core.NewResolver(vars, e.opts.Secrets)
		switch strings.ToLower(strings.TrimSpace(e.auth.Type)) {
		case "bearer":
			if tok, err := r.Resolve(e.auth.Token); err == nil && tok != "" {
				e.rememberSecret(tok)
			}
		case "basic":
			u, _ := r.Resolve(e.auth.Username)
			p, _ := r.Resolve(e.auth.Password)
			e.red.RegisterBasicPair(u, p)
			if p != "" {
				e.rememberSecret(p)
			}
		case "apikey":
			k, _ := r.Resolve(e.auth.Name)
			v, _ := r.Resolve(e.auth.Value)
			loc := core.NormalizeAPIKeyLocation(e.auth.In)
			e.red.RegisterSensitiveKey(loc, k)
			if v != "" {
				e.rememberSecret(v)
			}
		}
	}
	_ = planRed

	// Enforce target and production policy before sending any sample. Diagnostic
	// sampling never auto-confirms production targets or bypasses policy blocks.
	policy := e.opts.ExecuteOptions.EffectiveTargetPolicy()
	hasCreds := e.auth != nil && !strings.EqualFold(e.auth.Type, "none")
	for _, h := range plan.Headers {
		if strings.EqualFold(h[0], "Authorization") || strings.EqualFold(h[0], "Cookie") || strings.EqualFold(h[0], "Proxy-Authorization") {
			hasCreds = true
		}
	}
	dec, polErr := core.EvaluateTarget(policy, core.TargetCheckInput{
		URL:            plan.URL,
		Method:         plan.Method,
		EnvID:          e.opts.Environment,
		HasCredentials: hasCreds,
		Interactive:    false,
		Confirmed:      false,
	})
	if polErr != nil {
		e.policyBlocked = true
		return fmt.Errorf("target policy blocked diagnostic run: %w", polErr)
	}
	if dec != nil && (dec.Outcome == core.DecisionConfirmRequired || dec.Outcome == core.DecisionDeny) {
		e.policyBlocked = true
		return fmt.Errorf("target policy requires confirmation or denies batch diagnostic sampling: %s", strings.Join(dec.Reasons, "; "))
	}

	e.dryPlan = plan
	e.snapDigest = e.computeSnapshotDigest()
	e.runID = computeRunID(e.clock(), e.snapDigest, e.cfg.Seed)
	return nil
}

// Run executes the warmup and measured samples, returning a redacted Report.
// If Prepare fails, Run returns a Report with StatusConfigError or
// StatusPolicyBlocked and sends zero network requests.
func (e *Engine) Run(ctx context.Context) (*Report, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	startedWall := e.clock()
	startedMono := time.Now()

	if err := ctx.Err(); err != nil {
		cfg, _ := e.opts.Config.ValidateAndNormalize()
		e.cfg = cfg
		e.registerAllBoundSecrets()
		rep := e.baseReport(startedWall)
		rep.Status = StatusCanceled
		rep.Canceled = true
		rep.FailureReason = "diagnostic run canceled before start"
		rep.FinishedAt = e.clock()
		return rep, nil
	}

	if err := e.Prepare(ctx); err != nil {
		rep := e.baseReport(startedWall)
		if e.policyBlocked {
			rep.Status = StatusPolicyBlocked
		} else {
			rep.Status = StatusConfigError
		}
		rep.FailureReason = e.red.RedactString(err.Error())
		rep.FinishedAt = e.clock()
		return rep, err
	}

	runCtx := ctx
	var cancel context.CancelFunc
	if e.cfg.Deadline > 0 {
		runCtx, cancel = context.WithTimeout(ctx, e.cfg.Deadline)
		defer cancel()
	}

	rep := e.baseReport(startedWall)
	scopedClient, closeTransport, reuseSupport, reuseNote, err := e.prepareRunTransport(runCtx)
	if err != nil {
		rep.Status = StatusConfigError
		rep.FailureReason = e.red.RedactString(err.Error())
		rep.FinishedAt = e.clock()
		return rep, err
	}
	defer closeTransport()
	rep.ReuseSupport = reuseSupport
	rep.ReuseNote = reuseNote
	if reuseNote != "" {
		rep.Limitations = append(rep.Limitations, reuseNote)
	}

	e.emit(Event{
		Kind:    EventRunStarted,
		Message: fmt.Sprintf("starting %d warmup + %d measured samples (concurrency=%d, strategy=%s)", e.cfg.Warmup, e.cfg.Iterations, e.cfg.Concurrency, e.cfg.ConnectionStrategy),
	})

	// 1. Execute warmup samples sequentially or up to Concurrency.
	if e.cfg.Warmup > 0 {
		rep.WarmupSamples = e.executeBatch(runCtx, ctx, e.cfg.Warmup, true, scopedClient)
	}

	// 2. Execute measured samples if not canceled by caller.
	if ctx.Err() == nil {
		rep.Samples = e.executeBatch(runCtx, ctx, e.cfg.Iterations, false, scopedClient)
	}

	rep.FinishedAt = e.clock()
	rep.TotalElapsedMs = durationToMs(time.Since(startedMono))

	summary, successStats, failureStats, stageStats := AggregateSamples(e.cfg, rep.WarmupSamples, rep.Samples)
	rep.Summary = summary
	rep.SuccessLatency = successStats
	rep.FailureLatency = failureStats
	rep.StageStats = stageStats

	if e.cfg.Gate != nil {
		rep.GateResult = EvaluateGate(e.cfg.Gate, summary, successStats)
	}

	if ctx.Err() == context.Canceled {
		rep.Status = StatusCanceled
		rep.Canceled = true
		if rep.FailureReason == "" {
			rep.FailureReason = "diagnostic run canceled"
		}
	} else if rep.GateResult != nil && !rep.GateResult.Passed {
		rep.Status = StatusGateFailed
		if len(rep.GateResult.Reasons) > 0 {
			rep.FailureReason = strings.Join(rep.GateResult.Reasons, "; ")
		}
	} else {
		rep.Status = StatusCompleted
	}

	e.redactReport(rep)
	e.emit(Event{
		Kind:       EventRunFinished,
		DurationMs: rep.TotalElapsedMs,
		Message:    string(rep.Status),
	})
	return rep, nil
}

func (e *Engine) executeBatch(
	runCtx context.Context,
	userCtx context.Context,
	count int,
	isWarmup bool,
	scopedClient *http.Client,
) []SampleRecord {
	if count <= 0 {
		return nil
	}
	concurrency := e.cfg.Concurrency
	if concurrency < 1 {
		concurrency = 1
	}
	if concurrency > count {
		concurrency = count
	}

	type indexedSample struct {
		pos int
		rec SampleRecord
	}

	results := make([]SampleRecord, count)
	written := make([]bool, count)
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	var mu sync.Mutex

	for i := 0; i < count; i++ {
		if userCtx.Err() != nil || runCtx.Err() != nil {
			break
		}
		if i > 0 && e.cfg.Interval > 0 {
			if err := e.sleep(runCtx, e.cfg.Interval); err != nil {
				break
			}
		}
		select {
		case <-runCtx.Done():
			break
		case sem <- struct{}{}:
		}
		if userCtx.Err() != nil || runCtx.Err() != nil {
			<-sem
			break
		}
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			defer func() { <-sem }()
			rec := e.runOneSample(runCtx, userCtx, idx+1, isWarmup, scopedClient)
			mu.Lock()
			results[idx] = rec
			written[idx] = true
			mu.Unlock()
			e.emit(Event{
				Kind:        EventSampleDone,
				SampleIndex: rec.Index,
				IsWarmup:    rec.IsWarmup,
				Outcome:     rec.Outcome,
				StatusCode:  rec.StatusCode,
				DurationMs:  rec.DurationMs,
				Message:     rec.Error,
			})
		}(i)
	}
	wg.Wait()

	out := make([]SampleRecord, 0, count)
	for i := 0; i < count; i++ {
		if written[i] {
			out = append(out, results[i])
		}
	}
	return out
}

func (e *Engine) runOneSample(
	runCtx context.Context,
	userCtx context.Context,
	sampleIndex int,
	isWarmup bool,
	scopedClient *http.Client,
) SampleRecord {
	sampleStartWall := e.clock()
	red := e.newSampleRedactor()
	if e.auth != nil {
		r := core.NewResolver(e.vars, e.opts.Secrets)
		if strings.EqualFold(e.auth.Type, "apikey") {
			k, _ := r.Resolve(e.auth.Name)
			e.red.RegisterSensitiveKey(core.NormalizeAPIKeyLocation(e.auth.In), k)
			red.RegisterSensitiveKey(core.NormalizeAPIKeyLocation(e.auth.In), k)
		}
	}

	plan, buildRed, buildErr := core.BuildTrackedPlanWithOptions(
		runCtx,
		e.entry.Request,
		e.vars,
		e.opts.Secrets,
		e.auth,
		nil,
		e.opts.PlanOptions,
	)
	if buildErr != nil {
		msg := e.red.RedactString(buildRed.RedactError(buildErr).Error())
		return SampleRecord{
			Index:      sampleIndex,
			IsWarmup:   isWarmup,
			StartedAt:  sampleStartWall,
			Outcome:    OutcomeError,
			DurationMs: 0,
			Error:      msg,
		}
	}

	execOpts := e.opts.ExecuteOptions
	execOpts.Interactive = false
	execOpts.Confirmed = false
	execOpts.EnvID = e.opts.Environment
	execOpts.AuthScheme = e.auth
	execOpts.Redactor = red
	if !execOpts.DisableCookies {
		// Isolate cookies per sample so Set-Cookie from one sample does not
		// mutate shared project CookieJar state.
		execOpts.CookieJar = core.NewCookieJar(fmt.Sprintf("%s-sample-%d", e.runID, sampleIndex))
	}
	if scopedClient != nil {
		execOpts.Client = scopedClient
	}

	executor := e.opts.Executor
	if executor == nil {
		executor = ExecutorFunc(core.ExecuteWithOptions)
	}

	stc, clientTrace := newSampleTraceCollector(time.Now)
	traceCtx := httptrace.WithClientTrace(runCtx, clientTrace)

	t0 := time.Now()
	res, execErr := executor.Execute(traceCtx, plan, execOpts)
	elapsed := time.Since(t0)
	if res != nil && res.Duration > 0 && len(stc.hops) == 0 {
		// Honor synthetic duration when an injected test executor provides one.
		elapsed = res.Duration
	}

	hops, notes := buildSampleTimeline(stc, plan, res, execErr, elapsed, red)
	sanitizedPlan := red.SanitizePlan(plan)
	var sanitizedRes *core.ExecResult
	if res != nil {
		sanitizedRes = red.SanitizeExecResult(res)
	}

	rec := SampleRecord{
		Index:         sampleIndex,
		IsWarmup:      isWarmup,
		StartedAt:     sampleStartWall,
		DurationMs:    durationToMs(elapsed),
		Hops:          hops,
		TimelineNotes: notes,
	}
	if sanitizedPlan != nil {
		rec.FinalURL = sanitizedPlan.URL
	}
	if sanitizedRes != nil {
		rec.StatusCode = sanitizedRes.Status
		rec.Proto = sanitizedRes.Proto
		if sanitizedRes.FinalURL != "" {
			rec.FinalURL = sanitizedRes.FinalURL
		}
		rec.TransferSize = sanitizedRes.TransferSize
		rec.BodySize = sanitizedRes.BodySize
		rec.Truncated = sanitizedRes.Truncated
		rec.RedirectCount = len(sanitizedRes.Redirects)
		if sanitizedRes.Trace != nil && sanitizedRes.Trace.ProxyUsed != "" {
			rec.ProxyUsed = sanitizedRes.Trace.ProxyUsed
		}
	}
	if len(hops) > 0 {
		lastHop := hops[len(hops)-1]
		rec.ReusedConn = lastHop.ReusedConn
		rec.ReusedKnown = lastHop.ReusedKnown
		if rec.ProxyUsed == "" && lastHop.ProxyUsed != "" {
			rec.ProxyUsed = lastHop.ProxyUsed
		}
	}

	rec.Outcome, rec.Error = classifySampleOutcome(userCtx, runCtx, res, execErr, e.cfg.AllowHTTPErrorStatus, red)
	return rec
}

func classifySampleOutcome(
	userCtx context.Context,
	runCtx context.Context,
	res *core.ExecResult,
	execErr error,
	allowHTTPError bool,
	red *core.Redactor,
) (SampleOutcome, string) {
	redact := func(s string) string {
		if red != nil {
			return red.RedactString(s)
		}
		return s
	}
	if execErr != nil {
		errMsg := redact(execErr.Error())
		lower := strings.ToLower(errMsg)
		if (userCtx != nil && errors.Is(userCtx.Err(), context.Canceled)) || strings.Contains(lower, "canceled") {
			return OutcomeCanceled, errMsg
		}
		if (runCtx != nil && errors.Is(runCtx.Err(), context.DeadlineExceeded)) ||
			strings.Contains(lower, "timed out") ||
			strings.Contains(lower, "timeout") {
			return OutcomeTimeout, errMsg
		}
		var netErr net.Error
		if errors.As(execErr, &netErr) && netErr.Timeout() {
			return OutcomeTimeout, errMsg
		}
		return OutcomeError, errMsg
	}
	if res == nil {
		return OutcomeError, "empty execution result"
	}
	if res.Truncated {
		return OutcomeTruncated, "response body truncated at maximum size limit"
	}
	if !allowHTTPError && res.Status >= 400 {
		return OutcomeError, fmt.Sprintf("HTTP status %d", res.Status)
	}
	return OutcomeSuccess, ""
}

// prepareRunTransport builds a run-scoped *http.Client and cleanup function that
// honors ConnectionStrategy while remaining compatible with Core's TLS, proxy,
// and resource cleanup rules. When a transport option requires Core's internal
// per-request transport builder (e.g. SOCKS5 or un-frozen file refs), it returns
// a nil client and ReuseUnmeasurableFallback rather than falsely claiming reuse.
func (e *Engine) prepareRunTransport(ctx context.Context) (*http.Client, func(), ReuseSupportStatus, string, error) {
	noop := func() {}
	opts := e.opts.ExecuteOptions

	if e.opts.Executor != nil {
		if opts.Client != nil {
			if baseTr, ok := opts.Client.Transport.(*http.Transport); ok && baseTr != nil {
				tr := baseTr.Clone()
				tr.DisableKeepAlives = e.cfg.ConnectionStrategy == StrategyFresh
				client := *opts.Client
				client.Transport = tr
				return &client, tr.CloseIdleConnections, ReuseSupported, "", nil
			}
		}
		return opts.Client, noop, ReuseCustomExecutorUnknown, "custom executor injected; connection pooling is managed by the caller's executor", nil
	}

	if opts.Client != nil {
		if baseTr, ok := opts.Client.Transport.(*http.Transport); ok && baseTr != nil {
			tr := baseTr.Clone()
			tr.DisableKeepAlives = e.cfg.ConnectionStrategy == StrategyFresh
			client := *opts.Client
			client.Transport = tr
			return &client, tr.CloseIdleConnections, ReuseSupported, "", nil
		}
		return opts.Client, noop, ReuseCustomExecutorUnknown, "custom http.Client RoundTripper provided; keep-alive reuse cannot be verified by diagnostics transport", nil
	}

	// Check whether any options require Core's unexported per-request builders.
	proxyMode := opts.Proxy.EffectiveMode()
	if proxyMode == core.ProxySOCKS5 {
		if e.cfg.ConnectionStrategy == StrategyKeepAlive {
			return nil, noop, ReuseUnmeasurableFallback, "SOCKS5 proxy uses Core per-request dialer/transport which closes idle connections after each request; cross-sample keep-alive reuse is unmeasurable", nil
		}
		return nil, noop, ReuseSupported, "SOCKS5 proxy uses Core per-request isolated transport (fresh connection per sample)", nil
	}
	if strings.TrimSpace(opts.TLS.CustomCARef) != "" ||
		strings.TrimSpace(opts.TLS.ClientCertRef) != "" ||
		strings.TrimSpace(opts.TLS.ClientKeyRef) != "" {
		if e.cfg.ConnectionStrategy == StrategyKeepAlive {
			return nil, noop, ReuseUnmeasurableFallback, "un-frozen TLS file references use Core per-request transport; freeze PEM material into ExecuteOptions to enable run-scoped keep-alive reuse", nil
		}
		return nil, noop, ReuseSupported, "", nil
	}

	tlsCfg, err := buildDiagnosticsTLSConfig(opts)
	if err != nil {
		return nil, noop, ReuseUnmeasurableFallback, "", err
	}

	dialTimeout := opts.Timeouts.DialTimeout
	if dialTimeout <= 0 {
		dialTimeout = core.DefaultDialTimeout
	}
	tlsTimeout := opts.Timeouts.TLSHandshakeTimeout
	if tlsTimeout <= 0 {
		tlsTimeout = core.DefaultTLSHandshakeTimeout
	}

	baseDialer := &net.Dialer{
		Timeout:   dialTimeout,
		KeepAlive: 30 * time.Second,
	}
	tr := &http.Transport{
		DialContext:           baseDialer.DialContext,
		TLSClientConfig:       tlsCfg,
		TLSHandshakeTimeout:   tlsTimeout,
		ResponseHeaderTimeout: opts.Timeouts.ResponseHeaderTimeout,
		DisableCompression:    true,
		ForceAttemptHTTP2:     true,
		DisableKeepAlives:     e.cfg.ConnectionStrategy == StrategyFresh,
		MaxIdleConns:          32,
		MaxIdleConnsPerHost:   max(8, e.cfg.Concurrency),
		IdleConnTimeout:       30 * time.Second,
	}

	if err := configureDiagnosticsProxy(tr, opts.Proxy, opts.Secrets, e.red); err != nil {
		return nil, noop, ReuseUnmeasurableFallback, "", err
	}
	_ = ctx
	client := &http.Client{Transport: tr}
	return client, tr.CloseIdleConnections, ReuseSupported, "", nil
}

func buildDiagnosticsTLSConfig(opts core.ExecuteOptions) (*tls.Config, error) {
	policy := opts.EffectiveTargetPolicy()
	cfg := opts.TLS
	if cfg.InsecureSkipVerify && policy.EnforceTLSVerify {
		return nil, errors.New("TLS verification cannot be skipped: target policy enforces certificate verification")
	}
	minVer := cfg.MinVersion
	if minVer == 0 {
		minVer = tls.VersionTLS12
	}
	stdCfg := &tls.Config{
		MinVersion:         minVer,
		InsecureSkipVerify: cfg.InsecureSkipVerify,
		ServerName:         strings.TrimSpace(cfg.ServerName),
	}
	useSys := cfg.SystemCAEnabled()
	if !useSys || len(cfg.CustomCAPEM) > 0 {
		var pool *x509.CertPool
		if useSys {
			if sysPool, err := x509.SystemCertPool(); err == nil && sysPool != nil {
				pool = sysPool
			} else {
				pool = x509.NewCertPool()
			}
		} else {
			pool = x509.NewCertPool()
		}
		if len(cfg.CustomCAPEM) > 0 {
			if !pool.AppendCertsFromPEM(cfg.CustomCAPEM) {
				return nil, errors.New("custom CA bundle contains no valid PEM certificates")
			}
		}
		stdCfg.RootCAs = pool
	}
	if len(cfg.ClientCertPEM) > 0 || len(cfg.ClientKeyPEM) > 0 {
		if len(cfg.ClientCertPEM) == 0 || len(cfg.ClientKeyPEM) == 0 {
			return nil, errors.New("mTLS requires both client certificate and client private key")
		}
		pair, err := tls.X509KeyPair(cfg.ClientCertPEM, cfg.ClientKeyPEM)
		if err != nil {
			return nil, fmt.Errorf("invalid mTLS client key pair: %w", err)
		}
		stdCfg.Certificates = []tls.Certificate{pair}
	}
	return stdCfg, nil
}

func configureDiagnosticsProxy(tr *http.Transport, cfg core.ProxyConfig, secrets core.SecretProvider, red *redactorGuard) error {
	mode := cfg.EffectiveMode()
	switch mode {
	case core.ProxyNone:
		tr.Proxy = nil
		return nil
	case core.ProxySystem:
		noProxy := strings.TrimSpace(cfg.NoProxy)
		tr.Proxy = func(req *http.Request) (*url.URL, error) {
			if core.ShouldBypassProxy(req.URL, noProxy) {
				return nil, nil
			}
			return http.ProxyFromEnvironment(req)
		}
		return nil
	case core.ProxyHTTP:
		rawURL := strings.TrimSpace(cfg.URL)
		if rawURL == "" {
			return errors.New("proxy URL is required when proxy mode is http")
		}
		if !strings.Contains(rawURL, "://") {
			rawURL = "http://" + rawURL
		}
		parsed, err := url.Parse(rawURL)
		if err != nil || parsed.Hostname() == "" {
			return fmt.Errorf("invalid proxy URL %q", cfg.URL)
		}
		user := strings.TrimSpace(cfg.Username)
		pass := cfg.Password
		if parsed.User != nil {
			if user == "" {
				user = parsed.User.Username()
			}
			if p, ok := parsed.User.Password(); ok && pass == "" {
				pass = p
			}
		}
		if pass == "" && strings.TrimSpace(cfg.PasswordSecret) != "" {
			if secrets == nil {
				return fmt.Errorf("proxy password secret %q requires a secret provider", cfg.PasswordSecret)
			}
			v, ok := secrets.Get(strings.TrimSpace(cfg.PasswordSecret))
			if !ok {
				return fmt.Errorf("proxy password secret %q is not bound", cfg.PasswordSecret)
			}
			pass = v
		}
		if red != nil && (user != "" || pass != "") {
			red.RegisterBasicPair(user, pass)
			if pass != "" {
				red.RegisterSecret(pass)
			}
		}
		cleanURL := *parsed
		if user != "" || pass != "" {
			cleanURL.User = url.UserPassword(user, pass)
			basic := "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+pass))
			tr.ProxyConnectHeader = http.Header{"Proxy-Authorization": []string{basic}}
		} else {
			cleanURL.User = nil
		}
		noProxy := strings.TrimSpace(cfg.NoProxy)
		tr.Proxy = func(req *http.Request) (*url.URL, error) {
			if core.ShouldBypassProxy(req.URL, noProxy) {
				return nil, nil
			}
			cp := cleanURL
			return &cp, nil
		}
		return nil
	default:
		return fmt.Errorf("unsupported proxy mode %q", mode)
	}
}

func (e *Engine) baseReport(startedAt time.Time) *Report {
	rep := &Report{
		FormatVersion:   ReportFormatVersion,
		ScopeDisclaimer: ScopeDisclaimer,
		RunID:           e.runID,
		RequestPath:     strings.TrimSpace(e.opts.RequestRef),
		SnapshotDigest:  e.snapDigest,
		Environment:     e.opts.Environment,
		CoreVersion:     e.opts.CoreVersion,
		StartedAt:       startedAt,
		Config:          e.cfg,
		ProxyMode:       string(e.opts.ExecuteOptions.Proxy.EffectiveMode()),
		ReuseSupport:    ReuseSupported,
		Summary: SampleSummary{
			ConfiguredIterations: e.cfg.Iterations,
			ConfiguredWarmup:     e.cfg.Warmup,
			StatisticalMethod:    StatisticalMethodNearestRank,
		},
	}
	if e.project != nil && e.project.File != nil {
		rep.ProjectID = e.project.File.ID
		rep.ProjectName = e.project.File.Name
	}
	if e.entry != nil {
		rep.RequestPath = e.entry.RelPath
		if e.entry.Request != nil {
			rep.RequestID = e.entry.Request.ID
			rep.RequestName = e.entry.Request.Name
			if e.entry.Request.HTTP != nil {
				rep.Method = strings.ToUpper(strings.TrimSpace(e.entry.Request.HTTP.Method))
				if rep.Method == "" {
					rep.Method = http.MethodGet
				}
				rep.TargetURL = e.red.RedactString(e.entry.Request.HTTP.URL)
			}
		}
	}
	if e.dryPlan != nil {
		red := e.newSampleRedactor()
		sp := red.SanitizePlan(e.dryPlan)
		rep.Method = sp.Method
		rep.TargetURL = sp.URL
	}
	return rep
}

func (e *Engine) redactReport(rep *Report) {
	if rep == nil {
		return
	}
	rep.TargetURL = e.red.RedactString(rep.TargetURL)
	rep.FailureReason = e.red.RedactString(rep.FailureReason)
	rep.ReuseNote = e.red.RedactString(rep.ReuseNote)
	for i := range rep.Limitations {
		rep.Limitations[i] = e.red.RedactString(rep.Limitations[i])
	}
	redactSamples := func(list []SampleRecord) {
		for i := range list {
			list[i].FinalURL = e.red.RedactString(list[i].FinalURL)
			list[i].ProxyUsed = e.red.RedactString(list[i].ProxyUsed)
			list[i].Error = e.red.RedactString(list[i].Error)
			for j := range list[i].TimelineNotes {
				list[i].TimelineNotes[j] = e.red.RedactString(list[i].TimelineNotes[j])
			}
			for h := range list[i].Hops {
				hop := &list[i].Hops[h]
				hop.URL = e.red.RedactString(hop.URL)
				hop.FromURL = e.red.RedactString(hop.FromURL)
				hop.RemoteAddr = e.red.RedactString(hop.RemoteAddr)
				hop.ProxyUsed = e.red.RedactString(hop.ProxyUsed)
				for d := range hop.DialAttempts {
					hop.DialAttempts[d].RemoteAddr = e.red.RedactString(hop.DialAttempts[d].RemoteAddr)
					hop.DialAttempts[d].Error = e.red.RedactString(hop.DialAttempts[d].Error)
				}
				for k, sm := range hop.Stages {
					sm.Note = e.red.RedactString(sm.Note)
					sm.Error = e.red.RedactString(sm.Error)
					hop.Stages[k] = sm
				}
				for l := range hop.Limitations {
					hop.Limitations[l] = e.red.RedactString(hop.Limitations[l])
				}
			}
		}
	}
	redactSamples(rep.WarmupSamples)
	redactSamples(rep.Samples)
}

func (e *Engine) emit(ev Event) {
	if e.opts.Emit == nil {
		return
	}
	ev.Seq = e.seq.Add(1)
	ev.Time = e.clock()
	ev.RunID = e.runID
	ev.Message = e.red.RedactString(ev.Message)
	e.opts.Emit(ev)
}

func (e *Engine) computeSnapshotDigest() string {
	h := sha256.New()
	if e.project != nil && e.project.File != nil {
		fmt.Fprintf(h, "project:%s:%d\n", e.project.File.ID, e.project.File.SchemaVersion)
	}
	if e.entry != nil {
		reqDigest := e.entry.BaselineDigest
		if reqDigest == "" && e.entry.Request != nil {
			if b, err := yaml.Marshal(e.entry.Request); err == nil {
				sum := sha256.Sum256(b)
				reqDigest = hex.EncodeToString(sum[:])
			}
		}
		fmt.Fprintf(h, "request:%s:%s:%s\n", e.entry.RelPath, e.entry.Request.ID, reqDigest)
	}
	fmt.Fprintf(h, "environment:%s\n", e.opts.Environment)
	if e.project != nil && e.opts.Environment != "" {
		if envEntry, ok := e.project.EnvEntries[e.opts.Environment]; ok && envEntry != nil {
			fmt.Fprintf(h, "envDigest:%s:%s\n", envEntry.RelPath, envEntry.BaselineDigest)
		} else if env, ok := e.project.Environments[e.opts.Environment]; ok && env != nil {
			keys := make([]string, 0, len(env.Variables))
			for k := range env.Variables {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				fmt.Fprintf(h, "envVar:%s=%s\n", k, env.Variables[k])
			}
		}
	}
	if e.auth != nil {
		fmt.Fprintf(h, "auth:%s:%s:%s\n", e.auth.ID, e.auth.Type, e.auth.In)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func computeRunID(now time.Time, snap string, seed int64) string {
	raw := fmt.Sprintf("%d-%s-%d", now.UTC().UnixNano(), snap, seed)
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:8])
}

func findRequestEntry(p *core.Project, ref string) *core.RequestEntry {
	if p == nil {
		return nil
	}
	norm := strings.TrimPrefix(strings.ReplaceAll(strings.TrimSpace(ref), "\\", "/"), "./")
	for _, entry := range p.Requests {
		if entry == nil || entry.Request == nil {
			continue
		}
		entryRel := strings.TrimPrefix(strings.ReplaceAll(strings.TrimSpace(entry.RelPath), "\\", "/"), "./")
		if entryRel == norm || entry.Request.ID == ref {
			return entry
		}
	}
	return nil
}

func collectRequestStrings(req *core.Request, auth *core.AuthScheme, vars map[string]string) []string {
	var out []string
	if req != nil {
		if req.HTTP != nil {
			out = append(out, req.HTTP.URL)
			for _, kv := range req.HTTP.Query {
				if kv.IsEnabled() {
					out = append(out, kv.Name, kv.Value)
				}
			}
			for _, kv := range req.HTTP.Headers {
				if kv.IsEnabled() {
					out = append(out, kv.Name, kv.Value)
				}
			}
			if req.HTTP.Body != nil {
				out = appendYAMLStrings(out, req.HTTP.Body.Value)
				out = append(out, req.HTTP.Body.File)
			}
		}
		out = append(out, req.Timeout)
		for _, v := range req.Variables {
			out = append(out, v)
		}
	}
	if auth != nil {
		out = append(out, auth.Token, auth.Username, auth.Password, auth.Name, auth.Value)
	}
	for _, v := range vars {
		out = append(out, v)
	}
	return out
}

func appendYAMLStrings(out []string, n *yaml.Node) []string {
	if n == nil {
		return out
	}
	if n.Kind == yaml.ScalarNode {
		out = append(out, n.Value)
	}
	for _, c := range n.Content {
		out = appendYAMLStrings(out, c)
	}
	return out
}
