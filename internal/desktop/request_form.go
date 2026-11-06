package desktop

import (
	"fmt"
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

// kvForm draws a real per-row Key/Value table for Query or Headers: an Enabled
// checkbox, an editable Key with clickable suggestions, an editable Value, and
// a delete button, plus an "Add row" action and inline incomplete-row feedback.
func (a *App) kvForm(c *ui.Context, t *ui.Theme, tab *Tab, kind kvKind) {
	m := a.msg()
	p := uikit.PaletteFor(t)
	rows := tab.rows(kind)

	// Column headers.
	ui.Row(c).Gap(8).AlignItems(ui.Center).Padding(0, 8).Children(func() {
		ui.Box(c).Width(28)
		ui.Text(c, m.Key).FontSize(12).FontWeight(600).TextColor(p.Muted).Width(200)
		ui.Text(c, m.Value).FontSize(12).FontWeight(600).TextColor(p.Muted).Grow(1)
		ui.Box(c).Width(28)
	})

	// The table: rounded, hairline-edged, scrolling, with a hover-washed row per
	// entry and a trailing ghost row to type into.
	ui.Box(c).Grow(1).FillWidth().Radius(10).BorderWidth(1).BorderColor(p.Hairline).Clip().Children(func() {
		ui.Scroll(c).Grow(1).FillWidth().Children(func() {
			ui.Column(c).FillWidth().Children(func() {
				for _, row := range rows {
					a.kvRow(c, t, tab, kind, row)
				}
			})
		})
	})

	if err := validateRowsFor(m, rows); err != nil {
		ui.Text(c, err.Error()).FontSize(12).TextColor(uikit.MethodColor("DELETE", p)).Padding(2, 8)
	}
}

func (a *App) kvRow(c *ui.Context, t *ui.Theme, tab *Tab, kind kvKind, row *KVRow) {
	m := a.msg()
	p := uikit.PaletteFor(t)
	r := ui.Row(c).Key(row.ID).Height(36).Gap(8).AlignItems(ui.Center).FillWidth().Padding(0, 8)
	if r.Hovered() {
		r.Background(p.Hover)
	}
	r.Children(func() {
		if ui.Checkbox(c, &row.Enabled, "").Changed() {
			tab.Dirty = true
		}
		// Key: editable autocomplete; opening the suggestions does not change the
		// key, so it does not mark the request dirty.
		if ui.Autocomplete(c, &row.Name, suggestionsFor(kind)).Width(200).Placeholder(m.Key).Changed() {
			tab.Dirty = true
			tab.ensureTrailingBlank(kind)
		}
		vals := filterSuggestions(valueSuggestions(row.Name, kind), row.Value)
		if ui.Autocomplete(c, &row.Value, vals).Grow(1).Placeholder(m.Value).Changed() {
			tab.Dirty = true
			tab.ensureTrailingBlank(kind)
		}
		if row.isBlank() {
			ui.Box(c).Width(28)
		} else if ui.Button(c, "✕").Clicked() {
			tab.removeRow(kind, row.ID)
			tab.ensureTrailingBlank(kind)
			tab.Dirty = true
		}
	})
}
