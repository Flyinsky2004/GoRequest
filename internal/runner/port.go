package runner

// This file defines the optional execution ports that let the desktop inject
// OAuth/OIDC token acquisition and non-HTTP protocol executors without
// introducing any GUI or platform dependency into the pure-Go runner.
// The wire contract is documented in docs/contracts/F01-runner-extension.md.

import (
	"context"
	"strings"
	"time"

	"gorequest/internal/core"
)

// AuthRequest is one step's non-interactive OAuth/OIDC material. It contains no
// runtime token; the adapter resolves or refreshes the token through
// core.OAuthService.
type AuthRequest struct {
	StepID         string
	SchemeID       string
	SchemeType     string
	Scheme         *core.AuthScheme
	Variables      map[string]string
	Secrets        core.SecretProvider
	ExecuteOptions core.ExecuteOptions
	EnvID          string
	// Redactor is a per-step scratch redactor. The adapter registers any secret
	// it resolves so preflight errors can be scrubbed before they enter a report.
	Redactor *core.Redactor
}

// AuthResult reports the runtime credentials the runner must register with its
// step and run-level redactors. Secrets are never serialized.
type AuthResult struct {
	Secrets   []string
	ExpiresAt time.Time
}

// AuthProvider is the optional OAuth/OIDC port. Implementations must never open
// a browser, wait for user interaction or start a loopback listener from the
// runner. PreflightAuth must perform no network I/O.
type AuthProvider interface {
	PreflightAuth(ctx context.Context, req AuthRequest) error
	// ApplyAuth may call the token endpoint for refresh/client-credentials.
	// It must not overwrite an Authorization header the plan already carries and
	// must register secret values in req.Redactor.
	ApplyAuth(ctx context.Context, plan *core.Plan, req AuthRequest) (AuthResult, error)
}

// ProtocolRequest is the input shared by protocol preflight and execution.
type ProtocolRequest struct {
	ResolveOptions *core.ResolveOptions
	CheckTemplates func([]string) error
	Variables      map[string]string
	StepID         string
	Entry          *core.RequestEntry
	Plan           *core.Plan
	ExecuteOptions core.ExecuteOptions
	Redactor       *core.Redactor
}

// ProtocolExecutor is the optional non-HTTP execution port (GraphQL,
// WebSocket, ...). The runner owns assertions, extraction, DAG scheduling,
// retry, row isolation and reporting; an adapter returns the same ExecResult
// facts an HTTP attempt would produce.
type ProtocolExecutor interface {
	Supports(req *core.Request) bool
	// PreflightProtocol must validate everything it can before any business
	// request is sent.
	PreflightProtocol(ctx context.Context, req ProtocolRequest) error
	ExecuteProtocol(ctx context.Context, req ProtocolRequest) (*core.ExecResult, error)
}

// isOAuthScheme reports whether the scheme needs the AuthProvider port.
func isOAuthScheme(auth *core.AuthScheme) bool {
	if auth == nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(auth.Type)) {
	case "oauth2", "oidc":
		return true
	}
	return false
}

// isNonHTTPProtocol reports whether the request must be handled by a protocol
// executor. An empty Protocol keeps the historical HTTP behavior.
func isNonHTTPProtocol(req *core.Request) bool {
	if req == nil {
		return false
	}
	proto := strings.TrimSpace(req.Protocol)
	return proto != "" && !strings.EqualFold(proto, core.ProtocolHTTP)
}
