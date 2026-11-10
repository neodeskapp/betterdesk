package netmonitor

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"sync"
	"testing"
)

// outcome is what a function of the Node console answered: its result, or
// the message of the error it threw.
type outcome struct {
	OK      json.RawMessage `json:"ok"`
	Error   *string         `json:"error"`
	Blocked bool            `json:"blocked"`
}

// guardVectors is testdata/guard.json.gz (testdata/nodeoracle/guard.js).
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

// compare checks a Go result against what Node answered.
func compare(t *testing.T, what string, want outcome, got any, err error) {
	t.Helper()
	if want.Error != nil {
		if err == nil {
			t.Errorf("%s: got %v, Node threw %q", what, got, *want.Error)
			return
		}
		if err.Error() != *want.Error {
			t.Errorf("%s: error %q, Node threw %q", what, err.Error(), *want.Error)
		}
		var refusal *BlockedError
		if errors.As(err, &refusal) != want.Blocked {
			t.Errorf("%s: is a refusal of the guard: %v, in Node: %v", what, !want.Blocked, want.Blocked)
		}
		return
	}
	if err != nil {
		t.Errorf("%s: error %q, Node answered %s", what, err.Error(), want.OK)
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

func TestNodeBlockedIP(t *testing.T) {
	v := vectors(t)
	if len(v.BlockedIP) < 10000 {
		t.Fatalf("only %d vectors", len(v.BlockedIP))
	}
	for _, row := range v.BlockedIP {
		ip := decode[string](t, row[0])
		allow := decode[*bool](t, row[1])
		want := decode[bool](t, row[2])
		if got := isBlockedIP(ip, allow != nil && *allow); got != want {
			t.Errorf("isBlockedIP(%q, %v) = %v, Node: %v", ip, allow != nil && *allow, got, want)
		}
	}
}

func TestNodeSafeHostname(t *testing.T) {
	v := vectors(t)
	if len(v.Hostname) < 500 {
		t.Fatalf("only %d vectors", len(v.Hostname))
	}
	for _, row := range v.Hostname {
		host, allow := decode[string](t, row[0]), decode[bool](t, row[1])
		clean, err := assertSafeHostname(host, allow)
		compare(t, "assertSafeHostname "+string(row[0])+" "+string(row[1]), decode[outcome](t, row[2]), clean, err)
	}
}

func TestNodeSafeResolvedHost(t *testing.T) {
	v := vectors(t)
	if len(v.Resolved) < 500 {
		t.Fatalf("only %d vectors", len(v.Resolved))
	}
	g := ssrfGuard{resolver: newTableResolver(t, v.DNS)}
	for _, row := range v.Resolved {
		host, allow := decode[string](t, row[0]), decode[bool](t, row[1])
		clean, err := g.safeResolvedHost(context.Background(), host, allow)
		compare(t, "assertSafeResolvedHost "+string(row[0])+" "+string(row[1]), decode[outcome](t, row[2]), clean, err)
	}
}

func TestNodeSafeMonitoringURL(t *testing.T) {
	v := vectors(t)
	if len(v.URL) < 3000 {
		t.Fatalf("only %d vectors", len(v.URL))
	}
	g := ssrfGuard{resolver: newTableResolver(t, v.DNS)}
	for _, row := range v.URL {
		raw := decode[string](t, row[0])
		parsed, err := g.SafeMonitoringURL(context.Background(), raw)
		got := map[string]string{
			"protocol": parsed.Protocol, "hostname": parsed.Hostname, "port": parsed.Port,
			"pathname": parsed.Pathname, "search": parsed.Search,
		}
		compare(t, "assertSafeMonitoringUrl "+string(row[0]), decode[outcome](t, row[1]), got, err)
	}
}

func TestNodeResolveHost(t *testing.T) {
	v := vectors(t)
	if len(v.Resolve) < 300 {
		t.Fatalf("only %d vectors", len(v.Resolve))
	}
	resolver := newTableResolver(t, v.DNS)
	p := prober{guard: ssrfGuard{resolver: resolver}, resolver: resolver}
	for _, row := range v.Resolve {
		host := decode[string](t, row[0])
		address, err := p.resolveHost(context.Background(), host)
		compare(t, "resolveHost "+string(row[0]), decode[outcome](t, row[1]), address, err)
	}
}

// The guard asks the resolver once for a name and never for an address.
func TestGuardLooksNamesUpOnce(t *testing.T) {
	resolver := newTableResolver(t, vectors(t).DNS)
	g := ssrfGuard{resolver: resolver}
	for _, host := range []string{"10.0.0.1", "[2001:db8::1]", "127.1", "localhost", "example.com", " Example.COM "} {
		_, _ = g.SafeResolvedHost(context.Background(), host)
	}
	if len(resolver.lookups) != 2 || resolver.lookups[0] != "example.com" || resolver.lookups[1] != "example.com" {
		t.Errorf("lookups: %q, want example.com twice", resolver.lookups)
	}
}
