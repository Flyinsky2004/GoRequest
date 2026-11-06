package gitservice

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// DiffRequest selects one side of a file's change.
type DiffRequest struct {
	// Path is the current repository-relative path.
	Path string
	// OrigPath is the pre-rename path from status when known; empty means Path.
	OrigPath string
	// Staged compares HEAD with the index. When false it compares the index
	// with the working tree (or HEAD with the working tree for unmerged paths).
	Staged bool
}

// FileDiff is the bounded text pair the native editor renders. Binary or
// oversized sides are reported instead of being loaded into memory.
type FileDiff struct {
	Path      string
	OrigPath  string
	Staged    bool
	OldText   string
	NewText   string
	OldSize   int64
	NewSize   int64
	Binary    bool
	TooLarge  bool
	OldExists bool
	NewExists bool
	Unmerged  bool
}

// Diff builds the old/new text for one path without invoking external diff or
// textconv programs. Blobs come from `git cat-file`; working-tree content is
// read directly.
func (s *Service) Diff(ctx context.Context, repoRoot string, req DiffRequest) (*FileDiff, error) {
	if s == nil {
		return nil, ErrGitUnavailable
	}
	root, err := s.requireRoot(repoRoot)
	if err != nil {
		return nil, err
	}
	rel, err := CleanPath(req.Path)
	if err != nil {
		return nil, err
	}
	orig := rel
	if req.OrigPath != "" {
		orig, err = CleanPath(req.OrigPath)
		if err != nil {
			return nil, err
		}
	}
	out := &FileDiff{Path: rel, OrigPath: orig, Staged: req.Staged}

	var oldSide, newSide diffSide
	if req.Staged {
		oldSide, err = s.blobSide(ctx, root, "HEAD:"+orig)
		if err != nil {
			return nil, err
		}
		newSide, err = s.blobSide(ctx, root, ":"+rel)
		if err != nil {
			return nil, err
		}
		if !newSide.exists {
			// Stage 0 missing means a staged deletion or an unmerged path.
			if unmerged, uerr := s.pathUnmerged(ctx, root, rel); uerr == nil && unmerged {
				out.Unmerged = true
			}
		}
	} else {
		oldSide, err = s.blobSide(ctx, root, ":"+orig)
		if err != nil {
			return nil, err
		}
		newSide, err = s.worktreeSide(root, rel)
		if err != nil {
			return nil, err
		}
		if !oldSide.exists {
			if unmerged, uerr := s.pathUnmerged(ctx, root, rel); uerr == nil && unmerged {
				out.Unmerged = true
				// Show the unresolved working tree against HEAD so the user can
				// see what is on disk, while the status badge warns it is unmerged.
				if headSide, herr := s.blobSide(ctx, root, "HEAD:"+orig); herr == nil {
					oldSide = headSide
				}
			}
		}
	}

	out.OldText = oldSide.text
	out.NewText = newSide.text
	out.OldSize = oldSide.size
	out.NewSize = newSide.size
	out.OldExists = oldSide.exists
	out.NewExists = newSide.exists
	out.Binary = oldSide.binary || newSide.binary
	out.TooLarge = oldSide.tooLarge || newSide.tooLarge
	return out, nil
}

type diffSide struct {
	text     string
	size     int64
	exists   bool
	binary   bool
	tooLarge bool
}

func (s *Service) blobSide(ctx context.Context, root, spec string) (diffSide, error) {
	size, exists, err := s.blobSize(ctx, root, spec)
	if err != nil {
		return diffSide{}, err
	}
	if !exists {
		return diffSide{}, nil
	}
	if size > s.maxDiff {
		return diffSide{size: size, exists: true, tooLarge: true}, nil
	}
	res, err := s.run(ctx, root, runOpts{
		op:       "cat-file",
		repoRoot: root,
		maxOut:   s.maxDiff + 1,
		args:     []string{"cat-file", "blob", spec},
	})
	if err != nil {
		return diffSide{}, err
	}
	text := string(res.stdout)
	return diffSide{
		text:   text,
		size:   size,
		exists: true,
		binary: isBinary(res.stdout),
	}, nil
}

func (s *Service) blobSize(ctx context.Context, root, spec string) (int64, bool, error) {
	res, err := s.run(ctx, root, runOpts{
		op:       "cat-file",
		repoRoot: root,
		args:     []string{"cat-file", "-s", spec},
	})
	if err != nil {
		if isMissingObject(err) {
			return 0, false, nil
		}
		if isCanceled(ctx, err) {
			return 0, false, canceledErr(ctx, err)
		}
		return 0, false, err
	}
	n, err := strconv.ParseInt(strings.TrimSpace(string(res.stdout)), 10, 64)
	if err != nil {
		return 0, false, fmt.Errorf("parse git blob size: %w", err)
	}
	return n, true, nil
}

func (s *Service) worktreeSide(root, rel string) (diffSide, error) {
	p := worktreePath(root, rel)
	info, err := os.Lstat(p)
	if err != nil {
		if os.IsNotExist(err) {
			return diffSide{}, nil
		}
		return diffSide{}, fmt.Errorf("read working tree %s: %w", rel, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(p)
		if err != nil {
			return diffSide{}, fmt.Errorf("read symlink %s: %w", rel, err)
		}
		return diffSide{text: target, size: int64(len(target)), exists: true}, nil
	}
	if !info.Mode().IsRegular() {
		return diffSide{}, fmt.Errorf("%w: %s is not a regular file", ErrUnsupportedPath, rel)
	}
	if info.Size() > s.maxDiff {
		return diffSide{size: info.Size(), exists: true, tooLarge: true}, nil
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return diffSide{}, fmt.Errorf("read working tree %s: %w", rel, err)
	}
	return diffSide{text: string(data), size: int64(len(data)), exists: true, binary: isBinary(data)}, nil
}

func (s *Service) pathUnmerged(ctx context.Context, root, rel string) (bool, error) {
	res, err := s.run(ctx, root, runOpts{
		op:       "ls-files",
		repoRoot: root,
		args:     []string{"ls-files", "-u", "-z", "--", rel},
	})
	if err != nil {
		return false, err
	}
	return len(res.stdout) > 0, nil
}

func isMissingObject(err error) bool {
	var ce *CommandError
	if !errors.As(err, &ce) {
		return false
	}
	msg := strings.ToLower(ce.Stderr)
	return strings.Contains(msg, "does not exist") ||
		strings.Contains(msg, "not a valid object name") ||
		strings.Contains(msg, "exists on disk, but not in") ||
		strings.Contains(msg, "bad file") ||
		(strings.Contains(msg, "invalid object name") && strings.Contains(msg, "head"))
}
