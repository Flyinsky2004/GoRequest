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

	StorageTitle              string
	StorageCurrentFmt         string
	StoragePathFmt            string
	StorageUnavailableFmt     string
	StoragePlaintext          string
	StorageEnvNote            string
	StorageKeychain           string
	StorageFile               string
	StorageSwitch             string
	StorageSame               string
	StorageNoBackend          string
	StoragePlanTitle          string
	StoragePlanSummaryFmt     string
	StoragePlanTargetPathFmt  string
	StoragePlanEnumLimit      string
	StoragePlanEmpty          string
	StoragePlanConflict       string
	StoragePlanCopy           string
	StoragePlanSame           string
	StoragePlanTargetOnly     string
	StoragePlanMissingBoth    string
	StorageConflictHintFmt    string
	StorageKeepTarget         string
	StorageOverwriteTarget    string
	StoragePolicyKeepFmt      string
	StoragePolicyOverwriteFmt string
	StorageConfirm            string
	StorageConfirmOverwrite   string
	StorageCancel             string
	StorageSwitchedFmt        string
	StorageSwitchFailedFmt    string

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
	ManageCredentials:   "秘密与存储",
	ManageDiagnostics:   "诊断",
	IdentityManageTitle: "身份与凭据管理",

	SchemesSection: "可复用方案",
	FolderSection:  "文件夹鉴权",
	CredSection:    "秘密与存储",
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
	CredValue:         "秘密值（写入当前所选存储位置）",
	CredValueHint:     "值不会写入项目文件；写入当前所选存储位置。系统凭据不可用且未选择本地文件时保存会失败，不会静默降级。",
	CredSources:       "来源",
	CredStatusBound:   "已绑定（%s）",
	CredStatusMissing: "未绑定",
	CredStatusUnavail: "凭据后端不可用（%s）",
	CredSave:          "写入/更新凭据",
	CredDelete:        "删除凭据",
	CredDeleteConfirm: "确认删除凭据 %q？",
	CredSaved:         "凭据已写入所选存储位置。",
	CredDeleted:       "凭据已删除。",
	CredEmpty:         "请输入秘密名称和值。",
	CredNoDeclared:    "项目尚未声明秘密；请先在环境或鉴权方案中使用 {{secret.name}}。",
	CredBackendFailed: "无法访问所选存储位置：%s",
	CredSecretHint:    "只显示绑定状态，不读取或展示已保存的值。",

	StorageTitle:              "秘密存储位置",
	StorageCurrentFmt:         "当前存储位置：%s",
	StoragePathFmt:            "实际路径/服务：%s",
	StorageUnavailableFmt:     "不可用原因：%s",
	StoragePlaintext:          "本地文件为明文存储，未加密；仅依赖 0700 目录与 0600 文件权限保护。",
	StorageEnvNote:            "环境变量 GOREQUEST_SECRET_<NAME> 仍是只读覆盖，优先级高于所选存储位置，不会被迁移或改写。",
	StorageKeychain:           "系统 Keychain",
	StorageFile:               "本地文件（明文）",
	StorageSwitch:             "切换存储位置",
	StorageSame:               "当前已使用该存储位置。",
	StorageNoBackend:          "没有可用的秘密存储后端。",
	StoragePlanTitle:          "切换计划（确认前不会写入）",
	StoragePlanSummaryFmt:     "从 %s 切换到 %s",
	StoragePlanTargetPathFmt:  "目标路径/服务：%s",
	StoragePlanEnumLimit:      "来源后端无法枚举全部条目：只覆盖项目声明与已管理清单中的名称，不声称迁移来源中的其他条目。",
	StoragePlanEmpty:          "没有需要复制的秘密名称。",
	StoragePlanConflict:       "两边值不同（需选择处理方式）",
	StoragePlanCopy:           "将复制并读回校验",
	StoragePlanSame:           "两边值相同",
	StoragePlanTargetOnly:     "仅目标已有（保留）",
	StoragePlanMissingBoth:    "两边均未设置",
	StorageConflictHintFmt:    "有 %d 个名称在两边值不同。默认保留目标值；选择覆盖才会替换。",
	StorageKeepTarget:         "保留目标值",
	StorageOverwriteTarget:    "覆盖目标值",
	StoragePolicyKeepFmt:      "冲突处理：保留目标值（不覆盖）。",
	StoragePolicyOverwriteFmt: "冲突处理：用来源值覆盖目标值。",
	StorageConfirm:            "开始切换",
	StorageConfirmOverwrite:   "覆盖并切换",
	StorageCancel:             "取消",
	StorageSwitchedFmt:        "已切换到 %s；复制并逐项校验 %d 个秘密，来源副本保留。",
	StorageSwitchFailedFmt:    "切换失败，仍使用原存储位置：%s",

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
	ManageCredentials:   "Secrets & storage",
	ManageDiagnostics:   "Diagnostics",
	IdentityManageTitle: "Identity & credential management",

	SchemesSection: "Reusable schemes",
	FolderSection:  "Folder auth",
	CredSection:    "Secrets & storage",
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
	CredValue:         "Secret value (written to the selected storage location)",
	CredValueHint:     "Values never go into project files; they are written to the selected storage location. If the system store is unavailable and no local file is selected, saving fails instead of falling back silently.",
	CredSources:       "Sources",
	CredStatusBound:   "Bound (%s)",
	CredStatusMissing: "Not bound",
	CredStatusUnavail: "Credential backend unavailable (%s)",
	CredSave:          "Write / update credential",
	CredDelete:        "Delete credential",
	CredDeleteConfirm: "Delete credential %q?",
	CredSaved:         "Credential written to the selected storage location.",
	CredDeleted:       "Credential deleted.",
	CredEmpty:         "Enter a secret name and value.",
	CredNoDeclared:    "No secrets are declared yet; use {{secret.name}} in an environment or auth scheme first.",
	CredBackendFailed: "Cannot access the selected secret store: %s",
	CredSecretHint:    "Only binding status is shown; stored values are never read back for display.",

	StorageTitle:              "Secret storage location",
	StorageCurrentFmt:         "Current location: %s",
	StoragePathFmt:            "Actual path/service: %s",
	StorageUnavailableFmt:     "Unavailable: %s",
	StoragePlaintext:          "The local file stores plaintext, not ciphertext; protection is the 0700 directory and 0600 file permissions only.",
	StorageEnvNote:            "GOREQUEST_SECRET_<NAME> stays a read-only override with higher priority than the selected location; it is never migrated or rewritten.",
	StorageKeychain:           "System Keychain",
	StorageFile:               "Local file (plaintext)",
	StorageSwitch:             "Change storage location",
	StorageSame:               "This storage location is already selected.",
	StorageNoBackend:          "No secret storage backend is available.",
	StoragePlanTitle:          "Switch plan (nothing is written until you confirm)",
	StoragePlanSummaryFmt:     "%s → %s",
	StoragePlanTargetPathFmt:  "Target path/service: %s",
	StoragePlanEnumLimit:      "The source backend cannot enumerate every item: only project-declared and managed-manifest names are covered. This is not a full-store migration.",
	StoragePlanEmpty:          "No secret names need copying.",
	StoragePlanConflict:       "Values differ (choose how to resolve)",
	StoragePlanCopy:           "Will copy and read back to verify",
	StoragePlanSame:           "Values already match",
	StoragePlanTargetOnly:     "Target only (kept)",
	StoragePlanMissingBoth:    "Unset in both stores",
	StorageConflictHintFmt:    "%d name(s) differ between the stores. Target values are kept by default; choose overwrite to replace them.",
	StorageKeepTarget:         "Keep target values",
	StorageOverwriteTarget:    "Overwrite target values",
	StoragePolicyKeepFmt:      "Conflict handling: keep target values (no overwrite).",
	StoragePolicyOverwriteFmt: "Conflict handling: overwrite target values with source values.",
	StorageConfirm:            "Start switch",
	StorageConfirmOverwrite:   "Overwrite and switch",
	StorageCancel:             "Cancel",
	StorageSwitchedFmt:        "Switched to %s; copied and verified %d secret(s). Source copies were kept.",
	StorageSwitchFailedFmt:    "Switch failed; the previous storage location is still active: %s",

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

// secretEntryMessages holds the bilingual labels for the "declare a name ->
// set the local value" dialog. FIX09 keeps them here (a card-owned file) so
// i18n.go, owned by the integrator, does not need a merge. Every string is a
// label or a non-sensitive status; none of them can carry a secret value.
type secretEntryMessages struct {
	Title            string
	ProjectFmt       string
	RefFmt           string
	SourceHint       string
	ADRHint          string
	NameLabel        string
	NamePlaceholder  string
	ValueLabel       string
	ValuePlaceholder string
	ValueHint        string
	StatusLabel      string
	StatusBoundFmt   string
	StatusUnavailFmt string
	StatusMissing    string
	SetValue         string
	UpdateValue      string
	Delete           string
	DeleteConfirmFmt string
	SavedFmt         string
	DeletedFmt       string
	BackendFailed    string
	InvalidName      string
	Empty            string
	Cancel           string
	// Env-row entry points.
	EnvSetValue      string
	EnvUpdateValue   string
	EnvStatusBound   string
	EnvStatusMissing string
	EnvStatusUnavail string
	EnvHint          string
	// Credentials-page entry point.
	CredSetValue string
	// Storage location shown inside the value dialog.
	BackendFmt            string
	BackendPathFmt        string
	BackendPlaintext      string
	BackendUnavailableFmt string
	ChangeStorage         string
}

func secretEntryMessagesFor(lang string) *secretEntryMessages {
	if lang == "en" {
		return &secretEntryMessagesEn
	}
	return &secretEntryMessagesZh
}

var secretEntryMessagesZh = secretEntryMessages{
	Title:            "设置本机密钥值",
	ProjectFmt:       "项目：%s（值保存到此项目隔离的本机存储位置）",
	RefFmt:           "引用：{{secret.%s}}",
	SourceHint:       "项目文件只保存名称与 {{secret.名称}} 引用；真实值不会写入项目 yaml、普通变量或导出内容。",
	ADRHint:          "请求发送时从所选本机存储位置读取；存储不可用或锁定时会明确报错，不会静默降级。环境变量 GOREQUEST_SECRET_<NAME> 仍为只读覆盖。",
	NameLabel:        "密钥名称",
	NamePlaceholder:  "密钥名称（例如 apiToken）",
	ValueLabel:       "真实值（写入所选存储位置）",
	ValuePlaceholder: "粘贴或输入真实值",
	ValueHint:        "密码输入，不会回显；保存后立即清空临时输入。",
	StatusLabel:      "绑定状态：",
	StatusBoundFmt:   "已绑定（%s）",
	StatusUnavailFmt: "后端不可用（%s）",
	StatusMissing:    "未设置",
	SetValue:         "设置值",
	UpdateValue:      "更新本机值",
	Delete:           "删除本机值",
	DeleteConfirmFmt: "确认删除本机值 %q？项目中的名称声明不会被删除。",
	SavedFmt:         "已把 %q 的值写入所选本机存储位置。",
	DeletedFmt:       "已删除 %q 的本机值。",
	BackendFailed:    "无法访问所选存储位置：%s",
	InvalidName:      "密钥名称无效：%s",
	Empty:            "请输入密钥名称和值。",
	Cancel:           "取消",
	EnvSetValue:      "设置值",
	EnvUpdateValue:   "更新本机值",
	EnvStatusBound:   "已绑定",
	EnvStatusMissing: "未设置",
	EnvStatusUnavail: "后端不可用",
	EnvHint:          "每个名称旁点“设置值/更新本机值”填写真实值；只写所选本机存储位置，不写入项目文件。",
	CredSetValue:     "设置/更新值",

	BackendFmt:            "存储位置：%s",
	BackendPathFmt:        "实际路径/服务：%s",
	BackendPlaintext:      "本地文件为明文存储，未加密。",
	BackendUnavailableFmt: "存储不可用：%s",
	ChangeStorage:         "更改存储位置",
}

var secretEntryMessagesEn = secretEntryMessages{
	Title:            "Set local secret value",
	ProjectFmt:       "Project: %s (the value is stored in this project's isolated local storage location)",
	RefFmt:           "Reference: {{secret.%s}}",
	SourceHint:       "Project files keep only names and {{secret.name}} references; real values never go into project YAML, ordinary variables or exports.",
	ADRHint:          "Requests read the value from the selected local storage location at send time; an unavailable or locked store reports an error instead of falling back silently. GOREQUEST_SECRET_<NAME> stays a read-only override.",
	NameLabel:        "Secret name",
	NamePlaceholder:  "Secret name (e.g. apiToken)",
	ValueLabel:       "Real value (written to the selected storage location)",
	ValuePlaceholder: "Paste or type the real value",
	ValueHint:        "Password input is never echoed; the temporary value is cleared immediately after saving.",
	StatusLabel:      "Binding status:",
	StatusBoundFmt:   "Bound (%s)",
	StatusUnavailFmt: "Backend unavailable (%s)",
	StatusMissing:    "Not set",
	SetValue:         "Set value",
	UpdateValue:      "Update local value",
	Delete:           "Delete local value",
	DeleteConfirmFmt: "Delete the local value of %q? The declared name in the project is not removed.",
	SavedFmt:         "Saved the value of %q to the selected local storage location.",
	DeletedFmt:       "Deleted the local value of %q.",
	BackendFailed:    "Cannot access the selected storage location: %s",
	InvalidName:      "Invalid secret name: %s",
	Empty:            "Enter a secret name and value.",
	Cancel:           "Cancel",
	EnvSetValue:      "Set value",
	EnvUpdateValue:   "Update local value",
	EnvStatusBound:   "Bound",
	EnvStatusMissing: "Not set",
	EnvStatusUnavail: "Backend unavailable",
	EnvHint:          "Use “Set value / Update local value” beside each declared name to enter the real value; only the selected local storage location is written, never a project file.",
	CredSetValue:     "Set / update value",

	BackendFmt:            "Storage location: %s",
	BackendPathFmt:        "Actual path/service: %s",
	BackendPlaintext:      "The local file stores plaintext, not ciphertext.",
	BackendUnavailableFmt: "Storage unavailable: %s",
	ChangeStorage:         "Change storage location",
}
