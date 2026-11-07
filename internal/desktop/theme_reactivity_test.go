package desktop

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"gorequest/internal/core"
)

// TestThemeSwitchRestylesCachedResponseEditor is the regression for the
// appearance switch leaving the response body on the previous theme until a tab
// switch or resend rebuilt the cached editor. Flipping the preference must
// restyle the editor in place, keeping the buffer, caret and scroll state.
func TestThemeSwitchRestylesCachedResponseEditor(t *testing.T) {
	a, ps := openTestProject(t, "Theme Response")
	tab := openRequestTabs(t, ps, "Colors")[0]
	tab.Resp = &core.ExecResult{
		Body:     []byte(`{"alpha":1,"beta":[true,null]}`),
		Pretty:   "{\n  \"alpha\": 1,\n  \"beta\": [\n    true,\n    null\n  ]\n}",
		BodySize: 33,
	}
	tab.RespSection = 0
	tab.RespRawIdx = 0
	a.mgr.cfg.setAppearance("Light")

	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()

	st := tab.respEditor
	if st == nil {
		t.Fatal("the response editor was not created")
	}
	if st.Theme == nil {
		t.Fatal("the response editor has no theme")
	}
	lightBG := st.Theme.Background
	wantBody := st.Buffer.String()
	st.Select(5, 5)
	wantCursor := st.Cursor()

	// Switch to Dark and render one frame: same editor, new theme, same
	// content and caret.
	a.mgr.cfg.setAppearance("Dark")
	tt.Frame()

	if tab.respEditor != st {
		t.Fatal("an appearance switch rebuilt the response editor instead of restyling it")
	}
	if st.Theme.Background == lightBG {
		t.Fatal("the cached response editor kept the light theme after switching to Dark")
	}
	if got := st.Buffer.String(); got != wantBody {
		t.Fatalf("response buffer changed across the theme switch: %q", got)
	}
	if got := st.Cursor(); got != wantCursor {
		t.Fatalf("response caret moved across the theme switch: %d, want %d", got, wantCursor)
	}

	// And back to Light, still without rebuilding.
	a.mgr.cfg.setAppearance("Light")
	tt.Frame()

	if tab.respEditor != st {
		t.Fatal("switching back to Light rebuilt the response editor")
	}
	if st.Theme.Background != lightBG {
		t.Fatal("the cached response editor did not return to the light theme")
	}
	if got := st.Buffer.String(); got != wantBody {
		t.Fatalf("response buffer changed after switching back: %q", got)
	}
	if got := st.Cursor(); got != wantCursor {
		t.Fatalf("response caret moved after switching back: %d, want %d", got, wantCursor)
	}
}

// TestThemeSwitchRestylesCachedDocEditor covers the documentation editor, which
// is cached on the tab for the lifetime of the document.
func TestThemeSwitchRestylesCachedDocEditor(t *testing.T) {
	a, ps := openTestProject(t, "Theme Docs")
	tab := openDocForTest(t, ps, "docs/theme.md", docGuideSrc)
	a.mgr.cfg.setAppearance("Light")

	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()

	st := tab.DocEditor
	if st == nil || st.Theme == nil {
		t.Fatal("the doc editor was not themed")
	}
	lightBG := st.Theme.Background
	wantBody := st.Buffer.String()
	st.Select(3, 3)
	wantCursor := st.Cursor()

	a.mgr.cfg.setAppearance("Dark")
	tt.Frame()

	if tab.DocEditor != st {
		t.Fatal("an appearance switch rebuilt the doc editor")
	}
	if st.Theme.Background == lightBG {
		t.Fatal("the cached doc editor kept the light theme after switching to Dark")
	}
	if got := st.Buffer.String(); got != wantBody {
		t.Fatalf("doc buffer changed across the theme switch: %q", got)
	}
	if got := st.Cursor(); got != wantCursor {
		t.Fatalf("doc caret moved across the theme switch: %d, want %d", got, wantCursor)
	}
}
