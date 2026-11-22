package signal

import (
	"net"
	"os"
	"strconv"
	"strings"
)

// routeTable is the kernel's IPv4 routing table on Linux.
const routeTable = "/proc/net/route"

// defaultGateway is the gateway of the server's default route, nil on a
// system without /proc/net/route or without such a route. Inside a
// container on a bridge network it is the bridge gateway, which Docker
// Desktop makes the address of every client.
func defaultGateway() net.IP {
	table, err := os.ReadFile(routeTable)
	if err != nil {
		return nil
	}
	return parseDefaultGateway(string(table))
}

// parseDefaultGateway reads the gateway of the first default route of a
// /proc/net/route table: destination 00000000 with the gateway flag
// (0x2), the address in hex in the kernel's byte order.
func parseDefaultGateway(table string) net.IP {
	for _, line := range strings.Split(table, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 || fields[1] != "00000000" {
			continue
		}
		flags, err := strconv.ParseUint(fields[3], 16, 32)
		if err != nil || flags&0x2 == 0 {
			continue
		}
		gw, err := strconv.ParseUint(fields[2], 16, 32)
		if err != nil || gw == 0 {
			continue
		}
		return net.IPv4(byte(gw), byte(gw>>8), byte(gw>>16), byte(gw>>24))
	}
	return nil
}
