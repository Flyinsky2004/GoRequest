// Package websocket implements GoRequest's manual WebSocket debugging session
// on top of the shared bounded stream contract (internal/stream).
//
// Scope (P01 phase 1): a single client session per dialog with explicit user
// actions only — connect, send text/binary, ping, close. There is no silent
// reconnect, no auto-connect, no automatic keep-alive ping and no infinite
// buffering: connectivity facts, per-message events and resource bounds are
// all recorded in the shared stream log.
//
// Security contract (B04/B05 reuse, mandatory):
//
//   - core.EvaluateTarget is checked BEFORE any network I/O. WebSocket
//     sessions are evaluated as mutating traffic (method "WEBSOCKET") because
//     a live socket can carry writes, so production read-only/confirm/deny
//     semantics apply. ws:// is mapped to http:// for policy evaluation.
//   - TLS verification follows the merged target policy: InsecureSkipVerify
//     is rejected before dialing while MergedTargetPolicy.EnforceTLSVerify
//     holds; custom CA and mTLS material come from machine-local slots or
//     .gorequest-confined files exactly like core's HTTP transport, with the
//     private key forbidden from repository paths.
//   - Proxy settings are honored, never silently dropped: system
//     (environment), none (direct), HTTP/HTTPS (local CONNECT tunnel) and
//     SOCKS5 (gorilla's bundled RFC 1928/1929 client) with credentials resolved
//     from machine-local secrets (PasswordSecret) all work. An unsupported
//     proxy scheme fails before dialing.
//   - Any option that cannot be honored safely is a pre-network config error.
//
// UI-independent by design: this package has no MyGo/UI imports. The desktop
// modal (internal/desktop/websocket*.go) renders its state; A01/P02/P03
// consume Session.Events() (the *stream.Log) without any UI.
package websocket

import (
	"net/http"
	"strings"
	"time"

	"gorequest/internal/core"
	"gorequest/internal/stream"
)

// State is the session lifecycle state reported to UI and reports.
type State string

const (
	StateDisconnected State = "disconnected"
	StateConnecting   State = "connecting"
	StateOpen         State = "open"
	StateClosing      State = "closing"
	StateClosed       State = "closed"
	StateError        State = "error"
)

// Bounds for the session's own resources. Every bound is explicit and never
// grows in the background: the stream ring keeps ringCapacity events, the
// message inbox keeps inboxCapacity full payloads, and a single receive
// message may not exceed MaxMessageBytes.
const (
	DefaultMaxMessageBytes  int64 = 1 << 20 // 1 MiB per inbound/outbound frame message
	DefaultInboxCapacity    int   = 200     // full retained payloads for detail/save
	DefaultHandshakeTimeout       = 15 * time.Second
	DefaultConnectTimeout         = 10 * time.Second
	DefaultMessageTimeout         = 10 * time.Second
)

// Options is the full input for one Session. The desktop layer resolves
// variables/templates before filling it; this package only consumes concrete
// values and never resolves templates itself, so no secret can leak into
// option strings beyond what the caller explicitly resolved.
type Options struct {
	// URL is the WebSocket endpoint (ws:// or wss:// only, no userinfo).
	URL string
	// Headers are extra handshake request headers ("Name","Value" pairs).
	// Reserved upgrade headers (Upgrade, Connection, Sec-WebSocket-*) are
	// rejected. Values must be fully resolved by the caller; any value the
	// caller marked SensitiveValues implies credentials for target policy.
	Headers [][2]string
	// Subprotocols lists the offered WebSocket subprotocols (e.g.
	// graphql-transport-ws) in preference order.
	Subprotocols []string
	// EnableCompression negotiates permessage-deflate (RFC 7692).
	EnableCompression bool

	// Deadlines declares connect/handshake/message/total bounds and
	// zero-value defaults through Effective* helpers below.
	Deadlines stream.DeadlinePolicy

	// MaxMessageBytes caps a single inbound or outbound message (gorilla
	// SetReadLimit on receive; oversize frames fail the read with a clear
	// fact instead of buffering). DefaultMaxMessageBytes when <= 0.
	MaxMessageBytes int64
	// MaxStreamEvents bounds the shared stream ring (stream.Options.Capacity).
	MaxStreamEvents int
	// MaxEventBytes bounds each retained stream event payload preview.
	MaxEventBytes int
	// InboxCapacity bounds retained full message payloads for detail/save.
	InboxCapacity int

	// Proxy is the core proxy configuration. All modes are honored; a proxy
	// requirement that cannot be mapped safely fails before dialing.
	Proxy core.ProxyConfig
	// TLS is the core TLS configuration (system/custom CA, mTLS, SNI,
	// InsecureSkipVerify subject to policy).
	TLS core.TLSConfig

	// TargetPolicy (merged device + project) is always evaluated when
	// HasTargetPolicy is set; a nil policy uses core.MergeTargetPolicy's
	// defaults.
	TargetPolicy    core.MergedTargetPolicy
	HasTargetPolicy bool
	// EnvID feeds production environment classification in policy checks.
	EnvID string
	// Interactive/Confirmed mirror core.ExecuteOptions semantics:
	// Interactive=false turns confirm-required into an immediate deny;
	// Confirmed is the user's explicit one-time approval of THIS exact
	// target (never persisted, never reused after a reconnect).
	Interactive bool
	Confirmed   bool

	// HasCredentials forces the "credentials over non-loopback plain HTTP"
	// policy guard even when headers are not trivially recognizable.
	HasCredentials bool

	// Secrets resolves Proxy.PasswordSecret and TLS slot material; nil
	// default nil-safe: a required secret fails with a config error.
	Secrets core.SecretProvider
	// LocalFiles resolves machine-local TLS/CA slot bindings.
	LocalFiles core.LocalFileProvider
	// ProjectRoot confines project-relative CA/cert references.
	ProjectRoot string
	// ProjectFiles overrides the .gorequest file opener (tests inject in-memory
	// stores); when nil, ProjectRoot's DirFileOpener is used.
	ProjectFiles core.ProjectFileOpener

	// Redactor owns secret registration and sensitive-value scrubbing; the
	// Session creates a private one when nil. The dialer registers proxy
	// credentials and mTLS private keys into it.
	Redactor *core.Redactor
}

// EffectiveMaxMessageBytes returns the per-message bound.
func (o *Options) EffectiveMaxMessageBytes() int64 {
	if o != nil && o.MaxMessageBytes > 0 {
		return o.MaxMessageBytes
	}
	return DefaultMaxMessageBytes
}

// EffectiveInboxCapacity returns the retained-full-payload bound.
func (o *Options) EffectiveInboxCapacity() int {
	if o != nil && o.InboxCapacity > 0 {
		return o.InboxCapacity
	}
	return DefaultInboxCapacity
}

// EventOptions maps the session bounds onto stream.Options.
func (o *Options) EventOptions() stream.Options {
	maxEventBytes := 0
	if o != nil {
		maxEventBytes = o.MaxEventBytes
	}
	capacity := 0
	if o != nil {
		capacity = o.MaxStreamEvents
	}
	return stream.Options{Capacity: capacity, MaxPayloadBytes: maxEventBytes}
}

// EffectiveConnect, EffectiveHandshake and EffectiveMessage return the
// active deadline (documented default when zero).
func (o *Options) EffectiveConnect() time.Duration {
	if o != nil && o.Deadlines.Connect > 0 {
		return o.Deadlines.Connect
	}
	return DefaultConnectTimeout
}

func (o *Options) EffectiveHandshake() time.Duration {
	if o != nil && o.Deadlines.Handshake > 0 {
		return o.Deadlines.Handshake
	}
	return DefaultHandshakeTimeout
}

func (o *Options) EffectiveMessage() time.Duration {
	if o != nil && o.Deadlines.Message > 0 {
		return o.Deadlines.Message
	}
	return DefaultMessageTimeout
}

// TotalDeadline returns the session total deadline (0 = unlimited).
func (o *Options) TotalDeadline() time.Duration {
	if o == nil {
		return 0
	}
	return o.Deadlines.Total
}

// effectiveTargetPolicy returns the merged policy, or core's defaults when the
// caller did not supply one.
func (o *Options) effectiveTargetPolicy() core.MergedTargetPolicy {
	if o.HasTargetPolicy {
		return o.TargetPolicy
	}
	return core.MergeTargetPolicy(nil, nil)
}

// reservedHandshakeHeaders are owned by the WebSocket protocol itself.
var reservedHandshakeHeaders = map[string]bool{
	"upgrade":                  true,
	"connection":               true,
	"sec-websocket-key":        true,
	"sec-websocket-version":    true,
	"sec-websocket-protocol":   true,
	"sec-websocket-extensions": true,
	"sec-websocket-accept":     true,
}

// headersCarryCredentials reports whether header names semantically carry
// credentials recognized by the target policy guard.
func headersCarryCredentials(headers [][2]string) bool {
	for _, h := range headers {
		if len(h) < 2 {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(h[0])) {
		case "authorization", "proxy-authorization", "cookie":
			if strings.TrimSpace(h[1]) != "" {
				return true
			}
		}
	}
	return false
}

// Validate performs every pre-network check: URL shape, handshake header
// sanity, message bounds and proxy/TLS resolvability. It never touches the
// network. A *core.ConfigError means "fix the input".
func Validate(o *Options) error {
	if o == nil {
		return configErrorf("websocket options are nil")
	}
	rawURL := strings.TrimSpace(o.URL)
	if rawURL == "" {
		return configErrorf("websocket url is empty")
	}
	if _, err := parseWSURL(rawURL); err != nil {
		return err
	}

	if o.EffectiveMaxMessageBytes() <= 0 {
		return configErrorf("max message bytes must be positive")
	}

	for _, h := range o.Headers {
		if len(h) != 2 {
			return configErrorf("handshake header pair must be [name, value]")
		}
		name := strings.TrimSpace(h[0])
		if name == "" {
			return configErrorf("handshake header name is empty")
		}
		if strings.ContainsAny(name, " \t\r\n") {
			return configErrorf("handshake header name %q is invalid", name)
		}
		if reservedHandshakeHeaders[strings.ToLower(name)] {
			return configErrorf("handshake header %q is reserved by the WebSocket protocol", name)
		}
		if strings.ContainsAny(h[1], "\r\n") {
			return configErrorf("handshake header %q value contains line breaks", name)
		}
	}

	// Proxy/TLS must be resolvable before the first byte hits the wire.
	if _, err := resolveProxy(o, nil); err != nil {
		return err
	}
	if err := validateTLSOptions(o); err != nil {
		return err
	}
	return nil
}

// PromptRequiredBy runs the pre-network target policy evaluation and reports
// whether the exact target needs an interactive confirmation. It returns the
// decision for UI rendering; automation that sets Options.Interactive=false
// gets the same check enforced as a plain deny by Connect.
func PromptRequiredBy(o *Options) (*core.TargetDecision, error) {
	if o == nil {
		return nil, configErrorf("websocket options are nil")
	}
	policyURL, err := policyURLFor(strings.TrimSpace(o.URL))
	if err != nil {
		return nil, err
	}
	return core.EvaluateTarget(o.effectiveTargetPolicy(), core.TargetCheckInput{
		URL:            policyURL,
		Method:         "WEBSOCKET",
		EnvID:          o.EnvID,
		HasCredentials: o.HasCredentials || headersCarryCredentials(o.Headers),
		Interactive:    true,
		Confirmed:      o.Confirmed,
	})
}

// policyURLFor converts ws/wss to the http(s) URL shape that
// core.CanonicalOrigin and core.EvaluateTarget accept.
func policyURLFor(raw string) (string, error) {
	u, err := parseWSURL(raw)
	if err != nil {
		return "", err
	}
	cp := *u
	switch strings.ToLower(cp.Scheme) {
	case "ws":
		cp.Scheme = "http"
	case "wss":
		cp.Scheme = "https"
	}
	return cp.String(), nil
}

// httpHandshakeHeader converts the simple header pairs to http.Header.
// Repeated names are kept as multiple values; gorilla's dialer sends them all.
func httpHandshakeHeader(headers [][2]string) (http.Header, error) {
	h := http.Header{}
	for _, pair := range headers {
		name := strings.TrimSpace(pair[0])
		if name == "" {
			return nil, configErrorf("handshake header name is empty")
		}
		h.Add(name, pair[1])
	}
	return h, nil
}
