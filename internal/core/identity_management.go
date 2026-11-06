package core

import (
	"fmt"
	"net/url"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

// This file provides the reusable identity management primitives that B05's
// engine already exposes for resolution:
//
//   - CRUD for reusable auth schemes under auth/*.auth.yaml (baseline checked,
//     comment preserving, with reference diagnostics before deletion);
//   - comment-preserving folder auth writes and inherited-impact diagnostics;
//   - secret declaration/reference inventory with non-sensitive binding state;
//   - aggregated identity diagnostics for missing refs and unbound secrets;
//   - an exact-scope, one-shot target confirmation token.
//
// None of these helpers resolve or print secret values, and none write plaintext
// credentials into repository files.

// AuthSchemesDir is the project-relative directory holding reusable auth
// schemes.
const AuthSchemesDir = "auth"

const authSchemeSuffix = ".auth.yaml"

// IdentityReference kinds.
const (
	IdentityRefProjectDefault = "project-default"
	IdentityRefFolder         = "folder"
	IdentityRefRequest        = "request"
)

// IdentityReference names one place that points at a reusable auth scheme.
// Path is project-relative; Detail is a display hint (folder dir, request name).
type IdentityReference struct {
	Kind   string `json:"kind"`
	Path   string `json:"path"`
	Detail string `json:"detail,omitempty"`
}

// AuthSchemeInfo summarizes one reusable auth scheme for management UI.
type AuthSchemeInfo struct {
	ID         string              `json:"id"`
	Type       string              `json:"type"`
	Path       string              `json:"path"`
	References []IdentityReference `json:"references,omitempty"`
}

// AuthSchemeInUseError reports that a scheme cannot be deleted because project
// defaults, folders, or requests still reference it.
type AuthSchemeInUseError struct {
	ID         string
	References []IdentityReference
}

func (e *AuthSchemeInUseError) Error() string {
	return fmt.Sprintf("auth scheme %q is still referenced in %d place(s)", e.ID, len(e.References))
}

// ValidateAuthSchemeID validates a reusable auth scheme ID before it becomes a
// filename under auth/.
func ValidateAuthSchemeID(id string) error {
	t := strings.TrimSpace(id)
	if t == "" {
		return configErrorf("auth scheme id is empty")
	}
	if filepath.IsAbs(t) {
		return configErrorf("auth scheme id must not be an absolute path")
	}
	if strings.ContainsAny(t, `/\`) {
		return configErrorf("auth scheme id must not contain path separators")
	}
	if t == "." || t == ".." || strings.Contains(t, "..") {
		return configErrorf("auth scheme id must not contain %q", "..")
	}
	if strings.Contains(t, authSchemeSuffix) {
		return configErrorf("auth scheme id must not include the %q suffix", authSchemeSuffix)
	}
	for _, r := range t {
		if r < 0x20 || r == 0x7f {
			return configErrorf("auth scheme id contains a control character")
		}
		if strings.ContainsRune(`<>:"|?*`, r) {
			return configErrorf("auth scheme id contains an invalid character %q", string(r))
		}
	}
	return nil
}

func (p *Project) authSchemeRelPath(id string) (string, error) {
	if err := ValidateAuthSchemeID(id); err != nil {
		return "", err
	}
	return filepath.ToSlash(filepath.Join(AuthSchemesDir, strings.TrimSpace(id)+authSchemeSuffix)), nil
}

// ListAuthSchemes returns every loaded reusable scheme with the places that
// reference it, sorted by ID.
func (p *Project) ListAuthSchemes() []AuthSchemeInfo {
	if p == nil {
		return nil
	}
	ids := make([]string, 0, len(p.AuthSchemes))
	for id := range p.AuthSchemes {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]AuthSchemeInfo, 0, len(ids))
	for _, id := range ids {
		s := p.AuthSchemes[id]
		typ := ""
		if s != nil {
			typ = strings.ToLower(strings.TrimSpace(s.Type))
		}
		out = append(out, AuthSchemeInfo{
			ID:         id,
			Type:       typ,
			Path:       AuthSchemesDir + "/" + id + authSchemeSuffix,
			References: p.AuthSchemeReferences(id),
		})
	}
	return out
}

// AuthSchemeReferences returns the project-default, folder, and request places
// that reference scheme id. Paths are project-relative and deterministic.
func (p *Project) AuthSchemeReferences(id string) []IdentityReference {
	refID := strings.TrimSpace(id)
	if p == nil || refID == "" {
		return nil
	}
	var refs []IdentityReference
	if p.File != nil && p.File.Defaults != nil && strings.TrimSpace(p.File.Defaults.Auth) == refID {
		refs = append(refs, IdentityReference{
			Kind:   IdentityRefProjectDefault,
			Path:   "project.yaml",
			Detail: "defaults.auth",
		})
	}

	dirs := make([]string, 0, len(p.colByDir))
	for dir := range p.colByDir {
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)
	for _, dir := range dirs {
		c := p.colByDir[dir]
		if c == nil || c.Auth == nil || c.Auth.Mode != "ref" || strings.TrimSpace(c.Auth.Ref) != refID {
			continue
		}
		refs = append(refs, IdentityReference{
			Kind:   IdentityRefFolder,
			Path:   dir + "/collection.yaml",
			Detail: folderDisplayName(dir),
		})
	}

	ids := make([]string, 0, len(p.Requests))
	for reqID := range p.Requests {
		ids = append(ids, reqID)
	}
	sort.Strings(ids)
	for _, reqID := range ids {
		e := p.Requests[reqID]
		if e == nil || e.Request == nil || e.Request.Auth == nil {
			continue
		}
		if e.Request.Auth.Mode != "ref" || strings.TrimSpace(e.Request.Auth.Ref) != refID {
			continue
		}
		refs = append(refs, IdentityReference{
			Kind:   IdentityRefRequest,
			Path:   e.RelPath,
			Detail: e.Request.Name,
		})
	}
	return refs
}

// LoadAuthScheme reads one scheme and returns a deep copy plus its disk digest
// for baseline-checked saving.
func (p *Project) LoadAuthScheme(id string) (*AuthScheme, string, error) {
	if p == nil || p.Store == nil {
		return nil, "", configErrorf("project has no store")
	}
	rel, err := p.authSchemeRelPath(id)
	if err != nil {
		return nil, "", err
	}
	data, digest, err := p.Store.Read(rel)
	if err != nil {
		return nil, "", fmt.Errorf("reading %s: %w", rel, err)
	}
	var s AuthScheme
	if err := decodeStrict(data, &s); err != nil {
		return nil, "", &ConfigError{Err: fmt.Errorf("%s: %w", rel, err)}
	}
	if strings.TrimSpace(s.ID) == "" {
		s.ID = strings.TrimSpace(id)
	}
	if strings.TrimSpace(s.ID) != strings.TrimSpace(id) {
		return nil, "", configErrorf("%s declares id %q, expected %q", rel, s.ID, id)
	}
	return CloneAuthScheme(&s), digest, nil
}

func normalizeAuthSchemeForWrite(s *AuthScheme) (*AuthScheme, error) {
	if s == nil {
		return nil, configErrorf("auth scheme is nil")
	}
	cp := CloneAuthScheme(s)
	cp.ID = strings.TrimSpace(cp.ID)
	if err := ValidateAuthSchemeID(cp.ID); err != nil {
		return nil, err
	}
	cp.Type = strings.ToLower(strings.TrimSpace(cp.Type))
	if cp.Type == "none" {
		cp.Type = ""
	}
	if cp.SchemaVersion == 0 {
		cp.SchemaVersion = SupportedSchemaVersion
	}
	if err := validateAuthScheme(cp); err != nil {
		return nil, &ConfigError{Err: err}
	}
	switch cp.Type {
	case "bearer":
		cp.Username, cp.Password, cp.Name, cp.In, cp.Value = "", "", "", "", ""
	case "basic":
		cp.Token, cp.Name, cp.In, cp.Value = "", "", "", ""
	case "apikey":
		cp.Token, cp.Username, cp.Password = "", "", ""
		cp.In = NormalizeAPIKeyLocation(cp.In)
	default:
		return nil, configErrorf("unsupported auth type %q: use bearer, basic, or apikey", cp.Type)
	}
	return cp, nil
}

// CreateAuthScheme writes a new auth/<id>.auth.yaml. It refuses to overwrite an
// existing file (including one that appeared after the project was loaded).
func (p *Project) CreateAuthScheme(s *AuthScheme) (string, error) {
	if p == nil || p.Store == nil {
		return "", configErrorf("project has no store")
	}
	norm, err := normalizeAuthSchemeForWrite(s)
	if err != nil {
		return "", err
	}
	rel, err := p.authSchemeRelPath(norm.ID)
	if err != nil {
		return "", err
	}
	if p.Store.Exists(rel) || p.AuthSchemes[norm.ID] != nil {
		return "", &ConflictError{Path: rel, Baseline: "", Disk: ""}
	}
	data, err := encodeYAML(norm)
	if err != nil {
		return "", err
	}
	if err := p.Store.SaveChecked(rel, data, ""); err != nil {
		return "", err
	}
	p.AuthSchemes[norm.ID] = norm
	p.rebuildTree()
	return Digest(data), nil
}

// SaveAuthScheme updates an existing scheme, checking baselineDigest against the
// file on disk and preserving comments and unrelated fields.
func (p *Project) SaveAuthScheme(s *AuthScheme, baselineDigest string) (string, error) {
	if p == nil || p.Store == nil {
		return "", configErrorf("project has no store")
	}
	norm, err := normalizeAuthSchemeForWrite(s)
	if err != nil {
		return "", err
	}
	rel, err := p.authSchemeRelPath(norm.ID)
	if err != nil {
		return "", err
	}
	if baselineDigest == "" {
		return "", configErrorf("saving auth scheme %q requires a baseline digest", norm.ID)
	}
	original, diskDigest, err := p.Store.Read(rel)
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", rel, err)
	}
	if diskDigest != baselineDigest {
		return "", &ConflictError{Path: rel, Baseline: baselineDigest, Disk: diskDigest}
	}
	data, err := MergeAuthSchemeYAML(original, norm)
	if err != nil {
		return "", err
	}
	if err := p.Store.SaveChecked(rel, data, baselineDigest); err != nil {
		return "", err
	}
	p.AuthSchemes[norm.ID] = norm
	p.rebuildTree()
	return Digest(data), nil
}

// MergeAuthSchemeYAML updates original scheme YAML while preserving comments,
// key order, and extensions.
func MergeAuthSchemeYAML(original []byte, updated *AuthScheme) ([]byte, error) {
	if updated == nil {
		return nil, configErrorf("auth scheme is nil")
	}
	cp := *updated
	if cp.SchemaVersion == 0 {
		cp.SchemaVersion = SupportedSchemaVersion
	}
	if cp.SchemaVersion != SupportedSchemaVersion {
		return nil, configErrorf("unsupported auth scheme schemaVersion %d (this build writes %d)", cp.SchemaVersion, SupportedSchemaVersion)
	}
	return mergeEntityYAML(EntityAuth, original, &cp)
}

// stageClearSchemeReference stages replacing a reference to scheme id with an
// explicit none at ref's path. It reads the current bytes so the staged update
// is baseline checked and comment preserving.
func (p *Project) stageClearSchemeReference(cs *ChangeSet, ref IdentityReference, schemeID string) error {
	switch ref.Kind {
	case IdentityRefProjectDefault:
		original, digest, err := p.Store.Read("project.yaml")
		if err != nil {
			return err
		}
		var pf ProjectFile
		if err := decodeStrict(original, &pf); err != nil {
			return &ConfigError{Err: fmt.Errorf("project.yaml: %w", err)}
		}
		if pf.Defaults == nil {
			pf.Defaults = &Defaults{}
		}
		def := *pf.Defaults
		def.Auth = "none"
		pf.Defaults = &def
		data, err := MergeProjectFileYAML(original, &pf)
		if err != nil {
			return err
		}
		cs.Update("project.yaml", data, digest)
		return nil

	case IdentityRefFolder:
		original, digest, err := p.Store.Read(ref.Path)
		if err != nil {
			return err
		}
		var ci CollectionIdentity
		if err := decodeStrict(original, &ci); err != nil {
			return &ConfigError{Err: fmt.Errorf("%s: %w", ref.Path, err)}
		}
		if ci.SchemaVersion == 0 {
			ci.SchemaVersion = SupportedSchemaVersion
		}
		ci.Auth = &AuthRef{Mode: "none"}
		col := Collection{
			SchemaVersion: ci.SchemaVersion,
			ID:            ci.ID,
			Name:          ci.Name,
			Variables:     ci.Variables,
			Extensions:    ci.Extensions,
			Auth:          ci.Auth,
		}
		data, err := MergeCollectionYAML(original, &col)
		if err != nil {
			return err
		}
		cs.Update(ref.Path, data, digest)
		return nil

	case IdentityRefRequest:
		original, digest, err := p.Store.Read(ref.Path)
		if err != nil {
			return err
		}
		var req Request
		if err := decodeStrict(original, &req); err != nil {
			return &ConfigError{Err: fmt.Errorf("%s: %w", ref.Path, err)}
		}
		req.Auth = &AuthRef{Mode: "none"}
		data, err := MergeRequestYAML(original, &req)
		if err != nil {
			return err
		}
		cs.Update(ref.Path, data, digest)
		return nil
	}
	return configErrorf("unknown auth reference kind %q", ref.Kind)
}

// DeleteAuthScheme removes auth/<id>.auth.yaml after a baseline check. When the
// scheme is still referenced, deletion returns *AuthSchemeInUseError unless
// clearReferences is true; clearing rewrites every referencing project default,
// folder, and request to an explicit `none` in one atomic ChangeSet so no file
// is left pointing at a missing scheme.
func (p *Project) DeleteAuthScheme(id, baselineDigest string, clearReferences bool) ([]IdentityReference, error) {
	if p == nil || p.Store == nil {
		return nil, configErrorf("project has no store")
	}
	rel, err := p.authSchemeRelPath(id)
	if err != nil {
		return nil, err
	}
	if !p.Store.Exists(rel) {
		return nil, &ConflictError{Path: rel, Baseline: baselineDigest, Disk: ""}
	}
	_, diskDigest, err := p.Store.Read(rel)
	if err != nil {
		return nil, err
	}
	if baselineDigest == "" {
		baselineDigest = diskDigest
	}
	if diskDigest != baselineDigest {
		return nil, &ConflictError{Path: rel, Baseline: baselineDigest, Disk: diskDigest}
	}

	refs := p.AuthSchemeReferences(id)
	if len(refs) > 0 && !clearReferences {
		return refs, &AuthSchemeInUseError{ID: strings.TrimSpace(id), References: refs}
	}

	if len(refs) == 0 {
		if err := p.Store.RemoveChecked(rel, baselineDigest); err != nil {
			return nil, err
		}
		delete(p.AuthSchemes, strings.TrimSpace(id))
		p.rebuildTree()
		return nil, nil
	}

	cs := p.Store.NewChangeSet()
	cs.Delete(rel, baselineDigest)
	for _, ref := range refs {
		if err := p.stageClearSchemeReference(cs, ref, id); err != nil {
			return refs, err
		}
	}
	if _, err := cs.Apply(); err != nil {
		return refs, err
	}

	// Reflect the committed changes in memory.
	delete(p.AuthSchemes, strings.TrimSpace(id))
	if p.File != nil && p.File.Defaults != nil && strings.TrimSpace(p.File.Defaults.Auth) == strings.TrimSpace(id) {
		def := *p.File.Defaults
		def.Auth = "none"
		p.File.Defaults = &def
	}
	st := folderStateFor(p, false)
	for _, ref := range refs {
		switch ref.Kind {
		case IdentityRefFolder:
			dir := filepath.ToSlash(filepath.Dir(ref.Path))
			if c := p.colByDir[dir]; c != nil {
				c.Auth = &AuthRef{Mode: "none"}
			}
			if st != nil {
				folderAuthMu.Lock()
				st.byDir[dir] = &AuthRef{Mode: "none"}
				folderAuthMu.Unlock()
			}
		case IdentityRefRequest:
			for _, e := range p.Requests {
				if e != nil && e.RelPath == ref.Path && e.Request != nil {
					e.Request.Auth = &AuthRef{Mode: "none"}
				}
			}
		}
	}
	p.rebuildTree()
	return refs, nil
}

// SaveFolderAuthPreserving writes folder-level auth to collection.yaml while
// preserving comments, ID/name/variables, and extensions. baselineDigest must
// match the file on disk; when it is empty and the file already exists, the
// disk copy is adopted only when it still matches the loaded in-memory version
// (so an unrelated external edit is reported as a conflict instead of being
// overwritten).
func (p *Project) SaveFolderAuthPreserving(folderRel string, auth *AuthRef, baselineDigest string) (string, error) {
	if p == nil || p.Store == nil {
		return "", configErrorf("project has no store")
	}
	rel, err := sanitizeCollectionRel(folderRel)
	if err != nil {
		return "", err
	}
	if rel == "" {
		return "", configErrorf("root collections directory uses project defaults.auth, not folder auth")
	}
	dir := collectionsDirOf(rel)
	colRel := dir + "/collection.yaml"

	var before *Collection
	col := &Collection{SchemaVersion: SupportedSchemaVersion, ID: filepath.Base(dir), Name: folderDisplayName(dir)}
	if c := p.colByDir[dir]; c != nil {
		// Keep a pristine copy for the baseline-adoption comparison; col is the
		// copy that receives the new auth.
		prior := *c
		before = &prior
		next := *c
		col = &next
	}
	if col.SchemaVersion == 0 {
		col.SchemaVersion = SupportedSchemaVersion
	}
	if strings.TrimSpace(col.ID) == "" {
		col.ID = filepath.Base(dir)
	}
	if strings.TrimSpace(col.Name) == "" {
		col.Name = folderDisplayName(dir)
	}

	normalized, err := normalizeFolderAuthRef(auth)
	if err != nil {
		return "", err
	}
	col.Auth = normalized

	if baselineDigest == "" && p.Store.Exists(colRel) {
		original, digest, err := p.Store.Read(colRel)
		if err != nil {
			return "", err
		}
		var ci CollectionIdentity
		if err := decodeStrict(original, &ci); err != nil {
			return "", &ConfigError{Err: fmt.Errorf("%s: %w", colRel, err)}
		}
		if before == nil || !collectionsEquivalent(*before, ci) {
			return "", &ConflictError{Path: colRel, Baseline: "", Disk: digest}
		}
		baselineDigest = digest
	}

	digest, err := p.SaveCollectionPreserving(dir, col, baselineDigest)
	if err != nil {
		return "", err
	}

	st := folderStateFor(p, true)
	folderAuthMu.Lock()
	if normalized == nil {
		delete(st.byDir, dir)
	} else {
		st.byDir[dir] = CloneAuthRef(normalized)
	}
	st.baselines[colRel] = digest
	folderAuthMu.Unlock()
	p.rebuildTree()
	return digest, nil
}

func normalizeFolderAuthRef(auth *AuthRef) (*AuthRef, error) {
	if auth == nil || auth.Mode == "" || auth.Mode == "inherit" {
		return nil, nil
	}
	cp := CloneAuthRef(auth)
	switch cp.Mode {
	case "none":
		return &AuthRef{Mode: "none"}, nil
	case "ref":
		cp.Ref = strings.TrimSpace(cp.Ref)
		if cp.Ref == "" {
			return nil, configErrorf("folder auth ref requires a scheme id")
		}
		return cp, nil
	case "inline":
		if err := validateAuthScheme(cp.Inline); err != nil {
			return nil, &ConfigError{Err: err}
		}
		if cp.Inline != nil && cp.Inline.SchemaVersion == 0 {
			cp.Inline.SchemaVersion = SupportedSchemaVersion
		}
		return cp, nil
	default:
		return nil, configErrorf("unknown folder auth mode %q", cp.Mode)
	}
}

func collectionsEquivalent(mem Collection, disk CollectionIdentity) bool {
	if strings.TrimSpace(mem.ID) != strings.TrimSpace(disk.ID) {
		return false
	}
	if strings.TrimSpace(mem.Name) != strings.TrimSpace(disk.Name) {
		return false
	}
	if len(mem.Variables) != len(disk.Variables) {
		return false
	}
	for k, v := range mem.Variables {
		if dv, ok := disk.Variables[k]; !ok || dv != v {
			return false
		}
	}
	return authRefsEquivalent(mem.Auth, disk.Auth)
}

func authRefsEquivalent(a, b *AuthRef) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	if a.Mode != b.Mode || strings.TrimSpace(a.Ref) != strings.TrimSpace(b.Ref) {
		return false
	}
	if a.Inline == nil || b.Inline == nil {
		return a.Inline == nil && b.Inline == nil
	}
	return reflect.DeepEqual(a.Inline, b.Inline)
}

// FolderAuthImpact lists requests under folderRel whose effective auth is
// currently sourced from that folder, so clearing or changing folder auth can
// show exactly what will change. Requests with a deeper folder or request-level
// override are not included.
func (p *Project) FolderAuthImpact(folderRel string) []IdentityReference {
	if p == nil {
		return nil
	}
	rel, err := sanitizeCollectionRel(folderRel)
	if err != nil || rel == "" {
		return nil
	}
	dir := collectionsDirOf(rel)
	prefix := dir + "/"
	folderPath := dir + "/collection.yaml"

	ids := make([]string, 0, len(p.Requests))
	for id := range p.Requests {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	var out []IdentityReference
	for _, id := range ids {
		e := p.Requests[id]
		if e == nil || e.Request == nil || !strings.HasPrefix(e.RelPath, prefix) {
			continue
		}
		eff, err := p.ResolveEffectiveAuth(e, nil)
		if err != nil || eff == nil {
			continue
		}
		if eff.SourceKind != "folder" || eff.SourcePath != folderPath {
			continue
		}
		out = append(out, IdentityReference{
			Kind:   IdentityRefRequest,
			Path:   e.RelPath,
			Detail: e.Request.Name,
		})
	}
	return out
}

// SecretReference inventories a declared secret name and the project places
// that reference it, plus non-sensitive binding status when a provider is given.
type SecretReference struct {
	Name    string      `json:"name"`
	Sources []string    `json:"sources"`
	State   SecretState `json:"state,omitempty"`
	Backend string      `json:"backend,omitempty"`
	Detail  string      `json:"detail,omitempty"`
}

// ValidateSecretName validates a bare secret name (without the secret. prefix)
// before it is written to an OS credential store.
func ValidateSecretName(name string) error {
	t := strings.TrimSpace(name)
	if t == "" {
		return configErrorf("secret name is empty")
	}
	if IsReservedNamespace(t) {
		return configErrorf("secret name %q must be a bare name without a namespace prefix", name)
	}
	if strings.ContainsAny(t, "{}") {
		return configErrorf("secret name %q must not contain template braces", name)
	}
	if t == "." || t == ".." || strings.Contains(t, "..") || strings.ContainsAny(t, `/\`) || filepath.IsAbs(t) {
		return configErrorf("secret name %q must be a bare name without path separators", name)
	}
	for _, r := range t {
		if r < 0x20 || r == 0x7f {
			return configErrorf("secret name contains a control character")
		}
	}
	if len(t) > 128 {
		return configErrorf("secret name %q is too long (max 128 bytes)", name)
	}
	return nil
}

func authSchemeTemplateStrings(s *AuthScheme) []string {
	if s == nil {
		return nil
	}
	switch strings.ToLower(strings.TrimSpace(s.Type)) {
	case "bearer":
		return []string{s.Token}
	case "basic":
		return []string{s.Username, s.Password}
	case "apikey":
		return []string{s.Name, s.Value}
	default:
		return nil
	}
}

func authRefTemplateStrings(a *AuthRef) []string {
	if a == nil || a.Mode != "inline" {
		return nil
	}
	return authSchemeTemplateStrings(a.Inline)
}

func collectSecretNames(dst map[string]map[string]bool, source string, templates []string) {
	for _, tpl := range templates {
		for _, name := range TemplateNames(tpl) {
			sec, ok := strings.CutPrefix(name, "secret.")
			if !ok {
				continue
			}
			sec = strings.TrimSpace(sec)
			if sec == "" {
				continue
			}
			if dst[sec] == nil {
				dst[sec] = map[string]bool{}
			}
			dst[sec][source] = true
		}
	}
}

// SecretReferences returns every declared secret with its reference sources,
// resolved through secrets for non-sensitive binding status.
func (p *Project) SecretReferences(secrets SecretProvider) []SecretReference {
	if p == nil {
		return nil
	}
	acc := map[string]map[string]bool{}

	envIDs := make([]string, 0, len(p.Environments))
	for id := range p.Environments {
		envIDs = append(envIDs, id)
	}
	sort.Strings(envIDs)
	for _, id := range envIDs {
		env := p.Environments[id]
		if env == nil {
			continue
		}
		for _, name := range env.Secrets {
			trimmed := strings.TrimSpace(name)
			if trimmed == "" {
				continue
			}
			if acc[trimmed] == nil {
				acc[trimmed] = map[string]bool{}
			}
			acc[trimmed]["environment:"+id] = true
		}
	}

	schemeIDs := make([]string, 0, len(p.AuthSchemes))
	for id := range p.AuthSchemes {
		schemeIDs = append(schemeIDs, id)
	}
	sort.Strings(schemeIDs)
	for _, id := range schemeIDs {
		collectSecretNames(acc, "auth:"+id, authSchemeTemplateStrings(p.AuthSchemes[id]))
	}

	dirs := make([]string, 0, len(p.colByDir))
	for dir := range p.colByDir {
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)
	for _, dir := range dirs {
		c := p.colByDir[dir]
		if c == nil {
			continue
		}
		collectSecretNames(acc, dir+"/collection.yaml", authRefTemplateStrings(c.Auth))
	}

	reqIDs := make([]string, 0, len(p.Requests))
	for id := range p.Requests {
		reqIDs = append(reqIDs, id)
	}
	sort.Strings(reqIDs)
	for _, id := range reqIDs {
		e := p.Requests[id]
		if e == nil || e.Request == nil {
			continue
		}
		collectSecretNames(acc, e.RelPath, authRefTemplateStrings(e.Request.Auth))
	}

	names := make([]string, 0, len(acc))
	for name := range acc {
		names = append(names, name)
	}
	sort.Strings(names)

	out := make([]SecretReference, 0, len(names))
	for _, name := range names {
		sources := make([]string, 0, len(acc[name]))
		for src := range acc[name] {
			sources = append(sources, src)
		}
		sort.Strings(sources)
		ref := SecretReference{Name: name, Sources: sources}
		if secrets != nil {
			st := InspectSecretFrom(secrets, name)
			ref.State = st.State
			ref.Backend = st.Backend
			ref.Detail = st.Detail
		}
		out = append(out, ref)
	}
	return out
}

// IdentityDiagnostic is one actionable identity finding. It never contains a
// secret value or an encoded credential.
type IdentityDiagnostic struct {
	Level   string `json:"level"` // error | warning
	Code    string `json:"code"`
	Path    string `json:"path"`
	Message string `json:"message"`
}

// IdentityDiagnostics aggregates project identity issues: missing scheme refs,
// declaration errors, and unbound or unavailable secrets. It performs no
// network access and never reads secret values for display.
func (p *Project) IdentityDiagnostics(secrets SecretProvider) []IdentityDiagnostic {
	if p == nil {
		return nil
	}
	var out []IdentityDiagnostic
	seen := map[string]bool{}
	add := func(level, code, path, msg string) {
		key := level + "\x00" + code + "\x00" + path + "\x00" + msg
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, IdentityDiagnostic{Level: level, Code: code, Path: path, Message: msg})
	}

	for _, iss := range p.Issues {
		if !isIdentityIssueCode(iss.Code) {
			continue
		}
		level := iss.Level
		if level == "" {
			level = "error"
		}
		add(level, iss.Code, iss.Path, iss.Message)
	}

	schemeIDs := make([]string, 0, len(p.AuthSchemes))
	for id := range p.AuthSchemes {
		schemeIDs = append(schemeIDs, id)
	}
	sort.Strings(schemeIDs)
	checked := map[string]bool{}
	for _, id := range schemeIDs {
		checked[id] = true
	}
	// Project default may reference an unloaded scheme.
	if p.File != nil && p.File.Defaults != nil {
		def := strings.TrimSpace(p.File.Defaults.Auth)
		if def != "" && def != "none" && !checked[def] {
			add("error", "missing-auth-ref", "project.yaml", fmt.Sprintf("default auth %q not found", def))
			checked[def] = true
		}
	}
	dirs := make([]string, 0, len(p.colByDir))
	for dir := range p.colByDir {
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)
	for _, dir := range dirs {
		c := p.colByDir[dir]
		if c == nil || c.Auth == nil || c.Auth.Mode != "ref" {
			continue
		}
		ref := strings.TrimSpace(c.Auth.Ref)
		if ref != "" && !checked[ref] {
			add("error", "missing-auth-ref", dir+"/collection.yaml", fmt.Sprintf("auth %q not found", ref))
			checked[ref] = true
		}
	}
	reqIDs := make([]string, 0, len(p.Requests))
	for id := range p.Requests {
		reqIDs = append(reqIDs, id)
	}
	sort.Strings(reqIDs)
	for _, id := range reqIDs {
		e := p.Requests[id]
		if e == nil || e.Request == nil || e.Request.Auth == nil || e.Request.Auth.Mode != "ref" {
			continue
		}
		ref := strings.TrimSpace(e.Request.Auth.Ref)
		if ref != "" && !checked[ref] {
			add("error", "missing-auth-ref", e.RelPath, fmt.Sprintf("auth %q not found", ref))
			checked[ref] = true
		}
	}

	for _, ref := range p.SecretReferences(secrets) {
		switch ref.State {
		case SecretStateMissing:
			add("warning", "secret-unbound", firstSource(ref.Sources), fmt.Sprintf("secret %q is declared but not bound", ref.Name))
		case SecretStateUnavailable:
			msg := fmt.Sprintf("secret %q cannot be read: %s", ref.Name, ref.Backend)
			if ref.Detail != "" {
				msg += " (" + ref.Detail + ")"
			}
			add("warning", "secret-backend", firstSource(ref.Sources), msg)
		}
	}

	sort.SliceStable(out, func(i, j int) bool {
		ai, aj := out[i], out[j]
		if ai.Level != aj.Level {
			return ai.Level == "error"
		}
		if ai.Path != aj.Path {
			return ai.Path < aj.Path
		}
		if ai.Code != aj.Code {
			return ai.Code < aj.Code
		}
		return ai.Message < aj.Message
	})
	return out
}

func isIdentityIssueCode(code string) bool {
	switch code {
	case "default-auth-ref", "auth-scheme", "folder-auth-ref", "folder-auth-inline",
		"folder-auth-mode", "auth-parse", "auth-id", "auth-dup",
		"env-secret-name", "env-secret-dup":
		return true
	default:
		return false
	}
}

func firstSource(sources []string) string {
	if len(sources) == 0 {
		return ""
	}
	return sources[0]
}

// TargetConfirmation binds an interactive target-policy confirmation to one
// exact request, resolved URL, HTTP method, and environment, and can be consumed
// exactly once. Redirect hops must never reuse it: a redirect is a different
// target and requires its own decision.
type TargetConfirmation struct {
	RequestID string
	Method    string
	EnvID     string
	URL       string
	Origin    string
	Reasons   []string
	consumed  atomic.Bool
}

func canonicalTargetURL(raw string) (canonical, origin string, err error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", "", configErrorf("invalid target url: %v", err)
	}
	if !u.IsAbs() || strings.TrimSpace(u.Host) == "" {
		return "", "", configErrorf("target url must be absolute, got %q", raw)
	}
	u.Fragment = ""
	origin, err = CanonicalOrigin(u)
	if err != nil {
		return "", "", err
	}
	return u.String(), origin, nil
}

func normalizeTargetMethod(method string) string {
	m := strings.ToUpper(strings.TrimSpace(method))
	if m == "" {
		m = "GET"
	}
	return m
}

// NewTargetConfirmation captures the exact scope a user approved.
func NewTargetConfirmation(requestID, method, envID, rawURL string, reasons []string) (*TargetConfirmation, error) {
	canonical, origin, err := canonicalTargetURL(rawURL)
	if err != nil {
		return nil, err
	}
	reasonsCopy := append([]string(nil), reasons...)
	return &TargetConfirmation{
		RequestID: strings.TrimSpace(requestID),
		Method:    normalizeTargetMethod(method),
		EnvID:     strings.TrimSpace(envID),
		URL:       canonical,
		Origin:    origin,
		Reasons:   reasonsCopy,
	}, nil
}

// Matches reports whether the given send is exactly the one this confirmation
// covers. It does not consume the confirmation.
func (c *TargetConfirmation) Matches(requestID, method, envID, rawURL string) bool {
	if c == nil || c.consumed.Load() {
		return false
	}
	canonical, _, err := canonicalTargetURL(rawURL)
	if err != nil {
		return false
	}
	return c.RequestID == strings.TrimSpace(requestID) &&
		c.Method == normalizeTargetMethod(method) &&
		c.EnvID == strings.TrimSpace(envID) &&
		c.URL == canonical
}

// MatchesOnConsumed verifies method/target/environment against a consumed token
// so a confirmed send can be bound to the exact plan that is about to execute.
// It does not authorize a second send or a redirect.
func (c *TargetConfirmation) MatchesOnConsumed(requestID, method, rawURL, envID string) bool {
	if c == nil || !c.consumed.Load() {
		return false
	}
	canonical, _, err := canonicalTargetURL(rawURL)
	if err != nil {
		return false
	}
	return c.RequestID == strings.TrimSpace(requestID) &&
		c.Method == normalizeTargetMethod(method) &&
		c.EnvID == strings.TrimSpace(envID) &&
		c.URL == canonical
}

// Consume atomically accepts the confirmation for exactly one matching send.
func (c *TargetConfirmation) Consume(requestID, method, envID, rawURL string) bool {
	if !c.Matches(requestID, method, envID, rawURL) {
		return false
	}
	return c.consumed.CompareAndSwap(false, true)
}

// IsConsumed reports whether the token has already authorized a send.
func (c *TargetConfirmation) IsConsumed() bool {
	return c == nil || c.consumed.Load()
}

// ReasonsText joins the policy reasons for display, without secret material.
func (c *TargetConfirmation) ReasonsText() string {
	if c == nil {
		return ""
	}
	return strings.Join(c.Reasons, "; ")
}

// OneShotConfirmGate carries an exact-scope authorization into core.Execute.
// ConfirmedForInitialHop authorizes ONLY the first request whose plan URL,
// method, request ID, and environment match the consumed token. Any redirect
// hop is evaluated separately with Confirmed=false, so an initial confirmation
// can never become a broad authorization for a different target.
type OneShotConfirmGate struct {
	Token             *TargetConfirmation
	RequestID         string
	EnvID             string
	initialAuthorized bool
	mu                sync.Mutex
}

// NewOneShotConfirmGate builds a gate for a consumed confirmation token.
func NewOneShotConfirmGate(token *TargetConfirmation, requestID, envID string) *OneShotConfirmGate {
	return &OneShotConfirmGate{
		Token:     token,
		RequestID: strings.TrimSpace(requestID),
		EnvID:     strings.TrimSpace(envID),
	}
}

// AuthorizeInitialHop returns true exactly once, and only for the plan that
// matches the consumed token. It is called before the network client is built.
func (g *OneShotConfirmGate) AuthorizeInitialHop(method, rawURL string) bool {
	if g == nil || g.Token == nil || !g.Token.IsConsumed() {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.initialAuthorized {
		return false
	}
	if !g.Token.MatchesOnConsumed(g.RequestID, method, rawURL, g.EnvID) {
		return false
	}
	g.initialAuthorized = true
	return true
}

// AttachPlanConfirmGate binds an exact-scope gate to plan.Network without
// requiring ExecuteOptions to gain a new public field in this worktree. It is
// used through a tiny reflection adapter so the integrator can later replace
// the transport wiring with a direct field if desired.
func AttachPlanConfirmGate(plan *Plan, gate *OneShotConfirmGate) {
	if plan == nil {
		return
	}
	setPlanConfirmGate(plan, gate)
}

// SetPlanConfirmGate reports whether the current core build supports attaching
// the gate. It returns non-nil on success and nil when the transport patch has
// not been applied yet; callers must never treat that as a broad authorization.
func SetPlanConfirmGate(plan *Plan, gate *OneShotConfirmGate) *OneShotConfirmGate {
	if plan == nil {
		return nil
	}
	return setPlanConfirmGate(plan, gate)
}
