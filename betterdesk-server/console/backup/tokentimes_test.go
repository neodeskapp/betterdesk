package backup

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/console/store"
)

// account is a row of users for the tests of the sign-in token times.
type account struct {
	name, hash string
	totp       bool
	secret     string
	changedAt  any // credentials_changed_at, nil for NULL
}

func addAccounts(t *testing.T, db *store.DB, accounts ...account) {
	t.Helper()
	for i, a := range accounts {
		var totp any = a.totp
		if !db.IsPostgres() {
			totp = 0
			if a.totp {
				totp = 1
			}
		}
		mustExec(t, db, `INSERT INTO users (id, username, password_hash, role, totp_secret, totp_enabled, credentials_changed_at) VALUES (?, ?, ?, 'operator', ?, ?, ?)`,
			100+i, a.name, a.hash, a.secret, totp, a.changedAt)
	}
}

// tokenTimes lists "name=credentials_changed_at" of every account.
func tokenTimes(t *testing.T, db *store.DB) string {
	t.Helper()
	rows, err := readStrings(context.Background(), db, `SELECT username || '=' || COALESCE(CAST(credentials_changed_at AS TEXT), 'NULL') FROM users ORDER BY username`)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Join(rows, " ")
}

// The accounts of the archive and of the install it is restored into.
var (
	archivedAccounts = []account{
		{name: "alice", hash: "alice-1", changedAt: 100},
		{name: "bob", hash: "bob-1", changedAt: 1000},
		{name: "carol", hash: "carol-1"},
		{name: "dave", hash: "dave-1", changedAt: 50},
		{name: "frank", hash: "frank-1", changedAt: 70},
	}
	liveAccounts = []account{
		{name: "alice", hash: "alice-2", changedAt: 2000},
		{name: "bob", hash: "bob-1", changedAt: 500},
		{name: "carol", hash: "carol-1", changedAt: 3000},
		{name: "erin", hash: "erin-1", changedAt: 10},
		{name: "frank", hash: "frank-1", totp: true, secret: "FRANKSECRET", changedAt: 80},
	}
)

// wantTokenTimes is what a restore at now leaves: alice's password, dave
// (not there before) and frank's second factor are replaced and get now;
// bob and carol keep their credentials and the later of their two times.
func wantTokenTimes(now int64) string {
	n := strconv.FormatInt(now, 10)
	return "alice=" + n + " bob=1000 carol=3000 dave=" + n + " frank=" + n
}

// archiveOfAccounts makes a full backup of an install with the archived
// accounts.
func archiveOfAccounts(t *testing.T, dialect string) []byte {
	t.Helper()
	src := newInstall(t, dialect)
	addAccounts(t, src.db, archivedAccounts...)
	if dialect == "sqlite" {
		mustExec(t, src.db, `PRAGMA wal_checkpoint(TRUNCATE)`)
	}
	archive, _, err := src.service(t, "x").CreateFullBackup(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return archive
}

// RV181: a full restore ends the sign-in tokens of every account whose
// password or second factor it replaced, and of every account the
// database did not have, with the time of the restore; an account it
// leaves its credentials keeps the later of its time in the archive and
// before. The restore's writes do not fire the trigger.
func TestRestoreEndsTheTokensOfReplacedCredentials(t *testing.T) {
	dialects(t, func(t *testing.T, dialect string) {
		archive := archiveOfAccounts(t, dialect)
		dst := newInstall(t, dialect)
		addAccounts(t, dst.db, liveAccounts...)
		svc := dst.service(t, "x")
		svc.o.Now = func() time.Time { return fixtureRestoreTime }
		res, err := svc.RestoreFullBackup(context.Background(), archive, RestoreOptions{Database: true})
		if err != nil || len(res.Warnings) != 0 {
			t.Fatalf("restore: %+v %v", res, err)
		}
		if got, want := tokenTimes(t, dst.db), wantTokenTimes(fixtureRestoreTime.Unix()); got != want {
			t.Errorf("token times %s, want %s", got, want)
		}
	})
}

// RV181: the accounts of a settings snapshot, restored when asked, end
// the same tokens.
func TestSnapshotRestoreEndsTheTokensOfReplacedCredentials(t *testing.T) {
	dialects(t, func(t *testing.T, dialect string) {
		ctx := context.Background()
		src := newInstall(t, dialect)
		addAccounts(t, src.db, archivedAccounts...)
		made, err := src.snapshotService(t).CreateBackup(ctx)
		if err != nil {
			t.Fatal(err)
		}
		text, err := SnapshotJSON(made)
		if err != nil {
			t.Fatal(err)
		}
		dst := newInstall(t, dialect)
		addAccounts(t, dst.db, liveAccounts...)
		svc := dst.snapshotService(t)
		svc.o.Now = func() time.Time { return fixtureRestoreTime }
		if res := svc.RestoreBackup(ctx, parsedJSON(t, string(text)), SnapshotOptions{Users: true}); len(res.Warnings) != 0 {
			t.Fatalf("restore: %+v", res)
		}
		if got, want := tokenTimes(t, dst.db), wantTokenTimes(fixtureRestoreTime.Unix()); got != want {
			t.Errorf("token times %s, want %s", got, want)
		}
	})
}

// RV181: the server's SQLite file of an archive goes in place at the next
// start with the token times set against the database it replaces, at
// the time of the swap; with the dump restored as well, the times the
// dump's restore set stay.
func TestSwappedDatabaseEndsTheTokensOfReplacedCredentials(t *testing.T) {
	ctx := context.Background()
	archive := archiveOfAccounts(t, "sqlite")
	for _, tc := range []struct {
		name     string
		database bool
	}{
		{"server file only", false},
		{"server file and dump", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dst := newInstall(t, "sqlite")
			addAccounts(t, dst.db, liveAccounts...)
			svc := dst.service(t, "x")
			svc.o.Now = func() time.Time { return fixtureRestoreTime }
			res, err := svc.RestoreFullBackup(ctx, archive, RestoreOptions{Database: tc.database, GoDB: true})
			if err != nil || len(res.Warnings) != 0 {
				t.Fatalf("restore: %+v %v", res, err)
			}
			if err := dst.db.Close(); err != nil {
				t.Fatal(err)
			}
			start := time.Now().Unix()
			if swapped, err := SwapInRestoredDatabase(dst.dbPath); err != nil || !swapped {
				t.Fatalf("swap: %v %v", swapped, err)
			}
			end := time.Now().Unix()
			for _, side := range []string{"-wal", "-shm"} {
				if _, err := os.Stat(dst.dbPath + RestoredSuffix + side); err == nil {
					t.Errorf("the swap left %s", RestoredSuffix+side)
				}
			}
			db, err := store.OpenSQLite(dst.dbPath)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			got := tokenTimes(t, db)
			if tc.database {
				if want := wantTokenTimes(fixtureRestoreTime.Unix()); got != want {
					t.Errorf("token times %s, want %s", got, want)
				}
				return
			}
			for now := start; now <= end; now++ {
				if got == wantTokenTimes(now) {
					return
				}
			}
			t.Errorf("token times %s, want %s with the time of the swap", got, wantTokenTimes(start))
		})
	}
}

// RV181: a server file from before credentials_changed_at gets the column
// at the swap, as the server's migration adds it, and with no database
// before it every account counts as replaced.
func TestSwapAddsTheTokenTimeToAnOlderFile(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "db_v2.sqlite3")
	old, err := sql.Open("sqlite", "file:"+dbPath+RestoredSuffix)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`CREATE TABLE users (id INTEGER PRIMARY KEY, username TEXT UNIQUE NOT NULL, password_hash TEXT NOT NULL, totp_secret TEXT DEFAULT '', totp_enabled INTEGER DEFAULT 0)`,
		`INSERT INTO users (username, password_hash) VALUES ('alice', 'alice-1'), ('bob', 'bob-1')`,
	} {
		if _, err := old.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}
	start := time.Now().Unix()
	if swapped, err := SwapInRestoredDatabase(dbPath); err != nil || !swapped {
		t.Fatalf("swap: %v %v", swapped, err)
	}
	end := time.Now().Unix()
	db, err := store.OpenSQLite(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := readStrings(context.Background(), db, `SELECT sql FROM sqlite_master WHERE name = 'users'`)
	if err != nil || len(rows) != 1 || !strings.HasSuffix(rows[0], ", credentials_changed_at INTEGER DEFAULT NULL)") {
		t.Errorf("users after the swap: %q %v", rows, err)
	}
	got := tokenTimes(t, db)
	for now := start; now <= end; now++ {
		if got == "alice="+strconv.FormatInt(now, 10)+" bob="+strconv.FormatInt(now, 10) {
			return
		}
	}
	t.Errorf("token times %s, want the time of the swap", got)
}
