package update

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStateStoreRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "update-state.json")
	s := NewStateStore(path)
	auto := false
	if err := s.Save(State{SkippedVersion: "1.2.3", NeverNotify: true, AutoCheck: &auto}); err != nil {
		t.Fatal(err)
	}
	got := NewStateStore(path).Load()
	if got.SkippedVersion != "1.2.3" || !got.NeverNotify || got.AutoCheckEnabled() {
		t.Fatalf("state = %+v", got)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("state mode = %v, want 0600", info.Mode().Perm())
	}
}

func TestStateStoreCorruptFileStartsFresh(t *testing.T) {
	path := filepath.Join(t.TempDir(), "update-state.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := NewStateStore(path).Load(); got != (State{}) {
		t.Fatalf("corrupt state = %+v, want zero", got)
	}
}

func TestStateStoreInMemory(t *testing.T) {
	s := NewStateStore("")
	if err := s.Save(State{SkippedVersion: "x"}); err != nil {
		t.Fatal(err)
	}
	if got := s.Load(); got.SkippedVersion != "x" {
		t.Fatalf("in-memory store lost state: %+v", got)
	}
	if _, err := s.Mutate(func(st *State) { st.NeverNotify = true }); err != nil {
		t.Fatal(err)
	}
	if !s.Load().NeverNotify {
		t.Fatal("in-memory Mutate did not persist for the run")
	}
}

func TestStateMutate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "update-state.json")
	s := NewStateStore(path)
	if _, err := s.Mutate(func(st *State) {
		st.NeverNotify = true
		st.LastCheckDate = "2026-11-08"
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Mutate(func(st *State) { st.SkippedVersion = "0.0.2-dev" }); err != nil {
		t.Fatal(err)
	}
	got := s.Load()
	if !got.NeverNotify || got.LastCheckDate != "2026-11-08" || got.SkippedVersion != "0.0.2-dev" {
		t.Fatalf("state = %+v", got)
	}
}

func TestStateDailyCacheAndSuppression(t *testing.T) {
	st := State{LastCheckDate: "2026-11-08", LastCheckVersion: "0.0.1-dev"}
	if !st.CheckedToday("0.0.1-dev", "2026-11-08") {
		t.Fatal("same day and version should be cached")
	}
	if st.CheckedToday("0.0.2-dev", "2026-11-08") {
		t.Fatal("a new installed version must invalidate the daily cache")
	}
	if st.CheckedToday("0.0.1-dev", "2026-11-09") {
		t.Fatal("a new day must invalidate the daily cache")
	}

	st.SkippedVersion = "0.0.2-dev"
	if !st.Suppresses("0.0.2-dev", "2026-11-08") {
		t.Fatal("skipped version should be suppressed")
	}
	if st.Suppresses("0.0.3-dev", "2026-11-08") {
		t.Fatal("a different version must prompt")
	}

	st = State{DeferredVersion: "0.0.2-dev", DeferredDate: "2026-11-08"}
	if !st.Suppresses("0.0.2-dev", "2026-11-08") {
		t.Fatal("deferred version should be suppressed today")
	}
	if st.Suppresses("0.0.2-dev", "2026-11-09") {
		t.Fatal("deferred version should prompt tomorrow")
	}
	if st.Suppresses("0.0.3-dev", "2026-11-08") {
		t.Fatal("a newer version must prompt the same day")
	}
}

func TestStateAutoCheckDefault(t *testing.T) {
	if !(State{}).AutoCheckEnabled() {
		t.Fatal("absent autoCheck must default to enabled")
	}
	off := false
	if (State{AutoCheck: &off}).AutoCheckEnabled() {
		t.Fatal("explicit false must disable auto check")
	}
}

func TestToday(t *testing.T) {
	loc := time.FixedZone("UTC+8", 8*3600)
	now := time.Date(2026, 11, 8, 23, 30, 0, 0, loc)
	if got := Today(now); got != "2026-11-08" {
		t.Fatalf("Today = %q", got)
	}
}
