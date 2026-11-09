package desktop

// runnerMockMessages holds the Runner panel's controlled Mock labels. It is a
// dedicated catalog so N03 does not edit the shared i18n.go file.
type runnerMockMessages struct {
	Enable, Section, Loopback, Scenario, Port, EndpointVar, EndpointHint string
	NoRules, RunningLocked, InvalidConfig                                string
	Result, ScenarioLabel, DigestLabel, Released, NotReleased            string
	AccessLabel, StopError                                               string
}

var runnerMockEN = runnerMockMessages{
	Enable:        "Run against a controlled local Mock",
	Section:       "Controlled Mock (this run only)",
	Loopback:      "The Mock server binds 127.0.0.1 only. The actual port is injected into the run's variables; shared environments are never modified.",
	Scenario:      "Mock scenario",
	Port:          "Port (0 = ephemeral)",
	EndpointVar:   "Extra endpoint variable",
	EndpointHint:  "mockUrl, mockBaseUrl, mockHost and mockPort are always injected; optionally add another name such as baseUrl.",
	NoRules:       "No enabled Mock rules are available for this scenario.",
	RunningLocked: "Mock configuration is fixed while a run is active.",
	InvalidConfig: "Mock configuration error",
	Result:        "Controlled Mock",
	ScenarioLabel: "scenario",
	DigestLabel:   "rules",
	Released:      "server released",
	NotReleased:   "server NOT released",
	AccessLabel:   "Mock requests recorded",
	StopError:     "Mock stop error",
}

var runnerMockZH = runnerMockMessages{
	Enable:        "本次运行使用受控本地 Mock",
	Section:       "受控 Mock（仅本次运行）",
	Loopback:      "Mock 服务仅监听 127.0.0.1。实际端口注入本次运行变量，绝不修改共享环境。",
	Scenario:      "Mock 场景",
	Port:          "端口（0 = 随机空闲）",
	EndpointVar:   "额外端点变量",
	EndpointHint:  "始终注入 mockUrl、mockBaseUrl、mockHost、mockPort；可另加 baseUrl 等名称。",
	NoRules:       "当前场景没有可用的已启用 Mock 规则。",
	RunningLocked: "运行中不可修改 Mock 配置。",
	InvalidConfig: "Mock 配置错误",
	Result:        "受控 Mock",
	ScenarioLabel: "场景",
	DigestLabel:   "规则",
	Released:      "服务已释放",
	NotReleased:   "服务未释放",
	AccessLabel:   "记录的 Mock 请求",
	StopError:     "Mock 停止错误",
}

func (a *App) runnerMockMsg() runnerMockMessages {
	if a != nil && a.mgr != nil && a.mgr.cfg != nil && parseLanguage(string(a.mgr.cfg.language())) == langSimplifiedChinese {
		return runnerMockZH
	}
	return runnerMockEN
}
