package main

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	maxEchoBody = 1 << 20  // 1 MiB cap for echoed request bodies
	maxDelayMS  = 30000    // longest simulated delay, in milliseconds
	maxLarge    = 64 << 20 // largest byte count /large will stream
	largeBlock  = 32 << 10 // streaming block size for /large
)

// kvPair is one ordered query parameter, preserving duplicates and order.
type kvPair struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// echoResponse is the JSON shape returned by /echo. It surfaces the raw request
// so the desktop app can show method, repeated query parameters, headers and
// body exactly as the server received them.
type echoResponse struct {
	Method        string              `json:"method"`
	Path          string              `json:"path"`
	RawQuery      string              `json:"rawQuery"`
	Query         []kvPair            `json:"query"`
	Headers       map[string][]string `json:"headers"`
	ContentType   string              `json:"contentType"`
	ContentLength int64               `json:"contentLength"`
	Body          string              `json:"body"`
	BodyTruncated bool                `json:"bodyTruncated"`
}

// parseOrderedQuery parses a raw query string by hand so repeated keys and the
// original ordering are preserved (url.Values sorts keys and loses order).
func parseOrderedQuery(raw string) []kvPair {
	if raw == "" {
		return []kvPair{}
	}
	parts := strings.Split(raw, "&")
	out := make([]kvPair, 0, len(parts))
	for _, part := range parts {
		if part == "" {
			continue
		}
		name, value, _ := strings.Cut(part, "=")
		if decoded, err := url.QueryUnescape(name); err == nil {
			name = decoded
		}
		if decoded, err := url.QueryUnescape(value); err == nil {
			value = decoded
		}
		out = append(out, kvPair{Name: name, Value: value})
	}
	return out
}

func handleEcho(w http.ResponseWriter, r *http.Request) {
	limited := io.LimitReader(r.Body, maxEchoBody+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		writeError(w, http.StatusBadRequest, "cannot read request body", err.Error())
		return
	}
	truncated := false
	if len(body) > maxEchoBody {
		body = body[:maxEchoBody]
		truncated = true
	}

	headers := make(map[string][]string, len(r.Header)+1)
	for name, values := range r.Header {
		headers[name] = append([]string(nil), values...)
	}
	headers["Host"] = []string{r.Host}

	writeJSON(w, http.StatusOK, echoResponse{
		Method:        r.Method,
		Path:          r.URL.Path,
		RawQuery:      r.URL.RawQuery,
		Query:         parseOrderedQuery(r.URL.RawQuery),
		Headers:       headers,
		ContentType:   r.Header.Get("Content-Type"),
		ContentLength: r.ContentLength,
		Body:          string(body),
		BodyTruncated: truncated,
	})
}

// handleStatus returns whatever 4xx/5xx status the caller asks for, with a JSON
// body, so error-state handling in the app can be exercised deliberately.
func handleStatus(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	raw := strings.TrimPrefix(r.URL.Path, "/status/")
	code, err := strconv.Atoi(raw)
	if err != nil || code < 400 || code > 599 {
		writeError(w, http.StatusBadRequest, "invalid status code", "use an integer between 400 and 599")
		return
	}
	writeJSON(w, code, map[string]any{
		"status":  code,
		"message": http.StatusText(code),
	})
}

// handleDelay sleeps for ?ms=N milliseconds while honoring request cancellation
// (timeout or client disconnect), so the app's cancel/timeout paths can be
// observed against a real in-flight request.
func handleDelay(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	raw := r.URL.Query().Get("ms")
	if raw == "" {
		raw = "500"
	}
	ms, err := strconv.Atoi(raw)
	if err != nil || ms < 0 || ms > maxDelayMS {
		writeError(w, http.StatusBadRequest, "invalid delay", fmt.Sprintf("ms must be an integer between 0 and %d", maxDelayMS))
		return
	}

	timer := time.NewTimer(time.Duration(ms) * time.Millisecond)
	defer timer.Stop()

	select {
	case <-r.Context().Done():
		// Client canceled or timed out; there is no one to write to.
		return
	case <-timer.C:
		writeJSON(w, http.StatusOK, map[string]any{"delayedMs": ms})
	}
}

// handleRedirect issues a redirect to a local path. Only same-origin relative
// targets are allowed, which prevents using the demo as an open redirect.
func handleRedirect(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	q := r.URL.Query()
	to := q.Get("to")
	if to == "" {
		to = "/health"
	}
	// Reject backslashes and control characters too: browsers treat "\" like
	// "/" when resolving, so "/\evil.com" would otherwise become "//evil.com"
	// and slip past the "//" check as a protocol-relative external redirect.
	if !strings.HasPrefix(to, "/") || strings.HasPrefix(to, "//") || strings.ContainsAny(to, "\r\n\\") {
		writeError(w, http.StatusBadRequest, "invalid redirect target", "to must be a local path beginning with a single /")
		return
	}

	status := http.StatusFound
	if raw := q.Get("status"); raw != "" {
		code, err := strconv.Atoi(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid redirect status", "allowed: 301, 302, 303, 307, 308")
			return
		}
		switch code {
		case http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther, http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
			status = code
		default:
			writeError(w, http.StatusBadRequest, "invalid redirect status", "allowed: 301, 302, 303, 307, 308")
			return
		}
	}
	http.Redirect(w, r, to, status)
}

// handleRedirectChain follows /redirect/chain/{n}: n>0 redirects to n-1, and
// n==0 returns 200. It lets the app show a multi-hop redirect ending in success.
func handleRedirectChain(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	raw := strings.TrimPrefix(r.URL.Path, "/redirect/chain/")
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 || n > 20 {
		writeError(w, http.StatusBadRequest, "invalid redirect chain", "use /redirect/chain/{n} with 0 <= n <= 20")
		return
	}
	if n == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"redirected": true, "remaining": 0})
		return
	}
	http.Redirect(w, r, "/redirect/chain/"+strconv.Itoa(n-1), http.StatusFound)
}

// handleFormat returns the same fictional resource as JSON, plain text or XML so
// the app can display different body types and content types.
func handleFormat(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	kind := strings.TrimPrefix(r.URL.Path, "/format/")
	switch kind {
	case "json":
		writeJSON(w, http.StatusOK, map[string]any{
			"id":     1,
			"name":   "示例资源",
			"active": true,
			"tags":   []string{"demo", "sample"},
		})
	case "text":
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("X-Demo-Server", "gorequest-demo")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "id=1\nname=示例资源\nactive=true\ntags=demo,sample\n")
	case "xml":
		w.Header().Set("Content-Type", "application/xml; charset=utf-8")
		w.Header().Set("X-Demo-Server", "gorequest-demo")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `<?xml version="1.0" encoding="UTF-8"?>
<resource>
  <id>1</id>
  <name>示例资源</name>
  <active>true</active>
  <tags>demo</tags>
  <tags>sample</tags>
</resource>
`)
	default:
		writeError(w, http.StatusNotFound, "unknown format", "use /format/json, /format/text or /format/xml")
	}
}

// handleMultiHeader emits duplicate response headers and two cookies so the app
// can verify it preserves repeated headers and redacts Set-Cookie values.
func handleMultiHeader(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	w.Header().Add("X-Multi", "one")
	w.Header().Add("X-Multi", "two")
	w.Header().Add("Set-Cookie", "session=demo-session; Path=/; HttpOnly")
	w.Header().Add("Set-Cookie", "theme=dark; Path=/")
	w.Header().Set("X-Request-Id", "demo-request-1")
	writeJSON(w, http.StatusOK, map[string]any{
		"duplicateHeader": "X-Multi",
		"cookieCount":     2,
	})
}

// handleLarge streams ?size=N bytes of plain text (default 1 MiB). Reading more
// than core's 32 MiB limit lets the app be checked for truncation behavior.
func handleLarge(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	size := 1 << 20
	if raw := r.URL.Query().Get("size"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v < 0 || v > maxLarge {
			writeError(w, http.StatusBadRequest, "invalid size", fmt.Sprintf("size must be an integer between 0 and %d", maxLarge))
			return
		}
		size = v
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Demo-Server", "gorequest-demo")
	w.Header().Set("X-Content-Length", strconv.Itoa(size))
	w.Header().Set("Content-Length", strconv.Itoa(size))
	w.WriteHeader(http.StatusOK)

	block := []byte(strings.Repeat("0123456789abcdef", largeBlock/16))
	remaining := size
	for remaining > 0 {
		n := largeBlock
		if remaining < n {
			n = remaining
		}
		if _, err := w.Write(block[:n]); err != nil {
			return
		}
		remaining -= n
	}
}
