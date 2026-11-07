package desktop

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"gorequest/internal/core"
)

// R1: the request editor keeps exactly one "resolved preview" line under the
// URL's variable chips. The Params tab no longer repeats that same URL; it only
// adds a clearly labelled full-URL line when the Params query actually changes
// the final URL. These tests use ui.NewTester and position/text assertions,
// never screenshot comparison.

func countText(tt *ui.Tester, want string) int {
	n := 0
	for _, s := range tt.Texts() {
		if s == want {
			n++
		}
	}
	return n
}

func TestURLPreviewSingleLineAndParamsDifferenceOnly(t *testing.T) {
	a, ps := openTestProject(t, "URLPreview")
	ps.project.File.Variables["version"] = "v1"
	tab := openRequestTabs(t, ps, "Items")[0]
	tab.URL = "https://api.example.com/{{version}}/items"
	tab.Section = 0 // Params

	tt := ui.NewTester(a.View, 1240, 820)
	tt.Frame()

	topLabel := bodyMessagesEn.ResolvedURLPreviewLabel + ":"
	paramsLabel := bodyMessagesEn.ResolvedURLWithParamsLabel + ":"

	// No Params yet: one resolved line, and no difference line.
	if got := countText(tt, topLabel); got != 1 {
		t.Fatalf("resolved preview label count = %d, want 1; texts=%v", got, tt.Texts())
	}
	if !tt.HasText("https://api.example.com/v1/items") {
		t.Fatalf("URL-only resolved preview missing; texts=%v", tt.Texts())
	}
	if got := countText(tt, paramsLabel); got != 0 {
		t.Fatalf("params difference line rendered with no params (%d); texts=%v", got, tt.Texts())
	}

	// Add a Params row: the top line stays URL-only and the Params line shows
	// the full URL exactly once.
	tab.QueryRows = rowsFromKV([]core.KV{{Name: "q", Value: "hello world"}})
	tt.Frame()

	if got := countText(tt, topLabel); got != 1 {
		t.Fatalf("resolved preview label count after params = %d, want 1; texts=%v", got, tt.Texts())
	}
	if got := countText(tt, paramsLabel); got != 1 {
		t.Fatalf("params difference label count = %d, want 1; texts=%v", got, tt.Texts())
	}
	if !tt.HasText("https://api.example.com/v1/items?q=hello+world") {
		t.Fatalf("full URL with query missing; texts=%v", tt.Texts())
	}
	// The two lines must not print the same URL: the top line is the URL
	// field alone, the Params line is the full URL.
	if got := countText(tt, "https://api.example.com/v1/items"); got != 1 {
		t.Fatalf("URL-only line repeated %d times; texts=%v", got, tt.Texts())
	}

	top, okTop := tt.Find(topLabel)
	full, okFull := tt.Find(paramsLabel)
	if !okTop || !okFull {
		t.Fatalf("preview lines not found: top=%v full=%v", okTop, okFull)
	}
	if top.Y >= full.Y {
		t.Fatalf("URL preview must sit above the Params difference line: top=%+v full=%+v", top, full)
	}

	// Disabling every Params row removes the difference line again.
	disabled := false
	tab.QueryRows = rowsFromKV([]core.KV{{Name: "q", Value: "x", Enabled: &disabled}})
	tt.Frame()
	if got := countText(tt, paramsLabel); got != 0 {
		t.Fatalf("difference line rendered for disabled params (%d); texts=%v", got, tt.Texts())
	}
}

func TestURLPreviewMasksSecretsInBothLines(t *testing.T) {
	a, ps := openTestProject(t, "URLPreviewSecret")
	a.mgr.secrets = staticSecrets{"token": "TOP_SECRET_TOKEN"}
	tab := openRequestTabs(t, ps, "Secret")[0]
	tab.URL = "https://api.example.com/items/{{secret.token}}"
	tab.QueryRows = rowsFromKV([]core.KV{{Name: "key", Value: "{{secret.token}}"}})
	tab.Section = 0

	tt := ui.NewTester(a.View, 1240, 820)
	tt.Frame()

	for _, s := range tt.Texts() {
		if strings.Contains(s, "TOP_SECRET_TOKEN") {
			t.Fatalf("secret leaked into a preview: %q", s)
		}
	}
	if !tt.HasText("https://api.example.com/items/(secret)") {
		t.Fatalf("URL-only preview did not mask the secret; texts=%v", tt.Texts())
	}
	// The query value is context-encoded, so the masked "(secret)" appears as
	// %28secret%29; the real secret never does.
	if !tt.HasText("https://api.example.com/items/(secret)?key=%28secret%29") {
		t.Fatalf("params preview did not mask the secret; texts=%v", tt.Texts())
	}
}
