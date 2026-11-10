// Package protocolrun executes bounded, declarative WebSocket action plans:
// connect -> send(text/binary) -> await/assert -> close.
//
// The package is deliberately transport-light and UI-free. A plan is a saved,
// secret-free definition; templates ({{...}}) are resolved against the current
// environment and secret provider only when the plan runs, so replaying a plan
// never reuses credentials captured from an earlier run.
//
// Every step reports its direction, timeline sequence, receive cursor,
// skipped/retained messages and close facts. Results are never flattened into
// an HTTP-style status: an await timeout, a missing match, a close code and a
// transport failure stay distinct facts.
package protocolrun

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// PlanVersion is the only accepted plan schema version.
const PlanVersion = 1

// Plan bounds. A plan file is user-selected and therefore bounded before
// decoding; binary send files are bounded when compiled.
const (
	MaxPlanBytes       = 1 << 20
	MaxPlanSteps       = 128
	MaxBinaryFileBytes = 1 << 20
)

// Duration is a JSON-friendly time.Duration written as "3s".
type Duration time.Duration

// Std returns the standard library duration.
func (d Duration) Std() time.Duration { return time.Duration(d) }

// MarshalJSON writes the duration using time.Duration's string form.
func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Duration(d).String())
}

// UnmarshalJSON accepts a duration string ("250ms", "3s") only. A bare number
// is rejected so a unit mix-up cannot silently become nanoseconds.
func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("duration must be a string like \"3s\": %w", err)
	}
	if strings.TrimSpace(s) == "" || s == "0" {
		*d = 0
		return nil
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", s, err)
	}
	if v < 0 {
		return fmt.Errorf("duration %q must not be negative", s)
	}
	*d = Duration(v)
	return nil
}

// Action is one plan step kind.
type Action string

const (
	ActionConnect Action = "connect"
	ActionSend    Action = "send"
	ActionAwait   Action = "await"
	ActionClose   Action = "close"
)

// CursorMode selects where an await starts scanning.
type CursorMode string

const (
	// CursorCurrent starts at the executor's current receive cursor (default).
	CursorCurrent CursorMode = "current"
	// CursorStart re-scans from the first retained received message.
	CursorStart CursorMode = "start"
)

// Header is one handshake header in a plan. The value is a template resolved
// at run time; the plan never stores a resolved credential.
type Header struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// MatchSpec selects the message an await step waits for. At least one
// criterion is required; when several are set they must all match (AND).
type MatchSpec struct {
	// JSONPath is a bounded path into a JSON text payload, for example
	// "payload.data.ticks[0].id". Only object keys and array indexes are
	// supported; a missing path never matches.
	JSONPath string `json:"jsonPath,omitempty"`
	// JSONEquals compares the value at JSONPath (or the whole payload when no
	// path is set) with this JSON literal, structurally.
	JSONEquals json.RawMessage `json:"jsonEquals,omitempty"`
	// Contains requires a substring of a text payload.
	Contains string `json:"contains,omitempty"`
	// Equals requires the exact text payload.
	Equals string `json:"equals,omitempty"`
	// BinaryHex requires exact bytes, written as hexadecimal.
	BinaryHex string `json:"binaryHex,omitempty"`
}

// Empty reports whether the match spec has no criterion.
func (m *MatchSpec) Empty() bool {
	return m == nil || (m.JSONPath == "" && len(m.JSONEquals) == 0 && m.Contains == "" && m.Equals == "" && m.BinaryHex == "")
}

// Plan is the saved definition. It is safe to store in a project: no resolved
// secret ever enters it.
type Plan struct {
	Version int    `json:"version"`
	Name    string `json:"name,omitempty"`
	Limits  Limits `json:"limits,omitempty"`
	Steps   []Step `json:"steps"`
}

// Limits bounds the whole run. Zero values select the documented defaults.
type Limits struct {
	TotalTimeout      Duration `json:"totalTimeout,omitempty"`
	MaxMessageBytes   int64    `json:"maxMessageBytes,omitempty"`
	MaxReceivedBytes  int64    `json:"maxReceivedBytes,omitempty"`
	MaxRetainMessages int      `json:"maxRetainMessages,omitempty"`
}

const (
	DefaultTotalTimeout      = 30 * time.Second
	DefaultAwaitTimeout      = 5 * time.Second
	DefaultMaxMessageBytes   = 1 << 20
	DefaultMaxReceivedBytes  = 8 << 20
	DefaultMaxRetainMessages = 256
)

// EffectiveTotalTimeout returns the run deadline (0 disables it only when the
// plan explicitly asks with "0s"; a missing value selects the default).
func (l Limits) EffectiveTotalTimeout() time.Duration {
	if l.TotalTimeout > 0 {
		return l.TotalTimeout.Std()
	}
	return DefaultTotalTimeout
}

// EffectiveMaxMessageBytes returns the per-message bound.
func (l Limits) EffectiveMaxMessageBytes() int64 {
	if l.MaxMessageBytes > 0 {
		return l.MaxMessageBytes
	}
	return DefaultMaxMessageBytes
}

// EffectiveMaxReceivedBytes returns the total-received bound.
func (l Limits) EffectiveMaxReceivedBytes() int64 {
	if l.MaxReceivedBytes > 0 {
		return l.MaxReceivedBytes
	}
	return DefaultMaxReceivedBytes
}

// EffectiveMaxRetainMessages returns the retained-message bound.
func (l Limits) EffectiveMaxRetainMessages() int {
	if l.MaxRetainMessages > 0 {
		return l.MaxRetainMessages
	}
	return DefaultMaxRetainMessages
}

// Step is one action. Only the fields relevant to its Action are read; unused
// fields are ignored and preserved nowhere, so a plan cannot smuggle state
// between actions.
type Step struct {
	Action Action `json:"action"`
	Label  string `json:"label,omitempty"`

	// connect
	URL          string   `json:"url,omitempty"`
	Headers      []Header `json:"headers,omitempty"`
	Subprotocols []string `json:"subprotocols,omitempty"`

	// send: exactly one of Text, BinaryHex or File.
	Text      *string `json:"text,omitempty"`
	BinaryHex string  `json:"binaryHex,omitempty"`
	File      string  `json:"file,omitempty"`
	Sensitive bool    `json:"sensitive,omitempty"`

	// await
	Match         *MatchSpec `json:"match,omitempty"`
	Timeout       Duration   `json:"timeout,omitempty"`
	Cursor        CursorMode `json:"cursor,omitempty"`
	KeepUnmatched bool       `json:"keepUnmatched,omitempty"`
	MaxMessages   int        `json:"maxMessages,omitempty"`

	// close
	Code   int    `json:"code,omitempty"`
	Reason string `json:"reason,omitempty"`
}

// Validate checks the structural rules without touching the network or disk.
func (p *Plan) Validate() error {
	if p == nil {
		return fmt.Errorf("plan is nil")
	}
	if p.Version == 0 {
		p.Version = PlanVersion
	}
	if p.Version != PlanVersion {
		return fmt.Errorf("plan version %d is not supported (want %d)", p.Version, PlanVersion)
	}
	if len(p.Steps) == 0 {
		return fmt.Errorf("plan has no steps")
	}
	if len(p.Steps) > MaxPlanSteps {
		return fmt.Errorf("plan has %d steps; the limit is %d", len(p.Steps), MaxPlanSteps)
	}
	seenConnect := false
	closed := false
	for i, s := range p.Steps {
		if err := s.validate(i, seenConnect, closed); err != nil {
			return err
		}
		switch s.Action {
		case ActionConnect:
			seenConnect = true
		case ActionClose:
			closed = true
		}
	}
	if !seenConnect {
		return fmt.Errorf("plan must contain a connect step")
	}
	return nil
}

func (s Step) validate(i int, seenConnect, closed bool) error {
	where := fmt.Sprintf("step %d", i+1)
	if s.Label != "" {
		where += " (" + s.Label + ")"
	}
	switch s.Action {
	case ActionConnect:
		if seenConnect {
			return fmt.Errorf("%s: only one connect step is supported", where)
		}
		if closed {
			return fmt.Errorf("%s: connect after close", where)
		}
		if strings.TrimSpace(s.URL) == "" {
			return fmt.Errorf("%s: connect requires a url", where)
		}
	case ActionSend:
		if !seenConnect {
			return fmt.Errorf("%s: send before connect", where)
		}
		if closed {
			return fmt.Errorf("%s: send after close", where)
		}
		n := 0
		if s.Text != nil {
			n++
			if len(*s.Text) > MaxBinaryFileBytes {
				return fmt.Errorf("%s: text payload exceeds %d bytes", where, MaxBinaryFileBytes)
			}
		}
		if s.BinaryHex != "" {
			n++
		}
		if s.File != "" {
			n++
		}
		if n != 1 {
			return fmt.Errorf("%s: send requires exactly one of text, binaryHex or file", where)
		}
		if s.BinaryHex != "" && !isHexString(s.BinaryHex) {
			return fmt.Errorf("%s: binaryHex is not valid hexadecimal", where)
		}
	case ActionAwait:
		if !seenConnect {
			return fmt.Errorf("%s: await before connect", where)
		}
		if closed {
			return fmt.Errorf("%s: await after close", where)
		}
		if s.Match.Empty() {
			return fmt.Errorf("%s: await requires a match", where)
		}
		if s.Cursor != "" && s.Cursor != CursorCurrent && s.Cursor != CursorStart {
			return fmt.Errorf("%s: cursor must be %q or %q", where, CursorCurrent, CursorStart)
		}
		if s.MaxMessages < 0 {
			return fmt.Errorf("%s: maxMessages must not be negative", where)
		}
	case ActionClose:
		if !seenConnect {
			return fmt.Errorf("%s: close before connect", where)
		}
		if closed {
			return fmt.Errorf("%s: only one close step is supported", where)
		}
		if s.Code < 0 || s.Code > 4999 {
			return fmt.Errorf("%s: close code %d is out of range", where, s.Code)
		}
	default:
		return fmt.Errorf("%s: unknown action %q", where, s.Action)
	}
	return nil
}

func isHexString(s string) bool {
	if len(s)%2 != 0 {
		return false
	}
	for _, r := range s {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
			return false
		}
	}
	return true
}

// DecodePlan strictly decodes and validates a plan from a reader. Unknown
// fields are rejected so a typo cannot silently disable a bound.
func DecodePlan(r io.Reader) (*Plan, error) {
	if r == nil {
		return nil, fmt.Errorf("plan reader is nil")
	}
	data, err := io.ReadAll(io.LimitReader(r, MaxPlanBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read plan: %w", err)
	}
	if len(data) > MaxPlanBytes {
		return nil, fmt.Errorf("plan exceeds %d bytes", MaxPlanBytes)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var p Plan
	if err := dec.Decode(&p); err != nil {
		return nil, fmt.Errorf("decode plan: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("plan must contain exactly one JSON value")
	}
	if err := p.Validate(); err != nil {
		return nil, err
	}
	return &p, nil
}

// OpenPlan loads and validates a plan file with the size bound applied before
// the whole file is read.
func OpenPlan(path string) (*Plan, error) {
	p, _, err := LoadPlanFile(path)
	return p, err
}

// SavePlan writes a validated plan. The written document contains no resolved
// secrets because templates are resolved only at run time.
func SavePlan(path string, p *Plan) error {
	if err := p.Validate(); err != nil {
		return err
	}
	out, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return fmt.Errorf("encode plan: %w", err)
	}
	if err := os.WriteFile(path, append(out, '\n'), 0o644); err != nil {
		return fmt.Errorf("save plan %q: %w", path, err)
	}
	return nil
}
