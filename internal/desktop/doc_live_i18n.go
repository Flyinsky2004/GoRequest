package desktop

import (
	"strings"

	"gorequest/internal/docedit"
)

// docLiveMessages are the canvas strings kept beside the view that uses
// them, so no public i18n file needs a cross-task edit.
type docLiveMessages struct {
	Canvas      string
	EmptyHint   string
	AddBlock    string
	SlashMenu   string
	NoMatches   string
	NoRequests  string
	SourceBlock string
	DerivedCard string
	RequestHint string
	InvalidIDs  string
}

func docLiveMessagesFor(l language) *docLiveMessages {
	if l == langEnglish {
		return &docLiveMessages{
			Canvas:      "Canvas",
			EmptyHint:   "Type / to insert content, @ to reference a request",
			AddBlock:    "Click to add a block, or type / to insert",
			SlashMenu:   "Insert block",
			NoMatches:   "No matching blocks",
			NoRequests:  "No requests in this project",
			SourceBlock: "Source block",
			DerivedCard: "Request card (read-only; edit the directive)",
			RequestHint: "Choose a request to reference",
			InvalidIDs:  "Some requests have IDs that documents cannot reference",
		}
	}
	return &docLiveMessages{
		Canvas:      "文档",
		EmptyHint:   "输入 / 插入内容，输入 @ 引用接口",
		AddBlock:    "点击添加块，或输入 / 插入",
		SlashMenu:   "插入块",
		NoMatches:   "没有匹配的块",
		NoRequests:  "当前项目没有接口",
		SourceBlock: "源码块",
		DerivedCard: "接口卡片（只读，编辑原始指令）",
		RequestHint: "选择要引用的接口",
		InvalidIDs:  "部分接口 ID 无法被文档引用",
	}
}

func (a *App) docLiveMsg() *docLiveMessages {
	lang := defaultLanguage()
	if a.mgr != nil && a.mgr.cfg != nil {
		lang = a.mgr.cfg.language()
	}
	return docLiveMessagesFor(lang)
}

// docSlashLabel returns the menu label in the app's language.
func (a *App) docSlashLabel(cmd docedit.SlashCommand) string {
	if a.mgr != nil && a.mgr.cfg != nil && a.mgr.cfg.language() == langEnglish {
		return cmd.LabelEN
	}
	return cmd.Label
}

// docSlashQueryText is the query shown in the menu heading.
func docSlashQueryText(st *docViewState) string {
	if st.live == nil {
		return ""
	}
	return strings.TrimSpace(st.live.slashQuery)
}
