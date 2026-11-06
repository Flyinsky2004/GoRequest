package desktop

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/egoist/mygo/ui/editor"

	"gorequest/internal/gitservice"
)

const (
	// gitMaxRows bounds the rendered list; the status command itself is also
	// output-bounded, but a large repository can still exceed a usable rail.
	gitMaxRows = 500
)

// gitController is the per-window Git panel state. It is created by the
// integration wiring for exactly one App and released with that window; no
// state is shared between windows or projects. All methods that touch UI state
// run on the UI thread, while Git commands run in a goroutine that posts its
// result back through the owning window.
type gitController struct {
	app *App
	svc *gitservice.Service
	// svcErr records why the service could not be created (git missing or the
	// isolated hooks directory could not be prepared).
	svcErr error

	loaded  bool
	loading bool
	repo    *gitservice.Repository
	status  *gitservice.Status
	repoErr string

	// selection is the set of repository-relative paths selected for commit.
	selection map[string]bool

	// busy names the running operation ("refresh", "commit", "pull", "push",
	// "diff"); cancel stops it and close aborts it on window teardown.
	busy   string
	cancel context.CancelFunc
	gen    int

	opErr string
	note  string

	// commit modal
	commitOpen      bool
	commitErr       string
	commitMsgEditor *editor.State

	// diff modal
	diffOpen   bool
	diffPath   string
	diffOrig   string
	diffStaged bool
	diff       *gitservice.FileDiff
	diffErr    string
	diffState  *editor.DiffState

	// pull/push confirmation modal
	confirmOpen   bool
	confirmKind   string // "pull" or "push"
	confirmTarget *gitservice.RemoteTarget
	confirmErr    string
}

func newGitController(app *App) *gitController {
	g := &gitController{app: app, selection: map[string]bool{}}
	svc, err := gitservice.New(gitservice.Options{})
	if err != nil {
		g.svcErr = err
		return g
	}
	g.svc = svc
	return g
}

// close cancels running work and removes the isolated hooks directory. It is
// called when the owning window is destroyed.
func (g *gitController) close() {
	if g == nil {
		return
	}
	g.gen++
	if g.cancel != nil {
		g.cancel()
		g.cancel = nil
	}
	if g.svc != nil {
		_ = g.svc.Close()
		g.svc = nil
	}
}

func (g *gitController) msg() *gitMessages {
	if g == nil || g.app == nil {
		return gitMessagesFor(defaultLanguage())
	}
	return g.app.gitMsg()
}

// post runs fn on the owning window's UI thread unless that window is gone.
func (g *gitController) post(fn func()) {
	if g == nil || fn == nil {
		return
	}
	if g.app == nil {
		fn()
		return
	}
	if g.app.closed {
		return
	}
	if g.app.win == nil {
		fn()
		return
	}
	if g.app.win.IsDestroyed() {
		return
	}
	g.app.win.Update(fn)
}

func (g *gitController) root() string {
	if g == nil || g.repo == nil {
		return ""
	}
	return g.repo.Root
}

// begin starts an asynchronous operation with a fresh cancellation context.
// It returns false when the panel is already busy or Git is unavailable.
func (g *gitController) begin(kind string) (context.Context, bool) {
	if g == nil || g.svc == nil || g.busy != "" {
		return nil, false
	}
	ctx, cancel := context.WithCancel(context.Background())
	g.busy = kind
	g.cancel = cancel
	g.opErr = ""
	g.diffErr = ""
	return ctx, true
}

func (g *gitController) finish(ctx context.Context, err error) {
	g.busy = ""
	g.loading = false
	if g.cancel != nil {
		g.cancel = nil
	}
	switch {
	case ctx != nil && ctx.Err() != nil:
		g.opErr = g.msg().Canceled
	case err != nil:
		g.opErr = translate(g.msg().ErrorPrefix, g.describeErr(err))
	}
}

func (g *gitController) describeErr(err error) string {
	m := g.msg()
	switch {
	case err == nil:
		return ""
	case errors.Is(err, gitservice.ErrGitUnavailable):
		return m.Unavailable
	case errors.Is(err, gitservice.ErrNotRepository), errors.Is(err, gitservice.ErrNotWorkTree):
		return m.NotRepo
	case errors.Is(err, gitservice.ErrNoUpstream):
		return m.NoUpstream
	case errors.Is(err, gitservice.ErrDetachedHead):
		return m.Detached
	case errors.Is(err, gitservice.ErrDirtyWorkingTree):
		return translate("%s (%s)", m.PullDirty, err.Error())
	case errors.Is(err, gitservice.ErrDiverged):
		return m.PullDiverged
	case errors.Is(err, gitservice.ErrNoChanges):
		return m.Clean
	case errors.Is(err, gitservice.ErrUnmerged):
		return translate("%s (%s)", m.DiffUnmerged, err.Error())
	case errors.Is(err, gitservice.ErrCanceled):
		return m.Canceled
	case errors.Is(err, gitservice.ErrOutputTooLarge):
		return err.Error()
	case errors.Is(err, gitservice.ErrUnsupportedPath):
		return err.Error()
	}
	return err.Error()
}

// refreshNow performs discovery and status synchronously and applies the
// result. Tests use it; the panel calls asyncRefresh.
func (g *gitController) refreshNow(ctx context.Context) error {
	if g == nil || g.svc == nil {
		g.loaded = true
		return nil
	}
	repo, st, err := g.fetchStatus(ctx)
	g.applyStatus(repo, st, err)
	return err
}

func (g *gitController) fetchStatus(ctx context.Context) (*gitservice.Repository, *gitservice.Status, error) {
	if g == nil || g.svc == nil {
		return nil, nil, gitservice.ErrGitUnavailable
	}
	if g.app == nil || g.app.ps == nil {
		return nil, nil, fmt.Errorf("no active project")
	}
	return g.statusAt(ctx, g.svc, g.app.ps.root)
}

// statusAt performs repository reads with an explicitly captured service and
// start directory, so background work never races with window teardown or a
// project-state replacement.
func (g *gitController) statusAt(ctx context.Context, svc *gitservice.Service, start string) (*gitservice.Repository, *gitservice.Status, error) {
	if svc == nil {
		return nil, nil, gitservice.ErrGitUnavailable
	}
	repo, err := svc.Discover(ctx, start)
	if err != nil {
		return nil, nil, err
	}
	st, err := svc.Status(ctx, repo.Root)
	if err != nil {
		return repo, nil, err
	}
	return repo, st, nil
}

func (g *gitController) applyStatus(repo *gitservice.Repository, st *gitservice.Status, err error) {
	g.loaded = true
	if err != nil {
		g.repoErr = err.Error()
		if repo != nil {
			g.repo = repo
		}
		return
	}
	g.repoErr = ""
	g.repo = repo
	g.status = st
	g.pruneSelection()
}

func (g *gitController) pruneSelection() {
	if g.selection == nil {
		g.selection = map[string]bool{}
		return
	}
	live := map[string]bool{}
	if g.status != nil {
		for _, e := range g.status.Entries {
			live[e.Path] = true
		}
	}
	for p := range g.selection {
		if !live[p] {
			delete(g.selection, p)
		}
	}
}

// ensureLoaded triggers the first read only when the Git view is actually
// shown. Opening a project never calls Git.
func (g *gitController) ensureLoaded() {
	if g == nil || g.loaded || g.loading || g.busy != "" || g.svc == nil {
		return
	}
	g.asyncRefresh()
}

func (g *gitController) asyncRefresh() {
	ctx, ok := g.begin("refresh")
	if !ok {
		return
	}
	g.loading = true
	gen := g.gen
	svc := g.svc
	start := ""
	if g.app != nil && g.app.ps != nil {
		start = g.app.ps.root
	}
	go func() {
		repo, st, err := g.statusAt(ctx, svc, start)
		g.post(func() {
			if gen != g.gen {
				return
			}
			g.applyStatus(repo, st, err)
			if err != nil && !errors.Is(err, gitservice.ErrCanceled) && ctx.Err() == nil {
				g.opErr = translate(g.msg().ErrorPrefix, g.describeErr(err))
			}
			g.finish(ctx, nil)
		})
	}()
}

func (g *gitController) retry() {
	if g == nil {
		return
	}
	if g.svc == nil {
		svc, err := gitservice.New(gitservice.Options{})
		g.svcErr = err
		if err == nil {
			g.svc = svc
		}
	}
	g.loaded = false
	g.repoErr = ""
	g.opErr = ""
	g.ensureLoaded()
}

func (g *gitController) cancelBusy() {
	if g == nil || g.cancel == nil {
		return
	}
	g.cancel()
}

func (g *gitController) selectedPaths() []string {
	if g == nil || g.status == nil {
		return nil
	}
	out := make([]string, 0, len(g.selection))
	seen := make(map[string]bool, len(g.selection))
	add := func(p string) {
		if p != "" && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	for _, e := range g.status.Entries {
		if !g.selection[e.Path] {
			continue
		}
		add(e.Path)
		// A staged rename is one status row covering both paths; committing it
		// without the source path would leave the old file in place.
		if e.OrigPath != "" && e.Staged() {
			add(e.OrigPath)
		}
	}
	sort.Strings(out)
	return out
}

func (g *gitController) selectAll(entries []gitservice.Entry) {
	if g.selection == nil {
		g.selection = map[string]bool{}
	}
	for _, e := range entries {
		if !e.Unmerged {
			g.selection[e.Path] = true
		}
	}
}

func (g *gitController) clearSelection() { g.selection = map[string]bool{} }

// --- commit --------------------------------------------------------------

func (g *gitController) openCommit() {
	if g == nil {
		return
	}
	if len(g.selectedPaths()) == 0 {
		g.opErr = g.msg().SelectFilesHint
		return
	}
	if g.commitMsgEditor == nil || g.commitMsgEditor.Buffer == nil {
		g.commitMsgEditor = &editor.State{Buffer: editor.NewBuffer("")}
	}
	g.commitOpen = true
	g.commitErr = ""
}

func (g *gitController) commitMessage() string {
	if g == nil || g.commitMsgEditor == nil || g.commitMsgEditor.Buffer == nil {
		return ""
	}
	return strings.TrimSpace(g.commitMsgEditor.Buffer.String())
}

// commitSync performs the selective commit and applies its result. It is used
// directly by tests and from the async UI path.
func (g *gitController) commitSync(ctx context.Context) error {
	if g == nil || g.svc == nil || g.repo == nil {
		return fmt.Errorf("git is not available")
	}
	paths := g.selectedPaths()
	if len(paths) == 0 {
		return fmt.Errorf("select at least one file")
	}
	msg := g.commitMessage()
	if msg == "" {
		return fmt.Errorf("commit message is required")
	}
	res, err := g.svc.Commit(ctx, g.repo.Root, gitservice.CommitRequest{Message: msg, Paths: paths})
	if err != nil {
		g.commitErr = g.describeErr(err)
		return err
	}
	g.applyCommitResult(res)
	// Refresh status with the same context, best effort.
	if repo, st, serr := g.fetchStatus(ctx); serr == nil {
		g.applyStatus(repo, st, nil)
	}
	return nil
}

// applyCommitResult updates the UI-thread state after a successful commit.
func (g *gitController) applyCommitResult(res *gitservice.CommitResult) {
	if res == nil {
		return
	}
	g.note = translate(g.msg().CommitDone, shortSHA(res.Commit))
	if res.IndexWarning != "" {
		g.note += " · " + res.IndexWarning
	}
	if g.commitMsgEditor != nil && g.commitMsgEditor.Buffer != nil {
		g.commitMsgEditor.Buffer.Reset("")
	}
	g.commitOpen = false
	g.commitErr = ""
	g.clearSelection()
}

func (g *gitController) asyncCommit() {
	if g == nil || g.svc == nil || g.repo == nil {
		return
	}
	root := g.repo.Root
	paths := g.selectedPaths()
	msg := g.commitMessage()
	if len(paths) == 0 || msg == "" {
		return
	}
	ctx, ok := g.begin("commit")
	if !ok {
		return
	}
	gen := g.gen
	svc := g.svc
	go func() {
		res, err := svc.Commit(ctx, root, gitservice.CommitRequest{Message: msg, Paths: paths})
		var repo *gitservice.Repository
		var st *gitservice.Status
		var stErr error
		if err == nil {
			repo, st, stErr = g.statusAt(ctx, svc, root)
		}
		g.post(func() {
			if gen != g.gen {
				return
			}
			if err == nil {
				g.applyCommitResult(res)
				if stErr == nil {
					g.applyStatus(repo, st, nil)
				}
			} else {
				g.commitErr = g.describeErr(err)
			}
			g.finish(ctx, err)
		})
	}()
}

// --- pull / push ---------------------------------------------------------

// pullBlockReason returns a localized reason when unsaved edits or B01 sync
// conflicts make a pull unsafe. It never saves, discards or resolves anything.
func (g *gitController) pullBlockReason() string {
	if g == nil || g.app == nil || g.app.ps == nil {
		return ""
	}
	ps := g.app.ps
	if ps.hasDirty() {
		return g.msg().PullDirty
	}
	if ctrl := peekFileSync(ps); ctrl != nil && ctrl.engine != nil {
		if len(ctrl.engine.Conflicts()) > 0 {
			return g.msg().PullConflict
		}
	}
	for _, tab := range ps.tabs {
		if g.app.ConflictForTab(tab) != nil {
			return g.msg().PullConflict
		}
	}
	return ""
}

func (g *gitController) openPull() {
	if g == nil {
		return
	}
	g.confirmKind = "pull"
	g.confirmErr = ""
	g.confirmTarget = nil
	g.confirmOpen = true
	if reason := g.pullBlockReason(); reason != "" {
		g.confirmErr = reason
		return
	}
	if g.svc == nil || g.repo == nil {
		g.confirmErr = g.msg().Unavailable
		return
	}
	targetCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	target, err := g.svc.PullTarget(targetCtx, g.repo.Root)
	if err != nil {
		g.confirmErr = g.describeErr(err)
		return
	}
	g.confirmTarget = target
}

func (g *gitController) openPush() {
	if g == nil {
		return
	}
	g.confirmKind = "push"
	g.confirmErr = ""
	g.confirmTarget = nil
	g.confirmOpen = true
	if g.svc == nil || g.repo == nil {
		g.confirmErr = g.msg().Unavailable
		return
	}
	targetCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	target, err := g.svc.PushTarget(targetCtx, g.repo.Root)
	if err != nil {
		g.confirmErr = g.describeErr(err)
		return
	}
	g.confirmTarget = target
}

func (g *gitController) doPull(ctx context.Context) (*gitservice.PullResult, error) {
	if reason := g.pullBlockReason(); reason != "" {
		return nil, errors.New(reason)
	}
	if g.svc == nil || g.repo == nil {
		return nil, gitservice.ErrGitUnavailable
	}
	return g.svc.Pull(ctx, g.repo.Root)
}

func (g *gitController) doPush(ctx context.Context) (*gitservice.PushResult, error) {
	if g.svc == nil || g.repo == nil {
		return nil, gitservice.ErrGitUnavailable
	}
	return g.svc.Push(ctx, g.repo.Root)
}

func (g *gitController) asyncPull() {
	if reason := g.pullBlockReason(); reason != "" {
		g.confirmErr = reason
		return
	}
	root := g.root()
	svc := g.svc
	ctx, ok := g.begin("pull")
	if !ok {
		return
	}
	gen := g.gen
	go func() {
		if svc == nil || root == "" {
			g.post(func() { g.finish(ctx, gitservice.ErrGitUnavailable) })
			return
		}
		res, err := svc.Pull(ctx, root)
		var repo *gitservice.Repository
		var st *gitservice.Status
		var stErr error
		if err == nil {
			repo, st, stErr = g.statusAt(ctx, svc, root)
		}
		g.post(func() {
			if gen != g.gen {
				return
			}
			g.confirmOpen = false
			if err == nil {
				if res.UpToDate {
					g.note = g.msg().PullUpToDate
				} else {
					g.note = translate(g.msg().PullDone, res.Behind)
				}
				if stErr == nil {
					g.applyStatus(repo, st, nil)
				}
				// The worktree changed on disk; refresh project tabs and files
				// through the B01 sync engine. Safe because no dirty buffers or
				// conflicts were allowed before the pull.
				if g.app != nil {
					_, _ = g.app.SyncNow()
				}
			}
			g.finish(ctx, err)
		})
	}()
}

func (g *gitController) asyncPush() {
	root := g.root()
	svc := g.svc
	ctx, ok := g.begin("push")
	if !ok {
		return
	}
	gen := g.gen
	go func() {
		if svc == nil || root == "" {
			g.post(func() { g.finish(ctx, gitservice.ErrGitUnavailable) })
			return
		}
		res, err := svc.Push(ctx, root)
		var repo *gitservice.Repository
		var st *gitservice.Status
		if err == nil {
			repo, st, _ = g.statusAt(ctx, svc, root)
		}
		g.post(func() {
			if gen != g.gen {
				return
			}
			g.confirmOpen = false
			if err == nil {
				if res.Updated {
					g.note = g.msg().PushDone
				} else {
					g.note = g.msg().PushNothing
				}
				if st != nil {
					g.applyStatus(repo, st, nil)
				}
			}
			g.finish(ctx, err)
		})
	}()
}

// --- diff ----------------------------------------------------------------

func (g *gitController) openDiff(path, orig string, staged bool) {
	if g == nil || g.svc == nil {
		return
	}
	g.diffOpen = true
	g.diffPath = path
	g.diffOrig = orig
	g.diffStaged = staged
	g.diff = nil
	g.diffErr = ""
	g.diffState = nil
	if g.busy != "" {
		g.diffErr = translate(g.msg().Busy, g.busyLabel(g.msg()))
		return
	}
	g.asyncDiff()
}

func (g *gitController) asyncDiff() {
	if g.svc == nil || g.repo == nil {
		return
	}
	ctx, ok := g.begin("diff")
	if !ok {
		return
	}
	gen := g.gen
	path, orig, staged := g.diffPath, g.diffOrig, g.diffStaged
	svc := g.svc
	root := g.root()
	go func() {
		diff, err := svc.Diff(ctx, root, gitservice.DiffRequest{Path: path, OrigPath: orig, Staged: staged})
		g.post(func() {
			if gen != g.gen {
				return
			}
			if err == nil {
				g.diff = diff
				g.diffState = buildGitDiffState(diff)
			}
			g.finish(ctx, err)
		})
	}()
}

func buildGitDiffState(diff *gitservice.FileDiff) *editor.DiffState {
	if diff == nil {
		return nil
	}
	return &editor.DiffState{
		Old:             editor.NewBuffer(diff.OldText),
		New:             editor.NewBuffer(diff.NewText),
		Layout:          editor.DiffSplit,
		Editable:        false,
		ExpandUnchanged: true,
	}
}

func shortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}
