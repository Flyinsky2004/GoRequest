package core

import (
	"context"
	"testing"
	"time"
)

// FIX01: when an event's NewDigest already equals the open buffer's own reading
// baseline, the change is a self-write reflection (e.g. an in-place rename that
// rewrote the same path, or a save whose snapshot echo raced the ack) and must
// NOT raise a three-version conflict — even for a dirty buffer. A genuine
// divergence (NewDigest != buffer baseline) must still conflict.
func TestFIX01SelfWriteSameBaselineSuppressesConflict(t *testing.T) {
	_, p := mustNewProject(t, "SelfWriteSuppress")
	gq := p.Dir

	entry, err := p.NewRequest("", "Suppress")
	if err != nil {
		t.Fatal(err)
	}
	reqID := entry.Request.ID
	bufID := "req:" + reqID
	_, baseDigest, err := p.Store.Read(entry.RelPath)
	if err != nil {
		t.Fatal(err)
	}

	editedYAML := "schemaVersion: 1\nid: " + reqID + "\nname: Suppress Edited\nprotocol: http\nhttp:\n  method: GET\n  url: \"https://local.example.com/edit\"\n"
	diskYAML := "schemaVersion: 1\nid: " + reqID + "\nname: Suppress\nprotocol: http\nhttp:\n  method: GET\n  url: \"https://self.example.com/renamed\"\n"
	diskDigest := Digest([]byte(diskYAML))

	// Buffer baseline is captured early; the closure lets each case choose the
	// baseline the simulated self-write would have advanced to.
	bufBaseline := baseDigest
	eng, err := p.NewSyncEngine(SyncOptions{
		OpenBuffersFn: func() []OpenBuffer {
			return []OpenBuffer{{
				BufferID:       bufID,
				EntityKind:     EntityRequest,
				EntityID:       reqID,
				RelPath:        entry.RelPath,
				Dirty:          true,
				BaselineDigest: bufBaseline,
				EditedBytes:    func() ([]byte, error) { return []byte(editedYAML), nil },
			}}
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Self-write: disk now holds diskYAML and the tab baseline was advanced to
	// match it (as commitRename does), but the engine snapshot is still stale.
	writeRawProjectFile(t, gq, entry.RelPath, diskYAML)
	bufBaseline = diskDigest

	rep, err := eng.ScanAndSync()
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Conflicts) != 0 {
		t.Fatalf("self-write with matching baseline must not conflict: %+v", rep.Conflicts)
	}
	if eng.ConflictFor(bufID) != nil {
		t.Fatal("self-write with matching baseline recorded a conflict")
	}

	// Genuine external divergence: buffer baseline stays at the ORIGINAL digest
	// while disk is changed again to a different version → must conflict.
	diskYAML2 := "schemaVersion: 1\nid: " + reqID + "\nname: Suppress\nprotocol: http\nhttp:\n  method: PUT\n  url: \"https://external.example.com/changed\"\n"
	writeRawProjectFile(t, gq, entry.RelPath, diskYAML2)
	bufBaseline = baseDigest
	rep2, err := eng.ScanAndSync()
	if err != nil {
		t.Fatal(err)
	}
	if len(rep2.Conflicts) != 1 || rep2.Conflicts[0].ConflictKind != ConflictModified {
		t.Fatalf("genuine divergence must conflict, got %+v", rep2.Conflicts)
	}
}

// FIX01: acknowledging a single saved path must not silently swallow a genuine
// external change to a different file in the tree.
func TestFIX01AcknowledgePathDoesNotSwallowOtherExternalChange(t *testing.T) {
	_, p := mustNewProject(t, "AckScope")
	gq := p.Dir

	e1, err := p.NewRequest("", "One")
	if err != nil {
		t.Fatal(err)
	}
	e2, err := p.NewRequest("", "Two")
	if err != nil {
		t.Fatal(err)
	}

	w, err := StartWatcher(context.Background(), p.Store, WatchOptions{
		PollInterval:   time.Hour,
		DebounceWindow: 10 * time.Millisecond,
	}, func() {})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	// Genuine external change to e2.
	external := "schemaVersion: 1\nid: " + e2.Request.ID + "\nname: Two External\nprotocol: http\nhttp:\n  method: GET\n  url: \"https://external.example.com\"\n"
	writeRawProjectFile(t, gq, e2.RelPath, external)

	cur, err := computeTreeState(p.Store, p.Store.EffectiveLimits())
	if err != nil {
		t.Fatal(err)
	}

	// Acknowledge only the path we (hypothetically) just saved (e1).
	w.AcknowledgeCurrent(e1.RelPath)

	w.mu.Lock()
	baseE2 := w.baselineFiles[e2.RelPath]
	w.mu.Unlock()
	if baseE2 == cur[e2.RelPath] {
		t.Fatal("path-scoped acknowledge swallowed an unrelated external change")
	}

	// Whole-tree acknowledge must settle everything.
	w.AcknowledgeCurrent()
	w.mu.Lock()
	baseE2b := w.baselineFiles[e2.RelPath]
	w.mu.Unlock()
	if baseE2b != cur[e2.RelPath] {
		t.Fatal("whole-tree acknowledge did not adopt current state")
	}
}
