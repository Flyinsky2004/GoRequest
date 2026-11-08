package update

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Result tokens persisted in State.LastResult. They are stable, language
// independent identifiers; the UI maps them to localized text.
const (
	ResultUpdateAvailable = "update-available"
	ResultUpToDate        = "up-to-date"
	ResultNoRelease       = "no-release"
	ResultDevelopment     = "development"
	ResultError           = "error"
	ResultRateLimited     = "rate-limited"
	ResultOffline         = "offline"
	ResultNoAsset         = "no-asset"
	ResultCanceled        = "canceled"
	ResultDownloaded      = "downloaded"
)

// State is the persistent update state. It lives beside the app config, never
// in a project or Git. It holds no secrets and no downloaded content.
type State struct {
	// LastCheckDate is the local calendar date (YYYY-MM-DD) of the last
	// completed automatic or manual check, and LastCheckVersion is the
	// installed version at that moment. Together they implement "once per
	// day per installed version": a new installed version invalidates the
	// date cache immediately, so installing V2 the same day still finds V3.
	LastCheckDate    string `json:"lastCheckDate,omitempty"`
	LastCheckVersion string `json:"lastCheckVersion,omitempty"`
	LastCheckedAt    string `json:"lastCheckedAt,omitempty"` // RFC3339, UTC
	LastResult       string `json:"lastResult,omitempty"`
	LastError        string `json:"lastError,omitempty"`
	LatestVersion    string `json:"latestVersion,omitempty"`

	// SkippedVersion is the one version the user chose not to be offered
	// again. A different (newer) version prompts normally.
	SkippedVersion string `json:"skippedVersion,omitempty"`
	// DeferredVersion/DeferredDate record "Later" for one version on one
	// local day, so the same prompt does not reappear until tomorrow.
	DeferredVersion string `json:"deferredVersion,omitempty"`
	DeferredDate    string `json:"deferredDate,omitempty"`

	// NeverNotify persistently disables automatic checks and prompts. Manual
	// checks still work. AutoCheck (default on) turns automatic checks off
	// without hiding prompts for a manually requested check.
	NeverNotify bool  `json:"neverNotify,omitempty"`
	AutoCheck   *bool `json:"autoCheck,omitempty"`
}

// AutoCheckEnabled reports whether automatic startup checks are allowed. An
// absent value means enabled, so old state files keep working.
func (s State) AutoCheckEnabled() bool { return s.AutoCheck == nil || *s.AutoCheck }

// CheckedToday reports whether an automatic check already completed today
// for the running installed version.
func (s State) CheckedToday(current, today string) bool {
	return s.LastCheckDate == today && s.LastCheckVersion == current
}

// Suppresses reports whether an automatic prompt for version must stay
// hidden: the user skipped that exact version, or deferred it earlier today.
// Manual checks ignore this.
func (s State) Suppresses(version, today string) bool {
	if s.SkippedVersion != "" && s.SkippedVersion == version {
		return true
	}
	return s.DeferredDate == today && s.DeferredVersion == version
}

// StateStore reads and writes State atomically. An empty path keeps the state
// in memory only, so a run without a config directory still works.
type StateStore struct {
	path string
	mu   sync.Mutex
	// mem caches the state for this run, so the Settings pane (which reads it
	// every frame) does not hit the disk each frame. It also holds the state
	// when path is empty, so preferences work without a config directory.
	mem    State
	loaded bool
}

// NewStateStore returns a store for path ("" for in-memory).
func NewStateStore(path string) *StateStore { return &StateStore{path: path} }

// Load returns the persisted state. A missing or malformed file yields the
// zero state rather than failing startup; update preferences are conveniences,
// not a reason to block the app.
func (s *StateStore) Load() State {
	if s == nil {
		return State{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadLocked()
}

func (s *StateStore) loadLocked() State {
	if s.loaded {
		return s.mem
	}
	s.loaded = true
	if s.path == "" {
		return s.mem
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		return s.mem
	}
	var st State
	if err := json.Unmarshal(data, &st); err == nil {
		s.mem = st
	}
	return s.mem
}

// Save writes the state atomically with owner-only permissions.
func (s *StateStore) Save(st State) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveLocked(st)
}

func (s *StateStore) saveLocked(st State) error {
	// Keep the in-memory copy current even if the disk write fails, so the
	// Settings pane reflects the user's choice for this run.
	s.mem = st
	s.loaded = true
	if s.path == "" {
		return nil
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(dir, ".update-state-*.json")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after a successful rename
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		return err
	}
	return os.Rename(tmpName, s.path)
}

// Mutate applies fn to the current state and saves the result atomically.
// It returns the state after the change. A save failure is returned but the
// in-memory result is still what the caller asked for.
func (s *StateStore) Mutate(fn func(*State)) (State, error) {
	if s == nil {
		var st State
		if fn != nil {
			fn(&st)
		}
		return st, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.loadLocked()
	if fn != nil {
		fn(&st)
	}
	return st, s.saveLocked(st)
}

// Today returns the local calendar date used by the daily cache.
func Today(now time.Time) string { return now.Format("2006-01-02") }
