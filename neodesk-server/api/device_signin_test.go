package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/auth"
	"github.com/neodeskapp/neodesk/neodesk-server/config"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
	"github.com/neodeskapp/neodesk/neodesk-server/db"
	"github.com/neodeskapp/neodesk/neodesk-server/peer"
	"github.com/neodeskapp/neodesk/neodesk-server/relay"
)

// The saved sign-in's test password; made up, it opens nothing.
const signinTestPassword = "Wr0ng-Hors3-Battery-ä€\\@"

var (
	signinAdmin    = Identity{Username: "signin_boss", Role: auth.RoleSuperAdmin, Actor: "signin_boss"}
	signinOperator = Identity{Username: "signin_op", Role: auth.RoleOperator, Actor: "signin_op"}
)

// signinServer is a server on database with the vault's key in a folder
// of its own and the two test accounts.
func signinServer(t *testing.T, database db.Database) *Server {
	t.Helper()
	srv := New(config.DefaultConfig(), database, peer.NewMap(), nil, "1.0.0-test")
	if err := srv.InitDeviceVault(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	for _, u := range []*db.User{
		{Username: signinAdmin.Username, PasswordHash: "hash", Role: signinAdmin.Role},
		{Username: signinOperator.Username, PasswordHash: "hash", Role: signinOperator.Role},
	} {
		if err := database.CreateUser(u); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"SIGNPEER1", "SIGNPEER2"} {
		if err := database.UpsertPeer(&db.Peer{ID: id, UUID: "u-" + id, OS: "Windows"}); err != nil {
			t.Fatal(err)
		}
	}
	return srv
}

// TestSavedSigninIsWriteOnly: the account name and password are stored
// sealed, the reads show the name, who saved it and when but never the
// password, a replace says so and keeps the switch, a clear removes them,
// and the switch is off until someone turns it on. On SQLite and
// PostgreSQL.
func TestSavedSigninIsWriteOnly(t *testing.T) {
	storetest.Each(t, func(t *testing.T, sdb *store.DB) {
		database := storetest.GoServer(t, sdb)
		srv := signinServer(t, database)

		status, body := callAs(t, srv, signinAdmin, http.MethodGet, "/api/peers/SIGNPEER1/signin", "")
		if status != http.StatusOK || !strings.Contains(body, `"set":false`) || !strings.Contains(body, `"available":true`) || !strings.Contains(body, `"auto":false`) {
			t.Fatalf("status before a save: %d %s", status, body)
		}
		status, body = callAs(t, srv, signinAdmin, http.MethodPut, "/api/peers/SIGNPEER1/signin",
			`{"username":"LAB\\admin","password":"`+strings.ReplaceAll(signinTestPassword, `\`, `\\`)+`"}`)
		if status != http.StatusOK || !strings.Contains(body, `"replaced":false`) {
			t.Fatalf("set: %d %s", status, body)
		}
		if strings.Contains(body, "Hors3") {
			t.Fatalf("the answer to the set holds the password: %s", body)
		}
		status, body = callAs(t, srv, signinOperator, http.MethodGet, "/api/peers/SIGNPEER1/signin", "")
		if status != http.StatusForbidden {
			t.Errorf("an operator without the permissions reads the status: %d %s", status, body)
		}
		status, body = callAs(t, srv, signinAdmin, http.MethodGet, "/api/peers/SIGNPEER1/signin", "")
		var st struct {
			Set       bool   `json:"set"`
			Username  string `json:"username"`
			Form      string `json:"form"`
			Auto      bool   `json:"auto"`
			UpdatedBy string `json:"updated_by"`
			UpdatedAt string `json:"updated_at"`
		}
		if err := json.Unmarshal([]byte(body), &st); err != nil || status != http.StatusOK {
			t.Fatalf("status: %d %s", status, body)
		}
		if !st.Set || st.Username != `LAB\admin` || st.Form != "password" || st.Auto || st.UpdatedBy != "signin_boss" || st.UpdatedAt == "" {
			t.Errorf("status after the save: %+v", st)
		}
		if strings.Contains(body, "Hors3") || strings.Contains(body, "password\":\"") {
			t.Errorf("the status holds the password: %s", body)
		}
		row, err := database.GetDeviceSignin("SIGNPEER1")
		if err != nil || row == nil {
			t.Fatalf("row: %+v %v", row, err)
		}
		for _, v := range []string{row.UserCipher, row.PassCipher, row.UserNonce, row.PassNonce} {
			if strings.Contains(v, "Hors3") || strings.Contains(v, "admin") {
				t.Errorf("the database holds a readable value: %q", v)
			}
		}

		if status, body = callAs(t, srv, signinAdmin, http.MethodPatch, "/api/peers/SIGNPEER1/signin", `{"auto":true}`); status != http.StatusOK || !strings.Contains(body, `"auto":true`) {
			t.Fatalf("switch on: %d %s", status, body)
		}
		status, body = callAs(t, srv, signinAdmin, http.MethodPut, "/api/peers/SIGNPEER1/signin", `{"username":"admin","password":"other-made-up","form":"name_password"}`)
		if status != http.StatusOK || !strings.Contains(body, `"replaced":true`) || !strings.Contains(body, `"auto":true`) || !strings.Contains(body, `"form":"name_password"`) {
			t.Fatalf("replace: %d %s", status, body)
		}
		if status, body = callAs(t, srv, signinAdmin, http.MethodPatch, "/api/peers/SIGNPEER2/signin", `{"auto":true}`); status != http.StatusNotFound {
			t.Errorf("switch on a device without details: %d %s", status, body)
		}
		if status, body = callAs(t, srv, signinAdmin, http.MethodDelete, "/api/peers/SIGNPEER1/signin", ""); status != http.StatusOK || !strings.Contains(body, `"cleared":true`) {
			t.Fatalf("clear: %d %s", status, body)
		}
		if row, _ := database.GetDeviceSignin("SIGNPEER1"); row != nil {
			t.Error("the row is left after a clear")
		}
	})
}

// TestSavedSigninRefusesWhatCannotBeTyped: an empty or too long name or
// password, a control character (a tab or line break would move focus or
// submit) and a character outside the Basic Multilingual Plane (typed as
// two broken keys) are refused, and nothing is stored.
func TestSavedSigninRefusesWhatCannotBeTyped(t *testing.T) {
	srv, database := newInternalServer(t, nil)
	if err := srv.InitDeviceVault(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	for body, code := range map[string]string{
		`{"username":"","password":"x"}`:                                 "username_required",
		`{"username":"a","password":""}`:                                 "password_required",
		`{"username":"a","password":"` + strings.Repeat("x", 257) + `"}`: "password_too_long",
		`{"username":"a","password":"tab\there"}`:                        "password_control_character",
		`{"username":"a","password":"line\nbreak"}`:                      "password_control_character",
		`{"username":"a","password":"smile 😀"}`:                          "password_character_not_typeable",
		`{"username":"a\u0007","password":"x"}`:                          "username_control_character",
		`{"username":"a","password":"x","form":"pin"}`:                   "invalid_form",
	} {
		status, answer := asConsole(t, srv, http.MethodPut, "/api/peers/PEERAAA1/signin", body)
		if status != http.StatusBadRequest || !strings.Contains(answer, code) {
			t.Errorf("%s: %d %s, want 400 %s", body, status, answer, code)
		}
	}
	if row, _ := database.GetDeviceSignin("PEERAAA1"); row != nil {
		t.Error("a refused save stored a row")
	}
}

// TestSavedSigninNeedsItsPermissionsAndTheScope: the reads need one of the
// two permissions, the writes Manage saved sign-in, the use Use saved
// sign-in and Connect; no role but the super admins has them by default;
// a role given them keeps to its device scope. The routes answer only the
// console in the process, never the listener.
func TestSavedSigninNeedsItsPermissionsAndTheScope(t *testing.T) {
	for _, role := range []string{auth.RoleServerAdmin, auth.RoleGlobalAdmin, auth.RoleOperator, auth.RoleViewer, auth.RoleGuestIssuer, auth.RolePro} {
		for _, perm := range []string{auth.PermDeviceSigninUse, auth.PermDeviceSigninManage} {
			if auth.RoleHasPermission(role, perm) {
				t.Errorf("%s holds %s by default", role, perm)
			}
		}
	}
	for _, role := range []string{auth.RoleSuperAdmin, auth.RoleAdmin} {
		if !auth.RoleHasPermission(role, auth.PermDeviceSigninUse) || !auth.RoleHasPermission(role, auth.PermDeviceSigninManage) {
			t.Errorf("%s lacks a saved sign-in permission", role)
		}
	}

	srv := scopedFixture(t)
	if err := srv.InitDeviceVault(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	database := srv.db
	op := Identity{Username: "rv_op", Role: auth.RoleOperator, Actor: "rv_op"}
	if err := database.CreateUser(&db.User{Username: "rv_op", PasswordHash: "hash", Role: auth.RoleOperator}); err != nil {
		t.Fatal(err)
	}
	set := `{"username":"admin","password":"made-up"}`
	use := `{"session":"no-such-session","account":1}`
	for _, c := range []struct{ method, path, body string }{
		{http.MethodGet, "/api/peers/PEERBBB2/signin", ""},
		{http.MethodPut, "/api/peers/PEERBBB2/signin", set},
		{http.MethodPatch, "/api/peers/PEERBBB2/signin", `{"auto":true}`},
		{http.MethodDelete, "/api/peers/PEERBBB2/signin", ""},
		{http.MethodPost, "/api/peers/PEERBBB2/signin/use", use},
	} {
		if status, body := callAs(t, srv, op, c.method, c.path, c.body); status != http.StatusForbidden {
			t.Errorf("operator without the permissions, %s %s: %d %s, want 403", c.method, c.path, status, body)
		}
	}
	if err := database.SetRolePermission(auth.RoleOperator, auth.PermDeviceSigninManage, true); err != nil {
		t.Fatal(err)
	}
	if status, body := callAs(t, srv, op, http.MethodPut, "/api/peers/PEERBBB2/signin", set); status != http.StatusOK {
		t.Errorf("operator with Manage saved sign-in, a device in scope: %d %s", status, body)
	}
	if status, body := callAs(t, srv, op, http.MethodPut, "/api/peers/PEERAAA1/signin", set); status != http.StatusForbidden || !strings.Contains(body, "not in your scope") {
		t.Errorf("operator with Manage saved sign-in, a device outside the scope: %d %s", status, body)
	}
	if row, _ := database.GetDeviceSignin("PEERAAA1"); row != nil {
		t.Error("a device outside the scope got a saved sign-in")
	}
	if status, body := callAs(t, srv, op, http.MethodPost, "/api/peers/PEERBBB2/signin/use", use); status != http.StatusForbidden {
		t.Errorf("Manage saved sign-in uses the details: %d %s", status, body)
	}
	if err := database.SetRolePermission(auth.RoleOperator, auth.PermDeviceSigninUse, true); err != nil {
		t.Fatal(err)
	}
	if status, body := callAs(t, srv, op, http.MethodPost, "/api/peers/PEERAAA1/signin/use", use); status != http.StatusForbidden {
		t.Errorf("use outside the scope: %d %s", status, body)
	}
	if err := database.SetRolePermission(auth.RoleOperator, auth.PermDeviceConnect, false); err != nil {
		t.Fatal(err)
	}
	if status, body := callAs(t, srv, op, http.MethodPost, "/api/peers/PEERBBB2/signin/use", use); status != http.StatusForbidden {
		t.Errorf("use without Connect to devices: %d %s", status, body)
	}

	key := map[string]string{"X-API-Key": testAPIKey}
	for _, c := range []struct{ method, path, body string }{
		{http.MethodGet, "/api/peers/PEERBBB2/signin", ""},
		{http.MethodPut, "/api/peers/PEERBBB2/signin", set},
		{http.MethodPost, "/api/peers/PEERBBB2/signin/use", use},
		{http.MethodPost, "/api/peers/PEERBBB2/connect-password/answer", `{"salt":"s","challenge":"c"}`},
	} {
		if status, body := listenerCall(t, srv, c.method, c.path, c.body, key); status != http.StatusNotFound {
			t.Errorf("%s %s on the listener: %d %s, want 404", c.method, c.path, status, body)
		}
	}
}

// TestSavedSigninIsHandedOutOncePerLiveSession: the use answers the details
// only for a relay session that runs, to that device, from the browser
// viewer, opened by the same console account; once per session, also when
// the first attempt failed at the device; and not without saved details.
func TestSavedSigninIsHandedOutOncePerLiveSession(t *testing.T) {
	srv, database := newInternalServer(t, nil)
	if err := srv.InitDeviceVault(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	srv.signinLiveFn = func(uuid string) (relay.LivePair, bool) {
		pairs := map[string]relay.LivePair{
			"sess-a":     {Initiator: webRemoteInitiator, Target: "PEERAAA1", Account: 5, ConsoleSession: "console-1"},
			"sess-b":     {Initiator: webRemoteInitiator, Target: "PEERAAA1", Account: 5, ConsoleSession: "console-1"},
			"sess-other": {Initiator: webRemoteInitiator, Target: "PEERBBB2", Account: 5, ConsoleSession: "console-1"},
			"sess-app":   {Initiator: "123456789", Target: "PEERAAA1", Account: 5, ConsoleSession: "console-1"},
			"sess-guest": {Initiator: webRemoteInitiator, Target: "PEERAAA1", Account: 0},
			"sess-old":   {Initiator: webRemoteInitiator, Target: "PEERAAA1", Account: 5},
		}
		p, ok := pairs[uuid]
		return p, ok
	}
	useFrom := func(session string, account int, console string) (int, string) {
		return asConsole(t, srv, http.MethodPost, "/api/peers/PEERAAA1/signin/use",
			`{"session":"`+session+`","account":`+jsonInt(account)+`,"console_session":"`+console+`"}`)
	}
	use := func(session string, account int) (int, string) { return useFrom(session, account, "console-1") }
	if status, body := use("sess-a", 5); status != http.StatusNotFound || !strings.Contains(body, "not_set") {
		t.Fatalf("use without saved details: %d %s", status, body)
	}
	if status, body := asConsole(t, srv, http.MethodPut, "/api/peers/PEERAAA1/signin",
		`{"username":"LAB\\admin","password":"made-up-pass"}`); status != http.StatusOK {
		t.Fatalf("set: %d %s", status, body)
	}
	for _, c := range []struct {
		session string
		account int
		why     string
	}{
		{"no-such-session", 5, "a session that does not run"},
		{"sess-other", 5, "a session to another device"},
		{"sess-app", 5, "a session a RustDesk app opened"},
		{"sess-guest", 0, "a guest's session"},
		{"sess-a", 6, "another account's session"},
		{"sess-old", 5, "a session no console session is named for"},
	} {
		if status, body := use(c.session, c.account); status != http.StatusConflict || !strings.Contains(body, "session_not_open") {
			t.Errorf("%s: %d %s, want 409 session_not_open", c.why, status, body)
		}
	}
	// Another console session of the same account (a second browser, A239)
	// can't spend the one use of the session the first one opened
	for _, other := range []string{"console-2", ""} {
		if status, body := useFrom("sess-a", 5, other); status != http.StatusConflict || !strings.Contains(body, "session_not_open") {
			t.Errorf("console session %q: %d %s, want 409 session_not_open", other, status, body)
		}
	}
	status, body := use("sess-a", 5)
	if status != http.StatusOK {
		t.Fatalf("use: %d %s", status, body)
	}
	var got struct{ Username, Password, Form string }
	if err := json.Unmarshal([]byte(body), &got); err != nil || got.Username != `LAB\admin` || got.Password != "made-up-pass" || got.Form != "password" {
		t.Fatalf("use answer: %s %v", body, err)
	}
	if status, body := use("sess-a", 5); status != http.StatusConflict || !strings.Contains(body, "already_used") {
		t.Errorf("second use in the same session: %d %s, want 409 already_used", status, body)
	}
	if status, _ := use("sess-b", 5); status != http.StatusOK {
		t.Errorf("a new session uses the details again: %d", status)
	}

	// Details saved under another server's key do not open: no answer
	other, err := database.GetDeviceSignin("PEERAAA1")
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.InitDeviceVault(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	srv.signinLiveFn = func(string) (relay.LivePair, bool) {
		return relay.LivePair{Initiator: webRemoteInitiator, Target: "PEERAAA1", Account: 5, ConsoleSession: "console-1"}, true
	}
	if status, body := use("sess-c", 5); status != http.StatusConflict || !strings.Contains(body, "cannot_open") || strings.Contains(body, "made-up") {
		t.Errorf("details sealed with another key (%s): %d %s", other.KeyID, status, body)
	}
}

func jsonInt(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}
