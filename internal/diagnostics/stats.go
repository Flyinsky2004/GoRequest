package diagnostics

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// StatisticalMethodNearestRank is the canonical quantile calculation identifier.
const StatisticalMethodNearestRank = "nearest-rank"

// DistributionStats holds nearest-rank quantiles and summary metrics for a set
// of observed, complete duration samples (in milliseconds).
type DistributionStats struct {
	Method         string  `json:"method"`
	Count          int     `json:"n"`
	MeanMs         float64 `json:"meanMs"`
	MinMs          float64 `json:"minMs"`
	MaxMs          float64 `json:"maxMs"`
	P50Ms          float64 `json:"p50Ms"`
	P90Ms          float64 `json:"p90Ms"`
	P95Ms          float64 `json:"p95Ms"`
	P99Ms          float64 `json:"p99Ms"`
	ResolutionNote string  `json:"resolutionNote,omitempty"`
}

// StageSummary aggregates one transport stage across measured samples with its
// own independent sample count n. Unobserved, reused, not-applicable, and
// incomplete stages are counted by status and never coerced to 0ms.
type StageSummary struct {
	Stage              StageKind          `json:"stage"`
	ObservedCount      int                `json:"n"`
	SkippedReusedCount int                `json:"skippedReusedCount"`
	UnobservedCount    int                `json:"unobservedCount"`
	IncompleteCount    int                `json:"incompleteCount"`
	NotApplicableCount int                `json:"notApplicableCount"`
	Stats              *DistributionStats `json:"stats,omitempty"`
}

// SampleSummary summarizes outcomes across all warmup and measured samples.
type SampleSummary struct {
	ConfiguredIterations int     `json:"configuredIterations"`
	ConfiguredWarmup     int     `json:"configuredWarmup"`
	WarmupCompleted      int     `json:"warmupCompleted"`
	MeasuredTotal        int     `json:"measuredTotal"`
	SuccessCount         int     `json:"successCount"`
	FailureCount         int     `json:"failureCount"`
	ErrorCount           int     `json:"errorCount"`
	TimeoutCount         int     `json:"timeoutCount"`
	CanceledCount        int     `json:"canceledCount"`
	TruncatedCount       int     `json:"truncatedCount"`
	ReusedConnCount      int     `json:"reusedConnCount"`
	FreshConnCount       int     `json:"freshConnCount"`
	UnknownReuseCount    int     `json:"unknownReuseCount"`
	SuccessRate          float64 `json:"successRate"`
	FailureRate          float64 `json:"failureRate"`
	StatisticalMethod    string  `json:"statisticalMethod"`
}

// GateStatus represents the outcome of evaluating a ThresholdGate.
type GateStatus string

const (
	GatePassed                          GateStatus = "passed"
	GateFailed                          GateStatus = "failed"
	GateIndeterminateInsufficientSample GateStatus = "insufficient_samples"
)

// ThresholdGate defines an optional quality gate over measured samples.
// A gate requires simultaneously declaring the target quantile, minimum valid
// sample count, and maximum allowed failure rate. Insufficient valid samples
// never pass the gate.
type ThresholdGate struct {
	Quantile        string  `json:"quantile"`
	MaxLatencyMs    float64 `json:"maxLatencyMs"`
	MinValidSamples int     `json:"minValidSamples"`
	MaxFailureRate  float64 `json:"maxFailureRate"`
}

// ValidateAndNormalize checks that a ThresholdGate is well-formed.
func (g ThresholdGate) ValidateAndNormalize() (ThresholdGate, error) {
	out := g
	q := strings.ToUpper(strings.TrimSpace(out.Quantile))
	switch q {
	case "", "P95":
		out.Quantile = "P95"
	case "P50", "P90", "P99", "MEAN", "MAX":
		out.Quantile = q
	default:
		return out, fmt.Errorf("unsupported gate quantile %q (expected P50, P90, P95, P99, MEAN, or MAX)", g.Quantile)
	}
	if out.MaxLatencyMs <= 0 || math.IsNaN(out.MaxLatencyMs) || math.IsInf(out.MaxLatencyMs, 0) {
		return out, fmt.Errorf("gate maxLatencyMs must be > 0 (got %v)", g.MaxLatencyMs)
	}
	if out.MinValidSamples <= 0 {
		return out, fmt.Errorf("gate minValidSamples must be >= 1 (got %d)", g.MinValidSamples)
	}
	if out.MaxFailureRate < 0 || out.MaxFailureRate > 1 || math.IsNaN(out.MaxFailureRate) {
		return out, fmt.Errorf("gate maxFailureRate must be between 0.0 and 1.0 (got %v)", g.MaxFailureRate)
	}
	return out, nil
}

// GateEvaluation records the detailed verdict of a ThresholdGate.
type GateEvaluation struct {
	Status             GateStatus `json:"status"`
	Passed             bool       `json:"passed"`
	Quantile           string     `json:"quantile"`
	ObservedQuantileMs float64    `json:"observedQuantileMs,omitempty"`
	MaxLatencyMs       float64    `json:"maxLatencyMs"`
	ValidSamples       int        `json:"validSamples"`
	MinValidSamples    int        `json:"minValidSamples"`
	FailureRate        float64    `json:"failureRate"`
	MaxFailureRate     float64    `json:"maxFailureRate"`
	Reasons            []string   `json:"reasons,omitempty"`
}

// NearestRankQuantile computes Pq = x[ceil(q * n)] (1-based index) on a slice
// of already sorted ascending float64 values.
func NearestRankQuantile(sorted []float64, q float64) float64 {
	n := len(sorted)
	if n == 0 {
		return 0
	}
	if q <= 0 {
		return sorted[0]
	}
	if q >= 1 {
		return sorted[n-1]
	}
	rank := int(math.Ceil(q * float64(n)))
	if rank < 1 {
		rank = 1
	}
	if rank > n {
		rank = n
	}
	return sorted[rank-1]
}

// ComputeDistribution calculates nearest-rank quantiles (P50, P90, P95, P99),
// mean, min, max, and sample size resolution warnings for a slice of observed
// durations in milliseconds. Returns nil when values has no valid samples so
// callers never mistake an empty set for 0ms.
func ComputeDistribution(values []float64) *DistributionStats {
	clean := make([]float64, 0, len(values))
	for _, v := range values {
		if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
			continue
		}
		clean = append(clean, v)
	}
	n := len(clean)
	if n == 0 {
		return nil
	}
	sort.Float64s(clean)
	var sum float64
	for _, v := range clean {
		sum += v
	}
	mean := sum / float64(n)

	var note string
	switch {
	case n < 20:
		note = fmt.Sprintf("low sample resolution (n=%d): nearest-rank P95/P99 equal max; n>=20 needed for distinct P95 and n>=100 for P99", n)
	case n < 100:
		note = fmt.Sprintf("moderate sample resolution (n=%d): nearest-rank P99 equals max; n>=100 needed for distinct P99", n)
	}

	return &DistributionStats{
		Method:         StatisticalMethodNearestRank,
		Count:          n,
		MeanMs:         roundMetric(mean),
		MinMs:          roundMetric(clean[0]),
		MaxMs:          roundMetric(clean[n-1]),
		P50Ms:          roundMetric(NearestRankQuantile(clean, 0.50)),
		P90Ms:          roundMetric(NearestRankQuantile(clean, 0.90)),
		P95Ms:          roundMetric(NearestRankQuantile(clean, 0.95)),
		P99Ms:          roundMetric(NearestRankQuantile(clean, 0.99)),
		ResolutionNote: note,
	}
}

func roundMetric(v float64) float64 {
	return math.Round(v*1000) / 1000
}

// AggregateSamples computes the SampleSummary, success latency distribution,
// separate failure latency distribution, and per-stage distributions with
// independent sample counts n from warmup and measured samples.
func AggregateSamples(
	cfg SamplingConfig,
	warmup []SampleRecord,
	measured []SampleRecord,
) (SampleSummary, *DistributionStats, *DistributionStats, map[StageKind]*StageSummary) {
	summary := SampleSummary{
		ConfiguredIterations: cfg.Iterations,
		ConfiguredWarmup:     cfg.Warmup,
		WarmupCompleted:      len(warmup),
		MeasuredTotal:        len(measured),
		StatisticalMethod:    StatisticalMethodNearestRank,
	}

	var successDurations []float64
	var failureDurations []float64

	stageBuckets := make(map[StageKind]*StageSummary, len(AllStageKinds))
	stageValues := make(map[StageKind][]float64, len(AllStageKinds))
	for _, k := range AllStageKinds {
		stageBuckets[k] = &StageSummary{Stage: k}
	}

	for _, s := range measured {
		outcome := s.Outcome
		if outcome == "" {
			if s.Truncated {
				outcome = OutcomeTruncated
			} else if s.Error != "" {
				outcome = OutcomeError
			} else {
				outcome = OutcomeSuccess
			}
		}

		if s.ReusedKnown {
			if s.ReusedConn {
				summary.ReusedConnCount++
			} else {
				summary.FreshConnCount++
			}
		} else {
			summary.UnknownReuseCount++
		}

		switch outcome {
		case OutcomeSuccess:
			if s.Truncated {
				summary.TruncatedCount++
				summary.FailureCount++
				failureDurations = append(failureDurations, s.DurationMs)
			} else {
				summary.SuccessCount++
				successDurations = append(successDurations, s.DurationMs)
			}
		case OutcomeError:
			summary.ErrorCount++
			summary.FailureCount++
			failureDurations = append(failureDurations, s.DurationMs)
		case OutcomeTimeout:
			summary.TimeoutCount++
			summary.FailureCount++
			failureDurations = append(failureDurations, s.DurationMs)
		case OutcomeCanceled:
			summary.CanceledCount++
			summary.FailureCount++
			failureDurations = append(failureDurations, s.DurationMs)
		case OutcomeTruncated:
			summary.TruncatedCount++
			summary.FailureCount++
			failureDurations = append(failureDurations, s.DurationMs)
		default:
			summary.ErrorCount++
			summary.FailureCount++
			failureDurations = append(failureDurations, s.DurationMs)
		}

		isCompleteSample := outcome == OutcomeSuccess && !s.Truncated
		for _, kind := range AllStageKinds {
			bucket := stageBuckets[kind]
			meas, ok := selectSampleStageMeasurement(s, kind)
			if !ok {
				bucket.UnobservedCount++
				continue
			}
			switch meas.Status {
			case ObsObserved:
				if isCompleteSample {
					bucket.ObservedCount++
					stageValues[kind] = append(stageValues[kind], meas.DurationMs)
				} else {
					bucket.IncompleteCount++
				}
			case ObsSkippedReused:
				bucket.SkippedReusedCount++
			case ObsNotApplicable:
				bucket.NotApplicableCount++
			case ObsIncomplete:
				bucket.IncompleteCount++
			default:
				bucket.UnobservedCount++
			}
		}
	}

	if summary.MeasuredTotal > 0 {
		summary.SuccessRate = roundMetric(float64(summary.SuccessCount) / float64(summary.MeasuredTotal))
		summary.FailureRate = roundMetric(float64(summary.FailureCount) / float64(summary.MeasuredTotal))
	}

	successStats := ComputeDistribution(successDurations)
	failureStats := ComputeDistribution(failureDurations)

	for _, kind := range AllStageKinds {
		vals := stageValues[kind]
		if len(vals) > 0 {
			stageBuckets[kind].Stats = ComputeDistribution(vals)
		}
	}

	return summary, successStats, failureStats, stageBuckets
}

// selectSampleStageMeasurement resolves the stage measurement for a sample.
// For single-hop requests it returns Hops[0].Stages[kind]. For multi-hop
// redirected requests, connection-setup stages (DNS, TCP, TLS, ConnWait) use
// the hop where the stage occurred if unique, while request/response stages
// (Write, ServerWait, TTFB, Download, HopTotal) use the final hop.
func selectSampleStageMeasurement(s SampleRecord, kind StageKind) (StageMeasurement, bool) {
	if len(s.Hops) == 0 {
		return StageMeasurement{}, false
	}
	if len(s.Hops) == 1 {
		m, ok := s.Hops[0].Stages[kind]
		return m, ok
	}
	finalHop := s.Hops[len(s.Hops)-1]
	switch kind {
	case StageDNS, StageTCPConnect, StageTLSHandshake, StageConnWait:
		var observed []StageMeasurement
		hasIncomplete := false
		allReused := true
		for _, h := range s.Hops {
			m, ok := h.Stages[kind]
			if !ok {
				allReused = false
				continue
			}
			if m.Status != ObsSkippedReused {
				allReused = false
			}
			if m.Status == ObsIncomplete {
				hasIncomplete = true
			}
			if m.Status == ObsObserved {
				observed = append(observed, m)
			}
		}
		if hasIncomplete {
			return StageMeasurement{Stage: kind, Status: ObsIncomplete}, true
		}
		if len(observed) == 1 {
			return observed[0], true
		}
		if len(observed) > 1 {
			// Multiple hops independently executed this setup stage; do not sum
			// across redirect hops into a single hop metric—take the initial hop
			// or mark per-hop inspection required.
			return observed[0], true
		}
		if allReused {
			return StageMeasurement{Stage: kind, Status: ObsSkippedReused}, true
		}
		m, ok := finalHop.Stages[kind]
		return m, ok
	default:
		m, ok := finalHop.Stages[kind]
		return m, ok
	}
}

// EvaluateGate evaluates a ThresholdGate against the aggregated SampleSummary
// and success latency distribution. Insufficient valid samples always result in
// GateIndeterminateInsufficientSample with Passed=false.
func EvaluateGate(gate *ThresholdGate, summary SampleSummary, successStats *DistributionStats) *GateEvaluation {
	if gate == nil {
		return nil
	}
	norm, err := gate.ValidateAndNormalize()
	if err != nil {
		return &GateEvaluation{
			Status:  GateFailed,
			Passed:  false,
			Reasons: []string{err.Error()},
		}
	}

	eval := &GateEvaluation{
		Quantile:        norm.Quantile,
		MaxLatencyMs:    norm.MaxLatencyMs,
		ValidSamples:    summary.SuccessCount,
		MinValidSamples: norm.MinValidSamples,
		FailureRate:     summary.FailureRate,
		MaxFailureRate:  norm.MaxFailureRate,
	}

	if summary.MeasuredTotal == 0 || summary.SuccessCount < norm.MinValidSamples || successStats == nil || successStats.Count < norm.MinValidSamples {
		eval.Status = GateIndeterminateInsufficientSample
		eval.Passed = false
		eval.Reasons = append(eval.Reasons, fmt.Sprintf(
			"insufficient valid complete samples: got n=%d, require at least %d",
			summary.SuccessCount, norm.MinValidSamples,
		))
		if summary.MeasuredTotal > 0 && summary.FailureRate > norm.MaxFailureRate {
			eval.Reasons = append(eval.Reasons, fmt.Sprintf(
				"failure rate %.4f exceeds maximum allowed %.4f",
				summary.FailureRate, norm.MaxFailureRate,
			))
		}
		return eval
	}

	var observed float64
	switch norm.Quantile {
	case "P50":
		observed = successStats.P50Ms
	case "P90":
		observed = successStats.P90Ms
	case "P95":
		observed = successStats.P95Ms
	case "P99":
		observed = successStats.P99Ms
	case "MEAN":
		observed = successStats.MeanMs
	case "MAX":
		observed = successStats.MaxMs
	default:
		observed = successStats.P95Ms
	}
	eval.ObservedQuantileMs = observed

	if summary.FailureRate > norm.MaxFailureRate {
		eval.Reasons = append(eval.Reasons, fmt.Sprintf(
			"failure rate %.4f exceeds maximum allowed %.4f",
			summary.FailureRate, norm.MaxFailureRate,
		))
	}
	if observed > norm.MaxLatencyMs {
		eval.Reasons = append(eval.Reasons, fmt.Sprintf(
			"%s latency %.3f ms exceeds threshold %.3f ms",
			norm.Quantile, observed, norm.MaxLatencyMs,
		))
	}

	if len(eval.Reasons) > 0 {
		eval.Status = GateFailed
		eval.Passed = false
		return eval
	}

	eval.Status = GatePassed
	eval.Passed = true
	return eval
}
