package brand

import (
	"bytes"
	"image"
	"image/png"
	"runtime"
	"testing"
)

func TestMacOSIconOpticalBounds(t *testing.T) {
	icon, err := png.Decode(bytes.NewReader(macOSIconPNG))
	if err != nil {
		t.Fatal(err)
	}
	if icon.Bounds() != image.Rect(0, 0, 1024, 1024) {
		t.Fatalf("unexpected canvas: %v", icon.Bounds())
	}
	// Measure the visible body rather than faint resampling fringes. The
	// original body's 91% footprint made it larger than neighboring Dock icons.
	var body image.Rectangle
	for y := 0; y < 1024; y++ {
		for x := 0; x < 1024; x++ {
			_, _, _, a := icon.At(x, y).RGBA()
			if a >= 0x8000 {
				body = body.Union(image.Rect(x, y, x+1, y+1))
			}
		}
	}
	if body.Dx() < 800 || body.Dx() > 840 || body.Dy() < 800 || body.Dy() > 840 {
		t.Fatalf("visible body must occupy about 80%% of the canvas: %v", body)
	}
	if body.Min.X < 90 || body.Min.Y < 90 || body.Max.X > 934 || body.Max.Y > 934 {
		t.Fatalf("icon lacks centered transparent margins: %v", body)
	}
}

func TestNativeIconUsesPlatformResource(t *testing.T) {
	want := LogoPNG
	if runtime.GOOS == "darwin" {
		want = macOSIconPNG
	}
	got := PNG()
	if !bytes.Equal(got, want) {
		t.Fatal("native icon does not use the platform resource")
	}
	got[0] ^= 0xff
	if !bytes.Equal(PNG(), want) {
		t.Fatal("caller mutated the embedded icon")
	}
}
