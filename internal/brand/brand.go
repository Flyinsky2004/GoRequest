// Package brand owns the GoRequest logo bytes so the desktop app can use the
// original artwork for in-app display and platform-sized icons. The PNGs are
// embedded: the running binary never depends on a path in
// the source tree, a checkout layout, or the user's Downloads folder.
package brand

import (
	_ "embed"
	"runtime"

	"github.com/egoist/mygo/ui"
)

// LogoPNG is the user-provided GoRequest logo, unchanged: 1600×1600 RGBA with
// a transparent area outside its macOS rounded-square container. It is the
// single source for every derived icon; see scripts/make-macos-icon.sh for the
// deterministic size/format conversion into an .icns app-bundle resource.
//
//go:embed gorequest-logo.png
var LogoPNG []byte

// macOSIconPNG includes transparent optical margins for the Dock. The bundle
// icon is generated from these same pixels by scripts/make-macos-icon.sh.
//
//go:embed gorequest-macos.png
var macOSIconPNG []byte

// logoImage decodes the embedded PNG once. ui.DecodeBitmap accepts PNG and
// keeps its alpha; a nil source makes ui.Image a harmless empty element, so a
// decode failure degrades to text-only instead of breaking the view.
var logoImage ui.ImageSource

func init() {
	logoImage, _ = ui.DecodeImage(LogoPNG)
}

// Logo returns the decoded image for ui.Image, or nil if the embedded PNG
// could not be decoded. The value is shared and read-only; never mutate it.
func Logo() ui.ImageSource {
	return logoImage
}

// PNG returns a copy of the raw PNG bytes for the native icon APIs
// (mygo.App.Dock.SetIcon and *mygo.Window.SetIcon), which take []byte.
// The copy keeps callers from altering the embedded resource.
func PNG() []byte {
	if runtime.GOOS == "darwin" {
		return append([]byte(nil), macOSIconPNG...)
	}
	return append([]byte(nil), LogoPNG...)
}
