package avatars

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"

	// GIF pictures decode too; WebP has no decoder in the standard library
	_ "image/gif"
)

// maxThumbSourcePixels bounds the picture Thumbnail decodes: a small file
// can name a huge size.
const maxThumbSourcePixels = 4096 * 4096

// Thumbnail returns a picture as a data URL of at most side by side pixels
// and at most maxLen characters, the data: prefix included: a PNG when the
// picture has see-through pixels and that fits, else a JPEG. A picture
// that is smaller already keeps its size. ok is false for a picture it
// can't decode (WebP among them) and for one that does not fit in maxLen.
func Thumbnail(img Image, side, maxLen int) (string, bool) {
	if side <= 0 || maxLen <= 0 || len(img.Data) == 0 {
		return "", false
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(img.Data))
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width*cfg.Height > maxThumbSourcePixels {
		return "", false
	}
	src, _, err := image.Decode(bytes.NewReader(img.Data))
	if err != nil {
		return "", false
	}
	small, seeThrough := shrink(src, side)
	fits := func(mime string, data []byte) (string, bool) {
		url := "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)
		return url, len(url) <= maxLen
	}
	if seeThrough {
		var buf bytes.Buffer
		if png.Encode(&buf, small) == nil {
			if url, ok := fits(TypePNG, buf.Bytes()); ok {
				return url, true
			}
		}
		small = onWhite(small)
	}
	for _, quality := range []int{85, 70, 55, 40} {
		var buf bytes.Buffer
		if jpeg.Encode(&buf, small, &jpeg.Options{Quality: quality}) != nil {
			return "", false
		}
		if url, ok := fits(TypeJPEG, buf.Bytes()); ok {
			return url, true
		}
	}
	return "", false
}

// shrink scales src down to fit side by side, keeping its shape, by the
// mean of the pixels under each new pixel. It reports whether any pixel is
// see-through.
func shrink(src image.Image, side int) (*image.RGBA, bool) {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	dw, dh := w, h
	if w > side || h > side {
		if w >= h {
			dw, dh = side, max(1, (h*side+w/2)/w)
		} else {
			dw, dh = max(1, (w*side+h/2)/h), side
		}
	}
	out := image.NewRGBA(image.Rect(0, 0, dw, dh))
	seeThrough := false
	for y := 0; y < dh; y++ {
		y0, y1 := b.Min.Y+y*h/dh, b.Min.Y+(y+1)*h/dh
		if y1 <= y0 {
			y1 = y0 + 1
		}
		for x := 0; x < dw; x++ {
			x0, x1 := b.Min.X+x*w/dw, b.Min.X+(x+1)*w/dw
			if x1 <= x0 {
				x1 = x0 + 1
			}
			var r, g, bl, a, n uint64
			for sy := y0; sy < y1; sy++ {
				for sx := x0; sx < x1; sx++ {
					pr, pg, pb, pa := src.At(sx, sy).RGBA()
					r, g, bl, a = r+uint64(pr), g+uint64(pg), bl+uint64(pb), a+uint64(pa)
					n++
				}
			}
			px := color.RGBA64{R: uint16(r / n), G: uint16(g / n), B: uint16(bl / n), A: uint16(a / n)}
			if px.A != 0xffff {
				seeThrough = true
			}
			out.Set(x, y, px)
		}
	}
	return out, seeThrough
}

// onWhite puts a picture with see-through pixels on white, for a JPEG.
func onWhite(img *image.RGBA) *image.RGBA {
	out := image.NewRGBA(img.Bounds())
	for i := 0; i+3 < len(img.Pix); i += 4 {
		a := uint32(img.Pix[i+3])
		for c := 0; c < 3; c++ {
			// premultiplied: the colour plus white behind what is see-through
			out.Pix[i+c] = uint8(uint32(img.Pix[i+c]) + 255 - a)
		}
		out.Pix[i+3] = 255
	}
	return out
}
