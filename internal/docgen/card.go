package docgen

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"gorequest/internal/core"
)

// CardLabels are the localized labels of a derived card. Cards are written
// into user documents, so their labels are per-export data, not app chrome:
// the UI picks the table matching the app language.
type CardLabels struct {
	Interface      string //
	Method         string
	URL            string
	PathTemplate   string
	Auth           string
	AuthNone       string
	AuthInherit    string
	AuthInline     string
	AuthRef        string
	Parameters     string
	ParamName      string
	ParamIn        string
	ParamRequired  string
	ParamType      string
	ParamDesc      string
	ParamNone      string
	RequestBody    string
	Examples       string
	Responses      string
	Status         string
	Content        string
	RespHeaders    string
	NoContract     string // note when a request has no OpenAPI contract
	NoResponses    string // note when no response definitions exist
	Deprecated     string // badge text
	OpenRequest    string // link label, %s = request id
	DerivedFromDef string // note when params come from the raw request
	ScrubbedNote   string // %d masked, %d blocked
	BlockedNote    string // example withheld after scrubbing
	NonHTTPNote    string // %s = protocol
	TemplateVars   string // note about {{var}} placeholders in examples
}

// enLabels / zhLabels are the two shipped label tables.
func enLabels() CardLabels {
	return CardLabels{
		Interface: "Interface", Method: "Method", URL: "URL", PathTemplate: "Path template",
		Auth: "Auth", AuthNone: "none", AuthInherit: "project default", AuthInline: "inline",
		AuthRef: "ref", Parameters: "Parameters", ParamName: "Name", ParamIn: "In",
		ParamRequired: "Required", ParamType: "Type", ParamDesc: "Description", ParamNone: "None",
		RequestBody: "Request body", Examples: "Example", Responses: "Responses",
		Status: "Status", Content: "Content", RespHeaders: "Headers",
		NoContract:  "No OpenAPI contract is attached; parameters below are derived from the stored request definition.",
		NoResponses: "No response definitions are recorded for this request.",
		Deprecated:  "Deprecated", OpenRequest: "Open request %s in GoRequest",
		DerivedFromDef: "derived from the stored request definition",
		ScrubbedNote:   "%d value(s) masked and %d blocked as credential-shaped while deriving this card.",
		BlockedNote:    "One example was withheld because it could not be proven safe to publish.",
		NonHTTPNote:    "Non-HTTP request (protocol %s); only basic fields are documented in this build.",
		TemplateVars:   "Scalar values, including {{…}} variable templates, are shown exactly as stored.",
	}
}

// EnglishCardLabels returns the English label table.
func EnglishCardLabels() CardLabels { return enLabels() }

// ChineseCardLabels returns the Chinese label table.
func ChineseCardLabels() CardLabels {
	return CardLabels{
		Interface: "接口", Method: "方法", URL: "地址", PathTemplate: "路径模板",
		Auth: "鉴权", AuthNone: "无", AuthInherit: "项目默认", AuthInline: "内联",
		AuthRef: "引用", Parameters: "参数", ParamName: "名称", ParamIn: "位置",
		ParamRequired: "必填", ParamType: "类型", ParamDesc: "说明", ParamNone: "无",
		RequestBody: "请求体", Examples: "示例", Responses: "响应",
		Status: "状态", Content: "内容", RespHeaders: "响应头",
		NoContract:  "未附加 OpenAPI 契约，以下参数按已保存的请求定义派生。",
		NoResponses: "该请求未记录响应定义。",
		Deprecated:  "已弃用", OpenRequest: "在 GoRequest 中打开请求 %s",
		DerivedFromDef: "按已保存的请求定义派生",
		ScrubbedNote:   "派生本卡片时自动脱敏 %d 个值，并因疑似凭据阻断 %d 个值。",
		BlockedNote:    "有一条示例因无法证明可安全发布而被保留，未予展示。",
		NonHTTPNote:    "非 HTTP 请求（协议 %s）；本版本仅文档化基础字段。",
		TemplateVars:   "标量值（含 {{…}} 变量模板）按存储原样展示。",
	}
}

// CardOptions tune card derivation.
type CardOptions struct {
	Labels CardLabels
	// Redactor registers known-secret values for masking; may be nil.
	Redactor *core.Redactor
	// MaxExampleBytes bounds one serialized example; default 4 KiB.
	MaxExampleBytes int
}

const defaultMaxExampleBytes = 4096

func (o CardOptions) exampleLimit() int {
	if o.MaxExampleBytes > 0 {
		return o.MaxExampleBytes
	}
	return defaultMaxExampleBytes
}

// CardParameter is one published parameter row.
type CardParameter struct {
	Name        string
	In          string
	Required    bool
	Deprecated  bool
	Type        string
	Description string
	// Example is a scrubbed value; present only when the definition itself
	// declares one (contract metadata or a stored raw value).
	Example    any
	HasExample bool
}

// CardMediaType is one published media type with an optional scrubbed example.
type CardMediaType struct {
	Name        string
	Schema      string // compact schema summary, "" when absent
	Example     string // serialized, scrubbed example block
	HasExample  bool   // Example carries content
	ExampleLang string // code fence language, e.g. json
	ExampleName string // named example label, "" for the anonymous example
}

// CardResponse is one published status with its media types and headers.
type CardResponse struct {
	Status     string
	MediaTypes []CardMediaType
	Headers    []CardParameter
}

// CardBodySummary is the published request body.
type CardBodySummary struct {
	Required   bool
	MediaTypes []CardMediaType
}

// RequestCard is the derived, publishable view of one request.
type RequestCard struct {
	ID            string
	Name          string
	Protocol      string
	OperationID   string
	Method        string
	URL           string
	PathTemplate  string
	Summary       string
	Description   string
	Deprecated    bool
	Auth          string
	AuthMissing   bool // referenced auth scheme not found
	HasContract   bool
	Parameters    []CardParameter
	ParamsDerived bool // parameters came from the raw request, not a contract
	Body          *CardBodySummary
	Responses     []CardResponse
	// responseDesc carries scrubbed response descriptions keyed by status;
	// kept separately so table rows stay compact.
	responseDesc   map[string]string
	labels         CardLabels
	Scrub          ScrubStats
	BlockedExample bool
	NonHTTP        bool
}

// Labels returns the label table the card was built with.
func (c *RequestCard) Labels() CardLabels { return c.labels }

// BuildRequestCard derives the card data for entry. It only reads the stored
// request, its optional OpenAPI contract and — for the auth summary — the
// referenced auth scheme. No variable is resolved, no request is executed and
// no credential store is consulted.
func BuildRequestCard(p *core.Project, entry *core.RequestEntry, opts CardOptions) (*RequestCard, error) {
	if entry == nil || entry.Request == nil {
		return nil, fmt.Errorf("request entry is nil")
	}
	req := entry.Request
	if opts.Labels.Interface == "" {
		opts.Labels = EnglishCardLabels()
	}
	card := &RequestCard{ID: req.ID, Name: req.Name, Protocol: req.Protocol, labels: opts.Labels, responseDesc: map[string]string{}}
	if card.Protocol == "" && req.HTTP != nil {
		card.Protocol = "http"
	}

	scrub := NewScrubber(opts.Redactor)
	// API key header names become sensitive names so their example values
	// are masked whatever their shape.
	if p != nil {
		for _, scheme := range p.AuthSchemes {
			if scheme != nil && scheme.Type == "apikey" && strings.EqualFold(scheme.In, "header") && scheme.Name != "" {
				scrub.RegisterSensitiveName(scheme.Name)
			}
		}
	}

	authLine, inlineScheme := authSummary(p, req, opts.Labels)
	card.Auth = authLine
	if inlineScheme != nil && strings.EqualFold(inlineScheme.In, "header") && inlineScheme.Name != "" {
		scrub.RegisterSensitiveName(inlineScheme.Name)
	}

	if req.HTTP == nil {
		card.NonHTTP = true
		if docs := nonEmpty(req.Documentation); len(docs) > 0 {
			card.Summary = scrub.String(docs[0])
		}
		card.Scrub = scrub.Stats()
		return card, nil
	}
	card.Method = strings.ToUpper(strings.TrimSpace(req.HTTP.Method))
	card.URL = req.HTTP.URL

	contract, err := core.GetRequestOpenAPI(req)
	if err != nil {
		return nil, fmt.Errorf("request %s: reading extensions.openapi: %w", req.ID, err)
	}
	card.HasContract = contract != nil
	if contract != nil {
		card.OperationID = contract.OperationID
		card.Summary = scrub.String(contract.Summary)
		card.Description = scrub.String(contract.Description)
		card.Deprecated = contract.Deprecated
		if contract.PathTemplate != "" {
			card.PathTemplate = contract.PathTemplate
		}
		card.Parameters = contractParameters(contract, scrub)
		card.Body = contractBody(contract, scrub, opts)
		card.Responses, card.responseDesc = contractResponses(contract, scrub, opts)
	}
	if card.Parameters == nil {
		// Derive the parameter table from the stored definition when the
		// request carries no contract parameters.
		card.Parameters = derivedParameters(req, scrub, opts.Labels)
		card.ParamsDerived = len(card.Parameters) > 0
		card.Summary = firstNonEmpty(card.Summary, scrub.String(req.Name))
	}
	if card.Body == nil {
		card.Body = derivedBody(req, scrub, opts)
	}
	card.Scrub = scrub.Stats()
	card.BlockedExample = scrub.Stats().Blocked > 0
	return card, nil
}

func nonEmpty(in []string) []string {
	out := make([]string, 0, len(in))
	for _, v := range in {
		if strings.TrimSpace(v) != "" {
			out = append(out, strings.TrimSpace(v))
		}
	}
	return out
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// authSummary produces a value-free description of the request's auth and
// returns the inline scheme when one is declared (for sensitive-name
// registration by the caller).
func authSummary(p *core.Project, req *core.Request, labels CardLabels) (string, *core.AuthScheme) {
	switch {
	case req.Auth == nil:
		return labels.AuthInherit, nil
	case req.Auth.Mode == "none":
		return labels.AuthNone, nil
	case req.Auth.Mode == "ref":
		if p != nil && p.AuthSchemes != nil {
			if scheme, ok := p.AuthSchemes[req.Auth.Ref]; ok && scheme != nil {
				return fmt.Sprintf("%s (%s %q)", strings.ToUpper(scheme.Type), labels.AuthRef, scheme.ID), nil
			}
		}
		return fmt.Sprintf("%s (%s %q)", strings.ToUpper(labels.Auth), labels.AuthRef, req.Auth.Ref), nil
	case req.Auth.Mode == "inline" && req.Auth.Inline != nil:
		s := req.Auth.Inline
		return fmt.Sprintf("%s (%s)", strings.ToUpper(s.Type), labels.AuthInline), s
	}
	return labels.AuthInherit, nil
}

// --- parameters -----------------------------------------------------------

func contractParameters(contract *core.OperationContract, scrub *Scrubber) []CardParameter {
	if len(contract.Parameters) == 0 {
		return nil
	}
	out := make([]CardParameter, 0, len(contract.Parameters))
	for _, p := range contract.Parameters {
		row := CardParameter{
			Name:        p.Name,
			In:          p.In,
			Required:    p.Required,
			Deprecated:  p.Deprecated,
			Type:        schemaTableType(p.Schema, p.SchemaRef),
			Description: scrub.String(p.Description),
		}
		if p.Example != nil {
			row.Example, row.HasExample = scrub.NamedValue(p.Name, p.Example), true
		} else if len(p.Examples) > 0 {
			if v, name, ok := firstNamedExample(p.Examples); ok {
				row.Example, row.HasExample = scrub.NamedValue(p.Name, v), true
				_ = name
			}
		}
		// Path parameters are required by contract whenever present.
		if p.In == "path" {
			row.Required = true
		}
		out = append(out, row)
	}
	return out
}

// derivedParameters turns the raw request query/header pairs into rows. Only
// enabled pairs count: a disabled pair is not part of the current definition.
func derivedParameters(req *core.Request, scrub *Scrubber, labels CardLabels) []CardParameter {
	var out []CardParameter
	for _, kv := range req.HTTP.Query {
		if !kv.IsEnabled() {
			continue
		}
		out = append(out, CardParameter{
			Name: kv.Name, In: "query",
			Description: labels.DerivedFromDef,
			Example:     scrub.NamedValue(kv.Name, kv.Value), HasExample: true,
		})
	}
	for _, kv := range req.HTTP.Headers {
		if !kv.IsEnabled() {
			continue
		}
		out = append(out, CardParameter{
			Name: kv.Name, In: "header",
			Description: labels.DerivedFromDef,
			Example:     scrub.NamedValue(kv.Name, kv.Value), HasExample: true,
		})
	}
	return out
}

func firstNamedExample(examples map[string]core.OpenAPIExampleObject) (any, string, bool) {
	names := make([]string, 0, len(examples))
	for name := range examples {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		ex := examples[name]
		if ex.Value != nil {
			return ex.Value, name, true
		}
		if ex.DataValue != nil {
			return ex.DataValue, name, true
		}
		if ex.SerializedValue != "" {
			return ex.SerializedValue, name, true
		}
	}
	return nil, "", false
}

// --- body -----------------------------------------------------------------

func contractBody(contract *core.OperationContract, scrub *Scrubber, opts CardOptions) *CardBodySummary {
	rb := contract.RequestBody
	if rb == nil || len(rb.Content) == 0 {
		return nil
	}
	body := &CardBodySummary{Required: rb.Required}
	body.MediaTypes = mediaTypesToCard(rb.Content, scrub, opts)
	if len(body.MediaTypes) == 0 {
		return nil
	}
	return body
}

func mediaTypesToCard(content map[string]core.MediaTypeContract, scrub *Scrubber, opts CardOptions) []CardMediaType {
	names := make([]string, 0, len(content))
	for name := range content {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]CardMediaType, 0, len(names))
	for _, name := range names {
		mc := content[name]
		mt := CardMediaType{
			Name:   name,
			Schema: compactSchema(mc.Schema, mc.SchemaRef),
		}
		if mc.Example != nil {
			mt.Example, mt.ExampleLang, mt.HasExample = serializeExample(name, mc.Example, scrub, opts)
		}
		if !mt.HasExample && len(mc.Examples) > 0 {
			for _, exName := range sortedExampleNames(mc.Examples) {
				ex := mc.Examples[exName]
				if ex.Value == nil && ex.DataValue == nil && ex.SerializedValue == "" {
					continue
				}
				var v any
				switch {
				case ex.Value != nil:
					v = ex.Value
				case ex.DataValue != nil:
					v = ex.DataValue
				default:
					v = ex.SerializedValue
				}
				mt.Example, mt.ExampleLang, mt.HasExample = serializeExample(name, v, scrub, opts)
				mt.ExampleName = exName
				if mt.HasExample {
					break
				}
			}
		}
		out = append(out, mt)
	}
	return out
}

func sortedExampleNames(examples map[string]core.OpenAPIExampleObject) []string {
	names := make([]string, 0, len(examples))
	for name := range examples {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// derivedBody publishes the stored body when no contract exists. Only content
// types already present in the YAML are shown, and raw values publish only
// below the size bound and after scrubbing.
func derivedBody(req *core.Request, scrub *Scrubber, opts CardOptions) *CardBodySummary {
	body := req.HTTP.Body
	if body == nil || (body.Type == "" && body.Value == nil) {
		return nil
	}
	mt := CardMediaType{Name: "text/plain"}
	switch strings.ToLower(body.Type) {
	case "json":
		mt.Name = "application/json"
		if body.Value != nil {
			var v any
			if err := body.Value.Decode(&v); err != nil {
				v = nil
			}
			if v != nil {
				mt.Example, mt.ExampleLang, mt.HasExample = serializeExample(mt.Name, v, scrub, opts)
			}
		}
	case "text":
		mt.Name = "text/plain"
	case "xml":
		mt.Name = "application/xml"
	default:
		if body.Type != "" {
			mt.Name = body.Type
		}
	}
	return &CardBodySummary{MediaTypes: []CardMediaType{mt}}
}

// --- responses ------------------------------------------------------------

func contractResponses(contract *core.OperationContract, scrub *Scrubber, opts CardOptions) ([]CardResponse, map[string]string) {
	desc := map[string]string{}
	if len(contract.Responses) == 0 {
		return nil, desc
	}
	statuses := make([]string, 0, len(contract.Responses))
	for status := range contract.Responses {
		statuses = append(statuses, status)
	}
	sort.Slice(statuses, func(i, j int) bool {
		ki, kj := responseSortKey(statuses[i]), responseSortKey(statuses[j])
		if ki != kj {
			return ki < kj
		}
		return statuses[i] < statuses[j]
	})
	out := make([]CardResponse, 0, len(statuses))
	for _, status := range statuses {
		rc := contract.Responses[status]
		desc[status] = scrub.String(rc.Description)
		row := CardResponse{Status: status, MediaTypes: []CardMediaType{}}
		if len(rc.Content) > 0 {
			row.MediaTypes = mediaTypesToCard(rc.Content, scrub, opts)
		}
		for _, hname := range sortedHeaderNames(rc.Headers) {
			h := rc.Headers[hname]
			row.Headers = append(row.Headers, CardParameter{
				Name:        hname,
				In:          "header",
				Required:    h.Required,
				Deprecated:  h.Deprecated,
				Type:        schemaTableType(h.Schema, h.SchemaRef),
				Description: scrub.String(h.Description),
			})
		}
		out = append(out, row)
	}
	return out, desc
}

func sortedHeaderNames(headers map[string]core.HeaderContract) []string {
	names := make([]string, 0, len(headers))
	for name := range headers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func responseSortKey(status string) int {
	switch {
	case status == "default":
		return 1 << 30
	case strings.HasSuffix(status, "XX"):
		if n := statusNumber(strings.TrimSuffix(status, "XX")); n > 0 {
			return 1<<20 + n
		}
		return 1<<29 - 1
	default:
		return statusNumber(status)
	}
}

func statusNumber(prefix string) int {
	if len(prefix) == 0 {
		return -1
	}
	n := 0
	for i := 0; i < len(prefix); i++ {
		c := prefix[i]
		if c < '0' || c > '9' {
			return -1
		}
		n = n*10 + int(c-'0')
		if n > 1<<20 {
			return -1
		}
	}
	return n
}

// --- schema summary -------------------------------------------------------

// schemaTableType renders the base type of a schema for a table cell.
func schemaTableType(schema map[string]any, ref string) string {
	if refName := schemaRefName(ref); refName != "" {
		return refName
	}
	if len(schema) == 0 {
		return "string"
	}
	if t, ok := schema["type"]; ok && t != nil {
		switch v := t.(type) {
		case string:
			return v
		case []any:
			parts := make([]string, 0, len(v))
			for _, e := range v {
				parts = append(parts, fmt.Sprintf("%v", e))
			}
			return strings.Join(parts, "|")
		}
	}
	switch {
	case schema["properties"] != nil:
		return "object"
	case schema["items"] != nil:
		return "array"
	}
	return "string"
}

// compactSchema summarizes a schema in one line, bounded in size; it never
// fabricates types that are not declared.
func compactSchema(schema map[string]any, ref string) string {
	if refName := schemaRefName(ref); refName != "" {
		s := "[" + refName + "]"
		if len(schema) == 0 {
			return s
		}
		return s + " " + inlineSchemaParts(schema)
	}
	if len(schema) == 0 {
		return ""
	}
	return inlineSchemaParts(schema)
}

func schemaRefName(ref string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return ""
	}
	if i := strings.LastIndexByte(ref, '/'); i >= 0 {
		return ref[i+1:]
	}
	return ref
}

const maxSchemaChildren = 12

func inlineSchemaParts(schema map[string]any) string {
	switch t := schema["type"].(type) {
	case nil:
		// OpenAPI 3.1 leaves `type` implicit; fall back to shape indicators.
		switch {
		case schema["properties"] != nil:
			return schemaObjectSummary(schema["properties"])
		case schema["items"] != nil:
			return schemaArraySummary(schema["items"])
		}
		return ""
	case string:
		switch t {
		case "array":
			if items, ok := schema["items"]; ok {
				return schemaArraySummary(items)
			}
			return "array"
		case "object":
			if props, ok := schema["properties"]; ok {
				return schemaObjectSummary(props)
			}
			return "object"
		}
		return t
	case []any:
		parts := make([]string, 0, len(t))
		for _, e := range t {
			parts = append(parts, fmt.Sprintf("%v", e))
		}
		return strings.Join(parts, "|")
	default:
		return fmt.Sprintf("%v", t)
	}
}

func schemaArraySummary(items any) string {
	if m, ok := items.(map[string]any); ok {
		return "array<" + compactSchema(m, "") + ">"
	}
	return "array"
}

func schemaObjectSummary(props any) string {
	m, ok := props.(map[string]any)
	if !ok {
		return "object"
	}
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}
	sort.Strings(names)
	shown := names
	if len(shown) > maxSchemaChildren {
		shown = shown[:maxSchemaChildren]
	}
	parts := make([]string, 0, len(shown))
	for _, name := range shown {
		typeName := "any"
		if pm, ok := m[name].(map[string]any); ok {
			if t := inlineSchemaParts(pm); t != "" {
				typeName = schemaTableType(pm, "")
			}
		}
		parts = append(parts, name+":"+typeName)
	}
	out := "object{" + strings.Join(parts, ",")
	if len(names) > len(shown) {
		out += fmt.Sprintf(",+%d", len(names)-len(shown))
	}
	return out + "}"
}

// --- example serialization --------------------------------------------------

// serializeExample scrubs v (under name for sensitive-name masking) and
// renders it into a bounded code block. hasExample=false means the value
// should not be published at all.
func serializeExample(name string, v any, scrub *Scrubber, opts CardOptions) (text, lang string, hasExample bool) {
	scrubbed := scrub.Value(v)
	lang = "text"
	switch {
	case isJSONish(name):
		lang = "json"
		b, err := json.MarshalIndent(scrubbed, "", "  ")
		if err != nil {
			return "", "", false
		}
		text = string(b)
	case isXMLish(name):
		lang = "xml"
		text = fmt.Sprintf("%v", scrubbed)
	default:
		switch t := scrubbed.(type) {
		case string:
			text = t
			lang = "text"
		default:
			b, err := yaml.Marshal(scrubbed)
			if err != nil {
				return "", "", false
			}
			text = strings.TrimRight(string(b), "\n")
			lang = "yaml"
		}
	}
	if len(text) > opts.exampleLimit() {
		text = text[:opts.exampleLimit()] + "\n…"
	}
	if lang == "json" || lang == "yaml" {
		text = sanitizeFenceBody(text)
	}
	return text, lang, text != ""
}

func isJSONish(name string) bool {
	base := strings.TrimSpace(strings.SplitN(name, ";", 2)[0])
	return base == "application/json" || strings.HasSuffix(base, "+json") || base == "json"
}

func isXMLish(name string) bool {
	base := strings.TrimSpace(strings.SplitN(name, ";", 2)[0])
	return base == "application/xml" || strings.HasSuffix(base, "+xml") || base == "xml"
}

// sanitizeFenceBody prevents example text from breaking out of the enclosing
// fenced code block.
func sanitizeFenceBody(s string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		t := strings.TrimLeft(line, " \t")
		if strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			lines[i] = strings.Repeat(" ", 4) + line
		}
	}
	return strings.Join(lines, "\n")
}

// --- markdown rendering -----------------------------------------------------

// MarkdownCell escapes one table cell or inline code boundary.
func MarkdownCell(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "|", "\\|")
	return strings.TrimSpace(s)
}

// WantsView reports whether the derived card should include the view section
// for the directive's parsed view tokens (empty means all sections).
func WantsView(views []string, view string) bool {
	if len(views) == 0 {
		return true
	}
	for _, v := range views {
		if v == view {
			return true
		}
	}
	return false
}

// BuildCardMarkdown renders card as the Markdown block inserted into a
// document. Output is deterministic for the same inputs; the first line is
// the card heading so the HTML export can anchor it.
func BuildCardMarkdown(card *RequestCard, views []string) string {
	labels := card.labels
	var b strings.Builder
	writeCardHeading(&b, card)
	writeCardBasics(&b, card)
	if WantsView(views, ViewParameters) {
		writeCardParameters(&b, card, labels)
		writeCardBody(&b, card, labels)
	}
	if WantsView(views, ViewResponses) {
		writeCardResponses(&b, card, labels)
	}
	writeCardNotes(&b, card, labels)
	fmt.Fprintf(&b, "[%s](%s%s)\n", fmt.Sprintf(labels.OpenRequest, card.ID), RefLinkPrefix, card.ID)
	return strings.TrimRight(b.String(), "\n") + "\n"
}

func writeCardHeading(b *strings.Builder, card *RequestCard) {
	labels := card.labels
	var title string
	if card.NonHTTP {
		title = fmt.Sprintf("`%s` — %s", card.ID, card.Name)
	} else {
		title = fmt.Sprintf("`%s` — %s %s", card.ID, card.Method, card.displayPath())
		if card.Deprecated {
			title += " (" + labels.Deprecated + ")"
		}
	}
	b.WriteString("#### " + title + "\n\n")
}

func (c *RequestCard) displayPath() string {
	if c.PathTemplate != "" {
		return c.PathTemplate
	}
	return c.URL
}

func writeCardBasics(b *strings.Builder, card *RequestCard) {
	labels := card.labels
	if card.NonHTTP {
		fmt.Fprintf(b, "- %s: `%s`\n", labels.Protocol(), MarkdownCell(card.Protocol))
		fmt.Fprintf(b, "\n%s\n\n", fmt.Sprintf(labels.NonHTTPNote, card.Protocol))
		return
	}
	fmt.Fprintf(b, "- %s: `%s`\n", labels.Method, MarkdownCell(card.Method))
	fmt.Fprintf(b, "- %s: `%s`\n", labels.URL, MarkdownCell(card.URL))
	if card.PathTemplate != "" && card.PathTemplate != card.URL {
		fmt.Fprintf(b, "- %s: `%s`\n", labels.PathTemplate, MarkdownCell(card.PathTemplate))
	}
	if card.Auth != "" {
		fmt.Fprintf(b, "- %s: %s\n", labels.Auth, MarkdownCell(card.Auth))
	}
	if card.Summary != "" {
		fmt.Fprintf(b, "\n%s\n", card.Summary)
	}
	if card.Description != "" {
		fmt.Fprintf(b, "\n%s\n", card.Description)
	}
	b.WriteString("\n")
}

func writeCardParameters(b *strings.Builder, card *RequestCard, labels CardLabels) {
	fmt.Fprintf(b, "**%s**\n\n", labels.Parameters)
	if len(card.Parameters) == 0 {
		fmt.Fprintf(b, "%s\n\n", labels.ParamNone)
		return
	}
	fmt.Fprintf(b, "| %s | %s | %s | %s | %s |\n", labels.ParamName, labels.ParamIn, labels.ParamRequired, labels.ParamType, labels.ParamDesc)
	b.WriteString("| --- | --- | --- | --- | --- |\n")
	for _, p := range card.Parameters {
		required := "-"
		if p.Required {
			required = "✓"
		}
		desc := p.Description
		if p.HasExample && p.Example != nil {
			if s := formatScalar(p.Example); s != "" {
				if len(s) > 80 {
					s = s[:80] + "…"
				}
				if desc != "" {
					desc += " · "
				}
				desc += "e.g. `" + s + "`"
			}
		}
		fmt.Fprintf(b, "| %s | %s | %s | %s | %s |\n",
			MarkdownCell(p.Name), MarkdownCell(p.In), required, MarkdownCell(p.Type), MarkdownCell(desc))
	}
	b.WriteString("\n")
}

func writeCardBody(b *strings.Builder, card *RequestCard, labels CardLabels) {
	body := card.Body
	if body == nil || len(body.MediaTypes) == 0 {
		return
	}
	fmt.Fprintf(b, "**%s**\n\n", labels.RequestBody)
	if body.Required {
		fmt.Fprintf(b, "- %s: ✓\n", labels.ParamRequired)
	}
	for _, mt := range body.MediaTypes {
		entry := fmt.Sprintf("- `%s`", MarkdownCell(mt.Name))
		if mt.Schema != "" {
			entry += " — `" + MarkdownCell(mt.Schema) + "`"
		}
		b.WriteString(entry + "\n")
		if mt.HasExample {
			writeExample(b, labels, mt)
		}
	}
	b.WriteString("\n")
}

func writeCardResponses(b *strings.Builder, card *RequestCard, labels CardLabels) {
	fmt.Fprintf(b, "**%s**\n\n", labels.Responses)
	if len(card.Responses) == 0 {
		fmt.Fprintf(b, "%s\n\n", labels.NoResponses)
		// Still record the missing-definition fact in notes below.
		return
	}
	fmt.Fprintf(b, "| %s | %s | %s |\n", labels.Status, labels.Content, labels.ParamDesc)
	b.WriteString("| --- | --- | --- |\n")
	for _, r := range card.Responses {
		content := []string{}
		for _, mt := range r.MediaTypes {
			name := MarkdownCell(mt.Name)
			if mt.Schema != "" {
				name += " `" + MarkdownCell(mt.Schema) + "`"
			}
			content = append(content, name)
		}
		fmt.Fprintf(b, "| %s | %s | %s |\n",
			MarkdownCell(r.Status), MarkdownCell(strings.Join(content, "; ")), MarkdownCell(card.responseDesc[r.Status]))
	}
	b.WriteString("\n")
	for _, r := range card.Responses {
		for _, mt := range r.MediaTypes {
			if mt.HasExample {
				fmt.Fprintf(b, "**%s %s** — `%s`\n\n", labels.Examples, MarkdownCell(r.Status), MarkdownCell(mt.Name))
				writeExample(b, labels, mt)
			}
		}
		if len(r.Headers) > 0 {
			fmt.Fprintf(b, "**%s (%s)**\n\n", labels.RespHeaders, MarkdownCell(r.Status))
			fmt.Fprintf(b, "| %s | %s | %s |\n", labels.ParamName, labels.ParamType, labels.ParamDesc)
			b.WriteString("| --- | --- | --- |\n")
			for _, h := range r.Headers {
				fmt.Fprintf(b, "| %s | %s | %s |\n", MarkdownCell(h.Name), MarkdownCell(h.Type), MarkdownCell(h.Description))
			}
			b.WriteString("\n")
		}
	}
}

func writeExample(b *strings.Builder, labels CardLabels, mt CardMediaType) {
	name := ""
	if mt.ExampleName != "" {
		name = " (" + MarkdownCell(mt.ExampleName) + ")"
	}
	fmt.Fprintf(b, "%s%s:\n\n", labels.Examples, name)
	fmt.Fprintf(b, "```%s\n%s\n```\n\n", mt.ExampleLang, mt.Example)
}

func writeCardNotes(b *strings.Builder, card *RequestCard, labels CardLabels) {
	var notes []string
	if !card.HasContract {
		notes = append(notes, labels.NoContract)
	}
	if card.Scrub.Masked > 0 || card.Scrub.Blocked > 0 {
		notes = append(notes, fmt.Sprintf(labels.ScrubbedNote, card.Scrub.Masked, card.Scrub.Blocked))
	}
	if card.Scrub.SecretTemplateRefs > 0 {
		notes = append(notes, labels.TemplateVars)
	}
	for _, n := range notes {
		fmt.Fprintf(b, "> %s\n\n", MarkdownCell(n))
	}
}

// Protocol label helper: CardLabels has Interface/Method but no Protocol;
// reuse Interface for the non-HTTP protocol row. The method value keeps gofmt
// object literal alignment stable.
func (l CardLabels) Protocol() string { return l.Interface }

func formatScalar(v any) string {
	switch t := v.(type) {
	case nil:
		return "null"
	case string:
		return t
	case bool:
		return fmt.Sprintf("%v", t)
	case int, int64, float64, float32:
		return fmt.Sprintf("%v", t)
	case map[string]any, []any:
		b, err := json.Marshal(t)
		if err != nil {
			return "…"
		}
		return string(b)
	default:
		return fmt.Sprintf("%v", t)
	}
}
