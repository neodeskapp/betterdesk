package discovery

import (
	"net"
	"sort"
	"strconv"
)

// Address is one address of a network interface, as Node's
// os.networkInterfaces() describes it.
type Address struct {
	// Family is "IPv4" or "IPv6".
	Family string
	// Address is the address in text, without a zone.
	Address string
	// Internal is true for a loopback interface.
	Internal bool
	// MAC is the interface's hardware address, "00:00:00:00:00:00" when
	// it has none.
	MAC string
	// Prefix is the length in bits of the network the address is on, 0
	// when the system did not say.
	Prefix int
}

// Interface is a network interface with its addresses.
type Interface struct {
	Name    string
	Entries []Address
}

// SystemInterfaces lists the interfaces that are up and running with
// their addresses, in the order the system lists them: what libuv hands
// to Node's os.networkInterfaces().
func SystemInterfaces() ([]Interface, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	var out []Interface
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagRunning == 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			return nil, err
		}
		// libuv keeps the first six bytes of the hardware address
		hw := make(net.HardwareAddr, 6)
		copy(hw, iface.HardwareAddr)
		mac := hw.String()
		entry := Interface{Name: iface.Name}
		for _, addr := range addrs {
			var ip net.IP
			prefix, bits := 0, 0
			switch a := addr.(type) {
			case *net.IPNet:
				ip = a.IP
				prefix, bits = a.Mask.Size()
			case *net.IPAddr:
				ip = a.IP
			default:
				continue
			}
			family := "IPv6"
			if v4 := ip.To4(); v4 != nil {
				family, ip = "IPv4", v4
				if bits == 8*net.IPv6len {
					prefix -= 8 * (net.IPv6len - net.IPv4len)
				}
			}
			entry.Entries = append(entry.Entries, Address{
				Family:   family,
				Address:  ip.String(),
				Internal: iface.Flags&net.FlagLoopback != 0,
				MAC:      mac,
				Prefix:   prefix,
			})
		}
		if len(entry.Entries) > 0 {
			out = append(out, entry)
		}
	}
	return out, nil
}

// NodeOrder puts interfaces in the order of the keys of the object Node
// builds from them: names that are array indices (a canonical decimal
// number below 2^32 - 1) first, by value, then the other names in the
// order they first appear. The entries of a name that appears twice are
// joined in the order they come.
func NodeOrder(list []Interface) []Interface {
	byName := map[string]int{}
	var out []Interface
	for _, iface := range list {
		if i, ok := byName[iface.Name]; ok {
			out[i].Entries = append(out[i].Entries, iface.Entries...)
			continue
		}
		byName[iface.Name] = len(out)
		out = append(out, Interface{Name: iface.Name, Entries: append([]Address(nil), iface.Entries...)})
	}
	sort.SliceStable(out, func(a, b int) bool {
		ia, aok := arrayIndex(out[a].Name)
		ib, bok := arrayIndex(out[b].Name)
		switch {
		case aok && bok:
			return ia < ib
		default:
			return aok && !bok
		}
	})
	return out
}

// arrayIndex reports whether a property name is an array index in
// JavaScript, which an object lists before its other keys.
func arrayIndex(name string) (uint64, bool) {
	if name == "" || (len(name) > 1 && name[0] == '0') {
		return 0, false
	}
	for i := 0; i < len(name); i++ {
		if name[i] < '0' || name[i] > '9' {
			return 0, false
		}
	}
	n, err := strconv.ParseUint(name, 10, 64)
	if err != nil || n >= 1<<32-1 {
		return 0, false
	}
	return n, true
}
