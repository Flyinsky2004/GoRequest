// Generate the macOS icon with transparent optical margins, preserving the logo.
package main

import (
	"image"
	"image/png"
	"log"
	"os"

	"golang.org/x/image/draw"
)

func main() {
	if len(os.Args) != 3 {
		log.Fatal("usage: macos-icon source.png output.png")
	}
	in, err := os.Open(os.Args[1])
	if err != nil {
		log.Fatal(err)
	}
	src, err := png.Decode(in)
	in.Close()
	if err != nil {
		log.Fatal(err)
	}
	// The source's rounded square occupies about 91% of its canvas. Scaling
	// the full artwork to 900/1024 leaves a visible body of about 80%, matching
	// the optical size of macOS app icons without cropping or redrawing it.
	const canvas, artwork = 1024, 900
	const inset = (canvas - artwork) / 2
	dst := image.NewNRGBA(image.Rect(0, 0, canvas, canvas))
	draw.CatmullRom.Scale(dst, image.Rect(inset, inset, inset+artwork, inset+artwork), src, src.Bounds(), draw.Src, nil)
	out, err := os.Create(os.Args[2])
	if err != nil {
		log.Fatal(err)
	}
	if err := png.Encode(out, dst); err != nil {
		log.Fatal(err)
	}
	if err := out.Close(); err != nil {
		log.Fatal(err)
	}
}
