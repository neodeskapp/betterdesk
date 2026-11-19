package cli

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/unitronix/betterdesk-server/console/jsbind"
)

const checkUsage = "Usage: betterdesk-server check [--api URL] [--panel URL] [--proxy URL] [--port HOST:PORT] [--timeout-ms MS] [--insecure]"

// checkOptions are what the protocol check reads besides Env. Tests
// replace them.
type checkOptions struct {
	// roots are the certificate authorities HTTPS is checked against. Nil
	// means the system's.
	roots *x509.CertPool
	// resolve looks a host name up. Nil means the system's resolver.
	resolve func(ctx context.Context, host string) ([]string, error)
	// now is the clock certificates are checked at.
	now func() time.Time
}

// Check is "check", what scripts/installer-protocol-check.js did after an
// install: GET the API's and the console's health addresses (and a proxy
// address when given), each with a 2xx answer, or a 3xx with a Location,
// and over HTTPS a certificate that is valid for the host, and connect to
// each --port HOST:PORT (a port alone means 127.0.0.1). It prints one
// line per check, "PASS <what> (<status>)" or "FAIL <what> (<status>):
// <error>", and exits with 0 when every check passed and with 1
// otherwise. --api, --panel and --proxy are also taken as --api-url,
// --panel-url and --proxy-url, the script's names.
//
// HTTPS is checked against the system's certificate authorities; the
// script used Node's own list and NODE_EXTRA_CA_CERTS. --insecure checks
// no certificate.
func Check(args []string, env Env) int {
	return check(args, env, checkOptions{now: time.Now})
}

type checkConfig struct {
	urls      []string
	ports     []portArg
	timeout   time.Duration
	insecure  bool
	help      bool
	hasAPI    bool
	hasPanel  bool
	hasProxy  bool
	api       string
	panel     string
	proxy     string
	timeoutMs float64
}

// portArg is a --port value; given is false when the option was the last
// argument (the script then checked "undefined").
type portArg struct {
	text  string
	given bool
}

// parseCheckArgs is the script's parseArgs. An option that needs a value
// takes the next argument whatever it is; at the end there is none, and
// the URL check is then left out.
func parseCheckArgs(args []string) (checkConfig, error) {
	c := checkConfig{
		api: "http://127.0.0.1:21121/api/health", hasAPI: true,
		panel: "http://127.0.0.1:5000/health", hasPanel: true,
		timeoutMs: 5000,
	}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		next, hasNext := "", i+1 < len(args)
		if hasNext {
			next = args[i+1]
		}
		switch arg {
		case "--api-url", "--api":
			c.api, c.hasAPI = next, hasNext
		case "--panel-url", "--panel":
			c.panel, c.hasPanel = next, hasNext
		case "--proxy-url", "--proxy":
			c.proxy, c.hasProxy = next, hasNext
		case "--port":
			c.ports = append(c.ports, portArg{next, hasNext})
		case "--timeout-ms":
			c.timeoutMs = math.NaN()
			if hasNext {
				c.timeoutMs = jsbind.ParseNumber(next)
			}
		case "--insecure":
			c.insecure = true
		case "--help", "-h":
			c.help = true
		default:
			return c, fmt.Errorf("Unknown option: %s", arg)
		}
		if arg != "--insecure" && arg != "--help" && arg != "-h" {
			i++
		}
	}
	if math.IsNaN(c.timeoutMs) || math.IsInf(c.timeoutMs, 0) || c.timeoutMs < 250 {
		return c, errors.New("--timeout-ms must be at least 250")
	}
	c.timeout = time.Duration(c.timeoutMs * float64(time.Millisecond))
	// [apiUrl, panelUrl, proxyUrl].filter(Boolean)
	for _, u := range []struct {
		set  bool
		text string
	}{{c.hasAPI, c.api}, {c.hasPanel, c.panel}, {c.hasProxy, c.proxy}} {
		if u.set && u.text != "" {
			c.urls = append(c.urls, u.text)
		}
	}
	return c, nil
}

type checkResult struct {
	label  string
	ok     bool
	status int
	err    string
}

func check(args []string, env Env, opts checkOptions) int {
	cfg, err := parseCheckArgs(args)
	if err != nil {
		fmt.Fprintf(env.Stderr, "FAIL %s\n", err)
		return 1
	}
	if cfg.help {
		fmt.Fprintln(env.Stdout, checkUsage)
		return 0
	}
	if opts.now == nil {
		opts.now = time.Now
	}
	var results []checkResult
	for _, raw := range cfg.urls {
		r, err := checkEndpoint(raw, cfg, opts)
		if err != nil {
			// The script stopped at a check it could not start and
			// printed nothing else
			fmt.Fprintf(env.Stderr, "FAIL %s\n", err)
			return 1
		}
		results = append(results, r)
	}
	for _, port := range cfg.ports {
		r, err := checkPort(port, cfg.timeout, opts)
		if err != nil {
			fmt.Fprintf(env.Stderr, "FAIL %s\n", err)
			return 1
		}
		results = append(results, r)
	}
	all := true
	for _, r := range results {
		word := "PASS"
		if !r.ok {
			word, all = "FAIL", false
		}
		line := word + " " + r.label
		if r.status != 0 {
			line += " (" + strconv.Itoa(r.status) + ")"
		}
		if r.err != "" {
			line += ": " + r.err
		}
		fmt.Fprintln(env.Stdout, line)
	}
	if !all {
		return 1
	}
	return 0
}

// errInvalidURL is the TypeError of new URL().
var errInvalidURL = errors.New("Invalid URL")

// parseTarget reads a URL as new URL() would for these checks: a scheme
// is needed, and http and https need a host.
func parseTarget(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimFunc(raw, func(r rune) bool { return r <= ' ' }))
	if err != nil || u.Scheme == "" {
		return nil, errInvalidURL
	}
	u.Scheme = strings.ToLower(u.Scheme)
	if (u.Scheme == "http" || u.Scheme == "https") && u.Host == "" {
		return nil, errInvalidURL
	}
	return u, nil
}

// checkEndpoint is the script's checkEndpoint. err is for a URL that is
// no URL, which ended the script.
func checkEndpoint(raw string, cfg checkConfig, opts checkOptions) (checkResult, error) {
	result := checkResult{label: raw}
	target, err := parseTarget(raw)
	if err != nil {
		return result, err
	}
	if target.Scheme != "http" && target.Scheme != "https" {
		result.err = `Protocol "` + target.Scheme + `:" not supported. Expected "http:"`
		return result, nil
	}
	hostname := strings.ToLower(target.Hostname())
	if strings.Contains(hostname, ":") {
		hostname = "[" + hostname + "]"
	}

	ctx, cancel := context.WithTimeout(context.Background(), cfg.timeout)
	defer cancel()
	var peer []*x509.Certificate
	transport := &http.Transport{
		Proxy:              nil,
		DisableKeepAlives:  true,
		DisableCompression: true,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			return dial(ctx, address, opts)
		},
		TLSClientConfig: &tls.Config{
			ServerName:         strings.Trim(hostname, "[]"),
			InsecureSkipVerify: true,
			VerifyConnection: func(state tls.ConnectionState) error {
				peer = state.PeerCertificates
				if cfg.insecure {
					return nil
				}
				return verifyLikeNode(state.PeerCertificates, strings.ToLower(target.Hostname()), opts)
			},
		},
	}
	client := &http.Client{
		Transport:     transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		result.err = err.Error()
		return result, nil
	}
	req.Header.Set("User-Agent", "BetterDesk-Installer-Protocol-Check/1")
	res, err := client.Do(req)
	if err == nil {
		_, err = io.Copy(io.Discard, res.Body)
		_ = res.Body.Close()
	}
	if err != nil {
		result.err = nodeMessage(err, ctx, "request timed out")
		return result, nil
	}
	result.status = res.StatusCode
	switch {
	case res.StatusCode >= 200 && res.StatusCode < 300:
		result.ok = true
	case res.StatusCode >= 300 && res.StatusCode < 400:
		result.ok = res.Header.Get("Location") != ""
	}
	if target.Scheme == "https" && !cfg.insecure {
		result.ok = result.ok && len(peer) > 0 && certificateHostname(subjectAltName(peer[0]), hostname)
	}
	return result, nil
}

var sanPrefix = regexp.MustCompile(`(?i)^(?:DNS|IP Address|IP):`)

// certificateHostname is the script's own look at the certificate after
// the answer (checkCertificateHostname), on its subjectaltname: the host
// is one of the names, or matches a "*.name" by its end.
func certificateHostname(subjectAltName, hostname string) bool {
	if hostname == "" {
		return false
	}
	for _, name := range strings.Split(subjectAltName, ",") {
		name = sanPrefix.ReplaceAllString(jsbind.Trim(name), "")
		if name == "" {
			continue
		}
		if name == hostname || (strings.HasPrefix(name, "*.") && strings.HasSuffix(hostname, name[1:])) {
			return true
		}
	}
	return false
}

// checkPort is the script's checkPort: connect, and close at once. err is
// for a port Node refused to connect to at all, which ended the script.
func checkPort(port portArg, timeout time.Duration, opts checkOptions) (checkResult, error) {
	text := port.text
	if !port.given {
		text = "undefined"
	}
	result := checkResult{label: text}
	// Without a colon the port alone on 127.0.0.1; with one, split at the
	// last colon when something follows it (split(/:(?=[^:]+$)/)). Text
	// with a colon left whole is no number either
	host, portText := "127.0.0.1", text
	if strings.Contains(text, ":") {
		host = text
		if i := strings.LastIndexByte(text, ':'); i < len(text)-1 {
			host, portText = text[:i], text[i+1:]
		}
	}
	number := math.NaN()
	if port.given {
		number = jsbind.ParseNumber(portText)
	}
	if math.IsNaN(number) || number < 0 || number > 65535 || number != math.Trunc(number) {
		return result, fmt.Errorf("Port should be >= 0 and < 65536. Received type number (%s).", jsInspectNumber(number))
	}
	if host == "" {
		host = "localhost"
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	conn, err := dial(ctx, net.JoinHostPort(host, strconv.Itoa(int(number))), opts)
	if err != nil {
		result.err = nodeMessage(err, ctx, "connection timed out")
		return result, nil
	}
	_ = conn.Close()
	result.ok = true
	return result, nil
}

// jsInspectNumber is util.inspect of a number.
func jsInspectNumber(f float64) string {
	if f == 0 && math.Signbit(f) {
		return "-0"
	}
	return jsbind.NumberString(f)
}

// dialError is a failed connection with the text Node gave it.
type dialError struct{ message string }

func (e *dialError) Error() string { return e.message }

// dial connects as Node's net.connect did: the host is looked up (an IP
// literal is not), and each address is tried in turn. When every address
// fails, a single address gives its own error and several give an error
// without a message, as Node's AggregateError has none.
func dial(ctx context.Context, address string, opts checkOptions) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, &dialError{err.Error()}
	}
	addrs := []string{host}
	if net.ParseIP(host) == nil {
		resolve := opts.resolve
		if resolve == nil {
			resolve = net.DefaultResolver.LookupHost
		}
		found, err := resolve(ctx, host)
		if err != nil {
			code := "ENOTFOUND"
			var dnsErr *net.DNSError
			if errors.As(err, &dnsErr) && (dnsErr.IsTemporary || dnsErr.IsTimeout) {
				code = "EAI_AGAIN"
			}
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, &dialError{"getaddrinfo " + code + " " + host}
		}
		addrs = addrConfig(found)
	}
	var first error
	var d net.Dialer
	for _, ip := range addrs {
		conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(ip, port))
		if err == nil {
			return conn, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if first == nil {
			first = &dialError{connectMessage(err, ip, port)}
		}
	}
	if len(addrs) > 1 {
		return nil, &dialError{""}
	}
	return nil, first
}

// addrConfig keeps the addresses getaddrinfo gave Node, which asked with
// AI_ADDRCONFIG: those of a family the machine has an address of on an
// interface other than loopback. Without any of either family the list
// stays as it is.
func addrConfig(ips []string) []string {
	has4, has6 := false, false
	if addrs, err := net.InterfaceAddrs(); err == nil {
		for _, a := range addrs {
			n, ok := a.(*net.IPNet)
			if !ok || n.IP.IsLoopback() || n.IP.IsLinkLocalUnicast() {
				continue
			}
			if n.IP.To4() != nil {
				has4 = true
			} else {
				has6 = true
			}
		}
	}
	var kept []string
	for _, text := range ips {
		ip := net.ParseIP(text)
		if ip != nil && (ip.To4() != nil && has4 || ip.To4() == nil && has6) {
			kept = append(kept, text)
		}
	}
	if len(kept) == 0 {
		return ips
	}
	return kept
}

// connectMessage is Node's text for a failed connect: "connect
// ECONNREFUSED 127.0.0.1:5000".
func connectMessage(err error, ip, port string) string {
	codes := []struct {
		errno syscall.Errno
		code  string
	}{
		{syscall.ECONNREFUSED, "ECONNREFUSED"},
		{syscall.ECONNRESET, "ECONNRESET"},
		{syscall.EHOSTUNREACH, "EHOSTUNREACH"},
		{syscall.ENETUNREACH, "ENETUNREACH"},
		{syscall.EADDRNOTAVAIL, "EADDRNOTAVAIL"},
		{syscall.ETIMEDOUT, "ETIMEDOUT"},
	}
	for _, c := range codes {
		if errors.Is(err, c.errno) {
			return "connect " + c.code + " " + ip + ":" + port
		}
	}
	return err.Error()
}

// nodeMessage is the message Node gave a failed request or connection.
func nodeMessage(err error, ctx context.Context, timedOut string) string {
	var de *dialError
	var verr *verifyError
	switch {
	case errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil:
		return timedOut
	case errors.As(err, &verr):
		return verr.message
	case errors.As(err, &de):
		return de.message
	case errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF):
		return "socket hang up"
	case errors.Is(err, syscall.ECONNRESET):
		return "read ECONNRESET"
	case strings.Contains(err.Error(), "malformed HTTP"):
		// An answer that is no HTTP, as Node's parser named it
		return "Parse Error: Expected HTTP/, RTSP/ or ICE/"
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return urlErr.Err.Error()
	}
	return err.Error()
}

// verifyError is a certificate Node refused, with Node's message.
type verifyError struct{ message string }

func (e *verifyError) Error() string { return e.message }

// verifyLikeNode checks a server's certificate as Node's TLS client did
// with rejectUnauthorized: the chain first (OpenSSL's messages), then the
// host (tls.checkServerIdentity).
func verifyLikeNode(chain []*x509.Certificate, host string, opts checkOptions) error {
	if len(chain) == 0 {
		return &verifyError{"unable to verify the first certificate"}
	}
	leaf := chain[0]
	intermediates := x509.NewCertPool()
	for _, c := range chain[1:] {
		intermediates.AddCert(c)
	}
	roots := opts.roots
	if roots == nil {
		var err error
		if roots, err = x509.SystemCertPool(); err != nil {
			roots = x509.NewCertPool()
		}
	}
	now := opts.now()
	_, err := leaf.Verify(x509.VerifyOptions{Roots: roots, Intermediates: intermediates, CurrentTime: now})
	if err != nil {
		var unknown x509.UnknownAuthorityError
		var invalid x509.CertificateInvalidError
		switch {
		case errors.As(err, &unknown):
			selfSigned := func(c *x509.Certificate) bool {
				return string(c.RawIssuer) == string(c.RawSubject) && c.CheckSignature(c.SignatureAlgorithm, c.RawTBSCertificate, c.Signature) == nil
			}
			switch {
			case selfSigned(leaf):
				return &verifyError{"self-signed certificate"}
			case len(chain) > 1 && selfSigned(chain[len(chain)-1]):
				return &verifyError{"self-signed certificate in certificate chain"}
			case len(chain) == 1:
				return &verifyError{"unable to verify the first certificate"}
			}
			return &verifyError{"unable to get local issuer certificate"}
		case errors.As(err, &invalid) && invalid.Reason == x509.Expired:
			if now.Before(invalid.Cert.NotBefore) {
				return &verifyError{"certificate is not yet valid"}
			}
			return &verifyError{"certificate has expired"}
		}
		return &verifyError{err.Error()}
	}
	if reason := serverIdentity(host, subjectAltName(leaf), leaf.Subject.CommonName); reason != "" {
		return &verifyError{"Hostname/IP does not match certificate's altnames: " + reason}
	}
	return nil
}

// subjectAltName is the certificate's subjectaltname as Node writes it:
// "DNS:a, IP Address:127.0.0.1", an IPv6 address as eight groups of
// capital hex digits.
func subjectAltName(c *x509.Certificate) string {
	var parts []string
	for _, name := range c.DNSNames {
		parts = append(parts, "DNS:"+name)
	}
	for _, ip := range c.IPAddresses {
		text := ip.String()
		if len(ip) == net.IPv6len {
			groups := make([]string, 8)
			for i := range groups {
				groups[i] = strings.ToUpper(strconv.FormatUint(uint64(ip[2*i])<<8|uint64(ip[2*i+1]), 16))
			}
			text = strings.Join(groups, ":")
		}
		parts = append(parts, "IP Address:"+text)
	}
	for _, mail := range c.EmailAddresses {
		parts = append(parts, "email:"+mail)
	}
	for _, uri := range c.URIs {
		parts = append(parts, "URI:"+uri.String())
	}
	return strings.Join(parts, ", ")
}

// canonicalIP is the address as Node's canonicalizeIP writes it (as
// inet_ntop does): "" for text that is no address.
func canonicalIP(text string) string {
	ip := net.ParseIP(text)
	switch {
	case ip == nil:
		return ""
	case !strings.Contains(text, ":"):
		return ip.To4().String()
	case ip.To4() != nil:
		return "::ffff:" + ip.To4().String()
	}
	return ip.String()
}

// serverIdentity is Node's tls.checkServerIdentity on a certificate's
// subjectaltname and common name: the reason it is not for host, "" when
// it is. A host name is matched in its ASCII form (domainToASCII), and a
// host that is an address only in that form ("0127.0.0.1") is refused.
func serverIdentity(host, altNames, cn string) string {
	var dnsNames, ips []string
	if altNames != "" {
		for _, name := range strings.Split(altNames, ", ") {
			if rest, ok := strings.CutPrefix(name, "DNS:"); ok {
				dnsNames = append(dnsNames, rest)
			} else if rest, ok := strings.CutPrefix(name, "IP Address:"); ok {
				ips = append(ips, canonicalIP(rest))
			}
		}
	}
	ascii := strings.TrimSuffix(domainToASCII(host), ".")
	host = strings.TrimSuffix(host, ".")
	hostIsIP := net.ParseIP(host) != nil
	if !hostIsIP && net.ParseIP(ascii) != nil {
		return "Host is a non-canonical IP address"
	}
	if hostIsIP {
		want := canonicalIP(host)
		for _, ip := range ips {
			if ip == want {
				return ""
			}
		}
		return "IP: " + host + " is not in the cert's list: " + strings.Join(ips, ", ")
	}
	hostParts := splitHost(ascii)
	switch {
	case len(dnsNames) > 0:
		for _, name := range dnsNames {
			if wildcardMatch(hostParts, name) {
				return ""
			}
		}
		return "Host: " + host + ". is not in the cert's altnames: " + altNames
	case cn != "":
		if wildcardMatch(hostParts, cn) {
			return ""
		}
		return "Host: " + host + ". is not cert's CN: " + cn
	}
	return "Cert does not contain a DNS name"
}

// domainToASCII is url.domainToASCII for a host name of ASCII letters:
// the name in lower case, a name that ends in a number read as an IPv4
// address the way URLs read one ("0127.0.0.1" is 87.0.0.1, "127.1" is
// 127.0.0.1), and "" for a name URLs refuse. Names with other letters, and
// labels in Punycode, are taken as they are: this check has no IDNA
// tables.
func domainToASCII(host string) string {
	for i := 0; i < len(host); i++ {
		if c := host[i]; c >= 0x80 {
			return strings.ToLower(host)
		} else if c <= ' ' || c == 0x7f || strings.IndexByte("#/:<>?@[\\]^|%", c) >= 0 {
			return ""
		}
	}
	host = strings.ToLower(host)
	if !endsInNumber(host) {
		return host
	}
	ip, ok := parseURLIPv4(host)
	if !ok {
		return ""
	}
	return ip
}

// endsInNumber is the URL standard's "ends in a number": the last label
// (a trailing dot aside) is digits or reads as an IPv4 number.
func endsInNumber(host string) bool {
	parts := strings.Split(host, ".")
	if parts[len(parts)-1] == "" {
		if len(parts) == 1 {
			return false
		}
		parts = parts[:len(parts)-1]
	}
	last := parts[len(parts)-1]
	if last != "" && strings.Trim(last, "0123456789") == "" {
		return true
	}
	_, ok := parseIPv4Number(last)
	return ok
}

// parseIPv4Number reads one part of an IPv4 address in a URL: decimal,
// octal with a leading 0, or hex with 0x.
func parseIPv4Number(part string) (uint64, bool) {
	if part == "" {
		return 0, false
	}
	base := 10
	switch {
	case len(part) >= 2 && (part[:2] == "0x" || part[:2] == "0X"):
		base, part = 16, part[2:]
	case len(part) >= 2 && part[0] == '0':
		base, part = 8, part[1:]
	}
	if part == "" {
		return 0, true
	}
	n, err := strconv.ParseUint(part, base, 64)
	if err != nil {
		if errors.Is(err, strconv.ErrRange) && strings.Trim(strings.ToLower(part), "0123456789abcdef"[:base]) == "" {
			return math.MaxUint64, true
		}
		return 0, false
	}
	return n, true
}

// parseURLIPv4 is the URL standard's IPv4 parser, answering the address
// in dotted form.
func parseURLIPv4(host string) (string, bool) {
	parts := strings.Split(host, ".")
	if parts[len(parts)-1] == "" && len(parts) > 1 {
		parts = parts[:len(parts)-1]
	}
	if len(parts) > 4 {
		return "", false
	}
	numbers := make([]uint64, len(parts))
	for i, part := range parts {
		n, ok := parseIPv4Number(part)
		if !ok {
			return "", false
		}
		numbers[i] = n
	}
	last := numbers[len(numbers)-1]
	for _, n := range numbers[:len(numbers)-1] {
		if n > 255 {
			return "", false
		}
	}
	if last >= uint64(1)<<(8*(5-len(numbers))) {
		return "", false
	}
	ipv4 := last
	for i, n := range numbers[:len(numbers)-1] {
		ipv4 += n << (8 * (3 - i))
	}
	return fmt.Sprintf("%d.%d.%d.%d", ipv4>>24&255, ipv4>>16&255, ipv4>>8&255, ipv4&255), true
}

func splitHost(host string) []string {
	return strings.Split(strings.ToLower(strings.TrimSuffix(host, ".")), ".")
}

var nonPrintable = regexp.MustCompile(`[^\x21-\x7F]`)

// wildcardMatch is the check of Node's checkServerIdentity: the same
// number of labels, all but the first equal, and the first equal or
// matched by one "*" in a name of three labels or more.
func wildcardMatch(hostParts []string, pattern string) bool {
	if pattern == "" {
		return false
	}
	parts := splitHost(pattern)
	if len(hostParts) != len(parts) {
		return false
	}
	for _, p := range parts {
		if p == "" || nonPrintable.MatchString(p) {
			return false
		}
	}
	for i := len(hostParts) - 1; i > 0; i-- {
		if hostParts[i] != parts[i] {
			return false
		}
	}
	sub, pat := hostParts[0], parts[0]
	pieces := strings.SplitN(pat, "*", 3)
	if len(pieces) == 1 || strings.Contains(pat, "xn--") {
		return sub == pat
	}
	if len(pieces) > 2 || len(parts) <= 2 {
		return false
	}
	prefix, suffix := pieces[0], pieces[1]
	return len(prefix)+len(suffix) <= len(sub) && strings.HasPrefix(sub, prefix) && strings.HasSuffix(sub, suffix)
}
