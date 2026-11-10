package config

import "testing"

func env(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}

func TestLoadDefaults(t *testing.T) {
	cfg, err := Load(env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Port != 0 || cfg.Host != "0.0.0.0" || cfg.NodeUpstream != nil || cfg.TrustProxy.Enabled() {
		t.Errorf("defaults = %+v", cfg)
	}
}

func TestLoadValues(t *testing.T) {
	cfg, err := Load(env(map[string]string{
		"HOST": "127.0.0.1", "CONSOLE_PORT": "5000", "TRUST_PROXY": "loopback",
		"CONSOLE_NODE_UPSTREAM": "http://127.0.0.1:5001",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Addr() != "127.0.0.1:5000" {
		t.Errorf("Addr = %q", cfg.Addr())
	}
	if cfg.NodeUpstream == nil || cfg.NodeUpstream.Host != "127.0.0.1:5001" {
		t.Errorf("NodeUpstream = %v", cfg.NodeUpstream)
	}
	if !cfg.TrustProxy.Enabled() {
		t.Error("TrustProxy is off")
	}
}

func TestLoadRejectsBadValues(t *testing.T) {
	for _, values := range []map[string]string{
		{"CONSOLE_PORT": "abc"},
		{"CONSOLE_PORT": "70000"},
		{"TRUST_PROXY": "not an address"},
		{"CONSOLE_NODE_UPSTREAM": "ftp://x"},
		{"CONSOLE_NODE_UPSTREAM": "127.0.0.1:5001"},
	} {
		if _, err := Load(env(values)); err == nil {
			t.Errorf("Load(%v) accepted a bad value", values)
		}
	}
}
