package httpx

import (
	"math"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// jsWhiteChars is what JavaScript's \s matches, for use inside a character
// class.
const jsWhiteChars = `\t\n\v\f\r \x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}`

func isJSWhite(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0xA0, 0x1680, 0x2028, 0x2029, 0x202F, 0x205F, 0x3000, 0xFEFF:
		return true
	}
	return r >= 0x2000 && r <= 0x200A
}

// mediaRange is one entry of an Accept header.
type mediaRange struct {
	kind, subtype string
	params        map[string]string
	q             float64
	order         int
}

var mediaRangePattern = regexp.MustCompile(`^[` + jsWhiteChars + `]*([^` + jsWhiteChars + `/;]+)/([^;` + jsWhiteChars + `]+)[` + jsWhiteChars + `]*(?:;(.*))?$`)

// Accepts picks the media type the client likes best among the offered
// ones, as req.accepts did in the old console: the first offer without an
// Accept header, "" when the client takes none.
func Accepts(r *http.Request, offers ...string) string {
	header := Latin1(strings.Join(r.Header.Values("Accept"), ", "))
	if header == "" {
		return offers[0]
	}
	ranges := parseAccept(header)
	type priority struct {
		q       float64
		s, o, i int
	}
	chosen := make([]priority, 0, len(offers))
	for i, offer := range offers {
		kind, subtype, _ := strings.Cut(offer, "/")
		best := priority{o: -1, i: i}
		for _, candidate := range ranges {
			s, ok := specificity(kind, subtype, candidate)
			if !ok {
				continue
			}
			if firstTruthy(float64(best.s-s), best.q-candidate.q, float64(best.o-candidate.order)) < 0 {
				best = priority{q: candidate.q, s: s, o: candidate.order, i: i}
			}
		}
		if best.q > 0 {
			chosen = append(chosen, best)
		}
	}
	if len(chosen) == 0 {
		return ""
	}
	sort.SliceStable(chosen, func(a, b int) bool {
		x, y := chosen[a], chosen[b]
		return firstTruthy(y.q-x.q, float64(y.s-x.s), float64(x.o-y.o), float64(x.i-y.i)) < 0
	})
	return offers[chosen[0].i]
}

// firstTruthy is JavaScript's a || b || c for numbers: the first that is
// neither zero nor NaN, else the last.
func firstTruthy(values ...float64) float64 {
	for _, v := range values {
		if v != 0 && !math.IsNaN(v) {
			return v
		}
	}
	return values[len(values)-1]
}

// specificity says how exactly a media range names a type: 4 for the type,
// 2 for the subtype, 1 for parameters. An offer has no parameters, so a
// range with parameters fits only when each is "*" or empty.
func specificity(kind, subtype string, candidate mediaRange) (int, bool) {
	s := 0
	switch {
	case strings.EqualFold(candidate.kind, kind):
		s |= 4
	case candidate.kind != "*":
		return 0, false
	}
	switch {
	case strings.EqualFold(candidate.subtype, subtype):
		s |= 2
	case candidate.subtype != "*":
		return 0, false
	}
	if len(candidate.params) > 0 {
		for _, value := range candidate.params {
			if value != "*" && value != "" {
				return 0, false
			}
		}
		s |= 1
	}
	return s, true
}

func parseAccept(header string) []mediaRange {
	var ranges []mediaRange
	for i, part := range splitQuoted(header, ",") {
		m := mediaRangePattern.FindStringSubmatch(strings.TrimFunc(part, isJSWhite))
		if m == nil {
			continue
		}
		entry := mediaRange{kind: m[1], subtype: m[2], q: 1, order: i}
		if m[3] != "" {
			for _, param := range splitQuoted(m[3], ";") {
				key, value, _ := strings.Cut(strings.TrimFunc(param, isJSWhite), "=")
				if n := len(value); n > 0 && value[0] == '"' && value[n-1] == '"' {
					if n == 1 {
						value = ""
					} else {
						value = value[1 : n-1]
					}
				}
				if strings.ToLower(key) == "q" {
					entry.q = parseFloat(value)
					break
				}
				if entry.params == nil {
					entry.params = map[string]string{}
				}
				entry.params[strings.ToLower(key)] = value
			}
		}
		ranges = append(ranges, entry)
	}
	return ranges
}

// splitQuoted splits at a separator, but not inside double quotes.
func splitQuoted(s, sep string) []string {
	parts := strings.Split(s, sep)
	out := parts[:1]
	for _, part := range parts[1:] {
		last := len(out) - 1
		if strings.Count(out[last], `"`)%2 == 0 {
			out = append(out, part)
		} else {
			out[last] += sep + part
		}
	}
	return out
}

var floatPrefix = regexp.MustCompile(`^[+-]?(?:Infinity|(?:[0-9]+\.?[0-9]*|\.[0-9]+)(?:[eE][+-]?[0-9]+)?)`)

// parseFloat reads the number a string starts with, as JavaScript's
// parseFloat does; NaN when it starts with none.
func parseFloat(s string) float64 {
	m := floatPrefix.FindString(strings.TrimLeftFunc(s, isJSWhite))
	if m == "" {
		return math.NaN()
	}
	if strings.HasSuffix(m, "Infinity") {
		if m[0] == '-' {
			return math.Inf(-1)
		}
		return math.Inf(1)
	}
	v, err := strconv.ParseFloat(m, 64)
	if err != nil && !math.IsInf(v, 0) {
		return math.NaN()
	}
	return v
}
