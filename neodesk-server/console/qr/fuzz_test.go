package qr

import (
	"strings"
	"testing"
)

// FuzzEncode feeds Encode arbitrary bytes and levels. It must never panic.
// It may refuse the input only for a reason, and a code it makes must have
// the fixed patterns in place and read back as the input.
func FuzzEncode(f *testing.F) {
	seeds := []string{
		"",
		"7",
		"0123456789",
		"HELLO WORLD",
		"A1B2c3",
		"otpauth://totp/BetterDesk:alice?secret=JBSWY3DPEHPK3PXP&issuer=BetterDesk",
		"rustdesk://config/9JSPrxWYpJXZ0NXYt9lI6ISeltmIsISM0ETMyoTZk5Cc",
		"OeVuKk5nlHiXp+APNn0Y3pC1Iwpwn44JGqrQCsWqmBw=",
		"äöü€😀",
		"\xff\xfe\x00\x80",
		strings.Repeat("1a", 700),
		strings.Repeat("A", 4296),
		strings.Repeat("9", 7089),
	}
	for i, seed := range seeds {
		f.Add([]byte(seed), int8(i%4))
	}
	f.Add([]byte("abc"), int8(4))
	f.Add([]byte("abc"), int8(-1))

	f.Fuzz(func(t *testing.T, data []byte, levelByte int8) {
		text := string(data)
		level := Level(levelByte)
		code, err := Encode(text, level)
		if err != nil {
			// The search for segments can miss the cheapest cut by one bit
			// for every run it joins, so a text is certain to fit only
			// when nine bits per byte and one header do: bytes take eight.
			certainToFit := level.valid() && len(data) > 0 && 9*len(data)+20 <= dataCapacityBits(40, level)
			if certainToFit {
				t.Fatalf("%d bytes at level %d refused: %v", len(data), level, err)
			}
			return
		}
		if !level.valid() || len(data) == 0 {
			t.Fatalf("level %d, %d bytes: a code was made", level, len(data))
		}
		checkPatterns(t, code)
		checkReadsBack(t, code, text, level)
	})
}
