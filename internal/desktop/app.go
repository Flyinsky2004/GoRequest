// Package desktop builds the native MyGo user interface for GoRequest. It
// holds only views and interaction state; all request, variable, auth and
// execution logic lives in the pure Go core package.
package desktop

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/egoist/mygo/ui/markdown"

	"gorequest/internal/core"
	"gorequest/internal/platform"
)

var (
	methods   = []string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"}
	bodyTypes = []string{"json", "text", "xml"}
	authModes = []string{"none", "bearer", "basic"}
)

// Tab is one open editor: a request or a Markdown document.
type Tab struct {
	Kind  string // "request" or "doc"
	Title string

	// request editing state
	Entry       *core.RequestEntry
	Method      string
	URL         string
	QueryText   string
	HeadersText string
	BodyIdx     int
	BodyText    string
	AuthMode    string
	AuthIdx     int
	AuthToken   string
	AuthUser    string
	AuthPass    string
	Timeout     string
	Section     int // 0 params, 1 headers, 2 body, 3 auth

	// doc editing state
	DocRel    string
	DocEditor *editor.State

	// shared
	Dirty    bool
	Baseline string

	// response
	Resp         *core.ExecResult
	RespErr      string
	RespRawIdx   int
	RespSize     float32
	Sending      bool
	cancel       context.CancelFunc
	respEditor   *editor.State
	respVersion  int
	respBuiltVer int
	respBuiltIdx int
}

// App is the whole desktop state.
type App struct {
	win         *mygo.Window
	project     *core.Project
	projectPath string
	pathInput   string
	status      string
	secrets     core.SecretProvider

	tabs      []*Tab
	active    int
	sel       string
	envSel    string
	splitSize float32
	docSplit  float32
}

// New creates the app, optionally opening a project path immediately.
func New(projectPath string) *App {
	a := &App{
		pathInput: projectPath,
		secrets:   platform.NewEnvSecrets(),
		envSel:    "(none)",
		splitSize: 260,
		docSplit:  0.5,
	}
	if projectPath != "" {
		a.openProject(projectPath)
	}
	return a
}

// SetWindow records the window so background requests can post updates.
func (a *App) SetWindow(w *mygo.Window) { a.win = w }

// OpenPath loads a project, reporting failures in the status bar.
func (a *App) OpenPath(path string) {
	a.openProject(path)
}

func (a *App) openProject(path string) {
	p, err := core.OpenProject(path)
	if err != nil {
		a.status = "open failed: " + err.Error()
		return
	}
	a.project = p
	a.projectPath = p.Dir
	a.pathInput = p.Dir
	a.tabs = nil
	a.active = 0
	a.sel = ""
	a.envSel = "(none)"
	if len(p.EnvOrder) > 0 {
		a.envSel = p.EnvOrder[0]
	}
	if n := core.ErrorCount(p.Issues); n > 0 {
		a.status = fmt.Sprintf("opened %s with %d validation error(s)", p.File.Name, n)
	} else {
		a.status = fmt.Sprintf("opened %s (%d requests)", p.File.Name, len(p.Requests))
	}
}

func (a *App) currentEnv() string {
	if a.envSel == "(none)" {
		return ""
	}
	return a.envSel
}

// View builds one frame.
func (a *App) View(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Fill().Children(func() {
		a.toolbar(c, t)
		if a.project == nil {
			a.emptyState(c, t)
			return
		}
		ui.Split(c, &a.splitSize, func() { a.sidebar(c, t) }, func() { a.main(c, t) }).Grow(1).FillWidth()
	})
}

func (a *App) toolbar(c *ui.Context, t *ui.Theme) {
	ui.Row(c).Gap(8).Padding(8, 12).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
		ui.Text(c, "GoRequest").Bold()
		ui.TextInput(c, &a.pathInput).Width(300)
		if ui.Button(c, "Open").Clicked() {
			a.openProject(a.pathInput)
		}
		if a.project != nil {
			ui.Text(c, "·").TextColor(t.TextMuted)
			ui.Text(c, a.project.File.Name).TextColor(t.TextMuted)
			if ui.Button(c, "New Request").Clicked() {
				a.newRequest()
			}
			if ui.Button(c, "New Doc").Clicked() {
				a.newDoc()
			}
			opts := append([]string{"(none)"}, a.project.EnvOrder...)
			sel := ui.Select(c, &a.envSel, opts)
			if sel.Changed() {
				a.status = "environment: " + a.envSel
			}
		}
		ui.Box(c).Grow(1)
		if a.active < len(a.tabs) && a.tabs[a.active].Dirty {
			if ui.PrimaryButton(c, "Save").Clicked() {
				a.saveActive()
			}
		}
	})
}

func (a *App) emptyState(c *ui.Context, t *ui.Theme) {
	ui.Column(c).Grow(1).FillWidth().Center().Gap(8).Children(func() {
		ui.Text(c, "Open a project").FontSize(20).Bold()
		ui.Text(c, "Enter a folder containing .gorequest/ above, then Open.").TextColor(t.TextMuted)
	})
}

func (a *App) sidebar(c *ui.Context, t *ui.Theme) {
	ui.Column(c).Width(a.splitSize).FillHeight().Background(t.Surface).Children(func() {
		ui.Scroll(c).Grow(1).Children(func() {
			sb := ui.Sidebar(c, &a.sel, func() {
				ui.SidebarSection(c, "Requests", nil, func() {
					for _, re := range flattenRequests(a.project.Collections) {
						ui.SidebarItem(c, "req:"+re.Request.ID, nil, re.Request.Name)
					}
				})
				ui.SidebarSection(c, "Documents", nil, func() {
					for _, d := range a.project.Docs {
						ui.SidebarItem(c, "doc:"+d.RelPath, nil, d.Name)
					}
				})
			})
			if sb.Changed() {
				a.openSelection(a.sel)
			}
		})
	})
}

func (a *App) openSelection(sel string) {
	switch {
	case strings.HasPrefix(sel, "req:"):
		id := strings.TrimPrefix(sel, "req:")
		if e, ok := a.project.Requests[id]; ok {
			a.openRequest(e)
		}
	case strings.HasPrefix(sel, "doc:"):
		rel := strings.TrimPrefix(sel, "doc:")
		a.openDoc(rel)
	}
}

func (a *App) openRequest(entry *core.RequestEntry) {
	for i, tab := range a.tabs {
		if tab.Kind == "request" && tab.Entry == entry {
			a.active = i
			return
		}
	}
	tab := &Tab{
		Kind:     "request",
		Title:    entry.Request.Name,
		Entry:    entry,
		Baseline: entry.BaselineDigest,
		RespSize: 320,
	}
	tab.Method = entry.Request.HTTP.Method
	tab.URL = entry.Request.HTTP.URL
	tab.Timeout = entry.Request.Timeout
	tab.QueryText = renderKV(entry.Request.HTTP.Query, "=")
	tab.HeadersText = renderKV(entry.Request.HTTP.Headers, ": ")
	if entry.Request.HTTP.Body != nil {
		tab.BodyIdx = indexOf(bodyTypes, entry.Request.HTTP.Body.Type)
		tab.BodyText = core.BodyEditText(entry.Request.HTTP.Body)
	}
	a.fillAuth(tab, entry.Request)
	a.tabs = append(a.tabs, tab)
	a.active = len(a.tabs) - 1
}

func (a *App) openDoc(rel string) {
	for i, tab := range a.tabs {
		if tab.Kind == "doc" && tab.DocRel == rel {
			a.active = i
			return
		}
	}
	content, digest, err := a.project.Store.Read(rel)
	if err != nil {
		a.status = "read doc: " + err.Error()
		return
	}
	tab := &Tab{
		Kind:      "doc",
		Title:     filepath.Base(rel),
		DocRel:    rel,
		Baseline:  digest,
		DocEditor: &editor.State{Buffer: editor.NewBuffer(string(content))},
	}
	tab.DocEditor.Buffer.SetLanguage(editor.LanguageNamed("markdown"))
	a.tabs = append(a.tabs, tab)
	a.active = len(a.tabs) - 1
}

func (a *App) main(c *ui.Context, t *ui.Theme) {
	if len(a.tabs) == 0 {
		ui.Column(c).Grow(1).Center().Children(func() {
			ui.Text(c, "Choose a request or document on the left.").TextColor(t.TextMuted)
		})
		return
	}
	if a.active >= len(a.tabs) {
		a.active = len(a.tabs) - 1
	}
	titles := make([]string, len(a.tabs))
	for i, tab := range a.tabs {
		titles[i] = tab.Title
		if tab.Dirty {
			titles[i] += " •"
		}
	}
	ui.Column(c).Grow(1).FillHeight().Children(func() {
		ui.Tabs(c, &a.active, titles...)
		tab := a.tabs[a.active]
		if tab.Kind == "request" {
			a.requestView(c, t, tab)
		} else {
			a.docView(c, t, tab)
		}
	})
}

func (a *App) requestView(c *ui.Context, t *ui.Theme, tab *Tab) {
	ui.Column(c).Grow(1).FillHeight().Padding(10, 12).Gap(8).Children(func() {
		// Method + URL + send.
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			if ui.Select(c, &tab.Method, methods).Changed() {
				tab.Dirty = true
			}
			if ui.TextInput(c, &tab.URL).Grow(1).Changed() {
				tab.Dirty = true
			}
			if ui.TextInput(c, &tab.Timeout).Width(70).Changed() {
				tab.Dirty = true
			}
			if tab.Sending {
				if ui.Button(c, "Cancel").Clicked() {
					if tab.cancel != nil {
						tab.cancel()
					}
				}
			} else if ui.PrimaryButton(c, "Send").Clicked() {
				a.send(tab)
			}
		})
		// Section tabs.
		ui.Segmented(c, &tab.Section, "Params", "Headers", "Body", "Auth")
		// Editor above, response below.
		ui.SplitVertical(c, &tab.RespSize, func() {
			ui.Column(c).Grow(1).FillWidth().Gap(4).Children(func() {
				switch tab.Section {
				case 0:
					a.kvEditor(c, t, "Query — one name=value per line", &tab.QueryText, tab)
				case 1:
					a.kvEditor(c, t, "Headers — one Name: Value per line", &tab.HeadersText, tab)
				case 2:
					a.bodyEditor(c, t, tab)
				case 3:
					a.authEditor(c, t, tab)
				}
			})
		}, func() {
			a.responseView(c, t, tab)
		}).Grow(1).FillWidth()
	})
}

func (a *App) kvEditor(c *ui.Context, t *ui.Theme, label string, value *string, tab *Tab) {
	ui.Text(c, label).FontSize(12).TextColor(t.TextMuted)
	if ui.TextArea(c, value).Grow(1).FillWidth().Changed() {
		tab.Dirty = true
	}
}

func (a *App) bodyEditor(c *ui.Context, t *ui.Theme, tab *Tab) {
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "Body type").FontSize(12).TextColor(t.TextMuted)
		if ui.Segmented(c, &tab.BodyIdx, bodyTypes...).Changed() {
			tab.Dirty = true
		}
	})
	if ui.TextArea(c, &tab.BodyText).Grow(1).FillWidth().Changed() {
		tab.Dirty = true
	}
}

func (a *App) authEditor(c *ui.Context, t *ui.Theme, tab *Tab) {
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "Auth").FontSize(12).TextColor(t.TextMuted)
		if ui.Select(c, &tab.AuthMode, authModes).Changed() {
			tab.AuthIdx = indexOf(authModes, tab.AuthMode)
			tab.Dirty = true
		}
	})
	switch tab.AuthMode {
	case "bearer":
		ui.Text(c, "Token (use {{secret.name}} to keep the value out of the file)").FontSize(12).TextColor(t.TextMuted)
		if ui.TextInput(c, &tab.AuthToken).Changed() {
			tab.Dirty = true
		}
	case "basic":
		ui.Text(c, "Username").FontSize(12).TextColor(t.TextMuted)
		if ui.TextInput(c, &tab.AuthUser).Changed() {
			tab.Dirty = true
		}
		ui.Text(c, "Password").FontSize(12).TextColor(t.TextMuted)
		if ui.TextInput(c, &tab.AuthPass).Changed() {
			tab.Dirty = true
		}
	default:
		ui.Text(c, "No auth.").TextColor(t.TextMuted)
	}
}

func (a *App) responseView(c *ui.Context, t *ui.Theme, tab *Tab) {
	ui.Column(c).Grow(1).FillHeight().Background(t.Surface).Children(func() {
		ui.Row(c).Gap(10).Padding(6, 10).AlignItems(ui.Center).BorderWidth(1, 0, 0, 0).BorderColor(t.Border).Children(func() {
			if tab.Sending {
				ui.Spinner(c)
				ui.Text(c, "Sending…")
			} else if tab.RespErr != "" {
				ui.Text(c, "Error").TextColor(t.Danger)
			} else if tab.Resp != nil {
				ui.Textf(c, "%s %d", tab.Resp.Proto, tab.Resp.Status).Bold()
				ui.Textf(c, "%s · %d B", tab.Resp.Duration.Round(1e6), tab.Resp.BodySize).FontSize(12).TextColor(t.TextMuted)
			} else {
				ui.Text(c, "No response yet").TextColor(t.TextMuted)
			}
			ui.Box(c).Grow(1)
			ui.Segmented(c, &tab.RespRawIdx, "Pretty", "Raw")
		})
		if tab.Sending {
			ui.Box(c).Fill()
			return
		}
		if tab.RespErr != "" {
			ui.Scroll(c).Grow(1).FillWidth().Children(func() {
				ui.Text(c, tab.RespErr).TextColor(t.Danger)
			})
			return
		}
		if tab.Resp == nil {
			ui.Box(c).Fill()
			return
		}
		if tab.respEditor == nil || tab.respBuiltVer != tab.respVersion || tab.respBuiltIdx != tab.RespRawIdx {
			tab.respEditor = &editor.State{
				Buffer:   editor.NewBuffer(a.responseText(tab)),
				ReadOnly: true,
			}
			tab.respBuiltVer = tab.respVersion
			tab.respBuiltIdx = tab.RespRawIdx
		}
		editor.View(c, tab.respEditor).Grow(1).FillWidth()
	})
}

func (a *App) responseText(tab *Tab) string {
	res := tab.Resp
	if res == nil {
		return ""
	}
	var b strings.Builder
	for _, h := range res.Headers {
		if strings.EqualFold(h[0], "Authorization") || strings.EqualFold(h[0], "Set-Cookie") {
			b.WriteString(h[0] + ": ***\n")
			continue
		}
		b.WriteString(h[0] + ": " + h[1] + "\n")
	}
	b.WriteString("\n")
	body := string(res.Body)
	if tab.RespRawIdx == 0 && res.Pretty != "" {
		body = res.Pretty
	}
	if res.Truncated {
		body += "\n\n[truncated at 32 MB]"
	}
	b.WriteString(body)
	return b.String()
}

func (a *App) docView(c *ui.Context, t *ui.Theme, tab *Tab) {
	ui.Column(c).Grow(1).FillHeight().Children(func() {
		ui.Row(c).Gap(8).Padding(8, 12).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
			ui.Segmented(c, &tab.Section, "Split", "Edit", "Preview")
			ui.Box(c).Grow(1)
			if tab.Dirty {
				if ui.PrimaryButton(c, "Save Doc").Clicked() {
					a.saveDoc(tab)
				}
			}
		})
		src := tab.DocEditor.Buffer.String()
		switch tab.Section {
		case 1:
			editor.View(c, tab.DocEditor).Grow(1).FillWidth()
		case 2:
			ui.Scroll(c).Grow(1).FillWidth().Children(func() {
				markdown.View(c, src, nil)
			})
		default:
			ui.Split(c, &a.docSplit, func() {
				editor.View(c, tab.DocEditor).Grow(1).FillWidth()
			}, func() {
				ui.Scroll(c).Grow(1).FillWidth().Children(func() {
					markdown.View(c, src, nil)
				})
			}).Grow(1).FillWidth()
		}
	})
}

// --- actions -------------------------------------------------------------

func (a *App) saveActive() {
	if a.active >= len(a.tabs) {
		return
	}
	tab := a.tabs[a.active]
	if tab.Kind == "request" {
		a.saveRequest(tab)
	} else {
		a.saveDoc(tab)
	}
}

func (a *App) saveRequest(tab *Tab) {
	req, err := a.requestFromTab(tab)
	if err != nil {
		a.status = "save: " + err.Error()
		return
	}
	_, err = a.project.SaveRequest(tab.Entry, req, tab.Baseline)
	if core.IsConflict(err) {
		a.status = "conflict: " + tab.Entry.RelPath + " changed on disk; reopen to merge before saving"
		return
	}
	if err != nil {
		a.status = "save: " + err.Error()
		return
	}
	tab.Baseline = tab.Entry.BaselineDigest
	tab.Dirty = false
	a.status = "saved " + tab.Entry.RelPath
}

func (a *App) saveDoc(tab *Tab) {
	content := []byte(tab.DocEditor.Buffer.String())
	digest, err := a.project.SaveDoc(tab.DocRel, content, tab.Baseline)
	if core.IsConflict(err) {
		a.status = "conflict: " + tab.DocRel + " changed on disk; reopen to merge before saving"
		return
	}
	if err != nil {
		a.status = "save: " + err.Error()
		return
	}
	tab.Baseline = digest
	tab.Dirty = false
	a.status = "saved " + tab.DocRel
}

func (a *App) send(tab *Tab) {
	req, err := a.requestFromTab(tab)
	if err != nil {
		a.status = "request: " + err.Error()
		return
	}
	vars, env, err := a.project.ResolveVars(tab.Entry, a.currentEnv())
	if err != nil {
		a.status = err.Error()
		return
	}
	_ = env // declared secrets are checked where they are actually referenced
	auth, err := a.project.AuthFor(req)
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
	go func() {
		res, err := core.Execute(ctx, plan, nil)
		a.win.Update(func() {
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

func (a *App) newRequest() {
	entry, err := a.project.NewRequest("", "New Request")
	if err != nil {
		a.status = "new request: " + err.Error()
		return
	}
	a.openRequest(entry)
	a.status = "created " + entry.RelPath
}

func (a *App) newDoc() {
	de, err := a.project.NewDoc("document")
	if err != nil {
		a.status = "new doc: " + err.Error()
		return
	}
	a.openDoc(de.RelPath)
	a.status = "created " + de.RelPath
}

func (a *App) requestFromTab(tab *Tab) (*core.Request, error) {
	if tab.Entry == nil || tab.Entry.Request == nil {
		return nil, fmt.Errorf("no request")
	}
	base := *tab.Entry.Request
	httpDef := &core.HTTPDef{
		Method:  tab.Method,
		URL:     tab.URL,
		Query:   parseKV(tab.QueryText, "="),
		Headers: parseKV(tab.HeadersText, ":"),
	}
	if strings.TrimSpace(tab.BodyText) != "" {
		body, err := core.ParseBodyText(bodyTypes[tab.BodyIdx], tab.BodyText)
		if err != nil {
			return nil, err
		}
		httpDef.Body = body
	}
	base.HTTP = httpDef
	base.Timeout = strings.TrimSpace(tab.Timeout)
	switch tab.AuthMode {
	case "bearer":
		base.Auth = &core.AuthRef{Mode: "inline", Inline: &core.AuthScheme{SchemaVersion: 1, Type: "bearer", Token: tab.AuthToken}}
	case "basic":
		base.Auth = &core.AuthRef{Mode: "inline", Inline: &core.AuthScheme{SchemaVersion: 1, Type: "basic", Username: tab.AuthUser, Password: tab.AuthPass}}
	default:
		base.Auth = &core.AuthRef{Mode: "none"}
	}
	return &base, nil
}

func (a *App) fillAuth(tab *Tab, req *core.Request) {
	scheme, err := a.project.AuthFor(req)
	if err != nil || scheme == nil {
		tab.AuthMode = "none"
		tab.AuthIdx = 0
		return
	}
	switch scheme.Type {
	case "bearer":
		tab.AuthMode = "bearer"
		tab.AuthToken = scheme.Token
	case "basic":
		tab.AuthMode = "basic"
		tab.AuthUser = scheme.Username
		tab.AuthPass = scheme.Password
	default:
		tab.AuthMode = "none"
	}
	tab.AuthIdx = indexOf(authModes, tab.AuthMode)
}

// --- helpers -------------------------------------------------------------

func flattenRequests(cols []*core.CollectionEntry) []*core.RequestEntry {
	var out []*core.RequestEntry
	var walk func(n *core.CollectionEntry)
	walk = func(n *core.CollectionEntry) {
		out = append(out, n.Requests...)
		for _, c := range n.Children {
			walk(c)
		}
	}
	for _, n := range cols {
		walk(n)
	}
	return out
}

func renderKV(kvs []core.KV, sep string) string {
	var b strings.Builder
	for _, kv := range kvs {
		if !kv.IsEnabled() {
			continue
		}
		b.WriteString(kv.Name + sep + kv.Value + "\n")
	}
	return b.String()
}

func parseKV(text, sep string) []core.KV {
	var out []core.KV
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		name, value, _ := strings.Cut(line, sep)
		name = strings.TrimSpace(name)
		value = strings.TrimSpace(value)
		if name == "" {
			continue
		}
		out = append(out, core.KV{Name: name, Value: value, Enabled: core.BoolPtr(true)})
	}
	return out
}

func indexOf(list []string, v string) int {
	for i, s := range list {
		if s == v {
			return i
		}
	}
	return 0
}

// Status returns the current status line text (used by the window title bar).
func (a *App) Status() string { return a.status }
