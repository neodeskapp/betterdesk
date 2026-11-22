package qr

import (
	"bytes"
	"encoding/base64"
	"image/color"
	"image/png"
	"math"
	"strings"
	"testing"
)

// checkPixels decodes a PNG and compares it with the code drawn at a whole
// number of pixels per module with a margin in modules.
func checkPixels(t *testing.T, what string, data []byte, code *Code, scale, margin int) {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("%s: %v", what, err)
	}
	side := (code.Size() + 2*margin) * scale
	if img.Bounds().Dx() != side || img.Bounds().Dy() != side {
		t.Fatalf("%s: image is %dx%d, want %dx%d", what, img.Bounds().Dx(), img.Bounds().Dy(), side, side)
	}
	black := color.NRGBA{A: 0xFF}
	white := color.NRGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 0xFF}
	for y := 0; y < side; y++ {
		for x := 0; x < side; x++ {
			want := white
			// Black is false in the margin.
			if moduleX, moduleY := x/scale-margin, y/scale-margin; x >= margin*scale && y >= margin*scale && code.Black(moduleX, moduleY) {
				want = black
			}
			if got := color.NRGBAModel.Convert(img.At(x, y)); got != want {
				t.Fatalf("%s: pixel %d,%d is %v, want %v", what, x, y, got, want)
			}
		}
	}
}

func TestPNGScalesModulesToPixels(t *testing.T) {
	code, err := Encode("otpauth://totp/BetterDesk:alice?secret=JBSWY3DPEHPK3PXP&issuer=BetterDesk", M)
	if err != nil {
		t.Fatal(err)
	}
	size := code.Size() // 37 modules: version 5
	if size != 37 {
		t.Fatalf("size %d, want 37", size)
	}
	cases := []struct {
		what          string
		opts          Options
		scale, margin int
	}{
		{"width a multiple of the modules", Options{Width: (size + 4) * 6, Margin: 2}, 6, 2},
		{"width exactly the modules", Options{Width: size + 8, Margin: 4}, 1, 4},
		{"no margin", Options{Width: size * 3, Margin: 0}, 3, 0},
		{"no width: 4 pixels per module", Options{Margin: 2}, 4, 2},
		{"tiny width", Options{Width: 20, Margin: 0}, 4, 0},
		{"width one less than the modules", Options{Width: size + 3, Margin: 2}, 4, 2},
		{"negative margin: the standard's 4 modules", Options{Width: (size + 8) * 2, Margin: -1}, 2, 4},
		{"negative width", Options{Width: -5, Margin: 1}, 4, 1},
	}
	for _, c := range cases {
		data, err := code.PNG(c.opts)
		if err != nil {
			t.Fatalf("%s: %v", c.what, err)
		}
		checkPixels(t, c.what, data, code, c.scale, c.margin)
	}
}

// TestPNGWithAScaleThatIsNotWhole checks the grid for a width the modules
// do not divide: 37 modules and a margin of 2 in 256 pixels is 6.24 pixels
// per module. Pixel i shows module floor(i/6.24) of the 41, margin
// included.
func TestPNGWithAScaleThatIsNotWhole(t *testing.T) {
	code, err := Encode("otpauth://totp/BetterDesk:alice?secret=JBSWY3DPEHPK3PXP&issuer=BetterDesk", M)
	if err != nil {
		t.Fatal(err)
	}
	data, err := code.PNG(Options{Width: 256, Margin: 2})
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() != 256 || img.Bounds().Dy() != 256 {
		t.Fatalf("image is %v, want 256x256", img.Bounds())
	}
	dark := 0
	for y := 0; y < 256; y++ {
		for x := 0; x < 256; x++ {
			// Whole number arithmetic for floor(x * 41 / 256).
			moduleX, moduleY := x*41/256-2, y*41/256-2
			got := color.GrayModel.Convert(img.At(x, y)).(color.Gray).Y == 0
			if got != code.Black(moduleX, moduleY) {
				t.Fatalf("pixel %d,%d is dark=%v, module %d,%d is dark=%v", x, y, got, moduleX, moduleY, !got)
			}
			if got {
				dark++
			}
		}
	}
	if dark < 256*256/4 {
		t.Errorf("only %d dark pixels", dark)
	}
}

func TestPNGLimits(t *testing.T) {
	code, err := Encode("x", M)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := code.PNG(Options{Width: maxImageSide}); err != nil {
		t.Errorf("width %d: %v", maxImageSide, err)
	}
	if _, err := code.PNG(Options{Width: maxImageSide + 1}); err == nil {
		t.Errorf("width %d: no error", maxImageSide+1)
	}
	// 21 modules and a margin of 1014 on each side at 4 pixels per module
	// is 8196 pixels.
	if _, err := code.PNG(Options{Margin: 1014}); err == nil {
		t.Error("margin 1014: no error")
	}
	if _, err := code.PNG(Options{Margin: math.MaxInt}); err == nil {
		t.Error("huge margin: no error")
	}
	if _, err := code.PNG(Options{Width: math.MaxInt}); err == nil {
		t.Error("huge width: no error")
	}
	// Just inside the limit: a margin of 1013 gives 8188 pixels.
	if _, err := code.PNG(Options{Margin: 1013}); err != nil {
		t.Errorf("margin 1013: %v", err)
	}
}

func TestPNGDataURL(t *testing.T) {
	const text = "rustdesk://config/9JSPrxWYpJXZ0NXYt9lI6ISeltmIsISM0ETMyoTZk5CcvR3ajVGZuMXek9yL6MHc0RHaiojIpBXYiwiIlRmLw9GdrNWZk5ycnRmI6ISehxWZyJCLiUGZuA3b0t2YlRmLzdGZiojI0N3boJye"
	url, err := PNGDataURL(text, Options{Level: Q, Width: 256, Margin: 2})
	if err != nil {
		t.Fatal(err)
	}
	const prefix = "data:image/png;base64,"
	if !strings.HasPrefix(url, prefix) {
		t.Fatalf("data URL starts %q", url[:min(len(url), 30)])
	}
	data, err := base64.StdEncoding.DecodeString(url[len(prefix):])
	if err != nil {
		t.Fatalf("the data URL is not base64: %v", err)
	}
	// The image must be the code for this text at this level.
	code, err := Encode(text, Q)
	if err != nil {
		t.Fatal(err)
	}
	want, err := code.PNG(Options{Width: 256, Margin: 2})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, want) {
		t.Error("the data URL does not hold the PNG of the code at level Q")
	}
	other, err := Encode(text, M)
	if err != nil {
		t.Fatal(err)
	}
	if otherPNG, _ := other.PNG(Options{Width: 256, Margin: 2}); bytes.Equal(data, otherPNG) {
		t.Error("the level in the options made no difference")
	}

	if _, err := PNGDataURL("", Options{}); err == nil {
		t.Error("empty text: no error")
	}
	if _, err := PNGDataURL("x", Options{Width: maxImageSide + 1}); err == nil {
		t.Error("width over the limit: no error")
	}
}
