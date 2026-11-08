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
	if apps[0].ps.hasDirty() {
		t.Fatal("successful handoff after discard should clear dirty")
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

func TestHandoffCancelDoesNotLaunchOnSubsequentNormalQuit(t *testing.T) {
	r := newHandoffRig(t)
	apps := r.openDirty(t, 1)
	r.run()
	apps[0].cancelCloseGuard()
	// Even before a UI frame calls reconcileHandoff, a normal quit must not
	// mistake the canceled handoff for an approved update launch.
	r.m.promptQuitGuard()
	if !apps[0].closeGuard.active {
		t.Fatal("normal quit after canceled update must still guard dirty window")
	}
	apps[0].resolveCloseGuard(true)
	if r.order() != "quit" {
		t.Fatalf("order = %q, want quit (installer must never launch on normal quit)", r.order())
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

func TestHandoffDiscardThenLaunchFailurePreservesDirty(t *testing.T) {
	for _, viaDirectEntry := range []bool{false, true} {
		name := "compat_resolveCloseGuard"
		if viaDirectEntry {
			name = "patched_tryResolveHandoffGuard"
		}
		t.Run(name, func(t *testing.T) {
			r := newHandoffRig(t)
			r.openErr = errors.New("installer launch failed")
			apps := r.openDirty(t, 1)
			reqTab := apps[0].ps.activeTab()
			reqTab.URL = "https://example.com/unsaved-draft"
			reqTab.BodyText = `{"draft":true}`

			// Also add a dirty doc tab and a dirty environment editor draft.
			if _, err := apps[0].ps.newDoc(); err != nil {
				t.Fatal(err)
			}
			docTab := apps[0].ps.activeTab()
			docTab.DocEditor.Buffer.Set("# Unsaved Doc Draft\n")
			docTab.Dirty = true

			apps[0].openNewEnvironment()
			apps[0].ps.envUI.idText = "staging-draft"

			r.run()
			if !apps[0].closeGuard.active {
				t.Fatal("expected close guard on dirty window")
			}
			if viaDirectEntry {
				if !r.m.tryResolveHandoffGuard(apps[0], true) {
					t.Fatal("tryResolveHandoffGuard returned false")
				}
			} else {
				apps[0].resolveCloseGuard(true)
			}

			if r.order() != "open" || r.m.quitting {
				t.Fatalf("order=%q quitting=%v, want open without quit", r.order(), r.m.quitting)
			}
			if !apps[0].ps.hasDirty() || !reqTab.Dirty || !docTab.Dirty {
				t.Fatalf("dirty flags lost after installer launch failure: hasDirty=%v req=%v doc=%v",
					apps[0].ps.hasDirty(), reqTab.Dirty, docTab.Dirty)
			}
			if reqTab.URL != "https://example.com/unsaved-draft" || reqTab.BodyText != `{"draft":true}` {
				t.Fatalf("request draft lost: url=%q body=%q", reqTab.URL, reqTab.BodyText)
			}
			if got := docTab.DocEditor.Buffer.String(); got != "# Unsaved Doc Draft\n" {
				t.Fatalf("doc draft lost: %q", got)
			}
			if e := apps[0].ps.envUI; e == nil || !e.open || !e.dirty() || e.idText != "staging-draft" {
				t.Fatalf("env draft lost: %+v", e)
			}

			// A subsequent normal quit must still prompt the save guard.
			r.m.promptQuitGuard()
			if !apps[0].closeGuard.active {
				t.Fatal("subsequent normal quit did not prompt save guard after failed launch")
			}
		})
	}
}

func TestHandoffFirstWindowDiscardSecondWindowCancelPreservesDirty(t *testing.T) {
	for _, viaDirectEntry := range []bool{false, true} {
		name := "compat_resolveCloseGuard"
		if viaDirectEntry {
			name = "patched_tryResolveHandoffGuard"
		}
		t.Run(name, func(t *testing.T) {
			r := newHandoffRig(t)
			apps := r.openDirty(t, 2)
			tab0 := apps[0].ps.activeTab()
			tab0.URL = "https://example.com/window1-unsaved"
			apps[0].openNewEnvironment()
			apps[0].ps.envUI.idText = "win1-env"

			tab1 := apps[1].ps.activeTab()
			tab1.URL = "https://example.com/window2-unsaved"

			r.run()
			if !apps[0].closeGuard.active {
				t.Fatal("window 1 should prompt first")
			}
			// Window 1 chooses Discard.
			if viaDirectEntry {
				if !r.m.tryResolveHandoffGuard(apps[0], true) {
					t.Fatal("tryResolveHandoffGuard returned false")
				}
			} else {
				apps[0].resolveCloseGuard(true)
			}
			if !apps[0].ps.hasDirty() || !tab0.Dirty {
				t.Fatal("window 1 dirty flag must stay set until handoff actually succeeds")
			}
			if !apps[1].closeGuard.active {
				t.Fatal("window 2 should prompt next")
			}

			// Window 2 chooses Cancel.
			apps[1].cancelCloseGuard()
			r.m.reconcileHandoff()

			if r.order() != "" || r.m.quitting || r.m.handoff != nil {
				t.Fatalf("canceled handoff leaked: order=%q quitting=%v handoff=%v",
					r.order(), r.m.quitting, r.m.handoff)
			}
			if !apps[0].ps.hasDirty() || !tab0.Dirty || tab0.URL != "https://example.com/window1-unsaved" {
				t.Fatalf("window 1 dirty draft lost after window 2 canceled: hasDirty=%v tabDirty=%v url=%q",
					apps[0].ps.hasDirty(), tab0.Dirty, tab0.URL)
			}
			if e := apps[0].ps.envUI; e == nil || !e.open || !e.dirty() || e.idText != "win1-env" {
				t.Fatalf("window 1 env draft lost after window 2 canceled: %+v", e)
			}
			if !apps[1].ps.hasDirty() || !tab1.Dirty {
				t.Fatal("window 2 dirty draft must remain dirty after cancel")
			}

			// Subsequent normal quit must still guard window 1 first and never launch installer.
			r.m.promptQuitGuard()
			if !apps[0].closeGuard.active {
				t.Fatal("window 1 did not prompt on subsequent normal quit")
			}
		})
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

func TestHandoffDiscardThenEditedDuringGuardIsRechecked(t *testing.T) {
	r := newHandoffRig(t)
	apps := r.openDirty(t, 2)
	apps[0].ps.activeTab().URL = "https://example.com/v1"
	r.run()
	apps[0].resolveCloseGuard(true) // discard v1 on window 1
	if !apps[1].closeGuard.active {
		t.Fatal("window 2 should prompt")
	}
	// While window 2's guard is open, window 1's draft changes to v2.
	apps[0].ps.activeTab().URL = "https://example.com/v2"
	apps[1].resolveCloseGuard(true)
	if r.order() != "" {
		t.Fatalf("modified window 1 slipped through without re-prompting: %q", r.order())
	}
	if !apps[0].closeGuard.active {
		t.Fatal("window 1 must prompt again after its draft changed")
	}
	apps[0].resolveCloseGuard(true)
	if r.order() != "open,quit" {
		t.Fatalf("order after re-approval = %q, want open,quit", r.order())
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

func TestReviewHandoffDiscardThenCancelKeepsDirty(t *testing.T) {
	r := newHandoffRig(t)
	apps := r.openDirty(t, 2)
	r.run()
	apps[0].resolveCloseGuard(true)
	apps[1].cancelCloseGuard()
	r.m.reconcileHandoff()
	if !apps[0].ps.hasDirty() {
		t.Fatal("first window lost its dirty flag after second window canceled")
	}
	if r.order() != "" {
		t.Fatal(r.order())
	}
}

func TestReviewHandoffDiscardThenLaunchFailureKeepsDirty(t *testing.T) {
	r := newHandoffRig(t)
	apps := r.openDirty(t, 1)
	r.openErr = errors.New("installer unavailable")
	r.run()
	apps[0].resolveCloseGuard(true)
	if !apps[0].ps.hasDirty() {
		t.Fatal("discard approval cleared dirty flag although installer failed")
	}
	if r.order() != "open" {
		t.Fatal(r.order())
	}
}
