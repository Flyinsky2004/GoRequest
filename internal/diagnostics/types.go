// Package diagnostics implements bounded, low-concurrency HTTP performance
// diagnostics, per-hop httptrace timeline capture, nearest-rank quantile
// aggregation with independent per-stage sample counts, report comparison, and
// redacted JSON/CSV export.
//
// Scope boundary: this package is a diagnostic sampling tool for single-request
// latency and transport stage inspection. It does not perform high-concurrency
// load generation or capacity stress testing, and it never bypasses B05 target
// policy or production confirmation guardrails.
package diagnostics

import (
	"fmt"
	"strings"
	"time"
)

// ReportFormatVersion identifies the schema version of exported diagnostic reports.
const ReportFormatVersion = 1

// Hard safety bounds for low-concurrency diagnostic sampling. Unbounded or
// high-concurrency stress parameters are rejected before any network call.
const (
	DefaultIterations  = 10
	MaxIterations      = 200
	DefaultConcurrency = 1
	MaxConcurrency     = 8
	DefaultWarmup      = 0
	MaxWarmup          = 20
	MaxInterval        = 30 * time.Second
	DefaultDeadline    = 60 * time.Second
	MaxDeadline        = 5 * time.Minute
)

// ScopeDisclaimer is included in every diagnostic report to prevent
// misinterpreting low-concurrency client sampling as load/capacity testing.
const ScopeDisclaimer = "Controlled low-concurrency client HTTP diagnostics only; not a load, stress, or server capacity test."

// ConnectionStrategy specifies how HTTP connections are managed across samples.
type ConnectionStrategy string

const (
	// StrategyKeepAlive reuses idle keep-alive connections across samples when
	// the underlying transport configuration supports a run-scoped pool.
	StrategyKeepAlive ConnectionStrategy = "keep_alive"
	// StrategyFresh disables keep-alive reuse so every sample establishes a
	// fresh connection.
	StrategyFresh ConnectionStrategy = "fresh"
)

// NormalizeConnectionStrategy returns a canonical ConnectionStrategy or an
// error if raw is unrecognized. Empty defaults to StrategyKeepAlive.
func NormalizeConnectionStrategy(raw ConnectionStrategy) (ConnectionStrategy, error) {
	switch strings.ToLower(strings.TrimSpace(string(raw))) {
	case "", string(StrategyKeepAlive), "reuse", "keep-alive", "keepalive":
		return StrategyKeepAlive, nil
	case string(StrategyFresh), "no_reuse", "no-reuse", "per_request", "isolated":
		return StrategyFresh, nil
	default:
		return "", fmt.Errorf("unsupported connection strategy %q (expected %q or %q)", raw, StrategyKeepAlive, StrategyFresh)
	}
}

// ReuseSupportStatus records whether the configured ConnectionStrategy could be
// enforced across samples while honoring Core transport/resource rules.
type ReuseSupportStatus string

const (
	// ReuseSupported indicates the run-scoped transport enforced the requested
	// connection reuse policy and closed all idle connections on completion.
	ReuseSupported ReuseSupportStatus = "supported"
	// ReuseUnmeasurableFallback indicates the transport required per-request
	// Core isolation (e.g. SOCKS5 or un-frozen file references) or could not
	// guarantee cross-sample keep-alive reuse without bypassing Core rules.
	ReuseUnmeasurableFallback ReuseSupportStatus = "unmeasurable_fallback"
	// ReuseCustomExecutorUnknown indicates a caller-supplied Executor was used
	// whose internal connection pooling is opaque to the diagnostics engine.
	ReuseCustomExecutorUnknown ReuseSupportStatus = "custom_executor_unknown"
)

// StageKind identifies a single transport or protocol timing phase.
type StageKind string

const (
	StageDNS          StageKind = "dns"
	StageTCPConnect   StageKind = "tcp_connect"
	StageTLSHandshake StageKind = "tls_handshake"
	StageConnWait     StageKind = "conn_wait"
	StageRequestWrite StageKind = "request_write"
	StageServerWait   StageKind = "server_wait"
	StageTTFB         StageKind = "ttfb"
	StageDownload     StageKind = "download"
	StageHopTotal     StageKind = "hop_total"
)

// AllStageKinds lists the canonical order of stages in reports and UI tables.
var AllStageKinds = []StageKind{
	StageDNS,
	StageTCPConnect,
	StageTLSHandshake,
	StageConnWait,
	StageRequestWrite,
	StageServerWait,
	StageTTFB,
	StageDownload,
	StageHopTotal,
}

// StageObsStatus describes whether a stage occurred and was completely observed.
// Unobserved or reused stages are never recorded as 0ms samples.
type StageObsStatus string

const (
	// ObsObserved means the stage genuinely occurred and completed; its duration
	// is included in the stage's independent sample count n.
	ObsObserved StageObsStatus = "observed"
	// ObsSkippedReused means the stage did not occur because an existing
	// connection was reused (e.g. DNS/TCP/TLS on keep-alive or HTTP/2).
	ObsSkippedReused StageObsStatus = "not_applicable_reused"
	// ObsUnobserved means the stage could not be observed (e.g. proxy-side DNS
	// or tunnel setup, intermediate redirect body drain, or synthetic executor).
	ObsUnobserved StageObsStatus = "unobserved"
	// ObsIncomplete means the stage started or partially ran but did not
	// complete normally (e.g. truncated body download, timeout, or cancel).
	ObsIncomplete StageObsStatus = "incomplete"
	// ObsNotApplicable means the stage does not apply to this request/hop
	// (e.g. TLS on plain http://, or body download on HEAD / 204 / 304).
	ObsNotApplicable StageObsStatus = "not_applicable"
)

// TimeInterval represents a monotonic time span [StartOffsetMs, EndOffsetMs]
// relative to the start of the sample request. Intervals may overlap (for
// example ConnWait overlaps DNS/TCP/TLS, and Happy Eyeballs dials overlap) and
// must never be naively summed.
type TimeInterval struct {
	StartOffsetMs float64 `json:"startOffsetMs"`
	EndOffsetMs   float64 `json:"endOffsetMs"`
	DurationMs    float64 `json:"durationMs"`
}

// DialAttempt records one TCP dial attempt (supporting Happy Eyeballs concurrent
// dial inspection and identifying the winning connection).
type DialAttempt struct {
	Network    string        `json:"network,omitempty"`
	RemoteAddr string        `json:"remoteAddr,omitempty"`
	Observed   bool          `json:"observed"`
	Winner     bool          `json:"winner"`
	DurationMs float64       `json:"durationMs,omitempty"`
	Interval   *TimeInterval `json:"interval,omitempty"`
	Error      string        `json:"error,omitempty"`
}

// StageMeasurement captures one stage's observation status, monotonic duration,
// and optional relative interval within a single hop.
type StageMeasurement struct {
	Stage      StageKind      `json:"stage"`
	Status     StageObsStatus `json:"status"`
	DurationMs float64        `json:"durationMs,omitempty"`
	Interval   *TimeInterval  `json:"interval,omitempty"`
	Note       string         `json:"note,omitempty"`
	Error      string         `json:"error,omitempty"`
}

// HopTimeline captures the observed transport facts and stage intervals for one
// HTTP hop (initial request or redirect hop).
type HopTimeline struct {
	Hop                   int                            `json:"hop"`
	Method                string                         `json:"method"`
	URL                   string                         `json:"url"`
	FromURL               string                         `json:"fromUrl,omitempty"`
	StatusCode            int                            `json:"statusCode,omitempty"`
	Proto                 string                         `json:"proto,omitempty"`
	RemoteAddr            string                         `json:"remoteAddr,omitempty"`
	ReusedConn            bool                           `json:"reusedConn"`
	ReusedKnown           bool                           `json:"reusedKnown"`
	ProxyUsed             string                         `json:"proxyUsed,omitempty"`
	OriginChanged         bool                           `json:"originChanged,omitempty"`
	StrippedCredentials   bool                           `json:"strippedCredentials,omitempty"`
	TLSVersion            string                         `json:"tlsVersion,omitempty"`
	TLSCipherSuite        string                         `json:"tlsCipherSuite,omitempty"`
	TLSServerName         string                         `json:"tlsServerName,omitempty"`
	TLSInsecureSkipVerify bool                           `json:"tlsInsecureSkipVerify,omitempty"`
	CustomCAUsed          bool                           `json:"customCaUsed,omitempty"`
	ClientCertUsed        bool                           `json:"clientCertUsed,omitempty"`
	DialAttempts          []DialAttempt                  `json:"dialAttempts,omitempty"`
	Stages                map[StageKind]StageMeasurement `json:"stages"`
	Limitations           []string                       `json:"limitations,omitempty"`
}

// SampleOutcome classifies the result of one sample execution.
type SampleOutcome string

const (
	OutcomeSuccess   SampleOutcome = "success"
	OutcomeError     SampleOutcome = "error"
	OutcomeTimeout   SampleOutcome = "timeout"
	OutcomeCanceled  SampleOutcome = "canceled"
	OutcomeTruncated SampleOutcome = "truncated"
)

// SampleRecord holds the redacted outcome and per-hop timeline for one warmup
// or measured sample.
type SampleRecord struct {
	Index         int           `json:"index"`
	IsWarmup      bool          `json:"isWarmup"`
	StartedAt     time.Time     `json:"startedAt"`
	Outcome       SampleOutcome `json:"outcome"`
	StatusCode    int           `json:"statusCode,omitempty"`
	Proto         string        `json:"proto,omitempty"`
	FinalURL      string        `json:"finalUrl,omitempty"`
	DurationMs    float64       `json:"durationMs"`
	TransferSize  int64         `json:"transferSize,omitempty"`
	BodySize      int64         `json:"bodySize,omitempty"`
	Truncated     bool          `json:"truncated,omitempty"`
	ReusedConn    bool          `json:"reusedConn"`
	ReusedKnown   bool          `json:"reusedKnown"`
	ProxyUsed     string        `json:"proxyUsed,omitempty"`
	RedirectCount int           `json:"redirectCount"`
	Error         string        `json:"error,omitempty"`
	Hops          []HopTimeline `json:"hops,omitempty"`
	TimelineNotes []string      `json:"timelineNotes,omitempty"`
}

// SamplingConfig defines bounded parameters for a diagnostic run.
type SamplingConfig struct {
	Iterations           int                `json:"iterations"`
	Concurrency          int                `json:"concurrency"`
	Warmup               int                `json:"warmup"`
	Interval             time.Duration      `json:"interval"`
	Deadline             time.Duration      `json:"deadline"`
	Seed                 int64              `json:"seed"`
	ConnectionStrategy   ConnectionStrategy `json:"connectionStrategy"`
	AllowHTTPErrorStatus bool               `json:"allowHttpErrorStatus,omitempty"`
	Gate                 *ThresholdGate     `json:"gate,omitempty"`
}

// ValidateAndNormalize checks all sampling bounds and returns a normalized copy
// or a configuration error if any parameter is negative or exceeds safe limits.
func (c SamplingConfig) ValidateAndNormalize() (SamplingConfig, error) {
	out := c
	if out.Iterations == 0 {
		out.Iterations = DefaultIterations
	}
	if out.Iterations < 1 || out.Iterations > MaxIterations {
		return out, fmt.Errorf("iterations must be between 1 and %d (got %d)", MaxIterations, c.Iterations)
	}
	if out.Concurrency == 0 {
		out.Concurrency = DefaultConcurrency
	}
	if out.Concurrency < 1 || out.Concurrency > MaxConcurrency {
		return out, fmt.Errorf("concurrency must be between 1 and %d (got %d)", MaxConcurrency, c.Concurrency)
	}
	if out.Warmup < 0 || out.Warmup > MaxWarmup {
		return out, fmt.Errorf("warmup must be between 0 and %d (got %d)", MaxWarmup, c.Warmup)
	}
	if out.Interval < 0 || out.Interval > MaxInterval {
		return out, fmt.Errorf("interval must be between 0s and %s (got %s)", MaxInterval, c.Interval)
	}
	if out.Deadline == 0 {
		out.Deadline = DefaultDeadline
	}
	if out.Deadline < 0 || out.Deadline > MaxDeadline {
		return out, fmt.Errorf("deadline must be between 1ms and %s (got %s)", MaxDeadline, c.Deadline)
	}
	if out.Seed == 0 {
		out.Seed = 1
	}
	strat, err := NormalizeConnectionStrategy(out.ConnectionStrategy)
	if err != nil {
		return out, err
	}
	out.ConnectionStrategy = strat
	if out.Gate != nil {
		g, err := out.Gate.ValidateAndNormalize()
		if err != nil {
			return out, err
		}
		out.Gate = &g
	}
	return out, nil
}

// RunStatus represents the overall lifecycle status of a diagnostic run.
type RunStatus string

const (
	StatusCompleted     RunStatus = "completed"
	StatusGateFailed    RunStatus = "gate_failed"
	StatusCanceled      RunStatus = "canceled"
	StatusConfigError   RunStatus = "config_error"
	StatusPolicyBlocked RunStatus = "policy_blocked"
)

// EventKind identifies live progress notifications emitted during a run.
type EventKind string

const (
	EventRunStarted  EventKind = "run_started"
	EventSampleDone  EventKind = "sample_done"
	EventRunFinished EventKind = "run_finished"
)

// Event is a bounded, redacted progress notification for the desktop UI.
type Event struct {
	Seq         int64         `json:"seq"`
	Time        time.Time     `json:"time"`
	RunID       string        `json:"runId"`
	Kind        EventKind     `json:"kind"`
	SampleIndex int           `json:"sampleIndex,omitempty"`
	IsWarmup    bool          `json:"isWarmup,omitempty"`
	Outcome     SampleOutcome `json:"outcome,omitempty"`
	StatusCode  int           `json:"statusCode,omitempty"`
	DurationMs  float64       `json:"durationMs,omitempty"`
	Message     string        `json:"message,omitempty"`
}

// Emitter receives progress events in order.
type Emitter func(Event)

// Report is the complete, redacted output of a diagnostic run.
type Report struct {
	FormatVersion   int                         `json:"formatVersion"`
	ScopeDisclaimer string                      `json:"scopeDisclaimer"`
	RunID           string                      `json:"runId"`
	ProjectID       string                      `json:"projectId,omitempty"`
	ProjectName     string                      `json:"projectName,omitempty"`
	RequestID       string                      `json:"requestId"`
	RequestName     string                      `json:"requestName,omitempty"`
	RequestPath     string                      `json:"requestPath"`
	Method          string                      `json:"method"`
	TargetURL       string                      `json:"targetUrl"`
	SnapshotDigest  string                      `json:"snapshotDigest"`
	Environment     string                      `json:"environment,omitempty"`
	CoreVersion     string                      `json:"coreVersion"`
	StartedAt       time.Time                   `json:"startedAt"`
	FinishedAt      time.Time                   `json:"finishedAt"`
	TotalElapsedMs  float64                     `json:"totalElapsedMs"`
	Status          RunStatus                   `json:"status"`
	Canceled        bool                        `json:"canceled,omitempty"`
	FailureReason   string                      `json:"failureReason,omitempty"`
	Config          SamplingConfig              `json:"config"`
	ProxyMode       string                      `json:"proxyMode,omitempty"`
	ReuseSupport    ReuseSupportStatus          `json:"reuseSupport"`
	ReuseNote       string                      `json:"reuseNote,omitempty"`
	Summary         SampleSummary               `json:"summary"`
	SuccessLatency  *DistributionStats          `json:"successLatency,omitempty"`
	FailureLatency  *DistributionStats          `json:"failureLatency,omitempty"`
	StageStats      map[StageKind]*StageSummary `json:"stageStats,omitempty"`
	GateResult      *GateEvaluation             `json:"gateResult,omitempty"`
	Comparison      *ComparisonReport           `json:"comparison,omitempty"`
	WarmupSamples   []SampleRecord              `json:"warmupSamples,omitempty"`
	Samples         []SampleRecord              `json:"samples,omitempty"`
	Limitations     []string                    `json:"limitations,omitempty"`
}
