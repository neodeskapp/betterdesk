package netmonitor

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/unitronix/betterdesk-server/console/ssrfguard"
)

// DefaultTimeoutMs is how long a check waits when its target names no
// timeout.
const DefaultTimeoutMs = 5000

// timerMax is the longest timer Node has, in milliseconds. A longer
// socket timeout is cut to it; a longer timer for a child process fires
// after one millisecond.
const timerMax = 1<<31 - 1

// invalidHost is the answer to a host that is no host name and no
// address.
const invalidHost = "Invalid host format"

// Result is the outcome of one check: {success, rtt_ms, error}, and for an
// HTTP check also status_code.
type Result struct {
	Success bool
	// RTT is the round trip time in milliseconds, nil when the check
	// failed.
	RTT *Number
	// HTTP is true for the result of an HTTP check, which has a status
	// code (nil when no answer came).
	HTTP       bool
	StatusCode *int
	// Error is nil for a check that got an answer.
	Error *string
}

func failed(message string) Result {
	return Result{Error: &message}
}

func failedHTTP(message string) Result {
	return Result{HTTP: true, Error: &message}
}

// InvalidHost is the result of a ping or TCP check for a host that is not
// text.
func InvalidHost() Result { return failed(invalidHost) }

// URLRequired is the result of an HTTP check for a URL that is not text.
func URLRequired() Result { return failedHTTP("URL is required") }

// Dialer opens connections. *net.Dialer is one.
type Dialer interface {
	DialContext(ctx context.Context, network, address string) (net.Conn, error)
}

// PingRunner runs the ping command and returns what it printed and its
// exit code. err is for a command that could not be started; a command
// that was stopped because ctx ended has a non-zero exit code.
type PingRunner func(ctx context.Context, name string, args ...string) (stdout []byte, exitCode int, err error)

// spawnError is a command that could not be started, with the message
// Node gave: "spawn ping ENOENT".
type spawnError struct {
	name string
	code string
	err  error
}

func (e *spawnError) Error() string { return "spawn " + e.name + " " + e.code }
func (e *spawnError) Unwrap() error { return e.err }

// runCommand is the PingRunner that starts a real process. When ctx ends
// it asks the process to stop.
func runCommand(ctx context.Context, name string, args ...string) ([]byte, int, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = 2 * time.Second
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Start(); err != nil {
		code := "ENOENT"
		if name, ok := errnoName(err); ok {
			code = name
		}
		return nil, -1, &spawnError{name: name, code: code, err: err}
	}
	err := cmd.Wait()
	if err == nil {
		return out.Bytes(), 0, nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() > 0 {
		return out.Bytes(), exit.ExitCode(), nil
	}
	return out.Bytes(), -1, nil
}

var (
	// validHost matches a domain name, four numbers with dots, or the
	// letters of an IPv6 address.
	validHost = regexp.MustCompile(`^(?:[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?\.)*[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?$|^(?:\d{1,3}\.){3}\d{1,3}$|^(?:[a-fA-F0-9:]+)$`)
	// shellCharacters must not be in a host that goes to the ping command.
	shellCharacters = regexp.MustCompile("[;&|`$(){}\\[\\]<>\\\\!#*?'\"~]")

	pingTimeUnix    = regexp.MustCompile(`(?i)time=(\d+\.?\d*)\s*ms`)
	pingTimeWindows = regexp.MustCompile(`(?i)time[=<](\d+)ms`)
)

// isValidHost reports whether host is a host name or an address and safe
// to hand to the ping command.
func isValidHost(host string) bool {
	if host == "" || utf16Length(host) > 253 {
		return false
	}
	if shellCharacters.MatchString(host) {
		return false
	}
	return validHost.MatchString(host) || ssrfguard.IsIP(host) != 0
}

// utf16Length is the length of s as JavaScript counts it.
func utf16Length(s string) int {
	n := 0
	for _, r := range s {
		n++
		if r > 0xFFFF {
			n++
		}
	}
	return n
}

// prober runs the checks. It holds what they reach the outside with.
type prober struct {
	guard     *ssrfguard.Guard
	dialer    Dialer
	transport http.RoundTripper
	ping      PingRunner
	windows   bool
	now       func() time.Time
}

func (p *prober) nowMs() int64 { return p.now().UnixMilli() }

// rangeError is the message of Node's ERR_OUT_OF_RANGE.
func rangeError(name, must string, received Number) string {
	return `The value of "` + name + `" is out of range. It must be ` + must + `. Received ` + received.received()
}

// timerDuration is what Node makes of a socket timeout: an error for a
// negative, infinite or NaN number, and the longest timer for anything
// above it.
func timerDuration(ms Number, name string) (time.Duration, string) {
	f := float64(ms)
	if math.IsNaN(f) || math.IsInf(f, 0) || f < 0 {
		return 0, rangeError(name, "a non-negative finite number", ms)
	}
	if f > timerMax {
		f = timerMax
	}
	return time.Duration(f * float64(time.Millisecond)), ""
}

// check asks the guard about the host of a ping or TCP check or a lookup.
// What follows goes to an address the guard checked, never to the name,
// which could answer with another address the second time.
func (p *prober) check(ctx context.Context, host string) (ssrfguard.Host, error) {
	if !isValidHost(host) {
		return ssrfguard.Host{}, errors.New(invalidHost)
	}
	return p.guard.CheckHost(ctx, host, tools)
}

// resolveHost is resolveHost(host): the first address of a host that
// passes the guard, of the ones the guard checked.
func (p *prober) resolveHost(ctx context.Context, host string) (string, error) {
	checked, err := p.check(ctx, host)
	if err != nil {
		return "", err
	}
	return checked.Addresses[0], nil
}

// pingHost is pingHost(host, timeoutMs): one echo request with the
// system's ping command, to the first address the guard checked.
func (p *prober) pingHost(ctx context.Context, host string, timeoutMs Number) Result {
	checked, err := p.check(ctx, host)
	if err != nil {
		return failed(err.Error())
	}
	address := checked.Addresses[0]
	var args []string
	if p.windows {
		args = []string{"-n", "1", "-w", timeoutMs.String(), address}
	} else {
		args = []string{"-c", "1", "-W", Number(math.Ceil(float64(timeoutMs) / 1000)).String(), address}
	}

	// The process may run two seconds longer than the ping waits
	limit := timeoutMs + 2000
	if !limit.isInteger() || limit < 0 {
		return failed(rangeError("timeout", "an unsigned integer", limit))
	}
	wait := time.Duration(float64(limit) * float64(time.Millisecond))
	if limit > timerMax {
		wait = time.Millisecond
	}
	start := p.nowMs()
	var (
		stdout []byte
		code   int
	)
	if limit == 0 {
		// Node sets no timer for a limit of zero
		stdout, code, err = p.ping(ctx, "ping", args...)
	} else {
		runCtx, cancel := context.WithTimeout(ctx, wait)
		stdout, code, err = p.ping(runCtx, "ping", args...)
		cancel()
	}
	if err != nil {
		return failed(err.Error())
	}
	elapsed := p.nowMs() - start
	if code != 0 {
		return failed("Host unreachable")
	}
	rtt := Number(elapsed)
	if p.windows {
		if m := pingTimeWindows.FindSubmatch(stdout); m != nil {
			rtt = ParseInt(string(m[1]))
		}
	} else if m := pingTimeUnix.FindSubmatch(stdout); m != nil {
		if f, err := strconv.ParseFloat(string(m[1]), 64); err == nil {
			rtt = Number(f)
		}
	}
	return Result{Success: true, RTT: &rtt}
}

// checkTCPPort is checkTcpPort(host, port, timeoutMs): it opens a
// connection to the port of the first address the guard checked and
// closes it.
func (p *prober) checkTCPPort(ctx context.Context, host string, port, timeoutMs Number) Result {
	checked, err := p.check(ctx, host)
	if err != nil {
		return failed(err.Error())
	}
	address := checked.Addresses[0]
	timeout, problem := timerDuration(timeoutMs, "msecs")
	if problem != "" {
		return failed(problem)
	}
	if !port.isInteger() || port < 0 || port > 0xFFFF {
		return failed("Port should be >= 0 and < 65536. Received type number (" + port.inspect() + ").")
	}

	dialCtx := ctx
	if timeout > 0 {
		var cancel context.CancelFunc
		dialCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	start := p.nowMs()
	conn, err := p.dialer.DialContext(dialCtx, "tcp", net.JoinHostPort(address, strconv.Itoa(int(port))))
	if err != nil {
		if dialCtx.Err() != nil || isTimeout(err) {
			return failed("Connection timeout")
		}
		return failed(connectMessage(err, address, int(port)))
	}
	rtt := Number(p.nowMs() - start)
	_ = conn.Close()
	return Result{Success: true, RTT: &rtt}
}

func isTimeout(err error) bool {
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout() && !errors.Is(err, syscall.ETIMEDOUT)
}

// checkHTTP is checkHttp(url, timeoutMs): one GET request. Any status
// from 200 to 399 counts as success; redirects are not followed.
func (p *prober) checkHTTP(ctx context.Context, rawURL string, timeoutMs Number) Result {
	parsed, checked, err := p.guard.CheckHTTPURL(ctx, rawURL, tools)
	if err != nil {
		return failedHTTP(err.Error())
	}
	timeout, problem := timerDuration(timeoutMs, "timeout")
	if problem != "" {
		return failedHTTP(problem)
	}

	scheme := "http"
	if parsed.Protocol == "https:" {
		scheme = "https"
	}
	host := parsed.Hostname
	if parsed.Port != "" {
		host += ":" + parsed.Port
	}
	// The connection goes to an address the guard checked (connector.dial)
	reqCtx, cancel := context.WithCancel(context.WithValue(ctx, checkedHost{}, checked))
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, scheme+"://"+host+"/", nil)
	if err != nil {
		return failedHTTP(err.Error())
	}
	// The path and query go out as the URL parser wrote them. A path that
	// starts with two slashes cannot go that way, since the client would
	// read a host out of it; there the client writes the path itself and
	// encodes "|", "^" and a "%" that starts no escape.
	path := parsed.Pathname
	if path == "" {
		path = "/"
	}
	req.URL = &url.URL{Scheme: scheme, Host: host, Opaque: path}
	if strings.HasPrefix(path, "//") {
		req.URL.Opaque = ""
		req.URL.Path, req.URL.RawPath = percentDecode(path), path
	}
	if len(parsed.Search) > 1 {
		req.URL.RawQuery = parsed.Search[1:]
	}
	req.Header["User-Agent"] = nil
	req.Header.Set("Connection", "keep-alive")

	// The request fails when nothing arrives for the length of the
	// timeout: until the answer starts, then between the parts of its body
	idle := newIdleTimer(timeout, cancel)
	defer idle.stop()

	start := p.nowMs()
	res, err := p.transport.RoundTrip(req)
	if err != nil {
		if idle.fired() {
			return failedHTTP("Timeout")
		}
		return failedHTTP(requestMessage(err))
	}
	rtt := Number(p.nowMs() - start)
	idle.reset()
	_, err = io.Copy(io.Discard, &touchReader{r: res.Body, touch: idle.reset})
	_ = res.Body.Close()
	if err != nil {
		if idle.fired() {
			return failedHTTP("Timeout")
		}
		// The connection broke inside the body. The old check never
		// ended there (BUGS.md NM4).
		return failedHTTP("aborted")
	}
	status := res.StatusCode
	return Result{
		Success:    status >= 200 && status < 400,
		RTT:        &rtt,
		HTTP:       true,
		StatusCode: &status,
	}
}

// percentDecode turns every %XX into its byte and leaves a "%" that starts
// no escape as it is.
func percentDecode(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '%' && i+2 < len(s) {
			if n, err := strconv.ParseUint(s[i+1:i+3], 16, 8); err == nil {
				b.WriteByte(byte(n))
				i += 2
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// idleTimer calls cancel when reset has not been called for the length of
// timeout. A timeout of zero never fires.
type idleTimer struct {
	mu      sync.Mutex
	timeout time.Duration
	timer   *time.Timer
	done    bool
}

func newIdleTimer(timeout time.Duration, cancel func()) *idleTimer {
	t := &idleTimer{timeout: timeout}
	if timeout > 0 {
		t.timer = time.AfterFunc(timeout, func() {
			t.mu.Lock()
			t.done = true
			t.mu.Unlock()
			cancel()
		})
	}
	return t
}

func (t *idleTimer) reset() {
	if t.timer != nil {
		t.timer.Reset(t.timeout)
	}
}

func (t *idleTimer) stop() {
	if t.timer != nil {
		t.timer.Stop()
	}
}

func (t *idleTimer) fired() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.done
}

// touchReader calls touch after every read that brought something.
type touchReader struct {
	r     io.Reader
	touch func()
}

func (t *touchReader) Read(p []byte) (int, error) {
	n, err := t.r.Read(p)
	if n > 0 {
		t.touch()
	}
	return n, err
}

// attemptTimeout is how long one address of a name gets before the next
// one is tried.
const attemptTimeout = 250 * time.Millisecond

// connectError is a connection to an address that failed, with what the
// message needs.
type connectError struct {
	err  error
	ip   string
	port int
}

func (e *connectError) Error() string { return connectMessage(e.err, e.ip, e.port) }
func (e *connectError) Unwrap() error { return e.err }

// certError is a server certificate that was not accepted.
type certError struct {
	message string
}

func (e *certError) Error() string { return e.message }

// checkedHost is the key of the host the guard checked for an HTTP check,
// in the context of its request.
type checkedHost struct{}

// errNotChecked is a connection to a host the guard was not asked about
// for the request. The HTTP checks never ask for one.
var errNotChecked = errors.New("connection to a host the guard has not checked")

// connector opens the connections of the HTTP checks.
type connector struct {
	dialer Dialer
	// roots are the trusted authorities, nil for the system's.
	roots           *x509.CertPool
	allowSelfSigned bool
	now             func() time.Time
}

// dial connects to host:port at the addresses the guard checked for the
// request (in ctx), and nowhere else; for an address that is the address
// itself. With several addresses each gets a quarter of a second before
// the next is tried, the families taking turns.
func (c *connector) dial(ctx context.Context, network, address string) (net.Conn, error) {
	host, portText, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	port, _ := strconv.Atoi(portText)
	checked, ok := ctx.Value(checkedHost{}).(ssrfguard.Host)
	if !ok || checked.Name != host {
		return nil, errNotChecked
	}
	addresses := checked.Addresses
	switch len(addresses) {
	case 0:
		return nil, &ssrfguard.LookupError{Code: "ENOTFOUND", Host: host}
	case 1:
		conn, err := c.dialer.DialContext(ctx, network, net.JoinHostPort(addresses[0], portText))
		if err != nil {
			return nil, &connectError{err: err, ip: addresses[0], port: port}
		}
		return conn, nil
	}
	ordered := takeTurns(addresses)
	var first error
	for i, ip := range ordered {
		attemptCtx, cancel := ctx, context.CancelFunc(func() {})
		if i < len(ordered)-1 {
			attemptCtx, cancel = context.WithTimeout(ctx, attemptTimeout)
		}
		conn, err := c.dialer.DialContext(attemptCtx, network, net.JoinHostPort(ip, portText))
		timedOut := attemptCtx.Err() != nil
		cancel()
		if err == nil {
			return conn, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if first == nil {
			if timedOut {
				err = syscall.ETIMEDOUT
			}
			first = &connectError{err: err, ip: ip, port: port}
		}
	}
	// Every address failed: the message is the first one's
	return nil, first
}

// takeTurns orders addresses so that IPv4 and IPv6 alternate, starting
// with the family of the first.
func takeTurns(addresses []string) []string {
	var first, second []string
	firstIsV6 := strings.Contains(addresses[0], ":")
	for _, a := range addresses {
		if strings.Contains(a, ":") == firstIsV6 {
			first = append(first, a)
		} else {
			second = append(second, a)
		}
	}
	ordered := make([]string, 0, len(addresses))
	for i := 0; i < len(first) || i < len(second); i++ {
		if i < len(first) {
			ordered = append(ordered, first[i])
		}
		if i < len(second) {
			ordered = append(ordered, second[i])
		}
	}
	return ordered
}

// dialTLS connects and does the TLS handshake. The certificate is checked
// in Node's order, so the message of a refused one is Node's: first the
// chain, then the dates, then the name.
func (c *connector) dialTLS(ctx context.Context, network, address string) (net.Conn, error) {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	raw, err := c.dial(ctx, network, address)
	if err != nil {
		return nil, err
	}
	cfg := &tls.Config{
		ServerName:         host,
		InsecureSkipVerify: true, //nolint:gosec // verified in VerifyConnection, unless ALLOW_SELF_SIGNED_CERTS is on
		NextProtos:         []string{"http/1.1"},
	}
	if !c.allowSelfSigned {
		cfg.VerifyConnection = func(state tls.ConnectionState) error {
			return c.verify(host, state.PeerCertificates)
		}
	}
	conn := tls.Client(raw, cfg)
	if err := conn.HandshakeContext(ctx); err != nil {
		_ = raw.Close()
		return nil, err
	}
	return conn, nil
}

// verify checks the certificates a server sent for host.
func (c *connector) verify(host string, certs []*x509.Certificate) error {
	if len(certs) == 0 {
		return &certError{message: "unable to verify the first certificate"}
	}
	leaf := certs[0]
	opts := x509.VerifyOptions{Roots: c.roots, Intermediates: x509.NewCertPool(), CurrentTime: c.now()}
	for _, cert := range certs[1:] {
		opts.Intermediates.AddCert(cert)
	}
	_, err := leaf.Verify(opts)
	var invalid x509.CertificateInvalidError
	if errors.As(err, &invalid) && invalid.Reason == x509.Expired {
		// A chain nobody vouches for is named before a date
		inTime := opts
		inTime.CurrentTime = leaf.NotAfter.Add(-time.Second)
		var unknown x509.UnknownAuthorityError
		if _, trustErr := leaf.Verify(inTime); errors.As(trustErr, &unknown) {
			err = trustErr
		}
	}
	if err != nil {
		return &certError{message: chainMessage(err, certs, opts.CurrentTime)}
	}
	if err := leaf.VerifyHostname(host); err != nil {
		return &certError{message: altNamesMessage(leaf, host)}
	}
	return nil
}

// newTransport is the transport of the HTTP checks: HTTP/1.1, no proxy,
// no compression, connections kept for the next check, and certificates
// checked unless the console allows self-signed ones.
func newTransport(c *connector) *http.Transport {
	return &http.Transport{
		Proxy:              nil,
		DialContext:        c.dial,
		DialTLSContext:     c.dialTLS,
		TLSNextProto:       map[string]func(string, *tls.Conn) http.RoundTripper{},
		DisableCompression: true,
		IdleConnTimeout:    5 * time.Second,
	}
}
