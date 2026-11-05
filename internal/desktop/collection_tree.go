package desktop

import (
	"github.com/egoist/mygo/ui"

	"gorequest/internal/core"
)

// This file draws the left "Requests" tree and the "Documents" group of the
// current project. It renders the real folder hierarchy (root requests, folders,
// subfolders, and the requests inside them) from core.Project.Collections, so a
// folder created or a request moved this session shows up immediately without
// reopening the project.

// projectContents draws the project's request tree and documents into the nav.
func (a *App) projectContents(c *ui.Context, t *ui.Theme) {
	ps := a.ps
	if ps == nil {
		return
	}
	m := a.msg()
	ui.Column(c).Gap(1).FillWidth().Children(func() {
		ui.Row(c).Padding(6, 8, 2, 8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, m.RequestsGroup).FontSize(11).Bold().TextColor(t.TextMuted).Grow(1)
			if ui.Button(c, m.AddFolder).Tooltip(m.AddFolderTip).Clicked() {
				a.openNewFolder()
			}
		})

		for _, e := range ps.project.RootRequests() {
			a.requestRow(c, t, ps, e, 0)
		}
		for _, n := range ps.project.Collections {
			a.folderNode(c, t, ps, n, 0)
		}
		if len(ps.project.Requests) == 0 {
			ui.Text(c, m.NoRequests).FontSize(12).TextColor(t.TextMuted).Padding(2, 10)
		}

		ui.Row(c).Padding(10, 8, 2, 8).Children(func() {
			ui.Text(c, m.DocumentsGroup).FontSize(11).Bold().TextColor(t.TextMuted)
		})
		if len(ps.project.Docs) == 0 {
			ui.Text(c, m.NoDocuments).FontSize(12).TextColor(t.TextMuted).Padding(2, 10)
		}
		for _, d := range ps.project.Docs {
			a.docRow(c, t, ps, d)
		}
	})
}

// requestRow draws one request. The method and path are shown in the tooltip so
// two requests with the same name remain distinguishable. The current request is
// highlighted.
func (a *App) requestRow(c *ui.Context, t *ui.Theme, ps *projectState, e *core.RequestEntry, depth int) {
	id := "req:" + e.Request.ID
	sel := ps.sel == id
	bg := t.Background
	if sel {
		bg = t.Surface
	}
	label := e.Request.Name
	if e.Request.HTTP != nil && e.Request.HTTP.Method != "" {
		label = e.Request.HTTP.Method + "  " + label
	}
	tip := e.Request.ID
	if e.Request.HTTP != nil {
		tip = e.Request.HTTP.Method + " " + e.Request.HTTP.URL + "\n" + e.RelPath
	}
	ui.Row(c).Key(id).FillWidth().AlignItems(ui.Center).Background(bg).
		Padding(2, float32(8+depth*14)).Children(func() {
		b := ui.Button(c, label).FillWidth().Tooltip(tip)
		if sel {
			b.Bold()
		}
		if b.Clicked() {
			ps.sel = id
			if err := ps.openSelection(id); err != nil {
				a.status = err.Error()
			}
		}
	})
}

// folderNode draws a folder header, then—when expanded—its requests and child
// folders, indented. Clicking a folder toggles it and selects it as the target
// for new requests.
func (a *App) folderNode(c *ui.Context, t *ui.Theme, ps *projectState, n *core.CollectionEntry, depth int) {
	open := ps.expanded[n.RelPath]
	sel := ps.sel == "folder:"+n.RelPath
	bg := t.Background
	if sel {
		bg = t.Surface
	}
	arrow := "▸"
	if open {
		arrow = "▾"
	}
	label := arrow + " " + n.Name
	ui.Row(c).Key("folder-node:"+n.RelPath).FillWidth().AlignItems(ui.Center).Background(bg).
		Padding(2, float32(8+depth*14)).Children(func() {
		b := ui.Button(c, label).FillWidth().Tooltip(translate(a.msg().FolderTip, collectionRelOf(n.RelPath)))
		if sel {
			b.Bold()
		}
		if b.Clicked() {
			ps.expanded[n.RelPath] = !open
			ps.sel = "folder:" + n.RelPath
		}
	})
	if !open {
		return
	}
	for _, e := range n.Requests {
		a.requestRow(c, t, ps, e, depth+1)
	}
	for _, ch := range n.Children {
		a.folderNode(c, t, ps, ch, depth+1)
	}
}

// docRow draws one document and opens it on click.
func (a *App) docRow(c *ui.Context, t *ui.Theme, ps *projectState, d *core.DocEntry) {
	id := "doc:" + d.RelPath
	sel := ps.sel == id
	bg := t.Background
	if sel {
		bg = t.Surface
	}
	ui.Row(c).Key(id).FillWidth().AlignItems(ui.Center).Background(bg).Padding(2, 8).Children(func() {
		b := ui.Button(c, d.Name).FillWidth().Tooltip(d.RelPath)
		if sel {
			b.Bold()
		}
		if b.Clicked() {
			ps.sel = id
			if err := ps.openSelection(id); err != nil {
				a.status = err.Error()
			}
		}
	})
}
