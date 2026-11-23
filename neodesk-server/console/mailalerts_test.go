package console

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/config"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// TestAlertSweepRuns pins that the offline sweep is a job of the console
// and that the mail service and the alert engine are in Deps.
func TestAlertSweepRuns(t *testing.T) {
	cfg := &config.Config{
		Host: "127.0.0.1", DataDir: t.TempDir(), UploadsDir: t.TempDir(),
		LangDir: "static/files/lang", WebUIDir: t.TempDir(), DefaultLanguage: "en",
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
	if names := s.Jobs().Names(); !slices.Contains(names, "alert sweep") {
		t.Errorf("the sweep must run, jobs = %q", names)
	}
	if s.Deps().Mail == nil || s.Deps().Alerts == nil {
		t.Error("the mail service and the alert engine must be in Deps")
	}
}

// writeCertificate writes a certificate and its key as PEM files and
// returns their paths.
func writeCertificate(t *testing.T, cert tls.Certificate) (certPath, keyPath string) {
	t.Helper()
	dir := t.TempDir()
	key, err := x509.MarshalPKCS8PrivateKey(cert.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	certPath, keyPath = filepath.Join(dir, "panel.crt"), filepath.Join(dir, "panel.key")
	for path, block := range map[string]*pem.Block{certPath: {Type: "CERTIFICATE", Bytes: cert.Certificate[0]}, keyPath: {Type: "PRIVATE KEY", Bytes: key}} {
		if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return certPath, keyPath
}

// TestMailSettingsReachTheService pins what the server hands the mail
// service from the configuration: whether the mail server's certificate
// is checked when the saved settings do not say, and how the console's
// address in a mail is built: the panel's public host, with https only
// when the console serves HTTPS with a certificate (BUGS EM8).
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
	certPath, keyPath := writeCertificate(t, certificate)

	for _, tt := range []struct {
		verify     bool
		panelHost  string
		https      bool
		certFile   string
		wantError  string
		wantURL    string
		explaining string
	}{
		{true, "panel.example.com", true, certPath, "self-signed certificate", "https://panel.example.com:5443", "HTTPS with a certificate"},
		{false, "panel.example.com", true, filepath.Join(t.TempDir(), "missing.crt"), "Invalid greeting. response=421 busy: 421 busy", "http://panel.example.com:5000", "HTTPS mode without a certificate"},
		{false, "panel.example.com", false, "", "Invalid greeting. response=421 busy: 421 busy", "http://panel.example.com:5000", "plain HTTP"},
		{false, "", true, certPath, "Invalid greeting. response=421 busy: 421 busy", "", "no public host"},
	} {
		db := storetest.Migrated(t)
		stored := `{"host":"127.0.0.1","port":` + port + `,"secure":true}`
		if _, err := db.Exec(context.Background(), "INSERT INTO settings (key, value) VALUES (?, ?)", "smtp_config", stored); err != nil {
			t.Fatal(err)
		}
		cfg := &config.Config{
			Host: "0.0.0.0", Port: 5000, HTTPSPort: 5443, HTTPS: tt.https, CertFile: tt.certFile, KeyFile: keyPath,
			DataDir: t.TempDir(), UploadsDir: t.TempDir(),
			LangDir: "static/files/lang", WebUIDir: t.TempDir(), DefaultLanguage: "en", LogLevel: "silent",
			SMTPTLSVerify: tt.verify, HTTPRedirect: true,
			Getenv: func(key string) string {
				switch key {
				case "SESSION_SECRET":
					return testSecret
				case "PANEL_PUBLIC_HOST":
					return tt.panelHost
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
			t.Errorf("%s: the console's address in mails is %q, want %q", tt.explaining, got, tt.wantURL)
		}
		s.Stop()
	}
}
