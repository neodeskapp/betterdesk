package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/unitronix/betterdesk-server/config"
	"github.com/unitronix/betterdesk-server/console/serverinfo"
)

// L90: before the signal server starts, it gets the "Public relay server"
// saved in the console; RELAY_SERVERS is the first default.
func TestStartGivesDevicesTheRelaySavedInTheConsole(t *testing.T) {
	root := t.TempDir()
	data := filepath.Join(root, "data")
	env := map[string]string{"CONSOLE_DIR": root, "DATA_DIR": data}
	getenv := func(key string) string { return env[key] }

	cfg := config.DefaultConfig()
	cfg.RelayServers = "relay-env.example"
	applySavedPublicRelay(cfg, getenv)
	if got := cfg.GetRelayServers(); len(got) != 1 || got[0] != "relay-env.example:21117" {
		t.Fatalf("nothing saved: %q, want RELAY_SERVERS", got)
	}

	if err := os.MkdirAll(data, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(data, serverinfo.DurableBasename), []byte("PUBLIC_RELAY_SERVER=relay-saved.example\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg = config.DefaultConfig()
	cfg.RelayServers = "relay-env.example"
	applySavedPublicRelay(cfg, getenv)
	if got := cfg.GetRelayServers(); len(got) != 1 || got[0] != "relay-saved.example:21117" {
		t.Errorf("saved in the console: %q, want relay-saved.example:21117", got)
	}
}
