package desktop

import (
	"net/url"
	"strings"

	"github.com/egoist/mygo/ui"

	"gorequest/internal/desktop/uikit"
)

// This file owns the history panel's display-only controls added by U09: the
// endpoint-only switch, the shortened URL text and the row's compact action
// area. Nothing here changes what history stores, filters or restores.

// historyDisplayURL is the URL text a history row shows. With endpointOnly it
// keeps only the path, query and fragment; otherwise the stored URL is shown
// unchanged. Restore, filtering and the detail modal keep using r.URL.
func historyDisplayURL(raw string, endpointOnly bool) string {
	if !endpointOnly {
		return raw
	}
	return historyEndpoint(raw)
}

// historyEndpoint strips the scheme and authority (user info, host and port)
// from an absolute URL and keeps its escaped path, raw query and fragment. It
// parses rather than cutting the string, so IPv6 hosts, ports, escaped paths,
// queries and fragments survive. A relative reference, an opaque URI or
// anything the parser rejects is returned unchanged: a conservative fallback
// is safer than deleting part of a URL we do not understand.
func historyEndpoint(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return raw
	}
	path := u.EscapedPath()
	if path == "" {
		// A root URL has no path of its own; "/" keeps the endpoint readable.
		path = "/"
	}
	var b strings.Builder
	b.WriteString(path)
	if u.ForceQuery || u.RawQuery != "" {
		b.WriteByte('?')
		b.WriteString(u.RawQuery)
	}
	if frag := u.EscapedFragment(); frag != "" {
		b.WriteByte('#')
		b.WriteString(frag)
	}
	return b.String()
}

const (
	// historyActionSide is the compact hit target of one row action. The two
	// actions plus their gap reserve a fixed slot, so the row never reflows
	// when the pointer enters it.
	historyActionSide float32 = 24
	historyActionGap  float32 = 4
	historyActionIcon float32 = 14
	historyActionSlot float32 = historyActionSide*2 + historyActionGap
)

// historyEndpointToggle is the "endpoint only" switch beside Clear history. It
// is available with an empty list and in the all-project view, and its state
// lives in historyUI, so a refresh, a filter change or hiding and reopening the
// panel keeps it.
func (a *App) historyEndpointToggle(c *ui.Context, p uikit.Palette) {
	m := a.historyDisplayMsg()
	ui.Row(c).Gap(6).AlignItems(ui.Center).Shrink(0).Children(func() {
		ui.Text(c, m.EndpointOnly).FontSize(11).TextColor(p.Muted).SingleLine()
		sw := uikit.Switch(c, &a.historyUI.endpointOnly).Shrink(0).Label(m.EndpointOnly)
		sw.Tooltip(m.EndpointOnlyHint)
	})
}

// historyRowActions draws the row's independent action area: a neutral detail
// button and a Danger delete button. They are laid out in a reserved slot, not
// absolutely positioned, so they cannot overlap the status or duration cells or
// each other. Each is its own click target, so clicking one never restores the
// request (the row's action) or triggers the other.
func (a *App) historyRowActions(c *ui.Context, p uikit.Palette, t *ui.Theme, r historyRecord) {
	m := a.historyDisplayMsg()
	slot := ui.Row(c).Width(historyActionSlot).Gap(historyActionGap).AlignItems(ui.Center).Shrink(0)
	slot.Children(func() {
		historyActionButton(c, p, uikit.IconMore, m.Detail, m.Detail, p.Muted, p.Text, func() {
			a.historyUI.detail = r
			a.historyUI.detailOpen = true
		})
		historyActionButton(c, p, uikit.IconClose, m.Delete, m.Delete, t.Danger, t.Danger, func() {
			a.deleteHistory(r)
		})
	})
}

// historyActionButton is a compact 24 DIP icon control. It uses only existing
// palette/theme steps: a neutral or Danger icon, the shared hover/press washes
// and the theme's focus ring. The label is its accessible name and its tooltip
// names the action.
func historyActionButton(c *ui.Context, p uikit.Palette, ic *ui.SVG, label, tooltip string, rest, hover ui.Color, onClick func()) ui.Element {
	e := uikit.ActionCursor(ui.Box(c).Size(historyActionSide, historyActionSide).Radius(6).Center().Shrink(0)).Label(label)
	hovered := e.Hovered()
	bg := ui.Transparent
	switch {
	case e.Pressed():
		bg = p.Press
	case hovered:
		bg = p.Hover
	}
	e.Background(e.AnimateColor("history-action-bg", bg, uikit.AnimHover))
	e.DrawOver(func(painter *ui.Painter, r ui.Rect) {
		if e.FocusVisible() {
			painter.FocusRing(r, [4]float32{6, 6, 6, 6})
		}
	})
	col := rest
	if hovered {
		col = hover
	}
	e.Children(func() {
		ui.Icon(c, ic).FontSize(historyActionIcon).TextColor(col)
	})
	if tooltip != "" {
		e.Tooltip(tooltip)
	}
	if e.Clicked() {
		onClick()
	}
	return e
}
