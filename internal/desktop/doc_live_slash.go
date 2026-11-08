package desktop

import (
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"gorequest/internal/core"
	"gorequest/internal/desktop/uikit"
	"gorequest/internal/docedit"
	"gorequest/internal/docgen"
	docmd "gorequest/internal/markdown"
)

// docSlashMenu draws the canvas slash menu below the active block. It is a
// native, themed list: Up/Down move, Enter chooses, Escape closes, and the
// pointer can click an entry. It never takes a key while an input method is
// composing, because composition keys are the input method's and do not reach
// the editor's OnInput.
func (a *App) docSlashMenu(c *ui.Context, t *ui.Theme, tab *Tab, st *docViewState, dm *docLiveMessages) {
	lv := st.live
	if lv == nil {
		return
	}
	p := uikit.PaletteFor(t)
	menu := ui.Column(c).Key("doc-slash-menu").FillWidth().MaxHeight(300).Padding(4).Gap(1).Radius(8).
		Background(p.Sidebar).Border(1, p.Hairline).Shadow(0, 4, 14, 0, p.CardShade).
		KeepFocus().Transition(uikit.DropdownInlineTransition)
	menu.Label(dm.SlashMenu)
	menu.Children(func() {
		ui.Row(c).FillWidth().Height(24).Padding(0, 8).AlignItems(ui.Center).Children(func() {
			title := dm.SlashMenu
			if q := docSlashQueryText(st); q != "" {
				title += "  /" + q
			}
			ui.Text(c, title).FontSize(11).TextColor(p.Muted).SingleLine()
		})
		ui.Scroll(c).MaxHeight(260).FillWidth().Children(func() {
			if lv.slashMode == 1 {
				a.docSlashRequestRows(c, p, tab, st, dm)
				return
			}
			if len(lv.slashItems) == 0 {
				ui.Text(c, dm.NoMatches).FontSize(12).TextColor(p.Faint).Padding(6, 8)
				return
			}
			for i, cmd := range lv.slashItems {
				i, cmd := i, cmd
				row := ui.Row(c).FillWidth().Height(30).Padding(0, 8).Gap(8).AlignItems(ui.Center).Radius(4)
				if i == lv.slashIndex {
					row.Background(p.Press)
				} else if row.Hovered() {
					row.Background(p.Hover)
				}
				row.Children(func() {
					ui.Text(c, a.docSlashLabel(cmd)).FontSize(13).TextColor(p.Text).SingleLine()
				})
				row.Label(a.docSlashLabel(cmd))
				if row.Clicked() {
					lv.slashIndex = i
					a.docSlashChoose(tab, st)
				}
			}
		})
	})
}

// docSlashRequestRows draws the second-level request picker.
func (a *App) docSlashRequestRows(c *ui.Context, p uikit.Palette, tab *Tab, st *docViewState, dm *docLiveMessages) {
	lv := st.live
	if len(lv.slashRequest) == 0 {
		note := dm.NoRequests
		if a.docSlashSkippedInvalid(tab) {
			note = dm.InvalidIDs
		}
		ui.Text(c, note).FontSize(12).TextColor(p.Faint).Padding(6, 8)
		return
	}
	for i, entry := range lv.slashRequest {
		i, entry := i, entry
		method := docRequestMethod(entry)
		row := ui.Row(c).FillWidth().Height(32).Padding(0, 8).Gap(8).AlignItems(ui.Center).Radius(4)
		if i == lv.slashIndex {
			row.Background(p.Press)
		} else if row.Hovered() {
			row.Background(p.Hover)
		}
		row.Children(func() {
			ui.Text(c, method).Font("monospace").FontSize(11).FontWeight(700).
				TextColor(uikit.MethodColor(method, p)).Width(44).SingleLine()
			name := entry.Request.Name
			if name == "" {
				name = entry.Request.ID
			}
			ui.Text(c, name).FontSize(13).TextColor(p.Text).Grow(1).SingleLine()
			ui.Text(c, docRequestDisplayPath(entry)).FontSize(11).TextColor(p.Muted).SingleLine()
		})
		label := method + " " + entry.Request.Name
		if entry.Request.Name == "" {
			label = method + " " + entry.Request.ID
		}
		row.Label(label)
		if row.Clicked() {
			lv.slashIndex = i
			a.docSlashChoose(tab, st)
		}
	}
}

// --- slash menu -----------------------------------------------------------

// refreshDocSlash derives the slash menu from the active block's text. It
// opens only on a logical line whose first content is a command query, judged
// at the caret, so a new line made with Enter works without leaving the
// block; code, URLs and paths never match.
func (a *App) refreshDocSlash(tab *Tab, st *docViewState, blocks []docedit.Block) {
	lv := st.live
	if lv == nil || lv.block == nil {
		if lv != nil {
			lv.slashOpen = false
		}
		return
	}
	be := lv.block
	switch be.kind {
	case docmd.CodeBlock, docmd.HTMLBlock, docmd.Table, docmd.MathBlock, docmd.FootnoteDefinition:
		lv.slashOpen = false
		lv.slashMode = 0
		return
	}
	query, ok := docedit.SlashQueryAt(be.ed.Buffer.String(), be.ed.Cursor())
	if !ok {
		lv.slashOpen = false
		lv.slashMode = 0
		lv.slashIndex = 0
		lv.slashDismissed = ""
		lv.slashDismissedSet = false
		return
	}
	if lv.slashDismissedSet && lv.slashDismissed == query {
		lv.slashOpen = false
		return
	}
	if lv.slashMode == 1 {
		word, _, _ := strings.Cut(strings.TrimSpace(query), " ")
		if !strings.EqualFold(word, "request") && word != "接口" && word != "引用" {
			lv.slashMode = 0
			lv.slashIndex = 0
		}
	}
	lv.slashOpen = true
	lv.slashQuery = query
	if lv.slashMode == 1 {
		lv.slashRequest = a.docSlashRequestItems(tab, docedit.SlashRest(query, docSlashRequestCommand()))
		if lv.slashIndex >= len(lv.slashRequest) {
			lv.slashIndex = max(0, len(lv.slashRequest)-1)
		}
		return
	}
	filter := query
	if word, rest, ok := strings.Cut(query, " "); ok && strings.TrimSpace(rest) != "" {
		// "/h2 Hello" filters by the command word and keeps "Hello" as the
		// heading text. A multi-word name such as "heading 2" filters whole.
		if _, found := docedit.FindSlashCommand(word); found {
			filter = word
		}
	}
	lv.slashItems = docedit.FilterSlash(filter)
	if lv.slashIndex >= len(lv.slashItems) {
		lv.slashIndex = max(0, len(lv.slashItems)-1)
	}
}

// docSlashMove moves the menu's selection.
func (a *App) docSlashMove(st *docViewState, delta int) {
	lv := st.live
	if lv == nil {
		return
	}
	n := len(lv.slashItems)
	if lv.slashMode == 1 {
		n = len(lv.slashRequest)
	}
	if n == 0 {
		return
	}
	lv.slashIndex = (lv.slashIndex + delta + n) % n
}

// docSlashChoose applies the selected menu entry. The request entry opens a
// second-level picker instead of inserting text.
func (a *App) docSlashChoose(tab *Tab, st *docViewState) {
	lv := st.live
	be := lv.block
	if be == nil {
		return
	}
	if lv.slashMode == 1 {
		if len(lv.slashRequest) == 0 {
			return
		}
		entry := lv.slashRequest[lv.slashIndex]
		label := "@" + docRequestDisplayPath(entry)
		text := docgen.FormatRefLink(entry.Request.ID, label)
		a.applyDocSlashText(tab, st, text, len(text), len(text))
		return
	}
	if len(lv.slashItems) == 0 {
		return
	}
	cmd := lv.slashItems[lv.slashIndex]
	if cmd.Kind == docedit.SlashRequest {
		lv.slashMode = 1
		lv.slashIndex = 0
		lv.slashRequest = a.docSlashRequestItems(tab, docedit.SlashRest(lv.slashQuery, cmd))
		return
	}
	res := docedit.ApplySlash(cmd, docedit.SlashRest(lv.slashQuery, cmd))
	a.applyDocSlashText(tab, st, res.Text, res.SelStart, res.SelEnd)
}

// applyDocSlashText replaces the logical line holding the caret with text in
// one canonical transaction, then keeps editing the block that now holds the
// caret. The rest of the block stays byte-identical, so a slash command on a
// new line of a paragraph does not rewrite the lines around it.
func (a *App) applyDocSlashText(tab *Tab, st *docViewState, text string, selStart, selEnd int) {
	lv := st.live
	be := lv.block
	if be == nil {
		return
	}
	local := be.ed.Buffer.String()
	caret := be.ed.Cursor()
	lineStart := docedit.LineStart(local, caret)
	lineEnd := docedit.LineEnd(local, caret)
	if lineEnd > lineStart && local[lineEnd-1] == '\n' {
		lineEnd--
		if lineEnd > lineStart && local[lineEnd-1] == '\r' {
			lineEnd--
		}
	}
	newText := local[:lineStart] + text + local[lineEnd:]
	if !be.synthetic && be.terminator {
		newText += "\n"
	}
	if be.end == tab.DocEditor.Buffer.Len() && !be.terminator && newText != "" && !strings.HasSuffix(newText, "\n") {
		// A block created at the end of the file keeps the usual final
		// newline.
		newText += "\n"
	}
	tab.DocEditor.Buffer.Edit(editor.Edit{Start: be.start, End: be.end, Text: newText})
	lv.slashOpen = false
	lv.slashMode = 0
	lv.slashIndex = 0
	lv.slashDismissed = ""
	lv.slashDismissedSet = false
	// The selection is in the applied text, which begins at lineStart of the
	// block.
	base := be.start + lineStart
	a.relocateDocBlock(tab, st, base+selStart)
	if b := lv.block; b != nil {
		s := base + selStart - b.start
		e := base + selEnd - b.start
		s = max(0, min(s, len(b.base)))
		e = max(s, min(e, len(b.base)))
		b.ed.Select(s, e)
		b.focusPending = true
	}
}

// docSlashRequestCommand is the catalog entry that opens the request picker.
func docSlashRequestCommand() docedit.SlashCommand {
	for _, cmd := range docedit.SlashCommands() {
		if cmd.Kind == docedit.SlashRequest {
			return cmd
		}
	}
	return docedit.SlashCommand{Kind: docedit.SlashRequest}
}

// docSlashRequestItems lists the project's requests for the picker, filtered
// by name, path, ID, method and URL. It is searchable rather than a fixed
// unsearchable first page.
func (a *App) docSlashRequestItems(tab *Tab, query string) []*core.RequestEntry {
	if tab == nil || tab.Owner == nil || tab.Owner.project == nil {
		return nil
	}
	all := allRequests(tab.Owner.project)
	terms := strings.Fields(strings.ToLower(strings.TrimSpace(query)))
	out := make([]*core.RequestEntry, 0, min(len(all), 60))
	for _, e := range all {
		if e == nil || e.Request == nil || !docgen.ValidRequestID(e.Request.ID) {
			continue
		}
		hay := strings.ToLower(e.Request.Name + " " + e.Request.ID + " " + e.RelPath + " " + docRequestMethod(e) + " " + docRequestURL(e))
		ok := true
		for _, t := range terms {
			if !strings.Contains(hay, t) {
				ok = false
				break
			}
		}
		if !ok {
			continue
		}
		out = append(out, e)
		if len(out) >= 60 {
			break
		}
	}
	return out
}

// docSlashSkippedInvalid reports whether the project has requests whose IDs
// documents cannot reference, so the picker can say why they are missing.
func (a *App) docSlashSkippedInvalid(tab *Tab) bool {
	if tab == nil || tab.Owner == nil || tab.Owner.project == nil {
		return false
	}
	for _, e := range tab.Owner.project.Requests {
		if e != nil && e.Request != nil && !docgen.ValidRequestID(e.Request.ID) {
			return true
		}
	}
	return false
}

// docRequestDisplayPath is the folder/name label a request reference shows.
func docRequestDisplayPath(e *core.RequestEntry) string {
	if e == nil {
		return ""
	}
	p := strings.TrimPrefix(e.RelPath, "collections/")
	p = strings.TrimSuffix(p, ".yaml")
	if p == "" {
		if e.Request != nil {
			return e.Request.Name
		}
		return ""
	}
	return p
}

func docRequestMethod(e *core.RequestEntry) string {
	if e != nil && e.Request != nil && e.Request.HTTP != nil && e.Request.HTTP.Method != "" {
		return e.Request.HTTP.Method
	}
	return "GET"
}

func docRequestURL(e *core.RequestEntry) string {
	if e != nil && e.Request != nil && e.Request.HTTP != nil {
		return e.Request.HTTP.URL
	}
	return ""
}
