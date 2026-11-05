package desktop

import (
	"strings"
	"testing"
	"time"

	"gorequest/internal/core"
)

func TestResponseBodyExcludesMetadata(t *testing.T) {
	res := &core.ExecResult{
		Status:   200,
		Proto:    "HTTP/1.1",
		Duration: 12 * time.Millisecond,
		BodySize: 12,
		Body:     []byte(`{"ok":true}`),
		Pretty:   "{\n  \"ok\": true\n}",
		Headers: [][2]string{
			{"Content-Type", "application/json"},
			{"Content-Length", "12"},
			{"Date", "Wed, 05 Nov 2026 00:00:00 GMT"},
		},
	}

	body := responseBodyText(res, false)
	for _, meta := range []string{"Content-Type", "Content-Length", "Date", "HTTP/1.1", "200"} {
		if strings.Contains(body, meta) {
			t.Errorf("pretty body leaked metadata %q: %q", meta, body)
		}
	}
	if body != res.Pretty {
		t.Errorf("pretty body = %q, want res.Pretty", body)
	}
	if !strings.Contains(body, "\n  \"ok\"") {
		t.Errorf("pretty body should be indented: %q", body)
	}

	raw := responseBodyText(res, true)
	if raw != string(res.Body) {
		t.Errorf("raw body = %q, want original bytes", raw)
	}
	if strings.Contains(raw, "Content-Type") {
		t.Errorf("raw body leaked headers: %q", raw)
	}
}

func TestResponseBodyNonJSONFallsBackToRaw(t *testing.T) {
	res := &core.ExecResult{Body: []byte("<html><body>hi</body></html>")}
	if responseBodyIsJSON(res) {
		t.Error("non-JSON body must not be reported as JSON")
	}
	if got := responseBodyText(res, false); got != "<html><body>hi</body></html>" {
		t.Errorf("got %q, want raw text fallback", got)
	}
}

func TestResponseBodyInvalidJSONFallsBackToRaw(t *testing.T) {
	// Execute leaves Pretty empty when the body is not valid JSON.
	res := &core.ExecResult{Body: []byte(`{"broken": `)}
	if got := responseBodyText(res, false); got != `{"broken": ` {
		t.Errorf("got %q, want raw fallback", got)
	}
}

func TestResponseBodyEmpty(t *testing.T) {
	if got := responseBodyText(nil, false); got != "" {
		t.Errorf("nil result body = %q, want empty", got)
	}
	if got := responseBodyText(&core.ExecResult{}, false); got != "" {
		t.Errorf("empty result body = %q, want empty", got)
	}
}

func TestHeaderRowsPreserveDuplicates(t *testing.T) {
	res := &core.ExecResult{Headers: [][2]string{
		{"Set-Cookie", "a=1"},
		{"Set-Cookie", "b=2"},
		{"X-Dup", "1"},
		{"X-Dup", "2"},
	}}
	rows := headerRows(res)
	if len(rows) != 4 {
		t.Fatalf("rows = %d, want 4 (duplicates preserved)", len(rows))
	}
	setCookie := 0
	dup := map[string]bool{}
	for _, r := range rows {
		switch r.Name {
		case "Set-Cookie":
			setCookie++
			if r.Value != "***" {
				t.Errorf("Set-Cookie not masked: %q", r.Value)
			}
		case "X-Dup":
			dup[r.Value] = true
		}
	}
	if setCookie != 2 {
		t.Errorf("Set-Cookie occurrences = %d, want 2", setCookie)
	}
	if !dup["1"] || !dup["2"] {
		t.Errorf("X-Dup values lost: %v", dup)
	}
}

func TestHeaderRowsRedactSensitive(t *testing.T) {
	res := &core.ExecResult{Headers: [][2]string{
		{"Authorization", "Bearer topsecret"},
		{"authorization", "Bearer lower"},
		{"Set-Cookie", "session=abc"},
		{"Content-Type", "application/json"},
	}}
	rows := headerRows(res)
	for _, r := range rows {
		switch strings.ToLower(r.Name) {
		case "authorization", "set-cookie":
			if r.Value != "***" || !r.Sensitive {
				t.Errorf("%s not redacted: %+v", r.Name, r)
			}
			if strings.Contains(r.Value, "topsecret") || strings.Contains(r.Value, "abc") {
				t.Errorf("%s leaked value %q", r.Name, r.Value)
			}
		case "content-type":
			if r.Sensitive || r.Value != "application/json" {
				t.Errorf("content-type wrongly masked: %+v", r)
			}
		}
	}
}

func TestTruncationNoticeIsOutOfBand(t *testing.T) {
	res := &core.ExecResult{
		Body:      []byte(`{"a":1}`),
		Pretty:    "{\n  \"a\": 1\n}",
		Truncated: true,
	}
	if notice := truncationNotice(res); !strings.Contains(notice, "truncated") {
		t.Errorf("notice = %q, want truncation message", notice)
	}
	if strings.Contains(responseBodyText(res, false), "truncated") {
		t.Error("pretty body must not embed the truncation notice")
	}
	if strings.Contains(responseBodyText(res, true), "truncated") {
		t.Error("raw body must not embed the truncation notice")
	}
	if got := truncationNotice(&core.ExecResult{}); got != "" {
		t.Errorf("untruncated notice = %q, want empty", got)
	}
}

func TestIsCanceledErr(t *testing.T) {
	if !isCanceledErr("request canceled") {
		t.Error("expected canceled detection")
	}
	if isCanceledErr("request failed: boom") {
		t.Error("did not expect canceled detection")
	}
}
