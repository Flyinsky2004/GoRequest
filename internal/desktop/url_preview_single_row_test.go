package desktop

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

// U03: the URL variable chips and the resolved URL preview share one row under
// the URL bar, so the editor gives back the height the old separate preview
// row took. The row wraps instead of overflowing: many chips or a long URL move
// to the next line in a narrow window, and the preview text itself is
// single-line so it ellipsizes rather than pushing past the window edge. These
// tests assert positions and text, never screenshots.

func TestURLVarChipsAndResolvedPreviewShareOneRow(t *testing.T) {
	a, ps := openTestProject(t, "PreviewSameRow")
	ps.project.File.Variables["version"] = "v1"
	tab := openRequestTabs(t, ps, "Items")[0]
	tab.URL = "https://api.example.com/{{version}}/items"
	tab.Section = 0 // Params

	tt := ui.NewTester(a.View, 1240, 820)
	tt.Frame()

	chip, ok := tt.Find("{{version}} = v1")
	if !ok {
		t.Fatalf("variable chip missing; texts=%v", tt.Texts())
	}
	label, ok := tt.Find(bodyMessagesEn.ResolvedURLPreviewLabel + ":")
	if !ok {
		t.Fatalf("resolved preview label missing; texts=%v", tt.Texts())
	}
	url, ok := tt.Find("https://api.example.com/v1/items")
	if !ok {
		t.Fatalf("resolved preview URL missing; texts=%v", tt.Texts())
	}

	// Same row: the chip, the preview label and the resolved URL all sit on
	// one line, and the preview follows the chip left to right.
	if d := chip.Y - label.Y; d > 1 || d < -1 {
		t.Fatalf("preview label not on the chip row: chip=%+v label=%+v", chip, label)
	}
	if d := chip.Y - url.Y; d > 1 || d < -1 {
		t.Fatalf("resolved URL not on the chip row: chip=%+v url=%+v", chip, url)
	}
	if label.X <= chip.X {
		t.Fatalf("preview should follow the chips: chip=%+v label=%+v", chip, label)
	}
}

func TestURLPreviewManyVarsLongURLNarrowWindowNoOverflow(t *testing.T) {
	a, ps := openTestProject(t, "PreviewNarrow")
	ps.project.File.Variables["alpha"] = strings.Repeat("A", 24)
	ps.project.File.Variables["beta"] = strings.Repeat("B", 24)
	ps.project.File.Variables["gamma"] = strings.Repeat("C", 24)
	tab := openRequestTabs(t, ps, "Items")[0]
	tab.URL = "https://api.example.com/{{alpha}}/{{beta}}/{{gamma}}/" +
		strings.Repeat("segment/", 12) + "?q=" + strings.Repeat("x", 60)
	tab.Section = 0

	const width = 760
	tt := ui.NewTester(a.View, width, 560)
	tt.Frame()

	// The preview is still rendered (it may be ellipsized, not dropped).
	if !tt.HasText(bodyMessagesEn.ResolvedURLPreviewLabel + ":") {
		t.Fatalf("resolved preview label missing in narrow window; texts=%v", tt.Texts())
	}

	// Every element this preview draws stays inside the window horizontally,
	// and the crowded row still keeps the chips and the preview on one line.
	firstChip, ok := tt.Find("{{alpha}} = " + strings.Repeat("A", 24))
	if !ok {
		t.Fatalf("first chip missing from the narrow window; texts=%v", tt.Texts())
	}
	for _, text := range []string{
		englishMessages.VarLabel,
		"{{alpha}} = " + strings.Repeat("A", 24),
		"{{beta}} = " + strings.Repeat("B", 24),
		"{{gamma}} = " + strings.Repeat("C", 24),
		bodyMessagesEn.ResolvedURLPreviewLabel + ":",
	} {
		r, ok := tt.Find(text)
		if !ok {
			t.Fatalf("%q missing from the narrow window; texts=%v", text, tt.Texts())
		}
		if r.X < -0.5 || r.X+r.W > width+0.5 {
			t.Fatalf("%q overflows the %d DIP window: rect=%+v", text, width, r)
		}
		if d := r.Y - firstChip.Y; d > 1 || d < -1 {
			t.Fatalf("%q left the shared row in a narrow window: chip=%+v rect=%+v", text, firstChip, r)
		}
	}
}

// TestURLPreviewKeepsMaskingAndUndefinedHintInSharedRow guards the behavior the
// layout change must not drop: a secret stays masked and an undefined variable
// keeps its clickable red chip that opens the environment editor.
func TestURLPreviewKeepsMaskingAndUndefinedHintInSharedRow(t *testing.T) {
	a, ps := openTestProject(t, "PreviewSharedRowBehavior")
	a.mgr.secrets = staticSecrets{"token": "TOP_SECRET_TOKEN"}
	tab := openRequestTabs(t, ps, "Secret")[0]
	tab.URL = "https://api.example.com/{{secret.token}}/{{missing}}"
	tab.Section = 0

	tt := ui.NewTester(a.View, 1240, 820)
	tt.Frame()

	for _, s := range tt.Texts() {
		if strings.Contains(s, "TOP_SECRET_TOKEN") {
			t.Fatalf("secret leaked into the shared preview row: %q", s)
		}
	}
	if !tt.HasText("{{missing}} ?") {
		t.Fatalf("undefined chip missing from the shared row; texts=%v", tt.Texts())
	}
	if err := tt.Click("{{missing}} ?"); err != nil {
		t.Fatalf("click undefined chip: %v", err)
	}
	if ps.envUI == nil || !ps.envUI.open {
		t.Fatalf("undefined chip should still open the environment editor, got %+v", ps.envUI)
	}
}
