//go:build unix

package discovery

import "syscall"

// reuseAddress lets other sockets bind the same port (SO_REUSEADDR), as
// Node's reuseAddr did.
func reuseAddress(network, address string, raw syscall.RawConn) error {
	var opErr error
	err := raw.Control(func(fd uintptr) {
		opErr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1)
	})
	if err != nil {
		return err
	}
	return opErr
}

// joinGroup joins a multicast group on the interface with the address.
func joinGroup(raw syscall.RawConn, group, iface [4]byte) error {
	return control(raw, func(fd uintptr) error {
		return syscall.SetsockoptIPMreq(int(fd), syscall.IPPROTO_IP, syscall.IP_ADD_MEMBERSHIP, &syscall.IPMreq{Multiaddr: group, Interface: iface})
	})
}

// setMulticastInterface picks the interface multicast datagrams leave by.
func setMulticastInterface(raw syscall.RawConn, iface [4]byte) error {
	return control(raw, func(fd uintptr) error {
		return syscall.SetsockoptInet4Addr(int(fd), syscall.IPPROTO_IP, syscall.IP_MULTICAST_IF, iface)
	})
}

// setMulticastTTL sets how many hops multicast datagrams may take.
func setMulticastTTL(raw syscall.RawConn, ttl int) error {
	return control(raw, func(fd uintptr) error {
		return syscall.SetsockoptInt(int(fd), syscall.IPPROTO_IP, syscall.IP_MULTICAST_TTL, ttl)
	})
}

// setMulticastLoop says whether this machine hears its own multicast.
func setMulticastLoop(raw syscall.RawConn, on bool) error {
	value := 0
	if on {
		value = 1
	}
	return control(raw, func(fd uintptr) error {
		return syscall.SetsockoptInt(int(fd), syscall.IPPROTO_IP, syscall.IP_MULTICAST_LOOP, value)
	})
}

func control(raw syscall.RawConn, fn func(fd uintptr) error) error {
	var opErr error
	if err := raw.Control(func(fd uintptr) { opErr = fn(fd) }); err != nil {
		return err
	}
	return opErr
}
