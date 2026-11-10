package docgen

import (
	"context"
	"fmt"
	"html"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"gorequest/internal/core"
	"gorequest/internal/diagram"
	"gorequest/internal/markdown"
)

// ExportFormat selects the artifact layout.
type ExportFormat string

const (
	// ExportMarkdown writes one expanded .md per document plus an index.
	ExportMarkdown ExportFormat = "markdown"
	// ExportHTML writes one static .html per document plus an index page.
	ExportHTML ExportFormat = "html"
)

// GeneratedFileMarker is the first line of every file docgen writes; a file
// without it is a manual file and is never overwritten, whatever the
// overwrite flag says.
const GeneratedFileMarker = "<!-- gorequest:generated -->"

// DefaultExportLimits bound an export set.
const (
	DefaultMaxExportFiles = 200
	DefaultMaxExportBytes = 64 << 20 // 64 MiB
	snippetBytes          = 2048
)

// ExportOptions configure one export.
type ExportOptions struct {
	Format ExportFormat
	// Docs lists project-relative documents to export; empty means all.
	Docs []string
	// ContentOverrides supplies in-memory document content keyed by project
	// rel path (unsaved editor state); disk content is used otherwise.
	ContentOverrides map[string]string
	// TargetDir is an absolute directory outside the project store.
	TargetDir string
	// IncludeAssets copies referenced local attachments into the set.
	IncludeAssets bool
	// AllowOverwrite permits replacing previous docgen output (files that
	// carry the generated marker). Manual files are never replaced.
	AllowOverwrite bool
	// Labels select the card label language.
	Labels CardLabels
	// MermaidNote overrides the default HTML Mermaid warning.
	MermaidNote string
	// Diagram configures the offline flowchart/sequence renderer shared by
	// HTML pages and the standalone SVG assets of the export set.
	Diagram DiagramOptions
	// Limits bound the set; zero values take the defaults.
	Limits ExportLimits
}

// ExportLimits bound one export.
type ExportLimits struct {
	MaxFiles      int
	MaxTotalBytes int64
}

func (l ExportLimits) effective() (maxFiles int, maxBytes int64) {
	maxFiles, maxBytes = l.MaxFiles, l.MaxTotalBytes
	if maxFiles <= 0 {
		maxFiles = DefaultMaxExportFiles
	}
	if maxBytes <= 0 {
		maxBytes = DefaultMaxExportBytes
	}
	return maxFiles, maxBytes
}

// normalized validates the option combination.
func (o ExportOptions) normalized() (ExportOptions, error) {
	switch o.Format {
	case "", ExportMarkdown:
		o.Format = ExportMarkdown
	case ExportHTML:
	default:
		return o, fmt.Errorf("unsupported export format %q (supported: %s, %s)", o.Format, ExportMarkdown, ExportHTML)
	}
	if strings.TrimSpace(o.TargetDir) == "" {
		return o, fmt.Errorf("export target directory is empty")
	}
	if !filepath.IsAbs(o.TargetDir) {
		return o, fmt.Errorf("export target directory %q must be absolute", o.TargetDir)
	}
	if o.Labels.Interface == "" {
		o.Labels = EnglishCardLabels()
	}
	return o, nil
}

// ExportFile is one planned output file.
type ExportFile struct {
	// TargetPath is relative to the target directory, slash-separated.
	TargetPath string
	// Bytes is the planned file size in bytes.
	Bytes int64
	// SourceRel is the source project path for generated documents and
	// copied assets, or "" for the generated index.
	SourceRel string
	// Generated marks files whose content docgen produced (documents, index).
	Generated bool
	// Copied marks asset files copied verbatim from the project.
	Copied bool
}

// ExportConflict is a target that cannot be written.
type ExportConflict struct {
	TargetPath string
	Reason     string
}

// Preview holds everything the export preview panel shows without touching
// the target directory.
type Preview struct {
	Format ExportFormat
	Files  []ExportFile
	// Docs are the source rel paths in the set.
	Docs []string
	// Snippets carries the first bytes of each generated document.
	Snippets map[string]string
	// Conflicts list blocked targets (pre-existing files that are manual,
	// symlinked, or that the run may not overwrite).
	Conflicts []ExportConflict
	// Diagnostics are the merged document, link and reference findings of
	// the set, already sorted per document.
	Diagnostics []core.DocDiagnostic
	// TruncatedFiles / TruncatedBytes report limit exhaustion.
	TruncatedFiles bool
	TruncatedBytes bool
	TotalBytes     int64
	Err            error // setup-level failure (bad options, project problem)
}

// HasErrors reports whether the preview contains error-severity diagnostics
// or a setup error.
func (p *Preview) HasErrors() bool {
	if p.Err != nil {
		return true
	}
	for _, d := range p.Diagnostics {
		if d.Severity == core.SeverityError {
			return true
		}
	}
	return false
}

// Result reports the writes RunExport performed.
type Result struct {
	Written    []ExportFile
	Skipped    []ExportFile
	Conflicts  []ExportConflict
	TotalBytes int64
}

// exportSet is the computed export before any write.
type exportSet struct {
	opts      ExportOptions
	project   *core.Project
	docs      []string
	content   map[string]string // rel -> source content
	expanded  map[string]*Expansion
	assets    []assetCopy
	docsInSet map[string]bool
	files     []ExportFile
	conflicts []ExportConflict
	// generated carries content produced during planning (diagram SVGs)
	// keyed by target path; writeFile writes it verbatim.
	generated      map[string][]byte
	diagramPlanned []ExportFile
	truncF         bool
	truncB         bool
	// indexTarget/indexContent carry the computed index so preview and the
	// writer cannot drift.
	indexTarget  string
	indexContent string
}

// markdown renders one document's expanded content with the generated
// marker, exactly as it is written.
func (s *exportSet) markdown(rel string) string {
	return GeneratedFileMarker + "\n\n" + s.expanded[rel].Markdown
}

// assetCopy is one asset copied into the set.
type assetCopy struct {
	rel   string // store-relative source
	size  int64
	media string
}

const (
	// DiagOutsideSet marks a link from an exported document to a document
	// that is in the project but not in this export set (severity info).
	DiagOutsideSet = "docgen-doc-outside-export"
)

// PreviewExport computes the export set and its diagnostics without writing
// anything.
func PreviewExport(project *core.Project, opts ExportOptions) *Preview {
	opts, err := opts.normalized()
	if err != nil {
		return &Preview{Err: err}
	}
	if project == nil || project.Store == nil || project.File == nil {
		return &Preview{Err: fmt.Errorf("project is not loaded")}
	}
	if err := checkTargetDir(project, opts.TargetDir); err != nil {
		return &Preview{Err: err}
	}
	set, err := buildExportSet(project, opts)
	if err != nil {
		return &Preview{Err: err}
	}
	return set.preview()
}

// RunExport writes the export set atomically: every file lands through a
// temporary file in the target directory followed by a rename, files are
// never deleted, and manual or symlinked targets are always refused.
func RunExport(project *core.Project, opts ExportOptions) (*Result, error) {
	opts, err := opts.normalized()
	if err != nil {
		return nil, err
	}
	if project == nil || project.Store == nil || project.File == nil {
		return nil, fmt.Errorf("project is not loaded")
	}
	if err := checkTargetDir(project, opts.TargetDir); err != nil {
		return nil, err
	}
	set, err := buildExportSet(project, opts)
	if err != nil {
		return nil, err
	}
	return set.apply()
}

// checkTargetDir refuses unsafe targets: the project store itself and the
// filesystem root.
func checkTargetDir(p *core.Project, target string) error {
	abs := filepath.Clean(target)
	if abs == string(filepath.Separator) {
		return fmt.Errorf("refusing to export into the filesystem root")
	}
	storeRoot := resolveTargetSymlinks(p.Store.Root)
	abs = resolveTargetSymlinks(abs)
	if abs == storeRoot || strings.HasPrefix(abs, storeRoot+string(filepath.Separator)) {
		return fmt.Errorf("target directory is inside the GoRequest project store (%s); pick a separate export directory", p.Store.Root)
	}
	return nil
}

func resolveTargetSymlinks(p string) string {
	clean := filepath.Clean(p)
	cur := clean
	var tail []string
	for {
		if resolved, err := filepath.EvalSymlinks(cur); err == nil {
			for i := len(tail) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, tail[i])
			}
			return resolved
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return clean
		}
		tail = append(tail, filepath.Base(cur))
		cur = parent
	}
}

// buildExportSet enumerates documents and assets, expands cards, computes
// conflicts and diagnostics, and bounds the set. It performs no writes.
func buildExportSet(project *core.Project, opts ExportOptions) (*exportSet, error) {
	set := &exportSet{
		opts: opts, project: project,
		content: map[string]string{}, expanded: map[string]*Expansion{},
		docsInSet: map[string]bool{},
		generated: map[string][]byte{},
	}

	docs, err := exportDocs(project, opts)
	if err != nil {
		return nil, err
	}
	set.docs = docs
	for _, d := range docs {
		set.docsInSet[d] = true
	}

	// Read + expand each document.
	for _, rel := range docs {
		src := ""
		if override, ok := opts.ContentOverrides[rel]; ok {
			src = override
		} else {
			data, _, err := project.Store.Read(rel)
			if err != nil {
				return nil, fmt.Errorf("reading %s: %w", rel, err)
			}
			src = string(data)
		}
		set.content[rel] = src
		exp, err := ExpandDoc(project, src, CardOptions{Labels: opts.Labels})
		if err != nil {
			return nil, fmt.Errorf("expanding %s: %w", rel, err)
		}
		set.expanded[rel] = exp
	}

	// Render supported Mermaid diagrams into standalone SVG files shared by
	// both export formats. The Markdown documents keep their source verbatim;
	// the HTML pages additionally embed the same scene inline.
	if !opts.Diagram.Disable {
		set.collectDiagramAssets()
	}

	// Collect assets from the sources.
	if opts.IncludeAssets {
		seen := map[string]bool{}
		for _, rel := range docs {
			for _, a := range collectAssets(project, rel, set.content[rel]) {
				if seen[a.rel] {
					continue
				}
				seen[a.rel] = true
				set.assets = append(set.assets, a)
			}
		}
	}

	// Plan files: documents first, then assets, then the index last.
	switch opts.Format {
	case ExportHTML:
		for _, rel := range docs {
			set.addFile(ExportFile{TargetPath: htmlTargetPath(rel), Bytes: int64(len(set.html(rel))), SourceRel: rel, Generated: true})
		}
	case ExportMarkdown:
		for _, rel := range docs {
			set.addFile(ExportFile{TargetPath: rel, Bytes: int64(len(set.markdown(rel))), SourceRel: rel, Generated: true})
		}
	}
	for _, a := range set.assets {
		set.addFile(ExportFile{TargetPath: a.rel, Bytes: a.size, SourceRel: a.rel, Copied: true})
	}
	for _, f := range set.diagramFiles() {
		set.addFile(f)
	}
	idx := set.indexFile()
	set.indexTarget, set.indexContent = idx.TargetPath, idx.content
	set.addFile(ExportFile{TargetPath: idx.TargetPath, Bytes: int64(len(idx.content)), Generated: true, SourceRel: ""})

	maxFiles, maxBytes := opts.Limits.effective()
	if len(set.files) > maxFiles {
		set.truncF = true
		over := set.files[maxFiles:]
		set.files = set.files[:maxFiles]
		set.conflicts = append(set.conflicts, ExportConflict{TargetPath: "…", Reason: fmt.Sprintf("file limit %d reached; %d further files dropped", maxFiles, len(over))})
	}
	var total int64
	for _, f := range set.files {
		total += f.Bytes
	}
	if total > maxBytes {
		set.truncB = true
		set.conflicts = append(set.conflicts, ExportConflict{TargetPath: "…", Reason: fmt.Sprintf("byte limit %d exceeded (%d bytes planned)", maxBytes, total)})
	}
	return set, nil
}

// exportDocs returns the deterministic document list of the set.
func exportDocs(project *core.Project, opts ExportOptions) ([]string, error) {
	if len(opts.Docs) > 0 {
		docs := append([]string(nil), opts.Docs...)
		sort.Strings(docs)
		out := make([]string, 0, len(docs))
		seen := map[string]bool{}
		for _, d := range docs {
			if d == "" || seen[d] {
				continue
			}
			seen[d] = true
			if _, ok := opts.ContentOverrides[d]; !ok && !project.Store.Exists(d) {
				return nil, fmt.Errorf("document %s is not in the project", d)
			}
			out = append(out, d)
		}
		return out, nil
	}
	out := make([]string, 0, len(project.Docs))
	for _, entry := range project.Docs {
		out = append(out, entry.RelPath)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("project has no documents under docs/")
	}
	return out, nil
}

func (s *exportSet) addFile(f ExportFile) { s.files = append(s.files, f) }

// collectDiagramAssets renders every supported Mermaid fence of the
// exported sources into a standalone SVG file. The documents themselves
// keep their source unchanged, so a Markdown round trip stays lossless.
func (s *exportSet) collectDiagramAssets() {
	for _, rel := range s.docs {
		d := markdown.Parse(s.content[rel])
		blockIndex := 0
		for _, b := range d.Blocks() {
			if b.Kind != markdown.CodeBlock || !strings.EqualFold(b.Language(), "mermaid") {
				continue
			}
			source := codeTextOf(s.content[rel], b)
			blockIndex++
			scene, diag := diagram.Render(context.Background(), source, diagram.Options{FontSize: s.opts.Diagram.FontSize})
			if diag != nil {
				continue
			}
			svg, diag := diagram.RenderSVG(scene, s.opts.Diagram.Theme, diagram.DefaultLimits)
			if diag != nil {
				continue
			}
			content := GeneratedFileMarker + "\n" + svg
			target := diagramTargetPath(rel, blockIndex, source)
			s.generated[target] = []byte(content)
			s.diagramPlanned = append(s.diagramPlanned, ExportFile{
				TargetPath: target, Bytes: int64(len(content)), SourceRel: rel, Generated: true,
			})
		}
	}
}

func (s *exportSet) diagramFiles() []ExportFile { return s.diagramPlanned }

// diagramTargetPath names an SVG deterministically from the source digest,
// so repeated exports overwrite their own previous file and never orphan a
// new name for unchanged input.
func diagramTargetPath(rel string, block int, source string) string {
	base := strings.TrimSuffix(path.Base(rel), path.Ext(rel))
	var sb strings.Builder
	for _, r := range base {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			sb.WriteRune(r)
		default:
			sb.WriteByte('-')
		}
	}
	name := strings.Trim(sb.String(), "-")
	if name == "" {
		name = "diagram"
	}
	return "diagrams/" + name + "-" + strconv.Itoa(block) + "-" + core.Digest([]byte(source))[:12] + ".svg"
}

// codeTextOf extracts the literal content of a fenced code block.
func codeTextOf(src string, b markdown.Block) string {
	raw := src[b.Start:b.End]
	lines := strings.Split(strings.TrimRight(raw, "\n"), "\n")
	if b.Fenced {
		return fencedCodeText(lines)
	}
	return strings.Join(lines, "\n")
}

// htmlTargetPath maps docs/a/b.md to docs/a/b.html.
func htmlTargetPath(rel string) string {
	return strings.TrimSuffix(rel, ".md") + ".html"
}

// html renders one document's expanded Markdown as a standalone page.
func (s *exportSet) html(rel string) string {
	exp := s.expanded[rel]
	opts := RenderOptions{
		SourceRel:    rel,
		Cards:        exp.Cards,
		MermaidNote:  s.opts.MermaidNote,
		Diagram:      s.opts.Diagram,
		ResolveLink:  s.resolveLink,
		ResolveImage: s.resolveImage,
	}
	body := RenderHTML(exp.Markdown, opts)
	return RenderHTMLDocument(docTitle(s.project, rel), body)
}

// resolveLinkedTarget maps a destination to a file in the planned set and
// returns its target-relative URL from sourceRel's directory.
func (s *exportSet) resolveLink(sourceRel, dest string) (string, bool) {
	if id, ok := ParseRefLink(dest); ok {
		return "#" + cardAnchor(id), true
	}
	if IsValidRefShape(dest) {
		return "", false
	}
	switch {
	case strings.HasPrefix(dest, "http://"), strings.HasPrefix(dest, "https://"), strings.HasPrefix(dest, "mailto:"):
		return dest, true
	case strings.HasPrefix(dest, "#"):
		return dest, true
	}
	// Relative destination: confine to the export set's project resolution.
	tgt, err := s.project.ResolveDocLink(sourceRel, dest)
	if err != nil {
		return "", false
	}
	switch tgt.Kind {
	case core.LinkDoc:
		if !s.docsInSet[tgt.RelPath] {
			// The document exists in the project but is not part of this
			// export set; a dead link would be worse than plain text.
			return "", false
		}
		target := htmlTargetPath(tgt.RelPath)
		if s.opts.Format == ExportMarkdown {
			target = tgt.RelPath
		}
		uri := targetURI(path.Dir(sourceRel), target)
		if tgt.Fragment != "" || strings.Contains(dest, "#") {
			frag := ""
			if i := strings.IndexByte(dest, '#'); i >= 0 {
				frag = dest[i:]
			}
			uri += fragmentURI(frag)
		}
		return uri, true
	case core.LinkAsset, core.LinkAnchor:
		if tgt.Kind == core.LinkAnchor {
			return dest, true
		}
		if !s.opts.IncludeAssets {
			return "", false
		}
		if !s.assetInSet(tgt.RelPath) {
			return "", false
		}
		return targetURI(path.Dir(sourceRel), tgt.RelPath), true
	}
	return "", false
}

// resolveImage maps an image destination to a copied asset URL.
func (s *exportSet) resolveImage(sourceRel, dest string) (string, bool, string) {
	info, err := s.project.ResolveDocAsset(sourceRel, dest)
	if err != nil {
		if problem, ok := err.(*core.AssetProblem); ok && problem.Kind == core.AssetProblemRemote {
			return "", false, "remote image not embedded (offline export never downloads)"
		}
		return "", false, "attachment not available; alt text only"
	}
	if !s.opts.IncludeAssets || !s.assetInSet(info.RelPath) {
		return "", false, "attachment not included in this export; alt text only"
	}
	return targetURI(path.Dir(sourceRel), info.RelPath), true, ""
}

func (s *exportSet) assetInSet(rel string) bool {
	for _, a := range s.assets {
		if a.rel == rel {
			return true
		}
	}
	return false
}

// collectAssets enumerates the local attachments referenced by a document.
func collectAssets(project *core.Project, docRel, src string) []assetCopy {
	var out []assetCopy
	addTarget := func(rel string) {
		abs, err := project.Store.ResolvePath(rel)
		if err == nil {
			if fi, err := os.Lstat(project.Store.Root + string(filepath.Separator) + filepath.FromSlash(abs)); err == nil && fi.Mode().IsRegular() {
				out = append(out, assetCopy{rel: rel, size: fi.Size()})
			}
		}
	}
	d := markdown.Parse(src)
	for i := range d.Blocks() {
		for _, in := range d.Inlines(i) {
			switch in.Kind {
			case markdown.Image:
				if info, err := project.ResolveDocAsset(docRel, in.Dest); err == nil {
					out = append(out, assetCopy{rel: info.RelPath, size: info.Size, media: info.MediaType})
				}
			case markdown.Link:
				if t, err := project.ResolveDocLink(docRel, in.Dest); err == nil && t.Kind == core.LinkAsset {
					addTarget(t.RelPath)
				}
			}
		}
	}
	return out
}

// indexFile builds the set's index (README.md or index.html) plus snapshot
// summary.
type indexOutput struct {
	TargetPath string
	content    string
}

func (s *exportSet) indexFile() indexOutput {
	projectName := s.project.File.Name
	if projectName == "" {
		projectName = s.project.File.ID
	}
	var b strings.Builder
	b.WriteString(GeneratedFileMarker)
	b.WriteString("\n\n# " + MarkdownCell(projectName) + "\n\n")
	fmt.Fprintf(&b, "- Format: `%s`\n", s.opts.Format)
	fmt.Fprintf(&b, "- Documents: %d\n", len(s.docs))
	fmt.Fprintf(&b, "- Attachments: %d\n", len(s.assets))
	requests := s.referencedRequests()
	fmt.Fprintf(&b, "- Referenced requests: %d\n", len(requests))
	for _, id := range requests {
		digest := s.requestDigest(id)
		fmt.Fprintf(&b, "  - `%s` (`%s`)\n", id, digest)
	}
	b.WriteString("\n")
	switch s.opts.Format {
	case ExportMarkdown:
		b.WriteString("## Documents\n\n")
		for _, rel := range s.docs {
			fmt.Fprintf(&b, "- [%s](%s)\n", MarkdownCell(rel), MarkdownCell(rel))
		}
		s.snapshotNotes(&b)
		return indexOutput{TargetPath: "README.md", content: b.String()}
	default:
		var body strings.Builder
		body.WriteString("<h1>" + html.EscapeString(projectName) + "</h1>\n")
		body.WriteString("<ul>\n")
		for _, rel := range s.docs {
			fmt.Fprintf(&body, "<li><a href=\"%s\">%s</a></li>\n",
				html.EscapeString(targetURI("", htmlTargetPath(rel))), html.EscapeString(rel))
		}
		body.WriteString("</ul>\n")
		notes := s.snapshotNotesPlain()
		if notes != "" {
			body.WriteString("<blockquote>" + html.EscapeString(notes) + "</blockquote>\n")
		}
		return indexOutput{
			TargetPath: "index.html",
			content:    RenderHTMLDocument(projectName, body.String()),
		}
	}
}

// referencedRequests collects the sorted request IDs referenced by the set.
func (s *exportSet) referencedRequests() []string {
	seen := map[string]bool{}
	for _, rel := range s.docs {
		for _, r := range s.expanded[rel].Refs {
			if r.Malformed {
				continue
			}
			seen[r.ID] = true
		}
	}
	out := make([]string, 0, len(seen))
	for id := range seen {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// requestDigest returns the stable digest of a stored request, or "?" when
// the request is not in the project.
func (s *exportSet) requestDigest(id string) string {
	entry, ok := s.project.Requests[id]
	if !ok || entry.Request == nil {
		return "missing"
	}
	data, err := s.project.EncodeRequest(entry.Request)
	if err != nil {
		return "unencodable"
	}
	return core.Digest(data)[:12]
}

// snapshotNotes appends the honest notes of an export: what was kept, what
// was refused.
func (s *exportSet) snapshotNotes(b *strings.Builder) {
	notes := s.snapshotNotesPlain()
	if notes != "" {
		b.WriteString("\n" + notes + "\n")
	}
}

func (s *exportSet) snapshotNotesPlain() string {
	var notes []string
	for _, a := range s.assets {
		if strings.HasSuffix(a.rel, ".svg") {
			notes = append(notes, "SVG attachments are copied verbatim and are not sanitized; review them before publishing.")
			break
		}
	}
	if len(s.conflicts) > 0 {
		notes = append(notes, fmt.Sprintf("%d target conflict(s) are listed in the export preview; conflicted files are skipped, never overwritten.", len(s.conflicts)))
	}
	return strings.Join(notes, "\n")
}

// preview assembles the no-write preview.
func (s *exportSet) preview() *Preview {
	p := &Preview{
		Format:   s.opts.Format,
		Files:    s.files,
		Docs:     s.docs,
		Snippets: map[string]string{},
	}
	for _, rel := range s.docs {
		p.Snippets[rel] = s.snippet(rel)
	}
	p.Conflicts = append(p.Conflicts, s.fileConflicts()...)
	p.Conflicts = append(p.Conflicts, s.conflicts...)
	p.Diagnostics = s.diagnostics()
	p.TruncatedFiles = s.truncF
	p.TruncatedBytes = s.truncB
	var total int64
	for _, f := range s.files {
		total += f.Bytes
	}
	p.TotalBytes = total
	return p
}

func (s *exportSet) snippet(rel string) string {
	data := s.html(rel)
	if s.opts.Format == ExportMarkdown {
		data = s.markdown(rel)
	}
	if len(data) > snippetBytes {
		return data[:snippetBytes] + "…"
	}
	return data
}

// diagnostics merges the B09 document findings with docgen reference
// findings for every document in the set.
func (s *exportSet) diagnostics() []core.DocDiagnostic {
	var all []core.DocDiagnostic
	for _, rel := range s.docs {
		exp := s.expanded[rel]
		base := s.project.DiagnoseDoc(rel, s.content[rel])
		refs := DiagnoseRefs(s.project, rel, s.content[rel], exp)
		all = append(all, MergeDocDiagnostics(base, refs)...)
		all = append(all, s.outsideSetDiagnostics(rel, s.content[rel])...)
	}
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].RelPath == all[j].RelPath {
			if all[i].Line == all[j].Line {
				return all[i].Column < all[j].Column
			}
			return all[i].Line < all[j].Line
		}
		return all[i].RelPath < all[j].RelPath
	})
	return all
}

// outsideSetDiagnostics marks links to project documents that are not part of
// this export set (their .html will not exist).
func (s *exportSet) outsideSetDiagnostics(docRel, src string) []core.DocDiagnostic {
	d := markdown.Parse(src)
	var out []core.DocDiagnostic
	for i := range d.Blocks() {
		for _, in := range d.Inlines(i) {
			if in.Kind != markdown.Link || in.Dest == "" {
				continue
			}
			if IsValidRefShape(in.Dest) {
				continue
			}
			t, err := s.project.ResolveDocLink(docRel, in.Dest)
			if err != nil || t.Kind != core.LinkDoc {
				continue
			}
			if !s.docsInSet[t.RelPath] {
				line, col := LineColAt(src, in.Start)
				out = append(out, core.DocDiagnostic{
					RelPath: docRel, Line: line, Column: col,
					Severity: core.SeverityInfo, Kind: DiagOutsideSet, Dest: t.RelPath,
					Message: fmt.Sprintf("document %s is in the project but not in this export set; the link stays plain text in markdown sets and degrades in HTML", t.RelPath),
				})
			}
		}
	}
	return out
}

// fileConflicts lists pre-existing targets the run would refuse to write.
func (s *exportSet) fileConflicts() []ExportConflict {
	var out []ExportConflict
	for _, f := range s.files {
		absTarget := filepath.Join(s.opts.TargetDir, filepath.FromSlash(f.TargetPath))
		fi, err := os.Lstat(absTarget)
		if err != nil {
			continue
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			out = append(out, ExportConflict{TargetPath: f.TargetPath, Reason: "target is a symbolic link"})
			continue
		}
		if !fi.Mode().IsRegular() {
			out = append(out, ExportConflict{TargetPath: f.TargetPath, Reason: "target exists and is not a regular file"})
			continue
		}
		if !s.opts.AllowOverwrite {
			out = append(out, ExportConflict{TargetPath: f.TargetPath, Reason: "target exists and overwriting is disabled"})
			continue
		}
		if !s.canOverwrite(f, absTarget) {
			out = append(out, ExportConflict{TargetPath: f.TargetPath, Reason: "target exists and does not carry the docgen marker; manual files are never overwritten"})
		}
	}
	return out
}

func (s *exportSet) canOverwrite(f ExportFile, absTarget string) bool {
	if f.Copied {
		srcPath := filepath.Join(s.project.Store.Root, filepath.FromSlash(f.SourceRel))
		a, err1 := os.ReadFile(absTarget)
		b, err2 := os.ReadFile(srcPath)
		return err1 == nil && err2 == nil && string(a) == string(b)
	}
	return isDocgenFile(absTarget)
}

// isDocgenFile reports whether an existing file carries the generated marker.
func isDocgenFile(absPath string) bool {
	f, err := os.Open(absPath)
	if err != nil {
		return false
	}
	defer f.Close()
	buf := make([]byte, len(GeneratedFileMarker))
	if _, err := f.Read(buf); err != nil {
		return false
	}
	return string(buf) == GeneratedFileMarker
}

// apply writes the planned files atomically.
func (s *exportSet) apply() (*Result, error) {
	res := &Result{}
	blocked := s.fileConflicts()
	blockedMap := map[string]bool{}
	for _, c := range blocked {
		blockedMap[c.TargetPath] = true
	}
	res.Conflicts = append(res.Conflicts, blocked...)
	res.Conflicts = append(res.Conflicts, s.conflicts...)

	maxFiles, maxBytes := s.opts.Limits.effective()
	written, running := 0, int64(0)
	for _, f := range s.files {
		if blockedMap[f.TargetPath] {
			res.Skipped = append(res.Skipped, f)
			continue
		}
		if written >= maxFiles || running+f.Bytes > maxBytes {
			res.Skipped = append(res.Skipped, f)
			continue
		}
		if err := s.writeFile(f); err != nil {
			return res, fmt.Errorf("writing %s: %w", f.TargetPath, err)
		}
		res.Written = append(res.Written, f)
		res.TotalBytes += f.Bytes
		running += f.Bytes
		written++
	}
	return res, nil
}

// writeFile produces one target file.
func (s *exportSet) writeFile(f ExportFile) error {
	var data []byte
	if g, ok := s.generated[f.TargetPath]; ok {
		data = g
	} else {
		switch {
		case f.Generated && f.SourceRel == "" && f.TargetPath == s.indexTarget:
			data = []byte(s.indexContent)
		case f.Generated && s.opts.Format == ExportHTML:
			data = []byte(s.html(f.SourceRel))
		case f.Generated:
			data = []byte(s.markdown(f.SourceRel))
		case f.Copied:
			b, err := os.ReadFile(filepath.Join(s.project.Store.Root, filepath.FromSlash(f.SourceRel)))
			if err != nil {
				return err
			}
			data = b
		default:
			return fmt.Errorf("internal error: unplannable file %s", f.TargetPath)
		}
	}
	absTarget := filepath.Join(s.opts.TargetDir, filepath.FromSlash(f.TargetPath))
	if err := mkdirAllNoSymlink(filepath.Dir(absTarget), s.opts.TargetDir); err != nil {
		return err
	}
	if fi, err := os.Lstat(absTarget); err == nil {
		if fi.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("target is a symbolic link")
		}
		if !s.opts.AllowOverwrite || !s.canOverwrite(f, absTarget) {
			return fmt.Errorf("target exists and is protected from overwriting")
		}
	}
	tmp, err := os.CreateTemp(filepath.Dir(absTarget), ".docgen-tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after a successful rename
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return err
	}
	return os.Rename(tmpName, absTarget)
}

// mkdirAllNoSymlink creates dir and its parents up to stopAt, refusing to
// traverse a symlinked directory.
func mkdirAllNoSymlink(dir, stopAt string) error {
	stopAt = filepath.Clean(stopAt)
	var dirs []string
	cur := filepath.Clean(dir)
	for strings.HasPrefix(cur, stopAt) && cur != parentOf(cur) {
		dirs = append(dirs, cur)
		if cur == stopAt {
			break
		}
		next := parentOf(cur)
		if len(next) >= len(cur) {
			break
		}
		cur = next
	}
	for i := len(dirs) - 1; i >= 0; i-- {
		d := dirs[i]
		if fi, err := os.Lstat(d); err == nil {
			if fi.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("target directory component %s is a symbolic link", d)
			}
			if fi.IsDir() {
				continue
			}
			return fmt.Errorf("target directory component %s exists and is not a directory", d)
		}
		if err := os.Mkdir(d, 0o755); err != nil && !os.IsExist(err) {
			return err
		}
	}
	return nil
}

func parentOf(dir string) string {
	parent := filepath.Dir(dir)
	if len(parent) >= len(dir) {
		return dir
	}
	return parent
}

// --- URL helpers -------------------------------------------------------------

// targetURI renders target's URL as seen from the directory baseRelDir of a
// source document, percent-encoding each path segment (Chinese, spaces and
// other reserved bytes stay intact across readers).
func targetURI(baseDir, target string) string {
	rel := target
	if baseDir != "" && baseDir != "." {
		base := baseDir
		var ups []string
		for {
			if rel == base || strings.HasPrefix(rel, base+"/") {
				break
			}
			ups = append(ups, "..")
			base = path.Dir(base)
			if base == "." || base == "/" {
				break
			}
		}
		for _, up := range ups {
			rel = up + "/" + strings.TrimPrefix(rel, base)
		}
	}
	return encodePathURI(rel)
}

// encodePathURI percent-encodes every segment of a slash-separated relative
// path, preserving the directory structure.
func encodePathURI(rel string) string {
	segs := strings.Split(rel, "/")
	for i, seg := range segs {
		segs[i] = uriPathEscapeSeg(seg)
	}
	return strings.Join(segs, "/")
}

func uriPathEscapeSeg(seg string) string {
	out := make([]byte, 0, len(seg))
	for i := 0; i < len(seg); i++ {
		c := seg[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
			out = append(out, c)
		case c == '-' || c == '_' || c == '.' || c == '~':
			out = append(out, c)
		default:
			out = append(out, hexEscapeByte(c)...)
		}
	}
	return string(out)
}

func hexEscapeByte(c byte) string {
	const hexdigits = "0123456789ABCDEF"
	return "%" + string(hexdigits[c>>4]) + string(hexdigits[c&15])
}

// fragmentURI percent-encodes a "#anchor" part.
func fragmentURI(frag string) string {
	frag = strings.TrimPrefix(frag, "#")
	if frag == "" {
		return ""
	}
	return "#" + uriPathEscapeSeg(frag)
}

// docTitle returns the index/page title of a document.
func docTitle(p *core.Project, rel string) string {
	name := p.File.Name
	if name == "" {
		name = p.File.ID
	}
	return name + " — " + rel
}
