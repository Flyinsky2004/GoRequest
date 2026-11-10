// Package runner implements GoRequest's pure-Go automation engine: typed
// assertions, JSONPath extraction, declarative pre/post actions, collection and
// suite DAG execution, data-row isolation, retry, cancellation, live events and
// JSON/JUnit reports.
//
// The package deliberately depends only on internal/core. It performs no GUI
// work, opens no windows and never writes run state into the shared project.
// Network execution reuses core.ExecuteWithOptions, so stage timeouts, proxy,
// TLS/mTLS, target policy and the isolated CookieJar behave exactly like the
// desktop send path.
//
// The YAML formats in this file are the A01 initial contract. They are
// documented in docs/specs/runner-core-contracts.md and are not yet frozen:
// unknown keys are rejected so incompatible files fail loudly instead of being
// silently misread.
package runner

import (
	"fmt"
	"time"

	"gopkg.in/yaml.v3"
)

// SuiteSchemaVersion is the only suite schema version this build reads.
const SuiteSchemaVersion = 1

// ReportFormatVersion is embedded in JSON reports.
const ReportFormatVersion = 1

const (
	// DefaultMaxRows bounds dataset rows loaded into memory.
	DefaultMaxRows = 1000
	// DefaultMaxDatasetBytes bounds the raw dataset file.
	DefaultMaxDatasetBytes int64 = 8 << 20
	// DefaultMaxSuiteBytes bounds a suite YAML file.
	DefaultMaxSuiteBytes int64 = 1 << 20
	// DefaultMaxColumns bounds dataset columns.
	DefaultMaxColumns = 256
	// DefaultStepConcurrency bounds concurrently running independent steps
	// inside one row. Dependencies always wait for all required upstreams.
	DefaultStepConcurrency = 4
	// DefaultRowConcurrency keeps dataset rows sequential by default so row
	// isolation and report ordering stay deterministic.
	DefaultRowConcurrency = 1
	// MaxStepConcurrency is the effective upper bound accepted by Options and
	// ValidateConcurrency.
	MaxStepConcurrency = 64
	// MaxRowConcurrency is the effective upper bound accepted by Options and
	// ValidateConcurrency.
	MaxRowConcurrency = 16
	// MaxBackoff caps retry backoff.
	MaxBackoff = 30 * time.Second
)

// ValidateConcurrency validates user-provided concurrency bounds. Zero selects
// the engine default; negative or above-limit values are explicit errors rather
// than being silently clamped by the desktop.
func ValidateConcurrency(step, row int) error {
	if step < 0 || step > MaxStepConcurrency {
		return fmt.Errorf("step concurrency must be between 0 and %d", MaxStepConcurrency)
	}
	if row < 0 || row > MaxRowConcurrency {
		return fmt.Errorf("row concurrency must be between 0 and %d", MaxRowConcurrency)
	}
	return nil
}

// OnFailure controls what happens after a step fails inside one row.
type OnFailure string

const (
	// OnFailureStop stops starting new steps, including independent branches.
	OnFailureStop OnFailure = "stop"
	// OnFailureContinue keeps running independent branches; dependents of a
	// failed step are skipped.
	OnFailureContinue OnFailure = "continue"
)

// Suite is a declarative, cross-collection test suite. Steps form a DAG through
// Needs; declaration order only breaks ties when several steps are ready.
type Suite struct {
	SchemaVersion int               `yaml:"schemaVersion"`
	ID            string            `yaml:"id"`
	Name          string            `yaml:"name"`
	Variables     map[string]string `yaml:"variables,omitempty"`
	OnFailure     OnFailure         `yaml:"onFailure,omitempty"`
	Dataset       *DatasetSpec      `yaml:"dataset,omitempty"`
	Before        []Action          `yaml:"before,omitempty"`
	After         []Action          `yaml:"after,omitempty"`
	Steps         []SuiteStep       `yaml:"steps"`

	// sourceDigest is the SHA-256 of the suite file as read from disk. It is
	// empty for programmatically built suites and replaced by a canonical
	// encoding when the snapshot digest is computed.
	sourceDigest string
}

// SuiteStep is one request invocation plus its assertions, extractions,
// declarative actions and optional retry policy.
type SuiteStep struct {
	ID      string       `yaml:"id"`
	Name    string       `yaml:"name,omitempty"`
	Request string       `yaml:"request"`
	Needs   []string     `yaml:"needs,omitempty"`
	Assert  []yaml.Node  `yaml:"assert,omitempty"`
	Extract []Extraction `yaml:"extract,omitempty"`
	Retry   *RetryPolicy `yaml:"retry,omitempty"`
	Before  []Action     `yaml:"before,omitempty"`
	After   []Action     `yaml:"after,omitempty"`
}

// DatasetSpec selects the data-driven rows for a suite. File paths are relative
// to the .gorequest/ directory and resolved through the confined core.Store, so
// path escapes and symlink escapes are rejected before any request is sent.
type DatasetSpec struct {
	Format    string            `yaml:"format"` // csv | json
	File      string            `yaml:"file"`
	MaxRows   int               `yaml:"maxRows,omitempty"`
	MaxBytes  int64             `yaml:"maxBytes,omitempty"`
	Fields    []string          `yaml:"fields,omitempty"`
	Delimiter string            `yaml:"delimiter,omitempty"`
	Types     map[string]string `yaml:"types,omitempty"` // field -> string|number|boolean
}

// Extraction publishes one value from a successful step as
// {{steps.<stepID>.<Name>}}. Required outputs default to true: a missing
// required value fails the step and publishes nothing.
type Extraction struct {
	Name      string `yaml:"name"`
	From      string `yaml:"from"` // jsonpath | header | cookie | body
	Path      string `yaml:"path,omitempty"`
	Header    string `yaml:"header,omitempty"`
	Cookie    string `yaml:"cookie,omitempty"`
	Regex     string `yaml:"regex,omitempty"`
	Group     *int   `yaml:"group,omitempty"`
	Required  *bool  `yaml:"required,omitempty"`
	Sensitive *bool  `yaml:"sensitive,omitempty"`
}

// IsRequired reports whether a missing extraction fails the step. The default
// is true so a half-built output never silently flows downstream.
func (e Extraction) IsRequired() bool { return e.Required == nil || *e.Required }

// Action is one declarative pre/post action. Arbitrary scripts are out of
// scope by design.
type Action struct {
	Set      *SetAction      `yaml:"set,omitempty"`
	Generate *GenerateAction `yaml:"generate,omitempty"`
}

// SetAction assigns a template value to an ordinary run/step variable.
type SetAction struct {
	Name  string `yaml:"name"`
	Value string `yaml:"value"`
}

// GenerateAction creates dynamic data once per scope. Supported kinds:
// uuid, timestamp, timestampMs, randomString, randomInt.
type GenerateAction struct {
	Name   string `yaml:"name"`
	Kind   string `yaml:"kind"`
	Length int    `yaml:"length,omitempty"`
	Min    int    `yaml:"min,omitempty"`
	Max    int    `yaml:"max,omitempty"`
	Prefix string `yaml:"prefix,omitempty"`
}

// RetryPolicy is explicit: absent or MaxAttempts <= 1 disables retries. Config
// errors, cancellations and production-confirmation failures are never retried.
type RetryPolicy struct {
	MaxAttempts        int      `yaml:"maxAttempts"`
	On                 []string `yaml:"on,omitempty"` // network | timeout | status | assertion
	Statuses           []int    `yaml:"statuses,omitempty"`
	DelayMs            int      `yaml:"delayMs,omitempty"`
	Backoff            float64  `yaml:"backoff,omitempty"` // multiplier >= 1
	JitterMs           int      `yaml:"jitterMs,omitempty"`
	DeadlineMs         int      `yaml:"deadlineMs,omitempty"`
	AllowNonIdempotent bool     `yaml:"allowNonIdempotent,omitempty"`
}

// EffectiveMaxAttempts returns at least one attempt.
func (p *RetryPolicy) EffectiveMaxAttempts() int {
	if p == nil || p.MaxAttempts < 1 {
		return 1
	}
	return p.MaxAttempts
}

// Retry reasons accepted in RetryPolicy.On.
const (
	RetryNetwork   = "network"
	RetryTimeout   = "timeout"
	RetryStatus    = "status"
	RetryAssertion = "assertion"
)
