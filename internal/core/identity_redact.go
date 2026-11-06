package core

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

const redactedMask = "***"

// minSubstringSecretLen is the minimum variant length replaced across arbitrary
// free-form text; shorter secrets are still masked in structured header, query,
// cookie, and Basic auth slots without corrupting ordinary words.
const minSubstringSecretLen = 4

// Redactor scrubs registered secrets, their encoded variants (Base64, URL, JSON,
// Basic user:pass), and sensitive transport slots from strings, errors, plans,
// and execution results. Reusable by History (B06), Runner/Export (B08/A01),
// and Desktop diagnostics.
type Redactor struct {
	rawSecrets     map[string]struct{}
	variants       map[string]struct{}
	replacer       *strings.Replacer
	sensitiveHdrs  map[string]struct{}
	sensitiveQuery map[string]struct{}
	sensitiveCks   map[string]struct{}
}

// NewRedactor constructs a Redactor preconfigured with standard sensitive HTTP
// credential headers (Authorization, Proxy-Authorization, Cookie, Set-Cookie).
func NewRedactor() *Redactor {
	return &Redactor{
		rawSecrets: map[string]struct{}{},
		variants:   map[string]struct{}{},
		sensitiveHdrs: map[string]struct{}{
			"authorization":       {},
			"proxy-authorization": {},
			"cookie":              {},
			"set-cookie":          {},
		},
		sensitiveQuery: map[string]struct{}{},
		sensitiveCks:   map[string]struct{}{},
	}
}

// HasSecrets reports whether any secret value or sensitive transport key has
// been registered with r.
func (r *Redactor) HasSecrets() bool {
	if r == nil {
		return false
	}
	return len(r.rawSecrets) > 0 || len(r.variants) > 0 || len(r.sensitiveQuery) > 0 || len(r.sensitiveCks) > 0
}

// RegisterSecret records secret and its common Base64, URL-escaped, and JSON-
// escaped encodings for redaction.
func (r *Redactor) RegisterSecret(secret string) {
	if r == nil || secret == "" {
		return
	}
	r.rawSecrets[secret] = struct{}{}
	r.addEncodedVariants(secret)
	r.rebuildReplacer()
}

// RegisterBasicPair records password (if non-empty) and the combined Basic
// credential "username:password" along with its Base64 and URL userinfo forms.
func (r *Redactor) RegisterBasicPair(username, password string) {
	if r == nil {
		return
	}
	if password != "" {
		r.rawSecrets[password] = struct{}{}
		r.addEncodedVariants(password)
	}
	if username != "" || password != "" {
		pair := username + ":" + password
		if password != "" {
			r.addVariant(pair)
		}
		b := []byte(pair)
		r.addVariant(base64.StdEncoding.EncodeToString(b))
		r.addVariant(base64.RawStdEncoding.EncodeToString(b))
		r.addVariant(base64.URLEncoding.EncodeToString(b))
		r.addVariant(base64.RawURLEncoding.EncodeToString(b))
		if ui := url.UserPassword(username, password).String(); ui != "" && password != "" {
			r.addVariant(ui)
		}
	}
	r.rebuildReplacer()
}

// RegisterSensitiveKey marks a custom header, query parameter, or cookie name
// (such as an API Key's configured key name) so its value is masked regardless
// of secret length.
func (r *Redactor) RegisterSensitiveKey(location, name string) {
	if r == nil {
		return
	}
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return
	}
	switch NormalizeAPIKeyLocation(location) {
	case "header":
		r.sensitiveHdrs[strings.ToLower(trimmed)] = struct{}{}
	case "query":
		r.sensitiveQuery[trimmed] = struct{}{}
	case "cookie":
		r.sensitiveCks[trimmed] = struct{}{}
	}
}

func (r *Redactor) addEncodedVariants(val string) {
	r.addVariant(val)
	b := []byte(val)
	r.addVariant(base64.StdEncoding.EncodeToString(b))
	r.addVariant(base64.RawStdEncoding.EncodeToString(b))
	r.addVariant(base64.URLEncoding.EncodeToString(b))
	r.addVariant(base64.RawURLEncoding.EncodeToString(b))
	r.addVariant(url.QueryEscape(val))
	r.addVariant(url.PathEscape(val))
	if jb, err := json.Marshal(val); err == nil && len(jb) >= 2 {
		r.addVariant(string(jb[1 : len(jb)-1]))
	}
	if q := strconv.Quote(val); len(q) >= 2 {
		r.addVariant(q[1 : len(q)-1])
	}
}

func (r *Redactor) addVariant(v string) {
	if len(v) < minSubstringSecretLen || v == redactedMask {
		return
	}
	r.variants[v] = struct{}{}
}

func (r *Redactor) rebuildReplacer() {
	if len(r.variants) == 0 {
		r.replacer = nil
		return
	}
	keys := make([]string, 0, len(r.variants))
	for k := range r.variants {
		keys = append(keys, k)
	}
	// Longest-first order gives longest-match priority in strings.NewReplacer.
	sort.Slice(keys, func(i, j int) bool {
		if len(keys[i]) != len(keys[j]) {
			return len(keys[i]) > len(keys[j])
		}
		return keys[i] < keys[j]
	})
	pairs := make([]string, 0, len(keys)*2)
	for _, k := range keys {
		pairs = append(pairs, k, redactedMask)
	}
	r.replacer = strings.NewReplacer(pairs...)
}

// RedactString replaces all registered secrets and encoded variants in s with "***".
func (r *Redactor) RedactString(s string) string {
	if r == nil || s == "" {
		return s
	}
	if _, exact := r.rawSecrets[s]; exact {
		return redactedMask
	}
	if r.replacer == nil {
		return s
	}
	return r.replacer.Replace(s)
}

// RedactError scrubs sensitive values from err's message while preserving error
// classification (*ConfigError, *RunError, *ConflictError) for errors.As callers.
func (r *Redactor) RedactError(err error) error {
	if err == nil || r == nil {
		return err
	}
	var cfe *ConflictError
	if errors.As(err, &cfe) {
		cp := *cfe
		cp.Path = r.RedactString(cfe.Path)
		return &cp
	}
	msg := r.RedactString(err.Error())
	var ce *ConfigError
	if errors.As(err, &ce) {
		return &ConfigError{Err: errors.New(msg)}
	}
	var re *RunError
	if errors.As(err, &re) {
		return &RunError{Err: errors.New(msg)}
	}
	return errors.New(msg)
}

// SanitizePlan returns a deep copy of plan with sensitive headers, query
// parameters, cookies, URL userinfo, and body payloads masked for safe logging,
// history storage, or diagnostic export.
func (r *Redactor) SanitizePlan(plan *Plan) *Plan {
	if plan == nil {
		return nil
	}
	red := r
	if red == nil {
		red = NewRedactor()
	}

	cp := &Plan{
		Method:  plan.Method,
		URL:     red.sanitizeURL(plan.URL),
		Timeout: plan.Timeout,
		HasBody: plan.HasBody,
	}
	if len(plan.Headers) > 0 {
		cp.Headers = make([][2]string, len(plan.Headers))
		for i, kv := range plan.Headers {
			cp.Headers[i] = [2]string{red.RedactString(kv[0]), red.sanitizeHeaderValue(kv[0], kv[1])}
		}
	}
	if len(plan.Query) > 0 {
		cp.Query = make([][2]string, len(plan.Query))
		for i, kv := range plan.Query {
			val := kv[1]
			if _, ok := red.sensitiveQuery[kv[0]]; ok {
				val = redactedMask
			} else if _, exact := red.rawSecrets[val]; exact {
				val = redactedMask
			} else {
				val = red.RedactString(val)
			}
			cp.Query[i] = [2]string{red.RedactString(kv[0]), val}
		}
	}
	if len(plan.Body) > 0 {
		cp.Body = []byte(red.RedactString(string(plan.Body)))
	}
	return cp
}

// SanitizeExecResult returns a deep copy of res with Set-Cookie, sensitive
// headers, Body, and Pretty masked so echoed secrets cannot leak into history
// or error bundles.
func (r *Redactor) SanitizeExecResult(res *ExecResult) *ExecResult {
	if res == nil {
		return nil
	}
	red := r
	if red == nil {
		red = NewRedactor()
	}
	cp := *res
	if len(res.Headers) > 0 {
		cp.Headers = make([][2]string, len(res.Headers))
		for i, kv := range res.Headers {
			cp.Headers[i] = [2]string{red.RedactString(kv[0]), red.sanitizeHeaderValue(kv[0], kv[1])}
		}
	}
	if len(res.Body) > 0 {
		redBody := red.RedactString(string(res.Body))
		cp.Body = []byte(redBody)
		cp.BodySize = int64(len(cp.Body))
	}
	if res.Pretty != "" {
		cp.Pretty = red.RedactString(res.Pretty)
	}
	return &cp
}

func (r *Redactor) sanitizeURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return r.RedactString(raw)
	}
	if u.User != nil {
		user := r.RedactString(u.User.Username())
		if _, hasPwd := u.User.Password(); hasPwd {
			u.User = url.UserPassword(user, redactedMask)
		} else {
			u.User = url.User(user)
		}
	}
	if u.RawQuery != "" {
		q := u.Query()
		changed := false
		for k, vals := range q {
			_, keySensitive := r.sensitiveQuery[k]
			for i, v := range vals {
				if keySensitive {
					vals[i] = redactedMask
					changed = true
				} else if _, exact := r.rawSecrets[v]; exact {
					vals[i] = redactedMask
					changed = true
				} else if rv := r.RedactString(v); rv != v {
					vals[i] = rv
					changed = true
				}
			}
		}
		if changed {
			u.RawQuery = strings.ReplaceAll(q.Encode(), "%2A%2A%2A", redactedMask)
		}
	}
	out := u.String()
	out = strings.ReplaceAll(out, ":%2A%2A%2A@", ":"+redactedMask+"@")
	return r.RedactString(out)
}

func (r *Redactor) sanitizeHeaderValue(name, val string) string {
	lk := strings.ToLower(strings.TrimSpace(name))
	switch lk {
	case "authorization", "proxy-authorization":
		if scheme, _, ok := strings.Cut(strings.TrimSpace(val), " "); ok && scheme != "" {
			return scheme + " " + redactedMask
		}
		return redactedMask
	case "cookie":
		parts := strings.Split(val, ";")
		for i, p := range parts {
			trimmed := strings.TrimSpace(p)
			if ckName, _, ok := strings.Cut(trimmed, "="); ok {
				parts[i] = strings.TrimSpace(ckName) + "=" + redactedMask
			} else if trimmed != "" {
				parts[i] = redactedMask
			}
		}
		return strings.Join(parts, "; ")
	case "set-cookie":
		if first, rest, ok := strings.Cut(val, ";"); ok {
			if ckName, _, eq := strings.Cut(strings.TrimSpace(first), "="); eq {
				return strings.TrimSpace(ckName) + "=" + redactedMask + ";" + r.RedactString(rest)
			}
		}
		if ckName, _, eq := strings.Cut(strings.TrimSpace(val), "="); eq {
			return strings.TrimSpace(ckName) + "=" + redactedMask
		}
		return redactedMask
	}
	if _, ok := r.sensitiveHdrs[lk]; ok {
		return redactedMask
	}
	if _, exact := r.rawSecrets[val]; exact {
		return redactedMask
	}
	return r.RedactString(val)
}

// trackingSecrets wraps a SecretProvider to record every retrieved secret value
// into red immediately at lookup time, so even errors occurring later during
// URL/body construction can be scrubbed before surfacing.
type trackingSecrets struct {
	inner SecretProvider
	red   *Redactor
}

func (t *trackingSecrets) Get(name string) (string, bool) {
	if t.inner == nil {
		return "", false
	}
	v, ok := t.inner.Get(name)
	if ok && t.red != nil {
		t.red.RegisterSecret(v)
	}
	return v, ok
}

func (t *trackingSecrets) InspectSecret(name string) SecretStatus {
	return InspectSecretFrom(t.inner, name)
}

// BuildTrackedPlan resolves req into a ready-to-execute *Plan while tracking
// all touched secrets, runtime sensitive step outputs, Basic user:pass pairs,
// and custom API Key slots into the returned *Redactor. Any error produced
// during resolution is scrubbed via redactor.RedactError before returning.
func BuildTrackedPlan(req *Request, vars map[string]string, secrets SecretProvider, authScheme *AuthScheme, opts *ResolveOptions) (*Plan, *Redactor, error) {
	return BuildTrackedPlanWithOptions(context.Background(), req, vars, secrets, authScheme, opts, PlanOptions{})
}

// BuildTrackedPlanWithOptions combines sensitive-value tracking with bounded file/body encoding.
func BuildTrackedPlanWithOptions(ctx context.Context, req *Request, vars map[string]string, secrets SecretProvider, authScheme *AuthScheme, opts *ResolveOptions, bodyOpts PlanOptions) (*Plan, *Redactor, error) {
	red := NewRedactor()
	if req == nil || req.HTTP == nil {
		return nil, red, configErrorf("request has no http definition")
	}

	merged := MergeVars(vars)
	if opts != nil {
		for _, extra := range []map[string]string{opts.RunVars, opts.StepVars, opts.OverrideVars} {
			for k, v := range extra {
				if !IsReservedNamespace(k) {
					merged[k] = v
				}
			}
		}
		for _, so := range opts.StepOutputs {
			if so.Sensitive && so.Value != "" {
				red.RegisterSecret(so.Value)
			}
		}
	}

	var sp SecretProvider
	if secrets != nil {
		sp = &trackingSecrets{inner: secrets, red: red}
	}
	r := NewResolver(merged, sp)
	if opts != nil {
		r.WithDataVars(opts.DataVars)
		r.WithStepOutputs(opts.StepOutputs)
	}

	method := strings.ToUpper(strings.TrimSpace(req.HTTP.Method))
	if method == "" {
		method = http.MethodGet
	}
	if err := ValidateHTTPMethod(method); err != nil {
		return nil, red, red.RedactError(configErrorf("invalid HTTP method %q", req.HTTP.Method))
	}

	rawURL, err := r.Resolve(req.HTTP.URL)
	if err != nil {
		return nil, red, red.RedactError(err)
	}
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return nil, red, configErrorf("request url is empty")
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, red, red.RedactError(configErrorf("invalid url %q: %v", rawURL, err))
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return nil, red, red.RedactError(configErrorf("url %q must be absolute (scheme and host)", rawURL))
	}

	var headers [][2]string
	for _, kv := range req.HTTP.Headers {
		if !kv.IsEnabled() {
			continue
		}
		name, err := r.Resolve(kv.Name)
		if err != nil {
			return nil, red, red.RedactError(err)
		}
		val, err := r.Resolve(kv.Value)
		if err != nil {
			return nil, red, red.RedactError(err)
		}
		if strings.ContainsAny(name, "\r\n") || strings.ContainsAny(val, "\r\n") {
			return nil, red, red.RedactError(configErrorf("header %q contains a newline; refusing header injection", name))
		}
		if strings.TrimSpace(name) == "" {
			return nil, red, configErrorf("header name is empty")
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
			return nil, red, red.RedactError(err)
		}
		val, err := r.Resolve(kv.Value)
		if err != nil {
			return nil, red, red.RedactError(err)
		}
		query = append(query, [2]string{name, val})
	}

	if err := trackAndApplyAuth(authScheme, r, red, &headers, &query); err != nil {
		return nil, red, red.RedactError(err)
	}

	encoded, err := BuildBody(ctx, req.HTTP.Body, r, bodyOpts)
	if err != nil {
		return nil, red, red.RedactError(err)
	}
	if encoded.HasBody && encoded.ContentType != "" {
		headers = applyBodyContentType(headers, encoded.ContentType, encoded.ForceContentType)
	}

	timeout := DefaultTimeout
	if req.Timeout != "" {
		d, err := time.ParseDuration(req.Timeout)
		if err != nil {
			return nil, red, red.RedactError(configErrorf("invalid timeout %q: %v", req.Timeout, err))
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
		Body:    encoded.Bytes,
		Timeout: timeout,
		HasBody: encoded.HasBody,
	}, red, nil
}

func trackAndApplyAuth(scheme *AuthScheme, r *Resolver, red *Redactor, headers *[][2]string, query *[][2]string) error {
	if scheme != nil {
		switch strings.ToLower(strings.TrimSpace(scheme.Type)) {
		case "bearer":
			if tok, err := r.Resolve(scheme.Token); err == nil && tok != "" {
				red.RegisterSecret(tok)
			}
		case "basic":
			user, errU := r.Resolve(scheme.Username)
			pass, errP := r.Resolve(scheme.Password)
			if errU == nil && errP == nil {
				red.RegisterBasicPair(user, pass)
			}
		case "apikey":
			keyName, errN := r.Resolve(scheme.Name)
			val, errV := r.Resolve(scheme.Value)
			loc, errL := ValidateAPIKeyLocation(scheme.In)
			if errN == nil && errL == nil {
				red.RegisterSensitiveKey(loc, keyName)
			}
			if errV == nil && val != "" {
				red.RegisterSecret(val)
			}
		}
	}
	return applyAuth(scheme, r, headers, query)
}
