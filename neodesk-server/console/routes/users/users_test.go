package users_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/config"
	"github.com/neodeskapp/neodesk/neodesk-server/console/httpx"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/routetest"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
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
// accounts.
func TestListRefusedByRoleOverride(t *testing.T) {
	w := world(t, nil, routetest.Options{Roles: routetest.Overrides{"operator": {"user.view": false}}})
	got := w.As("operator").Do("GET", "/api/users", "")
	if got.Status != http.StatusForbidden {
		t.Fatalf("list: %d %s, want 403", got.Status, got.Body)
	}
	if got := w.As("admin").Do("GET", "/api/users", ""); got.Status != http.StatusOK {
		t.Errorf("list as admin: %d", got.Status)
	}
}

// TestRoleChangeEndsTheAccountsSessions: a password set here is the one
// sign-in takes, and a new role, a new password, a new name and a deleted
// account end every session of the account and revoke its access tokens.
// A caller who resets their own password keeps the session that did it
// (BUGS.md S9, UM24).
func TestRoleChangeEndsTheAccountsSessions(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		// Two sessions per account, so the own reset has another one to end,
		// and sign-ins enough for all of the test
		w := world(t, storetest.Prepare(t, db, storetest.GoServerFirst), routetest.Options{Env: map[string]string{"SESSION_MAX_PER_USER": "2", "LOGIN_RATE_LIMIT_MAX": "1000"}})
		ctx := context.Background()
		if db.IsPostgres() {
			// The test accounts came with their ids; new ones follow them
			if _, err := db.Exec(ctx, `SELECT setval(pg_get_serial_sequence('users', 'id'), (SELECT MAX(id) FROM users))`); err != nil {
				t.Fatal(err)
			}
		}
		admin := w.As("admin")
		id := createdID(t, admin.Do("POST", "/api/users", `{"username":"carol","password":"First-Pass-1!","role":"operator"}`, "X-Forwarded-For", "10.1.0.1"))
		carolID, _ := strconv.ParseInt(id, 10, 64)
		authn := w.Server.Deps().Authn

		signIn := func(name, password string) *routetest.Agent {
			t.Helper()
			browser := w.SignedOut()
			if got := browser.Do("POST", "/api/auth/login", `{"username":"`+name+`","password":"`+password+`"}`); got.Status != http.StatusOK {
				t.Fatalf("sign in as %s: %d %s", name, got.Status, got.Body)
			}
			return browser
		}
		token := func() string {
			t.Helper()
			issued, err := authn.IssueAccessToken(ctx, carolID, "client-1", "uuid-1", "10.1.0.9")
			if err != nil {
				t.Fatal(err)
			}
			return issued
		}
		ended := func(what string, browser *routetest.Agent, issued string) {
			t.Helper()
			if got := browser.Do("GET", "/api/auth/verify", ""); got.Status != http.StatusUnauthorized {
				t.Errorf("%s: the session goes on: %d %s", what, got.Status, got.Body)
			}
			if user, err := authn.ValidateAccessToken(ctx, issued); err != nil || user != nil {
				t.Errorf("%s: the access token still works: %v, %v", what, user, err)
			}
		}

		// The same role again changes nothing
		carol, issued := signIn("carol", "First-Pass-1!"), token()
		expect(t, "same role", admin.Do("PATCH", "/api/users/"+id, `{"role":"operator"}`), 200, "")
		if got := carol.Do("GET", "/api/auth/verify", ""); got.Status != http.StatusOK {
			t.Errorf("the same role ended the session: %d %s", got.Status, got.Body)
		}
		expect(t, "role", admin.Do("PATCH", "/api/users/"+id, `{"role":"viewer"}`), 200, "")
		ended("new role", carol, issued)

		carol, issued = signIn("carol", "First-Pass-1!"), token()
		expect(t, "password", admin.Do("PATCH", "/api/users/"+id, `{"password":"Second-Pass-2!"}`), 200, "")
		ended("new password", carol, issued)
		if got := w.SignedOut().Do("POST", "/api/auth/login", `{"username":"carol","password":"First-Pass-1!"}`); got.Status == http.StatusOK {
			t.Errorf("the first password still signs in")
		}

		carol, issued = signIn("carol", "Second-Pass-2!"), token()
		expect(t, "reset", admin.Do("POST", "/api/users/"+id+"/reset-password", `{"newPassword":"Third-Pass-3!"}`, "X-Forwarded-For", "10.1.0.2"), 200, "")
		ended("reset password", carol, issued)

		carol, issued = signIn("carol", "Third-Pass-3!"), token()
		expect(t, "rename", admin.Do("PATCH", "/api/users/"+id, `{"username":"caroline"}`), 200, "")
		ended("new name", carol, issued)

		carol, issued = signIn("caroline", "Third-Pass-3!"), token()
		expect(t, "delete", admin.Do("DELETE", "/api/users/"+id, ""), 200, `{"success":true}`)
		ended("deleted account", carol, issued)

		var reasons []string
		rows, err := db.Query(ctx, `SELECT DISTINCT end_reason FROM panel_sessions WHERE user_id = ?`, carolID)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var reason *string
			if err := rows.Scan(&reason); err != nil {
				t.Fatal(err)
			}
			if reason != nil {
				reasons = append(reasons, *reason)
			}
		}
		_ = rows.Close()
		if strings.Join(reasons, ",") != "revoked" {
			t.Errorf("end reasons of the account's sessions: %v, want revoked", reasons)
		}

		// A reset of one's own password keeps the session that made it and
		// ends the others
		other := w.As("admin")
		expect(t, "own reset", admin.Do("POST", "/api/users/1/reset-password", `{"newPassword":"Fourth-Pass-4!"}`, "X-Forwarded-For", "10.1.0.3"), 200, "")
		if got := admin.Do("GET", "/api/auth/verify", ""); got.Status != http.StatusOK {
			t.Errorf("the own reset ended its own session: %d %s", got.Status, got.Body)
		}
		if got := other.Do("GET", "/api/auth/verify", ""); got.Status != http.StatusUnauthorized {
			t.Errorf("the own reset left another session of the account: %d %s", got.Status, got.Body)
		}
	})
}

const (
	// ownPassword is a change of one's own password that is refused: the
	// test accounts have no password that "x" matches
	ownPassword = `{"currentPassword":"x","newPassword":"Other-Pass-2!","confirmPassword":"Other-Pass-2!"}`
	// passwordPolicy is the RateLimit-Policy of the password change limit
	passwordPolicy = "3;w=300"
	tooManyChanges = `{"success":false,"error":"Too many password change attempts. Please try again later."}`
)

// unlimited checks an answer of the account routes: the status, and no
// headers of the password change limit.
func unlimited(t *testing.T, what string, got routetest.Answer, status int) {
	t.Helper()
	expect(t, what, got, status, "")
	if policy := got.Header.Get("RateLimit-Policy"); policy == passwordPolicy {
		t.Errorf("%s: answered with the password change limit's headers (RateLimit-Policy %s)", what, policy)
	}
}

// One admin creates more than three accounts and resets more than three
// passwords in five minutes from one address: each answers as without a
// limit, and the admin's own password change still has its three in five
// minutes (BUGS C4).
func TestCreatingAccountsDoesNotSpendThePasswordBudget(t *testing.T) {
	w := world(t, nil, routetest.Options{})
	admin := w.As("admin")
	const address = "10.2.0.1"
	for i := 0; i < 5; i++ {
		name := "dave_" + strconv.Itoa(i)
		unlimited(t, "create "+name, admin.Do("POST", "/api/users", `{"username":"`+name+`","password":"Strong-Pass-1!"}`, "X-Forwarded-For", address), 200)
	}
	// operator, viewer, guest_issuer and pro
	for _, id := range []string{"5", "6", "7", "8"} {
		unlimited(t, "reset of "+id, admin.Do("POST", "/api/users/"+id+"/reset-password", `{"newPassword":"Strong-Pass-1!"}`, "X-Forwarded-For", address), 200)
	}
	unlimited(t, "reset of a missing account", admin.Do("POST", "/api/users/999999/reset-password", `{}`, "X-Forwarded-For", address), 404)

	for i := 1; i <= 3; i++ {
		expect(t, "own password "+strconv.Itoa(i), admin.Do("POST", "/api/auth/password", ownPassword, "X-Forwarded-For", address), 400, "")
	}
	expect(t, "fourth own password", admin.Do("POST", "/api/auth/password", ownPassword, "X-Forwarded-For", address), 429, tooManyChanges)
}

// Only one's own password change counts against the password change
// limit, and the budget is the account's: an admin who spent it still
// creates accounts and resets passwords, has none left from another
// address, and another account behind the same address has its own three
// (BUGS C4).
func TestPasswordLimitCountsOnlyOwnPasswordChanges(t *testing.T) {
	w := world(t, nil, routetest.Options{})
	admin := w.As("admin")
	const address = "10.2.0.1"
	for i := 1; i <= 3; i++ {
		expect(t, "own password "+strconv.Itoa(i), admin.Do("POST", "/api/auth/password", ownPassword, "X-Forwarded-For", address), 400, "")
	}
	expect(t, "fourth own password", admin.Do("POST", "/api/auth/password", ownPassword, "X-Forwarded-For", address), 429, tooManyChanges)

	unlimited(t, "create", admin.Do("POST", "/api/users", `{"username":"erin","password":"Strong-Pass-1!"}`, "X-Forwarded-For", address), 200)
	unlimited(t, "reset", admin.Do("POST", "/api/users/6/reset-password", `{"newPassword":"Strong-Pass-1!"}`, "X-Forwarded-For", address), 200)

	expect(t, "own password from another address", admin.Do("POST", "/api/auth/password", ownPassword, "X-Forwarded-For", "10.2.0.2"), 429, tooManyChanges)
	expect(t, "another account's own password", w.As("super_admin").Do("POST", "/api/auth/password", ownPassword, "X-Forwarded-For", address), 400, "")
}

// The last administrator is the last account with the role admin or
// super_admin: both count.
func TestLastAdministrator(t *testing.T) {
	w := world(t, nil, routetest.Options{})
	admin := w.As("admin")
	ctx := context.Background()
	// The session keeps the role admin; the account's row no longer
	// counts. other_admin (9) and super_admin (2) are the two
	// administrators.
	for _, query := range []string{
		`UPDATE users SET role = 'operator' WHERE id = 1`,
		`INSERT INTO users (id, username, password_hash, role) VALUES (9, 'other_admin', 'x', 'admin')`,
	} {
		if _, err := w.DB.Exec(ctx, query); err != nil {
			t.Fatal(err)
		}
	}
	expect(t, "one of two", admin.Do("DELETE", "/api/users/9", ""), 200, `{"success":true}`)
	const last = `{"success":false,"error":"Cannot delete the last administrator"}`
	expect(t, "the last", admin.Do("DELETE", "/api/users/2", ""), 400, last)

	// Nor can the last one lose the role (BUGS US2); admin and super_admin
	// are both administrators
	expect(t, "the last demoted", admin.Do("PATCH", "/api/users/2", `{"role":"operator"}`), 400, last)
	var role string
	if err := w.DB.QueryRow(ctx, `SELECT role FROM users WHERE id = 2`).Scan(&role); err != nil || role != "super_admin" {
		t.Errorf("the last administrator's role: %q, %v", role, err)
	}
	expect(t, "the last as admin", admin.Do("PATCH", "/api/users/2", `{"role":"admin"}`), 200, "")
	expect(t, "the last back", admin.Do("PATCH", "/api/users/2", `{"role":"super_admin"}`), 200, "")
}

// global_admin may give operator, viewer and pro, so it may change only
// accounts with those roles, guest_issuer accounts and its own: an
// account above it keeps its password, email, role and row (BUGS US1).
func TestCannotChangeAnAccountAboveYourRole(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		w := world(t, storetest.Prepare(t, db, storetest.GoServerFirst), routetest.Options{})
		ctx := context.Background()
		global := w.As("global_admin")
		const strong = `"Strong-Pass-1!"`
		const above = `{"success":false,"error":"Cannot assign a role higher than your own"}`
		// admin (1), super_admin (2), server_admin (3)
		for i, id := range []string{"1", "2", "3"} {
			ip := "10.3.0." + strconv.Itoa(i+1)
			expect(t, "password of "+id, global.Do("PATCH", "/api/users/"+id, `{"password":`+strong+`}`), 403, above)
			expect(t, "email of "+id, global.Do("PATCH", "/api/users/"+id, `{"email":"taken@example.invalid"}`), 403, above)
			expect(t, "groups of "+id, global.Do("PATCH", "/api/users/"+id, `{"groupGuids":[]}`), 403, above)
			expect(t, "role of "+id, global.Do("PATCH", "/api/users/"+id, `{"role":"viewer"}`), 403, above)
			expect(t, "reset of "+id, global.Do("POST", "/api/users/"+id+"/reset-password", `{"newPassword":`+strong+`}`, "X-Forwarded-For", ip), 403, above)
			expect(t, "delete of "+id, global.Do("DELETE", "/api/users/"+id, ""), 403, above)
		}
		for id, role := range map[int]string{1: "admin", 2: "super_admin", 3: "server_admin"} {
			var stored string
			if err := db.QueryRow(ctx, `SELECT role || ' ' || password_hash || ' ' || COALESCE(email, '') FROM users WHERE id = ?`, id).Scan(&stored); err != nil {
				t.Fatalf("account %d: %v", id, err)
			}
			if want := role + " x "; stored != want {
				t.Errorf("account %d is now %q, want %q", id, stored, want)
			}
		}

		// What global_admin may still change
		expect(t, "operator", global.Do("PATCH", "/api/users/5", `{"role":"viewer","password":`+strong+`}`), 200, "")
		expect(t, "own account", global.Do("PATCH", "/api/users/4", `{"email":"me@example.invalid"}`), 200, "")
		expect(t, "guest_issuer", global.Do("POST", "/api/users/7/reset-password", `{"newPassword":`+strong+`}`, "X-Forwarded-For", "10.3.0.9"), 200, `{"success":true}`)
		expect(t, "pro", global.Do("DELETE", "/api/users/8", ""), 200, `{"success":true}`)
		// And the super admin roles may change any account
		expect(t, "admin by super_admin", w.As("super_admin").Do("PATCH", "/api/users/1", `{"role":"super_admin"}`), 200, "")
	})
}

// A new account's username is text: a number, flag, list or object is
// refused before anything is looked up or written, so a list named like
// an existing account cannot get past the "taken" check on PostgreSQL
// and empty that account's folders (BUGS US6).
func TestUsernameMustBeText(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		w := world(t, storetest.Prepare(t, db, storetest.GoServerFirst), routetest.Options{})
		ctx := context.Background()
		if db.IsPostgres() {
			if _, err := db.Exec(ctx, `SELECT setval(pg_get_serial_sequence('users', 'id'), (SELECT MAX(id) FROM users))`); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := db.Exec(ctx, `INSERT INTO folders (name) VALUES ('Office')`); err != nil {
			t.Fatal(err)
		}
		admin := w.As("admin")
		expect(t, "abc", admin.Do("POST", "/api/users", `{"username":"abc","password":"Strong-Pass-1!","folderIds":[1]}`, "X-Forwarded-For", "10.4.0.1"), 200, "")
		var before int
		if err := db.QueryRow(ctx, `SELECT COUNT(*) FROM users`).Scan(&before); err != nil {
			t.Fatal(err)
		}
		const invalid = `{"success":false,"error":"A username has 3 to 32 characters: letters, numbers and underscores only."}`
		for i, username := range []string{`12345`, `1.5`, `true`, `["abc"]`, `["ab","cd"]`, `{}`, `{"toString":"x"}`} {
			got := admin.Do("POST", "/api/users", `{"username":`+username+`,"password":"Strong-Pass-1!","folderIds":[]}`, "X-Forwarded-For", "10.4.1."+strconv.Itoa(i+1))
			expect(t, username, got, 400, invalid)
		}
		var after int
		if err := db.QueryRow(ctx, `SELECT COUNT(*) FROM users`).Scan(&after); err != nil {
			t.Fatal(err)
		}
		if after != before {
			t.Errorf("%d accounts were created", after-before)
		}
		if got := admin.Do("GET", "/api/users", ""); !strings.Contains(got.Body, `"username":"abc","role":"viewer","email":"","auth_provider":"local","created_at":`) ||
			!strings.Contains(got.Body, `"folder_ids":[1]`) {
			t.Errorf("abc lost its folder: %s", got.Body)
		}
	})
}

// The sign-in keeps the account id as text where the users table has
// 64-bit ids. The caller's own account is still its own: it cannot be
// deleted or demoted through these routes, and global_admin may still
// change it (BUGS US3).
func TestSessionAccountWithTextID(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		w := world(t, storetest.Prepare(t, db, storetest.GoServerFirst), routetest.Options{})
		w.Server.Router().Get("/__users/text-id", func(rw http.ResponseWriter, r *http.Request) {
			kit.Session(r).Set("userId", strconv.FormatInt(kit.UserID(r), 10))
			kit.OK(rw, r, nil)
		})
		admin, global := w.As("admin"), w.As("global_admin")
		for _, a := range []*routetest.Agent{admin, global} {
			expect(t, "text id", a.Do("GET", "/__users/text-id", ""), 200, "")
		}
		expect(t, "delete", admin.Do("DELETE", "/api/users/1", ""), 400, `{"success":false,"error":"You cannot delete your own account"}`)
		expect(t, "demote", admin.Do("PATCH", "/api/users/1", `{"role":"viewer"}`), 400, `{"success":false,"error":"You cannot demote yourself"}`)
		var role string
		if err := db.QueryRow(context.Background(), `SELECT role FROM users WHERE id = 1`).Scan(&role); err != nil || role != "admin" {
			t.Errorf("the admin account: role %q, %v", role, err)
		}
		expect(t, "own email", global.Do("PATCH", "/api/users/4", `{"email":"me@example.invalid"}`), 200, "")
	})
}

// Every check runs before the first write: a refused create stores no
// account and no memberships, a refused change leaves the account's
// name, role, password, email, guid, user groups, folders and strategy
// as they were. The two email errors are texts, not message keys (BUGS
// US4).
func TestRefusedChangesWriteNothing(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		w := world(t, storetest.Prepare(t, db, storetest.GoServerFirst), routetest.Options{})
		ctx := context.Background()
		queries := []string{
			`INSERT INTO folders (name) VALUES ('Office')`,
			`INSERT INTO user_groups (guid, name) VALUES ('g1', 'Group one')`,
		}
		if db.IsPostgres() {
			queries = append(queries, `SELECT setval(pg_get_serial_sequence('users', 'id'), (SELECT MAX(id) FROM users))`)
		}
		for _, query := range queries {
			if _, err := db.Exec(ctx, query); err != nil {
				t.Fatal(err)
			}
		}
		state := func() string {
			t.Helper()
			var s string
			if err := db.QueryRow(ctx, `SELECT username || ' ' || role || ' ' || password_hash || ' ' || COALESCE(email, '') || ' ' || COALESCE(guid, '') || ' ' ||
				(SELECT COUNT(*) FROM users) || ' ' || (SELECT COUNT(*) FROM user_group_members) || ' ' ||
				(SELECT COUNT(*) FROM device_group_user_access) || ' ' || (SELECT COUNT(*) FROM strategy_assignments)
				FROM users WHERE id = 5`).Scan(&s); err != nil {
				t.Fatal(err)
			}
			return s
		}
		const (
			invalidEmail = `{"success":false,"error":"Enter a valid email address."}`
			emailTooLong = `{"success":false,"error":"An email address has at most 200 characters."}`
			serverError  = `{"success":false,"error":"Server error"}`
			// An unknown strategy is refused, no longer a server error (L397)
			strategyMissing = `{"success":false,"error":"This RustDesk Pro strategy doesn't exist any more. Reload the page and choose again."}`
		)
		long := strings.Repeat("a", 195) + "@b.cde"
		admin := w.As("admin")
		for i, c := range []struct {
			method, path, body string
			status             int
			answer             string
		}{
			{"POST", "/api/users", `{"username":"mail_bad","password":"Strong-Pass-1!","groupGuids":["g1"],"email":"not-a-mail"}`, 400, invalidEmail},
			{"POST", "/api/users", `{"username":"mail_long","password":"Strong-Pass-1!","groupGuids":["g1"],"email":"` + long + `"}`, 400, emailTooLong},
			{"POST", "/api/users", `{"username":"mail_number","password":"Strong-Pass-1!","email":5}`, 400, invalidEmail},
			{"POST", "/api/users", `{"username":"no_strategy","password":"Strong-Pass-1!","groupGuids":["g1"],"folderIds":[1],"strategyGuid":"missing"}`, 400, strategyMissing},
			{"PATCH", "/api/users/5", `{"role":"viewer","password":"weak"}`, 400, ""},
			{"PATCH", "/api/users/5", `{"role":"viewer","password":"Strong-Pass-1!","groupGuids":["bad guid"]}`, 400, `{"success":false,"error":"Invalid user group identifier"}`},
			{"PATCH", "/api/users/5", `{"role":"viewer","password":"Strong-Pass-1!","groupGuids":["g1"],"folderIds":[1],"email":"bad"}`, 400, invalidEmail},
			{"PATCH", "/api/users/5", `{"groupGuids":["g1"],"email":"` + long + `"}`, 400, emailTooLong},
			{"PATCH", "/api/users/5", `{"role":"viewer","folderIds":[1],"strategyGuid":"missing"}`, 400, strategyMissing},
			// A password that is no text breaks the password rule (L94)
			{"PATCH", "/api/users/5", `{"role":"viewer","password":["Abcdefgh1!"]}`, 400, `{"success":false,"error":"users.password_too_short"}`},
			{"PATCH", "/api/users/5", `{"username":"renamed_op","role":"nonsense"}`, 400, `{"success":false,"error":"Invalid role"}`},
			// A taken name is refused before the other checks, as when the
			// rename came first
			{"PATCH", "/api/users/5", `{"username":"e2e_viewer","role":"nonsense"}`, 409, `{"success":false,"error":"Username already exists"}`},
			{"PATCH", "/api/users/5", `{"username":"renamed_again","password":"weak"}`, 400, ""},
		} {
			before := state()
			got := admin.Do(c.method, c.path, c.body, "X-Forwarded-For", "10.5.0."+strconv.Itoa(i+1))
			expect(t, c.body, got, c.status, c.answer)
			if after := state(); after != before {
				t.Errorf("%s %s: the refused request wrote\nbefore %s\n after %s", c.method, c.body, before, after)
			}
		}
	})
}

// A new password needs 8 characters, as the web form asks, also when it
// rates better than weak: creating, changing and resetting refuse a
// shorter one and write nothing (RV204).
func TestNewPasswordsNeedEightCharacters(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		w := world(t, storetest.Prepare(t, db, storetest.GoServerFirst), routetest.Options{})
		ctx := context.Background()
		state := func() string {
			t.Helper()
			var s string
			if err := db.QueryRow(ctx, `SELECT password_hash || ' ' || (SELECT COUNT(*) FROM users) FROM users WHERE id = 5`).Scan(&s); err != nil {
				t.Fatal(err)
			}
			return s
		}
		// The test console's language files lack the text, so the answer
		// names its key (users.password_too_short in en.json)
		const tooShort = `{"success":false,"error":"users.password_too_short"}`
		admin := w.As("admin")
		for i, c := range []struct{ method, path, body string }{
			{"POST", "/api/users", `{"username":"short_pw","password":"aB3!"}`},
			{"POST", "/api/users", `{"username":"short_pw","password":"Ab1!xyz"}`},
			{"PATCH", "/api/users/5", `{"password":"aB3!"}`},
			{"POST", "/api/users/5/reset-password", `{"newPassword":"aB3!"}`},
		} {
			before := state()
			got := admin.Do(c.method, c.path, c.body, "X-Forwarded-For", "10.6.0."+strconv.Itoa(i+1))
			expect(t, c.body, got, 400, tooShort)
			if after := state(); after != before {
				t.Errorf("%s %s: the refused request wrote %q, was %q", c.method, c.body, after, before)
			}
		}
		expect(t, "eight", admin.Do("POST", "/api/users/5/reset-password", `{"newPassword":"Ab1!xyzw"}`, "X-Forwarded-For", "10.6.0.9"), 200, "")
	})
}

// A new password longer than the sign-in takes (128 UTF-16 units) is
// refused by creating, changing and resetting (RV123).
func TestNewPasswordsFitTheSignIn(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		w := world(t, storetest.Prepare(t, db, storetest.GoServerFirst), routetest.Options{})
		ctx := context.Background()
		state := func() string {
			t.Helper()
			var s string
			if err := db.QueryRow(ctx, `SELECT password_hash || ' ' || (SELECT COUNT(*) FROM users) FROM users WHERE id = 5`).Scan(&s); err != nil {
				t.Fatal(err)
			}
			return s
		}
		const tooLong = `{"success":false,"error":"Password must be at most 128 characters"}`
		long := "Aa1!" + strings.Repeat("x", 125)
		admin := w.As("admin")
		for i, c := range []struct{ method, path, body string }{
			{"POST", "/api/users", `{"username":"long_pw","password":"` + long + `"}`},
			{"PATCH", "/api/users/5", `{"password":"` + long + `"}`},
			{"POST", "/api/users/5/reset-password", `{"newPassword":"` + long + `"}`},
		} {
			before := state()
			got := admin.Do(c.method, c.path, c.body, "X-Forwarded-For", "10.7.0."+strconv.Itoa(i+1))
			expect(t, c.method+" "+c.path, got, 400, tooLong)
			if after := state(); after != before {
				t.Errorf("%s %s: the refused request wrote %q, was %q", c.method, c.path, after, before)
			}
		}
		expect(t, "128", admin.Do("POST", "/api/users/5/reset-password", `{"newPassword":"`+long[:128]+`"}`, "X-Forwarded-For", "10.7.0.9"), 200, "")
	})
}

// The answer to a new account lists the user groups it became a member
// of, not the guids as they were sent, and an email of null is "" (BUGS
// US7). An unknown group is refused (L397, TestUsersRefuseBadValues).
func TestCreateAnswersStoredGroups(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		w := world(t, storetest.Prepare(t, db, storetest.GoServerFirst), routetest.Options{})
		ctx := context.Background()
		queries := []string{
			`INSERT INTO user_groups (guid, name) VALUES ('g-b', 'Beta')`,
			`INSERT INTO user_groups (guid, name) VALUES ('g-a', 'Alpha')`,
		}
		if db.IsPostgres() {
			queries = append(queries, `SELECT setval(pg_get_serial_sequence('users', 'id'), (SELECT MAX(id) FROM users))`)
		}
		for _, query := range queries {
			if _, err := db.Exec(ctx, query); err != nil {
				t.Fatal(err)
			}
		}
		got := w.As("admin").Do("POST", "/api/users", `{"username":"grouped","password":"Strong-Pass-1!","groupGuids":["g-b"," g-a ","g-b"],"email":null}`, "X-Forwarded-For", "10.6.0.1")
		want := `"username":"grouped","role":"viewer","email":"","user_groups":["g-a","g-b"],"folder_ids":[],"peer_grants":[]}}`
		if got.Status != http.StatusOK || !strings.HasSuffix(got.Body, want) {
			t.Errorf("create: %d %s, want the answer to end with %s", got.Status, got.Body, want)
		}
	})
}

// Reading accounts writes nothing: an account without a guid has none
// after the list and after the answer to a change, and gets one when a
// strategy is assigned to it (BUGS US8).
func TestListWritesNothing(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		w := world(t, storetest.Prepare(t, db, storetest.GoServerFirst), routetest.Options{})
		ctx := context.Background()
		if _, err := db.Exec(ctx, `INSERT INTO strategies (guid, name) VALUES ('st-1', 'Strategy one')`); err != nil {
			t.Fatal(err)
		}
		withGUID := func() int {
			t.Helper()
			var n int
			if err := db.QueryRow(ctx, `SELECT COUNT(*) FROM users WHERE COALESCE(guid, '') <> ''`).Scan(&n); err != nil {
				t.Fatal(err)
			}
			return n
		}
		admin := w.As("admin")
		expect(t, "list", admin.Do("GET", "/api/users", ""), 200, "")
		expect(t, "change", admin.Do("PATCH", "/api/users/5", `{"email":"op@example.invalid"}`), 200, "")
		if n := withGUID(); n != 0 {
			t.Errorf("%d accounts got a guid from reading them", n)
		}
		got := admin.Do("PATCH", "/api/users/5", `{"strategyGuid":"st-1"}`)
		if got.Status != http.StatusOK || !strings.Contains(got.Body, `"strategy_guid":"st-1"`) {
			t.Errorf("assign: %d %s", got.Status, got.Body)
		}
		if n := withGUID(); n != 1 {
			t.Errorf("%d accounts have a guid after one strategy was assigned, want 1", n)
		}
	})
}

// A deleted account leaves the list of who has a picture at once, not
// when the list's 30 seconds run out (BUGS AV2).
func TestDeletedUserLeavesTheAvatarList(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		w := world(t, storetest.Prepare(t, db, storetest.GoServerFirst), routetest.Options{})
		picture := db.Pick(`X'89504E47'`, `decode('89504e47', 'hex')`)
		if _, err := db.Exec(context.Background(), `INSERT INTO user_avatars (user_id, mime_type, data, version, source) VALUES (6, 'image/png', `+picture+`, 'v6', 'profile')`); err != nil {
			t.Fatal(err)
		}
		admin := w.As("admin")
		if got := admin.Do("GET", "/api/users/avatars", ""); got.Status != http.StatusOK || !strings.Contains(got.Body, `"v6"`) {
			t.Fatalf("the list before: %d %s", got.Status, got.Body)
		}
		expect(t, "delete", admin.Do("DELETE", "/api/users/6", ""), 200, `{"success":true}`)
		if got := admin.Do("GET", "/api/users/avatars", ""); got.Status != http.StatusOK || strings.Contains(got.Body, `"v6"`) {
			t.Errorf("the list after the delete: %d %s", got.Status, got.Body)
		}
	})
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
