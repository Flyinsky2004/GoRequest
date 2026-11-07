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

// JSON tree collapse/expand chevron metrics. The chevron gets a compact square
// hit target close to the row's text height instead of the theme button's wide
// padding (which made a single glyph float in a large box). Non-container rows
// reserve the same width and height so keys stay in one aligned column, row to
// row. Kept as constants so the container toggle and the placeholder cannot
// drift apart.
const (
	jsonTreeToggleSize  float32 = 16 // square hit target (px) for ▾/▸
	jsonTreeToggleGlyph float32 = 12 // chevron font size (px), matches row text
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

// responseBytesForSave returns the exact byte slice to write when saving a
// response to disk. Binary responses and Raw mode always preserve the exact
// raw bytes (res.Body) without UTF-8 conversion or formatting.
func responseBytesForSave(res *core.ExecResult, raw bool) []byte {
	if res == nil {
		return nil
	}
	if res.IsBinary || raw || res.Pretty == "" {
		return res.Body
	}
	return []byte(res.Pretty)
}

// defaultSaveFilename returns the suggested filename for saving a response.
func defaultSaveFilename(res *core.ExecResult) string {
	if res != nil && strings.TrimSpace(res.SuggestedFilename) != "" {
		return strings.TrimSpace(res.SuggestedFilename)
	}
	if res != nil && res.IsBinary {
		return "response.bin"
	}
	if responseBodyIsJSON(res) {
		return "response.json"
	}
	return "response.txt"
}

// saveResponseToPath writes a response payload to destPath, preserving raw
// binary bytes when res.IsBinary or raw is true.
func saveResponseToPath(res *core.ExecResult, raw bool, destPath string) error {
	if res == nil {
		return fmt.Errorf("no response to save")
	}
	if strings.TrimSpace(destPath) == "" {
		return fmt.Errorf("destination path is empty")
	}
	data := responseBytesForSave(res, raw)
	return os.WriteFile(destPath, data, 0o644)
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

// transferSizeSummary returns a human-readable comparison of wire transfer
// bytes vs decompressed body bytes when the response was compressed or when
// transfer size differs from body size.
func transferSizeSummary(res *core.ExecResult, nm *networkMessages) string {
	if res == nil || nm == nil {
		return ""
	}
	if res.TransferSize <= 0 {
		return ""
	}
	if !res.Decompressed && res.TransferSize == res.BodySize && res.ContentEncoding == "" {
		return ""
	}
	enc := res.ContentEncoding
	if enc == "" {
		enc = "identity"
	}
	return fmt.Sprintf(nm.TransferVsBodyFmt,
		formatByteSize(res.TransferSize),
		formatByteSize(res.BodySize),
		enc,
	)
}

// isCanceledErr reports whether a request error was a user cancellation.
func isCanceledErr(msg string) bool { return strings.Contains(msg, "canceled") }

// responseView draws the response panel: a status line (status-code capsule,
// protocol, duration and size as rolling tabular figures, plus wire vs
// decompressed size and redirect/TLS badges), a Body/Headers/Cookies/Network
// switch with Pretty/Raw/Tree for the body, quick access to the project's
// isolated Cookie Jar and Network/TLS settings, a send progress line and
// skeleton while a request is in flight, an inline error with a Retry pill, and
// the body as a read-only code view, interactive JSON tree, or binary inspector.
func (a *App) responseView(c *ui.Context, t *ui.Theme, tab *Tab) {
	m := a.msg()
	nm := a.networkMsg()
	p := uikit.PaletteFor(t)
	ps := a.ps
	if tab != nil && tab.Owner != nil {
		ps = tab.Owner
	}
	jar := a.cookieJarFor(ps)

	ui.Column(c).Grow(1).FillHeight().Background(p.BG).Children(func() {
		// Navigation is the first, non-shrinking group. Context controls wrap
		// after it; metrics and project settings have their own independent row.
		// Neither changing body mode nor a long status can move its left anchor.
		ui.Row(c).FillWidth().Wrap().Gap(8).Padding(6, 10).AlignItems(ui.Start).
			BorderWidth(1, 0, 0, 0).BorderColor(p.Hairline).Children(func() {
			if tab.Resp != nil && !tab.Sending && tab.RespErr == "" {
				responseSegments(c, t, &tab.RespSection, m.BodySection, m.HeadersSection, nm.CookiesSection, nm.NetworkSection)
				if tab.RespSection == 0 && !tab.Resp.IsBinary {
					ui.Row(c).Shrink(0).Gap(8).AlignItems(ui.Center).Children(func() {
						if responseBodyIsJSON(tab.Resp) && tab.Resp.BodySize <= largeResponseThreshold {
							responseSegments(c, t, &tab.RespRawIdx, m.Pretty, m.Raw, nm.TreeMode)
						} else {
							if tab.RespRawIdx > 1 {
								tab.RespRawIdx = 0
							}
							responseSegments(c, t, &tab.RespRawIdx, m.Pretty, m.Raw)
						}
						if b := uikit.IconButton(c, p, uikit.IconSearch, m.FindPlaceholder).Label(m.FindPlaceholder).Cursor(ui.CursorPointer); b.Clicked() {
							tab.respFindOpen = !tab.respFindOpen
							if tab.respFindOpen {
								tab.respFindFocus = true
							}
						}
					})
				}
				ui.Row(c).Shrink(0).Gap(8).Children(func() {
					if b := uikit.IconButton(c, p, uikit.IconCopy, m.CopyResponse).Label(m.CopyResponse).Cursor(ui.CursorPointer); b.Clicked() {
						c.WriteClipboard(responseBodyText(tab.Resp, tab.RespRawIdx == 1))
						a.status = m.Copied
					}
					if b := uikit.IconButton(c, p, uikit.IconFile, m.SaveResponse).Label(m.SaveResponse).Cursor(ui.CursorPointer); b.Clicked() {
						a.saveResponse(tab)
					}
				})
			}
		})
		ui.Row(c).FillWidth().Wrap().Gap(8).Padding(4, 10).AlignItems(ui.Center).Children(func() {
			a.responseStatus(c, tab, m, p)
			// Project settings remain usable even without a response.
			if ui.Button(c, fmt.Sprintf(nm.CookieJarBtnFmt, jar.Count())).Cursor(ui.CursorPointer).Clicked() {
				a.openCookieJarModal(ps)
			}
			if ui.Button(c, nm.NetworkBtn).Cursor(ui.CursorPointer).Clicked() {
				a.openNetworkSettingsModal(ps)
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
			a.cookieJarModal(c, t, tab)
			a.networkSettingsModal(c, t, tab)
			return
		}

		if tab.RespErr != "" {
			a.responseError(c, t, tab, m, p)
			a.cookieJarModal(c, t, tab)
			a.networkSettingsModal(c, t, tab)
			return
		}
		if tab.Resp == nil {
			// One empty state only (§7/D9): icon + one line + shortcut hint.
			ui.Column(c).Grow(1).Center().Gap(8).Children(func() {
				ui.Icon(c, uikit.IconSend).FontSize(28).TextColor(p.Faint)
				ui.Text(c, m.NoResponseHint).FontSize(13).TextColor(p.Muted)
				ui.Text(c, m.SendShortcutHint).FontSize(12).TextColor(p.Faint)
			})
			a.cookieJarModal(c, t, tab)
			a.networkSettingsModal(c, t, tab)
			return
		}
		switch tab.RespSection {
		case 1:
			a.responseHeaders(c, t, tab.Resp, p)
		case 2:
			a.responseCookiesView(c, t, tab, p)
		case 3:
			a.responseNetworkView(c, t, tab.Resp, p)
		default:
			a.responseFindBar(c, t, tab)
			a.responseBodyArea(c, t, tab, m, p)
		}
		a.cookieJarModal(c, t, tab)
		a.networkSettingsModal(c, t, tab)
	})
}

// responseSegments uses MyGo's native segmented radio/focus behavior and
// theme geometry. Setting the cursor on each clickable segment is necessary:
// MyGo stops cursor inheritance at clickable children of a Segmented track.
func responseSegments(c *ui.Context, t *ui.Theme, selected *int, labels ...string) {
	parts := ui.SegmentedBase(c, selected, len(labels))
	parts.Track.AlignItems(ui.Stretch).Padding(t.Space(0.5)).Gap(t.Space(0.5)).
		Radius(t.Radius).Background(t.Surface).Border(1, t.Border).Children(func() {
		for i, label := range labels {
			segment := parts.Segment(i).Cursor(ui.CursorPointer).
				Padding(t.Space(1), t.Space(3)).Gap(t.Space(1.5)).
				Radius(max(t.Radius-t.Space(0.5), 0)).TextColor(t.Text)
			if i == *selected {
				face := t.Background
				if t.Dark {
					face = t.SurfacePressed
				}
				segment.Background(face).Shadow(0, 1, 2, 0, ui.RGBA(0, 0, 0, 0.12))
			} else if segment.Pressed() {
				segment.Background(t.SurfacePressed)
			} else if segment.Hovered() {
				segment.Background(t.SurfaceHover)
			}
			segment.Children(func() { ui.Text(c, label).SingleLine() })
		}
	})
}

// findMatches returns the start offsets of query in text, case-insensitively.
// It is capped so a pathological query cannot stall a frame.
func findMatches(text, query string) []int {
	out, _ := findMatchesBounded(text, query, core.MaxResponseSearchMatches)
	return out
}

// findMatchesBounded returns up to maxMatches start offsets of query in text
// (case-insensitively) and reports whether additional matches were capped.
func findMatchesBounded(text, query string, maxMatches int) (out []int, capped bool) {
	if query == "" {
		return nil, false
	}
	if maxMatches <= 0 {
		maxMatches = core.MaxResponseSearchMatches
	}
	lower, q := strings.ToLower(text), strings.ToLower(query)
	for i := 0; i <= len(lower)-len(q); {
		j := strings.Index(lower[i:], q)
		if j < 0 {
			break
		}
		if len(out) >= maxMatches {
			return out, true
		}
		at := i + j
		out = append(out, at)
		i = at + len(q)
	}
	return out, false
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
// response editor (or filters the JSON tree when Tree mode is active), never
// opening a system dialog or the editor's own bar.
func (a *App) responseFindBar(c *ui.Context, t *ui.Theme, tab *Tab) {
	if !tab.respFindOpen {
		return
	}
	m := a.msg()
	nm := a.networkMsg()
	p := uikit.PaletteFor(t)

	inTreeMode := tab.Resp != nil && !tab.Resp.IsBinary &&
		tab.RespRawIdx == 2 && responseBodyIsJSON(tab.Resp) &&
		tab.Resp.BodySize <= largeResponseThreshold

	var st *editor.State
	var body string
	if !inTreeMode {
		st = a.responseEditorFor(t, tab)
		body = responseBodyText(tab.Resp, tab.RespRawIdx == 1)
	}

	ui.Row(c).FillWidth().Wrap().Gap(6).Padding(4, 10).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(p.Hairline).Children(func() {
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

		var matchCount int
		var matches []int
		var capped bool
		if inTreeMode {
			ts := a.ensureJSONTreeFor(tab)
			_, matchCount, capped = core.FlattenJSONTree(ts.jsonTreeRoot, ts.collapsedPaths, tab.respFindQuery, core.MaxResponseSearchMatches)
		} else {
			matches, capped = findMatchesBounded(body, tab.respFindQuery, core.MaxResponseSearchMatches)
			matchCount = len(matches)
		}

		ui.Box(c).Grow(1)
		if matchCount > 0 {
			if ui.Button(c, "↑").Cursor(ui.CursorPointer).Clicked() {
				tab.respFindIdx--
				tab.respFindJump = true
			}
			if ui.Button(c, "↓").Cursor(ui.CursorPointer).Clicked() {
				tab.respFindIdx++
				tab.respFindJump = true
			}
			// Clamp after the buttons so a click never indexes out of range.
			if tab.respFindIdx >= matchCount {
				tab.respFindIdx = 0
			}
			if tab.respFindIdx < 0 {
				tab.respFindIdx = matchCount - 1
			}
			ui.Textf(c, m.FindCount, tab.respFindIdx+1, matchCount).FontSize(11).
				FontFeatures("tnum").TextColor(p.Faint)
			if capped {
				ui.Text(c, fmt.Sprintf(nm.SearchCappedNote, core.MaxResponseSearchMatches)).
					FontSize(11).TextColor(p.Muted)
			}
		} else {
			tab.respFindIdx = 0
			if tab.respFindQuery != "" {
				ui.Text(c, m.FindNone).FontSize(11).TextColor(p.Faint)
			}
		}
		if b := uikit.IconButton(c, p, uikit.IconClose, m.Cancel).Label(m.Cancel).Cursor(ui.CursorPointer); b.Clicked() {
			tab.respFindOpen = false
		}
		// Jump only when the query or the chosen match changed, so the user's
		// own selection in the body is left alone on later frames.
		if !inTreeMode && tab.respFindJump && len(matches) > 0 && st != nil {
			start := matches[tab.respFindIdx]
			st.Select(start, start+len(tab.respFindQuery))
			st.ScrollTo(lineOf(body, start))
			tab.respFindJump = false
		} else if inTreeMode && tab.respFindJump {
			tab.respFindJump = false
		}
	})
}

// saveResponse writes the response body to a user-chosen local file. The native
// save dialog runs off the UI thread; the result is posted back through the
// owning window's Update, so no UI state is touched from a goroutine.
// Binary responses and Raw mode always write exact raw bytes without corruption.
func (a *App) saveResponse(tab *Tab) {
	if tab == nil || tab.Resp == nil {
		return
	}
	res := tab.Resp
	raw := tab.RespRawIdx == 1 || res.IsBinary
	defaultFile := defaultSaveFilename(res)
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
			DefaultPath: defaultFile,
			ButtonLabel: m.Save,
		})
		if err != nil || path == "" {
			return
		}
		if err := saveResponseToPath(res, raw, path); err != nil {
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
	nm := a.networkMsg()
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
		if summary := transferSizeSummary(tab.Resp, nm); summary != "" {
			ui.Text(c, summary).FontSize(11).FontFeatures("tnum").TextColor(p.Muted)
		}
		if len(tab.Resp.Redirects) > 0 {
			ui.Text(c, fmt.Sprintf(nm.RedirectsBadgeFmt, len(tab.Resp.Redirects))).
				FontSize(11).FontFeatures("tnum").TextColor(p.Accent)
		}
		if tab.Resp.Trace != nil && tab.Resp.Trace.TLSInsecureSkipVerify {
			ui.Text(c, nm.TLSInsecureBadge).
				FontSize(11).FontWeight(600).TextColor(uikit.MethodColor("DELETE", p))
		}
		if tab.Resp.Truncated {
			ui.Text(c, m.TruncatedNotice).FontSize(12).TextColor(uikit.MethodColor("POST", p))
		}
	default:
		// No response yet: the single centered empty state below carries the
		// message, so the status line stays blank here (§7/D9).
	}
}

// responseError shows a centered error with an accent Retry pill, never a
// dialog and never a red-tinted panel.
func (a *App) responseError(c *ui.Context, t *ui.Theme, tab *Tab, m *messages, p uikit.Palette) {
	ui.Column(c).Grow(1).FillWidth().Center().Gap(12).Children(func() {
		ui.Text(c, tab.RespErr).FontSize(13).TextColor(p.Muted).MaxWidth(460).TextAlign(ui.Center)
		if b := uikit.PillButton(c, p, uikit.PillPrimary, m.Retry, uikit.IconRetry).Cursor(ui.CursorPointer); b.Clicked() {
			a.send(tab)
		}
	})
}

// responseBodyArea draws the body: binary payload inspector for binary
// responses, interactive JSON tree when Tree mode is selected, a large-response
// plain-text fallback with a note, or the read-only code view.
func (a *App) responseBodyArea(c *ui.Context, t *ui.Theme, tab *Tab, m *messages, p uikit.Palette) {
	if tab.Resp != nil && tab.Resp.IsBinary {
		a.responseBinaryView(c, t, tab, p)
		return
	}
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
	} else if tab.Resp != nil && tab.RespRawIdx == 2 && responseBodyIsJSON(tab.Resp) {
		a.responseJSONTreeView(c, t, tab, p)
		return
	}
	editor.View(c, a.responseEditorFor(t, tab)).Grow(1).FillWidth()
}

// responseBinaryView renders the safe Binary Payload Inspector with MIME type,
// raw byte length, SHA-256 digest, bounded hex+ASCII preview, and raw binary
// file download trigger.
func (a *App) responseBinaryView(c *ui.Context, t *ui.Theme, tab *Tab, p uikit.Palette) {
	nm := a.networkMsg()
	res := tab.Resp
	if res == nil {
		return
	}
	shaHex, hexDump := core.FormatBinaryPreview(res.Body, 512)
	mimeLabel := res.ContentType
	if mimeLabel == "" {
		mimeLabel = "application/octet-stream"
	}

	ui.Column(c).Grow(1).FillWidth().Padding(10, 12).Gap(8).Children(func() {
		ui.Row(c).Gap(10).AlignItems(ui.Center).FillWidth().Children(func() {
			ui.Text(c, nm.BinaryPayloadTitle).FontSize(13).FontWeight(600).TextColor(p.Text)
			ui.Box(c).Grow(1)
			if ui.PrimaryButton(c, nm.SaveBinaryBtn).Cursor(ui.CursorPointer).Clicked() {
				a.saveResponse(tab)
			}
		})
		ui.Text(c, fmt.Sprintf(nm.BinaryInfoFmt, mimeLabel, res.BodySize, shaHex)).
			FontSize(11).Font("monospace").FontFeatures("tnum").Selectable().TextColor(p.Muted)
		if hexDump != "" {
			ui.Text(c, nm.BinaryPreviewLabel).FontSize(11).TextColor(p.Muted)
			ui.Scroll(c).Grow(1).FillWidth().BorderWidth(1).BorderColor(p.Hairline).Radius(6).Padding(6, 8).Children(func() {
				ui.Text(c, hexDump).FontSize(11).Font("monospace").Selectable().TextColor(p.Text)
			})
		}
	})
}

func (a *App) ensureJSONTreeFor(tab *Tab) *tabNetworkState {
	ts := tabNetStateFor(tab)
	if tab == nil || tab.Resp == nil {
		return ts
	}
	if ts.builtRespVer == tab.respVersion && (ts.jsonTreeRoot != nil || ts.jsonTreeErr != "") {
		return ts
	}
	root, trunc, err := core.ParseJSONTree(tab.Resp.Body, core.MaxJSONTreeNodes)
	ts.builtRespVer = tab.respVersion
	ts.jsonTreeRoot = root
	ts.jsonTreeTrunc = trunc
	if err != nil {
		ts.jsonTreeErr = err.Error()
	} else {
		ts.jsonTreeErr = ""
	}
	ts.collapsedPaths = make(map[string]bool)
	return ts
}

// responseJSONTreeView renders the interactive collapsible JSON Tree with
// JSONPath inspection, Expand/Collapse All, search match auto-expansion, and
// Copy Path / Copy Value actions.
func (a *App) responseJSONTreeView(c *ui.Context, t *ui.Theme, tab *Tab, p uikit.Palette) {
	nm := a.networkMsg()
	m := a.msg()
	ts := a.ensureJSONTreeFor(tab)
	if ts.jsonTreeRoot == nil {
		editor.View(c, a.responseEditorFor(t, tab)).Grow(1).FillWidth()
		return
	}

	rows, _, _ := core.FlattenJSONTree(ts.jsonTreeRoot, ts.collapsedPaths, tab.respFindQuery, core.MaxResponseSearchMatches)

	ui.Column(c).Grow(1).FillWidth().Children(func() {
		ui.Row(c).FillWidth().Wrap().Gap(8).Padding(4, 10).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(p.Hairline).Children(func() {
			if ui.Button(c, nm.ExpandAllBtn).Cursor(ui.CursorPointer).Clicked() {
				clear(ts.collapsedPaths)
			}
			if ui.Button(c, nm.CollapseAllBtn).Cursor(ui.CursorPointer).Clicked() {
				collapseAllContainers(ts.jsonTreeRoot, ts.collapsedPaths)
			}
			if ts.jsonTreeTrunc {
				ui.Text(c, fmt.Sprintf(nm.TreeTruncatedNote, core.MaxJSONTreeNodes)).
					FontSize(11).TextColor(uikit.MethodColor("POST", p))
			}
		})

		ui.Scroll(c).Grow(1).FillWidth().Padding(4, 10).Children(func() {
			ui.Column(c).Gap(2).FillWidth().Children(func() {
				for i, row := range rows {
					row := row
					n := row.Node
					indent := float32(n.Depth * 16)
					isCurrentMatch := row.Matched && row.MatchIndex == tab.respFindIdx

					r := ui.Row(c).Key(i).Gap(6).Padding(2, 4).AlignItems(ui.Center).FillWidth().Radius(4)
					if isCurrentMatch {
						r = r.Background(p.Hover)
					}
					r.Children(func() {
						if indent > 0 {
							ui.Box(c).Width(indent).Height(1)
						}
						if n.IsContainer() {
							glyph := "▾"
							if !row.Expanded {
								glyph = "▸"
							}
							// A look-free ButtonBase keeps the pointer/keyboard
							// behavior of Button but drops its wide theme padding,
							// so the chevron sits in a compact square instead of
							// floating in a large box.
							toggle := ui.ButtonBase(c).
								Size(jsonTreeToggleSize, jsonTreeToggleSize).
								Radius(3).Cursor(ui.CursorPointer)
							toggle.Children(func() {
								ui.Text(c, glyph).FontSize(jsonTreeToggleGlyph).TextColor(p.Muted)
							})
							if toggle.Clicked() {
								ts.collapsedPaths[n.Path] = row.Expanded
							}
						} else {
							ui.Box(c).Width(jsonTreeToggleSize).Height(jsonTreeToggleSize)
						}

						if n.Key != "" {
							ui.Text(c, n.Key+":").FontSize(12).Font("monospace").FontWeight(600).SingleLine().Selectable().TextColor(p.Text)
						}
						valColor := jsonKindColor(n.Kind, p)
						if row.Matched {
							valColor = p.Accent
						}
						ui.Text(c, n.Value).FontSize(12).Font("monospace").SingleLine().Selectable().TextColor(valColor)
						ui.Text(c, n.Path).FontSize(10).Font("monospace").SingleLine().TextColor(p.Faint)
						ui.Box(c).Grow(1)

						if ui.Button(c, nm.CopyJSONPathBtn).Cursor(ui.CursorPointer).Clicked() {
							c.WriteClipboard(n.Path)
							a.status = m.Copied
						}
						if ui.Button(c, nm.CopyValueBtn).Cursor(ui.CursorPointer).Clicked() {
							c.WriteClipboard(n.RawValue)
							a.status = m.Copied
						}
					})
				}
			})
		})
	})
}

func collapseAllContainers(n *core.JSONTreeNode, collapsed map[string]bool) {
	if n == nil {
		return
	}
	for _, c := range n.Children {
		if c.IsContainer() {
			collapsed[c.Path] = true
			collapseAllContainers(c, collapsed)
		}
	}
}

func jsonKindColor(kind core.JSONNodeKind, p uikit.Palette) ui.Color {
	switch kind {
	case core.JSONNodeString:
		return uikit.MethodColor("GET", p)
	case core.JSONNodeNumber:
		return uikit.MethodColor("POST", p)
	case core.JSONNodeBool:
		return uikit.MethodColor("PUT", p)
	case core.JSONNodeNull:
		return p.Muted
	default:
		return p.Text
	}
}

func (a *App) responseEditorFor(t *ui.Theme, tab *Tab) *editor.State {
	degraded := tab.Resp != nil && tab.Resp.BodySize > largeResponseThreshold
	if tab.respEditor != nil && tab.respBuiltVer == tab.respVersion &&
		tab.respBuiltIdx == tab.RespRawIdx && tab.respBuiltDegraded == degraded {
		// The buffer is unchanged; only a theme switch can make the cached
		// editor stale. Update its colors in place so it repaints immediately
		// without losing selection, scroll or undo history.
		a.syncEditorTheme(tab.respEditor, &tab.respBuiltGen, t)
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
	tab.respBuiltGen = a.themeGeneration()
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
