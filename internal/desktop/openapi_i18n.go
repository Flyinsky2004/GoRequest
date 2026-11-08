package desktop

// openAPIMessages holds localized strings (en / zh-CN) for the A03 OpenAPI &
// Schema Studio modal and request contract inspector.
type openAPIMessages struct {
	MenuImportOpenAPI     string
	MenuExportOpenAPI     string
	MenuContractInspector string
	ModalTitle            string
	TabImportSync         string
	TabExportCoverage     string
	TabContractValidator  string
	UndoLastButton        string
	CloseButton           string

	// Import & Sync tab
	SpecSourceFileLabel   string
	SpecSourcePlaceholder string
	BrowseButton          string
	SpecInlineLabel       string
	SpecInlinePlaceholder string
	SourceModeLabel       string
	ModeNative            string
	ModeExternalSpec      string
	SyncActionLabel       string
	ActionFreshImport     string
	ActionThreeWaySync    string
	StrictLossRejectLabel string
	GroupByTagLabel       string
	ConflictPolicyLabel   string
	PolicyFail            string
	PolicyRename          string
	PolicyOverwrite       string
	PolicySkip            string
	TargetFolderLabel     string
	TargetEnvLabel        string
	StagePreviewButton    string
	ApplyButton           string
	ValidationHeading     string
	SyncSummaryHeading    string
	MissingRefsHeading    string
	PreviewReadyBadge     string
	PreviewBlockedBadge   string
	AppliedStatusFmt      string
	RolledBackStatus      string

	// Export & Coverage tab
	TargetVersionLabel    string
	Version31             string
	Version32             string
	ExportFormatLabel     string
	FormatYAML            string
	FormatJSON            string
	StrictExportLabel     string
	AllowLossyDowngrade   string
	IncludeExamplesLabel  string
	OutputPathLabel       string
	OutputPathPlaceholder string
	AllowOverwriteLabel   string
	RefreshExportButton   string
	CopySpecButton        string
	SaveSpecFileButton    string
	CoverageHeading       string
	CompatHeading         string
	CompatLosslessBadge   string
	CompatLossyBadge      string
	CompatRejectedBadge   string
	ExportSavedFmt        string

	// Contract & Schema Validator tab
	ActiveRequestLabel      string
	NoActiveHTTPRequest     string
	OperationIDLabel        string
	SummaryLabel            string
	PathTemplateLabel       string
	DeprecatedLabel         string
	ResponseStatusLabel     string
	ResponseMediaTypeLabel  string
	ResponseSchemaJSONLabel string
	AddUpdateResponseBtn    string
	SaveContractButton      string
	ContractSavedStatus     string
	TestStatusInputLabel    string
	TestContentTypeLabel    string
	TestBodyJSONLabel       string
	ValidateResponseBtn     string
	UseLiveResponseBtn      string
	ValidationPassedBadge   string
	ValidationFailedBadge   string
}

var openAPIEN = openAPIMessages{
	MenuImportOpenAPI:     "OpenAPI / Swagger Import & Sync…",
	MenuExportOpenAPI:     "Export OpenAPI 3.1 / 3.2 & Coverage…",
	MenuContractInspector: "OpenAPI Contract & Schema Validator…",
	ModalTitle:            "OpenAPI 3.1 / 3.2 & JSON Schema Studio",
	TabImportSync:         "Import / Migrate / 3-Way Sync",
	TabExportCoverage:     "Export 3.1 & 3.2 / Coverage",
	TabContractValidator:  "Operation Contract & Schema Check",
	UndoLastButton:        "Undo Last OpenAPI Apply",
	CloseButton:           "Close",

	SpecSourceFileLabel:   "OpenAPI / Swagger File Path (.yaml / .json — Offline Multi-File $ref Root)",
	SpecSourcePlaceholder: "/path/to/openapi.yaml or swagger.json",
	BrowseButton:          "Browse…",
	SpecInlineLabel:       "Or Paste OpenAPI 3.1 / 3.2 / 3.0 / Swagger 2.0 Specification",
	SpecInlinePlaceholder: "openapi: 3.1.2\ninfo:\n  title: Sample API\n  version: 1.0.0\npaths: ...",
	SourceModeLabel:       "Contract Source of Truth",
	ModeNative:            "GoRequest Native",
	ModeExternalSpec:      "External Spec Mode",
	SyncActionLabel:       "Staging Mode",
	ActionFreshImport:     "Import / Migrate",
	ActionThreeWaySync:    "3-Way Sync Preview",
	StrictLossRejectLabel: "Strict Mode (Reject Any Lossy / Unsupported Construct)",
	GroupByTagLabel:       "Group Requests into Tag Subfolders",
	ConflictPolicyLabel:   "Conflict Strategy",
	PolicyFail:            "Block on Conflict",
	PolicyRename:          "Copy with New ID",
	PolicyOverwrite:       "Overwrite Existing",
	PolicySkip:            "Skip Conflicts",
	TargetFolderLabel:     "Target Folder",
	TargetEnvLabel:        "Target Environment",
	StagePreviewButton:    "Stage OpenAPI Preview",
	ApplyButton:           "Confirm & Apply to Project",
	ValidationHeading:     "Specification Validation & Version Matrix",
	SyncSummaryHeading:    "3-Way Contract Sync Summary",
	MissingRefsHeading:    "Blocked Remote / Missing Offline $ref Dependencies",
	PreviewReadyBadge:     "READY TO APPLY",
	PreviewBlockedBadge:   "BLOCKED",
	AppliedStatusFmt:      "Applied OpenAPI plan (%d file change(s))",
	RolledBackStatus:      "Rolled back last OpenAPI import/sync",

	TargetVersionLabel:    "Target OpenAPI Version",
	Version31:             "OpenAPI 3.1.2",
	Version32:             "OpenAPI 3.2.1",
	ExportFormatLabel:     "Serialization Format",
	FormatYAML:            "YAML",
	FormatJSON:            "JSON",
	StrictExportLabel:     "Strict Compatibility Gate",
	AllowLossyDowngrade:   "Allow Lossy 3.2 → 3.1 Downgrade",
	IncludeExamplesLabel:  "Include Redacted Examples",
	OutputPathLabel:       "Export File Path",
	OutputPathPlaceholder: "/path/to/openapi-3.1.yaml or openapi-3.2.json",
	AllowOverwriteLabel:   "Allow Overwrite Existing File",
	RefreshExportButton:   "Refresh Export & Coverage",
	CopySpecButton:        "Copy Specification",
	SaveSpecFileButton:    "Export OpenAPI File",
	CoverageHeading:       "Contract Documentation Coverage (Denominator: Exportable HTTP Operations)",
	CompatHeading:         "OpenAPI 3.1 vs 3.2 Version Compatibility Report",
	CompatLosslessBadge:   "COMPATIBLE (LOSSLESS)",
	CompatLossyBadge:      "DOWNGRADED (LOSSY)",
	CompatRejectedBadge:   "REJECTED BY STRICT 3.1 GATE",
	ExportSavedFmt:        "Exported OpenAPI specification to %s",

	ActiveRequestLabel:      "Active HTTP Request",
	NoActiveHTTPRequest:     "Open an HTTP request tab to edit its OpenAPI Operation Contract or validate responses.",
	OperationIDLabel:        "operationId",
	SummaryLabel:            "Contract Summary",
	PathTemplateLabel:       "OpenAPI Path Template (e.g. /users/{id})",
	DeprecatedLabel:         "Deprecated Operation",
	ResponseStatusLabel:     "Status Code (200, 400, default)",
	ResponseMediaTypeLabel:  "Media Type (application/json, application/problem+json)",
	ResponseSchemaJSONLabel: "Response JSON Schema (Draft 2020-12)",
	AddUpdateResponseBtn:    "Stage Response Contract Entry",
	SaveContractButton:      "Save Operation Contract to Request YAML",
	ContractSavedStatus:     "Saved OpenAPI operation contract to request YAML",
	TestStatusInputLabel:    "Response Status Code",
	TestContentTypeLabel:    "Response Content-Type Header",
	TestBodyJSONLabel:       "Response JSON Payload to Validate",
	ValidateResponseBtn:     "Validate Against Contract Schema",
	UseLiveResponseBtn:      "Load Latest Live Response",
	ValidationPassedBadge:   "SCHEMA VALID",
	ValidationFailedBadge:   "SCHEMA VIOLATIONS",
}

var openAPIZH = openAPIMessages{
	MenuImportOpenAPI:     "OpenAPI / Swagger 导入与同步…",
	MenuExportOpenAPI:     "导出 OpenAPI 3.1 / 3.2 与覆盖率…",
	MenuContractInspector: "OpenAPI 契约与 Schema 校验器…",
	ModalTitle:            "OpenAPI 3.1 / 3.2 与 JSON Schema 工作台",
	TabImportSync:         "导入 / 迁移 / 三方同步",
	TabExportCoverage:     "导出 3.1 & 3.2 / 覆盖率",
	TabContractValidator:  "Operation 契约与 Schema 校验",
	UndoLastButton:        "撤销上次 OpenAPI 变更",
	CloseButton:           "关闭",

	SpecSourceFileLabel:   "OpenAPI / Swagger 文件路径（.yaml / .json — 离线多文件 $ref 根目录）",
	SpecSourcePlaceholder: "/path/to/openapi.yaml 或 swagger.json",
	BrowseButton:          "浏览…",
	SpecInlineLabel:       "或粘贴 OpenAPI 3.1 / 3.2 / 3.0 / Swagger 2.0 规范内容",
	SpecInlinePlaceholder: "openapi: 3.1.2\ninfo:\n  title: 示例 API\n  version: 1.0.0\npaths: ...",
	SourceModeLabel:       "契约真源模式",
	ModeNative:            "GoRequest 原生项目真源",
	ModeExternalSpec:      "外部规范模式 (External Spec)",
	SyncActionLabel:       "预演模式",
	ActionFreshImport:     "导入 / 迁移",
	ActionThreeWaySync:    "三方同步预览 (3-Way Sync)",
	StrictLossRejectLabel: "严格模式（拒绝任何有损或不支持字段）",
	GroupByTagLabel:       "按 Tag 创建子目录分组",
	ConflictPolicyLabel:   "冲突策略",
	PolicyFail:            "冲突时阻断",
	PolicyRename:          "新 ID 副本",
	PolicyOverwrite:       "覆盖已有",
	PolicySkip:            "跳过冲突项",
	TargetFolderLabel:     "目标目录",
	TargetEnvLabel:        "目标环境",
	StagePreviewButton:    "生成 OpenAPI 变更预览",
	ApplyButton:           "确认并写入项目",
	ValidationHeading:     "规范校验与版本矩阵",
	SyncSummaryHeading:    "三方契约同步摘要",
	MissingRefsHeading:    "被阻断的远程或缺失本地 $ref 依赖清单",
	PreviewReadyBadge:     "可应用",
	PreviewBlockedBadge:   "已阻断",
	AppliedStatusFmt:      "已应用 OpenAPI 变更计划（%d 个文件变更）",
	RolledBackStatus:      "已回滚上次 OpenAPI 导入/同步",

	TargetVersionLabel:    "目标 OpenAPI 版本",
	Version31:             "OpenAPI 3.1.2",
	Version32:             "OpenAPI 3.2.1",
	ExportFormatLabel:     "导出序列化格式",
	FormatYAML:            "YAML",
	FormatJSON:            "JSON",
	StrictExportLabel:     "严格兼容门禁",
	AllowLossyDowngrade:   "允许 3.2 → 3.1 有损降级",
	IncludeExamplesLabel:  "包含脱敏示例 (Examples)",
	OutputPathLabel:       "导出文件路径",
	OutputPathPlaceholder: "/path/to/openapi-3.1.yaml 或 openapi-3.2.json",
	AllowOverwriteLabel:   "允许覆盖同名文件",
	RefreshExportButton:   "刷新导出预览与覆盖率",
	CopySpecButton:        "复制规范内容",
	SaveSpecFileButton:    "导出 OpenAPI 文件",
	CoverageHeading:       "契约文档覆盖率（分母：可导出 HTTP 操作数）",
	CompatHeading:         "OpenAPI 3.1 与 3.2 版本兼容性报告",
	CompatLosslessBadge:   "完全兼容（无损）",
	CompatLossyBadge:      "已降级（有损）",
	CompatRejectedBadge:   "已被 3.1 严格模式拒绝",
	ExportSavedFmt:        "已导出 OpenAPI 规范至 %s",

	ActiveRequestLabel:      "当前 HTTP 请求",
	NoActiveHTTPRequest:     "请先打开一个 HTTP 请求标签页以编辑其 OpenAPI 契约或执行 Schema 校验。",
	OperationIDLabel:        "operationId",
	SummaryLabel:            "契约摘要 (Summary)",
	PathTemplateLabel:       "OpenAPI 路径模板 (如 /users/{id})",
	DeprecatedLabel:         "标记为已废弃 (Deprecated)",
	ResponseStatusLabel:     "响应状态码 (200, 400, default)",
	ResponseMediaTypeLabel:  "媒体类型 (application/json, application/problem+json)",
	ResponseSchemaJSONLabel: "响应 JSON Schema (Draft 2020-12)",
	AddUpdateResponseBtn:    "暂存响应契约条目",
	SaveContractButton:      "保存 Operation 契约到请求 YAML",
	ContractSavedStatus:     "已保存 OpenAPI Operation 契约至请求文件",
	TestStatusInputLabel:    "待校验响应状态码",
	TestContentTypeLabel:    "待校验 Content-Type",
	TestBodyJSONLabel:       "待校验响应 JSON 内容",
	ValidateResponseBtn:     "执行契约 Schema 校验",
	UseLiveResponseBtn:      "载入最近一次实际响应",
	ValidationPassedBadge:   "SCHEMA 校验通过",
	ValidationFailedBadge:   "SCHEMA 校验失败",
}

func (a *App) openAPIMsg() openAPIMessages {
	if a != nil && a.mgr != nil && a.mgr.cfg != nil && parseLanguage(string(a.mgr.cfg.language())) == langSimplifiedChinese {
		return openAPIZH
	}
	return openAPIEN
}
