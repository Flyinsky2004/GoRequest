package desktop

import (
	"fmt"
	"path"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/egoist/mygo/ui/markdown"

	"gorequest/internal/core"
	"gorequest/internal/desktop/uikit"
)

// This file owns the document workspace's interaction state and its extra
// panels: the heading outline, project full-text search, link and attachment
// diagnostics, insertion helpers and the validated local-image loader. The
// package-level state lives on the doc view element (ui.Local) keyed by the
// tab, so two documents (or two project windows) never share a query, a
// scroll position or an anchor.

// docViewState is what one document tab keeps between frames.
type docViewState struct {
	digest   string
	baseline string
	headings []core.Heading

	outlineOpen bool
	searchOpen  bool
	searchQuery string
	// searchScope is 0 for the current document, 1 for every document.
	searchScope int
	searchDone  bool
	results     []core.DocMatch
	searchDiags []core.DocDiagnostic
	truncated   bool

	problemsOpen bool
	diagnostics  []core.DocDiagnostic

	anchor        string
	anchorVersion uint64
	// pendingAnchor is set when a link from another tab targets this one;
	// the next frame applies it to anchor/anchorVersion.
	pendingAnchor string

	options *markdown.Options

	outlineScroll  ui.ScrollState
	resultsScroll  ui.ScrollState
	problemsScroll ui.ScrollState
}

// docStateKey names the element-local store; docStateStore keeps one explicit
// state per open document tab and is pruned when a tab closes, so a long
// session does not accumulate view state for documents that are gone.
type docStateKey struct{}

type docStateStore struct {
	states map[*Tab]*docViewState
}

// docStateFor returns the per-tab document state stored on root. The store is
// element-local (ui.Local), and the key is the tab, so switching tabs,
// opening another document, or closing a tab never leaks outline/search/
// anchor state into a different document.
func docStateFor(root ui.Element, tab *Tab) *docViewState {
	store := ui.Local(root, docStateKey{}, func() docStateStore {
		return docStateStore{states: map[*Tab]*docViewState{}}
	})
	if st, ok := store.states[tab]; ok {
		return st
	}
	st := &docViewState{}
	store.states[tab] = st
	return st
}

// pruneDocStates drops the state of tabs that are no longer open.
func pruneDocStates(root ui.Element, ps *projectState) {
	if ps == nil {
		return
	}
	store := ui.Local(root, docStateKey{}, func() docStateStore {
		return docStateStore{states: map[*Tab]*docViewState{}}
	})
	for tab := range store.states {
		open := false
		for _, t := range ps.tabs {
			if t == tab {
				open = true
				break
			}
		}
		if !open {
			delete(store.states, tab)
		}
	}
}

func (st *docViewState) applyPending() {
	if st.pendingAnchor != "" {
		st.anchor = st.pendingAnchor
		st.anchorVersion++
		st.pendingAnchor = ""
	}
}

// docMessages are the document-workspace strings kept beside the view that
// uses them, so no public i18n file needs a cross-task edit.
type docMessages struct {
	Outline            string
	Search             string
	SearchPlaceholder  string
	ThisDoc            string
	AllDocs            string
	SearchAction       string
	NoResults          string
	Results            string // %d
	Truncated          string // %d
	Problems           string
	NoProblems         string
	Insert             string
	InsertImageDocs    string
	InsertImageSibling string
	InsertLink         string
	Bold               string
	Italic             string
	CodeBlock          string
	Table              string
	NoHeadings         string
	Imported           string // %s
	ImportFailed       string // %s
	AttachmentRef      string // %s
	DialogUnavailable  string
	ImageFilter        string
	AttachmentFilter   string
	BlockedLink        string // %s
	MissingDocument    string // %s
	MissingAttachment  string // %s
	MissingAnchor      string // %s
	UnsupportedAttach  string // %s
	DiagMissingLink    string // %s
	DiagMissingAsset   string // %s
	DiagDangerous      string // %s
	DiagRemoteImage    string // %s
	DiagHTML           string
	DiagMermaid        string
	DiagBadLink        string // %s
	HeadingLabel       string // %d, %s
	OutlineHint        string
}

func docMessagesFor(l language) *docMessages {
	if l == langEnglish {
		return &docMessages{
			Outline: "Outline", Search: "Search", SearchPlaceholder: "Find in documents…",
			ThisDoc: "This document", AllDocs: "All documents", SearchAction: "Find",
			NoResults: "No matches", Results: "%d matches", Truncated: "showing the first %d",
			Problems: "Problems", NoProblems: "No link or attachment problems",
			Insert: "Insert…", InsertImageDocs: "Image to docs/assets…",
			InsertImageSibling: "Image next to this document…", InsertLink: "Link…",
			Bold: "Bold", Italic: "Italic", CodeBlock: "Code block", Table: "Table",
			NoHeadings: "No headings", Imported: "Inserted %s",
			ImportFailed: "Import failed: %s", AttachmentRef: "Attachment: %s",
			DialogUnavailable: "The file dialog is not available in this window",
			ImageFilter:       "Images", AttachmentFilter: "Attachments",
			BlockedLink: "Blocked link: %s", MissingDocument: "Missing document: %s",
			MissingAttachment: "Missing attachment: %s", MissingAnchor: "Missing heading: %s",
			UnsupportedAttach: "Unsupported attachment type: %s",
			DiagMissingLink:   "Missing document: %s", DiagMissingAsset: "Missing attachment: %s",
			DiagDangerous: "Blocked link: %s", DiagRemoteImage: "Remote image not loaded: %s",
			DiagHTML:    "HTML is shown as text and never executed",
			DiagMermaid: "Mermaid is not rendered in this build; the source is kept",
			DiagBadLink: "Bad link: %s", HeadingLabel: "H%d %s",
			OutlineHint: "Click a heading to jump",
		}
	}
	return &docMessages{
		Outline: "大纲", Search: "搜索", SearchPlaceholder: "在文档中查找…",
		ThisDoc: "当前文档", AllDocs: "全部文档", SearchAction: "查找",
		NoResults: "无匹配结果", Results: "%d 条结果", Truncated: "仅显示前 %d 条",
		Problems: "问题", NoProblems: "未发现链接或附件问题",
		Insert: "插入…", InsertImageDocs: "图片插入 docs/assets…",
		InsertImageSibling: "图片插入文档同级 assets…", InsertLink: "链接…",
		Bold: "加粗", Italic: "斜体", CodeBlock: "代码块", Table: "表格",
		NoHeadings: "无标题", Imported: "已插入 %s",
		ImportFailed: "导入失败：%s", AttachmentRef: "附件：%s",
		DialogUnavailable: "当前窗口无法打开文件对话框",
		ImageFilter:       "图片", AttachmentFilter: "附件",
		BlockedLink: "已拦截链接：%s", MissingDocument: "文档不存在：%s",
		MissingAttachment: "附件不存在：%s", MissingAnchor: "标题不存在：%s",
		UnsupportedAttach: "不支持的附件类型：%s",
		DiagMissingLink:   "文档不存在：%s", DiagMissingAsset: "附件不存在：%s",
		DiagDangerous: "已拦截链接：%s", DiagRemoteImage: "远程图片未加载：%s",
		DiagHTML:    "HTML 仅按文本显示，不执行",
		DiagMermaid: "当前版本不渲染 Mermaid，保留源代码",
		DiagBadLink: "链接无效：%s", HeadingLabel: "H%d %s",
		OutlineHint: "点击标题跳转",
	}
}

func (a *App) docMsg() *docMessages {
	lang := defaultLanguage()
	if a.mgr != nil && a.mgr.cfg != nil {
		lang = a.mgr.cfg.language()
	}
	return docMessagesFor(lang)
}

// docWorkspaceView draws the document tab: source/preview/split plus the
// outline, search, diagnostics and insertion affordances. workspace.go's
// docView delegates here so the shared file only points at the owner.
func (a *App) docWorkspaceView(c *ui.Context, t *ui.Theme, tab *Tab) {
	m := a.msg()
	dm := a.docMsg()
	p := uikit.PaletteFor(t)
	root := ui.Column(c).Grow(1).FillHeight()
	pruneDocStates(root, tab.Owner)
	st := docStateFor(root, tab)
	src := tab.DocEditor.Buffer.String()
	digest := core.Digest([]byte(src))
	// The editor buffer tracks its own saved text (including undo back to
	// it), so it is the authoritative dirty signal; a save updates the tab
	// baseline, which marks the buffer saved again. Comparing digests alone
	// would report every CRLF file dirty because the editor normalizes to LF.
	if st.baseline != tab.Baseline {
		st.baseline = tab.Baseline
		tab.DocEditor.Buffer.MarkSaved()
	}
	tab.Dirty = tab.DocEditor.Buffer.Modified()
	c.OnShortcut(ui.Cmd, ui.KeyS, func() {
		if tab.Owner != nil && tab.Owner.activeTab() == tab {
			a.saveActive()
		}
	})
	if st.digest != digest {
		st.digest = digest
		st.headings = core.ExtractHeadings(src)
		// Link diagnostics are local file stats and parsing only. Keep them
		// current for ordinary documents; very large documents are checked
		// on demand from the Problems button.
		if len(src) <= 256<<10 {
			st.diagnostics = tab.Owner.project.DiagnoseDoc(tab.DocRel, src)
		} else {
			st.diagnostics = nil
		}
	}
	st.applyPending()
	var opts *markdown.Options
	if tab.Section != 1 {
		opts = a.docMarkdownOptions(c, root, tab, st)
	}

	root.Children(func() {
		a.docHeader(c, t, p, tab, st, dm, m)
		if st.searchOpen {
			a.docSearchBar(c, p, tab, st, dm)
			if st.searchDone {
				a.docSearchResults(c, p, root, tab, st, dm)
			}
		}
		if st.problemsOpen {
			a.docProblems(c, p, tab, st, dm)
		}
		ui.Row(c).Grow(1).FillWidth().Children(func() {
			if st.outlineOpen {
				a.docOutline(c, p, tab, st, dm)
				ui.Box(c).Width(1).FillHeight().Background(p.Hairline)
			}
			ui.Column(c).Grow(1).FillHeight().Children(func() {
				a.docBody(c, t, tab, src, opts)
			})
		})
	})
}

func (a *App) docHeader(c *ui.Context, t *ui.Theme, p uikit.Palette, tab *Tab, st *docViewState, dm *docMessages, m *messages) {
	ui.Row(c).Gap(8).Padding(8, 12).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
		uikit.NativeSegmented(c, &tab.Section, m.SplitView, m.EditView, m.PreviewView)
		if uikit.ActionCursor(ui.Button(c, dm.Outline)).Clicked() {
			st.outlineOpen = !st.outlineOpen
		}
		if uikit.ActionCursor(ui.Button(c, dm.Search)).Clicked() {
			st.searchOpen = !st.searchOpen
		}
		problems := dm.Problems
		if n := len(st.diagnostics); n > 0 {
			problems = fmt.Sprintf("%s (%d)", dm.Problems, n)
		}
		if uikit.ActionCursor(ui.Button(c, problems)).Clicked() {
			st.problemsOpen = !st.problemsOpen
			st.diagnostics = tab.Owner.project.DiagnoseDoc(tab.DocRel, tab.DocEditor.Buffer.String())
		}
		ui.Box(c).Grow(1)
		if tab.Dirty {
			if uikit.ActionCursor(ui.PrimaryButton(c, m.SaveDoc)).Clicked() {
				a.saveActive()
			}
		}
		uikit.ActionCursor(ui.MenuButton(c, dm.Insert, func(mm *ui.Menu) { a.docInsertMenu(mm, tab, dm) }))
	})
}

func (a *App) docInsertMenu(mm *ui.Menu, tab *Tab, dm *docMessages) {
	if tab == nil || tab.Owner == nil {
		return
	}
	docsAssets := "docs/assets"
	sibling := path.Join(path.Dir(tab.DocRel), "assets")
	if mm.Item(dm.InsertImageDocs).Chosen() {
		a.chooseDocAsset(tab, docsAssets)
	}
	if mm.Item(dm.InsertImageSibling).Chosen() {
		a.chooseDocAsset(tab, sibling)
	}
	mm.Separator()
	if mm.Item(dm.Bold).Chosen() {
		wrapDocSelection(tab, "**", "**")
	}
	if mm.Item(dm.Italic).Chosen() {
		wrapDocSelection(tab, "*", "*")
	}
	if mm.Item(dm.InsertLink).Chosen() {
		insertDocLink(tab)
	}
	if mm.Item(dm.CodeBlock).Chosen() {
		insertDocTemplate(tab, "```\n", "\n```\n")
	}
	if mm.Item(dm.Table).Chosen() {
		insertDocTemplate(tab, "", "\n| Column | Column |\n| --- | --- |\n| Cell | Cell |\n")
	}
}

func (a *App) docOutline(c *ui.Context, p uikit.Palette, tab *Tab, st *docViewState, dm *docMessages) {
	ui.Column(c).Width(240).FillHeight().Background(p.Sidebar).BorderWidth(0, 0, 0, 1).BorderColor(p.Hairline).Children(func() {
		ui.Row(c).Padding(8, 10).Tooltip(dm.OutlineHint).Children(func() {
			ui.Text(c, dm.Outline).FontSize(12).FontWeight(600).TextColor(p.Muted)
		})
		ui.Scroll(c).Grow(1).FillWidth().TrackScroll(&st.outlineScroll).Children(func() {
			if len(st.headings) == 0 {
				ui.Text(c, dm.NoHeadings).FontSize(12).TextColor(p.Faint).Padding(6, 10)
				return
			}
			cursorLine := tab.DocEditor.CursorPos().Line + 1
			activeID := ""
			if h, ok := core.HeadingAtLine(st.headings, cursorLine); ok {
				activeID = h.ID
			}
			for _, h := range st.headings {
				h := h
				row := ui.Row(c).Key("doc-outline:"+h.ID).FillWidth().Height(26).
					Padding(0, 8+float32(h.Level-1)*12).AlignItems(ui.Center)
				active := h.ID == activeID
				if row.Hovered() {
					row.Background(p.Hover)
				} else if active {
					row.Background(p.Press)
				}
				row.Children(func() {
					color := p.Text
					if h.Level >= 4 {
						color = p.Muted
					}
					ui.Text(c, fmt.Sprintf(dm.HeadingLabel, h.Level, h.Text)).FontSize(12).
						TextColor(color).SingleLine()
				})
				row.Label(fmt.Sprintf(dm.HeadingLabel, h.Level, h.Text))
				if uikit.ActionCursor(row).Clicked() {
					a.docGoToHeading(tab, st, h)
				}
			}
		})
	})
}

func (a *App) docSearchBar(c *ui.Context, p uikit.Palette, tab *Tab, st *docViewState, dm *docMessages) {
	run := func() { a.runDocSearch(tab, st) }
	ui.Row(c).Gap(8).Padding(6, 12).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(p.Hairline).Children(func() {
		inp := ui.TextInput(c, &st.searchQuery).Grow(1).Placeholder(dm.SearchPlaceholder).Label(dm.SearchPlaceholder)
		// Enter is one of the keys a single-line input edits with, so it is
		// seen through OnTextKey rather than a shortcut; k.Text() holds the
		// text typed before this frame's bound value did.
		inp.OnTextKey(func(k ui.TextKey) bool {
			if k.Key != ui.KeyEnter || k.Mods != 0 {
				return false
			}
			st.searchQuery = k.Text()
			run()
			return true
		})
		ui.Box(c).Width(190).Children(func() {
			uikit.NativeSegmented(c, &st.searchScope, dm.ThisDoc, dm.AllDocs)
		})
		if uikit.ActionCursor(ui.Button(c, dm.SearchAction)).Clicked() {
			run()
		}
	})
}

func (a *App) runDocSearch(tab *Tab, st *docViewState) {
	st.searchDone = true
	st.results = nil
	st.searchDiags = nil
	st.truncated = false
	query := strings.TrimSpace(st.searchQuery)
	if query == "" {
		return
	}
	opts := core.SearchOptions{Query: query}
	if st.searchScope == 0 {
		st.results, st.truncated = core.SearchDocText(tab.DocRel, tab.DocEditor.Buffer.String(), opts)
		return
	}
	overrides := map[string]string{}
	if tab.Dirty {
		overrides[tab.DocRel] = tab.DocEditor.Buffer.String()
	}
	opts.Overrides = overrides
	st.results, st.searchDiags, st.truncated = tab.Owner.project.SearchDocs(opts)
}

func (a *App) docSearchResults(c *ui.Context, p uikit.Palette, root ui.Element, tab *Tab, st *docViewState, dm *docMessages) {
	ui.Column(c).MaxHeight(190).FillWidth().BorderWidth(0, 0, 1, 0).BorderColor(p.Hairline).Children(func() {
		if len(st.results) == 0 {
			ui.Text(c, dm.NoResults).FontSize(12).TextColor(p.Muted).Padding(6, 12)
		} else {
			summary := fmt.Sprintf(dm.Results, len(st.results))
			if st.truncated {
				summary = fmt.Sprintf(dm.Results, len(st.results)) + " · " + fmt.Sprintf(dm.Truncated, len(st.results))
			}
			ui.Text(c, summary).FontSize(11).TextColor(p.Muted).Padding(4, 12)
			ui.Scroll(c).Grow(1).FillWidth().TrackScroll(&st.resultsScroll).Children(func() {
				for _, match := range st.results {
					match := match
					label := fmt.Sprintf("%s:%d  %s", match.RelPath, match.Line, match.Snippet)
					row := ui.Row(c).Key("doc-search:"+match.RelPath+":"+fmt.Sprint(match.Line)+":"+fmt.Sprint(match.Column)).
						FillWidth().Height(26).Padding(0, 12).AlignItems(ui.Center)
					if row.Hovered() {
						row.Background(p.Hover)
					}
					row.Children(func() {
						ui.Text(c, label).FontSize(12).TextColor(p.Text).SingleLine()
					})
					row.Label(label)
					if uikit.ActionCursor(row).Clicked() {
						a.openDocSearchMatch(root, tab, match)
					}
				}
			})
		}
		if len(st.searchDiags) > 0 {
			for _, d := range st.searchDiags {
				label := fmt.Sprintf("%s:%d  %s", d.RelPath, d.Line, dm.diagText(d))
				ui.Text(c, label).FontSize(11).TextColor(severityColor(d.Severity, p)).Padding(1, 12).SingleLine()
			}
		}
	})
}

func (a *App) docProblems(c *ui.Context, p uikit.Palette, tab *Tab, st *docViewState, dm *docMessages) {
	ui.Column(c).MaxHeight(160).FillWidth().BorderWidth(0, 0, 1, 0).BorderColor(p.Hairline).Children(func() {
		if len(st.diagnostics) == 0 {
			ui.Text(c, dm.NoProblems).FontSize(12).TextColor(p.Muted).Padding(6, 12)
			return
		}
		ui.Scroll(c).Grow(1).FillWidth().TrackScroll(&st.problemsScroll).Children(func() {
			for _, d := range st.diagnostics {
				d := d
				label := fmt.Sprintf("%d:%d  %s", d.Line, d.Column, dm.diagText(d))
				row := ui.Row(c).FillWidth().Height(24).Padding(0, 12).AlignItems(ui.Center)
				if row.Hovered() {
					row.Background(p.Hover)
				}
				row.Children(func() {
					ui.Text(c, label).FontSize(12).TextColor(severityColor(d.Severity, p)).SingleLine()
				})
				row.Label(label)
				if uikit.ActionCursor(row).Clicked() {
					a.docGoToLine(tab, st, d.Line, d.Column)
				}
			}
		})
	})
}

func severityColor(s core.DiagnosticSeverity, p uikit.Palette) ui.Color {
	switch s {
	case core.SeverityError:
		return uikit.MethodColor("DELETE", p)
	case core.SeverityWarning:
		return uikit.MethodColor("POST", p)
	default:
		return p.Muted
	}
}

func (dm *docMessages) diagText(d core.DocDiagnostic) string {
	switch d.Kind {
	case core.DiagMissingLink:
		return fmt.Sprintf(dm.DiagMissingLink, d.Dest)
	case core.DiagMissingAsset:
		return fmt.Sprintf(dm.DiagMissingAsset, d.Dest)
	case core.DiagDangerousScheme:
		return fmt.Sprintf(dm.DiagDangerous, d.Dest)
	case core.DiagRemoteImage:
		return fmt.Sprintf(dm.DiagRemoteImage, d.Dest)
	case core.DiagHTMLNotExecuted:
		return dm.DiagHTML
	case core.DiagMermaid:
		return dm.DiagMermaid
	default:
		return fmt.Sprintf(dm.DiagBadLink, d.Dest)
	}
}

func (a *App) docBody(c *ui.Context, t *ui.Theme, tab *Tab, src string, opts *markdown.Options) {
	switch tab.Section {
	case 1:
		editor.View(c, tab.DocEditor).Grow(1).FillWidth()
	case 2:
		ui.Scroll(c).Grow(1).FillWidth().Children(func() {
			a.markdownPreview(c, src, opts)
		})
	default:
		docSplit := &tab.Owner.docSplit
		ui.Split(c, docSplit, func() {
			editor.View(c, tab.DocEditor).Grow(1).FillWidth()
		}, func() {
			ui.Scroll(c).Grow(1).FillWidth().Children(func() {
				a.markdownPreview(c, src, opts)
			})
		}).Grow(1).FillWidth()
	}
}

func (a *App) docMarkdownOptions(c *ui.Context, root ui.Element, tab *Tab, st *docViewState) *markdown.Options {
	if st.options == nil {
		st.options = &markdown.Options{}
	}
	o := st.options
	o.Theme = a.mgr.workspacePrefs().markdownPolicy().theme(c.Theme())
	o.LoadImage = func(ref string) (ui.ImageSource, error) {
		data, info, err := tab.Owner.project.ReadDocAsset(tab.DocRel, ref)
		if err != nil {
			return nil, err
		}
		if info.IsSVG {
			return ui.ParseSVG(data)
		}
		return ui.DecodeBitmap(data)
	}
	o.OnLink = func(dest string) { a.handleDocLink(c, root, tab, st, dest) }
	o.Anchor = st.anchor
	o.AnchorVersion = st.anchorVersion
	return o
}

// Padding belongs to the scroll content so all four edges remain available,
// including below the last block. The native view retains its parser, image,
// selection and scroll caches across preference updates.
func (a *App) markdownPreview(c *ui.Context, src string, opts *markdown.Options) {
	padding := float32(a.mgr.workspacePrefs().markdownPolicy().Padding)
	ui.Column(c).FillWidth().Padding(padding).Label("markdown-preview-insets").Children(func() {
		markdown.View(c, src, opts).FillWidth().Label("markdown-preview-content")
	})
}
