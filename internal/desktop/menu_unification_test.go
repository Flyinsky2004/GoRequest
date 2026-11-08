package desktop

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

// selectorCalls returns the "pkg.Name" calls of the non-test Go files in dir,
// as "file:line" strings. It parses the source, so comments and strings that
// mention an API are not counted.
func selectorCalls(t *testing.T, dir string, pkg, name string) []string {
	t.Helper()
	fset := token.NewFileSet()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	var found []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			// Unwrap a generic instantiation such as ui.SelectBase[string](…).
			fun := call.Fun
			switch f := fun.(type) {
			case *ast.IndexExpr:
				fun = f.X
			case *ast.IndexListExpr:
				fun = f.X
			}
			sel, ok := fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != name {
				return true
			}
			id, ok := sel.X.(*ast.Ident)
			if !ok || id.Name != pkg {
				return true
			}
			pos := fset.Position(call.Pos())
			found = append(found, fmt.Sprintf("%s:%d", pos.Filename, pos.Line))
			return true
		})
	}
	return found
}

// TestNoNativeMenuButtonsRemain is the static half of the U19 contract: every
// in-app drop-down must draw its menu inside the window. A ui.MenuButton asks
// the operating system for the menu surface and is therefore forbidden in the
// desktop package; uikit.MenuButton (the PopoverMenuButton baseline) or
// uikit.Select is the only entry point.
//
// Right-click context menus (ui.PopoverMenu) and the macOS top-level menu bar
// are not drop-down selectors and are not covered by this scan.
func TestNoNativeMenuButtonsRemain(t *testing.T) {
	if got := selectorCalls(t, ".", "ui", "MenuButton"); len(got) > 0 {
		t.Fatalf("native ui.MenuButton remains in the desktop package:\n%s", strings.Join(got, "\n"))
	}
	if got := selectorCalls(t, "uikit", "ui", "MenuButton"); len(got) > 0 {
		t.Fatalf("native ui.MenuButton remains in the uikit package:\n%s", strings.Join(got, "\n"))
	}
	if got := selectorCalls(t, ".", "ui", "Select"); len(got) > 0 {
		t.Fatalf("ui.Select bypasses the themed uikit.Select:\n%s", strings.Join(got, "\n"))
	}
	if got := selectorCalls(t, "uikit", "ui", "Select"); len(got) > 0 {
		t.Fatalf("ui.Select bypasses the themed uikit.Select:\n%s", strings.Join(got, "\n"))
	}
}

// TestSelectBaseStaysInsideTheComponent checks that the raw SelectBase is only
// used by uikit.Select, so every themed select keeps the shared look and the
// Changed notification.
func TestSelectBaseStaysInsideTheComponent(t *testing.T) {
	got := selectorCalls(t, "uikit", "ui", "SelectBase")
	if len(got) != 1 || filepath.Base(strings.Split(got[0], ":")[0]) != "select.go" {
		t.Fatalf("ui.SelectBase must only be used by uikit/select.go, got %v", got)
	}
	if got := selectorCalls(t, ".", "ui", "SelectBase"); len(got) > 0 {
		t.Fatalf("ui.SelectBase used outside the component:\n%s", strings.Join(got, "\n"))
	}
}

// TestOpenTabsMenuIsDrawnInWindow proves one migrated call site end to end:
// the tab overflow menu now opens the in-window themed menu, keeps its
// submenus and bulk actions, and still closes the tabs.
func TestOpenTabsMenuIsDrawnInWindow(t *testing.T) {
	a, ps := openTestProject(t, "MenuUnificationTabs")
	openRequestTabs(t, ps, "Alpha", "Beta", "Gamma")
	tt := ui.NewTester(a.View, 1100, 820)
	tt.Frame()

	label := fmt.Sprintf(a.msg().OpenTabsMenu, 3)
	if err := tt.Click(label); err != nil {
		t.Fatalf("click open-tabs menu: %v", err)
	}
	tt.Frame()
	if got := tt.Menu(); got != nil {
		t.Fatalf("the system showed a native menu: %v", got)
	}
	if !tt.HasText(a.msg().CloseAll) {
		t.Fatalf("drawn open-tabs menu missing Close All; texts=%v", tt.Texts())
	}
	if err := tt.Click(a.msg().CloseAll); err != nil {
		t.Fatalf("choose Close All: %v", err)
	}
	tt.Frame()
	if len(ps.tabs) != 0 {
		t.Fatalf("Close All left %d tabs open", len(ps.tabs))
	}
}
