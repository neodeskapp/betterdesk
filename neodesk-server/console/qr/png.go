package qr

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
)

// maxImageSide is the widest image PNG draws, in pixels. It keeps a wrong
// width or margin from asking for gigabytes.
const maxImageSide = 8192

// pixelGrid says which module every pixel row (and, as the image is square,
// every pixel column) shows. side is the image width in pixels; source[i]
// is the module index for pixel i, or -1 in the margin.
//
// With a width that the code and its margin fit into, the scale is the
// width divided by the number of modules, margin included, and is usually
// not a whole number. Pixel i then shows module floor((i - margin*scale) /
// scale), so modules come out one pixel wider or narrower here and there.
// The arithmetic is in float64 with each step rounded on its own, the way
// JavaScript does it, so the grid is the one the npm package qrcode draws.
// When the product of modules and scale lands just under the width, the
// image is one pixel smaller than asked.
func pixelGrid(size int, opts Options) (side int, source []int) {
	margin := opts.Margin
	if margin < 0 {
		margin = 4
	}
	modules := size + 2*margin

	scale := 4.0
	if opts.Width >= modules {
		scale = float64(opts.Width) / float64(modules)
	}
	side = int(math.Floor(float64(float64(modules) * scale)))
	scaledMargin := float64(float64(margin) * scale)

	source = make([]int, side)
	for i := range source {
		at := float64(i)
		if at >= scaledMargin && at < float64(float64(side)-scaledMargin) {
			source[i] = int(math.Floor(float64(at-scaledMargin) / scale))
		} else {
			source[i] = -1
		}
	}
	return side, source
}

// PNG draws the code as a black and white PNG image. Only Width and Margin
// of the options are used.
func (c *Code) PNG(opts Options) ([]byte, error) {
	if opts.Width > maxImageSide || opts.Margin > maxImageSide {
		return nil, fmt.Errorf("qr: width %d or margin %d is over the limit of %d pixels", opts.Width, opts.Margin, maxImageSide)
	}
	// Without a usable width a wide margin alone can still be too much.
	side, source := pixelGrid(c.size, opts)
	if side > maxImageSide {
		return nil, fmt.Errorf("qr: image width %d is over the limit of %d pixels", side, maxImageSide)
	}

	const white, black = 0, 1
	img := image.NewPaletted(image.Rect(0, 0, side, side), color.Palette{
		white: color.Gray{Y: 0xFF},
		black: color.Gray{Y: 0x00},
	})
	for y, row := range source {
		if row < 0 {
			continue
		}
		for x, col := range source {
			// The index is checked against the end of the modules so that
			// a rounding surprise at the last pixel row could only leave a
			// pixel white, never read past the code.
			if at := row*c.size + col; col >= 0 && at < len(c.dark) && c.dark[at] {
				img.Pix[y*img.Stride+x] = black
			}
		}
	}

	var out bytes.Buffer
	if err := png.Encode(&out, img); err != nil {
		return nil, fmt.Errorf("qr: write png: %w", err)
	}
	return out.Bytes(), nil
}

// PNGDataURL encodes a text and returns the image as a data URL
// ("data:image/png;base64,...") that an <img> can show.
func PNGDataURL(text string, opts Options) (string, error) {
	code, err := Encode(text, opts.Level)
	if err != nil {
		return "", err
	}
	data, err := code.PNG(opts)
	if err != nil {
		return "", err
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(data), nil
}
