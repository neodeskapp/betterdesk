package scheduledbackup

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"syscall"
	"testing"

	"github.com/unitronix/betterdesk-server/console/branding"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// The vectors are what services/scheduledBackupService.js answered
// (console/backup/testdata/nodeoracle/vectors.js).

type jsErr struct {
	Name    string `json:"name"`
	Message string `json:"message"`
}

type scheduleVectors struct {
	DataDir string `json:"dataDir"`
	AppDir  string `json:"appDir"`
	Save    []struct {
		Body   json.RawMessage `json:"body"`
		Result json.RawMessage `json:"result"`
		Error  *jsErr          `json:"error"`
		Stored *string         `json:"stored"`
	} `json:"save"`
	Get []struct {
		Stored *string         `json:"stored"`
		Result json.RawMessage `json:"result"`
		Error  *jsErr          `json:"error"`
	} `json:"get"`
	Destination []struct {
		Body   json.RawMessage `json:"body"`
		Result *string         `json:"result"`
		Error  *jsErr          `json:"error"`
	} `json:"destination"`
	SMBSave []struct {
		Body   json.RawMessage `json:"body"`
		Before *string         `json:"before"`
		Result json.RawMessage `json:"result"`
		Error  *jsErr          `json:"error"`
		Stored *string         `json:"stored"`
	} `json:"smbSave"`
	SMBGet []struct {
		Stored *string         `json:"stored"`
		Result json.RawMessage `json:"result"`
	} `json:"smbGet"`
	SMBTest []struct {
		Platform string          `json:"platform"`
		Body     json.RawMessage `json:"body"`
		Plan     struct {
			Code      int    `json:"code"`
			ErrorCode string `json:"errorCode"`
		} `json:"plan"`
		Calls  []json.RawMessage `json:"calls"`
		Result json.RawMessage   `json:"result"`
		Error  *jsErr            `json:"error"`
	} `json:"smbTest"`
}

func loadSchedule(t *testing.T) scheduleVectors {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", "schedule.json.gz"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	var v scheduleVectors
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func parse(t *testing.T, raw []byte) any {
	t.Helper()
	v, err := branding.ParseJSON(raw)
	if err != nil {
		t.Fatalf("parse %s: %v", raw, err)
	}
	return v
}

// canon is JSON text as JavaScript writes it.
func canon(t *testing.T, raw []byte) string {
	t.Helper()
	return branding.Stringify(parse(t, raw))
}

var loneSurrogate = regexp.MustCompile(`\\ud[89a-f][0-9a-f]{2}`)

func putSetting(t *testing.T, db *store.DB, key string, value *string) {
	t.Helper()
	ctx := context.Background()
	if _, err := db.Exec(ctx, `DELETE FROM settings WHERE key = ?`, key); err != nil {
		t.Fatal(err)
	}
	if value != nil {
		if _, err := db.Exec(ctx, `INSERT INTO settings (key, value) VALUES (?, ?)`, key, *value); err != nil {
			t.Fatal(err)
		}
	}
}

func storedSetting(t *testing.T, s *Service, key string) *string {
	t.Helper()
	v, err := s.getSetting(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	if !v.Valid {
		return nil
	}
	return &v.String
}

func same(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// hide replaces the passwords in JSON text, so a failure never prints one.
var passwordField = regexp.MustCompile(`"password":\s*"(?:[^"\\]|\\.)*"`)

func hide(s string) string { return passwordField.ReplaceAllString(s, `"password":"<hidden>"`) }

func show(p *string) string {
	if p == nil {
		return "<none>"
	}
	return *p
}

func TestScheduleMatchesNode(t *testing.T) {
	v := loadSchedule(t)
	if len(v.Save) < 1000 || len(v.Get) < 300 || len(v.Destination) < 1000 {
		t.Fatal("too few vectors")
	}
	db := storetest.Migrated(t)
	s := New(Options{Store: db, DataDir: v.DataDir, AppDir: v.AppDir})
	ctx := context.Background()
	for i, c := range v.Save {
		putSetting(t, db, ConfigKey, nil)
		got, err := s.SaveConfig(ctx, parse(t, c.Body))
		stored := storedSetting(t, s, ConfigKey)
		if c.Error != nil {
			if err == nil || err.Error() != c.Error.Message {
				t.Errorf("save %d %s: want error %q, got %v", i, c.Body, c.Error.Message, err)
			}
		} else if err != nil || ConfigJSON(got) != canon(t, c.Result) {
			t.Errorf("save %d %s: got %s %v, want %s", i, c.Body, ConfigJSON(got), err, c.Result)
		}
		if !same(stored, c.Stored) {
			t.Errorf("save %d %s: stored %s, want %s", i, c.Body, show(stored), show(c.Stored))
		}
	}
	for i, c := range v.Get {
		putSetting(t, db, ConfigKey, c.Stored)
		got, err := s.GetConfig(ctx)
		if c.Error != nil {
			if err == nil || err.Error() != c.Error.Message {
				t.Errorf("get %d %s: want error %q, got %v", i, show(c.Stored), c.Error.Message, err)
			}
			continue
		}
		if err != nil || ConfigJSON(got) != canon(t, c.Result) {
			t.Errorf("get %d %s: got %s %v, want %s", i, show(c.Stored), ConfigJSON(got), err, c.Result)
		}
	}
	for i, c := range v.Destination {
		cfg, err := Normalize(parse(t, c.Body))
		var got string
		if err == nil {
			got, err = s.ResolveDestination(cfg)
		}
		if c.Error != nil {
			if err == nil || err.Error() != c.Error.Message {
				t.Errorf("destination %d %s: want error %q, got %q %v", i, c.Body, c.Error.Message, got, err)
			}
			continue
		}
		if err != nil || got != *c.Result {
			t.Errorf("destination %d %s: got %q %v, want %q", i, c.Body, got, err, *c.Result)
		}
	}
}

func TestSMBSettingsMatchNode(t *testing.T) {
	v := loadSchedule(t)
	if len(v.SMBSave) < 1000 || len(v.SMBTest) < 600 {
		t.Fatal("too few vectors")
	}
	if got := hide(`{"user":"u","password": "a\"b\\","x":1}`); got != `{"user":"u","password":"<hidden>","x":1}` {
		t.Fatalf("hide: %s", got)
	}
	db := storetest.Migrated(t)
	s := New(Options{Store: db, DataDir: v.DataDir, AppDir: v.AppDir})
	ctx := context.Background()
	for i, c := range v.SMBSave {
		putSetting(t, db, SMBKey, c.Before)
		got, err := s.SaveSMB(ctx, parse(t, c.Body))
		stored := storedSetting(t, s, SMBKey)
		if c.Error != nil {
			if err == nil || err.Error() != c.Error.Message {
				t.Errorf("smb save %d %s: want error %q, got %v", i, hide(string(c.Body)), c.Error.Message, err)
			}
		} else if err != nil || got != canon(t, c.Result) {
			t.Errorf("smb save %d %s: got %s %v, want %s", i, hide(string(c.Body)), hide(got), err, hide(string(c.Result)))
		}
		if !same(stored, c.Stored) {
			t.Errorf("smb save %d %s: stored %s, want %s", i, hide(string(c.Body)), hide(show(stored)), hide(show(c.Stored)))
		}
	}
	for i, c := range v.SMBGet {
		putSetting(t, db, SMBKey, c.Stored)
		got, err := s.GetSMB(ctx)
		if err != nil || got != canon(t, c.Result) {
			t.Errorf("smb get %d %s: got %s %v, want %s", i, hide(show(c.Stored)), hide(got), err, hide(string(c.Result)))
		}
	}
	for i, c := range v.SMBTest {
		windows := c.Platform == "win32"
		var calls []string
		plan := c.Plan
		run := func(_ context.Context, name string, args ...string) (int, error) {
			list := make([]any, len(args))
			for k, a := range args {
				list[k] = a
			}
			calls = append(calls, branding.Stringify([]any{name, list}))
			switch plan.ErrorCode {
			case "ENOENT":
				return 0, &exec.Error{Name: name, Err: exec.ErrNotFound}
			case "EACCES":
				return 0, &fs.PathError{Op: "fork/exec", Path: name, Err: syscall.EACCES}
			}
			return plan.Code, nil
		}
		ts := New(Options{Store: db, Run: run, Windows: &windows})
		target, err := ts.TestSMBConnection(ctx, parse(t, c.Body))
		if c.Error != nil {
			if err == nil || err.Error() != c.Error.Message {
				t.Errorf("smb test %d %s %s: want error %q, got %v", i, c.Platform, hide(string(c.Body)), c.Error.Message, err)
			}
		} else if err != nil || `{"valid":true,"target":`+branding.Stringify(target)+`}` != canon(t, c.Result) {
			t.Errorf("smb test %d %s %s: got %q %v, want %s", i, c.Platform, hide(string(c.Body)), target, err, hide(string(c.Result)))
		}
		if len(calls) != len(c.Calls) {
			t.Errorf("smb test %d: %d calls, want %d", i, len(calls), len(c.Calls))
			continue
		}
		for k := range calls {
			// spawn hands a lone surrogate to the program as U+FFFD
			if calls[k] != loneSurrogate.ReplaceAllString(canon(t, c.Calls[k]), "\ufffd") {
				t.Errorf("smb test %d call %d differs (%d bytes, want %d); the arguments hold the password", i, k, len(calls[k]), len(c.Calls[k]))
			}
		}
	}
}
