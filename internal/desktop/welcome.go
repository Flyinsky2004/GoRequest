package desktop

import (
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// welcomeState is the lightweight project-chooser window: the brand, the
// scrollable recent-project list, an Open Project action, a Settings entry and
// a status line. It deliberately does not build the request editor workspace.
type welcomeState struct {
	mgr          *Manager
	win          window
	status       string
	sel          string // sidebar selection id ("project:<root>")
	showSettings bool
}

// wire connects the welcome window's lifecycle. Closing it is always allowed;
// if it is the only window the application's default "last window closed"
// behavior quits, and if project windows are still open they keep running.
func (w *welcomeState) wire() {
	w.win.OnClose(func(e *mygo.CloseEvent) {})
	w.win.OnClosed(func() {
		if w.mgr.welcome == w {
			w.mgr.welcome = nil
		}
	})
}

// view builds one frame of the welcome window.
func (w *welcomeState) view(c *ui.Context) {
	m := w.mgr
	applyAppearance(c, m.cfg)
	t := c.Theme()

	ui.Column(c).Fill().Children(func() {
		// Brand block.
		ui.Column(c).Padding(24, 24, 14, 24).Gap(6).Children(func() {
			ui.Text(c, "GoRequest").FontSize(24).Bold().SingleLine()
			ui.Text(c, "Open a project to start. Each project opens in its own window.").
				FontSize(12).TextColor(t.TextMuted).SingleLine()
		})

		// Actions.
		ui.Row(c).Padding(0, 24, 10, 24).Gap(10).AlignItems(ui.Center).Children(func() {
			if ui.PrimaryButton(c, "Open Project…").Tooltip("Choose a folder containing .gorequest/").Clicked() {
				m.pickProject(w.win)
			}
			ui.Box(c).Grow(1)
			if ui.Button(c, "Settings").Clicked() {
				w.showSettings = true
			}
		})

		// Recent projects, independently scrollable.
		ui.Row(c).Padding(12, 24, 4, 24).Children(func() {
			ui.Text(c, "Recent Projects").FontSize(11).Bold().TextColor(t.TextMuted).SingleLine()
		})
		sb := ui.Sidebar(c, &w.sel, func() {
			projects := m.visibleRecents()
			if len(projects) == 0 {
				ui.Text(c, "No projects yet. Open one to get started.").
					FontSize(12).TextColor(t.TextMuted).Padding(2, 8)
				return
			}
			for _, rp := range projects {
				ui.SidebarItem(c, "project:"+rp.Path, nil, m.recentLabel(rp))
			}
		}).Grow(1).FillWidth().Label("Recent Projects")
		if sb.Changed() {
			w.openSelected()
		}

		// Status feedback (open errors, outcomes).
		if w.status != "" {
			ui.Row(c).Padding(6, 24).Clip().BorderWidth(1, 0, 0, 0).BorderColor(t.Border).Children(func() {
				ui.Text(c, w.status).FontSize(12).TextColor(t.TextMuted).SingleLine()
			})
		}
	})

	w.settingsModal(c, t)
}

// openSelected opens the recent project chosen in the list. A failure stays in
// the welcome window with a recoverable message and clears the selection, since
// no project became current.
func (w *welcomeState) openSelected() {
	id := w.sel
	const prefix = "project:"
	if !strings.HasPrefix(id, prefix) {
		return
	}
	path := strings.TrimPrefix(id, prefix)
	if err := w.mgr.openProject(path); err != nil {
		w.status = "open failed: " + err.Error()
		w.sel = ""
	}
}

func (w *welcomeState) settingsModal(c *ui.Context, t *ui.Theme) {
	renderSettings(c, t, w.mgr.cfg, &w.showSettings, func(s string) { w.status = s })
}
