package desktop

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestFeedbackToastDedupesAndNeverClaimsEmptyCopy(t *testing.T) {
	a, _ := openTestProject(t, "Feedback")
	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()
	a.notifyCopied("")
	tt.Frame()
	if tt.HasText("复制成功") || tt.HasText("Copied") {
		t.Fatalf("empty copy must not show success: %v", tt.Texts())
	}
	a.notifyCopied("x")
	tt.Frame()
	a.notifyCopied("y")
	tt.Frame()
	n := 0
	for _, s := range tt.Texts() {
		if s == a.feedbackMsg().CopySuccess {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("want exactly one visible copy toast, got %d: %v", n, tt.Texts())
	}
}

func TestFeedbackMessagesBothLanguages(t *testing.T) {
	if feedbackMessagesZh.CopySuccess != "复制成功" || feedbackMessagesEn.CopySuccess == "" {
		t.Fatal("localized copy messages missing")
	}
}

func TestHeaderPathAfterProjectNameSingleCopy(t *testing.T) {
	a, ps := openTestProject(t, "PathProj")
	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()
	if !tt.HasText(a.headerPathMsg().NoActive) {
		t.Fatalf("no-tab hint missing: %v", tt.Texts())
	}
	folder, _ := ps.createFolder("", "API")
	entry, err := ps.createRequestIn(folder.RelPath, "Unique Req Title")
	if err != nil {
		t.Fatal(err)
	}
	ps.openRequest(entry)
	tt.Frame()
	if tt.HasText(a.headerPathMsg().NoActive) {
		t.Fatal("no-tab hint must disappear with an active tab")
	}
	n := 0
	for _, s := range tt.Texts() {
		if s == "API" {
			n++
		}
	}
	if !tt.HasText("Unique Req Title") || n < 1 {
		t.Fatalf("path missing: %v", tt.Texts())
	}
	// Narrow window must still render without panic.
	nt := ui.NewTester(a.View, 360, 600)
	nt.Frame()
}
