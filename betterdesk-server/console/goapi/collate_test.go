package goapi

import (
	"sort"
	"strings"
	"testing"
)

func sign(n int) int {
	switch {
	case n < 0:
		return -1
	case n > 0:
		return 1
	}
	return 0
}

func TestLocaleCompare(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"", "a", -1},
		{"a", "b", -1},
		{"b", "a", 1},
		{"a", "B", -1},
		{"B", "a", 1},
		{"a", "A", -1},
		{"A", "a", 1},
		{"ab", "Ab", -1},
		{"Ab", "aC", -1},
		{"a", "ab", -1},
		{"host-10", "host-9", -1},
		{"9", "a", -1},
		{"_", "0", -1},
		{" ", "_", -1},
		{"-", ".", -1},
		{"a b", "a_b", -1},
		{"a_b", "ab", -1},
		{"2026-11-10T01:05:50Z", "2026-11-10T01:05:51Z", -1},
		{"2026-11-10T01:05:50.5Z", "2026-11-10T01:05:50Z", -1},
		{"a\x01b", "ab", 0},
		{"z", "ä", 1},
		{"a", "ä", -1},
		{"ä", "b", -1},
		{"Ä", "ä", 1},
		{"é", "è", -1},
		{"z", "я", -1},
		// Letters with accents sort with their base letter.
		{"lodz", "łódź", -1},
		{"łódź", "lodza", -1},
		{"zolw", "żółw", -1},
		{"żółw", "zolx", -1},
		{"ź", "ž", -1},
		{"ž", "ż", -1},
		{"I", "İ", -1},
		{"š", "ſ", -1},
		{"ſ", "t", -1},
		// Two letters in one.
		{"strasse", "straße", -1},
		{"straße", "strasse1", -1},
		{"aeon", "æon", -1},
		{"æon", "aeoo", -1},
		// Letters of their own.
		{"iz", "ı", -1},
		{"ı", "j", -1},
		{"zz", "þ", -1},
	}
	for _, c := range cases {
		if got := sign(localeCompare(c.a, c.b)); got != c.want {
			t.Errorf("localeCompare(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
		if got := sign(localeCompare(c.b, c.a)); got != -c.want {
			t.Errorf("localeCompare(%q, %q) = %d, want %d", c.b, c.a, got, -c.want)
		}
	}

	names := []string{"beta", "Alpha", "alpha", "10", "9", "_x", "Zulu", "", "ärger", "zebra", "Beta"}
	sort.SliceStable(names, func(i, j int) bool { return localeCompare(names[i], names[j]) < 0 })
	if got, want := strings.Join(names, " "), " _x 10 9 alpha Alpha ärger beta Beta zebra Zulu"; got != want {
		t.Errorf("sorted = %q, want %q", got, want)
	}
}
