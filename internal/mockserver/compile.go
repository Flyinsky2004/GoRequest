package mockserver

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"

	"gorequest/internal/core"
)

// RuleConflict describes an overlap/ambiguity between two enabled rules that
// share the same priority in the same scenario.
type RuleConflict struct {
	RuleA    string `json:"ruleA"`
	RuleB    string `json:"ruleB"`
	Priority int    `json:"priority"`
	Scenario string `json:"scenario"`
	Method   string `json:"method"`
	PathA    string `json:"pathA"`
	PathB    string `json:"pathB"`
	Reason   string `json:"reason"`
}

func (c RuleConflict) String() string {
	return fmt.Sprintf("ambiguous rules %q and %q at priority %d (scenario %q, %s %s vs %s): %s",
		c.RuleA, c.RuleB, c.Priority, c.Scenario, c.Method, c.PathA, c.PathB, c.Reason)
}

// AmbiguityError is returned when one or more pairs of rules share the same
// priority and have overlapping match conditions in the active scenario.
type AmbiguityError struct {
	Conflicts []RuleConflict
}

func (e *AmbiguityError) Error() string {
	if e == nil || len(e.Conflicts) == 0 {
		return "ambiguous mock rules"
	}
	parts := make([]string, len(e.Conflicts))
	for i, c := range e.Conflicts {
		parts[i] = c.String()
	}
	return strings.Join(parts, "; ")
}

type pathSegment struct {
	raw     string
	literal string
	param   string
	isParam bool
}

type compiledPath struct {
	raw          string
	segments     []pathSegment
	literalCount int
}

type compiledValueMatcher struct {
	raw   ValueMatcher
	regex *regexp.Regexp
}

type compiledKVMatcher struct {
	name       string
	ignoreCase bool
	vm         compiledValueMatcher
}

type compiledJSONField struct {
	raw    JSONFieldMatcher
	tokens []string
	vm     compiledValueMatcher
}

type compiledBodyMatcher struct {
	raw            BodyMatcher
	regex          *regexp.Regexp
	hasJSONEquals  bool
	jsonEqualsNorm any
	jsonFields     []compiledJSONField
}

// CompiledRule is a validated, precompiled rule ready for deterministic matching.
type CompiledRule struct {
	FileID      string
	FileRelPath string
	Rule        MockRule
	Method      string
	Path        compiledPath
	PathParams  map[string]compiledValueMatcher
	Query       []compiledKVMatcher
	Headers     []compiledKVMatcher
	Body        *compiledBodyMatcher
	Response    RuleResponse
	Variables   map[string]string
	CORS        *CORSConfig
}

// CompiledSnapshot is an immutable, self-contained snapshot of compiled mock
// rules for a specific scenario and seed. Once bound to a running server or
// Suite run, switching scenarios in the UI creates a new CompiledSnapshot
// without mutating existing snapshots.
type CompiledSnapshot struct {
	SnapshotDigest     string
	Scenario           string
	Seed               int64
	AvailableScenarios []string
	AllFiles           []*MockFile
	Rules              []CompiledRule
	Conflicts          []RuleConflict
	Limits             ServerLimits
}

// HasConflicts reports whether the snapshot contains any same-priority rule ambiguity.
func (s *CompiledSnapshot) HasConflicts() bool {
	return s != nil && len(s.Conflicts) > 0
}

// WithScenario compiles a new immutable snapshot for newScenario from the same
// underlying mock files and seed, leaving s completely unchanged.
func (s *CompiledSnapshot) WithScenario(newScenario string) (*CompiledSnapshot, error) {
	if s == nil {
		return nil, &core.ConfigError{Err: fmt.Errorf("snapshot is nil")}
	}
	return CompileMockFiles(s.AllFiles, newScenario, s.Seed, s.Limits)
}

// ValidateMockFileStructure validates a MockFile's schemaVersion, IDs, CORS,
// template placeholders, matchers, and response definitions.
func ValidateMockFileStructure(mf *MockFile, limits ServerLimits) error {
	lim := limits.Effective()
	if mf == nil {
		return &core.ConfigError{Err: fmt.Errorf("mock file is nil")}
	}
	if mf.SchemaVersion != MockSchemaVersion {
		if mf.SchemaVersion > MockSchemaVersion {
			return &core.ConfigError{
				Err: fmt.Errorf("mock schemaVersion %d unsupported (this build supports %d); refusing execution", mf.SchemaVersion, MockSchemaVersion),
			}
		}
		return &core.ConfigError{
			Err: fmt.Errorf("mock schemaVersion %d must be %d", mf.SchemaVersion, MockSchemaVersion),
		}
	}
	if !isValidIdentifier(mf.ID) {
		return &core.ConfigError{
			Err: fmt.Errorf("mock file id %q must match [A-Za-z0-9][A-Za-z0-9._-]*", mf.ID),
		}
	}
	if strings.TrimSpace(mf.Name) == "" {
		return &core.ConfigError{Err: fmt.Errorf("mock file %q: name is required", mf.ID)}
	}
	if mf.CORS != nil {
		if err := validateCORSConfig(mf.CORS); err != nil {
			return &core.ConfigError{Err: fmt.Errorf("mock file %q cors: %w", mf.ID, err)}
		}
	}
	for k, v := range mf.Variables {
		if !isValidIdentifier(k) || strings.HasPrefix(strings.ToLower(k), "secret.") {
			return &core.ConfigError{Err: fmt.Errorf("mock file %q: invalid variable name %q", mf.ID, k)}
		}
		if err := validateMockTemplateString(v); err != nil {
			return &core.ConfigError{Err: fmt.Errorf("mock file %q variable %q: %w", mf.ID, k, err)}
		}
	}
	if len(mf.Rules) == 0 {
		return &core.ConfigError{Err: fmt.Errorf("mock file %q must define at least one rule", mf.ID)}
	}
	if len(mf.Rules) > DefaultMaxRulesPerProject {
		return &core.ConfigError{Err: fmt.Errorf("mock file %q exceeds max rules (%d)", mf.ID, DefaultMaxRulesPerProject)}
	}
	seenIDs := map[string]bool{}
	for i := range mf.Rules {
		r := &mf.Rules[i]
		if !isValidIdentifier(r.ID) {
			return &core.ConfigError{
				Err: fmt.Errorf("mock file %q rule #%d: id %q must match [A-Za-z0-9][A-Za-z0-9._-]*", mf.ID, i+1, r.ID),
			}
		}
		if seenIDs[r.ID] {
			return &core.ConfigError{
				Err: fmt.Errorf("mock file %q: duplicate rule id %q", mf.ID, r.ID),
			}
		}
		seenIDs[r.ID] = true
		if err := validateMockRule(r, lim); err != nil {
			return &core.ConfigError{Err: fmt.Errorf("rule %q: %w", r.ID, err)}
		}
	}
	return nil
}

func validateCORSConfig(c *CORSConfig) error {
	if c == nil {
		return nil
	}
	if c.MaxAgeSeconds < 0 || c.MaxAgeSeconds > 86400*7 {
		return fmt.Errorf("maxAgeSeconds %d out of range [0, 604800]", c.MaxAgeSeconds)
	}
	for _, origin := range c.AllowOrigins {
		trimmed := strings.TrimSpace(origin)
		if trimmed == "" {
			return fmt.Errorf("allowOrigins cannot contain empty string")
		}
		if trimmed == "*" && c.AllowCredentials {
			return fmt.Errorf("CORS allowCredentials: true is forbidden when allowOrigins contains wildcard '*'")
		}
	}
	return nil
}

func validateMockRule(r *MockRule, lim ServerLimits) error {
	method := strings.ToUpper(strings.TrimSpace(r.Match.Method))
	if method == "" {
		return fmt.Errorf("match.method is required")
	}
	if err := core.ValidateHTTPMethod(method); err != nil {
		return fmt.Errorf("invalid match.method %q: %w", r.Match.Method, err)
	}
	cp, err := compilePathTemplate(r.Match.Path)
	if err != nil {
		return fmt.Errorf("match.path: %w", err)
	}
	paramNames := map[string]bool{}
	for _, seg := range cp.segments {
		if seg.isParam {
			paramNames[seg.param] = true
		}
	}
	for pname, vm := range r.Match.PathParams {
		if !paramNames[pname] {
			return fmt.Errorf("match.pathParams references %q which is not in path template %q", pname, r.Match.Path)
		}
		if _, err := compileValueMatcher(vm); err != nil {
			return fmt.Errorf("match.pathParams[%q]: %w", pname, err)
		}
	}
	for _, qm := range r.Match.Query {
		if strings.TrimSpace(qm.Name) == "" {
			return fmt.Errorf("match.query name cannot be empty")
		}
		if _, err := compileValueMatcher(qm.ToValueMatcher()); err != nil {
			return fmt.Errorf("match.query[%q]: %w", qm.Name, err)
		}
	}
	for _, hm := range r.Match.Headers {
		name := strings.TrimSpace(hm.Name)
		if name == "" {
			return fmt.Errorf("match.headers name cannot be empty")
		}
		if isForbiddenCredentialHeader(name) && hm.Equals != nil && looksLikeRawCredential(*hm.Equals) {
			return fmt.Errorf("match.headers[%q] must not embed raw credential secrets in shared mock rules", name)
		}
		if _, err := compileValueMatcher(hm.ToValueMatcher()); err != nil {
			return fmt.Errorf("match.headers[%q]: %w", hm.Name, err)
		}
	}
	if r.Match.Body != nil {
		if _, err := compileBodyMatcher(*r.Match.Body); err != nil {
			return fmt.Errorf("match.body: %w", err)
		}
	}
	for _, sc := range r.Scenarios {
		if strings.TrimSpace(sc) == "" {
			return fmt.Errorf("scenarios cannot contain an empty name")
		}
	}
	if err := validateRuleResponse(&r.Response, lim); err != nil {
		return fmt.Errorf("response: %w", err)
	}
	for scName, varResp := range r.Variants {
		if strings.TrimSpace(scName) == "" {
			return fmt.Errorf("variants key cannot be empty")
		}
		vr := varResp
		if err := validateRuleResponse(&vr, lim); err != nil {
			return fmt.Errorf("variants[%q]: %w", scName, err)
		}
	}
	return nil
}

func validateRuleResponse(resp *RuleResponse, lim ServerLimits) error {
	if resp.Status == 0 {
		resp.Status = http.StatusOK
	}
	if resp.Status < 100 || resp.Status > 599 {
		return fmt.Errorf("status %d must be between 100 and 599", resp.Status)
	}
	if resp.DelayMs < 0 {
		return fmt.Errorf("delayMs %d must not be negative", resp.DelayMs)
	}
	if resp.DelayMs > lim.MaxDelayMs {
		return fmt.Errorf("delayMs %d exceeds maximum allowed delay (%d ms)", resp.DelayMs, lim.MaxDelayMs)
	}
	if resp.DelayRangeMs != nil {
		if resp.DelayMs > 0 {
			return fmt.Errorf("cannot specify both delayMs and delayRangeMs")
		}
		if resp.DelayRangeMs.Min < 0 || resp.DelayRangeMs.Max < resp.DelayRangeMs.Min {
			return fmt.Errorf("delayRangeMs [%d, %d] is invalid", resp.DelayRangeMs.Min, resp.DelayRangeMs.Max)
		}
		if resp.DelayRangeMs.Max > lim.MaxDelayMs {
			return fmt.Errorf("delayRangeMs.max %d exceeds maximum allowed delay (%d ms)", resp.DelayRangeMs.Max, lim.MaxDelayMs)
		}
	}
	if int64(len(resp.Body)) > lim.MaxResponseBodyBytes {
		return fmt.Errorf("response body exceeds maximum allowed size (%d > %d bytes)", len(resp.Body), lim.MaxResponseBodyBytes)
	}
	if err := validateMockTemplateString(resp.Body); err != nil {
		return fmt.Errorf("body template: %w", err)
	}
	for _, kv := range resp.Headers {
		if !kv.IsEnabled() {
			continue
		}
		hName := strings.TrimSpace(kv.Name)
		if hName == "" {
			return fmt.Errorf("response header name cannot be empty")
		}
		if strings.ContainsAny(hName, "\r\n") || strings.ContainsAny(kv.Value, "\r\n") {
			return fmt.Errorf("response header %q contains newline", hName)
		}
		if isForbiddenCredentialHeader(hName) && looksLikeRawCredential(kv.Value) {
			return fmt.Errorf("response header %q must not embed raw credentials in shared mock rules", hName)
		}
		if err := validateMockTemplateString(kv.Value); err != nil {
			return fmt.Errorf("header %q template: %w", hName, err)
		}
	}
	return nil
}

// validateMockTemplateString ensures templates only reference safe, supported
// declarative namespaces ({{path.<name>}}, {{query.<name>}}, {{var.<name>}},
// {{req.id}}, {{req.seq}}, {{rand.int}}, {{rand.hex8}}, {{rand.uuid}}) and
// explicitly forbids {{secret.*}} or arbitrary expressions.
func validateMockTemplateString(s string) error {
	if !strings.Contains(s, "{{") {
		return nil
	}
	rem := s
	for {
		start := strings.Index(rem, "{{")
		if start < 0 {
			if strings.Contains(rem, "}}") {
				return fmt.Errorf("unmatched closing '}}' in template")
			}
			break
		}
		if strings.Contains(rem[:start], "}}") {
			return fmt.Errorf("unmatched closing '}}' in template")
		}
		end := strings.Index(rem[start+2:], "}}")
		if end < 0 {
			return fmt.Errorf("unclosed '{{' in template")
		}
		expr := strings.TrimSpace(rem[start+2 : start+2+end])
		rem = rem[start+2+end+2:]
		if expr == "" {
			return fmt.Errorf("empty '{{}}' expression is not allowed")
		}
		if strings.HasPrefix(strings.ToLower(expr), "secret.") {
			return fmt.Errorf("mock templates must not reference {{%s}} secrets", expr)
		}
		if !isSupportedMockTemplateExpr(expr) {
			return fmt.Errorf("unsupported mock template expression {{%s}} (allowed: path.<name>, query.<name>, var.<name>, req.id, req.seq, rand.int, rand.hex8, rand.uuid)", expr)
		}
	}
	return nil
}

func isSupportedMockTemplateExpr(expr string) bool {
	switch expr {
	case "req.id", "req.seq", "rand.int", "rand.hex8", "rand.uuid":
		return true
	}
	for _, prefix := range []string{"path.", "query.", "var."} {
		if rest, ok := strings.CutPrefix(expr, prefix); ok {
			return isValidIdentifier(rest)
		}
	}
	return false
}

func compilePathTemplate(rawPath string) (compiledPath, error) {
	p := strings.TrimSpace(rawPath)
	if p == "" {
		return compiledPath{}, fmt.Errorf("path cannot be empty")
	}
	if !strings.HasPrefix(p, "/") {
		return compiledPath{}, fmt.Errorf("path %q must start with '/'", rawPath)
	}
	if strings.ContainsAny(p, "?#\r\n") {
		return compiledPath{}, fmt.Errorf("path %q must not contain query string, fragment, or control characters", rawPath)
	}
	norm := normalizeURLPath(p)
	if norm == "/" {
		return compiledPath{raw: "/"}, nil
	}
	parts := strings.Split(strings.TrimPrefix(norm, "/"), "/")
	segs := make([]pathSegment, 0, len(parts))
	seenParams := map[string]bool{}
	litCount := 0
	for _, part := range parts {
		if strings.HasPrefix(part, "{") && strings.HasSuffix(part, "}") {
			name := strings.TrimSpace(part[1 : len(part)-1])
			if !isValidIdentifier(name) {
				return compiledPath{}, fmt.Errorf("invalid path parameter %q in %q", part, rawPath)
			}
			if seenParams[name] {
				return compiledPath{}, fmt.Errorf("duplicate path parameter %q in %q", name, rawPath)
			}
			seenParams[name] = true
			segs = append(segs, pathSegment{raw: part, param: name, isParam: true})
		} else if strings.HasPrefix(part, ":") && len(part) > 1 {
			name := strings.TrimSpace(part[1:])
			if !isValidIdentifier(name) {
				return compiledPath{}, fmt.Errorf("invalid path parameter %q in %q", part, rawPath)
			}
			if seenParams[name] {
				return compiledPath{}, fmt.Errorf("duplicate path parameter %q in %q", name, rawPath)
			}
			seenParams[name] = true
			segs = append(segs, pathSegment{raw: part, param: name, isParam: true})
		} else if strings.ContainsAny(part, "{}:") {
			return compiledPath{}, fmt.Errorf("malformed path template segment %q in %q", part, rawPath)
		} else {
			litCount++
			segs = append(segs, pathSegment{raw: part, literal: part, isParam: false})
		}
	}
	return compiledPath{
		raw:          norm,
		segments:     segs,
		literalCount: litCount,
	}, nil
}

func normalizeURLPath(p string) string {
	if p == "" || p == "/" {
		return "/"
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	for strings.Contains(p, "//") {
		p = strings.ReplaceAll(p, "//", "/")
	}
	if len(p) > 1 && strings.HasSuffix(p, "/") {
		p = strings.TrimSuffix(p, "/")
	}
	return p
}

func compileValueMatcher(vm ValueMatcher) (compiledValueMatcher, error) {
	hasAny := vm.Exists != nil || vm.Equals != nil || vm.NotEquals != nil ||
		len(vm.In) > 0 || vm.Contains != nil || vm.Prefix != nil || vm.Regex != nil
	if !hasAny {
		return compiledValueMatcher{}, fmt.Errorf("value matcher must specify at least one condition (exists, equals, notEquals, in, contains, prefix, or regex)")
	}
	if vm.Exists != nil && !*vm.Exists {
		if vm.Equals != nil || len(vm.In) > 0 || vm.Contains != nil || vm.Prefix != nil || vm.Regex != nil {
			return compiledValueMatcher{}, fmt.Errorf("exists: false cannot be combined with positive value conditions")
		}
	}
	var re *regexp.Regexp
	if vm.Regex != nil {
		if len(*vm.Regex) > 512 {
			return compiledValueMatcher{}, fmt.Errorf("regex pattern exceeds 512 characters")
		}
		compiled, err := regexp.Compile(*vm.Regex)
		if err != nil {
			return compiledValueMatcher{}, fmt.Errorf("invalid regex %q: %w", *vm.Regex, err)
		}
		re = compiled
	}
	return compiledValueMatcher{raw: vm, regex: re}, nil
}

func compileBodyMatcher(bm BodyMatcher) (*compiledBodyMatcher, error) {
	hasAny := bm.Equals != nil || bm.Contains != nil || bm.Prefix != nil ||
		bm.Regex != nil || bm.JSONEquals != nil || len(bm.JSONFields) > 0
	if !hasAny {
		return nil, fmt.Errorf("body matcher must specify at least one condition")
	}
	out := &compiledBodyMatcher{raw: bm}
	if bm.Regex != nil {
		if len(*bm.Regex) > 512 {
			return nil, fmt.Errorf("body regex exceeds 512 characters")
		}
		re, err := regexp.Compile(*bm.Regex)
		if err != nil {
			return nil, fmt.Errorf("invalid body regex %q: %w", *bm.Regex, err)
		}
		out.regex = re
	}
	if bm.JSONEquals != nil {
		norm, err := normalizeJSONValue(bm.JSONEquals)
		if err != nil {
			return nil, fmt.Errorf("invalid jsonEquals value: %w", err)
		}
		out.hasJSONEquals = true
		out.jsonEqualsNorm = norm
	}
	for i, jf := range bm.JSONFields {
		tokens, err := parseJSONFieldPath(jf.Path)
		if err != nil {
			return nil, fmt.Errorf("jsonFields[%d] path %q: %w", i, jf.Path, err)
		}
		if jf.Type != "" {
			switch jf.Type {
			case "string", "number", "boolean", "object", "array", "null":
			default:
				return nil, fmt.Errorf("jsonFields[%d] unsupported type %q", i, jf.Type)
			}
		}
		var cvm compiledValueMatcher
		vm := jf.ToValueMatcher()
		if vm.Exists != nil || vm.Equals != nil || vm.NotEquals != nil || len(vm.In) > 0 || vm.Contains != nil || vm.Prefix != nil || vm.Regex != nil {
			cvm, err = compileValueMatcher(vm)
			if err != nil {
				return nil, fmt.Errorf("jsonFields[%d] (%s): %w", i, jf.Path, err)
			}
		} else if jf.Type == "" {
			return nil, fmt.Errorf("jsonFields[%d] (%s) must specify a value condition or type", i, jf.Path)
		}
		out.jsonFields = append(out.jsonFields, compiledJSONField{
			raw:    jf,
			tokens: tokens,
			vm:     cvm,
		})
	}
	return out, nil
}

func normalizeJSONValue(v any) (any, error) {
	if s, ok := v.(string); ok {
		trimmed := strings.TrimSpace(s)
		if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
			var parsed any
			dec := json.NewDecoder(strings.NewReader(trimmed))
			dec.UseNumber()
			if err := dec.Decode(&parsed); err == nil {
				return parsed, nil
			}
		}
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var out any
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.UseNumber()
	if err := dec.Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}

func parseJSONFieldPath(raw string) ([]string, error) {
	p := strings.TrimSpace(raw)
	if p == "" {
		return nil, fmt.Errorf("empty field path")
	}
	if strings.HasPrefix(p, "/") {
		parts := strings.Split(p[1:], "/")
		out := make([]string, 0, len(parts))
		for _, tok := range parts {
			tok = strings.ReplaceAll(tok, "~1", "/")
			tok = strings.ReplaceAll(tok, "~0", "~")
			out = append(out, tok)
		}
		return out, nil
	}
	p = strings.TrimPrefix(p, "$.")
	p = strings.TrimPrefix(p, "$")
	if p == "" {
		return nil, nil
	}
	parts := strings.Split(p, ".")
	var out []string
	for _, part := range parts {
		if part == "" {
			return nil, fmt.Errorf("invalid empty segment in %q", raw)
		}
		out = append(out, part)
	}
	return out, nil
}

// CompileProjectMocks loads all `.gorequest/mocks/*.mock.yaml` files from project
// and compiles an immutable CompiledSnapshot for scenario and seed.
func CompileProjectMocks(project *core.Project, scenario string, seed int64, limits ServerLimits) (*CompiledSnapshot, error) {
	files, err := LoadProjectMocks(project)
	if err != nil {
		return nil, err
	}
	return CompileMockFiles(files, scenario, seed, limits)
}

// CompileMockFiles compiles an in-memory slice of MockFiles into a deterministic
// CompiledSnapshot. Ordering is strictly by canonical file ID / rule ID and
// explicit rule Priority (descending); slice order does not affect evaluation.
func CompileMockFiles(files []*MockFile, scenario string, seed int64, limits ServerLimits) (*CompiledSnapshot, error) {
	lim := limits.Effective()
	scenario = strings.TrimSpace(scenario)
	if scenario == "" {
		for _, f := range files {
			if f != nil && strings.TrimSpace(f.DefaultScenario) != "" {
				scenario = strings.TrimSpace(f.DefaultScenario)
				break
			}
		}
		if scenario == "" {
			scenario = DefaultScenario
		}
	}
	if seed == 0 {
		seed = 1
	}

	// Copy and sort files deterministically by (SourceRelPath, ID).
	sortedFiles := make([]*MockFile, 0, len(files))
	seenFileIDs := map[string]bool{}
	seenRuleIDs := map[string]bool{}
	scenarioSet := map[string]bool{}
	for _, b := range BuiltinScenarios {
		scenarioSet[b] = true
	}

	for _, f := range files {
		if f == nil {
			continue
		}
		if err := ValidateMockFileStructure(f, lim); err != nil {
			return nil, err
		}
		if seenFileIDs[f.ID] {
			return nil, &core.ConfigError{Err: fmt.Errorf("duplicate mock file id %q", f.ID)}
		}
		seenFileIDs[f.ID] = true
		for _, r := range f.Rules {
			if seenRuleIDs[r.ID] {
				return nil, &core.ConfigError{Err: fmt.Errorf("duplicate mock rule id %q", r.ID)}
			}
			seenRuleIDs[r.ID] = true
			for _, sc := range r.Scenarios {
				scenarioSet[strings.TrimSpace(sc)] = true
			}
			for sc := range r.Variants {
				scenarioSet[strings.TrimSpace(sc)] = true
			}
		}
		if strings.TrimSpace(f.DefaultScenario) != "" {
			scenarioSet[strings.TrimSpace(f.DefaultScenario)] = true
		}
		sortedFiles = append(sortedFiles, f)
	}
	scenarioSet[scenario] = true

	sort.Slice(sortedFiles, func(i, j int) bool {
		if sortedFiles[i].SourceRelPath != sortedFiles[j].SourceRelPath {
			return sortedFiles[i].SourceRelPath < sortedFiles[j].SourceRelPath
		}
		return sortedFiles[i].ID < sortedFiles[j].ID
	})

	availableScenarios := make([]string, 0, len(scenarioSet))
	for sc := range scenarioSet {
		if sc != "" {
			availableScenarios = append(availableScenarios, sc)
		}
	}
	sort.Strings(availableScenarios)

	var compiledRules []CompiledRule
	for _, mf := range sortedFiles {
		for _, rule := range mf.Rules {
			if !rule.IsEnabled() {
				continue
			}
			if !ruleAppliesToScenario(rule, scenario) {
				continue
			}
			cr, err := buildCompiledRule(mf, rule, scenario, lim)
			if err != nil {
				return nil, err
			}
			compiledRules = append(compiledRules, cr)
		}
	}

	// Deterministic ordering: higher Priority first; tie-break by stable Rule.ID ascending.
	sort.Slice(compiledRules, func(i, j int) bool {
		if compiledRules[i].Rule.Priority != compiledRules[j].Rule.Priority {
			return compiledRules[i].Rule.Priority > compiledRules[j].Rule.Priority
		}
		return compiledRules[i].Rule.ID < compiledRules[j].Rule.ID
	})

	conflicts := detectRuleAmbiguities(compiledRules, scenario)
	digest := computeSnapshotDigest(sortedFiles, scenario, seed, compiledRules)

	snap := &CompiledSnapshot{
		SnapshotDigest:     digest,
		Scenario:           scenario,
		Seed:               seed,
		AvailableScenarios: availableScenarios,
		AllFiles:           sortedFiles,
		Rules:              compiledRules,
		Conflicts:          conflicts,
		Limits:             lim,
	}
	if len(conflicts) > 0 {
		return snap, &AmbiguityError{Conflicts: conflicts}
	}
	return snap, nil
}

func ruleAppliesToScenario(r MockRule, scenario string) bool {
	if len(r.Scenarios) == 0 {
		return true
	}
	for _, sc := range r.Scenarios {
		if strings.EqualFold(strings.TrimSpace(sc), scenario) || strings.TrimSpace(sc) == "*" {
			return true
		}
	}
	// If the rule defines a specific variant for this scenario, it also applies.
	if _, ok := r.Variants[scenario]; ok {
		return true
	}
	return false
}

func selectResponseForScenario(r MockRule, scenario string) RuleResponse {
	if len(r.Variants) > 0 {
		if v, ok := r.Variants[scenario]; ok {
			return v
		}
		for k, v := range r.Variants {
			if strings.EqualFold(strings.TrimSpace(k), scenario) {
				return v
			}
		}
	}
	return r.Response
}

func buildCompiledRule(mf *MockFile, r MockRule, scenario string, lim ServerLimits) (CompiledRule, error) {
	cp, err := compilePathTemplate(r.Match.Path)
	if err != nil {
		return CompiledRule{}, err
	}
	pp := make(map[string]compiledValueMatcher, len(r.Match.PathParams))
	for k, vm := range r.Match.PathParams {
		cvm, err := compileValueMatcher(vm)
		if err != nil {
			return CompiledRule{}, err
		}
		pp[k] = cvm
	}
	qMatchers := make([]compiledKVMatcher, 0, len(r.Match.Query))
	for _, q := range r.Match.Query {
		cvm, err := compileValueMatcher(q.ToValueMatcher())
		if err != nil {
			return CompiledRule{}, err
		}
		qMatchers = append(qMatchers, compiledKVMatcher{
			name:       q.Name,
			ignoreCase: q.IgnoreCase,
			vm:         cvm,
		})
	}
	hMatchers := make([]compiledKVMatcher, 0, len(r.Match.Headers))
	for _, h := range r.Match.Headers {
		cvm, err := compileValueMatcher(h.ToValueMatcher())
		if err != nil {
			return CompiledRule{}, err
		}
		hMatchers = append(hMatchers, compiledKVMatcher{
			name:       strings.ToLower(strings.TrimSpace(h.Name)),
			ignoreCase: h.IgnoreCase,
			vm:         cvm,
		})
	}
	var bm *compiledBodyMatcher
	if r.Match.Body != nil {
		bm, err = compileBodyMatcher(*r.Match.Body)
		if err != nil {
			return CompiledRule{}, err
		}
	}
	resp := selectResponseForScenario(r, scenario)
	if err := validateRuleResponse(&resp, lim); err != nil {
		return CompiledRule{}, err
	}
	varsCopy := make(map[string]string, len(mf.Variables))
	for k, v := range mf.Variables {
		varsCopy[k] = v
	}
	return CompiledRule{
		FileID:      mf.ID,
		FileRelPath: mf.SourceRelPath,
		Rule:        r,
		Method:      strings.ToUpper(strings.TrimSpace(r.Match.Method)),
		Path:        cp,
		PathParams:  pp,
		Query:       qMatchers,
		Headers:     hMatchers,
		Body:        bm,
		Response:    resp,
		Variables:   varsCopy,
		CORS:        mf.CORS,
	}, nil
}

// detectRuleAmbiguities checks all pairs of compiled rules sharing the same
// priority. Because rules are already sorted by (Priority DESC, ID ASC),
// conflict ordering is 100% deterministic.
func detectRuleAmbiguities(rules []CompiledRule, scenario string) []RuleConflict {
	var conflicts []RuleConflict
	for i := 0; i < len(rules); i++ {
		for j := i + 1; j < len(rules); j++ {
			a := rules[i]
			b := rules[j]
			if a.Rule.Priority != b.Rule.Priority {
				break
			}
			if a.Method != b.Method {
				continue
			}
			overlap, pathReason := pathsCanOverlap(a, b)
			if !overlap {
				continue
			}
			if conditionsMutuallyExclusive(a, b) {
				continue
			}
			conflicts = append(conflicts, RuleConflict{
				RuleA:    a.Rule.ID,
				RuleB:    b.Rule.ID,
				Priority: a.Rule.Priority,
				Scenario: scenario,
				Method:   a.Method,
				PathA:    a.Path.raw,
				PathB:    b.Path.raw,
				Reason:   pathReason + " with overlapping query/header/body conditions at identical priority",
			})
		}
	}
	return conflicts
}

func pathsCanOverlap(a, b CompiledRule) (bool, string) {
	if len(a.Path.segments) != len(b.Path.segments) {
		return false, ""
	}
	if len(a.Path.segments) == 0 {
		return true, "both match root path '/'"
	}
	hasTemplate := false
	for idx := range a.Path.segments {
		sa := a.Path.segments[idx]
		sb := b.Path.segments[idx]
		switch {
		case !sa.isParam && !sb.isParam:
			if sa.literal != sb.literal {
				return false, ""
			}
		case !sa.isParam && sb.isParam:
			hasTemplate = true
			if cvm, ok := b.PathParams[sb.param]; ok {
				if !matchValue(cvm, sa.literal, false) {
					return false, ""
				}
			}
		case sa.isParam && !sb.isParam:
			hasTemplate = true
			if cvm, ok := a.PathParams[sa.param]; ok {
				if !matchValue(cvm, sb.literal, false) {
					return false, ""
				}
			}
		default:
			hasTemplate = true
			cvmA, okA := a.PathParams[sa.param]
			cvmB, okB := b.PathParams[sb.param]
			if okA && okB && valueMatchersDisjoint(cvmA.raw, cvmB.raw) {
				return false, ""
			}
		}
	}
	if a.Path.raw == b.Path.raw {
		return true, fmt.Sprintf("identical path %q", a.Path.raw)
	}
	if hasTemplate {
		return true, fmt.Sprintf("overlapping path templates %q and %q", a.Path.raw, b.Path.raw)
	}
	return true, fmt.Sprintf("overlapping paths %q and %q", a.Path.raw, b.Path.raw)
}

func conditionsMutuallyExclusive(a, b CompiledRule) bool {
	// Check Query matchers on the same key
	for _, qa := range a.Query {
		for _, qb := range b.Query {
			if qa.name == qb.name && !qa.ignoreCase && !qb.ignoreCase {
				if valueMatchersDisjoint(qa.vm.raw, qb.vm.raw) {
					return true
				}
			}
		}
	}
	// Check Header matchers on the same canonical lower-case header name
	for _, ha := range a.Headers {
		for _, hb := range b.Headers {
			if ha.name == hb.name && !ha.ignoreCase && !hb.ignoreCase {
				if valueMatchersDisjoint(ha.vm.raw, hb.vm.raw) {
					return true
				}
			}
		}
	}
	// Check Body matchers
	if a.Body != nil && b.Body != nil {
		if a.Body.raw.Equals != nil && b.Body.raw.Equals != nil && *a.Body.raw.Equals != *b.Body.raw.Equals {
			return true
		}
		if a.Body.hasJSONEquals && b.Body.hasJSONEquals && !jsonDeepEqual(a.Body.jsonEqualsNorm, b.Body.jsonEqualsNorm) {
			return true
		}
		for _, jfa := range a.Body.jsonFields {
			for _, jfb := range b.Body.jsonFields {
				if strings.Join(jfa.tokens, "/") == strings.Join(jfb.tokens, "/") {
					if jfa.raw.Type != "" && jfb.raw.Type != "" && jfa.raw.Type != jfb.raw.Type {
						return true
					}
					if valueMatchersDisjoint(jfa.vm.raw, jfb.vm.raw) {
						return true
					}
				}
			}
		}
	}
	return false
}

func valueMatchersDisjoint(a, b ValueMatcher) bool {
	// exists: true vs exists: false
	if a.Exists != nil && b.Exists != nil && *a.Exists != *b.Exists {
		return true
	}
	if a.Exists != nil && !*a.Exists && (b.Equals != nil || len(b.In) > 0 || b.Contains != nil || b.Prefix != nil) {
		return true
	}
	if b.Exists != nil && !*b.Exists && (a.Equals != nil || len(a.In) > 0 || a.Contains != nil || a.Prefix != nil) {
		return true
	}
	// equals vs equals
	if a.Equals != nil && b.Equals != nil && *a.Equals != *b.Equals {
		return true
	}
	// equals vs notEquals
	if a.Equals != nil && b.NotEquals != nil && *a.Equals == *b.NotEquals {
		return true
	}
	if b.Equals != nil && a.NotEquals != nil && *b.Equals == *a.NotEquals {
		return true
	}
	// equals vs in
	if a.Equals != nil && len(b.In) > 0 && !sliceContains(b.In, *a.Equals) {
		return true
	}
	if b.Equals != nil && len(a.In) > 0 && !sliceContains(a.In, *b.Equals) {
		return true
	}
	// in vs in
	if len(a.In) > 0 && len(b.In) > 0 {
		anyCommon := false
		for _, va := range a.In {
			if sliceContains(b.In, va) {
				anyCommon = true
				break
			}
		}
		if !anyCommon {
			return true
		}
	}
	// prefix vs prefix (neither is a prefix of the other)
	if a.Prefix != nil && b.Prefix != nil {
		if !strings.HasPrefix(*a.Prefix, *b.Prefix) && !strings.HasPrefix(*b.Prefix, *a.Prefix) {
			return true
		}
	}
	return false
}

func sliceContains(list []string, target string) bool {
	for _, s := range list {
		if s == target {
			return true
		}
	}
	return false
}

func computeSnapshotDigest(files []*MockFile, scenario string, seed int64, rules []CompiledRule) string {
	h := sha256.New()
	fmt.Fprintf(h, "scenario:%s\nseed:%d\n", scenario, seed)
	for _, f := range files {
		fmt.Fprintf(h, "file:%s:%s:%s\n", f.ID, f.SourceRelPath, f.SourceDigest)
	}
	for _, r := range rules {
		b, _ := json.Marshal(struct {
			ID       string       `json:"id"`
			Priority int          `json:"priority"`
			Match    RuleMatch    `json:"match"`
			Response RuleResponse `json:"response"`
		}{
			ID:       r.Rule.ID,
			Priority: r.Rule.Priority,
			Match:    r.Rule.Match,
			Response: r.Response,
		})
		fmt.Fprintf(h, "rule:%s\n", string(b))
	}
	sum := h.Sum(nil)
	return hex.EncodeToString(sum[:16])
}

func isValidIdentifier(id string) bool {
	id = strings.TrimSpace(id)
	if id == "" || len(id) > 128 {
		return false
	}
	for i, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case i > 0 && (r == '_' || r == '-' || r == '.'):
		default:
			return false
		}
	}
	return true
}

func sanitizeIDSlug(raw string) string {
	raw = strings.TrimSpace(strings.ToLower(raw))
	var b strings.Builder
	for _, r := range raw {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
		case r == '-' || r == '_' || r == '.' || r == ' ' || r == '/':
			if b.Len() > 0 && b.String()[b.Len()-1] != '-' {
				b.WriteByte('-')
			}
		}
	}
	return strings.Trim(b.String(), "-._")
}

func isForbiddenCredentialHeader(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "authorization", "proxy-authorization", "cookie", "set-cookie":
		return true
	default:
		return false
	}
}

func looksLikeRawCredential(val string) bool {
	v := strings.TrimSpace(val)
	if v == "" || v == "***" || strings.Contains(v, "{{") {
		return false
	}
	lower := strings.ToLower(v)
	if strings.HasPrefix(lower, "bearer ") || strings.HasPrefix(lower, "basic ") {
		rest := strings.TrimSpace(v[6:])
		return rest != "" && rest != "***" && rest != "<redacted>" && !strings.HasPrefix(rest, "mock-")
	}
	return len(v) >= 8 && !strings.HasPrefix(lower, "mock-")
}
