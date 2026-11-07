package desktop

import (
	"fmt"
	"image/color"
	"testing"

	"github.com/egoist/mygo/ui"

	"gorequest/internal/desktop/uikit"
	"gorequest/internal/gitservice"
)

// gitCheckboxTester renders the Git row checkbox alone so its look and input
// can be asserted without the rest of the panel.
func gitCheckboxTester(t *testing.T, checked *bool, disabled, dark bool) *ui.Tester {
	t.Helper()
	th := uikit.Theme(uikit.PaletteFor(&ui.Theme{Dark: dark}), dark)
	p := uikit.PaletteFor(th)
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(th)
		ui.Row(c).Padding(10).Children(func() {
			gitCheckbox(c, p, checked, disabled).Label("git-check")
		})
	}, 120, 60)
	tt.Frame()
	return tt
}

// TestGitCheckboxIsVisibleInBothThemes samples the control itself: the box
// must paint a stroke when off and the accent fill when on, in light and dark
// appearances alike. This pins the bug the local look fixes (an invisible
// CheckboxBase) rather than a screenshot.
func TestGitCheckboxIsVisibleInBothThemes(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, on := range []bool{false, true} {
			t.Run(fmt.Sprintf("dark=%v/on=%v", dark, on), func(t *testing.T) {
				checked := on
				tt := gitCheckboxTester(t, &checked, false, dark)
				r, ok := tt.Find("git-check")
				if !ok {
					t.Fatal("checkbox not found")
				}
				if want := gitCheckSide + 2*gitCheckPad; r.W != want || r.H != want {
					t.Fatalf("checkbox target = %gx%g, want %gx%g", r.W, r.H, want, want)
				}
				im := tt.Image()
				outside := im.RGBAAt(int(r.X), int(r.Y+r.H/2))
				edge := im.RGBAAt(int(r.X+gitCheckPad), int(r.Y+r.H/2))
				if colorNear(edge, outside, 8) {
					t.Fatalf("box has no visible stroke: edge=%+v outside=%+v", edge, outside)
				}
				inside := im.RGBAAt(int(r.X+gitCheckPad+3), int(r.Y+gitCheckPad+3))
				reddish := int(inside.R) > int(inside.G)+40 && int(inside.R) > int(inside.B)+40
				if on != reddish {
					t.Fatalf("on=%v inside pixel = %+v, reddish=%v", on, inside, reddish)
				}
			})
		}
	}
}

// TestGitCheckboxTogglesAndKeepsFocus keeps the framework's input contract:
// click and Space toggle the bound value and the box keeps the keyboard focus.
func TestGitCheckboxTogglesAndKeepsFocus(t *testing.T) {
	checked := false
	tt := gitCheckboxTester(t, &checked, false, false)
	if err := tt.Click("git-check"); err != nil {
		t.Fatalf("click: %v", err)
	}
	if !checked {
		t.Fatal("click did not select")
	}
	tt.Frame()
	tt.Key(0, ui.KeySpace)
	if checked {
		t.Fatal("Space did not clear the selection")
	}
	tt.Frame()
	if !tt.Focused("git-check") {
		t.Fatal("checkbox lost the keyboard focus")
	}
}

// TestGitCheckboxDisabledIgnoresInput pins the conflict-row contract: a
// disabled box paints but takes neither a click nor the focus.
func TestGitCheckboxDisabledIgnoresInput(t *testing.T) {
	checked := false
	tt := gitCheckboxTester(t, &checked, true, false)
	if err := tt.Click("git-check"); err != nil {
		t.Fatalf("click: %v", err)
	}
	tt.Frame()
	if checked {
		t.Fatal("disabled checkbox accepted a selection")
	}
	if tt.Focused("git-check") {
		t.Fatal("disabled checkbox took the keyboard focus")
	}
}

// TestGitRowCheckboxAndFilenameAreSeparateTargets drives the real row: the box
// selects a file (feeding selectedPaths and the commit count) without opening
// its diff, and the filename opens the diff without changing the selection.
func TestGitRowCheckboxAndFilenameAreSeparateTargets(t *testing.T) {
	a, _ := openTestProject(t, "Demo")
	g := newGitController(a)
	t.Cleanup(g.close)
	if g.svc == nil {
		t.Skipf("git service unavailable: %v", g.svcErr)
	}
	const path = "中文 文件.yaml"
	g.loaded = true
	g.repo = &gitservice.Repository{Root: a.ps.root, Branch: "main"}
	g.status = &gitservice.Status{
		Branch:  "main",
		Entries: []gitservice.Entry{{Path: path, Worktree: 'M'}},
	}
	tt := renderGitPanel(t, g)

	// The visible box is a real target: clicking it selects the file and does
	// not open its diff.
	if err := tt.Click(path); err != nil {
		t.Fatalf("click checkbox: %v", err)
	}
	tt.Frame()
	if !g.selection[path] {
		t.Fatal("checkbox click did not select the file")
	}
	if g.diffOpen {
		t.Fatal("checkbox click opened the diff")
	}
	if paths := g.selectedPaths(); len(paths) != 1 || paths[0] != path {
		t.Fatalf("selected paths after checkbox click = %v", paths)
	}
	if !tt.HasText(fmt.Sprintf("%s (1)", g.msg().Commit)) {
		t.Fatalf("commit count did not follow the checkbox: %v", tt.Texts())
	}

	// The filename button opens the diff and leaves the selection alone.
	if err := tt.Click(translate(g.msg().DiffTitle, path)); err != nil {
		t.Fatalf("click filename: %v", err)
	}
	tt.Frame()
	if !g.diffOpen {
		t.Fatal("filename click did not open the diff")
	}
	if !g.selection[path] {
		t.Fatal("filename click changed the selection")
	}
}

// TestGitUnmergedRowIsDisabledAndDropsStaleSelection pins the conflict rule:
// a refresh drops a selection made before the path became conflicted, and the
// disabled box does not take a new one.
func TestGitUnmergedRowIsDisabledAndDropsStaleSelection(t *testing.T) {
	a, _ := openTestProject(t, "Demo")
	const path = "conflict.go"
	g := &gitController{
		app:       a,
		selection: map[string]bool{path: true},
		loaded:    true,
		status: &gitservice.Status{
			Branch:  "main",
			Entries: []gitservice.Entry{{Path: path, Unmerged: true}},
		},
	}
	g.pruneSelection()
	if g.selection[path] {
		t.Fatal("stale selection of a conflicted path survived the refresh")
	}
	g.selectAll(g.status.Entries)
	if g.selection[path] {
		t.Fatal("select all selected a conflicted path")
	}

	th := uikit.Theme(uikit.PaletteFor(&ui.Theme{}), false)
	p := uikit.PaletteFor(th)
	entry := g.status.Entries[0]
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(th)
		ui.Column(c).Width(360).Children(func() {
			g.entryRow(c, c.Theme(), p, g.msg(), entry)
		})
	}, 360, 60)
	tt.Frame()
	if err := tt.Click(path); err != nil {
		t.Fatalf("click disabled checkbox: %v", err)
	}
	tt.Frame()
	if g.selection[path] {
		t.Fatal("disabled conflict checkbox accepted a selection")
	}
}

func colorNear(a, b color.Color, tol int) bool {
	ar, ag, ab, _ := a.RGBA()
	br, bg, bb, _ := b.RGBA()
	return absInt(int(ar)-int(br)) <= tol*257 &&
		absInt(int(ag)-int(bg)) <= tol*257 &&
		absInt(int(ab)-int(bb)) <= tol*257
}
