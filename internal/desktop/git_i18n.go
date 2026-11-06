package desktop

// gitMessages holds every user-facing string for the B07 Git panel. It lives in
// its own file so the Git delivery never touches the shared i18n catalog and
// the panel reads the window's current language each frame.
type gitMessages struct {
	NavLabel string
	Title    string

	Refresh         string
	Cancel          string
	Retry           string
	Close           string
	Busy            string // %s operation
	Canceled        string
	ErrorPrefix     string // %s
	ShowFirst       string // %d
	RepoPath        string // %s
	BranchLabel     string // %s
	Upstream        string // %s
	NoUpstream      string
	Detached        string
	Unavailable     string
	UnavailableHint string
	NotRepo         string
	NotRepoHint     string
	Clean           string
	ChangedFiles    string // %d
	Loading         string

	SelectAll       string
	ClearSelection  string
	SelectedCount   string // %d
	StagedWarn      string // %d
	SelectFilesHint string

	Commit            string
	CommitTitle       string
	CommitMessage     string
	CommitPlaceholder string
	CommitAction      string
	CommitDone        string // %s
	CommitFailed      string // %s
	CommitNote        string

	DiffTitle    string // %s
	DiffUnstaged string
	DiffStaged   string
	DiffBinary   string
	DiffTooLarge string
	DiffUnmerged string
	DiffMissing  string

	Pull         string
	PullTitle    string
	PullConfirm  string // %s remote/ref
	PullFFOnly   string
	PullDirty    string
	PullConflict string
	PullUpToDate string
	PullDone     string // %d
	PullDiverged string

	Push         string
	PushTitle    string
	PushConfirm  string // %s remote/ref
	PushNoForce  string
	PushDone     string
	PushNothing  string
	PushRejected string
}

var gitMessagesZh = gitMessages{
	NavLabel: "Git",
	Title:    "Git",

	Refresh:         "刷新",
	Cancel:          "取消",
	Retry:           "重试",
	Close:           "关闭",
	Busy:            "正在执行：%s…",
	Canceled:        "已取消该 Git 操作，未修改仓库。",
	ErrorPrefix:     "Git 操作失败：%s",
	ShowFirst:       "仅显示前 %d 项",
	RepoPath:        "仓库：%s",
	BranchLabel:     "分支：%s",
	Upstream:        "上游：%s",
	NoUpstream:      "当前分支没有配置上游，Pull/Push 不会猜测远程。请先在终端设置 upstream。",
	Detached:        "当前处于分离 HEAD，提交与远程操作已禁用。",
	Unavailable:     "未找到系统 Git 程序。",
	UnavailableHint: "HTTP 请求与文档编辑不受影响。安装 Git 后可点击“重试”。",
	NotRepo:         "当前项目不在 Git 仓库中。",
	NotRepoHint:     "可以继续编辑和发送 HTTP；初始化仓库请使用终端或系统 Git 工具。",
	Clean:           "工作区没有变更。",
	ChangedFiles:    "变更文件：%d",
	Loading:         "正在读取 Git 状态…",

	SelectAll:       "全选",
	ClearSelection:  "清除选择",
	SelectedCount:   "已选择 %d 个文件",
	StagedWarn:      "已有 %d 项暂存内容不会被本次提交包含（除非你也选中它们）。不会自动取消暂存。",
	SelectFilesHint: "勾选要提交的文件；点击文件名查看原生 Diff。",

	Commit:            "提交…",
	CommitTitle:       "选择性提交",
	CommitMessage:     "提交说明",
	CommitPlaceholder: "描述本次改动",
	CommitAction:      "提交所选文件",
	CommitDone:        "已创建提交 %s",
	CommitFailed:      "提交失败：%s",
	CommitNote:        "只提交所选文件；其他已暂存内容不会夹带，也不会自动执行仓库 hook。",

	DiffTitle:    "差异：%s",
	DiffUnstaged: "工作区 ↔ 索引",
	DiffStaged:   "索引 ↔ HEAD",
	DiffBinary:   "二进制文件，原生文本 Diff 不可用。",
	DiffTooLarge: "文件超过 Diff 大小上限，未加载内容。",
	DiffUnmerged: "该文件存在未解决的合并冲突；本面板不会自动解决或覆盖。",
	DiffMissing:  "该侧文件不存在（新增或删除）。",

	Pull:         "Pull",
	PullTitle:    "拉取上游",
	PullConfirm:  "从 %s 快进拉取？",
	PullFFOnly:   "仅允许快进合并；不会自动合并、rebase、stash 或解决冲突。",
	PullDirty:    "存在未保存的编辑或已暂存/未提交的改动。请先保存并提交或自行处理后重试。",
	PullConflict: "存在 B01 外部同步冲突。请先在冲突界面处理后再 Pull。",
	PullUpToDate: "已是最新，无需拉取。",
	PullDone:     "已快进 %d 个提交",
	PullDiverged: "本地与上游已分叉，已停止。请自行在终端处理合并。",

	Push:         "Push",
	PushTitle:    "推送当前分支",
	PushConfirm:  "推送到 %s？",
	PushNoForce:  "只做普通 push，绝不强推；不会执行仓库 hook。",
	PushDone:     "已推送",
	PushNothing:  "远端已是最新。",
	PushRejected: "远端拒绝了推送（可能已分叉）；不会强推。",
}

var gitMessagesEn = gitMessages{
	NavLabel: "Git",
	Title:    "Git",

	Refresh:         "Refresh",
	Cancel:          "Cancel",
	Retry:           "Retry",
	Close:           "Close",
	Busy:            "Running: %s…",
	Canceled:        "Git operation canceled; the repository was not changed.",
	ErrorPrefix:     "Git operation failed: %s",
	ShowFirst:       "Showing the first %d entries",
	RepoPath:        "Repository: %s",
	BranchLabel:     "Branch: %s",
	Upstream:        "Upstream: %s",
	NoUpstream:      "The current branch has no upstream. Pull/Push will not guess a remote; set an upstream in a terminal first.",
	Detached:        "HEAD is detached; commit and remote operations are disabled.",
	Unavailable:     "The system Git executable was not found.",
	UnavailableHint: "HTTP requests and document editing still work. Install Git, then click Retry.",
	NotRepo:         "This project is not inside a Git repository.",
	NotRepoHint:     "HTTP editing and sending continue to work. Initialize a repository with Git in a terminal.",
	Clean:           "No changes in the working tree.",
	ChangedFiles:    "Changed files: %d",
	Loading:         "Reading Git status…",

	SelectAll:       "Select All",
	ClearSelection:  "Clear",
	SelectedCount:   "%d selected",
	StagedWarn:      "%d already-staged change(s) are excluded unless you select them too. Nothing is unstaged automatically.",
	SelectFilesHint: "Check files to commit; click a file name for the native diff.",

	Commit:            "Commit…",
	CommitTitle:       "Selective Commit",
	CommitMessage:     "Commit message",
	CommitPlaceholder: "Describe this change",
	CommitAction:      "Commit Selected Files",
	CommitDone:        "Created commit %s",
	CommitFailed:      "Commit failed: %s",
	CommitNote:        "Only the selected files are committed. Other staged content and repository hooks are never included or run.",

	DiffTitle:    "Diff: %s",
	DiffUnstaged: "Worktree ↔ Index",
	DiffStaged:   "Index ↔ HEAD",
	DiffBinary:   "Binary file; the native text diff is unavailable.",
	DiffTooLarge: "File exceeds the diff size limit and was not loaded.",
	DiffUnmerged: "This file has unresolved merge conflicts; the panel will not resolve or overwrite them.",
	DiffMissing:  "This side does not exist (added or deleted file).",

	Pull:         "Pull",
	PullTitle:    "Pull Upstream",
	PullConfirm:  "Fast-forward from %s?",
	PullFFOnly:   "Fast-forward only. No automatic merge, rebase, stash or conflict resolution.",
	PullDirty:    "There are unsaved edits or uncommitted changes. Save and commit them, or handle them yourself, then retry.",
	PullConflict: "An external B01 sync conflict is open. Resolve it before pulling.",
	PullUpToDate: "Already up to date; nothing to pull.",
	PullDone:     "Fast-forwarded %d commit(s)",
	PullDiverged: "Local and upstream branches diverged; stopped. Merge manually in a terminal.",

	Push:         "Push",
	PushTitle:    "Push Current Branch",
	PushConfirm:  "Push to %s?",
	PushNoForce:  "A regular push only; never forced, and repository hooks are not run.",
	PushDone:     "Pushed",
	PushNothing:  "The remote is already up to date.",
	PushRejected: "The remote rejected the push (possibly diverged); it was not forced.",
}

func gitMessagesFor(l language) *gitMessages {
	if parseLanguage(string(l)) == langEnglish {
		return &gitMessagesEn
	}
	return &gitMessagesZh
}

func (a *App) gitMsg() *gitMessages {
	if a == nil || a.mgr == nil || a.mgr.cfg == nil {
		return gitMessagesFor(defaultLanguage())
	}
	return gitMessagesFor(a.mgr.cfg.language())
}
