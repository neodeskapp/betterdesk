package discovery

import (
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
	"testing"
	"time"
)

type nodeInterface struct {
	Name    string `json:"name"`
	Entries []struct {
		Family   string `json:"family"`
		Address  string `json:"address"`
		Internal bool   `json:"internal"`
		MAC      string `json:"mac"`
	} `json:"entries"`
}

type nodeConfig struct {
	Port         float64 `json:"port"`
	APIPort      float64 `json:"apiPort"`
	HTTPSEnabled bool    `json:"httpsEnabled"`
	Host         string  `json:"host"`
	AppVersion   string  `json:"appVersion"`
}

type nodeEvent struct {
	T      float64 `json:"t"`
	Op     string  `json:"op"`
	Bytes  string  `json:"bytes"`
	Port   float64 `json:"port"`
	Iface  string  `json:"iface"`
	Addr   string  `json:"addr"`
	Group  string  `json:"group"`
	Target string  `json:"address"`
}

// mdnsRecording is what testdata/nodeoracle/mdns.js wrote.
type mdnsRecording struct {
	Scenarios []struct {
		Env        map[string]string `json:"env"`
		Config     nodeConfig        `json:"config"`
		Interfaces []nodeInterface   `json:"interfaces"`
		Hostname   string            `json:"hostname"`
		Random     float64           `json:"random"`
		Logs       []string          `json:"logs"`
		LaterLogs  []string          `json:"laterLogs"`
		Events     []nodeEvent       `json:"events"`
	} `json:"scenarios"`
	Conflict struct {
		SentBefore int         `json:"sentBefore"`
		Events     []nodeEvent `json:"events"`
		Logs       []string    `json:"logs"`
		Packet     string      `json:"packet"`
	} `json:"conflict"`
	Early struct {
		Events []nodeEvent `json:"events"`
	} `json:"early"`
	Answers []struct {
		Config     nodeConfig      `json:"config"`
		Interfaces []nodeInterface `json:"interfaces"`
		Hostname   string          `json:"hostname"`
		Packets    []struct {
			Packet  string `json:"packet"`
			Decoded struct {
				OK          bool          `json:"ok"`
				Type        string        `json:"type"`
				Questions   []dnsQuestion `json:"questions"`
				Answers     []string      `json:"answers"`
				Additionals []string      `json:"additionals"`
			} `json:"decoded"`
			Sent []string `json:"sent"`
		} `json:"packets"`
	} `json:"answers"`
}

var (
	mdnsOnce sync.Once
	mdnsRec  mdnsRecording
	mdnsErr  error
)

func loadMDNS(t testing.TB) *mdnsRecording {
	t.Helper()
	mdnsOnce.Do(func() {
		f, err := os.Open(filepath.Join("testdata", "mdns.json.gz"))
		if err != nil {
			mdnsErr = err
			return
		}
		defer f.Close()
		zr, err := gzip.NewReader(f)
		if err != nil {
			mdnsErr = err
			return
		}
		raw, err := io.ReadAll(zr)
		if err != nil {
			mdnsErr = err
			return
		}
		mdnsErr = json.Unmarshal(raw, &mdnsRec)
	})
	if mdnsErr != nil {
		t.Fatal(mdnsErr)
	}
	return &mdnsRec
}

func toInterfaces(list []nodeInterface) []Interface {
	var out []Interface
	for _, iface := range list {
		entry := Interface{Name: iface.Name}
		for _, e := range iface.Entries {
			entry.Entries = append(entry.Entries, Address{Family: e.Family, Address: e.Address, Internal: e.Internal, MAC: e.MAC})
		}
		out = append(out, entry)
	}
	return out
}

func mdnsSettings(env map[string]string, config nodeConfig, ifaces []Interface, hostname string) Settings {
	return FromEnv(func(key string) string { return env[key] }, Settings{
		Hostname:   func() (string, error) { return hostname, nil },
		Interfaces: func() ([]Interface, error) { return ifaces, nil },
		PubKeyPath: filepath.Join(os.TempDir(), "no-such-key-file"),
		Port:       config.Port,
		APIPort:    config.APIPort,
		HTTPS:      config.HTTPSEnabled,
		Version:    config.AppVersion,
	})
}

// fakeSocket records what the announcement sends, at the test's clock.
type fakeSocket struct {
	packets chan []byte
	clock   *fakeClock

	mu      sync.Mutex
	sent    []sentPacket
	want    int
	enough  chan struct{}
	closed  bool
	updates int
}

type sentPacket struct {
	at    float64
	bytes []byte
}

func newFakeSocket(clock *fakeClock, want int) *fakeSocket {
	return &fakeSocket{packets: make(chan []byte), clock: clock, want: want, enough: make(chan struct{})}
}

func (f *fakeSocket) Packets() <-chan []byte { return f.packets }

func (f *fakeSocket) Send(b []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, sentPacket{at: f.clock.ms(), bytes: append([]byte(nil), b...)})
	if len(f.sent) == f.want {
		close(f.enough)
	}
	return nil
}

func (f *fakeSocket) Update() {
	f.mu.Lock()
	f.updates++
	f.mu.Unlock()
}

func (f *fakeSocket) Close() error {
	f.mu.Lock()
	f.closed = true
	f.mu.Unlock()
	return nil
}

// fakeClock hands out timers the test fires.
type fakeClock struct {
	mu       sync.Mutex
	now      time.Duration
	requests chan timerRequest
	ticks    chan time.Time
	stopped  bool
}

type timerRequest struct {
	d  time.Duration
	ch chan time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{requests: make(chan timerRequest, 1), ticks: make(chan time.Time)}
}

func (c *fakeClock) after(d time.Duration) <-chan time.Time {
	ch := make(chan time.Time)
	c.requests <- timerRequest{d: d, ch: ch}
	return ch
}

func (c *fakeClock) tick(d time.Duration) (<-chan time.Time, func()) {
	if d != membershipEvery {
		panic(fmt.Sprintf("ticker of %v", d))
	}
	return c.ticks, func() {
		c.mu.Lock()
		c.stopped = true
		c.mu.Unlock()
	}
}

func (c *fakeClock) ms() float64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return float64(c.now) / float64(time.Millisecond)
}

// fire takes the pending timer request, moves the clock on by its delay
// and fires it. It fails the test when no request comes.
func (c *fakeClock) fire(t *testing.T) time.Duration {
	t.Helper()
	select {
	case req := <-c.requests:
		c.mu.Lock()
		c.now += req.d
		c.mu.Unlock()
		req.ch <- time.Time{}
		return req.d
	case <-time.After(10 * time.Second):
		t.Fatal("no timer was asked for")
		return 0
	}
}

// harness runs an MDNS with the fake clock and socket.
type harness struct {
	m      *MDNS
	clock  *fakeClock
	sock   *fakeSocket
	logs   *lines
	cancel context.CancelFunc
	done   chan struct{}
	opened chan struct{}
}

func startHarness(settings Settings, random float64, wantSends int) *harness {
	h := &harness{clock: newFakeClock(), logs: &lines{}, done: make(chan struct{}), opened: make(chan struct{})}
	h.sock = newFakeSocket(h.clock, wantSends)
	h.m = NewMDNS(settings)
	h.m.logf = h.logs.logf
	h.m.random = func() float64 { return random }
	h.m.after = h.clock.after
	h.m.tick = h.clock.tick
	h.m.open = func(ctx context.Context, s Settings) (mdnsSocket, error) {
		close(h.opened)
		return h.sock, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel
	go func() { defer close(h.done); h.m.Run(ctx) }()
	return h
}

// stop ends Run and checks that it returned with its socket closed and
// its ticker stopped. With noTimer it also checks that no timer is left
// asked for, as Node had none left at the end of its schedule.
func (h *harness) stop(t *testing.T, noTimer bool) {
	t.Helper()
	h.cancel()
	select {
	case <-h.done:
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return when its context ended")
	}
	select {
	case req := <-h.clock.requests:
		if noTimer {
			t.Errorf("a timer of %v was asked for that Node never set", req.d)
		}
	default:
	}
	select {
	case <-h.opened:
		h.sock.mu.Lock()
		closed := h.sock.closed
		h.sock.mu.Unlock()
		h.clock.mu.Lock()
		stopped := h.clock.stopped
		h.clock.mu.Unlock()
		if !closed || !stopped {
			t.Errorf("after Run: socket closed %v, ticker stopped %v", closed, stopped)
		}
	default:
	}
}

// runUntilSent fires each timer Run asks for until it has sent as many
// datagrams as the socket waits for.
func (h *harness) runUntilSent(t *testing.T) {
	t.Helper()
	for {
		select {
		case <-h.sock.enough:
			return
		case req := <-h.clock.requests:
			h.clock.mu.Lock()
			h.clock.now += req.d
			h.clock.mu.Unlock()
			req.ch <- time.Time{}
		case <-time.After(10 * time.Second):
			t.Fatalf("stuck after %d datagrams", len(h.sock.sent))
		}
	}
}

func nodeSends(events []nodeEvent) []nodeEvent {
	var out []nodeEvent
	for _, e := range events {
		if e.Op == "send" {
			out = append(out, e)
		}
	}
	return out
}

func compareSends(t *testing.T, label string, got []sentPacket, want []nodeEvent) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("%s: %d datagrams sent, Node %d", label, len(got), len(want))
	}
	for i := 0; i < len(got) && i < len(want); i++ {
		wantBytes, _ := base64.StdEncoding.DecodeString(want[i].Bytes)
		if got[i].at != want[i].T || string(got[i].bytes) != string(wantBytes) || want[i].Port != 5353 || want[i].Target != "224.0.0.251" {
			t.Errorf("%s: datagram %d at %vms\n got %x\nwant %x at %vms to %s:%v", label, i, got[i].at, got[i].bytes, wantBytes, want[i].T, want[i].Target, want[i].Port)
		}
	}
}

// TestMDNSScheduleAgainstNode runs the announcement for every recorded
// setting on a test clock: the log line, the probe queries, the
// announcements, when each went out and their bytes must be Node's, and
// Run must end with its context.
func TestMDNSScheduleAgainstNode(t *testing.T) {
	rec := loadMDNS(t)
	if len(rec.Scenarios) < 10 {
		t.Fatalf("recording has %d scenarios", len(rec.Scenarios))
	}
	for i, s := range rec.Scenarios {
		settings := mdnsSettings(s.Env, s.Config, toInterfaces(s.Interfaces), s.Hostname)
		want := nodeSends(s.Events)
		h := startHarness(settings, s.Random, len(want))
		if len(want) > 0 {
			<-h.opened
			h.runUntilSent(t)
		}
		h.stop(t, true)
		compareSends(t, fmt.Sprintf("scenario %d %v", i, s.Env), h.sock.sent, want)
		var wantLogs []string
		for _, line := range append(append([]string{}, s.Logs...), s.LaterLogs...) {
			wantLogs = append(wantLogs, line)
		}
		gotLogs := strings.Split(strings.ReplaceAll(h.logs.String(), "WARN: ", ""), "\n")
		if h.logs.String() == "" {
			gotLogs = nil
		}
		if strings.Join(gotLogs, "\n") != strings.Join(wantLogs, "\n") {
			t.Errorf("scenario %d %v: log %q, Node %q", i, s.Env, gotLogs, wantLogs)
		}
	}
}

// TestMDNSConflictAgainstNode answers a response that claims the
// service's name while probing with the goodbye Node sent, and the log
// line; one that comes before the first query is ignored.
func TestMDNSConflictAgainstNode(t *testing.T) {
	rec := loadMDNS(t)
	s := rec.Scenarios[0]
	settings := mdnsSettings(s.Env, s.Config, toInterfaces(s.Interfaces), s.Hostname)
	packet, _ := base64.StdEncoding.DecodeString(rec.Conflict.Packet)

	want := nodeSends(rec.Conflict.Events)
	h := startHarness(settings, s.Random, len(want))
	h.clock.fire(t)
	h.sock.packets <- packet
	select {
	case <-h.done:
	case <-time.After(10 * time.Second):
		t.Fatal("Run kept going after the conflict")
	}
	h.stop(t, false)
	compareSends(t, "conflict", h.sock.sent, want)
	if got := h.logs.String(); !strings.HasSuffix(got, "\n"+rec.Conflict.Logs[1]) {
		t.Errorf("log %q, Node %q", got, rec.Conflict.Logs)
	}

	want = nodeSends(rec.Early.Events)
	h = startHarness(settings, s.Random, len(want))
	h.sock.packets <- packet
	h.runUntilSent(t)
	h.stop(t, true)
	compareSends(t, "early conflict", h.sock.sent, want)
}

// TestMDNSMembershipsAgainstNode joins the group on the addresses Node
// joined, and sends by the interface it chose.
func TestMDNSMembershipsAgainstNode(t *testing.T) {
	for i, s := range loadMDNS(t).Scenarios {
		var joined []string
		var chosen string
		for _, e := range s.Events {
			switch e.Op {
			case "addMembership":
				joined = append(joined, e.Iface)
			case "setMulticastInterface":
				chosen = e.Addr
			}
		}
		if len(nodeSends(s.Events)) == 0 {
			continue
		}
		ifaces := NodeOrder(toInterfaces(s.Interfaces))
		var got []string
		memberships := map[string]bool{}
		updated := joinNew(ifaces, memberships, func(ip [4]byte) error {
			got = append(got, net.IP(ip[:]).String())
			return nil
		})
		if strings.Join(got, ",") != strings.Join(joined, ",") || updated != (len(joined) > 0) {
			t.Errorf("scenario %d: joins %v (updated %v), Node %v", i, got, updated, joined)
		}
		if joinNew(ifaces, memberships, func([4]byte) error { t.Error("joined twice"); return nil }) {
			t.Errorf("scenario %d: a second pass reports new memberships", i)
		}
		if len(joined) > 0 {
			if got := defaultInterface(ifaces, "linux"); got != chosen {
				t.Errorf("scenario %d: sends by %s, Node %s", i, got, chosen)
			}
		}
	}
}

// TestMDNSAnswersAgainstNode decodes every recorded datagram as
// dns-packet did and answers it as the announced service did.
func TestMDNSAnswersAgainstNode(t *testing.T) {
	total, answered := 0, 0
	for _, a := range loadMDNS(t).Answers {
		settings := mdnsSettings(map[string]string{"PANEL_PUBLIC_URL": "http://panel.example:5000/"}, a.Config, toInterfaces(a.Interfaces), a.Hostname)
		m := NewMDNS(settings)
		m.logf = func(string, ...any) {}
		svc, ok := m.prepare()
		if !ok {
			t.Fatal("no service")
		}
		ifaces, _ := settings.interfaces()
		reg := newRegistry()
		reg.register(svc.records(ifaces))
		for i, p := range a.Packets {
			total++
			raw, _ := base64.StdEncoding.DecodeString(p.Packet)
			msg, err := decodeMessage(raw)
			switch {
			case (err == nil) != p.Decoded.OK:
				t.Errorf("datagram %d %x: decodes %v, Node %v", i, raw, err == nil, p.Decoded.OK)
			case err == nil:
				gotType := "query"
				if msg.Response {
					gotType = "response"
				}
				if gotType != p.Decoded.Type || fmt.Sprint(msg.Questions) != fmt.Sprint(p.Decoded.Questions) ||
					fmt.Sprint(msg.Answers) != fmt.Sprint(p.Decoded.Answers) || fmt.Sprint(msg.Additionals) != fmt.Sprint(p.Decoded.Additionals) {
					t.Errorf("datagram %d %x:\n got %s %v %q %q\nwant %s %v %q %q", i, raw, gotType, msg.Questions, msg.Answers, msg.Additionals,
						p.Decoded.Type, p.Decoded.Questions, p.Decoded.Answers, p.Decoded.Additionals)
				}
			}
			var got []string
			for _, out := range reg.respond(raw) {
				got = append(got, base64.StdEncoding.EncodeToString(out))
			}
			if strings.Join(got, " ") != strings.Join(p.Sent, " ") {
				t.Errorf("datagram %d %x: answers\n got %q\nwant %q", i, raw, got, p.Sent)
			}
			if len(p.Sent) > 0 {
				answered++
			}
		}
	}
	if total < 5000 || answered < 100 {
		t.Fatalf("recording has %d datagrams, %d answered", total, answered)
	}
}

// TestMDNSAnswersWhileRunning feeds a query to a running announcement and
// gets the answer on the socket.
func TestMDNSAnswersWhileRunning(t *testing.T) {
	rec := loadMDNS(t)
	s := rec.Scenarios[0]
	settings := mdnsSettings(s.Env, s.Config, toInterfaces(s.Interfaces), s.Hostname)
	h := startHarness(settings, s.Random, 5)
	for i := 0; i < 4; i++ {
		h.clock.fire(t)
	}
	// 3 queries and the first announcement are out; a question about the
	// service type gets one answer
	h.sock.packets <- encodeQuery(serviceType + tld)
	<-h.sock.enough
	h.clock.ticks <- time.Time{}
	h.stop(t, false)
	h.sock.mu.Lock()
	defer h.sock.mu.Unlock()
	if len(h.sock.sent) != 5 {
		t.Fatalf("%d datagrams sent", len(h.sock.sent))
	}
	answer := newRegistry()
	svc, _ := NewMDNS(settings).prepare()
	ifaces, _ := settings.interfaces()
	answer.register(svc.records(ifaces))
	if want := answer.respond(encodeQuery(serviceType + tld)); len(want) != 1 || string(h.sock.sent[4].bytes) != string(want[0]) {
		t.Errorf("answer %x, want %x", h.sock.sent[4].bytes, want)
	}
	// The membership goroutine may still be at its tick when Run ends;
	// it has to be done by then
	if h.sock.updates > 1 {
		t.Errorf("%d membership updates after one tick", h.sock.updates)
	}
}
