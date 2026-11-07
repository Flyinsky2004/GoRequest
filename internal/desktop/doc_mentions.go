package desktop

import (
	"fmt"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"gorequest/internal/core"
	"gorequest/internal/desktop/uikit"
	"gorequest/internal/docgen"
	"gorequest/internal/docmention"
)

// This file owns the @ interface reference completion of the document
// editors. It is a self-contained plugin: it composes whatever callbacks the
// editor already carries (the slash menu, link hovers) instead of replacing
// them, works on the editor.State it is given rather than assuming the tab's
// canonical DocEditor, and never sends a request. The canonical source stays
// plain Markdown; a confirmed mention is written as
//
//	[@auth/用户登录](gorequest://request/auth.login)
//
// so the identity is the stable request ID and the visible label is only a
// snapshot.

const (
	// docMentionMaxRows bounds the rows drawn at once, matching the editor's
	// own completion cap. Search still covers the whole catalogue; the footer
	// says how many more the query found.
	docMentionMaxRows = 200
	// docMentionMaxStaleButtons bounds the repair buttons of the stale strip.
	docMentionMaxStaleButtons = 3
	// docMentionMaxRepairRows bounds the re-pick menu; the completion itself
	// is the searchable path for large projects.
	docMentionMaxRepairRows = 60
)

// docMentionMessages are the strings of the completion and the stale strip,
// kept beside the view that uses them so no shared i18n file changes.
type docMentionMessages struct {
	NoMatch       string
	NoRequests    string
	InvalidOnly   string
	MoreResults   string
	InvalidFooter string
	StaleCount    string
	RepickFor     string
	Repicked      string
	StaleMissing  string
	InvalidRef    string
	MoreRequests  string
}

var docMentionMessagesZh = docMentionMessages{
	NoMatch:       "无匹配接口 · 继续输入",
	NoRequests:    "当前项目还没有接口",
	InvalidOnly:   "%d 个匹配接口的 ID 无法被文档引用",
	MoreResults:   "还有 %d 项，继续输入以缩小范围",
	InvalidFooter: "%d 个接口 ID 无法被文档引用",
	StaleCount:    "%d 个接口引用已失效",
	RepickFor:     "重新选择：%s",
	Repicked:      "已重新绑定接口：%s",
	StaleMissing:  "请求不存在：%s",
	InvalidRef:    "引用 ID 无法被文档引用",
	MoreRequests:  "更多接口请用 @ 补全搜索",
}

var docMentionMessagesEn = docMentionMessages{
	NoMatch:       "No matching interface · keep typing",
	NoRequests:    "This project has no interfaces yet",
	InvalidOnly:   "%d matching interface IDs cannot be referenced from documents",
	MoreResults:   "%d more · keep typing to narrow",
	InvalidFooter: "%d interface IDs cannot be referenced from documents",
	StaleCount:    "%d interface reference(s) no longer resolve",
	RepickFor:     "Re-pick: %s",
	Repicked:      "Interface rebound: %s",
	StaleMissing:  "Request not found: %s",
	InvalidRef:    "The reference ID cannot be used in a document",
	MoreRequests:  "Use @ completion to search more interfaces",
}

func (a *App) docMentionMsg() *docMentionMessages {
	lang := defaultLanguage()
	if a.mgr != nil && a.mgr.cfg != nil {
		lang = a.mgr.cfg.language()
	}
	if lang == langEnglish {
		return &docMentionMessagesEn
	}
	return &docMentionMessagesZh
}

// docMentionSession is the completion state of one editor.State. It is kept
// outside the editor so the plugin can cache the scanned links and the last
// completion payload without touching shared app or tab fields.
type docMentionSession struct {
	app *App
	tab *Tab
	ed  *editor.State

	prevInput      func(ui.InputEvent) bool
	prevCompletion func(editor.CompletionRequest)
	prevRender     func(*ui.Context, ui.Element, *editor.CompletionMenu)
	prevDefinition func(int)

	// at is the '@' offset of the completion shown, shown reports whether
	// this plugin opened it, and invalid counts the matched requests whose
	// ID the URI grammar cannot carry.
	at      int
	shown   bool
	invalid int

	refs        []docmention.LinkRef
	refsVersion uint64
}

// docMentionSessions is the per-editor registry that makes installation
// idempotent. It is private to this plugin; no shared UI map is touched.
var docMentionSessions = struct {
	sync.Mutex
	m map[*editor.State]*docMentionSession
}{m: map[*editor.State]*docMentionSession{}}

func docMentionSessionFor(ed *editor.State) *docMentionSession {
	docMentionSessions.Lock()
	defer docMentionSessions.Unlock()
	return docMentionSessions.m[ed]
}

// installDocMentionHooks installs the @ completion on ed, composing with the
// callbacks already present (a slash menu's OnInput/OnCompletion/
// RenderCompletions, a link hover's OnDefinition). Calling it again for the
// same editor only refreshes the owner tab; it never wraps the callbacks a
// second time.
func (a *App) installDocMentionHooks(tab *Tab, ed *editor.State) {
	if a == nil || ed == nil {
		return
	}
	docMentionSessions.Lock()
	if s, ok := docMentionSessions.m[ed]; ok {
		s.app, s.tab = a, tab
		docMentionSessions.Unlock()
		return
	}
	s := &docMentionSession{
		app: a, tab: tab, ed: ed,
		prevInput:      ed.OnInput,
		prevCompletion: ed.OnCompletion,
		prevRender:     ed.RenderCompletions,
		prevDefinition: ed.OnDefinition,
	}
	docMentionSessions.m[ed] = s
	docMentionSessions.Unlock()

	ed.OnInput = s.onInput
	ed.OnCompletion = s.onCompletion
	ed.RenderCompletions = s.renderCompletions
	ed.OnDefinition = s.onDefinition
}

// onInput takes the '@' that starts a mention and the path characters that
// the editor's word-based completion would otherwise close. Word characters
// are left to the editor so typing stays one undo group and IME commits keep
// their composition transaction; the editor then asks onCompletion, which
// answers with the same mention list.
func (s *docMentionSession) onInput(ev ui.InputEvent) bool {
	if s.ed.ReadOnly {
		return s.delegateInput(ev)
	}
	switch ev.Kind {
	case ui.InputText:
		if s.handleText(ev.Text) {
			return true
		}
	case ui.InputKeyDown:
		switch ev.Key {
		case ui.KeyEscape:
			s.shown = false
		case ui.KeyBackspace:
			if ev.Mods == 0 && s.handleBackspace() {
				return true
			}
		}
	}
	return s.delegateInput(ev)
}

func (s *docMentionSession) delegateInput(ev ui.InputEvent) bool {
	return s.prevInput != nil && s.prevInput(ev)
}

// handleText starts a mention on '@' and continues one on the non-word query
// characters (/, :, -, . …) that the editor would treat as the end of a
// word. It returns false for everything the editor should type itself.
func (s *docMentionSession) handleText(text string) bool {
	if text == "" {
		return false
	}
	src := s.ed.Buffer.String()
	caret := s.ed.Cursor()
	if trig, ok := docmention.FindTrigger(src, caret); ok {
		if !continuesQuery(text) {
			return false
		}
		s.ed.Insert(text)
		s.showAt(trig.Start, s.ed.Cursor())
		return true
	}
	at, ok := startsMention(text)
	if !ok {
		return false
	}
	// The text is typed at the caret, so the '@' lands at caret+at; the
	// context before it is the buffer up to the caret plus the text before
	// the '@'.
	pos := caret + at
	context := src[:caret] + text[:at]
	if !docmention.CanStartAt(context, len(context)) {
		return false
	}
	s.ed.Insert(text)
	s.showAt(pos, s.ed.Cursor())
	return true
}

// startsMention reports the index of the '@' in text when text contains
// exactly one and everything after it can be query text.
func startsMention(text string) (int, bool) {
	if strings.Count(text, "@") != 1 {
		return 0, false
	}
	at := strings.IndexByte(text, '@')
	for _, r := range text[at+1:] {
		if !docmention.IsQueryRune(r) {
			return 0, false
		}
	}
	return at, true
}

// continuesQuery reports whether text should be inserted by the plugin while
// a mention is open. Word characters go to the editor (native typing and IME
// transactions); only a leading non-word query rune would make the editor
// close the list, so those are taken here.
func continuesQuery(text string) bool {
	first, _ := utf8.DecodeRuneInString(text)
	if editorWordRune(first) {
		return false
	}
	for _, r := range text {
		if !docmention.IsQueryRune(r) {
			return false
		}
	}
	return true
}

func editorWordRune(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

func (s *docMentionSession) handleBackspace() bool {
	if !s.shown {
		return false
	}
	src := s.ed.Buffer.String()
	caret := s.ed.Cursor()
	trig, ok := docmention.FindTrigger(src, caret)
	if !ok || trig.Start != s.at {
		s.shown = false
		return false
	}
	s.ed.Run(editor.Backspace)
	src = s.ed.Buffer.String()
	caret = s.ed.Cursor()
	if trig, ok := docmention.FindTrigger(src, caret); ok && trig.Start == s.at {
		s.showAt(trig.Start, caret)
	} else {
		s.shown = false
		s.ed.ShowCompletions(0, nil)
	}
	return true
}

// onCompletion answers the editor's own completion request. A mention takes
// precedence; everything else is passed to the callback that was installed
// before this plugin (for example the slash menu).
func (s *docMentionSession) onCompletion(req editor.CompletionRequest) {
	src := s.ed.Buffer.String()
	trig, ok := docmention.FindTrigger(src, req.Offset)
	if !ok {
		s.shown = false
		if s.prevCompletion != nil {
			s.prevCompletion(req)
		}
		return
	}
	s.showAt(trig.Start, req.Offset)
}

// showAt builds the completion items for the mention at start and shows them
// through the editor's native completion list, so Up/Down, Enter, Tab,
// Escape and mouse clicks keep their built-in behavior and IME candidate
// keys never reach the list.
func (s *docMentionSession) showAt(start, caret int) {
	src := s.ed.Buffer.String()
	if start < 0 || start >= len(src) || src[start] != '@' {
		return
	}
	caret = clampInt(caret, start+1, len(src))
	query := src[start+1 : caret]
	reqs := docmention.Collect(s.project())
	matches := docmention.Search(reqs, query)
	items, invalid := s.buildItems(matches, query, len(reqs))
	s.at, s.shown, s.invalid = start, true, invalid
	s.ed.ShowCompletions(start, items)
}

func (s *docMentionSession) project() *core.Project {
	if s.tab == nil || s.tab.Owner == nil {
		return nil
	}
	return s.tab.Owner.project
}

// buildItems turns matches into editor completions. Requests whose ID the
// URI grammar cannot carry are left out of the list and counted in the
// footer; they are never silently rewritten to another ID.
func (s *docMentionSession) buildItems(matches []docmention.Match, query string, catalogue int) ([]editor.Completion, int) {
	dm := s.app.docMentionMsg()
	labels := make(map[string]int, len(matches))
	for _, m := range matches {
		labels[m.Label()]++
	}
	items := make([]editor.Completion, 0, len(matches)+1)
	invalid := 0
	for _, m := range matches {
		if !m.Valid {
			invalid++
			continue
		}
		link, ok := m.Link()
		if !ok {
			invalid++
			continue
		}
		label := m.Label()
		if labels[label] > 1 {
			// Same folder and name: the ID is what tells them apart.
			label += " · " + m.ID
		}
		detail := m.Detail()
		items = append(items, editor.Completion{
			Label:         label,
			Detail:        detail,
			Kind:          editor.CompletionMethod,
			Insert:        link,
			Documentation: "ID: " + m.ID + "\n" + detail,
			// The search already matched the folder, name, ID, method and
			// address, so every item matches the typed query. Giving them
			// the same filter text keeps the editor's fuzzy filter from
			// reordering the ranked results by the length of their
			// metadata.
			FilterText: "@" + query,
		})
	}
	if len(items) == 0 {
		hint := dm.NoMatch
		switch {
		case len(matches) == 0 && catalogue == 0:
			hint = dm.NoRequests
		case len(matches) == 0:
			hint = dm.NoMatch
		case invalid > 0:
			hint = fmt.Sprintf(dm.InvalidOnly, invalid)
		}
		items = append(items, editor.Completion{
			Label:      hint,
			Kind:       editor.CompletionText,
			Insert:     "@" + query,
			FilterText: "@" + query,
		})
	}
	return items, invalid
}

// mentionShowing reports whether the editor's open completion list is the
// mention list of this session. Deriving it from the buffer keeps a slash
// menu or a stale list from being drawn with mention rows.
func (s *docMentionSession) mentionShowing() bool {
	if !s.shown || s.ed == nil || s.ed.Buffer == nil {
		return false
	}
	trig, ok := docmention.FindTrigger(s.ed.Buffer.String(), s.ed.Cursor())
	return ok && trig.Start == s.at
}

// renderCompletions draws the mention rows with the method color, the
// folder/name label and the address. Anything else falls through to the
// renderer installed before this plugin.
func (s *docMentionSession) renderCompletions(c *ui.Context, panel ui.Element, m *editor.CompletionMenu) {
	if !s.mentionShowing() {
		if s.prevRender != nil {
			s.prevRender(c, panel, m)
			return
		}
		editor.DefaultCompletions(c, panel, m)
		return
	}
	t := c.Theme()
	p := uikit.PaletteFor(t)
	dm := s.app.docMentionMsg()
	lh := m.LineHeight()
	font := m.Font()
	items := m.Items()
	panel.Background(t.Surface).Border(1, t.Border).Radius(t.Radius).
		Shadow(0, 4, 16, 0, ui.RGBA(0, 0, 0, 0.2)).
		Padding(t.Space(1)).MaxHeight(lh*9 + t.Space(2)).
		MinWidth(t.Space(60)).MaxWidth(t.Space(150))
	panel.Children(func() {
		ui.Scroll(c).Key("doc-mention-rows").MaxHeight(lh * 9).Children(func() {
			for i, it := range items {
				if i >= docMentionMaxRows {
					break
				}
				row := m.Item(i).Row().Justify(ui.Start).Gap(t.Space(2)).AlignItems(ui.Center).
					Padding(t.Space(0.5), t.Space(1.5)).Radius(t.Radius - 2).Height(lh + 2)
				if i == m.Selected() {
					row.Background(t.Accent.Alpha(0.18))
				}
				method, rest := splitMentionDetail(it.Detail)
				row.Children(func() {
					if method != "" {
						ui.Text(c, method).Font("monospace").FontSize(font.Size - 1).FontWeight(600).
							TextColor(uikit.MethodColor(method, p)).Width(t.Space(9)).SingleLine()
					}
					ui.Text(c, it.Label).Font(font.Family).FontSize(font.Size).
						TextColor(t.Text).SingleLine().Shrink(1)
					if rest != "" {
						ui.Text(c, rest).FontSize(font.Size - 1).TextColor(t.TextMuted).
							SingleLine().Shrink(1).Grow(1).TextAlign(ui.End)
					}
				})
			}
		})
		if sel := m.Selected(); sel >= 0 && sel < len(items) && items[sel].Documentation != "" {
			ui.Box(c).Height(1).Background(t.Border).Margin(t.Space(1), 0)
			ui.Text(c, items[sel].Documentation).FontSize(t.FontSize-1).TextColor(t.TextMuted).
				MaxWidth(t.Space(140)).Padding(t.Space(0.5), t.Space(1.5))
		}
		var foot []string
		if len(items) > docMentionMaxRows {
			foot = append(foot, fmt.Sprintf(dm.MoreResults, len(items)-docMentionMaxRows))
		}
		if s.invalid > 0 {
			foot = append(foot, fmt.Sprintf(dm.InvalidFooter, s.invalid))
		}
		if len(foot) > 0 {
			ui.Text(c, strings.Join(foot, " · ")).FontSize(t.FontSize-2).TextColor(t.TextMuted).
				Padding(t.Space(0.5), t.Space(1.5)).SingleLine()
		}
	})
}

// onDefinition opens a mention link on Cmd/Ctrl+click (or F12) without
// moving the selection, and leaves other definitions to the previous
// callback.
func (s *docMentionSession) onDefinition(offset int) {
	for _, ref := range s.links() {
		if offset >= ref.Start && offset < ref.End {
			s.app.docMentionOpenLink(s.tab, ref.Dest)
			return
		}
	}
	if s.prevDefinition != nil {
		s.prevDefinition(offset)
	}
}

// links returns the scanned request links of the editor, cached by buffer
// version so the stale strip and Cmd+click do not rescan every frame.
func (s *docMentionSession) links() []docmention.LinkRef {
	if s.ed == nil || s.ed.Buffer == nil {
		return nil
	}
	if version := s.ed.Buffer.Version(); s.refs != nil && s.refsVersion == version {
		return s.refs
	}
	s.refs = docmention.ScanRequestLinks(s.project(), s.ed.Buffer.String())
	s.refsVersion = s.ed.Buffer.Version()
	return s.refs
}

func splitMentionDetail(detail string) (method, rest string) {
	detail = strings.TrimSpace(detail)
	if detail == "" {
		return "", ""
	}
	if i := strings.IndexByte(detail, ' '); i > 0 {
		return detail[:i], strings.TrimSpace(detail[i+1:])
	}
	return detail, ""
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// docMentionOpenLink opens or focuses the request tab of a mention link. It
// reuses docgenOpenRequestLink, so an existing tab is focused rather than
// duplicated and nothing is sent.
func (a *App) docMentionOpenLink(tab *Tab, dest string) bool {
	if a == nil || tab == nil {
		return false
	}
	return a.docgenOpenRequestLink(tab, dest)
}

// docMentionHoverInfo is the read-only preview of a mention link: the
// current name, method and address, or a stale marker when the ID no longer
// resolves. The label written in the document stays untouched.
type docMentionHoverInfo struct {
	Title  string
	Detail string
	Method string
	Stale  bool
}

func (a *App) docMentionHover(tab *Tab, dest string) docMentionHoverInfo {
	dm := a.docMentionMsg()
	id, ok := docgen.ParseRefLink(dest)
	if !ok {
		return docMentionHoverInfo{Title: dm.InvalidRef, Detail: dest, Stale: true}
	}
	if tab == nil || tab.Owner == nil || tab.Owner.project == nil {
		return docMentionHoverInfo{Title: id, Detail: dest, Stale: true}
	}
	entry := tab.Owner.project.Requests[id]
	if entry == nil || entry.Request == nil {
		return docMentionHoverInfo{Title: fmt.Sprintf(dm.StaleMissing, id), Detail: id, Stale: true}
	}
	req := entry.Request
	info := docMentionHoverInfo{Title: req.Name}
	if req.HTTP != nil {
		info.Method = req.HTTP.Method
		info.Detail = strings.TrimSpace(req.HTTP.Method + " " + req.HTTP.URL)
	}
	if info.Detail == "" {
		info.Detail = id
	}
	return info
}
