package discovery

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
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
	for i, c := range rec.Announcements {
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
		if err != nil {
			t.Fatalf("case %d: %v", i, err)
		}
		if want := decode64(t, c.Reply); !bytes.Equal(got, want) {
			t.Errorf("case %d (env %v):\n got %s\nwant %s", i, c.Env, got, want)
		}
	}
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

// TestDiscoveryPortsAgainstNode reads DISCOVERY_PORT values that are no
// port the way Node did: the number in the log line, and the port the
// socket gets (the low 16 bits; 0 lets the system pick one).
func TestDiscoveryPortsAgainstNode(t *testing.T) {
	for _, c := range loadLAN(t).Ports {
		port := FromEnv(func(key string) string {
			if key == "DISCOVERY_PORT" {
				return c.Value
			}
			return ""
		}, Settings{}).DiscoveryPort
		if want := "  LAN Discovery active on UDP port " + numberString(port); len(c.Logs) != 1 || c.Logs[0] != want {
			t.Errorf("%q: Node logged %q, Go would log %q", c.Value, c.Logs, want)
		}
		if raw := strings.Trim(string(c.Port), `"`); raw != numberString(port) {
			t.Errorf("%q: port %s, Node %s", c.Value, numberString(port), raw)
		}
		bound := toUint16(port)
		switch {
		case bound == 0 && c.Bound == 0:
			t.Errorf("%q: Node bound no port", c.Value)
		case bound != 0 && float64(bound) != c.Bound:
			t.Errorf("%q: binds %d, Node bound %v", c.Value, bound, c.Bound)
		}
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
		Host:          "127.0.0.1",
		Port:          5000,
		APIPort:       21121,
		Version:       "1.0",
		DiscoveryPort: 65536, // the low 16 bits are 0: any free port
	}
	lan := NewLAN(settings)
	lan.logf = log.logf
	bound := make(chan net.PacketConn, 1)
	listen := lan.listen
	lan.listen = func(ctx context.Context, address string) (net.PacketConn, error) {
		if address != "0.0.0.0:0" {
			t.Errorf("binds %s, want 0.0.0.0:0", address)
		}
		conn, err := listen(ctx, address)
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
			`","version":"1.0","port":5000,"apiPort":21121,"protocol":"http","publicKey":"","addresses":[],"discoveryPort":65536,"panelUrl":"http://127.0.0.1:5000"}}`
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
	if got := log.String(); got != "  LAN Discovery active on UDP port 65536" {
		t.Errorf("log %q", got)
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
