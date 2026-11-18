package generator

import (
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode/utf16"
)

// FuzzUploadName is the file name an uploaded archive is stored under: the
// time, a dash and the client's name with every UTF-16 code unit that is
// not a letter, digit, dot, dash or underscore made "_". Nothing of it can
// leave the uploads folder.
func FuzzUploadName(f *testing.F) {
	for _, s := range []string{"templates.tar.gz", "ä b/../c.tar.gz", "😀", "", "..", "a\x00b"} {
		f.Add(s)
	}
	safe := regexp.MustCompile(`^1700000000000-[A-Za-z0-9._-]+$`)
	f.Fuzz(func(t *testing.T, name string) {
		got := uploadName(time.UnixMilli(1700000000000), name)
		if !safe.MatchString(got) || strings.Contains(got, "/") {
			t.Fatalf("%q gave %q", name, got)
		}
		if name != "" && len(got)-len("1700000000000-") != len(utf16.Encode([]rune(name))) {
			t.Fatalf("%q: %d characters for %d code units", name, len(got), len(utf16.Encode([]rune(name))))
		}
	})
}
