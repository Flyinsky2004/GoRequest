package desktop

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"weak"

	"github.com/egoist/mygo/ui/editor"

	"gorequest/internal/core"
)

// graphql_schema.go owns the local-SDL lifecycle of a GraphQL tab: explicit
// file loading, digest checks, offline document validation and the explicit
// introspection action. Nothing here loads a remote schema or connects to a
// server without a user action.

// gqlIntrospectionPreview is a fetched introspection result shown to the user
// before anything is written. Cancel discards it; Save writes the generated SDL
// into the project and uses it as the active schema.
type gqlIntrospectionPreview struct {
	SDL   string
	Model *core.GraphQLSchemaModel
}

// gqlEnsureSchema loads a persisted project-relative SDL reference once per tab
// open. It is a local disk read only: opening a project or editing a document
// never performs network I/O.
func (a *App) gqlEnsureSchema(tab *Tab) {
	if tab == nil || tab.gql == nil || tab.gql.schemaTried {
		return
	}
	tab.gql.schemaTried = true
	ref := tab.gql.schemaRef
	if ref.Path == "" {
		return
	}
	ps := tab.Owner
	if ps == nil {
		ps = a.ps
	}
	if ps == nil || ps.project == nil {
		return
	}
	model, err := core.LoadGraphQLSchemaProjectFile(context.Background(),
		core.NewDirFileOpener(ps.project.Dir), ref.Path)
	if err != nil {
		tab.gql.schemaErr = err.Error()
		return
	}
	tab.gql.schema = model
	tab.gql.parsedDoc = ""
	if ref.Digest != "" && model.Digest != ref.Digest {
		tab.gql.schemaNotice = fmt.Sprintf(a.gqlMsg().SchemaDigestChangedFmt, ref.Digest, model.Digest)
	}
}

// gqlLoadSchemaPath loads an explicitly chosen SDL file. Files inside
// .gorequest/ are stored as project-relative references; files elsewhere stay
// local to this tab and are never written into shared project YAML.
func (a *App) gqlLoadSchemaPath(tab *Tab, absPath string) {
	if tab == nil || tab.gql == nil {
		return
	}
	st := tab.gql
	absPath = strings.TrimSpace(absPath)
	if absPath == "" {
		return
	}
	ps := tab.Owner
	if ps == nil {
		ps = a.ps
	}
	var (
		model *core.GraphQLSchemaModel
		err   error
	)
	rel := ""
	if ps != nil && ps.project != nil {
		rel = projectRelSDL(ps.project.Dir, absPath)
	}
	if rel != "" {
		model, err = core.LoadGraphQLSchemaProjectFile(context.Background(),
			core.NewDirFileOpener(ps.project.Dir), rel)
	} else {
		model, err = core.LoadGraphQLSchemaFile(absPath)
	}
	if err != nil {
		st.schemaErr = err.Error()
		st.schemaNotice = ""
		return
	}
	st.schema = model
	st.schemaErr = ""
	st.parsedDoc = ""
	st.schemaTypeSel = ""
	if rel != "" {
		st.schemaRef = core.GraphQLSchemaRef{Path: rel, Digest: model.Digest}
		st.localSDLAbs = ""
		st.schemaNotice = fmt.Sprintf(a.gqlMsg().SchemaLoadedFmt, rel)
		if tab.Entry != nil && tab.Entry.Request != nil {
			core.SetGraphQLSchemaRef(tab.Entry.Request, st.schemaRef)
			tab.Dirty = true
		}
	} else {
		st.localSDLAbs = absPath
		st.schemaRef = core.GraphQLSchemaRef{}
		st.schemaNotice = a.gqlMsg().SchemaLocalOnly
	}
}

// projectRelSDL returns a .gorequest-relative path when absPath lives inside
// the project store; otherwise "".
func projectRelSDL(projectDir, absPath string) string {
	storeDir := projectDir
	rel, err := filepath.Rel(storeDir, absPath)
	if err != nil || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return ""
	}
	return filepath.ToSlash(rel)
}

// gqlIntrospect performs the user-triggered introspection request through the
// normal tracked HTTP plan: auth, proxy, TLS, cookies, cancellation, response
// bounds and target policy are inherited. The result is only a preview until
// the user saves it.
func (a *App) gqlIntrospect(tab *Tab) {
	st := tab.ensureGQLState()
	if st == nil || st.introspecting {
		return
	}
	ps := tab.Owner
	if ps == nil {
		ps = a.ps
	}
	m := a.gqlMsg()
	if ps == nil || ps.project == nil {
		st.introspectErr = m.SchemaNoProject
		return
	}
	req, err := requestFromTab(tab)
	if err != nil {
		st.introspectErr = err.Error()
		return
	}
	if strings.TrimSpace(tab.URL) == "" {
		st.introspectErr = m.SchemaNoURL
		return
	}
	req.GraphQL = &core.GraphQLDef{Document: core.GraphQLIntrospectionDocument(), Variables: "{}"}
	req.Assert = nil

	ctx, cancel := context.WithCancel(context.Background())
	st.introspectCancel = cancel
	st.introspecting = true
	st.introspectErr = ""
	st.introspect = nil
	win := a.win
	key := weak.Make(a)
	psRef := ps
	go func() {
		plan, red, err := a.gqlBuildPlanForRequest(ctx, psRef, tab, req)
		if err == nil {
			var res *core.ExecResult
			res, err = a.executeTabPlan(ctx, tab, req, plan, red)
			if err == nil {
				sdl, model, perr := core.ParseGraphQLIntrospection(res.Body)
				if perr != nil {
					err = perr
				} else if win != nil {
					win.Update(func() {
						owner := key.Value()
						if owner == nil || owner.closed {
							return
						}
						d := tab.gql
						if d == nil || psRef.indexOfTab(tab) < 0 {
							return
						}
						d.introspecting = false
						d.introspect = &gqlIntrospectionPreview{SDL: sdl, Model: model}
					})
					return
				}
			}
		}
		cancel()
		if win == nil || win.IsDestroyed() {
			return
		}
		win.Update(func() {
			owner := key.Value()
			if owner == nil || owner.closed {
				return
			}
			d := tab.gql
			if d == nil || psRef.indexOfTab(tab) < 0 {
				return
			}
			d.introspecting = false
			if err != nil {
				d.introspectErr = red.RedactError(err).Error()
			}
		})
	}()
}

// gqlBuildPlanForRequest mirrors buildTrackedTabPlan for a request that is not
// the tab's current draft (introspection swaps in a different document). The
// public entry point is owned by the integrator, so this card keeps its own
// minimal copy instead of editing body_http_ui.go.
func (a *App) gqlBuildPlanForRequest(ctx context.Context, ps *projectState, tab *Tab, req *core.Request) (*core.Plan, *core.Redactor, error) {
	var (
		vars        map[string]string
		auth        *core.AuthScheme
		projectRoot string
	)
	if ps != nil && ps.project != nil {
		var err error
		vars, _, err = ps.project.ResolveVarsWith(tab.Entry, ps.currentEnv(), nil)
		if err != nil {
			return nil, core.NewRedactor(), err
		}
		eff, err := ps.project.ResolveEffectiveAuth(tab.Entry, req)
		if err != nil {
			return nil, core.NewRedactor(), err
		}
		if eff != nil {
			auth = eff.Scheme
		}
		projectRoot = ps.project.Dir
	}
	plan, red, err := core.BuildTrackedPlanWithOptions(ctx, req, vars, a.mgrSecrets(), auth, nil, core.PlanOptions{
		ProjectRoot:    projectRoot,
		LocalFiles:     a.localFilesFor(ps),
		MaxUploadBytes: core.DefaultMaxUploadBytes,
		AllowGraphQL:   true,
	})
	if err != nil {
		return plan, red, err
	}
	if auth != nil {
		if t := strings.ToLower(strings.TrimSpace(auth.Type)); t == "oauth2" || t == "oidc" {
			if _, err := a.ApplyTabOAuthIfConfigured(ctx, ps, tab, plan, red, true); err != nil {
				return plan, red, err
			}
		}
	}
	return plan, red, nil
}

// gqlIntrospectCancel aborts a running introspection request.
func (a *App) gqlIntrospectCancel(tab *Tab) {
	if tab == nil || tab.gql == nil {
		return
	}
	if tab.gql.introspectCancel != nil {
		tab.gql.introspectCancel()
		tab.gql.introspectCancel = nil
	}
	tab.gql.introspecting = false
}

// gqlIntrospectSave writes the previewed SDL into .gorequest/schemas/ and
// activates it. Nothing is written before this explicit confirmation.
func (a *App) gqlIntrospectSave(tab *Tab) {
	st := tab.gql
	if st == nil || st.introspect == nil {
		return
	}
	ps := tab.Owner
	if ps == nil {
		ps = a.ps
	}
	m := a.gqlMsg()
	if ps == nil || ps.project == nil || ps.project.Store == nil {
		st.introspectErr = m.SchemaNoProject
		return
	}
	rel := "schemas/introspected.graphql"
	if err := ps.project.Store.WriteAtomic(rel, []byte(st.introspect.SDL)); err != nil {
		st.introspectErr = err.Error()
		return
	}
	st.schema = st.introspect.Model
	st.schemaRef = core.GraphQLSchemaRef{Path: rel, Digest: st.introspect.Model.Digest}
	st.localSDLAbs = ""
	st.schemaErr = ""
	st.schemaNotice = fmt.Sprintf(m.SchemaSavedFmt, rel)
	st.introspect = nil
	st.parsedDoc = ""
	if tab.Entry != nil && tab.Entry.Request != nil {
		core.SetGraphQLSchemaRef(tab.Entry.Request, st.schemaRef)
		tab.Dirty = true
	}
}

// gqlInsertField inserts a schema-derived snippet at the editor caret. It uses
// the verified public editor API (State.Insert writes at the current
// selection); it is not a full schema-aware completion engine.
func (a *App) gqlInsertField(tab *Tab, snippet string) {
	st := tab.ensureGQLState()
	if st == nil || snippet == "" {
		return
	}
	if st.docEditor == nil {
		st.docEditor = &editor.State{Buffer: editor.NewBuffer(st.document)}
	}
	st.docEditor.Insert(snippet)
	st.document = st.docEditor.Buffer.String()
	st.parsedDoc = ""
	st.schemaNotice = fmt.Sprintf(a.gqlMsg().SchemaInsertedFmt, snippet)
	tab.Dirty = true
}

// filteredSchemaTypes returns the explorer types matching the search text.
func (st *gqlTabState) filteredSchemaTypes() []core.GraphQLSchemaTypeInfo {
	if st == nil || st.schema == nil {
		return nil
	}
	q := strings.ToLower(strings.TrimSpace(st.schemaSearch))
	if q == "" {
		return st.schema.Types
	}
	out := make([]core.GraphQLSchemaTypeInfo, 0, len(st.schema.Types))
	for _, typ := range st.schema.Types {
		if strings.Contains(strings.ToLower(typ.Name), q) {
			out = append(out, typ)
		}
	}
	return out
}

// selectedSchemaType returns the explorer type currently shown in the detail
// pane, defaulting to the first visible type.
func (st *gqlTabState) selectedSchemaType() *core.GraphQLSchemaTypeInfo {
	if st == nil || st.schema == nil {
		return nil
	}
	types := st.filteredSchemaTypes()
	if len(types) == 0 {
		return nil
	}
	if st.schemaTypeSel != "" {
		for i := range st.schema.Types {
			if st.schema.Types[i].Name == st.schemaTypeSel {
				return &st.schema.Types[i]
			}
		}
	}
	return &types[0]
}
