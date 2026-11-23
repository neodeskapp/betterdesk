package signal

import (
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/config"
	cryptopkg "github.com/neodeskapp/neodesk/neodesk-server/crypto"
	"github.com/neodeskapp/neodesk/neodesk-server/peer"
	pb "github.com/neodeskapp/neodesk/neodesk-server/proto"
)

// A peer's own relay value wins over the server-derived one, because the peer
// validates the incoming request against its own configuration.
func TestRelayAdvertisedToTargetPrefersPeerReportedRelay(t *testing.T) {
	target := &peer.Entry{ID: "TGT1", RelayServer: "desk.example.com"}

	got := relayAdvertisedToTarget(target, "85.14.70.12:21117")
	if got != "desk.example.com" {
		t.Fatalf("relay = %q, want the peer-reported value", got)
	}
}

func TestRelayAdvertisedToTargetFallsBackForOldClients(t *testing.T) {
	// Older clients never report a relay in PunchHoleSent. Send the bare host so
	// the peer's own check_port() normalises it to RELAY_PORT — that matches a
	// client configured as either `host` or `host:21117`. Production confirmed
	// this shape pairs, while a forced :21117 did not.
	target := &peer.Entry{ID: "TGT2"}

	got := relayAdvertisedToTarget(target, "85.14.70.12:21117")
	if got != "85.14.70.12" {
		t.Fatalf("relay = %q, want the bare host", got)
	}
	if relayAdvertisedToTarget(nil, "fallback") != "fallback" {
		t.Fatal("nil target must use the fallback unchanged")
	}
	// Hostname fallbacks keep working too.
	if got := relayAdvertisedToTarget(target, "desk.example.com:21117"); got != "desk.example.com" {
		t.Fatalf("relay = %q, want the bare host", got)
	}
}

// L233: check_port() adds 21117 to a bare host, so a relay on another port
// keeps its port; without it the peer dials 21117 and never pairs.
func TestRelayAdvertisedToTargetKeepsAPortOtherThanTheDefault(t *testing.T) {
	target := &peer.Entry{ID: "TGT3"}
	for _, relay := range []string{"127.0.0.1:24117", "desk.example.com:31117", "[2001:db8::7]:24117"} {
		if got := relayAdvertisedToTarget(target, relay); got != relay {
			t.Errorf("relay %q went to the target as %q", relay, got)
		}
	}
	if got := relayAdvertisedToTarget(target, "[2001:db8::7]:21117"); got != "2001:db8::7" {
		t.Errorf("relay [2001:db8::7]:21117 went to the target as %q, want the bare host", got)
	}
}

// The reported relay is stored verbatim: the peer validates incoming relay
// requests against its own value, so rewriting it makes the peer drop them.
// A peer reporting the API port works because that port also speaks relay
// framing (api/sniff_listener.go).
func TestHandlePunchHoleSentRecordsPeerRelayVerbatim(t *testing.T) {
	srv, _ := newTestSignalServer(t, config.EnrollmentModeOpen)
	srv.localIP.Store("198.51.100.20")
	srv.cfg.RelayServers = "85.14.70.12"
	srv.cfg.RelayPort = 21117
	srv.cfg.APIPort = 21114

	target := &peer.Entry{
		ID:       "TGTREP1",
		UDPAddr:  udpAddr("203.0.113.80", 52100),
		ConnType: peer.ConnUDP,
		LastReg:  time.Now(),
	}
	srv.peers.Put(target)

	initiatorAddr := udpAddr("203.0.113.90", 51000)
	srv.storePendingUUID("TGTREP1", initiatorAddr, "", "INIREP1")
	// The failing agent reports the API port as its relay.
	srv.handlePunchHoleSent(&pb.PunchHoleSent{
		Id:          "TGTREP1",
		SocketAddr:  cryptopkg.EncodeAddr(initiatorAddr),
		RelayServer: "85.14.70.12:21114",
	}, udpAddr("203.0.113.80", 52100), true)

	entry := srv.peers.Get("TGTREP1")
	if entry == nil {
		t.Fatal("target disappeared")
	}
	if entry.RelayServer != "85.14.70.12:21114" {
		t.Fatalf("stored relay = %q, want the peer's own value (rewriting it makes the peer drop the request)", entry.RelayServer)
	}
	if got := relayAdvertisedToTarget(entry, "fallback:21117"); got != "85.14.70.12:21114" {
		t.Fatalf("forwarded relay = %q, want the peer's own value", got)
	}
}
