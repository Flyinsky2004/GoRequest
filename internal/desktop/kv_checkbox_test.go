package desktop

import (
	"fmt"
	"testing"

	"github.com/egoist/mygo/ui"

	"gorequest/internal/desktop/uikit"
)

// kvCheckboxTester renders the KV enable checkbox alone so its geometry,
// look and input behaviour can be asserted without the rest of the window.
func kvCheckboxTester(t *testing.T, checked *bool) *ui.Tester {
	t.Helper()
	th := uikit.Theme(uikit.PaletteFor(&ui.Theme{Dark: false}), false)
	p := uikit.PaletteFor(th)
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(th)
		ui.Row(c).Padding(10).Children(func() {
			kvCheckbox(c, p, checked).Label("kv-check")
		})
	}, 120, 60)
	tt.Frame()
	return tt
}

// TestKVCheckboxGeometryAndInput pins R6's geometry and keeps the framework's
// input contract: a 16 DIP box that toggles on click and on Space, and keeps
// the keyboard focus.
func TestKVCheckboxGeometryAndInput(t *testing.T) {
	checked := false
	tt := kvCheckboxTester(t, &checked)

	r, ok := tt.Find("kv-check")
	if !ok {
		t.Fatal("checkbox not found")
	}
	if r.W != kvCheckSide || r.H != kvCheckSide {
		t.Fatalf("checkbox box = %gx%g, want %gx%g", r.W, r.H, kvCheckSide, kvCheckSide)
	}

	if err := tt.Click("kv-check"); err != nil {
		t.Fatalf("click: %v", err)
	}
	if !checked {
		t.Fatal("click did not enable the row")
	}
	tt.Frame()

	// The click leaves the box focused, so Space toggles it back off.
	tt.Key(0, ui.KeySpace)
	if checked {
		t.Fatal("Space did not disable the row")
	}
	tt.Frame()
	if !tt.Focused("kv-check") {
		t.Fatal("checkbox lost the keyboard focus")
	}
}

// TestKVCheckboxNeutralLook samples the box itself: unchecked is an empty box
// with a neutral gray stroke, checked is the accent fill. This is a targeted
// pixel check of the control, not a screenshot comparison.
func TestKVCheckboxNeutralLook(t *testing.T) {
	for _, on := range []bool{false, true} {
		t.Run(fmt.Sprint("on=", on), func(t *testing.T) {
			checked := on
			tt := kvCheckboxTester(t, &checked)
			r, ok := tt.Find("kv-check")
			if !ok {
				t.Fatal("checkbox not found")
			}
			im := tt.Image()
			inside := im.RGBAAt(int(r.X+3), int(r.Y+3))
			reddish := int(inside.R) > int(inside.G)+40 && int(inside.R) > int(inside.B)+40
			if on != reddish {
				t.Fatalf("on=%v inside pixel = %+v, reddish=%v", on, inside, reddish)
			}
			if on {
				return
			}
			stroke := im.RGBAAt(int(r.X), int(r.Y+r.H/2))
			if absInt(int(stroke.R)-int(stroke.G)) > 12 || absInt(int(stroke.G)-int(stroke.B)) > 12 {
				t.Fatalf("unchecked stroke is not neutral gray: %+v", stroke)
			}
		})
	}
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// TestKVCheckboxDrivesEnableSemantics ties the new look to the existing enable
// semantics: toggling the box flips the same Enabled field rowsFromKV/kvFromRows
// read and write, so a disabled row survives the save round-trip.
func TestKVCheckboxDrivesEnableSemantics(t *testing.T) {
	row := &KVRow{ID: nextRowID(), Enabled: false, Name: "off", Value: "x"}
	tt := kvCheckboxTester(t, &row.Enabled)
	if err := tt.Click("kv-check"); err != nil {
		t.Fatalf("click: %v", err)
	}
	if !row.Enabled {
		t.Fatal("checkbox did not flip the row's Enabled field")
	}

	kvs, err := kvFromRows([]*KVRow{row, newBlankRow()})
	if err != nil {
		t.Fatalf("kvFromRows: %v", err)
	}
	if len(kvs) != 1 || !kvs[0].IsEnabled() {
		t.Fatalf("saved KVs = %+v, want one enabled row", kvs)
	}

	// And back off: the disabled state must still be written and read.
	if err := tt.Click("kv-check"); err != nil {
		t.Fatalf("second click: %v", err)
	}
	kvs, err = kvFromRows([]*KVRow{row, newBlankRow()})
	if err != nil {
		t.Fatalf("kvFromRows: %v", err)
	}
	if len(kvs) != 1 || kvs[0].IsEnabled() {
		t.Fatalf("saved KVs = %+v, want one disabled row", kvs)
	}
}
