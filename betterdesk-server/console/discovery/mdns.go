package discovery

import (
	"context"
	"math/rand/v2"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/unitronix/betterdesk-server/console/ssrfguard"
	"github.com/unitronix/betterdesk-server/logging"
)

// The mDNS announcement publishes the service _betterdesk._tcp with the
// console's port and its address and version in TXT records, the way
// bonjour-service did for the old console: three probe queries for the
// service's name 250 ms apart (after a random wait of up to 250 ms),
// then the records, sent again after 3, 9, 27 ... seconds while the
// wait stays under an hour, and answers to queries for them. At stop the
// records go out once more with a lifetime of 0.

const (
	// instanceName is the service's name (the old console's appName).
	instanceName = "BetterDesk Console"
	serviceType  = "_betterdesk._tcp"
	tld          = ".local"

	probeInterval   = 250 * time.Millisecond
	probeCount      = 3
	firstAnnounce   = time.Second
	announceFactor  = 3
	announceMax     = time.Hour
	membershipEvery = 5 * time.Second
)

// mdnsGroup is where every mDNS message goes.
var mdnsGroup = &net.UDPAddr{IP: net.IPv4(224, 0, 0, 251), Port: 5353}

// mdnsSocket is the multicast socket the announcement uses.
type mdnsSocket interface {
	// Packets delivers the datagrams that arrive.
	Packets() <-chan []byte
	// Send sends a datagram to the mDNS group.
	Send(b []byte) error
	// Update joins the group on interfaces that were not there before.
	Update()
	Close() error
}

// service is the published service.
type service struct {
	// name is the service's instance name, fqdn its name in the service
	// type
	name string
	fqdn string
	host string
	port uint16
	txt  [][]byte
}

// newService is the service of the instance name.
func newService(name string) *service {
	s := &service{}
	s.setName(name)
	return s
}

func (s *service) setName(name string) {
	s.name = name
	s.fqdn = strings.ReplaceAll(name, ".", "-") + "." + serviceType + tld
}

// rename gives the service the attempt-th name: instanceName (n).
func (s *service) rename(attempt int) {
	s.setName(instanceName + " (" + strconv.Itoa(attempt) + ")")
}

// records are the service's records: the pointer to it, its SRV and TXT
// records, the pointer from the list of service types, and an A or AAAA
// record for each address of an interface that is not loopback and has a
// hardware address.
func (s *service) records(ifaces []Interface) []dnsRecord {
	out := []dnsRecord{
		{Name: serviceType + tld, Type: "PTR", TTL: 28800, Data: s.fqdn},
		{Name: s.fqdn, Type: "SRV", TTL: 120, Data: srvData{Port: s.port, Target: s.host}},
		{Name: s.fqdn, Type: "TXT", TTL: 4500, Data: s.txt},
		{Name: "_services._dns-sd._udp" + tld, Type: "PTR", TTL: 120, Data: serviceType + tld},
	}
	for _, iface := range ifaces {
		for _, addr := range iface.Entries {
			if addr.Internal || addr.MAC == "00:00:00:00:00:00" {
				continue
			}
			switch addr.Family {
			case "IPv4":
				out = append(out, dnsRecord{Name: s.host, Type: "A", TTL: 120, Data: addr.Address})
			case "IPv6":
				out = append(out, dnsRecord{Name: s.host, Type: "AAAA", TTL: 120, Data: addr.Address})
			}
		}
	}
	return out
}

// registry holds the records the responder answers with, by type, in the
// order the types first came.
type registry struct {
	types   []string
	records map[string][]dnsRecord
}

func newRegistry() *registry { return &registry{records: map[string][]dnsRecord{}} }

// register adds records, leaving out one that is already there with the
// same type, name and data.
func (r *registry) register(records []dnsRecord) {
	for _, rec := range records {
		list, ok := r.records[rec.Type]
		if !ok {
			r.types = append(r.types, rec.Type)
		}
		duplicate := false
		for _, have := range list {
			if have.Name == rec.Name && dataEqual(have.Data, rec.Data) {
				duplicate = true
				break
			}
		}
		if !duplicate {
			r.records[rec.Type] = append(list, rec)
		}
	}
}

// dnsEqual compares names with the letters A to Z in lower case.
func dnsEqual(a, b string) bool {
	lower := func(s string) string {
		return strings.Map(func(r rune) rune {
			if r >= 'A' && r <= 'Z' {
				return r + 'a' - 'A'
			}
			return r
		}, s)
	}
	return lower(a) == lower(b)
}

// recordsFor are the records of a type for a name. A name without a dot
// is compared with the first label of each record's name.
func (r *registry) recordsFor(name, typ string) []dnsRecord {
	var out []dnsRecord
	for _, rec := range r.records[typ] {
		have := rec.Name
		if !strings.Contains(name, ".") {
			have, _, _ = strings.Cut(have, ".")
		}
		if dnsEqual(have, name) {
			out = append(out, rec)
		}
	}
	return out
}

// answer is the response to one question, nil when there is nothing to
// say: for ANY every record of the name; else the records of the type,
// and with a pointer the SRV and TXT records it points at and the
// addresses of their hosts.
func (r *registry) answer(q dnsQuestion) []byte {
	var answers, additionals []dnsRecord
	if q.Type == "ANY" {
		for _, typ := range r.types {
			answers = append(answers, r.recordsFor(q.Name, typ)...)
		}
	} else {
		answers = r.recordsFor(q.Name, q.Type)
	}
	if len(answers) == 0 {
		return nil
	}
	if q.Type != "ANY" {
		for _, a := range answers {
			if a.Type != "PTR" {
				continue
			}
			target := a.Data.(string)
			additionals = append(additionals, r.recordsFor(target, "SRV")...)
			additionals = append(additionals, r.recordsFor(target, "TXT")...)
		}
		var seen []string
		for _, rec := range additionals {
			if rec.Type != "SRV" {
				continue
			}
			target := rec.Data.(srvData).Target
			dup := false
			for _, s := range seen {
				dup = dup || s == target
			}
			if dup {
				continue
			}
			seen = append(seen, target)
			additionals = append(additionals, r.recordsFor(target, "A")...)
			additionals = append(additionals, r.recordsFor(target, "AAAA")...)
		}
	}
	return encodeResponse(answers, additionals)
}

// respond is the responder's reaction to a datagram: one response per
// question it has records for. Datagrams that do not decode and
// responses get nothing.
func (r *registry) respond(datagram []byte) [][]byte {
	msg, err := decodeMessage(datagram)
	if err != nil || msg.Response {
		return nil
	}
	var out [][]byte
	for _, q := range msg.Questions {
		if packet := r.answer(q); packet != nil {
			out = append(out, packet)
		}
	}
	return out
}

// MDNS is the mDNS announcement.
type MDNS struct {
	settings Settings
	logf     func(format string, args ...any)
	random   func() float64
	after    func(d time.Duration) <-chan time.Time
	tick     func(d time.Duration) (<-chan time.Time, func())
	open     func(ctx context.Context, settings Settings) (mdnsSocket, error)
}

// NewMDNS returns the announcement for the settings. Its lines go to the
// log under "console".
func NewMDNS(settings Settings) *MDNS {
	return &MDNS{
		settings: settings,
		logf:     func(format string, args ...any) { logging.Logf("console", format, args...) },
		random:   rand.Float64,
		after:    time.After,
		tick: func(d time.Duration) (<-chan time.Time, func()) {
			t := time.NewTicker(d)
			return t.C, t.Stop
		},
		open: openMulticast,
	}
}

// Enabled reports whether PANEL_MDNS leaves the announcement on.
func (s Settings) mdnsEnabled() bool { return s.MDNS != "off" && s.MDNS != "0" }

// prepare works out the service from the settings, as the old console
// did before it published: nothing when PANEL_MDNS is off or the
// console's address is no URL, the reason in the log when the service
// cannot be described.
func (m *MDNS) prepare() (*service, bool) {
	if !m.settings.mdnsEnabled() {
		return nil, false
	}
	if _, err := m.settings.Announcement(); err != nil {
		m.logf("WARN: [server] mDNS panel discovery disabled: %v", err)
		return nil, false
	}
	panelURL := m.settings.PanelURL()
	parsed, ok := ssrfguard.ParseURL(panelURL)
	if !ok {
		return nil, false
	}
	port := 80.0
	switch {
	case parsed.Port != "":
		port = parseIntOr(parsed.Port, 0)
	case parsed.Protocol == "https:":
		port = 443
	}
	if port == 0 {
		m.logf("WARN: [server] mDNS panel discovery disabled: ServiceConfig requires `port` property to be set")
		return nil, false
	}
	host, err := m.settings.hostname()
	if err != nil {
		m.logf("WARN: [server] mDNS panel discovery disabled: %v", err)
		return nil, false
	}
	// The host goes out as the first label of the machine's name with
	// .local, the name mDNS resolves
	label, _, _ := strings.Cut(strings.Trim(decodeUTF8([]byte(host)), "."), ".")
	svc := newService(instanceName)
	svc.host = label + tld
	svc.port = uint16(port)
	svc.txt = [][]byte{
		[]byte("url=" + strings.TrimSuffix(panelURL, "/")),
		[]byte("version=" + m.settings.Version),
	}
	m.logf("  mDNS discovery published _betterdesk._tcp port %d (%s)", svc.port, panelURL)
	return svc, true
}

// Run publishes the service and answers for it until ctx ends. Problems
// are logged; nothing ends the process.
func (m *MDNS) Run(ctx context.Context) {
	svc, ok := m.prepare()
	if !ok {
		return
	}
	sock, err := m.open(ctx, m.settings)
	if err != nil {
		m.logf("ERROR: mDNS discovery: %v", err)
		return
	}
	defer sock.Close()
	ticks, stopTicks := m.tick(membershipEvery)
	defer stopTicks()
	updates := make(chan struct{})
	updateCtx, stopUpdates := context.WithCancel(ctx)
	go func() {
		defer close(updates)
		for {
			select {
			case <-updateCtx.Done():
				return
			case <-ticks:
				sock.Update()
			}
		}
	}()
	defer func() {
		stopUpdates()
		<-updates
	}()

	reg := newRegistry()
	serve := func(datagram []byte) {
		for _, packet := range reg.respond(datagram) {
			_ = sock.Send(packet)
		}
	}

	// Probing: is the name taken already? A response that claims it makes
	// the probing start again under the next name, "BetterDesk Console
	// (2)", then 3 and on (RFC 6762 section 9).
	wait := time.Duration(m.random() * float64(probeInterval))
	if wait < time.Millisecond {
		wait = time.Millisecond
	}
	sent, restart, attempt := false, false, 1
	for i := 0; i <= probeCount; i++ {
		timer := m.after(wait)
	waiting:
		for {
			select {
			case <-ctx.Done():
				return
			case datagram := <-sock.Packets():
				if sent && m.conflict(datagram, svc.fqdn) {
					attempt++
					taken := svc.name
					svc.rename(attempt)
					m.logf("WARN: mDNS discovery: the name %q is in use on the network; trying %q", taken, svc.name)
					sent, restart = false, true
					continue
				}
				serve(datagram)
			case <-timer:
				break waiting
			}
		}
		if restart {
			i, restart, wait = -1, false, probeInterval
			continue
		}
		if i < probeCount {
			_ = sock.Send(encodeQuery(svc.fqdn))
			sent = true
			wait = probeInterval
		}
	}

	// Announcing
	ifaces, err := m.settings.interfaces()
	if err != nil {
		ifaces = nil
	}
	records := svc.records(ifaces)
	reg.register(records)
	announcement := encodeResponse(records, nil)
	delay := firstAnnounce
	var timer <-chan time.Time
	announce := func() {
		_ = sock.Send(announcement)
		delay *= announceFactor
		timer = nil
		if delay < announceMax {
			timer = m.after(delay)
		}
	}
	announce()
	for {
		select {
		case <-ctx.Done():
			// Browsers drop the service at once, not when its records
			// would run out
			_ = sock.Send(m.goodbye(svc))
			return
		case datagram := <-sock.Packets():
			serve(datagram)
		case <-timer:
			announce()
		}
	}
}

// conflict reports whether a datagram is a response that names the
// service.
func (m *MDNS) conflict(datagram []byte, fqdn string) bool {
	msg, err := decodeMessage(datagram)
	if err != nil || !msg.Response {
		return false
	}
	for _, list := range [][]string{msg.Answers, msg.Additionals} {
		for _, name := range list {
			if dnsEqual(name, fqdn) {
				return true
			}
		}
	}
	return false
}

// goodbye is the service's records with a lifetime of 0, which tells
// browsers the service is gone.
func (m *MDNS) goodbye(svc *service) []byte {
	ifaces, err := m.settings.interfaces()
	if err != nil {
		ifaces = nil
	}
	records := svc.records(ifaces)
	for i := range records {
		records[i].TTL = 0
	}
	return encodeResponse(records, nil)
}
