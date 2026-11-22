// Package qr makes QR codes (ISO/IEC 18004:2015, the usual "model 2" symbol)
// and draws them as PNG images, with the standard library only.
//
// The console shows three codes: the otpauth address for two-factor setup,
// the client configuration address and the server's public key. A phone
// scans them from an <img> that holds a data URL.
//
// How a code is made, in the order of the files:
//
//   - segments.go cuts the text into parts that are written as digits, as
//     the 45 character alphanumeric set or as bytes, and picks the cut that
//     needs the fewest bits.
//   - version.go holds the capacity tables and finds the smallest of the 40
//     versions (sizes) that holds the parts at the wanted error correction
//     level.
//   - bits.go writes the parts into data codewords and fills the rest with
//     padding.
//   - reedsolomon.go adds the error correction codewords and interleaves the
//     blocks.
//   - matrix.go draws the fixed patterns and lays the codewords into the
//     free modules.
//   - mask.go tries the eight masks and keeps the one with the lowest
//     penalty.
//   - png.go scales the modules to pixels.
//
// For the same text and level the package gives the same modules as the npm
// package qrcode 1.5.4, which is what the vectors in testdata come from.
// That fixes three choices the standard leaves open: how the text is cut
// into parts, that the lowest mask number wins a tie, and how the image
// width is turned into a pixel grid.
package qr

import (
	"errors"
	"fmt"
)

// Level is the error correction level: how much of the code may be damaged
// and still be read. A higher level leaves less room for text.
//
// The values are the two-bit indicators the format information carries
// (ISO/IEC 18004:2015, 7.9.1, table "Error correction level indicators"),
// which makes M the zero value.
type Level int

const (
	M Level = 0 // about 15 % of the codewords can be restored
	L Level = 1 // about 7 %
	H Level = 2 // about 30 %
	Q Level = 3 // about 25 %
)

func (l Level) valid() bool { return l >= 0 && l <= 3 }

// Options say how a code is made and drawn.
type Options struct {
	// Level is the error correction level. The zero value is M.
	Level Level
	// Width is the wanted image width in pixels. The modules are stretched
	// to fill it. When it is smaller than the number of modules, margin
	// included, every module is 4 pixels wide instead.
	Width int
	// Margin is the white border in modules. Zero means no border; a
	// negative value means the 4 modules the standard asks for.
	Margin int
}

// ErrEmpty is returned for an empty text.
var ErrEmpty = errors.New("qr: no text to encode")

// ErrTooLong is returned for a text that does not fit into the largest code
// at the wanted level.
var ErrTooLong = errors.New("qr: the text is too long for a QR code")

// Code is a finished QR code: a square of dark and light modules.
type Code struct {
	version int
	level   Level
	mask    int
	size    int
	dark    []bool // row after row, size*size modules
}

// Encode makes the code for a text. It uses the smallest version that holds
// the text at the given level. The text goes in byte for byte: UTF-8 stays
// UTF-8, and bytes that are not valid UTF-8 are kept as they are.
func Encode(text string, level Level) (*Code, error) {
	if !level.valid() {
		return nil, fmt.Errorf("qr: unknown error correction level %d", int(level))
	}
	if text == "" {
		return nil, ErrEmpty
	}
	segments, version, err := plan(text, level)
	if err != nil {
		return nil, err
	}
	codewords := addErrorCorrection(dataCodewords(segments, version, level), version, level)

	m := newMatrix(version)
	m.drawFunctionPatterns(version)
	m.drawFormat(level, 0)
	m.placeData(codewords)
	mask := m.bestMask(level)
	m.applyMask(mask)
	m.drawFormat(level, mask)

	return &Code{version: version, level: level, mask: mask, size: m.size, dark: m.dark}, nil
}

// Size is the number of modules along one side, without any margin.
func (c *Code) Size() int { return c.size }

// Black reports whether the module in column x and row y is dark. Outside
// the code it is false.
func (c *Code) Black(x, y int) bool {
	if x < 0 || y < 0 || x >= c.size || y >= c.size {
		return false
	}
	return c.dark[y*c.size+x]
}
