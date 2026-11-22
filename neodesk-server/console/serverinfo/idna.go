package serverinfo

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/bidi"
	"golang.org/x/text/unicode/norm"
)

// domainToASCII turns the host name of an http or https URL into the form
// Node's URL parser gives it: ASCII letters in lower case, and a label
// with other letters written as "xn--" and its Punycode. It reports false
// where Node refuses the name.
//
// Node follows Unicode's rules for international domain names (UTS 46)
// with its own table of every character. There is no such table here: a
// character is mapped by compatibility normalising and case folding, and
// refused when it is a control, a format character, unassigned, for
// private use or one of the few the rules name one by one. That gives
// Node's answer for all but a few hundred characters: those Unicode added
// since version 13, which Node's parser takes in some places where this
// one refuses them. The rule for a zero width non-joiner between joining
// letters is kept by script, not letter by letter.
func domainToASCII(domain string) (string, bool) {
	if isASCII(domain) {
		lower := lowerASCII(domain)
		for _, label := range strings.Split(lower, ".") {
			if strings.HasPrefix(label, "xn--") && !validPunycodeLabel(label[4:]) {
				return "", false
			}
		}
		return lower, true
	}

	mapped, ok := idnaMap(domain)
	if !ok || mapped == "" {
		return "", false
	}
	mapped = norm.NFC.String(mapped)

	labels := strings.Split(mapped, ".")
	for i, label := range labels {
		switch {
		case label == "":
		case isASCII(label):
			if strings.HasPrefix(label, "xn--") && !validPunycodeLabel(label[4:]) {
				return "", false
			}
		default:
			runes := []rune(label)
			if strings.HasPrefix(label, "xn--") || !idnaLabelValid(runes) {
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

// validPunycodeLabel reports whether the part after "xn--" stands for a
// label that could have been written out: it decodes to something, and
// that is already in the form mapping and normalising give. A label that
// decodes to ASCII alone passes, as it does in Node 22.
func validPunycodeLabel(encoded string) bool {
	runes, ok := punycodeDecode(encoded)
	if !ok || len(runes) == 0 {
		return false
	}
	label := string(runes)
	mapped, ok := idnaMap(label)
	if !ok || mapped != label || norm.NFC.String(label) != label {
		return false
	}
	return idnaLabelValid(runes)
}

// idnaMap maps every character of a name: letters to their folded form,
// compatibility characters to what they stand for, and the characters that
// are ignored in names to nothing. It reports false for a character that
// is not allowed in a name.
func idnaMap(s string) (string, bool) {
	var b strings.Builder
	b.Grow(len(s))
	fold := cases.Fold()
	for _, r := range s {
		switch {
		case r < utf8.RuneSelf:
			if r >= 'A' && r <= 'Z' {
				r += 'a' - 'A'
			}
			b.WriteRune(r)
		case r == 0xDF, r == 0x3C2, r == 0x200C, r == 0x200D:
			b.WriteRune(r)
		case idnaIgnored(r):
		case idnaDisallowed(r):
			return "", false
		case r >= 0x13F8 && r <= 0x13FD:
			// The small Cherokee letters fold to the capital ones.
			b.WriteRune(r - 8)
		case r >= 0xAB70 && r <= 0xABBF:
			b.WriteRune(r - 0xAB70 + 0x13A0)
		default:
			mapped := norm.NFKC.String(fold.String(norm.NFKC.String(fold.String(string(r)))))
			for _, m := range mapped {
				switch {
				case m == 0x3002:
					b.WriteByte('.')
				case m >= utf8.RuneSelf && m != 0xDF && m != 0x3C2 && (idnaIgnored(m) || idnaDisallowed(m)):
					return "", false
				default:
					b.WriteRune(m)
				}
			}
		}
	}
	return b.String(), true
}

// idnaIgnored reports whether r is left out of a name: the soft hyphen, the
// zero width space, the word joiner and the variation selectors.
func idnaIgnored(r rune) bool {
	switch r {
	case 0xAD, 0x34F, 0x180B, 0x180C, 0x180D, 0x180F, 0x200B, 0x2060, 0x2064, 0xFEFF:
		return true
	}
	return r >= 0xFE00 && r <= 0xFE0F || r >= 0x1BCA0 && r <= 0x1BCA3 || r >= 0xE0100 && r <= 0xE01EF
}

// idnaDisallowed reports whether a character outside ASCII cannot be in a
// name: controls, format characters, line and paragraph separators,
// surrogates, private use and unassigned characters, and the characters
// the rules for domain names exclude one by one.
func idnaDisallowed(r rune) bool {
	switch r {
	case 0x04C0, 0x115F, 0x1160, 0x1680, 0x17B4, 0x17B5, 0x1806, 0x2024, 0x2025, 0x2026, 0x2132, 0x2183,
		0x3164, 0x33C2, 0x33C7, 0x33D8, 0xFE12, 0xFE19, 0xFE30, 0xFE52, 0xFFA0, 0xFFFC, 0xFFFD, 0x1F100:
		return true
	}
	if r >= 0x10A0 && r <= 0x10C5 || r >= 0x2488 && r <= 0x249B || r >= 0x2FF0 && r <= 0x2FFB {
		return true
	}
	if !utf8.ValidRune(r) || r&0xFFFE == 0xFFFE || r >= 0xFDD0 && r <= 0xFDEF {
		return true
	}
	if unicode.In(r, unicode.C, unicode.Zl, unicode.Zp) {
		return true
	}
	return !unicode.In(r, unicode.L, unicode.M, unicode.N, unicode.P, unicode.S, unicode.Zs)
}

// idnaLabelValid checks a mapped and normalised label the way Node 22
// does: it must not start with a combining mark, a zero width joiner must
// follow a virama (a non-joiner may also stand between joining letters),
// and a label with right-to-left characters must keep the rules for such
// labels. Of those rules Node applies less to a label that starts with a
// left-to-right character: its last character is not looked at.
func idnaLabelValid(label []rune) bool {
	if len(label) == 0 {
		return true
	}
	if unicode.Is(unicode.M, label[0]) {
		return false
	}
	for i, r := range label {
		if r != 0x200C && r != 0x200D {
			continue
		}
		if i > 0 && norm.NFC.PropertiesString(string(label[i-1])).CCC() == 9 {
			return true
		}
		if r == 0x200D || i == 0 || i+1 >= len(label) {
			return false
		}
		return containsJoining(label[:i]) && containsJoining(label[i+1:])
	}

	last := len(label) - 1
	for last >= 0 && bidiClass(label[last]) == bidi.NSM {
		last--
	}
	if last < 0 {
		return false
	}
	rtl := false
	for _, r := range label {
		if c := bidiClass(r); c == bidi.R || c == bidi.AL || c == bidi.AN {
			rtl = true
			break
		}
	}
	if !rtl {
		return true
	}
	if bidiClass(label[0]) == bidi.L {
		for _, r := range label[:last] {
			switch bidiClass(r) {
			case bidi.L, bidi.EN, bidi.ES, bidi.CS, bidi.ET, bidi.ON, bidi.BN, bidi.NSM:
			default:
				return false
			}
		}
		return true
	}
	hasEN, hasAN := false, false
	for _, r := range label[:last+1] {
		switch bidiClass(r) {
		case bidi.EN:
			hasEN = true
		case bidi.AN:
			hasAN = true
		case bidi.R, bidi.AL, bidi.ES, bidi.CS, bidi.ET, bidi.ON, bidi.BN, bidi.NSM:
		default:
			return false
		}
		if hasEN && hasAN {
			return false
		}
	}
	switch bidiClass(label[last]) {
	case bidi.R, bidi.AL, bidi.EN, bidi.AN:
		return true
	}
	return false
}

func bidiClass(r rune) bidi.Class {
	p, _ := bidi.LookupRune(r)
	return p.Class()
}

// containsJoining reports whether one of the runes is a letter of a script
// whose letters join.
func containsJoining(runes []rune) bool {
	for _, r := range runes {
		if unicode.IsLetter(r) && unicode.In(r, unicode.Arabic, unicode.Syriac, unicode.Nko, unicode.Mongolian,
			unicode.Mandaic, unicode.Adlam, unicode.Phags_Pa, unicode.Manichaean, unicode.Hanifi_Rohingya) {
			return true
		}
	}
	return false
}

// Punycode (RFC 3492).
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
	for delta > (punyBase-punyTMin)*punyTMax/2 {
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

func punyDigit(d int32) byte {
	if d < 26 {
		return byte('a' + d)
	}
	return byte('0' + d - 26)
}

func punycodeEncode(label []rune) (string, bool) {
	var out []byte
	for _, r := range label {
		if r < utf8.RuneSelf {
			out = append(out, byte(r))
		}
	}
	basic := int32(len(out))
	handled := basic
	if basic > 0 {
		out = append(out, '-')
	}
	n, delta, bias := int32(punyInitialN), int32(0), int32(punyInitialBias)
	for handled < int32(len(label)) {
		next := int32(unicode.MaxRune + 1)
		for _, r := range label {
			if r >= n && r < next {
				next = r
			}
		}
		if next-n > (1<<31-1-delta)/(handled+1) {
			return "", false
		}
		delta += (next - n) * (handled + 1)
		n = next
		for _, r := range label {
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
				out = append(out, punyDigit(t+(q-t)%(punyBase-t)))
				q = (q - t) / (punyBase - t)
			}
			out = append(out, punyDigit(q))
			bias = punyAdapt(delta, handled+1, handled == basic)
			delta = 0
			handled++
		}
		delta++
		n++
	}
	return string(out), true
}

func punycodeDecode(s string) ([]rune, bool) {
	var out []rune
	if i := strings.LastIndexByte(s, '-'); i >= 0 {
		for _, c := range []byte(s[:i]) {
			if c >= utf8.RuneSelf {
				return nil, false
			}
			out = append(out, rune(c))
		}
		s = s[i+1:]
	}
	n, i, bias := int32(punyInitialN), int32(0), int32(punyInitialBias)
	for s != "" {
		oldI, w := i, int32(1)
		for k := int32(punyBase); ; k += punyBase {
			if s == "" {
				return nil, false
			}
			c := s[0]
			s = s[1:]
			var digit int32
			switch {
			case c >= 'a' && c <= 'z':
				digit = int32(c - 'a')
			case c >= '0' && c <= '9':
				digit = int32(c-'0') + 26
			default:
				return nil, false
			}
			if digit > (1<<31-1-i)/w {
				return nil, false
			}
			i += digit * w
			t := punyThreshold(k, bias)
			if digit < t {
				break
			}
			if w > (1<<31-1)/(punyBase-t) {
				return nil, false
			}
			w *= punyBase - t
		}
		count := int32(len(out)) + 1
		bias = punyAdapt(i-oldI, count, oldI == 0)
		if i/count > 1<<31-1-n {
			return nil, false
		}
		n += i / count
		i %= count
		if n < utf8.RuneSelf || !utf8.ValidRune(n) {
			return nil, false
		}
		out = append(out, 0)
		copy(out[i+1:], out[i:])
		out[i] = n
		i++
	}
	return out, true
}
