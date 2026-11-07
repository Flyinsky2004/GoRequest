package docedit

import (
	"strings"
	"unicode"
)

// SlashCommand is one entry of the canvas slash menu.
type SlashCommand struct {
	// ID is stable and never shown.
	ID string
	// Label is the Chinese label, LabelEN the English one.
	Label, LabelEN string
	// Keywords are extra search terms, in both languages.
	Keywords []string
	// Kind selects how Apply builds the block.
	Kind string
	// Level is the heading level for heading kinds.
	Level int
}

// Slash command kinds.
const (
	SlashParagraph = "paragraph"
	SlashHeading   = "heading"
	SlashBullet    = "bullet"
	SlashOrdered   = "ordered"
	SlashTask      = "task"
	SlashQuote     = "quote"
	SlashCode      = "code"
	SlashDivider   = "divider"
	SlashTable     = "table"
	SlashLink      = "link"
	SlashImage     = "image"
	SlashRequest   = "request"
)

// slashCommands is the first-release menu, in display order.
var slashCommands = []SlashCommand{
	{ID: "paragraph", Label: "正文", LabelEN: "Text", Keywords: []string{"paragraph", "text", "正文", "文本"}, Kind: SlashParagraph},
	{ID: "h1", Label: "标题 1", LabelEN: "Heading 1", Keywords: []string{"h1", "title", "标题"}, Kind: SlashHeading, Level: 1},
	{ID: "h2", Label: "标题 2", LabelEN: "Heading 2", Keywords: []string{"h2", "title", "标题"}, Kind: SlashHeading, Level: 2},
	{ID: "h3", Label: "标题 3", LabelEN: "Heading 3", Keywords: []string{"h3", "title", "标题"}, Kind: SlashHeading, Level: 3},
	{ID: "h4", Label: "标题 4", LabelEN: "Heading 4", Keywords: []string{"h4", "title", "标题"}, Kind: SlashHeading, Level: 4},
	{ID: "h5", Label: "标题 5", LabelEN: "Heading 5", Keywords: []string{"h5", "title", "标题"}, Kind: SlashHeading, Level: 5},
	{ID: "h6", Label: "标题 6", LabelEN: "Heading 6", Keywords: []string{"h6", "title", "标题"}, Kind: SlashHeading, Level: 6},
	{ID: "bullet", Label: "无序列表", LabelEN: "Bulleted list", Keywords: []string{"bullet", "list", "ul", "无序", "列表"}, Kind: SlashBullet},
	{ID: "ordered", Label: "有序列表", LabelEN: "Numbered list", Keywords: []string{"ordered", "number", "ol", "有序", "编号"}, Kind: SlashOrdered},
	{ID: "task", Label: "任务列表", LabelEN: "To-do list", Keywords: []string{"task", "todo", "check", "任务", "待办"}, Kind: SlashTask},
	{ID: "quote", Label: "引用", LabelEN: "Quote", Keywords: []string{"quote", "blockquote", "引用"}, Kind: SlashQuote},
	{ID: "code", Label: "代码块", LabelEN: "Code block", Keywords: []string{"code", "fence", "代码", "代码块"}, Kind: SlashCode},
	{ID: "divider", Label: "分割线", LabelEN: "Divider", Keywords: []string{"divider", "rule", "hr", "分割线"}, Kind: SlashDivider},
	{ID: "table", Label: "表格", LabelEN: "Table", Keywords: []string{"table", "grid", "表格"}, Kind: SlashTable},
	{ID: "link", Label: "链接", LabelEN: "Link", Keywords: []string{"link", "url", "链接"}, Kind: SlashLink},
	{ID: "image", Label: "图片", LabelEN: "Image", Keywords: []string{"image", "picture", "图片"}, Kind: SlashImage},
	{ID: "request", Label: "接口引用", LabelEN: "Request reference", Keywords: []string{"request", "api", "接口", "引用"}, Kind: SlashRequest},
}

// SlashCommands returns the menu entries in display order.
func SlashCommands() []SlashCommand { return slashCommands }

// FindSlashCommand returns the command whose ID is word, case-insensitively.
func FindSlashCommand(word string) (SlashCommand, bool) {
	for _, cmd := range slashCommands {
		if strings.EqualFold(cmd.ID, word) {
			return cmd, true
		}
	}
	return SlashCommand{}, false
}

// FilterSlash returns the commands matching query. Every whitespace-separated
// term must match the label in either language or a keyword, case-insensitively,
// so "head 2" and "标题 2" both find Heading 2.
func FilterSlash(query string) []SlashCommand {
	terms := strings.Fields(strings.ToLower(strings.TrimSpace(query)))
	if len(terms) == 0 {
		return slashCommands
	}
	out := make([]SlashCommand, 0, len(slashCommands))
	for _, cmd := range slashCommands {
		hay := strings.ToLower(cmd.Label + " " + cmd.LabelEN + " " + strings.Join(cmd.Keywords, " "))
		ok := true
		for _, t := range terms {
			if !strings.Contains(hay, t) {
				ok = false
				break
			}
		}
		if ok {
			out = append(out, cmd)
		}
	}
	return out
}

// SlashQuery reports the slash command query at the start of a block's text.
//
// It triggers only when the block is a single line whose first non-space
// content is "/" and the rest is a command query: empty, or made of letters,
// digits, spaces and CJK. A path such as "/usr/local" (a second slash), a URL
// or a dotted name never opens the menu. A list item's marker is accepted so
// "/h1" typed as the only item of a list converts the item.
func SlashQuery(text string) (query string, ok bool) {
	line, rest, hasMore := strings.Cut(text, "\n")
	if hasMore && strings.TrimSpace(rest) != "" {
		return "", false
	}
	line = strings.TrimSpace(line)
	line = trimListMarker(line)
	if !strings.HasPrefix(line, "/") {
		return "", false
	}
	query = strings.TrimSpace(line[1:])
	for _, r := range query {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r), unicode.IsSpace(r):
		case r == '-' || r == '_':
		default:
			return "", false
		}
	}
	return query, true
}

// trimListMarker removes one bullet or ordered-list marker from the start of
// line, with the spaces after it.
func trimListMarker(line string) string {
	if len(line) >= 2 && (line[0] == '-' || line[0] == '*' || line[0] == '+') && (line[1] == ' ' || line[1] == '\t') {
		return strings.TrimLeft(line[2:], " \t")
	}
	i := 0
	for i < len(line) && line[i] >= '0' && line[i] <= '9' {
		i++
	}
	if i > 0 && i < len(line) && (line[i] == '.' || line[i] == ')') {
		if i+1 < len(line) && (line[i+1] == ' ' || line[i+1] == '\t') {
			return strings.TrimLeft(line[i+2:], " \t")
		}
	}
	return line
}

// SlashResult is the block text a slash command makes, and the selection the
// canvas puts in the in-place editor afterwards.
type SlashResult struct {
	Text     string
	SelStart int
	SelEnd   int
}

// ApplySlash builds the replacement block text for cmd. rest is the text the
// user typed after the command word, if any; it is kept where the command has
// a natural place for it.
func ApplySlash(cmd SlashCommand, rest string) SlashResult {
	rest = strings.TrimSpace(rest)
	switch cmd.Kind {
	case SlashHeading:
		prefix := strings.Repeat("#", max(1, min(cmd.Level, 6))) + " "
		return SlashResult{Text: prefix + rest, SelStart: len(prefix), SelEnd: len(prefix) + len(rest)}
	case SlashBullet:
		return SlashResult{Text: "- " + rest, SelStart: 2, SelEnd: 2 + len(rest)}
	case SlashOrdered:
		return SlashResult{Text: "1. " + rest, SelStart: 3, SelEnd: 3 + len(rest)}
	case SlashTask:
		return SlashResult{Text: "- [ ] " + rest, SelStart: 6, SelEnd: 6 + len(rest)}
	case SlashQuote:
		return SlashResult{Text: "> " + rest, SelStart: 2, SelEnd: 2 + len(rest)}
	case SlashCode:
		lang := ""
		if rest != "" && !strings.ContainsAny(rest, " \t\n") {
			lang = rest
			rest = ""
		}
		text := "```" + lang + "\n" + rest + "\n```"
		at := len("```" + lang + "\n")
		return SlashResult{Text: text, SelStart: at, SelEnd: at + len(rest)}
	case SlashDivider:
		if rest == "" {
			return SlashResult{Text: "---", SelStart: 3, SelEnd: 3}
		}
		text := "---\n\n" + rest
		return SlashResult{Text: text, SelStart: len(text), SelEnd: len(text)}
	case SlashTable:
		text := "| Column | Column |\n| --- | --- |\n| Cell | Cell |\n| Cell | Cell |"
		if rest != "" {
			text += "\n\n" + rest
		}
		// Caret in the first header cell.
		return SlashResult{Text: text, SelStart: 2, SelEnd: 8}
	case SlashLink:
		label := rest
		if label == "" {
			label = "text"
		}
		text := "[" + label + "](url)"
		at := len(label) + 3
		return SlashResult{Text: text, SelStart: at, SelEnd: at + 3}
	case SlashImage:
		alt := rest
		if alt == "" {
			alt = "alt"
		}
		text := "![" + alt + "](path)"
		at := len(alt) + 4
		return SlashResult{Text: text, SelStart: at, SelEnd: at + 4}
	default:
		return SlashResult{Text: rest, SelStart: 0, SelEnd: len(rest)}
	}
}

// SlashRest returns the text after the command word in a slash query, so
// "/h1 Hello" keeps "Hello" when it becomes a heading. The command word is
// the first whitespace-separated token; a query that matches a command by a
// keyword rather than its ID leaves the whole query as rest.
func SlashRest(query string, cmd SlashCommand) string {
	query = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(query), "/"))
	if query == "" {
		return ""
	}
	// The whole query naming the command leaves no content.
	if strings.EqualFold(query, cmd.Label) || strings.EqualFold(query, cmd.LabelEN) {
		return ""
	}
	for _, k := range cmd.Keywords {
		if strings.EqualFold(query, k) {
			return ""
		}
	}
	word, rest, ok := strings.Cut(query, " ")
	if !ok {
		return query
	}
	if strings.EqualFold(word, cmd.ID) || strings.EqualFold(word, cmd.Label) || strings.EqualFold(word, cmd.LabelEN) {
		return strings.TrimSpace(rest)
	}
	for _, k := range cmd.Keywords {
		if strings.EqualFold(word, k) {
			return strings.TrimSpace(rest)
		}
	}
	return query
}
