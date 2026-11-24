package devices_test

import (
	"context"
	"crypto/ed25519"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	apiserver "github.com/neodeskapp/neodesk/neodesk-server/api"
	"github.com/neodeskapp/neodesk/neodesk-server/auth"
	serverconfig "github.com/neodeskapp/neodesk/neodesk-server/config"
	"github.com/neodeskapp/neodesk/neodesk-server/console/admintoken"
	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/routetest"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
	"github.com/neodeskapp/neodesk/neodesk-server/db"
	"github.com/neodeskapp/neodesk/neodesk-server/peer"
	"github.com/neodeskapp/neodesk/neodesk-server/relay"
)

// signinPassword is the made-up password of the console tests.
const signinPassword = "Made-Up-Pass-42"

// signinWorld is a console in front of a real API server with the saved
// sign-in vault, the devices SIGNC1 (every account sees it) and SIGNC2 (in
// a group only the viewer may see).
func signinWorld(t *testing.T, sdb *store.DB, roles routetest.Overrides) (*routetest.World, db.Database) {
	t.Helper()
	gdb := storetest.GoServer(t, sdb)
	srv := apiserver.New(serverconfig.DefaultConfig(), gdb, peer.NewMap(), relay.New(nil), "test")
	if err := srv.InitDeviceVault(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	opts := routetest.Options{DB: sdb, API: srv.InternalHandler(), Env: raisedLimits, Config: nodeLanguages}
	if roles != nil {
		// The console and the API server each decide by the same overrides
		opts.Roles = roles
		for role, perms := range roles {
			for perm, granted := range perms {
				if err := gdb.SetRolePermission(role, perm, granted); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	w := routetest.New(t, opts)
	for _, id := range []string{"SIGNC1", "SIGNC2"} {
		if err := gdb.UpsertPeer(&db.Peer{ID: id, UUID: "u-" + id, OS: "Windows"}); err != nil {
			t.Fatal(err)
		}
	}
	for _, role := range []string{"super_admin", "operator", "viewer"} {
		if err := gdb.CreateUser(&db.User{Username: "e2e_" + role, PasswordHash: "x", Role: role}); err != nil && !strings.Contains(strings.ToLower(err.Error()), "unique") {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	for _, q := range []string{
		`INSERT INTO device_groups (guid, name, note, team_id, source_type, tag_filter) VALUES ('g-signin', 'Viewer only', '', '', 'manual', '')`,
		`INSERT INTO device_group_user_access (device_group_id, user_id) SELECT id, 6 FROM device_groups WHERE guid = 'g-signin'`,
		`INSERT INTO device_group_members (device_group_id, peer_id) SELECT id, 'SIGNC2' FROM device_groups WHERE guid = 'g-signin'`,
	} {
		if _, err := sdb.Exec(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	return w, gdb
}

var liveSessions atomic.Int64

// liveSession opens a relay session of the browser viewer of account to
// device, as signal and the relay do, and returns its uuid: prefix and a
// number of its own (a uuid is used once, also across the databases' runs).
func liveSession(t *testing.T, prefix, device string, account int64) string {
	t.Helper()
	uuid := fmt.Sprintf("%s-%d", prefix, liveSessions.Add(1))
	if !relay.AuthorizeRelayPairFor(uuid, "panel-web-remote", device, account) || !relay.ClaimRelayPair(uuid) || !relay.ClaimRelayPair(uuid) {
		t.Fatalf("open the relay session %s", uuid)
	}
	return uuid
}

func auditHas(rows []string, action, part string) bool {
	for _, row := range rows {
		if strings.HasPrefix(row, action+"|") && strings.Contains(row, part) {
			return true
		}
	}
	return false
}

// TestSavedSigninThroughTheConsole: a super admin sets, replaces, switches
// and clears a device's saved sign-in, each with an audit entry; the
// reads and the audit log never hold the password; the viewer's open
// session gets the details once, as an answer no browser or proxy keeps,
// with the audit entry written first; a second use in the session is
// refused and audited. On SQLite and PostgreSQL.
func TestSavedSigninThroughTheConsole(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, sdb *store.DB) {
		w, _ := signinWorld(t, sdb, nil)
		admin := w.As("super_admin")

		got := admin.Do(http.MethodGet, "/api/devices/SIGNC1/signin", "")
		if got.Status != http.StatusOK || !strings.Contains(got.Body, `"set":false`) || !strings.Contains(got.Body, `"auto":false`) {
			t.Fatalf("status before a save: %d %s", got.Status, got.Body)
		}
		got = admin.Do(http.MethodPut, "/api/devices/SIGNC1/signin", `{"username":"LAB\\admin","password":"`+signinPassword+`"}`)
		if got.Status != http.StatusOK || strings.Contains(got.Body, signinPassword) {
			t.Fatalf("set: %d %s", got.Status, got.Body)
		}
		got = admin.Do(http.MethodPut, "/api/devices/SIGNC1/signin", `{"username":"LAB\\admin","password":"`+signinPassword+`"}`)
		if got.Status != http.StatusOK || !strings.Contains(got.Body, `"replaced":true`) {
			t.Fatalf("replace: %d %s", got.Status, got.Body)
		}
		got = admin.Do(http.MethodGet, "/api/devices/SIGNC1/signin", "")
		if got.Status != http.StatusOK || !strings.Contains(got.Body, `"username":"LAB\\admin"`) || strings.Contains(got.Body, signinPassword) {
			t.Fatalf("status: %d %s", got.Status, got.Body)
		}
		if got = admin.Do(http.MethodPatch, "/api/devices/SIGNC1/signin", `{"auto":true}`); got.Status != http.StatusOK || !strings.Contains(got.Body, `"auto":true`) {
			t.Fatalf("switch: %d %s", got.Status, got.Body)
		}

		session := liveSession(t, "11111111-2222-4333-8444-555555555555", "SIGNC1", routetest.RoleIDs["super_admin"])
		got = admin.Do(http.MethodPost, "/api/devices/SIGNC1/signin/use", `{"session":"`+session+`"}`)
		if got.Status != http.StatusOK || !strings.Contains(got.Body, `"password":"`+signinPassword+`"`) {
			t.Fatalf("use: %d %s", got.Status, got.Body)
		}
		if cc := got.Header.Get("Cache-Control"); cc != "no-store" {
			t.Errorf("use answer Cache-Control %q, want no-store", cc)
		}
		if etag := got.Header.Get("ETag"); etag != "" {
			t.Errorf("use answer has an ETag (a hash of the password's answer): %q", etag)
		}
		rows := w.AuditRows()
		if !auditHas(rows, "signin.use", "SIGNC1, session 11111111") {
			t.Errorf("no audit entry of the use: %v", rows)
		}
		got = admin.Do(http.MethodPost, "/api/devices/SIGNC1/signin/use", `{"session":"`+session+`"}`)
		if got.Status != http.StatusConflict || !strings.Contains(got.Body, "already_used") || strings.Contains(got.Body, signinPassword) {
			t.Errorf("second use in the session: %d %s", got.Status, got.Body)
		}
		if got = admin.Do(http.MethodPost, "/api/devices/SIGNC1/signin/use", `{"session":"no-such-session"}`); got.Status != http.StatusConflict || !strings.Contains(got.Body, "session_not_open") {
			t.Errorf("use without an open session: %d %s", got.Status, got.Body)
		}
		if got = admin.Do(http.MethodDelete, "/api/devices/SIGNC1/signin", ""); got.Status != http.StatusOK || !strings.Contains(got.Body, `"cleared":true`) {
			t.Fatalf("clear: %d %s", got.Status, got.Body)
		}

		rows = w.AuditRows()
		for _, want := range [][2]string{
			{"signin.set", `Saved sign-in set on SIGNC1 for account LAB\admin`},
			{"signin.replace", "Saved sign-in replaced on SIGNC1"},
			{"signin.change", "automatic sign-in on"},
			{"signin.use", "session 11111111"},
			{"signin.refused", "use on SIGNC1 refused: already_used, session 11111111"},
			{"signin.refused", "session_not_open"},
			{"signin.clear", "Saved sign-in cleared on SIGNC1"},
		} {
			if !auditHas(rows, want[0], want[1]) {
				t.Errorf("no audit entry %s %q in %v", want[0], want[1], rows)
			}
		}
		for _, row := range rows {
			if strings.Contains(row, signinPassword) {
				t.Errorf("an audit entry holds the password: %s", row)
			}
		}
	})
}

// TestSavedSigninRefusesTokensAndAccountsWithoutIt: an API token may set
// the details (it can carry Manage saved sign-in) but never use them, also
// with every permission; an account without the permissions, and one with
// them for a device outside its scope, is refused; every refused write and
// use is in the audit log.
func TestSavedSigninRefusesTokensAndAccountsWithoutIt(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, sdb *store.DB) {
		w, _ := signinWorld(t, sdb, nil)
		tokens := w.Server.Deps().AdminTokens
		value, _, err := tokens.Create(context.Background(),
			admintoken.Creator{ID: routetest.RoleIDs["super_admin"], Username: "e2e_super_admin", Holds: func(string) bool { return true }},
			admintoken.Input{Name: "signin test", Permissions: auth.AllPermissions, ExpiresAt: time.Now().Add(time.Hour)}, auth.AllPermissions)
		if err != nil {
			t.Fatal(err)
		}
		bearer := []string{"Authorization", "Bearer " + value}
		script := w.SignedOut()
		if got := script.Do(http.MethodPut, "/api/devices/SIGNC1/signin", `{"username":"admin","password":"`+signinPassword+`"}`, bearer...); got.Status != http.StatusOK {
			t.Fatalf("a token sets the details: %d %s", got.Status, got.Body)
		}
		session := liveSession(t, "21111111-2222-4333-8444-555555555555", "SIGNC1", routetest.RoleIDs["super_admin"])
		got := script.Do(http.MethodPost, "/api/devices/SIGNC1/signin/use", `{"session":"`+session+`"}`, bearer...)
		if got.Status != http.StatusForbidden || !strings.Contains(got.Body, "token_not_allowed") || strings.Contains(got.Body, signinPassword) {
			t.Errorf("a token uses the details: %d %s", got.Status, got.Body)
		}
		// The token's refusal did not spend the session's one use
		if got := w.As("super_admin").Do(http.MethodPost, "/api/devices/SIGNC1/signin/use", `{"session":"`+session+`"}`); got.Status != http.StatusOK {
			t.Errorf("the person's use after the token's refusal: %d %s", got.Status, got.Body)
		}

		op := w.As("operator")
		if got := op.Do(http.MethodGet, "/api/devices/SIGNC1/signin", ""); got.Status != http.StatusForbidden {
			t.Errorf("operator reads the status: %d %s", got.Status, got.Body)
		}
		if got := op.Do(http.MethodPut, "/api/devices/SIGNC1/signin", `{"username":"x","password":"y"}`); got.Status != http.StatusForbidden {
			t.Errorf("operator sets the details: %d %s", got.Status, got.Body)
		}
		if got := op.Do(http.MethodPost, "/api/devices/SIGNC1/signin/use", `{"session":"`+session+`"}`); got.Status != http.StatusForbidden {
			t.Errorf("operator uses the details: %d %s", got.Status, got.Body)
		}
		rows := w.AuditRows()
		for _, want := range []string{
			"use on SIGNC1 refused (API token): the use needs a person in the browser viewer",
			"set on SIGNC1 refused: no permission device.signin_manage",
			"use on SIGNC1 refused: no permission device.signin_use",
		} {
			if !auditHas(rows, "signin.refused", want) {
				t.Errorf("no refusal entry %q in %v", want, rows)
			}
		}
	})
}

// TestSavedSigninSealedThroughTheConsole: a viewer whose device opens
// sealed sign-ins gets a packet and never the password, the audit entry
// says it went sealed, and the device gets no readable details afterwards
// (L469).
func TestSavedSigninSealedThroughTheConsole(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, sdb *store.DB) {
		w, gdb := signinWorld(t, sdb, nil)
		seed := make([]byte, 32)
		seed[0] = 7
		if err := gdb.UpsertPeer(&db.Peer{ID: "SIGNC1", PK: ed25519.NewKeyFromSeed(seed).Public().(ed25519.PublicKey)}); err != nil {
			t.Fatal(err)
		}
		admin := w.As("super_admin")
		if got := admin.Do(http.MethodPut, "/api/devices/SIGNC1/signin", `{"username":"admin","password":"`+signinPassword+`"}`); got.Status != http.StatusOK {
			t.Fatalf("set: %d %s", got.Status, got.Body)
		}
		session := liveSession(t, "41111111-2222-4333-8444-555555555555", "SIGNC1", routetest.RoleIDs["super_admin"])
		got := admin.Do(http.MethodPost, "/api/devices/SIGNC1/signin/use", `{"session":"`+session+`","mode":"sealed"}`)
		if got.Status != http.StatusOK || !strings.Contains(got.Body, `"path":"sealed"`) || strings.Contains(got.Body, signinPassword) || strings.Contains(got.Body, `"password"`) {
			t.Fatalf("sealed use: %d %s", got.Status, got.Body)
		}
		if !auditHas(w.AuditRows(), "signin.use", "sealed for the computer's NeoDesk") {
			t.Errorf("no audit entry of the sealed use: %v", w.AuditRows())
		}
		other := liveSession(t, "51111111-2222-4333-8444-555555555555", "SIGNC1", routetest.RoleIDs["super_admin"])
		if got := admin.Do(http.MethodPost, "/api/devices/SIGNC1/signin/use", `{"session":"`+other+`","mode":"typed"}`); got.Status != http.StatusConflict || strings.Contains(got.Body, signinPassword) {
			t.Errorf("readable details after a sealed use: %d %s", got.Status, got.Body)
		}
	})
}

// TestSavedSigninKeepsToTheScope: a role given both permissions sets and
// uses the details of a device in its scope only; the refusal of a
// device outside it is in the audit log.
func TestSavedSigninKeepsToTheScope(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, sdb *store.DB) {
		w, _ := signinWorld(t, sdb, routetest.Overrides{"operator": {auth.PermDeviceSigninManage: true, auth.PermDeviceSigninUse: true}})
		op := w.As("operator")
		if got := op.Do(http.MethodPut, "/api/devices/SIGNC2/signin", `{"username":"x","password":"y"}`); got.Status != http.StatusForbidden {
			t.Errorf("a device outside the scope: %d %s", got.Status, got.Body)
		}
		if got := op.Do(http.MethodPost, "/api/devices/SIGNC2/signin/use", `{"session":"x"}`); got.Status != http.StatusForbidden {
			t.Errorf("use on a device outside the scope: %d %s", got.Status, got.Body)
		}
		if got := op.Do(http.MethodPut, "/api/devices/SIGNC1/signin", `{"username":"x","password":"y"}`); got.Status != http.StatusOK {
			t.Errorf("a device in the scope: %d %s", got.Status, got.Body)
		}
		session := liveSession(t, "31111111-2222-4333-8444-555555555555", "SIGNC1", routetest.RoleIDs["operator"])
		if got := op.Do(http.MethodPost, "/api/devices/SIGNC1/signin/use", `{"session":"`+session+`"}`); got.Status != http.StatusOK || !strings.Contains(got.Body, `"password":"y"`) {
			t.Errorf("use on a device in the scope: %d %s", got.Status, got.Body)
		}
		rows := w.AuditRows()
		for _, want := range []string{
			"set on SIGNC2 refused: device outside the account's scope",
			"use on SIGNC2 refused: device outside the account's scope",
		} {
			if !auditHas(rows, "signin.refused", want) {
				t.Errorf("no refusal entry %q in %v", want, rows)
			}
		}
	})
}
