package netmonitor

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"strings"
	"sync"
	"syscall"
	"testing"

	"github.com/unitronix/betterdesk-server/console/ssrfguard"
	"github.com/unitronix/betterdesk-server/console/ssrfguard/guardtest"
)

// outcome is what a function of the Node console answered: its result, or
// the message of the error it threw.
type outcome struct {
	OK      json.RawMessage `json:"ok"`
	Error   *string         `json:"error"`
	Blocked bool            `json:"blocked"`
}

// guardVectors is testdata/guard.json.gz (testdata/README.md).
type guardVectors struct {
	DNS       map[string]json.RawMessage `json:"dns"`
	BlockedIP [][]json.RawMessage        `json:"blocked_ip"`
	Hostname  [][]json.RawMessage        `json:"hostname"`
	Resolved  [][]json.RawMessage        `json:"resolved"`
	URL       [][]json.RawMessage        `json:"url"`
	Resolve   [][]json.RawMessage        `json:"resolve"`
}

var loadGuardVectors = sync.OnceValues(func() (*guardVectors, error) {
	f, err := os.Open("testdata/guard.json.gz")
	if err != nil {
		return nil, err
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		return nil, err
	}
	var v guardVectors
	if err := json.NewDecoder(zr).Decode(&v); err != nil {
		return nil, err
	}
	return &v, nil
})

func vectors(t testing.TB) *guardVectors {
	t.Helper()
	v, err := loadGuardVectors()
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func decode[T any](t testing.TB, raw json.RawMessage) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	return v
}

// tableResolver answers lookups from a table of names: addresses, or the
// code of the error.
type tableResolver struct {
	mu      sync.Mutex
	names   map[string][]string
	errors  map[string]string
	lookups []string
}

func newTableResolver(t testing.TB, table map[string]json.RawMessage) *tableResolver {
	t.Helper()
	r := &tableResolver{names: map[string][]string{}, errors: map[string]string{}}
	for name, raw := range table {
		var addresses []string
		if err := json.Unmarshal(raw, &addresses); err == nil {
			r.names[name] = addresses
			continue
		}
		r.errors[name] = decode[struct {
			Error string `json:"error"`
		}](t, raw).Error
	}
	return r
}

func (r *tableResolver) LookupIPAddr(_ context.Context, host string) ([]net.IPAddr, error) {
	r.mu.Lock()
	r.lookups = append(r.lookups, host)
	r.mu.Unlock()
	if code, ok := r.errors[host]; ok {
		return nil, &net.DNSError{Err: code, Name: host, IsNotFound: code == "ENOTFOUND", IsTemporary: code != "ENOTFOUND"}
	}
	addresses, ok := r.names[host]
	if !ok {
		return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
	}
	out := make([]net.IPAddr, len(addresses))
	for i, a := range addresses {
		out[i] = net.IPAddr{IP: net.ParseIP(a)}
	}
	return out, nil
}

// differences counts the answers that are not Node's on purpose, by the
// row of BUGS.md or the decision that explains them.
type differences map[string]int

// compare checks a Go result against what Node answered, for a check in
// the mode allowPrivate. An answer that differs must be one a fixed row of
// BUGS.md explains (guardtest).
func (d differences) compare(t *testing.T, what string, want outcome, got any, err error, allowPrivate bool) {
	t.Helper()
	nodeErr, goErr := "", ""
	if want.Error != nil {
		nodeErr = *want.Error
	}
	if err != nil {
		goErr = err.Error()
	}
	if nodeErr != goErr {
		bug := guardtest.MessageChange(nodeErr, goErr, allowPrivate)
		if bug == "unexplained" {
			t.Errorf("%s: %q here, Node threw %q and answered %s", what, goErr, nodeErr, want.OK)
		}
		d[bug]++
		return
	}
	if err != nil {
		var refusal *ssrfguard.BlockedError
		if errors.As(err, &refusal) != want.Blocked {
			t.Errorf("%s: is a refusal of the guard: %v, in Node: %v", what, !want.Blocked, want.Blocked)
		}
		return
	}
	gotJSON, _ := json.Marshal(got)
	var a, b any
	_ = json.Unmarshal(gotJSON, &a)
	_ = json.Unmarshal(want.OK, &b)
	wantJSON, _ := json.Marshal(b)
	gotJSON, _ = json.Marshal(a)
	if string(gotJSON) != string(wantJSON) {
		t.Errorf("%s: got %s, Node answered %s", what, gotJSON, wantJSON)
	}
}

// needsIDNA reports whether the host of a URL has letters outside ASCII
// or a label in the "xn--" form: the console's URL parser refuses such a
// host (DECISIONS A21), where Node converted it.
func needsIDNA(raw string) bool {
	_, rest, ok := strings.Cut(strings.TrimSpace(raw), ":")
	if !ok {
		return false
	}
	rest = strings.TrimLeft(rest, `/\`)
	if i := strings.IndexAny(rest, `/?#\`); i >= 0 {
		rest = rest[:i]
	}
	if i := strings.LastIndex(rest, "@"); i >= 0 {
		rest = rest[i+1:]
	}
	host := percentDecode(rest)
	for i := 0; i < len(host); i++ {
		if host[i] >= 0x80 {
			return true
		}
	}
	return strings.Contains("."+strings.ToLower(host), ".xn--")
}

// The vectors of the guard were recorded from lib/ssrfGuard.js, which the
// checks now reach through console/ssrfguard.

func TestNodeBlockedIP(t *testing.T) {
	v := vectors(t)
	if len(v.BlockedIP) < 10000 {
		t.Fatalf("only %d vectors", len(v.BlockedIP))
	}
	changed := differences{}
	for _, row := range v.BlockedIP {
		ip := decode[string](t, row[0])
		allow := decode[*bool](t, row[1])
		private := allow != nil && *allow
		node := decode[bool](t, row[2])
		if guardtest.NodeBlocked(ip, private) != node {
			t.Fatalf("guardtest.NodeBlocked(%q, %v) is not what Node answered", ip, private)
		}
		want := guardtest.Blocked(ip, private)
		if got := ssrfguard.IsBlockedIP(ip, ssrfguard.Options{AllowPrivate: private}); got != want {
			t.Errorf("IsBlockedIP(%q, %v) = %v, want %v (Node: %v)", ip, private, got, want, node)
		}
		if bug := guardtest.Change(ip, private); bug != "" {
			changed[bug]++
		}
	}
	t.Logf("answers that are not Node's: %v", changed)
}

func TestNodeSafeHostname(t *testing.T) {
	v := vectors(t)
	if len(v.Hostname) < 500 {
		t.Fatalf("only %d vectors", len(v.Hostname))
	}
	changed := differences{}
	for _, row := range v.Hostname {
		host, allow := decode[string](t, row[0]), decode[bool](t, row[1])
		clean, err := ssrfguard.SafeHostname(host, ssrfguard.Options{AllowPrivate: allow})
		changed.compare(t, "assertSafeHostname "+string(row[0])+" "+string(row[1]), decode[outcome](t, row[2]), clean, err, allow)
	}
	t.Logf("answers that are not Node's: %v", changed)
}

func TestNodeSafeResolvedHost(t *testing.T) {
	v := vectors(t)
	if len(v.Resolved) < 500 {
		t.Fatalf("only %d vectors", len(v.Resolved))
	}
	g := newGuard(newTableResolver(t, v.DNS))
	changed := differences{}
	for _, row := range v.Resolved {
		host, allow := decode[string](t, row[0]), decode[bool](t, row[1])
		clean, err := g.SafeResolvedHost(context.Background(), host, ssrfguard.Options{AllowPrivate: allow})
		changed.compare(t, "assertSafeResolvedHost "+string(row[0])+" "+string(row[1]), decode[outcome](t, row[2]), clean, err, allow)
	}
	t.Logf("answers that are not Node's: %v", changed)
}

func TestNodeSafeMonitoringURL(t *testing.T) {
	v := vectors(t)
	if len(v.URL) < 3000 {
		t.Fatalf("only %d vectors", len(v.URL))
	}
	g := newGuard(newTableResolver(t, v.DNS))
	changed := differences{}
	for _, row := range v.URL {
		raw := decode[string](t, row[0])
		want := decode[outcome](t, row[1])
		parsed, err := g.SafeMonitoringURL(context.Background(), raw)
		if err != nil && err.Error() == "Invalid URL" && (want.Error == nil || *want.Error != "Invalid URL") && needsIDNA(raw) {
			changed["A21"]++
			continue
		}
		var got map[string]string
		if err == nil {
			got = map[string]string{
				"protocol": parsed.Protocol, "hostname": parsed.Hostname, "port": parsed.Port,
				"pathname": parsed.Pathname, "search": parsed.Search,
			}
		}
		changed.compare(t, "assertSafeMonitoringUrl "+string(row[0]), want, got, err, true)
	}
	t.Logf("answers that are not Node's: %v", changed)
}

func TestNodeResolveHost(t *testing.T) {
	v := vectors(t)
	if len(v.Resolve) < 300 {
		t.Fatalf("only %d vectors", len(v.Resolve))
	}
	resolver := newTableResolver(t, v.DNS)
	p := prober{guard: newGuard(resolver)}
	changed := differences{}
	for _, row := range v.Resolve {
		host := decode[string](t, row[0])
		address, err := p.resolveHost(context.Background(), host)
		changed.compare(t, "resolveHost "+string(row[0]), decode[outcome](t, row[1]), address, err, true)
	}
	t.Logf("answers that are not Node's: %v", changed)
}

// The checks refuse loopback and link-local however they are written,
// before anything leaves the server (BUGS.md SC1).
func TestChecksRefuseEverySpellingOfLoopback(t *testing.T) {
	resolver := &tableResolver{names: map[string][]string{"mapped.test": {"::ffff:7f00:1"}}, errors: map[string]string{}}
	nothing := dialerFunc(func(_ context.Context, _, address string) (net.Conn, error) {
		t.Errorf("a connection to %s", address)
		return nil, errors.New("no connection in this test")
	})
	no := false
	m := New(Options{Resolver: resolver, Dialer: nothing, Windows: &no, Ping: func(_ context.Context, _ string, args ...string) ([]byte, int, error) {
		t.Errorf("ping %q", args)
		return nil, 1, nil
	}})
	ctx := context.Background()
	for _, host := range []string{"0:0:0:0:0:0:0:1", "::ffff:7f00:1", "::127.0.0.1", "febf::1"} {
		if res := m.CheckTCPPort(ctx, host, 80, 1000); res.Error == nil || *res.Error != "Blocked IP address: "+host {
			t.Errorf("tcp %s: %s", host, errText(res))
		}
		if res := m.PingHost(ctx, host, 1000); res.Error == nil || *res.Error != "Blocked IP address: "+host {
			t.Errorf("ping %s: %s", host, errText(res))
		}
	}
	if res := m.CheckTCPPort(ctx, "mapped.test", 80, 1000); res.Error == nil || *res.Error != "Hostname mapped.test resolves to blocked address ::ffff:7f00:1" && *res.Error != "Hostname mapped.test resolves to blocked address 127.0.0.1" {
		t.Errorf("tcp of a name: %s", errText(res))
	}
	for url, want := range map[string]string{
		"http://[::ffff:127.0.0.1]:5000/":  "Blocked IP address: ::ffff:7f00:1",
		"http://[::ffff:169.254.169.254]/": "Blocked IP address: ::ffff:a9fe:a9fe",
		"http://[0:0:0:0:0:0:0:1]/":        "Blocked IP address: ::1",
	} {
		if res := m.CheckHTTP(ctx, url, 1000); res.Error == nil || *res.Error != want {
			t.Errorf("http %s: %s, want %q", url, errText(res), want)
		}
	}
}

// 0.0.0.0 and :: reach the server itself, so the checks refuse them like
// loopback (BUGS.md SC3).
func TestChecksRefuseThisNetwork(t *testing.T) {
	resolver := &tableResolver{names: map[string][]string{"zero.test": {"0.0.0.0"}}, errors: map[string]string{}}
	nothing := dialerFunc(func(_ context.Context, _, address string) (net.Conn, error) {
		t.Errorf("a connection to %s", address)
		return nil, errors.New("no connection in this test")
	})
	no := false
	m := New(Options{Resolver: resolver, Dialer: nothing, Windows: &no, Ping: func(_ context.Context, _ string, args ...string) ([]byte, int, error) {
		t.Errorf("ping %q", args)
		return nil, 1, nil
	}})
	ctx := context.Background()
	for _, host := range []string{"0.0.0.0", "0.1.2.3", "::"} {
		want := "Blocked IP address: " + host
		if res := m.CheckTCPPort(ctx, host, 5000, 1000); errText(res) != want {
			t.Errorf("tcp %s: %s", host, errText(res))
		}
		if res := m.PingHost(ctx, host, 1000); errText(res) != want {
			t.Errorf("ping %s: %s", host, errText(res))
		}
		if _, err := m.ResolveHost(ctx, host); err == nil || err.Error() != want {
			t.Errorf("resolve %s: %v", host, err)
		}
	}
	if res := m.CheckTCPPort(ctx, "zero.test", 5000, 1000); errText(res) != "Hostname zero.test resolves to blocked address 0.0.0.0" {
		t.Errorf("tcp zero.test: %s", errText(res))
	}
	for url, want := range map[string]string{
		"http://0.0.0.0:5000/health": "Blocked IP address: 0.0.0.0",
		"http://[::]:5000/health":    "Blocked IP address: ::",
		"https://zero.test/":         "Hostname zero.test resolves to blocked address 0.0.0.0",
	} {
		if res := m.CheckHTTP(ctx, url, 1000); errText(res) != want {
			t.Errorf("http %s: %s, want %q", url, errText(res), want)
		}
	}
}

// rebinding answers its first lookup with a public address and every
// later one with loopback.
type rebinding struct {
	mu      sync.Mutex
	lookups int
}

func (r *rebinding) LookupIPAddr(context.Context, string) ([]net.IPAddr, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lookups++
	if r.lookups == 1 {
		return []net.IPAddr{{IP: net.ParseIP("192.0.2.50")}}, nil
	}
	return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
}

// A check goes to the address the guard checked. A name that answers the
// guard with a public address and the next lookup with loopback does not
// lead it to loopback (BUGS.md SC4).
func TestChecksGoToTheAddressesTheGuardChecked(t *testing.T) {
	var mu sync.Mutex
	var dialed, pinged []string
	refuse := dialerFunc(func(_ context.Context, network, address string) (net.Conn, error) {
		mu.Lock()
		dialed = append(dialed, address)
		mu.Unlock()
		return nil, &net.OpError{Op: "dial", Net: network, Err: os.NewSyscallError("connect", syscall.ECONNREFUSED)}
	})
	ping := func(_ context.Context, _ string, args ...string) ([]byte, int, error) {
		pinged = append(pinged, args[len(args)-1])
		return []byte("time=1.5 ms\n"), 0, nil
	}
	no := false
	ctx := context.Background()
	check := func(name string, run func(m *Monitor) Result, want string) {
		t.Helper()
		resolver := &rebinding{}
		dialed, pinged = nil, nil
		res := run(New(Options{Resolver: resolver, Dialer: refuse, Windows: &no, Ping: ping}))
		if errText(res) != want {
			t.Errorf("%s: %s, want %s", name, errText(res), want)
		}
		if resolver.lookups != 1 {
			t.Errorf("%s: %d lookups, want the guard's one", name, resolver.lookups)
		}
	}
	check("tcp", func(m *Monitor) Result { return m.CheckTCPPort(ctx, "rebind.test", 80, 1000) }, "connect ECONNREFUSED 192.0.2.50:80")
	if len(dialed) != 1 || dialed[0] != "192.0.2.50:80" {
		t.Errorf("tcp went to %q", dialed)
	}
	check("http", func(m *Monitor) Result { return m.CheckHTTP(ctx, "http://rebind.test:8080/", 1000) }, "connect ECONNREFUSED 192.0.2.50:8080")
	if len(dialed) != 1 || dialed[0] != "192.0.2.50:8080" {
		t.Errorf("http went to %q", dialed)
	}
	check("ping", func(m *Monitor) Result { return m.PingHost(ctx, "rebind.test", 1000) }, "no error")
	if len(pinged) != 1 || pinged[0] != "192.0.2.50" {
		t.Errorf("ping went to %q", pinged)
	}
	resolver := &rebinding{}
	if address, err := New(Options{Resolver: resolver, Dialer: refuse}).ResolveHost(ctx, "rebind.test"); err != nil || address != "192.0.2.50" || resolver.lookups != 1 {
		t.Errorf("resolve: %q, %v, %d lookups", address, err, resolver.lookups)
	}
}

// errText is the error of a result, or what it is when there is none.
func errText(res Result) string {
	if res.Error == nil {
		return "no error"
	}
	return *res.Error
}

// The guard asks the resolver once for a name and never for an address.
func TestGuardLooksNamesUpOnce(t *testing.T) {
	resolver := newTableResolver(t, vectors(t).DNS)
	g := newGuard(resolver)
	for _, host := range []string{"10.0.0.1", "[2001:db8::1]", "127.1", "localhost", "example.com", " Example.COM "} {
		_, _ = g.SafeResolvedHost(context.Background(), host, tools)
	}
	if len(resolver.lookups) != 2 || resolver.lookups[0] != "example.com" || resolver.lookups[1] != "example.com" {
		t.Errorf("lookups: %q, want example.com twice", resolver.lookups)
	}
}
