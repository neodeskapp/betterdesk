// Package brandmail is the one layout of every mail the console sends. A
// sender writes what its mail says as a Mail (subject, preheader, heading,
// lead, one button, details, why the reader gets it) in the reader's
// language; Render lays it out in the server's branding as an HTML part
// and a plain text part that say the same, with the logo as inline
// images and the footer that says which server sent it and who runs it.
// The mail service (package mail) renders every mail it sends this way
// and sends nothing else.
//
// The HTML is a table layout with inline styles, 600 pixels wide and
// fluid below that, without scripts, external style sheets or remote
// images. It has the brand's light palette inline and its dark palette
// for clients that follow dark mode, a button Outlook for Windows draws
// too (VML), and it says its language and direction. Its contrast meets
// WCAG 2.2 AA in both palettes for any accent colour (see Palette).
package brandmail

import (
	"bytes"
	"embed"
	"image"
	"image/color"
	"image/draw"
	_ "image/gif"  // logos the server's admin uploads
	_ "image/jpeg" // logos the server's admin uploads
	"image/png"
	"math"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/neodeskapp/neodesk/neodesk-server/console/mail/message"
)

// DefaultServer is the server's name until an admin gives it another.
const DefaultServer = "NeoDesk"

// Branding is what a mail shows of the server that sends it.
type Branding struct {
	// Server is the console's name (Settings, Appearance), "NeoDesk" by
	// default.
	Server string
	// Accent is the branding's accent colour (#rrggbb); anything else, and
	// the console's own blues, is the brand's default.
	Accent string
	// LogoImage is the logo an admin uploaded, as a PNG, JPEG or GIF, and
	// LogoType its content type. Without one the mail shows NeoDesk's
	// logo, or NeoDesk's mark and the server's name when the server has a
	// name of its own.
	LogoImage []byte
	LogoType  string
	// Host is the console's address without the scheme
	// ("internal.example.com"), for the footer.
	Host string
	// Contact is who runs the server, as the admin wrote it in the mail
	// settings; empty fields are left out of the footer.
	Contact Contact
}

// Contact is who runs a server and how to reach them.
type Contact struct {
	// Organisation runs the server ("Acme IT").
	Organisation string
	// Email and URL are where its users get help.
	Email string
	URL   string
}

// OwnName reports whether the server has a name of its own, not the
// default "NeoDesk".
func (b Branding) OwnName() bool {
	name := strings.TrimSpace(b.Server)
	return name != "" && !strings.EqualFold(name, DefaultServer)
}

// Name is the server's name, "NeoDesk" when it has none.
func (b Branding) Name() string {
	if name := strings.TrimSpace(b.Server); name != "" {
		return name
	}
	return DefaultServer
}

// Fact is one row of a mail's label and value table.
type Fact struct{ Label, Value string }

// DefaultAccent is the button colour without an accent of the server's
// own: the brand's blue for "your server" (BRAND.md), which reaches
// 4.6:1 with white text.
const DefaultAccent = "#1f6feb"

var hexColour = regexp.MustCompile(`^#([0-9a-fA-F]{6}|[0-9a-fA-F]{3})$`)

// Accent is the accent as #rrggbb in lower case, the default for
// anything that is not a colour.
func Accent(c string) string {
	c = strings.TrimSpace(c)
	if !hexColour.MatchString(c) {
		return DefaultAccent
	}
	c = strings.ToLower(c)
	if len(c) == 4 {
		c = "#" + strings.Repeat(c[1:2], 2) + strings.Repeat(c[2:3], 2) + strings.Repeat(c[3:4], 2)
	}
	return c
}

// The two text colours a button can have.
const (
	white     = "#ffffff"
	nearBlack = "#111827"
)

func rgbOf(c string) [3]float64 {
	var out [3]float64
	for i := range out {
		v, _ := strconv.ParseUint(c[1+2*i:3+2*i], 16, 8)
		out[i] = float64(v)
	}
	return out
}

func hexOf(rgb [3]float64) string {
	out := "#"
	for _, v := range rgb {
		out += strconv.FormatUint(uint64(math.Round(math.Min(math.Max(v, 0), 255)))|0x100, 16)[1:]
	}
	return out
}

func luminance(c string) float64 {
	lum := 0.0
	for i, w := range []float64{0.2126, 0.7152, 0.0722} {
		v := rgbOf(c)[i] / 255
		if v <= 0.03928 {
			v /= 12.92
		} else {
			v = math.Pow((v+0.055)/1.055, 2.4)
		}
		lum += w * v
	}
	return lum
}

// Contrast is the WCAG contrast ratio of two #rrggbb colours.
func Contrast(a, b string) float64 {
	x, y := luminance(a), luminance(b)
	if x < y {
		x, y = y, x
	}
	return (x + 0.05) / (y + 0.05)
}

// OnAccent is the text colour on the accent: white or near black,
// whichever has the higher contrast on it.
func OnAccent(accent string) string {
	if Contrast(white, accent) >= Contrast(nearBlack, accent) {
		return white
	}
	return nearBlack
}

// Button is the button's colour and its text colour for an accent on a
// page colour: the accent where its text (white or near black) reaches
// 4.5:1 on it and the button 3:1 on the page, else the accent moved
// step by step away from the page (darker on a light page, lighter on a
// dark one) until both hold.
func Button(accent, page string) (bg, fg string) {
	accent = Accent(accent)
	ok := func(c string) (string, bool) {
		on := OnAccent(c)
		return on, Contrast(on, c) >= 4.5 && Contrast(c, page) >= 3
	}
	if on, good := ok(accent); good {
		return accent, on
	}
	rgb := rgbOf(accent)
	towards := 0.0
	if luminance(page) < 0.2 {
		towards = 255
	}
	for f := 0.02; f <= 1.0001; f += 0.02 {
		var moved [3]float64
		for i, v := range rgb {
			moved[i] = v + (towards-v)*f
		}
		c := hexOf(moved)
		if on, good := ok(c); good {
			return c, on
		}
	}
	if towards == 0 {
		return "#000000", white
	}
	return "#ffffff", nearBlack
}

// Palette is one of the mail's two colour sets: the console's light and
// dark palettes from the brand guide (BRAND.md), and the button in it.
type Palette struct {
	Page, Ink, Muted, Rule, Quote, Link string
	Button, OnButton                    string
}

var (
	lightBase = Palette{Page: "#ffffff", Ink: "#1b1e1f", Muted: "#566064", Rule: "#dde1e2", Quote: "#f3f4f4", Link: "#1b1e1f"}
	darkBase  = Palette{Page: "#141616", Ink: "#e4e7e7", Muted: "#a3abad", Rule: "#2c3131", Quote: "#1f2323", Link: "#e4e7e7"}
)

// defaultAccents are the accents of the console's own themes: an
// appearance with one of them has no accent of its own.
var defaultAccents = map[string]bool{"#58a6ff": true, "#1b62cf": true, "#1d4ed8": true, "#1f6feb": true, "#00677a": true, "#62c4d0": true}

// Palettes are the light and the dark palette for a branding's accent.
func Palettes(accent string) (light, dark Palette) {
	a := Accent(accent)
	if strings.TrimSpace(accent) == "" || defaultAccents[a] {
		a = DefaultAccent
	}
	light, dark = lightBase, darkBase
	light.Button, light.OnButton = Button(a, light.Page)
	dark.Button, dark.OnButton = Button(a, dark.Page)
	return light, dark
}

const (
	mailFontList = "'IBM Plex Sans', -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, Helvetica, Arial, sans-serif"
	monoFontList = "'IBM Plex Mono', ui-monospace, Menlo, Consolas, 'Courier New', monospace"
)

//go:embed logo/*.png
var logoFiles embed.FS

// The logo images a mail carries, by Content-ID.
const (
	cidLogoLight = "logo-light@neodesk"
	cidLogoDark  = "logo-dark@neodesk"
	cidLogoOwn   = "logo@server"
)

func readLogo(name string) []byte {
	data, err := logoFiles.ReadFile(name)
	if err != nil {
		panic("brandmail: embedded logo missing: " + name)
	}
	return data
}

// The light logo's plate: 8 pixels of white around it on screen (16 in
// the image, which is drawn at twice the size), with the brand's 4 pixel
// corners.
const (
	platePad    = 16
	plateRadius = 8
)

var (
	plateOnce sync.Once
	plateData []byte
)

// platedLogo is NeoDesk's light logo on an opaque white plate. The light
// logo has a dark wordmark on a transparent ground, which disappears in
// clients that turn the mail dark by inverting its colours but leave its
// images as they are (the Gmail apps, Outlook for Windows). On the plate
// it stays readable there, and in a light mail the plate is the page's
// own white. The artwork is drawn on the plate pixel for pixel, not
// changed.
func platedLogo() []byte {
	plateOnce.Do(func() {
		art, err := png.Decode(bytes.NewReader(readLogo("logo/logo-on-light-64h.png")))
		if err != nil {
			panic("brandmail: embedded logo is no PNG")
		}
		b := art.Bounds()
		w, h := b.Dx()+2*platePad, b.Dy()+2*platePad
		out := image.NewNRGBA(image.Rect(0, 0, w, h))
		r := float64(plateRadius)
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				cx := math.Max(math.Max(r-float64(x)-0.5, float64(x)+0.5-float64(w)+r), 0)
				cy := math.Max(math.Max(r-float64(y)-0.5, float64(y)+0.5-float64(h)+r), 0)
				a := math.Min(math.Max(r-math.Hypot(cx, cy)+0.5, 0), 1)
				if cx == 0 || cy == 0 {
					a = 1
				}
				out.SetNRGBA(x, y, color.NRGBA{255, 255, 255, uint8(math.Round(a * 255))})
			}
		}
		draw.Draw(out, b.Sub(b.Min).Add(image.Pt(platePad, platePad)), art, b.Min, draw.Over)
		var buf bytes.Buffer
		if err := png.Encode(&buf, out); err != nil {
			panic("brandmail: " + err.Error())
		}
		plateData = buf.Bytes()
	})
	return plateData
}

// logoKinds says how a mail shows the server.
type logoKind int

const (
	logoNeoDesk logoKind = iota // NeoDesk's full logo
	logoMark                    // NeoDesk's mark and the server's name
	logoOwn                     // the logo the admin uploaded
)

// ownLogo is the uploaded logo when a mail can carry it: a PNG, JPEG or
// GIF that decodes, with its size. SVG and WebP can't be shown by every
// client; the mail then shows the server's name with NeoDesk's mark.
func ownLogo(b Branding) (w, h int, ok bool) {
	switch b.LogoType {
	case "image/png", "image/jpeg", "image/gif":
	default:
		return 0, 0, false
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(b.LogoImage))
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 || "image/"+format != b.LogoType {
		return 0, 0, false
	}
	return cfg.Width, cfg.Height, true
}

func kindOf(b Branding) logoKind {
	if _, _, ok := ownLogo(b); ok {
		return logoOwn
	}
	if b.OwnName() {
		return logoMark
	}
	return logoNeoDesk
}

// ownLogoSize is the uploaded logo's size in the mail: 40 pixels high, at
// most 240 wide, in its own proportions.
func ownLogoSize(b Branding) (w, h int) {
	iw, ih, _ := ownLogo(b)
	h = 40
	w = int(math.Round(float64(iw) * 40 / float64(ih)))
	if w > 240 {
		w = 240
		h = int(math.Max(1, math.Round(float64(ih)*240/float64(iw))))
	}
	return w, h
}

// Inline are the images a mail's HTML shows by Content-ID: NeoDesk's logo
// for light mode on its plate and for dark mode, or its mark for both
// when the server has a name of its own, or the logo the admin uploaded.
func Inline(b Branding) []message.Inline {
	switch kindOf(b) {
	case logoOwn:
		ext := map[string]string{"image/png": "png", "image/jpeg": "jpg", "image/gif": "gif"}[b.LogoType]
		return []message.Inline{{ID: cidLogoOwn, ContentType: b.LogoType, Filename: "logo." + ext, Data: b.LogoImage}}
	case logoMark:
		return []message.Inline{
			{ID: cidLogoLight, ContentType: "image/png", Filename: "neodesk-mark.png", Data: readLogo("logo/mark-on-light-64.png")},
			{ID: cidLogoDark, ContentType: "image/png", Filename: "neodesk-mark-dark.png", Data: readLogo("logo/mark-on-dark-64.png")},
		}
	}
	return []message.Inline{
		{ID: cidLogoLight, ContentType: "image/png", Filename: "neodesk-logo.png", Data: platedLogo()},
		{ID: cidLogoDark, ContentType: "image/png", Filename: "neodesk-logo-dark.png", Data: readLogo("logo/logo-on-dark-64h.png")},
	}
}
