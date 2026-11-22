package main

import (
	"strings"
	"testing"

	consoleconfig "github.com/unitronix/betterdesk-server/console/config"
)

// portsOf is the listeners a start with env and the given flags opens.
func portsOf(t *testing.T, env map[string]string, args ...string) []listener {
	t.Helper()
	for key, value := range env {
		t.Setenv(key, value)
	}
	cfg := configureWith(t, args...)
	ccfg, err := consoleconfig.Load(func(key string) string { return env[key] })
	if err != nil {
		t.Fatal(err)
	}
	return startListeners(cfg, ccfg, func(key string) string { return env[key] })
}

// L85: API_PORT alone gives the server's API the console's client API
// port; the start refuses and names both variables.
func TestStartRefusesTheAPIPortClash(t *testing.T) {
	t.Run("clash", func(t *testing.T) {
		err := checkPorts(portsOf(t, map[string]string{"API_PORT": "21121"}))
		if err == nil {
			t.Fatal("the server's API and the console's client API both on 21121 were taken")
		}
		for _, want := range []string{"API_PORT (the server's API, as GO_API_PORT is not set)", "API_PORT (the console's client API)", "21121"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("the refusal %q does not name %s", err, want)
			}
		}
	})
	t.Run("apart", func(t *testing.T) {
		if err := checkPorts(portsOf(t, map[string]string{"API_PORT": "21121", "GO_API_PORT": "21114"})); err != nil {
			t.Errorf("GO_API_PORT and API_PORT apart: %v", err)
		}
	})
}

// L85: any two listeners on one port end the start, with both settings
// named; the usual ports do not.
func TestStartRefusesTwoListenersOnOnePort(t *testing.T) {
	if err := checkPorts(portsOf(t, nil)); err != nil {
		t.Fatalf("the default ports: %v", err)
	}
	for _, c := range []struct {
		env        map[string]string
		args       []string
		first, and string
	}{
		{map[string]string{"SIGNAL_PORT": "5000"}, nil, "SIGNAL_PORT", "CONSOLE_PORT"},
		{map[string]string{"CDAP_PORT": "21117"}, nil, "RELAY_PORT", "CDAP_PORT"},
		{map[string]string{"GO_API_PORT": "21117"}, nil, "RELAY_PORT", "GO_API_PORT"},
		{nil, []string{"-port", "21118"}, "SIGNAL_PORT-1", "RELAY_PORT"},
		{map[string]string{"CONSOLE_PORT": "21121"}, nil, "CONSOLE_PORT", "API_PORT"},
	} {
		t.Run(c.first+" "+c.and, func(t *testing.T) {
			err := checkPorts(portsOf(t, c.env, c.args...))
			if err == nil || !strings.Contains(err.Error(), c.first) || !strings.Contains(err.Error(), c.and) {
				t.Errorf("%v %v: %v, want a refusal that names %s and %s", c.env, c.args, err, c.first, c.and)
			}
		})
	}
	// A listener that is off takes no port
	t.Run("off", func(t *testing.T) {
		if err := checkPorts(portsOf(t, map[string]string{"CDAP_PORT": "21117", "CDAP_ENABLED": "N"})); err != nil {
			t.Errorf("CDAP off on the relay's port: %v", err)
		}
	})
	t.Run("console off", func(t *testing.T) {
		if err := checkPorts(portsOf(t, map[string]string{"CONSOLE_PORT": "0", "API_PORT": "21121"})); err != nil {
			t.Errorf("console off: %v", err)
		}
	})
}
