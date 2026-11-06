package desktop

import (
	"fmt"
	"sort"
	"strings"

	"github.com/egoist/mygo/ui"

	"gorequest/internal/core"
	"gorequest/internal/desktop/uikit"
)

// IdentityManagementView renders the normal project window plus this card's
// identity dialogs. Headless tests can drive it without modifying the public
// App.View wiring (which the integrator owns).
func (a *App) IdentityManagementView(c *ui.Context) {
	a.View(c)
	a.IdentityManagementModals(c, c.Theme())
}

// IdentityManagementModals is the exact integration entry point for the
// integrator's App.View. It renders nothing when no dialog is open.
func (a *App) IdentityManagementModals(c *ui.Context, t *ui.Theme) {
	a.identityManagementModal(c, t)
	a.productionPolicyModal(c, t)
}

// BuildIdentityManagementMenuItems adds the four management entries to an
// existing project menu. The integrator can call it from buildProjectMenu.
func (a *App) BuildIdentityManagementMenuItems(mm *ui.Menu, tab *Tab) {
	m := a.identityManagementMsg()
	if mm.Item(m.ManageSchemes).Chosen() {
		a.openIdentityManagement(tab, 0)
	}
	if mm.Item(m.ManageFolderAuth).Chosen() {
		a.openIdentityManagement(tab, 1)
	}
	if mm.Item(m.ManageCredentials).Chosen() {
		a.openIdentityManagement(tab, 2)
	}
	if mm.Item(m.ManageDiagnostics).Chosen() {
		a.openIdentityManagement(tab, 3)
	}
}

// BuildIdentityFolderMenuItem adds a "folder auth" entry to a folder context
// menu. It opens the folder section with exactly that folder selected.
func (a *App) BuildIdentityFolderMenuItem(mm *ui.Menu, folderRel string) {
	m := a.identityManagementMsg()
	if mm.Item(m.ManageFolderAuth).Chosen() {
		ps := a.ps
		if ps == nil || ps.project == nil {
			return
		}
		st := identityMgmtFor(ps)
		st.folderRel = collectionRelOf(folderRel)
		a.openIdentityManagement(nil, 1)
		// openIdentityManagement keeps an explicitly set folderRel.
		st.folderRel = collectionRelOf(folderRel)
		a.openFolderAuthEditor(ps, st, nil)
	}
}

func (a *App) identityManagementModal(c *ui.Context, t *ui.Theme) {
	ps := a.ps
	if ps == nil || ps.project == nil {
		return
	}
	st := identityMgmtFor(ps)
	if !st.open {
		return
	}
	m := a.identityManagementMsg()
	p := uikit.PaletteFor(t)
	open := true
	ui.Modal(c, &open, func() {
		ui.Column(c).Width(860).MaxHeight(700).Gap(10).Padding(18).
			Background(p.BG).Radius(12).BorderWidth(1).BorderColor(p.Hairline).Children(func() {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
				ui.Text(c, m.IdentityManageTitle).FontSize(17).FontWeight(700).TextColor(p.Text)
				ui.Box(c).Grow(1)
				if ui.Button(c, m.Close).Clicked() {
					st.open = false
				}
			})
			labels := []string{m.SchemesSection, m.FolderSection, m.CredSection, m.DiagSection}
			if _, changed := uikit.SimpleSegmented(c, p, &st.section, labels); changed {
				st.notice = ""
				st.errMsg = ""
				switch st.section {
				case 0:
					a.refreshSchemeEditor(ps, st)
				case 1:
					a.openFolderAuthEditor(ps, st, ps.activeTab())
				case 2:
					a.refreshCredentialEntries(ps, st, true)
				case 3:
					a.refreshDiagnostics(ps, st, true)
				}
			}
			ui.Box(c).Height(1).Background(t.Border)
			ui.Scroll(c).Grow(1).FillWidth().MaxHeight(520).Children(func() {
				switch st.section {
				case 0:
					a.renderSchemeSection(c, t, ps, st)
				case 1:
					a.renderFolderSection(c, t, ps, st)
				case 2:
					a.renderCredentialSection(c, t, ps, st)
				case 3:
					a.renderDiagnosticSection(c, t, ps, st)
				}
			})
			if st.notice != "" {
				ui.Text(c, st.notice).FontSize(12).TextColor(p.Muted)
			}
			if st.errMsg != "" {
				ui.Text(c, st.errMsg).FontSize(12).TextColor(t.Danger)
			}
		})
	})
	if !open {
		st.open = false
		// Never keep a credential value in a dismissed dialog's memory.
		st.credValue = ""
	}
}

// --- schemes view ----------------------------------------------------------

func (a *App) renderSchemeSection(c *ui.Context, t *ui.Theme, ps *projectState, st *identityManagementState) {
	m := a.identityManagementMsg()
	p := uikit.PaletteFor(t)
	infos := ps.project.ListAuthSchemes()

	ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, fmt.Sprintf("%s (%d)", m.SchemesSection, len(infos))).FontSize(13).FontWeight(600).TextColor(p.Text)
		ui.Box(c).Grow(1)
		if ui.Button(c, m.NewScheme).Clicked() {
			a.beginNewAuthScheme(ps, st)
		}
		if !st.isNew && st.selectedID != "" {
			if ui.Button(c, m.SchemeReload).Clicked() {
				a.refreshSchemeEditor(ps, st)
			}
		}
	})

	if len(infos) == 0 && !st.isNew {
		ui.Text(c, m.SchemeListEmpty).FontSize(12).TextColor(p.Muted)
		return
	}

	if !st.isNew {
		ui.Row(c).FillWidth().Gap(6).Children(func() {
			for _, info := range infos {
				label := info.ID + " · " + info.Type
				b := ui.Button(c, label)
				if info.ID == st.selectedID {
					b = b.TextColor(p.Accent)
				}
				if b.Clicked() {
					a.selectAuthScheme(ps, st, info.ID)
				}
			}
		})
	}

	ui.Box(c).Height(1).Background(t.Border)
	ui.Column(c).Gap(6).FillWidth().Children(func() {
		ui.Text(c, m.SchemeID).FontSize(12).FontWeight(600).TextColor(p.Muted)
		if st.isNew {
			ui.TextInput(c, &st.draft.ID).Grow(1).Placeholder("team-bearer")
		} else {
			ui.Text(c, st.draft.ID).FontSize(13)
		}
		ui.Text(c, m.SchemeIDHint).FontSize(11).TextColor(p.Muted)

		ui.Text(c, m.SchemeType).FontSize(12).FontWeight(600).TextColor(p.Muted)
		types := []string{"bearer", "basic", "apikey"}
		if ui.Select(c, &st.draft.Type, types).Changed() {
			if st.draft.Type == "apikey" && strings.TrimSpace(st.draft.In) == "" {
				st.draft.In = "header"
			}
		}
		switch st.draft.Type {
		case "basic":
			ui.Text(c, m.SchemeUsername).FontSize(12).TextColor(p.Muted)
			ui.TextInput(c, &st.draft.Username).Grow(1)
			ui.Text(c, m.SchemePassword).FontSize(12).TextColor(p.Muted)
			ui.TextInput(c, &st.draft.Password).Grow(1).Placeholder("{{secret.password}}")
		case "apikey":
			ui.Text(c, m.SchemeKeyLocation).FontSize(12).TextColor(p.Muted)
			ui.Select(c, &st.draft.In, core.APIKeyLocations)
			ui.Text(c, m.SchemeKeyName).FontSize(12).TextColor(p.Muted)
			ui.TextInput(c, &st.draft.Name).Grow(1).Placeholder("X-API-Key")
			ui.Text(c, m.SchemeKeyValue).FontSize(12).TextColor(p.Muted)
			ui.TextInput(c, &st.draft.Value).Grow(1).Placeholder("{{secret.apiKey}}")
		default:
			ui.Text(c, m.SchemeToken).FontSize(12).TextColor(p.Muted)
			ui.TextInput(c, &st.draft.Token).Grow(1).Placeholder("{{secret.token}}")
		}

		ui.Row(c).Gap(8).Children(func() {
			label := m.SchemeSave
			if st.isNew {
				label = m.SchemeCreate
			}
			if ui.PrimaryButton(c, label).Clicked() {
				a.saveSchemeDraft(ps, st)
			}
			if !st.isNew && st.selectedID != "" {
				if ui.Button(c, m.SchemeDelete).TextColor(t.Danger).Clicked() {
					a.requestDeleteAuthScheme(ps, st)
				}
			}
		})
	})

	if !st.isNew && st.selectedID != "" {
		ui.Box(c).Height(1).Background(t.Border)
		ui.Text(c, m.SchemeRefsTitle).FontSize(12).FontWeight(600).TextColor(p.Muted)
		var refs []core.IdentityReference
		for _, info := range infos {
			if info.ID == st.selectedID {
				refs = info.References
			}
		}
		if len(refs) == 0 {
			ui.Text(c, m.DiagEmpty).FontSize(11).TextColor(p.Muted)
		} else {
			ui.Text(c, m.SchemeRefsHint).FontSize(11).TextColor(p.Muted)
			for _, ref := range refs {
				ui.Text(c, fmt.Sprintf("• %s · %s", ref.Kind, ref.Path)).FontSize(11).TextColor(p.Muted).SingleLine()
			}
		}
	}

	if dlg := st.deleteDlg; dlg != nil {
		ui.Box(c).Height(1).Background(t.Border)
		ui.Text(c, fmt.Sprintf(m.SchemeInUseFmt, dlg.ID, len(dlg.References))).FontSize(12).TextColor(t.Danger)
		if len(dlg.DirtyNames) > 0 {
			ui.Text(c, fmt.Sprintf(m.SchemeDirtyBlock, strings.Join(dlg.DirtyNames, ", "))).FontSize(12).TextColor(t.Danger)
		}
		ui.Text(c, m.SchemeClearHint).FontSize(11).TextColor(p.Muted)
		ui.Row(c).Gap(8).Children(func() {
			if ui.Button(c, m.Close).Clicked() {
				st.deleteDlg = nil
			}
			if ui.Button(c, m.SchemeClearDelete).TextColor(t.Danger).Disabled(len(dlg.DirtyNames) > 0).Clicked() {
				a.confirmDeleteAuthScheme(ps, st)
			}
		})
	}
}

// --- folder view -----------------------------------------------------------

func (a *App) renderFolderSection(c *ui.Context, t *ui.Theme, ps *projectState, st *identityManagementState) {
	m := a.identityManagementMsg()
	p := uikit.PaletteFor(t)
	folders := listFolderRels(ps.project.Collections, nil)
	sort.Strings(folders)

	ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, m.FolderSection).FontSize(13).FontWeight(600).TextColor(p.Text)
		ui.Box(c).Grow(1)
		if ui.Button(c, m.SchemeReload).Clicked() {
			a.reloadFolderAuth(ps, st)
		}
	})

	if len(folders) == 0 {
		ui.Text(c, m.FolderImpactEmpty).FontSize(12).TextColor(p.Muted)
		return
	}
	if st.folderRel == "" {
		st.folderRel = folders[0]
		a.openFolderAuthEditor(ps, st, nil)
	}

	ui.Text(c, m.FolderPick).FontSize(12).FontWeight(600).TextColor(p.Muted)
	if ui.Select(c, &st.folderRel, folders).Changed() {
		a.openFolderAuthEditor(ps, st, nil)
	}

	modes := []string{
		"inherit", "none", "ref", "inline-bearer", "inline-basic", "inline-apikey",
	}
	labels := []string{
		m.FolderInherit, m.FolderNone, m.FolderRef,
		m.FolderInlineBearer, m.FolderInlineBasic, m.FolderInlineAPIKey,
	}
	idx := maxInt(0, indexOf(modes, st.folderMode))
	if _, changed := uikit.SimpleSegmented(c, p, &idx, labels); changed {
		st.folderMode = modes[idx]
		st.folderConfirm = false
		st.folderDirty = nil
	}

	ui.Column(c).Gap(6).FillWidth().Children(func() {
		switch st.folderMode {
		case "ref":
			ids := []string{}
			for _, info := range ps.project.ListAuthSchemes() {
				ids = append(ids, info.ID)
			}
			if len(ids) == 0 {
				ui.Text(c, m.SchemeListEmpty).FontSize(11).TextColor(p.Muted)
			}
			if ui.Select(c, &st.folderRef, ids).Changed() {
				st.folderConfirm = false
			}
		case "inline-bearer":
			ui.Text(c, m.SchemeToken).FontSize(12).TextColor(p.Muted)
			ui.TextInput(c, &st.folderToken).Grow(1).Placeholder("{{secret.token}}")
		case "inline-basic":
			ui.Text(c, m.SchemeUsername).FontSize(12).TextColor(p.Muted)
			ui.TextInput(c, &st.folderUser).Grow(1)
			ui.Text(c, m.SchemePassword).FontSize(12).TextColor(p.Muted)
			ui.TextInput(c, &st.folderPass).Grow(1).Placeholder("{{secret.password}}")
		case "inline-apikey":
			ui.Text(c, m.SchemeKeyLocation).FontSize(12).TextColor(p.Muted)
			ui.Select(c, &st.folderKeyIn, core.APIKeyLocations)
			ui.Text(c, m.SchemeKeyName).FontSize(12).TextColor(p.Muted)
			ui.TextInput(c, &st.folderKeyName).Grow(1)
			ui.Text(c, m.SchemeKeyValue).FontSize(12).TextColor(p.Muted)
			ui.TextInput(c, &st.folderKeyValue).Grow(1).Placeholder("{{secret.apiKey}}")
		}
	})

	ui.Box(c).Height(1).Background(t.Border)
	ui.Text(c, m.FolderImpactTitle).FontSize(12).FontWeight(600).TextColor(p.Muted)
	ui.Text(c, m.FolderImpactHint).FontSize(11).TextColor(p.Muted)
	if len(st.folderImpact) == 0 {
		ui.Text(c, m.FolderImpactEmpty).FontSize(11).TextColor(p.Muted)
	} else {
		for _, ref := range st.folderImpact {
			ui.Text(c, "• "+ref.Path).FontSize(11).TextColor(p.Muted).SingleLine()
		}
	}
	if st.folderConfirm && len(st.folderDirty) > 0 {
		ui.Text(c, fmt.Sprintf(m.FolderDirtyConfirm, strings.Join(st.folderDirty, ", "))).FontSize(12).TextColor(t.Danger)
	}
	ui.Row(c).Gap(8).Children(func() {
		if ui.PrimaryButton(c, m.FolderSave).Clicked() {
			a.requestSaveFolderAuth(ps, st, false)
		}
		if ui.Button(c, m.FolderClear).Clicked() {
			a.requestSaveFolderAuth(ps, st, true)
		}
	})
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// --- credentials view ------------------------------------------------------

func (a *App) renderCredentialSection(c *ui.Context, t *ui.Theme, ps *projectState, st *identityManagementState) {
	m := a.identityManagementMsg()
	p := uikit.PaletteFor(t)
	a.refreshCredentialEntries(ps, st, false)
	entries := st.credEntries

	ui.Text(c, m.CredDeclared).FontSize(13).FontWeight(600).TextColor(p.Text)
	ui.Text(c, m.CredSecretHint).FontSize(11).TextColor(p.Muted)
	if len(entries) == 0 {
		ui.Text(c, m.CredNoDeclared).FontSize(12).TextColor(p.Muted)
	} else {
		ui.Scroll(c).MaxHeight(220).FillWidth().Children(func() {
			ui.Column(c).Gap(4).FillWidth().Children(func() {
				for _, entry := range entries {
					a.renderCredentialRow(c, t, ps, st, entry)
				}
			})
		})
	}

	ui.Box(c).Height(1).Background(t.Border)
	ui.Column(c).Gap(6).FillWidth().Children(func() {
		ui.Text(c, m.CredName).FontSize(12).FontWeight(600).TextColor(p.Muted)
		ui.TextInput(c, &st.credName).Grow(1).Placeholder("apiToken")
		ui.Text(c, m.CredValue).FontSize(12).FontWeight(600).TextColor(p.Muted)
		ui.TextInput(c, &st.credValue).Grow(1).Password().Placeholder("{{PASTE_CREDENTIAL}}")
		ui.Text(c, m.CredValueHint).FontSize(11).TextColor(p.Muted)
		if ui.PrimaryButton(c, m.CredSave).Clicked() {
			a.saveCredential(ps, st)
		}
	})

	if st.credConfirmName != "" {
		ui.Box(c).Height(1).Background(t.Border)
		ui.Text(c, fmt.Sprintf(m.CredDeleteConfirm, st.credConfirmName)).FontSize(12).TextColor(t.Danger)
		ui.Row(c).Gap(8).Children(func() {
			if ui.Button(c, m.Close).Clicked() {
				st.credConfirmName = ""
			}
			if ui.Button(c, m.CredDelete).TextColor(t.Danger).Clicked() {
				a.confirmDeleteCredential(ps, st)
			}
		})
	}
}

func (a *App) renderCredentialRow(c *ui.Context, t *ui.Theme, ps *projectState, st *identityManagementState, entry credentialView) {
	m := a.identityManagementMsg()
	p := uikit.PaletteFor(t)
	status := m.CredStatusMissing
	col := uikit.MethodColor("DELETE", p)
	switch entry.Status.State {
	case core.SecretStateBound:
		status = fmt.Sprintf(m.CredStatusBound, entry.Status.Backend)
		col = p.Muted
	case core.SecretStateUnavailable:
		status = fmt.Sprintf(m.CredStatusUnavail, entry.Status.Backend)
	}
	ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "{{secret."+entry.Name+"}}").FontSize(12).Font("monospace").TextColor(p.Text).Width(220).SingleLine()
		ui.Text(c, status).FontSize(11).TextColor(col).Grow(1).SingleLine()
		ui.Text(c, strings.Join(entry.Sources, ", ")).FontSize(10).TextColor(p.Muted).SingleLine()
		if ui.Button(c, m.CredDelete).TextColor(t.Danger).Clicked() {
			a.requestDeleteCredential(st, entry.Name)
		}
	})
}

// --- diagnostics view ------------------------------------------------------

func (a *App) renderDiagnosticSection(c *ui.Context, t *ui.Theme, ps *projectState, st *identityManagementState) {
	m := a.identityManagementMsg()
	p := uikit.PaletteFor(t)
	a.refreshDiagnostics(ps, st, false)
	ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
		ui.Text(c, m.DiagSection).FontSize(13).FontWeight(600).TextColor(p.Text)
		ui.Box(c).Grow(1)
		if ui.Button(c, m.DiagRefresh).Clicked() {
			a.refreshDiagnostics(ps, st, true)
		}
	})
	if len(st.diagItems) == 0 {
		ui.Text(c, m.DiagEmpty).FontSize(12).TextColor(p.Muted)
		return
	}
	for _, d := range st.diagItems {
		col := uikit.MethodColor("DELETE", p)
		if d.Level == "warning" {
			col = p.Muted
		}
		ui.Text(c, fmt.Sprintf("[%s] %s · %s: %s", d.Level, d.Code, d.Path, d.Message)).
			FontSize(11).TextColor(col).SingleLine()
	}
}

// --- production confirmation ----------------------------------------------

func (a *App) productionPolicyModal(c *ui.Context, t *ui.Theme) {
	ps := a.ps
	if ps == nil || ps.project == nil {
		return
	}
	st := identityMgmtFor(ps)
	prompt := st.policy
	if prompt == nil || prompt.Confirmation == nil {
		return
	}
	m := a.identityManagementMsg()
	p := uikit.PaletteFor(t)
	open := true
	ui.Modal(c, &open, func() {
		ui.Column(c).Gap(12).MinWidth(520).Children(func() {
			ui.Text(c, m.PolicyTitle).FontSize(16).Bold().TextColor(p.Text)
			ui.Text(c, fmt.Sprintf(m.PolicyDetailFmt, prompt.RequestID, prompt.Method, prompt.URL, prompt.EnvID)).
				FontSize(12).TextColor(p.Muted)
			ui.Text(c, prompt.Reasons).FontSize(12).TextColor(t.Danger)
			ui.Text(c, fmt.Sprintf(m.PolicyScopeFmt, prompt.URL)).FontSize(11).TextColor(p.Muted)
			ui.Text(c, m.PolicyOnceHint).FontSize(11).TextColor(p.Muted)
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if ui.Button(c, m.Close).Clicked() {
					a.cancelPolicyPrompt(st)
					open = false
				}
				if ui.PrimaryButton(c, m.PolicyConfirmSend).Clicked() {
					a.acceptPolicyPrompt(ps, st)
					open = false
					// Confirm and send through the exact public integration
					// entry point. The one-shot token is consumed only when
					// the public send path calls prepareConfirmedSend; when
					// the integrator has not yet applied the patch the token
					// stays pending and no broad or unconfirmed send occurs.
					a.trySend(prompt.Tab)
				}
			})
		})
	})
	if !open {
		a.cancelPolicyPrompt(st)
	}
}
