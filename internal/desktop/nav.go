package desktop

import (
	"runtime"
	"strconv"

	"github.com/egoist/mygo/ui"

	"gorequest/internal/desktop/uikit"
)

// nav draws the project work window's left rail. It shows the GoRequest
// wordmark below the traffic-light safe area, a filter box, the current project's
// Requests and Documents trees, and a Settings entry. Project switching lives in
// the top bar's project menu, so the rail stays about this project's contents.
func (a *App) nav(c *ui.Context, t *ui.Theme) {
	m := a.msg()
	p := uikit.PaletteFor(t)
	rail := ui.Column(c).Width(a.navWidth).FillHeight()
	// Let the system's sidebar vibrancy show through on a real chrome window;
	// otherwise paint the palette's sidebar face.
	if !c.Vibrancy() {
		rail.Background(p.Sidebar)
	}
	rail.Children(func() {
		a.navHeader(c, p)
		ui.Box(c).Height(1).Background(p.Hairline)

		// Requests / History / Git switch (§3): a small segmented view toggle.
		ui.Column(c).Padding(6, 10, 6, 10).Children(func() {
			idx := 0
			if a.historyView {
				idx = 1
			}
			if a.gitView {
				idx = 2
			}
			seg, changed := uikit.SimpleSegmented(c, p, &idx, []string{m.RequestsGroup, m.HistoryLabel, a.gitMsg().NavLabel})
			seg.FillWidth()
			if changed {
				a.historyView = idx == 1
				a.gitView = idx == 2
				if a.historyView {
					a.reloadHistory()
				}
			}
		})

		if a.gitView {
			if a.git == nil {
				a.git = newGitController(a)
			}
			a.git.panel(c, t)
		} else if a.historyView {
			a.historyPanel(c, t)
		} else {
			a.filterBox(c, p, m)
			ui.Box(c).Grow(1).FillWidth().Children(func() {
				ui.Scroll(c).Grow(1).FillWidth().Label(m.ProjectContents).TrackScroll(&a.navScroll).Children(func() {
					a.projectContents(c, t)
				})
				uikit.ScrollEdges(c, p, &a.navScroll)
			})
		}

		// Fixed Settings entry.
		ui.Box(c).Height(1).Background(p.Hairline)
		ui.Column(c).Padding(8, 10).Children(func() {
			b := uikit.ActionCursor(ui.Button(c, "")).FillWidth().Tooltip(m.OpenSettingsTip)
			b.Children(func() {
				ui.Icon(c, iconSettings).FontSize(15).TextColor(p.Muted)
				ui.Text(c, m.Settings).SingleLine().Grow(1)
			})
			if b.Clicked() {
				a.showSettings = true
			}
		})
	})
}

// navHeader centers the wordmark between the macOS window-control band and
// the separator below. TitleBar reports AppKit's actual reserved band (and is
// zero in full screen); do not infer the controls' position from an x inset.
// Keep the usual 52-DIP header, allowing 30 DIPs below taller control bands
// for the existing 17-point wordmark and balanced breathing room.
func (a *App) navHeader(c *ui.Context, p uikit.Palette) {
	top, bottom, height := float32(8), float32(6), float32(52)
	left, right := float32(14), float32(12)
	if runtime.GOOS == "darwin" {
		top, bottom = c.TitleBar().Height, 0
		left, right = 12, 14
		height = max(height, top+30)
	}
	ui.Column(c).Padding(top, right, bottom, left).Gap(1).Height(height).Justify(ui.Center).Children(func() {
		ui.Text(c, "GoRequest").Bold().FontSize(17).TextColor(p.Text).SingleLine()
		windowDragBrand(c, left)
	})
}

// filterBox draws the sidebar filter (§3): a 32-tall search row with a live
// match count and Esc to clear. Typing filters the whole tree.
func (a *App) filterBox(c *ui.Context, p uikit.Palette, m *messages) {
	ui.Column(c).Padding(4, 10, 6, 10).Children(func() {
		r := ui.Row(c).Height(32).Radius(9).BorderWidth(1).BorderColor(p.Hairline).
			Background(p.Sidebar).AlignItems(ui.Center).Padding(0, 8).Gap(6)
		r.Children(func() {
			ui.Icon(c, uikit.IconSearch).FontSize(14).TextColor(p.Muted)
			in := ui.TextInput(c, &a.filterQuery).Grow(1).Placeholder(m.FilterPlaceholder)
			if a.filterFocus {
				in.Focus()
				a.filterFocus = false
			}
			in.OnShortcut(0, ui.KeyEscape, func() { a.filterQuery = "" })
			if a.filtering() {
				ui.Text(c, strconv.Itoa(a.matchCount())).FontSize(11).TextColor(p.Faint).SingleLine()
			}
		})
	})
}

// matchCount counts matching requests and documents for the filter's hit badge.
func (a *App) matchCount() int {
	if a.ps == nil || a.ps.project == nil {
		return 0
	}
	n := 0
	for _, e := range a.ps.project.Requests {
		if a.reqMatch(e) {
			n++
		}
	}
	for _, d := range a.ps.project.Docs {
		if a.docMatch(d) {
			n++
		}
	}
	return n
}
