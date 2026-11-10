package panelaccess_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/access"
	"github.com/unitronix/betterdesk-server/console/routes/internal/routetest"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// The test accounts: admin is 1, operator 5, viewer 6 (routetest.RoleIDs).

const policy = `{
	"sections": [
		{"resource_key": "devices", "access_level": "write"},
		{"key": "group:operations", "access": "read"},
		{"resource_key": "devices", "access_level": "none"},
		{"resource_key": "no-such-section", "access_level": "read"},
		{"resource_key": " users ", "access_level": 2},
		{"resource_key": "tickets"},
		"not a row"
	],
	"features": [
		{"feature_key": "file_transfer", "enabled": false},
		{"key": "clipboard", "enabled": "true"},
		{"feature_key": "tunnel", "enabled": "yes"},
		{"feature_key": "no-such-feature", "enabled": true}
	]
}`

const (
	storedSections = `[{"resource_key":"devices","access_level":"write"},{"resource_key":"group:operations","access_level":"read"},{"resource_key":"users","access_level":"write"},{"resource_key":"tickets","access_level":"none"}]`
	storedFeatures = `[{"feature_key":"file_transfer","enabled":false},{"feature_key":"clipboard","enabled":true},{"feature_key":"tunnel","enabled":false}]`
	// Rows come back ordered by their key
	readSections = `[{"resource_key":"devices","access_level":"write"},{"resource_key":"group:operations","access_level":"read"},{"resource_key":"tickets","access_level":"none"},{"resource_key":"users","access_level":"write"}]`
	readFeatures = `[{"feature_key":"clipboard","enabled":true},{"feature_key":"file_transfer","enabled":false},{"feature_key":"tunnel","enabled":false}]`
)

// answer is the body of the policy routes, with the parts kept as text.
type answer struct {
	Success bool   `json:"success"`
	Error   string `json:"error"`
	Data    struct {
		UserID    json.RawMessage `json:"user_id"`
		GroupGUID json.RawMessage `json:"group_guid"`
		Sections  json.RawMessage `json:"sections"`
		Features  json.RawMessage `json:"features"`
		Effective struct {
			SubjectType string `json:"subject_type"`
			SubjectID   string `json:"subject_id"`
			Sections    map[string]struct {
				Access string `json:"access"`
				Source string `json:"source"`
			} `json:"sections"`
			Features map[string]struct {
				Enabled bool   `json:"enabled"`
				Source  string `json:"source"`
			} `json:"features"`
			Groups json.RawMessage `json:"groups"`
		} `json:"effective"`
	} `json:"data"`
}

func decode(t *testing.T, got routetest.Answer) answer {
	t.Helper()
	var a answer
	if err := json.Unmarshal([]byte(got.Body), &a); err != nil {
		t.Fatalf("answer is not JSON: %v\n%s", err, got.Body)
	}
	return a
}

func exec(t *testing.T, db *store.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(context.Background(), query, args...); err != nil {
		t.Fatal(err)
	}
}

func eachDB(t *testing.T, fn func(t *testing.T, w *routetest.World)) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		fn(t, routetest.New(t, routetest.Options{DB: db}))
	})
}

func TestCatalogAndEffective(t *testing.T) {
	eachDB(t, func(t *testing.T, w *routetest.World) {
		catalog, err := json.Marshal(access.GetCatalog())
		if err != nil {
			t.Fatal(err)
		}
		operator := w.As("operator")
		if got := operator.Do("GET", "/api/panel/access/catalog", ""); got.Status != 200 || got.Body != `{"success":true,"data":`+string(catalog)+`}` {
			t.Errorf("catalog: %d %s", got.Status, got.Body)
		}
		got := operator.Do("GET", "/api/panel/access/effective", "")
		a := decode(t, got)
		if got.Status != 200 || !a.Success || !strings.HasPrefix(got.Body, `{"success":true,"data":{"subject_type":"user","subject_id":"5","sections":{"dashboard":{"access":"read","source":"role","role_ceiling":"read"}`) ||
			!strings.HasSuffix(got.Body, `"groups":[]}}`) {
			t.Errorf("effective: %d %s", got.Status, got.Body)
		}
	})
}

func TestUserPolicy(t *testing.T) {
	eachDB(t, func(t *testing.T, w *routetest.World) {
		admin := w.As("admin")
		got := admin.Do("GET", "/api/panel/access/users/6", "")
		a := decode(t, got)
		if got.Status != 200 || string(a.Data.UserID) != "6" || string(a.Data.Sections) != "[]" || string(a.Data.Features) != "[]" ||
			a.Data.Effective.SubjectID != "6" || a.Data.Effective.Sections["devices"].Access != "read" || a.Data.Effective.Sections["devices"].Source != "role" {
			t.Fatalf("before: %d %s", got.Status, got.Body)
		}
		if !strings.HasPrefix(got.Body, `{"success":true,"data":{"user_id":6,"sections":[],"features":[],"effective":{"subject_type":"user","subject_id":"6",`) {
			t.Errorf("order of the answer: %s", got.Body)
		}

		got = admin.Do("PUT", "/api/panel/access/users/6", policy)
		a = decode(t, got)
		if got.Status != 200 || string(a.Data.UserID) != "6" || string(a.Data.Sections) != storedSections || string(a.Data.Features) != storedFeatures {
			t.Fatalf("save: %d %s", got.Status, got.Body)
		}
		eff := a.Data.Effective
		if eff.Sections["devices"].Access != "write" || eff.Sections["devices"].Source != "user" ||
			eff.Sections["tickets"].Access != "none" || eff.Sections["chat"].Access != "read" || eff.Sections["chat"].Source != "user" ||
			eff.Features["file_transfer"].Enabled || eff.Features["file_transfer"].Source != "user" || !eff.Features["audio"].Enabled {
			t.Errorf("effective policy after the save: %s", got.Body)
		}
		if rows := w.AuditRows(); len(rows) != 1 || rows[0] != "user_access_policy_updated|Updated access policy for user 6|"+routetest.ClientIP+"|1" {
			t.Errorf("audit log: %q", rows)
		}

		got = admin.Do("GET", "/api/panel/access/users/6", "")
		a = decode(t, got)
		if got.Status != 200 || string(a.Data.Sections) != readSections || string(a.Data.Features) != readFeatures {
			t.Errorf("after: %d %s", got.Status, got.Body)
		}

		// A body that is a list, or lists of the wrong kind, store nothing
		for _, body := range []string{`[{"sections":[{"resource_key":"devices","access_level":"write"}]}]`, `{"sections":"devices","features":{"audio":false}}`, `{}`} {
			exec(t, w.DB, "INSERT INTO panel_access_policies (subject_type, subject_id, resource_key, access_level) VALUES ('user', '6', 'reports', 'read')")
			got = admin.Do("PUT", "/api/panel/access/users/6", body)
			a = decode(t, got)
			if got.Status != 200 || string(a.Data.Sections) != "[]" || string(a.Data.Features) != "[]" || a.Data.Effective.Sections["devices"].Source != "role" {
				t.Errorf("save %s: %d %s", body, got.Status, got.Body)
			}
		}
	})
}

func TestUserIDIsReadLikeANumberAtTheStart(t *testing.T) {
	eachDB(t, func(t *testing.T, w *routetest.World) {
		admin := w.As("admin")
		for _, id := range []string{"6", "6x", "%206", "6.9", "%2B6", "0006"} {
			got := admin.Do("GET", "/api/panel/access/users/"+id, "")
			if a := decode(t, got); got.Status != 200 || string(a.Data.UserID) != "6" || a.Data.Effective.SubjectID != "6" {
				t.Errorf("user %q: %d %s", id, got.Status, got.Body)
			}
		}
		notFound := `{"success":false,"error":"User not found"}`
		for _, id := range []string{"999", "-6", "0", "0x6"} {
			if got := admin.Do("GET", "/api/panel/access/users/"+id, ""); got.Status != 404 || got.Body != notFound {
				t.Errorf("user %q: %d %s", id, got.Status, got.Body)
			}
			if got := admin.Do("PUT", "/api/panel/access/users/"+id, policy); got.Status != 404 || got.Body != notFound {
				t.Errorf("save user %q: %d %s", id, got.Status, got.Body)
			}
		}
		if got := admin.Do("GET", "/api/panel/access/users/999", "", "Accept-Language", "de"); got.Status != 404 || got.Body != `{"success":false,"error":"Benutzer nicht gefunden"}` {
			t.Errorf("in German: %d %s", got.Status, got.Body)
		}
		// Text that is no number, and a number no id can be: SQLite finds
		// no such account, PostgreSQL fails on the value
		status, body := 404, notFound
		if w.DB.IsPostgres() {
			status, body = 500, `{"success":false,"error":"Server error"}`
		}
		for _, id := range []string{"abc", "x6", "99999999999999999999999", "-99999999999999999999999", "3000000000"} {
			if got := admin.Do("GET", "/api/panel/access/users/"+id, ""); got.Status != status || got.Body != body {
				t.Errorf("user %q: %d %s, want %d %s", id, got.Status, got.Body, status, body)
			}
			if got := admin.Do("PUT", "/api/panel/access/users/"+id, policy); got.Status != status || got.Body != body {
				t.Errorf("save user %q: %d %s, want %d %s", id, got.Status, got.Body, status, body)
			}
		}
		if rows := w.AuditRows(); len(rows) != 0 {
			t.Errorf("audit log after saves that found no account: %q", rows)
		}
	})
}

func TestGroupPolicy(t *testing.T) {
	eachDB(t, func(t *testing.T, w *routetest.World) {
		exec(t, w.DB, "INSERT INTO user_groups (guid, name) VALUES ('team-1', 'Team')")
		admin := w.As("admin")

		got := admin.Do("GET", "/api/panel/access/groups/team-1", "")
		a := decode(t, got)
		if got.Status != 200 || string(a.Data.GroupGUID) != `"team-1"` || string(a.Data.Sections) != "[]" || a.Data.Effective.SubjectType != "group" ||
			a.Data.Effective.SubjectID != "team-1" || a.Data.Effective.Sections["devices"].Access != "none" || a.Data.Effective.Sections["devices"].Source != "unset" {
			t.Fatalf("before: %d %s", got.Status, got.Body)
		}
		if !strings.HasPrefix(got.Body, `{"success":true,"data":{"group_guid":"team-1","sections":[],"features":[],"effective":{"subject_type":"group","subject_id":"team-1",`) || strings.Contains(got.Body, `"groups"`) {
			t.Errorf("order of the answer: %s", got.Body)
		}

		got = admin.Do("PUT", "/api/panel/access/groups/team-1", policy)
		a = decode(t, got)
		if got.Status != 200 || string(a.Data.Sections) != storedSections || string(a.Data.Features) != storedFeatures ||
			a.Data.Effective.Sections["devices"].Access != "write" || a.Data.Effective.Sections["devices"].Source != "group" || a.Data.Effective.Features["file_transfer"].Enabled {
			t.Fatalf("save: %d %s", got.Status, got.Body)
		}
		if rows := w.AuditRows(); len(rows) != 1 || rows[0] != "user_group_access_policy_updated|Updated access policy for group team-1|"+routetest.ClientIP+"|1" {
			t.Errorf("audit log: %q", rows)
		}
		got = admin.Do("GET", "/api/panel/access/groups/team-1", "")
		if a = decode(t, got); got.Status != 200 || string(a.Data.Sections) != readSections || string(a.Data.Features) != readFeatures {
			t.Errorf("after: %d %s", got.Status, got.Body)
		}

		notFound := `{"success":false,"error":"User group not found"}`
		for _, guid := range []string{"team-2", "TEAM-1", "no%20such%2Fgroup"} {
			if got := admin.Do("GET", "/api/panel/access/groups/"+guid, ""); got.Status != 404 || got.Body != notFound {
				t.Errorf("group %q: %d %s", guid, got.Status, got.Body)
			}
			if got := admin.Do("PUT", "/api/panel/access/groups/"+guid, policy); got.Status != 404 || got.Body != notFound {
				t.Errorf("save group %q: %d %s", guid, got.Status, got.Body)
			}
		}
		if got := admin.Do("GET", "/api/panel/access/groups/team-2", "", "Accept-Language", "de"); got.Body != `{"success":false,"error":"Benutzergruppe nicht gefunden"}` {
			t.Errorf("in German: %s", got.Body)
		}

		// A group whose guid a policy subject cannot be is refused with the reason
		exec(t, w.DB, "INSERT INTO user_groups (guid, name) VALUES ('odd guid', 'Odd')")
		if got := admin.Do("PUT", "/api/panel/access/groups/odd%20guid", policy); got.Status != 400 || got.Body != `{"success":false,"error":"Invalid policy subject"}` {
			t.Errorf("save for a guid with a space: %d %s", got.Status, got.Body)
		}
		if rows := w.AuditRows(); len(rows) != 1 {
			t.Errorf("audit log after refused saves: %q", rows)
		}
	})
}
