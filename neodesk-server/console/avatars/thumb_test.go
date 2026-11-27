package avatars

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"math/rand/v2"
	"strings"
	"testing"
)

func encodePNG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// noise is a w by h picture of random pixels, see-through ones too when
// clear: the hardest picture to make small.
func noise(w, h int, clear bool) *image.NRGBA {
	r := rand.New(rand.NewPCG(1, 2))
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for i := range img.Pix {
		img.Pix[i] = uint8(r.IntN(256))
		if !clear && i%4 == 3 {
			img.Pix[i] = 255
		}
	}
	return img
}

func size(t *testing.T, url, prefix string) (int, int) {
	t.Helper()
	if !strings.HasPrefix(url, prefix) {
		t.Fatalf("%.40q does not start with %s", url, prefix)
	}
	data, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(url, prefix))
	if err != nil {
		t.Fatal(err)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	return cfg.Width, cfg.Height
}

func TestThumbnailFitsSizeAndLength(t *testing.T) {
	// A tall picture keeps its shape
	url, ok := Thumbnail(Image{MimeType: TypePNG, Data: encodePNG(t, noise(200, 400, false))}, 96, 48<<10)
	if !ok || len(url) > 48<<10 {
		t.Fatalf("noise: ok %v, %d characters", ok, len(url))
	}
	if w, h := size(t, url, "data:image/jpeg;base64,"); w != 48 || h != 96 {
		t.Fatalf("noise: %dx%d, want 48x96", w, h)
	}
	// A small picture keeps its size
	url, ok = Thumbnail(Image{MimeType: TypePNG, Data: encodePNG(t, noise(40, 30, false))}, 96, 48<<10)
	if w, h := size(t, url, "data:image/jpeg;base64,"); !ok || w != 40 || h != 30 {
		t.Fatalf("small: %v %dx%d, want 40x30", ok, w, h)
	}
	// See-through noise is too big as a PNG: it goes on white as a JPEG
	url, ok = Thumbnail(Image{MimeType: TypePNG, Data: encodePNG(t, noise(300, 300, true))}, 96, 20<<10)
	if w, h := size(t, url, "data:image/jpeg;base64,"); !ok || len(url) > 20<<10 || w != 96 || h != 96 {
		t.Fatalf("see-through noise: %v %d characters %dx%d", ok, len(url), w, h)
	}
	// A plain see-through picture stays a PNG
	plain := image.NewNRGBA(image.Rect(0, 0, 200, 200))
	for i := 0; i < len(plain.Pix); i += 4 {
		plain.Pix[i], plain.Pix[i+3] = 200, uint8(i/4%2*255)
	}
	if url, ok = Thumbnail(Image{MimeType: TypePNG, Data: encodePNG(t, plain)}, 96, 48<<10); !ok {
		t.Fatal("plain see-through picture refused")
	}
	size(t, url, "data:image/png;base64,")
}

func TestThumbnailRefusesWhatItCannotMakeSmall(t *testing.T) {
	if _, ok := Thumbnail(Image{MimeType: TypePNG, Data: encodePNG(t, noise(200, 200, false))}, 96, 200); ok {
		t.Error("a limit no JPEG fits in was met")
	}
	webp := []byte("RIFF\x24\x00\x00\x00WEBPVP8 \x18\x00\x00\x00")
	if _, ok := Thumbnail(Image{MimeType: TypeWebP, Data: webp}, 96, 48<<10); ok {
		t.Error("a WebP picture was made")
	}
	huge := image.NewGray(image.Rect(0, 0, 5000, 4000))
	huge.Set(0, 0, color.White)
	if _, ok := Thumbnail(Image{MimeType: TypePNG, Data: encodePNG(t, huge)}, 96, 48<<10); ok {
		t.Error("a picture of 20 million pixels was decoded")
	}
}
