package smtpwire

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"os"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// Tests for what the recorded sessions cannot show on one machine: which
// address is picked, the timeouts of connecting, a cancelled context, the
// message writer under a server that stops reading, and the defaults.

func op(text string) sinkOp { return sinkOp{W: &text} }

// acceptingScript is a sink that takes everything, with the given EHLO
// extensions.
func acceptingScript(features ...string) sinkScript {
	ehlo := "250 sink.example.test\r\n"
	if len(features) > 0 {
		ehlo = "250-sink.example.test\r\n"
		for i, feature := range features {
			sep := "-"
			if i == len(features)-1 {
				sep = " "
			}
			ehlo += "250" + sep + feature + "\r\n"
		}
	}
	return sinkScript{
		Cert:     "localhost",
		Greeting: []sinkOp{op("220 sink.example.test ESMTP\r\n")},
		Rules: map[string][][]sinkOp{
			"EHLO":       {{op(ehlo)}},
			"STARTTLS":   {{op("220 go ahead\r\n"), {TLS: 1}}},
			"AUTH PLAIN": {{op("235 ok\r\n")}},
			"DATA":       {{op("354 go ahead\r\n"), {Data: 1}}},
			"ENDDATA":    {{op("250 queued\r\n")}},
			"QUIT":       {{op("221 bye\r\n"), {Close: 1}}},
		},
	}
}

func testHostname() (string, error) { return "client.example.test", nil }

// sinkOptions are options that reach the sink under the name "sink.test".
func sinkOptions(s *sink) Options {
	return Options{
		Host:     "127.0.0.1",
		Port:     2525,
		Hostname: testHostname,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			var dialer net.Dialer
			return dialer.DialContext(ctx, network, s.addr())
		},
	}
}

func lines(events []sinkEvent) []string {
	var out []string
	for _, event := range events {
		if event.E == "line" {
			out = append(out, strings.TrimRight(event.S, "\r\n"))
		} else {
			out = append(out, "<"+event.E+">")
		}
	}
	return out
}

func wantError(t *testing.T, err error, message, code string) *Error {
	t.Helper()
	var got *Error
	if !errors.As(err, &got) {
		t.Fatalf("got %v (%T), want an *Error %q", err, err, message)
	}
	if got.Message != message || got.Code != code || got.Command != "CONN" {
		t.Fatalf("got %q %s %s, want %q %s CONN", got.Message, got.Code, got.Command, message, code)
	}
	return got
}

func TestDefaults(t *testing.T) {
	c := newClient(context.Background(), Options{Hostname: testHostname})
	if c.host != "localhost" || c.port != 587 {
		t.Errorf("host %q port %d, want localhost 587", c.host, c.port)
	}
	if c := newClient(context.Background(), Options{Secure: true, Hostname: testHostname}); c.port != 465 {
		t.Errorf("port %d with Secure, want 465", c.port)
	}
	if c := newClient(context.Background(), Options{Secure: true, Port: 2525, Hostname: testHostname}); c.port != 2525 {
		t.Errorf("port %d, want the given 2525", c.port)
	}
	if c.connectionTimeout != 2*time.Minute || c.greetingTimeout != 30*time.Second ||
		c.socketTimeout != 10*time.Minute || c.dnsTimeout != 30*time.Second {
		t.Errorf("timeouts %v %v %v %v", c.connectionTimeout, c.greetingTimeout, c.socketTimeout, c.dnsTimeout)
	}
	custom := newClient(context.Background(), Options{
		Hostname: testHostname, ConnectionTimeout: time.Second, GreetingTimeout: 2 * time.Second,
		SocketTimeout: 3 * time.Second, DNSTimeout: 4 * time.Second,
	})
	if custom.connectionTimeout != time.Second || custom.greetingTimeout != 2*time.Second ||
		custom.socketTimeout != 3*time.Second || custom.dnsTimeout != 4*time.Second {
		t.Errorf("given timeouts are not used: %v %v %v %v", custom.connectionTimeout, custom.greetingTimeout, custom.socketTimeout, custom.dnsTimeout)
	}
	for _, creds := range []struct {
		user, pass string
		use        bool
	}{{"u", "p", true}, {"u", "", false}, {"", "p", false}, {"", "", false}} {
		if c := newClient(context.Background(), Options{User: creds.user, Pass: creds.pass, Hostname: testHostname}); c.useCreds != creds.use {
			t.Errorf("user %q pass %q: credentials used %v, want %v", creds.user, creds.pass, c.useCreds, creds.use)
		}
	}
	if closeTimeout != 5*time.Second || attemptTimeout != 250*time.Millisecond || maxResponseSize != 1<<20 {
		t.Errorf("constants changed: %v %v %d", closeTimeout, attemptTimeout, maxResponseSize)
	}
}

func TestIPFamily(t *testing.T) {
	for host, want := range map[string]int{
		"127.0.0.1": 4, "10.1.2.3": 4, "::1": 6, "2001:db8::1": 6, "::ffff:10.1.2.3": 6, "fe80::1%eth0": 6,
		"localhost": 0, "": 0, "127.1": 0, "1.2.3.4.5": 0, "256.1.1.1": 0, "01.2.3.4": 0, "[::1]": 0, "smtp.example.test": 0,
	} {
		if got := ipFamily(host); got != want {
			t.Errorf("ipFamily(%q) = %d, want %d", host, got, want)
		}
	}
}

func TestAlternate(t *testing.T) {
	for _, tc := range []struct{ in, want []string }{
		{[]string{"::1", "::2", "10.0.0.1", "10.0.0.2", "10.0.0.3"}, []string{"::1", "10.0.0.1", "::2", "10.0.0.2", "10.0.0.3"}},
		{[]string{"10.0.0.1", "::1", "10.0.0.2"}, []string{"10.0.0.1", "::1", "10.0.0.2"}},
		{[]string{"10.0.0.1", "10.0.0.1", "10.0.0.2"}, []string{"10.0.0.1", "10.0.0.2"}},
		{[]string{"::1", "not an address", "::1"}, []string{"::1"}},
		{nil, nil},
	} {
		if got := alternate(tc.in); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("alternate(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// fakeResolver answers from tables and notes what it was asked.
type fakeResolver struct {
	mu      sync.Mutex
	asked   []string
	records map[string][]string
	system  []string
	sysErr  error
	block   bool
}

func (r *fakeResolver) note(call string) {
	r.mu.Lock()
	r.asked = append(r.asked, call)
	r.mu.Unlock()
}

func (r *fakeResolver) LookupIP(ctx context.Context, network, host string) ([]net.IP, error) {
	r.note(network + " " + host)
	if r.block {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	var ips []net.IP
	for _, ip := range r.records[network] {
		ips = append(ips, net.ParseIP(ip))
	}
	if len(ips) == 0 {
		return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
	}
	return ips, nil
}

func (r *fakeResolver) LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error) {
	r.note("system " + host)
	if r.sysErr != nil {
		return nil, r.sysErr
	}
	var addrs []net.IPAddr
	for _, ip := range r.system {
		addrs = append(addrs, net.IPAddr{IP: net.ParseIP(ip)})
	}
	return addrs, nil
}

func TestResolve(t *testing.T) {
	noShuffle := func(int, func(i, j int)) {}
	reverse := func(n int, swap func(i, j int)) {
		for i, j := 0, n-1; i < j; i, j = i+1, j-1 {
			swap(i, j)
		}
	}
	both := func() (bool, bool) { return true, true }
	onlyV4 := func() (bool, bool) { return true, false }
	onlyV6 := func() (bool, bool) { return false, true }
	none := func() (bool, bool) { return false, false }
	tests := []struct {
		name     string
		host     string
		resolver *fakeResolver
		families func() (bool, bool)
		shuffle  func(int, func(i, j int))
		want     []string
		asked    []string
		attempt  time.Duration
		share    bool
	}{
		{name: "IPv4 address", host: "10.1.2.3", resolver: &fakeResolver{}, families: none, want: []string{"10.1.2.3"}},
		{name: "IPv6 address as written", host: "0:0:0:0:0:0:0:1", resolver: &fakeResolver{}, families: none, want: []string{"0:0:0:0:0:0:0:1"}},
		{name: "one A record", host: "smtp.example.test",
			resolver: &fakeResolver{records: map[string][]string{"ip4": {"192.0.2.1"}}}, families: both,
			want: []string{"192.0.2.1"}, asked: []string{"ip4 smtp.example.test", "ip6 smtp.example.test"}},
		{name: "both families, IPv6 first and alternating", host: "smtp.example.test",
			resolver: &fakeResolver{records: map[string][]string{"ip4": {"192.0.2.1", "192.0.2.2", "192.0.2.3"}, "ip6": {"2001:db8::1", "2001:db8::2"}}},
			families: both, shuffle: noShuffle,
			want:    []string{"2001:db8::1", "192.0.2.1", "2001:db8::2", "192.0.2.2", "192.0.2.3"},
			asked:   []string{"ip4 smtp.example.test", "ip6 smtp.example.test"},
			attempt: 250 * time.Millisecond},
		{name: "addresses of a family are shuffled", host: "smtp.example.test",
			resolver: &fakeResolver{records: map[string][]string{"ip4": {"192.0.2.1", "192.0.2.2"}, "ip6": {"2001:db8::1", "2001:db8::2"}}},
			families: both, shuffle: reverse,
			want:    []string{"2001:db8::2", "192.0.2.2", "2001:db8::1", "192.0.2.1"},
			asked:   []string{"ip4 smtp.example.test", "ip6 smtp.example.test"},
			attempt: 250 * time.Millisecond},
		{name: "no IPv6 on the machine: AAAA is not asked", host: "smtp.example.test",
			resolver: &fakeResolver{records: map[string][]string{"ip4": {"192.0.2.1"}, "ip6": {"2001:db8::1"}}}, families: onlyV4,
			want: []string{"192.0.2.1"}, asked: []string{"ip4 smtp.example.test"}},
		{name: "no IPv4 on the machine: A is not asked", host: "smtp.example.test",
			resolver: &fakeResolver{records: map[string][]string{"ip4": {"192.0.2.1"}, "ip6": {"2001:db8::1"}}}, families: onlyV6,
			want: []string{"2001:db8::1"}, asked: []string{"ip6 smtp.example.test"}},
		{name: "several addresses of one family share the time", host: "smtp.example.test",
			resolver: &fakeResolver{records: map[string][]string{"ip4": {"192.0.2.1", "192.0.2.2", "192.0.2.3", "192.0.2.4"}}},
			families: both, shuffle: noShuffle,
			want:  []string{"192.0.2.1", "192.0.2.2", "192.0.2.3", "192.0.2.4"},
			asked: []string{"ip4 smtp.example.test", "ip6 smtp.example.test"}, share: true},
		{name: "no records: the system's addresses, filtered", host: "localhost",
			resolver: &fakeResolver{system: []string{"::1", "127.0.0.1"}}, families: onlyV4,
			want: []string{"127.0.0.1"}, asked: []string{"ip4 localhost", "system localhost"}},
		{name: "no records: the system's addresses of both families", host: "localhost",
			resolver: &fakeResolver{system: []string{"127.0.0.1", "::1"}}, families: both, shuffle: noShuffle,
			want:    []string{"::1", "127.0.0.1"},
			asked:   []string{"ip4 localhost", "ip6 localhost", "system localhost"},
			attempt: 250 * time.Millisecond},
		{name: "no usable family: connect by name, in the system's order", host: "localhost",
			resolver: &fakeResolver{system: []string{"127.0.0.1", "::1", "127.0.0.2"}}, families: none,
			want: []string{"127.0.0.1", "::1", "127.0.0.2"}, asked: []string{"system localhost"}, attempt: 250 * time.Millisecond},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := newClient(context.Background(), Options{
				Host: tc.host, Resolver: tc.resolver, Families: tc.families, Shuffle: tc.shuffle, Hostname: testHostname,
				ConnectionTimeout: 8 * time.Second,
			})
			c.connectionDeadline = time.Now().Add(c.connectionTimeout)
			got, err := c.resolve()
			if err != nil {
				t.Fatalf("resolve: %v", err)
			}
			if !reflect.DeepEqual(got.addresses, tc.want) {
				t.Errorf("addresses %q, want %q", got.addresses, tc.want)
			}
			asked := append([]string(nil), tc.resolver.asked...)
			if len(asked) >= 2 && asked[0] > asked[1] && strings.HasPrefix(asked[1], "ip") {
				asked[0], asked[1] = asked[1], asked[0]
			}
			if !sameStrings(asked, tc.asked) {
				t.Errorf("lookups %q, want %q", asked, tc.asked)
			}
			switch {
			case tc.share:
				// 8 s for 4 addresses
				if got.perAttempt < 1900*time.Millisecond || got.perAttempt > 2*time.Second {
					t.Errorf("each attempt gets %v, want about 2s", got.perAttempt)
				}
			case len(tc.want) > 1 && got.perAttempt != tc.attempt:
				t.Errorf("each attempt gets %v, want %v", got.perAttempt, tc.attempt)
			}
		})
	}
}

func TestResolveShortShare(t *testing.T) {
	c := newClient(context.Background(), Options{
		Host:     "smtp.example.test",
		Resolver: &fakeResolver{records: map[string][]string{"ip4": {"192.0.2.1", "192.0.2.2", "192.0.2.3"}}},
		Families: func() (bool, bool) { return true, true }, Hostname: testHostname,
	})
	c.connectionDeadline = time.Now().Add(12 * time.Millisecond)
	got, err := c.resolve()
	if err != nil {
		t.Fatal(err)
	}
	if got.perAttempt != 10*time.Millisecond {
		t.Errorf("each attempt gets %v, want the minimum of 10ms", got.perAttempt)
	}
}

func TestResolveErrors(t *testing.T) {
	notFound := &net.DNSError{Err: "no such host", Name: "x", IsNotFound: true}
	temporary := &net.DNSError{Err: "server misbehaving", Name: "x", IsTemporary: true}
	timedOut := &net.DNSError{Err: "i/o timeout", Name: "x", IsTimeout: true}
	for _, tc := range []struct {
		err  error
		want string
	}{
		{notFound, "getaddrinfo ENOTFOUND smtp.example.test"},
		{temporary, "getaddrinfo EAI_AGAIN smtp.example.test"},
		{timedOut, "getaddrinfo EAI_AGAIN smtp.example.test"},
		{errors.New("resolver broke"), "getaddrinfo EAI_FAIL smtp.example.test"},
	} {
		err := Verify(context.Background(), Options{
			Host: "smtp.example.test", Resolver: &fakeResolver{sysErr: tc.err}, Hostname: testHostname,
			Families: func() (bool, bool) { return true, true },
		})
		var got *Error
		if !errors.As(err, &got) || got.Message != tc.want || got.Code != "EDNS" || got.Command != "CONN" {
			t.Errorf("lookup error %v: got %+v, want %q EDNS CONN", tc.err, err, tc.want)
			continue
		}
		if !errors.Is(err, tc.err) {
			t.Errorf("the lookup error %v is not kept as the cause", tc.err)
		}
	}
}

func TestDNSTimeout(t *testing.T) {
	for _, tc := range []struct {
		name            string
		dns, connection time.Duration
	}{
		{"dns timeout", 80 * time.Millisecond, 5 * time.Second},
		{"cut short by the connection timeout", 5 * time.Second, 80 * time.Millisecond},
	} {
		t.Run(tc.name, func(t *testing.T) {
			start := time.Now()
			err := Verify(context.Background(), Options{
				Host: "smtp.example.test", Resolver: &fakeResolver{block: true}, Hostname: testHostname,
				Families:   func() (bool, bool) { return true, true },
				DNSTimeout: tc.dns, ConnectionTimeout: tc.connection,
			})
			var got *Error
			if !errors.As(err, &got) || got.Message != "DNS lookup for smtp.example.test timed out" || got.Code != "EDNS" || got.Command != "CONN" {
				t.Fatalf("got %+v", err)
			}
			if took := time.Since(start); took < 60*time.Millisecond || took > 2*time.Second {
				t.Errorf("took %v, want about 80ms", took)
			}
		})
	}
}

func refusal(code syscall.Errno) error {
	return &net.OpError{Op: "dial", Net: "tcp", Err: os.NewSyscallError("connect", code)}
}

func TestConnectErrorTexts(t *testing.T) {
	plain := errors.New("blocked by policy")
	for _, tc := range []struct {
		host string
		err  error
		want string
	}{
		{"192.0.2.1", refusal(syscall.ECONNREFUSED), "connect ECONNREFUSED 192.0.2.1:2525"},
		{"192.0.2.1", refusal(syscall.ETIMEDOUT), "connect ETIMEDOUT 192.0.2.1:2525"},
		{"192.0.2.1", refusal(syscall.EHOSTUNREACH), "connect EHOSTUNREACH 192.0.2.1:2525"},
		{"192.0.2.1", refusal(syscall.ENETUNREACH), "connect ENETUNREACH 192.0.2.1:2525"},
		{"192.0.2.1", refusal(syscall.ECONNRESET), "connect ECONNRESET 192.0.2.1:2525"},
		{"2001:db8::1", refusal(syscall.ECONNREFUSED), "connect ECONNREFUSED 2001:db8::1:2525"},
		{"192.0.2.1", plain, "blocked by policy"},
	} {
		err := Verify(context.Background(), Options{
			Host: tc.host, Port: 2525, Hostname: testHostname,
			Dial: func(ctx context.Context, network, address string) (net.Conn, error) { return nil, tc.err },
		})
		wantError(t, err, tc.want, "ESOCKET")
		if !errors.Is(err, tc.err) {
			t.Errorf("%s: the dial error is not kept as the cause", tc.want)
		}
	}
}

func TestSocketErrorTexts(t *testing.T) {
	reset := &net.OpError{Op: "read", Net: "tcp", Err: os.NewSyscallError("read", syscall.ECONNRESET)}
	pipe := &net.OpError{Op: "write", Net: "tcp", Err: os.NewSyscallError("write", syscall.EPIPE)}
	for _, tc := range []struct {
		call string
		err  error
		want string
	}{
		{"read", reset, "read ECONNRESET"},
		{"write", pipe, "write EPIPE"},
		{"write", reset, "write ECONNRESET"},
		{"read", errors.New("tls: bad record MAC"), "tls: bad record MAC"},
	} {
		if got := socketErrorText(tc.call, tc.err, ""); got != tc.want {
			t.Errorf("%s error %v: %q, want %q", tc.call, tc.err, got, tc.want)
		}
	}
}

// failingConn fails every read or write with a given error.
type failingConn struct {
	net.Conn
	readErr, writeErr error
}

func (c failingConn) Read(p []byte) (int, error) {
	if c.readErr != nil {
		return 0, c.readErr
	}
	return c.Conn.Read(p)
}

func (c failingConn) Write(p []byte) (int, error) {
	if c.writeErr != nil {
		return 0, c.writeErr
	}
	return c.Conn.Write(p)
}

func TestSocketErrors(t *testing.T) {
	reset := &net.OpError{Op: "read", Net: "tcp", Err: os.NewSyscallError("read", syscall.ECONNRESET)}
	pipe := &net.OpError{Op: "write", Net: "tcp", Err: os.NewSyscallError("write", syscall.EPIPE)}
	for _, tc := range []struct {
		name string
		conn failingConn
		want string
		code string
	}{
		{"read error", failingConn{readErr: reset}, "read ECONNRESET", "ESOCKET"},
		{"write error", failingConn{writeErr: pipe}, "write EPIPE", "ESOCKET"},
		{"write timeout", failingConn{writeErr: os.ErrDeadlineExceeded}, "Timeout", "ETIMEDOUT"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := startSink(t, acceptingScript())
			opts := sinkOptions(s)
			opts.Dial = func(ctx context.Context, network, address string) (net.Conn, error) {
				var dialer net.Dialer
				conn, err := dialer.DialContext(ctx, network, s.addr())
				if err != nil {
					return nil, err
				}
				tc.conn.Conn = conn
				return tc.conn, nil
			}
			_, err := Send(context.Background(), opts, Envelope{From: "a@example.test", To: []string{"b@example.test"}}, []byte("x"))
			wantError(t, err, tc.want, tc.code)
		})
	}
}

func TestDialSeveralAddresses(t *testing.T) {
	resolver := &fakeResolver{records: map[string][]string{"ip4": {"192.0.2.1", "192.0.2.2"}, "ip6": {"2001:db8::1"}}}
	both := func() (bool, bool) { return true, true }
	noShuffle := func(int, func(i, j int)) {}

	t.Run("the first address that answers is used", func(t *testing.T) {
		s := startSink(t, acceptingScript())
		var tried []string
		err := Verify(context.Background(), Options{
			Host: "smtp.example.test", Port: 2525, Resolver: resolver, Families: both, Shuffle: noShuffle, Hostname: testHostname,
			Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
				tried = append(tried, address)
				if address != "192.0.2.1:2525" {
					return nil, refusal(syscall.ECONNREFUSED)
				}
				var dialer net.Dialer
				return dialer.DialContext(ctx, network, s.addr())
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		if want := []string{"[2001:db8::1]:2525", "192.0.2.1:2525"}; !reflect.DeepEqual(tried, want) {
			t.Errorf("tried %q, want %q", tried, want)
		}
		if got := lines(s.wait(t, 5*time.Second)); !reflect.DeepEqual(got, []string{"EHLO client.example.test", "QUIT", "<eof>"}) {
			t.Errorf("session %q", got)
		}
	})

	// Node's error had no text here; the first address's failure is the
	// text (BUGS EM11)
	t.Run("all refused: the error is the first address's", func(t *testing.T) {
		var tried []string
		err := Verify(context.Background(), Options{
			Host: "smtp.example.test", Port: 2525, Resolver: resolver, Families: both, Shuffle: noShuffle, Hostname: testHostname,
			Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
				tried = append(tried, address)
				return nil, refusal(syscall.ECONNREFUSED)
			},
		})
		wantError(t, err, "connect ECONNREFUSED 2001:db8::1:2525", "ESOCKET")
		if want := []string{"[2001:db8::1]:2525", "192.0.2.1:2525", "192.0.2.2:2525"}; !reflect.DeepEqual(tried, want) {
			t.Errorf("tried %q, want %q", tried, want)
		}
	})

	t.Run("an address that does not answer is given up after 250ms", func(t *testing.T) {
		s := startSink(t, acceptingScript())
		start := time.Now()
		var waited time.Duration
		err := Verify(context.Background(), Options{
			Host: "smtp.example.test", Port: 2525, Resolver: resolver, Families: both, Shuffle: noShuffle, Hostname: testHostname,
			Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
				if address == "[2001:db8::1]:2525" {
					<-ctx.Done()
					waited = time.Since(start)
					return nil, ctx.Err()
				}
				var dialer net.Dialer
				return dialer.DialContext(ctx, network, s.addr())
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		if waited < 200*time.Millisecond || waited > 2*time.Second {
			t.Errorf("the first address was given %v, want 250ms", waited)
		}
	})

	t.Run("the last address has the rest of the connection timeout", func(t *testing.T) {
		start := time.Now()
		err := Verify(context.Background(), Options{
			Host: "smtp.example.test", Port: 2525, Resolver: resolver, Families: both, Shuffle: noShuffle, Hostname: testHostname,
			ConnectionTimeout: 700 * time.Millisecond,
			Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
				if address != "192.0.2.2:2525" {
					return nil, refusal(syscall.ECONNREFUSED)
				}
				<-ctx.Done()
				return nil, ctx.Err()
			},
		})
		wantError(t, err, "Connection timeout", "ETIMEDOUT")
		if took := time.Since(start); took < 600*time.Millisecond || took > 3*time.Second {
			t.Errorf("took %v, want about 700ms", took)
		}
	})
}

func TestConnectionTimeout(t *testing.T) {
	t.Run("connecting", func(t *testing.T) {
		start := time.Now()
		err := Verify(context.Background(), Options{
			Host: "192.0.2.1", Port: 2525, Hostname: testHostname, ConnectionTimeout: 100 * time.Millisecond,
			Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
				<-ctx.Done()
				return nil, ctx.Err()
			},
		})
		wantError(t, err, "Connection timeout", "ETIMEDOUT")
		if took := time.Since(start); took < 80*time.Millisecond || took > 2*time.Second {
			t.Errorf("took %v, want about 100ms", took)
		}
	})
	t.Run("TLS handshake of an implicit TLS connection", func(t *testing.T) {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer listener.Close()
		go func() {
			conn, err := listener.Accept()
			if err == nil {
				defer conn.Close()
				_, _ = io.Copy(io.Discard, conn)
			}
		}()
		host, port, _ := net.SplitHostPort(listener.Addr().String())
		opts := Options{Host: host, Secure: true, Hostname: testHostname, ConnectionTimeout: 150 * time.Millisecond}
		opts.Port = mustAtoi(t, port)
		start := time.Now()
		wantError(t, Verify(context.Background(), opts), "Connection timeout", "ETIMEDOUT")
		if took := time.Since(start); took < 120*time.Millisecond || took > 2*time.Second {
			t.Errorf("took %v, want about 150ms", took)
		}
	})
	t.Run("what is left of it limits the wait for the greeting", func(t *testing.T) {
		s := startSink(t, sinkScript{Greeting: []sinkOp{{Stall: 1}}})
		opts := sinkOptions(s)
		opts.ConnectionTimeout = 150 * time.Millisecond
		opts.GreetingTimeout = 5 * time.Second
		start := time.Now()
		wantError(t, Verify(context.Background(), opts), "Greeting never received", "ETIMEDOUT")
		if took := time.Since(start); took < 100*time.Millisecond || took > 2*time.Second {
			t.Errorf("took %v, want about 150ms", took)
		}
	})
}

func mustAtoi(t *testing.T, s string) int {
	t.Helper()
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			t.Fatalf("not a number: %q", s)
		}
		n = n*10 + int(c-'0')
	}
	return n
}

func TestContext(t *testing.T) {
	t.Run("cancelled before the call", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		err := Verify(ctx, Options{Host: "127.0.0.1", Port: 1, Hostname: testHostname})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("got %v, want context.Canceled", err)
		}
	})
	t.Run("cancelled while waiting for the greeting", func(t *testing.T) {
		s := startSink(t, sinkScript{Greeting: []sinkOp{{Stall: 1}}})
		ctx, cancel := context.WithCancel(context.Background())
		time.AfterFunc(100*time.Millisecond, cancel)
		start := time.Now()
		_, err := Send(ctx, sinkOptions(s), Envelope{From: "a@example.test", To: []string{"b@example.test"}}, []byte("x"))
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("got %v, want context.Canceled", err)
		}
		if took := time.Since(start); took > 2*time.Second {
			t.Errorf("took %v after the context was cancelled at 100ms", took)
		}
		if got := lines(s.wait(t, 5*time.Second)); !reflect.DeepEqual(got, []string{"<eof>"}) {
			t.Errorf("the connection was not closed: %q", got)
		}
	})
	t.Run("deadline while a reply is outstanding", func(t *testing.T) {
		script := acceptingScript()
		script.Rules["EHLO"] = [][]sinkOp{{{Stall: 1}}}
		s := startSink(t, script)
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		err := Verify(ctx, sinkOptions(s))
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("got %v, want context.DeadlineExceeded", err)
		}
		if got := lines(s.wait(t, 5*time.Second)); !reflect.DeepEqual(got, []string{"EHLO client.example.test", "<eof>"}) {
			t.Errorf("session %q", got)
		}
	})
	t.Run("cancelled during the STARTTLS handshake", func(t *testing.T) {
		script := acceptingScript("STARTTLS")
		script.Rules["STARTTLS"] = [][]sinkOp{{op("220 go ahead\r\n"), {Stall: 1}}}
		s := startSink(t, script)
		ctx, cancel := context.WithCancel(context.Background())
		time.AfterFunc(100*time.Millisecond, cancel)
		err := Verify(ctx, sinkOptions(s))
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("got %v, want context.Canceled", err)
		}
	})
	t.Run("cancelled while connecting", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		time.AfterFunc(50*time.Millisecond, cancel)
		err := Verify(ctx, Options{
			Host: "192.0.2.1", Port: 2525, Hostname: testHostname,
			Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
				<-ctx.Done()
				return nil, ctx.Err()
			},
		})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("got %v, want context.Canceled", err)
		}
	})
	t.Run("cancelled during the lookup", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		time.AfterFunc(50*time.Millisecond, cancel)
		err := Verify(ctx, Options{
			Host: "smtp.example.test", Resolver: &fakeResolver{block: true}, Hostname: testHostname,
			Families: func() (bool, bool) { return true, true },
		})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("got %v, want context.Canceled", err)
		}
	})
}

// TestVerifyWaitsForQuitReply checks that Verify returns once QUIT is
// written, and that the connection then stays open until the server has
// answered, whatever happens to the caller's context.
func TestVerifyWaitsForQuitReply(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	type result struct {
		openBeforeReply bool
		closedAfter     bool
		err             error
	}
	results := make(chan result, 1)
	quitSeen := make(chan struct{})
	go func() {
		var res result
		defer func() { results <- res }()
		conn, err := listener.Accept()
		if err != nil {
			res.err = err
			return
		}
		defer conn.Close()
		reader := bufio.NewReader(conn)
		_, _ = conn.Write([]byte("220 hi\r\n"))
		if _, res.err = reader.ReadString('\n'); res.err != nil {
			return
		}
		_, _ = conn.Write([]byte("250 ok\r\n"))
		if _, res.err = reader.ReadString('\n'); res.err != nil {
			return
		}
		close(quitSeen)
		// the client must still be there after a while without an answer
		_ = conn.SetReadDeadline(time.Now().Add(400 * time.Millisecond))
		_, err = reader.ReadByte()
		res.openBeforeReply = errors.Is(err, os.ErrDeadlineExceeded)
		_, _ = conn.Write([]byte("221 bye\r\n"))
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		_, err = reader.ReadByte()
		res.closedAfter = errors.Is(err, io.EOF)
	}()
	ctx, cancel := context.WithCancel(context.Background())
	host, port, _ := net.SplitHostPort(listener.Addr().String())
	start := time.Now()
	if err := Verify(ctx, Options{Host: host, Port: mustAtoi(t, port), Hostname: testHostname}); err != nil {
		t.Fatal(err)
	}
	if took := time.Since(start); took > 300*time.Millisecond {
		t.Errorf("Verify took %v, it waited for the reply to QUIT", took)
	}
	<-quitSeen
	cancel()
	res := <-results
	if res.err != nil {
		t.Fatal(res.err)
	}
	if !res.openBeforeReply {
		t.Error("the connection was closed before the server answered QUIT")
	}
	if !res.closedAfter {
		t.Error("the connection was not closed after the reply to QUIT")
	}
}

// bodyServer accepts one connection, takes the envelope and then does what
// afterData says with the connection.
func bodyServer(t *testing.T, afterData func(conn *net.TCPConn, reader *bufio.Reader)) Options {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	t.Cleanup(func() {
		listener.Close()
		<-done
	})
	go func() {
		defer close(done)
		accepted, err := listener.Accept()
		if err != nil {
			return
		}
		conn := accepted.(*net.TCPConn)
		defer conn.Close()
		_ = conn.SetReadBuffer(64 * 1024)
		reader := bufio.NewReaderSize(conn, 512)
		_, _ = conn.Write([]byte("220 hi\r\n"))
		for _, reply := range []string{"250 ok\r\n", "250 ok\r\n", "250 ok\r\n", "354 go\r\n"} {
			if _, err := reader.ReadString('\n'); err != nil {
				return
			}
			_, _ = conn.Write([]byte(reply))
		}
		afterData(conn, reader)
	}()
	host, port, _ := net.SplitHostPort(listener.Addr().String())
	return Options{
		Host: host, Port: mustAtoi(t, port), Hostname: testHostname,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			var dialer net.Dialer
			conn, err := dialer.DialContext(ctx, network, address)
			if err == nil {
				_ = conn.(*net.TCPConn).SetWriteBuffer(64 * 1024)
			}
			return conn, err
		},
	}
}

func TestMessageWriter(t *testing.T) {
	env := Envelope{From: "a@example.test", To: []string{"b@example.test"}}
	large := bytes.Repeat([]byte("a line of the message that is sent\r\n"), 400000)

	t.Run("a server that stops reading runs into the socket timeout", func(t *testing.T) {
		release := make(chan struct{})
		defer close(release)
		opts := bodyServer(t, func(conn *net.TCPConn, reader *bufio.Reader) { <-release })
		opts.SocketTimeout = 300 * time.Millisecond
		start := time.Now()
		info, err := Send(context.Background(), opts, env, large)
		wantError(t, err, "Timeout", "ETIMEDOUT")
		if info != nil {
			t.Errorf("info %+v", info)
		}
		if took := time.Since(start); took < 250*time.Millisecond || took > 5*time.Second {
			t.Errorf("took %v, want about 300ms", took)
		}
	})

	t.Run("a slow server that keeps reading is not timed out", func(t *testing.T) {
		opts := bodyServer(t, func(conn *net.TCPConn, reader *bufio.Reader) {
			// reads 16 KiB every 5ms for longer than the socket timeout,
			// then everything
			chunk := make([]byte, 64*1024)
			for i := 0; i < 300; i++ {
				if _, err := reader.Read(chunk[:16*1024]); err != nil {
					return
				}
				time.Sleep(5 * time.Millisecond)
			}
			var tail []byte
			for !bytes.Equal(tail, []byte("\r\n.\r\n")) {
				n, err := reader.Read(chunk)
				if err != nil {
					return
				}
				tail = append(tail, chunk[:n]...)
				if len(tail) > 5 {
					tail = tail[len(tail)-5:]
				}
			}
			_, _ = conn.Write([]byte("250 queued\r\n"))
		})
		opts.SocketTimeout = 500 * time.Millisecond
		start := time.Now()
		info, err := Send(context.Background(), opts, env, large)
		if took := time.Since(start); took < 1500*time.Millisecond {
			t.Errorf("took %v: the server was not slow for longer than the socket timeout", took)
		}
		if err != nil {
			t.Fatal(err)
		}
		if want := int64(len(large) + 3); info.MessageSize != want {
			t.Errorf("message size %d, want %d", info.MessageSize, want)
		}
	})

	t.Run("a reply before the message is read ends the call", func(t *testing.T) {
		release := make(chan struct{})
		defer close(release)
		opts := bodyServer(t, func(conn *net.TCPConn, reader *bufio.Reader) {
			_, _ = conn.Write([]byte("552 5.3.4 too big\r\n"))
			<-release
		})
		start := time.Now()
		_, err := Send(context.Background(), opts, env, large)
		var got *Error
		if !errors.As(err, &got) || got.Message != "Message failed: 552 5.3.4 too big" || got.Code != "EMESSAGE" || got.Command != "DATA" {
			t.Fatalf("got %+v", err)
		}
		if took := time.Since(start); took > 5*time.Second {
			t.Errorf("took %v", took)
		}
	})

	t.Run("a server that closes while the message is written", func(t *testing.T) {
		opts := bodyServer(t, func(conn *net.TCPConn, reader *bufio.Reader) {
			chunk := make([]byte, 4096)
			_, _ = reader.Read(chunk)
			_ = conn.CloseWrite()
			_, _ = io.Copy(io.Discard, reader)
		})
		_, err := Send(context.Background(), opts, env, large)
		wantError(t, err, "Connection closed unexpectedly", "ECONNECTION")
	})

	t.Run("cancelled while the message is written", func(t *testing.T) {
		release := make(chan struct{})
		defer close(release)
		opts := bodyServer(t, func(conn *net.TCPConn, reader *bufio.Reader) { <-release })
		ctx, cancel := context.WithCancel(context.Background())
		time.AfterFunc(150*time.Millisecond, cancel)
		start := time.Now()
		_, err := Send(ctx, opts, env, large)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("got %v, want context.Canceled", err)
		}
		if took := time.Since(start); took > 5*time.Second {
			t.Errorf("took %v", took)
		}
	})
}

// TestStartTLSBeforeLogin states the rule for credentials: when the reply to
// EHLO offers STARTTLS, the next command is STARTTLS and nothing else goes
// out in plain text; a refused upgrade ends the session. Without the offer
// the login goes out in plain text.
func TestStartTLSBeforeLogin(t *testing.T) {
	plainLogin := "AUTH PLAIN " + encodeBase64("\x00console@example.test\x00secret-password")
	run := func(t *testing.T, script sinkScript) ([]string, error) {
		s := startSink(t, script)
		opts := sinkOptions(s)
		opts.User, opts.Pass = "console@example.test", "secret-password"
		_, err := Send(context.Background(), opts, Envelope{From: "a@example.test", To: []string{"b@example.test"}}, []byte("the message"))
		return lines(s.wait(t, 5*time.Second)), err
	}
	for _, ehlo := range []string{
		"250-sink\r\n250-AUTH PLAIN\r\n250 STARTTLS\r\n",
		"250-sink\r\n250-STARTTLS\r\n250 AUTH PLAIN\r\n",
		"250-sink\r\n250-starttls\r\n250 AUTH PLAIN\r\n",
		"250-sink\r\n250-StartTLS required\r\n250 AUTH PLAIN\r\n",
		"250-sink\r\n250-X-STARTTLS\r\n250 AUTH PLAIN\r\n",
		"250 sink AUTH PLAIN STARTTLS\r\n",
		"250-STARTTLS\n250 AUTH PLAIN\n",
	} {
		t.Run("offered: "+ehlo, func(t *testing.T) {
			script := acceptingScript()
			script.Rules["EHLO"] = [][]sinkOp{{op(ehlo)}, {op("250-sink\r\n250 AUTH PLAIN\r\n")}}
			got, err := run(t, script)
			if err != nil {
				t.Fatal(err)
			}
			want := []string{"EHLO client.example.test", "STARTTLS", "<tls>", "EHLO client.example.test", plainLogin,
				"MAIL FROM:<a@example.test>", "RCPT TO:<b@example.test>", "DATA", "<data>", "<eof>"}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("session %q,\nwant %q", got, want)
			}
		})
		t.Run("offered and refused: "+ehlo, func(t *testing.T) {
			script := acceptingScript()
			script.Rules["EHLO"] = [][]sinkOp{{op(ehlo)}}
			script.Rules["STARTTLS"] = [][]sinkOp{{op("454 TLS not available\r\n")}}
			got, err := run(t, script)
			var failed *Error
			if !errors.As(err, &failed) || failed.Code != "ETLS" || failed.Message != "Error upgrading connection with STARTTLS: 454 TLS not available" {
				t.Fatalf("got %+v", err)
			}
			if want := []string{"EHLO client.example.test", "STARTTLS", "<eof>"}; !reflect.DeepEqual(got, want) {
				t.Errorf("session %q,\nwant %q", got, want)
			}
		})
	}
	for _, ehlo := range []string{
		"250-sink\r\n250 AUTH PLAIN\r\n",
		"250-sink\r\n250-STARTTLSX\r\n250 AUTH PLAIN\r\n",
		"250-sink\r\n250-XSTARTTLS\r\n250 AUTH PLAIN\r\n",
	} {
		t.Run("not offered: "+ehlo, func(t *testing.T) {
			script := acceptingScript()
			script.Rules["EHLO"] = [][]sinkOp{{op(ehlo)}}
			got, err := run(t, script)
			if err != nil {
				t.Fatal(err)
			}
			want := []string{"EHLO client.example.test", plainLogin, "MAIL FROM:<a@example.test>", "RCPT TO:<b@example.test>", "DATA", "<data>", "<eof>"}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("session %q,\nwant %q", got, want)
			}
		})
	}
}

// TestCertificateClock checks that certificates are judged at the time the
// injected clock gives.
func TestCertificateClock(t *testing.T) {
	roots := trustedRoots(t)
	for _, tc := range []struct {
		now  time.Time
		want string
	}{
		{time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC), ""},
		{time.Date(2130, 1, 1, 0, 0, 0, 0, time.UTC), "certificate has expired"},
		{time.Date(2010, 1, 1, 0, 0, 0, 0, time.UTC), "certificate is not yet valid"},
	} {
		script := acceptingScript()
		script.TLS, script.Cert = true, "ca-leaf"
		s := startSink(t, script)
		opts := sinkOptions(s)
		opts.Secure, opts.TLSVerify, opts.RootCAs = true, true, roots
		opts.Now = func() time.Time { return tc.now }
		err := Verify(context.Background(), opts)
		if tc.want == "" {
			if err != nil {
				t.Errorf("at %v: %v", tc.now, err)
			}
			continue
		}
		wantError(t, err, tc.want, "ESOCKET")
	}
}

// TestSystemRoots checks that without RootCAs the roots of the operating
// system decide, which do not hold the test certificate.
func TestSystemRoots(t *testing.T) {
	script := acceptingScript()
	script.TLS = true
	s := startSink(t, script)
	opts := sinkOptions(s)
	opts.Secure, opts.TLSVerify = true, true
	wantError(t, Verify(context.Background(), opts), "self-signed certificate", "ESOCKET")
}

func TestUsableFamilies(t *testing.T) {
	ip := net.ParseIP
	lo := interfaceAddrs{loopback: true, ips: []net.IP{ip("127.0.0.1"), ip("::1")}}
	for _, tc := range []struct {
		name       string
		interfaces []interfaceAddrs
		v4, v6     bool
	}{
		{"no interface at all", nil, true, true},
		{"interfaces without addresses", []interfaceAddrs{{}, {loopback: true}}, true, true},
		{"loopback only", []interfaceAddrs{lo}, false, false},
		{"IPv4 only", []interfaceAddrs{lo, {ips: []net.IP{ip("172.17.0.2")}}}, true, false},
		{"IPv6 only", []interfaceAddrs{lo, {ips: []net.IP{ip("2001:db8::2")}}}, false, true},
		{"both on one interface", []interfaceAddrs{lo, {ips: []net.IP{ip("172.17.0.2"), ip("fe80::1")}}}, true, true},
		{"both on two interfaces", []interfaceAddrs{{ips: []net.IP{ip("2001:db8::2")}}, {ips: []net.IP{ip("10.0.0.5")}}}, true, true},
	} {
		if v4, v6 := usableFamilies(tc.interfaces); v4 != tc.v4 || v6 != tc.v6 {
			t.Errorf("%s: families %v %v, want %v %v", tc.name, v4, v6, tc.v4, tc.v6)
		}
	}
	// the machine the tests run on has at least a loopback address
	if _, err := net.Interfaces(); err == nil {
		defaultFamilies()
	}
}
