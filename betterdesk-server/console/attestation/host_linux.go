package attestation

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// readHost reads /proc/stat, /proc/meminfo and df, as Node's os module
// and serverManagement did on Linux.
func readHost() hostReading {
	in := hostReading{hostname: hostname()}
	if raw, err := os.ReadFile("/proc/stat"); err == nil {
		in.cpus = parseProcStat(string(raw))
	}
	if raw, err := os.ReadFile("/proc/meminfo"); err == nil {
		in.memTotal, in.free = parseMeminfo(string(raw))
	}
	in.disks = parseDF(runCommand(4*time.Second, "df", "-B1", "--output=target,fstype,size,used,avail"))
	return in
}

// parseProcStat reads the times of each processor (the lines cpu0,
// cpu1, ...): user + nice + system + idle + irq is the total, as os.cpus()
// counted it.
func parseProcStat(raw string) []cpuTimes {
	var out []cpuTimes
	for _, line := range strings.Split(raw, "\n") {
		fields := strings.Fields(line)
		number, isCPU := strings.CutPrefix(fields0(fields), "cpu")
		if _, err := strconv.Atoi(number); !isCPU || err != nil || len(fields) < 8 {
			continue
		}
		n := func(i int) float64 {
			v, _ := strconv.ParseFloat(fields[i], 64)
			return v
		}
		user, nice, sys, idle, irq := n(1), n(2), n(3), n(4), n(6)
		out = append(out, cpuTimes{idle: idle, total: user + nice + sys + idle + irq})
	}
	return out
}

// fields0 is the first field of a line, "" for an empty one.
func fields0(fields []string) string {
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

// parseMeminfo reads the memory and the memory available in bytes, as
// os.totalmem() and os.freemem() did.
func parseMeminfo(raw string) (total, available float64) {
	for _, line := range strings.Split(raw, "\n") {
		key, rest, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		kb, err := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(rest), " kB"), 64)
		if err != nil {
			continue
		}
		switch key {
		case "MemTotal":
			total = kb * 1024
		case "MemAvailable":
			available = kb * 1024
		}
	}
	return total, available
}
