package backup

import (
	"context"
	"database/sql"
	"slices"
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

func addressBooks(t *testing.T, in *install) string {
	t.Helper()
	rows, err := readStrings(context.Background(), in.db, `SELECT username || '|' || ab_type || '|' || data FROM address_books ORDER BY username, ab_type`)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Join(rows, "\n")
}

// BK1: a snapshot's address books come back with their type and data in
// place, also from a snapshot made on PostgreSQL, whose rows name the
// account instead of its id.
func TestSnapshotRestoresAddressBooks(t *testing.T) {
	dialects(t, func(t *testing.T, dialect string) {
		ctx := context.Background()
		in := newInstall(t, dialect)
		in.seed(t)
		s := in.snapshotService(t)
		snapshot := parsedJSON(t, `{"_format":"betterdesk-backup","_version":1,"console":{"addressBooks":[`+
			`{"user_id":50,"ab_type":"legacy","data":"{\"tags\":[\"a\"]}"},`+
			`{"username":"second","ab_type":"personal","data":"{\"peers\":[]}"},`+
			`{"username":"nobody","ab_type":"legacy","data":"{}"},`+
			`{"user_id":51,"ab_type":"shared"}]}}`)
		if res := s.RestoreBackup(ctx, snapshot, SnapshotOptions{AddressBooks: true}); len(res.Warnings) != 0 || !slices.Contains(res.Restored, "addressBooks") {
			t.Fatalf("restore: %+v", res)
		}
		want := "second|personal|{\"peers\":[]}\nsecond|shared|{}\nseed_admin|legacy|{\"tags\":[\"a\"]}"
		if got := addressBooks(t, in); got != want {
			t.Errorf("address books:\n%s\nwant\n%s", got, want)
		}

		// a snapshot of this install restored into another one
		made, err := s.CreateBackup(ctx)
		if err != nil {
			t.Fatal(err)
		}
		text, err := SnapshotJSON(made)
		if err != nil {
			t.Fatal(err)
		}
		other := newInstall(t, dialect)
		other.seed(t)
		if res := other.snapshotService(t).RestoreBackup(ctx, parsedJSON(t, string(text)), SnapshotOptions{AddressBooks: true}); len(res.Warnings) != 0 {
			t.Fatalf("restore of the made snapshot: %+v", res)
		}
		if got := addressBooks(t, other); got != want {
			t.Errorf("address books from the made snapshot:\n%s\nwant\n%s", got, want)
		}
	})
}

// BK11: a restored rule's permissions are stored as JSON text once, so
// they read back as the object they were, whether the snapshot holds
// them as an object or as text.
func TestSnapshotRestoresRulePermissions(t *testing.T) {
	dialects(t, func(t *testing.T, dialect string) {
		ctx := context.Background()
		in := newInstall(t, dialect)
		s := in.snapshotService(t)
		snapshot := parsedJSON(t, `{"_format":"betterdesk-backup","_version":1,"console":{"strategies":[`+
			`{"guid":"s1","name":"text","permissions":"{\"x\":1}"},`+
			`{"guid":"s2","name":"object","permissions":{"y":true,"z":[1]}},`+
			`{"guid":"s3","name":"empty text","permissions":""},`+
			`{"guid":"s4","name":"none"}]}}`)
		if res := s.RestoreBackup(ctx, snapshot, SnapshotOptions{Groups: true}); len(res.Warnings) != 0 {
			t.Fatalf("restore: %+v", res)
		}
		rules, err := allStrategies(ctx, in.db)
		if err != nil {
			t.Fatal(err)
		}
		got := map[string]string{}
		for _, r := range rules {
			got[r.Str("guid")] = branding.Stringify(r.Get("permissions"))
		}
		for guid, want := range map[string]string{"s1": `{"x":1}`, "s2": `{"y":true,"z":[1]}`, "s3": `{}`, "s4": `{}`} {
			if got[guid] != want {
				t.Errorf("%s: permissions %s, want %s", guid, got[guid], want)
			}
		}
	})
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
