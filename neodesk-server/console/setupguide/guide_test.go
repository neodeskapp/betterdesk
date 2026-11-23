package setupguide

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// fake is the server state the steps read besides the database.
type fake struct {
	address   string
	https     bool
	devices   int
	directory bool
}

func newService(db *store.DB, f *fake) *Service {
	return New(Options{
		DB:              db,
		Address:         func() string { return f.address },
		ServesHTTPS:     func() bool { return f.https },
		Devices:         func(context.Context) (int, error) { return f.devices, nil },
		DirectorySignIn: func(context.Context) bool { return f.directory },
		Now:             func() time.Time { return time.Date(2026, 11, 23, 9, 0, 0, 0, time.UTC) },
	})
}

func exec(t *testing.T, db *store.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(context.Background(), query, args...); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
}

func addUser(t *testing.T, db *store.DB, id int64, name, role string) {
	t.Helper()
	exec(t, db, `INSERT INTO users (id, username, password_hash, role) VALUES (?, ?, ?, ?)`, id, name, "pbkdf2-sha256$first-"+name, role)
}

// all holds every permission.
func all(string) bool { return true }

func status(t *testing.T, s *Service, v Viewer) Status {
	t.Helper()
	got, err := s.Status(context.Background(), v)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// steps writes the steps as "id=done" in order, for comparing.
func steps(st Status) string {
	parts := make([]string, len(st.Steps))
	for i, s := range st.Steps {
		mark := "no"
		if s.Done {
			mark = "yes"
		}
		parts[i] = s.ID + "=" + mark
	}
	return strings.Join(parts, " ")
}

func done(t *testing.T, st Status, id string) bool {
	t.Helper()
	for _, s := range st.Steps {
		if s.ID == id {
			return s.Done
		}
	}
	t.Fatalf("no step %s in %s", id, steps(st))
	return false
}

// On a new install the first admin finds the guide open. An admin made
// later, an operator and a viewer find it closed, and starting again
// does not open it a second time once the admin hid it.
func TestNewInstallOpensTheGuideForTheFirstAdminOnly(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		addUser(t, db, 1, "admin", "admin")
		s := newService(db, &fake{})
		if err := s.Begin(ctx); err != nil {
			t.Fatal(err)
		}
		addUser(t, db, 2, "second", "super_admin")
		addUser(t, db, 3, "helper", "operator")
		if !status(t, s, Viewer{UserID: 1, Can: all, SuperAdmin: true}).Visible {
			t.Error("the first admin does not see the guide")
		}
		for _, id := range []int64{2, 3} {
			if status(t, s, Viewer{UserID: id, Can: all, SuperAdmin: true}).Visible {
				t.Errorf("account %d sees the guide without asking", id)
			}
		}
		if err := s.SetOpen(ctx, 1, false); err != nil {
			t.Fatal(err)
		}
		if err := s.Begin(ctx); err != nil {
			t.Fatal(err)
		}
		if status(t, s, Viewer{UserID: 1, Can: all, SuperAdmin: true}).Visible {
			t.Error("a second Begin opened the guide the admin hid")
		}
	})
}

// The first admin is the first account with an admin role that may sign
// in, whatever came before it.
func TestBeginPicksTheFirstAdmin(t *testing.T) {
	db := storetest.Migrated(t)
	addUser(t, db, 1, "node", "viewer")
	addUser(t, db, 2, "old", "admin")
	exec(t, db, `UPDATE users SET disabled = 1 WHERE id = 2`)
	addUser(t, db, 3, "admin", "super_admin")
	s := newService(db, &fake{})
	if err := s.Begin(context.Background()); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[int64]bool{1: false, 2: false, 3: true} {
		if got := status(t, s, Viewer{UserID: id, Can: all, SuperAdmin: true}).Visible; got != want {
			t.Errorf("account %d: visible %v, want %v", id, got, want)
		}
	}
}

// An install the console ran on before (here: the start after the first
// one, as after an update) opens the guide for nobody, and neither does a
// database the Node console set up.
func TestExistingInstallOpensNothing(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		addUser(t, db, 1, "admin", "admin")
		if err := db.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
		s := newService(db, &fake{})
		if err := s.Begin(ctx); err != nil {
			t.Fatal(err)
		}
		if status(t, s, Viewer{UserID: 1, Can: all, SuperAdmin: true}).Visible {
			t.Error("an update opened the guide")
		}
		var rows int
		if err := db.QueryRow(ctx, `SELECT COUNT(*) FROM settings WHERE key LIKE 'setup_guide%'`).Scan(&rows); err != nil || rows != 0 {
			t.Errorf("%d guide rows after an update (%v)", rows, err)
		}
	})
	t.Run("node-first", func(t *testing.T) {
		db := storetest.Prepare(t, storetest.SQLite(t), storetest.NodeFirst)
		addUser(t, db, 1, "admin", "admin")
		s := newService(db, &fake{})
		if err := s.Begin(context.Background()); err != nil {
			t.Fatal(err)
		}
		if status(t, s, Viewer{UserID: 1, Can: all, SuperAdmin: true}).Visible {
			t.Error("a database of the Node console opened the guide")
		}
	})
}

// Hiding and bringing back work for every account, and an account that
// never had the guide can ask for it.
func TestHideAndBringBack(t *testing.T) {
	db := storetest.Migrated(t)
	ctx := context.Background()
	addUser(t, db, 1, "admin", "admin")
	addUser(t, db, 2, "second", "admin")
	s := newService(db, &fake{})
	if err := s.Begin(ctx); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		id   int64
		open bool
	}{{1, false}, {1, true}, {2, true}, {2, false}} {
		if err := s.SetOpen(ctx, c.id, c.open); err != nil {
			t.Fatal(err)
		}
		if got := status(t, s, Viewer{UserID: c.id, Can: all, SuperAdmin: true}).Visible; got != c.open {
			t.Errorf("account %d after SetOpen(%v): visible %v", c.id, c.open, got)
		}
	}
}

// Every step ticks from what the server sees, and the ones that happened
// once stay ticked when their trace is gone.
func TestStepsTickFromTheServer(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		addUser(t, db, 1, "admin", "admin")
		f := &fake{address: "localhost"}
		s := newService(db, f)
		if err := s.Begin(ctx); err != nil {
			t.Fatal(err)
		}
		admin := Viewer{UserID: 1, Can: all, SuperAdmin: true}
		want := "password=no two_factor=no address=no https=no computer=no session=no people=no backup=no"
		if got := steps(status(t, s, admin)); got != want {
			t.Fatalf("fresh install:\n got %s\nwant %s", got, want)
		}
		on, off := db.Pick("1", "TRUE"), db.Pick("0", "FALSE")

		exec(t, db, `UPDATE users SET password_hash = 'pbkdf2-sha256$mine' WHERE id = 1`)
		if !done(t, status(t, s, admin), StepPassword) {
			t.Error("a changed password does not tick")
		}
		exec(t, db, `UPDATE users SET password_hash = 'pbkdf2-sha256$first-admin' WHERE id = 1`)
		if !done(t, status(t, s, admin), StepPassword) {
			t.Error("the password step went back")
		}

		exec(t, db, `UPDATE users SET totp_enabled = `+on+` WHERE id = 1`)
		if !done(t, status(t, s, admin), StepTwoFactor) {
			t.Error("two-factor sign-in on does not tick")
		}
		exec(t, db, `UPDATE users SET totp_enabled = `+off+` WHERE id = 1`)
		if done(t, status(t, s, admin), StepTwoFactor) {
			t.Error("two-factor sign-in off still ticks")
		}

		for host, reachable := range map[string]bool{
			"localhost": false, "127.0.0.1": false, "[::1]": false, "0.0.0.0": false, "neodesk": false,
			"desk.local": false, "": false, "10.10.10.74": true, "nd.example.com": true, "[2001:db8::5]": true,
		} {
			f.address = host
			if got := done(t, status(t, s, admin), StepAddress); got != reachable {
				t.Errorf("address %q: done %v, want %v", host, got, reachable)
			}
		}

		if !done(t, status(t, s, Viewer{UserID: 1, Can: all, SuperAdmin: true, Secure: true}), StepHTTPS) {
			t.Error("a request over HTTPS does not tick")
		}
		f.https = true
		if !done(t, status(t, s, admin), StepHTTPS) {
			t.Error("the console's own TLS does not tick")
		}

		f.devices = 1
		if !done(t, status(t, s, admin), StepComputer) {
			t.Error("a device does not tick")
		}

		exec(t, db, `INSERT INTO audit_connections (host_id, action) VALUES ('900100001', 'new')`)
		if !done(t, status(t, s, admin), StepSession) {
			t.Error("a session does not tick")
		}
		exec(t, db, `DELETE FROM audit_connections`)
		if !done(t, status(t, s, admin), StepSession) {
			t.Error("the session step went back when its record aged out")
		}

		addUser(t, db, 2, "helper", "operator")
		if !done(t, status(t, s, admin), StepPeople) {
			t.Error("a second account does not tick")
		}
		exec(t, db, `UPDATE users SET disabled = `+on+` WHERE id = 2`)
		if done(t, status(t, s, admin), StepPeople) {
			t.Error("a disabled second account ticks")
		}
		f.directory = true
		if !done(t, status(t, s, admin), StepPeople) {
			t.Error("directory sign-in does not tick")
		}

		exec(t, db, `INSERT INTO audit_log (user_id, action, details) VALUES (1, 'backup_created', 'Full backup downloaded (1 MB)')`)
		if !done(t, status(t, s, admin), StepBackup) {
			t.Error("a downloaded backup does not tick")
		}
		exec(t, db, `DELETE FROM audit_log`)
		if !done(t, status(t, s, admin), StepBackup) {
			t.Error("the backup step went back when its record aged out")
		}
	})
}

// A scheduled backup that succeeded counts; one that only failed does not.
func TestScheduledBackupTicks(t *testing.T) {
	db := storetest.Migrated(t)
	addUser(t, db, 1, "admin", "admin")
	s := newService(db, &fake{})
	admin := Viewer{UserID: 1, Can: all, SuperAdmin: true}
	exec(t, db, `INSERT INTO settings (key, value) VALUES ('scheduled_backup_status', '{"lastAttemptAt":"2026-11-23T08:00:00.000Z","lastSuccessAt":null,"lastError":"disk full"}')`)
	if done(t, status(t, s, admin), StepBackup) {
		t.Error("a failed scheduled backup ticks")
	}
	exec(t, db, `UPDATE settings SET value = '{"lastSuccessAt":"2026-11-23T09:00:00.000Z"}' WHERE key = 'scheduled_backup_status'`)
	if !done(t, status(t, s, admin), StepBackup) {
		t.Error("a scheduled backup that succeeded does not tick")
	}
}

// The guide lists only the steps an account can act on: an account
// without the right to add computers, connect or add people does not get
// those steps, one that is no super admin gets no backup step (only super
// admins may make a full backup), and one that signs in through a
// directory gets no password or two-factor step. The password step belongs to the account the guide
// opened for, which holds the install's first password.
func TestStepsFollowTheAccount(t *testing.T) {
	db := storetest.Migrated(t)
	ctx := context.Background()
	addUser(t, db, 1, "admin", "admin")
	addUser(t, db, 2, "infra", "server_admin")
	addUser(t, db, 3, "sso", "super_admin")
	exec(t, db, `UPDATE users SET auth_provider = 'oidc' WHERE id = 3`)
	s := newService(db, &fake{})
	if err := s.Begin(ctx); err != nil {
		t.Fatal(err)
	}
	serverOnly := func(p string) bool { return p == "server.config" }
	for _, c := range []struct {
		v    Viewer
		want string
	}{
		{Viewer{UserID: 2, Can: serverOnly}, "two_factor=no address=no https=no"},
		{Viewer{UserID: 3, Can: all, SuperAdmin: true}, "address=no https=no computer=no session=no people=yes backup=no"},
		{Viewer{UserID: 99, Can: all, SuperAdmin: true}, ""},
	} {
		if got := steps(status(t, s, c.v)); got != c.want {
			t.Errorf("account %d:\n got %s\nwant %s", c.v.UserID, got, c.want)
		}
	}
}
