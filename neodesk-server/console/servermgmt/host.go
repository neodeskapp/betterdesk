package servermgmt

import (
	"context"
	"encoding/json"
	"math"
	"net"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/jsbind"
)

// historyMax is how many samples the snapshot's history keeps.
const historyMax = 120

// Host reads the machine the way serverManagement.getResourceSnapshot
// did. The processor load is the share of busy time since the previous
// reading, so a Host keeps the last one (the first reading is 0), and it
// keeps the last historyMax samples. Every reading counts, whoever asks:
// the resources route and the server attestation share one Host.
type Host struct {
	read func() hostReading
	now  func() time.Time

	mu      sync.Mutex
	last    []cpuTimes
	history []Sample
}

// NewHost returns a reader of this machine with its own history.
func NewHost() *Host { return &Host{read: readHost, now: time.Now} }

// cpuTimes are one processor's idle and total time.
type cpuTimes struct{ idle, total float64 }

// hostReading is what the operating system said.
type hostReading struct {
	cpus             []cpuTimes
	cpuModel         string
	memTotal, free   float64
	swap             Swap
	load             []float64
	uptime           float64
	hostname         string
	release          string
	disks            []Disk
	network          []NetAddress
	platform, goArch string
}

// Num is a JavaScript number in JSON: NaN and the infinities are null, as
// JSON.stringify wrote them.
type Num float64

// MarshalJSON writes the number as JSON.stringify did.
func (n Num) MarshalJSON() ([]byte, error) {
	f := float64(n)
	switch {
	case math.IsNaN(f) || math.IsInf(f, 0):
		return []byte("null"), nil
	case f == 0:
		// -0 is written as 0
		return []byte("0"), nil
	}
	return json.Marshal(f)
}

// Memory is the machine's memory in bytes and the share in use in percent
// with one decimal.
type Memory struct {
	Total   Num `json:"total"`
	Free    Num `json:"free"`
	Used    Num `json:"used"`
	Percent Num `json:"percent"`
}

// Swap is the swap space in bytes (Linux only; zero elsewhere).
type Swap struct {
	Total Num `json:"total"`
	Used  Num `json:"used"`
	Free  Num `json:"free"`
}

// Disk is a mounted file system: where, its type, its size, the bytes in
// use and the bytes free.
type Disk struct {
	Mount  string `json:"mount"`
	FSType string `json:"fstype"`
	Size   Num    `json:"size"`
	Used   Num    `json:"used"`
	Avail  Num    `json:"avail"`
}

// NetAddress is an address of a network interface that is not a loopback
// one, as os.networkInterfaces() described it.
type NetAddress struct {
	Name    string `json:"name"`
	Family  string `json:"family"`
	Address string `json:"address"`
	MAC     string `json:"mac"`
	CIDR    string `json:"cidr"`
}

// Sample is one entry of the history: when, the processor and the memory
// in percent.
type Sample struct {
	TS  int64 `json:"ts"`
	CPU Num   `json:"cpu"`
	Mem Num   `json:"mem"`
}

// Snapshot is what GET /api/server-management/resources answers, in the
// order of its keys.
type Snapshot struct {
	TS          int64        `json:"ts"`
	CPU         Num          `json:"cpu"`
	Mem         Memory       `json:"mem"`
	Swap        Swap         `json:"swap"`
	Load        []Num        `json:"load"`
	Uptime      Num          `json:"uptime"`
	NodeUptime  Num          `json:"nodeUptime"`
	CPUCount    int          `json:"cpuCount"`
	CPUModel    string       `json:"cpuModel"`
	Platform    string       `json:"platform"`
	Arch        string       `json:"arch"`
	Hostname    string       `json:"hostname"`
	Release     string       `json:"release"`
	NodeVersion string       `json:"nodeVersion"`
	Disks       []Disk       `json:"disks"`
	Network     []NetAddress `json:"network"`
	History     []Sample     `json:"history"`
}

// Snapshot reads the machine. started is when the process started, for
// nodeUptime.
func (h *Host) Snapshot(started time.Time) Snapshot {
	in := h.read()
	now := h.now()
	h.mu.Lock()
	cpu := cpuPercent(h.last, in.cpus)
	h.last = in.cpus
	snap := Snapshot{
		TS:  now.UnixMilli(),
		CPU: Num(jsRound(cpu*10) / 10),
		Mem: Memory{
			Total: Num(in.memTotal), Free: Num(in.free), Used: Num(in.memTotal - in.free),
			Percent: Num(jsRound(((in.memTotal-in.free)/in.memTotal)*1000) / 10),
		},
		Swap: in.swap,
	}
	h.history = append(h.history, Sample{TS: snap.TS, CPU: snap.CPU, Mem: snap.Mem.Percent})
	if len(h.history) > historyMax {
		h.history = append([]Sample(nil), h.history[len(h.history)-historyMax:]...)
	}
	snap.History = append([]Sample{}, h.history...)
	h.mu.Unlock()

	load := in.load
	if load == nil {
		load = []float64{0, 0, 0}
	}
	for _, l := range load {
		snap.Load = append(snap.Load, Num(l))
	}
	snap.Uptime = Num(in.uptime)
	snap.NodeUptime = Num(now.Sub(started).Seconds())
	snap.CPUCount = len(in.cpus)
	snap.CPUModel = firstSet(in.cpuModel, "unknown")
	if len(in.cpus) == 0 {
		snap.CPUModel = "unknown"
	}
	snap.Platform = in.platform
	snap.Arch = in.goArch
	snap.Hostname = in.hostname
	snap.Release = in.release
	snap.NodeVersion = runtime.Version()
	snap.Disks = nonNil(in.disks)
	snap.Network = nonNil(in.network)
	return snap
}

func nonNil[T any](list []T) []T {
	if list == nil {
		return []T{}
	}
	return list
}

// Snapshot reads the machine through the service's Host.
func (s *Service) Snapshot() Snapshot { return s.host.Snapshot(s.opts.Started) }

// cpuPercent is computeCpuPercent: the busy share of all processors
// between two readings, 0 without a previous one of the same processors.
func cpuPercent(prev, cur []cpuTimes) float64 {
	if prev == nil || len(prev) != len(cur) {
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

// commandOutput is safeExecSync: what the command printed, trimmed, or ""
// when it fails or runs longer than timeout.
func commandOutput(timeout time.Duration, name string, args ...string) string {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).Output()
	if err != nil {
		return ""
	}
	return jsbind.Trim(string(out))
}

// pseudoFileSystems are left out of the disks.
var pseudoFileSystems = map[string]bool{
	"tmpfs": true, "devtmpfs": true, "squashfs": true, "overlay": true, "proc": true, "sysfs": true,
	"cgroup": true, "cgroup2": true, "devpts": true, "debugfs": true, "tracefs": true, "pstore": true,
	"autofs": true, "fusectl": true,
}

// parseDF reads the output of df -B1 --output=target,fstype,size,used,avail
// (the header line first): the file systems of at least 1 MiB that are
// not pseudo file systems.
func parseDF(raw string) []Disk {
	var disks []Disk
	lines := strings.Split(raw, "\n")
	for _, line := range lines[1:] {
		parts := splitSpace(jsbind.Trim(line))
		if len(parts) < 5 {
			continue
		}
		size := parseIntJS(parts[2])
		if pseudoFileSystems[parts[1]] || size == 0 || math.IsNaN(size) || size < 1024*1024 {
			continue
		}
		disks = append(disks, Disk{
			Mount: parts[0], FSType: parts[1], Size: Num(size), Used: Num(parseIntJS(parts[3])), Avail: Num(parseIntJS(parts[4])),
		})
	}
	return disks
}

// parseWMIC reads the output of wmic logicaldisk ... /format:csv: lines
// of Node,DeviceID,FileSystem,FreeSpace,Size with a drive and a size.
func parseWMIC(raw string) []Disk {
	var disks []Disk
	for _, line := range strings.Split(raw, "\n") {
		parts := strings.Split(jsbind.Trim(line), ",")
		if len(parts) < 5 || parts[1] == "" {
			continue
		}
		fstype := parts[2]
		if fstype == "" {
			fstype = "NTFS"
		}
		free, size := orZero(parseIntJS(parts[3])), orZero(parseIntJS(parts[4]))
		if size == 0 {
			continue
		}
		disks = append(disks, Disk{Mount: parts[1], FSType: fstype, Size: Num(size), Used: Num(size - free), Avail: Num(free)})
	}
	return disks
}

// orZero is x || 0 for a number: NaN and -0 are 0.
func orZero(x float64) float64 {
	if math.IsNaN(x) || x == 0 {
		return 0
	}
	return x
}

// splitSpace is text.split(/\s+/) of a text without white space at its
// ends.
func splitSpace(text string) []string {
	return strings.FieldsFunc(text, jsbind.IsSpace)
}

// parseIntJS is parseInt(text, 10): the leading digits after white space
// and a sign, NaN when there are none.
func parseIntJS(text string) float64 {
	text = strings.TrimLeftFunc(text, jsbind.IsSpace)
	end := 0
	if end < len(text) && (text[end] == '-' || text[end] == '+') {
		end++
	}
	digits := end
	for end < len(text) && text[end] >= '0' && text[end] <= '9' {
		end++
	}
	if end == digits {
		return math.NaN()
	}
	n, err := strconv.ParseFloat(text[:end], 64)
	if err != nil {
		return math.NaN()
	}
	return n
}

// parseMeminfo reads the memory, the memory available and the swap space
// in bytes from /proc/meminfo, as os.totalmem(), os.freemem() and
// readSwapInfo did. The swap is the first "SwapTotal:" followed by white
// space and digits anywhere in the text (zero without one) and the first
// such "SwapFree:" (all of it free without one).
func parseMeminfo(raw string) (total, available float64, swap Swap) {
	for _, line := range strings.Split(raw, "\n") {
		key, rest, ok := strings.Cut(line, ":")
		if !ok || key != "MemTotal" && key != "MemAvailable" {
			continue
		}
		kb, err := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(rest), " kB"), 64)
		if err != nil {
			continue
		}
		if key == "MemTotal" {
			total = kb * 1024
		} else {
			available = kb * 1024
		}
	}
	m := swapTotalPattern.FindStringSubmatch(raw)
	if m == nil {
		return total, available, Swap{}
	}
	swapTotal := parseIntJS(m[1]) * 1024
	swapFree := swapTotal
	if f := swapFreePattern.FindStringSubmatch(raw); f != nil {
		swapFree = parseIntJS(f[1]) * 1024
	}
	return total, available, Swap{Total: Num(swapTotal), Used: Num(swapTotal - swapFree), Free: Num(swapFree)}
}

// jsSpace is JavaScript's \s.
const jsSpace = `[\t\n\v\f\r \x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}]`

var (
	swapTotalPattern = regexp.MustCompile(`SwapTotal:` + jsSpace + `+([0-9]+)`)
	swapFreePattern  = regexp.MustCompile(`SwapFree:` + jsSpace + `+([0-9]+)`)
)

// networkAddresses lists the addresses of the interfaces that are up and
// not loopback ones, in the order os.networkInterfaces() gave them:
// interface names that are numbers first, then the others in the order
// the system lists them.
func networkAddresses() []NetAddress {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	type group struct {
		name  string
		addrs []NetAddress
	}
	var groups []group
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagRunning == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		hw := make(net.HardwareAddr, 6)
		copy(hw, iface.HardwareAddr)
		g := group{name: iface.Name}
		for _, addr := range addrs {
			ipnet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}
			family, ip := "IPv6", ipnet.IP
			if v4 := ip.To4(); v4 != nil {
				family, ip = "IPv4", v4
			}
			ones, _ := ipnet.Mask.Size()
			g.addrs = append(g.addrs, NetAddress{
				Name: iface.Name, Family: family, Address: ip.String(), MAC: hw.String(),
				CIDR: ip.String() + "/" + strconv.Itoa(ones),
			})
		}
		if len(g.addrs) > 0 {
			groups = append(groups, g)
		}
	}
	sort.SliceStable(groups, func(a, b int) bool {
		ia, aok := arrayIndex(groups[a].name)
		ib, bok := arrayIndex(groups[b].name)
		if aok && bok {
			return ia < ib
		}
		return aok && !bok
	})
	var out []NetAddress
	for _, g := range groups {
		out = append(out, g.addrs...)
	}
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

// hostname is os.hostname().
func hostname() string {
	name, _ := os.Hostname()
	return name
}
