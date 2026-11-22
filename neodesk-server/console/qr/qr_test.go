package qr

import (
	"bytes"
	"errors"
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"
)

var allLevels = []Level{L, M, Q, H}

// textOfVersion returns a text that needs exactly the given version at a
// level: as many bytes as that version holds in byte mode.
func textOfVersion(rng *rand.Rand, version int, level Level) string {
	text := make([]byte, capacity(version, level, byteMode))
	for i := range text {
		text[i] = "abcdefghijklmnopqrstuvwxyz"[rng.IntN(26)]
	}
	return string(text)
}

func TestEncodeRefusesWhatCannotBeEncoded(t *testing.T) {
	if _, err := Encode("", M); !errors.Is(err, ErrEmpty) {
		t.Errorf("empty text: error %v, want ErrEmpty", err)
	}
	for _, level := range []Level{-1, 4, 99} {
		if _, err := Encode("abc", level); err == nil {
			t.Errorf("level %d: no error", level)
		}
	}
	// The largest code at level L holds 7089 digits or 2953 bytes.
	if _, err := Encode(strings.Repeat("7", 7089), L); err != nil {
		t.Errorf("7089 digits at level L: %v", err)
	}
	if _, err := Encode(strings.Repeat("7", 7090), L); !errors.Is(err, ErrTooLong) {
		t.Errorf("7090 digits at level L: error %v, want ErrTooLong", err)
	}
	if _, err := Encode(strings.Repeat("x", 2953), L); err != nil {
		t.Errorf("2953 bytes at level L: %v", err)
	}
	if _, err := Encode(strings.Repeat("x", 2954), L); !errors.Is(err, ErrTooLong) {
		t.Errorf("2954 bytes at level L: error %v, want ErrTooLong", err)
	}
	if _, err := Encode(strings.Repeat("x", 1274), H); !errors.Is(err, ErrTooLong) {
		t.Errorf("1274 bytes at level H: error %v, want ErrTooLong", err)
	}
}

func TestSizeAndBlack(t *testing.T) {
	code, err := Encode("HELLO WORLD", Q)
	if err != nil {
		t.Fatal(err)
	}
	if code.Size() != 21 {
		t.Fatalf("size %d, want 21", code.Size())
	}
	// The top left module of every code is the corner of a finder pattern;
	// the one diagonally inside it belongs to the light ring.
	if !code.Black(0, 0) || code.Black(1, 1) {
		t.Error("the finder pattern is not at the top left")
	}
	for _, p := range [][2]int{{-1, 0}, {0, -1}, {21, 0}, {0, 21}, {1000, 1000}} {
		if code.Black(p[0], p[1]) {
			t.Errorf("Black(%d, %d) outside the code is true", p[0], p[1])
		}
	}
	// Black takes the column first: the module that is always dark sits in
	// column 8 of row 13, and the module in column 13 of row 8 belongs to
	// the format information.
	if !code.Black(8, 13) {
		t.Error("Black(8, 13) is light; the always dark module is in column 8 of row 13")
	}
	mirrored := 0
	for y := 0; y < 21; y++ {
		for x := 0; x < 21; x++ {
			if code.Black(x, y) != code.dark[y*21+x] {
				t.Fatalf("Black(%d, %d) is not the module in row %d, column %d", x, y, y, x)
			}
			if code.Black(x, y) == code.Black(y, x) {
				mirrored++
			}
		}
	}
	if mirrored == 21*21 {
		t.Error("the code is the same when rows and columns are swapped; the check above proves nothing")
	}
}

// TestCapacitiesFromTheStandard checks the capacity of the first and last
// version and one in each range of length field widths against the numbers
// printed in ISO/IEC 18004:2015, table 7.
func TestCapacitiesFromTheStandard(t *testing.T) {
	cases := []struct {
		version                     int
		level                       Level
		digits, alphanumerics, data int
	}{
		{1, L, 41, 25, 17}, {1, M, 34, 20, 14}, {1, Q, 27, 16, 11}, {1, H, 17, 10, 7},
		{2, L, 77, 47, 32}, {5, M, 202, 122, 84}, {9, H, 235, 143, 98},
		{10, L, 652, 395, 271}, {10, M, 513, 311, 213}, {26, Q, 1804, 1094, 751},
		{27, H, 1501, 910, 625},
		{40, L, 7089, 4296, 2953}, {40, M, 5596, 3391, 2331}, {40, Q, 3993, 2420, 1663}, {40, H, 3057, 1852, 1273},
	}
	for _, c := range cases {
		got := [3]int{capacity(c.version, c.level, numeric), capacity(c.version, c.level, alphanumeric), capacity(c.version, c.level, byteMode)}
		if want := [3]int{c.digits, c.alphanumerics, c.data}; got != want {
			t.Errorf("version %d level %d: capacity %v, want %v", c.version, c.level, got, want)
		}
	}
}

// TestCodewordTables checks the tables against each other and against the
// layout: the codewords of a version are the modules left free by the fixed
// patterns, and the error correction codewords divide evenly into blocks.
func TestCodewordTables(t *testing.T) {
	for version := 1; version <= 40; version++ {
		free := 0
		for _, row := range fixedModules(version) {
			for _, fixed := range row {
				if !fixed {
					free++
				}
			}
		}
		if free/8 != totalCodewords[version] {
			t.Errorf("version %d: %d free modules hold %d codewords, the table says %d", version, free, free/8, totalCodewords[version])
		}
		for _, level := range allLevels {
			ec, blocks := ecCodewords(version, level), ecBlocks(version, level)
			if ec%blocks != 0 {
				t.Errorf("version %d level %d: %d error correction codewords do not divide into %d blocks", version, level, ec, blocks)
			}
			if perBlock := ec / blocks; perBlock < 7 || perBlock > 30 {
				t.Errorf("version %d level %d: %d error correction codewords per block", version, level, perBlock)
			}
		}
		// A higher level spends more on error correction.
		if !(ecCodewords(version, L) < ecCodewords(version, M) && ecCodewords(version, M) < ecCodewords(version, Q) && ecCodewords(version, Q) < ecCodewords(version, H)) {
			t.Errorf("version %d: error correction codewords do not grow from L to H", version)
		}
	}
}

func TestAlignmentTable(t *testing.T) {
	for version := 1; version <= 40; version++ {
		got, want := alignmentCentres[version-1], wantAlignmentCentres(version)
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("version %d: alignment coordinates %v, want %v", version, got, want)
		}
	}
}

// TestPatternsOfEveryVersion makes a code of every version at every level
// and checks the finder, timing and alignment patterns and the format and
// version information.
func TestPatternsOfEveryVersion(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	masks := map[int]bool{}
	for version := 1; version <= 40; version++ {
		for _, level := range allLevels {
			code, err := Encode(textOfVersion(rng, version, level), level)
			if err != nil {
				t.Fatalf("version %d level %d: %v", version, level, err)
			}
			if code.version != version {
				t.Fatalf("a text of %d bytes at level %d got version %d, want %d", capacity(version, level, byteMode), level, code.version, version)
			}
			checkPatterns(t, code)
			masks[code.mask] = true
		}
	}
	if len(masks) < 6 {
		t.Errorf("160 codes used only %d different masks", len(masks))
	}
}

func TestFormatAndVersionBits(t *testing.T) {
	// ISO/IEC 18004:2015, annex C, table C.1 lists all 32 format words.
	// These are five of them.
	formats := []struct {
		level Level
		mask  int
		want  int
	}{
		{M, 0, 0b101010000010010},
		{M, 5, 0b100000011001110},
		{L, 0, 0b111011111000100},
		{Q, 7, 0b010101111101101},
		{H, 3, 0b001100111010000},
	}
	for _, f := range formats {
		if got := formatBits(f.level, f.mask); got != f.want {
			t.Errorf("format bits for level %d mask %d: %015b, want %015b", f.level, f.mask, got, f.want)
		}
	}
	// Annex D, table D.1.
	versions := map[int]int{7: 0x07C94, 8: 0x085BC, 21: 0x15683, 40: 0x28C69}
	for version, want := range versions {
		if got := versionBits(version); got != want {
			t.Errorf("version bits for version %d: %#x, want %#x", version, got, want)
		}
	}
}

func TestSplitRuns(t *testing.T) {
	cases := map[string][]segment{
		"12345":      {{numeric, "12345"}},
		"AB12cd":     {{alphanumeric, "AB"}, {numeric, "12"}, {byteMode, "cd"}},
		"a1":         {{byteMode, "a"}, {numeric, "1"}},
		"HTTP://X.Y": {{alphanumeric, "HTTP://X.Y"}},
		"é9":         {{byteMode, "é"}, {numeric, "9"}},
		"\xff\x00 $": {{byteMode, "\xff\x00"}, {alphanumeric, " $"}},
	}
	for text, want := range cases {
		if got := splitRuns(text); fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("splitRuns(%q) = %v, want %v", text, got, want)
		}
	}
}

func TestPlanJoinsRunsWhenThatIsShorter(t *testing.T) {
	cases := []struct {
		text string
		want []segment
	}{
		// One header for all is cheaper than four.
		{"A1B2", []segment{{alphanumeric, "A1B2"}}},
		// A long run of digits pays for its own segment.
		{"abc1234567890123456789", []segment{{byteMode, "abc"}, {numeric, "1234567890123456789"}}},
		// A short one does not.
		{"abc12def", []segment{{byteMode, "abc12def"}}},
		{"0123456789", []segment{{numeric, "0123456789"}}},
		{"HELLO WORLD", []segment{{alphanumeric, "HELLO WORLD"}}},
	}
	for _, c := range cases {
		got, _, err := plan(c.text, M)
		if err != nil {
			t.Fatalf("plan(%q): %v", c.text, err)
		}
		if fmt.Sprint(got) != fmt.Sprint(c.want) {
			t.Errorf("plan(%q) = %v, want %v", c.text, got, c.want)
		}
	}
}

// TestDataCodewordsFromTheStandard encodes the example of ISO/IEC
// 18004:2015, annex I: the digits 01234567 in a version 1 code at level M.
func TestDataCodewordsFromTheStandard(t *testing.T) {
	data := dataCodewords([]segment{{numeric, "01234567"}}, 1, M)
	wantData := []byte{0x10, 0x20, 0x0C, 0x56, 0x61, 0x80, 0xEC, 0x11, 0xEC, 0x11, 0xEC, 0x11, 0xEC, 0x11, 0xEC, 0x11}
	if !bytes.Equal(data, wantData) {
		t.Fatalf("data codewords % X, want % X", data, wantData)
	}
	all := addErrorCorrection(data, 1, M)
	wantEC := []byte{0xA5, 0x24, 0xD4, 0xC1, 0xED, 0x36, 0xC7, 0x87, 0x2C, 0x55}
	if !bytes.Equal(all[:16], wantData) || !bytes.Equal(all[16:], wantEC) {
		t.Fatalf("codewords % X, want % X followed by % X", all, wantData, wantEC)
	}
}

func TestDataCodewordsOfEachMode(t *testing.T) {
	// "AC-42" is the alphanumeric example of the standard (7.4.4): the
	// pairs AC and -4 and the single 2 give 00111001110 11100111001 000010.
	// Version 1 at level H has room for 9 codewords.
	got := dataCodewords([]segment{{alphanumeric, "AC-42"}}, 1, H)
	//   0010 mode, 000000101 length 5, 00111001110 11100111001 000010, 0000 terminator
	want := []byte{0b00100000, 0b00101001, 0b11001110, 0b11100111, 0b00100001, 0b00000000, 0xEC, 0x11, 0xEC}
	if !bytes.Equal(got, want) {
		t.Errorf("AC-42: % 08b, want % 08b", got, want)
	}

	// Two segments: a byte and three digits. 0100 00000001 01100001, then
	// 0001 0000000011 0001111011 (123), terminator.
	got = dataCodewords([]segment{{byteMode, "a"}, {numeric, "123"}}, 1, H)
	want = []byte{0b01000000, 0b00010110, 0b00010001, 0b00000000, 0b11000111, 0b10110000, 0xEC, 0x11, 0xEC}
	if !bytes.Equal(got, want) {
		t.Errorf("a + 123: % 08b, want % 08b", got, want)
	}

	// Seven bytes fill version 1 at level H to the last four bits: the
	// terminator takes them and no pad codeword follows.
	got = dataCodewords([]segment{{byteMode, "abcdefg"}}, 1, H)
	want = []byte{0x40, 0x76, 0x16, 0x26, 0x36, 0x46, 0x56, 0x66, 0x70}
	if !bytes.Equal(got, want) {
		t.Errorf("abcdefg: % X, want % X", got, want)
	}

	// Seventeen digits at level H: 4 + 10 + 57 bits leave one bit, which
	// is all the terminator there is.
	got = dataCodewords([]segment{{numeric, "99999999999999999"}}, 1, H)
	if len(got) != 9 || got[8] != 0b11000110 {
		t.Errorf("17 digits: % 08b, want 9 codewords, the last one 11000110", got)
	}
}

// TestGeneratorPolynomials checks two of the generator polynomials listed
// in ISO/IEC 18004:2015, annex A. The standard prints them as powers of 2:
// for 7 codewords the exponents are 0, 87, 229, 146, 149, 238, 102, 21.
func TestGeneratorPolynomials(t *testing.T) {
	cases := map[int][]int{
		7:  {0, 87, 229, 146, 149, 238, 102, 21},
		10: {0, 251, 67, 46, 61, 118, 70, 64, 94, 32, 45},
	}
	for degree, exponents := range cases {
		got := rsGenerator(degree)
		if len(got) != degree+1 {
			t.Fatalf("degree %d: %d coefficients", degree, len(got))
		}
		for i, exponent := range exponents {
			want := byte(1)
			for k := 0; k < exponent; k++ {
				want = slowMul(want, 2)
			}
			if got[i] != want {
				t.Errorf("degree %d: coefficient %d is %d, want 2^%d = %d", degree, i, got[i], exponent, want)
			}
		}
	}
}

func TestFieldMultiplication(t *testing.T) {
	for a := 0; a < 256; a++ {
		for b := 0; b < 256; b++ {
			if got, want := gfMul(byte(a), byte(b)), slowMul(byte(a), byte(b)); got != want {
				t.Fatalf("%d times %d = %d, want %d", a, b, got, want)
			}
		}
	}
}

// TestBlocksAreInterleaved uses version 5 at level Q, the example of the
// standard for blocks of two sizes: 62 data codewords in two blocks of 15
// and two of 16, with 18 error correction codewords each.
func TestBlocksAreInterleaved(t *testing.T) {
	data := make([]byte, 62)
	for i := range data {
		data[i] = byte(i + 1)
	}
	all := addErrorCorrection(data, 5, Q)
	if len(all) != 134 {
		t.Fatalf("%d codewords, want 134", len(all))
	}
	// The first codeword of each block, then the second of each, ...
	wantStart := []byte{1, 16, 31, 47, 2, 17, 32, 48}
	if !bytes.Equal(all[:8], wantStart) {
		t.Errorf("codewords start % d, want % d", all[:8], wantStart)
	}
	// ... and at the end of the data the sixteenth codeword of the two
	// longer blocks.
	wantEnd := []byte{15, 30, 45, 61, 46, 62}
	if !bytes.Equal(all[56:62], wantEnd) {
		t.Errorf("data codewords end % d, want % d", all[56:62], wantEnd)
	}
	// Every block must check: data 1 to 15 with the error correction
	// codewords at positions 62, 66, 70, ...
	block := append([]byte{}, data[:15]...)
	for i := 0; i < 18; i++ {
		block = append(block, all[62+4*i])
	}
	if !syndromesZero(block, 18) {
		t.Error("the first block fails the Reed-Solomon check")
	}
}

// matrixOf builds a matrix from rows of '#' (dark) and '.' (light).
func matrixOf(rows ...string) *matrix {
	m := &matrix{size: len(rows), dark: make([]bool, len(rows)*len(rows)), function: make([]bool, len(rows)*len(rows))}
	for r, row := range rows {
		if len(row) != len(rows) {
			panic("matrixOf: not square")
		}
		for c := range row {
			m.dark[r*m.size+c] = row[c] == '#'
		}
	}
	return m
}

func TestPenaltyRules(t *testing.T) {
	// Rule 1. Rows: a run of 5 (3), a run of 6 (4), a run of 7 light (5),
	// then runs of at most 4. Columns: no run reaches 5.
	runs := matrixOf(
		"#####.#",
		"######.",
		".......",
		"#.#.#.#",
		".#.#.#.",
		"#.#.#.#",
		".#.#.#.",
	)
	if got := runs.penaltyRuns(); got != 3+4+5 {
		t.Errorf("rule 1: %d, want 12", got)
	}
	// The same turned on its side counts the same.
	turned := matrixOf("##.#.#.", "##..#.#", "##.#.#.", "##..#.#", "##.#.#.", ".#..#.#", "#..#.#.")
	if got := turned.penaltyRuns(); got != 3+4+5 {
		t.Errorf("rule 1, columns: %d, want 12", got)
	}

	// Rule 2. A dark 3x3 area holds four 2x2 squares, the light 2x2 one more.
	blocks := matrixOf(
		"###.#",
		"###.#",
		"###.#",
		"#.#..",
		".#...",
	)
	if got := blocks.penaltyBlocks(); got != 5*3 {
		t.Errorf("rule 2: %d, want 15", got)
	}

	// Rule 3. Row 0 holds the pattern with four light modules after it,
	// row 8 with four before it, and column 0 reads #.###.#.... downwards.
	finder := matrixOf(
		"#.###.#....",
		"...........",
		"#..........",
		"#..........",
		"#..........",
		"...........",
		"#..........",
		"...........",
		"....#.###.#",
		"...........",
		"...........",
	)
	if got := finder.penaltyFinderLike(); got != 3*40 {
		t.Errorf("rule 3: %d, want 120", got)
	}
	// Three light modules beside the pattern are not enough.
	almost := matrixOf(
		"#.###.#...#",
		"...........", "...........", "...........", "...........", "...........",
		"...........", "...........", "...........", "...........", "...........",
	)
	if got := almost.penaltyFinderLike(); got != 0 {
		t.Errorf("rule 3 with three light modules: %d, want 0", got)
	}

	// Rule 4. The share of dark modules in percent, divided by 5 and
	// rounded up, compared with 10.
	balance := []struct {
		dark, want int
	}{
		{0, 100},   // 0 %: 0, ten steps away
		{50, 0},    // 50 %: exactly 10
		{51, 10},   // 51 %: rounds up to 11
		{45, 10},   // 45 %: 9
		{46, 0},    // 46 %: rounds up to 10
		{43, 10},   // 43 %: 9
		{58, 20},   // 58 %: 12
		{100, 100}, // 100 %: 20
	}
	for _, b := range balance {
		m := &matrix{size: 10, dark: make([]bool, 100), function: make([]bool, 100)}
		for i := 0; i < b.dark; i++ {
			m.dark[i] = true
		}
		if got := m.penaltyBalance(); got != b.want {
			t.Errorf("rule 4 with %d %% dark: %d, want %d", b.dark, got, b.want)
		}
	}

	// The penalty is the sum of the four rules.
	if got, want := finder.penalty(), finder.penaltyRuns()+finder.penaltyBlocks()+finder.penaltyFinderLike()+finder.penaltyBalance(); got != want || finder.penaltyBlocks() == 0 || finder.penaltyRuns() == 0 {
		t.Errorf("penalty %d, want the sum of the rules %d", got, want)
	}
}

func TestMaskFormulas(t *testing.T) {
	for mask := 0; mask < 8; mask++ {
		for row := 0; row < 30; row++ {
			for col := 0; col < 30; col++ {
				if got, want := maskAt(mask, row, col), wantMasks[mask](row, col); got != want {
					t.Fatalf("mask %d at row %d, column %d: %v, want %v", mask, row, col, got, want)
				}
			}
		}
	}
}

// TestMaskIsTheBestOfEight builds codes by hand up to the masking step and
// checks that the mask Encode chose has the lowest penalty, and the lowest
// number among equals.
func TestMaskIsTheBestOfEight(t *testing.T) {
	rng := rand.New(rand.NewPCG(3, 4))
	differentWinners := map[int]bool{}
	for i := 0; i < 300; i++ {
		level := allLevels[i%4]
		text := randomText(rng, 1+rng.IntN(120))
		code, err := Encode(text, level)
		if err != nil {
			t.Fatal(err)
		}
		// Take the mask off the finished code to get back to the unmasked
		// one, then score every mask.
		best, lowest := -1, 0
		for mask := 0; mask < 8; mask++ {
			m := &matrix{size: code.size, dark: append([]bool{}, code.dark...), function: make([]bool, len(code.dark))}
			for row, fixedRow := range fixedModules(code.version) {
				for col, fixed := range fixedRow {
					m.function[row*m.size+col] = fixed
				}
			}
			m.applyMask(code.mask)
			m.applyMask(mask)
			m.drawFormat(level, mask)
			if penalty := m.penalty(); best < 0 || penalty < lowest {
				best, lowest = mask, penalty
			}
		}
		if code.mask != best {
			t.Fatalf("text %q level %d: mask %d, but mask %d has the lowest penalty (%d)", text, level, code.mask, best, lowest)
		}
		differentWinners[best] = true
	}
	if len(differentWinners) < 8 {
		t.Errorf("300 codes chose only %d of the 8 masks", len(differentWinners))
	}
}
