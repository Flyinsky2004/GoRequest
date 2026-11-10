package desktop

import (
	"context"
	"strings"

	"github.com/egoist/mygo/ui/editor"

	"gorequest/internal/core"
)

// graphql.go owns the per-tab GraphQL editor state and actions. A GraphQL tab
// keeps Kind "request" so save, close, file-sync and history keep their normal
// semantics; only the workspace view and the request builder differ.
//
// Nothing here resolves templates or sends network requests itself: the tab
// builds a core.Request and the existing send path (BuildTrackedPlan ->
// ExecuteWithOptions) does the rest, so OAuth, API keys, cookies, proxy, TLS,
// target policy, redirect credential filtering, timeouts and cancellation are
// inherited rather than reimplemented.

// gqlTabState is the GraphQL workspace state of one request tab.
type gqlTabState struct {
	// section selects the upper pane: 0 document, 1 variables, 2 schema,
	// 3 headers, 4 auth.
	section int

	// Drafts survive tab switches. The editors are views over them.
	document      string
	operationName string
	variables     string

	docEditor *editor.State
	varEditor *editor.State
	docGen    int
	varGen    int

	// parsedDoc caches the last analyzed document text so a frame does not
	// reparse an unchanged document. parsed still lists operations when
	// operation selection fails, so the dropdown remains useful.
	parsedDoc string
	parsed    []core.GraphQLOperation
	selected  core.GraphQLOperation
	parseErr  string
	// diagnostics are offline schema validation errors with source locations.
	diagnostics []core.GraphQLDiagnostic

	// Local SDL schema (explicitly chosen by the user). schemaRef mirrors the
	// request extension so a project-relative reference survives save/reopen.
	schema        *core.GraphQLSchemaModel
	schemaRef     core.GraphQLSchemaRef
	schemaTried   bool
	localSDLAbs   string // absolute path used when the file is outside .gorequest
	schemaErr     string
	schemaNotice  string
	schemaSearch  string
	schemaTypeSel string

	// Explicit introspection preview (never fetched automatically).
	introspecting    bool
	introspectErr    string
	introspect       *gqlIntrospectionPreview
	introspectCancel context.CancelFunc
	introspectGen    uint64

	// sub is the live graphql-transport-ws subscription for this tab.
	sub *gqlSubscriptionState

	// resultSection selects the lower pane: 0 data, 1 errors, 2 raw, 3 headers.
	resultSection int
	notice        string
}

// ensureGQLState returns this tab's GraphQL state, lazily initializing it from
// the stored request. The lazy path keeps a tab usable even before the
// integration fill hook runs; fillGraphQLTab resets it on reload.
func (tab *Tab) ensureGQLState() *gqlTabState {
	if tab == nil {
		return nil
	}
	if tab.gql == nil {
		st := &gqlTabState{}
		if tab.Entry != nil && tab.Entry.Request != nil && tab.Entry.Request.GraphQL != nil {
			g := tab.Entry.Request.GraphQL
			st.document = g.Document
			st.operationName = g.OperationName
			st.variables = g.Variables
		}
		tab.gql = st
	}
	return tab.gql
}

// fillGraphQLTab resets a tab's GraphQL drafts from the stored request after an
// open, reload or external refresh. Non-GraphQL tabs drop any stale state.
func fillGraphQLTab(tab *Tab, req *core.Request) {
	if tab == nil {
		return
	}
	if req == nil || !strings.EqualFold(strings.TrimSpace(req.Protocol), core.ProtocolGraphQL) {
		tab.gql = nil
		return
	}
	st := tab.ensureGQLState()
	st.document = ""
	st.operationName = ""
	st.variables = ""
	if req.GraphQL != nil {
		st.document = req.GraphQL.Document
		st.operationName = req.GraphQL.OperationName
		st.variables = req.GraphQL.Variables
	}
	st.docEditor = nil
	st.varEditor = nil
	st.docGen = 0
	st.varGen = 0
	st.parsedDoc = ""
	st.parsed = nil
	st.selected = core.GraphQLOperation{}
	st.parseErr = ""
	st.diagnostics = nil
	st.schema = nil
	st.schemaTried = false
	st.localSDLAbs = ""
	st.schemaErr = ""
	st.schemaNotice = ""
	st.schemaSearch = ""
	st.schemaTypeSel = ""
	st.introspect = nil
	st.introspectErr = ""
	st.introspecting = false
	st.resultSection = 0
	st.notice = ""
	st.schemaRef = core.GraphQLSchemaRef{}
	if req != nil {
		if ref, ok := core.GraphQLSchemaRefFromRequest(req); ok {
			st.schemaRef = ref
		}
	}
	gqlTeardownTab(tab)
}

// isGraphQLTab reports whether a tab edits a protocol: graphql request.
func isGraphQLTab(tab *Tab) bool {
	return tab != nil && tab.Kind == "request" && tab.Entry != nil &&
		tab.Entry.Request != nil &&
		strings.EqualFold(strings.TrimSpace(tab.Entry.Request.Protocol), core.ProtocolGraphQL)
}

// gqlMethod returns the tab's GraphQL over HTTP method, defaulting to POST.
func (tab *Tab) gqlMethod() string {
	if tab == nil {
		return "POST"
	}
	switch strings.ToUpper(strings.TrimSpace(tab.Method)) {
	case "GET":
		return "GET"
	case "POST":
		return "POST"
	}
	if tab.Entry != nil && tab.Entry.Request != nil && tab.Entry.Request.HTTP != nil {
		if strings.EqualFold(strings.TrimSpace(tab.Entry.Request.HTTP.Method), "GET") {
			return "GET"
		}
	}
	return "POST"
}

// gqlDef builds the persistable GraphQL block from the live editors. It falls
// back to the stored block when no editor state exists, so a save never drops
// the document merely because the view has not rendered yet.
func (tab *Tab) gqlDef() *core.GraphQLDef {
	if tab == nil {
		return nil
	}
	if tab.gql == nil {
		if tab.Entry != nil && tab.Entry.Request != nil && tab.Entry.Request.GraphQL != nil {
			g := *tab.Entry.Request.GraphQL
			return &g
		}
		return nil
	}
	doc := tab.gql.document
	if tab.gql.docEditor != nil {
		doc = tab.gql.docEditor.Buffer.String()
		tab.gql.document = doc
	}
	vars := tab.gql.variables
	if tab.gql.varEditor != nil {
		vars = tab.gql.varEditor.Buffer.String()
		tab.gql.variables = vars
	}
	return &core.GraphQLDef{
		Document:      doc,
		OperationName: strings.TrimSpace(tab.gql.operationName),
		Variables:     vars,
	}
}

// createGraphQLIn creates a protocol: graphql request in folderRel and opens
// its workspace tab.
func (ps *projectState) createGraphQLIn(folderRel, name string) (*core.RequestEntry, error) {
	entry, err := ps.project.NewGraphQLRequest(collectionRelOf(folderRel), name)
	if err != nil {
		return nil, err
	}
	ps.expandChain(folderRel)
	ps.openRequest(entry)
	ps.sel = "req:" + entry.Request.ID
	return entry, nil
}

// openNewGraphQL creates a GraphQL request in the selected folder (or root).
func (a *App) openNewGraphQL() {
	ps := a.ps
	if ps == nil {
		return
	}
	entry, err := ps.createGraphQLIn(ps.selectedFolder(), a.gqlMsg().NewGraphQLDefaultName)
	if err != nil {
		a.status = err.Error()
		return
	}
	a.status = translate(a.msg().Created, entry.RelPath)
}

// gqlRefreshAnalysis reparses the document only when its text changed. When a
// local SDL schema is loaded it also validates the document fully offline and
// keeps located diagnostics for display.
func (st *gqlTabState) gqlRefreshAnalysis() {
	if st == nil {
		return
	}
	doc := st.document
	if st.docEditor != nil {
		doc = st.docEditor.Buffer.String()
	}
	if doc == st.parsedDoc {
		return
	}
	st.parsedDoc = doc
	st.parsed = nil
	st.selected = core.GraphQLOperation{}
	st.diagnostics = nil
	st.parseErr = ""
	if st.schema != nil {
		analysis, diags, err := st.schema.ValidateDocument(doc, st.operationName)
		if analysis != nil {
			st.parsed = analysis.Operations
			st.selected = analysis.Selected
		}
		st.diagnostics = diags
		if err != nil {
			st.parseErr = err.Error()
		}
		return
	}
	analysis, err := core.AnalyzeGraphQLDocument(doc, st.operationName)
	if analysis != nil {
		st.parsed = analysis.Operations
		st.selected = analysis.Selected
	}
	if err != nil {
		st.parseErr = err.Error()
	}
}

// gqlSetOperation selects one operation by name.
func (a *App) gqlSetOperation(tab *Tab, name string) {
	if tab == nil {
		return
	}
	st := tab.ensureGQLState()
	st.operationName = name
	st.parsedDoc = ""
	tab.Dirty = true
}

// gqlCancel cancels the tab's in-flight request or live subscription.
func (a *App) gqlCancel(tab *Tab) {
	if tab == nil {
		return
	}
	if tab.gql != nil && tab.gql.sub != nil && tab.gql.sub.running {
		a.gqlSubCancel(tab)
		return
	}
	if tab.cancel != nil {
		tab.cancel()
	}
}

// gqlSelectedOperation returns the operation selected in the last analysis.
func (tab *Tab) gqlSelectedOperation() core.GraphQLOperation {
	if tab == nil || tab.gql == nil {
		return core.GraphQLOperation{}
	}
	return tab.gql.selected
}

// gqlSelectedIsSubscription reports whether Send must use graphql-transport-ws
// instead of the HTTP executor.
func (tab *Tab) gqlSelectedIsSubscription() bool {
	return tab != nil && tab.Kind == "request" && isGraphQLTab(tab) &&
		tab.gqlSelectedOperation().Type == "subscription"
}

// gqlResult classifies the tab's current response.
func gqlResult(tab *Tab) core.GraphQLResult {
	if tab == nil || tab.Resp == nil {
		return core.GraphQLResult{}
	}
	return core.ClassifyGraphQLResult(tab.Resp)
}
