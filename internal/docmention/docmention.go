// Package docmention resolves the @ interface references of Markdown
// documents: the project's request catalogue, the completion query, the
// canonical gorequest:// link form, and the source spans of the references
// already written.
//
// It is read-only. Nothing here sends a request, resolves an environment or
// reads a secret; the catalogue is the in-memory project snapshot. The URI
// identity is always the request ID, never the display path, so a request
// that is renamed or moved stays reachable and the label written in the
// document remains a snapshot.
package docmention

import (
	"path"
	"sort"
	"strings"

	"gorequest/internal/core"
	"gorequest/internal/docgen"
)

// Request is one interface of the owner project, as the @ completion shows
// it. Folder is the display path of the collection ("auth", "auth/users"),
// empty for a request at the root of collections/.
type Request struct {
	ID      string
	Name    string
	Method  string
	URL     string
	Folder  string
	RelPath string
	// Valid reports whether the ID can be carried by a gorequest://request/
	// URI under docgen's grammar. An invalid ID is still a usable request in
	// GoRequest; it is only unreachable from documents, and the completion
	// says so instead of rewriting it.
	Valid bool
}

// Label is the visible @ form: @folder/name, or @name for a root request.
// It is a display snapshot; the link destination is the ID.
func (r Request) Label() string {
	name := strings.TrimSpace(r.Name)
	if name == "" {
		name = r.ID
	}
	if r.Folder != "" {
		return "@" + r.Folder + "/" + name
	}
	return "@" + name
}

// Detail is the method and address shown beside the label. It is definition
// text only: variables are not resolved and no environment is read.
func (r Request) Detail() string {
	return strings.TrimSpace(strings.TrimSpace(r.Method) + " " + strings.TrimSpace(r.URL))
}

// Link builds the standard Markdown link for the request:
//
//	[@auth/用户登录](gorequest://request/auth.login)
//
// It returns false for an ID the existing URI grammar cannot carry, so the
// caller can report the limitation rather than invent another identity.
func (r Request) Link() (string, bool) {
	if !docgen.ValidRequestID(r.ID) {
		return "", false
	}
	return docgen.FormatRefLink(r.ID, EscapeLabel(r.Label())), true
}

// labelEscaper makes a display name safe inside Markdown link text. The
// destination is the ID and needs no escaping; docgen.FormatRefLink escapes
// table pipes itself.
var labelEscaper = strings.NewReplacer(
	`\`, `\\`,
	`[`, `\[`,
	`]`, `\]`,
	`*`, `\*`,
	`_`, `\_`,
	"`", "\\`",
	`<`, `\<`,
	`>`, `\>`,
	"\r", " ",
	"\n", " ",
)

// EscapeLabel escapes a label for Markdown link text. The visible text is
// unchanged once Markdown is rendered.
func EscapeLabel(s string) string {
	return labelEscaper.Replace(strings.TrimSpace(s))
}

// UnescapeLabel reverses the Markdown backslash escapes of a label, so a
// chip or a repair button shows the text the author sees rather than the
// source spelling.
func UnescapeLabel(s string) string {
	var b strings.Builder
	escaped := false
	for _, r := range s {
		switch {
		case escaped:
			b.WriteRune(r)
			escaped = false
		case r == '\\':
			escaped = true
		default:
			b.WriteRune(r)
		}
	}
	if escaped {
		b.WriteByte('\\')
	}
	return b.String()
}

// Collect returns the owner project's requests in a stable order. The
// project snapshot is the only source: no file is read and no network call
// is made.
func Collect(p *core.Project) []Request {
	if p == nil {
		return nil
	}
	folders := folderDisplayPaths(p.Collections)
	out := make([]Request, 0, len(p.Requests))
	for id, entry := range p.Requests {
		if entry == nil || entry.Request == nil {
			continue
		}
		r := Request{
			ID:      id,
			Name:    entry.Request.Name,
			RelPath: entry.RelPath,
			Valid:   docgen.ValidRequestID(id),
		}
		if http := entry.Request.HTTP; http != nil {
			r.Method = http.Method
			r.URL = http.URL
		}
		dir := path.Dir(entry.RelPath)
		if dir != "collections" && dir != "." && dir != "" {
			if folder, ok := folders[dir]; ok {
				r.Folder = folder
			} else {
				r.Folder = fallbackFolder(dir)
			}
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return lessRequest(out[i], out[j]) })
	return out
}

// folderDisplayPaths maps a collection directory to its display path, using
// the collection names the tree already resolved (collection.yaml's name,
// else the directory name).
func folderDisplayPaths(roots []*core.CollectionEntry) map[string]string {
	out := map[string]string{}
	var walk func(n *core.CollectionEntry, parent string)
	walk = func(n *core.CollectionEntry, parent string) {
		if n == nil {
			return
		}
		name := strings.TrimSpace(n.Name)
		if name == "" {
			name = path.Base(n.RelPath)
		}
		full := name
		if parent != "" {
			full = parent + "/" + name
		}
		out[n.RelPath] = full
		for _, child := range n.Children {
			walk(child, full)
		}
	}
	for _, root := range roots {
		walk(root, "")
	}
	return out
}

func fallbackFolder(dir string) string {
	dir = strings.TrimPrefix(path.Clean(dir), "collections/")
	if dir == "." || dir == "" {
		return ""
	}
	return dir
}

func lessRequest(a, b Request) bool {
	if a.Folder != b.Folder {
		return a.Folder < b.Folder
	}
	if a.Name != b.Name {
		return a.Name < b.Name
	}
	return a.ID < b.ID
}

// Match is one search result. Score orders the list; the request fields are
// what the completion renders.
type Match struct {
	Request
	Score int
}

// Search matches query against the folder, name, ID, method and address of
// every request. The whole catalogue is searched; the caller pages or limits
// only what it draws, so nothing becomes unreachable behind a fixed first
// page. An empty query returns the catalogue in stable order.
//
// Matching is case-insensitive, works on Chinese and other non-ASCII names,
// and falls back to a subsequence match so "ulg" still finds "用户登录".
func Search(reqs []Request, query string) []Match {
	q := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(query), "@")))
	out := make([]Match, 0, len(reqs))
	for _, r := range reqs {
		if score, ok := scoreRequest(r, q); ok {
			out = append(out, Match{Request: r, Score: score})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return lessRequest(out[i].Request, out[j].Request)
	})
	return out
}

// scoreRequest returns the best score of query against the request's fields,
// and false when none matches.
func scoreRequest(r Request, q string) (int, bool) {
	if q == "" {
		return 0, true
	}
	best := -1
	add := func(base int, field string, fuzzy bool) {
		if field == "" {
			return
		}
		if score := scoreField(strings.ToLower(field), q, fuzzy); score >= 0 {
			if v := base + score; v > best {
				best = v
			}
		}
	}
	// Subsequence matching is only meaningful for a name; on an ID, an
	// address or a folder it would make a short query match almost every
	// request.
	add(500, r.ID, false)
	add(450, r.Name, true)
	add(400, r.Label(), false)
	add(300, r.Folder, false)
	add(250, r.Method, false)
	add(200, r.URL, false)
	if best < 0 {
		return 0, false
	}
	return best, true
}

// scoreField scores one field: exact match, then prefix, then substring,
// then, when fuzzy is set, subsequence. -1 means no match.
func scoreField(field, q string, fuzzy bool) int {
	switch {
	case field == q:
		return 400
	case strings.HasPrefix(field, q):
		return 300 - min(len(field)-len(q), 50)
	}
	if i := strings.Index(field, q); i >= 0 {
		return 200 - min(i, 100)
	}
	if fuzzy {
		if score, ok := subsequenceScore(field, q); ok {
			return 100 + score
		}
	}
	return -1
}

// subsequenceScore reports whether q's runes appear in order in text, and
// scores how tightly they are packed.
func subsequenceScore(text, q string) (int, bool) {
	tr, qr := []rune(text), []rune(q)
	ti, last, score := 0, -1, 0
	for _, want := range qr {
		found := false
		for ti < len(tr) {
			if tr[ti] == want {
				if last >= 0 {
					score -= min(ti-last-1, 20)
				}
				last, ti, found = ti, ti+1, true
				break
			}
			ti++
		}
		if !found {
			return 0, false
		}
	}
	return score, true
}
