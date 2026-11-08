package desktop

import (
	"fmt"
	"runtime"
	"strings"
	"sync"
	"weak"

	"github.com/egoist/mygo/ui"

	"gorequest/internal/core"
	"gorequest/internal/desktop/uikit"
	"gorequest/internal/platform"
)

// secretEntryDialog closes the ADR-010 loop for one declared secret name:
// declaration ({{secret.<name>}} in a shared environment) -> setting the real
// value in the project-scoped OS credential store -> binding status -> use by
// requests. It deliberately holds only a *temporary* plaintext value typed by
// the user; the value is never written to a project file, never read back from
// the store for display, and is cleared as soon as the dialog is dismissed or
// the value has been handed to the credential backend. A successful write
// closes the dialog on its own (setting a value needs no extra "Cancel" click);
// a failed write keeps the dialog and the typed value so the user can retry.
//
// The dialog lives for as long as its projectState (weak key), so switching or
// closing a project can never apply one project's draft to another, exactly
// like identityMgmtFor. It reuses a.managedCredentials(ps), the same
// project-isolated ManagedCredentials store the "System credentials" page
// uses: a value set here is immediately visible there and vice versa. There is
// no plaintext file fallback; an unavailable/locked backend is an error.
type secretEntryDialog struct {
	open bool

	// name is the secret name. It is pre-filled from the row the user clicked;
	// when the row name was blank the user can type one. It is never a value.
	name string
	// nameLocked is true when opened from a declared, validated name: the user
	// is setting *that* secret, not renaming it.
	nameLocked bool
	// value is the temporary plaintext. Clear it on every exit path.
	value string

	status core.SecretStatus

	// lastAction records the last successful operation so the dialog can show
	// an accurate, non-sensitive confirmation. It is never a secret value.
	lastAction string // "created" | "updated" | "deleted" | ""
	notice     string
	errMsg     string

	// generation is bumped after every successful Set/Delete. Other surfaces
	// (the environment editor's status badges) use it to invalidate their
	// non-sensitive status cache without polling the OS store every frame or
	// ever reading a value back.
	generation uint64

	// confirmDelete gates the destructive action with the exact name.
	confirmDelete bool
}

var (
	secretEntryMu     sync.Mutex
	secretEntryByWeak = map[weak.Pointer[projectState]]*secretEntryDialog{}
)

func secretEntryFor(ps *projectState) *secretEntryDialog {
	secretEntryMu.Lock()
	defer secretEntryMu.Unlock()
	if ps == nil {
		return &secretEntryDialog{}
	}
	key := weak.Make(ps)
	if d, ok := secretEntryByWeak[key]; ok && d != nil {
		return d
	}
	d := &secretEntryDialog{}
	secretEntryByWeak[key] = d
	runtime.AddCleanup(ps, func(k weak.Pointer[projectState]) {
		secretEntryMu.Lock()
		delete(secretEntryByWeak, k)
		secretEntryMu.Unlock()
	}, key)
	return d
}

// secretEntryActive reports whether the single value-entry overlay should own
// the window this frame. Both modal owners (envEditorModal and
// identityManagementModal) yield to it so two ui.Modal overlays never stack:
// closing a secret dialog must not discard an unsaved environment draft, lose
// keyboard focus to a second backdrop, or switch the current project.
func (a *App) secretEntryActive(ps *projectState) bool {
	if a == nil || ps == nil {
		return false
	}
	d := secretEntryFor(ps)
	return d.open
}

// secretEntryMessages returns the bilingual catalog for the dialog. Keeping
// the labels in identity_management_i18n.go avoids touching i18n.go, which is
// owned by the integrator.
func (a *App) secretEntryMessages() *secretEntryMessages {
	return secretEntryMessagesFor(a.identityLangCode())
}

// --- controllers ----------------------------------------------------------

// openSecretEntry opens the dialog for a declared secret name. The name is
// pre-filled (and locked) so "set value" always targets exactly the name that
// {{secret.<name>}} resolves. A blank name (a brand-new row) stays editable.
func (a *App) openSecretEntry(ps *projectState, name string) {
	if ps == nil || ps.project == nil {
		return
	}
	d := secretEntryFor(ps)
	trimmed := strings.TrimSpace(name)
	d.open = true
	d.name = trimmed
	d.nameLocked = trimmed != ""
	d.value = ""
	d.confirmDelete = false
	d.lastAction = ""
	d.notice = ""
	d.errMsg = ""
	a.probeSecretBackend(ps)
	a.refreshSecretEntryStatus(ps, d)
}

// refreshSecretEntryStatus probes BINDING status only. It never calls Get,
// never reads a value back and never renders one. List/InspectSecret
// distinguishes missing from an unavailable/locked backend so the UI can fail
// closed with no plaintext fallback.
func (a *App) refreshSecretEntryStatus(ps *projectState, d *secretEntryDialog) {
	name := strings.TrimSpace(d.name)
	kind := a.secretBackendKind(ps)
	if name == "" {
		d.status = core.SecretStatus{Name: name, State: core.SecretStateMissing, Backend: string(kind)}
		return
	}
	mc, err := a.managedCredentials(ps)
	if err != nil {
		d.status = core.SecretStatus{
			Name: name, State: core.SecretStateUnavailable, Backend: string(kind),
			Detail: err.Error(),
		}
		return
	}
	d.status = secretStatusFromManaged(mc, name, kind)
}

// secretStatusFromManaged reuses the project-scoped ManagedCredentials port's
// existing non-sensitive List surface. It returns only a state/backend/detail
// triple; no secret value is ever read, returned or rendered.
func secretStatusFromManaged(mc *platform.ManagedCredentials, name string, kind platform.SecretBackendKind) core.SecretStatus {
	trimmed := strings.TrimSpace(name)
	if mc == nil || trimmed == "" {
		return core.SecretStatus{Name: trimmed, State: core.SecretStateMissing, Backend: string(kind)}
	}
	records, err := mc.List([]string{trimmed})
	if err != nil {
		return core.SecretStatus{
			Name: trimmed, State: core.SecretStateUnavailable, Backend: string(kind),
			Detail: err.Error(),
		}
	}
	for _, rec := range records {
		if rec.Name == trimmed {
			return core.SecretStatus{
				Name: trimmed, State: rec.State, Backend: rec.Backend, Detail: rec.Detail,
			}
		}
	}
	return core.SecretStatus{Name: trimmed, State: core.SecretStateMissing, Backend: string(kind)}
}

// closeSecretEntry clears every temporary value and returns the single overlay
// to the surface that opened it. The environment draft / management state are
// left exactly as they were: no draft is discarded, no project is switched.
func (a *App) closeSecretEntry(ps *projectState) {
	if ps == nil {
		return
	}
	d := secretEntryFor(ps)
	d.open = false
	d.value = ""
	d.confirmDelete = false
	d.errMsg = ""
	d.notice = ""
	d.lastAction = ""
}

// saveSecretEntry validates the name, then hands the value to the project-
// scoped OS credential store. A successful write clears the temporary
// plaintext, refreshes the non-sensitive binding status/caches and closes the
// dialog, so setting a value needs no extra "Cancel" click. A failed write
// keeps the dialog open with the typed value intact, so the user can correct
// the name or backend and retry without pasting the secret again; nothing is
// written and the value is still cleared when the dialog is dismissed. The
// backend error is surfaced verbatim (a locked/unsupported backend never
// degrades to a plaintext file). a.invalidateIdentityDiagnostics keeps the
// diagnostics page honest.
func (a *App) saveSecretEntry(ps *projectState) {
	if ps == nil || ps.project == nil {
		return
	}
	d := secretEntryFor(ps)
	m := a.secretEntryMessages()
	name := strings.TrimSpace(d.name)
	value := d.value
	// The typed value is kept on every failure path so it can be retried; it is
	// cleared on success below and on every dismissal path in closeSecretEntry.
	// Only the local `value` is handed to the OS credential store, and only on
	// a successful path.
	if name == "" || value == "" {
		d.errMsg = m.Empty
		return
	}
	mc, err := a.managedCredentials(ps)
	if err != nil {
		d.errMsg = fmt.Sprintf(m.BackendFailed, err.Error())
		return
	}
	if err := mc.ValidateName(name); err != nil {
		d.errMsg = fmt.Sprintf(m.InvalidName, err.Error())
		return
	}
	change, err := mc.Set(name, value)
	if err != nil {
		d.errMsg = fmt.Sprintf(m.BackendFailed, err.Error())
		d.status = secretStatusFromManaged(mc, d.name, a.secretBackendKind(ps))
		return
	}
	// Success: clear the sensitive input first, then refresh the shared
	// non-sensitive caches so the environment editor badge and the system
	// credentials page both show "Bound" on their next render.
	d.value = ""
	d.name = change.Name
	d.lastAction = change.Action
	d.notice = fmt.Sprintf(m.SavedFmt, change.Name)
	d.errMsg = ""
	d.confirmDelete = false
	d.generation++
	d.status = secretStatusFromManaged(mc, d.name, a.secretBackendKind(ps))
	a.invalidateIdentityDiagnostics(ps)
	// The system credentials page caches a keyed list; force its next render to
	// re-inspect so "Bound" appears there without a manual reload.
	if st := identityMgmtFor(ps); st != nil {
		st.credEntriesKey = ""
	}
	// Auto-close: the write succeeded, so the dialog has nothing left to do.
	// Only the overlay closes; the unsaved environment draft, the active
	// request and the surface that opened the dialog are untouched. The
	// non-sensitive confirmation moves to the status line the dialog used to
	// cover, so the close is not silent.
	d.open = false
	a.status = d.notice
}

func (a *App) requestDeleteSecretEntry(ps *projectState) {
	if ps == nil {
		return
	}
	d := secretEntryFor(ps)
	if strings.TrimSpace(d.name) == "" {
		return
	}
	d.confirmDelete = true
	d.errMsg = ""
	d.notice = ""
}

func (a *App) confirmDeleteSecretEntry(ps *projectState) {
	if ps == nil || ps.project == nil {
		return
	}
	d := secretEntryFor(ps)
	m := a.secretEntryMessages()
	name := strings.TrimSpace(d.name)
	if name == "" {
		return
	}
	mc, err := a.managedCredentials(ps)
	if err != nil {
		d.errMsg = fmt.Sprintf(m.BackendFailed, err.Error())
		return
	}
	if _, err := mc.Delete(name); err != nil {
		d.errMsg = fmt.Sprintf(m.BackendFailed, err.Error())
		return
	}
	d.confirmDelete = false
	d.value = ""
	d.lastAction = "deleted"
	d.notice = fmt.Sprintf(m.DeletedFmt, name)
	d.errMsg = ""
	d.generation++
	d.status = secretStatusFromManaged(mc, d.name, a.secretBackendKind(ps))
	a.invalidateIdentityDiagnostics(ps)
	if st := identityMgmtFor(ps); st != nil {
		st.credEntriesKey = ""
	}
}

// --- view -----------------------------------------------------------------

// secretEntryModal is the exact integration entry point, called from
// IdentityManagementModals (already wired into App.View). It renders nothing
// when the dialog is closed, so it is safe on every project window.
func (a *App) secretEntryModal(c *ui.Context, t *ui.Theme) {
	ps := a.ps
	if ps == nil || ps.project == nil {
		return
	}
	d := secretEntryFor(ps)
	if !d.open {
		return
	}
	m := a.secretEntryMessages()
	p := uikit.PaletteFor(t)

	open := true
	ui.Modal(c, &open, func() {
		ui.Column(c).Gap(12).MinWidth(460).MaxWidth(620).Children(func() {
			ui.Text(c, m.Title).FontSize(17).Bold().TextColor(p.Text)
			ui.Text(c, fmt.Sprintf(m.ProjectFmt, ps.name())).FontSize(11).TextColor(p.Muted).SingleLine()

			if strings.TrimSpace(d.name) != "" {
				ui.Text(c, fmt.Sprintf(m.RefFmt, d.name)).FontSize(12).Font("monospace").TextColor(p.Text)
			}
			ui.Text(c, m.SourceHint).FontSize(11).TextColor(p.Muted)
			ui.Text(c, m.ADRHint).FontSize(11).TextColor(p.Muted)

			ui.Box(c).Height(1).Background(t.Border)

			// Name: pre-filled and locked for a declared secret; editable only
			// when opened from an empty row.
			ui.Text(c, m.NameLabel).FontSize(12).Bold().TextColor(p.Muted)
			if d.nameLocked {
				ui.Text(c, d.name).FontSize(13).TextColor(p.Text)
			} else {
				ui.TextInput(c, &d.name).Grow(1).Placeholder(m.NamePlaceholder).AutoFocus().Changed()
			}

			ui.Text(c, m.ValueLabel).FontSize(12).Bold().TextColor(p.Muted)
			// Password input only: the value is masked on screen (Password is
			// applied every frame), never echoed as a Text label, and cleared
			// from memory on every exit path. AutoFocus puts the caret here when
			// the name is pre-filled; otherwise the name field takes focus.
			if d.nameLocked {
				ui.TextInput(c, &d.value).Grow(1).Password().Placeholder(m.ValuePlaceholder).AutoFocus().Changed()
			} else {
				ui.TextInput(c, &d.value).Grow(1).Password().Placeholder(m.ValuePlaceholder).Changed()
			}
			ui.Text(c, m.ValueHint).FontSize(11).TextColor(p.Muted)

			a.secretEntryStatus(c, t, d, m, p)
			a.secretEntryBackend(c, t, ps, m, p)

			if d.lastAction != "" && d.notice != "" {
				ui.Text(c, d.notice).FontSize(12).TextColor(t.Success)
			}
			if d.errMsg != "" {
				ui.Text(c, d.errMsg).FontSize(12).TextColor(t.Danger)
			}

			if d.confirmDelete {
				ui.Text(c, fmt.Sprintf(m.DeleteConfirmFmt, d.name)).FontSize(12).TextColor(t.Danger)
				ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
					if uikit.ActionCursor(ui.Button(c, m.Cancel)).Clicked() {
						d.confirmDelete = false
					}
					if uikit.ActionCursor(ui.Button(c, m.Delete).TextColor(t.Danger)).Clicked() {
						a.confirmDeleteSecretEntry(ps)
					}
				})
				return
			}

			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if d.status.State == core.SecretStateBound {
					if uikit.ActionCursor(ui.Button(c, m.Delete).TextColor(t.Danger)).Clicked() {
						a.requestDeleteSecretEntry(ps)
					}
				}
				if uikit.ActionCursor(ui.Button(c, m.Cancel)).Clicked() {
					a.closeSecretEntry(ps)
				}
				if uikit.ActionCursor(uikit.PrimaryButton(c, a.secretEntrySaveLabel(d, m))).Clicked() {
					a.saveSecretEntry(ps)
				}
			})
		})
	})
	if !open {
		// Escape / outside click: clear the temporary plaintext, but do NOT
		// touch the environment draft, focus ownership or the active project.
		a.closeSecretEntry(ps)
	}
}

func (a *App) secretEntrySaveLabel(d *secretEntryDialog, m *secretEntryMessages) string {
	if d.status.State == core.SecretStateBound {
		return m.UpdateValue
	}
	return m.SetValue
}

func (a *App) secretEntryStatus(c *ui.Context, t *ui.Theme, d *secretEntryDialog, m *secretEntryMessages, p uikit.Palette) {
	label, col := m.StatusMissing, t.Warning
	switch d.status.State {
	case core.SecretStateBound:
		label = fmt.Sprintf(m.StatusBoundFmt, a.secretBackendStatusLabel(d.status.Backend))
		col = t.Success
	case core.SecretStateUnavailable:
		label = fmt.Sprintf(m.StatusUnavailFmt, a.secretBackendStatusLabel(d.status.Backend))
		col = t.Danger
	}
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, m.StatusLabel).FontSize(12).Bold().TextColor(p.Muted)
		ui.Text(c, label).FontSize(12).TextColor(col)
	})
	if d.status.State == core.SecretStateUnavailable && d.status.Detail != "" {
		ui.Text(c, d.status.Detail).FontSize(11).TextColor(t.Danger)
	}
}

// secretEntryBackend shows the real storage location behind this dialog. The
// local-file backend is labelled plaintext; an unavailable backend shows its
// reason. "Change storage" closes this dialog and opens the credentials page's
// storage section, where the copy-and-verify switch is reviewed.
func (a *App) secretEntryBackend(c *ui.Context, t *ui.Theme, ps *projectState, m *secretEntryMessages, p uikit.Palette) {
	info := a.secretBackendInfo(ps)
	col := p.Muted
	if !info.Available {
		col = t.Danger
	}
	ui.Text(c, fmt.Sprintf(m.BackendFmt, a.secretBackendLabel(info.Kind))).FontSize(11).TextColor(col)
	if info.Path != "" {
		ui.Text(c, fmt.Sprintf(m.BackendPathFmt, info.Path)).FontSize(11).Font("monospace").TextColor(p.Muted).SingleLine()
	}
	if info.Plaintext {
		ui.Text(c, m.BackendPlaintext).FontSize(11).TextColor(t.Warning)
	}
	if !info.Available && info.Detail != "" {
		ui.Text(c, fmt.Sprintf(m.BackendUnavailableFmt, info.Detail)).FontSize(11).TextColor(t.Danger)
	}
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		if uikit.ActionCursor(ui.Button(c, m.ChangeStorage)).Clicked() {
			a.closeSecretEntry(ps)
			a.openIdentityManagement(nil, 2)
		}
	})
}
