package desktop

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"

	"gorequest/internal/desktop/uikit"

	"gorequest/internal/core"
	"gorequest/internal/platform"
)

// envVarRow is one editable environment variable row. ID is a stable UI
// identity so editing or deleting a row keeps focus, suggestions and input on
// the same logical row.
type envVarRow struct {
	ID    string
	Key   string
	Value string
}

// envSecretRow is one declared secret *name*. Environment files never carry
// secret values; only names (referenced as {{secret.name}}) are stored here.
type envSecretRow struct {
	ID   string
	Name string
}

// environmentEditor holds the in-progress create/edit draft. It lives on the
// projectState that opened it, so switching projects can never apply one
// project's draft to another, and it remembers the environment identity it was
// bound to.
type environmentEditor struct {
	open    bool // logical: the editor should be shown
	visible bool // bound to ui.Modal; the framework sets it false on dismiss
	mode    string

	baseline string // digest captured when the draft opened; never advanced by file refresh
	relPath  string
	envID    string // identity of the environment being edited ("" for new)
	idText   string
	idLocked bool

	varRows    []*envVarRow
	secretRows []*envSecretRow

	// secretStatus caches NON-SENSITIVE binding state (bound / missing /
	// unavailable) for declared names, keyed by name. It never holds or renders
	// a real secret value. FIX09 uses it to make each declared name show
	// whether a local value has been set, and to label the entry button.
	secretStatus map[string]core.SecretStatus
	// secretStatusKey invalidates the cache after a value is set/deleted in the
	// focused dialog, so the "Set value"/"Update local value" label and badge
	// refresh without reopening the environment editor.
	secretStatusKey string

	err            string
	confirmDiscard bool
	snapshot       string
}

func newEnvVarRow() *envVarRow { return &envVarRow{ID: nextRowID()} }

// signature is a stable representation of the draft used only to tell whether
// it differs from the snapshot taken when the editor opened. Blank rows are
// ignored so adding/removing the trailing empty row does not count as a change.
func (e *environmentEditor) signature() string {
	var b strings.Builder
	b.WriteString(e.idText)
	b.WriteByte(0x1f)
	for _, r := range e.varRows {
		if strings.TrimSpace(r.Key) == "" && r.Value == "" {
			continue
		}
		b.WriteString(r.Key)
		b.WriteByte(0x1e)
		b.WriteString(r.Value)
		b.WriteByte(0x1f)
	}
	for _, r := range e.secretRows {
		n := strings.TrimSpace(r.Name)
		if n == "" {
			continue
		}
		b.WriteString("S:")
		b.WriteString(n)
		b.WriteByte(0x1f)
	}
	return b.String()
}

func (e *environmentEditor) dirty() bool { return e.signature() != e.snapshot }

func (e *environmentEditor) close() {
	e.open = false
	e.visible = false
	e.err = ""
	e.confirmDiscard = false
}

// build validates the draft and produces a core.Environment. It uses the English
// catalog so pure unit tests (which call build directly) stay stable; the app
// saves through buildWith with the current language catalog.
func (e *environmentEditor) build(ps *projectState) (*core.Environment, error) {
	return e.buildWith(ps, englishMessages)
}

// buildWith validates the draft and produces a core.Environment, localizing
// validation feedback. Values are kept verbatim (leading/trailing spaces, empty
// strings, URLs, ':', '=', templates); only keys and secret names are trimmed.
// Duplicate variable keys and duplicate secret names are errors, never silent
// overwrites, and a value without a key is rejected.
func (e *environmentEditor) buildWith(ps *projectState, m *messages) (*core.Environment, error) {
	id := strings.TrimSpace(e.idText)
	if e.mode == "new" {
		if err := core.ValidateEnvironmentID(id); err != nil {
			return nil, err
		}
		if _, dup := ps.project.Environments[id]; dup {
			return nil, fmt.Errorf(m.ErrEnvDuplicateID, id)
		}
	} else {
		id = e.envID
	}

	vars := map[string]string{}
	for i, r := range e.varRows {
		key := strings.TrimSpace(r.Key)
		if key == "" {
			if strings.TrimSpace(r.Value) != "" {
				return nil, fmt.Errorf(m.ErrEnvRowNoKey, i+1, r.Value)
			}
			continue
		}
		if strings.ContainsAny(key, "{}") {
			return nil, fmt.Errorf(m.ErrEnvInvalidVarKey, i+1, key)
		}
		if _, dup := vars[key]; dup {
			return nil, fmt.Errorf(m.ErrEnvDuplicateVarKey, key)
		}
		vars[key] = r.Value
	}

	var secrets []string
	seen := map[string]bool{}
	for i, r := range e.secretRows {
		n := strings.TrimSpace(r.Name)
		if n == "" {
			continue
		}
		if strings.ContainsAny(n, "{}") {
			return nil, fmt.Errorf(m.ErrEnvInvalidSecret, i+1, n)
		}
		if seen[n] {
			return nil, fmt.Errorf(m.ErrEnvDuplicateSecret, n)
		}
		seen[n] = true
		secrets = append(secrets, n)
	}

	env := &core.Environment{SchemaVersion: core.SupportedSchemaVersion, ID: id, Variables: vars}
	if len(secrets) > 0 {
		env.Secrets = secrets
	}
	return env, nil
}

// canEditEnv reports whether an environment is selected to edit.
func (ps *projectState) canEditEnv() bool { return ps.currentEnv() != "" }

// startNewEnvironment begins a create draft bound to this project.
func (ps *projectState) startNewEnvironment() {
	e := &environmentEditor{mode: "new", open: true, visible: true}
	e.varRows = []*envVarRow{newEnvVarRow()}
	e.snapshot = e.signature()
	ps.envUI = e
}

// startEditEnvironment begins an edit draft for the selected environment. It
// uses the English catalog for direct unit tests; the app opens drafts through
// startEditEnvironmentWith with the current language.
func (ps *projectState) startEditEnvironment() error {
	return ps.startEditEnvironmentWith(englishMessages)
}

// startEditEnvironmentWith begins an edit draft, loading the selected
// environment's actual variables (stable sort) and declared secret names.
func (ps *projectState) startEditEnvironmentWith(m *messages) error {
	id := ps.currentEnv()
	if id == "" {
		return fmt.Errorf("%s", m.ErrSelectEnvToEdit)
	}
	env := ps.project.Environments[id]
	if env == nil {
		return fmt.Errorf(m.ErrEnvNotFound, id)
	}
	if _, ok := ps.project.EnvEntries[id]; !ok {
		return fmt.Errorf(m.ErrEnvNoFile, id)
	}
	e := &environmentEditor{mode: "edit", envID: id, idText: id, idLocked: true, open: true, visible: true,
		baseline: ps.project.EnvEntries[id].BaselineDigest, relPath: ps.project.EnvEntries[id].RelPath}
	for _, k := range env.SortedVariableKeys() {
		e.varRows = append(e.varRows, &envVarRow{ID: nextRowID(), Key: k, Value: env.Variables[k]})
	}
	e.varRows = append(e.varRows, newEnvVarRow()) // trailing blank affordance
	for _, s := range env.Secrets {
		e.secretRows = append(e.secretRows, &envSecretRow{ID: nextRowID(), Name: s})
	}
	e.snapshot = e.signature()
	ps.envUI = e
	return nil
}

// --- app actions ---------------------------------------------------------

func (a *App) openNewEnvironment() {
	ps := a.ps
	if ps == nil {
		a.status = a.msg().ErrOpenProjectFirst
		return
	}
	ps.startNewEnvironment()
}

func (a *App) openEditEnvironment() {
	ps := a.ps
	if ps == nil {
		return
	}
	m := a.msg()
	if !ps.canEditEnv() {
		a.status = m.ErrSelectEnvToEdit
		return
	}
	if err := ps.startEditEnvironmentWith(m); err != nil {
		a.status = err.Error()
	}
}

// saveEnvironmentDraft writes the draft through core and, on success, updates
// the project's in-memory environments and the dropdown. The selected
// environment is preserved on edit and set to the new one on create. Failures
// keep the panel open with the input and error intact.
func (a *App) saveEnvironmentDraft(ps *projectState) error {
	e := ps.envUI
	if e == nil {
		return nil
	}
	m := a.msg()
	env, err := e.buildWith(ps, m)
	if err != nil {
		e.err = err.Error()
		return err
	}
	if e.mode == "new" {
		if _, err := ps.project.NewEnvironment(env); err != nil {
			e.err = err.Error()
			return err
		}
		ps.envSel = env.ID // auto-select the freshly created environment
		a.status = translate(m.CreatedEnvironment, env.ID)
	} else {
		current := ps.project.EnvEntries[e.envID]
		if current == nil || current.RelPath != e.relPath || current.BaselineDigest != e.baseline {
			disk := ""
			if current != nil {
				disk = current.BaselineDigest
			}
			err := &core.ConflictError{Path: e.relPath, Baseline: e.baseline, Disk: disk}
			e.err = err.Error()
			return err
		}
		if err := ps.project.SaveEnvironmentPreserving(e.envID, env); err != nil {
			e.err = err.Error()
			return err
		}
		ps.envSel = e.envID // keep the current selection
		a.status = translate(m.SavedEnvironment, e.envID)
	}
	e.close()
	return nil
}

// --- view ----------------------------------------------------------------

// envEditorModal renders the environment create/edit panel for the current
// project, if one is open. Because the draft lives on the projectState, it only
// shows for the project that opened it and can never be saved into another.
func (a *App) envEditorModal(c *ui.Context, t *ui.Theme) {
	ps := a.ps
	if ps == nil || ps.envUI == nil {
		return
	}
	e := ps.envUI
	if !e.open {
		return
	}
	// FIX09: while the secret value-entry dialog is open, exactly one overlay
	// owns the window. Do not close/hide the environment modal (that would run
	// the dismissal logic below and could discard an unsaved draft); simply
	// skip rendering it for this frame. The unsaved draft, focus and current
	// project are untouched, and closing the dialog returns here unchanged.
	if a.secretEntryActive(ps) {
		return
	}
	// ui.Modal sets its bool to false on Escape or an outside click. If there
	// were edits, turn that dismissal into an explicit discard confirmation
	// rather than silently throwing the draft away.
	if !e.visible {
		if e.dirty() {
			e.confirmDiscard = true
			e.visible = true
		} else {
			e.close()
			return
		}
	}
	e.visible = true
	ui.Modal(c, &e.visible, func() {
		a.envEditorBody(c, t, ps, e)
	})
}

func (a *App) envEditorBody(c *ui.Context, t *ui.Theme, ps *projectState, e *environmentEditor) {
	m := a.msg()
	title := m.NewEnvTitle
	if e.mode == "edit" {
		title = translate(m.EditEnvTitle, e.envID)
	}
	ui.Column(c).Gap(12).MinWidth(480).MaxWidth(720).Children(func() {
		ui.Text(c, title).FontSize(18).Bold()
		ui.Text(c, translate(m.ProjectLabel, ps.name())).FontSize(12).TextColor(t.TextMuted).SingleLine()

		// Environment ID.
		ui.Text(c, m.EnvIDLabel).FontSize(12).Bold().TextColor(t.TextMuted)
		if e.idLocked {
			ui.Text(c, e.idText).FontSize(13)
			ui.Text(c, m.EnvIDLockedHint).FontSize(11).TextColor(t.TextMuted)
		} else {
			ui.TextInput(c, &e.idText).Placeholder(m.EnvIDPlaceholder).Changed()
			ui.Text(c, m.EnvIDHint).FontSize(11).TextColor(t.TextMuted)
		}

		// Variables: a real per-row Key/Value table, not a free-form text blob.
		ui.Text(c, m.VariablesLabel).FontSize(12).Bold().TextColor(t.TextMuted)
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, m.Key).FontSize(12).Bold().Width(200)
			ui.Text(c, m.Value).FontSize(12).Bold().Grow(1)
			ui.Box(c).Width(24)
		})
		ui.Scroll(c).MaxHeight(220).FillWidth().Children(func() {
			ui.Column(c).Gap(4).FillWidth().Children(func() {
				for _, row := range e.varRows {
					e.envVarRow(c, t, ps, row, m)
				}
			})
		})
		if uikit.ActionCursor(ui.Button(c, m.AddVariable)).Clicked() {
			e.varRows = append(e.varRows, newEnvVarRow())
		}

		ui.Box(c).Height(1).Background(t.Border)

		// Secret names: declarations only, never values. FIX09 adds an
		// actionable hint and a per-name entry to set the real local value,
		// so declaring a name is immediately followed by a discoverable step.
		ui.Text(c, m.SecretNamesLabel).FontSize(12).Bold().TextColor(t.TextMuted)
		ui.Text(c, m.SecretNamesHint).FontSize(11).TextColor(t.TextMuted)
		a.refreshEnvSecretStatuses(ps, e)
		ui.Column(c).Gap(4).FillWidth().Children(func() {
			for _, row := range e.secretRows {
				a.envSecretRow(c, t, ps, e, row, m)
			}
		})
		if uikit.ActionCursor(ui.Button(c, m.AddSecretName)).Clicked() {
			e.secretRows = append(e.secretRows, &envSecretRow{ID: nextRowID()})
		}
		sm := a.secretEntryMessages()
		ui.Text(c, sm.EnvHint).FontSize(11).TextColor(t.TextMuted)

		if e.err != "" {
			ui.Text(c, e.err).FontSize(12).TextColor(t.Danger)
		}

		if e.confirmDiscard {
			ui.Text(c, m.DiscardEnvConfirm).FontSize(12).TextColor(t.Danger)
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if uikit.ActionCursor(ui.Button(c, m.KeepEditing)).Clicked() {
					e.confirmDiscard = false
				}
				if uikit.ActionCursor(ui.PrimaryButton(c, m.Discard)).Clicked() {
					e.close()
				}
			})
			return
		}
		ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
			if uikit.ActionCursor(ui.Button(c, m.Cancel)).Clicked() {
				if e.dirty() {
					e.confirmDiscard = true
				} else {
					e.close()
				}
			}
			if uikit.ActionCursor(ui.PrimaryButton(c, m.Save)).Clicked() {
				a.saveEnvironmentDraft(ps)
			}
		})
	})
}

func (e *environmentEditor) envVarRow(c *ui.Context, t *ui.Theme, ps *projectState, row *envVarRow, m *messages) {
	ui.Row(c).Key(row.ID).Gap(8).AlignItems(ui.Center).FillWidth().Children(func() {
		ui.TextInput(c, &row.Key).Width(200).Placeholder(m.Key).Changed()
		ui.TextInput(c, &row.Value).Grow(1).Placeholder(m.Value).Changed()
		if strings.TrimSpace(row.Key) == "" && row.Value == "" {
			ui.Box(c).Width(24)
			return
		}
		if uikit.ActionCursor(ui.Button(c, "✕")).Clicked() {
			e.removeVarRow(row.ID)
		}
	})
}

func (a *App) envSecretRow(c *ui.Context, t *ui.Theme, ps *projectState, e *environmentEditor, row *envSecretRow, m *messages) {
	sm := a.secretEntryMessages()
	ui.Row(c).Key(row.ID).Gap(8).AlignItems(ui.Center).FillWidth().Children(func() {
		ui.TextInput(c, &row.Name).Grow(1).Placeholder(m.SecretPlaceholder).Changed()
		name := strings.TrimSpace(row.Name)
		if name == "" {
			ui.Box(c).Width(24)
			return
		}
		// Non-sensitive binding status for this declared name. Values are
		// never read back or displayed.
		st := e.secretStatus[name]
		label, col := sm.EnvStatusMissing, t.Warning
		switch st.State {
		case core.SecretStateBound:
			label, col = sm.EnvStatusBound, t.Success
		case core.SecretStateUnavailable:
			label, col = sm.EnvStatusUnavail, t.Danger
		}
		ui.Text(c, label).FontSize(11).TextColor(col).Width(84).SingleLine()
		btn := sm.EnvSetValue
		if st.State == core.SecretStateBound {
			btn = sm.EnvUpdateValue
		}
		if uikit.ActionCursor(ui.Button(c, btn)).Clicked() {
			// Pre-fill the same name; the focused dialog writes only to the
			// project-scoped OS credential store. The unsaved environment
			// draft stays open behind a single overlay.
			a.openSecretEntry(ps, name)
		}
		if uikit.ActionCursor(ui.Button(c, "✕")).Clicked() {
			e.removeSecretRow(row.ID)
		}
	})
}

// refreshEnvSecretStatuses fills a small NON-SENSITIVE status cache for the
// declared secret names shown in the editor. It uses ManagedCredentials.List,
// which returns state/backend/detail only: no secret value is ever read back,
// stored in the draft, or rendered. The cache is recomputed when the set of
// declared names changes or when the value-entry dialog reports a successful
// Set/Delete via its generation counter — never on every frame.
func (a *App) refreshEnvSecretStatuses(ps *projectState, e *environmentEditor) {
	names := make([]string, 0, len(e.secretRows))
	for _, row := range e.secretRows {
		n := strings.TrimSpace(row.Name)
		if n != "" {
			names = append(names, n)
		}
	}
	d := secretEntryFor(ps)
	key := fmt.Sprintf("%d\x1f%s", d.generation, strings.Join(names, "\x1e"))
	if e.secretStatusKey == key && e.secretStatus != nil {
		return
	}
	out := make(map[string]core.SecretStatus, len(names))
	mc, err := a.managedCredentials(ps)
	if err != nil {
		for _, n := range names {
			out[n] = core.SecretStatus{
				Name: n, State: core.SecretStateUnavailable, Backend: "keychain",
				Detail: err.Error(),
			}
		}
	} else {
		// A single List call probes binding status for all declared names at
		// once (no repeated OS keychain reads, no secret values read back).
		records, listErr := mc.List(names)
		byName := make(map[string]platform.CredentialRecord, len(records))
		for _, rec := range records {
			byName[rec.Name] = rec
		}
		for _, n := range names {
			if rec, ok := byName[n]; ok {
				out[n] = core.SecretStatus{
					Name: n, State: rec.State, Backend: rec.Backend, Detail: rec.Detail,
				}
				continue
			}
			st := core.SecretStatus{Name: n, State: core.SecretStateMissing, Backend: "keychain"}
			if listErr != nil {
				st = core.SecretStatus{
					Name: n, State: core.SecretStateUnavailable, Backend: "keychain",
					Detail: listErr.Error(),
				}
			}
			out[n] = st
		}
	}
	e.secretStatus = out
	e.secretStatusKey = key
}

func (e *environmentEditor) removeVarRow(id string) {
	kept := make([]*envVarRow, 0, len(e.varRows))
	for _, r := range e.varRows {
		if r.ID != id {
			kept = append(kept, r)
		}
	}
	e.varRows = kept
}

func (e *environmentEditor) removeSecretRow(id string) {
	kept := make([]*envSecretRow, 0, len(e.secretRows))
	for _, r := range e.secretRows {
		if r.ID != id {
			kept = append(kept, r)
		}
	}
	e.secretRows = kept
}
