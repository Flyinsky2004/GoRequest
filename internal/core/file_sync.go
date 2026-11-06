package core

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// SyncEntityKind classifies tracked files under .gorequest/.
type SyncEntityKind string

const (
	EntityProject     SyncEntityKind = "project"
	EntityRequest     SyncEntityKind = "request"
	EntityCollection  SyncEntityKind = "collection"
	EntityEnvironment SyncEntityKind = "environment"
	EntityAuth        SyncEntityKind = "auth"
	EntityDoc         SyncEntityKind = "doc"
)

// SyncEventKind classifies an external filesystem transition between two
// snapshots of a .gorequest/ directory.
type SyncEventKind string

const (
	SyncCreated  SyncEventKind = "created"
	SyncModified SyncEventKind = "modified"
	SyncDeleted  SyncEventKind = "deleted"
	SyncRenamed  SyncEventKind = "renamed"
)

// SyncEvent describes one external file change detected under .gorequest/.
type SyncEvent struct {
	Kind           SyncEventKind
	EntityKind     SyncEntityKind
	EntityID       string
	Path           string // current path (or deleted path for SyncDeleted)
	OldPath        string // previous path when Kind == SyncRenamed
	OldDigest      string
	NewDigest      string
	ContentChanged bool // true for Created, Modified, and Renamed-with-edit
	HasGitConflict bool
	ParseErr       string
}

// ConflictStateKind describes why a dirty buffer entered three-version
// protection.
type ConflictStateKind string

const (
	ConflictModified        ConflictStateKind = "modified"
	ConflictDeleted         ConflictStateKind = "deleted"
	ConflictRenamedModified ConflictStateKind = "renamed-modified"
)

// BufferActionKind tells the UI/caller how an open editor buffer should react
// to an external change.
type BufferActionKind string

const (
	BufferActionRefreshClean BufferActionKind = "refresh-clean"
	BufferActionFollowRename BufferActionKind = "follow-rename"
	BufferActionCloseDeleted BufferActionKind = "close-deleted"
	BufferActionConflict     BufferActionKind = "conflict"
)

// BufferSyncAction is one instruction produced by ScanAndSync for an open
// buffer.
type BufferSyncAction struct {
	BufferID   string
	EntityKind SyncEntityKind
	EntityID   string
	Action     BufferActionKind
	OldPath    string
	NewPath    string
	NewDigest  string
	Conflict   *SyncConflict
}

// SyncConflict holds all three versions ("reading baseline", "disk version",
// and "unsaved edited version") when an external edit, delete, or rename+edit
// collides with a dirty buffer.
type SyncConflict struct {
	BufferID       string
	EntityKind     SyncEntityKind
	ConflictKind   ConflictStateKind
	EntityID       string
	OldPath        string
	RelPath        string
	BaselineDigest string
	BaselineBytes  []byte
	DiskExists     bool
	DiskDigest     string
	DiskBytes      []byte
	EditedBytes    []byte
	HasGitConflict bool
	ParseErr       string
}

// Clone returns a deep copy of c so callers/UI cannot mutate engine state by
// accident.
func (c *SyncConflict) Clone() *SyncConflict {
	if c == nil {
		return nil
	}
	cp := *c
	cp.BaselineBytes = cloneBytes(c.BaselineBytes)
	cp.DiskBytes = cloneBytes(c.DiskBytes)
	cp.EditedBytes = cloneBytes(c.EditedBytes)
	return &cp
}

// OpenBuffer describes an open editor tab/draft in pure Go terms so Core can
// decide clean refresh vs. three-version conflict without importing GUI types.
type OpenBuffer struct {
	BufferID       string
	EntityKind     SyncEntityKind
	EntityID       string
	RelPath        string
	Dirty          bool
	BaselineDigest string
	BaselineBytes  []byte
	EditedBytes    func() ([]byte, error)
}

// ResolutionKind chooses how a user resolves a SyncConflict.
type ResolutionKind string

const (
	ResolutionReloadDisk   ResolutionKind = "reload-disk"
	ResolutionAcceptDelete ResolutionKind = "accept-delete"
	ResolutionKeepEdited   ResolutionKind = "keep-edited"
	ResolutionApplyMerged  ResolutionKind = "apply-merged"
	ResolutionSaveAsCopy   ResolutionKind = "save-as-copy"
)

// ResolutionResult reports the outcome of resolving a SyncConflict.
type ResolutionResult struct {
	BufferID     string
	Action       ResolutionKind
	RelPath      string
	NewDigest    string
	SavedBytes   []byte
	CopyRequest  *RequestEntry
	CopyDoc      *DocEntry
	ClosedBuffer bool
}

// SyncReport summarizes one ScanAndSync pass.
type SyncReport struct {
	Events        []SyncEvent
	BufferActions []BufferSyncAction
	Conflicts     []*SyncConflict
	Issues        []Issue
}

// SyncOptions configures a SyncEngine.
type SyncOptions struct {
	Limits        StoreLimits
	OpenBuffersFn func() []OpenBuffer
}

// DiskFileRecord captures one tracked project file in a DiskSnapshot.
type DiskFileRecord struct {
	RelPath        string
	Kind           SyncEntityKind
	EntityID       string
	Digest         string
	Bytes          []byte
	ModTimeNano    int64
	HasGitConflict bool
	ParseErr       string

	parsedReq  *Request
	parsedEnv  *Environment
	parsedCol  *Collection
	parsedAuth *AuthScheme
	parsedProj *ProjectFile
}

// DiskSnapshot is a complete point-in-time read of tracked files under
// .gorequest/.
type DiskSnapshot struct {
	Files  map[string]*DiskFileRecord // relPath -> record
	Issues []Issue
}

// Optional hooks upgraded by yaml_preserve.go and yaml_strict.go.
var (
	encodeRequestPreservingHook func(original []byte, r *Request) ([]byte, error)
	decodeYAMLStrictCheckHook   func(rel string, kind SyncEntityKind, data []byte) error
)

// EncodeRequestAgainstBaseline serializes r while preserving comments, key
// ordering, and untouched fields from baselineYAML when available.
func EncodeRequestAgainstBaseline(baselineYAML []byte, r *Request) ([]byte, error) {
	if r == nil {
		return nil, configErrorf("request is nil")
	}
	c := *r
	if c.SchemaVersion == 0 {
		c.SchemaVersion = SupportedSchemaVersion
	}
	if encodeRequestPreservingHook != nil && len(baselineYAML) > 0 {
		return encodeRequestPreservingHook(baselineYAML, &c)
	}
	return encodeYAML(&c)
}

// SyncEngine coordinates external file detection, clean in-memory refresh,
// baseline byte retention, and dirty three-version conflict protection for one
// loaded Project.
type SyncEngine struct {
	project       *Project
	limits        StoreLimits
	openBuffersFn func() []OpenBuffer

	lastSnap *DiskSnapshot
	// baselines caches raw bytes keyed by digest and by relPath so a tab that
	// only stored BaselineDigest can still recover its exact BaselineBytes for
	// three-version conflict view and 3-way merge.
	bytesByDigest map[string][]byte
	bytesByPath   map[string][]byte
	conflicts     map[string]*SyncConflict // bufferID -> active conflict
}

// NewSyncEngine initializes a SyncEngine from p's current on-disk files without
// writing anything to disk or performing network I/O.
func (p *Project) NewSyncEngine(opts SyncOptions) (*SyncEngine, error) {
	if p == nil || p.Store == nil {
		return nil, configErrorf("project or store is nil")
	}
	lim := opts.Limits
	if lim == (StoreLimits{}) {
		lim = p.Store.EffectiveLimits()
	} else {
		lim = lim.Effective()
	}
	eng := &SyncEngine{
		project:       p,
		limits:        lim,
		openBuffersFn: opts.OpenBuffersFn,
		bytesByDigest: map[string][]byte{},
		bytesByPath:   map[string][]byte{},
		conflicts:     map[string]*SyncConflict{},
	}
	snap, err := ScanDiskSnapshot(p.Store, lim)
	if err != nil {
		return nil, err
	}
	eng.lastSnap = snap
	for rel, rec := range snap.Files {
		eng.rememberBaseline(rel, rec.Digest, rec.Bytes)
	}
	// If OpenProject skipped valid entities that use top-level extensions: (before
	// core/model.go carries Extensions directly), reconcile snap into p in memory
	// without touching disk.
	if snapshotRecoversUnloadedEntities(p, snap) {
		eng.applySnapshotToProject(snap, nil, nil, nil)
	}
	return eng, nil
}

func snapshotRecoversUnloadedEntities(p *Project, snap *DiskSnapshot) bool {
	if p == nil || snap == nil {
		return false
	}
	for _, rec := range snap.Files {
		if rec == nil || rec.ParseErr != "" || rec.HasGitConflict {
			continue
		}
		switch rec.Kind {
		case EntityRequest:
			if rec.EntityID != "" && p.Requests[rec.EntityID] == nil {
				return true
			}
		case EntityEnvironment:
			if rec.EntityID != "" && p.Environments[rec.EntityID] == nil {
				return true
			}
		case EntityAuth:
			if rec.EntityID != "" && p.AuthSchemes[rec.EntityID] == nil {
				return true
			}
		}
	}
	return false
}

func (e *SyncEngine) rememberBaseline(rel, digest string, data []byte) {
	if digest == "" || data == nil {
		return
	}
	cp := cloneBytes(data)
	e.bytesByDigest[digest] = cp
	if rel != "" {
		e.bytesByPath[rel] = cp
	}
}

// BaselineBytes returns the cached raw bytes for baselineDigest (falling back
// to relPath if digest is empty), or nil if unknown.
func (e *SyncEngine) BaselineBytes(relPath, baselineDigest string) []byte {
	if e == nil {
		return nil
	}
	if baselineDigest != "" {
		if b, ok := e.bytesByDigest[baselineDigest]; ok {
			return cloneBytes(b)
		}
	}
	if relPath != "" {
		if b, ok := e.bytesByPath[relPath]; ok {
			return cloneBytes(b)
		}
	}
	return nil
}

// SetOpenBuffersProvider sets or replaces the callback used by ScanAndSync to
// inspect open editor buffers.
func (e *SyncEngine) SetOpenBuffersProvider(fn func() []OpenBuffer) {
	e.openBuffersFn = fn
}

// RecordLocalSave informs the sync engine that GoRequest itself just wrote data
// to relPath, updating the snapshot and baseline cache so the next watcher scan
// does not treat our own save as an external edit.
func (e *SyncEngine) RecordLocalSave(relPath string, data []byte) {
	if e == nil {
		return
	}
	norm, err := e.project.Store.ResolvePath(relPath)
	if err != nil {
		norm = filepath.ToSlash(relPath)
	}
	rec := buildDiskRecord(norm, data, 0)
	if e.lastSnap == nil {
		e.lastSnap = &DiskSnapshot{Files: map[string]*DiskFileRecord{}}
	}
	if rec != nil {
		e.lastSnap.Files[norm] = rec
		e.rememberBaseline(norm, rec.Digest, rec.Bytes)
	}
}

// RecordLocalMove informs the sync engine that GoRequest itself moved oldRel to
// newRel with content data.
func (e *SyncEngine) RecordLocalMove(oldRel, newRel string, data []byte) {
	if e == nil {
		return
	}
	oldNorm := filepath.ToSlash(oldRel)
	if n, err := e.project.Store.ResolvePath(oldRel); err == nil {
		oldNorm = n
	}
	if e.lastSnap != nil {
		delete(e.lastSnap.Files, oldNorm)
	}
	delete(e.bytesByPath, oldNorm)
	e.RecordLocalSave(newRel, data)
}

// RecordLocalDelete informs the sync engine that GoRequest itself deleted relPath.
func (e *SyncEngine) RecordLocalDelete(relPath string) {
	if e == nil {
		return
	}
	norm := filepath.ToSlash(relPath)
	if n, err := e.project.Store.ResolvePath(relPath); err == nil {
		norm = n
	}
	if e.lastSnap != nil {
		delete(e.lastSnap.Files, norm)
	}
	delete(e.bytesByPath, norm)
}

// Conflicts returns deep copies of all unresolved conflicts sorted by BufferID.
func (e *SyncEngine) Conflicts() []*SyncConflict {
	if e == nil || len(e.conflicts) == 0 {
		return nil
	}
	ids := make([]string, 0, len(e.conflicts))
	for id := range e.conflicts {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]*SyncConflict, 0, len(ids))
	for _, id := range ids {
		out = append(out, e.conflicts[id].Clone())
	}
	return out
}

// ConflictFor returns a copy of the active conflict for bufferID, or nil.
func (e *SyncEngine) ConflictFor(bufferID string) *SyncConflict {
	if e == nil {
		return nil
	}
	return e.conflicts[bufferID].Clone()
}

// ClearConflict removes any conflict recorded for bufferID (used when the user
// explicitly discards/closes a dirty tab).
func (e *SyncEngine) ClearConflict(bufferID string) {
	if e == nil {
		return
	}
	delete(e.conflicts, bufferID)
}

// CaptureSaveConflict builds and registers a three-version SyncConflict when an
// explicit Save attempt hits a *ConflictError before the periodic watcher ran.
func (e *SyncEngine) CaptureSaveConflict(buf OpenBuffer) (*SyncConflict, error) {
	if e == nil {
		return nil, configErrorf("sync engine is nil")
	}
	norm, err := e.project.Store.ResolvePath(buf.RelPath)
	if err != nil {
		return nil, &ConfigError{Err: err}
	}
	baseBytes := buf.BaselineBytes
	if len(baseBytes) == 0 {
		baseBytes = e.BaselineBytes(norm, buf.BaselineDigest)
	}
	var edited []byte
	if buf.EditedBytes != nil {
		edited, err = buf.EditedBytes()
		if err != nil {
			return nil, err
		}
	}
	maxB := e.limits.MaxYAMLBytes
	if buf.EntityKind == EntityDoc {
		maxB = e.limits.MaxDocBytes
	}
	diskBytes, diskDigest, rerr := e.project.Store.ReadBounded(norm, maxB)
	conflict := &SyncConflict{
		BufferID:       buf.BufferID,
		EntityKind:     buf.EntityKind,
		EntityID:       buf.EntityID,
		RelPath:        norm,
		BaselineDigest: buf.BaselineDigest,
		BaselineBytes:  cloneBytes(baseBytes),
		EditedBytes:    cloneBytes(edited),
	}
	switch {
	case os.IsNotExist(rerr):
		conflict.ConflictKind = ConflictDeleted
		conflict.DiskExists = false
	case rerr != nil:
		return nil, rerr
	default:
		conflict.ConflictKind = ConflictModified
		conflict.DiskExists = true
		conflict.DiskDigest = diskDigest
		conflict.DiskBytes = diskBytes
		rec := buildDiskRecord(norm, diskBytes, 0)
		if rec != nil {
			conflict.HasGitConflict = rec.HasGitConflict
			conflict.ParseErr = rec.ParseErr
		}
	}
	e.conflicts[buf.BufferID] = conflict
	return conflict.Clone(), nil
}

// CanAutoSave reports whether an automatic timer save may safely write bufferID.
// It returns false whenever a conflict is active, the file was deleted on disk
// (preventing silent resurrection), or the on-disk digest differs from
// baselineDigest.
func (e *SyncEngine) CanAutoSave(bufferID, relPath, baselineDigest string) bool {
	if e == nil || e.project == nil || e.project.Store == nil {
		return false
	}
	if _, has := e.conflicts[bufferID]; has {
		return false
	}
	if relPath == "" || baselineDigest == "" {
		return false
	}
	_, diskDigest, err := e.project.Store.ReadBounded(relPath, e.limits.MaxDocBytes)
	if err != nil {
		return false
	}
	return diskDigest == baselineDigest
}

// classifyProjectFile maps a slash-separated project-relative path to its
// tracked SyncEntityKind, or ("", false) for untracked files.
func classifyProjectFile(rel string) (SyncEntityKind, bool) {
	rel = filepath.ToSlash(rel)
	base := baseNameSlash(rel)
	if IsTempOrIgnoredName(base) {
		return "", false
	}
	if rel == "project.yaml" {
		return EntityProject, true
	}
	switch {
	case strings.HasPrefix(rel, "collections/"):
		if base == "collection.yaml" {
			return EntityCollection, true
		}
		if strings.HasSuffix(base, ".request.yaml") {
			return EntityRequest, true
		}
	case strings.HasPrefix(rel, "environments/"):
		// Only direct children of environments/ are loaded by OpenProject.
		rest := strings.TrimPrefix(rel, "environments/")
		if !strings.Contains(rest, "/") && (strings.HasSuffix(base, ".yaml") || strings.HasSuffix(base, ".yml")) {
			return EntityEnvironment, true
		}
	case strings.HasPrefix(rel, "auth/"):
		rest := strings.TrimPrefix(rel, "auth/")
		if !strings.Contains(rest, "/") && strings.HasSuffix(base, ".auth.yaml") {
			return EntityAuth, true
		}
	case strings.HasPrefix(rel, "docs/"):
		if strings.HasSuffix(strings.ToLower(base), ".md") {
			return EntityDoc, true
		}
	}
	return "", false
}

// ScanDiskSnapshot scans s.Root for all tracked GoRequest files while enforcing
// path confinement, symlink escape rejection, file count caps, and per-file
// size limits. It never writes to disk or accesses the network.
func ScanDiskSnapshot(s *Store, limits StoreLimits) (*DiskSnapshot, error) {
	if s == nil {
		return nil, configErrorf("store is nil")
	}
	lim := limits.Effective()
	snap := &DiskSnapshot{
		Files: map[string]*DiskFileRecord{},
	}

	fileCount := 0
	walkErr := filepath.WalkDir(s.Root, func(absPath string, d os.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if absPath == s.Root {
			return nil
		}
		name := d.Name()
		if IsTempOrIgnoredName(name) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		relOS, err := filepath.Rel(s.Root, absPath)
		if err != nil {
			return err
		}
		rel := filepath.ToSlash(relOS)

		// Reject symlinked directories or files escaping Root or dangling.
		if d.Type()&os.ModeSymlink != 0 {
			if _, rerr := s.resolve(rel); rerr != nil {
				snap.Issues = append(snap.Issues, Issue{
					Level:   "error",
					Code:    "symlink-escape",
					Path:    rel,
					Message: rerr.Error(),
				})
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
		}
		if d.IsDir() {
			// Skip local ignored output dirs inside .gorequest if any exist.
			if strings.HasPrefix(name, ".gorequest-") || name == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		kind, ok := classifyProjectFile(rel)
		if !ok {
			return nil
		}
		fileCount++
		if fileCount > lim.MaxTrackedFiles {
			snap.Issues = append(snap.Issues, Issue{
				Level:   "error",
				Code:    "file-count-limit",
				Path:    rel,
				Message: fmt.Sprintf("tracked file count exceeds limit (%d)", lim.MaxTrackedFiles),
			})
			return filepath.SkipAll
		}

		maxBytes := lim.MaxYAMLBytes
		if kind == EntityDoc {
			maxBytes = lim.MaxDocBytes
		}
		info, ierr := d.Info()
		if ierr != nil {
			if os.IsNotExist(ierr) {
				return nil
			}
			return ierr
		}
		if !info.Mode().IsRegular() {
			snap.Issues = append(snap.Issues, Issue{
				Level:   "error",
				Code:    "non-regular-file",
				Path:    rel,
				Message: "tracked file must be a regular file",
			})
			return nil
		}
		if info.Size() > maxBytes {
			snap.Issues = append(snap.Issues, Issue{
				Level:   "error",
				Code:    "file-size-limit",
				Path:    rel,
				Message: fmt.Sprintf("file size %d exceeds limit %d bytes", info.Size(), maxBytes),
			})
			return nil
		}
		data, _, rerr := s.ReadBounded(rel, maxBytes)
		if rerr != nil {
			if os.IsNotExist(rerr) {
				return nil
			}
			snap.Issues = append(snap.Issues, Issue{
				Level:   "error",
				Code:    "file-read",
				Path:    rel,
				Message: rerr.Error(),
			})
			return nil
		}
		rec := buildDiskRecord(rel, data, info.ModTime().UnixNano())
		if rec != nil {
			snap.Files[rel] = rec
		}
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	return snap, nil
}

func buildDiskRecord(rel string, data []byte, modNano int64) *DiskFileRecord {
	kind, ok := classifyProjectFile(rel)
	if !ok {
		return nil
	}
	rec := &DiskFileRecord{
		RelPath:     rel,
		Kind:        kind,
		Digest:      Digest(data),
		Bytes:       cloneBytes(data),
		ModTimeNano: modNano,
	}
	if line, hasConflict := DetectGitConflictMarkers(data); hasConflict {
		rec.HasGitConflict = true
		if kind != EntityDoc {
			rec.ParseErr = fmt.Sprintf("unresolved Git merge conflict markers at line %d", line)
			return rec
		}
	}
	if kind == EntityDoc {
		return rec
	}
	if decodeYAMLStrictCheckHook != nil {
		if err := decodeYAMLStrictCheckHook(rel, kind, data); err != nil {
			rec.ParseErr = err.Error()
			// Still try best-effort ID extraction when only a non-ID field failed
			// so rename matching does not lose the stable ID unnecessarily.
			rec.EntityID = bestEffortYAMLID(kind, rel, data)
			return rec
		}
	}
	switch kind {
	case EntityProject:
		var pf ProjectFile
		if err := decodeStrict(data, &pf); err != nil {
			rec.ParseErr = err.Error()
			return rec
		}
		if pf.SchemaVersion != SupportedSchemaVersion {
			rec.ParseErr = fmt.Sprintf("schemaVersion %d unsupported (this build reads %d)", pf.SchemaVersion, SupportedSchemaVersion)
			rec.EntityID = pf.ID
			return rec
		}
		rec.EntityID = pf.ID
		rec.parsedProj = &pf

	case EntityRequest:
		var req Request
		if err := decodeStrictForSync(data, &req); err != nil {
			rec.ParseErr = err.Error()
			rec.EntityID = bestEffortYAMLID(kind, rel, data)
			return rec
		}
		if req.ID == "" {
			rec.ParseErr = "request has no id"
			return rec
		}
		rec.EntityID = req.ID
		rec.parsedReq = &req

	case EntityEnvironment:
		var env Environment
		if err := decodeStrictForSync(data, &env); err != nil {
			rec.ParseErr = err.Error()
			rec.EntityID = bestEffortYAMLID(kind, rel, data)
			return rec
		}
		if env.ID == "" {
			base := baseNameSlash(rel)
			env.ID = strings.TrimSuffix(base, filepath.Ext(base))
		}
		rec.EntityID = env.ID
		rec.parsedEnv = &env

	case EntityCollection:
		var col Collection
		if err := decodeStrictForSync(data, &col); err != nil {
			rec.ParseErr = err.Error()
			return rec
		}
		rec.EntityID = col.ID
		rec.parsedCol = &col

	case EntityAuth:
		var auth AuthScheme
		if err := decodeStrictForSync(data, &auth); err != nil {
			rec.ParseErr = err.Error()
			rec.EntityID = bestEffortYAMLID(kind, rel, data)
			return rec
		}
		if auth.ID == "" {
			rec.ParseErr = "auth scheme has no id"
			return rec
		}
		rec.EntityID = auth.ID
		rec.parsedAuth = &auth
	}
	return rec
}

var decodeStrictExtensionStripper func(data []byte, out any) error

func decodeStrictForSync(data []byte, out any) error {
	if decodeStrictExtensionStripper != nil {
		return decodeStrictExtensionStripper(data, out)
	}
	return decodeStrict(data, out)
}

func bestEffortYAMLID(kind SyncEntityKind, rel string, data []byte) string {
	for _, raw := range bytes.Split(data, []byte{'\n'}) {
		line := strings.TrimSpace(string(bytes.TrimSuffix(raw, []byte{'\r'})))
		if strings.HasPrefix(line, "id:") {
			v := strings.TrimSpace(strings.TrimPrefix(line, "id:"))
			v = strings.Trim(v, `"'`)
			if v != "" && !strings.ContainsAny(v, " \t#") {
				return v
			}
		}
	}
	if kind == EntityEnvironment {
		base := baseNameSlash(rel)
		return strings.TrimSuffix(base, filepath.Ext(base))
	}
	return ""
}

// DiffSnapshots compares prev and next and produces deterministic SyncEvents,
// pairing disappeared and appeared files into SyncRenamed when their stable
// entity ID (or content digest / strong doc similarity) matches unambiguously.
func DiffSnapshots(prev, next *DiskSnapshot) []SyncEvent {
	var prevFiles, nextFiles map[string]*DiskFileRecord
	if prev != nil {
		prevFiles = prev.Files
	}
	if next != nil {
		nextFiles = next.Files
	}

	var removed []*DiskFileRecord
	var added []*DiskFileRecord
	var events []SyncEvent

	// Collect existing keys in deterministic order.
	allPaths := map[string]struct{}{}
	for k := range prevFiles {
		allPaths[k] = struct{}{}
	}
	for k := range nextFiles {
		allPaths[k] = struct{}{}
	}
	sortedPaths := make([]string, 0, len(allPaths))
	for k := range allPaths {
		sortedPaths = append(sortedPaths, k)
	}
	sort.Strings(sortedPaths)

	for _, rel := range sortedPaths {
		oldRec, hadOld := prevFiles[rel]
		newRec, hasNew := nextFiles[rel]
		switch {
		case hadOld && hasNew:
			if oldRec.Digest != newRec.Digest {
				entityID := newRec.EntityID
				if entityID == "" {
					entityID = oldRec.EntityID
				}
				events = append(events, SyncEvent{
					Kind:           SyncModified,
					EntityKind:     newRec.Kind,
					EntityID:       entityID,
					Path:           rel,
					OldDigest:      oldRec.Digest,
					NewDigest:      newRec.Digest,
					ContentChanged: true,
					HasGitConflict: newRec.HasGitConflict,
					ParseErr:       newRec.ParseErr,
				})
			}
		case hadOld && !hasNew:
			removed = append(removed, oldRec)
		case !hadOld && hasNew:
			added = append(added, newRec)
		}
	}

	removedUsed := make([]bool, len(removed))
	addedUsed := make([]bool, len(added))

	// Pass 1: Match structured entities by (Kind, non-empty unique EntityID).
	removedByKey := map[string][]int{}
	addedByKey := map[string][]int{}
	for i, r := range removed {
		if r.EntityID != "" && r.Kind != EntityDoc {
			k := string(r.Kind) + ":" + r.EntityID
			removedByKey[k] = append(removedByKey[k], i)
		}
	}
	for i, a := range added {
		if a.EntityID != "" && a.Kind != EntityDoc {
			k := string(a.Kind) + ":" + a.EntityID
			addedByKey[k] = append(addedByKey[k], i)
		}
	}
	for k, rIdxs := range removedByKey {
		aIdxs := addedByKey[k]
		if len(rIdxs) == 1 && len(aIdxs) == 1 {
			ri, ai := rIdxs[0], aIdxs[0]
			oldRec, newRec := removed[ri], added[ai]
			removedUsed[ri] = true
			addedUsed[ai] = true
			events = append(events, SyncEvent{
				Kind:           SyncRenamed,
				EntityKind:     newRec.Kind,
				EntityID:       newRec.EntityID,
				OldPath:        oldRec.RelPath,
				Path:           newRec.RelPath,
				OldDigest:      oldRec.Digest,
				NewDigest:      newRec.Digest,
				ContentChanged: oldRec.Digest != newRec.Digest,
				HasGitConflict: newRec.HasGitConflict,
				ParseErr:       newRec.ParseErr,
			})
		}
	}

	// Pass 2: Match remaining removed+added of the same Kind by exact Digest.
	for ri, oldRec := range removed {
		if removedUsed[ri] {
			continue
		}
		for ai, newRec := range added {
			if addedUsed[ai] || newRec.Kind != oldRec.Kind {
				continue
			}
			if oldRec.Digest == newRec.Digest {
				removedUsed[ri] = true
				addedUsed[ai] = true
				id := newRec.EntityID
				if id == "" {
					id = oldRec.EntityID
				}
				events = append(events, SyncEvent{
					Kind:           SyncRenamed,
					EntityKind:     newRec.Kind,
					EntityID:       id,
					OldPath:        oldRec.RelPath,
					Path:           newRec.RelPath,
					OldDigest:      oldRec.Digest,
					NewDigest:      newRec.Digest,
					ContentChanged: false,
					HasGitConflict: newRec.HasGitConflict,
					ParseErr:       newRec.ParseErr,
				})
				break
			}
		}
	}

	// Pass 3: Unambiguous 1-to-1 Markdown rename+edit with strong line overlap.
	var remDocs, addDocs []int
	for ri, oldRec := range removed {
		if !removedUsed[ri] && oldRec.Kind == EntityDoc {
			remDocs = append(remDocs, ri)
		}
	}
	for ai, newRec := range added {
		if !addedUsed[ai] && newRec.Kind == EntityDoc {
			addDocs = append(addDocs, ai)
		}
	}
	if len(remDocs) == 1 && len(addDocs) == 1 {
		oldRec := removed[remDocs[0]]
		newRec := added[addDocs[0]]
		if docLooksRenamedWithEdit(oldRec.Bytes, newRec.Bytes) {
			removedUsed[remDocs[0]] = true
			addedUsed[addDocs[0]] = true
			events = append(events, SyncEvent{
				Kind:           SyncRenamed,
				EntityKind:     EntityDoc,
				OldPath:        oldRec.RelPath,
				Path:           newRec.RelPath,
				OldDigest:      oldRec.Digest,
				NewDigest:      newRec.Digest,
				ContentChanged: true,
				HasGitConflict: newRec.HasGitConflict,
			})
		}
	}

	// Emit remaining deletions and creations.
	for ri, oldRec := range removed {
		if removedUsed[ri] {
			continue
		}
		events = append(events, SyncEvent{
			Kind:       SyncDeleted,
			EntityKind: oldRec.Kind,
			EntityID:   oldRec.EntityID,
			Path:       oldRec.RelPath,
			OldDigest:  oldRec.Digest,
		})
	}
	for ai, newRec := range added {
		if addedUsed[ai] {
			continue
		}
		events = append(events, SyncEvent{
			Kind:           SyncCreated,
			EntityKind:     newRec.Kind,
			EntityID:       newRec.EntityID,
			Path:           newRec.RelPath,
			NewDigest:      newRec.Digest,
			ContentChanged: true,
			HasGitConflict: newRec.HasGitConflict,
			ParseErr:       newRec.ParseErr,
		})
	}

	sort.Slice(events, func(i, j int) bool {
		if events[i].Path == events[j].Path {
			return events[i].Kind < events[j].Kind
		}
		return events[i].Path < events[j].Path
	})
	return events
}

func docLooksRenamedWithEdit(oldBytes, newBytes []byte) bool {
	oldLines := nonEmptyTrimmedLines(oldBytes)
	newLines := nonEmptyTrimmedLines(newBytes)
	if len(oldLines) < 2 || len(newLines) < 2 {
		return false
	}
	counts := map[string]int{}
	for _, l := range oldLines {
		counts[l]++
	}
	shared := 0
	for _, l := range newLines {
		if counts[l] > 0 {
			shared++
			counts[l]--
		}
	}
	if shared < 2 {
		return false
	}
	maxLen := len(oldLines)
	if len(newLines) > maxLen {
		maxLen = len(newLines)
	}
	return float64(shared)/float64(maxLen) >= 0.60
}

func nonEmptyTrimmedLines(b []byte) []string {
	var out []string
	for _, raw := range bytes.Split(b, []byte{'\n'}) {
		s := strings.TrimSpace(string(bytes.TrimSuffix(raw, []byte{'\r'})))
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// ScanAndSync scans .gorequest/, diffs against the engine's last known disk
// snapshot, refreshes clean Project state in memory, and isolates dirty open
// buffers into three-version SyncConflicts without overwriting disk or losing
// unsaved edits.
func (e *SyncEngine) ScanAndSync() (*SyncReport, error) {
	if e == nil || e.project == nil || e.project.Store == nil {
		return nil, configErrorf("sync engine or project is nil")
	}
	nextSnap, err := ScanDiskSnapshot(e.project.Store, e.limits)
	if err != nil {
		return nil, err
	}
	events := DiffSnapshots(e.lastSnap, nextSnap)
	var buffers []OpenBuffer
	if e.openBuffersFn != nil {
		buffers = e.openBuffersFn()
	}

	bufByPath := map[string]OpenBuffer{}
	bufByReqID := map[string]OpenBuffer{}
	bufByEnvID := map[string]OpenBuffer{}
	for _, b := range buffers {
		norm := filepath.ToSlash(b.RelPath)
		if norm != "" {
			bufByPath[norm] = b
		}
		if b.EntityKind == EntityRequest && b.EntityID != "" {
			bufByReqID[b.EntityID] = b
		}
		if b.EntityKind == EntityEnvironment && b.EntityID != "" {
			bufByEnvID[b.EntityID] = b
		}
	}

	findBuffer := func(ev SyncEvent) (OpenBuffer, bool) {
		if ev.EntityKind == EntityRequest && ev.EntityID != "" {
			if b, ok := bufByReqID[ev.EntityID]; ok {
				return b, true
			}
		}
		if ev.EntityKind == EntityEnvironment && ev.EntityID != "" {
			if b, ok := bufByEnvID[ev.EntityID]; ok {
				return b, true
			}
		}
		if ev.OldPath != "" {
			if b, ok := bufByPath[ev.OldPath]; ok {
				return b, true
			}
		}
		if b, ok := bufByPath[ev.Path]; ok {
			return b, true
		}
		return OpenBuffer{}, false
	}

	var actions []BufferSyncAction
	protectedDirtyReqIDs := map[string]bool{}
	protectedDirtyEnvIDs := map[string]bool{}
	renamedDirtyReqPaths := map[string]string{} // reqID -> newRelPath

	for _, ev := range events {
		buf, hasBuf := findBuffer(ev)
		if !hasBuf {
			continue
		}

		// Suppress self-write events when buffer is clean and already at NewDigest.
		if !buf.Dirty && ev.NewDigest != "" && buf.BaselineDigest == ev.NewDigest && ev.Kind == SyncModified {
			continue
		}

		switch ev.Kind {
		case SyncModified:
			newRec := nextSnap.Files[ev.Path]
			if !buf.Dirty {
				// Clean buffer: if disk version is valid (or a doc), refresh cleanly!
				if newRec != nil && (newRec.ParseErr == "" || ev.EntityKind == EntityDoc) {
					delete(e.conflicts, buf.BufferID)
					actions = append(actions, BufferSyncAction{
						BufferID:   buf.BufferID,
						EntityKind: ev.EntityKind,
						EntityID:   ev.EntityID,
						Action:     BufferActionRefreshClean,
						NewPath:    ev.Path,
						NewDigest:  ev.NewDigest,
					})
				}
				continue
			}
			// Dirty buffer + external modification => three-version conflict!
			if ev.EntityKind == EntityRequest && ev.EntityID != "" {
				protectedDirtyReqIDs[ev.EntityID] = true
			}
			if ev.EntityKind == EntityEnvironment && ev.EntityID != "" {
				protectedDirtyEnvIDs[ev.EntityID] = true
			}
			c := e.recordConflictFromEvent(buf, ConflictModified, "", ev.Path, newRec)
			actions = append(actions, BufferSyncAction{
				BufferID:   buf.BufferID,
				EntityKind: ev.EntityKind,
				EntityID:   ev.EntityID,
				Action:     BufferActionConflict,
				NewPath:    ev.Path,
				NewDigest:  ev.NewDigest,
				Conflict:   c.Clone(),
			})

		case SyncDeleted:
			if !buf.Dirty {
				delete(e.conflicts, buf.BufferID)
				actions = append(actions, BufferSyncAction{
					BufferID:   buf.BufferID,
					EntityKind: ev.EntityKind,
					EntityID:   ev.EntityID,
					Action:     BufferActionCloseDeleted,
					OldPath:    ev.Path,
				})
				continue
			}
			// Dirty buffer + external delete => keep dirty buffer alive in
			// ConflictDeleted state; never resurrect on disk!
			if ev.EntityKind == EntityRequest && ev.EntityID != "" {
				protectedDirtyReqIDs[ev.EntityID] = true
			}
			if ev.EntityKind == EntityEnvironment && ev.EntityID != "" {
				protectedDirtyEnvIDs[ev.EntityID] = true
			}
			c := e.recordConflictFromEvent(buf, ConflictDeleted, ev.Path, ev.Path, nil)
			actions = append(actions, BufferSyncAction{
				BufferID:   buf.BufferID,
				EntityKind: ev.EntityKind,
				EntityID:   ev.EntityID,
				Action:     BufferActionConflict,
				OldPath:    ev.Path,
				NewPath:    ev.Path,
				Conflict:   c.Clone(),
			})

		case SyncRenamed:
			newRec := nextSnap.Files[ev.Path]
			if !buf.Dirty {
				delete(e.conflicts, buf.BufferID)
				act := BufferActionFollowRename
				if ev.ContentChanged && newRec != nil && (newRec.ParseErr == "" || ev.EntityKind == EntityDoc) {
					act = BufferActionRefreshClean
				}
				actions = append(actions, BufferSyncAction{
					BufferID:   buf.BufferID,
					EntityKind: ev.EntityKind,
					EntityID:   ev.EntityID,
					Action:     act,
					OldPath:    ev.OldPath,
					NewPath:    ev.Path,
					NewDigest:  ev.NewDigest,
				})
				continue
			}
			// Dirty buffer + external rename:
			// Always follow NewPath so OldPath is never resurrected!
			if ev.EntityKind == EntityRequest && ev.EntityID != "" {
				protectedDirtyReqIDs[ev.EntityID] = true
				renamedDirtyReqPaths[ev.EntityID] = ev.Path
			}
			if !ev.ContentChanged && ev.NewDigest == buf.BaselineDigest {
				// Pure rename without content conflict: dirty edits remain valid
				// against NewDigest at NewPath!
				if existing := e.conflicts[buf.BufferID]; existing != nil {
					existing.OldPath = ev.OldPath
					existing.RelPath = ev.Path
				}
				actions = append(actions, BufferSyncAction{
					BufferID:   buf.BufferID,
					EntityKind: ev.EntityKind,
					EntityID:   ev.EntityID,
					Action:     BufferActionFollowRename,
					OldPath:    ev.OldPath,
					NewPath:    ev.Path,
					NewDigest:  ev.NewDigest,
				})
				continue
			}
			// Renamed AND content changed while dirty => three-version conflict at NewPath!
			c := e.recordConflictFromEvent(buf, ConflictRenamedModified, ev.OldPath, ev.Path, newRec)
			actions = append(actions, BufferSyncAction{
				BufferID:   buf.BufferID,
				EntityKind: ev.EntityKind,
				EntityID:   ev.EntityID,
				Action:     BufferActionConflict,
				OldPath:    ev.OldPath,
				NewPath:    ev.Path,
				NewDigest:  ev.NewDigest,
				Conflict:   c.Clone(),
			})
		}
	}

	// Also protect any pre-existing unresolved conflicts from earlier scans even
	// if no new filesystem delta occurred on this scan.
	for _, b := range buffers {
		if b.Dirty {
			if _, has := e.conflicts[b.BufferID]; has {
				if b.EntityKind == EntityRequest && b.EntityID != "" {
					protectedDirtyReqIDs[b.EntityID] = true
				}
				if b.EntityKind == EntityEnvironment && b.EntityID != "" {
					protectedDirtyEnvIDs[b.EntityID] = true
				}
			}
		}
	}

	if len(events) > 0 || len(nextSnap.Issues) != len(e.lastSnap.Issues) {
		e.applySnapshotToProject(nextSnap, protectedDirtyReqIDs, renamedDirtyReqPaths, protectedDirtyEnvIDs)
	}

	// Cache baselines for clean disk files.
	for rel, rec := range nextSnap.Files {
		e.bytesByDigest[rec.Digest] = cloneBytes(rec.Bytes)
		// Only overwrite bytesByPath if no unresolved conflict depends on the old path.
		e.bytesByPath[rel] = cloneBytes(rec.Bytes)
	}
	e.lastSnap = nextSnap

	return &SyncReport{
		Events:        events,
		BufferActions: actions,
		Conflicts:     e.Conflicts(),
		Issues:        append([]Issue(nil), e.project.Issues...),
	}, nil
}

func (e *SyncEngine) recordConflictFromEvent(buf OpenBuffer, kind ConflictStateKind, oldPath, newPath string, diskRec *DiskFileRecord) *SyncConflict {
	// Keep the original BaselineBytes if this buffer was already in conflict!
	var baseBytes []byte
	baseDigest := buf.BaselineDigest
	if prev, ok := e.conflicts[buf.BufferID]; ok && len(prev.BaselineBytes) > 0 {
		baseBytes = prev.BaselineBytes
		if baseDigest == "" {
			baseDigest = prev.BaselineDigest
		}
		if oldPath == "" && prev.OldPath != "" {
			oldPath = prev.OldPath
		}
	}
	if len(baseBytes) == 0 {
		baseBytes = buf.BaselineBytes
	}
	if len(baseBytes) == 0 {
		baseBytes = e.BaselineBytes(buf.RelPath, baseDigest)
	}
	var edited []byte
	if buf.EditedBytes != nil {
		if b, err := buf.EditedBytes(); err == nil {
			edited = b
		}
	}
	c := &SyncConflict{
		BufferID:       buf.BufferID,
		EntityKind:     buf.EntityKind,
		ConflictKind:   kind,
		EntityID:       buf.EntityID,
		OldPath:        oldPath,
		RelPath:        newPath,
		BaselineDigest: baseDigest,
		BaselineBytes:  cloneBytes(baseBytes),
		EditedBytes:    cloneBytes(edited),
	}
	if diskRec != nil {
		c.DiskExists = true
		c.DiskDigest = diskRec.Digest
		c.DiskBytes = cloneBytes(diskRec.Bytes)
		c.HasGitConflict = diskRec.HasGitConflict
		c.ParseErr = diskRec.ParseErr
	}
	e.conflicts[buf.BufferID] = c
	return c
}

// applySnapshotToProject updates e.project's in-memory structures for clean
// files while preserving RequestEntry/EnvironmentEntry pointers that belong to
// open tabs (updating clean ones in place, and keeping dirty conflicted ones
// bound to their user state without losing renamed paths).
func (e *SyncEngine) applySnapshotToProject(
	snap *DiskSnapshot,
	protectedReqIDs map[string]bool,
	renamedDirtyReqPaths map[string]string,
	protectedEnvIDs map[string]bool,
) {
	p := e.project
	var loadIssues []Issue
	loadIssues = append(loadIssues, snap.Issues...)

	// Gather sorted paths for deterministic issue ordering and ID dup checks.
	paths := make([]string, 0, len(snap.Files))
	for rel := range snap.Files {
		paths = append(paths, rel)
	}
	sort.Strings(paths)

	// 1. ProjectFile
	if rec, ok := snap.Files["project.yaml"]; ok {
		if rec.HasGitConflict {
			loadIssues = append(loadIssues, Issue{Level: "error", Code: "git-conflict", Path: rec.RelPath, Message: rec.ParseErr})
		} else if rec.ParseErr != "" {
			loadIssues = append(loadIssues, Issue{Level: "error", Code: "project-parse", Path: rec.RelPath, Message: rec.ParseErr})
		} else if rec.parsedProj != nil {
			pfCopy := *rec.parsedProj
			p.File = &pfCopy
		}
	} else {
		loadIssues = append(loadIssues, Issue{Level: "error", Code: "project-missing", Path: "project.yaml", Message: "project.yaml missing on disk"})
	}

	// 2. AuthSchemes
	newAuth := map[string]*AuthScheme{}
	for _, rel := range paths {
		rec := snap.Files[rel]
		if rec.Kind != EntityAuth {
			continue
		}
		if rec.HasGitConflict {
			loadIssues = append(loadIssues, Issue{Level: "error", Code: "git-conflict", Path: rel, Message: rec.ParseErr})
			continue
		}
		if rec.ParseErr != "" {
			code := "auth-parse"
			if rec.ParseErr == "auth scheme has no id" {
				code = "auth-id"
			}
			loadIssues = append(loadIssues, Issue{Level: "error", Code: code, Path: rel, Message: rec.ParseErr})
			continue
		}
		if _, dup := newAuth[rec.EntityID]; dup {
			loadIssues = append(loadIssues, Issue{Level: "error", Code: "auth-dup", Path: rel, Message: "duplicate auth id " + rec.EntityID})
			continue
		}
		sCopy := *rec.parsedAuth
		newAuth[rec.EntityID] = &sCopy
	}
	p.AuthSchemes = newAuth

	// 3. Environments (keep protected dirty entries intact if any)
	newEnvs := map[string]*Environment{}
	newEnvEntries := map[string]*EnvironmentEntry{}
	var newEnvOrder []string
	for _, rel := range paths {
		rec := snap.Files[rel]
		if rec.Kind != EntityEnvironment {
			continue
		}
		if rec.HasGitConflict {
			loadIssues = append(loadIssues, Issue{Level: "error", Code: "git-conflict", Path: rel, Message: rec.ParseErr})
			continue
		}
		if rec.ParseErr != "" {
			loadIssues = append(loadIssues, Issue{Level: "error", Code: "env-parse", Path: rel, Message: rec.ParseErr})
			continue
		}
		if _, dup := newEnvs[rec.EntityID]; dup {
			loadIssues = append(loadIssues, Issue{Level: "error", Code: "env-dup", Path: rel, Message: "duplicate environment id " + rec.EntityID})
			continue
		}
		if protectedEnvIDs[rec.EntityID] {
			if prevEntry, ok := p.EnvEntries[rec.EntityID]; ok {
				prevEntry.RelPath = rel
				newEnvEntries[rec.EntityID] = prevEntry
				newEnvs[rec.EntityID] = prevEntry.Environment
				newEnvOrder = append(newEnvOrder, rec.EntityID)
				continue
			}
		}
		envCopy := *rec.parsedEnv
		if prevEntry, ok := p.EnvEntries[rec.EntityID]; ok {
			prevEntry.Environment = &envCopy
			prevEntry.RelPath = rel
			prevEntry.BaselineDigest = rec.Digest
			newEnvEntries[rec.EntityID] = prevEntry
		} else {
			newEnvEntries[rec.EntityID] = &EnvironmentEntry{
				Environment:    &envCopy,
				RelPath:        rel,
				BaselineDigest: rec.Digest,
			}
		}
		newEnvs[rec.EntityID] = &envCopy
		newEnvOrder = append(newEnvOrder, rec.EntityID)
	}
	sort.Strings(newEnvOrder)
	p.Environments = newEnvs
	p.EnvEntries = newEnvEntries
	p.EnvOrder = newEnvOrder

	// 4. Collections
	colByDir := map[string]*Collection{}
	for _, rel := range paths {
		rec := snap.Files[rel]
		if rec.Kind != EntityCollection {
			continue
		}
		if rec.HasGitConflict {
			loadIssues = append(loadIssues, Issue{Level: "error", Code: "git-conflict", Path: rel, Message: rec.ParseErr})
			continue
		}
		if rec.ParseErr != "" {
			loadIssues = append(loadIssues, Issue{Level: "error", Code: "collection-parse", Path: rel, Message: rec.ParseErr})
			continue
		}
		cCopy := *rec.parsedCol
		colByDir[filepath.ToSlash(filepath.Dir(rel))] = &cCopy
	}
	p.colByDir = colByDir

	// 5. Requests: mutate existing *RequestEntry pointers in place so open clean
	// tabs keep a valid Entry pointer! For dirty conflicted requests:
	// - if still on disk (or renamed on disk), keep existing Entry pointer in
	//   p.Requests (updating RelPath + CollectionVars if renamed, WITHOUT
	//   overwriting Entry.BaselineDigest or Entry.Request!).
	// - if deleted on disk, remove from p.Requests tree (so project tree reflects
	//   disk deletion) while the dirty open tab retains its Entry pointer for
	//   explicit Restore / Save-As-Copy / Discard!
	newRequests := map[string]*RequestEntry{}
	var newOrder []string
	for _, rel := range paths {
		rec := snap.Files[rel]
		if rec.Kind != EntityRequest {
			continue
		}
		if rec.HasGitConflict {
			loadIssues = append(loadIssues, Issue{Level: "error", Code: "git-conflict", Path: rel, Message: rec.ParseErr})
			if rec.EntityID != "" {
				if prev, ok := p.Requests[rec.EntityID]; ok {
					prev.RelPath = rel
					prev.CollectionVars = p.collectionVarsFor(rel, colByDir)
					newRequests[rec.EntityID] = prev
					newOrder = append(newOrder, rec.EntityID)
				}
			}
			continue
		}
		if rec.ParseErr != "" {
			code := "request-parse"
			if rec.ParseErr == "request has no id" {
				code = "request-id"
			}
			loadIssues = append(loadIssues, Issue{Level: "error", Code: code, Path: rel, Message: rec.ParseErr})
			if rec.EntityID != "" {
				if prev, ok := p.Requests[rec.EntityID]; ok {
					prev.RelPath = rel
					prev.CollectionVars = p.collectionVarsFor(rel, colByDir)
					newRequests[rec.EntityID] = prev
					newOrder = append(newOrder, rec.EntityID)
				}
			}
			continue
		}
		id := rec.EntityID
		if _, dup := newRequests[id]; dup {
			loadIssues = append(loadIssues, Issue{Level: "error", Code: "request-dup", Path: rel, Message: "duplicate request id " + id})
			continue
		}
		if protectedReqIDs[id] {
			if prev, ok := p.Requests[id]; ok {
				if newRel, renamed := renamedDirtyReqPaths[id]; renamed {
					prev.RelPath = newRel
				} else {
					prev.RelPath = rel
				}
				prev.CollectionVars = p.collectionVarsFor(prev.RelPath, colByDir)
				newRequests[id] = prev
				newOrder = append(newOrder, id)
				continue
			}
		}
		reqCopy := *rec.parsedReq
		if prev, ok := p.Requests[id]; ok {
			prev.Request = &reqCopy
			prev.RelPath = rel
			prev.BaselineDigest = rec.Digest
			prev.CollectionVars = p.collectionVarsFor(rel, colByDir)
			newRequests[id] = prev
		} else {
			newRequests[id] = &RequestEntry{
				Request:        &reqCopy,
				RelPath:        rel,
				BaselineDigest: rec.Digest,
				CollectionVars: p.collectionVarsFor(rel, colByDir),
			}
		}
		newOrder = append(newOrder, id)
	}
	p.Requests = newRequests
	p.Order = newOrder

	// 6. Docs
	prevDocByPath := map[string]*DocEntry{}
	for _, de := range p.Docs {
		prevDocByPath[de.RelPath] = de
	}
	var newDocs []*DocEntry
	for _, rel := range paths {
		rec := snap.Files[rel]
		if rec.Kind != EntityDoc {
			continue
		}
		if rec.HasGitConflict {
			loadIssues = append(loadIssues, Issue{
				Level:   "warning",
				Code:    "doc-git-conflict",
				Path:    rel,
				Message: "document contains Git merge conflict markers",
			})
		}
		if prev, ok := prevDocByPath[rel]; ok {
			prev.Name = filepath.Base(rel)
			prev.BaselineDigest = rec.Digest
			newDocs = append(newDocs, prev)
		} else {
			newDocs = append(newDocs, &DocEntry{
				RelPath:        rel,
				Name:           filepath.Base(rel),
				BaselineDigest: rec.Digest,
			})
		}
	}
	p.Docs = newDocs

	p.loadIssues = loadIssues
	p.rebuildTree()
}

// ResolveConflict executes the user's chosen conflict resolution safely. On any
// write failure or a new concurrent external change (*ConflictError), neither
// the disk file nor the stored conflict's EditedBytes/BaselineBytes are lost.
func (e *SyncEngine) ResolveConflict(bufferID string, kind ResolutionKind, mergedOverride []byte) (*ResolutionResult, error) {
	if e == nil || e.project == nil || e.project.Store == nil {
		return nil, configErrorf("sync engine is nil")
	}
	c, ok := e.conflicts[bufferID]
	if !ok {
		return nil, configErrorf("no active conflict for %q", bufferID)
	}

	switch kind {
	case ResolutionAcceptDelete:
		delete(e.conflicts, bufferID)
		if c.EntityKind == EntityRequest && c.EntityID != "" && !c.DiskExists {
			delete(e.project.Requests, c.EntityID)
			e.project.Order = removeString(e.project.Order, c.EntityID)
			e.project.rebuildTree()
		}
		return &ResolutionResult{
			BufferID:     bufferID,
			Action:       ResolutionAcceptDelete,
			RelPath:      c.RelPath,
			ClosedBuffer: true,
		}, nil

	case ResolutionReloadDisk:
		if !c.DiskExists {
			return nil, configErrorf("file %q was deleted on disk; choose accept-delete, keep-edited, or save-as-copy", c.RelPath)
		}
		// Re-read disk to guarantee we reload the latest bytes.
		maxB := e.limits.MaxYAMLBytes
		if c.EntityKind == EntityDoc {
			maxB = e.limits.MaxDocBytes
		}
		diskBytes, diskDigest, err := e.project.Store.ReadBounded(c.RelPath, maxB)
		if err != nil {
			if os.IsNotExist(err) {
				c.ConflictKind = ConflictDeleted
				c.DiskExists = false
				c.DiskDigest = ""
				c.DiskBytes = nil
				return nil, &ConflictError{Path: c.RelPath, Baseline: c.BaselineDigest, Disk: ""}
			}
			return nil, err
		}
		rec := buildDiskRecord(c.RelPath, diskBytes, 0)
		if rec != nil && rec.ParseErr != "" && c.EntityKind != EntityDoc {
			c.DiskBytes = diskBytes
			c.DiskDigest = diskDigest
			c.HasGitConflict = rec.HasGitConflict
			c.ParseErr = rec.ParseErr
			return nil, configErrorf("cannot reload invalid %s on disk (%s): %s", c.EntityKind, c.RelPath, rec.ParseErr)
		}
		delete(e.conflicts, bufferID)
		e.applySingleDiskRecord(rec)
		e.RecordLocalSave(c.RelPath, diskBytes)
		return &ResolutionResult{
			BufferID:   bufferID,
			Action:     ResolutionReloadDisk,
			RelPath:    c.RelPath,
			NewDigest:  diskDigest,
			SavedBytes: diskBytes,
		}, nil

	case ResolutionKeepEdited, ResolutionApplyMerged:
		candidate := c.EditedBytes
		if kind == ResolutionApplyMerged || len(mergedOverride) > 0 {
			candidate = mergedOverride
		}
		if candidate == nil {
			return nil, configErrorf("no edited content to save for %q", c.RelPath)
		}
		if err := e.validateCandidateForSave(c, candidate, true); err != nil {
			return nil, err
		}
		expectedDisk := ""
		if c.DiskExists {
			expectedDisk = c.DiskDigest
		}
		if err := e.project.Store.SaveChecked(c.RelPath, candidate, expectedDisk); err != nil {
			// Refresh conflict's disk side if yet another external write occurred!
			if IsConflict(err) {
				e.refreshConflictDiskSide(c)
			}
			return nil, err
		}
		rec := buildDiskRecord(c.RelPath, candidate, 0)
		delete(e.conflicts, bufferID)
		e.applySingleDiskRecord(rec)
		e.RecordLocalSave(c.RelPath, candidate)
		return &ResolutionResult{
			BufferID:   bufferID,
			Action:     kind,
			RelPath:    c.RelPath,
			NewDigest:  Digest(candidate),
			SavedBytes: cloneBytes(candidate),
		}, nil

	case ResolutionSaveAsCopy:
		candidate := c.EditedBytes
		if len(mergedOverride) > 0 {
			candidate = mergedOverride
		}
		if candidate == nil {
			return nil, configErrorf("no edited content to save as copy for %q", c.RelPath)
		}
		res, err := e.saveConflictAsCopy(c, candidate)
		if err != nil {
			return nil, err
		}
		delete(e.conflicts, bufferID)
		// If the original file still exists on disk, refresh the original entry
		// in Project to the clean disk version so both versions coexist!
		if c.DiskExists {
			if diskBytes, _, rerr := e.project.Store.ReadBounded(c.RelPath, e.limits.MaxDocBytes); rerr == nil {
				rec := buildDiskRecord(c.RelPath, diskBytes, 0)
				if rec != nil && (rec.ParseErr == "" || c.EntityKind == EntityDoc) {
					e.applySingleDiskRecord(rec)
					e.RecordLocalSave(c.RelPath, diskBytes)
				}
			}
		}
		return res, nil

	default:
		return nil, configErrorf("unknown conflict resolution %q", kind)
	}
}

func (e *SyncEngine) refreshConflictDiskSide(c *SyncConflict) {
	maxB := e.limits.MaxYAMLBytes
	if c.EntityKind == EntityDoc {
		maxB = e.limits.MaxDocBytes
	}
	diskBytes, diskDigest, err := e.project.Store.ReadBounded(c.RelPath, maxB)
	if os.IsNotExist(err) {
		c.ConflictKind = ConflictDeleted
		c.DiskExists = false
		c.DiskDigest = ""
		c.DiskBytes = nil
		c.HasGitConflict = false
		c.ParseErr = ""
		return
	}
	if err == nil {
		c.DiskExists = true
		c.DiskDigest = diskDigest
		c.DiskBytes = diskBytes
		if rec := buildDiskRecord(c.RelPath, diskBytes, 0); rec != nil {
			c.HasGitConflict = rec.HasGitConflict
			c.ParseErr = rec.ParseErr
		}
	}
}

func (e *SyncEngine) validateCandidateForSave(c *SyncConflict, data []byte, requireSameID bool) error {
	maxB := e.limits.MaxYAMLBytes
	if c.EntityKind == EntityDoc {
		maxB = e.limits.MaxDocBytes
	}
	if int64(len(data)) > maxB {
		return configErrorf("content for %q exceeds size limit (%d > %d bytes)", c.RelPath, len(data), maxB)
	}
	if c.EntityKind == EntityDoc {
		return nil
	}
	if line, has := DetectGitConflictMarkers(data); has {
		return configErrorf("unresolved Git merge conflict markers at line %d", line)
	}
	rec := buildDiskRecord(c.RelPath, data, 0)
	if rec == nil {
		return configErrorf("invalid target path %q", c.RelPath)
	}
	if rec.ParseErr != "" {
		return configErrorf("invalid YAML for %q: %s", c.RelPath, rec.ParseErr)
	}
	if requireSameID && c.EntityID != "" && rec.EntityID != c.EntityID {
		return configErrorf("in-place resolution must keep stable id %q (got %q); use save-as-copy for a new id", c.EntityID, rec.EntityID)
	}
	return nil
}

func (e *SyncEngine) applySingleDiskRecord(rec *DiskFileRecord) {
	if rec == nil {
		return
	}
	p := e.project
	switch rec.Kind {
	case EntityRequest:
		if rec.parsedReq == nil {
			return
		}
		reqCopy := *rec.parsedReq
		if entry, ok := p.Requests[reqCopy.ID]; ok {
			entry.Request = &reqCopy
			entry.RelPath = rec.RelPath
			entry.BaselineDigest = rec.Digest
			entry.CollectionVars = p.collectionVarsFor(rec.RelPath, p.colByDir)
		} else {
			entry = &RequestEntry{
				Request:        &reqCopy,
				RelPath:        rec.RelPath,
				BaselineDigest: rec.Digest,
				CollectionVars: p.collectionVarsFor(rec.RelPath, p.colByDir),
			}
			p.Requests[reqCopy.ID] = entry
			p.Order = append(p.Order, reqCopy.ID)
		}
		p.rebuildTree()

	case EntityDoc:
		found := false
		for _, de := range p.Docs {
			if de.RelPath == rec.RelPath {
				de.BaselineDigest = rec.Digest
				de.Name = filepath.Base(rec.RelPath)
				found = true
				break
			}
		}
		if !found {
			p.Docs = append(p.Docs, &DocEntry{
				RelPath:        rec.RelPath,
				Name:           filepath.Base(rec.RelPath),
				BaselineDigest: rec.Digest,
			})
			sort.Slice(p.Docs, func(i, j int) bool { return p.Docs[i].RelPath < p.Docs[j].RelPath })
		}

	case EntityEnvironment:
		if rec.parsedEnv == nil {
			return
		}
		envCopy := *rec.parsedEnv
		if entry, ok := p.EnvEntries[envCopy.ID]; ok {
			entry.Environment = &envCopy
			entry.RelPath = rec.RelPath
			entry.BaselineDigest = rec.Digest
		} else {
			p.EnvEntries[envCopy.ID] = &EnvironmentEntry{
				Environment:    &envCopy,
				RelPath:        rec.RelPath,
				BaselineDigest: rec.Digest,
			}
			p.EnvOrder = append(p.EnvOrder, envCopy.ID)
			sort.Strings(p.EnvOrder)
		}
		p.Environments[envCopy.ID] = &envCopy
	}
}

func (e *SyncEngine) saveConflictAsCopy(c *SyncConflict, candidate []byte) (*ResolutionResult, error) {
	p := e.project
	switch c.EntityKind {
	case EntityRequest:
		if err := e.validateCandidateForSave(c, candidate, false); err != nil {
			return nil, err
		}
		var req Request
		if err := decodeStrictForSync(candidate, &req); err != nil {
			return nil, &ConfigError{Err: err}
		}
		baseID := req.ID
		if baseID == "" {
			baseID = "request"
		}
		newID := baseID + "-copy"
		for i := 2; ; i++ {
			if _, exists := p.Requests[newID]; !exists {
				break
			}
			newID = fmt.Sprintf("%s-copy-%d", baseID, i)
		}
		req.ID = newID
		if strings.TrimSpace(req.Name) != "" {
			req.Name = req.Name + " (Copy)"
		} else {
			req.Name = newID
		}
		dir := filepath.ToSlash(filepath.Dir(c.RelPath))
		if dir == "." || dir == "" || !strings.HasPrefix(dir, "collections") {
			dir = "collections"
		}
		stem := strings.TrimSuffix(baseNameSlash(c.RelPath), ".request.yaml")
		if stem == "" {
			stem = Slug(newID)
		}
		copyRel := filepath.ToSlash(filepath.Join(dir, stem+"-copy.request.yaml"))
		for i := 2; p.Store.Exists(copyRel); i++ {
			copyRel = filepath.ToSlash(filepath.Join(dir, fmt.Sprintf("%s-copy-%d.request.yaml", stem, i)))
		}
		outBytes, err := EncodeRequestAgainstBaseline(candidate, &req)
		if err != nil {
			return nil, err
		}
		if err := p.Store.SaveChecked(copyRel, outBytes, ""); err != nil {
			return nil, err
		}
		entry := &RequestEntry{
			Request:        &req,
			RelPath:        copyRel,
			BaselineDigest: Digest(outBytes),
			CollectionVars: p.collectionVarsFor(copyRel, p.colByDir),
		}
		p.Requests[newID] = entry
		p.Order = append(p.Order, newID)
		p.rebuildTree()
		e.RecordLocalSave(copyRel, outBytes)
		return &ResolutionResult{
			BufferID:    c.BufferID,
			Action:      ResolutionSaveAsCopy,
			RelPath:     copyRel,
			NewDigest:   entry.BaselineDigest,
			SavedBytes:  outBytes,
			CopyRequest: entry,
		}, nil

	case EntityDoc:
		if int64(len(candidate)) > e.limits.MaxDocBytes {
			return nil, configErrorf("document exceeds size limit (%d > %d bytes)", len(candidate), e.limits.MaxDocBytes)
		}
		dir := filepath.ToSlash(filepath.Dir(c.RelPath))
		if dir == "." || dir == "" || !strings.HasPrefix(dir, "docs") {
			dir = "docs"
		}
		stem := strings.TrimSuffix(baseNameSlash(c.RelPath), ".md")
		if stem == "" {
			stem = "document"
		}
		copyRel := filepath.ToSlash(filepath.Join(dir, stem+"-copy.md"))
		for i := 2; p.Store.Exists(copyRel); i++ {
			copyRel = filepath.ToSlash(filepath.Join(dir, fmt.Sprintf("%s-copy-%d.md", stem, i)))
		}
		if err := p.Store.SaveChecked(copyRel, candidate, ""); err != nil {
			return nil, err
		}
		de := &DocEntry{
			RelPath:        copyRel,
			Name:           filepath.Base(copyRel),
			BaselineDigest: Digest(candidate),
		}
		p.Docs = append(p.Docs, de)
		sort.Slice(p.Docs, func(i, j int) bool { return p.Docs[i].RelPath < p.Docs[j].RelPath })
		e.RecordLocalSave(copyRel, candidate)
		return &ResolutionResult{
			BufferID:   c.BufferID,
			Action:     ResolutionSaveAsCopy,
			RelPath:    copyRel,
			NewDigest:  de.BaselineDigest,
			SavedBytes: cloneBytes(candidate),
			CopyDoc:    de,
		}, nil

	default:
		return nil, configErrorf("save-as-copy is not supported for %s", c.EntityKind)
	}
}

func removeString(list []string, target string) []string {
	out := make([]string, 0, len(list))
	for _, s := range list {
		if s != target {
			out = append(out, s)
		}
	}
	return out
}

// --- Three-Way Line Merge ------------------------------------------------

// MergeThreeWay performs a line-level three-way merge of edited and disk
// against baseline. Non-overlapping changes on both sides merge cleanly with
// clean == true; overlapping changes emit standard diff3-style conflict blocks
// (<<<<<<< Edited / ||||||| Baseline / ======= / >>>>>>> Disk) with
// clean == false.
func MergeThreeWay(baseline, edited, disk []byte) (merged []byte, clean bool) {
	baseNorm := NormalizeLF(baseline)
	editNorm := NormalizeLF(edited)
	diskNorm := NormalizeLF(disk)

	if bytes.Equal(editNorm, diskNorm) {
		return editNorm, true
	}
	if bytes.Equal(baseNorm, editNorm) {
		return diskNorm, true
	}
	if bytes.Equal(baseNorm, diskNorm) {
		return editNorm, true
	}

	baseLines := splitContentLines(baseNorm)
	editLines := splitContentLines(editNorm)
	diskLines := splitContentLines(diskNorm)

	editsA := diffBaseToSide(baseLines, editLines)
	editsB := diffBaseToSide(baseLines, diskLines)

	var out []string
	clean = true
	bi := 0
	ia, ib := 0, 0

	for bi < len(baseLines) || ia < len(editsA) || ib < len(editsB) {
		// Advance any unchanged prefix before the next hunk on either side.
		nextBase := len(baseLines)
		if ia < len(editsA) && editsA[ia].baseStart < nextBase {
			nextBase = editsA[ia].baseStart
		}
		if ib < len(editsB) && editsB[ib].baseStart < nextBase {
			nextBase = editsB[ib].baseStart
		}
		if bi < nextBase {
			out = append(out, baseLines[bi:nextBase]...)
			bi = nextBase
		}
		if ia >= len(editsA) && ib >= len(editsB) {
			if bi < len(baseLines) {
				out = append(out, baseLines[bi:]...)
				bi = len(baseLines)
			}
			break
		}

		// Pure insertion/change on only side A before side B starts:
		if ia < len(editsA) && (ib >= len(editsB) || (editsA[ia].baseEnd < editsB[ib].baseStart) ||
			(editsA[ia].baseEnd == editsB[ib].baseStart && editsA[ia].baseStart < editsA[ia].baseEnd)) {
			out = append(out, editsA[ia].repl...)
			bi = editsA[ia].baseEnd
			ia++
			continue
		}
		// Pure insertion/change on only side B before side A starts:
		if ib < len(editsB) && (ia >= len(editsA) || (editsB[ib].baseEnd < editsA[ia].baseStart) ||
			(editsB[ib].baseEnd == editsA[ia].baseStart && editsB[ib].baseStart < editsB[ib].baseEnd)) {
			out = append(out, editsB[ib].repl...)
			bi = editsB[ib].baseEnd
			ib++
			continue
		}

		// Overlapping or adjacent zero-length hunks: expand the overlapping
		// region across A and B until neither crosses the end of the region.
		regStart := bi
		regEnd := bi
		startIA, startIB := ia, ib
		if ia < len(editsA) && editsA[ia].baseEnd > regEnd {
			regEnd = editsA[ia].baseEnd
		}
		if ib < len(editsB) && editsB[ib].baseEnd > regEnd {
			regEnd = editsB[ib].baseEnd
		}
		for {
			grew := false
			for ia < len(editsA) && (editsA[ia].baseStart < regEnd || (editsA[ia].baseStart == regEnd && ia == startIA)) {
				if editsA[ia].baseEnd > regEnd {
					regEnd = editsA[ia].baseEnd
					grew = true
				}
				ia++
			}
			for ib < len(editsB) && (editsB[ib].baseStart < regEnd || (editsB[ib].baseStart == regEnd && ib == startIB)) {
				if editsB[ib].baseEnd > regEnd {
					regEnd = editsB[ib].baseEnd
					grew = true
				}
				ib++
			}
			if !grew {
				break
			}
		}

		sideA := renderRegion(baseLines, regStart, regEnd, editsA[startIA:ia])
		sideB := renderRegion(baseLines, regStart, regEnd, editsB[startIB:ib])
		if stringSlicesEqual(sideA, sideB) {
			out = append(out, sideA...)
		} else {
			clean = false
			out = append(out, "<<<<<<< Edited")
			out = append(out, sideA...)
			out = append(out, "||||||| Baseline")
			out = append(out, baseLines[regStart:regEnd]...)
			out = append(out, "=======")
			out = append(out, sideB...)
			out = append(out, ">>>>>>> Disk")
		}
		bi = regEnd
	}

	if len(out) == 0 {
		return nil, clean
	}
	joined := strings.Join(out, "\n") + "\n"
	return []byte(joined), clean
}

type lineHunk struct {
	baseStart int
	baseEnd   int
	repl      []string
}

func splitContentLines(b []byte) []string {
	if len(b) == 0 {
		return nil
	}
	trimmed := bytes.TrimSuffix(b, []byte{'\n'})
	raw := bytes.Split(trimmed, []byte{'\n'})
	out := make([]string, len(raw))
	for i, l := range raw {
		out[i] = string(l)
	}
	return out
}

func renderRegion(base []string, start, end int, hunks []lineHunk) []string {
	var out []string
	cur := start
	for _, h := range hunks {
		if cur < h.baseStart {
			out = append(out, base[cur:h.baseStart]...)
		}
		out = append(out, h.repl...)
		cur = h.baseEnd
	}
	if cur < end {
		out = append(out, base[cur:end]...)
	}
	return out
}

func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// diffBaseToSide computes minimal line hunks transforming base into side using
// LCS (with common prefix/suffix trimming so large files stay fast).
func diffBaseToSide(base, side []string) []lineHunk {
	prefix := 0
	for prefix < len(base) && prefix < len(side) && base[prefix] == side[prefix] {
		prefix++
	}
	baseEnd := len(base)
	sideEnd := len(side)
	for baseEnd > prefix && sideEnd > prefix && base[baseEnd-1] == side[sideEnd-1] {
		baseEnd--
		sideEnd--
	}
	bMid := base[prefix:baseEnd]
	sMid := side[prefix:sideEnd]
	if len(bMid) == 0 && len(sMid) == 0 {
		return nil
	}
	if len(bMid) == 0 {
		return []lineHunk{{baseStart: prefix, baseEnd: prefix, repl: append([]string(nil), sMid...)}}
	}
	if len(sMid) == 0 {
		return []lineHunk{{baseStart: prefix, baseEnd: baseEnd, repl: nil}}
	}
	// Guard quadratic DP size on very large files by falling back to a single
	// replacement hunk above 4000x4000 mid-section lines.
	if len(bMid)*len(sMid) > 16_000_000 {
		return []lineHunk{{baseStart: prefix, baseEnd: baseEnd, repl: append([]string(nil), sMid...)}}
	}

	n, m := len(bMid), len(sMid)
	dp := make([]int, (n+1)*(m+1))
	stride := m + 1
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if bMid[i] == sMid[j] {
				dp[i*stride+j] = dp[(i+1)*stride+(j+1)] + 1
			} else {
				down := dp[(i+1)*stride+j]
				right := dp[i*stride+(j+1)]
				if down >= right {
					dp[i*stride+j] = down
				} else {
					dp[i*stride+j] = right
				}
			}
		}
	}

	var hunks []lineHunk
	i, j := 0, 0
	for i < n || j < m {
		if i < n && j < m && bMid[i] == sMid[j] {
			i++
			j++
			continue
		}
		hStartB := i
		var repl []string
		for !(i < n && j < m && bMid[i] == sMid[j]) && (i < n || j < m) {
			switch {
			case j < m && (i == n || dp[i*stride+(j+1)] >= dp[(i+1)*stride+j]):
				repl = append(repl, sMid[j])
				j++
			default:
				i++
			}
		}
		hunks = append(hunks, lineHunk{
			baseStart: prefix + hStartB,
			baseEnd:   prefix + i,
			repl:      repl,
		})
	}
	return hunks
}
