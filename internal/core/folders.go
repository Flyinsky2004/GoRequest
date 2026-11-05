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
	slug := Slug(trimmed)
	if slug == "item" && strings.ToLower(trimmed) != "item" {
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

// rebuildTree refreshes the in-memory collection tree and validation issues
// after a successful on-disk mutation. It never re-reads request files.
func (p *Project) rebuildTree() {
	p.Collections = buildCollectionTree(p.colByDir, p.Requests)
	p.revalidate()
}
