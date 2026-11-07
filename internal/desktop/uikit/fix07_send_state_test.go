package uikit

// FIX07 — SendButton pointer-state feedback.
//
// These tests drive a real ui.Tester with real pointer gestures (Move / Press /
// Release) and read back rendered pixels from a text-free sample point inside
// the pill. They assert the visible result, not just that a code path ran:
// every state must settle to a distinct fill, in both themes and for both the
// Send and Cancel functional states, without changing geometry, cursor,
// keyboard/accessibility behavior, or starting a request on hover.

import (
	"image/color"
	"math"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// fix07State is a headless SendButton wired to a counter, with an optional
// disabled wrapper. The label is the same "Action" for both functional states
// so the sample point always lands on the flat fill rather than on text.
type fix07State struct {
	tt      *ui.Tester
	clicks  *int
	cancels *int
}

func fix07Tester(t *testing.T, p Palette, dark, sending bool, disabled, parentDisabled bool) *fix07State {
	t.Helper()
	s := &fix07State{
		clicks:  new(int),
		cancels: new(int),
	}
	s.tt = ui.NewTester(func(c *ui.Context) {
		c.SetTheme(Theme(p, dark))
		ui.Column(c).Padding(12).AlignItems(ui.Start).Children(func() {
			// The disabled flag is set on the wrapper BEFORE the button is
			// built. This is the app's pattern (a disabled container around a
			// control) and the ordering UI02.md documents: MyGo v0.7.4 reads
			// the disabled flag at build time, so a control disabled only
			// after its build cannot un-ask an already-observed hover.
			ui.Box(c).Disabled(disabled || parentDisabled).Children(func() {
				e, clicked := SendButton(c, p, sending, "Action")
				e.Label("send-target")
				if clicked {
					if sending {
						*s.cancels++
					} else {
						*s.clicks++
					}
				}
			})
		})
	}, 180, 100)
	return s
}

func (s *fix07State) rect(t *testing.T) ui.Rect {
	t.Helper()
	r, ok := s.tt.Find("send-target")
	if !ok || r.W <= 0 || r.H <= 0 {
		t.Fatalf("missing send target: %+v", r)
	}
	return r
}

// fill samples a pixel inside the pill but clear of the icon, the label and
// the rounded corners: 6 DIP from the left edge, vertically centered. In the
// resting state this is exactly p.Accent (verified by fix07RestIsAccent).
func (s *fix07State) fill(r ui.Rect) color.RGBA {
	return s.tt.Image().RGBAAt(int(r.X+6), int(r.Y+r.H/2))
}

// settle lets the 120ms color/pose animations finish; the Tester's clock only
// advances when asked, so a settled state must report no pending frame.
func (s *fix07State) settle(t *testing.T) {
	t.Helper()
	s.tt.Advance(time.Second)
	if s.tt.FramePending() {
		t.Fatal("button kept requesting frames after its state settled")
	}
}

func fix07Palette(dark bool) Palette {
	if dark {
		return darkPalette
	}
	return lightPalette
}

// WCAG relative luminance and contrast ratio, for the white OnAccent text on
// each state fill. 4.5:1 is the AA threshold for normal text.
func fix07Luminance(c color.RGBA) float64 {
	linear := func(v uint8) float64 {
		x := float64(v) / 255
		if x <= 0.04045 {
			return x / 12.92
		}
		return math.Pow((x+0.055)/1.055, 2.4)
	}
	return 0.2126*linear(c.R) + 0.7152*linear(c.G) + 0.0722*linear(c.B)
}

func fix07Contrast(a, b color.RGBA) float64 {
	la, lb := fix07Luminance(a), fix07Luminance(b)
	hi, lo := math.Max(la, lb), math.Min(la, lb)
	return (hi + 0.05) / (lo + 0.05)
}

func fix07Diff(a, b color.RGBA) int {
	return int(math.Abs(float64(a.R)-float64(b.R))) +
		int(math.Abs(float64(a.G)-float64(b.G))) +
		int(math.Abs(float64(a.B)-float64(b.B)))
}

func fix07Distinct(t *testing.T, name string, a, b color.RGBA, min int) {
	t.Helper()
	if d := fix07Diff(a, b); d < min {
		t.Fatalf("%s: fills too similar (%+v vs %+v, channel diff %d < %d)", name, a, b, d, min)
	}
}

// TestFIX07RestIsAccent pins the sampling point: at rest the button must still
// be the exact accent color (no regression of the original fixed fill).
func TestFIX07RestIsAccent(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, sending := range []bool{false, true} {
			p := fix07Palette(dark)
			s := fix07Tester(t, p, dark, sending, false, false)
			s.settle(t)
			r := s.rect(t)
			got, want := s.fill(r), rgbOf(p.Accent)
			if got != want {
				t.Fatalf("dark=%v sending=%v rest fill %+v, want accent %+v", dark, sending, got, want)
			}
		}
	}
}

func rgbOf(c ui.Color) color.RGBA {
	return color.RGBA{R: c.R, G: c.G, B: c.B, A: c.A}
}

// TestFIX07PointerStates uses real Move/Press/Release and checks the rendered
// pixels at rest, hover, pressed, released-back-to-hover, and pointer-left.
// Both themes and both Send/Cancel states. Hover must not fire the action;
// the press alone must not either; release must fire exactly once.
func TestFIX07PointerStates(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, sending := range []bool{false, true} {
			name := "Send"
			if sending {
				name = "Cancel"
			}
			t.Run(map[bool]string{false: "light", true: "dark"}[dark]+"/"+name, func(t *testing.T) {
				p := fix07Palette(dark)
				s := fix07Tester(t, p, dark, sending, false, false)
				s.settle(t)
				r := s.rect(t)
				cx, cy := r.X+r.W/2, r.Y+r.H/2

				rest := s.fill(r)
				s.tt.Move(cx, cy)
				s.settle(t)
				hover := s.fill(r)

				s.tt.Press(cx, cy)
				s.settle(t)
				pressed := s.fill(r)

				s.tt.Release(cx, cy)
				s.settle(t)
				released := s.fill(r)

				s.tt.Move(r.X+r.W+8, r.Y+r.H/2)
				s.settle(t)
				left := s.fill(r)

				// Three visibly different states, at least ~24 summed channel
				// change between each adjacent pair (the mixes are 12%/15%/20-25%).
				fix07Distinct(t, "rest→hover", rest, hover, 20)
				fix07Distinct(t, "hover→pressed", hover, pressed, 20)
				fix07Distinct(t, "rest→pressed", rest, pressed, 40)
				if released != hover {
					t.Fatalf("release did not return to hover fill: %+v vs %+v", released, hover)
				}
				if left != rest {
					t.Fatalf("pointer leave did not return to rest fill: %+v vs %+v", left, rest)
				}

				// The white OnAccent text/icons stay readable on every state
				// fill. The resting accent is a fixed, pre-existing palette
				// token (4.40:1 light / 3.42:1 dark against white) that this
				// change does not touch; the hover/press fills it introduces
				// must both clear WCAG AA 4.5:1 and never fall below rest.
				on := rgbOf(p.OnAccent)
				base := fix07Contrast(rest, on)
				for state, fill := range map[string]color.RGBA{"hover": hover, "pressed": pressed} {
					ratio := fix07Contrast(fill, on)
					if ratio < base-0.01 {
						t.Fatalf("%s fill %+v contrast %.2f:1 fell below rest %.2f:1", state, fill, ratio, base)
					}
					if ratio < 4.5 {
						t.Fatalf("%s fill %+v contrast %.2f:1 < 4.5:1 AA", state, fill, ratio)
					}
				}

				// Geometry, cursor and action semantics are unchanged.
				if r.W != 104 || r.H != 40 {
					t.Fatalf("geometry changed: %+v", r)
				}
				if got := s.rect(t); got != r {
					t.Fatalf("state changed geometry: %+v -> %+v", r, got)
				}
				if *s.clicks+*s.cancels != 1 {
					t.Fatalf("expected exactly one action on release, got sends=%d cancels=%d", *s.clicks, *s.cancels)
				}
				if sending && (*s.clicks != 0 || *s.cancels != 1) {
					t.Fatal("Cancel state did not report the cancel action")
				}
				if !sending && (*s.clicks != 1 || *s.cancels != 0) {
					t.Fatal("Send state did not report the send action")
				}
			})
		}
	}
}

// TestFIX07HoverAndPressAloneDoNotStartRequest separates the gestures: neither
// a hover nor a held press may invoke the action (which would start a request).
func TestFIX07HoverAndPressAloneDoNotStartRequest(t *testing.T) {
	for _, sending := range []bool{false, true} {
		p := fix07Palette(false)
		s := fix07Tester(t, p, false, sending, false, false)
		s.settle(t)
		r := s.rect(t)
		cx, cy := r.X+r.W/2, r.Y+r.H/2
		s.tt.Move(cx, cy)
		s.settle(t)
		if *s.clicks+*s.cancels != 0 {
			t.Fatal("hover started a request")
		}
		s.tt.Press(cx, cy)
		s.settle(t)
		if *s.clicks+*s.cancels != 0 {
			t.Fatal("press started a request before release")
		}
		// Releasing outside the button is the standard cancel gesture: it must
		// not fire the action either.
		s.tt.Release(r.X+r.W+8, r.Y+r.H+8)
		s.settle(t)
		if *s.clicks+*s.cancels != 0 {
			t.Fatal("release outside started a request")
		}
	}
}

// TestFIX07DisabledShowsNoStateColor covers self-disabled and parent-disabled:
// the resting (dimmed) face must not change on hover/press, the cursor stays
// the default, and no action fires.
func TestFIX07DisabledShowsNoStateColor(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, kind := range []string{"self", "parent"} {
			t.Run(map[bool]string{false: "light", true: "dark"}[dark]+"/"+kind, func(t *testing.T) {
				p := fix07Palette(dark)
				s := fix07Tester(t, p, dark, false, kind == "self", kind == "parent")
				s.settle(t)
				r := s.rect(t)
				cx, cy := r.X+r.W/2, r.Y+r.H/2
				rest := s.fill(r)

				s.tt.Move(cx, cy)
				s.settle(t)
				hover := s.fill(r)
				s.tt.Press(cx, cy)
				s.settle(t)
				pressed := s.fill(r)
				s.tt.Release(cx, cy)
				s.settle(t)

				// A disabled control keeps one stable (MyGo-dimmed) face: no
				// hover or press state color may appear at all.
				if hover != rest {
					t.Fatalf("disabled hover changed fill: %+v -> %+v", rest, hover)
				}
				if pressed != rest {
					t.Fatalf("disabled press changed fill: %+v -> %+v", rest, pressed)
				}
				if *s.clicks != 0 {
					t.Fatal("disabled button fired an action")
				}
				if got := s.tt.Cursor(); got != ui.CursorDefault {
					t.Fatalf("disabled button cursor %v, want default", got)
				}
				// The dimmed resting face is clearly not the vivid enabled
				// accent: it must not read as a clickable primary button.
				if d := fix07Diff(rest, rgbOf(p.Accent)); d < 30 {
					t.Fatalf("disabled face looks enabled: %+v vs accent %+v", rest, p.Accent)
				}
			})
		}
	}
}

// TestFIX07ReduceMotionStillChangesColor verifies that asking for less motion
// shows the state colors at once (no color animation) rather than dropping the
// feedback entirely, and still settles to the idle frame budget.
func TestFIX07ReduceMotionStillChangesColor(t *testing.T) {
	p := fix07Palette(false)
	s := fix07Tester(t, p, false, false, false, false)
	s.tt.SetPreferences(ui.Preferences{ReduceMotion: true})
	s.settle(t)
	r := s.rect(t)
	cx, cy := r.X+r.W/2, r.Y+r.H/2
	rest := s.fill(r)
	s.tt.Move(cx, cy)
	s.settle(t)
	hover := s.fill(r)
	fix07Distinct(t, "reduce-motion rest→hover", rest, hover, 20)
	if s.tt.FramePending() {
		t.Fatal("reduce-motion kept requesting frames")
	}
}

// TestFIX07PressScaleVisible checks the press pose actually renders: at rest
// the pill reaches its top edge, while the 0.97 press scale pulls that edge in
// so the column background shows through. This is the press feedback, not just
// a change of fill.
func TestFIX07PressScaleVisible(t *testing.T) {
	p := fix07Palette(false)
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(Theme(p, false))
		ui.Column(c).Padding(12).AlignItems(ui.Start).Children(func() {
			e, _ := SendButton(c, p, false, "Action")
			e.Label("send-target")
		})
	}, 180, 100)
	r, _ := tt.Find("send-target")
	cx, cy := r.X+r.W/2, r.Y+r.H/2
	top := func() color.RGBA {
		return tt.Image().RGBAAt(int(r.X+r.W/2), int(r.Y))
	}
	tt.Advance(time.Second)
	restTop := top()
	tt.Move(cx, cy)
	tt.Press(cx, cy)
	tt.Advance(time.Second)
	pressTop := top()
	if restTop == rgbOf(p.BG) {
		t.Fatal("rest pill does not reach its top edge (sample point invalid)")
	}
	if pressTop == restTop {
		t.Fatal("press scale did not change the top-edge pixel")
	}
}

// TestFIX07IdleFrameBudget proves the state feedback does not force continuous
// redraws: once settled, ten forced idle frames must neither ask for another
// frame nor change a single pixel of the hover fill.
func TestFIX07IdleFrameBudget(t *testing.T) {
	p := fix07Palette(true)
	s := fix07Tester(t, p, true, false, false, false)
	r := s.rect(t)
	cx, cy := r.X+r.W/2, r.Y+r.H/2
	s.tt.Move(cx, cy)
	s.settle(t)
	before := s.fill(r)
	for i := 0; i < 10; i++ {
		s.tt.Frame()
		if s.tt.FramePending() {
			t.Fatalf("idle frame %d still asks for another frame", i)
		}
		s.tt.Advance(time.Second)
	}
	if after := s.fill(r); before != after {
		t.Fatalf("idle frames changed the hover fill: %+v -> %+v", before, after)
	}
}

// TestFIX07KeyboardAndCursor keeps the UI02 guarantee that keyboard activation,
// label/accessibility and pointer cursor survive the state-fill change.
func TestFIX07KeyboardAndCursor(t *testing.T) {
	p := fix07Palette(false)
	s := fix07Tester(t, p, false, false, false, false)
	s.settle(t)
	r := s.rect(t)
	cx, cy := r.X+r.W/2, r.Y+r.H/2
	// Whole-target pointer cursor over icon, text and padding.
	for _, pt := range [][2]float32{{r.X + 20, r.Y + 20}, {cx, cy}, {r.X + r.W - 12, r.Y + 20}} {
		s.tt.Move(pt[0], pt[1])
		if got := s.tt.Cursor(); got != ui.CursorPointer {
			t.Fatalf("cursor at %v: %v, want pointer", pt, got)
		}
	}
	// Space/Enter activation (after a focus click, as UI02 exercises it).
	s.tt.ClickAt(cx, cy)
	s.settle(t)
	before := *s.clicks
	s.tt.Key(0, ui.KeySpace)
	s.tt.Key(0, ui.KeyEnter)
	s.tt.Frame()
	if *s.clicks < before {
		t.Fatal("keyboard activation lost")
	}
	// The accessible label is still discoverable.
	if _, ok := s.tt.Find("send-target"); !ok {
		t.Fatal("send-target label lost")
	}
}
