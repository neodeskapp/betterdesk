package discovery

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// lanRecording is what testdata/nodeoracle/lan.js wrote.
type lanRecording struct {
	Announcements []struct {
		Env        map[string]string `json:"env"`
		Key        *string           `json:"key"`
		Hostname   string            `json:"hostname"`
		Interfaces []struct {
			Name    string `json:"name"`
			Entries []struct {
				Family   string `json:"family"`
				Address  string `json:"address"`
				Internal bool   `json:"internal"`
				MAC      string `json:"mac"`
			} `json:"entries"`
		} `json:"interfaces"`
		Config struct {
			Port          float64 `json:"port"`
			APIPort       float64 `json:"apiPort"`
			HTTPSEnabled  bool    `json:"httpsEnabled"`
			Host          string  `json:"host"`
			AppVersion    string  `json:"appVersion"`
			DiscoveryPort float64 `json:"discoveryPort"`
		} `json:"config"`
		Reply string `json:"reply"`
	} `json:"announcements"`
	KeyOrders []struct {
		Names []string `json:"names"`
		Keys  []string `json:"keys"`
	} `json:"keyOrders"`
	Probes []struct {
		Probe    string `json:"probe"`
		Answered bool   `json:"answered"`
	} `json:"probes"`
	Exchange struct {
		Reply    string `json:"reply"`
		Expected string `json:"expected"`
	} `json:"exchange"`
	Ports []struct {
		Value string          `json:"value"`
		Port  json.RawMessage `json:"port"`
		Bound float64         `json:"bound"`
		Logs  []string        `json:"logs"`
	} `json:"ports"`
}

var (
	lanOnce sync.Once
	lanRec  lanRecording
	lanErr  error
)

func loadLAN(t testing.TB) *lanRecording {
	t.Helper()
	lanOnce.Do(func() {
		f, err := os.Open(filepath.Join("testdata", "lan.json.gz"))
		if err != nil {
			lanErr = err
			return
		}
		defer f.Close()
		zr, err := gzip.NewReader(f)
		if err != nil {
			lanErr = err
			return
		}
		raw, err := io.ReadAll(zr)
		if err != nil {
			lanErr = err
			return
		}
		lanErr = json.Unmarshal(raw, &lanRec)
	})
	if lanErr != nil {
		t.Fatal(lanErr)
	}
	return &lanRec
}

func decode64(t testing.TB, s string) []byte {
	t.Helper()
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestAnnouncementsAgainstNode builds the announcement for every recorded
// setting, host name, interface list and key file: it must be Node's
// reply byte for byte.
func TestAnnouncementsAgainstNode(t *testing.T) {
	rec := loadLAN(t)
	if len(rec.Announcements) < 100 {
		t.Fatalf("recording has %d announcements", len(rec.Announcements))
	}
	dir := t.TempDir()
	silenced, moved := 0, 0
	for i, c := range rec.Announcements {
		if host := c.Env["HOST"]; c.Env["PANEL_PUBLIC_URL"] == "" && c.Env["PANEL_PUBLIC_HOST"] == "" && (host == "0.0.0.0" || host == "::") {
			moved++
		}
		keyPath := filepath.Join(dir, fmt.Sprintf("key-%d.pub", i))
		switch {
		case c.Key == nil:
		case *c.Key == "dir":
			if err := os.Mkdir(keyPath, 0o755); err != nil {
				t.Fatal(err)
			}
		default:
			if err := os.WriteFile(keyPath, decode64(t, *c.Key), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		var ifaces []Interface
		for _, iface := range c.Interfaces {
			entry := Interface{Name: iface.Name}
			for _, e := range iface.Entries {
				entry.Entries = append(entry.Entries, Address{Family: e.Family, Address: e.Address, Internal: e.Internal, MAC: e.MAC})
			}
			ifaces = append(ifaces, entry)
		}
		hostname := c.Hostname
		settings := FromEnv(func(key string) string { return c.Env[key] }, Settings{
			Hostname:   func() (string, error) { return hostname, nil },
			Interfaces: func() ([]Interface, error) { return ifaces, nil },
			PubKeyPath: keyPath,
			Port:       c.Config.Port,
			APIPort:    c.Config.APIPort,
			HTTPS:      c.Config.HTTPSEnabled,
			Version:    c.Config.AppVersion,
		})
		if settings.Host != c.Config.Host {
			t.Errorf("case %d: host %q, Node %q", i, settings.Host, c.Config.Host)
		}
		if settings.DiscoveryPort != c.Config.DiscoveryPort {
			t.Errorf("case %d: discovery port %v, Node %v", i, settings.DiscoveryPort, c.Config.DiscoveryPort)
		}
		got, err := settings.Announcement()
		want, silent := announcedAs(t, c.Env, decode64(t, c.Reply))
		if silent {
			silenced++
			if !errors.Is(err, ErrNoAddress) {
				t.Errorf("case %d (env %v): announced %s, want nothing", i, c.Env, got)
			}
			continue
		}
		if err != nil {
			t.Fatalf("case %d: %v", i, err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("case %d (env %v):\n got %s\nwant %s", i, c.Env, got, want)
		}
	}
	if silenced == 0 || moved == 0 {
		t.Errorf("%d announcements silenced, %d moved to a network address", silenced, moved)
	}
}

// announcedAs is Node's announcement as Go sends it: nothing (silent) for
// a console on loopback with no public address, and for one that listens
// on every address the first of the announced addresses in panelUrl, not
// 0.0.0.0 or ::; nothing when there is none (BUGS JB2).
func announcedAs(t *testing.T, env map[string]string, reply []byte) ([]byte, bool) {
	t.Helper()
	if env["PANEL_PUBLIC_URL"] != "" || env["PANEL_PUBLIC_HOST"] != "" {
		return reply, false
	}
	var node struct {
		Server struct {
			Addresses []string `json:"addresses"`
			PanelURL  string   `json:"panelUrl"`
		} `json:"server"`
	}
	if err := json.Unmarshal(reply, &node); err != nil {
		t.Fatal(err)
	}
	host := strings.TrimSuffix(strings.TrimPrefix(env["HOST"], "["), "]")
	switch {
	case host == "" || host == "localhost" || strings.HasPrefix(host, "127.") || host == "::1":
		return nil, true
	case host == "0.0.0.0" || host == "::":
		if len(node.Server.Addresses) == 0 {
			return nil, true
		}
		scheme, rest, _ := strings.Cut(node.Server.PanelURL, "://")
		moved := scheme + "://" + node.Server.Addresses[0] + strings.TrimPrefix(rest, env["HOST"])
		return bytes.Replace(reply, []byte(`"panelUrl":"`+node.Server.PanelURL+`"`), []byte(`"panelUrl":"`+moved+`"`), 1), false
	}
	return reply, false
}

// TestInterfaceOrderAgainstNode puts interface names in the order of the
// keys of a JavaScript object: array indices first.
func TestInterfaceOrderAgainstNode(t *testing.T) {
	for _, c := range loadLAN(t).KeyOrders {
		var list []Interface
		for _, name := range c.Names {
			list = append(list, Interface{Name: name})
		}
		var got []string
		for _, iface := range NodeOrder(list) {
			got = append(got, iface.Name)
		}
		if strings.Join(got, "|") != strings.Join(c.Keys, "|") {
			t.Errorf("names %q: order %q, Node %q", c.Names, got, c.Keys)
		}
	}
}

// TestProbesAgainstNode decides for every recorded datagram whether it
// gets an answer, as Node's handler did.
func TestProbesAgainstNode(t *testing.T) {
	rec := loadLAN(t)
	answered := 0
	for i, c := range rec.Probes {
		msg := decode64(t, c.Probe)
		if got := IsProbe(msg); got != c.Answered {
			shown := string(msg)
			if len(shown) > 120 {
				shown = shown[:120] + "..."
			}
			t.Errorf("probe %d %q: answered %v, Node %v", i, shown, got, c.Answered)
		}
		if c.Answered {
			answered++
		}
	}
	if len(rec.Probes) < 5000 || answered < 100 {
		t.Fatalf("recording has %d probes, %d answered", len(rec.Probes), answered)
	}
}

// TestDiscoveryPortsAgainstNode reads DISCOVERY_PORT values as Node did:
// the number it announces, logs and binds. A number outside 1 to 65535
// is 21119 with a warning, where Node bound another port than it named
// (the low 16 bits; 0 let the system pick one) (BUGS JB4).
func TestDiscoveryPortsAgainstNode(t *testing.T) {
	outside := 0
	for _, c := range loadLAN(t).Ports {
		settings := FromEnv(func(key string) string {
			if key == "DISCOVERY_PORT" {
				return c.Value
			}
			return ""
		}, Settings{})
		raw := strings.Trim(string(c.Port), `"`)
		if want := "  LAN Discovery active on UDP port " + raw; len(c.Logs) != 1 || c.Logs[0] != want {
			t.Errorf("%q: Node logged %q, not %q", c.Value, c.Logs, want)
		}
		want, wantBound, warn := raw, c.Bound, ""
		if node, err := strconv.ParseFloat(raw, 64); err != nil || !(node >= 1 && node <= 65535) {
			outside++
			want, wantBound, warn = "21119", 21119, c.Value
		}
		if got := numberString(settings.DiscoveryPort); got != want {
			t.Errorf("%q: port %s, want %s (Node %s)", c.Value, got, want, raw)
		}
		if bound := float64(int(settings.DiscoveryPort)); bound != wantBound {
			t.Errorf("%q: binds %v, want %v (Node %v)", c.Value, bound, wantBound, c.Bound)
		}
		if settings.BadDiscoveryPort != warn {
			t.Errorf("%q: the value to warn about is %q, want %q", c.Value, settings.BadDiscoveryPort, warn)
		}
	}
	if outside == 0 {
		t.Error("no recorded value outside 1 to 65535")
	}
}

// TestLoopbackConsoleIsNotAnnounced: a console that listens on loopback
// only, with no public address set, binds no discovery port and says why;
// a public host or URL announces it.
func TestLoopbackConsoleIsNotAnnounced(t *testing.T) {
	for _, c := range []struct {
		env   map[string]string
		binds bool
	}{
		{map[string]string{}, false},
		{map[string]string{"HOST": "127.0.0.1"}, false},
		{map[string]string{"HOST": "localhost"}, false},
		{map[string]string{"HOST": "::1"}, false},
		{map[string]string{"HOST": "127.0.0.1", "PANEL_PUBLIC_HOST": "desk.lan"}, true},
		{map[string]string{"PANEL_PUBLIC_URL": "https://desk.example"}, true},
		{map[string]string{"HOST": "0.0.0.0"}, true},
		{map[string]string{"HOST": "192.168.1.10"}, true},
	} {
		log := &lines{}
		lan := NewLAN(FromEnv(func(key string) string { return c.env[key] }, Settings{}))
		lan.logf = log.logf
		bound := false
		lan.listen = func(context.Context, string) (net.PacketConn, error) {
			bound = true
			return nil, fmt.Errorf("not in this test")
		}
		lan.Run(context.Background())
		if bound != c.binds || !c.binds && log.String() != "  LAN Discovery off: "+ErrNoAddress.Error() {
			t.Errorf("env %v: bound %v, log %q", c.env, bound, log.String())
		}
	}
}

// TestResponderWarnsAboutAPortOutOfRange logs the DISCOVERY_PORT it did
// not take and binds 21119.
func TestResponderWarnsAboutAPortOutOfRange(t *testing.T) {
	log := &lines{}
	env := map[string]string{"DISCOVERY_PORT": "70000", "HOST": "0.0.0.0"}
	lan := NewLAN(FromEnv(func(key string) string { return env[key] }, Settings{}))
	lan.logf = log.logf
	var bound string
	lan.listen = func(_ context.Context, address string) (net.PacketConn, error) {
		bound = address
		return nil, fmt.Errorf("not in this test")
	}
	lan.Run(context.Background())
	if bound != "0.0.0.0:21119" || !strings.Contains(log.String(), `WARN: LAN Discovery: DISCOVERY_PORT="70000" is no port (1-65535); using 21119`) {
		t.Errorf("bound %q, log %q", bound, log.String())
	}
}

// lines collects log lines.
type lines struct {
	mu  sync.Mutex
	all []string
}

func (l *lines) logf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.all = append(l.all, fmt.Sprintf(format, args...))
}

func (l *lines) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.all, "\n")
}

// TestResponderAnswersOverUDP runs the responder on a free port: a
// datagram that is no probe gets nothing, a probe gets the announcement
// at the sender's address, and Run returns when its context ends. Probes
// come from one socket and other datagrams from another between them; the
// host name counts the announcements built, so an answer to a datagram
// that is no probe shows in the second reply.
func TestResponderAnswersOverUDP(t *testing.T) {
	rec := loadLAN(t)
	log := &lines{}
	var built atomic.Int32
	settings := Settings{
		Hostname:      func() (string, error) { return fmt.Sprintf("host-%d", built.Add(1)), nil },
		Interfaces:    func() ([]Interface, error) { return nil, nil },
		Host:          "192.168.1.10",
		Port:          5000,
		APIPort:       21121,
		Version:       "1.0",
		DiscoveryPort: 21119,
	}
	lan := NewLAN(settings)
	lan.logf = log.logf
	bound := make(chan net.PacketConn, 1)
	listen := lan.listen
	lan.listen = func(ctx context.Context, address string) (net.PacketConn, error) {
		if address != "0.0.0.0:21119" {
			t.Errorf("binds %s, want 0.0.0.0:21119", address)
		}
		// Any free port, so the test does not need 21119
		conn, err := listen(ctx, "0.0.0.0:0")
		if err == nil {
			bound <- conn
		}
		return conn, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); lan.Run(ctx) }()
	server := <-bound
	port := server.LocalAddr().(*net.UDPAddr).Port

	open := func() *net.UDPConn {
		c, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = c.Close() })
		return c
	}
	prober, other := open(), open()
	to := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: port}
	send := func(c *net.UDPConn, msg string) {
		if _, err := c.WriteTo([]byte(msg), to); err != nil {
			t.Fatal(err)
		}
	}
	send(prober, `{"type":"betterdesk-discover","version":1}`)
	send(other, `{"type":"betterdesk-announce"}`)
	send(other, "not json")
	send(prober, `{"type":"betterdesk-discover"}`)
	_ = prober.SetReadDeadline(time.Now().Add(10 * time.Second))
	buf := make([]byte, 65536)
	for i := 1; i <= 2; i++ {
		n, from, err := prober.ReadFrom(buf)
		if err != nil {
			t.Fatalf("reply %d: %v", i, err)
		}
		if from.(*net.UDPAddr).Port != port {
			t.Errorf("reply from port %d, want %d", from.(*net.UDPAddr).Port, port)
		}
		want := `{"type":"betterdesk-announce","version":1,"server":{"name":"host-` + fmt.Sprint(i) +
			`","version":"1.0","port":5000,"apiPort":21121,"protocol":"http","publicKey":"","addresses":[],"discoveryPort":21119,"panelUrl":"http://192.168.1.10:5000"}}`
		if string(buf[:n]) != want {
			t.Errorf("reply %d %s, want %s", i, buf[:n], want)
		}
	}
	if !bytes.Equal(decode64(t, rec.Exchange.Reply), decode64(t, rec.Exchange.Expected)) {
		t.Error("Node's reply over UDP differs from its announcement")
	}

	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return when its context ended")
	}
	if got := log.String(); got != "  LAN Discovery active on UDP port 21119" {
		t.Errorf("log %q", got)
	}
}

// sentTo is a socket that records where the responder sends to.
type sentTo struct {
	net.PacketConn
	to []string
}

func (s *sentTo) WriteTo(_ []byte, addr net.Addr) (int, error) {
	s.to = append(s.to, addr.String())
	return 0, nil
}

// TestResponderIgnoresForeignSenders answers probes from local networks
// only (loopback, the private, link-local and carrier-grade NAT ranges,
// the networks of the host's interfaces), and a sender at most five times
// a second.
func TestResponderIgnoresForeignSenders(t *testing.T) {
	lan := NewLAN(Settings{
		Hostname: func() (string, error) { return "h", nil },
		Interfaces: func() ([]Interface, error) {
			return []Interface{{Name: "eth0", Entries: []Address{
				{Family: "IPv4", Address: "203.0.113.5", Prefix: 24},
				{Family: "IPv4", Address: "192.0.2.9"},
				{Family: "IPv6", Address: "2001:db8::5", Prefix: 64},
			}}}, nil
		},
		Port: 5000, DiscoveryPort: 21119,
	})
	now := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	lan.now = func() time.Time { return now }
	probe := []byte(`{"type":"betterdesk-discover"}`)
	conn := &sentTo{}
	send := func(ip string) bool {
		t.Helper()
		before := len(conn.to)
		lan.answer(conn, probe, &net.UDPAddr{IP: net.ParseIP(ip), Port: 40000})
		return len(conn.to) > before
	}
	for ip, want := range map[string]bool{
		"127.0.0.1": true, "10.1.2.3": true, "172.16.0.1": true, "192.168.1.20": true, "169.254.10.1": true,
		"100.64.0.1": true, "100.127.255.254": true, "203.0.113.200": true, "2001:db8::77": true, "fe80::1": true,
		"8.8.8.8": false, "203.0.114.1": false, "100.128.0.1": false, "192.0.2.10": false, "172.32.0.1": false, "2001:db9::1": false,
	} {
		if got := send(ip); got != want {
			t.Errorf("probe from %s: answered %v, want %v", ip, got, want)
		}
	}
	answered := 0
	for range 8 {
		if send("192.168.7.7") {
			answered++
		}
	}
	if answered != 5 {
		t.Errorf("8 probes in one second: %d answered, want 5", answered)
	}
	now = now.Add(time.Second)
	if !send("192.168.7.7") {
		t.Error("the next second: no answer")
	}
}

// TestResponderOnABusyPort logs Node's warning and returns when another
// socket holds the port without sharing it.
func TestResponderOnABusyPort(t *testing.T) {
	holder, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero})
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close()
	port := holder.LocalAddr().(*net.UDPAddr).Port
	log := &lines{}
	lan := NewLAN(Settings{DiscoveryPort: float64(port)})
	lan.logf = log.logf
	done := make(chan struct{})
	go func() { defer close(done); lan.Run(context.Background()) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Run kept going on a busy port")
	}
	if want := fmt.Sprintf("WARN: LAN Discovery: port %d already in use, service disabled", port); log.String() != want {
		t.Errorf("log %q, want %q", log.String(), want)
	}
}

// TestAnnouncementFailsWithoutHostname sends nothing when the machine's
// name or interfaces cannot be read, as Node's handler threw and dropped
// the probe.
func TestAnnouncementFailsWithoutHostname(t *testing.T) {
	for _, s := range []Settings{
		{Hostname: func() (string, error) { return "", fmt.Errorf("no name") }, Interfaces: func() ([]Interface, error) { return nil, nil }},
		{Hostname: func() (string, error) { return "h", nil }, Interfaces: func() ([]Interface, error) { return nil, fmt.Errorf("no list") }},
	} {
		if out, err := s.Announcement(); err == nil {
			t.Errorf("announcement %s without a name or interface list", out)
		}
	}
}
