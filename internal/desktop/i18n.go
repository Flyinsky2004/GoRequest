package desktop

import (
	"fmt"
	"strings"
)

// This file is the single, centralized translation catalog for the desktop UI.
// Every user-visible string that should change with the interface language lives
// here; views only reference message fields, never hard-coded natural language.
//
// Design notes:
//   - User data, HTTP protocol field values, storage sentinels (e.g. the
//     "(none)" environment and "(root)" folder markers), template syntax such as
//     {{secret.name}} and technical identifiers are deliberately NOT translated.
//   - Both catalogs are structs, so a missing translation is a compile-time
//     visible empty field and is caught by TestMessagesCoverEveryField.
//   - An unknown or absent stored language falls back to Simplified Chinese,
//     which is also the default for a fresh install.

// language is an interface language persisted in the local config file.
type language string

const (
	// langSimplifiedChinese is the default interface language.
	langSimplifiedChinese language = "zh-CN"
	// langEnglish is the English interface language.
	langEnglish language = "en"
)

// The language selector shows each language in its own script, independent of
// the current interface language, so users can always find their language.
const (
	languageLabelChinese = "简体中文"
	languageLabelEnglish = "English"
)

// defaultLanguage is the language used when nothing is stored yet.
func defaultLanguage() language { return langSimplifiedChinese }

// parseLanguage normalizes a stored preference to a supported language. Empty,
// unknown or unsupported values fall back to the default Simplified Chinese, so
// a config written by an older build (which had no language) keeps working.
func parseLanguage(v string) language {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "en", "en-us", "en-gb", "english":
		return langEnglish
	default:
		return langSimplifiedChinese
	}
}

// languageFromLabel maps a selector label back to a language.
func languageFromLabel(label string) language {
	if label == languageLabelEnglish {
		return langEnglish
	}
	return langSimplifiedChinese
}

// languageLabel returns a language's own display label.
func languageLabel(l language) string {
	if parseLanguage(string(l)) == langEnglish {
		return languageLabelEnglish
	}
	return languageLabelChinese
}

// messages holds every translatable string for one language. Fields with
// positional verbs (%s/%d) are formatted with fmt.Sprintf by the caller.
type messages struct {
	// Common actions and words.
	Cancel   string
	Discard  string
	Save     string
	Close    string
	Create   string
	Settings string
	Done     string
	Open     string
	Error    string
	Key      string
	Value    string

	// Welcome / project chooser.
	WelcomeTagline  string
	RecentProjects  string
	WelcomeEmpty    string
	OpenProject     string
	ChooseFolderTip string
	Projects        string
	ProjectsTip     string

	// Status line and validation feedback.
	OpenFailed          string // %s
	WindowNotReady      string
	SwitchedTo          string // %s
	OpenedWithErrors    string // %s, %d
	OpenedRequests      string // %s, %d
	Saved               string // %s
	Conflict            string // %s
	SaveFailedPrefix    string // %s
	RequestFailedPrefix string // %s
	Sending             string // method, url
	NewDocFailed        string // %s
	Created             string // %s
	EnvironmentStatus   string // %s
	MovedStatus         string // %s
	MoveFailedPrefix    string // %s
	CreatedFolder       string // %s
	CreatedEnvironment  string // %s
	SavedEnvironment    string // %s
	LanguageStatus      string // %s
	AppearanceStatus    string // %s
	StartupOn           string
	StartupOff          string

	// Navigation rail.
	ProjectContents string
	OpenSettingsTip string

	// Workspace header and empty states.
	NoProjectLoaded    string
	ChooseRequestOrDoc string
	EnvLabel           string
	NewEnv             string
	EditEnv            string
	CreateEnvTip       string
	EditEnvTip         string
	SelectEnvTip       string
	SelectEnvHint      string
	NewRequest         string
	MoveEllipsis       string
	MoveTip            string
	NewDoc             string
	CloseProject       string
	CloseProjectTip    string
	CopyProjectPath    string

	// Request editor.
	ParamsTab      string
	HeadersTab     string
	BodyTab        string
	AuthTab        string
	ScriptsTab     string
	TestsTab       string
	BuildSettings  string
	NoScriptsHint  string
	NoTestsHint    string
	NoSettingsHint string
	BodyType       string
	TimeoutLabel   string
	TokenHint      string
	Username       string
	Password       string
	NoAuth         string
	Send           string
	CancelRequest  string
	AddRow         string
	RowNoKey       string // %d, %q
	DragTip        string
	VarLabel       string
	AutoHeaderTip  string
	UndefinedVars  string // %d
	SendGuardTitle string
	SendGuardHint  string // %s
	SendAnyway     string
	GoDefine       string
	KeyValueMode   string
	TextMode       string

	// Document editor.
	SplitView   string
	EditView    string
	PreviewView string
	SaveDoc     string

	// Response panel.
	SendingLabel      string
	CanceledLabel     string
	ErrorLabel        string
	NoResponseYet     string
	NoResponseHint    string
	SendShortcutHint  string
	CopyResponse      string
	BodySection       string
	HeadersSection    string
	Pretty            string
	Raw               string
	BodyEmpty         string
	NoResponseHeaders string
	TruncatedNotice   string
	Retry             string
	LargeResponseNote string
	Copied            string

	// Environment editor.
	NewEnvTitle           string
	EditEnvTitle          string // %s
	ProjectLabel          string // %s
	EnvIDLabel            string
	EnvIDLockedHint       string
	EnvIDPlaceholder      string
	EnvIDHint             string
	VariablesLabel        string
	AddVariable           string
	SecretNamesLabel      string
	SecretNamesHint       string
	AddSecretName         string
	SecretPlaceholder     string
	DiscardEnvConfirm     string
	KeepEditing           string
	ErrEnvDuplicateID     string // %q
	ErrEnvRowNoKey        string // %d, %q
	ErrEnvInvalidVarKey   string // %d, %q
	ErrEnvDuplicateVarKey string // %q
	ErrEnvInvalidSecret   string // %d, %q
	ErrEnvDuplicateSecret string // %q
	ErrSelectEnvToEdit    string
	ErrEnvNotFound        string // %q
	ErrEnvNoFile          string // %q
	ErrOpenProjectFirst   string

	// Dialogs (new request/folder, move, dirty close).
	NewRequestTitle        string
	TargetFolder           string // %s
	NameLabel              string
	RequestNamePlaceholder string
	ErrEnterRequestName    string
	NewFolderTitle         string
	ParentFolder           string // %s
	FolderNamePlaceholder  string
	ErrEnterFolderName     string
	CreateFolder           string
	MoveTitle              string
	MoveHint               string
	SaveAndMove            string
	DiscardAndMove         string
	MoveAction             string
	UnsavedChangesTitle    string
	UnsavedChangesMsg      string // %q
	SaveAndClose           string

	// Close guard (window / quit).
	GuardTitle       string
	GuardItems       string
	GuardEnvironment string // %s
	SaveAll          string

	// Tab strip and menu.
	OpenTabsMenu string // %d
	SwitchTab    string
	CloseTabMenu string
	CloseCurrent string
	CloseOthers  string
	CloseAll     string
	CloseTabTip  string // %s

	// Left tree.
	RequestsGroup       string
	AddFolder           string
	AddFolderTip        string
	AddDocTip           string
	NewFolder           string
	NoRequests          string
	DocumentsGroup      string
	NoDocuments         string
	FolderTip           string // %s
	FilterPlaceholder   string
	NoMatch             string
	HistoryLabel        string
	HistoryEmpty        string
	ClearHistory        string
	ConfirmClearHistory string
	Today               string
	Yesterday           string

	// Settings sheet.
	SettingsTitle       string
	AppearanceLabel     string
	AppearanceSystem    string
	AppearanceLight     string
	AppearanceDark      string
	LanguageLabel       string
	StartupLabel        string
	RestoreLast         string
	VersionLabel        string
	PlatformLabel       string
	GoLabel             string
	ConfigFileLabel     string
	RecentProjectsLabel string
	SecretNote          string
	InMemoryConfig      string

	// Native dialog titles/messages.
	OpenDialogTitle   string
	OpenDialogMessage string
}

// messagesZh is the Simplified Chinese catalog (the default).
var messagesZh = messages{
	Cancel:   "取消",
	Discard:  "放弃",
	Save:     "保存",
	Close:    "关闭",
	Create:   "创建",
	Settings: "设置",
	Done:     "完成",
	Open:     "打开",
	Error:    "错误",
	Key:      "键",
	Value:    "值",

	WelcomeTagline:  "打开一个项目开始。每个项目在自己的窗口中打开。",
	RecentProjects:  "最近项目",
	WelcomeEmpty:    "还没有项目。打开一个以开始。",
	OpenProject:     "打开项目…",
	ChooseFolderTip: "选择包含 .gorequest/ 的文件夹",
	Projects:        "项目",
	ProjectsTip:     "显示项目选择窗口",

	OpenFailed:          "打开失败：%s",
	WindowNotReady:      "打开失败：窗口未就绪",
	SwitchedTo:          "已切换到 %s",
	OpenedWithErrors:    "已打开 %s（%d 个校验错误）",
	OpenedRequests:      "已打开 %s（%d 个请求）",
	Saved:               "已保存 %s",
	Conflict:            "冲突：%s 已在磁盘上更改；重新打开并合并后再保存",
	SaveFailedPrefix:    "保存：%s",
	RequestFailedPrefix: "请求：%s",
	Sending:             "正在发送 %s %s",
	NewDocFailed:        "新建文档：%s",
	Created:             "已创建 %s",
	EnvironmentStatus:   "环境：%s",
	MovedStatus:         "已移动 %s",
	MoveFailedPrefix:    "移动：%s",
	CreatedFolder:       "已创建文件夹 %s",
	CreatedEnvironment:  "已创建环境 %s",
	SavedEnvironment:    "已保存环境 %s",
	LanguageStatus:      "语言：%s",
	AppearanceStatus:    "外观：%s",
	StartupOn:           "启动：恢复上次项目已开启",
	StartupOff:          "启动：始终显示项目选择",

	ProjectContents: "项目内容",
	OpenSettingsTip: "打开设置",

	NoProjectLoaded:    "未加载项目。",
	ChooseRequestOrDoc: "从左侧选择请求或文档。",
	EnvLabel:           "环境",
	NewEnv:             "新建环境",
	EditEnv:            "编辑环境",
	CreateEnvTip:       "创建新环境",
	EditEnvTip:         "编辑所选环境",
	SelectEnvTip:       "选择要编辑的环境",
	SelectEnvHint:      "选择要编辑的环境",
	NewRequest:         "新建请求",
	MoveEllipsis:       "移动…",
	MoveTip:            "将此请求移动到文件夹",
	NewDoc:             "新建文档",
	CloseProject:       "关闭项目",
	CloseProjectTip:    "关闭此项目窗口（会提示保存未保存的更改）",
	CopyProjectPath:    "复制项目路径",

	ParamsTab:      "参数",
	HeadersTab:     "头部",
	BodyTab:        "正文",
	AuthTab:        "鉴权",
	ScriptsTab:     "脚本",
	TestsTab:       "测试",
	BuildSettings:  "设置",
	NoScriptsHint:  "暂无脚本。",
	NoTestsHint:    "暂无测试。",
	NoSettingsHint: "当前没有可配置项。",
	BodyType:       "正文类型",
	TimeoutLabel:   "超时",
	TokenHint:      "令牌（使用 {{secret.name}} 将值保留在文件之外）",
	Username:       "用户名",
	Password:       "密码",
	NoAuth:         "无鉴权。",
	Send:           "发送",
	CancelRequest:  "取消",
	AddRow:         "添加行",
	RowNoKey:       "第 %d 行：值 %q 没有键",
	DragTip:        "拖拽排序（默认表格视图）",
	VarLabel:       "变量",
	AutoHeaderTip:  "由请求内容自动生成",
	UndefinedVars:  "%d 个变量未定义",
	SendGuardTitle: "存在未定义变量",
	SendGuardHint:  "以下变量在当前环境未定义：%s。发送前请定义，或仍然发送。",
	SendAnyway:     "仍然发送",
	GoDefine:       "去定义",
	KeyValueMode:   "键值对",
	TextMode:       "文本",

	SplitView:   "分栏",
	EditView:    "编辑",
	PreviewView: "预览",
	SaveDoc:     "保存文档",

	SendingLabel:      "发送中…",
	CanceledLabel:     "已取消",
	ErrorLabel:        "错误",
	NoResponseYet:     "尚无响应",
	NoResponseHint:    "发送请求以在此查看响应。",
	SendShortcutHint:  "⌘↩ 发送 · ⌘L 聚焦地址栏",
	CopyResponse:      "复制响应",
	BodySection:       "正文",
	HeadersSection:    "头部",
	Pretty:            "美化",
	Raw:               "原始",
	BodyEmpty:         "响应正文为空。",
	NoResponseHeaders: "无响应头。",
	TruncatedNotice:   "[已在 32 MB 处截断]",
	Retry:             "重试",
	LargeResponseNote: "响应较大，已降级为纯文本显示。",
	Copied:            "已复制",

	NewEnvTitle:           "新建环境",
	EditEnvTitle:          "编辑环境：%s",
	ProjectLabel:          "项目：%s",
	EnvIDLabel:            "环境 ID",
	EnvIDLockedHint:       "ID 是请求使用的稳定引用，编辑时保持不变。",
	EnvIDPlaceholder:      "例如 staging",
	EnvIDHint:             "用作环境标识符和文件名。",
	VariablesLabel:        "变量",
	AddVariable:           "添加变量",
	SecretNamesLabel:      "密钥名称",
	SecretNamesHint:       "仅声明（以 {{secret.name}} 引用）。值保存在本地密钥存储中，永不在此显示或保存。",
	AddSecretName:         "添加密钥名称",
	SecretPlaceholder:     "密钥名称",
	DiscardEnvConfirm:     "放弃对此环境的未保存更改？",
	KeepEditing:           "继续编辑",
	ErrEnvDuplicateID:     "环境 ID %q 已存在",
	ErrEnvRowNoKey:        "第 %d 行：值 %q 没有键",
	ErrEnvInvalidVarKey:   "第 %d 行：无效变量键 %q（不能包含花括号）",
	ErrEnvDuplicateVarKey: "重复的变量键 %q",
	ErrEnvInvalidSecret:   "第 %d 个密钥：无效名称 %q",
	ErrEnvDuplicateSecret: "重复的密钥名称 %q",
	ErrSelectEnvToEdit:    "选择要编辑的环境",
	ErrEnvNotFound:        "环境 %q 未找到",
	ErrEnvNoFile:          "环境 %q 没有可编辑的已保存文件",
	ErrOpenProjectFirst:   "请先打开项目再创建环境",

	NewRequestTitle:        "新建请求",
	TargetFolder:           "目标文件夹：%s",
	NameLabel:              "名称",
	RequestNamePlaceholder: "请求名称",
	ErrEnterRequestName:    "请输入请求名称。",
	NewFolderTitle:         "新建文件夹",
	ParentFolder:           "父文件夹：%s",
	FolderNamePlaceholder:  "文件夹名称",
	ErrEnterFolderName:     "请输入文件夹名称。",
	CreateFolder:           "创建文件夹",
	MoveTitle:              "移动请求",
	MoveHint:               "选择目标文件夹。请求会保留其稳定 ID。",
	SaveAndMove:            "保存并移动",
	DiscardAndMove:         "放弃并移动",
	MoveAction:             "移动",
	UnsavedChangesTitle:    "未保存的更改",
	UnsavedChangesMsg:      "%q 有未保存的更改。关闭前保存吗？",
	SaveAndClose:           "保存并关闭",

	GuardTitle:       "未保存的更改",
	GuardItems:       "以下项有未保存的更改：",
	GuardEnvironment: "环境：%s",
	SaveAll:          "全部保存",

	OpenTabsMenu: "已打开标签（%d）",
	SwitchTab:    "切换",
	CloseTabMenu: "关闭",
	CloseCurrent: "关闭当前",
	CloseOthers:  "关闭其他",
	CloseAll:     "关闭全部",
	CloseTabTip:  "关闭 %s",

	RequestsGroup:       "请求",
	AddFolder:           "+ 文件夹",
	AddFolderTip:        "创建新文件夹",
	AddDocTip:           "新建文档",
	NewFolder:           "新建文件夹",
	NoRequests:          "无请求",
	DocumentsGroup:      "文档",
	NoDocuments:         "无文档",
	FolderTip:           "文件夹：%s",
	FilterPlaceholder:   "过滤请求…",
	NoMatch:             "没有匹配的请求",
	HistoryLabel:        "历史",
	HistoryEmpty:        "暂无历史；发送请求后会记录在这里",
	ClearHistory:        "清空历史",
	ConfirmClearHistory: "确认清空本项目的全部请求历史？此操作不可恢复。",
	Today:               "今天",
	Yesterday:           "昨天",

	SettingsTitle:       "设置",
	AppearanceLabel:     "外观",
	AppearanceSystem:    "跟随系统",
	AppearanceLight:     "浅色",
	AppearanceDark:      "深色",
	LanguageLabel:       "语言",
	StartupLabel:        "启动",
	RestoreLast:         "启动时恢复上次打开的项目",
	VersionLabel:        "版本",
	PlatformLabel:       "平台",
	GoLabel:             "Go",
	ConfigFileLabel:     "配置文件",
	RecentProjectsLabel: "最近项目",
	SecretNote:          "密钥值来自名为 GOREQUEST_SECRET_<NAME> 的环境变量；项目文件只保留 {{secret.name}} 引用。",
	InMemoryConfig:      "内存中（无配置目录）",

	OpenDialogTitle:   "打开 GoRequest 项目",
	OpenDialogMessage: "选择包含 .gorequest/ 目录的文件夹。",
}

// messagesEn is the English catalog.
var messagesEn = messages{
	Cancel:   "Cancel",
	Discard:  "Discard",
	Save:     "Save",
	Close:    "Close",
	Create:   "Create",
	Settings: "Settings",
	Done:     "Done",
	Open:     "Open",
	Error:    "Error",
	Key:      "Key",
	Value:    "Value",

	WelcomeTagline:  "Open a project to start. Each project opens in its own window.",
	RecentProjects:  "Recent Projects",
	WelcomeEmpty:    "No projects yet. Open one to get started.",
	OpenProject:     "Open Project…",
	ChooseFolderTip: "Choose a folder containing .gorequest/",
	Projects:        "Projects",
	ProjectsTip:     "Show the project chooser",

	OpenFailed:          "open failed: %s",
	WindowNotReady:      "open failed: window not ready",
	SwitchedTo:          "switched to %s",
	OpenedWithErrors:    "opened %s with %d validation error(s)",
	OpenedRequests:      "opened %s (%d requests)",
	Saved:               "saved %s",
	Conflict:            "conflict: %s changed on disk; reopen to merge before saving",
	SaveFailedPrefix:    "save: %s",
	RequestFailedPrefix: "request: %s",
	Sending:             "sending %s %s",
	NewDocFailed:        "new doc: %s",
	Created:             "created %s",
	EnvironmentStatus:   "environment: %s",
	MovedStatus:         "moved %s",
	MoveFailedPrefix:    "move: %s",
	CreatedFolder:       "created folder %s",
	CreatedEnvironment:  "created environment %s",
	SavedEnvironment:    "saved environment %s",
	LanguageStatus:      "language: %s",
	AppearanceStatus:    "appearance: %s",
	StartupOn:           "startup: restore last project on",
	StartupOff:          "startup: always show project chooser",

	ProjectContents: "Project contents",
	OpenSettingsTip: "Open settings",

	NoProjectLoaded:    "No project loaded.",
	ChooseRequestOrDoc: "Choose a request or document on the left.",
	EnvLabel:           "Env",
	NewEnv:             "New Env",
	EditEnv:            "Edit Env",
	CreateEnvTip:       "Create a new environment",
	EditEnvTip:         "Edit the selected environment",
	SelectEnvTip:       "Select an environment to edit",
	SelectEnvHint:      "Select an environment to edit",
	NewRequest:         "New Request",
	MoveEllipsis:       "Move…",
	MoveTip:            "Move this request to a folder",
	NewDoc:             "New Doc",
	CloseProject:       "Close Project",
	CloseProjectTip:    "Close this project window (asks to save unsaved changes)",
	CopyProjectPath:    "Copy Project Path",

	ParamsTab:      "Params",
	HeadersTab:     "Headers",
	BodyTab:        "Body",
	AuthTab:        "Auth",
	ScriptsTab:     "Scripts",
	TestsTab:       "Tests",
	BuildSettings:  "Settings",
	NoScriptsHint:  "No scripts.",
	NoTestsHint:    "No tests.",
	NoSettingsHint: "Nothing to configure here.",
	BodyType:       "Body type",
	TimeoutLabel:   "Timeout",
	TokenHint:      "Token (use {{secret.name}} to keep the value out of the file)",
	Username:       "Username",
	Password:       "Password",
	NoAuth:         "No auth.",
	Send:           "Send",
	CancelRequest:  "Cancel",
	AddRow:         "Add row",
	RowNoKey:       "row %d: value %q has no key",
	DragTip:        "Drag to reorder (table view)",
	VarLabel:       "Vars",
	AutoHeaderTip:  "Generated automatically from the request",
	UndefinedVars:  "%d undefined variable(s)",
	SendGuardTitle: "Undefined variables",
	SendGuardHint:  "These variables are undefined in the current environment: %s. Define them, or send anyway.",
	SendAnyway:     "Send anyway",
	GoDefine:       "Define",
	KeyValueMode:   "Key:Value",
	TextMode:       "Text",

	SplitView:   "Split",
	EditView:    "Edit",
	PreviewView: "Preview",
	SaveDoc:     "Save Doc",

	SendingLabel:      "Sending…",
	CanceledLabel:     "Canceled",
	ErrorLabel:        "Error",
	NoResponseYet:     "No response yet",
	NoResponseHint:    "Send a request to see its response here.",
	SendShortcutHint:  "⌘↩ send · ⌘L focus address",
	CopyResponse:      "Copy response",
	BodySection:       "Body",
	HeadersSection:    "Headers",
	Pretty:            "Pretty",
	Raw:               "Raw",
	BodyEmpty:         "Response body is empty.",
	NoResponseHeaders: "No response headers.",
	TruncatedNotice:   "[truncated at 32 MB]",
	Retry:             "Retry",
	LargeResponseNote: "Large response shown as plain text.",
	Copied:            "Copied",

	NewEnvTitle:           "New Environment",
	EditEnvTitle:          "Edit Environment: %s",
	ProjectLabel:          "Project: %s",
	EnvIDLabel:            "Environment ID",
	EnvIDLockedHint:       "The ID is the stable reference requests use; it stays fixed when editing.",
	EnvIDPlaceholder:      "e.g. staging",
	EnvIDHint:             "Used as the environment identifier and file name.",
	VariablesLabel:        "Variables",
	AddVariable:           "Add variable",
	SecretNamesLabel:      "Secret names",
	SecretNamesHint:       "Declarations only (referenced as {{secret.name}}). Values stay in the local secret store and are never shown or saved here.",
	AddSecretName:         "Add secret name",
	SecretPlaceholder:     "secret name",
	DiscardEnvConfirm:     "Discard unsaved changes to this environment?",
	KeepEditing:           "Keep editing",
	ErrEnvDuplicateID:     "environment id %q already exists",
	ErrEnvRowNoKey:        "row %d: value %q has no key",
	ErrEnvInvalidVarKey:   "row %d: invalid variable key %q (must not contain braces)",
	ErrEnvDuplicateVarKey: "duplicate variable key %q",
	ErrEnvInvalidSecret:   "secret %d: invalid name %q",
	ErrEnvDuplicateSecret: "duplicate secret name %q",
	ErrSelectEnvToEdit:    "select an environment to edit",
	ErrEnvNotFound:        "environment %q not found",
	ErrEnvNoFile:          "environment %q has no saved file to edit",
	ErrOpenProjectFirst:   "open a project before creating an environment",

	NewRequestTitle:        "New Request",
	TargetFolder:           "Target folder: %s",
	NameLabel:              "Name",
	RequestNamePlaceholder: "Request name",
	ErrEnterRequestName:    "Enter a request name.",
	NewFolderTitle:         "New Folder",
	ParentFolder:           "Parent folder: %s",
	FolderNamePlaceholder:  "Folder name",
	ErrEnterFolderName:     "Enter a folder name.",
	CreateFolder:           "Create Folder",
	MoveTitle:              "Move Request",
	MoveHint:               "Choose a destination folder. The request keeps its stable ID.",
	SaveAndMove:            "Save & Move",
	DiscardAndMove:         "Discard & Move",
	MoveAction:             "Move",
	UnsavedChangesTitle:    "Unsaved Changes",
	UnsavedChangesMsg:      "%q has unsaved changes. Save before closing?",
	SaveAndClose:           "Save & Close",

	GuardTitle:       "Unsaved changes",
	GuardItems:       "These items have changes that are not saved:",
	GuardEnvironment: "Environment: %s",
	SaveAll:          "Save All",

	OpenTabsMenu: "Open Tabs (%d)",
	SwitchTab:    "Switch",
	CloseTabMenu: "Close",
	CloseCurrent: "Close Current",
	CloseOthers:  "Close Others",
	CloseAll:     "Close All",
	CloseTabTip:  "Close %s",

	RequestsGroup:       "Requests",
	AddFolder:           "+ Folder",
	AddFolderTip:        "Create a new folder",
	AddDocTip:           "New Doc",
	NewFolder:           "New Folder",
	NoRequests:          "No requests",
	DocumentsGroup:      "Documents",
	NoDocuments:         "No documents",
	FolderTip:           "Folder: %s",
	FilterPlaceholder:   "Filter requests…",
	NoMatch:             "No matching requests",
	HistoryLabel:        "History",
	HistoryEmpty:        "No history yet — sends will be recorded here",
	ClearHistory:        "Clear History",
	ConfirmClearHistory: "Clear all request history for this project? This cannot be undone.",
	Today:               "Today",
	Yesterday:           "Yesterday",

	SettingsTitle:       "Settings",
	AppearanceLabel:     "Appearance",
	AppearanceSystem:    "System",
	AppearanceLight:     "Light",
	AppearanceDark:      "Dark",
	LanguageLabel:       "Language",
	StartupLabel:        "Startup",
	RestoreLast:         "Restore last opened project on startup",
	VersionLabel:        "Version",
	PlatformLabel:       "Platform",
	GoLabel:             "Go",
	ConfigFileLabel:     "Config file",
	RecentProjectsLabel: "Recent projects",
	SecretNote:          "Secret values come from environment variables named GOREQUEST_SECRET_<NAME>; project files keep only {{secret.name}} references.",
	InMemoryConfig:      "in-memory (no config directory)",

	OpenDialogTitle:   "Open GoRequest Project",
	OpenDialogMessage: "Choose a folder that contains a .gorequest/ directory.",
}

// messagesFor returns the catalog for a language, falling back to the default
// Simplified Chinese catalog for anything unrecognized.
func messagesFor(l language) *messages {
	if parseLanguage(string(l)) == langEnglish {
		return &messagesEn
	}
	return &messagesZh
}

// englishMessages is used by pure helper functions whose unit tests expect
// English wording and which have no config/App context.
var englishMessages = &messagesEn

// msg returns the catalog for the app window's current language. It is read on
// every frame, so a language change applies on the next draw of every window.
func (a *App) msg() *messages {
	if a.mgr == nil || a.mgr.cfg == nil {
		return messagesFor(defaultLanguage())
	}
	return messagesFor(a.mgr.cfg.language())
}

// msg returns the catalog for the welcome window's current language.
func (w *welcomeState) msg() *messages {
	if w.mgr == nil || w.mgr.cfg == nil {
		return messagesFor(defaultLanguage())
	}
	return messagesFor(w.mgr.cfg.language())
}

// translate formats a catalog string that contains positional verbs.
func translate(format string, args ...any) string {
	if len(args) == 0 {
		return format
	}
	return fmt.Sprintf(format, args...)
}
