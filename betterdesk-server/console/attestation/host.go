package attestation

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Host reads the processor, memory and disk load of the machine the way
// serverManagement.getResourceSnapshot did. The processor load is the
// share of busy time since the previous reading, so a Host keeps the last
// one; the first reading is 0.
type Host struct {
	mu   sync.Mutex
	last []cpuTimes
	// read reads the processor times, the memory and the disks; tests
	// replace it
	read func() hostReading
}

// cpuTimes are one processor's idle and total time.
type cpuTimes struct{ idle, total float64 }

// hostReading is what the operating system said.
type hostReading struct {
	cpus           []cpuTimes
	memTotal, free float64
	disks          []Disk
	hostname       string
}

// NewHost returns a reader of this machine.
func NewHost() *Host { return &Host{read: readHost} }

// Snapshot reads the machine.
func (h *Host) Snapshot() Snapshot {
	in := h.read()
	h.mu.Lock()
	cpu := cpuPercent(h.last, in.cpus)
	h.last = in.cpus
	h.mu.Unlock()
	percent := 0.0
	if in.memTotal > 0 {
		percent = jsRound(((in.memTotal-in.free)/in.memTotal)*1000) / 10
	}
	return Snapshot{
		CPU:        jsRound(cpu*10) / 10,
		MemTotal:   in.memTotal,
		MemPercent: percent,
		Disks:      in.disks,
		Hostname:   in.hostname,
		CPUCount:   len(in.cpus),
	}
}

// cpuPercent is computeCpuPercent: the busy share of all processors
// between two readings, 0 without a previous one of the same processors.
func cpuPercent(prev, cur []cpuTimes) float64 {
	if len(prev) != len(cur) {
		return 0
	}
	var totalDiff, idleDiff float64
	for i := range cur {
		totalDiff += cur[i].total - prev[i].total
		idleDiff += cur[i].idle - prev[i].idle
	}
	if totalDiff <= 0 {
		return 0
	}
	return max(0, min(100, ((totalDiff-idleDiff)/totalDiff)*100))
}

// runCommand is safeExecSync: what the command printed, trimmed, or ""
// when it fails or runs longer than timeout.
func runCommand(timeout time.Duration, name string, args ...string) string {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// pseudoFileSystems are left out of the disks.
var pseudoFileSystems = map[string]bool{
	"tmpfs": true, "devtmpfs": true, "squashfs": true, "overlay": true, "proc": true, "sysfs": true,
	"cgroup": true, "cgroup2": true, "devpts": true, "debugfs": true, "tracefs": true, "pstore": true,
	"autofs": true, "fusectl": true,
}

// parseDF reads the output of df -B1 --output=target,fstype,size,used,avail:
// the file systems of at least 1 MiB that are not pseudo file systems.
func parseDF(raw string) []Disk {
	var disks []Disk
	lines := strings.Split(raw, "\n")
	if len(lines) > 0 {
		lines = lines[1:]
	}
	for _, line := range lines {
		parts := strings.Fields(line)
		if len(parts) < 5 || pseudoFileSystems[parts[1]] {
			continue
		}
		size, used := parseIntJS(parts[2]), parseIntJS(parts[3])
		if size == 0 || size < 1024*1024 {
			continue
		}
		disks = append(disks, Disk{Size: size, Used: used})
	}
	return disks
}

// parseIntJS is parseInt(text, 10) for the digits of a number: the
// leading digits, NaN (here 0) when there are none.
func parseIntJS(text string) float64 {
	end := 0
	if end < len(text) && (text[end] == '-' || text[end] == '+') {
		end++
	}
	for end < len(text) && text[end] >= '0' && text[end] <= '9' {
		end++
	}
	n, err := strconv.ParseFloat(text[:end], 64)
	if err != nil {
		return 0
	}
	return n
}

// hostname is os.hostname().
func hostname() string {
	name, _ := os.Hostname()
	return name
}
