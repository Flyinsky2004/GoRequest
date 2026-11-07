package diagnostics

import (
	"fmt"
	"math"
	"strconv"
)

// AttributionDisclaimer prevents misinterpreting client-side latency shifts
// between two runs as definitive backend regressions.
const AttributionDisclaimer = "Client-side diagnostic comparison reflects end-to-end observation from the local machine; local Wi-Fi/VPN/proxy/DNS jitter, connection reuse state, or environment changes must not be directly attributed to backend server performance regression."

// ConfigDiff records a configuration or snapshot discrepancy between a
// baseline report and a candidate report.
type ConfigDiff struct {
	Field     string `json:"field"`
	Baseline  string `json:"baseline"`
	Candidate string `json:"candidate"`
	Severity  string `json:"severity"` // "blocking" or "advisory"
	Note      string `json:"note,omitempty"`
}

// MetricDelta captures the change in one quantile or rate metric between two runs.
type MetricDelta struct {
	Metric         string  `json:"metric"`
	BaselineValue  float64 `json:"baselineValue"`
	CandidateValue float64 `json:"candidateValue"`
	Delta          float64 `json:"delta"`
	DeltaPct       float64 `json:"deltaPct,omitempty"`
	BaselineN      int     `json:"baselineN"`
	CandidateN     int     `json:"candidateN"`
	Comparable     bool    `json:"comparable"`
	Note           string  `json:"note,omitempty"`
}

// ComparisonReport summarizes configuration parity and statistical deltas
// between a historical baseline report and a candidate report.
type ComparisonReport struct {
	BaselineRunID           string                      `json:"baselineRunId"`
	CandidateRunID          string                      `json:"candidateRunId"`
	BaselineSnapshotDigest  string                      `json:"baselineSnapshotDigest"`
	CandidateSnapshotDigest string                      `json:"candidateSnapshotDigest"`
	SnapshotMatched         bool                        `json:"snapshotMatched"`
	EnvironmentMatched      bool                        `json:"environmentMatched"`
	StrategyMatched         bool                        `json:"strategyMatched"`
	SeedMatched             bool                        `json:"seedMatched"`
	Comparable              bool                        `json:"comparable"`
	AttributionDisclaimer   string                      `json:"attributionDisclaimer"`
	ConfigDiffs             []ConfigDiff                `json:"configDiffs,omitempty"`
	OverallDeltas           []MetricDelta               `json:"overallDeltas,omitempty"`
	StageDeltas             map[StageKind][]MetricDelta `json:"stageDeltas,omitempty"`
	Warnings                []string                    `json:"warnings,omitempty"`
}

// CompareReports compares a candidate diagnostic Report against a historical
// baseline Report, checking snapshot digest, target request, environment,
// connection reuse strategy, seed, concurrency, and proxy configuration before
// computing nearest-rank quantile deltas.
func CompareReports(baseline, candidate *Report) *ComparisonReport {
	if baseline == nil || candidate == nil {
		return nil
	}
	comp := &ComparisonReport{
		BaselineRunID:           baseline.RunID,
		CandidateRunID:          candidate.RunID,
		BaselineSnapshotDigest:  baseline.SnapshotDigest,
		CandidateSnapshotDigest: candidate.SnapshotDigest,
		SnapshotMatched:         baseline.SnapshotDigest != "" && baseline.SnapshotDigest == candidate.SnapshotDigest,
		EnvironmentMatched:      baseline.Environment == candidate.Environment,
		StrategyMatched:         baseline.Config.ConnectionStrategy == candidate.Config.ConnectionStrategy,
		SeedMatched:             baseline.Config.Seed == candidate.Config.Seed,
		Comparable:              true,
		AttributionDisclaimer:   AttributionDisclaimer,
		StageDeltas:             make(map[StageKind][]MetricDelta, len(AllStageKinds)),
		Warnings:                []string{AttributionDisclaimer},
	}

	addDiff := func(field, bVal, cVal, severity, note string) {
		if bVal == cVal {
			return
		}
		comp.ConfigDiffs = append(comp.ConfigDiffs, ConfigDiff{
			Field:     field,
			Baseline:  bVal,
			Candidate: cVal,
			Severity:  severity,
			Note:      note,
		})
		if severity == "blocking" {
			comp.Comparable = false
		}
		if note != "" {
			comp.Warnings = append(comp.Warnings, fmt.Sprintf("%s changed (%s -> %s): %s", field, bVal, cVal, note))
		} else {
			comp.Warnings = append(comp.Warnings, fmt.Sprintf("%s changed (%s -> %s)", field, bVal, cVal))
		}
	}

	addDiff("requestPath", baseline.RequestPath, candidate.RequestPath, "blocking", "comparing different saved requests invalidates latency delta attribution")
	addDiff("requestId", baseline.RequestID, candidate.RequestID, "blocking", "request identity differs")
	addDiff("method", baseline.Method, candidate.Method, "blocking", "HTTP method differs")
	addDiff("targetUrl", baseline.TargetURL, candidate.TargetURL, "blocking", "resolved target URL differs")
	addDiff("snapshotDigest", baseline.SnapshotDigest, candidate.SnapshotDigest, "blocking", "project/request/environment snapshot digest differs")
	addDiff("environment", baseline.Environment, candidate.Environment, "blocking", "environment selection differs")
	addDiff("connectionStrategy", string(baseline.Config.ConnectionStrategy), string(candidate.Config.ConnectionStrategy), "blocking", "connection reuse strategy differs (keep-alive vs fresh connections change DNS/TCP/TLS and TTFB distributions)")
	addDiff("reuseSupport", string(baseline.ReuseSupport), string(candidate.ReuseSupport), "advisory", "transport connection reuse observability changed")
	addDiff("proxyMode", baseline.ProxyMode, candidate.ProxyMode, "blocking", "proxy mode changed; network path is not equivalent")
	addDiff("concurrency", strconv.Itoa(baseline.Config.Concurrency), strconv.Itoa(candidate.Config.Concurrency), "advisory", "sampling concurrency changed")
	addDiff("iterations", strconv.Itoa(baseline.Config.Iterations), strconv.Itoa(candidate.Config.Iterations), "advisory", "configured sample count changed; nearest-rank quantile resolution may differ")
	addDiff("warmup", strconv.Itoa(baseline.Config.Warmup), strconv.Itoa(candidate.Config.Warmup), "advisory", "warmup sample count changed")
	addDiff("interval", baseline.Config.Interval.String(), candidate.Config.Interval.String(), "advisory", "inter-sample pacing interval changed")
	addDiff("seed", strconv.FormatInt(baseline.Config.Seed, 10), strconv.FormatInt(candidate.Config.Seed, 10), "advisory", "sampling seed changed")
	addDiff("coreVersion", baseline.CoreVersion, candidate.CoreVersion, "advisory", "diagnostics core version changed")

	// Failure rate delta is always reported usingMeasuredTotal as n.
	comp.OverallDeltas = append(comp.OverallDeltas, buildRateDelta(
		"failureRate",
		baseline.Summary.FailureRate,
		candidate.Summary.FailureRate,
		baseline.Summary.MeasuredTotal,
		candidate.Summary.MeasuredTotal,
		comp.Comparable,
	))

	// Overall success latency distribution deltas.
	comp.OverallDeltas = append(comp.OverallDeltas, buildDistributionDeltas(
		baseline.SuccessLatency,
		candidate.SuccessLatency,
		comp.Comparable,
	)...)

	// Per-stage independent n distribution deltas.
	for _, kind := range AllStageKinds {
		var bStats, cStats *DistributionStats
		if baseline.StageStats != nil && baseline.StageStats[kind] != nil {
			bStats = baseline.StageStats[kind].Stats
		}
		if candidate.StageStats != nil && candidate.StageStats[kind] != nil {
			cStats = candidate.StageStats[kind].Stats
		}
		if bStats == nil && cStats == nil {
			continue
		}
		comp.StageDeltas[kind] = buildDistributionDeltas(bStats, cStats, comp.Comparable)
	}

	return comp
}

func buildRateDelta(name string, bVal, cVal float64, bN, cN int, comparable bool) MetricDelta {
	delta := roundMetric(cVal - bVal)
	var pct float64
	if bVal > 0 {
		pct = roundMetric(((cVal - bVal) / bVal) * 100.0)
	}
	ok := comparable && bN > 0 && cN > 0
	note := ""
	if bN == 0 || cN == 0 {
		note = "insufficient measured samples in one or both reports"
	}
	return MetricDelta{
		Metric:         name,
		BaselineValue:  bVal,
		CandidateValue: cVal,
		Delta:          delta,
		DeltaPct:       pct,
		BaselineN:      bN,
		CandidateN:     cN,
		Comparable:     ok,
		Note:           note,
	}
}

func buildDistributionDeltas(base, cand *DistributionStats, configComparable bool) []MetricDelta {
	type metricExtractor struct {
		name string
		get  func(*DistributionStats) float64
	}
	extractors := []metricExtractor{
		{name: "meanMs", get: func(d *DistributionStats) float64 { return d.MeanMs }},
		{name: "minMs", get: func(d *DistributionStats) float64 { return d.MinMs }},
		{name: "p50Ms", get: func(d *DistributionStats) float64 { return d.P50Ms }},
		{name: "p90Ms", get: func(d *DistributionStats) float64 { return d.P90Ms }},
		{name: "p95Ms", get: func(d *DistributionStats) float64 { return d.P95Ms }},
		{name: "p99Ms", get: func(d *DistributionStats) float64 { return d.P99Ms }},
		{name: "maxMs", get: func(d *DistributionStats) float64 { return d.MaxMs }},
	}

	out := make([]MetricDelta, 0, len(extractors))
	for _, ex := range extractors {
		if base == nil || cand == nil || base.Count == 0 || cand.Count == 0 {
			bN, cN := 0, 0
			var bVal, cVal float64
			if base != nil {
				bN = base.Count
				bVal = ex.get(base)
			}
			if cand != nil {
				cN = cand.Count
				cVal = ex.get(cand)
			}
			out = append(out, MetricDelta{
				Metric:         ex.name,
				BaselineValue:  bVal,
				CandidateValue: cVal,
				BaselineN:      bN,
				CandidateN:     cN,
				Comparable:     false,
				Note:           "stage or sample metric was not observed (n=0) in one or both runs",
			})
			continue
		}
		bVal := ex.get(base)
		cVal := ex.get(cand)
		delta := roundMetric(cVal - bVal)
		var pct float64
		if math.Abs(bVal) > 1e-9 {
			pct = roundMetric(((cVal - bVal) / bVal) * 100.0)
		}
		note := ""
		if !configComparable {
			note = "configuration or snapshot mismatch; treat delta as non-attributable"
		}
		out = append(out, MetricDelta{
			Metric:         ex.name,
			BaselineValue:  bVal,
			CandidateValue: cVal,
			Delta:          delta,
			DeltaPct:       pct,
			BaselineN:      base.Count,
			CandidateN:     cand.Count,
			Comparable:     configComparable,
			Note:           note,
		})
	}
	return out
}
