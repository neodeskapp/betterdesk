package console

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/unitronix/betterdesk-server/console/listen"
	"github.com/unitronix/betterdesk-server/logging"
)

// listeners are what the console listens on, as the old console's
// startServer and startRustDeskApiServer set them up:
//
//   - HTTP mode: the console on Port.
//   - HTTPS mode (HTTPS_ENABLED) with a certificate: the console with TLS
//     on HTTPSPort, and on Port a redirect to it unless
//     HTTP_REDIRECT_HTTPS is false. Ports below 1024 the process may not
//     bind become 5443 and 5000.
//   - HTTPS mode whose certificate files are missing or unreadable: the
//     console on Port with plain HTTP. Its session and CSRF cookies keep
//     their HTTPS names and Secure, so browsers drop them there (BUGS
//     LS3).
//   - The client API port (API_ENABLED) on API_HOST:API_PORT, with TLS
//     when the certificate loads and RUSTDESK_API_TLS does not turn it
//     off.
//
// Certificate files that are there but do not make a certificate stop the
// start, for the console in HTTPS mode and for the client API port with
// TLS. A console port that is taken or may not be bound stops the start
// too; the client API port is then only logged.
type listeners struct {
	cert    *tls.Certificate
	apiCert *tls.Certificate

	servers      []*http.Server
	addr         string
	redirectAddr string
	clientAPI    *listen.ClientAPI
}

// apiTLSOff are the RUSTDESK_API_TLS values that keep the client API port
// on plain HTTP.
var apiTLSOff = map[string]bool{"false": true, "0": true, "off": true, "http": true}

// loadCertificates reads and parses the certificate where the console or
// the client API port needs one, before anything listens.
func (s *Server) loadCertificates() error {
	cfg := s.cfg
	if !cfg.HTTPS && !cfg.APIEnabled {
		return nil
	}
	logf := func(format string, args ...any) { logging.Logf("console", "ERROR: "+format, args...) }
	files, ok := listen.ReadFiles(cfg.CertFile, cfg.KeyFile, cfg.CAFile, logf)
	var cert *tls.Certificate
	if ok && (cfg.HTTPS || (cfg.APIEnabled && !apiTLSOff[cfg.ClientAPITLS])) {
		var err error
		if cert, err = files.Certificate(); err != nil {
			return fmt.Errorf("certificate %s: %w", cfg.CertFile, err)
		}
	}
	if cfg.HTTPS {
		if cert != nil {
			s.net.cert = cert
		} else {
			logging.Logf("console", "WARN: HTTPS enabled but certificates not found/invalid")
			logging.Logf("console", "WARN: Falling back to HTTP mode")
			logging.Logf("console", "WARN:   → Session and CSRF cookies keep their HTTPS names and Secure")
			logging.Logf("console", "WARN:   → Fix: check SSL_CERT_PATH and SSL_KEY_PATH in .env")
		}
	}
	if !cfg.APIEnabled {
		return nil
	}
	switch {
	case ok && !apiTLSOff[cfg.ClientAPITLS]:
		s.net.apiCert = cert
		logging.Logf("client-api", "API TLS: Enabled (HTTPS on :%d)", cfg.APIPort)
	case (cfg.CertFile != "" || cfg.KeyFile != "") && cfg.ClientAPITLS != "false":
		logging.Logf("client-api", "WARN: SSL certs configured but invalid — API running insecure HTTP on :%d", cfg.APIPort)
	}
	if ok && cfg.ClientAPITLS == "false" {
		logging.Logf("client-api", "WARN: RUSTDESK_API_TLS=false — RustDesk Client API is HTTP on :%d. Use only behind a trusted network/VPN or with a low-privilege account.", cfg.APIPort)
	}
	return nil
}

// newClientAPI builds the client API port when API_ENABLED is on. In
// proxy mode it passes requests to the server's API at the scheme of
// BETTERDESK_API_URL and the host the console calls the API by.
func (s *Server) newClientAPI() {
	cfg := s.cfg
	if !cfg.APIEnabled {
		return
	}
	origin := &url.URL{Scheme: "http", Host: cfg.APIHost}
	if u, err := url.Parse(cfg.APIURL); err == nil && strings.EqualFold(u.Scheme, "https") {
		origin.Scheme = "https"
	}
	s.net.clientAPI = listen.NewClientAPI(listen.ClientAPIOptions{
		Deps:        s.deps,
		Local:       clientAPILocalRoutes,
		Shared:      clientAPISharedRoutes,
		Proxy:       cfg.ClientAPIProxy,
		APIOrigin:   origin,
		InsecureAPI: cfg.AllowSelfSignedCerts,
		Node:        cfg.NodeUpstream,
		TrustProxy:  cfg.TrustProxy,
	})
}

// openListeners opens the console's listeners.
func (s *Server) openListeners(ctx context.Context) error {
	cfg := s.cfg
	canBind := func() bool { return listen.CanBindLowPorts(cfg.Getenv) }
	warn := func(format string, args ...any) { logging.Logf("console", "WARN: "+format, args...) }
	// Port 0 (tests) asks the system for a free port
	resolve := func(port, fallback int, label string) int {
		if port == 0 {
			return 0
		}
		return listen.ResolvePort(port, fallback, label, canBind, warn)
	}
	plainPort := resolve(cfg.Port, 5000, "HTTP")
	if s.net.cert == nil {
		addr, err := s.serve(ctx, plainPort, "HTTP", s, nil)
		s.net.addr = addr
		return err
	}
	httpsPort := resolve(cfg.HTTPSPort, 5443, "HTTPS")
	addr, err := s.serve(ctx, httpsPort, "HTTPS", s, s.net.cert)
	if err != nil {
		return err
	}
	s.net.addr = addr
	if cfg.HTTPRedirect {
		if _, port, err := net.SplitHostPort(addr); err == nil {
			httpsPort, _ = strconv.Atoi(port)
		}
		// The host is chosen once: PANEL_PUBLIC_HOST, else the bound address
		host, _ := listen.RedirectHost(s.deps.ServerInfo.ReadPanelPublicHostValue(), cfg.Host)
		redirect, err := s.serve(ctx, plainPort, "HTTP redirect", listen.Redirect(host, httpsPort), nil)
		if err != nil {
			return err
		}
		s.net.redirectAddr = redirect
	}
	return nil
}

// startClientAPI opens the client API port, if there is one.
func (s *Server) startClientAPI(ctx context.Context) error {
	if s.net.clientAPI == nil {
		return nil
	}
	return s.net.clientAPI.Start(ctx, s.cfg.ClientAPIHost, s.cfg.APIPort, s.net.apiCert)
}

// serve listens on the console's host and the port and serves h, with
// TLS when cert is set, and returns the address it listens on. A port that
// is taken or may not be bound is an error that ends the start.
func (s *Server) serve(ctx context.Context, port int, label string, h http.Handler, cert *tls.Certificate) (string, error) {
	addr := net.JoinHostPort(s.cfg.Host, strconv.Itoa(port))
	ln, err := net.Listen("tcp", addr)
	switch {
	case err != nil && listen.Denied(err):
		logging.Logf("console", "ERROR: Cannot bind %s port %d — permission denied", label, port)
		logging.Logf("console", "ERROR:   → Ports below 1024 require root or CAP_NET_BIND_SERVICE in the systemd unit")
		logging.Logf("console", "ERROR:   → Or set HTTPS_PORT=5443 / CONSOLE_PORT=5000 and use a reverse proxy on :443")
		return "", fmt.Errorf("console %s listen on %s: %w", label, addr, err)
	case err != nil && listen.InUse(err):
		logging.Logf("console", "ERROR: %s port %d is already in use", label, port)
		return "", fmt.Errorf("console %s listen on %s: %w", label, addr, err)
	case err != nil:
		return "", fmt.Errorf("console %s listen on %s: %w", label, addr, err)
	}
	bound := ln.Addr().String()
	srv := &http.Server{
		Handler:           h,
		ReadHeaderTimeout: 15 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return ctx },
		ErrorLog:          listen.ErrorLog("console"),
	}
	switch {
	case cert != nil:
		ln = listen.TLSListener(ln, listen.ServerTLS(cert))
		srv.TLSNextProto = map[string]func(*http.Server, *tls.Conn, http.Handler){}
	case label == "HTTP redirect":
		// The redirect listener had Node's default limits
		ln = listen.PlainListener(ln)
		srv.ReadHeaderTimeout, srv.ReadTimeout, srv.IdleTimeout = 60*time.Second, 300*time.Second, 5*time.Second
		srv.MaxHeaderBytes = listen.MaxHeaderBytes
		srv.DisableGeneralOptionsHandler = true
		srv.ConnContext = listen.ConnContext
	}
	s.net.servers = append(s.net.servers, srv)
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logging.Logf("console", "%s server stopped: %v", label, err)
		}
	}()
	return bound, nil
}

// closeListeners closes every listener and gives open requests ten
// seconds to finish.
func (s *Server) closeListeners() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, srv := range s.net.servers {
		if err := srv.Shutdown(ctx); err != nil {
			_ = srv.Close()
		}
	}
	if s.net.clientAPI != nil {
		s.net.clientAPI.Stop()
	}
}

// ClientAPIAddr is the address of the client API port, "" when it does
// not listen.
func (s *Server) ClientAPIAddr() string {
	if s.net.clientAPI == nil {
		return ""
	}
	return s.net.clientAPI.Addr()
}

// ClientAPI is the client API port, nil when API_ENABLED is off.
func (s *Server) ClientAPI() *listen.ClientAPI { return s.net.clientAPI }
