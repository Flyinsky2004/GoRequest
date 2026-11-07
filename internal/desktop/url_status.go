package desktop

import (
	"github.com/egoist/mygo/ui"

	"gorequest/internal/desktop/uikit"
)

// urlSendState is the request line's send outcome. It is derived from the
// tab's existing send fields, so the URL group needs no new Tab state.
type urlSendState int

const (
	urlSendIdle urlSendState = iota
	urlSendSending
	urlSendSuccess
	urlSendFailure
	urlSendCanceled
)

// urlSendStateOf classifies the tab's last send. Sending wins over a result
// left from the previous attempt; a canceled error is terminal but neutral;
// any other transport error (timeout, DNS, connection, TLS) and any HTTP
// status at or above 400 are failures.
func urlSendStateOf(tab *Tab) urlSendState {
	if tab == nil {
		return urlSendIdle
	}
	if tab.Sending {
		return urlSendSending
	}
	if tab.RespErr != "" {
		if isCanceledErr(tab.RespErr) {
			return urlSendCanceled
		}
		return urlSendFailure
	}
	if tab.Resp != nil {
		if tab.Resp.Status >= 400 {
			return urlSendFailure
		}
		return urlSendSuccess
	}
	return urlSendIdle
}

// urlStatusBorderColor maps the send state to the request line's border color.
// It reuses the existing palette steps: GET green for success, DELETE red for
// failure, Muted while in flight or after a cancel, and Hairline at rest.
// Focus is drawn as a separate ring, so the status color never hides it.
func urlStatusBorderColor(state urlSendState, p uikit.Palette) ui.Color {
	switch state {
	case urlSendSending, urlSendCanceled:
		return p.Muted
	case urlSendSuccess:
		return uikit.MethodColor("GET", p)
	case urlSendFailure:
		return uikit.MethodColor("DELETE", p)
	default:
		return p.Hairline
	}
}
