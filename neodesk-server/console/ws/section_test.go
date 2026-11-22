package ws_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/access"
	"github.com/unitronix/betterdesk-server/console/session"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
	"github.com/unitronix/betterdesk-server/console/ws"
)

type sectionSession struct {
	id   int64
	role string
}

func (s sectionSession) SignedIn() bool      { return s.id != 0 }
func (s sectionSession) UserID() int64       { return s.id }
func (s sectionSession) UserRole() string    { return s.role }
func (s sectionSession) Role() string        { return s.role }
func (s sectionSession) Username() string    { return "u" }
func (s sectionSession) User() *session.User { return nil }

// sectionAnswer runs SectionAllows for sn inside a handler of a registry
// with opts and returns what it said.
func sectionAnswer(t *testing.T, opts ws.Options, sn ws.Session, section string, level access.Level) (bool, error) {
	t.Helper()
	var allowed bool
	var err error
	reg := ws.New(opts)
	reg.Handle("/ws/x", func(u *ws.Upgrade) {
		allowed, err = u.SectionAllows(sn, section, level)
		u.Refuse(http.StatusForbidden)
	})
	reg.ServeHTTP(httptest.NewRecorder(), upgrade("/ws/x"))
	reg.Close()
	return allowed, err
}

// TestSectionAllowsAsksTheAccountsPolicy: SectionAllows hands the
// signed-in account's id and role, the section and the level to the
// lookup and returns its answer; nobody signed in, and a registry without
// a lookup, reach nothing (RV89).
func TestSectionAllowsAsksTheAccountsPolicy(t *testing.T) {
	var asked []string
	opts := ws.Options{SectionAccess: func(_ context.Context, user access.PolicyUser, section string, level access.Level) (bool, error) {
		asked = append(asked, user.Role+" "+section+" "+string(level))
		return user.ID == 7, nil
	}}
	if ok, err := sectionAnswer(t, opts, sectionSession{7, "admin"}, "server-management", access.LevelWrite); !ok || err != nil {
		t.Errorf("account 7: %v %v", ok, err)
	}
	if ok, _ := sectionAnswer(t, opts, sectionSession{8, "admin"}, "server-management", access.LevelWrite); ok {
		t.Error("account 8 was let in")
	}
	if ok, _ := sectionAnswer(t, opts, sectionSession{}, "server-management", access.LevelWrite); ok {
		t.Error("nobody signed in was let in")
	}
	if got := strings.Join(asked, ","); got != "admin server-management write,admin server-management write" {
		t.Errorf("asked %q", got)
	}
	if ok, _ := sectionAnswer(t, ws.Options{}, sectionSession{7, "admin"}, "server-management", access.LevelRead); ok {
		t.Error("without a lookup the account was let in")
	}
}

// TestSectionAccessOfReadsTheStoredPolicy: the adapter resolves the
// account's policy as the guard does: a server admin reaches write in
// server-management until its own row lowers the section to read or none
// (RV89). A row never lowers a super admin (A92).
func TestSectionAccessOfReadsTheStoredPolicy(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		if _, err := db.Exec(ctx, `INSERT INTO users (username, password_hash, role) VALUES (?, ?, ?)`, "srv", "x", "server_admin"); err != nil {
			t.Fatal(err)
		}
		var id int64
		if err := db.QueryRow(ctx, `SELECT id FROM users WHERE username = ?`, "srv").Scan(&id); err != nil {
			t.Fatal(err)
		}
		svc := access.New(db, access.NewDBRoleSource(nil), access.Options{})
		lookup := ws.SectionAccessOf(svc)
		user := access.PolicyUser{ID: id, Role: "server_admin"}
		check := func(level access.Level, want bool) {
			t.Helper()
			got, err := lookup(ctx, user, "server-management", level)
			if err != nil || got != want {
				t.Errorf("%s: %v %v, want %v", level, got, err, want)
			}
		}
		check(access.LevelWrite, true)
		for _, c := range []struct {
			stored      string
			read, write bool
		}{{"read", true, false}, {"none", false, false}} {
			sections := []any{map[string]any{"resource_key": "server-management", "access_level": c.stored}}
			if _, err := svc.SavePolicy(ctx, access.SubjectUser, strconv.FormatInt(id, 10), sections, []any{}); err != nil {
				t.Fatal(err)
			}
			check(access.LevelRead, c.read)
			check(access.LevelWrite, c.write)
		}
		// The same row, stored as "none", for an admin account
		if _, err := db.Exec(ctx, `INSERT INTO users (username, password_hash, role) VALUES (?, ?, ?)`, "root", "x", "admin"); err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRow(ctx, `SELECT id FROM users WHERE username = ?`, "root").Scan(&id); err != nil {
			t.Fatal(err)
		}
		none := []any{map[string]any{"resource_key": "server-management", "access_level": "none"}}
		if _, err := svc.SavePolicy(ctx, access.SubjectUser, strconv.FormatInt(id, 10), none, []any{}); err != nil {
			t.Fatal(err)
		}
		user = access.PolicyUser{ID: id, Role: "admin"}
		check(access.LevelRead, true)
		check(access.LevelWrite, true)
	})
}
