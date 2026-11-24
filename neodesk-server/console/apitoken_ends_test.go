package console

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/api"
	"github.com/neodeskapp/neodesk/neodesk-server/config"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
	"github.com/neodeskapp/neodesk/neodesk-server/peer"
)

// madeWorld is a console with its API server on the same database, so a
// token's calls make real guest links, codes and device tokens.
func madeWorld(t *testing.T, db *store.DB) *world {
	t.Helper()
	gdb := storetest.GoServer(t, db)
	apiSrv := api.New(config.DefaultConfig(), gdb, peer.NewMap(), nil, "test")
	w := newWorld(t, setup{db: db, api: apiSrv.InternalHandler()})
	if _, err := db.Exec(context.Background(), `INSERT INTO organizations (id, name, slug) VALUES ('org-1', 'Org', 'org')`); err != nil {
		t.Fatal(err)
	}
	return w
}

// made is what one caller made: a guest link's token, and the names that
// tell its device token, support session, invite, pairing code and
// organization invitation apart.
type made struct {
	guestToken string
	sessionID  string
	label      string
	email      string
}

// makeEverything makes one of each credential a token can make that
// outlives a request, as the caller a, labelled with label.
func makeEverything(t *testing.T, a *agent, label string) made {
	t.Helper()
	post := func(path, body string) map[string]any {
		t.Helper()
		var res answer
		if a.always != nil {
			res = a.postJSON(path, body)
		} else {
			res = a.post(path, body)
		}
		if res.status != http.StatusOK && res.status != http.StatusCreated {
			t.Fatalf("%s as %s: %d %s", path, label, res.status, res.body)
		}
		return res.json(t)
	}
	out := made{label: label, email: strings.ReplaceAll(label, " ", "-") + "@example.com"}
	link := post("/api/guest/access-links", `{"peer_ids":["111222333"],"ttl_minutes":30,"label":"`+label+`"}`)
	out.guestToken, _ = link["token"].(string)
	post("/api/panel/tokens", `{"name":"`+label+`","max_uses":1}`)
	sess := post("/api/support/sessions", `{"name":"`+label+`"}`)
	if s, ok := sess["session"].(map[string]any); ok {
		out.sessionID, _ = s["id"].(string)
	}
	post("/api/onboard/invites", `{"name":"`+label+`"}`)
	post("/api/panel/pairing/codes", `{"label":"`+label+`"}`)
	post("/api/panel/org/org-1/invite", `{"email":"`+out.email+`"}`)
	if out.guestToken == "" || out.sessionID == "" {
		t.Fatalf("no guest link token or support session for %s", label)
	}
	return out
}

// open reports which of the credentials of m still work, by kind.
func (w *world) open(m made) map[string]bool {
	w.t.Helper()
	ctx := context.Background()
	out := map[string]bool{}
	admin := w.agent().signIn("super_admin")

	res := admin.get("/api/guest/access-links/validate?token=" + url.QueryEscape(m.guestToken))
	out["guest link"] = res.status == http.StatusOK && strings.Contains(res.body, `"valid":true`)

	var status string
	if err := w.db.QueryRow(ctx, `SELECT status FROM device_tokens WHERE name = ?`, m.label).Scan(&status); err != nil {
		w.t.Fatalf("device token %s: %v", m.label, err)
	}
	out["device token"] = status != "revoked" && status != "expired"

	res = admin.get("/api/support/sessions/" + m.sessionID)
	var sess struct {
		Session struct {
			Status string `json:"status"`
		} `json:"session"`
	}
	_ = json.Unmarshal([]byte(res.body), &sess)
	out["support session"] = sess.Session.Status == "waiting"

	codes := map[string]bool{}
	rows, err := w.db.Query(ctx, `SELECT value FROM server_config WHERE key LIKE 'pairing_code_%'`)
	if err != nil {
		w.t.Fatal(err)
	}
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			w.t.Fatal(err)
		}
		var rec struct {
			Kind  string `json:"kind"`
			Name  string `json:"name"`
			Label string `json:"label"`
		}
		_ = json.Unmarshal([]byte(raw), &rec)
		switch {
		case rec.Kind == "join" && rec.Name == m.label:
			codes["invite"] = true
		case rec.Kind == "support" && rec.Name == m.label:
			codes["support code"] = true
		case rec.Kind == "" && rec.Label == m.label:
			codes["pairing code"] = true
		}
	}
	rows.Close()
	out["invite"], out["pairing code"] = codes["invite"], codes["pairing code"]
	if out["support session"] != codes["support code"] {
		w.t.Errorf("%s: the support session is open %v, its code %v", m.label, out["support session"], codes["support code"])
	}

	var unused int
	if err := w.db.QueryRow(ctx, `SELECT COUNT(*) FROM org_invitations WHERE email = ? AND used_at IS NULL`, m.email).Scan(&unused); err != nil {
		w.t.Fatal(err)
	}
	out["organization invitation"] = unused == 1
	return out
}

var madeKinds = []string{"guest link", "device token", "support session", "invite", "pairing code", "organization invitation"}

// Revoking a token ends the guest links, device tokens, support sessions,
// invites, pairing codes and organization invitations it made, in one
// go, with one audit entry that counts them. What the same account made
// in its browser stays.
func TestRevokingATokenEndsWhatItMade(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) { testRevokingATokenEndsWhatItMade(t, madeWorld(t, db)) })
}

func testRevokingATokenEndsWhatItMade(t *testing.T, w *world) {
	value, id := w.makeToken("super_admin", `{"name":"script","permissions":`+allAdmin+`,"read_only":false,"expires_in_days":1}`)
	byToken := makeEverything(t, w.bearer(value), "by token")
	byBrowser := makeEverything(t, w.agent().signIn("super_admin"), "by browser")
	for kind, ok := range w.open(byToken) {
		if !ok {
			t.Fatalf("the token's %s does not work before the revoke", kind)
		}
	}

	admin := w.agent().signIn("super_admin")
	res := admin.do("DELETE", "/api/panel/api-tokens/"+id, "", "X-CSRF-Token", admin.csrf())
	if res.status != http.StatusOK {
		t.Fatalf("revoke: %d %s", res.status, res.body)
	}
	tokenOpen, browserOpen := w.open(byToken), w.open(byBrowser)
	for _, kind := range madeKinds {
		if tokenOpen[kind] {
			t.Errorf("the %s made with the token still works after the revoke", kind)
		}
		if !browserOpen[kind] {
			t.Errorf("the %s made in the browser by the same account ended with the token", kind)
		}
	}
	want := "api_token_revoked|API token script (ndt_" + id + "_…) of e2e_super_admin revoked. Ended with it: 1 guest link, 1 device token, 1 support session, 1 invite, 1 pairing code, 1 organization invitation"
	if audit := strings.Join(w.auditRows(), "\n"); strings.Count(audit, "api_token_revoked|") != 1 || !strings.Contains(audit, want) {
		t.Errorf("one revoke entry with the counts, want %q:\n%s", want, audit)
	}
}

// The token list says what a token made that is still open, so the
// revoke dialog can say what ends with it; the revoke's answer says what
// ended.
func TestTheTokenListCountsWhatEndsWithIt(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) { testTheTokenListCountsWhatEndsWithIt(t, madeWorld(t, db)) })
}

func testTheTokenListCountsWhatEndsWithIt(t *testing.T, w *world) {
	value, id := w.makeToken("super_admin", `{"name":"script","permissions":`+allAdmin+`,"read_only":false,"expires_in_days":1}`)
	makeEverything(t, w.bearer(value), "by token")
	makeEverything(t, w.agent().signIn("super_admin"), "by browser")
	counts := `{"guest_links":1,"device_tokens":1,"support_sessions":1,"invites":1,"pairing_codes":1,"share_links":0,"organization_invitations":1}`
	admin := w.agent().signIn("super_admin")
	if res := admin.get("/api/panel/api-tokens"); !strings.Contains(res.body, `"ends_with_it":`+counts) {
		t.Errorf("the list does not count what ends with the token, want %s:\n%s", counts, res.body)
	}
	res := admin.do("DELETE", "/api/panel/api-tokens/"+id, "", "X-CSRF-Token", admin.csrf())
	if !strings.Contains(res.body, `"ended":`+counts) {
		t.Errorf("the revoke does not say what ended, want %s:\n%s", counts, res.body)
	}
	if res := admin.get("/api/panel/api-tokens"); !strings.Contains(res.body, `"ends_with_it":{"guest_links":0,`) {
		t.Errorf("a revoked token still has open credentials in the list:\n%s", res.body)
	}
}

// A token that expires ends what it made, as a revoke does, soon after
// its expiry, with one audit entry.
func TestAnExpiredTokenEndsWhatItMade(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) { testAnExpiredTokenEndsWhatItMade(t, madeWorld(t, db)) })
}

func testAnExpiredTokenEndsWhatItMade(t *testing.T, w *world) {
	if err := w.s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	value, id := w.makeToken("super_admin", `{"name":"short","permissions":`+allAdmin+`,"read_only":false,"expires_in_days":1}`)
	byToken := makeEverything(t, w.bearer(value), "by token")
	// The token runs out in two seconds; the next token made tells the
	// sweep, which then wakes at that expiry
	if _, err := w.db.Exec(context.Background(), `UPDATE admin_tokens SET expires_at = ? WHERE public_id = ?`, time.Now().Add(2*time.Second).UnixMilli(), id); err != nil {
		t.Fatal(err)
	}
	w.makeToken("super_admin", `{"name":"other","permissions":["audit.view"],"expires_in_days":1}`)
	deadline := time.Now().Add(20 * time.Second)
	for {
		open := w.open(byToken)
		ended := 0
		for _, kind := range madeKinds {
			if !open[kind] {
				ended++
			}
		}
		if ended == len(madeKinds) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("what the token made still works 10 s after it expired: %v", open)
		}
		time.Sleep(250 * time.Millisecond)
	}
	want := "api_token_expired|API token short (ndt_" + id + "_…) of e2e_super_admin expired. Ended with it: 1 guest link, 1 device token, 1 support session, 1 invite, 1 pairing code, 1 organization invitation"
	if audit := strings.Join(w.auditRows(), "\n"); !strings.Contains(audit, want) {
		t.Errorf("no expiry entry with the counts, want %q:\n%s", want, audit)
	}
}
