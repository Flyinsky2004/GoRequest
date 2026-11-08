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
}

// wsWorkspaceMsg returns the active-language WebSocket workspace messages.
func (a *App) wsWorkspaceMsg() *wsWorkspaceMessages {
	if a != nil && a.mgr != nil && a.mgr.cfg != nil && parseLanguage(string(a.mgr.cfg.language())) == langEnglish {
		return &wsWorkspaceMessagesEn
	}
	return &wsWorkspaceMessagesZh
}
