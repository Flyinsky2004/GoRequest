package desktop

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"gorequest/internal/core"
	"gorequest/internal/desktop/uikit"
)

// conflictSyntaxThreshold caps syntax highlighting in conflict/diff editors so
// large Markdown or YAML buffers degrade cleanly to plain text without frame
// stalls.
const conflictSyntaxThreshold = 512 << 10

var (
	fileSyncMu          sync.Mutex
	fileSyncControllers = map[*projectState]*fileSyncController{}
)

// fileSyncController binds one projectState's open editor tabs to Core's
// SyncEngine and ProjectWatcher, and holds the state of the three-version
// conflict resolution modal. Keeping it in a dedicated B01 module avoids
// modifying shared app.go / project_state.go / manager.go structs.
type fileSyncController struct {
	ps     *projectState
	engine *core.SyncEngine

	watcherMu sync.Mutex
	watcher   *core.ProjectWatcher

	tabBufferIDs map[*Tab]string
	nextBufSeq   uint64

	// Three-version conflict modal state (UI thread only).
	modalOpen     bool
	modalTab      *Tab
	modalConflict *core.SyncConflict
	modalMode     int // 0: Three Versions, 1: Baseline ↔ Disk, 2: Disk ↔ Edited Merge
	modalErr      string
	modalNote     string
	modalNoteWarn bool

	baseEditor    *editor.State
	diskEditor    *editor.State
	mergeEditor   *editor.State
	baseDiskDiff  *editor.DiffState
	diskMergeDiff *editor.DiffState
}

// peekFileSync returns the existing fileSyncController for ps, or nil without
// allocating or performing disk I/O. View functions use peekFileSync so idle
// workspaces incur zero filesystem overhead.
func peekFileSync(ps *projectState) *fileSyncController {
	if ps == nil {
		return nil
	}
	fileSyncMu.Lock()
	ctrl := fileSyncControllers[ps]
	fileSyncMu.Unlock()
	return ctrl
}

// ensureFileSync returns or lazily initializes the fileSyncController for a.ps.
func (a *App) ensureFileSync() (*fileSyncController, error) {
	if a == nil || a.ps == nil || a.ps.project == nil {
		return nil, fmt.Errorf("no active project")
	}
	ps := a.ps
	fileSyncMu.Lock()
	if existing := fileSyncControllers[ps]; existing != nil {
		fileSyncMu.Unlock()
		return existing, nil
	}
	fileSyncMu.Unlock()

	ctrl := &fileSyncController{
		ps:           ps,
		tabBufferIDs: map[*Tab]string{},
	}
	eng, err := ps.project.NewSyncEngine(core.SyncOptions{
		OpenBuffersFn: func() []core.OpenBuffer {
			return ctrl.collectOpenBuffers()
		},
	})
	if err != nil {
		return nil, err
	}
	ctrl.engine = eng

	fileSyncMu.Lock()
	if raced := fileSyncControllers[ps]; raced != nil {
		fileSyncMu.Unlock()
		return raced, nil
	}
	fileSyncControllers[ps] = ctrl
	fileSyncMu.Unlock()
	return ctrl, nil
}

// InitFileSync initializes the project's SyncEngine baseline snapshot without
// starting a background watcher goroutine (useful for deterministic synchronous
// testing and manual SyncNow passes).
func (a *App) InitFileSync() error {
	_, err := a.ensureFileSync()
	return err
}

// StartFileSync starts background filesystem monitoring for this workspace's
// project, coalescing external write storms via core.ProjectWatcher and routing
// settled updates onto the window's UI thread via win.Update.
func (a *App) StartFileSync(opts ...core.WatchOptions) error {
	ctrl, err := a.ensureFileSync()
	if err != nil {
		return err
	}
	var wopts core.WatchOptions
	if len(opts) > 0 {
		wopts = opts[0]
	}
	ps := a.ps
	win := a.win

	ctrl.stopWatcher()
	w, err := core.StartWatcher(context.Background(), ps.project.Store, wopts, func() {
		if win == nil || win.IsDestroyed() {
			// Lifecycle cleanup closes the watcher on the UI thread. Closing it
			// from its own notification goroutine would wait for itself.
			return
		}
		win.Update(func() {
			if a.closed || win.IsDestroyed() || a.ps != ps {
				a.StopFileSync()
				return
			}
			_, _ = a.SyncNow()
		})
	})
	if err != nil {
		return err
	}
	ctrl.watcherMu.Lock()
	ctrl.watcher = w
	ctrl.watcherMu.Unlock()
	return nil
}

// StopFileSync stops any active background ProjectWatcher for this workspace
// and releases controller state if the window has closed.
func (a *App) StopFileSync() {
	if a == nil || a.ps == nil {
		return
	}
	ps := a.ps
	ctrl := peekFileSync(ps)
	if ctrl == nil {
		return
	}
	ctrl.stopWatcher()
	if a.closed || (a.win != nil && a.win.IsDestroyed()) {
		fileSyncMu.Lock()
		if fileSyncControllers[ps] == ctrl {
			delete(fileSyncControllers, ps)
		}
		fileSyncMu.Unlock()
	}
}

// StopAllFileSync stops every active watcher across all workspaces (used for
// clean shutdown or test teardown).
func StopAllFileSync() {
	fileSyncMu.Lock()
	list := make([]*fileSyncController, 0, len(fileSyncControllers))
	for _, c := range fileSyncControllers {
		list = append(list, c)
	}
	fileSyncControllers = map[*projectState]*fileSyncController{}
	fileSyncMu.Unlock()

	for _, c := range list {
		c.stopWatcher()
	}
}

// FileSyncWatcherStats returns the watcher diagnostics for this workspace.
func (a *App) FileSyncWatcherStats() core.WatcherStats {
	if a == nil {
		return core.WatcherStats{}
	}
	ctrl := peekFileSync(a.ps)
	if ctrl == nil {
		return core.WatcherStats{}
	}
	ctrl.watcherMu.Lock()
	w := ctrl.watcher
	ctrl.watcherMu.Unlock()
	if w == nil {
		return core.WatcherStats{}
	}
	return w.Stats()
}

func (c *fileSyncController) stopWatcher() {
	if c == nil {
		return
	}
	c.watcherMu.Lock()
	w := c.watcher
	c.watcher = nil
	c.watcherMu.Unlock()
	if w != nil {
		_ = w.Close()
	}
}

func (c *fileSyncController) acknowledgeWatcher(paths ...string) {
	if c == nil {
		return
	}
	c.watcherMu.Lock()
	w := c.watcher
	c.watcherMu.Unlock()
	if w != nil {
		w.AcknowledgeCurrent(paths...)
	}
}

func (c *fileSyncController) recordLocalSave(relPath string, data []byte) {
	if c == nil || c.engine == nil {
		return
	}
	c.engine.RecordLocalSave(relPath, data)
	// Acknowledge only the path we just wrote so an unrelated concurrent external
	// change elsewhere in the tree is still detected on the next scan.
	c.acknowledgeWatcher(relPath)
}

func (c *fileSyncController) bufferIDForTab(tab *Tab) string {
	if tab == nil {
		return ""
	}
	if id, ok := c.tabBufferIDs[tab]; ok {
		return id
	}
	var id string
	switch tab.Kind {
	case "request":
		if tab.Entry != nil && tab.Entry.Request != nil && tab.Entry.Request.ID != "" {
			id = "req:" + tab.Entry.Request.ID
		} else {
			c.nextBufSeq++
			id = "reqbuf:" + strconv.FormatUint(c.nextBufSeq, 10)
		}
	case "doc":
		c.nextBufSeq++
		id = "docbuf:" + strconv.FormatUint(c.nextBufSeq, 10) + ":" + tab.DocRel
	default:
		c.nextBufSeq++
		id = "buf:" + strconv.FormatUint(c.nextBufSeq, 10)
	}
	c.tabBufferIDs[tab] = id
	return id
}

func (c *fileSyncController) clearTabState(tab *Tab) {
	if c == nil || tab == nil {
		return
	}
	if id, ok := c.tabBufferIDs[tab]; ok {
		if c.engine != nil {
			c.engine.ClearConflict(id)
		}
		delete(c.tabBufferIDs, tab)
	}
	if c.modalTab == tab {
		c.closeModal()
	}
}

func (c *fileSyncController) pruneClosedTabs() {
	if c == nil || c.ps == nil {
		return
	}
	openSet := make(map[*Tab]bool, len(c.ps.tabs))
	for _, t := range c.ps.tabs {
		openSet[t] = true
	}
	for t, id := range c.tabBufferIDs {
		if !openSet[t] {
			if c.engine != nil {
				c.engine.ClearConflict(id)
			}
			delete(c.tabBufferIDs, t)
			if c.modalTab == t {
				c.closeModal()
			}
		}
	}
}

func (c *fileSyncController) openBufferForTab(tab *Tab) (core.OpenBuffer, bool) {
	if c == nil || c.ps == nil || tab == nil {
		return core.OpenBuffer{}, false
	}
	bufID := c.bufferIDForTab(tab)
	switch tab.Kind {
	case "request":
		if tab.Entry == nil || tab.Entry.Request == nil {
			return core.OpenBuffer{}, false
		}
		rel := tab.Entry.RelPath
		baseBytes := baselineBytesForTab(c.ps, c, tab)
		return core.OpenBuffer{
			BufferID:       bufID,
			EntityKind:     core.EntityRequest,
			EntityID:       tab.Entry.Request.ID,
			RelPath:        rel,
			Dirty:          tab.Dirty,
			BaselineDigest: tab.Baseline,
			BaselineBytes:  baseBytes,
			EditedBytes: func() ([]byte, error) {
				return editedBytesForTab(c.ps, c, tab)
			},
		}, true

	case "doc":
		if tab.DocRel == "" {
			return core.OpenBuffer{}, false
		}
		baseBytes := baselineBytesForTab(c.ps, c, tab)
		return core.OpenBuffer{
			BufferID:       bufID,
			EntityKind:     core.EntityDoc,
			RelPath:        tab.DocRel,
			Dirty:          tab.Dirty,
			BaselineDigest: tab.Baseline,
			BaselineBytes:  baseBytes,
			EditedBytes: func() ([]byte, error) {
				return editedBytesForTab(c.ps, c, tab)
			},
		}, true
	}
	return core.OpenBuffer{}, false
}

func (c *fileSyncController) collectOpenBuffers() []core.OpenBuffer {
	if c == nil || c.ps == nil {
		return nil
	}
	c.pruneClosedTabs()
	out := make([]core.OpenBuffer, 0, len(c.ps.tabs))
	for _, tab := range c.ps.tabs {
		if ob, ok := c.openBufferForTab(tab); ok {
			out = append(out, ob)
		}
	}
	return out
}

// baselineBytesForTab returns the cached raw bytes of tab's reading baseline,
// falling back to encoding tab.Entry.Request if the sync engine was lazily
// created after an external edit already replaced the file on disk.
func baselineBytesForTab(ps *projectState, ctrl *fileSyncController, tab *Tab) []byte {
	if tab == nil {
		return nil
	}
	rel := tab.DocRel
	if tab.Kind == "request" && tab.Entry != nil {
		rel = tab.Entry.RelPath
	}
	if ctrl != nil && ctrl.engine != nil {
		if b := ctrl.engine.BaselineBytes(rel, tab.Baseline); len(b) > 0 {
			return b
		}
	}
	if tab.Kind == "request" && tab.Entry != nil && tab.Entry.Request != nil {
		if b, err := core.EncodeRequestAgainstBaseline(nil, tab.Entry.Request); err == nil {
			return b
		}
	}
	return nil
}

// requestForSaveFromTab uses the shared Body/Auth editors, preserving their
// explicit inheritance, references and unsaved edits.
func requestForSaveFromTab(_ *projectState, tab *Tab) (*core.Request, error) {
	return requestFromTab(tab)
}

func editedBytesForTab(ps *projectState, ctrl *fileSyncController, tab *Tab) ([]byte, error) {
	if tab == nil {
		return nil, fmt.Errorf("nil tab")
	}
	// Sync live bodyEditor buffer if user typed in body code view on a prior frame.
	if tab.Kind == "request" && tab.bodyEditor != nil && tab.bodyBuiltIdx == tab.BodyIdx {
		if s := tab.bodyEditor.Buffer.String(); s != tab.BodyText {
			tab.BodyText = s
			tab.Dirty = true
		}
	}
	if tab.Kind == "doc" {
		if tab.DocEditor == nil {
			return nil, nil
		}
		return []byte(tab.DocEditor.Buffer.String()), nil
	}
	baseBytes := baselineBytesForTab(ps, ctrl, tab)
	req, err := requestForSaveFromTab(ps, tab)
	if err == nil {
		return core.EncodeRequestAgainstBaseline(baseBytes, req)
	}
	fallback := bestEffortRequestFromTab(tab)
	if fallback == nil {
		return nil, err
	}
	return core.EncodeRequestAgainstBaseline(baseBytes, fallback)
}

func bestEffortRequestFromTab(tab *Tab) *core.Request {
	if tab == nil || tab.Entry == nil || tab.Entry.Request == nil {
		return nil
	}
	base := *tab.Entry.Request
	httpDef := &core.HTTPDef{
		Method:  tab.Method,
		URL:     tab.URL,
		Query:   bestEffortKV(tab.QueryRows),
		Headers: bestEffortKV(tab.HeaderRows),
	}
	if strings.TrimSpace(tab.BodyText) != "" {
		btype := "text"
		if tab.BodyIdx >= 0 && tab.BodyIdx < len(bodyTypes) {
			btype = bodyTypes[tab.BodyIdx]
		}
		if body, err := core.ParseBodyText(btype, tab.BodyText); err == nil {
			httpDef.Body = body
		} else if rawBody, berr := core.ParseBodyText("text", tab.BodyText); berr == nil {
			httpDef.Body = rawBody
		}
	}
	base.HTTP = httpDef
	base.Timeout = strings.TrimSpace(tab.Timeout)
	if auth, err := authRefFromTab(tab); err == nil {
		base.Auth = auth
	}

	return &base
}

func bestEffortKV(rows []*KVRow) []core.KV {
	var out []core.KV
	for i, r := range rows {
		if r == nil || r.isBlank() {
			continue
		}
		name := r.Name
		if strings.TrimSpace(name) == "" {
			name = fmt.Sprintf("_incomplete_row_%d", i+1)
		}
		var enabled *bool
		if !r.Enabled {
			f := false
			enabled = &f
		}
		out = append(out, core.KV{Name: name, Value: r.Value, Enabled: enabled})
	}
	return out
}

// ConflictForTab returns a copy of the active three-version conflict for tab,
// or nil when tab is not in conflict.
func (a *App) ConflictForTab(tab *Tab) *core.SyncConflict {
	if a == nil || a.ps == nil || tab == nil {
		return nil
	}
	ctrl := peekFileSync(a.ps)
	if ctrl == nil || ctrl.engine == nil {
		return nil
	}
	id, ok := ctrl.tabBufferIDs[tab]
	if !ok {
		return nil
	}
	return ctrl.engine.ConflictFor(id)
}

// SyncNow runs one synchronous filesystem scan and reconciles clean project
// entities and open tabs while isolating dirty conflicting tabs into
// three-version SyncConflicts. It never writes to disk or sends network
// requests.
func (a *App) SyncNow() (*core.SyncReport, error) {
	ctrl, err := a.ensureFileSync()
	if err != nil {
		return nil, err
	}
	ps := a.ps
	tabByBuf := make(map[string]*Tab, len(ps.tabs))
	for _, t := range ps.tabs {
		tabByBuf[ctrl.bufferIDForTab(t)] = t
	}

	report, err := ctrl.engine.ScanAndSync()
	if err != nil {
		return nil, err
	}
	ctrl.acknowledgeWatcher()

	// Reconcile selected environment ONLY if its file was genuinely deleted from
	// disk. If the file still exists on disk with Git conflict markers or syntax
	// errors, keep ps.envSel pointing at it so CanSendTab blocks sending instead
	// of silently switching environments!
	if ps.envSel != "" && ps.envSel != "(none)" {
		if ps.project.Environments[ps.envSel] == nil && !envStillPresentOnDisk(ps.project, ps.envSel) {
			if len(ps.project.EnvOrder) > 0 {
				ps.envSel = ps.project.EnvOrder[0]
			} else {
				ps.envSel = "(none)"
			}
		}
	}

	var firstConflictPath string
	for _, act := range report.BufferActions {
		tab := tabByBuf[act.BufferID]
		if tab == nil || ps.indexOfTab(tab) < 0 {
			continue
		}
		switch act.Action {
		case core.BufferActionRefreshClean:
			if tab.Kind == "request" {
				refreshCleanRequestTab(ps, tab)
				if act.NewPath != "" {
					ps.expandChain(filepath.ToSlash(filepath.Dir(act.NewPath)))
				}
			} else if tab.Kind == "doc" {
				if ps.sel == "doc:"+tab.DocRel && act.NewPath != "" {
					ps.sel = "doc:" + act.NewPath
				}
				content, dig, rerr := ps.project.Store.ReadBounded(act.NewPath, ps.project.Store.EffectiveLimits().MaxDocBytes)
				if rerr == nil {
					refreshCleanDocTab(tab, act.NewPath, dig, content)
				}
			}

		case core.BufferActionFollowRename:
			if tab.Kind == "request" {
				if !tab.Dirty && act.NewDigest != "" {
					tab.Baseline = act.NewDigest
				}
				if act.NewPath != "" {
					ps.expandChain(filepath.ToSlash(filepath.Dir(act.NewPath)))
				}
			} else if tab.Kind == "doc" && act.NewPath != "" {
				if ps.sel == "doc:"+tab.DocRel {
					ps.sel = "doc:" + act.NewPath
				}
				tab.DocRel = act.NewPath
				tab.Title = filepath.Base(act.NewPath)
				if !tab.Dirty && act.NewDigest != "" {
					tab.Baseline = act.NewDigest
				}
			}

		case core.BufferActionCloseDeleted:
			if ps.sel == tabIdentity(tab) {
				ps.sel = ""
			}
			ctrl.clearTabState(tab)
			ps.removeTab(tab)

		case core.BufferActionConflict:
			if tab.Kind == "doc" && act.NewPath != "" && tab.DocRel != act.NewPath {
				if ps.sel == "doc:"+tab.DocRel {
					ps.sel = "doc:" + act.NewPath
				}
				tab.DocRel = act.NewPath
				tab.Title = filepath.Base(act.NewPath)
			}
			if firstConflictPath == "" {
				firstConflictPath = act.NewPath
				if firstConflictPath == "" {
					firstConflictPath = act.OldPath
				}
			}
			if ctrl.modalOpen && ctrl.modalTab == tab && act.Conflict != nil {
				ctrl.refreshModalDiskSide(act.Conflict)
			}
		}
	}

	// Update workspace status only when external changes actually happened.
	if len(report.Events) > 0 || len(report.BufferActions) > 0 {
		fm := a.fileSyncMsg()
		switch {
		case firstConflictPath != "":
			a.status = translate(fm.StatusConflict, firstConflictPath)
		case len(report.Events) == 1 && report.Events[0].Kind == core.SyncRenamed:
			ev := report.Events[0]
			a.status = translate(fm.StatusRenamed, ev.OldPath, ev.Path)
		case len(report.Events) == 1 && report.Events[0].Kind == core.SyncDeleted:
			a.status = translate(fm.StatusDeleted, report.Events[0].Path)
		default:
			a.status = translate(fm.StatusRefreshed, len(report.Events))
		}
	}

	return report, nil
}

func refreshCleanRequestTab(ps *projectState, tab *Tab) {
	if ps == nil || tab == nil || tab.Entry == nil || tab.Entry.Request == nil {
		return
	}
	fillTabFromRequest(tab, tab.Entry, ps.project)
	tab.Baseline = tab.Entry.BaselineDigest
	tab.Dirty = false
	tab.bodyEditor = nil
	tab.queryText = false
	tab.headerText = false
	tab.kvErr = ""
}

func refreshCleanDocTab(tab *Tab, relPath, digest string, content []byte) {
	if tab == nil {
		return
	}
	tab.DocRel = relPath
	tab.Title = filepath.Base(relPath)
	tab.Baseline = digest
	tab.Dirty = false
	st := &editor.State{
		Buffer: editor.NewBuffer(string(content)),
	}
	if len(content) <= conflictSyntaxThreshold {
		st.Buffer.SetLanguage(editor.LanguageNamed("markdown"))
	}
	tab.DocEditor = st
}

// saveTabSynced saves tab using comment-preserving YAML merge (for requests) or
// baseline-checked Markdown save (for docs), recording local writes in the
// SyncEngine and capturing a three-version SyncConflict if disk diverged or was
// deleted externally.
func (a *App) saveTabSynced(tab *Tab) error {
	if a == nil || a.ps == nil || tab == nil {
		return fmt.Errorf("no tab to save")
	}
	ctrl, err := a.ensureFileSync()
	if err != nil {
		return err
	}
	ps := a.ps
	bufID := ctrl.bufferIDForTab(tab)
	rel := tabRelPath(tab)

	// Block blind overwrite or resurrection while an unresolved conflict exists.
	if existing := ctrl.engine.ConflictFor(bufID); existing != nil {
		if existing.RelPath != "" {
			rel = existing.RelPath
		}
		return &core.ConflictError{
			Path:     rel,
			Baseline: existing.BaselineDigest,
			Disk:     existing.DiskDigest,
		}
	}

	if tab.Kind == "request" {
		req, rerr := requestForSaveFromTab(ps, tab)
		if rerr != nil {
			return rerr
		}
		newDigest, serr := ps.project.SaveRequestPreserving(tab.Entry, req, tab.Baseline)
		if serr != nil {
			if core.IsConflict(serr) {
				_, _ = ctrl.captureTabSaveConflict(tab)
			}
			return serr
		}
		tab.Baseline = newDigest
		tab.Dirty = false
		if savedBytes, _, berr := ps.project.Store.ReadBounded(tab.Entry.RelPath, ps.project.Store.EffectiveLimits().MaxYAMLBytes); berr == nil {
			ctrl.recordLocalSave(tab.Entry.RelPath, savedBytes)
		}
		return nil
	}

	if tab.DocEditor == nil {
		return fmt.Errorf("document has no editor buffer")
	}
	content := []byte(tab.DocEditor.Buffer.String())
	newDigest, serr := ps.project.SaveDoc(tab.DocRel, content, tab.Baseline)
	if serr != nil {
		if core.IsConflict(serr) {
			_, _ = ctrl.captureTabSaveConflict(tab)
		}
		return serr
	}
	tab.Baseline = newDigest
	tab.DocEditor.Buffer.MarkSaved()
	tab.Dirty = false
	ctrl.recordLocalSave(tab.DocRel, content)
	return nil
}

func (c *fileSyncController) captureTabSaveConflict(tab *Tab) (*core.SyncConflict, error) {
	if c == nil || c.engine == nil || tab == nil {
		return nil, fmt.Errorf("sync controller unavailable")
	}
	ob, ok := c.openBufferForTab(tab)
	if !ok {
		return nil, fmt.Errorf("unsupported tab kind %q", tab.Kind)
	}
	conflict, err := c.engine.CaptureSaveConflict(ob)
	if err == nil {
		c.acknowledgeWatcher(ob.RelPath)
	}
	return conflict, err
}

// saveActiveSynced saves the active tab via saveTabSynced, preserving the
// existing workspace status wording while opening the three-version conflict
// modal whenever an external modification or deletion blocks saving.
func (a *App) saveActiveSynced() {
	if a == nil || a.ps == nil {
		return
	}
	tab := a.ps.activeTab()
	if tab == nil {
		return
	}
	rel := tabRelPath(tab)
	if err := a.saveTabSynced(tab); err != nil {
		m := a.msg()
		if core.IsConflict(err) {
			if c := a.ConflictForTab(tab); c != nil && c.RelPath != "" {
				rel = c.RelPath
			}
			a.status = translate(m.Conflict, rel)
			a.OpenConflictModal(tab)
		} else {
			a.status = translate(m.SaveFailedPrefix, err.Error())
		}
		return
	}
	a.status = translate(a.msg().Saved, tabRelPath(tab))
}

// AutoSaveTick safely saves clean-baseline dirty tabs on an auto-save timer
// while strictly skipping conflicted tabs and externally modified/deleted files
// so auto-save never overwrites external edits and never resurrects deleted
// files.
func (a *App) AutoSaveTick() (saved int, skipped int) {
	if a == nil || a.ps == nil {
		return 0, 0
	}
	ctrl, err := a.ensureFileSync()
	if err != nil {
		return 0, 0
	}
	for _, tab := range a.ps.tabs {
		if tab == nil || !tab.Dirty {
			continue
		}
		bufID := ctrl.bufferIDForTab(tab)
		rel := tabRelPath(tab)
		if !ctrl.engine.CanAutoSave(bufID, rel, tab.Baseline) {
			if ctrl.engine.ConflictFor(bufID) == nil {
				_, _ = ctrl.captureTabSaveConflict(tab)
			}
			skipped++
			continue
		}
		if err := a.saveTabSynced(tab); err != nil {
			skipped++
			continue
		}
		saved++
	}
	return saved, skipped
}

// HasGitConflictForSend checks whether sending tab must be blocked due to
// unresolved Git merge conflict markers in project.yaml, the active
// environment, referenced auth, the request file on disk, or the tab's edited
// fields.
func (a *App) HasGitConflictForSend(tab *Tab) (string, bool) {
	if a == nil || a.ps == nil || a.ps.project == nil || tab == nil || tab.Kind != "request" {
		return "", false
	}
	ps := a.ps
	if c := a.ConflictForTab(tab); c != nil && c.HasGitConflict {
		return c.RelPath, true
	}
	reqRel := tabRelPath(tab)
	envRel := ""
	if envID := ps.currentEnv(); envID != "" {
		if ee, ok := ps.project.EnvEntries[envID]; ok && ee != nil {
			envRel = ee.RelPath
		} else {
			envRel = "environments/" + envID + ".yaml"
		}
	}
	for _, iss := range ps.project.Issues {
		if iss.Code != "git-conflict" {
			continue
		}
		if iss.Path == "project.yaml" || iss.Path == reqRel || (envRel != "" && iss.Path == envRel) || strings.HasPrefix(iss.Path, "auth/") {
			return iss.Path, true
		}
	}
	if core.HasGitConflictMarkers([]byte(tab.URL)) || core.HasGitConflictMarkers([]byte(tab.BodyText)) {
		return reqRel, true
	}
	return "", false
}

// CanSendTab validates that tab is not blocked by unresolved Git merge conflict
// markers before network execution.
func (a *App) CanSendTab(tab *Tab) error {
	if path, blocked := a.HasGitConflictForSend(tab); blocked {
		msg := translate(a.fileSyncMsg().ErrGitConflictSend, path)
		a.status = msg
		return fmt.Errorf("%s", msg)
	}
	return nil
}

func tabRelPath(tab *Tab) string {
	if tab == nil {
		return ""
	}
	if tab.Kind == "request" && tab.Entry != nil {
		return tab.Entry.RelPath
	}
	return tab.DocRel
}

func envStillPresentOnDisk(p *core.Project, envID string) bool {
	if p == nil || p.Store == nil || envID == "" {
		return false
	}
	if p.Store.Exists("environments/"+envID+".yaml") || p.Store.Exists("environments/"+envID+".yml") {
		return true
	}
	for _, iss := range p.Issues {
		if strings.HasPrefix(iss.Path, "environments/") &&
			(iss.Path == "environments/"+envID+".yaml" || iss.Path == "environments/"+envID+".yml") {
			return true
		}
	}
	return false
}

// --- Three-Version Conflict Modal Controller -----------------------------

// OpenConflictModal opens the three-version comparison and merge modal for a
// conflicted tab.
func (a *App) OpenConflictModal(tab *Tab) {
	if a == nil || a.ps == nil || tab == nil {
		return
	}
	ctrl, err := a.ensureFileSync()
	if err != nil {
		return
	}
	bufID := ctrl.bufferIDForTab(tab)
	c := ctrl.engine.ConflictFor(bufID)
	if c == nil {
		return
	}
	// Refresh EditedBytes from current tab fields before opening so any edits
	// typed after the conflict first arrived are included in the merge pane.
	if latestEdited, eerr := editedBytesForTab(a.ps, ctrl, tab); eerr == nil && len(latestEdited) > 0 {
		c.EditedBytes = latestEdited
	}
	ctrl.openModalWithConflict(tab, c, nil)
}

// CloseConflictModal dismisses the conflict modal while keeping the tab's
// three-version conflict protection and warning banner active.
func (a *App) CloseConflictModal() {
	if a == nil || a.ps == nil {
		return
	}
	if ctrl := peekFileSync(a.ps); ctrl != nil {
		ctrl.closeModal()
	}
}

func (c *fileSyncController) closeModal() {
	if c == nil {
		return
	}
	c.modalOpen = false
	c.modalTab = nil
	c.modalConflict = nil
	c.modalErr = ""
	c.modalNote = ""
	c.modalNoteWarn = false
	c.baseEditor = nil
	c.diskEditor = nil
	c.mergeEditor = nil
	c.baseDiskDiff = nil
	c.diskMergeDiff = nil
}

func conflictLangForKind(kind core.SyncEntityKind) *editor.Language {
	if kind == core.EntityDoc {
		return editor.LanguageNamed("markdown")
	}
	return editor.LanguageNamed("yaml")
}

func newConflictEditorState(text string, lang *editor.Language, readOnly bool, th *ui.Theme) *editor.State {
	buf := editor.NewBuffer(text)
	if lang != nil && len(text) <= conflictSyntaxThreshold {
		buf.SetLanguage(lang)
	}
	var edTheme *editor.Theme
	if th != nil {
		edTheme = uikit.EditorTheme(th)
	}
	return &editor.State{
		Buffer:   buf,
		ReadOnly: readOnly,
		Theme:    edTheme,
	}
}

func (c *fileSyncController) openModalWithConflict(tab *Tab, conflict *core.SyncConflict, th *ui.Theme) {
	lang := conflictLangForKind(conflict.EntityKind)
	baseText := string(conflict.BaselineBytes)
	diskText := string(conflict.DiskBytes)
	editText := string(conflict.EditedBytes)

	c.modalOpen = true
	c.modalTab = tab
	c.modalConflict = conflict
	c.modalMode = 0
	c.modalErr = ""
	c.modalNote = ""
	c.modalNoteWarn = false

	c.baseEditor = newConflictEditorState(baseText, lang, true, th)
	c.diskEditor = newConflictEditorState(diskText, lang, true, th)
	c.mergeEditor = newConflictEditorState(editText, lang, false, th)

	var edTheme *editor.Theme
	if th != nil {
		edTheme = uikit.EditorTheme(th)
	}
	c.baseDiskDiff = &editor.DiffState{
		Old:             c.baseEditor.Buffer,
		New:             c.diskEditor.Buffer,
		Layout:          editor.DiffSplit,
		Editable:        false,
		ExpandUnchanged: true,
		Theme:           edTheme,
	}
	c.diskMergeDiff = &editor.DiffState{
		Old:             c.diskEditor.Buffer,
		New:             c.mergeEditor.Buffer,
		Layout:          editor.DiffSplit,
		Editable:        true,
		HunkActions:     true,
		ExpandUnchanged: true,
		Theme:           edTheme,
	}
}

func (c *fileSyncController) refreshModalDiskSide(updated *core.SyncConflict) {
	if c == nil || updated == nil {
		return
	}
	c.modalConflict = updated.Clone()
	if c.diskEditor != nil && c.diskEditor.Buffer != nil {
		c.diskEditor.Buffer.Reset(string(updated.DiskBytes))
	}
}

// RunThreeWayMergeInModal computes a line-level 3-way merge of Baseline,
// Edited, and Disk into the modal's editable merge buffer.
func (a *App) RunThreeWayMergeInModal() {
	if a == nil || a.ps == nil {
		return
	}
	ctrl := peekFileSync(a.ps)
	if ctrl == nil || !ctrl.modalOpen || ctrl.modalConflict == nil || ctrl.mergeEditor == nil {
		return
	}
	c := ctrl.modalConflict
	if !c.DiskExists {
		return
	}
	merged, clean := core.MergeThreeWay(c.BaselineBytes, c.EditedBytes, c.DiskBytes)
	ctrl.mergeEditor.Buffer.Set(string(merged))
	if ctrl.modalMode == 1 {
		ctrl.modalMode = 2
	}
	ctrl.modalErr = ""
	fm := a.fileSyncMsg()
	if clean {
		ctrl.modalNote = fm.MergeCleanNote
		ctrl.modalNoteWarn = false
	} else {
		ctrl.modalNote = fm.MergeOverlapNote
		ctrl.modalNoteWarn = true
	}
}

// ResolveTabConflict executes kind for tab's active conflict, updating the open
// tab(s), project tree, and modal state without losing either side on error.
func (a *App) ResolveTabConflict(tab *Tab, kind core.ResolutionKind) error {
	if a == nil || a.ps == nil || tab == nil {
		return fmt.Errorf("no conflicted tab")
	}
	ctrl, err := a.ensureFileSync()
	if err != nil {
		return err
	}
	ps := a.ps
	bufID := ctrl.bufferIDForTab(tab)
	conflictBefore := ctrl.engine.ConflictFor(bufID)
	if conflictBefore == nil {
		return fmt.Errorf("no active conflict for %s", tabRelPath(tab))
	}

	var override []byte
	switch kind {
	case core.ResolutionKeepEdited, core.ResolutionApplyMerged, core.ResolutionSaveAsCopy:
		if ctrl.modalOpen && ctrl.modalTab == tab && ctrl.mergeEditor != nil {
			override = []byte(ctrl.mergeEditor.Buffer.String())
		} else if b, berr := editedBytesForTab(ps, ctrl, tab); berr == nil {
			override = b
		} else {
			override = conflictBefore.EditedBytes
		}
	}

	res, err := ctrl.engine.ResolveConflict(bufID, kind, override)
	if err != nil {
		if latest := ctrl.engine.ConflictFor(bufID); latest != nil && ctrl.modalOpen && ctrl.modalTab == tab {
			ctrl.refreshModalDiskSide(latest)
		}
		if ctrl.modalOpen && ctrl.modalTab == tab {
			ctrl.modalErr = err.Error()
		}
		a.status = translate(a.msg().SaveFailedPrefix, err.Error())
		return err
	}

	ctrl.acknowledgeWatcher()
	if ctrl.modalTab == tab {
		ctrl.closeModal()
	}

	fm := a.fileSyncMsg()
	switch kind {
	case core.ResolutionAcceptDelete:
		if ps.sel == tabIdentity(tab) {
			ps.sel = ""
		}
		ctrl.clearTabState(tab)
		ps.removeTab(tab)
		a.status = translate(fm.StatusDeleted, res.RelPath)

	case core.ResolutionReloadDisk, core.ResolutionKeepEdited, core.ResolutionApplyMerged:
		if tab.Kind == "request" {
			refreshCleanRequestTab(ps, tab)
		} else if tab.Kind == "doc" {
			refreshCleanDocTab(tab, res.RelPath, res.NewDigest, res.SavedBytes)
		}
		a.status = translate(fm.StatusResolved, res.RelPath)

	case core.ResolutionSaveAsCopy:
		if conflictBefore.DiskExists {
			if tab.Kind == "request" {
				refreshCleanRequestTab(ps, tab)
			} else if tab.Kind == "doc" {
				if diskBytes, dig, rerr := ps.project.Store.ReadBounded(conflictBefore.RelPath, ps.project.Store.EffectiveLimits().MaxDocBytes); rerr == nil {
					refreshCleanDocTab(tab, conflictBefore.RelPath, dig, diskBytes)
				}
			}
		} else {
			ctrl.clearTabState(tab)
			ps.removeTab(tab)
		}
		if res.CopyRequest != nil {
			ps.expandChain(filepath.ToSlash(filepath.Dir(res.CopyRequest.RelPath)))
			ps.openRequest(res.CopyRequest)
			ps.sel = "req:" + res.CopyRequest.Request.ID
		} else if res.CopyDoc != nil {
			_ = ps.openDoc(res.CopyDoc.RelPath)
			ps.sel = "doc:" + res.CopyDoc.RelPath
		}
		a.status = translate(fm.StatusSavedCopy, res.RelPath)
	}

	return nil
}
