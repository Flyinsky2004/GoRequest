package desktop

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"gorequest/internal/desktop/uikit"
)

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
	}
}

func TestU33DeleteBannerAndModalActions(t *testing.T) {
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

	// Narrow window: banner + modal still render all actions.
	tt := ui.NewTester(a.View, 520, 420)
	tt.Frame()
	for _, want := range []string{"Compare 3 Versions…", "Save As Copy", "Accept Delete…"} {
		if !tt.HasText(want) {
			t.Fatalf("banner missing %q; texts=%v", want, tt.Texts())
		}
	}
	if tt.HasText("Restore File") {
		t.Fatal("restore must not be offered as a banner shortcut")
	}
	if err := tt.Click("Compare 3 Versions…"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Keep Editing", "Save As Copy", "Restore File", "Accept Delete"} {
		if !tt.HasText(want) {
			t.Fatalf("modal missing %q; texts=%v", want, tt.Texts())
		}
	}
	// Closing keeps edits and file stays absent.
	if err := tt.Click("Keep Editing"); err != nil {
		t.Fatal(err)
	}
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
