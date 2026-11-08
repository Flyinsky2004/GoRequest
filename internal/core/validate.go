package core

import (
	"fmt"
	"strings"
	"time"
)

// Issue is one validation finding. Level is "error" or "warning"; a project
// with any error must not run.
type Issue struct {
	Level   string `json:"level"`
	Code    string `json:"code"`
	Path    string `json:"path"`
	Message string `json:"message"`
}

func (i Issue) String() string {
	return fmt.Sprintf("[%s] %s: %s (%s)", i.Level, i.Path, i.Message, i.Code)
}

// Errors counts error-level issues in a slice.
func ErrorCount(issues []Issue) int {
	n := 0
	for _, i := range issues {
		if i.Level == "error" {
			n++
		}
	}
	return n
}

// ValidateProject gathers load-time issues and validates every request. It
// performs no network access.
func ValidateProject(p *Project) []Issue {
	return validateProjectFrom(p.Issues, p)
}

// revalidate recomputes issues after an in-memory mutation, starting from the
// original load-time findings so they are never duplicated.
func (p *Project) revalidate() {
	p.Issues = validateProjectFrom(p.loadIssues, p)
}

// validateProjectFrom appends per-request validation of p to a copy of base.
func validateProjectFrom(base []Issue, p *Project) []Issue {
	issues := append([]Issue(nil), base...)
	ids := make([]string, 0, len(p.Requests))
	for id := range p.Requests {
		ids = append(ids, id)
	}
	sortStrings(ids)
	for _, id := range ids {
		e := p.Requests[id]
		issues = append(issues, validateRequest(e.Request, e.RelPath, p)...)
	}
	issues = append(issues, p.ValidateIdentity()...)
	return issues
}

func validateRequest(r *Request, path string, p *Project) []Issue {
	var issues []Issue
	add := func(code, msg string) {
		issues = append(issues, Issue{Level: "error", Code: code, Path: path, Message: msg})
	}
	if r.SchemaVersion != SupportedSchemaVersion {
		add("schema-version", fmt.Sprintf("schemaVersion %d unsupported (this build reads %d)", r.SchemaVersion, SupportedSchemaVersion))
	}
	if strings.TrimSpace(r.ID) == "" {
		add("request-id", "request id is empty")
	}
	if strings.TrimSpace(r.Name) == "" {
		add("request-name", "request name is empty")
	}
	proto := strings.TrimSpace(r.Protocol)
	websocket := proto == ProtocolWebSocket
	if proto != "" && proto != ProtocolHTTP && !websocket {
		add("protocol", fmt.Sprintf("protocol %q is not supported (supported: http, websocket)", r.Protocol))
	}
	if r.HTTP == nil {
		add("http-missing", "request has no http block")
		return issues
	}
	if websocket {
		// The shared HTTPDef.Method field only carries the "WS" display tag for
		// WebSocket requests; it is never sent as an HTTP method.
		if m := strings.TrimSpace(r.HTTP.Method); m != "" && !strings.EqualFold(m, WebSocketMethod) {
			add("method", fmt.Sprintf("websocket request method must be %q, got %q", WebSocketMethod, r.HTTP.Method))
		}
	} else if strings.TrimSpace(r.HTTP.Method) != "" {
		if err := ValidateHTTPMethod(r.HTTP.Method); err != nil {
			add("method", err.Error())
		}
	}
	if strings.TrimSpace(r.HTTP.URL) == "" {
		add("url", "request url is empty")
	}
	for _, kv := range r.HTTP.Headers {
		if kv.IsEnabled() && strings.TrimSpace(kv.Name) == "" {
			add("header-name", "enabled header has an empty name")
		}
	}
	if r.HTTP.Body != nil {
		if err := ValidateBody(r.HTTP.Body); err != nil {
			add("body", err.Error())
		}
	}
	if websocket && r.WS != nil {
		for _, sp := range r.WS.Subprotocols {
			if strings.TrimSpace(sp) == "" {
				add("ws-subprotocol", "websocket subprotocol is empty")
				break
			}
		}
	}

	if r.Timeout != "" {
		if d, err := time.ParseDuration(r.Timeout); err != nil {
			add("timeout", fmt.Sprintf("invalid timeout %q: %v", r.Timeout, err))
		} else if d <= 0 {
			add("timeout", "timeout must be positive")
		}
	}
	if r.Auth != nil {
		switch r.Auth.Mode {
		case "", "none":
		case "ref":
			if _, ok := p.AuthSchemes[r.Auth.Ref]; !ok {
				add("auth-ref", fmt.Sprintf("auth %q not found", r.Auth.Ref))
			}
		case "inline":
			if err := validateAuthScheme(r.Auth.Inline); err != nil {
				add("auth-inline", err.Error())
			}
		default:
			add("auth-mode", fmt.Sprintf("unknown auth mode %q", r.Auth.Mode))
		}
	}
	return issues
}
