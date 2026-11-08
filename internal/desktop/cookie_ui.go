package desktop

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"

	"gorequest/internal/core"
	"gorequest/internal/desktop/uikit"
)

// cookieModalState holds the UI draft state for viewing, adding, editing, and
// clearing cookies in a project's isolated CookieJar.
type cookieModalState struct {
	open         bool
	filter       string
	revealedKeys map[string]bool
	editDomain   string
	editPath     string
	editName     string
	editValue    string
	editHostOnly bool
	editSecure   bool
	editHttpOnly bool
	editSameSite string
	errMsg       string
}

func newCookieModalState() *cookieModalState {
	return &cookieModalState{
		revealedKeys: make(map[string]bool),
		editPath:     "/",
	}
}

// CookieJarForProject returns the isolated *core.CookieJar owned by ps. Two
// projects never share a CookieJar, and no cookie is ever persisted into
// .gorequest/ repository files.
func (a *App) cookieJarFor(ps *projectState) *core.CookieJar {
	if ps == nil && a != nil {
		ps = a.ps
	}
	st := projectNetStateFor(ps, a)
	if st == nil {
		return core.NewSessionCookieJar()
	}
	return st.cookieJar
}

// OpenCookieJarModal opens the Cookie Jar manager dialog for the active project.
func (a *App) openCookieJarModal(ps *projectState) {
	if ps == nil && a != nil {
		ps = a.ps
	}
	st := projectNetStateFor(ps, a)
	if st == nil {
		return
	}
	st.cookieUI.errMsg = ""
	if st.cookieUI.editPath == "" {
		st.cookieUI.editPath = "/"
	}
	st.cookieUI.open = true
}

func (a *App) cookieJarModal(c *ui.Context, t *ui.Theme, tab *Tab) {
	ps := a.ps
	if tab != nil && tab.Owner != nil {
		ps = tab.Owner
	}
	st := projectNetStateFor(ps, a)
	if st == nil || !st.cookieUI.open {
		return
	}
	nm := a.networkMsg()
	m := a.msg()
	p := uikit.PaletteFor(t)
	jar := st.cookieJar
	cui := st.cookieUI

	modal := ui.Modal(c, &cui.open, func() {
		ui.Column(c).Width(680).MaxHeight(560).Gap(10).Padding(16).Children(func() {
			ui.Row(c).AlignItems(ui.Center).Gap(8).FillWidth().Children(func() {
				ui.Text(c, nm.CookieJarTitle).FontSize(16).Bold()
				scopeName := "session"
				if ps != nil {
					scopeName = ps.name()
				}
				ui.Text(c, fmt.Sprintf("(%s · %d)", scopeName, jar.Count())).
					FontSize(12).FontFeatures("tnum").TextColor(p.Muted)
				ui.Box(c).Grow(1)
				if jar.Count() > 0 {
					if uikit.ActionCursor(ui.Button(c, nm.CookieClearAll)).TextColor(t.Danger).Clicked() {
						jar.Clear()
						cui.errMsg = ""
						a.status = nm.CookieClearedStatus
					}
				}
			})

			ui.Text(c, nm.CookieIsolationNote).FontSize(11).TextColor(p.Muted)

			// Filter bar
			ui.Row(c).Gap(8).AlignItems(ui.Center).FillWidth().Children(func() {
				ui.Icon(c, uikit.IconSearch).FontSize(13).TextColor(p.Muted)
				ui.TextInput(c, &cui.filter).Grow(1).Placeholder(nm.CookieFilterPlaceholder)
			})

			// Existing cookies list
			entries := jar.Entries()
			q := strings.ToLower(strings.TrimSpace(cui.filter))
			var filtered []core.CookieEntry
			for _, e := range entries {
				if q == "" ||
					strings.Contains(strings.ToLower(e.Domain), q) ||
					strings.Contains(strings.ToLower(e.Name), q) ||
					strings.Contains(strings.ToLower(e.Path), q) {
					filtered = append(filtered, e)
				}
			}

			ui.Scroll(c).MaxHeight(210).FillWidth().BorderWidth(1).BorderColor(p.Hairline).Radius(8).Padding(6, 8).Children(func() {
				if len(filtered) == 0 {
					ui.Column(c).Padding(16, 8).Center().FillWidth().Children(func() {
						ui.Text(c, nm.CookieEmptyHint).FontSize(12).TextColor(p.Muted)
					})
					return
				}
				ui.Column(c).Gap(4).FillWidth().Children(func() {
					for i, ck := range filtered {
						ck := ck
						key := ck.Key()
						revealed := cui.revealedKeys[key]
						ui.Row(c).Key(i).Gap(8).AlignItems(ui.Center).Padding(4, 4).FillWidth().
							BorderWidth(0, 0, 1, 0).BorderColor(p.Hairline).Children(func() {
							domLabel := ck.Domain
							if !ck.HostOnly && !strings.HasPrefix(domLabel, ".") {
								domLabel = "." + domLabel
							}
							ui.Text(c, domLabel+ck.Path).Width(160).FontSize(11).Font("monospace").SingleLine().TextColor(p.Muted)
							ui.Text(c, ck.Name).Width(110).FontSize(12).FontWeight(600).SingleLine().Selectable().TextColor(p.Text)
							valText := "***"
							if revealed {
								valText = ck.Value
							}
							ui.Text(c, valText).Grow(1).FontSize(12).Font("monospace").SingleLine().Selectable().TextColor(p.Text)

							var flags []string
							if ck.Secure {
								flags = append(flags, "Secure")
							}
							if ck.HttpOnly {
								flags = append(flags, "HttpOnly")
							}
							if ck.SameSite != "" {
								flags = append(flags, "SameSite="+ck.SameSite)
							}
							if len(flags) > 0 {
								ui.Text(c, strings.Join(flags, " · ")).FontSize(10).TextColor(p.Faint).SingleLine()
							}

							revealLabel := nm.RevealSecret
							if revealed {
								revealLabel = nm.HideSecret
							}
							if uikit.ActionCursor(ui.Button(c, revealLabel)).Clicked() {
								cui.revealedKeys[key] = !revealed
							}
							if uikit.ActionCursor(ui.Button(c, nm.CookieEditBtn)).Clicked() {
								cui.editDomain = ck.Domain
								cui.editPath = ck.Path
								cui.editName = ck.Name
								cui.editValue = ck.Value
								cui.editHostOnly = ck.HostOnly
								cui.editSecure = ck.Secure
								cui.editHttpOnly = ck.HttpOnly
								cui.editSameSite = ck.SameSite
								cui.errMsg = ""
							}
							if uikit.ActionCursor(ui.Button(c, m.Delete)).TextColor(t.Danger).Clicked() {
								jar.Delete(ck.Domain, ck.Path, ck.Name)
							}
						})
					}
				})
			})

			// Add / Edit Cookie form
			ui.Column(c).Gap(6).Padding(8, 10).BorderWidth(1).BorderColor(p.Hairline).Radius(8).FillWidth().Children(func() {
				ui.Text(c, nm.CookieUpsertHeader).FontSize(12).FontWeight(600)
				ui.Row(c).Gap(8).AlignItems(ui.Center).FillWidth().Children(func() {
					ui.TextInput(c, &cui.editDomain).Width(170).Font("monospace").Placeholder(nm.CookieDomainPlaceholder)
					ui.TextInput(c, &cui.editPath).Width(90).Font("monospace").Placeholder("/")
					ui.TextInput(c, &cui.editName).Width(130).Font("monospace").Placeholder(nm.CookieNamePlaceholder)
					ui.TextInput(c, &cui.editValue).Grow(1).Font("monospace").Placeholder(nm.CookieValuePlaceholder)
				})
				ui.Row(c).Gap(12).AlignItems(ui.Center).FillWidth().Children(func() {
					uikit.ActionCursor(ui.Checkbox(c, &cui.editHostOnly, "HostOnly"))
					uikit.ActionCursor(ui.Checkbox(c, &cui.editSecure, "Secure"))
					uikit.ActionCursor(ui.Checkbox(c, &cui.editHttpOnly, "HttpOnly"))
					ui.Box(c).Grow(1)
					if uikit.ActionCursor(uikit.PrimaryButton(c, nm.CookieSaveBtn)).Clicked() {
						err := jar.Upsert(core.CookieEntry{
							Domain:   cui.editDomain,
							Path:     cui.editPath,
							Name:     cui.editName,
							Value:    cui.editValue,
							HostOnly: cui.editHostOnly,
							Secure:   cui.editSecure,
							HttpOnly: cui.editHttpOnly,
							SameSite: cui.editSameSite,
						})
						if err != nil {
							cui.errMsg = err.Error()
						} else {
							cui.errMsg = ""
							cui.editName = ""
							cui.editValue = ""
							a.notifySuccess(a.feedbackMsg().SaveSuccess)
						}
					}
				})
				if cui.errMsg != "" {
					ui.Text(c, cui.errMsg).FontSize(11).TextColor(t.Danger)
				}
			})

			ui.Row(c).Justify(ui.End).Children(func() {
				if uikit.ActionCursor(ui.Button(c, m.Close)).Clicked() {
					cui.open = false
				}
			})
		})
	})
	if modal.Dismissed() {
		cui.open = false
	}
}

// cookieSectionLabel names the response Cookies view in the tool row. The
// project Cookie Jar count is attached only when non-zero, so an empty jar
// reads as the plain view name instead of "Cookies · 0".
func cookieSectionLabel(nm *networkMessages, count int) string {
	if nm == nil || count <= 0 {
		if nm == nil {
			return ""
		}
		return nm.CookiesSection
	}
	return fmt.Sprintf("%s · %d", nm.CookiesSection, count)
}

// responseCookiesView renders the Cookies tab inside the Response panel,
// showing Set-Cookie entries from the response and providing direct access to
// the project's isolated Cookie Jar manager.
func (a *App) responseCookiesView(c *ui.Context, t *ui.Theme, tab *Tab, p uikit.Palette) {
	nm := a.networkMsg()
	ps := a.ps
	if tab != nil && tab.Owner != nil {
		ps = tab.Owner
	}
	jar := a.cookieJarFor(ps)
	st := projectNetStateFor(ps, a)

	var respCookies []core.CookieEntry
	if tab != nil && tab.Resp != nil {
		if len(tab.Resp.Cookies) > 0 {
			respCookies = tab.Resp.Cookies
		} else {
			respCookies = core.ParseResponseCookies(tab.Resp.FinalURL, tab.Resp.Headers)
		}
	}

	ui.Column(c).Grow(1).FillWidth().Children(func() {
		ui.Row(c).Gap(10).Padding(6, 10).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(p.Hairline).Children(func() {
			ui.Text(c, fmt.Sprintf("%s (%d) · %s (%d)",
				nm.ResponseCookiesHeader, len(respCookies),
				nm.CookieJarTitle, jar.Count())).
				FontSize(12).FontFeatures("tnum").TextColor(p.Muted)
			ui.Box(c).Grow(1)
			if uikit.ActionCursor(ui.Button(c, nm.OpenCookieJarBtn)).Clicked() {
				a.openCookieJarModal(ps)
			}
		})

		if len(respCookies) == 0 {
			ui.Column(c).Grow(1).Center().Gap(6).Children(func() {
				ui.Text(c, nm.NoResponseCookies).FontSize(12).TextColor(p.Muted)
			})
			return
		}

		ui.Scroll(c).Grow(1).FillWidth().Padding(6, 10).Children(func() {
			ui.Column(c).Gap(4).FillWidth().Children(func() {
				for i, ck := range respCookies {
					ck := ck
					key := "resp:" + ck.Key()
					revealed := st != nil && st.cookieUI.revealedKeys[key]
					ui.Row(c).Key(i).Gap(10).AlignItems(ui.Center).Padding(4, 0).FillWidth().
						BorderWidth(0, 0, 1, 0).BorderColor(p.Hairline).Children(func() {
						ui.Text(c, ck.Name).Width(150).FontSize(12).FontWeight(600).SingleLine().Selectable().TextColor(p.Text)
						val := "***"
						if revealed {
							val = ck.Value
						}
						ui.Text(c, val).Grow(1).FontSize(12).Font("monospace").SingleLine().Selectable().TextColor(p.Text)
						ui.Text(c, ck.Domain+ck.Path).Width(160).FontSize(11).Font("monospace").SingleLine().TextColor(p.Muted)
						if st != nil {
							lbl := nm.RevealSecret
							if revealed {
								lbl = nm.HideSecret
							}
							if uikit.ActionCursor(ui.Button(c, lbl)).Clicked() {
								st.cookieUI.revealedKeys[key] = !revealed
							}
						}
					})
				}
			})
		})
	})
}
