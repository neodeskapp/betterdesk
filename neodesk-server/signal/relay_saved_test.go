package signal

import (
	"testing"

	"github.com/unitronix/betterdesk-server/config"
)

// L90: the relay devices are given is the "Public relay server" saved in
// the console; RELAY_SERVERS is the first default, and comes back when
// the console names no relay.
func TestDevicesGetTheRelaySavedInTheConsole(t *testing.T) {
	s, _ := newTestSignalServer(t, config.EnrollmentModeOpen)
	s.cfg.RelayServers = "relay-env.example:21117"
	if got := s.getRelayServer(); got != "relay-env.example:21117" {
		t.Fatalf("with RELAY_SERVERS: %q", got)
	}

	s.cfg.SetPublicRelayServer("relay-saved.example")
	if got := s.getRelayServer(); got != "relay-saved.example:21117" {
		t.Errorf("after the console saved a relay: %q, want relay-saved.example:21117", got)
	}
	// A relay a client names that no client can use gives the saved one
	if got := s.sanitizeClientRelayAddress(""); got != "relay-saved.example:21117" {
		t.Errorf("for a client without a relay: %q", got)
	}

	s.cfg.SetPublicRelayServer("")
	if got := s.getRelayServer(); got != "relay-env.example:21117" {
		t.Errorf("after the console cleared the relay: %q, want RELAY_SERVERS again", got)
	}
}
