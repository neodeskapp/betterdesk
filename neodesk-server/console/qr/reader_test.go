package qr

import (
	"fmt"
	"testing"
)

// A small QR code reader for the tests. It takes a finished Code and works
// its way back to the text: where the fixed patterns are, what the format
// and version information say, the codewords, the Reed-Solomon check and
// the segments. It does not repair errors; a code the encoder made has
// none.
//
// The layout, the mask formulas, the BCH and Reed-Solomon arithmetic and
// the alphanumeric table are written out again here instead of calling the
// encoder's, so a mistake in the encoder does not cancel itself out. The
// block sizes come from the encoder's tables; the vectors from the npm
// package check those.

// wantAlignmentCentres works out the alignment pattern coordinates of a
// version from its size: the first is 6, the last is 7 from the far edge,
// and the ones between are evenly spaced with an even step, counted back
// from the last. Version 32 is the one exception in the standard's table.
func wantAlignmentCentres(version int) []int {
	if version == 1 {
		return nil
	}
	count := version/7 + 2
	size := version*4 + 17
	step := 26
	if version != 32 {
		// (size-13) / (count-1), rounded up to the next even number.
		step = (size - 13 + 2*(count-1) - 1) / (2 * (count - 1)) * 2
	}
	centres := make([]int, count)
	centres[0] = 6
	for i := count - 1; i >= 1; i-- {
		centres[i] = size - 7 - (count-1-i)*step
	}
	return centres
}

// alignmentPositions lists the centres (row, column) of all alignment
// patterns of a version: every pair of coordinates except the three corners
// where the finder patterns are.
func alignmentPositions(version int) [][2]int {
	centres := wantAlignmentCentres(version)
	last := len(centres) - 1
	var positions [][2]int
	for i, row := range centres {
		for j, col := range centres {
			if (i == 0 && j == 0) || (i == 0 && j == last) || (i == last && j == 0) {
				continue
			}
			positions = append(positions, [2]int{row, col})
		}
	}
	return positions
}

// fixedModules marks the modules of a version that never hold data.
func fixedModules(version int) [][]bool {
	size := version*4 + 17
	fixed := make([][]bool, size)
	for row := range fixed {
		fixed[row] = make([]bool, size)
	}
	mark := func(row0, col0, rows, cols int) {
		for r := row0; r < row0+rows; r++ {
			for c := col0; c < col0+cols; c++ {
				fixed[r][c] = true
			}
		}
	}
	// Finder patterns with their separators and the format information
	// beside them. The always dark module is in the bottom left block.
	mark(0, 0, 9, 9)
	mark(0, size-8, 9, 8)
	mark(size-8, 0, 8, 9)
	// Timing patterns.
	mark(6, 0, 1, size)
	mark(0, 6, size, 1)
	for _, p := range alignmentPositions(version) {
		mark(p[0]-2, p[1]-2, 5, 5)
	}
	if version >= 7 {
		mark(0, size-11, 6, 3)
		mark(size-11, 0, 3, 6)
	}
	return fixed
}

// polyRemainder divides value by generator, both polynomials with bits as
// coefficients. width is the number of bits in value.
func polyRemainder(value, generator, width int) int {
	degree := 0
	for generator>>(degree+1) != 0 {
		degree++
	}
	for bit := width - 1; bit >= degree; bit-- {
		if value>>bit&1 == 1 {
			value ^= generator << (bit - degree)
		}
	}
	return value
}

func module(c *Code, row, col int) int {
	if c.Black(col, row) {
		return 1
	}
	return 0
}

// readFormat reads both copies of the format information, checks that they
// agree and that the BCH check bits fit, and returns the level and mask.
func readFormat(c *Code) (Level, int, error) {
	n := c.Size()
	// Module positions (row, column) for bit 14 down to bit 0.
	first := [15][2]int{
		{8, 0}, {8, 1}, {8, 2}, {8, 3}, {8, 4}, {8, 5}, {8, 7}, {8, 8},
		{7, 8}, {5, 8}, {4, 8}, {3, 8}, {2, 8}, {1, 8}, {0, 8},
	}
	second := [15][2]int{
		{n - 1, 8}, {n - 2, 8}, {n - 3, 8}, {n - 4, 8}, {n - 5, 8}, {n - 6, 8}, {n - 7, 8},
		{8, n - 8}, {8, n - 7}, {8, n - 6}, {8, n - 5}, {8, n - 4}, {8, n - 3}, {8, n - 2}, {8, n - 1},
	}
	var a, b int
	for i := range first {
		a = a<<1 | module(c, first[i][0], first[i][1])
		b = b<<1 | module(c, second[i][0], second[i][1])
	}
	if a != b {
		return 0, 0, fmt.Errorf("the two copies of the format information differ: %015b and %015b", a, b)
	}
	if module(c, n-8, 8) != 1 {
		return 0, 0, fmt.Errorf("the module at row %d, column 8 must be dark", n-8)
	}
	word := a ^ 0b101010000010010
	if rest := polyRemainder(word, 0b10100110111, 15); rest != 0 {
		return 0, 0, fmt.Errorf("format information %015b fails its BCH check", a)
	}
	return Level(word >> 13), word >> 10 & 7, nil
}

// readVersion reads both copies of the version information of a code of
// version 7 or higher, checks them and returns the version number.
func readVersion(c *Code) (int, error) {
	n := c.Size()
	var a, b int
	for i := 17; i >= 0; i-- {
		a = a<<1 | module(c, i/3, n-11+i%3)
		b = b<<1 | module(c, n-11+i%3, i/3)
	}
	if a != b {
		return 0, fmt.Errorf("the two copies of the version information differ: %018b and %018b", a, b)
	}
	if rest := polyRemainder(a, 0b1111100100101, 18); rest != 0 {
		return 0, fmt.Errorf("version information %018b fails its BCH check", a)
	}
	return a >> 12, nil
}

// wantMasks are the eight mask conditions of the standard, with i the row
// and j the column. A module is flipped where the condition holds.
var wantMasks = [8]func(i, j int) bool{
	func(i, j int) bool { return (i+j)%2 == 0 },
	func(i, j int) bool { return i%2 == 0 },
	func(i, j int) bool { return j%3 == 0 },
	func(i, j int) bool { return (i+j)%3 == 0 },
	func(i, j int) bool { return (i/2+j/3)%2 == 0 },
	func(i, j int) bool { return (i*j)%2+(i*j)%3 == 0 },
	func(i, j int) bool { return ((i*j)%2+(i*j)%3)%2 == 0 },
	func(i, j int) bool { return ((i+j)%2+(i*j)%3)%2 == 0 },
}

// slowMul multiplies two elements of the field of 256 elements bit by bit.
func slowMul(a, b byte) byte {
	var product byte
	for ; b != 0; b >>= 1 {
		if b&1 == 1 {
			product ^= a
		}
		overflow := a&0x80 != 0
		a <<= 1
		if overflow {
			a ^= 0x1D // x^8 = x^4 + x^3 + x^2 + 1
		}
	}
	return product
}

// syndromesZero reports whether a block, data followed by its error
// correction codewords, is a valid Reed-Solomon codeword: as a polynomial
// it must be zero at 2^0, 2^1, ... for as many powers as there are error
// correction codewords.
func syndromesZero(block []byte, ecCount int) bool {
	root := byte(1)
	for i := 0; i < ecCount; i++ {
		var value byte
		for _, coefficient := range block {
			value = slowMul(value, root) ^ coefficient
		}
		if value != 0 {
			return false
		}
		root = slowMul(root, 2)
	}
	return true
}

// reading is what the reader found in a code.
type reading struct {
	version int
	level   Level
	mask    int
	text    []byte    // the characters of all segments, in order
	parts   []segment // the segments as they are in the code
}

// bitReader hands out the data bits of a code from the front.
type bitReader struct {
	bytes []byte
	pos   int
}

func (r *bitReader) left() int { return len(r.bytes)*8 - r.pos }

func (r *bitReader) take(n int) int {
	value := 0
	for ; n > 0; n-- {
		value = value<<1 | int(r.bytes[r.pos/8]>>(7-r.pos%8)&1)
		r.pos++
	}
	return value
}

// wantCountBits is the width of a segment's length field for the three
// version ranges 1 to 9, 10 to 26 and 27 to 40.
func wantCountBits(m mode, version int) int {
	widths := map[mode][3]int{
		numeric:      {10, 12, 14},
		alphanumeric: {9, 11, 13},
		byteMode:     {8, 16, 16},
	}[m]
	switch {
	case version <= 9:
		return widths[0]
	case version <= 26:
		return widths[1]
	default:
		return widths[2]
	}
}

const wantAlphanumeric = "0123456789" + "ABCDEFGHIJKLMNOPQRSTUVWXYZ" + " $%*+-./:"

// readCode reads a code back. It fails on anything a correct code cannot
// have: a broken format or version word, a Reed-Solomon block that does
// not check, a segment that runs past the end, wrong padding.
func readCode(c *Code) (*reading, error) {
	n := c.Size()
	if n < 21 || n > 177 || n%4 != 1 {
		return nil, fmt.Errorf("size %d is not a QR code size", n)
	}
	version := (n - 17) / 4
	level, mask, err := readFormat(c)
	if err != nil {
		return nil, err
	}
	if version >= 7 {
		v, err := readVersion(c)
		if err != nil {
			return nil, err
		}
		if v != version {
			return nil, fmt.Errorf("version information says %d, the size says %d", v, version)
		}
	}

	// Collect the data modules along the zigzag path and take the mask off.
	fixed := fixedModules(version)
	var codewords []byte
	var current byte
	count := 0
	upward := true
	for right := n - 1; right >= 1; right -= 2 {
		if right == 6 {
			right = 5
		}
		for k := 0; k < n; k++ {
			row := k
			if upward {
				row = n - 1 - k
			}
			for _, col := range []int{right, right - 1} {
				if fixed[row][col] {
					continue
				}
				bit := c.Black(col, row) != wantMasks[mask](row, col)
				current <<= 1
				if bit {
					current |= 1
				}
				if count++; count%8 == 0 {
					codewords = append(codewords, current)
					current = 0
				}
			}
		}
		upward = !upward
	}
	if current != 0 {
		return nil, fmt.Errorf("the modules left over after the last codeword are not all light")
	}
	if len(codewords) != totalCodewords[version] {
		return nil, fmt.Errorf("version %d has room for %d codewords, the table says %d", version, len(codewords), totalCodewords[version])
	}

	// Undo the interleaving and check every block.
	blocks := ecBlocks(version, level)
	ecCount := ecCodewords(version, level) / blocks
	dataTotal := len(codewords) - blocks*ecCount
	short := dataTotal / blocks
	longBlocks := dataTotal % blocks
	dataParts := make([][]byte, blocks)
	next := 0
	for i := 0; i <= short; i++ {
		for b := range dataParts {
			if i < short || b >= blocks-longBlocks {
				dataParts[b] = append(dataParts[b], codewords[next])
				next++
			}
		}
	}
	ecParts := make([][]byte, blocks)
	for i := 0; i < ecCount; i++ {
		for b := range ecParts {
			ecParts[b] = append(ecParts[b], codewords[next])
			next++
		}
	}
	var data []byte
	for b := range dataParts {
		block := append(append([]byte{}, dataParts[b]...), ecParts[b]...)
		if !syndromesZero(block, ecCount) {
			return nil, fmt.Errorf("block %d of %d fails the Reed-Solomon check", b+1, blocks)
		}
		data = append(data, dataParts[b]...)
	}

	// Read the segments.
	result := &reading{version: version, level: level, mask: mask}
	bits := &bitReader{bytes: data}
	for bits.left() >= 4 {
		m := mode(bits.take(4))
		if m == 0 {
			break
		}
		if m != numeric && m != alphanumeric && m != byteMode {
			return nil, fmt.Errorf("unknown mode indicator %04b at bit %d", m, bits.pos-4)
		}
		width := wantCountBits(m, version)
		if bits.left() < width {
			return nil, fmt.Errorf("the code ends inside a length field")
		}
		length := bits.take(width)
		var chars []byte
		for len(chars) < length {
			rest := length - len(chars)
			take, group := 8, 1
			switch {
			case m == numeric && rest >= 3:
				take, group = 10, 3
			case m == numeric:
				take, group = rest*3+1, rest
			case m == alphanumeric && rest >= 2:
				take, group = 11, 2
			case m == alphanumeric:
				take, group = 6, 1
			}
			if bits.left() < take {
				return nil, fmt.Errorf("the code ends inside a segment")
			}
			value := bits.take(take)
			switch m {
			case numeric:
				if value >= []int{0, 10, 100, 1000}[group] {
					return nil, fmt.Errorf("%d is too large for %d digits", value, group)
				}
				chars = append(chars, fmt.Sprintf("%0*d", group, value)...)
			case alphanumeric:
				if (group == 2 && value >= 45*45) || (group == 1 && value >= 45) {
					return nil, fmt.Errorf("%d is not an alphanumeric value", value)
				}
				if group == 2 {
					chars = append(chars, wantAlphanumeric[value/45])
				}
				chars = append(chars, wantAlphanumeric[value%45])
			default:
				chars = append(chars, byte(value))
			}
		}
		result.text = append(result.text, chars...)
		result.parts = append(result.parts, segment{mode: m, data: string(chars)})
	}

	// After the terminator: zero bits up to the byte boundary, then the
	// pad bytes 0xEC and 0x11 in turn.
	if rest := bits.left() % 8; rest != 0 && bits.take(rest) != 0 {
		return nil, fmt.Errorf("the bits between the terminator and the next codeword are not zero")
	}
	for i := 0; bits.left() > 0; i++ {
		want := []int{0xEC, 0x11}[i%2]
		if got := bits.take(8); got != want {
			return nil, fmt.Errorf("pad codeword %d is %#02x, want %#02x", i+1, got, want)
		}
	}
	return result, nil
}

// checkPatterns fails the test when a code does not have the fixed patterns
// where the standard puts them, or when its format and version information
// do not say what the Code says about itself.
func checkPatterns(t testing.TB, c *Code) {
	t.Helper()
	n := c.Size()
	if n != c.version*4+17 || len(c.dark) != n*n {
		t.Fatalf("version %d: size %d with %d modules", c.version, n, len(c.dark))
	}
	want := func(row, col int, dark bool, what string) {
		t.Helper()
		if c.Black(col, row) != dark {
			t.Fatalf("version %d: %s: module at row %d, column %d is dark=%v, want %v", c.version, what, row, col, !dark, dark)
		}
	}

	// Finder patterns: dark 7x7 frame, light ring, dark 3x3 centre, and a
	// light separator around the sides that face into the code.
	for _, corner := range [][2]int{{0, 0}, {0, n - 7}, {n - 7, 0}} {
		for r := -1; r <= 7; r++ {
			for col := -1; col <= 7; col++ {
				row, column := corner[0]+r, corner[1]+col
				if row < 0 || row >= n || column < 0 || column >= n {
					continue
				}
				ring := max(abs(r-3), abs(col-3)) // distance from the centre
				want(row, column, ring <= 1 || ring == 3, "finder pattern")
			}
		}
	}
	// Timing patterns between the finder patterns.
	for i := 8; i <= n-9; i++ {
		want(6, i, i%2 == 0, "timing row")
		want(i, 6, i%2 == 0, "timing column")
	}
	// Alignment patterns.
	positions := alignmentPositions(c.version)
	wantCount := 0
	if c.version > 1 {
		perSide := c.version/7 + 2
		wantCount = perSide*perSide - 3
	}
	if len(positions) != wantCount {
		t.Fatalf("version %d: %d alignment patterns, want %d", c.version, len(positions), wantCount)
	}
	for _, p := range positions {
		for r := -2; r <= 2; r++ {
			for col := -2; col <= 2; col++ {
				ring := max(abs(r), abs(col))
				want(p[0]+r, p[1]+col, ring != 1, "alignment pattern")
			}
		}
	}

	level, mask, err := readFormat(c)
	if err != nil {
		t.Fatalf("version %d: %v", c.version, err)
	}
	if level != c.level || mask != c.mask {
		t.Fatalf("version %d: format information says level %d mask %d, the code was made with level %d mask %d", c.version, level, mask, c.level, c.mask)
	}
	if c.version >= 7 {
		version, err := readVersion(c)
		if err != nil {
			t.Fatalf("version %d: %v", c.version, err)
		}
		if version != c.version {
			t.Fatalf("version information says %d, want %d", version, c.version)
		}
	}
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
