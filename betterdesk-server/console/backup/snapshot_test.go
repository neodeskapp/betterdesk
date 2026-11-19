package backup

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/branding"
	"github.com/unitronix/betterdesk-server/console/devicegroups"
)

// Tests of the settings snapshot that need neither Node nor, unless a
// server is configured, PostgreSQL.

// snapshotService is the service of an install with the console's group
// service and no branding.
func (in *install) snapshotService(t *testing.T) *Service {
	t.Helper()
	s := in.service(t, "x")
	s.o.Groups = devicegroups.New(devicegroups.Options{DB: in.db})
	return s
}

func storedValue(t *testing.T, in *install, key string) string {
	t.Helper()
	var v sql.NullString
	if err := in.db.QueryRow(context.Background(), `SELECT value FROM settings WHERE key = ?`, key).Scan(&v); err != nil {
		t.Fatalf("setting %s: %v", key, err)
	}
	return v.String
}

func setValue(t *testing.T, in *install, key, value string) {
	t.Helper()
	mustExec(t, in.db, `DELETE FROM settings WHERE key = ?`, key)
	mustExec(t, in.db, `INSERT INTO settings (key, value) VALUES (?, ?)`, key, value)
}

func restoreSettings(t *testing.T, s *Service, snapshot string) RestoreResult {
	t.Helper()
	parsed, err := branding.ParseJSON([]byte(snapshot))
	if err != nil {
		t.Fatal(err)
	}
	return s.RestoreBackup(context.Background(), parsed, SnapshotOptions{Settings: true})
}

// EM2: the snapshot is a file people keep; the SMTP password stays in the
// database. A snapshot without one keeps the stored password; one that
// brings a password (made by an older console) restores it.
func TestTheSnapshotHasNoSMTPPassword(t *testing.T) {
	dialects(t, func(t *testing.T, dialect string) {
		in := newInstall(t, dialect)
		setValue(t, in, "smtp_config", `{"host":"mail.example.org","port":587,"secure":false,"user":"u","pass":"hunter2","from":"a@example.org"}`)
		s := in.snapshotService(t)
		snap, err := s.CreateBackup(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		text, err := SnapshotJSON(snap)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(text), "hunter2") {
			t.Fatal("the snapshot holds the SMTP password")
		}
		config := snap.Object("console").Object("settings").Str("smtp_config")
		if config != `{"host":"mail.example.org","port":587,"secure":false,"user":"u","from":"a@example.org"}` {
			t.Errorf("mail settings in the snapshot: %s", config)
		}

		setValue(t, in, "smtp_config", `{"host":"old.example.org","pass":"newer"}`)
		if res := restoreSettings(t, s, string(text)); len(res.Warnings) != 0 {
			t.Fatalf("restore: %+v", res)
		}
		if got := storedValue(t, in, "smtp_config"); got != `{"host":"mail.example.org","port":587,"secure":false,"user":"u","from":"a@example.org","pass":"newer"}` {
			t.Errorf("after the restore: %s", got)
		}

		older := `{"_format":"betterdesk-backup","_version":1,"console":{"settings":{"smtp_config":"{\"host\":\"h\",\"pass\":\"from-the-file\"}"}}}`
		restoreSettings(t, s, older)
		if got := storedValue(t, in, "smtp_config"); got != `{"host":"h","pass":"from-the-file"}` {
			t.Errorf("after an older snapshot: %s", got)
		}

		// nothing stored: the settings go in as they are
		mustExec(t, in.db, `DELETE FROM settings WHERE key = 'smtp_config'`)
		restoreSettings(t, s, string(text))
		if got := storedValue(t, in, "smtp_config"); got != `{"host":"mail.example.org","port":587,"secure":false,"user":"u","from":"a@example.org"}` {
			t.Errorf("restored without a stored password: %s", got)
		}
	})
}
