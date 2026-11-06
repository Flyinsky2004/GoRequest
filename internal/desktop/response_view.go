package desktop

import (
	"fmt"
	"os"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"gorequest/internal/core"
	"gorequest/internal/desktop/uikit"
)

// largeResponseThreshold is the point (2 MB) past which the response body is
// shown as plain text instead of a syntax-highlighted, foldable JSON tree, so a
// big response cannot stall rendering (§5.9).
const largeResponseThreshold = 2 << 20

// headerRow is one response header line shown read-only. Duplicate names stay
// as separate rows and are never collapsed into a map that would drop them.
type headerRow struct {
	Name      string
	Value     string
	Sensitive bool
}

// sensitiveHeader reports whether a response header's value must be masked
// before display, so switching between Body and Headers never leaks
// Authorization or Set-Cookie.
func sensitiveHeader(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "authorization", "proxy-authorization", "set-cookie", "cookie":
		return true
	}
	return false
}

// headerRows flattens a response's ordered headers for display, preserving
// duplicates and masking sensitive values.
func headerRows(res *core.ExecResult) []headerRow {
	if res == nil {
		return nil
	}
	rows := make([]headerRow, 0, len(res.Headers))
	for _, h := range res.Headers {
		name, val := h[0], h[1]
		sensitive := sensitiveHeader(name)
		if sensitive {
			val = "***"
		}
		rows = append(rows, headerRow{Name: name, Value: val, Sensitive: sensitive})
	}
	return rows
}

// responseBodyIsJSON reports whether the response body was valid JSON, which
// Execute records by populating Pretty.
func responseBodyIsJSON(res *core.ExecResult) bool {
	return res != nil && res.Pretty != ""
}

// responseBodyText returns only the response body for the Pretty/Raw toggle:
// Pretty uses the pre-parsed indented JSON, Raw the original bytes. It never
// contains headers or the status line, and never appends a truncation notice,
// so the body stays valid JSON/text.
func responseBodyText(res *core.ExecResult, raw bool) string {
	if res == nil {
		return ""
	}
	if !raw && res.Pretty != "" {
		return res.Pretty
	}
	return string(res.Body)
}

// truncationNotice returns the out-of-band truncation message, empty when the
// response was not truncated. It is shown beside the status, never inside the
// body editor.
func truncationNotice(res *core.ExecResult) string {
	if res != nil && res.Truncated {
		return "[truncated at 32 MB]"
	}
	return ""
}

// isCanceledErr reports whether a request error was a user cancellation.
func isCanceledErr(msg string) bool { return strings.Contains(msg, "canceled") }

// responseView draws the response panel: a status line (status-code capsule,
// protocol, duration and size as rolling tabular figures), a Body/Headers switch
// with Pretty/Raw for the body, a send progress line and skeleton while a
// request is in flight, an inline error with a Retry pill, and the body as a
// read-only code view.
func (a *App) responseView(c *ui.Context, t *ui.Theme, tab *Tab) {
	m := a.msg()
	p := uikit.PaletteFor(t)
	ui.Column(c).Grow(1).FillHeight().Background(p.BG).Children(func() {
		ui.Row(c).Gap(10).Padding(6, 10).AlignItems(ui.Center).BorderWidth(1, 0, 0, 0).BorderColor(p.Hairline).Children(func() {
			a.responseStatus(c, tab, m, p)
			ui.Box(c).Grow(1)
			if tab.Resp != nil && !tab.Sending && tab.RespErr == "" {
				ui.Segmented(c, &tab.RespSection, m.BodySection, m.HeadersSection)
				if tab.RespSection == 0 {
					ui.Segmented(c, &tab.RespRawIdx, m.Pretty, m.Raw)
				}
				if b := uikit.IconButton(c, p, uikit.IconCopy, m.CopyResponse); b.Clicked() {
					c.WriteClipboard(responseBodyText(tab.Resp, tab.RespRawIdx == 1))
					a.status = m.Copied
				}
				if b := uikit.IconButton(c, p, uikit.IconFile, m.SaveResponse); b.Clicked() {
					a.saveResponse(tab)
				}
			}
		})

		// In flight: progress line over the previous response at 40%, or a
		// skeleton when there is nothing yet.
		if tab.Sending {
			uikit.SendProgress(c, p)
			if tab.Resp != nil {
				ui.Box(c).Grow(1).FillWidth().Opacity(0.4).Children(func() {
					a.responseBodyArea(c, t, tab, m, p)
				})
			} else {
				ui.Column(c).Grow(1).FillWidth().Children(func() {
					uikit.SkeletonLines(c, p, 12)
				})
			}
			return
		}

		if tab.RespErr != "" {
			a.responseError(c, t, tab, m, p)
			return
		}
		if tab.Resp == nil {
			// One empty state only (§7/D9): icon + one line + shortcut hint.
			ui.Column(c).Grow(1).Center().Gap(8).Children(func() {
				ui.Icon(c, uikit.IconSend).FontSize(28).TextColor(p.Faint)
				ui.Text(c, m.NoResponseHint).FontSize(13).TextColor(p.Muted)
				ui.Text(c, m.SendShortcutHint).FontSize(12).TextColor(p.Faint)
			})
			return
		}
		if tab.RespSection == 1 {
			a.responseHeaders(c, t, tab.Resp, p)
			return
		}
		a.responseFindBar(c, t, tab)
		a.responseBodyArea(c, t, tab, m, p)
	})
}

// responseFindBar is the small in-panel find strip (§7): a query field, match
// count, and previous/next buttons. It drives the read-only response editor's
// native find instead of opening a system dialog.
// findMatches returns the start offsets of query in text, case-insensitively.
// It is capped so a pathological query cannot stall a frame.
func findMatches(text, query string) []int {
	if query == "" {
		return nil
	}
	lower, q := strings.ToLower(text), strings.ToLower(query)
	var out []int
	for i := 0; len(out) < 10000; {
		j := strings.Index(lower[i:], q)
		if j < 0 {
			break
		}
		at := i + j
		out = append(out, at)
		i = at + len(q)
	}
	return out
}

// lineOf returns the 0-based line number containing offset in text.
func lineOf(text string, offset int) int {
	if offset < 0 {
		return 0
	}
	if offset > len(text) {
		offset = len(text)
	}
	return strings.Count(text[:offset], "\n")
}

// responseFindBar is the small in-panel find strip (§7): a query field, match
// count, and previous/next buttons. It selects the match in the read-only
// response editor, never opening a system dialog or the editor's own bar.
func (a *App) responseFindBar(c *ui.Context, t *ui.Theme, tab *Tab) {
	if !tab.respFindOpen {
		return
	}
	m := a.msg()
	p := uikit.PaletteFor(t)
	st := a.responseEditorFor(t, tab)
	body := responseBodyText(tab.Resp, tab.RespRawIdx == 1)
	ui.Row(c).Gap(6).Padding(4, 10).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(p.Hairline).Children(func() {
		ui.Icon(c, uikit.IconSearch).FontSize(13).TextColor(p.Muted)
		in := ui.TextInput(c, &tab.respFindQuery).Width(220).Placeholder(m.FindPlaceholder)
		if tab.respFindFocus {
			in.Focus()
			tab.respFindFocus = false
		}
		in.OnShortcut(0, ui.KeyEscape, func() { tab.respFindOpen = false })
		if in.Changed() {
			tab.respFindIdx = 0
			tab.respFindJump = true
		}
		matches := findMatches(body, tab.respFindQuery)
		ui.Box(c).Grow(1)
		if len(matches) > 0 {
			if ui.Button(c, "↑").Clicked() {
				tab.respFindIdx--
				tab.respFindJump = true
			}
			if ui.Button(c, "↓").Clicked() {
				tab.respFindIdx++
				tab.respFindJump = true
			}
			// Clamp after the buttons so a click never indexes out of range.
			if tab.respFindIdx >= len(matches) {
				tab.respFindIdx = 0
			}
			if tab.respFindIdx < 0 {
				tab.respFindIdx = len(matches) - 1
			}
			ui.Textf(c, m.FindCount, tab.respFindIdx+1, len(matches)).FontSize(11).
				FontFeatures("tnum").TextColor(p.Faint)
		} else {
			tab.respFindIdx = 0
			if tab.respFindQuery != "" {
				ui.Text(c, m.FindNone).FontSize(11).TextColor(p.Faint)
			}
		}
		if b := uikit.IconButton(c, p, uikit.IconClose, m.Cancel); b.Clicked() {
			tab.respFindOpen = false
		}
		// Jump only when the query or the chosen match changed, so the user's
		// own selection in the body is left alone on later frames.
		if tab.respFindJump && len(matches) > 0 {
			start := matches[tab.respFindIdx]
			st.Select(start, start+len(tab.respFindQuery))
			st.ScrollTo(lineOf(body, start))
			tab.respFindJump = false
		}
	})
}

// saveResponse writes the response body to a user-chosen local file. The native
// save dialog runs off the UI thread; the result is posted back through the
// owning window's Update, so no UI state is touched from a goroutine.
func (a *App) saveResponse(tab *Tab) {
	if tab == nil || tab.Resp == nil {
		return
	}
	body := []byte(responseBodyText(tab.Resp, tab.RespRawIdx == 1))
	m := a.msg()
	win := a.win
	var parent *mygo.Window
	if nw, ok := win.(nativeWindower); ok {
		parent = nw.nativeWindow()
	}
	go func() {
		path, err := mygo.Dialog.Save(mygo.SaveDialogOptions{
			Parent:      parent,
			Title:       m.SaveResponse,
			DefaultPath: "response.txt",
			ButtonLabel: m.Save,
		})
		if err != nil || path == "" {
			return
		}
		if err := os.WriteFile(path, body, 0o644); err != nil {
			if win != nil && !win.IsDestroyed() {
				win.Update(func() { a.status = fmt.Sprintf("%s: %v", m.SaveResponse, err) })
			}
			return
		}
		if win != nil && !win.IsDestroyed() {
			win.Update(func() { a.status = translate(m.SavedResponse, path) })
		}
	}()
}

// responseStatus draws the status line's left part.
func (a *App) responseStatus(c *ui.Context, tab *Tab, m *messages, p uikit.Palette) {
	switch {
	case tab.Sending:
		ui.Text(c, m.SendingLabel).FontSize(12).TextColor(p.Muted)
	case tab.RespErr != "":
		if isCanceledErr(tab.RespErr) {
			ui.Text(c, m.CanceledLabel).FontSize(12).TextColor(p.Muted)
		} else {
			ui.Text(c, m.ErrorLabel).FontSize(12).TextColor(uikit.MethodColor("DELETE", p))
		}
	case tab.Resp != nil:
		uikit.StatusPill(c, p, tab.Resp.Status)
		ui.Text(c, tab.Resp.Proto).FontSize(12).TextColor(p.Muted)
		// Duration and size roll to their new values (§6.4).
		host := ui.Box(c).Width(0).Height(0)
		durMs := host.AnimateTo("resp-dur", float32(tab.Resp.Duration.Microseconds())/1000.0, uikit.SpringNumber)
		sizeB := host.AnimateTo("resp-size", float32(tab.Resp.BodySize), uikit.SpringNumber)
		ui.Textf(c, "%.1f ms · %d B", durMs, int64(sizeB)).
			FontSize(12).FontFeatures("tnum").TextColor(p.Muted)
		if tab.Resp.Truncated {
			ui.Text(c, m.TruncatedNotice).FontSize(12).TextColor(uikit.MethodColor("POST", p))
		}
	default:
		// No response yet: the single centered empty state below carries the
		// message, so the status line stays blank here (§7/D9).
	}
}

// responseError shows a centered error with an accent Retry pi ll, never a
// dialog and never a red-tinted panel.
func (a *App) responseError(c *ui.Context, t *ui.Theme, tab *Tab, m *messages, p uikit.Palette) {
	ui.Column(c).Grow(1).FillWidth().Center().Gap(12).Children(func() {
		ui.Text(c, tab.RespErr).FontSize(13).TextColor(p.Muted).MaxWidth(460).TextAlign(ui.Center)
		if b := uikit.PillButton(c, p, uikit.PillPrimary, m.Retry, uikit.IconRetry); b.Clicked() {
			a.send(tab)
		}
	})
}

// responseBodyArea draws the body: an empty hint, a large-response plain-text
// fallback with a note, else the read-only code view.
func (a *App) responseBodyArea(c *ui.Context, t *ui.Theme, tab *Tab, m *messages, p uikit.Palette) {
	if responseBodyText(tab.Resp, tab.RespRawIdx == 1) == "" {
		ui.Column(c).Grow(1).Center().Children(func() {
			ui.Text(c, m.BodyEmpty).TextColor(p.Muted)
		})
		return
	}
	if tab.Resp != nil && tab.Resp.BodySize > largeResponseThreshold {
		ui.Row(c).Padding(4, 10).Children(func() {
			ui.Text(c, m.LargeResponseNote).FontSize(11).TextColor(p.Muted)
		})
	}
	editor.View(c, a.responseEditorFor(t, tab)).Grow(1).FillWidth()
}

func (a *App) responseEditorFor(t *ui.Theme, tab *Tab) *editor.State {
	degraded := tab.Resp != nil && tab.Resp.BodySize > largeResponseThreshold
	if tab.respEditor != nil && tab.respBuiltVer == tab.respVersion &&
		tab.respBuiltIdx == tab.RespRawIdx && tab.respBuiltDegraded == degraded {
		return tab.respEditor
	}
	st := &editor.State{
		Buffer:   editor.NewBuffer(responseBodyText(tab.Resp, tab.RespRawIdx == 1)),
		ReadOnly: true,
		Theme:    uikit.EditorTheme(t),
	}
	if !degraded && responseBodyIsJSON(tab.Resp) {
		st.Buffer.SetLanguage(editor.LanguageNamed("json"))
	}
	tab.respEditor = st
	tab.respBuiltVer = tab.respVersion
	tab.respBuiltIdx = tab.RespRawIdx
	tab.respBuiltDegraded = degraded
	return st
}

// responseHeaders shows the response headers as a read-only Key/Value table
// that keeps duplicates, scrolls, and allows selection/copy.
func (a *App) responseHeaders(c *ui.Context, t *ui.Theme, res *core.ExecResult, p uikit.Palette) {
	m := a.msg()
	rows := headerRows(res)
	if len(rows) == 0 {
		ui.Column(c).Grow(1).Center().Children(func() {
			ui.Text(c, m.NoResponseHeaders).TextColor(p.Muted)
		})
		return
	}
	ui.Row(c).Gap(12).Padding(4, 10).Children(func() {
		ui.Text(c, m.Key).FontSize(12).FontWeight(600).Width(200).TextColor(p.Muted)
		ui.Text(c, m.Value).FontSize(12).FontWeight(600).TextColor(p.Muted)
	})
	ui.Scroll(c).Grow(1).FillWidth().Children(func() {
		ui.Column(c).Gap(2).FillWidth().Children(func() {
			for i, h := range rows {
				ui.Row(c).Key(i).Gap(12).FillWidth().AlignItems(ui.Center).Children(func() {
					ui.Text(c, h.Name).Width(200).FontSize(12).SingleLine().Selectable().TextColor(p.Text)
					col := p.Text
					if h.Sensitive {
						col = p.Muted
					}
					ui.Text(c, h.Value).Grow(1).FontSize(12).Selectable().TextColor(col)
				})
			}
		})
	})
}
