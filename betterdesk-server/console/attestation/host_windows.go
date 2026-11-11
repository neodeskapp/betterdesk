package attestation

import (
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

var (
	kernel32                 = syscall.NewLazyDLL("kernel32.dll")
	procGlobalMemoryStatusEx = kernel32.NewProc("GlobalMemoryStatusEx")
	procGetSystemTimes       = kernel32.NewProc("GetSystemTimes")
)

// memoryStatusEx is MEMORYSTATUSEX.
type memoryStatusEx struct {
	length               uint32
	memoryLoad           uint32
	totalPhys            uint64
	availPhys            uint64
	totalPageFile        uint64
	availPageFile        uint64
	totalVirtual         uint64
	availVirtual         uint64
	availExtendedVirtual uint64
}

// readHost reads the memory and the processor times from kernel32 and the
// disks from wmic, as Node's os module and serverManagement did on
// Windows. The processor times are those of all processors together,
// counted as one per processor.
func readHost() hostReading {
	in := hostReading{hostname: hostname()}
	var m memoryStatusEx
	m.length = uint32(unsafe.Sizeof(m))
	if ok, _, _ := procGlobalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&m))); ok != 0 {
		in.memTotal, in.free = float64(m.totalPhys), float64(m.availPhys)
	}
	var idle, kernel, user syscall.Filetime
	if ok, _, _ := procGetSystemTimes.Call(uintptr(unsafe.Pointer(&idle)), uintptr(unsafe.Pointer(&kernel)), uintptr(unsafe.Pointer(&user))); ok != 0 {
		n := runtime.NumCPU()
		ticks := func(f syscall.Filetime) float64 {
			return float64(uint64(f.HighDateTime)<<32|uint64(f.LowDateTime)) / float64(n)
		}
		for i := 0; i < n; i++ {
			// the kernel time includes the idle time
			in.cpus = append(in.cpus, cpuTimes{idle: ticks(idle), total: ticks(kernel) + ticks(user)})
		}
	}
	raw := runCommand(8*time.Second, "wmic", "logicaldisk", "where", "DriveType=3", "get", "DeviceID,FileSystem,FreeSpace,Size", "/format:csv")
	for _, line := range strings.Split(raw, "\n") {
		parts := strings.Split(strings.TrimSpace(line), ",")
		if len(parts) < 5 || parts[1] == "" {
			continue
		}
		free, _ := strconv.ParseFloat(parts[3], 64)
		size, _ := strconv.ParseFloat(parts[4], 64)
		if size == 0 {
			continue
		}
		in.disks = append(in.disks, Disk{Size: size, Used: size - free})
	}
	return in
}
