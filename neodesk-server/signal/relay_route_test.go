package signal

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/config"
	"github.com/neodeskapp/neodesk/neodesk-server/peer"
	pb "github.com/neodeskapp/neodesk/neodesk-server/proto"
	"github.com/neodeskapp/neodesk/neodesk-server/relay"
)

// recordingTickets is a ticket store that keeps what it was asked and
// answers with err.
type recordingTickets struct {
	mu      sync.Mutex
	issued  []relay.Ticket
	revoked []string
	err     error
}

func (r *recordingTickets) Authorize(_ context.Context, t relay.Ticket) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return r.err
	}
	r.issued = append(r.issued, t)
	return nil
}

func (r *recordingTickets) Admitted(uuid, initiator, target string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, t := range r.issued {
		if t.UUID == uuid && t.Initiator == initiator && t.Target == target {
			return true
		}
	}
	return false
}

func (r *recordingTickets) RevokeForPeer(peer string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.revoked = append(r.revoked, peer)
}

func (r *recordingTickets) Live(string) (relay.LivePair, bool) { return relay.LivePair{}, false }

func (r *recordingTickets) issuedUUIDs() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.issued))
	for _, t := range r.issued {
		out = append(out, t.UUID)
	}
	return out
}

// TestSessionsGetTheirTicketFromTheStoreSignalWasGiven: every path that
// issues a relay ticket issues it on the store from SetTickets, never on
// the in-process default, so a store elsewhere (Valkey, a relay node)
// sees every session.
func TestSessionsGetTheirTicketFromTheStoreSignalWasGiven(t *testing.T) {
	paths := []struct {
		name string
		run  func(srv *Server, uuid string)
	}{
		{"udp request relay", func(srv *Server, uuid string) {
			srv.handleRequestRelay(&pb.RequestRelay{Id: "ROUTETGT1", Uuid: uuid}, udpAddr("203.0.113.71", 41000))
		}},
		{"tcp request relay", func(srv *Server, uuid string) {
			srv.handleRequestRelayTCP(&pb.RequestRelay{Id: "ROUTETGT1", Uuid: uuid}, udpAddr("203.0.113.71", 41000), peer.ConnTCP)
		}},
		{"websocket request relay", func(srv *Server, uuid string) {
			srv.handleRequestRelayTCP(&pb.RequestRelay{Id: "ROUTETGT1", Uuid: uuid}, udpAddr("203.0.113.71", 41000), peer.ConnWS)
		}},
	}
	for _, p := range paths {
		t.Run(p.name, func(t *testing.T) {
			srv, _ := newTestSignalServer(t, config.EnrollmentModeOpen)
			store := &recordingTickets{}
			srv.SetTickets(store)
			putOnlinePeer(srv, "ROUTETGT1", "198.51.100.71", 52000, peer.ConnUDP)
			putOnlinePeer(srv, "ROUTEINI1", "203.0.113.71", 41000, peer.ConnTCP)

			uuid := "route-ticket-" + p.name
			p.run(srv, uuid)

			if got := store.issuedUUIDs(); len(got) != 1 || got[0] != uuid {
				t.Fatalf("store got tickets %v, want [%s]", got, uuid)
			}
			if relay.DefaultTickets().Admitted(uuid, "ROUTEINI1", "ROUTETGT1") {
				t.Fatal("the in-process store got the ticket too")
			}
		})
	}

	t.Run("forced relay of a udp punch", func(t *testing.T) {
		srv, _ := newTestSignalServer(t, config.EnrollmentModeOpen)
		store := &recordingTickets{}
		srv.SetTickets(store)
		putOnlinePeer(srv, "ROUTETGT2", "198.51.100.72", 52000, peer.ConnUDP)
		putOnlinePeer(srv, "ROUTEINI2", "203.0.113.72", 41000, peer.ConnUDP)

		srv.handlePunchHoleRequest(&pb.PunchHoleRequest{Id: "ROUTETGT2", ForceRelay: true}, udpAddr("203.0.113.72", 41000))

		if got := store.issuedUUIDs(); len(got) != 1 {
			t.Fatalf("store got tickets %v, want one", got)
		}
	})
}

// TestAnUnreachableTicketStoreRefusesWithItsOwnReason: a store that cannot
// be asked refuses the session as unavailable, not as a rejected pair.
func TestAnUnreachableTicketStoreRefusesWithItsOwnReason(t *testing.T) {
	srv, _ := newTestSignalServer(t, config.EnrollmentModeOpen)
	srv.SetTickets(&recordingTickets{err: fmt.Errorf("dial: %w", relay.ErrTicketsUnavailable)})
	putOnlinePeer(srv, "ROUTETGT3", "198.51.100.73", 52000, peer.ConnUDP)
	putOnlinePeer(srv, "ROUTEINI3", "203.0.113.73", 41000, peer.ConnTCP)

	resp := srv.handleRequestRelayTCP(&pb.RequestRelay{Id: "ROUTETGT3", Uuid: "route-unavailable"}, udpAddr("203.0.113.73", 41000), peer.ConnTCP)
	if got := resp.GetRelayResponse().GetRefuseReason(); got != refuseRelayUnavailable {
		t.Fatalf("refuse reason %q, want %q", got, refuseRelayUnavailable)
	}

	srv.SetTickets(&recordingTickets{err: relay.ErrTicketRejected})
	resp = srv.handleRequestRelayTCP(&pb.RequestRelay{Id: "ROUTETGT3", Uuid: "route-rejected"}, udpAddr("203.0.113.73", 41000), peer.ConnTCP)
	if got := resp.GetRelayResponse().GetRefuseReason(); got != refuseRelayTicketRejected {
		t.Fatalf("refuse reason %q, want %q", got, refuseRelayTicketRejected)
	}
}

// TestABanRevokesOnTheStoreSignalWasGiven: revoking a banned peer's
// tickets reaches the injected store.
func TestABanRevokesOnTheStoreSignalWasGiven(t *testing.T) {
	srv, _ := newTestSignalServer(t, config.EnrollmentModeOpen)
	store := &recordingTickets{}
	srv.SetTickets(store)
	srv.revokeBannedPeerAccess("ROUTEBAN1", nil)
	if len(store.revoked) != 1 || store.revoked[0] != "ROUTEBAN1" {
		t.Fatalf("revoked %v, want [ROUTEBAN1]", store.revoked)
	}
}

// TestChooseRelayNamesTheTargetsOwnRelay: the route keeps the relay the
// target reported for the RequestRelay it gets, and says the session runs
// over the LAN relay only when the address changed for the LAN.
func TestChooseRelayNamesTheTargetsOwnRelay(t *testing.T) {
	srv, _ := newTestSignalServer(t, config.EnrollmentModeOpen)
	target := &peer.Entry{ID: "ROUTETGT4", RelayServer: "relay.example.com"}
	route := srv.chooseRelay(relayRouteRequest{
		base: "203.0.113.1:21117", a: udpAddr("198.51.100.74", 1), b: udpAddr("203.0.113.74", 2), target: target,
	})
	if route.Addr != "203.0.113.1:21117" || route.TargetAddr != "relay.example.com" || route.Via != viaCentral {
		t.Fatalf("route %+v, want the base for the initiator, the reported relay for the target, via central", route)
	}
	if route.Tickets == nil {
		t.Fatal("route names no ticket store")
	}
}
