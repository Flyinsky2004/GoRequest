// Package sdkgen generates a compilable Go HTTP client from an explicit
// OpenAPI 3.1/3.2 contract snapshot.
//
// The pipeline is deliberately layered so every step is inspectable before
// anything reaches disk:
//
//	SpecSnapshot -> Parse/Validate -> SupportedOperations -> GeneratedFiles
//	             -> Preview -> Apply
//
// The generator never reads secrets, device state, cookies, history, tokens or
// network resources. Callers pass a snapshot produced by core.ExportOpenAPI
// (JSON, fixed target version, strict lossy rejection). Generated code uses
// only the Go standard library; authentication is supplied by the caller
// through Client.Auth and is never embedded.
package sdkgen

import (
	"errors"
	"fmt"
	"strings"
)

// GeneratorVersion and TemplateVersion are recorded in every manifest. They are
// fixed for this phase so repeated generation is byte-stable and an unexpected
// version change is refused rather than guessed.
const (
	GeneratorVersion = "sdkgen-go/1"
	TemplateVersion  = "1"
)

// Output limits bound one generation before anything is written.
const (
	DefaultMaxFiles      = 500
	DefaultMaxTotalBytes = 32 << 20 // 32 MiB
)

// ManifestName is the ownership manifest written last in the target directory.
const ManifestName = ".gorequest-sdk-manifest.json"

// Sentinel errors let the desktop layer classify failures without string
// matching.
var (
	// ErrUnsupportedOperations means at least one operation cannot be generated
	// faithfully and the caller did not opt into a supported subset.
	ErrUnsupportedOperations = errors.New("sdkgen: contract contains operations that cannot be generated faithfully")
	// ErrUnsafeTarget means the output directory is not a safe project-external
	// absolute directory.
	ErrUnsafeTarget = errors.New("sdkgen: unsafe output directory")
	// ErrConflict means the target directory changed or contains files the
	// generator must not overwrite or delete.
	ErrConflict = errors.New("sdkgen: output conflict")
	// ErrInvalidManifest means an existing manifest is corrupt, malicious, or
	// was produced by an incompatible generator/template version.
	ErrInvalidManifest = errors.New("sdkgen: invalid or incompatible manifest")
	// ErrLimitExceeded means the generated set exceeds the configured limits.
	ErrLimitExceeded = errors.New("sdkgen: output limits exceeded")
)

// Options configures one generation and its preview/apply.
type Options struct {
	// PackageName is the Go package name declared by every generated source
	// file. It must be a valid, non-keyword identifier and not "main".
	PackageName string
	// ModulePath is written to the generated go.mod.
	ModulePath string
	// TargetDir is the absolute output directory. It must be outside the
	// project root and the project store.
	TargetDir string
	// ProjectRoot is the project folder (the directory containing
	// .gorequest/). When set, TargetDir must not be inside it.
	ProjectRoot string
	// IncludeUnsupported generates only the supported subset instead of
	// blocking the whole run. The preview always lists every skipped
	// operation, and the UI must not default this to true.
	IncludeUnsupported bool
	// AllowOverwrite permits updating files that are still owned by the
	// generator and whose on-disk digest matches the previous manifest.
	AllowOverwrite bool
	// AllowDeleteStale permits deleting stale generated files that are still
	// owned and unmodified. Unregistered files are never deleted.
	AllowDeleteStale bool
	// MaxFiles and MaxTotalBytes bound the set; zero values take the defaults.
	MaxFiles      int
	MaxTotalBytes int64
}

func (o Options) maxFiles() int {
	if o.MaxFiles <= 0 {
		return DefaultMaxFiles
	}
	return o.MaxFiles
}

func (o Options) maxTotalBytes() int64 {
	if o.MaxTotalBytes <= 0 {
		return DefaultMaxTotalBytes
	}
	return o.MaxTotalBytes
}

// File is one generated file, held in memory until Apply writes it.
type File struct {
	// RelPath is slash-separated and relative to the target directory.
	RelPath string
	Content []byte
	Digest  string
	// Kind is "module", "source", "readme" or "manifest".
	Kind string
}

// Operation describes one generated operation.
type Operation struct {
	Method      string // upper-case HTTP method
	Path        string // OpenAPI path template
	GoName      string // exported Go method name
	OperationID string // effective operationId (provided or derived)
	Summary     string
	Deprecated  bool
	// Security lists the security scheme names referenced by the operation
	// (names only, never credentials).
	Security []string

	// paramsList and body are the resolved generation inputs; they stay
	// unexported so the public result is a stable description.
	paramsList []paramSpec
	body       *bodySpec
}

// UnsupportedOperation explains why one operation was skipped.
type UnsupportedOperation struct {
	Method      string
	Path        string
	OperationID string
	Reason      string
}

// Diagnostic is a non-blocking finding shown in the preview.
type Diagnostic struct {
	Severity string // "info" | "warning"
	Code     string
	Location string
	Message  string
}

// Result is the complete in-memory generation output. It is deterministic for
// a given snapshot and options.
type Result struct {
	SpecVersion      string
	SpecDigest       string
	GeneratorVersion string
	TemplateVersion  string
	PackageName      string
	ModulePath       string
	Files            []File
	Operations       []Operation
	Unsupported      []UnsupportedOperation
	Diagnostics      []Diagnostic
	Manifest         Manifest
	TotalBytes       int64
}

// Generate parses the snapshot and produces the complete file set in memory.
// It performs no filesystem writes and no network or secret access.
func Generate(snapshot []byte, opts Options) (*Result, error) {
	opts, err := normalizeOptions(opts)
	if err != nil {
		return nil, err
	}
	doc, err := parseSpec(snapshot)
	if err != nil {
		return nil, err
	}
	ops, unsupported, diags, err := collectOperations(doc)
	if err != nil {
		return nil, err
	}
	if len(unsupported) > 0 && !opts.IncludeUnsupported {
		return nil, &UnsupportedError{Operations: unsupported}
	}
	if opts.IncludeUnsupported {
		for _, u := range unsupported {
			diags = append(diags, Diagnostic{
				Severity: "warning",
				Code:     "sdk-operation-skipped",
				Location: u.Method + " " + u.Path,
				Message:  u.Reason,
			})
		}
	}
	assignOperationNames(ops)
	specDigest := digestBytes(snapshot)
	files, manifest, err := buildFiles(doc, opts, ops, unsupported, diags, specDigest)
	if err != nil {
		return nil, err
	}
	result := &Result{
		SpecVersion:      doc.OpenAPI,
		SpecDigest:       specDigest,
		GeneratorVersion: GeneratorVersion,
		TemplateVersion:  TemplateVersion,
		PackageName:      opts.PackageName,
		ModulePath:       opts.ModulePath,
		Files:            files,
		Operations:       ops,
		Unsupported:      unsupported,
		Diagnostics:      diags,
		Manifest:         manifest,
	}
	for _, f := range files {
		result.TotalBytes += int64(len(f.Content))
	}
	if len(files) > opts.maxFiles() {
		return nil, fmt.Errorf("%w: %d files exceeds limit %d", ErrLimitExceeded, len(files), opts.maxFiles())
	}
	if result.TotalBytes > opts.maxTotalBytes() {
		return nil, fmt.Errorf("%w: %d bytes exceeds limit %d", ErrLimitExceeded, result.TotalBytes, opts.maxTotalBytes())
	}
	return result, nil
}

// UnsupportedError carries the operation-level reasons for a blocked run.
type UnsupportedError struct {
	Operations []UnsupportedOperation
}

func (e *UnsupportedError) Error() string {
	if e == nil || len(e.Operations) == 0 {
		return ErrUnsupportedOperations.Error()
	}
	var b strings.Builder
	b.WriteString(ErrUnsupportedOperations.Error())
	b.WriteString(": ")
	for i, op := range e.Operations {
		if i > 0 {
			b.WriteString("; ")
		}
		fmt.Fprintf(&b, "%s %s: %s", op.Method, op.Path, op.Reason)
	}
	return b.String()
}

func (e *UnsupportedError) Unwrap() error { return ErrUnsupportedOperations }

// normalizeOptions validates and normalizes options without touching disk.
func normalizeOptions(opts Options) (Options, error) {
	pkg, err := normalizePackageName(opts.PackageName)
	if err != nil {
		return opts, err
	}
	opts.PackageName = pkg
	mod, err := normalizeModulePath(opts.ModulePath)
	if err != nil {
		return opts, err
	}
	opts.ModulePath = mod
	opts.TargetDir = strings.TrimSpace(opts.TargetDir)
	if opts.TargetDir == "" {
		return opts, fmt.Errorf("%w: target directory is empty", ErrUnsafeTarget)
	}
	opts.ProjectRoot = strings.TrimSpace(opts.ProjectRoot)
	return opts, nil
}
