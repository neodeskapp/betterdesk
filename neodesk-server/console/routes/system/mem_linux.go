package system

import (
	"os"
	"strconv"
	"strings"
)

// totalMemory is the machine's memory in bytes, from /proc/meminfo.
func totalMemory() uint64 {
	raw, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if rest, ok := strings.CutPrefix(line, "MemTotal:"); ok {
			kb, _ := strconv.ParseUint(strings.TrimSuffix(strings.TrimSpace(rest), " kB"), 10, 64)
			return kb * 1024
		}
	}
	return 0
}

// residentBytes is the memory the process holds in RAM, from
// /proc/self/statm, 0 when that cannot be read.
func residentBytes() uint64 {
	raw, err := os.ReadFile("/proc/self/statm")
	if err != nil {
		return 0
	}
	parts := strings.Fields(string(raw))
	if len(parts) < 2 {
		return 0
	}
	pages, _ := strconv.ParseUint(parts[1], 10, 64)
	return pages * uint64(os.Getpagesize())
}
