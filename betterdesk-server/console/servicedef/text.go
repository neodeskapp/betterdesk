package servicedef

import (
	"regexp"
	"strings"
)

// The text changes below work on a systemd unit or on the environment
// block of a Windows service. Their line rules are those of JavaScript's
// multiline regular expressions: a line ends at \n, \r, U+2028 or
// U+2029.

// tlsFlag is a Go API TLS flag with the blanks in front of it.
var tlsFlag = regexp.MustCompile(`[ \t]+-(?:tls-api|force-https)`)

var blankRun = regexp.MustCompile(`[ \t]{2,}`)

// StripIncompatibleGoAPITLSArgs removes the flags -tls-api and
// -force-https, each with the blanks in front of it, where white space or
// the end follows. compact also folds runs of blanks into one and trims
// the result, for a Windows service's one-line parameters.
func StripIncompatibleGoAPITLSArgs(value string, compact bool) string {
	var b strings.Builder
	last := 0
	for _, m := range tlsFlag.FindAllStringIndex(value, -1) {
		if m[1] < len(value) && !isJSSpaceAt(value, m[1]) {
			continue
		}
		b.WriteString(value[last:m[0]])
		last = m[1]
	}
	b.WriteString(value[last:])
	stripped := b.String()
	if !compact {
		return stripped
	}
	return jsTrim(blankRun.ReplaceAllString(stripped, " "))
}

// EnsureMeshEnabledInServiceEnv adds MESH_ENABLED=Y to a Windows
// service's environment block that does not set MESH_ENABLED, at the end,
// after removing the line ends there. An empty block becomes that one
// line.
func EnsureMeshEnabledInServiceEnv(envText string) (string, bool) {
	if envText == "" {
		return envText, false
	}
	if lineStarting(envText, "MESH_ENABLED=") >= 0 {
		return envText, false
	}
	trimmed := strings.TrimRight(strings.ReplaceAll(envText, "\r\n", "\n"), "\n")
	const line = "MESH_ENABLED=Y"
	if trimmed == "" {
		return line, true
	}
	return trimmed + "\n" + line, true
}

// EnsureGoServerEnvironmentFile makes a systemd unit read the console's
// .env file: an EnvironmentFile= line before the AUTH_DB_PATH line, else
// right after [Service], else in front. A unit that has an
// EnvironmentFile= line already stays as it is.
func EnsureGoServerEnvironmentFile(unitText, envFilePath string) (string, bool) {
	if unitText == "" || lineStarting(unitText, "EnvironmentFile=") >= 0 {
		return unitText, false
	}
	envLine := "EnvironmentFile=-" + envFilePath
	if start := lineStarting(unitText, "Environment=AUTH_DB_PATH="); start >= 0 {
		end := lineEnd(unitText, start)
		line := unitText[start:end]
		return unitText[:start] + expandReplacement(envLine+"\n$1", unitText, start, end, true, line) + unitText[end:], true
	}
	if start := lineStarting(unitText, "[Service]"); start >= 0 {
		end := start + len("[Service]")
		return unitText[:start] + expandReplacement("[Service]\n"+envLine, unitText, start, end, false, "") + unitText[end:], true
	}
	return envLine + "\n" + unitText, true
}

// EnsureGoServerSignalRelayPorts adds SIGNAL_PORT=21116, RELAY_PORT=21117
// and GO_API_PORT=21114 as Environment= lines to a systemd unit that does
// not set them, each after the AUTH_DB_PATH line, else right after
// [Service], else in front, so the console's .env cannot move the
// server's ports.
func EnsureGoServerSignalRelayPorts(unitText string) (string, bool) {
	if unitText == "" {
		return unitText, false
	}
	text, changed := unitText, false
	for _, port := range [][2]string{{"SIGNAL_PORT", "21116"}, {"RELAY_PORT", "21117"}, {"GO_API_PORT", "21114"}} {
		line := "Environment=" + port[0] + "=" + port[1]
		if lineStarting(text, "Environment="+port[0]+"=") >= 0 {
			continue
		}
		text = insertServiceLine(text, line)
		changed = true
	}
	return text, changed
}

// insertServiceLine puts a line after the AUTH_DB_PATH line, else right
// after [Service], else in front.
func insertServiceLine(text, line string) string {
	if start := lineStarting(text, "Environment=AUTH_DB_PATH="); start >= 0 {
		end := lineEnd(text, start)
		return text[:end] + "\n" + line + text[end:]
	}
	if start := lineStarting(text, "[Service]"); start >= 0 {
		end := start + len("[Service]")
		return text[:end] + "\n" + line + text[end:]
	}
	return line + "\n" + text
}

// lineStarting is the index of the first line that starts with prefix,
// or -1.
func lineStarting(text, prefix string) int {
	for start := 0; start <= len(text); {
		if strings.HasPrefix(text[start:], prefix) {
			return start
		}
		end := lineEnd(text, start)
		if end >= len(text) {
			return -1
		}
		start = end + lineTerminatorLength(text, end)
	}
	return -1
}

// lineEnd is the index of the line end after start, or the length of the
// text.
func lineEnd(text string, start int) int {
	for i := start; i < len(text); {
		if n := lineTerminatorLength(text, i); n > 0 {
			return i
		}
		i++
	}
	return len(text)
}

// lineTerminatorLength is the length in bytes of the line terminator at
// i, 0 for none.
func lineTerminatorLength(text string, i int) int {
	switch {
	case text[i] == '\n', text[i] == '\r':
		return 1
	case strings.HasPrefix(text[i:], "\u2028"), strings.HasPrefix(text[i:], "\u2029"):
		return 3
	}
	return 0
}

// expandReplacement expands the $ patterns of a JavaScript replacement
// text for a match from start to end: $$, $&, $` and $', and with one
// group (hasGroup) $1 and $01. Anything else after a $ stays as it is.
func expandReplacement(replacement, text string, start, end int, hasGroup bool, group string) string {
	var b strings.Builder
	for i := 0; i < len(replacement); i++ {
		c := replacement[i]
		if c != '$' || i+1 >= len(replacement) {
			b.WriteByte(c)
			continue
		}
		next := replacement[i+1]
		switch {
		case next == '$':
			b.WriteByte('$')
			i++
		case next == '&':
			b.WriteString(text[start:end])
			i++
		case next == '`':
			b.WriteString(text[:start])
			i++
		case next == '\'':
			b.WriteString(text[end:])
			i++
		case hasGroup && strings.HasPrefix(replacement[i+1:], "01"):
			b.WriteString(group)
			i += 2
		case hasGroup && next == '1':
			b.WriteString(group)
			i++
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// jsTrim is String.prototype.trim.
func jsTrim(s string) string { return strings.TrimFunc(s, isJSSpace) }

func isJSSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0xA0, 0x1680, 0x2028, 0x2029, 0x202F, 0x205F, 0x3000, 0xFEFF:
		return true
	}
	return r >= 0x2000 && r <= 0x200A
}

// isJSSpaceAt reports whether the character at byte i is white space for
// JavaScript's \s.
func isJSSpaceAt(s string, i int) bool {
	for _, r := range s[i:] {
		return isJSSpace(r)
	}
	return false
}
