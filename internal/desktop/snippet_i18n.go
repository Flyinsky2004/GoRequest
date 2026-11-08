package desktop

// snippetMessages holds localized strings for the C04 request-snippet modal.
type snippetMessages struct {
	MenuLabel            string
	ModalTitle           string
	SecurityNote         string
	RequestLabel         string
	EnvLabel             string
	RuntimeLabel         string
	RequiredEnvHeading   string
	NotesHeading         string
	PreviewHeading       string
	NoPreviewYet         string
	BlockedBadge         string
	ReadyBadge           string
	RefreshButton        string
	CopyButton           string
	ExportButton         string
	PathLabel            string
	PathPlaceholder      string
	AllowOverwriteLabel  string
	BrowseFileButton     string
	CloseButton          string
	CopiedStatus         string
	CopyNothingStatus    string
	SnippetExportSaved   string // %s path
	NeedRefreshStatus    string
	EnvNoneLabel         string
	NotesScrubbedHeading string
}

var snippetEN = snippetMessages{
	MenuLabel:            "Request Code Snippet…",
	ModalTitle:           "Request Code Snippet (cURL / Go / Python / JavaScript / TypeScript)",
	SecurityNote:         "Values come from the selected environment snapshot; secrets stay as environment-variable placeholders and are never read from this machine.",
	RequestLabel:         "Request",
	EnvLabel:             "Environment",
	RuntimeLabel:         "Runtime",
	RequiredEnvHeading:   "Environment variables to bind before running",
	NotesHeading:         "Diagnostics & Scrubbing",
	PreviewHeading:       "Generated Code",
	NoPreviewYet:         "Pick a language to generate the reviewable snippet.",
	BlockedBadge:         "BLOCKED",
	ReadyBadge:           "REVIEWED OUTPUT",
	RefreshButton:        "Refresh",
	CopyButton:           "Copy to Clipboard",
	ExportButton:         "Export to File…",
	PathLabel:            "Export file path",
	PathPlaceholder:      "/path/to/snippet.go",
	AllowOverwriteLabel:  "Allow Overwrite Existing File",
	BrowseFileButton:     "Browse…",
	CloseButton:          "Close",
	CopiedStatus:         "Snippet copied to clipboard (never executed)",
	CopyNothingStatus:    "Nothing to copy yet — refresh the preview first",
	SnippetExportSaved:   "Snippet exported to %s",
	NeedRefreshStatus:    "Refresh the snippet preview first",
	EnvNoneLabel:         "(none)",
	NotesScrubbedHeading: "",
}

var snippetZH = snippetMessages{
	MenuLabel:            "请求代码片段…",
	ModalTitle:           "请求代码片段（cURL / Go / Python / JavaScript / TypeScript）",
	SecurityNote:         "取值来自所选环境快照；Secret 保持环境变量占位，绝不读取本机凭据。",
	RequestLabel:         "请求",
	EnvLabel:             "环境",
	RuntimeLabel:         "运行时",
	RequiredEnvHeading:   "运行前需绑定的环境变量",
	NotesHeading:         "诊断与脱敏报告",
	PreviewHeading:       "生成代码",
	NoPreviewYet:         "选择语言后生成可审阅片段。",
	BlockedBadge:         "已阻止",
	ReadyBadge:           "可审阅输出",
	RefreshButton:        "刷新",
	CopyButton:           "复制到剪贴板",
	ExportButton:         "导出到文件…",
	PathLabel:            "导出文件路径",
	PathPlaceholder:      "/path/to/snippet.go",
	AllowOverwriteLabel:  "允许覆盖已有文件",
	BrowseFileButton:     "浏览…",
	CloseButton:          "关闭",
	CopiedStatus:         "片段已复制到剪贴板（不会自动执行）",
	CopyNothingStatus:    "暂无可复制内容，请先刷新预览",
	SnippetExportSaved:   "片段已导出至 %s",
	NeedRefreshStatus:    "请先刷新片段预览",
	EnvNoneLabel:         "（无）",
	NotesScrubbedHeading: "",
}

func (a *App) snippetMsg() *snippetMessages {
	if a != nil && a.mgr != nil && a.mgr.cfg != nil && parseLanguage(string(a.mgr.cfg.language())) == langSimplifiedChinese {
		return &snippetZH
	}
	return &snippetEN
}
