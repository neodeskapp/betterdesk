package console

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/console/config"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// TestAlertSweepRunsOnlyWithoutNode pins the rule that keeps alerts from
// being raised and mailed twice: with a Node console behind this one, Node
// runs the offline sweep and the Go console must not register its own.
func TestAlertSweepRunsOnlyWithoutNode(t *testing.T) {
	build := func(upstream *url.URL) *Server {
		t.Helper()
		cfg := &config.Config{
			Host: "127.0.0.1", NodeUpstream: upstream, DataDir: t.TempDir(), UploadsDir: t.TempDir(),
			LangDir: "../../web-nodejs/lang", WebUIDir: t.TempDir(), DefaultLanguage: "en",
			Getenv: func(key string) string {
				if key == "SESSION_SECRET" {
					return testSecret
				}
				return ""
			},
		}
		s, err := New(context.Background(), Options{Config: cfg, Store: storetest.Migrated(t), API: http.NotFoundHandler()})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(s.Stop)
		return s
	}

	alone := build(nil)
	if names := alone.Jobs().Names(); !slices.Contains(names, "alert sweep") {
		t.Errorf("without a Node console the sweep must run here, jobs = %q", names)
	}
	if alone.Deps().Mail == nil || alone.Deps().Alerts == nil {
		t.Error("the mail service and the alert engine must be in Deps")
	}

	upstream, _ := url.Parse("http://127.0.0.1:1")
	front := build(upstream)
	if names := front.Jobs().Names(); slices.Contains(names, "alert sweep") {
		t.Errorf("with a Node console behind it the sweep must not run here, jobs = %q", names)
	}
	if front.Deps().Mail == nil || front.Deps().Alerts == nil {
		t.Error("the mail service and the alert engine must be in Deps in front of Node too")
	}
}

// TestMailSettingsReachTheService pins what the server hands the mail
// service from the configuration: whether the mail server's certificate
// is checked when the saved settings do not say, and how the console's
// address in a mail is built.
func TestMailSettingsReachTheService(t *testing.T) {
	// A mail server with implicit TLS and a certificate no one trusts. It
	// greets with 421, so a client that gets through the handshake fails
	// on the greeting.
	plain := httptest.NewUnstartedServer(nil)
	plain.StartTLS()
	certificate := plain.TLS.Certificates[0]
	plain.Close()
	listener, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{certificate}})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
				if _, err := conn.Write([]byte("421 busy\r\n")); err == nil {
					_, _ = io.Copy(io.Discard, conn)
				}
			}()
		}
	}()
	_, port, _ := net.SplitHostPort(listener.Addr().String())

	for _, tt := range []struct {
		verify, redirect bool
		wantError        string
		wantURL          string
	}{
		{true, true, "self-signed certificate", "https://127.0.0.1:5000"},
		{false, false, "Invalid greeting. response=421 busy: 421 busy", "http://127.0.0.1:5000"},
	} {
		db := storetest.Migrated(t)
		stored := `{"host":"127.0.0.1","port":` + port + `,"secure":true}`
		if _, err := db.Exec(context.Background(), "INSERT INTO settings (key, value) VALUES (?, ?)", "smtp_config", stored); err != nil {
			t.Fatal(err)
		}
		cfg := &config.Config{
			Host: "0.0.0.0", Port: 5000, DataDir: t.TempDir(), UploadsDir: t.TempDir(),
			LangDir: "../../web-nodejs/lang", WebUIDir: t.TempDir(), DefaultLanguage: "en", LogLevel: "silent",
			SMTPTLSVerify: tt.verify, HTTPRedirect: tt.redirect,
			Getenv: func(key string) string {
				if key == "SESSION_SECRET" {
					return testSecret
				}
				return ""
			},
		}
		s, err := New(context.Background(), Options{Config: cfg, Store: db, API: http.NotFoundHandler()})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		result, err := s.Deps().Mail.TestConnection(ctx)
		cancel()
		if err != nil || result.Success || result.Error == nil || *result.Error != tt.wantError {
			got := "<none>"
			if result.Error != nil {
				got = *result.Error
			}
			t.Errorf("SMTP_TLS_VERIFY %v: the test answered %v, %q (%v), want the failure %q", tt.verify, result.Success, got, err, tt.wantError)
		}
		if got := s.Deps().Mail.GetPanelBaseURL(); got != tt.wantURL {
			t.Errorf("HTTP_REDIRECT_HTTPS %v: the console's address in mails is %q, want %q", tt.redirect, got, tt.wantURL)
		}
		s.Stop()
	}
}
