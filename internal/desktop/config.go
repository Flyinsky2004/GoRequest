package desktop

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"gorequest/internal/core"
)

const (
	configDirName     = "GoRequest"
	configFileName    = "config.json"
	maxRecentProjects = 25
)

// recentProject is one entry of the recently opened project list. Only the
// normalized project root path and a display name are kept; nothing about the
// project's requests or secrets is stored here.
type recentProject struct {
	Path string `json:"path"`
	Name string `json:"name"`
}

// appConfig is the minimal local configuration, kept in the user's config
// directory so that recent projects, the last active project, the startup
// restore preference and a personal appearance preference never land in a
// Git-tracked project file.
//
// The three older fields keep their names and meanings, so a config written by
// a previous build still loads: unknown-to-the-old-build fields are simply
// absent and take their defaults (see restoreLastEnabled).
type appConfig struct {
	Appearance string `json:"appearance,omitempty"`
	// Language is the interface language ("zh-CN" or "en"). An absent value (a
	// new install or a config written by an older build) defaults to Simplified
	// Chinese, so old configs keep loading with no migration needed.
	Language string `json:"language,omitempty"`
	// LastProject is the normalized root of the project that was most recently
	// opened or activated. It is written only after a successful open or
	// activation, so a failed open never clobbers the last good value, and it
	// is cleared or moved when that project is closed, so a restart does not
	// reopen a project the user explicitly returned from.
	LastProject string `json:"lastProject,omitempty"`
	// RestoreLast controls whether startup reopens LastProject. It is stored as
	// a pointer so an old config without the field means "unset" (default on)
	// while an explicit false is preserved.
	RestoreLast    *bool           `json:"restoreLastProject,omitempty"`
	RecentProjects []recentProject `json:"recentProjects,omitempty"`
}

// configStore reads and writes appConfig atomically. A store with an empty path
// works in memory only, so the app still runs when no config directory is
// available; load and save never fail the app.
type configStore struct {
	path string
	cfg  appConfig
}

// configFilePath returns the default config file under the OS config dir, or ""
// when the platform cannot provide one.
func configFilePath() string {
	dir, err := os.UserConfigDir()
	if err != nil || dir == "" {
		return ""
	}
	return filepath.Join(dir, configDirName, configFileName)
}

func newConfigStore(path string) *configStore {
	s := &configStore{path: path}
	s.load()
	return s
}

func (s *configStore) load() {
	if s.path == "" {
		return
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		return
	}
	// A malformed config is ignored rather than fatal; the app starts fresh.
	_ = json.Unmarshal(data, &s.cfg)
}

func (s *configStore) save() error {
	if s.path == "" {
		return nil
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s.cfg, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(dir, ".config-*.json")
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
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return err
	}
	return os.Rename(tmpName, s.path)
}

// appearance returns the stored appearance choice ("", "System", "Light" or
// "Dark"); "" follows the system.
func (s *configStore) appearance() string { return s.cfg.Appearance }

func (s *configStore) setAppearance(v string) {
	s.cfg.Appearance = v
	_ = s.save()
}

// language returns the stored interface language, defaulting to Simplified
// Chinese for an absent, empty or unsupported value (backward compatible with
// configs written before language support existed).
func (s *configStore) language() language { return parseLanguage(s.cfg.Language) }

// setLanguage persists the interface language choice.
func (s *configStore) setLanguage(l language) {
	s.cfg.Language = string(parseLanguage(string(l)))
	_ = s.save()
}

// lastProject returns the normalized root of the most recently opened or
// activated project, or "" when none has been recorded.
func (s *configStore) lastProject() string { return s.cfg.LastProject }

// setLastProject records path as the last active project. It is called only on
// a successful open or activation, so a failed open leaves the previous value
// intact. It does not itself touch the recent list.
func (s *configStore) setLastProject(path string) {
	if path == "" || s.cfg.LastProject == path {
		return
	}
	s.cfg.LastProject = path
	_ = s.save()
}

// clearLastProject forgets the last active project, so the next launch shows
// the project chooser instead of reopening it. This is what "close project /
// return to project selection" uses.
func (s *configStore) clearLastProject() {
	if s.cfg.LastProject == "" {
		return
	}
	s.cfg.LastProject = ""
	_ = s.save()
}

// restoreLastEnabled reports whether startup should reopen the last project.
// An unset preference (a new install, or an old config without the field)
// defaults to enabled; an explicit false turns it off.
func (s *configStore) restoreLastEnabled() bool {
	return s.cfg.RestoreLast == nil || *s.cfg.RestoreLast
}

func (s *configStore) setRestoreLast(v bool) {
	b := v
	s.cfg.RestoreLast = &b
	_ = s.save()
}

// removeRecent drops a recent project entry (used when the user closes a
// project and it should no longer be offered as a recent, while a stale path
// can also simply stay and be reported on next click). It keeps the list
// capped and preserves order.
func (s *configStore) removeRecent(path string) {
	if path == "" {
		return
	}
	kept := make([]recentProject, 0, len(s.cfg.RecentProjects))
	for _, rp := range s.cfg.RecentProjects {
		if rp.Path != path {
			kept = append(kept, rp)
		}
	}
	if len(kept) == len(s.cfg.RecentProjects) {
		return
	}
	s.cfg.RecentProjects = kept
	_ = s.save()
}

// recent returns a copy of the recent project list, newest first.
func (s *configStore) recent() []recentProject {
	out := make([]recentProject, len(s.cfg.RecentProjects))
	copy(out, s.cfg.RecentProjects)
	return out
}

// addRecent records path as recently opened, moving an existing entry to the
// front instead of duplicating it, and caps the list.
func (s *configStore) addRecent(path, name string) {
	if path == "" {
		return
	}
	next := []recentProject{{Path: path, Name: name}}
	for _, rp := range s.cfg.RecentProjects {
		if rp.Path == path {
			continue
		}
		next = append(next, rp)
	}
	if len(next) > maxRecentProjects {
		next = next[:maxRecentProjects]
	}
	s.cfg.RecentProjects = next
	_ = s.save()
}

// setRecentName updates the display name of an already stored project path.
func (s *configStore) setRecentName(path, name string) {
	changed := false
	for i := range s.cfg.RecentProjects {
		if s.cfg.RecentProjects[i].Path == path && s.cfg.RecentProjects[i].Name != name {
			s.cfg.RecentProjects[i].Name = name
			changed = true
		}
	}
	if changed {
		_ = s.save()
	}
}

// normalizeProjectPath resolves a user-supplied path to the canonical project
// root directory (the folder that contains .gorequest/), resolving symlinks so
// that "." and an absolute path to the same project compare equal.
func normalizeProjectPath(path string) (string, error) {
	gq, err := core.LocateProjectDir(path)
	if err != nil {
		return "", err
	}
	root := filepath.Dir(gq)
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	return filepath.Clean(root), nil
}

// displayPath shortens a path under the user's home directory with "~" so that
// long secondary labels stay readable in a narrow sidebar.
func displayPath(p string) string {
	home, err := os.UserHomeDir()
	if err == nil && home != "" {
		if p == home {
			return "~"
		}
		if strings.HasPrefix(p, home+string(filepath.Separator)) {
			return "~" + p[len(home):]
		}
	}
	return p
}
