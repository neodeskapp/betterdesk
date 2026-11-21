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
	// api is the server's API in this process; nil sends the requests to
	// origin over a connection
	api      http.Handler
	clientIP func(r *http.Request) string
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
	if f.api != nil {
		f.serveInProcess(w, r, out, ip)
		return
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

// serveInProcess hands out to the server's API in this process, as the
// API's listener reads the request the forward would send: with the
// client's address as RemoteAddr, so the API's limits per address count
// each client, and with the TLS state of the client's connection. The API
// answers the client directly.
func (f *apiForward) serveInProcess(w http.ResponseWriter, r, out *http.Request, ip string) {
	// The path and query of the request line, without the origin
	u := *out.URL
	u.Scheme, u.Host = "", ""
	in := out.WithContext(r.Context())
	in.URL, in.RequestURI, in.RemoteAddr, in.TLS = &u, u.RequestURI(), net.JoinHostPort(ip, "0"), r.TLS
	if in.Body == nil {
		in.Body = http.NoBody
	}
	if in.ContentLength < 0 {
		in.TransferEncoding = []string{"chunked"}
	}
	f.api.ServeHTTP(w, in)
}

func (f *apiForward) close() { f.transport.CloseIdleConnections() }

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

// errorBody is the JSON error the port answers with.
type errorBody struct {
	Error string `json:"error"`
}
