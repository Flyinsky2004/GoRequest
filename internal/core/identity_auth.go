package core

import (
	"fmt"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"weak"
)

// CollectionIdentity represents collection.yaml including folder-level Auth.
// Keeping this schema in identity_auth.go avoids directly modifying model.go
// before integration while still enforcing strict YAML field validation.
type CollectionIdentity struct {
	Extensions    map[string]any    `yaml:"extensions,omitempty"`
	SchemaVersion int               `yaml:"schemaVersion"`
	ID            string            `yaml:"id"`
	Name          string            `yaml:"name"`
	Variables     map[string]string `yaml:"variables,omitempty"`
	Auth          *AuthRef          `yaml:"auth,omitempty"`
}

// AuthLayerStep records one node along the project -> folders -> request
// inheritance chain.
type AuthLayerStep struct {
	Layer string // "project" | "folder" | "request"
	Path  string // "project.yaml", "collections/<dir>/collection.yaml", or request RelPath
	Mode  string // "inherit" | "none" | "ref" | "inline"
	Ref   string // referenced scheme ID when Mode == "ref"
}

// EffectiveAuth is the resolved authentication decision for a request, along
// with full provenance for UI previews and diagnostics.
type EffectiveAuth struct {
	Scheme     *AuthScheme
	SourceKind string // "none" | "project" | "folder" | "request"
	SourcePath string
	Mode       string // "none" | "ref" | "inline"
	RefID      string
	Chain      []AuthLayerStep
}

type projectFolderAuthState struct {
	byDir     map[string]*AuthRef
	baselines map[string]string
}

var (
	folderAuthMu        sync.RWMutex
	folderAuthByProject = map[weak.Pointer[Project]]*projectFolderAuthState{}
)

func folderStateFor(p *Project, create bool) *projectFolderAuthState {
	if p == nil {
		return nil
	}
	key := weak.Make(p)
	folderAuthMu.RLock()
	st := folderAuthByProject[key]
	folderAuthMu.RUnlock()
	if st != nil || !create {
		return st
	}

	folderAuthMu.Lock()
	defer folderAuthMu.Unlock()
	if st = folderAuthByProject[key]; st != nil {
		return st
	}
	st = &projectFolderAuthState{
		byDir:     map[string]*AuthRef{},
		baselines: map[string]string{},
	}
	folderAuthByProject[key] = st
	runtime.AddCleanup(p, func(k weak.Pointer[Project]) {
		folderAuthMu.Lock()
		delete(folderAuthByProject, k)
		folderAuthMu.Unlock()
	}, key)
	return st
}

// SetFolderAuth sets or clears (nil / Mode == "") the in-memory folder-level
// auth reference for folderRel (e.g. "users" or "collections/users").
func (p *Project) SetFolderAuth(folderRel string, auth *AuthRef) error {
	if p == nil {
		return configErrorf("nil project")
	}
	rel, err := sanitizeCollectionRel(folderRel)
	if err != nil {
		return err
	}
	if rel == "" {
		return configErrorf("root collections directory uses project defaults.auth, not folder auth")
	}
	dir := collectionsDirOf(rel)
	st := folderStateFor(p, true)

	folderAuthMu.Lock()
	if auth == nil || auth.Mode == "" || auth.Mode == "inherit" {
		delete(st.byDir, dir)
	} else {
		st.byDir[dir] = CloneAuthRef(auth)
	}
	folderAuthMu.Unlock()
	return nil
}

// FolderAuth returns a copy of the configured AuthRef for folderRel, or nil
// when the folder inherits from its parent.
func (p *Project) FolderAuth(folderRel string) *AuthRef {
	if p == nil {
		return nil
	}
	rel, err := sanitizeCollectionRel(folderRel)
	if err != nil || rel == "" {
		return nil
	}
	return p.folderAuthForDir(collectionsDirOf(rel))
}

// FolderAuthBaseline returns the last read/saved digest of folderRel's
// collection.yaml if known.
func (p *Project) FolderAuthBaseline(folderRel string) string {
	if p == nil {
		return ""
	}
	rel, err := sanitizeCollectionRel(folderRel)
	if err != nil || rel == "" {
		return ""
	}
	st := folderStateFor(p, false)
	if st == nil {
		return ""
	}
	folderAuthMu.RLock()
	defer folderAuthMu.RUnlock()
	return st.baselines[collectionsDirOf(rel)+"/collection.yaml"]
}

func (p *Project) folderAuthForDir(dir string) *AuthRef {
	st := folderStateFor(p, false)
	if st == nil {
		return nil
	}
	folderAuthMu.RLock()
	defer folderAuthMu.RUnlock()
	return CloneAuthRef(st.byDir[dir])
}

// LoadFolderIdentities scans collections/**/collection.yaml with strict
// CollectionIdentity decoding (supporting both variables and folder auth),
// refreshes inherited collection variables on loaded requests, and revalidates
// project + identity issues.
func (p *Project) LoadFolderIdentities() ([]Issue, error) {
	if p == nil || p.Store == nil {
		return nil, configErrorf("project has no store")
	}
	colFiles, err := p.Store.List("collections", func(n string) bool { return n == "collection.yaml" })
	if err != nil {
		return nil, fmt.Errorf("listing collections: %w", err)
	}

	st := folderStateFor(p, true)
	newByDir := map[string]*AuthRef{}
	newBaselines := map[string]string{}
	validPaths := map[string]bool{}
	var parseIssues []Issue

	if p.colByDir == nil {
		p.colByDir = map[string]*Collection{}
	}

	for _, rel := range colFiles {
		data, digest, err := p.Store.Read(rel)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", rel, err)
		}
		var ci CollectionIdentity
		if err := decodeStrict(data, &ci); err != nil {
			parseIssues = append(parseIssues, Issue{
				Level:   "error",
				Code:    "collection-parse",
				Path:    rel,
				Message: err.Error(),
			})
			continue
		}
		validPaths[rel] = true
		newBaselines[rel] = digest
		dir := filepath.ToSlash(filepath.Dir(rel))
		p.colByDir[dir] = &Collection{
			SchemaVersion: ci.SchemaVersion,
			ID:            ci.ID,
			Name:          ci.Name,
			Variables:     ci.Variables,
			Auth:          CloneAuthRef(ci.Auth),
			Extensions:    ci.Extensions,
		}
		if ci.Auth != nil && ci.Auth.Mode != "" && ci.Auth.Mode != "inherit" {
			newByDir[dir] = CloneAuthRef(ci.Auth)
		}
	}

	folderAuthMu.Lock()
	st.byDir = newByDir
	st.baselines = newBaselines
	folderAuthMu.Unlock()

	// Replace stale collection-parse findings from OpenProject for files that
	// decoded cleanly under CollectionIdentity, while keeping real parse errors.
	filtered := make([]Issue, 0, len(p.loadIssues)+len(parseIssues))
	for _, iss := range p.loadIssues {
		if iss.Code == "collection-parse" && (validPaths[iss.Path] || containsPathIssue(parseIssues, iss.Path)) {
			continue
		}
		filtered = append(filtered, iss)
	}
	filtered = append(filtered, parseIssues...)
	p.loadIssues = filtered

	for _, entry := range p.Requests {
		entry.CollectionVars = p.collectionVarsFor(entry.RelPath, p.colByDir)
	}
	p.rebuildTree()
	return p.Issues, nil
}

func containsPathIssue(issues []Issue, path string) bool {
	for _, iss := range issues {
		if iss.Path == path {
			return true
		}
	}
	return false
}

// SaveFolderAuth persists folder-level auth (or omits `auth` when nil/inherit)
// to collections/<folderRel>/collection.yaml with optimistic baseline checking,
// preserving existing folder ID, Name, and Variables.
func (p *Project) SaveFolderAuth(folderRel string, auth *AuthRef, baseline string) (string, error) {
	if p == nil || p.Store == nil {
		return "", configErrorf("project has no store")
	}
	rel, err := sanitizeCollectionRel(folderRel)
	if err != nil {
		return "", err
	}
	if rel == "" {
		return "", configErrorf("root collections directory cannot save collection.yaml auth")
	}
	dir := collectionsDirOf(rel)
	colRel := dir + "/collection.yaml"

	ci := CollectionIdentity{
		SchemaVersion: SupportedSchemaVersion,
		ID:            filepath.Base(dir),
		Name:          folderDisplayName(dir),
	}
	checkBaseline := baseline
	if p.Store.Exists(colRel) {
		existing, diskDigest, err := p.Store.Read(colRel)
		if err != nil {
			return "", fmt.Errorf("reading %s: %w", colRel, err)
		}
		if checkBaseline == "" {
			if known := p.FolderAuthBaseline(rel); known != "" {
				checkBaseline = known
			} else {
				checkBaseline = diskDigest
			}
		}
		var parsed CollectionIdentity
		if err := decodeStrict(existing, &parsed); err != nil {
			return "", &ConfigError{Err: fmt.Errorf("%s: %w", colRel, err)}
		}
		ci = parsed
		if ci.SchemaVersion == 0 {
			ci.SchemaVersion = SupportedSchemaVersion
		}
	} else if p.colByDir != nil && p.colByDir[dir] != nil {
		c := p.colByDir[dir]
		if c.ID != "" {
			ci.ID = c.ID
		}
		if c.Name != "" {
			ci.Name = c.Name
		}
		ci.Variables = c.Variables
	}

	var normalized *AuthRef
	if auth != nil && auth.Mode != "" && auth.Mode != "inherit" {
		if auth.Mode == "inline" {
			if err := validateAuthScheme(auth.Inline); err != nil {
				return "", &ConfigError{Err: err}
			}
		}
		normalized = CloneAuthRef(auth)
	}
	ci.Auth = normalized

	data, err := encodeYAML(&ci)
	if err != nil {
		return "", err
	}
	if err := p.Store.SaveChecked(colRel, data, checkBaseline); err != nil {
		return "", err
	}
	digest := Digest(data)

	if p.colByDir == nil {
		p.colByDir = map[string]*Collection{}
	}
	p.colByDir[dir] = &Collection{
		SchemaVersion: ci.SchemaVersion,
		ID:            ci.ID,
		Name:          ci.Name,
		Variables:     ci.Variables,
		Auth:          CloneAuthRef(ci.Auth),
		Extensions:    ci.Extensions,
	}

	st := folderStateFor(p, true)
	folderAuthMu.Lock()
	if normalized == nil {
		delete(st.byDir, dir)
	} else {
		st.byDir[dir] = normalized
	}
	st.baselines[colRel] = digest
	folderAuthMu.Unlock()

	p.rebuildTree()
	return digest, nil
}

// ResolveEffectiveAuth resolves authentication across the full hierarchy:
// project defaults.auth -> ancestor folders (outermost to innermost) -> request.
// Omitting auth (nil or Mode == "") inherits; Mode == "none" explicitly clears
// any ancestor auth unless overridden by a deeper folder or the request itself.
func (p *Project) ResolveEffectiveAuth(entry *RequestEntry, reqOverride *Request) (*EffectiveAuth, error) {
	req := reqOverride
	if req == nil && entry != nil {
		req = entry.Request
	}
	var relPath string
	if entry != nil {
		relPath = entry.RelPath
	}

	eff := &EffectiveAuth{
		SourceKind: "none",
		Mode:       "none",
	}

	type winner struct {
		sourceKind string
		sourcePath string
		mode       string
		refID      string
		inline     *AuthScheme
	}
	win := winner{sourceKind: "none", mode: "none"}

	// 1. Project defaults.auth
	var defAuth string
	if p != nil && p.File != nil && p.File.Defaults != nil {
		defAuth = strings.TrimSpace(p.File.Defaults.Auth)
	}
	switch defAuth {
	case "":
		eff.Chain = append(eff.Chain, AuthLayerStep{
			Layer: "project",
			Path:  "project.yaml",
			Mode:  "inherit",
		})
	case "none":
		eff.Chain = append(eff.Chain, AuthLayerStep{
			Layer: "project",
			Path:  "project.yaml",
			Mode:  "none",
		})
		win = winner{sourceKind: "project", sourcePath: "project.yaml", mode: "none"}
	default:
		eff.Chain = append(eff.Chain, AuthLayerStep{
			Layer: "project",
			Path:  "project.yaml",
			Mode:  "ref",
			Ref:   defAuth,
		})
		win = winner{sourceKind: "project", sourcePath: "project.yaml", mode: "ref", refID: defAuth}
	}

	// 2. Ancestor folders from outermost to innermost
	for _, dir := range ancestorCollectionDirs(relPath) {
		colPath := dir + "/collection.yaml"
		fa := p.folderAuthForDir(dir)
		if fa == nil || fa.Mode == "" || fa.Mode == "inherit" {
			eff.Chain = append(eff.Chain, AuthLayerStep{
				Layer: "folder",
				Path:  colPath,
				Mode:  "inherit",
			})
			continue
		}
		eff.Chain = append(eff.Chain, AuthLayerStep{
			Layer: "folder",
			Path:  colPath,
			Mode:  fa.Mode,
			Ref:   fa.Ref,
		})
		win = winner{
			sourceKind: "folder",
			sourcePath: colPath,
			mode:       fa.Mode,
			refID:      fa.Ref,
			inline:     fa.Inline,
		}
	}

	// 3. Request-level auth
	if req != nil {
		if req.Auth == nil || req.Auth.Mode == "" || req.Auth.Mode == "inherit" {
			eff.Chain = append(eff.Chain, AuthLayerStep{
				Layer: "request",
				Path:  relPath,
				Mode:  "inherit",
			})
		} else {
			eff.Chain = append(eff.Chain, AuthLayerStep{
				Layer: "request",
				Path:  relPath,
				Mode:  req.Auth.Mode,
				Ref:   req.Auth.Ref,
			})
			win = winner{
				sourceKind: "request",
				sourcePath: relPath,
				mode:       req.Auth.Mode,
				refID:      req.Auth.Ref,
				inline:     req.Auth.Inline,
			}
		}
	}

	eff.SourceKind = win.sourceKind
	eff.SourcePath = win.sourcePath
	eff.Mode = win.mode
	eff.RefID = win.refID

	switch win.mode {
	case "", "none":
		eff.Mode = "none"
		eff.Scheme = nil
		eff.RefID = ""
		return eff, nil
	case "inline":
		eff.Scheme = win.inline
		return eff, nil
	case "ref":
		if p == nil {
			return nil, configErrorf("auth %q not found", win.refID)
		}
		s, ok := p.AuthSchemes[win.refID]
		if !ok {
			if win.sourceKind == "project" {
				return nil, configErrorf("default auth %q not found", win.refID)
			}
			return nil, configErrorf("auth %q not found", win.refID)
		}
		eff.Scheme = s
		return eff, nil
	default:
		return nil, configErrorf("unknown auth mode %q", win.mode)
	}
}

// ValidateIdentity checks project default auth, reusable auth schemes, folder
// auth definitions, and environment secret declarations without network access
// or exposing secret values.
func (p *Project) ValidateIdentity() []Issue {
	if p == nil {
		return nil
	}
	var issues []Issue
	add := func(code, path, msg string) {
		issues = append(issues, Issue{Level: "error", Code: code, Path: path, Message: msg})
	}

	if p.File != nil && p.File.Defaults != nil {
		def := strings.TrimSpace(p.File.Defaults.Auth)
		if def != "" && def != "none" {
			if _, ok := p.AuthSchemes[def]; !ok {
				add("default-auth-ref", "project.yaml", fmt.Sprintf("default auth %q not found", def))
			}
		}
	}

	schemeIDs := make([]string, 0, len(p.AuthSchemes))
	for id := range p.AuthSchemes {
		schemeIDs = append(schemeIDs, id)
	}
	sort.Strings(schemeIDs)
	for _, id := range schemeIDs {
		s := p.AuthSchemes[id]
		if err := validateAuthScheme(s); err != nil {
			add("auth-scheme", "auth/"+id+".auth.yaml", err.Error())
		}
	}

	if st := folderStateFor(p, false); st != nil {
		folderAuthMu.RLock()
		dirs := make([]string, 0, len(st.byDir))
		snapshot := make(map[string]*AuthRef, len(st.byDir))
		for d, a := range st.byDir {
			dirs = append(dirs, d)
			snapshot[d] = CloneAuthRef(a)
		}
		folderAuthMu.RUnlock()

		sort.Strings(dirs)
		for _, dir := range dirs {
			fa := snapshot[dir]
			if fa == nil {
				continue
			}
			colPath := dir + "/collection.yaml"
			switch fa.Mode {
			case "", "none":
			case "ref":
				if _, ok := p.AuthSchemes[fa.Ref]; !ok {
					add("folder-auth-ref", colPath, fmt.Sprintf("auth %q not found", fa.Ref))
				}
			case "inline":
				if err := validateAuthScheme(fa.Inline); err != nil {
					add("folder-auth-inline", colPath, err.Error())
				}
			default:
				add("folder-auth-mode", colPath, fmt.Sprintf("unknown auth mode %q", fa.Mode))
			}
		}
	}

	for _, envID := range p.EnvOrder {
		env := p.Environments[envID]
		if env == nil {
			continue
		}
		envPath := "environments/" + envID + ".yaml"
		if p.EnvEntries != nil && p.EnvEntries[envID] != nil {
			envPath = p.EnvEntries[envID].RelPath
		}
		seen := map[string]bool{}
		for _, sec := range env.Secrets {
			trimmed := strings.TrimSpace(sec)
			if trimmed == "" {
				add("env-secret-name", envPath, "environment declares an empty secret name")
				continue
			}
			if IsReservedNamespace(trimmed) {
				add("env-secret-name", envPath, fmt.Sprintf("environment secret %q must be a bare name without namespace prefix", sec))
				continue
			}
			if seen[trimmed] {
				add("env-secret-dup", envPath, fmt.Sprintf("duplicate secret declaration %q", trimmed))
				continue
			}
			seen[trimmed] = true
		}
	}

	return issues
}
