package core

import (
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
)

// This file owns local document assets: validated reads for the preview and
// attachments imported into the project. Every path is confined to the
// .gorequest directory, symlinks are refused, sizes are bounded and only a
// documented extension whitelist is accepted. Remote images are never
// downloaded.

// DefaultAssetMaxBytes bounds both imported files and files the preview may
// load.
const DefaultAssetMaxBytes int64 = 10 << 20

// docAssetTypes maps every allowed asset extension to its media type. Only
// inert content is allowed: images, documents and archives. Executables,
// scripts and unknown types are refused.
var docAssetTypes = map[string]string{
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".gif":  "image/gif",
	".webp": "image/webp",
	".bmp":  "image/bmp",
	".svg":  "image/svg+xml",
	".pdf":  "application/pdf",
	".txt":  "text/plain",
	".md":   "text/markdown",
	".json": "application/json",
	".yaml": "application/yaml",
	".yml":  "application/yaml",
	".csv":  "text/csv",
	".zip":  "application/zip",
}

// AllowedAssetExts returns the whitelist as sorted extensions without dots,
// for a native file dialog's filters.
func AllowedAssetExts() []string {
	out := make([]string, 0, len(docAssetTypes))
	for ext := range docAssetTypes {
		out = append(out, strings.TrimPrefix(ext, "."))
	}
	sort.Strings(out)
	return out
}

// asset extension groups used for dialog filters and insertion helpers.
func assetExtsWhere(match func(string) bool) []string {
	var out []string
	for ext, media := range docAssetTypes {
		if match(media) {
			out = append(out, strings.TrimPrefix(ext, "."))
		}
	}
	sort.Strings(out)
	return out
}

// ImageAssetExts returns the image extensions of the whitelist.
func ImageAssetExts() []string {
	return assetExtsWhere(func(media string) bool { return strings.HasPrefix(media, "image/") })
}

// Asset problem kinds, reported in AssetProblem.Kind.
const (
	AssetProblemRemote      = "remote"
	AssetProblemMissing     = "missing"
	AssetProblemOutside     = "outside-project"
	AssetProblemSymlink     = "symlink"
	AssetProblemNotFile     = "not-file"
	AssetProblemTooLarge    = "too-large"
	AssetProblemUnsupported = "unsupported-type"
	AssetProblemRead        = "read"
	AssetProblemTarget      = "invalid-target"
)

// AssetProblem is an asset reference that cannot be used.
type AssetProblem struct {
	Kind   string
	Ref    string
	Detail string
}

func (e *AssetProblem) Error() string {
	if e.Detail == "" {
		return e.Kind + ": " + e.Ref
	}
	return fmt.Sprintf("%s: %s (%s)", e.Kind, e.Detail, e.Ref)
}

// AssetKind returns the AssetProblem kind of err, or "".
func AssetKind(err error) string {
	if p, ok := err.(*AssetProblem); ok {
		return p.Kind
	}
	return ""
}

// AssetInfo describes a validated local asset.
type AssetInfo struct {
	RelPath   string
	AbsPath   string
	Ext       string
	MediaType string
	Size      int64
	IsSVG     bool
}

// ResolveDocAsset validates the image or attachment reference ref written in
// docRel and returns its location and metadata. It performs no read of the
// content and no network access.
func (p *Project) ResolveDocAsset(docRel, ref string) (AssetInfo, error) {
	var zero AssetInfo
	raw := strings.TrimSpace(ref)
	if raw == "" {
		return zero, &AssetProblem{Kind: AssetProblemMissing, Ref: raw, Detail: "empty reference"}
	}
	if p == nil || p.Store == nil {
		return zero, &AssetProblem{Kind: AssetProblemMissing, Ref: raw, Detail: "no project"}
	}
	if scheme := urlScheme(raw); scheme != "" {
		switch scheme {
		case "http", "https":
			return zero, &AssetProblem{Kind: AssetProblemRemote, Ref: raw, Detail: "remote assets are never downloaded"}
		case "data":
			return zero, &AssetProblem{Kind: AssetProblemRemote, Ref: raw, Detail: "inline data is not loaded"}
		default:
			return zero, &AssetProblem{Kind: AssetProblemUnsupported, Ref: raw, Detail: scheme + ": is not allowed"}
		}
	}
	if strings.HasPrefix(raw, "//") {
		return zero, &AssetProblem{Kind: AssetProblemRemote, Ref: raw, Detail: "remote assets are never downloaded"}
	}
	raw, _, _ = strings.Cut(raw, "#")
	raw, _, _ = strings.Cut(raw, "?")
	if raw == "" {
		return zero, &AssetProblem{Kind: AssetProblemMissing, Ref: ref, Detail: "empty path"}
	}
	decoded, err := url.PathUnescape(raw)
	if err != nil {
		return zero, &AssetProblem{Kind: AssetProblemUnsupported, Ref: raw, Detail: err.Error()}
	}
	if containsControl(decoded) || strings.ContainsRune(decoded, '\\') || strings.HasPrefix(decoded, "/") {
		return zero, &AssetProblem{Kind: AssetProblemOutside, Ref: raw, Detail: "asset paths must be relative"}
	}
	rel := path.Clean(path.Join(docDir(docRel), decoded))
	if rel == ".." || strings.HasPrefix(rel, "../") || rel == "." {
		return zero, &AssetProblem{Kind: AssetProblemOutside, Ref: raw}
	}
	ext := strings.ToLower(path.Ext(rel))
	media, ok := docAssetTypes[ext]
	if !ok {
		return zero, &AssetProblem{Kind: AssetProblemUnsupported, Ref: raw, Detail: ext}
	}
	abs, err := p.Store.resolve(rel)
	if err != nil {
		return zero, &AssetProblem{Kind: AssetProblemOutside, Ref: raw, Detail: err.Error()}
	}
	fi, err := os.Lstat(abs)
	if err != nil {
		return zero, &AssetProblem{Kind: AssetProblemMissing, Ref: rel}
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return zero, &AssetProblem{Kind: AssetProblemSymlink, Ref: rel, Detail: "symbolic links are not loaded"}
	}
	if !fi.Mode().IsRegular() {
		return zero, &AssetProblem{Kind: AssetProblemNotFile, Ref: rel}
	}
	if fi.Size() > DefaultAssetMaxBytes {
		return zero, &AssetProblem{Kind: AssetProblemTooLarge, Ref: rel, Detail: fmt.Sprintf("%d bytes", fi.Size())}
	}
	return AssetInfo{
		RelPath:   rel,
		AbsPath:   abs,
		Ext:       ext,
		MediaType: media,
		Size:      fi.Size(),
		IsSVG:     ext == ".svg",
	}, nil
}

// ReadDocAsset validates ref and returns its content, re-checking the size
// after the read so a file that grows meanwhile is still refused.
func (p *Project) ReadDocAsset(docRel, ref string) ([]byte, AssetInfo, error) {
	info, err := p.ResolveDocAsset(docRel, ref)
	if err != nil {
		return nil, AssetInfo{}, err
	}
	f, err := os.Open(info.AbsPath)
	if err != nil {
		return nil, info, &AssetProblem{Kind: AssetProblemRead, Ref: info.RelPath, Detail: err.Error()}
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, DefaultAssetMaxBytes+1))
	if err != nil {
		return nil, info, &AssetProblem{Kind: AssetProblemRead, Ref: info.RelPath, Detail: err.Error()}
	}
	if int64(len(b)) > DefaultAssetMaxBytes {
		return nil, info, &AssetProblem{Kind: AssetProblemTooLarge, Ref: info.RelPath}
	}
	info.Size = int64(len(b))
	return b, info, nil
}

// AssetImport is a file copied into the project and the Markdown that
// references it.
type AssetImport struct {
	// RelPath is the project-relative path of the stored copy.
	RelPath string
	// RefPath is the path from the document's directory to the copy, for
	// Markdown.
	RefPath string
	// Markdown is ready to insert at the caret: an image or a link.
	Markdown string
	Info     AssetInfo
}

// ImportDocAsset copies srcPath into targetDir, which must be inside docs/
// ("" means "docs/assets"). An existing file is never overwritten: a -2, -3,
// … suffix is chosen instead. Only whitelisted extensions and files within
// the size limit are copied.
func (p *Project) ImportDocAsset(docRel, srcPath, targetDir string) (AssetImport, error) {
	if p == nil || p.Store == nil {
		return AssetImport{}, &AssetProblem{Kind: AssetProblemTarget, Ref: targetDir, Detail: "no project"}
	}
	srcInfo, err := os.Lstat(srcPath)
	if err != nil {
		return AssetImport{}, &AssetProblem{Kind: AssetProblemMissing, Ref: srcPath}
	}
	if srcInfo.Mode()&os.ModeSymlink != 0 {
		return AssetImport{}, &AssetProblem{Kind: AssetProblemSymlink, Ref: srcPath, Detail: "symbolic links are not imported"}
	}
	if !srcInfo.Mode().IsRegular() {
		return AssetImport{}, &AssetProblem{Kind: AssetProblemNotFile, Ref: srcPath}
	}
	if srcInfo.Size() > DefaultAssetMaxBytes {
		return AssetImport{}, &AssetProblem{Kind: AssetProblemTooLarge, Ref: srcPath, Detail: fmt.Sprintf("%d bytes", srcInfo.Size())}
	}
	name := sanitizeAssetName(filepath.Base(srcPath))
	ext := strings.ToLower(path.Ext(name))
	media, ok := docAssetTypes[ext]
	if !ok {
		return AssetImport{}, &AssetProblem{Kind: AssetProblemUnsupported, Ref: srcPath, Detail: ext}
	}
	data, err := readFileLimited(srcPath, DefaultAssetMaxBytes)
	if err != nil {
		return AssetImport{}, &AssetProblem{Kind: AssetProblemRead, Ref: srcPath, Detail: err.Error()}
	}
	dir, err := normalizeAssetDir(targetDir)
	if err != nil {
		return AssetImport{}, err
	}

	stem := strings.TrimSuffix(name, path.Ext(name))
	rel := ""
	for i := 1; i <= 1000; i++ {
		candidate := stem + path.Ext(name)
		if i > 1 {
			candidate = fmt.Sprintf("%s-%d%s", stem, i, path.Ext(name))
		}
		candidate = path.Join(dir, candidate)
		if err := p.Store.SaveChecked(candidate, data, ""); err == nil {
			rel = candidate
			break
		} else if !IsConflict(err) {
			return AssetImport{}, &AssetProblem{Kind: AssetProblemRead, Ref: candidate, Detail: err.Error()}
		}
	}
	if rel == "" {
		return AssetImport{}, &AssetProblem{Kind: AssetProblemTarget, Ref: targetDir, Detail: "too many name conflicts"}
	}
	abs, err := p.Store.resolve(rel)
	if err != nil {
		return AssetImport{}, &AssetProblem{Kind: AssetProblemOutside, Ref: rel, Detail: err.Error()}
	}
	refPath := relativeDocPath(docRel, rel)
	dest := MarkdownDest(refPath)
	alt := strings.TrimSuffix(path.Base(rel), path.Ext(rel))
	md := MarkdownLinkRef(alt, dest)
	info := AssetInfo{RelPath: rel, AbsPath: abs, Ext: ext, MediaType: media, Size: int64(len(data)), IsSVG: ext == ".svg"}
	if strings.HasPrefix(media, "image/") {
		md = MarkdownImageRef(alt, dest)
	}
	return AssetImport{RelPath: rel, RefPath: refPath, Markdown: md, Info: info}, nil
}

func normalizeAssetDir(targetDir string) (string, error) {
	dir := strings.TrimSpace(filepath.ToSlash(targetDir))
	if dir == "" {
		dir = "docs/assets"
	}
	if strings.HasPrefix(dir, "/") || strings.ContainsRune(dir, '\\') || containsControl(dir) {
		return "", &AssetProblem{Kind: AssetProblemTarget, Ref: targetDir, Detail: "asset directory must be relative"}
	}
	clean := path.Clean(dir)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || !(clean == "docs" || strings.HasPrefix(clean, "docs/")) {
		return "", &AssetProblem{Kind: AssetProblemTarget, Ref: targetDir, Detail: "assets must be stored under docs/"}
	}
	return clean, nil
}

func readFileLimited(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("file is larger than %d bytes", limit)
	}
	return b, nil
}

func sanitizeAssetName(name string) string {
	name = path.Base(filepath.ToSlash(name))
	ext := strings.ToLower(path.Ext(name))
	var b strings.Builder
	for _, r := range strings.TrimSuffix(name, path.Ext(name)) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' || r == '.' || r == ' ':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	stem := strings.Trim(b.String(), " .-")
	if stem == "" {
		stem = "asset"
	}
	if rs := []rune(stem); len(rs) > 80 {
		stem = string(rs[:80])
	}
	return stem + ext
}

// relativeDocPath returns targetRel as a slash path from the document's
// directory.
func relativeDocPath(docRel, targetRel string) string {
	from := docDir(docRel)
	rel, err := filepath.Rel(filepath.FromSlash(from), filepath.FromSlash(targetRel))
	if err != nil {
		return targetRel
	}
	return filepath.ToSlash(rel)
}

// MarkdownDest percent-encodes the characters that would end or split a
// Markdown link destination. Unicode letters (including Chinese) stay
// readable; only spaces, parentheses and similar delimiters are escaped.
func MarkdownDest(rel string) string {
	var b strings.Builder
	for _, r := range rel {
		switch r {
		case '%':
			b.WriteString("%25")
		case ' ':
			b.WriteString("%20")
		case '(':
			b.WriteString("%28")
		case ')':
			b.WriteString("%29")
		case '#':
			b.WriteString("%23")
		case '?':
			b.WriteString("%3F")
		case '<':
			b.WriteString("%3C")
		case '>':
			b.WriteString("%3E")
		case '"':
			b.WriteString("%22")
		case '`':
			b.WriteString("%60")
		case '\\':
			b.WriteString("%5C")
		case '\n', '\r', '\t':
			b.WriteString("-")
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// MarkdownImageRef builds an image reference for the caret.
func MarkdownImageRef(alt, dest string) string {
	return "![" + escapeAlt(alt) + "](" + dest + ")"
}

// MarkdownLinkRef builds a link reference for the caret.
func MarkdownLinkRef(text, dest string) string {
	return "[" + escapeAlt(text) + "](" + dest + ")"
}

func escapeAlt(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, "[", `\[`)
	s = strings.ReplaceAll(s, "]", `\]`)
	s = strings.NewReplacer("\n", " ", "\r", " ", "\t", " ").Replace(s)
	return s
}
