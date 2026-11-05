// Package desktop builds the native MyGo user interface for GoRequest. It
// holds only views and interaction state; all request, variable, auth and
// execution logic lives in the pure Go core package.
package desktop

import (
	"context"
	"fmt"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"gorequest/internal/core"
	"gorequest/internal/platform"
)

var (
	methods   = []string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"}
	bodyTypes = []string{"json", "text", "xml"}
	authModes = []string{"none", "bearer", "basic"}
)

// App is the whole desktop state. Projects are kept side by side, each with its
// own tabs and edits, so switching between them never discards work.
type App struct {
	win     *mygo.Window
	secrets core.SecretProvider
	cfg     *configStore

	projects   []*projectState
	current    *projectState
	projectSel string // sidebar selection id of the recent-project list
	navWidth   float32
	status     string

	showSettings bool
}

// New creates the app, optionally opening a project path immediately.
func New(projectPath string) *App {
	a := &App{
		secrets:  platform.NewEnvSecrets(),
		cfg:      newConfigStore(configFilePath()),
		navWidth: 260,
	}
	if projectPath != "" {
		_ = a.openProject(projectPath)
	}
	return a
}

// SetWindow records the window so background requests and native dialogs can
// post updates.
func (a *App) SetWindow(w *mygo.Window) { a.win = w }

// OpenPath loads a project, reporting failures in the status bar.
func (a *App) OpenPath(path string) { _ = a.openProject(path) }

// View builds one frame.
func (a *App) View(c *ui.Context) {
	a.applyAppearance(c)
	t := c.Theme()
	ui.Column(c).Fill().Children(func() {
		ui.Split(c, &a.navWidth, func() { a.nav(c, t) }, func() { a.workspace(c, t) }).Grow(1).FillWidth()
		if a.status != "" {
			ui.Row(c).Padding(5, 12).Clip().BorderWidth(1, 0, 0, 0).BorderColor(t.Border).Children(func() {
				ui.Text(c, a.status).FontSize(12).TextColor(t.TextMuted).SingleLine()
			})
		}
	})
	a.envEditorModal(c, t)
	a.settingsModal(c, t)
}

// applyAppearance applies the stored appearance preference for the frame. When
// the preference is System (or unset) the frame keeps the system theme.
func (a *App) applyAppearance(c *ui.Context) {
	switch a.cfg.appearance() {
	case "Light":
		c.SetTheme(ui.LightTheme())
	case "Dark":
		c.SetTheme(ui.DarkTheme())
	}
}

// openProject loads a project or, when it is already open, just switches to it.
// A failed open leaves the current project, its edits and the recent list
// untouched.
func (a *App) openProject(path string) error {
	root, err := normalizeProjectPath(path)
	if err != nil {
		a.status = "open failed: " + err.Error()
		return err
	}
	if ps := a.findProject(root); ps != nil {
		a.activate(ps)
		a.status = "switched to " + ps.name()
		return nil
	}
	p, err := core.OpenProject(root)
	if err != nil {
		a.status = "open failed: " + err.Error()
		return err
	}
	ps := newProjectState(root, p)
	a.projects = append(a.projects, ps)
	a.activate(ps)
	a.cfg.addRecent(root, ps.name())
	if n := core.ErrorCount(p.Issues); n > 0 {
		a.status = fmt.Sprintf("opened %s with %d validation error(s)", ps.name(), n)
	} else {
		a.status = fmt.Sprintf("opened %s (%d requests)", ps.name(), len(p.Requests))
	}
	return nil
}

func (a *App) findProject(root string) *projectState {
	for _, ps := range a.projects {
		if ps.root == root {
			return ps
		}
	}
	return nil
}

func (a *App) activate(ps *projectState) {
	a.current = ps
	a.projectSel = "project:" + ps.root
	a.cfg.setRecentName(ps.root, ps.name())
}

// chooseProject handles a click in the recent-project list. When an unloaded
// project fails to open, the list selection returns to the current project so
// the highlight stays truthful.
func (a *App) chooseProject() {
	id := a.projectSel
	if len(id) < len("project:") || id[:len("project:")] != "project:" {
		return
	}
	root := id[len("project:"):]
	if ps := a.findProject(root); ps != nil {
		a.activate(ps)
		a.status = "switched to " + ps.name()
		return
	}
	if err := a.openProject(root); err != nil {
		if a.current != nil {
			a.projectSel = "project:" + a.current.root
		} else {
			a.projectSel = ""
		}
	}
}

// pickProject opens the platform's native directory chooser on a background
// goroutine, then loads the chosen folder. Cancelling changes nothing.
func (a *App) pickProject() {
	win := a.win
	if win == nil {
		a.status = "open failed: window not ready"
		return
	}
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent:      win,
			Title:       "Open GoRequest Project",
			ButtonLabel: "Open",
			Message:     "Choose a folder that contains a .gorequest/ directory.",
			Directory:   true,
		})
		if err != nil {
			win.Update(func() { a.status = "open failed: " + err.Error() })
			return
		}
		if len(paths) == 0 {
			return
		}
		win.Update(func() { _ = a.openProject(paths[0]) })
	}()
}

// saveActive saves the active tab of the current project, reporting conflicts
// rather than overwriting external edits.
func (a *App) saveActive() {
	ps := a.current
	if ps == nil {
		return
	}
	tab := ps.activeTab()
	if tab == nil {
		return
	}
	rel := tab.DocRel
	if tab.Kind == "request" {
		rel = tab.Entry.RelPath
	}
	if err := ps.saveTab(tab); err != nil {
		if core.IsConflict(err) {
			a.status = "conflict: " + rel + " changed on disk; reopen to merge before saving"
		} else {
			a.status = "save: " + err.Error()
		}
		return
	}
	a.status = "saved " + rel
}

// send executes the tab's request. The goroutine captures the tab pointer (and
// thus its owning project), so the result always lands in the project that
// started the request even after the user switches projects.
func (a *App) send(tab *Tab) {
	ps := tab.Owner
	if ps == nil {
		a.status = "request: no project"
		return
	}
	req, err := requestFromTab(tab)
	if err != nil {
		a.status = "request: " + err.Error()
		return
	}
	vars, env, err := ps.project.ResolveVars(tab.Entry, ps.currentEnv())
	if err != nil {
		a.status = err.Error()
		return
	}
	_ = env // declared secrets are checked where they are actually referenced
	auth, err := ps.project.AuthFor(req)
	if err != nil {
		a.status = err.Error()
		return
	}
	plan, err := core.BuildPlan(req, vars, a.secrets, auth)
	if err != nil {
		a.status = err.Error()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	tab.cancel = cancel
	tab.Sending = true
	tab.RespErr = ""
	a.status = "sending " + req.HTTP.Method + " " + req.HTTP.URL
	win := a.win
	go func() {
		res, err := core.Execute(ctx, plan, nil)
		if win == nil {
			tab.Sending = false
			return
		}
		win.Update(func() {
			tab.Sending = false
			if err != nil {
				tab.Resp = nil
				tab.RespErr = err.Error()
			} else {
				tab.Resp = res
				tab.RespErr = ""
				tab.respVersion++
				tab.respEditor = nil
			}
		})
	}()
}

// Status returns the current status line text.
func (a *App) Status() string { return a.status }
