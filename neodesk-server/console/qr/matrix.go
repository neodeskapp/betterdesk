package qr

// matrix is a code while it is being made. A module is addressed by row and
// column, both counted from the top left corner.
type matrix struct {
	size int
	dark []bool
	// function marks the modules of the fixed patterns and of the format
	// and version information. Data never goes there and masks leave them
	// alone.
	function []bool
}

func newMatrix(version int) *matrix {
	size := version*4 + 17
	return &matrix{
		size:     size,
		dark:     make([]bool, size*size),
		function: make([]bool, size*size),
	}
}

// setFunction sets a module of a fixed pattern.
func (m *matrix) setFunction(row, col int, dark bool) {
	m.dark[row*m.size+col] = dark
	m.function[row*m.size+col] = true
}

// drawFunctionPatterns draws everything that does not depend on the text
// or the mask: the three finder patterns, the timing patterns, the
// alignment patterns and, from version 7 on, the version information.
func (m *matrix) drawFunctionPatterns(version int) {
	m.drawFinder(0, 0)
	m.drawFinder(m.size-7, 0)
	m.drawFinder(0, m.size-7)
	m.drawTiming()
	m.drawAlignment(version)
	if version >= 7 {
		m.drawVersion(version)
	}
}

// drawFinder draws a finder pattern with its top left corner at row, col:
// a dark 7x7 square with a light ring and a dark 3x3 centre, and the light
// separator of one module around it where that is inside the code (ISO/IEC
// 18004:2015, 6.3.3 and 6.3.4).
func (m *matrix) drawFinder(row, col int) {
	for r := -1; r <= 7; r++ {
		for c := -1; c <= 7; c++ {
			if row+r < 0 || row+r >= m.size || col+c < 0 || col+c >= m.size {
				continue
			}
			inSquare := r >= 0 && r <= 6 && c >= 0 && c <= 6
			onEdge := r == 0 || r == 6 || c == 0 || c == 6
			inCentre := r >= 2 && r <= 4 && c >= 2 && c <= 4
			m.setFunction(row+r, col+c, inSquare && (onEdge || inCentre))
		}
	}
}

// drawTiming draws the two timing patterns: row 6 and column 6 between the
// finder patterns, dark and light in turn, dark on even positions (ISO/IEC
// 18004:2015, 6.3.5).
func (m *matrix) drawTiming() {
	for i := 8; i < m.size-8; i++ {
		m.setFunction(i, 6, i%2 == 0)
		m.setFunction(6, i, i%2 == 0)
	}
}

// alignmentCentres holds the row and column coordinates of the centres of
// the alignment patterns for each version, version 1 first (ISO/IEC
// 18004:2015, annex E, table E.1). A pattern sits at every pair of
// coordinates except the three corners taken by the finder patterns.
var alignmentCentres = [40][]int{
	{},
	{6, 18},
	{6, 22},
	{6, 26},
	{6, 30},
	{6, 34},
	{6, 22, 38},
	{6, 24, 42},
	{6, 26, 46},
	{6, 28, 50},
	{6, 30, 54},
	{6, 32, 58},
	{6, 34, 62},
	{6, 26, 46, 66},
	{6, 26, 48, 70},
	{6, 26, 50, 74},
	{6, 30, 54, 78},
	{6, 30, 56, 82},
	{6, 30, 58, 86},
	{6, 34, 62, 90},
	{6, 28, 50, 72, 94},
	{6, 26, 50, 74, 98},
	{6, 30, 54, 78, 102},
	{6, 28, 54, 80, 106},
	{6, 32, 58, 84, 110},
	{6, 30, 58, 86, 114},
	{6, 34, 62, 90, 118},
	{6, 26, 50, 74, 98, 122},
	{6, 30, 54, 78, 102, 126},
	{6, 26, 52, 78, 104, 130},
	{6, 30, 56, 82, 108, 134},
	{6, 34, 60, 86, 112, 138},
	{6, 30, 58, 86, 114, 142},
	{6, 34, 62, 90, 118, 146},
	{6, 30, 54, 78, 102, 126, 150},
	{6, 24, 50, 76, 102, 128, 154},
	{6, 28, 54, 80, 106, 132, 158},
	{6, 32, 58, 84, 110, 136, 162},
	{6, 26, 54, 82, 110, 138, 166},
	{6, 30, 58, 86, 114, 142, 170},
}

// drawAlignment draws the alignment patterns: 5x5 squares with a dark
// border, a light ring and a dark centre (ISO/IEC 18004:2015, 6.3.6).
func (m *matrix) drawAlignment(version int) {
	centres := alignmentCentres[version-1]
	last := len(centres) - 1
	for i, row := range centres {
		for j, col := range centres {
			if (i == 0 && j == 0) || (i == 0 && j == last) || (i == last && j == 0) {
				continue
			}
			for r := -2; r <= 2; r++ {
				for c := -2; c <= 2; c++ {
					onEdge := r == -2 || r == 2 || c == -2 || c == 2
					m.setFunction(row+r, col+c, onEdge || (r == 0 && c == 0))
				}
			}
		}
	}
}

// bchRemainder divides value by a generator polynomial, both taken as
// polynomials with bits as coefficients, and returns the remainder.
func bchRemainder(value, generator int) int {
	top := bitLength(generator)
	for bitLength(value) >= top {
		value ^= generator << (bitLength(value) - top)
	}
	return value
}

// bitLength is the position of the highest set bit, counted from 1.
func bitLength(v int) int {
	n := 0
	for ; v != 0; v >>= 1 {
		n++
	}
	return n
}

// formatBits is the 15-bit format information: two bits for the level and
// three for the mask, ten check bits of the BCH (15,5) code with the
// generator x^10 + x^8 + x^5 + x^4 + x^2 + x + 1 (0x537), and the whole
// XORed with 101010000010010 (0x5412) so it is never all zero (ISO/IEC
// 18004:2015, 7.9.1 and annex C).
func formatBits(level Level, mask int) int {
	data := int(level)<<3 | mask
	return (data<<10 | bchRemainder(data<<10, 0x537)) ^ 0x5412
}

// versionBits is the 18-bit version information: six bits for the version
// and twelve check bits of the BCH (18,6) code with the generator x^12 +
// x^11 + x^10 + x^9 + x^8 + x^5 + x^2 + 1 (0x1F25) (ISO/IEC 18004:2015,
// 7.10 and annex D).
func versionBits(version int) int {
	return version<<12 | bchRemainder(version<<12, 0x1F25)
}

// drawFormat writes the format information twice: once around the top left
// finder pattern, and once split over the bottom left and top right ones.
// It also sets the module above the bottom left finder pattern that is
// always dark (ISO/IEC 18004:2015, 7.9.1).
func (m *matrix) drawFormat(level Level, mask int) {
	bits := formatBits(level, mask)
	for i := 0; i < 15; i++ {
		dark := bits>>i&1 == 1

		// Down column 8: bits 0 to 7 at the top, stepping over the timing
		// row, bits 8 to 14 at the bottom.
		switch {
		case i < 6:
			m.setFunction(i, 8, dark)
		case i < 8:
			m.setFunction(i+1, 8, dark)
		default:
			m.setFunction(m.size-15+i, 8, dark)
		}

		// Along row 8 from the right: bits 0 to 7 at the right end, bits 8
		// to 14 at the left, stepping over the timing column.
		switch {
		case i < 8:
			m.setFunction(8, m.size-1-i, dark)
		case i == 8:
			m.setFunction(8, 7, dark)
		default:
			m.setFunction(8, 14-i, dark)
		}
	}
	m.setFunction(m.size-8, 8, true)
}

// drawVersion writes the version information twice: in a block of 3x6
// modules left of the top right finder pattern and, mirrored, in a block of
// 6x3 above the bottom left one. The lowest bit comes first (ISO/IEC
// 18004:2015, 7.10).
func (m *matrix) drawVersion(version int) {
	bits := versionBits(version)
	for i := 0; i < 18; i++ {
		dark := bits>>i&1 == 1
		row := i / 3
		col := m.size - 11 + i%3
		m.setFunction(row, col, dark)
		m.setFunction(col, row, dark)
	}
}

// placeData lays the codewords into the modules that hold no fixed pattern
// (ISO/IEC 18004:2015, 7.7.3).
//
// The path starts in the bottom right corner and runs in columns two
// modules wide, up the first, down the next and so on, skipping the timing
// column. In every row of such a column the right module comes before the
// left one. Bits go in from the highest bit of the first codeword. Modules
// left over after the last codeword stay light.
func (m *matrix) placeData(codewords []byte) {
	bit := 0
	upward := true
	for right := m.size - 1; right > 0; right -= 2 {
		if right == 6 {
			right--
		}
		for step := 0; step < m.size; step++ {
			row := step
			if upward {
				row = m.size - 1 - step
			}
			for _, col := range [2]int{right, right - 1} {
				at := row*m.size + col
				if m.function[at] {
					continue
				}
				m.dark[at] = bit < len(codewords)*8 && codewords[bit/8]>>(7-bit%8)&1 == 1
				bit++
			}
		}
		upward = !upward
	}
}
