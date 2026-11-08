package desktop

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"

	"gorequest/internal/core"
	"gorequest/internal/desktop/uikit"
	"gorequest/internal/localstate"
)

// Request history (§3): local only, never in the project directory or Git.
//
// The on-disk engine and the record model live in internal/localstate so they
// stay pure Go and reusable by a future Runner. This file adapts one project
// window to that engine: it derives the per-clone key, turns a plan/result or
// an execution error into a redacted record, and renders the panel.

// historyRecord is the local history model. Keeping this alias means existing
// callers (and old JSONL files) keep working while the engine owns the format.
type historyRecord = localstate.Record

// historyStore is the desktop-side adapter for one history directory. It owns a
// single engine instance so concurrent appends from one process are serialized,
// and it applies the limits configured by the application.
type historyStore struct {
	dir    string
	mu     sync.Mutex
	engine *localstate.Store
	limits localstate.Limits
}

func defaultHistoryDir() string {
	base, err := os.UserConfigDir()
	if err != nil || base == "" {
		return ""
	}
	return filepath.Join(base, "GoRequest", "history")
}

func newHistoryStore() *historyStore { return &historyStore{dir: defaultHistoryDir()} }

func (s *historyStore) get() *localstate.Store {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.engine == nil {
		s.engine = localstate.NewStore(s.dir, s.limits)
	}
	return s.engine
}

// configure updates the retention/size limits used by later writes.
func (s *historyStore) configure(l localstate.Limits) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.limits = l
	if s.engine == nil {
		s.engine = localstate.NewStore(s.dir, l)
		return
	}
	s.engine.SetLimits(l)
}

// load returns records for key, newest first. It is kept for the existing
// metadata-only callers; richer callers use loadFiltered.
func (s *historyStore) load(key string) []historyRecord {
	res, _ := s.get().Load(key)
	return res.Records
}

func (s *historyStore) loadFiltered(key string, f localstate.Filter) (localstate.LoadResult, error) {
	return s.get().LoadFiltered(key, f)
}

func (s *historyStore) loadAll(f localstate.Filter) (localstate.AllResult, error) {
	return s.get().LoadAll(f)
}

func (s *historyStore) append(key string, r historyRecord) error {
	_, err := s.get().Append(key, r)
	return err
}

func (s *historyStore) appendRecord(key string, r historyRecord) (localstate.AppendResult, error) {
	return s.get().Append(key, r)
}

func (s *historyStore) remove(key string, ts time.Time) {
	_, _ = s.get().Remove(key, "", ts)
}

func (s *historyStore) removeRecord(key, id string, ts time.Time) (bool, error) {
	return s.get().Remove(key, id, ts)
}

func (s *historyStore) clear(key string) error { return s.get().Clear(key) }

func (s *historyStore) cleanup(key string) (localstate.CleanupResult, error) {
	return s.get().Cleanup(key)
}

// historyKey derives a per-project file key from the stable project id and a
// hash of the normalized root, so two clones of the same repo never share
// history. The shape matches the previous metadata-only store.
func historyKey(ps *projectState) string {
	if ps == nil {
		return ""
	}
	id := ""
	if ps.project != nil && ps.project.File != nil {
		id = ps.project.File.ID
	}
	return localstate.ProjectKey(id, ps.root)
}

// --- App integration -----------------------------------------------------

func (a *App) historyPolicy() localstate.Policy {
	p := localstate.DefaultPolicy()
	if a.mgr != nil && a.mgr.cfg != nil {
		if a.mgr.cfg.historyFullCapture() {
			return localstate.FullPolicy()
		}
		p.Snapshots = a.mgr.cfg.historySaveSnapshots()
		p.Bodies = p.Snapshots && a.mgr.cfg.historySaveBodies()
	}
	return p
}

func (a *App) historyLimits() localstate.Limits {
	l := localstate.DefaultLimits()
	if a.mgr != nil && a.mgr.cfg != nil {
		if a.mgr.cfg.historyFullCapture() {
			l = localstate.FullLimits()
		}
		days := a.mgr.cfg.historyRetentionDays()
		if days < 0 {
			l.Retention = -1 // keep until the record/byte cap removes it
		} else {
			l.Retention = time.Duration(days) * 24 * time.Hour
		}
	}
	return l
}

func (a *App) store() *historyStore {
	if a.mgr != nil && a.mgr.history != nil {
		a.mgr.history.configure(a.historyLimits())
		return a.mgr.history
	}
	return &historyStore{dir: ""} // disabled (tests or no config directory)
}

// recordHistory stores a completed send. The URL/headers/bodies are redacted by
// the shared B05 Redactor before they reach disk, and bodies are omitted unless
// the user opted in.
func (a *App) recordHistory(tab *Tab, plan *core.Plan, red *core.Redactor, res *core.ExecResult) {
	if tab == nil || tab.Entry == nil || tab.Entry.Request == nil || plan == nil || res == nil {
		return
	}
	s := a.store()
	if s.dir == "" {
		return
	}
	in := a.historyInput(tab, plan, red, localstate.OutcomeOK)
	in.Status = res.Status
	in.Proto = res.Proto
	in.Duration = res.Duration
	in.SizeBytes = res.BodySize
	in.ResponseHeaders = res.Headers
	in.ResponseBody = res.Body
	in.ResponseTruncated = res.Truncated
	rec := localstate.BuildRecord(in, a.historyPolicy())
	ares, err := s.appendRecord(historyKey(a.ps), rec)
	if err != nil {
		a.historyUI.diag = translate(a.msg().HistoryWriteFailed, err.Error())
		a.status = translate(a.msg().HistoryWriteFailed, err.Error())
		return
	}
	if a.historyView {
		a.reloadHistory()
	}
	if ares.CorruptLines > 0 {
		a.appendHistoryDiag(translate(a.msg().HistoryDiagCorrupt, ares.CorruptLines))
	}
	if ares.Trimmed {
		a.appendHistoryDiag(a.msg().HistorySnapshotTrimmed)
	}
}

// recordHistoryFailure stores a send that produced no response. Canceled and
// timed-out sends are distinguished from transport/config errors so the result
// filter is truthful; the error text is redacted like every other string.
func (a *App) recordHistoryFailure(tab *Tab, plan *core.Plan, red *core.Redactor, err, ctxErr error) {
	if tab == nil || tab.Entry == nil || tab.Entry.Request == nil || plan == nil || err == nil {
		return
	}
	s := a.store()
	if s.dir == "" {
		return
	}
	outcome := localstate.OutcomeError
	switch {
	case errors.Is(ctxErr, context.Canceled), errors.Is(err, context.Canceled):
		outcome = localstate.OutcomeCanceled
	case errors.Is(ctxErr, context.DeadlineExceeded), errors.Is(err, context.DeadlineExceeded):
		outcome = localstate.OutcomeTimeout
	case strings.Contains(strings.ToLower(err.Error()), "timed out after"):
		// core.Execute wraps its own deadline into a RunError, so the outer ctx
		// cannot reveal it; its stable message is the transport fact we have.
		outcome = localstate.OutcomeTimeout
	}
	in := a.historyInput(tab, plan, red, outcome)
	in.Error = err.Error()
	rec := localstate.BuildRecord(in, a.historyPolicy())
	ares, err := s.appendRecord(historyKey(a.ps), rec)
	if err != nil {
		a.historyUI.diag = translate(a.msg().HistoryWriteFailed, err.Error())
		a.status = translate(a.msg().HistoryWriteFailed, err.Error())
		return
	}
	if a.historyView {
		a.reloadHistory()
	}
	if ares.CorruptLines > 0 {
		a.appendHistoryDiag(translate(a.msg().HistoryDiagCorrupt, ares.CorruptLines))
	}
	if ares.Trimmed {
		a.appendHistoryDiag(a.msg().HistorySnapshotTrimmed)
	}
}

// historyInput is the reusable port the desktop fills from the current
// plan/ExecResult shape. A future Runner can populate the same localstate.HistoryInput
// fields without a second history model.
func (a *App) historyInput(tab *Tab, plan *core.Plan, red *core.Redactor, outcome string) localstate.HistoryInput {
	return localstate.HistoryInput{
		Ts:                 time.Now(),
		ProjectKey:         historyKey(a.ps),
		ProjectName:        a.ps.name(),
		RequestID:          tab.Entry.Request.ID,
		RequestName:        tab.Title,
		Environment:        a.ps.currentEnv(),
		Method:             plan.Method,
		URL:                effectivePlanURL(plan),
		Outcome:            outcome,
		RequestHeaders:     plan.Headers,
		RequestBody:        plan.Body,
		RequestHasBody:     plan.HasBody,
		RequestContentType: headerValue(plan.Headers, "Content-Type"),
		Redactor:           red,
		Policy:             a.historyPolicy(),
	}
}

// effectivePlanURL mirrors the URL Execute actually sends: plan.Query entries
// are appended to plan.URL. Storing only plan.URL would drop the query string
// from history.
func effectivePlanURL(plan *core.Plan) string {
	if plan == nil {
		return ""
	}
	if len(plan.Query) == 0 {
		return plan.URL
	}
	u, err := url.Parse(plan.URL)
	if err != nil {
		q := make(url.Values, len(plan.Query))
		for _, kv := range plan.Query {
			q.Add(kv[0], kv[1])
		}
		enc := q.Encode()
		if enc == "" {
			return plan.URL
		}
		base, frag, hasFrag := strings.Cut(plan.URL, "#")
		sep := "?"
		if strings.Contains(base, "?") {
			sep = "&"
		}
		if hasFrag {
			return base + sep + enc + "#" + frag
		}
		return base + sep + enc
	}
	q := u.Query()
	for _, kv := range plan.Query {
		q.Add(kv[0], kv[1])
	}
	u.RawQuery = q.Encode()
	return u.String()
}

func headerValue(headers [][2]string, name string) string {
	for _, kv := range headers {
		if strings.EqualFold(kv[0], name) {
			return kv[1]
		}
	}
	return ""
}

func (a *App) reloadHistory() {
	s := a.store()
	key := historyKey(a.ps)
	a.historyKey = key
	a.historyUI.diag = ""
	if s.dir == "" {
		a.historyRecords = nil
		a.historyUI.diag = a.msg().HistoryUnavailable
		return
	}
	s.configure(a.historyLimits())
	if a.historyUI.allProjects {
		all, err := s.loadAll(localstate.Filter{})
		a.historyRecords = all.Records
		if a.historyUI.projectKey != "" && !historyHasProject(all.Records, a.historyUI.projectKey) {
			a.historyUI.projectKey = ""
		}
		a.historyUI.diag = historyAllDiagnostics(all.Diagnostics, err, a.msg())
		return
	}
	// Load first so corrupt-line and read diagnostics reflect what was actually
	// on disk, then compact (retention/size/corrupt lines). A later refresh then
	// reads the compacted file without losing the first pass's diagnostics.
	res, err := s.loadFiltered(key, localstate.Filter{})
	a.historyUI.diag = historyDiagnostics(res.Diagnostics, err, a.msg())
	cleanupErr := error(nil)
	if err == nil {
		if _, cerr := s.cleanup(key); cerr != nil {
			cleanupErr = cerr
		} else if res2, err2 := s.loadFiltered(key, localstate.Filter{}); err2 == nil {
			res = res2
		}
	}
	a.historyRecords = res.Records
	if cleanupErr != nil {
		msg := translate(a.msg().HistoryReadFailed, cleanupErr.Error())
		if a.historyUI.diag == "" {
			a.historyUI.diag = msg
		} else {
			a.historyUI.diag += " · " + msg
		}
	}
}

func (a *App) clearHistory() {
	if err := a.store().clear(historyKey(a.ps)); err != nil {
		a.historyUI.diag = translate(a.msg().HistoryWriteFailed, err.Error())
		return
	}
	a.historyRecords = nil
	a.reloadHistory()
}

func (a *App) deleteHistory(r historyRecord) {
	key := historyKey(a.ps)
	if r.ProjectKey != "" {
		// In the all-project view the row may belong to another project, and its
		// record must be removed from that project's file, not the focused one.
		key = r.ProjectKey
	}
	if _, err := a.store().removeRecord(key, r.ID, r.Ts); err != nil {
		a.historyUI.diag = translate(a.msg().HistoryWriteFailed, err.Error())
		return
	}
	if a.historyUI.detailOpen && (a.historyUI.detail.ID == r.ID || a.historyUI.detail.Ts.Equal(r.Ts)) {
		a.historyUI.detailOpen = false
	}
	a.reloadHistory()
}

// restoreHistory reopens the recorded request (by id) and fills its URL/method
// from the record, as an edit the user can then inspect or resend. URLs are
// stored redacted, so restoring never puts a live secret back in the editor.
// A row from another project is opened in that project's window when it is open;
// it is never applied to the focused project.
func (a *App) restoreHistory(r historyRecord) {
	if a.ps == nil || a.ps.project == nil {
		return
	}
	if r.ProjectKey != "" && r.ProjectKey != historyKey(a.ps) {
		target := a.workspaceForKey(r.ProjectKey)
		if target == nil {
			a.status = a.msg().HistoryProjectNotOpen
			return
		}
		if e, ok := target.ps.project.Requests[r.RequestID]; ok && e.Request != nil {
			target.ps.openRequest(e)
			if tab := target.ps.activeTab(); tab != nil {
				tab.Method = r.Method
				tab.URL = r.URL
				tab.Dirty = true
			}
			target.historyView = false
			target.historyUI.detailOpen = false
			a.historyView = false
			a.historyUI.detailOpen = false
			target.win.Show()
			target.win.Focus()
			return
		}
		a.status = a.msg().HistoryRequestGone
		return
	}
	if e, ok := a.ps.project.Requests[r.RequestID]; ok && e.Request != nil {
		a.ps.openRequest(e)
	} else if a.ps.activeTab() == nil {
		a.status = a.msg().HistoryRequestGone
		return
	}
	tab := a.ps.activeTab()
	if tab == nil {
		return
	}
	tab.Method = r.Method
	tab.URL = r.URL
	tab.Dirty = true
	a.historyView = false
	a.historyUI.detailOpen = false
}

// workspaceForKey finds an open project window by its history key. It does not
// create or reopen a project, so a history row can never silently launch a
// different workspace.
func (a *App) workspaceForKey(key string) *App {
	if a.mgr == nil || key == "" {
		return nil
	}
	for _, app := range a.mgr.workspaces {
		if app == nil || app.closed || app.win == nil || app.win.IsDestroyed() {
			continue
		}
		if historyKey(app.ps) == key {
			return app
		}
	}
	return nil
}

// historyDiagnostics turns a load result into one short, user-facing line.
func historyDiagnostics(d localstate.Diagnostics, err error, m *messages) string {
	var parts []string
	if err != nil {
		parts = append(parts, translate(m.HistoryReadFailed, err.Error()))
	} else if d.ReadError != "" {
		parts = append(parts, translate(m.HistoryReadFailed, d.ReadError))
	}
	if d.CorruptLines > 0 {
		parts = append(parts, translate(m.HistoryDiagCorrupt, d.CorruptLines))
	}
	return strings.Join(parts, " · ")
}

// appendHistoryDiag adds one line to the panel's diagnostics without hiding a
// problem reported earlier in the same interaction.
func (a *App) appendHistoryDiag(msg string) {
	if strings.TrimSpace(msg) == "" {
		return
	}
	if a.historyUI.diag == "" {
		a.historyUI.diag = msg
		return
	}
	a.historyUI.diag += " · " + msg
}

func historyAllDiagnostics(diags []localstate.Diagnostics, err error, m *messages) string {
	if err != nil {
		return translate(m.HistoryReadFailed, err.Error())
	}
	var corrupt int
	var readErr string
	for _, d := range diags {
		corrupt += d.CorruptLines
		if readErr == "" && d.ReadError != "" {
			readErr = d.ReadError
		}
	}
	var parts []string
	if readErr != "" {
		parts = append(parts, translate(m.HistoryReadFailed, readErr))
	}
	if corrupt > 0 {
		parts = append(parts, translate(m.HistoryDiagCorrupt, corrupt))
	}
	return strings.Join(parts, " · ")
}

// historyUIState is this window's history view state: project scope, filters,
// diagnostics and the open detail preview. It lives on App as one field so no
// package-level map owns per-window state.
type historyUIState struct {
	allProjects bool
	// projectKey narrows the all-project view to one project; "" means all.
	projectKey string
	timeRange  int // 0 all, 1 today, 2 last 7 days, 3 last 30 days
	result     int // 0 all, 1 success, 2 client, 3 server, 4 network, 5 canceled
	query      string
	// endpointOnly shortens each row's URL to its path, query and fragment.
	// It is display state only: records, filters and restore keep the full
	// URL, and the row tooltip still shows it.
	endpointOnly bool

	diag string

	detailOpen bool
	detail     historyRecord
}

func (h historyUIState) filter(now time.Time) localstate.Filter {
	f := localstate.Filter{Query: h.query}
	if h.allProjects {
		f.ProjectKey = h.projectKey
	}
	switch h.timeRange {
	case 1:
		f.Since = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	case 2:
		f.Since = now.AddDate(0, 0, -7)
	case 3:
		f.Since = now.AddDate(0, 0, -30)
	}
	switch h.result {
	case 1:
		f.Result = "success"
	case 2:
		f.Result = "client"
	case 3:
		f.Result = "server"
	case 4:
		f.Result = "network"
	case 5:
		f.Result = "canceled"
	}
	return f
}

// visibleHistory applies the live filters to the already loaded scope. Disk is
// touched only when the scope or data changes, so typing in the filter box does
// not re-read history on every frame.
func (a *App) visibleHistory() []historyRecord {
	return localstate.FilterRecords(a.historyRecords, a.historyUI.filter(time.Now()))
}

// --- view -----------------------------------------------------------------

func (a *App) historyPanel(c *ui.Context, t *ui.Theme) {
	m := a.msg()
	p := uikit.PaletteFor(t)
	if a.historyKey != historyKey(a.ps) {
		a.reloadHistory()
	}
	ui.Column(c).Grow(1).FillWidth().Children(func() {
		ui.Row(c).Padding(6, 10, 4, 10).AlignItems(ui.Center).Children(func() {
			ui.Text(c, m.HistoryLabel).FontSize(11).FontWeight(600).TextColor(p.Muted).Grow(1)
			// The display switch is independent of the clear scope: it stays
			// usable with an empty list and in the read-only all-project view.
			a.historyEndpointToggle(c, p)
			// Clearing is scoped to this project, so it is only offered while the
			// panel shows this project. The all-project view is read-only on
			// purpose: one click must not erase other projects' history.
			if len(a.historyRecords) > 0 && !a.historyUI.allProjects {
				if b := uikit.ActionCursor(ui.Button(c, m.ClearHistory)).Clicked(); b {
					a.historyClearOpen = true
				}
			}
		})
		a.historyFilters(c, p, m)
		if a.historyUI.diag != "" {
			ui.Text(c, a.historyUI.diag).FontSize(11).TextColor(uikit.MethodColor("POST", p)).Padding(2, 10)
		}
		records := a.visibleHistory()
		if len(records) == 0 {
			text := m.HistoryEmpty
			if len(a.historyRecords) > 0 {
				text = m.HistoryNoMatch
			}
			ui.Text(c, text).FontSize(12).TextColor(p.Faint).Padding(4, 10)
		} else {
			ui.Scroll(c).Grow(1).FillWidth().Children(func() {
				ui.Column(c).FillWidth().Children(func() {
					lastDay := ""
					for _, r := range records {
						r := r
						if d := dayKey(r.Ts); d != lastDay {
							lastDay = d
							ui.Text(c, dayLabel(r.Ts, m)).FontSize(11).FontWeight(600).TextColor(p.Faint).Padding(6, 10)
						}
						a.historyRow(c, t, p, m, r)
					}
				})
			})
		}
		privacyNote := m.HistoryPrivacyNote
		if a.historyPolicy().Unredacted {
			privacyNote = m.HistoryPrivacyFullNote
		}
		ui.Text(c, privacyNote).FontSize(10).TextColor(p.Faint).Padding(4, 10, 8, 10)
	})
}

func (a *App) historyFilters(c *ui.Context, p uikit.Palette, m *messages) {
	ui.Column(c).Padding(0, 10, 4, 10).Gap(4).Children(func() {
		scope := 0
		if a.historyUI.allProjects {
			scope = 1
		}
		seg, changed := uikit.SimpleSegmented(c, p, &scope, []string{m.HistoryThisProject, m.HistoryAllProjects})
		seg.FillWidth()
		if changed {
			a.historyUI.allProjects = scope == 1
			a.historyUI.projectKey = ""
			a.reloadHistory()
		}

		if a.historyUI.allProjects {
			labels, keys := historyProjectOptions(a.historyRecords, m.HistoryAllProjects)
			if len(keys) > 1 {
				idx := 0
				for i, k := range keys {
					if k == a.historyUI.projectKey {
						idx = i
					}
				}
				choice := labels[idx]
				if uikit.ActionCursor(uikit.Select(c, &choice, labels, uikit.SameIcon(uikit.IconFolder))).FillWidth().Changed() {
					a.historyUI.projectKey = keys[stringIndex(labels, choice)]
				}
			}
		}

		ui.Row(c).Gap(4).Children(func() {
			timeChoices := []string{m.HistoryTimeAll, m.HistoryTimeToday, m.HistoryTimeWeek, m.HistoryTimeMonth}
			timeIdx := clampIndex(a.historyUI.timeRange, len(timeChoices))
			timeChoice := timeChoices[timeIdx]
			if uikit.ActionCursor(uikit.Select(c, &timeChoice, timeChoices, uikit.SameIcon(uikit.IconClock))).Grow(1).Changed() {
				a.historyUI.timeRange = stringIndex(timeChoices, timeChoice)
			}

			resultChoices := []string{
				m.HistoryResultAll, m.HistoryResultSuccess, m.HistoryResultClient,
				m.HistoryResultServer, m.HistoryResultNetwork, m.HistoryResultCanceled,
			}
			resultIdx := clampIndex(a.historyUI.result, len(resultChoices))
			resultChoice := resultChoices[resultIdx]
			if uikit.ActionCursor(uikit.Select(c, &resultChoice, resultChoices, uikit.IconMap(map[string]*ui.SVG{
				m.HistoryResultAll:      uikit.IconList,
				m.HistoryResultSuccess:  uikit.IconCheck,
				m.HistoryResultClient:   uikit.IconShield,
				m.HistoryResultServer:   uikit.IconServer,
				m.HistoryResultNetwork:  uikit.IconGlobe,
				m.HistoryResultCanceled: uikit.IconClose,
			}))).Grow(1).Changed() {
				a.historyUI.result = stringIndex(resultChoices, resultChoice)
			}
		})

		ui.TextInput(c, &a.historyUI.query).Placeholder(m.HistoryFilterPlaceholder).FillWidth()
	})
}

func (a *App) historyRow(c *ui.Context, t *ui.Theme, p uikit.Palette, m *messages, r historyRecord) {
	row := ui.Row(c).Height(32).FillWidth().AlignItems(ui.Center).Padding(0, 6).Gap(4).Clip()
	if row.Hovered() {
		row.Background(p.Hover)
	}
	// The stored, already-redacted URL is the row's tooltip in both display
	// modes, so a shortened or clipped label never hides where the request
	// went. This reads the record only; it never re-reads a secret.
	row.Tooltip(r.URL)
	if uikit.ActionCursor(row).Clicked() {
		a.restoreHistory(r)
	}
	row.Children(func() {
		ui.Text(c, r.Ts.Format("15:04")).FontSize(11).FontFeatures("tnum").TextColor(p.Faint).Width(38).SingleLine()
		ui.Text(c, r.Method).Font("monospace").FontWeight(700).FontSize(11).
			TextColor(uikit.MethodColor(r.Method, p)).Width(40).SingleLine()
		if a.historyUI.allProjects && r.ProjectName != "" {
			ui.Text(c, r.ProjectName).FontSize(10).TextColor(p.Faint).Width(58).SingleLine()
		}
		ui.Text(c, historyDisplayURL(r.URL, a.historyUI.endpointOnly)).FontSize(12).TextColor(p.Muted).Grow(1).SingleLine()
		if r.Status > 0 {
			uikit.StatusPill(c, p, r.Status)
		} else {
			ui.Text(c, historyOutcomeLabel(m, r.OutcomeValue())).FontSize(10).
				TextColor(historyOutcomeColor(p, r.OutcomeValue())).Width(52).SingleLine()
		}
		ui.Text(c, formatDurationMs(float64(r.DurationMs))).FontSize(11).FontFeatures("tnum").TextColor(p.Faint).SingleLine()
		// A reserved, compact action area replaces the old absolutely
		// positioned buttons, which overlapped the status and duration cells.
		a.historyRowActions(c, p, t, r)
	})
}

// historyProjectOptions builds the project filter for the all-project view.
// The first entry is "all projects"; duplicate display names get a short key
// suffix so the selector cannot point at the wrong project.
func historyProjectOptions(recs []historyRecord, allLabel string) ([]string, []string) {
	labels := []string{allLabel}
	keys := []string{""}
	names := map[string]string{}
	nameCount := map[string]int{}
	for _, r := range recs {
		if r.ProjectKey == "" {
			continue
		}
		if _, ok := names[r.ProjectKey]; ok {
			continue
		}
		name := r.ProjectName
		if name == "" {
			name = r.ProjectKey
		}
		names[r.ProjectKey] = name
		nameCount[name]++
	}
	seen := map[string]bool{}
	for _, r := range recs {
		if r.ProjectKey == "" || seen[r.ProjectKey] {
			continue
		}
		seen[r.ProjectKey] = true
		name := names[r.ProjectKey]
		if nameCount[name] > 1 {
			name += " · " + shortKey(r.ProjectKey)
		}
		labels = append(labels, name)
		keys = append(keys, r.ProjectKey)
	}
	return labels, keys
}

func shortKey(key string) string {
	if i := strings.LastIndexByte(key, '-'); i >= 0 && i+1 < len(key) {
		key = key[i+1:]
	}
	if len(key) > 6 {
		key = key[len(key)-6:]
	}
	return key
}

func historyHasProject(recs []historyRecord, key string) bool {
	for _, r := range recs {
		if r.ProjectKey == key {
			return true
		}
	}
	return false
}

func historyOutcomeLabel(m *messages, outcome string) string {
	switch outcome {
	case localstate.OutcomeCanceled:
		return m.HistoryOutcomeCanceled
	case localstate.OutcomeTimeout:
		return m.HistoryOutcomeTimeout
	default:
		return m.HistoryOutcomeError
	}
}

func historyOutcomeColor(p uikit.Palette, outcome string) ui.Color {
	switch outcome {
	case localstate.OutcomeCanceled:
		return p.Muted
	case localstate.OutcomeTimeout:
		return p.Faint
	default:
		return p.Accent
	}
}

func (a *App) historyDetailModal(c *ui.Context, t *ui.Theme) {
	if !a.historyUI.detailOpen {
		return
	}
	m := a.msg()
	p := uikit.PaletteFor(t)
	r := a.historyUI.detail
	open := true
	ui.Modal(c, &open, func() {
		ui.Column(c).Gap(10).MinWidth(560).Children(func() {
			title := r.RequestName
			if strings.TrimSpace(title) == "" {
				title = r.Method + " " + r.URL
			}
			ui.Text(c, m.HistoryDetailTitle).FontSize(16).Bold().TextColor(p.Text)
			ui.Text(c, title).FontSize(13).TextColor(p.Muted).SingleLine()

			detailRow := func(k, v string) {
				if v == "" {
					return
				}
				ui.Row(c).Gap(10).Children(func() {
					ui.Text(c, k).Width(92).FontSize(11).TextColor(p.Faint).SingleLine()
					ui.Text(c, v).FontSize(12).TextColor(p.Text).Grow(1).SingleLine()
				})
			}
			ui.Text(c, m.HistoryDetailSummary).FontSize(12).FontWeight(600).TextColor(p.Muted)
			detailRow(m.HistoryLabel, r.Ts.Format("2006-01-02 15:04:05"))
			detailRow(m.Projects, r.ProjectName)
			detailRow(m.EnvLabel, r.Env)
			detailRow(m.HistoryDetailResult, fmt.Sprintf("%s · %s · %s", resultSummary(m, r), formatDurationMs(float64(r.DurationMs)), formatByteSize(r.SizeBytes)))
			detailRow(m.HistoryDetailCapture, historyCaptureSummary(m, r))
			detailRow("URL", r.URL)
			detailRow(m.Error, r.Error)

			ui.Scroll(c).MaxHeight(320).FillWidth().Children(func() {
				ui.Column(c).Gap(10).FillWidth().Children(func() {
					a.historySnapshot(c, p, m, m.HistoryDetailRequest, r.Request)
					a.historySnapshot(c, p, m, m.HistoryDetailResponse, r.Response)
				})
			})
			if r.IsUnredacted() {
				ui.Text(c, m.HistoryUnredactedNote).FontSize(11).TextColor(p.Muted)
				if r.SnapshotTrimmed {
					ui.Text(c, m.HistorySnapshotTrimmed).FontSize(11).TextColor(p.Faint)
				}
				if r.Response != nil && r.Response.Truncated {
					ui.Text(c, m.HistoryTruncatedBoundaryNote).FontSize(11).TextColor(p.Faint)
				}
				if r.Request != nil && (r.Request.Omitted == localstate.OmitStream || r.Request.Truncated) {
					ui.Text(c, m.HistoryStreamBoundaryNote).FontSize(11).TextColor(p.Faint)
				}
			} else {
				ui.Text(c, m.HistoryRedactedNote).FontSize(11).TextColor(p.Faint)
			}

			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if uikit.ActionCursor(ui.Button(c, m.HistoryDetailDelete)).Clicked() {
					a.deleteHistory(r)
					open = false
				}
				if uikit.ActionCursor(ui.Button(c, m.Close)).Clicked() {
					open = false
				}
				if uikit.ActionCursor(uikit.PrimaryButton(c, m.HistoryDetailOpen)).Clicked() {
					a.restoreHistory(r)
					open = false
				}
			})
		})
	})
	if !open {
		a.historyUI.detailOpen = false
	}
}

func (a *App) historySnapshot(c *ui.Context, p uikit.Palette, m *messages, title string, s *localstate.Snapshot) {
	if s == nil {
		return
	}
	ui.Column(c).Gap(3).FillWidth().Children(func() {
		ui.Text(c, title).FontSize(12).FontWeight(600).TextColor(p.Muted)
		for _, line := range snapshotLines(s, m) {
			ui.Text(c, line).Font("monospace").FontSize(11).TextColor(p.Faint)
		}
	})
}

const maxSnapshotPreviewBytes = 16 << 10

func historyCaptureSummary(m *messages, r historyRecord) string {
	if !r.IsUnredacted() {
		return m.HistoryCaptureRedacted
	}
	if r.SnapshotTrimmed {
		return m.HistoryCaptureTrimmed
	}
	if (r.Response != nil && r.Response.Truncated) ||
		(r.Request != nil && (r.Request.Truncated || r.Request.Omitted == localstate.OmitStream)) {
		return m.HistoryCapturePartial
	}
	return m.HistoryCaptureFull
}

func snapshotLines(s *localstate.Snapshot, m *messages) []string {
	var lines []string
	for _, kv := range s.Headers {
		lines = append(lines, kv[0]+": "+kv[1])
	}
	if s.IsBinaryBody() {
		lines = append(lines, "")
		size := s.BodySize
		raw, err := s.BodyBytes()
		if err == nil && size <= 0 {
			size = int64(len(raw))
		}
		lines = append(lines, translate(m.HistoryBinarySavedFmt, formatByteSize(size)))
		if err == nil && len(raw) > 0 {
			n := len(raw)
			suffix := ""
			if n > 64 {
				n = 64
				suffix = "…"
			}
			lines = append(lines, "hex: "+hex.EncodeToString(raw[:n])+suffix)
		}
		if s.Truncated {
			lines = append(lines, translate(m.HistoryBodyTruncatedSavedFmt, formatByteSize(size)))
		}
		return lines
	}
	if s.Body != "" {
		lines = append(lines, "")
		preview := s.Body
		shortened := false
		if len(preview) > maxSnapshotPreviewBytes {
			preview = truncateUTF8(preview, maxSnapshotPreviewBytes)
			shortened = true
		}
		bodyLines := strings.Split(preview, "\n")
		if len(bodyLines) > 380 {
			bodyLines = bodyLines[:380]
			shortened = true
		}
		lines = append(lines, bodyLines...)
		size := s.BodySize
		if size <= 0 {
			size = int64(len(s.Body))
		}
		if shortened {
			lines = append(lines, translate(m.HistoryPreviewShortenedFmt, formatByteSize(size)))
		}
		if s.Truncated {
			lines = append(lines, translate(m.HistoryBodyTruncatedSavedFmt, formatByteSize(size)))
		}
	} else if s.Omitted != "" {
		lines = append(lines, translate(m.HistoryBodyOmitted, historyOmitReason(m, s.Omitted)))
	}
	if len(lines) > 400 {
		lines = lines[:400]
	}
	return lines
}

func truncateUTF8(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	cut := s[:maxBytes]
	for len(cut) > 0 && !utf8.ValidString(cut) {
		cut = cut[:len(cut)-1]
	}
	return cut
}

func historyOmitReason(m *messages, reason string) string {
	switch reason {
	case localstate.OmitPolicy:
		return m.HistoryOmitPolicy
	case localstate.OmitLarge:
		return m.HistoryOmitLarge
	case localstate.OmitBinary:
		return m.HistoryOmitBinary
	case localstate.OmitTruncated:
		return m.HistoryOmitTruncated
	case localstate.OmitStream:
		return m.HistoryOmitStream
	default:
		return reason
	}
}

func resultSummary(m *messages, r historyRecord) string {
	switch r.OutcomeValue() {
	case localstate.OutcomeCanceled:
		return m.HistoryOutcomeCanceled
	case localstate.OutcomeTimeout:
		return m.HistoryOutcomeTimeout
	case localstate.OutcomeError:
		return m.HistoryOutcomeError
	}
	if r.Status > 0 {
		if r.Proto != "" {
			return fmt.Sprintf("%s %d", r.Proto, r.Status)
		}
		return fmt.Sprintf("%d", r.Status)
	}
	return m.HistoryOutcomeError
}

func (a *App) historyClearModal(c *ui.Context, t *ui.Theme) {
	if !a.historyClearOpen {
		return
	}
	m := a.msg()
	p := uikit.PaletteFor(t)
	open := true
	ui.Modal(c, &open, func() {
		ui.Column(c).Gap(12).MinWidth(380).Children(func() {
			ui.Text(c, m.HistoryLabel).FontSize(16).Bold().TextColor(p.Text)
			ui.Text(c, m.ConfirmClearHistory).FontSize(12).TextColor(p.Muted)
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if uikit.ActionCursor(ui.Button(c, m.Cancel)).Clicked() {
					a.historyClearOpen = false
					open = false
				}
				if uikit.ActionCursor(uikit.PrimaryButton(c, m.ClearHistory)).Clicked() {
					a.clearHistory()
					a.historyClearOpen = false
					open = false
				}
			})
		})
	})
	if !open {
		a.historyClearOpen = false
	}
}

func dayKey(ts time.Time) string { return ts.Format("2006-01-02") }

func dayLabel(ts time.Time, m *messages) string {
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	day := time.Date(ts.Year(), ts.Month(), ts.Day(), 0, 0, 0, 0, now.Location())
	switch {
	case day.Equal(today):
		return m.Today
	case day.Equal(today.AddDate(0, 0, -1)):
		return m.Yesterday
	default:
		return dayKey(ts)
	}
}

func clampIndex(v, n int) int {
	if n <= 0 || v < 0 {
		return 0
	}
	if v >= n {
		return n - 1
	}
	return v
}

func stringIndex(list []string, v string) int {
	for i, s := range list {
		if s == v {
			return i
		}
	}
	return 0
}
