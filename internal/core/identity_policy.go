package core

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
)

// PolicyAction controls how requests targeting production environments or
// restricted hosts are handled. Ordered from least to most restrictive:
// ActionAllow < ActionAllowReadOnly < ActionConfirm < ActionDeny.
type PolicyAction string

const (
	ActionAllow         PolicyAction = "allow"
	ActionAllowReadOnly PolicyAction = "allow_readonly"
	ActionConfirm       PolicyAction = "confirm"
	ActionDeny          PolicyAction = "deny"
)

func actionRank(a PolicyAction) int {
	switch strings.ToLower(strings.TrimSpace(string(a))) {
	case string(ActionAllowReadOnly):
		return 1
	case string(ActionConfirm):
		return 2
	case string(ActionDeny):
		return 3
	default:
		return 0
	}
}

func stricterAction(a, b PolicyAction) PolicyAction {
	if actionRank(b) > actionRank(a) {
		return normalizePolicyAction(b)
	}
	return normalizePolicyAction(a)
}

func normalizePolicyAction(a PolicyAction) PolicyAction {
	switch strings.ToLower(strings.TrimSpace(string(a))) {
	case "", string(ActionAllow):
		return ActionAllow
	case string(ActionAllowReadOnly):
		return ActionAllowReadOnly
	case string(ActionConfirm):
		return ActionConfirm
	case string(ActionDeny):
		return ActionDeny
	default:
		return ActionConfirm
	}
}

// DeviceTargetPolicy holds machine-local target & TLS guardrails owned by the
// user's device configuration. Repository files can never relax these limits.
type DeviceTargetPolicy struct {
	ProductionAction        PolicyAction
	ProductionEnvs          []string
	ProductionHosts         []string
	AllowedHosts            []string
	DeniedHosts             []string
	AllowInsecureTLS        bool
	AllowPlainHTTPWithCreds bool
}

// ProjectTargetPolicy holds repository-declared target restrictions. By Q-09's
// monotonicity rule ("a repository cannot expand its own privileges"), project
// settings can only tighten host lists, production classification, or HTTPS
// requirements, and can never weaken DeviceTargetPolicy.
type ProjectTargetPolicy struct {
	ProductionAction     PolicyAction `yaml:"productionAction,omitempty"`
	ProductionEnvs       []string     `yaml:"productionEnvs,omitempty"`
	ProductionHosts      []string     `yaml:"productionHosts,omitempty"`
	AllowedHosts         []string     `yaml:"allowedHosts,omitempty"`
	DeniedHosts          []string     `yaml:"deniedHosts,omitempty"`
	RequireHTTPSForCreds *bool        `yaml:"requireHttpsForCreds,omitempty"`
}

// MergedTargetPolicy is the immutable effective policy computed by
// MergeTargetPolicy.
type MergedTargetPolicy struct {
	ProductionAction     PolicyAction
	ProductionEnvs       map[string]bool
	ProductionHosts      []string
	DeviceAllowedHosts   []string
	ProjectAllowedHosts  []string
	DeniedHosts          []string
	EnforceTLSVerify     bool
	RequireHTTPSForCreds bool
}

// MergeTargetPolicy combines machine-level and project-level policies
// monotonically so project config can only tighten restrictions.
func MergeTargetPolicy(device *DeviceTargetPolicy, project *ProjectTargetPolicy) MergedTargetPolicy {
	devAction := ActionConfirm
	enforceTLS := true
	requireHTTPS := true
	var devProdEnvs, devProdHosts, devAllow, devDeny []string

	if device != nil {
		if strings.TrimSpace(string(device.ProductionAction)) != "" {
			devAction = normalizePolicyAction(device.ProductionAction)
		}
		enforceTLS = !device.AllowInsecureTLS
		requireHTTPS = !device.AllowPlainHTTPWithCreds
		devProdEnvs = device.ProductionEnvs
		devProdHosts = device.ProductionHosts
		devAllow = cleanPatterns(device.AllowedHosts)
		devDeny = cleanPatterns(device.DeniedHosts)
	}

	mergedAction := devAction
	prodEnvs := map[string]bool{
		"prod":       true,
		"production": true,
	}
	for _, e := range devProdEnvs {
		if k := strings.ToLower(strings.TrimSpace(e)); k != "" {
			prodEnvs[k] = true
		}
	}

	prodHosts := cleanPatterns(devProdHosts)
	denied := append([]string(nil), devDeny...)
	var projAllow []string

	if project != nil {
		if strings.TrimSpace(string(project.ProductionAction)) != "" {
			mergedAction = stricterAction(mergedAction, project.ProductionAction)
		}
		for _, e := range project.ProductionEnvs {
			if k := strings.ToLower(strings.TrimSpace(e)); k != "" {
				prodEnvs[k] = true
			}
		}
		prodHosts = appendUniquePatterns(prodHosts, cleanPatterns(project.ProductionHosts)...)
		denied = appendUniquePatterns(denied, cleanPatterns(project.DeniedHosts)...)
		projAllow = cleanPatterns(project.AllowedHosts)
		// Project can require HTTPS for credentials even if device allowed plain HTTP,
		// but can NEVER turn RequireHTTPSForCreds off when device requires it.
		if project.RequireHTTPSForCreds != nil && *project.RequireHTTPSForCreds {
			requireHTTPS = true
		}
	}

	return MergedTargetPolicy{
		ProductionAction:     mergedAction,
		ProductionEnvs:       prodEnvs,
		ProductionHosts:      prodHosts,
		DeviceAllowedHosts:   devAllow,
		ProjectAllowedHosts:  projAllow,
		DeniedHosts:          denied,
		EnforceTLSVerify:     enforceTLS,
		RequireHTTPSForCreds: requireHTTPS,
	}
}

func cleanPatterns(in []string) []string {
	var out []string
	for _, p := range in {
		v := normalizeHost(p)
		if v != "" {
			out = appendUniquePatterns(out, v)
		}
	}
	return out
}

func appendUniquePatterns(dst []string, more ...string) []string {
	for _, m := range more {
		exists := false
		for _, cur := range dst {
			if cur == m {
				exists = true
				break
			}
		}
		if !exists {
			dst = append(dst, m)
		}
	}
	return dst
}

// DecisionOutcome describes whether a request target may proceed immediately,
// requires explicit interactive confirmation, or is blocked outright.
type DecisionOutcome string

const (
	DecisionAllow           DecisionOutcome = "allow"
	DecisionConfirmRequired DecisionOutcome = "confirm_required"
	DecisionDeny            DecisionOutcome = "deny"
)

// TargetCheckInput supplies the resolved target attributes for policy evaluation.
type TargetCheckInput struct {
	URL            string
	Method         string
	EnvID          string
	HasCredentials bool
	Interactive    bool
	Confirmed      bool
}

// TargetDecision details the policy check outcome and classification facts.
type TargetDecision struct {
	Outcome      DecisionOutcome
	Origin       string
	Host         string
	IsLoopback   bool
	IsProduction bool
	IsMutating   bool
	PlainHTTP    bool
	Reasons      []string
}

// IsLoopbackHost reports whether host is localhost or a loopback IP (127.0.0.0/8, ::1).
func IsLoopbackHost(host string) bool {
	h := normalizeHost(host)
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// IsSafeReadMethod reports whether method is read-only / safe (GET, HEAD, OPTIONS).
func IsSafeReadMethod(method string) bool {
	switch strings.ToUpper(strings.TrimSpace(method)) {
	case "", http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	default:
		return false
	}
}

func normalizeHost(raw string) string {
	h := strings.ToLower(strings.TrimSpace(raw))
	h = strings.TrimPrefix(h, "[")
	h = strings.TrimSuffix(h, "]")
	h = strings.TrimSuffix(h, ".")
	return h
}

func matchHostPattern(pattern, host string) bool {
	pat := normalizeHost(pattern)
	h := normalizeHost(host)
	if pat == "" || h == "" {
		return false
	}
	if pat == "*" || pat == h {
		return true
	}
	if strings.HasPrefix(pat, "*.") {
		suffix := pat[1:] // ".example.com"
		return len(h) > len(suffix) && strings.HasSuffix(h, suffix)
	}
	return false
}

func matchesAnyHost(patterns []string, host string) bool {
	for _, pat := range patterns {
		if matchHostPattern(pat, host) {
			return true
		}
	}
	return false
}

func isProductionEnv(prodEnvs map[string]bool, envID string) bool {
	e := strings.ToLower(strings.TrimSpace(envID))
	if e == "" {
		return false
	}
	if prodEnvs[e] {
		return true
	}
	return strings.HasPrefix(e, "prod-") || strings.HasPrefix(e, "prod_")
}

// CanonicalOrigin returns the normalized "scheme://host:effectivePort" origin
// for u, rejecting unsupported or relative URLs.
func CanonicalOrigin(u *url.URL) (string, error) {
	if u == nil {
		return "", configErrorf("nil url")
	}
	scheme := strings.ToLower(strings.TrimSpace(u.Scheme))
	if scheme != "http" && scheme != "https" {
		return "", configErrorf("unsupported url scheme %q", u.Scheme)
	}
	host := normalizeHost(u.Hostname())
	if host == "" {
		return "", configErrorf("url %q has no host", u.String())
	}
	port := u.Port()
	if port == "" {
		if scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	if strings.Contains(host, ":") {
		return fmt.Sprintf("%s://[%s]:%s", scheme, host, port), nil
	}
	return fmt.Sprintf("%s://%s:%s", scheme, host, port), nil
}

// SameOrigin reports whether rawA and rawB share the exact canonical scheme,
// host, and effective port.
func SameOrigin(rawA, rawB string) (bool, error) {
	ua, err := url.Parse(strings.TrimSpace(rawA))
	if err != nil {
		return false, configErrorf("invalid url %q: %v", rawA, err)
	}
	ub, err := url.Parse(strings.TrimSpace(rawB))
	if err != nil {
		return false, configErrorf("invalid url %q: %v", rawB, err)
	}
	ub = ua.ResolveReference(ub)
	oa, err := CanonicalOrigin(ua)
	if err != nil {
		return false, err
	}
	ob, err := CanonicalOrigin(ub)
	if err != nil {
		return false, err
	}
	return oa == ob, nil
}

// EvaluateTarget checks whether a request to in.URL is allowed, requires
// interactive confirmation, or must be blocked under policy. Non-interactive
// callers (Runner/CI) receive a *ConfigError immediately whenever confirmation
// or denial applies.
func EvaluateTarget(policy MergedTargetPolicy, in TargetCheckInput) (*TargetDecision, error) {
	parsed, err := url.Parse(strings.TrimSpace(in.URL))
	if err != nil {
		return nil, configErrorf("invalid target url: %v", err)
	}
	origin, err := CanonicalOrigin(parsed)
	if err != nil {
		return nil, err
	}
	host := normalizeHost(parsed.Hostname())
	scheme := strings.ToLower(parsed.Scheme)
	method := strings.ToUpper(strings.TrimSpace(in.Method))
	if method == "" {
		method = http.MethodGet
	}

	dec := &TargetDecision{
		Outcome:      DecisionAllow,
		Origin:       origin,
		Host:         host,
		IsLoopback:   IsLoopbackHost(host),
		IsProduction: isProductionEnv(policy.ProductionEnvs, in.EnvID) || matchesAnyHost(policy.ProductionHosts, host),
		IsMutating:   !IsSafeReadMethod(method),
		PlainHTTP:    scheme == "http",
	}

	// 1. Host denylist & allowlist intersection (device AND project must both permit).
	if matchesAnyHost(policy.DeniedHosts, host) {
		dec.Outcome = DecisionDeny
		reason := fmt.Sprintf("target host %q is blocked by target policy", host)
		dec.Reasons = append(dec.Reasons, reason)
		return dec, configErrorf("%s", reason)
	}
	if len(policy.DeviceAllowedHosts) > 0 && !matchesAnyHost(policy.DeviceAllowedHosts, host) {
		dec.Outcome = DecisionDeny
		reason := fmt.Sprintf("target host %q is not in device allowed hosts", host)
		dec.Reasons = append(dec.Reasons, reason)
		return dec, configErrorf("%s", reason)
	}
	if len(policy.ProjectAllowedHosts) > 0 && !matchesAnyHost(policy.ProjectAllowedHosts, host) {
		dec.Outcome = DecisionDeny
		reason := fmt.Sprintf("target host %q is not in project allowed hosts", host)
		dec.Reasons = append(dec.Reasons, reason)
		return dec, configErrorf("%s", reason)
	}

	needsConfirm := false

	// 2. Non-loopback plain HTTP with credentials guard.
	if dec.PlainHTTP && !dec.IsLoopback && in.HasCredentials && policy.RequireHTTPSForCreds {
		reason := fmt.Sprintf("sending credentials over plain HTTP to non-loopback host %q", host)
		dec.Reasons = append(dec.Reasons, reason)
		if !in.Interactive {
			dec.Outcome = DecisionDeny
			return dec, configErrorf("target policy blocked %s", reason)
		}
		if !in.Confirmed {
			needsConfirm = true
		}
	}

	// 3. Production environment / production host guard.
	if dec.IsProduction {
		switch policy.ProductionAction {
		case ActionDeny:
			dec.Outcome = DecisionDeny
			reason := fmt.Sprintf("requests to production target %q are denied by policy", host)
			dec.Reasons = append(dec.Reasons, reason)
			return dec, configErrorf("%s", reason)
		case ActionAllowReadOnly:
			if dec.IsMutating {
				dec.Outcome = DecisionDeny
				reason := fmt.Sprintf("mutating %s request to production target %q is forbidden by read-only policy", method, host)
				dec.Reasons = append(dec.Reasons, reason)
				return dec, configErrorf("%s", reason)
			}
		case ActionConfirm:
			if dec.IsMutating {
				reason := fmt.Sprintf("mutating %s request to production target %q requires confirmation", method, host)
				dec.Reasons = append(dec.Reasons, reason)
				if !in.Interactive {
					dec.Outcome = DecisionDeny
					return dec, configErrorf("%s", reason)
				}
				if !in.Confirmed {
					needsConfirm = true
				}
			}
		}
	}

	if needsConfirm {
		dec.Outcome = DecisionConfirmRequired
	}
	return dec, nil
}

// StripCrossOriginCredentials inspects a redirect hop from prevURL to nextURL
// (resolving relative Location references) and removes Authorization,
// Proxy-Authorization, Cookie, AND any custom API Key header declared in
// authScheme whenever scheme, host, or effective port changes (including
// same-host https -> http downgrades).
func StripCrossOriginCredentials(prevURL, nextURL string, headers [][2]string, authScheme *AuthScheme) ([][2]string, bool, error) {
	same, err := SameOrigin(prevURL, nextURL)
	if err != nil {
		return nil, false, err
	}
	if same {
		cp := make([][2]string, len(headers))
		copy(cp, headers)
		return cp, false, nil
	}

	customHdr := ""
	if authScheme != nil &&
		strings.EqualFold(strings.TrimSpace(authScheme.Type), "apikey") &&
		NormalizeAPIKeyLocation(authScheme.In) == "header" {
		customHdr = strings.ToLower(strings.TrimSpace(authScheme.Name))
	}

	kept := make([][2]string, 0, len(headers))
	for _, h := range headers {
		lk := strings.ToLower(strings.TrimSpace(h[0]))
		if lk == "authorization" || lk == "proxy-authorization" || lk == "cookie" {
			continue
		}
		if customHdr != "" && lk == customHdr {
			continue
		}
		kept = append(kept, h)
	}
	return kept, true, nil
}

// EvaluateRedirectHop resolves nextURL against prevURL, strips cross-origin or
// downgraded credentials (including custom API Key headers), and re-evaluates
// target policy on the redirect destination before the hop is followed.
func EvaluateRedirectHop(
	policy MergedTargetPolicy,
	prevURL, nextURL, method, envID string,
	headers [][2]string,
	authScheme *AuthScheme,
	interactive, confirmed bool,
) ([][2]string, string, *TargetDecision, error) {
	prevParsed, err := url.Parse(strings.TrimSpace(prevURL))
	if err != nil {
		return nil, "", nil, configErrorf("invalid previous url %q: %v", prevURL, err)
	}
	nextParsed, err := url.Parse(strings.TrimSpace(nextURL))
	if err != nil {
		return nil, "", nil, configErrorf("invalid redirect url %q: %v", nextURL, err)
	}
	resolved := prevParsed.ResolveReference(nextParsed)
	resolvedStr := resolved.String()

	kept, crossOrigin, err := StripCrossOriginCredentials(prevURL, resolvedStr, headers, authScheme)
	if err != nil {
		return nil, "", nil, err
	}

	stillHasCreds := false
	for _, h := range kept {
		lk := strings.ToLower(strings.TrimSpace(h[0]))
		if lk == "authorization" || lk == "proxy-authorization" || lk == "cookie" {
			stillHasCreds = true
			break
		}
		if authScheme != nil && strings.EqualFold(strings.TrimSpace(authScheme.Type), "apikey") &&
			NormalizeAPIKeyLocation(authScheme.In) == "header" &&
			strings.EqualFold(strings.TrimSpace(authScheme.Name), lk) {
			stillHasCreds = true
			break
		}
	}
	if !crossOrigin && authScheme != nil &&
		strings.EqualFold(strings.TrimSpace(authScheme.Type), "apikey") &&
		NormalizeAPIKeyLocation(authScheme.In) == "query" {
		stillHasCreds = true
	}
	if resolved.User != nil {
		if _, hasPwd := resolved.User.Password(); hasPwd {
			stillHasCreds = true
		}
	}

	dec, err := EvaluateTarget(policy, TargetCheckInput{
		URL:            resolvedStr,
		Method:         method,
		EnvID:          envID,
		HasCredentials: stillHasCreds,
		Interactive:    interactive,
		Confirmed:      confirmed,
	})
	if err != nil {
		return nil, resolvedStr, dec, err
	}
	return kept, resolvedStr, dec, nil
}
