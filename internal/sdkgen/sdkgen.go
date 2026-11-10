// Package sdkgen generates a compilable HTTP SDK from an explicit OpenAPI
// 3.1/3.2 contract snapshot.
//
// The pipeline is deliberately layered so every step is inspectable before
// anything reaches disk:
//
//	SpecSnapshot -> Parse/Validate -> Schema model -> SupportedOperations
//	             -> GeneratedFiles -> Preview -> Apply
//
// The generator never reads secrets, device state, cookies, history, tokens or
// network resources. Callers pass a snapshot produced by core.ExportOpenAPI
// (JSON, fixed target version, strict lossy rejection). Generated code uses
// only the Go standard library (for Go) or the platform fetch API (for
// TypeScript); authentication is supplied by the caller and never embedded.
package sdkgen

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Language selects the generated SDK backend.
type Language string

const (
	LanguageGo         Language = "go"
	LanguageTypeScript Language = "typescript"
)

func (l Language) String() string { return string(l) }

// GeneratorVersion and the per-language TemplateVersion are recorded in every
// manifest. They are fixed for this phase so repeated generation is
// byte-stable and an unexpected version change is refused rather than guessed.
const (
	GeneratorVersion          = "sdkgen/2"
	TemplateVersionGo         = "go/2"
	TemplateVersionTypeScript = "ts/1"
	// TemplateVersion is the Go template version, kept as an alias for callers
	// written against the first Go-only generator.
	TemplateVersion = TemplateVersionGo
)

// TemplateVersionFor returns the template version of a language.
func TemplateVersionFor(lang Language) string {
	if lang == LanguageTypeScript {
		return TemplateVersionTypeScript
	}
	return TemplateVersionGo
}

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
	// Language selects the backend. An empty value means Go, so callers written
	// against the first Go-only generator keep working.
	Language Language
	// PackageName is the Go package name / TypeScript package name declared by
	// the generated files. It must be a valid, non-keyword Go identifier.
	PackageName string
	// ModulePath is written to the generated go.mod and used as the npm package
	// name for TypeScript.
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
	// Kind is "module", "source", "readme", "config" or "manifest".
	Kind string
}

// Operation describes one generated operation.
type Operation struct {
	Method      string // upper-case HTTP method
	Path        string // OpenAPI path template
	GoName      string // exported Go method name / TypeScript function base
	OperationID string // effective operationId (provided or derived)
	Summary     string
	Deprecated  bool
	// Security lists the security scheme names referenced by the operation
	// (names only, never credentials).
	Security []string

	// BodyType names the generated request body model, empty when the operation
	// has no JSON request body.
	BodyType string
	// Responses lists the declared response bodies that were mapped to a model.
	Responses []ResponseSummary
	// TypedName and ResultName are the additive typed Go helpers. Legacy
	// methods keep their (*Response, error) signature.
	TypedName  string
	ResultName string

	// paramsList, body and responseTypes are the resolved generation inputs;
	// they stay unexported so the public result is a stable description.
	paramsList    []paramSpec
	body          *bodySpec
	responseTypes []responseType
	rawResponses  map[string]json.RawMessage
}

// ResponseSummary describes one typed response body.
type ResponseSummary struct {
	Status string
	Model  string
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
	Language         string
	SpecVersion      string
	SpecDigest       string
	GeneratorVersion string
	TemplateVersion  string
	PackageName      string
	ModulePath       string
	Files            []File
	Operations       []Operation
	Models           []ModelSummary
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

	assignOperationNames(ops)
	names := newNameAllocator(reservedTypeNames)
	for _, op := range ops {
		names.Reserve(op.GoName)
	}

	comp := newTypeCompiler(doc, names)
	kept := make([]Operation, 0, len(ops))
	for _, op := range ops {
		mark := comp.mark()
		built, reason := compileOperationTypes(comp, &op)
		if reason != "" {
			comp.rollback(mark)
			unsupported = append(unsupported, UnsupportedOperation{
				Method:      op.Method,
				Path:        op.Path,
				OperationID: op.OperationID,
				Reason:      reason,
			})
			continue
		}
		kept = append(kept, *built)
	}
	sortUnsupported(unsupported)
	if len(unsupported) > 0 && !opts.IncludeUnsupported {
		return nil, &UnsupportedError{Operations: unsupported}
	}
	diags = append(diags, comp.diags...)
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
	assignTypedNames(kept, names)
	for i := range kept {
		kept[i].Responses = kept[i].responseSummaries()
	}

	specDigest := digestBytes(snapshot)
	files, manifest, err := buildFiles(doc, opts, kept, unsupported, diags, specDigest, comp)
	if err != nil {
		return nil, err
	}
	result := &Result{
		Language:         string(opts.Language),
		SpecVersion:      doc.OpenAPI,
		SpecDigest:       specDigest,
		GeneratorVersion: GeneratorVersion,
		TemplateVersion:  TemplateVersionFor(opts.Language),
		PackageName:      opts.PackageName,
		ModulePath:       opts.ModulePath,
		Files:            files,
		Operations:       kept,
		Models:           comp.summaries(),
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

// compileOperationTypes resolves request and response schemas for one
// operation. It returns a reason (not an error) when the schemas cannot be
// represented faithfully.
func compileOperationTypes(comp *typeCompiler, op *Operation) (*Operation, string) {
	if op.body != nil && len(op.body.Schema) > 0 {
		t, err := comp.compileRoot(op.body.Schema, op.GoName+"Body",
			"operation "+op.Method+" "+op.Path+" requestBody", true)
		if err != nil {
			return nil, "request body schema: " + err.Error()
		}
		op.BodyType = t.Ref
	}
	types, err := compileResponseTypes(comp, op)
	if err != nil {
		return nil, err.Error()
	}
	op.responseTypes = types
	return op, ""
}

// responseType is one declared response body mapped to a model.
type responseType struct {
	Status string
	Type   *Type
}

func (o *Operation) responseSummaries() []ResponseSummary {
	out := make([]ResponseSummary, 0, len(o.responseTypes))
	for _, r := range o.responseTypes {
		if r.Type == nil || r.Type.Kind != TypeModel {
			continue
		}
		out = append(out, ResponseSummary{Status: r.Status, Model: r.Type.Ref})
	}
	return out
}

// assignTypedNames allocates the additive typed helper names after models have
// claimed their names, so no generated identifier collides.
func assignTypedNames(ops []Operation, names *nameAllocator) {
	for i := range ops {
		if len(ops[i].responseTypes) == 0 {
			continue
		}
		ops[i].TypedName = names.Allocate(ops[i].GoName + "Typed")
		ops[i].ResultName = names.Allocate(ops[i].GoName + "Result")
	}
}

func sortUnsupported(list []UnsupportedOperation) {
	sort.SliceStable(list, func(i, j int) bool {
		if list[i].Path == list[j].Path {
			return list[i].Method < list[j].Method
		}
		return list[i].Path < list[j].Path
	})
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

// normalizeLanguage validates and defaults the language.
func normalizeLanguage(raw Language) (Language, error) {
	s := strings.ToLower(strings.TrimSpace(string(raw)))
	switch s {
	case "":
		return LanguageGo, nil
	case string(LanguageGo):
		return LanguageGo, nil
	case string(LanguageTypeScript), "ts":
		return LanguageTypeScript, nil
	default:
		return "", fmt.Errorf("sdkgen: unsupported language %q (supported: go, typescript)", raw)
	}
}

// normalizeOptions validates and normalizes options without touching disk.
func normalizeOptions(opts Options) (Options, error) {
	lang, err := normalizeLanguage(opts.Language)
	if err != nil {
		return opts, err
	}
	opts.Language = lang
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
