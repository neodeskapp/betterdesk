package main

import (
	"testing"

	consoleconfig "github.com/neodeskapp/neodesk/neodesk-server/console/config"
)

// linkedConsole is the console's configuration for a start with env and the
// given flags, linked to the server of that start.
func linkedConsole(t *testing.T, env map[string]string, args ...string) *consoleconfig.Config {
	t.Helper()
	for key, value := range env {
		t.Setenv(key, value)
	}
	getenv := func(key string) string { return env[key] }
	ccfg, err := consoleconfig.Load(getenv)
	if err != nil {
		t.Fatal(err)
	}
	linkConsoleToServer(ccfg, configureWith(t, args...), getenv)
	return ccfg
}

// L228: the browser viewer's sockets, the status page and the attestation
// reach the signal server and the relay on the ports this process listens
// on, also when they are not 21116 and 21117.
func TestTheConsoleReachesTheServersOwnSignalAndRelayPorts(t *testing.T) {
	t.Run("variables", func(t *testing.T) {
		ccfg := linkedConsole(t, map[string]string{"SIGNAL_PORT": "24116", "RELAY_PORT": "24117"})
		if ccfg.SignalPort != 24116 || ccfg.RelayPort != 24117 {
			t.Errorf("SIGNAL_PORT=24116 RELAY_PORT=24117: the console reaches %d and %d", ccfg.SignalPort, ccfg.RelayPort)
		}
	})
	t.Run("flags", func(t *testing.T) {
		ccfg := linkedConsole(t, nil, "-port", "25116", "-relay-port", "25117")
		if ccfg.SignalPort != 25116 || ccfg.RelayPort != 25117 {
			t.Errorf("-port 25116 -relay-port 25117: the console reaches %d and %d", ccfg.SignalPort, ccfg.RelayPort)
		}
	})
	t.Run("defaults", func(t *testing.T) {
		ccfg := linkedConsole(t, nil)
		if ccfg.SignalPort != 21116 || ccfg.RelayPort != 21117 {
			t.Errorf("no ports set: the console reaches %d and %d", ccfg.SignalPort, ccfg.RelayPort)
		}
	})
	t.Run("the console's own variables win", func(t *testing.T) {
		ccfg := linkedConsole(t, map[string]string{
			"SIGNAL_PORT": "24116", "RELAY_PORT": "24117",
			"WS_HBBS_PORT": "26116", "WS_HBBR_PORT": "26117",
		})
		if ccfg.SignalPort != 26116 || ccfg.RelayPort != 26117 {
			t.Errorf("WS_HBBS_PORT=26116 WS_HBBR_PORT=26117: the console reaches %d and %d", ccfg.SignalPort, ccfg.RelayPort)
		}
	})
}
