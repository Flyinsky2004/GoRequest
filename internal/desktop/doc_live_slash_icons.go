package desktop

import (
	"fmt"

	"github.com/egoist/mygo/ui"

	"gorequest/internal/desktop/uikit"
	"gorequest/internal/docedit"
)

// The slash menu shows one SVG icon per block type, drawn on the same 24×24
// stroke grid as the table toolbar, inside a small bordered tile following the
// slash-command reference structure: Text, H1..H6, lists, Quote, Code block,
// Divider, Table, Link, Image and Request reference. Headings draw a geometric
// "H" beside a stroke digit so every tile stays a pure vector icon.

const docSlashHeadingH = `<path d="M3.5 6.5v11M3.5 12h7M10.5 6.5v11"/>`

var docSlashHeadingDigits = [7]string{
	0: "",
	1: `<path d="M15.5 9.5 18.2 7.5v10"/>`,
	2: `<path d="M14.8 9.8a2.8 2.4 0 0 1 5.5.4c0 2.2-5.5 4.6-5.5 7.3h5.6"/>`,
	3: `<path d="M14.8 8.8a2.7 2.1 0 0 1 5.2.7c0 1.4-1.1 2.4-2.8 2.6 1.9.2 3.1 1.2 3.1 2.8a2.8 2.5 0 0 1-5.5.8"/>`,
	4: `<path d="M19.2 17.5v-10l-4.7 6.8h6.2"/>`,
	5: `<path d="M20 7.5h-4.6l-.4 4.3a3.2 2.2 0 0 1 2.4-.7 2.6 2.6 0 0 1 2.8 2.7c0 2.1-1.6 3.7-3.6 3.7-1.1 0-2-.4-2.5-1.2"/>`,
	6: `<path d="M19.6 8.5a2.8 2.5 0 0 0-4.8 1.8v4.2a2.7 2.7 0 1 0 5.4 0 2.5 2.4 0 0 0-2.7-2.4 2.6 2.4 0 0 0-2.7 2.2"/>`,
}

var (
	docSlashIconText    = docTableIcon(`<path d="M5 7V5.5h14V7M12 5.5v13M9.5 18.5h5"/>`)
	docSlashIconBullet  = docTableIcon(`<line x1="9" y1="7" x2="20" y2="7"/><line x1="9" y1="12" x2="20" y2="12"/><line x1="9" y1="17" x2="20" y2="17"/><circle cx="4.8" cy="7" r="0.9" fill="currentColor" stroke="none"/><circle cx="4.8" cy="12" r="0.9" fill="currentColor" stroke="none"/><circle cx="4.8" cy="17" r="0.9" fill="currentColor" stroke="none"/>`)
	docSlashIconOrdered = docTableIcon(`<line x1="11" y1="7" x2="20" y2="7"/><line x1="11" y1="12" x2="20" y2="12"/><line x1="11" y1="17" x2="20" y2="17"/><path d="M4.2 6l1.4-1v4"/><path d="M4 11.2a1.3 1.2 0 0 1 2.6 0c0 1-2.6 1.8-2.6 3h2.7"/><path d="M4 16.5h2.5l-1.2 1.4a1.1 1.1 0 1 1-1.2 1.4"/>`)
	docSlashIconTask    = docTableIcon(`<rect x="4" y="4" width="16" height="16" rx="3"/><path d="m8.5 12.2 2.4 2.4 4.7-5"/>`)
	docSlashIconQuote   = docTableIcon(`<path d="M9.5 7.5H6.2A1.7 1.7 0 0 0 4.5 9.2v3.1c0 .9.7 1.6 1.6 1.6h2c0 1.6-.8 2.5-2.3 2.8M19.5 7.5h-3.3a1.7 1.7 0 0 0-1.7 1.7v3.1c0 .9.7 1.6 1.6 1.6h2c0 1.6-.8 2.5-2.3 2.8"/>`)
	docSlashIconCode    = docTableIcon(`<rect x="3" y="4.5" width="18" height="15" rx="2.5"/><path d="m9.8 9.8-2.6 2.2 2.6 2.2M14.2 9.8l2.6 2.2-2.6 2.2"/>`)
	docSlashIconDivider = docTableIcon(`<line x1="4" y1="12" x2="20" y2="12"/><line x1="4" y1="6.5" x2="9" y2="6.5"/><line x1="4" y1="17.5" x2="9" y2="17.5"/>`)
	docSlashIconTable   = docTableIcon(`<rect x="4" y="4" width="16" height="16" rx="2"/><line x1="4" y1="10" x2="20" y2="10"/><line x1="10" y1="10" x2="10" y2="20"/>`)
	docSlashIconLink    = docTableIcon(`<path d="M10 14a4 4 0 0 0 5.7 0l3-3a4 4 0 0 0-5.7-5.7l-1 1"/><path d="M14 10a4 4 0 0 0-5.7 0l-3 3a4 4 0 0 0 5.7 5.7l1-1"/>`)
	docSlashIconImage   = docTableIcon(`<rect x="3.5" y="4.5" width="17" height="15" rx="2.5"/><circle cx="9" cy="10" r="1.6"/><path d="m4 17 5-4.5 3.5 3 2.5-2 5 4"/>`)
	docSlashIconRequest = docTableIcon(`<path d="M5 8h13l-3-3"/><path d="M19 16H6l3 3"/>`)
	docSlashIconBlock   = docTableIcon(`<rect x="4" y="4" width="16" height="16" rx="3"/>`)
	docSlashIconChevron = docTableIcon(`<path d="m9 6 6 6-6 6"/>`)

	docSlashIconHeadings = func() [7]*ui.SVG {
		var out [7]*ui.SVG
		for lvl := 1; lvl <= 6; lvl++ {
			out[lvl] = docTableIcon(docSlashHeadingH + docSlashHeadingDigits[lvl])
		}
		return out
	}()
)

// docSlashIcon returns the SVG icon for a slash command. Every command has
// one; an unknown kind gets a plain block tile so a row never lacks its icon.
func docSlashIcon(cmd docedit.SlashCommand) *ui.SVG {
	switch cmd.Kind {
	case docedit.SlashParagraph:
		return docSlashIconText
	case docedit.SlashHeading:
		return docSlashIconHeadings[max(1, min(cmd.Level, 6))]
	case docedit.SlashBullet:
		return docSlashIconBullet
	case docedit.SlashOrdered:
		return docSlashIconOrdered
	case docedit.SlashTask:
		return docSlashIconTask
	case docedit.SlashQuote:
		return docSlashIconQuote
	case docedit.SlashCode:
		return docSlashIconCode
	case docedit.SlashDivider:
		return docSlashIconDivider
	case docedit.SlashTable:
		return docSlashIconTable
	case docedit.SlashLink:
		return docSlashIconLink
	case docedit.SlashImage:
		return docSlashIconImage
	case docedit.SlashRequest:
		return docSlashIconRequest
	}
	return docSlashIconBlock
}

// docSlashDescription is the one-line hint under a command's name.
func (a *App) docSlashDescription(cmd docedit.SlashCommand) string {
	en := a.mgr != nil && a.mgr.cfg != nil && a.mgr.cfg.language() == langEnglish
	pick := func(zh, e string) string {
		if en {
			return e
		}
		return zh
	}
	switch cmd.Kind {
	case docedit.SlashParagraph:
		return pick("使用普通正文段落输入", "Start typing with plain text.")
	case docedit.SlashHeading:
		switch cmd.Level {
		case 1:
			return pick("最大字号的一级标题", "Headings in the largest font.")
		case 2:
			return pick("次大字号的二级章节标题", "Headings in the 2nd font size.")
		case 3:
			return pick("三级小节标题", "Headings in the 3rd font size.")
		default:
			if en {
				return fmt.Sprintf("Level %d subsection heading.", cmd.Level)
			}
			return fmt.Sprintf("第 %d 级小节标题", cmd.Level)
		}
	case docedit.SlashBullet:
		return pick("使用圆点组织条目", "Create a simple bulleted list.")
	case docedit.SlashOrdered:
		return pick("带自动序号的条目列表", "Create a list with numbering.")
	case docedit.SlashTask:
		return pick("带复选框的待办事项", "Track tasks with checkboxes.")
	case docedit.SlashQuote:
		return pick("添加引用块以突出说明", "Add a blockquote for emphasis.")
	case docedit.SlashCode:
		return pick("保留缩进与高亮的代码片段", "Code snippet with formatting.")
	case docedit.SlashDivider:
		return pick("使用水平分割线隔开章节", "Visually divide sections with a rule.")
	case docedit.SlashTable:
		return pick("插入可视化编辑的表格", "Insert an editable Markdown table.")
	case docedit.SlashLink:
		return pick("插入带文字的链接", "Insert a hyperlink with label.")
	case docedit.SlashImage:
		return pick("插入项目文档内的图片", "Embed a document image.")
	case docedit.SlashRequest:
		return pick("选择并引用当前项目的接口", "Choose a project request to reference.")
	}
	return ""
}

// docSlashIconTile draws the bordered icon tile of a slash menu row.
func docSlashIconTile(c *ui.Context, p uikit.Palette, icon *ui.SVG, selected bool) {
	fg := p.Muted
	if selected {
		fg = p.Text
	}
	ui.Row(c).Width(28).Height(28).AlignItems(ui.Center).Justify(ui.Center).Radius(6).
		Background(p.BG).Border(1, p.Hairline).Shrink(0).Children(func() {
		ui.Icon(c, icon).FontSize(15).TextColor(fg)
	})
}
