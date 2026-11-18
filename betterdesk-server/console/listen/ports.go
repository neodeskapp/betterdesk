package listen

import (
	"errors"
	"os"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"syscall"
)

// ResolvePort is the port a console listener uses: the configured one,
// unless it is not above zero (then fallback) or below 1024 while the
// process may not bind such ports (then fallback, with a warning). label
// names the listener in the warning ("HTTP", "HTTPS").
func ResolvePort(configured, fallback int, label string, canBindLow func() bool, logf func(format string, args ...any)) int {
	if configured <= 0 {
		return fallback
	}
	if configured <= 1023 && !canBindLow() {
		logf("WARNING: %s port %d requires root or CAP_NET_BIND_SERVICE; using %d instead", label, configured, fallback)
		logf("  → Set HTTPS_PORT=5443 (or PORT=5000) in .env, use a reverse proxy on :443, or grant CAP_NET_BIND_SERVICE in the systemd unit")
		return fallback
	}
	return configured
}

// CanBindLowPorts reports whether the process counts as one that may
// bind ports below 1024: it runs as root, BETTERDESK_HAS_BIND_SERVICE is
// 1, true, yes or y, or (on Linux) bit 12 of its effective capabilities
// is set. Bit 12 is CAP_NET_ADMIN; CAP_NET_BIND_SERVICE is bit 10. The
// old console tested bit 12, and the installers set the variable.
func CanBindLowPorts(getenv func(string) string) bool {
	if os.Getuid() == 0 {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(getenv("BETTERDESK_HAS_BIND_SERVICE"))) {
	case "1", "true", "yes", "y":
		return true
	}
	if runtime.GOOS != "linux" {
		return false
	}
	status, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return false
	}
	return capabilityBit(string(status))
}

var capEffLine = regexp.MustCompile(`(?m)^CapEff:\s*([0-9a-fA-F]+)`)

// capabilityBit reads the bit of the CapEff line of /proc/self/status
// that the old console took for the right to bind low ports: bit 12.
func capabilityBit(status string) bool {
	m := capEffLine.FindStringSubmatch(status)
	if m == nil {
		return false
	}
	digits := strings.TrimLeft(m[1], "0")
	// Only the low 64 bits can hold the capabilities there are
	if len(digits) > 16 {
		digits = digits[len(digits)-16:]
	}
	if digits == "" {
		return false
	}
	value, err := strconv.ParseUint(digits, 16, 64)
	if err != nil {
		return false
	}
	return value&(1<<12) != 0
}

// InUse reports a listen error because the address is taken.
func InUse(err error) bool {
	var errno syscall.Errno
	return errors.As(err, &errno) && (errno == syscall.EADDRINUSE || errno == wsaEADDRINUSE)
}

// Denied reports a listen error because the process may not bind the
// port.
func Denied(err error) bool {
	var errno syscall.Errno
	return errors.As(err, &errno) && (errno == syscall.EACCES || errno == wsaEACCES)
}

// The Windows socket errors for the two, which syscall does not name.
const (
	wsaEACCES     syscall.Errno = 10013
	wsaEADDRINUSE syscall.Errno = 10048
)
