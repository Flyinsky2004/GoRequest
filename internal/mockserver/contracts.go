// Package mockserver implements GoRequest's pure-Go, deterministic, stateless
// local HTTP Mock Server (C01 phase 1).
//
// Design & security invariants (docs/specs/mock-performance.md §1–3, AC-13, Q-13):
//   - Rules live under .gorequest/mocks/*.mock.yaml (or .mock.yml) and are read
//     exclusively through the confined core.Store with strict YAML AST auditing
//     and KnownFields(true).
//   - Ordering is deterministic: files are sorted by normalized relative path
//     (never filesystem traversal order) and rules are ordered by explicit
//     Priority (descending) with stable ID tie-breaking. Overlapping rules at
//     the same priority within a scenario fail compilation with a structured
//     AmbiguityError.
//   - Templates are declarative value interpolations only ({{path.*}},
//     {{query.*}}, {{var.*}}, {{req.id}}, {{req.seq}}, {{rand.int}},
//     {{rand.hex8}}, {{rand.uuid}}) seeded deterministically. No arbitrary code
//     execution and no {{secret.*}} resolution in mock responses.
//   - Default bind is loopback-only (127.0.0.1). Non-loopback/LAN addresses are
//     rejected unless AllowLAN is explicitly enabled. Opening a project never
//     starts a listener automatically.
//   - Unmatched requests return a structured 404 diagnostic JSON and never
//     proxy or forward to the public network.
//   - Access logs are bounded in memory and scrubbed via core.Redactor plus
//     header/query/JSON-body secret masking; captured secrets are never written
//     to rules or disk.
package mockserver

import (
	"bytes"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"gorequest/internal/core"
)

// MockSchemaVersion is the only mock file schemaVersion supported by this build.
const MockSchemaVersion = 1

// Default resource and safety bounds for mock files and runtime execution.
const (
	DefaultBindHost              = "127.0.0.1"
	DefaultBindPort              = 4010
	DefaultScenario              = "default"
	DefaultMaxMockFileBytes      = 1 << 20 // 1 MiB per .mock.yaml file
	DefaultMaxRulesPerProject    = 512
	DefaultMaxRequestBodyBytes   = 1 << 20 // 1 MiB max incoming request body
	DefaultMaxResponseBodyBytes  = 4 << 20 // 4 MiB max synthesized response body
	DefaultMaxDelayMs            = 10000   // 10s max simulated response delay
	DefaultMaxConcurrentRequests = 64
	DefaultMaxAccessLogEntries   = 200
	DefaultMaxLogBodyPreview     = 4096 // 4 KiB max body preview per access log record
	DefaultShutdownTimeout       = 2 * time.Second
)

// Standard built-in scenario names offered alongside rule-defined scenarios.
var BuiltinScenarios = []string{
	"default",
	"success",
	"error_400",
	"error_401",
	"error_500",
}

// ServerLimits bounds memory, concurrency, payload sizes, and artificial delays
// for a running Mock Server. Zero fields fall back to Default* constants.
type ServerLimits struct {
	MaxRequestBodyBytes   int64
	MaxResponseBodyBytes  int64
	MaxDelayMs            int
	MaxConcurrentRequests int
	MaxAccessLogEntries   int
	MaxLogBodyPreview     int
	ShutdownTimeout       time.Duration
}

// Effective returns a copy of l with zero or negative fields replaced by safe defaults.
func (l ServerLimits) Effective() ServerLimits {
	out := l
	if out.MaxRequestBodyBytes <= 0 {
		out.MaxRequestBodyBytes = DefaultMaxRequestBodyBytes
	}
	if out.MaxResponseBodyBytes <= 0 {
		out.MaxResponseBodyBytes = DefaultMaxResponseBodyBytes
	}
	if out.MaxDelayMs <= 0 {
		out.MaxDelayMs = DefaultMaxDelayMs
	}
	if out.MaxConcurrentRequests <= 0 {
		out.MaxConcurrentRequests = DefaultMaxConcurrentRequests
	}
	if out.MaxAccessLogEntries <= 0 {
		out.MaxAccessLogEntries = DefaultMaxAccessLogEntries
	}
	if out.MaxLogBodyPreview <= 0 {
		out.MaxLogBodyPreview = DefaultMaxLogBodyPreview
	}
	if out.ShutdownTimeout <= 0 {
		out.ShutdownTimeout = DefaultShutdownTimeout
	}
	return out
}

// MockFile represents one `.gorequest/mocks/*.mock.yaml` definition file.
type MockFile struct {
	SchemaVersion   int               `yaml:"schemaVersion" json:"schemaVersion"`
	ID              string            `yaml:"id" json:"id"`
	Name            string            `yaml:"name" json:"name"`
	Description     string            `yaml:"description,omitempty" json:"description,omitempty"`
	DefaultScenario string            `yaml:"defaultScenario,omitempty" json:"defaultScenario,omitempty"`
	Variables       map[string]string `yaml:"variables,omitempty" json:"variables,omitempty"`
	CORS            *CORSConfig       `yaml:"cors,omitempty" json:"cors,omitempty"`
	Rules           []MockRule        `yaml:"rules" json:"rules"`

	// SourceRelPath and SourceDigest are populated when loaded from a project Store.
	SourceRelPath string `yaml:"-" json:"sourceRelPath,omitempty"`
	SourceDigest  string `yaml:"-" json:"sourceDigest,omitempty"`
}

// CORSConfig configures optional, explicit CORS headers on mock responses.
// Wildcard "*" origin with AllowCredentials=true is rejected at validation time.
type CORSConfig struct {
	AllowOrigins     []string `yaml:"allowOrigins,omitempty" json:"allowOrigins,omitempty"`
	AllowMethods     []string `yaml:"allowMethods,omitempty" json:"allowMethods,omitempty"`
	AllowHeaders     []string `yaml:"allowHeaders,omitempty" json:"allowHeaders,omitempty"`
	ExposeHeaders    []string `yaml:"exposeHeaders,omitempty" json:"exposeHeaders,omitempty"`
	MaxAgeSeconds    int      `yaml:"maxAgeSeconds,omitempty" json:"maxAgeSeconds,omitempty"`
	AllowCredentials bool     `yaml:"allowCredentials,omitempty" json:"allowCredentials,omitempty"`
}

// MockRule is one deterministic stateless HTTP rule inside a MockFile.
type MockRule struct {
	ID          string                  `yaml:"id" json:"id"`
	Name        string                  `yaml:"name,omitempty" json:"name,omitempty"`
	Description string                  `yaml:"description,omitempty" json:"description,omitempty"`
	Enabled     *bool                   `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	Priority    int                     `yaml:"priority" json:"priority"`
	Scenarios   []string                `yaml:"scenarios,omitempty" json:"scenarios,omitempty"`
	Match       RuleMatch               `yaml:"match" json:"match"`
	Response    RuleResponse            `yaml:"response" json:"response"`
	Variants    map[string]RuleResponse `yaml:"variants,omitempty" json:"variants,omitempty"`
}

// IsEnabled reports whether r is active (nil defaults to true).
func (r MockRule) IsEnabled() bool {
	return r.Enabled == nil || *r.Enabled
}

// RuleMatch specifies the HTTP request conditions required to match a MockRule.
type RuleMatch struct {
	Method     string                  `yaml:"method" json:"method"`
	Path       string                  `yaml:"path" json:"path"`
	PathParams map[string]ValueMatcher `yaml:"pathParams,omitempty" json:"pathParams,omitempty"`
	Query      []KeyValueMatcher       `yaml:"query,omitempty" json:"query,omitempty"`
	Headers    []KeyValueMatcher       `yaml:"headers,omitempty" json:"headers,omitempty"`
	Body       *BodyMatcher            `yaml:"body,omitempty" json:"body,omitempty"`
}

// ValueMatcher tests a single string value (path param, query param, header, or JSON field).
type ValueMatcher struct {
	Exists    *bool    `yaml:"exists,omitempty" json:"exists,omitempty"`
	Equals    *string  `yaml:"equals,omitempty" json:"equals,omitempty"`
	NotEquals *string  `yaml:"notEquals,omitempty" json:"notEquals,omitempty"`
	In        []string `yaml:"in,omitempty" json:"in,omitempty"`
	Contains  *string  `yaml:"contains,omitempty" json:"contains,omitempty"`
	Prefix    *string  `yaml:"prefix,omitempty" json:"prefix,omitempty"`
	Regex     *string  `yaml:"regex,omitempty" json:"regex,omitempty"`
}

// KeyValueMatcher binds a named Query parameter or HTTP Header to a ValueMatcher.
type KeyValueMatcher struct {
	Name       string   `yaml:"name" json:"name"`
	Exists     *bool    `yaml:"exists,omitempty" json:"exists,omitempty"`
	Equals     *string  `yaml:"equals,omitempty" json:"equals,omitempty"`
	NotEquals  *string  `yaml:"notEquals,omitempty" json:"notEquals,omitempty"`
	In         []string `yaml:"in,omitempty" json:"in,omitempty"`
	Contains   *string  `yaml:"contains,omitempty" json:"contains,omitempty"`
	Prefix     *string  `yaml:"prefix,omitempty" json:"prefix,omitempty"`
	Regex      *string  `yaml:"regex,omitempty" json:"regex,omitempty"`
	IgnoreCase bool     `yaml:"ignoreCase,omitempty" json:"ignoreCase,omitempty"`
}

// ToValueMatcher extracts the value-matching predicates from kv.
func (kv KeyValueMatcher) ToValueMatcher() ValueMatcher {
	return ValueMatcher{
		Exists:    kv.Exists,
		Equals:    kv.Equals,
		NotEquals: kv.NotEquals,
		In:        kv.In,
		Contains:  kv.Contains,
		Prefix:    kv.Prefix,
		Regex:     kv.Regex,
	}
}

// BodyMatcher specifies text or structured JSON conditions on the request body.
type BodyMatcher struct {
	Equals     *string            `yaml:"equals,omitempty" json:"equals,omitempty"`
	Contains   *string            `yaml:"contains,omitempty" json:"contains,omitempty"`
	Prefix     *string            `yaml:"prefix,omitempty" json:"prefix,omitempty"`
	Regex      *string            `yaml:"regex,omitempty" json:"regex,omitempty"`
	JSONEquals any                `yaml:"jsonEquals,omitempty" json:"jsonEquals,omitempty"`
	JSONFields []JSONFieldMatcher `yaml:"jsonFields,omitempty" json:"jsonFields,omitempty"`
}

// JSONFieldMatcher matches a specific JSON field inside a JSON request body
// using either a JSONPath-like dot path ("$.user.id" / "user.id") or an
// RFC 6901 JSON Pointer ("/user/id").
type JSONFieldMatcher struct {
	Path      string   `yaml:"path" json:"path"`
	Exists    *bool    `yaml:"exists,omitempty" json:"exists,omitempty"`
	Equals    *string  `yaml:"equals,omitempty" json:"equals,omitempty"`
	NotEquals *string  `yaml:"notEquals,omitempty" json:"notEquals,omitempty"`
	In        []string `yaml:"in,omitempty" json:"in,omitempty"`
	Contains  *string  `yaml:"contains,omitempty" json:"contains,omitempty"`
	Prefix    *string  `yaml:"prefix,omitempty" json:"prefix,omitempty"`
	Regex     *string  `yaml:"regex,omitempty" json:"regex,omitempty"`
	Type      string   `yaml:"type,omitempty" json:"type,omitempty"` // string | number | boolean | object | array | null
}

// ToValueMatcher extracts the string value predicates of a JSONFieldMatcher.
func (jf JSONFieldMatcher) ToValueMatcher() ValueMatcher {
	return ValueMatcher{
		Exists:    jf.Exists,
		Equals:    jf.Equals,
		NotEquals: jf.NotEquals,
		In:        jf.In,
		Contains:  jf.Contains,
		Prefix:    jf.Prefix,
		Regex:     jf.Regex,
	}
}

// DelayRange configures a deterministic seeded delay in [Min, Max] milliseconds.
type DelayRange struct {
	Min int `yaml:"min" json:"min"`
	Max int `yaml:"max" json:"max"`
}

// RuleResponse defines the HTTP status, headers, body, and optional artificial
// delay returned when a rule (or scenario variant) matches.
type RuleResponse struct {
	Status       int         `yaml:"status" json:"status"`
	ContentType  string      `yaml:"contentType,omitempty" json:"contentType,omitempty"`
	Headers      []core.KV   `yaml:"headers,omitempty" json:"headers,omitempty"`
	Body         string      `yaml:"body,omitempty" json:"body,omitempty"`
	DelayMs      int         `yaml:"delayMs,omitempty" json:"delayMs,omitempty"`
	DelayRangeMs *DelayRange `yaml:"delayRangeMs,omitempty" json:"delayRangeMs,omitempty"`
}

// ParseMockFile strictly parses and validates the YAML structure of a single
// `.mock.yaml` document without touching the filesystem.
func ParseMockFile(data []byte) (*MockFile, error) {
	if len(data) == 0 {
		return nil, &core.ConfigError{Err: fmt.Errorf("mock file is empty")}
	}
	if int64(len(data)) > DefaultMaxMockFileBytes {
		return nil, &core.ConfigError{Err: fmt.Errorf("mock file exceeds size limit (%d > %d bytes)", len(data), DefaultMaxMockFileBytes)}
	}
	doc, err := core.ParseStrictYAMLTree(data, core.YAMLLimits{MaxBytes: DefaultMaxMockFileBytes})
	if err != nil {
		return nil, err
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, &core.ConfigError{Err: fmt.Errorf("mock file root must be a YAML mapping")}
	}

	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var mf MockFile
	if err := dec.Decode(&mf); err != nil {
		if err == io.EOF {
			return nil, &core.ConfigError{Err: fmt.Errorf("mock file is empty")}
		}
		return nil, &core.ConfigError{Err: err}
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return nil, &core.ConfigError{Err: fmt.Errorf("expected a single YAML document in mock file, found multiple")}
		}
		return nil, &core.ConfigError{Err: err}
	}
	if err := ValidateMockFileStructure(&mf, ServerLimits{}); err != nil {
		return nil, err
	}
	return &mf, nil
}

// EncodeMockFile serializes mf to canonical 2-space indented YAML after
// validating its structure.
func EncodeMockFile(mf *MockFile) ([]byte, error) {
	if err := ValidateMockFileStructure(mf, ServerLimits{}); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(mf); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	out := buf.Bytes()
	if len(out) == 0 || out[len(out)-1] != '\n' {
		buf.WriteByte('\n')
		out = buf.Bytes()
	}
	return out, nil
}

// LoadMockFile reads and strictly parses one mock file relative to the project's
// .gorequest/ store.
func LoadMockFile(project *core.Project, relPath string) (*MockFile, error) {
	if project == nil || project.Store == nil {
		return nil, &core.ConfigError{Err: fmt.Errorf("loading mock file requires a loaded project")}
	}
	cleanRel := filepath.ToSlash(strings.TrimSpace(relPath))
	data, digest, err := project.Store.ReadBounded(cleanRel, DefaultMaxMockFileBytes)
	if err != nil {
		return nil, fmt.Errorf("mock file %q: %w", cleanRel, err)
	}
	mf, err := ParseMockFile(data)
	if err != nil {
		return nil, fmt.Errorf("mock file %q: %w", cleanRel, err)
	}
	mf.SourceRelPath = cleanRel
	mf.SourceDigest = digest
	return mf, nil
}

// IsMockFileName reports whether name has a supported mock file suffix
// (`.mock.yaml` or `.mock.yml`).
func IsMockFileName(name string) bool {
	lower := strings.ToLower(strings.TrimSpace(name))
	return strings.HasSuffix(lower, ".mock.yaml") || strings.HasSuffix(lower, ".mock.yml")
}

// ListProjectMockFiles returns all `.gorequest/mocks/*.mock.yaml` (and `.mock.yml`)
// relative paths sorted in deterministic lexical order, never relying on OS
// directory iteration order.
func ListProjectMockFiles(project *core.Project) ([]string, error) {
	if project == nil || project.Store == nil {
		return nil, &core.ConfigError{Err: fmt.Errorf("listing mock files requires a loaded project")}
	}
	if !project.Store.Exists("mocks") {
		return nil, nil
	}
	files, err := project.Store.List("mocks", IsMockFileName)
	if err != nil {
		return nil, err
	}
	for i := range files {
		files[i] = filepath.ToSlash(files[i])
	}
	sort.Strings(files)
	return files, nil
}

// LoadProjectMocks reads and validates all `.gorequest/mocks/*.mock.yaml` files
// in deterministic path order, verifying cross-file MockFile ID and MockRule ID
// uniqueness.
func LoadProjectMocks(project *core.Project) ([]*MockFile, error) {
	paths, err := ListProjectMockFiles(project)
	if err != nil {
		return nil, err
	}
	var files []*MockFile
	seenFileIDs := map[string]string{}
	seenRuleIDs := map[string]string{}
	totalRules := 0

	for _, rel := range paths {
		mf, err := LoadMockFile(project, rel)
		if err != nil {
			return nil, err
		}
		if prevRel, dup := seenFileIDs[mf.ID]; dup {
			return nil, &core.ConfigError{
				Err: fmt.Errorf("duplicate mock file id %q in %s and %s", mf.ID, prevRel, rel),
			}
		}
		seenFileIDs[mf.ID] = rel
		for _, r := range mf.Rules {
			totalRules++
			if totalRules > DefaultMaxRulesPerProject {
				return nil, &core.ConfigError{
					Err: fmt.Errorf("project exceeds maximum mock rules (%d)", DefaultMaxRulesPerProject),
				}
			}
			if prevLoc, dup := seenRuleIDs[r.ID]; dup {
				return nil, &core.ConfigError{
					Err: fmt.Errorf("duplicate mock rule id %q in %s (already defined in %s)", r.ID, rel, prevLoc),
				}
			}
			seenRuleIDs[r.ID] = fmt.Sprintf("%s#%s", rel, r.ID)
		}
		files = append(files, mf)
	}
	return files, nil
}

// SaveMockFile encodes and atomically saves mf to `.gorequest/mocks/<slug>.mock.yaml`
// (or mf.SourceRelPath if already set) with optional baseline digest checking.
func SaveMockFile(project *core.Project, mf *MockFile, baselineDigest string) (string, error) {
	if project == nil || project.Store == nil {
		return "", &core.ConfigError{Err: fmt.Errorf("saving mock file requires a loaded project")}
	}
	if mf == nil {
		return "", &core.ConfigError{Err: fmt.Errorf("mock file is nil")}
	}
	if mf.SchemaVersion == 0 {
		mf.SchemaVersion = MockSchemaVersion
	}
	data, err := EncodeMockFile(mf)
	if err != nil {
		return "", err
	}
	rel := strings.TrimSpace(mf.SourceRelPath)
	if rel == "" {
		slug := sanitizeIDSlug(mf.ID)
		if slug == "" {
			return "", &core.ConfigError{Err: fmt.Errorf("invalid mock file id %q", mf.ID)}
		}
		rel = "mocks/" + slug + ".mock.yaml"
	}
	rel = filepath.ToSlash(rel)
	if !strings.HasPrefix(rel, "mocks/") || !IsMockFileName(rel) {
		return "", &core.ConfigError{Err: fmt.Errorf("mock file path %q must be under mocks/*.mock.yaml", rel)}
	}
	if err := project.Store.SaveChecked(rel, data, baselineDigest); err != nil {
		return "", err
	}
	digest := core.Digest(data)
	mf.SourceRelPath = rel
	mf.SourceDigest = digest
	return digest, nil
}
