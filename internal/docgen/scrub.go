package docgen

import (
	"encoding/base64"
	"fmt"
	"regexp"
	"strings"

	"gorequest/internal/core"
)

// The scrubber decides which stored example values may appear in derived
// documentation. Example sources are restricted by the card builder to values
// the author deliberately placed in the operation contract (extensions in the
// shared request YAML) — never live responses, local credentials or the
// secret provider. The scrubber then masks everything that looks like a
// credential so a committed example cannot leak a real token even when the
// author pasted one by mistake.

// maskedValue replaces whatever a heuristic considered unsafe; it matches the
// core identity mask "***".
const maskedValue = "***"

// URLCredentials is what a URL-embedded credential becomes after scrubbing.
var urlCredentialRe = regexp.MustCompile(`://[^/@\s:]+:[^@/\s]+@`)

// ScrubStats summarizes everything a scrubber changed.
type ScrubStats struct {
	// Masked counts values replaced because they matched a registered secret,
	// an encoded variant, a sensitive name or embedded URL credentials.
	Masked int
	// Blocked counts values replaced because their shape was credential-like
	// (tokens, key material) and safety could not be proven.
	Blocked int
	// SecretTemplateRefs counts {{secret.*}} template references kept as
	// literal text; they publish a name, never a value.
	SecretTemplateRefs int
}

// Merge adds the counts of o into the receiver.
func (s *ScrubStats) Merge(o ScrubStats) {
	s.Masked += o.Masked
	s.Blocked += o.Blocked
	s.SecretTemplateRefs += o.SecretTemplateRefs
}

// Scrubber masks credential-shaped example values. It is per-card state: the
// card builder creates one, scrubs the card's examples through it and reports
// its stats as diagnostics so an author sees exactly which values were
// masked or blocked.
type Scrubber struct {
	red   *core.Redactor
	sens  map[string]bool
	stats ScrubStats
}

// NewScrubber returns a scrubber; red may be nil, in which case only the
// shape heuristics apply. The standard credential transport names are
// pre-registered, mirroring core's Redactor defaults.
func NewScrubber(red *core.Redactor) *Scrubber {
	s := &Scrubber{red: red, sens: map[string]bool{}}
	for _, name := range []string{"authorization", "proxy-authorization", "cookie", "set-cookie", "www-authenticate", "x-api-key"} {
		s.sens[name] = true
	}
	return s
}

// RegisterSensitiveName masks the value of a header, query or cookie with
// this name regardless of its shape.
func (s *Scrubber) RegisterSensitiveName(name string) {
	name = strings.ToLower(strings.TrimSpace(name))
	if name != "" {
		s.sens[name] = true
	}
}

// Stats returns the counting snapshot so far.
func (s *Scrubber) Stats() ScrubStats { return s.stats }

// sensitiveName reports whether values under the given case-insensitive name
// must be masked.
func (s *Scrubber) sensitiveName(name string) bool {
	return s.sens[strings.ToLower(strings.TrimSpace(name))]
}

var secretTemplateRe = regexp.MustCompile(`\{\{\s*secret\.([A-Za-z0-9_.-]+)\s*\}\}`)

// Value walks v and returns a publishable copy.
func (s *Scrubber) Value(v any) any {
	switch t := v.(type) {
	case nil:
		return nil
	case string:
		return s.String(t)
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = s.Value(e)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, e := range t {
			out[s.String(k)] = s.valueUnder(k, e)
		}
		return out
	case map[any]any:
		out := make(map[string]any, len(t))
		for k, e := range t {
			var key string
			if ks, ok := k.(string); ok {
				key = ks
			} else {
				key = fmt.Sprintf("%v", k)
			}
			out[s.String(key)] = s.valueUnder(key, e)
		}
		return out
	default:
		// Numbers, booleans and anything without string content publish
		// as-is.
		return v
	}
}

// valueUnder scrubs e, replacing it wholesale when its name is sensitive.
func (s *Scrubber) valueUnder(name string, e any) any {
	if s.sensitiveName(name) && e != nil {
		s.stats.Masked++
		return maskedValue
	}
	return s.Value(e)
}

// NamedValue scrubs the value under a concrete name (header, query or cookie).
func (s *Scrubber) NamedValue(name string, v any) any {
	return s.valueUnder(name, v)
}

// KVValue scrubs a stored name/value pair for display, masking by name and by
// shape. The name is shown as-is (names are published constants such as
// "Authorization" or an API key header name), the value is scrubbed.
func (s *Scrubber) KVValue(name, value string) string {
	if s.sensitiveName(name) {
		s.stats.Masked++
		return maskedValue
	}
	return s.String(value)
}

// String scrubs a single string.
func (s *Scrubber) String(v string) string {
	if v == "" {
		return v
	}
	// Secret template references publish a name, not a value; keep them
	// literal so readers see what the example binds, and count them.
	s.stats.SecretTemplateRefs += len(secretTemplateRe.FindAllStringSubmatch(v, -1))
	if s.red != nil {
		masked := s.red.RedactString(v)
		if masked != v {
			s.stats.Masked++
			v = masked
		}
	}
	if credentialMaterial(v) {
		s.stats.Blocked++
		return maskedValue
	}
	if urlCredentialRe.MatchString(v) {
		s.stats.Masked++
		v = urlCredentialRe.ReplaceAllString(v, "://"+maskedValue+"@")
	}
	return v
}

// credentialMaterial reports whether s must be treated as live key/token
// material, in which case the value is replaced entirely: it cannot be proven
// safe to publish.
func credentialMaterial(s string) bool {
	if len(s) < 16 {
		return false
	}
	if strings.Contains(s, "-----BEGIN") && strings.Contains(s, "PRIVATE KEY-----") {
		return true
	}
	for _, prefix := range []string{"sk-", "sk_", "ghp_", "gho_", "ghu_", "ghr_", "github_pat_", "xoxb", "xoxp", "xoxa", "glpat-", "AIza", "ya29.", "SG.", "AKIA", "ASIA"} {
		if strings.HasPrefix(s, prefix) {
			return true
		}
	}
	// Treat long bearer/basic authorization headers as key material.
	if rest, ok := strings.CutPrefix(s, "Bearer "); ok && credentialBlob(rest) {
		return true
	}
	if rest, ok := strings.CutPrefix(s, "Basic "); ok && credentialBlob(rest) {
		return true
	}
	return credentialBlob(s)
}

// credentialBlob reports whether s itself looks like opaque key material.
func credentialBlob(s string) bool {
	if len(s) < 32 {
		return false
	}
	if base64ish(s) || hexish(s) {
		return byteVariety(s) >= 0.30
	}
	return false
}

var base64ishRe = regexp.MustCompile(`^[A-Za-z0-9+/=_-]+$`)
var hexishRe = regexp.MustCompile(`^[0-9a-fA-F]+$`)

func base64ish(s string) bool {
	if !base64ishRe.MatchString(s) {
		return false
	}
	if strings.Contains(s, "=") {
		// Standard base64 pads to multiples of four.
		return len(s)%4 == 0
	}
	if _, err := base64.StdEncoding.DecodeString(s); err == nil {
		return true
	}
	if _, err := base64.RawStdEncoding.DecodeString(s); err == nil {
		return true
	}
	if _, err := base64.URLEncoding.DecodeString(s); err == nil {
		return true
	}
	if _, err := base64.RawURLEncoding.DecodeString(s); err == nil {
		return true
	}
	return false
}

func hexish(s string) bool { return hexishRe.MatchString(s) }

// byteVariety measures character variety; human words and repeated
// placeholders score low, credential blobs score high. The denominator is the
// printable ASCII range size a random blob would draw from.
func byteVariety(s string) float64 {
	if s == "" {
		return 0
	}
	seen := map[byte]bool{}
	for i := 0; i < len(s); i++ {
		seen[s[i]] = true
	}
	return float64(len(seen)) / 64.0
}
