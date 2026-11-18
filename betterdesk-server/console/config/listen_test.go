package config

import "testing"

// The listener settings as config/config.js read them.
func TestListenerSettings(t *testing.T) {
	cfg, err := load(env(nil), machine("/srv/bin", "/srv/run"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTPSPort != 5443 || !cfg.HTTPRedirect || cfg.CertFile != "" || cfg.KeyFile != "" || cfg.CAFile != "" {
		t.Errorf("TLS defaults: port %d, redirect %v, files %q %q %q", cfg.HTTPSPort, cfg.HTTPRedirect, cfg.CertFile, cfg.KeyFile, cfg.CAFile)
	}
	if !cfg.APIEnabled || cfg.APIPort != 21121 || cfg.ClientAPIHost != "0.0.0.0" || !cfg.ClientAPIProxy || cfg.ClientAPITLS != "auto" {
		t.Errorf("client API defaults: enabled %v, port %d, host %q, proxy %v, tls %q",
			cfg.APIEnabled, cfg.APIPort, cfg.ClientAPIHost, cfg.ClientAPIProxy, cfg.ClientAPITLS)
	}

	cfg, err = load(env(map[string]string{
		"HTTPS_PORT": "8443x", "HTTP_REDIRECT_HTTPS": "yes", "SSL_CERT_PATH": "/c.pem", "SSL_KEY_PATH": "/k.pem",
		"SSL_CA_PATH": "/ca.pem", "API_HOST": "10.0.0.1", "RUSTDESK_API_PROXY": "FALSE", "RUSTDESK_API_TLS": "Off",
	}), machine("/srv/bin", "/srv/run"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTPSPort != 8443 || cfg.HTTPRedirect || cfg.CertFile != "/c.pem" || cfg.KeyFile != "/k.pem" || cfg.CAFile != "/ca.pem" {
		t.Errorf("TLS settings: port %d, redirect %v, files %q %q %q", cfg.HTTPSPort, cfg.HTTPRedirect, cfg.CertFile, cfg.KeyFile, cfg.CAFile)
	}
	if cfg.ClientAPIHost != "10.0.0.1" || cfg.ClientAPIProxy || cfg.ClientAPITLS != "off" {
		t.Errorf("client API settings: host %q, proxy %v, tls %q", cfg.ClientAPIHost, cfg.ClientAPIProxy, cfg.ClientAPITLS)
	}
	for _, c := range []struct {
		value string
		want  bool
	}{{"true", true}, {"TRUE", true}, {"false", false}, {"0", false}, {"1", false}, {" true", false}} {
		cfg, _ := load(env(map[string]string{"HTTP_REDIRECT_HTTPS": c.value, "RUSTDESK_API_PROXY": c.value}), machine("/srv/bin", "/srv/run"))
		if cfg.HTTPRedirect != c.want || cfg.ClientAPIProxy != c.want {
			t.Errorf("%q: redirect %v, proxy %v, want %v", c.value, cfg.HTTPRedirect, cfg.ClientAPIProxy, c.want)
		}
	}
}

// API_ENABLED and RUSTDESK_API_PROXY were read with "??": set to the empty
// text they are off, unset they are on. HTTP_REDIRECT_HTTPS was read with
// "||": empty is the default.
func TestEmptyIsNotUnsetForTwoSwitches(t *testing.T) {
	values := map[string]string{"API_ENABLED": "", "RUSTDESK_API_PROXY": "", "HTTP_REDIRECT_HTTPS": ""}
	p := machine("/srv/bin", "/srv/run")
	p.isSet = func(name string) bool { _, ok := values[name]; return ok }
	cfg, err := load(env(values), p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.APIEnabled || cfg.ClientAPIProxy || !cfg.HTTPRedirect {
		t.Errorf("set to empty: API enabled %v, proxy %v, redirect %v", cfg.APIEnabled, cfg.ClientAPIProxy, cfg.HTTPRedirect)
	}
	// Load without a presence test counts empty as unset
	cfg, _ = load(env(values), machine("/srv/bin", "/srv/run"))
	if !cfg.APIEnabled || !cfg.ClientAPIProxy {
		t.Errorf("without presence: API enabled %v, proxy %v", cfg.APIEnabled, cfg.ClientAPIProxy)
	}
}

func TestLoadEnvSeesEmptyVariables(t *testing.T) {
	t.Setenv("API_ENABLED", "")
	cfg, err := LoadEnv()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.APIEnabled {
		t.Error("API_ENABLED set to empty turned the client API port on")
	}
}
