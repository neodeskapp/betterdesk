package message

import (
	"bytes"
	"encoding/base64"
	"strings"
)

// Transfer encodings of a text part.
const (
	transfer7bit   = "7bit"
	transferQP     = "quoted-printable"
	transferBase64 = "base64"
)

// maxBodyLine is the line length of an encoded body, and the longest line a
// text may have to go out unencoded.
const maxBodyLine = 76

// transferEncoding picks how a text goes into the message: as it is when it
// is ASCII without control characters and no line is longer than 76
// characters, else quoted-printable or base64, whichever suits the text
// (see textEncoding).
func transferEncoding(content string) string {
	if isPlainText(content) && !hasLongerLines(content, maxBodyLine) {
		return transfer7bit
	}
	if textEncoding(content) == 'Q' {
		return transferQP
	}
	return transferBase64
}

// isPlainText reports whether a text is ASCII without control characters
// other than tab, line feed and carriage return. DEL counts as plain.
func isPlainText(content string) bool {
	for i := 0; i < len(content); i++ {
		if c := content[i]; c >= 0x80 || isControl(c) {
			return false
		}
	}
	return true
}

// hasLongerLines reports whether a line of an ASCII text has more than
// limit characters. Lines end at a line feed or a carriage return. A text
// of more than 128 KiB is not looked at and counts as having one.
func hasLongerLines(content string, limit int) bool {
	if len(content) > 128*1024 {
		return true
	}
	length := 0
	for i := 0; i < len(content); i++ {
		if content[i] == '\n' || content[i] == '\r' {
			length = 0
		} else if length++; length > limit {
			return true
		}
	}
	return false
}

// encodeBody returns a text in the given transfer encoding.
func encodeBody(content, encoding string) []byte {
	switch encoding {
	case transferQP:
		return qpEncodeBody([]byte(content))
	case transferBase64:
		return base64Body([]byte(content))
	}
	return []byte(content)
}

// base64Body encodes to base64 in lines of 76 characters. The last line has
// no line break.
func base64Body(content []byte) []byte {
	encoded := make([]byte, base64.StdEncoding.EncodedLen(len(content)))
	base64.StdEncoding.Encode(encoded, content)
	out := make([]byte, 0, len(encoded)+len(encoded)/maxBodyLine*2)
	for len(encoded) > maxBodyLine {
		out = append(out, encoded[:maxBodyLine]...)
		out = append(out, '\r', '\n')
		encoded = encoded[maxBodyLine:]
	}
	return append(out, encoded...)
}

// qpSliceSize is the number of input bytes that are encoded and wrapped in
// one go.
const qpSliceSize = 64 * 1024

// qpEncodeBody encodes a text as quoted-printable with soft line breaks.
//
// White space at the very end of the text is encoded, however long the run
// is not: only its last byte. The input is worked through in slices of
// 64 KiB; the last line of a slice is wrapped again together with the next
// slice.
func qpEncodeBody(content []byte) []byte {
	// trailing white space waits until the rest is done, unless there is
	// more of it than one slice holds
	end := len(content)
	for end > 0 && (content[end-1] == ' ' || content[end-1] == '\t') {
		end--
	}
	var held []byte
	if len(content)-end <= qpSliceSize {
		held = content[end:]
		content = content[:end]
	}

	var out []byte
	line := ""
	encodeSlices := func(buf []byte) {
		for start := 0; start < len(buf); start += qpSliceSize {
			sliceEnd := min(start+qpSliceSize, len(buf))
			next := -1
			if sliceEnd < len(buf) {
				next = int(buf[sliceEnd])
			}
			wrapped := qpWrap(line+qpEncodeBytes(buf[start:sliceEnd], next), maxBodyLine)
			lastBreak := strings.LastIndexByte(wrapped, '\n')
			out = append(out, wrapped[:lastBreak+1]...)
			line = wrapped[lastBreak+1:]
		}
	}
	encodeSlices(content)
	encodeSlices(held)
	return append(out, line...)
}

// qpEncodeBytes encodes bytes as quoted-printable without wrapping. Tab,
// line feed, carriage return and printable ASCII except '=' stay; a space
// or tab in front of a line break or at the end is encoded. next is the
// byte that follows the buffer, or -1 at the end of the text.
func qpEncodeBytes(buf []byte, next int) string {
	var out strings.Builder
	out.Grow(len(buf) + len(buf)/8)
	for i, c := range buf {
		following := next
		if i+1 < len(buf) {
			following = int(buf[i+1])
		}
		literal := c == '\t' || c == '\n' || c == '\r' || (c >= 0x20 && c <= 0x7e && c != '=')
		if literal && (c == ' ' || c == '\t') && (following < 0 || following == '\n' || following == '\r') {
			literal = false
		}
		if literal {
			out.WriteByte(c)
			continue
		}
		out.WriteByte('=')
		out.WriteByte(hexDigits[c>>4])
		out.WriteByte(hexDigits[c&0xf])
	}
	return out.String()
}

// endsWithEscape reports how the end of a line relates to a "=XX" escape:
// 3 when the line ends with a complete one, 2 or 1 when it ends with the
// first two or the first character of one, 0 otherwise.
func endsWithEscape(line string) int {
	n := len(line)
	switch {
	case n >= 3 && line[n-3] == '=' && isHexDigit(line[n-2]) && isHexDigit(line[n-1]):
		return 3
	case n >= 2 && line[n-2] == '=' && isHexDigit(line[n-1]):
		return 2
	case n >= 1 && line[n-1] == '=':
		return 1
	}
	return 0
}

// isEscapeRun reports whether a line is made of one to four "=XX" escapes
// and nothing else.
func isEscapeRun(line string) bool {
	if len(line) == 0 || len(line) > 12 || len(line)%3 != 0 {
		return false
	}
	for i := 0; i < len(line); i += 3 {
		if line[i] != '=' || !isHexDigit(line[i+1]) || !isHexDigit(line[i+2]) {
			return false
		}
	}
	return true
}

// qpWrap puts soft line breaks ("=" and CR LF) into quoted-printable text
// so that no line is longer than lineLength. It breaks after the last
// space, tab or one of .,!? in the last third of a line where it can, and
// never inside a "=XX" escape or between the bytes of one character.
func qpWrap(text string, lineLength int) string {
	if len(text) <= lineLength {
		return text
	}
	margin := lineLength / 3
	var out strings.Builder
	out.Grow(len(text) + len(text)/lineLength*3 + 3)
	for pos := 0; pos < len(text); {
		line := text[pos:min(pos+lineLength, len(text))]
		if lineBreak := strings.Index(line, "\r\n"); lineBreak >= 0 {
			out.WriteString(line[:lineBreak+2])
			pos += lineBreak + 2
			continue
		}
		if line[len(line)-1] == '\n' {
			out.WriteString(line)
			pos += len(line)
			continue
		}
		tail := line[max(len(line)-margin, 0):]
		if lineFeed := strings.LastIndexByte(tail, '\n'); lineFeed >= 0 && strings.IndexByte(tail[lineFeed:], '\r') < 0 {
			// a line feed in the last third: the line ends there
			line = line[:len(line)-len(tail)+lineFeed+1]
			out.WriteString(line)
			pos += len(line)
			continue
		}
		if stop := strings.LastIndexAny(tail, " \t.,!?"); len(line) > lineLength-margin && stop >= 0 {
			line = line[:len(line)-len(tail)+stop+1]
		} else if escape := endsWithEscape(line); escape > 0 {
			if escape < 3 {
				line = line[:len(line)-escape]
			}
			// keep the bytes of one character on one line: go back over
			// the bytes from its inside to its first one
			for len(line) > 3 && len(line) < len(text)-pos && !isEscapeRun(line) && endsWithEscape(line) == 3 {
				code := hexValue(line[len(line)-2], line[len(line)-1])
				if code < 128 {
					break
				}
				line = line[:len(line)-3]
				if code >= 0xc0 {
					break
				}
			}
		}
		if line == "" {
			line = text[pos:min(pos+lineLength, len(text))]
		}
		if pos+len(line) < len(text) && line[len(line)-1] != '\n' {
			if len(line) == lineLength {
				if endsWithEscape(line) == 3 {
					line = line[:len(line)-3]
				} else {
					line = line[:len(line)-1]
				}
			}
			pos += len(line)
			out.WriteString(line)
			out.WriteString("=\r\n")
		} else {
			pos += len(line)
			out.WriteString(line)
		}
	}
	return out.String()
}

// endsWithNewline appends what the message needs to end in a line feed:
// nothing after one, a line feed after a carriage return, CR LF otherwise.
func endsWithNewline(message []byte) []byte {
	switch {
	case bytes.HasSuffix(message, []byte("\n")):
		return message
	case bytes.HasSuffix(message, []byte("\r")):
		return append(message, '\n')
	}
	return append(message, '\r', '\n')
}
