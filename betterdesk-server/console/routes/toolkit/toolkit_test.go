package toolkit

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/unitronix/betterdesk-server/console/ssrfguard"
)

const signedOut = `{"success":false,"error":"Unauthorized. Please log in.","code":"session_required"}`

func TestRoutesInTheOrderOfTheOldFile(t *testing.T) {
	s := newSite(t, Options{})
	var got []string
	for _, route := range s.d.Router.Routes() {
		if strings.HasPrefix(route.Path, "/api/") {
			got = append(got, route.Method+" "+route.Path)
		}
	}
	want := []string{
		"POST /api/toolkit/ssl", "POST /api/toolkit/hash", "POST /api/toolkit/password", "POST /api/toolkit/base64",
		"POST /api/toolkit/wol", "POST /api/toolkit/whois", "POST /api/toolkit/urlencode", "POST /api/toolkit/jwt-decode",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("routes:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// unreachable is a network that fails the test when a route uses it.
func unreachable(t *testing.T) Network {
	return Network{
		Lookup: func(_ context.Context, host string) ([]string, error) {
			t.Errorf("lookup of %q", host)
			return nil, errors.New("no network in this test")
		},
		Dial: func(_ context.Context, _, address string) (net.Conn, error) {
			t.Errorf("connection to %s", address)
			return nil, errors.New("no network in this test")
		},
		Records: fakeRecords{fail: true, t: t},
		SendUDP: func(_ context.Context, _ []byte, to *net.UDPAddr) error {
			t.Errorf("packet to %s", to)
			return errors.New("no network in this test")
		},
	}
}

func TestEveryRouteNeedsASession(t *testing.T) {
	s := newSite(t, Options{Network: unreachable(t)})
	bodies := map[string]string{
		"ssl": `{"host":"example.com"}`, "hash": `{"text":"x"}`, "password": `{}`, "base64": `{"text":"x"}`,
		"wol": `{"mac":"aabbccddeeff"}`, "whois": `{"domain":"example.com"}`, "urlencode": `{"text":"x"}`, "jwt-decode": `{"token":"e30.e30"}`,
	}
	for route, body := range bodies {
		if status, answer := s.post("", "/api/toolkit/"+route, body); status != 401 || answer != signedOut {
			t.Errorf("%s signed out: %d %s", route, status, answer)
		}
	}
}

func TestWakeOnLANNeedsThePermissionToConnect(t *testing.T) {
	s := newSite(t, Options{Network: unreachable(t)})
	status, answer := s.post(s.signIn("viewer"), "/api/toolkit/wol", `{"mac":"aabbccddeeff"}`)
	if status != 403 || answer != `{"success":false,"error":"Permission denied: device.connect"}` {
		t.Errorf("viewer: %d %s", status, answer)
	}
	// The text tools ask for a session only
	if status, answer := s.post(s.signIn("viewer"), "/api/toolkit/hash", `{"text":""}`); status != 200 || !strings.Contains(answer, `"e3b0c44298fc1c14`) {
		t.Errorf("viewer hashes: %d %s", status, answer)
	}
}

// A body that is a JSON array names no length: the password gets the
// default 16 characters, not the array's length (BUGS.md SC9).
func TestPasswordOfAnArrayBodyHasTheDefaultLength(t *testing.T) {
	s := newSite(t, Options{Network: unreachable(t)})
	admin := s.signIn("admin")
	for body, want := range map[string]int{`[1,2,3,4,5,6,7,8,9]`: 16, `[]`: 16, `[1,2]`: 16, `{"length":9}`: 9} {
		status, answer := s.post(admin, "/api/toolkit/password", body)
		var got struct {
			Data struct {
				Password string `json:"password"`
				Length   int    `json:"length"`
			} `json:"data"`
		}
		if err := json.Unmarshal([]byte(answer), &got); status != 200 || err != nil || got.Data.Length != want || len(got.Data.Password) != want {
			t.Errorf("%s: %d %s, want a password of %d", body, status, answer, want)
		}
	}
}

func TestTextTools(t *testing.T) {
	s := newSite(t, Options{Network: unreachable(t), Now: func() time.Time { return oracleNow }, Random: bytes.NewReader(bytes.Repeat([]byte{255, 0, 61, 62, 200}, 100))})
	admin := s.signIn("admin")
	cases := []struct {
		route, body string
		status      int
		want        string
	}{
		{"hash", `{"text":"hello","algorithm":"md5"}`, 200, `{"success":true,"data":{"algorithm":"md5","hash":"5d41402abc4b2a76b9719d911017c592"}}`},
		{"hash", `{"text":"hello","algorithm":"whirlpool"}`, 200, `{"success":true,"data":{"algorithm":"sha256","hash":"2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"}}`},
		{"hash", `{"text":7}`, 400, `{"success":false,"error":"Text is required"}`},
		{"hash", `{"text":"` + strings.Repeat("x", maxText+1) + `"}`, 400, `{"success":false,"error":"Input too large (max 1 MB)"}`},
		// 255 and 200 are thrown away: they would favour the first letters
		{"password", `{"length":4}`, 200, `{"success":true,"data":{"password":"A9AO","length":4,"entropy":24,"charsetSize":62}}`},
		{"password", `{"length":4,"uppercase":false,"lowercase":false,"digits":false}`, 200, `{"success":true,"data":{"password":"0uaz","length":4,"entropy":21,"charsetSize":36}}`},
		{"base64", `{"text":"hi"}`, 200, `{"success":true,"data":{"result":"aGk=","mode":"encode"}}`},
		{"base64", `{"text":"aG k_*=xx","mode":"decode"}`, 200, `{"success":true,"data":{"result":"hi?","mode":"decode"}}`},
		{"urlencode", `{"text":"a b/ü"}`, 200, `{"success":true,"data":{"result":"a%20b%2F%C3%BC","mode":"encode"}}`},
		{"urlencode", `{"text":"%zz","mode":"decode"}`, 400, `{"success":false,"error":"URI malformed"}`},
		{"urlencode", `{"text":"\ud800"}`, 400, `{"success":false,"error":"URI malformed"}`},
		{"jwt-decode", `{"token":"abc"}`, 400, `{"success":false,"error":"Invalid JWT format"}`},
		{"jwt-decode", `{"token":""}`, 400, `{"success":false,"error":"Token is required"}`},
		{"jwt-decode", `{"token":"` + jwt(`{"alg":"none"}`, `{"exp":1794312001}`) + `"}`, 200, `{"success":true,"data":{"header":{"alg":"none"},"payload":{"exp":1794312001},"expiry":{"date":"2026-11-10T12:00:01.000Z","expired":false,"secondsLeft":1},"hasSig":false}}`},
		{"jwt-decode", `{"token":"` + jwt(`{}`, `{"exp":1794311999.5}`) + `.s"}`, 200, `{"success":true,"data":{"header":{},"payload":{"exp":1794311999.5},"expiry":{"date":"2026-11-10T11:59:59.500Z","expired":true,"secondsLeft":-1},"hasSig":true}}`},
		{"jwt-decode", `{"token":"` + jwt(`{}`, `null`) + `"}`, 400, `{"success":false,"error":"Failed to decode JWT: Cannot read properties of null (reading 'exp')"}`},
		{"jwt-decode", `{"token":"` + jwt(`{}`, `{"exp":{"toString":1}}`) + `"}`, 400, `{"success":false,"error":"Failed to decode JWT: Cannot convert object to primitive value"}`},
		{"jwt-decode", `{"token":"` + jwt(`{`, `{}`) + `"}`, 400, `{"success":false,"error":"Failed to decode JWT: Expected property name or '}' in JSON at position 1 (line 1 column 2)"}`},
	}
	for _, c := range cases {
		if status, answer := s.post(admin, "/api/toolkit/"+c.route, c.body); status != c.status || answer != c.want {
			t.Errorf("%s %s\n got  %d %s\n want %d %s", c.route, clip(c.body), status, answer, c.status, c.want)
		}
	}
}

func jwt(parts ...string) string {
	for i, part := range parts {
		parts[i] = base64.RawURLEncoding.EncodeToString([]byte(part))
	}
	return strings.Join(parts, ".")
}

// resolving is a lookup that knows the names of a test.
func resolving(names map[string][]string) ssrfguard.LookupFunc {
	return func(_ context.Context, host string) ([]string, error) {
		if addresses, ok := names[host]; ok {
			return addresses, nil
		}
		return nil, &ssrfguard.LookupError{Code: "ENOTFOUND", Host: host}
	}
}

// dialTo is a dialer that takes every connection to one local port.
func dialTo(port int) func(ctx context.Context, network, address string) (net.Conn, error) {
	return func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, fmt.Sprintf("127.0.0.1:%d", port))
	}
}

func TestSSLDescribesTheCertificateOfAName(t *testing.T) {
	chain := newLiveChain(t)
	port := chain.serve(t, &tls.Config{MaxVersion: tls.VersionTLS12, CipherSuites: []uint16{tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256}})
	var asked []string
	lookups := 0
	network := Network{
		Lookup: func(ctx context.Context, host string) ([]string, error) {
			lookups++
			return resolving(map[string][]string{"live.test": {"93.184.216.34"}})(ctx, host)
		},
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			asked = append(asked, address)
			return dialTo(port)(ctx, network, address)
		},
		Roots: chain.roots(),
	}
	s := newSite(t, Options{Network: network, Now: func() time.Time { return oracleNow }})
	status, answer := s.post(s.signIn("admin"), "/api/toolkit/ssl", `{"host":"https://live.test:8443/login","port":"8443"}`)
	if status != 200 {
		t.Fatalf("%d %s", status, answer)
	}
	var got struct {
		Success bool
		Data    sslData
	}
	if err := json.Unmarshal([]byte(answer), &got); err != nil {
		t.Fatal(err)
	}
	d := got.Data
	if !got.Success || d.Subject != "live.test" || d.Issuer != "Live Test Intermediate" || d.Organization != "BetterDesk Test" || d.Serial != "F1A2B3" || d.DaysLeft != 3338 || strings.Join(d.AltNames, " ") != "live.test *.live.test" {
		t.Errorf("certificate: %s", answer)
	}
	if !d.Authorized {
		t.Errorf("the name is in the certificate and the root is trusted, but authorized is false: %s", answer)
	}
	if d.Protocol != "TLSv1.2" || d.Cipher != (cipherInfo{"ECDHE-ECDSA-AES128-GCM-SHA256", "TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256", "TLSv1.2"}) {
		t.Errorf("protocol %q, cipher %+v", d.Protocol, d.Cipher)
	}
	// The root is not sent: it comes from the trusted ones
	if len(d.Chain) != 3 || d.Chain[2].Subject != "Live Test Root" || d.Chain[2].Serial != "0A0B0C" {
		t.Errorf("chain: %+v", d.Chain)
	}
	// The guard resolves the name, and the connection goes to the address
	// it checked
	if lookups != 1 || len(asked) != 1 || asked[0] != "93.184.216.34:8443" {
		t.Errorf("%d lookups, connections to %q", lookups, asked)
	}
}

// A name that answers a public address to the guard and loopback to the
// next lookup does not lead the certificate check to loopback: the check
// connects to the address the guard checked (BUGS.md SC4).
func TestSSLConnectsToTheAddressItChecked(t *testing.T) {
	chain := newLiveChain(t)
	port := chain.serve(t, &tls.Config{})
	var asked []string
	lookups := 0
	s := newSite(t, Options{Now: func() time.Time { return oracleNow }, Network: Network{
		Lookup: func(context.Context, string) ([]string, error) {
			lookups++
			if lookups == 1 {
				return []string{"93.184.216.34"}, nil
			}
			return []string{"127.0.0.1"}, nil
		},
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			asked = append(asked, address)
			return dialTo(port)(ctx, network, address)
		},
		Roots: chain.roots(),
	}})
	status, answer := s.post(s.signIn("admin"), "/api/toolkit/ssl", `{"host":"live.test"}`)
	if status != 200 || !strings.Contains(answer, `"subject":"live.test"`) {
		t.Errorf("%d %s", status, answer)
	}
	if lookups != 1 || len(asked) != 1 || asked[0] != "93.184.216.34:443" {
		t.Errorf("%d lookups, connections to %q, want one lookup and 93.184.216.34:443", lookups, asked)
	}
}

// An IPv6 address reaches the guard whole, in brackets with or without a
// port, or bare (BUGS.md SC7).
func TestSSLOfAnIPv6Address(t *testing.T) {
	var asked []string
	s := newSite(t, Options{Network: Network{
		Lookup: func(_ context.Context, host string) ([]string, error) {
			t.Errorf("lookup of %q", host)
			return nil, &ssrfguard.LookupError{Code: "ENOTFOUND", Host: host}
		},
		Dial: func(_ context.Context, _, address string) (net.Conn, error) {
			asked = append(asked, address)
			return nil, syscall.ECONNREFUSED
		},
	}})
	admin := s.signIn("admin")
	for body, want := range map[string]string{
		`{"host":"[2001:db8::1]"}`:                            "[2001:db8::1]:443",
		`{"host":"[2001:DB8::1]:8443","port":8443}`:           "[2001:db8::1]:8443",
		`{"host":"https://[2001:db8::1]:9443/x","port":9443}`: "[2001:db8::1]:9443",
		`{"host":" 2001:db8::1 "}`:                            "[2001:db8::1]:443",
	} {
		asked = nil
		status, answer := s.post(admin, "/api/toolkit/ssl", body)
		if status != 200 || !strings.HasPrefix(answer, `{"success":false,"error":"connect ECONNREFUSED 2001:`) || len(asked) != 1 || asked[0] != want {
			t.Errorf("%s: %d %s, connections %q, want one to %s", body, status, answer, asked, want)
		}
	}
	for body, want := range map[string]string{
		`{"host":"[::1]:443"}`:           "Blocked IP address: ::1",
		`{"host":"[::ffff:127.0.0.1]"}`:  "Blocked IP address: ::ffff:127.0.0.1",
		`{"host":"https://[fe80::1]/x"}`: "Blocked IP address: fe80::1",
	} {
		if status, answer := s.post(admin, "/api/toolkit/ssl", body); status != 400 || answer != `{"success":false,"error":"`+want+`"}` {
			t.Errorf("%s: %d %s, want 400 %s", body, status, answer, want)
		}
	}
}

func TestSSLRefusals(t *testing.T) {
	s := newSite(t, Options{Network: Network{
		Lookup: resolving(map[string][]string{"inside.test": {"127.0.0.1"}, "rebind.test": {"8.8.8.8", "169.254.169.254"}, "lan.test": {"10.0.0.9"}}),
		Dial: func(context.Context, string, string) (net.Conn, error) {
			t.Error("a refused request reached the network")
			return nil, errors.New("no")
		},
	}})
	admin := s.signIn("admin")
	cases := []struct{ body, want string }{
		{`{}`, "Host is required"},
		{`{"host":""}`, "Host is required"},
		{`{"host":443}`, "Host is required"},
		{`{"host":"http://"}`, "Invalid hostname"},
		{`{"host":"a b"}`, "Invalid hostname"},
		{`{"host":"a` + " " + `b"}`, "Invalid hostname"},
		{`{"host":"a|b"}`, "Invalid hostname"},
		{`{"host":"a^b"}`, "Invalid hostname"},
		{"{\"host\":\"a`b\"}", "Invalid hostname"},
		{`{"host":"a\\b"}`, "Invalid hostname"},
		{`{"host":"` + strings.Repeat("a", 254) + `"}`, "Invalid hostname"},
		{`{"host":"LocalHost"}`, "Blocked hostname: localhost"},
		{`{"host":"127.0.0.1:8443"}`, "Blocked IP address: 127.0.0.1"},
		{`{"host":"169.254.169.254"}`, "Blocked IP address: 169.254.169.254"},
		{`{"host":"inside.test"}`, "Hostname inside.test resolves to blocked address 127.0.0.1"},
		{`{"host":"rebind.test"}`, "Hostname rebind.test resolves to blocked address 169.254.169.254"},
		{`{"host":"gone.test"}`, "DNS lookup failed for gone.test: getaddrinfo ENOTFOUND gone.test"},
		{`{"host":"lan.test","port":65536}`, "Invalid port"},
		{`{"host":"lan.test","port":-1}`, "Invalid port"},
		{`{"host":"10.0.0.9","port":"70000 or so"}`, "Invalid port"},
	}
	for _, c := range cases {
		if status, answer := s.post(admin, "/api/toolkit/ssl", c.body); status != 400 || answer != `{"success":false,"error":"`+c.want+`"}` {
			t.Errorf("%s: %d %s, want 400 %s", clip(c.body), status, answer, c.want)
		}
	}
}

func TestSSLPort(t *testing.T) {
	var asked []string
	s := newSite(t, Options{Network: Network{
		Lookup: resolving(nil),
		Dial: func(_ context.Context, _, address string) (net.Conn, error) {
			asked = append(asked, address)
			return nil, syscall.ECONNREFUSED
		},
	}})
	admin := s.signIn("admin")
	for body, port := range map[string]string{
		`{"host":"10.0.0.9"}`: "443", `{"host":"10.0.0.9","port":0}`: "443", `{"host":"10.0.0.9","port":"x"}`: "443", `{"host":"10.0.0.9","port":null}`: "443",
		`{"host":"10.0.0.9","port":8443.9}`: "8443", `{"host":"10.0.0.9","port":" 22ssh"}`: "22", `{"host":"10.0.0.9","port":[993,1]}`: "993",
		`{"host":"10.0.0.9","port":5e-7}`: "5", `{"host":"10.0.0.9","port":true}`: "443", `{"host":"10.0.0.9","port":{"a":1}}`: "443",
	} {
		asked = nil
		status, answer := s.post(admin, "/api/toolkit/ssl", body)
		want := `{"success":false,"error":"connect ECONNREFUSED 10.0.0.9:` + port + `"}`
		if status != 200 || answer != want || len(asked) != 1 || asked[0] != "10.0.0.9:"+port {
			t.Errorf("%s: %d %s, connections %q, want %s", body, status, answer, asked, want)
		}
	}
}

func TestSSLConnectionFailures(t *testing.T) {
	closing, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer closing.Close()
	go func() {
		for {
			conn, err := closing.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()
	silent, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer silent.Close()

	var target string
	var refuse error
	s := newSite(t, Options{SSLTimeout: 300 * time.Millisecond, Network: Network{
		Lookup: resolving(map[string][]string{"one.test": {"93.184.216.34"}, "two.test": {"93.184.216.34", "2606:4700::1111"}}),
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			if refuse != nil {
				return nil, refuse
			}
			return (&net.Dialer{}).DialContext(ctx, network, target)
		},
	}})
	admin := s.signIn("admin")
	check := func(name, body, want string) {
		t.Helper()
		if status, answer := s.post(admin, "/api/toolkit/ssl", body); status != 200 || answer != `{"success":false,"error":"`+want+`"}` {
			t.Errorf("%s: %d %s, want %s", name, status, answer, want)
		}
	}
	refuse = syscall.ECONNREFUSED
	check("one address refuses", `{"host":"one.test"}`, "connect ECONNREFUSED 93.184.216.34:443")
	check("every address refuses", `{"host":"two.test"}`, "")
	refuse = syscall.EHOSTUNREACH
	check("no route", `{"host":"one.test","port":9}`, "connect EHOSTUNREACH 93.184.216.34:9")
	refuse = nil
	target = closing.Addr().String()
	check("the server hangs up", `{"host":"one.test"}`, "Client network socket disconnected before secure TLS connection was established")
	target = silent.Addr().String()
	check("the server says nothing", `{"host":"one.test"}`, "Connection timed out")
}

// The broadcast address of a magic packet goes through the guard for the
// network tools: loopback, link-local and 0.0.0.0 are refused before a
// packet leaves (BUGS.md SC8).
func TestWakeOnLANRefusesLoopback(t *testing.T) {
	s := newSite(t, Options{Network: unreachable(t)})
	admin := s.signIn("admin")
	for _, broadcast := range []string{"127.0.0.1", "127.255.255.255", "169.254.255.255", "0.0.0.0"} {
		status, answer := s.post(admin, "/api/toolkit/wol", `{"mac":"aabbccddee0f","broadcast":"`+broadcast+`"}`)
		if want := `{"success":false,"error":"Blocked IP address: ` + broadcast + `"}`; status != 400 || answer != want {
			t.Errorf("%s: %d %s, want 400 %s", broadcast, status, answer, want)
		}
	}
}

func TestWakeOnLAN(t *testing.T) {
	type sent struct {
		packet []byte
		to     string
	}
	var packets []sent
	var sendErr error
	s := newSite(t, Options{Network: Network{
		Lookup: resolving(nil),
		SendUDP: func(_ context.Context, packet []byte, to *net.UDPAddr) error {
			packets = append(packets, sent{append([]byte(nil), packet...), to.String()})
			return sendErr
		},
	}})
	admin := s.signIn("admin")
	magic := append(bytes.Repeat([]byte{0xFF}, 6), bytes.Repeat([]byte{0xAA, 0xBB, 0xCC, 0xDD, 0xEE, 0x0F}, 16)...)

	cases := []struct {
		body   string
		status int
		want   string
		to     string
	}{
		{`{"mac":"aa:bb:cc:dd:ee:0f"}`, 200, `{"success":true,"data":{"mac":"AA:BB:CC:DD:EE:0F","broadcast":"255.255.255.255"}}`, "255.255.255.255:9"},
		{`{"mac":"AA-BB-CC-DD-EE-0F","broadcast":"192.168.1.255"}`, 200, `{"success":true,"data":{"mac":"AA:BB:CC:DD:EE:0F","broadcast":"192.168.1.255"}}`, "192.168.1.255:9"},
		{`{"mac":"aabbccddee0f","broadcast":""}`, 200, `{"success":true,"data":{"mac":"AA:BB:CC:DD:EE:0F","broadcast":"255.255.255.255"}}`, "255.255.255.255:9"},
		{`{"mac":"aabbccddee0f","broadcast":7}`, 200, `{"success":true,"data":{"mac":"AA:BB:CC:DD:EE:0F","broadcast":"255.255.255.255"}}`, "255.255.255.255:9"},
		// A broadcast address is an IPv4 address the guard lets through: no
		// name, no IPv6 address, no loopback (SC8)
		{`{"mac":"aabbccddee0f","broadcast":"office.test"}`, 400, `{"success":false,"error":"Invalid broadcast address"}`, ""},
		{`{"mac":"aabbccddee0f","broadcast":"127.0.0.1"}`, 400, `{"success":false,"error":"Blocked IP address: 127.0.0.1"}`, ""},
		{`{"mac":"aabbccddee0f","broadcast":"::1"}`, 400, `{"success":false,"error":"Invalid broadcast address"}`, ""},
		{`{"mac":"aabbccddee0f","broadcast":"ff02::1"}`, 400, `{"success":false,"error":"Invalid broadcast address"}`, ""},
		{`{"mac":"aabbccddee0f","broadcast":"127.1"}`, 400, `{"success":false,"error":"Invalid broadcast address"}`, ""},
		{`{"mac":"aabbccddee0f","broadcast":"10.255.255.255"}`, 200, `{"success":true,"data":{"mac":"AA:BB:CC:DD:EE:0F","broadcast":"10.255.255.255"}}`, "10.255.255.255:9"},
		{`{}`, 400, `{"success":false,"error":"MAC address is required"}`, ""},
		{`{"mac":""}`, 400, `{"success":false,"error":"MAC address is required"}`, ""},
		{`{"mac":112233445566}`, 400, `{"success":false,"error":"MAC address is required"}`, ""},
		{`{"mac":"aa:bb:cc:dd:ee"}`, 400, `{"success":false,"error":"Invalid MAC address format"}`, ""},
		{`{"mac":"aa:bb:cc:dd:ee:0f:11"}`, 400, `{"success":false,"error":"Invalid MAC address format"}`, ""},
		{`{"mac":"aa:bb:cc:dd:ee:0g"}`, 400, `{"success":false,"error":"Invalid MAC address format"}`, ""},
		{`{"mac":"aabb.ccdd.ee0f"}`, 400, `{"success":false,"error":"Invalid MAC address format"}`, ""},
	}
	for _, c := range cases {
		packets = nil
		status, answer := s.post(admin, "/api/toolkit/wol", c.body)
		if status != c.status || answer != c.want {
			t.Errorf("%s: %d %s, want %d %s", c.body, status, answer, c.status, c.want)
		}
		switch {
		case c.to == "" && len(packets) != 0:
			t.Errorf("%s: a packet went to %s", c.body, packets[0].to)
		case c.to != "" && (len(packets) != 1 || packets[0].to != c.to || !bytes.Equal(packets[0].packet, magic)):
			t.Errorf("%s: packets %+v, want the magic packet to %s", c.body, packets, c.to)
		}
	}
	sendErr = syscall.ENETUNREACH
	if status, answer := s.post(admin, "/api/toolkit/wol", `{"mac":"aabbccddee0f","broadcast":"10.255.255.255"}`); status != 200 || answer != `{"success":false,"error":"send ENETUNREACH 10.255.255.255:9"}` {
		t.Errorf("unreachable network: %d %s", status, answer)
	}
}

func TestSendUDPSendsToALocalSocket(t *testing.T) {
	listener, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if err := sendUDP(context.Background(), []byte("magic"), listener.LocalAddr().(*net.UDPAddr)); err != nil {
		t.Fatal(err)
	}
	_ = listener.SetReadDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, 16)
	n, _, err := listener.ReadFrom(buf)
	if err != nil || string(buf[:n]) != "magic" {
		t.Errorf("received %q, %v", buf[:n], err)
	}
}

// fakeRecords answers record lookups from fixed data.
type fakeRecords struct {
	fail  bool
	t     *testing.T
	asked *[]string
}

// askedMu guards the notes of fakeRecords: a request runs its lookups at
// the same time.
var askedMu sync.Mutex

func (f fakeRecords) note(kind, name string) error {
	if f.t != nil {
		f.t.Errorf("%s lookup of %q", kind, name)
	}
	if f.asked != nil {
		askedMu.Lock()
		*f.asked = append(*f.asked, kind+" "+name)
		askedMu.Unlock()
	}
	if f.fail {
		return errors.New("no such host")
	}
	return nil
}

func (f fakeRecords) LookupIP(_ context.Context, network, host string) ([]net.IP, error) {
	if err := f.note(network, host); err != nil {
		return nil, err
	}
	if network == "ip6" {
		return []net.IP{net.ParseIP("2606:2800:220:1::1")}, nil
	}
	return []net.IP{net.ParseIP("93.184.216.34"), net.ParseIP("93.184.216.35")}, nil
}

func (f fakeRecords) LookupMX(_ context.Context, name string) ([]*net.MX, error) {
	if err := f.note("mx", name); err != nil {
		return nil, err
	}
	return []*net.MX{{Host: "mx1.example.com.", Pref: 10}, {Host: "mx2.example.com", Pref: 20}}, nil
}

func (f fakeRecords) LookupTXT(_ context.Context, name string) ([]string, error) {
	if err := f.note("txt", name); err != nil {
		return nil, err
	}
	return []string{"v=spf1 -all", "note=\"quoted\"  "}, nil
}

func (f fakeRecords) LookupNS(_ context.Context, name string) ([]*net.NS, error) {
	if err := f.note("ns", name); err != nil {
		return nil, err
	}
	return []*net.NS{{Host: "a.iana-servers.net."}}, nil
}

func TestWhois(t *testing.T) {
	var asked []string
	public := resolving(map[string][]string{"example.com": {"93.184.216.34"}, "inside.test": {"127.0.0.1"}, "kernel.org": {"1.1.1.1"}})
	s := newSite(t, Options{Network: Network{Lookup: public, Records: fakeRecords{asked: &asked}}})
	admin := s.signIn("admin")

	status, answer := s.post(admin, "/api/toolkit/whois", `{"domain":"https://Example.COM:443/about"}`)
	want := `{"success":true,"data":{"domain":"example.com","dns":{"A":["93.184.216.34","93.184.216.35"],"AAAA":["2606:2800:220:1::1"],"MX":[{"priority":10,"exchange":"mx1.example.com"},{"priority":20,"exchange":"mx2.example.com"}],"TXT":["v=spf1 -all","note=\"quoted\" ` + " " + `"],"NS":["a.iana-servers.net"]}}}`
	if status != 200 || answer != want {
		t.Errorf("records:\n got  %d %s\n want 200 %s", status, answer, want)
	}
	if len(asked) != 5 {
		t.Errorf("lookups: %q", asked)
	}
	// The Kelvin sign lowers to k
	if status, answer := s.post(admin, "/api/toolkit/whois", `{"domain":"`+"K"+`ernel.org"}`); status != 200 || !strings.Contains(answer, `"domain":"kernel.org"`) {
		t.Errorf("Kelvin sign: %d %s", status, answer)
	}

	// An address has no address records, and the other kinds are asked
	asked = nil
	status, answer = s.post(admin, "/api/toolkit/whois", `{"domain":"10.0.0.1"}`)
	if status != 200 || !strings.Contains(answer, `"A":[],"AAAA":[],"MX":[{`) || len(asked) != 3 {
		t.Errorf("address: %d %s, lookups %q", status, answer, asked)
	}

	failing := newSite(t, Options{Network: Network{Lookup: public, Records: fakeRecords{fail: true}}})
	status, answer = failing.post(failing.signIn("admin"), "/api/toolkit/whois", `{"domain":"example.com"}`)
	if status != 200 || answer != `{"success":true,"data":{"domain":"example.com","dns":{"A":[],"AAAA":[],"MX":[],"TXT":[],"NS":[]}}}` {
		t.Errorf("no records: %d %s", status, answer)
	}

	refusing := newSite(t, Options{Network: Network{Lookup: public, Records: fakeRecords{t: t}}})
	admin = refusing.signIn("admin")
	for body, want := range map[string]string{
		`{}`: "Domain is required", `{"domain":""}`: "Domain is required", `{"domain":["example.com"]}`: "Domain is required",
		`{"domain":"a"}`: "Invalid domain", `{"domain":"-example.com"}`: "Invalid domain", `{"domain":"example.com-"}`: "Invalid domain",
		`{"domain":"exa mple.com"}`: "Invalid domain", `{"domain":"exämple.com"}`: "Invalid domain", `{"domain":"` + "İ" + `stanbul.com"}`: "Invalid domain",
		`{"domain":"` + strings.Repeat("a", 254) + `"}`: "Invalid domain", `{"domain":"http://"}`: "Invalid domain",
		`{"domain":"LOCALHOST"}`: "Blocked hostname: localhost", `{"domain":"127.0.0.1"}`: "Blocked IP address: 127.0.0.1",
		`{"domain":"metadata.google.internal"}`: "Blocked hostname: metadata.google.internal",
		`{"domain":"inside.test"}`:              "Hostname inside.test resolves to blocked address 127.0.0.1",
		`{"domain":"gone.test"}`:                "DNS lookup failed for gone.test: getaddrinfo ENOTFOUND gone.test",
	} {
		if status, answer := refusing.post(admin, "/api/toolkit/whois", body); status != 400 || answer != `{"success":false,"error":"`+want+`"}` {
			t.Errorf("%s: %d %s, want 400 %s", clip(body), status, answer, want)
		}
	}
}

func TestAltNamesAndChainsOfTheTestCertificates(t *testing.T) {
	full := loadCert(t, "leaf-full")
	names := altNames(full)
	if len(names) != 14 || names[0] != "tls.test" || names[4] != "IP Address:2001:DB8:0:0:0:0:0:1" || names[7] != `"odd`+"\\"+`u002cname"` || names[10] != "DNS:inner" || names[11] != "URI:http://8/x" {
		t.Errorf("alt names: %q", names)
	}
	if got := altNames(loadCert(t, "leaf-selfsigned")); got == nil || len(got) != 0 {
		t.Errorf("a certificate without names: %#v, want an empty list", got)
	}
	inter, root, stranger := loadCert(t, "inter"), loadCert(t, "root"), loadCert(t, "leaf-selfsigned")
	chain, closed := issuerChain([]*x509.Certificate{full, inter, root}, rootsOf(t, false))
	if len(chain) != 3 || !closed {
		t.Errorf("in order: %d certificates, closed %v", len(chain), closed)
	}
	// The issuer found last in the list ends the search
	chain, closed = issuerChain([]*x509.Certificate{full, stranger, inter, root}[:3], rootsOf(t, false))
	if len(chain) != 2 || closed {
		t.Errorf("issuer at the end of the list: %d certificates, closed %v", len(chain), closed)
	}
	chain, closed = issuerChain([]*x509.Certificate{full, root, inter}, rootsOf(t, true))
	if len(chain) != 3 || !closed || chain[2].Subject.CommonName != "Toolkit Test Root" {
		t.Errorf("out of order with a trusted root: %d certificates, closed %v", len(chain), closed)
	}
	if issued(loadCert(t, "other-root"), full) || !issued(inter, full) || issued(loadCert(t, "leaf-nocertsign"), loadCert(t, "leaf-nocertsign")) {
		t.Error("issued: wrong answer for the test certificates")
	}
}

// FuzzTextTools feeds text to the decoders of the text tools. None may
// panic, and each must agree with the encoder of its format.
func FuzzTextTools(f *testing.F) {
	for _, seed := range []string{"", "aGVsbG8=", "%E2%82%AC", "100%", `{"a":[1,2,{"b":null}],"10":1e21}`, "😀", "a\xed\xa0\x80b", "=", "%ED%A0%80", `"\ud800"`, "-0", "1e400", "{\"a\":1,\"a\":2}"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, text string) {
		// base64: what the standard encoder writes comes back
		encoded := base64.StdEncoding.EncodeToString([]byte(text))
		if got := decodeBase64(units(encoded)); !bytes.Equal(got, []byte(text)) {
			t.Fatalf("base64 of %q decodes to %q", text, got)
		}
		raw := decodeBase64(units(text))
		if len(raw) > len(text)*3/4+2 {
			t.Fatalf("%d bytes out of %d characters of base64", len(raw), len(text))
		}
		if decoded := decodeUTF8(raw); !utf8.ValidString(decoded) {
			t.Fatalf("decodeUTF8(%x) is not UTF-8", raw)
		}

		// URL encoding: a text without lone halves encodes and comes back
		if utf8.ValidString(text) {
			escaped, ok := encodeURIComponent(text)
			if !ok {
				t.Fatalf("encodeURIComponent(%q) refuses valid text", text)
			}
			back, ok := decodeURIComponent(escaped)
			if !ok || back != text {
				t.Fatalf("decodeURIComponent(%q) = %q, %v, want %q", escaped, back, ok, text)
			}
		}
		if decoded, ok := decodeURIComponent(text); ok && utf8.ValidString(text) && !utf8.ValidString(decoded) {
			t.Fatalf("decodeURIComponent(%q) = %q, not UTF-8", text, decoded)
		}

		// JSON: valid means the same as for encoding/json, and what is
		// written out reads back as the same value
		if !utf8.ValidString(text) || len(text) > 4096 {
			return
		}
		value, err := parseJSON(text)
		if valid := json.Valid([]byte(text)); (err == nil) != valid {
			t.Fatalf("parseJSON(%q): %v, encoding/json says valid=%v", text, err, valid)
		}
		if err != nil {
			return
		}
		var b strings.Builder
		writeJSON(&b, value)
		if !json.Valid([]byte(b.String())) {
			t.Fatalf("parseJSON(%q) writes %q, which is no JSON", text, b.String())
		}
		again, err := parseJSON(b.String())
		if err != nil {
			t.Fatalf("parseJSON(%q) writes %q, which does not parse: %v", text, b.String(), err)
		}
		var c strings.Builder
		writeJSON(&c, again)
		if b.String() != c.String() {
			t.Fatalf("parseJSON(%q) writes %q, then %q", text, b.String(), c.String())
		}
		_, _ = toNumber(value)
	})
}
