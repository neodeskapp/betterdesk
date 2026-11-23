package main

import (
	"flag"
	"io"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/config"
)

// configureWith runs the start's reading of flags and variables on args.
func configureWith(t *testing.T, args ...string) *config.Config {
	t.Helper()
	fs := flag.NewFlagSet("neodesk-server", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	cfg, showVersion, err := configure(fs, args)
	if err != nil || showVersion {
		t.Fatalf("configure(%q): version %v, %v", args, showVersion, err)
	}
	return cfg
}

// L84: on/off flags take the values the variables take, and a flag alone
// still turns its setting on.
func TestOnOffFlagsTakeEveryOnOffValue(t *testing.T) {
	for _, value := range []string{"yes", "on", "Y", "1", "true", "t", "TRUE"} {
		if cfg := configureWith(t, "-tls-signal="+value); !cfg.TLSSignal {
			t.Errorf("-tls-signal=%s leaves TLS on the signal port off", value)
		}
	}
	for _, value := range []string{"no", "off", "N", "0", "false", "f"} {
		if cfg := configureWith(t, "-cdap="+value); cfg.CDAPEnabled {
			t.Errorf("-cdap=%s leaves the CDAP gateway on", value)
		}
	}
	if cfg := configureWith(t, "-force-https", "-trust-proxy"); !cfg.ForceHTTPS || !cfg.TrustProxy {
		t.Errorf("flags alone: force HTTPS %v, trust proxy %v; want both on", cfg.ForceHTTPS, cfg.TrustProxy)
	}
	fs := flag.NewFlagSet("neodesk-server", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	if _, _, err := configure(fs, []string{"-tls-relay=maybe"}); err == nil {
		t.Error("-tls-relay=maybe was taken")
	}
}

// L84: a variable wins over its flag, also when it turns the flag off.
func TestVariablesWinOverFlags(t *testing.T) {
	t.Setenv("TLS_SIGNAL", "N")
	t.Setenv("FORCE_HTTPS", "off")
	t.Setenv("CDAP_ENABLED", "yes")
	cfg := configureWith(t, "-tls-signal", "-force-https", "-cdap=false")
	if cfg.TLSSignal || cfg.ForceHTTPS || !cfg.CDAPEnabled {
		t.Errorf("TLS signal %v, force HTTPS %v, CDAP %v; want the variables' off, off, on",
			cfg.TLSSignal, cfg.ForceHTTPS, cfg.CDAPEnabled)
	}
}

// L84: TRUSTED_PROXIES wins over -trusted-proxies, as every variable wins
// over its flag; the flag applies without the variable.
func TestTrustedProxiesVariableWinsOverTheFlag(t *testing.T) {
	cfg := configureWith(t, "-trusted-proxies", "10.0.0.0/8")
	if len(cfg.TrustedProxies) != 1 || cfg.TrustedProxies[0].String() != "10.0.0.0/8" {
		t.Fatalf("flag alone: %v, want 10.0.0.0/8", cfg.TrustedProxies)
	}
	t.Setenv("TRUSTED_PROXIES", "192.168.0.0/16")
	cfg = configureWith(t, "-trusted-proxies", "10.0.0.0/8")
	if len(cfg.TrustedProxies) != 1 || cfg.TrustedProxies[0].String() != "192.168.0.0/16" {
		t.Fatalf("flag and variable: %v, want the variable's 192.168.0.0/16", cfg.TrustedProxies)
	}
}
