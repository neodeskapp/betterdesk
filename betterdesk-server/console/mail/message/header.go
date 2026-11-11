package message

import (
	"encoding/base64"
	"strings"
)

// Lengths in this file count UTF-16 code units, see jsstring.go.

// maxHeaderLine is the length a header line is folded at. A line gets
// longer only when it holds a single word that is longer.
const maxHeaderLine = 76

// maxEncodedWord is the longest encoded word, "=?UTF-8?Q?...?=", that the
// header encoding writes.
const maxEncodedWord = 52

// isControl reports whether a byte is a control character that an
// unstructured header value cannot carry as it is: all of them but tab,
// line feed and carriage return.
func isControl(c byte) bool {
	return c <= 0x08 || c == 0x0b || c == 0x0c || (c >= 0x0e && c <= 0x1f)
}

// textEncoding picks the encoding for a value that has to be encoded: 'Q'
// (quoted-printable) when it holds more ASCII letters than control
// characters and code units outside ASCII, and 'B' (base64) otherwise.
func textEncoding(value string) byte {
	other, latin := 0, 0
	for _, r := range value {
		switch {
		case r >= 0x10000:
			other += 2
		case r >= 0x80 || isControl(byte(r)):
			other++
		case r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z':
			latin++
		}
	}
	if other < latin {
		return 'Q'
	}
	return 'B'
}

// lineBreaksToSpaces replaces every line break (CR LF, LF or CR) by one
// space. It is what keeps a header value on its line.
func lineBreaksToSpaces(value string) string {
	if !strings.ContainsAny(value, "\r\n") {
		return value
	}
	return strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ").Replace(value)
}

// encodeHeaderText returns an unstructured header value (the subject) as it
// goes on the line: unchanged when it is printable ASCII without a quote,
// and as encoded words otherwise. The caller has replaced line breaks.
func encodeHeaderText(value string) string {
	needsEncoding := false
	for i := 0; i < len(value); i++ {
		c := value[i]
		if c >= 0x80 || c == '"' || c == 0x7f || isControl(c) {
			needsEncoding = true
			break
		}
	}
	if !needsEncoding {
		return value
	}
	return encodeWord(value, textEncoding(value), maxEncodedWord)
}

const hexDigits = "0123456789ABCDEF"

// encodeWord returns a value as one or more encoded words (RFC 2047) in
// UTF-8, separated by spaces, each at most maxLength long. encoding is 'Q'
// or 'B'. A word never ends inside a character.
func encodeWord(value string, encoding byte, maxLength int) string {
	const overhead = len("=?UTF-8?Q??=")
	if maxLength > overhead {
		maxLength -= overhead
	}
	separator := "?= =?UTF-8?" + string(encoding) + "?"
	var encoded string
	if encoding == 'Q' {
		encoded = qEncode(value)
		if maxLength > 0 && len(encoded) > maxLength {
			encoded = strings.Join(splitQEncoded(encoded, maxLength), separator)
		}
	} else {
		if maxLength > 0 {
			// the bytes that fit into a word of that many base64 characters
			maxLength = max(3, (maxLength-maxLength%4)/4*3)
		}
		encoded = base64.StdEncoding.EncodeToString([]byte(value))
		if maxLength > 0 && len(encoded) > maxLength {
			var parts []string
			start := 0
			for i, r := range value {
				if i+len(string(r))-start > maxLength && i > start {
					parts = append(parts, base64.StdEncoding.EncodeToString([]byte(value[start:i])))
					start = i
				}
			}
			parts = append(parts, base64.StdEncoding.EncodeToString([]byte(value[start:])))
			encoded = strings.Join(parts, separator)
		}
	}
	if strings.HasSuffix(encoded, "?=") {
		return "=?UTF-8?" + string(encoding) + "?" + encoded
	}
	return "=?UTF-8?" + string(encoding) + "?" + encoded + "?="
}

// qEncode returns the text of a Q encoded word: letters, digits and !*+-/
// stay, a space becomes '_', and every other byte of the UTF-8 form becomes
// "=XX". A space at the end or in front of a line break is "=20".
func qEncode(value string) string {
	var out strings.Builder
	out.Grow(len(value))
	for i := 0; i < len(value); i++ {
		c := value[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '!', c == '*', c == '+', c == '-', c == '/':
			out.WriteByte(c)
		case c == ' ' && i+1 < len(value) && value[i+1] != '\n' && value[i+1] != '\r':
			out.WriteByte('_')
		default:
			out.WriteByte('=')
			out.WriteByte(hexDigits[c>>4])
			out.WriteByte(hexDigits[c&0xf])
		}
	}
	return out.String()
}

func isHexDigit(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

// hexValue returns the value of two hex digits.
func hexValue(high, low byte) int {
	return digitValue(high)<<4 | digitValue(low)
}

// splitQEncoded cuts Q encoded text into parts of at most maxLength
// characters (at least 12) without cutting through a "=XX" or through the
// bytes of one character.
func splitQEncoded(text string, maxLength int) []string {
	maxLength = max(maxLength, 12)
	var parts []string
	for len(text) > 0 {
		line := text[:min(maxLength, len(text))]
		// an escape that does not fit goes to the next part
		if n := len(line); n >= 1 && line[n-1] == '=' {
			line = line[:n-1]
		} else if n >= 2 && line[n-2] == '=' && isHexDigit(line[n-1]) {
			line = line[:n-2]
		}
		fallback := line
		if fallback == "" {
			fallback = text[:min(maxLength, len(text))]
		}
		// while the next part would start with a byte from the inside of a
		// character, give it the three characters in front as well
		for len(line) > 0 {
			rest := text[len(line):]
			if len(rest) < 3 || rest[0] != '=' || !isHexDigit(rest[1]) || !isHexDigit(rest[2]) {
				break
			}
			if b := hexValue(rest[1], rest[2]); b >= 0xc2 || b <= 0x7f {
				break
			}
			line = line[:max(len(line)-3, 0)]
		}
		if line == "" {
			line = fallback
		}
		parts = append(parts, line)
		text = text[len(line):]
	}
	return parts
}

// foldLines breaks a header line into lines of at most maxHeaderLine
// characters. A break goes in front of the last run of white space that
// leaves something on the line, so the next line starts with that white
// space. A word that does not fit stays in one piece on a line of its own.
func foldLines(line string) string {
	units := utf16Units(line)
	space := func(u uint16) bool { return isJSSpace(rune(u)) }
	result := make([]uint16, 0, len(units)+len(units)/maxHeaderLine*2+2)
	for pos := 0; pos < len(units); {
		if len(units)-pos < maxHeaderLine {
			result = append(result, units[pos:]...)
			break
		}
		end := pos + maxHeaderLine
		window := units[pos:end]
		if lineBreak := lineBreakEnd(window); lineBreak > 0 {
			result = append(result, window[:lineBreak]...)
			pos += lineBreak
			continue
		}
		// the start of the last run of white space in the window
		cut := len(window)
		for cut > 0 && !space(window[cut-1]) {
			cut--
		}
		for cut > 0 && space(window[cut-1]) {
			cut--
		}
		switch {
		case cut > 0:
			end = pos + cut
		default:
			// no place to break: take the rest of the word as well
			for end < len(units) && !space(units[end]) {
				end++
			}
		}
		result = append(result, units[pos:end]...)
		pos = end
		if pos < len(units) {
			result = append(result, '\r', '\n')
		}
	}
	return unitsString(result)
}

// lineBreakEnd returns the position after the first line break in the
// window (CR LF, LF or CR), or 0 when it has none.
func lineBreakEnd(window []uint16) int {
	for i, u := range window {
		switch {
		case u == '\n':
			return i + 1
		case u == '\r' && i+1 < len(window) && window[i+1] == '\n':
			return i + 2
		case u == '\r':
			return i + 1
		}
	}
	return 0
}
