package discovery

import (
	"context"
	"errors"
	"net"
	"runtime"
	"sync"
	"syscall"
)

// multicast is the mDNS socket as multicast-dns set it up: bound to
// 0.0.0.0:5353 with SO_REUSEADDR, the group joined on the first IPv4
// address of every interface (loopback included), the default interface
// for sending, a TTL of 255 and loopback on, so programs on this machine
// hear the announcement too. It hands on only datagrams whose source is
// on one of the host's networks (onLink).
type multicast struct {
	conn     *net.UDPConn
	raw      syscall.RawConn
	settings Settings
	packets  chan []byte
	// done closes when the reader has stopped
	done chan struct{}

	mu          sync.Mutex
	memberships map[string]bool
	// ifaces are the interfaces as Update last saw them
	ifaces []Interface
}

// openMulticast opens the mDNS socket and starts reading from it.
func openMulticast(ctx context.Context, settings Settings) (mdnsSocket, error) {
	lc := net.ListenConfig{Control: reuseAddress}
	pc, err := lc.ListenPacket(ctx, "udp4", "0.0.0.0:5353")
	if err != nil {
		return nil, err
	}
	conn := pc.(*net.UDPConn)
	raw, err := conn.SyscallConn()
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	m := &multicast{conn: conn, raw: raw, settings: settings, packets: make(chan []byte), done: make(chan struct{}), memberships: map[string]bool{}}
	m.Update()
	_ = setMulticastTTL(raw, 255)
	_ = setMulticastLoop(raw, true)
	go m.read(ctx)
	return m, nil
}

func (m *multicast) read(ctx context.Context) {
	defer close(m.done)
	buf := make([]byte, 64*1024)
	for {
		n, src, err := m.conn.ReadFromUDP(buf)
		if err != nil {
			if errors.Is(err, net.ErrClosed) || ctx.Err() != nil {
				return
			}
			continue
		}
		m.mu.Lock()
		local := onLink(src.IP, m.ifaces)
		m.mu.Unlock()
		if !local {
			continue
		}
		datagram := append([]byte(nil), buf[:n]...)
		select {
		case m.packets <- datagram:
		case <-ctx.Done():
			return
		}
	}
}

func (m *multicast) Packets() <-chan []byte { return m.packets }

func (m *multicast) Send(b []byte) error {
	_, err := m.conn.WriteToUDP(b, mdnsGroup)
	return err
}

// Close closes the socket and waits for the reader to stop.
func (m *multicast) Close() error {
	err := m.conn.Close()
	<-m.done
	return err
}

// Update joins the group on the first IPv4 address of each interface it
// has not joined yet, and when it joined one, points sends at the default
// interface again. Failures are skipped, as multicast-dns only warned. The
// interfaces it finds are the networks datagrams are taken from.
func (m *multicast) Update() {
	ifaces, err := m.settings.interfaces()
	if err != nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ifaces = ifaces
	updated := joinNew(ifaces, m.memberships, func(ip [4]byte) error {
		return joinGroup(m.raw, [4]byte(mdnsGroup.IP.To4()), ip)
	})
	if updated {
		if ip := net.ParseIP(defaultInterface(ifaces, runtime.GOOS)).To4(); ip != nil {
			_ = setMulticastInterface(m.raw, [4]byte(ip))
		}
	}
}

// joinNew joins the group, by join, on the first IPv4 address of each
// interface that is not in memberships yet, and adds the ones that
// worked. It reports whether it added any.
func joinNew(ifaces []Interface, memberships map[string]bool, join func(ip [4]byte) error) bool {
	updated := false
	for _, addr := range firstIPv4s(ifaces) {
		if memberships[addr] {
			continue
		}
		ip := net.ParseIP(addr).To4()
		if ip == nil || join([4]byte(ip)) != nil {
			continue
		}
		memberships[addr] = true
		updated = true
	}
	return updated
}

// firstIPv4s is the first IPv4 address of every interface, in Node's
// order.
func firstIPv4s(ifaces []Interface) []string {
	var out []string
	for _, iface := range ifaces {
		for _, e := range iface.Entries {
			if e.Family == "IPv4" {
				out = append(out, e.Address)
				break
			}
		}
	}
	return out
}

// defaultInterface is where multicast-dns sent from: 0.0.0.0 (the
// system's choice) when some interface has an IPv4 address that is not
// loopback (on macOS the address itself when that interface is en0), else
// 127.0.0.1.
func defaultInterface(ifaces []Interface, goos string) string {
	for _, iface := range ifaces {
		for _, e := range iface.Entries {
			if e.Family == "IPv4" && !e.Internal {
				if goos == "darwin" && iface.Name == "en0" {
					return e.Address
				}
				return "0.0.0.0"
			}
		}
	}
	return "127.0.0.1"
}

// onLink reports whether ip is on the network of one of the interfaces'
// IPv4 addresses, or is one of them where the system gave no network.
// mDNS answers only such sources (RFC 6762 section 11): a datagram from
// elsewhere was sent from outside the network the service lives in.
func onLink(ip net.IP, ifaces []Interface) bool {
	ip4 := ip.To4()
	if ip4 == nil {
		return false
	}
	for _, iface := range ifaces {
		for _, e := range iface.Entries {
			addr := net.ParseIP(e.Address).To4()
			if e.Family != "IPv4" || addr == nil {
				continue
			}
			prefix := e.Prefix
			if prefix <= 0 || prefix > 32 {
				prefix = 32
			}
			mask := net.CIDRMask(prefix, 32)
			if addr.Mask(mask).Equal(ip4.Mask(mask)) {
				return true
			}
		}
	}
	return false
}
