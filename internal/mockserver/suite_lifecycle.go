package mockserver

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"gorequest/internal/core"
	"gorequest/internal/runner"
)

// Default variable names injected into a Suite's run context when a controlled
// Mock Server is started for that Suite run.
const (
	DefaultMockURLVar  = "mockUrl"
	DefaultMockBaseVar = "mockBaseUrl"
	DefaultMockHostVar = "mockHost"
	DefaultMockPortVar = "mockPort"
)

// SuiteMockConfig configures the controlled Mock Server lifecycle for a single
// Suite execution.
type SuiteMockConfig struct {
	// Port is the loopback TCP port to bind; 0 (the default) allocates an
	// ephemeral loopback port (127.0.0.1:0) and injects the actual address into
	// the Suite's run variables without modifying any shared environment file.
	Port int
	// Scenario selects the mock scenario frozen for the entire Suite run.
	Scenario string
	// Seed is the deterministic seed used by the Mock Server (defaults to
	// runner.Options.Seed or 1).
	Seed int64
	// EndpointVar is an optional additional run variable name (such as "baseUrl"
	// or "apiBaseUrl") to populate with the bound "http://127.0.0.1:<port>" URL
	// alongside mockUrl / mockBaseUrl / mockHost / mockPort.
	EndpointVar string
	// Snapshot optionally supplies an already-compiled immutable snapshot. When
	// nil, rules are loaded and compiled from project's .gorequest/mocks/*.mock.yaml.
	Snapshot *CompiledSnapshot
	// Limits bounds the controlled Mock Server's resources.
	Limits ServerLimits
	// Redactor, when non-nil, is installed on the server before it starts
	// listening. Callers must register every project/environment secret here so
	// access records are scrubbed from the first accepted request; there is no
	// window where the listener is up but unredacted.
	Redactor *core.Redactor
}

// SuiteMockRunOutcome wraps the Runner's RunResult together with metadata from
// the controlled Mock Server lifecycle (bound endpoint, frozen snapshot digest,
// sanitized access records, and clean shutdown status).
type SuiteMockRunOutcome struct {
	Result        *runner.RunResult `json:"result"`
	MockBaseURL   string            `json:"mockBaseUrl"`
	MockBoundAddr string            `json:"mockBoundAddr"`
	MockScenario  string            `json:"mockScenario"`
	MockSnapshot  string            `json:"mockSnapshot"`
	AccessRecords []AccessRecord    `json:"accessRecords"`
	ServerStopped bool              `json:"serverStopped"`
}

// SuiteMockLifecyclePort is the minimal lifecycle port for starting and stopping
// a Suite-scoped Mock Server around a Runner execution.
type SuiteMockLifecyclePort interface {
	StartForSuite(ctx context.Context, project *core.Project) (injectedVars map[string]string, stop func(context.Context) error, err error)
}

// ControlledSuiteMock implements SuiteMockLifecyclePort using SuiteMockConfig.
type ControlledSuiteMock struct {
	Config SuiteMockConfig
	server *Server
	snap   *CompiledSnapshot
}

// StartForSuite validates/compiles the mock rules, starts the loopback listener,
// checks readiness, and returns the run-scoped endpoint variables plus the
// idempotent stop callback.
func (m *ControlledSuiteMock) StartForSuite(ctx context.Context, project *core.Project) (map[string]string, func(context.Context) error, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	seed := m.Config.Seed
	if seed == 0 {
		seed = 1
	}

	snap := m.Config.Snapshot
	if snap == nil {
		compiled, err := CompileProjectMocks(project, m.Config.Scenario, seed, m.Config.Limits)
		if err != nil {
			return nil, nil, err
		}
		if len(compiled.Rules) == 0 {
			return nil, nil, &core.ConfigError{
				Err: fmt.Errorf("suite controlled mock requires at least one enabled rule in .gorequest/mocks/*.mock.yaml for scenario %q", compiled.Scenario),
			}
		}
		snap = compiled
	} else {
		if len(snap.Conflicts) > 0 {
			return nil, nil, &AmbiguityError{Conflicts: snap.Conflicts}
		}
		if len(snap.Rules) == 0 {
			return nil, nil, &core.ConfigError{
				Err: fmt.Errorf("suite controlled mock snapshot has no active rules for scenario %q", snap.Scenario),
			}
		}
		if m.Config.Scenario != "" && !strings.EqualFold(m.Config.Scenario, snap.Scenario) {
			recompiled, err := snap.WithScenario(m.Config.Scenario)
			if err != nil {
				return nil, nil, err
			}
			snap = recompiled
		}
	}

	srv := NewServer(ServerConfig{
		Host:     DefaultBindHost, // Suite controlled mock always binds loopback 127.0.0.1
		Port:     m.Config.Port,
		AllowLAN: false,
		Limits:   m.Config.Limits,
		Redactor: m.Config.Redactor,
	})
	if err := srv.Start(ctx, snap); err != nil {
		_ = srv.Stop(context.Background())
		return nil, nil, err
	}
	if err := srv.WaitReady(ctx); err != nil {
		_ = srv.Stop(context.Background())
		return nil, nil, err
	}

	m.server = srv
	m.snap = snap

	baseURL := srv.BaseURL()
	portStr := strconv.Itoa(srv.Port())
	vars := map[string]string{
		DefaultMockURLVar:  baseURL,
		DefaultMockBaseVar: baseURL,
		DefaultMockHostVar: DefaultBindHost,
		DefaultMockPortVar: portStr,
	}
	if customVar := strings.TrimSpace(m.Config.EndpointVar); customVar != "" && !core.IsReservedNamespace(customVar) {
		vars[customVar] = baseURL
	}

	stopFn := func(stopCtx context.Context) error {
		return srv.Stop(stopCtx)
	}
	return vars, stopFn, nil
}

// SuiteMockLease is a started, frozen controlled Mock Server scoped to one Suite
// run. It exposes the injected run variables and actual bound endpoint so a
// caller can prepare its Runner against the real address, and it guarantees an
// idempotent, concurrency-safe Stop that reports the true shutdown outcome.
type SuiteMockLease struct {
	server *Server
	snap   *CompiledSnapshot
	vars   map[string]string

	stopOnce sync.Once
	mu       sync.Mutex
	stopErr  error
	stopped  bool
}

// StartSuiteMockLease validates/compiles the frozen snapshot, binds the loopback
// listener (port 0 allocates an ephemeral port), checks readiness, and returns a
// lease. The caller must Stop it on every path.
func StartSuiteMockLease(ctx context.Context, project *core.Project, cfg SuiteMockConfig) (*SuiteMockLease, error) {
	ctrl := &ControlledSuiteMock{Config: cfg}
	vars, _, err := ctrl.StartForSuite(ctx, project)
	if err != nil {
		return nil, err
	}
	return &SuiteMockLease{server: ctrl.server, snap: ctrl.snap, vars: vars}, nil
}

// Variables returns a copy of the run variables injected for this lease
// (mockUrl/mockBaseUrl/mockHost/mockPort plus the optional EndpointVar).
func (l *SuiteMockLease) Variables() map[string]string {
	if l == nil {
		return nil
	}
	out := make(map[string]string, len(l.vars))
	for k, v := range l.vars {
		out[k] = v
	}
	return out
}

// BaseURL returns the actual `http://127.0.0.1:<port>` bound for this lease.
func (l *SuiteMockLease) BaseURL() string {
	if l == nil {
		return ""
	}
	return l.server.BaseURL()
}

// BoundAddr returns the actual bound `host:port`, or "" once stopped.
func (l *SuiteMockLease) BoundAddr() string {
	if l == nil {
		return ""
	}
	return l.server.BoundAddr()
}

// Port returns the actual bound TCP port, or 0 once stopped.
func (l *SuiteMockLease) Port() int {
	if l == nil {
		return 0
	}
	return l.server.Port()
}

// Scenario returns the frozen scenario served by this lease.
func (l *SuiteMockLease) Scenario() string {
	if l == nil || l.snap == nil {
		return ""
	}
	return l.snap.Scenario
}

// SnapshotDigest returns the frozen rule snapshot digest served by this lease.
func (l *SuiteMockLease) SnapshotDigest() string {
	if l == nil || l.snap == nil {
		return ""
	}
	return l.snap.SnapshotDigest
}

// AccessRecords returns the bounded, already-sanitized access records captured
// while this lease was running.
func (l *SuiteMockLease) AccessRecords() []AccessRecord {
	if l == nil || l.server == nil || l.server.AccessLog() == nil {
		return nil
	}
	return l.server.AccessLog().Entries()
}

// ServerStopped reports whether Stop completed without error and the listener is
// no longer running. It never reports true for a failed shutdown.
func (l *SuiteMockLease) ServerStopped() bool {
	if l == nil {
		return true
	}
	l.mu.Lock()
	stopped := l.stopped
	l.mu.Unlock()
	return stopped && !l.server.IsRunning()
}

// Stop shuts the controlled Mock Server down. It is idempotent and safe to call
// concurrently; only the first call performs the shutdown and the returned error
// is that call's real result.
func (l *SuiteMockLease) Stop(ctx context.Context) error {
	if l == nil {
		return nil
	}
	l.stopOnce.Do(func() {
		err := l.server.Stop(ctx)
		l.mu.Lock()
		l.stopErr = err
		l.stopped = err == nil
		l.mu.Unlock()
	})
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.stopErr
}

// RunSuiteWithControlledMock executes a runner.Suite against a dedicated,
// snapshot-frozen loopback Mock Server:
//  1. Validates and compiles the mock rules (or frozen snapshot) before starting
//     any listener or sending any network request.
//  2. Starts the loopback Mock Server and verifies readiness.
//  3. Injects the actual bound endpoint into a private copy of suite.Variables
//     (never writing back to shared project/environment files).
//  4. Runs runner.Runner with existing event emission and context cancellation.
//  5. Guarantees Mock Server shutdown whether the Suite passes, fails, errors
//     at preflight, or is canceled.
func RunSuiteWithControlledMock(
	ctx context.Context,
	project *core.Project,
	suite *runner.Suite,
	mockCfg SuiteMockConfig,
	opts runner.Options,
) (*SuiteMockRunOutcome, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if project == nil {
		res := configErrorRunResult(suite, opts.Environment, "controlled mock suite requires a loaded project")
		return &SuiteMockRunOutcome{Result: res, ServerStopped: true}, fmt.Errorf("controlled mock suite requires a loaded project")
	}
	if suite == nil {
		res := configErrorRunResult(nil, opts.Environment, "controlled mock suite requires a suite definition")
		return &SuiteMockRunOutcome{Result: res, ServerStopped: true}, fmt.Errorf("controlled mock suite requires a suite definition")
	}
	if mockCfg.Seed == 0 {
		if opts.Seed != 0 {
			mockCfg.Seed = opts.Seed
		} else {
			mockCfg.Seed = 1
		}
	}

	ctrl := &ControlledSuiteMock{Config: mockCfg}
	// Register project/environment secrets before the listener accepts requests so
	// no access record can be captured unredacted.
	if ctrl.Config.Redactor == nil {
		ctrl.Config.Redactor = buildProjectSecretRedactor(project, opts.Secrets)
	}
	injectedVars, stopMock, startErr := ctrl.StartForSuite(ctx, project)
	if startErr != nil {
		res := configErrorRunResult(suite, opts.Environment, startErr.Error())
		return &SuiteMockRunOutcome{
			Result:        res,
			MockScenario:  mockCfg.Scenario,
			ServerStopped: true,
		}, startErr
	}

	outcome := &SuiteMockRunOutcome{
		MockBaseURL:   ctrl.server.BaseURL(),
		MockBoundAddr: ctrl.server.BoundAddr(),
		MockScenario:  ctrl.snap.Scenario,
		MockSnapshot:  ctrl.snap.SnapshotDigest,
	}

	// Ensure the controlled Mock Server is always stopped and cleaned up when
	// RunSuiteWithControlledMock returns, even on panic, cancel, or preflight failure.
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), ctrl.server.limits.ShutdownTimeout)
		defer cancel()
		_ = stopMock(stopCtx)
		outcome.AccessRecords = ctrl.server.AccessLog().Entries()
		outcome.ServerStopped = !ctrl.server.IsRunning()
	}()

	// Also cancel the Mock Server immediately if ctx is canceled mid-run so any
	// in-flight mock delay timer wakes up without waiting for client timeout.
	watchDone := make(chan struct{})
	defer close(watchDone)
	go func() {
		select {
		case <-ctx.Done():
			stopCtx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
			defer cancel()
			_ = stopMock(stopCtx)
		case <-watchDone:
		}
	}()

	// Clone Suite so injected run variables never mutate the caller's Suite or project.
	suiteCopy := InjectSuiteVariables(suite, injectedVars)

	r := runner.New(project, suiteCopy, opts)

	res, runErr := r.Run(ctx)
	outcome.Result = res
	return outcome, runErr
}

// InjectSuiteVariables returns a copy of s whose run variables merge injected on
// top of the caller's Suite.Variables. The caller's Suite and project are never
// mutated, so a controlled Mock endpoint stays scoped to one run.
func InjectSuiteVariables(s *runner.Suite, injected map[string]string) *runner.Suite {
	if s == nil {
		return nil
	}
	cp := *s
	merged := make(map[string]string, len(s.Variables)+len(injected))
	for k, v := range s.Variables {
		merged[k] = v
	}
	for k, v := range injected {
		merged[k] = v
	}
	cp.Variables = merged
	cp.Steps = append([]runner.SuiteStep(nil), s.Steps...)
	return &cp
}

func buildProjectSecretRedactor(project *core.Project, secrets core.SecretProvider) *core.Redactor {
	red := core.NewRedactor()
	if secrets == nil {
		return red
	}
	if enumerator, ok := secrets.(interface{ SecretNames() []string }); ok {
		for _, name := range enumerator.SecretNames() {
			if v, ok := secrets.Get(name); ok && v != "" {
				red.RegisterSecret(v)
			}
		}
	} else if ms, ok := secrets.(core.MapSecrets); ok {
		for _, v := range ms {
			if v != "" {
				red.RegisterSecret(v)
			}
		}
	}
	if project != nil {
		for _, env := range project.Environments {
			if env == nil {
				continue
			}
			for _, name := range env.Secrets {
				if v, ok := secrets.Get(strings.TrimSpace(name)); ok && v != "" {
					red.RegisterSecret(v)
				}
			}
		}
	}
	return red
}

func configErrorRunResult(suite *runner.Suite, env string, msg string) *runner.RunResult {
	res := &runner.RunResult{
		FormatVersion: runner.ReportFormatVersion,
		SchemaVersion: runner.SuiteSchemaVersion,
		Environment:   env,
		Status:        runner.StatusConfigError,
		Failure: &runner.Failure{
			Category: runner.FailureConfig,
			Kind:     runner.KindDefinition,
			Message:  msg,
		},
		Rows: []runner.RowResult{},
	}
	if suite != nil {
		res.SuiteID = suite.ID
		res.SuiteName = suite.Name
	}
	return res
}
