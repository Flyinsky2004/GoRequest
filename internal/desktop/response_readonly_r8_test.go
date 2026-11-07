package desktop

import (
	"image"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"gorequest/internal/core"
)

// r8Fixture renders only the production response editor so gutter geometry and
// line backgrounds can be inspected headlessly at a known origin.
func r8Fixture(t *testing.T) (*App, *Tab, *ui.Tester) {
	t.Helper()
	m, _ := newTestManager(t)
	if err := m.openProject(writeProject(t, "R8")); err != nil {
		t.Fatal(err)
	}
	m.cfg.cfg.Language = string(langEnglish)
	a := m.workspaces[0]
	body := []byte("first line\nsecond line\nthird line")
	tab := &Tab{Owner: a.ps, Resp: &core.ExecResult{
		Status:   200,
		Proto:    "HTTP/1.1",
		Body:     body,
		BodySize: int64(len(body)),
	}}
	tab.respVersion = 1
	tt := ui.NewTester(func(c *ui.Context) {
		editor.View(c, a.responseEditorFor(c.Theme(), tab)).Grow(1).FillWidth()
	}, 400, 200)
	return a, tab, tt
}

func r8Pixel(img *image.RGBA, x, y int) (r, g, b uint8) {
	o := y*img.Stride + x*4
	return img.Pix[o], img.Pix[o+1], img.Pix[o+2]
}

func r8Differs(a, b uint8) bool {
	d := int(a) - int(b)
	return d > 6 || d < -6
}

func r8Same(img *image.RGBA, x1, y1, x2, y2 int) bool {
	r1, g1, b1 := r8Pixel(img, x1, y1)
	r2, g2, b2 := r8Pixel(img, x2, y2)
	return !r8Differs(r1, r2) && !r8Differs(g1, g2) && !r8Differs(b1, b2)
}

// A read-only response must not paint the caret's line: the first line sits
// directly under the tool row, so a grey band there reads as a selected row.
// The first line's background must match the editor's plain background.
func TestR8ResponseFirstLineHasNoActiveLineBackground(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(map[bool]string{false: "light", true: "dark"}[dark], func(t *testing.T) {
			_, _, tt := r8Fixture(t)
			tt.SetDark(dark)
			tt.Frame()
			img := tt.Image()
			// x=300 is right of both the line number and the short text, so
			// only the line background is sampled. y=150 is below the last
			// line and shows the editor's plain background.
			if !r8Same(img, 300, 8, 300, 150) {
				t.Fatalf("dark=%v: first line carries an active-line background", dark)
			}
			// The second line is a control: it must also be plain.
			if !r8Same(img, 300, 28, 300, 150) {
				t.Fatalf("dark=%v: plain line background unexpected", dark)
			}
		})
	}
}

// The gutter keeps right-aligned numbers, is narrower than the old
// number+fold-marker column, and leaves an 8–12 DIP gap before the text.
func TestR8ResponseGutterNarrowAndGap(t *testing.T) {
	m, _ := newTestManager(t)
	if err := m.openProject(writeProject(t, "R8 gutter")); err != nil {
		t.Fatal(err)
	}
	m.cfg.cfg.Language = string(langEnglish)
	a := m.workspaces[0]
	// 100 lines make the last visible number three digits wide, so its ink
	// reaches the number column's right edge and the measured gap is the real
	// column-to-text spacing rather than a narrow glyph's side bearing.
	var b strings.Builder
	for i := 1; i <= 99; i++ {
		b.WriteString("x\n")
	}
	b.WriteString("marker")
	body := []byte(b.String())
	tab := &Tab{Owner: a.ps, Resp: &core.ExecResult{
		Status: 200, Proto: "HTTP/1.1", Body: body, BodySize: int64(len(body)),
	}}
	tab.respVersion = 1
	tt := ui.NewTester(func(c *ui.Context) {
		editor.View(c, a.responseEditorFor(c.Theme(), tab)).Grow(1).FillWidth()
	}, 400, 2100)
	tt.Frame()
	st := tab.respEditor
	if st == nil {
		t.Fatal("response editor missing")
	}
	img := tt.Image()
	y := int(st.LineTop(99)) + 8 // line 100 ("marker"), mid-glyph
	bgR, bgG, bgB := r8Pixel(img, 300, y)

	// Runs of non-background pixels on that line: the line number, then
	// (after the gutter gap) the body text.
	type run struct{ start, end int }
	var runs []run
	in, start := false, 0
	for x := 0; x < 220; x++ {
		r, g, bl := r8Pixel(img, x, y)
		ink := r8Differs(r, bgR) || r8Differs(g, bgG) || r8Differs(bl, bgB)
		switch {
		case ink && !in:
			in, start = true, x
		case !ink && in:
			in = false
			runs = append(runs, run{start, x - 1})
		}
	}
	if in {
		runs = append(runs, run{start, 219})
	}
	if len(runs) < 2 {
		t.Fatalf("expected a line number and body text run, got %+v", runs)
	}
	// The gutter gap is the first wide gap between ink runs; the runs before
	// it are the digits of "100".
	split := -1
	for i := 0; i+1 < len(runs); i++ {
		if runs[i+1].start-runs[i].end-1 >= 6 {
			split = i
			break
		}
	}
	if split < 0 {
		t.Fatalf("could not separate the gutter from the text: %+v", runs)
	}
	digits := runs[:split+1]
	text := runs[split+1]
	// MyGo lays the number column 12 DIP (8 + half a character) from the text
	// origin. The ink measurement adds the text glyph's ~1 DIP left side
	// bearing, so 13 is the pixel-level ceiling for the same 12 DIP column
	// spacing.
	inkGap := text.start - digits[len(digits)-1].end - 1
	if inkGap < 8 || inkGap > 13 {
		t.Fatalf("gutter-to-text gap = %d DIP (ink), want 8–12 column spacing; digits=%+v text=%+v runs=%+v", inkGap, digits, text, runs)
	}
	// The old gutter reserved fold-marker space and put the text at ~59 DIP;
	// the narrowed gutter keeps it at or under 48 DIP.
	if text.start > 48 {
		t.Fatalf("gutter not narrowed: text starts at %d DIP", text.start)
	}
	t.Logf("y=%d digits=%+v text=%+v inkGap=%d", y, digits, text, inkGap)
}

// The read-only options are scoped to the response body: the request body
// editor on the Body tab must stay editable, with its active line and fold
// gutter intact.
func TestR8RequestBodyEditorStaysEditable(t *testing.T) {
	a, ps := openTestProject(t, "R8 body")
	tab := openRequestTabs(t, ps, "Body Req")[0]
	bs := ensureBodyState(tab, a)
	bs.modeIdx = uiBodyModeIndex(core.BodyText)
	bs.bodyMutated = true
	tab.Section = 3 // Body tab

	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()
	if tab.bodyEditor == nil {
		t.Fatal("request body editor was not created")
	}
	if tab.bodyEditor.ReadOnly {
		t.Fatal("request body editor became read-only")
	}
	if tab.bodyEditor.NoActiveLine || tab.bodyEditor.NoFolding {
		t.Fatal("request body editor lost its editing affordances")
	}
}

// Theme refreshes replace only the editor's colors: the read-only options must
// survive, or switching appearance would bring the active-line band back.
func TestR8ReadOnlyFlagsSurviveThemeSwitch(t *testing.T) {
	a, tab, tt := r8Fixture(t)
	tt.Frame()
	st := tab.respEditor
	if st == nil {
		t.Fatal("response editor missing")
	}
	check := func(when string) {
		t.Helper()
		if !st.ReadOnly {
			t.Fatalf("%s: response editor is editable", when)
		}
		if !st.NoActiveLine {
			t.Fatalf("%s: active-line highlight re-enabled", when)
		}
		if !st.NoFolding {
			t.Fatalf("%s: gutter fold-marker space re-enabled", when)
		}
	}
	check("initial")

	a.mgr.cfg.setAppearance("Dark")
	tt.Frame()
	if tab.respEditor != st {
		t.Fatal("theme switch rebuilt the response editor")
	}
	check("dark")
	if !r8Same(tt.Image(), 300, 8, 300, 150) {
		t.Fatal("dark theme brought the first-line band back")
	}

	a.mgr.cfg.setAppearance("Light")
	tt.Frame()
	if tab.respEditor != st {
		t.Fatal("theme switch back rebuilt the response editor")
	}
	check("light")
	if !r8Same(tt.Image(), 300, 8, 300, 150) {
		t.Fatal("light theme brought the first-line band back")
	}
}
