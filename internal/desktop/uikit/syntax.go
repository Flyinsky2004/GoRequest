package uikit

import (
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

// EditorTheme builds a code theme for the codeView/respones-body editor with
// the syntax colors of §1.3, all derived from the palette/method tokens so
// light and dark both stay readable on the background. It starts from the
// editor's default theme (so unrelated fields — diagnostics, diff colors, find
// highlights — are filled) and overrides the token colors we care about.
//
// Mapping:
//   - key (JSON property)      = Text
//   - string                   = GET green mixed 20% toward Muted (pressed gray)
//   - number                   = PUT blue
//   - boolean / null           = PATCH purple
//   - punctuation / brackets   = Faint
//   - XML tag                  = PUT blue
//   - XML attribute            = POST amber mixed toward Muted
func EditorTheme(t *ui.Theme) *editor.Theme {
	p := PaletteFor(t)
	ct := editor.DefaultTheme(t)

	ct.Background = p.BG
	ct.Text = p.Text
	ct.LineNumber = p.Faint
	ct.ActiveLineNumber = p.Muted
	ct.Selection = p.Accent.Alpha(0.25)
	ct.InactiveSelection = p.Accent.Alpha(0.14)
	ct.Cursor = p.Accent
	ct.Match = p.Accent.Alpha(0.25)
	ct.CurrentMatch = p.Accent
	ct.InactiveSelection = p.Accent.Alpha(0.14)

	green := MethodColor("GET", p)
	blue := MethodColor("PUT", p)
	purple := MethodColor("PATCH", p)
	amber := MethodColor("POST", p)

	strColor := green.Mix(p.Muted, 0.20)
	attrColor := amber.Mix(p.Muted, 0.30)

	set := func(k editor.TokenKind, c ui.Color) { ct.Tokens[k].Color = c }
	set(editor.TokenProperty, p.Text)
	set(editor.TokenString, strColor)
	set(editor.TokenEscape, strColor)
	set(editor.TokenNumber, blue)
	set(editor.TokenConstant, purple) // true / false / null
	set(editor.TokenPunctuation, p.Faint)
	set(editor.TokenOperator, p.Faint)
	set(editor.TokenKeyword, purple)
	set(editor.TokenTag, blue)
	set(editor.TokenAttribute, attrColor)
	set(editor.TokenComment, p.Faint)
	set(editor.TokenVariable, p.Text)
	set(editor.TokenType, blue)

	return ct
}
