package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// DefaultTimeout applies when neither the request nor project sets one.
const DefaultTimeout = 10 * time.Second

// DefaultMaxBody caps how many response bytes are read into memory. Larger
// bodies are truncated and flagged, never silently dropped.
const DefaultMaxBody int64 = 32 << 20

// Plan is a fully resolved, immutable request ready to send. Building it
// separates variable/auth resolution (which can fail as a config error before
// any network call) from execution.
type Plan struct {
	Method  string
	URL     string
	Headers [][2]string
	Query   [][2]string
	Body    []byte
	Timeout time.Duration
	HasBody bool
	// Network holds optional stage timeouts, redirect policy, proxy, TLS/mTLS,
	// CookieJar, and target policy options for Execute.
	Network *ExecuteOptions
}

func renderNode(n *yaml.Node, r *Resolver) (any, error) {
	switch n.Kind {
	case yaml.DocumentNode:
		if len(n.Content) == 0 {
			return nil, configErrorf("empty body")
		}
		return renderNode(n.Content[0], r)
	case yaml.MappingNode:
		m := make(map[string]any, len(n.Content)/2)
		for i := 0; i+1 < len(n.Content); i += 2 {
			k := n.Content[i].Value
			v, err := renderNode(n.Content[i+1], r)
			if err != nil {
				return nil, err
			}
			m[k] = v
		}
		return m, nil
	case yaml.SequenceNode:
		arr := make([]any, 0, len(n.Content))
		for _, c := range n.Content {
			v, err := renderNode(c, r)
			if err != nil {
				return nil, err
			}
			arr = append(arr, v)
		}
		return arr, nil
	case yaml.ScalarNode:
		if n.Tag == "!!str" || n.Tag == "" {
			return r.Resolve(n.Value)
		}
		var v any
		if err := n.Decode(&v); err != nil {
			return nil, err
		}
		return v, nil
	default:
		return nil, configErrorf("unsupported body node kind %d", n.Kind)
	}
}

// BuildPlan resolves a request into a Plan. vars are the merged variables;
// authScheme is the resolved reusable scheme (nil for none).
func BuildPlan(req *Request, vars map[string]string, secrets SecretProvider, authScheme *AuthScheme) (*Plan, error) {
	return BuildPlanWithOptions(context.Background(), req, vars, secrets, authScheme, PlanOptions{})
}

func ensureContentType(headers [][2]string, ct string) [][2]string {
	for _, h := range headers {
		if strings.EqualFold(h[0], "Content-Type") {
			return headers
		}
	}
	return append(headers, [2]string{"Content-Type", ct})
}

// ExecResult is the outcome of one request: transport facts, not a pass/fail
// judgement (that belongs to assertions in later phases).
type ExecResult struct {
	Status            int
	Proto             string
	Duration          time.Duration
	Body              []byte
	BodySize          int64
	TransferSize      int64
	ContentEncoding   string
	Decompressed      bool
	Truncated         bool
	Headers           [][2]string
	Pretty            string
	ContentType       string
	IsBinary          bool
	SuggestedFilename string
	FinalURL          string
	Redirects         []RedirectHop
	Cookies           []CookieEntry
	Trace             *NetworkTrace
}

// Execute sends a plan and reads a bounded response. It honors ctx cancellation
// and the plan's timeout, and never follows redirects to a different scheme or
// host while forwarding credentials. If plan.Network is populated, its stage
// timeouts, proxy, TLS/mTLS, redirect policy, CookieJar, and target policy are
// applied automatically.
func Execute(ctx context.Context, plan *Plan, client *http.Client) (*ExecResult, error) {
	var opts ExecuteOptions
	if plan != nil && plan.Network != nil {
		opts = *plan.Network
	}
	if client != nil {
		opts.Client = client
	}
	return ExecuteWithOptions(ctx, plan, opts)
}

// ExecuteWithOptions sends plan using the transport, stage timeouts, proxy,
// TLS/mTLS, redirect policy, CookieJar, and target policy in opts.
func ExecuteWithOptions(ctx context.Context, plan *Plan, opts ExecuteOptions) (*ExecResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	parentCtx := ctx
	redactErr := func(err error) error {
		if opts.Redactor != nil {
			return opts.Redactor.RedactError(err)
		}
		return err
	}
	if plan == nil {
		return nil, configErrorf("nil plan")
	}

	effectiveTimeout := opts.Timeouts.TotalTimeout
	if effectiveTimeout <= 0 {
		effectiveTimeout = plan.Timeout
	}
	if effectiveTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, effectiveTimeout)
		defer cancel()
	}

	u, err := url.Parse(strings.TrimSpace(plan.URL))
	if err != nil {
		return nil, redactErr(configErrorf("invalid url: %v", err))
	}
	if len(plan.Query) > 0 {
		q := u.Query()
		for _, kv := range plan.Query {
			q.Add(kv[0], kv[1])
		}
		u.RawQuery = q.Encode()
	}

	method := strings.ToUpper(strings.TrimSpace(plan.Method))
	if method == "" {
		method = http.MethodGet
	}

	if !opts.DisableCookies && opts.CookieJar != nil && opts.Redactor != nil {
		opts.CookieJar.RegisterSecrets(opts.Redactor)
	}

	currentHeaders := MergeHeadersWithCookies(plan.Headers, u, opts.CookieJar, opts.DisableCookies)
	policy := opts.EffectiveTargetPolicy()

	// Evaluate Hop 0 against target policy when configured.
	if opts.HasTargetPolicy {
		hasCreds := hasRequestCredentials(currentHeaders, u.String(), opts.AuthScheme, opts.CookieJar, opts.DisableCookies)
		dec, err := EvaluateTarget(policy, TargetCheckInput{
			URL:            u.String(),
			Method:         method,
			EnvID:          opts.EnvID,
			HasCredentials: hasCreds,
			Interactive:    opts.Interactive,
			Confirmed:      opts.Confirmed,
		})
		if err != nil {
			return nil, redactErr(err)
		}
		if dec != nil && dec.Outcome == DecisionConfirmRequired && !opts.Confirmed {
			return nil, redactErr(configErrorf("target policy requires confirmation: %s", strings.Join(dec.Reasons, "; ")))
		}
	}

	// Automatically advertise gzip/deflate when the caller has not set an
	// explicit Accept-Encoding header and auto-decompression is enabled.
	wireHeaders := make([][2]string, len(currentHeaders))
	copy(wireHeaders, currentHeaders)
	if !opts.DisableAutoDecompress && !hasHeaderKey(wireHeaders, "Accept-Encoding") {
		wireHeaders = append(wireHeaders, [2]string{"Accept-Encoding", "gzip, deflate"})
	}

	tc, clientTrace := newTraceCollector()
	traceCtx := httptrace.WithClientTrace(ctx, clientTrace)

	var bodyReader io.Reader
	if plan.HasBody {
		bodyReader = bytes.NewReader(plan.Body)
	}
	req, err := http.NewRequestWithContext(traceCtx, method, u.String(), bodyReader)
	if err != nil {
		return nil, redactErr(configErrorf("cannot build request: %v", err))
	}
	if plan.HasBody {
		bodyCopy := plan.Body
		req.GetBody = func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(bodyCopy)), nil
		}
	}
	applyHeadersToHTTPRequest(req, wireHeaders)

	var (
		execClient http.Client
		builtTLS   *builtTLSResult
		ownTrans   *http.Transport
	)

	if opts.Client != nil {
		execClient = *opts.Client
		// Still validate TLS policy / cert config if caller explicitly provided TLS options.
		if opts.TLS.InsecureSkipVerify || opts.TLS.CustomCARef != "" || len(opts.TLS.CustomCAPEM) > 0 ||
			opts.TLS.ClientCertRef != "" || opts.TLS.ClientKeyRef != "" ||
			len(opts.TLS.ClientCertPEM) > 0 || len(opts.TLS.ClientKeyPEM) > 0 {
			bt, err := buildStdTLSConfig(ctx, opts.TLS, policy, opts, opts.Redactor)
			if err != nil {
				return nil, redactErr(err)
			}
			builtTLS = bt
		}
	} else {
		rp, err := resolveProxyConfig(opts.Proxy, opts.Secrets, opts.Redactor)
		if err != nil {
			return nil, redactErr(err)
		}
		bt, err := buildStdTLSConfig(ctx, opts.TLS, policy, opts, opts.Redactor)
		if err != nil {
			return nil, redactErr(err)
		}
		builtTLS = bt

		dialTimeout := opts.Timeouts.DialTimeout
		if dialTimeout <= 0 {
			dialTimeout = DefaultDialTimeout
		}
		tlsTimeout := opts.Timeouts.TLSHandshakeTimeout
		if tlsTimeout <= 0 {
			tlsTimeout = DefaultTLSHandshakeTimeout
		}
		respHdrTimeout := opts.Timeouts.ResponseHeaderTimeout

		baseDialer := &net.Dialer{
			Timeout:   dialTimeout,
			KeepAlive: 30 * time.Second,
		}
		tr := &http.Transport{
			DialContext:           baseDialer.DialContext,
			TLSClientConfig:       builtTLS.config,
			TLSHandshakeTimeout:   tlsTimeout,
			ResponseHeaderTimeout: respHdrTimeout,
			DisableCompression:    true,
			ForceAttemptHTTP2:     rp.mode != ProxySOCKS5,
			MaxIdleConns:          16,
			IdleConnTimeout:       30 * time.Second,
		}
		configureTransportProxy(tr, baseDialer, rp, tc.setProxyUsed)
		ownTrans = tr
		execClient.Transport = tr
	}
	if ownTrans != nil {
		defer ownTrans.CloseIdleConnections()
	}

	var redirects []RedirectHop
	activeLogicalHeaders := make([][2]string, len(plan.Headers))
	copy(activeLogicalHeaders, plan.Headers)

	execClient.CheckRedirect = func(nextReq *http.Request, via []*http.Request) error {
		if len(via) == 0 {
			return nil
		}
		prevReq := via[len(via)-1]
		prevURL := prevReq.URL.String()
		nextURL := nextReq.URL.String()
		statusCode := 0
		if nextReq.Response != nil {
			statusCode = nextReq.Response.StatusCode
			if !opts.DisableCookies && opts.CookieJar != nil {
				opts.CookieJar.SetCookies(prevReq.URL, nextReq.Response.Cookies())
				if opts.Redactor != nil {
					opts.CookieJar.RegisterSecrets(opts.Redactor)
				}
			}
		}

		mode := opts.Redirect.EffectiveMode()
		if mode == RedirectNone {
			return http.ErrUseLastResponse
		}
		maxHops := opts.Redirect.EffectiveMaxHops()
		if len(via) > maxHops {
			return configErrorf("stopped after %d redirects (max redirects exceeded)", maxHops)
		}

		sameOrig, err := SameOrigin(prevURL, nextURL)
		if err != nil {
			return err
		}
		if mode == RedirectSameOriginOnly && !sameOrig {
			return configErrorf("redirect from %q to %q blocked by same-origin redirect policy", prevURL, nextURL)
		}

		kept, _, dec, err := EvaluateRedirectHop(
			policy,
			prevURL,
			nextURL,
			nextReq.Method,
			opts.EnvID,
			activeLogicalHeaders,
			opts.AuthScheme,
			opts.Interactive,
			opts.Confirmed,
		)
		if err != nil {
			return err
		}
		if dec != nil && dec.Outcome == DecisionConfirmRequired && !opts.Confirmed {
			return configErrorf("redirect target %q requires confirmation: %s", nextURL, strings.Join(dec.Reasons, "; "))
		}

		// Also strip any custom sensitive headers registered in Redactor on cross-origin hops.
		if !sameOrig && opts.Redactor != nil && len(opts.Redactor.sensitiveHdrs) > 0 {
			filtered := make([][2]string, 0, len(kept))
			for _, h := range kept {
				lk := strings.ToLower(strings.TrimSpace(h[0]))
				if _, sens := opts.Redactor.sensitiveHdrs[lk]; sens {
					continue
				}
				filtered = append(filtered, h)
			}
			kept = filtered
		}

		stripped := len(kept) < len(activeLogicalHeaders)
		if !sameOrig && !stripped {
			if prevReq.Header.Get("Authorization") != "" || prevReq.Header.Get("Cookie") != "" || prevReq.Header.Get("Proxy-Authorization") != "" {
				stripped = true
			}
		}
		activeLogicalHeaders = kept

		// Rebuild request headers for the redirect target.
		hopHeaders := MergeHeadersWithCookies(activeLogicalHeaders, nextReq.URL, opts.CookieJar, opts.DisableCookies)
		if !opts.DisableAutoDecompress && !hasHeaderKey(hopHeaders, "Accept-Encoding") {
			hopHeaders = append(hopHeaders, [2]string{"Accept-Encoding", "gzip, deflate"})
		}
		// Preserve Content-Type/Content-Length only when the redirect preserves the body (307/308).
		if nextReq.Body == nil {
			cleaned := make([][2]string, 0, len(hopHeaders))
			for _, h := range hopHeaders {
				if strings.EqualFold(h[0], "Content-Type") || strings.EqualFold(h[0], "Content-Length") {
					continue
				}
				cleaned = append(cleaned, h)
			}
			hopHeaders = cleaned
		}
		applyHeadersToHTTPRequest(nextReq, hopHeaders)

		redirects = append(redirects, RedirectHop{
			Hop:                 len(via),
			StatusCode:          statusCode,
			Method:              nextReq.Method,
			FromURL:             prevURL,
			ToURL:               nextURL,
			OriginChanged:       !sameOrig,
			StrippedCredentials: stripped,
		})
		return nil
	}

	start := time.Now()
	resp, err := execClient.Do(req)
	if err != nil {
		var ce *ConfigError
		if errors.As(err, &ce) {
			return nil, redactErr(ce)
		}
		if parentCtx.Err() == context.Canceled {
			return nil, &RunError{Err: fmt.Errorf("request canceled")}
		}
		if ctx.Err() == context.DeadlineExceeded {
			return nil, &RunError{Err: fmt.Errorf("request timed out after %s", effectiveTimeout)}
		}
		if ctx.Err() == context.Canceled {
			return nil, &RunError{Err: fmt.Errorf("request canceled")}
		}
		return nil, redactErr(classifyTransportError(err))
	}

	// Ensure context cancellation during body download closes resp.Body immediately
	// so blocked network reads wake up and release sockets without delay.
	bodyDone := make(chan struct{})
	defer func() {
		close(bodyDone)
		_ = resp.Body.Close()
	}()
	go func() {
		select {
		case <-ctx.Done():
			_ = resp.Body.Close()
		case <-bodyDone:
		}
	}()

	downloadStart := time.Now()
	maxBody := opts.EffectiveMaxResponseBytes()
	payload, readErr := readAndDecodeResponseBody(ctx, resp, req.Method, maxBody, opts.DisableAutoDecompress)
	downloadDur := time.Since(downloadStart)
	if readErr != nil {
		if parentCtx.Err() == context.Canceled {
			return nil, &RunError{Err: fmt.Errorf("request canceled")}
		}
		if ctx.Err() == context.DeadlineExceeded {
			return nil, &RunError{Err: fmt.Errorf("request timed out after %s", effectiveTimeout)}
		}
		if ctx.Err() == context.Canceled {
			return nil, &RunError{Err: fmt.Errorf("request canceled")}
		}
		return nil, redactErr(&RunError{Err: fmt.Errorf("reading response body: %w", readErr)})
	}

	finalURL := u.String()
	if resp.Request != nil && resp.Request.URL != nil {
		finalURL = resp.Request.URL.String()
		if !opts.DisableCookies && opts.CookieJar != nil {
			opts.CookieJar.SetCookies(resp.Request.URL, resp.Cookies())
			if opts.Redactor != nil {
				opts.CookieJar.RegisterSecrets(opts.Redactor)
			}
		}
	}

	flatHeaders := flattenHeader(resp.Header)
	respCookies := ParseResponseCookies(finalURL, flatHeaders)
	if opts.Redactor != nil {
		for _, ck := range respCookies {
			if ck.Name != "" {
				opts.Redactor.RegisterSensitiveKey("cookie", ck.Name)
			}
			if ck.Value != "" {
				opts.Redactor.RegisterSecret(ck.Value)
			}
		}
	}

	contentType, isBinary, suggestedName := InspectPayload(finalURL, flatHeaders, payload.body)

	res := &ExecResult{
		Status:            resp.StatusCode,
		Proto:             resp.Proto,
		Duration:          time.Since(start),
		Body:              payload.body,
		BodySize:          payload.bodySize,
		TransferSize:      payload.transferSize,
		ContentEncoding:   payload.contentEncoding,
		Decompressed:      payload.decompressed,
		Truncated:         payload.truncated,
		Headers:           flatHeaders,
		ContentType:       contentType,
		IsBinary:          isBinary,
		SuggestedFilename: suggestedName,
		FinalURL:          finalURL,
		Redirects:         redirects,
		Cookies:           respCookies,
		Trace:             tc.snapshot(resp, downloadDur, builtTLS),
	}
	if !isBinary && json.Valid(payload.body) {
		var pretty bytes.Buffer
		if json.Indent(&pretty, payload.body, "", "  ") == nil {
			res.Pretty = pretty.String()
		}
	}
	return res, nil
}

func flattenHeader(h http.Header) [][2]string {
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
