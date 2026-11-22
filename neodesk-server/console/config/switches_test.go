package config

import "testing"

// consoleSwitch is an on/off variable of the console, the field it sets
// and its value when the variable is unset or holds no on/off value.
type consoleSwitch struct {
	name     string
	get      func(*Config) bool
	fallback bool
}

func consoleSwitches() []consoleSwitch {
	return []consoleSwitch{
		{"HTTPS_ENABLED", func(c *Config) bool { return c.HTTPS }, false},
		{"HTTP_REDIRECT_HTTPS", func(c *Config) bool { return c.HTTPRedirect }, true},
		{"ALLOW_SELF_SIGNED_CERTS", func(c *Config) bool { return c.AllowSelfSignedCerts }, false},
		{"SMTP_TLS_VERIFY", func(c *Config) bool { return c.SMTPTLSVerify }, true},
		{"SMTP_REQUIRE_TLS", func(c *Config) bool { return c.SMTPRequireTLS }, true},
		{"API_ENABLED", func(c *Config) bool { return c.APIEnabled }, true},
		{"RUSTDESK_API_PROXY", func(c *Config) bool { return c.ClientAPIProxy }, true},
		{"CONSOLE_DEBUG_ROUTING", func(c *Config) bool { return c.DebugRouting }, false},
		{"DOCKER", func(c *Config) bool { return c.Docker }, false},
		{"AGENT_BUILD_WORKER", func(c *Config) bool { return c.AgentBuildWorker }, true},
	}
}

var (
	onValues  = []string{"1", "true", "TRUE", "True", "yes", "YES", "y", "Y", "on", "ON", " on "}
	offValues = []string{"0", "false", "FALSE", "False", "no", "NO", "n", "N", "off", "OFF", " off "}
)

// L84: every on/off variable of the console takes 1/0, true/false, yes/no,
// y/n and on/off in any letter case, as the server's do. Unset, a switch
// has its default.
func TestEverySwitchTakesEveryOnOffValue(t *testing.T) {
	for _, s := range consoleSwitches() {
		for _, value := range onValues {
			cfg, err := load(env(map[string]string{s.name: value}), machine())
			if err != nil {
				t.Fatal(err)
			}
			if !s.get(cfg) {
				t.Errorf("%s=%q leaves the setting off", s.name, value)
			}
		}
		for _, value := range offValues {
			cfg, err := load(env(map[string]string{s.name: value}), machine())
			if err != nil {
				t.Fatal(err)
			}
			if s.get(cfg) {
				t.Errorf("%s=%q leaves the setting on", s.name, value)
			}
		}
		cfg, err := load(env(nil), machine())
		if err != nil {
			t.Fatal(err)
		}
		if s.get(cfg) != s.fallback {
			t.Errorf("%s unset gives %v, want %v", s.name, s.get(cfg), s.fallback)
		}
	}
}

// A value that is no on/off value reads as it did: switches that only
// "true" turned on stay off, switches that only "false" or "off" turned
// off stay on.
func TestOtherSwitchValuesReadAsBefore(t *testing.T) {
	for _, s := range consoleSwitches() {
		want := s.fallback
		switch s.name {
		case "HTTP_REDIRECT_HTTPS", "API_ENABLED", "RUSTDESK_API_PROXY":
			want = false
		}
		cfg, err := load(env(map[string]string{s.name: "enabled"}), machine())
		if err != nil {
			t.Fatal(err)
		}
		if s.get(cfg) != want {
			t.Errorf("%s=enabled gives %v, want %v", s.name, s.get(cfg), want)
		}
	}
}

// API_ENABLED and RUSTDESK_API_PROXY set to the empty text are off, as
// they were; only an unset variable takes the default.
func TestEmptyClientAPISwitchesAreOff(t *testing.T) {
	p := machine()
	p.isSet = func(name string) bool { return name == "API_ENABLED" || name == "RUSTDESK_API_PROXY" }
	cfg, err := load(env(nil), p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.APIEnabled || cfg.ClientAPIProxy {
		t.Errorf("set to empty: API %v, proxy %v; want both off", cfg.APIEnabled, cfg.ClientAPIProxy)
	}
}

// RUSTDESK_API_TLS takes every off value for plain HTTP; HSTS_ENABLED
// every on and off value.
func TestTLSModeSwitches(t *testing.T) {
	for _, value := range offValues {
		cfg, _ := load(env(map[string]string{"RUSTDESK_API_TLS": value}), machine())
		if cfg.ClientAPITLS != "false" {
			t.Errorf("RUSTDESK_API_TLS=%q gives %q, want false", value, cfg.ClientAPITLS)
		}
	}
	for value, want := range map[string]string{"": "auto", "Auto": "auto", "http": "http", "YES": "yes", "1": "1"} {
		cfg, _ := load(env(map[string]string{"RUSTDESK_API_TLS": value}), machine())
		if cfg.ClientAPITLS != want {
			t.Errorf("RUSTDESK_API_TLS=%q gives %q, want %q", value, cfg.ClientAPITLS, want)
		}
	}
	for value, want := range map[string]string{"yes": "true", "Y": "true", "on": "true", "no": "false", "N": "false", "0": "false", "": "", "auto": "auto"} {
		cfg, _ := load(env(map[string]string{"HSTS_ENABLED": value}), machine())
		if cfg.HSTSEnabled != want {
			t.Errorf("HSTS_ENABLED=%q gives %q, want %q", value, cfg.HSTSEnabled, want)
		}
	}
}

// TRUST_PROXY takes every on/off value: on trusts one hop, off nobody.
func TestTrustProxyTakesEveryOnOffValue(t *testing.T) {
	for _, value := range append(append([]string{}, onValues...), offValues...) {
		cfg, err := load(env(map[string]string{"TRUST_PROXY": value}), machine())
		if err != nil {
			t.Errorf("TRUST_PROXY=%q: %v", value, err)
			continue
		}
		on, _ := switchOf(value)
		if cfg.TrustProxy.Enabled() != on {
			t.Errorf("TRUST_PROXY=%q: trust %v, want %v", value, cfg.TrustProxy.Enabled(), on)
		}
	}
}

func switchOf(value string) (bool, bool) {
	for _, v := range onValues {
		if v == value {
			return true, true
		}
	}
	for _, v := range offValues {
		if v == value {
			return false, true
		}
	}
	return false, false
}
