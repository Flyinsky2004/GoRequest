package desktop

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/egoist/mygo/ui"

	"gorequest/internal/core"
	"gorequest/internal/desktop/uikit"
)

// kvKind selects which suggestion set a key/value form uses, so query keys and
// HTTP header names never share a suggestion list.
type kvKind int

const (
	kvQuery kvKind = iota
	kvHeader
)

// KVRow is one editable row of the Query or Headers form. ID is a stable,
// unique identity used as the row's UI key: editing, the suggestion popup and
// focus stay on the same logical row even when rows above it are added or
// deleted, instead of leaking into a neighbouring row.
type KVRow struct {
	ID      string
	Enabled bool
	Name    string
	Value   string
}

var rowSeq atomic.Uint64

func nextRowID() string { return "kvrow-" + strconv.FormatUint(rowSeq.Add(1), 10) }

func newBlankRow() *KVRow { return &KVRow{ID: nextRowID(), Enabled: true} }

// isBlank reports whether the row holds no meaningful data, so it can serve as
// the trailing "add a row" affordance without ever producing an empty key.
func (r *KVRow) isBlank() bool {
	return strings.TrimSpace(r.Name) == "" && strings.TrimSpace(r.Value) == ""
}

// rowsFromKV builds form rows from stored ordered KVs. Order, duplicate keys,
// empty values, disabled rows and values are all preserved verbatim — colons,
// equals signs, meaningful spaces, {{variable}} and {{secret.name}} included.
// One blank trailing row is appended as the affordance to add another.
func rowsFromKV(kvs []core.KV) []*KVRow {
	rows := make([]*KVRow, 0, len(kvs)+1)
	for _, kv := range kvs {
		rows = append(rows, &KVRow{
			ID:      nextRowID(),
			Enabled: kv.IsEnabled(),
			Name:    kv.Name,
			Value:   kv.Value,
		})
	}
	rows = append(rows, newBlankRow())
	return rows
}

// validateRows reports the first incomplete row for inline feedback while
// editing, in English. It backs the pure conversion helpers; the view uses
// validateRowsFor with the current language catalog.
func validateRows(rows []*KVRow) error { return validateRowsFor(englishMessages, rows) }

// validateRowsFor localizes the first incomplete-row message while editing. A
// row with a value but no key is incomplete; blank rows are fine.
func validateRowsFor(m *messages, rows []*KVRow) error {
	for i, r := range rows {
		if r == nil {
			continue
		}
		if strings.TrimSpace(r.Name) == "" && strings.TrimSpace(r.Value) != "" {
			return fmt.Errorf(m.RowNoKey, i+1, r.Value)
		}
	}
	return nil
}

// kvFromRows converts form rows back to ordered core.KV pairs. Fully blank rows
// — including an untouched trailing row — are dropped so they never produce an
// empty key on save or send. A value without a key is reported as an error
// rather than silently discarded.
func kvFromRows(rows []*KVRow) ([]core.KV, error) {
	if err := validateRows(rows); err != nil {
		return nil, err
	}
	out := make([]core.KV, 0, len(rows))
	for _, r := range rows {
		if r == nil || r.isBlank() {
			continue
		}
		enabled := r.Enabled
		out = append(out, core.KV{Name: r.Name, Value: r.Value, Enabled: &enabled})
	}
	return out, nil
}

// headerKeySuggestions are common HTTP header names offered in the Headers Key
// field. They are suggestions only; nothing is injected into a request unless
// the user puts it in a row.
var headerKeySuggestions = []string{
	"Accept", "Authorization", "Content-Type", "User-Agent", "Cache-Control",
	"Accept-Language", "Accept-Encoding", "Origin", "Referer", "X-Request-ID",
	"X-API-Key",
}

// queryKeySuggestions are common query parameter names, kept separate from the
// header suggestions.
var queryKeySuggestions = []string{
	"page", "page_size", "limit", "offset", "q", "search", "sort", "order",
	"filter", "fields",
}

// contentTypeSuggestions are value hints for Content-Type and Accept. Free text
// is always allowed.
var contentTypeSuggestions = []string{
	"application/json", "text/plain", "application/xml",
	"application/x-www-form-urlencoded", "multipart/form-data",
}

// suggestionsFor returns the Key suggestions for a form. Query keys and header
// names are deliberately disjoint sets.
func suggestionsFor(kind kvKind) []string {
	if kind == kvHeader {
		return headerKeySuggestions
	}
	return queryKeySuggestions
}

// valueSuggestions returns Value hints for a row: only Content-Type and Accept
// (case-insensitively) suggest media types; everything else allows free text.
func valueSuggestions(name string, kind kvKind) []string {
	if kind != kvHeader {
		return nil
	}
	switch {
	case strings.EqualFold(strings.TrimSpace(name), "Content-Type"),
		strings.EqualFold(strings.TrimSpace(name), "Accept"):
		return contentTypeSuggestions
	default:
		return nil
	}
}

// filterSuggestions applies the suggestion filter contract: case-insensitive,
// those starting with the query first, then those merely containing it; all
// options for a blank query. Value hints narrow as the user types.
func filterSuggestions(options []string, query string) []string {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return options
	}
	var starts, contains []string
	for _, o := range options {
		l := strings.ToLower(o)
		switch {
		case strings.HasPrefix(l, q):
			starts = append(starts, o)
		case strings.Contains(l, q):
			contains = append(contains, o)
		}
	}
	return append(starts, contains...)
}

// --- Tab row helpers -----------------------------------------------------

func (t *Tab) rowsPtr(kind kvKind) *[]*KVRow {
	if kind == kvHeader {
		return &t.HeaderRows
	}
	return &t.QueryRows
}

func (t *Tab) rows(kind kvKind) []*KVRow { return *t.rowsPtr(kind) }

func (t *Tab) addRow(kind kvKind) {
	p := t.rowsPtr(kind)
	*p = append(*p, newBlankRow())
}

func (t *Tab) removeRow(kind kvKind, id string) {
	p := t.rowsPtr(kind)
	// Allocate a new slice instead of compacting in place: the frame that is
	// still iterating the old slice keeps its rows intact, so deleting a row
	// never renders a duplicate for the rest of the frame.
	kept := make([]*KVRow, 0, len(*p))
	for _, r := range *p {
		if r.ID != id {
			kept = append(kept, r)
		}
	}
	*p = kept
}

// kvDrag is the payload a row's drag handle carries, so a row dropped onto
// another reorders the list.
type kvDrag struct {
	kind kvKind
	id   string
}

// moveBy swaps a row with its neighbour in the same section (Alt+Up/Down). It
// never moves a real row onto the trailing blank ghost row.
func (t *Tab) moveBy(kind kvKind, id string, delta int) {
	p := t.rowsPtr(kind)
	rows := *p
	i := -1
	for idx, r := range rows {
		if r.ID == id {
			i = idx
			break
		}
	}
	if i < 0 {
		return
	}
	j := i + delta
	if j < 0 || j >= len(rows) || rows[j].isBlank() {
		return
	}
	rows[i], rows[j] = rows[j], rows[i]
}

// moveRowTo moves the row with movingID to sit where targetID currently is.
func (t *Tab) moveRowTo(kind kvKind, movingID, targetID string) {
	if movingID == "" || movingID == targetID {
		return
	}
	p := t.rowsPtr(kind)
	rows := *p
	rest := make([]*KVRow, 0, len(rows))
	var moving *KVRow
	for _, r := range rows {
		if r.ID == movingID {
			moving = r
			continue
		}
		rest = append(rest, r)
	}
	if moving == nil {
		return
	}
	tj := -1
	for i, r := range rest {
		if r.ID == targetID {
			tj = i
			break
		}
	}
	if tj < 0 {
		return
	}
	out := make([]*KVRow, 0, len(rows))
	out = append(out, rest[:tj]...)
	out = append(out, moving)
	out = append(out, rest[tj:]...)
	*p = out
}

// ensureTrailingBlank appends a blank row when the last row holds data, so
// there is always an empty line to type into.
func (t *Tab) ensureTrailingBlank(kind kvKind) {
	p := t.rowsPtr(kind)
	rows := *p
	if len(rows) == 0 || !rows[len(rows)-1].isBlank() {
		*p = append(rows, newBlankRow())
	}
}

// --- view ----------------------------------------------------------------

// KV table density (§6): the rows are a fixed 36 high, and the bordered panel
// around them hugs the content between a floor that keeps a few rows usable
// and a ceiling past which the panel scrolls instead of growing further. The
// inset accounts for the panel's hairline border so a full row is never cut.
const (
	kvRowHeight  float32 = 36
	kvPanelFloor float32 = 108
	kvPanelCeil  float32 = 320
	kvPanelInset float32 = 2
)

// kvPanelHeight is the height of the KV table showing rows rows: it follows the
// content, clamped to [kvPanelFloor, kvPanelCeil], so a short table leaves no
// large bordered blank below it and a long one scrolls inside the panel.
func kvPanelHeight(rows int) float32 {
	if rows < 0 {
		rows = 0
	}
	h := float32(rows)*kvRowHeight + kvPanelInset
	if h < kvPanelFloor {
		return kvPanelFloor
	}
	if h > kvPanelCeil {
		return kvPanelCeil
	}
	return h
}

// kvFocus asks the next frame to focus a cell of row rowID: its Key, or its
// Value when val is set. Tab and Enter both drive the KV keyboard flow through
// this one mechanism.
func (t *Tab) kvFocus(rowID string, val bool) {
	if rowID == "" {
		return
	}
	t.kvFocusID, t.kvFocusVal = rowID, val
}

// kvFocusNextRowKey moves to the next row's Key, or stays put on the trailing
// ghost row where there is no next row.
func (t *Tab) kvFocusNextRowKey(nextID string) {
	if nextID != "" {
		t.kvFocus(nextID, false)
	}
}

// kvFocusPrevRowValue moves to the previous row's Value, or stays put on the
// first row.
func (t *Tab) kvFocusPrevRowValue(prevID string) {
	if prevID != "" {
		t.kvFocus(prevID, true)
	}
}

// kvForm draws a real per-row Key/Value table for Query or Headers: an Enabled
// checkbox, an editable Key with clickable suggestions, an editable Value, and
// a delete button, plus an "Add row" action and inline incomplete-row feedback.
// --- KV batch text mode ------------------------------------------------

// sepFor returns the key/value separator text mode uses: "=" for query pairs,
// ":" for headers.
func sepFor(kind kvKind) string {
	if kind == kvHeader {
		return ":"
	}
	return "="
}

func (t *Tab) textMode(kind kvKind) *bool {
	if kind == kvHeader {
		return &t.headerText
	}
	return &t.queryText
}

func (t *Tab) rawPtr(kind kvKind) *string {
	if kind == kvHeader {
		return &t.headerRaw
	}
	return &t.queryRaw
}

// rowsToText serializes rows to batch-edit text: one "key SEP value" per line,
// disabled rows prefixed "# ". Blank rows are skipped.
func rowsToText(rows []*KVRow, kind kvKind) string {
	sep := sepFor(kind)
	var lines []string
	for _, r := range rows {
		if r == nil || r.isBlank() {
			continue
		}
		line := r.Name + " " + sep + " " + r.Value
		if !r.Enabled {
			line = "# " + line
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

// textToRows parses batch-edit text back into rows, appending a trailing ghost row.
// A line without its separator is a hard error (the caller keeps text mode and
// reports it), so no pair is ever silently dropped.
func textToRows(text string, kind kvKind) ([]*KVRow, error) {
	sep := sepFor(kind)
	var rows []*KVRow
	for i, line := range strings.Split(text, "\n") {
		raw := strings.TrimSpace(line)
		if raw == "" {
			continue
		}
		enabled := true
		if strings.HasPrefix(raw, "#") {
			enabled = false
			raw = strings.TrimSpace(strings.TrimPrefix(raw, "#"))
		}
		idx := strings.Index(raw, sep)
		if idx < 0 {
			return nil, fmt.Errorf("line %d: missing separator %q", i+1, sep)
		}
		name := strings.TrimSpace(raw[:idx])
		val := strings.TrimSpace(raw[idx+1:])
		if name == "" {
			return nil, fmt.Errorf("line %d: missing key", i+1)
		}
		e := enabled
		rows = append(rows, &KVRow{ID: nextRowID(), Enabled: e, Name: name, Value: val})
	}
	rows = append(rows, newBlankRow())
	return rows, nil
}

// autoHeadersFor lists the headers MyGo / net/http add for a request, shown
// read-only at the top of the Headers tab so the user can see what actually
// ships. Host and Content-Length come from the URL and body; the rest are the
// client defaults.
func autoHeadersFor(tab *Tab) [][2]string {
	var out [][2]string
	if u, err := url.Parse(strings.TrimSpace(tab.URL)); err == nil && u.Host != "" {
		out = append(out, [2]string{"Host", u.Host})
	}
	if n := len([]byte(strings.TrimSpace(tab.BodyText))); n > 0 {
		out = append(out, [2]string{"Content-Length", strconv.Itoa(n)})
	}
	out = append(out, [2]string{"Accept-Encoding", "gzip"}, [2]string{"User-Agent", "Go-http-client/1.1"})
	return out
}

func (a *App) kvForm(c *ui.Context, t *ui.Theme, tab *Tab, kind kvKind) {
	m := a.msg()
	p := uikit.PaletteFor(t)
	rows := tab.rows(kind)
	textMode := *tab.textMode(kind)

	// View switch, right-aligned in its own row above the table so it never
	// squeezes the column headers (§6).
	ui.Row(c).Padding(0, 8).AlignItems(ui.Center).Children(func() {
		ui.Box(c).Grow(1)
		idx := 0
		if textMode {
			idx = 1
		}
		seg, changed := uikit.SimpleSegmented(c, p, &idx, []string{m.KeyValueMode, m.TextMode})
		seg.Width(132)
		if changed {
			if idx == 1 {
				*tab.rawPtr(kind) = rowsToText(rows, kind)
				tab.kvErr = ""
				*tab.textMode(kind) = true
			} else if parsed, err := textToRows(*tab.rawPtr(kind), kind); err != nil {
				tab.kvErr = err.Error()
			} else {
				*tab.rowsPtr(kind) = parsed
				tab.kvErr = ""
				*tab.textMode(kind) = false
			}
		}
	})

	// Column headers — shown once.
	ui.Row(c).Gap(8).AlignItems(ui.Center).Padding(0, 8).Children(func() {
		ui.Box(c).Width(14)
		ui.Box(c).Width(16)
		ui.Text(c, m.Key).FontSize(12).FontWeight(600).TextColor(p.Muted).Width(200)
		ui.Text(c, m.Value).FontSize(12).FontWeight(600).TextColor(p.Muted).Grow(1)
		ui.Box(c).Width(28)
	})

	if kind == kvHeader {
		for _, h := range autoHeadersFor(tab) {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Padding(0, 8).Height(30).Children(func() {
				ui.Box(c).Width(14)
				ui.Box(c).Width(16)
				ui.Icon(c, uikit.IconLock).FontSize(12).TextColor(p.Faint)
				ui.Text(c, h[0]).FontSize(12).TextColor(p.Faint).Width(188)
				ui.Text(c, h[1]).FontSize(12).TextColor(p.Faint).Grow(1).SingleLine()
			}).Tooltip(m.AutoHeaderTip)
		}
	}

	if textMode {
		ui.Box(c).Grow(1).FillWidth().Radius(10).BorderWidth(1).BorderColor(p.Hairline).Clip().Children(func() {
			ta := ui.TextArea(c, tab.rawPtr(kind)).Grow(1).FillWidth().Font("monospace")
			if ta.Changed() {
				if parsed, err := textToRows(*tab.rawPtr(kind), kind); err != nil {
					tab.kvErr = err.Error()
				} else {
					*tab.rowsPtr(kind) = parsed
					tab.kvErr = ""
					tab.Dirty = true
				}
			}
		})
		if tab.kvErr != "" {
			ui.Text(c, tab.kvErr).FontSize(12).TextColor(uikit.MethodColor("DELETE", p)).Padding(2, 8)
		}
		return
	}

	// The table: rounded, hairline-edged, scrolling, hover-washed rows, and a
	// trailing ghost row to type into. Its height follows the row count (§6)
	// rather than filling the pane, so no bordered blank is left behind; past
	// the ceiling the Scroll inside scrolls.
	ui.Box(c).Height(kvPanelHeight(len(rows))).FillWidth().Radius(10).BorderWidth(1).BorderColor(p.Hairline).Clip().Children(func() {
		ui.Scroll(c).Grow(1).FillWidth().Children(func() {
			ui.Column(c).FillWidth().Children(func() {
				prev := ""
				for i, row := range rows {
					next := ""
					if i+1 < len(rows) {
						next = rows[i+1].ID
					}
					a.kvRow(c, t, tab, kind, row, prev, next)
					prev = row.ID
				}
			})
		})
	})

	if err := validateRowsFor(m, rows); err != nil {
		ui.Text(c, err.Error()).FontSize(12).TextColor(uikit.MethodColor("DELETE", p)).Padding(2, 8)
	}
}

func (a *App) kvRow(c *ui.Context, t *ui.Theme, tab *Tab, kind kvKind, row *KVRow, prevID, nextID string) {
	m := a.msg()
	p := uikit.PaletteFor(t)
	blank := row.isBlank()
	r := ui.Row(c).Key(row.ID).Height(36).Gap(8).AlignItems(ui.Center).FillWidth().Padding(0, 8)
	if r.Hovered() {
		r.Background(p.Hover)
	}
	r.Children(func() {
		// Drag handle: only in the default table view (kvRow is never called in
		// text mode), so autocomplete/text views never offer drag (per spec rule).
		if blank {
			ui.Box(c).Width(14)
		} else {
			g := ui.Box(c).Width(14).Height(24).Center()
			g.Children(func() {
				ui.Icon(c, uikit.IconMore).FontSize(12).TextColor(p.Faint)
			})
			g.Drag(kvDrag{kind: kind, id: row.ID}).Tooltip(m.DragTip)
		}
		if ui.Checkbox(c, &row.Enabled, "").Changed() {
			tab.Dirty = true
		}
		keyPh := ""
		if blank {
			keyPh = m.Key
		}
		keyEl := ui.Autocomplete(c, &row.Name, suggestionsFor(kind)).Width(200).Placeholder(keyPh)
		if tab.kvFocusID == row.ID && !tab.kvFocusVal {
			keyEl.Focus()
			tab.kvFocusID = ""
		}
		keyEl.OnShortcut(ui.Alt, ui.KeyUp, func() { tab.moveBy(kind, row.ID, -1); tab.kvFocus(row.ID, false) })
		keyEl.OnShortcut(ui.Alt, ui.KeyDown, func() { tab.moveBy(kind, row.ID, 1); tab.kvFocus(row.ID, false) })
		// Tab walks key → value → next key; Shift+Tab walks back (§6). The
		// autocomplete popup does not take Tab itself, so these always move
		// the focus while the input holds it.
		keyEl.OnShortcut(0, ui.KeyTab, func() { tab.kvFocus(row.ID, true) })
		keyEl.OnShortcut(ui.Shift, ui.KeyTab, func() { tab.kvFocusPrevRowValue(prevID) })
		if keyEl.Changed() {
			tab.Dirty = true
			tab.ensureTrailingBlank(kind)
		}
		valPh := ""
		if blank {
			valPh = m.Value
		}
		vals := filterSuggestions(valueSuggestions(row.Name, kind), row.Value)
		valEl := ui.Autocomplete(c, &row.Value, vals).Grow(1).Placeholder(valPh)
		if tab.kvFocusID == row.ID && tab.kvFocusVal {
			valEl.Focus()
			tab.kvFocusID = ""
		}
		valEl.OnShortcut(ui.Alt, ui.KeyUp, func() { tab.moveBy(kind, row.ID, -1); tab.kvFocus(row.ID, false) })
		valEl.OnShortcut(ui.Alt, ui.KeyDown, func() { tab.moveBy(kind, row.ID, 1); tab.kvFocus(row.ID, false) })
		// Tab leaves the Value for the next row's Key; Shift+Tab goes back to
		// this row's Key (§6).
		valEl.OnShortcut(0, ui.KeyTab, func() { tab.kvFocusNextRowKey(nextID) })
		valEl.OnShortcut(ui.Shift, ui.KeyTab, func() { tab.kvFocus(row.ID, false) })
		if valEl.Changed() {
			tab.Dirty = true
			tab.ensureTrailingBlank(kind)
		}
		// Enter in the value jumps to the next row's key (D8 keyboard flow).
		valEl.OnShortcut(0, ui.KeyEnter, func() { tab.kvFocusNextRowKey(nextID) })
		if blank {
			ui.Box(c).Width(28)
		} else if ui.Button(c, "✕").Clicked() {
			tab.removeRow(kind, row.ID)
			tab.ensureTrailingBlank(kind)
			tab.Dirty = true
		}
	})
	// Drop target: a row dropped onto this one reorders the list.
	if src, ok := ui.Drop[kvDrag](r); ok && src.kind == kind && src.id != row.ID {
		tab.moveRowTo(kind, src.id, row.ID)
		tab.Dirty = true
	}
}
