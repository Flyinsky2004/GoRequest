package desktop

import (
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"gorequest/internal/desktop/uikit"
)

// applyTheme applies the stored appearance preference and bumps the theme
// generation when the resolved light/dark mode changes. View calls it once per
// frame before rendering, so every cached editor can compare generations.
func (a *App) applyTheme(c *ui.Context) {
	if a == nil {
		return
	}
	dark := appearanceDark(c, a.mgr.cfg)
	if dark != a.themeDark {
		a.themeDark = dark
		a.themeGen++
	}
	applyAppearance(c, a.mgr.cfg)
}

// themeGeneration returns the app's current theme generation, or 0 for a nil
// app (headless helpers and tests construct bare controllers).
func (a *App) themeGeneration() int {
	if a == nil {
		return 0
	}
	return a.themeGen
}

// syncEditorTheme makes a cached editor follow the current theme. It replaces
// only Theme: the buffer, selections, scroll position and undo history stay
// untouched, so switching appearance never discards the user's edits.
func (a *App) syncEditorTheme(ed *editor.State, gen *int, t *ui.Theme) {
	if ed == nil || gen == nil {
		return
	}
	cur := a.themeGeneration()
	if *gen == cur && ed.Theme != nil {
		return
	}
	ed.Theme = uikit.EditorTheme(t)
	*gen = cur
}

// syncDiffTheme is syncEditorTheme for a cached diff view. DiffState keeps its
// buffers and hunk/scroll state; only the colors change.
func (a *App) syncDiffTheme(d *editor.DiffState, gen *int, t *ui.Theme) {
	if d == nil || gen == nil {
		return
	}
	cur := a.themeGeneration()
	if *gen == cur && d.Theme != nil {
		return
	}
	d.Theme = uikit.EditorTheme(t)
	*gen = cur
}
