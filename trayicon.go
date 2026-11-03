package main

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/png"
)

// trayIcon draws a clipboard, black on transparent for macOS to tint, blue
// elsewhere. Paused, pause bars replace the text lines.
func trayIcon(paused bool) []byte {
	img := image.NewNRGBA(image.Rect(0, 0, 32, 32))
	ink := color.NRGBA{R: 79, G: 124, B: 255, A: 255}
	if isDarwin {
		ink = color.NRGBA{A: 255}
	}
	fill := func(x0, y0, x1, y1 int) {
		draw.Draw(img, image.Rect(x0, y0, x1, y1), image.NewUniform(ink), image.Point{}, draw.Src)
	}
	// board outline: 2-px rectangle x 6..26, y 5..29
	fill(6, 5, 26, 7)
	fill(6, 27, 26, 29)
	fill(6, 5, 8, 29)
	fill(24, 5, 26, 29)
	// clip
	fill(11, 2, 21, 7)
	if paused {
		fill(12, 12, 15, 23)
		fill(17, 12, 20, 23)
	} else {
		fill(10, 13, 22, 15)
		fill(10, 19, 22, 21)
	}
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	return b.Bytes()
}
