package desktop

// runnerEditorMessages is the assertion/extraction editor's bilingual message
// set, following the runnerMessages pattern: full structs per language, no
// format machinery, so missing keys surface at compile time.
type runnerEditorMessages struct {
	Title, Close, Save, Reload                              string
	Request, NoRequest                                      string
	AssertionSection, ExtractionSection                     string
	NewAssertion, NewExtraction                             string
	EditButton, DeleteButton, ApplyButton, CancelEditButton string
	AddMatcher, MatchersLabel                               string
	MessageLabel, HeaderLabel, JSONPathLabel, BodyLabel     string
	StatusLabel, StatusEqualsLabel, StatusInLabel           string
	PreservedHint, InvalidHint                              string
	DirtyTabsHint                                           string
	SuiteTargetLabel, NoSuiteTarget                         string
	SuitesSkipped                                           string
	ExtractNameLabel, ExtractFromLabel, ExtractPathLabel    string
	ExtractHeaderLabel, ExtractCookieLabel                  string
	ExtractRegexLabel, ExtractGroupLabel                    string
	ExtractRequiredLabel, ExtractSensitiveLabel             string
	InHint                                                  string
	TargetStatus, TargetHeader, TargetJSONPath, TargetBody  string
	TargetDuration, TargetSize, TargetSchema                string
	PendingEdits, PendingForm                               string
	Saved, SaveConflict, SuiteChanged, NothingToSave        string
	DirtyTabs                                               string
	SensitiveBadge, RequiredBadge                           string
	RunnerConnectHint, SecretRefHint                        string
	MatchEquals, MatchNotEquals, MatchExists                string
	MatchIn, MatchContains, MatchNotContains                string
	MatchRegex, MatchType                                   string
	LitKindLabel                                            string
	UnknownTarget                                           string
}

var runnerEditorEN = runnerEditorMessages{
	Title:                 "Assertions & Extraction Editor",
	Close:                 "Close",
	Save:                  "Save",
	Reload:                "Discard edits & reload",
	Request:               "Request",
	NoRequest:             "No saved request is open in this project.",
	AssertionSection:      "Assertions",
	ExtractionSection:     "Extractions",
	NewAssertion:          "New assertion",
	NewExtraction:         "New extraction",
	EditButton:            "Edit",
	DeleteButton:          "Delete",
	ApplyButton:           "Apply",
	CancelEditButton:      "Cancel edit",
	AddMatcher:            "Add matcher",
	MatchersLabel:         "Matchers",
	MessageLabel:          "message",
	HeaderLabel:           "header name",
	JSONPathLabel:         "JSONPath",
	BodyLabel:             "body",
	StatusLabel:           "status",
	StatusEqualsLabel:     "equals code",
	StatusInLabel:         "in codes",
	PreservedHint:         "Preserved verbatim: this target is not structurally editable in this phase; it runs unchanged. Delete removes it explicitly.",
	InvalidHint:           "This assertion cannot be parsed and will make runs fail preflight; fix or delete it.",
	DirtyTabsHint:         "This request has unsaved tab edits. Their later save will report a conflict on purpose instead of overwriting.",
	SuiteTargetLabel:      "Suite step",
	NoSuiteTarget:         "No saved suite step references this request. Extraction rules live in suite steps (request: <path or id>); create one in a .suite.yaml to edit extractions here.",
	SuitesSkipped:         "%d suite file(s) unreadable or invalid were skipped.",
	ExtractNameLabel:      "output name",
	ExtractFromLabel:      "from",
	ExtractPathLabel:      "path",
	ExtractHeaderLabel:    "header",
	ExtractCookieLabel:    "cookie",
	ExtractRegexLabel:     "regex",
	ExtractGroupLabel:     "group",
	ExtractRequiredLabel:  "required",
	ExtractSensitiveLabel: "sensitive",
	InHint:                "comma-separated values; numbers/true/false/null are typed, all else string",
	TargetStatus:          "status",
	TargetHeader:          "header",
	TargetJSONPath:        "jsonpath",
	TargetBody:            "body",
	TargetDuration:        "durationMs",
	TargetSize:            "size",
	TargetSchema:          "schema",
	PendingEdits:          "There are unsaved edits: save, or discard them with the reload button first.",
	PendingForm:           "Apply or cancel the inline edit first.",
	Saved:                 "Saved.",
	SaveConflict:          "Save conflict: the file changed on disk meanwhile. Reload the editor (discarding drafts) and redo your edit.",
	SuiteChanged:          "The suite changed on disk since it was loaded; reload the editor and redo your edit.",
	NothingToSave:         "Nothing to save.",
	DirtyTabs:             "Saved. %d tab(s) with unsaved edits keep their edits and will hit an explicit conflict on save.",
	SensitiveBadge:        "sensitive",
	RequiredBadge:         "required",
	RunnerConnectHint:     "Saved rules are read by the collection/Suite runner at the next run; a running snapshot is not modified.",
	SecretRefHint:         "Rule values keep {{secret.name}} references; this editor never reads or writes secret values.",
	MatchEquals:           "equals",
	MatchNotEquals:        "notEquals",
	MatchExists:           "exists",
	MatchIn:               "in",
	MatchContains:         "contains",
	MatchNotContains:      "notContains",
	MatchRegex:            "regex",
	MatchType:             "type",
	LitKindLabel:          "type",
	UnknownTarget:         "unknown",
}

var runnerEditorZH = runnerEditorMessages{
	Title:                 "断言 / 提取编辑器",
	Close:                 "关闭",
	Save:                  "保存",
	Reload:                "放弃编辑并重载",
	Request:               "请求",
	NoRequest:             "当前项目没有已保存的请求。",
	AssertionSection:      "断言",
	ExtractionSection:     "提取",
	NewAssertion:          "新增断言",
	NewExtraction:         "新增提取",
	EditButton:            "编辑",
	DeleteButton:          "删除",
	ApplyButton:           "应用",
	CancelEditButton:      "取消编辑",
	AddMatcher:            "添加匹配器",
	MatchersLabel:         "匹配器",
	MessageLabel:          "message",
	HeaderLabel:           "header 名称",
	JSONPathLabel:         "JSONPath",
	BodyLabel:             "body",
	StatusLabel:           "status",
	StatusEqualsLabel:     "equals 状态码",
	StatusInLabel:         "in 状态码",
	PreservedHint:         "原样保留：本阶段不支持该目标的结构化编辑，保存时按原样运行；删除是显式操作。",
	InvalidHint:           "该断言无法解析，运行预检会失败；请修正或删除。",
	DirtyTabsHint:         "该请求存在含未保存编辑的标签页；之后保存会显式报告冲突，不会覆盖此处结果。",
	SuiteTargetLabel:      "Suite 步骤",
	NoSuiteTarget:         "没有已保存 Suite 步骤引用此请求。提取规则保存在 Suite 步骤（request: <路径或 id>）；请先在 .suite.yaml 中创建步骤，再在此编辑提取。",
	SuitesSkipped:         "%d 个 Suite 文件不可读或校验失败，已跳过。",
	ExtractNameLabel:      "输出名",
	ExtractFromLabel:      "来源 from",
	ExtractPathLabel:      "path",
	ExtractHeaderLabel:    "header",
	ExtractCookieLabel:    "cookie",
	ExtractRegexLabel:     "regex",
	ExtractGroupLabel:     "group",
	ExtractRequiredLabel:  "required",
	ExtractSensitiveLabel: "sensitive",
	InHint:                "逗号分隔；数字/true/false/null 按类型识别，其余按字符串",
	TargetStatus:          "status",
	TargetHeader:          "header",
	TargetJSONPath:        "jsonpath",
	TargetBody:            "body",
	TargetDuration:        "durationMs",
	TargetSize:            "size",
	TargetSchema:          "schema",
	PendingEdits:          "存在未保存的编辑：请先保存，或用重载按钮放弃后切换。",
	PendingForm:           "请先应用或取消正在进行的行内编辑。",
	Saved:                 "已保存。",
	SaveConflict:          "保存冲突：文件在磁盘上已被修改。请重载编辑器（放弃草稿）后重新编辑。",
	SuiteChanged:          "Suite 在加载后发生了变化；请重载编辑器后重新编辑。",
	NothingToSave:         "没有需要保存的修改。",
	DirtyTabs:             "已保存。%d 个含未保存编辑的标签页保持原编辑，之后保存会显式报告冲突。",
	SensitiveBadge:        "敏感",
	RequiredBadge:         "必需",
	RunnerConnectHint:     "保存的规则在下次集合 / Suite 运行时读取；运行中的快照不受影响。",
	SecretRefHint:         "规则值保持 {{secret.name}} 引用；本编辑器不读取也不写入秘密值。",
	MatchEquals:           "equals",
	MatchNotEquals:        "notEquals",
	MatchExists:           "exists",
	MatchIn:               "in",
	MatchContains:         "contains",
	MatchNotContains:      "notContains",
	MatchRegex:            "regex",
	MatchType:             "type",
	LitKindLabel:          "类型",
	UnknownTarget:         "未知",
}

func (a *App) runnerEditorMsg() runnerEditorMessages {
	if a != nil && a.mgr != nil && a.mgr.cfg != nil && parseLanguage(string(a.mgr.cfg.language())) == langSimplifiedChinese {
		return runnerEditorZH
	}
	return runnerEditorEN
}
