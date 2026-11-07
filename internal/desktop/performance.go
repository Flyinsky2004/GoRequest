package desktop

import (
	"context"
	"fmt"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"weak"

	"gorequest/internal/core"
	"gorequest/internal/diagnostics"
)

const perfEventLimit = 256

// performanceState holds per-window UI state for the HTTP Performance
// Diagnostics modal. It holds no strong references to App, Window, Tab, or
// projectState and is accessed exclusively on the owning window's UI thread.
type performanceState struct {
	open        bool
	running     bool
	requestRef  string
	environment string
	exportPath  string

	requests     []string
	environments []string

	iterationsText  string
	concurrencyText string
	warmupText      string
	intervalMsText  string
	deadlineSecText string
	seedText        string
	strategyMode    int // 0 = Keep-Alive, 1 = Fresh per Sample

	gateEnabled         bool
	gateQuantile        string
	gateMaxMsText       string
	gateMinSamplesText  string
	gateMaxFailRateText string

	viewTab           int // 0=Summary, 1=Stages, 2=Timeline, 3=Samples, 4=Compare
	selectedSampleIdx int

	notice       string
	exportNotice string
	snapshotRoot string

	events       []diagnostics.Event
	report       *diagnostics.Report
	baseline     *diagnostics.Report
	comparison   *diagnostics.ComparisonReport
	cancel       context.CancelFunc
	generation   uint64
	testExecutor diagnostics.Executor
}

var perfStates = struct {
	sync.Mutex
	byWindow map[weak.Pointer[App]]*performanceState
}{byWindow: map[weak.Pointer[App]]*performanceState{}}

func newDefaultPerformanceState() *performanceState {
	return &performanceState{
		iterationsText:      strconv.Itoa(diagnostics.DefaultIterations),
		concurrencyText:     strconv.Itoa(diagnostics.DefaultConcurrency),
		warmupText:          strconv.Itoa(diagnostics.DefaultWarmup),
		intervalMsText:      "0",
		deadlineSecText:     "60",
		seedText:            "1",
		strategyMode:        0,
		gateQuantile:        "P95",
		gateMaxMsText:       "500",
		gateMinSamplesText:  "5",
		gateMaxFailRateText: "0.05",
	}
}

func (a *App) performanceState(create bool) *performanceState {
	if a == nil {
		return nil
	}
	key := weak.Make(a)
	perfStates.Lock()
	defer perfStates.Unlock()
	if s := perfStates.byWindow[key]; s != nil {
		return s
	}
	if !create {
		return nil
	}
	s := newDefaultPerformanceState()
	perfStates.byWindow[key] = s
	runtime.AddCleanup(a, func(k weak.Pointer[App]) {
		perfStates.Lock()
		delete(perfStates.byWindow, k)
		perfStates.Unlock()
	}, key)
	if a.win != nil {
		a.win.OnClosed(func() {
			if s.cancel != nil {
				s.cancel()
			}
			s.generation++
			perfStates.Lock()
			delete(perfStates.byWindow, key)
			perfStates.Unlock()
		})
	}
	return s
}

// OpenPerformance opens the HTTP Performance Diagnostics modal for the active
// project, defaulting to the active request tab if one is selected.
func (a *App) OpenPerformance() {
	if a == nil || a.ps == nil || a.ps.project == nil {
		return
	}
	defaultRef := ""
	if tab := a.ps.activeTab(); tab != nil && tab.Entry != nil {
		defaultRef = tab.Entry.RelPath
	}
	a.OpenPerformanceForRequest(defaultRef)
}

// OpenPerformanceForRequest opens the HTTP Performance Diagnostics modal and
// preselects relPath when non-empty. Opening the modal never sends network traffic.
func (a *App) OpenPerformanceForRequest(relPath string) {
	if a == nil || a.ps == nil || a.ps.project == nil {
		return
	}
	s := a.performanceState(true)
	s.open = true
	if s.running {
		return
	}
	s.environment = a.ps.currentEnv()
	s.environments = []string{""}
	s.environments = append(s.environments, a.ps.project.EnvOrder...)

	reqs := make([]string, 0, len(a.ps.project.Requests))
	if len(a.ps.project.Order) > 0 {
		for _, rel := range a.ps.project.Order {
			if e := a.ps.project.Requests[rel]; e != nil && e.Request != nil {
				reqs = append(reqs, e.RelPath)
			}
		}
	} else {
		for _, e := range a.ps.project.Requests {
			if e != nil && e.Request != nil {
				reqs = append(reqs, e.RelPath)
			}
		}
		sort.Strings(reqs)
	}
	s.requests = reqs

	if relPath != "" {
		s.requestRef = relPath
	} else if s.requestRef == "" && len(s.requests) > 0 {
		s.requestRef = s.requests[0]
	} else if len(s.requests) > 0 {
		found := false
		for _, r := range s.requests {
			if r == s.requestRef {
				found = true
				break
			}
		}
		if !found {
			s.requestRef = s.requests[0]
		}
	}
	s.notice = ""
}

// performanceMailbox bounds pending UI progress events to perfEventLimit and
// coalesces Window.Update callbacks so background sampling never floods the UI
// thread. The final Report is stored in a dedicated slot and is never dropped.
type performanceMailbox struct {
	mu        sync.Mutex
	events    []diagnostics.Event
	final     *diagnostics.Report
	finished  bool
	scheduled bool
	post      func(func())
	deliver   func([]diagnostics.Event, *diagnostics.Report, bool)
}

func (m *performanceMailbox) publish(ev *diagnostics.Event, rep *diagnostics.Report, finished bool) {
	m.mu.Lock()
	if ev != nil {
		if len(m.events) == perfEventLimit {
			copy(m.events, m.events[1:])
			m.events = m.events[:perfEventLimit-1]
		}
		m.events = append(m.events, *ev)
	}
	if finished {
		m.final = rep
		m.finished = true
	}
	schedule := !m.scheduled
	m.scheduled = true
	m.mu.Unlock()
	if schedule {
		m.post(func() {
			m.mu.Lock()
			events, finalRep, done := m.events, m.final, m.finished
			m.events = nil
			m.final = nil
			m.finished = false
			m.scheduled = false
			m.mu.Unlock()
			m.deliver(events, finalRep, done)
		})
	}
}

func (s *performanceState) appendEvents(events []diagnostics.Event) {
	s.events = append(s.events, events...)
	if len(s.events) > perfEventLimit {
		s.events = append([]diagnostics.Event(nil), s.events[len(s.events)-perfEventLimit:]...)
	}
}

func (s *performanceState) buildSamplingConfig() (diagnostics.SamplingConfig, error) {
	iter, err := strconv.Atoi(strings.TrimSpace(s.iterationsText))
	if err != nil {
		return diagnostics.SamplingConfig{}, fmt.Errorf("iterations must be an integer between 1 and %d", diagnostics.MaxIterations)
	}
	conc, err := strconv.Atoi(strings.TrimSpace(s.concurrencyText))
	if err != nil {
		return diagnostics.SamplingConfig{}, fmt.Errorf("concurrency must be an integer between 1 and %d", diagnostics.MaxConcurrency)
	}
	warm, err := strconv.Atoi(strings.TrimSpace(s.warmupText))
	if err != nil {
		return diagnostics.SamplingConfig{}, fmt.Errorf("warmup must be an integer between 0 and %d", diagnostics.MaxWarmup)
	}
	intervalMs, err := strconv.Atoi(strings.TrimSpace(s.intervalMsText))
	if err != nil {
		return diagnostics.SamplingConfig{}, fmt.Errorf("interval (ms) must be a non-negative integer")
	}
	deadlineSec, err := strconv.Atoi(strings.TrimSpace(s.deadlineSecText))
	if err != nil {
		return diagnostics.SamplingConfig{}, fmt.Errorf("deadline (s) must be a positive integer")
	}
	var seed int64 = 1
	if trimmedSeed := strings.TrimSpace(s.seedText); trimmedSeed != "" {
		seed, err = strconv.ParseInt(trimmedSeed, 10, 64)
		if err != nil {
			return diagnostics.SamplingConfig{}, fmt.Errorf("seed must be a valid 64-bit integer")
		}
	}

	strat := diagnostics.StrategyKeepAlive
	if s.strategyMode == 1 {
		strat = diagnostics.StrategyFresh
	}

	cfg := diagnostics.SamplingConfig{
		Iterations:         iter,
		Concurrency:        conc,
		Warmup:             warm,
		Interval:           time.Duration(intervalMs) * time.Millisecond,
		Deadline:           time.Duration(deadlineSec) * time.Second,
		Seed:               seed,
		ConnectionStrategy: strat,
	}

	if s.gateEnabled {
		maxMs, err := strconv.ParseFloat(strings.TrimSpace(s.gateMaxMsText), 64)
		if err != nil {
			return diagnostics.SamplingConfig{}, fmt.Errorf("gate max latency (ms) must be a positive number")
		}
		minN, err := strconv.Atoi(strings.TrimSpace(s.gateMinSamplesText))
		if err != nil {
			return diagnostics.SamplingConfig{}, fmt.Errorf("gate min valid samples must be an integer >= 1")
		}
		maxFail, err := strconv.ParseFloat(strings.TrimSpace(s.gateMaxFailRateText), 64)
		if err != nil {
			return diagnostics.SamplingConfig{}, fmt.Errorf("gate max failure rate must be between 0.0 and 1.0")
		}
		cfg.Gate = &diagnostics.ThresholdGate{
			Quantile:        s.gateQuantile,
			MaxLatencyMs:    maxMs,
			MinValidSamples: minN,
			MaxFailureRate:  maxFail,
		}
	}

	return cfg.ValidateAndNormalize()
}

// StartPerformance captures the saved project snapshot, environment, network
// options, and bound secrets on the UI thread, then launches bounded sampling
// in a background goroutine with zero UI object references.
func (a *App) StartPerformance() {
	s := a.performanceState(true)
	if s.running || a.ps == nil || a.win == nil {
		return
	}
	cfg, err := s.buildSamplingConfig()
	if err != nil {
		s.reject(err.Error(), a.ps.root)
		return
	}
	if strings.TrimSpace(s.requestRef) == "" {
		s.reject("select a saved request before starting diagnostics", a.ps.root)
		return
	}
	p, err := core.OpenProject(a.ps.root)
	if err != nil {
		s.reject("cannot load saved project snapshot: "+err.Error(), a.ps.root)
		return
	}

	// Copy network options on the UI thread via the stable port without
	// modifying network_ui.go or sharing UI Tab pointers with workers.
	tab := &Tab{Owner: a.ps}
	netOpts := a.buildExecuteOptionsForTab(a.ps, tab, nil, nil, nil)
	netOpts.EnvID = s.environment

	ctx, cancel := context.WithCancel(context.Background())
	s.generation++
	generation := s.generation
	key := weak.Make(a)
	win := a.win

	mailbox := &performanceMailbox{
		post: win.Update,
		deliver: func(events []diagnostics.Event, rep *diagnostics.Report, finished bool) {
			owner := key.Value()
			if owner == nil || owner.closed {
				return
			}
			state := owner.performanceState(false)
			if state == nil || state.generation != generation {
				return
			}
			state.appendEvents(events)
			if finished {
				state.report = rep
				state.running = false
				state.selectedSampleIdx = 0
				if state.cancel != nil {
					state.cancel()
					state.cancel = nil
				}
				if rep != nil {
					if state.baseline != nil {
						comp := diagnostics.CompareReports(state.baseline, rep)
						state.comparison = comp
						rep.Comparison = comp
					}
					if rep.FailureReason != "" {
						state.notice = fmt.Sprintf("%s: %s", rep.Status, rep.FailureReason)
					} else {
						state.notice = perfSummaryText(rep)
					}
				} else {
					state.notice = "diagnostics failed to produce a report"
				}
			}
		},
	}

	eng, err := prepareDesktopPerformance(
		ctx,
		p,
		s.requestRef,
		s.environment,
		cfg,
		netOpts,
		a.mgrSecrets(),
		s.testExecutor,
		func(ev diagnostics.Event) { mailbox.publish(&ev, nil, false) },
	)
	if err != nil {
		cancel()
		s.reject(err.Error(), a.ps.root)
		return
	}

	// Check preflight result immediately so policy/config blocks surface
	// synchronously without spawning unnecessary background workers.
	if prepErr := eng.Prepare(ctx); prepErr != nil {
		rep, _ := eng.Run(ctx)
		cancel()
		s.report = rep
		s.running = false
		s.events = nil
		s.snapshotRoot = a.ps.root
		if rep != nil && rep.FailureReason != "" {
			s.notice = fmt.Sprintf("%s: %s", rep.Status, rep.FailureReason)
		} else {
			s.notice = prepErr.Error()
		}
		return
	}

	s.cancel = cancel
	s.running = true
	s.report = nil
	s.comparison = nil
	s.events = nil
	s.exportNotice = ""
	s.notice = a.perfMsg().Running
	s.snapshotRoot = a.ps.root

	go func() {
		rep, _ := eng.Run(ctx)
		mailbox.publish(nil, rep, true)
	}()
}

// CancelPerformance cancels any active diagnostic run in this window.
func (a *App) CancelPerformance() {
	if s := a.performanceState(false); s != nil && s.cancel != nil {
		s.cancel()
	}
}

// PinPerformanceBaseline pins the current diagnostic Report as the comparison
// baseline for subsequent runs in this window.
func (a *App) PinPerformanceBaseline() {
	s := a.performanceState(false)
	if s == nil || s.report == nil {
		return
	}
	cp := *s.report
	s.baseline = &cp
	s.comparison = diagnostics.CompareReports(s.baseline, s.report)
}

// ClearPerformanceBaseline clears the pinned comparison baseline in this window.
func (a *App) ClearPerformanceBaseline() {
	s := a.performanceState(false)
	if s == nil {
		return
	}
	s.baseline = nil
	s.comparison = nil
	if s.report != nil {
		s.report.Comparison = nil
	}
}

func (s *performanceState) reject(message, root string) {
	s.notice = message
	s.events = nil
	s.snapshotRoot = root
	s.running = false
	s.report = &diagnostics.Report{
		FormatVersion:   diagnostics.ReportFormatVersion,
		ScopeDisclaimer: diagnostics.ScopeDisclaimer,
		RequestPath:     s.requestRef,
		Environment:     s.environment,
		Status:          diagnostics.StatusConfigError,
		FailureReason:   message,
	}
}

func perfSummaryText(rep *diagnostics.Report) string {
	if rep == nil {
		return ""
	}
	sum := rep.Summary
	base := fmt.Sprintf(
		"%s · measured %d (warmup %d) · success %d (%.1f%%) · failed %d (%.1f%%) · reused %d / fresh %d",
		rep.Status,
		sum.MeasuredTotal,
		sum.WarmupCompleted,
		sum.SuccessCount,
		sum.SuccessRate*100,
		sum.FailureCount,
		sum.FailureRate*100,
		sum.ReusedConnCount,
		sum.FreshConnCount,
	)
	if rep.SuccessLatency != nil {
		base += fmt.Sprintf(
			" · P50 %.2f ms · P95 %.2f ms · P99 %.2f ms (n=%d)",
			rep.SuccessLatency.P50Ms,
			rep.SuccessLatency.P95Ms,
			rep.SuccessLatency.P99Ms,
			rep.SuccessLatency.Count,
		)
	}
	return base
}
