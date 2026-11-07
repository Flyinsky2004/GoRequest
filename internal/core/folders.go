package core

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// This file holds the folder (collection) tree and folder-editing API. It is
// deliberately separate from project.go so that the read-time loading logic and
// the write-time folder/request organisation logic stay easy to reason about.

// --- path helpers ---------------------------------------------------------

// sanitizeCollectionRel validates and normalizes a path relative to the
// collections directory ("" means the root). It rejects absolute paths,
// traversal segments and empty segments, so a caller can never escape
// collections/ or climb above it.
func sanitizeCollectionRel(rel string) (string, error) {
	rel = filepath.ToSlash(strings.TrimSpace(rel))
	if filepath.IsAbs(rel) {
		return "", configErrorf("invalid folder path %q", rel)
	}
	rel = strings.TrimPrefix(rel, "collections/")
	if rel == "" || rel == "." {
		return "", nil
	}
	if strings.HasPrefix(rel, "..") {
		return "", configErrorf("invalid folder path %q", rel)
	}
	for _, seg := range strings.Split(rel, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return "", configErrorf("invalid folder path %q", rel)
		}
	}
	return rel, nil
}

// collectionsDirOf maps a collections-relative path to the project-relative
// directory ("collections" for the root).
func collectionsDirOf(collectionRel string) string {
	if collectionRel == "" {
		return "collections"
	}
	return "collections/" + collectionRel
}

// collectionRelOf maps a project-relative folder path (CollectionEntry.RelPath,
// e.g. "collections/auth") back to a collections-relative path ("auth").
func collectionRelOf(entryRel string) string {
	rel := filepath.ToSlash(strings.TrimSpace(entryRel))
	rel = strings.TrimPrefix(rel, "collections/")
	rel = strings.TrimPrefix(rel, "collections")
	rel = strings.TrimPrefix(rel, "/")
	if rel == "." || rel == "" {
		return ""
	}
	return rel
}

// folderSlug returns the directory name for a folder display name and whether
// the name has one. ASCII names use the portable Slug; names with non-ASCII
// characters (for example Chinese) keep a readable directory name instead of
// collapsing to "item", with the characters illegal on common filesystems
// replaced. It mirrors projectFolderName, which does the same for project
// directories.
func folderSlug(name string) (string, bool) {
	ascii := true
	for _, r := range name {
		if r > 127 {
			ascii = false
			break
		}
	}
	if ascii {
		slug := Slug(name)
		if slug == "item" && strings.ToLower(name) != "item" {
			return "", false
		}
		return slug, true
	}
	var b strings.Builder
	for _, r := range name {
		if r < 0x20 || strings.ContainsRune(`:*?"<>|`, r) {
			b.WriteByte('-')
			continue
		}
		b.WriteRune(r)
	}
	folder := strings.Trim(b.String(), " .-")
	if folder == "" {
		return "", false
	}
	return folder, true
}

// validateFolderName turns a user-supplied folder name into a safe directory
// slug, rejecting names that would inject path separators or traversal.
func validateFolderName(name string) (string, error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return "", configErrorf("folder name is empty")
	}
	if filepath.IsAbs(trimmed) || strings.ContainsAny(trimmed, `/\`) || trimmed == "." || trimmed == ".." {
		return "", configErrorf("invalid folder name %q", name)
	}
	slug, ok := folderSlug(trimmed)
	if !ok {
		return "", configErrorf("folder name %q must contain letters or digits", name)
	}
	return slug, nil
}

// --- tree model -----------------------------------------------------------

// folderDisplayName is the label shown for a directory that has no
// collection.yaml: the directory's own base name.
func folderDisplayName(dir string) string {
	base := filepath.Base(dir)
	if base == "." || base == string(filepath.Separator) || base == "" {
		return dir
	}
	return base
}

// buildCollectionTree turns the parsed collection.yaml map and the loaded
// requests into a real folder tree. Directories that only contain requests (and
// their intermediate parent directories) are synthesized so that projects
// created before collection.yaml existed still show their hierarchy. Requests
// that live directly under collections/ are not part of any folder node and are
// exposed through Project.RootRequests instead.
func buildCollectionTree(colByDir map[string]*Collection, requests map[string]*RequestEntry) []*CollectionEntry {
	nodes := map[string]*CollectionEntry{}
	ensure := func(dir string) *CollectionEntry {
		if n, ok := nodes[dir]; ok {
			return n
		}
		n := &CollectionEntry{RelPath: dir}
		if c, ok := colByDir[dir]; ok {
			n.ID = c.ID
			n.Name = c.Name
			n.Vars = c.Variables
		} else {
			n.Name = folderDisplayName(dir)
		}
		nodes[dir] = n
		return n
	}

	// Gather every directory that needs a node: each collection dir, each
	// request dir, and every ancestor up to (but not including) collections.
	dirs := map[string]bool{}
	addChain := func(dir string) {
		cur := filepath.ToSlash(dir)
		for cur != "" && cur != "." && cur != "collections" {
			dirs[cur] = true
			parent := filepath.ToSlash(filepath.Dir(cur))
			if parent == cur {
				break
			}
			cur = parent
		}
	}
	for d := range colByDir {
		addChain(d)
	}
	for _, e := range requests {
		addChain(filepath.ToSlash(filepath.Dir(e.RelPath)))
	}
	sorted := make([]string, 0, len(dirs))
	for d := range dirs {
		sorted = append(sorted, d)
	}
	sort.Strings(sorted)
	for _, d := range sorted {
		ensure(d)
	}

	// Attach requests (those inside a folder, not root-level ones).
	for _, e := range requests {
		dir := filepath.ToSlash(filepath.Dir(e.RelPath))
		if dir == "collections" {
			continue
		}
		if n, ok := nodes[dir]; ok {
			n.Requests = append(n.Requests, e)
		}
	}

	// Link children to parents; nodes whose parent is outside the tree (i.e.
	// the parent is "collections") become roots.
	var roots []*CollectionEntry
	for _, d := range sorted {
		parent := filepath.ToSlash(filepath.Dir(d))
		if pn, ok := nodes[parent]; ok {
			pn.Children = append(pn.Children, nodes[d])
			continue
		}
		roots = append(roots, nodes[d])
	}

	var sortNode func(n *CollectionEntry)
	sortNode = func(n *CollectionEntry) {
		sort.Slice(n.Requests, func(i, j int) bool {
			if n.Requests[i].Request.Name == n.Requests[j].Request.Name {
				return n.Requests[i].RelPath < n.Requests[j].RelPath
			}
			return n.Requests[i].Request.Name < n.Requests[j].Request.Name
		})
		sort.Slice(n.Children, func(i, j int) bool { return n.Children[i].RelPath < n.Children[j].RelPath })
		for _, c := range n.Children {
			sortNode(c)
		}
	}
	sort.Slice(roots, func(i, j int) bool { return roots[i].RelPath < roots[j].RelPath })
	for _, r := range roots {
		sortNode(r)
	}
	return roots
}

// findCollectionNode walks the tree for a node whose RelPath matches dir.
func findCollectionNode(nodes []*CollectionEntry, dir string) *CollectionEntry {
	for _, n := range nodes {
		if n.RelPath == dir {
			return n
		}
		if c := findCollectionNode(n.Children, dir); c != nil {
			return c
		}
	}
	return nil
}

// --- public API -----------------------------------------------------------

// RootRequests returns the requests stored directly under collections/, sorted
// by name then path. These have no folder node, so the tree view shows them
// above the folder tree.
func (p *Project) RootRequests() []*RequestEntry {
	out := make([]*RequestEntry, 0)
	for _, e := range p.Requests {
		if filepath.ToSlash(filepath.Dir(e.RelPath)) == "collections" {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Request.Name == out[j].Request.Name {
			return out[i].RelPath < out[j].RelPath
		}
		return out[i].Request.Name < out[j].Request.Name
	})
	return out
}

// NewFolder creates a folder (a directory with a collection.yaml) under
// parentCollectionRel ("" for the root) and updates the in-memory tree. The
// folder name is slugged into the directory name while the display name keeps
// the user's spelling. Creating a folder where a collection.yaml already exists
// is refused so an existing folder is never overwritten.
func (p *Project) NewFolder(parentCollectionRel, name string) (*CollectionEntry, error) {
	slug, err := validateFolderName(name)
	if err != nil {
		return nil, err
	}
	parentRel, err := sanitizeCollectionRel(parentCollectionRel)
	if err != nil {
		return nil, err
	}
	dir := collectionsDirOf(parentRel) + "/" + slug
	colRel := dir + "/collection.yaml"
	if _, err := p.Store.resolve(colRel); err != nil {
		return nil, err
	}
	if p.Store.Exists(colRel) {
		return nil, configErrorf("a folder named %q already exists here", strings.TrimSpace(name))
	}
	c := &Collection{SchemaVersion: SupportedSchemaVersion, ID: slug, Name: strings.TrimSpace(name)}
	data, err := encodeYAML(c)
	if err != nil {
		return nil, err
	}
	if err := p.Store.WriteAtomic(colRel, data); err != nil {
		return nil, err
	}
	if p.colByDir == nil {
		p.colByDir = map[string]*Collection{}
	}
	p.colByDir[dir] = c
	p.rebuildTree()
	if node := findCollectionNode(p.Collections, dir); node != nil {
		return node, nil
	}
	return &CollectionEntry{RelPath: dir, ID: slug, Name: c.Name, Vars: c.Variables}, nil
}

// MoveRequest moves a request file into targetCollectionRel ("" for the root)
// without changing its stable request ID. It reads and verifies the source
// against the entry's baseline first, writes the target, and only then removes
// the source; a failure to write the target leaves the source untouched, and a
// failed removal rolls the freshly written target back so no duplicate remains.
// It refuses to overwrite an existing file at the target.
func (p *Project) MoveRequest(entry *RequestEntry, targetCollectionRel string) error {
	if entry == nil {
		return configErrorf("no request to move")
	}
	targetRel, err := sanitizeCollectionRel(targetCollectionRel)
	if err != nil {
		return err
	}
	targetDir := collectionsDirOf(targetRel)
	srcDir := filepath.ToSlash(filepath.Dir(entry.RelPath))
	if srcDir == targetDir {
		return nil
	}
	if targetDir != "collections" && !p.Store.Exists(targetDir) {
		return configErrorf("target folder %q does not exist", targetRel)
	}
	base := filepath.Base(entry.RelPath)
	newRel := filepath.ToSlash(filepath.Join(targetDir, base))
	if _, err := p.Store.resolve(newRel); err != nil {
		return err
	}
	if p.Store.Exists(newRel) {
		return configErrorf("target already has a file named %q", base)
	}

	content, diskDigest, err := p.Store.Read(entry.RelPath)
	if err != nil {
		return err
	}
	if entry.BaselineDigest != "" && diskDigest != entry.BaselineDigest {
		return &ConflictError{Path: entry.RelPath, Baseline: entry.BaselineDigest, Disk: diskDigest}
	}

	if err := p.Store.WriteAtomic(newRel, content); err != nil {
		return err
	}
	srcAbs, err := p.Store.resolve(entry.RelPath)
	if err != nil {
		return err
	}
	if err := os.Remove(srcAbs); err != nil {
		// Roll back the copy so the project never ends up with the same request
		// in two places after a partial move.
		if newAbs, e2 := p.Store.resolve(newRel); e2 == nil {
			_ = os.Remove(newAbs)
		}
		return fmt.Errorf("removing source request: %w", err)
	}

	entry.RelPath = newRel
	entry.BaselineDigest = Digest(content)
	entry.CollectionVars = p.collectionVarsFor(newRel, p.colByDir)
	p.rebuildTree()
	return nil
}

// --- rename ---------------------------------------------------------------

// RenameRequest changes a request's display name in place, keeping its stable
// ID and file path. It goes through SaveRequest so the write is conflict
// checked against the entry's baseline.
func (p *Project) RenameRequest(entry *RequestEntry, name string) error {
	if entry == nil || entry.Request == nil {
		return configErrorf("no request to rename")
	}
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return configErrorf("request name is empty")
	}
	if trimmed == entry.Request.Name {
		return nil
	}
	next := *entry.Request
	next.Name = trimmed
	_, err := p.SaveRequest(entry, &next, entry.BaselineDigest)
	return err
}

// RenameFolder renames a folder in place: it moves the directory to the slug of
// the new name (within the same parent), rewrites its collection.yaml display
// name and updates every in-memory path beneath it. The collection ID is kept.
func (p *Project) RenameFolder(node *CollectionEntry, name string) (*CollectionEntry, error) {
	if node == nil {
		return nil, configErrorf("no folder to rename")
	}
	rel := collectionRelOf(node.RelPath)
	if rel == "" {
		return nil, configErrorf("cannot rename the collections root")
	}
	slug, err := validateFolderName(name)
	if err != nil {
		return nil, err
	}
	parent := filepath.ToSlash(filepath.Dir(rel))
	if parent == "." {
		parent = ""
	}
	return p.moveFolderDir(rel, parent, slug, strings.TrimSpace(name))
}

// MoveFolder moves a folder under targetParentCollectionRel ("" for the root),
// keeping its directory name and display name. A folder can never move inside
// itself or one of its descendants.
func (p *Project) MoveFolder(node *CollectionEntry, targetParentCollectionRel string) (*CollectionEntry, error) {
	if node == nil {
		return nil, configErrorf("no folder to move")
	}
	rel := collectionRelOf(node.RelPath)
	if rel == "" {
		return nil, configErrorf("cannot move the collections root")
	}
	parent, err := sanitizeCollectionRel(targetParentCollectionRel)
	if err != nil {
		return nil, err
	}
	if parent == rel || strings.HasPrefix(parent, rel+"/") {
		return nil, configErrorf("cannot move a folder into itself")
	}
	display := node.Name
	if strings.TrimSpace(display) == "" {
		display = filepath.Base(rel)
	}
	return p.moveFolderDir(rel, parent, filepath.Base(rel), display)
}

// moveFolderDir moves the directory rel under parentRel as slug, rewrites the
// folder's collection.yaml, and re-keys every path it holds in the in-memory
// model. A move that lands on the same directory only rewrites the display name.
func (p *Project) moveFolderDir(rel, parentRel, slug, displayName string) (*CollectionEntry, error) {
	oldDir := collectionsDirOf(rel)
	newDir := collectionsDirOf(filepath.ToSlash(filepath.Join(parentRel, slug)))
	if oldDir == newDir {
		return p.writeFolderCollection(newDir, slug, displayName)
	}
	if p.Store.Exists(newDir) {
		return nil, configErrorf("a folder named %q already exists there", displayName)
	}
	oldAbs, err := p.Store.resolve(oldDir)
	if err != nil {
		return nil, err
	}
	if info, statErr := os.Stat(oldAbs); statErr != nil || !info.IsDir() {
		return nil, configErrorf("folder %q does not exist", rel)
	}
	newAbs, err := p.Store.resolve(newDir)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(newAbs), 0o755); err != nil {
		return nil, fmt.Errorf("creating parent folder: %w", err)
	}
	if err := os.Rename(oldAbs, newAbs); err != nil {
		return nil, fmt.Errorf("moving folder: %w", err)
	}

	// Re-key collection.yaml entries and request paths under the moved dir.
	if p.colByDir == nil {
		p.colByDir = map[string]*Collection{}
	}
	var oldKeys []string
	for d := range p.colByDir {
		if d == oldDir || strings.HasPrefix(d, oldDir+"/") {
			oldKeys = append(oldKeys, d)
		}
	}
	for _, d := range oldKeys {
		c := p.colByDir[d]
		delete(p.colByDir, d)
		p.colByDir[newDir+strings.TrimPrefix(d, oldDir)] = c
	}
	for _, e := range p.Requests {
		if e.RelPath == oldDir || strings.HasPrefix(e.RelPath, oldDir+"/") {
			e.RelPath = newDir + strings.TrimPrefix(e.RelPath, oldDir)
			e.CollectionVars = p.collectionVarsFor(e.RelPath, p.colByDir)
		}
	}
	return p.writeFolderCollection(newDir, slug, displayName)
}

// writeFolderCollection writes collection.yaml for dir with the given display
// name, keeping the collection's existing ID when there is one, and rebuilds
// the tree.
func (p *Project) writeFolderCollection(dir, slug, displayName string) (*CollectionEntry, error) {
	if p.colByDir == nil {
		p.colByDir = map[string]*Collection{}
	}
	c := p.colByDir[dir]
	if c == nil {
		c = &Collection{SchemaVersion: SupportedSchemaVersion, ID: slug}
	}
	c.Name = displayName
	if c.ID == "" {
		c.ID = slug
	}
	if c.SchemaVersion == 0 {
		c.SchemaVersion = SupportedSchemaVersion
	}
	data, err := encodeYAML(c)
	if err != nil {
		return nil, err
	}
	if err := p.Store.WriteAtomic(dir+"/collection.yaml", data); err != nil {
		return nil, err
	}
	p.colByDir[dir] = c
	p.rebuildTree()
	if node := findCollectionNode(p.Collections, dir); node != nil {
		return node, nil
	}
	return &CollectionEntry{RelPath: dir, ID: c.ID, Name: c.Name, Vars: c.Variables}, nil
}

// --- delete and restore ---------------------------------------------------

// FolderSnapshot is the content of a deleted folder, kept so the caller can
// offer a short undo window. Files are project-relative paths to bytes.
type FolderSnapshot struct {
	Dir   string
	Files map[string][]byte
}

// DeleteRequest removes a request's file and its in-memory registration. The
// raw file content is returned so RestoreRequest can put it back, unchanged,
// under the same ID.
func (p *Project) DeleteRequest(entry *RequestEntry) ([]byte, error) {
	if entry == nil || entry.Request == nil {
		return nil, configErrorf("no request to delete")
	}
	content, diskDigest, err := p.Store.Read(entry.RelPath)
	if err != nil {
		return nil, err
	}
	if entry.BaselineDigest != "" && diskDigest != entry.BaselineDigest {
		return nil, &ConflictError{Path: entry.RelPath, Baseline: entry.BaselineDigest, Disk: diskDigest}
	}
	abs, err := p.Store.resolve(entry.RelPath)
	if err != nil {
		return nil, err
	}
	if err := os.Remove(abs); err != nil {
		return nil, fmt.Errorf("deleting request: %w", err)
	}
	p.forgetRequest(entry)
	p.rebuildTree()
	return content, nil
}

// RestoreRequest puts a deleted request back at its path and re-registers it,
// keeping its stable ID.
func (p *Project) RestoreRequest(entry *RequestEntry, content []byte) error {
	if entry == nil || entry.Request == nil {
		return configErrorf("no request to restore")
	}
	if err := p.Store.WriteAtomic(entry.RelPath, content); err != nil {
		return err
	}
	entry.BaselineDigest = Digest(content)
	if p.colByDir != nil {
		entry.CollectionVars = p.collectionVarsFor(entry.RelPath, p.colByDir)
	}
	p.Requests[entry.Request.ID] = entry
	p.Order = append(p.Order, entry.Request.ID)
	p.rebuildTree()
	return nil
}

// forgetRequest drops entry from the request index and order without touching
// the filesystem.
func (p *Project) forgetRequest(entry *RequestEntry) {
	if entry == nil || entry.Request == nil {
		return
	}
	delete(p.Requests, entry.Request.ID)
	for i, id := range p.Order {
		if id == entry.Request.ID {
			p.Order = append(p.Order[:i], p.Order[i+1:]...)
			break
		}
	}
}

// DeleteFolder removes a folder and everything beneath it, first snapshotting
// every file so RestoreFolder can put it back. Requests inside are removed from
// the in-memory index; collection.yaml entries under the folder are dropped.
func (p *Project) DeleteFolder(node *CollectionEntry) (*FolderSnapshot, error) {
	if node == nil {
		return nil, configErrorf("no folder to delete")
	}
	rel := collectionRelOf(node.RelPath)
	if rel == "" {
		return nil, configErrorf("cannot delete the collections root")
	}
	dir, err := sanitizeCollectionRel(rel)
	if err != nil {
		return nil, err
	}
	projectDir := collectionsDirOf(dir)
	abs, err := p.Store.resolve(projectDir)
	if err != nil {
		return nil, err
	}
	if info, statErr := os.Stat(abs); statErr != nil || !info.IsDir() {
		return nil, configErrorf("folder %q does not exist", dir)
	}
	files := map[string][]byte{}
	err = filepath.WalkDir(abs, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		relPath, relErr := filepath.Rel(p.Store.Root, path)
		if relErr != nil {
			return relErr
		}
		files[filepath.ToSlash(relPath)] = data
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("snapshotting folder: %w", err)
	}

	for _, e := range flattenCollectionRequests(node) {
		p.forgetRequest(e)
	}
	if err := os.RemoveAll(abs); err != nil {
		return nil, fmt.Errorf("deleting folder: %w", err)
	}
	for d := range p.colByDir {
		if d == projectDir || strings.HasPrefix(d, projectDir+"/") {
			delete(p.colByDir, d)
		}
	}
	p.rebuildTree()
	return &FolderSnapshot{Dir: projectDir, Files: files}, nil
}

// RestoreFolder writes a deleted folder's files back and reloads its
// collections and requests into the in-memory model.
func (p *Project) RestoreFolder(snap *FolderSnapshot) error {
	if snap == nil {
		return configErrorf("no folder to restore")
	}
	for rel, data := range snap.Files {
		if err := p.Store.WriteAtomic(rel, data); err != nil {
			return err
		}
	}
	if p.colByDir == nil {
		p.colByDir = map[string]*Collection{}
	}
	for rel, data := range snap.Files {
		if filepath.Base(rel) != "collection.yaml" {
			continue
		}
		var c Collection
		if err := decodeStrict(data, &c); err != nil {
			continue
		}
		p.colByDir[filepath.ToSlash(filepath.Dir(rel))] = &c
	}
	for rel, data := range snap.Files {
		if !strings.HasSuffix(rel, ".request.yaml") {
			continue
		}
		var req Request
		if err := decodeStrict(data, &req); err != nil || req.ID == "" {
			continue
		}
		entry := &RequestEntry{
			Request:        &req,
			RelPath:        rel,
			BaselineDigest: Digest(data),
			CollectionVars: p.collectionVarsFor(rel, p.colByDir),
		}
		p.Requests[req.ID] = entry
		p.Order = append(p.Order, req.ID)
	}
	p.rebuildTree()
	return nil
}

// flattenCollectionRequests returns every request in a folder subtree.
func flattenCollectionRequests(n *CollectionEntry) []*RequestEntry {
	if n == nil {
		return nil
	}
	out := append([]*RequestEntry(nil), n.Requests...)
	for _, ch := range n.Children {
		out = append(out, flattenCollectionRequests(ch)...)
	}
	return out
}

// --- documents ------------------------------------------------------------

// RenameDoc moves a document to the slug of a new name within docs/, keeping
// its content and baseline digest.
func (p *Project) RenameDoc(d *DocEntry, name string) error {
	if d == nil {
		return configErrorf("no document to rename")
	}
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return configErrorf("document name is empty")
	}
	if filepath.IsAbs(trimmed) || strings.ContainsAny(trimmed, `/\`) || trimmed == "." || trimmed == ".." {
		return configErrorf("invalid document name %q", name)
	}
	slug := Slug(trimmed)
	if slug == "item" && strings.ToLower(trimmed) != "item" {
		return configErrorf("document name %q must contain letters or digits", name)
	}
	newRel := filepath.ToSlash(filepath.Join("docs", slug+".md"))
	if newRel == d.RelPath {
		d.Name = filepath.Base(newRel)
		return nil
	}
	if p.Store.Exists(newRel) {
		return configErrorf("a document named %q already exists", trimmed)
	}
	data, digest, err := p.Store.Read(d.RelPath)
	if err != nil {
		return err
	}
	if err := p.Store.WriteAtomic(newRel, data); err != nil {
		return fmt.Errorf("writing renamed document: %w", err)
	}
	oldAbs, err := p.Store.resolve(d.RelPath)
	if err != nil {
		return err
	}
	if err := os.Remove(oldAbs); err != nil {
		if newAbs, e2 := p.Store.resolve(newRel); e2 == nil {
			_ = os.Remove(newAbs)
		}
		return fmt.Errorf("removing old document: %w", err)
	}
	d.RelPath = newRel
	d.Name = filepath.Base(newRel)
	d.BaselineDigest = digest
	return nil
}

// DeleteDoc removes a document file, returning its content for undo.
func (p *Project) DeleteDoc(d *DocEntry) ([]byte, error) {
	if d == nil {
		return nil, configErrorf("no document to delete")
	}
	data, digest, err := p.Store.Read(d.RelPath)
	if err != nil {
		return nil, err
	}
	if d.BaselineDigest != "" && digest != d.BaselineDigest {
		return nil, &ConflictError{Path: d.RelPath, Baseline: d.BaselineDigest, Disk: digest}
	}
	abs, err := p.Store.resolve(d.RelPath)
	if err != nil {
		return nil, err
	}
	if err := os.Remove(abs); err != nil {
		return nil, fmt.Errorf("deleting document: %w", err)
	}
	for i, doc := range p.Docs {
		if doc == d {
			p.Docs = append(p.Docs[:i], p.Docs[i+1:]...)
			break
		}
	}
	return data, nil
}

// RestoreDoc writes a deleted document back and re-registers it.
func (p *Project) RestoreDoc(d *DocEntry, content []byte) error {
	if d == nil {
		return configErrorf("no document to restore")
	}
	if err := p.Store.WriteAtomic(d.RelPath, content); err != nil {
		return err
	}
	d.BaselineDigest = Digest(content)
	for _, doc := range p.Docs {
		if doc == d {
			return nil
		}
	}
	p.Docs = append(p.Docs, d)
	return nil
}

// rebuildTree refreshes the in-memory collection tree and validation issues
// after a successful on-disk mutation. It never re-reads request files.
func (p *Project) rebuildTree() {
	p.Collections = buildCollectionTree(p.colByDir, p.Requests)
	p.revalidate()
}
