package desktop

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"gorequest/internal/desktop/uikit"
)

// This file owns the B02 device-level workbench preferences: editor font and
// indentation, safe auto-save, per-shortcut toggles and pinned recent projects.
// They are stored in their own JSON file next to config.json and never in a
// Git-tracked project, so a shared project cannot change a device's behaviour.

const (
	defaultEditorFontSize = 13
	minEditorFontSize     = 10
	maxEditorFontSize     = 24
	defaultEditorTabSize  = 4
	defaultAutoSaveDelay  = 5
	minAutoSaveDelay      = 2
	maxAutoSaveDelay      = 60
	autoSaveTickInterval  = time.Second
)

// Shortcut identifiers. They are stable tokens persisted in workspace.json;
// the visible label and key binding are localized/display-only.
const (
	shortcutNewRequest    = "new_request"
	shortcutCloseTab      = "close_tab"
	shortcutTabSwitch     = "tab_switch"
	shortcutFocusURL      = "focus_url"
	shortcutQuickSearch   = "quick_search"
	shortcutEditEnv       = "edit_env"
	shortcutToggleSidebar = "toggle_sidebar"
	shortcutUndoDelete    = "undo_delete"
	shortcutRename        = "rename"
)

// workspacePrefs is the persisted B02 preference set. Missing fields take
// documented defaults, so older/partial files keep loading.
type workspacePrefs struct {
	EditorFontSize       int      `json:"editorFontSize,omitempty"`
	EditorTabSize        int      `json:"editorTabSize,omitempty"`
	EditorSoftWrap       bool     `json:"editorSoftWrap,omitempty"`
	EditorLineNumbers    *bool    `json:"editorLineNumbers,omitempty"`
	AutoSaveEnabled      bool     `json:"autoSaveEnabled,omitempty"`
	AutoSaveDelaySeconds int      `json:"autoSaveDelaySeconds,omitempty"`
	DisabledShortcuts    []string `json:"disabledShortcuts,omitempty"`
	PinnedProjects       []string `json:"pinnedProjects,omitempty"`

	Markdown markdownPreferences `json:"markdown,omitempty"`
}

// workspacePrefsStore reads and writes workspacePrefs atomically. An empty path
// is memory-only, so headless tests and a platform without a config directory
// still work.
type workspacePrefsStore struct {
	// UI-thread callback; no preference data is stored in project files.
	markdownChanged func()

	path string

	mu  sync.Mutex
	cfg workspacePrefs
}

func newWorkspacePrefsStore(path string) *workspacePrefsStore {
	s := &workspacePrefsStore{path: path}
	s.load()
	return s
}

// workspacePrefsPathFor returns the preference file beside a config file, or ""
// when no config directory is available.
func workspacePrefsPathFor(configPath string) string {
	if strings.TrimSpace(configPath) == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(configPath), "workspace.json")
}

func (s *workspacePrefsStore) load() {
	if s == nil || s.path == "" {
		return
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		return
	}
	_ = json.Unmarshal(data, &s.cfg)
}

func (s *workspacePrefsStore) save() {
	if s == nil || s.path == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.saveLocked()
}

func (s *workspacePrefsStore) saveLocked() {
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	data, err := json.MarshalIndent(s.cfg, "", "  ")
	if err != nil {
		return
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(dir, ".workspace-*.json")
	if err != nil {
		return
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return
	}
	if err := tmp.Close(); err != nil {
		return
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return
	}
	_ = os.Rename(tmpName, s.path)
}

// --- normalized getters and setters --------------------------------------

func (s *workspacePrefsStore) fontSize() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	v := s.cfg.EditorFontSize
	if v < minEditorFontSize || v > maxEditorFontSize {
		return defaultEditorFontSize
	}
	return v
}

func (s *workspacePrefsStore) setFontSize(v int) {
	if v < minEditorFontSize || v > maxEditorFontSize {
		return
	}
	s.mu.Lock()
	changed := s.cfg.EditorFontSize != v
	s.cfg.EditorFontSize = v
	s.mu.Unlock()
	if changed {
		s.save()
	}
}

func (s *workspacePrefsStore) tabSize() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	v := s.cfg.EditorTabSize
	if v < 1 || v > 16 {
		return defaultEditorTabSize
	}
	return v
}

func (s *workspacePrefsStore) setTabSize(v int) {
	if v < 1 || v > 16 {
		return
	}
	s.mu.Lock()
	changed := s.cfg.EditorTabSize != v
	s.cfg.EditorTabSize = v
	s.mu.Unlock()
	if changed {
		s.save()
	}
}

func (s *workspacePrefsStore) softWrap() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cfg.EditorSoftWrap
}

func (s *workspacePrefsStore) setSoftWrap(v bool) {
	s.mu.Lock()
	changed := s.cfg.EditorSoftWrap != v
	s.cfg.EditorSoftWrap = v
	s.mu.Unlock()
	if changed {
		s.save()
	}
}

func (s *workspacePrefsStore) lineNumbers() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cfg.EditorLineNumbers == nil || *s.cfg.EditorLineNumbers
}

func (s *workspacePrefsStore) setLineNumbers(v bool) {
	b := v
	s.mu.Lock()
	changed := s.cfg.EditorLineNumbers == nil || *s.cfg.EditorLineNumbers != b
	s.cfg.EditorLineNumbers = &b
	s.mu.Unlock()
	if changed {
		s.save()
	}
}

func (s *workspacePrefsStore) autoSaveEnabled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cfg.AutoSaveEnabled
}

func (s *workspacePrefsStore) setAutoSaveEnabled(v bool) {
	s.mu.Lock()
	changed := s.cfg.AutoSaveEnabled != v
	s.cfg.AutoSaveEnabled = v
	s.mu.Unlock()
	if changed {
		s.save()
	}
}

// autoSaveDelay returns the configured interval between safe auto-save
// attempts, clamped to a usable range.
func (s *workspacePrefsStore) autoSaveDelay() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	sec := s.cfg.AutoSaveDelaySeconds
	if sec < minAutoSaveDelay || sec > maxAutoSaveDelay {
		sec = defaultAutoSaveDelay
	}
	return time.Duration(sec) * time.Second
}

func (s *workspacePrefsStore) setAutoSaveDelaySeconds(sec int) {
	if sec < minAutoSaveDelay || sec > maxAutoSaveDelay {
		return
	}
	s.mu.Lock()
	changed := s.cfg.AutoSaveDelaySeconds != sec
	s.cfg.AutoSaveDelaySeconds = sec
	s.mu.Unlock()
	if changed {
		s.save()
	}
}

func (s *workspacePrefsStore) shortcutEnabled(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, disabled := range s.cfg.DisabledShortcuts {
		if disabled == id {
			return false
		}
	}
	return true
}

func (s *workspacePrefsStore) setShortcutEnabled(id string, on bool) {
	if strings.TrimSpace(id) == "" {
		return
	}
	s.mu.Lock()
	var next []string
	changed := false
	for _, disabled := range s.cfg.DisabledShortcuts {
		if disabled == id {
			changed = true
			continue
		}
		next = append(next, disabled)
	}
	if !on {
		next = append(next, id)
		changed = true
	}
	if changed {
		s.cfg.DisabledShortcuts = next
	}
	s.mu.Unlock()
	if changed {
		s.save()
	}
}

func (s *workspacePrefsStore) pinned(path string) bool {
	if path == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range s.cfg.PinnedProjects {
		if p == path {
			return true
		}
	}
	return false
}

func (s *workspacePrefsStore) setPinned(path string, pinned bool) {
	if path == "" {
		return
	}
	s.mu.Lock()
	next := make([]string, 0, len(s.cfg.PinnedProjects)+1)
	found := false
	for _, p := range s.cfg.PinnedProjects {
		if p == path {
			found = true
			if !pinned {
				continue
			}
		}
		next = append(next, p)
	}
	if pinned && !found {
		next = append(next, path)
	}
	if len(next) == len(s.cfg.PinnedProjects) && (pinned == found) {
		s.mu.Unlock()
		return
	}
	s.cfg.PinnedProjects = next
	s.mu.Unlock()
	s.save()
}

// editorPolicy is the immutable snapshot applied to an editor.State.
type editorPolicy struct {
	FontSize    float32
	TabSize     int
	SoftWrap    bool
	LineNumbers bool
}

func (s *workspacePrefsStore) editorPolicy() editorPolicy {
	return editorPolicy{
		FontSize:    float32(s.fontSize()),
		TabSize:     s.tabSize(),
		SoftWrap:    s.softWrap(),
		LineNumbers: s.lineNumbers(),
	}
}

func (p editorPolicy) apply(st *editor.State) {
	if st == nil {
		return
	}
	st.Font = ui.Font{Size: p.FontSize}
	st.TabSize = p.TabSize
	st.SoftWrap = p.SoftWrap
	if p.LineNumbers {
		st.LineNumbers = editor.LineNumbersOn
	} else {
		st.LineNumbers = editor.LineNumbersOff
	}
}

// workspacePrefs returns the manager's preference store, creating it beside the
// config file on first use. A manager built without a config path stays
// memory-only; tests that set a temp config path therefore never touch the real
// user directory.
func (m *Manager) workspacePrefs() *workspacePrefsStore {
	if m == nil {
		return newWorkspacePrefsStore("")
	}
	if m.prefs == nil {
		path := ""
		if m.cfg != nil {
			path = workspacePrefsPathFor(m.cfg.path)
		}
		m.prefs = newWorkspacePrefsStore(path)
		m.prefs.markdownChanged = m.invalidateMarkdownPreviews
	}
	return m.prefs
}

// applyEditorPrefs applies the current editor policy to a tab's live editors.
// It is idempotent, so the window calls it once per frame and a settings change
// takes effect on the next draw without rebuilding the tab.
func (a *App) applyEditorPrefs(tab *Tab) {
	if a == nil || tab == nil {
		return
	}
	p := a.mgr.workspacePrefs().editorPolicy()
	p.apply(tab.DocEditor)
	p.apply(tab.bodyEditor)
	p.apply(tab.respEditor)
}

// --- auto-save ------------------------------------------------------------

// startAutoSaveTicker starts the one-second decision tick for safe auto-save.
// The tick only runs while the window is alive; AutoSaveTick itself is the
// authority that refuses conflicts and externally deleted files.
func (a *App) startAutoSaveTicker() {
	if a == nil || a.autoSaveStop != nil || a.win == nil {
		return
	}
	stop := make(chan struct{})
	a.autoSaveStop = stop
	a.autoSaveLast = time.Now()
	go func() {
		t := time.NewTicker(autoSaveTickInterval)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				win := a.win
				if win == nil || win.IsDestroyed() {
					return
				}
				win.Update(func() {
					if a.closed {
						return
					}
					a.autoSaveStep(time.Now())
				})
			}
		}
	}()
}

func (a *App) stopAutoSaveTicker() {
	if a == nil || a.autoSaveStop == nil {
		return
	}
	close(a.autoSaveStop)
	a.autoSaveStop = nil
}

// autoSaveStep is the timer's UI-thread body, kept separate so tests can drive
// the policy with an explicit clock. It returns whether a save attempt ran.
func (a *App) autoSaveStep(now time.Time) bool {
	if a == nil || a.ps == nil || a.mgr == nil {
		return false
	}
	prefs := a.mgr.workspacePrefs()
	if !prefs.autoSaveEnabled() {
		return false
	}
	if a.autoSaveLast.IsZero() {
		a.autoSaveLast = now
		return false
	}
	if now.Sub(a.autoSaveLast) < prefs.autoSaveDelay() {
		return false
	}
	a.autoSaveLast = now
	saved, skipped := a.AutoSaveTick()
	if skipped > 0 {
		a.status = translate(a.msg().AutoSaveSkipped, skipped)
	} else if saved > 0 {
		a.status = translate(a.msg().AutoSaveSaved, saved)
	}
	return true
}

// shortcutOn reports whether the user left a shortcut enabled in settings.
func (a *App) shortcutOn(id string) bool {
	return a != nil && a.mgr != nil && a.mgr.workspacePrefs().shortcutEnabled(id)
}

// --- settings UI ----------------------------------------------------------

type shortcutDef struct {
	ID    string
	Label string
	Keys  string
}

func shortcutDefs(m *messages) []shortcutDef {
	return []shortcutDef{
		{shortcutNewRequest, m.ShortcutNewRequest, "⌘T"},
		{shortcutCloseTab, m.ShortcutCloseTab, "⌘W"},
		{shortcutTabSwitch, m.ShortcutTabSwitch, "⌘1–9"},
		{shortcutFocusURL, m.ShortcutFocusURL, "⌘L"},
		{shortcutQuickSearch, m.ShortcutQuickSearch, "⌘K"},
		{shortcutEditEnv, m.ShortcutEditEnv, "⌘E"},
		{shortcutToggleSidebar, m.ShortcutToggleSidebar, "⌘\\"},
		{shortcutUndoDelete, m.ShortcutUndoDelete, "⌘Z"},
		{shortcutRename, m.ShortcutRename, "F2"},
	}
}

func fontSizeChoices() []string {
	return []string{"11", "12", "13", "14", "15", "16", "18", "20"}
}

func tabSizeChoices() []string {
	return []string{"2", "3", "4", "8"}
}

func delayChoices() []string {
	return []string{"2", "5", "10", "30"}
}

// settingsGroups draws the B02 preference groups inside the existing settings
// sheet. The caller passes the same palette and status sink as the rest of the
// sheet.
func (s *workspacePrefsStore) settingsGroups(c *ui.Context, p uikit.Palette, m *messages, setStatus func(string)) {
	settingsGroup(c, p, m.EditorSection, func() {
		ui.Column(c).Gap(6).FillWidth().Children(func() {
			ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
				ui.Text(c, m.FontSizeLabel).FontSize(12).TextColor(p.Muted).Width(150)
				cur := strconv.Itoa(s.fontSize())
				if uikit.ActionCursor(uikit.Select(c, &cur, fontSizeChoices(), uikit.SameIcon(uikit.IconType))).Width(90).Changed() {
					if n, err := strconv.Atoi(cur); err == nil {
						s.setFontSize(n)
						if setStatus != nil {
							setStatus(translate(m.FontSizeStatus, n))
						}
					}
				}
			})
			ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
				ui.Text(c, m.TabSizeLabel).FontSize(12).TextColor(p.Muted).Width(150)
				cur := strconv.Itoa(s.tabSize())
				if uikit.ActionCursor(uikit.Select(c, &cur, tabSizeChoices(), uikit.SameIcon(uikit.IconList))).Width(90).Changed() {
					if n, err := strconv.Atoi(cur); err == nil {
						s.setTabSize(n)
					}
				}
			})
			wrap := s.softWrap()
			if uikit.ActionCursor(ui.Checkbox(c, &wrap, m.SoftWrapLabel)).Changed() {
				s.setSoftWrap(wrap)
			}
			nums := s.lineNumbers()
			if uikit.ActionCursor(ui.Checkbox(c, &nums, m.LineNumbersLabel)).Changed() {
				s.setLineNumbers(nums)
			}
		})
	})

	settingsGroup(c, p, m.AutoSaveSection, func() {
		ui.Column(c).Gap(6).FillWidth().Children(func() {
			on := s.autoSaveEnabled()
			if uikit.ActionCursor(ui.Checkbox(c, &on, m.AutoSaveEnable)).Changed() {
				s.setAutoSaveEnabled(on)
				if setStatus != nil {
					if on {
						setStatus(translate(m.AutoSaveOnStatus, delaySecondsLabel(s.autoSaveDelay())))
					} else {
						setStatus(m.AutoSaveOffStatus)
					}
				}
			}
			ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
				ui.Text(c, m.AutoSaveDelayLabel).FontSize(12).TextColor(p.Muted).Width(150)
				cur := delaySecondsLabel(s.autoSaveDelay())
				if uikit.ActionCursor(uikit.Select(c, &cur, delayChoices(), uikit.SameIcon(uikit.IconClock))).Width(90).Changed() {
					if n, err := strconv.Atoi(cur); err == nil {
						s.setAutoSaveDelaySeconds(n)
					}
				}
			})
			ui.Text(c, m.AutoSaveSafetyNote).FontSize(11).TextColor(p.Faint)
		})
	})

	settingsGroup(c, p, m.ShortcutsSection, func() {
		ui.Column(c).Gap(4).FillWidth().Children(func() {
			for _, def := range shortcutDefs(m) {
				def := def
				on := s.shortcutEnabled(def.ID)
				label := fmt.Sprintf("%s  (%s)", def.Label, def.Keys)
				if uikit.ActionCursor(ui.Checkbox(c, &on, label)).Changed() {
					s.setShortcutEnabled(def.ID, on)
				}
			}
			ui.Text(c, m.ShortcutsNote).FontSize(11).TextColor(p.Faint)
		})
	})
}

func delaySecondsLabel(d time.Duration) string {
	return strconv.Itoa(int(d / time.Second))
}
