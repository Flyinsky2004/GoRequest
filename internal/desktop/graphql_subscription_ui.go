package desktop

import (
	"fmt"

	"github.com/egoist/mygo/ui"

	"gorequest/internal/desktop/uikit"
)

// graphql_subscription_ui.go renders the live graphql-transport-ws pane. It is
// deliberately separate from the HTTP result pane: transport state, per-event
// facts and the close code/reason are never folded into an HTTP status.

// gqlSubscriptionActive reports whether the lower pane must show live
// subscription facts instead of an HTTP response.
func gqlSubscriptionActive(st *gqlTabState) bool {
	return st != nil && st.sub != nil && (st.sub.state != "" || st.sub.running)
}

// gqlSubscriptionPane renders connection state, the bounded event list, the
// explicit confirmation step and the terminal close facts.
func (a *App) gqlSubscriptionPane(c *ui.Context, t *ui.Theme, tab *Tab, st *gqlTabState, m *gqlMessages, p uikit.Palette) {
	d := st.sub
	ui.Column(c).Grow(1).FillHeight().Background(p.BG).Children(func() {
		ui.Row(c).FillWidth().Wrap().Gap(8).Padding(6, 10).AlignItems(ui.Center).
			BorderWidth(1, 0, 0, 0).BorderColor(p.Hairline).Children(func() {
			ui.Text(c, m.SubTitle).FontSize(12).FontWeight(600).TextColor(p.Text)
			gqlSubStateChip(c, d, p)
			ui.Box(c).Grow(1)
			if d.running {
				if uikit.ActionCursor(ui.Button(c, m.SubCancelBtn)).Clicked() {
					a.gqlSubCancel(tab)
				}
			}
		})
		if d.notice != "" {
			ui.Text(c, d.notice).FontSize(11).TextColor(p.Muted).Padding(2, 10).SingleLine()
		}
		if d.closeCode != 0 || d.closeReason != "" {
			ui.Text(c, fmt.Sprintf(m.SubCloseFmt, d.closeCode, d.closeReason)).FontSize(11).
				Font("monospace").TextColor(p.Faint).Padding(2, 10).SingleLine()
		}
		if d.confirmAsked {
			ui.Column(c).FillWidth().Gap(6).Padding(10).Background(p.Sidebar).Radius(8).
				BorderWidth(1).BorderColor(t.Warning).Children(func() {
				ui.Text(c, m.SubConfirmNeeded).FontSize(12).FontWeight(600).TextColor(t.Warning)
				if d.promptFacts != "" {
					ui.Text(c, d.promptFacts).FontSize(11).TextColor(p.Muted)
				}
				uikit.ActionCursor(ui.Checkbox(c, &d.confirmChecked, m.SubConfirmCheck))
			})
		}
		if len(d.messages) == 0 {
			ui.Column(c).Grow(1).Center().Children(func() {
				ui.Text(c, m.SubNoMessages).FontSize(12).TextColor(p.Faint)
			})
			return
		}
		ui.Scroll(c).Grow(1).FillWidth().Padding(6, 10).Children(func() {
			ui.Column(c).Gap(4).Children(func() {
				if d.truncated {
					ui.Text(c, fmt.Sprintf(m.SubTruncatedFmt, gqlSubMessageLimit)).FontSize(10).
						TextColor(p.Warning)
				}
				for _, msg := range d.messages {
					row := ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Start)
					row.Children(func() {
						ui.Text(c, fmt.Sprintf("#%d", msg.Seq)).FontSize(10).Font("monospace").
							TextColor(p.Faint).Width(52)
						col := p.Text
						if msg.IsError {
							col = p.Danger
						}
						ui.Text(c, msg.Type).FontSize(10).Font("monospace").TextColor(col).Width(56)
						ui.Text(c, msg.Preview).FontSize(11).Font("monospace").TextColor(col).
							Selectable().Grow(1)
					})
				}
			})
		})
	})
}

// gqlSubStateChip shows the transport state as text + color, never color alone.
func gqlSubStateChip(c *ui.Context, d *gqlSubscriptionState, p uikit.Palette) {
	col := p.Muted
	switch d.state {
	case "open":
		col = p.Success
	case "completed":
		col = p.Success
	case "error":
		col = p.Danger
	case "canceled":
		col = p.Warning
	}
	ui.Text(c, d.state).FontSize(11).Font("monospace").TextColor(col).
		Padding(2, 6).Background(p.Sidebar).Radius(6).SingleLine()
}
