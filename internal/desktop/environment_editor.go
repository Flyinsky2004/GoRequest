package desktop

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"

	"gorequest/internal/core"
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

	envID    string // identity of the environment being edited ("" for new)
	idText   string
	idLocked bool

	varRows    []*envVarRow
	secretRows []*envSecretRow

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

// build validates the draft and produces a core.Environment. Values are kept
// verbatim (leading/trailing spaces, empty strings, URLs, ':', '=', templates);
// only keys and secret names are trimmed. Duplicate variable keys and duplicate
// secret names are errors, never silent overwrites, and a value without a key is
// rejected.
func (e *environmentEditor) build(ps *projectState) (*core.Environment, error) {
	id := strings.TrimSpace(e.idText)
	if e.mode == "new" {
		if err := core.ValidateEnvironmentID(id); err != nil {
			return nil, err
		}
		if _, dup := ps.project.Environments[id]; dup {
			return nil, fmt.Errorf("environment id %q already exists", id)
		}
	} else {
		id = e.envID
	}

	vars := map[string]string{}
	for i, r := range e.varRows {
		key := strings.TrimSpace(r.Key)
		if key == "" {
			if strings.TrimSpace(r.Value) != "" {
				return nil, fmt.Errorf("row %d: value %q has no key", i+1, r.Value)
			}
			continue
		}
		if strings.ContainsAny(key, "{}") {
			return nil, fmt.Errorf("row %d: invalid variable key %q (must not contain braces)", i+1, key)
		}
		if _, dup := vars[key]; dup {
			return nil, fmt.Errorf("duplicate variable key %q", key)
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
			return nil, fmt.Errorf("secret %d: invalid name %q", i+1, n)
		}
		if seen[n] {
			return nil, fmt.Errorf("duplicate secret name %q", n)
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

// startEditEnvironment begins an edit draft for the selected environment,
// loading its actual variables (stable sort) and declared secret names.
func (ps *projectState) startEditEnvironment() error {
	id := ps.currentEnv()
	if id == "" {
		return fmt.Errorf("select an environment to edit")
	}
	env := ps.project.Environments[id]
	if env == nil {
		return fmt.Errorf("environment %q not found", id)
	}
	if _, ok := ps.project.EnvEntries[id]; !ok {
		return fmt.Errorf("environment %q has no saved file to edit", id)
	}
	e := &environmentEditor{mode: "edit", envID: id, idText: id, idLocked: true, open: true, visible: true}
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
		a.status = "open a project before creating an environment"
		return
	}
	ps.startNewEnvironment()
}

func (a *App) openEditEnvironment() {
	ps := a.ps
	if ps == nil {
		return
	}
	if !ps.canEditEnv() {
		a.status = "select an environment to edit"
		return
	}
	if err := ps.startEditEnvironment(); err != nil {
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
	env, err := e.build(ps)
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
		a.status = "created environment " + env.ID
	} else {
		if err := ps.project.SaveEnvironment(e.envID, env); err != nil {
			e.err = err.Error()
			return err
		}
		ps.envSel = e.envID // keep the current selection
		a.status = "saved environment " + e.envID
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
	title := "New Environment"
	if e.mode == "edit" {
		title = "Edit Environment: " + e.envID
	}
	ui.Column(c).Gap(12).MinWidth(480).MaxWidth(720).Children(func() {
		ui.Text(c, title).FontSize(18).Bold()
		ui.Text(c, "Project: "+ps.name()).FontSize(12).TextColor(t.TextMuted).SingleLine()

		// Environment ID.
		ui.Text(c, "Environment ID").FontSize(12).Bold().TextColor(t.TextMuted)
		if e.idLocked {
			ui.Text(c, e.idText).FontSize(13)
			ui.Text(c, "The ID is the stable reference requests use; it stays fixed when editing.").
				FontSize(11).TextColor(t.TextMuted)
		} else {
			ui.TextInput(c, &e.idText).Placeholder("e.g. staging").Changed()
			ui.Text(c, "Used as the environment identifier and file name.").
				FontSize(11).TextColor(t.TextMuted)
		}

		// Variables: a real per-row Key/Value table, not a free-form text blob.
		ui.Text(c, "Variables").FontSize(12).Bold().TextColor(t.TextMuted)
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "Key").FontSize(12).Bold().Width(200)
			ui.Text(c, "Value").FontSize(12).Bold().Grow(1)
			ui.Box(c).Width(24)
		})
		ui.Scroll(c).MaxHeight(220).FillWidth().Children(func() {
			ui.Column(c).Gap(4).FillWidth().Children(func() {
				for _, row := range e.varRows {
					e.envVarRow(c, t, ps, row)
				}
			})
		})
		if ui.Button(c, "Add variable").Clicked() {
			e.varRows = append(e.varRows, newEnvVarRow())
		}

		ui.Box(c).Height(1).Background(t.Border)

		// Secret names: declarations only, never values.
		ui.Text(c, "Secret names").FontSize(12).Bold().TextColor(t.TextMuted)
		ui.Text(c, "Declarations only (referenced as {{secret.name}}). Values stay in the local secret store and are never shown or saved here.").
			FontSize(11).TextColor(t.TextMuted)
		ui.Column(c).Gap(4).FillWidth().Children(func() {
			for _, row := range e.secretRows {
				e.envSecretRow(c, t, ps, row)
			}
		})
		if ui.Button(c, "Add secret name").Clicked() {
			e.secretRows = append(e.secretRows, &envSecretRow{ID: nextRowID()})
		}

		if e.err != "" {
			ui.Text(c, e.err).FontSize(12).TextColor(t.Danger)
		}

		if e.confirmDiscard {
			ui.Text(c, "Discard unsaved changes to this environment?").FontSize(12).TextColor(t.Danger)
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if ui.Button(c, "Keep editing").Clicked() {
					e.confirmDiscard = false
				}
				if ui.PrimaryButton(c, "Discard").Clicked() {
					e.close()
				}
			})
			return
		}
		ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
			if ui.Button(c, "Cancel").Clicked() {
				if e.dirty() {
					e.confirmDiscard = true
				} else {
					e.close()
				}
			}
			if ui.PrimaryButton(c, "Save").Clicked() {
				a.saveEnvironmentDraft(ps)
			}
		})
	})
}

func (e *environmentEditor) envVarRow(c *ui.Context, t *ui.Theme, ps *projectState, row *envVarRow) {
	ui.Row(c).Key(row.ID).Gap(8).AlignItems(ui.Center).FillWidth().Children(func() {
		ui.TextInput(c, &row.Key).Width(200).Placeholder("Key").Changed()
		ui.TextInput(c, &row.Value).Grow(1).Placeholder("Value").Changed()
		if strings.TrimSpace(row.Key) == "" && row.Value == "" {
			ui.Box(c).Width(24)
			return
		}
		if ui.Button(c, "✕").Clicked() {
			e.removeVarRow(row.ID)
		}
	})
}

func (e *environmentEditor) envSecretRow(c *ui.Context, t *ui.Theme, ps *projectState, row *envSecretRow) {
	ui.Row(c).Key(row.ID).Gap(8).AlignItems(ui.Center).FillWidth().Children(func() {
		ui.TextInput(c, &row.Name).Grow(1).Placeholder("secret name")
		if strings.TrimSpace(row.Name) == "" {
			ui.Box(c).Width(24)
			return
		}
		if ui.Button(c, "✕").Clicked() {
			e.removeSecretRow(row.ID)
		}
	})
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
