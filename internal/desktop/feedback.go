package desktop

import (
	"time"

	"github.com/egoist/mygo/ui"

	"gorequest/internal/desktop/uikit"
)

// feedbackToastID is the single toast identity shared by action confirmations.
// AddToast replaces a toast already showing under the same ID, so repeated
// clicks refresh the timer of the active pill instead of stacking toasts.
const feedbackToastID = "gorequest-action-feedback"

// feedbackToastTimeout keeps a confirmation visible long enough to read
// clearly without lingering over workspace or modal content.
const (
	feedbackToastTimeout = 1800 * time.Millisecond
	feedbackErrorTimeout = 3600 * time.Millisecond
	feedbackFadeOut      = 180 * time.Millisecond
)

// feedbackMessages holds localized short confirmations for common actions.
type feedbackMessages struct {
	CopySuccess    string
	SaveSuccess    string
	ExportOK       string
	DuplicateOK    string
	SaveFailed     string
	ExportFailed   string
	CopyURLTooltip string
}

var feedbackMessagesZh = feedbackMessages{
	CopySuccess:    "复制成功",
	SaveSuccess:    "保存成功",
	ExportOK:       "导出成功",
	DuplicateOK:    "已创建副本",
	SaveFailed:     "保存失败",
	ExportFailed:   "导出失败",
	CopyURLTooltip: "复制 URL",
}

var feedbackMessagesEn = feedbackMessages{
	CopySuccess:    "Copied to clipboard",
	SaveSuccess:    "Saved successfully",
	ExportOK:       "Exported successfully",
	DuplicateOK:    "Duplicated",
	SaveFailed:     "Save failed",
	ExportFailed:   "Export failed",
	CopyURLTooltip: "Copy URL",
}

func (a *App) feedbackMsg() *feedbackMessages {
	if a != nil && a.mgr != nil && a.mgr.cfg != nil && parseLanguage(string(a.mgr.cfg.language())) == langEnglish {
		return &feedbackMessagesEn
	}
	return &feedbackMessagesZh
}

// feedbackToast is one queued action outcome awaiting DrainFeedback in View.
type feedbackToast struct {
	title, description string
	kind               string // "success" or "error"
}

// notifySuccess queues a short visible toast after an action has completed.
// Must be called on the UI thread (inside View, OnShortcut, or win.Update).
func (a *App) notifySuccess(title string) {
	if title == "" {
		return
	}
	a.queueFeedback(feedbackToast{title: title, kind: "success"})
}

// notifyError queues an error toast; it never reports success.
func (a *App) notifyError(title, detail string) {
	if title == "" {
		return
	}
	a.queueFeedback(feedbackToast{title: title, description: detail, kind: "error"})
}

// notifyCopied confirms a copy once non-empty text has been sent to the
// clipboard. ui.Context.WriteClipboard returns no error (the platform API does
// not expose clipboard write failures to callers), so callers guard against nil
// contexts and empty payloads rather than reporting unverified outcomes.
func (a *App) notifyCopied(text string) {
	if text == "" {
		return
	}
	a.notifySuccess(a.feedbackMsg().CopySuccess)
}

// copyToClipboard writes non-empty text to c's clipboard and records both the
// status-bar message and the visible toast. When c is nil or text is empty it
// does nothing and returns false so callers never report a false copy.
func (a *App) copyToClipboard(c *ui.Context, text string) bool {
	if c == nil || text == "" {
		return false
	}
	c.WriteClipboard(text)
	if a != nil {
		a.status = a.msg().Copied
		a.notifyCopied(text)
	}
	return true
}

func (a *App) queueFeedback(f feedbackToast) {
	if a == nil {
		return
	}
	a.feedbackPending = &f
	// When called outside View (for example from win.Update after an async file
	// save), wake a frame on a live window so an idle view renders the toast.
	if !a.inView && a.win != nil && !a.win.IsDestroyed() {
		a.win.Update(func() {})
	}
}

// drainFeedback commits any queued feedback into MyGo's toast manager. Called
// at the end of App.View after all workspace views and modals have run so
// clicks anywhere in the window show in the same frame.
func (a *App) drainFeedback(c *ui.Context) {
	if a == nil || c == nil {
		return
	}
	f := a.feedbackPending
	if f == nil {
		return
	}
	a.feedbackPending = nil
	timeout := feedbackToastTimeout
	if f.kind == "error" {
		timeout = feedbackErrorTimeout
	}
	c.AddToast(ui.Toast{
		ID:          feedbackToastID,
		Title:       f.title,
		Description: f.description,
		Type:        f.kind,
		Timeout:     timeout,
	})
}

// renderFeedbackViewport renders the window's toasts above workspace and modal
// layers using MyGo's ToastViewportBase. Confirmation toasts without action
// buttons are marked PassThrough so they never intercept repeated clicks or
// freeze while hovered, start at full opacity on frame 0 so rapid clicks stay
// crisply visible, and sit 36 DIPs above the bottom edge clear of the status
// bar.
func (a *App) renderFeedbackViewport(c *ui.Context, t *ui.Theme) {
	if c == nil || t == nil {
		return
	}
	p := uikit.PaletteFor(t)
	winW, _ := c.Size()
	if winW <= 0 {
		winW = 600
	}
	maxW := min(float32(420), max(float32(180), winW-40))

	fill, fg := p.Text, p.BG
	subFG := p.BG.Alpha(0.78)
	okColor, hasOK := uikit.StatusPillColor(200, p)
	if !hasOK {
		okColor = t.Accent
	}
	errColor := t.Danger

	ui.ToastViewportBase(c, func(viewport ui.Element, toasts []ui.Toast) {
		viewport.Padding(0, 16, 36, 16).AlignItems(ui.Center).Gap(8)
		for _, ts := range toasts {
			parts := ui.ToastBase(c, ts)
			pass := ts.Action == ""
			if pass {
				parts.Root.PassThrough()
			}
			opacity := float32(1)
			if left := parts.Left(); left > 0 && left < feedbackFadeOut {
				opacity = float32(left) / float32(feedbackFadeOut)
				c.AnimationFrame()
			} else if left > 0 {
				c.After(left - feedbackFadeOut)
			}

			box := parts.Root.Row().AlignItems(ui.Center).Gap(8).
				Padding(7, 14).Radius(999).MaxWidth(maxW).
				Background(fill).TextColor(fg).
				Shadow(0, 6, 20, 0, ui.RGBA(0, 0, 0, 0.24)).
				Opacity(opacity)
			box.Children(func() {
				var ic ui.Element
				switch ts.Type {
				case "error":
					ic = ui.Icon(c, uikit.IconClose).FontSize(14).TextColor(errColor).Shrink(0)
				default:
					ic = ui.Icon(c, uikit.IconCheck).FontSize(14).TextColor(okColor).Shrink(0)
				}
				if pass {
					ic.PassThrough()
				}
				if ts.Description == "" {
					txt := ui.Text(c, ts.Title).FontSize(12).FontWeight(600).TextColor(fg).
						SingleLine().Shrink(1).MinWidth(0).Ellipsis("…")
					if pass {
						txt.PassThrough()
					}
				} else {
					col := ui.Column(c).Shrink(1).MinWidth(0).Gap(1)
					if pass {
						col.PassThrough()
					}
					col.Children(func() {
						t1 := ui.Text(c, ts.Title).FontSize(12).FontWeight(600).TextColor(fg).
							SingleLine().Ellipsis("…")
						t2 := ui.Text(c, ts.Description).FontSize(11).TextColor(subFG).
							SingleLine().Ellipsis("…")
						if pass {
							t1.PassThrough()
							t2.PassThrough()
						}
					})
				}
				if ts.Action != "" {
					b := uikit.ActionCursor(parts.ActionButton()).Padding(2, 8).Radius(6).
						TextColor(t.Accent).FontWeight(600).Shrink(0)
					b.Children(func() { ui.Text(c, ts.Action).FontSize(12).SingleLine() })
				}
			})
		}
	})
}

// saveActiveWithFeedback saves the active tab and shows a "saved" toast only
// when saveActiveSynced genuinely wrote the tab to disk; conflicts and write
// errors surface as error toasts instead of false success.
func (a *App) saveActiveWithFeedback() {
	if a == nil || a.ps == nil {
		return
	}
	tab := a.ps.activeTab()
	if tab == nil {
		a.saveActiveSynced()
		return
	}
	prevStatus := a.status
	a.status = ""
	a.saveActiveSynced()
	if a.status != "" && a.status == translate(a.msg().Saved, tabRelPath(tab)) {
		a.notifySuccess(a.feedbackMsg().SaveSuccess)
	} else if a.status != "" {
		a.notifyError(a.feedbackMsg().SaveFailed, a.status)
	} else {
		a.status = prevStatus
	}
}
