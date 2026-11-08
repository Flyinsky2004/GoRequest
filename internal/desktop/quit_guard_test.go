package desktop

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gorequest/internal/update"
)

// handoffRig wires a Manager with fake installer and fake quit that record the
// call order, so no test opens a real installer or quits a real app.
type handoffRig struct {
	m      *Manager
	u      *updateController
	events []string
	cand   *update.Candidate
	dl     *fakeUpdateDownloader
	// openErr, when set, makes the fake installer fail.
	openErr error
}

func newHandoffRig(t *testing.T) *handoffRig {
	t.Helper()
	r := &handoffRig{}
	installed := "0.0.1-dev"
	clock := fixedClock()
	r.cand = testUpdateCandidate(t, "0.0.2-dev")
	r.dl = &fakeUpdateDownloader{downloaded: &update.Downloaded{Path: filepath.Join(t.TempDir(), "app.dmg")}}
	r.m, r.u, _ = newUpdateTestManager(t, &fakeUpdateChecker{}, r.dl, clock.Now, &installed, func(p string) error {
		r.events = append(r.events, "open")
		return r.openErr
	})
	r.m.quitApp = func() { r.events = append(r.events, "quit") }
	return r
}

func (r *handoffRig) openDirty(t *testing.T, n int) []*App {
	t.Helper()
	var apps []*App
	for i := 0; i < n; i++ {
		if err := r.m.openProject(writeProject(t, "P"+string(rune('A'+i)))); err != nil {
			t.Fatal(err)
		}
		app := r.m.workspaces[len(r.m.workspaces)-1]
		if _, err := app.ps.newRequest(); err != nil {
			t.Fatal(err)
		}
		app.ps.activeTab().Dirty = true
		apps = append(apps, app)
	}
	return apps
}

func (r *handoffRig) run() {
	r.u.setHost(r.m.updateHostWindow())
	r.u.downloadAndOpen(context.Background(), r.cand)
}

func (r *handoffRig) order() string { return strings.Join(r.events, ",") }

func TestHandoffCleanOpensThenQuits(t *testing.T) {
	r := newHandoffRig(t)
	r.m.showWelcome("")
	r.run()
	if r.order() != "open,quit" {
		t.Fatalf("order = %q, want open,quit", r.order())
	}
	if !r.m.quitting || r.m.handoff != nil {
		t.Fatalf("quitting=%v handoff=%v", r.m.quitting, r.m.handoff)
	}
	if got := r.u.state.Load().LastResult; got != update.ResultDownloaded {
		t.Fatalf("state = %q", got)
	}
}

func TestHandoffDirtyAsksBeforeOpen(t *testing.T) {
	r := newHandoffRig(t)
	apps := r.openDirty(t, 1)
	r.run()
	if r.order() != "" {
		t.Fatalf("installer opened before the guard resolved: %q", r.order())
	}
	if g := apps[0].closeGuard; g == nil || !g.active {
		t.Fatal("dirty window did not get the save guard")
	}
	if r.u.dialogOpen {
		t.Fatal("update dialog must not stack on the save guard")
	}

	apps[0].resolveCloseGuard(false) // save and continue
	if apps[0].ps.hasDirty() {
		t.Fatal("save left the tab dirty")
	}
	if r.order() != "open,quit" {
		t.Fatalf("order after save = %q", r.order())
	}
}

func TestHandoffDiscardContinues(t *testing.T) {
	r := newHandoffRig(t)
	apps := r.openDirty(t, 1)
	r.run()
	apps[0].resolveCloseGuard(true)
	if r.order() != "open,quit" {
		t.Fatalf("order = %q", r.order())
	}
}

func TestHandoffCancelNeverOpensOrQuits(t *testing.T) {
	r := newHandoffRig(t)
	apps := r.openDirty(t, 1)
	r.run()
	apps[0].cancelCloseGuard()
	r.m.reconcileHandoff() // next frame
	if r.order() != "" || r.m.quitting || r.m.handoff != nil || r.m.pendingQuit {
		t.Fatalf("cancel leaked: order=%q quitting=%v", r.order(), r.m.quitting)
	}
	if !apps[0].ps.hasDirty() {
		t.Fatal("cancel must keep the unsaved edit")
	}
	if !r.u.dialogOpen || r.u.result != updateResultHandoffCanceled {
		t.Fatalf("dialog: open=%v result=%v", r.u.dialogOpen, r.u.result)
	}
}

func TestHandoffSaveFailureBlocksUntilCanceled(t *testing.T) {
	r := newHandoffRig(t)
	apps := r.openDirty(t, 1)
	tab := apps[0].ps.activeTab()
	path := filepath.Join(apps[0].ps.root, ".gorequest", tab.Entry.RelPath)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o755); err != nil { // writes now fail
		t.Fatal(err)
	}
	r.run()
	apps[0].resolveCloseGuard(false)
	if apps[0].closeGuard.errMsg == "" {
		t.Skip("save unexpectedly succeeded on this platform")
	}
	r.m.reconcileHandoff()
	if r.order() != "" || r.m.handoff == nil || !apps[0].closeGuard.active {
		t.Fatalf("failed save must keep the guard and block: order=%q", r.order())
	}
	apps[0].cancelCloseGuard()
	r.m.reconcileHandoff()
	if r.order() != "" || r.m.handoff != nil {
		t.Fatalf("order=%q handoff=%v", r.order(), r.m.handoff)
	}
}

func TestHandoffLaunchFailureDoesNotQuit(t *testing.T) {
	r := newHandoffRig(t)
	r.openErr = errors.New("no opener")
	r.m.showWelcome("")
	r.run()
	if r.order() != "open" || r.m.quitting {
		t.Fatalf("order=%q quitting=%v", r.order(), r.m.quitting)
	}
	if r.u.result != updateResultError || !strings.Contains(r.u.detail, "no opener") {
		t.Fatalf("result=%v detail=%q", r.u.result, r.u.detail)
	}
	if got := r.u.state.Load().LastResult; got == update.ResultDownloaded {
		t.Fatal("failed launch must not be recorded as downloaded/opened")
	}
}

func TestHandoffMultiWindowWalksEachDirtyWindow(t *testing.T) {
	r := newHandoffRig(t)
	apps := r.openDirty(t, 2)
	r.run()
	if !apps[0].closeGuard.active || (apps[1].closeGuard != nil && apps[1].closeGuard.active) {
		t.Fatal("only the first dirty window may prompt")
	}
	apps[0].resolveCloseGuard(false)
	if r.order() != "" {
		t.Fatalf("opened with a second dirty window: %q", r.order())
	}
	if apps[1].closeGuard == nil || !apps[1].closeGuard.active {
		t.Fatal("second dirty window did not prompt")
	}
	apps[1].resolveCloseGuard(true)
	if r.order() != "open,quit" {
		t.Fatalf("order = %q", r.order())
	}
}

func TestHandoffEditAddedDuringDownloadIsCaught(t *testing.T) {
	r := newHandoffRig(t)
	apps := r.openDirty(t, 1)
	apps[0].ps.activeTab().Dirty = false // clean when the download starts
	r.dl.onDownload = func() { apps[0].ps.activeTab().Dirty = true }
	r.run()
	if r.order() != "" {
		t.Fatalf("edit made during download was ignored: %q", r.order())
	}
	if apps[0].closeGuard == nil || !apps[0].closeGuard.active {
		t.Fatal("guard missing for edit made during download")
	}
}

func TestHandoffEditDuringGuardIsRechecked(t *testing.T) {
	r := newHandoffRig(t)
	apps := r.openDirty(t, 2)
	r.run()
	apps[0].resolveCloseGuard(false)
	// The first window gets dirty again while the second guard is up.
	apps[0].ps.activeTab().Dirty = true
	apps[1].resolveCloseGuard(false)
	if r.order() != "" {
		t.Fatalf("re-dirtied window slipped through: %q", r.order())
	}
	if !apps[0].closeGuard.active {
		t.Fatal("re-dirtied window must prompt again")
	}
}

func TestHandoffCanceledDownloadDoesNothing(t *testing.T) {
	r := newHandoffRig(t)
	r.m.showWelcome("")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r.u.setHost(r.m.welcome.win)
	r.u.downloadAndOpen(ctx, r.cand)
	if r.order() != "" {
		t.Fatalf("canceled download opened installer: %q", r.order())
	}
}

func TestHandoffUserQuitDuringGuardDropsInstaller(t *testing.T) {
	r := newHandoffRig(t)
	apps := r.openDirty(t, 1)
	r.run()
	r.m.abandonHandoff() // what OnBeforeQuit does first
	apps[0].resolveCloseGuard(true)
	if strings.Contains(r.order(), "open") {
		t.Fatalf("installer opened behind a user quit: %q", r.order())
	}
}
