package config

import "testing"

// The two switches the mail service reads through the configuration:
// certificates of the mail server are checked unless SMTP_TLS_VERIFY is
// off (BUGS EM1, L84), and links in mails are https when
// HTTP_REDIRECT_HTTPS is unset or on.
func TestMailSwitches(t *testing.T) {
	for _, tt := range []struct {
		verify, redirect         string
		wantVerify, wantRedirect bool
	}{
		{"", "", true, true},
		{"true", "true", true, true},
		{"TRUE", "True", true, true},
		{"false", "false", false, false},
		{"FALSE", "False", false, false},
		{"1", "1", true, true},
		{"yes", "0", true, false},
		{" true", "true ", true, true},
		{"off", "off", false, false},
		{"0", "", false, true},
		{"enabled", "enabled", true, false},
	} {
		cfg, err := load(env(map[string]string{"SMTP_TLS_VERIFY": tt.verify, "HTTP_REDIRECT_HTTPS": tt.redirect}), machine())
		if err != nil {
			t.Fatal(err)
		}
		if cfg.SMTPTLSVerify != tt.wantVerify {
			t.Errorf("SMTP_TLS_VERIFY=%q: verify %v, want %v", tt.verify, cfg.SMTPTLSVerify, tt.wantVerify)
		}
		if cfg.HTTPRedirect != tt.wantRedirect {
			t.Errorf("HTTP_REDIRECT_HTTPS=%q: redirect %v, want %v", tt.redirect, cfg.HTTPRedirect, tt.wantRedirect)
		}
	}
	// ALLOW_SELF_SIGNED_CERTS is about the console's own calls, not mail
	for _, allow := range []string{"true", "false"} {
		cfg, err := load(env(map[string]string{"ALLOW_SELF_SIGNED_CERTS": allow}), machine())
		if err != nil {
			t.Fatal(err)
		}
		if !cfg.SMTPTLSVerify {
			t.Errorf("ALLOW_SELF_SIGNED_CERTS=%s turned the mail server check off", allow)
		}
	}
}

// A login to the mail server needs TLS unless SMTP_REQUIRE_TLS is off,
// the switch for a mail server on a trusted network (BUGS EM10, L84).
func TestRequireTLSSwitch(t *testing.T) {
	for value, want := range map[string]bool{"": true, "true": true, "false": false, "FALSE": false, "False": false, "0": false, "no": false, " false": false, "enabled": true} {
		cfg, err := load(env(map[string]string{"SMTP_REQUIRE_TLS": value}), machine("/srv/bin", "/srv/run"))
		if err != nil {
			t.Fatal(err)
		}
		if cfg.SMTPRequireTLS != want {
			t.Errorf("SMTP_REQUIRE_TLS=%q: require %v, want %v", value, cfg.SMTPRequireTLS, want)
		}
	}
}
