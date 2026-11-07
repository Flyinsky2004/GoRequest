package desktop

// layoutStatusMessages holds the request-send terminal status lines. They live
// in their own file so the shared i18n.go catalog is not touched by the R9
// layout/status work.
type layoutStatusMessages struct {
	SendDoneFmt     string // status code, milliseconds
	SendFailedFmt   string // milliseconds, error
	SendCanceledFmt string // milliseconds
}

var layoutStatusMessagesZh = layoutStatusMessages{
	SendDoneFmt:     "已收到 %d 响应 · %.1f ms",
	SendFailedFmt:   "请求失败 · %.1f ms · %s",
	SendCanceledFmt: "请求已取消 · %.1f ms",
}

var layoutStatusMessagesEn = layoutStatusMessages{
	SendDoneFmt:     "received %d response · %.1f ms",
	SendFailedFmt:   "request failed · %.1f ms · %s",
	SendCanceledFmt: "request canceled · %.1f ms",
}

func layoutStatusMessagesFor(l language) *layoutStatusMessages {
	if parseLanguage(string(l)) == langEnglish {
		return &layoutStatusMessagesEn
	}
	return &layoutStatusMessagesZh
}

func (a *App) layoutMsg() *layoutStatusMessages {
	if a == nil || a.mgr == nil || a.mgr.cfg == nil {
		return layoutStatusMessagesFor(defaultLanguage())
	}
	return layoutStatusMessagesFor(a.mgr.cfg.language())
}
