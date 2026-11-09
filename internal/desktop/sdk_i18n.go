package desktop

import "strings"

// sdkMessages holds localized strings for the SDK generator modal.
type sdkMessages struct {
	MenuLabel          string
	ModalTitle         string
	SecurityNote       string
	SpecVersionLabel   string
	PackageLabel       string
	ModuleLabel        string
	TargetLabel        string
	Browse             string
	IncludeUnsupported string
	AllowOverwrite     string
	AllowDeleteStale   string
	PreviewAction      string
	ApplyAction        string
	Close              string
	Busy               string
	NoPreview          string
	NeedPreview        string
	ConflictsBlock     string
	SummaryFmt         string // files, bytes, operations, unsupported, conflicts, stale
	FilesHeading       string
	OperationsHeading  string
	UnsupportedHeading string
	ConflictsHeading   string
	StaleHeading       string
	ManifestFmt        string // generator, template, spec digest
	AppliedFmt         string // written, deleted
	TargetEmpty        string
	NoOperations       string
	ActionCreate       string
	ActionUpdate       string
	ActionUnchanged    string
	ActionDelete       string
	UnsupportedBadge   string
	ConflictBadge      string
}

var sdkEN = sdkMessages{
	MenuLabel:          "Generate Go SDK…",
	ModalTitle:         "OpenAPI Go SDK Generator",
	SecurityNote:       "The snapshot comes from the project contract. No secrets, cookies, history or tokens are read, and generated code embeds no credentials.",
	SpecVersionLabel:   "OpenAPI version",
	PackageLabel:       "Package name",
	ModuleLabel:        "Module path",
	TargetLabel:        "Output directory (outside the project)",
	Browse:             "Choose…",
	IncludeUnsupported: "Generate supported subset (skip unsupported operations)",
	AllowOverwrite:     "Allow updating generated files",
	AllowDeleteStale:   "Delete stale generated files",
	PreviewAction:      "Preview",
	ApplyAction:        "Write files",
	Close:              "Close",
	Busy:               "Generating preview…",
	NoPreview:          "Preview to see the generated file list before writing anything.",
	NeedPreview:        "Preview first.",
	ConflictsBlock:     "Resolve the listed conflicts before writing.",
	SummaryFmt:         "%d files · %d bytes · %d operations · %d unsupported · %d conflicts · %d stale",
	FilesHeading:       "Files",
	OperationsHeading:  "Operations",
	UnsupportedHeading: "Unsupported operations",
	ConflictsHeading:   "Conflicts",
	StaleHeading:       "Stale files",
	ManifestFmt:        "generator %s · template %s · spec %s",
	AppliedFmt:         "Wrote %d files, deleted %d stale files",
	TargetEmpty:        "Choose an output directory outside the project first.",
	NoOperations:       "No supported operations.",
	ActionCreate:       "new",
	ActionUpdate:       "update",
	ActionUnchanged:    "unchanged",
	ActionDelete:       "delete",
	UnsupportedBadge:   "skipped",
	ConflictBadge:      "blocked",
}

var sdkZH = sdkMessages{
	MenuLabel:          "生成 Go SDK…",
	ModalTitle:         "OpenAPI Go SDK 生成器",
	SecurityNote:       "快照来自项目契约；不读取 Secret、Cookie、历史或令牌，生成代码不嵌入任何凭据。",
	SpecVersionLabel:   "OpenAPI 版本",
	PackageLabel:       "包名",
	ModuleLabel:        "模块路径",
	TargetLabel:        "输出目录（项目之外）",
	Browse:             "选择…",
	IncludeUnsupported: "只生成支持子集（跳过不支持的 operation）",
	AllowOverwrite:     "允许更新已生成的受管文件",
	AllowDeleteStale:   "删除过期的生成文件",
	PreviewAction:      "预览",
	ApplyAction:        "写入文件",
	Close:              "关闭",
	Busy:               "正在生成预览…",
	NoPreview:          "先预览文件清单，再决定是否写入。",
	NeedPreview:        "请先预览。",
	ConflictsBlock:     "请先解决列出的冲突再写入。",
	SummaryFmt:         "%d 个文件 · %d 字节 · %d 个 operation · %d 个不支持 · %d 个冲突 · %d 个过期",
	FilesHeading:       "文件",
	OperationsHeading:  "Operations",
	UnsupportedHeading: "不支持的 operation",
	ConflictsHeading:   "冲突",
	StaleHeading:       "过期文件",
	ManifestFmt:        "生成器 %s · 模板 %s · 契约 %s",
	AppliedFmt:         "已写入 %d 个文件，删除 %d 个过期文件",
	TargetEmpty:        "请先选择项目之外的输出目录。",
	NoOperations:       "没有可支持的 operation。",
	ActionCreate:       "新建",
	ActionUpdate:       "更新",
	ActionUnchanged:    "无变化",
	ActionDelete:       "删除",
	UnsupportedBadge:   "已跳过",
	ConflictBadge:      "已阻止",
}

func (a *App) sdkMsg() *sdkMessages {
	if a != nil && a.mgr != nil && a.mgr.cfg != nil && parseLanguage(string(a.mgr.cfg.language())) == langSimplifiedChinese {
		return &sdkZH
	}
	return &sdkEN
}

// sdkActionLabel maps an action kind to its localized badge.
func (m *sdkMessages) sdkActionLabel(kind string) string {
	switch kind {
	case "create":
		return m.ActionCreate
	case "update":
		return m.ActionUpdate
	case "delete-stale":
		return m.ActionDelete
	default:
		return m.ActionUnchanged
	}
}

// shortDigest trims a digest for display.
func shortDigest(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 12 {
		return s[:12]
	}
	return s
}
