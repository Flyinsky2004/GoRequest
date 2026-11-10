package desktop

// websocket_workspace_i18n.go owns the WebSocket workspace's own bilingual
// messages. It is deliberately separate from the global i18n catalog so adding
// the WebSocket workspace cannot disturb existing HTTP wording, and separate
// from websocket_i18n.go so the session vocabulary stays reusable.

// wsWorkspaceMessages is the workspace chrome's message set (EN is the
// zero-value default).
type wsWorkspaceMessages struct {
	NewWebSocket            string
	NewWebSocketDefaultName string
	TabBadge                string
	SectionHeaders          string
	SectionAuth             string
	SectionSettings         string
	MessagesTitle           string
	ConnectHint             string
	NoSessionHint           string
	TotalTimeoutLabel       string
	TotalTimeoutHint        string

	// Bounded script-action workspace. It is a separate surface from manual
	// debugging, so its vocabulary is separate too.
	SectionScript        string
	PlanPathPlaceholder  string
	PlanOpen             string
	PlanRun              string
	PlanCancel           string
	PlanNoPlan           string
	PlanNoProject        string
	PlanBusy             string
	PlanRunning          string
	PlanCanceling        string
	PlanConfirmNeeded    string
	PlanConfirmCheck     string
	PlanReplayHint       string
	PlanStepsLabel       string
	PlanResultTitle      string
	PlanNoResult         string
	PlanLoadedFmt        string
	PlanDigestChangedFmt string
	PlanLoadFailedFmt    string
	PlanResolveFailedFmt string
	PlanCompileFailedFmt string
	PlanRunFailedFmt     string
	PlanRunDoneFmt       string
	PlanBoundsFmt        string
	PlanTotalsFmt        string
	PlanFailedStepFmt    string
	PlanCloseFmt         string
	PlanCursorFmt        string
	PlanSentSeqFmt       string
	PlanRecvSeqFmt       string
	PlanSkippedFmt       string
	PlanStepErrorFmt     string
	PlanStatePassed      string
	PlanStateFailed      string
	PlanStateCanceled    string
	PlanStateTimedOut    string
	PlanStateRunning     string
}

var wsWorkspaceMessagesEn = wsWorkspaceMessages{
	NewWebSocket:            "New WebSocket",
	NewWebSocketDefaultName: "WebSocket",
	TabBadge:                "WS",
	SectionHeaders:          "Headers",
	SectionAuth:             "Auth",
	SectionSettings:         "Settings",
	MessagesTitle:           "Messages",
	ConnectHint:             "Connect to start a session. Variables and secrets resolve on Connect.",
	NoSessionHint:           "No live session yet — press Connect to open one.",
	TotalTimeoutLabel:       "Total session deadline",
	TotalTimeoutHint:        "Go duration such as 30s or 2m; empty means no total deadline.",

	SectionScript:        "Script",
	PlanPathPlaceholder:  "No plan file selected",
	PlanOpen:             "Open plan…",
	PlanRun:              "Run plan",
	PlanCancel:           "Cancel run",
	PlanNoPlan:           "Open a plan file to see its bounded steps.",
	PlanNoProject:        "A plan needs a project so variables and secrets can resolve.",
	PlanBusy:             "A run is already in flight.",
	PlanRunning:          "Running the plan…",
	PlanCanceling:        "Canceling…",
	PlanConfirmNeeded:    "This plan needs confirmation",
	PlanConfirmCheck:     "I confirm this target for this run only",
	PlanReplayHint:       "A run re-reads the file and re-resolves the current environment and secrets; no value from an earlier run is reused.",
	PlanStepsLabel:       "Declared steps",
	PlanResultTitle:      "Plan run",
	PlanNoResult:         "No run yet — press Run plan.",
	PlanLoadedFmt:        "%s · %d steps",
	PlanDigestChangedFmt: "Plan changed on disk since it was reviewed (%s → %s); reopen to review the new steps.",
	PlanLoadFailedFmt:    "Could not open the plan: %s",
	PlanResolveFailedFmt: "Could not resolve variables: %s",
	PlanCompileFailedFmt: "Could not compile the plan: %s",
	PlanRunFailedFmt:     "Run failed: %s",
	PlanRunDoneFmt:       "Run finished: %s",
	PlanBoundsFmt:        "max message %s · total %s · retain %d",
	PlanTotalsFmt:        "sent %d · received %d · %s",
	PlanFailedStepFmt:    "failed at step %d",
	PlanCloseFmt:         "close %d %s",
	PlanCursorFmt:        "cursor %d→%d",
	PlanSentSeqFmt:       "sent #%d",
	PlanRecvSeqFmt:       "received #%d · %s",
	PlanSkippedFmt:       "%d skipped · %d retained",
	PlanStepErrorFmt:     "error: %s",
	PlanStatePassed:      "passed",
	PlanStateFailed:      "failed",
	PlanStateCanceled:    "canceled",
	PlanStateTimedOut:    "timed out",
	PlanStateRunning:     "running",
}

var wsWorkspaceMessagesZh = wsWorkspaceMessages{
	NewWebSocket:            "新建 WebSocket",
	NewWebSocketDefaultName: "WebSocket",
	TabBadge:                "WS",
	SectionHeaders:          "Headers",
	SectionAuth:             "鉴权",
	SectionSettings:         "设置",
	MessagesTitle:           "消息",
	ConnectHint:             "连接后开始会话；变量与密钥在连接时解析。",
	NoSessionHint:           "尚无活动会话 — 点击“连接”建立。",
	TotalTimeoutLabel:       "会话总体超时",
	TotalTimeoutHint:        "Go 时长，如 30s 或 2m；留空表示不限制总时长。",

	SectionScript:        "脚本",
	PlanPathPlaceholder:  "尚未选择计划文件",
	PlanOpen:             "打开计划…",
	PlanRun:              "运行计划",
	PlanCancel:           "取消运行",
	PlanNoPlan:           "打开计划文件后可查看其有界步骤。",
	PlanNoProject:        "计划需要项目上下文，才能解析变量与密钥。",
	PlanBusy:             "已有一次运行正在进行。",
	PlanRunning:          "正在运行计划…",
	PlanCanceling:        "正在取消…",
	PlanConfirmNeeded:    "该计划需要确认",
	PlanConfirmCheck:     "我确认本次运行的目标（仅本次）",
	PlanReplayHint:       "每次运行都会重新读取文件并重新解析当前环境与密钥，不复用上一次运行的任何值。",
	PlanStepsLabel:       "声明的步骤",
	PlanResultTitle:      "计划运行",
	PlanNoResult:         "尚未运行 — 点击“运行计划”。",
	PlanLoadedFmt:        "%s · %d 步",
	PlanDigestChangedFmt: "计划文件自上次审阅后被改动（%s → %s），请重新打开以审阅新步骤。",
	PlanLoadFailedFmt:    "无法打开计划：%s",
	PlanResolveFailedFmt: "无法解析变量：%s",
	PlanCompileFailedFmt: "无法编译计划：%s",
	PlanRunFailedFmt:     "运行失败：%s",
	PlanRunDoneFmt:       "运行结束：%s",
	PlanBoundsFmt:        "单条上限 %s · 总时长 %s · 保留 %d",
	PlanTotalsFmt:        "已发送 %d · 已接收 %d · %s",
	PlanFailedStepFmt:    "失败于第 %d 步",
	PlanCloseFmt:         "关闭 %d %s",
	PlanCursorFmt:        "游标 %d→%d",
	PlanSentSeqFmt:       "已发送 #%d",
	PlanRecvSeqFmt:       "已接收 #%d · %s",
	PlanSkippedFmt:       "跳过 %d · 保留 %d",
	PlanStepErrorFmt:     "错误：%s",
	PlanStatePassed:      "通过",
	PlanStateFailed:      "失败",
	PlanStateCanceled:    "已取消",
	PlanStateTimedOut:    "超时",
	PlanStateRunning:     "运行中",
}

// wsWorkspaceMsg returns the active-language WebSocket workspace messages.
func (a *App) wsWorkspaceMsg() *wsWorkspaceMessages {
	if a != nil && a.mgr != nil && a.mgr.cfg != nil && parseLanguage(string(a.mgr.cfg.language())) == langEnglish {
		return &wsWorkspaceMessagesEn
	}
	return &wsWorkspaceMessagesZh
}
