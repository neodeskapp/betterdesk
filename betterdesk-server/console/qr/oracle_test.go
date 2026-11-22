package qr

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"testing"
)

// These tests replay what the npm package qrcode produced for the same
// input. testdata/README.md says what the files hold.

// readLines decodes a gzip-compressed file of JSON objects, one per line.
func readLines[T any](t *testing.T, name string) []T {
	t.Helper()
	file, err := os.Open(name)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	unzipped, err := gzip.NewReader(file)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	var lines []T
	decoder := json.NewDecoder(unzipped)
	for {
		var line T
		if err := decoder.Decode(&line); errors.Is(err, io.EOF) {
			return lines
		} else if err != nil {
			t.Fatalf("%s: line %d: %v", name, len(lines)+1, err)
		}
		lines = append(lines, line)
	}
}

var levelNames = map[string]Level{"L": L, "M": M, "Q": Q, "H": H}

// packModules writes the modules row by row, one bit each, the first in the
// highest bit of the first byte.
func packModules(c *Code) []byte {
	n := c.Size()
	packed := make([]byte, (n*n+7)/8)
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			if c.Black(x, y) {
				packed[(y*n+x)/8] |= 0x80 >> ((y*n + x) % 8)
			}
		}
	}
	return packed
}

// shorten keeps failure messages readable for texts of thousands of
// characters.
func shorten(text string) string {
	if len(text) > 80 {
		return fmt.Sprintf("%q... (%d bytes)", text[:80], len(text))
	}
	return fmt.Sprintf("%q", text)
}

func TestSameModulesAsNpmPackage(t *testing.T) {
	type vector struct {
		Text    string `json:"t"`
		Raw     string `json:"x"` // base64, instead of Text
		Level   string `json:"l"`
		Refused int    `json:"e"`
		Version int    `json:"v"`
		Mask    int    `json:"m"`
		Modules string `json:"b"` // base64
	}
	vectors := readLines[vector](t, "testdata/vectors.jsonl.gz")

	// Guard against a cut or thinned out vector file.
	if len(vectors) < 5000 {
		t.Fatalf("only %d vectors", len(vectors))
	}
	seenVersion := map[string]bool{}
	seenMask := map[int]bool{}
	refused, failures := 0, 0

	for i, v := range vectors {
		level, ok := levelNames[v.Level]
		if !ok {
			t.Fatalf("vector %d: level %q", i, v.Level)
		}
		text := v.Text
		if v.Raw != "" {
			raw, err := base64.StdEncoding.DecodeString(v.Raw)
			if err != nil {
				t.Fatalf("vector %d: %v", i, err)
			}
			text = string(raw)
		}
		fail := func(format string, args ...any) {
			t.Errorf("vector %d, level %s, text %s: %s", i, v.Level, shorten(text), fmt.Sprintf(format, args...))
			if failures++; failures >= 10 {
				t.Fatal("stopping after 10 vectors that differ")
			}
		}

		code, err := Encode(text, level)
		if v.Refused == 1 {
			refused++
			if err == nil {
				fail("the npm package refuses this input, Encode made a version %d code", code.version)
			}
			continue
		}
		if err != nil {
			fail("Encode: %v", err)
			continue
		}
		seenVersion[fmt.Sprintf("%s%d", v.Level, v.Version)] = true
		seenMask[v.Mask] = true
		if code.version != v.Version {
			fail("version %d, want %d", code.version, v.Version)
			continue
		}
		if code.mask != v.Mask {
			fail("mask %d, want %d", code.mask, v.Mask)
			continue
		}
		want, err := base64.StdEncoding.DecodeString(v.Modules)
		if err != nil {
			t.Fatalf("vector %d: %v", i, err)
		}
		if got := packModules(code); !bytes.Equal(got, want) {
			differing := 0
			for k := range got {
				if k >= len(want) || got[k] != want[k] {
					differing++
				}
			}
			fail("version %d, mask %d: %d of %d module bytes differ", v.Version, v.Mask, differing, len(got))
		}
	}

	if len(seenVersion) != 160 {
		t.Errorf("the vectors cover %d of the 160 pairs of version and level", len(seenVersion))
	}
	if len(seenMask) != 8 {
		t.Errorf("the vectors cover %d of the 8 masks", len(seenMask))
	}
	if refused < 10 {
		t.Errorf("only %d vectors with refused input", refused)
	}
}

func decodePNG(t *testing.T, what string, data []byte) image.Image {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("%s: %v", what, err)
	}
	return img
}

func TestSamePixelsAsNpmPackage(t *testing.T) {
	type picture struct {
		Text   string `json:"t"`
		Level  string `json:"l"`
		Width  int    `json:"w"`
		Margin int    `json:"g"`
		PNG    string `json:"p"` // base64
	}
	pictures := readLines[picture](t, "testdata/images.jsonl.gz")
	if len(pictures) < 200 {
		t.Fatalf("only %d images", len(pictures))
	}

	narrower := 0 // images the npm package made smaller than the width asked for
	for i, p := range pictures {
		what := fmt.Sprintf("image %d (level %s, width %d, margin %d, text %s)", i, p.Level, p.Width, p.Margin, shorten(p.Text))
		theirs, err := base64.StdEncoding.DecodeString(p.PNG)
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
		want := decodePNG(t, what+" from npm", theirs)

		code, err := Encode(p.Text, levelNames[p.Level])
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
		ours, err := code.PNG(Options{Width: p.Width, Margin: p.Margin})
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
		got := decodePNG(t, what, ours)

		if got.Bounds() != want.Bounds() {
			t.Errorf("%s: image is %v, want %v", what, got.Bounds(), want.Bounds())
			continue
		}
		if p.Width >= code.Size()+2*p.Margin && want.Bounds().Dx() < p.Width {
			narrower++
		}
		differing := 0
		for y := want.Bounds().Min.Y; y < want.Bounds().Max.Y; y++ {
			for x := want.Bounds().Min.X; x < want.Bounds().Max.X; x++ {
				if color.NRGBAModel.Convert(got.At(x, y)) != color.NRGBAModel.Convert(want.At(x, y)) {
					differing++
				}
			}
		}
		if differing > 0 {
			t.Errorf("%s: %d of %d pixels differ", what, differing, want.Bounds().Dx()*want.Bounds().Dy())
		}
	}

	// A version 7 code with a margin of 2 is 49 modules wide, and 49 times
	// 256/49 comes out just under 256 in floating point. The npm package
	// then draws 255 pixels. The vectors must hold at least that case.
	if narrower == 0 {
		t.Error("no image where the npm package drew one pixel less than the width; the vectors lost their rounding case")
	}
}
