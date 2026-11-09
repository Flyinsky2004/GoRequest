package diagnostics

import (
	"context"
	"encoding/csv"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gorequest/internal/core"
)

func TestNearestRankQuantiles_KnownSample5(t *testing.T) {
	// Spec §5.2 canonical vector: [100, 200, 300, 400, 500] -> P50=300, P95=500, P99=500.
	stats := ComputeDistribution([]float64{400, 100, 500, 200, 300})
	if stats == nil {
		t.Fatal("expected non-nil DistributionStats")
	}
	if stats.Method != StatisticalMethodNearestRank {
		t.Fatalf("Method = %q, want %q", stats.Method, StatisticalMethodNearestRank)
	}
	if stats.Count != 5 {
		t.Fatalf("Count = %d, want 5", stats.Count)
	}
	if stats.MeanMs != 300 || stats.MinMs != 100 || stats.MaxMs != 500 {
		t.Fatalf("mean/min/max = %v/%v/%v, want 300/100/500", stats.MeanMs, stats.MinMs, stats.MaxMs)
	}
	if stats.P50Ms != 300 {
		t.Fatalf("P50Ms = %v, want 300", stats.P50Ms)
	}
	if stats.P90Ms != 500 {
		t.Fatalf("P90Ms = %v, want 500", stats.P90Ms)
	}
	if stats.P95Ms != 500 {
		t.Fatalf("P95Ms = %v, want 500", stats.P95Ms)
	}
	if stats.P99Ms != 500 {
		t.Fatalf("P99Ms = %v, want 500", stats.P99Ms)
	}
	if !strings.Contains(stats.ResolutionNote, "n=5") {
		t.Fatalf("expected low-sample resolution note, got %q", stats.ResolutionNote)
	}
}

func TestSamplingConfig_BoundsValidation(t *testing.T) {
	cases := []struct {
		name string
		cfg  SamplingConfig
	}{
		{
			name: "negative iterations",
			cfg:  SamplingConfig{Iterations: -1},
		},
		{
			name: "excessive iterations",
			cfg:  SamplingConfig{Iterations: MaxIterations + 1},
		},
		{
			name: "excessive concurrency",
			cfg:  SamplingConfig{Iterations: 10, Concurrency: MaxConcurrency + 1},
		},
		{
			name: "negative warmup",
			cfg:  SamplingConfig{Iterations: 10, Warmup: -2},
		},
		{
			name: "excessive interval",
			cfg:  SamplingConfig{Iterations: 10, Interval: MaxInterval + time.Second},
		},
		{
			name: "excessive deadline",
			cfg:  SamplingConfig{Iterations: 10, Deadline: MaxDeadline + time.Second},
		},
		{
			name: "unknown connection strategy",
			cfg:  SamplingConfig{Iterations: 5, ConnectionStrategy: "unlimited_pool"},
		},
		{
			name: "invalid gate minValidSamples",
			cfg: SamplingConfig{
				Iterations: 5,
				Gate: &ThresholdGate{
					Quantile:        "P95",
					MaxLatencyMs:    250,
					MinValidSamples: 0,
					MaxFailureRate:  0.1,
				},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := tc.cfg.ValidateAndNormalize(); err == nil {
				t.Fatalf("expected validation error for %s, got nil", tc.name)
			}
		})
	}
}

func TestAggregateSamples_IndependentStageNAndReuse(t *testing.T) {
	cfg := SamplingConfig{
		Iterations:         5,
		Concurrency:        1,
		ConnectionStrategy: StrategyKeepAlive,
	}

	makeSample := func(idx int, reused bool, totalMs float64, ttfbMs float64, dlMs float64) SampleRecord {
		stages := map[StageKind]StageMeasurement{
			StageConnWait:     {Stage: StageConnWait, Status: ObsObserved, DurationMs: 2},
			StageRequestWrite: {Stage: StageRequestWrite, Status: ObsObserved, DurationMs: 1},
			StageServerWait:   {Stage: StageServerWait, Status: ObsObserved, DurationMs: ttfbMs - 5},
			StageTTFB:         {Stage: StageTTFB, Status: ObsObserved, DurationMs: ttfbMs},
			StageDownload:     {Stage: StageDownload, Status: ObsObserved, DurationMs: dlMs},
			StageHopTotal:     {Stage: StageHopTotal, Status: ObsObserved, DurationMs: totalMs},
		}
		if !reused {
			stages[StageDNS] = StageMeasurement{Stage: StageDNS, Status: ObsObserved, DurationMs: 15}
			stages[StageTCPConnect] = StageMeasurement{Stage: StageTCPConnect, Status: ObsObserved, DurationMs: 22}
			stages[StageTLSHandshake] = StageMeasurement{Stage: StageTLSHandshake, Status: ObsObserved, DurationMs: 35}
		} else {
			stages[StageDNS] = StageMeasurement{Stage: StageDNS, Status: ObsSkippedReused}
			stages[StageTCPConnect] = StageMeasurement{Stage: StageTCPConnect, Status: ObsSkippedReused}
			stages[StageTLSHandshake] = StageMeasurement{Stage: StageTLSHandshake, Status: ObsSkippedReused}
		}
		return SampleRecord{
			Index:       idx,
			Outcome:     OutcomeSuccess,
			StatusCode:  http.StatusOK,
			DurationMs:  totalMs,
			ReusedConn:  reused,
			ReusedKnown: true,
			Hops: []HopTimeline{
				{
					Hop:         0,
					Method:      http.MethodGet,
					URL:         "https://example.test/items",
					StatusCode:  http.StatusOK,
					ReusedConn:  reused,
					ReusedKnown: true,
					Stages:      stages,
				},
			},
		}
	}

	samples := []SampleRecord{
		makeSample(1, false, 100, 80, 20),
		makeSample(2, true, 200, 170, 30),
		makeSample(3, true, 300, 260, 40),
		makeSample(4, true, 400, 350, 50),
		makeSample(5, true, 500, 440, 60),
	}

	summary, successStats, failureStats, stageStats := AggregateSamples(cfg, nil, samples)
	if failureStats != nil {
		t.Fatalf("expected nil failureStats, got %+v", failureStats)
	}
	if summary.SuccessCount != 5 || summary.ReusedConnCount != 4 || summary.FreshConnCount != 1 {
		t.Fatalf("unexpected summary counts: %+v", summary)
	}
	if successStats == nil || successStats.P50Ms != 300 || successStats.P95Ms != 500 {
		t.Fatalf("unexpected overall distribution: %+v", successStats)
	}

	// DNS, TCP, and TLS only occurred on sample 1: their independent n must be 1,
	// and reused samples must be counted in SkippedReusedCount (never as 0ms).
	for _, setupStage := range []StageKind{StageDNS, StageTCPConnect, StageTLSHandshake} {
		st := stageStats[setupStage]
		if st == nil {
			t.Fatalf("missing stage summary for %s", setupStage)
		}
		if st.ObservedCount != 1 {
			t.Fatalf("stage %s ObservedCount (n) = %d, want 1", setupStage, st.ObservedCount)
		}
		if st.SkippedReusedCount != 4 {
			t.Fatalf("stage %s SkippedReusedCount = %d, want 4", setupStage, st.SkippedReusedCount)
		}
		if st.Stats == nil || st.Stats.Count != 1 || st.Stats.MinMs <= 0 {
			t.Fatalf("stage %s stats polluted by zero or missing: %+v", setupStage, st.Stats)
		}
	}

	// TTFB and Download occurred on all 5 samples: their independent n must be 5.
	if stageStats[StageTTFB].ObservedCount != 5 || stageStats[StageDownload].ObservedCount != 5 {
		t.Fatalf("expected n=5 for TTFB and Download, got TTFB=%d Download=%d",
			stageStats[StageTTFB].ObservedCount, stageStats[StageDownload].ObservedCount)
	}
}

func TestEngine_WarmupFailuresTruncatedAndGateIndeterminate(t *testing.T) {
	proj := makeTestProject("http://127.0.0.1:8080/api/orders")

	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time {
		now = now.Add(10 * time.Millisecond)
		return now
	}
	var slept []time.Duration
	sleepFn := func(ctx context.Context, d time.Duration) error {
		slept = append(slept, d)
		return ctx.Err()
	}

	var callNum atomic.Int32
	execFn := ExecutorFunc(func(ctx context.Context, plan *core.Plan, opts core.ExecuteOptions) (*core.ExecResult, error) {
		n := int(callNum.Add(1))
		switch n {
		case 1:
			// Warmup sample (slow, should be excluded from measured stats).
			return &core.ExecResult{
				Status:   http.StatusOK,
				Duration: 999 * time.Millisecond,
				Trace: &core.NetworkTrace{
					TTFBDuration:     900 * time.Millisecond,
					TTFBObserved:     true,
					DownloadDuration: 99 * time.Millisecond,
					DNSObserved:      false,
					ConnectObserved:  true,
					ConnectDuration:  10 * time.Millisecond,
				},
			}, nil
		case 2:
			// Measured #1: success (120ms).
			return &core.ExecResult{
				Status:   http.StatusOK,
				Duration: 120 * time.Millisecond,
				Trace: &core.NetworkTrace{
					TTFBDuration:     100 * time.Millisecond,
					TTFBObserved:     true,
					DownloadDuration: 20 * time.Millisecond,
					ConnectObserved:  true,
					ConnectDuration:  12 * time.Millisecond,
				},
			}, nil
		case 3:
			// Measured #2: HTTP 500 error (45ms).
			return &core.ExecResult{
				Status:   http.StatusInternalServerError,
				Duration: 45 * time.Millisecond,
				Trace: &core.NetworkTrace{
					TTFBDuration: 40 * time.Millisecond,
					TTFBObserved: true,
				},
			}, nil
		case 4:
			// Measured #3: truncated response body (75ms).
			return &core.ExecResult{
				Status:    http.StatusOK,
				Truncated: true,
				Duration:  75 * time.Millisecond,
				Trace: &core.NetworkTrace{
					TTFBDuration:     30 * time.Millisecond,
					TTFBObserved:     true,
					DownloadDuration: 45 * time.Millisecond,
				},
			}, nil
		default:
			// Measured #4: timeout error.
			return nil, errors.New("request timed out waiting for response headers")
		}
	})

	eng := New(proj, Options{
		RequestRef: "requests/orders.yaml",
		Config: SamplingConfig{
			Warmup:             1,
			Iterations:         4,
			Concurrency:        1,
			Interval:           25 * time.Millisecond,
			Deadline:           10 * time.Second,
			Seed:               42,
			ConnectionStrategy: StrategyKeepAlive,
			Gate: &ThresholdGate{
				Quantile:        "P95",
				MaxLatencyMs:    200,
				MinValidSamples: 3, // Only 1 valid sample will succeed -> insufficient_samples!
				MaxFailureRate:  0.10,
			},
		},
		Executor: execFn,
		Clock:    clock,
		Sleep:    sleepFn,
	})

	rep, err := eng.Run(context.Background())
	if err != nil {
		t.Fatalf("Run returned unexpected error: %v", err)
	}
	if rep.Summary.WarmupCompleted != 1 {
		t.Fatalf("WarmupCompleted = %d, want 1", rep.Summary.WarmupCompleted)
	}
	if rep.Summary.MeasuredTotal != 4 {
		t.Fatalf("MeasuredTotal = %d, want 4", rep.Summary.MeasuredTotal)
	}
	if rep.Summary.SuccessCount != 1 || rep.Summary.FailureCount != 3 {
		t.Fatalf("Success/Failure = %d/%d, want 1/3", rep.Summary.SuccessCount, rep.Summary.FailureCount)
	}
	if rep.Summary.ErrorCount != 1 || rep.Summary.TruncatedCount != 1 || rep.Summary.TimeoutCount != 1 {
		t.Fatalf("unexpected failure breakdown: %+v", rep.Summary)
	}
	if rep.SuccessLatency == nil || rep.SuccessLatency.Count != 1 || rep.SuccessLatency.P50Ms != 120 {
		t.Fatalf("warmup or failed samples leaked into SuccessLatency: %+v", rep.SuccessLatency)
	}
	if rep.FailureLatency == nil || rep.FailureLatency.Count != 3 {
		t.Fatalf("expected 3 failure latency samples, got %+v", rep.FailureLatency)
	}
	// Truncated sample's Download stage must be marked incomplete, not counted in complete stage n.
	if dl := rep.StageStats[StageDownload]; dl == nil || dl.ObservedCount != 1 || dl.IncompleteCount == 0 {
		t.Fatalf("expected StageDownload ObservedCount=1 and IncompleteCount>0, got %+v", dl)
	}
	// Gate must fail with insufficient_samples (never fake-passing on n=1 < MinValidSamples=3).
	if rep.GateResult == nil {
		t.Fatal("expected GateResult")
	}
	if rep.GateResult.Passed || rep.GateResult.Status != GateIndeterminateInsufficientSample {
		t.Fatalf("expected GateIndeterminateInsufficientSample with Passed=false, got %+v", rep.GateResult)
	}
	if rep.Status != StatusGateFailed {
		t.Fatalf("Report.Status = %q, want %q", rep.Status, StatusGateFailed)
	}
	if len(slept) != 3 {
		t.Fatalf("expected 3 inter-sample sleep intervals across 4 measured samples, got %d", len(slept))
	}
}

func TestEngine_PolicyBlockedAndMissingSecretPreflight(t *testing.T) {
	// 1. Production environment + mutating method requires confirmation -> blocked
	// before any request is sent. The default policy allows safe reads (GET), so
	// the fixture must use a mutating method to exercise the guard.
	proj := makeTestProject("https://api.prod.example.com/v1/charge")
	proj.Requests["requests/orders.yaml"].Request.HTTP.Method = http.MethodPost
	proj.Environments["prod"] = &core.Environment{
		SchemaVersion: 1,
		ID:            "prod",
		Variables:     map[string]string{},
	}

	var mutatingCalls atomic.Int32
	mutatingExec := ExecutorFunc(func(ctx context.Context, plan *core.Plan, opts core.ExecuteOptions) (*core.ExecResult, error) {
		mutatingCalls.Add(1)
		return &core.ExecResult{Status: http.StatusOK}, nil
	})
	eng := New(proj, Options{
		RequestRef:  "requests/orders.yaml",
		Environment: "prod",
		Config:      SamplingConfig{Iterations: 3, Concurrency: 1},
		Executor:    mutatingExec,
	})
	rep, err := eng.Run(context.Background())
	if err == nil {
		t.Fatal("expected target policy error for unconfirmed prod mutation")
	}
	if rep == nil || rep.Status != StatusPolicyBlocked {
		t.Fatalf("expected StatusPolicyBlocked report, got %+v", rep)
	}
	if mutatingCalls.Load() != 0 {
		t.Fatalf("expected 0 executor calls when policy blocks, got %d", mutatingCalls.Load())
	}

	// 2. Safe read (GET) to the same production environment is allowed under the
	// default confirm policy; the executor actually runs.
	projRead := makeTestProject("https://api.prod.example.com/v1/orders")
	projRead.Environments["prod"] = &core.Environment{
		SchemaVersion: 1,
		ID:            "prod",
		Variables:     map[string]string{},
	}
	var readCalls atomic.Int32
	readExec := ExecutorFunc(func(ctx context.Context, plan *core.Plan, opts core.ExecuteOptions) (*core.ExecResult, error) {
		readCalls.Add(1)
		return &core.ExecResult{Status: http.StatusOK}, nil
	})
	engRead := New(projRead, Options{
		RequestRef:  "requests/orders.yaml",
		Environment: "prod",
		Config:      SamplingConfig{Iterations: 3, Concurrency: 1},
		Executor:    readExec,
	})
	repRead, errRead := engRead.Run(context.Background())
	if errRead != nil {
		t.Fatalf("safe GET to production must be allowed under default confirm policy: %v", errRead)
	}
	if repRead == nil || repRead.Status != StatusCompleted {
		t.Fatalf("expected completed report for safe GET, got %+v", repRead)
	}
	if got := readCalls.Load(); got != 3 {
		t.Fatalf("expected 3 executor calls for the allowed GET, got %d", got)
	}

	// 3. A deny policy blocks even safe reads with zero calls.
	projDeny := makeTestProject("https://api.prod.example.com/v1/orders")
	projDeny.Environments["prod"] = &core.Environment{
		SchemaVersion: 1,
		ID:            "prod",
		Variables:     map[string]string{},
	}
	var denyCalls atomic.Int32
	denyExec := ExecutorFunc(func(ctx context.Context, plan *core.Plan, opts core.ExecuteOptions) (*core.ExecResult, error) {
		denyCalls.Add(1)
		return &core.ExecResult{Status: http.StatusOK}, nil
	})
	engDeny := New(projDeny, Options{
		RequestRef:  "requests/orders.yaml",
		Environment: "prod",
		Config:      SamplingConfig{Iterations: 3, Concurrency: 1},
		Executor:    denyExec,
		ExecuteOptions: core.ExecuteOptions{
			HasTargetPolicy: true,
			TargetPolicy: core.MergedTargetPolicy{
				ProductionAction: core.ActionDeny,
				ProductionEnvs:   map[string]bool{"prod": true},
			},
		},
	})
	repDeny, errDeny := engDeny.Run(context.Background())
	if errDeny == nil {
		t.Fatal("deny policy must block even a safe GET")
	}
	if repDeny == nil || repDeny.Status != StatusPolicyBlocked {
		t.Fatalf("expected StatusPolicyBlocked for deny policy, got %+v", repDeny)
	}
	if denyCalls.Load() != 0 {
		t.Fatalf("expected 0 executor calls under deny policy, got %d", denyCalls.Load())
	}

	// 4. Missing secret reference -> fails in Prepare with StatusConfigError and 0 calls.
	projSecret := makeTestProject("http://127.0.0.1:8080/items")
	projSecret.Requests["requests/orders.yaml"].Request.HTTP.Headers = []core.KV{
		{Name: "X-Secret", Value: "{{secret.missing_key}}"},
	}
	var secretCalls atomic.Int32
	secretExec := ExecutorFunc(func(ctx context.Context, plan *core.Plan, opts core.ExecuteOptions) (*core.ExecResult, error) {
		secretCalls.Add(1)
		return &core.ExecResult{Status: http.StatusOK}, nil
	})
	engSecret := New(projSecret, Options{
		RequestRef: "requests/orders.yaml",
		Config:     SamplingConfig{Iterations: 3, Concurrency: 1},
		Secrets:    core.MapSecrets{},
		Executor:   secretExec,
	})
	repSecret, errSecret := engSecret.Run(context.Background())
	if errSecret == nil {
		t.Fatal("expected error for missing secret reference")
	}
	if repSecret == nil || repSecret.Status != StatusConfigError {
		t.Fatalf("expected StatusConfigError report, got %+v", repSecret)
	}
	if secretCalls.Load() != 0 {
		t.Fatalf("expected 0 executor calls on missing secret, got %d", secretCalls.Load())
	}
}

func TestEngine_LocalHTTPTest_KeepAliveVsFreshAndRedirects(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/ok", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	mux.HandleFunc("/redirect", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/ok", http.StatusFound)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// 1. Keep-alive connection reuse across 4 samples.
	projKeepAlive := makeTestProject(srv.URL + "/ok")
	engKA := New(projKeepAlive, Options{
		RequestRef: "requests/orders.yaml",
		Config: SamplingConfig{
			Iterations:         4,
			Concurrency:        1,
			ConnectionStrategy: StrategyKeepAlive,
		},
	})
	repKA, err := engKA.Run(context.Background())
	if err != nil {
		t.Fatalf("keep-alive Run failed: %v", err)
	}
	if repKA.ReuseSupport != ReuseSupported {
		t.Fatalf("ReuseSupport = %q, want %q", repKA.ReuseSupport, ReuseSupported)
	}
	if repKA.Summary.FreshConnCount != 1 || repKA.Summary.ReusedConnCount != 3 {
		t.Fatalf("expected 1 fresh and 3 reused connections, got fresh=%d reused=%d",
			repKA.Summary.FreshConnCount, repKA.Summary.ReusedConnCount)
	}
	if tcp := repKA.StageStats[StageTCPConnect]; tcp == nil || tcp.ObservedCount != 1 || tcp.SkippedReusedCount != 3 {
		t.Fatalf("expected TCP stage n=1 and skippedReused=3 under keep-alive, got %+v", tcp)
	}

	// 2. Fresh connection strategy across 3 samples.
	engFresh := New(projKeepAlive, Options{
		RequestRef: "requests/orders.yaml",
		Config: SamplingConfig{
			Iterations:         3,
			Concurrency:        1,
			ConnectionStrategy: StrategyFresh,
		},
	})
	repFresh, err := engFresh.Run(context.Background())
	if err != nil {
		t.Fatalf("fresh Run failed: %v", err)
	}
	if repFresh.Summary.FreshConnCount != 3 || repFresh.Summary.ReusedConnCount != 0 {
		t.Fatalf("expected 3 fresh connections under StrategyFresh, got fresh=%d reused=%d",
			repFresh.Summary.FreshConnCount, repFresh.Summary.ReusedConnCount)
	}
	if tcp := repFresh.StageStats[StageTCPConnect]; tcp == nil || tcp.ObservedCount != 3 {
		t.Fatalf("expected TCP stage n=3 under StrategyFresh, got %+v", tcp)
	}

	// 3. Redirect chain timeline (302 -> 200).
	projRedir := makeTestProject(srv.URL + "/redirect")
	engRedir := New(projRedir, Options{
		RequestRef: "requests/orders.yaml",
		Config: SamplingConfig{
			Iterations:         1,
			Concurrency:        1,
			ConnectionStrategy: StrategyKeepAlive,
		},
	})
	repRedir, err := engRedir.Run(context.Background())
	if err != nil {
		t.Fatalf("redirect Run failed: %v", err)
	}
	if len(repRedir.Samples) != 1 || repRedir.Samples[0].RedirectCount != 1 {
		t.Fatalf("expected 1 sample with RedirectCount=1, got %+v", repRedir.Samples)
	}
	if len(repRedir.Samples[0].Hops) < 2 {
		t.Fatalf("expected per-hop timeline with >=2 hops, got %d", len(repRedir.Samples[0].Hops))
	}
	hop0 := repRedir.Samples[0].Hops[0]
	if hop0.Stages[StageDownload].Status != ObsUnobserved {
		t.Fatalf("expected intermediate redirect hop download status to be ObsUnobserved, got %s", hop0.Stages[StageDownload].Status)
	}
}

func TestRedactionAndExportConfinement(t *testing.T) {
	secretVal := "super-secret-bearer-token-98765"
	proj := makeTestProject("http://127.0.0.1:8080/secure?api_key={{secret.api_token}}")
	proj.AuthSchemes["bearer_auth"] = &core.AuthScheme{
		SchemaVersion: 1,
		ID:            "bearer_auth",
		Type:          "bearer",
		Token:         "{{secret.api_token}}",
	}
	proj.Requests["requests/orders.yaml"].Request.Auth = &core.AuthRef{Mode: "ref", Ref: "bearer_auth"}

	execFn := ExecutorFunc(func(ctx context.Context, plan *core.Plan, opts core.ExecuteOptions) (*core.ExecResult, error) {
		return nil, errors.New("upstream rejected token " + secretVal)
	})

	eng := New(proj, Options{
		RequestRef: "requests/orders.yaml",
		Config:     SamplingConfig{Iterations: 2, Concurrency: 1},
		Secrets:    core.MapSecrets{"api_token": secretVal},
		Executor:   execFn,
	})
	rep, _ := eng.Run(context.Background())

	jsonBytes, err := MarshalJSONReport(rep, nil)
	if err != nil {
		t.Fatalf("MarshalJSONReport failed: %v", err)
	}
	if strings.Contains(string(jsonBytes), secretVal) {
		t.Fatalf("secret leaked in JSON report:\n%s", string(jsonBytes))
	}

	csvBytes, err := MarshalCSVReport(rep, nil)
	if err != nil {
		t.Fatalf("MarshalCSVReport failed: %v", err)
	}
	if strings.Contains(string(csvBytes), secretVal) {
		t.Fatalf("secret leaked in CSV report:\n%s", string(csvBytes))
	}

	// Verify CSV stage columns never coerce reused/unobserved stages to 0.
	reusedRep := &Report{
		RunID:          "run-1",
		SnapshotDigest: "snap-1",
		RequestID:      "req-1",
		RequestPath:    "requests/orders.yaml",
		Method:         http.MethodGet,
		Config:         SamplingConfig{ConnectionStrategy: StrategyKeepAlive, Seed: 1},
		ReuseSupport:   ReuseSupported,
		Samples: []SampleRecord{
			{
				Index:       1,
				Outcome:     OutcomeSuccess,
				StatusCode:  http.StatusOK,
				DurationMs:  42.5,
				ReusedConn:  true,
				ReusedKnown: true,
				Hops: []HopTimeline{
					{
						Hop: 0,
						Stages: map[StageKind]StageMeasurement{
							StageDNS:          {Stage: StageDNS, Status: ObsSkippedReused},
							StageTCPConnect:   {Stage: StageTCPConnect, Status: ObsSkippedReused},
							StageTLSHandshake: {Stage: StageTLSHandshake, Status: ObsNotApplicable},
							StageTTFB:         {Stage: StageTTFB, Status: ObsObserved, DurationMs: 40.0},
							StageDownload:     {Stage: StageDownload, Status: ObsObserved, DurationMs: 2.5},
						},
					},
				},
			},
		},
	}
	rawCSV, err := MarshalCSVReport(reusedRep, nil)
	if err != nil {
		t.Fatalf("MarshalCSVReport failed: %v", err)
	}
	rows, err := csv.NewReader(strings.NewReader(string(rawCSV))).ReadAll()
	if err != nil || len(rows) != 2 {
		t.Fatalf("expected header + 1 row, got len=%d err=%v", len(rows), err)
	}
	colIdx := make(map[string]int, len(rows[0]))
	for i, name := range rows[0] {
		colIdx[name] = i
	}
	row := rows[1]
	if row[colIdx["dns_status"]] != string(ObsSkippedReused) || row[colIdx["dns_ms"]] != "" {
		t.Fatalf("expected dns_status=%q and empty dns_ms, got status=%q ms=%q",
			ObsSkippedReused, row[colIdx["dns_status"]], row[colIdx["dns_ms"]])
	}
	if row[colIdx["ttfb_status"]] != string(ObsObserved) || row[colIdx["ttfb_ms"]] != "40.000" {
		t.Fatalf("expected ttfb_status=observed and ttfb_ms=40.000, got status=%q ms=%q",
			row[colIdx["ttfb_status"]], row[colIdx["ttfb_ms"]])
	}

	// Verify ExportReportFile confinement rules.
	projectRoot := t.TempDir()
	exportDir := t.TempDir()

	// 1. Inside projectRoot must fail.
	if err := ExportReportFile(filepath.Join(projectRoot, "report.json"), projectRoot, ExportJSON, reusedRep, nil); err == nil {
		t.Fatal("expected error when exporting inside shared projectRoot")
	}
	// 2. Inside .gorequest must fail.
	if err := ExportReportFile(filepath.Join(exportDir, ".gorequest", "report.json"), projectRoot, ExportJSON, reusedRep, nil); err == nil {
		t.Fatal("expected error when exporting inside .gorequest directory")
	}
	// 3. Valid external path must succeed with 0600 permissions.
	validJSONPath := filepath.Join(exportDir, "diag-report.json")
	if err := ExportReportFile(validJSONPath, projectRoot, ExportJSON, reusedRep, nil); err != nil {
		t.Fatalf("ExportReportFile to external path failed: %v", err)
	}
	fi, err := os.Stat(validJSONPath)
	if err != nil {
		t.Fatalf("stat exported file: %v", err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("exported file permissions = %v, want 0600", fi.Mode().Perm())
	}
}

func TestCompareReports_ConfigParityAndAttribution(t *testing.T) {
	base := &Report{
		RunID:          "base-1",
		RequestID:      "req-orders",
		RequestPath:    "requests/orders.yaml",
		Method:         http.MethodGet,
		TargetURL:      "http://127.0.0.1:8080/orders",
		SnapshotDigest: "digest-abc",
		Environment:    "staging",
		CoreVersion:    DefaultCoreVersion,
		ProxyMode:      "none",
		ReuseSupport:   ReuseSupported,
		Config: SamplingConfig{
			Iterations:         5,
			Concurrency:        1,
			Seed:               1,
			ConnectionStrategy: StrategyKeepAlive,
		},
		Summary:        SampleSummary{MeasuredTotal: 5, SuccessCount: 5, FailureRate: 0},
		SuccessLatency: ComputeDistribution([]float64{100, 200, 300, 400, 500}),
	}

	candSame := *base
	candSame.RunID = "cand-1"
	candSame.SuccessLatency = ComputeDistribution([]float64{110, 210, 315, 420, 525})

	compOK := CompareReports(base, &candSame)
	if compOK == nil || !compOK.Comparable || !compOK.SnapshotMatched || !compOK.StrategyMatched {
		t.Fatalf("expected comparable reports, got %+v", compOK)
	}
	if compOK.AttributionDisclaimer == "" {
		t.Fatal("expected non-empty AttributionDisclaimer")
	}

	// Changing ConnectionStrategy or Environment must mark Comparable=false.
	candDiff := candSame
	candDiff.Environment = "dev"
	candDiff.Config.ConnectionStrategy = StrategyFresh
	compMismatch := CompareReports(base, &candDiff)
	if compMismatch.Comparable {
		t.Fatal("expected Comparable=false when environment and connectionStrategy differ")
	}
	if len(compMismatch.ConfigDiffs) < 2 {
		t.Fatalf("expected blocking ConfigDiffs, got %+v", compMismatch.ConfigDiffs)
	}
}

func makeTestProject(rawURL string) *core.Project {
	req := &core.Request{
		SchemaVersion: 1,
		ID:            "req-orders",
		Name:          "List Orders",
		HTTP: &core.HTTPDef{
			Method: http.MethodGet,
			URL:    rawURL,
		},
	}
	return &core.Project{
		Dir: "/tmp/test-project",
		File: &core.ProjectFile{
			SchemaVersion: 1,
			ID:            "proj-test",
			Name:          "Test Project",
		},
		Order: []string{"requests/orders.yaml"},
		Requests: map[string]*core.RequestEntry{
			"requests/orders.yaml": {
				RelPath:        "requests/orders.yaml",
				BaselineDigest: "baseline-digest-1",
				Request:        req,
			},
		},
		Environments: map[string]*core.Environment{
			"dev": {
				SchemaVersion: 1,
				ID:            "dev",
				Variables:     map[string]string{},
			},
		},
		EnvEntries:  map[string]*core.EnvironmentEntry{},
		AuthSchemes: map[string]*core.AuthScheme{},
	}
}
