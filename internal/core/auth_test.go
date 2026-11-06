package core

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAPIKeyBuildAndExecuteThreeLocations(t *testing.T) {
	type captured struct {
		headerVal string
		queryVal  string
		cookieVal string
		rawCookie string
	}
	var got captured
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = captured{
			headerVal: r.Header.Get("X-API-Key"),
			queryVal:  r.URL.Query().Get("api_key"),
			rawCookie: r.Header.Get("Cookie"),
		}
		if c, err := r.Cookie("sid"); err == nil {
			got.cookieVal = c.Value
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	secrets := MapSecrets{"k": "top-secret-value"}
	vars := map[string]string{"hdrName": "X-API-Key"}

	t.Run("header_default_and_explicit", func(t *testing.T) {
		for _, in := range []string{"", "header", "HEADER"} {
			req := &Request{HTTP: &HTTPDef{Method: "GET", URL: srv.URL + "/items"}}
			scheme := &AuthScheme{Type: "apikey", Name: "{{hdrName}}", In: in, Value: "{{secret.k}}"}
			plan, err := BuildPlan(req, vars, secrets, scheme)
			if err != nil {
				t.Fatalf("in=%q BuildPlan: %v", in, err)
			}
			if _, err := Execute(context.Background(), plan, srv.Client()); err != nil {
				t.Fatalf("in=%q Execute: %v", in, err)
			}
			if got.headerVal != "top-secret-value" || got.queryVal != "" || got.cookieVal != "" {
				t.Fatalf("in=%q server received %+v", in, got)
			}
		}
	})

	t.Run("query_preserves_existing_query", func(t *testing.T) {
		req := &Request{HTTP: &HTTPDef{
			Method: "GET",
			URL:    srv.URL + "/items",
			Query:  []KV{{Name: "page", Value: "2"}},
		}}
		scheme := &AuthScheme{Type: "apikey", Name: "api_key", In: "query", Value: "{{secret.k}}"}
		plan, err := BuildPlan(req, vars, secrets, scheme)
		if err != nil {
			t.Fatalf("BuildPlan: %v", err)
		}
		if _, err := Execute(context.Background(), plan, srv.Client()); err != nil {
			t.Fatalf("Execute: %v", err)
		}
		if got.queryVal != "top-secret-value" || got.headerVal != "" {
			t.Fatalf("server received %+v", got)
		}
	})

	t.Run("cookie_merges_with_existing_cookie_header", func(t *testing.T) {
		req := &Request{HTTP: &HTTPDef{
			Method:  "GET",
			URL:     srv.URL + "/items",
			Headers: []KV{{Name: "Cookie", Value: "theme=dark"}},
		}}
		scheme := &AuthScheme{Type: "apikey", Name: "sid", In: "cookie", Value: "{{secret.k}}"}
		plan, err := BuildPlan(req, vars, secrets, scheme)
		if err != nil {
			t.Fatalf("BuildPlan: %v", err)
		}
		if _, err := Execute(context.Background(), plan, srv.Client()); err != nil {
			t.Fatalf("Execute: %v", err)
		}
		if got.cookieVal != "top-secret-value" {
			t.Fatalf("cookie sid = %q, want top-secret-value (raw=%q)", got.cookieVal, got.rawCookie)
		}
		if !strings.Contains(got.rawCookie, "theme=dark") {
			t.Fatalf("existing cookie lost: %q", got.rawCookie)
		}
	})
}

func TestAuthRejectsInjectionAndMissingConfig(t *testing.T) {
	req := &Request{HTTP: &HTTPDef{Method: "GET", URL: "http://127.0.0.1:4010/test"}}

	cases := []struct {
		name    string
		scheme  *AuthScheme
		secrets SecretProvider
	}{
		{
			name:    "bearer_newline_injection",
			scheme:  &AuthScheme{Type: "bearer", Token: "{{secret.bad}}"},
			secrets: MapSecrets{"bad": "tok\r\nX-Injected: 1"},
		},
		{
			name:    "apikey_empty_name",
			scheme:  &AuthScheme{Type: "apikey", Name: "   ", In: "header", Value: "v"},
			secrets: nil,
		},
		{
			name:    "apikey_header_newline",
			scheme:  &AuthScheme{Type: "apikey", Name: "X-Key", In: "header", Value: "a\nb"},
			secrets: nil,
		},
		{
			name:    "apikey_cookie_semicolon_injection",
			scheme:  &AuthScheme{Type: "apikey", Name: "sid", In: "cookie", Value: "val; Admin=1"},
			secrets: nil,
		},
		{
			name:    "apikey_cookie_bad_name",
			scheme:  &AuthScheme{Type: "apikey", Name: "sid=evil", In: "cookie", Value: "val"},
			secrets: nil,
		},
		{
			name:    "apikey_invalid_location",
			scheme:  &AuthScheme{Type: "apikey", Name: "X-Key", In: "body", Value: "val"},
			secrets: nil,
		},
		{
			name:    "missing_secret",
			scheme:  &AuthScheme{Type: "apikey", Name: "X-Key", In: "header", Value: "{{secret.missing}}"},
			secrets: MapSecrets{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := BuildPlan(req, nil, tc.secrets, tc.scheme)
			if err == nil || !IsConfig(err) {
				t.Fatalf("expected ConfigError, got %v", err)
			}
		})
	}
}

func TestCloneAuthRefDeepCopy(t *testing.T) {
	orig := &AuthRef{
		Mode:   "inline",
		Inline: &AuthScheme{SchemaVersion: 1, Type: "apikey", Name: "X-Key", In: "header", Value: "{{secret.k}}"},
	}
	cp := CloneAuthRef(orig)
	cp.Inline.Name = "Changed"
	if orig.Inline.Name != "X-Key" {
		t.Fatalf("CloneAuthRef mutated original: %+v", orig.Inline)
	}
	if CloneAuthRef(nil) != nil {
		t.Fatal("CloneAuthRef(nil) must be nil")
	}
}
