package desktop

import (
	"time"

	"github.com/egoist/mygo/ui"
)

// feedbackToastID is the single toast identity shared by every action
// confirmation. AddToast replaces a toast showing under the same ID, so a
// repeated click restarts the short display instead of stacking toasts, and
// only one small confirmation is ever on screen over the window content.
const feedbackToastID = "gorequest-action-feedback"

// feedbackToastTimeout keeps a confirmation visible long enough to read but
// short enough never to cover content for long.
const feedbackToastTimeout = 1800 * time.Millisecond

// feedbackMessages are the localized confirmations for common actions.
type feedbackMessages struct {
	CopySuccess string
	SaveSuccess string
	ExportOK    string
	SaveFailed  string
}

var feedbackMessagesZh = feedbackMessages{
	CopySuccess: "复制成功",
	SaveSuccess: "保存成功",
	ExportOK:    "导出成功",
	SaveFailed:  "保存失败",
}

var feedbackMessagesEn = feedbackMessages{
	CopySuccess: "Copied",
	SaveSuccess: "Saved",
	ExportOK:    "Exported",
	SaveFailed:  "Save failed",
}

func (a *App) feedbackMsg() *feedbackMessages {
	if a != nil && a.mgr != nil && a.mgr.cfg != nil && parseLanguage(string(a.mgr.cfg.language())) == langEnglish {
		return &feedbackMessagesEn
	}
	return &feedbackMessagesZh
}

// feedbackToast is one queued confirmation.
type feedbackToast struct {
	title, description string
	kind               string // "success" or "error"
}

// notifySuccess queues a short success toast. Call it only after the action
// has really succeeded. It must run on the UI thread (a click handler or a
// win.Update callback); the toast is shown at the end of the current frame.
func (a *App) notifySuccess(title string) {
	a.queueFeedback(feedbackToast{title: title, kind: "success"})
}

// notifyError queues a short error toast; it never claims success.
func (a *App) notifyError(title, detail string) {
	a.queueFeedback(feedbackToast{title: title, description: detail, kind: "error"})
}

// notifyCopied confirms a copy that really placed non-empty text on the
// clipboard. WriteClipboard cannot fail, so the empty case is the only one
// that must not be reported as copied.
func (a *App) notifyCopied(text string) {
	if text == "" {
		return
	}
	a.notifySuccess(a.feedbackMsg().CopySuccess)
}

func (a *App) queueFeedback(f feedbackToast) {
	if a == nil {
		return
	}
	a.feedbackPending = &f
	if a.win != nil && !a.win.IsDestroyed() {
		a.win.Update(func() {}) // wake a frame so an idle window shows it
	}
}

// drainFeedback shows the queued confirmation. It is called once per frame
// from View, after the content, so a click handled during the frame shows
// immediately.
func (a *App) drainFeedback(c *ui.Context) {
	f := a.feedbackPending
	if f == nil {
		return
	}
	a.feedbackPending = nil
	timeout := feedbackToastTimeout
	if f.kind == "error" {
		timeout = 4 * time.Second
	}
	c.AddToast(ui.Toast{
		ID:          feedbackToastID,
		Title:       f.title,
		Description: f.description,
		Type:        f.kind,
		Timeout:     timeout,
	})
}

// saveActiveWithFeedback saves the active tab and shows a "saved" toast only
// when the save really completed: saveActiveSynced reports success by setting
// the localized Saved status, while conflicts and failures set other text.
func (a *App) saveActiveWithFeedback() {
	if a == nil || a.ps == nil {
		return
	}
	tab := a.ps.activeTab()
	if tab == nil {
		a.saveActiveSynced()
		return
	}
	a.status = ""
	a.saveActiveSynced()
	if a.status != "" && a.status == translate(a.msg().Saved, tabRelPath(tab)) {
		a.notifySuccess(a.feedbackMsg().SaveSuccess)
	} else if a.status != "" {
		a.notifyError(a.feedbackMsg().SaveFailed, a.status)
	}
}
