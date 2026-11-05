package desktop

import (
	"os"
	"path/filepath"
	"testing"

	"gorequest/internal/core"
)

func writeProject(t *testing.T, name string) string {
	t.Helper()
	dir := t.TempDir()
	if _, err := core.NewProject(dir, name); err != nil {
		t.Fatalf("NewProject(%q): %v", dir, err)
	}
	return dir
}

func TestNormalizeProjectPathDedup(t *testing.T) {
	dir := writeProject(t, "Demo")
	a, err := normalizeProjectPath(dir)
	if err != nil {
		t.Fatal(err)
	}
	b, err := normalizeProjectPath(filepath.Join(dir, ".gorequest"))
	if err != nil {
		t.Fatal(err)
	}
	c, err := normalizeProjectPath(filepath.Join(dir, "."))
	if err != nil {
		t.Fatal(err)
	}
	if a != b || a != c {
		t.Fatalf("paths not normalized equal: %q vs %q vs %q", a, b, c)
	}
}

func TestNormalizeProjectPathInvalid(t *testing.T) {
	if _, err := normalizeProjectPath(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("expected error for a folder without .gorequest/")
	}
}

func TestConfigStoreRecentDedupAndPersist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	s := newConfigStore(path)
	s.addRecent("/proj/a", "A")
	s.addRecent("/proj/b", "B")
	s.addRecent("/proj/a", "A renamed") // reopening must move, not duplicate

	got := s.recent()
	if len(got) != 2 {
		t.Fatalf("want 2 recent projects, got %d: %+v", len(got), got)
	}
	if got[0].Path != "/proj/a" || got[0].Name != "A renamed" {
		t.Fatalf("expected reopened project first with new name, got %+v", got[0])
	}
	if got[1].Path != "/proj/b" {
		t.Fatalf("expected /proj/b second, got %+v", got[1])
	}

	// Reloading from disk must restore the same list (survives restart).
	s2 := newConfigStore(path)
	got2 := s2.recent()
	if len(got2) != 2 || got2[0].Path != "/proj/a" || got2[1].Path != "/proj/b" {
		t.Fatalf("reloaded recents mismatch: %+v", got2)
	}
}

func TestConfigStoreCapsRecent(t *testing.T) {
	s := newConfigStore(filepath.Join(t.TempDir(), "config.json"))
	for i := 0; i < maxRecentProjects+5; i++ {
		s.addRecent(filepath.Join("/proj", string(rune('a'+i))), string(rune('A'+i)))
	}
	if n := len(s.recent()); n != maxRecentProjects {
		t.Fatalf("want cap %d, got %d", maxRecentProjects, n)
	}
}

func TestConfigStoreMalformedFileIgnored(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte("{ this is not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := newConfigStore(path) // must not panic
	if len(s.recent()) != 0 {
		t.Fatalf("want empty recents from malformed config, got %+v", s.recent())
	}
	if s.appearance() != "" {
		t.Fatalf("want empty appearance, got %q", s.appearance())
	}
}

func TestConfigStoreMemoryOnly(t *testing.T) {
	s := newConfigStore("")
	s.addRecent("/x", "X")
	// Without a path the list still works in memory for this session...
	if len(s.recent()) != 1 {
		t.Fatalf("in-memory store should keep the list, got %d", len(s.recent()))
	}
	// ...but nothing is written to disk, so a fresh store starts empty.
	if err := s.save(); err != nil {
		t.Fatalf("save with empty path should be a no-op, got %v", err)
	}
	if got := len(newConfigStore("").recent()); got != 0 {
		t.Fatalf("a fresh in-memory store must start empty, got %d", got)
	}
}

func TestConfigStoreAppearancePersist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	s := newConfigStore(path)
	s.setAppearance("Dark")
	if got := newConfigStore(path).appearance(); got != "Dark" {
		t.Fatalf("appearance not persisted, got %q", got)
	}
}

// TestConfigBackwardCompatibleOldFile ensures a config written before the
// last-project/restore fields existed still loads, with restore defaulting on.
func TestConfigBackwardCompatibleOldFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	old := `{
  "appearance": "Light",
  "recentProjects": [{"path": "/proj/a", "name": "A"}]
}`
	if err := os.WriteFile(path, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	s := newConfigStore(path)
	if s.appearance() != "Light" {
		t.Fatalf("old appearance lost: %q", s.appearance())
	}
	if len(s.recent()) != 1 || s.recent()[0].Path != "/proj/a" {
		t.Fatalf("old recents lost: %+v", s.recent())
	}
	if s.lastProject() != "" {
		t.Fatalf("old config should have no last project, got %q", s.lastProject())
	}
	if !s.restoreLastEnabled() {
		t.Fatal("restore should default to enabled when the field is absent")
	}
}

func TestConfigLastProjectPersistAndClear(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	s := newConfigStore(path)
	s.setLastProject("/proj/a")
	if got := newConfigStore(path).lastProject(); got != "/proj/a" {
		t.Fatalf("last project not persisted: %q", got)
	}
	s.clearLastProject()
	if got := newConfigStore(path).lastProject(); got != "" {
		t.Fatalf("last project not cleared: %q", got)
	}
}

func TestConfigRestoreTogglePersist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	s := newConfigStore(path)
	if !s.restoreLastEnabled() {
		t.Fatal("default should be enabled")
	}
	s.setRestoreLast(false)
	if newConfigStore(path).restoreLastEnabled() {
		t.Fatal("disabled restore should persist")
	}
	s.setRestoreLast(true)
	if !newConfigStore(path).restoreLastEnabled() {
		t.Fatal("re-enabled restore should persist")
	}
}
