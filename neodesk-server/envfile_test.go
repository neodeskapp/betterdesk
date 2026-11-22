package main

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// fakeEnv is an environment for loadEnvFile.
type fakeEnv map[string]string

func (e fakeEnv) get(k string) string { return e[k] }
func (e fakeEnv) set(k, v string) error {
	e[k] = v
	return nil
}

func TestEnvFileFillsWhatIsNotSet(t *testing.T) {
	file := filepath.Join(t.TempDir(), ".env")
	text := "# comment\r\n" +
		"  PORT=5000  \r\n" +
		"CONSOLE_PORT=5000\n" +
		"HOST=127.0.0.1\n" +
		"EMPTY_BEFORE=filled\n" +
		"KEPT=from-file\n" +
		"=no-key\n" +
		"  # indented comment=x\n" +
		"no equals sign\n" +
		" SPACED KEY = spaced value \n" +
		"DUP=first\n" +
		"DUP=second\n" +
		"LATER_EMPTY=\n" +
		"LATER_EMPTY=second\n" +
		"WITH_EQUALS=a=b=c\n" +
		"QUOTED=\"kept as written\"\n" +
		"LAST=no newline"
	if err := os.WriteFile(file, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	env := fakeEnv{"BETTERDESK_ENV_FILE": file, "KEPT": "from-service", "EMPTY_BEFORE": ""}
	if err := loadEnvFile(env.get, env.set, os.ReadFile); err != nil {
		t.Fatal(err)
	}
	want := fakeEnv{
		"BETTERDESK_ENV_FILE": file,
		"PORT":                "5000",
		"CONSOLE_PORT":        "5000",
		"HOST":                "127.0.0.1",
		"EMPTY_BEFORE":        "filled",
		"KEPT":                "from-service",
		"SPACED KEY":          "spaced value",
		"DUP":                 "first",
		"LATER_EMPTY":         "second",
		"WITH_EQUALS":         "a=b=c",
		"QUOTED":              "kept as written",
		"LAST":                "no newline",
	}
	if !reflect.DeepEqual(env, want) {
		t.Errorf("environment\n got %v\nwant %v", env, want)
	}
}

func TestEnvFileOnlyWhenNamed(t *testing.T) {
	env := fakeEnv{}
	read := func(string) ([]byte, error) {
		t.Error("a file was read without BETTERDESK_ENV_FILE")
		return nil, nil
	}
	if err := loadEnvFile(env.get, env.set, read); err != nil || len(env) != 0 {
		t.Errorf("err %v, env %v", err, env)
	}
}

func TestEnvFileThatCannotBeRead(t *testing.T) {
	env := fakeEnv{"BETTERDESK_ENV_FILE": filepath.Join(t.TempDir(), "missing")}
	err := loadEnvFile(env.get, env.set, os.ReadFile)
	if !errors.Is(err, os.ErrNotExist) || len(env) != 1 {
		t.Errorf("err %v, env %v", err, env)
	}
}

// L86: the env file takes quoted values and comments as dotenv files do;
// lines without quotes read as before.
func TestEnvFileReadsQuotesAndComments(t *testing.T) {
	file := filepath.Join(t.TempDir(), ".env")
	text := "DOUBLE=\"kept as written\"\n" +
		"SINGLE='single # not a comment'\n" +
		`ESCAPES="a\"b\\c\nd"` + "\n" +
		"COMMENTED=value # the comment\n" +
		"QUOTED_COMMENTED=\"x # y\" # the comment\n" +
		"PADDED=\"  inside  \"\n" +
		"HASH=pa#ss\n" +
		"PLAIN=  as before  \n" +
		"UNCLOSED=\"open\n"
	if err := os.WriteFile(file, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	env := fakeEnv{"BETTERDESK_ENV_FILE": file}
	if err := loadEnvFile(env.get, env.set, os.ReadFile); err != nil {
		t.Fatal(err)
	}
	want := fakeEnv{
		"BETTERDESK_ENV_FILE": file,
		"DOUBLE":              "kept as written",
		"SINGLE":              "single # not a comment",
		"ESCAPES":             "a\"b\\c\nd",
		"COMMENTED":           "value",
		"QUOTED_COMMENTED":    "x # y",
		"PADDED":              "  inside  ",
		"HASH":                "pa#ss",
		"PLAIN":               "as before",
		"UNCLOSED":            "\"open",
	}
	if !reflect.DeepEqual(env, want) {
		t.Errorf("environment\n got %q\nwant %q", env, want)
	}
}
