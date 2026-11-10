package desktop

type runnerMessages struct {
	Title, Collection, Suite, Environment, Start, Cancel, Close, EditorEntry, Dirty, Snapshot, Running, SnapshotFailed, SelectionFailed, Empty, ExportPath, JSON, JUnit, Browse, Exported, EventLimit string
	StepConcurrency, RowConcurrency, ConcurrencyHint                                                                                                                                                  string
	ResultFilter, FilterAll, FilterPassed, FilterFailed, FilterSkipped, FilterCanceled, FilterConfig, FilterInternal                                                                                  string
	FilterText, PrevPage, NextPage, PageInfo, NoMatches, ShowFailures, FailureSummary                                                                                                                 string
}

var runnerEN = runnerMessages{"Collection / Suite Runner", "Collection", "Saved Suite (.suite.yaml)", "Environment (fixed for this run)", "Run saved snapshot", "Cancel run", "Close", "Edit assertions & extraction", "Unsaved edits are excluded from the saved snapshot.", "Runs use saved requests, Suite, dataset and schemas. Project/environment changes do not affect an active run.", "Running…", "Configuration error: cannot capture saved project snapshot.", "Configuration error: select a nonempty collection or a valid saved Suite.", "No saved items available.", "Local report path (outside this project)", "Export JSON", "Export JUnit", "Choose path…", "Report exported", "Most recent 256 events; the final view is paged and filterable. Export for the complete result.", "Step concurrency (1–64)", "Row concurrency (1–16)", "Validated at start; dependency order and per-row isolation are unchanged.",
	"Result filter", "All", "Passed", "Failed", "Skipped", "Canceled", "Config errors", "Internal errors",
	"Find in step id, name, request or reason", "Previous page", "Next page", "%d–%d of %d step results", "No step result matches this filter.", "Show failures", "Failure locations: %d (step and per-attempt)."}
var runnerZH = runnerMessages{"集合 / Suite 运行器", "集合", "已保存 Suite（.suite.yaml）", "环境（本次运行固定）", "运行保存快照", "取消运行", "关闭", "编辑断言与提取", "未保存的编辑不会纳入保存快照。", "使用已保存请求、Suite、数据及 Schema。运行中切换项目或环境不会改变本次计划。", "正在运行…", "配置错误：无法读取保存的项目快照。", "配置错误：请选择非空集合或有效的已保存 Suite。", "没有可用的已保存条目。", "本地报告路径（当前项目之外）", "导出 JSON", "导出 JUnit", "选择路径…", "报告已导出", "显示最近 256 条事件；最终结果可筛选并分页。导出可查看完整结果。", "步骤并发（1–64）", "数据行并发（1–16）", "启动时校验；不改变依赖顺序与每行隔离。",
	"结果筛选", "全部", "通过", "失败", "跳过", "已取消", "配置错误", "内部错误",
	"搜索步骤 ID、名称、请求或原因", "上一页", "下一页", "第 %d–%d 条，共 %d 条步骤结果", "没有匹配的步骤结果。", "只看失败", "失败定位：%d 处（步骤与逐次尝试）。"}

func (a *App) runnerMsg() runnerMessages {
	if a != nil && a.mgr != nil && a.mgr.cfg != nil && parseLanguage(string(a.mgr.cfg.language())) == langSimplifiedChinese {
		return runnerZH
	}
	return runnerEN
}
