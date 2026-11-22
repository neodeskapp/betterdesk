package message

import (
	"slices"
	"strings"
	"unicode/utf8"
)

// This file turns a domain into its ASCII form and back the way the URL
// library inside Node 22 does (ada 2.9.2, which url.domainToASCII and
// url.domainToUnicode call). It follows that library step by step, also
// where the library leaves the standard (UTS 46), because the result decides
// which domain a mail goes to. The places are marked.
//
// Code points are uint32 here: a label that is decoded from its "xn--" form
// can hold values that are no Unicode code points.

// idnaMap applies the IDNA mapping to every code point: some are dropped,
// some replaced, most kept. It returns nil when a code point is not allowed.
func idnaMap(t *unicodeTables, input []uint32) []uint32 {
	output := make([]uint32, 0, len(input))
	for _, c := range input {
		descriptor := t.mapRanges[mapRangeIndex(t, c)*2+1]
		switch descriptor & 0xff {
		case 0:
		case 1:
			output = append(output, c)
		case 2:
			return nil
		default:
			start := int(descriptor >> 8 & 0xffff)
			output = append(output, t.mapData[start:start+int(descriptor>>24)]...)
		}
	}
	return output
}

// mapRangeIndex returns the index of the last mapping range that starts at
// or before the code point.
func mapRangeIndex(t *unicodeTables, c uint32) int {
	low, high := 0, len(t.mapRanges)/2-1
	for low <= high {
		middle := (low + high) >> 1
		start := t.mapRanges[middle*2]
		switch {
		case start < c:
			low = middle + 1
		case start > c:
			high = middle - 1
		default:
			return middle
		}
	}
	if low == 0 {
		return 0
	}
	return low - 1
}

const (
	hangulSBase  = 0xAC00
	hangulLBase  = 0x1100
	hangulVBase  = 0x1161
	hangulTBase  = 0x11A7
	hangulLCount = 19
	hangulVCount = 21
	hangulTCount = 28
	hangulNCount = hangulVCount * hangulTCount
	hangulSCount = hangulLCount * hangulNCount
)

// idnaNormalize brings the text into Unicode normalization form C: it takes
// composed characters apart, sorts the combining marks and puts the
// characters together again.
func idnaNormalize(t *unicodeTables, input []uint32) []uint32 {
	output := idnaDecompose(t, input)
	idnaSortMarks(t, output)
	return idnaCompose(t, output)
}

// idnaDecompose replaces every character that has a canonical decomposition
// by it. The table holds the full decomposition, so one step is enough.
func idnaDecompose(t *unicodeTables, input []uint32) []uint32 {
	output := make([]uint32, 0, len(input))
	for _, c := range input {
		switch {
		case c >= hangulSBase && c < hangulSBase+hangulSCount:
			index := c - hangulSBase
			output = append(output, hangulLBase+index/hangulNCount, hangulVBase+index%hangulNCount/hangulTCount)
			if index%hangulTCount != 0 {
				output = append(output, hangulTBase+index%hangulTCount)
			}
		case c < 0x110000:
			at := int(t.decompIndex[c>>8])*257 + int(c%256)
			from, to := t.decompBlock[at], t.decompBlock[at+1]
			// the lowest bit marks a decomposition that only the
			// compatibility forms use
			if to>>2 > from>>2 && from&1 == 0 {
				output = append(output, t.decompData[from>>2:to>>2]...)
			} else {
				output = append(output, c)
			}
		default:
			output = append(output, c)
		}
	}
	return output
}

// combiningClass returns the canonical combining class of a code point.
func combiningClass(t *unicodeTables, c uint32) uint8 {
	if c >= 0x110000 {
		return 0
	}
	return t.cccBlock[int(t.cccIndex[c>>8])*256+int(c%256)]
}

// idnaSortMarks orders every run of combining marks by combining class,
// keeping marks of the same class in their order.
func idnaSortMarks(t *unicodeTables, text []uint32) {
	for i := 1; i < len(text); i++ {
		class := combiningClass(t, text[i])
		if class == 0 {
			continue
		}
		current := text[i]
		back := i
		for back != 0 && combiningClass(t, text[back-1]) > class {
			text[back] = text[back-1]
			back--
		}
		text[back] = current
	}
}

// idnaCompose joins a character with the marks after it where a composed
// character exists, and Hangul parts into syllables. It works in place. The
// text comes from idnaDecompose, so it holds no whole Hangul syllables.
func idnaCompose(t *unicodeTables, text []uint32) []uint32 {
	pair := func(c uint32) (uint16, uint16) {
		at := int(t.compIndex[c>>8])*257 + int(c%256)
		return t.compBlock[at], t.compBlock[at+1]
	}
	isTrailing := func(c uint32) bool { return c > hangulTBase && c < hangulTBase+hangulTCount }

	read, write := 0, 0
	for ; read < len(text); read, write = read+1, write+1 {
		c := text[read]
		text[write] = c
		switch {
		case c >= hangulLBase && c < hangulLBase+hangulLCount:
			if read+1 < len(text) && text[read+1] >= hangulVBase && text[read+1] < hangulVBase+hangulVCount {
				text[write] = hangulSBase + ((c-hangulLBase)*hangulVCount+text[read+1]-hangulVBase)*hangulTCount
				read++
				if read+1 < len(text) && isTrailing(text[read+1]) {
					read++
					text[write] += text[read] - hangulTBase
				}
			}
		case c < 0x110000:
			from, to := pair(c)
			starter := write
			previousClass := -1
			for ; read+1 < len(text); read++ {
				next := text[read+1]
				class := int(combiningClass(t, next))
				if to != from && previousClass < class {
					// the candidates are pairs (second character, composed
					// character), sorted by the second character
					left, right := int(from), int(to)
					for left+2 < right {
						middle := left + (right-left)>>1&^1
						if t.compData[middle] <= next {
							left = middle
						}
						if t.compData[middle] >= next {
							right = middle
						}
					}
					if t.compData[left] == next {
						text[starter] = t.compData[left+1]
						from, to = pair(t.compData[left+1])
						continue
					}
				}
				if class == 0 {
					break
				}
				previousClass = class
				write++
				text[write] = next
			}
		}
	}
	return text[:write]
}

// Text directions of the bidi table that the label rules name.
const (
	bidiBN  = 1
	bidiCS  = 2
	bidiES  = 3
	bidiON  = 4
	bidiEN  = 5
	bidiL   = 6
	bidiR   = 7
	bidiNSM = 8
	bidiAL  = 9
	bidiAN  = 10
	bidiET  = 11
)

const (
	zeroWidthNonJoiner = 0x200c
	zeroWidthJoiner    = 0x200d
)

// idnaLabelValid checks a mapped and normalized label: what it starts with,
// where zero width joiners stand, and the mix of text directions.
//
// Not as in the standard: the check ends at the first zero width joiner or
// non-joiner, whatever comes after it; the direction rules apply per label
// and not to the whole domain; a label with right-to-left characters may
// start with a digit; and in a left-to-right label the last character is not
// looked at.
func idnaLabelValid(t *unicodeTables, label []uint32) bool {
	if len(label) == 0 {
		return true
	}
	if inList(t.combining, label[0]) {
		return false
	}

	for i, c := range label {
		if c != zeroWidthNonJoiner && c != zeroWidthJoiner {
			continue
		}
		if i > 0 && inList(t.virama, label[i-1]) {
			return true
		}
		if c == zeroWidthJoiner || i == 0 || i+1 >= len(label) {
			return false
		}
		before := slices.ContainsFunc(label[:i], func(c uint32) bool { return inList(t.joinL, c) || inList(t.joinD, c) })
		after := slices.ContainsFunc(label[i+1:], func(c uint32) bool { return inList(t.joinR, c) || inList(t.joinD, c) })
		return before && after
	}

	direction := func(c uint32) uint32 { return rangeValue(t.bidi, c) }
	last := len(label) - 1
	for last >= 0 && direction(label[last]) == bidiNSM {
		last--
	}
	if last < 0 {
		return false
	}
	rightToLeft := slices.ContainsFunc(label, func(c uint32) bool {
		d := direction(c)
		return d == bidiR || d == bidiAL || d == bidiAN
	})
	if !rightToLeft {
		return true
	}

	if direction(label[0]) == bidiL {
		for _, c := range label[:last] {
			switch direction(c) {
			case bidiL, bidiEN, bidiES, bidiCS, bidiET, bidiON, bidiBN, bidiNSM:
			default:
				return false
			}
		}
		return true
	}

	hasEN, hasAN := false, false
	for i, c := range label[:last+1] {
		d := direction(c)
		if d == bidiEN {
			hasEN = true
		}
		if d == bidiAN {
			hasAN = true
		}
		if (d == bidiEN || d == bidiAN) && hasEN && hasAN {
			return false
		}
		switch d {
		case bidiR, bidiAL, bidiAN, bidiEN:
		case bidiES, bidiCS, bidiET, bidiON, bidiBN, bidiNSM:
			if i == last {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// Punycode parameters (RFC 3492).
const (
	punyBase        = 36
	punyTMin        = 1
	punyTMax        = 26
	punySkew        = 38
	punyDamp        = 700
	punyInitialBias = 72
	punyInitialN    = 128
	maxInt32        = 0x7fffffff
)

func punyAdapt(delta, count int32, first bool) int32 {
	if first {
		delta /= punyDamp
	} else {
		delta /= 2
	}
	delta += delta / count
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

// idnaPunycodeDecode decodes the part of a label after "xn--". Only lower
// case letters and digits count as digits. It reports false for input that
// is no Punycode.
func idnaPunycodeDecode(input string) ([]uint32, bool) {
	var output []uint32
	n := uint32(punyInitialN)
	i := int32(0)
	bias := int32(punyInitialBias)
	if end := strings.LastIndexByte(input, '-'); end >= 0 {
		for j := 0; j < end; j++ {
			if input[j] >= 0x80 {
				return nil, false
			}
			output = append(output, uint32(input[j]))
		}
		input = input[end+1:]
	}
	for len(input) > 0 {
		oldI := i
		w := int32(1)
		for k := int32(punyBase); ; k += punyBase {
			if len(input) == 0 {
				return nil, false
			}
			var digit int32
			switch c := input[0]; {
			case c >= 'a' && c <= 'z':
				digit = int32(c - 'a')
			case c >= '0' && c <= '9':
				digit = int32(c-'0') + 26
			default:
				return nil, false
			}
			input = input[1:]
			if digit > (maxInt32-i)/w {
				return nil, false
			}
			i += digit * w
			threshold := punyThreshold(k, bias)
			if digit < threshold {
				break
			}
			if w > maxInt32/(punyBase-threshold) {
				return nil, false
			}
			w *= punyBase - threshold
		}
		count := int32(len(output)) + 1
		bias = punyAdapt(i-oldI, count, oldI == 0)
		if i/count > int32(maxInt32-n) {
			return nil, false
		}
		n += uint32(i / count)
		i %= count
		if n < 0x80 {
			return nil, false
		}
		output = slices.Insert(output, int(i), n)
		i++
	}
	return output, true
}

// idnaPunycodeEncode appends the Punycode form of a label to out. It
// reports false for values that are no code points and for labels that are
// too long to encode.
func idnaPunycodeEncode(out []byte, input []uint32) ([]byte, bool) {
	digitChar := func(digit int32) byte {
		if digit < 26 {
			return byte(digit + 'a')
		}
		return byte(digit + 22)
	}
	n := uint32(punyInitialN)
	delta := int32(0)
	bias := int32(punyInitialBias)
	handled := 0
	for _, c := range input {
		if c < 0x80 {
			handled++
			out = append(out, byte(c))
		}
		// the lower bound is the library's; it lets a few surrogates through
		if c > 0x10ffff || (c >= 0xd880 && c < 0xe000) {
			return out, false
		}
	}
	basic := handled
	if basic > 0 {
		out = append(out, '-')
	}
	for handled < len(input) {
		m := uint32(0x10FFFF)
		for _, c := range input {
			if c >= n && c < m {
				m = c
			}
		}
		if uint64(m-n) > uint64(maxInt32-delta)/uint64(handled+1) {
			return out, false
		}
		delta += int32(uint64(m-n) * uint64(handled+1))
		n = m
		for _, c := range input {
			if c < n {
				if delta == maxInt32 {
					return out, false
				}
				delta++
			}
			if c == n {
				q := delta
				for k := int32(punyBase); ; k += punyBase {
					threshold := punyThreshold(k, bias)
					if q < threshold {
						break
					}
					out = append(out, digitChar(threshold+(q-threshold)%(punyBase-threshold)))
					q = (q - threshold) / (punyBase - threshold)
				}
				out = append(out, digitChar(q))
				bias = punyAdapt(delta, int32(handled+1), handled == basic)
				delta = 0
				handled++
			}
		}
		delta++
		n++
	}
	return out, true
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

// idnaEncodedLabelValid checks a label that came in its "xn--" form: the
// part after the prefix must decode, and what it decodes to must be a label
// that mapping and normalization leave as it is.
func idnaEncodedLabelValid(t *unicodeTables, encoded string) bool {
	decoded, ok := idnaPunycodeDecode(encoded)
	if !ok || len(decoded) == 0 {
		return false
	}
	mapped := idnaMap(t, decoded)
	if !slices.Equal(mapped, decoded) {
		return false
	}
	if !slices.Equal(idnaNormalize(t, slices.Clone(mapped)), mapped) {
		return false
	}
	return idnaLabelValid(t, mapped)
}

// idnaToASCII returns the ASCII form of a domain, or "" when the domain is
// not valid. Labels are separated by '.'; an empty label stays as it is.
func idnaToASCII(t *unicodeTables, domain string) string {
	if isASCII(domain) {
		lower := asciiLower(domain)
		for _, label := range strings.Split(lower, ".") {
			if strings.HasPrefix(label, "xn--") && !idnaEncodedLabelValid(t, label[4:]) {
				return ""
			}
		}
		return lower
	}

	text := make([]uint32, 0, len(domain))
	for _, r := range domain {
		text = append(text, uint32(r))
	}
	text = idnaNormalize(t, idnaMap(t, text))
	var out []byte
	for len(text) > 0 {
		label := text
		last := true
		if dot := slices.Index(text, '.'); dot >= 0 {
			label, text, last = text[:dot], text[dot+1:], false
		} else {
			text = nil
		}
		switch {
		case len(label) == 0:
		case len(label) >= 4 && label[0] == 'x' && label[1] == 'n' && label[2] == '-' && label[3] == '-':
			start := len(out)
			for _, c := range label {
				if c >= 0x80 {
					return ""
				}
				out = append(out, byte(c))
			}
			if !idnaEncodedLabelValid(t, string(out[start+4:])) {
				return ""
			}
		case !slices.ContainsFunc(label, func(c uint32) bool { return c >= 0x80 }):
			for _, c := range label {
				out = append(out, byte(c))
			}
		default:
			if !idnaLabelValid(t, label) {
				return ""
			}
			out = append(out, "xn--"...)
			var ok bool
			if out, ok = idnaPunycodeEncode(out, label); !ok {
				return ""
			}
		}
		if !last {
			out = append(out, '.')
		}
	}
	return string(out)
}

// idnaToUnicode decodes every "xn--" label of an ASCII domain. A label that
// does not decode stays as it is.
func idnaToUnicode(domain string) string {
	var out []byte
	for start := 0; start < len(domain); {
		label := domain[start:]
		last := true
		if dot := strings.IndexByte(label, '.'); dot >= 0 {
			label, last = label[:dot], false
		}
		start += len(label) + 1
		decoded, ok := []uint32(nil), false
		if strings.HasPrefix(label, "xn--") && isASCII(label) {
			decoded, ok = idnaPunycodeDecode(label[4:])
		}
		if ok {
			for _, c := range decoded {
				out = utf8.AppendRune(out, codePointRune(c))
			}
		} else {
			out = append(out, label...)
		}
		if !last {
			out = append(out, '.')
		}
	}
	return string(out)
}

// codePointRune returns the code point as a rune, and U+FFFD for a value
// that is no code point or half of a surrogate pair.
func codePointRune(c uint32) rune {
	if c > utf8.MaxRune || (c >= 0xd800 && c <= 0xdfff) {
		return utf8.RuneError
	}
	return rune(c)
}

// asciiLower lowers the letters A to Z and leaves every other byte alone.
func asciiLower(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] >= 'A' && s[i] <= 'Z' {
			lowered := []byte(s)
			for j := i; j < len(lowered); j++ {
				if lowered[j] >= 'A' && lowered[j] <= 'Z' {
					lowered[j] += 'a' - 'A'
				}
			}
			return string(lowered)
		}
	}
	return s
}
