package desktop

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func ui02DesktopHover(t *testing.T, tt *ui.Tester, label string, want ui.Cursor) ui.Rect {
	t.Helper()
	r, ok := tt.Find(label)
	if !ok || r.W <= 0 || r.H <= 0 {
		t.Fatalf("missing visible target %q: %+v", label, r)
	}
	tt.Move(r.X+r.W/2, r.Y+r.H/2)
	if got := tt.Cursor(); got != want {
		t.Fatalf("%q cursor=%v want=%v", label, got, want)
	}
	return r
}

func TestUI02WorkspaceCursorTargetsAndRename(t *testing.T) {
	a, ps := openTestProject(t, "Cursor")
	tab := openRequestTabs(t, ps, "Cursor request")[0]
	tt := ui.NewTester(a.View, 1100, 820)
	tt.Advance(time.Second)
	for _, size := range [][2]int{{1100, 820}, {760, 560}} {
		tt.SetSize(size[0], size[1])
		ui02DesktopHover(t, tt, a.msg().Send, ui.CursorPointer)
		ui02DesktopHover(t, tt, a.msg().RequestsGroup, ui.CursorPointer)
		tab.Sending = true
		tt.Frame()
		ui02DesktopHover(t, tt, a.msg().CancelRequest, ui.CursorPointer)
		tab.Sending = false
		tt.Frame()
	}
	tt.SetSize(1100, 820)
	// A real request row contains rich text and is also a drag source.
	ui02DesktopHover(t, tt, tab.Entry.Request.Name, ui.CursorPointer)
	ps.renaming = "req:" + tab.Entry.Request.ID
	ps.renameText = "Renamed request"
	tt.Frame()
	ui02DesktopHover(t, tt, a.msg().Rename, ui.CursorText)
	ps.renaming = ""
	tt.Frame()
	// Existing modal actions and read-only title retain distinct feedback.
	a.openNewRequest()
	tt.Frame()
	ui02DesktopHover(t, tt, a.msg().Create, ui.CursorPointer)
	if err := tt.Click(a.msg().Cancel); err != nil {
		t.Fatal(err)
	}
	if a.newReqOpen {
		t.Fatal("Cancel did not dismiss new request form")
	}
}

func TestUI02ImportDisabledActionCursor(t *testing.T) {
	a, _ := openTestProject(t, "Cursor import")
	a.OpenImportCURLModal()
	tt := ui.NewTester(a.View, 1100, 820)
	ui02DesktopHover(t, tt, a.interopMsg().PreviewButton, ui.CursorPointer)
	ui02DesktopHover(t, tt, a.interopMsg().ConfirmApplyButton, ui.CursorDefault)
	if err := tt.Click(a.interopMsg().ConfirmApplyButton); err != nil {
		t.Fatal(err)
	}
	if !a.Interop().ImportOpen || a.Interop().LastReceipt != nil {
		t.Fatal("disabled import action ran")
	}
}
