package desktop

// graphql_i18n.go owns the GraphQL workspace's bilingual messages. It stays
// separate from the global catalog so the new protocol cannot disturb existing
// HTTP/WebSocket wording.

type gqlMessages struct {
	NewGraphQL            string
	NewGraphQLDefaultName string
	TabBadge              string

	SectionDocument  string
	SectionVariables string
	SectionHeaders   string
	SectionAuth      string

	MethodLabel          string
	OperationLabel       string
	OperationPlaceholder string
	OperationHint        string
	DocumentHint         string
	VariablesHint        string
	ParseOperationsFmt   string
	ParseSelectedFmt     string
	ParseErrorFmt        string

	ResultData    string
	ResultErrors  string
	ResultRaw     string
	ResultHeaders string

	DataNull       string
	DataAbsent     string
	NoErrors       string
	PartialSuccess string
	GraphQLErrors  string
	HTTPError      string
	InvalidJSON    string
	Truncated      string
	StatusFmt      string

	ErrorPath       string
	ErrorLocations  string
	ErrorExtensions string

	DataTooLargeFmt    string
	ErrorsTruncatedFmt string
}

var gqlMessagesEn = gqlMessages{
	NewGraphQL:            "New GraphQL",
	NewGraphQLDefaultName: "GraphQL",
	TabBadge:              "GQL",

	SectionDocument:  "Document",
	SectionVariables: "Variables",
	SectionHeaders:   "Headers",
	SectionAuth:      "Auth",

	MethodLabel:          "Method",
	OperationLabel:       "Operation",
	OperationPlaceholder: "operationName (required when the document has several)",
	OperationHint:        "A multi-operation document requires an explicit operationName.",
	DocumentHint:         "GraphQL document. The text is sent exactly as written; templates are not expanded here.",
	VariablesHint:        "JSON object. Templates are resolved only inside string values; numbers, booleans and null keep their type.",
	ParseOperationsFmt:   "%d operation(s) parsed",
	ParseSelectedFmt:     "Selected: %s %s",
	ParseErrorFmt:        "Document error: %s",

	ResultData:    "Data",
	ResultErrors:  "Errors",
	ResultRaw:     "Raw",
	ResultHeaders: "Headers",

	DataNull:       "data is null",
	DataAbsent:     "no data field",
	NoErrors:       "No GraphQL errors.",
	PartialSuccess: "Partial success: data and errors are both present",
	GraphQLErrors:  "GraphQL errors",
	HTTPError:      "HTTP error",
	InvalidJSON:    "Response is not a valid GraphQL JSON body",
	Truncated:      "Response truncated",
	StatusFmt:      "HTTP %d · %s · %s",

	ErrorPath:       "path",
	ErrorLocations:  "locations",
	ErrorExtensions: "extensions",

	DataTooLargeFmt:    "data is %s; showing the first 512 KiB. Use Raw for the full body.",
	ErrorsTruncatedFmt: "Showing the first %d errors.",
}

var gqlMessagesZh = gqlMessages{
	NewGraphQL:            "新建 GraphQL",
	NewGraphQLDefaultName: "GraphQL",
	TabBadge:              "GQL",

	SectionDocument:  "文档",
	SectionVariables: "变量",
	SectionHeaders:   "Headers",
	SectionAuth:      "鉴权",

	MethodLabel:          "方法",
	OperationLabel:       "Operation",
	OperationPlaceholder: "operationName（文档含多个操作时必填）",
	OperationHint:        "含多个 Operation 的文档必须显式选择 operationName。",
	DocumentHint:         "GraphQL 文档按原文发送，不在此处展开模板。",
	VariablesHint:        "JSON 对象。仅解析字符串值中的模板；数字、布尔与 null 保持原类型。",
	ParseOperationsFmt:   "已解析 %d 个 Operation",
	ParseSelectedFmt:     "已选择：%s %s",
	ParseErrorFmt:        "文档错误：%s",

	ResultData:    "Data",
	ResultErrors:  "Errors",
	ResultRaw:     "原始",
	ResultHeaders: "Headers",

	DataNull:       "data 为 null",
	DataAbsent:     "无 data 字段",
	NoErrors:       "无 GraphQL errors。",
	PartialSuccess: "部分成功：data 与 errors 同时存在",
	GraphQLErrors:  "GraphQL 错误",
	HTTPError:      "HTTP 错误",
	InvalidJSON:    "响应不是有效的 GraphQL JSON",
	Truncated:      "响应已截断",
	StatusFmt:      "HTTP %d · %s · %s",

	ErrorPath:       "path",
	ErrorLocations:  "locations",
	ErrorExtensions: "extensions",

	DataTooLargeFmt:    "data 为 %s；仅显示前 512 KiB，完整正文请查看“原始”。",
	ErrorsTruncatedFmt: "仅显示前 %d 个错误。",
}

// gqlMsg returns the active-language GraphQL workspace messages.
func (a *App) gqlMsg() *gqlMessages {
	if a != nil && a.mgr != nil && a.mgr.cfg != nil && parseLanguage(string(a.mgr.cfg.language())) == langEnglish {
		return &gqlMessagesEn
	}
	return &gqlMessagesZh
}
