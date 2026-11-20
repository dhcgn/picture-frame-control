// SPDX-License-Identifier: GPL-3.0-or-later

// Draws the app icon: a walnut picture frame with a mat and a small
// landscape, rendered with 4x4 supersampling.
//
// go run ./tools/appicon build/appicon.png
// wails3 task common:generate:icons    # build/windows/icon.ico
package main

import (
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
)

const size = 1024

type rgba struct{ r, g, b, a float64 }

func hex(c uint32) rgba {
	return rgba{float64(c >> 16 & 0xff), float64(c >> 8 & 0xff), float64(c & 0xff), 255}
}

func mix(a, b rgba, t float64) rgba {
	t = math.Max(0, math.Min(1, t))
	return rgba{a.r + (b.r-a.r)*t, a.g + (b.g-a.g)*t, a.b + (b.b-a.b)*t, a.a + (b.a-a.a)*t}
}

// inRounded reports whether (x,y) lies in the rectangle with rounded corners.
func inRounded(x, y, x0, y0, x1, y1, r float64) bool {
	if x < x0 || x > x1 || y < y0 || y > y1 {
		return false
	}
	cx := math.Max(x0+r, math.Min(x, x1-r))
	cy := math.Max(y0+r, math.Min(y, y1-r))
	return (x-cx)*(x-cx)+(y-cy)*(y-cy) <= r*r
}

// edgeDist is the distance from (x,y) to the nearest edge of the rectangle.
func edgeDist(x, y, x0, y0, x1, y1 float64) float64 {
	return math.Min(math.Min(x-x0, x1-x), math.Min(y-y0, y1-y))
}

var (
	woodHi  = hex(0x7a5540)
	woodLo  = hex(0x45301f) // light enough to stay visible on a dark taskbar
	mat     = hex(0xf7f3ea)
	skyTop  = hex(0x6fa8d6)
	skyLow  = hex(0xf2d8a6)
	sun     = hex(0xfbe7a6)
	hillBk  = hex(0x86ad6f)
	hillFr  = hex(0x47784b)
	clear   = rgba{}
	frame0x = 56.0
	frame0y = 150.0
	frame1x = 968.0
	frame1y = 874.0
	border  = 96.0
	matW    = 58.0
)

func shade(x, y float64) rgba {
	if !inRounded(x, y, frame0x, frame0y, frame1x, frame1y, 30) {
		return clear
	}
	// The moulding: a diagonal walnut gradient, lit on the outer edge and
	// shadowed where it meets the mat.
	ix0, iy0, ix1, iy1 := frame0x+border, frame0y+border, frame1x-border, frame1y-border
	if x < ix0 || x > ix1 || y < iy0 || y > iy1 {
		c := mix(woodHi, woodLo, (x+y-frame0x-frame0y)/(frame1x+frame1y-frame0x-frame0y))
		if d := edgeDist(x, y, frame0x, frame0y, frame1x, frame1y); d < 10 {
			c = mix(c, hex(0x9a7357), (10-d)/10*0.6)
		}
		if d := -edgeDist(x, y, ix0, iy0, ix1, iy1); d >= 0 && d < 14 {
			c = mix(c, hex(0x140d09), (14-d)/14*0.7)
		}
		return c
	}
	// The mat, with a soft shadow along the picture's edge.
	px0, py0, px1, py1 := ix0+matW, iy0+matW, ix1-matW, iy1-matW
	if x < px0 || x > px1 || y < py0 || y > py1 {
		c := mix(mat, hex(0xd9d2c4), (6-edgeDist(x, y, ix0, iy0, ix1, iy1))/6*0.8)
		return c
	}
	// The picture: sky, sun, two hills.
	w, h := px1-px0, py1-py0
	u, v := (x-px0)/w, (y-py0)/h
	c := mix(skyTop, skyLow, v/0.7)
	if sx, sy := u-0.7, (v-0.36)*h/w; sx*sx+sy*sy < 0.085*0.085 {
		c = sun
	}
	if v > 0.58+0.10*math.Sin(u*math.Pi*1.4+0.6) {
		c = hillBk
	}
	if v > 0.72+0.09*math.Sin(u*math.Pi*1.1+2.6) {
		c = hillFr
	}
	return c
}

func main() {
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	const n = 4
	for py := 0; py < size; py++ {
		for px := 0; px < size; px++ {
			var sum rgba
			for sy := 0; sy < n; sy++ {
				for sx := 0; sx < n; sx++ {
					c := shade(float64(px)+(float64(sx)+0.5)/n, float64(py)+(float64(sy)+0.5)/n)
					// Premultiply so transparent samples do not darken the edge.
					sum.r += c.r * c.a
					sum.g += c.g * c.a
					sum.b += c.b * c.a
					sum.a += c.a
				}
			}
			if sum.a == 0 {
				continue
			}
			img.SetNRGBA(px, py, color.NRGBA{
				R: uint8(sum.r / sum.a), G: uint8(sum.g / sum.a), B: uint8(sum.b / sum.a),
				A: uint8(sum.a / (n * n)),
			})
		}
	}
	f, err := os.Create(os.Args[1])
	if err != nil {
		panic(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		panic(err)
	}
}
