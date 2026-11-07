package desktop

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"gorequest/internal/desktop/uikit"
	"gorequest/internal/gitservice"
)

// panel renders the Git rail view. It is reached through the integration
// patch's third sidebar segment; opening a project does not call it and
// therefore does not run Git. The first frame reads status once.
func (g *gitController) panel(c *ui.Context, t *ui.Theme) {
	if g == nil {
		return
	}
	g.ensureLoaded()
	m := g.msg()
	p := uikit.PaletteFor(t)
	ui.Column(c).Grow(1).FillWidth().Children(func() {
		ui.Row(c).Padding(6, 10, 4, 10).Gap(6).AlignItems(ui.Center).Children(func() {
			ui.Text(c, m.Title).FontSize(11).FontWeight(600).TextColor(p.Muted).Grow(1)
			switch {
			case g.busy != "":
				if uikit.ActionCursor(ui.Button(c, m.Cancel)).Clicked() {
					g.cancelBusy()
				}
			case g.svc == nil:
				if uikit.ActionCursor(ui.Button(c, m.Retry)).Clicked() {
					g.retry()
				}
			default:
				if uikit.ActionCursor(ui.Button(c, m.Refresh)).Clicked() {
					g.asyncRefresh()
				}
			}
		})

		if g.svc == nil {
			g.messageBlock(c, p, m.Unavailable, m.UnavailableHint, true)
			return
		}
		if g.busy != "" {
			label := translate(m.Busy, g.busyLabel(m))
			ui.Text(c, label).FontSize(12).TextColor(p.Muted).Padding(4, 10)
		}
		if g.repoErr != "" {
			g.messageBlock(c, p, m.NotRepo, m.NotRepoHint, true)
			return
		}
		if !g.loaded || g.repo == nil || g.status == nil {
			ui.Text(c, m.Loading).FontSize(12).TextColor(p.Faint).Padding(4, 10)
			return
		}
		g.repoSummary(c, p, m)
		if g.opErr != "" {
			ui.Text(c, g.opErr).FontSize(11).TextColor(t.Danger).Padding(2, 10)
		}
		if g.note != "" {
			ui.Text(c, g.note).FontSize(11).TextColor(p.Muted).Padding(2, 10)
		}
		g.list(c, t, p, m)
	})
}

func (g *gitController) busyLabel(m *gitMessages) string {
	switch g.busy {
	case "commit":
		return m.CommitAction
	case "pull":
		return m.Pull
	case "push":
		return m.Push
	case "diff":
		return m.DiffUnstaged
	default:
		return m.Refresh
	}
}

func (g *gitController) messageBlock(c *ui.Context, p uikit.Palette, title, hint string, retry bool) {
	ui.Column(c).Padding(8, 10).Gap(4).Children(func() {
		ui.Text(c, title).FontSize(12).TextColor(p.Text)
		ui.Text(c, hint).FontSize(11).TextColor(p.Faint)
		if retry {
			if uikit.ActionCursor(ui.Button(c, g.msg().Retry)).Clicked() {
				g.retry()
			}
		}
	})
}

func (g *gitController) repoSummary(c *ui.Context, p uikit.Palette, m *gitMessages) {
	st := g.status
	ui.Column(c).Padding(2, 10, 4, 10).Gap(2).Children(func() {
		branch := st.Branch
		if branch == "" {
			branch = "HEAD"
		}
		ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
			ui.Text(c, translate(m.BranchLabel, branch)).FontSize(11).TextColor(p.Text).SingleLine()
			if st.Ahead > 0 || st.Behind > 0 {
				ui.Text(c, fmt.Sprintf("↑%d ↓%d", st.Ahead, st.Behind)).FontSize(11).TextColor(p.Faint).SingleLine()
			}
		})
		if st.Detached {
			ui.Text(c, m.Detached).FontSize(11).TextColor(p.Muted)
		}
		if st.Upstream != "" {
			ui.Text(c, translate(m.Upstream, st.Upstream)).FontSize(11).TextColor(p.Muted).SingleLine()
		}
		if g.repo != nil {
			ui.Text(c, translate(m.RepoPath, g.repo.Root)).FontSize(10).TextColor(p.Faint).SingleLine().
				Tooltip(g.repo.Root)
		}
	})

	ui.Row(c).Padding(4, 10).Gap(6).AlignItems(ui.Center).Children(func() {
		selected := len(g.selectedPaths())
		commit := ui.Button(c, fmt.Sprintf("%s (%d)", m.Commit, selected)).Disabled(selected == 0 || g.busy != "")
		if uikit.ActionCursor(commit).Clicked() {
			g.openCommit()
		}
		pull := ui.Button(c, m.Pull).Disabled(g.busy != "")
		if uikit.ActionCursor(pull).Clicked() {
			g.openPull()
		}
		push := ui.Button(c, m.Push).Disabled(g.busy != "")
		if uikit.ActionCursor(push).Clicked() {
			g.openPush()
		}
	})

	if n := st.StagedCount(); n > 0 {
		ui.Text(c, translate(m.StagedWarn, n)).FontSize(11).TextColor(p.Muted).Padding(2, 10)
	}
}

func (g *gitController) list(c *ui.Context, t *ui.Theme, p uikit.Palette, m *gitMessages) {
	entries := g.displayEntries()
	if len(entries) == 0 {
		ui.Text(c, m.Clean).FontSize(12).TextColor(p.Faint).Padding(4, 10)
		return
	}
	ui.Row(c).Padding(4, 10).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, translate(m.ChangedFiles, len(entries))).FontSize(11).TextColor(p.Muted).Grow(1)
		if len(g.selection) > 0 {
			if uikit.ActionCursor(ui.Button(c, m.ClearSelection)).Clicked() {
				g.clearSelection()
			}
		} else if uikit.ActionCursor(ui.Button(c, m.SelectAll)).Clicked() {
			g.selectAll(entries)
		}
	})
	ui.Text(c, m.SelectFilesHint).FontSize(10).TextColor(p.Faint).Padding(0, 10, 4, 10)
	shown := entries
	if len(shown) > gitMaxRows {
		shown = shown[:gitMaxRows]
	}
	ui.Scroll(c).Grow(1).FillWidth().Children(func() {
		ui.Column(c).FillWidth().Children(func() {
			for _, e := range shown {
				g.entryRow(c, t, p, m, e)
			}
			if len(entries) > len(shown) {
				ui.Text(c, translate(m.ShowFirst, len(shown))).FontSize(10).TextColor(p.Faint).Padding(4, 10)
			}
		})
	})
}

// displayEntries puts changes under this project first, so a GoRequest folder
// inside a large business repository is immediately visible. Order within each
// group stays path-sorted and stable.
func (g *gitController) displayEntries() []gitservice.Entry {
	if g == nil || g.status == nil {
		return nil
	}
	prefix := ""
	if g.repo != nil && g.app != nil && g.app.ps != nil {
		if rel, err := filepath.Rel(g.repo.Root, g.app.ps.root); err == nil {
			rel = filepath.ToSlash(rel)
			if rel != "." && !strings.HasPrefix(rel, "../") {
				prefix = strings.TrimSuffix(rel, "/") + "/"
			}
		}
	}
	if prefix == "" {
		return g.status.Entries
	}
	local := make([]gitservice.Entry, 0, len(g.status.Entries))
	other := make([]gitservice.Entry, 0, len(g.status.Entries))
	for _, e := range g.status.Entries {
		if strings.HasPrefix(e.Path, prefix) {
			local = append(local, e)
		} else {
			other = append(other, e)
		}
	}
	return append(local, other...)
}

func (g *gitController) entryRow(c *ui.Context, t *ui.Theme, p uikit.Palette, m *gitMessages, e gitservice.Entry) {
	checked := g.selection[e.Path]
	row := ui.Row(c).Height(28).FillWidth().AlignItems(ui.Center).Padding(0, 8).Gap(6).Clip()
	if row.Hovered() {
		row.Background(p.Hover)
	}
	row.Children(func() {
		uikit.ActionCursor(ui.CheckboxBase(c, &checked).Label(e.Path).Disabled(e.Unmerged))
		badge := e.Marker()
		col := p.Muted
		switch badge {
		case "A", "?":
			col = p.Accent
		case "D", "U":
			col = t.Danger
		}
		ui.Text(c, badge).Font("monospace").FontWeight(700).FontSize(11).TextColor(col).Width(14).SingleLine()
		name := uikit.ActionCursor(ui.ButtonBase(c)).Grow(1).Height(24).Padding(0, 2)
		name.Label(translate(m.DiffTitle, e.Path)).Tooltip(e.Path)
		name.Children(func() {
			ui.Text(c, e.Path).FontSize(12).TextColor(p.Text).SingleLine()
		})
		if name.Clicked() {
			g.openDiff(e.Path, e.OrigPath, false)
		}
	})
	if checked != g.selection[e.Path] {
		if g.selection == nil {
			g.selection = map[string]bool{}
		}
		g.selection[e.Path] = checked
	}
}

// modals renders the commit, diff and pull/push confirmation dialogs. The
// integration patch calls this once per frame from App.View.
func (g *gitController) modals(c *ui.Context, t *ui.Theme) {
	if g == nil {
		return
	}
	g.commitModal(c, t)
	g.diffModal(c, t)
	g.confirmModal(c, t)
}

func (g *gitController) commitModal(c *ui.Context, t *ui.Theme) {
	if !g.commitOpen {
		return
	}
	m := g.msg()
	p := uikit.PaletteFor(t)
	open := true
	ui.Modal(c, &open, func() {
		ui.Column(c).Width(560).Gap(10).Padding(16).Children(func() {
			ui.Text(c, m.CommitTitle).FontSize(16).Bold().TextColor(p.Text)
			ui.Text(c, m.CommitNote).FontSize(11).TextColor(p.Faint)
			ui.Text(c, translate(m.SelectedCount, len(g.selectedPaths()))).FontSize(12).TextColor(p.Muted)
			if g.status != nil {
				if n := g.status.StagedCount(); n > 0 {
					ui.Text(c, translate(m.StagedWarn, n)).FontSize(11).TextColor(t.Danger)
				}
			}
			ui.Text(c, m.CommitMessage).FontSize(12).TextColor(p.Text)
			if g.commitMsgEditor == nil {
				g.commitMsgEditor = &editor.State{Buffer: editor.NewBuffer("")}
			}
			g.app.syncEditorTheme(g.commitMsgEditor, &g.commitMsgEditorGen, t)
			ui.Column(c).Height(130).Radius(6).BorderWidth(1).BorderColor(p.Hairline).Clip().Children(func() {
				editor.View(c, g.commitMsgEditor).Grow(1).FillWidth()
			})
			if g.commitErr != "" {
				ui.Text(c, translate(m.CommitFailed, g.commitErr)).FontSize(11).TextColor(t.Danger)
			}
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if uikit.ActionCursor(ui.Button(c, m.Cancel)).Clicked() {
					if g.busy == "commit" {
						g.cancelBusy()
					}
					g.commitOpen = false
					open = false
				}
				commit := ui.PrimaryButton(c, m.CommitAction).
					Disabled(g.busy != "" || g.commitMessage() == "" || len(g.selectedPaths()) == 0)
				if uikit.ActionCursor(commit).Clicked() {
					g.asyncCommit()
				}
			})
		})
	})
	if !open {
		// Dismissed by Escape/outside click; the message draft is kept.
		g.commitOpen = false
	}
}

func (g *gitController) diffModal(c *ui.Context, t *ui.Theme) {
	if !g.diffOpen {
		return
	}
	m := g.msg()
	p := uikit.PaletteFor(t)
	open := true
	ui.Modal(c, &open, func() {
		ui.Column(c).Width(1000).Height(640).Gap(10).Padding(16).Children(func() {
			ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
				ui.Text(c, translate(m.DiffTitle, g.diffPath)).Bold().FontSize(15).TextColor(p.Text).Grow(1).SingleLine()
				idx := 0
				if g.diffStaged {
					idx = 1
				}
				uikit.NativeSegmented(c, &idx, m.DiffUnstaged, m.DiffStaged)
				if newStaged := idx == 1; newStaged != g.diffStaged {
					g.diffStaged = newStaged
					g.diff = nil
					g.diffState = nil
					g.asyncDiff()
				}
			})
			switch {
			case g.busy == "diff":
				ui.Text(c, m.Loading).FontSize(12).TextColor(p.Faint)
			case g.opErr != "":
				ui.Text(c, g.opErr).FontSize(12).TextColor(t.Danger)
			case g.diffErr != "":
				ui.Text(c, g.diffErr).FontSize(12).TextColor(t.Danger)
			case g.diff == nil:
				ui.Text(c, m.Loading).FontSize(12).TextColor(p.Faint)
			case g.diff.Unmerged:
				ui.Text(c, m.DiffUnmerged).FontSize(12).TextColor(t.Danger)
			case g.diff.Binary:
				ui.Text(c, m.DiffBinary).FontSize(12).TextColor(p.Muted)
			case g.diff.TooLarge:
				ui.Text(c, m.DiffTooLarge).FontSize(12).TextColor(p.Muted)
			case !g.diff.OldExists && !g.diff.NewExists:
				ui.Text(c, m.DiffMissing).FontSize(12).TextColor(p.Muted)
			case g.diffState != nil:
				g.app.syncDiffTheme(g.diffState, &g.diffStateGen, t)
				ui.Column(c).Grow(1).FillWidth().MinHeight(0).Radius(6).
					BorderWidth(1).BorderColor(p.Hairline).Clip().Children(func() {
					editor.DiffView(c, g.diffState).Grow(1).FillWidth()
				})
			default:
				ui.Text(c, m.Loading).FontSize(12).TextColor(p.Faint)
			}
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if uikit.ActionCursor(ui.Button(c, m.Close)).Clicked() {
					g.diffOpen = false
					open = false
				}
			})
		})
	})
	if !open {
		g.diffOpen = false
	}
}

func (g *gitController) confirmModal(c *ui.Context, t *ui.Theme) {
	if !g.confirmOpen {
		return
	}
	m := g.msg()
	p := uikit.PaletteFor(t)
	title := m.PullTitle
	note := m.PullFFOnly
	action := m.Pull
	if g.confirmKind == "push" {
		title = m.PushTitle
		note = m.PushNoForce
		action = m.Push
	}
	open := true
	ui.Modal(c, &open, func() {
		ui.Column(c).Width(520).Gap(10).Padding(16).Children(func() {
			ui.Text(c, title).FontSize(16).Bold().TextColor(p.Text)
			if g.confirmTarget != nil {
				ref := g.confirmTarget.Remote + "/" + g.confirmTarget.RemoteRef
				target := translate(m.PullConfirm, ref)
				if g.confirmKind == "push" {
					target = translate(m.PushConfirm, ref)
				}
				ui.Text(c, target).FontSize(13).TextColor(p.Text)
				if g.confirmTarget.URL != "" {
					ui.Text(c, g.confirmTarget.URL).FontSize(11).TextColor(p.Muted).SingleLine()
				}
			}
			ui.Text(c, note).FontSize(11).TextColor(p.Muted)
			if g.confirmErr != "" {
				ui.Text(c, g.confirmErr).FontSize(12).TextColor(t.Danger)
			}
			if g.busy != "" {
				ui.Text(c, translate(m.Busy, g.busyLabel(m))).FontSize(11).TextColor(p.Muted)
			}
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if uikit.ActionCursor(ui.Button(c, m.Cancel)).Clicked() {
					if g.busy != "" {
						g.cancelBusy()
					}
					g.confirmOpen = false
					open = false
				}
				primary := ui.PrimaryButton(c, action).
					Disabled(g.busy != "" || g.confirmErr != "" || g.confirmTarget == nil)
				if uikit.ActionCursor(primary).Clicked() {
					if g.confirmKind == "pull" {
						g.asyncPull()
					} else {
						g.asyncPush()
					}
				}
			})
		})
	})
	if !open {
		g.confirmOpen = false
	}
}
