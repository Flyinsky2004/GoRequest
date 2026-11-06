package core

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func mustParseURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse url %q: %v", raw, err)
	}
	return u
}

func TestCookieJarDomainPathSecureAndExpiry(t *testing.T) {
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	jar := NewCookieJar("proj-a")
	jar.nowFunc = func() time.Time { return now }

	origin := mustParseURL(t, "https://api.example.com/v1/users/login")
	jar.SetCookies(origin, []*http.Cookie{
		{Name: "host_sid", Value: "h1"}, // HostOnly (no Domain attr), default path "/v1/users"
		{Name: "dom_sid", Value: "d1", Domain: ".example.com", Path: "/"},
		{Name: "sec_tok", Value: "s1", Domain: "example.com", Path: "/", Secure: true, HttpOnly: true},
		{Name: "short_lived", Value: "temp", Path: "/", MaxAge: 10},
	})

	if jar.Count() != 4 {
		t.Fatalf("jar.Count() = %d, want 4", jar.Count())
	}

	// Same host & subpath over HTTPS gets all 4, ordered by longest path first.
	cks := jar.Cookies(mustParseURL(t, "https://api.example.com/v1/users/profile"))
	if len(cks) != 4 {
		t.Fatalf("got %d cookies, want 4", len(cks))
	}
	if cks[0].Name != "host_sid" {
		t.Fatalf("longest path cookie should be first, got %q", cks[0].Name)
	}

	// Sibling subdomain gets domain cookies (dom_sid, sec_tok) over HTTPS, NOT host_sid.
	subCks := jar.Cookies(mustParseURL(t, "https://billing.example.com/"))
	if len(subCks) != 2 {
		t.Fatalf("subdomain cookies = %d, want 2: %+v", len(subCks), subCks)
	}
	for _, c := range subCks {
		if c.Name == "host_sid" {
			t.Fatal("host-only cookie leaked to sibling subdomain")
		}
	}

	// Plain HTTP to api.example.com must NOT receive Secure cookie "sec_tok".
	httpCks := jar.Cookies(mustParseURL(t, "http://api.example.com/v1/users/profile"))
	for _, c := range httpCks {
		if c.Name == "sec_tok" {
			t.Fatal("Secure cookie leaked over plain HTTP")
		}
	}

	// Advance clock by 15s: short_lived expires and is pruned.
	now = now.Add(15 * time.Second)
	afterExpiry := jar.Cookies(mustParseURL(t, "https://api.example.com/v1/users/profile"))
	for _, c := range afterExpiry {
		if c.Name == "short_lived" {
			t.Fatal("expired cookie was not pruned")
		}
	}

	// Server sending Max-Age < 0 deletes an existing cookie.
	jar.SetCookies(origin, []*http.Cookie{
		{Name: "dom_sid", Value: "", Domain: "example.com", Path: "/", MaxAge: -1},
	})
	for _, c := range jar.Entries() {
		if c.Name == "dom_sid" {
			t.Fatal("cookie with Max-Age=-1 was not deleted")
		}
	}
}

func TestCookieJarRejectsCrossDomainAndInjection(t *testing.T) {
	jar := NewCookieJar("proj-sec")
	origin := mustParseURL(t, "https://app.example.com/")

	jar.SetCookies(origin, []*http.Cookie{
		{Name: "evil_other", Value: "1", Domain: "attacker.com", Path: "/"},
		{Name: "evil_tld", Value: "1", Domain: "com", Path: "/"},
		{Name: "bad_val", Value: "abc\r\nX-Injected: 1", Path: "/"},
		{Name: "bad;name", Value: "1", Path: "/"},
	})
	if jar.Count() != 0 {
		t.Fatalf("expected all invalid cookies to be rejected, got %d: %+v", jar.Count(), jar.Entries())
	}

	if err := jar.Upsert(CookieEntry{Name: "ok", Value: "bad;semicolon", Domain: "example.com"}); err == nil {
		t.Fatal("expected Upsert to reject semicolon in cookie value")
	}
	if err := jar.Upsert(CookieEntry{Name: "bad name", Value: "v", Domain: "example.com"}); err == nil {
		t.Fatal("expected Upsert to reject space in cookie name")
	}
}

func TestCookieJarProjectAndSessionIsolation(t *testing.T) {
	jarA := NewCookieJar("project-alpha")
	jarB := NewCookieJar("project-beta")

	if err := jarA.Upsert(CookieEntry{
		Name:   "alpha_token",
		Value:  "secret-alpha-12345",
		Domain: "api.example.com",
		Path:   "/",
	}); err != nil {
		t.Fatal(err)
	}
	if err := jarB.Upsert(CookieEntry{
		Name:   "beta_token",
		Value:  "secret-beta-67890",
		Domain: "api.example.com",
		Path:   "/",
	}); err != nil {
		t.Fatal(err)
	}

	u := mustParseURL(t, "https://api.example.com/v1")
	cksA := jarA.Cookies(u)
	cksB := jarB.Cookies(u)
	if len(cksA) != 1 || cksA[0].Name != "alpha_token" {
		t.Fatalf("jarA cookies = %+v", cksA)
	}
	if len(cksB) != 1 || cksB[0].Name != "beta_token" {
		t.Fatalf("jarB cookies = %+v", cksB)
	}

	// Session clone isolates mutations during a Runner row from the parent project jar.
	rowSession := jarA.CloneForSession("runner-row-1")
	rowSession.SetCookies(u, []*http.Cookie{{Name: "row_only", Value: "r1", Path: "/"}})
	if rowSession.Count() != 2 {
		t.Fatalf("rowSession count = %d, want 2", rowSession.Count())
	}
	if jarA.Count() != 1 {
		t.Fatalf("parent jarA was mutated by session clone: count = %d", jarA.Count())
	}

	// Redactor integration scrubs cookie values.
	red := NewRedactor()
	jarA.RegisterSecrets(red)
	msg := red.RedactString("leaked cookie secret-alpha-12345 in error")
	if strings.Contains(msg, "secret-alpha-12345") {
		t.Fatalf("expected cookie secret to be redacted, got %q", msg)
	}
}
