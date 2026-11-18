package devices_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/unitronix/betterdesk-server/api"
	"github.com/unitronix/betterdesk-server/console/config"
	"github.com/unitronix/betterdesk-server/console/routes/internal/routetest"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// identityAPI is a made-up API server that notes who each panel grouping
// call acted as, and refuses a caller without a role with 401 as the API
// server's in-process handler does.
type identityAPI struct {
	mu   sync.Mutex
	seen []string
}

func (a *identityAPI) calls() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := a.seen
	a.seen = nil
	return out
}

func (a *identityAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	_, _ = io.ReadAll(r.Body)
	w.Header().Set("Content-Type", "application/json")
	path := r.URL.EscapedPath()
	if strings.HasPrefix(path, "/api/panel/") {
		id, _ := api.IdentityFromContext(r.Context())
		a.mu.Lock()
		a.seen = append(a.seen, r.Method+" "+path+" as "+id.Username+"/"+id.Role)
		a.mu.Unlock()
		if id.Role == "" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"error":"Invalid or missing credentials"}`)
			return
		}
	}
	switch {
	case path == "/api/peers":
		_, _ = io.WriteString(w, `{"peers":[{"id":"A","hostname":"a"}]}`)
	case path == "/api/peers/A":
		_, _ = io.WriteString(w, `{"id":"A","hostname":"a"}`)
	case strings.HasPrefix(path, "/api/remote-targets"):
		_, _ = io.WriteString(w, `{"targets":[]}`)
	case r.Method == http.MethodPost && path == "/api/panel/device-groups":
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"guid":"g-new","name":"New","created":true}`)
	case strings.HasSuffix(path, "/peers"):
		_, _ = io.WriteString(w, `{"guid":"g","peers":[],"total":0}`)
	case r.Method == http.MethodDelete:
		_, _ = io.WriteString(w, `{"guid":"g","deleted":true}`)
	default:
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error":"not found"}`)
	}
}

// TestPanelTrustActsAsTheOperator checks who the device group writes reach
// the API server as. Without PANEL_TRUST_SECRET they act as the console's
// API key. With it, Node signed the operator's name and role and the API
// server took the role from the account's users row (the claimed role when
// there is no row, none for a role it does not know), and refused a
// statement signed for another path than the one requested, which a guid
// with a colon gives: encodeURIComponent escapes it in the request only.
func TestPanelTrustActsAsTheOperator(t *testing.T) {
	ctx := context.Background()
	run := func(t *testing.T, db *store.DB, trust bool) (*routetest.World, *identityAPI) {
		a := &identityAPI{}
		w := routetest.New(t, routetest.Options{DB: db, API: a, Env: raisedLimits, Config: func(cfg *config.Config) {
			nodeLanguages(cfg)
			cfg.PanelTrust = trust
		}})
		for _, q := range []string{
			`UPDATE device_groups SET guid = 'g-default'`,
			`INSERT INTO device_groups (guid, name, note, team_id, source_type, tag_filter) VALUES ('g-one', 'One', '', '', 'manual', '')`,
			`INSERT INTO device_groups (guid, name, note, team_id, source_type, tag_filter) VALUES ('g:two', 'Two', '', '', 'manual', '')`,
		} {
			if _, err := db.Exec(ctx, q); err != nil {
				t.Fatal(err)
			}
		}
		return w, a
	}
	expect := func(t *testing.T, a *identityAPI, what string, want ...string) {
		t.Helper()
		if got := a.calls(); strings.Join(got, "\n") != strings.Join(want, "\n") {
			t.Errorf("%s:\n got  %q\n want %q", what, got, want)
		}
	}

	t.Run("without the secret", func(t *testing.T) {
		w, a := run(t, storetest.Migrated(t), false)
		operator := w.As("operator")
		operator.Do("POST", "/api/device-groups", `{"name":"New"}`)
		expect(t, a, "save", "POST /api/panel/device-groups as apikey:console-bridge/admin")
		operator.Do("DELETE", "/api/device-groups/g:two", "")
		expect(t, a, "delete", "DELETE /api/panel/device-groups/g%3Atwo as apikey:console-bridge/admin")
	})

	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w, a := run(t, db, true)
		operator := w.As("operator")
		got := operator.Do("POST", "/api/device-groups", `{"name":"New"}`)
		if got.Status != http.StatusOK || !strings.Contains(got.Body, `"guid":"g-new"`) {
			t.Errorf("save: %d %s", got.Status, got.Body)
		}
		expect(t, a, "save", "POST /api/panel/device-groups as e2e_operator/operator")

		// The account's role in the users table, not the session's
		if _, err := w.DB.Exec(ctx, `UPDATE users SET role = 'viewer' WHERE username = 'e2e_operator'`); err != nil {
			t.Fatal(err)
		}
		operator.Do("PUT", "/api/devices/A/groups", `{"groupGuids":["g-one"]}`)
		expect(t, a, "members as a viewer in the table",
			"GET /api/panel/device-groups/g-default/peers as e2e_operator/viewer",
			"GET /api/panel/device-groups/g-one/peers as e2e_operator/viewer",
			"GET /api/panel/device-groups/g%3Atwo/peers as e2e_operator/",
		)

		// A role the API server does not know: no role, 401
		if _, err := w.DB.Exec(ctx, `UPDATE users SET role = 'made_up' WHERE username = 'e2e_operator'`); err != nil {
			t.Fatal(err)
		}
		got = operator.Do("POST", "/api/device-groups", `{"name":"New"}`)
		if got.Status != http.StatusUnauthorized || got.Body != `{"success":false,"error":"Request failed with status code 401"}` {
			t.Errorf("save with an unknown role: %d %s", got.Status, got.Body)
		}
		expect(t, a, "save with an unknown role", "POST /api/panel/device-groups as e2e_operator/")

		// No users row by that name: the role of the session
		if _, err := w.DB.Exec(ctx, `UPDATE users SET username = 'someone_else', role = 'operator' WHERE id = 5`); err != nil {
			t.Fatal(err)
		}
		operator.Do("POST", "/api/device-groups", `{"name":"New"}`)
		expect(t, a, "save without a users row", "POST /api/panel/device-groups as e2e_operator/operator")

		// A guid with a colon: the statement does not hold
		got = w.As("admin").Do("DELETE", "/api/device-groups/g:two", "")
		if got.Status != http.StatusUnauthorized {
			t.Errorf("delete of g:two: %d %s", got.Status, got.Body)
		}
		expect(t, a, "delete of g:two", "DELETE /api/panel/device-groups/g%3Atwo as e2e_admin/")
		w.As("admin").Do("DELETE", "/api/device-groups/g-one", "")
		expect(t, a, "delete of g-one", "DELETE /api/panel/device-groups/g-one as e2e_admin/admin")

		// A session without a user name: Node signed no statement, and the
		// call acted as the API key
		blank := w.As("operator")
		if _, err := w.DB.Exec(ctx, `UPDATE panel_sessions SET session_json = REPLACE(session_json, '"username":"e2e_operator"', '"username":" "')`); err != nil {
			t.Fatal(err)
		}
		blank.Do("POST", "/api/device-groups", `{"name":"New"}`)
		expect(t, a, "save without a user name", "POST /api/panel/device-groups as apikey:console-bridge/admin")
	})
}

// TestNodeOwnerKeepsTheWrites checks BACKEND_PANEL_GROUPING=node: the
// device group writes stay with the console's own tables and make no call
// to the API server's panel grouping, with the trust bridge on too.
func TestNodeOwnerKeepsTheWrites(t *testing.T) {
	ctx := context.Background()
	a := &identityAPI{}
	db := storetest.Migrated(t)
	w := routetest.New(t, routetest.Options{DB: db, API: a, Env: raisedLimits, Config: func(cfg *config.Config) {
		nodeLanguages(cfg)
		cfg.PanelGroupingNode, cfg.PanelTrust = true, true
	}})
	admin := w.As("admin")
	got := admin.Do("POST", "/api/device-groups", `{"name":"Kept","note":"n"}`)
	var saved struct {
		Data struct {
			ID   any    `json:"id"`
			GUID string `json:"guid"`
			Note string `json:"note"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(got.Body), &saved); err != nil || got.Status != http.StatusOK || saved.Data.ID == nil || saved.Data.Note != "n" {
		t.Fatalf("save: %d %s", got.Status, got.Body)
	}
	if got := admin.Do("PUT", "/api/devices/A/groups", `{"groupGuids":["`+saved.Data.GUID+`"]}`); got.Status != http.StatusOK {
		t.Errorf("members: %d %s", got.Status, got.Body)
	}
	var member string
	if err := db.QueryRow(ctx, `SELECT m.peer_id FROM device_group_members m JOIN device_groups g ON g.id = m.device_group_id WHERE g.guid = ?`, saved.Data.GUID).Scan(&member); err != nil || member != "A" {
		t.Errorf("the console's member row: %q, %v", member, err)
	}
	if got := admin.Do("DELETE", "/api/device-groups/"+saved.Data.GUID, ""); got.Status != http.StatusOK {
		t.Errorf("delete: %d %s", got.Status, got.Body)
	}
	var left int
	if err := db.QueryRow(ctx, `SELECT COUNT(*) FROM device_groups WHERE guid = ?`, saved.Data.GUID).Scan(&left); err != nil || left != 0 {
		t.Errorf("the group's row is still there: %d, %v", left, err)
	}
	if calls := a.calls(); len(calls) != 0 {
		t.Errorf("calls to the panel grouping: %q", calls)
	}
}
