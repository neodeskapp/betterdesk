package goapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/unitronix/betterdesk-server/api"
	"github.com/unitronix/betterdesk-server/events"
	"github.com/unitronix/betterdesk-server/logging"
)

const (
	// BridgeUsername and BridgeRole are the caller the API server sees for
	// every call: the console's API key.
	BridgeUsername = "apikey:console-bridge"
	BridgeRole     = "admin"

	// DefaultHost is the Host the API handlers see when none is configured:
	// the address the console reaches the API on by default.
	DefaultHost = "127.0.0.1:21114"

	// DefaultRemoteAddr is the address the API handlers see as the caller's:
	// loopback, where the console's calls come from. Audit entries and the
	// limits that count per address use it.
	DefaultRemoteAddr = "127.0.0.1:0"

	// DefaultTimeout is how long a call may take (BETTERDESK_API_TIMEOUT).
	DefaultTimeout = 5 * time.Second

	apiPrefix    = "/api"
	maxRedirects = 21
)

// Client calls the API server's handler in process. Every call builds a
// request, runs the handler against a recorder and returns what the handler
// wrote. A Client is safe for concurrent use.
type Client struct {
	handler    http.Handler
	identity   api.Identity
	host       string
	remoteAddr string
	timeout    time.Duration
	bus        *events.Bus
	now        func() time.Time
	// signInCap is the most a sign-in call with a capped timeout may take.
	// Tests that sign in against real password hashing raise it.
	signInCap time.Duration

	cacheMu     sync.Mutex
	healthAt    time.Time
	healthOK    bool
	healthKnown bool
	ssoAt       time.Time
	sso         *Object
}

// Option configures a Client.
type Option func(*Client)

// WithHost sets the Host the API handlers see: the host and port of the API
// address the console is configured with. Pairing codes and the mesh agent
// file carry it.
func WithHost(host string) Option {
	return func(c *Client) {
		if host != "" {
			c.host = host
		}
	}
}

// WithRemoteAddr sets the caller address ("ip:port") the API handlers see.
func WithRemoteAddr(addr string) Option {
	return func(c *Client) {
		if addr != "" {
			c.remoteAddr = addr
		}
	}
}

// WithTimeout sets how long a call may take. Zero means no limit.
func WithTimeout(d time.Duration) Option {
	return func(c *Client) { c.timeout = d }
}

// WithEventBus gives the client the server's event bus for Events.
func WithEventBus(bus *events.Bus) Option {
	return func(c *Client) { c.bus = bus }
}

// New returns a client for handler, which is api.Server.InternalHandler()
// outside tests. Calls act as the console's API key.
func New(handler http.Handler, opts ...Option) *Client {
	c := &Client{
		handler:    handler,
		identity:   api.Identity{Username: BridgeUsername, Role: BridgeRole},
		host:       DefaultHost,
		remoteAddr: DefaultRemoteAddr,
		timeout:    DefaultTimeout,
		now:        time.Now,
		signInCap:  signInTimeout,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// Handler returns the API server's handler for requests that cannot go
// through a recorder, such as websocket upgrades. The request it passes on
// carries the client's identity, Host and caller address; path, query,
// headers and body are the caller's.
func (c *Client) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(api.WithIdentity(r.Context(), c.identity))
		r.Host = c.host
		r.RemoteAddr = c.remoteAddr
		c.handler.ServeHTTP(w, r)
	})
}

// Response is an answer from the API server.
type Response struct {
	Status int
	Header http.Header
	Body   []byte
}

// Text returns the body as text, without a byte order mark.
func (r *Response) Text() string {
	return strings.TrimPrefix(string(r.Body), string(rune(0xFEFF)))
}

// Data returns the body the way axios handed it to the Node code as
// response.data: the parsed JSON when the body is JSON, whatever the
// Content-Type says, and the text otherwise. An empty body is "".
func (r *Response) Data() any {
	text := r.Text()
	if text == "" {
		return ""
	}
	v, err := Parse([]byte(text))
	if err != nil {
		return text
	}
	return v
}

// Decode parses the body as JSON into v.
func (r *Response) Decode(v any) error {
	return json.Unmarshal([]byte(r.Text()), v)
}

// Error is a failed call, with the fields the Node code read from an axios
// error. Response is nil when the API server gave no answer.
type Error struct {
	// Message is err.message: "Request failed with status code 404",
	// "timeout of 5000ms exceeded", or the path guard's text.
	Message string
	// Code is err.code: ERR_BAD_REQUEST for a 4xx answer, ERR_BAD_RESPONSE
	// for a 5xx answer, ECONNABORTED for a timeout, ERR_CANCELED when the
	// caller's context ended, ECONNRESET when the handler died. Empty for
	// the path guard and for a 3xx answer.
	Code string
	// Response is err.response.
	Response *Response
}

func (e *Error) Error() string { return e.Message }

// Status returns err.response.status, or 0 when there was no answer.
func (e *Error) Status() int {
	if e == nil || e.Response == nil {
		return 0
	}
	return e.Response.Status
}

// Data returns err.response.data, or nil when there was no answer.
func (e *Error) Data() any {
	if e == nil || e.Response == nil {
		return nil
	}
	return e.Response.Data()
}

// AsError returns err as an *Error, or nil when it is none.
func AsError(err error) *Error {
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	return nil
}

// call is one request in the making.
type call struct {
	query          []queryPair
	hasBody        bool
	body           []byte
	header         http.Header
	dropHeader     []string
	timeout        time.Duration
	timeoutSet     bool
	noRedirects    bool
	validateStatus func(int) bool
	outsideAPI     bool
	err            error
}

// CallOption changes one call.
type CallOption func(*call)

// Query adds one query parameter. Value is turned into text the way axios
// did it for a params entry: nil is left out, a slice becomes key[]= per
// element, anything else is its JavaScript string form.
func Query(key string, value any) CallOption {
	return func(c *call) { c.query = appendParam(c.query, key, value) }
}

// QueryParams adds the entries of an axios params object: an *Object, a
// map[string]any or url.Values.
func QueryParams(params any) CallOption {
	return func(c *call) { c.query = append(c.query, paramsOf(params)...) }
}

// Body sets the request body to v as JSON. A string follows the axios rule:
// it is sent as it is when it already is JSON, and as a JSON string
// otherwise.
func Body(v any) CallOption {
	return func(c *call) {
		if s, ok := v.(string); ok {
			if _, err := Parse([]byte(s)); err == nil {
				c.body, c.hasBody = []byte(jsTrim(s)), true
				return
			}
		}
		var buf bytes.Buffer
		if err := encodeValue(&buf, v); err != nil {
			c.err = fmt.Errorf("encode request body: %w", err)
			return
		}
		c.body, c.hasBody = buf.Bytes(), true
	}
}

// RawBody sets the request body to b as it is.
func RawBody(b []byte) CallOption {
	return func(c *call) { c.body, c.hasBody = b, true }
}

// Header sets a request header.
func Header(key, value string) CallOption {
	return func(c *call) { c.header.Set(key, value) }
}

// Timeout sets how long this call may take, instead of the client's
// timeout. Zero means no limit.
func Timeout(d time.Duration) CallOption {
	return func(c *call) { c.timeout, c.timeoutSet = d, true }
}

// NoRedirects makes the call return a redirect instead of following it
// (axios maxRedirects: 0).
func NoRedirects() CallOption {
	return func(c *call) { c.noRedirects = true }
}

// ValidateStatus replaces the rule for which statuses are a success (axios
// validateStatus). Without it 200 to 299 are.
func ValidateStatus(ok func(status int) bool) CallOption {
	return func(c *call) { c.validateStatus = ok }
}

// outsideAPI makes the path absolute on the API server instead of relative
// to /api.
func outsideAPI() CallOption {
	return func(c *call) { c.outsideAPI = true }
}

// withoutHeader removes one of the default request headers.
func withoutHeader(key string) CallOption {
	return func(c *call) { c.dropHeader = append(c.dropHeader, key) }
}

// Get sends a GET request. The path is relative to /api, like the paths the
// Node code gave its axios client: Get(ctx, "/peers") calls GET /api/peers.
//
// The result follows axios. A status from 200 to 299 returns the response
// and a nil error. Every other status returns the response and an *Error
// whose Message is "Request failed with status code N" and whose Response
// is that same response, so err.response.status is AsError(err).Status()
// and err.response.data is AsError(err).Data(). When the API server gives
// no answer the response is nil and the *Error has no Response: after the
// timeout ("timeout of 5000ms exceeded"), when ctx ends ("canceled"), when
// the handler panics ("socket hang up"), and when the path fails the guard
// the Node client had (no "..", no backslash, no absolute URL), in which
// case no request is made.
//
// A redirect to a path on the API server is followed, as axios followed it.
func (c *Client) Get(ctx context.Context, path string, opts ...CallOption) (*Response, error) {
	return c.Request(ctx, http.MethodGet, path, opts...)
}

// Delete sends a DELETE request. See Get.
func (c *Client) Delete(ctx context.Context, path string, opts ...CallOption) (*Response, error) {
	return c.Request(ctx, http.MethodDelete, path, opts...)
}

// Post sends a POST request, with the body the Body option sets. See Get.
func (c *Client) Post(ctx context.Context, path string, opts ...CallOption) (*Response, error) {
	return c.Request(ctx, http.MethodPost, path, opts...)
}

// Put sends a PUT request, with the body the Body option sets. See Get.
func (c *Client) Put(ctx context.Context, path string, opts ...CallOption) (*Response, error) {
	return c.Request(ctx, http.MethodPut, path, opts...)
}

// Patch sends a PATCH request, with the body the Body option sets. See Get.
func (c *Client) Patch(ctx context.Context, path string, opts ...CallOption) (*Response, error) {
	return c.Request(ctx, http.MethodPatch, path, opts...)
}

// Request sends a request with any method. See Get.
func (c *Client) Request(ctx context.Context, method, path string, opts ...CallOption) (*Response, error) {
	cl := &call{header: http.Header{}, timeout: c.timeout}
	cl.header.Set("Accept", "application/json, text/plain, */*")
	cl.header.Set("Content-Type", "application/json")
	cl.header.Set("User-Agent", "betterdesk-console")
	for _, opt := range opts {
		opt(cl)
	}
	for _, key := range cl.dropHeader {
		cl.header.Del(key)
	}
	if cl.err != nil {
		return nil, &Error{Message: cl.err.Error()}
	}
	if err := checkPath(path); err != nil {
		return nil, &Error{Message: err.Error()}
	}

	target := path
	if !cl.outsideAPI {
		target = apiPrefix + "/" + strings.TrimLeft(path, "/")
	}
	target = whatwgTarget(target)
	if q := encodeQuery(cl.query); q != "" {
		if strings.Contains(target, "?") {
			target += "&" + q
		} else {
			target += "?" + q
		}
	}

	if cl.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, cl.timeout)
		defer cancel()
	}

	resp, err := c.send(ctx, method, target, cl)
	if err != nil {
		return nil, c.transportError(ctx, err, cl)
	}
	valid := cl.validateStatus
	if valid == nil {
		valid = func(status int) bool { return status >= 200 && status < 300 }
	}
	if !valid(resp.Status) {
		return resp, &Error{
			Message:  "Request failed with status code " + strconv.Itoa(resp.Status),
			Code:     statusCode(resp.Status),
			Response: resp,
		}
	}
	return resp, nil
}

func statusCode(status int) string {
	switch status / 100 {
	case 4:
		return "ERR_BAD_REQUEST"
	case 5:
		return "ERR_BAD_RESPONSE"
	}
	return ""
}

var errHandlerDied = errors.New("socket hang up")

// transportError turns a call without an answer into the error axios gave.
func (c *Client) transportError(ctx context.Context, err error, cl *call) *Error {
	switch {
	case errors.Is(err, errHandlerDied):
		return &Error{Message: "socket hang up", Code: "ECONNRESET"}
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return &Error{Message: fmt.Sprintf("timeout of %dms exceeded", cl.timeout.Milliseconds()), Code: "ECONNABORTED"}
	case ctx.Err() != nil:
		return &Error{Message: "canceled", Code: "ERR_CANCELED"}
	}
	return &Error{Message: err.Error()}
}

// send runs the request and follows redirects that stay on the API server.
func (c *Client) send(ctx context.Context, method, target string, cl *call) (*Response, error) {
	body, hasBody := cl.body, cl.hasBody
	header := cl.header
	for hop := 0; ; hop++ {
		resp, err := c.roundTrip(ctx, method, target, header, body, hasBody)
		if err != nil {
			return nil, err
		}
		location := resp.Header.Get("Location")
		if cl.noRedirects || resp.Status < 300 || resp.Status >= 400 || !strings.HasPrefix(location, "/") || strings.HasPrefix(location, "//") {
			return resp, nil
		}
		if hop == maxRedirects {
			return nil, errors.New("Maximum number of redirects exceeded")
		}
		// A POST becomes a GET after 301 and 302, and everything but GET and
		// HEAD does after 303; 307 and 308 repeat the request.
		if ((resp.Status == 301 || resp.Status == 302) && method == http.MethodPost) ||
			(resp.Status == 303 && method != http.MethodGet && method != http.MethodHead) {
			method, body, hasBody = http.MethodGet, nil, false
			header = header.Clone()
			header.Del("Content-Type")
		}
		target = location
	}
}

// roundTrip runs the handler once. The handler runs on its own goroutine,
// so that a handler that does not return in time cannot hold the caller.
func (c *Client) roundTrip(ctx context.Context, method, target string, header http.Header, body []byte, hasBody bool) (*Response, error) {
	u, err := url.ParseRequestURI(target)
	if err != nil {
		// The listener answers a request line it cannot parse like this.
		return &Response{
			Status: http.StatusBadRequest,
			Header: http.Header{"Content-Type": {"text/plain; charset=utf-8"}, "Connection": {"close"}},
			Body:   []byte("400 Bad Request"),
		}, nil
	}
	req := (&http.Request{
		Method:     method,
		URL:        u,
		Proto:      "HTTP/1.1",
		ProtoMajor: 1,
		ProtoMinor: 1,
		Header:     header.Clone(),
		Body:       http.NoBody,
		Host:       c.host,
		RemoteAddr: c.remoteAddr,
		RequestURI: target,
	}).WithContext(api.WithIdentity(ctx, c.identity))
	if hasBody {
		req.Body = io.NopCloser(bytes.NewReader(body))
		req.ContentLength = int64(len(body))
		req.Header.Set("Content-Length", strconv.Itoa(len(body)))
	}

	rec := newRecorder()
	done := make(chan error, 1)
	go func() {
		defer func() {
			if p := recover(); p != nil {
				if p != http.ErrAbortHandler {
					logging.Logf("goapi", "panic in the API handler for %s %s: %v\n%s", method, u.Path, p, debug.Stack())
				}
				done <- errHandlerDied
				return
			}
			done <- nil
		}()
		c.handler.ServeHTTP(rec, req)
	}()
	select {
	case err := <-done:
		if err != nil {
			return nil, err
		}
		return rec.response(), nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// recorder is the ResponseWriter a call hands to the handler.
type recorder struct {
	header   http.Header
	sent     http.Header
	status   int
	body     bytes.Buffer
	gotBytes bool
}

func newRecorder() *recorder {
	return &recorder{header: http.Header{}}
}

func (r *recorder) Header() http.Header { return r.header }

func (r *recorder) WriteHeader(status int) {
	if r.sent != nil {
		return
	}
	r.status = status
	r.sent = r.header.Clone()
}

func (r *recorder) Write(b []byte) (int, error) {
	r.WriteHeader(http.StatusOK)
	if !r.gotBytes && len(b) > 0 {
		r.gotBytes = true
		// The listener names the type of a body the handler did not name.
		if r.sent.Get("Content-Type") == "" && r.sent.Get("Content-Encoding") == "" {
			r.sent.Set("Content-Type", http.DetectContentType(b))
		}
	}
	return r.body.Write(b)
}

func (r *recorder) Flush() { r.WriteHeader(http.StatusOK) }

func (r *recorder) response() *Response {
	r.WriteHeader(http.StatusOK)
	return &Response{Status: r.status, Header: r.sent, Body: r.body.Bytes()}
}

var absoluteURL = regexp.MustCompile(`(?i)^https?://`)

// checkPath is the guard the Node client ran on every path before a
// request: it keeps a call on the API server and out of parent directories.
func checkPath(path string) error {
	if path == "" {
		return errors.New("API path is required")
	}
	if strings.ContainsAny(path, "\\\x00") {
		return errors.New("Invalid API path")
	}
	if absoluteURL.MatchString(path) || strings.HasPrefix(path, "//") {
		return errors.New("Absolute API URLs are not allowed")
	}
	pathname, query, _ := strings.Cut(path, "?")
	if !strings.HasPrefix(pathname, "/") {
		return errors.New("API path must be relative to the configured Go API base URL")
	}
	if strings.Contains(pathname, "..") {
		return errors.New("API path must not contain parent-directory segments")
	}
	for _, seg := range strings.Split(pathname, "/") {
		if seg == "." || seg == ".." {
			return errors.New("Invalid API path segment")
		}
	}
	if strings.ContainsAny(query, "\r\n\x00") {
		return errors.New("Invalid query string")
	}
	return nil
}

// whatwgTarget prepares a path and query the way Node's URL parser did
// before the request went out: tabs and line breaks are dropped, the
// fragment is cut off, and characters a URL cannot hold are escaped.
// Existing escapes stay as they are.
func whatwgTarget(s string) string {
	s = strings.TrimFunc(s, func(r rune) bool { return r <= 0x20 })
	s = strings.NewReplacer("\t", "", "\n", "", "\r", "").Replace(s)
	if i := strings.IndexByte(s, '#'); i >= 0 {
		s = s[:i]
	}
	pathname, query, hasQuery := strings.Cut(s, "?")
	out := escapeBytes(pathname, " \"<>^`{}")
	if hasQuery {
		out += "?" + escapeBytes(query, " \"<>'")
	}
	return out
}

func escapeBytes(s, set string) string {
	const upperhex = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c <= 0x1F || c > 0x7E || strings.IndexByte(set, c) >= 0 {
			b.WriteByte('%')
			b.WriteByte(upperhex[c>>4])
			b.WriteByte(upperhex[c&15])
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}
