package qr

import (
	"math/rand/v2"
	"testing"
)

// randomText makes a text of about n bytes out of runs of digits,
// alphanumeric characters, other ASCII, UTF-8 and arbitrary bytes, so that
// every mix of segments comes up.
func randomText(rng *rand.Rand, n int) string {
	alphabets := []string{
		"0123456789",
		"ABCDEFGHIJKLMNOPQRSTUVWXYZ $%*+-./:",
		"0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ $%*+-./:",
		"abcdefghijklmnopqrstuvwxyz_=&?#@!",
		"äöüß€✓😀日本",
	}
	// One text in four sticks to a single kind.
	only := -1
	if rng.IntN(4) == 0 {
		only = rng.IntN(len(alphabets) + 1)
	}
	var text []byte
	for len(text) < n {
		kind := only
		if kind < 0 {
			kind = rng.IntN(len(alphabets) + 1)
		}
		run := 1 + rng.IntN(1+rng.IntN(12))
		for i := 0; i < run && len(text) < n; i++ {
			if kind == len(alphabets) {
				text = append(text, byte(rng.IntN(256)))
				continue
			}
			letters := []rune(alphabets[kind])
			text = append(text, string(letters[rng.IntN(len(letters))])...)
		}
	}
	return string(text)
}

// checkReadsBack reads a code with the test reader and fails unless it
// holds exactly the text, at the level asked for, in the smallest version
// its segments fit into.
func checkReadsBack(t testing.TB, code *Code, text string, level Level) {
	t.Helper()
	read, err := readCode(code)
	if err != nil {
		t.Fatalf("text %s level %d: reading the code back: %v", shorten(text), level, err)
	}
	if string(read.text) != text {
		t.Fatalf("text %s level %d: the code holds %s", shorten(text), level, shorten(string(read.text)))
	}
	if read.level != level {
		t.Fatalf("text %s: the code is at level %d, want %d", shorten(text), read.level, level)
	}
	if read.version > 1 {
		smaller := read.version - 1
		bits := 0
		for _, part := range read.parts {
			bits += 4 + wantCountBits(part.mode, smaller) + dataBits(part.mode, len(part.data))
		}
		if bits <= dataCapacityBits(smaller, level) {
			t.Fatalf("text %s level %d: version %d, but the segments take %d bits and version %d has room for %d", shorten(text), level, read.version, bits, smaller, dataCapacityBits(smaller, level))
		}
	}
}

func TestRandomTextsReadBack(t *testing.T) {
	rng := rand.New(rand.NewPCG(2026, 11))
	modes := map[mode]int{}
	versions := map[int]bool{}
	mixed := 0
	const texts = 4000
	for i := 0; i < texts; i++ {
		level := allLevels[i%4]
		// Mostly short texts, some up to a thousand bytes.
		n := 1 + rng.IntN(60)
		switch {
		case i%40 == 0:
			n = 1 + rng.IntN(1000)
		case i%5 == 0:
			n = 1 + rng.IntN(300)
		}
		text := randomText(rng, n)
		code, err := Encode(text, level)
		if err != nil {
			t.Fatalf("text %s level %d: %v", shorten(text), level, err)
		}
		checkPatterns(t, code)
		checkReadsBack(t, code, text, level)

		read, _ := readCode(code)
		for _, part := range read.parts {
			modes[part.mode]++
		}
		if len(read.parts) > 1 {
			mixed++
		}
		versions[read.version] = true
	}
	// The texts must have exercised what the reader can check.
	if modes[numeric] < 100 || modes[alphanumeric] < 100 || modes[byteMode] < 100 {
		t.Errorf("segments read: %d numeric, %d alphanumeric, %d byte; want at least 100 of each", modes[numeric], modes[alphanumeric], modes[byteMode])
	}
	if mixed < texts/10 {
		t.Errorf("only %d of %d codes hold more than one segment", mixed, texts)
	}
	if len(versions) < 20 {
		t.Errorf("the texts reached only %d versions", len(versions))
	}
}

// TestEveryVersionReadsBack fills a code of every version at every level
// to its capacity in each mode and reads it back.
func TestEveryVersionReadsBack(t *testing.T) {
	rng := rand.New(rand.NewPCG(7, 8))
	alphabets := []string{
		numeric:      "0123456789",
		alphanumeric: "ABCDEFGHIJKLMNOPQRSTUVWXYZ $%*+-./:",
		byteMode:     "abcdefghijklmnopqrstuvwxyz",
	}
	for version := 1; version <= 40; version++ {
		for _, level := range allLevels {
			for _, m := range []mode{numeric, alphanumeric, byteMode} {
				alphabet := alphabets[m]
				text := make([]byte, capacity(version, level, m))
				for i := range text {
					text[i] = alphabet[rng.IntN(len(alphabet))]
				}
				code, err := Encode(string(text), level)
				if err != nil {
					t.Fatalf("version %d level %d mode %d: %v", version, level, m, err)
				}
				if code.version != version {
					t.Fatalf("%d characters in mode %d at level %d got version %d, want %d", len(text), m, level, code.version, version)
				}
				checkReadsBack(t, code, string(text), level)
				read, _ := readCode(code)
				if len(read.parts) != 1 || read.parts[0].mode != m {
					t.Fatalf("version %d level %d: text of one class was written as %d segments, the first in mode %d, want one in mode %d", version, level, len(read.parts), read.parts[0].mode, m)
				}
			}
		}
	}
}
