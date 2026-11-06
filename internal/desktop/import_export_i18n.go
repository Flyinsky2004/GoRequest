package desktop

// interopMessages holds localized strings for the B08 Import / Export modals.
type interopMessages struct {
	ImportBufferBusy     string // %s path
	ImportMenuLabel      string
	ImportCURLMenuLabel  string
	ExportMenuLabel      string
	ExportCURLMenuLabel  string
	ExportBundleMenu     string
	UndoLastImportMenu   string
	ImportModalTitle     string
	ExportModalTitle     string
	SourceLabel          string
	SourceCURL           string
	SourcePostman        string
	SourcePortableBundle string
	TargetFolderLabel    string
	TargetEnvLabel       string
	ConflictPolicyLabel  string
	PolicyFail           string
	PolicyRename         string
	PolicyOverwrite      string
	PolicySkip           string
	CURLInputLabel       string
	CURLPlaceholder      string
	FilePathLabel        string
	FilePathPlaceholder  string
	BrowseFileButton     string
	PreviewButton        string
	ConfirmApplyButton   string
	UndoLastImportButton string
	CloseButton          string
	PreviewSectionTitle  string
	NoPreviewYet         string
	ChangesHeading       string
	IDMappingsHeading    string
	SecretsVarsHeading   string
	LossesHeading        string
	ConflictsHeading     string
	CanApplyBadge        string
	BlockedBadge         string
	ImportAppliedStatus  string
	ImportRolledBack     string

	ExportFormatLabel        string
	ExportKindCURL           string
	ExportKindPortableReq    string
	ExportKindPortableFolder string
	ExportKindPortableProj   string
	ExportKindPostmanCol     string
	ExportKindPostmanEnv     string
	OutputPathLabel          string
	OutputPathPlaceholder    string
	AllowOverwriteLabel      string
	RefreshPreviewButton     string
	CopyClipboardButton      string
	SaveToFileButton         string
	ExportPreviewHeading     string
	ExportSecurityNote       string
	ExportSavedStatus        string
}

var interopEN = interopMessages{
	ImportBufferBusy:     "Save or discard edits and stop any active request before changing %s.",
	ImportMenuLabel:      "Import (cURL / Postman / Bundle)…",
	ImportCURLMenuLabel:  "Import from cURL…",
	ExportMenuLabel:      "Export / Portable Bundle…",
	ExportCURLMenuLabel:  "Copy as Redacted cURL…",
	ExportBundleMenu:     "Export Portable Bundle…",
	UndoLastImportMenu:   "Undo Last Import",
	ImportModalTitle:     "Import & Interoperability Preview",
	ExportModalTitle:     "Export & Portable Package",
	SourceLabel:          "Import Source",
	SourceCURL:           "cURL Command",
	SourcePostman:        "Postman JSON",
	SourcePortableBundle: "Portable Bundle (.zip)",
	TargetFolderLabel:    "Target Folder",
	TargetEnvLabel:       "Target Environment",
	ConflictPolicyLabel:  "Conflict Strategy",
	PolicyFail:           "Block on Conflict",
	PolicyRename:         "Copy with New ID",
	PolicyOverwrite:      "Overwrite Existing",
	PolicySkip:           "Skip Existing",
	CURLInputLabel:       "cURL Command (Pure Go Parser — Never Invokes Shell)",
	CURLPlaceholder:      "curl -X POST https://api.example.com/v1/items -H 'Authorization: Bearer ...' -d '{\"name\":\"demo\"}'",
	FilePathLabel:        "Source File Path (.json / .yaml / .gorequest-bundle.zip)",
	FilePathPlaceholder:  "/path/to/collection.json or project.gorequest-bundle.zip",
	BrowseFileButton:     "Browse…",
	PreviewButton:        "Generate Preview",
	ConfirmApplyButton:   "Confirm & Apply Import",
	UndoLastImportButton: "Undo Last Import",
	CloseButton:          "Close",
	PreviewSectionTitle:  "ChangeSet & Loss Preview",
	NoPreviewYet:         "Click 'Generate Preview' to inspect file changes, ID mappings, secret bindings, and conversion losses before writing to disk.",
	ChangesHeading:       "Staged File Changes",
	IDMappingsHeading:    "Entity ID & Reference Mappings",
	SecretsVarsHeading:   "Variables, Secret Slots & Local File Slots",
	LossesHeading:        "Conversion Losses, Warnings & Security Scrubbing",
	ConflictsHeading:     "Blocking Conflicts",
	CanApplyBadge:        "READY TO APPLY",
	BlockedBadge:         "BLOCKED",
	ImportAppliedStatus:  "Imported %d file change(s)",
	ImportRolledBack:     "Rolled back last import",

	ExportFormatLabel:        "Export Format & Scope",
	ExportKindCURL:           "Redacted cURL",
	ExportKindPortableReq:    "Request Bundle",
	ExportKindPortableFolder: "Folder Bundle",
	ExportKindPortableProj:   "Project Bundle",
	ExportKindPostmanCol:     "Postman v2.1",
	ExportKindPostmanEnv:     "Postman Env",
	OutputPathLabel:          "Export File Path",
	OutputPathPlaceholder:    "/path/to/export.gorequest-bundle.zip or collection.json",
	AllowOverwriteLabel:      "Allow Overwrite Existing File",
	RefreshPreviewButton:     "Refresh Preview",
	CopyClipboardButton:      "Copy Redacted Output",
	SaveToFileButton:         "Export to File",
	ExportPreviewHeading:     "Redacted Preview & Manifest Summary",
	ExportSecurityNote:       "Real secrets, cookies, local history, device configs, reports, and host file paths are automatically excluded.",
	ExportSavedStatus:        "Exported to %s",
}

var interopZH = interopMessages{
	ImportBufferBusy:     "请先保存或放弃编辑，并停止正在执行的请求，再变更 %s。",
	ImportMenuLabel:      "导入 (cURL / Postman / 便携包)…",
	ImportCURLMenuLabel:  "从 cURL 导入…",
	ExportMenuLabel:      "导出 / 便携包…",
	ExportCURLMenuLabel:  "复制为脱敏 cURL…",
	ExportBundleMenu:     "导出便携包…",
	UndoLastImportMenu:   "撤销上次导入",
	ImportModalTitle:     "导入与变更预览",
	ExportModalTitle:     "导出与原生便携包",
	SourceLabel:          "导入来源",
	SourceCURL:           "cURL 命令",
	SourcePostman:        "Postman JSON",
	SourcePortableBundle: "原生便携包 (.zip)",
	TargetFolderLabel:    "目标文件夹",
	TargetEnvLabel:       "目标环境",
	ConflictPolicyLabel:  "冲突策略",
	PolicyFail:           "冲突时阻止",
	PolicyRename:         "分配新 ID 副本",
	PolicyOverwrite:      "覆盖已有项",
	PolicySkip:           "跳过已有项",
	CURLInputLabel:       "cURL 命令（纯 Go 解析，绝不调用 Shell）",
	CURLPlaceholder:      "curl -X POST https://api.example.com/v1/items -H 'Authorization: Bearer ...' -d '{\"name\":\"demo\"}'",
	FilePathLabel:        "源文件路径 (.json / .yaml / .gorequest-bundle.zip)",
	FilePathPlaceholder:  "/path/to/collection.json 或 project.gorequest-bundle.zip",
	BrowseFileButton:     "浏览…",
	PreviewButton:        "生成变更预览",
	ConfirmApplyButton:   "确认并应用导入",
	UndoLastImportButton: "撤销上次导入",
	CloseButton:          "关闭",
	PreviewSectionTitle:  "ChangeSet 变更与损失预览",
	NoPreviewYet:         "点击「生成变更预览」检查目标路径、ID 映射、敏感信息槽位及无法转换的损失项，确认后才会写盘。",
	ChangesHeading:       "待写入文件变更",
	IDMappingsHeading:    "实体 ID 与引用映射",
	SecretsVarsHeading:   "变量来源、Secret 槽位与本地文件槽位",
	LossesHeading:        "转换损失、告警与安全脱敏报告",
	ConflictsHeading:     "阻塞冲突",
	CanApplyBadge:        "可应用",
	BlockedBadge:         "已阻止",
	ImportAppliedStatus:  "已导入 %d 项文件变更",
	ImportRolledBack:     "已回滚上次导入",

	ExportFormatLabel:        "导出格式与范围",
	ExportKindCURL:           "脱敏 cURL",
	ExportKindPortableReq:    "单请求便携包",
	ExportKindPortableFolder: "文件夹便携包",
	ExportKindPortableProj:   "完整项目便携包",
	ExportKindPostmanCol:     "Postman v2.1",
	ExportKindPostmanEnv:     "Postman 环境",
	OutputPathLabel:          "导出文件路径",
	OutputPathPlaceholder:    "/path/to/export.gorequest-bundle.zip 或 collection.json",
	AllowOverwriteLabel:      "允许覆盖同名文件",
	RefreshPreviewButton:     "刷新预览",
	CopyClipboardButton:      "复制脱敏内容",
	SaveToFileButton:         "导出至文件",
	ExportPreviewHeading:     "脱敏预览与便携包清单摘要",
	ExportSecurityNote:       "真实密钥、Cookie、执行历史、设备配置、测试报告及本机绝对路径已自动排除。",
	ExportSavedStatus:        "已导出至 %s",
}

func (a *App) interopMsg() *interopMessages {
	if a != nil && a.mgr != nil && a.mgr.cfg != nil && a.mgr.cfg.language() == "zh" {
		return &interopZH
	}
	return &interopEN
}
