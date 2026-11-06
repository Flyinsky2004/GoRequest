package gitservice

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// CommitRequest selects files and a message for one selective commit.
type CommitRequest struct {
	Message string
	Paths   []string
}

// CommitResult reports the commit that was created. IndexReconciled lists the
// paths whose real-index entries were advanced to the new HEAD; IndexWarning is
// set when the commit succeeded but that local index refresh failed.
type CommitResult struct {
	Commit          string
	Tree            string
	Parent          string
	Branch          string
	Paths           []string
	IndexReconciled []string
	IndexWarning    string
}

// Commit creates a commit containing exactly the selected paths. It builds the
// tree in an isolated temporary index seeded from HEAD, so content already
// staged for other paths can never be included. For each selected path:
//
//   - if the real index differs from HEAD, the staged (index) version is used,
//     matching what `git commit -- <path>` would record;
//   - otherwise the working-tree version is hashed without clean/smudge
//     filters, and deletions are recorded as removals.
//
// The commit is written with plumbing (read-tree, hash-object, update-index,
// write-tree, commit-tree, update-ref), so no repository hook, signing program
// or configured external command runs. The real index is advanced only for
// paths committed from the working tree, leaving every other staged entry
// untouched.
func (s *Service) Commit(ctx context.Context, repoRoot string, req CommitRequest) (*CommitResult, error) {
	if s == nil {
		return nil, ErrGitUnavailable
	}
	root, err := s.requireRoot(repoRoot)
	if err != nil {
		return nil, err
	}
	msg := strings.TrimSpace(req.Message)
	if msg == "" {
		return nil, fmt.Errorf("commit message is required")
	}
	if len(msg) > defaultMaxMessageBytes {
		return nil, fmt.Errorf("commit message exceeds %d bytes", defaultMaxMessageBytes)
	}
	paths, err := cleanPaths(req.Paths)
	if err != nil {
		return nil, err
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("select at least one file to commit")
	}

	ref, err := s.symbolicHead(ctx, root)
	if err != nil {
		return nil, err
	}
	parent, hasParent, err := s.revParseVerify(ctx, root, "HEAD^{commit}")
	if err != nil {
		return nil, err
	}
	var headTree string
	if hasParent {
		headTree, _, err = s.revParseVerify(ctx, root, "HEAD^{tree}")
		if err != nil {
			return nil, err
		}
	}

	tmpDir, err := os.MkdirTemp("", "gorequest-git-index-*")
	if err != nil {
		return nil, fmt.Errorf("create isolated git index: %w", err)
	}
	defer os.RemoveAll(tmpDir)
	indexPath := filepath.Join(tmpDir, "index")
	indexEnv := []string{"GIT_INDEX_FILE=" + indexPath}

	if hasParent {
		if _, err := s.run(ctx, root, runOpts{
			op: "read-tree", repoRoot: root, env: indexEnv,
			args: []string{"read-tree", parent},
		}); err != nil {
			return nil, err
		}
	} else {
		if _, err := s.run(ctx, root, runOpts{
			op: "read-tree", repoRoot: root, env: indexEnv,
			args: []string{"read-tree", "--empty"},
		}); err != nil {
			return nil, err
		}
	}

	fromWorktree := make(map[string]stagedWork, len(paths))
	result := &CommitResult{Parent: parent, Branch: strings.TrimPrefix(ref, "refs/heads/"), Paths: append([]string(nil), paths...)}
	for _, rel := range paths {
		idx, hasIndex, err := s.readIndexEntry(ctx, root, rel)
		if err != nil {
			return nil, err
		}
		if hasIndex && idx.Stage > 0 {
			return nil, fmt.Errorf("%w: %s", ErrUnmerged, rel)
		}
		intentToAdd := hasIndex && isZeroSHA(idx.SHA)
		if intentToAdd {
			hasIndex = false
		}
		if hasIndex && idx.Mode == "160000" {
			return nil, fmt.Errorf("%w: submodule path %s", ErrUnsupportedPath, rel)
		}
		head, hasHead, err := s.readTreeEntry(ctx, root, parent, rel)
		if err != nil {
			return nil, err
		}
		var work stagedWork
		switch {
		case intentToAdd:
			// `git add -N` intent-to-add: record the working-tree content.
			work, err = s.stageWorktreeFile(ctx, root, indexEnv, rel)
			fromWorktree[rel] = work
		case hasIndex && hasHead && idx.Mode == head.Mode && idx.SHA == head.SHA:
			// Index matches HEAD: commit the working-tree state.
			work, err = s.stageWorktreeFile(ctx, root, indexEnv, rel)
			fromWorktree[rel] = work
		case hasIndex:
			// Staged content: use exactly the index entry.
			if err = s.addIndexEntry(ctx, root, indexEnv, idx, rel); err == nil {
				work = stagedWork{Mode: idx.Mode, SHA: idx.SHA}
			}
		case hasHead:
			// No index entry but HEAD has the path: staged deletion.
			err = s.removeIndexEntry(ctx, root, indexEnv, rel)
			work = stagedWork{Deleted: true}
			fromWorktree[rel] = work
		default:
			// Untracked: add the working-tree content.
			work, err = s.stageWorktreeFile(ctx, root, indexEnv, rel)
			fromWorktree[rel] = work
		}
		if err != nil {
			return nil, err
		}
	}

	treeRes, err := s.run(ctx, root, runOpts{
		op: "write-tree", repoRoot: root, env: indexEnv,
		args: []string{"write-tree"},
	})
	if err != nil {
		return nil, err
	}
	tree := strings.TrimSpace(string(treeRes.stdout))
	if tree == "" {
		return nil, fmt.Errorf("git write-tree returned no tree")
	}
	if hasParent && tree == headTree {
		return nil, &NoChangesError{Paths: append([]string(nil), paths...)}
	}

	message := msg
	if !strings.HasSuffix(message, "\n") {
		message += "\n"
	}
	commitArgs := []string{"commit-tree", tree}
	if hasParent {
		commitArgs = append(commitArgs, "-p", parent)
	}
	commitArgs = append(commitArgs, "-F", "-")
	commitRes, err := s.run(ctx, root, runOpts{
		op:       "commit-tree",
		repoRoot: root,
		stdin:    strings.NewReader(message),
		args:     commitArgs,
	})
	if err != nil {
		return nil, err
	}
	commit := strings.TrimSpace(string(commitRes.stdout))
	if commit == "" {
		return nil, fmt.Errorf("git commit-tree returned no commit")
	}

	old := parent
	if !hasParent {
		old = strings.Repeat("0", 40)
	}
	if _, err := s.run(ctx, root, runOpts{
		op:       "update-ref",
		repoRoot: root,
		args:     []string{"update-ref", "-m", "gorequest: selective commit", ref, commit, old},
	}); err != nil {
		return nil, err
	}

	// Keep the real index coherent for paths we committed from the working
	// tree. Paths committed from the index already match the new HEAD, and
	// every unselected staged entry is left exactly as it was.
	for _, rel := range paths {
		work, ok := fromWorktree[rel]
		if !ok {
			continue
		}
		var ierr error
		if work.Deleted {
			ierr = s.removeIndexEntry(ctx, root, nil, rel)
		} else {
			ierr = s.addIndexEntry(ctx, root, nil, indexEntry{Mode: work.Mode, SHA: work.SHA}, rel)
		}
		if ierr != nil {
			if result.IndexWarning == "" {
				result.IndexWarning = fmt.Sprintf("%s: %v", rel, ierr)
			}
			continue
		}
		result.IndexReconciled = append(result.IndexReconciled, rel)
	}

	result.Commit = commit
	result.Tree = tree
	return result, nil
}

type indexEntry struct {
	Mode  string
	SHA   string
	Stage int
}

type stagedWork struct {
	Mode    string
	SHA     string
	Deleted bool
}

func (s *Service) symbolicHead(ctx context.Context, root string) (string, error) {
	res, err := s.run(ctx, root, runOpts{
		op: "symbolic-ref", repoRoot: root,
		args: []string{"symbolic-ref", "--quiet", "HEAD"},
	})
	if err != nil {
		var ce *CommandError
		if errors.As(err, &ce) && ce.ExitCode == 1 && strings.TrimSpace(ce.Stderr) == "" {
			return "", ErrDetachedHead
		}
		return "", err
	}
	ref := strings.TrimSpace(string(res.stdout))
	if !strings.HasPrefix(ref, "refs/heads/") {
		return "", ErrDetachedHead
	}
	return ref, nil
}

func (s *Service) revParseVerify(ctx context.Context, root, spec string) (string, bool, error) {
	res, err := s.run(ctx, root, runOpts{
		op: "rev-parse", repoRoot: root,
		args: []string{"rev-parse", "--verify", "--quiet", spec},
	})
	if err != nil {
		var ce *CommandError
		if errors.As(err, &ce) && ce.ExitCode == 1 && strings.TrimSpace(ce.Stderr) == "" {
			return "", false, nil
		}
		return "", false, err
	}
	sha := strings.TrimSpace(string(res.stdout))
	return sha, sha != "", nil
}

func (s *Service) readIndexEntry(ctx context.Context, root, rel string) (indexEntry, bool, error) {
	res, err := s.run(ctx, root, runOpts{
		op: "ls-files", repoRoot: root,
		args: []string{"ls-files", "-s", "-z", "--", rel},
	})
	if err != nil {
		return indexEntry{}, false, err
	}
	var found indexEntry
	has := false
	for _, rec := range strings.Split(string(res.stdout), "\x00") {
		if rec == "" {
			continue
		}
		tab := strings.IndexByte(rec, '\t')
		if tab < 0 {
			return indexEntry{}, false, fmt.Errorf("parse git index entry %q", rec)
		}
		fields := strings.Fields(rec[:tab])
		if len(fields) != 3 {
			return indexEntry{}, false, fmt.Errorf("parse git index entry %q", rec)
		}
		stage := 0
		if _, err := fmt.Sscanf(fields[2], "%d", &stage); err != nil {
			return indexEntry{}, false, fmt.Errorf("parse git index stage %q", rec)
		}
		if stage > 0 {
			return indexEntry{Mode: fields[0], SHA: fields[1], Stage: stage}, true, nil
		}
		if !has {
			found = indexEntry{Mode: fields[0], SHA: fields[1]}
			has = true
		}
	}
	return found, has, nil
}

func (s *Service) readTreeEntry(ctx context.Context, root, treeish, rel string) (indexEntry, bool, error) {
	if treeish == "" {
		return indexEntry{}, false, nil
	}
	res, err := s.run(ctx, root, runOpts{
		op: "ls-tree", repoRoot: root,
		args: []string{"ls-tree", "-z", treeish, "--", rel},
	})
	if err != nil {
		return indexEntry{}, false, err
	}
	for _, rec := range strings.Split(string(res.stdout), "\x00") {
		if rec == "" {
			continue
		}
		tab := strings.IndexByte(rec, '\t')
		if tab < 0 {
			return indexEntry{}, false, fmt.Errorf("parse git tree entry %q", rec)
		}
		fields := strings.Fields(rec[:tab])
		if len(fields) != 3 {
			return indexEntry{}, false, fmt.Errorf("parse git tree entry %q", rec)
		}
		if fields[1] == "commit" {
			return indexEntry{}, false, fmt.Errorf("%w: submodule path %s", ErrUnsupportedPath, rel)
		}
		return indexEntry{Mode: fields[0], SHA: fields[2]}, true, nil
	}
	return indexEntry{}, false, nil
}

func (s *Service) addIndexEntry(ctx context.Context, root string, env []string, entry indexEntry, rel string) error {
	_, err := s.run(ctx, root, runOpts{
		op: "update-index", repoRoot: root, env: env,
		args: []string{"update-index", "--add", "--cacheinfo", entry.Mode, entry.SHA, rel},
	})
	return err
}

func (s *Service) removeIndexEntry(ctx context.Context, root string, env []string, rel string) error {
	_, err := s.run(ctx, root, runOpts{
		op: "update-index", repoRoot: root, env: env,
		args: []string{"update-index", "--force-remove", "--", rel},
	})
	return err
}

// stageWorktreeFile hashes the current working-tree content into the isolated
// index. It refuses files above the configured cap, submodules/directories and
// files that change while being read.
func (s *Service) stageWorktreeFile(ctx context.Context, root string, env []string, rel string) (stagedWork, error) {
	p := worktreePath(root, rel)
	info, err := os.Lstat(p)
	if err != nil {
		if os.IsNotExist(err) {
			if err := s.removeIndexEntry(ctx, root, env, rel); err != nil {
				return stagedWork{}, err
			}
			return stagedWork{Deleted: true}, nil
		}
		return stagedWork{}, fmt.Errorf("read %s: %w", rel, err)
	}

	var content io.Reader
	var mode string
	switch {
	case info.Mode()&os.ModeSymlink != 0:
		target, err := os.Readlink(p)
		if err != nil {
			return stagedWork{}, fmt.Errorf("read symlink %s: %w", rel, err)
		}
		mode = "120000"
		content = strings.NewReader(target)
	case info.Mode().IsRegular():
		if info.Size() > s.maxCommit {
			return stagedWork{}, fmt.Errorf("%s is larger than the %d-byte commit limit", rel, s.maxCommit)
		}
		f, err := os.Open(p)
		if err != nil {
			return stagedWork{}, fmt.Errorf("open %s: %w", rel, err)
		}
		defer f.Close()
		mode = "100644"
		if info.Mode().Perm()&0o111 != 0 {
			mode = "100755"
		}
		content = f
	default:
		return stagedWork{}, fmt.Errorf("%w: %s is not a regular file", ErrUnsupportedPath, rel)
	}

	hashRes, err := s.run(ctx, root, runOpts{
		op: "hash-object", repoRoot: root, stdin: content,
		args: []string{"hash-object", "-w", "--no-filters", "--stdin"},
	})
	if err != nil {
		return stagedWork{}, err
	}
	sha := strings.TrimSpace(string(hashRes.stdout))
	if len(sha) != 40 && len(sha) != 64 {
		return stagedWork{}, fmt.Errorf("git hash-object returned %q", sha)
	}
	if info.Mode().IsRegular() {
		after, err := os.Lstat(p)
		if err != nil || after.Size() != info.Size() || !after.ModTime().Equal(info.ModTime()) {
			return stagedWork{}, fmt.Errorf("%s changed while it was being committed; refresh and try again", rel)
		}
	}
	if err := s.addIndexEntry(ctx, root, env, indexEntry{Mode: mode, SHA: sha}, rel); err != nil {
		return stagedWork{}, err
	}
	return stagedWork{Mode: mode, SHA: sha}, nil
}

func isZeroSHA(sha string) bool {
	if sha == "" {
		return true
	}
	for _, c := range sha {
		if c != '0' {
			return false
		}
	}
	return true
}
