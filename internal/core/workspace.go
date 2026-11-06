package core

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// This file owns the B02 workspace primitives that are pure data and filesystem
// work, so the desktop layer only renders and dispatches:
//
//   - creating a project at a user-chosen location without overwriting anything;
//   - locating and scaffolding the global (personal) workspace in the OS
//     application-data directory, separate from every Git-tracked project;
//   - searching requests by name, method, URL and body text.
//
// Cross-workspace transfer lives in workspace_transfer.go.

// GlobalWorkspaceDirEnv overrides the global workspace location. Tests (and a
// user who wants the scratch area on another disk) set it; the product default
// is the per-user application-data directory.
const GlobalWorkspaceDirEnv = "GOREQUEST_GLOBAL_DIR"

// globalWorkspaceName is the display name of a freshly scaffolded personal
// workspace. It is user data, not an interface label, so it stays stable.
const globalWorkspaceName = "Global Workspace"

// GlobalWorkspaceDir returns the directory of the personal workspace. It is
// always outside any opened project, so scratch requests and personal
// collections never land in a Git-tracked tree.
func GlobalWorkspaceDir() (string, error) {
	if v := strings.TrimSpace(os.Getenv(GlobalWorkspaceDirEnv)); v != "" {
		abs, err := filepath.Abs(v)
		if err != nil {
			return "", fmt.Errorf("global workspace path: %w", err)
		}
		return filepath.Clean(abs), nil
	}
	base, err := os.UserConfigDir()
	if err != nil || strings.TrimSpace(base) == "" {
		return "", fmt.Errorf("global workspace: the platform has no application-data directory")
	}
	return filepath.Join(base, "GoRequest", "workspace"), nil
}

// EnsureGlobalWorkspace opens the global workspace, creating its minimal
// project layout on first use. It never performs network access and never
// overwrites an existing project file.
func EnsureGlobalWorkspace(dir string) (*Project, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil, configErrorf("global workspace directory is empty")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return nil, fmt.Errorf("creating global workspace: %w", err)
	}
	if _, err := os.Stat(filepath.Join(abs, ".gorequest", "project.yaml")); err == nil {
		return OpenProject(abs)
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("global workspace: %w", err)
	}
	return NewProject(abs, globalWorkspaceName)
}

// OpenGlobalWorkspace resolves and opens the personal workspace.
func OpenGlobalWorkspace() (*Project, error) {
	dir, err := GlobalWorkspaceDir()
	if err != nil {
		return nil, err
	}
	return EnsureGlobalWorkspace(dir)
}

// CreateProjectAt creates a new project directory under parent and scaffolds
// .gorequest inside it, then returns the project root.
//
// The target directory is <parent>/<slug(name)>. It must be absent or empty, and
// an existing .gorequest is never overwritten, so pointing the wizard at a
// folder with unrelated work cannot damage it. A failure cleans up the new
// directory only when this call created it.
func CreateProjectAt(parent, name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", configErrorf("project name is empty")
	}
	folder, err := projectFolderName(name)
	if err != nil {
		return "", err
	}
	parent = strings.TrimSpace(parent)
	if parent == "" {
		return "", configErrorf("project location is empty")
	}
	absParent, err := filepath.Abs(parent)
	if err != nil {
		return "", err
	}
	st, err := os.Stat(absParent)
	if err != nil {
		return "", fmt.Errorf("project location: %w", err)
	}
	if !st.IsDir() {
		return "", configErrorf("project location %q is not a folder", parent)
	}
	target := filepath.Join(absParent, folder)

	created := false
	if st, err := os.Stat(target); err == nil {
		if !st.IsDir() {
			return "", configErrorf("target %q is not a folder", target)
		}
		entries, err := os.ReadDir(target)
		if err != nil {
			return "", fmt.Errorf("reading target folder: %w", err)
		}
		if len(entries) > 0 {
			return "", configErrorf("target folder %q is not empty; choose another location or project name", target)
		}
	} else if os.IsNotExist(err) {
		if err := os.MkdirAll(target, 0o755); err != nil {
			return "", fmt.Errorf("creating project folder: %w", err)
		}
		created = true
	} else {
		return "", fmt.Errorf("checking target folder: %w", err)
	}

	p, err := NewProject(target, name)
	if err != nil {
		if created {
			_ = os.RemoveAll(target)
		} else {
			// The folder was empty before this call, so the only content this
			// call could have added is .gorequest/.
			_ = os.RemoveAll(filepath.Join(target, ".gorequest"))
		}
		return "", err
	}
	return p.DirParent(), nil
}

// projectFolderName picks the directory name for a new project. ASCII names use
// the same portable slug as request/folder IDs; names with non-ASCII characters
// (for example Chinese) keep a readable folder name instead of collapsing to
// "item", with characters that are illegal on common filesystems replaced.
func projectFolderName(name string) (string, error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" || trimmed == "." || trimmed == ".." ||
		filepath.IsAbs(trimmed) || strings.ContainsAny(trimmed, `/\`) {
		return "", configErrorf("invalid project name %q", name)
	}
	ascii := true
	for _, r := range trimmed {
		if r > 127 {
			ascii = false
			break
		}
	}
	if ascii {
		return Slug(trimmed), nil
	}
	var b strings.Builder
	for _, r := range trimmed {
		if r < 0x20 || strings.ContainsRune(`:*?"<>|`, r) {
			b.WriteByte('-')
			continue
		}
		b.WriteRune(r)
	}
	folder := strings.Trim(b.String(), " .-")
	if folder == "" || folder == "." || folder == ".." {
		return "", configErrorf("invalid project name %q", name)
	}
	return folder, nil
}

// DirParent returns the directory that contains .gorequest/, i.e. the project
// root the user opened.
func (p *Project) DirParent() string {
	if p == nil || p.Dir == "" {
		return ""
	}
	return filepath.Dir(p.Dir)
}

// HasCollection reports whether the project has the given collections-relative
// folder. The empty path (the collections root) always exists. Transfers use it
// to keep a request's folder when the destination has the same structure.
func (p *Project) HasCollection(collectionRel string) bool {
	if p == nil || p.Store == nil {
		return false
	}
	rel, err := sanitizeCollectionRel(collectionRel)
	if err != nil {
		return false
	}
	if rel == "" {
		return true
	}
	return p.Store.Exists(collectionsDirOf(rel))
}

// SearchHit is one match of the in-project search. Field names the part of the
// request or document that matched so the UI can explain a hit.
type SearchHit struct {
	Kind    string // "request" or "doc"
	ID      string
	RelPath string
	Name    string
	Method  string
	URL     string
	Field   string // "name", "method", "url", "body", "doc"
}

// MatchRequest reports whether a request matches query by display name, HTTP
// method, URL or body text, case-insensitively. An empty query matches nothing;
// callers that mean "no filter" skip the search entirely.
func MatchRequest(entry *RequestEntry, query string) bool {
	return matchRequestField(entry, query) != ""
}

// matchRequestField returns which field matched ("" for no match), preferring
// the more specific body match only after the identifying fields.
func matchRequestField(entry *RequestEntry, query string) string {
	q := strings.TrimSpace(query)
	if entry == nil || entry.Request == nil || q == "" {
		return ""
	}
	req := entry.Request
	if containsFold(req.Name, q) {
		return "name"
	}
	if req.HTTP == nil {
		return ""
	}
	if containsFold(req.HTTP.Method, q) {
		return "method"
	}
	if containsFold(req.HTTP.URL, q) {
		return "url"
	}
	if containsFold(BodyEditText(req.HTTP.Body), q) {
		return "body"
	}
	return ""
}

// MatchDoc reports whether a document's name matches query.
func MatchDoc(d *DocEntry, query string) bool {
	if d == nil {
		return false
	}
	return containsFold(d.Name, query)
}

// SearchProject returns request and document hits for query, deterministically
// ordered by kind and path. It is used by callers that need a ranked result
// list; the sidebar filter uses MatchRequest/MatchDoc directly.
func SearchProject(p *Project, query string) []SearchHit {
	q := strings.TrimSpace(query)
	if p == nil || q == "" {
		return nil
	}
	var hits []SearchHit
	for _, e := range p.Requests {
		if field := matchRequestField(e, q); field != "" {
			hit := SearchHit{Kind: "request", ID: e.Request.ID, RelPath: e.RelPath,
				Name: e.Request.Name, Field: field}
			if e.Request.HTTP != nil {
				hit.Method = e.Request.HTTP.Method
				hit.URL = e.Request.HTTP.URL
			}
			hits = append(hits, hit)
		}
	}
	for _, d := range p.Docs {
		if MatchDoc(d, q) {
			hits = append(hits, SearchHit{Kind: "doc", RelPath: d.RelPath, Name: d.Name, Field: "doc"})
		}
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].Kind != hits[j].Kind {
			return hits[i].Kind < hits[j].Kind
		}
		if hits[i].Name != hits[j].Name {
			return hits[i].Name < hits[j].Name
		}
		return hits[i].RelPath < hits[j].RelPath
	})
	return hits
}

func containsFold(haystack, needle string) bool {
	return strings.Contains(strings.ToLower(haystack), strings.ToLower(needle))
}

var templateVarPattern = regexp.MustCompile(`\{\{\s*([A-Za-z0-9_][A-Za-z0-9_.-]*)\s*\}\}`)

// templateVariableRefs returns every {{name}} reference in the given texts,
// excluding the secret namespace. It is used to warn that a transferred request
// still points at variables the destination does not define; the environment on
// the destination may still supply them, so this is a preview warning, not a
// hard failure.
func templateVariableRefs(texts ...string) []string {
	seen := map[string]bool{}
	for _, text := range texts {
		for _, m := range templateVarPattern.FindAllStringSubmatch(text, -1) {
			name := m[1]
			if strings.HasPrefix(name, "secret.") {
				continue
			}
			seen[name] = true
		}
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// secretRefs returns every {{secret.name}} reference in the given texts, sorted
// and unique. Only names are returned; values are never read.
func secretRefs(texts ...string) []string {
	seen := map[string]bool{}
	for _, text := range texts {
		for _, m := range templateVarPattern.FindAllStringSubmatch(text, -1) {
			name := m[1]
			if strings.HasPrefix(name, "secret.") {
				seen[strings.TrimPrefix(name, "secret.")] = true
			}
		}
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// requestTexts collects the searchable and reference-bearing strings of a
// request: identifying fields, parameters, headers and body text. It does not
// include project or environment values.
func requestTexts(req *Request) []string {
	if req == nil {
		return nil
	}
	texts := []string{req.Name}
	if req.HTTP == nil {
		return texts
	}
	texts = append(texts, req.HTTP.Method, req.HTTP.URL)
	for _, kv := range req.HTTP.Query {
		texts = append(texts, kv.Name, kv.Value)
	}
	for _, kv := range req.HTTP.Headers {
		texts = append(texts, kv.Name, kv.Value)
	}
	if body := BodyEditText(req.HTTP.Body); body != "" {
		texts = append(texts, body)
	}
	if req.HTTP.Body != nil {
		texts = append(texts, req.HTTP.Body.File)
	}
	for name, value := range req.Variables {
		texts = append(texts, name, value)
	}
	return texts
}
