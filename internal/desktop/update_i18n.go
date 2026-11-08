package desktop

// This file owns every user-visible string of the update feature in both
// interface languages. It is deliberately separate from i18n.go so this card
// does not edit the shared catalog (U21 owns it). The update dialog, the
// Settings "Updates" category and the version line all read from here.

// updateMessages holds the update feature's translatable strings. Fields with
// positional verbs are formatted with translate at the call site.
type updateMessages struct {
	// Dialog titles and bodies.
	AvailableTitle    string // %s target version
	CurrentLabel      string
	TargetLabel       string
	ReleaseNotesLabel string
	CheckingTitle     string
	CheckingBody      string
	DownloadingTitle  string
	DownloadingBody   string // %s target version
	DownloadProgress  string // %s done, %s total
	ReadyTitle        string
	ReadyMac          string // %s path
	ReadyWindows      string // %s path
	ReadyOther        string // %s path
	VerifiedNote      string
	UpToDateTitle     string
	UpToDateBody      string // %s current version
	DevelopmentTitle  string
	DevelopmentBody   string
	NoReleaseTitle    string
	NoReleaseBody     string
	NoAssetTitle      string
	NoAssetBody       string // %s technical detail
	RateLimitTitle    string
	RateLimitBody     string
	OfflineTitle      string
	OfflineBody       string
	ErrorTitle        string
	ErrorBody         string // %s technical detail
	CanceledTitle     string
	CanceledBody      string

	// Dialog actions.
	Later       string
	UpdateNow   string
	SkipVersion string
	NeverNotify string
	Settings    string
	Close       string
	Cancel      string

	// Settings category.
	VersionLabel          string
	AutomaticChecksLabel  string
	AutoCheckLabel        string
	NeverNotifyLabel      string
	CheckNow              string
	CheckingNow           string
	ManualCheckLabel      string
	PreferencesSaved      string
	LastCheckLabel        string
	NeverChecked          string
	LastCheckFormat       string // %s time, %s result
	StatusUpdateAvailable string // %s version
	StatusUpToDate        string
	StatusDevelopment     string
	StatusNoRelease       string
	StatusNoAsset         string
	StatusRateLimited     string
	StatusOffline         string
	StatusError           string
	StatusCanceled        string
	StatusDownloaded      string
	StatusUnknown         string
	UnavailableNote       string
	AutoCheckNote         string
	NeverNotifyNote       string
	VersionDev            string
}

var updateMessagesZh = updateMessages{
	AvailableTitle:    "发现新版本 %s",
	CurrentLabel:      "当前版本",
	TargetLabel:       "新版本",
	ReleaseNotesLabel: "更新说明",
	CheckingTitle:     "正在检查更新",
	CheckingBody:      "正在从 GitHub Releases 获取最新版本信息…",
	DownloadingTitle:  "正在下载更新",
	DownloadingBody:   "正在下载 %s，完成后会校验 SHA-256。",
	DownloadProgress:  "%s / %s",
	ReadyTitle:        "更新已下载并校验",
	ReadyMac:          "已打开磁盘映像。请把 GoRequest 拖入“应用程序”替换旧版本，然后重新启动。安装完成前不会退出当前程序。",
	ReadyWindows:      "已启动安装程序。请按向导完成安装，然后重新启动 GoRequest。安装完成前不会退出当前程序。",
	ReadyOther:        "安装包已保存到 %s。请手动完成安装。",
	VerifiedNote:      "文件已按发布方提供的 SHA-256 校验。下载并打开安装器不等于已安装；下次启动显示新版本号后才算完成。",
	UpToDateTitle:     "已是最新版本",
	UpToDateBody:      "当前版本 %s 没有可用更新。",
	DevelopmentTitle:  "开发版本",
	DevelopmentBody:   "当前运行的是未打包的开发版本，没有可比较的发布版本号，因此不会自动检查或安装更新。",
	NoReleaseTitle:    "暂无发布版本",
	NoReleaseBody:     "GitHub 上还没有可用的正式发布版本。",
	NoAssetTitle:      "没有适用于此平台的安装包",
	NoAssetBody:       "最新版本没有为当前操作系统和架构提供安装包：%s",
	RateLimitTitle:    "检查过于频繁",
	RateLimitBody:     "GitHub API 的访问次数已达上限，请稍后再试。",
	OfflineTitle:      "无法连接更新服务",
	OfflineBody:       "网络不可用或请求超时。应用仍可正常使用，稍后可在设置中手动检查。",
	ErrorTitle:        "检查更新失败",
	ErrorBody:         "无法完成检查：%s",
	CanceledTitle:     "已取消检查",
	CanceledBody:      "本次更新检查已取消。",

	Later:       "暂不更新",
	UpdateNow:   "更新",
	SkipVersion: "跳过此版本",
	NeverNotify: "永远不提示",
	Settings:    "设置",
	Close:       "关闭",
	Cancel:      "取消",

	VersionLabel:          "版本",
	AutomaticChecksLabel:  "自动检查",
	AutoCheckLabel:        "启动时自动检查更新",
	NeverNotifyLabel:      "永远不提示更新",
	CheckNow:              "立即检查更新",
	CheckingNow:           "正在检查更新…",
	ManualCheckLabel:      "手动检查",
	PreferencesSaved:      "更新偏好已保存",
	LastCheckLabel:        "上次检查",
	NeverChecked:          "从未检查",
	LastCheckFormat:       "%s · %s",
	StatusUpdateAvailable: "发现新版本 %s",
	StatusUpToDate:        "已是最新版本",
	StatusDevelopment:     "开发版本，不参与更新",
	StatusNoRelease:       "暂无发布版本",
	StatusNoAsset:         "没有适用于此平台的安装包",
	StatusRateLimited:     "GitHub API 访问次数已达上限",
	StatusOffline:         "网络不可用或请求超时",
	StatusError:           "检查失败",
	StatusCanceled:        "已取消",
	StatusDownloaded:      "更新已下载并打开安装器",
	StatusUnknown:         "未知结果",
	UnavailableNote:       "此构建未启用更新检查。",
	AutoCheckNote:         "自动检查每个安装版本每天最多一次；安装新版本后会立即重新检查。手动检查不受此限制。",
	NeverNotifyNote:       "开启后停止自动检查和自动提醒；手动检查仍然可用，也可以在此关闭该选项。",
	VersionDev:            "开发版本（无发布版本号）",
}

var updateMessagesEn = updateMessages{
	AvailableTitle:    "Update available: %s",
	CurrentLabel:      "Current version",
	TargetLabel:       "New version",
	ReleaseNotesLabel: "Release notes",
	CheckingTitle:     "Checking for updates",
	CheckingBody:      "Fetching the latest version from GitHub Releases…",
	DownloadingTitle:  "Downloading update",
	DownloadingBody:   "Downloading %s. The file is verified with SHA-256 when complete.",
	DownloadProgress:  "%s / %s",
	ReadyTitle:        "Update downloaded and verified",
	ReadyMac:          "The disk image is open. Drag GoRequest into Applications to replace the old version, then relaunch. The current app is not quit until you do.",
	ReadyWindows:      "The installer has started. Finish the wizard, then relaunch GoRequest. The current app is not quit until you do.",
	ReadyOther:        "The installer was saved to %s. Complete the installation manually.",
	VerifiedNote:      "The file matches the publisher's SHA-256. Downloading and opening the installer is not an installation; the update is complete only when the next launch reports the new version.",
	UpToDateTitle:     "You're up to date",
	UpToDateBody:      "No update is available for version %s.",
	DevelopmentTitle:  "Development build",
	DevelopmentBody:   "This is an unpackaged development build with no comparable release version, so updates are neither checked nor installed.",
	NoReleaseTitle:    "No releases yet",
	NoReleaseBody:     "GitHub has no published release for this project yet.",
	NoAssetTitle:      "No installer for this platform",
	NoAssetBody:       "The latest version has no package for the running OS and architecture: %s",
	RateLimitTitle:    "Too many checks",
	RateLimitBody:     "The GitHub API rate limit has been reached. Try again later.",
	OfflineTitle:      "Cannot reach the update service",
	OfflineBody:       "The network is unavailable or the request timed out. The app keeps working; check again later from Settings.",
	ErrorTitle:        "Update check failed",
	ErrorBody:         "The check could not complete: %s",
	CanceledTitle:     "Check canceled",
	CanceledBody:      "This update check was canceled.",

	Later:       "Later",
	UpdateNow:   "Update",
	SkipVersion: "Skip this version",
	NeverNotify: "Never notify",
	Settings:    "Settings",
	Close:       "Close",
	Cancel:      "Cancel",

	VersionLabel:          "Version",
	AutomaticChecksLabel:  "Automatic checks",
	AutoCheckLabel:        "Automatically check for updates at startup",
	NeverNotifyLabel:      "Never notify me about updates",
	CheckNow:              "Check for Updates Now",
	CheckingNow:           "Checking for updates…",
	ManualCheckLabel:      "Manual check",
	PreferencesSaved:      "Update preferences saved",
	LastCheckLabel:        "Last check",
	NeverChecked:          "Never checked",
	LastCheckFormat:       "%s · %s",
	StatusUpdateAvailable: "Update available: %s",
	StatusUpToDate:        "Up to date",
	StatusDevelopment:     "Development build; updates do not apply",
	StatusNoRelease:       "No releases yet",
	StatusNoAsset:         "No installer for this platform",
	StatusRateLimited:     "GitHub API rate limit reached",
	StatusOffline:         "Network unavailable or request timed out",
	StatusError:           "Check failed",
	StatusCanceled:        "Canceled",
	StatusDownloaded:      "Update downloaded and installer opened",
	StatusUnknown:         "Unknown result",
	UnavailableNote:       "Update checking is unavailable in this build.",
	AutoCheckNote:         "Automatic checks run at most once per local day per installed version; installing a new version re-enables them immediately. Manual checks are never limited.",
	NeverNotifyNote:       "When on, automatic checks and reminders stop. Manual checks still work, and this option can be turned off again here.",
	VersionDev:            "Development build (no release version)",
}

// updateMessagesFor returns the update catalog for a language.
func updateMessagesFor(l language) *updateMessages {
	if parseLanguage(string(l)) == langEnglish {
		return &updateMessagesEn
	}
	return &updateMessagesZh
}
