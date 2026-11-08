package desktop

// layoutStatusMessages holds the request-send terminal status lines. They live
// in their own file so the shared i18n.go catalog is not touched by the R9
// layout/status work.
type layoutStatusMessages struct {
	SendDoneFmt     string // status code, formatted duration
	SendFailedFmt   string // formatted duration, error
	SendCanceledFmt string // formatted duration
}

var layoutStatusMessagesZh = layoutStatusMessages{
	SendDoneFmt:     "已收到 %d 响应 · %s",
	SendFailedFmt:   "请求失败 · %s · %s",
	SendCanceledFmt: "请求已取消 · %s",
}

var layoutStatusMessagesEn = layoutStatusMessages{
	SendDoneFmt:     "received %d response · %s",
	SendFailedFmt:   "request failed · %s · %s",
	SendCanceledFmt: "request canceled · %s",
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
