package desktop

import (
	"math"
	"testing"

	"github.com/egoist/mygo/ui"

	"gorequest/internal/core"
)

// countKVHeaderText counts exact matches of want in texts. The KV headers are compared
// exactly so the "Key:Value" mode label is not mistaken for the "Key" heading.
func countKVHeaderText(texts []string, want string) int {
	n := 0
	for _, s := range texts {
		if s == want {
			n++
		}
	}
	return n
}

// assertKVHeadersOnce checks the shared acceptance for every KV table: the
// "Key" and "Value" headings appear exactly once, on one line, in that order.
func assertKVHeadersOnce(t *testing.T, tt *ui.Tester, m *messages) {
	t.Helper()
	texts := tt.Texts()
	if got := countKVHeaderText(texts, m.Key); got != 1 {
		t.Fatalf("Key heading appears %d times, want 1; texts=%v", got, texts)
	}
	if got := countKVHeaderText(texts, m.Value); got != 1 {
		t.Fatalf("Value heading appears %d times, want 1; texts=%v", got, texts)
	}
	key, ok := tt.Find(m.Key)
	if !ok {
		t.Fatal("Key heading not found")
	}
	val, ok := tt.Find(m.Value)
	if !ok {
		t.Fatal("Value heading not found")
	}
	if key.X >= val.X {
		t.Fatalf("Key must sit left of Value: key=%+v value=%+v", key, val)
	}
	if math.Abs(float64(key.Y+key.H/2-(val.Y+val.H/2))) > 0.5 {
		t.Fatalf("Key and Value headings are not on one line: key=%+v value=%+v", key, val)
	}
}

// TestKVGhostPlaceholdersDoNotRepeatHeaders pins the R3 choice: the trailing
// ghost row hints "add parameter" in its Key and leaves Value empty, so the
// header words are never printed a second time as placeholders. Rows that hold
// data keep no placeholder.
func TestKVGhostPlaceholdersDoNotRepeatHeaders(t *testing.T) {
	key, val := kvCellPlaceholders(englishMessages, true)
	if key != englishMessages.AddParamHint || val != "" {
		t.Fatalf("ghost placeholders = (%q, %q), want (%q, \"\")", key, val, englishMessages.AddParamHint)
	}
	if key == englishMessages.Key || val == englishMessages.Value {
		t.Fatalf("ghost placeholders repeat the column headings: (%q, %q)", key, val)
	}
	if k, v := kvCellPlaceholders(englishMessages, false); k != "" || v != "" {
		t.Fatalf("data row placeholders = (%q, %q), want both empty", k, v)
	}
}

// TestKeepSingleTrailingBlank covers the shared ghost-row normalisation used by
// Params, Headers and the Body form.
func TestKeepSingleTrailingBlank(t *testing.T) {
	rows := []*KVRow{
		{ID: "a", Enabled: true, Name: "a", Value: "1"},
		{ID: "b", Enabled: true},
		{ID: "c", Enabled: true},
	}
	got := keepSingleTrailingBlank(rows, isBlankKVRow, newBlankRow)
	if len(got) != 2 || got[0].ID != "a" || got[1].ID != "c" || !got[1].isBlank() {
		t.Fatalf("deduplicated rows = %+v, want [a, last blank c]", got)
	}
	again := keepSingleTrailingBlank(got, isBlankKVRow, newBlankRow)
	if len(again) != 2 || again[0] != got[0] || again[1] != got[1] {
		t.Fatalf("normalisation is not idempotent: %+v then %+v", got, again)
	}

	noGhost := keepSingleTrailingBlank([]*KVRow{{ID: "x", Enabled: true, Name: "x"}}, isBlankKVRow, newBlankRow)
	if len(noGhost) != 2 || noGhost[0].ID != "x" || !noGhost[1].isBlank() {
		t.Fatalf("missing ghost not appended: %+v", noGhost)
	}

	// A blank row in the middle is left alone: the user may be clearing it.
	middle := keepSingleTrailingBlank([]*KVRow{
		{ID: "a", Enabled: true, Name: "a"},
		{ID: "m", Enabled: true},
		{ID: "b", Enabled: true, Name: "b"},
	}, isBlankKVRow, newBlankRow)
	if len(middle) != 4 || middle[1].ID != "m" || !middle[3].isBlank() {
		t.Fatalf("middle blank row was not preserved: %+v", middle)
	}
}

// TestKVSharedTablesShowHeadersOnce renders all three shared KV tables and
// checks the R3 acceptance: one "Key" and one "Value" heading, and exactly one
// trailing ghost row even when clearing left several blank rows behind.
func TestKVSharedTablesShowHeadersOnce(t *testing.T) {
	t.Run("params", func(t *testing.T) {
		a, ps := openTestProject(t, "R3 Params")
		tab := openRequestTabs(t, ps, "Alpha")[0]
		tab.Section = 0 // Params
		tab.QueryRows = []*KVRow{
			{ID: nextRowID(), Enabled: true, Name: "alpha", Value: "1"},
			newBlankRow(), newBlankRow(),
		}
		tt := ui.NewTester(a.View, 1240, 820)
		tt.Frame()
		assertKVHeadersOnce(t, tt, a.msg())
		if n := len(tab.QueryRows); n != 2 {
			t.Fatalf("Params rows = %d, want 2 (data + one ghost)", n)
		}
	})

	t.Run("headers", func(t *testing.T) {
		a, ps := openTestProject(t, "R3 Headers")
		tab := openRequestTabs(t, ps, "Alpha")[0]
		tab.Section = 2 // Headers
		tab.HeaderRows = []*KVRow{
			{ID: nextRowID(), Enabled: true, Name: "X-Test", Value: "1"},
			newBlankRow(), newBlankRow(),
		}
		tt := ui.NewTester(a.View, 1240, 820)
		tt.Frame()
		assertKVHeadersOnce(t, tt, a.msg())
		if n := len(tab.HeaderRows); n != 2 {
			t.Fatalf("Headers rows = %d, want 2 (data + one ghost)", n)
		}
	})

	t.Run("body-form", func(t *testing.T) {
		a, ps := openTestProject(t, "R3 Body Form")
		tab := openRequestTabs(t, ps, "Alpha")[0]
		tab.Section = 3 // Body
		bs := ensureBodyState(tab, a)
		bs.modeIdx = uiBodyModeIndex(core.BodyURLEncoded)
		bs.formViewMode = 0
		bs.formRows = []*formBodyRow{
			{ID: nextRowID(), Enabled: true, Name: "alpha", Value: "1"},
			newBlankFormRow(), newBlankFormRow(),
		}
		tt := ui.NewTester(a.View, 1240, 820)
		tt.Frame()
		assertKVHeadersOnce(t, tt, a.msg())
		if n := len(bs.formRows); n != 2 {
			t.Fatalf("Body form rows = %d, want 2 (data + one ghost)", n)
		}
		// The Body form keeps its headings on the mode-switch line.
		seg, ok := tt.Find(a.msg().KeyValueMode)
		if !ok {
			t.Fatalf("Key:Value mode switch not found; texts=%v", tt.Texts())
		}
		key, _ := tt.Find(a.msg().Key)
		if math.Abs(float64(key.Y+key.H/2-(seg.Y+seg.H/2))) > 0.5 {
			t.Fatalf("Body form heading left the mode line: key=%+v switch=%+v", key, seg)
		}
	})
}
