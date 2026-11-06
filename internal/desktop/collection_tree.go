package desktop

import (
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

// projectContents draws the project's request tree and documents into the nav.
func (a *App) projectContents(c *ui.Context, t *ui.Theme) {
	ps := a.ps
	if ps == nil {
		return
	}
	m := a.msg()
	p := uikit.PaletteFor(t)
	ui.Column(c).Gap(1).FillWidth().Children(func() {
		ui.Row(c).Padding(6, 8, 2, 8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, m.RequestsGroup).FontSize(11).FontWeight(600).TextColor(p.Muted).Grow(1)
			if ui.Button(c, m.AddFolder).Tooltip(m.AddFolderTip).Clicked() {
				a.openNewFolder()
			}
		})

		for i, e := range ps.project.RootRequests() {
			a.requestRow(c, t, ps, e, 0, i)
		}
		for i, n := range ps.project.Collections {
			a.folderNode(c, t, ps, n, 0, i)
		}
		if len(ps.project.Requests) == 0 {
			ui.Text(c, m.NoRequests).FontSize(12).TextColor(p.Muted).Padding(2, 10)
		}

		ui.Row(c).Padding(10, 8, 2, 8).Children(func() {
			ui.Text(c, m.DocumentsGroup).FontSize(11).FontWeight(600).TextColor(p.Muted)
		})
		if len(ps.project.Docs) == 0 {
			ui.Text(c, m.NoDocuments).FontSize(12).TextColor(p.Muted).Padding(2, 10)
		}
		for i, d := range ps.project.Docs {
			a.docRow(c, t, ps, d, i)
		}
	})
}

// requestRow draws one request. The method and path are shown in the tooltip so
// two requests with the same name remain distinguishable. The current request is
// highlighted.
func (a *App) requestRow(c *ui.Context, t *ui.Theme, ps *projectState, e *core.RequestEntry, depth, order int) {
	id := "req:" + e.Request.ID
	sel := ps.sel == id
	p := uikit.PaletteFor(t)
	label := e.Request.Name
	if e.Request.HTTP != nil && e.Request.HTTP.Method != "" {
		label = e.Request.HTTP.Method + "  " + label
	}
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
	if row.Clicked() {
		ps.sel = id
		if err := ps.openSelection(id); err != nil {
			a.status = err.Error()
		}
	}
	row.Children(func() {
		drawTreeLabel(c, p, e, label, sel)
	})
}

func drawTreeLabel(c *ui.Context, p uikit.Palette, e *core.RequestEntry, label string, sel bool) {
	weight := 500
	col := p.Text
	if sel {
		weight = 600
	}
	// Method color only when the entry has a method; the name stays text color.
	if e.Request.HTTP != nil && e.Request.HTTP.Method != "" {
		ui.Text(c, e.Request.HTTP.Method).Font("monospace").FontWeight(700).FontSize(11).
			TextColor(uikit.MethodColor(e.Request.HTTP.Method, p)).Width(42).SingleLine()
	}
	ui.Text(c, e.Request.Name).FontSize(13).FontWeight(weight).TextColor(col).Grow(1).SingleLine()
}

// folderNode draws a folder header, then—when expanded—its requests and child
// folders, indented. Clicking a folder toggles it and selects it as the target
// for new requests.
func (a *App) folderNode(c *ui.Context, t *ui.Theme, ps *projectState, n *core.CollectionEntry, depth, order int) {
	open := ps.expanded[n.RelPath]
	sel := ps.sel == "folder:"+n.RelPath
	p := uikit.PaletteFor(t)
	arrow := "▸"
	if open {
		arrow = "▾"
	}
	label := arrow + " " + n.Name
	row := ui.Row(c).Key("folder-node:"+n.RelPath).FillWidth().AlignItems(ui.Center).Height(30).
		Padding(0, float32(8+depth*14)).Transition(rowTransition(order)).Clip()
	if row.Hovered() {
		row.Background(p.Hover)
	} else if sel {
		row.Background(p.Press)
	}
	row.Children(func() {
		b := ui.Button(c, label).FillWidth().TextColor(p.Text).Background(ui.Transparent)
		b.Label(label)
		if b.Clicked() {
			ps.expanded[n.RelPath] = !open
			ps.sel = "folder:" + n.RelPath
		}
		if row.Hovered() {
			plus := uikit.IconButton(c, p, uikit.IconPlus, a.msg().NewRequest)
			plus.Absolute().Right(6).Top(3)
			if plus.Clicked() {
				ps.sel = "folder:" + n.RelPath
				a.openNewRequest()
			}
		}
	})
	if !open {
		return
	}
	ui.Column(c).Key("kids:" + n.RelPath).FillWidth().Transition(childrenTransition()).Children(func() {
		for i, e := range n.Requests {
			a.requestRow(c, t, ps, e, depth+1, i)
		}
		for i, ch := range n.Children {
			a.folderNode(c, t, ps, ch, depth+1, i)
		}
	})
}

// docRow draws one document and opens it on click.
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
		b := ui.Button(c, d.Name).FillWidth().Tooltip(d.RelPath).TextColor(p.Text).Background(ui.Transparent)
		if sel {
			b = b.Bold()
		}
		if b.Clicked() {
			ps.sel = id
			if err := ps.openSelection(id); err != nil {
				a.status = err.Error()
			}
		}
	})
}
