package desktop

import (
	"context"
	"encoding/json"
	"time"

	"github.com/egoist/mygo"

	"gorequest/internal/core"
	"gorequest/internal/update"
)

// installerHandoff is a verified update package waiting to be opened. It lives
// on the Manager because the unsaved-edit guard is per project window while the
// installer launch is application-wide.
//
// Choosing "Discard" on a window during an update handoff does not clear its
// Dirty flags up front: instead it records the window's current dirty-content
// fingerprint in approved. If a subsequent window cancels or the installer
// fails to launch, dropping the handoff leaves every window's Dirty flags and
// in-memory drafts intact. If an approved window is edited again while another
// window's guard is open, its fingerprint no longer matches and it is prompted
// again before the installer can launch.
type installerHandoff struct {
	// launch opens the installer. It must only run once no window has
	// unapproved dirty work.
	launch func() error
	// launched/failed/canceled report the outcome on the UI thread.
	launched func()
	failed   func(error)
	canceled func()

	approved map[*App]windowDirtyFingerprint
}

type windowDirtyFingerprint struct {
	tabs     map[*Tab]string
	envSig   string
	suiteSig string
}

type kvRowSnap struct {
	ID      string `json:"id"`
	Enabled bool   `json:"enabled"`
	Name    string `json:"name"`
	Value   string `json:"value"`
}

type formRowSnap struct {
	ID      string `json:"id"`
	Enabled bool   `json:"enabled"`
	Name    string `json:"name"`
	Value   string `json:"value"`
	Encoded bool   `json:"encoded"`
}

type multipartRowSnap struct {
	ID             string `json:"id"`
	Enabled        bool   `json:"enabled"`
	Name           string `json:"name"`
	PartSource     int    `json:"partSource"`
	Value          string `json:"value"`
	ProjectFile    string `json:"projectFile"`
	LocalSlot      string `json:"localSlot"`
	LocalPathDraft string `json:"localPathDraft"`
	FileName       string `json:"fileName"`
	ContentType    string `json:"contentType"`
}

type bodyStateSnap struct {
	Unsupported    bool               `json:"unsupported"`
	RawType        string             `json:"rawType"`
	ModeIdx        int                `json:"modeIdx"`
	TextSource     int                `json:"textSource"`
	InlineJSON     string             `json:"inlineJSON"`
	InlineXML      string             `json:"inlineXML"`
	InlineText     string             `json:"inlineText"`
	ProjectFile    string             `json:"projectFile"`
	LocalSlot      string             `json:"localSlot"`
	LocalPathDraft string             `json:"localPathDraft"`
	BinSource      int                `json:"binSource"`
	BinProjectFile string             `json:"binProjectFile"`
	BinLocalSlot   string             `json:"binLocalSlot"`
	BinLocalPath   string             `json:"binLocalPath"`
	BinContentType string             `json:"binContentType"`
	FormRows       []formRowSnap      `json:"formRows,omitempty"`
	FormViewMode   int                `json:"formViewMode"`
	FormRawText    string             `json:"formRawText"`
	FormErr        string             `json:"formErr"`
	MultipartRows  []multipartRowSnap `json:"multipartRows,omitempty"`
	MultipartErr   string             `json:"multipartErr"`
}

type wsDraftSnap struct {
	SubprotoDraft    string `json:"subprotoDraft"`
	Compress         bool   `json:"compress"`
	SendModeIdx      int    `json:"sendModeIdx"`
	TextDraft        string `json:"textDraft"`
	BinaryDraft      string `json:"binaryDraft"`
	CloseCodeDraft   string `json:"closeCodeDraft"`
	CloseReasonDraft string `json:"closeReasonDraft"`
	SendEditor       string `json:"sendEditor,omitempty"`
}

type tabDraftSnapshot struct {
	Kind            string           `json:"kind"`
	Title           string           `json:"title"`
	Baseline        string           `json:"baseline"`
	Method          string           `json:"method"`
	URL             string           `json:"url"`
	QueryRows       []kvRowSnap      `json:"queryRows,omitempty"`
	HeaderRows      []kvRowSnap      `json:"headerRows,omitempty"`
	QueryText       bool             `json:"queryText"`
	QueryRawEncoded bool             `json:"queryRawEncoded"`
	QueryRaw        string           `json:"queryRaw"`
	HeaderText      bool             `json:"headerText"`
	HeaderRaw       string           `json:"headerRaw"`
	KVErr           string           `json:"kvErr"`
	BodyIdx         int              `json:"bodyIdx"`
	BodyText        string           `json:"bodyText"`
	BodyEditor      string           `json:"bodyEditor,omitempty"`
	BodyState       *bodyStateSnap   `json:"bodyState,omitempty"`
	AuthMode        string           `json:"authMode"`
	AuthIdx         int              `json:"authIdx"`
	AuthRefID       string           `json:"authRefId"`
	AuthToken       string           `json:"authToken"`
	AuthUser        string           `json:"authUser"`
	AuthPass        string           `json:"authPass"`
	AuthKeyName     string           `json:"authKeyName"`
	AuthKeyIn       string           `json:"authKeyIn"`
	AuthKeyInIdx    int              `json:"authKeyInIdx"`
	AuthKeyValue    string           `json:"authKeyValue"`
	AuthInlineVer   int              `json:"authInlineVer"`
	AuthInlineID    string           `json:"authInlineId"`
	AuthKeyInOmit   bool             `json:"authKeyInOmit"`
	AuthPassthrough *core.AuthRef    `json:"authPassthrough,omitempty"`
	Timeout         string           `json:"timeout"`
	DocRel          string           `json:"docRel"`
	DocBuffer       string           `json:"docBuffer,omitempty"`
	WS              *wsDraftSnap     `json:"ws,omitempty"`
	GraphQL         *core.GraphQLDef `json:"graphql,omitempty"`
	SavedReq        *core.Request    `json:"savedReq,omitempty"`
	SaveErr         string           `json:"saveErr,omitempty"`
	FallbackReq     *core.Request    `json:"fallbackReq,omitempty"`
	EditedYAML      string           `json:"editedYAML,omitempty"`
}

type envVarRowSnap struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type envSecretRowSnap struct {
	Name string `json:"name"`
}

type envDraftSnapshot struct {
	Open       bool               `json:"open"`
	Mode       string             `json:"mode"`
	Baseline   string             `json:"baseline"`
	RelPath    string             `json:"relPath"`
	EnvID      string             `json:"envId"`
	IDText     string             `json:"idText"`
	IDLocked   bool               `json:"idLocked"`
	VarRows    []envVarRowSnap    `json:"varRows,omitempty"`
	SecretRows []envSecretRowSnap `json:"secretRows,omitempty"`
	Signature  string             `json:"signature"`
}

// flushAllDocEdits commits any pending visual-markdown block edits across all
// open project windows so Dirty flags and DocEditor buffers are up to date
// before checking for unsaved work or computing content fingerprints.
func (m *Manager) flushAllDocEdits() {
	if m == nil {
		return
	}
	for _, app := range m.workspaces {
		flushAppDocEdits(app)
	}
}

func flushAppDocEdits(app *App) {
	if app == nil || app.closed || app.ps == nil {
		return
	}
	for _, tab := range app.ps.tabs {
		if tab != nil && tab.flushDocEdit != nil {
			tab.flushDocEdit()
		}
	}
}

func captureDirtyFingerprint(app *App) windowDirtyFingerprint {
	fp := windowDirtyFingerprint{tabs: make(map[*Tab]string)}
	if app == nil || app.ps == nil {
		return fp
	}
	flushAppDocEdits(app)
	for _, tab := range app.ps.tabs {
		if tab != nil && tab.Dirty {
			fp.tabs[tab] = tabDraftFingerprint(app.ps, tab)
		}
	}
	if e := app.ps.envUI; e != nil && e.open && e.dirty() {
		fp.envSig = envDraftFingerprint(e)
	}
	if s := app.suiteEditorState(false); s != nil && s.dirty() {
		data, _ := json.Marshal([]string{s.draft.Signature(), s.maxRowsText, s.maxBytesText})
		fp.suiteSig = string(data)
	}
	return fp
}

func envDraftFingerprint(e *environmentEditor) string {
	if e == nil {
		return ""
	}
	snap := envDraftSnapshot{
		Open:      e.open,
		Mode:      e.mode,
		Baseline:  e.baseline,
		RelPath:   e.relPath,
		EnvID:     e.envID,
		IDText:    e.idText,
		IDLocked:  e.idLocked,
		Signature: e.signature(),
	}
	for _, r := range e.varRows {
		if r != nil {
			snap.VarRows = append(snap.VarRows, envVarRowSnap{Key: r.Key, Value: r.Value})
		}
	}
	for _, r := range e.secretRows {
		if r != nil {
			snap.SecretRows = append(snap.SecretRows, envSecretRowSnap{Name: r.Name})
		}
	}
	data, _ := json.Marshal(snap)
	return string(data)
}

func tabDraftFingerprint(ps *projectState, tab *Tab) string {
	if tab == nil {
		return ""
	}
	if tab.flushDocEdit != nil {
		tab.flushDocEdit()
	}
	var savedReq *core.Request
	var saveErr string
	var fallbackReq *core.Request
	var editedYAML string
	if tab.Kind == "request" && tab.Entry != nil && tab.Entry.Request != nil {
		if b, err := editedBytesForTab(ps, peekFileSync(ps), tab); err == nil {
			editedYAML = string(b)
		}
		if req, err := requestForSaveFromTab(ps, tab); err == nil && req != nil {
			savedReq = req
		} else {
			if err != nil {
				saveErr = err.Error()
			}
			fallbackReq = bestEffortRequestFromTab(tab)
		}
	}
	snap := tabDraftSnapshot{
		Kind:            tab.Kind,
		Title:           tab.Title,
		Baseline:        tab.Baseline,
		Method:          tab.Method,
		URL:             tab.URL,
		QueryText:       tab.queryText,
		QueryRawEncoded: tab.queryRawEncoded,
		QueryRaw:        tab.queryRaw,
		HeaderText:      tab.headerText,
		HeaderRaw:       tab.headerRaw,
		KVErr:           tab.kvErr,
		BodyIdx:         tab.BodyIdx,
		BodyText:        tab.BodyText,
		AuthMode:        tab.AuthMode,
		AuthIdx:         tab.AuthIdx,
		AuthRefID:       tab.AuthRefID,
		AuthToken:       tab.AuthToken,
		AuthUser:        tab.AuthUser,
		AuthPass:        tab.AuthPass,
		AuthKeyName:     tab.AuthKeyName,
		AuthKeyIn:       tab.AuthKeyIn,
		AuthKeyInIdx:    tab.AuthKeyInIdx,
		AuthKeyValue:    tab.AuthKeyValue,
		AuthInlineVer:   tab.authInlineVer,
		AuthInlineID:    tab.authInlineID,
		AuthKeyInOmit:   tab.authKeyInOmitted,
		AuthPassthrough: core.CloneAuthRef(tab.authPassthrough),
		Timeout:         tab.Timeout,
		DocRel:          tab.DocRel,
		SavedReq:        savedReq,
		SaveErr:         saveErr,
		FallbackReq:     fallbackReq,
		EditedYAML:      editedYAML,
	}
	for _, r := range tab.QueryRows {
		if r != nil {
			snap.QueryRows = append(snap.QueryRows, kvRowSnap{
				ID:      r.ID,
				Enabled: r.Enabled,
				Name:    r.Name,
				Value:   r.Value,
			})
		}
	}
	if isGraphQLTab(tab) {
		// Invalid documents/JSON cannot be saved, but edits to them must still
		// invalidate an earlier update-discard approval.
		snap.GraphQL = tab.gqlDef()
	}
	for _, r := range tab.HeaderRows {
		if r != nil {
			snap.HeaderRows = append(snap.HeaderRows, kvRowSnap{
				ID:      r.ID,
				Enabled: r.Enabled,
				Name:    r.Name,
				Value:   r.Value,
			})
		}
	}
	if tab.bodyEditor != nil && tab.bodyEditor.Buffer != nil {
		snap.BodyEditor = tab.bodyEditor.Buffer.String()
	}
	if bs := tab.bodyState; bs != nil {
		bsSnap := &bodyStateSnap{
			Unsupported:    bs.unsupported,
			RawType:        bs.rawType,
			ModeIdx:        bs.modeIdx,
			TextSource:     bs.textSource,
			InlineJSON:     bs.inlineJSON,
			InlineXML:      bs.inlineXML,
			InlineText:     bs.inlineText,
			ProjectFile:    bs.projectFile,
			LocalSlot:      bs.localSlot,
			LocalPathDraft: bs.localPathDraft,
			BinSource:      bs.binSource,
			BinProjectFile: bs.binProjectFile,
			BinLocalSlot:   bs.binLocalSlot,
			BinLocalPath:   bs.binLocalPath,
			BinContentType: bs.binContentType,
			FormViewMode:   bs.formViewMode,
			FormRawText:    bs.formRawText,
			FormErr:        bs.formErr,
			MultipartErr:   bs.multipartErr,
		}
		for _, r := range bs.formRows {
			if r != nil {
				bsSnap.FormRows = append(bsSnap.FormRows, formRowSnap{
					ID:      r.ID,
					Enabled: r.Enabled,
					Name:    r.Name,
					Value:   r.Value,
					Encoded: r.Encoded,
				})
			}
		}
		for _, r := range bs.multipartRows {
			if r != nil {
				bsSnap.MultipartRows = append(bsSnap.MultipartRows, multipartRowSnap{
					ID:             r.ID,
					Enabled:        r.Enabled,
					Name:           r.Name,
					PartSource:     r.PartSource,
					Value:          r.Value,
					ProjectFile:    r.ProjectFile,
					LocalSlot:      r.LocalSlot,
					LocalPathDraft: r.LocalPathDraft,
					FileName:       r.FileName,
					ContentType:    r.ContentType,
				})
			}
		}
		snap.BodyState = bsSnap
	}
	if tab.DocEditor != nil && tab.DocEditor.Buffer != nil {
		snap.DocBuffer = tab.DocEditor.Buffer.String()
	}
	if ws := tab.ws; ws != nil {
		wsSnap := &wsDraftSnap{
			SubprotoDraft:    ws.subprotoDraft,
			Compress:         ws.compress,
			SendModeIdx:      ws.sendModeIdx,
			TextDraft:        ws.textDraft,
			BinaryDraft:      ws.binaryDraft,
			CloseCodeDraft:   ws.closeCodeDraft,
			CloseReasonDraft: ws.closeReasonDraft,
		}
		if ws.sendEditor != nil && ws.sendEditor.Buffer != nil {
			wsSnap.SendEditor = ws.sendEditor.Buffer.String()
		}
		snap.WS = wsSnap
	}
	data, _ := json.Marshal(snap)
	return string(data)
}

func (h *installerHandoff) approveWindow(app *App) {
	if h == nil || app == nil {
		return
	}
	if h.approved == nil {
		h.approved = make(map[*App]windowDirtyFingerprint)
	}
	h.approved[app] = captureDirtyFingerprint(app)
}

func (h *installerHandoff) isApproved(app *App) bool {
	if h == nil || app == nil || app.ps == nil {
		return false
	}
	prev, ok := h.approved[app]
	if !ok {
		return false
	}
	cur := captureDirtyFingerprint(app)
	if cur.envSig != prev.envSig || cur.suiteSig != prev.suiteSig || len(cur.tabs) != len(prev.tabs) {
		return false
	}
	for tab, sig := range cur.tabs {
		if prevSig, exists := prev.tabs[tab]; !exists || prevSig != sig {
			return false
		}
	}
	return true
}

// tryResolveHandoffGuard is the Manager entry point for app.go's
// resolveCloseGuard when an update handoff is active. Save still runs
// saveAllDirty(); Discard records the window's dirty fingerprint in the
// handoff without clearing Dirty until the installer launches.
func (m *Manager) tryResolveHandoffGuard(app *App, discard bool) bool {
	if m == nil || m.handoff == nil || app == nil || app.closeGuard == nil || !app.closeGuard.active {
		return false
	}
	if !m.pendingQuit {
		m.abandonHandoff()
		return false
	}
	flushAppDocEdits(app)
	if !discard {
		if err := app.saveAllDirty(); err != nil {
			app.closeGuard.errMsg = err.Error()
			return true
		}
		delete(m.handoff.approved, app)
	} else {
		m.handoff.approveWindow(app)
	}
	app.closeGuard.active = false
	m.promptQuitGuard()
	return true
}

// quit asks the application to quit through the normal lifecycle.
func (m *Manager) quit() {
	if m.quitApp != nil {
		m.quitApp()
		return
	}
	mygo.App.Quit()
}

// anyGuardActive reports whether a project window is showing its close guard.
func (m *Manager) anyGuardActive() bool {
	for _, app := range m.workspaces {
		if !app.closed && app.closeGuard != nil && app.closeGuard.active {
			return true
		}
	}
	return false
}

// hasUnapprovedDirtyForHandoff reports whether any open project has unsaved
// edits that have not been approved for discard at their current fingerprint.
func (m *Manager) hasUnapprovedDirtyForHandoff(h *installerHandoff) bool {
	m.flushAllDocEdits()
	for _, app := range m.workspaces {
		if !app.closed && app.ps.hasDirty() && !h.isApproved(app) {
			return true
		}
	}
	return false
}

// beginHandoff must run on the UI thread. It reuses the quit guard: every dirty
// project is offered save / discard / cancel in turn, and only when all are
// saved or approved at their current content fingerprint is the installer
// launched (finishHandoff). Returns false when another handoff already waits.
func (m *Manager) beginHandoff(h *installerHandoff) bool {
	if m.handoff != nil {
		return false
	}
	m.flushAllDocEdits()
	m.handoff = h
	m.pendingQuit = true
	m.promptQuitGuard()
	return true
}

// finishHandoff runs on the UI thread right after the last dirty check, with
// no yield in between, so an edit made during download or while a guard was
// open cannot slip past. A launch failure never clears Dirty or quits.
func (m *Manager) finishHandoff() {
	h := m.handoff
	m.handoff = nil
	m.pendingQuit = false
	if h == nil {
		return
	}
	if m.hasUnapprovedDirtyForHandoff(h) { // defensive: never launch over unapproved edits
		h.canceled()
		return
	}
	if err := h.launch(); err != nil {
		h.failed(err)
		return
	}
	for app := range h.approved {
		if app != nil && !app.closed && app.ps != nil {
			app.clearDirty()
		}
	}
	h.launched()
	m.quitting = true
	m.quit()
}

// reconcileHandoff cancels a waiting handoff once the guard is gone without
// having settled it (the user pressed Cancel or dismissed the modal). It runs
// every frame from updateModalFor, so it also works before app.go is patched.
func (m *Manager) reconcileHandoff() {
	if m == nil || m.handoff == nil {
		return
	}
	if m.pendingQuit && m.anyGuardActive() {
		return
	}
	m.abandonHandoff()
}

// abandonHandoff drops a waiting handoff without opening the installer or
// clearing Dirty on any previously approved window.
func (m *Manager) abandonHandoff() {
	if m == nil || m.handoff == nil {
		return
	}
	h := m.handoff
	m.handoff = nil
	m.pendingQuit = false
	h.canceled()
}

// handOffInstaller runs on the UI thread after a verified download. The
// installer is opened only after the unsaved-edit guard passes.
func (u *updateController) handOffInstaller(ctx context.Context, dl *update.Downloaded) {
	if ctx.Err() != nil { // download canceled just as it finished
		u.closeDialog()
		return
	}
	fail := func(err error) {
		u.dialogKind = updateDialogResult
		u.result = updateResultError
		u.detail = err.Error()
		u.dialogOpen = true
		u.cancel = nil
	}
	h := &installerHandoff{
		launch: func() error { return u.open(dl.Path) },
		launched: func() {
			u.state.Mutate(func(st *update.State) {
				st.LastCheckedAt = u.now().UTC().Format(time.RFC3339)
				st.LastResult = update.ResultDownloaded
				st.LastError = ""
			})
			u.dialogKind = updateDialogResult
			u.result = updateResultDownloaded
			u.detail = dl.Path
			u.dialogOpen = true
			u.cancel = nil
		},
		failed: fail,
		canceled: func() {
			u.dialogKind = updateDialogResult
			u.result = updateResultHandoffCanceled
			u.detail = dl.Path
			u.dialogOpen = true
			u.cancel = nil
		},
	}
	if u.mgr == nil {
		if err := h.launch(); err != nil {
			h.failed(err)
			return
		}
		h.launched()
		return
	}
	u.cancel = nil
	u.mgr.flushAllDocEdits()
	if u.mgr.anyDirty() {
		u.dialogOpen = false // the save guard is the only modal meanwhile
	}
	if !u.mgr.beginHandoff(h) {
		fail(errHandoffBusy)
	}
}

var errHandoffBusy = handoffError("another update handoff is already waiting")

type handoffError string

func (e handoffError) Error() string { return string(e) }
