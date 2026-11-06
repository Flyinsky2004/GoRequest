package desktop

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gorequest/internal/core"
	"gorequest/internal/platform"
)

// drainFor pumps the headless test window's queued UI callbacks for d, matching
// the real app's UI-thread dispatch of watcher notifications.
func drainFor(win *queuedTestWindow, d time.Duration) {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		select {
		case <-win.notifyCh:
		case <-time.After(10 * time.Millisecond):
		}
		win.drainUI()
	}
}

// FIX01 regression: a normal local save (and an in-place rename performed by
// the app itself) must never be misreported as an external three-version
// conflict, and it must never cause the user's unsaved edits to be reloaded
// away.
func TestFIX01LocalSaveAndSelfRenameProduceNoFalseConflict(t *testing.T) {
	t.Cleanup(StopAllFileSync)

	a, ps := openTestProject(t, "FIX01SelfWrite")
	tab := openRequestTabs(t, ps, "Alpha")[0]
	if err := a.InitFileSync(); err != nil {
		t.Fatal(err)
	}

	// 1. Plain local save reflected to disk: repeated edit → save → sync must
	// never raise a conflict.
	for i := 0; i < 4; i++ {
		tab.URL = fmt.Sprintf("https://fix01.example.com/save-%d", i)
		tab.Dirty = true
		if err := a.saveTabSynced(tab); err != nil {
			t.Fatalf("save #%d: %v", i, err)
		}
		if _, err := a.SyncNow(); err != nil {
			t.Fatal(err)
		}
		if c := a.ConflictForTab(tab); c != nil {
			t.Fatalf("false conflict after local save #%d: %+v", i, c)
		}
	}

	// 2. User edits again, then renames the request from the tree. The rename is
	// an app self-write through core.SaveRequest; it does not notify the engine.
	// A later watcher scan must not treat it as an external divergence.
	tab.URL = "https://fix01.example.com/edited-not-saved"
	tab.Dirty = true
	if err := ps.project.RenameRequest(tab.Entry, "Alpha Renamed"); err != nil {
		t.Fatal(err)
	}
	tab.Title = tab.Entry.Request.Name
	tab.Baseline = tab.Entry.BaselineDigest

	if _, err := a.SyncNow(); err != nil {
		t.Fatal(err)
	}
	if c := a.ConflictForTab(tab); c != nil {
		t.Fatalf("self rename while dirty raised false conflict: kind=%s disk=%q", c.ConflictKind, string(c.DiskBytes))
	}
	if tab.URL != "https://fix01.example.com/edited-not-saved" || !tab.Dirty {
		t.Fatalf("unsaved edit was dropped by self-write handling: url=%q dirty=%v", tab.URL, tab.Dirty)
	}
	// The pending edit must remain savable onto the renamed file.
	if err := a.saveTabSynced(tab); err != nil {
		t.Fatalf("save after self rename: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(ps.project.Store.Root, filepath.FromSlash(tab.Entry.RelPath)))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "https://fix01.example.com/edited-not-saved") {
		t.Fatalf("edited URL not persisted after self rename save:\n%s", string(raw))
	}
	if !strings.Contains(string(raw), "Alpha Renamed") {
		t.Fatalf("rename lost after save:\n%s", string(raw))
	}
}

// FIX01 regression: a genuine external modification of a dirty open request
// must still be detected as a conflict, and the conflict must retain all three
// versions (reading baseline / disk / current edit).
func TestFIX01RealExternalModificationStillConflictsWithThreeVersions(t *testing.T) {
	t.Cleanup(StopAllFileSync)

	a, ps := openTestProject(t, "FIX01External")
	tab := openRequestTabs(t, ps, "Alpha")[0]
	reqID := tab.Entry.Request.ID
	if err := a.InitFileSync(); err != nil {
		t.Fatal(err)
	}
	baseBytes, baseDigest, err := ps.project.Store.Read(tab.Entry.RelPath)
	if err != nil {
		t.Fatal(err)
	}
	if baseDigest != tab.Baseline {
		t.Fatalf("baseline mismatch setup")
	}

	tab.URL = "https://local.example.com/edit"
	tab.Dirty = true

	// Genuine external edit: change a different field on disk.
	abs := filepath.Join(ps.project.Store.Root, filepath.FromSlash(tab.Entry.RelPath))
	diskYAML := "schemaVersion: 1\nid: " + reqID + "\nname: Alpha\nprotocol: http\nhttp:\n  method: POST\n  url: \"https://remote.example.com/disk\"\n"
	if err := os.WriteFile(abs, []byte(diskYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := a.SyncNow(); err != nil {
		t.Fatal(err)
	}
	c := a.ConflictForTab(tab)
	if c == nil || c.ConflictKind != core.ConflictModified {
		t.Fatalf("expected modified conflict for genuine external edit, got %+v", c)
	}
	if string(c.BaselineBytes) != string(baseBytes) {
		t.Fatalf("conflict baseline version wrong: %q", string(c.BaselineBytes))
	}
	if string(c.DiskBytes) != diskYAML {
		t.Fatalf("conflict disk version wrong: %q", string(c.DiskBytes))
	}
	if len(c.EditedBytes) == 0 || !strings.Contains(string(c.EditedBytes), "https://local.example.com/edit") {
		t.Fatalf("conflict edited version wrong: %q", string(c.EditedBytes))
	}
	// The user's unsaved edit and the disk file are both untouched.
	if tab.URL != "https://local.example.com/edit" || !tab.Dirty {
		t.Fatal("external conflict must preserve the unsaved edit")
	}
	if raw, _ := os.ReadFile(abs); string(raw) != diskYAML {
		t.Fatal("external disk file must not be overwritten while resolving is pending")
	}
}

// FIX01 regression: resolving a genuine conflict with the user's edited version
// (keep-edited) must persist that edit, never silently fall back to the old
// baseline/disk bytes.
func TestFIX01ResolveKeepEditedDoesNotRevertToOldBaseline(t *testing.T) {
	t.Cleanup(StopAllFileSync)

	a, ps := openTestProject(t, "FIX01Resolve")
	tab := openRequestTabs(t, ps, "Alpha")[0]
	reqID := tab.Entry.Request.ID
	if err := a.InitFileSync(); err != nil {
		t.Fatal(err)
	}

	tab.URL = "https://local.example.com/keep-me"
	tab.Dirty = true
	abs := filepath.Join(ps.project.Store.Root, filepath.FromSlash(tab.Entry.RelPath))
	diskYAML := "schemaVersion: 1\nid: " + reqID + "\nname: Alpha\nprotocol: http\nhttp:\n  method: POST\n  url: \"https://remote.example.com/other\"\n"
	if err := os.WriteFile(abs, []byte(diskYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SyncNow(); err != nil {
		t.Fatal(err)
	}
	if a.ConflictForTab(tab) == nil {
		t.Fatal("expected a conflict before resolving")
	}

	if err := a.ResolveTabConflict(tab, core.ResolutionKeepEdited); err != nil {
		t.Fatalf("resolve keep-edited: %v", err)
	}
	if a.ConflictForTab(tab) != nil || tab.Dirty {
		t.Fatal("conflict should be resolved and tab clean")
	}
	raw, err := os.ReadFile(abs)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "https://local.example.com/keep-me") {
		t.Fatalf("keep-edited did not persist the user's edit (reverted?):\n%s", string(raw))
	}
	if strings.Contains(string(raw), "https://remote.example.com/other") {
		t.Fatalf("keep-edited left the external disk version in place:\n%s", string(raw))
	}
	if tab.URL != "https://local.example.com/keep-me" {
		t.Fatalf("tab URL reverted after resolve: %q", tab.URL)
	}
}

// FIX01 regression: with the real background watcher, editing and saving in a
// loop must never raise a spurious conflict banner.
func TestFIX01RealWatcherRepeatedSaveNoConflict(t *testing.T) {
	t.Cleanup(StopAllFileSync)

	var createdWin *queuedTestWindow
	mgr := &Manager{
		cfg:     newConfigStore(filepath.Join(t.TempDir(), "config.json")),
		secrets: platform.NewEnvSecrets(),
		newWindow: func(s windowSpec) window {
			createdWin = newQueuedTestWindow(s.Title)
			return createdWin
		},
	}
	mgr.cfg.cfg.Language = string(langEnglish)
	if err := mgr.openProject(writeProject(t, "FIX01Watcher")); err != nil {
		t.Fatal(err)
	}
	a := mgr.workspaces[0]
	tab := openRequestTabs(t, a.ps, "Alpha")[0]
	if err := a.StartFileSync(core.WatchOptions{
		PollInterval:   20 * time.Millisecond,
		DebounceWindow: 30 * time.Millisecond,
		MaxBatchWait:   120 * time.Millisecond,
	}); err != nil {
		t.Fatal(err)
	}
	drainFor(createdWin, 200*time.Millisecond)
	for i := 0; i < 5; i++ {
		tab.URL = fmt.Sprintf("https://fix01.example.com/live-%d", i)
		tab.Dirty = true
		if err := a.saveTabSynced(tab); err != nil {
			t.Fatalf("save #%d: %v", i, err)
		}
		drainFor(createdWin, 250*time.Millisecond)
		if c := a.ConflictForTab(tab); c != nil {
			t.Fatalf("watcher raised false conflict after save #%d: %+v", i, c)
		}
	}
}
