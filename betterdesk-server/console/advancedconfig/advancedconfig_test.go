package advancedconfig

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/unitronix/betterdesk-server/console/httpx"
)

func testService(t *testing.T, env map[string]string) (*Service, string) {
	t.Helper()
	root := t.TempDir()
	s := New(Options{
		ConsoleDir: filepath.Join(root, "web-nodejs"),
		DataDir:    filepath.Join(root, "data"),
		KeysPath:   filepath.Join(root, "keys"),
		Getenv:     func(k string) string { return env[k] },
		Platform:   "linux",
		Now:        func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
	})
	return s, root
}

func ids(files []File) string {
	var out []string
	for _, f := range files {
		out = append(out, f.ID)
	}
	return strings.Join(out, ",")
}

// The systemd units and the build settings are listed and editable on
// Linux only.
func TestPlatform(t *testing.T) {
	s, _ := testService(t, nil)
	if got := ids(s.ListFiles()); got != "console-env,console-env-local,console-session-secret,go-blocklist,go-audit-log,systemd-console,systemd-server,build-env,docker-supervisord,docker-compose" {
		t.Errorf("linux: %s", got)
	}
	s.opts.Platform = "win32"
	if got := ids(s.ListFiles()); got != "console-env,console-env-local,console-session-secret,go-blocklist,go-audit-log,docker-supervisord,docker-compose" {
		t.Errorf("windows: %s", got)
	}
	for _, id := range []string{"systemd-console", "systemd-server", "build-env"} {
		if _, err := s.ReadFile(id); !errors.Is(err, ErrUnknownFile) {
			t.Errorf("read %s on windows: %v", id, err)
		}
		if _, err := s.WriteFile(id, "x"); !errors.Is(err, ErrUnknownFile) {
			t.Errorf("write %s on windows: %v", id, err)
		}
		if err := s.RestoreBackup(id, "x", true); !errors.Is(err, ErrUnknownFile) {
			t.Errorf("restore %s on windows: %v", id, err)
		}
	}
}

func TestAuditLogPath(t *testing.T) {
	env := map[string]string{}
	s, root := testService(t, env)
	keysLog := filepath.Join(root, "keys", "audit.jsonl")
	if got := s.auditLogPath(); got != defaultAuditLog {
		t.Errorf("nothing there: %s", got)
	}
	if err := os.MkdirAll(filepath.Dir(keysLog), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keysLog, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(defaultAuditLog); err == nil {
		t.Skip("this machine has " + defaultAuditLog)
	}
	if got := s.auditLogPath(); got != keysLog {
		t.Errorf("in the keys folder: %s", got)
	}
	env["AUDIT_LOG_FILE"] = "relative/audit.jsonl"
	if got := s.auditLogPath(); got != keysLog {
		t.Errorf("relative AUDIT_LOG_FILE: %s", got)
	}
	env["AUDIT_LOG_FILE"] = "/elsewhere/audit.jsonl"
	if got := s.auditLogPath(); got != "/elsewhere/audit.jsonl" {
		t.Errorf("absolute AUDIT_LOG_FILE: %s", got)
	}
}

// What the process may not write is listed as such and refused, before
// anything is backed up or written. For a folder the folder above counts.
func TestNotWritable(t *testing.T) {
	s, root := testService(t, nil)
	env := filepath.Join(root, "web-nodejs", ".env")
	if err := os.MkdirAll(filepath.Dir(env), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(env, []byte("A=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "web-nodejs", ".env.local"), 0o755); err != nil {
		t.Fatal(err)
	}
	denied := map[string]bool{env: true, filepath.Join(root, "web-nodejs", ".env.local"): true}
	s.writable = func(p string) bool { return !denied[p] }
	files := s.ListFiles()
	if files[0].ID != "console-env" || files[0].Writable || !files[0].Exists {
		t.Errorf("file: %+v", files[0])
	}
	if files[1].ID != "console-env-local" || !files[1].Writable {
		t.Errorf("a folder in a writable folder: %+v", files[1])
	}
	if _, err := s.WriteFile("console-env", "B=2\n"); !errors.Is(err, ErrNotWritable) {
		t.Errorf("write: %v", err)
	}
	if content, _ := os.ReadFile(env); string(content) != "A=1\n" {
		t.Errorf("content %q", content)
	}
	if matches, _ := filepath.Glob(env + ".bak.*"); len(matches) != 0 {
		t.Errorf("backups %v", matches)
	}
	denied[filepath.Join(root, "web-nodejs")] = true
	if files := s.ListFiles(); files[1].Writable {
		t.Errorf("a folder in a folder that may not be written: %+v", files[1])
	}
}

func TestWriteKeepsTheModeOfAFileAndCreatesNewOnesPrivate(t *testing.T) {
	s, root := testService(t, nil)
	env := filepath.Join(root, "web-nodejs", ".env")
	if err := os.MkdirAll(filepath.Dir(env), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(env, []byte("A=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(env, 0o644); err != nil {
		t.Fatal(err)
	}
	written, err := s.WriteFile("console-env", "B=2\n")
	if err != nil || written.Created || written.BackupPath == nil || *written.BackupPath != env+".bak.2026-01-01T00-00-00-000Z" {
		t.Fatalf("write: %+v %v", written, err)
	}
	for path, mode := range map[string]os.FileMode{env: 0o644, *written.BackupPath: 0o644} {
		if info, err := os.Stat(path); err != nil || info.Mode().Perm() != mode {
			t.Errorf("%s: %v %v", path, info.Mode(), err)
		}
	}
	created, err := s.WriteFile("console-session-secret", "x")
	if err != nil || !created.Created || created.BackupPath != nil {
		t.Fatalf("create: %+v %v", created, err)
	}
	if info, _ := os.Stat(created.Path); info.Mode().Perm() != 0o600 {
		t.Errorf("new file mode %v", info.Mode())
	}
	if b, _ := json.Marshal(created); !strings.Contains(string(b), `"backupPath":null,"requiresRestart":"console","created":true}`) {
		t.Errorf("json %s", b)
	}
}

// A file the write created is removed by a restore with created; the
// settings routes never pass created (the old console did not).
func TestRestoreCreated(t *testing.T) {
	s, root := testService(t, nil)
	if _, err := s.WriteFile("go-blocklist", "x"); err != nil {
		t.Fatal(err)
	}
	if err := s.RestoreBackup("go-blocklist", nil, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "keys", "blocklist.txt")); !os.IsNotExist(err) {
		t.Errorf("still there: %v", err)
	}
	if err := s.RestoreBackup("go-blocklist", nil, false); !errors.Is(err, ErrInvalidBackup) {
		t.Errorf("without created: %v", err)
	}
}

func TestTextIsWrittenAsJSONStringify(t *testing.T) {
	b, err := httpx.Marshal(Text("a\b\f\n\r\t\x01\x1f\"\\<>&\u2028é"))
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `"a\b\f\n\r\t\u0001\u001f\"\\<>&`+"\u2028"+`é"` {
		t.Errorf("%s", b)
	}
}

func TestDecodeUTF8(t *testing.T) {
	for in, want := range map[string]string{
		"plain":                    "plain",
		"\xe2\x82A":                "\uFFFDA",
		"\xc0\xaf":                 "\uFFFD\uFFFD",
		"\xed\xa0\x80":             "\uFFFD\uFFFD\uFFFD",
		"\xf0\x9f\x98\x80\xff":     "😀\uFFFD",
		"\xf0\x9f\x98":             "\uFFFD",
		"\xe0\x80\x80":             "\uFFFD\uFFFD\uFFFD",
		"\xf4\x90\x80\x80":         "\uFFFD\uFFFD\uFFFD\uFFFD",
		"a\xc3":                    "a\uFFFD",
		"\xe2\x82\xac\xe2\x82\xac": "€€",
	} {
		if got := DecodeUTF8([]byte(in)); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}

func FuzzDecodeUTF8(f *testing.F) {
	for _, seed := range []string{"", "a", "\xe2\x82A", "\xf0\x9f\x98\x80\xff", "\xed\xa0\x80"} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, in []byte) {
		out := DecodeUTF8(in)
		if !utf8.ValidString(out) {
			t.Fatalf("%q gives invalid UTF-8 %q", in, out)
		}
		if utf8.Valid(in) && out != string(in) {
			t.Fatalf("valid %q changed to %q", in, out)
		}
		if n := utf8.RuneCountInString(out); n > len(in) {
			t.Fatalf("%q gives %d characters", in, n)
		}
	})
}

// FuzzRestoreBackup sends any backup path: only a backup of the same
// file, named as a clean absolute path that exists, is copied, and
// nothing but the target ever changes.
func FuzzRestoreBackup(f *testing.F) {
	for _, seed := range []string{
		"/web-nodejs/.env.bak.1", "/web-nodejs/.env.bak.1/../../outside", "/web-nodejs/.env.bak./../.env.bak.1",
		"/outside", "/web-nodejs//.env.bak.1", "/web-nodejs/.env.bak.1/", "web-nodejs/.env.bak.1", "",
		"/web-nodejs/.env.bak.link", "/web-nodejs/.env.bak.d/../../outside", "/web-nodejs/.env.bak.d/../.env.bak.1",
	} {
		f.Add(seed, true)
		f.Add(seed, false)
	}
	f.Fuzz(func(t *testing.T, suffix string, rooted bool) {
		s, root := testService(t, nil)
		env := filepath.Join(root, "web-nodejs", ".env")
		outside := filepath.Join(root, "outside")
		files := map[string]string{env: "current\n", env + ".bak.1": "backup\n", outside: "outside\n"}
		for path, content := range files {
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.Symlink(outside, env+".bak.link"); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(env+".bak.d", 0o755); err != nil {
			t.Fatal(err)
		}
		backup := suffix
		if rooted {
			backup = root + suffix
		}
		err := s.RestoreBackup("console-env", backup, false)
		if err == nil {
			if !strings.HasPrefix(backup, env+".bak.") || filepath.Clean(backup) != backup {
				t.Fatalf("restored from %q", backup)
			}
		}
		for path, content := range files {
			got, _ := os.ReadFile(path)
			if path == env {
				continue
			}
			if string(got) != content {
				t.Fatalf("%s changed to %q by %q", path, got, backup)
			}
		}
	})
}

// FuzzWriteRead writes any text and reads it back.
func FuzzWriteRead(f *testing.F) {
	for _, seed := range []string{"", "A=1\n", "nul\x00inside", "ünïcödé\u2028", "\xff\xfe"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, content string) {
		s, _ := testService(t, nil)
		written, err := s.WriteFile("go-blocklist", content)
		if len(content) > MaxBytes {
			if !errors.Is(err, ErrTooLarge) {
				t.Fatalf("large content: %v", err)
			}
			return
		}
		if err != nil || written.Size != int64(len(content)) {
			t.Fatalf("write: %+v %v", written, err)
		}
		read, err := s.ReadFile("go-blocklist")
		if strings.ContainsRune(content, 0) {
			if !errors.Is(err, ErrBinary) {
				t.Fatalf("NUL: %v", err)
			}
			return
		}
		if err != nil || string(read.Content) != DecodeUTF8([]byte(content)) {
			t.Fatalf("read: %+v %v", read, err)
		}
		b, err := json.Marshal(read.Content)
		var back string
		if err != nil || json.Unmarshal(b, &back) != nil || back != string(read.Content) {
			t.Fatalf("JSON %s %v", b, err)
		}
	})
}

// Node rounds the time of the last change to the nearest millisecond.
func TestMtimeRounds(t *testing.T) {
	s, root := testService(t, nil)
	path := filepath.Join(root, "keys", "blocklist.txt")
	if _, err := s.WriteFile("go-blocklist", "x"); err != nil {
		t.Fatal(err)
	}
	when := time.Date(2026, 1, 1, 0, 0, 0, 123600000, time.UTC)
	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatal(err)
	}
	read, err := s.ReadFile("go-blocklist")
	if err != nil || read.Mtime != "2026-01-01T00:00:00.124Z" {
		t.Errorf("read: %+v %v", read, err)
	}
	if files := s.ListFiles(); *files[3].Mtime != "2026-01-01T00:00:00.124Z" {
		t.Errorf("list: %s", *files[3].Mtime)
	}
}
