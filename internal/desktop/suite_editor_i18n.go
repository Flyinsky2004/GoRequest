package desktop

// Suite editor messages (A02 phase 3). They live in their own file so the
// shared i18n.go stays owned by the integrator.

type suiteEditorMessages struct {
	Title, OpenEntry, NewSuite, SavedSuites, EmptySuites, SuitesSkipped, NewDraftHint  string
	BasicSection, IDLabel, NameLabel, OnFailureLabel, OnFailureStop, OnFailureContinue string
	ReadonlyIDHint                                                                     string
	StepsSection, AddStep, NoSteps, StepIDLabel, StepNameLabel, RequestLabel           string
	SelectRequest, NoRequests, NeedsLabel, NeedsHint, MoveUp, MoveDown, DeleteStep     string
	PreservedLabel, PreservedHint, SelectStepHint, ReadonlyStepID, StepCount           string
	SuitePreservedLabel, SuitePreservedHint, OpenAssertionEditorHint                   string
	DatasetSection, DatasetNone, DatasetCSV, DatasetJSON, DatasetDisabledHint          string
	DatasetFileLabel, DatasetMaxRowsLabel, DatasetMaxBytesLabel, DatasetDelimiterLabel string
	PreviewButton, PreviewFailed, PreviewHint, PreviewRowsLabel, PreviewTruncated      string
	PreviewLimits, PreviewSensitive                                                    string
	Save, Cancel, Close, Discard, KeepEditing, DiscardTitle, DiscardBody               string
	Saved, SaveConflict, NothingToSave, InvalidDraft, RequestUnavailable               string
}

var suiteEditorEN = suiteEditorMessages{
	Title:                   "Suite editor",
	OpenEntry:               "Edit suites…",
	NewSuite:                "New suite",
	SavedSuites:             "Saved suites",
	EmptySuites:             "No saved suites yet.",
	SuitesSkipped:           "%d more suite files are not listed (scan limit).",
	NewDraftHint:            "New suite: choose an id, add steps and a request per step, then Save.",
	BasicSection:            "Basics",
	IDLabel:                 "Suite id",
	NameLabel:               "Name",
	OnFailureLabel:          "On failure",
	OnFailureStop:           "stop",
	OnFailureContinue:       "continue",
	ReadonlyIDHint:          "A saved suite keeps its id; create a new suite to change it.",
	StepsSection:            "Steps",
	AddStep:                 "Add step",
	NoSteps:                 "No steps yet. A suite needs at least one step.",
	StepIDLabel:             "Step id",
	StepNameLabel:           "Step name",
	RequestLabel:            "Request",
	SelectRequest:           "Choose a saved HTTP request…",
	NoRequests:              "No saved HTTP requests are available.",
	NeedsLabel:              "Needs (dependencies)",
	NeedsHint:               "Dependencies must exist and must not form a cycle. Declaration order only breaks ties.",
	MoveUp:                  "Up",
	MoveDown:                "Down",
	DeleteStep:              "Delete",
	PreservedLabel:          "assert %d · extract %d · before %d · after %d · retry %v",
	PreservedHint:           "Assertions, extractions, actions and retry are preserved verbatim; edit them in the assertion/extraction editor.",
	SelectStepHint:          "Select a step to edit its request and dependencies.",
	ReadonlyStepID:          "A saved step id is read-only in this phase (renaming would break needs and {{steps.id.*}} references).",
	StepCount:               "%d step(s)",
	SuitePreservedLabel:     "variables %d · suite before %d · suite after %d",
	SuitePreservedHint:      "Suite variables and before/after actions are preserved verbatim.",
	OpenAssertionEditorHint: "Opens the existing Runner editor for this step's request. The suite draft stays in memory and resumes when you reopen this editor; that editor never writes this draft.",
	DatasetSection:          "Dataset",
	DatasetNone:             "none",
	DatasetCSV:              "CSV",
	DatasetJSON:             "JSON",
	DatasetDisabledHint:     "No dataset: the suite runs once.",
	DatasetFileLabel:        "Project-relative file",
	DatasetMaxRowsLabel:     "maxRows",
	DatasetMaxBytesLabel:    "maxBytes",
	DatasetDelimiterLabel:   "CSV delimiter",
	PreviewButton:           "Preview",
	PreviewFailed:           "Preview failed",
	PreviewHint:             "Preview reads at most 20 rows / 256 KiB. Execution applies maxRows/maxBytes and validates the whole file.",
	PreviewRowsLabel:        "Preview rows: %d",
	PreviewTruncated:        "Preview is truncated; execution still validates the whole dataset.",
	PreviewLimits:           "Execution maxRows %d / maxBytes %d.",
	PreviewSensitive:        "Sensitive columns are masked: %s",
	Save:                    "Save",
	Cancel:                  "Cancel",
	Close:                   "Close",
	Discard:                 "Discard changes",
	KeepEditing:             "Keep editing",
	DiscardTitle:            "Discard unsaved changes?",
	DiscardBody:             "This draft has unsaved edits. Discarding writes nothing to disk.",
	Saved:                   "Suite saved.",
	SaveConflict:            "The file changed on disk. Reload it before saving; your draft is kept.",
	NothingToSave:           "Nothing to save.",
	InvalidDraft:            "Fix the errors before saving.",
	RequestUnavailable:      "Unavailable request: %s",
}

var suiteEditorZH = suiteEditorMessages{
	Title:                   "Suite 编排",
	OpenEntry:               "编辑 Suite…",
	NewSuite:                "新建 Suite",
	SavedSuites:             "已保存 Suite",
	EmptySuites:             "还没有已保存的 Suite。",
	SuitesSkipped:           "另有 %d 个 Suite 文件未列出（扫描上限）。",
	NewDraftHint:            "新建 Suite：填写 ID、添加步骤并为每步选择请求，然后保存。",
	BasicSection:            "基础信息",
	IDLabel:                 "Suite ID",
	NameLabel:               "名称",
	OnFailureLabel:          "失败策略",
	OnFailureStop:           "停止",
	OnFailureContinue:       "继续",
	ReadonlyIDHint:          "已保存 Suite 的 ID 只读；如需修改请新建 Suite。",
	StepsSection:            "步骤",
	AddStep:                 "添加步骤",
	NoSteps:                 "还没有步骤。Suite 至少需要一个步骤。",
	StepIDLabel:             "步骤 ID",
	StepNameLabel:           "步骤名称",
	RequestLabel:            "请求",
	SelectRequest:           "选择已保存的 HTTP 请求…",
	NoRequests:              "没有可用的已保存 HTTP 请求。",
	NeedsLabel:              "依赖（needs）",
	NeedsHint:               "依赖必须存在且不能成环；声明顺序只用于同就绪级排序。",
	MoveUp:                  "上移",
	MoveDown:                "下移",
	DeleteStep:              "删除",
	PreservedLabel:          "断言 %d · 提取 %d · 前置 %d · 后置 %d · 重试 %v",
	PreservedHint:           "断言、提取、动作与重试原样保留；请在断言/提取编辑器中修改。",
	SelectStepHint:          "选择一个步骤以编辑其请求和依赖。",
	ReadonlyStepID:          "本阶段已保存步骤的 ID 只读（重命名会破坏 needs 与 {{steps.id.*}} 引用）。",
	StepCount:               "%d 个步骤",
	SuitePreservedLabel:     "变量 %d · Suite 前置 %d · Suite 后置 %d",
	SuitePreservedHint:      "Suite 变量与前后置动作原样保留。",
	OpenAssertionEditorHint: "打开现有 Runner 编辑器编辑该步骤的请求；本 Suite 草稿保留在内存，重新打开本编辑器即可继续，该编辑器不会写入本草稿。",
	DatasetSection:          "数据源",
	DatasetNone:             "无",
	DatasetCSV:              "CSV",
	DatasetJSON:             "JSON",
	DatasetDisabledHint:     "未配置数据源：Suite 只运行一次。",
	DatasetFileLabel:        "项目相对文件",
	DatasetMaxRowsLabel:     "maxRows",
	DatasetMaxBytesLabel:    "maxBytes",
	DatasetDelimiterLabel:   "CSV 分隔符",
	PreviewButton:           "预览",
	PreviewFailed:           "预览失败",
	PreviewHint:             "预览最多读取 20 行 / 256 KiB；执行时按 maxRows/maxBytes 校验整个文件。",
	PreviewRowsLabel:        "预览行数：%d",
	PreviewTruncated:        "预览已截断；执行时仍会校验完整数据集。",
	PreviewLimits:           "执行上限 maxRows %d / maxBytes %d。",
	PreviewSensitive:        "敏感列已脱敏：%s",
	Save:                    "保存",
	Cancel:                  "取消",
	Close:                   "关闭",
	Discard:                 "放弃修改",
	KeepEditing:             "继续编辑",
	DiscardTitle:            "放弃未保存的修改？",
	DiscardBody:             "当前草稿有未保存的修改。放弃不会写入磁盘。",
	Saved:                   "Suite 已保存。",
	SaveConflict:            "文件已在磁盘上被修改，请先重新加载；草稿已保留。",
	NothingToSave:           "没有需要保存的修改。",
	InvalidDraft:            "请先修正错误再保存。",
	RequestUnavailable:      "请求不可用：%s",
}

func (a *App) suiteEditorMsg() suiteEditorMessages {
	if a != nil && a.mgr != nil && a.mgr.cfg != nil && parseLanguage(string(a.mgr.cfg.language())) == langSimplifiedChinese {
		return suiteEditorZH
	}
	return suiteEditorEN
}
