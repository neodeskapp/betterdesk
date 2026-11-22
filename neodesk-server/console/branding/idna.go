package branding

import (
	"sort"
	"strings"

	"golang.org/x/text/unicode/norm"
)

// The kinds of entry in idnaRanges.
const (
	idnaValid   = 0 // the character stays
	idnaRefused = 1 // the host is not allowed
	idnaShift   = 2 // the character becomes the one a fixed distance away
	idnaText    = 3 // the character becomes a text, maybe an empty one
)

const (
	zeroWidthNonJoiner = 0x200C
	zeroWidthJoiner    = 0x200D
)

// domainToASCII turns the host of a URL into the form that goes on the
// wire, the way Node's URL parser does: every character is mapped (lower
// case, compatibility forms; some are dropped, some refuse the host), the
// result is put in normal form C, and each label that still has
// characters outside ASCII is checked and written as Punycode ("xn--...").
// A label that already is Punycode must decode to something that would
// have been written that way. ok is false for a host that is refused.
func domainToASCII(domain string) (string, bool) {
	ascii := true
	for i := 0; i < len(domain); i++ {
		if domain[i] >= 0x80 {
			ascii = false
			break
		}
	}
	if ascii {
		domain = strings.ToLower(domain)
	} else {
		mapped, ok := idnaMap([]rune(domain), true)
		if !ok {
			return "", false
		}
		domain = norm.NFC.String(string(mapped))
	}
	if !strings.Contains(domain, "xn--") && ascii {
		return domain, true
	}
	labels := strings.Split(domain, ".")
	for i, label := range labels {
		switch {
		case label == "":
		case strings.HasPrefix(label, "xn--"):
			if !validPunycodeLabel(label) {
				return "", false
			}
		case isASCII(label):
		default:
			runes := []rune(label)
			if !idnaLabelValid(runes) {
				return "", false
			}
			encoded, ok := punycodeEncode(runes)
			if !ok {
				return "", false
			}
			labels[i] = "xn--" + encoded
		}
	}
	return strings.Join(labels, "."), true
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

// validPunycodeLabel reports whether an "xn--" label is one the mapping
// could have produced: ASCII, decodable to at least one character, already
// mapped and in normal form, and valid as a label. A label that decodes to
// plain ASCII passes.
func validPunycodeLabel(label string) bool {
	if !isASCII(label) {
		return false
	}
	decoded, ok := punycodeDecode(label[len("xn--"):])
	if !ok || len(decoded) == 0 {
		return false
	}
	mapped, ok := idnaMap(decoded, false)
	if !ok || string(mapped) != string(decoded) {
		return false
	}
	if text := string(decoded); norm.NFC.String(text) != text {
		return false
	}
	return idnaLabelValid(decoded)
}

// idnaLookup returns the kind of a character above ASCII and the value
// that goes with it.
func idnaLookup(r rune) (kind int, value int32) {
	i := sort.Search(len(idnaRanges), func(i int) bool { return idnaRanges[i][0] > int32(r) }) - 1
	if i < 0 {
		return idnaRefused, 0
	}
	entry := idnaRanges[i][1]
	return int(entry & 3), entry >> 2
}

// idnaMap maps the characters of a host. lowerASCII says whether ASCII
// capitals are folded, which they are in a host as typed and are not in a
// decoded Punycode label (a capital there means the label was not made by
// this mapping).
func idnaMap(in []rune, lowerASCII bool) ([]rune, bool) {
	out := make([]rune, 0, len(in))
	for _, r := range in {
		switch {
		case r < 0x80:
			if r >= 'A' && r <= 'Z' {
				if !lowerASCII {
					return nil, false
				}
				r += 'a' - 'A'
			}
			out = append(out, r)
		case r == zeroWidthNonJoiner || r == zeroWidthJoiner:
			// allowed in some places, see idnaLabelValid
			out = append(out, r)
		case r > 0x10FFFF || r >= 0xD800 && r <= 0xDFFF:
			return nil, false
		default:
			switch kind, value := idnaLookup(r); kind {
			case idnaValid:
				out = append(out, r)
			case idnaRefused:
				return nil, false
			case idnaShift:
				out = append(out, r+value)
			case idnaText:
				out = append(out, []rune(idnaTexts[value])...)
			}
		}
	}
	return out, true
}

func inRanges(ranges [][2]int32, r rune) bool {
	i := sort.Search(len(ranges), func(i int) bool { return ranges[i][1] >= int32(r) })
	return i < len(ranges) && ranges[i][0] <= int32(r)
}

// idnaLabelValid checks one mapped label the way Node's URL parser does.
//
// It must not start with a combining mark. A zero width joiner must
// follow a virama; a zero width non-joiner must follow one too, or stand
// after a character that joins to the left and before one that joins to
// the right. A label with either joiner is not checked further.
//
// A label with right-to-left letters or Arabic digits has to follow a
// rule that depends on its first character. When that is a left-to-right
// letter, everything up to the last character (marks at the end aside)
// must be left-to-right letters, European digits or neutral, and the last
// character may be anything. Otherwise the label must hold only
// right-to-left letters, digits and neutral characters, must end in a
// right-to-left letter or a digit, and must not mix European and Arabic
// digits. This is less than the standard asks for; it is what Node does.
func idnaLabelValid(label []rune) bool {
	if len(label) == 0 {
		return true
	}
	if inRanges(idnaLeadingMarks[:], label[0]) {
		return false
	}
	for i, r := range label {
		switch r {
		case zeroWidthNonJoiner:
			if i > 0 && inRanges(idnaViramas[:], label[i-1]) {
				return true
			}
			if i == 0 || i+1 >= len(label) {
				return false
			}
			before, after := false, false
			for _, c := range label[:i] {
				before = before || inRanges(idnaJoinsLeft[:], c)
			}
			for _, c := range label[i+1:] {
				after = after || inRanges(idnaJoinsRight[:], c)
			}
			return before && after
		case zeroWidthJoiner:
			return i > 0 && inRanges(idnaViramas[:], label[i-1])
		}
	}

	classes := make([]int32, len(label))
	rightToLeft, european, arabic := false, false, false
	last := -1
	for i, r := range label {
		classes[i] = idnaDirection(r)
		switch classes[i] {
		case idnaDirRTL:
			rightToLeft = true
		case idnaDirArabicDigit:
			rightToLeft, arabic = true, true
		case idnaDirDigit:
			european = true
		}
		if classes[i] != idnaDirMark {
			last = i
		}
	}
	if !rightToLeft {
		return true
	}
	if last < 0 {
		return false
	}
	if classes[0] == idnaDirLTR {
		for _, c := range classes[:last] {
			if c == idnaDirRTL || c == idnaDirArabicDigit || c == idnaDirOther {
				return false
			}
		}
		return true
	}
	for _, c := range classes {
		if c == idnaDirLTR || c == idnaDirOther {
			return false
		}
	}
	if european && arabic {
		return false
	}
	return classes[last] == idnaDirRTL || classes[last] == idnaDirArabicDigit || classes[last] == idnaDirDigit
}

// The directions of characters, as far as the rule in idnaLabelValid
// tells them apart.
const (
	idnaDirNeutral     = 0 // punctuation and symbols
	idnaDirRTL         = 1 // a right-to-left letter
	idnaDirArabicDigit = 2
	idnaDirLTR         = 3 // a left-to-right letter
	idnaDirDigit       = 4 // a European digit
	idnaDirMark        = 5 // a combining mark
	idnaDirOther       = 6 // not allowed in a label that has right-to-left characters
)

// idnaDirection returns the direction of a character that is valid in a
// label.
func idnaDirection(r rune) int32 {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z':
		return idnaDirLTR
	case r >= '0' && r <= '9':
		return idnaDirDigit
	case r < 0x80:
		return idnaDirNeutral
	}
	i := sort.Search(len(idnaDirections), func(i int) bool { return idnaDirections[i][1] >= int32(r) })
	if i < len(idnaDirections) && idnaDirections[i][0] <= int32(r) {
		return idnaDirections[i][2]
	}
	return idnaDirLTR
}

// Punycode (RFC 3492) with the parameters of internationalised domain
// names.
const (
	punyBase        = 36
	punyTMin        = 1
	punyTMax        = 26
	punySkew        = 38
	punyDamp        = 700
	punyInitialBias = 72
	punyInitialN    = 128
)

func punyAdapt(delta, points int32, first bool) int32 {
	if first {
		delta /= punyDamp
	} else {
		delta /= 2
	}
	delta += delta / points
	k := int32(0)
	for delta > ((punyBase-punyTMin)*punyTMax)/2 {
		delta /= punyBase - punyTMin
		k += punyBase
	}
	return k + (punyBase-punyTMin+1)*delta/(delta+punySkew)
}

func punyThreshold(k, bias int32) int32 {
	switch {
	case k <= bias:
		return punyTMin
	case k >= bias+punyTMax:
		return punyTMax
	}
	return k - bias
}

// punycodeDecode decodes the part of a label after "xn--".
func punycodeDecode(input string) ([]rune, bool) {
	const maxInt = int32(0x7fffffff)
	var out []rune
	if end := strings.LastIndexByte(input, '-'); end >= 0 {
		out = []rune(input[:end])
		input = input[end+1:]
	}
	n, i, bias := int32(punyInitialN), int32(0), int32(punyInitialBias)
	for input != "" {
		oldI, w := i, int32(1)
		for k := int32(punyBase); ; k += punyBase {
			if input == "" {
				return nil, false
			}
			c := input[0]
			input = input[1:]
			var digit int32
			switch {
			case c >= 'a' && c <= 'z':
				digit = int32(c - 'a')
			case c >= '0' && c <= '9':
				digit = int32(c-'0') + 26
			default:
				return nil, false
			}
			if digit > (maxInt-i)/w {
				return nil, false
			}
			i += digit * w
			t := punyThreshold(k, bias)
			if digit < t {
				break
			}
			if w > maxInt/(punyBase-t) {
				return nil, false
			}
			w *= punyBase - t
		}
		count := int32(len(out)) + 1
		bias = punyAdapt(i-oldI, count, oldI == 0)
		if i/count > maxInt-n {
			return nil, false
		}
		n += i / count
		i %= count
		if n < 0x80 {
			return nil, false
		}
		out = append(out, 0)
		copy(out[i+1:], out[i:])
		out[i] = rune(n)
		i++
	}
	return out, true
}

// punycodeEncode encodes a label for use after "xn--".
func punycodeEncode(input []rune) (string, bool) {
	const maxInt = int32(0x7fffffff)
	const digits = "abcdefghijklmnopqrstuvwxyz0123456789"
	var out strings.Builder
	basic := int32(0)
	for _, r := range input {
		if r < 0x80 {
			out.WriteByte(byte(r))
			basic++
		}
	}
	handled := basic
	if basic > 0 {
		out.WriteByte('-')
	}
	n, delta, bias := int32(punyInitialN), int32(0), int32(punyInitialBias)
	for handled < int32(len(input)) {
		next := maxInt
		for _, r := range input {
			if r >= n && r < next {
				next = r
			}
		}
		if next-n > (maxInt-delta)/(handled+1) {
			return "", false
		}
		delta += (next - n) * (handled + 1)
		n = next
		for _, r := range input {
			if r < n {
				delta++
				if delta < 0 {
					return "", false
				}
			}
			if r != n {
				continue
			}
			q := delta
			for k := int32(punyBase); ; k += punyBase {
				t := punyThreshold(k, bias)
				if q < t {
					break
				}
				out.WriteByte(digits[t+(q-t)%(punyBase-t)])
				q = (q - t) / (punyBase - t)
			}
			out.WriteByte(digits[q])
			bias = punyAdapt(delta, handled+1, handled == basic)
			delta = 0
			handled++
		}
		delta++
		n++
	}
	return out.String(), true
}
