package users_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/auth"
	"github.com/neodeskapp/neodesk/neodesk-server/console/admintoken"
	"github.com/neodeskapp/neodesk/neodesk-server/console/mail"
	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/routetest"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// pageUser is what the user's page reads of GET /api/users/{id}.
type pageUser struct {
	ID             json.Number `json:"id"`
	Username       string      `json:"username"`
	DisplayName    string      `json:"display_name"`
	FirstName      string      `json:"first_name"`
	LastName       string      `json:"last_name"`
	TimeZone       string      `json:"time_zone"`
	Language       string      `json:"preferred_language"`
	Permissions    []string    `json:"permissions"`
	TeamChat       bool        `json:"team_chat"`
	LastSuperAdmin bool        `json:"last_super_admin"`
	Invite         *struct {
		ExpiresAt string `json:"expires_at"`
	} `json:"invite"`
}

func readPageUser(t *testing.T, got routetest.Answer) pageUser {
	t.Helper()
	var out struct {
		Success bool     `json:"success"`
		Data    pageUser `json:"data"`
	}
	if got.Status != http.StatusOK || json.Unmarshal([]byte(got.Body), &out) != nil || !out.Success {
		t.Fatalf("user: %d %s", got.Status, got.Body)
	}
	return out.Data
}

// The user's page reads one account: anyone with user.view gets it, with
// the display name (first and last name, else the username), what the
// role allows in effect and whether the person can use Team chat. An id
// without an account answers as the other routes of the account do, and
// the routes of later groups below /api/users still answer.
func TestTheUsersPageReadsOneAccount(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		w := world(t, storetest.Prepare(t, db, storetest.GoServerFirst), routetest.Options{})
		admin := w.As("admin")
		operator := fmt.Sprint(routetest.RoleIDs["operator"])

		u := readPageUser(t, admin.Do("GET", "/api/users/"+operator, ""))
		if u.Username != "e2e_operator" || u.DisplayName != "e2e_operator" {
			t.Errorf("without names: %+v", u)
		}
		if !slices.Contains(u.Permissions, "device.connect") || slices.Contains(u.Permissions, "user.create") || !u.TeamChat {
			t.Errorf("operator's permissions %v, team chat %v", u.Permissions, u.TeamChat)
		}
		guest := readPageUser(t, admin.Do("GET", fmt.Sprintf("/api/users/%d", routetest.RoleIDs["guest_issuer"]), ""))
		if guest.TeamChat || strings.Join(guest.Permissions, ",") != "device.view,guest.create" {
			t.Errorf("guest issuer: %v, team chat %v", guest.Permissions, guest.TeamChat)
		}
		super := readPageUser(t, admin.Do("GET", "/api/users/1", ""))
		if len(super.Permissions) != len(auth.AllPermissions) || super.LastSuperAdmin {
			t.Errorf("admin: %d permissions, last %v", len(super.Permissions), super.LastSuperAdmin)
		}
		// A disabled account cannot be written to in Team chat
		expect(t, "disable", admin.Do("PATCH", "/api/users/"+operator, `{"disabled":true}`), 200, "")
		if u := readPageUser(t, admin.Do("GET", "/api/users/"+operator, "")); u.TeamChat {
			t.Error("a disabled account offers Team chat")
		}
		expect(t, "enable", admin.Do("PATCH", "/api/users/"+operator, `{"disabled":false}`), 200, "")

		// The display name everywhere the list and the page name the person
		if _, err := w.DB.Exec(context.Background(), `UPDATE users SET first_name = 'Ada', last_name = ' Lovelace ' WHERE id = ?`, operator); err != nil {
			t.Fatal(err)
		}
		if u := readPageUser(t, admin.Do("GET", "/api/users/"+operator, "")); u.DisplayName != "Ada Lovelace" {
			t.Errorf("display name %q", u.DisplayName)
		}
		expectHas(t, "list", admin.Do("GET", "/api/users", ""), 200, `"username":"e2e_operator"`)
		if list := admin.Do("GET", "/api/users", "").Body; !strings.Contains(list, `"display_name":"Ada Lovelace"`) || !strings.Contains(list, `"display_name":"e2e_viewer"`) {
			t.Errorf("the list lacks the display names: %.400s", list)
		}

		// user.view is enough to read; without it the route refuses
		readPageUser(t, w.As("server_admin").Do("GET", "/api/users/"+operator, ""))
		expect(t, "viewer", w.As("viewer").Do("GET", "/api/users/"+operator, ""), http.StatusForbidden, "")
		expect(t, "missing", admin.Do("GET", "/api/users/99999", ""), http.StatusNotFound, `{"success":false,"error":"User not found"}`)
		expect(t, "zero", admin.Do("GET", "/api/users/0", ""), http.StatusBadRequest, `{"success":false,"error":"Invalid user ID"}`)
		// Other routes of the same shape answer as before
		expectHas(t, "avatars", admin.Do("GET", "/api/users/avatars", ""), 200, `"users":[`)
		expectHas(t, "invites", admin.Do("GET", "/api/users/invites", ""), 200, `"invites":[`)
		expectHas(t, "own profile", w.As("viewer").Do("PUT", "/api/users/me/profile", `{"first_name":"Vi"}`), 200, `"success":true`)
	})
}

// An admin who may change an account sets its display name and time zone
// on the user's page; the audit log says what changed, about whom. A time
// zone the server doesn't know is refused, and an account above the
// caller's own stays out of reach.
func TestTheUsersPageChangesTheProfile(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		w := world(t, storetest.Prepare(t, db, storetest.GoServerFirst), routetest.Options{})
		admin := w.As("admin")
		viewer := fmt.Sprint(routetest.RoleIDs["viewer"])
		path := "/api/users/" + viewer + "/profile"

		expect(t, "unknown zone", admin.Do("PUT", path, `{"time_zone":"Mars/Olympus"}`), http.StatusBadRequest, "")
		expect(t, "zone as a number", admin.Do("PUT", path, `{"time_zone":7}`), http.StatusBadRequest, "")
		expect(t, "nothing", admin.Do("PUT", path, `{}`), http.StatusBadRequest, "")
		u := readPageUser(t, admin.Do("PUT", path, `{"first_name":"Vera","last_name":"Rubin","time_zone":"Europe/Berlin"}`))
		if u.DisplayName != "Vera Rubin" || u.FirstName != "Vera" || u.TimeZone != "Europe/Berlin" {
			t.Errorf("after the change: %+v", u)
		}
		if u := readPageUser(t, admin.Do("GET", "/api/users/"+viewer, "")); u.TimeZone != "Europe/Berlin" {
			t.Errorf("time zone read back %q", u.TimeZone)
		}
		var target int64
		var details string
		if err := w.DB.QueryRow(context.Background(), `SELECT COALESCE(target_user_id, 0), details FROM audit_log WHERE action = 'user_profile_changed'`).Scan(&target, &details); err != nil {
			t.Fatal(err)
		}
		if target != routetest.RoleIDs["viewer"] || details != "Changed the profile of e2e_viewer: name from e2e_viewer to Vera Rubin; time zone from none to Europe/Berlin" {
			t.Errorf("audit: about %d, %q", target, details)
		}
		// The same values again write no entry
		readPageUser(t, admin.Do("PUT", path, `{"time_zone":"Europe/Berlin"}`))
		if n := strings.Count(strings.Join(w.AuditRows(), "\n"), "user_profile_changed"); n != 1 {
			t.Errorf("%d entries", n)
		}
		// Taking the zone away
		if u := readPageUser(t, admin.Do("PUT", path, `{"time_zone":""}`)); u.TimeZone != "" {
			t.Errorf("zone left: %q", u.TimeZone)
		}

		// global_admin may not change a super admin, operator has no user.edit
		expect(t, "global admin on a super admin", w.As("global_admin").Do("PUT", "/api/users/2/profile", `{"first_name":"X"}`), http.StatusForbidden, "")
		expect(t, "operator", w.As("operator").Do("PUT", path, `{"first_name":"X"}`), http.StatusForbidden, "")
		expect(t, "missing", admin.Do("PUT", "/api/users/99999/profile", `{"first_name":"X"}`), http.StatusNotFound, "")

		// The person sets their own on Your account
		own := w.As("operator")
		expectHas(t, "own zone", own.Do("PUT", "/api/users/me/profile", `{"time_zone":"America/New_York"}`), 200, `"success":true`)
		expectHas(t, "own zone read", own.Do("GET", "/api/users/me/profile", ""), 200, `"time_zone":"America/New_York"`)
		expect(t, "own bad zone", own.Do("PUT", "/api/users/me/profile", `{"time_zone":"Local"}`), http.StatusBadRequest, `{"success":false,"error":"time_zone_invalid"}`)

		// Deleting the account takes its row along
		expect(t, "delete", admin.Do("DELETE", "/api/users/"+viewer, ""), http.StatusOK, "")
		var rows int
		if err := w.DB.QueryRow(context.Background(), `SELECT COUNT(*) FROM account_profile WHERE user_id = ?`, routetest.RoleIDs["viewer"]).Scan(&rows); err != nil || rows != 0 {
			t.Errorf("rows left: %d %v", rows, err)
		}
	})
}

// The activity of an account is what it did and what was done about it,
// newest first; it needs user.view and audit.view.
func TestTheUsersPageListsActivity(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		w := world(t, storetest.Prepare(t, db, storetest.GoServerFirst), routetest.Options{})
		admin := w.As("admin")
		viewer := routetest.RoleIDs["viewer"]
		ctx := context.Background()
		for _, row := range []struct {
			user   int64
			target any
			action string
		}{
			{viewer, nil, "login"},
			{1, viewer, "user_disabled"},
			{1, nil, "settings_changed"},
			{routetest.RoleIDs["operator"], nil, "login"},
		} {
			if _, err := w.DB.Exec(ctx, `INSERT INTO audit_log (user_id, target_user_id, action, details, ip_address) VALUES (?, ?, ?, 'd', '192.0.2.1')`, row.user, row.target, row.action); err != nil {
				t.Fatal(err)
			}
		}
		got := admin.Do("GET", fmt.Sprintf("/api/users/%d/activity", viewer), "")
		var out struct {
			Data struct {
				Entries []struct {
					Action string `json:"action"`
					Actor  *struct {
						Username string `json:"username"`
					} `json:"actor"`
					Target *struct {
						Username string `json:"username"`
					} `json:"target"`
				} `json:"entries"`
			} `json:"data"`
		}
		if got.Status != 200 || json.Unmarshal([]byte(got.Body), &out) != nil {
			t.Fatalf("activity: %d %s", got.Status, got.Body)
		}
		var lines []string
		for _, e := range out.Data.Entries {
			line := e.Action + " by " + e.Actor.Username
			if e.Target != nil {
				line += " about " + e.Target.Username
			}
			lines = append(lines, line)
		}
		if strings.Join(lines, "; ") != "user_disabled by e2e_admin about e2e_viewer; login by e2e_viewer" {
			t.Errorf("entries: %v", lines)
		}
		expect(t, "guest issuer", w.As("guest_issuer").Do("GET", fmt.Sprintf("/api/users/%d/activity", viewer), ""), http.StatusForbidden, "")
		expect(t, "missing", admin.Do("GET", "/api/users/99999/activity", ""), http.StatusNotFound, "")
		expectHas(t, "limit", admin.Do("GET", fmt.Sprintf("/api/users/%d/activity?limit=1", viewer), ""), 200, `"entries":[{`)
	})
}

// makeToken gives the account an API token, made the way the console
// makes them.
func makeToken(t *testing.T, w *routetest.World, id int64, username, name string) *admintoken.Token {
	t.Helper()
	_, tok, err := w.Server.Deps().AdminTokens.Create(context.Background(),
		admintoken.Creator{ID: id, Username: username, Holds: func(string) bool { return true }},
		admintoken.Input{Name: name, Permissions: []string{"device.view"}, ReadOnly: true, ExpiresAt: time.Now().Add(24 * time.Hour)},
		auth.AllPermissions)
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

// The user's page lists an account's API tokens to anyone with user.view
// and revokes one with user.security for an account the caller may
// change; the audit entry is about the account. A token of another
// account is not found through this one.
func TestTheUsersPageRevokesTokens(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		w := world(t, storetest.Prepare(t, db, storetest.GoServerFirst), routetest.Options{})
		admin := w.As("admin")
		server := routetest.RoleIDs["server_admin"]
		tok := makeToken(t, w, server, "e2e_server_admin", "backup script")
		other := makeToken(t, w, routetest.RoleIDs["super_admin"], "e2e_super_admin", "theirs")
		list := fmt.Sprintf("/api/users/%d/api-tokens", server)

		expectHas(t, "list", w.As("operator").Do("GET", list, ""), 200, `"name":"backup script"`)
		if body := admin.Do("GET", list, "").Body; strings.Contains(body, "theirs") {
			t.Errorf("another account's token in the list: %s", body)
		}
		expect(t, "list, no user.view", w.As("viewer").Do("GET", list, ""), http.StatusForbidden, "")
		expect(t, "list, missing", admin.Do("GET", "/api/users/99999/api-tokens", ""), http.StatusNotFound, "")

		expect(t, "operator", w.As("operator").Do("DELETE", list+"/"+tok.ID, ""), http.StatusForbidden, "")
		// global_admin holds user.security but may not change a server admin
		expect(t, "global admin", w.As("global_admin").Do("DELETE", list+"/"+tok.ID, ""), http.StatusForbidden, "")
		expect(t, "through another account", admin.Do("DELETE", list+"/"+other.ID, ""), http.StatusNotFound, `{"success":false,"error":"API token not found"}`)
		expectHas(t, "revoke", admin.Do("DELETE", list+"/"+tok.ID, ""), 200, `"state":"revoked"`)
		expectHas(t, "listed revoked", admin.Do("GET", list, ""), 200, `"state":"revoked"`)
		var target int64
		var details string
		if err := w.DB.QueryRow(context.Background(), `SELECT COALESCE(target_user_id, 0), details FROM audit_log WHERE action = 'api_token_revoked'`).Scan(&target, &details); err != nil {
			t.Fatal(err)
		}
		if target != server || !strings.Contains(details, "backup script") {
			t.Errorf("audit: about %d, %q", target, details)
		}
		if got, err := w.Server.Deps().AdminTokens.Get(context.Background(), other.ID); err != nil || got == nil || !got.RevokedAt.IsZero() {
			t.Errorf("the other token: %+v %v", got, err)
		}
	})
}

// The security notices give the time in the person's time zone, and in
// UTC without one.
func TestSecurityNoticesUseThePersonsTimeZone(t *testing.T) {
	w, box := mailWorld(t, true)
	admin := w.As("admin")
	ana := makeAccount(t, w, admin, "ana", "ana@example.test")
	bo := makeAccount(t, w, admin, "bob", "bob@example.test")
	readPageUser(t, admin.Do("PUT", "/api/users/"+ana+"/profile", `{"time_zone":"Asia/Tokyo"}`))
	for _, id := range []string{ana, bo} {
		expect(t, "reset "+id, admin.Do("POST", "/api/users/"+id+"/reset-password", `{"newPassword":"Admin-Set-123!"}`), 200, "")
	}
	if got := mailsTo(box, "ana@example.test", 500*time.Millisecond); len(got) != 1 || !strings.Contains(*got[0].Text, " JST") || strings.Contains(*got[0].Text, " UTC") {
		t.Errorf("ana's mail: %q", textsOf(got))
	}
	if got := mailsTo(box, "bob@example.test", 500*time.Millisecond); len(got) != 1 || !strings.Contains(*got[0].Text, " "+time.Now().Format("MST")) || strings.Contains(*got[0].Text, " JST") {
		t.Errorf("bob mail: %q", textsOf(got))
	}
}

// textsOf are the plain-text parts of mails.
func textsOf(ms []mail.Outgoing) []string {
	out := []string{}
	for _, m := range ms {
		if m.Text != nil {
			out = append(out, *m.Text)
		}
	}
	return out
}

// What the page says a role allows is the role's permissions in effect:
// an override that grants or takes away a permission counts.
func TestTheUsersPageCountsRoleOverrides(t *testing.T) {
	w := world(t, storetest.Migrated(t), routetest.Options{Roles: routetest.Overrides{
		"operator": {"user.create": true, "device.connect": false},
	}})
	u := readPageUser(t, w.As("admin").Do("GET", fmt.Sprintf("/api/users/%d", routetest.RoleIDs["operator"]), ""))
	if !slices.Contains(u.Permissions, "user.create") || slices.Contains(u.Permissions, "device.connect") || !slices.Contains(u.Permissions, "device.view") {
		t.Errorf("operator with overrides: %v", u.Permissions)
	}
	pro := readPageUser(t, w.As("admin").Do("GET", fmt.Sprintf("/api/users/%d", routetest.RoleIDs["pro"]), ""))
	if len(pro.Permissions) != 0 || pro.TeamChat {
		t.Errorf("pro: %v", pro.Permissions)
	}
}
