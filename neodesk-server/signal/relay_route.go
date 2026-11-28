package signal

import (
	"net"

	"github.com/neodeskapp/neodesk/neodesk-server/peer"
	"github.com/neodeskapp/neodesk/neodesk-server/relay"
)

// RelayRoute is the relay a session uses: the address the initiator dials,
// the address the target is told, the ticket store the session's ticket is
// issued on (the relay at that address claims from it) and how the session
// travels, for live sessions and logs.
type RelayRoute struct {
	// Addr is the relay address the initiator gets (RelayResponse,
	// PunchHoleResponse, PunchHole, FetchLocalAddr).
	Addr string
	// TargetAddr is the relay address in the RequestRelay sent to the
	// target. Stock clients compare it with the relay they configured
	// (relayAdvertisedToTarget), so it can differ from Addr.
	TargetAddr string
	// Tickets is where admitSession issues the session's ticket.
	Tickets relay.Tickets
	// Via says which relay carries the session: viaCentral or viaLAN.
	Via string
	// SameNetwork is true when both peers are on one private network,
	// Hairpin when they share a public address and SAME_NAT_RELAY forces
	// the public relay.
	SameNetwork bool
	Hairpin     bool
}

// Values of RelayRoute.Via.
const (
	// viaCentral is the server's own relay at its public address.
	viaCentral = "central"
	// viaLAN is the server's own relay at its LAN address.
	viaLAN = "lan"
)

// relayRouteRequest is what chooseRelay picks a route from.
type relayRouteRequest struct {
	// base is the relay the session starts from: getRelayServer(), or
	// the relay a client named after sanitizeClientRelayAddress.
	base string
	// a and b are the two peers' addresses for the LAN and shared-NAT
	// checks; either may be nil.
	a, b *net.UDPAddr
	// target fills RelayRoute.TargetAddr; nil leaves it at Addr.
	target *peer.Entry
	// policyPeers are the peers whose org network policy
	// (allowed_relay_servers) applies; none skips the policy.
	policyPeers []string
}

// chooseRelay picks the relay of a session. Every place that names a relay
// to a peer for a session gets it from here, so a new kind of relay (a pod
// of a cluster, a relay node at a site) is added in one spot.
func (s *Server) chooseRelay(req relayRouteRequest) RelayRoute {
	addr, sameNetwork, hairpin := s.selectPeerRelayServer(req.base, req.a, req.b)
	if len(req.policyPeers) > 0 {
		addr = s.applyNetworkRelayPolicy(addr, req.policyPeers...)
	}
	route := RelayRoute{
		Addr:        addr,
		TargetAddr:  addr,
		Tickets:     s.ticketStore(),
		Via:         viaCentral,
		SameNetwork: sameNetwork,
		Hairpin:     hairpin,
	}
	if sameNetwork && addr != req.base {
		route.Via = viaLAN
	}
	if req.target != nil {
		route.TargetAddr = relayAdvertisedToTarget(req.target, addr)
	}
	return route
}

// SetTickets sets the ticket store signal issues relay tickets on instead
// of the in-process one (relay.DefaultTickets). The relay must claim from
// the same store.
func (s *Server) SetTickets(t relay.Tickets) {
	if t != nil {
		s.tickets = t
	}
}

// ticketStore is the store tickets are issued on and looked up in.
func (s *Server) ticketStore() relay.Tickets {
	if s.tickets == nil {
		return relay.DefaultTickets()
	}
	return s.tickets
}
