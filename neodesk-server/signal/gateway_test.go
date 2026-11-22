package signal

import (
	"testing"

	"github.com/unitronix/betterdesk-server/config"
)

func TestParseDefaultGateway(t *testing.T) {
	const header = "Iface\tDestination\tGateway \tFlags\tRefCnt\tUse\tMetric\tMask\t\tMTU\tWindow\tIRTT\n"
	cases := []struct{ name, table, want string }{
		{"a Docker bridge", header +
			"eth0\t00000000\t010011AC\t0003\t0\t0\t0\t00000000\t0\t0\t0\n" +
			"eth0\t000011AC\t00000000\t0001\t0\t0\t0\t0000FFFF\t0\t0\t0\n", "172.17.0.1"},
		{"the default route after others", header +
			"enp3s0\t0001A8C0\t00000000\t0001\t0\t0\t100\t00FFFFFF\t0\t0\t0\n" +
			"enp3s0\t00000000\t0101A8C0\t0003\t0\t0\t100\t00000000\t0\t0\t0\n", "192.168.1.1"},
		{"no default route", header + "eth0\t000011AC\t00000000\t0001\t0\t0\t0\t0000FFFF\t0\t0\t0\n", ""},
		{"a default route without the gateway flag", header + "tun0\t00000000\t00000000\t0001\t0\t0\t0\t00000000\t0\t0\t0\n", ""},
		{"not a route table", "garbage", ""},
	}
	for _, c := range cases {
		got := parseDefaultGateway(c.table)
		if (got == nil && c.want != "") || (got != nil && got.String() != c.want) {
			t.Errorf("%s: gateway %v, want %q", c.name, got, c.want)
		}
	}
}

// L232: on Docker Desktop every client reaches the container from the
// bridge gateway, and none of them reaches the container's own address.
// A pair with a peer at the gateway gets the relay the admin named, not
// the server's LAN IP.
func TestThePeersOfTheGatewayGetTheNamedRelay(t *testing.T) {
	srv, _ := newTestSignalServer(t, config.EnrollmentModeOpen)
	srv.cfg.RelayPort = 24117
	srv.localIP.Store("198.51.100.20")
	docker := func(relayServers, publicRelay string) {
		srv.cfg.RelayServers = relayServers
		srv.cfg.SetPublicRelayServer(publicRelay)
		srv.lanIP.Store("172.17.0.2")
		srv.lanGateway.Store("172.17.0.1")
	}
	cases := []struct {
		name                      string
		relayServers, publicRelay string
		a, b                      string
		want                      string
	}{
		{"the browser viewer and a device at the gateway", "127.0.0.1:24117", "", "127.0.0.1", "172.17.0.1", "127.0.0.1:24117"},
		{"two clients at the gateway", "127.0.0.1:24117", "", "172.17.0.1", "172.17.0.1", "127.0.0.1:24117"},
		{"a device at the gateway and another container", "127.0.0.1:24117", "", "172.17.0.3", "172.17.0.1", "127.0.0.1:24117"},
		{"the console's Public relay server", "", "relay.example.com", "127.0.0.1", "172.17.0.1", "relay.example.com:24117"},
		// Unchanged: a peer of the bridge itself, and no relay named, where
		// nothing is known to be better than the LAN IP
		{"the browser viewer and another container", "127.0.0.1:24117", "", "127.0.0.1", "172.17.0.3", "172.17.0.2:24117"},
		{"no relay named", "", "", "127.0.0.1", "172.17.0.1", "172.17.0.2:24117"},
	}
	for _, c := range cases {
		docker(c.relayServers, c.publicRelay)
		relay, _, _ := srv.selectPeerRelayServer(srv.getRelayServer(), udpAddr(c.a, 51000), udpAddr(c.b, 52000))
		if relay != c.want {
			t.Errorf("%s: relay %q, want %q", c.name, relay, c.want)
		}
	}

	// Peers with addresses of their own on the server's LAN keep the LAN IP
	docker("relay.example.com", "")
	srv.lanIP.Store("192.168.1.20")
	srv.lanGateway.Store("192.168.1.1")
	if relay, _, _ := srv.selectPeerRelayServer(srv.getRelayServer(), udpAddr("192.168.1.10", 51000), udpAddr("192.168.1.42", 52000)); relay != "192.168.1.20:24117" {
		t.Errorf("two LAN peers: relay %q, want the LAN relay 192.168.1.20:24117", relay)
	}
}
