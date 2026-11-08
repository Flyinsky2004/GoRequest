package desktop

import (
	"fmt"

	"github.com/egoist/mygo/ui"

	"gorequest/internal/desktop/uikit"
)

// updateModalFor draws the manager-level update modal in the window that owns
// it. Both App.View and welcome.View call it; only the host window renders it,
// so two open windows never show the same prompt twice.
func (m *Manager) updateModalFor(w window, c *ui.Context, t *ui.Theme) {
	if m == nil || m.update == nil {
		return
	}
	m.update.modalFor(w, c, t)
}

func (u *updateController) modalFor(w window, c *ui.Context, t *ui.Theme) {
	if u == nil || !u.dialogOpen {
		return
	}
	h := u.getHost()
	if (h == nil || h.IsDestroyed()) && u.mgr != nil {
		h = u.mgr.updateHostWindow()
		u.setHost(h)
	}
	if h != nil && h != w {
		return
	}
	lang := defaultLanguage()
	if u.mgr != nil && u.mgr.cfg != nil {
		lang = u.mgr.cfg.language()
	}
	u.renderModal(c, t, lang)
}

func (u *updateController) renderModal(c *ui.Context, t *ui.Theme, l language) {
	um := updateMessagesFor(l)
	p := uikit.PaletteFor(t)
	open := true
	ui.Modal(c, &open, func() {
		ui.Column(c).Gap(12).MinWidth(420).Children(func() {
			switch u.dialogKind {
			case updateDialogChecking:
				u.renderChecking(c, p, um)
			case updateDialogAvailable:
				u.renderAvailable(c, p, um)
			case updateDialogDownloading:
				u.renderDownloading(c, p, um)
			case updateDialogResult:
				u.renderResult(c, t, p, um)
			}
		})
	})
	if !open {
		// Escape or an outside click dismisses the dialog. An in-flight
		// check or download is canceled instead of being left running with
		// no visible owner.
		if u.dialogKind == updateDialogChecking || u.dialogKind == updateDialogDownloading {
			u.cancelCheck()
		} else {
			u.closeDialog()
		}
	}
}

func (u *updateController) renderChecking(c *ui.Context, p uikit.Palette, um *updateMessages) {
	ui.Text(c, um.CheckingTitle).FontSize(16).Bold().TextColor(p.Text)
	ui.Text(c, um.CheckingBody).FontSize(12).TextColor(p.Muted)
	ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
		if uikit.ActionCursor(ui.Button(c, um.Cancel)).Clicked() {
			u.cancelCheck()
		}
	})
}

func (u *updateController) renderAvailable(c *ui.Context, p uikit.Palette, um *updateMessages) {
	if u.candidate == nil {
		return
	}
	target := u.candidate.Release.Version.String()
	ui.Text(c, translate(um.AvailableTitle, target)).FontSize(16).Bold().TextColor(p.Text)
	ui.Column(c).Gap(2).Children(func() {
		versionRow(c, p, um.CurrentLabel, u.current)
		versionRow(c, p, um.TargetLabel, target)
	})
	if notes := u.candidate.Release.Notes; notes != "" {
		ui.Text(c, um.ReleaseNotesLabel).FontSize(11).FontWeight(600).TextColor(p.Muted)
		ui.Scroll(c).MaxHeight(180).Children(func() {
			ui.Text(c, notes).FontSize(12).TextColor(p.Text)
		})
	}
	// Secondary choices stay on their own row so four actions plus Settings
	// fit a 420-DIP sheet in both languages.
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		if uikit.ActionCursor(ui.Button(c, um.Settings)).Clicked() {
			u.openSettings()
		}
		ui.Box(c).Grow(1)
		if uikit.ActionCursor(ui.Button(c, um.SkipVersion)).Clicked() {
			u.skipVersion()
		}
		if uikit.ActionCursor(ui.Button(c, um.NeverNotify)).Clicked() {
			u.neverNotify()
		}
	})
	ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
		if uikit.ActionCursor(ui.Button(c, um.Later)).Clicked() {
			u.later()
		}
		if uikit.ActionCursor(ui.PrimaryButton(c, um.UpdateNow)).Clicked() {
			u.startDownload()
		}
	})
}

func (u *updateController) renderDownloading(c *ui.Context, p uikit.Palette, um *updateMessages) {
	target := ""
	if u.candidate != nil {
		target = u.candidate.Release.Version.String()
	}
	ui.Text(c, um.DownloadingTitle).FontSize(16).Bold().TextColor(p.Text)
	ui.Text(c, translate(um.DownloadingBody, target)).FontSize(12).TextColor(p.Muted)
	ui.Text(c, translate(um.DownloadProgress, formatBytes(u.progress), formatBytes(u.total))).
		FontSize(12).TextColor(p.Text).SingleLine()
	ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
		if uikit.ActionCursor(ui.Button(c, um.Cancel)).Clicked() {
			u.cancelCheck()
		}
	})
}

func (u *updateController) renderResult(c *ui.Context, t *ui.Theme, p uikit.Palette, um *updateMessages) {
	title, body := u.resultText(um)
	ui.Text(c, title).FontSize(16).Bold().TextColor(p.Text)
	if body != "" {
		bodyText := ui.Text(c, body).FontSize(12)
		if u.result == updateResultError {
			bodyText.TextColor(t.Danger)
		} else {
			bodyText.TextColor(p.Muted)
		}
	}
	if u.result == updateResultDownloaded {
		ui.Text(c, um.VerifiedNote).FontSize(11).TextColor(p.Faint)
	}
	ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
		if u.result == updateResultDownloaded {
			if uikit.ActionCursor(ui.Button(c, um.Settings)).Clicked() {
				u.openSettings()
			}
		}
		if uikit.ActionCursor(ui.PrimaryButton(c, um.Close)).Clicked() {
			u.closeDialog()
		}
	})
}

func (u *updateController) resultText(um *updateMessages) (title, body string) {
	switch u.result {
	case updateResultUpToDate:
		return um.UpToDateTitle, translate(um.UpToDateBody, u.current)
	case updateResultDevelopment:
		return um.DevelopmentTitle, um.DevelopmentBody
	case updateResultNoRelease:
		return um.NoReleaseTitle, um.NoReleaseBody
	case updateResultNoAsset:
		return um.NoAssetTitle, translate(um.NoAssetBody, u.detail)
	case updateResultRateLimited:
		return um.RateLimitTitle, um.RateLimitBody
	case updateResultOffline:
		return um.OfflineTitle, um.OfflineBody
	case updateResultCanceled:
		return um.CanceledTitle, um.CanceledBody
	case updateResultDownloaded:
		switch u.goos {
		case "darwin":
			return um.ReadyTitle, translate(um.ReadyMac, u.detail)
		case "windows":
			return um.ReadyTitle, translate(um.ReadyWindows, u.detail)
		default:
			return um.ReadyTitle, translate(um.ReadyOther, u.detail)
		}
	default:
		return um.ErrorTitle, translate(um.ErrorBody, u.detail)
	}
}

func versionRow(c *ui.Context, p uikit.Palette, label, value string) {
	ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
		ui.Text(c, label).Width(120).FontSize(12).TextColor(p.Muted).SingleLine()
		ui.Text(c, value).FontSize(12).TextColor(p.Text).SingleLine().Grow(1)
	})
}

// settingsUpdateContent draws the update controls inside Settings > About: the two persistent
// preferences, a manual check and the last check outcome (including a raw error for traceability).
func settingsUpdateContent(c *ui.Context, p uikit.Palette, lang language, host window, up *updateController, setStatus func(string)) {
	um := updateMessagesFor(lang)
	if up == nil {
		ui.Text(c, um.UnavailableNote).FontSize(12).TextColor(p.Muted)
		return
	}

	st := up.state.Load()
	settingsGroup(c, p, um.AutomaticChecksLabel, func() {
		ui.Column(c).Gap(8).Children(func() {
			auto := st.AutoCheckEnabled()
			if uikit.ActionCursor(ui.Checkbox(c, &auto, um.AutoCheckLabel)).Changed() {
				up.setAutoCheck(auto)
				if setStatus != nil {
					setStatus(um.PreferencesSaved)
				}
			}
			never := st.NeverNotify
			if uikit.ActionCursor(ui.Checkbox(c, &never, um.NeverNotifyLabel)).Changed() {
				up.setNeverNotify(never)
				if setStatus != nil {
					setStatus(um.PreferencesSaved)
				}
			}
			ui.Text(c, um.AutoCheckNote).FontSize(11).TextColor(p.Faint)
			ui.Text(c, um.NeverNotifyNote).FontSize(11).TextColor(p.Faint)
		})
	})

	settingsGroup(c, p, um.ManualCheckLabel, func() {
		ui.Column(c).Gap(8).Children(func() {
			if uikit.ActionCursor(ui.Button(c, um.CheckNow)).Clicked() {
				up.manualCheckFrom(host)
				if setStatus != nil {
					setStatus(um.CheckingNow)
				}
			}
			ui.Text(c, um.LastCheckLabel+": "+up.lastStatusText(lang)).FontSize(11).TextColor(p.Muted)
		})
	})
}

// formatBytes renders a byte count for the download progress line.
func formatBytes(n int64) string {
	if n < 0 {
		n = 0
	}
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit && exp < 3; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGT"[exp])
}
