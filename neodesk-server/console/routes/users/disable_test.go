package users_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/routetest"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// listedDisabled is what the account list says about the account's
// disabled flag; the test fails when the list has no such account.
func listedDisabled(t *testing.T, admin *routetest.Agent, name string) bool {
	t.Helper()
	got := admin.Do("GET", "/api/users", "")
	var out struct {
		Data struct {
			Users []struct {
				Username string `json:"username"`
				Disabled *bool  `json:"disabled"`
			} `json:"users"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(got.Body), &out); err != nil {
		t.Fatalf("list: %d %s", got.Status, got.Body)
	}
	for _, u := range out.Data.Users {
		if u.Username == name {
			if u.Disabled == nil {
				t.Fatalf("the list has no disabled field for %s", name)
			}
			return *u.Disabled
		}
	}
	t.Fatalf("%s is not in the list", name)
	return false
}

// TestDisableAndEnableAnAccount: PATCH {"disabled": true} keeps the account
// and its history, ends its sessions, revokes its access tokens and
// refuses its sign-in; {"disabled": false} lets it sign in again. Both
// are in the audit log (L107).
func TestDisableAndEnableAnAccount(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		w := world(t, storetest.Prepare(t, db, storetest.GoServerFirst), routetest.Options{Env: map[string]string{"LOGIN_RATE_LIMIT_MAX": "1000"}})
		ctx := context.Background()
		if db.IsPostgres() {
			if _, err := db.Exec(ctx, `SELECT setval(pg_get_serial_sequence('users', 'id'), (SELECT MAX(id) FROM users))`); err != nil {
				t.Fatal(err)
			}
		}
		admin := w.As("admin")
		id := createdID(t, admin.Do("POST", "/api/users", `{"username":"dora","password":"First-Pass-1!","role":"operator"}`, "X-Forwarded-For", "10.2.0.1"))
		doraID, _ := strconv.ParseInt(id, 10, 64)
		authn := w.Server.Deps().Authn
		if listedDisabled(t, admin, "dora") {
			t.Fatal("a new account is listed as disabled")
		}

		dora := w.SignedOut()
		if got := dora.Do("POST", "/api/auth/login", `{"username":"dora","password":"First-Pass-1!"}`); got.Status != http.StatusOK {
			t.Fatalf("sign in: %d %s", got.Status, got.Body)
		}
		token, err := authn.IssueAccessToken(ctx, doraID, "client-1", "uuid-1", "10.2.0.9")
		if err != nil {
			t.Fatal(err)
		}

		got := admin.Do("PATCH", "/api/users/"+id, `{"disabled":true}`)
		if got.Status != http.StatusOK || !strings.Contains(got.Body, `"disabled":true`) {
			t.Fatalf("disable: %d %s", got.Status, got.Body)
		}
		if got := dora.Do("GET", "/api/auth/verify", ""); got.Status != http.StatusUnauthorized {
			t.Errorf("the session of a disabled account goes on: %d %s", got.Status, got.Body)
		}
		if user, err := authn.ValidateAccessToken(ctx, token); err != nil || user != nil {
			t.Errorf("the access token of a disabled account works: %+v, %v", user, err)
		}
		if got := w.SignedOut().Do("POST", "/api/auth/login", `{"username":"dora","password":"First-Pass-1!"}`); got.Status != http.StatusForbidden || !strings.Contains(got.Body, `"code":"account_disabled"`) {
			t.Errorf("sign-in of a disabled account: %d %s", got.Status, got.Body)
		}
		expect(t, "wrong password of a disabled account", w.SignedOut().Do("POST", "/api/auth/login", `{"username":"dora","password":"Wrong-Pass-1!"}`),
			401, "")
		if !listedDisabled(t, admin, "dora") {
			t.Error("the list does not show the account as disabled")
		}

		expect(t, "enable", admin.Do("PATCH", "/api/users/"+id, `{"disabled":false}`), 200, "")
		if listedDisabled(t, admin, "dora") {
			t.Error("the list shows the enabled account as disabled")
		}
		if got := w.SignedOut().Do("POST", "/api/auth/login", `{"username":"dora","password":"First-Pass-1!"}`); got.Status != http.StatusOK {
			t.Errorf("sign in after enabling: %d %s", got.Status, got.Body)
		}

		var actions []string
		rows, err := db.Query(ctx, `SELECT action FROM audit_log WHERE action IN ('user_disabled', 'user_enabled') ORDER BY id`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		for rows.Next() {
			var a string
			if err := rows.Scan(&a); err != nil {
				t.Fatal(err)
			}
			actions = append(actions, a)
		}
		if strings.Join(actions, ",") != "user_disabled,user_enabled" {
			t.Errorf("audit entries %v, want user_disabled then user_enabled", actions)
		}
	})
}

// TestDisableFollowsWhoMayChangeWhom: an account cannot disable itself, the
// last administrator, or an account whose role it could not give; a value
// that is no true or false is refused (L107).
func TestDisableFollowsWhoMayChangeWhom(t *testing.T) {
	w := world(t, nil, routetest.Options{})
	admin := w.As("admin")
	ownID := strconv.FormatInt(routetest.RoleIDs["admin"], 10)
	expect(t, "own account", admin.Do("PATCH", "/api/users/"+ownID, `{"disabled":true}`),
		400, `{"success":false,"error":"You cannot disable your own account"}`)

	global := w.As("global_admin")
	expect(t, "an admin by global_admin", global.Do("PATCH", "/api/users/"+ownID, `{"disabled":true}`),
		403, `{"success":false,"error":"Cannot assign a role higher than your own"}`)

	id := createdID(t, admin.Do("POST", "/api/users", `{"username":"vic","password":"First-Pass-1!","role":"viewer"}`, "X-Forwarded-For", "10.3.0.1"))
	expect(t, "not a flag", admin.Do("PATCH", "/api/users/"+id, `{"disabled":"yes"}`),
		400, `{"success":false,"error":"Invalid value for disabled"}`)
	expect(t, "a viewer by global_admin", global.Do("PATCH", "/api/users/"+id, `{"disabled":true}`), 200, "")
}
