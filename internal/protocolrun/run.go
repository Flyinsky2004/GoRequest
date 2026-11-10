package protocolrun

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorequest/internal/stream"
)

// MaxResultPayloadBytes bounds the payload preview retained in a step result.
// The match decision always used the full message; only the report copy is cut.
const MaxResultPayloadBytes = 64 << 10

// RunState is the overall outcome of one plan run.
type RunState string

const (
	StatePassed   RunState = "passed"
	StateFailed   RunState = "failed"
	StateCanceled RunState = "canceled"
	StateTimedOut RunState = "timed_out"
)

// SentFact reports one outbound message.
type SentFact struct {
	Seq   uint64
	Bytes int
}

// Message is one inbound payload with its session timeline sequence.
type Message struct {
	Seq  uint64
	Time time.Time
	Text bool
	Data []byte
}

// Transport is the protocol-facing boundary the runner executes against. The
// WebSocket adapter in this package implements it; tests use fakes.
type Transport interface {
	Connect(ctx context.Context, spec ConnectSpec) error
	Send(ctx context.Context, payload SendPayload) (SentFact, error)
	Receive(ctx context.Context) (Message, error)
	Close(code int, reason string) error
	// CloseFacts reports the terminal close code/reason observed on the wire
	// (0 when the connection never reached a close frame).
	CloseFacts() (code int, reason string)
}

// SkippedFact records one unmatched message examined by an await.
type SkippedFact struct {
	Seq      uint64
	Bytes    int
	Retained bool // true when the timeout policy kept it for a later step
}

// StepResult preserves the facts of one action: direction, sequence, cursor,
// skipped/retained messages, failure and close reason.
type StepResult struct {
	Index         int
	Action        Action
	Label         string
	State         RunState
	Direction     stream.Direction
	StartedAt     time.Time
	EndedAt       time.Time
	Duration      time.Duration
	CursorBefore  int64
	CursorAfter   int64
	SentSeq       uint64
	ReceivedSeq   uint64
	ReceivedBytes int
	Payload       []byte // redacted, bounded preview
	Truncated     bool
	Detail        string
	Skipped       []SkippedFact
	Error         string
	CloseCode     int
	CloseReason   string
}

// Result is the complete outcome of one run. Transport facts (close code and
// reason) stay separate from per-step facts; nothing is collapsed into an
// HTTP-like status.
type Result struct {
	Name          string
	State         RunState
	Error         string
	FailedStep    int // 0-based index, -1 when no step failed
	Steps         []StepResult
	StartedAt     time.Time
	EndedAt       time.Time
	CloseCode     int
	CloseReason   string
	SentCount     int
	ReceivedCount int
	BytesSent     int64
	BytesReceived int64
}

// Run executes a compiled plan against a transport. It always returns a
// Result; structural or transport failures are captured as explicit facts.
// The runner never reconnects, never retries a step and never ignores an
// unmatched message count bound.
func Run(ctx context.Context, plan *CompiledPlan, tr Transport) *Result {
	res := &Result{State: StatePassed, FailedStep: -1, StartedAt: time.Now().UTC()}
	if plan == nil {
		return finishResult(res, StateFailed, "plan is nil")
	}
	res.Name = plan.Name
	if tr == nil {
		return finishResult(res, StateFailed, "transport is nil")
	}
	runCtx := ctx
	if runCtx == nil {
		runCtx = context.Background()
	}
	if total := plan.Limits.EffectiveTotalTimeout(); total > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(runCtx, total)
		defer cancel()
	}
	r := &runner{plan: plan, tr: tr, res: res, started: res.StartedAt}

	closed := false
	for i := range plan.Steps {
		step := &plan.Steps[i]
		sr := StepResult{
			Index:        i,
			Action:       step.Action,
			Label:        step.Label,
			State:        StatePassed,
			Direction:    stream.DirNone,
			StartedAt:    time.Now().UTC(),
			CursorBefore: r.cursor,
			CursorAfter:  r.cursor,
		}
		stop := false
		switch step.Action {
		case ActionConnect:
			r.runConnect(runCtx, step, &sr)
		case ActionSend:
			r.runSend(runCtx, step, &sr)
		case ActionAwait:
			r.runAwait(runCtx, step, &sr)
		case ActionClose:
			closed = true
			r.runClose(step, &sr)
		}
		sr.EndedAt = time.Now().UTC()
		sr.Duration = sr.EndedAt.Sub(sr.StartedAt)
		sr.CursorAfter = r.cursor
		res.Steps = append(res.Steps, sr)
		if sr.State != StatePassed {
			res.FailedStep = i
			res.State = StateFailed
			res.Error = sr.Error
			stop = true
		}
		if stop {
			break
		}
	}

	if res.State == StateFailed {
		r.abort()
		if code, reason := tr.CloseFacts(); code != 0 {
			res.CloseCode, res.CloseReason = code, reason
		}
	} else if !closed {
		if err := tr.Close(1000, "plan complete"); err != nil {
			res.Error = r.redact(fmt.Sprintf("automatic close failed: %v", err))
			res.State = StateFailed
		} else {
			res.CloseCode, res.CloseReason = 1000, "plan complete"
		}
	}
	if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		res.State = StateTimedOut
		if res.Error == "" {
			res.Error = "total plan deadline exceeded"
		}
	} else if errors.Is(runCtx.Err(), context.Canceled) {
		res.State = StateCanceled
		if res.Error == "" {
			res.Error = "plan canceled"
		}
	}
	res.EndedAt = time.Now().UTC()
	return res
}

func finishResult(res *Result, state RunState, msg string) *Result {
	res.State = state
	res.Error = msg
	res.EndedAt = time.Now().UTC()
	return res
}

type runner struct {
	plan    *CompiledPlan
	tr      Transport
	res     *Result
	started time.Time
	buf     []Message
	pulled  int64
	cursor  int64
	bytes   int64
}

func (r *runner) runConnect(ctx context.Context, step *CompiledStep, sr *StepResult) {
	if step.Connect == nil {
		r.fail(sr, "connect step has no spec")
		return
	}
	if err := r.tr.Connect(ctx, *step.Connect); err != nil {
		r.fail(sr, r.classify(ctx, err))
		return
	}
	sr.Detail = fmt.Sprintf("connected to %s", step.Connect.URL)
}

func (r *runner) runSend(ctx context.Context, step *CompiledStep, sr *StepResult) {
	if step.Send == nil {
		r.fail(sr, "send step has no payload")
		return
	}
	sr.Direction = stream.DirOut
	fact, err := r.tr.Send(ctx, *step.Send)
	if err != nil {
		r.fail(sr, r.classify(ctx, err))
		return
	}
	sr.SentSeq = fact.Seq
	sr.ReceivedBytes = 0
	r.res.SentCount++
	r.res.BytesSent += int64(fact.Bytes)
	kind := "binary"
	if step.Send.Text {
		kind = "text"
	}
	sr.Detail = fmt.Sprintf("sent %s message (%d bytes, seq %d)", kind, fact.Bytes, fact.Seq)
}

func (r *runner) runAwait(ctx context.Context, step *CompiledStep, sr *StepResult) {
	if step.Await == nil {
		r.fail(sr, "await step has no spec")
		return
	}
	start := r.cursor
	if step.Await.FromStart {
		start = 0
	}
	cursor := start
	scanned := 0
	var skipped []SkippedFact

	awaitCtx, cancel := context.WithTimeout(ctx, step.Await.Timeout)
	defer cancel()

	for {
		if err := awaitCtx.Err(); err != nil {
			r.awaitTimeout(sr, step, start, cursor, skipped, err)
			return
		}
		if cursor >= r.pulled {
			msg, err := r.receive(awaitCtx)
			if err != nil {
				if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
					r.awaitTimeout(sr, step, start, cursor, skipped, err)
					return
				}
				r.fail(sr, r.classify(ctx, err))
				return
			}
			r.buf = append(r.buf, msg)
			r.pulled++
		}
		msg := r.buf[cursor]
		cursor++
		scanned++
		ok, detail := step.Await.Match.Match(msg)
		if ok {
			if cursor > r.cursor {
				r.cursor = cursor
			}
			sr.Direction = stream.DirIn
			sr.ReceivedSeq = msg.Seq
			sr.ReceivedBytes = len(msg.Data)
			sr.Payload, sr.Truncated = r.payloadPreview(msg)
			sr.Detail = fmt.Sprintf("matched after %d message(s): %s", scanned, detail)
			sr.Skipped = skipped
			return
		}
		skipped = append(skipped, SkippedFact{Seq: msg.Seq, Bytes: len(msg.Data)})
		if scanned >= step.Await.MaxMessages {
			r.cursor = cursor
			sr.Skipped = skipped
			r.fail(sr, fmt.Sprintf("examined %d messages without a match (maxMessages)", scanned))
			return
		}
	}
}

// awaitTimeout records the timeout fact and applies the step's unmatched
// policy: skip consumes every examined message, keep leaves the cursor where
// the step started so a later step can still observe them.
func (r *runner) awaitTimeout(sr *StepResult, step *CompiledStep, start, cursor int64, skipped []SkippedFact, err error) {
	for i := range skipped {
		skipped[i].Retained = step.Await.KeepUnmatched
	}
	sr.Skipped = skipped
	if !step.Await.KeepUnmatched && cursor > r.cursor {
		r.cursor = cursor
	}
	_ = start
	reason := "await timeout"
	if errors.Is(err, context.Canceled) {
		reason = "await canceled"
	}
	sr.Error = fmt.Sprintf("%s after examining %d message(s)", reason, len(skipped))
	sr.State = StateFailed
	r.res.Error = sr.Error
}

func (r *runner) runClose(step *CompiledStep, sr *StepResult) {
	if step.Close == nil {
		r.fail(sr, "close step has no spec")
		return
	}
	err := r.tr.Close(step.Close.Code, step.Close.Reason)
	r.res.CloseCode = step.Close.Code
	r.res.CloseReason = step.Close.Reason
	sr.CloseCode = step.Close.Code
	sr.CloseReason = step.Close.Reason
	if err != nil {
		r.fail(sr, r.redact(fmt.Sprintf("close failed: %v", err)))
		return
	}
	sr.Detail = fmt.Sprintf("closed with code %d", step.Close.Code)
}

func (r *runner) abort() {
	_ = r.tr.Close(1011, "plan failed")
}

// receive pulls one message under the per-step or run context.
func (r *runner) receive(ctx context.Context) (Message, error) {
	msg, err := r.tr.Receive(ctx)
	if err != nil {
		return Message{}, err
	}
	if int64(len(msg.Data)) > r.plan.Limits.EffectiveMaxMessageBytes() {
		return Message{}, fmt.Errorf("received message exceeds %d bytes", r.plan.Limits.EffectiveMaxMessageBytes())
	}
	r.bytes += int64(len(msg.Data))
	r.res.ReceivedCount++
	r.res.BytesReceived += int64(len(msg.Data))
	if r.bytes > r.plan.Limits.EffectiveMaxReceivedBytes() {
		return Message{}, fmt.Errorf("total received bytes exceed %d", r.plan.Limits.EffectiveMaxReceivedBytes())
	}
	if len(r.buf) >= r.plan.Limits.EffectiveMaxRetainMessages() {
		return Message{}, fmt.Errorf("retained message bound of %d reached; the plan did not consume messages", r.plan.Limits.EffectiveMaxRetainMessages())
	}
	return msg, nil
}

// payloadPreview returns a redacted, size-bounded copy for reports.
func (r *runner) payloadPreview(msg Message) ([]byte, bool) {
	data := msg.Data
	if len(data) > MaxResultPayloadBytes {
		data = data[:MaxResultPayloadBytes]
		return r.redactBytes(data, msg.Text), true
	}
	return r.redactBytes(data, msg.Text), false
}

func (r *runner) redactBytes(data []byte, text bool) []byte {
	if !text {
		return []byte("***")
	}
	if r.plan.Redactor == nil {
		return append([]byte(nil), data...)
	}
	return []byte(r.plan.Redactor.RedactString(string(data)))
}

func (r *runner) redact(s string) string {
	if r.plan.Redactor == nil {
		return s
	}
	return r.plan.Redactor.RedactString(s)
}

func (r *runner) fail(sr *StepResult, msg string) {
	sr.State = StateFailed
	sr.Error = r.redact(msg)
	r.res.Error = sr.Error
}

// classify keeps cancellation/deadline facts distinct from ordinary failures.
func (r *runner) classify(ctx context.Context, err error) string {
	if ctx != nil {
		switch {
		case errors.Is(ctx.Err(), context.DeadlineExceeded):
			return "total plan deadline exceeded: " + r.redact(err.Error())
		case errors.Is(ctx.Err(), context.Canceled):
			return "plan canceled: " + r.redact(err.Error())
		}
	}
	return r.redact(err.Error())
}
