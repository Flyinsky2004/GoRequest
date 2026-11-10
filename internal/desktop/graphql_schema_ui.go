package desktop

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"

	"gorequest/internal/core"
	"gorequest/internal/desktop/uikit"
)

// graphql_schema_ui.go renders the Schema tab: local SDL status, the explicit
// introspection action, a searchable type list and field/argument/deprecation
// details with API-verified insertion at the editor caret.

// gqlTrySend routes Send: a selected subscription operation starts the
// graphql-transport-ws workspace, everything else keeps the HTTP executor.
func (a *App) gqlTrySend(tab *Tab) {
	if tab == nil {
		return
	}
	if st := tab.ensureGQLState(); st != nil {
		st.gqlRefreshAnalysis()
	}
	if tab.gqlSelectedIsSubscription() {
		a.gqlSubStart(tab)
		return
	}
	// An HTTP send replaces the subscription facts, so the lower pane never
	// shows a stale transport state beside an HTTP response.
	if st := tab.gql; st != nil && st.sub != nil && !st.sub.running {
		st.sub = nil
	}
	a.trySend(tab)
}

// gqlBusy reports an in-flight HTTP request or live subscription.
func gqlBusy(tab *Tab) bool {
	if tab == nil {
		return false
	}
	if tab.Sending {
		return true
	}
	return tab.gql != nil && tab.gql.sub != nil && tab.gql.sub.running
}

func (a *App) gqlSchemaSection(c *ui.Context, t *ui.Theme, tab *Tab, st *gqlTabState, m *gqlMessages, p uikit.Palette) {
	refPath := st.schemaRef.Path
	if st.localSDLAbs != "" {
		refPath = st.localSDLAbs
	}
	ui.Column(c).Grow(1).FillWidth().Gap(8).Children(func() {
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			pathText := refPath
			if pathText == "" {
				pathText = m.SchemaPathPlaceholder
			}
			ui.Text(c, pathText).FontSize(12).Font("monospace").TextColor(p.Muted).SingleLine().Grow(1)
			if uikit.ActionCursor(ui.Button(c, m.SchemaLoad)).Clicked() {
				a.pickLocalFileDialog(func(abs string) { a.gqlLoadSchemaPath(tab, abs) })
			}
			if st.introspecting {
				if uikit.ActionCursor(ui.Button(c, m.SchemaIntrospectCancel)).Clicked() {
					a.gqlIntrospectCancel(tab)
				}
			} else if uikit.ActionCursor(ui.Button(c, m.SchemaIntrospect)).Clicked() {
				a.gqlIntrospect(tab)
			}
		})
		if st.introspecting {
			ui.Text(c, m.SchemaIntrospectRunning).FontSize(11).TextColor(p.Muted)
		}
		if st.schemaErr != "" {
			ui.Text(c, st.schemaErr).FontSize(11).TextColor(p.Danger).SingleLine()
		}
		if st.schemaNotice != "" {
			ui.Text(c, st.schemaNotice).FontSize(11).TextColor(p.Muted).SingleLine()
		}
		if st.introspectErr != "" {
			ui.Text(c, fmt.Sprintf(m.SchemaIntrospectFailedFmt, st.introspectErr)).FontSize(11).TextColor(p.Danger).SingleLine()
		}
		if st.schema == nil {
			ui.Column(c).Grow(1).Center().Gap(8).Children(func() {
				ui.Text(c, m.SchemaEmpty).FontSize(12).TextColor(p.Muted).MaxWidth(460).TextAlign(ui.Center)
				ui.Text(c, m.SchemaIntrospectHint).FontSize(11).TextColor(p.Faint).MaxWidth(460).TextAlign(ui.Center)
			})
			return
		}
		a.gqlSchemaExplorer(c, tab, st, m, p)
	})
}

// gqlSchemaExplorer renders the searchable type list next to the selected
// type's fields, arguments, enum values and deprecation facts.
func (a *App) gqlSchemaExplorer(c *ui.Context, tab *Tab, st *gqlTabState, m *gqlMessages, p uikit.Palette) {
	ui.Row(c).FillWidth().Grow(1).Gap(10).Children(func() {
		ui.Column(c).Width(230).Grow(1).FillHeight().Gap(6).Children(func() {
			ui.TextInput(c, &st.schemaSearch).Placeholder(m.SchemaSearchPlaceholder)
			types := st.filteredSchemaTypes()
			ui.Scroll(c).Grow(1).FillWidth().Children(func() {
				ui.Column(c).Gap(2).Children(func() {
					sel := st.selectedSchemaType()
					selName := ""
					if sel != nil {
						selName = sel.Name
					}
					for i := range types {
						typ := types[i]
						row := ui.Row(c).FillWidth().Padding(4, 8).Gap(6).Radius(6).AlignItems(ui.Center)
						if typ.Name == selName {
							row = row.Background(p.Sidebar)
						}
						col := p.Text
						if typ.Builtin {
							col = p.Faint
						}
						row.Children(func() {
							ui.Text(c, typ.Name).FontSize(12).Font("monospace").TextColor(col).SingleLine().Grow(1)
							ui.Text(c, typ.Kind).FontSize(10).TextColor(p.Faint).SingleLine()
						})
						if uikit.ActionCursor(row).Clicked() {
							st.schemaTypeSel = typ.Name
						}
					}
				})
			})
		})
		ui.Box(c).Width(1).FillHeight().Background(p.Hairline)
		ui.Scroll(c).Grow(1).FillHeight().Children(func() {
			sel := st.selectedSchemaType()
			if sel == nil {
				return
			}
			ui.Column(c).Gap(8).Padding(0, 4).Children(func() {
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Text(c, sel.Name).FontSize(15).Bold().TextColor(p.Text).SingleLine()
					ui.Text(c, sel.Kind).FontSize(11).TextColor(p.Muted)
					if sel.Deprecated {
						ui.Text(c, fmt.Sprintf(m.SchemaDeprecatedFmt, sel.DeprecationReason)).FontSize(11).TextColor(p.Warning)
					}
				})
				if sel.Description != "" {
					ui.Text(c, sel.Description).FontSize(11).TextColor(p.Muted)
				}
				if sel.FieldsTruncated {
					ui.Text(c, fmt.Sprintf(m.SubTruncatedFmt, 0)).FontSize(10).TextColor(p.Warning)
				}
				if len(sel.Fields) > 0 {
					ui.Text(c, m.SchemaFieldsLabel).FontSize(11).TextColor(p.Faint)
				}
				for i := range sel.Fields {
					a.gqlSchemaFieldRow(c, tab, sel.Fields[i], m, p)
					if i >= 99 {
						break
					}
				}
				if len(sel.InputFields) > 0 {
					ui.Text(c, m.SchemaArgsLabel).FontSize(11).TextColor(p.Faint)
					for _, in := range sel.InputFields {
						ui.Text(c, "  "+in.Name+": "+in.Type+schemaDefault(in.DefaultValue)).FontSize(11).
							Font("monospace").TextColor(p.Text).SingleLine()
					}
				}
				if len(sel.EnumValues) > 0 {
					ui.Text(c, m.SchemaEnumLabel).FontSize(11).TextColor(p.Faint)
					for _, ev := range sel.EnumValues {
						line := "  " + ev.Name
						if ev.Deprecated {
							line += "  (" + fmt.Sprintf(m.SchemaDeprecatedFmt, ev.DeprecationReason) + ")"
						}
						ui.Text(c, line).FontSize(11).Font("monospace").TextColor(p.Text).SingleLine()
					}
				}
			})
		})
	})
}

func (a *App) gqlSchemaFieldRow(c *ui.Context, tab *Tab, f core.GraphQLSchemaFieldInfo, m *gqlMessages, p uikit.Palette) {
	signature := f.Name
	if len(f.Args) > 0 {
		args := make([]string, 0, len(f.Args))
		for _, arg := range f.Args {
			args = append(args, arg.Name+": "+arg.Type+schemaDefault(arg.DefaultValue))
		}
		signature += "(" + strings.Join(args, ", ") + ")"
	}
	signature += ": " + f.Type
	row := ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center)
	col := p.Text
	if f.Deprecated {
		col = p.Warning
	}
	row.Children(func() {
		ui.Text(c, signature).FontSize(11).Font("monospace").TextColor(col).SingleLine().Grow(1)
		if uikit.ActionCursor(ui.Button(c, m.SchemaInsertField)).Clicked() {
			a.gqlInsertField(tab, f.Name)
		}
	})
	if f.Description != "" {
		ui.Text(c, "  "+f.Description).FontSize(10).TextColor(p.Faint).SingleLine()
	}
}

func schemaDefault(v string) string {
	if v == "" {
		return ""
	}
	return " = " + v
}

// gqlIntrospectionModal shows the fetched SDL and saves it only after the user
// confirms. Closing the modal without saving discards the preview.
func (a *App) gqlIntrospectionModal(c *ui.Context, t *ui.Theme, tab *Tab, st *gqlTabState, m *gqlMessages, p uikit.Palette) {
	if st.introspect == nil {
		return
	}
	open := true
	ui.Modal(c, &open, func() {
		ui.Column(c).Gap(10).MinWidth(620).Children(func() {
			ui.Text(c, m.SchemaIntrospectTitle).FontSize(16).Bold().TextColor(p.Text)
			ui.Text(c, m.SchemaIntrospectHint).FontSize(11).TextColor(p.Muted)
			preview := st.introspect.SDL
			if len(preview) > 32<<10 {
				preview = preview[:32<<10] + "\n…"
			}
			ui.Scroll(c).Height(320).FillWidth().Children(func() {
				ui.Text(c, preview).Font("monospace").FontSize(11).Selectable().TextColor(p.Text)
			})
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if uikit.ActionCursor(ui.Button(c, a.msg().Cancel)).Clicked() {
					st.introspect = nil
					open = false
				}
				if uikit.ActionCursor(ui.PrimaryButton(c, m.SchemaIntrospectSave)).Clicked() {
					a.gqlIntrospectSave(tab)
					if st.introspect == nil {
						open = false
					}
				}
			})
		})
	})
	if !open && st.introspect != nil {
		st.introspect = nil
	}
}
