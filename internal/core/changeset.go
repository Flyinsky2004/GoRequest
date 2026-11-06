package core

import (
	"fmt"
	"os"
	"strings"
)

// ChangeKind identifies one file-level operation inside a ChangeSet.
type ChangeKind string

const (
	ChangeCreate ChangeKind = "create"
	ChangeUpdate ChangeKind = "update"
	ChangeDelete ChangeKind = "delete"
	ChangeMove   ChangeKind = "move"
)

// FileChange describes one staged file operation relative to a Store's Root.
type FileChange struct {
	Kind             ChangeKind
	RelPath          string // target path for create/update/delete; source path for move
	TargetRelPath    string // destination path when Kind == ChangeMove
	ExpectedBaseline string // required digest on disk for update/delete/move; "" for create
	NewContent       []byte // replacement bytes for create/update; optional override for move (nil keeps source bytes)
	MaxBytes         int64  // optional per-file byte limit; <= 0 uses Store default
}

// ChangePreview summarizes the verified before/after effect of one FileChange
// without touching disk.
type ChangePreview struct {
	Kind         ChangeKind
	RelPath      string
	TargetPath   string
	BeforeDigest string
	AfterDigest  string
	BeforeBytes  int
	AfterBytes   int
}

// ChangeReceipt records a committed ChangeSet so the caller can inspect final
// digests or roll the entire set back to its exact pre-apply state in memory
// without leaving backup/temp artifacts inside .gorequest/.
type ChangeReceipt struct {
	store      *Store
	applied    []appliedStep
	Previews   []ChangePreview
	rolledBack bool
}

type appliedStep struct {
	rel         string
	existed     bool
	priorBytes  []byte
	finalDigest string
}

// ChangeSet stages multiple Store mutations so they can be previewed, checked
// against baselines and resource limits up front, applied atomically file by
// file, and rolled back cleanly if any middle step fails.
type ChangeSet struct {
	store   *Store
	changes []FileChange
	// beforeWriteStep is an optional fault-injection hook used by recovery tests.
	beforeWriteStep func(stepIndex int, rel string) error
}

// NewChangeSet constructs an empty ChangeSet bound to s.
func (s *Store) NewChangeSet() *ChangeSet {
	return &ChangeSet{store: s}
}

// Create stages creation of a brand-new file (fails with *ConflictError if rel
// already exists on disk).
func (cs *ChangeSet) Create(rel string, content []byte) *ChangeSet {
	cs.changes = append(cs.changes, FileChange{
		Kind:       ChangeCreate,
		RelPath:    rel,
		NewContent: cloneBytes(content),
	})
	return cs
}

// Update stages updating an existing file whose current disk digest must match
// baselineDigest.
func (cs *ChangeSet) Update(rel string, content []byte, baselineDigest string) *ChangeSet {
	cs.changes = append(cs.changes, FileChange{
		Kind:             ChangeUpdate,
		RelPath:          rel,
		ExpectedBaseline: baselineDigest,
		NewContent:       cloneBytes(content),
	})
	return cs
}

// Delete stages deleting rel only when its on-disk digest matches baselineDigest.
func (cs *ChangeSet) Delete(rel string, baselineDigest string) *ChangeSet {
	cs.changes = append(cs.changes, FileChange{
		Kind:             ChangeDelete,
		RelPath:          rel,
		ExpectedBaseline: baselineDigest,
	})
	return cs
}

// Move stages moving srcRel to dstRel while checking srcRel against
// baselineDigest and ensuring dstRel does not already exist. When newContent is
// non-nil, dstRel receives newContent instead of the verbatim source bytes.
func (cs *ChangeSet) Move(srcRel, dstRel, baselineDigest string, newContent []byte) *ChangeSet {
	var body []byte
	if newContent != nil {
		body = cloneBytes(newContent)
	}
	cs.changes = append(cs.changes, FileChange{
		Kind:             ChangeMove,
		RelPath:          srcRel,
		TargetRelPath:    dstRel,
		ExpectedBaseline: baselineDigest,
		NewContent:       body,
	})
	return cs
}

// Add appends a raw FileChange to the set.
func (cs *ChangeSet) Add(ch FileChange) *ChangeSet {
	ch.NewContent = cloneBytes(ch.NewContent)
	cs.changes = append(cs.changes, ch)
	return cs
}

// Len returns the number of staged operations.
func (cs *ChangeSet) Len() int {
	if cs == nil {
		return 0
	}
	return len(cs.changes)
}

type preparedChange struct {
	preview ChangePreview
	// low-level mutations in order (1 step for create/update/delete, 2 for move)
	mutations []fileMutation
}

type fileMutation struct {
	rel         string
	existed     bool
	priorBytes  []byte
	deleteAfter bool
	writeBytes  []byte
}

// Preview validates every staged operation (path confinement, symlink safety,
// size limits, Git conflict markers on structured YAML, duplicate path
// collisions, and baseline digests) without modifying any file on disk.
func (cs *ChangeSet) Preview() ([]ChangePreview, error) {
	prepared, err := cs.prepare()
	if err != nil {
		return nil, err
	}
	out := make([]ChangePreview, len(prepared))
	for i, p := range prepared {
		out[i] = p.preview
	}
	return out, nil
}

func (cs *ChangeSet) prepare() ([]preparedChange, error) {
	if cs == nil || cs.store == nil {
		return nil, configErrorf("changeset has no store")
	}
	limits := cs.store.EffectiveLimits()
	seenPaths := map[string]struct{}{}
	claimPath := func(rel string) (string, error) {
		norm, err := cs.store.ResolvePath(rel)
		if err != nil {
			return "", &ConfigError{Err: err}
		}
		if IsTempOrIgnoredName(baseNameSlash(norm)) {
			return "", configErrorf("temporary or ignored file name not allowed: %q", rel)
		}
		if _, dup := seenPaths[norm]; dup {
			return "", configErrorf("duplicate path %q in changeset", norm)
		}
		seenPaths[norm] = struct{}{}
		return norm, nil
	}

	checkContent := func(norm string, data []byte, maxOverride int64) error {
		limit := maxOverride
		if limit <= 0 {
			if isMarkdownPath(norm) {
				limit = limits.MaxDocBytes
			} else {
				limit = limits.MaxYAMLBytes
			}
		}
		if int64(len(data)) > limit {
			return configErrorf("staged content for %q exceeds size limit (%d > %d bytes)", norm, len(data), limit)
		}
		if isYAMLPath(norm) {
			if line, ok := DetectGitConflictMarkers(data); ok {
				return configErrorf("file %q contains unresolved Git conflict markers at line %d", norm, line)
			}
		}
		return nil
	}

	out := make([]preparedChange, 0, len(cs.changes))
	for _, ch := range cs.changes {
		switch ch.Kind {
		case ChangeCreate:
			norm, err := claimPath(ch.RelPath)
			if err != nil {
				return nil, err
			}
			if err := checkContent(norm, ch.NewContent, ch.MaxBytes); err != nil {
				return nil, err
			}
			diskBytes, diskDigest, rerr := cs.readOptional(norm)
			if rerr != nil {
				return nil, rerr
			}
			if diskBytes != nil {
				return nil, &ConflictError{Path: norm, Baseline: "", Disk: diskDigest}
			}
			out = append(out, preparedChange{
				preview: ChangePreview{
					Kind:        ChangeCreate,
					RelPath:     norm,
					AfterDigest: Digest(ch.NewContent),
					AfterBytes:  len(ch.NewContent),
				},
				mutations: []fileMutation{{
					rel:        norm,
					existed:    false,
					writeBytes: cloneBytes(ch.NewContent),
				}},
			})

		case ChangeUpdate:
			norm, err := claimPath(ch.RelPath)
			if err != nil {
				return nil, err
			}
			if ch.ExpectedBaseline == "" {
				return nil, configErrorf("update of %q requires a non-empty baseline digest", norm)
			}
			if err := checkContent(norm, ch.NewContent, ch.MaxBytes); err != nil {
				return nil, err
			}
			diskBytes, diskDigest, rerr := cs.readOptional(norm)
			if rerr != nil {
				return nil, rerr
			}
			if diskBytes == nil {
				return nil, &ConflictError{Path: norm, Baseline: ch.ExpectedBaseline, Disk: ""}
			}
			if diskDigest != ch.ExpectedBaseline {
				return nil, &ConflictError{Path: norm, Baseline: ch.ExpectedBaseline, Disk: diskDigest}
			}
			out = append(out, preparedChange{
				preview: ChangePreview{
					Kind:         ChangeUpdate,
					RelPath:      norm,
					BeforeDigest: diskDigest,
					AfterDigest:  Digest(ch.NewContent),
					BeforeBytes:  len(diskBytes),
					AfterBytes:   len(ch.NewContent),
				},
				mutations: []fileMutation{{
					rel:        norm,
					existed:    true,
					priorBytes: diskBytes,
					writeBytes: cloneBytes(ch.NewContent),
				}},
			})

		case ChangeDelete:
			norm, err := claimPath(ch.RelPath)
			if err != nil {
				return nil, err
			}
			if ch.ExpectedBaseline == "" {
				return nil, configErrorf("delete of %q requires a non-empty baseline digest", norm)
			}
			diskBytes, diskDigest, rerr := cs.readOptional(norm)
			if rerr != nil {
				return nil, rerr
			}
			if diskBytes == nil {
				return nil, &ConflictError{Path: norm, Baseline: ch.ExpectedBaseline, Disk: ""}
			}
			if diskDigest != ch.ExpectedBaseline {
				return nil, &ConflictError{Path: norm, Baseline: ch.ExpectedBaseline, Disk: diskDigest}
			}
			out = append(out, preparedChange{
				preview: ChangePreview{
					Kind:         ChangeDelete,
					RelPath:      norm,
					BeforeDigest: diskDigest,
					BeforeBytes:  len(diskBytes),
				},
				mutations: []fileMutation{{
					rel:         norm,
					existed:     true,
					priorBytes:  diskBytes,
					deleteAfter: true,
				}},
			})

		case ChangeMove:
			srcNorm, err := claimPath(ch.RelPath)
			if err != nil {
				return nil, err
			}
			dstNorm, err := claimPath(ch.TargetRelPath)
			if err != nil {
				return nil, err
			}
			if ch.ExpectedBaseline == "" {
				return nil, configErrorf("move of %q requires a non-empty baseline digest", srcNorm)
			}
			srcBytes, srcDigest, rerr := cs.readOptional(srcNorm)
			if rerr != nil {
				return nil, rerr
			}
			if srcBytes == nil {
				return nil, &ConflictError{Path: srcNorm, Baseline: ch.ExpectedBaseline, Disk: ""}
			}
			if srcDigest != ch.ExpectedBaseline {
				return nil, &ConflictError{Path: srcNorm, Baseline: ch.ExpectedBaseline, Disk: srcDigest}
			}
			dstBytes, dstDigest, rerr := cs.readOptional(dstNorm)
			if rerr != nil {
				return nil, rerr
			}
			if dstBytes != nil {
				return nil, &ConflictError{Path: dstNorm, Baseline: "", Disk: dstDigest}
			}
			targetContent := srcBytes
			if ch.NewContent != nil {
				targetContent = cloneBytes(ch.NewContent)
			}
			if err := checkContent(dstNorm, targetContent, ch.MaxBytes); err != nil {
				return nil, err
			}
			out = append(out, preparedChange{
				preview: ChangePreview{
					Kind:         ChangeMove,
					RelPath:      srcNorm,
					TargetPath:   dstNorm,
					BeforeDigest: srcDigest,
					AfterDigest:  Digest(targetContent),
					BeforeBytes:  len(srcBytes),
					AfterBytes:   len(targetContent),
				},
				// Write target first, then remove source: if target fails source is
				// untouched; if source removal fails target is rolled back.
				mutations: []fileMutation{
					{
						rel:        dstNorm,
						existed:    false,
						writeBytes: targetContent,
					},
					{
						rel:         srcNorm,
						existed:     true,
						priorBytes:  srcBytes,
						deleteAfter: true,
					},
				},
			})

		default:
			return nil, configErrorf("unknown change kind %q", ch.Kind)
		}
	}
	return out, nil
}

func (cs *ChangeSet) readOptional(norm string) ([]byte, string, error) {
	abs, err := cs.store.resolve(norm)
	if err != nil {
		return nil, "", &ConfigError{Err: err}
	}
	b, err := os.ReadFile(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, "", nil
		}
		return nil, "", err
	}
	return b, Digest(b), nil
}

// Apply validates and commits every staged change. If any operation fails mid-way,
// Apply rolls back all prior operations in reverse order before returning the
// error so the project directory is never left in a half-mutated state.
func (cs *ChangeSet) Apply() (*ChangeReceipt, error) {
	prepared, err := cs.prepare()
	if err != nil {
		return nil, err
	}
	receipt := &ChangeReceipt{
		store:    cs.store,
		Previews: make([]ChangePreview, len(prepared)),
	}
	stepIdx := 0
	for i, p := range prepared {
		receipt.Previews[i] = p.preview
		for _, mut := range p.mutations {
			if cs.beforeWriteStep != nil {
				if hookErr := cs.beforeWriteStep(stepIdx, mut.rel); hookErr != nil {
					_ = receipt.Rollback()
					return nil, hookErr
				}
			}
			stepIdx++
			if mut.deleteAfter {
				expected := ""
				if mut.existed {
					expected = Digest(mut.priorBytes)
				}
				if err := cs.store.RemoveChecked(mut.rel, expected); err != nil {
					_ = receipt.Rollback()
					return nil, err
				}
				receipt.applied = append(receipt.applied, appliedStep{
					rel:        mut.rel,
					existed:    mut.existed,
					priorBytes: mut.priorBytes,
				})
				continue
			}
			expected := ""
			if mut.existed {
				expected = Digest(mut.priorBytes)
			}
			if err := cs.store.SaveChecked(mut.rel, mut.writeBytes, expected); err != nil {
				_ = receipt.Rollback()
				return nil, err
			}
			receipt.applied = append(receipt.applied, appliedStep{
				rel:         mut.rel,
				existed:     mut.existed,
				priorBytes:  mut.priorBytes,
				finalDigest: Digest(mut.writeBytes),
			})
		}
	}
	return receipt, nil
}

// Rollback restores all files touched by this receipt back to their exact
// pre-apply state in reverse order. Calling Rollback more than once is a no-op.
func (r *ChangeReceipt) Rollback() error {
	if r == nil || r.rolledBack {
		return nil
	}
	var firstErr error
	for i := len(r.applied) - 1; i >= 0; i-- {
		step := r.applied[i]
		if step.existed {
			if err := r.store.WriteAtomic(step.rel, step.priorBytes); err != nil && firstErr == nil {
				firstErr = fmt.Errorf("rollback restore %s: %w", step.rel, err)
			}
			continue
		}
		abs, err := r.store.resolve(step.rel)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if err := os.Remove(abs); err != nil && !os.IsNotExist(err) && firstErr == nil {
			firstErr = fmt.Errorf("rollback remove %s: %w", step.rel, err)
		}
	}
	if firstErr == nil {
		r.rolledBack = true
	}
	return firstErr
}

func cloneBytes(b []byte) []byte {
	if b == nil {
		return nil
	}
	out := make([]byte, len(b))
	copy(out, b)
	return out
}

func baseNameSlash(rel string) string {
	idx := strings.LastIndexByte(rel, '/')
	if idx < 0 {
		return rel
	}
	return rel[idx+1:]
}

func isYAMLPath(rel string) bool {
	lower := strings.ToLower(rel)
	return strings.HasSuffix(lower, ".yaml") || strings.HasSuffix(lower, ".yml")
}

func isMarkdownPath(rel string) bool {
	return strings.HasSuffix(strings.ToLower(rel), ".md")
}
