package kit

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/auditlog"
	"github.com/neodeskapp/neodesk/neodesk-server/logging"
)

type logLine struct{ level, line string }

// capture makes l hand its lines to the returned list.
func captureLog(l *Logger) *[]logLine {
	var lines []logLine
	l.out = func(level, line string) { lines = append(lines, logLine{level, line}) }
	return &lines
}

// The first two tests are the cases of web-nodejs/tests/logger.test.js.

func TestLoggerProductionDefaultsToWarn(t *testing.T) {
	logger := NewLogger("", true)
	lines := captureLog(logger)
	if logger.Level() != "warn" {
		t.Errorf("level is %q, want warn", logger.Level())
	}
	logger.Info("hidden info")
	logger.Warn("visible warn")
	if len(*lines) != 1 || (*lines)[0] != (logLine{"warn", "visible warn"}) {
		t.Errorf("printed %v, want only the warn line", *lines)
	}
}

func TestLoggerChildRedactsQuotedUsernames(t *testing.T) {
	root := NewLogger("info", false)
	lines := captureLog(root)
	root.Child("AUTH").Info("Login failed: user 'administrator' not found")
	if len(*lines) != 1 {
		t.Fatalf("printed %d lines, want 1", len(*lines))
	}
	line := (*lines)[0].line
	if !strings.Contains(line, "a***r") || strings.Contains(line, "administrator") {
		t.Errorf("line is %q, want the name shortened to a***r", line)
	}
}

// The vectors are a fixed recording of the Node console's lib/logger.js
// (console/auditlog/testdata/logger.json).
type loggerVectors struct {
	Levels []struct {
		LogLevel     *string            `json:"log_level"`
		Production   bool               `json:"production"`
		NodeEnv      *string            `json:"node_env"`
		Level        string             `json:"level"`
		Printed      map[string]*string `json:"printed"`
		ChildPrinted int                `json:"child_printed"`
	} `json:"levels"`
	Parts []struct {
		In  string `json:"in"`
		Out string `json:"out"`
	} `json:"parts"`
	Lines []struct {
		Prefix *string `json:"prefix"`
		Level  string  `json:"level"`
		Args   []any   `json:"args"`
		Line   string  `json:"line"`
	} `json:"lines"`
}

func loadLoggerVectors(t *testing.T) loggerVectors {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "auditlog", "testdata", "logger.json"))
	if err != nil {
		t.Fatal(err)
	}
	var file loggerVectors
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	return file
}

var logLevelNames = []string{"error", "warn", "info", "debug"}

func TestLoggerLevelsAgainstNode(t *testing.T) {
	vectors := loadLoggerVectors(t).Levels
	if len(vectors) < 80 {
		t.Fatalf("%d vectors, want at least 80", len(vectors))
	}
	for _, v := range vectors {
		raw, name := "", "LOG_LEVEL unset"
		if v.LogLevel != nil {
			raw, name = *v.LogLevel, fmt.Sprintf("LOG_LEVEL=%q", *v.LogLevel)
		}
		name += fmt.Sprintf(" production=%v", v.Production)

		logger := NewLogger(raw, v.Production)
		if logger.Level() != v.Level {
			t.Errorf("%s: level %q, Node has %q", name, logger.Level(), v.Level)
		}
		for _, target := range []*Logger{logger, logger.Child("P")} {
			lines := captureLog(target)
			target.Error("m")
			target.Warn("m")
			target.Info("m")
			target.Debug("m")
			var got, want []string
			for _, line := range *lines {
				got = append(got, line.level)
			}
			for _, level := range logLevelNames {
				if v.Printed[level] != nil {
					want = append(want, level)
				}
			}
			if strings.Join(got, ",") != strings.Join(want, ",") || len(want) != v.ChildPrinted {
				t.Errorf("%s: prints %v, Node prints %v", name, got, want)
			}
		}
	}
}

func TestLoggerPartsAgainstNode(t *testing.T) {
	vectors := loadLoggerVectors(t).Parts
	if len(vectors) < 1000 {
		t.Fatalf("%d vectors, want at least 1000", len(vectors))
	}
	logger := NewLogger("debug", false)
	lines := captureLog(logger)
	changed := 0
	for _, v := range vectors {
		*lines = nil
		logger.Info(v.In)
		if len(*lines) != 1 {
			t.Fatalf("Info(%q) prints %q", v.In, *lines)
		}
		got := (*lines)[0].line
		// AL5: where the word before a quoted name holds the name, Node
		// shortened the word; the console shortens the quoted name and
		// prints the word as it is
		if kept := wordsBeforeShortenedNames(v.In); len(kept) > 0 {
			changed++
			for _, word := range kept {
				if !strings.Contains(got, word) {
					t.Errorf("Info(%q) prints %q without %q (Node prints %q)", v.In, got, word, v.Out)
				}
			}
			if got == v.Out {
				t.Errorf("Info(%q) prints %q as Node does", v.In, got)
			}
			continue
		}
		if got != v.Out {
			t.Errorf("Info(%q) prints %q, Node prints %q", v.In, got, v.Out)
		}
	}
	if changed == 0 {
		t.Error("no vector changes with AL5")
	}
}

// wordsBeforeShortenedNames lists, for each match of logNameAfterWord in a
// log text whose name shows up in the match before its quotes, the text
// from the word to the opening quote: what Node shortened in place of the
// name (BUGS.md AL5).
func wordsBeforeShortenedNames(text string) []string {
	s := auditlog.SanitizeLogValue(text)
	var words []string
	for _, m := range logNameAfterWord.FindAllStringSubmatchIndex(s, -1) {
		if strings.Index(s[m[0]:m[1]], s[m[2]:m[3]]) < m[2]-m[0] {
			words = append(words, s[m[0]:m[2]])
		}
	}
	return words
}

// TestLoggerMasksTheQuotedNameNotTheWordBeforeIt: the first pass shortens
// the quoted name between its quotes, also when the word before it holds
// the name (BUGS.md AL5).
func TestLoggerMasksTheQuotedNameNotTheWordBeforeIt(t *testing.T) {
	logger := NewLogger("debug", false)
	lines := captureLog(logger)
	for in, want := range map[string]string{
		"Login failed for 'or'":               "Login failed for '***'",
		"Login failed: user 'user' not found": "Login failed: user 'u***r' not found",
		"Login failed for ' '":                "Login failed for '(empty)'",
		"Login failed for 'alice'":            "Login failed for 'a***e'",
		"account 'count' and for 'fo'":        "account 'c***t' and for '***'",
	} {
		*lines = nil
		logger.Info(in)
		if len(*lines) != 1 || (*lines)[0].line != want {
			t.Errorf("Info(%q) prints %q, want %q", in, *lines, want)
		}
	}
}

// Node prints "[PREFIX] " before the line of a child logger; here the
// prefix is the component the line goes out under.
func TestLoggerLinesAgainstNode(t *testing.T) {
	vectors := loadLoggerVectors(t).Lines
	if len(vectors) < 15 {
		t.Fatalf("%d vectors, want at least 15", len(vectors))
	}
	for _, v := range vectors {
		logger := NewLogger("debug", false)
		want, component := v.Line, "console"
		if v.Prefix != nil && *v.Prefix != "" {
			logger = logger.Child(*v.Prefix)
			component = *v.Prefix
			want = strings.TrimPrefix(strings.TrimPrefix(want, "["+*v.Prefix+"]"), " ")
		} else if v.Prefix != nil {
			logger = logger.Child("")
		}
		lines := captureLog(logger)
		switch v.Level {
		case "error":
			logger.Error(v.Args...)
		case "warn":
			logger.Warn(v.Args...)
		case "info":
			logger.Info(v.Args...)
		case "debug":
			logger.Debug(v.Args...)
		}
		if len(*lines) != 1 || (*lines)[0] != (logLine{v.Level, want}) {
			t.Errorf("%s%v prints %q, Node prints %q", v.Level, v.Args, *lines, want)
		}
		if logger.component() != component {
			t.Errorf("prefix %v: component %q, want %q", v.Prefix, logger.component(), component)
		}
	}
}

func TestLoggerLevels(t *testing.T) {
	cases := []struct {
		raw        string
		production bool
		want       string
	}{
		{"", false, "info"},
		{"", true, "warn"},
		{"error", false, "error"},
		{"warn", false, "warn"},
		{"info", true, "info"},
		{"debug", true, "debug"},
		{" DEBUG\n", true, "debug"},
		{"\ufeffError\u00a0", false, "error"},
		{"warning", false, "info"},
		{"verbose", true, "warn"},
		// Only ASCII letters change case: U+0130 is no "i".
		{"\u0130NFO", true, "warn"},
		{"\u0085debug", false, "info"},
	}
	for _, c := range cases {
		if got := NewLogger(c.raw, c.production).Level(); got != c.want {
			t.Errorf("NewLogger(%q, %v).Level() = %q, want %q", c.raw, c.production, got, c.want)
		}
	}

	printed := map[string]string{"error": "error", "warn": "error,warn", "info": "error,warn,info", "debug": "error,warn,info,debug"}
	for level, want := range printed {
		logger := NewLogger(level, false)
		lines := captureLog(logger)
		logger.Debug("m")
		logger.Info("m")
		logger.Warn("m")
		logger.Error("m")
		got := map[string]bool{}
		for _, line := range *lines {
			got[line.level] = true
		}
		var names []string
		for _, name := range logLevelNames {
			if got[name] {
				names = append(names, name)
			}
		}
		if strings.Join(names, ",") != want || len(*lines) != len(names) {
			t.Errorf("level %s prints %v, want %s", level, *lines, want)
		}
	}
}

func TestLoggerArguments(t *testing.T) {
	cases := []struct {
		name string
		args []any
		want string
	}{
		{"strings joined with a space", []any{"a", "b", "", "c"}, "a b  c"},
		{"no arguments", nil, ""},
		{"line breaks", []any{"one\r\ntwo\u2028three"}, `one\n\ntwo\nthree`},
		{"name after a word", []any{"Login successful for 'alice'"}, "Login successful for 'a***e'"},
		{"name alone", []any{"'alice' signed in"}, "'a***e' signed in"},
		{"short name", []any{"user 'al' and 'x'"}, "user '***' and 'x'"},
		{"the quoted name, not the word before it", []any{"for 'or'"}, "for '***'"},
		{"name with spaces around", []any{"for ' bob '"}, "for 'b***b'"},
		{"role and provider words stay", []any{"role 'Admin' provider 'local' hash 'bcrypt' and 'PBKDF2-SHA256'"}, "role 'Admin' provider 'l***l' hash 'bcrypt' and 'PBKDF2-SHA256'"},
		{"paths, addresses and settings stay", []any{"file '/etc/x' host 'a.b' at '10:30' env 'NEODESK_API_KEY' kind 'hash type'"}, "file '/etc/x' host 'a.b' at '10:30' env 'NEODESK_API_KEY' kind 'hash type'"},
		{"64 code units", []any{"'" + strings.Repeat("a", 64) + "' '" + strings.Repeat("b", 65) + "'"}, "'a***a' '" + strings.Repeat("b", 65) + "'"},
		{"an emoji is two code units", []any{"'\U0001F600'ab'cd'"}, "'\U0001F600'ab'***'"},
		{"the closing quote opens nothing", []any{"'alice'bob'carol'"}, "'a***e'bob'c***l'"},
		{"only ASCII letters fold", []any{"u\u017fer 'alice bob' '\u212a\u212a'"}, "u\u017fer 'alice bob' '\u212a\u212a'"},
		{"an error prints its message", []any{"sync failed:", errors.New("no row for 'alice'\nnext")}, `sync failed: no row for 'a***e'\nnext`},
		{"other values", []any{"user id", 12, ":", true, 1.5, nil}, "user id 12 : true 1.5 <nil>"},
		{"other values keep one line", []any{[]string{"a\nb"}}, `[a\nb]`},
		{"a percent sign is no format", []any{"100%s done", "x", "%d"}, "100%s done x %d"},
	}
	for _, c := range cases {
		logger := NewLogger("info", false)
		lines := captureLog(logger)
		logger.Info(c.args...)
		if len(*lines) != 1 || (*lines)[0].line != c.want {
			t.Errorf("%s: Info(%q) prints %q, want %q", c.name, c.args, *lines, c.want)
		}
	}
}

// Without the hook the lines go through the logging package, which reads
// the level of a line from its first word.
func TestLoggerWritesThroughLogging(t *testing.T) {
	var buf bytes.Buffer
	writer, flags := log.Writer(), log.Flags()
	log.SetOutput(logging.NewJSONWriter(&buf))
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(writer)
		log.SetFlags(flags)
	})

	root := NewLogger("debug", false)
	root.Error("database is gone")
	root.Warn("slow answer:", 1200, "ms")
	root.Info("listening for 'alice'")
	root.Debug("cache hit")
	root.Child("AUTH").Warn("lockout")
	root.Child("AUTH").Info("100% done")
	NewLogger("warn", false).Info("dropped")

	type entry struct {
		Level     string `json:"level"`
		Component string `json:"component"`
		Message   string `json:"message"`
	}
	var got []entry
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		var e entry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("line %q: %v", line, err)
		}
		got = append(got, e)
	}
	want := []entry{
		{"error", "console", "ERROR: database is gone"},
		{"warn", "console", "WARN: slow answer: 1200 ms"},
		{"info", "console", "listening for 'a***e'"},
		{"debug", "console", "DEBUG: cache hit"},
		{"warn", "AUTH", "WARN: lockout"},
		{"info", "AUTH", "100% done"},
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("logged\n%v\nwant\n%v", got, want)
	}
}
