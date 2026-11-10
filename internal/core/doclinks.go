package core

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"sort"
	"strings"

	"gorequest/internal/diagram"
	"gorequest/internal/markdown"
)

// This file resolves the destinations of document links and images and turns
// failures into positioned diagnostics. Resolution is purely local: no network
// access happens while opening, searching or previewing a document.

// LinkKind is how a resolved destination should be handled.
type LinkKind int

const (
	// LinkAnchor scrolls to a heading of the same document.
	LinkAnchor LinkKind = iota
	// LinkDoc opens another document of the same project.
	LinkDoc
	// LinkAsset points at a local file in the project.
	LinkAsset
	// LinkExternal is an http, https or mailto address that opens only when
	// the user clicks it.
	LinkExternal
	// LinkDocRef is a gorequest://request/<id> reference to a request of the
	// project (C03). The desktop layer owns what opening it does; core only
	// validates that the referenced request exists.
	LinkDocRef
)

func (k LinkKind) String() string {
	switch k {
	case LinkAnchor:
		return "anchor"
	case LinkDoc:
		return "document"
	case LinkAsset:
		return "asset"
	case LinkExternal:
		return "external"
	case LinkDocRef:
		return "request"
	}
	return "unknown"
}

// LinkTarget is a resolved document destination.
type LinkTarget struct {
	Kind LinkKind
	// Dest is the destination exactly as written in the Markdown.
	Dest string
	// RelPath is the project-relative path for LinkDoc and LinkAsset.
	RelPath string
	// Fragment is the decoded "#…" part, without the leading '#'.
	Fragment string
	// Slug is the GitHub heading slug the fragment names.
	Slug string
	// Line and Offset locate the named heading, 0 when there is none.
	Line   int
	Offset int
	// URL is the address for LinkExternal.
	URL string
}

// Link problem kinds, reported in LinkProblem.Kind so the UI can translate
// them and tests can assert the reason rather than a formatted sentence.
const (
	LinkProblemEmpty             = "empty"
	LinkProblemDangerousScheme   = "dangerous-scheme"
	LinkProblemUnsupportedScheme = "unsupported-scheme"
	LinkProblemAbsolutePath      = "absolute-path"
	LinkProblemOutsideProject    = "outside-project"
	LinkProblemBadEncoding       = "bad-encoding"
	LinkProblemMissingDocument   = "missing-document"
	LinkProblemMissingAsset      = "missing-asset"
	LinkProblemMissingAnchor     = "missing-anchor"
	LinkProblemUnsupportedAsset  = "unsupported-asset"
	LinkProblemNotFile           = "not-file"
	// LinkProblemMissingRequest marks a gorequest://request/<id> reference
	// whose request ID is not in the project (C03).
	LinkProblemMissingRequest = "missing-request"
)

// LinkProblem is a link that cannot be used safely.
type LinkProblem struct {
	Kind   string
	Dest   string
	Detail string
}

func (e *LinkProblem) Error() string {
	if e.Detail == "" {
		return e.Kind + ": " + e.Dest
	}
	return fmt.Sprintf("%s: %s (%s)", e.Kind, e.Detail, e.Dest)
}

// ProblemKind returns the LinkProblem kind of err, or "" for another error.
func ProblemKind(err error) string {
	if p, ok := err.(*LinkProblem); ok {
		return p.Kind
	}
	return ""
}

// ResolveDocLink resolves a destination written in docRel. Relative paths are
// confined to the project; symlink escapes are rejected by the store. Anchors
// are verified against the target document when it can be read.
func (p *Project) ResolveDocLink(docRel, dest string) (LinkTarget, error) {
	raw := strings.TrimSpace(dest)
	t := LinkTarget{Dest: raw}
	if raw == "" {
		return t, &LinkProblem{Kind: LinkProblemEmpty, Dest: raw}
	}
	if containsControl(raw) || strings.ContainsRune(raw, '\\') {
		return t, &LinkProblem{Kind: LinkProblemDangerousScheme, Dest: raw, Detail: "control characters or backslashes"}
	}
	if strings.HasPrefix(raw, "//") {
		return t, &LinkProblem{Kind: LinkProblemUnsupportedScheme, Dest: raw, Detail: "scheme-relative address"}
	}
	if scheme := urlScheme(raw); scheme != "" {
		switch scheme {
		case "http", "https", "mailto":
			t.Kind, t.URL = LinkExternal, raw
			return t, nil
		case "gorequest":
			// C03: gorequest://request/<id> references resolve against the
			// project's request map; the desktop owns the open action.
			return p.resolveRequestRef(raw)
		case "javascript", "data", "file", "vbscript", "blob":
			return t, &LinkProblem{Kind: LinkProblemDangerousScheme, Dest: raw, Detail: scheme + ": is not allowed"}
		default:
			return t, &LinkProblem{Kind: LinkProblemUnsupportedScheme, Dest: raw, Detail: scheme + ": is not supported"}
		}
	}

	pathPart, frag, _ := strings.Cut(raw, "#")
	pathPart = strings.TrimSpace(pathPart)
	if pathPart == "" {
		if frag == "" {
			return t, &LinkProblem{Kind: LinkProblemEmpty, Dest: raw}
		}
		return p.resolveDocAnchor(docRel, t, frag)
	}
	if strings.HasPrefix(pathPart, "/") || path.IsAbs(pathPart) {
		return t, &LinkProblem{Kind: LinkProblemAbsolutePath, Dest: raw, Detail: "use a path relative to this document"}
	}
	decoded, err := url.PathUnescape(pathPart)
	if err != nil {
		return t, &LinkProblem{Kind: LinkProblemBadEncoding, Dest: raw, Detail: err.Error()}
	}
	if containsControl(decoded) || strings.ContainsRune(decoded, '\\') {
		return t, &LinkProblem{Kind: LinkProblemDangerousScheme, Dest: raw, Detail: "control characters or backslashes"}
	}
	if strings.HasPrefix(decoded, "/") || path.IsAbs(decoded) {
		return t, &LinkProblem{Kind: LinkProblemAbsolutePath, Dest: raw, Detail: "use a path relative to this document"}
	}
	rel := path.Clean(path.Join(docDir(docRel), decoded))
	if rel == ".." || strings.HasPrefix(rel, "../") || rel == "." {
		return t, &LinkProblem{Kind: LinkProblemOutsideProject, Dest: raw}
	}
	t.RelPath = rel
	switch strings.ToLower(path.Ext(rel)) {
	case ".md":
		if p == nil || p.Store == nil || !p.Store.Exists(rel) {
			return t, &LinkProblem{Kind: LinkProblemMissingDocument, Dest: raw, Detail: rel}
		}
		t.Kind = LinkDoc
		if frag != "" {
			return p.applyAnchor(t, frag)
		}
		return t, nil
	default:
		if _, ok := docAssetTypes[strings.ToLower(path.Ext(rel))]; !ok {
			return t, &LinkProblem{Kind: LinkProblemUnsupportedAsset, Dest: raw, Detail: path.Ext(rel)}
		}
		if p == nil || p.Store == nil {
			return t, &LinkProblem{Kind: LinkProblemMissingAsset, Dest: raw, Detail: rel}
		}
		abs, err := p.Store.resolve(rel)
		if err != nil {
			return t, &LinkProblem{Kind: LinkProblemOutsideProject, Dest: raw, Detail: err.Error()}
		}
		fi, err := os.Lstat(abs)
		if err != nil {
			return t, &LinkProblem{Kind: LinkProblemMissingAsset, Dest: raw, Detail: rel}
		}
		if !fi.Mode().IsRegular() {
			return t, &LinkProblem{Kind: LinkProblemNotFile, Dest: raw, Detail: rel}
		}
		t.Kind = LinkAsset
		return t, nil
	}
}

// resolveRequestRef validates a gorequest:// destination. Only
// gorequest://request/<id> is a reference; a valid shape whose request ID is
// not in the project is a missing-request problem; any other gorequest path
// stays unsupported.
func (p *Project) resolveRequestRef(dest string) (LinkTarget, error) {
	t := LinkTarget{Dest: dest}
	rest, ok := strings.CutPrefix(dest, "gorequest://request/")
	if !ok {
		return t, &LinkProblem{Kind: LinkProblemUnsupportedScheme, Dest: dest, Detail: "only request/<id> references exist under gorequest:"}
	}
	id := strings.TrimSpace(rest)
	if id == "" || strings.ContainsAny(id, " \t\r\n/?#%\\") {
		return t, &LinkProblem{Kind: LinkProblemMissingRequest, Dest: dest, Detail: "reference is not gorequest://request/<id>"}
	}
	if p == nil || p.Requests == nil {
		return t, &LinkProblem{Kind: LinkProblemMissingRequest, Dest: dest, Detail: id}
	}
	if _, found := p.Requests[id]; !found {
		return t, &LinkProblem{Kind: LinkProblemMissingRequest, Dest: dest, Detail: id}
	}
	t.Kind = LinkDocRef
	return t, nil
}

func (p *Project) resolveDocAnchor(docRel string, t LinkTarget, frag string) (LinkTarget, error) {
	t.Kind = LinkAnchor
	t.RelPath = docRel
	if p == nil || p.Store == nil {
		return t, &LinkProblem{Kind: LinkProblemMissingAnchor, Dest: t.Dest, Detail: frag}
	}
	return p.applyAnchor(t, frag)
}

func (p *Project) applyAnchor(t LinkTarget, frag string) (LinkTarget, error) {
	decoded := decodeFragment(frag)
	t.Fragment = decoded
	h, ok, read := findDocHeading(p, t.RelPath, decoded)
	if !ok {
		if read {
			return t, &LinkProblem{Kind: LinkProblemMissingAnchor, Dest: t.Dest, Detail: decoded}
		}
		// The document exists but could not be read within the size limit;
		// keep the link usable and let the preview open at the top.
		return t, nil
	}
	t.Slug = h.Slug
	t.Line = h.Line
	t.Offset = h.Offset
	return t, nil
}

func findDocHeading(p *Project, rel, frag string) (Heading, bool, bool) {
	if rel == "" {
		return Heading{}, false, false
	}
	src, err := p.readDocLimited(rel, DefaultDocMaxBytes)
	if err != nil {
		return Heading{}, false, false
	}
	want := GitHubHeadingSlug(frag)
	if want == "" {
		want = strings.ToLower(strings.TrimSpace(frag))
	}
	for _, h := range ExtractHeadings(src) {
		if h.ID == want || h.Slug == want || strings.EqualFold(h.Slug, frag) {
			return h, true, true
		}
	}
	return Heading{}, false, true
}

// readDocLimited reads a document without exceeding limit bytes.
func (p *Project) readDocLimited(rel string, limit int64) (string, error) {
	if p == nil || p.Store == nil {
		return "", fmt.Errorf("no project")
	}
	abs, err := p.Store.resolve(rel)
	if err != nil {
		return "", err
	}
	f, err := os.Open(abs)
	if err != nil {
		return "", err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return "", err
	}
	if int64(len(b)) > limit {
		return "", fmt.Errorf("%s is larger than %d bytes", rel, limit)
	}
	return string(b), nil
}

// --- diagnostics ----------------------------------------------------------

// DiagnosticSeverity ranks a document diagnostic for display.
type DiagnosticSeverity string

const (
	SeverityError   DiagnosticSeverity = "error"
	SeverityWarning DiagnosticSeverity = "warning"
	SeverityInfo    DiagnosticSeverity = "info"
)

// DocDiagnostic is one positioned finding about a document.
type DocDiagnostic struct {
	RelPath  string
	Line     int
	Column   int
	Severity DiagnosticSeverity
	// Kind is a stable identifier the UI can translate.
	Kind string
	// Dest is the offending link or image destination, when there is one.
	Dest string
	// Message is an English technical description used as a fallback.
	Message string
}

// Diagnostic kinds produced by DiagnoseDoc.
const (
	DiagMissingLink     = "missing-link"
	DiagMissingAsset    = "missing-asset"
	DiagDangerousScheme = "dangerous-scheme"
	DiagRemoteImage     = "remote-image"
	DiagHTMLNotExecuted = "html-not-executed"
	DiagMermaid         = "mermaid-unsupported"
	DiagBadLink         = "bad-link"
	// DiagMissingRequest marks a gorequest reference to a request that is
	// not in the project (C03).
	DiagMissingRequest = "missing-request"
)

// DiagnoseDoc checks every link and image of src and reports local problems:
// missing relative documents or attachments, missing anchors, dangerous
// schemes, remote images (never downloaded), raw HTML (shown as text) and
// Mermaid code (source kept, not rendered). Results are sorted by position.
func (p *Project) DiagnoseDoc(docRel, src string) []DocDiagnostic {
	d := markdown.Parse(src)
	var out []DocDiagnostic
	add := func(off int, sev DiagnosticSeverity, kind, dest, msg string) {
		if len(out) >= 500 {
			return
		}
		line, col, _ := lineColumnAt(src, off)
		out = append(out, DocDiagnostic{
			RelPath: docRel, Line: line, Column: col,
			Severity: sev, Kind: kind, Dest: dest, Message: msg,
		})
	}
	for i, b := range d.Blocks() {
		switch b.Kind {
		case markdown.HTMLBlock:
			add(b.Start, SeverityInfo, DiagHTMLNotExecuted, "", "raw HTML is shown as source text and is never executed")
		case markdown.CodeBlock:
			if strings.EqualFold(b.Language(), "mermaid") {
				body := fencedCodeBody(src, b)
				if _, d := diagram.Render(context.Background(), body, diagram.Options{}); d != nil {
					// Report the line in document coordinates. The diagram
					// parser knows the line within the fence.
					line, _, _ := lineColumnAt(src, b.Start)
					out = append(out, DocDiagnostic{
						RelPath: docRel, Line: line + d.Line, Column: d.Column,
						Severity: SeverityWarning, Kind: DiagMermaid,
						Message: "Mermaid diagram is not rendered: " + d.Message + "; the source is kept",
					})
				}
			}
		}
		for _, in := range d.Inlines(i) {
			switch in.Kind {
			case markdown.HTML:
				lower := strings.ToLower(in.Text)
				for _, tag := range []string{"<script", "<iframe", "<object", "<embed", "<style"} {
					if strings.Contains(lower, tag) {
						add(in.Start, SeverityWarning, DiagHTMLNotExecuted, "", "raw HTML is shown as text; scripts and embedded frames never run")
						break
					}
				}
			case markdown.Link, markdown.Image:
				if in.Dest == "" {
					continue
				}
				t, err := p.ResolveDocLink(docRel, in.Dest)
				if err != nil {
					kind := DiagBadLink
					sev := SeverityError
					switch ProblemKind(err) {
					case LinkProblemMissingAsset, LinkProblemMissingDocument:
						kind = DiagMissingAsset
						if in.Kind == markdown.Link && strings.HasSuffix(strings.ToLower(strings.SplitN(in.Dest, "#", 2)[0]), ".md") {
							kind = DiagMissingLink
						}
					case LinkProblemDangerousScheme:
						kind = DiagDangerousScheme
					case LinkProblemMissingAnchor:
						kind = DiagMissingLink
					case LinkProblemMissingRequest:
						kind = DiagMissingRequest
					}
					add(in.Start, sev, kind, in.Dest, err.Error())
					continue
				}
				if in.Kind == markdown.Image && t.Kind == LinkExternal {
					add(in.Start, SeverityWarning, DiagRemoteImage, in.Dest, "remote images are not downloaded when a document opens")
				}
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Line == out[j].Line {
			return out[i].Column < out[j].Column
		}
		return out[i].Line < out[j].Line
	})
	return out
}

// fencedCodeBody extracts a fenced block's content. The project parser ends
// a fenced block at its last content line, so the closing fence is optional.
func fencedCodeBody(src string, b markdown.Block) string {
	raw := src[b.Start:b.End]
	lines := strings.Split(strings.TrimRight(raw, "\n"), "\n")
	if len(lines) > 0 {
		lines = lines[1:]
	}
	if len(lines) > 0 {
		last := strings.TrimSpace(lines[len(lines)-1])
		if strings.HasPrefix(last, "```") || strings.HasPrefix(last, "~~~") {
			lines = lines[:len(lines)-1]
		}
	}
	return strings.Join(lines, "\n")
}

// --- destination helpers --------------------------------------------------

func containsControl(s string) bool {
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}

// urlScheme returns a lower-case URI scheme when s begins with one, else "".
func urlScheme(s string) string {
	i := strings.IndexByte(s, ':')
	if i <= 0 {
		return ""
	}
	for j := 0; j < i; j++ {
		c := s[j]
		if j == 0 && !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z') {
			return ""
		}
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '+' || c == '-' || c == '.') {
			return ""
		}
	}
	return strings.ToLower(s[:i])
}

func decodeFragment(frag string) string {
	if f, err := url.PathUnescape(frag); err == nil {
		return f
	}
	return frag
}
