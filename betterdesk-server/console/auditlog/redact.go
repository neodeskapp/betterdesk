package auditlog

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// The helpers in this file take text as JavaScript sees it: white space is
// what trim and \s match there, lengths count UTF-16 code units, and letter
// case is folded for ASCII letters only. Bytes that are not UTF-8 are read
// as U+FFFD each.

// jsSpaceClass is the inside of a character class for JavaScript's white
// space: the ASCII ones, the Unicode space separators, the two line
// separators and U+FEFF. U+0085 is not part of it.
const jsSpaceClass = `\t\n\v\f\r \x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}`

const (
	jsSpace    = `[` + jsSpaceClass + `]`
	jsNonSpace = `[^` + jsSpaceClass + `]`
)

func isJSSpace(r rune) bool {
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

func trimJSSpace(s string) string {
	return strings.TrimFunc(s, isJSSpace)
}

// toUTF8 returns s with every byte that is not part of a UTF-8 sequence
// replaced by U+FFFD.
func toUTF8(s string) string {
	if utf8.ValidString(s) {
		return s
	}
	return string([]rune(s))
}

// foldASCII writes a regular expression that matches word in any ASCII
// letter case. The (?i) flag is not used because it also matches U+017F for
// "s" and U+212A for "k", which JavaScript's /i does not.
func foldASCII(word string) string {
	var b strings.Builder
	for i := 0; i < len(word); i++ {
		c := word[i]
		switch {
		case c >= 'a' && c <= 'z':
			b.WriteString("[" + string(c) + string(c-'a'+'A') + "]")
		case c >= 'A' && c <= 'Z':
			b.WriteString("[" + string(c-'A'+'a') + string(c) + "]")
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	return b.String()
}

func foldAll(words ...string) string {
	folded := make([]string, len(words))
	for i, word := range words {
		folded[i] = foldASCII(word)
	}
	return strings.Join(folded, "|")
}

var (
	// "Username: alice" and "User: bob". The name is every character up to
	// the next white space, so punctuation that follows a name counts as
	// part of it.
	userLabel = regexp.MustCompile(`\b(` + foldAll("Username", "User") + `):` + jsSpace + `*(` + jsNonSpace + `+)`)
	// "token=abc", "password: abc".
	secretPair = regexp.MustCompile(`(\b(?:` + foldAll("password", "secret", "token", "api_key", "api-key", "key") + `)` +
		jsSpace + `*[:=]` + jsSpace + `*)` + jsNonSpace + `+`)
	sensitiveKey = regexp.MustCompile(foldAll("password", "secret", "token") + `|` + foldASCII("api") + `[_-]?` + foldASCII("key") +
		`|^` + foldASCII("key") + `$`)
)

var lineBreaks = strings.NewReplacer("\r", `\n`, "\n", `\n`, "\u2028", `\n`, "\u2029", `\n`)

// SanitizeLogValue makes a value safe to put on one log line: every CR, LF,
// U+2028 and U+2029 becomes the two characters \n.
func SanitizeLogValue(s string) string {
	return lineBreaks.Replace(toUTF8(s))
}

// RedactUsername shortens a name to its first and last character around
// three stars ("admin" is "a***n"). A name of one or two characters is
// "***", an empty one "(empty)". White space around the name does not
// count.
//
// A character outside the Basic Multilingual Plane at either end comes out
// as U+FFFD: the name is cut by UTF-16 code units, and half a surrogate
// pair is what a log file or a database shows as U+FFFD.
func RedactUsername(username string) string {
	value := trimJSSpace(toUTF8(username))
	if value == "" {
		return "(empty)"
	}
	if utf16Length(value) <= 2 {
		return "***"
	}
	first, _ := utf8.DecodeRuneInString(value)
	last, _ := utf8.DecodeLastRuneInString(value)
	return codeUnit(first) + "***" + codeUnit(last)
}

// codeUnit is the text one UTF-16 code unit of r leaves behind.
func codeUnit(r rune) string {
	if r >= 0x10000 {
		return "\uFFFD"
	}
	return string(r)
}

func utf16Length(s string) int {
	n := 0
	for _, r := range s {
		n++
		if r >= 0x10000 {
			n++
		}
	}
	return n
}

// RedactDetails prepares the free text of an audit entry for the details
// column: line breaks are made harmless (SanitizeLogValue), the name after
// "Username:" or "User:" is shortened (RedactUsername), and the value after
// password, secret, token, api_key, api-key or key with ":" or "=" becomes
// "***". An empty text stays empty.
//
// The name after a label runs to the next white space. "Username: alice;"
// therefore becomes "Username: a***;".
func RedactDetails(details string) string {
	if details == "" {
		return ""
	}
	text := SanitizeLogValue(details)
	text = replaceSubmatches(userLabel, text, func(groups []string) string {
		return groups[1] + ": " + RedactUsername(groups[2])
	})
	return secretPair.ReplaceAllString(text, "${1}***")
}

// SensitiveDetailKey reports whether a field name looks like it holds a
// secret: it contains password, secret, token, api_key, api-key or apikey,
// or it is "key", in any ASCII letter case.
func SensitiveDetailKey(name string) bool {
	return sensitiveKey.MatchString(name)
}

// replaceSubmatches replaces every match of re in s with what fn returns
// for the match and its groups.
func replaceSubmatches(re *regexp.Regexp, s string, fn func(groups []string) string) string {
	matches := re.FindAllStringSubmatchIndex(s, -1)
	if matches == nil {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	end := 0
	groups := make([]string, re.NumSubexp()+1)
	for _, m := range matches {
		b.WriteString(s[end:m[0]])
		for i := range groups {
			groups[i] = ""
			if m[2*i] >= 0 {
				groups[i] = s[m[2*i]:m[2*i+1]]
			}
		}
		b.WriteString(fn(groups))
		end = m[1]
	}
	b.WriteString(s[end:])
	return b.String()
}
