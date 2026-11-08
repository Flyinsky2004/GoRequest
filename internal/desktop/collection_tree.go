package desktop

import (
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"gorequest/internal/core"
	"gorequest/internal/desktop/uikit"
)

// This file draws the left "Requests" tree and the "Documents" group of the
// current project. It renders the real folder hierarchy (root requests, folders,
// subfolders, and the requests inside them) from core.Project.Collections, so a
// folder created or a request moved this session shows up immediately without
// reopening the project.
//
// Everything here is a row, never a card or a button: left-aligned, indented per
// depth, hover/selection shown by a background wash only (§3). Section headers
// reveal their "+" actions on hover and fold their whole section when clicked.

// rowTransition animates a tree row in as its folder opens: fade + a 4 DIP rise,
// staggered 20ms per row (§6.3).
func rowTransition(order int) ui.ElementTransition {
	return ui.ElementTransition{
		Animation: &ui.Animation{
			Spring: uikit.SpringExpand.Spring,
			Delay:  time.Duration(order) * uikit.StaggerStep,
		},
		Enter: &ui.Motion{Y: 4, Opacity: 0},
	}
}

// childrenTransition collapses a folder's children to nothing on close and
// grows them back on open, following Snappy (§6.3).
func childrenTransition() ui.ElementTransition {
	return ui.ElementTransition{
		Animation: &ui.Animation{Spring: uikit.SpringExpand.Spring},
		Enter:     &ui.Motion{Opacity: 0, Collapse: true},
		Exit:      &ui.Motion{Collapse: true},
	}
}

// --- filter helpers -------------------------------------------------------

// filtering reports whether the sidebar filter is active.
func (a *App) filtering() bool { return strings.TrimSpace(a.filterQuery) != "" }

// matchText reports whether text contains the current filter query,
// case-insensitively. An empty query matches everything.
func (a *App) matchText(text string) bool {
	q := strings.TrimSpace(a.filterQuery)
	if q == "" {
		return true
	}
	return strings.Contains(strings.ToLower(text), strings.ToLower(q))
}

func (a *App) reqMatch(e *core.RequestEntry) bool {
	// The shared Core matcher searches name, method, URL and body text, so the
	// sidebar filter and any future result list agree on what "matches" means.
	return core.MatchRequest(e, a.filterQuery)
}

func (a *App) docMatch(d *core.DocEntry) bool {
	return core.MatchDoc(d, a.filterQuery)
}

// folderMatch reports whether a folder or any descendant request/document
// matches the current filter.
func (a *App) folderMatch(n *core.CollectionEntry) bool {
	if n == nil {
		return false
	}
	if a.matchText(n.Name) {
		return true
	}
	for _, e := range n.Requests {
		if a.reqMatch(e) {
			return true
		}
	}
	for _, ch := range n.Children {
		if a.folderMatch(ch) {
			return true
		}
	}
	return false
}

// nameSpans turns a label into rich-text spans, highlighting the matched filter
// substring with an accent wash (§3).
func (a *App) nameSpans(p uikit.Palette, text string, weight int, col ui.Color) []ui.Span {
	base := ui.Span{Text: text, Size: 13, Weight: weight, Color: col}
	q := strings.TrimSpace(a.filterQuery)
	if q == "" {
		return []ui.Span{base}
	}
	low, lq := strings.ToLower(text), strings.ToLower(q)
	var spans []ui.Span
	i := 0
	for i < len(text) {
		j := strings.Index(low[i:], lq)
		if j < 0 {
			break
		}
		j += i
		if j > i {
			spans = append(spans, ui.Span{Text: text[i:j], Size: 13, Weight: weight, Color: col})
		}
		spans = append(spans, ui.Span{
			Text: text[j : j+len(q)], Size: 13, Weight: weight, Color: col,
			Background: p.Selection, Radius: 2,
		})
		i = j + len(q)
	}
	if i < len(text) {
		spans = append(spans, ui.Span{Text: text[i:], Size: 13, Weight: weight, Color: col})
	}
	if len(spans) == 0 {
		return []ui.Span{base}
	}
	return spans
}

// --- sections -------------------------------------------------------------

// sectionAction is one hover-revealed action in a section header.
type sectionAction struct {
	icon *ui.SVG
	tip  string
	fn   func()
}

// sectionHeader draws a collapsible section title. Clicking the title folds the
// section; hovering reveals its action icons at the right edge, absolutely
// positioned so they never take space when hidden.
func (a *App) sectionHeader(c *ui.Context, p uikit.Palette, title, key string, actions []sectionAction) {
	ps := a.ps
	if ps.collapsed == nil {
		ps.collapsed = map[string]bool{}
	}
	folded := ps.collapsed[key]
	row := ui.Row(c).Padding(8, 8, 2, 8).AlignItems(ui.Center).Height(28).FillWidth()
	hovered := row.Hovered()
	if hovered {
		row.Background(p.Hover)
	}
	row.Children(func() {
		b := ui.Row(c).Grow(1).Height(24).AlignItems(ui.Center).Gap(4)
		b.Children(func() {
			chev := ui.Icon(c, uikit.IconChevron).FontSize(11).TextColor(p.Muted)
			rot := float32(0)
			if folded {
				rot = -90
			}
			chev.AnimatePose(ui.Pose{Rotate: rot}, ui.Snappy)
			ui.Text(c, title).FontSize(11).FontWeight(600).TextColor(p.Muted).SingleLine()
		})
		b.Label("section:" + title)
		if uikit.ActionCursor(b).Clicked() {
			ps.collapsed[key] = !folded
		}
		if hovered {
			for i := len(actions) - 1; i >= 0; i-- {
				ac := actions[i]
				ib := uikit.IconButton(c, p, ac.icon, ac.tip)
				ib.Absolute().Right(float32(6 + (len(actions)-1-i)*30)).Top(-2)
				if ib.Clicked() {
					ac.fn()
				}
			}
		}
	})
}

// projectContents draws the project's request tree and documents into the nav.
func (a *App) projectContents(c *ui.Context, t *ui.Theme) {
	ps := a.ps
	if ps == nil {
		return
	}
	m := a.msg()
	p := uikit.PaletteFor(t)
	filtering := a.filtering()
	reqShown, folderShown, docShown := 0, 0, 0

	// The whole list accepts a dragged request as a drop on the root: the
	// innermost accepting row (a folder) wins over this outer target.
	list := ui.Column(c).Gap(1).FillWidth()
	if _, ok := ui.DragOver[draggedRequest](list); ok {
		list.Background(p.Focus.Alpha(0.10))
	}
	if dropped, ok := ui.Drop[draggedRequest](list); ok {
		a.moveRequestByID(dropped.id, "")
	}
	list.Children(func() {
		// Requests.
		a.sectionHeader(c, p, m.RequestsGroup, "requests", []sectionAction{
			{icon: uikit.IconPlus, tip: m.NewRequest, fn: a.openNewRequest},
			{icon: uikit.IconFolder, tip: m.AddFolderTip, fn: a.openNewFolder},
		})
		if filtering || !ps.collapsed["requests"] {
			for i, e := range ps.project.RootRequests() {
				if !filtering || a.reqMatch(e) {
					a.requestRow(c, t, ps, e, 0, i)
					reqShown++
				}
			}
			for i, n := range ps.project.Collections {
				if !filtering || a.folderMatch(n) {
					a.folderNode(c, t, ps, n, 0, i)
					folderShown++
				}
			}
			if !filtering && len(ps.project.Requests) == 0 {
				ui.Text(c, m.NoRequests).FontSize(12).TextColor(p.Muted).Padding(2, 10)
			}
		}

		// Documents.
		a.sectionHeader(c, p, m.DocumentsGroup, "docs", []sectionAction{
			{icon: uikit.IconFile, tip: m.AddDocTip, fn: func() {
				if de, err := ps.newDoc(); err != nil {
					a.status = translate(m.NewDocFailed, err.Error())
				} else {
					a.status = translate(m.Created, de.RelPath)
				}
			}},
		})
		if !(!filtering && ps.collapsed["docs"]) {
			for i, d := range ps.project.Docs {
				if !filtering || a.docMatch(d) {
					a.docRow(c, t, ps, d, i)
					docShown++
				}
			}
			if !filtering && len(ps.project.Docs) == 0 {
				ui.Text(c, m.NoDocuments).FontSize(12).TextColor(p.Muted).Padding(2, 10)
			}
		}

		if filtering && reqShown+folderShown+docShown == 0 {
			ui.Text(c, m.NoMatch).FontSize(12).TextColor(p.Faint).Padding(2, 10)
		}
	})
}

// requestRow draws one request as a row: hover/selection background, a
// method-colored tag and the name (filter hits highlighted). The method and path
// are shown in the tooltip so two same-named requests stay distinguishable.
func (a *App) requestRow(c *ui.Context, t *ui.Theme, ps *projectState, e *core.RequestEntry, depth, order int) {
	id := "req:" + e.Request.ID
	sel := ps.sel == id
	p := uikit.PaletteFor(t)
	tip := e.Request.ID
	if e.Request.HTTP != nil {
		tip = e.Request.HTTP.Method + " " + e.Request.HTTP.URL + "\n" + e.RelPath
	}
	row := ui.Row(c).Key(id).FillWidth().AlignItems(ui.Center).Height(30).
		Padding(0, float32(8+depth*14)).Transition(rowTransition(order)).Clip().Tooltip(tip)
	if row.Hovered() {
		row.Background(p.Hover)
	} else if sel {
		row.Background(p.Press)
	}
	if over, ok := ui.DragOver[draggedRequest](row); ok && over.id != e.Request.ID {
		row.Background(p.Focus.Alpha(0.16))
	}
	if uikit.ActionCursor(row).Clicked() {
		ps.sel = id
		if err := ps.openSelection(id); err != nil {
			a.status = err.Error()
		}
	}
	row.Children(func() {
		a.drawTreeLabel(c, p, e, sel, id)
	})
	row.PopoverMenu(func(mm *ui.Menu) { a.requestMenu(mm, e, id) })
	// Request rows are drag sources; accepting a drop on another request row
	// moves the dragged request into that row's folder (so releasing over a row
	// never falls through to the root target).
	row.Drag(draggedRequest{id: e.Request.ID})
	if dropped, ok := ui.Drop[draggedRequest](row); ok && dropped.id != e.Request.ID {
		a.moveRequestByID(dropped.id, folderRelOfPath(e.RelPath))
	}
}

func (a *App) drawTreeLabel(c *ui.Context, p uikit.Palette, e *core.RequestEntry, sel bool, selID string) {
	weight := 500
	col := p.Text
	if sel {
		weight = 600
	}
	if e.Request.HTTP != nil && e.Request.HTTP.Method != "" {
		ui.Text(c, e.Request.HTTP.Method).Font("monospace").FontWeight(700).FontSize(11).
			TextColor(uikit.MethodColor(e.Request.HTTP.Method, p)).Width(42).SingleLine()
	}
	a.treeRowLabel(c, selID, a.nameSpans(p, e.Request.Name, weight, col))
}

// folderNode draws a folder as a row with a rotating chevron and folder icon,
// then—when expanded—its requests and child folders, indented. Clicking toggles
// and selects it as the target for new requests. While filtering, matching
// folders are shown open so hits are visible.
func (a *App) folderNode(c *ui.Context, t *ui.Theme, ps *projectState, n *core.CollectionEntry, depth, order int) {
	filtering := a.filtering()
	open := ps.expanded[n.RelPath] || filtering
	selID := "folder:" + n.RelPath
	sel := ps.sel == selID
	p := uikit.PaletteFor(t)

	row := ui.Row(c).Key("folder-node:"+n.RelPath).FillWidth().AlignItems(ui.Center).Height(30).
		Padding(0, float32(8+depth*14)).Transition(rowTransition(order)).Clip()
	if row.Hovered() {
		row.Background(p.Hover)
	} else if sel {
		row.Background(p.Press)
	}
	if _, ok := ui.DragOver[draggedRequest](row); ok {
		row.Background(p.Focus.Alpha(0.18))
	}
	row.Children(func() {
		b := ui.Row(c).Grow(1).Height(30).Gap(6).AlignItems(ui.Center)
		b.Children(func() {
			chev := ui.Icon(c, uikit.IconChevron).FontSize(12).TextColor(p.Muted)
			rot := float32(0)
			if !ps.expanded[n.RelPath] {
				rot = -90
			}
			chev.AnimatePose(ui.Pose{Rotate: rot}, ui.Snappy)
			ui.Icon(c, uikit.IconFolder).FontSize(13).TextColor(p.Muted)
			weight := 500
			if sel {
				weight = 600
			}
			a.treeRowLabel(c, selID, a.nameSpans(p, n.Name, weight, p.Text))
		})
		b.Label(n.Name)
		if uikit.ActionCursor(b).Clicked() {
			ps.expanded[n.RelPath] = !ps.expanded[n.RelPath]
			ps.sel = selID
		}
		if row.Hovered() {
			plus := uikit.IconButton(c, p, uikit.IconPlus, a.msg().NewRequest)
			plus.Absolute().Right(6).Top(-2)
			if plus.Clicked() {
				ps.sel = selID
				a.openNewRequest()
			}
		}
	})
	row.PopoverMenu(func(mm *ui.Menu) { a.folderMenu(mm, n, selID) })
	if dropped, ok := ui.Drop[draggedRequest](row); ok {
		a.moveRequestByID(dropped.id, collectionRelOf(n.RelPath))
	}
	if !open {
		return
	}
	ui.Column(c).Key("kids:" + n.RelPath).FillWidth().Transition(childrenTransition()).Children(func() {
		for i, e := range n.Requests {
			if !filtering || a.reqMatch(e) {
				a.requestRow(c, t, ps, e, depth+1, i)
			}
		}
		for i, ch := range n.Children {
			if !filtering || a.folderMatch(ch) {
				a.folderNode(c, t, ps, ch, depth+1, i)
			}
		}
	})
}

// docRow draws one document as a row with a file icon and opens it on click.
func (a *App) docRow(c *ui.Context, t *ui.Theme, ps *projectState, d *core.DocEntry, order int) {
	id := "doc:" + d.RelPath
	sel := ps.sel == id
	p := uikit.PaletteFor(t)
	row := ui.Row(c).Key(id).FillWidth().AlignItems(ui.Center).Height(30).Padding(0, 8).
		Transition(rowTransition(order)).Clip()
	if row.Hovered() {
		row.Background(p.Hover)
	} else if sel {
		row.Background(p.Press)
	}
	row.Children(func() {
		b := ui.Row(c).Grow(1).Height(30).Gap(6).AlignItems(ui.Center)
		weight := 500
		if sel {
			weight = 600
		}
		b.Children(func() {
			ui.Icon(c, uikit.IconFile).FontSize(13).TextColor(p.Muted)
			a.treeRowLabel(c, id, a.nameSpans(p, d.Name, weight, p.Text))
		})
		b.Label(d.Name)
		if uikit.ActionCursor(b).Clicked() {
			ps.sel = id
			if err := ps.openSelection(id); err != nil {
				a.status = err.Error()
			}
		}
	})
	row.PopoverMenu(func(mm *ui.Menu) { a.docMenu(mm, d, id) })
}
