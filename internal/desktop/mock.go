package desktop

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
	"weak"

	"runtime"

	"gorequest/internal/core"
	"gorequest/internal/mockserver"
	"gorequest/internal/runner"
)

const defaultWorkspaceMockFileID = "workspace-mocks"

// mockState holds per-window Local Mock Server UI and lifecycle state.
// It stores no strong pointer to App, window, Tab, or projectState, preventing
// any global UI map leak.
type mockState struct {
	open        bool
	tab         int // 0 = Rules & Conflicts, 1 = Access Log, 2 = Suite Controlled Run
	projectRoot string

	listenHost string
	listenPort string
	allowLAN   bool
	scenario   string
	seedText   string
	targetVar  string

	files              []*mockserver.MockFile
	snapshot           *mockserver.CompiledSnapshot
	conflicts          []mockserver.RuleConflict
	availableScenarios []string

	server      *mockserver.Server
	running     bool
	boundAddr   string
	baseURL     string
	isLAN       bool
	accessLog   []mockserver.AccessRecord
	logCapacity int

	suites        []string
	selectedSuite string
	suiteEnv      string
	environments  []string
	suiteRunning  bool
	suiteCancel   context.CancelFunc
	suiteGen      uint64
	suiteEvents   []runner.Event
	suiteOutcome  *mockserver.SuiteMockRunOutcome

	notice string
	errMsg string
}

var mockStates = struct {
	sync.Mutex
	byWindow map[weak.Pointer[App]]*mockState
}{byWindow: map[weak.Pointer[App]]*mockState{}}

func (a *App) mockState(create bool) *mockState {
	if a == nil {
		return nil
	}
	key := weak.Make(a)
	mockStates.Lock()
	defer mockStates.Unlock()
	if s := mockStates.byWindow[key]; s != nil {
		return s
	}
	if !create {
		return nil
	}
	s := &mockState{
		listenHost:  mockserver.DefaultBindHost,
		listenPort:  "0",
		scenario:    mockserver.DefaultScenario,
		seedText:    "1",
		targetVar:   "baseUrl",
		logCapacity: mockserver.DefaultMaxAccessLogEntries,
	}
	mockStates.byWindow[key] = s
	runtime.AddCleanup(a, func(k weak.Pointer[App]) {
		mockStates.Lock()
		st := mockStates.byWindow[k]
		delete(mockStates.byWindow, k)
		mockStates.Unlock()
		if st != nil {
			st.stopAllBackground()
		}
	}, key)
	if a.win != nil {
		a.win.OnClosed(func() {
			s.stopAllBackground()
			mockStates.Lock()
			delete(mockStates.byWindow, key)
			mockStates.Unlock()
		})
	}
	return s
}

func (s *mockState) stopAllBackground() {
	if s == nil {
		return
	}
	if s.suiteCancel != nil {
		s.suiteCancel()
		s.suiteCancel = nil
	}
	s.suiteGen++
	s.suiteRunning = false
	if srv := s.server; srv != nil {
		s.server = nil
		s.running = false
		s.boundAddr = ""
		s.baseURL = ""
		s.isLAN = false
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = srv.Stop(ctx)
	}
}

func (s *mockState) seedInt() int64 {
	if s == nil {
		return 1
	}
	v, err := strconv.ParseInt(strings.TrimSpace(s.seedText), 10, 64)
	if err != nil || v == 0 {
		return 1
	}
	return v
}

func (s *mockState) portInt() (int, error) {
	if s == nil {
		return 0, nil
	}
	raw := strings.TrimSpace(s.listenPort)
	if raw == "" {
		return 0, nil
	}
	p, err := strconv.Atoi(raw)
	if err != nil || p < 0 || p > 65535 {
		return 0, &mockserver.BindPolicyError{
			Host:   s.listenHost,
			Port:   -1,
			Reason: fmt.Sprintf("invalid port %q (must be 0..65535)", raw),
		}
	}
	return p, nil
}

// ensureMockProjectBound checks whether the window switched projects and, if so,
// stops any previous project's mock server and reloads the new project's rules
// without auto-starting any listener.
func (a *App) ensureMockProjectBound(s *mockState) {
	if a == nil || s == nil {
		return
	}
	curRoot := ""
	if a.ps != nil {
		curRoot = a.ps.root
	}
	if s.projectRoot != curRoot {
		s.stopAllBackground()
		s.projectRoot = curRoot
		s.files = nil
		s.snapshot = nil
		s.conflicts = nil
		s.accessLog = nil
		s.suiteOutcome = nil
		s.suiteEvents = nil
		s.notice = ""
		s.errMsg = ""
		if a.ps != nil && a.ps.project != nil {
			a.reloadMockCatalogLocked(s)
		}
	}
}

// OpenMockServer opens the Local Mock Server modal for the current project
// without starting a listener automatically.
func (a *App) OpenMockServer() {
	if a == nil || a.ps == nil || a.ps.project == nil {
		if a != nil {
			a.status = mockT(a, "mock.msg.no_project")
		}
		return
	}
	s := a.mockState(true)
	a.ensureMockProjectBound(s)
	s.open = true
	a.reloadMockCatalogLocked(s)
}

// ReloadMockRules reloads `.gorequest/mocks/*.mock.yaml` from the current project
// and recompiles the active scenario snapshot.
func (a *App) ReloadMockRules() {
	if a == nil || a.ps == nil || a.ps.project == nil {
		return
	}
	s := a.mockState(true)
	a.ensureMockProjectBound(s)
	a.reloadMockCatalogLocked(s)
}

func (a *App) reloadMockCatalogLocked(s *mockState) {
	if a == nil || a.ps == nil || a.ps.project == nil || s == nil {
		return
	}
	p := a.ps.project
	s.environments = []string{""}
	s.environments = append(s.environments, p.EnvOrder...)
	if s.suiteEnv == "" {
		s.suiteEnv = a.ps.currentEnv()
	}

	suites, err := p.Store.List(".", func(name string) bool {
		return strings.HasSuffix(name, ".suite.yaml") || strings.HasSuffix(name, ".suite.yml")
	})
	if err == nil {
		s.suites = suites
		if s.selectedSuite == "" && len(suites) > 0 {
			s.selectedSuite = suites[0]
		}
	}

	files, loadErr := mockserver.LoadProjectMocks(p)
	if loadErr != nil {
		s.files = nil
		s.snapshot = nil
		s.conflicts = nil
		s.errMsg = loadErr.Error()
		return
	}
	s.files = files

	snap, compErr := mockserver.CompileMockFiles(files, s.scenario, s.seedInt(), mockserver.ServerLimits{})
	s.snapshot = snap
	if snap != nil {
		s.scenario = snap.Scenario
		s.availableScenarios = snap.AvailableScenarios
		s.conflicts = snap.Conflicts
	} else {
		s.availableScenarios = append([]string(nil), mockserver.BuiltinScenarios...)
		s.conflicts = nil
	}

	if compErr != nil {
		var ambErr *mockserver.AmbiguityError
		if errors.As(compErr, &ambErr) {
			s.conflicts = ambErr.Conflicts
		}
		s.errMsg = compErr.Error()
		return
	}
	s.errMsg = ""

	if s.running && s.server != nil && snap != nil {
		s.server.SetRedactor(a.buildMockRedactor(p))
		if swapErr := s.server.SwapSnapshot(snap); swapErr != nil {
			s.errMsg = swapErr.Error()
		}
	}
}

// StartMockServer explicitly starts the interactive Mock Server on the configured
// loopback (or explicitly allowed LAN) address.
func (a *App) StartMockServer() {
	if a == nil || a.ps == nil || a.ps.project == nil {
		return
	}
	s := a.mockState(true)
	a.ensureMockProjectBound(s)
	if s.running {
		return
	}

	a.reloadMockCatalogLocked(s)
	if s.errMsg != "" {
		return
	}
	if s.snapshot == nil || len(s.snapshot.Rules) == 0 {
		s.errMsg = mockT(a, "mock.rules.empty")
		return
	}

	port, portErr := s.portInt()
	if portErr != nil {
		s.errMsg = portErr.Error()
		return
	}

	red := a.buildMockRedactor(a.ps.project)
	key := weak.Make(a)
	win := a.win
	var logNotifyScheduled sync.Mutex
	scheduled := false

	srv := mockserver.NewServer(mockserver.ServerConfig{
		Host:     s.listenHost,
		Port:     port,
		AllowLAN: s.allowLAN,
		Redactor: red,
		OnAccess: func(_ mockserver.AccessRecord) {
			if win == nil {
				return
			}
			logNotifyScheduled.Lock()
			if scheduled {
				logNotifyScheduled.Unlock()
				return
			}
			scheduled = true
			logNotifyScheduled.Unlock()
			win.Update(func() {
				logNotifyScheduled.Lock()
				scheduled = false
				logNotifyScheduled.Unlock()
				owner := key.Value()
				if owner == nil || owner.closed {
					return
				}
				st := owner.mockState(false)
				if st == nil || st.server == nil {
					return
				}
				st.accessLog = st.server.AccessLog().Entries()
				st.logCapacity = st.server.AccessLog().MaxEntries()
			})
		},
	})

	if err := srv.Start(context.Background(), s.snapshot); err != nil {
		s.errMsg = err.Error()
		return
	}

	s.server = srv
	s.running = srv.IsRunning()
	s.boundAddr = srv.BoundAddr()
	s.baseURL = srv.BaseURL()
	s.isLAN = srv.IsLAN()
	s.accessLog = srv.AccessLog().Entries()
	s.logCapacity = srv.AccessLog().MaxEntries()
	s.errMsg = ""
	s.notice = mockTf(a, "mock.msg.started", s.baseURL, s.scenario)
	a.status = s.notice
}

// StopMockServer stops the interactive Mock Server and releases its listener.
func (a *App) StopMockServer() {
	if a == nil {
		return
	}
	s := a.mockState(false)
	if s == nil || s.server == nil {
		return
	}
	srv := s.server
	s.server = nil
	s.running = false
	s.boundAddr = ""
	s.baseURL = ""
	s.isLAN = false
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = srv.Stop(ctx)
	s.accessLog = srv.AccessLog().Entries()
	s.notice = mockT(a, "mock.msg.stopped")
	a.status = s.notice
}

// SetMockScenario switches the active scenario on the interactive Mock panel.
// In-flight controlled Suite runs keep their own frozen snapshot.
func (a *App) SetMockScenario(scenario string) {
	if a == nil {
		return
	}
	s := a.mockState(true)
	scenario = strings.TrimSpace(scenario)
	if scenario == "" {
		scenario = mockserver.DefaultScenario
	}
	s.scenario = scenario
	if s.snapshot != nil {
		nextSnap, err := s.snapshot.WithScenario(scenario)
		if err != nil {
			var ambErr *mockserver.AmbiguityError
			if errors.As(err, &ambErr) {
				s.conflicts = ambErr.Conflicts
			}
			s.errMsg = err.Error()
			return
		}
		s.snapshot = nextSnap
		s.conflicts = nextSnap.Conflicts
		s.errMsg = ""
		if s.running && s.server != nil {
			if err := s.server.SwapSnapshot(nextSnap); err != nil {
				s.errMsg = err.Error()
				return
			}
		}
	} else {
		a.reloadMockCatalogLocked(s)
	}
	s.notice = mockTf(a, "mock.msg.scenario_updated", scenario)
}

// ClearMockAccessLog clears the bounded in-memory access log.
func (a *App) ClearMockAccessLog() {
	if a == nil {
		return
	}
	s := a.mockState(false)
	if s == nil {
		return
	}
	if s.server != nil && s.server.AccessLog() != nil {
		s.server.AccessLog().Clear()
	}
	s.accessLog = nil
	s.notice = mockT(a, "mock.msg.log_cleared")
}

// CreateMockRuleFromActiveRequest builds a sanitized MockRule from the active
// request tab (and its last response example if present), saves it to
// `.gorequest/mocks/workspace-mocks.mock.yaml`, and reloads the mock catalog.
func (a *App) CreateMockRuleFromActiveRequest() {
	if a == nil || a.ps == nil || a.ps.project == nil {
		return
	}
	s := a.mockState(true)
	a.ensureMockProjectBound(s)
	tab := a.ps.activeTab()
	if tab == nil || tab.Kind != "request" || tab.Entry == nil || tab.Entry.Request == nil {
		s.errMsg = mockT(a, "mock.msg.no_active_request")
		return
	}

	red := a.buildMockRedactor(a.ps.project)
	rule, err := mockserver.BuildRuleFromRequest(tab.Entry.Request, tab.Resp, mockserver.BuildRuleOptions{
		Priority:     100,
		IncludeQuery: true,
		Redactor:     red,
	})
	if err != nil {
		s.errMsg = err.Error()
		return
	}

	relPath, err := a.upsertRuleIntoWorkspaceMockFile(rule)
	if err != nil {
		s.errMsg = err.Error()
		return
	}
	a.reloadMockCatalogLocked(s)
	if s.errMsg == "" {
		s.notice = mockTf(a, "mock.msg.rule_created", rule.ID, relPath)
		a.status = s.notice
	}
}

// CreateMockRuleFromActiveOpenAPI builds a MockRule from the active request's
// OpenAPI response schema, validating the synthesized example through A03.
func (a *App) CreateMockRuleFromActiveOpenAPI() {
	if a == nil || a.ps == nil || a.ps.project == nil {
		return
	}
	s := a.mockState(true)
	a.ensureMockProjectBound(s)
	tab := a.ps.activeTab()
	if tab == nil || tab.Kind != "request" || tab.Entry == nil || tab.Entry.Request == nil {
		s.errMsg = mockT(a, "mock.msg.no_active_request")
		return
	}

	rule, err := mockserver.BuildRuleFromOperationContract(tab.Entry.Request, "200", 100, s.seedInt())
	if err != nil {
		s.errMsg = err.Error()
		return
	}

	relPath, err := a.upsertRuleIntoWorkspaceMockFile(rule)
	if err != nil {
		s.errMsg = err.Error()
		return
	}
	a.reloadMockCatalogLocked(s)
	if s.errMsg == "" {
		s.notice = mockTf(a, "mock.msg.schema_rule_created", rule.ID, relPath)
		a.status = s.notice
	}
}

func (a *App) upsertRuleIntoWorkspaceMockFile(rule *mockserver.MockRule) (string, error) {
	p := a.ps.project
	rel := "mocks/" + defaultWorkspaceMockFileID + ".mock.yaml"
	var mf *mockserver.MockFile
	var baseline string
	if p.Store.Exists(rel) {
		loaded, err := mockserver.LoadMockFile(p, rel)
		if err != nil {
			return "", err
		}
		mf = loaded
		baseline = loaded.SourceDigest
	} else {
		mf = &mockserver.MockFile{
			SchemaVersion:   mockserver.MockSchemaVersion,
			ID:              defaultWorkspaceMockFileID,
			Name:            "Workspace Mock Rules",
			DefaultScenario: mockserver.DefaultScenario,
			SourceRelPath:   rel,
		}
	}

	replaced := false
	for i := range mf.Rules {
		if mf.Rules[i].ID == rule.ID {
			mf.Rules[i] = *rule
			replaced = true
			break
		}
	}
	if !replaced {
		mf.Rules = append(mf.Rules, *rule)
	}

	if _, err := mockserver.SaveMockFile(p, mf, baseline); err != nil {
		return "", err
	}
	return mf.SourceRelPath, nil
}

// StartControlledMockSuite executes the selected `.suite.yaml` against an
// isolated, snapshot-frozen loopback Mock Server.
func (a *App) StartControlledMockSuite() {
	if a == nil || a.ps == nil || a.win == nil {
		return
	}
	s := a.mockState(true)
	a.ensureMockProjectBound(s)
	if s.suiteRunning {
		return
	}
	if strings.TrimSpace(s.selectedSuite) == "" {
		s.errMsg = mockT(a, "mock.suite.empty")
		return
	}

	p, err := core.OpenProject(a.ps.root)
	if err != nil {
		s.errMsg = err.Error()
		return
	}
	suite, err := runner.LoadSuite(p, s.selectedSuite)
	if err != nil {
		s.errMsg = err.Error()
		return
	}

	frozenScenario := s.scenario
	frozenSeed := s.seedInt()
	targetVar := strings.TrimSpace(s.targetVar)
	if targetVar == "" {
		targetVar = "baseUrl"
	}

	snap, err := mockserver.CompileProjectMocks(p, frozenScenario, frozenSeed, mockserver.ServerLimits{})
	if err != nil {
		s.errMsg = err.Error()
		return
	}
	if len(snap.Rules) == 0 {
		s.errMsg = mockT(a, "mock.rules.empty")
		return
	}

	tab := &Tab{Owner: a.ps}
	netOpts := a.buildExecuteOptionsForTab(a.ps, tab, nil, nil, nil)
	netOpts.EnvID = s.suiteEnv

	ctx, cancel := context.WithCancel(context.Background())
	s.suiteGen++
	gen := s.suiteGen
	key := weak.Make(a)
	win := a.win

	var outcomeHolder struct {
		sync.Mutex
		outcome *mockserver.SuiteMockRunOutcome
		err     error
	}

	mailbox := &runnerMailbox{
		post: win.Update,
		deliver: func(events []runner.Event, result *runner.RunResult, finished bool) {
			owner := key.Value()
			if owner == nil || owner.closed {
				return
			}
			st := owner.mockState(false)
			if st == nil || st.suiteGen != gen {
				return
			}
			st.suiteEvents = append(st.suiteEvents, events...)
			if len(st.suiteEvents) > runnerEventLimit {
				st.suiteEvents = append([]runner.Event(nil), st.suiteEvents[len(st.suiteEvents)-runnerEventLimit:]...)
			}
			if finished {
				st.suiteRunning = false
				if st.suiteCancel != nil {
					st.suiteCancel()
					st.suiteCancel = nil
				}
				outcomeHolder.Lock()
				out := outcomeHolder.outcome
				runErr := outcomeHolder.err
				outcomeHolder.Unlock()
				if out == nil && result != nil {
					out = &mockserver.SuiteMockRunOutcome{Result: result, ServerStopped: true}
				}
				st.suiteOutcome = out
				if runErr != nil {
					st.errMsg = runErr.Error()
				} else if out != nil && out.Result != nil {
					sum := out.Result.Summary
					st.notice = mockTf(
						owner,
						"mock.suite.status_done",
						out.Result.SuiteName,
						string(out.Result.Status),
						sum.Passed,
						sum.Failed,
						sum.Skipped,
						out.MockBaseURL,
						len(out.AccessRecords),
					)
				}
			}
		},
	}

	secrets := &runnerSecrets{source: a.mgrSecrets(), values: core.MapSecrets{}}
	for _, e := range p.Environments {
		for _, name := range e.Secrets {
			secrets.Get(name)
		}
	}
	files := &runnerFiles{source: core.NewDirFileOpener(p.Dir), files: map[string]runnerFile{}}
	if err := freezeRunnerTLS(ctx, &netOpts, files); err != nil {
		cancel()
		s.errMsg = err.Error()
		return
	}
	netOpts.CookieJar = nil
	netOpts.LocalFiles = nil
	netOpts.Secrets = secrets
	netOpts.Interactive = false
	netOpts.Confirmed = false
	netOpts.ProjectFiles = files

	runOpts := runner.Options{
		Environment:     s.suiteEnv,
		Secrets:         secrets,
		ExecuteOptions:  netOpts,
		PlanOptions:     core.PlanOptions{ProjectRoot: p.Dir, ProjectFiles: files, MaxUploadBytes: core.DefaultMaxUploadBytes},
		Schema:          runner.NewProjectSchemaAdapter(p),
		Emit:            func(e runner.Event) { mailbox.publish(&e, nil, false) },
		Seed:            frozenSeed,
		StepConcurrency: 1,
		RowConcurrency:  1,
	}
	mockCfg := mockserver.SuiteMockConfig{
		Port:        0,
		Scenario:    frozenScenario,
		Seed:        frozenSeed,
		EndpointVar: targetVar,
		Snapshot:    snap,
	}

	s.suiteCancel = cancel
	s.suiteRunning = true
	s.suiteEvents = nil
	s.suiteOutcome = nil
	s.errMsg = ""
	s.notice = mockTf(a, "mock.suite.status_running", suite.Name, frozenScenario)

	go func() {
		out, runErr := mockserver.RunSuiteWithControlledMock(ctx, p, suite, mockCfg, runOpts)
		outcomeHolder.Lock()
		outcomeHolder.outcome = out
		outcomeHolder.err = runErr
		outcomeHolder.Unlock()
		var res *runner.RunResult
		if out != nil {
			res = out.Result
		}
		mailbox.publish(nil, res, true)
	}()
}

// CancelControlledMockSuite cancels an active controlled Mock Suite execution.
func (a *App) CancelControlledMockSuite() {
	if s := a.mockState(false); s != nil && s.suiteCancel != nil {
		s.suiteCancel()
	}
}

func (a *App) buildMockRedactor(p *core.Project) *core.Redactor {
	red := core.NewRedactor()
	if a == nil {
		return red
	}
	sec := a.mgrSecrets()
	if sec == nil {
		return red
	}
	if p != nil {
		for _, env := range p.Environments {
			if env == nil {
				continue
			}
			for _, name := range env.Secrets {
				if v, ok := sec.Get(strings.TrimSpace(name)); ok && v != "" {
					red.RegisterSecret(v)
				}
			}
		}
	}
	return red
}
