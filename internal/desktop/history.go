package desktop

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"gorequest/internal/core"
	"gorequest/internal/desktop/uikit"
)

// Request history (§3): one metadata record per send, stored locally (never in
// the project dir or Git). Plan A: metadata only — no response bodies.

type historyRecord struct {
	Ts         time.Time `json:"ts"`
	Method     string    `json:"method"`
	URL        string    `json:"url"`
	Env        string    `json:"env,omitempty"`
	Status     int       `json:"status"`
	DurationMs int64     `json:"durationMs"`
	SizeBytes  int64     `json:"size"`
	RequestID  string    `json:"requestId,omitempty"`
}

// historyStore reads and appends JSONL under a local data directory, one file per
// project. An empty dir disables it (used in headless tests unless set up).
type historyStore struct {
	dir string
}

func defaultHistoryDir() string {
	base, err := os.UserConfigDir()
	if err != nil || base == "" {
		return ""
	}
	return filepath.Join(base, "GoRequest", "history")
}

func newHistoryStore() *historyStore { return &historyStore{dir: defaultHistoryDir()} }

func (s *historyStore) path(key string) string {
	if s == nil || s.dir == "" || key == "" {
		return ""
	}
	return filepath.Join(s.dir, key+".jsonl")
}

// historyKey derives a per-project file key from the project id and a hash of
// the normalized root, so two clones of the same repo never share history.
func historyKey(ps *projectState) string {
	if ps == nil {
		return ""
	}
	id := "proj"
	if ps.project != nil && ps.project.File != nil && strings.TrimSpace(ps.project.File.ID) != "" {
		id = core.Slug(ps.project.File.ID)
	}
	sum := sha256.Sum256([]byte(ps.root))
	return id + "-" + hex.EncodeToString(sum[:])[:8]
}

func (s *historyStore) load(key string) []historyRecord {
	p := s.path(key)
	if p == "" {
		return nil
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	var out []historyRecord
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var r historyRecord
		if json.Unmarshal([]byte(line), &r) == nil {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Ts.After(out[j].Ts) })
	return out
}

func (s *historyStore) append(key string, r historyRecord) error {
	p := s.path(key)
	if p == "" {
		return nil
	}
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(b, '\n'))
	return err
}

func (s *historyStore) writeAll(key string, recs []historyRecord) error {
	p := s.path(key)
	if p == "" {
		return nil
	}
	if len(recs) == 0 {
		return s.clear(key)
	}
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}
	var b strings.Builder
	for _, r := range recs {
		line, err := json.Marshal(r)
		if err != nil {
			return err
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	return os.WriteFile(p, []byte(b.String()), 0o600)
}

func (s *historyStore) remove(key string, ts time.Time) {
	recs := s.load(key)
	kept := recs[:0]
	for _, r := range recs {
		if !r.Ts.Equal(ts) {
			kept = append(kept, r)
		}
	}
	_ = s.writeAll(key, kept)
}

func (s *historyStore) clear(key string) error {
	p := s.path(key)
	if p == "" {
		return nil
	}
	err := os.Remove(p)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// --- App integration -----------------------------------------------------

func (a *App) store() *historyStore {
	if a.mgr != nil && a.mgr.history != nil {
		return a.mgr.history
	}
	return &historyStore{dir: ""} // disabled (tests without a configured store)
}

func (a *App) recordHistory(tab *Tab, url string, res *core.ExecResult) {
	if tab == nil || tab.Entry == nil || tab.Entry.Request == nil || res == nil {
		return
	}
	rec := historyRecord{
		Ts:         time.Now(),
		Method:     tab.Method,
		URL:        url,
		Env:        a.ps.currentEnv(),
		Status:     res.Status,
		DurationMs: res.Duration.Microseconds() / 1000,
		SizeBytes:  res.BodySize,
		RequestID:  tab.Entry.Request.ID,
	}
	_ = a.store().append(historyKey(a.ps), rec)
	if a.historyView {
		a.reloadHistory()
	}
}

func (a *App) reloadHistory() {
	key := historyKey(a.ps)
	a.historyRecords = a.store().load(key)
	a.historyKey = key
}

func (a *App) clearHistory() {
	_ = a.store().clear(historyKey(a.ps))
	a.historyRecords = nil
}

// restoreHistory reopens the recorded request (by id) and fills its URL/method
// from the record, as an edit the user can then inspect or resend.
func (a *App) restoreHistory(r historyRecord) {
	if a.ps == nil || a.ps.project == nil {
		return
	}
	if e, ok := a.ps.project.Requests[r.RequestID]; ok && e.Request != nil {
		a.ps.openRequest(e)
	} else if a.ps.activeTab() == nil {
		a.status = "request no longer exists"
		return
	}
	tab := a.ps.activeTab()
	if tab == nil {
		return
	}
	tab.Method = r.Method
	tab.URL = r.URL
	tab.Dirty = true
	a.historyView = false
}

// --- view -----------------------------------------------------------------

func (a *App) historyPanel(c *ui.Context, t *ui.Theme) {
	m := a.msg()
	p := uikit.PaletteFor(t)
	if a.historyKey != historyKey(a.ps) {
		a.reloadHistory()
	}
	ui.Column(c).Grow(1).FillWidth().Children(func() {
		ui.Row(c).Padding(6, 10, 4, 10).AlignItems(ui.Center).Children(func() {
			ui.Text(c, m.HistoryLabel).FontSize(11).FontWeight(600).TextColor(p.Muted).Grow(1)
			if len(a.historyRecords) > 0 {
				if b := ui.Button(c, m.ClearHistory).Clicked(); b {
					a.historyClearOpen = true
				}
			}
		})
		if len(a.historyRecords) == 0 {
			ui.Text(c, m.HistoryEmpty).FontSize(12).TextColor(p.Faint).Padding(4, 10)
			return
		}
		ui.Scroll(c).Grow(1).FillWidth().Children(func() {
			ui.Column(c).FillWidth().Children(func() {
				lastDay := ""
				for _, r := range a.historyRecords {
					r := r
					if d := dayKey(r.Ts); d != lastDay {
						lastDay = d
						ui.Text(c, dayLabel(r.Ts, m)).FontSize(11).FontWeight(600).TextColor(p.Faint).Padding(6, 10)
					}
					a.historyRow(c, p, r)
				}
			})
		})
	})
}

func (a *App) historyRow(c *ui.Context, p uikit.Palette, r historyRecord) {
	row := ui.Row(c).Height(30).FillWidth().AlignItems(ui.Center).Padding(0, 8).Gap(6).Clip()
	if row.Hovered() {
		row.Background(p.Hover)
	}
	if row.Clicked() {
		a.restoreHistory(r)
	}
	row.Children(func() {
		ui.Text(c, r.Ts.Format("15:04")).FontSize(11).FontFeatures("tnum").TextColor(p.Faint).Width(38).SingleLine()
		ui.Text(c, r.Method).Font("monospace").FontWeight(700).FontSize(11).
			TextColor(uikit.MethodColor(r.Method, p)).Width(40).SingleLine()
		ui.Text(c, r.URL).FontSize(12).TextColor(p.Muted).Grow(1).SingleLine()
		if r.Status > 0 {
			uikit.StatusPill(c, p, r.Status)
		}
		ui.Text(c, fmt.Sprintf("%d ms", r.DurationMs)).FontSize(11).FontFeatures("tnum").TextColor(p.Faint).SingleLine()
		if row.Hovered() {
			del := ui.Button(c, "✕")
			del.Absolute().Right(2).Top(-2)
			if del.Clicked() {
				a.store().remove(historyKey(a.ps), r.Ts)
				a.reloadHistory()
			}
		}
	})
}

func (a *App) historyClearModal(c *ui.Context, t *ui.Theme) {
	if !a.historyClearOpen {
		return
	}
	m := a.msg()
	p := uikit.PaletteFor(t)
	open := true
	ui.Modal(c, &open, func() {
		ui.Column(c).Gap(12).MinWidth(380).Children(func() {
			ui.Text(c, m.HistoryLabel).FontSize(16).Bold().TextColor(p.Text)
			ui.Text(c, m.ConfirmClearHistory).FontSize(12).TextColor(p.Muted)
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if ui.Button(c, m.Cancel).Clicked() {
					a.historyClearOpen = false
					open = false
				}
				if ui.PrimaryButton(c, m.ClearHistory).Clicked() {
					a.clearHistory()
					a.historyClearOpen = false
					open = false
				}
			})
		})
	})
	if !open {
		a.historyClearOpen = false
	}
}

func dayKey(ts time.Time) string { return ts.Format("2006-01-02") }

func dayLabel(ts time.Time, m *messages) string {
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	day := time.Date(ts.Year(), ts.Month(), ts.Day(), 0, 0, 0, 0, now.Location())
	switch {
	case day.Equal(today):
		return m.Today
	case day.Equal(today.AddDate(0, 0, -1)):
		return m.Yesterday
	default:
		return dayKey(ts)
	}
}
