package desktop

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"gorequest/internal/core"
)

const previewBody = `{
  "id": 1024,
  "name": "GoRequest",
  "active": true,
  "tags": ["api", "native"],
  "meta": null,
  "score": 98.5
}`

// setupPreviewProject opens a project with four method-colored request tabs and
// returns the app; the active tab is configured per view by the caller.
func setupPreviewProject(t *testing.T, dark bool) *App {
	t.Helper()
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
	tabs[0].QueryRows = []*KVRow{
		{ID: nextRowID(), Enabled: true, Name: "page", Value: "2"},
		{ID: nextRowID(), Enabled: true, Name: "limit", Value: "20"},
		newBlankRow(),
	}
	tabs[1].Dirty = true
	tabs[3].BodyIdx = 0
	tabs[3].BodyText = previewBody
	tabs[3].Section = 3 // Body
	tabs[3].Resp = &core.ExecResult{
		Status:   200,
		Proto:    "HTTP/1.1",
		Duration: 12 * time.Millisecond,
		BodySize: int64(len(previewBody)),
		Body:     []byte(previewBody),
		Pretty:   previewBody,
		Headers: [][2]string{
			{"Content-Type", "application/json"},
			{"X-Request-ID", "abc123"},
		},
	}
	tabs[3].respVersion = 1
	return a
}

func writeRGBA(t *testing.T, img *image.RGBA, out, name string) int {
	t.Helper()
	if img == nil {
		t.Fatalf("%s: render returned nil", name)
	}
	uniq := map[uint32]struct{}{}
	for i := 0; i < len(img.Pix); i += 4 {
		key := uint32(img.Pix[i])<<24 | uint32(img.Pix[i+1])<<16 | uint32(img.Pix[i+2])<<8 | uint32(img.Pix[i+3])
		uniq[key] = struct{}{}
	}
	if len(uniq) < 50 {
		t.Fatalf("%s: looked blank (%d colors)", name, len(uniq))
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
	return len(uniq)
}

// TestRenderShellPreview renders the real project window in the Body codeView
// and in the Params KV table, light and dark. It writes PNGs when PREVIEW_OUT is
// set: build-body-{light,dark}.png and build-params-{light,dark}.png.
func TestRenderShellPreview(t *testing.T) {
	out := os.Getenv("PREVIEW_OUT")
	for _, dark := range []bool{false, true} {
		// Body tab active (codeView + underline tabs + URL group).
		a := setupPreviewProject(t, dark)
		a.ps.active = 3
		tone := "light"
		if dark {
			tone = "dark"
		}
		writeRGBA(t, ui.Render(a.View, 1320, 860, 1), out, "build-body-"+tone+".png")

		// Params tab active (KV table + ghost row).
		b := setupPreviewProject(t, dark)
		b.ps.active = 0
		b.ps.tabs[0].Section = 0
		writeRGBA(t, ui.Render(b.View, 1320, 860, 1), out, "build-params-"+tone+".png")
	}
}
