package sdkgen

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// Action kinds reported by Preview and executed by Apply.
const (
	ActionCreate      = "create"
	ActionUpdate      = "update"
	ActionUnchanged   = "unchanged"
	ActionDeleteStale = "delete-stale"
)

// maxManifestBytes bounds an untrusted manifest read.
const maxManifestBytes int64 = 1 << 20

// Action is one planned file operation.
type Action struct {
	RelPath      string
	Kind         string
	BeforeDigest string
	AfterDigest  string
	Bytes        int
}

// Conflict is a target the generator refuses to touch.
type Conflict struct {
	RelPath string
	Reason  string
}

// StaleFile is a previously generated file that is no longer produced.
type StaleFile struct {
	RelPath string
	Digest  string
	Reason  string
}

// Plan is the no-write preview of one generation. Apply re-validates every
// baseline before writing anything.
type Plan struct {
	Result      *Result
	TargetDir   string
	Actions     []Action
	Conflicts   []Conflict
	Stale       []StaleFile
	Manifest    *Manifest
	OldManifest *Manifest

	baselines        map[string]string // rel -> digest at preview ("" = absent)
	manifestBaseline string
	opts             Options
	// beforeWrite is a fault-injection hook used by rollback tests. It runs
	// before each write/delete; a non-nil error aborts and rolls back.
	beforeWrite func(rel string) error
}

// ApplyResult reports what Apply actually did.
type ApplyResult struct {
	Created    []string
	Updated    []string
	Unchanged  []string
	Deleted    []string
	TotalBytes int64
}

// ConflictError carries every conflict that blocked Apply.
type ConflictError struct {
	Conflicts []Conflict
}

func (e *ConflictError) Error() string {
	if e == nil || len(e.Conflicts) == 0 {
		return ErrConflict.Error()
	}
	var b strings.Builder
	b.WriteString(ErrConflict.Error())
	b.WriteString(": ")
	for i, c := range e.Conflicts {
		if i > 0 {
			b.WriteString("; ")
		}
		fmt.Fprintf(&b, "%s: %s", c.RelPath, c.Reason)
	}
	return b.String()
}

func (e *ConflictError) Unwrap() error { return ErrConflict }

// Preview generates the SDK in memory and compares it with the target
// directory without writing anything.
func Preview(snapshot []byte, opts Options) (*Plan, error) {
	res, err := Generate(snapshot, opts)
	if err != nil {
		return nil, err
	}
	target, err := resolveOutputDir(opts.TargetDir, opts.ProjectRoot)
	if err != nil {
		return nil, err
	}
	oldManifest, oldBytes, err := readManifest(target)
	if err != nil {
		return nil, err
	}
	plan := &Plan{
		Result:           res,
		TargetDir:        target,
		Manifest:         &res.Manifest,
		OldManifest:      oldManifest,
		baselines:        map[string]string{},
		manifestBaseline: digestBytes(oldBytes),
		opts:             opts,
	}
	if oldBytes == nil {
		plan.manifestBaseline = ""
	}

	oldByPath := map[string]ManifestEntry{}
	if oldManifest != nil {
		for _, e := range oldManifest.Files {
			oldByPath[e.Path] = e
		}
	}

	var contentFiles []File
	var manifestFile *File
	for i := range res.Files {
		if res.Files[i].RelPath == ManifestName {
			manifestFile = &res.Files[i]
			continue
		}
		contentFiles = append(contentFiles, res.Files[i])
	}
	sort.Slice(contentFiles, func(i, j int) bool { return contentFiles[i].RelPath < contentFiles[j].RelPath })

	for _, f := range contentFiles {
		action, conflict := classifyTarget(target, f, oldByPath, opts)
		if conflict != nil {
			plan.Conflicts = append(plan.Conflicts, *conflict)
			continue
		}
		plan.Actions = append(plan.Actions, action)
		plan.baselines[f.RelPath] = action.BeforeDigest
	}

	// Stale files: previously owned, no longer produced.
	newSet := map[string]bool{}
	for _, f := range res.Files {
		newSet[f.RelPath] = true
	}
	if oldManifest != nil {
		for _, e := range oldManifest.Files {
			if newSet[e.Path] {
				continue
			}
			stale, conflict := classifyStale(target, e, opts)
			if stale != nil {
				plan.Stale = append(plan.Stale, *stale)
			}
			if conflict != nil {
				plan.Conflicts = append(plan.Conflicts, *conflict)
				continue
			}
			if stale == nil {
				continue
			}
			if stale.Reason == "" {
				plan.Actions = append(plan.Actions, Action{
					RelPath:      e.Path,
					Kind:         ActionDeleteStale,
					BeforeDigest: e.Digest,
				})
				plan.baselines[e.Path] = e.Digest
			}
		}
	}

	// Manifest is always last so a failed write never leaves a manifest that
	// claims files which are not on disk.
	if manifestFile != nil {
		action, conflict := classifyManifest(target, *manifestFile, plan.manifestBaseline)
		if conflict != nil {
			plan.Conflicts = append(plan.Conflicts, *conflict)
		} else {
			plan.Actions = append(plan.Actions, action)
			plan.baselines[ManifestName] = action.BeforeDigest
		}
	}

	sort.Slice(plan.Conflicts, func(i, j int) bool { return plan.Conflicts[i].RelPath < plan.Conflicts[j].RelPath })
	return plan, nil
}

// classifyTarget decides the action for one generated content file.
func classifyTarget(target string, f File, oldByPath map[string]ManifestEntry, opts Options) (Action, *Conflict) {
	abs := filepath.Join(target, filepath.FromSlash(f.RelPath))
	fi, err := os.Lstat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return Action{RelPath: f.RelPath, Kind: ActionCreate, AfterDigest: f.Digest, Bytes: len(f.Content)}, nil
		}
		return Action{}, &Conflict{RelPath: f.RelPath, Reason: err.Error()}
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return Action{}, &Conflict{RelPath: f.RelPath, Reason: "target is a symbolic link"}
	}
	if !fi.Mode().IsRegular() {
		return Action{}, &Conflict{RelPath: f.RelPath, Reason: "target is not a regular file"}
	}
	old, owned := oldByPath[f.RelPath]
	if !owned {
		return Action{}, &Conflict{RelPath: f.RelPath, Reason: "existing file is not owned by the SDK generator (manual file)"}
	}
	current, err := fileDigest(abs)
	if err != nil {
		return Action{}, &Conflict{RelPath: f.RelPath, Reason: err.Error()}
	}
	if current != old.Digest {
		return Action{}, &Conflict{RelPath: f.RelPath, Reason: "generated file was modified manually; refusing to overwrite"}
	}
	if current == f.Digest {
		return Action{RelPath: f.RelPath, Kind: ActionUnchanged, BeforeDigest: current, AfterDigest: f.Digest, Bytes: len(f.Content)}, nil
	}
	if !opts.AllowOverwrite {
		return Action{}, &Conflict{RelPath: f.RelPath, Reason: "generated file would change; enable overwrite to update it"}
	}
	return Action{RelPath: f.RelPath, Kind: ActionUpdate, BeforeDigest: current, AfterDigest: f.Digest, Bytes: len(f.Content)}, nil
}

// classifyManifest decides the action for the manifest file. The manifest is
// generator-owned; it updates automatically as long as it is unchanged since
// preview.
func classifyManifest(target string, f File, baseline string) (Action, *Conflict) {
	abs := filepath.Join(target, ManifestName)
	fi, err := os.Lstat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			if baseline != "" {
				return Action{}, &Conflict{RelPath: ManifestName, Reason: "manifest disappeared since preview"}
			}
			return Action{RelPath: ManifestName, Kind: ActionCreate, AfterDigest: f.Digest, Bytes: len(f.Content)}, nil
		}
		return Action{}, &Conflict{RelPath: ManifestName, Reason: err.Error()}
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return Action{}, &Conflict{RelPath: ManifestName, Reason: "manifest is a symbolic link"}
	}
	if !fi.Mode().IsRegular() {
		return Action{}, &Conflict{RelPath: ManifestName, Reason: "manifest is not a regular file"}
	}
	current, err := fileDigest(abs)
	if err != nil {
		return Action{}, &Conflict{RelPath: ManifestName, Reason: err.Error()}
	}
	if baseline == "" {
		return Action{}, &Conflict{RelPath: ManifestName, Reason: "manifest exists but was not valid at preview"}
	}
	if current != baseline {
		return Action{}, &Conflict{RelPath: ManifestName, Reason: "manifest changed since preview"}
	}
	if current == f.Digest {
		return Action{RelPath: ManifestName, Kind: ActionUnchanged, BeforeDigest: current, AfterDigest: f.Digest, Bytes: len(f.Content)}, nil
	}
	return Action{RelPath: ManifestName, Kind: ActionUpdate, BeforeDigest: current, AfterDigest: f.Digest, Bytes: len(f.Content)}, nil
}

// classifyStale decides whether a stale generated file can be deleted.
func classifyStale(target string, e ManifestEntry, opts Options) (*StaleFile, *Conflict) {
	abs := filepath.Join(target, filepath.FromSlash(e.Path))
	fi, err := os.Lstat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, &Conflict{RelPath: e.Path, Reason: err.Error()}
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return nil, &Conflict{RelPath: e.Path, Reason: "stale target is a symbolic link"}
	}
	if !fi.Mode().IsRegular() {
		return nil, &Conflict{RelPath: e.Path, Reason: "stale target is not a regular file"}
	}
	current, err := fileDigest(abs)
	if err != nil {
		return nil, &Conflict{RelPath: e.Path, Reason: err.Error()}
	}
	if current != e.Digest {
		return nil, &Conflict{RelPath: e.Path, Reason: "stale generated file was modified manually; refusing to delete"}
	}
	if !opts.AllowDeleteStale {
		return &StaleFile{RelPath: e.Path, Digest: current, Reason: "not deleted (stale deletion is off)"},
			&Conflict{RelPath: e.Path, Reason: "stale generated file remains; enable stale deletion to remove it"}
	}
	return &StaleFile{RelPath: e.Path, Digest: current}, nil
}

// Apply writes a previewed plan after re-validating every baseline. It rolls
// back all prior writes if any step fails and writes the manifest last.
func Apply(plan *Plan) (*ApplyResult, error) {
	if plan == nil || plan.Result == nil {
		return nil, errors.New("sdkgen: nil plan")
	}
	target, err := resolveOutputDir(plan.TargetDir, plan.opts.ProjectRoot)
	if err != nil {
		return nil, err
	}
	if target != plan.TargetDir {
		return nil, &ConflictError{Conflicts: []Conflict{{RelPath: ".", Reason: "target directory changed since preview"}}}
	}
	if len(plan.Conflicts) > 0 {
		return nil, &ConflictError{Conflicts: plan.Conflicts}
	}

	// Re-validate the manifest baseline before any file is touched.
	if err := validateBaseline(target, ManifestName, plan.manifestBaseline); err != nil {
		return nil, &ConflictError{Conflicts: []Conflict{{RelPath: ManifestName, Reason: err.Error()}}}
	}
	for _, action := range plan.Actions {
		if action.RelPath == ManifestName {
			continue
		}
		if err := validateBaseline(target, action.RelPath, plan.baselines[action.RelPath]); err != nil {
			return nil, &ConflictError{Conflicts: []Conflict{{RelPath: action.RelPath, Reason: err.Error()}}}
		}
	}

	contentByPath := map[string][]byte{}
	for _, f := range plan.Result.Files {
		contentByPath[f.RelPath] = f.Content
	}

	res := &ApplyResult{}
	var undo []undoStep
	rollback := func(cause error) error {
		var rbErr error
		for i := len(undo) - 1; i >= 0; i-- {
			step := undo[i]
			if step.existed {
				if err := writeAtomicFile(target, step.rel, step.prior); err != nil && rbErr == nil {
					rbErr = fmt.Errorf("rollback restore %s: %w", step.rel, err)
				}
				continue
			}
			abs := filepath.Join(target, filepath.FromSlash(step.rel))
			if err := os.Remove(abs); err != nil && !os.IsNotExist(err) && rbErr == nil {
				rbErr = fmt.Errorf("rollback remove %s: %w", step.rel, err)
			}
		}
		if rbErr != nil {
			return fmt.Errorf("%w (rollback also failed: %v)", cause, rbErr)
		}
		return cause
	}

	for _, action := range plan.Actions {
		data, ok := contentByPath[action.RelPath]
		if !ok && action.Kind != ActionDeleteStale {
			return nil, rollback(fmt.Errorf("sdkgen: missing planned content for %s", action.RelPath))
		}
		if plan.beforeWrite != nil {
			if err := plan.beforeWrite(action.RelPath); err != nil {
				return nil, rollback(err)
			}
		}
		abs := filepath.Join(target, filepath.FromSlash(action.RelPath))
		switch action.Kind {
		case ActionCreate, ActionUpdate:
			prior, existed, err := readOptional(abs)
			if err != nil {
				return nil, rollback(err)
			}
			if err := writeAtomicFile(target, action.RelPath, data); err != nil {
				return nil, rollback(err)
			}
			undo = append(undo, undoStep{rel: action.RelPath, existed: existed, prior: prior})
			if action.Kind == ActionCreate {
				res.Created = append(res.Created, action.RelPath)
			} else {
				res.Updated = append(res.Updated, action.RelPath)
			}
			res.TotalBytes += int64(len(data))
		case ActionUnchanged:
			res.Unchanged = append(res.Unchanged, action.RelPath)
		case ActionDeleteStale:
			prior, existed, err := readOptional(abs)
			if err != nil {
				return nil, rollback(err)
			}
			if !existed {
				return nil, rollback(fmt.Errorf("sdkgen: stale file %s disappeared", action.RelPath))
			}
			if err := os.Remove(abs); err != nil {
				return nil, rollback(err)
			}
			undo = append(undo, undoStep{rel: action.RelPath, existed: true, prior: prior})
			res.Deleted = append(res.Deleted, action.RelPath)
		default:
			return nil, rollback(fmt.Errorf("sdkgen: unknown action %q", action.Kind))
		}
	}
	return res, nil
}

type undoStep struct {
	rel     string
	existed bool
	prior   []byte
}

// validateBaseline checks that rel still matches the digest captured at
// preview. An empty baseline means the file must not exist.
func validateBaseline(target, rel, baseline string) error {
	abs := filepath.Join(target, filepath.FromSlash(rel))
	fi, err := os.Lstat(abs)
	if baseline == "" {
		if err == nil {
			return fmt.Errorf("file appeared since preview")
		}
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("file disappeared since preview")
		}
		return err
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("target became a symbolic link")
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("target is not a regular file")
	}
	current, err := fileDigest(abs)
	if err != nil {
		return err
	}
	if current != baseline {
		return fmt.Errorf("file changed since preview")
	}
	return nil
}

// resolveOutputDir validates the target and resolves symlinks in its existing
// prefix so confinement checks compare real paths.
func resolveOutputDir(target, projectRoot string) (string, error) {
	clean := filepath.Clean(strings.TrimSpace(target))
	if clean == "" || clean == "." {
		return "", fmt.Errorf("%w: target directory is empty", ErrUnsafeTarget)
	}
	if !filepath.IsAbs(clean) {
		return "", fmt.Errorf("%w: target directory %q must be absolute", ErrUnsafeTarget, target)
	}
	if clean == string(filepath.Separator) {
		return "", fmt.Errorf("%w: refusing to write into the filesystem root", ErrUnsafeTarget)
	}
	resolved := resolveExistingPrefix(clean)
	if projectRoot != "" {
		proj := resolveExistingPrefix(filepath.Clean(projectRoot))
		if resolved == proj || strings.HasPrefix(resolved, proj+string(filepath.Separator)) {
			return "", fmt.Errorf("%w: target directory is inside the project (%s); pick a directory outside the project", ErrUnsafeTarget, projectRoot)
		}
	}
	if fi, err := os.Stat(resolved); err == nil && !fi.IsDir() {
		return "", fmt.Errorf("%w: target %q is not a directory", ErrUnsafeTarget, target)
	}
	return resolved, nil
}

// resolveExistingPrefix resolves symlinks in the longest existing prefix of p.
func resolveExistingPrefix(p string) string {
	clean := filepath.Clean(p)
	cur := clean
	var tail []string
	for {
		if resolved, err := filepath.EvalSymlinks(cur); err == nil {
			for i := len(tail) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, tail[i])
			}
			return resolved
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return clean
		}
		tail = append(tail, filepath.Base(cur))
		cur = parent
	}
}

// readManifest reads and validates the ownership manifest. A missing manifest
// returns (nil, nil, nil); a corrupt or incompatible one returns
// ErrInvalidManifest.
func readManifest(target string) (*Manifest, []byte, error) {
	abs := filepath.Join(target, ManifestName)
	fi, err := os.Lstat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return nil, nil, fmt.Errorf("%w: manifest is a symbolic link", ErrInvalidManifest)
	}
	if !fi.Mode().IsRegular() {
		return nil, nil, fmt.Errorf("%w: manifest is not a regular file", ErrInvalidManifest)
	}
	if fi.Size() > maxManifestBytes {
		return nil, nil, fmt.Errorf("%w: manifest exceeds %d bytes", ErrInvalidManifest, maxManifestBytes)
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return nil, nil, err
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, nil, fmt.Errorf("%w: %v", ErrInvalidManifest, err)
	}
	if m.GeneratorVersion != GeneratorVersion || m.TemplateVersion != TemplateVersion {
		return nil, nil, fmt.Errorf("%w: manifest version %q/%q is not %q/%q", ErrInvalidManifest, m.GeneratorVersion, m.TemplateVersion, GeneratorVersion, TemplateVersion)
	}
	seen := map[string]bool{}
	for _, e := range m.Files {
		if err := validateRelPath(e.Path); err != nil {
			return nil, nil, fmt.Errorf("%w: %v", ErrInvalidManifest, err)
		}
		if e.Path == ManifestName {
			return nil, nil, fmt.Errorf("%w: manifest lists itself", ErrInvalidManifest)
		}
		if e.Digest == "" {
			return nil, nil, fmt.Errorf("%w: manifest entry %q has no digest", ErrInvalidManifest, e.Path)
		}
		if seen[e.Path] {
			return nil, nil, fmt.Errorf("%w: duplicate manifest entry %q", ErrInvalidManifest, e.Path)
		}
		seen[e.Path] = true
	}
	return &m, data, nil
}

// validateRelPath rejects absolute, escaping, unclean and control-character
// paths from an untrusted manifest.
func validateRelPath(rel string) error {
	if rel == "" {
		return errors.New("empty path")
	}
	if strings.ContainsAny(rel, "\\:\x00") {
		return fmt.Errorf("path %q contains a backslash, colon or NUL", rel)
	}
	if strings.HasPrefix(rel, "/") || filepath.IsAbs(rel) {
		return fmt.Errorf("path %q is absolute", rel)
	}
	if clean := path.Clean(rel); clean != rel {
		return fmt.Errorf("path %q is not clean", rel)
	}
	for _, seg := range strings.Split(rel, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return fmt.Errorf("path %q has an empty or relative segment", rel)
		}
	}
	return nil
}

// writeAtomicFile writes rel under target via a temporary file and rename,
// refusing symlinked targets and symlinked directory components.
func writeAtomicFile(target, rel string, data []byte) error {
	abs := filepath.Join(target, filepath.FromSlash(rel))
	if err := mkdirAllNoSymlink(filepath.Dir(abs), target); err != nil {
		return err
	}
	if fi, err := os.Lstat(abs); err == nil {
		if fi.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("target %s is a symbolic link", rel)
		}
		if !fi.Mode().IsRegular() {
			return fmt.Errorf("target %s is not a regular file", rel)
		}
	}
	tmp, err := os.CreateTemp(filepath.Dir(abs), ".sdkgen-tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after a successful rename
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return err
	}
	return os.Rename(tmpName, abs)
}

// mkdirAllNoSymlink creates dir and its parents up to stopAt, refusing to
// traverse a symlinked directory.
func mkdirAllNoSymlink(dir, stopAt string) error {
	stopAt = filepath.Clean(stopAt)
	var dirs []string
	cur := filepath.Clean(dir)
	for strings.HasPrefix(cur, stopAt) && cur != parentOf(cur) {
		dirs = append(dirs, cur)
		if cur == stopAt {
			break
		}
		next := parentOf(cur)
		if len(next) >= len(cur) {
			break
		}
		cur = next
	}
	for i := len(dirs) - 1; i >= 0; i-- {
		d := dirs[i]
		if fi, err := os.Lstat(d); err == nil {
			if fi.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("target directory component %s is a symbolic link", d)
			}
			if fi.IsDir() {
				continue
			}
			return fmt.Errorf("target directory component %s exists and is not a directory", d)
		}
		if err := os.Mkdir(d, 0o755); err != nil && !os.IsExist(err) {
			return err
		}
	}
	return nil
}

func parentOf(dir string) string {
	parent := filepath.Dir(dir)
	if len(parent) >= len(dir) {
		return dir
	}
	return parent
}

func fileDigest(abs string) (string, error) {
	data, err := os.ReadFile(abs)
	if err != nil {
		return "", err
	}
	return digestBytes(data), nil
}

func readOptional(abs string) ([]byte, bool, error) {
	data, err := os.ReadFile(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return data, true, nil
}
