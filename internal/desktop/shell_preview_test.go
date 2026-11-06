package desktop

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestRenderShellPreview renders the real project window shell (sidebar, top
// bar, request tab strip, request build area, response pane) headlessly in the
// light and dark palettes. When PREVIEW_OUT is set it writes shell-light.png and
// shell-dark.png there for visual inspection.
func TestRenderShellPreview(t *testing.T) {
	out := os.Getenv("PREVIEW_OUT")
	for _, dark := range []bool{false, true} {
		a, ps := openTestProject(t, "Preview")
		if dark {
			a.mgr.cfg.setAppearance("Dark")
		} else {
			a.mgr.cfg.setAppearance("Light")
		}
		methods := []string{"GET", "POST", "DELETE", "PUT"}
		names := []string{"List Users", "Create User", "Delete User", "Update User"}
		tabs := openRequestTabs(t, ps, names...)
		for i, tab := range tabs {
			tab.Method = methods[i%len(methods)]
			tab.URL = "https://api.example.com/v1/users"
		}
		tabs[1].Dirty = true
		ps.active = 0

		img := ui.Render(a.View, 1320, 860, 1)
		if img == nil {
			t.Fatalf("render returned nil")
		}
		// Non-blank check.
		uniq := map[uint32]struct{}{}
		for i := 0; i < len(img.Pix); i += 4 {
			key := uint32(img.Pix[i])<<24 | uint32(img.Pix[i+1])<<16 | uint32(img.Pix[i+2])<<8 | uint32(img.Pix[i+3])
			uniq[key] = struct{}{}
		}
		if len(uniq) < 50 {
			t.Fatalf("shell looked blank (%d colors)", len(uniq))
		}
		name := "shell-light.png"
		if dark {
			name = "shell-dark.png"
		}
		if out != "" {
			if err := os.MkdirAll(out, 0o755); err != nil {
				t.Fatal(err)
			}
			f, err := os.Create(filepath.Join(out, name))
			if err != nil {
				t.Fatal(err)
			}
			if err := png.Encode(f, img); err != nil {
				f.Close()
				t.Fatal(err)
			}
			f.Close()
		}
		t.Logf("%s: %d colors", name, len(uniq))
	}
}
