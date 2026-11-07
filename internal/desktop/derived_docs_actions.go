package desktop

import (
	"fmt"
	"sort"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"gorequest/internal/core"
	"gorequest/internal/docgen"
)

// This file contains the reactions of the derived-document panel: preview
// computation, the guarded export run, the target-directory picker, the
// insert-reference menus and the click-through of gorequest://request
// preview links. The definitions stay read-only: nothing in this file sends
// a request, resolves variables or reads the secret provider.

// docgenExportOpts maps the panel state onto export options for tab's doc.
func (a *App) docgenExportOpts(tab *Tab, st *derivedDocState) docgen.ExportOptions {
	opts := docgen.ExportOptions{
		TargetDir:      strings.TrimSpace(st.target),
		IncludeAssets:  st.assets,
		AllowOverwrite: st.overwr,
		Labels:         a.cardLabelsFor(),
		Limits:         docgen.ExportLimits{},
	}
	opts.Format = docgen.ExportMarkdown
	if st.format == 1 {
		opts.Format = docgen.ExportHTML
	}
	ps := tab.Owner
	overrides := map[string]string{}
	docs := []string{}
	if ps != nil && ps.activeTab() != nil && tab.Kind == "doc" {
		overrides[tabDocRel(tab)] = tab.DocEditor.Buffer.String()
	}
	scopeAll := st.scope == 1
	if scopeAll {
		for _, d := range ps.project.Docs {
			docs = append(docs, d.RelPath)
		}
	} else {
		docs = []string{tabDocRel(tab)}
	}
	if len(overrides) > 0 {
		opts.ContentOverrides = overrides
	}
	opts.Docs = docs
	return opts
}

func tabDocRel(tab *Tab) string {
	if tab == nil || tab.DocEditor == nil {
		return ""
	}
	return tab.DocRel
}

// docgenPreview recomputes the export preview for the panel state.
func (a *App) docgenPreview(tab *Tab, st *derivedDocState) {
	dm := a.derivedDocMsg()
	ps := tab.Owner
	if ps == nil || ps.project == nil {
		return
	}
	rel := tabDocRel(tab)
	if rel == "" {
		st.lastErr = dm.TargetEmpty
		return
	}
	opts := a.docgenExportOpts(tab, st)
	prev := docgen.PreviewExport(ps.project, opts)
	st.preview = prev
	st.result = nil
	st.notice = ""
	if prev.Err != nil {
		st.lastErr = fmt.Sprintf(dm.Failed, prev.Err.Error())
		return
	}
	st.lastErr = ""
}

// docgenExport runs the guarded export and reports the outcome.
func (a *App) docgenExport(tab *Tab, st *derivedDocState) {
	dm := a.derivedDocMsg()
	ps := tab.Owner
	if ps == nil || ps.project == nil {
		return
	}
	rel := tabDocRel(tab)
	if rel == "" || strings.TrimSpace(st.target) == "" {
		st.lastErr = dm.TargetEmpty
		st.notice = ""
		return
	}
	// Error-level diagnostics block the export until the author resolves
	// them: publishing a set with missing references contradicts the export's
	// purpose. Conflicts are handled by the writer (skip, never overwrite).
	opts := a.docgenExportOpts(tab, st)
	st.preview = docgen.PreviewExport(ps.project, opts)
	st.result = nil
	st.notice = ""
	if n := countSeverity(st.preview.Diagnostics, core.SeverityError); n > 0 {
		st.lastErr = fmt.Sprintf(dm.ProblemsBlock, n)
		return
	}
	res, err := docgen.RunExport(ps.project, opts)
	if err != nil {
		st.lastErr = fmt.Sprintf(dm.Failed, err.Error())
		st.notice = ""
		return
	}
	res.Conflicts = append(res.Conflicts, st.preview.Conflicts...)
	st.result = res
	st.preview = docgen.PreviewExport(ps.project, opts)
	st.notice = fmt.Sprintf(dm.Done, len(res.Written))
	if len(res.Skipped) > 0 {
		st.notice += " · " + fmt.Sprintf(dm.Skipped, len(res.Skipped))
	}
	st.lastErr = ""
}

// docgenPickTargetDir opens the native directory picker off the UI thread
// and lands the choice on the panel state.
func (a *App) docgenPickTargetDir(st *derivedDocState) {
	dm := a.derivedDocMsg()
	var pw *mygo.Window
	if nw, ok := a.win.(nativeWindower); ok {
		pw = nw.nativeWindow()
	}
	if pw == nil {
		a.status = a.docMsg().DialogUnavailable
		return
	}
	onPick := func(dir string) {
		if dir == "" {
			return
		}
		st.target = dir
	}
	go func() {
		dirs, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent: pw, Title: dm.TargetLabel, ButtonLabel: dm.Export,
			Directory: true, CreateDirectories: true,
		})
		if err != nil {
			a.win.Update(func() { a.status = fmt.Sprintf(dm.Failed, err.Error()) })
			return
		}
		if len(dirs) == 0 {
			return
		}
		a.win.Update(func() { onPick(dirs[0]) })
	}()
}

// docgenInsertMenuEntries adds the derived-doc entries to the doc insert menu
// (called by the integration patch's docInsertMenu).
func (a *App) docgenInsertMenuEntries(mm *ui.Menu, tab *Tab, dm *derivedDocMessages) {
	ps := tab.Owner
	if ps == nil || ps.project == nil || len(ps.project.Order) == 0 {
		return
	}
	orders := make([]string, 0, len(ps.project.Order))
	orders = append(orders, ps.project.Order...)
	names := map[string]string{}
	for _, id := range orders {
		if e := ps.project.Requests[id]; e != nil && e.Request != nil {
			names[id] = e.Request.Name
		}
	}
	sort.Strings(orders)

	mm.Separator()
	linkMenu := mm.Submenu(dm.InsertRef)
	cardMenu := mm.Submenu(dm.InsertCard)
	for i, id := range orders {
		if i >= docgenMaxListedRequests {
			break
		}
		if id == "" {
			continue
		}
		label := id
		if names[id] != "" {
			label = names[id] + " (" + id + ")"
		}
		if linkMenu.Item(label).Chosen() {
			insertDocgenRef(tab, id, docgen.FormatRefLink(id, names[id]))
			a.status = fmt.Sprintf(dm.OpenRequest, id)
		}
		if cardMenu.Item(label).Chosen() {
			insertDocgenRef(tab, id, docgen.FormatDirective(id, nil))
			a.status = fmt.Sprintf(dm.OpenRequest, id)
		}
	}
}

// docgenMaxListedRequests keeps an oversized collection from building an
// unbounded menu; beyond it the reference text is typed from the docs.
const docgenMaxListedRequests = 60

// insertDocgenRef inserts a reference at the caret.
func insertDocgenRef(tab *Tab, id, text string) {
	if tab == nil || tab.DocEditor == nil {
		return
	}
	cur := tab.DocEditor.Cursor()
	tab.DocEditor.Select(cur, cur)
	tab.DocEditor.Insert(text + "\n")
}

// docgenOpenRequestLink handles a gorequest://request/<id> preview link:
// it resolves within the project and opens the request tab. It returns
// false when the destination is not a docgen link or the id is unknown, so
// the caller can fall back to B09's link handling.
func (a *App) docgenOpenRequestLink(tab *Tab, dest string) bool {
	dm := a.derivedDocMsg()
	ps := tab.Owner
	if ps == nil || ps.project == nil {
		return false
	}
	id, ok := docgen.ParseRefLink(dest)
	if !ok {
		if docgen.IsValidRefShape(dest) {
			a.status = fmt.Sprintf(dm.RequestMissing, dest)
			return true // it is ours, but broken: keep B09 away from it
		}
		return false
	}
	entry, ok := ps.project.Requests[id]
	if !ok || entry == nil {
		a.status = fmt.Sprintf(dm.RequestMissing, id)
		return true
	}
	ps.openRequest(entry)
	a.status = fmt.Sprintf(dm.OpenRequest, id)
	return true
}

// docgenPreviewSource expands the docgen references of src for the preview,
// caching by digest. The B09 preview options are unchanged; only the text is
// the derived one, and a document without references renders identically.
func (a *App) docgenPreviewSource(root ui.Element, tab *Tab, src string) string {
	st := derivedDocStateFor(root, tab)
	var key strings.Builder
	key.WriteString(src)
	key.WriteString("\x00" + a.cardLabelsFor().Interface)
	if tab.Owner != nil && tab.Owner.project != nil {
		for _, ref := range docgen.ScanDocument(src) {
			key.WriteString("\x00" + ref.ID)
			if entry := tab.Owner.project.Requests[ref.ID]; entry != nil {
				key.WriteString("\x00" + entry.BaselineDigest)
			}
		}
	}
	digest := core.Digest([]byte(key.String()))
	if st.previewDigest == digest {
		return st.previewContent
	}
	st.previewDigest = digest
	ps := tab.Owner
	if ps == nil || ps.project == nil || !docgen.ExpandedSourceHasRefs(src) {
		st.previewContent, st.previewRefs = src, false
		return src
	}
	exp, err := docgen.ExpandDoc(ps.project, src, docgen.CardOptions{Labels: a.cardLabelsFor()})
	if err != nil || !exp.Changed {
		st.previewContent, st.previewRefs = src, false
		return src
	}
	st.previewContent, st.previewRefs = exp.Markdown, true
	return exp.Markdown
}
