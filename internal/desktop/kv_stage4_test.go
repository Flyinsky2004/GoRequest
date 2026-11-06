package desktop

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

// kvTabFixture opens a project whose Params tab holds rows, focused on the
// requested cell through the same "focus next frame" mechanism Tab and Enter
// use.
func kvTabFixture(t *testing.T, rows []*KVRow, focusID string, focusVal bool) (*Tab, *ui.Tester) {
	t.Helper()
	a, ps := openTestProject(t, "KVStage4")
	tab := openRequestTabs(t, ps, "Alpha")[0]
	tab.Section = 0 // Params
	tab.QueryRows = rows
	tab.kvFocusID = focusID
	tab.kvFocusVal = focusVal
	tt := ui.NewTester(a.View, 1100, 820)
	tt.Frame()
	return tab, tt
}

// TestKVTabKeyToValue verifies the first leg of the §6 keyboard flow: Tab in a
// Key cell moves the caret to that row's Value, so typed text lands in Value
// and leaves the Key untouched.
func TestKVTabKeyToValue(t *testing.T) {
	row := &KVRow{ID: "row-a", Enabled: true, Name: "seed"}
	_, tt := kvTabFixture(t, []*KVRow{row, newBlankRow()}, row.ID, false)

	tt.Key(0, ui.KeyTab)
	tt.Frame()
	tt.Type("abc")
	tt.Frame()

	if row.Value != "abc" {
		t.Fatalf("typed text did not reach Value: value=%q", row.Value)
	}
	if row.Name != "seed" {
		t.Fatalf("Key must keep its text, got %q", row.Name)
	}
}

// TestKVTabValueToNextRowKey verifies the second leg: Tab in a Value cell moves
// to the next row's Key, not its Value.
func TestKVTabValueToNextRowKey(t *testing.T) {
	first := &KVRow{ID: "row-a", Enabled: true, Name: "k1", Value: "v1"}
	second := newBlankRow()
	tab, tt := kvTabFixture(t, []*KVRow{first, second}, first.ID, true)

	tt.Key(0, ui.KeyTab)
	tt.Frame()
	tt.Type("next")
	tt.Frame()

	if second.Name != "next" {
		t.Fatalf("Tab from Value should focus the next row's Key, got name=%q value=%q", second.Name, second.Value)
	}
	if second.Value != "" {
		t.Fatalf("Tab from Value must not focus the next Value: %q", second.Value)
	}
	if first.Name != "k1" || first.Value != "v1" {
		t.Fatalf("previous row changed: %+v", first)
	}
	// Typing in the trailing ghost Key keeps a fresh ghost row available.
	if n := len(tab.QueryRows); n != 3 {
		t.Fatalf("rows = %d, want 3 (two data rows + ghost)", n)
	}
}

// TestKVShiftTabValueToKey verifies the reverse leg: Shift+Tab in a Value cell
// returns to the same row's Key.
func TestKVShiftTabValueToKey(t *testing.T) {
	row := &KVRow{ID: "row-a", Enabled: true, Name: "k", Value: "v"}
	_, tt := kvTabFixture(t, []*KVRow{row, newBlankRow()}, row.ID, true)

	tt.Key(ui.Shift, ui.KeyTab)
	tt.Frame()
	tt.Type("x")
	tt.Frame()

	if row.Value != "v" {
		t.Fatalf("Shift+Tab from Value must return to Key; value changed to %q", row.Value)
	}
	if !strings.Contains(row.Name, "x") {
		t.Fatalf("typed text did not reach Key: name=%q", row.Name)
	}
}

// TestKVTabFirstCellShiftTabStays verifies the flow never leaves the table: on
// the first row's Key, Shift+Tab has no previous cell to go to.
func TestKVTabFirstCellShiftTabStays(t *testing.T) {
	row := &KVRow{ID: "row-a", Enabled: true, Name: "k", Value: "v"}
	_, tt := kvTabFixture(t, []*KVRow{row, newBlankRow()}, row.ID, false)

	tt.Key(ui.Shift, ui.KeyTab)
	tt.Frame()
	tt.Type("x")
	tt.Frame()

	if row.Name != "kx" && row.Name != "xk" {
		t.Fatalf("Shift+Tab on the first Key should keep the focus there, name=%q", row.Name)
	}
	if row.Value != "v" {
		t.Fatalf("Shift+Tab must not move to a previous row's Value: %q", row.Value)
	}
}

// TestKVPanelHeightFollowsRows verifies §6 density: the bordered table hugs its
// rows between a usable floor and a scroll-inducing ceiling instead of filling
// the pane with bordered blank space.
func TestKVPanelHeightFollowsRows(t *testing.T) {
	cases := []struct {
		rows int
		want float32
	}{
		{0, kvPanelFloor},
		{1, kvPanelFloor},
		{2, kvPanelFloor},
		{3, 3*kvRowHeight + kvPanelInset},
		{6, 6*kvRowHeight + kvPanelInset},
		{8, 8*kvRowHeight + kvPanelInset},
		{9, kvPanelCeil},
		{100, kvPanelCeil},
	}
	for _, c := range cases {
		if got := kvPanelHeight(c.rows); got != c.want {
			t.Errorf("kvPanelHeight(%d) = %v, want %v", c.rows, got, c.want)
		}
	}
	// Height grows with the content between the bounds, so the table never
	// leaves a fixed empty box as rows are added.
	if kvPanelHeight(4) <= kvPanelHeight(3) {
		t.Errorf("panel height must grow with rows: 4 rows=%v, 3 rows=%v", kvPanelHeight(4), kvPanelHeight(3))
	}
	if kvPanelHeight(8) > kvPanelCeil {
		t.Errorf("panel height %v exceeds the ceiling %v", kvPanelHeight(8), kvPanelCeil)
	}
}
