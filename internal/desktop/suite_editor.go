package desktop

// Suite editor state and actions (A02 phase 3). The editor is a window-local
// draft over internal/suiteedit; it never parses or executes a suite itself.
// All fields are read/written only on the owning window's UI thread. The weak
// App key registry mirrors runnerState/runnerEditorState so two project windows
// never share a draft.

import (
	"fmt"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"weak"

	"gorequest/internal/core"
	"gorequest/internal/runner"
	"gorequest/internal/suiteedit"
)

type suiteEditorState struct {
	open bool

	draft       *suiteedit.Draft
	originalSig string

	suites        []string
	suitesSkipped int

	selectedRow string

	maxRowsText  string
	maxBytesText string

	preview    *runner.DatasetPreview
	previewErr string

	err    string
	notice string

	confirmDiscard bool
	pending        func()
}

var suiteEditorStates = struct {
	sync.Mutex
	byWindow map[weak.Pointer[App]]*suiteEditorState
}{byWindow: map[weak.Pointer[App]]*suiteEditorState{}}

func (a *App) suiteEditorState(create bool) *suiteEditorState {
	if a == nil {
		return nil
	}
	key := weak.Make(a)
	suiteEditorStates.Lock()
	defer suiteEditorStates.Unlock()
	if s := suiteEditorStates.byWindow[key]; s != nil {
		return s
	}
	if !create {
		return nil
	}
	s := &suiteEditorState{}
	suiteEditorStates.byWindow[key] = s
	runtime.AddCleanup(a, func(k weak.Pointer[App]) {
		suiteEditorStates.Lock()
		delete(suiteEditorStates.byWindow, k)
		suiteEditorStates.Unlock()
	}, key)
	if a.win != nil {
		a.win.OnClosed(func() {
			suiteEditorStates.Lock()
			delete(suiteEditorStates.byWindow, key)
			suiteEditorStates.Unlock()
		})
	}
	return s
}

func (s *suiteEditorState) reset() {
	if s == nil {
		return
	}
	s.open = false
	s.draft = nil
	s.originalSig = ""
	s.suites = nil
	s.suitesSkipped = 0
	s.selectedRow = ""
	s.maxRowsText = ""
	s.maxBytesText = ""
	s.preview = nil
	s.previewErr = ""
	s.err = ""
	s.notice = ""
	s.confirmDiscard = false
	s.pending = nil
}

func (s *suiteEditorState) dirty() bool {
	if s == nil || s.draft == nil {
		return false
	}
	return s.draft.Signature() != s.originalSig
}

// OpenSuiteEditor opens the editor for the current project and loads the first
// saved suite when one exists. An empty project starts a new draft.
func (a *App) OpenSuiteEditor() {
	if a == nil || a.ps == nil || a.ps.project == nil {
		return
	}
	s := a.suiteEditorState(true)
	if s == nil || s.open {
		return
	}
	// A draft retained by navigation to the assertion/extraction editor resumes
	// exactly as it was; only a clean close clears it.
	if s.draft != nil {
		s.open = true
		s.confirmDiscard = false
		s.pending = nil
		a.refreshSuiteEditorList()
		return
	}
	s.reset()
	s.open = true
	a.refreshSuiteEditorList()
	if len(s.suites) > 0 {
		a.loadSuiteEditorNow(s.suites[0])
	} else {
		a.newSuiteEditorNow()
	}
}

// CloseSuiteEditor closes the editor. A dirty draft asks for confirmation first.
func (a *App) CloseSuiteEditor() {
	s := a.suiteEditorState(false)
	if s == nil {
		return
	}
	if s.dirty() {
		s.confirmDiscard = true
		s.pending = func() { s.reset() }
		return
	}
	s.reset()
}

// DiscardSuiteEditorDraft confirms the pending discard/close action.
func (a *App) DiscardSuiteEditorDraft() {
	s := a.suiteEditorState(false)
	if s == nil {
		return
	}
	pending := s.pending
	s.confirmDiscard = false
	s.pending = nil
	if pending != nil {
		pending()
		return
	}
	s.reset()
}

// KeepSuiteEditorDraft cancels the discard confirmation and keeps editing.
func (a *App) KeepSuiteEditorDraft() {
	s := a.suiteEditorState(false)
	if s == nil {
		return
	}
	s.confirmDiscard = false
	s.pending = nil
}

// LoadSuiteEditor loads a saved suite, asking first when the current draft is
// dirty.
func (a *App) LoadSuiteEditor(rel string) {
	s := a.suiteEditorState(false)
	if s == nil {
		return
	}
	if s.dirty() {
		s.confirmDiscard = true
		s.pending = func() { a.loadSuiteEditorNow(rel) }
		return
	}
	a.loadSuiteEditorNow(rel)
}

func (a *App) loadSuiteEditorNow(rel string) {
	s := a.suiteEditorState(false)
	if s == nil || a.ps == nil || a.ps.project == nil {
		return
	}
	d, err := suiteedit.Load(a.ps.project, rel)
	if err != nil {
		s.err = err.Error()
		return
	}
	s.draft = d
	s.originalSig = d.Signature()
	s.selectedRow = ""
	if len(d.Steps) > 0 {
		s.selectedRow = d.Steps[0].RowID
	}
	s.syncDatasetText()
	s.preview = nil
	s.previewErr = ""
	s.err = ""
	s.notice = ""
	s.confirmDiscard = false
	s.pending = nil
}

// NewSuiteEditor starts a new draft, asking first when the current one is dirty.
func (a *App) NewSuiteEditor() {
	s := a.suiteEditorState(false)
	if s == nil {
		return
	}
	if s.dirty() {
		s.confirmDiscard = true
		s.pending = func() { a.newSuiteEditorNow() }
		return
	}
	a.newSuiteEditorNow()
}

func (a *App) newSuiteEditorNow() {
	s := a.suiteEditorState(false)
	if s == nil || a.ps == nil || a.ps.project == nil {
		return
	}
	d := suiteedit.NewDraft(a.uniqueSuiteID(), "")
	s.draft = d
	s.originalSig = d.Signature()
	s.selectedRow = ""
	s.maxRowsText = ""
	s.maxBytesText = ""
	s.preview = nil
	s.previewErr = ""
	s.err = ""
	s.notice = ""
	s.confirmDiscard = false
	s.pending = nil
}

func (a *App) uniqueSuiteID() string {
	base := "suite"
	for i := 1; i < 1000; i++ {
		id := base
		if i > 1 {
			id = fmt.Sprintf("%s-%d", base, i)
		}
		rel, err := suiteedit.SuiteRelPath(id)
		if err != nil {
			continue
		}
		if a.ps == nil || a.ps.project == nil || !a.ps.project.Store.Exists(rel) {
			return id
		}
	}
	return base
}

func (a *App) refreshSuiteEditorList() {
	s := a.suiteEditorState(false)
	if s == nil || a.ps == nil || a.ps.project == nil {
		return
	}
	files, skipped, err := suiteedit.ListSuites(a.ps.project, suiteedit.MaxSuiteScan)
	if err != nil {
		s.err = err.Error()
		return
	}
	s.suites = files
	s.suitesSkipped = skipped
}

// AddSuiteStep appends a new step with a unique default id.
func (a *App) AddSuiteStep() {
	s := a.suiteEditorState(false)
	if s == nil || s.draft == nil {
		return
	}
	id := a.uniqueStepID()
	st := s.draft.AddStep(id)
	s.selectedRow = st.RowID
	s.err = ""
}

func (a *App) uniqueStepID() string {
	s := a.suiteEditorState(false)
	used := map[string]bool{}
	if s != nil && s.draft != nil {
		for _, st := range s.draft.Steps {
			used[st.ID] = true
		}
	}
	for i := 1; i < 10000; i++ {
		id := fmt.Sprintf("step%d", i)
		if !used[id] {
			return id
		}
	}
	return "step"
}

// DeleteSuiteStep deletes a step only when no dependency or template reference
// would be broken.
func (a *App) DeleteSuiteStep(rowID string) {
	s := a.suiteEditorState(false)
	if s == nil || s.draft == nil || a.ps == nil || a.ps.project == nil {
		return
	}
	if err := s.draft.CanDeleteStep(a.ps.project, rowID); err != nil {
		s.err = err.Error()
		return
	}
	idx := s.draft.StepIndex(rowID)
	s.draft.RemoveStep(idx)
	if s.selectedRow == rowID {
		s.selectedRow = ""
		if len(s.draft.Steps) > 0 {
			if idx >= len(s.draft.Steps) {
				idx = len(s.draft.Steps) - 1
			}
			s.selectedRow = s.draft.Steps[idx].RowID
		}
	}
	s.err = ""
}

// MoveSuiteStep reorders a step without touching its fields.
func (a *App) MoveSuiteStep(rowID string, dir int) {
	s := a.suiteEditorState(false)
	if s == nil || s.draft == nil {
		return
	}
	idx := s.draft.StepIndex(rowID)
	if idx < 0 {
		return
	}
	s.draft.MoveStep(idx, dir)
}

func (a *App) SelectSuiteStep(rowID string) {
	s := a.suiteEditorState(false)
	if s == nil {
		return
	}
	s.selectedRow = rowID
}

// OpenRunnerEditorForSuiteStep navigates to the existing assertion/extraction
// editor for the selected step's request. The suite draft is hidden, not reset,
// so reopening the Suite editor resumes it. A dirty assertion-editor draft is
// never discarded by this navigation.
func (a *App) OpenRunnerEditorForSuiteStep(st *suiteedit.StepDraft) {
	s := a.suiteEditorState(false)
	if s == nil || s.draft == nil || st == nil || a.ps == nil || a.ps.project == nil {
		return
	}
	entry, err := suiteedit.ResolveRequest(a.ps.project, st.RequestRef)
	if err != nil {
		s.err = err.Error()
		return
	}
	if re := a.runnerEditorState(false); re != nil && re.open && re.dirty() {
		s.err = a.runnerEditorMsg().PendingEdits
		return
	}
	s.open = false
	a.OpenRunnerEditor()
	a.SetRunnerEditorRequest(entry.Request.ID)
}

// SetSuiteStepRequest selects a saved HTTP request. Choosing the request the
// step already resolves to leaves the stored reference untouched, so opening and
// saving never rewrites a path reference to an id.
func (a *App) SetSuiteStepRequest(rowID, requestID string) {
	s := a.suiteEditorState(false)
	if s == nil || s.draft == nil || a.ps == nil || a.ps.project == nil {
		return
	}
	idx := s.draft.StepIndex(rowID)
	if idx < 0 {
		return
	}
	var chosen *core.RequestEntry
	for _, opt := range suiteedit.RequestOptions(a.ps.project) {
		if opt.Entry != nil && opt.Entry.Request != nil && opt.Entry.Request.ID == requestID {
			if !opt.Selectable {
				s.err = opt.Reason
				return
			}
			chosen = opt.Entry
			break
		}
	}
	if chosen == nil {
		s.err = fmt.Sprintf("request %q was not found", requestID)
		return
	}
	st := s.draft.Steps[idx]
	if cur, err := suiteedit.ResolveRequest(a.ps.project, st.RequestRef); err == nil && cur.Request.ID == chosen.Request.ID {
		s.err = ""
		return
	}
	st.RequestRef = chosen.Request.ID
	s.err = ""
}

// ToggleSuiteStepNeed adds or removes one dependency.
func (a *App) ToggleSuiteStepNeed(rowID, needID string) {
	s := a.suiteEditorState(false)
	if s == nil || s.draft == nil {
		return
	}
	idx := s.draft.StepIndex(rowID)
	if idx < 0 {
		return
	}
	st := s.draft.Steps[idx]
	if needID == st.ID {
		s.err = "a step cannot depend on itself"
		return
	}
	for i, dep := range st.Needs {
		if dep == needID {
			st.Needs = append(st.Needs[:i], st.Needs[i+1:]...)
			s.err = ""
			return
		}
	}
	st.Needs = append(st.Needs, needID)
	s.err = ""
}

// SetSuiteDatasetMode switches the dataset between none/csv/json.
func (a *App) SetSuiteDatasetMode(mode int) {
	s := a.suiteEditorState(false)
	if s == nil || s.draft == nil {
		return
	}
	switch mode {
	case 0:
		s.draft.Dataset.Enabled = false
	case 1:
		s.draft.Dataset.Enabled = true
		s.draft.Dataset.Format = "csv"
	case 2:
		s.draft.Dataset.Enabled = true
		s.draft.Dataset.Format = "json"
	}
	s.preview = nil
	s.previewErr = ""
}

func (s *suiteEditorState) syncDatasetText() {
	if s == nil || s.draft == nil {
		return
	}
	s.maxRowsText = ""
	s.maxBytesText = ""
	if s.draft.Dataset.MaxRows > 0 {
		s.maxRowsText = strconv.Itoa(s.draft.Dataset.MaxRows)
	}
	if s.draft.Dataset.MaxBytes > 0 {
		s.maxBytesText = strconv.FormatInt(s.draft.Dataset.MaxBytes, 10)
	}
}

func (s *suiteEditorState) syncDatasetNumbers() error {
	if s == nil || s.draft == nil || !s.draft.Dataset.Enabled {
		return nil
	}
	rows, err := parseOptionalInt(s.maxRowsText)
	if err != nil {
		return fmt.Errorf("maxRows: %w", err)
	}
	bytes, err := parseOptionalInt64(s.maxBytesText)
	if err != nil {
		return fmt.Errorf("maxBytes: %w", err)
	}
	s.draft.Dataset.MaxRows = rows
	s.draft.Dataset.MaxBytes = bytes
	return nil
}

func parseOptionalInt(text string) (int, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(text)
	if err != nil {
		return 0, fmt.Errorf("%q is not an integer", text)
	}
	if n < 0 {
		return 0, fmt.Errorf("%q must not be negative", text)
	}
	return n, nil
}

func parseOptionalInt64(text string) (int64, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return 0, nil
	}
	n, err := strconv.ParseInt(text, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%q is not an integer", text)
	}
	if n < 0 {
		return 0, fmt.Errorf("%q must not be negative", text)
	}
	return n, nil
}

// PreviewSuiteDataset validates and previews the current dataset form.
func (a *App) PreviewSuiteDataset() {
	s := a.suiteEditorState(false)
	if s == nil || s.draft == nil || a.ps == nil || a.ps.project == nil {
		return
	}
	s.preview = nil
	s.previewErr = ""
	if err := s.syncDatasetNumbers(); err != nil {
		s.previewErr = err.Error()
		return
	}
	pv, err := suiteedit.PreviewDataset(a.ps.project, s.draft, 0, 0)
	if err != nil {
		s.previewErr = err.Error()
		return
	}
	s.preview = pv
}

// SaveSuiteEditor validates and writes the draft. A failure (including an
// external-edit conflict) leaves the draft exactly as it was.
func (a *App) SaveSuiteEditor() {
	s := a.suiteEditorState(false)
	if s == nil || s.draft == nil || a.ps == nil || a.ps.project == nil {
		return
	}
	m := a.suiteEditorMsg()
	s.err = ""
	s.notice = ""
	if err := s.syncDatasetNumbers(); err != nil {
		s.err = err.Error()
		return
	}
	if !s.dirty() {
		s.notice = m.NothingToSave
		return
	}
	rel, _, err := suiteedit.Save(a.ps.project, s.draft)
	if err != nil {
		if core.IsConflict(err) {
			s.err = m.SaveConflict + " " + err.Error()
		} else {
			s.err = err.Error()
		}
		return
	}
	s.originalSig = s.draft.Signature()
	s.notice = m.Saved
	a.refreshSuiteEditorList()
	a.refreshRunnerSuiteSelection(rel)
}

// refreshRunnerSuiteSelection updates an already-open Runner modal so its saved
// suite list includes and selects the just-saved file. It never reopens the
// project or touches tabs, responses or the environment.
func (a *App) refreshRunnerSuiteSelection(rel string) {
	rs := a.runnerState(false)
	if rs == nil || a.ps == nil || a.ps.project == nil {
		return
	}
	files, _, err := suiteedit.ListSuites(a.ps.project, 0)
	if err != nil {
		return
	}
	rs.suites = files
	rs.suite = rel
	// Make the refreshed selection visible without disturbing an active run.
	if !rs.running {
		rs.mode = 1
	}
}
