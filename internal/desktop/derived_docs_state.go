package desktop

import (
	"gorequest/internal/docgen"

	"github.com/egoist/mygo/ui"
)

// This file owns the per-tab state and strings of the derived-document
// export panel. All of it lives in derived_docs*.go so no shared desktop
// file grows outside the integrator's control; the documentation
// workspace only gains one button and one panel call through the
// integration patch.

// derivedDocStateKey names the element-local state store of the panel.
type derivedDocStateKey struct{}

type derivedDocStateStore struct {
	states map[*Tab]*derivedDocState
}

// derivedDocState is the state of the export/preview panel for one open
// document tab.
type derivedDocState struct {
	// open toggles the panel (header button).
	open bool

	// scope 0 = current document, 1 = all documents.
	scope int
	// format 0 = Markdown set, 1 = static HTML set.
	format     int
	target     string
	assets     bool
	overwr     bool
	preview    *docgen.Preview
	result     *docgen.Result
	lastErr    string
	notice     string
	scroll     ui.ScrollState
	diagScroll ui.ScrollState

	// previewSrc caches the docgen-expanded source of the document preview
	// (patched preview hook); the digest keys the cache.
	previewDigest  string
	previewContent string
	previewRefs    bool
}

// derivedDocMessages are the panel strings kept beside their view so no
// public i18n file needs a cross-task edit (the B09 docMessages pattern).
type derivedDocMessages struct {
	Export         string
	PanelTitle     string
	ScopeLabel     string
	ScopeThis      string
	ScopeAll       string
	FormatLabel    string
	FormatMD       string
	FormatHTML     string
	TargetLabel    string
	Browse         string
	IncludeAssets  string
	AllowOverwrite string
	PreviewAction  string
	ExportAction   string
	Files          string // %d
	Assets         string // %d
	Bytes          string // %d
	Conflicts      string // %d
	Problems       string // %d
	NoProblems     string
	NoConflicts    string
	Skipped        string // %d
	ViewSkipped    string
	Failed         string // %s
	Done           string // %d
	ProblemsBlock  string // %d
	TargetEmpty    string
	OpenRequest    string // %s
	RequestMissing string // %s
	InsertRef      string
	InsertCard     string
	FormatHint     string
}

func derivedDocMessagesFor(l language) *derivedDocMessages {
	if l == langEnglish {
		return &derivedDocMessages{
			Export: "Export", PanelTitle: "Derived docs export",
			ScopeLabel: "Scope", ScopeThis: "This document", ScopeAll: "All documents",
			FormatLabel: "Format", FormatMD: "Markdown set", FormatHTML: "Static HTML",
			TargetLabel: "Target directory", Browse: "Choose…",
			IncludeAssets: "Copy local attachments", AllowOverwrite: "Allow replacing previous exports",
			PreviewAction: "Preview", ExportAction: "Export",
			Files: "%d files", Assets: "%d attachments", Bytes: "%d bytes",
			Conflicts: "%d conflicts", Problems: "%d problems", NoProblems: "No link or reference problems",
			NoConflicts: "No target conflicts", Skipped: "%d skipped", ViewSkipped: "Skipped files",
			Failed: "Export failed: %s", Done: "Exported %d files",
			ProblemsBlock: "%d error-level problems block the export; resolve them first",
			TargetEmpty:   "Choose a target directory outside the project first",
			OpenRequest:   "Opening request %s", RequestMissing: "Request %s is not in this project",
			InsertRef: "Request link…", InsertCard: "Request card directive…",
			FormatHint: "Exports are offline: no scripts, no CDN, no remote fetches.",
		}
	}
	return &derivedDocMessages{
		Export: "导出", PanelTitle: "派生文档导出",
		ScopeLabel: "范围", ScopeThis: "当前文档", ScopeAll: "全部文档",
		FormatLabel: "格式", FormatMD: "Markdown 文档集", FormatHTML: "静态 HTML",
		TargetLabel: "目标目录", Browse: "选择…",
		IncludeAssets: "复制本地附件", AllowOverwrite: "允许覆盖此前导出的文件",
		PreviewAction: "预览", ExportAction: "导出",
		Files: "%d 个文件", Assets: "%d 个附件", Bytes: "%d 字节",
		Conflicts: "%d 个目标冲突", Problems: "%d 个问题", NoProblems: "没有链接或引用问题",
		NoConflicts: "没有目标冲突", Skipped: "%d 个已跳过", ViewSkipped: "跳过的文件",
		Failed: "导出失败：%s", Done: "已导出 %d 个文件",
		ProblemsBlock: "%d 个错误级问题未解决，已阻止导出",
		TargetEmpty:   "请先选择项目之外的目标目录",
		OpenRequest:   "打开请求 %s", RequestMissing: "项目内没有请求 %s",
		InsertRef: "请求链接…", InsertCard: "请求卡片指令…",
		FormatHint: "导出完全离线：无脚本、无 CDN、不抓取远程资源。",
	}
}

// derivedDocMsg resolves the panel strings for the app language.
func (a *App) derivedDocMsg() *derivedDocMessages {
	lang := defaultLanguage()
	if a.mgr != nil && a.mgr.cfg != nil {
		lang = a.mgr.cfg.language()
	}
	return derivedDocMessagesFor(lang)
}

// derivedDocStateFor returns the per-tab panel state stored on the doc
// workspace root (element-local like the B09 doc state), pruned when tabs
// close.
func derivedDocStateFor(root ui.Element, tab *Tab) *derivedDocState {
	store := ui.Local(root, derivedDocStateKey{}, func() derivedDocStateStore {
		return derivedDocStateStore{states: map[*Tab]*derivedDocState{}}
	})
	if st, ok := store.states[tab]; ok {
		return st
	}
	st := &derivedDocState{}
	store.states[tab] = st
	return st
}

// pruneDerivedDocStates drops states of tabs that are no longer open.
func pruneDerivedDocStates(root ui.Element, ps *projectState) {
	if ps == nil {
		return
	}
	local := ui.Local(root, derivedDocStateKey{}, func() derivedDocStateStore {
		return derivedDocStateStore{states: map[*Tab]*derivedDocState{}}
	})
	for tab := range local.states {
		open := false
		for _, t := range ps.tabs {
			if t == tab {
				open = true
				break
			}
		}
		if !open {
			delete(local.states, tab)
		}
	}
}

// cardLabelsFor maps the app language onto docgen card labels.
func (a *App) cardLabelsFor() docgen.CardLabels {
	lang := defaultLanguage()
	if a.mgr != nil && a.mgr.cfg != nil {
		lang = a.mgr.cfg.language()
	}
	if lang == langEnglish {
		return docgen.EnglishCardLabels()
	}
	return docgen.ChineseCardLabels()
}
