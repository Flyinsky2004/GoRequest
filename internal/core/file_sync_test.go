package core

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func writeRawProjectFile(t *testing.T, gqDir, rel, content string) {
	t.Helper()
	abs := filepath.Join(gqDir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", rel, err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

func TestOpenAndSyncNeverMutatesDisk(t *testing.T) {
	dir, p := mustNewProject(t, "ImmutableOnOpen")
	gq := p.Dir

	// Write a custom-formatted request with CRLF, extra spacing, and comments.
	rawReq := "# Team comment\r\nschemaVersion: 1\r\nid: auth.login\r\nname: Login\r\nhttp:\r\n  method: POST\r\n  url: \"{{baseUrl}}/login\"\r\n"
	reqRel := "collections/auth/login.request.yaml"
	writeRawProjectFile(t, gq, reqRel, rawReq)
	past := time.Now().Add(-10 * time.Minute).Truncate(time.Second)
	reqAbs := filepath.Join(gq, filepath.FromSlash(reqRel))
	if err := os.Chtimes(reqAbs, past, past); err != nil {
		t.Fatal(err)
	}

	p2, err := OpenProject(dir)
	if err != nil {
		t.Fatalf("OpenProject: %v", err)
	}
	eng, err := p2.NewSyncEngine(SyncOptions{})
	if err != nil {
		t.Fatalf("NewSyncEngine: %v", err)
	}
	rep, err := eng.ScanAndSync()
	if err != nil {
		t.Fatalf("ScanAndSync: %v", err)
	}
	if len(rep.Events) != 0 {
		t.Fatalf("expected 0 events on untouched open, got %+v", rep.Events)
	}

	w, err := StartWatcher(context.Background(), p2.Store, WatchOptions{
		PollInterval:   15 * time.Millisecond,
		DebounceWindow: 10 * time.Millisecond,
	}, func() {})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(45 * time.Millisecond)
	_ = w.Close()

	afterBytes, err := os.ReadFile(reqAbs)
	if err != nil {
		t.Fatal(err)
	}
	if string(afterBytes) != rawReq {
		t.Fatalf("open/sync/watch rewrote request file:\ngot:  %q\nwant: %q", string(afterBytes), rawReq)
	}
	fi, err := os.Stat(reqAbs)
	if err != nil {
		t.Fatal(err)
	}
	if !fi.ModTime().Equal(past) {
		t.Fatalf("open/sync/watch changed mtime: got %v, want %v", fi.ModTime(), past)
	}
}

func TestExternalCleanModifyRenameDeleteAndCreate(t *testing.T) {
	_, p := mustNewProject(t, "SyncClean")
	gq := p.Dir

	if _, err := p.NewFolder("", "Users"); err != nil {
		t.Fatal(err)
	}
	entry, err := p.NewRequest("users", "Get User")
	if err != nil {
		t.Fatal(err)
	}
	origPtr := entry
	reqID := entry.Request.ID

	var cleanBuf OpenBuffer
	eng, err := p.NewSyncEngine(SyncOptions{
		OpenBuffersFn: func() []OpenBuffer {
			if cleanBuf.BufferID == "" {
				return nil
			}
			return []OpenBuffer{cleanBuf}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	cleanBuf = OpenBuffer{
		BufferID:       "req:" + reqID,
		EntityKind:     EntityRequest,
		EntityID:       reqID,
		RelPath:        entry.RelPath,
		Dirty:          false,
		BaselineDigest: entry.BaselineDigest,
	}

	// 1. External Modify of clean open request + external Create of new doc.
	modifiedYAML := "schemaVersion: 1\nid: " + reqID + "\nname: Fetch User V2\nprotocol: http\nhttp:\n  method: POST\n  url: \"{{baseUrl}}/v2/users\"\n"
	writeRawProjectFile(t, gq, entry.RelPath, modifiedYAML)
	writeRawProjectFile(t, gq, "docs/guide.md", "# API Guide\n\nWelcome.\n")

	rep, err := eng.ScanAndSync()
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Events) != 2 {
		t.Fatalf("expected 2 events (modify + create), got %+v", rep.Events)
	}
	if p.Requests[reqID] != origPtr {
		t.Fatal("clean refresh must preserve existing *RequestEntry pointer for open tabs")
	}
	if entry.Request.Name != "Fetch User V2" || entry.Request.HTTP.Method != "POST" || entry.Request.HTTP.URL != "{{baseUrl}}/v2/users" {
		t.Fatalf("request not refreshed in memory: %+v / %+v", entry.Request, entry.Request.HTTP)
	}
	if len(rep.BufferActions) != 1 || rep.BufferActions[0].Action != BufferActionRefreshClean {
		t.Fatalf("expected BufferActionRefreshClean, got %+v", rep.BufferActions)
	}
	cleanBuf.BaselineDigest = entry.BaselineDigest

	// 2. External Folder + File Rename (users -> accounts/fetch-user.request.yaml)
	// plus collection.yaml with inherited variable!
	writeRawProjectFile(t, gq, "collections/accounts/collection.yaml", "schemaVersion: 1\nid: accounts\nname: Accounts\nvariables:\n  team: billing\n")
	newRel := "collections/accounts/fetch-user.request.yaml"
	writeRawProjectFile(t, gq, newRel, modifiedYAML)
	if err := os.RemoveAll(filepath.Join(gq, "collections", "users")); err != nil {
		t.Fatal(err)
	}

	rep, err = eng.ScanAndSync()
	if err != nil {
		t.Fatal(err)
	}
	var sawReqRename bool
	for _, ev := range rep.Events {
		if ev.Kind == SyncRenamed && ev.EntityKind == EntityRequest && ev.EntityID == reqID {
			sawReqRename = true
			if ev.OldPath != "collections/users/get-user.request.yaml" || ev.Path != newRel || ev.ContentChanged {
				t.Fatalf("unexpected rename event: %+v", ev)
			}
		}
	}
	if !sawReqRename {
		t.Fatalf("expected request SyncRenamed, got %+v", rep.Events)
	}
	if entry.RelPath != newRel {
		t.Fatalf("entry.RelPath = %q, want %q", entry.RelPath, newRel)
	}
	if entry.CollectionVars["team"] != "billing" {
		t.Fatalf("inherited collection var not updated after rename: %+v", entry.CollectionVars)
	}
	if len(rep.BufferActions) != 1 || rep.BufferActions[0].Action != BufferActionFollowRename || rep.BufferActions[0].NewPath != newRel {
		t.Fatalf("expected BufferActionFollowRename to %s, got %+v", newRel, rep.BufferActions)
	}
	cleanBuf.RelPath = newRel

	// 3. External Rename+Edit of Markdown doc (docs/guide.md -> docs/handbook.md).
	if err := os.Remove(filepath.Join(gq, "docs", "guide.md")); err != nil {
		t.Fatal(err)
	}
	writeRawProjectFile(t, gq, "docs/handbook.md", "# API Guide\n\nWelcome.\nUpdated section.\n")
	rep, err = eng.ScanAndSync()
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Events) != 1 || rep.Events[0].Kind != SyncRenamed || rep.Events[0].OldPath != "docs/guide.md" || rep.Events[0].Path != "docs/handbook.md" || !rep.Events[0].ContentChanged {
		t.Fatalf("expected doc rename+edit event, got %+v", rep.Events)
	}

	// 4. External Delete of clean open request.
	if err := os.Remove(filepath.Join(gq, filepath.FromSlash(newRel))); err != nil {
		t.Fatal(err)
	}
	rep, err = eng.ScanAndSync()
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := p.Requests[reqID]; exists {
		t.Fatal("deleted request should be removed from Project.Requests")
	}
	if len(rep.BufferActions) != 1 || rep.BufferActions[0].Action != BufferActionCloseDeleted {
		t.Fatalf("expected BufferActionCloseDeleted, got %+v", rep.BufferActions)
	}
	if p.Store.Exists(newRel) {
		t.Fatal("clean delete must never resurrect deleted file")
	}
}

func TestDirtyBufferThreeVersionModifyMergeSaveCopyAndRace(t *testing.T) {
	_, p := mustNewProject(t, "DirtyThreeWay")
	gq := p.Dir

	entry, err := p.NewRequest("", "Orders")
	if err != nil {
		t.Fatal(err)
	}
	reqID := entry.Request.ID
	baseBytes, baseDigest, err := p.Store.Read(entry.RelPath)
	if err != nil {
		t.Fatal(err)
	}

	editedYAML := "schemaVersion: 1\nid: " + reqID + "\nname: Orders Local Edit\nprotocol: http\nhttp:\n  method: GET\n  url: \"{{baseUrl}}/orders?local=1\"\n"
	bufID := "req:" + reqID

	eng, err := p.NewSyncEngine(SyncOptions{
		OpenBuffersFn: func() []OpenBuffer {
			return []OpenBuffer{{
				BufferID:       bufID,
				EntityKind:     EntityRequest,
				EntityID:       reqID,
				RelPath:        entry.RelPath,
				Dirty:          true,
				BaselineDigest: baseDigest,
				EditedBytes:    func() ([]byte, error) { return []byte(editedYAML), nil },
			}}
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	// External modification on disk.
	diskYAML1 := "schemaVersion: 1\nid: " + reqID + "\nname: Orders Disk V1\nprotocol: http\nhttp:\n  method: POST\n  url: \"{{baseUrl}}/orders/disk1\"\n"
	writeRawProjectFile(t, gq, entry.RelPath, diskYAML1)

	rep, err := eng.ScanAndSync()
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Conflicts) != 1 {
		t.Fatalf("expected 1 conflict, got %+v", rep.Conflicts)
	}
	c := eng.ConflictFor(bufID)
	if c == nil || c.ConflictKind != ConflictModified || !c.DiskExists {
		t.Fatalf("bad conflict: %+v", c)
	}
	if string(c.BaselineBytes) != string(baseBytes) {
		t.Fatalf("baseline bytes mismatch:\ngot:  %s\nwant: %s", c.BaselineBytes, baseBytes)
	}
	if string(c.DiskBytes) != diskYAML1 {
		t.Fatalf("disk bytes mismatch:\ngot:  %s\nwant: %s", c.DiskBytes, diskYAML1)
	}
	if string(c.EditedBytes) != editedYAML {
		t.Fatalf("edited bytes mismatch:\ngot:  %s\nwant: %s", c.EditedBytes, editedYAML)
	}
	if eng.CanAutoSave(bufID, entry.RelPath, baseDigest) {
		t.Fatal("CanAutoSave must be false when buffer has a conflict")
	}

	// Inject a SECOND external modification before user clicks KeepEdited:
	// ResolveConflict must catch the disk digest mismatch, NOT overwrite diskYAML2,
	// and refresh c.DiskBytes to diskYAML2 while keeping BaselineBytes & EditedBytes!
	diskYAML2 := "schemaVersion: 1\nid: " + reqID + "\nname: Orders Disk V2\nprotocol: http\nhttp:\n  method: PUT\n  url: \"{{baseUrl}}/orders/disk2\"\n"
	writeRawProjectFile(t, gq, entry.RelPath, diskYAML2)

	if _, err := eng.ResolveConflict(bufID, ResolutionKeepEdited, nil); !IsConflict(err) {
		t.Fatalf("expected *ConflictError on unseen concurrent disk edit, got %v", err)
	}
	onDisk, _, _ := p.Store.Read(entry.RelPath)
	if string(onDisk) != diskYAML2 {
		t.Fatal("concurrent disk edit was overwritten despite digest mismatch")
	}
	cAfterRace := eng.ConflictFor(bufID)
	if cAfterRace == nil || string(cAfterRace.DiskBytes) != diskYAML2 || string(cAfterRace.BaselineBytes) != string(baseBytes) || string(cAfterRace.EditedBytes) != editedYAML {
		t.Fatalf("conflict state corrupted after race: %+v", cAfterRace)
	}

	// Attempting to apply merged YAML that still contains Git conflict markers must fail!
	mergedWithConflict, clean := MergeThreeWay(cAfterRace.BaselineBytes, cAfterRace.EditedBytes, cAfterRace.DiskBytes)
	if clean {
		t.Fatal("overlapping URL/method edits should not report clean=true")
	}
	if _, err := eng.ResolveConflict(bufID, ResolutionApplyMerged, mergedWithConflict); err == nil {
		t.Fatal("expected error when saving YAML containing unresolved conflict markers")
	}

	// Save As Copy preserves BOTH: creates a new request with a fresh ID, and
	// refreshes the original request to diskYAML2!
	copyRes, err := eng.ResolveConflict(bufID, ResolutionSaveAsCopy, nil)
	if err != nil {
		t.Fatalf("ResolutionSaveAsCopy: %v", err)
	}
	if copyRes.CopyRequest == nil || copyRes.CopyRequest.Request.ID == reqID {
		t.Fatalf("expected copy with new unique ID, got %+v", copyRes.CopyRequest)
	}
	if eng.ConflictFor(bufID) != nil {
		t.Fatal("conflict should clear after ResolutionSaveAsCopy")
	}
	if p.Requests[reqID].Request.Name != "Orders Disk V2" {
		t.Fatalf("original request should reflect disk version after save-as-copy, got %q", p.Requests[reqID].Request.Name)
	}
	if !strings.Contains(copyRes.CopyRequest.Request.HTTP.URL, "local=1") {
		t.Fatalf("copy request lost user's edited URL: %q", copyRes.CopyRequest.Request.HTTP.URL)
	}
}

func TestDirtyBufferExternalDeleteNeverResurrects(t *testing.T) {
	_, p := mustNewProject(t, "NoResurrect")
	gq := p.Dir

	entry, err := p.NewRequest("", "Ephemeral")
	if err != nil {
		t.Fatal(err)
	}
	reqID := entry.Request.ID
	rel := entry.RelPath
	baseDigest := entry.BaselineDigest
	editedYAML := "schemaVersion: 1\nid: " + reqID + "\nname: Unsaved Work\nprotocol: http\nhttp:\n  method: DELETE\n  url: \"{{baseUrl}}/ephemeral\"\n"
	bufID := "req:" + reqID

	eng, err := p.NewSyncEngine(SyncOptions{
		OpenBuffersFn: func() []OpenBuffer {
			return []OpenBuffer{{
				BufferID:       bufID,
				EntityKind:     EntityRequest,
				EntityID:       reqID,
				RelPath:        rel,
				Dirty:          true,
				BaselineDigest: baseDigest,
				EditedBytes:    func() ([]byte, error) { return []byte(editedYAML), nil },
			}}
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Delete file externally on disk.
	absPath := filepath.Join(gq, filepath.FromSlash(rel))
	if err := os.Remove(absPath); err != nil {
		t.Fatal(err)
	}
	// Even BEFORE ScanAndSync runs, CanAutoSave MUST return false so a timer tick
	// cannot resurrect a deleted file!
	if eng.CanAutoSave(bufID, rel, baseDigest) {
		t.Fatal("CanAutoSave must reject externally deleted file even before scan")
	}

	rep, err := eng.ScanAndSync()
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Conflicts) != 1 || rep.Conflicts[0].ConflictKind != ConflictDeleted || rep.Conflicts[0].DiskExists {
		t.Fatalf("expected ConflictDeleted, got %+v", rep.Conflicts)
	}
	if p.Store.Exists(rel) {
		t.Fatal("ScanAndSync resurrected deleted file")
	}

	// ResolutionAcceptDelete discards without touching disk.
	res, err := eng.ResolveConflict(bufID, ResolutionAcceptDelete, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !res.ClosedBuffer || p.Store.Exists(rel) {
		t.Fatal("accept-delete must close buffer and leave file deleted on disk")
	}
}

func TestDirtyBufferExternalRenameFollowsNewPathWithoutResurrectingOld(t *testing.T) {
	_, p := mustNewProject(t, "DirtyRename")
	gq := p.Dir

	entry, err := p.NewRequest("", "Profile")
	if err != nil {
		t.Fatal(err)
	}
	oldRel := entry.RelPath
	newRel := "collections/renamed-profile.request.yaml"
	reqID := entry.Request.ID
	baseBytes, baseDigest, err := p.Store.Read(oldRel)
	if err != nil {
		t.Fatal(err)
	}
	editedYAML := "schemaVersion: 1\nid: " + reqID + "\nname: Profile Dirty\nprotocol: http\nhttp:\n  method: PATCH\n  url: \"{{baseUrl}}/profile\"\n"
	bufID := "req:" + reqID

	curRel := oldRel
	eng, err := p.NewSyncEngine(SyncOptions{
		OpenBuffersFn: func() []OpenBuffer {
			return []OpenBuffer{{
				BufferID:       bufID,
				EntityKind:     EntityRequest,
				EntityID:       reqID,
				RelPath:        curRel,
				Dirty:          true,
				BaselineDigest: baseDigest,
				EditedBytes:    func() ([]byte, error) { return []byte(editedYAML), nil },
			}}
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Case 1: Pure rename (same content) while dirty.
	writeRawProjectFile(t, gq, newRel, string(baseBytes))
	if err := os.Remove(filepath.Join(gq, filepath.FromSlash(oldRel))); err != nil {
		t.Fatal(err)
	}
	rep, err := eng.ScanAndSync()
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Conflicts) != 0 {
		t.Fatalf("pure external rename with unchanged content should not conflict: %+v", rep.Conflicts)
	}
	if len(rep.BufferActions) != 1 || rep.BufferActions[0].Action != BufferActionFollowRename || rep.BufferActions[0].NewPath != newRel {
		t.Fatalf("expected BufferActionFollowRename to %s, got %+v", newRel, rep.BufferActions)
	}
	if entry.RelPath != newRel {
		t.Fatalf("entry.RelPath = %q, want %q", entry.RelPath, newRel)
	}
	curRel = newRel

	// Case 2: External rename + external edit at the same time while still dirty!
	newerRel := "collections/v2-profile.request.yaml"
	diskEdited := "schemaVersion: 1\nid: " + reqID + "\nname: Profile Remote Edit\nprotocol: http\nhttp:\n  method: PUT\n  url: \"{{baseUrl}}/v2/profile\"\n"
	writeRawProjectFile(t, gq, newerRel, diskEdited)
	if err := os.Remove(filepath.Join(gq, filepath.FromSlash(newRel))); err != nil {
		t.Fatal(err)
	}
	rep, err = eng.ScanAndSync()
	if err != nil {
		t.Fatal(err)
	}
	c := eng.ConflictFor(bufID)
	if c == nil || c.ConflictKind != ConflictRenamedModified || c.RelPath != newerRel || c.OldPath != newRel {
		t.Fatalf("expected ConflictRenamedModified at %s, got %+v", newerRel, c)
	}
	// Resolving with KeepEdited writes to newerRel and NEVER resurrects oldRel or newRel!
	if _, err := eng.ResolveConflict(bufID, ResolutionKeepEdited, nil); err != nil {
		t.Fatalf("ResolveConflict KeepEdited: %v", err)
	}
	if p.Store.Exists(oldRel) || p.Store.Exists(newRel) {
		t.Fatal("resolving renamed conflict resurrected old path")
	}
	saved, _, err := p.Store.Read(newerRel)
	if err != nil || string(saved) != editedYAML {
		t.Fatalf("expected edited YAML saved at %s, got %q (err=%v)", newerRel, saved, err)
	}
}

func TestMergeThreeWayCleanAndOverlapping(t *testing.T) {
	base := []byte("line1\nline2\nline3\nline4\nline5\n")
	edited := []byte("line1-local\nline2\nline3\nline4\nline5\n")
	disk := []byte("line1\nline2\nline3\nline4\nline5-remote\n")

	merged, clean := MergeThreeWay(base, edited, disk)
	if !clean {
		t.Fatalf("expected clean 3-way merge, got:\n%s", merged)
	}
	want := "line1-local\nline2\nline3\nline4\nline5-remote\n"
	if string(merged) != want {
		t.Fatalf("merged =\n%s\nwant:\n%s", merged, want)
	}

	// Identical changes on both sides merge cleanly without markers.
	sameMerged, sameClean := MergeThreeWay(base, edited, edited)
	if !sameClean || string(sameMerged) != string(edited) {
		t.Fatalf("identical edits should merge cleanly: clean=%v merged=%q", sameClean, sameMerged)
	}
}

func TestWatcherStormCoalescingAndTempFileFiltering(t *testing.T) {
	_, p := mustNewProject(t, "Storm")
	gq := p.Dir
	entry, err := p.NewRequest("", "Burst")
	if err != nil {
		t.Fatal(err)
	}

	eng, err := p.NewSyncEngine(SyncOptions{})
	if err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	var notifyCount atomic.Int32
	var lastReport *SyncReport

	w, err := StartWatcher(context.Background(), p.Store, WatchOptions{
		PollInterval:   20 * time.Millisecond,
		DebounceWindow: 35 * time.Millisecond,
		MaxBatchWait:   200 * time.Millisecond,
	}, func() {
		mu.Lock()
		defer mu.Unlock()
		notifyCount.Add(1)
		rep, err := eng.ScanAndSync()
		if err == nil {
			lastReport = rep
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	// Step 1: Write temp/swap files only -> watcher MUST ignore them completely!
	writeRawProjectFile(t, gq, "collections/.tmp-atomic-123", "partial")
	writeRawProjectFile(t, gq, "collections/burst.request.yaml.swp", "vimswap")
	w.Trigger()
	time.Sleep(80 * time.Millisecond)
	if got := notifyCount.Load(); got != 0 {
		t.Fatalf("temp/swap files should produce 0 watcher notifications, got %d", got)
	}
	_ = os.Remove(filepath.Join(gq, "collections", ".tmp-atomic-123"))
	_ = os.Remove(filepath.Join(gq, "collections", "burst.request.yaml.swp"))

	// Step 2: Simulate a watch storm: 60 rapid overwrites + ephemeral create/delete!
	for i := 0; i < 60; i++ {
		content := "schemaVersion: 1\nid: " + entry.Request.ID + "\nname: Storm " + string(rune('A'+(i%26))) + "\nprotocol: http\nhttp:\n  method: GET\n  url: \"http://127.0.0.1:4010/iter\"\n"
		if i == 59 {
			content = "schemaVersion: 1\nid: " + entry.Request.ID + "\nname: Final Settled\nprotocol: http\nhttp:\n  method: GET\n  url: \"http://127.0.0.1:4010/final\"\n"
		}
		writeRawProjectFile(t, gq, entry.RelPath, content)
		if i == 10 {
			writeRawProjectFile(t, gq, "collections/ephemeral.request.yaml", "schemaVersion: 1\nid: eph\nname: E\nhttp:\n  method: GET\n  url: http://h\n")
		}
		if i == 20 {
			_ = os.Remove(filepath.Join(gq, "collections", "ephemeral.request.yaml"))
		}
		w.Trigger()
	}

	// Wait for debounce to settle.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		settled := p.Requests[entry.Request.ID] != nil && p.Requests[entry.Request.ID].Request.Name == "Final Settled"
		mu.Unlock()
		if settled {
			break
		}
		time.Sleep(15 * time.Millisecond)
	}

	mu.Lock()
	gotName := p.Requests[entry.Request.ID].Request.Name
	gotURL := p.Requests[entry.Request.ID].Request.HTTP.URL
	_, hasEphemeral := p.Requests["eph"]
	batches := notifyCount.Load()
	_ = lastReport
	mu.Unlock()

	if gotName != "Final Settled" || gotURL != "http://127.0.0.1:4010/final" {
		t.Fatalf("watcher did not settle on final content: name=%q url=%q", gotName, gotURL)
	}
	if hasEphemeral {
		t.Fatal("ephemeral request created and deleted mid-storm should not remain in Project")
	}
	// 60 rapid writes must coalesce into far fewer batches (typically 1-3).
	if batches < 1 || batches > 10 {
		t.Fatalf("expected storm of 60 triggers to coalesce into 1..10 batches, got %d (stats=%+v)", batches, w.Stats())
	}

	// Step 3: Local save recorded + AcknowledgeCurrent must NOT fire a watcher callback.
	beforeLocal := notifyCount.Load()
	entry.Request.Name = "Local Save"
	if _, err := p.SaveRequest(entry, entry.Request, entry.BaselineDigest); err != nil {
		t.Fatal(err)
	}
	savedBytes, _, _ := p.Store.Read(entry.RelPath)
	eng.RecordLocalSave(entry.RelPath, savedBytes)
	w.AcknowledgeCurrent()
	w.Trigger()
	time.Sleep(80 * time.Millisecond)
	if afterLocal := notifyCount.Load(); afterLocal != beforeLocal {
		t.Fatalf("acknowledged local save triggered external notification: %d -> %d", beforeLocal, afterLocal)
	}
}
