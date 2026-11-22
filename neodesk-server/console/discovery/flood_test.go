package discovery

import (
	"context"
	"encoding/binary"
	"net"
	"strings"
	"testing"
	"time"
)

// repeatedQuestions is one query datagram that asks n times for the PTR
// records of name: the name once, then a pointer to it per question.
func repeatedQuestions(name string, n int) []byte {
	b := make([]byte, 12)
	binary.BigEndian.PutUint16(b[4:], uint16(n))
	for _, label := range strings.Split(name, ".") {
		b = append(b, byte(len(label)))
		b = append(b, label...)
	}
	b = append(b, 0, 0, 12, 0, 1)
	for i := 1; i < n; i++ {
		b = append(b, 0xC0, 12, 0, 12, 0, 1)
	}
	return b
}

func floodRegistry(t *testing.T) (*registry, Settings) {
	t.Helper()
	rec := loadMDNS(t)
	s := rec.Scenarios[0]
	settings := asPublished(mdnsSettings(s.Env, s.Config, toInterfaces(s.Interfaces), s.Hostname))
	m := NewMDNS(settings)
	m.logf = func(string, ...any) {}
	svc, ok := m.prepare()
	if !ok {
		t.Fatal("no service")
	}
	ifaces, _ := settings.interfaces()
	reg := newRegistry()
	reg.register(svc.records(ifaces))
	return reg, settings
}

// TestMDNSRespondsOncePerDatagram: a datagram with thousands of questions
// gets one response that holds each record once (BUGS.md RV101).
func TestMDNSRespondsOncePerDatagram(t *testing.T) {
	reg, _ := floodRegistry(t)
	datagram := repeatedQuestions(serviceType+tld, 10912)
	out := reg.respond(datagram)
	if len(out) != 1 {
		t.Fatalf("%d responses to one datagram", len(out))
	}
	single := reg.respond(repeatedQuestions(serviceType+tld, 1))
	if len(single) != 1 || string(out[0]) != string(single[0]) {
		t.Errorf("the response to 10,912 questions is not the one to one question:\n got  %x\n want %x", out[0], single)
	}
	// Two questions with records in common: each record once
	both := make([]byte, 12)
	binary.BigEndian.PutUint16(both[4:], 2)
	both = append(both, encodeQuery(serviceType + tld)[12:]...)
	both = append(both, encodeQuery("_services._dns-sd._udp" + tld)[12:]...)
	merged := reg.respond(both)
	msg, err := decodeMessage(merged[0])
	if len(merged) != 1 || err != nil {
		t.Fatalf("two questions: %d responses, %v", len(merged), err)
	}
	seen := map[string]bool{}
	for _, name := range append(msg.Answers, msg.Additionals...) {
		seen[name] = true
	}
	if len(msg.Answers) != 2 || !seen[serviceType+tld] || !seen["_services._dns-sd._udp"+tld] {
		t.Errorf("two questions: answers %q, additionals %q", msg.Answers, msg.Additionals)
	}
}

// TestMDNSAnswersAFewTimesASecond: queries that come faster get no more
// than maxResponsesPerSecond answers in a second (BUGS.md RV101).
func TestMDNSAnswersAFewTimesASecond(t *testing.T) {
	rec := loadMDNS(t)
	s := rec.Scenarios[0]
	settings := asPublished(mdnsSettings(s.Env, s.Config, toInterfaces(s.Interfaces), s.Hostname))
	h := startHarness(settings, s.Random, -1)
	for i := 0; i < 4; i++ {
		h.clock.fire(t)
	}
	start := time.Now()
	for i := 0; i < 20; i++ {
		h.sock.packets <- encodeQuery(serviceType + tld)
	}
	if time.Since(start) > 500*time.Millisecond {
		t.Skip("the queries took longer than half a second to hand over")
	}
	h.stop(t, false)
	h.sock.mu.Lock()
	defer h.sock.mu.Unlock()
	// 3 probes and the announcement before, the goodbye after
	if answers := len(h.sock.sent) - 5; answers < 1 || answers > maxResponsesPerSecond {
		t.Errorf("20 queries in a moment got %d answers", answers)
	}
}

// TestOnLink: a datagram counts when its source is on the network of one
// of the host's IPv4 addresses (RFC 6762 section 11).
func TestOnLink(t *testing.T) {
	ifaces := []Interface{
		{Name: "lo", Entries: []Address{{Family: "IPv4", Address: "127.0.0.1", Internal: true, Prefix: 8}}},
		{Name: "eth0", Entries: []Address{{Family: "IPv4", Address: "192.168.10.20", Prefix: 24}, {Family: "IPv6", Address: "fe80::1", Prefix: 64}}},
		{Name: "tun0", Entries: []Address{{Family: "IPv4", Address: "10.8.0.2"}}},
	}
	for addr, want := range map[string]bool{
		"127.0.0.1": true, "127.4.5.6": true, "192.168.10.1": true, "192.168.10.255": true, "10.8.0.2": true,
		"192.168.11.1": false, "203.0.113.9": false, "10.8.0.3": false, "8.8.8.8": false,
	} {
		if got := onLink(net.ParseIP(addr), ifaces); got != want {
			t.Errorf("onLink(%s) = %v", addr, got)
		}
	}
}

// TestMulticastDropsOffLinkDatagrams: the socket hands on a datagram from
// a source on one of the host's networks and drops one from elsewhere.
func TestMulticastDropsOffLinkDatagrams(t *testing.T) {
	for _, c := range []struct {
		name   string
		ifaces []Interface
		want   bool
	}{
		{"loopback listed", []Interface{{Name: "lo", Entries: []Address{{Family: "IPv4", Address: "127.0.0.1", Internal: true, Prefix: 8}}}}, true},
		{"other network only", []Interface{{Name: "eth0", Entries: []Address{{Family: "IPv4", Address: "192.0.2.10", Prefix: 24}}}}, false},
	} {
		conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		m := &multicast{conn: conn, packets: make(chan []byte), done: make(chan struct{}), ifaces: c.ifaces}
		go m.read(ctx)
		sender, err := net.DialUDP("udp4", nil, conn.LocalAddr().(*net.UDPAddr))
		if err != nil {
			t.Fatal(err)
		}
		_, _ = sender.Write([]byte("datagram"))
		select {
		case got := <-m.Packets():
			if !c.want {
				t.Errorf("%s: %q handed on", c.name, got)
			}
		case <-time.After(time.Second):
			if c.want {
				t.Errorf("%s: nothing handed on", c.name)
			}
		}
		_ = sender.Close()
		cancel()
		_ = m.Close()
	}
}
