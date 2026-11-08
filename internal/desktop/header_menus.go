package desktop

import (
	"github.com/egoist/mygo/ui"

	"gorequest/internal/desktop/uikit"
)

// headerMenuButton draws the workspace header's menu trigger. It is the
// reference drop-down of the workbench: the trigger follows the palette and
// the menu is drawn inside the window (ui.PopoverMenuButton) instead of asking
// the system for a native menu.
//
// Since U19 this is a thin wrapper over uikit.MenuButtonWithPalette, the one
// entry point every other in-app drop-down uses (project/environment
// switchers, tab overflow, runner, mock, identity, document insert). Keeping
// the header on the same helper means the reference cannot drift from the
// menus it defines: one trigger geometry, one surface, one keyboard and
// dismissal model. Right-click context menus (ui.PopoverMenu) and the macOS
// top-level menu bar are not drop-down selectors and stay outside this scope.
func headerMenuButton(c *ui.Context, p uikit.Palette, label string, build func(*ui.Menu)) ui.Element {
	return uikit.MenuButtonWithPalette(c, p, label, build)
}
