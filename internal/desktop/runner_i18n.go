package desktop

type runnerMessages struct{ Title, Collection, Suite, Environment, Start, Cancel, Close, Dirty, Snapshot, Running, SnapshotFailed, SelectionFailed, Empty, ExportPath, JSON, JUnit, Browse, Exported string }

var runnerEN = runnerMessages{"Collection / Suite Runner", "Collection", "Saved Suite (.suite.yaml)", "Environment (fixed for this run)", "Run saved snapshot", "Cancel run", "Close", "Unsaved edits are excluded from the saved snapshot.", "Runs use saved requests, Suite, dataset and schemas. Project/environment changes do not affect an active run.", "Running…", "Configuration error: cannot capture saved project snapshot.", "Configuration error: select a nonempty collection or a valid saved Suite.", "No saved items available.", "Local report path (outside this project)", "Export JSON", "Export JUnit", "Choose path…", "Report exported"}
var runnerZH = runnerMessages{"集合 / Suite 运行器", "集合", "已保存 Suite（.suite.yaml）", "环境（本次运行固定）", "运行保存快照", "取消运行", "关闭", "未保存的编辑不会纳入保存快照。", "使用已保存请求、Suite、数据及 Schema。运行中切换项目或环境不会改变本次计划。", "正在运行…", "配置错误：无法读取保存的项目快照。", "配置错误：请选择非空集合或有效的已保存 Suite。", "没有可用的已保存条目。", "本地报告路径（当前项目之外）", "导出 JSON", "导出 JUnit", "选择路径…", "报告已导出"}

func (a *App) runnerMsg() runnerMessages {
	if a != nil && a.mgr != nil && a.mgr.cfg != nil && a.mgr.cfg.language() == "zh" {
		return runnerZH
	}
	return runnerEN
}
