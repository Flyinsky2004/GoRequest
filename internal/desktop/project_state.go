package desktop

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo/ui/editor"

	"gorequest/internal/core"
)

// projectState holds everything that belongs to one opened project: the loaded
// core.Project plus the tabs, unsaved edits, selection and environment choice
// for that project. Keeping it per project means switching projects never
// discards another project's open tabs or dirty editors.
type projectState struct {
	root     string // normalized project root (the folder containing .gorequest/)
	project  *core.Project
	tabs     []*Tab
	active   int
	envSel   string
	sel      string // sidebar selection id within this project ("req:..." / "doc:...")
	docSplit float32
}

func newProjectState(root string, p *core.Project) *projectState {
	ps := &projectState{
		root:     root,
		project:  p,
		envSel:   "(none)",
		docSplit: 0.5,
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

// openSelection opens the request or document named by a sidebar selection id.
func (ps *projectState) openSelection(sel string) error {
	switch {
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

func (ps *projectState) openRequest(entry *core.RequestEntry) {
	for i, tab := range ps.tabs {
		if tab.Kind == "request" && tab.Entry == entry {
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
	tab.Method = entry.Request.HTTP.Method
	tab.URL = entry.Request.HTTP.URL
	tab.Timeout = entry.Request.Timeout
	tab.QueryRows = rowsFromKV(entry.Request.HTTP.Query)
	tab.HeaderRows = rowsFromKV(entry.Request.HTTP.Headers)
	if entry.Request.HTTP.Body != nil {
		tab.BodyIdx = indexOf(bodyTypes, entry.Request.HTTP.Body.Type)
		tab.BodyText = core.BodyEditText(entry.Request.HTTP.Body)
	}
	fillAuth(ps.project, tab, entry.Request)
	ps.tabs = append(ps.tabs, tab)
	ps.active = len(ps.tabs) - 1
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

// newRequest creates a request in this project and opens its tab.
func (ps *projectState) newRequest() (*core.RequestEntry, error) {
	entry, err := ps.project.NewRequest("", "New Request")
	if err != nil {
		return nil, err
	}
	ps.openRequest(entry)
	return entry, nil
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
		if _, err := ps.project.SaveRequest(tab.Entry, req, tab.Baseline); err != nil {
			return err
		}
		tab.Baseline = tab.Entry.BaselineDigest
		tab.Dirty = false
		return nil
	}
	content := []byte(tab.DocEditor.Buffer.String())
	digest, err := ps.project.SaveDoc(tab.DocRel, content, tab.Baseline)
	if err != nil {
		return err
	}
	tab.Baseline = digest
	tab.Dirty = false
	return nil
}
