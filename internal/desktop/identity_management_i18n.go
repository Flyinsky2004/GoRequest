package desktop

// identityManagementMessages holds localized labels for the B05-UI identity
// management dialog (reusable schemes, folder auth, system credentials,
// diagnostics, and production target confirmation). Keeping them in a
// B05-owned file means i18n.go (owned by the integrator) does not need a merge.
type identityManagementMessages struct {
	ManageSchemes       string
	ManageFolderAuth    string
	ManageCredentials   string
	ManageDiagnostics   string
	IdentityManageTitle string

	SchemesSection string
	FolderSection  string
	CredSection    string
	DiagSection    string

	SchemeListEmpty   string
	NewScheme         string
	SchemeID          string
	SchemeIDHint      string
	SchemeType        string
	SchemeToken       string
	SchemeUsername    string
	SchemePassword    string
	SchemeKeyName     string
	SchemeKeyLocation string
	SchemeKeyValue    string
	SchemeSave        string
	SchemeCreate      string
	SchemeDelete      string
	SchemeReload      string
	SchemeSaved       string
	SchemeCreated     string
	SchemeDeleted     string
	SchemeRefsTitle   string
	SchemeRefsHint    string
	SchemeClearHint   string
	SchemeClearDelete string
	SchemeInUseFmt    string
	SchemeDirtyBlock  string
	SchemeConflict    string
	SchemeValidation  string

	FolderPick          string
	FolderInherit       string
	FolderNone          string
	FolderRef           string
	FolderInlineBearer  string
	FolderInlineBasic   string
	FolderInlineAPIKey  string
	FolderCurrentSource string
	FolderImpactTitle   string
	FolderImpactHint    string
	FolderImpactEmpty   string
	FolderDirtyConfirm  string
	FolderSave          string
	FolderClear         string
	FolderSaved         string
	FolderCleared       string
	FolderConflict      string

	CredDeclared      string
	CredName          string
	CredValue         string
	CredValueHint     string
	CredSources       string
	CredStatusBound   string
	CredStatusMissing string
	CredStatusUnavail string
	CredSave          string
	CredDelete        string
	CredDeleteConfirm string
	CredSaved         string
	CredDeleted       string
	CredEmpty         string
	CredNoDeclared    string
	CredBackendFailed string
	CredSecretHint    string

	DiagEmpty   string
	DiagRefresh string

	PolicyTitle       string
	PolicyDetailFmt   string
	PolicyScopeFmt    string
	PolicyOnceHint    string
	PolicyConfirmSend string

	Close string
}

var identityManagementMessagesZh = identityManagementMessages{
	ManageSchemes:       "管理鉴权方案",
	ManageFolderAuth:    "文件夹鉴权",
	ManageCredentials:   "系统凭据",
	ManageDiagnostics:   "诊断",
	IdentityManageTitle: "身份与凭据管理",

	SchemesSection: "可复用方案",
	FolderSection:  "文件夹鉴权",
	CredSection:    "系统凭据",
	DiagSection:    "诊断",

	SchemeListEmpty:   "尚未创建可复用鉴权方案。",
	NewScheme:         "新建方案",
	SchemeID:          "方案 ID",
	SchemeIDHint:      "ID 用于 request 的 auth.ref；创建后不可在此修改。",
	SchemeType:        "类型",
	SchemeToken:       "Token（建议 {{secret.name}}）",
	SchemeUsername:    "用户名",
	SchemePassword:    "密码（建议 {{secret.name}}）",
	SchemeKeyName:     "参数/请求头/Cookie 名称",
	SchemeKeyLocation: "发送位置",
	SchemeKeyValue:    "密钥值（建议 {{secret.name}}）",
	SchemeSave:        "保存方案",
	SchemeCreate:      "创建方案",
	SchemeDelete:      "删除方案",
	SchemeReload:      "重新载入",
	SchemeSaved:       "鉴权方案已保存。",
	SchemeCreated:     "鉴权方案已创建。",
	SchemeDeleted:     "鉴权方案已删除。",
	SchemeRefsTitle:   "引用位置",
	SchemeRefsHint:    "以下位置仍引用该方案：",
	SchemeClearHint:   "删除会把上述引用改写为显式 none，并在同一变更集中原子提交；不会留下悬空引用。",
	SchemeClearDelete: "清除引用并删除",
	SchemeInUseFmt:    "方案 %q 仍被 %d 处引用，需确认后才能删除。",
	SchemeDirtyBlock:  "以下有未保存编辑的请求引用了该方案，请先保存或放弃编辑：%s",
	SchemeConflict:    "方案文件已在外部修改。请重新载入后再编辑，当前改动未写入磁盘。",
	SchemeValidation:  "请检查方案字段：bearer 需要 token，basic 需要用户名和密码，apikey 需要名称与值。",

	FolderPick:          "文件夹",
	FolderInherit:       "继承上级",
	FolderNone:          "显式无鉴权（none）",
	FolderRef:           "引用方案",
	FolderInlineBearer:  "内联 Bearer",
	FolderInlineBasic:   "内联 Basic",
	FolderInlineAPIKey:  "内联 API Key",
	FolderCurrentSource: "当前生效来源：%s",
	FolderImpactTitle:   "将受影响并继承此文件夹的请求",
	FolderImpactHint:    "变更只写入该文件夹的 collection.yaml；请求文件保持不变。",
	FolderImpactEmpty:   "当前没有请求从该文件夹继承鉴权。",
	FolderDirtyConfirm:  "以下请求有未保存编辑且受此改动影响：%s。继续保存会在发送时使用新的文件夹鉴权。",
	FolderSave:          "保存文件夹鉴权",
	FolderClear:         "清除文件夹鉴权",
	FolderSaved:         "文件夹鉴权已保存。",
	FolderCleared:       "文件夹鉴权已清除。",
	FolderConflict:      "collection.yaml 已在外部修改。请重新载入后再保存，当前改动未写入磁盘。",

	CredDeclared:      "项目声明的秘密（不含值）",
	CredName:          "秘密名称",
	CredValue:         "秘密值（仅写入系统凭据存储）",
	CredValueHint:     "值不会写入项目文件；系统凭据不可用时保存会失败，不会降级为明文。",
	CredSources:       "来源",
	CredStatusBound:   "已绑定（%s）",
	CredStatusMissing: "未绑定",
	CredStatusUnavail: "凭据后端不可用（%s）",
	CredSave:          "写入/更新凭据",
	CredDelete:        "删除凭据",
	CredDeleteConfirm: "确认删除凭据 %q？",
	CredSaved:         "凭据已写入系统凭据存储。",
	CredDeleted:       "凭据已删除。",
	CredEmpty:         "请输入秘密名称和值。",
	CredNoDeclared:    "项目尚未声明秘密；请先在环境或鉴权方案中使用 {{secret.name}}。",
	CredBackendFailed: "无法访问系统凭据存储：%s",
	CredSecretHint:    "只显示绑定状态，不读取或展示已保存的值。",

	DiagEmpty:   "没有发现缺失鉴权引用或未绑定秘密。",
	DiagRefresh: "重新检查",

	PolicyTitle:       "生产目标策略确认",
	PolicyDetailFmt:   "请求 %s 将 %s %s（环境 %s）。",
	PolicyScopeFmt:    "确认仅对本次请求有效：%s。",
	PolicyOnceHint:    "确认只授权这一次发送；重定向到其他目标会重新检查策略。",
	PolicyConfirmSend: "确认并发送",

	Close: "关闭",
}

var identityManagementMessagesEn = identityManagementMessages{
	ManageSchemes:       "Manage auth schemes",
	ManageFolderAuth:    "Folder auth",
	ManageCredentials:   "Credentials",
	ManageDiagnostics:   "Diagnostics",
	IdentityManageTitle: "Identity & credential management",

	SchemesSection: "Reusable schemes",
	FolderSection:  "Folder auth",
	CredSection:    "System credentials",
	DiagSection:    "Diagnostics",

	SchemeListEmpty:   "No reusable auth schemes yet.",
	NewScheme:         "New scheme",
	SchemeID:          "Scheme ID",
	SchemeIDHint:      "The ID is used by request auth.ref and cannot be changed here after creation.",
	SchemeType:        "Type",
	SchemeToken:       "Token (prefer {{secret.name}})",
	SchemeUsername:    "Username",
	SchemePassword:    "Password (prefer {{secret.name}})",
	SchemeKeyName:     "Parameter / header / cookie name",
	SchemeKeyLocation: "Add to",
	SchemeKeyValue:    "Value (prefer {{secret.name}})",
	SchemeSave:        "Save scheme",
	SchemeCreate:      "Create scheme",
	SchemeDelete:      "Delete scheme",
	SchemeReload:      "Reload",
	SchemeSaved:       "Auth scheme saved.",
	SchemeCreated:     "Auth scheme created.",
	SchemeDeleted:     "Auth scheme deleted.",
	SchemeRefsTitle:   "Referenced by",
	SchemeRefsHint:    "These places still reference this scheme:",
	SchemeClearHint:   "Deleting rewrites those references to an explicit none in one atomic change set, leaving no dangling reference.",
	SchemeClearDelete: "Clear references & delete",
	SchemeInUseFmt:    "Scheme %q is still referenced %d time(s); confirm before deleting.",
	SchemeDirtyBlock:  "These requests with unsaved edits reference this scheme; save or discard them first: %s",
	SchemeConflict:    "The scheme file changed on disk. Reload before editing; your changes were not written.",
	SchemeValidation:  "Check the scheme fields: bearer needs a token, basic needs username and password, apikey needs name and value.",

	FolderPick:          "Folder",
	FolderInherit:       "Inherit parent",
	FolderNone:          "Explicit none",
	FolderRef:           "Reference scheme",
	FolderInlineBearer:  "Inline bearer",
	FolderInlineBasic:   "Inline basic",
	FolderInlineAPIKey:  "Inline API key",
	FolderCurrentSource: "Current effective source: %s",
	FolderImpactTitle:   "Requests that inherit this folder auth",
	FolderImpactHint:    "Only this folder's collection.yaml is written; request files stay unchanged.",
	FolderImpactEmpty:   "No request currently inherits auth from this folder.",
	FolderDirtyConfirm:  "These affected requests have unsaved edits: %s. Saving will use the new folder auth when they are sent.",
	FolderSave:          "Save folder auth",
	FolderClear:         "Clear folder auth",
	FolderSaved:         "Folder auth saved.",
	FolderCleared:       "Folder auth cleared.",
	FolderConflict:      "collection.yaml changed on disk. Reload before saving; your changes were not written.",

	CredDeclared:      "Secrets declared by this project (names only)",
	CredName:          "Secret name",
	CredValue:         "Secret value (written only to the system credential store)",
	CredValueHint:     "Values never go into project files; if the system store is unavailable saving fails instead of falling back to plaintext.",
	CredSources:       "Sources",
	CredStatusBound:   "Bound (%s)",
	CredStatusMissing: "Not bound",
	CredStatusUnavail: "Credential backend unavailable (%s)",
	CredSave:          "Write / update credential",
	CredDelete:        "Delete credential",
	CredDeleteConfirm: "Delete credential %q?",
	CredSaved:         "Credential written to the system store.",
	CredDeleted:       "Credential deleted.",
	CredEmpty:         "Enter a secret name and value.",
	CredNoDeclared:    "No secrets are declared yet; use {{secret.name}} in an environment or auth scheme first.",
	CredBackendFailed: "Cannot access the system credential store: %s",
	CredSecretHint:    "Only binding status is shown; stored values are never read back for display.",

	DiagEmpty:   "No missing auth references or unbound secrets found.",
	DiagRefresh: "Check again",

	PolicyTitle:       "Production target confirmation",
	PolicyDetailFmt:   "Request %s will send %s %s (environment %s).",
	PolicyScopeFmt:    "This confirmation covers only this request: %s.",
	PolicyOnceHint:    "Confirmation authorizes only this single send; redirects to another target are re-evaluated.",
	PolicyConfirmSend: "Confirm and send",

	Close: "Close",
}

func (a *App) identityManagementMsg() *identityManagementMessages {
	if a != nil && a.mgr != nil && a.mgr.cfg != nil && parseLanguage(string(a.mgr.cfg.language())) == langEnglish {
		return &identityManagementMessagesEn
	}
	return &identityManagementMessagesZh
}
