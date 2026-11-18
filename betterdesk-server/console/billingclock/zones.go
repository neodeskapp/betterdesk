package billingclock

import (
	_ "embed"
	"regexp"
	"strings"
)

// zoneTable is every time zone name Intl.DateTimeFormat of the Node
// console took, with the name Intl reported for it (zones.txt, written
// from Node by testdata/nodeoracle/zones.js).
//
//go:embed zones.txt
var zoneTable string

// zoneNames maps a name in lower case to the name Intl reports for it,
// and zoneSpelling to the name as ICU spells it. Both are filled once,
// from zoneTable, and only read after that.
var zoneNames, zoneSpelling = parseZoneTable(zoneTable)

func parseZoneTable(text string) (names, spelling map[string]string) {
	names, spelling = map[string]string{}, map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, resolved, _ := strings.Cut(line, " ")
		names[asciiLower(name)] = resolved
		spelling[asciiLower(name)] = name
	}
	return names, spelling
}

// zoneOffset is an offset Intl takes as a time zone: a sign (+, - or the
// minus sign U+2212), hours from 00 to 23, and minutes from 00 to 59 with
// or without a colon.
var zoneOffset = regexp.MustCompile(`^([+\x{2212}-])([01][0-9]|2[0-3])(?::?([0-5][0-9]))?$`)

// resolveZone is what Intl.DateTimeFormat makes of a time zone: the name
// it reports (resolvedOptions().timeZone), or ok false where it throws.
// Names are matched in any letter case; an offset is reported as ±HH:MM,
// with + where there is no offset at all.
func resolveZone(name string) (resolved string, ok bool) {
	if m := zoneOffset.FindStringSubmatch(name); m != nil {
		minutes := m[3]
		if minutes == "" {
			minutes = "00"
		}
		sign := "-"
		if m[1] == "+" || (m[2] == "00" && minutes == "00") {
			sign = "+"
		}
		return sign + m[2] + ":" + minutes, true
	}
	resolved, ok = zoneNames[asciiLower(name)]
	return resolved, ok
}

// asciiLower lowers the letters A to Z only, so that no other character
// can turn into a letter of a zone name.
func asciiLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 'a' - 'A'
		}
	}
	return string(b)
}
