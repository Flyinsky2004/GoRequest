package core

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// RequestEntry is a request plus the file it lives in and the collection
// variables inherited by it.
type RequestEntry struct {
	Request        *Request
	RelPath        string
	BaselineDigest string
	CollectionVars map[string]string
}

// CollectionEntry is a folder under collections/ with optional collection.yaml
// and the requests directly inside it.
type CollectionEntry struct {
	RelPath  string // project-relative dir, e.g. "collections/auth"
	ID       string
	Name     string
	Vars     map[string]string
	Requests []*RequestEntry
	Children []*CollectionEntry
}

// DocEntry is a Markdown document under docs/.
type DocEntry struct {
	RelPath        string
	Name           string
	BaselineDigest string
}

// Project is a loaded .gorequest project. Files on disk are the source of
// truth; this struct is a read snapshot plus the baseline digests needed for
// safe saving.
type Project struct {
	Dir          string // absolute path to the .gorequest directory
	File         *ProjectFile
	Store        *Store
	Requests     map[string]*RequestEntry
	Order        []string
	Collections  []*CollectionEntry
	Environments map[string]*Environment
	EnvEntries   map[string]*EnvironmentEntry // id -> file path and baseline digest
	EnvOrder     []string
	AuthSchemes  map[string]*AuthScheme
	Docs         []*DocEntry
	Issues       []Issue

	// loadIssues keeps the load-time findings (parse errors and the like) so
	// that re-validating after an in-memory mutation does not accumulate
	// duplicates of them on every rebuild.
	loadIssues []Issue
	// colByDir is the parsed collection.yaml of every folder, keyed by the
	// project-relative directory (e.g. "collections/auth"). It is kept so that
	// creating a folder or moving a request can rebuild the tree without
	// re-reading the project.
	colByDir map[string]*Collection
}

// LocateProjectDir finds the .gorequest directory for a user-supplied path,
// which may be the project root, the .gorequest dir itself, or a parent.
func LocateProjectDir(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	if filepath.Base(abs) == ".gorequest" {
		if _, err := os.Stat(filepath.Join(abs, "project.yaml")); err == nil {
			return abs, nil
		}
		return "", fmt.Errorf("%s has no project.yaml", abs)
	}
	gq := filepath.Join(abs, ".gorequest")
	if _, err := os.Stat(filepath.Join(gq, "project.yaml")); err == nil {
		return gq, nil
	}
	return "", fmt.Errorf("no .gorequest/project.yaml found under %s", abs)
}

// OpenProject loads and validates a project. It never performs network access.
func OpenProject(dir string) (*Project, error) {
	gq, err := LocateProjectDir(dir)
	if err != nil {
		return nil, &ConfigError{Err: err}
	}
	store := NewStore(gq)
	data, _, err := store.Read("project.yaml")
	if err != nil {
		return nil, fmt.Errorf("reading project.yaml: %w", err)
	}
	var pf ProjectFile
	if err := decodeStrict(data, &pf); err != nil {
		return nil, &ConfigError{Err: fmt.Errorf("project.yaml: %w", err)}
	}
	if pf.SchemaVersion != SupportedSchemaVersion {
		return nil, &ConfigError{Err: fmt.Errorf("project.yaml schemaVersion %d unsupported (this build reads %d)", pf.SchemaVersion, SupportedSchemaVersion)}
	}
	p := &Project{
		Dir:          gq,
		File:         &pf,
		Store:        store,
		Requests:     map[string]*RequestEntry{},
		Environments: map[string]*Environment{},
		EnvEntries:   map[string]*EnvironmentEntry{},
		AuthSchemes:  map[string]*AuthScheme{},
	}

	if err := p.loadAuth(); err != nil {
		return nil, err
	}
	if err := p.loadEnvironments(); err != nil {
		return nil, err
	}
	if err := p.loadCollectionsAndRequests(); err != nil {
		return nil, err
	}
	if err := p.loadDocs(); err != nil {
		return nil, err
	}
	p.loadIssues = append([]Issue(nil), p.Issues...)
	p.Issues = ValidateProject(p)
	return p, nil
}

func (p *Project) loadAuth() error {
	files, err := p.Store.List("auth", func(n string) bool { return strings.HasSuffix(n, ".auth.yaml") })
	if err != nil {
		return fmt.Errorf("listing auth: %w", err)
	}
	for _, rel := range files {
		data, _, err := p.Store.Read(rel)
		if err != nil {
			return fmt.Errorf("reading %s: %w", rel, err)
		}
		var s AuthScheme
		if err := decodeStrict(data, &s); err != nil {
			p.Issues = append(p.Issues, Issue{Level: "error", Code: "auth-parse", Path: rel, Message: err.Error()})
			continue
		}
		if s.ID == "" {
			p.Issues = append(p.Issues, Issue{Level: "error", Code: "auth-id", Path: rel, Message: "auth scheme has no id"})
			continue
		}
		if _, dup := p.AuthSchemes[s.ID]; dup {
			p.Issues = append(p.Issues, Issue{Level: "error", Code: "auth-dup", Path: rel, Message: "duplicate auth id " + s.ID})
			continue
		}
		p.AuthSchemes[s.ID] = &s
	}
	return nil
}

func (p *Project) loadEnvironments() error {
	files, err := p.Store.List("environments", func(n string) bool { return strings.HasSuffix(n, ".yaml") || strings.HasSuffix(n, ".yml") })
	if err != nil {
		return fmt.Errorf("listing environments: %w", err)
	}
	for _, rel := range files {
		data, digest, err := p.Store.Read(rel)
		if err != nil {
			return fmt.Errorf("reading %s: %w", rel, err)
		}
		var e Environment
		if err := decodeStrict(data, &e); err != nil {
			p.Issues = append(p.Issues, Issue{Level: "error", Code: "env-parse", Path: rel, Message: err.Error()})
			continue
		}
		if e.ID == "" {
			e.ID = strings.TrimSuffix(filepath.Base(rel), filepath.Ext(rel))
		}
		if _, dup := p.Environments[e.ID]; dup {
			p.Issues = append(p.Issues, Issue{Level: "error", Code: "env-dup", Path: rel, Message: "duplicate environment id " + e.ID})
			continue
		}
		env := e
		p.Environments[e.ID] = &env
		p.EnvEntries[e.ID] = &EnvironmentEntry{Environment: &env, RelPath: rel, BaselineDigest: digest}
		p.EnvOrder = append(p.EnvOrder, e.ID)
	}
	sort.Strings(p.EnvOrder)
	return nil
}

func (p *Project) loadCollectionsAndRequests() error {
	// Gather collection.yaml per directory.
	colFiles, err := p.Store.List("collections", func(n string) bool { return n == "collection.yaml" })
	if err != nil {
		return fmt.Errorf("listing collections: %w", err)
	}
	colByDir := map[string]*Collection{}
	for _, rel := range colFiles {
		data, _, err := p.Store.Read(rel)
		if err != nil {
			return fmt.Errorf("reading %s: %w", rel, err)
		}
		var c Collection
		if err := decodeStrict(data, &c); err != nil {
			p.Issues = append(p.Issues, Issue{Level: "error", Code: "collection-parse", Path: rel, Message: err.Error()})
			continue
		}
		colByDir[filepath.ToSlash(filepath.Dir(rel))] = &c
	}

	// Gather requests.
	reqFiles, err := p.Store.List("collections", func(n string) bool { return strings.HasSuffix(n, ".request.yaml") })
	if err != nil {
		return fmt.Errorf("listing requests: %w", err)
	}
	sort.Strings(reqFiles)
	for _, rel := range reqFiles {
		data, digest, err := p.Store.Read(rel)
		if err != nil {
			return fmt.Errorf("reading %s: %w", rel, err)
		}
		var req Request
		if err := decodeStrict(data, &req); err != nil {
			p.Issues = append(p.Issues, Issue{Level: "error", Code: "request-parse", Path: rel, Message: err.Error()})
			continue
		}
		if req.ID == "" {
			p.Issues = append(p.Issues, Issue{Level: "error", Code: "request-id", Path: rel, Message: "request has no id"})
			continue
		}
		if _, dup := p.Requests[req.ID]; dup {
			p.Issues = append(p.Issues, Issue{Level: "error", Code: "request-dup", Path: rel, Message: "duplicate request id " + req.ID})
			continue
		}
		entry := &RequestEntry{
			Request:        &req,
			RelPath:        rel,
			BaselineDigest: digest,
			CollectionVars: p.collectionVarsFor(rel, colByDir),
		}
		p.Requests[req.ID] = entry
		p.Order = append(p.Order, req.ID)
	}

	p.colByDir = colByDir
	p.Collections = buildCollectionTree(colByDir, p.Requests)
	return nil
}

// collectionVarsFor merges collection variables from the outermost to the
// innermost ancestor directory of a request file.
func (p *Project) collectionVarsFor(rel string, colByDir map[string]*Collection) map[string]string {
	dir := filepath.ToSlash(filepath.Dir(rel)) // e.g. collections/auth
	var chain []string
	cur := dir
	for cur != "" && cur != "." && cur != "collections" {
		chain = append(chain, cur)
		parent := filepath.ToSlash(filepath.Dir(cur))
		if parent == cur {
			break
		}
		cur = parent
	}
	// chain is innermost-first; reverse for outer-to-inner precedence.
	vars := map[string]string{}
	for i := len(chain) - 1; i >= 0; i-- {
		if c, ok := colByDir[chain[i]]; ok {
			for k, v := range c.Variables {
				vars[k] = v
			}
		}
	}
	return vars
}

func (p *Project) loadDocs() error {
	files, err := p.Store.List("docs", func(n string) bool { return strings.HasSuffix(n, ".md") })
	if err != nil {
		return fmt.Errorf("listing docs: %w", err)
	}
	sort.Strings(files)
	for _, rel := range files {
		_, digest, err := p.Store.Read(rel)
		if err != nil {
			return fmt.Errorf("reading %s: %w", rel, err)
		}
		p.Docs = append(p.Docs, &DocEntry{RelPath: rel, Name: filepath.Base(rel), BaselineDigest: digest})
	}
	return nil
}

// ResolveVars merges project, environment, collection and request variables for
// a request.
func (p *Project) ResolveVars(entry *RequestEntry, envID string) (map[string]string, *Environment, error) {
	var env *Environment
	if envID != "" {
		e, ok := p.Environments[envID]
		if !ok {
			return nil, nil, configErrorf("environment %q not found", envID)
		}
		env = e
	}
	var envVars map[string]string
	if env != nil {
		envVars = env.Variables
	}
	merged := MergeVars(p.File.Variables, envVars, entry.CollectionVars, entry.Request.Variables)
	return merged, env, nil
}

// AuthFor resolves the reusable auth scheme for a request, or nil for none.
func (p *Project) AuthFor(req *Request) (*AuthScheme, error) {
	if req.Auth == nil || req.Auth.Mode == "" {
		if p.File.Defaults != nil && p.File.Defaults.Auth != "" && p.File.Defaults.Auth != "none" {
			s, ok := p.AuthSchemes[p.File.Defaults.Auth]
			if !ok {
				return nil, configErrorf("default auth %q not found", p.File.Defaults.Auth)
			}
			return s, nil
		}
		return nil, nil
	}
	switch req.Auth.Mode {
	case "none":
		return nil, nil
	case "inline":
		return req.Auth.Inline, nil
	case "ref":
		s, ok := p.AuthSchemes[req.Auth.Ref]
		if !ok {
			return nil, configErrorf("auth %q not found", req.Auth.Ref)
		}
		return s, nil
	default:
		return nil, configErrorf("unknown auth mode %q", req.Auth.Mode)
	}
}

// EncodeRequest serializes a request to YAML.
func (p *Project) EncodeRequest(r *Request) ([]byte, error) {
	c := *r
	if c.SchemaVersion == 0 {
		c.SchemaVersion = SupportedSchemaVersion
	}
	return encodeYAML(&c)
}

// SaveRequest writes a request if the file still matches baseline, then
// updates the entry's digest.
func (p *Project) SaveRequest(entry *RequestEntry, r *Request, baseline string) (string, error) {
	data, err := p.EncodeRequest(r)
	if err != nil {
		return "", err
	}
	if err := p.Store.SaveChecked(entry.RelPath, data, baseline); err != nil {
		return "", err
	}
	d := Digest(data)
	entry.BaselineDigest = d
	entry.Request = r
	// Keep the maps current for the UI.
	p.Requests[r.ID] = entry
	p.revalidate()
	return d, nil
}

// NewRequest creates and saves a new request in a collection directory
// (relative to collections, may be empty for the root) and keeps the in-memory
// collection tree, index and inherited collection variables in sync.
func (p *Project) NewRequest(collectionRel, name string) (*RequestEntry, error) {
	if strings.TrimSpace(name) == "" {
		name = "New Request"
	}
	colRel, err := sanitizeCollectionRel(collectionRel)
	if err != nil {
		return nil, err
	}
	base := Slug(name)
	dir := collectionsDirOf(colRel)
	prefix := ""
	if colRel != "" {
		prefix = Slug(filepath.Base(colRel)) + "."
	}
	id := prefix + base
	for i := 2; ; i++ {
		if _, exists := p.Requests[id]; !exists {
			break
		}
		id = fmt.Sprintf("%s%s-%d", prefix, base, i)
	}
	rel := filepath.ToSlash(filepath.Join(dir, base+".request.yaml"))
	for i := 2; p.Store.Exists(rel); i++ {
		rel = filepath.ToSlash(filepath.Join(dir, fmt.Sprintf("%s-%d.request.yaml", base, i)))
	}
	req := &Request{
		SchemaVersion: SupportedSchemaVersion,
		ID:            id,
		Name:          name,
		Protocol:      "http",
		HTTP:          &HTTPDef{Method: "GET", URL: "{{baseUrl}}"},
	}
	data, err := p.EncodeRequest(req)
	if err != nil {
		return nil, err
	}
	if err := p.Store.SaveChecked(rel, data, ""); err != nil {
		return nil, err
	}
	entry := &RequestEntry{
		Request:        req,
		RelPath:        rel,
		BaselineDigest: Digest(data),
		CollectionVars: p.collectionVarsFor(rel, p.colByDir),
	}
	p.Requests[id] = entry
	p.Order = append(p.Order, id)
	p.rebuildTree()
	return entry, nil
}

// NewDoc creates a Markdown document under docs/.
func (p *Project) NewDoc(name string) (*DocEntry, error) {
	if strings.TrimSpace(name) == "" {
		name = "document"
	}
	base := Slug(name)
	rel := filepath.ToSlash(filepath.Join("docs", base+".md"))
	for i := 2; p.Store.Exists(rel); i++ {
		rel = filepath.ToSlash(filepath.Join("docs", fmt.Sprintf("%s-%d.md", base, i)))
	}
	content := []byte("# " + strings.TrimSpace(name) + "\n\n")
	if err := p.Store.WriteAtomic(rel, content); err != nil {
		return nil, err
	}
	de := &DocEntry{RelPath: rel, Name: filepath.Base(rel), BaselineDigest: Digest(content)}
	p.Docs = append(p.Docs, de)
	return de, nil
}

// SaveDoc writes a Markdown document with conflict checking.
func (p *Project) SaveDoc(rel string, content []byte, baseline string) (string, error) {
	if err := p.Store.SaveChecked(rel, content, baseline); err != nil {
		return "", err
	}
	d := Digest(content)
	for _, de := range p.Docs {
		if de.RelPath == rel {
			de.BaselineDigest = d
		}
	}
	return d, nil
}

// NewProject scaffolds a minimal .gorequest project under dir and returns it.
func NewProject(dir, name string) (*Project, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	gq := filepath.Join(abs, ".gorequest")
	for _, sub := range []string{"collections", "environments", "docs", "auth"} {
		if err := os.MkdirAll(filepath.Join(gq, sub), 0o755); err != nil {
			return nil, err
		}
	}
	store := NewStore(gq)
	if name == "" {
		name = filepath.Base(abs)
	}
	if !store.Exists("project.yaml") {
		pf := ProjectFile{
			SchemaVersion: SupportedSchemaVersion,
			ID:            Slug(name),
			Name:          name,
			Variables:     map[string]string{"baseUrl": "http://127.0.0.1:4010"},
			Defaults:      &Defaults{Timeout: "10s", Auth: "none"},
		}
		data, err := encodeYAML(&pf)
		if err != nil {
			return nil, err
		}
		if err := store.WriteAtomic("project.yaml", data); err != nil {
			return nil, err
		}
	}
	if !store.Exists("environments/dev.yaml") {
		env := Environment{SchemaVersion: SupportedSchemaVersion, ID: "dev", Variables: map[string]string{"baseUrl": "http://127.0.0.1:4010"}}
		data, err := encodeYAML(&env)
		if err != nil {
			return nil, err
		}
		if err := store.WriteAtomic("environments/dev.yaml", data); err != nil {
			return nil, err
		}
	}
	if !store.Exists("docs/README.md") {
		if err := store.WriteAtomic("docs/README.md", []byte("# "+name+"\n\nAPI 项目文档。\n")); err != nil {
			return nil, err
		}
	}
	return OpenProject(abs)
}
