package core

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
	if req == nil || req.HTTP == nil {
		return nil, configErrorf("request has no http definition")
	}
	r := NewResolver(vars, secrets)

	method := strings.ToUpper(strings.TrimSpace(req.HTTP.Method))
	if method == "" {
		method = http.MethodGet
	}
	if strings.ContainsAny(method, " \t\r\n") {
		return nil, configErrorf("invalid HTTP method %q", req.HTTP.Method)
	}

	rawURL, err := r.Resolve(req.HTTP.URL)
	if err != nil {
		return nil, err
	}
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return nil, configErrorf("request url is empty")
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, configErrorf("invalid url %q: %v", rawURL, err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return nil, configErrorf("url %q must be absolute (scheme and host)", rawURL)
	}

	var headers [][2]string
	for _, kv := range req.HTTP.Headers {
		if !kv.IsEnabled() {
			continue
		}
		name, err := r.Resolve(kv.Name)
		if err != nil {
			return nil, err
		}
		val, err := r.Resolve(kv.Value)
		if err != nil {
			return nil, err
		}
		if strings.ContainsAny(name, "\r\n") || strings.ContainsAny(val, "\r\n") {
			return nil, configErrorf("header %q contains a newline; refusing header injection", name)
		}
		if strings.TrimSpace(name) == "" {
			return nil, configErrorf("header name is empty")
		}
		headers = append(headers, [2]string{name, val})
	}

	var query [][2]string
	for _, kv := range req.HTTP.Query {
		if !kv.IsEnabled() {
			continue
		}
		name, err := r.Resolve(kv.Name)
		if err != nil {
			return nil, err
		}
		val, err := r.Resolve(kv.Value)
		if err != nil {
			return nil, err
		}
		query = append(query, [2]string{name, val})
	}

	if err := applyAuth(authScheme, r, &headers, &query); err != nil {
		return nil, err
	}

	var bodyBytes []byte
	hasBody := false
	if req.HTTP.Body != nil {
		encBody, err := BuildBody(context.Background(), req.HTTP.Body, r, PlanOptions{})
		if err != nil {
			return nil, err
		}
		bodyBytes = encBody.Bytes
		hasBody = encBody.HasBody
		if encBody.HasBody && encBody.ContentType != "" {
			headers = applyBodyContentType(headers, encBody.ContentType, encBody.ForceContentType)
		}
	}

	timeout := DefaultTimeout
	if req.Timeout != "" {
		d, err := time.ParseDuration(req.Timeout)
		if err != nil {
			return nil, configErrorf("invalid timeout %q: %v", req.Timeout, err)
		}
		if d > 0 {
			timeout = d
		}
	}

	return &Plan{
		Method:  method,
		URL:     rawURL,
		Headers: headers,
		Query:   query,
		Body:    bodyBytes,
		Timeout: timeout,
		HasBody: hasBody,
	}, nil
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
	Status    int
	Proto     string
	Duration  time.Duration
	Body      []byte
	BodySize  int64
	Truncated bool
	Headers   [][2]string
	Pretty    string
}

// Execute sends a plan and reads a bounded response. It honors ctx cancellation
// and the plan's timeout, and never follows redirects to a different scheme
// host while forwarding credentials (Go's client strips sensitive headers on
// cross-origin redirects by default).
func Execute(ctx context.Context, plan *Plan, client *http.Client) (*ExecResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if plan == nil {
		return nil, configErrorf("nil plan")
	}
	if plan.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, plan.Timeout)
		defer cancel()
	}

	u, err := url.Parse(plan.URL)
	if err != nil {
		return nil, configErrorf("invalid url: %v", err)
	}
	if len(plan.Query) > 0 {
		q := u.Query()
		for _, kv := range plan.Query {
			q.Add(kv[0], kv[1])
		}
		u.RawQuery = q.Encode()
	}

	var bodyReader io.Reader
	if plan.HasBody {
		bodyReader = bytes.NewReader(plan.Body)
	}
	req, err := http.NewRequestWithContext(ctx, plan.Method, u.String(), bodyReader)
	if err != nil {
		return nil, configErrorf("cannot build request: %v", err)
	}
	for _, h := range plan.Headers {
		req.Header.Add(h[0], h[1])
	}

	if client == nil {
		client = &http.Client{}
	}

	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() == context.Canceled {
			return nil, &RunError{Err: fmt.Errorf("request canceled")}
		}
		if ctx.Err() == context.DeadlineExceeded {
			return nil, &RunError{Err: fmt.Errorf("request timed out after %s", plan.Timeout)}
		}
		return nil, &RunError{Err: fmt.Errorf("request failed: %w", err)}
	}
	defer resp.Body.Close()

	limited := io.LimitReader(resp.Body, DefaultMaxBody+1)
	data, readErr := io.ReadAll(limited)
	if readErr != nil {
		return nil, &RunError{Err: fmt.Errorf("reading response body: %w", readErr)}
	}
	truncated := false
	if int64(len(data)) > DefaultMaxBody {
		data = data[:DefaultMaxBody]
		truncated = true
	}

	res := &ExecResult{
		Status:    resp.StatusCode,
		Proto:     resp.Proto,
		Duration:  time.Since(start),
		Body:      data,
		BodySize:  int64(len(data)),
		Truncated: truncated,
		Headers:   flattenHeader(resp.Header),
	}
	if json.Valid(data) {
		var pretty bytes.Buffer
		if json.Indent(&pretty, data, "", "  ") == nil {
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
