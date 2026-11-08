package desktop

import (
	"fmt"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"gorequest/internal/desktop/uikit"
)

// hasDistinctPixels reports whether rect [x0, x1) × [y0, y1) in img contains
// pixels that differ noticeably from bg (proving an icon or text glyph was
// actually rasterized inside that sub-rectangle of the button).
func hasDistinctPixels(img *image.RGBA, x0, y0, x1, y1 int, bg color.RGBA) bool {
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			c := img.RGBAAt(x, y)
			dr := absDiffU8(c.R, bg.R)
			dg := absDiffU8(c.G, bg.G)
			db := absDiffU8(c.B, bg.B)
			if int(dr)+int(dg)+int(db) > 40 {
				return true
			}
		}
	}
	return false
}

func absDiffU8(a, b uint8) uint8 {
	if a > b {
		return a - b
	}
	return b - a
}

func TestU33ButtonChildrenLabelGeometryClickAndDisabled(t *testing.T) {
	for _, dark := range []bool{false, true} {
		dark := dark
		t.Run(fmt.Sprintf("dark=%v", dark), func(t *testing.T) {
			var (
				withIconClicks    int
				noIconClicks      int
				disabledBtnClicks int
			)
			tt := ui.NewTester(func(c *ui.Context) {
				th := c.Theme()
				ui.Row(c).Padding(16).Gap(12).AlignItems(ui.Center).Children(func() {
					if fileSyncButton(c, th, fileSyncBtnDanger, "Accept Delete", fileSyncIconTrash, false, false).Clicked() {
						withIconClicks++
					}
					if fileSyncButton(c, th, fileSyncBtnSecondary, "Plain Action", nil, false, false).Clicked() {
						noIconClicks++
					}
					if fileSyncButton(c, th, fileSyncBtnDanger, "Disabled Delete", fileSyncIconTrash, false, true).Clicked() {
						disabledBtnClicks++
					}
				})
			}, 640, 160)
			tt.SetDark(dark)
			tt.Frame()

			rIcon, ok := tt.Find("Accept Delete")
			if !ok {
				t.Fatal("Accept Delete button not found by label")
			}
			rPlain, ok := tt.Find("Plain Action")
			if !ok {
				t.Fatal("Plain Action button not found by label")
			}
			rDis, ok := tt.Find("Disabled Delete")
			if !ok {
				t.Fatal("Disabled Delete button not found by label")
			}
			if rIcon.H != 32 || rPlain.H != 32 || rDis.H != 32 {
				t.Fatalf("unexpected button heights: icon=%v plain=%v dis=%v", rIcon.H, rPlain.H, rDis.H)
			}
			// Both labels have similar character counts; the icon+gap inside
			// e.Children must make the icon button wider than the icon-less one.
			if rIcon.W <= rPlain.W+10 {
				t.Fatalf("expected icon inside button to expand width: rIcon.W=%v rPlain.W=%v", rIcon.W, rPlain.W)
			}

			// Verify rendered pixels in tt.Image(): background fill, icon region, and text region.
			img := tt.Image()
			bgSample := img.RGBAAt(int(rIcon.X)+3, int(rIcon.Y+rIcon.H/2))
			if int(bgSample.R) <= int(bgSample.G)+50 || int(bgSample.R) <= int(bgSample.B)+50 {
				t.Fatalf("expected red danger fill on Accept Delete, got RGBA=%+v", bgSample)
			}
			iconX0, iconX1 := int(rIcon.X)+12, int(rIcon.X)+26
			textX0, textX1 := int(rIcon.X)+32, int(rIcon.X+rIcon.W)-12
			y0, y1 := int(rIcon.Y)+4, int(rIcon.Y+rIcon.H)-4
			if !hasDistinctPixels(img, iconX0, y0, iconX1, y1, bgSample) {
				t.Fatalf("expected SVG icon strokes rendered inside button icon slot [%d,%d]x[%d,%d]", iconX0, iconX1, y0, y1)
			}
			if !hasDistinctPixels(img, textX0, y0, textX1, y1, bgSample) {
				t.Fatalf("expected label glyphs rendered inside button text slot [%d,%d]x[%d,%d]", textX0, textX1, y0, y1)
			}

			// 1. Click by accessible label / text.
			if err := tt.Click("Accept Delete"); err != nil {
				t.Fatalf("Click(Accept Delete): %v", err)
			}
			if withIconClicks != 1 {
				t.Fatalf("withIconClicks after Click(label) = %d, want 1", withIconClicks)
			}

			// 2. Click directly on the left SVG icon region inside the button.
			tt.ClickAt(rIcon.X+18, rIcon.Y+rIcon.H/2)
			if withIconClicks != 2 {
				t.Fatalf("withIconClicks after clicking icon area = %d, want 2", withIconClicks)
			}

			// 3. Click directly on the right text region inside the button.
			tt.ClickAt(rIcon.X+rIcon.W-16, rIcon.Y+rIcon.H/2)
			if withIconClicks != 3 {
				t.Fatalf("withIconClicks after clicking text area = %d, want 3", withIconClicks)
			}

			// 4. Keyboard focus (requires e.Label(label) on ButtonBase) + Space activation.
			if !tt.Focused("Accept Delete") {
				t.Fatal("expected Accept Delete to hold focus after click")
			}
			tt.Key(0, ui.KeySpace)
			if withIconClicks != 4 {
				t.Fatalf("withIconClicks after KeySpace = %d, want 4", withIconClicks)
			}

			// 5. Pointer cursor on enabled vs default cursor & no-op on disabled.
			tt.Move(rIcon.X+rIcon.W/2, rIcon.Y+rIcon.H/2)
			if got := tt.Cursor(); got != ui.CursorPointer {
				t.Fatalf("enabled button cursor = %v, want CursorPointer", got)
			}
			tt.Move(rDis.X+rDis.W/2, rDis.Y+rDis.H/2)
			if got := tt.Cursor(); got != ui.CursorDefault {
				t.Fatalf("disabled button cursor = %v, want CursorDefault", got)
			}
			tt.ClickAt(rDis.X+18, rDis.Y+rDis.H/2)
			tt.ClickAt(rDis.X+rDis.W/2, rDis.Y+rDis.H/2)
			if disabledBtnClicks != 0 {
				t.Fatalf("disabled button must not fire Clicked(), got %d", disabledBtnClicks)
			}

			// Disabled button must look visibly faded compared to enabled danger button.
			disSample := img.RGBAAt(int(rDis.X)+3, int(rDis.Y+rDis.H/2))
			if absDiffU8(disSample.R, bgSample.R) < 20 {
				t.Fatalf("disabled button fill (%+v) should be visibly dimmed compared to enabled (%+v)", disSample, bgSample)
			}
		})
	}
}

func TestU33DangerOnlyForAcceptDeleteAndRestoreIsSecondary(t *testing.T) {
	for _, dark := range []bool{false, true} {
		th := ui.LightTheme()
		if dark {
			th = ui.DarkTheme()
		}
		p := uikit.PaletteFor(th)
		bg, _, fg, _ := fileSyncButtonColors(p, fileSyncBtnDanger, false)
		if bg != p.Danger || fg != p.OnAccent {
			t.Fatalf("dark=%v danger button must use palette danger", dark)
		}
		rbg, _, _, border := fileSyncButtonColors(p, fileSyncBtnSecondary, false)
		if rbg == p.Danger || border == (ui.Color{}) {
			t.Fatalf("dark=%v secondary (restore) must not be red and needs a border", dark)
		}
		dbg, _, dfg, _ := fileSyncButtonColors(p, fileSyncBtnDanger, true)
		if dbg.A >= bg.A || dfg.A >= fg.A {
			t.Fatalf("dark=%v disabled danger must be dimmed", dark)
		}
	}
}

func TestU33ActionIconsAllPresent(t *testing.T) {
	for name, ic := range map[string]*ui.SVG{
		"compare": fileSyncIconCompare, "trash": fileSyncIconTrash, "restore": fileSyncIconRestore,
		"merge": fileSyncIconMerge, "later": fileSyncIconLater, "save": fileSyncIconSave,
		"copy": fileSyncIconCopy, "reload": fileSyncIconReload,
	} {
		if ic == nil {
			t.Errorf("icon %s failed to parse", name)
		}
	}
}

func TestU33DeleteWordingIsCalmAndAcceptIsDangerLabel(t *testing.T) {
	for _, m := range []*fileSyncMessages{&fileSyncMessagesZh, &fileSyncMessagesEn} {
		if strings.Contains(m.AcceptDeleteBtn, "Close") || strings.Contains(m.AcceptDeleteBtn, "并关闭") {
			t.Errorf("accept delete label too long: %q", m.AcceptDeleteBtn)
		}
		if strings.Contains(m.BannerDeleted, "Git") {
			t.Errorf("deleted banner must not mention Git: %q", m.BannerDeleted)
		}
		if !strings.Contains(m.DeletedOnDiskEmpty, "Git") {
			t.Errorf("deleted empty pane should clarify it is not a Git conflict: %q", m.DeletedOnDiskEmpty)
		}
	}
}

func TestU33DeleteBannerAndModalActionsGeometryAndPixels(t *testing.T) {
	t.Cleanup(StopAllFileSync)
	a, ps := openTestProject(t, "U33Delete")
	docEntry, err := ps.newDoc()
	if err != nil {
		t.Fatal(err)
	}
	docTab := ps.activeTab()
	if err := a.InitFileSync(); err != nil {
		t.Fatal(err)
	}
	docTab.DocEditor.Buffer.Set("# Unsaved\n\nkeep me\n")
	docTab.Dirty = true
	docAbs := filepath.Join(ps.project.Store.Root, filepath.FromSlash(docEntry.RelPath))
	if err := os.Remove(docAbs); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SyncNow(); err != nil {
		t.Fatal(err)
	}
	if a.ConflictForTab(docTab) == nil {
		t.Fatal("expected delete conflict")
	}

	// Narrow window: banner + modal still render all actions with icons inside bounds.
	tt := ui.NewTester(a.View, 520, 420)
	tt.Frame()
	for _, want := range []string{"Compare 3 Versions…", "Save As Copy", "Accept Delete…"} {
		r, ok := tt.Find(want)
		if !ok {
			t.Fatalf("banner missing %q; texts=%v", want, tt.Texts())
		}
		if r.W < 40 || r.H != 28 || r.X < 0 || r.X+r.W > 520 {
			t.Fatalf("banner button %q out of narrow window bounds: %+v", want, r)
		}
	}
	if tt.HasText("Restore File") {
		t.Fatal("restore must not be offered as a banner shortcut")
	}

	// Click the left icon slot of "Accept Delete…" in the banner: must open the
	// comparison modal (never silently discarding unsaved edits).
	acceptBannerRect, _ := tt.Find("Accept Delete…")
	tt.ClickAt(acceptBannerRect.X+15, acceptBannerRect.Y+acceptBannerRect.H/2)
	if !tt.HasText("Resolve External File Deletion") {
		t.Fatalf("clicking icon of Accept Delete… should open modal; texts=%v", tt.Texts())
	}

	// Verify modal buttons in both light and dark mode: geometry, icon pixels,
	// and that ONLY "Accept Delete" has red danger fill while "Restore File" is neutral.
	for _, dark := range []bool{false, true} {
		tt.SetDark(dark)
		tt.Frame()
		img := tt.Image()
		for _, want := range []string{"Keep Editing", "Save As Copy", "Restore File", "Accept Delete"} {
			r, ok := tt.Find(want)
			if !ok {
				t.Fatalf("dark=%v modal missing %q; texts=%v", dark, want, tt.Texts())
			}
			if r.W < 40 || r.H != 32 || r.X < 0 || r.X+r.W > 520 || r.Y+r.H > 420 {
				t.Fatalf("dark=%v modal button %q out of bounds: %+v", dark, want, r)
			}
			bg := img.RGBAAt(int(r.X)+3, int(r.Y+r.H/2))
			if !hasDistinctPixels(img, int(r.X)+12, int(r.Y)+4, int(r.X)+26, int(r.Y+r.H)-4, bg) {
				t.Fatalf("dark=%v modal button %q missing rendered icon pixels in [%d,%d]", dark, want, int(r.X)+12, int(r.X)+26)
			}
		}
		rAccept, _ := tt.Find("Accept Delete")
		rRestore, _ := tt.Find("Restore File")
		pxAccept := img.RGBAAt(int(rAccept.X)+3, int(rAccept.Y+rAccept.H/2))
		pxRestore := img.RGBAAt(int(rRestore.X)+3, int(rRestore.Y+rRestore.H/2))
		if int(pxAccept.R) <= int(pxAccept.G)+50 || int(pxAccept.R) <= int(pxAccept.B)+50 {
			t.Fatalf("dark=%v Accept Delete must be red danger primary, got RGBA=%+v", dark, pxAccept)
		}
		if absDiffU8(pxRestore.R, pxRestore.G) > 25 {
			t.Fatalf("dark=%v Restore File must be neutral secondary (not red), got RGBA=%+v", dark, pxRestore)
		}
	}
	tt.SetDark(false)
	tt.Frame()

	// Click the icon area of "Keep Editing" to close modal; edits stay protected.
	keepRect, _ := tt.Find("Keep Editing")
	tt.ClickAt(keepRect.X+18, keepRect.Y+keepRect.H/2)
	if a.ConflictForTab(docTab) == nil || !strings.Contains(docTab.DocEditor.Buffer.String(), "keep me") {
		t.Fatalf("edits lost on Keep Editing: dirty=%v buf=%q", docTab.Dirty, docTab.DocEditor.Buffer.String())
	}
	if _, err := os.Stat(docAbs); !os.IsNotExist(err) {
		t.Fatal("file resurrected")
	}

	// Save As Copy from banner keeps edits in a new file and closes the deleted tab.
	tt.Frame()
	if err := tt.Click("Save As Copy"); err != nil {
		t.Fatal(err)
	}
	if ps.indexOfTab(docTab) >= 0 {
		t.Fatal("deleted tab should be replaced by the saved copy")
	}
	if _, err := os.Stat(docAbs); !os.IsNotExist(err) {
		t.Fatal("original resurrected by save-as-copy")
	}
}

func TestU33BannerInlineSaveAsCopyErrorAndGitConflictDistinction(t *testing.T) {
	t.Cleanup(StopAllFileSync)
	a, ps := openTestProject(t, "U33BannerErr")
	docEntry, err := ps.newDoc()
	if err != nil {
		t.Fatal(err)
	}
	docTab := ps.activeTab()
	if err := a.InitFileSync(); err != nil {
		t.Fatal(err)
	}
	docTab.DocEditor.Buffer.Set("# Copy me\n")
	docTab.Dirty = true
	docAbs := filepath.Join(ps.project.Store.Root, filepath.FromSlash(docEntry.RelPath))
	docsDir := filepath.Dir(docAbs)
	if err := os.Remove(docAbs); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SyncNow(); err != nil {
		t.Fatal(err)
	}

	// Make docs directory read-only so inline Save As Copy fails with a real I/O error.
	if err := os.Chmod(docsDir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(docsDir, 0o755) })

	tt := ui.NewTester(a.View, 900, 600)
	tt.Frame()
	// Click the icon region of "Save As Copy" in the banner.
	copyRect, ok := tt.Find("Save As Copy")
	if !ok {
		t.Fatal("missing Save As Copy in banner")
	}
	tt.ClickAt(copyRect.X+15, copyRect.Y+copyRect.H/2)
	if fileSyncBannerErr(docTab) == "" || !tt.HasText(fileSyncBannerErr(docTab)) {
		t.Fatalf("expected inline banner error when Save As Copy fails; err=%q texts=%v", fileSyncBannerErr(docTab), tt.Texts())
	}

	// Restore write permission and retry Save As Copy: error clears and copy succeeds.
	if err := os.Chmod(docsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click("Save As Copy"); err != nil {
		t.Fatal(err)
	}
	if got := fileSyncBannerErr(docTab); got != "" {
		t.Fatalf("expected banner error to clear after successful Save As Copy, got %q", got)
	}
}
