package config

import (
	"net"
	"strconv"
	"testing"
)

// ListenAddr is the address a server listens on for port: every interface,
// or only loopback inside a test binary. go test builds each test binary
// at a new temporary path, so on Windows a test that listens on every
// interface brings up the firewall prompt again on every run.
func ListenAddr(port int) string {
	host := ""
	if testing.Testing() {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, strconv.Itoa(port))
}
