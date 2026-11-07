package mockserver

import (
	"encoding/json"
	"fmt"
	"html"
	"math/rand/v2"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"time"

	"gorequest/internal/core"
)

// IncomingRequest is the normalized representation of an HTTP request evaluated
// by CompiledSnapshot.Evaluate.
type IncomingRequest struct {
	Seq     uint64
	Method  string
	Path    string
	Query   url.Values
	Headers http.Header
	Body    []byte
}

// NearMiss records why a candidate rule did not match an IncomingRequest.
type NearMiss struct {
	RuleID   string `json:"ruleId"`
	Priority int    `json:"priority"`
	Stage    string `json:"stage"` // method | path | pathParam | query | header | body
	Reason   string `json:"reason"`
}

// MatchOutcome holds the result of evaluating an IncomingRequest against a
// CompiledSnapshot.
type MatchOutcome struct {
	Matched        bool
	RuleID         string
	RuleName       string
	FileID         string
	Priority       int
	Scenario       string
	Status         int
	Headers        [][2]string
	Body           []byte
	Delay          time.Duration
	EvaluatedRules int
	NearMisses     []NearMiss
	Diagnostic     string
}

// Evaluate tests req against the compiled rules in s in deterministic
// (Priority DESC, ID ASC) order. If no rule matches, it returns a structured
// 404 JSON response with sanitized near-miss diagnostics.
func (s *CompiledSnapshot) Evaluate(req IncomingRequest, red *core.Redactor) MatchOutcome {
	if s == nil {
		return unmatchedOutcome(req, "default", 0, nil, red)
	}
	method := strings.ToUpper(strings.TrimSpace(req.Method))
	normPath := normalizeURLPath(req.Path)
	var reqSegments []string
	if normPath != "/" {
		reqSegments = strings.Split(strings.TrimPrefix(normPath, "/"), "/")
	}

	var parsedJSON any
	var parsedJSONReady bool
	var parsedJSONErr error
	getParsedJSON := func() (any, error) {
		if !parsedJSONReady {
			parsedJSONReady = true
			if len(bytesTrimSpace(req.Body)) == 0 {
				parsedJSONErr = fmt.Errorf("request body is empty")
			} else {
				dec := json.NewDecoder(strings.NewReader(string(req.Body)))
				dec.UseNumber()
				parsedJSONErr = dec.Decode(&parsedJSON)
			}
		}
		return parsedJSON, parsedJSONErr
	}

	var nearMisses []NearMiss
	for _, cr := range s.Rules {
		if cr.Method != method {
			continue
		}
		pathParams, ok := matchPathSegments(cr.Path, reqSegments)
		if !ok {
			continue
		}
		// Path & method matched; check path parameter constraints
		failedStage, failedReason := "", ""
		for pname, cvm := range cr.PathParams {
			val := pathParams[pname]
			if !matchValue(cvm, val, false) {
				failedStage = "pathParam"
				failedReason = fmt.Sprintf("path param %q did not satisfy condition", pname)
				break
			}
		}
		if failedStage == "" {
			for _, qm := range cr.Query {
				vals, exists := lookupQueryValues(req.Query, qm.name, qm.ignoreCase)
				if !matchMultiValues(qm.vm, vals, exists, qm.ignoreCase) {
					failedStage = "query"
					failedReason = fmt.Sprintf("query param %q did not satisfy condition", qm.name)
					break
				}
			}
		}
		if failedStage == "" {
			for _, hm := range cr.Headers {
				vals, exists := lookupHeaderValues(req.Headers, hm.name)
				if !matchMultiValues(hm.vm, vals, exists, hm.ignoreCase) {
					failedStage = "header"
					failedReason = fmt.Sprintf("header %q did not satisfy condition", hm.name)
					break
				}
			}
		}
		if failedStage == "" && cr.Body != nil {
			if ok, reason := matchBody(cr.Body, req.Body, getParsedJSON); !ok {
				failedStage = "body"
				failedReason = reason
			}
		}
		if failedStage != "" {
			if len(nearMisses) < 8 {
				if red != nil {
					failedReason = red.RedactString(failedReason)
				}
				nearMisses = append(nearMisses, NearMiss{
					RuleID:   cr.Rule.ID,
					Priority: cr.Rule.Priority,
					Stage:    failedStage,
					Reason:   failedReason,
				})
			}
			continue
		}

		// Matched! Render deterministic response and delay.
		return renderMatchedOutcome(s, cr, req, pathParams)
	}

	return unmatchedOutcome(req, s.Scenario, len(s.Rules), nearMisses, red)
}

func matchPathSegments(cp compiledPath, reqSegments []string) (map[string]string, bool) {
	if len(cp.segments) != len(reqSegments) {
		return nil, false
	}
	var params map[string]string
	for i, seg := range cp.segments {
		actual := reqSegments[i]
		if !seg.isParam {
			if seg.literal != actual {
				return nil, false
			}
			continue
		}
		if params == nil {
			params = make(map[string]string, 2)
		}
		decoded, err := url.PathUnescape(actual)
		if err != nil {
			decoded = actual
		}
		params[seg.param] = decoded
	}
	return params, true
}

func lookupQueryValues(q url.Values, name string, ignoreCase bool) ([]string, bool) {
	if len(q) == 0 {
		return nil, false
	}
	if !ignoreCase {
		vals, ok := q[name]
		return vals, ok
	}
	var out []string
	found := false
	for k, vals := range q {
		if strings.EqualFold(k, name) {
			found = true
			out = append(out, vals...)
		}
	}
	return out, found
}

func lookupHeaderValues(h http.Header, lowerName string) ([]string, bool) {
	if len(h) == 0 {
		return nil, false
	}
	var out []string
	found := false
	for k, vals := range h {
		if strings.EqualFold(strings.TrimSpace(k), lowerName) {
			found = true
			out = append(out, vals...)
		}
	}
	return out, found
}

func matchMultiValues(cvm compiledValueMatcher, vals []string, exists bool, ignoreCase bool) bool {
	if cvm.raw.Exists != nil {
		if *cvm.raw.Exists != exists {
			return false
		}
		// When only exists: false is specified and exists is false, it matches.
		if !*cvm.raw.Exists {
			return true
		}
	} else if !exists {
		return false
	}
	// If no further value constraints beyond exists: true
	if cvm.raw.Equals == nil && cvm.raw.NotEquals == nil && len(cvm.raw.In) == 0 &&
		cvm.raw.Contains == nil && cvm.raw.Prefix == nil && cvm.regex == nil {
		return exists
	}
	if len(vals) == 0 {
		return matchValue(cvm, "", ignoreCase)
	}
	for _, v := range vals {
		if matchValue(cvm, v, ignoreCase) {
			return true
		}
	}
	return false
}

func matchValue(cvm compiledValueMatcher, actual string, ignoreCase bool) bool {
	vm := cvm.raw
	cmpActual := actual
	if ignoreCase {
		cmpActual = strings.ToLower(actual)
	}
	if vm.Equals != nil {
		exp := *vm.Equals
		if ignoreCase {
			exp = strings.ToLower(exp)
		}
		if cmpActual != exp {
			return false
		}
	}
	if vm.NotEquals != nil {
		unexp := *vm.NotEquals
		if ignoreCase {
			unexp = strings.ToLower(unexp)
		}
		if cmpActual == unexp {
			return false
		}
	}
	if len(vm.In) > 0 {
		matched := false
		for _, candidate := range vm.In {
			c := candidate
			if ignoreCase {
				c = strings.ToLower(c)
			}
			if cmpActual == c {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	if vm.Contains != nil {
		sub := *vm.Contains
		if ignoreCase {
			sub = strings.ToLower(sub)
		}
		if !strings.Contains(cmpActual, sub) {
			return false
		}
	}
	if vm.Prefix != nil {
		pref := *vm.Prefix
		if ignoreCase {
			pref = strings.ToLower(pref)
		}
		if !strings.HasPrefix(cmpActual, pref) {
			return false
		}
	}
	if cvm.regex != nil {
		if !cvm.regex.MatchString(actual) {
			return false
		}
	}
	return true
}

func matchBody(bm *compiledBodyMatcher, rawBody []byte, getJSON func() (any, error)) (bool, string) {
	bodyText := string(rawBody)
	if bm.raw.Equals != nil && bodyText != *bm.raw.Equals {
		return false, "request body text did not equal expected body"
	}
	if bm.raw.Contains != nil && !strings.Contains(bodyText, *bm.raw.Contains) {
		return false, "request body text did not contain expected substring"
	}
	if bm.raw.Prefix != nil && !strings.HasPrefix(bodyText, *bm.raw.Prefix) {
		return false, "request body text did not match expected prefix"
	}
	if bm.regex != nil && !bm.regex.MatchString(bodyText) {
		return false, "request body text did not match regex"
	}
	if bm.hasJSONEquals || len(bm.jsonFields) > 0 {
		doc, err := getJSON()
		if err != nil {
			return false, "request body is not valid JSON"
		}
		if bm.hasJSONEquals && !jsonDeepEqual(doc, bm.jsonEqualsNorm) {
			return false, "request JSON body did not equal expected JSON structure"
		}
		for _, jf := range bm.jsonFields {
			val, exists := lookupJSONField(doc, jf.tokens)
			if jf.raw.Exists != nil {
				if *jf.raw.Exists != exists {
					return false, fmt.Sprintf("JSON field %q existence mismatch", jf.raw.Path)
				}
				if !*jf.raw.Exists {
					continue
				}
			} else if !exists {
				return false, fmt.Sprintf("JSON field %q is missing", jf.raw.Path)
			}
			if jf.raw.Type != "" {
				actualType := jsonValueType(val)
				if actualType != jf.raw.Type && !(jf.raw.Type == "number" && actualType == "integer") {
					return false, fmt.Sprintf("JSON field %q expected type %s, got %s", jf.raw.Path, jf.raw.Type, actualType)
				}
			}
			hasValCond := jf.raw.Equals != nil || jf.raw.NotEquals != nil || len(jf.raw.In) > 0 ||
				jf.raw.Contains != nil || jf.raw.Prefix != nil || jf.raw.Regex != nil
			if hasValCond {
				strVal := jsonScalarString(val)
				if !matchValue(jf.vm, strVal, false) {
					return false, fmt.Sprintf("JSON field %q value did not satisfy condition", jf.raw.Path)
				}
			}
		}
	}
	return true, ""
}

func lookupJSONField(doc any, tokens []string) (any, bool) {
	cur := doc
	for _, tok := range tokens {
		switch node := cur.(type) {
		case map[string]any:
			next, ok := node[tok]
			if !ok {
				return nil, false
			}
			cur = next
		case []any:
			idx, err := strconv.Atoi(tok)
			if err != nil || idx < 0 || idx >= len(node) {
				return nil, false
			}
			cur = node[idx]
		default:
			return nil, false
		}
	}
	return cur, true
}

func jsonValueType(v any) string {
	switch val := v.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case string:
		return "string"
	case json.Number:
		s := val.String()
		if !strings.ContainsAny(s, ".eE") {
			return "number"
		}
		return "number"
	case float64, int, int64:
		return "number"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	default:
		return "unknown"
	}
}

func jsonScalarString(v any) string {
	switch val := v.(type) {
	case nil:
		return "null"
	case string:
		return val
	case json.Number:
		return val.String()
	case bool:
		return strconv.FormatBool(val)
	default:
		b, err := json.Marshal(val)
		if err != nil {
			return fmt.Sprint(val)
		}
		return string(b)
	}
}

func jsonDeepEqual(a, b any) bool {
	na, errA := normalizeJSONValue(a)
	nb, errB := normalizeJSONValue(b)
	if errA != nil || errB != nil {
		return false
	}
	return reflect.DeepEqual(na, nb)
}

func renderMatchedOutcome(s *CompiledSnapshot, cr CompiledRule, req IncomingRequest, pathParams map[string]string) MatchOutcome {
	rng := seededRNG(s.Seed, req.Seq, cr.Rule.ID)
	delay := computeDeterministicDelay(cr.Response, rng, s.Limits.MaxDelayMs)

	contentType := strings.TrimSpace(cr.Response.ContentType)
	for _, kv := range cr.Response.Headers {
		if kv.IsEnabled() && strings.EqualFold(strings.TrimSpace(kv.Name), "Content-Type") && contentType == "" {
			contentType = strings.TrimSpace(kv.Value)
		}
	}
	if contentType == "" {
		trimmedBody := strings.TrimSpace(cr.Response.Body)
		if strings.HasPrefix(trimmedBody, "{") || strings.HasPrefix(trimmedBody, "[") {
			contentType = "application/json; charset=utf-8"
		} else {
			contentType = "text/plain; charset=utf-8"
		}
	}

	tctx := templateRenderContext{
		seq:         req.Seq,
		pathParams:  pathParams,
		query:       req.Query,
		vars:        cr.Variables,
		contentType: contentType,
		rng:         rng,
	}

	var headers [][2]string
	hasContentTypeHeader := false
	for _, kv := range cr.Response.Headers {
		if !kv.IsEnabled() {
			continue
		}
		name := sanitizeHeaderLine(kv.Name)
		if strings.EqualFold(name, "Content-Type") {
			hasContentTypeHeader = true
		}
		val := sanitizeHeaderLine(renderMockTemplate(kv.Value, tctx, true))
		headers = append(headers, [2]string{name, val})
	}
	if !hasContentTypeHeader && contentType != "" {
		headers = append([][2]string{{"Content-Type", contentType}}, headers...)
	}
	if cr.CORS != nil {
		headers = appendCORSHeaders(headers, cr.CORS, req.Headers.Get("Origin"))
	}

	renderedBody := renderMockTemplate(cr.Response.Body, tctx, false)
	if int64(len(renderedBody)) > s.Limits.MaxResponseBodyBytes {
		renderedBody = renderedBody[:s.Limits.MaxResponseBodyBytes]
	}

	status := cr.Response.Status
	if status == 0 {
		status = http.StatusOK
	}

	return MatchOutcome{
		Matched:        true,
		RuleID:         cr.Rule.ID,
		RuleName:       cr.Rule.Name,
		FileID:         cr.FileID,
		Priority:       cr.Rule.Priority,
		Scenario:       s.Scenario,
		Status:         status,
		Headers:        headers,
		Body:           []byte(renderedBody),
		Delay:          delay,
		EvaluatedRules: len(s.Rules),
		Diagnostic:     fmt.Sprintf("matched rule %q (priority %d, scenario %q)", cr.Rule.ID, cr.Rule.Priority, s.Scenario),
	}
}

type templateRenderContext struct {
	seq         uint64
	pathParams  map[string]string
	query       url.Values
	vars        map[string]string
	contentType string
	rng         *rand.Rand
}

func renderMockTemplate(tmpl string, ctx templateRenderContext, isHeader bool) string {
	if !strings.Contains(tmpl, "{{") {
		return tmpl
	}
	var out strings.Builder
	rem := tmpl
	for {
		start := strings.Index(rem, "{{")
		if start < 0 {
			out.WriteString(rem)
			break
		}
		out.WriteString(rem[:start])
		end := strings.Index(rem[start+2:], "}}")
		if end < 0 {
			out.WriteString(rem[start:])
			break
		}
		expr := strings.TrimSpace(rem[start+2 : start+2+end])
		rem = rem[start+2+end+2:]

		rawVal, isUserEcho := resolveTemplateExpr(expr, ctx)
		if isHeader {
			out.WriteString(sanitizeHeaderLine(rawVal))
		} else if isUserEcho {
			out.WriteString(encodeEchoForMediaType(rawVal, ctx.contentType))
		} else {
			out.WriteString(rawVal)
		}
	}
	return out.String()
}

func resolveTemplateExpr(expr string, ctx templateRenderContext) (val string, isUserEcho bool) {
	switch expr {
	case "req.seq":
		return strconv.FormatUint(ctx.seq, 10), false
	case "req.id":
		return fmt.Sprintf("mock-req-%04d", ctx.seq), false
	case "rand.int":
		return strconv.Itoa(ctx.rng.IntN(100000)), false
	case "rand.hex8":
		return fmt.Sprintf("%08x", ctx.rng.Uint32()), false
	case "rand.uuid":
		return deterministicUUID(ctx.rng), false
	}
	if name, ok := strings.CutPrefix(expr, "path."); ok {
		return ctx.pathParams[name], true
	}
	if name, ok := strings.CutPrefix(expr, "query."); ok {
		return ctx.query.Get(name), true
	}
	if name, ok := strings.CutPrefix(expr, "var."); ok {
		return ctx.vars[name], false
	}
	return "", false
}

func encodeEchoForMediaType(val, contentType string) string {
	ct := strings.ToLower(strings.TrimSpace(contentType))
	if idx := strings.IndexByte(ct, ';'); idx >= 0 {
		ct = strings.TrimSpace(ct[:idx])
	}
	switch {
	case ct == "application/json" || strings.HasSuffix(ct, "+json"):
		b, err := json.Marshal(val)
		if err == nil && len(b) >= 2 {
			return string(b[1 : len(b)-1])
		}
		return val
	case ct == "text/html" || ct == "application/xhtml+xml":
		return html.EscapeString(val)
	default:
		return val
	}
}

func sanitizeHeaderLine(s string) string {
	s = strings.ReplaceAll(s, "\r", "")
	s = strings.ReplaceAll(s, "\n", "")
	return s
}

func appendCORSHeaders(headers [][2]string, cors *CORSConfig, reqOrigin string) [][2]string {
	if cors == nil || len(cors.AllowOrigins) == 0 {
		return headers
	}
	allowedOrigin := ""
	for _, o := range cors.AllowOrigins {
		o = strings.TrimSpace(o)
		if o == "*" {
			allowedOrigin = "*"
			break
		}
		if reqOrigin != "" && strings.EqualFold(o, reqOrigin) {
			allowedOrigin = reqOrigin
			break
		}
	}
	if allowedOrigin == "" && len(cors.AllowOrigins) == 1 && cors.AllowOrigins[0] != "*" {
		allowedOrigin = cors.AllowOrigins[0]
	}
	if allowedOrigin != "" {
		headers = append(headers, [2]string{"Access-Control-Allow-Origin", sanitizeHeaderLine(allowedOrigin)})
	}
	if len(cors.AllowMethods) > 0 {
		headers = append(headers, [2]string{"Access-Control-Allow-Methods", sanitizeHeaderLine(strings.Join(cors.AllowMethods, ", "))})
	}
	if len(cors.AllowHeaders) > 0 {
		headers = append(headers, [2]string{"Access-Control-Allow-Headers", sanitizeHeaderLine(strings.Join(cors.AllowHeaders, ", "))})
	}
	if len(cors.ExposeHeaders) > 0 {
		headers = append(headers, [2]string{"Access-Control-Expose-Headers", sanitizeHeaderLine(strings.Join(cors.ExposeHeaders, ", "))})
	}
	if cors.MaxAgeSeconds > 0 {
		headers = append(headers, [2]string{"Access-Control-Max-Age", strconv.Itoa(cors.MaxAgeSeconds)})
	}
	if cors.AllowCredentials && allowedOrigin != "*" && allowedOrigin != "" {
		headers = append(headers, [2]string{"Access-Control-Allow-Credentials", "true"})
	}
	return headers
}

func seededRNG(seed int64, seq uint64, salt string) *rand.Rand {
	var h uint64 = 14695981039346656037
	for i := 0; i < len(salt); i++ {
		h ^= uint64(salt[i])
		h *= 1099511628211
	}
	s1 := uint64(seed) ^ (seq * 0x9e3779b97f4a7c15) ^ h
	s2 := (uint64(seed) * 0xbf58476d1ce4e5b9) ^ (h + seq + 1)
	return rand.New(rand.NewPCG(s1, s2))
}

func deterministicUUID(rng *rand.Rand) string {
	var b [16]byte
	u1 := rng.Uint64()
	u2 := rng.Uint64()
	for i := 0; i < 8; i++ {
		b[i] = byte(u1 >> (i * 8))
		b[i+8] = byte(u2 >> (i * 8))
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant RFC 4122
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func computeDeterministicDelay(resp RuleResponse, rng *rand.Rand, maxDelayMs int) time.Duration {
	ms := resp.DelayMs
	if resp.DelayRangeMs != nil {
		minMs := resp.DelayRangeMs.Min
		maxMs := resp.DelayRangeMs.Max
		if maxMs > minMs {
			ms = minMs + rng.IntN(maxMs-minMs+1)
		} else {
			ms = minMs
		}
	}
	if ms < 0 {
		ms = 0
	}
	if maxDelayMs > 0 && ms > maxDelayMs {
		ms = maxDelayMs
	}
	return time.Duration(ms) * time.Millisecond
}

func unmatchedOutcome(req IncomingRequest, scenario string, evaluated int, nearMisses []NearMiss, red *core.Redactor) MatchOutcome {
	path := normalizeURLPath(req.Path)
	if red != nil {
		path = red.RedactString(path)
	}
	payload := struct {
		Error          string     `json:"error"`
		Message        string     `json:"message"`
		Method         string     `json:"method"`
		Path           string     `json:"path"`
		Scenario       string     `json:"scenario"`
		EvaluatedRules int        `json:"evaluatedRules"`
		NearMisses     []NearMiss `json:"nearMisses,omitempty"`
	}{
		Error:          "mock_route_not_found",
		Message:        fmt.Sprintf("no mock rule matched %s %s in scenario %q (evaluated %d rules; public network forwarding is disabled)", strings.ToUpper(req.Method), path, scenario, evaluated),
		Method:         strings.ToUpper(req.Method),
		Path:           path,
		Scenario:       scenario,
		EvaluatedRules: evaluated,
		NearMisses:     nearMisses,
	}
	b, _ := json.MarshalIndent(payload, "", "  ")
	diag := fmt.Sprintf("unmatched %s %s in scenario %q (%d rules evaluated)", strings.ToUpper(req.Method), path, scenario, evaluated)
	if len(nearMisses) > 0 {
		diag += fmt.Sprintf("; closest rule %q failed at %s (%s)", nearMisses[0].RuleID, nearMisses[0].Stage, nearMisses[0].Reason)
	}
	return MatchOutcome{
		Matched:        false,
		Scenario:       scenario,
		Status:         http.StatusNotFound,
		Headers:        [][2]string{{"Content-Type", "application/json; charset=utf-8"}},
		Body:           b,
		EvaluatedRules: evaluated,
		NearMisses:     nearMisses,
		Diagnostic:     diag,
	}
}

func bytesTrimSpace(b []byte) []byte {
	return []byte(strings.TrimSpace(string(b)))
}
