package users_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/config"
	"github.com/unitronix/betterdesk-server/console/httpx"
	"github.com/unitronix/betterdesk-server/console/routes/internal/routetest"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// world is a console with the real language files, on db (a new SQLite
// database for nil), where X-Forwarded-For says the client's address.
func world(t *testing.T, db *store.DB, o routetest.Options) *routetest.World {
	t.Helper()
	lang := langDir(t)
	trust, err := httpx.ParseTrustProxy("true")
	if err != nil {
		t.Fatal(err)
	}
	o.DB = db
	o.Config = func(cfg *config.Config) {
		cfg.LangDir = lang
		cfg.TrustProxy = trust
	}
	return routetest.New(t, o)
}

func expect(t *testing.T, what string, got routetest.Answer, status int, body string) {
	t.Helper()
	if got.Status != status || (body != "" && got.Body != body) {
		t.Errorf("%s: %d %s, want %d %s", what, got.Status, got.Body, status, body)
	}
}

func createdID(t *testing.T, got routetest.Answer) string {
	t.Helper()
	var out struct {
		Data struct {
			ID json.Number `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(got.Body), &out); err != nil || out.Data.ID == "" {
		t.Fatalf("no id in %d %s", got.Status, got.Body)
	}
	return out.Data.ID.String()
}

// A username another request took between the check and the insert is
// answered as taken; another failure of the insert is a server error.
func TestUsernameTakenInTheMeantime(t *testing.T) {
	w := world(t, nil, routetest.Options{})
	admin := w.As("admin")
	ctx := context.Background()
	if _, err := w.DB.Exec(ctx, `CREATE TRIGGER race BEFORE INSERT ON users WHEN NEW.username = 'raced'
		BEGIN SELECT RAISE(ABORT, 'UNIQUE constraint failed: users.username'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := w.DB.Exec(ctx, `CREATE TRIGGER broken BEFORE INSERT ON users WHEN NEW.username = 'broken'
		BEGIN SELECT RAISE(ABORT, 'disk I/O error'); END`); err != nil {
		t.Fatal(err)
	}
	expect(t, "raced", admin.Do("POST", "/api/users", `{"username":"raced","password":"Strong-Pass-1!"}`, "X-Forwarded-For", "10.0.0.1"),
		400, `{"success":false,"error":"Username already exists"}`)
	expect(t, "broken", admin.Do("POST", "/api/users", `{"username":"broken","password":"Strong-Pass-1!"}`, "X-Forwarded-For", "10.0.0.2"),
		500, `{"success":false,"error":"Server error"}`)
}

// A role the role_permissions table takes user.view from cannot list the
// accounts, and the list is not read: no account gets its strategy guid.
func TestListRefusedByRoleOverride(t *testing.T) {
	w := world(t, nil, routetest.Options{Roles: routetest.Overrides{"operator": {"user.view": false}}})
	got := w.As("operator").Do("GET", "/api/users", "")
	if got.Status != http.StatusForbidden {
		t.Fatalf("list: %d %s, want 403", got.Status, got.Body)
	}
	var guids int
	if err := w.DB.QueryRow(context.Background(), `SELECT COUNT(*) FROM users WHERE guid <> ''`).Scan(&guids); err != nil {
		t.Fatal(err)
	}
	if guids != 0 {
		t.Errorf("%d accounts got a guid: the list was read", guids)
	}
	if got := w.As("admin").Do("GET", "/api/users", ""); got.Status != http.StatusOK {
		t.Errorf("list as admin: %d", got.Status)
	}
}

// A password set here is the one sign-in takes, and a role change, a new
// password and a deleted account leave the account's session alone
// (BUGS S9).
func TestPasswordsAndSessions(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		w := world(t, storetest.Prepare(t, db, storetest.GoServerFirst), routetest.Options{})
		if db.IsPostgres() {
			// The test accounts came with their ids; new ones follow them
			if _, err := db.Exec(context.Background(), `SELECT setval(pg_get_serial_sequence('users', 'id'), (SELECT MAX(id) FROM users))`); err != nil {
				t.Fatal(err)
			}
		}
		admin := w.As("admin")
		id := createdID(t, admin.Do("POST", "/api/users", `{"username":"carol","password":"First-Pass-1!","role":"operator"}`, "X-Forwarded-For", "10.1.0.1"))

		login := func(password string) (routetest.Answer, *routetest.Agent) {
			browser := w.SignedOut()
			return browser.Do("POST", "/api/auth/login", `{"username":"carol","password":"`+password+`"}`), browser
		}
		if got, _ := login("First-Pass-1!"); got.Status != http.StatusOK {
			t.Fatalf("sign in with the first password: %d %s", got.Status, got.Body)
		}
		expect(t, "change", admin.Do("PATCH", "/api/users/"+id, `{"password":"Second-Pass-2!","role":"viewer"}`), 200, "")
		if got, _ := login("First-Pass-1!"); got.Status == http.StatusOK {
			t.Errorf("the first password still signs in")
		}
		got, carol := login("Second-Pass-2!")
		if got.Status != http.StatusOK {
			t.Fatalf("sign in with the second password: %d %s", got.Status, got.Body)
		}
		expect(t, "reset", admin.Do("POST", "/api/users/"+id+"/reset-password", `{"newPassword":"Third-Pass-3!"}`, "X-Forwarded-For", "10.1.0.2"), 200, "")
		if got := carol.Do("GET", "/api/auth/verify", ""); got.Status != http.StatusOK || !strings.Contains(got.Body, `"role":"viewer"`) {
			t.Errorf("the session after the reset: %d %s", got.Status, got.Body)
		}
		expect(t, "delete", admin.Do("DELETE", "/api/users/"+id, ""), 200, `{"success":true}`)
		if got := carol.Do("GET", "/api/auth/verify", ""); got.Status != http.StatusOK {
			t.Errorf("the session of the deleted account ended: %d %s", got.Status, got.Body)
		}
	})
}

// Creating accounts, resetting passwords and changing one's own password
// share three requests per five minutes per address (BUGS C4).
func TestPasswordLimitIsSharedWithTheSignInRoutes(t *testing.T) {
	w := world(t, nil, routetest.Options{})
	admin := w.As("admin")
	const address = "10.2.0.1"
	expect(t, "first", admin.Do("POST", "/api/users", `{"username":"dave","password":"Strong-Pass-1!"}`, "X-Forwarded-For", address), 200, "")
	expect(t, "own password", admin.Do("POST", "/api/auth/password", `{"currentPassword":"x","newPassword":"y","confirmPassword":"y"}`, "X-Forwarded-For", address), 400, "")
	expect(t, "reset", admin.Do("POST", "/api/users/999999/reset-password", `{}`, "X-Forwarded-For", address), 404, "")
	expect(t, "fourth", admin.Do("POST", "/api/users", `{"username":"erin","password":"Strong-Pass-1!"}`, "X-Forwarded-For", address),
		429, `{"success":false,"error":"Too many password change attempts. Please try again later."}`)
	expect(t, "another address", admin.Do("POST", "/api/users", `{"username":"erin","password":"Strong-Pass-1!"}`, "X-Forwarded-For", "10.2.0.2"), 200, "")

	// The limit counts behind the sign-in check: requests without a
	// session leave the budget alone
	const fresh = "10.2.0.3"
	for i := 0; i < 3; i++ {
		expect(t, "signed out", w.SignedOut().Do("POST", "/api/users", `{"username":"frank","password":"Strong-Pass-1!"}`, "X-Forwarded-For", fresh), 401, "")
		expect(t, "signed out reset", w.SignedOut().Do("POST", "/api/users/6/reset-password", `{"newPassword":"Strong-Pass-1!"}`, "X-Forwarded-For", fresh), 401, "")
	}
	expect(t, "after the signed-out ones", admin.Do("POST", "/api/users", `{"username":"frank","password":"Strong-Pass-1!"}`, "X-Forwarded-For", fresh), 200, "")
}

// The last administrator is the last account with the role admin or
// super_admin: both count.
func TestLastAdministrator(t *testing.T) {
	w := world(t, nil, routetest.Options{})
	global := w.As("global_admin")
	// admin (1) and super_admin (2) are the two administrators
	expect(t, "one of two", global.Do("DELETE", "/api/users/2", ""), 200, `{"success":true}`)
	expect(t, "the last", global.Do("DELETE", "/api/users/1", ""), 400, `{"success":false,"error":"Cannot delete the last administrator"}`)
}

// String() of an object whose own toString member hides the method
// throws: folder and device ids made of such values are a server error,
// and nothing is written. An object as folderIds is no list and takes the
// account out of every folder.
func TestScopeValuesThatAreNoText(t *testing.T) {
	w := world(t, nil, routetest.Options{})
	ctx := context.Background()
	if _, err := w.DB.Exec(ctx, `INSERT INTO folders (name) VALUES ('Office')`); err != nil {
		t.Fatal(err)
	}
	admin := w.As("admin")
	expect(t, "grant", admin.Do("PATCH", "/api/users/6", `{"peerIds":["D1"],"folderIds":[1]}`), 200, "")
	expect(t, "folder ids", admin.Do("PATCH", "/api/users/6", `{"folderIds":[{"toString":1}],"peerIds":["D2"]}`), 500, `{"success":false,"error":"Server error"}`)
	expect(t, "device ids", admin.Do("PATCH", "/api/users/6", `{"peerIds":{"toString":1}}`), 500, `{"success":false,"error":"Server error"}`)
	expect(t, "device ids in a list", admin.Do("PATCH", "/api/users/6", `{"peerIds":[[{"toString":1}]]}`), 500, `{"success":false,"error":"Server error"}`)
	got := admin.Do("GET", "/api/users", "")
	if !strings.Contains(got.Body, `"username":"e2e_viewer"`) || !strings.Contains(got.Body, `"folder_ids":[1],"peer_grants":["D1"]`) {
		t.Errorf("a refused change wrote the scope: %s", got.Body)
	}
	got = admin.Do("PATCH", "/api/users/6", `{"folderIds":{"toString":1}}`)
	if got.Status != http.StatusOK || !strings.Contains(got.Body, `"folder_ids":[]`) {
		t.Errorf("an object as folder ids: %d %s", got.Status, got.Body)
	}
}

// A directory or single sign-on account keeps its password and (for LDAP)
// its role to the provider, but its user groups are set here.
func TestProviderAccountsGetTheirGroups(t *testing.T) {
	w := world(t, nil, routetest.Options{})
	ctx := context.Background()
	for _, provider := range []string{"ldap", "oidc"} {
		if _, err := w.DB.Exec(ctx, `INSERT INTO users (username, password_hash, role, auth_provider) VALUES (?, 'x', 'operator', ?)`, provider+"_user", provider); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := w.DB.Exec(ctx, `INSERT INTO user_groups (guid, name) VALUES ('field', 'Field')`); err != nil {
		t.Fatal(err)
	}
	admin := w.As("admin")
	for _, provider := range []string{"ldap", "oidc"} {
		var id string
		if err := w.DB.QueryRow(ctx, `SELECT CAST(id AS TEXT) FROM users WHERE username = ?`, provider+"_user").Scan(&id); err != nil {
			t.Fatal(err)
		}
		got := admin.Do("PATCH", "/api/users/"+id, `{"groupGuids":["field"]}`)
		if got.Status != http.StatusOK || !strings.Contains(got.Body, `"user_groups":["field"]`) {
			t.Errorf("%s: %d %s", provider, got.Status, got.Body)
		}
		expect(t, provider+" password", admin.Do("PATCH", "/api/users/"+id, `{"password":"Strong-Pass-1!","groupGuids":[]}`), 400, "")
		if got := admin.Do("GET", "/api/users", ""); !strings.Contains(got.Body, `"username":"`+provider+`_user","role":"operator","email":"","auth_provider":"`+provider+`","created_at":`) {
			t.Errorf("%s in the list: %s", provider, got.Body)
		}
	}
	var members int
	if err := w.DB.QueryRow(ctx, `SELECT COUNT(*) FROM user_group_members m INNER JOIN user_groups g ON g.id = m.user_group_id WHERE g.guid = 'field'`).Scan(&members); err != nil {
		t.Fatal(err)
	}
	if members != 2 {
		t.Errorf("%d members, want 2: a refused change must not write the groups", members)
	}
}

// The RustDesk client API answers the list for a bearer token; this route
// leaves such a request to it.
func TestBearerTokenIsLeftToTheClientAPI(t *testing.T) {
	w := world(t, nil, routetest.Options{})
	admin := w.As("admin")
	if got := admin.Do("GET", "/api/users", "", "Authorization", "Bearer abc"); got.Status == http.StatusOK && strings.Contains(got.Body, `"users"`) {
		t.Errorf("a bearer token got the account list: %d", got.Status)
	}
	expect(t, "an empty token", admin.Do("GET", "/api/users", "", "Authorization", "Bearer   "), 200, "")
	expect(t, "another scheme", admin.Do("GET", "/api/users", "", "Authorization", "Basic abc"), 200, "")
}
