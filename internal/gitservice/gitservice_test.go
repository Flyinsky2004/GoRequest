package gitservice

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func testGit(t *testing.T) string {
	t.Helper()
	p, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git executable not available")
	}
	return p
}

func newTestService(t *testing.T) *Service {
	t.Helper()
	s, err := New(Options{})
	if err != nil {
		t.Skipf("git service unavailable: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=T", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=T", "GIT_COMMITTER_EMAIL=t@example.com")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func initRepo(t *testing.T) string {
	t.Helper()
	testGit(t)
	dir := t.TempDir()
	runGit(t, dir, "init", "--quiet", "-b", "main")
	runGit(t, dir, "config", "user.name", "T")
	runGit(t, dir, "config", "user.email", "t@example.com")
	return dir
}

func writeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func commitAll(t *testing.T, root, msg string) string {
	t.Helper()
	runGit(t, root, "add", "-A")
	runGit(t, root, "commit", "--quiet", "-m", msg)
	return runGit(t, root, "rev-parse", "HEAD")
}

func TestNewUnavailable(t *testing.T) {
	_, err := New(Options{GitPath: filepath.Join(t.TempDir(), "missing-git")})
	if !errors.Is(err, ErrGitUnavailable) {
		t.Fatalf("want ErrGitUnavailable, got %v", err)
	}
}

func TestDiscoverNonRepositoryAndRepository(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()

	plain := t.TempDir()
	if _, err := s.Discover(ctx, plain); !errors.Is(err, ErrNotRepository) {
		t.Fatalf("plain dir: want ErrNotRepository, got %v", err)
	}

	root := initRepo(t)
	writeFile(t, root, "a.txt", "a\n")
	commitAll(t, root, "init")
	sub := filepath.Join(root, "nested", "project")
	if err := os.MkdirAll(filepath.Join(sub, ".gorequest"), 0o755); err != nil {
		t.Fatal(err)
	}
	repo, err := s.Discover(ctx, sub)
	if err != nil {
		t.Fatal(err)
	}
	if repo.Root != canonicalDir(root) {
		t.Fatalf("root = %q, want %q", repo.Root, canonicalDir(root))
	}
	if repo.Branch != "main" || repo.Detached {
		t.Fatalf("branch = %q detached=%v", repo.Branch, repo.Detached)
	}
}

func TestStatusMarksAddModifyDeleteAndUntracked(t *testing.T) {
	s := newTestService(t)
	root := initRepo(t)
	writeFile(t, root, "mod.txt", "one\n")
	writeFile(t, root, "del.txt", "gone\n")
	commitAll(t, root, "init")

	writeFile(t, root, "mod.txt", "two\n")
	if err := os.Remove(filepath.Join(root, "del.txt")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, root, "new.txt", "new\n")

	st, err := s.Status(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	byPath := map[string]Entry{}
	for _, e := range st.Entries {
		byPath[e.Path] = e
	}
	if e := byPath["mod.txt"]; e.Marker() != "M" || e.Staged() {
		t.Fatalf("mod entry: %+v", e)
	}
	if e := byPath["del.txt"]; e.Marker() != "D" {
		t.Fatalf("delete entry: %+v", e)
	}
	if e := byPath["new.txt"]; !e.Untracked || e.Marker() != "?" {
		t.Fatalf("untracked entry: %+v", e)
	}
	if st.Branch != "main" || st.Detached {
		t.Fatalf("branch=%q detached=%v", st.Branch, st.Detached)
	}
	if st.StagedCount() != 0 {
		t.Fatalf("staged count = %d", st.StagedCount())
	}
}

func TestSelectiveCommitExcludesPreexistingStagedContent(t *testing.T) {
	s := newTestService(t)
	root := initRepo(t)
	writeFile(t, root, "a.txt", "A1\n")
	writeFile(t, root, "b.txt", "B1\n")
	base := commitAll(t, root, "init")

	writeFile(t, root, "a.txt", "A2\n")
	writeFile(t, root, "b.txt", "B2\n")
	writeFile(t, root, "untracked.txt", "U\n")
	runGit(t, root, "add", "b.txt") // pre-existing staged content

	res, err := s.Commit(context.Background(), root, CommitRequest{
		Message: "only a",
		Paths:   []string{"a.txt"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Commit == "" || res.Parent != base {
		t.Fatalf("result: %+v", res)
	}
	files := runGit(t, root, "show", "--name-only", "--format=", "HEAD")
	if files != "a.txt" {
		t.Fatalf("commit touched %q, want only a.txt", files)
	}
	if strings.Contains(files, "untracked.txt") {
		t.Fatal("unselected untracked file leaked into commit")
	}
	if got := runGit(t, root, "show", "HEAD:a.txt"); got != "A2" {
		t.Fatalf("committed a = %q", got)
	}
	if got := runGit(t, root, "show", "HEAD:b.txt"); got != "B1" {
		t.Fatalf("staged b leaked into commit: %q", got)
	}
	// The unselected staged entry is still staged and unchanged.
	if got := runGit(t, root, "show", ":b.txt"); got != "B2" {
		t.Fatalf("real index b = %q, want B2", got)
	}
	cached := runGit(t, root, "diff", "--cached", "--name-only")
	if cached != "b.txt" {
		t.Fatalf("cached changes = %q, want b.txt", cached)
	}
	// The committed path is reconciled in the real index, so it is no longer dirty.
	if status := runGit(t, root, "status", "--porcelain"); strings.Contains(status, "a.txt") {
		t.Fatalf("a.txt still dirty after commit:\n%s", status)
	}
}

func TestCommitRunsNoRepositoryHooks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("hook fixtures use POSIX scripts")
	}
	s := newTestService(t)
	root := initRepo(t)
	writeFile(t, root, "a.txt", "1\n")
	commitAll(t, root, "init")
	writeFile(t, root, "a.txt", "2\n")

	var markers []string
	for _, hook := range []string{"pre-commit", "post-commit", "reference-transaction", "pre-push", "post-merge"} {
		marker := filepath.Join(root, "hook-"+hook+".ran")
		markers = append(markers, marker)
		hookPath := filepath.Join(root, ".git", "hooks", hook)
		script := "#!/bin/sh\ntouch " + marker + "\n"
		if err := os.WriteFile(hookPath, []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// A repository-configured hooks directory must not win either.
	evilHooks := filepath.Join(root, "evil-hooks")
	if err := os.MkdirAll(evilHooks, 0o755); err != nil {
		t.Fatal(err)
	}
	evilMarker := filepath.Join(root, "evil-hook.ran")
	if err := os.WriteFile(filepath.Join(evilHooks, "pre-commit"), []byte("#!/bin/sh\ntouch "+evilMarker+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, root, "config", "core.hooksPath", evilHooks)

	if _, err := s.Commit(context.Background(), root, CommitRequest{Message: "hookless", Paths: []string{"a.txt"}}); err != nil {
		t.Fatal(err)
	}
	for _, marker := range append(markers, evilMarker) {
		if _, err := os.Stat(marker); err == nil {
			t.Fatalf("hook ran: %s", marker)
		}
	}
}

func TestRepositoryCredentialHelperDoesNotRun(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("credential helper fixture uses a POSIX shell command")
	}
	s := newTestService(t)
	root := initRepo(t)
	writeFile(t, root, "a.txt", "1\n")
	commitAll(t, root, "init")

	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.WriteFile(filepath.Join(home, ".gitconfig"), []byte("[credential]\n\thelper = store\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(root, "repo-helper.ran")
	runGit(t, root, "config", "credential.helper", "!touch "+marker)

	_, _ = s.run(context.Background(), root, runOpts{
		op:    "credential",
		stdin: strings.NewReader("protocol=https\nhost=example.invalid\n"),
		args:  []string{"credential", "fill"},
	})
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("repository-local credential helper ran")
	}
}

func TestCommitPathValidationAndLiteralFilenames(t *testing.T) {
	s := newTestService(t)
	root := initRepo(t)
	writeFile(t, root, "a.txt", "1\n")
	commitAll(t, root, "init")

	for _, bad := range []string{"../escape.txt", "/etc/passwd", ".git/config", ""} {
		if _, err := s.Commit(context.Background(), root, CommitRequest{Message: "x", Paths: []string{bad}}); err == nil {
			t.Fatalf("path %q should be rejected", bad)
		}
	}

	// A literal filename containing shell and pathspec characters must be
	// committed verbatim without any shell or glob interpretation.
	weird := "evil; touch pwned $(touch pwned2) [a].txt"
	writeFile(t, root, weird, "payload\n")
	outside := filepath.Join(root, "pwned")
	if _, err := s.Commit(context.Background(), root, CommitRequest{Message: "literal", Paths: []string{weird}}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(outside); err == nil {
		t.Fatal("shell command executed for literal filename")
	}
	files := runGit(t, root, "ls-tree", "-r", "--name-only", "HEAD")
	if !strings.Contains(files, weird) {
		t.Fatalf("literal filename missing from tree: %q", files)
	}
}

func TestCommitDetachedHeadRefused(t *testing.T) {
	s := newTestService(t)
	root := initRepo(t)
	writeFile(t, root, "a.txt", "1\n")
	commitAll(t, root, "init")
	runGit(t, root, "checkout", "--quiet", "--detach", "HEAD")
	writeFile(t, root, "a.txt", "2\n")

	_, err := s.Commit(context.Background(), root, CommitRequest{Message: "x", Paths: []string{"a.txt"}})
	if !errors.Is(err, ErrDetachedHead) {
		t.Fatalf("want ErrDetachedHead, got %v", err)
	}
}

func TestCommitInitialUnbornBranch(t *testing.T) {
	s := newTestService(t)
	root := initRepo(t)
	writeFile(t, root, "first.txt", "hello\n")
	res, err := s.Commit(context.Background(), root, CommitRequest{Message: "initial", Paths: []string{"first.txt"}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Parent != "" {
		t.Fatalf("parent = %q", res.Parent)
	}
	if got := runGit(t, root, "show", "HEAD:first.txt"); got != "hello" {
		t.Fatalf("first.txt = %q", got)
	}
}

func TestCommitDeletionAndNoChanges(t *testing.T) {
	s := newTestService(t)
	root := initRepo(t)
	writeFile(t, root, "a.txt", "1\n")
	writeFile(t, root, "b.txt", "1\n")
	commitAll(t, root, "init")

	if err := os.Remove(filepath.Join(root, "a.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Commit(context.Background(), root, CommitRequest{Message: "del", Paths: []string{"a.txt"}}); err != nil {
		t.Fatal(err)
	}
	if out, _ := exec.Command("git", "-C", root, "cat-file", "-e", "HEAD:a.txt").CombinedOutput(); len(out) == 0 {
		// cat-file -e prints nothing on success but exits non-zero for missing.
		if err := exec.Command("git", "-C", root, "cat-file", "-e", "HEAD:a.txt").Run(); err == nil {
			t.Fatal("a.txt still present in HEAD")
		}
	}
	_, err := s.Commit(context.Background(), root, CommitRequest{Message: "noop", Paths: []string{"b.txt"}})
	if !errors.Is(err, ErrNoChanges) {
		t.Fatalf("want ErrNoChanges, got %v", err)
	}
}

func TestUnmergedPathRefused(t *testing.T) {
	s := newTestService(t)
	root := initRepo(t)
	writeFile(t, root, "a.txt", "base\n")
	commitAll(t, root, "base")
	runGit(t, root, "checkout", "--quiet", "-b", "side")
	writeFile(t, root, "a.txt", "side\n")
	commitAll(t, root, "side")
	runGit(t, root, "checkout", "--quiet", "main")
	writeFile(t, root, "a.txt", "main\n")
	commitAll(t, root, "main")
	// Merge with a guaranteed conflict; the plain git call leaves conflict stages.
	cmd := exec.Command("git", "merge", "side")
	cmd.Dir = root
	_ = cmd.Run()

	st, err := s.Status(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Entries) != 1 || !st.Entries[0].Unmerged {
		t.Fatalf("expected one unmerged entry: %+v", st.Entries)
	}
	_, err = s.Commit(context.Background(), root, CommitRequest{Message: "x", Paths: []string{"a.txt"}})
	if !errors.Is(err, ErrUnmerged) {
		t.Fatalf("want ErrUnmerged, got %v", err)
	}
}

func TestDiffUnstagedAndStaged(t *testing.T) {
	s := newTestService(t)
	root := initRepo(t)
	writeFile(t, root, "a.txt", "one\ntwo\n")
	commitAll(t, root, "init")

	writeFile(t, root, "a.txt", "one\nTWO\n")
	d, err := s.Diff(context.Background(), root, DiffRequest{Path: "a.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if d.OldText != "one\ntwo\n" || d.NewText != "one\nTWO\n" || d.Binary || d.TooLarge {
		t.Fatalf("unstaged diff: %+v", d)
	}

	runGit(t, root, "add", "a.txt")
	d, err = s.Diff(context.Background(), root, DiffRequest{Path: "a.txt", Staged: true})
	if err != nil {
		t.Fatal(err)
	}
	if d.OldText != "one\ntwo\n" || d.NewText != "one\nTWO\n" {
		t.Fatalf("staged diff: %+v", d)
	}
	if !d.OldExists || !d.NewExists {
		t.Fatalf("existence flags: %+v", d)
	}
}

func TestDiffUntrackedAndBinary(t *testing.T) {
	s := newTestService(t)
	root := initRepo(t)
	writeFile(t, root, "a.txt", "1\n")
	commitAll(t, root, "init")
	writeFile(t, root, "new.txt", "hello\n")
	d, err := s.Diff(context.Background(), root, DiffRequest{Path: "new.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if d.OldExists || !d.NewExists || d.NewText != "hello\n" {
		t.Fatalf("untracked diff: %+v", d)
	}
	bin := []byte{0x00, 0x01, 0x02, 0x00}
	if err := os.WriteFile(filepath.Join(root, "bin.dat"), bin, 0o644); err != nil {
		t.Fatal(err)
	}
	d, err = s.Diff(context.Background(), root, DiffRequest{Path: "bin.dat"})
	if err != nil {
		t.Fatal(err)
	}
	if !d.Binary {
		t.Fatalf("binary not detected: %+v", d)
	}
}

func TestLargeDiffReportsTooLarge(t *testing.T) {
	s, err := New(Options{MaxDiffBytes: 8})
	if err != nil {
		t.Skip(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	root := initRepo(t)
	writeFile(t, root, "big.txt", strings.Repeat("x", 100))
	commitAll(t, root, "init")
	writeFile(t, root, "big.txt", strings.Repeat("y", 200))
	d, err := s.Diff(context.Background(), root, DiffRequest{Path: "big.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if !d.TooLarge {
		t.Fatalf("expected TooLarge: %+v", d)
	}
}

// --- remote fixtures -----------------------------------------------------

func makeOrigin(t *testing.T, root string) string {
	t.Helper()
	origin := filepath.Join(t.TempDir(), "origin.git")
	runGit(t, root, "init", "--bare", "--quiet", origin)
	runGit(t, root, "remote", "add", "origin", origin)
	runGit(t, root, "push", "--quiet", "-u", "origin", "main")
	return origin
}

func cloneWork(t *testing.T, origin string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "clone")
	runGit(t, t.TempDir(), "clone", "--quiet", origin, dir)
	runGit(t, dir, "config", "user.name", "T")
	runGit(t, dir, "config", "user.email", "t@example.com")
	return dir
}

func TestPullFastForwardAndDirtyGuard(t *testing.T) {
	s := newTestService(t)
	root := initRepo(t)
	writeFile(t, root, "a.txt", "1\n")
	commitAll(t, root, "init")
	origin := makeOrigin(t, root)

	hookMarker := filepath.Join(root, "post-merge.ran")
	if runtime.GOOS != "windows" {
		for _, hook := range []string{"post-merge", "reference-transaction"} {
			if err := os.WriteFile(filepath.Join(root, ".git", "hooks", hook),
				[]byte("#!/bin/sh\ntouch "+hookMarker+"\n"), 0o755); err != nil {
				t.Fatal(err)
			}
		}
	}

	other := cloneWork(t, origin)
	writeFile(t, other, "a.txt", "2\n")
	commitAll(t, other, "remote change")
	runGit(t, other, "push", "--quiet", "origin", "main")

	res, err := s.Pull(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Updated || res.Behind != 1 {
		t.Fatalf("pull result: %+v", res)
	}
	if got := runGit(t, root, "show", "HEAD:a.txt"); got != "2" {
		t.Fatalf("pulled content = %q", got)
	}
	if runtime.GOOS != "windows" {
		if _, err := os.Stat(hookMarker); err == nil {
			t.Fatal("pull ran a repository hook")
		}
	}
	st, err := s.Status(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if st.Upstream != "origin/main" || st.Ahead != 0 || st.Behind != 0 {
		t.Fatalf("status after pull: upstream=%q ahead=%d behind=%d", st.Upstream, st.Ahead, st.Behind)
	}

	writeFile(t, root, "a.txt", "dirty\n")
	_, err = s.Pull(context.Background(), root)
	if !errors.Is(err, ErrDirtyWorkingTree) {
		t.Fatalf("want ErrDirtyWorkingTree, got %v", err)
	}
	if got := runGit(t, root, "show", ":a.txt"); got != "2" {
		t.Fatalf("dirty pull changed index: %q", got)
	}
}

func TestPullDivergedRefused(t *testing.T) {
	s := newTestService(t)
	root := initRepo(t)
	writeFile(t, root, "a.txt", "1\n")
	commitAll(t, root, "init")
	origin := makeOrigin(t, root)
	other := cloneWork(t, origin)
	writeFile(t, other, "a.txt", "remote\n")
	commitAll(t, other, "remote")
	runGit(t, other, "push", "--quiet", "origin", "main")

	writeFile(t, root, "b.txt", "local\n")
	commitAll(t, root, "local")
	headBefore := runGit(t, root, "rev-parse", "HEAD")

	_, err := s.Pull(context.Background(), root)
	if !errors.Is(err, ErrDiverged) {
		t.Fatalf("want ErrDiverged, got %v", err)
	}
	if head := runGit(t, root, "rev-parse", "HEAD"); head != headBefore {
		t.Fatalf("diverged pull moved HEAD: %s -> %s", headBefore, head)
	}
}

func TestPushToLocalBareOriginRunsNoPrePushHook(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("hook fixture uses POSIX scripts")
	}
	s := newTestService(t)
	root := initRepo(t)
	writeFile(t, root, "a.txt", "1\n")
	commitAll(t, root, "init")
	origin := makeOrigin(t, root)

	marker := filepath.Join(root, "pre-push.ran")
	hook := filepath.Join(root, ".git", "hooks", "pre-push")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\ntouch "+marker+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, root, "b.txt", "2\n")
	if _, err := s.Commit(context.Background(), root, CommitRequest{Message: "add b", Paths: []string{"b.txt"}}); err != nil {
		t.Fatal(err)
	}
	res, err := s.Push(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Updated {
		t.Fatalf("push result: %+v", res)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("pre-push hook ran")
	}
	if got := runGit(t, origin, "show", "main:b.txt"); got != "2" {
		t.Fatalf("origin b.txt = %q", got)
	}
}

func TestPushRejectedIsNotForced(t *testing.T) {
	s := newTestService(t)
	root := initRepo(t)
	writeFile(t, root, "a.txt", "1\n")
	commitAll(t, root, "init")
	origin := makeOrigin(t, root)
	other := cloneWork(t, origin)
	writeFile(t, other, "a.txt", "remote\n")
	commitAll(t, other, "remote")
	runGit(t, other, "push", "--quiet", "origin", "main")
	remoteBefore := runGit(t, origin, "rev-parse", "main")

	writeFile(t, root, "b.txt", "local\n")
	if _, err := s.Commit(context.Background(), root, CommitRequest{Message: "local", Paths: []string{"b.txt"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Push(context.Background(), root); err == nil {
		t.Fatal("diverged push should be rejected")
	}
	if got := runGit(t, origin, "rev-parse", "main"); got != remoteBefore {
		t.Fatalf("rejected push changed origin: %s -> %s", remoteBefore, got)
	}
}

func TestRemoteTargetSanitizesURL(t *testing.T) {
	root := initRepo(t)
	writeFile(t, root, "a.txt", "1\n")
	commitAll(t, root, "init")
	token := "plain" + "Secret"
	runGit(t, root, "remote", "add", "origin", "https://user:"+token+"@example.invalid/repo.git")
	runGit(t, root, "config", "branch.main.remote", "origin")
	runGit(t, root, "config", "branch.main.merge", "refs/heads/main")

	s := newTestService(t)
	target, err := s.PullTarget(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(target.URL, token) {
		t.Fatalf("token leaked in display URL: %q", target.URL)
	}
	if target.Remote != "origin" || target.RemoteRef != "refs/heads/main" {
		t.Fatalf("target: %+v", target)
	}
}

func TestPullPushRequireUpstream(t *testing.T) {
	s := newTestService(t)
	root := initRepo(t)
	writeFile(t, root, "a.txt", "1\n")
	commitAll(t, root, "init")
	if _, err := s.Pull(context.Background(), root); !errors.Is(err, ErrNoUpstream) {
		t.Fatalf("pull: want ErrNoUpstream, got %v", err)
	}
	if _, err := s.Push(context.Background(), root); !errors.Is(err, ErrNoUpstream) {
		t.Fatalf("push: want ErrNoUpstream, got %v", err)
	}
}

func TestCancelKillsGitProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell fixture")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "slow-git")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nsleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	s, err := New(Options{GitPath: script})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err = s.run(ctx, dir, runOpts{op: "slow", args: []string{"status"}})
	if !errors.Is(err, ErrCanceled) {
		t.Fatalf("want ErrCanceled, got %v", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("cancellation took too long: %s", elapsed)
	}
}

func TestStatusOnNonRepoAndEmptyRoot(t *testing.T) {
	s := newTestService(t)
	if _, err := s.Status(context.Background(), ""); !errors.Is(err, ErrNotRepository) {
		t.Fatalf("empty root: %v", err)
	}
	if _, err := s.Status(context.Background(), t.TempDir()); err == nil {
		t.Fatal("status outside a repository should fail")
	}
}

func TestSanitizeRemoteURL(t *testing.T) {
	token := "plain" + "Secret"
	cases := map[string]string{
		"https://user:" + token + "@example.com/a.git": "https://***@example.com/a.git",
		"https://example.com/a.git":                    "https://example.com/a.git",
		"git@example.com:a/b.git":                      "git@example.com:a/b.git",
	}
	for in, want := range cases {
		if got := sanitizeRemoteURL(in); got != want {
			t.Errorf("sanitize mismatch: got %q, want %q", got, want)
		}
	}
}

func TestCleanPath(t *testing.T) {
	good := map[string]string{"a.txt": "a.txt", "dir/../b.txt": "b.txt", "./c": "c"}
	for in, want := range good {
		got, err := CleanPath(in)
		if err != nil || got != want {
			t.Errorf("CleanPath(%q) = %q, %v", in, got, err)
		}
	}
	bad := []string{"", "../x", "/abs", ".git/config", ".git"}
	for _, in := range bad {
		if _, err := CleanPath(in); err == nil {
			t.Errorf("CleanPath(%q) should fail", in)
		}
	}
}

func TestOutputLimitIsAnError(t *testing.T) {
	s, err := New(Options{MaxOutputBytes: 32})
	if err != nil {
		t.Skip(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	root := initRepo(t)
	for i := 0; i < 30; i++ {
		writeFile(t, root, fmt.Sprintf("f%02d.txt", i), "x\n")
	}
	runGit(t, root, "add", "-A")
	runGit(t, root, "commit", "--quiet", "-m", "many")
	for i := 0; i < 30; i++ {
		writeFile(t, root, fmt.Sprintf("f%02d.txt", i), "y\n")
	}
	if _, err := s.Status(context.Background(), root); !errors.Is(err, ErrOutputTooLarge) {
		t.Fatalf("want ErrOutputTooLarge, got %v", err)
	}
}
