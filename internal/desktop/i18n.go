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

	// New-project wizard and the personal workspace.
	NewProject               string
	NewProjectTip            string
	NewProjectTitle          string
	NewProjectHint           string
	ProjectNamePlaceholder   string
	ProjectLocationLabel     string
	Browse                   string
	ChooseProjectLocation    string
	ChooseProjectLocationMsg string
	NewProjectFolderHint     string
	ErrEnterProjectName      string
	ErrEnterProjectLocation  string
	NewProjectFailed         string // %s
	CreatedProject           string // %s
	GlobalWorkspace          string
	GlobalWorkspaceTip       string
	GlobalWorkspaceTitle     string
	GlobalWorkspaceOpened    string

	// Recent-list management.
	RecentSearchPlaceholder string
	RecentNoMatch           string
	PinRecent               string
	UnpinRecent             string
	RemoveRecent            string
	RemovedRecent           string // %s

	// Project <-> global workspace transfer.
	CopyToGlobal            string
	MoveToGlobal            string
	CopyToProject           string
	MoveToProject           string
	TransferSameWorkspace   string
	TransferTitle           string
	TransferTarget          string
	TransferIDCopy          string // %s
	TransferIDMove          string // %s
	TransferDirtyCopy       string
	TransferDirtyMove       string // %s
	TransferSecrets         string // %s
	TransferBlocked         string
	TransferConfirm         string
	TransferDone            string // %s
	TransferFailed          string // %s
	TransferWarnInline      string // %s
	TransferWarnAuth        string // %s
	TransferWarnVar         string // %s
	TransferWarnDoc         string // %s
	TransferWarnLocalFile   string // %s
	TransferWarnDefaultAuth string // %s

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
	Rename         string
	OpenRow        string
	NewRequestHere string
	Duplicate      string
	DuplicateDoc   string
	MoveTo         string
	Delete         string
	Undo           string
	UndoDone       string
	DeletedToast   string // %s
	Description    string
	AddParamHint   string
	QuickOpen      string
	ToggleSidebar  string
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
	SaveResponse      string
	FindPlaceholder   string
	FindCount         string // %d, %d
	FindNone          string
	SavedResponse     string // %s
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
	CloseRight   string
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

	// History filters, preview and diagnostics (§3).
	HistoryThisProject       string
	HistoryAllProjects       string
	HistoryFilterPlaceholder string
	HistoryTimeAll           string
	HistoryTimeToday         string
	HistoryTimeWeek          string
	HistoryTimeMonth         string
	HistoryResultAll         string
	HistoryResultSuccess     string
	HistoryResultClient      string
	HistoryResultServer      string
	HistoryResultNetwork     string
	HistoryResultCanceled    string
	HistoryNoMatch           string
	HistoryUnavailable       string
	HistoryPrivacyNote       string
	HistoryDetailTitle       string
	HistoryDetailSummary     string
	HistoryDetailResult      string
	HistoryDetailRequest     string
	HistoryDetailResponse    string
	HistoryDetailOpen        string
	HistoryDetailDelete      string
	HistoryRedactedNote      string
	HistoryRequestGone       string
	HistoryProjectNotOpen    string
	HistoryBodyOmitted       string // %s
	HistoryOmitPolicy        string
	HistoryOmitLarge         string
	HistoryOmitBinary        string
	HistoryOmitTruncated     string
	HistorySnapshotTrimmed   string
	HistoryDiagCorrupt       string // %d
	HistoryDiagRead          string // %s
	HistoryWriteFailed       string // %s
	HistoryReadFailed        string // %s
	HistoryOutcomeError      string
	HistoryOutcomeCanceled   string
	HistoryOutcomeTimeout    string

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

	// Workbench preferences (B02).
	EditorSection         string
	FontSizeLabel         string
	FontSizeStatus        string // %d
	TabSizeLabel          string
	SoftWrapLabel         string
	LineNumbersLabel      string
	AutoSaveSection       string
	AutoSaveEnable        string
	AutoSaveDelayLabel    string
	AutoSaveSafetyNote    string
	AutoSaveOnStatus      string // %s
	AutoSaveOffStatus     string
	AutoSaveSkipped       string // %d
	AutoSaveSaved         string // %d
	ShortcutsSection      string
	ShortcutsNote         string
	ShortcutNewRequest    string
	ShortcutCloseTab      string
	ShortcutTabSwitch     string
	ShortcutFocusURL      string
	ShortcutQuickSearch   string
	ShortcutEditEnv       string
	ShortcutToggleSidebar string
	ShortcutUndoDelete    string
	ShortcutRename        string

	// History settings.
	HistorySettingsLabel    string
	HistorySaveSnapshots    string
	HistorySaveBodies       string
	HistoryRetentionLabel   string
	HistoryRetention30      string
	HistoryRetention90      string
	HistoryRetention365     string
	HistoryRetentionForever string
	HistorySettingsNote     string

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

	NewProject:               "新建项目…",
	NewProjectTip:            "在所选位置创建一个新的 .gorequest 项目",
	NewProjectTitle:          "新建项目",
	NewProjectHint:           "在选定位置创建一个新的 GoRequest 项目；不会写入现有文件。",
	ProjectNamePlaceholder:   "例如 订单 API",
	ProjectLocationLabel:     "位置",
	Browse:                   "浏览…",
	ChooseProjectLocation:    "选择项目位置",
	ChooseProjectLocationMsg: "新项目会创建为所选文件夹下的一个子文件夹。",
	NewProjectFolderHint:     "项目会创建在「位置/名称」子文件夹中，该文件夹必须不存在或为空。",
	ErrEnterProjectName:      "请输入项目名称。",
	ErrEnterProjectLocation:  "请选择项目位置。",
	NewProjectFailed:         "新建项目：%s",
	CreatedProject:           "已创建项目 %s",
	GlobalWorkspace:          "个人工作区",
	GlobalWorkspaceTip:       "打开保存在本机的临时请求与个人集合",
	GlobalWorkspaceTitle:     "个人工作区",
	GlobalWorkspaceOpened:    "已打开个人工作区（本机数据目录，不属于任何 Git 项目）",

	RecentSearchPlaceholder: "搜索最近项目…",
	RecentNoMatch:           "没有匹配的最近项目",
	PinRecent:               "固定到顶部",
	UnpinRecent:             "取消固定",
	RemoveRecent:            "从最近列表移除",
	RemovedRecent:           "已从最近列表移除 %s",

	CopyToGlobal:            "复制到个人工作区",
	MoveToGlobal:            "移动到个人工作区",
	CopyToProject:           "复制到项目",
	MoveToProject:           "移动到项目",
	TransferSameWorkspace:   "源和目标工作区相同",
	TransferTitle:           "跨工作区转移预览",
	TransferTarget:          "目标文件",
	TransferIDCopy:          "复制会分配新 ID：%s",
	TransferIDMove:          "移动会保留稳定 ID：%s",
	TransferDirtyCopy:       "该请求有未保存编辑；只会复制已保存的版本。",
	TransferDirtyMove:       "「%s」有未保存编辑，请先保存或放弃后再移动。",
	TransferSecrets:         "保留的密钥引用（值不会复制）：%s",
	TransferBlocked:         "已阻止：请先把内联凭据或字面 Cookie 改为 {{secret.name}} 引用。",
	TransferConfirm:         "确认转移",
	TransferDone:            "已转移到 %s",
	TransferFailed:          "转移：%s",
	TransferWarnInline:      "内联敏感值：%s（不会复制）",
	TransferWarnAuth:        "目标缺少鉴权方案 %s",
	TransferWarnVar:         "目标未定义变量 %s（环境仍可在运行时提供）",
	TransferWarnDoc:         "目标缺少关联文档 %s",
	TransferWarnLocalFile:   "本机文件绑定 %s 只保留槽名，需在目标设备重新绑定",
	TransferWarnDefaultAuth: "目标项目默认鉴权为 %s，可能改变未显式设置鉴权的请求",

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
	Rename:         "重命名",
	OpenRow:        "打开",
	NewRequestHere: "在此新建请求",
	Duplicate:      "复制请求",
	DuplicateDoc:   "复制文档",
	MoveTo:         "移动到…",
	Delete:         "删除",
	Undo:           "撤销",
	UndoDone:       "已撤销",
	DeletedToast:   "已删除 %s（⌘Z 撤销）",
	Description:    "描述",
	AddParamHint:   "添加参数…",
	QuickOpen:      "快速打开",
	ToggleSidebar:  "显示/隐藏侧栏",
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
	SaveResponse:      "保存响应到文件",
	FindPlaceholder:   "查找响应…",
	FindCount:         "%d / %d",
	FindNone:          "无匹配",
	SavedResponse:     "已保存响应：%s",
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
	CloseRight:   "关闭右侧",
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

	HistoryThisProject:       "本项目",
	HistoryAllProjects:       "全部项目",
	HistoryFilterPlaceholder: "筛选接口 / URL",
	HistoryTimeAll:           "全部时间",
	HistoryTimeToday:         "今天",
	HistoryTimeWeek:          "最近 7 天",
	HistoryTimeMonth:         "最近 30 天",
	HistoryResultAll:         "全部结果",
	HistoryResultSuccess:     "成功",
	HistoryResultClient:      "客户端错误",
	HistoryResultServer:      "服务端错误",
	HistoryResultNetwork:     "网络/配置错误",
	HistoryResultCanceled:    "已取消",
	HistoryNoMatch:           "没有符合筛选条件的历史",
	HistoryUnavailable:       "本机没有可用的配置目录，历史已禁用",
	HistoryPrivacyNote:       "历史只保存在本机；默认仅保存脱敏元数据，正文需在设置中显式开启。",
	HistoryDetailTitle:       "历史详情",
	HistoryDetailSummary:     "概要",
	HistoryDetailResult:      "结果",
	HistoryDetailRequest:     "请求快照",
	HistoryDetailResponse:    "响应快照",
	HistoryDetailOpen:        "打开请求",
	HistoryDetailDelete:      "删除此记录",
	HistoryRedactedNote:      "已脱敏；恢复时将使用脱敏后的 URL。",
	HistoryRequestGone:       "请求已不存在，无法打开",
	HistoryProjectNotOpen:    "该项目未打开，请先打开项目再恢复此请求",
	HistoryBodyOmitted:       "正文未保存：%s",
	HistoryOmitPolicy:        "默认策略",
	HistoryOmitLarge:         "超过大小上限",
	HistoryOmitBinary:        "非文本内容",
	HistoryOmitTruncated:     "响应已被截断",
	HistorySnapshotTrimmed:   "快照超出单条记录上限，仅保留元数据",
	HistoryDiagCorrupt:       "%d 行历史记录已损坏并被跳过",
	HistoryDiagRead:          "读取历史失败：%s",
	HistoryWriteFailed:       "无法写入历史：%s",
	HistoryReadFailed:        "无法读取历史：%s",
	HistoryOutcomeError:      "错误",
	HistoryOutcomeCanceled:   "已取消",
	HistoryOutcomeTimeout:    "超时",

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

	EditorSection:         "编辑器",
	FontSizeLabel:         "字号",
	FontSizeStatus:        "编辑器字号：%d",
	TabSizeLabel:          "缩进宽度",
	SoftWrapLabel:         "自动换行",
	LineNumbersLabel:      "显示行号",
	AutoSaveSection:       "自动保存",
	AutoSaveEnable:        "启用安全自动保存",
	AutoSaveDelayLabel:    "保存间隔（秒）",
	AutoSaveSafetyNote:    "自动保存只写入基线未变的缓冲区；外部修改、冲突和已删除文件会被跳过，绝不会覆盖或复活它们。",
	AutoSaveOnStatus:      "已启用自动保存（%s 秒）",
	AutoSaveOffStatus:     "已关闭自动保存",
	AutoSaveSkipped:       "自动保存跳过 %d 个有外部修改或已删除的文件",
	AutoSaveSaved:         "已自动保存 %d 个文件",
	ShortcutsSection:      "快捷键",
	ShortcutsNote:         "取消勾选可停用对应快捷键；输入框内的编辑快捷键不受影响。",
	ShortcutNewRequest:    "新建请求",
	ShortcutCloseTab:      "关闭当前标签",
	ShortcutTabSwitch:     "切换标签",
	ShortcutFocusURL:      "聚焦地址栏",
	ShortcutQuickSearch:   "快速搜索",
	ShortcutEditEnv:       "编辑环境",
	ShortcutToggleSidebar: "显示/隐藏侧栏",
	ShortcutUndoDelete:    "撤销删除",
	ShortcutRename:        "重命名",

	HistorySettingsLabel:    "本地历史",
	HistorySaveSnapshots:    "保存脱敏的请求/响应快照",
	HistorySaveBodies:       "快照包含正文（已脱敏、有大小上限）",
	HistoryRetentionLabel:   "保留期限",
	HistoryRetention30:      "30 天",
	HistoryRetention90:      "90 天",
	HistoryRetention365:     "365 天",
	HistoryRetentionForever: "不按期限删除",
	HistorySettingsNote:     "历史保存在本机配置目录，不进入项目或 Git；数量与总字节上限始终生效。",

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

	NewProject:               "New Project…",
	NewProjectTip:            "Create a new .gorequest project at a chosen location",
	NewProjectTitle:          "New Project",
	NewProjectHint:           "Create a new GoRequest project at a chosen location; nothing existing is overwritten.",
	ProjectNamePlaceholder:   "e.g. Orders API",
	ProjectLocationLabel:     "Location",
	Browse:                   "Browse…",
	ChooseProjectLocation:    "Choose Project Location",
	ChooseProjectLocationMsg: "The new project is created as a subfolder of the folder you choose.",
	NewProjectFolderHint:     "The project is created in a \"location/name\" subfolder, which must be missing or empty.",
	ErrEnterProjectName:      "Enter a project name.",
	ErrEnterProjectLocation:  "Choose a project location.",
	NewProjectFailed:         "New project: %s",
	CreatedProject:           "Created project %s",
	GlobalWorkspace:          "Personal Workspace",
	GlobalWorkspaceTip:       "Open local scratch requests and personal collections",
	GlobalWorkspaceTitle:     "Personal Workspace",
	GlobalWorkspaceOpened:    "Opened the personal workspace (local app data, not part of any Git project)",

	RecentSearchPlaceholder: "Search recent projects…",
	RecentNoMatch:           "No recent project matches",
	PinRecent:               "Pin to top",
	UnpinRecent:             "Unpin",
	RemoveRecent:            "Remove from recents",
	RemovedRecent:           "Removed %s from recent projects",

	CopyToGlobal:            "Copy to Personal Workspace",
	MoveToGlobal:            "Move to Personal Workspace",
	CopyToProject:           "Copy to Project",
	MoveToProject:           "Move to Project",
	TransferSameWorkspace:   "Source and destination workspaces are the same",
	TransferTitle:           "Cross-workspace Transfer Preview",
	TransferTarget:          "Target file",
	TransferIDCopy:          "Copy assigns a new ID: %s",
	TransferIDMove:          "Move keeps the stable ID: %s",
	TransferDirtyCopy:       "This request has unsaved edits; only its saved version is copied.",
	TransferDirtyMove:       "%q has unsaved edits; save or discard before moving it.",
	TransferSecrets:         "Secret references kept (values are not copied): %s",
	TransferBlocked:         "Blocked: replace inline credentials or literal Cookies with {{secret.name}} references first.",
	TransferConfirm:         "Transfer",
	TransferDone:            "Transferred to %s",
	TransferFailed:          "Transfer: %s",
	TransferWarnInline:      "Inline sensitive value: %s (not copied)",
	TransferWarnAuth:        "The destination has no auth scheme %s",
	TransferWarnVar:         "Variable %s is not defined in the destination (an environment may still supply it)",
	TransferWarnDoc:         "Linked document %s is missing in the destination",
	TransferWarnLocalFile:   "Local file binding %s keeps only its slot name and must be rebound on this device",
	TransferWarnDefaultAuth: "The destination's default auth is %s, which may change requests without explicit auth",

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
	Rename:         "Rename",
	OpenRow:        "Open",
	NewRequestHere: "New Request Here",
	Duplicate:      "Duplicate Request",
	DuplicateDoc:   "Duplicate Document",
	MoveTo:         "Move To…",
	Delete:         "Delete",
	Undo:           "Undo",
	UndoDone:       "Undone",
	DeletedToast:   "Deleted %s (⌘Z to undo)",
	Description:    "Description",
	AddParamHint:   "Add parameter…",
	QuickOpen:      "Quick Open",
	ToggleSidebar:  "Toggle Sidebar",
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
	SaveResponse:      "Save Response to File",
	FindPlaceholder:   "Find in response…",
	FindCount:         "%d / %d",
	FindNone:          "No matches",
	SavedResponse:     "Saved response: %s",
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
	CloseRight:   "Close to the Right",
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

	HistoryThisProject:       "This project",
	HistoryAllProjects:       "All projects",
	HistoryFilterPlaceholder: "Filter by request or URL",
	HistoryTimeAll:           "All time",
	HistoryTimeToday:         "Today",
	HistoryTimeWeek:          "Last 7 days",
	HistoryTimeMonth:         "Last 30 days",
	HistoryResultAll:         "All results",
	HistoryResultSuccess:     "Success",
	HistoryResultClient:      "Client error",
	HistoryResultServer:      "Server error",
	HistoryResultNetwork:     "Network/config error",
	HistoryResultCanceled:    "Canceled",
	HistoryNoMatch:           "No history matches these filters",
	HistoryUnavailable:       "No local config directory is available, so history is disabled",
	HistoryPrivacyNote:       "History stays on this device. Only redacted metadata is saved by default; bodies require an explicit opt-in.",
	HistoryDetailTitle:       "History Detail",
	HistoryDetailSummary:     "Summary",
	HistoryDetailResult:      "Result",
	HistoryDetailRequest:     "Request snapshot",
	HistoryDetailResponse:    "Response snapshot",
	HistoryDetailOpen:        "Open request",
	HistoryDetailDelete:      "Delete this record",
	HistoryRedactedNote:      "Redacted. Opening the request uses the redacted URL.",
	HistoryRequestGone:       "The request no longer exists, so it cannot be opened",
	HistoryProjectNotOpen:    "Open that project first to reopen this request from history",
	HistoryBodyOmitted:       "Body not stored: %s",
	HistoryOmitPolicy:        "default policy",
	HistoryOmitLarge:         "over the size limit",
	HistoryOmitBinary:        "non-text content",
	HistoryOmitTruncated:     "the response was truncated",
	HistorySnapshotTrimmed:   "The snapshot exceeded the per-record limit, so only metadata was kept",
	HistoryDiagCorrupt:       "%d history line(s) were corrupt and skipped",
	HistoryDiagRead:          "Could not read history: %s",
	HistoryWriteFailed:       "Could not write history: %s",
	HistoryReadFailed:        "Could not read history: %s",
	HistoryOutcomeError:      "Error",
	HistoryOutcomeCanceled:   "Canceled",
	HistoryOutcomeTimeout:    "Timed out",

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

	EditorSection:         "Editor",
	FontSizeLabel:         "Font size",
	FontSizeStatus:        "Editor font size: %d",
	TabSizeLabel:          "Indent width",
	SoftWrapLabel:         "Soft wrap",
	LineNumbersLabel:      "Show line numbers",
	AutoSaveSection:       "Auto-save",
	AutoSaveEnable:        "Enable safe auto-save",
	AutoSaveDelayLabel:    "Save interval (seconds)",
	AutoSaveSafetyNote:    "Auto-save writes only buffers whose baseline is unchanged; external edits, conflicts and deleted files are skipped, never overwritten or resurrected.",
	AutoSaveOnStatus:      "Auto-save enabled (%s s)",
	AutoSaveOffStatus:     "Auto-save disabled",
	AutoSaveSkipped:       "Auto-save skipped %d file(s) with external changes or deletion",
	AutoSaveSaved:         "Auto-saved %d file(s)",
	ShortcutsSection:      "Shortcuts",
	ShortcutsNote:         "Unchecking a shortcut removes that binding; editing shortcuts inside text fields are unaffected.",
	ShortcutNewRequest:    "New request",
	ShortcutCloseTab:      "Close current tab",
	ShortcutTabSwitch:     "Switch tabs",
	ShortcutFocusURL:      "Focus URL",
	ShortcutQuickSearch:   "Quick search",
	ShortcutEditEnv:       "Edit environment",
	ShortcutToggleSidebar: "Toggle sidebar",
	ShortcutUndoDelete:    "Undo delete",
	ShortcutRename:        "Rename",

	HistorySettingsLabel:    "Local history",
	HistorySaveSnapshots:    "Save redacted request/response snapshots",
	HistorySaveBodies:       "Include bodies in snapshots (redacted, size-limited)",
	HistoryRetentionLabel:   "Keep history",
	HistoryRetention30:      "30 days",
	HistoryRetention90:      "90 days",
	HistoryRetention365:     "365 days",
	HistoryRetentionForever: "Until the record/byte limit removes it",
	HistorySettingsNote:     "History is stored in the local config directory, never in the project or Git. Record and byte caps always apply.",

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
