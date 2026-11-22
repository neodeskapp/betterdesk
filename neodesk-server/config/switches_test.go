package config

import "testing"

// serverSwitch is an on/off variable of the server and the field it sets.
type serverSwitch struct {
	name string
	get  func(*Config) bool
	set  func(*Config, bool)
}

func serverSwitches() []serverSwitch {
	return []serverSwitch{
		{"ALWAYS_USE_RELAY", func(c *Config) bool { return c.AlwaysUseRelay }, func(c *Config, v bool) { c.AlwaysUseRelay = v }},
		{"CLIENT_SESSION_SLIDING", func(c *Config) bool { return c.ClientSessionSliding }, func(c *Config, v bool) { c.ClientSessionSliding = v }},
		{"FORCE_HTTPS", func(c *Config) bool { return c.ForceHTTPS }, func(c *Config, v bool) { c.ForceHTTPS = v }},
		{"TRUST_PROXY", func(c *Config) bool { return c.TrustProxy }, func(c *Config, v bool) { c.TrustProxy = v }},
		{"ALLOW_LEGACY_OUTBOUND", func(c *Config) bool { return c.AllowLegacyOutbound }, func(c *Config, v bool) { c.AllowLegacyOutbound = v }},
		{"SAME_NAT_RELAY", func(c *Config) bool { return c.SameNATRelay }, func(c *Config, v bool) { c.SameNATRelay = v }},
		{"ALLOW_SHARED_NAT_INITIATOR", func(c *Config) bool { return c.AllowSharedNATInitiator }, func(c *Config, v bool) { c.AllowSharedNATInitiator = v }},
		{"LOGGED_IN_ONLY_INITIATOR", func(c *Config) bool { return c.LoggedInOnlyInitiator }, func(c *Config, v bool) { c.LoggedInOnlyInitiator = v }},
		{"OPERATOR_ONLY_OUTBOUND", func(c *Config) bool { return c.OperatorOnlyOutbound }, func(c *Config, v bool) { c.OperatorOnlyOutbound = v }},
		{"P2P_FIRST", func(c *Config) bool { return c.P2PFirst }, func(c *Config, v bool) { c.P2PFirst = v }},
		{"METRICS_PUBLIC", func(c *Config) bool { return c.MetricsPublic }, func(c *Config, v bool) { c.MetricsPublic = v }},
		{"TLS_SIGNAL", func(c *Config) bool { return c.TLSSignal }, func(c *Config, v bool) { c.TLSSignal = v }},
		{"TLS_RELAY", func(c *Config) bool { return c.TLSRelay }, func(c *Config, v bool) { c.TLSRelay = v }},
		{"TLS_API", func(c *Config) bool { return c.TLSApi }, func(c *Config, v bool) { c.TLSApi = v }},
		{"CDAP_ENABLED", func(c *Config) bool { return c.CDAPEnabled }, func(c *Config, v bool) { c.CDAPEnabled = v }},
		{"CDAP_TLS", func(c *Config) bool { return c.CDAPTLS }, func(c *Config, v bool) { c.CDAPTLS = v }},
		{"CDAP_TLS_REQUIRED", func(c *Config) bool { return c.CDAPTLSRequired }, func(c *Config, v bool) { c.CDAPTLSRequired = v }},
		{"MESH_ENABLED", func(c *Config) bool { return c.MeshCentralEnabled }, func(c *Config, v bool) { c.MeshCentralEnabled = v }},
		{"BILLING_REQUIRE_SYNCED_CLOCK", func(c *Config) bool { return c.BillingRequireSyncedClock }, func(c *Config, v bool) { c.BillingRequireSyncedClock = v }},
		{"BILLING_TRUST_OS_NTP", func(c *Config) bool { return c.BillingTrustOSNTP }, func(c *Config, v bool) { c.BillingTrustOSNTP = v }},
		{"BILLING_REQUIRE_WORK_REPORT", func(c *Config) bool { return c.BillingRequireWorkReport }, func(c *Config, v bool) { c.BillingRequireWorkReport = v }},
	}
}

var (
	onValues  = []string{"1", "true", "TRUE", "True", "yes", "YES", "y", "Y", "on", "ON", " on "}
	offValues = []string{"0", "false", "FALSE", "False", "no", "NO", "n", "N", "off", "OFF", " off "}
)

// L84: every on/off variable of the server takes 1/0, true/false, yes/no,
// y/n and on/off in any letter case. An off value turns off what a flag or
// the default turned on, and a value that is no on/off value leaves the
// setting as it was.
func TestLoadEnv_EverySwitchTakesEveryOnOffValue(t *testing.T) {
	for _, s := range serverSwitches() {
		for _, value := range onValues {
			t.Setenv(s.name, value)
			cfg := DefaultConfig()
			s.set(cfg, false)
			cfg.LoadEnv()
			if !s.get(cfg) {
				t.Errorf("%s=%q leaves the setting off", s.name, value)
			}
		}
		for _, value := range offValues {
			t.Setenv(s.name, value)
			cfg := DefaultConfig()
			s.set(cfg, true)
			cfg.LoadEnv()
			if s.get(cfg) {
				t.Errorf("%s=%q leaves the setting on", s.name, value)
			}
		}
		for _, before := range []bool{false, true} {
			t.Setenv(s.name, "maybe")
			cfg := DefaultConfig()
			s.set(cfg, before)
			cfg.LoadEnv()
			if s.get(cfg) != before {
				t.Errorf("%s=maybe changes the setting from %v", s.name, before)
			}
		}
		t.Setenv(s.name, "")
	}
}

// CDAP_TLS_REQUIRED on turns TLS on the CDAP port on too; off leaves it.
func TestLoadEnv_CDAPTLSRequiredOffKeepsCDAPTLS(t *testing.T) {
	t.Setenv("CDAP_TLS_REQUIRED", "off")
	cfg := DefaultConfig()
	cfg.CDAPTLS, cfg.CDAPTLSRequired = true, true
	cfg.LoadEnv()
	if cfg.CDAPTLSRequired || !cfg.CDAPTLS {
		t.Fatalf("CDAP_TLS_REQUIRED=off: required %v, TLS %v; want not required, TLS kept", cfg.CDAPTLSRequired, cfg.CDAPTLS)
	}
	t.Setenv("CDAP_TLS_REQUIRED", "On")
	cfg = DefaultConfig()
	cfg.LoadEnv()
	if !cfg.CDAPTLSRequired || !cfg.CDAPTLS {
		t.Fatalf("CDAP_TLS_REQUIRED=On: required %v, TLS %v; want both", cfg.CDAPTLSRequired, cfg.CDAPTLS)
	}
}

// TRUSTED_PROXIES replaces the allowlist a flag set.
func TestLoadEnv_TrustedProxiesReplaceTheFlagsList(t *testing.T) {
	t.Setenv("TRUSTED_PROXIES", "192.168.0.0/16")
	cfg := DefaultConfig()
	cfg.TrustedProxies, _ = ParseTrustedProxies("10.0.0.0/8")
	cfg.LoadEnv()
	if len(cfg.TrustedProxies) != 1 || cfg.TrustedProxies[0].String() != "192.168.0.0/16" {
		t.Fatalf("TrustedProxies = %v, want the variable's 192.168.0.0/16", cfg.TrustedProxies)
	}
}
