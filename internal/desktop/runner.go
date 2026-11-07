package desktop

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"weak"

	"gorequest/internal/core"
	"gorequest/internal/runner"
)

const runnerEventLimit = 256

// runnerState has no App, window, Tab, projectState or other UI object pointer.
// The weak-key registry mirrors the existing OpenAPI extension lifecycle.
// Every field is read/written only on the owning window's UI thread.
type runnerState struct {
	open, running                              bool
	mode                                       int
	collection, suite, environment, exportPath string
	collections, suites, environments          []string
	notice, exportNotice, snapshotRoot         string
	events                                     []runner.Event
	result                                     *runner.RunResult
	cancel                                     context.CancelFunc
	generation                                 uint64
}

var runnerStates = struct {
	sync.Mutex
	byWindow map[weak.Pointer[App]]*runnerState
}{byWindow: map[weak.Pointer[App]]*runnerState{}}

func (a *App) runnerState(create bool) *runnerState {
	if a == nil {
		return nil
	}
	key := weak.Make(a)
	runnerStates.Lock()
	defer runnerStates.Unlock()
	if s := runnerStates.byWindow[key]; s != nil {
		return s
	}
	if !create {
		return nil
	}
	s := &runnerState{}
	runnerStates.byWindow[key] = s
	runtime.AddCleanup(a, func(k weak.Pointer[App]) {
		runnerStates.Lock()
		delete(runnerStates.byWindow, k)
		runnerStates.Unlock()
	}, key)
	if a.win != nil {
		a.win.OnClosed(func() {
			if s.cancel != nil {
				s.cancel()
			}
			s.generation++
			runnerStates.Lock()
			delete(runnerStates.byWindow, key)
			runnerStates.Unlock()
		})
	}
	return s
}

func (a *App) OpenRunner() {
	if a == nil || a.ps == nil {
		return
	}
	s := a.runnerState(true)
	s.open = true
	if s.running {
		return
	}
	s.environment = a.ps.currentEnv()
	s.collections = []string{"collections"}
	s.suites = nil
	s.environments = []string{""}
	var walk func([]*core.CollectionEntry)
	walk = func(list []*core.CollectionEntry) {
		for _, c := range list {
			s.collections = append(s.collections, c.RelPath)
			walk(c.Children)
		}
	}
	walk(a.ps.project.Collections)
	s.environments = append(s.environments, a.ps.project.EnvOrder...)
	if len(s.collections) > 0 {
		s.collection = s.collections[0]
	}
	// Only files with an explicit Suite suffix are offered; LoadSuite remains
	// the authoritative parser. An empty list is a visible empty state.
	files, err := a.ps.project.Store.List(".", func(name string) bool {
		return strings.HasSuffix(name, ".suite.yaml") || strings.HasSuffix(name, ".suite.yml")
	})
	if err == nil {
		s.suites = files
		if len(files) > 0 {
			s.suite = files[0]
		}
	}
	s.notice = ""
}

// runnerMailbox bounds both pending UI events and scheduled Window.Update
// callbacks. A slow/hidden window never accumulates one callback per event.
// Final results are separate from the bounded progress ring and never dropped.
type runnerMailbox struct {
	mu                  sync.Mutex
	events              []runner.Event
	final               *runner.RunResult
	finished, scheduled bool
	post                func(func())
	deliver             func([]runner.Event, *runner.RunResult, bool)
}

func (m *runnerMailbox) publish(e *runner.Event, result *runner.RunResult, finished bool) {
	m.mu.Lock()
	if e != nil {
		if len(m.events) == runnerEventLimit {
			copy(m.events, m.events[1:])
			m.events = m.events[:runnerEventLimit-1]
		}
		m.events = append(m.events, *e)
	}
	if finished {
		m.final = result
		m.finished = true
	}
	schedule := !m.scheduled
	m.scheduled = true
	m.mu.Unlock()
	if schedule {
		m.post(func() {
			m.mu.Lock()
			events, result, finished := m.events, m.final, m.finished
			m.events = nil
			m.final = nil
			m.finished = false
			m.scheduled = false
			m.mu.Unlock()
			m.deliver(events, result, finished)
		})
	}
}
func (s *runnerState) appendEvents(events []runner.Event) {
	s.events = append(s.events, events...)
	if len(s.events) > runnerEventLimit {
		s.events = append([]runner.Event(nil), s.events[len(s.events)-runnerEventLimit:]...)
	}
}

// StartRunner captures saved disk state, environment, B04 options and B05
// credentials before the worker starts. No goroutine receives App/Tab pointers.
func (a *App) StartRunner() {
	s := a.runnerState(true)
	if s.running || a.ps == nil || a.win == nil {
		return
	}
	p, err := core.OpenProject(a.ps.root)
	if err != nil {
		s.reject(a.runnerMsg().SnapshotFailed, a.ps.root)
		return
	}
	var suite *runner.Suite
	if s.mode == 0 {
		suite, err = desktopRunnerCollection(p, s.collection)
	} else {
		suite, err = runner.LoadSuite(p, s.suite)
	}
	if err != nil {
		s.reject(a.runnerMsg().SelectionFailed, a.ps.root)
		return
	}
	// Network helper is called only here, on the UI thread, with a real temporary
	// Tab. Its returned settings are copied; all providers are consumed/sealed by
	// prepareDesktopRunner before execution moves to the background.
	tab := &Tab{Owner: a.ps}
	net := a.buildExecuteOptionsForTab(a.ps, tab, nil, nil, nil)
	net.EnvID = s.environment
	// MergeTargetPolicy owns fresh policy slices/maps. Clone options containing
	// byte slices/pointers in prepareDesktopRunner's TLS snapshot routine.
	ctx, cancel := context.WithCancel(context.Background())
	s.generation++
	generation := s.generation
	key := weak.Make(a)
	win := a.win
	mailbox := &runnerMailbox{post: win.Update, deliver: func(events []runner.Event, result *runner.RunResult, finished bool) {
		owner := key.Value()
		if owner == nil || owner.closed {
			return
		}
		state := owner.runnerState(false)
		if state == nil || state.generation != generation {
			return
		}
		state.appendEvents(events)
		if finished {
			state.result = result
			state.running = false
			if state.cancel != nil {
				state.cancel()
				state.cancel = nil
			}
			if result != nil {
				state.notice = string(result.Status)
			} else {
				state.notice = owner.runnerMsg().SnapshotFailed
			}
		}
	}}
	r, err := prepareDesktopRunner(ctx, p, suite, s.environment, net, a.mgrSecrets(), func(e runner.Event) { mailbox.publish(&e, nil, false) })
	if err != nil {
		cancel()
		s.reject(err.Error(), a.ps.root)
		return
	}
	s.cancel = cancel
	s.running = true
	s.result = nil
	s.events = nil
	s.exportNotice = ""
	s.notice = a.runnerMsg().Running
	s.snapshotRoot = a.ps.root
	go func() { result, _ := r.Run(ctx); mailbox.publish(nil, result, true) }()
}
func (a *App) CancelRunner() {
	if s := a.runnerState(false); s != nil && s.cancel != nil {
		s.cancel()
	}
}

func runnerSummaryText(r *runner.RunResult) string {
	if r == nil {
		return ""
	}
	s := r.Summary
	return fmt.Sprintf("%s · rows %d · passed %d · failed %d · config %d · internal %d · canceled %d · skipped %d · attempts %d · retries %d", r.Status, s.Rows, s.Passed, s.Failed, s.ConfigErrors, s.InternalErrors, s.Canceled, s.Skipped, s.Attempts, s.Retries)
}

// writeRunnerReport writes only an explicit destination outside the captured
// project. Directory symlinks are resolved before confinement checks; a final
// symlink is rejected. Temp + rename avoids partial reports and uses 0600.
func writeRunnerReport(result *runner.RunResult, destination, projectRoot string, junit bool) error {
	if result == nil || !filepath.IsAbs(destination) {
		return errors.New("choose an absolute local report path")
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(destination))
	if err != nil {
		return err
	}
	root, err := filepath.EvalSymlinks(projectRoot)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(root, parent)
	if err != nil {
		return err
	}
	if rel == "." || (!strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != "..") {
		return errors.New("choose a report location outside this project")
	}
	target := filepath.Join(parent, filepath.Base(destination))
	if info, err := os.Lstat(target); err == nil && (info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular()) {
		return errors.New("report destination must be a regular file")
	}
	var b []byte
	if junit {
		b, err = runner.MarshalJUnitReport(result)
	} else {
		b, err = runner.MarshalJSONReport(result)
	}
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(parent, ".gorequest-report-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(b)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(tmp, target)
}

// The root selection includes all saved requests. CollectionSuite still owns
// deterministic ordering and dependency construction; the synthetic collection
// exists only in this run's private project view.
func desktopRunnerCollection(p *core.Project, rel string) (*runner.Suite, error) {
	if rel != "collections" {
		return runner.CollectionSuite(p, rel)
	}
	copyProject := *p
	root := &core.CollectionEntry{RelPath: "collections", Name: "All saved requests"}
	for _, e := range p.Requests {
		root.Requests = append(root.Requests, e)
	}
	copyProject.Collections = []*core.CollectionEntry{root}
	return runner.CollectionSuite(&copyProject, rel)
}
func (s *runnerState) reject(message, root string) {
	s.notice = message
	s.events = nil
	s.snapshotRoot = root
	s.result = &runner.RunResult{FormatVersion: 1, Environment: s.environment, Status: runner.StatusConfigError, Failure: &runner.Failure{Category: runner.FailureConfig, Kind: runner.KindDefinition, Message: message}}
}
