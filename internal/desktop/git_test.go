package desktop

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"gorequest/internal/gitservice"
)

func TestGitMessagesCoverEveryField(t *testing.T) {
	typ := reflect.TypeOf(gitMessages{})
	zh := reflect.ValueOf(gitMessagesZh)
	en := reflect.ValueOf(gitMessagesEn)
	for i := 0; i < typ.NumField(); i++ {
		name := typ.Field(i).Name
		if zh.Field(i).String() == "" {
			t.Errorf("gitMessagesZh.%s is empty", name)
		}
		if en.Field(i).String() == "" {
			t.Errorf("gitMessagesEn.%s is empty", name)
		}
	}
}

func gitc(t *testing.T, dir string, args ...string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git executable not available")
	}
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=T", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=T", "GIT_COMMITTER_EMAIL=t@example.com")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// gitProject opens a normal GoRequest test project and makes its folder a Git
// repository containing the generated .gorequest files.
func gitProject(t *testing.T, name string) (*App, *projectState, *gitController) {
	t.Helper()
	a, ps := openTestProject(t, name)
	gitc(t, ps.root, "init", "--quiet", "-b", "main")
	gitc(t, ps.root, "config", "user.name", "T")
	gitc(t, ps.root, "config", "user.email", "t@example.com")
	gitc(t, ps.root, "add", "-A")
	gitc(t, ps.root, "commit", "--quiet", "-m", "init")
	g := newGitController(a)
	t.Cleanup(g.close)
	if g.svc == nil {
		t.Skipf("git service unavailable: %v", g.svcErr)
	}
	if err := g.refreshNow(context.Background()); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	return a, ps, g
}

func renderGitPanel(t *testing.T, g *gitController) *ui.Tester {
	t.Helper()
	tt := ui.NewTester(func(c *ui.Context) { g.panel(c, c.Theme()) }, 360, 820)
	tt.Frame()
	return tt
}

func TestGitPanelGitUnavailable(t *testing.T) {
	a, _ := openTestProject(t, "Demo")
	g := &gitController{app: a, selection: map[string]bool{}, svcErr: gitservice.ErrGitUnavailable}
	tt := renderGitPanel(t, g)
	if !tt.HasText("Git") || !tt.HasText(g.msg().Retry) {
		t.Fatalf("unavailable panel missing expected text: %v", tt.Texts())
	}
	if !tt.HasText(g.msg().Unavailable) {
		t.Fatalf("missing unavailable explanation: %v", tt.Texts())
	}
}

func TestGitPanelNotRepository(t *testing.T) {
	a, _ := openTestProject(t, "Demo")
	g := newGitController(a)
	t.Cleanup(g.close)
	if err := g.refreshNow(context.Background()); err != nil {
		// A non-repository is recorded as a panel error, not a hard failure.
		t.Logf("refresh: %v", err)
	}
	if g.repoErr == "" {
		t.Fatal("expected non-repository diagnostic")
	}
	tt := renderGitPanel(t, g)
	if !tt.HasText(g.msg().NotRepo) || !tt.HasText(g.msg().NotRepoHint) {
		t.Fatalf("not-repository panel text missing: %v", tt.Texts())
	}
	// HTTP editing is not affected: the project from App remains loaded.
	if a.ps == nil || a.ps.project == nil {
		t.Fatal("project lost after Git failure")
	}
}

func TestGitPanelStatusAndSelection(t *testing.T) {
	_, ps, g := gitProject(t, "Demo")
	if err := os.WriteFile(filepath.Join(ps.root, ".gorequest", "git-work.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := g.refreshNow(context.Background()); err != nil {
		t.Fatal(err)
	}
	if g.status == nil || len(g.status.Entries) == 0 {
		t.Fatalf("status entries: %+v", g.status)
	}
	found := false
	for _, e := range g.status.Entries {
		if e.Path == ".gorequest/git-work.txt" {
			found = true
			g.selection[e.Path] = true
		}
	}
	if !found {
		t.Fatalf("untracked file not reported: %+v", g.status.Entries)
	}
	if paths := g.selectedPaths(); len(paths) != 1 || paths[0] != ".gorequest/git-work.txt" {
		t.Fatalf("selected paths: %v", paths)
	}
	tt := renderGitPanel(t, g)
	if !tt.HasText(".gorequest/git-work.txt") {
		t.Fatalf("file row missing: %v", tt.Texts())
	}
	if !tt.HasText("main") {
		t.Fatalf("branch missing: %v", tt.Texts())
	}
	// Removing the file from status prunes the stale selection.
	g.status.Entries = nil
	g.pruneSelection()
	if len(g.selectedPaths()) != 0 {
		t.Fatal("stale selection was not pruned")
	}
}

func TestGitCommitControllerSelectsOnlyChosenFiles(t *testing.T) {
	_, ps, g := gitProject(t, "Demo")
	requestFile := "config.yaml"
	// Ensure two changed files exist under the repository.
	if err := os.WriteFile(filepath.Join(ps.root, requestFile), []byte("a: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ps.root, "other.yaml"), []byte("b: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := g.refreshNow(context.Background()); err != nil {
		t.Fatal(err)
	}
	g.selection = map[string]bool{requestFile: true}
	g.commitMsgEditor = &editor.State{Buffer: editor.NewBuffer("only config")}
	if err := g.commitSync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(g.note, "Created commit") {
		t.Fatalf("commit note missing: %q", g.note)
	}
	if len(g.selectedPaths()) != 0 {
		t.Fatal("selection not cleared after commit")
	}
	names := gitc(t, ps.root, "show", "--name-only", "--format=", "HEAD")
	if strings.Contains(names, "other.yaml") || !strings.Contains(names, requestFile) {
		t.Fatalf("commit names = %q", names)
	}
	if out := gitc(t, ps.root, "status", "--porcelain"); !strings.Contains(out, "other.yaml") {
		t.Fatalf("unselected file should remain changed: %q", out)
	}
}

func TestGitStagedRenameCommitsBothPaths(t *testing.T) {
	_, ps, g := gitProject(t, "Demo")
	oldPath := ".gorequest/rename-old.txt"
	newPath := ".gorequest/rename-new.txt"
	if err := os.WriteFile(filepath.Join(ps.root, oldPath), []byte("content\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitc(t, ps.root, "add", "-A")
	gitc(t, ps.root, "commit", "--quiet", "-m", "add old")
	gitc(t, ps.root, "mv", oldPath, newPath)
	if err := g.refreshNow(context.Background()); err != nil {
		t.Fatal(err)
	}
	var rename gitservice.Entry
	for _, e := range g.status.Entries {
		if e.Path == newPath {
			rename = e
		}
	}
	if rename.OrigPath != oldPath || !rename.Staged() {
		t.Fatalf("rename entry: %+v", rename)
	}
	g.selection = map[string]bool{newPath: true}
	paths := g.selectedPaths()
	if len(paths) != 2 || paths[0] != newPath || paths[1] != oldPath {
		t.Fatalf("rename selection paths = %v", paths)
	}
	g.commitMsgEditor = &editor.State{Buffer: editor.NewBuffer("rename")}
	if err := g.commitSync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if out := gitc(t, ps.root, "ls-tree", "-r", "--name-only", "HEAD"); strings.Contains(out, "rename-old.txt") || !strings.Contains(out, "rename-new.txt") {
		t.Fatalf("rename not committed as expected: %q", out)
	}
}

func TestGitPullBlockedByDirtyBuffers(t *testing.T) {
	_, ps, g := gitProject(t, "Demo")
	tabs := openRequestTabs(t, ps, "Dirty")
	tabs[0].Dirty = true

	reason := g.pullBlockReason()
	if reason != g.msg().PullDirty {
		t.Fatalf("block reason = %q", reason)
	}
	g.openPull()
	if !g.confirmOpen || g.confirmErr != g.msg().PullDirty {
		t.Fatalf("pull confirmation should carry the dirty-buffer error: open=%v err=%q", g.confirmOpen, g.confirmErr)
	}
	if _, err := g.doPull(context.Background()); err == nil {
		t.Fatal("doPull must refuse dirty buffers")
	}
}

func TestGitDiffStateBuildsNativePair(t *testing.T) {
	_, ps, g := gitProject(t, "Demo")
	if err := os.WriteFile(filepath.Join(ps.root, ".gorequest", "diff.txt"), []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	diff, err := g.svc.Diff(context.Background(), g.repo.Root, gitservice.DiffRequest{Path: ".gorequest/diff.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if diff.OldExists || !diff.NewExists {
		t.Fatalf("diff sides: %+v", diff)
	}
	ds := buildGitDiffState(diff)
	if ds == nil || ds.Old == nil || ds.New == nil {
		t.Fatalf("diff state: %+v", ds)
	}
	if !strings.Contains(ds.New.String(), "new") {
		t.Fatalf("new side = %q", ds.New.String())
	}
	if ds.Editable {
		t.Fatal("diff must be read-only")
	}
}

func TestGitModalsRender(t *testing.T) {
	_, ps, g := gitProject(t, "Demo")
	if err := os.WriteFile(filepath.Join(ps.root, ".gorequest", "mod.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := g.refreshNow(context.Background()); err != nil {
		t.Fatal(err)
	}
	g.selection[".gorequest/mod.txt"] = true
	g.commitMsgEditor = &editor.State{Buffer: editor.NewBuffer("draft")}
	g.commitOpen = true

	g.diffOpen = true
	g.diffPath = ".gorequest/mod.txt"
	g.diff = &gitservice.FileDiff{Path: ".gorequest/mod.txt", NewText: "x\n", NewExists: true}
	g.diffState = buildGitDiffState(g.diff)

	g.confirmOpen = true
	g.confirmKind = "push"
	g.confirmTarget = &gitservice.RemoteTarget{Branch: "main", Remote: "origin", RemoteRef: "refs/heads/main", URL: "https://example.invalid/repo"}

	tt := ui.NewTester(func(c *ui.Context) { g.modals(c, c.Theme()) }, 1200, 820)
	tt.Frame()
	for _, want := range []string{g.msg().CommitTitle, g.msg().CommitNote, g.msg().PushTitle, g.diffPath} {
		if want == "" {
			continue
		}
		if !tt.HasText(want) {
			t.Fatalf("modal text %q missing: %v", want, tt.Texts())
		}
	}
	if tt.HasText(g.msg().Canceled) {
		t.Fatal("unexpected canceled text")
	}
}

func TestGitControllerCancelState(t *testing.T) {
	_, ps, g := gitProject(t, "Demo")
	ctx, ok := g.begin("refresh")
	if !ok || g.busy != "refresh" {
		t.Fatalf("begin failed: ok=%v busy=%q", ok, g.busy)
	}
	_ = ps
	g.cancelBusy()
	<-ctx.Done()
	g.finish(ctx, context.Canceled)
	if g.busy != "" {
		t.Fatalf("busy not cleared: %q", g.busy)
	}
	if g.opErr != g.msg().Canceled {
		t.Fatalf("cancel message = %q", g.opErr)
	}
}
