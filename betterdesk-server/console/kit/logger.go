package kit

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/unitronix/betterdesk-server/console/auditlog"
	"github.com/unitronix/betterdesk-server/logging"
)

// Logger is the console's logger for route groups. It drops lines below its
// level, and it keeps line breaks and user names out of what it prints.
//
// The level is the LOG_LEVEL setting: error, warn, info or debug. Without
// one, or with another word, it is warn in production and info otherwise.
//
// The arguments of a call are joined with one space. A string has its line
// breaks made harmless and the names in single quotes shortened ("user
// 'administrator'" prints as "user 'a***r'"); an error prints its message,
// treated the same way; anything else prints as fmt.Sprint writes it.
// Nothing is read as a format: a "%" prints as it is.
//
// Lines go to the logging package under the component "console", or under
// the prefix of a logger made with Child. Error, warn and debug lines start
// with "ERROR: ", "WARN: " and "DEBUG: ", which is how the logging package
// tells the level of a line.
type Logger struct {
	level  string
	prefix string
	// out takes the lines in place of the logging package when it is set:
	// the level and the joined arguments. Tests use it.
	out func(level, line string)
}

// logLevels are the levels in the order of their weight, and the word a
// line of each level starts with.
var logLevels = []struct{ name, word string }{
	{"error", "ERROR: "},
	{"warn", "WARN: "},
	{"info", ""},
	{"debug", "DEBUG: "},
}

func logRank(level string) int {
	for rank, l := range logLevels {
		if l.name == level {
			return rank
		}
	}
	return -1
}

// NewLogger returns a logger for the raw value of LOG_LEVEL. White space
// around the value and the case of its letters do not count.
func NewLogger(level string, production bool) *Logger {
	name := strings.TrimFunc(level, isLogSpace)
	name = strings.Map(func(r rune) rune {
		if r >= 'A' && r <= 'Z' {
			return r + 'a' - 'A'
		}
		return r
	}, name)
	if logRank(name) < 0 {
		name = "info"
		if production {
			name = "warn"
		}
	}
	return &Logger{level: name}
}

// Child returns a logger with the same level whose lines carry prefix as
// their component: Child("AUTH") prints "[AUTH] ...". An empty prefix gives
// the component "console".
func (l *Logger) Child(prefix string) *Logger {
	child := *l
	child.prefix = prefix
	return &child
}

// Level returns the level in use: error, warn, info or debug.
func (l *Logger) Level() string { return l.level }

// Error logs at level error.
func (l *Logger) Error(args ...any) { l.log("error", args) }

// Warn logs at level warn.
func (l *Logger) Warn(args ...any) { l.log("warn", args) }

// Info logs at level info.
func (l *Logger) Info(args ...any) { l.log("info", args) }

// Debug logs at level debug.
func (l *Logger) Debug(args ...any) { l.log("debug", args) }

func (l *Logger) log(level string, args []any) {
	rank := logRank(level)
	if rank > logRank(l.level) {
		return
	}
	parts := make([]string, len(args))
	for i, arg := range args {
		switch v := arg.(type) {
		case string:
			parts[i] = formatLogPart(v)
		case error:
			parts[i] = formatLogPart(v.Error())
		default:
			parts[i] = auditlog.SanitizeLogValue(fmt.Sprint(v))
		}
	}
	line := strings.Join(parts, " ")
	if l.out != nil {
		l.out(level, line)
		return
	}
	logging.Logf(l.component(), "%s%s", logLevels[rank].word, line)
}

func (l *Logger) component() string {
	if l.prefix == "" {
		return "console"
	}
	return l.prefix
}

// logSpaceClass is the inside of a character class for the white space
// JavaScript knows (its trim and \s): the ASCII ones, the Unicode space
// separators, the two line separators and U+FEFF, but not U+0085.
const logSpaceClass = `\t\n\v\f\r \x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}`

func isLogSpace(r rune) bool {
	switch {
	case r >= '\t' && r <= '\r', r == ' ', r == 0xA0, r == 0x1680:
		return true
	case r >= 0x2000 && r <= 0x200A:
		return true
	case r == 0x2028, r == 0x2029, r == 0x202F, r == 0x205F, r == 0x3000, r == 0xFEFF:
		return true
	}
	return false
}

// logNameAfterWord finds a quoted name after one of the words sign-in
// messages use: "for 'alice'", "local user 'alice'". The words match in any
// ASCII letter case, also at the end of a longer word.
var logNameAfterWord = regexp.MustCompile(`(?:[fF][oO][rR]|[uU][sS][eE][rR]|[aA][cC][cC][oO][uU][nN][tT]|` +
	`[cC][rR][eE][dD][eE][nN][tT][iI][aA][lL][sS] [fF][oO][rR]|[lL][oO][cC][aA][lL] [uU][sS][eE][rR]|[sS][yY][nN][cC][eE][dD])` +
	`[` + logSpaceClass + `]+'([^']+)'`)

// formatLogPart prepares one string argument: line breaks become "\n" in
// letters, then user names in single quotes are shortened in two passes.
//
// The first pass shortens the quoted name of every match of
// logNameAfterWord, in its place between the quotes: "for 'or'" becomes
// "for '***'". The second pass (maskQuotedNames) takes every quoted text.
func formatLogPart(s string) string {
	s = auditlog.SanitizeLogValue(s)
	if !strings.Contains(s, "'") {
		return s
	}
	if matches := logNameAfterWord.FindAllStringSubmatchIndex(s, -1); matches != nil {
		var b strings.Builder
		end := 0
		for _, m := range matches {
			b.WriteString(s[end:m[2]])
			b.WriteString(auditlog.RedactUsername(s[m[2]:m[3]]))
			end = m[3]
		}
		b.WriteString(s[end:])
		s = b.String()
	}
	return maskQuotedNames(s)
}

// maskQuotedNames shortens every text between two single quotes that looks
// like a user name. A text counts when it is 2 to 64 UTF-16 code units
// long; maskQuotedName decides what looks like a name. A quote that closes
// one text does not open the next.
func maskQuotedNames(s string) string {
	open := strings.IndexByte(s, '\'')
	if open < 0 {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	b.WriteString(s[:open])
	for {
		length := strings.IndexByte(s[open+1:], '\'')
		if length < 0 {
			b.WriteString(s[open:])
			return b.String()
		}
		closing := open + 1 + length
		inner := s[open+1 : closing]
		if units := logUTF16Length(inner); units < 2 || units > 64 {
			b.WriteString(s[open:closing])
			open = closing
			continue
		}
		b.WriteByte('\'')
		b.WriteString(maskQuotedName(inner))
		b.WriteByte('\'')
		next := strings.IndexByte(s[closing+1:], '\'')
		if next < 0 {
			b.WriteString(s[closing+1:])
			return b.String()
		}
		b.WriteString(s[closing+1 : closing+1+next])
		open = closing + 1 + next
	}
}

// The quoted texts that could be user names by their letters but are not:
// what sign-in messages quote besides names.
var (
	logQuotedPrefixes = []string{"betterdesk_", "pbkdf2", "bcrypt"}
	logQuotedWords    = []string{"admin", "operator", "viewer", "user", "role", "provider", "empty", "invalid"}
)

// maskQuotedName shortens a quoted text made of ASCII letters, digits, "_",
// "@" and "-" only. Anything else stays as it is: paths and addresses, and
// texts with a space or a "+" such as 'hash type' and 'LDAP+local'. So do
// the texts that start with one of logQuotedPrefixes or are one of
// logQuotedWords, in any ASCII letter case.
func maskQuotedName(inner string) string {
	lower := make([]byte, 0, len(inner))
	for i := 0; i < len(inner); i++ {
		c := inner[i]
		switch {
		case c >= 'A' && c <= 'Z':
			c += 'a' - 'A'
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '_', c == '@', c == '-':
		default:
			return inner
		}
		lower = append(lower, c)
	}
	for _, prefix := range logQuotedPrefixes {
		if strings.HasPrefix(string(lower), prefix) {
			return inner
		}
	}
	for _, word := range logQuotedWords {
		if string(lower) == word {
			return inner
		}
	}
	return auditlog.RedactUsername(inner)
}

func logUTF16Length(s string) int {
	n := 0
	for _, r := range s {
		n++
		if r >= 0x10000 {
			n++
		}
	}
	return n
}
