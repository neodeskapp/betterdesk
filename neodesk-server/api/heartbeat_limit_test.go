package api

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/config"
	"github.com/neodeskapp/neodesk/neodesk-server/db"
	"github.com/neodeskapp/neodesk/neodesk-server/metrics"
	"github.com/neodeskapp/neodesk/neodesk-server/peer"
	"github.com/neodeskapp/neodesk/neodesk-server/ratelimit"
)

// officeServer is a server with n devices that all send from one address.
func officeServer(t *testing.T, n int) (*Server, []string) {
	t.Helper()
	database := testSetupDB(t)
	t.Cleanup(func() { database.Close() })
	ids := make([]string, n)
	for i := range ids {
		ids[i] = fmt.Sprintf("office%03d", i)
		if err := database.UpsertPeer(&db.Peer{ID: ids[i], UUID: "uuid-" + ids[i], Hostname: ids[i]}); err != nil {
			t.Fatal(err)
		}
	}
	srv := New(config.DefaultConfig(), database, peer.NewMap(), nil, "test")
	srv.SetMetrics(metrics.NewCollector())
	return srv, ids
}

// officeHeartbeat sends a plain heartbeat with a CPU value, as a RustDesk
// client does, from the office's one address.
func officeHeartbeat(srv *Server, id string) *httptest.ResponseRecorder {
	body := `{"id":"` + id + `","uuid":"uuid-` + id + `","cpu":12.5,"memory":40,"disk":20}`
	req := httptest.NewRequest(http.MethodPost, "/api/heartbeat", bytes.NewReader([]byte(body)))
	req.RemoteAddr = "203.0.113.7:40000"
	rec := httptest.NewRecorder()
	srv.handleClientHeartbeat(rec, req)
	return rec
}

// TestHeartbeatsOfAnOfficeBehindOneAddress: 30 computers behind one NAT
// each send a minute of heartbeats (one every 15 s). Every one counts:
// the limit is per device, with a much higher ceiling per address. Before,
// 20 requests a minute per address were allowed and the rest dropped
// without a word, so most of the office lost its heartbeats (L498).
func TestHeartbeatsOfAnOfficeBehindOneAddress(t *testing.T) {
	srv, ids := officeServer(t, 30)
	for round := 0; round < 4; round++ {
		for _, id := range ids {
			if rec := officeHeartbeat(srv, id); rec.Code != http.StatusOK {
				t.Fatalf("round %d, %s: %d %s", round, id, rec.Code, rec.Body.String())
			}
		}
	}
	for _, id := range ids {
		rows, err := srv.db.GetPeerMetrics(id, 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != 4 {
			t.Errorf("%s kept %d of its 4 heartbeats", id, len(rows))
		}
	}
}

// TestHeartbeatDropsAreAnsweredLoggedAndCounted: a device over its own
// ceiling gets 429 with Retry-After, the other devices behind the same
// address go on, and the drops show in the exported metrics. A flood from
// one address meets the address ceiling the same way (L498).
func TestHeartbeatDropsAreAnsweredLoggedAndCounted(t *testing.T) {
	srv, ids := officeServer(t, 2)
	noisy, quiet := ids[0], ids[1]
	var last *httptest.ResponseRecorder
	for i := 0; i <= heartbeatsPerDevice; i++ {
		last = officeHeartbeat(srv, noisy)
	}
	if last.Code != http.StatusTooManyRequests || last.Header().Get("Retry-After") == "" {
		t.Fatalf("heartbeat %d of one device in a minute: %d, Retry-After %q", heartbeatsPerDevice+1, last.Code, last.Header().Get("Retry-After"))
	}
	if rec := officeHeartbeat(srv, quiet); rec.Code != http.StatusOK {
		t.Errorf("another device behind the same address: %d %s", rec.Code, rec.Body.String())
	}

	// The address ceiling, made small for the test
	srv.heartbeatLimiter = ratelimit.NewWindowCounter(3, time.Minute)
	for i := 0; i < 3; i++ {
		officeHeartbeat(srv, quiet)
	}
	if rec := officeHeartbeat(srv, quiet); rec.Code != http.StatusTooManyRequests {
		t.Errorf("over the address ceiling: %d", rec.Code)
	}

	var out strings.Builder
	srv.metrics.WritePrometheus(&out)
	for _, want := range []string{
		`neodesk_client_api_dropped_total{limit="device"} 1`,
		`neodesk_client_api_dropped_total{limit="address"} 1`,
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("metrics lack %s:\n%s", want, out.String())
		}
	}
}
