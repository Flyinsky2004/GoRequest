package desktop

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"gorequest/internal/core"
	"gorequest/internal/desktop/uikit"
)

// TestWelcomeIdleRequestsNoFrames is the frame-budget guard (§7): a settled
// interface must ask for no animation frames while idle. The welcome window has
// no running animation, so a regression that leaves a looping timer or frame
// loop on screen fails here.
func TestWelcomeIdleRequestsNoFrames(t *testing.T) {
	m, _ := newTestManager(t)
	m.showWelcome("")
	tt := ui.NewTester(m.welcome.view, 720, 480)
	tt.Frame()
	if tt.FramePending() {
		t.Fatal("welcome keeps requesting frames while idle after settling")
	}
	tt.Frame()
	if tt.FramePending() {
		t.Fatal("welcome requested frames on an idle redraw")
	}
}

// TestLargeResponseDegradesToPlainText checks §5.9: a body past the threshold is
// shown as plain text (no JSON syntax layer), while a normal JSON body is
// highlighted.
func TestLargeResponseDegradesToPlainText(t *testing.T) {
	app := &App{}
	th := uikit.Theme(uikit.PaletteFor(nil), false)

	big := &Tab{Resp: &core.ExecResult{
		BodySize: largeResponseThreshold + 1,
		Body:     []byte(`{"a":1}`),
		Pretty:   "{\n  \"a\": 1\n}",
	}}
	if st := app.responseEditorFor(th, big); st.Buffer.Language() != nil {
		t.Fatal("a large response must not keep the JSON syntax layer")
	}

	small := &Tab{Resp: &core.ExecResult{
		BodySize: 20,
		Body:     []byte(`{"a":1}`),
		Pretty:   "{\n  \"a\": 1\n}",
	}}
	if st := app.responseEditorFor(th, small); st.Buffer.Language() == nil {
		t.Fatal("a normal JSON response should be syntax-highlighted")
	}
}
