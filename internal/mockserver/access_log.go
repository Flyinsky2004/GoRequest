package mockserver

import (
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"gorequest/internal/core"
)

const redactedLiteral = "***"

var (
	sensitiveFieldNamePattern = regexp.MustCompile(`(?i)(password|passwd|pwd|secret|token|access_token|refresh_token|id_token|client_secret|api_key|apikey|x-api-key|authorization|proxy-authorization|cookie|set-cookie|private_key|session_id|sessionid|credential)`)
	bearerOrBasicValuePattern = regexp.MustCompile(`(?i)\b(bearer|basic)\s+[A-Za-z0-9._~+/=-]{4,}`)
)

// AccessRecord is one sanitized, bounded in-memory entry recorded by the Mock
// Server. It never leaves memory into shared project files.
type AccessRecord struct {
	Seq                 uint64      `json:"seq"`
	Timestamp           time.Time   `json:"timestamp"`
	Method              string      `json:"method"`
	Path                string      `json:"path"`
	SanitizedQuery      string      `json:"sanitizedQuery,omitempty"`
	Scenario            string      `json:"scenario"`
	Matched             bool        `json:"matched"`
	MatchedRuleID       string      `json:"matchedRuleId,omitempty"`
	Status              int         `json:"status"`
	DurationMs          int64       `json:"durationMs"`
	Canceled            bool        `json:"canceled,omitempty"`
	RequestHeaders      [][2]string `json:"requestHeaders,omitempty"`
	RequestBodyPreview  string      `json:"requestBodyPreview,omitempty"`
	ResponseHeaders     [][2]string `json:"responseHeaders,omitempty"`
	ResponseBodyPreview string      `json:"responseBodyPreview,omitempty"`
	Diagnostic          string      `json:"diagnostic,omitempty"`
}

// AccessLog is a thread-safe bounded ring buffer of sanitized AccessRecords.
type AccessLog struct {
	mu             sync.RWMutex
	maxEntries     int
	maxBodyPreview int
	entries        []AccessRecord
}

// NewAccessLog creates an in-memory AccessLog with bounded capacity and preview size.
func NewAccessLog(maxEntries, maxBodyPreview int) *AccessLog {
	if maxEntries <= 0 {
		maxEntries = DefaultMaxAccessLogEntries
	}
	if maxEntries > 1000 {
		maxEntries = 1000
	}
	if maxBodyPreview <= 0 {
		maxBodyPreview = DefaultMaxLogBodyPreview
	}
	if maxBodyPreview > 65536 {
		maxBodyPreview = 65536
	}
	return &AccessLog{
		maxEntries:     maxEntries,
		maxBodyPreview: maxBodyPreview,
		entries:        make([]AccessRecord, 0, maxEntries),
	}
}

// MaxEntries returns the ring buffer capacity.
func (l *AccessLog) MaxEntries() int {
	if l == nil {
		return 0
	}
	return l.maxEntries
}

// Record sanitizes rec using red and appends it to the bounded ring buffer.
func (l *AccessLog) Record(rec AccessRecord, red *core.Redactor) AccessRecord {
	sanitized := SanitizeAccessRecord(rec, red, l.effectiveMaxPreview())
	if l == nil {
		return sanitized
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.entries) >= l.maxEntries {
		copy(l.entries, l.entries[1:])
		l.entries = l.entries[:l.maxEntries-1]
	}
	l.entries = append(l.entries, sanitized)
	return sanitized
}

func (l *AccessLog) effectiveMaxPreview() int {
	if l == nil || l.maxBodyPreview <= 0 {
		return DefaultMaxLogBodyPreview
	}
	return l.maxBodyPreview
}

// Entries returns a snapshot copy of all recorded entries (oldest to newest).
func (l *AccessLog) Entries() []AccessRecord {
	if l == nil {
		return nil
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	out := make([]AccessRecord, len(l.entries))
	for i, e := range l.entries {
		cp := e
		if len(e.RequestHeaders) > 0 {
			cp.RequestHeaders = append([][2]string(nil), e.RequestHeaders...)
		}
		if len(e.ResponseHeaders) > 0 {
			cp.ResponseHeaders = append([][2]string(nil), e.ResponseHeaders...)
		}
		out[i] = cp
	}
	return out
}

// Count returns the current number of entries in the access log.
func (l *AccessLog) Count() int {
	if l == nil {
		return 0
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	return len(l.entries)
}

// Clear removes all entries from the access log.
func (l *AccessLog) Clear() {
	if l == nil {
		return
	}
	l.mu.Lock()
	l.entries = l.entries[:0]
	l.mu.Unlock()
}

// SanitizeAccessRecord scrubs sensitive headers, query params, and body fields
// from rec and truncates body previews to maxPreviewBytes.
func SanitizeAccessRecord(rec AccessRecord, red *core.Redactor, maxPreviewBytes int) AccessRecord {
	if maxPreviewBytes <= 0 {
		maxPreviewBytes = DefaultMaxLogBodyPreview
	}
	if red == nil {
		red = core.NewRedactor()
	}
	out := rec
	out.Path = ScrubFreeText(rec.Path, red)
	out.SanitizedQuery = SanitizeRawQuery(rec.SanitizedQuery, red)
	out.RequestHeaders = SanitizeHeaderPairs(rec.RequestHeaders, red)
	out.ResponseHeaders = SanitizeHeaderPairs(rec.ResponseHeaders, red)
	out.RequestBodyPreview = truncatePreview(SanitizeBodyText(rec.RequestBodyPreview, red), maxPreviewBytes)
	out.ResponseBodyPreview = truncatePreview(SanitizeBodyText(rec.ResponseBodyPreview, red), maxPreviewBytes)
	out.Diagnostic = ScrubFreeText(rec.Diagnostic, red)
	return out
}

// HeaderMapToPairs converts an http.Header into deterministically sorted
// [name, value] pairs.
func HeaderMapToPairs(h http.Header) [][2]string {
	if len(h) == 0 {
		return nil
	}
	keys := make([]string, 0, len(h))
	for k := range h {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var out [][2]string
	for _, k := range keys {
		for _, v := range h[k] {
			out = append(out, [2]string{k, v})
		}
	}
	return out
}

// SanitizeHeaderPairs masks sensitive HTTP headers (Authorization, Cookie,
// Set-Cookie, Proxy-Authorization, API keys, and any header matching
// sensitiveFieldNamePattern) as well as any registered secret in red.
func SanitizeHeaderPairs(headers [][2]string, red *core.Redactor) [][2]string {
	if len(headers) == 0 {
		return nil
	}
	if red == nil {
		red = core.NewRedactor()
	}
	dummy := &core.Plan{
		Method:  http.MethodGet,
		URL:     "http://127.0.0.1/",
		Headers: headers,
	}
	sanitized := red.SanitizePlan(dummy)
	out := make([][2]string, len(sanitized.Headers))
	for i, kv := range sanitized.Headers {
		name := kv[0]
		val := kv[1]
		if IsSensitiveKeyName(name) {
			if strings.EqualFold(name, "Authorization") || strings.EqualFold(name, "Proxy-Authorization") {
				if scheme, _, ok := strings.Cut(strings.TrimSpace(val), " "); ok && scheme != "" && scheme != redactedLiteral {
					val = scheme + " " + redactedLiteral
				} else {
					val = redactedLiteral
				}
			} else {
				val = redactedLiteral
			}
		} else {
			val = ScrubFreeText(val, red)
		}
		out[i] = [2]string{name, val}
	}
	return out
}

// SanitizeRawQuery masks sensitive query parameters and registered secrets.
func SanitizeRawQuery(rawQuery string, red *core.Redactor) string {
	if strings.TrimSpace(rawQuery) == "" {
		return ""
	}
	if red == nil {
		red = core.NewRedactor()
	}
	vals, err := url.ParseQuery(rawQuery)
	if err != nil {
		return ScrubFreeText(rawQuery, red)
	}
	keys := make([]string, 0, len(vals))
	for k := range vals {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		safeKey := ScrubFreeText(k, red)
		isSens := IsSensitiveKeyName(k)
		for _, v := range vals[k] {
			safeVal := v
			if isSens {
				safeVal = redactedLiteral
			} else {
				safeVal = ScrubFreeText(v, red)
			}
			parts = append(parts, url.QueryEscape(safeKey)+"="+url.QueryEscape(safeVal))
		}
	}
	encoded := strings.Join(parts, "&")
	return strings.ReplaceAll(encoded, "%2A%2A%2A", redactedLiteral)
}

// SanitizeBodyText scrubs registered secrets, sensitive JSON fields (such as
// password, token, secret, access_token, cookie, api_key), and Bearer/Basic
// tokens from a request or response body string.
func SanitizeBodyText(body string, red *core.Redactor) string {
	if body == "" {
		return ""
	}
	if red == nil {
		red = core.NewRedactor()
	}
	trimmed := strings.TrimSpace(body)
	if (strings.HasPrefix(trimmed, "{") && strings.HasSuffix(trimmed, "}")) ||
		(strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]")) {
		var parsed any
		dec := json.NewDecoder(strings.NewReader(trimmed))
		dec.UseNumber()
		if err := dec.Decode(&parsed); err == nil {
			scrubbed := scrubJSONNode(parsed, red)
			if b, merr := json.Marshal(scrubbed); merr == nil {
				return ScrubFreeText(string(b), red)
			}
		}
	}
	return ScrubFreeText(body, red)
}

func scrubJSONNode(v any, red *core.Redactor) any {
	switch node := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(node))
		for k, child := range node {
			if IsSensitiveKeyName(k) {
				out[k] = redactedLiteral
				continue
			}
			out[k] = scrubJSONNode(child, red)
		}
		return out
	case []any:
		out := make([]any, len(node))
		for i, elem := range node {
			out[i] = scrubJSONNode(elem, red)
		}
		return out
	case string:
		return ScrubFreeText(node, red)
	default:
		return node
	}
}

// ScrubFreeText applies red.RedactString and masks inline Bearer/Basic tokens.
func ScrubFreeText(s string, red *core.Redactor) string {
	if s == "" {
		return ""
	}
	out := s
	if red != nil {
		out = red.RedactString(out)
	}
	out = bearerOrBasicValuePattern.ReplaceAllStringFunc(out, func(m string) string {
		if scheme, _, ok := strings.Cut(m, " "); ok {
			return scheme + " " + redactedLiteral
		}
		return redactedLiteral
	})
	return out
}

// IsSensitiveKeyName reports whether a header, query parameter, or JSON property
// name matches standard credential/secret naming patterns.
func IsSensitiveKeyName(name string) bool {
	return sensitiveFieldNamePattern.MatchString(strings.TrimSpace(name))
}

func truncatePreview(s string, maxBytes int) string {
	if maxBytes <= 0 || len(s) <= maxBytes {
		return s
	}
	return s[:maxBytes] + "...[truncated]"
}
