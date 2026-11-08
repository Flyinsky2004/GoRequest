package core

import (
	"bufio"
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"path"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"
)

// Default stage timeout values used when a stage timeout is not explicitly overridden.
const (
	DefaultDialTimeout           = 10 * time.Second
	DefaultTLSHandshakeTimeout   = 10 * time.Second
	DefaultResponseHeaderTimeout = 30 * time.Second
	DefaultMaxRedirects          = 10
)

// StageTimeouts configures independent timeouts for each phase of an HTTP
// request. Zero values fall back to safe defaults or the overall request
// timeout. All stages also honor context cancellation immediately.
type StageTimeouts struct {
	// DialTimeout bounds TCP connection establishment (including DNS resolution).
	DialTimeout time.Duration `yaml:"dialTimeout,omitempty" json:"dialTimeout,omitempty"`
	// TLSHandshakeTimeout bounds the TLS handshake after TCP connect.
	TLSHandshakeTimeout time.Duration `yaml:"tlsHandshakeTimeout,omitempty" json:"tlsHandshakeTimeout,omitempty"`
	// ResponseHeaderTimeout bounds the time waiting for the server's first
	// response header byte after the request has been written (TTFB).
	ResponseHeaderTimeout time.Duration `yaml:"responseHeaderTimeout,omitempty" json:"responseHeaderTimeout,omitempty"`
	// TotalTimeout bounds the entire request execution including redirects and
	// response body download. When 0, Plan.Timeout (or DefaultTimeout) is used.
	TotalTimeout time.Duration `yaml:"totalTimeout,omitempty" json:"totalTimeout,omitempty"`
}

// RedirectMode controls how 3xx HTTP redirect responses are handled.
type RedirectMode string

const (
	// RedirectFollow follows 3xx redirects up to MaxHops while re-evaluating
	// target policy and stripping credentials on cross-origin or HTTPS->HTTP
	// downgrade hops.
	RedirectFollow RedirectMode = "follow"
	// RedirectNone stops at the first 3xx response and returns it directly.
	RedirectNone RedirectMode = "none"
	// RedirectSameOriginOnly follows redirects only when scheme, host, and
	// effective port remain identical.
	RedirectSameOriginOnly RedirectMode = "same_origin"
)

// RedirectPolicy configures redirect behavior for a request or project.
type RedirectPolicy struct {
	Mode    RedirectMode `yaml:"mode,omitempty" json:"mode,omitempty"`
	MaxHops int          `yaml:"maxHops,omitempty" json:"maxHops,omitempty"`
}

// EffectiveMode returns the normalized RedirectMode (defaulting to RedirectFollow).
func (p RedirectPolicy) EffectiveMode() RedirectMode {
	switch strings.ToLower(strings.TrimSpace(string(p.Mode))) {
	case string(RedirectNone), "manual", "stop", "false":
		return RedirectNone
	case string(RedirectSameOriginOnly), "same-origin":
		return RedirectSameOriginOnly
	default:
		return RedirectFollow
	}
}

// EffectiveMaxHops returns the maximum allowed redirect hops (defaulting to
// DefaultMaxRedirects when <= 0).
func (p RedirectPolicy) EffectiveMaxHops() int {
	if p.MaxHops < 0 {
		return 0
	}
	if p.MaxHops == 0 {
		return DefaultMaxRedirects
	}
	return p.MaxHops
}

// RedirectHop records transport and security facts for one 3xx redirect step.
type RedirectHop struct {
	Hop                 int
	StatusCode          int
	Method              string
	FromURL             string
	ToURL               string
	OriginChanged       bool
	StrippedCredentials bool
}

// NetworkTrace captures observed per-stage timings and connection metadata
// collected via httptrace and TLS connection state. Unobserved stages (for
// example DNS/Connect/TLS on a reused keep-alive connection) keep Observed=false
// so the UI never fabricates 0ms measurements.
type NetworkTrace struct {
	DNSDuration           time.Duration
	DNSObserved           bool
	ConnectDuration       time.Duration
	ConnectObserved       bool
	TLSDuration           time.Duration
	TLSObserved           bool
	WriteDuration         time.Duration
	WriteObserved         bool
	TTFBDuration          time.Duration
	TTFBObserved          bool
	DownloadDuration      time.Duration
	ReusedConn            bool
	RemoteAddr            string
	TLSVersion            string
	TLSCipherSuite        string
	TLSServerName         string
	TLSInsecureSkipVerify bool
	ClientCertUsed        bool
	CustomCAUsed          bool
	ProxyUsed             string
}

// ExecuteOptions configures transport, security, cookie jar, redirect, and
// response limits for ExecuteWithOptions.
type ExecuteOptions struct {
	// Client is an optional custom *http.Client. When nil, a dedicated
	// transport is constructed from Timeouts, Proxy, and TLS.
	Client *http.Client

	// Timeouts configures per-stage and overall deadlines.
	Timeouts StageTimeouts

	// Redirect configures 3xx handling and hop limits.
	Redirect RedirectPolicy

	// Proxy configures direct, system, HTTP/HTTPS, or SOCKS5 proxy routing.
	Proxy ProxyConfig

	// TLS configures system/custom CA trust and local mTLS client certificates.
	TLS TLSConfig

	// CookieJar is the project- or session-isolated CookieJar. When nil or when
	// DisableCookies is true, no jar cookies are read or stored.
	CookieJar      *CookieJar
	DisableCookies bool

	// TargetPolicy is the merged device + project target policy evaluated on
	// the initial target and every redirect hop.
	TargetPolicy    MergedTargetPolicy
	HasTargetPolicy bool
	EnvID           string
	AuthScheme      *AuthScheme
	Redactor        *Redactor
	Interactive     bool
	Confirmed       bool

	// File and secret resolvers for Custom CA / mTLS slot resolution and proxy
	// credentials.
	ProjectRoot  string
	ProjectFiles ProjectFileOpener
	LocalFiles   LocalFileProvider
	Secrets      SecretProvider

	// MaxResponseBytes caps the response body (and wire download) size. When
	// <= 0, DefaultMaxBody (32 MiB) is used.
	MaxResponseBytes int64

	// DisableAutoDecompress keeps compressed wire bytes as-is instead of
	// automatically decompressing gzip/deflate payloads.
	DisableAutoDecompress bool

	// Stream opts into incremental server-sent-event delivery. When set and the
	// response Content-Type is text/event-stream, ExecuteWithOptions parses the
	// stream as it arrives and calls the handler instead of waiting for EOF.
	// Ordinary responses are unaffected. Callbacks run on the reader goroutine.
	Stream *StreamHandler
}

// EffectiveTargetPolicy returns opts.TargetPolicy if HasTargetPolicy is set, or
// the default MergedTargetPolicy otherwise.
func (opts ExecuteOptions) EffectiveTargetPolicy() MergedTargetPolicy {
	if opts.HasTargetPolicy {
		return opts.TargetPolicy
	}
	return MergeTargetPolicy(nil, nil)
}

// EffectiveMaxResponseBytes returns the active response body size ceiling.
func (opts ExecuteOptions) EffectiveMaxResponseBytes() int64 {
	if opts.MaxResponseBytes > 0 {
		return opts.MaxResponseBytes
	}
	return DefaultMaxBody
}

// InspectPayload classifies a response body as textual or binary, extracts the
// normalized Content-Type, and computes a safe suggested filename for saving.
func InspectPayload(rawURL string, headers [][2]string, body []byte) (contentType string, isBinary bool, suggestedFilename string) {
	var rawCT, rawDisp string
	for _, kv := range headers {
		if strings.EqualFold(kv[0], "Content-Type") && rawCT == "" {
			rawCT = strings.TrimSpace(kv[1])
		} else if strings.EqualFold(kv[0], "Content-Disposition") && rawDisp == "" {
			rawDisp = strings.TrimSpace(kv[1])
		}
	}

	mediaType := ""
	if rawCT != "" {
		if mt, _, err := mime.ParseMediaType(rawCT); err == nil {
			mediaType = strings.ToLower(strings.TrimSpace(mt))
		} else {
			mediaType, _, _ = strings.Cut(strings.ToLower(rawCT), ";")
			mediaType = strings.TrimSpace(mediaType)
		}
	}
	contentType = mediaType

	if mediaType != "" {
		if isKnownBinaryMediaType(mediaType) {
			isBinary = true
		} else if isKnownTextMediaType(mediaType) {
			isBinary = false
		} else {
			isBinary = looksLikeBinaryBytes(body)
		}
	} else {
		isBinary = looksLikeBinaryBytes(body)
	}

	suggestedFilename = suggestResponseFilename(rawURL, rawDisp, mediaType, isBinary, body)
	return contentType, isBinary, suggestedFilename
}

func isKnownTextMediaType(mt string) bool {
	if strings.HasPrefix(mt, "text/") {
		return true
	}
	if strings.HasSuffix(mt, "+json") || strings.HasSuffix(mt, "+xml") || strings.HasSuffix(mt, "+yaml") {
		return true
	}
	switch mt {
	case "application/json",
		"application/xml",
		"application/javascript",
		"application/x-javascript",
		"application/ecmascript",
		"application/x-www-form-urlencoded",
		"application/graphql",
		"application/yaml",
		"application/x-yaml",
		"image/svg+xml":
		return true
	default:
		return false
	}
}

func isKnownBinaryMediaType(mt string) bool {
	if mt == "image/svg+xml" {
		return false
	}
	if strings.HasPrefix(mt, "image/") ||
		strings.HasPrefix(mt, "audio/") ||
		strings.HasPrefix(mt, "video/") ||
		strings.HasPrefix(mt, "font/") {
		return true
	}
	switch mt {
	case "application/octet-stream",
		"application/pdf",
		"application/zip",
		"application/gzip",
		"application/x-gzip",
		"application/x-tar",
		"application/wasm",
		"application/protobuf",
		"application/x-protobuf",
		"application/grpc":
		return true
	default:
		return false
	}
}

func looksLikeBinaryBytes(body []byte) bool {
	if len(body) == 0 {
		return false
	}
	sample := body
	if len(sample) > 8192 {
		sample = sample[:8192]
	}
	for _, b := range sample {
		if b == 0 {
			return true
		}
	}
	return !utf8.Valid(sample)
}

func suggestResponseFilename(rawURL, rawDisp, mediaType string, isBinary bool, body []byte) string {
	if rawDisp != "" {
		if _, params, err := mime.ParseMediaType(rawDisp); err == nil {
			if fn := sanitizeDownloadFilename(params["filename"]); fn != "" {
				return fn
			}
		}
	}
	ext := extensionForMediaType(mediaType, isBinary, body)
	if u, err := url.Parse(strings.TrimSpace(rawURL)); err == nil && u != nil {
		base := path.Base(strings.TrimSuffix(u.Path, "/"))
		if base != "" && base != "." && base != "/" {
			clean := sanitizeDownloadFilename(base)
			if clean != "" {
				if strings.Contains(clean, ".") {
					return clean
				}
				return clean + ext
			}
		}
	}
	return "response" + ext
}

func sanitizeDownloadFilename(raw string) string {
	s := strings.TrimSpace(raw)
	s = strings.ReplaceAll(s, "\\", "/")
	s = path.Base(s)
	s = strings.Trim(s, ". ")
	if s == "" || s == "." || s == ".." || s == "/" {
		return ""
	}
	var b strings.Builder
	for _, r := range s {
		if r < 0x20 || r == '/' || r == '\\' || r == ':' || r == '*' || r == '?' || r == '"' || r == '<' || r == '>' || r == '|' {
			b.WriteByte('_')
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func extensionForMediaType(mediaType string, isBinary bool, body []byte) string {
	switch mediaType {
	case "application/json":
		return ".json"
	case "application/xml", "text/xml":
		return ".xml"
	case "text/html":
		return ".html"
	case "text/csv":
		return ".csv"
	case "text/plain":
		return ".txt"
	case "application/pdf":
		return ".pdf"
	case "application/zip":
		return ".zip"
	case "application/gzip", "application/x-gzip":
		return ".gz"
	case "image/png":
		return ".png"
	case "image/jpeg":
		return ".jpg"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	case "image/svg+xml":
		return ".svg"
	}
	if strings.HasSuffix(mediaType, "+json") {
		return ".json"
	}
	if strings.HasSuffix(mediaType, "+xml") {
		return ".xml"
	}
	if isBinary {
		return ".bin"
	}
	if len(body) > 0 && (body[0] == '{' || body[0] == '[') {
		return ".json"
	}
	return ".txt"
}

// FormatBinaryPreview generates a deterministic hex + ASCII preview and SHA-256
// digest summary for a binary response body so the UI can inspect binary
// payloads safely without feeding arbitrary non-UTF8 bytes into a text editor.
func FormatBinaryPreview(body []byte, maxBytes int) (sha256Hex string, hexDump string) {
	sum := sha256.Sum256(body)
	sha256Hex = hex.EncodeToString(sum[:])
	if len(body) == 0 {
		return sha256Hex, ""
	}
	if maxBytes <= 0 {
		maxBytes = 512
	}
	sample := body
	if len(sample) > maxBytes {
		sample = sample[:maxBytes]
	}
	return sha256Hex, hex.Dump(sample)
}

func hasRequestCredentials(headers [][2]string, rawURL string, authScheme *AuthScheme, jar *CookieJar, disableCookies bool) bool {
	for _, kv := range headers {
		lk := strings.ToLower(strings.TrimSpace(kv[0]))
		if lk == "authorization" || lk == "proxy-authorization" || lk == "cookie" {
			return true
		}
		if authScheme != nil &&
			strings.EqualFold(strings.TrimSpace(authScheme.Type), "apikey") &&
			NormalizeAPIKeyLocation(authScheme.In) == "header" &&
			strings.EqualFold(strings.TrimSpace(authScheme.Name), lk) {
			return true
		}
	}
	if authScheme != nil && strings.EqualFold(strings.TrimSpace(authScheme.Type), "apikey") {
		loc := NormalizeAPIKeyLocation(authScheme.In)
		if loc == "query" || loc == "cookie" {
			return true
		}
	}
	if u, err := url.Parse(strings.TrimSpace(rawURL)); err == nil && u != nil {
		if u.User != nil {
			if _, hasPwd := u.User.Password(); hasPwd {
				return true
			}
		}
		if !disableCookies && jar != nil && len(jar.Cookies(u)) > 0 {
			return true
		}
	}
	return false
}

// MergeHeadersWithCookies combines explicit plan headers with CookieJar entries
// for targetURL without mutating plan.Headers. If the request already has an
// explicit Cookie header, jar cookies whose names are not already present in
// the explicit Cookie header are appended.
func MergeHeadersWithCookies(headers [][2]string, u *url.URL, jar *CookieJar, disableCookies bool) [][2]string {
	out := make([][2]string, len(headers))
	copy(out, headers)
	if disableCookies || jar == nil || u == nil {
		return out
	}
	jarCookies := jar.Cookies(u)
	if len(jarCookies) == 0 {
		return out
	}

	existingNames := map[string]bool{}
	cookieIdx := -1
	for i, kv := range out {
		if strings.EqualFold(strings.TrimSpace(kv[0]), "Cookie") {
			if cookieIdx < 0 {
				cookieIdx = i
			}
			for _, part := range strings.Split(kv[1], ";") {
				if name, _, ok := strings.Cut(strings.TrimSpace(part), "="); ok {
					existingNames[strings.TrimSpace(name)] = true
				}
			}
		}
	}

	var extra []string
	for _, hc := range jarCookies {
		if hc == nil || existingNames[hc.Name] {
			continue
		}
		extra = append(extra, hc.Name+"="+hc.Value)
	}
	if len(extra) == 0 {
		return out
	}
	joined := strings.Join(extra, "; ")
	if cookieIdx >= 0 {
		cur := strings.TrimSpace(out[cookieIdx][1])
		if cur != "" && !strings.HasSuffix(cur, ";") {
			cur += "; "
		} else if cur != "" {
			cur += " "
		}
		out[cookieIdx][1] = cur + joined
	} else {
		out = append(out, [2]string{"Cookie", joined})
	}
	return out
}

func applyHeadersToHTTPRequest(req *http.Request, headers [][2]string) {
	req.Header = make(http.Header, len(headers))
	for _, h := range headers {
		k := strings.TrimSpace(h[0])
		if k == "" {
			continue
		}
		if strings.EqualFold(k, "Host") {
			req.Host = h[1]
		}
		req.Header.Add(k, h[1])
	}
}

func hasHeaderKey(headers [][2]string, key string) bool {
	for _, h := range headers {
		if strings.EqualFold(strings.TrimSpace(h[0]), key) {
			return true
		}
	}
	return false
}

type traceCollector struct {
	mu           sync.Mutex
	dnsStart     time.Time
	connectStart time.Time
	tlsStart     time.Time
	writeStart   time.Time
	wroteReqAt   time.Time
	trace        NetworkTrace
}

func newTraceCollector() (*traceCollector, *httptrace.ClientTrace) {
	tc := &traceCollector{}
	ct := &httptrace.ClientTrace{
		DNSStart: func(httptrace.DNSStartInfo) {
			tc.mu.Lock()
			tc.dnsStart = time.Now()
			tc.mu.Unlock()
		},
		DNSDone: func(httptrace.DNSDoneInfo) {
			tc.mu.Lock()
			if !tc.dnsStart.IsZero() {
				tc.trace.DNSDuration = time.Since(tc.dnsStart)
				tc.trace.DNSObserved = true
			}
			tc.mu.Unlock()
		},
		ConnectStart: func(_, _ string) {
			tc.mu.Lock()
			if tc.connectStart.IsZero() {
				tc.connectStart = time.Now()
			}
			tc.mu.Unlock()
		},
		ConnectDone: func(_, addr string, err error) {
			tc.mu.Lock()
			if !tc.connectStart.IsZero() {
				tc.trace.ConnectDuration = time.Since(tc.connectStart)
				tc.trace.ConnectObserved = true
			}
			if err == nil && addr != "" {
				tc.trace.RemoteAddr = addr
			}
			tc.mu.Unlock()
		},
		TLSHandshakeStart: func() {
			tc.mu.Lock()
			tc.tlsStart = time.Now()
			tc.mu.Unlock()
		},
		TLSHandshakeDone: func(cs tls.ConnectionState, err error) {
			tc.mu.Lock()
			if !tc.tlsStart.IsZero() {
				tc.trace.TLSDuration = time.Since(tc.tlsStart)
				tc.trace.TLSObserved = true
			}
			if err == nil {
				tc.trace.TLSVersion = tlsVersionName(cs.Version)
				tc.trace.TLSCipherSuite = tls.CipherSuiteName(cs.CipherSuite)
				tc.trace.TLSServerName = cs.ServerName
			}
			tc.mu.Unlock()
		},
		GotConn: func(info httptrace.GotConnInfo) {
			tc.mu.Lock()
			tc.trace.ReusedConn = info.Reused
			if info.Conn != nil && info.Conn.RemoteAddr() != nil {
				tc.trace.RemoteAddr = info.Conn.RemoteAddr().String()
			}
			tc.writeStart = time.Now()
			tc.mu.Unlock()
		},
		WroteRequest: func(info httptrace.WroteRequestInfo) {
			tc.mu.Lock()
			now := time.Now()
			if !tc.writeStart.IsZero() {
				tc.trace.WriteDuration = now.Sub(tc.writeStart)
				tc.trace.WriteObserved = true
			}
			tc.wroteReqAt = now
			tc.mu.Unlock()
		},
		GotFirstResponseByte: func() {
			tc.mu.Lock()
			if !tc.wroteReqAt.IsZero() {
				tc.trace.TTFBDuration = time.Since(tc.wroteReqAt)
				tc.trace.TTFBObserved = true
			}
			tc.mu.Unlock()
		},
	}
	return tc, ct
}

func (tc *traceCollector) setProxyUsed(label string) {
	tc.mu.Lock()
	tc.trace.ProxyUsed = label
	tc.mu.Unlock()
}

func (tc *traceCollector) snapshot(resp *http.Response, downloadDur time.Duration, builtTLS *builtTLSResult) *NetworkTrace {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	out := tc.trace
	out.DownloadDuration = downloadDur
	if builtTLS != nil {
		out.CustomCAUsed = builtTLS.customCAUsed
		out.ClientCertUsed = builtTLS.clientCertUsed
		out.TLSInsecureSkipVerify = builtTLS.insecureUsed
	}
	if resp != nil && resp.TLS != nil {
		if out.TLSVersion == "" {
			out.TLSVersion = tlsVersionName(resp.TLS.Version)
		}
		if out.TLSCipherSuite == "" {
			out.TLSCipherSuite = tls.CipherSuiteName(resp.TLS.CipherSuite)
		}
		if out.TLSServerName == "" {
			out.TLSServerName = resp.TLS.ServerName
		}
	}
	return &out
}

type wireCountingReader struct {
	ctx       context.Context
	r         io.Reader
	total     atomic.Int64
	wireLimit int64
}

func (w *wireCountingReader) Read(p []byte) (int, error) {
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := w.r.Read(p)
	if n > 0 {
		tot := w.total.Add(int64(n))
		if w.wireLimit > 0 && tot > w.wireLimit {
			return n, io.EOF
		}
	}
	if err != nil && err != io.EOF {
		if cerr := w.ctx.Err(); cerr != nil {
			return n, cerr
		}
	}
	return n, err
}

func (w *wireCountingReader) Total() int64 {
	return w.total.Load()
}

type responseBodyPayload struct {
	body            []byte
	transferSize    int64
	bodySize        int64
	truncated       bool
	contentEncoding string
	decompressed    bool
}

func readAndDecodeResponseBody(
	ctx context.Context,
	resp *http.Response,
	reqMethod string,
	maxBody int64,
	disableAutoDecompress bool,
) (*responseBodyPayload, error) {
	if maxBody <= 0 {
		maxBody = DefaultMaxBody
	}
	// Allow wire stream up to maxBody*2 + 64 KiB so poorly compressible streams
	// can still fill maxBody while preventing unbounded zip-bomb wire reads.
	wireLimit := maxBody*2 + (64 << 10)
	if wireLimit < maxBody {
		wireLimit = maxBody
	}

	wire := &wireCountingReader{
		ctx:       ctx,
		r:         resp.Body,
		wireLimit: wireLimit,
	}

	rawCE := strings.ToLower(strings.TrimSpace(resp.Header.Get("Content-Encoding")))
	if strings.Contains(rawCE, ",") {
		// Take outer/primary encoding token.
		rawCE = strings.TrimSpace(strings.Split(rawCE, ",")[0])
	}

	shouldDecompress := !disableAutoDecompress &&
		!resp.Uncompressed &&
		!strings.EqualFold(reqMethod, http.MethodHead) &&
		resp.StatusCode != http.StatusNoContent &&
		resp.StatusCode != http.StatusNotModified &&
		(rawCE == "gzip" || rawCE == "x-gzip" || rawCE == "deflate")

	if !shouldDecompress {
		data, truncated, err := readBoundedChunks(ctx, wire, maxBody)
		if err != nil {
			return nil, err
		}
		transferSize := wire.Total()
		if resp.Uncompressed && resp.ContentLength > 0 {
			transferSize = resp.ContentLength
		}
		return &responseBodyPayload{
			body:            data,
			transferSize:    transferSize,
			bodySize:        int64(len(data)),
			truncated:       truncated,
			contentEncoding: rawCE,
			decompressed:    resp.Uncompressed,
		}, nil
	}

	br := bufio.NewReaderSize(wire, 32<<10)
	if _, err := br.Peek(1); err != nil {
		if err == io.EOF {
			return &responseBodyPayload{
				body:            nil,
				transferSize:    wire.Total(),
				bodySize:        0,
				contentEncoding: rawCE,
			}, nil
		}
		return nil, err
	}

	var decomp io.ReadCloser
	switch rawCE {
	case "gzip", "x-gzip":
		gr, err := gzip.NewReader(br)
		if err != nil {
			return nil, fmt.Errorf("decompressing gzip response body: %w", err)
		}
		decomp = gr
	case "deflate":
		hdr, _ := br.Peek(2)
		if isValidZlibHeader(hdr) {
			zr, err := zlib.NewReader(br)
			if err != nil {
				return nil, fmt.Errorf("decompressing deflate (zlib) response body: %w", err)
			}
			decomp = zr
		} else {
			decomp = flate.NewReader(br)
		}
	}
	defer decomp.Close()

	data, truncated, err := readBoundedChunks(ctx, decomp, maxBody)
	if err != nil {
		return nil, fmt.Errorf("decompressing %s response body: %w", rawCE, err)
	}
	if !truncated {
		// Drain any remaining framing trailer bytes (e.g. gzip 8-byte trailer)
		// from br so wire.Total() reflects the complete compressed transfer size.
		_, _ = io.CopyN(io.Discard, br, 64)
	}

	return &responseBodyPayload{
		body:            data,
		transferSize:    wire.Total(),
		bodySize:        int64(len(data)),
		truncated:       truncated,
		contentEncoding: rawCE,
		decompressed:    true,
	}, nil
}

func isValidZlibHeader(hdr []byte) bool {
	if len(hdr) < 2 {
		return false
	}
	cmf := hdr[0]
	flg := hdr[1]
	if (cmf&0x0F) != 8 || (cmf>>4) > 7 {
		return false
	}
	return (uint16(cmf)<<8|uint16(flg))%31 == 0
}

func readBoundedChunks(ctx context.Context, r io.Reader, maxBytes int64) ([]byte, bool, error) {
	var buf bytes.Buffer
	chunk := make([]byte, 32<<10)
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		n, err := r.Read(chunk)
		if n > 0 {
			remaining := maxBytes + 1 - total
			if int64(n) > remaining {
				buf.Write(chunk[:remaining])
				total += remaining
				return buf.Bytes()[:maxBytes], true, nil
			}
			buf.Write(chunk[:n])
			total += int64(n)
			if total > maxBytes {
				return buf.Bytes()[:maxBytes], true, nil
			}
		}
		if err == io.EOF {
			return buf.Bytes(), false, nil
		}
		if err != nil {
			if cerr := ctx.Err(); cerr != nil {
				return nil, false, cerr
			}
			return nil, false, err
		}
	}
}
