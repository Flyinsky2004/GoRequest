package desktop

// websocket_i18n.go owns the WebSocket session dialog's bilingual messages.
// It mirrors the module-local message pattern (network_ui.go, mock_i18n.go):
// a struct with EN defaults plus a ZH variant and an App accessor. No global
// map crosses window state.

// wsMessages is the dialog's message set (EN is the zero-value default).
type wsMessages struct {
	MenuEntry         string
	Title             string
	FieldURL          string
	FieldSubprotocols string
	FieldHeaders      string
	FieldHeadersHint  string
	FieldTotalTimeout string
	CompressionLabel  string
	BtnConnect        string
	BtnDisconnect     string
	BtnPing           string
	BtnSend           string
	BtnCloseSession   string
	BtnClearView      string
	BtnSaveSelected   string
	BtnDone           string
	SendModeText      string
	SendModeBinary    string
	SendBinaryHint    string
	DirFilterAll      string
	DirFilterIn       string
	DirFilterOut      string
	TimelineTitle     string
	DetailTitle       string
	DetailEmpty       string
	FactsProxy        string
	FactsTLS          string
	FactsDeadlines    string
	FactsLimits       string
	ConfirmRequired   string
	ConfirmCheckbox   string
	StateDisconnected string
	StateConnecting   string
	StateOpen         string
	StateClosing      string
	StateClosed       string
	StateError        string
	SensitiveBadge    string
	Connecting        string
	Connected         string
	ConnectFailed     string
	ConfirmNeeded     string
	ClosedByPeer      string
	SessionTimedOut   string
	SendFailedFmt     string // %s err
	SendDone          string
	SendNotOpen       string
	PongOKFmt         string // %s rtt
	PongMissingFmt    string
	ClosingFmt        string // %d code
	CloseNotOpen      string
	SavedToFmt        string // %s path
	SaveFailedFmt     string
	Terminated        string
	VarsResolveFailed string
	NoProject         string
	Busy              string
	FilterLabel       string
	OverflowNoteFmt   string // %d events, %d dropped subscribers
	BoundsNoteFmt     string // %d ring, %s msg cap, %d inbox
}

var wsMessagesEn = wsMessages{
	MenuEntry:         "WebSocket Session...",
	Title:             "WebSocket Session",
	FieldURL:          "URL",
	FieldSubprotocols: "Subprotocols (comma separated)",
	FieldHeaders:      "Handshake Headers",
	FieldHeadersHint:  "One Name: Value per line",
	FieldTotalTimeout: "Total deadline (s, 0 = none)",
	CompressionLabel:  "Negotiate permessage-deflate",
	BtnConnect:        "Connect",
	BtnDisconnect:     "Disconnect",
	BtnPing:           "Ping",
	BtnSend:           "Send",
	BtnCloseSession:   "Close (code)",
	BtnClearView:      "Clear View",
	BtnSaveSelected:   "Save Selected...",
	BtnDone:           "Close",
	SendModeText:      "Text",
	SendModeBinary:    "Binary",
	SendBinaryHint:    "Hex bytes (e.g. 00ff0a21)",
	DirFilterAll:      "All",
	DirFilterIn:       "In",
	DirFilterOut:      "Out",
	TimelineTitle:     "Timeline",
	DetailTitle:       "Detail",
	DetailEmpty:       "Select a message to inspect it",
	FactsProxy:        "Network",
	FactsTLS:          "TLS",
	FactsDeadlines:    "Deadlines",
	FactsLimits:       "Limits",
	ConfirmRequired:   "This target requires explicit confirmation under the target policy.",
	ConfirmCheckbox:   "I confirm connecting this production WebSocket target",
	StateDisconnected: "DISCONNECTED",
	StateConnecting:   "CONNECTING",
	StateOpen:         "OPEN",
	StateClosing:      "CLOSING",
	StateClosed:       "CLOSED",
	StateError:        "ERROR",
	SensitiveBadge:    "redacted",
	Connecting:        "Connecting to %s...",
	Connected:         "WebSocket session open",
	ConnectFailed:     "Connect failed: %v",
	ConfirmNeeded:     "Target policy requires confirmation before connecting",
	ClosedByPeer:      "Connection closed by peer (code %d %s)",
	SessionTimedOut:   "Total session deadline exceeded",
	SendFailedFmt:     "Send failed: %s",
	SendDone:          "Sent",
	SendNotOpen:       "Session is not open",
	PongOKFmt:         "Pong received, RTT %s",
	PongMissingFmt:    "No pong within %s",
	ClosingFmt:        "Closing with code %d",
	CloseNotOpen:      "Nothing to close",
	SavedToFmt:        "Saved message to %s",
	SaveFailedFmt:     "Save failed: %v",
	Terminated:        "Session terminated",
	VarsResolveFailed: "Variable resolution failed: %v",
	NoProject:         "Open a project first",
	Busy:              "Another connection attempt is already in progress",
	FilterLabel:       "Filter",
	OverflowNoteFmt:   "timeline: %d events kept, %d subscriber drops",
	BoundsNoteFmt:     "ring %d · message cap %s · inbox %d",
}

var wsMessagesZh = wsMessages{
	MenuEntry:         "WebSocket 会话...",
	Title:             "WebSocket 会话",
	FieldURL:          "地址",
	FieldSubprotocols: "子协议（逗号分隔）",
	FieldHeaders:      "握手 Headers",
	FieldHeadersHint:  "每行一条 Name: Value",
	FieldTotalTimeout: "总体超时（秒，0 = 不限）",
	CompressionLabel:  "协商 permessage-deflate 压缩",
	BtnConnect:        "连接",
	BtnDisconnect:     "断开",
	BtnPing:           "Ping",
	BtnSend:           "发送",
	BtnCloseSession:   "关闭（带码）",
	BtnClearView:      "清除视图",
	BtnSaveSelected:   "保存选中消息...",
	BtnDone:           "关闭",
	SendModeText:      "文本",
	SendModeBinary:    "二进制",
	SendBinaryHint:    "十六进制字节（如 00ff0a21）",
	DirFilterAll:      "全部",
	DirFilterIn:       "收",
	DirFilterOut:      "发",
	TimelineTitle:     "时间线",
	DetailTitle:       "详情",
	DetailEmpty:       "选择一条消息查看内容",
	FactsProxy:        "网络",
	FactsTLS:          "TLS",
	FactsDeadlines:    "超时",
	FactsLimits:       "限额",
	ConfirmRequired:   "目标策略要求显式确认后才能连接。",
	ConfirmCheckbox:   "我确认连接此生产 WebSocket 目标",
	StateDisconnected: "已断开",
	StateConnecting:   "连接中",
	StateOpen:         "已连接",
	StateClosing:      "关闭中",
	StateClosed:       "已关闭",
	StateError:        "错误",
	SensitiveBadge:    "已脱敏",
	Connecting:        "正在连接 %s...",
	Connected:         "WebSocket 会话已建立",
	ConnectFailed:     "连接失败：%v",
	ConfirmNeeded:     "目标策略要求先确认才能连接",
	ClosedByPeer:      "服务端已关闭连接（码 %d %s）",
	SessionTimedOut:   "会话总体超时已到",
	SendFailedFmt:     "发送失败：%s",
	SendDone:          "已发送",
	SendNotOpen:       "会话未连接",
	PongOKFmt:         "已收到 Pong，往返 %s",
	PongMissingFmt:    "%s 内未收到 Pong",
	ClosingFmt:        "正在以码 %d 关闭",
	CloseNotOpen:      "没有可关闭的连接",
	SavedToFmt:        "消息已保存到 %s",
	SaveFailedFmt:     "保存失败：%v",
	Terminated:        "会话已终止",
	VarsResolveFailed: "变量解析失败：%v",
	NoProject:         "请先打开项目",
	Busy:              "已有连接尝试正在进行",
	FilterLabel:       "过滤",
	OverflowNoteFmt:   "时间线：保留 %d 条事件，订阅丢弃 %d 条",
	BoundsNoteFmt:     "环形 %d · 单条上限 %s · 收件 %d",
}

// websocketMsg returns the active-language message set for the dialog.
func (a *App) websocketMsg() *wsMessages {
	if a != nil && a.mgr != nil && a.mgr.cfg != nil && parseLanguage(string(a.mgr.cfg.language())) == langEnglish {
		return &wsMessagesEn
	}
	return &wsMessagesZh
}

// wsT is the dialog's translate helper for the struct-held messages.
func wsT(a *App, pick func(*wsMessages) string) string {
	return pick(a.websocketMsg())
}
