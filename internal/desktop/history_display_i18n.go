package desktop

// historyDisplayMessages holds the labels added by the U09 history display
// work. They live in their own file so the shared i18n.go catalog is not
// touched by this task.
type historyDisplayMessages struct {
	EndpointOnly     string
	EndpointOnlyHint string
	Detail           string
	Delete           string
}

var historyDisplayMessagesZh = historyDisplayMessages{
	EndpointOnly:     "仅端点",
	EndpointOnlyHint: "只显示路径、查询和片段；悬浮记录可查看完整 URL",
	Detail:           "详情",
	Delete:           "删除",
}

var historyDisplayMessagesEn = historyDisplayMessages{
	EndpointOnly:     "Endpoint only",
	EndpointOnlyHint: "Show only path, query and fragment; hover a row for the full URL",
	Detail:           "Details",
	Delete:           "Delete",
}

func historyDisplayMessagesFor(l language) *historyDisplayMessages {
	if parseLanguage(string(l)) == langEnglish {
		return &historyDisplayMessagesEn
	}
	return &historyDisplayMessagesZh
}

func (a *App) historyDisplayMsg() *historyDisplayMessages {
	if a == nil || a.mgr == nil || a.mgr.cfg == nil {
		return historyDisplayMessagesFor(defaultLanguage())
	}
	return historyDisplayMessagesFor(a.mgr.cfg.language())
}
