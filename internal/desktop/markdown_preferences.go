package desktop

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
)

// markdownPreferences affect rendered documents only. Zero/invalid fields in
// old or hand-edited local workspace.json files resolve independently.
type markdownPreferences struct {
	FontPercent int `json:"fontPercent"`
	Padding     int `json:"padding"`
	LinePercent int `json:"linePercent"`
}

func defaultMarkdownPreferences() markdownPreferences {
	return markdownPreferences{100, 24, 150}
}

func (p markdownPreferences) normalized() markdownPreferences {
	d := defaultMarkdownPreferences()
	if p.FontPercent < 75 || p.FontPercent > 200 {
		p.FontPercent = d.FontPercent
	}
	if p.Padding < 8 || p.Padding > 64 {
		p.Padding = d.Padding
	}
	if p.LinePercent < 120 || p.LinePercent > 220 {
		p.LinePercent = d.LinePercent
	}
	return p
}

func (s *workspacePrefsStore) markdownPolicy() markdownPreferences {
	if s == nil {
		return defaultMarkdownPreferences()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cfg.Markdown.normalized()
}

// setMarkdownPolicy commits atomically and reports failures; failed changes do
// not silently become memory-only preferences. Existing B02 setters are untouched.
func (s *workspacePrefsStore) setMarkdownPolicy(p markdownPreferences) error {
	if p != p.normalized() {
		return fmt.Errorf("invalid Markdown preferences")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	previous := s.cfg.Markdown
	s.cfg.Markdown = p
	if err := s.saveMarkdownLocked(); err != nil {
		s.cfg.Markdown = previous
		return err
	}
	return nil
}

func (s *workspacePrefsStore) saveMarkdownLocked() error {
	if s.path == "" {
		return nil
	}
	data, err := json.MarshalIndent(s.cfg, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".workspace-markdown-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(append(data, '\n')); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Chmod(f.Name(), 0o644); err != nil {
		return err
	}
	return os.Rename(f.Name(), s.path)
}

// Only override body size and line height: MyGo keeps heading/list/table/code
// ratios in ems and invalidates its own text caches when the theme changes.
func (p markdownPreferences) theme(t *ui.Theme) *markdown.Theme {
	p = p.normalized()
	size := t.FontSize
	if size == 0 {
		size = 14
	}
	return &markdown.Theme{FontSize: size * float32(p.FontPercent) / 100, LineHeight: float32(p.LinePercent) / 100}
}

// Settings run on the UI thread. Schedule a draw for each live project window,
// including previews behind another window, without reopening any document.
func (m *Manager) invalidateMarkdownPreviews() {
	if m == nil {
		return
	}
	for _, a := range m.workspaces {
		if a != nil && !a.closed && a.win != nil && !a.win.IsDestroyed() {
			a.win.Update(func() {})
		}
	}
}
