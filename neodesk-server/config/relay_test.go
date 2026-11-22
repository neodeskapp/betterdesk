package config

import (
	"reflect"
	"sync"
	"testing"
)

// L90: the relay the console's settings name comes before RELAY_SERVERS,
// which is the first default, with the relay port where it names none.
func TestRelayServersPutTheConsolesRelayFirst(t *testing.T) {
	cfg := DefaultConfig()
	cfg.RelayServers = "relay-env.example, relay-two.example:443"
	if got := cfg.GetRelayServers(); !reflect.DeepEqual(got, []string{"relay-env.example:21117", "relay-two.example:443"}) {
		t.Fatalf("RELAY_SERVERS alone: %q", got)
	}
	for relay, want := range map[string]string{
		"relay-saved.example":     "relay-saved.example:21117",
		" relay-saved.example ":   "relay-saved.example:21117",
		"relay-saved.example:443": "relay-saved.example:443",
		"2001:db8::7":             "[2001:db8::7]:21117",
	} {
		cfg.SetPublicRelayServer(relay)
		if got := cfg.GetRelayServers(); !reflect.DeepEqual(got, []string{want}) {
			t.Errorf("saved relay %q: %q, want [%s]", relay, got, want)
		}
	}
	cfg.SetPublicRelayServer("")
	if got := cfg.GetRelayServers(); len(got) != 2 || got[0] != "relay-env.example:21117" {
		t.Errorf("after the relay was cleared: %q, want RELAY_SERVERS again", got)
	}
	cfg.SetPublicRelayServer("x")
	if got := cfg.GetRelayServers(); len(got) != 2 {
		t.Errorf("an invalid saved relay: %q, want RELAY_SERVERS", got)
	}
}

// The console sets the relay while the signal server reads it.
func TestPublicRelayServerUnderConcurrentUse(t *testing.T) {
	cfg := DefaultConfig()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 500; i++ {
			cfg.SetPublicRelayServer("relay-saved.example")
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 500; i++ {
			_ = cfg.GetRelayServers()
		}
	}()
	wg.Wait()
}
