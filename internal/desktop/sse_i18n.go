package desktop

// sseMessages holds the server-sent-events strings used by the response panel.
// They live in their own catalog, like networkMessages and layoutStatusMessages,
// so the shared i18n.go catalog is not touched by the streaming work. Every
// count-bearing string takes the event count as its only verb.
type sseMessages struct {
	StreamingFmt string // %d events
	CompletedFmt string // %d
	CanceledFmt  string // %d
	TimeoutFmt   string // %d
	FailedFmt    string // %d
	Truncated    string
	Waiting      string
}

var sseMessagesZh = sseMessages{
	StreamingFmt: "SSE 接收中 · %d 个事件",
	CompletedFmt: "SSE 完成 · %d 个事件",
	CanceledFmt:  "SSE 已取消 · 已保留 %d 个事件",
	TimeoutFmt:   "SSE 超时 · 已保留 %d 个事件",
	FailedFmt:    "SSE 连接中断 · 已保留 %d 个事件",
	Truncated:    "SSE 流已按缓冲上限截断，已接收内容保留。",
	Waiting:      "已连接事件流，等待服务器推送事件…",
}

var sseMessagesEn = sseMessages{
	StreamingFmt: "SSE streaming · %d events",
	CompletedFmt: "SSE completed · %d events",
	CanceledFmt:  "SSE canceled · %d events kept",
	TimeoutFmt:   "SSE timed out · %d events kept",
	FailedFmt:    "SSE disconnected · %d events kept",
	Truncated:    "SSE stream truncated at the buffer limit; received events are kept.",
	Waiting:      "Connected to the event stream; waiting for the server to send events…",
}

// sseMsg returns the catalog for the window's current interface language.
func (a *App) sseMsg() *sseMessages {
	if a != nil && a.mgr != nil && a.mgr.cfg != nil && parseLanguage(string(a.mgr.cfg.language())) == langEnglish {
		return &sseMessagesEn
	}
	return &sseMessagesZh
}
