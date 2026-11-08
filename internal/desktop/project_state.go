package desktop

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"gorequest/internal/core"
	"gorequest/internal/platform"
)

// projectState holds everything that belongs to one opened project: the loaded
// core.Project plus the tabs, unsaved edits, selection and environment choice
// for that project. Keeping it per project means switching projects never
// discards another project's open tabs or dirty editors.
type projectState struct {
	root    string // normalized project root (the folder containing .gorequest/)
	project *core.Project
	secrets core.SecretProvider
	// secretScope is the clone-isolated "<slug>:<hash>" namespace shared by the
	// selected backend, its management manifest and the persisted preference.
	secretScope string
	// secretKind is the backend the user selected for this project clone.
	secretKind platform.SecretBackendKind
	// secretBackend is the live backend object behind ps.secrets. Switching
	// replaces it and rebuilds ps.secrets so HTTP/WS/Runner reads the new store.
	secretBackend platform.SecretBackend
	tabs          []*Tab
	active        int
	envSel        string
	sel           string // sidebar selection id within this project ("req:..."/"doc:..."/"folder:...")
	docSplit      float32
	// buildPct is the build/response split percentage (build pane share) for
	// request tabs, 55 by default (§7) and adjusted when the user drags it.
	// It is the user's preference; a temporary height clamp is never stored
	// here.
	buildPct float32
	// layoutPct is the value handed to the splitter this frame. The splitter
	// writes it only on a user move, so comparing it with the value we passed
	// distinguishes a drag from a layout clamp.
	layoutPct float32
	// splitRef and respRef measure the request split's committed geometry. The
	// split height drives the minimum response height guard; the response rect
	// is also read by layout tests.
	splitRef ui.Handle
	respRef  ui.Handle

	// envUI is the in-progress environment create/edit draft for this project.
	// Keeping it per project means switching projects cannot apply one project's
	// draft to another.
	envUI *environmentEditor
	// expanded records which folders are open in this project's request tree.
	// It is per project (like tabs and edits), so two open project windows never
	// share an expansion state.
	expanded map[string]bool
	// collapsed records which sidebar sections ("requests"/"docs") are folded,
	// per project so a fold never leaks between windows.
	collapsed map[string]bool

	// renaming is the tree row being renamed in place (§3), and renameText
	// its draft name.
	renaming   string
	renameText string
}

func newProjectState(root string, p *core.Project) *projectState {
	ps := &projectState{
		root:      root,
		project:   p,
		envSel:    "(none)",
		docSplit:  0.5,
		buildPct:  defaultBuildPct,
		layoutPct: defaultBuildPct,
		expanded:  map[string]bool{},
		collapsed: map[string]bool{},
	}
	if len(p.EnvOrder) > 0 {
		ps.envSel = p.EnvOrder[0]
	}
	return ps
}

// name returns the project's display name.
func (ps *projectState) name() string {
	if ps.project != nil && ps.project.File != nil && strings.TrimSpace(ps.project.File.Name) != "" {
		return ps.project.File.Name
	}
	return filepath.Base(ps.root)
}

// currentEnv returns the selected environment id, or "" for none.
func (ps *projectState) currentEnv() string {
	if ps.envSel == "(none)" {
		return ""
	}
	return ps.envSel
}

func (ps *projectState) activeTab() *Tab {
	if ps.active < 0 || ps.active >= len(ps.tabs) {
		return nil
	}
	return ps.tabs[ps.active]
}

// hasDirty includes both edited tabs and the project's environment draft.
func (ps *projectState) hasDirty() bool {
	for _, tab := range ps.tabs {
		if tab.Dirty {
			return true
		}
	}
	return ps.envUI != nil && ps.envUI.open && ps.envUI.dirty()
}

// activateTab makes the given tab the active one by identity, without relying
// on its current index.
func (ps *projectState) activateTab(tab *Tab) {
	if i := ps.indexOfTab(tab); i >= 0 {
		ps.active = i
	}
}

// openSelection opens the request or document named by a sidebar selection id.
// A folder id only changes the selected target for new requests; folders are
// toggled by the tree view itself.
func (ps *projectState) openSelection(sel string) error {
	switch {
	case strings.HasPrefix(sel, "folder:"):
		ps.sel = sel
		return nil
	case strings.HasPrefix(sel, "req:"):
		id := strings.TrimPrefix(sel, "req:")
		entry, ok := ps.project.Requests[id]
		if !ok {
			return fmt.Errorf("request %q not found", id)
		}
		ps.openRequest(entry)
		return nil
	case strings.HasPrefix(sel, "doc:"):
		return ps.openDoc(strings.TrimPrefix(sel, "doc:"))
	}
	return nil
}

// selectedFolder returns the project-relative folder path selected as the
// target for new requests, or "" for the root.
func (ps *projectState) selectedFolder() string {
	if strings.HasPrefix(ps.sel, "folder:") {
		return strings.TrimPrefix(ps.sel, "folder:")
	}
	return ""
}

// expandChain marks every ancestor folder of rel (and rel itself optional) as
// expanded so a newly created/selected node is visible.
func (ps *projectState) expandChain(rel string) {
	if ps.expanded == nil {
		ps.expanded = map[string]bool{}
	}
	cur := filepath.ToSlash(strings.TrimSpace(rel))
	for cur != "" && cur != "." && cur != "collections" {
		ps.expanded[cur] = true
		parent := filepath.ToSlash(filepath.Dir(cur))
		if parent == cur {
			break
		}
		cur = parent
	}
}

func (ps *projectState) openRequest(entry *core.RequestEntry) {
	if entry == nil || entry.Request == nil {
		return
	}
	for i, tab := range ps.tabs {
		if tab.Kind == "request" && tab.Entry != nil && tab.Entry.Request != nil &&
			tab.Entry.Request.ID == entry.Request.ID {
			ps.active = i
			return
		}
	}
	tab := &Tab{
		Kind:     "request",
		Title:    entry.Request.Name,
		Owner:    ps,
		Entry:    entry,
		Baseline: entry.BaselineDigest,
		RespSize: 320,
	}
	fillTabFromRequest(tab, entry, ps.project)
	ps.tabs = append(ps.tabs, tab)
	ps.active = len(ps.tabs) - 1
}

// fillTabFromRequest copies a stored request into a tab's editable fields. It is
// shared by opening a tab and by discarding edits, so both start from exactly
// the persisted request.
func fillTabFromRequest(tab *Tab, entry *core.RequestEntry, p *core.Project) {
	req := entry.Request
	tab.Title = req.Name
	tab.Method = ""
	tab.URL = ""
	tab.Timeout = req.Timeout
	tab.QueryRows = nil
	tab.HeaderRows = nil
	tab.BodyIdx = 0
	tab.BodyText = ""
	if req.HTTP != nil {
		tab.Method = req.HTTP.Method
		tab.URL = req.HTTP.URL
		tab.QueryRows = rowsFromKV(req.HTTP.Query)
		tab.HeaderRows = rowsFromKV(req.HTTP.Headers)
		if req.HTTP.Body != nil {
			tab.BodyIdx = indexOf(bodyTypes, req.HTTP.Body.Type)
			tab.BodyText = core.BodyEditText(req.HTTP.Body)
		}
	}
	tab.resetBodyState()
	fillAuth(p, tab, req)
	if strings.EqualFold(strings.TrimSpace(req.Protocol), core.ProtocolWebSocket) {
		// Keep a live session across a discard/reload: only the persisted drafts
		// are refreshed. The endpoint/headers/auth already live on the tab.
		st := tab.ensureWSState()
		st.subprotoDraft = ""
		st.compress = false
		if req.WS != nil {
			st.subprotoDraft = strings.Join(req.WS.Subprotocols, ", ")
			st.compress = req.WS.Compression
		}
	} else if tab.ws != nil {
		wsTeardownTab(tab)
		tab.ws = nil
	}
}

func (ps *projectState) openDoc(rel string) error {
	for i, tab := range ps.tabs {
		if tab.Kind == "doc" && tab.DocRel == rel {
			ps.active = i
			return nil
		}
	}
	content, digest, err := ps.project.Store.Read(rel)
	if err != nil {
		return fmt.Errorf("read doc: %w", err)
	}
	tab := &Tab{
		Kind:     "doc",
		Title:    filepath.Base(rel),
		Owner:    ps,
		DocRel:   rel,
		Baseline: digest,
		DocEditor: &editor.State{
			Buffer: editor.NewBuffer(string(content)),
		},
	}
	tab.DocEditor.Buffer.SetLanguage(editor.LanguageNamed("markdown"))
	ps.tabs = append(ps.tabs, tab)
	ps.active = len(ps.tabs) - 1
	return nil
}

// newRequest creates a request in this project and opens its tab. When a folder
// is selected in the tree the request is created inside it; otherwise it goes
// to the collections root.
func (ps *projectState) newRequest() (*core.RequestEntry, error) {
	return ps.createRequestIn(ps.selectedFolder(), "New Request")
}

// createRequestIn creates a request in folderRel ("" for root) and opens it,
// revealing and selecting the target folder.
func (ps *projectState) createRequestIn(folderRel, name string) (*core.RequestEntry, error) {
	entry, err := ps.project.NewRequest(collectionRelOf(folderRel), name)
	if err != nil {
		return nil, err
	}
	ps.expandChain(folderRel)
	ps.openRequest(entry)
	ps.sel = "req:" + entry.Request.ID
	return entry, nil
}

// createWebSocketIn creates a protocol: websocket request in folderRel ("" for
// root) and opens its workspace tab. It is the single creation path for both the
// "+" menu and any future tree action, so a new WebSocket request can never be
// mistaken for an HTTP one.
func (ps *projectState) createWebSocketIn(folderRel, name string) (*core.RequestEntry, error) {
	entry, err := ps.project.NewWebSocketRequest(collectionRelOf(folderRel), name)
	if err != nil {
		return nil, err
	}
	ps.expandChain(folderRel)
	ps.openRequest(entry)
	ps.sel = "req:" + entry.Request.ID
	return entry, nil
}

// createFolder creates a folder under parentRel ("" for root), expands the path
// so it is immediately visible, and selects it as the new-request target.
func (ps *projectState) createFolder(parentRel, name string) (*core.CollectionEntry, error) {
	node, err := ps.project.NewFolder(collectionRelOf(parentRel), name)
	if err != nil {
		return nil, err
	}
	ps.expandChain(node.RelPath)
	if ps.expanded == nil {
		ps.expanded = map[string]bool{}
	}
	ps.expanded[node.RelPath] = true
	ps.sel = "folder:" + node.RelPath
	return node, nil
}

// moveRequest moves the tab's request into folderRel ("" for root), keeping the
// tab and request identity. The caller is responsible for any unsaved-content
// decision before calling.
func (ps *projectState) moveRequest(tab *Tab, folderRel string) error {
	if tab == nil || tab.Kind != "request" || tab.Entry == nil {
		return fmt.Errorf("no request tab to move")
	}
	if err := ps.project.MoveRequest(tab.Entry, collectionRelOf(folderRel)); err != nil {
		return err
	}
	ps.expandChain(folderRel)
	ps.sel = "req:" + tab.Entry.Request.ID
	return nil
}

// reloadRequestTab discards a request tab's edits by re-reading the entry's
// stored request into the tab fields, leaving it clean.
func (ps *projectState) reloadRequestTab(tab *Tab) {
	if tab == nil || tab.Entry == nil || tab.Entry.Request == nil {
		return
	}
	fillTabFromRequest(tab, tab.Entry, ps.project)
	tab.Dirty = false
	tab.Baseline = tab.Entry.BaselineDigest
}

// newDoc creates a Markdown document in this project and opens its tab.
func (ps *projectState) newDoc() (*core.DocEntry, error) {
	de, err := ps.project.NewDoc("document")
	if err != nil {
		return nil, err
	}
	if err := ps.openDoc(de.RelPath); err != nil {
		return nil, err
	}
	return de, nil
}

// saveTab writes the tab back to disk with conflict checking, clearing its
// dirty flag on success.
func (ps *projectState) saveTab(tab *Tab) error {
	if tab.Kind == "request" {
		req, err := requestFromTab(tab)
		if err != nil {
			return err
		}
		if _, err := ps.project.SaveRequestPreserving(tab.Entry, req, tab.Baseline); err != nil {
			return err
		}
		tab.Baseline = tab.Entry.BaselineDigest
		tab.Dirty = false
		return nil
	}
	content := documentBytesForSave(tab)
	digest, err := ps.project.SaveDoc(tab.DocRel, content, tab.Baseline)
	if err != nil {
		return err
	}
	tab.Baseline = digest
	tab.DocEditor.Buffer.MarkSaved()
	tab.Dirty = false
	return nil
}

// collectionRelOf maps a project-relative folder path ("collections/auth", or
// "" / "collections" for the root) to a collections-relative path ("auth").
func collectionRelOf(entryRel string) string {
	rel := filepath.ToSlash(strings.TrimSpace(entryRel))
	rel = strings.TrimPrefix(rel, "collections/")
	rel = strings.TrimPrefix(rel, "collections")
	rel = strings.TrimPrefix(rel, "/")
	if rel == "." {
		return ""
	}
	return rel
}
