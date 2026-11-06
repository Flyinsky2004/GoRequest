package core

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// PortableScope identifies the granularity of a native GoRequest portable bundle.
type PortableScope string

const (
	ScopeRequest PortableScope = "request"
	ScopeFolder  PortableScope = "folder"
	ScopeProject PortableScope = "project"
)

const (
	portableManifestName   = "bundle-manifest.yaml"
	portableStorePrefix    = ".gorequest/"
	portableRootFilePrefix = "project-files/"
	maxAssetAttachmentSize = 10 << 20 // 10 MiB
)

// PortableEntityItem describes a GoRequest entity packaged in a portable bundle.
type PortableEntityItem struct {
	Kind    string `json:"kind" yaml:"kind"` // project | request | folder | auth | doc | environment | asset
	ID      string `json:"id" yaml:"id"`
	RelPath string `json:"relPath" yaml:"relPath"`
	SHA256  string `json:"sha256,omitempty" yaml:"sha256,omitempty"`
}

// PortableManifest is the candidate metadata contract stored at bundle-manifest.yaml
// inside a .gorequest-bundle.zip archive.
type PortableManifest struct {
	SchemaVersion      int                  `json:"schemaVersion" yaml:"schemaVersion"`
	BundleKind         PortableScope        `json:"bundleKind" yaml:"bundleKind"`
	SourceProjectName  string               `json:"sourceProjectName" yaml:"sourceProjectName"`
	RootTargetRel      string               `json:"rootTargetRel,omitempty" yaml:"rootTargetRel,omitempty"`
	Entities           []PortableEntityItem `json:"entities" yaml:"entities"`
	PublicAttachments  []string             `json:"publicAttachments,omitempty" yaml:"publicAttachments,omitempty"`
	RequiredSecrets    []string             `json:"requiredSecrets,omitempty" yaml:"requiredSecrets,omitempty"`
	RequiredLocalSlots []string             `json:"requiredLocalSlots,omitempty" yaml:"requiredLocalSlots,omitempty"`
	ExcludedCategories []string             `json:"excludedCategories" yaml:"excludedCategories"`
}

// PortableExportOptions configures creating a .gorequest-bundle.zip archive.
type PortableExportOptions struct {
	Scope         PortableScope
	TargetRelPath string // required for ScopeRequest and ScopeFolder
}

var markdownAssetLinkPattern = regexp.MustCompile(`!\[[^\]]*\]\(([^)]+)\)`)

// ExportPortableBundle packages a single request, folder subtree, or full
// project into a deterministic .gorequest-bundle.zip byte slice, carrying
// public referenced auth/docs/assets while excluding real secrets, cookies,
// history, device configs, reports, and host file paths.
func ExportPortableBundle(p *Project, opts PortableExportOptions) ([]byte, *PortableManifest, []LossItem, error) {
	if p == nil || p.Store == nil {
		return nil, nil, nil, errors.New("project with store is required")
	}
	scope := opts.Scope
	if scope == "" {
		scope = ScopeProject
	}

	var (
		losses          []LossItem
		requiredSecrets []string
		requiredSlots   []string
		storeEntries    = make(map[string][]byte) // relPath under .gorequest -> bytes
		rootAttachments = make(map[string][]byte) // relPath under project root -> bytes
		entities        []PortableEntityItem
	)

	addSecretName := func(name string) {
		name = strings.TrimSpace(name)
		if name != "" && !slices.Contains(requiredSecrets, name) {
			requiredSecrets = append(requiredSecrets, name)
		}
	}
	addLocalSlot := func(slot, sourceRel, field string) {
		slot = strings.TrimSpace(slot)
		if slot == "" {
			return
		}
		if !slices.Contains(requiredSlots, slot) {
			requiredSlots = append(requiredSlots, slot)
		}
		losses = append(losses, LossItem{
			Code:       "portable-export-local-slot-preserved",
			Severity:   LossSecurityScrubbed,
			SourcePath: sourceRel,
			Field:      field,
			Message:    fmt.Sprintf("preserved logical file slot local:%s without exporting host machine path", slot),
		})
	}

	collectTemplateSecrets := func(s string) {
		for _, sub := range templatePattern.FindAllStringSubmatch(s, -1) {
			if len(sub) >= 2 {
				expr := strings.TrimSpace(sub[1])
				if strings.HasPrefix(expr, "secret.") {
					addSecretName(strings.TrimSpace(strings.TrimPrefix(expr, "secret.")))
				}
			}
		}
	}

	includeDocAndAssets := func(docRel string) {
		normDoc := strings.Trim(filepath.ToSlash(strings.TrimSpace(docRel)), "/")
		if normDoc == "" {
			return
		}
		if !strings.HasPrefix(normDoc, "docs/") {
			normDoc = path.Join("docs", filepath.Base(normDoc))
		}
		if _, exists := storeEntries[normDoc]; exists {
			return
		}
		rawDoc, digest, err := p.Store.Read(normDoc)
		if err != nil {
			losses = append(losses, LossItem{
				Code:       "portable-export-missing-doc",
				Severity:   LossWarning,
				SourcePath: normDoc,
				Message:    fmt.Sprintf("referenced documentation %q could not be read: %v", normDoc, err),
			})
			return
		}
		collectTemplateSecrets(string(rawDoc))
		storeEntries[normDoc] = rawDoc
		entities = append(entities, PortableEntityItem{
			Kind:    "doc",
			ID:      strings.TrimSuffix(filepath.Base(normDoc), ".md"),
			RelPath: normDoc,
			SHA256:  digest,
		})

		docDir := path.Dir(normDoc)
		for _, m := range markdownAssetLinkPattern.FindAllStringSubmatch(string(rawDoc), -1) {
			if len(m) < 2 {
				continue
			}
			target := strings.TrimSpace(m[1])
			if strings.Contains(target, "://") || strings.HasPrefix(target, "data:") {
				continue
			}
			resolved := path.Clean(path.Join(docDir, target))
			if strings.HasPrefix(resolved, "assets/") || strings.HasPrefix(resolved, "docs/assets/") {
				if _, ok := storeEntries[resolved]; !ok {
					if assetBytes, assetDigest, err := p.Store.Read(resolved); err == nil {
						storeEntries[resolved] = assetBytes
						entities = append(entities, PortableEntityItem{
							Kind:    "asset",
							ID:      filepath.Base(resolved),
							RelPath: resolved,
							SHA256:  assetDigest,
						})
					}
				}
			}
		}
	}

	includeAuthScheme := func(authID string) {
		authID = strings.TrimSpace(authID)
		if authID == "" || authID == "none" {
			return
		}
		authRel := path.Join("auth", authID+".auth.yaml")
		if _, exists := storeEntries[authRel]; exists {
			return
		}
		scheme, ok := p.AuthSchemes[authID]
		if !ok || scheme == nil {
			losses = append(losses, LossItem{
				Code:       "portable-export-missing-auth-ref",
				Severity:   LossWarning,
				SourcePath: authRel,
				Message:    fmt.Sprintf("referenced auth scheme %q not found in project", authID),
			})
			return
		}
		sanitized := sanitizeAuthSchemeForExport(*scheme, authRel, addSecretName, &losses)
		encoded, err := encodeYAML(&sanitized)
		if err != nil {
			return
		}
		storeEntries[authRel] = encoded
		entities = append(entities, PortableEntityItem{
			Kind:    "auth",
			ID:      sanitized.ID,
			RelPath: authRel,
			SHA256:  Digest(encoded),
		})
	}

	includeAttachmentRef := func(fileRef, localSlot, sourceRel, field string) {
		if localSlot != "" {
			addLocalSlot(localSlot, sourceRel, field)
			return
		}
		fileRef = strings.TrimSpace(fileRef)
		if fileRef == "" {
			return
		}
		if slot, ok := strings.CutPrefix(fileRef, LocalFileRefPrefix); ok {
			addLocalSlot(slot, sourceRel, field)
			return
		}
		if filepath.IsAbs(fileRef) || strings.Contains(fileRef, "\\") || (len(fileRef) >= 2 && fileRef[1] == ':') {
			slot := sanitizeSecretSlotName(strings.TrimSuffix(filepath.Base(fileRef), filepath.Ext(fileRef)), "file")
			addLocalSlot(slot, sourceRel, field)
			return
		}
		cleanRel := path.Clean(filepath.ToSlash(fileRef))
		if cleanRel == "." || cleanRel == ".." || strings.HasPrefix(cleanRel, "../") {
			return
		}
		// Check if attachment lives inside .gorequest/ store first, then project root
		if p.Store.Exists(cleanRel) {
			if _, ok := storeEntries[cleanRel]; !ok {
				if b, d, err := p.Store.Read(cleanRel); err == nil {
					storeEntries[cleanRel] = b
					entities = append(entities, PortableEntityItem{
						Kind:    "asset",
						ID:      filepath.Base(cleanRel),
						RelPath: cleanRel,
						SHA256:  d,
					})
				}
			}
			return
		}
		projRoot := projectRootDir(p)
		if _, already := rootAttachments[cleanRel]; already {
			return
		}
		data, err := readSafeProjectRootFile(projRoot, cleanRel, maxAssetAttachmentSize)
		if err != nil {
			losses = append(losses, LossItem{
				Code:       "portable-export-attachment-skipped",
				Severity:   LossWarning,
				SourcePath: sourceRel,
				Field:      field,
				Message:    fmt.Sprintf("public attachment %q could not be packaged: %v", cleanRel, err),
			})
			return
		}
		rootAttachments[cleanRel] = data
	}

	includeRequestEntry := func(entry *RequestEntry) error {
		if entry == nil || entry.Request == nil {
			return nil
		}
		rel := filepath.ToSlash(entry.RelPath)
		if _, exists := storeEntries[rel]; exists {
			return nil
		}
		cpReq := sanitizeRequestForExport(*entry.Request, rel, addSecretName, collectTemplateSecrets, &losses)
		if cpReq.Auth != nil && cpReq.Auth.Mode == "ref" && cpReq.Auth.Ref != "" {
			includeAuthScheme(cpReq.Auth.Ref)
		}
		for _, docRel := range cpReq.Documentation {
			includeDocAndAssets(docRel)
		}
		if cpReq.HTTP != nil && cpReq.HTTP.Body != nil {
			if tb, _ := InspectBody(cpReq.HTTP.Body); tb != nil {
				if tb.File != "" || tb.LocalFile != "" {
					includeAttachmentRef(tb.File, tb.LocalFile, rel, "http.body.file")
				}
				for _, mp := range tb.MultipartParts {
					if mp.EffectiveKind() == "file" {
						includeAttachmentRef(mp.File, mp.LocalFile, rel, "http.body.multipart."+mp.Name)
					}
				}
			}
		}

		// Also include any ancestor collection.yaml files along this request's folder path
		dir := path.Dir(rel)
		for dir != "." && dir != "" && strings.HasPrefix(dir, "collections") {
			colYAMLRel := path.Join(dir, "collection.yaml")
			if _, ok := storeEntries[colYAMLRel]; !ok && p.Store.Exists(colYAMLRel) {
				if rawCol, _, err := p.Store.Read(colYAMLRel); err == nil {
					var ci CollectionIdentity
					if decodeStrict(rawCol, &ci) == nil {
						sanitizedCI := sanitizeCollectionIdentityForExport(ci, colYAMLRel, addSecretName, collectTemplateSecrets, &losses)
						if sanitizedCI.Auth != nil && sanitizedCI.Auth.Mode == "ref" && sanitizedCI.Auth.Ref != "" {
							includeAuthScheme(sanitizedCI.Auth.Ref)
						}
						colObj := &Collection{
							SchemaVersion: SupportedSchemaVersion,
							ID:            sanitizedCI.ID,
							Name:          sanitizedCI.Name,
							Variables:     sanitizedCI.Variables,
							Auth:          sanitizedCI.Auth,
							Extensions:    sanitizedCI.Extensions,
						}
						if outBytes, err := MergeCollectionYAML(rawCol, colObj); err == nil && len(outBytes) > 0 {
							storeEntries[colYAMLRel] = outBytes
							entities = append(entities, PortableEntityItem{
								Kind:    "folder",
								ID:      filepath.Base(dir),
								RelPath: colYAMLRel,
								SHA256:  Digest(outBytes),
							})
						}
					}
				}
			}
			if dir == "collections" {
				break
			}
			dir = path.Dir(dir)
		}

		var origBytes []byte
		if p.Store.Exists(rel) {
			origBytes, _, _ = p.Store.Read(rel)
		}
		encoded, err := MergeRequestYAML(origBytes, &cpReq)
		if err != nil {
			return err
		}
		storeEntries[rel] = encoded
		entities = append(entities, PortableEntityItem{
			Kind:    "request",
			ID:      cpReq.ID,
			RelPath: rel,
			SHA256:  Digest(encoded),
		})
		return nil
	}

	rootTargetRel := ""
	switch scope {
	case ScopeRequest:
		norm := filepath.ToSlash(strings.TrimSpace(opts.TargetRelPath))
		var targetEntry *RequestEntry
		for _, id := range p.Order {
			r := p.Requests[id]
			if r != nil && (filepath.ToSlash(r.RelPath) == norm || (r.Request != nil && r.Request.ID == norm)) {
				targetEntry = r
				break
			}
		}
		if targetEntry == nil {
			return nil, nil, nil, fmt.Errorf("request %q not found in project", opts.TargetRelPath)
		}
		rootTargetRel = filepath.ToSlash(targetEntry.RelPath)
		if err := includeRequestEntry(targetEntry); err != nil {
			return nil, nil, nil, err
		}

	case ScopeFolder:
		norm := strings.Trim(filepath.ToSlash(strings.TrimSpace(opts.TargetRelPath)), "/")
		if norm == "" {
			return nil, nil, nil, errors.New("folder path is required for folder portable export")
		}
		if !strings.HasPrefix(norm, "collections/") && norm != "collections" {
			norm = "collections/" + norm
		}
		rootTargetRel = norm
		prefix := norm + "/"
		matched := 0
		for _, id := range p.Order {
			r := p.Requests[id]
			if r == nil {
				continue
			}
			rel := filepath.ToSlash(r.RelPath)
			if strings.HasPrefix(rel, prefix) {
				matched++
				if err := includeRequestEntry(r); err != nil {
					return nil, nil, nil, err
				}
			}
		}
		colYAMLRel := path.Join(norm, "collection.yaml")
		if p.Store.Exists(colYAMLRel) {
			if rawCol, d, err := p.Store.Read(colYAMLRel); err == nil {
				matched++
				if _, ok := storeEntries[colYAMLRel]; !ok {
					storeEntries[colYAMLRel] = rawCol
					entities = append(entities, PortableEntityItem{
						Kind:    "folder",
						ID:      filepath.Base(norm),
						RelPath: colYAMLRel,
						SHA256:  d,
					})
				}
			}
		}
		if matched == 0 {
			return nil, nil, nil, fmt.Errorf("folder %q not found or contains no exportable items", opts.TargetRelPath)
		}

	case ScopeProject:
		if p.Store.Exists("project.yaml") {
			projRaw, _, err := p.Store.Read("project.yaml")
			if err == nil && p.File != nil {
				sanitizedPF := sanitizeProjectFileForExport(*p.File, addSecretName, &losses)
				if encodedPF, err := MergeProjectFileYAML(projRaw, &sanitizedPF); err == nil {
					storeEntries["project.yaml"] = encodedPF
					entities = append(entities, PortableEntityItem{
						Kind:    "project",
						ID:      sanitizedPF.ID,
						RelPath: "project.yaml",
						SHA256:  Digest(encodedPF),
					})
				}
			}
		}
		for _, id := range p.Order {
			r := p.Requests[id]
			if err := includeRequestEntry(r); err != nil {
				return nil, nil, nil, err
			}
		}
		var authIDs []string
		for id := range p.AuthSchemes {
			authIDs = append(authIDs, id)
		}
		slices.Sort(authIDs)
		for _, id := range authIDs {
			includeAuthScheme(id)
		}
		for _, d := range p.Docs {
			if d != nil {
				includeDocAndAssets(d.RelPath)
			}
		}
		for _, id := range p.EnvOrder {
			env := p.Environments[id]
			if env == nil {
				continue
			}
			envRel := environmentRelPath(id)
			var origEnv []byte
			if p.EnvEntries != nil && p.EnvEntries[id] != nil {
				envRel = p.EnvEntries[id].RelPath
			}
			if p.Store.Exists(envRel) {
				origEnv, _, _ = p.Store.Read(envRel)
			}
			sanitizedEnv := sanitizeEnvironmentForExport(*env, envRel, addSecretName, &losses)
			encoded, err := MergeEnvironmentYAML(origEnv, &sanitizedEnv)
			if err != nil {
				return nil, nil, nil, err
			}
			storeEntries[envRel] = encoded
			entities = append(entities, PortableEntityItem{
				Kind:    "environment",
				ID:      sanitizedEnv.ID,
				RelPath: envRel,
				SHA256:  Digest(encoded),
			})
		}
	default:
		return nil, nil, nil, fmt.Errorf("unsupported portable export scope %q", scope)
	}

	var pubList []string
	for k := range rootAttachments {
		pubList = append(pubList, k)
	}
	slices.Sort(pubList)
	slices.Sort(requiredSecrets)
	slices.Sort(requiredSlots)
	slices.SortFunc(entities, func(a, b PortableEntityItem) int {
		return strings.Compare(a.RelPath, b.RelPath)
	})

	projName := "GoRequest Project"
	if p.File != nil && p.File.Name != "" {
		projName = p.File.Name
	}

	manifest := &PortableManifest{
		SchemaVersion:      1,
		BundleKind:         scope,
		SourceProjectName:  projName,
		RootTargetRel:      rootTargetRel,
		Entities:           entities,
		PublicAttachments:  pubList,
		RequiredSecrets:    requiredSecrets,
		RequiredLocalSlots: requiredSlots,
		ExcludedCategories: []string{
			"secrets",
			"cookies",
			"history",
			"device-config",
			"reports",
			"host-paths",
		},
	}

	manifestBytes, err := encodeYAML(manifest)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("marshal portable manifest: %w", err)
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	if err := writeDeterministicZipEntry(zw, portableManifestName, manifestBytes); err != nil {
		return nil, nil, nil, err
	}

	var storeKeys []string
	for k := range storeEntries {
		storeKeys = append(storeKeys, k)
	}
	slices.Sort(storeKeys)
	for _, k := range storeKeys {
		if err := writeDeterministicZipEntry(zw, portableStorePrefix+k, storeEntries[k]); err != nil {
			return nil, nil, nil, err
		}
	}

	for _, k := range pubList {
		if err := writeDeterministicZipEntry(zw, portableRootFilePrefix+k, rootAttachments[k]); err != nil {
			return nil, nil, nil, err
		}
	}

	if err := zw.Close(); err != nil {
		return nil, nil, nil, err
	}

	return buf.Bytes(), manifest, losses, nil
}

// ExportPortableBundleToFile writes a portable bundle to targetPath, refusing
// to overwrite an existing file unless overwrite is true.
func ExportPortableBundleToFile(p *Project, targetPath string, overwrite bool, opts PortableExportOptions) (*PortableManifest, []LossItem, error) {
	if strings.TrimSpace(targetPath) == "" {
		return nil, nil, errors.New("export file path is required")
	}
	if !overwrite {
		if _, err := os.Stat(targetPath); err == nil {
			return nil, nil, fmt.Errorf("export target %q already exists", targetPath)
		}
	}
	data, manifest, losses, err := ExportPortableBundle(p, opts)
	if err != nil {
		return nil, nil, err
	}
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return nil, nil, err
	}
	if err := os.WriteFile(targetPath, data, 0o644); err != nil {
		return nil, nil, err
	}
	return manifest, losses, nil
}

// StagePortableBundleImport validates and extracts a .gorequest-bundle.zip
// archive in memory (enforcing zip-bomb, symlink, and path traversal defenses)
// and stages its entities into a ChangeSet on p.
func StagePortableBundleImport(p *Project, bundleBytes []byte, opts ImportOptions) (*ImportPlan, error) {
	if p == nil || p.Store == nil {
		return nil, errors.New("target project is required")
	}
	opts = opts.normalized()

	extracted, err := extractAndValidatePortableZip(bundleBytes, opts.ArchiveLimits)
	if err != nil {
		return nil, err
	}

	manifest := extracted.manifest
	cs := p.Store.NewChangeSet()
	usedIDs := make(map[string]bool)
	usedPaths := make(map[string]bool)

	var (
		mappings       []IDMapping
		variables      []VariableSourceNote
		secretBindings = slices.Clone(manifest.RequiredSecrets)
		localSlots     = slices.Clone(manifest.RequiredLocalSlots)
		losses         []LossItem
		conflicts      []string
		rootWrites     []stagedRootFile
		importedReqs   []string
	)

	for _, sec := range secretBindings {
		variables = append(variables, VariableSourceNote{
			Name:          sec,
			Scope:         "bundle",
			Source:        "portable",
			IsSecret:      true,
			MappedRef:     "{{secret." + sec + "}}",
			MaskedPreview: redactedMask,
		})
	}
	for _, slot := range localSlots {
		losses = append(losses, LossItem{
			Code:     "portable-local-slot-binding-required",
			Severity: LossInfo,
			Field:    FormatLocalFileRef(slot),
			Message:  fmt.Sprintf("bundle references logical file slot %s; bind a local file on this device before sending", FormatLocalFileRef(slot)),
		})
	}

	authRenameMap := make(map[string]string) // oldAuthID -> newAuthID
	docRenameMap := make(map[string]string)  // oldDocRel -> newDocRel

	var storeKeys []string
	for k := range extracted.storeFiles {
		storeKeys = append(storeKeys, k)
	}
	slices.Sort(storeKeys)

	// 1. Stage Auth Schemes first
	for _, rel := range storeKeys {
		if !strings.HasPrefix(rel, "auth/") || !strings.HasSuffix(rel, ".auth.yaml") {
			continue
		}
		raw := extracted.storeFiles[rel]
		var scheme AuthScheme
		if err := decodeStrict(raw, &scheme); err != nil {
			return nil, fmt.Errorf("invalid auth YAML %s in bundle: %w", rel, err)
		}
		origID := scheme.ID
		if origID == "" {
			origID = strings.TrimSuffix(filepath.Base(rel), ".auth.yaml")
		}
		finalID, finalRel, action, baseline, conflictMsg := resolveEntityCollision(
			p,
			opts.ConflictPolicy,
			"auth",
			origID,
			"auth",
			".auth.yaml",
			usedIDs,
			usedPaths,
		)
		if conflictMsg != "" {
			conflicts = append(conflicts, conflictMsg)
		}
		if finalID != origID {
			authRenameMap[origID] = finalID
		}
		scheme.ID = finalID
		sanitized := sanitizeAuthSchemeForExport(scheme, rel, func(s string) {
			if !slices.Contains(secretBindings, s) {
				secretBindings = append(secretBindings, s)
			}
		}, &losses)
		mappings = append(mappings, IDMapping{
			EntityKind: "auth",
			SourceID:   origID,
			TargetID:   finalID,
			SourcePath: rel,
			TargetPath: finalRel,
			Action:     action,
			Renamed:    finalID != origID,
		})
		if action != "conflict" && action != "skip" {
			encoded, err := encodeYAML(&sanitized)
			if err != nil {
				return nil, err
			}
			if action == "update" {
				cs.Update(finalRel, encoded, baseline)
			} else {
				cs.Create(finalRel, encoded)
			}
		}
	}

	// 2. Stage Docs and Assets
	for _, rel := range storeKeys {
		if strings.HasPrefix(rel, "docs/") && strings.HasSuffix(rel, ".md") {
			raw := extracted.storeFiles[rel]
			origID := strings.TrimSuffix(filepath.Base(rel), ".md")
			finalID, finalRel, action, baseline, conflictMsg := resolveEntityCollision(
				p,
				opts.ConflictPolicy,
				"doc",
				origID,
				"docs",
				".md",
				usedIDs,
				usedPaths,
			)
			if conflictMsg != "" {
				conflicts = append(conflicts, conflictMsg)
			}
			if finalRel != rel {
				docRenameMap[rel] = finalRel
			}
			mappings = append(mappings, IDMapping{
				EntityKind: "doc",
				SourceID:   origID,
				TargetID:   finalID,
				SourcePath: rel,
				TargetPath: finalRel,
				Action:     action,
				Renamed:    finalID != origID,
			})
			if action != "conflict" && action != "skip" {
				if action == "update" {
					cs.Update(finalRel, raw, baseline)
				} else {
					cs.Create(finalRel, raw)
				}
			}
		} else if strings.HasPrefix(rel, "assets/") || strings.HasPrefix(rel, "docs/assets/") {
			raw := extracted.storeFiles[rel]
			var baseline string
			if p.Store.Exists(rel) {
				if _, d, err := p.Store.Read(rel); err == nil {
					baseline = d
				}
			}
			action := "create"
			if baseline != "" {
				switch opts.ConflictPolicy {
				case ConflictSkip:
					action = "skip"
				case ConflictFail:
					action = "conflict"
					conflicts = append(conflicts, fmt.Sprintf("asset %s already exists", rel))
				default:
					action = "update"
				}
			}
			mappings = append(mappings, IDMapping{
				EntityKind: "asset",
				SourceID:   filepath.Base(rel),
				TargetID:   filepath.Base(rel),
				SourcePath: rel,
				TargetPath: rel,
				Action:     action,
			})
			if action == "create" {
				cs.changes = append(cs.changes, FileChange{
					Kind:       ChangeCreate,
					RelPath:    rel,
					NewContent: cloneBytes(raw),
					MaxBytes:   maxAssetAttachmentSize,
				})
			} else if action == "update" {
				cs.changes = append(cs.changes, FileChange{
					Kind:             ChangeUpdate,
					RelPath:          rel,
					ExpectedBaseline: baseline,
					NewContent:       cloneBytes(raw),
					MaxBytes:         maxAssetAttachmentSize,
				})
			}
		}
	}

	// 3. Stage Environments
	for _, rel := range storeKeys {
		if !strings.HasPrefix(rel, "environments/") || (!strings.HasSuffix(rel, ".yaml") && !strings.HasSuffix(rel, ".yml")) {
			continue
		}
		raw := extracted.storeFiles[rel]
		var env Environment
		if err := decodeStrict(raw, &env); err != nil {
			return nil, fmt.Errorf("invalid environment YAML %s in bundle: %w", rel, err)
		}
		origID := env.ID
		if origID == "" {
			origID = strings.TrimSuffix(filepath.Base(rel), filepath.Ext(rel))
		}
		finalID, finalRel, action, baseline, conflictMsg := resolveEntityCollision(
			p,
			opts.ConflictPolicy,
			"environment",
			origID,
			"environments",
			".yaml",
			usedIDs,
			usedPaths,
		)
		if conflictMsg != "" {
			conflicts = append(conflicts, conflictMsg)
		}
		env.ID = finalID
		sanitizedEnv := sanitizeEnvironmentForExport(env, rel, func(s string) {
			if !slices.Contains(secretBindings, s) {
				secretBindings = append(secretBindings, s)
			}
		}, &losses)
		for k, v := range sanitizedEnv.Variables {
			variables = append(variables, VariableSourceNote{
				Name:          k,
				Scope:         "environment",
				Source:        "portable",
				TargetEnvID:   finalID,
				IsSecret:      false,
				MappedRef:     "{{" + k + "}}",
				MaskedPreview: opts.Redactor.RedactString(v),
			})
		}
		mappings = append(mappings, IDMapping{
			EntityKind: "environment",
			SourceID:   origID,
			TargetID:   finalID,
			SourcePath: rel,
			TargetPath: finalRel,
			Action:     action,
			Renamed:    finalID != origID,
		})
		if action != "conflict" && action != "skip" {
			var existingBytes []byte
			if action == "update" && p.Store.Exists(finalRel) {
				existingBytes, _, _ = p.Store.Read(finalRel)
			}
			encoded, err := MergeEnvironmentYAML(existingBytes, &sanitizedEnv)
			if err != nil {
				return nil, err
			}
			if action == "update" {
				cs.Update(finalRel, encoded, baseline)
			} else {
				cs.Create(finalRel, encoded)
			}
		}
	}

	targetOverrideDir := ""
	if strings.TrimSpace(opts.TargetFolderRel) != "" {
		td, err := normalizeTargetCollectionFolder(opts.TargetFolderRel)
		if err != nil {
			return nil, err
		}
		targetOverrideDir = td
	}

	// 4. Stage Folder metadata (collection.yaml) and Requests
	for _, rel := range storeKeys {
		if strings.HasPrefix(rel, "collections/") && strings.HasSuffix(rel, "/collection.yaml") {
			raw := extracted.storeFiles[rel]
			folderDir := mapPortableCollectionDir(path.Dir(rel), manifest, targetOverrideDir)
			var ci CollectionIdentity
			if err := decodeStrict(raw, &ci); err != nil {
				return nil, fmt.Errorf("invalid folder metadata %s: %w", rel, err)
			}
			sanitizedCI := sanitizeCollectionIdentityForExport(ci, rel, func(s string) {
				if !slices.Contains(secretBindings, s) {
					secretBindings = append(secretBindings, s)
				}
			}, func(string) {}, &losses)
			if sanitizedCI.Auth != nil && sanitizedCI.Auth.Mode == "ref" {
				if newAuthID, ok := authRenameMap[sanitizedCI.Auth.Ref]; ok {
					sanitizedCI.Auth.Ref = newAuthID
				}
			}
			stageFolderCollectionChange(p, cs, opts.ConflictPolicy, folderDir, filepath.Base(folderDir), sanitizedCI.Name, sanitizedCI.Auth, usedIDs, usedPaths, &mappings, &conflicts)
		}
	}

	for _, rel := range storeKeys {
		if !strings.HasPrefix(rel, "collections/") || !strings.HasSuffix(rel, ".request.yaml") {
			continue
		}
		raw := extracted.storeFiles[rel]
		var req Request
		if err := decodeStrict(raw, &req); err != nil {
			return nil, fmt.Errorf("invalid request YAML %s in bundle: %w", rel, err)
		}
		origID := req.ID
		if origID == "" {
			origID = strings.TrimSuffix(filepath.Base(rel), ".request.yaml")
		}
		destDir := mapPortableCollectionDir(path.Dir(rel), manifest, targetOverrideDir)
		finalID, finalRel, action, baseline, conflictMsg := resolveEntityCollision(
			p,
			opts.ConflictPolicy,
			"request",
			origID,
			destDir,
			".request.yaml",
			usedIDs,
			usedPaths,
		)
		if conflictMsg != "" {
			conflicts = append(conflicts, conflictMsg)
		}
		req.ID = finalID
		if finalID != origID && opts.ConflictPolicy == ConflictRenameNewID {
			req.Name = req.Name + " (Copy)"
		}
		if req.Auth != nil && req.Auth.Mode == "ref" && req.Auth.Ref != "" {
			if newAuthID, ok := authRenameMap[req.Auth.Ref]; ok {
				req.Auth.Ref = newAuthID
			}
		}
		for i, docRel := range req.Documentation {
			if newDocRel, ok := docRenameMap[docRel]; ok {
				req.Documentation[i] = newDocRel
			}
		}
		sanitizedReq := sanitizeRequestForExport(req, rel, func(s string) {
			if !slices.Contains(secretBindings, s) {
				secretBindings = append(secretBindings, s)
			}
		}, func(string) {}, &losses)

		mappings = append(mappings, IDMapping{
			EntityKind: "request",
			SourceID:   origID,
			TargetID:   finalID,
			SourcePath: rel,
			TargetPath: finalRel,
			Action:     action,
			Renamed:    finalID != origID,
		})
		if action != "conflict" && action != "skip" {
			baseYAML := raw
			if action == "update" && p.Store.Exists(finalRel) {
				baseYAML, _, _ = p.Store.Read(finalRel)
			}
			encoded, err := MergeRequestYAML(baseYAML, &sanitizedReq)
			if err != nil {
				return nil, err
			}
			if action == "update" {
				cs.Update(finalRel, encoded, baseline)
			} else {
				cs.Create(finalRel, encoded)
			}
			importedReqs = append(importedReqs, finalID)
		}
	}

	// 5. Stage public project-root attachments
	projRoot := projectRootDir(p)
	var rootKeys []string
	for k := range extracted.rootFiles {
		rootKeys = append(rootKeys, k)
	}
	slices.Sort(rootKeys)
	for _, rel := range rootKeys {
		content := extracted.rootFiles[rel]
		existingBytes, existed, err := checkSafeProjectRootTarget(projRoot, rel)
		if err != nil {
			conflicts = append(conflicts, err.Error())
			continue
		}
		action := "create"
		if existed {
			switch opts.ConflictPolicy {
			case ConflictSkip:
				action = "skip"
			case ConflictFail:
				action = "conflict"
				conflicts = append(conflicts, fmt.Sprintf("attachment %s already exists in project root", rel))
			default:
				action = "update"
			}
		}
		mappings = append(mappings, IDMapping{
			EntityKind: "attachment",
			SourceID:   rel,
			TargetID:   rel,
			SourcePath: portableRootFilePrefix + rel,
			TargetPath: rel,
			Action:     action,
		})
		if action == "create" || action == "update" {
			digest := ""
			if existed {
				digest = Digest(existingBytes)
			}
			rootWrites = append(rootWrites, stagedRootFile{
				relPath:        rel,
				content:        content,
				existedBefore:  existed,
				previousBytes:  existingBytes,
				baselineDigest: digest,
			})
		}
	}

	changePreviews, err := cs.Preview()
	if err != nil {
		return nil, err
	}
	for _, rw := range rootWrites {
		kind := ChangeCreate
		if rw.existedBefore {
			kind = ChangeUpdate
		}
		changePreviews = append(changePreviews, ChangePreview{
			Kind:         kind,
			RelPath:      rw.relPath,
			TargetPath:   rw.relPath,
			BeforeDigest: rw.baselineDigest,
			AfterDigest:  Digest(rw.content),
			BeforeBytes:  len(rw.previousBytes),
			AfterBytes:   len(rw.content),
		})
	}

	canApply := len(conflicts) == 0 && (len(changePreviews) > 0 || len(rootWrites) > 0)
	summary := fmt.Sprintf("Portable Bundle (%s) from %q: %d file change(s)", manifest.BundleKind, manifest.SourceProjectName, len(changePreviews))
	if len(conflicts) > 0 {
		summary = fmt.Sprintf("Portable Bundle (%s) blocked by %d conflict(s)", manifest.BundleKind, len(conflicts))
	}

	return &ImportPlan{
		project:      p,
		changeSet:    cs,
		rootWrites:   rootWrites,
		importedReqs: importedReqs,
		redactor:     opts.Redactor,
		preview: ImportPreview{
			Format:         FormatPortableBundle,
			FormatVersion:  fmt.Sprintf("v%d (%s)", manifest.SchemaVersion, manifest.BundleKind),
			Summary:        summary,
			ConflictPolicy: opts.ConflictPolicy,
			Changes:        changePreviews,
			IDMappings:     mappings,
			Variables:      variables,
			SecretBindings: secretBindings,
			LocalFileSlots: localSlots,
			Losses:         losses,
			Conflicts:      conflicts,
			CanApply:       canApply,
		},
	}, nil
}

// ImportPortableProjectToDir extracts a portable bundle into a brand-new or
// empty project directory on disk, initializing `.gorequest/` and returning the
// loaded *Project.
func ImportPortableProjectToDir(targetDir string, bundleBytes []byte, limits ArchiveLimits) (*Project, *ImportReceipt, error) {
	limits = limits.withDefaults()
	extracted, err := extractAndValidatePortableZip(bundleBytes, limits)
	if err != nil {
		return nil, nil, err
	}
	projName := strings.TrimSpace(extracted.manifest.SourceProjectName)
	if projName == "" {
		projName = "Imported Project"
	}
	p, err := NewProject(targetDir, projName)
	if err != nil {
		return nil, nil, err
	}
	plan, err := StagePortableBundleImport(p, bundleBytes, ImportOptions{
		ConflictPolicy: ConflictOverwrite,
		ArchiveLimits:  limits,
	})
	if err != nil {
		return nil, nil, err
	}
	receipt, err := plan.Apply()
	if err != nil {
		return nil, nil, err
	}
	return p, receipt, nil
}

// --- Internal Portable Bundle extraction & sanitization helpers ---

type extractedPortableBundle struct {
	manifest   PortableManifest
	storeFiles map[string][]byte
	rootFiles  map[string][]byte
}

func extractAndValidatePortableZip(bundleBytes []byte, limits ArchiveLimits) (*extractedPortableBundle, error) {
	limits = limits.withDefaults()
	if int64(len(bundleBytes)) > limits.MaxArchiveBytes {
		return nil, fmt.Errorf("archive size %d bytes exceeds limit %d bytes", len(bundleBytes), limits.MaxArchiveBytes)
	}
	zr, err := zip.NewReader(bytes.NewReader(bundleBytes), int64(len(bundleBytes)))
	if err != nil {
		return nil, fmt.Errorf("open zip archive: %w", err)
	}
	if len(zr.File) > limits.MaxEntryCount {
		return nil, fmt.Errorf("archive entry count %d exceeds limit %d", len(zr.File), limits.MaxEntryCount)
	}

	var totalExtracted int64
	seenEntries := make(map[string]bool, len(zr.File))
	storeFiles := make(map[string][]byte)
	rootFiles := make(map[string][]byte)
	var manifestBytes []byte

	for _, zf := range zr.File {
		rawName := zf.Name
		if err := validateArchiveEntryPath(rawName); err != nil {
			return nil, err
		}
		mode := zf.Mode()
		if mode&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("archive entry %q is a symlink (symlink escape blocked)", rawName)
		}
		if zf.FileInfo().IsDir() || strings.HasSuffix(rawName, "/") {
			continue
		}
		if !mode.IsRegular() {
			return nil, fmt.Errorf("archive entry %q is not a regular file", rawName)
		}

		cleanName := path.Clean(rawName)
		if seenEntries[cleanName] {
			return nil, fmt.Errorf("duplicate archive entry %q", cleanName)
		}
		seenEntries[cleanName] = true

		if zf.UncompressedSize64 > uint64(limits.MaxEntryBytes) {
			return nil, fmt.Errorf("archive entry %q declared size %d exceeds entry limit %d", cleanName, zf.UncompressedSize64, limits.MaxEntryBytes)
		}

		rc, err := zf.Open()
		if err != nil {
			return nil, fmt.Errorf("open archive entry %q: %w", cleanName, err)
		}
		data, readErr := io.ReadAll(io.LimitReader(rc, limits.MaxEntryBytes+1))
		_ = rc.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read archive entry %q: %w", cleanName, readErr)
		}
		entryLen := int64(len(data))
		if entryLen > limits.MaxEntryBytes {
			return nil, fmt.Errorf("archive entry %q uncompressed stream exceeds limit %d bytes", cleanName, limits.MaxEntryBytes)
		}
		totalExtracted += entryLen
		if totalExtracted > limits.MaxTotalUncompressedBytes {
			return nil, fmt.Errorf("archive total uncompressed bytes %d exceeds limit %d", totalExtracted, limits.MaxTotalUncompressedBytes)
		}

		if entryLen > 4096 {
			compSize := int64(zf.CompressedSize64)
			if compSize <= 0 {
				compSize = 1
			}
			if entryLen/compSize > int64(limits.MaxCompressionRatio) {
				return nil, fmt.Errorf("archive entry %q compression ratio (%d:1) exceeds zip-bomb safety threshold (%d:1)", cleanName, entryLen/compSize, limits.MaxCompressionRatio)
			}
		}

		if err := checkForbiddenBundleContent(cleanName, data); err != nil {
			return nil, err
		}

		switch {
		case cleanName == portableManifestName:
			manifestBytes = data
		case strings.HasPrefix(cleanName, portableStorePrefix):
			rel := strings.TrimPrefix(cleanName, portableStorePrefix)
			normRel, err := ValidateProjectRelPath(rel)
			if err != nil {
				return nil, fmt.Errorf("invalid .gorequest entry %q: %w", cleanName, err)
			}
			storeFiles[normRel] = data
		case strings.HasPrefix(cleanName, portableRootFilePrefix):
			rel := strings.TrimPrefix(cleanName, portableRootFilePrefix)
			normRel, err := ValidateProjectRelPath(rel)
			if err != nil {
				return nil, fmt.Errorf("invalid project attachment entry %q: %w", cleanName, err)
			}
			rootFiles[normRel] = data
		default:
			return nil, fmt.Errorf("unexpected archive entry %q outside %s and %s", cleanName, portableStorePrefix, portableRootFilePrefix)
		}
	}

	if len(manifestBytes) == 0 {
		return nil, fmt.Errorf("portable bundle is missing %s", portableManifestName)
	}
	var manifest PortableManifest
	if err := yaml.Unmarshal(manifestBytes, &manifest); err != nil {
		return nil, fmt.Errorf("invalid %s: %w", portableManifestName, err)
	}
	if manifest.SchemaVersion != 1 {
		return nil, fmt.Errorf("unsupported portable bundle schemaVersion %d (expected 1)", manifest.SchemaVersion)
	}

	return &extractedPortableBundle{
		manifest:   manifest,
		storeFiles: storeFiles,
		rootFiles:  rootFiles,
	}, nil
}

func validateArchiveEntryPath(raw string) error {
	if raw == "" {
		return errors.New("empty archive entry path")
	}
	if strings.ContainsRune(raw, 0) {
		return errors.New("archive entry path contains NUL byte")
	}
	if strings.Contains(raw, "\\") {
		return fmt.Errorf("archive entry %q contains backslash path separator", raw)
	}
	if strings.HasPrefix(raw, "/") {
		return fmt.Errorf("archive entry %q must be relative, not absolute", raw)
	}
	if len(raw) >= 2 && raw[1] == ':' {
		return fmt.Errorf("archive entry %q contains Windows drive prefix", raw)
	}
	for _, seg := range strings.Split(strings.TrimSuffix(raw, "/"), "/") {
		if seg == "" || seg == "." || seg == ".." {
			return fmt.Errorf("archive entry %q contains unsafe path traversal segment %q", raw, seg)
		}
	}
	return nil
}

func checkForbiddenBundleContent(cleanName string, data []byte) error {
	base := strings.ToLower(filepath.Base(cleanName))
	switch base {
	case "local-files.json", "config.json", "cookies.json", "history.json", "secrets.json":
		return fmt.Errorf("archive entry %q is a forbidden local/private state file", cleanName)
	}
	lower := strings.ToLower(cleanName)
	for _, forbiddenDir := range []string{"/history/", "/cookies/", "/reports/", "/.git/"} {
		if strings.Contains("/"+lower, forbiddenDir) {
			return fmt.Errorf("archive entry %q is in a forbidden private/report directory", cleanName)
		}
	}
	if strings.HasSuffix(lower, ".yaml") || strings.HasSuffix(lower, ".yml") || strings.HasSuffix(lower, ".md") {
		if containsGitConflictMarkers(data) {
			return fmt.Errorf("archive entry %q contains unresolved Git conflict markers", cleanName)
		}
	}
	return nil
}

func containsGitConflictMarkers(data []byte) bool {
	for _, line := range bytes.Split(data, []byte{'\n'}) {
		if bytes.HasPrefix(line, []byte("<<<<<<< ")) ||
			bytes.HasPrefix(line, []byte("=======")) ||
			bytes.HasPrefix(line, []byte(">>>>>>> ")) {
			return true
		}
	}
	return false
}

func mapPortableCollectionDir(srcDir string, manifest PortableManifest, targetOverrideDir string) string {
	if targetOverrideDir == "" {
		return srcDir
	}
	switch manifest.BundleKind {
	case ScopeRequest:
		return targetOverrideDir
	case ScopeFolder:
		rootRel := strings.Trim(filepath.ToSlash(manifest.RootTargetRel), "/")
		if rootRel != "" && (srcDir == rootRel || strings.HasPrefix(srcDir, rootRel+"/")) {
			suffix := strings.TrimPrefix(srcDir, path.Dir(rootRel))
			return path.Join(targetOverrideDir, strings.TrimPrefix(suffix, "/"))
		}
		return path.Join(targetOverrideDir, strings.TrimPrefix(srcDir, "collections/"))
	default:
		if targetOverrideDir == "collections" {
			return srcDir
		}
		return path.Join(targetOverrideDir, strings.TrimPrefix(srcDir, "collections/"))
	}
}

func sanitizeRequestForExport(
	req Request,
	sourceRel string,
	addSecretName func(string),
	collectTemplateSecrets func(string),
	losses *[]LossItem,
) Request {
	cp := req
	if cp.SchemaVersion == 0 {
		cp.SchemaVersion = SupportedSchemaVersion
	}
	if len(cp.Variables) > 0 {
		cp.Variables = make(map[string]string, len(req.Variables))
		for k, v := range req.Variables {
			collectTemplateSecrets(v)
			if isSensitiveKeyName(k) && v != "" && !strings.Contains(v, "{{") {
				slot := sanitizeSecretSlotName(k, "var_secret")
				addSecretName(slot)
				cp.Variables[k] = "{{secret." + slot + "}}"
				*losses = append(*losses, LossItem{
					Code:       "portable-secret-variable-scrubbed",
					Severity:   LossSecurityScrubbed,
					SourcePath: sourceRel,
					Field:      "variables." + k,
					Message:    fmt.Sprintf("literal credential in request variable %q replaced with {{secret.%s}}", k, slot),
				})
				continue
			}
			cp.Variables[k] = v
		}
	}
	if cp.Auth != nil {
		ac := CloneAuthRef(cp.Auth)
		sanitizeAuthRefFields(ac, sourceRel, addSecretName, collectTemplateSecrets, losses)
		cp.Auth = ac
	}
	if cp.HTTP != nil {
		h := *cp.HTTP
		collectTemplateSecrets(h.URL)
		if len(h.Headers) > 0 {
			h.Headers = slices.Clone(cp.HTTP.Headers)
			for i := range h.Headers {
				collectTemplateSecrets(h.Headers[i].Value)
				if isSensitiveKeyName(h.Headers[i].Name) && h.Headers[i].Value != "" && !strings.Contains(h.Headers[i].Value, "{{") {
					slot := sanitizeSecretSlotName(h.Headers[i].Name, "header_secret")
					addSecretName(slot)
					h.Headers[i].Value = "{{secret." + slot + "}}"
					*losses = append(*losses, LossItem{
						Code:       "portable-secret-header-scrubbed",
						Severity:   LossSecurityScrubbed,
						SourcePath: sourceRel,
						Field:      "http.headers." + h.Headers[i].Name,
						Message:    fmt.Sprintf("literal credential in header %q replaced with {{secret.%s}}", h.Headers[i].Name, slot),
					})
				}
			}
		}
		if len(h.Query) > 0 {
			h.Query = slices.Clone(cp.HTTP.Query)
			for i := range h.Query {
				collectTemplateSecrets(h.Query[i].Value)
			}
		}
		if h.Body != nil {
			h.Body = CloneBody(cp.HTTP.Body)
			if tb, err := InspectBody(h.Body); err == nil && tb != nil {
				collectTemplateSecrets(tb.Text)
				mutatedBody := false
				if tb.File != "" && (filepath.IsAbs(tb.File) || strings.Contains(tb.File, "\\") || (len(tb.File) >= 2 && tb.File[1] == ':')) {
					slot := sanitizeSecretSlotName(strings.TrimSuffix(filepath.Base(tb.File), filepath.Ext(tb.File)), "file")
					tb.File = ""
					tb.LocalFile = slot
					mutatedBody = true
					*losses = append(*losses, LossItem{
						Code:       "portable-host-path-scrubbed",
						Severity:   LossSecurityScrubbed,
						SourcePath: sourceRel,
						Field:      "http.body.file",
						Message:    fmt.Sprintf("machine-specific path in body.file replaced with logical slot local:%s", slot),
					})
				}
				for i := range tb.FormFields {
					collectTemplateSecrets(tb.FormFields[i].Value)
				}
				for i := range tb.MultipartParts {
					collectTemplateSecrets(tb.MultipartParts[i].Value)
					f := tb.MultipartParts[i].File
					if f != "" && (filepath.IsAbs(f) || strings.Contains(f, "\\") || (len(f) >= 2 && f[1] == ':')) {
						slot := sanitizeSecretSlotName(strings.TrimSuffix(filepath.Base(f), filepath.Ext(f)), "file")
						tb.MultipartParts[i].File = ""
						tb.MultipartParts[i].LocalFile = slot
						mutatedBody = true
						*losses = append(*losses, LossItem{
							Code:       "portable-host-path-scrubbed",
							Severity:   LossSecurityScrubbed,
							SourcePath: sourceRel,
							Field:      "http.body.multipart." + tb.MultipartParts[i].Name,
							Message:    fmt.Sprintf("machine-specific path in multipart field %q replaced with logical slot local:%s", tb.MultipartParts[i].Name, slot),
						})
					}
				}
				if mutatedBody {
					if reencoded, err := tb.ToBody(); err == nil {
						h.Body = reencoded
					}
				}
			}
		}
		cp.HTTP = &h
	}
	return cp
}

func sanitizeAuthRefFields(
	ar *AuthRef,
	sourceRel string,
	addSecretName func(string),
	collectTemplateSecrets func(string),
	losses *[]LossItem,
) {
	if ar == nil || ar.Mode != "inline" || ar.Inline == nil {
		return
	}
	s := sanitizeAuthSchemeForExport(*ar.Inline, sourceRel, addSecretName, losses)
	collectTemplateSecrets(s.Token)
	collectTemplateSecrets(s.Password)
	collectTemplateSecrets(s.Value)
	ar.Inline = &s
}

func sanitizeAuthSchemeForExport(
	scheme AuthScheme,
	sourceRel string,
	addSecretName func(string),
	losses *[]LossItem,
) AuthScheme {
	cp := scheme
	if cp.SchemaVersion == 0 {
		cp.SchemaVersion = SupportedSchemaVersion
	}
	scanSecrets := func(s string) {
		for _, sub := range templatePattern.FindAllStringSubmatch(s, -1) {
			if len(sub) >= 2 && strings.HasPrefix(strings.TrimSpace(sub[1]), "secret.") {
				addSecretName(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(sub[1]), "secret.")))
			}
		}
	}
	scanSecrets(cp.Token)
	scanSecrets(cp.Password)
	scanSecrets(cp.Value)

	if cp.Token != "" && !strings.Contains(cp.Token, "{{") {
		slot := sanitizeSecretSlotName(cp.ID+"_token", "bearer_token")
		addSecretName(slot)
		cp.Token = "{{secret." + slot + "}}"
		*losses = append(*losses, LossItem{
			Code:       "portable-auth-secret-scrubbed",
			Severity:   LossSecurityScrubbed,
			SourcePath: sourceRel,
			Field:      "auth.token",
			Message:    fmt.Sprintf("literal bearer token replaced with {{secret.%s}}", slot),
		})
	}
	if cp.Password != "" && !strings.Contains(cp.Password, "{{") {
		slot := sanitizeSecretSlotName(cp.ID+"_password", "basic_password")
		addSecretName(slot)
		cp.Password = "{{secret." + slot + "}}"
		*losses = append(*losses, LossItem{
			Code:       "portable-auth-secret-scrubbed",
			Severity:   LossSecurityScrubbed,
			SourcePath: sourceRel,
			Field:      "auth.password",
			Message:    fmt.Sprintf("literal basic password replaced with {{secret.%s}}", slot),
		})
	}
	if cp.Value != "" && !strings.Contains(cp.Value, "{{") {
		slot := sanitizeSecretSlotName(cp.Name, "api_key")
		addSecretName(slot)
		cp.Value = "{{secret." + slot + "}}"
		*losses = append(*losses, LossItem{
			Code:       "portable-auth-secret-scrubbed",
			Severity:   LossSecurityScrubbed,
			SourcePath: sourceRel,
			Field:      "auth.value",
			Message:    fmt.Sprintf("literal API key value replaced with {{secret.%s}}", slot),
		})
	}
	return cp
}

func sanitizeCollectionIdentityForExport(
	ci CollectionIdentity,
	sourceRel string,
	addSecretName func(string),
	collectTemplateSecrets func(string),
	losses *[]LossItem,
) CollectionIdentity {
	cp := ci
	if cp.SchemaVersion == 0 {
		cp.SchemaVersion = SupportedSchemaVersion
	}
	if len(cp.Variables) > 0 {
		cp.Variables = make(map[string]string, len(ci.Variables))
		for k, v := range ci.Variables {
			collectTemplateSecrets(v)
			if isSensitiveKeyName(k) && v != "" && !strings.Contains(v, "{{") {
				slot := sanitizeSecretSlotName(k, "folder_secret")
				addSecretName(slot)
				cp.Variables[k] = "{{secret." + slot + "}}"
				continue
			}
			cp.Variables[k] = v
		}
	}
	if cp.Auth != nil {
		ac := CloneAuthRef(cp.Auth)
		sanitizeAuthRefFields(ac, sourceRel, addSecretName, collectTemplateSecrets, losses)
		cp.Auth = ac
	}
	return cp
}

func sanitizeProjectFileForExport(
	pf ProjectFile,
	addSecretName func(string),
	losses *[]LossItem,
) ProjectFile {
	cp := pf
	if cp.SchemaVersion == 0 {
		cp.SchemaVersion = SupportedSchemaVersion
	}
	if len(cp.Variables) > 0 {
		cp.Variables = make(map[string]string, len(pf.Variables))
		for k, v := range pf.Variables {
			if isSensitiveKeyName(k) && v != "" && !strings.Contains(v, "{{") {
				slot := sanitizeSecretSlotName(k, "project_secret")
				addSecretName(slot)
				cp.Variables[k] = "{{secret." + slot + "}}"
				*losses = append(*losses, LossItem{
					Code:       "portable-project-secret-scrubbed",
					Severity:   LossSecurityScrubbed,
					SourcePath: "project.yaml",
					Field:      "variables." + k,
					Message:    fmt.Sprintf("sensitive project variable %q replaced with {{secret.%s}}", k, slot),
				})
				continue
			}
			cp.Variables[k] = v
		}
	}
	return cp
}

func sanitizeEnvironmentForExport(
	env Environment,
	sourceRel string,
	addSecretName func(string),
	losses *[]LossItem,
) Environment {
	cp := env
	if cp.SchemaVersion == 0 {
		cp.SchemaVersion = SupportedSchemaVersion
	}
	cp.Variables = make(map[string]string, len(env.Variables))
	cp.Secrets = slices.Clone(env.Secrets)
	for _, s := range cp.Secrets {
		addSecretName(s)
	}
	for k, v := range env.Variables {
		if isSensitiveKeyName(k) && v != "" && !strings.Contains(v, "{{") {
			slot := sanitizeSecretSlotName(k, "secret")
			if !slices.Contains(cp.Secrets, slot) {
				cp.Secrets = append(cp.Secrets, slot)
			}
			addSecretName(slot)
			*losses = append(*losses, LossItem{
				Code:       "portable-env-secret-scrubbed",
				Severity:   LossSecurityScrubbed,
				SourcePath: sourceRel,
				Field:      "variables." + k,
				Message:    fmt.Sprintf("sensitive environment variable %q moved to secrets list without exporting raw value", k),
			})
			continue
		}
		cp.Variables[k] = v
	}
	return cp
}

func writeDeterministicZipEntry(zw *zip.Writer, name string, data []byte) error {
	hdr := &zip.FileHeader{
		Name:   name,
		Method: zip.Deflate,
	}
	hdr.SetMode(0o644)
	w, err := zw.CreateHeader(hdr)
	if err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}

func readSafeProjectRootFile(rootDir, cleanRel string, maxBytes int64) ([]byte, error) {
	if err := checkProjectRootPathNoSymlinks(rootDir, cleanRel); err != nil {
		return nil, err
	}
	abs := filepath.Join(rootDir, filepath.FromSlash(cleanRel))
	info, err := os.Lstat(abs)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("attachment %q is not a regular file", cleanRel)
	}
	if info.Size() > maxBytes {
		return nil, fmt.Errorf("attachment %q size %d exceeds limit %d", cleanRel, info.Size(), maxBytes)
	}
	return os.ReadFile(abs)
}

func checkSafeProjectRootTarget(rootDir, cleanRel string) ([]byte, bool, error) {
	if err := checkProjectRootPathNoSymlinks(rootDir, cleanRel); err != nil {
		return nil, false, err
	}
	abs := filepath.Join(rootDir, filepath.FromSlash(cleanRel))
	info, err := os.Lstat(abs)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, false, fmt.Errorf("target attachment %q is a symlink", cleanRel)
	}
	if info.IsDir() {
		return nil, false, fmt.Errorf("target attachment %q is a directory", cleanRel)
	}
	b, err := os.ReadFile(abs)
	if err != nil {
		return nil, false, err
	}
	return b, true, nil
}

func checkProjectRootPathNoSymlinks(rootDir, cleanRel string) error {
	norm, err := ValidateProjectRelPath(cleanRel)
	if err != nil {
		return err
	}
	cur := filepath.Clean(rootDir)
	for _, seg := range strings.Split(norm, "/") {
		cur = filepath.Join(cur, seg)
		info, err := os.Lstat(cur)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("project attachment path %q traverses symlink at %q", cleanRel, cur)
		}
	}
	return nil
}

func applyProjectRootAttachment(rootDir string, rw stagedRootFile) ([]byte, bool, error) {
	existing, existed, err := checkSafeProjectRootTarget(rootDir, rw.relPath)
	if err != nil {
		return nil, false, err
	}
	if err := NewStore(rootDir).SaveChecked(rw.relPath, rw.content, rw.baselineDigest); err != nil {
		return nil, false, err
	}
	return existing, existed, nil
}

func rollbackProjectRootAttachment(rootDir string, rw stagedRootFile) error {
	if _, _, err := checkSafeProjectRootTarget(rootDir, rw.relPath); err != nil {
		return err
	}
	store := NewStore(rootDir)
	if rw.existedBefore {
		return store.SaveChecked(rw.relPath, rw.previousBytes, Digest(rw.content))
	}
	return store.RemoveChecked(rw.relPath, Digest(rw.content))
}
