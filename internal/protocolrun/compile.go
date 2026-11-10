package protocolrun

import (
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"gorequest/internal/core"
)

// DefaultAwaitMaxMessages bounds how many messages one await may examine when
// the step does not set maxMessages.
const DefaultAwaitMaxMessages = 1000

// ConnectSpec is a compiled connect action (templates already resolved).
type ConnectSpec struct {
	URL          string
	Headers      []Header
	Subprotocols []string
}

// SendPayload is one compiled outbound message. Sensitive payloads must be
// redacted by the transport when retained.
type SendPayload struct {
	Text      bool
	Data      []byte
	Sensitive bool
}

// AwaitSpec is one compiled await action.
type AwaitSpec struct {
	Match         MatchSpec
	Timeout       time.Duration
	FromStart     bool
	KeepUnmatched bool
	MaxMessages   int
}

// CloseSpec is one compiled close action.
type CloseSpec struct {
	Code   int
	Reason string
}

// CompiledStep is one action with resolved values. It may hold secret bytes in
// memory for the duration of the run; it is never serialized.
type CompiledStep struct {
	Action  Action
	Label   string
	Connect *ConnectSpec
	Send    *SendPayload
	Await   *AwaitSpec
	Close   *CloseSpec
}

// CompiledPlan is a plan ready to execute against a Transport.
type CompiledPlan struct {
	Name     string
	Limits   Limits
	Steps    []CompiledStep
	Redactor *core.Redactor
}

// CompileOptions supplies the run-time environment. Resolver is the current
// environment/secret resolver; a replay always recompiles with the current one
// and never reuses values captured from an earlier run.
type CompileOptions struct {
	Resolver  *core.Resolver
	Redactor  *core.Redactor
	OpenFile  func(path string) ([]byte, error)
	MaxBinary int64
}

// Compile validates a saved plan and resolves its templates once for this run.
func Compile(p *Plan, opts CompileOptions) (*CompiledPlan, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	red := opts.Redactor
	if red == nil {
		red = core.NewRedactor()
	}
	maxBinary := opts.MaxBinary
	if maxBinary <= 0 || maxBinary > MaxBinaryFileBytes {
		maxBinary = MaxBinaryFileBytes
	}
	open := opts.OpenFile
	if open == nil {
		open = func(path string) ([]byte, error) { return readBoundedFile(path, maxBinary) }
	}

	out := &CompiledPlan{Name: p.Name, Limits: p.Limits, Redactor: red}
	for i, step := range p.Steps {
		cs := CompiledStep{Action: step.Action, Label: step.Label}
		switch step.Action {
		case ActionConnect:
			url, sensitive, err := resolveTemplate(opts.Resolver, red, step.URL)
			if err != nil {
				return nil, fmt.Errorf("step %d connect url: %w", i+1, err)
			}
			_ = sensitive
			if strings.TrimSpace(url) == "" {
				return nil, fmt.Errorf("step %d connect url is empty after resolution", i+1)
			}
			spec := &ConnectSpec{URL: url, Subprotocols: append([]string(nil), step.Subprotocols...)}
			for _, h := range step.Headers {
				if strings.TrimSpace(h.Name) == "" || strings.ContainsAny(h.Name, " \t\r\n") {
					return nil, fmt.Errorf("step %d connect header name %q is invalid", i+1, h.Name)
				}
				value, _, err := resolveTemplate(opts.Resolver, red, h.Value)
				if err != nil {
					return nil, fmt.Errorf("step %d connect header %q: %w", i+1, h.Name, err)
				}
				spec.Headers = append(spec.Headers, Header{Name: strings.TrimSpace(h.Name), Value: value})
			}
			cs.Connect = spec
		case ActionSend:
			payload := &SendPayload{Sensitive: step.Sensitive}
			switch {
			case step.Text != nil:
				text, sensitive, err := resolveTemplate(opts.Resolver, red, *step.Text)
				if err != nil {
					return nil, fmt.Errorf("step %d send text: %w", i+1, err)
				}
				payload.Text = true
				payload.Data = []byte(text)
				payload.Sensitive = payload.Sensitive || sensitive
			case step.BinaryHex != "":
				data, err := hex.DecodeString(step.BinaryHex)
				if err != nil {
					return nil, fmt.Errorf("step %d send binaryHex: %w", i+1, err)
				}
				if int64(len(data)) > maxBinary {
					return nil, fmt.Errorf("step %d send payload exceeds %d bytes", i+1, maxBinary)
				}
				payload.Data = data
			case step.File != "":
				path, _, err := resolveTemplate(opts.Resolver, red, step.File)
				if err != nil {
					return nil, fmt.Errorf("step %d send file path: %w", i+1, err)
				}
				data, err := open(path)
				if err != nil {
					return nil, fmt.Errorf("step %d send file %q: %w", i+1, path, err)
				}
				if int64(len(data)) > maxBinary {
					return nil, fmt.Errorf("step %d send file exceeds %d bytes", i+1, maxBinary)
				}
				payload.Data = append([]byte(nil), data...)
			default:
				return nil, fmt.Errorf("step %d send has no payload", i+1)
			}
			cs.Send = payload
		case ActionAwait:
			if err := step.Match.Validate(); err != nil {
				return nil, fmt.Errorf("step %d await: %w", i+1, err)
			}
			timeout := step.Timeout.Std()
			if timeout <= 0 {
				timeout = DefaultAwaitTimeout
			}
			maxMessages := step.MaxMessages
			if maxMessages <= 0 {
				maxMessages = DefaultAwaitMaxMessages
			}
			cs.Await = &AwaitSpec{
				Match:         *step.Match,
				Timeout:       timeout,
				FromStart:     step.Cursor == CursorStart,
				KeepUnmatched: step.KeepUnmatched,
				MaxMessages:   maxMessages,
			}
		case ActionClose:
			code := step.Code
			if code == 0 {
				code = 1000
			}
			cs.Close = &CloseSpec{Code: code, Reason: step.Reason}
		default:
			return nil, fmt.Errorf("step %d has unknown action %q", i+1, step.Action)
		}
		out.Steps = append(out.Steps, cs)
	}
	return out, nil
}

// resolveTemplate expands {{...}} through the resolver and registers secret
// values for redaction. A template with no resolver is an error, never a
// literal "{{...}}" sent to the wire.
func resolveTemplate(r *core.Resolver, red *core.Redactor, raw string) (string, bool, error) {
	if !strings.Contains(raw, "{{") {
		return raw, false, nil
	}
	if r == nil {
		return "", false, fmt.Errorf("value contains a template but no variable resolver was supplied")
	}
	tv, err := r.ResolveTracked(raw)
	if err != nil {
		return "", false, fmt.Errorf("%w", err)
	}
	if tv.Sensitive && red != nil {
		// Register both the composed output and each underlying secret value:
		// a server may echo only the token from "Bearer <token>".
		if tv.Value != "" {
			red.RegisterSecret(tv.Value)
		}
		for _, name := range tv.Secrets {
			if name == "" {
				continue
			}
			if sv, err := r.ResolveTracked("{{secret." + name + "}}"); err == nil && sv.Value != "" {
				red.RegisterSecret(sv.Value)
			}
		}
	}
	return tv.Value, tv.Sensitive, nil
}

// readBoundedFile reads a local file with a hard byte bound. Oversize files
// fail before the whole payload is retained.
func readBoundedFile(path string, max int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > max {
		return nil, fmt.Errorf("file exceeds %d bytes", max)
	}
	return data, nil
}
