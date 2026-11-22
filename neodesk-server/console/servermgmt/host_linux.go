package servermgmt

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// readHost reads /proc, df and the network interfaces, as Node's os module
// and serverManagement did on Linux.
func readHost() hostReading {
	in := hostReading{hostname: hostname(), platform: "linux", goArch: Arch()}
	if raw, err := os.ReadFile("/proc/stat"); err == nil {
		in.cpus = parseProcStat(string(raw))
	}
	if raw, err := os.ReadFile("/proc/cpuinfo"); err == nil {
		in.cpuModel = parseCPUModel(string(raw))
	}
	if raw, err := os.ReadFile("/proc/meminfo"); err == nil {
		in.memTotal, in.free, in.swap = parseMeminfo(string(raw))
	}
	if raw, err := os.ReadFile("/proc/loadavg"); err == nil {
		in.load = parseLoadavg(string(raw))
	}
	if raw, err := os.ReadFile("/proc/uptime"); err == nil {
		in.uptime = parseUptime(string(raw))
	}
	if raw, err := os.ReadFile("/proc/sys/kernel/osrelease"); err == nil {
		in.release = strings.TrimSpace(string(raw))
	}
	in.disks = parseDF(commandOutput(4*time.Second, "df", "-B1", "--output=target,fstype,size,used,avail"))
	in.network = networkAddresses()
	return in
}

// parseProcStat reads the times of each processor (the lines cpu0, cpu1,
// ...): user + nice + system + idle + irq is the total, as os.cpus()
// counted it.
func parseProcStat(raw string) []cpuTimes {
	var out []cpuTimes
	for _, line := range strings.Split(raw, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 8 {
			continue
		}
		number, isCPU := strings.CutPrefix(fields[0], "cpu")
		if _, err := strconv.Atoi(number); !isCPU || err != nil {
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

// parseCPUModel is the model of the first processor in /proc/cpuinfo, ""
// when it names none.
func parseCPUModel(raw string) string {
	for _, line := range strings.Split(raw, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if ok && strings.TrimSpace(key) == "model name" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// parseLoadavg reads the load of the last 1, 5 and 15 minutes.
func parseLoadavg(raw string) []float64 {
	fields := strings.Fields(raw)
	if len(fields) < 3 {
		return nil
	}
	out := make([]float64, 3)
	for i := range out {
		out[i], _ = strconv.ParseFloat(fields[i], 64)
	}
	return out
}

// parseUptime reads the seconds since the machine started.
func parseUptime(raw string) float64 {
	fields := strings.Fields(raw)
	if len(fields) == 0 {
		return 0
	}
	v, _ := strconv.ParseFloat(fields[0], 64)
	return v
}
