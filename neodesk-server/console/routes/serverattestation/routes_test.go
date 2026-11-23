package serverattestation_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/routetest"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// keys lists the keys of a JSON object in the order they came.
func keys(t *testing.T, body string) []string {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(body))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		t.Fatalf("not an object: %s", body)
	}
	var out []string
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, tok.(string))
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			t.Fatal(err)
		}
	}
	return out
}

// The routes need a session and the permission server.attestation, and
// the panel access of the section; the public badge needs nothing.
func TestGuards(t *testing.T) {
	w := routetest.New(t, routetest.Options{})
	out := w.SignedOut()
	sectionDenied := `{"success":false,"error":"Panel access denied: server-attestation","code":"panel_access_denied"}`
	for _, path := range []string{"/api/server-attestation/status", "/api/server-attestation/result", "/api/server-attestation/badge-config"} {
		if got := out.Do(http.MethodGet, path, ""); got.Status != http.StatusUnauthorized {
			t.Errorf("signed out GET %s = %d %s", path, got.Status, got.Body)
		}
		if got := w.As("viewer").Do(http.MethodGet, path, ""); got.Status != http.StatusForbidden {
			t.Errorf("viewer GET %s = %d %s", path, got.Status, got.Body)
		}
		if got := w.As("server_admin").Do(http.MethodGet, path, ""); got.Status != http.StatusOK {
			t.Errorf("server_admin GET %s = %d %s", path, got.Status, got.Body)
		}
		// The section asks server.attestation for reading and writing
		// (PA5, F9): global_admin, who has branding.edit only, is refused
		// by the panel access guard
		if got := w.As("global_admin").Do(http.MethodGet, path, ""); got.Status != http.StatusForbidden ||
			got.Body != sectionDenied {
			t.Errorf("global_admin GET %s = %d %s", path, got.Status, got.Body)
		}
	}
	for _, c := range []struct{ method, path, body string }{
		{http.MethodPost, "/api/server-attestation/abort", "{}"},
		{http.MethodPut, "/api/server-attestation/badge-config", `{"badgeTitle":"x"}`},
	} {
		if got := w.As("global_admin").Do(c.method, c.path, c.body); got.Status != http.StatusForbidden ||
			got.Body != sectionDenied {
			t.Errorf("global_admin %s %s = %d %s", c.method, c.path, got.Status, got.Body)
		}
	}
	for _, path := range []string{"/api/server-attestation/run", "/api/server-attestation/abort"} {
		if got := out.Do(http.MethodPost, path, "{}"); got.Status != http.StatusUnauthorized {
			t.Errorf("signed out POST %s = %d", path, got.Status)
		}
	}
	// server_admin, who has server.attestation, may change it now that the
	// section's write permission is server.attestation (PA5, F9)
	if got := w.As("server_admin").Do(http.MethodPost, "/api/server-attestation/abort", "{}"); got.Status != http.StatusOK ||
		got.Body != `{"success":true,"aborted":false}` {
		t.Errorf("server_admin POST abort = %d %s", got.Status, got.Body)
	}
	if got := w.As("operator").Do(http.MethodPut, "/api/server-attestation/badge-config", `{"badgeTitle":"x"}`); got.Status != http.StatusForbidden {
		t.Errorf("operator PUT = %d", got.Status)
	}
	// The panel access guard lets a browser without a session through;
	// the route's own guard answers
	if got := out.Do(http.MethodPut, "/api/server-attestation/badge-config", `{"badgeTitle":"x"}`); got.Status != http.StatusUnauthorized {
		t.Errorf("signed out PUT = %d %s", got.Status, got.Body)
	}
	var saved int
	if err := w.DB.QueryRow(context.Background(), "SELECT COUNT(*) FROM settings WHERE key = 'server_attestation_badge_config'").Scan(&saved); err != nil || saved != 0 {
		t.Errorf("badge texts saved by a refused request: %d %v", saved, err)
	}
	if got := out.Do(http.MethodGet, "/api/public/server-attestation", ""); got.Status != http.StatusOK {
		t.Errorf("signed out public = %d %s", got.Status, got.Body)
	}
	if got := w.As("admin").Do(http.MethodPost, "/api/server-attestation/abort", "{}"); got.Body != `{"success":true,"aborted":false}` {
		t.Errorf("abort without a test = %s", got.Body)
	}
	if rows := w.AuditRows(); len(rows) != 0 {
		t.Errorf("audit rows %q", rows)
	}
}

// Saved badge texts reach the badge routes and the public badge, the row
// holds them cleaned, and the audit log notes the save with the account
// and the address.
func TestBadgeTextsChain(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := routetest.New(t, routetest.Options{DB: db})
		admin := w.As("admin")
		got := admin.Do(http.MethodPut, "/api/server-attestation/badge-config", `{"badgeTitle":"  Ours\u0001 ","tierLabels":{"iron":"Eisen","x":"y"}}`)
		want := `{"success":true,"config":{"badgeTitle":"Ours","tierLabels":{"bronze":"","iron":"Eisen","platinum":"","titanium":"","obsidian":""}},` +
			`"presentation":{"brand":"NeoDesk","badgeTitle":"Ours","tierLabels":{"bronze":"BRONZE","iron":"Eisen","platinum":"PLATINUM","titanium":"TITANIUM","obsidian":"OBSIDIAN"},"tierLabel":null}}`
		if got.Status != http.StatusOK || got.Body != want {
			t.Fatalf("PUT = %d %s", got.Status, got.Body)
		}
		var stored string
		if err := db.QueryRow(context.Background(), "SELECT value FROM settings WHERE key = 'server_attestation_badge_config'").Scan(&stored); err != nil {
			t.Fatal(err)
		}
		if stored != `{"badgeTitle":"Ours","tierLabels":{"bronze":"","iron":"Eisen","platinum":"","titanium":"","obsidian":""}}` {
			t.Errorf("stored %s", stored)
		}
		if rows := w.AuditRows(); len(rows) != 1 || rows[0] != "server_attestation_badge_updated|Updated Server Attestation badge labels|127.0.0.1|1" {
			t.Errorf("audit rows %q", rows)
		}
		if got := w.As("server_admin").Do(http.MethodGet, "/api/server-attestation/badge-config", ""); !strings.Contains(got.Body, `"config":{"badgeTitle":"Ours"`) {
			t.Errorf("GET after PUT = %s", got.Body)
		}
		if got := w.SignedOut().Do(http.MethodGet, "/api/public/server-attestation", ""); got.Body != `{"tier":null,"maxConnections":0,"testedAt":null,"valid":false,"brand":"NeoDesk","badgeTitle":"Ours",`+
			`"tierLabels":{"bronze":"BRONZE","iron":"Eisen","platinum":"PLATINUM","titanium":"TITANIUM","obsidian":"OBSIDIAN"}}` {
			t.Errorf("public = %s", got.Body)
		}
		if got := admin.Do(http.MethodPut, "/api/server-attestation/badge-config", `[{"badgeTitle":"x"}]`); got.Status != http.StatusBadRequest || got.Body != `{"success":false,"error":"Invalid badge configuration"}` {
			t.Errorf("PUT of an array = %d %s", got.Status, got.Body)
		}
		if got := admin.Do(http.MethodPut, "/api/server-attestation/badge-config", `{"badgeTitle":{"toString":1}}`); got.Status != http.StatusInternalServerError ||
			got.Body != `{"success":false,"error":"Cannot convert object to primitive value"}` {
			t.Errorf("PUT of a title without text = %d %s", got.Status, got.Body)
		}
		if rows := w.AuditRows(); len(rows) != 1 {
			t.Errorf("refused saves were audited: %q", rows)
		}
	})
}

// The public badge of a stored result gives out the tier, the number of
// connections, the time and the validity with the badge texts, and
// nothing else the result holds; the sign-in page shows the same tier.
func TestPublicBadgeGivesOutNoMore(t *testing.T) {
	w := routetest.New(t, routetest.Options{})
	result := `{"valid":true,"mode":"loadtest","tier":"titanium","maxConnections":250,"signalConnections":150,"relaySessions":50,` +
		`"threshold":80,"testedAt":"2026-11-10T09:00:00.000Z","completedAt":"2026-11-10T09:00:00.000Z","durationMs":95000,"aborted":false,` +
		`"metricsHost":"internal-host-7","finalMetrics":{"cpu":81},"baseline":{"hostname":"internal-host-7","goStats":{"peers_total":9}},` +
		`"rampSteps":[{"cpu":12}],"targets":{"signal":"ws://10.1.2.3:21118"},"probeLog":[{"url":"ws://10.1.2.3:21118","ok":true}],` +
		`"errorRate":0,"diskBaselineHigh":false,"wsResolved":true}`
	if _, err := w.DB.Exec(context.Background(), "INSERT INTO settings (key, value) VALUES ('server_attestation_result', ?)", result); err != nil {
		t.Fatal(err)
	}
	got := w.SignedOut().Do(http.MethodGet, "/api/public/server-attestation", "")
	want := `{"tier":"titanium","maxConnections":250,"testedAt":"2026-11-10T09:00:00.000Z","valid":true,"brand":"NeoDesk",` +
		`"badgeTitle":"NeoDesk Server Attestation","tierLabel":"TITANIUM",` +
		`"tierLabels":{"bronze":"BRONZE","iron":"IRON","platinum":"PLATINUM","titanium":"TITANIUM","obsidian":"OBSIDIAN"}}`
	if got.Body != want {
		t.Errorf("public:\n got %s\nwant %s", got.Body, want)
	}
	if k := strings.Join(keys(t, got.Body), ","); k != "tier,maxConnections,testedAt,valid,brand,badgeTitle,tierLabel,tierLabels" {
		t.Errorf("keys %s", k)
	}
	for _, private := range []string{"internal-host", "10.1.2.3", "peers_total", "rampSteps", "durationMs", "mode", "loadtest"} {
		if strings.Contains(got.Body, private) {
			t.Errorf("the public badge gives out %q", private)
		}
	}
	if got.Header.Get("Cache-Control") != "no-cache, no-store, must-revalidate" {
		t.Errorf("Cache-Control %q", got.Header.Get("Cache-Control"))
	}
	page := w.SignedOut().Do(http.MethodGet, "/api/auth/login-page", "")
	if !strings.Contains(page.Body, `"attestation":{"tier":"titanium","brand":"NeoDesk","badgeTitle":"NeoDesk Server Attestation","tierLabel":"TITANIUM"}`) {
		t.Errorf("sign-in page: %s", page.Body)
	}
	if got := w.As("admin").Do(http.MethodGet, "/api/server-attestation/result", ""); got.Body != `{"success":true,"result":`+result+`}` {
		t.Errorf("result = %s", got.Body)
	}
	status := w.As("admin").Do(http.MethodGet, "/api/server-attestation/status", "")
	if status.Body != `{"success":true,"running":false,"phase":"idle","progress":null,"lastResult":`+result+`}` {
		t.Errorf("status = %s", status.Body)
	}
}

// /tenants sends everyone to /organizations.
func TestTenantsRedirect(t *testing.T) {
	w := routetest.New(t, routetest.Options{})
	for _, path := range []string{"/tenants", "/TENANTS/", "/tenants?tab=x"} {
		got := w.SignedOut().Do(http.MethodGet, path, "", "Accept", "text/html")
		if got.Status != http.StatusFound || got.Header.Get("Location") != "/organizations" || got.Body != "<p>Found. Redirecting to /organizations</p>" {
			t.Errorf("GET %s = %d %q %q", path, got.Status, got.Header.Get("Location"), got.Body)
		}
	}
}
