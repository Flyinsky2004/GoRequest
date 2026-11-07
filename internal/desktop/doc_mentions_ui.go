package desktop

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"gorequest/internal/core"
	"gorequest/internal/desktop/uikit"
	"gorequest/internal/docgen"
	"gorequest/internal/docmention"
)

// This file is the rendering and repair side of the mention plugin: the
// stale-reference strip, the re-pick menu, the read-only chip U06's canvas
// draws for a mention link, and the source replacement that keeps the label
// snapshot. U06 calls renderDocMentionTools from its canvas lifecycle; the
// plugin never edits documentation.go or another owner's files.

// renderDocMentionTools draws the stale-reference strip of the document in
// ed. It draws nothing when every reference resolves. The strip is read-only
// until the user picks a replacement: the source, including the original
// label, is preserved.
func (a *App) renderDocMentionTools(c *ui.Context, t *ui.Theme, root ui.Element, tab *Tab, ed *editor.State) {
	if a == nil || tab == nil || tab.Owner == nil || tab.Owner.project == nil || ed == nil {
		return
	}
	var stale []docmention.LinkRef
	for _, ref := range a.docMentionRefs(tab, ed) {
		if !ref.Found {
			stale = append(stale, ref)
		}
	}
	if len(stale) == 0 {
		return
	}
	p := uikit.PaletteFor(t)
	dm := a.docMentionMsg()
	ui.Row(c).Key("doc-mention-tools").FillWidth().Gap(8).Padding(4, 12).
		AlignItems(ui.Center).Background(p.Sidebar).
		BorderWidth(0, 0, 1, 0).BorderColor(p.Hairline).Children(func() {
		ui.Text(c, fmt.Sprintf(dm.StaleCount, len(stale))).FontSize(12).
			TextColor(uikit.MethodColor("POST", p)).SingleLine()
		for i, ref := range stale {
			if i >= docMentionMaxStaleButtons {
				break
			}
			ref := ref
			label := strings.TrimSpace(docmention.UnescapeLabel(ref.Label))
			if label == "" {
				label = ref.ID
			}
			uikit.ActionCursor(ui.MenuButton(c, fmt.Sprintf(dm.RepickFor, label), func(mm *ui.Menu) {
				a.docMentionRepickMenu(mm, tab, ed, ref)
			}))
		}
	})
}

// docMentionRefs returns the scanned links of ed, reusing the session cache
// when the plugin is installed on that editor.
func (a *App) docMentionRefs(tab *Tab, ed *editor.State) []docmention.LinkRef {
	if ed == nil || ed.Buffer == nil {
		return nil
	}
	if s := docMentionSessionFor(ed); s != nil {
		return s.links()
	}
	return docmention.ScanRequestLinks(tabProject(tab), ed.Buffer.String())
}

func tabProject(tab *Tab) *core.Project {
	if tab == nil || tab.Owner == nil {
		return nil
	}
	return tab.Owner.project
}

// docMentionRepickMenu lists the owner project's referenceable requests and
// replaces the destination of ref with the chosen ID. The label is not
// touched, so the document keeps its snapshot.
func (a *App) docMentionRepickMenu(mm *ui.Menu, tab *Tab, ed *editor.State, ref docmention.LinkRef) {
	if mm == nil || tab == nil || tab.Owner == nil || tab.Owner.project == nil || ed == nil {
		return
	}
	dm := a.docMentionMsg()
	count := 0
	for _, r := range docmention.Collect(tab.Owner.project) {
		if !r.Valid {
			continue
		}
		if count >= docMentionMaxRepairRows {
			mm.Separator()
			mm.Item(dm.MoreRequests).Disabled(true)
			break
		}
		r := r
		if mm.Item(r.Label() + " · " + r.ID).Chosen() {
			a.docMentionReplaceDest(tab, ed, ref, r.ID)
		}
		count++
	}
}

// docMentionReplaceDest rewrites only the destination span of ref, as one
// undo step. The visible label snapshot and the rest of the document are
// untouched.
func (a *App) docMentionReplaceDest(tab *Tab, ed *editor.State, ref docmention.LinkRef, newID string) {
	if ed == nil || ed.ReadOnly || !docgen.ValidRequestID(newID) {
		return
	}
	if ref.DestStart < 0 || ref.DestEnd > ed.Buffer.Len() || ref.DestStart > ref.DestEnd {
		return
	}
	dest := docgen.RefLinkPrefix + newID
	ed.Select(ref.DestStart, ref.DestEnd)
	ed.Insert(dest)
	a.status = fmt.Sprintf(a.docMentionMsg().Repicked, newID)
}

// docMentionChip renders a mention link as a read-only, clickable chip for
// U06's canvas: method color, the label snapshot, and a tooltip with the
// current name and address. A stale ID shows a warning mark and still keeps
// its text. Clicking opens or focuses the request tab; nothing is sent.
func (a *App) docMentionChip(c *ui.Context, t *ui.Theme, tab *Tab, label, dest string) ui.Element {
	p := uikit.PaletteFor(t)
	info := a.docMentionHover(tab, dest)
	row := ui.ButtonBase(c).Row().Gap(4).Padding(1, 4).Radius(t.Radius).AlignItems(ui.Center)
	row.Children(func() {
		if info.Method != "" {
			ui.Text(c, info.Method).FontSize(t.FontSize - 2).FontWeight(600).
				TextColor(uikit.MethodColor(info.Method, p)).SingleLine()
		}
		text := strings.TrimSpace(docmention.UnescapeLabel(label))
		if text == "" {
			text = info.Title
		}
		ui.Text(c, text).FontSize(t.FontSize - 1).TextColor(t.Text).SingleLine()
		if info.Stale {
			ui.Text(c, "!").FontSize(t.FontSize - 2).
				TextColor(uikit.MethodColor("DELETE", p)).SingleLine()
		}
	})
	row.Tooltip(info.Title + " · " + info.Detail)
	if uikit.ActionCursor(row).Clicked() {
		a.docMentionOpenLink(tab, dest)
	}
	return row
}
