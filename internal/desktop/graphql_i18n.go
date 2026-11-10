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
	SectionSchema    string
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

	// Schema explorer and local SDL.
	SchemaLoad                string
	SchemaIntrospect          string
	SchemaIntrospectCancel    string
	SchemaIntrospectTitle     string
	SchemaIntrospectSave      string
	SchemaIntrospectHint      string
	SchemaIntrospectRunning   string
	SchemaIntrospectFailedFmt string
	SchemaPathPlaceholder     string
	SchemaSearchPlaceholder   string
	SchemaEmpty               string
	SchemaLocalOnly           string
	SchemaLoadedFmt           string
	SchemaSavedFmt            string
	SchemaInsertedFmt         string
	SchemaDigestChangedFmt    string
	SchemaNoProject           string
	SchemaNoURL               string
	SchemaFieldsLabel         string
	SchemaArgsLabel           string
	SchemaEnumLabel           string
	SchemaDeprecatedFmt       string
	SchemaDiagnosticsFmt      string
	SchemaInsertField         string

	// graphql-transport-ws subscription.
	SubNoProject          string
	SubSelectSubscription string
	SubNoDocument         string
	SubResolveFailedFmt   string
	SubNoURL              string
	SubBadTimeout         string
	SubConnecting         string
	SubOpen               string
	SubCompleted          string
	SubCanceled           string
	SubConfirmNeeded      string
	SubConnectFailedFmt   string
	SubErrorFmt           string
	SubTitle              string
	SubCancelBtn          string
	SubStartHint          string
	SubTruncatedFmt       string
	SubNoMessages         string
	SubConfirmCheck       string
	SubCloseFmt           string
}

var gqlMessagesEn = gqlMessages{
	NewGraphQL:            "New GraphQL",
	NewGraphQLDefaultName: "GraphQL",
	TabBadge:              "GQL",

	SectionDocument:  "Document",
	SectionVariables: "Variables",
	SectionSchema:    "Schema",
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

	SchemaLoad:                "Load SDL…",
	SchemaIntrospect:          "Introspect…",
	SchemaIntrospectCancel:    "Cancel introspection",
	SchemaIntrospectTitle:     "Introspection preview",
	SchemaIntrospectSave:      "Save to project",
	SchemaIntrospectHint:      "Introspection runs only when you click it, through the request's auth/network settings. Nothing is saved until you confirm.",
	SchemaIntrospectRunning:   "Introspecting…",
	SchemaIntrospectFailedFmt: "Introspection failed: %s",
	SchemaPathPlaceholder:     "No local SDL loaded",
	SchemaSearchPlaceholder:   "Filter types",
	SchemaEmpty:               "Load a local .graphql SDL file to validate offline and explore the schema.",
	SchemaLocalOnly:           "Loaded from a file outside .gorequest; the reference is not saved into the project.",
	SchemaLoadedFmt:           "Loaded %s",
	SchemaSavedFmt:            "Introspection saved to %s",
	SchemaInsertedFmt:         "Inserted %s",
	SchemaDigestChangedFmt:    "Schema changed on disk (saved digest %s, current %s); validation uses the current file.",
	SchemaNoProject:           "No project is open.",
	SchemaNoURL:               "Enter the GraphQL endpoint first.",
	SchemaFieldsLabel:         "Fields",
	SchemaArgsLabel:           "Arguments",
	SchemaEnumLabel:           "Values",
	SchemaDeprecatedFmt:       "deprecated: %s",
	SchemaDiagnosticsFmt:      "%d schema diagnostic(s)",
	SchemaInsertField:         "Insert",

	SubNoProject:          "No project is open.",
	SubSelectSubscription: "Select a subscription operation before sending.",
	SubNoDocument:         "The GraphQL document is empty.",
	SubResolveFailedFmt:   "Variable resolution failed: %s",
	SubNoURL:              "Enter the GraphQL endpoint first.",
	SubBadTimeout:         "The total timeout is not a valid duration.",
	SubConnecting:         "Connecting subscription…",
	SubOpen:               "Subscription open",
	SubCompleted:          "Subscription completed by server",
	SubCanceled:           "Subscription canceled",
	SubConfirmNeeded:      "This target needs interactive confirmation.",
	SubConnectFailedFmt:   "Subscription failed: %s",
	SubErrorFmt:           "Subscription error: %s",
	SubTitle:              "Subscription events",
	SubCancelBtn:          "Cancel subscription",
	SubStartHint:          "Send starts the selected subscription over graphql-transport-ws.",
	SubTruncatedFmt:       "Showing the latest %d events.",
	SubNoMessages:         "Waiting for events…",
	SubConfirmCheck:       "I confirm this target may be contacted",
	SubCloseFmt:           "Transport closed: %d %s",
}

var gqlMessagesZh = gqlMessages{
	NewGraphQL:            "新建 GraphQL",
	NewGraphQLDefaultName: "GraphQL",
	TabBadge:              "GQL",

	SectionDocument:  "文档",
	SectionVariables: "变量",
	SectionSchema:    "Schema",
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

	SchemaLoad:                "加载 SDL…",
	SchemaIntrospect:          "Introspection…",
	SchemaIntrospectCancel:    "取消 Introspection",
	SchemaIntrospectTitle:     "Introspection 预览",
	SchemaIntrospectSave:      "保存到项目",
	SchemaIntrospectHint:      "仅在点击时通过请求的鉴权/网络设置执行 Introspection；确认前不保存任何内容。",
	SchemaIntrospectRunning:   "正在 Introspection…",
	SchemaIntrospectFailedFmt: "Introspection 失败：%s",
	SchemaPathPlaceholder:     "未加载本地 SDL",
	SchemaSearchPlaceholder:   "筛选类型",
	SchemaEmpty:               "加载本地 .graphql SDL 文件后可离线校验并浏览 Schema。",
	SchemaLocalOnly:           "文件位于 .gorequest 之外，引用不会写入项目。",
	SchemaLoadedFmt:           "已加载 %s",
	SchemaSavedFmt:            "Introspection 已保存到 %s",
	SchemaInsertedFmt:         "已插入 %s",
	SchemaDigestChangedFmt:    "磁盘上的 Schema 已变化（保存摘要 %s，当前 %s）；校验使用当前文件。",
	SchemaNoProject:           "未打开项目。",
	SchemaNoURL:               "请先填写 GraphQL 端点。",
	SchemaFieldsLabel:         "字段",
	SchemaArgsLabel:           "参数",
	SchemaEnumLabel:           "枚举值",
	SchemaDeprecatedFmt:       "已弃用：%s",
	SchemaDiagnosticsFmt:      "%d 个 Schema 诊断",
	SchemaInsertField:         "插入",

	SubNoProject:          "未打开项目。",
	SubSelectSubscription: "发送前请选择 subscription 操作。",
	SubNoDocument:         "GraphQL 文档为空。",
	SubResolveFailedFmt:   "变量解析失败：%s",
	SubNoURL:              "请先填写 GraphQL 端点。",
	SubBadTimeout:         "总超时不是有效时长。",
	SubConnecting:         "正在连接订阅…",
	SubOpen:               "订阅已建立",
	SubCompleted:          "服务端已结束订阅",
	SubCanceled:           "订阅已取消",
	SubConfirmNeeded:      "该目标需要交互确认。",
	SubConnectFailedFmt:   "订阅失败：%s",
	SubErrorFmt:           "订阅错误：%s",
	SubTitle:              "订阅事件",
	SubCancelBtn:          "取消订阅",
	SubStartHint:          "点击发送将通过 graphql-transport-ws 启动所选订阅。",
	SubTruncatedFmt:       "仅显示最近 %d 个事件。",
	SubNoMessages:         "等待事件…",
	SubConfirmCheck:       "我确认可以访问该目标",
	SubCloseFmt:           "传输已关闭：%d %s",
}

// gqlMsg returns the active-language GraphQL workspace messages.
func (a *App) gqlMsg() *gqlMessages {
	if a != nil && a.mgr != nil && a.mgr.cfg != nil && parseLanguage(string(a.mgr.cfg.language())) == langEnglish {
		return &gqlMessagesEn
	}
	return &gqlMessagesZh
}
