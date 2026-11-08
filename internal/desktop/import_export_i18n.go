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

	// Structured preview rendering. These keep the technical facts (IDs, paths,
	// codes, JSON keys, request data) verbatim while localizing the surrounding
	// natural language.
	RequestLabel          string
	RootFolderPlaceholder string
	LabelColon            string
	BytesFmt              string
	ChangeLineFmt         string
	IDMappingLineFmt      string
	VariableLineFmt       string
	LocalSlotLineFmt      string
	RenamedTag            string
	VarKindVar            string
	VarKindSecret         string
	SummaryFallback       string
	LossUnknownFmt        string

	// Loss severity labels (the machine code stays visible next to them).
	SeverityInfo             string
	SeverityWarning          string
	SeverityUnsupported      string
	SeveritySecurityScrubbed string
	SeverityBlocked          string

	// Change kinds.
	ChangeKindCreate string
	ChangeKindUpdate string
	ChangeKindDelete string
	ChangeKindMove   string

	// Entity kinds.
	EntityRequest     string
	EntityFolder      string
	EntityEnvironment string
	EntityAuth        string
	EntityDoc         string
	EntityAsset       string
	EntityAttachment  string

	// Mapping actions.
	ActionCreate   string
	ActionUpdate   string
	ActionRename   string
	ActionSkip     string
	ActionConflict string

	// Variable scopes and sources.
	ScopeEnvironment string
	ScopeCollection  string
	ScopeRequestAuth string
	ScopeHeader      string
	ScopeBundle      string

	SourcePostmanEnv        string
	SourcePostmanCollection string
	SourceCurlAuth          string
	SourceCurlHeader        string
	SourcePortable          string

	// Format display names used only when the core summary cannot be mapped.
	FormatCURL               string
	FormatPostmanCollection  string
	FormatPostmanEnvironment string
	FormatPortableBundle     string
	FormatOpenAPI            string
	FormatSwagger2           string

	// User-facing import/export errors. Known desktop messages are translated in
	// full; unknown core errors keep their original technical detail behind a
	// localized classification.
	ErrorCURLRequired              string
	ErrorImportFileRequired        string
	ErrorPreviewRequired           string
	ErrorNoReceipt                 string
	ErrorSelectRequestForCURL      string
	ErrorSelectRequestForBundle    string
	ErrorExportPathRequired        string
	ErrorTargetExistsFmt           string
	ErrorReadImportFilePrefix      string
	ErrorParsePostmanCollection    string
	ErrorParsePostmanEnvironment   string
	ErrorUnrecognizedImportFormat  string
	ErrorImportPlanBlockedPrefix   string
	ErrorImportPlanApplied         string
	ErrorOpenAPIRouted             string
	ErrorUnknownImportFmt          string
	ErrorUnknownExportFmt          string
	ErrorExportTargetExistsFmt     string
	ErrorExportPathRequiredCore    string
	ErrorUnsupportedExportScopeFmt string
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

	RequestLabel:          "Request",
	RootFolderPlaceholder: "collections (root)",
	LabelColon:            ": ",
	BytesFmt:              "%d bytes",
	ChangeLineFmt:         "[%s] %s (%s)",
	IDMappingLineFmt:      "%s: %s -> %s (%s; %s)%s",
	VariableLineFmt:       "[%s] %s -> %s = %s (source: %s)",
	LocalSlotLineFmt:      "[local-slot] local:%s (bind local file before sending)",
	RenamedTag:            " [RENAMED]",
	VarKindVar:            "var",
	VarKindSecret:         "secret",
	SummaryFallback:       "%s import preview: %d file change(s), %d loss/warning note(s), %d conflict(s)",
	LossUnknownFmt:        "Untranslated conversion diagnostic: %s",

	SeverityInfo:             "info",
	SeverityWarning:          "warning",
	SeverityUnsupported:      "unsupported",
	SeveritySecurityScrubbed: "security-scrubbed",
	SeverityBlocked:          "blocked",

	ChangeKindCreate: "create",
	ChangeKindUpdate: "update",
	ChangeKindDelete: "delete",
	ChangeKindMove:   "move",

	EntityRequest:     "request",
	EntityFolder:      "folder",
	EntityEnvironment: "environment",
	EntityAuth:        "auth",
	EntityDoc:         "doc",
	EntityAsset:       "asset",
	EntityAttachment:  "attachment",

	ActionCreate:   "create",
	ActionUpdate:   "update",
	ActionRename:   "rename",
	ActionSkip:     "skip",
	ActionConflict: "conflict",

	ScopeEnvironment: "environment",
	ScopeCollection:  "collection",
	ScopeRequestAuth: "request-auth",
	ScopeHeader:      "header",
	ScopeBundle:      "bundle",

	SourcePostmanEnv:        "postman-env",
	SourcePostmanCollection: "postman-collection",
	SourceCurlAuth:          "curl-auth",
	SourceCurlHeader:        "curl-header",
	SourcePortable:          "portable",

	FormatCURL:               "cURL",
	FormatPostmanCollection:  "Postman Collection",
	FormatPostmanEnvironment: "Postman Environment",
	FormatPortableBundle:     "Portable Bundle",
	FormatOpenAPI:            "OpenAPI",
	FormatSwagger2:           "Swagger 2.0",

	ErrorCURLRequired:              "Please enter a cURL command to preview.",
	ErrorImportFileRequired:        "Please enter or browse to an import file (.json, .yaml, or .gorequest-bundle.zip).",
	ErrorPreviewRequired:           "Generate a preview before applying.",
	ErrorNoReceipt:                 "No applied import receipt available to roll back.",
	ErrorSelectRequestForCURL:      "Select or open an HTTP request to generate a redacted cURL command.",
	ErrorSelectRequestForBundle:    "Select a request to export as a Portable Bundle.",
	ErrorExportPathRequired:        "Please enter an export file path.",
	ErrorTargetExistsFmt:           "Target file %q already exists (enable 'Allow Overwrite' to replace it).",
	ErrorReadImportFilePrefix:      "Read import file: ",
	ErrorParsePostmanCollection:    "parse Postman Collection structure: ",
	ErrorParsePostmanEnvironment:   "parse Postman Environment structure: ",
	ErrorUnrecognizedImportFormat:  "unsupported or unrecognized import format (file: %q)",
	ErrorImportPlanBlockedPrefix:   "import plan cannot be applied: ",
	ErrorImportPlanApplied:         "import plan already applied",
	ErrorOpenAPIRouted:             "openapi/swagger specification conversion belongs to task A03 and is not handled by B08 basic interop",
	ErrorUnknownImportFmt:          "Import failed: %s",
	ErrorUnknownExportFmt:          "Export failed: %s",
	ErrorExportTargetExistsFmt:     "export target %q already exists",
	ErrorExportPathRequiredCore:    "export file path is required",
	ErrorUnsupportedExportScopeFmt: "unsupported portable export scope %q",
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

	RequestLabel:          "请求",
	RootFolderPlaceholder: "collections（根目录）",
	LabelColon:            "：",
	BytesFmt:              "%d 字节",
	ChangeLineFmt:         "[%s] %s（%s）",
	IDMappingLineFmt:      "%s：%s → %s（%s；%s）%s",
	VariableLineFmt:       "[%s] %s → %s = %s（来源：%s）",
	LocalSlotLineFmt:      "[本地文件槽位] local:%s（发送前请绑定本地文件）",
	RenamedTag:            " [已重命名]",
	VarKindVar:            "变量",
	VarKindSecret:         "Secret",
	SummaryFallback:       "%s导入预览：%d 项文件变更，%d 项损失/警告，%d 项冲突",
	LossUnknownFmt:        "未收录的转换诊断：%s",

	SeverityInfo:             "提示",
	SeverityWarning:          "警告",
	SeverityUnsupported:      "不支持",
	SeveritySecurityScrubbed: "安全脱敏",
	SeverityBlocked:          "已阻止",

	ChangeKindCreate: "新建",
	ChangeKindUpdate: "更新",
	ChangeKindDelete: "删除",
	ChangeKindMove:   "移动",

	EntityRequest:     "请求",
	EntityFolder:      "文件夹",
	EntityEnvironment: "环境",
	EntityAuth:        "鉴权方案",
	EntityDoc:         "文档",
	EntityAsset:       "资源",
	EntityAttachment:  "附件",

	ActionCreate:   "新建",
	ActionUpdate:   "更新",
	ActionRename:   "重命名",
	ActionSkip:     "跳过",
	ActionConflict: "冲突",

	ScopeEnvironment: "环境",
	ScopeCollection:  "集合",
	ScopeRequestAuth: "请求鉴权",
	ScopeHeader:      "请求头",
	ScopeBundle:      "便携包",

	SourcePostmanEnv:        "Postman 环境",
	SourcePostmanCollection: "Postman 集合",
	SourceCurlAuth:          "cURL 鉴权",
	SourceCurlHeader:        "cURL 请求头",
	SourcePortable:          "便携包",

	FormatCURL:               "cURL",
	FormatPostmanCollection:  "Postman 集合",
	FormatPostmanEnvironment: "Postman 环境",
	FormatPortableBundle:     "便携包",
	FormatOpenAPI:            "OpenAPI",
	FormatSwagger2:           "Swagger 2.0",

	ErrorCURLRequired:              "请输入要预览的 cURL 命令。",
	ErrorImportFileRequired:        "请输入或浏览选择导入文件（.json、.yaml 或 .gorequest-bundle.zip）。",
	ErrorPreviewRequired:           "请先生成变更预览再应用。",
	ErrorNoReceipt:                 "没有可回滚的已应用导入记录。",
	ErrorSelectRequestForCURL:      "请选择或打开一个 HTTP 请求以生成脱敏 cURL 命令。",
	ErrorSelectRequestForBundle:    "请选择要导出为便携包的请求。",
	ErrorExportPathRequired:        "请输入导出文件路径。",
	ErrorTargetExistsFmt:           "目标文件 %q 已存在（启用「允许覆盖」后可替换）。",
	ErrorReadImportFilePrefix:      "读取导入文件失败：",
	ErrorParsePostmanCollection:    "解析 Postman 集合结构失败：",
	ErrorParsePostmanEnvironment:   "解析 Postman 环境结构失败：",
	ErrorUnrecognizedImportFormat:  "不支持或无法识别的导入格式（文件：%q）",
	ErrorImportPlanBlockedPrefix:   "导入计划无法应用：",
	ErrorImportPlanApplied:         "导入计划已应用，不能重复应用",
	ErrorOpenAPIRouted:             "OpenAPI/Swagger 规范转换属于 A03 任务，B08 基础互操作不处理",
	ErrorUnknownImportFmt:          "导入失败：%s",
	ErrorUnknownExportFmt:          "导出失败：%s",
	ErrorExportTargetExistsFmt:     "导出目标 %q 已存在",
	ErrorExportPathRequiredCore:    "必须提供导出文件路径",
	ErrorUnsupportedExportScopeFmt: "不支持的便携包导出范围 %q",
}

// interopLanguage returns the interface language for interop surfaces. It reads
// the config on every call so a language switch applies on the next frame.
func (a *App) interopLanguage() language {
	if a != nil && a.mgr != nil && a.mgr.cfg != nil {
		return a.mgr.cfg.language()
	}
	return defaultLanguage()
}

func (a *App) interopMsg() *interopMessages {
	if a.interopLanguage() == langEnglish {
		return &interopEN
	}
	return &interopZH
}
