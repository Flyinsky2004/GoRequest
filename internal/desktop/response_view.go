package desktop

import (
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"gorequest/internal/core"
)

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

// responseView draws the response panel: a status line (proto/status, duration,
// size, sending/error/canceled), a Body/Headers switch, and Pretty/Raw for the
// body only.
func (a *App) responseView(c *ui.Context, t *ui.Theme, tab *Tab) {
	ui.Column(c).Grow(1).FillHeight().Background(t.Surface).Children(func() {
		ui.Row(c).Gap(10).Padding(6, 10).AlignItems(ui.Center).BorderWidth(1, 0, 0, 0).BorderColor(t.Border).Children(func() {
			if tab.Sending {
				ui.Spinner(c)
				ui.Text(c, "Sending…")
			} else if tab.RespErr != "" {
				if isCanceledErr(tab.RespErr) {
					ui.Text(c, "Canceled").TextColor(t.TextMuted)
				} else {
					ui.Text(c, "Error").TextColor(t.Danger)
				}
			} else if tab.Resp != nil {
				ui.Textf(c, "%s %d", tab.Resp.Proto, tab.Resp.Status).Bold()
				ui.Textf(c, "%s · %d B", tab.Resp.Duration.Round(1e6), tab.Resp.BodySize).FontSize(12).TextColor(t.TextMuted)
				if notice := truncationNotice(tab.Resp); notice != "" {
					ui.Text(c, notice).FontSize(12).TextColor(t.Warning)
				}
			} else {
				ui.Text(c, "No response yet").TextColor(t.TextMuted)
			}
			ui.Box(c).Grow(1)
			if tab.Resp != nil && !tab.Sending && tab.RespErr == "" {
				ui.Segmented(c, &tab.RespSection, "Body", "Headers")
				if tab.RespSection == 0 {
					ui.Segmented(c, &tab.RespRawIdx, "Pretty", "Raw")
				}
			}
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
			ui.Column(c).Grow(1).Center().Children(func() {
				ui.Text(c, "Send a request to see its response here.").TextColor(t.TextMuted)
			})
			return
		}
		if tab.RespSection == 1 {
			a.responseHeaders(c, t, tab.Resp)
			return
		}
		a.responseBody(c, t, tab)
	})
}

// responseBody shows only the body. The read-only editor is rebuilt only when
// the response version or the Pretty/Raw choice changes, so switching views
// never shows stale content and large bodies are not rebuilt every frame.
func (a *App) responseBody(c *ui.Context, t *ui.Theme, tab *Tab) {
	if responseBodyText(tab.Resp, tab.RespRawIdx == 1) == "" {
		ui.Column(c).Grow(1).Center().Children(func() {
			ui.Text(c, "Response body is empty.").TextColor(t.TextMuted)
		})
		return
	}
	editor.View(c, a.responseEditorFor(tab)).Grow(1).FillWidth()
}

func (a *App) responseEditorFor(tab *Tab) *editor.State {
	if tab.respEditor != nil && tab.respBuiltVer == tab.respVersion && tab.respBuiltIdx == tab.RespRawIdx {
		return tab.respEditor
	}
	st := &editor.State{
		Buffer:   editor.NewBuffer(responseBodyText(tab.Resp, tab.RespRawIdx == 1)),
		ReadOnly: true,
	}
	if responseBodyIsJSON(tab.Resp) {
		st.Buffer.SetLanguage(editor.LanguageNamed("json"))
	}
	tab.respEditor = st
	tab.respBuiltVer = tab.respVersion
	tab.respBuiltIdx = tab.RespRawIdx
	return st
}

// responseHeaders shows the response headers as a read-only Key/Value table
// that keeps duplicates, scrolls, and allows selection/copy.
func (a *App) responseHeaders(c *ui.Context, t *ui.Theme, res *core.ExecResult) {
	rows := headerRows(res)
	if len(rows) == 0 {
		ui.Column(c).Grow(1).Center().Children(func() {
			ui.Text(c, "No response headers.").TextColor(t.TextMuted)
		})
		return
	}
	ui.Row(c).Gap(12).Padding(4, 10).Children(func() {
		ui.Text(c, "Key").FontSize(12).Bold().Width(200).TextColor(t.TextMuted)
		ui.Text(c, "Value").FontSize(12).Bold().TextColor(t.TextMuted)
	})
	ui.Scroll(c).Grow(1).FillWidth().Children(func() {
		ui.Column(c).Gap(2).FillWidth().Children(func() {
			for i, h := range rows {
				ui.Row(c).Key(i).Gap(12).FillWidth().AlignItems(ui.Center).Children(func() {
					ui.Text(c, h.Name).Width(200).FontSize(12).SingleLine().Selectable().TextColor(t.Text)
					col := t.Text
					if h.Sensitive {
						col = t.TextMuted
					}
					ui.Text(c, h.Value).Grow(1).FontSize(12).Selectable().TextColor(col)
				})
			}
		})
	})
}
