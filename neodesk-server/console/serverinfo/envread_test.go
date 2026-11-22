package serverinfo

import (
	"strings"
	"testing"
)

// L86: what UpsertEnvKey writes, both readers read back.
func TestUpsertEnvKeyReadsBackThroughBothReaders(t *testing.T) {
	for _, value := range []string{"a # b", " lead", `"quoted"`, "multi\nline", "plain", `C:\path with space\`} {
		out := UpsertEnvKey("A=1\nKEY=old\n", "KEY", value)
		if got := ParseEnvFile(out)["KEY"]; got != value {
			t.Errorf("UpsertEnvKey %q: ParseEnvFile reads %q from %q", value, got, out)
		}
		if got, _ := ParseEnvLines(out).Get("KEY"); got != value {
			t.Errorf("UpsertEnvKey %q: ParseEnvLines reads %q from %q", value, got, out)
		}
		if got := ParseEnvFile(out)["A"]; got != "1" {
			t.Errorf("UpsertEnvKey %q: the other key reads %q", value, got)
		}
	}
}

// L86: a placeholder that is the whole value of a line gets its value
// quoted where it needs quotes; elsewhere it goes in as it is.
func TestApplySubstitutionsQuotesWholeValues(t *testing.T) {
	text := "# __PASS__ in a comment\nDEFAULT_ADMIN_PASSWORD=__PASS__\r\nDIR=__DIR__/sub\nPLAIN=__DIR__\n"
	got := ApplySubstitutions(text, []Substitution{{"PASS", "pa ss # 1"}, {"DIR", "/data"}})
	want := "# pa ss # 1 in a comment\nDEFAULT_ADMIN_PASSWORD=\"pa ss # 1\"\r\nDIR=/data/sub\nPLAIN=/data\n"
	if got != want {
		t.Fatalf("ApplySubstitutions = %q, want %q", got, want)
	}
	if value, _ := ParseEnvLines(got).Get("DEFAULT_ADMIN_PASSWORD"); value != "pa ss # 1" {
		t.Fatalf("the password reads %q", value)
	}
}

// L86: MergeEnv copies the template's lines as written, quotes included.
func TestMergeEnvKeepsTheTemplatesQuotes(t *testing.T) {
	content, added := MergeEnv("A=1\n", "A=2\nB=\"x # y\" # note\n")
	if content != "A=1\nB=\"x # y\" # note\n" || len(added) != 1 {
		t.Fatalf("MergeEnv = %q, %q", content, added)
	}
	if value, _ := ParseEnvLines(content).Get("B"); value != "x # y" {
		t.Fatalf("B reads %q", value)
	}
}

// plainEnvValue reports whether value reads back as it is written without
// quotes, by the rule on its own: no white space at either end, no quote
// at the start, no comment after a space or a tab and no line break.
func plainEnvValue(value string) bool {
	if strings.TrimFunc(value, fuzzIsSpace) != value || strings.ContainsAny(value, "\r\n") ||
		strings.Contains(value, " #") || strings.Contains(value, "\t#") {
		return false
	}
	return value == "" || (value[0] != '"' && value[0] != '\'')
}

// plainEnvText reports whether every line of text reads as before L86:
// the text after each "=" is a plain value (plainEnvValue).
func plainEnvText(text string) bool {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if eq := strings.IndexByte(line, '='); eq >= 0 && !plainEnvValue(line[eq+1:]) {
			return false
		}
	}
	return true
}

// dotenvAffects reports whether the text after a "=" reads otherwise than
// before L86, when the value was the text trimmed: it has a comment after
// a space or a tab, or a quote at its start.
func dotenvAffects(raw string) bool {
	return strings.Contains(raw, " #") || strings.Contains(raw, "\t#") || !plainEnvValue(strings.TrimFunc(raw, fuzzIsSpace))
}

// dotenvText reports whether a line of text has a value dotenvAffects.
func dotenvText(text string) bool {
	for _, line := range strings.Split(text, "\n") {
		if eq := strings.IndexByte(line, '='); eq >= 0 && dotenvAffects(strings.TrimSuffix(line[eq+1:], "\r")) {
			return true
		}
	}
	return false
}
