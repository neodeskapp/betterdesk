package listen

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/unitronix/betterdesk-server/console/httpx"
	"github.com/unitronix/betterdesk-server/logging"
)

// apiTimeout is how long the server's API may stay silent before the
// forward gives up with 504.
const apiTimeout = 15 * time.Second

// apiForward passes a request of the client API port on to the server's
// API (middleware/goApiProxy.js): same method, path, query and body, the
// client's headers without the hop-by-hop ones, X-Forwarded-For and
// X-Real-IP set to the client's address under TRUST_PROXY, Host set to the
// API's. The answer comes back with its
// status, headers and body; its headers replace those the port had set.
type apiForward struct {
	origin    *url.URL
	transport *http.Transport
	clientIP  func(r *http.Request) string
}

func newAPIForward(origin *url.URL, insecure bool, clientIP func(r *http.Request) string) *apiForward {
	transport := &http.Transport{
		Proxy:                 nil,
		DialContext:           (&net.Dialer{Timeout: apiTimeout, KeepAlive: 30 * time.Second}).DialContext,
		MaxIdleConns:          64,
		IdleConnTimeout:       5 * time.Second,
		ResponseHeaderTimeout: apiTimeout,
		DisableCompression:    true,
		TLSClientConfig:       &tls.Config{InsecureSkipVerify: insecure}, //nolint:gosec // ALLOW_SELF_SIGNED_CERTS, as the old console
	}
	return &apiForward{origin: origin, transport: transport, clientIP: clientIP}
}

// hopByHop are the request headers the forward drops.
var hopByHop = []string{"Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization", "Te", "Trailers", "Transfer-Encoding", "Upgrade"}

// bodylessMethods are the methods Node sends without chunked encoding
// when their length is unknown.
var bodylessMethods = map[string]bool{"GET": true, "HEAD": true, "DELETE": true, "OPTIONS": true, "TRACE": true, "CONNECT": true}

func (f *apiForward) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	_, hasTrailer := r.Header["Trailer"]
	// net/http moves the Trailer header of a chunked request to r.Trailer
	hasTrailer = hasTrailer || len(r.Trailer) > 0
	_, hasLength := r.Header["Content-Length"]
	if hasTrailer && (hasLength || bodylessMethods[r.Method]) {
		// Node's request to the API threw here and the whole console
		// ended (BUGS LS1); this ends the one connection without an answer
		logging.Logf("client-api", "WARN: [rustdesk-api-proxy] %s %s refused: Trailer header on a request without chunked encoding", r.Method, httpx.RawPath(r))
		panic(http.ErrAbortHandler)
	}
	out := &http.Request{
		Method:     r.Method,
		URL:        &url.URL{Scheme: f.origin.Scheme, Host: f.origin.Host, Path: requestPath(r), RawQuery: forwardQuery(r)},
		Proto:      "HTTP/1.1",
		ProtoMajor: 1,
		ProtoMinor: 1,
		Header:     r.Header.Clone(),
		Host:       f.origin.Host,
	}
	for _, name := range hopByHop {
		out.Header.Del(name)
	}
	// The API learns the client's address from the console alone: what the
	// client wrote into X-Forwarded-For or X-Real-IP counts only through a
	// hop TRUST_PROXY trusts
	out.Header.Del("X-Forwarded-For")
	out.Header.Del("X-Real-IP")
	ip := f.clientIP(r)
	if ip == "" {
		ip = httpx.SocketIP(r)
	}
	if ip != "" {
		out.Header.Set("X-Forwarded-For", ip)
		out.Header.Set("X-Real-IP", ip)
	}
	out.Header.Set("Connection", "keep-alive")
	if len(r.Trailer) > 0 {
		out.Trailer = r.Trailer.Clone()
	}
	if _, ok := out.Header["User-Agent"]; !ok {
		out.Header["User-Agent"] = []string{""}
	}
	body, _ := io.ReadAll(r.Body)
	switch {
	case hasLength:
		out.ContentLength = int64(len(body))
		out.Body = io.NopCloser(bytes.NewReader(body))
	case len(r.TransferEncoding) > 0 && !bodylessMethods[r.Method]:
		out.ContentLength = -1
		out.Body = io.NopCloser(bytes.NewReader(body))
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	res, err := f.transport.RoundTrip(out.WithContext(ctx))
	if err != nil {
		var timeout interface{ Timeout() bool }
		if errors.As(err, &timeout) && timeout.Timeout() {
			httpx.WriteJSON(w, r, http.StatusGatewayTimeout, errorBody{"Gateway Timeout"})
			return
		}
		logging.Logf("client-api", "ERROR: [rustdesk-api-proxy] %s %s → %s://%s: %v", r.Method, httpx.RawPath(r), f.origin.Scheme, f.origin.Host, err)
		httpx.WriteJSON(w, r, http.StatusBadGateway, errorBody{"Bad Gateway"})
		return
	}
	defer res.Body.Close()
	h := w.Header()
	for name, values := range res.Header {
		h[name] = values
	}
	// net/http takes "Connection: close" out of the answer; Node passed it on
	if res.Close {
		h.Set("Connection", "close")
	}
	w.WriteHeader(res.StatusCode)
	_, _ = io.Copy(w, res.Body)
}

func (f *apiForward) close() { f.transport.CloseIdleConnections() }

// firstForwarded is the first address of X-Forwarded-For as the client
// sent it, trimmed.
func firstForwarded(r *http.Request) string {
	first, _, _ := strings.Cut(strings.Join(r.Header.Values("X-Forwarded-For"), ", "), ",")
	return strings.TrimSpace(first)
}

// requestPath is the path of the request as the client wrote it. On the
// client API port it is always one of the listed paths.
func requestPath(r *http.Request) string {
	return httpx.RawPath(r)
}

// forwardQuery is the query of the request as Node passed it on: what
// follows "?" up to a "#", with the characters a URL query may not carry
// raw (", <, >, ' and controls) percent-encoded, as new URL() did.
func forwardQuery(r *http.Request) string {
	target := r.RequestURI
	if !strings.HasPrefix(target, "/") {
		target = r.URL.RequestURI()
	}
	if i := strings.IndexByte(target, '#'); i >= 0 {
		target = target[:i]
	}
	_, q, ok := strings.Cut(target, "?")
	if !ok {
		return ""
	}
	var b strings.Builder
	for i := 0; i < len(q); i++ {
		c := q[i]
		if c <= ' ' || c >= 0x7f || c == '"' || c == '<' || c == '>' || c == '\'' {
			fmt.Fprintf(&b, "%%%02X", c)
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

// registrationRoutes are the two registration routes of the client API
// port and their methods. The path list lets nothing else under /api/bd
// through.
var registrationRoutes = map[string]string{
	"/api/bd/register-request": http.MethodPost,
	"/api/bd/register-status":  http.MethodGet,
}

// nodeRegistration passes the two registration routes on to the Node
// console, where they are mounted below /api/bd on the console port, until
// they move to Go. Only these two exact requests go; the path sent is the
// constant, never the client's, and the query goes as written.
type nodeRegistration struct {
	node      *url.URL
	transport *http.Transport
	// withBody says whether the body goes along. In proxy mode the old
	// port read no JSON for these routes, so they never saw one (BUGS I1).
	withBody bool
}

func newNodeRegistration(node *url.URL, withBody bool) *nodeRegistration {
	return &nodeRegistration{node: node, withBody: withBody, transport: &http.Transport{
		Proxy:                 nil,
		MaxIdleConns:          16,
		IdleConnTimeout:       30 * time.Second,
		ResponseHeaderTimeout: apiTimeout,
		DisableCompression:    true,
	}}
}

// forwardedHeaders are the request headers the registration routes may
// read; nothing else of the client's goes to the console port.
var forwardedHeaders = []string{"Content-Type", "Content-Encoding", "User-Agent", "Accept", "If-None-Match", "If-Modified-Since"}

// serve answers one of the two routes and reports true, or reports false
// for any other request.
func (n *nodeRegistration) serve(w http.ResponseWriter, r *http.Request) bool {
	p := httpx.RawPath(r)
	method, ok := registrationRoutes[p]
	if !ok || r.Method != method || n == nil {
		return false
	}
	target := *n.node
	target.Path = strings.TrimSuffix(target.Path, "/") + p
	target.RawPath = ""
	target.RawQuery = forwardQuery(r)
	out := &http.Request{
		Method: r.Method, URL: &target, Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1,
		Header: http.Header{}, Host: r.Host,
	}
	for _, name := range forwardedHeaders {
		if values := r.Header.Values(name); len(values) > 0 {
			out.Header[name] = values
		}
	}
	if _, ok := out.Header["User-Agent"]; !ok {
		out.Header["User-Agent"] = []string{""}
	}
	// The route records the first X-Forwarded-For address, else
	// X-Real-IP, else the socket; the console behind trusts this hop
	socket := httpx.SocketIP(r)
	address := firstForwarded(r)
	if address == "" {
		address = strings.Join(r.Header.Values("X-Real-IP"), ", ")
	}
	if address == "" || address == socket {
		out.Header.Set("X-Forwarded-For", socket)
	} else {
		out.Header.Set("X-Forwarded-For", address+", "+socket)
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	out.Header.Set("X-Forwarded-Proto", scheme)
	out.Header.Set("X-Forwarded-Host", r.Host)
	if n.withBody && method == http.MethodPost {
		body, _ := r.Context().Value(rawBodyKey{}).([]byte)
		out.ContentLength = int64(len(body))
		out.Body = io.NopCloser(bytes.NewReader(body))
	} else if method == http.MethodPost {
		out.Header.Set("Content-Type", "application/json")
		out.ContentLength = 0
		out.Body = http.NoBody
	}
	res, err := n.transport.RoundTrip(out.WithContext(r.Context()))
	if err != nil {
		logging.Logf("client-api", "ERROR: registration route %s %s: the Node console did not answer: %v", r.Method, p, err)
		httpx.WriteJSON(w, r, http.StatusBadGateway, errorBody{"Bad Gateway"})
		return true
	}
	defer res.Body.Close()
	h := w.Header()
	for _, name := range []string{"Content-Type", "ETag"} {
		if value := res.Header.Get(name); value != "" {
			h.Set(name, value)
		}
	}
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusNoContent && res.StatusCode != http.StatusNotModified {
		h.Set("Content-Length", fmt.Sprint(len(body)))
	}
	w.WriteHeader(res.StatusCode)
	_, _ = w.Write(body)
	return true
}

func (n *nodeRegistration) close() {
	if n != nil {
		n.transport.CloseIdleConnections()
	}
}

type errorBody struct {
	Error string `json:"error"`
}

type rawBodyKey struct{}

// keepRawBody keeps the body as it arrived for the registration forward,
// before the JSON parser reads it: the Node console parses it again with
// the same parser, so it gets the bytes, their type and their encoding.
func keepRawBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(body))
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), rawBodyKey{}, body)))
	})
}
