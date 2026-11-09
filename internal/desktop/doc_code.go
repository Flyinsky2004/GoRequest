package desktop

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"gorequest/internal/desktop/uikit"
	"gorequest/internal/docedit"
)

func docCodeLanguage(source string) string {
	line, _, _ := strings.Cut(source, "\n")
	trimmed := strings.TrimLeft(line, " ")
	if len(line)-len(trimmed) > 3 {
		return ""
	}
	line = trimmed
	if len(line) < 3 || (line[0] != '`' && line[0] != '~') {
		return ""
	}
	info := strings.TrimLeft(line, string(line[0]))
	fields := strings.Fields(info)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

// Both states retain the same native editor, including its layout, pointer
// selection and horizontal scroll. Activation never translates a preview
// coordinate or replaces the input client after the first pointer press.
func (a *App) docCanvasCodeBlock(c *ui.Context, t *ui.Theme, tab *Tab, st *docViewState, b docedit.Block, be *docBlockEdit) {
	lv := docLiveFor(st)
	if lv.codeBlocks == nil {
		lv.codeBlocks = map[int]*docBlockEdit{}
	}
	if be == nil {
		be = lv.codeBlocks[b.Start]
		if be == nil || be.srcBase != b.EditText() {
			be = &docBlockEdit{}
			be.adopt(b)
			be.ed = a.newDocBlockEditor(t, tab, st, be, be.base)
			lv.codeBlocks[b.Start] = be
		}
	}
	lv.codeBlocks[be.start] = be
	a.syncDocBlockTheme(be, t)
	be.rich = nil
	be.initialClick = nil
	be.ed.ReadOnly = lv.block != be
	if lv.canvasOpts != nil {
		be.ed.SoftWrap = lv.canvasOpts.WrapCode
	}
	be.ed.OnInput = func(ev ui.InputEvent) bool {
		if ev.Kind == ui.InputPointerDown && lv.block != be {
			a.commitDocBlock(tab, st)
			lv.block = be
			be.focusPending = true
			lv.slashOpen = false
		}
		return a.docBlockInput(tab, st, be, ev)
	}
	th := a.docMarkdownTheme(t)
	pad := th.FontSize * th.CodeBlockPadding
	host := ui.Column(c).FillWidth().Padding(2, 8).Key(fmt.Sprintf("doc-code:%d", be.start))
	host.Label(docBlockLabel(docedit.Block{Start: be.start, Kind: be.kind}))
	host.Children(func() {
		ui.Column(c).FillWidth().Background(th.CodeBlockBackground.Over(uikit.PaletteFor(t).BG)).Border(1, th.CodeBlockBorder).Radius(th.CodeBlockRadius).Clip().Children(func() {
			ui.Row(c).Label("doc-code-header").FillWidth().AlignItems(ui.Center).Padding(th.FontSize*.35, th.FontSize*.4, 0, pad).Children(func() {
				ui.Text(c, docCodeLanguage(be.srcBase)).FontSize(th.FontSize * .8).TextColor(th.Muted).Unselectable()
				ui.Spacer(c)
				copyLabel := "复制"
				if a.interopLanguage() == langEnglish {
					copyLabel = "Copy"
				}
				copy := ui.ButtonBase(c).Label("doc-code-copy").Padding(th.FontSize*.2, th.FontSize*.45).Radius(5).Gap(th.FontSize * .3)
				if copy.Hovered() {
					copy.Background(th.Text.Alpha(.08))
				}
				copy.Children(func() {
					ui.Icon(c, uikit.IconCopy).Size(th.FontSize*.95, th.FontSize*.95).TextColor(th.Muted)
					ui.Text(c, copyLabel).FontSize(th.FontSize * .8).TextColor(th.Muted)
				})
				if copy.Clicked() {
					c.WriteClipboard(be.ed.Buffer.String())
				}
			})
			// editor.View contributes 4 DIP of text inset; compensate at the edge
			// so the code starts at the Markdown theme's padding in either state.
			ui.Column(c).FillWidth().Padding(th.FontSize*.25, max(0, pad-4), pad, max(0, pad-4)).Children(func() {
				view := editor.View(c, be.ed).FillWidth().Label("doc-canvas-editor")
				if be.focusPending {
					view.Focus()
					be.focusPending = false
					c.Invalidate()
				}
			})
		})
	})
}

// MyGo v0.7.4 keeps preedit in Buffer but exposes no State composition query.
// Read only this boolean through reflection (no unsafe, no private mutation).
// The version contract is exercised by TestM02NativeCompositionContract; remove
// this bridge when the upstream State exposes MarkedRange/Composing publicly.
func docCodeComposing(ed *editor.State) bool {
	return reflect.ValueOf(ed).Elem().FieldByName("ed").FieldByName("markedActive").Bool()
}

// Prefer the existing space indentation unit; tabs retain the usual four
// display columns. This changes typing behavior, never the saved whitespace.
func docCodeTabSize(text string) int {
	unit := 0
	for _, line := range strings.Split(text, "\n") {
		n := len(line) - len(strings.TrimLeft(line, " "))
		if n == 0 || strings.TrimSpace(line) == "" {
			continue
		}
		if unit == 0 {
			unit = n
		} else {
			for n != 0 {
				unit, n = n, unit%n
			}
		}
	}
	if unit >= 1 && unit <= 8 {
		return unit
	}
	return 4
}
