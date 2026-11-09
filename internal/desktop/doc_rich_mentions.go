package desktop

import (
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"gorequest/internal/docmention"
)

func (a *App) docRichMentionItems(be *docBlockEdit) (int, []editor.Completion) {
	if be.rich == nil || be.rich.marked {
		return 0, nil
	}
	src := be.ed.Buffer.String()
	trigger, ok := docmention.FindTrigger(src, be.ed.Cursor())
	if !ok || src == be.rich.mentionDismissed {
		return 0, nil
	}
	session := docMentionSessionFor(be.ed)
	if session == nil {
		return 0, nil
	}
	reqs := docmention.Collect(session.project())
	query := src[trigger.Start+1 : be.ed.Cursor()]
	items, _ := session.buildItems(docmention.Search(reqs, query), query, len(reqs))
	return trigger.Start, items
}
func (a *App) docRichChooseMention(tab *Tab, st *docViewState, be *docBlockEdit, start int, items []editor.Completion) {
	if len(items) == 0 {
		return
	}
	w := be.rich
	index := max(0, min(w.mentionIndex, len(items)-1))
	be.ed.Select(start, be.ed.Cursor())
	be.ed.Insert(items[index].Insert)
	a.commitDocBlock(tab, st)
	w.mentionIndex = 0
	w.refresh()
}
func (a *App) docRichMentionInput(tab *Tab, st *docViewState, be *docBlockEdit, ev ui.InputEvent) bool {
	if ev.Kind != ui.InputKeyDown {
		return false
	}
	start, items := a.docRichMentionItems(be)
	if len(items) == 0 {
		return false
	}
	w := be.rich
	switch ev.Key {
	case ui.KeyUp:
		w.mentionIndex = max(0, w.mentionIndex-1)
		return true
	case ui.KeyDown:
		w.mentionIndex = min(len(items)-1, w.mentionIndex+1)
		return true
	case ui.KeyEnter, ui.KeyTab:
		a.docRichChooseMention(tab, st, be, start, items)
		return true
	case ui.KeyEscape:
		w.mentionDismissed = be.ed.Buffer.String()
		return true
	}
	return false
}
func (a *App) docRichMentionMenu(c *ui.Context, t *ui.Theme, tab *Tab, st *docViewState, be *docBlockEdit) {
	start, items := a.docRichMentionItems(be)
	if len(items) == 0 {
		return
	}
	ui.Column(c).FillWidth().MaxHeight(240).Background(t.Surface).Border(1, t.Border).KeepFocus().Children(func() {
		ui.Scroll(c).FillWidth().Children(func() {
			for i, item := range items {
				if i >= docMentionMaxRows {
					break
				}
				row := ui.Row(c).FillWidth().Padding(6, 8).KeepFocus()
				if i == be.rich.mentionIndex {
					row.Background(t.Selection)
				}
				row.Children(func() {
					ui.Text(c, item.Label).FontSize(13)
					ui.Text(c, item.Detail).FontSize(11).TextColor(t.TextMuted)
				})
				if row.Clicked() {
					be.rich.mentionIndex = i
					a.docRichChooseMention(tab, st, be, start, items)
				}
			}
		})
	})
}
