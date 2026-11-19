package auditlog

import (
	"encoding/hex"
	"strings"
	"testing"
	"unicode/utf8"
)

// The bug fixes of BUGS.md that change answers of the Node recordings in
// testdata (vectors.json, rows.json). The replays take the console's
// answer from the rules below where a fix changes Node's.

// detailsRules are the fixes a reference of RedactDetails applies.
type detailsRules struct {
	// labelAtStart: the user label counts at the start of the text only
	// (AL1). nameEndsAtSemicolon: the name after it ends before ";" too
	// (S19). beforeLineBreaks: the text is redacted before its line breaks
	// become the letters \n, and a secret's key, its ":" or "=" and its
	// value are on one line (AL2).
	labelAtStart, nameEndsAtSemicolon, beforeLineBreaks bool
}

var (
	nodeDetails = detailsRules{}
	nowDetails  = detailsRules{labelAtStart: true, nameEndsAtSemicolon: true}
)

// referenceDetails is RedactDetails written out by hand under the rules.
// Under nodeDetails it gives Node's answers.
func referenceDetails(in string, rules detailsRules) string {
	if in == "" {
		return ""
	}
	if rules.beforeLineBreaks {
		return SanitizeLogValue(referenceSecrets(referenceLabels(toUTF8(in), rules), rules))
	}
	return referenceSecrets(referenceLabels(SanitizeLogValue(in), rules), rules)
}

func isWordByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_'
}

// wordStartsAt is JavaScript's \b before a word character at i.
func wordStartsAt(text string, i int) bool { return i == 0 || !isWordByte(text[i-1]) }

// foldedAt reports whether word, in lower case ASCII, is at i in any ASCII
// letter case.
func foldedAt(text string, i int, word string) bool {
	if i+len(word) > len(text) {
		return false
	}
	for j := 0; j < len(word); j++ {
		c := text[i+j]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		if c != word[j] {
			return false
		}
	}
	return true
}

// afterSpace is the index after the white space at i.
func afterSpace(text string, i int) int { return afterSpaceOnLine(text, i, false) }

// afterSpaceOnLine is afterSpace that stops at a line break when onLine is
// set.
func afterSpaceOnLine(text string, i int, onLine bool) int {
	for i < len(text) {
		r, size := utf8.DecodeRuneInString(text[i:])
		if !isJSSpace(r) || onLine && (r == '\n' || r == '\r' || r == 0x2028 || r == 0x2029) {
			break
		}
		i += size
	}
	return i
}

// referenceLabels shortens the name after "Username:" or "User:".
func referenceLabels(text string, rules detailsRules) string {
	var b strings.Builder
	i, done := 0, 0
	for i < len(text) && (i == 0 || !rules.labelAtStart) {
		start, end, label := 0, 0, ""
		if wordStartsAt(text, i) {
			for _, word := range []string{"username", "user"} {
				if !foldedAt(text, i, word) || i+len(word) >= len(text) || text[i+len(word)] != ':' {
					continue
				}
				start = afterSpace(text, i+len(word)+1)
				end = start
				for end < len(text) {
					r, size := utf8.DecodeRuneInString(text[end:])
					if isJSSpace(r) || rules.nameEndsAtSemicolon && r == ';' {
						break
					}
					end += size
				}
				if end > start {
					label = text[i : i+len(word)]
					break
				}
			}
		}
		if label == "" {
			i++
			continue
		}
		b.WriteString(text[done:i])
		b.WriteString(label + ": " + RedactUsername(text[start:end]))
		i, done = end, end
	}
	b.WriteString(text[done:])
	return b.String()
}

var secretKeys = []string{"password", "secret", "token", "api_key", "api-key", "key"}

// referenceSecrets writes *** for the value after a secret's key and its
// ":" or "=".
func referenceSecrets(text string, rules detailsRules) string {
	var b strings.Builder
	i, done := 0, 0
	for i < len(text) {
		start, end := secretValueAt(text, i, rules)
		if end == start {
			i++
			continue
		}
		b.WriteString(text[done:start])
		b.WriteString("***")
		i, done = end, end
	}
	b.WriteString(text[done:])
	return b.String()
}

// secretValueAt is where the value of a secret whose key starts at i
// begins and ends; end equals start when no key with a value is there.
func secretValueAt(text string, i int, rules detailsRules) (start, end int) {
	if !wordStartsAt(text, i) {
		return 0, 0
	}
	for _, key := range secretKeys {
		if !foldedAt(text, i, key) {
			continue
		}
		j := afterSpaceOnLine(text, i+len(key), rules.beforeLineBreaks)
		if j >= len(text) || text[j] != ':' && text[j] != '=' {
			continue
		}
		start = afterSpaceOnLine(text, j+1, rules.beforeLineBreaks)
		end = start
		for end < len(text) {
			r, size := utf8.DecodeRuneInString(text[end:])
			if isJSSpace(r) {
				break
			}
			end += size
		}
		if end > start {
			return start, end
		}
	}
	return 0, 0
}

// TestReferenceDetailsIsNodes: under Node's rules the reference gives
// every answer Node recorded, so the rules it changes are the only
// difference.
func TestReferenceDetailsIsNodes(t *testing.T) {
	for _, v := range loadRedactVectors(t).Details {
		if got := referenceDetails(v.In, nodeDetails); got != v.Out {
			t.Errorf("referenceDetails(%q) = %q, Node answers %q", v.In, got, v.Out)
		}
	}
}

// fixedDetails is the details text the console stores for a text.
func fixedDetails(in string) string { return referenceDetails(in, nowDetails) }

// fixedRow is the row the console stores where Node stored want: the
// details under the fixed rules.
func fixedRow(want storedRow, details *string) storedRow {
	if details == nil || want.DetailsHex == nil {
		return want
	}
	now := hex.EncodeToString([]byte(fixedDetails(*details)))
	want.DetailsHex = &now
	return want
}

// nodeOrFixed is what the console answers for a vector: Node's answer as
// the fixes change it.
func nodeOrFixed(name string, v vector) string {
	switch name {
	case "RedactDetails":
		return fixedDetails(v.In)
	}
	return v.Out
}

// TestFixesChangeVectors: each fix changes some recorded answers.
func TestFixesChangeVectors(t *testing.T) {
	file := loadRedactVectors(t)
	for name, rules := range map[string]detailsRules{
		"AL1": {labelAtStart: true}, "S19": {nameEndsAtSemicolon: true},
	} {
		changed := 0
		for _, v := range file.Details {
			if referenceDetails(v.In, rules) != v.Out {
				changed++
			}
		}
		if changed == 0 {
			t.Errorf("%s changes no RedactDetails vector", name)
		}
	}
}

// TestRedactDetailsEndsTheNameAtPunctuation: the name after the label of a
// sign-in entry ends before ";", so it is shortened whole (BUGS.md S19).
func TestRedactDetailsEndsTheNameAtPunctuation(t *testing.T) {
	for in, want := range map[string]string{
		"Username: alice; ip=127.0.0.1; ua=x":        "Username: a***e; ip=127.0.0.1; ua=x",
		"Username: john.doe@example.com; ip=1.2.3.4": "Username: j***m; ip=1.2.3.4",
		"User: bob":            "User: b***b",
		"username:carol;":      "username: c***l;",
		"Username: a,,b; ip=1": "Username: a***b; ip=1",
	} {
		if got := RedactDetails(in); got != want {
			t.Errorf("RedactDetails(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestRedactDetailsKeepsAccountNamesOfUserManagement: a label later in
// the text is no label, so the entries of the user management keep the
// account an administrator created, changed, deleted or reset (BUGS.md
// AL1).
func TestRedactDetailsKeepsAccountNamesOfUserManagement(t *testing.T) {
	for _, in := range []string{
		"Created user: frank (operator)",
		"Updated user: erin",
		"Deleted user: frank",
		"Reset password for user: grace",
		"Renamed user frank -> frida",
	} {
		if got := RedactDetails(in); got != in {
			t.Errorf("RedactDetails(%q) = %q, want it as it is", in, got)
		}
	}
}
