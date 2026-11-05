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
	issues := append([]Issue(nil), p.Issues...)
	ids := make([]string, 0, len(p.Requests))
	for id := range p.Requests {
		ids = append(ids, id)
	}
	sortStrings(ids)
	for _, id := range ids {
		e := p.Requests[id]
		issues = append(issues, validateRequest(e.Request, e.RelPath, p)...)
	}
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
	if r.Protocol != "" && r.Protocol != "http" {
		add("protocol", fmt.Sprintf("protocol %q is not supported in the MVP (only http)", r.Protocol))
	}
	if r.HTTP == nil {
		add("http-missing", "request has no http block")
		return issues
	}
	if strings.ContainsAny(r.HTTP.Method, " \t\r\n") {
		add("method", fmt.Sprintf("invalid method %q", r.HTTP.Method))
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
		switch strings.ToLower(strings.TrimSpace(r.HTTP.Body.Type)) {
		case "json", "text", "xml":
		case "":
			add("body-type", "body has no type")
		default:
			add("body-type", fmt.Sprintf("unsupported body type %q (MVP supports json, text, xml)", r.HTTP.Body.Type))
		}
		if r.HTTP.Body.File != "" {
			add("body-file", "body.file is not supported yet")
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
