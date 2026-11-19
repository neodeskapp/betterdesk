package dashboard

import (
	"context"
	"errors"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/console/serverinfo"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

const (
	signedOut   = `{"success":false,"error":"Unauthorized. Please log in.","code":"session_required"}`
	serverFault = `{"success":false,"error":"Server error"}`
	health      = `{"status":"ok","version":"3.5.224","uptime":"1h2m3s","peers_online":14}`
)

func TestRoutesInTheOrderOfTheOldFile(t *testing.T) {
	w := newWorld(t, nil, nil)
	var got []string
	for _, route := range w.router.Routes() {
		got = append(got, route.Method+" "+route.Path)
	}
	want := "GET /api/stats,GET /api/server/status,GET /api/dashboard/client-config,GET /api/server/bandwidth,POST /api/sync-status,GET /api/dashboard/activity,GET /signin/{role}"
	if strings.Join(got, ",") != want {
		t.Errorf("routes = %v, want %s", got, want)
	}
}

func TestEveryRouteNeedsASession(t *testing.T) {
	w := newWorld(t, nil, map[string]string{"id_ed25519.pub": validKey})
	w.api.set("/api/health", health)
	w.api.set("/api/server/stats", `{"peers_total":1}`)
	w.api.set("/api/audit/events", `{"events":[]}`)
	requests := [][2]string{
		{"GET", "/api/stats"}, {"GET", "/api/server/status"}, {"GET", "/api/dashboard/client-config"},
		{"GET", "/api/server/bandwidth"}, {"POST", "/api/sync-status"}, {"GET", "/api/dashboard/activity"},
	}
	for _, req := range requests {
		want(t, req[0]+" "+req[1]+" signed out", w.do("", req[0], req[1]), 401, signedOut)
	}
	if calls := w.api.seen(); len(calls) != 0 {
		t.Errorf("requests without a session reached the API server: %v", calls)
	}
	for _, req := range requests {
		if rec := w.do("viewer", req[0], req[1]); rec.Code != 200 {
			t.Errorf("%s %s as viewer: %d %s", req[0], req[1], rec.Code, rec.Body.String())
		}
	}
}

// TestStats answers the device counts, the server's health, the name of
// the backend and whether there is a public key (BUGS SI11: the backend
// was always an empty object).
func TestStats(t *testing.T) {
	w := newWorld(t, nil, map[string]string{"id_ed25519.pub": validKey})
	w.api.set("/api/health", health)
	w.api.set("/api/server/stats", `{"peers_total":30,"peers_online":14,"peers_banned":2,"relay_active_sessions":1}`)
	want(t, "stats", w.get("viewer", "/api/stats"), 200,
		`{"success":true,"data":{"devices":{"total":30,"online":14,"offline":16,"banned":2,"withNotes":0},`+
			`"hbbs":{"status":"running","version":"3.5.224","uptime":"1h2m3s","peers_online":14,"backend":"betterdesk"},`+
			`"backend":"betterdesk","publicKey":true}}`)

	// No key in the file and none from the server: publicKey is false.
	// A server that does not answer its health check is "unreachable".
	w = newWorld(t, nil, nil)
	w.api.set("/api/server/stats", `{"total":3,"online":"1","with_notes":2}`)
	w.api.setStatus("/api/health", 503, `{"error":"down"}`)
	want(t, "no key, no health", w.get("viewer", "/api/stats"), 200,
		`{"success":true,"data":{"devices":{"total":3,"online":"1","offline":2,"banned":0,"withNotes":2},`+
			`"hbbs":{"status":"unreachable","backend":"betterdesk","error":"Request failed with status code 503"},`+
			`"backend":"betterdesk","publicKey":false}}`)
}

func TestStatsFromTheConsoleTablesWhenTheServerGivesNone(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := newWorld(t, db, nil)
		w.api.set("/api/health", health)
		w.api.setStatus("/api/server/stats", 500, `{"error":"no"}`)
		ctx := context.Background()
		yes, no := store.BoolArg(db.Shape().Table("peer").Type("is_deleted"), true), store.BoolArg(db.Shape().Table("peer").Type("is_deleted"), false)
		for _, p := range []struct {
			id                      string
			online, deleted, banned any
		}{
			{"1", yes, no, no}, {"2", yes, no, no}, {"3", no, no, no}, {"4", no, no, yes},
			{"5", yes, no, yes}, {"6", yes, yes, no}, {"7", no, yes, yes},
		} {
			if _, err := db.Exec(ctx, "INSERT INTO peer (id, status_online, is_deleted, is_banned) VALUES (?, ?, ?, ?)", p.id, p.online, p.deleted, p.banned); err != nil {
				t.Fatal(err)
			}
		}
		rec := w.get("viewer", "/api/stats")
		if !strings.HasPrefix(rec.Body.String(), `{"success":true,"data":{"devices":{"total":5,"online":3,"banned":2,"offline":2},"hbbs":{`) {
			t.Errorf("counts from the console table: %d %s", rec.Code, rec.Body.String())
		}

		if _, err := db.Exec(ctx, "ALTER TABLE peer RENAME TO peer_gone"); err != nil {
			t.Fatal(err)
		}
		want(t, "no table to count", w.get("viewer", "/api/stats"), 500, serverFault)
	})
}

// refuse is a dialer nothing listens behind.
func refuse(context.Context, string, string) (net.Conn, error) {
	return nil, errors.New("connection refused")
}

func TestServerStatus(t *testing.T) {
	w := newWorld(t, nil, nil)
	w.api.set("/api/health", health)
	w.h.dial = func(context.Context, string, string) (net.Conn, error) {
		t.Error("a port was probed although the API answers")
		return nil, errors.New("no")
	}
	w.h.now = func() time.Time { return w.h.started.Add(125*time.Second + 900*time.Millisecond) }
	want(t, "running", w.get("viewer", "/api/server/status"), 200,
		`{"success":true,"data":{"backend":"betterdesk","uptime":125,"hbbs":{"status":"running"},"hbbr":{"status":"running"},"signal":{"status":"running"},`+
			`"api_port":21114,"signal_port":21116,"relay_port":21117,"nat_port":21115,"ws_signal_port":21118,"ws_relay_port":21119,"client_api_port":21121,"console_port":5000}}`)

	// The ports follow the configuration
	w.cfg.Port, w.cfg.APIURL, w.cfg.APIEnabled = 8443, "http://127.0.0.1:21121/api", false
	w.cfg.SignalPort, w.cfg.RelayPort = 31116, 41117
	w.h.now = func() time.Time { return w.h.started }
	want(t, "other ports", w.get("viewer", "/api/server/status"), 200,
		`{"success":true,"data":{"backend":"betterdesk","uptime":0,"hbbs":{"status":"running"},"hbbr":{"status":"running"},"signal":{"status":"running"},`+
			`"api_port":21121,"signal_port":31116,"relay_port":41117,"nat_port":31115,"ws_signal_port":31118,"ws_relay_port":41119,"client_api_port":21121,"console_port":8443}}`)
}

func TestServerStatusAPIPort(t *testing.T) {
	w := newWorld(t, nil, nil)
	w.api.set("/api/health", health)
	for _, c := range []struct {
		url           string
		goAPIPort     int
		enabled       bool
		api, clientAP int
	}{
		{"http://127.0.0.1:21114/api", 9, true, 21114, 21121},
		{"http://127.0.0.1:21114/api", 9, false, 21114, 21114},
		// An address without a port of its own: the configured API port
		{"http://server/api", 7000, false, 7000, 7000},
		{"https://server:443/api", 7000, false, 7000, 7000},
		{"http://server:0/api", 7000, false, 7000, 7000},
		{"http://server/api", 0, false, 21114, 21114},
		{"http://server/api", 0, true, 21114, 21121},
		{" HTTPS://server:8443/api ", 7000, false, 8443, 8443},
	} {
		w.cfg.APIURL, w.cfg.GoAPIPort, w.cfg.APIEnabled = c.url, c.goAPIPort, c.enabled
		body := w.get("viewer", "/api/server/status").Body.String()
		if !strings.Contains(body, `"api_port":`+strconv.Itoa(c.api)+`,`) || !strings.Contains(body, `"client_api_port":`+strconv.Itoa(c.clientAP)+`,`) {
			t.Errorf("%q with API port %d, enabled %v: %s", c.url, c.goAPIPort, c.enabled, body)
		}
	}

	// An address that is no URL fails the request
	for _, url := range []string{"127.0.0.1:21114/api", "not a url", "http://server:99999/api", ""} {
		w.cfg.APIURL = url
		want(t, "address "+url, w.get("viewer", "/api/server/status"), 500, serverFault)
	}
}

func TestServerStatusProbesThePortsWhenTheAPIIsDown(t *testing.T) {
	w := newWorld(t, nil, nil)
	w.api.setStatus("/api/health", 500, `{"error":"down"}`)
	w.cfg.RelayHost, w.cfg.RelayPort, w.cfg.SignalHost, w.cfg.SignalPort = "relay.test", 41117, "signal.test", 31116

	var dialed []string
	var deadlines []time.Duration
	answers := map[string]bool{}
	w.h.dial = func(ctx context.Context, network, address string) (net.Conn, error) {
		dialed = append(dialed, network+" "+address)
		deadline, ok := ctx.Deadline()
		if !ok {
			t.Errorf("the probe of %s has no deadline", address)
		}
		deadlines = append(deadlines, time.Until(deadline))
		if !answers[address] {
			return nil, errors.New("connection refused")
		}
		client, server := net.Pipe()
		_ = server.Close()
		return client, nil
	}
	status := func() string {
		body := w.get("viewer", "/api/server/status").Body.String()
		start, end := strings.Index(body, `"hbbs"`), strings.Index(body, `,"api_port"`)
		return body[start:end]
	}

	if got := status(); got != `"hbbs":{"status":"stopped"},"hbbr":{"status":"stopped"},"signal":{"status":"stopped"}` {
		t.Errorf("nothing listens: %s", got)
	}
	// The relay is asked first
	if strings.Join(dialed, ",") != "tcp relay.test:41117,tcp signal.test:31116" {
		t.Errorf("probes = %v", dialed)
	}
	for _, left := range deadlines {
		if left <= time.Second || left > 2*time.Second {
			t.Errorf("a probe has %v, want two seconds", left)
		}
	}

	answers["relay.test:41117"] = true
	if got := status(); got != `"hbbs":{"status":"stopped"},"hbbr":{"status":"running"},"signal":{"status":"stopped"}` {
		t.Errorf("the relay listens: %s", got)
	}
	answers["relay.test:41117"], answers["signal.test:31116"] = false, true
	if got := status(); got != `"hbbs":{"status":"stopped"},"hbbr":{"status":"stopped"},"signal":{"status":"running"}` {
		t.Errorf("the signal server listens: %s", got)
	}

	// A number that is no port is not probed
	dialed = nil
	w.cfg.RelayPort, w.cfg.SignalPort = 70000, -5
	if got := status(); got != `"hbbs":{"status":"stopped"},"hbbr":{"status":"unknown"},"signal":{"status":"unknown"}` {
		t.Errorf("numbers that are no ports: %s", got)
	}
	if len(dialed) != 0 {
		t.Errorf("probes = %v", dialed)
	}

	// The probes come before the address check
	w.cfg.RelayPort, w.cfg.SignalPort, w.cfg.APIURL = 41117, 31116, "not a url"
	want(t, "address that is no URL", w.get("viewer", "/api/server/status"), 500, serverFault)
	if len(dialed) != 2 {
		t.Errorf("probes before the failure = %v", dialed)
	}
}

func TestClientConfig(t *testing.T) {
	w := newWorld(t, nil, map[string]string{"id_ed25519.pub": validKey})
	expect := func(host, source string, unreachable bool) string {
		payload := serverinfo.ConfigPayload{Host: host, Relay: host + ":21117", API: "http://" + host + ":21114", Key: validKey}
		flag := "false"
		if unreachable {
			flag = "true"
		}
		return `{"success":true,"data":{"server_id":"` + host + `","relay_server":"` + host + `:21117","api_url":"http://` + host + `:21114",` +
			`"public_key":"` + validKey + `","has_public_key":true,` +
			`"deploy_config_string":"` + serverinfo.EncodeRustDeskCliConfigString(payload) + `","config_uri":"` + serverinfo.EncodeRustDeskConfigURI(payload) + `",` +
			`"client_server_host":"` + host + `","endpoint_sources":{"host":"` + source + `","relay":"` + source + `","api":"` + source + `"},` +
			`"env_override_active":false,"phone_unreachable_host":` + flag + `,"qr":"qr:` + serverinfo.EncodeRustDeskConfigURI(payload) + `"}}`
	}
	want(t, "default", w.get("viewer", "/api/dashboard/client-config"), 200, expect("127.0.0.1", "config", true))
	want(t, "for a host", w.get("viewer", "/api/dashboard/client-config?host=desk.example.org"), 200, expect("desk.example.org", "query", false))
	want(t, "for a .local name", w.get("viewer", "/api/dashboard/client-config?host=nas.local"), 200, expect("nas.local", "query", true))
	for _, query := range []string{"host=a.example&host=b.example", "host[]=a.example", "host%5Bx%5D=a.example", "host=bad%20host", "host="} {
		want(t, query, w.get("viewer", "/api/dashboard/client-config?"+query), 200, expect("127.0.0.1", "config", true))
	}

	// Without a key the fields stay and the QR code is null
	w = newWorld(t, nil, nil)
	body := w.get("viewer", "/api/dashboard/client-config").Body.String()
	if !strings.HasPrefix(body, `{"success":true,"data":{"server_id":"127.0.0.1","relay_server":"127.0.0.1:21117","api_url":"http://127.0.0.1:21114","public_key":"","has_public_key":false,`) ||
		!strings.HasSuffix(body, `"env_override_active":false,"phone_unreachable_host":true,"qr":null}}`) {
		t.Errorf("without a key: %s", body)
	}
}

func TestBandwidth(t *testing.T) {
	w := newWorld(t, nil, nil)
	zeros := `{"success":true,"data":{"relay_active":0,"total_relayed":0,"bytes_transferred":0,"active_sessions":0,"throttle_hits":0}}`

	w.api.set("/api/server/stats", `{"relay_active_sessions":2,"relay_total_relayed":17,"bandwidth_bytes_transferred":123456789012,"bandwidth_active_sessions":1,"bandwidth_throttle_hits":4,"peers_total":9}`)
	want(t, "counters", w.get("viewer", "/api/server/bandwidth"), 200,
		`{"success":true,"data":{"relay_active":2,"total_relayed":17,"bytes_transferred":123456789012,"active_sessions":1,"throttle_hits":4}}`)
	if calls := w.api.seen(); len(calls) != 1 || calls[0] != "GET /api/server/stats" {
		t.Errorf("calls = %v", calls)
	}

	// What the server sends is handed on unless it counts as nothing
	w.api.set("/api/server/stats", `{"relay_active_sessions":"3","relay_total_relayed":null,"bandwidth_bytes_transferred":false,"bandwidth_active_sessions":"","bandwidth_throttle_hits":[1]}`)
	want(t, "other types", w.get("viewer", "/api/server/bandwidth"), 200,
		`{"success":true,"data":{"relay_active":"3","total_relayed":0,"bytes_transferred":0,"active_sessions":0,"throttle_hits":[1]}}`)

	for _, body := range []string{`{}`, `[]`, `"text"`, `null`, `7`, `{"error":"no statistics"}`} {
		w.api.set("/api/server/stats", body)
		want(t, "server says "+body, w.get("viewer", "/api/server/bandwidth"), 200, zeros)
	}
	w.api.setStatus("/api/server/stats", 500, `{"relay_active_sessions":2}`)
	want(t, "server fails", w.get("viewer", "/api/server/bandwidth"), 200, zeros)
}

func TestSyncStatusSyncsNothing(t *testing.T) {
	w := newWorld(t, nil, nil)
	want(t, "sync", w.do("viewer", "POST", "/api/sync-status"), 200,
		`{"success":true,"data":{"synced":0,"skipped":true,"reason":"betterdesk_manages_state"}}`)
	if calls := w.api.seen(); len(calls) != 0 {
		t.Errorf("the sync called the API server: %v", calls)
	}
}
