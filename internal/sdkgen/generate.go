package sdkgen

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"go/format"
	"sort"
)

// Manifest records generator ownership of every generated file. It is written
// last and never lists itself, so it cannot claim its own digest.
type Manifest struct {
	GeneratorVersion string          `json:"generatorVersion"`
	TemplateVersion  string          `json:"templateVersion"`
	Language         string          `json:"language"`
	SpecVersion      string          `json:"specVersion"`
	SpecDigest       string          `json:"specDigest"`
	PackageName      string          `json:"packageName"`
	ModulePath       string          `json:"modulePath"`
	Files            []ManifestEntry `json:"files"`
}

// ManifestEntry is one owned file.
type ManifestEntry struct {
	Path   string `json:"path"`
	Digest string `json:"digest"`
	Kind   string `json:"kind"`
}

func digestBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// buildFiles renders every generated file and the ownership manifest for the
// selected language.
func buildFiles(doc *specDoc, opts Options, ops []Operation, unsupported []UnsupportedOperation, diags []Diagnostic, specDigest string, comp *typeCompiler) ([]File, Manifest, error) {
	var files []File
	var err error
	switch opts.Language {
	case LanguageTypeScript:
		files, err = renderTypeScriptFiles(opts, doc, ops, unsupported, diags, specDigest, comp)
	default:
		files, err = renderGoFiles(opts, doc, ops, unsupported, diags, specDigest, comp)
	}
	if err != nil {
		return nil, Manifest{}, err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].RelPath < files[j].RelPath })

	manifest := Manifest{
		GeneratorVersion: GeneratorVersion,
		TemplateVersion:  TemplateVersionFor(opts.Language),
		Language:         string(opts.Language),
		SpecVersion:      doc.OpenAPI,
		SpecDigest:       specDigest,
		PackageName:      opts.PackageName,
		ModulePath:       opts.ModulePath,
		Files:            make([]ManifestEntry, 0, len(files)),
	}
	for _, f := range files {
		manifest.Files = append(manifest.Files, ManifestEntry{Path: f.RelPath, Digest: f.Digest, Kind: f.Kind})
	}
	manifestBytes, err := marshalManifest(manifest)
	if err != nil {
		return nil, Manifest{}, err
	}
	files = append(files, File{
		RelPath: ManifestName,
		Content: manifestBytes,
		Digest:  digestBytes(manifestBytes),
		Kind:    "manifest",
	})
	sort.Slice(files, func(i, j int) bool { return files[i].RelPath < files[j].RelPath })
	return files, manifest, nil
}

func marshalManifest(m Manifest) ([]byte, error) {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("sdkgen: encode manifest: %w", err)
	}
	return append(b, '\n'), nil
}

// formatGo formats generated source with go/format so output is canonical.
func formatGo(name, src string) ([]byte, error) {
	out, err := format.Source([]byte(src))
	if err != nil {
		return nil, fmt.Errorf("sdkgen: format %s: %w", name, err)
	}
	return out, nil
}

// newFile builds one generated file and its digest.
func newFile(rel string, content []byte, kind string) File {
	return File{RelPath: rel, Content: content, Digest: digestBytes(content), Kind: kind}
}

func newSourceFile(rel, src, kind string) (File, error) {
	formatted, err := formatGo(rel, src)
	if err != nil {
		return File{}, err
	}
	return newFile(rel, formatted, kind), nil
}
