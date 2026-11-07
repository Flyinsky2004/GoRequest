package runner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"gorequest/internal/core"
)

// Executor runs one prepared request. The default is core.ExecuteWithOptions;
// tests and the desktop can inject another implementation without changing the
// engine.
type Executor interface {
	Execute(ctx context.Context, plan *core.Plan, opts core.ExecuteOptions) (*core.ExecResult, error)
}

type execFunc func(ctx context.Context, plan *core.Plan, opts core.ExecuteOptions) (*core.ExecResult, error)

func (f execFunc) Execute(ctx context.Context, plan *core.Plan, opts core.ExecuteOptions) (*core.ExecResult, error) {
	return f(ctx, plan, opts)
}

// Options configures one run. Every field is optional except that a project and
// suite must be supplied to New.
type Options struct {
	Environment string
	Secrets     core.SecretProvider
	Executor    Executor

	// ExecuteOptions is a template copied for every attempt. The runner always
	// forces Interactive=false and Confirmed=false, sets EnvID/AuthScheme/
	// Redactor, and replaces CookieJar with a per-row jar unless cookies are
	// disabled. A production-confirmation target therefore fails before the
	// network instead of being auto-approved.
	ExecuteOptions core.ExecuteOptions
	// PlanOptions resolves project files and local file bindings.
	PlanOptions core.PlanOptions
	// PreparePlan is an optional hook (used by B04 desktop wiring) invoked after
	// a plan is built and before it is executed.
	PreparePlan func(req *core.Request, plan *core.Plan, red *core.Redactor)

	Schema SchemaValidator
	Emit   Emitter
	Clock  func() time.Time
	Seed   int64

	StepConcurrency int
	RowConcurrency  int
}

// StepPlan is the frozen, validated view of one suite step.
type StepPlan struct {
	Step        *SuiteStep
	Entry       *core.RequestEntry
	Assertions  []Assertion
	Extractions []Extraction
	Outputs     []string
}

// Runner owns one suite run. Create it with New, call Prepare (Run does so
// automatically) and then Run. A Runner is not safe for concurrent Run calls.
type Runner struct {
	project *core.Project
	suite   *Suite
	opts    Options

	steps      map[string]*StepPlan
	order      []*StepPlan
	byRel      map[string]*core.RequestEntry
	byID       map[string]*core.RequestEntry
	data       *dataset
	runID      string
	snap       string
	clock      func() time.Time
	prepareErr *preflightError

	knownSecrets []string
	red          *redactorGuard
	events       *eventBus
	prepared     bool
}

// New constructs a Runner without reading any file beyond the already loaded
// project. Validation happens in Prepare.
func New(project *core.Project, suite *Suite, opts Options) *Runner {
	if opts.Clock == nil {
		opts.Clock = timeNow
	}
	if opts.Seed == 0 {
		opts.Seed = 1
	}
	if opts.StepConcurrency <= 0 {
		opts.StepConcurrency = DefaultStepConcurrency
	}
	if opts.StepConcurrency > 64 {
		opts.StepConcurrency = 64
	}
	if opts.RowConcurrency <= 0 {
		opts.RowConcurrency = DefaultRowConcurrency
	}
	if opts.RowConcurrency > 16 {
		opts.RowConcurrency = 16
	}
	if opts.Executor == nil {
		opts.Executor = execFunc(core.ExecuteWithOptions)
	}
	return &Runner{
		project: project,
		suite:   suite,
		opts:    opts,
		steps:   map[string]*StepPlan{},
		byRel:   map[string]*core.RequestEntry{},
		byID:    map[string]*core.RequestEntry{},
		clock:   opts.Clock,
		red:     &redactorGuard{r: core.NewRedactor()},
	}
}

// timeNow is a variable so tests can freeze it without changing signatures.
var timeNow = func() time.Time { return time.Now() }

// preflightError carries the failure kind discovered before any network call.
type preflightError struct {
	kind FailureKind
	err  error
}

func (e *preflightError) Error() string { return e.err.Error() }
func (e *preflightError) Unwrap() error { return e.err }

func preflightf(kind FailureKind, format string, args ...any) error {
	return &preflightError{kind: kind, err: fmt.Errorf(format, args...)}
}

// Prepare validates the complete plan and freezes the project snapshot. Any
// error here means no request has been sent. It is safe to call Prepare more
// than once; the first error is cached.
func (r *Runner) Prepare(ctx context.Context) error {
	if r.prepared {
		if r.prepareErr != nil {
			return r.prepareErr
		}
		return nil
	}
	r.prepared = true
	r.registerBoundSecrets()
	if err := r.prepare(ctx); err != nil {
		if pe, ok := err.(*preflightError); ok {
			r.prepareErr = pe
		} else {
			r.prepareErr = &preflightError{kind: KindDefinition, err: err}
		}
		return r.prepareErr
	}
	return nil
}

func (r *Runner) prepare(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if r.project == nil {
		return preflightf(KindDefinition, "runner requires a loaded project")
	}
	if r.suite == nil {
		return preflightf(KindDefinition, "runner requires a suite")
	}
	if err := r.suite.ValidateStructure(); err != nil {
		return preflightf(KindDefinition, "%v", err)
	}
	for _, entry := range r.project.Requests {
		if entry == nil || entry.Request == nil {
			continue
		}
		rel := normalizeRel(entry.RelPath)
		r.byRel[rel] = entry
		if entry.Request.ID != "" {
			r.byID[entry.Request.ID] = entry
		}
	}

	// Resolve every step and parse its assertions/extractions.
	for i := range r.suite.Steps {
		st := &r.suite.Steps[i]
		entry := r.resolveRequest(st.Request)
		if entry == nil {
			return preflightf(KindDefinition, "step %q: request %q was not found", st.ID, st.Request)
		}
		assertions, err := parseAssertions(entry.Request.Assert)
		if err != nil {
			return preflightf(KindDefinition, "step %q request assert: %v", st.ID, err)
		}
		stepAsserts, err := parseAssertions(st.Assert)
		if err != nil {
			return preflightf(KindDefinition, "step %q assert: %v", st.ID, err)
		}
		assertions = append(assertions, stepAsserts...)
		exts, err := parseExtractions(st.Extract)
		if err != nil {
			return preflightf(KindDefinition, "step %q: %v", st.ID, err)
		}
		sp := &StepPlan{Step: st, Entry: entry, Assertions: assertions, Extractions: exts}
		for _, e := range exts {
			sp.Outputs = append(sp.Outputs, e.Name)
		}
		r.steps[st.ID] = sp
	}
	order, err := topoOrder(r.suite)
	if err != nil {
		return preflightf(KindDefinition, "%v", err)
	}
	for _, st := range order {
		r.order = append(r.order, r.steps[st.ID])
	}

	// Dataset first: its fields feed placeholder resolution.
	ds, err := loadDataset(r.project, r.suite.Dataset)
	if err != nil {
		return preflightf(KindDataset, "%v", err)
	}
	r.data = ds
	// Dataset credential fields must be masked even when never extracted. Keep
	// raw values only in the private execution rows; every step redactor knows
	// their values so assertion/event failures cannot echo them.
	for _, row := range ds.rows {
		for name, value := range row.Vars {
			if value != "" && sensitiveJSONKeyPattern.MatchString(name) {
				r.red.RegisterSecret(value)
				if !containsString(r.knownSecrets, value) {
					r.knownSecrets = append(r.knownSecrets, value)
				}
			}
		}
	}

	placeholders := map[string]core.StepOutput{}
	for _, sp := range r.order {
		for _, out := range sp.Outputs {
			placeholders[sp.Step.ID+"."+out] = core.StepOutput{Value: "preflight"}
		}
	}
	runPlaceholders := map[string]string{}
	for k := range r.suite.Variables {
		runPlaceholders[k] = "preflight"
	}
	for _, a := range r.suite.Before {
		if n := actionName(a); n != "" {
			runPlaceholders[n] = "preflight"
		}
	}
	dataPlaceholders := ds.placeholderVars()

	// Schema assertions must have an injected validator, and preflightable
	// validators must resolve their references before any request is sent.
	var schemaAssertions []*schemaExpectation
	for _, sp := range r.order {
		for _, a := range sp.Assertions {
			if a.schema != nil {
				schemaAssertions = append(schemaAssertions, a.schema)
			}
		}
	}
	if len(schemaAssertions) > 0 && r.opts.Schema == nil {
		return preflightf(KindSchema, "suite uses schema assertions but no schema validator was injected (A03 port)")
	}
	if pf, ok := r.opts.Schema.(SchemaPreflighter); ok {
		for _, ref := range schemaAssertions {
			if err := pf.PreflightSchema(SchemaRef{Ref: ref.ref, Operation: ref.operation}); err != nil {
				return preflightf(KindSchema, "schema preflight: %v", err)
			}
		}
	}

	secretNames := map[string]bool{}
	for _, sp := range r.order {
		stepVars := map[string]string{}
		for _, a := range sp.Step.Before {
			if n := actionName(a); n != "" {
				stepVars[n] = "preflight"
			}
		}
		resolveOpts := &core.ResolveOptions{
			RunVars:     runPlaceholders,
			StepVars:    stepVars,
			DataVars:    dataPlaceholders,
			StepOutputs: placeholders,
		}
		vars, _, err := r.project.ResolveVarsWith(sp.Entry, r.opts.Environment, resolveOpts)
		if err != nil {
			return preflightf(KindDefinition, "step %q: %v", sp.Step.ID, err)
		}
		eff, err := r.project.ResolveEffectiveAuth(sp.Entry, nil)
		if err != nil {
			return preflightf(KindDefinition, "step %q: %v", sp.Step.ID, err)
		}
		var auth *core.AuthScheme
		if eff != nil {
			auth = eff.Scheme
		}
		if auth != nil && (strings.EqualFold(auth.Type, "oauth2") || strings.EqualFold(auth.Type, "oidc")) {
			return preflightf(KindDefinition, "runner OAuth/OIDC token integration is unavailable; no requests sent")
		}
		strs := requestTemplateStrings(sp.Entry.Request, auth)
		for _, a := range sp.Assertions {
			strs = append(strs, assertionTemplateStrings(a)...)
		}
		for _, s := range strs {
			for _, name := range core.TemplateNames(s) {
				if strings.HasPrefix(name, "secret.") {
					secretNames[strings.TrimSpace(strings.TrimPrefix(name, "secret."))] = true
				}
			}
			if undef := core.UndefinedTemplatesWithContext(s, vars, r.opts.Secrets, resolveOpts); len(undef) > 0 {
				kind := KindTemplate
				for _, u := range undef {
					if strings.HasPrefix(u, "secret.") {
						kind = KindMissingSecret
					}
				}
				return preflightf(kind, "step %q: undefined template(s) %s", sp.Step.ID, templateList(undef))
			}
		}
		// Step-output references must be declared in the dependency closure,
		// otherwise concurrent steps could read a value that is not published
		// yet. This turns a race into a preflight error.
		if err := r.checkStepDependencies(sp, strs); err != nil {
			return err
		}
		if err := r.checkRetrySemantics(sp); err != nil {
			return err
		}
		for k, v := range vars {
			_ = k
			for _, name := range core.TemplateNames(v) {
				if strings.HasPrefix(name, "secret.") {
					secretNames[strings.TrimSpace(strings.TrimPrefix(name, "secret."))] = true
				}
			}
		}
		// Dry-build with placeholders: catches bad URLs, missing attachments,
		// unbound secrets, auth errors and body encoding problems before any row
		// runs.
		plan, _, buildErr := core.BuildTrackedPlanWithOptions(ctx, sp.Entry.Request, vars, r.opts.Secrets,
			auth, resolveOpts, r.opts.PlanOptions)
		if err := buildErr; err != nil {
			kind := KindDefinition
			if core.IsConfig(err) {
				kind = KindDefinition
				if strings.Contains(strings.ToLower(err.Error()), "secret") {
					kind = KindMissingSecret
				}
			}
			return preflightf(kind, "step %q: %v", sp.Step.ID, err)
		}
		// A suite must not send earlier steps before discovering a static target
		// requires confirmation. Real row/output URLs are checked again by Core.
		if r.opts.ExecuteOptions.HasTargetPolicy {
			hasCredentials := auth != nil && !strings.EqualFold(auth.Type, "none")
			for _, h := range plan.Headers {
				if strings.EqualFold(h[0], "Authorization") || strings.EqualFold(h[0], "Cookie") || strings.EqualFold(h[0], "Proxy-Authorization") {
					hasCredentials = true
				}
			}
			if _, err := core.EvaluateTarget(r.opts.ExecuteOptions.EffectiveTargetPolicy(), core.TargetCheckInput{
				URL: plan.URL, Method: plan.Method, EnvID: r.opts.Environment, HasCredentials: hasCredentials,
				Interactive: false, Confirmed: false,
			}); err != nil {
				return preflightf(KindTargetPolicy, "target policy preflight: %v", err)
			}
		}
	}
	if env := r.envEntry(); env != nil {
		for _, name := range env.Environment.Secrets {
			if n := strings.TrimSpace(name); n != "" {
				secretNames[n] = true
			}
		}
	}
	for name := range secretNames {
		if name == "" || r.opts.Secrets == nil {
			continue
		}
		if v, ok := r.opts.Secrets.Get(name); ok && v != "" {
			r.knownSecrets = append(r.knownSecrets, v)
			r.red.RegisterSecret(v)
		}
	}
	r.registerBoundSecrets()
	r.snap = r.snapshotDigest()
	r.runID = newRunID(r.clock())
	r.events = newEventBus(r.runID, r.clock, r.opts.Emit)
	return nil
}

func (r *Runner) resolveRequest(ref string) *core.RequestEntry {
	ref = strings.TrimSpace(ref)
	if e, ok := r.byRel[normalizeRel(ref)]; ok {
		return e
	}
	if e, ok := r.byID[ref]; ok {
		return e
	}
	return nil
}

func normalizeRel(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\\", "/"))
	return strings.TrimPrefix(s, "./")
}

func (r *Runner) envEntry() *core.EnvironmentEntry {
	if r.opts.Environment == "" || r.project == nil {
		return nil
	}
	return r.project.EnvEntries[r.opts.Environment]
}

func actionName(a Action) string {
	if a.Set != nil {
		return a.Set.Name
	}
	if a.Generate != nil {
		return a.Generate.Name
	}
	return ""
}

// checkStepDependencies verifies that every {{steps.<id>.<out>}} reference
// names a declared output of an upstream step.
func (r *Runner) checkStepDependencies(sp *StepPlan, strs []string) error {
	closure := r.depClosure(sp.Step.ID)
	for _, s := range strs {
		for _, name := range core.TemplateNames(s) {
			rest, ok := strings.CutPrefix(name, "steps.")
			if !ok {
				continue
			}
			stepID, out, hasDot := strings.Cut(rest, ".")
			if !hasDot {
				return preflightf(KindTemplate, "step %q: invalid step output reference {{%s}}", sp.Step.ID, name)
			}
			src, ok := r.steps[stepID]
			if !ok {
				return preflightf(KindTemplate, "step %q: references unknown step %q", sp.Step.ID, stepID)
			}
			if !containsString(src.Outputs, out) {
				return preflightf(KindTemplate, "step %q: step %q does not declare output %q", sp.Step.ID, stepID, out)
			}
			if !closure[stepID] {
				return preflightf(KindDefinition, "step %q: must declare needs on step %q before reading its output", sp.Step.ID, stepID)
			}
		}
	}
	return nil
}

// depClosure returns all transitive dependencies of stepID (excluding itself).
func (r *Runner) depClosure(stepID string) map[string]bool {
	out := map[string]bool{}
	var visit func(string)
	visit = func(id string) {
		sp := r.steps[id]
		if sp == nil {
			return
		}
		for _, dep := range sp.Step.Needs {
			if !out[dep] {
				out[dep] = true
				visit(dep)
			}
		}
	}
	visit(stepID)
	return out
}

func (r *Runner) checkRetrySemantics(sp *StepPlan) error {
	p := sp.Step.Retry
	if p == nil || p.MaxAttempts <= 1 {
		return nil
	}
	method := ""
	if sp.Entry != nil && sp.Entry.Request != nil && sp.Entry.Request.HTTP != nil {
		method = strings.ToUpper(strings.TrimSpace(sp.Entry.Request.HTTP.Method))
	}
	if method == "" {
		method = "GET"
	}
	if (method == "POST" || method == "PATCH") && !p.AllowNonIdempotent {
		return preflightf(KindDefinition,
			"step %q: retry on non-idempotent %s requires allowNonIdempotent: true", sp.Step.ID, method)
	}
	return nil
}

func (r *Runner) snapshotDigest() string {
	h := sha256.New()
	suiteDigest := r.suite.sourceDigest
	if suiteDigest == "" {
		if b, err := yaml.Marshal(r.suite); err == nil {
			suiteDigest = digestBytes(b)
		} else if b, err := json.Marshal(r.suite); err == nil {
			suiteDigest = digestBytes(b)
		}
	}
	fmt.Fprintf(h, "suite:%s\n", suiteDigest)
	if r.project.File != nil {
		fmt.Fprintf(h, "project:%s:%d:%d\n", r.project.File.ID, r.project.File.SchemaVersion, r.suite.SchemaVersion)
	}
	rels := make([]string, 0, len(r.byRel))
	for rel := range r.byRel {
		rels = append(rels, rel)
	}
	sort.Strings(rels)
	for _, rel := range rels {
		e := r.byRel[rel]
		fmt.Fprintf(h, "request:%s:%s\n", rel, e.BaselineDigest)
	}
	if schema, ok := r.opts.Schema.(interface{ SchemaSnapshotDigest() string }); ok {
		fmt.Fprintf(h, "schemas:%s\n", schema.SchemaSnapshotDigest())
	}
	fmt.Fprintf(h, "environment:%s\n", r.opts.Environment)
	if env := r.envEntry(); env != nil {
		fmt.Fprintf(h, "environmentDigest:%s:%s\n", env.RelPath, env.BaselineDigest)
	}
	if r.data != nil {
		fmt.Fprintf(h, "dataset:%s\n", r.data.digest)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func newRunID(now time.Time) string {
	// Run IDs are diagnostic only; they are not security material.
	raw := fmt.Sprintf("%d-%v", now.UTC().UnixNano(), now.UTC().Unix())
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:8])
}

func templateList(names []string) string {
	parts := make([]string, len(names))
	for i, n := range names {
		parts[i] = "{{" + n + "}}"
	}
	return strings.Join(parts, ", ")
}

func requestTemplateStrings(req *core.Request, auth *core.AuthScheme) []string {
	if req == nil {
		return nil
	}
	var out []string
	if req.HTTP != nil {
		out = append(out, req.HTTP.URL)
		for _, kv := range req.HTTP.Query {
			if kv.IsEnabled() {
				out = append(out, kv.Name, kv.Value)
			}
		}
		for _, kv := range req.HTTP.Headers {
			if kv.IsEnabled() {
				out = append(out, kv.Name, kv.Value)
			}
		}
		if req.HTTP.Body != nil {
			out = appendNodeStrings(out, req.HTTP.Body.Value)
			out = append(out, req.HTTP.Body.File)
		}
	}
	out = append(out, req.Timeout)
	for _, v := range req.Variables {
		out = append(out, v)
	}
	if auth != nil {
		out = append(out, auth.Token, auth.Username, auth.Password, auth.Name, auth.Value)
	}
	return out
}

func appendNodeStrings(out []string, n *yaml.Node) []string {
	if n == nil {
		return out
	}
	if n.Kind == yaml.ScalarNode {
		out = append(out, n.Value)
	}
	for _, c := range n.Content {
		out = appendNodeStrings(out, c)
	}
	return out
}

func assertionTemplateStrings(a Assertion) []string {
	var out []string
	if a.header != nil {
		out = append(out, matcherStrings(a.header.matcher)...)
	}
	if a.jsonPath != nil {
		out = append(out, matcherStrings(a.jsonPath.matcher)...)
	}
	if a.body != nil {
		out = append(out, matcherStrings(a.body.matcher)...)
	}
	return out
}

func matcherStrings(m Matcher) []string {
	var out []string
	if m.Equals != nil {
		if s, ok := m.Equals.v.(string); ok {
			out = append(out, s)
		}
	}
	if m.NotEquals != nil {
		if s, ok := m.NotEquals.v.(string); ok {
			out = append(out, s)
		}
	}
	for _, l := range m.In {
		if s, ok := l.v.(string); ok {
			out = append(out, s)
		}
	}
	if m.Contains != nil {
		out = append(out, *m.Contains)
	}
	if m.NotContains != nil {
		out = append(out, *m.NotContains)
	}
	return out
}

func digestBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
