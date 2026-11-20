package listen

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

	"github.com/unitronix/betterdesk-server/console/httpx"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/limits"
	"github.com/unitronix/betterdesk-server/console/middleware"
	"github.com/unitronix/betterdesk-server/logging"
)

// The client API port's connection limits: the headers and the body of a
// request within 10 seconds, a kept-alive connection idle for at most 5,
// and a request line with headers of 16 KB at most.
const (
	clientRequestTimeout = limits.WANRequestTimeout
	clientIdleTimeout    = limits.WANKeepAliveTimeout
	// MaxHeaderBytes plus the 4 KB net/http allows on top is 16 KB, Node's
	// limit for the request line and headers.
	MaxHeaderBytes = 16<<10 - 4<<10
	// jsonLimit is the JSON parser of the port in local mode: 64 KB.
	jsonLimit = 64 << 10
)

// ClientAPIOptions configure the client API port.
type ClientAPIOptions struct {
	// Deps are the console's. The port registers routes on a copy whose
	// Early is a router of its own.
	Deps *kit.Deps
	// Local are the route groups the port answers itself in local mode
	// (Proxy false): rustdeskapi.Register.
	Local []func(d *kit.Deps)
	// Shared are the route groups the port answers in both modes.
	Shared []func(d *kit.Deps)
	// Proxy passes every request the port lets through to the server's
	// API at APIOrigin (RUSTDESK_API_PROXY).
	Proxy     bool
	APIOrigin *url.URL
	// InsecureAPI accepts any certificate of an https APIOrigin
	// (ALLOW_SELF_SIGNED_CERTS).
	InsecureAPI bool
	// Node is the Node console. Until the registration routes move, the
	// port passes them there; nil answers them as any unknown path.
	Node *url.URL
	// TrustProxy decides the client's address, as TRUST_PROXY did there.
	TrustProxy httpx.TrustProxy
	// Now is the clock of the rate limiters. nil means time.Now.
	Now func() time.Time
}

// ClientAPI is the listener for RustDesk clients (API_PORT, 21121), facing
// the internet: everything passes limits.WANStack (the path list, the
// rate limits, the body limits, the headers), then
//
//   - in proxy mode the registration routes, then the server's API;
//   - in local mode the JSON parser (64 KB, objects and arrays only), the
//     console's client API routes, the registration routes, then an empty
//     404.
//
// A request whose JSON does not parse gets 400 {"error":"Invalid JSON"},
// one over 64 KB 413, any other body error 500. CONNECT is closed without
// an answer, a request target with bytes above 0x7f gets 400.
type ClientAPI struct {
	handler      http.Handler
	router       *kit.Router
	forward      *apiForward
	registration *nodeRegistration
	proxy        bool
	srv          *http.Server
	addr         string
}

// NewClientAPI builds the port's handler and registers its routes.
func NewClientAPI(opts ClientAPIOptions) *ClientAPI {
	c := &ClientAPI{router: kit.NewRouter(), proxy: opts.Proxy}
	d := *opts.Deps
	d.Early = c.router
	d.Router = kit.NewRouter()
	if !opts.Proxy {
		for _, register := range opts.Local {
			register(&d)
		}
	}
	for _, register := range opts.Shared {
		register(&d)
	}
	if opts.Node != nil {
		c.registration = newNodeRegistration(opts.Node, !opts.Proxy)
	}
	if opts.Proxy {
		c.forward = newAPIForward(opts.APIOrigin, opts.InsecureAPI, opts.TrustProxy.ClientIP)
	}
	var bundle = d.I18n
	trust := opts.TrustProxy
	routes := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if bundle != nil {
			r = kit.Begin(r, trust, bundle.Bundle)
		} else {
			r = kit.Begin(r, trust, nil)
		}
		if c.router.Serve(w, r) || c.registration.serve(w, r) {
			return
		}
		if c.forward != nil {
			c.forward.ServeHTTP(w, r)
			return
		}
		notFound(w, r)
	})
	parsed := middleware.RouteJSON(middleware.BodyOptions{Limit: jsonLimit, OnError: jsonError})(routes)
	inner := keepRawBody(parsed)
	if opts.Proxy {
		// The forward passes the body on as it came; the port's own routes
		// read it as JSON, as in local mode
		inner = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if c.router.Match(r) {
				parsed.ServeHTTP(w, r)
				return
			}
			routes.ServeHTTP(w, r)
		})
	}
	stack := limits.WANStack(limits.WANOptions{ClientIP: trust.ClientIP, Now: opts.Now})(inner)
	c.handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		Served(r)
		if r.Method == http.MethodConnect {
			closeUnanswered(w)
			return
		}
		if nonASCII(r.RequestURI) || r.ProtoMajor != 1 {
			badRequest(w)
			return
		}
		stack.ServeHTTP(w, r)
	})
	return c
}

// Handler is what the port answers with.
func (c *ClientAPI) Handler() http.Handler { return c.handler }

// Router is the port's own route table.
func (c *ClientAPI) Router() *kit.Router { return c.router }

// jsonError answers a body the JSON parser refused, as the port's error
// handler did.
func jsonError(w http.ResponseWriter, r *http.Request, _ int, err error) {
	var bodyErr *middleware.BodyError
	errors.As(err, &bodyErr)
	switch {
	case bodyErr != nil && bodyErr.Type == middleware.ErrTypeParseFailed:
		logging.Logf("client-api", "WARN: RustDesk API: JSON parse error from %s", httpx.SocketIP(r))
		httpx.WriteJSON(w, r, http.StatusBadRequest, errorBody{"Invalid JSON"})
	case bodyErr != nil && bodyErr.Type == middleware.ErrTypeTooLarge:
		httpx.WriteJSON(w, r, http.StatusRequestEntityTooLarge, errorBody{"Request too large"})
	default:
		logging.Logf("client-api", "ERROR: RustDesk API error: %v", err)
		httpx.WriteJSON(w, r, http.StatusInternalServerError, errorBody{"Server error"})
	}
}

// notFound is the port's answer to what no route took: 404 without a
// body, and a log line with the address and the user agent.
func notFound(w http.ResponseWriter, r *http.Request) {
	address := strings.Join(r.Header.Values("X-Forwarded-For"), ", ")
	if address == "" {
		address = httpx.SocketIP(r)
	}
	if address == "" {
		address = "unknown"
	}
	agent := r.UserAgent()
	if len(agent) > 80 {
		agent = agent[:80]
	}
	logging.Logf("client-api", "WARN: [rustdesk-api] 404 %s %s from %s ua=%q", r.Method, httpx.OriginalURL(r), address, agent)
	w.WriteHeader(http.StatusNotFound)
}

// Start listens on host:port, with TLS when cert is not nil, and serves
// until ctx ends or Stop is called. A port that is taken or may not be
// bound is logged and left alone, and the console runs on without it;
// any other listen error is returned.
func (c *ClientAPI) Start(ctx context.Context, host string, port int, cert *tls.Certificate) error {
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	ln, err := net.Listen("tcp", addr)
	switch {
	case err != nil && InUse(err):
		logging.Logf("client-api", "ERROR: API Port: %d FAILED (port in use)", port)
		logging.Logf("client-api", "ERROR: Hint: Check if hbbs uses the same port, or set API_PORT env var (default: 21121)")
		logging.Logf("client-api", "ERROR: WARNING: RustDesk Client API could not start on port %d", port)
		logging.Logf("client-api", "ERROR: Likely cause: hbbs API is on the same port. Client API default is 21121.")
		logging.Logf("client-api", "ERROR: The admin panel continues to run normally")
		return nil
	case err != nil && Denied(err):
		logging.Logf("client-api", "ERROR: WARNING: RustDesk Client API could not bind port %d (permission denied)", port)
		logging.Logf("client-api", "ERROR: Ports below 1024 require root or CAP_NET_BIND_SERVICE — set API_PORT to a high port.")
		return nil
	case err != nil:
		return fmt.Errorf("client API listen on %s: %w", addr, err)
	}
	if cert != nil {
		ln = TLSListener(ln, ServerTLS(cert))
	} else {
		ln = PlainListener(ln)
	}
	c.addr = ln.Addr().String()
	c.srv = &http.Server{
		Handler:                      c.handler,
		ReadHeaderTimeout:            clientRequestTimeout,
		ReadTimeout:                  clientRequestTimeout,
		IdleTimeout:                  clientIdleTimeout,
		MaxHeaderBytes:               MaxHeaderBytes,
		DisableGeneralOptionsHandler: true,
		TLSNextProto:                 map[string]func(*http.Server, *tls.Conn, http.Handler){},
		ErrorLog:                     ErrorLog("client-api"),
		BaseContext:                  func(net.Listener) context.Context { return ctx },
		ConnContext:                  ConnContext,
	}
	go func() {
		if err := c.srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logging.Logf("client-api", "server stopped: %v", err)
		}
	}()
	target := "answered here"
	if c.proxy {
		target = "passed on to the server's API"
	}
	scheme := "HTTP"
	if cert != nil {
		scheme = "HTTPS"
	}
	logging.Logf("client-api", "listening on %s (%s, %s)", ln.Addr(), scheme, target)
	return nil
}

// Addr is the address the port listens on, "" when it does not.
func (c *ClientAPI) Addr() string {
	return c.addr
}

// Stop closes the listener, gives open requests ten seconds and drops
// the connections to the server's API and the Node console.
func (c *ClientAPI) Stop() {
	if c.srv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := c.srv.Shutdown(ctx); err != nil {
			_ = c.srv.Close()
		}
	}
	if c.forward != nil {
		c.forward.close()
	}
	c.registration.close()
}
