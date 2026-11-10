package runner

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"gorequest/internal/core"
)

// Status is the final state of a run, row or step.
type Status string

const (
	StatusPassed        Status = "passed"
	StatusFailed        Status = "failed"
	StatusSkipped       Status = "skipped"
	StatusCanceled      Status = "canceled"
	StatusConfigError   Status = "config_error"
	StatusInternalError Status = "internal_error"
)

// FailureCategory groups failures exactly as the desktop automation
// specification requires: run, config, internal, canceled. Assertion and
// extraction failures are reported as run failures with a specific Kind so the
// UI can still separate "the server answered wrongly" from "the network broke".
type FailureCategory string

const (
	FailureRun       FailureCategory = "run"
	FailureConfig    FailureCategory = "config"
	FailureInternal  FailureCategory = "internal"
	FailureCanceled  FailureCategory = "canceled"
	FailureAssertion FailureCategory = "assertion"
)

// FailureKind is a finer, stable diagnostic label.
type FailureKind string

const (
	KindTimeout       FailureKind = "timeout"
	KindTLS           FailureKind = "tls"
	KindConnection    FailureKind = "connection"
	KindProtocol      FailureKind = "protocol"
	KindAssertion     FailureKind = "assertion"
	KindExtraction    FailureKind = "extraction"
	KindSchema        FailureKind = "schema"
	KindTargetPolicy  FailureKind = "target_policy"
	KindDataset       FailureKind = "dataset"
	KindDefinition    FailureKind = "definition"
	KindMissingSecret FailureKind = "missing_secret"
	KindTemplate      FailureKind = "template"
	KindAuth          FailureKind = "auth"
	KindReport        FailureKind = "report"
	KindPanic         FailureKind = "panic"
	KindOther         FailureKind = "other"
)

// Failure is redacted before it is stored: Message never contains registered
// secrets, extracted sensitive values or sensitive transport slots.
type Failure struct {
	Category FailureCategory `json:"category"`
	Kind     FailureKind     `json:"kind"`
	Message  string          `json:"message"`
}

func (f *Failure) Error() string {
	if f == nil {
		return ""
	}
	return f.Message
}

func newFailure(cat FailureCategory, kind FailureKind, msg string) *Failure {
	return &Failure{Category: cat, Kind: kind, Message: msg}
}

// classifyError maps an execution/build error to a category and kind. Config
// errors (unknown variables, missing secrets, attachment problems, target
// policy requiring confirmation) are never retried and never reach the network.
func classifyError(err error) *Failure {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		if errors.Is(err, context.DeadlineExceeded) {
			return newFailure(FailureRun, KindTimeout, err.Error())
		}
		return newFailure(FailureCanceled, KindOther, err.Error())
	}
	msg := err.Error()
	lower := strings.ToLower(msg)
	if core.IsConfig(err) || strings.Contains(lower, "target policy") || strings.Contains(lower, "requires confirmation") {
		return newFailure(FailureConfig, KindTargetPolicy, msg)
	}
	if core.IsRun(err) {
		return newFailure(FailureRun, classifyRunKind(err, lower), msg)
	}
	var x509Err x509.UnknownAuthorityError
	if errors.As(err, &x509Err) {
		return newFailure(FailureRun, KindTLS, msg)
	}
	var certErr *x509.CertificateInvalidError
	if errors.As(err, &certErr) {
		return newFailure(FailureRun, KindTLS, msg)
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		if netErr.Timeout() || strings.Contains(lower, "timed out") {
			return newFailure(FailureRun, KindTimeout, msg)
		}
		return newFailure(FailureRun, KindConnection, msg)
	}
	if strings.Contains(lower, "canceled") || strings.Contains(lower, "cancelled") {
		return newFailure(FailureCanceled, KindOther, msg)
	}
	return newFailure(FailureInternal, KindOther, msg)
}

func classifyRunKind(err error, lower string) FailureKind {
	if strings.Contains(lower, "timed out") || strings.Contains(lower, "timeout") || strings.Contains(lower, "deadline exceeded") {
		return KindTimeout
	}
	if strings.Contains(lower, "tls") || strings.Contains(lower, "certificate") || strings.Contains(lower, "x509") {
		return KindTLS
	}
	if strings.Contains(lower, "canceled") || strings.Contains(lower, "cancelled") {
		return KindCanceled // caller rewrites category below
	}
	if strings.Contains(lower, "connection refused") || strings.Contains(lower, "no such host") ||
		strings.Contains(lower, "connection reset") || strings.Contains(lower, "broken pipe") ||
		strings.Contains(lower, "dial") || strings.Contains(lower, "unreachable") {
		return KindConnection
	}
	return KindProtocol
}

// KindCanceled is used while interpreting run errors; classifyError rewrites the
// category to FailureCanceled via the generic cancel detection.
const KindCanceled FailureKind = "canceled"

// classifyAuthError maps an OAuth/OIDC provider failure. Credential and
// configuration problems are config errors (never retried); token-endpoint
// transport failures are run errors. Either way the business request has not
// been sent.
func classifyAuthError(err error) *Failure {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return newFailure(FailureCanceled, KindOther, err.Error())
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return newFailure(FailureRun, KindTimeout, err.Error())
	}
	var oe *core.OAuthError
	if errors.As(err, &oe) {
		switch oe.Category {
		case core.OAuthErrInteractionRequired, core.OAuthErrReauthRequired,
			core.OAuthErrInvalidConfig, core.OAuthErrDiscoveryMismatch,
			core.OAuthErrIDTokenMalformed, core.OAuthErrIDTokenInvalidSignature,
			core.OAuthErrIDTokenInvalidClaims, core.OAuthErrIDTokenExpired,
			core.OAuthErrStateMismatch, core.OAuthErrNonceMismatch,
			core.OAuthErrCallbackFailed, core.OAuthErrStoreUnavailable,
			core.OAuthErrAccessDenied, core.OAuthErrExpiredDeviceCode:
			return newFailure(FailureConfig, KindAuth, err.Error())
		case core.OAuthErrTargetPolicyDenied:
			return newFailure(FailureConfig, KindTargetPolicy, err.Error())
		case core.OAuthErrTokenExchangeFailed, core.OAuthErrRefreshFailed:
			// A 4xx token-endpoint rejection needs operator action; 5xx/429 is a
			// transport-level run failure worth surfacing as such.
			if oe.StatusCode >= 400 && oe.StatusCode < 500 {
				return newFailure(FailureConfig, KindAuth, err.Error())
			}
			return newFailure(FailureRun, KindAuth, err.Error())
		case core.OAuthErrDiscoveryFailed, core.OAuthErrJWKSFetchFailed, core.OAuthErrJWKSKeyNotFound:
			return newFailure(FailureRun, KindAuth, err.Error())
		}
	}
	if core.IsConfig(err) {
		return newFailure(FailureConfig, KindAuth, err.Error())
	}
	return newFailure(FailureInternal, KindAuth, err.Error())
}

// HeaderKV is one redacted response header retained for reporting.
type HeaderKV struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// AttemptResult records one execution attempt, including failed ones, so retry
// history is never erased.
type AttemptResult struct {
	ProtocolFacts json.RawMessage   `json:"protocolFacts,omitempty"`
	Number        int               `json:"number"`
	Status        Status            `json:"status"`
	StartedAt     time.Time         `json:"startedAt"`
	DurationMs    int64             `json:"durationMs"`
	HTTPStatus    int               `json:"httpStatus,omitempty"`
	Proto         string            `json:"proto,omitempty"`
	ResponseBytes int64             `json:"responseBytes,omitempty"`
	Truncated     bool              `json:"truncated,omitempty"`
	ContentType   string            `json:"contentType,omitempty"`
	FinalURL      string            `json:"finalUrl,omitempty"`
	Headers       []HeaderKV        `json:"headers,omitempty"`
	Failure       *Failure          `json:"failure,omitempty"`
	Assertions    []AssertionResult `json:"assertions,omitempty"`
	Extracted     []string          `json:"extracted,omitempty"`
}

// AssertionResult is one evaluated assertion. Expected and Actual are redacted
// before storage.
type AssertionResult struct {
	Kind     string `json:"kind"`
	Target   string `json:"target,omitempty"`
	Passed   bool   `json:"passed"`
	Message  string `json:"message,omitempty"`
	Expected string `json:"expected,omitempty"`
	Actual   string `json:"actual,omitempty"`
}

// ExtractionResult reports whether an output was published. Sensitive values
// are recorded as "***"; the value never enters events or reports.
type ExtractionResult struct {
	Name      string `json:"name"`
	From      string `json:"from"`
	Target    string `json:"target,omitempty"`
	Required  bool   `json:"required"`
	Sensitive bool   `json:"sensitive"`
	Published bool   `json:"published"`
	Value     string `json:"value,omitempty"`
	Message   string `json:"message,omitempty"`
}

// StepResult is the final state of one suite step in one row.
type StepResult struct {
	ID          string             `json:"id"`
	Name        string             `json:"name,omitempty"`
	Request     string             `json:"request"`
	Status      Status             `json:"status"`
	Reason      string             `json:"reason,omitempty"`
	StartedAt   time.Time          `json:"startedAt"`
	DurationMs  int64              `json:"durationMs"`
	Attempts    []AttemptResult    `json:"attempts"`
	Assertions  []AssertionResult  `json:"assertions,omitempty"`
	Extractions []ExtractionResult `json:"extractions,omitempty"`
	Failure     *Failure           `json:"failure,omitempty"`
}

// RetryCount returns how many attempts were retries.
func (s StepResult) RetryCount() int {
	if len(s.Attempts) <= 1 {
		return 0
	}
	return len(s.Attempts) - 1
}

// RowResult is one dataset iteration. Every row has its own variables, step
// outputs and CookieJar.
type RowResult struct {
	Index      int               `json:"index"`
	Status     Status            `json:"status"`
	Reason     string            `json:"reason,omitempty"`
	Data       map[string]string `json:"data,omitempty"`
	StartedAt  time.Time         `json:"startedAt"`
	DurationMs int64             `json:"durationMs"`
	Steps      []StepResult      `json:"steps"`
}

// Summary aggregates final step states, not attempts.
type Summary struct {
	Rows           int `json:"rows"`
	StepsTotal     int `json:"stepsTotal"`
	Passed         int `json:"passed"`
	Failed         int `json:"failed"`
	Skipped        int `json:"skipped"`
	Canceled       int `json:"canceled"`
	ConfigErrors   int `json:"configErrors"`
	InternalErrors int `json:"internalErrors"`
	Attempts       int `json:"attempts"`
	Retries        int `json:"retries"`
}

// DatasetInfo is the report-safe description of the data source.
type DatasetInfo struct {
	Format string   `json:"format,omitempty"`
	File   string   `json:"file,omitempty"`
	Fields []string `json:"fields,omitempty"`
	Rows   int      `json:"rows"`
}

// RunResult is the complete, report-safe result of one suite run. It contains
// protocol facts and verdicts, never raw request/response bodies.
type RunResult struct {
	FormatVersion   int          `json:"formatVersion"`
	ProjectID       string       `json:"projectId"`
	ProjectName     string       `json:"projectName,omitempty"`
	SchemaVersion   int          `json:"schemaVersion"`
	SuiteID         string       `json:"suiteId"`
	SuiteName       string       `json:"suiteName,omitempty"`
	RunID           string       `json:"runId"`
	Snapshot        string       `json:"snapshotDigest"`
	Environment     string       `json:"environment,omitempty"`
	Seed            int64        `json:"seed"`
	StepConcurrency int          `json:"stepConcurrency"`
	RowConcurrency  int          `json:"rowConcurrency"`
	StartedAt       time.Time    `json:"startedAt"`
	FinishedAt      time.Time    `json:"finishedAt"`
	DurationMs      int64        `json:"durationMs"`
	Status          Status       `json:"status"`
	Failure         *Failure     `json:"failure,omitempty"`
	Dataset         *DatasetInfo `json:"dataset,omitempty"`
	Rows            []RowResult  `json:"rows"`
	Summary         Summary      `json:"summary"`
	Canceled        bool         `json:"canceled,omitempty"`
}

// finalize recomputes the summary from row/step results.
func (r *RunResult) finalize() {
	var s Summary
	s.Rows = len(r.Rows)
	for i := range r.Rows {
		if r.Rows[i].Status == StatusCanceled {
			r.Canceled = true
		}
		for j := range r.Rows[i].Steps {
			st := &r.Rows[i].Steps[j]
			s.StepsTotal++
			switch st.Status {
			case StatusPassed:
				s.Passed++
			case StatusFailed:
				s.Failed++
			case StatusSkipped:
				s.Skipped++
			case StatusCanceled:
				s.Canceled++
			case StatusConfigError:
				s.ConfigErrors++
			case StatusInternalError:
				s.InternalErrors++
			}
			s.Attempts += len(st.Attempts)
			s.Retries += st.RetryCount()
		}
	}
	r.Summary = s
}

// MarshalJSON keeps empty rows as [] instead of null for machine consumers.
func (r *RunResult) MarshalJSON() ([]byte, error) {
	type alias RunResult
	cp := alias(*r)
	if cp.Rows == nil {
		cp.Rows = []RowResult{}
	}
	return json.Marshal(cp)
}

func formatFailure(cat FailureCategory, kind FailureKind, format string, args ...any) *Failure {
	return newFailure(cat, kind, fmt.Sprintf(format, args...))
}
