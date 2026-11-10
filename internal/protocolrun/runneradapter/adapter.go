// Package runneradapter bridges bounded protocol execution to F01's
// runner.ProtocolExecutor port. It is delivered as an integration patch and is
// not compiled in the F02 branch, because the runner port lives in the tree
// after F01 is merged.
package runneradapter

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"gorequest/internal/core"
	"gorequest/internal/protocolrun"
	"gorequest/internal/runner"
	"gorequest/internal/stream"
	gows "gorequest/internal/websocket"
)

// Executor implements runner.ProtocolExecutor for:
//   - WebSocket requests that carry a protocolrun.PlanRef (bounded action plans)
//   - GraphQL requests (query/mutation), which reuse the tracked HTTP plan the
//     runner already built (PlanOptions.AllowGraphQL must be enabled).
//
// GraphQL subscriptions are a live UI feature; the runner does not execute them.
type Executor struct{}

// New returns a ready protocol executor.
func New() *Executor { return &Executor{} }

// Supports reports whether this executor handles the request. A WebSocket
// request is only supported when it carries a plan reference.
func (e *Executor) Supports(req *core.Request) bool {
	if req == nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(req.Protocol)) {
	case core.ProtocolWebSocket:
		_, ok := protocolrun.PlanRefFromRequest(req)
		return ok
	case core.ProtocolGraphQL:
		return true
	}
	return false
}

// PreflightProtocol performs zero network I/O: it decodes and validates the
// plan, checks the stored digest and compiles templates against the current
// environment so a bad plan fails before any connection is opened.
func (e *Executor) PreflightProtocol(ctx context.Context, req runner.ProtocolRequest) error {
	if req.Entry == nil || req.Entry.Request == nil {
		return &core.ConfigError{Err: fmt.Errorf("protocol request has no request entry")}
	}
	r := req.Entry.Request
	switch strings.ToLower(strings.TrimSpace(r.Protocol)) {
	case core.ProtocolGraphQL:
		if req.Plan == nil {
			return &core.ConfigError{Err: fmt.Errorf("graphql request has no plan")}
		}
		if ref, ok := core.GraphQLSchemaRefFromRequest(r); ok {
			model, err := core.LoadGraphQLSchemaProjectFile(ctx, req.ExecuteOptions.ProjectFiles, ref.Path)
			if err != nil {
				return &core.ConfigError{Err: err}
			}
			if ref.Digest != "" && ref.Digest != model.Digest {
				return &core.ConfigError{Err: fmt.Errorf("GraphQL schema changed since review")}
			}
			_, diagnostics, err := model.ValidateDocument(r.GraphQL.Document, r.GraphQL.OperationName)
			if err != nil {
				return err
			}
			if len(diagnostics) != 0 {
				return &core.ConfigError{Err: fmt.Errorf("GraphQL schema: %s", diagnostics[0].String())}
			}
		}
		return nil
	case core.ProtocolWebSocket:
		_, _, _, err := e.compile(ctx, req, true)
		return err
	}
	return &core.ConfigError{Err: fmt.Errorf("protocol %q is not supported", r.Protocol)}
}

// ExecuteProtocol runs one bounded plan (WebSocket) or the tracked HTTP plan
// (GraphQL). It never reconnects and never retries.
func (e *Executor) ExecuteProtocol(ctx context.Context, req runner.ProtocolRequest) (*core.ExecResult, error) {
	if req.Entry == nil || req.Entry.Request == nil {
		return nil, &core.ConfigError{Err: fmt.Errorf("protocol request has no request entry")}
	}
	r := req.Entry.Request
	switch strings.ToLower(strings.TrimSpace(r.Protocol)) {
	case core.ProtocolGraphQL:
		if req.Plan == nil {
			return nil, &core.ConfigError{Err: fmt.Errorf("graphql request has no plan")}
		}
		result, err := core.ExecuteWithOptions(ctx, req.Plan, req.ExecuteOptions)
		if err != nil {
			return result, err
		}
		classified := core.ClassifyGraphQLResult(result)
		allowErrors, _ := r.Extensions["graphqlAllowErrors"].(bool)
		if classified.InvalidJSON || (!classified.DataPresent && !classified.HasErrors()) || (classified.HasErrors() && !allowErrors) {
			return result, &core.RunError{Err: fmt.Errorf("GraphQL response failed: %s", core.GraphQLResultSummary(classified))}
		}
		return result, nil
	case core.ProtocolWebSocket:
		// fall through to the bounded plan path
	default:
		return nil, &core.ConfigError{Err: fmt.Errorf("protocol %q is not supported", r.Protocol)}
	}

	plan, compiled, red, err := e.compile(ctx, req, false)
	if err != nil {
		return nil, err
	}
	base := gows.Options{
		EnableCompression: false,
		Deadlines: stream.DeadlinePolicy{
			Connect:   req.ExecuteOptions.Timeouts.DialTimeout,
			Handshake: req.ExecuteOptions.Timeouts.TLSHandshakeTimeout,
			Message:   gows.DefaultMessageTimeout,
			// The plan's own total deadline is enforced inside Run's context.
			Total: 0,
		},
		Proxy:           req.ExecuteOptions.Proxy,
		TLS:             req.ExecuteOptions.TLS,
		TargetPolicy:    req.ExecuteOptions.TargetPolicy,
		HasTargetPolicy: req.ExecuteOptions.HasTargetPolicy,
		EnvID:           req.ExecuteOptions.EnvID,
		// The runner is a non-interactive context: a confirm-required target
		// fails instead of being auto-approved.
		Interactive: false,
		Confirmed:   false,
		Secrets:     req.ExecuteOptions.Secrets,
		LocalFiles:  req.ExecuteOptions.LocalFiles,
		ProjectRoot: req.ExecuteOptions.ProjectRoot,
		Redactor:    red,
		CookieJar:   req.ExecuteOptions.CookieJar,
	}
	tr := protocolrun.NewWebSocketTransport(base, plan.Limits)
	res := protocolrun.Run(ctx, compiled, tr)
	exec := resultToExecResult(res)

	if res.State == protocolrun.StatePassed {
		return exec, nil
	}
	return exec, &core.RunError{Err: fmt.Errorf("%s", describeFailure(res, red))}
}

// compile loads, digests, validates and (unless preflightOnly) compiles a
// WebSocket plan. Preflight skips compilation of binary file payloads content
// but still validates structure and templates.
func (e *Executor) compile(ctx context.Context, req runner.ProtocolRequest, preflightOnly bool) (*protocolrun.Plan, *protocolrun.CompiledPlan, *core.Redactor, error) {
	ref, ok := protocolrun.PlanRefFromRequest(req.Entry.Request)
	if !ok {
		return nil, nil, nil, &core.ConfigError{Err: fmt.Errorf("websocket request has no plan reference")}
	}
	plan, digest, err := protocolrun.ReadPlanFromProject(ctx, req.ExecuteOptions.ProjectFiles, ref.Path)
	if err != nil {
		return nil, nil, nil, &core.ConfigError{Err: err}
	}
	if ref.Digest != "" && digest != ref.Digest {
		return nil, nil, nil, &core.ConfigError{Err: fmt.Errorf("plan %q changed since it was reviewed", ref.Path)}
	}
	red := req.Redactor
	if red == nil {
		red = core.NewRedactor()
	}
	if req.CheckTemplates != nil {
		raw, err := json.Marshal(plan)
		if err != nil {
			return nil, nil, red, err
		}
		if err := req.CheckTemplates([]string{string(raw)}); err != nil {
			return nil, nil, red, err
		}
	}
	resolver := core.NewResolver(req.Variables, req.ExecuteOptions.Secrets)
	if req.ResolveOptions != nil {
		resolver.WithDataVars(req.ResolveOptions.DataVars).WithStepOutputs(req.ResolveOptions.StepOutputs)
	}
	compiled, err := protocolrun.Compile(plan, protocolrun.CompileOptions{
		Resolver:  resolver,
		Redactor:  red,
		OpenFile:  openFileFor(ctx, req.ExecuteOptions.ProjectFiles),
		MaxBinary: protocolrun.MaxBinaryFileBytes,
	})
	if err != nil {
		return nil, nil, nil, &core.ConfigError{Err: err}
	}
	for _, step := range compiled.Steps {
		if step.Connect == nil {
			continue
		}
		opts := &gows.Options{URL: step.Connect.URL, TargetPolicy: req.ExecuteOptions.TargetPolicy, HasTargetPolicy: req.ExecuteOptions.HasTargetPolicy, EnvID: req.ExecuteOptions.EnvID}
		for _, h := range step.Connect.Headers {
			opts.Headers = append(opts.Headers, [2]string{h.Name, h.Value})
		}
		decision, err := gows.PromptRequiredBy(opts)
		if err != nil {
			return nil, nil, red, &core.ConfigError{Err: err}
		}
		if decision.Outcome == core.DecisionConfirmRequired {
			return nil, nil, red, &core.ConfigError{Err: fmt.Errorf("WebSocket target requires interactive confirmation")}
		}
	}
	_ = preflightOnly
	return plan, compiled, red, nil
}

// openFileFor is confined to the frozen project snapshot. A project plan
// cannot grant itself access to arbitrary host paths.
func openFileFor(ctx context.Context, opener core.ProjectFileOpener) func(string) ([]byte, error) {
	return func(p string) ([]byte, error) {
		clean, err := core.ValidateProjectRelPath(p)
		if err != nil {
			return nil, err
		}
		if opener == nil {
			return nil, fmt.Errorf("project file snapshot is unavailable")
		}
		f, err := opener.OpenProjectFile(ctx, clean)
		if err != nil {
			return nil, err
		}
		defer f.Reader.Close()
		return io.ReadAll(io.LimitReader(f.Reader, protocolrun.MaxBinaryFileBytes+1))
	}
}

// planFacts is the JSON body an assertion can target. It keeps the protocol end
// facts (close code/reason, per-step cursor/sequence/direction) separate and
// never flattens them into an HTTP status.
type planFacts struct {
	State         string     `json:"state"`
	Name          string     `json:"name,omitempty"`
	Error         string     `json:"error,omitempty"`
	FailedStep    int        `json:"failedStep"`
	CloseCode     int        `json:"closeCode"`
	CloseReason   string     `json:"closeReason,omitempty"`
	Sent          int        `json:"sent"`
	Received      int        `json:"received"`
	BytesSent     int64      `json:"bytesSent"`
	BytesReceived int64      `json:"bytesReceived"`
	DurationMs    int64      `json:"durationMs"`
	Steps         []stepFact `json:"steps"`
}

type stepFact struct {
	Index         int    `json:"index"`
	Action        string `json:"action"`
	Label         string `json:"label,omitempty"`
	State         string `json:"state"`
	Direction     string `json:"direction,omitempty"`
	CursorBefore  int64  `json:"cursorBefore"`
	CursorAfter   int64  `json:"cursorAfter"`
	SentSeq       uint64 `json:"sentSeq"`
	ReceivedSeq   uint64 `json:"receivedSeq"`
	ReceivedBytes int    `json:"receivedBytes"`
	Error         string `json:"error,omitempty"`
	CloseCode     int    `json:"closeCode"`
	CloseReason   string `json:"closeReason,omitempty"`
}

// resultToExecResult maps a plan result to transport facts. Status stays 0: a
// WebSocket plan has no HTTP status, so it is never fabricated.
func resultToExecResult(res *protocolrun.Result) *core.ExecResult {
	facts := planFacts{
		State:         string(res.State),
		Name:          res.Name,
		Error:         res.Error,
		FailedStep:    res.FailedStep,
		CloseCode:     res.CloseCode,
		CloseReason:   res.CloseReason,
		Sent:          res.SentCount,
		Received:      res.ReceivedCount,
		BytesSent:     res.BytesSent,
		BytesReceived: res.BytesReceived,
		DurationMs:    res.EndedAt.Sub(res.StartedAt).Milliseconds(),
	}
	for _, s := range res.Steps {
		facts.Steps = append(facts.Steps, stepFact{
			Index:         s.Index,
			Action:        string(s.Action),
			Label:         s.Label,
			State:         string(s.State),
			Direction:     string(s.Direction),
			CursorBefore:  s.CursorBefore,
			CursorAfter:   s.CursorAfter,
			SentSeq:       s.SentSeq,
			ReceivedSeq:   s.ReceivedSeq,
			ReceivedBytes: s.ReceivedBytes,
			Error:         s.Error,
			CloseCode:     s.CloseCode,
			CloseReason:   s.CloseReason,
		})
	}
	body, _ := json.Marshal(facts)
	return &core.ExecResult{
		Proto:       "websocket",
		Duration:    res.EndedAt.Sub(res.StartedAt),
		Body:        body,
		BodySize:    int64(len(body)),
		ContentType: "application/json",
	}
}

// describeFailure renders the essential end facts into an error message, because
// F01 currently discards the ExecResult when an attempt returns an error.
func describeFailure(res *protocolrun.Result, red *core.Redactor) string {
	msg := fmt.Sprintf("websocket plan %q did not pass: state=%s", res.Name, res.State)
	if res.FailedStep >= 0 {
		msg += fmt.Sprintf(" failedStep=%d", res.FailedStep)
	}
	if res.CloseCode != 0 || res.CloseReason != "" {
		msg += fmt.Sprintf(" close=%d %q", res.CloseCode, res.CloseReason)
	}
	if res.Error != "" {
		msg += ": " + res.Error
	}
	if red != nil {
		msg = red.RedactString(msg)
	}
	return msg
}
