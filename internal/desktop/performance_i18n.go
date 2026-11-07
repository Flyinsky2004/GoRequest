package desktop

type perfMessages struct {
	Title          string
	MenuLabel      string
	Subtitle       string
	Request        string
	Environment    string
	Iterations     string
	Concurrency    string
	Warmup         string
	IntervalMs     string
	DeadlineSec    string
	Seed           string
	Strategy       string
	StrategyReuse  string
	StrategyFresh  string
	EnableGate     string
	GateQuantile   string
	GateMaxMs      string
	GateMinSamples string
	GateMaxFailPct string
	Start          string
	Cancel         string
	Close          string
	PinBaseline    string
	ClearBaseline  string
	Running        string
	DirtyWarn      string
	ScopeNotice    string
	NoRequests     string
	ExportPath     string
	Browse         string
	ExportJSON     string
	ExportCSV      string
	Exported       string
	TabSummary     string
	TabStages      string
	TabTimeline    string
	TabSamples     string
	TabCompare     string
}

var perfEN = perfMessages{
	Title:          "HTTP Performance Diagnostics",
	MenuLabel:      "Performance Diagnostics…",
	Subtitle:       "Controlled low-concurrency client HTTP timing, per-hop trace timeline, and nearest-rank quantile statistics (not a load test).",
	Request:        "Saved Request",
	Environment:    "Environment (fixed for run)",
	Iterations:     "Samples (1..200)",
	Concurrency:    "Concurrency (1..8)",
	Warmup:         "Warmup (0..20)",
	IntervalMs:     "Interval (ms)",
	DeadlineSec:    "Deadline (s)",
	Seed:           "Seed",
	Strategy:       "Connection Strategy",
	StrategyReuse:  "Keep-Alive (Reuse)",
	StrategyFresh:  "Fresh per Sample",
	EnableGate:     "Enable Threshold Gate",
	GateQuantile:   "Gate Quantile",
	GateMaxMs:      "Max Latency (ms)",
	GateMinSamples: "Min Valid Samples (n)",
	GateMaxFailPct: "Max Failure Rate (0..1)",
	Start:          "Run Diagnostics Snapshot",
	Cancel:         "Cancel Run",
	Close:          "Close",
	PinBaseline:    "Pin Current as Baseline",
	ClearBaseline:  "Clear Baseline",
	Running:        "Sampling in progress…",
	DirtyWarn:      "Unsaved request or environment edits are excluded; diagnostics always use the saved project snapshot.",
	ScopeNotice:    "Low-concurrency client HTTP diagnostics only. Unobserved or reused stages are never recorded as 0 ms.",
	NoRequests:     "No saved requests in the current project.",
	ExportPath:     "Local export path (outside project directory)",
	Browse:         "Choose path…",
	ExportJSON:     "Export JSON",
	ExportCSV:      "Export CSV",
	Exported:       "Diagnostic report exported to ",
	TabSummary:     "Summary & Quantiles",
	TabStages:      "Stage Breakdown (Independent n)",
	TabTimeline:    "Per-Hop Timeline & Limits",
	TabSamples:     "Samples & Errors",
	TabCompare:     "Baseline Compare",
}

var perfZH = perfMessages{
	Title:          "HTTP 性能诊断与受控采样",
	MenuLabel:      "性能诊断与采样…",
	Subtitle:       "受控低并发客户端 HTTP 阶段耗时、逐跳时间线与 Nearest-Rank 分位数统计（非高并发压测工具）。",
	Request:        "已保存请求",
	Environment:    "运行环境（本次采样固定）",
	Iterations:     "采样次数 (1..200)",
	Concurrency:    "并发上限 (1..8)",
	Warmup:         "预热次数 (0..20)",
	IntervalMs:     "采样间隔 (ms)",
	DeadlineSec:    "总体时限 (s)",
	Seed:           "随机种子 (Seed)",
	Strategy:       "连接复用策略",
	StrategyReuse:  "Keep-Alive 复用连接",
	StrategyFresh:  "每次新建连接 (Fresh)",
	EnableGate:     "启用阈值门禁",
	GateQuantile:   "门禁分位数",
	GateMaxMs:      "最大耗时阈值 (ms)",
	GateMinSamples: "最少有效样本数 (n)",
	GateMaxFailPct: "最大允许失败率 (0..1)",
	Start:          "运行保存快照诊断",
	Cancel:         "取消采样",
	Close:          "关闭",
	PinBaseline:    "设为对比基线",
	ClearBaseline:  "清除对比基线",
	Running:        "正在执行诊断采样…",
	DirtyWarn:      "未保存的请求或环境修改不会纳入采样；诊断始终读取磁盘已保存快照。",
	ScopeNotice:    "仅限受控低并发客户端 HTTP 诊断；未发生或复用跳过的阶段绝不记为 0 ms。",
	NoRequests:     "当前项目没有已保存的请求。",
	ExportPath:     "本地导出路径（必须位于项目目录之外）",
	Browse:         "选择路径…",
	ExportJSON:     "导出 JSON",
	ExportCSV:      "导出 CSV",
	Exported:       "诊断报告已导出至 ",
	TabSummary:     "总览与分位数",
	TabStages:      "阶段统计（独立 n）",
	TabTimeline:    "逐跳时间线与限制",
	TabSamples:     "样本与错误明细",
	TabCompare:     "基线对比",
}

func (a *App) perfMsg() perfMessages {
	if a != nil && a.mgr != nil && a.mgr.cfg != nil && a.mgr.cfg.language() == "zh" {
		return perfZH
	}
	return perfEN
}
