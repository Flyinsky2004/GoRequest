package desktop

import "testing"

func pendingVisualDoc(t *testing.T, app *App) *Tab {
	t.Helper()
	if _, err := app.ps.newDoc(); err != nil {
		t.Fatal(err)
	}
	tab := app.ps.activeTab()
	tab.DocEditor.Buffer.Set("# Pending visual edit\n")
	tab.Dirty = false
	tab.flushDocEdit = func() { tab.Dirty = tab.DocEditor.Buffer.Modified() }
	return tab
}

func TestQuitDiscardDocumentDoesNotReopenGuard(t *testing.T) {
	r := newHandoffRig(t)
	app := r.openDirty(t, 1)[0]
	app.ps.activeTab().Dirty = false
	tab := pendingVisualDoc(t, app)
	r.m.pendingQuit = true
	r.m.promptQuitGuard()
	app.resolveCloseGuard(true)
	if r.order() != "quit" || app.closeGuard.active {
		t.Fatalf("discard repeated the guard: events=%q active=%v", r.order(), app.closeGuard.active)
	}
	if tab.DocEditor.Buffer.Modified() {
		t.Fatal("accepted discard must settle the document's modified marker")
	}
}

func TestQuitAndWindowCloseFlushPendingVisualDocument(t *testing.T) {
	for _, windowClose := range []bool{false, true} {
		r := newHandoffRig(t)
		app := r.openDirty(t, 1)[0]
		app.ps.activeTab().Dirty = false
		pendingVisualDoc(t, app)
		if windowClose {
			app.win.Close()
			if app.win.IsDestroyed() || app.closeGuard == nil || !app.closeGuard.active {
				t.Fatal("window closed without asking about the pending document edit")
			}
		} else if !r.m.anyDirty() {
			t.Fatal("application quit missed the pending document edit")
		}
	}
}

func TestWindowClosePreservesActiveUpdateGuard(t *testing.T) {
	r := newHandoffRig(t)
	app := r.openDirty(t, 1)[0]
	r.run()
	app.win.Close()
	if app.closeGuard == nil || !app.closeGuard.active || !app.closeGuard.quit {
		t.Fatal("window close replaced the update handoff guard")
	}
	app.resolveCloseGuard(true)
	if r.order() != "open,quit" {
		t.Fatalf("update guard no longer continued the handoff: %q", r.order())
	}
}
