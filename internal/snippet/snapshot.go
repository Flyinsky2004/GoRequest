package snippet

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"gorequest/internal/core"
)

// segment is one resolvable piece of a field value.
//
//   - segLiteral:    fully resolved definition/environment snapshot text.
//   - segSecret:     needs a runtime environment lookup; text is the derived
//     variable name (GOREQ_SECRET_<NAME> / GOREQ_<FIELD>). The secret value
//     is never resolved or embedded by the generator.
//   - segUnresolved: {{data.*}} / {{steps.*}}, which only exist inside a
//     dataset/suite run, or the visible "{{secret.<name>}}" keep-alive used
//     inside JSON text (no concatenation is possible there). The raw text
//     stays visually in the code with a warning note instead of any
//     fabricated value.
type segment struct {
	kind segmentKind
	text string
}

type segmentKind int

const (
	segLiteral segmentKind = iota
	segSecret
	segUnresolved
)

func literal(segs []segment) string {
	var b strings.Builder
	for _, s := range segs {
		b.WriteString(s.text)
	}
	return b.String()
}

func hasSecret(segs []segment) bool {
	for _, s := range segs {
		if s.kind == segSecret {
			return true
		}
	}
	return false
}

func hasUnresolved(segs []segment) bool {
	for _, s := range segs {
		if s.kind == segUnresolved {
			return true
		}
	}
	return false
}

// pair is a plan-ordered header (or cookie) pair. Value is segmented so
// language renderers can compose runtime lookups.
type pair struct {
	Name  string
	Value []segment
}

// authCarrier is how bearer/basic/OAuth credentials travel. API-key auth is
// folded into headers/query/cookie during the snapshot (core.applyAuth
// semantics), so it has no carrier here.
type authCarrier string

const (
	authNone   authCarrier = "none"
	authBearer authCarrier = "bearer"
	authBasic  authCarrier = "basic"
	authOAuth  authCarrier = "oauth"
)

// authSnapshot carries credentials languages must compose at runtime
// ("Bearer " prefix, base64 for basic auth, token placeholder for OAuth).
type authSnapshot struct {
	Carrier authCarrier
	Token   []segment // bearer / oauth
	User    []segment // basic
	Pass    []segment // basic
	RefID   string    // reusable-scheme provenance for review
	Source  string    // project | folder | request | none
}

// bodyKind mirrors the canonical core body kinds.
type bodyKind string

const (
	bodyNone       bodyKind = "none"
	bodyJSON       bodyKind = "json"
	bodyText       bodyKind = "text"
	bodyXML        bodyKind = "xml"
	bodyURLEncoded bodyKind = "urlencoded"
	bodyMultipart  bodyKind = "form-data"
	bodyBinary     bodyKind = "binary"
)

// fileRefSnap is a file input to the generated code. Machine paths never
// enter shared definitions, so the snippet emits an explicit
// user-configured path constant plus a note naming the project-relative path
// or the machine-local slot.
type fileRefSnap struct {
	IsLocal   bool
	Path      string // .gorequest-relative path (project refs)
	Slot      string // machine-local binding slot name (local refs)
	Suggested string // review hint (e.g. files/data.bin / local:avatar)
}

// formFieldSnap is one urlencoded field kept for runtime encoding.
type formFieldSnap struct {
	Name  string
	Value []segment
}

// multipartPartSnap is one multipart part.
type multipartPartSnap struct {
	Name        string
	IsFile      bool
	Text        []segment
	File        fileRefSnap
	FileName    string
	ContentType string
}

// bodySnapshot is the resolved body view.
type bodySnapshot struct {
	Kind        bodyKind
	HasBody     bool
	JSONText    string // rendered JSON with visible {{secret.*}} text placeholders
	Text        []segment
	Fields      []formFieldSnap // runtime-encoded urlencoded fields
	Lineup      string          // pre-built literal body (mixed Encoded fields)
	HasLineup   bool
	Parts       []multipartPartSnap
	File        fileRefSnap
	HasFile     bool
	ContentType string // explicit binary contentType
}

// snapshot is the definition+environment object rendered by language
// templates. It follows core.BuildPlanWithOptions + core.BuildBody semantics
// except where a Note says otherwise.
type snapshot struct {
	Name        string
	ID          string
	Method      string
	URL         []segment
	Headers     []pair // complete plan headers; Content-Type (if any) is last
	Auth        authSnapshot
	Timeout     time.Duration
	Body        bodySnapshot
	requiredEnv map[string]string
	authQuery   []pair // api-key-in-query folding (post user query, plan order)
}

// blockErr is a semantic refusal: the snapshot cannot be built honestly.
type blockErr struct {
	code string
	Text string
}

// builder carries resolution state through one snapshot build.
type builder struct {
	vars        map[string]string
	notes       []Note
	requiredEnv map[string]string
	seenSecret  map[string]bool
	headers     []pair
	block       *blockErr
	snap        *snapshot
}

var templateRe = regexp.MustCompile(`\{\{\s*([^{}]+?)\s*\}\}`)

// sensitiveKeyNeedles mirrors the core export redaction keys so a literal in
// a sensitive position is replaced by a placeholder, never copied verbatim.
var sensitiveKeyNeedles = []string{
	"authorization",
	"proxy-authorization",
	"x-api-key",
	"api-key",
	"apikey",
	"api_key",
	"secret",
	"token",
	"password",
	"passwd",
	"private-key",
	"private_key",
	"client-secret",
	"client_secret",
	"access-key",
	"access_key",
}

func isSensitiveKeyName(k string) bool {
	lower := strings.ToLower(strings.TrimSpace(k))
	for _, needle := range sensitiveKeyNeedles {
		if strings.Contains(lower, needle) {
			return true
		}
	}
	return false
}

func (b *builder) note(code, severity, message string) {
	b.notes = append(b.notes, Note{Code: code, Severity: severity, Message: message})
}

// requireEnv records an environment variable the snippet needs at runtime.
func (b *builder) requireEnv(name, desc string) {
	if b.requiredEnv == nil {
		b.requiredEnv = map[string]string{}
	}
	if _, ok := b.requiredEnv[name]; !ok {
		b.requiredEnv[name] = desc
	}
}

// secretEnvNote explains one placeholder once per variable name.
func (b *builder) secretEnvNote(env, secretName, where string) {
	if b.seenSecret == nil {
		b.seenSecret = map[string]bool{}
	}
	if b.seenSecret[env] {
		return
	}
	b.seenSecret[env] = true
	b.note("snippet-secret-placeholder", SeverityScrubbed,
		fmt.Sprintf("%s: secret %q stays as environment variable %s (GoRequest never reads its value for snippets); bind it before running", where, secretName, env))
}

// resolveSegments expands {{name}} templates into composable segments.
// Plain variables resolve recursively from the merged environment snapshot;
// {{secret.*}} becomes a runtime lookup segment; data.*/steps.* stay visibly
// unresolved. seen guards variable cycles; depth is capped.
func (b *builder) resolveSegments(raw, where string, seen []string) []segment {
	return b.resolveSegmentsMode(raw, where, seen, false)
}

func (b *builder) resolveSegmentsMode(raw, where string, seen []string, jsonMode bool) []segment {
	var segs []segment
	last := 0
	for _, loc := range templateRe.FindAllStringSubmatchIndex(raw, -1) {
		if loc[0] > last {
			segs = append(segs, segment{kind: segLiteral, text: raw[last:loc[0]]})
		}
		name := strings.TrimSpace(raw[loc[2]:loc[3]])
		segs = append(segs, b.resolveTemplate(name, where, seen, jsonMode)...)
		if b.block != nil {
			return nil
		}
		last = loc[1]
	}
	if last < len(raw) {
		segs = append(segs, segment{kind: segLiteral, text: raw[last:]})
	}
	return segs
}

func (b *builder) resolveTemplate(name, where string, seen []string, jsonMode bool) []segment {
	for _, s := range seen {
		if s == name {
			b.block = &blockErr{code: "snippet-variable-cycle",
				Text: fmt.Sprintf("variable cycle detected while resolving %q (%s)", name, where)}
			return nil
		}
	}
	if len(seen) >= 16 {
		b.block = &blockErr{code: "snippet-variable-depth",
			Text: fmt.Sprintf("variable nesting exceeds 16 levels while resolving %q (%s)", name, where)}
		return nil
	}

	if rest, ok := strings.CutPrefix(name, "secret."); ok {
		rest = strings.TrimSpace(rest)
		if rest == "" {
			b.block = &blockErr{code: "snippet-empty-secret", Text: "empty secret name"}
			return nil
		}
		env := envNameForSecret(rest)
		b.requireEnv(env, "secret "+rest)
		b.secretEnvNote(env, rest, where)
		if jsonMode {
			// Inside raw JSON text no concatenation is possible, so the
			// reference stays visible as {{secret.<name>}} with a note.
			return []segment{{kind: segUnresolved, text: "{{secret." + rest + "}}"}}
		}
		return []segment{{kind: segSecret, text: env}}
	}
	if strings.HasPrefix(name, "data.") || strings.HasPrefix(name, "steps.") {
		b.note("snippet-runtime-context", SeverityUnsupported,
			fmt.Sprintf("%s: %q only resolves inside a dataset/suite run; the placeholder stays in the code — replace it with a concrete value before running", where, "{{"+name+"}}"))
		return []segment{{kind: segUnresolved, text: "{{" + name + "}}"}}
	}

	raw, ok := b.vars[name]
	if !ok {
		b.block = &blockErr{code: "snippet-undefined-variable",
			Text: fmt.Sprintf("undefined variable %q in %s; bind it in the project, environment or request variables", name, where)}
		return nil
	}
	return b.resolveSegmentsMode(raw, where, append(seen, name), jsonMode)
}

// resolveFieldValue resolves a value that may sit in a sensitive position: a
// template-free literal behind a sensitive key is replaced by an explicit
// runtime placeholder plus a scrubbed note (parallel to how the core export
// redacts such values instead of copying them).
func (b *builder) resolveFieldValue(raw, keyName, where string) []segment {
	if isSensitiveKeyName(keyName) && !strings.Contains(raw, "{{") {
		env := envNameForKey(keyName)
		b.requireEnv(env, where)
		b.note("snippet-sensitive-scrubbed", SeverityScrubbed,
			fmt.Sprintf("%s holds a literal credential in the shared definition; the snippet replaces it with environment variable %s — bind the real value before running", where, env))
		return []segment{{kind: segSecret, text: env}}
	}
	return b.resolveSegments(raw, where, nil)
}

// stringField resolves a field where only a concrete string is meaningful
// (header names, query names, field names, file names). A secret placeholder
// cannot sit there (the runner would refuse too), so it blocks; runtime
// context placeholders stay visible with their warning note.
func (b *builder) stringField(raw, where string) string {
	segs := b.resolveSegments(raw, where, nil)
	if b.block != nil {
		return ""
	}
	if hasSecret(segs) {
		b.block = &blockErr{code: "snippet-secret-in-name",
			Text: fmt.Sprintf("%s resolves to a secret placeholder; secrets cannot sit in a name/identifier field — bind the value into a plain variable", where)}
		return ""
	}
	return literal(segs)
}

// credentialSegments resolves an auth credential. It mirrors the core export
// rule for credentials: only an explicit {{secret.*}} reference becomes a
// named runtime lookup; every other value (literal, empty or resolved
// through plain variables) is NEVER embedded — the snippet carries an
// environment-variable placeholder plus a scrubbed note instead.
func (b *builder) credentialSegments(raw, envKey, where, schemeID string) []segment {
	if raw == "" {
		env := envNameForKey(envKey)
		b.requireEnv(env, where)
		if schemeID != "" {
			b.note("snippet-credential-empty", SeverityWarning,
				fmt.Sprintf("%s from scheme %q declares no value; the snippet uses environment variable %s as a placeholder", where, schemeID, env))
		}
		return []segment{{kind: segSecret, text: env}}
	}
	if !strings.Contains(raw, "{{secret.") {
		segs := b.resolveSegments(raw, where, nil)
		if b.block != nil {
			return nil
		}
		env := envNameForKey(envKey)
		b.requireEnv(env, where)
		if literal(segs) == "" {
			b.note("snippet-credential-empty", SeverityWarning,
				fmt.Sprintf("%s resolves to an empty value; the snippet uses environment variable %s as a placeholder", where, env))
			return []segment{{kind: segSecret, text: env}}
		}
		b.note("snippet-credential-scrubbed", SeverityScrubbed,
			fmt.Sprintf("%s holds a credential in the shared definition; the snippet replaces it with environment variable %s — bind the real value before running", where, env))
		return []segment{{kind: segSecret, text: env}}
	}
	return b.resolveSegments(raw, where, nil)
}

func containsSegmentNewline(segs []segment) bool {
	for _, s := range segs {
		if s.kind != segSecret && strings.ContainsAny(s.text, "\r\n") {
			return true
		}
	}
	return false
}

func containsSegmentChar(segs []segment, ch byte) bool {
	for _, s := range segs {
		if s.kind != segSecret && strings.ContainsRune(s.text, rune(ch)) {
			return true
		}
	}
	return false
}

// isHTTPProtocol mirrors core.isHTTPProtocol for baseline parity.
func isHTTPProtocol(proto string) bool {
	s := strings.ToLower(strings.TrimSpace(proto))
	return s == "" || s == "http"
}

// buildSnapshot resolves the request into the reviewable snapshot following
// core.BuildPlanWithOptions + core.BuildBody semantics.
func buildSnapshot(p *core.Project, entry *core.RequestEntry, o Options) (*snapshot, []Note, *blockErr) {
	req := entry.Request
	if !isHTTPProtocol(req.Protocol) {
		return nil, nil, &blockErr{code: "snippet-non-http-protocol",
			Text: fmt.Sprintf("request %q uses protocol %q; snippets support http requests only", req.Name, req.Protocol)}
	}
	if req.HTTP == nil {
		return nil, nil, &blockErr{code: "snippet-no-http-def", Text: "request has no http definition"}
	}
	httpDef := req.HTTP

	merged, _, err := p.ResolveVars(entry, o.EnvID)
	if err != nil {
		return nil, nil, &blockErr{code: "snippet-env", Text: err.Error()}
	}
	snap := &snapshot{ID: req.ID, Name: req.Name, requiredEnv: map[string]string{}}
	b := &builder{vars: merged, requiredEnv: snap.requiredEnv, snap: snap}

	// --- method ---------------------------------------------------------
	method := strings.ToUpper(strings.TrimSpace(httpDef.Method))
	if method == "" {
		method = "GET"
	}
	if err := core.ValidateHTTPMethod(method); err != nil {
		return nil, b.notes, &blockErr{code: "snippet-invalid-method", Text: err.Error()}
	}
	snap.Method = method

	// --- headers (definition order, duplicates preserved) ----------------
	for _, kv := range httpDef.Headers {
		if !kv.IsEnabled() {
			continue
		}
		name := b.stringField(kv.Name, fmt.Sprintf("header name of %q", kv.Name))
		if b.block != nil {
			return nil, b.notes, b.block
		}
		if strings.TrimSpace(name) == "" {
			return nil, b.notes, &blockErr{code: "snippet-header-name-empty", Text: "header name is empty"}
		}
		valSegs := b.resolveFieldValue(kv.Value, name, fmt.Sprintf("header %q", name))
		if b.block != nil {
			return nil, b.notes, b.block
		}
		if strings.ContainsAny(name, "\r\n") || containsSegmentNewline(valSegs) {
			return nil, b.notes, &blockErr{code: "snippet-header-newline",
				Text: fmt.Sprintf("header %q contains a newline; refusing header injection", name)}
		}
		b.headers = append(b.headers, pair{Name: name, Value: valSegs})
	}

	// --- URL -------------------------------------------------------------
	urlSegs := b.resolveSegments(httpDef.URL, "url", nil)
	if b.block != nil {
		return nil, b.notes, b.block
	}
	if hasUnresolved(urlSegs) || hasSecret(urlSegs) {
		return nil, b.notes, &blockErr{code: "snippet-unresolvable-url",
			Text: "request url resolves to an unresolved placeholder; bind the value into a plain environment variable and retry"}
	}
	rawURL := strings.TrimSpace(literal(urlSegs))
	if rawURL == "" {
		return nil, b.notes, &blockErr{code: "snippet-url-empty", Text: "request url is empty"}
	}
	u, uerr := url.Parse(rawURL)
	if uerr != nil {
		return nil, b.notes, &blockErr{code: "snippet-url-invalid", Text: fmt.Sprintf("invalid url %q: %v", rawURL, uerr)}
	}
	if u.Scheme == "" || u.Host == "" {
		return nil, b.notes, &blockErr{code: "snippet-url-relative", Text: fmt.Sprintf("url %q must be absolute (scheme and host)", rawURL)}
	}

	// --- query (plan folds pairs through url.Values, sorted keys) ---------
	var queryPairs []pair
	for _, kv := range httpDef.Query {
		if !kv.IsEnabled() {
			continue
		}
		name := b.stringField(kv.Name, "query parameter name")
		if b.block != nil {
			return nil, b.notes, b.block
		}
		valSegs := b.resolveSegments(kv.Value, fmt.Sprintf("query parameter %q", name), nil)
		if b.block != nil {
			return nil, b.notes, b.block
		}
		if hasUnresolved(valSegs) || hasSecret(valSegs) {
			return nil, b.notes, &blockErr{code: "snippet-unresolvable-query",
				Text: fmt.Sprintf("query parameter %q resolves to an unresolved placeholder (secret or dataset value); bind the value into a plain environment variable instead", name)}
		}
		queryPairs = append(queryPairs, pair{Name: name, Value: valSegs})
	}

	// --- auth ------------------------------------------------------------
	if aerr := b.applyAuth(p, entry, req); aerr != nil || b.block != nil {
		if aerr != nil && b.block == nil {
			b.block = &blockErr{code: "snippet-auth", Text: aerr.Error()}
		}
		return nil, b.notes, b.block
	}
	for _, qp := range snap.authQuery {
		if hasUnresolved(qp.Value) || hasSecret(qp.Value) {
			return nil, b.notes, &blockErr{code: "snippet-unresolvable-query",
				Text: fmt.Sprintf("api-key %q travels in the query string and resolves to a secret; that cannot be represented as a runtime lookup — move the credential to a header", qp.Name)}
		}
	}
	queryPairs = append(queryPairs, snap.authQuery...)

	if len(queryPairs) > 0 {
		q := u.Query()
		for _, qp := range queryPairs {
			q.Add(qp.Name, literal(qp.Value))
		}
		u.RawQuery = q.Encode()
	}
	fullURL := u.String()
	snap.URL = []segment{{kind: segLiteral, text: fullURL}}

	// --- body ------------------------------------------------------------
	if berr := b.buildBody(httpDef.Body); berr != nil || b.block != nil {
		if berr != nil && b.block == nil {
			b.block = &blockErr{code: "snippet-body", Text: berr.Error()}
		}
		return nil, b.notes, b.block
	}
	if snap.Body.HasBody {
		ct, force := bodyContentType(&snap.Body)
		b.headers = applyBodyContentType(b.headers, ct, force)
	}

	// --- timeout (core: request override or DefaultTimeout) --------------
	snap.Timeout = core.DefaultTimeout
	if req.Timeout != "" {
		d, terr := time.ParseDuration(req.Timeout)
		if terr != nil {
			return nil, b.notes, &blockErr{code: "snippet-invalid-timeout", Text: fmt.Sprintf("invalid timeout %q: %v", req.Timeout, terr)}
		}
		if d > 0 {
			snap.Timeout = d
		}
	}

	snap.Headers = b.headers
	snap.requiredEnv = b.requiredEnv
	return snap, b.notes, nil
}

// applyAuth resolves the effective scheme (project → folder → request, like
// the runner) and folds it into the snapshot. Bearer/basic/OAuth stay on the
// explicit carrier because each language composes them at run time.
func (b *builder) applyAuth(p *core.Project, entry *core.RequestEntry, req *core.Request) error {
	snap := b.snap
	snap.Auth = authSnapshot{Carrier: authNone, Source: "none"}
	eff, err := p.ResolveEffectiveAuth(entry, req)
	if err != nil {
		return err
	}
	if eff == nil || eff.Scheme == nil {
		return nil
	}
	scheme := eff.Scheme
	snap.Auth.RefID = scheme.ID
	if eff.SourceKind != "" {
		snap.Auth.Source = eff.SourceKind
	}

	switch strings.ToLower(strings.TrimSpace(scheme.Type)) {
	case "", "none":
		return nil

	case "bearer":
		tok := b.credentialSegments(scheme.Token, "bearer-token", "bearer auth token", scheme.ID)
		if b.block != nil {
			return fmt.Errorf("%s", b.block.Text)
		}
		snap.Auth.Carrier = authBearer
		snap.Auth.Token = tok
		return nil

	case "basic":
		userSegs := b.resolveSegments(scheme.Username, "basic auth username", nil)
		if b.block != nil {
			return fmt.Errorf("%s", b.block.Text)
		}
		passSegs := b.credentialSegments(scheme.Password, "password", "basic auth password", "")
		if b.block != nil {
			return fmt.Errorf("%s", b.block.Text)
		}
		snap.Auth.Carrier = authBasic
		snap.Auth.User = userSegs
		snap.Auth.Pass = passSegs
		return nil

	case "apikey", "api-key":
		loc := core.NormalizeAPIKeyLocation(scheme.In)
		keyName := b.stringField(scheme.Name, "api-key name")
		if b.block != nil {
			return fmt.Errorf("%s", b.block.Text)
		}
		if keyName == "" {
			return fmt.Errorf("api key auth requires a name")
		}
		valSegs := b.credentialSegments(scheme.Value, keyName, fmt.Sprintf("api key %q", keyName), "")
		if b.block != nil {
			return fmt.Errorf("%s", b.block.Text)
		}
		switch loc {
		case "header":
			if strings.ContainsAny(keyName, "\r\n") || containsSegmentNewline(valSegs) {
				return fmt.Errorf("api key header %q contains a newline; refusing header injection", keyName)
			}
			b.headers = append(b.headers, pair{Name: keyName, Value: valSegs})
		case "query":
			snap.authQuery = append(snap.authQuery, pair{Name: keyName, Value: valSegs})
			b.note("snippet-apikey-query", SeverityInfo,
				fmt.Sprintf("api key %q travels in the query string; it is folded into the snippet url like the runner does", keyName))
		case "cookie":
			if strings.ContainsAny(keyName, "\r\n;=") || containsSegmentNewline(valSegs) || containsSegmentChar(valSegs, ';') {
				return fmt.Errorf("api key cookie %q contains invalid cookie characters; refusing injection", keyName)
			}
			b.appendCookiePair(keyName, valSegs)
		}
		return nil

	case "oauth2", "oidc":
		env := envNameForKey("oauth-access-token")
		b.requireEnv(env, "OAuth access token for this request")
		b.note("snippet-oauth-placeholder", SeverityWarning,
			fmt.Sprintf("GoRequest injects the live OAuth/OIDC token at send time and the snippet never reads it; bind a valid access token to environment variable %s before running", env))
		snap.Auth.Carrier = authOAuth
		snap.Auth.Token = []segment{{kind: segSecret, text: env}}
		return nil

	default:
		return fmt.Errorf("unsupported auth type %q", scheme.Type)
	}
}

// appendCookiePair mirrors core.appendCookiePair: merge into the first
// case-insensitive Cookie header or append a new one. The "<name>=" part is
// always a literal prefix; only the value may carry a runtime lookup segment.
func (b *builder) appendCookiePair(name string, val []segment) {
	prefixed := append([]segment{{kind: segLiteral, text: name + "="}}, val...)
	for i := range b.headers {
		if strings.EqualFold(b.headers[i].Name, "Cookie") {
			if len(b.headers[i].Value) == 0 || literal(b.headers[i].Value) == "" {
				b.headers[i].Value = prefixed
				return
			}
			b.headers[i].Value = append(b.headers[i].Value,
				segment{kind: segLiteral, text: "; " + name + "="})
			b.headers[i].Value = append(b.headers[i].Value, val...)
			return
		}
	}
	b.headers = append(b.headers, pair{Name: "Cookie", Value: prefixed})
}

// applyBodyContentType mirrors core.applyBodyContentType: multipart replaces
// any existing Content-Type; other kinds only fill a missing one.
// Content-Type always ends up as the last pair.
func applyBodyContentType(headers []pair, ct string, force bool) []pair {
	out := make([]pair, 0, len(headers)+1)
	replaced := false
	for _, h := range headers {
		if strings.EqualFold(h.Name, "Content-Type") {
			if !replaced {
				out = append(out, pair{Name: "Content-Type", Value: []segment{{kind: segLiteral, text: ct}}})
				replaced = true
			}
			continue
		}
		out = append(out, h)
	}
	if !replaced {
		out = append(out, pair{Name: "Content-Type", Value: []segment{{kind: segLiteral, text: ct}}})
	}
	return out
}

// bodyContentType returns the encoded-body media type and whether multipart
// forcibly replaced a user Content-Type (core.ensureContentType/Force).
func bodyContentType(body *bodySnapshot) (string, bool) {
	switch body.Kind {
	case bodyJSON:
		return "application/json", false
	case bodyText:
		return "text/plain; charset=utf-8", false
	case bodyXML:
		return "application/xml", false
	case bodyURLEncoded:
		return "application/x-www-form-urlencoded", false
	case bodyMultipart:
		return "multipart/form-data; boundary=" + multipartBoundary, true
	case bodyBinary:
		ct := strings.TrimSpace(body.ContentType)
		if ct == "" {
			ct = "application/octet-stream"
		}
		return ct, false
	default:
		return "", false
	}
}

// multipartBoundary is the fixed, deterministic boundary used by generated
// code. The core runner draws a random boundary per request; both are
// protocol-legal, so the difference is documented as an info note.
const multipartBoundary = "----GoRequestSnippetBoundary21890504ykT"

func (b *builder) buildBody(body *core.Body) error {
	snap := b.snap
	if body == nil {
		snap.Body = bodySnapshot{Kind: bodyNone}
		return nil
	}
	if err := core.ValidateBody(body); err != nil {
		return err
	}
	tb, err := core.InspectBody(body)
	if err != nil {
		return err
	}
	switch tb.Kind {
	case core.BodyNone:
		snap.Body = bodySnapshot{Kind: bodyNone}
		return nil

	case core.BodyJSON:
		snap.Body.Kind = bodyJSON
		snap.Body.HasBody = true
		if tb.File != "" || tb.LocalFile != "" {
			ref, ferr := fileRefOfBody(tb)
			if ferr != nil {
				return ferr
			}
			snap.Body.File = ref
			snap.Body.HasFile = true
			b.fileNote(ref, "json body file")
			return nil
		}
		text, jerr := b.renderJSONText(tb.JSONNode)
		if jerr != nil {
			return fmt.Errorf("json body: %s", jerr.Text)
		}
		snap.Body.JSONText = text
		return nil

	case core.BodyText, core.BodyXML:
		snap.Body.Kind = bodyText
		if tb.Kind == core.BodyXML {
			snap.Body.Kind = bodyXML
		}
		snap.Body.HasBody = true
		if tb.File != "" || tb.LocalFile != "" {
			ref, ferr := fileRefOfBody(tb)
			if ferr != nil {
				return ferr
			}
			snap.Body.File = ref
			snap.Body.HasFile = true
			b.fileNote(ref, "body file")
			return nil
		}
		snap.Body.Text = b.resolveSegments(tb.Text, "text body", nil)
		return nil

	case core.BodyURLEncoded:
		snap.Body.Kind = bodyURLEncoded
		snap.Body.HasBody = true
		if tb.File != "" || tb.LocalFile != "" {
			ref, ferr := fileRefOfBody(tb)
			if ferr != nil {
				return ferr
			}
			snap.Body.File = ref
			snap.Body.HasFile = true
			snap.Body.HasLineup = true
			b.fileNote(ref, "urlencoded body file")
			b.note("snippet-urlencoded-file-text", SeverityWarning,
				"the urlencoded body is read from a file at send time; the snippet references the file explicitly — set the placeholder path before running")
			return nil
		}
		mixed := false
		for _, f := range tb.FormFields {
			if f.IsEnabled() && f.Encoded {
				mixed = true
			}
		}
		if mixed {
			// Pre-encoded fields must not be encoded twice, so the body is
			// carried as a pre-built literal (same rule as the runner);
			// secrets cannot be composed in that mode and are refused.
			var parts []string
			for idx, f := range tb.FormFields {
				if !f.IsEnabled() {
					continue
				}
				name := b.stringField(f.Name, fmt.Sprintf("urlencoded field %d name", idx+1))
				if b.block != nil {
					return fmt.Errorf("%s", b.block.Text)
				}
				if strings.TrimSpace(name) == "" {
					return fmt.Errorf("urlencoded item %d: enabled field has an empty name", idx+1)
				}
				valSegs := b.resolveSegments(f.Value, fmt.Sprintf("urlencoded field %q value", name), nil)
				if b.block != nil {
					return fmt.Errorf("%s", b.block.Text)
				}
				if hasSecret(valSegs) {
					return fmt.Errorf("urlencoded field %q mixes pre-encoded fields with a secret placeholder; that cannot be represented exactly — bind the secret into a plain variable or split the request", name)
				}
				val := literal(valSegs)
				if !f.Encoded {
					name = url.QueryEscape(name)
					val = url.QueryEscape(val)
				}
				parts = append(parts, name+"="+val)
			}
			snap.Body.Lineup = strings.Join(parts, "&")
			snap.Body.HasLineup = true
			b.note("snippet-urlencoded-preencoded", SeverityInfo,
				"at least one urlencoded field is already percent-encoded; the snippet carries the exact pre-built body the runner would send")
			return nil
		}
		for idx, f := range tb.FormFields {
			if !f.IsEnabled() {
				continue
			}
			name := b.stringField(f.Name, fmt.Sprintf("urlencoded field %d name", idx+1))
			if b.block != nil {
				return fmt.Errorf("%s", b.block.Text)
			}
			if strings.TrimSpace(name) == "" {
				return fmt.Errorf("urlencoded item %d: enabled field has an empty name", idx+1)
			}
			valSegs := b.resolveSegments(f.Value, fmt.Sprintf("urlencoded field %q value", name), nil)
			if b.block != nil {
				return fmt.Errorf("%s", b.block.Text)
			}
			snap.Body.Fields = append(snap.Body.Fields, formFieldSnap{
				Name: name, Value: valSegs,
			})
		}
		return nil

	case core.BodyFormData:
		snap.Body.Kind = bodyMultipart
		snap.Body.HasBody = true
		for idx, part := range tb.MultipartParts {
			if !part.IsEnabled() {
				continue
			}
			name := b.stringField(part.Name, fmt.Sprintf("multipart part %d name", idx+1))
			if b.block != nil {
				return fmt.Errorf("%s", b.block.Text)
			}
			if strings.TrimSpace(name) == "" {
				return fmt.Errorf("form-data part %d: enabled part has an empty name", idx+1)
			}
			var partCT string
			if part.ContentType != "" {
				partCT = b.stringField(part.ContentType, fmt.Sprintf("multipart part %q contentType", name))
				if b.block != nil {
					return fmt.Errorf("%s", b.block.Text)
				}
			}
			np := multipartPartSnap{Name: name, ContentType: partCT}
			if part.EffectiveKind() == "file" {
				ref, rerr := part.ResolveFileRef()
				if rerr != nil {
					return rerr
				}
				if strings.ContainsAny(name, "\r\n") {
					return fmt.Errorf("form-data part %q contains a newline; refusing header injection", name)
				}
				np.IsFile = true
				np.File = fileRefFromCore(ref)
				if part.FileName != "" {
					fn := b.stringField(part.FileName, fmt.Sprintf("multipart part %q fileName", name))
					if b.block != nil {
						return fmt.Errorf("%s", b.block.Text)
					}
					if strings.ContainsAny(fn, "\r\n") {
						return fmt.Errorf("form-data part %q fileName contains a newline", name)
					}
					np.FileName = fn
				}
				b.fileNote(np.File, fmt.Sprintf("multipart file part %q", name))
			} else {
				if strings.ContainsAny(name, "\r\n") {
					return fmt.Errorf("form-data part %q contains a newline; refusing header injection", name)
				}
				np.Text = b.resolveSegments(part.Value, fmt.Sprintf("multipart field %q", name), nil)
				if b.block != nil {
					return fmt.Errorf("%s", b.block.Text)
				}
			}
			snap.Body.Parts = append(snap.Body.Parts, np)
		}
		b.note("snippet-multipart-boundary", SeverityInfo,
			"generated code builds multipart with a fixed boundary; the GoRequest runner uses a random boundary per request (both are protocol-legal)")
		return nil

	case core.BodyBinary:
		snap.Body.Kind = bodyBinary
		snap.Body.HasBody = true
		if tb.LocalFile == "" && tb.File == "" {
			return fmt.Errorf("binary body requires file or local file mapping")
		}
		ref, ferr := fileRefOfBody(tb)
		if ferr != nil {
			return ferr
		}
		snap.Body.File = ref
		snap.Body.HasFile = true
		if tb.ContentType != "" {
			ct := b.stringField(tb.ContentType, "binary body contentType")
			if b.block != nil {
				return fmt.Errorf("%s", b.block.Text)
			}
			snap.Body.ContentType = ct
		}
		b.fileNote(ref, "binary body file")
		return nil

	default:
		return fmt.Errorf("unsupported body type %q", body.Type)
	}
}

func fileRefOfBody(tb *core.TypedBody) (fileRefSnap, error) {
	if tb.LocalFile != "" {
		if err := core.ValidateLocalSlot(tb.LocalFile); err != nil {
			return fileRefSnap{}, err
		}
		return fileRefSnap{IsLocal: true, Slot: tb.LocalFile, Suggested: "local:" + tb.LocalFile}, nil
	}
	ref, err := core.ParseFileRef(tb.File)
	if err != nil {
		return fileRefSnap{}, err
	}
	return fileRefFromCore(ref), nil
}

func fileRefFromCore(ref core.FileRef) fileRefSnap {
	if ref.Kind == core.FileRefLocal {
		return fileRefSnap{IsLocal: true, Slot: ref.Slot, Suggested: "local:" + ref.Slot}
	}
	return fileRefSnap{Path: ref.Path, Suggested: ref.Path}
}

// fileNote documents that no machine path was copied from the project.
func (b *builder) fileNote(ref fileRefSnap, where string) {
	if ref.IsLocal {
		b.note("snippet-local-file-slot", SeverityWarning,
			fmt.Sprintf("%s is bound to machine-local %s; the snippet emits an explicit path constant because host paths never enter shared definitions", where, ref.Suggested))
		return
	}
	b.note("snippet-project-file", SeverityInfo,
		fmt.Sprintf("%s is the .gorequest-relative file %s; the snippet emits an explicit path constant — point it at the local copy before running", where, ref.Suggested))
}

// ---------------------------------------------------------------- JSON ----

// renderJSONText renders the JSON body node to core-parity JSON. Core renders
// mappings through map[string]any and encoding/json.Marshal, so keys are
// sorted byte-wise and scalars take the same decode path. Plain variables
// resolve in place; secret references stay as visible {{secret.<name>}} text
// with a note naming the environment variable.
func (b *builder) renderJSONText(node *yaml.Node) (string, *blockErr) {
	var sb strings.Builder
	n := node
	if n != nil && n.Kind == yaml.DocumentNode {
		if len(n.Content) == 0 {
			return "", &blockErr{code: "snippet-json-empty", Text: "empty body"}
		}
		n = n.Content[0]
	}
	if err := b.writeJSON(&sb, n, "json body", 0); err != nil {
		return "", err
	}
	return sb.String(), nil
}

func (b *builder) writeJSON(sb *strings.Builder, n *yaml.Node, where string, depth int) *blockErr {
	if n == nil {
		sb.WriteString("null")
		return nil
	}
	if depth > 64 {
		return &blockErr{code: "snippet-json-depth", Text: "json body nesting exceeds 64 levels"}
	}
	switch n.Kind {
	case yaml.DocumentNode:
		if len(n.Content) == 0 {
			return &blockErr{code: "snippet-json-empty", Text: "empty body"}
		}
		return b.writeJSON(sb, n.Content[0], where, depth+1)
	case yaml.MappingNode:
		type kv struct {
			key string
			val *yaml.Node
		}
		items := make([]kv, 0, len(n.Content)/2)
		for i := 0; i+1 < len(n.Content); i += 2 {
			keyNode := n.Content[i]
			if keyNode == nil {
				return &blockErr{code: "snippet-json-key", Text: "json body has an empty key"}
			}
			if keyNode.Kind != yaml.ScalarNode {
				return &blockErr{code: "snippet-json-key", Text: "json body keys must be scalar strings"}
			}
			segs := b.resolveSegmentsMode(keyNode.Value, where+" key", nil, true)
			if b.block != nil {
				return b.block
			}
			items = append(items, kv{key: literal(segs), val: n.Content[i+1]})
		}
		// encoding/json sorts map keys byte-wise; mirror it for parity.
		sort.Slice(items, func(i, j int) bool { return items[i].key < items[j].key })
		sb.WriteByte('{')
		for i, it := range items {
			if i > 0 {
				sb.WriteByte(',')
			}
			sb.WriteByte('"')
			sb.WriteString(jsonEscapeString(it.key))
			sb.WriteByte('"')
			sb.WriteByte(':')
			if err := b.writeJSON(sb, it.val, where, depth+1); err != nil {
				return err
			}
		}
		sb.WriteByte('}')
		return nil
	case yaml.SequenceNode:
		sb.WriteByte('[')
		for i, c := range n.Content {
			if i > 0 {
				sb.WriteByte(',')
			}
			if err := b.writeJSON(sb, c, where, depth+1); err != nil {
				return err
			}
		}
		sb.WriteByte(']')
		return nil
	case yaml.ScalarNode:
		if n.Tag == "!!str" || n.Tag == "" {
			segs := b.resolveSegmentsMode(n.Value, where, nil, true)
			if b.block != nil {
				return b.block
			}
			sb.WriteByte('"')
			for _, s := range segs {
				sb.WriteString(jsonEscapeString(s.text))
			}
			sb.WriteByte('"')
			return nil
		}
		var v any
		if err := n.Decode(&v); err != nil {
			return &blockErr{code: "snippet-json-scalar", Text: fmt.Sprintf("cannot decode json body scalar: %v", err)}
		}
		out, err := json.Marshal(v)
		if err != nil {
			return &blockErr{code: "snippet-json-scalar", Text: fmt.Sprintf("cannot encode json body scalar: %v", err)}
		}
		sb.Write(out)
		return nil
	default:
		return &blockErr{code: "snippet-json-node", Text: fmt.Sprintf("unsupported json body node kind %d", n.Kind)}
	}
}

// jsonEscapeString escapes s exactly like encoding/json.Marshal with default
// HTML escaping on (core parity: <, >, & become \u003c\u003e\u0026, control
// bytes use short escapes or \u00xx), so the snapshot byte-matches what the
// Go runner actually sends.
func jsonEscapeString(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 8)
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		case '<', '>', '&':
			fmt.Fprintf(&b, `\u%04x`, r)
		default:
			if r < 0x20 || r == 0x7f {
				fmt.Fprintf(&b, `\u%04x`, r)
				continue
			}
			b.WriteRune(r)
		}
	}
	return b.String()
}
