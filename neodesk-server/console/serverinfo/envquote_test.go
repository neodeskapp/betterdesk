package serverinfo

import (
	"strings"
	"testing"
)

// L86: values are read as dotenv files are read.
func TestEnvValueReadsQuotesAndComments(t *testing.T) {
	for text, want := range map[string]string{
		// Without quotes, as before: trimmed, "#" without a space before it kept
		"plain":       "plain",
		"  spaced  ":  "spaced",
		"a=b=c":       "a=b=c",
		"pa#ss":       "pa#ss",
		"#first":      "#first",
		"http://h/#x": "http://h/#x",
		"":            "",
		// A comment after a space or a tab
		"value # comment":  "value",
		"value\t# comment": "value",
		" # only comment":  "",
		"value #":          "value",
		// Single quotes: the text as written
		"'single'":           "single",
		"  'kept  inside'  ": "kept  inside",
		`'no \n escape'`:     `no \n escape`,
		"'a # b' # c":        "a # b",
		"'it''s'":            "'it''s'",
		// Double quotes: the usual escapes
		`"double"`:                "double",
		`"line\nbreak"`:           "line\nbreak",
		`"cr\rtab\t"`:             "cr\rtab\t",
		`"quote \" inside"`:       `quote " inside`,
		`"back\\slash"`:           `back\slash`,
		`"other \x escape"`:       `other \x escape`,
		`"a # b" # comment`:       "a # b",
		`"tight"#comment`:         "tight",
		`"  padded  "`:            "  padded  ",
		`"postgres://u:p#w@h/db"`: "postgres://u:p#w@h/db",
		`""`:                      "",
		// Quotes that do not make a quoted value: read as before
		`"unclosed`:                 `"unclosed`,
		`'unclosed`:                 `'unclosed`,
		`"a" b`:                     `"a" b`,
		`"a"b`:                      `"a"b`,
		`"trailing backslash\`:      `"trailing backslash\`,
		`mid"quote"`:                `mid"quote"`,
		"\u00a0'nbsp before quote'": "nbsp before quote",
	} {
		if got := EnvValue(text); got != want {
			t.Errorf("EnvValue(%q) = %q, want %q", text, got, want)
		}
	}
}

// L86: a value that needs quotes is written quoted, and a read gives the
// value back; a value that reads back as it is stays as it is.
func TestEnvQuoteReadsBack(t *testing.T) {
	for value, want := range map[string]string{
		"plain":                   "plain",
		"pa#ss":                   "pa#ss",
		"https://a.example:1/x?y": "https://a.example:1/x?y",
		"":                        "",
		"a # b":                   `"a # b"`,
		" lead":                   `" lead"`,
		"trail ":                  `"trail "`,
		`"quoted"`:                `"\"quoted\""`,
		"'single'":                `"'single'"`,
		"multi\nline":             `"multi\nline"`,
		`back\slash # x`:          `"back\\slash # x"`,
	} {
		got := EnvQuote(value)
		if got != want {
			t.Errorf("EnvQuote(%q) = %q, want %q", value, got, want)
		}
		if back := EnvValue(got); back != value {
			t.Errorf("EnvValue(EnvQuote(%q)) = %q", value, back)
		}
	}
}

func FuzzEnvQuote(f *testing.F) {
	for _, seed := range []string{"", "a", "a # b", "\"x\"", "'x'", " x ", "x\ny", `\`, "\"\\\"", "#", " #", "\u00a0x\u00a0", "\xff"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, value string) {
		written := EnvQuote(value)
		if strings.ContainsAny(written, "\r\n") {
			t.Fatalf("EnvQuote(%q) = %q spans lines", value, written)
		}
		if back := EnvValue(written); back != value {
			t.Fatalf("EnvValue(EnvQuote(%q)) = %q (written %q)", value, back, written)
		}
		if plainEnvValue(value) && written != value {
			t.Fatalf("EnvQuote(%q) = %q quotes a value that needs no quotes", value, written)
		}
	})
}
