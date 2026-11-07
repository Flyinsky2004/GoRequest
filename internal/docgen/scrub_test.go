package docgen

import (
	"strings"
	"testing"

	"gorequest/internal/core"
)

// All tests in this package are authored but NOT executed in phase 1; the
// post-integration verification run executes them with the go toolchain.

func TestScrubberMasksByRegisteredSecret(t *testing.T) {
	red := core.NewRedactor()
	red.RegisterSecret("s3cr3t-token-value")
	s := NewScrubber(red)
	got := s.String("Authorization: s3cr3t-token-value")
	if got != "Authorization: "+maskedValue {
		t.Fatalf("registered secret not masked: %q", got)
	}
	if s.Stats().Masked != 1 {
		t.Fatalf("stats: %+v", s.Stats())
	}
}

func TestScrubberBlocksCredentialShapes(t *testing.T) {
	s := NewScrubber(nil)
	cases := []string{
		"Bearer 8q6v2x0z4k9d7f3n1m5y8q6v2x0z4k9d7f3n1m5y",
		"-----BEGIN RSA PRIVATE KEY-----\nabc\n-----END RSA PRIVATE KEY-----",
		"ghp_abcdefghijklmnopqrstuvwxyz012345",
		"AKIA" + "ABCDEFGHIJ" + "KLMNOPQR",
		"8q6v2x0z4k9d7f3n1m5y8q6v2x0z4k9d7f3n1m5y", // bare high-variety blob
	}
	for _, v := range cases {
		if got := s.String(v); got != maskedValue {
			t.Errorf("credential shape not blocked: %q -> %q", v, got)
		}
	}
	if s.Stats().Blocked != len(cases) {
		t.Fatalf("blocked stats = %d, want %d", s.Stats().Blocked, len(cases))
	}
}

func TestScrubberKeepsReadableValues(t *testing.T) {
	s := NewScrubber(nil)
	for _, v := range []string{
		"/users/{id}?include=profile",
		"application/json; charset=utf-8",
		"2024-01-01T00:00:00Z",
		"{{baseUrl}}/users/{{userId}}",
	} {
		if got := s.String(v); got != v {
			t.Errorf("readable value changed: %q -> %q", v, got)
		}
	}
	if s.Stats().Blocked != 0 || s.Stats().Masked != 0 {
		t.Fatalf("false positives: %+v", s.Stats())
	}
}

func TestScrubberCountsSecretTemplateRefs(t *testing.T) {
	s := NewScrubber(nil)
	out := s.Value(map[string]any{"api_key": secretTemplateRef})
	if out != s.Value(map[string]any{"api_key": secretTemplateRef}) {
		t.Fatalf("nondeterministic scrub")
	}
	if got := s.String(secretTemplateRef); got != secretTemplateRef {
		t.Fatalf("template reference must stay literal: %q", got)
	}
	// Two Value/String passes on the reference counted twice.
	if s.Stats().SecretTemplateRefs != 2 {
		t.Fatalf("stats: %+v", s.Stats())
	}
}

func TestURLCredentialsAreMasked(t *testing.T) {
	s := NewScrubber(nil)
	got := s.String("https://user:p4ssw0rd@host.example/db")
	if strings.Contains(got, "p4ssw0rd") {
		t.Fatalf("URL credentials leaked: %q", got)
	}
	if !strings.Contains(got, "://"+maskedValue+"@host.example/db") {
		t.Fatalf("unexpected masking form: %q", got)
	}
}

func TestNamedValuesMaskWholeValue(t *testing.T) {
	s := NewScrubber(nil)
	if got := s.NamedValue("X-API-Key", "5123-act-guid-like"); got != maskedValue {
		t.Fatalf("sensitive name must mask its value: %q", got)
	}
	if got := s.NamedValue("X-API-Key", "short"); got != maskedValue {
		t.Fatalf("sensitive names mask regardless of shape: %q", got)
	}
	if s.Stats().Masked != 2 {
		t.Fatalf("stats: %+v", s.Stats())
	}
}
