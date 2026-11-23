package contract

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// userAgent is what every request of the suite names itself.
const userAgent = "bd-contract/1"

// Headers a request has unless the case sets them: what the web app sends
// for API calls, and what a browser sends when it opens a page.
const (
	acceptAPI  = "application/json"
	acceptPage = "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8"
)

// Client sends requests to one console and turns the answers into Answers.
type Client struct {
	base     *url.URL
	http     *http.Client
	timeout  time.Duration
	password string
	// secrets are the secrets of the run, masked in every answer.
	secrets *secretSet

	mu         sync.Mutex
	normalizer *Normalizer
	routing    map[string]string
	hasRouting *bool
}

// NewClient returns a client for the console in the settings. The password
// of the test accounts is read from the settings' file, the secrets from
// the secrets folder.
func NewClient(s *Settings) (*Client, error) {
	password, err := os.ReadFile(s.PasswordFile)
	if err != nil {
		return nil, fmt.Errorf("read the test accounts' password: %w", err)
	}
	secrets, err := loadSecrets(s.SecretsDir)
	if err != nil {
		return nil, err
	}
	return &Client{
		secrets: secrets,
		base:    s.BaseURL,
		http: &http.Client{
			Transport: &http.Transport{
				// The suite asks for gzip itself, to see Content-Encoding.
				DisableCompression:  true,
				MaxIdleConnsPerHost: 4,
				IdleConnTimeout:     30 * time.Second,
			},
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		timeout:  s.Timeout,
		password: strings.TrimSpace(string(password)),
		routing:  map[string]string{},
	}, nil
}

// Request is one request of the suite.
type Request struct {
	Method string
	// Path is the path and query: "/api/devices?limit=10".
	Path string
	// Header is added to the default headers and replaces them by name.
	Header http.Header
	// Body is sent as it is, with ContentType.
	Body        []byte
	ContentType string
	// NoCSRF leaves out the X-CSRF-Token header of a write.
	NoCSRF bool
	// NoSession leaves out the session cookie. NoCookies leaves out every
	// cookie. The cookies of the answer are then not kept.
	NoSession bool
	NoCookies bool
	// FollowRedirects follows up to ten redirects on this console.
	FollowRedirects bool
}

// Session is one browser: a cookie jar, a CSRF token and, after SignIn, a
// signed-in account.
type Session struct {
	Role   Role
	client *Client
	jar    []*http.Cookie
	csrf   string
	// csrfCookie is the CSRF cookie the token belongs to.
	csrfCookie string
	signedIn   bool
}

// The cookies the console uses: one name over HTTPS, one over HTTP.
var (
	sessionCookies = []string{"nd.sid", "neodesk.sid"}
	csrfCookies    = []string{"__csrf_h", "__csrf"}
)

// NewSession returns a browser with no cookies for a role. It is signed out
// until SignIn.
func (c *Client) NewSession(role Role) *Session {
	return &Session{Role: role, client: c}
}

// SignedIn reports whether the last SignIn got a session.
func (s *Session) SignedIn() bool { return s.signedIn }

// SignIn signs the session's account in the way the web app does: the
// sign-in page's data for the CSRF token, then the sign-in request. It
// returns the answer of the sign-in request. An account the console does
// not let in (role pro) is not an error: the session stays signed out and
// keeps its cookies. The signed-out role only fetches the token.
func (s *Session) SignIn() (*Answer, error) {
	s.jar, s.csrf, s.csrfCookie, s.signedIn = nil, "", "", false
	if err := s.fetchToken(); err != nil {
		return nil, err
	}
	if s.Role == SignedOut {
		return nil, nil
	}
	body, err := json.Marshal(map[string]string{"username": s.Role.Account(), "password": s.client.password})
	if err != nil {
		return nil, fmt.Errorf("encode sign-in: %w", err)
	}
	answer := s.Do(Request{Method: http.MethodPost, Path: "/api/auth/login", Body: body, ContentType: "application/json"})
	if answer.Failed != "" {
		return nil, fmt.Errorf("sign in as %s: no answer (%s)", s.Role, answer.Failed)
	}
	s.signedIn = answer.Status == http.StatusOK && s.cookie(sessionCookies) != ""
	if err := s.fetchToken(); err != nil {
		return nil, err
	}
	return &answer, nil
}

// Alive reports whether the console still accepts the session.
func (s *Session) Alive() bool {
	if !s.signedIn {
		return false
	}
	answer := s.Do(Request{Method: http.MethodGet, Path: "/api/auth/verify"})
	return answer.Status == http.StatusOK
}

// fetchToken gets the CSRF token for the session's cookies: from the
// console's bootstrap when signed in, from the sign-in page's data otherwise.
func (s *Session) fetchToken() error {
	paths := []string{"/api/auth/login-page"}
	if s.signedIn {
		paths = []string{"/api/console/bootstrap", "/api/auth/login-page"}
	}
	for _, p := range paths {
		raw, status, err := s.client.fetchJSON(s, p)
		if err != nil {
			return fmt.Errorf("get the CSRF token from %s: %w", p, err)
		}
		if status != http.StatusOK {
			continue
		}
		if token, _ := raw["csrfToken"].(string); token != "" {
			s.csrf = token
			s.csrfCookie = s.cookie(csrfCookies)
			return nil
		}
	}
	return fmt.Errorf("get the CSRF token: neither /api/console/bootstrap nor /api/auth/login-page gave one")
}

func (s *Session) cookie(names []string) string {
	for _, c := range s.jar {
		for _, name := range names {
			if c.Name == name {
				return c.Value
			}
		}
	}
	return ""
}

// keep stores the cookies of an answer the way a browser does.
func (s *Session) keep(cookies []*http.Cookie) {
	for _, c := range cookies {
		expired := c.MaxAge < 0 || c.Value == "" || (!c.Expires.IsZero() && c.Expires.Before(time.Now()))
		kept := s.jar[:0:0]
		for _, old := range s.jar {
			if old.Name != c.Name {
				kept = append(kept, old)
			}
		}
		if !expired {
			kept = append(kept, &http.Cookie{Name: c.Name, Value: c.Value})
		}
		s.jar = kept
	}
}

// Do sends a request with the session's cookies and CSRF token and returns
// the answer. It never fails: when no answer comes, the Answer says why
// (Failed).
func (s *Session) Do(r Request) Answer {
	c := s.client
	if !isSafeMethod(r.Method) && !r.NoCSRF && s.csrfCookie != s.cookie(csrfCookies) {
		// The console replaced the CSRF cookie; the token must match it.
		_ = s.fetchToken()
	}
	target := r.Path
	var redirects []string
	method, body := r.Method, r.Body
	for hop := 0; ; hop++ {
		ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
		req, err := http.NewRequestWithContext(ctx, method, c.base.String()+target, bytes.NewReader(body))
		if err != nil {
			cancel()
			return Answer{Failed: "connection", Body: Body{Kind: BodyEmpty}}
		}
		s.setHeaders(req, r, body)
		res, raw, undecoded, err := c.send(req)
		cancel()
		if err != nil {
			failed := "connection"
			var netErr net.Error
			if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout()) {
				failed = "timeout"
			}
			return Answer{Failed: failed, Redirects: redirects, Body: Body{Kind: BodyEmpty}}
		}
		if !r.NoSession && !r.NoCookies {
			s.keep(res.Cookies())
		}
		location := res.Header.Get("Location")
		if r.FollowRedirects && hop < 10 && res.StatusCode >= 300 && res.StatusCode < 400 && location != "" {
			next, err := c.base.Parse(target)
			if err == nil {
				next, err = next.Parse(location)
			}
			if err == nil && next.Host == c.base.Host {
				redirects = append(redirects, c.norm().Text(location))
				target = next.RequestURI()
				if res.StatusCode != http.StatusTemporaryRedirect && res.StatusCode != http.StatusPermanentRedirect {
					method, body = http.MethodGet, nil
				}
				continue
			}
		}
		answer := NewAnswer(res.StatusCode, res.Header, raw, undecoded, c.norm())
		answer.Redirects = redirects
		answer.raw = &rawAnswer{status: res.StatusCode, header: res.Header.Clone(), body: raw, undecoded: undecoded}
		return answer
	}
}

// remask normalizes an answer again from what the console sent, so the
// secrets that became known after it arrived are masked in it too.
func (c *Client) remask(a Answer) Answer {
	if a.raw == nil {
		return a
	}
	again := NewAnswer(a.raw.status, a.raw.header, a.raw.body, a.raw.undecoded, c.norm())
	for _, location := range a.Redirects {
		again.Redirects = append(again.Redirects, c.secrets.mask(location))
	}
	again.raw = a.raw
	return again
}

func isSafeMethod(method string) bool {
	return method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions
}

func (s *Session) setHeaders(req *http.Request, r Request, body []byte) {
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Accept-Encoding", "gzip")
	if strings.HasPrefix(strings.ToLower(req.URL.Path), "/api/") {
		req.Header.Set("Accept", acceptAPI)
	} else {
		req.Header.Set("Accept", acceptPage)
	}
	if len(body) > 0 && r.ContentType != "" {
		req.Header.Set("Content-Type", r.ContentType)
	}
	if !isSafeMethod(req.Method) && !r.NoCSRF && s.csrf != "" {
		req.Header.Set("X-CSRF-Token", s.csrf)
	}
	if !r.NoCookies {
		var pairs []string
		for _, c := range s.jar {
			if r.NoSession && contains(sessionCookies, c.Name) {
				continue
			}
			pairs = append(pairs, c.Name+"="+c.Value)
		}
		if len(pairs) > 0 {
			req.Header.Set("Cookie", strings.Join(pairs, "; "))
		}
	}
	for name, values := range r.Header {
		req.Header.Del(name)
		for _, value := range values {
			req.Header.Add(name, value)
		}
	}
}

func contains(list []string, s string) bool {
	for _, item := range list {
		if item == s {
			return true
		}
	}
	return false
}

// send does one round trip and reads the body. undecoded says the body is
// in a content encoding other than gzip and was left as it is.
func (c *Client) send(req *http.Request) (res *http.Response, body []byte, undecoded bool, err error) {
	trace := &httptrace.ClientTrace{GotConn: func(info httptrace.GotConnInfo) {
		c.noteAddrs(info.Conn.LocalAddr(), info.Conn.RemoteAddr())
	}}
	req = req.WithContext(httptrace.WithClientTrace(req.Context(), trace))
	res, err = c.http.Do(req)
	if err != nil {
		return nil, nil, false, err
	}
	defer res.Body.Close()
	if mediaTypeOf(res.Header.Get("Content-Type")) == "text/event-stream" {
		return res, nil, false, nil
	}
	var reader io.Reader = res.Body
	switch encoding := strings.ToLower(strings.TrimSpace(res.Header.Get("Content-Encoding"))); encoding {
	case "", "identity":
	case "gzip":
		if req.Method == http.MethodHead {
			break
		}
		zr, zerr := gzip.NewReader(res.Body)
		if zerr != nil {
			if errors.Is(zerr, io.EOF) {
				return res, nil, false, nil
			}
			return nil, nil, false, zerr
		}
		defer zr.Close()
		reader = zr
	default:
		_, err = io.Copy(io.Discard, res.Body)
		return res, nil, true, err
	}
	body, err = io.ReadAll(reader)
	if err != nil {
		return nil, nil, false, err
	}
	return res, body, false, nil
}

// noteAddrs learns from the first connection the address the console sees
// the suite at (local) and the console's own address (remote).
func (c *Client) noteAddrs(local, remote net.Addr) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.normalizer != nil {
		return
	}
	ip := ""
	if tcp, ok := local.(*net.TCPAddr); ok && !tcp.IP.IsLoopback() {
		ip = tcp.IP.String()
	}
	c.normalizer = NewNormalizer(c.base.Scheme+"://"+c.base.Host, ip)
	c.normalizer.secrets = c.secrets
	if tcp, ok := remote.(*net.TCPAddr); ok {
		c.normalizer.withConsoleIP(tcp.IP.String())
	}
}

// ConsoleHost is the variable {console_host} of every case: the host
// name of the console under test (bdgo-c1), for a case that points the
// console's network tools at the console itself. Answers write the
// address it stands for as <console-ip>.
const ConsoleHost = "console_host"

func (c *Client) norm() *Normalizer {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.normalizer == nil {
		n := NewNormalizer(c.base.Scheme+"://"+c.base.Host, "")
		n.secrets = c.secrets
		return n
	}
	return c.normalizer
}

// fetchJSON gets a path with the session's cookies and returns the decoded
// JSON object and the status. An answer that is not a JSON object gives an
// empty map.
func (c *Client) fetchJSON(s *Session, target string) (map[string]any, int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base.String()+target, nil)
	if err != nil {
		return nil, 0, err
	}
	if s != nil {
		s.setHeaders(req, Request{}, nil)
	} else {
		req.Header.Set("User-Agent", userAgent)
		req.Header.Set("Accept", acceptAPI)
	}
	res, body, _, err := c.send(req)
	if err != nil {
		return nil, 0, err
	}
	if s != nil {
		s.keep(res.Cookies())
	}
	object := map[string]any{}
	_ = json.Unmarshal(body, &object)
	return object, res.StatusCode, nil
}

// Routing answers of the console's routing report (GET /__bd/routing,
// console/routing.go).
const (
	HandlerGo      = "go"
	HandlerNode    = "node"
	HandlerNone    = "none"
	HandlerUnknown = ""
)

// HasRouting reports whether the console has the routing report: a Go
// console started with CONSOLE_DEBUG_ROUTING=1.
func (c *Client) HasRouting() bool {
	c.mu.Lock()
	known := c.hasRouting
	c.mu.Unlock()
	if known != nil {
		return *known
	}
	has := c.askRouting(http.MethodGet, "/health") != HandlerUnknown
	c.mu.Lock()
	c.hasRouting = &has
	c.mu.Unlock()
	return has
}

// Handler says who answers a method and path on the console: HandlerGo,
// HandlerNode, HandlerNone, or HandlerUnknown when the console has no
// routing report.
func (c *Client) Handler(method, target string) string {
	return c.handlerOf(method, target, false)
}

// handlerOf is Handler, for a websocket upgrade to target when upgrade
// is set.
func (c *Client) handlerOf(method, target string, upgrade bool) string {
	if !c.HasRouting() {
		return HandlerUnknown
	}
	key := method + " " + target
	if upgrade {
		key = "UPGRADE " + key
	}
	c.mu.Lock()
	handler, ok := c.routing[key]
	c.mu.Unlock()
	if ok {
		return handler
	}
	handler = c.askRouting(method, target, upgrade)
	c.mu.Lock()
	c.routing[key] = handler
	c.mu.Unlock()
	return handler
}

func (c *Client) askRouting(method, target string, upgrade ...bool) string {
	q := url.Values{"method": {method}, "path": {target}}
	if len(upgrade) > 0 && upgrade[0] {
		q.Set("upgrade", "websocket")
	}
	object, status, err := c.fetchJSON(nil, "/__bd/routing?"+q.Encode())
	if err != nil || status != http.StatusOK {
		return HandlerUnknown
	}
	switch handler, _ := object["handler"].(string); handler {
	case HandlerGo, HandlerNode, HandlerNone:
		return handler
	}
	return HandlerUnknown
}
