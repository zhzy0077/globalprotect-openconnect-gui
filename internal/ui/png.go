package ui

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
)

// encodePNG encodes raw NRGBA pixels into a PNG byte slice.
// pix must be w*h*4 bytes in NRGBA order.
func encodePNG(w, h int, pix []byte) []byte {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < w*h; i++ {
		img.Pix[i*4] = pix[i*4]
		img.Pix[i*4+1] = pix[i*4+1]
		img.Pix[i*4+2] = pix[i*4+2]
		img.Pix[i*4+3] = pix[i*4+3]
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

// circleIcon returns a filled circle PNG on a transparent background.
func circleIcon(sz int, c color.NRGBA) []byte {
	img := image.NewNRGBA(image.Rect(0, 0, sz, sz))
	cx, cy, r := float64(sz)/2, float64(sz)/2, float64(sz)/2-1
	for y := 0; y < sz; y++ {
		for x := 0; x < sz; x++ {
			dx, dy := float64(x)-cx, float64(y)-cy
			if dx*dx+dy*dy <= r*r {
				img.SetNRGBA(x, y, c)
			}
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}
