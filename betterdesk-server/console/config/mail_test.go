package config

import "testing"

// The two switches the mail service reads through the configuration, with
// the old console's defaults: certificates of the mail server are not
// checked unless SMTP_TLS_VERIFY is "true", and links in mails are https
// unless HTTP_REDIRECT_HTTPS is something else than "true".
func TestMailSwitches(t *testing.T) {
	for _, tt := range []struct {
		verify, redirect         string
		wantVerify, wantRedirect bool
	}{
		{"", "", false, true},
		{"true", "true", true, true},
		{"TRUE", "True", true, true},
		{"false", "false", false, false},
		{"1", "1", false, false},
		{"yes", "0", false, false},
		{" true", "true ", false, false},
		{"on", "off", false, false},
	} {
		cfg, err := load(env(map[string]string{"SMTP_TLS_VERIFY": tt.verify, "HTTP_REDIRECT_HTTPS": tt.redirect}), machine("/srv/bin", "/srv/run"))
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
	cfg, err := load(env(map[string]string{"ALLOW_SELF_SIGNED_CERTS": "true"}), machine("/srv/bin", "/srv/run"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SMTPTLSVerify {
		t.Error("ALLOW_SELF_SIGNED_CERTS changed the mail server check")
	}
}
