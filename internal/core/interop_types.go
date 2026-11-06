package core

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// InteropFormat identifies an external or portable format inspected by B08.
type InteropFormat string

const (
	FormatUnknown            InteropFormat = "unknown"
	FormatCURL               InteropFormat = "curl"
	FormatPostmanCollection  InteropFormat = "postman-collection"
	FormatPostmanEnvironment InteropFormat = "postman-environment"
	FormatPortableBundle     InteropFormat = "portable-bundle"
	FormatOpenAPI            InteropFormat = "openapi"
	FormatSwagger2           InteropFormat = "swagger-2.0"
)

// ConflictPolicy controls how an import handles existing files or entity IDs in
// the target project.
type ConflictPolicy string

const (
	// ConflictFail blocks Apply when any target file or ID already exists.
	ConflictFail ConflictPolicy = "fail"
	// ConflictRenameNewID allocates a fresh stable ID and relative path for
	// colliding entities and rewires internal references within the import batch.
	ConflictRenameNewID ConflictPolicy = "rename"
	// ConflictOverwrite stages ChangeUpdate mutations guarded by the current
	// on-disk SHA-256 baseline digest.
	ConflictOverwrite ConflictPolicy = "overwrite"
	// ConflictSkip omits colliding entities while importing non-colliding ones.
	ConflictSkip ConflictPolicy = "skip"
)

// LossSeverity classifies a conversion note, warning, or blocking diagnostic.
type LossSeverity string

const (
	LossInfo             LossSeverity = "info"
	LossWarning          LossSeverity = "warning"
	LossUnsupported      LossSeverity = "unsupported"
	LossSecurityScrubbed LossSeverity = "security-scrubbed"
	LossBlocked          LossSeverity = "blocked"
)

// LossItem describes a feature, option, script, secret, or construct that could
// not be converted 1:1 or was intentionally scrubbed/blocked for safety.
type LossItem struct {
	Code       string       `json:"code" yaml:"code"`
	Severity   LossSeverity `json:"severity" yaml:"severity"`
	SourcePath string       `json:"sourcePath,omitempty" yaml:"sourcePath,omitempty"`
	TargetPath string       `json:"targetPath,omitempty" yaml:"targetPath,omitempty"`
	Field      string       `json:"field,omitempty" yaml:"field,omitempty"`
	Message    string       `json:"message" yaml:"message"`
}

// IDMapping records how an imported entity's source identifier and path map into
// the target project.
type IDMapping struct {
	EntityKind string `json:"entityKind" yaml:"entityKind"` // request | folder | environment | auth | doc | asset | attachment
	SourceID   string `json:"sourceId" yaml:"sourceId"`
	TargetID   string `json:"targetId" yaml:"targetId"`
	SourcePath string `json:"sourcePath,omitempty" yaml:"sourcePath,omitempty"`
	TargetPath string `json:"targetPath" yaml:"targetPath"`
	Action     string `json:"action" yaml:"action"` // create | update | rename | skip | conflict
	Renamed    bool   `json:"renamed,omitempty" yaml:"renamed,omitempty"`
}

// VariableSourceNote explains where an imported variable or secret originates
// and how it is represented in GoRequest without leaking raw secret values.
type VariableSourceNote struct {
	Name          string `json:"name" yaml:"name"`
	Scope         string `json:"scope" yaml:"scope"`   // environment | collection | request-auth | header | bundle
	Source        string `json:"source" yaml:"source"` // postman-env | postman-collection | curl-auth | curl-header | portable
	TargetEnvID   string `json:"targetEnvId,omitempty" yaml:"targetEnvId,omitempty"`
	IsSecret      bool   `json:"isSecret" yaml:"isSecret"`
	MappedRef     string `json:"mappedRef" yaml:"mappedRef"`         // e.g. {{baseUrl}} or {{secret.api_key}}
	MaskedPreview string `json:"maskedPreview" yaml:"maskedPreview"` // value after Redactor scrubbing ("***" for secrets)
}

// ImportPreview summarizes every file mutation, ID allocation, variable/secret
// mapping, and loss diagnostic before any file is written to disk.
type ImportPreview struct {
	Format         InteropFormat        `json:"format" yaml:"format"`
	FormatVersion  string               `json:"formatVersion,omitempty" yaml:"formatVersion,omitempty"`
	Summary        string               `json:"summary" yaml:"summary"`
	ConflictPolicy ConflictPolicy       `json:"conflictPolicy" yaml:"conflictPolicy"`
	Changes        []ChangePreview      `json:"changes" yaml:"changes"`
	IDMappings     []IDMapping          `json:"idMappings,omitempty" yaml:"idMappings,omitempty"`
	Variables      []VariableSourceNote `json:"variables,omitempty" yaml:"variables,omitempty"`
	SecretBindings []string             `json:"secretBindings,omitempty" yaml:"secretBindings,omitempty"`
	LocalFileSlots []string             `json:"localFileSlots,omitempty" yaml:"localFileSlots,omitempty"`
	Losses         []LossItem           `json:"losses,omitempty" yaml:"losses,omitempty"`
	Conflicts      []string             `json:"conflicts,omitempty" yaml:"conflicts,omitempty"`
	CanApply       bool                 `json:"canApply" yaml:"canApply"`
}

// ArchiveLimits configures zip/bundle extraction safety boundaries.
type ArchiveLimits struct {
	MaxArchiveBytes           int64
	MaxEntryBytes             int64
	MaxTotalUncompressedBytes int64
	MaxEntryCount             int
	MaxCompressionRatio       int
}

// DefaultArchiveLimits returns conservative extraction limits against zip bombs
// and resource exhaustion.
func DefaultArchiveLimits() ArchiveLimits {
	return ArchiveLimits{
		MaxArchiveBytes:           64 << 20,  // 64 MiB
		MaxEntryBytes:             16 << 20,  // 16 MiB per entry
		MaxTotalUncompressedBytes: 128 << 20, // 128 MiB total uncompressed
		MaxEntryCount:             2000,
		MaxCompressionRatio:       100,
	}
}

func (l ArchiveLimits) withDefaults() ArchiveLimits {
	def := DefaultArchiveLimits()
	if l.MaxArchiveBytes <= 0 {
		l.MaxArchiveBytes = def.MaxArchiveBytes
	}
	if l.MaxEntryBytes <= 0 {
		l.MaxEntryBytes = def.MaxEntryBytes
	}
	if l.MaxTotalUncompressedBytes <= 0 {
		l.MaxTotalUncompressedBytes = def.MaxTotalUncompressedBytes
	}
	if l.MaxEntryCount <= 0 {
		l.MaxEntryCount = def.MaxEntryCount
	}
	if l.MaxCompressionRatio <= 0 {
		l.MaxCompressionRatio = def.MaxCompressionRatio
	}
	return l
}

// ImportOptions configures staging for cURL, Postman, or Portable Bundle imports.
type ImportOptions struct {
	// TargetFolderRel is an optional folder path inside .gorequest/collections
	// (e.g. "collections/imported" or "imported"). Empty means "collections".
	TargetFolderRel string
	// DefaultRequestName overrides the generated request name for single-request imports.
	DefaultRequestName string
	// TargetEnvID specifies the environment ID to create/update when importing
	// collection variables or Postman environments.
	TargetEnvID string
	// ConflictPolicy controls collision handling (defaults to ConflictFail).
	ConflictPolicy ConflictPolicy
	// ScrubSecretsToRefs converts literal inline credentials (Bearer tokens, Basic
	// passwords, API keys, secret variables) into {{secret.<name>}} references
	// and scrubs their values through Redactor. Defaults to true when ScrubSecretsSet is false.
	ScrubSecretsToRefs bool
	ScrubSecretsSet    bool
	// ArchiveLimits overrides default zip safety thresholds when importing bundles.
	ArchiveLimits ArchiveLimits
	// Redactor is an optional pre-configured Redactor; if nil, a fresh Redactor is created.
	Redactor *Redactor
}

func (o ImportOptions) normalized() ImportOptions {
	if o.ConflictPolicy == "" {
		o.ConflictPolicy = ConflictFail
	}
	if !o.ScrubSecretsSet {
		o.ScrubSecretsToRefs = true
	}
	o.ArchiveLimits = o.ArchiveLimits.withDefaults()
	if o.Redactor == nil {
		o.Redactor = NewRedactor()
	}
	return o
}

// ErrOpenAPIRoutedToA03 is returned when an OpenAPI or Swagger specification is
// passed to the B08 basic interoperability importer.
var ErrOpenAPIRoutedToA03 = errors.New("openapi/swagger specification conversion belongs to task A03 and is not handled by B08 basic interop")

// ErrImportBlocked indicates that the staged import plan cannot be applied due
// to conflicts, unsupported versions, or security blocks.
var ErrImportBlocked = errors.New("import plan cannot be applied")

// ImportPlan holds a staged, previewed import backed by a B01 ChangeSet.
// No files are written until Apply() is called.
type ImportPlan struct {
	project      *Project
	changeSet    *ChangeSet
	rootWrites   []stagedRootFile
	preview      ImportPreview
	redactor     *Redactor
	applied      bool
	importedReqs []string
}

type stagedRootFile struct {
	relPath        string
	content        []byte
	existedBefore  bool
	previousBytes  []byte
	baselineDigest string
}

// Preview returns a deep-safe copy of the staged import preview.
func (p *ImportPlan) Preview() ImportPreview {
	if p == nil {
		return ImportPreview{}
	}
	out := p.preview
	out.Changes = slices.Clone(p.preview.Changes)
	out.IDMappings = slices.Clone(p.preview.IDMappings)
	out.Variables = slices.Clone(p.preview.Variables)
	out.SecretBindings = slices.Clone(p.preview.SecretBindings)
	out.LocalFileSlots = slices.Clone(p.preview.LocalFileSlots)
	out.Losses = slices.Clone(p.preview.Losses)
	out.Conflicts = slices.Clone(p.preview.Conflicts)
	return out
}

// Redactor returns the Redactor instance populated during staging.
func (p *ImportPlan) Redactor() *Redactor {
	if p == nil {
		return nil
	}
	return p.redactor
}

// ImportedRequestIDs returns the final request IDs staged in this plan.
func (p *ImportPlan) ImportedRequestIDs() []string {
	if p == nil {
		return nil
	}
	return slices.Clone(p.importedReqs)
}

// SetBeforeWriteFaultHook installs a fault-injection hook on the underlying
// ChangeSet for transactional rollback testing.
func (p *ImportPlan) SetBeforeWriteFaultHook(fn func(stepIndex int, rel string) error) {
	if p != nil && p.changeSet != nil {
		p.changeSet.beforeWriteStep = fn
	}
}

// ImportReceipt represents a committed import and supports full Rollback.
type ImportReceipt struct {
	project       *Project
	changeReceipt *ChangeReceipt
	rootApplied   []stagedRootFile
	preview       ImportPreview
	rolledBack    bool
}

// Preview returns the ImportPreview that was committed.
func (r *ImportReceipt) Preview() ImportPreview {
	if r == nil {
		return ImportPreview{}
	}
	return r.preview
}

// Apply executes the staged ChangeSet atomically, writes any staged public root
// attachments safely, and refreshes the in-memory Project state. If any step
// fails, all changes are rolled back before returning the error.
func (p *ImportPlan) Apply() (*ImportReceipt, error) {
	if p == nil || p.project == nil || p.project.Store == nil {
		return nil, errors.New("import plan has no target project")
	}
	if p.applied {
		return nil, errors.New("import plan already applied")
	}
	if !p.preview.CanApply {
		if p.preview.Format == FormatOpenAPI || p.preview.Format == FormatSwagger2 {
			return nil, ErrOpenAPIRoutedToA03
		}
		if len(p.preview.Conflicts) > 0 {
			return nil, fmt.Errorf("%w: %s", ErrImportBlocked, strings.Join(p.preview.Conflicts, "; "))
		}
		return nil, fmt.Errorf("%w: %s", ErrImportBlocked, p.preview.Summary)
	}

	projRoot := projectRootDir(p.project)

	var csReceipt *ChangeReceipt
	if p.changeSet != nil && len(p.changeSet.changes) > 0 {
		r, err := p.changeSet.Apply()
		if err != nil {
			return nil, err
		}
		csReceipt = r
	}

	var appliedRoot []stagedRootFile
	for _, rw := range p.rootWrites {
		prev, existed, err := applyProjectRootAttachment(projRoot, rw)
		if err != nil {
			for i := len(appliedRoot) - 1; i >= 0; i-- {
				_ = rollbackProjectRootAttachment(projRoot, appliedRoot[i])
			}
			if csReceipt != nil {
				_ = csReceipt.Rollback()
			}
			return nil, err
		}
		rw.existedBefore = existed
		rw.previousBytes = prev
		appliedRoot = append(appliedRoot, rw)
	}

	if err := ReloadProjectInMemory(p.project); err != nil {
		for i := len(appliedRoot) - 1; i >= 0; i-- {
			_ = rollbackProjectRootAttachment(projRoot, appliedRoot[i])
		}
		if csReceipt != nil {
			_ = csReceipt.Rollback()
		}
		_ = ReloadProjectInMemory(p.project)
		return nil, fmt.Errorf("reload project after import: %w", err)
	}

	p.applied = true
	return &ImportReceipt{
		project:       p.project,
		changeReceipt: csReceipt,
		rootApplied:   appliedRoot,
		preview:       p.Preview(),
	}, nil
}

// Rollback reverts all files created or modified by the import and refreshes
// the in-memory Project state back to its pre-import state.
func (r *ImportReceipt) Rollback() error {
	if r == nil {
		return errors.New("nil import receipt")
	}
	if r.rolledBack {
		return errors.New("import receipt already rolled back")
	}
	projRoot := projectRootDir(r.project)
	var firstErr error
	for i := len(r.rootApplied) - 1; i >= 0; i-- {
		if err := rollbackProjectRootAttachment(projRoot, r.rootApplied[i]); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if r.changeReceipt != nil {
		if err := r.changeReceipt.Rollback(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if r.project != nil {
		if err := ReloadProjectInMemory(r.project); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	r.rolledBack = true
	return firstErr
}

func projectRootDir(p *Project) string {
	if p == nil || p.Dir == "" {
		return ""
	}
	if filepath.Base(p.Dir) == ".gorequest" {
		return filepath.Dir(p.Dir)
	}
	return p.Dir
}

// ReloadProjectInMemory reloads disk state into an existing *Project instance
// so callers holding the *Project pointer see updated Requests, Collections,
// Environments, AuthSchemes, Docs, and folder auth mappings immediately.
func ReloadProjectInMemory(p *Project) error {
	if p == nil || p.Dir == "" {
		return nil
	}
	fresh, err := OpenProject(p.Dir)
	if err != nil {
		return err
	}
	p.File = fresh.File
	p.Requests = fresh.Requests
	p.Order = fresh.Order
	p.Collections = fresh.Collections
	p.Environments = fresh.Environments
	p.EnvEntries = fresh.EnvEntries
	p.EnvOrder = fresh.EnvOrder
	p.AuthSchemes = fresh.AuthSchemes
	p.Docs = fresh.Docs
	p.Issues = fresh.Issues
	p.loadIssues = fresh.loadIssues
	p.colByDir = fresh.colByDir
	_, _ = p.LoadFolderIdentities()
	return nil
}

// StageAutoImport inspects raw bytes (and optional filename) and dispatches to
// the appropriate B08 staging pipeline (cURL, Postman Collection, Postman
// Environment, Portable Bundle, or OpenAPI/Swagger A03 triage).
func StageAutoImport(p *Project, data []byte, filename string, opts ImportOptions) (*ImportPlan, error) {
	opts = opts.normalized()
	format, version, err := DetectInteropFormat(data, filename)
	if err != nil {
		return nil, err
	}
	switch format {
	case FormatOpenAPI, FormatSwagger2:
		return buildOpenAPITriagePlan(p, format, version, filename, opts), nil
	case FormatCURL:
		return StageCURLImport(p, string(data), opts)
	case FormatPostmanCollection:
		return StagePostmanCollectionImport(p, data, opts)
	case FormatPostmanEnvironment:
		return StagePostmanEnvironmentImport(p, data, opts)
	case FormatPortableBundle:
		return StagePortableBundleImport(p, data, opts)
	default:
		return nil, fmt.Errorf("unsupported or unrecognized import format (file: %q)", filename)
	}
}

// DetectInteropFormat inspects content bytes and filename to determine the
// interoperability format and version string without executing any code.
func DetectInteropFormat(data []byte, filename string) (InteropFormat, string, error) {
	trimmed := bytes.TrimSpace(data)
	lowerName := strings.ToLower(strings.TrimSpace(filename))

	// 1. Check ZIP magic header (PK\x03\x04 / PK\x05\x06) for Portable Bundle.
	if len(data) >= 4 && data[0] == 'P' && data[1] == 'K' &&
		((data[2] == 3 && data[3] == 4) || (data[2] == 5 && data[3] == 6)) {
		return FormatPortableBundle, "v1", nil
	}
	if strings.HasSuffix(lowerName, ".gorequest-bundle.zip") || strings.HasSuffix(lowerName, ".zip") {
		if len(data) >= 4 && data[0] == 'P' && data[1] == 'K' {
			return FormatPortableBundle, "v1", nil
		}
	}

	if len(trimmed) == 0 {
		return FormatUnknown, "", errors.New("empty import input")
	}

	// 2. Check if it starts with a curl command (ignoring leading comment lines).
	if looksLikeCURLInput(string(trimmed)) {
		return FormatCURL, "cli", nil
	}

	// 3. Try JSON or YAML object inspection for OpenAPI / Swagger / Postman.
	var top map[string]any
	if trimmed[0] == '{' {
		if err := json.Unmarshal(trimmed, &top); err != nil {
			return FormatUnknown, "", fmt.Errorf("invalid JSON input: %w", err)
		}
	} else {
		if err := yaml.Unmarshal(trimmed, &top); err != nil || len(top) == 0 {
			return FormatUnknown, "", errors.New("unrecognized input format: expected cURL command, Postman JSON, or Portable Bundle ZIP")
		}
	}

	// Check OpenAPI 3.x
	if oa, ok := top["openapi"].(string); ok && strings.TrimSpace(oa) != "" {
		return FormatOpenAPI, strings.TrimSpace(oa), nil
	}
	// Check Swagger 2.0
	if sw, ok := top["swagger"].(string); ok && strings.TrimSpace(sw) != "" {
		return FormatSwagger2, strings.TrimSpace(sw), nil
	}

	// Check Postman Environment
	if scope, ok := top["_postman_variable_scope"].(string); ok && strings.EqualFold(scope, "environment") {
		return FormatPostmanEnvironment, "v2.1.0", nil
	}
	if _, hasValues := top["values"].([]any); hasValues {
		if _, hasInfo := top["info"]; !hasInfo {
			if _, hasItem := top["item"]; !hasItem {
				if _, hasName := top["name"].(string); hasName {
					return FormatPostmanEnvironment, "v1.0", nil
				}
			}
		}
	}

	// Check Postman Collection v2.x
	if infoRaw, ok := top["info"].(map[string]any); ok {
		schema, _ := infoRaw["schema"].(string)
		switch {
		case strings.Contains(schema, "/collection/v2.1.0/"):
			return FormatPostmanCollection, "v2.1.0", nil
		case strings.Contains(schema, "/collection/v2.0.0/"):
			return FormatPostmanCollection, "v2.0.0", nil
		case schema != "":
			return FormatPostmanCollection, schema, nil
		}
		if _, hasItem := top["item"]; hasItem {
			return FormatPostmanCollection, "unknown-schema", nil
		}
	}

	// Check Postman Collection v1.0 legacy (top-level "requests" and "order" without "info.schema")
	if _, hasRequests := top["requests"].([]any); hasRequests {
		if _, hasName := top["name"].(string); hasName {
			return FormatPostmanCollection, "v1.0", nil
		}
	}

	return FormatUnknown, "", errors.New("unrecognized JSON/YAML structure: not a supported Postman Collection, Postman Environment, or Portable Bundle")
}

func buildOpenAPITriagePlan(p *Project, format InteropFormat, version, filename string, opts ImportOptions) *ImportPlan {
	specLabel := "OpenAPI " + version
	if format == FormatSwagger2 {
		specLabel = "Swagger " + version
	}
	src := filename
	if src == "" {
		src = specLabel
	}
	msg := fmt.Sprintf("%s specification detected (%s). Full OpenAPI 3.x / Swagger 2.0 contract import, export, and sync belong to task A03; B08 basic interop does not claim OpenAPI conversion.", specLabel, src)
	preview := ImportPreview{
		Format:         format,
		FormatVersion:  version,
		Summary:        msg,
		ConflictPolicy: opts.ConflictPolicy,
		Losses: []LossItem{
			{
				Code:       "openapi-routed-to-a03",
				Severity:   LossBlocked,
				SourcePath: src,
				Message:    msg,
			},
		},
		CanApply: false,
	}
	return &ImportPlan{
		project:  p,
		preview:  preview,
		redactor: opts.Redactor,
	}
}

var slugNonAlphaNum = regexp.MustCompile(`[^a-z0-9]+`)

func sanitizeSlug(raw, fallback string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	s = slugNonAlphaNum.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if s == "" {
		return fallback
	}
	if len(s) > 48 {
		s = strings.Trim(s[:48], "-")
	}
	if s == "" {
		return fallback
	}
	return s
}

func sanitizeSecretSlotName(raw, fallback string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	s = slugNonAlphaNum.ReplaceAllString(s, "_")
	s = strings.Trim(s, "_")
	if s == "" {
		return fallback
	}
	if len(s) > 48 {
		s = strings.Trim(s[:48], "_")
	}
	if s == "" {
		return fallback
	}
	if IsReservedNamespace(s) {
		s = strings.ReplaceAll(s, ".", "_")
	}
	return s
}

func normalizeTargetCollectionFolder(raw string) (string, error) {
	colRel, err := sanitizeCollectionRel(raw)
	if err != nil {
		return "", err
	}
	return collectionsDirOf(colRel), nil
}

// resolveEntityCollision applies the configured ConflictPolicy when a target
// relative path or entity ID already exists in the project or current batch.
func resolveEntityCollision(
	p *Project,
	policy ConflictPolicy,
	entityKind string,
	desiredID string,
	desiredDir string,
	fileSuffix string,
	usedIDs map[string]bool,
	usedPaths map[string]bool,
) (finalID, finalRelPath, action string, baseline string, conflictMsg string) {
	makeRel := func(id string) string {
		switch entityKind {
		case "folder":
			return path.Join(desiredDir, id, "collection.yaml")
		case "environment":
			if p != nil && p.EnvEntries != nil {
				if existing, ok := p.EnvEntries[id]; ok && existing != nil && existing.RelPath != "" {
					return existing.RelPath
				}
			}
			return environmentRelPath(id)
		case "auth":
			return path.Join("auth", id+".auth.yaml")
		case "doc":
			return path.Join("docs", id+".md")
		default:
			return path.Join(desiredDir, id+fileSuffix)
		}
	}

	existsID := func(id string) bool {
		if usedIDs[id] {
			return true
		}
		if p == nil {
			return false
		}
		switch entityKind {
		case "request":
			_, ok := p.Requests[id]
			return ok
		case "environment":
			_, ok := p.Environments[id]
			return ok
		case "auth":
			_, ok := p.AuthSchemes[id]
			return ok
		}
		return false
	}

	existsPath := func(rel string) (bool, string) {
		if usedPaths[rel] {
			return true, ""
		}
		if p != nil && p.Store != nil && p.Store.Exists(rel) {
			if _, digest, err := p.Store.Read(rel); err == nil {
				return true, digest
			}
		}
		return false, ""
	}

	rel := makeRel(desiredID)
	idTaken := existsID(desiredID)
	pathTaken, digest := existsPath(rel)

	if !idTaken && !pathTaken {
		usedIDs[desiredID] = true
		usedPaths[rel] = true
		return desiredID, rel, "create", "", ""
	}

	// If overwriting an existing request by ID whose file lives in a different folder,
	// or at the same path, locate its actual RelPath and BaselineDigest.
	if policy == ConflictOverwrite && !usedPaths[rel] {
		if pathTaken && digest != "" {
			usedIDs[desiredID] = true
			usedPaths[rel] = true
			return desiredID, rel, "update", digest, ""
		}
		if entityKind == "request" && p != nil {
			if existing, ok := p.Requests[desiredID]; ok && existing != nil && !usedPaths[existing.RelPath] {
				usedIDs[desiredID] = true
				usedPaths[existing.RelPath] = true
				return desiredID, existing.RelPath, "update", existing.BaselineDigest, ""
			}
		}
	}

	switch policy {
	case ConflictSkip:
		return desiredID, rel, "skip", digest, ""
	case ConflictOverwrite, ConflictRenameNewID:
		for i := 2; i < 1000; i++ {
			candidateID := fmt.Sprintf("%s-copy-%d", desiredID, i)
			if i == 2 && !strings.HasSuffix(desiredID, "-copy") {
				candidateID = desiredID + "-copy"
			}
			candidateRel := makeRel(candidateID)
			cPathTaken, _ := existsPath(candidateRel)
			if !existsID(candidateID) && !cPathTaken {
				usedIDs[candidateID] = true
				usedPaths[candidateRel] = true
				return candidateID, candidateRel, "rename", "", ""
			}
		}
		return desiredID, rel, "conflict", "", fmt.Sprintf("unable to allocate unique ID for %s %q", entityKind, desiredID)
	default: // ConflictFail
		reason := fmt.Sprintf("%s %q already exists at %s", entityKind, desiredID, filepath.ToSlash(rel))
		return desiredID, rel, "conflict", digest, reason
	}
}

func defaultProjectEnvID(p *Project) string {
	if p == nil {
		return ""
	}
	if _, ok := p.Environments["dev"]; ok {
		return "dev"
	}
	if len(p.EnvOrder) > 0 {
		return p.EnvOrder[0]
	}
	return ""
}
