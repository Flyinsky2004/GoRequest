package desktop

import (
	"reflect"
	"testing"

	"github.com/egoist/mygo/ui"
)

// MyGo v0.7.4's headless host makes startDrag/titleBarDoubleClicked no-ops
// and exposes no drag counter. Read its hover/state registry, without unsafe
// or mutation, to check the actual innermost interactive target used by
// ui/input.go. These masks mirror that pinned version's element.go/input.go.
// This proves registration and routing, not movement of an OS window.
const (
	dragFlag         = uint64(1 << 3)
	interactiveFlags = uint64(1<<0 | 1<<1 | 1<<2 | 1<<3 | 1<<10 | 1<<12 | 1<<17)
)

func windowDragTarget(tt *ui.Tester, x, y float32) bool {
	tt.Move(x, y)
	rt := reflect.ValueOf(tt).Elem().FieldByName("rt").Elem()
	hover, states := rt.FieldByName("hover"), rt.FieldByName("states")
	for i := 0; i < hover.Len(); i++ {
		state := states.MapIndex(hover.Index(i)).Elem()
		flags := state.FieldByName("flags").Uint()
		if flags&interactiveFlags != 0 {
			return flags&interactiveFlags == dragFlag
		}
	}
	return false
}

func windowDragAtLabel(t *testing.T, tt *ui.Tester, label string) bool {
	t.Helper()
	r, ok := tt.Find(label)
	if !ok {
		t.Fatalf("missing %q", label)
	}
	return windowDragTarget(tt, r.X+r.W/2, r.Y+r.H/2)
}

func TestWindowDragRegions(t *testing.T) {
	for _, scale := range []float32{1, 2} {
		for _, width := range []int{560, 1024, 1600} {
			var input string
			clicks := 0
			tt := ui.NewTester(func(c *ui.Context) {
				ui.Column(c).Fill().Children(func() {
					ui.Column(c).Height(52).FillWidth().Children(func() {
						ui.Text(c, "GoRequest").Padding(8, 78)
						windowDragBrand(c, 78)
					})
					left, right := windowDragInsets(c, true)
					ui.Row(c).Height(46).Padding(8, right, 8, left).Gap(6).Children(func() {
						if ui.Button(c, "项目").Clicked() {
							clicks++
						}
						windowDragSpacer(c, 8)
						ui.TextInput(c, &input).Width(100).Label("环境输入")
						ui.Button(c, "禁用").Disabled(true)
					})
					if ui.Button(c, "标签关闭").Clicked() {
						clicks++
					}
				})
			}, width, 240)
			tt.SetTitleBar(ui.TitleBar{Height: 32, Left: 78, Right: 90})
			tt.SetScale(scale)
			tt.Frame()
			for _, p := range [][2]float32{{100, 5}, {float32(width) / 2, 26}, {float32(width) / 2, 53}, {float32(width) / 2, 95}} {
				if !windowDragTarget(tt, p[0], p[1]) {
					t.Fatalf("width=%d scale=%g: blank %v is not a drag target", width, scale, p)
				}
			}
			for _, p := range [][2]float32{{20, 15}, {float32(width) - 20, 15}, {20, 70}, {float32(width) - 20, 70}, {200, 180}} {
				if windowDragTarget(tt, p[0], p[1]) {
					t.Fatalf("reserved/body point %v drags", p)
				}
			}
			for _, label := range []string{"项目", "环境输入", "禁用", "标签关闭"} {
				if windowDragAtLabel(t, tt, label) {
					t.Fatalf("%q routes to window drag", label)
				}
			}
			if err := tt.Click("项目"); err != nil {
				t.Fatal(err)
			}
			if err := tt.Click("环境输入"); err != nil {
				t.Fatal(err)
			}
			tt.Type("中文")
			if input != "中文" {
				t.Fatalf("input lost: %q", input)
			}
			if err := tt.Click("禁用"); err != nil {
				t.Fatal(err)
			}
			if err := tt.Click("标签关闭"); err != nil {
				t.Fatal(err)
			}
			if clicks != 2 {
				t.Fatalf("clicks=%d, want 2", clicks)
			}
		}
	}
}

func TestWindowDragInsets(t *testing.T) {
	var left, right float32
	tt := ui.NewTester(func(c *ui.Context) { left, right = windowDragInsets(c, true) }, 300, 100)
	if left != 12 || right != 12 {
		t.Fatalf("decorated window: %g,%g", left, right)
	}
	tt.SetTitleBar(ui.TitleBar{Height: 32, Left: 110, Right: 140})
	tt.Frame()
	if left != 110 || right != 140 {
		t.Fatalf("native insets: %g,%g", left, right)
	}
}

func TestWindowDragImmediateDispatchAndZoom(t *testing.T) {
	tt := ui.NewTester(func(c *ui.Context) {
		windowDragRegion(c).Width(200).Height(52)
	}, 400, 180)
	for _, zoom := range []float32{1, 1.25, 1.5} {
		tt.SetZoom(zoom)
		tt.Frame()
		x, y := float32(100)*zoom, float32(20)*zoom
		if !windowDragTarget(tt, x, y) {
			t.Fatalf("zoom=%g: drag registration missing", zoom)
		}
		// No Advance or movement: the first press already takes the native path.
		tt.Press(x, y)
		rt := reflect.ValueOf(tt).Elem().FieldByName("rt").Elem()
		if !rt.FieldByName("pressed").IsNil() {
			t.Fatal("drag captured as an ordinary deferred pointer gesture")
		}
		tt.Release(x, y)
		// A second press follows MyGo's double-click route; no custom maximize
		// or timer is installed by the adapter. The headless platform is a no-op.
		tt.Press(x, y)
		if !rt.FieldByName("pressed").IsNil() {
			t.Fatal("double click captured as a pointer gesture")
		}
		tt.Release(x, y)
	}
}
