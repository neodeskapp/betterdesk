package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/audit"
	"github.com/unitronix/betterdesk-server/peer"
	"github.com/unitronix/betterdesk-server/ratelimit"
)

// TestMetricsCountWhatHappened: the registration, expiry, throttle and
// audit counters and the banned devices on /metrics follow what happened
// on the server, and the relay byte counter, which nothing counts, is not
// offered (BUGS L224).
func TestMetricsCountWhatHappened(t *testing.T) {
	srv, database := newInternalServer(t, nil)

	srv.peers.Put(&peer.Entry{ID: "REGOLD1", LastReg: time.Now().Add(-time.Hour)})
	srv.peers.Put(&peer.Entry{ID: "REGNEW2", LastReg: time.Now()})
	srv.peers.CleanExpired(time.Minute)

	// A global budget of one byte a second: the second read is throttled
	bw := ratelimit.NewBandwidthLimiter(1, 1<<20)
	srv.SetBandwidthLimiter(bw)
	r := bw.WrapReader(strings.NewReader("relayed bytes"))
	buf := make([]byte, 4)
	_, _ = r.Read(buf)
	_, _ = r.Read(buf)
	bw.SessionDone()

	srv.auditLog.Log(audit.ActionPeerBanned, "admin", "PEERAAA1", nil)
	srv.auditLog.Log(audit.ActionPeerUnbanned, "admin", "PEERAAA1", nil)
	srv.auditLog.Log(audit.ActionPeerBanned, "admin", "PEERAAA1", nil)
	if err := database.BanPeer("PEERAAA1", "test"); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	srv.handleMetrics(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := rec.Body.String()
	for _, want := range []string{
		"\nbetterdesk_registrations_total 2\n",
		"\nbetterdesk_expired_total 1\n",
		"\nbetterdesk_bandwidth_throttle_hits_total 1\n",
		"\nbetterdesk_audit_events_total 3\n",
		"\nbetterdesk_peers_banned 1\n",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics lacks %q", strings.TrimSpace(want))
		}
	}
	if strings.Contains(body, "betterdesk_relay_bytes_total") {
		t.Error("/metrics offers betterdesk_relay_bytes_total, which nothing counts")
	}
}
