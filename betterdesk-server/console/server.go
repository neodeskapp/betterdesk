// Package console serves the web console on its own port: the JSON API, the
// websockets and the built web app (web-ui/build).
//
// While routes move from the Node console to Go, a request no Go route
// handles goes to the Node console on its internal port (package proxy).
package console

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/unitronix/betterdesk-server/console/config"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/proxy"
	"github.com/unitronix/betterdesk-server/logging"
)

// Server is the console's HTTP server.
type Server struct {
	cfg      *config.Config
	router   *kit.Router
	fallback http.Handler
	httpSrv  *http.Server
	listener net.Listener
}

// New builds the console server from its configuration.
func New(cfg *config.Config) *Server {
	s := &Server{cfg: cfg, router: kit.NewRouter()}
	if cfg.NodeUpstream != nil {
		s.fallback = proxy.New(cfg.NodeUpstream, cfg.TrustProxy)
	} else {
		s.fallback = http.HandlerFunc(notFound)
	}
	if cfg.DebugRouting {
		s.router.Get(proxy.RoutingPath, proxy.Routing(s.handles, cfg.NodeUpstream != nil))
	}
	return s
}

// Router is the route table route groups register on.
func (s *Server) Router() *kit.Router { return s.router }

// ServeHTTP answers from the Go route table, and hands everything else to
// the fallback: the Node console while it still serves routes, a 404 after.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h, ok := s.router.Lookup(r); ok {
		h.ServeHTTP(w, r)
		return
	}
	s.fallback.ServeHTTP(w, r)
}

// handles reports whether a Go route answers r, by the lookup ServeHTTP uses.
func (s *Server) handles(r *http.Request) bool {
	_, ok := s.router.Lookup(r)
	return ok
}

// Start opens the listener and serves until ctx ends or Stop is called.
func (s *Server) Start(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.cfg.Addr())
	if err != nil {
		return fmt.Errorf("console listen on %s: %w", s.cfg.Addr(), err)
	}
	s.listener = ln
	s.httpSrv = &http.Server{
		Handler:           s,
		ReadHeaderTimeout: 15 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}
	go func() {
		if err := s.httpSrv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logging.Logf("console", "server stopped: %v", err)
		}
	}()
	target := "Go only"
	if s.cfg.NodeUpstream != nil {
		target = "unported routes go to " + s.cfg.NodeUpstream.Host
	}
	logging.Logf("console", "listening on %s (%s)", ln.Addr(), target)
	return nil
}

// Addr is the address the server listens on, once started.
func (s *Server) Addr() string {
	if s.listener == nil {
		return ""
	}
	return s.listener.Addr().String()
}

// Stop closes the listener and gives open requests ten seconds to finish.
func (s *Server) Stop() {
	if s.httpSrv == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := s.httpSrv.Shutdown(ctx); err != nil {
		_ = s.httpSrv.Close()
	}
}

func notFound(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusNotFound)
	_ = json.NewEncoder(w).Encode(map[string]any{"success": false, "error": "Not Found"})
}
