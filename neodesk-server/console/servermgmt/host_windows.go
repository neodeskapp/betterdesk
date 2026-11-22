package servermgmt

import (
	"runtime"
	"strconv"
	"syscall"
	"time"
	"unsafe"
)

var (
	kernel32                 = syscall.NewLazyDLL("kernel32.dll")
	ntdll                    = syscall.NewLazyDLL("ntdll.dll")
	procGlobalMemoryStatusEx = kernel32.NewProc("GlobalMemoryStatusEx")
	procGetSystemTimes       = kernel32.NewProc("GetSystemTimes")
	procGetTickCount64       = kernel32.NewProc("GetTickCount64")
	procRtlGetVersion        = ntdll.NewProc("RtlGetVersion")
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

// osVersionInfo is RTL_OSVERSIONINFOW.
type osVersionInfo struct {
	size, major, minor, build, platform uint32
	csd                                 [128]uint16
}

// readHost reads the memory, the processor times, the uptime and the
// version from the system, the processor's name from the registry and the
// disks from wmic, as Node's os module and serverManagement did on
// Windows. The processor times are those of all processors together,
// counted as one per processor.
func readHost() hostReading {
	in := hostReading{hostname: hostname(), platform: "win32", goArch: Arch()}
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
	in.cpuModel = processorName()
	if ms, _, _ := procGetTickCount64.Call(); ms != 0 {
		in.uptime = float64(ms / 1000)
	}
	v := osVersionInfo{}
	v.size = uint32(unsafe.Sizeof(v))
	if status, _, _ := procRtlGetVersion.Call(uintptr(unsafe.Pointer(&v))); status == 0 {
		in.release = strconv.Itoa(int(v.major)) + "." + strconv.Itoa(int(v.minor)) + "." + strconv.Itoa(int(v.build))
	}
	in.load = []float64{0, 0, 0}
	in.disks = parseWMIC(commandOutput(8*time.Second, "wmic", "logicaldisk", "where", "DriveType=3", "get", "DeviceID,FileSystem,FreeSpace,Size", "/format:csv"))
	in.network = networkAddresses()
	return in
}

// processorName is ProcessorNameString of the first processor.
func processorName() string {
	path, _ := syscall.UTF16PtrFromString(`HARDWARE\DESCRIPTION\System\CentralProcessor\0`)
	var key syscall.Handle
	if syscall.RegOpenKeyEx(syscall.HKEY_LOCAL_MACHINE, path, 0, syscall.KEY_READ, &key) != nil {
		return ""
	}
	defer syscall.RegCloseKey(key)
	name, _ := syscall.UTF16PtrFromString("ProcessorNameString")
	buf := make([]uint16, 256)
	size := uint32(len(buf) * 2)
	var typ uint32
	if syscall.RegQueryValueEx(key, name, nil, &typ, (*byte)(unsafe.Pointer(&buf[0])), &size) != nil {
		return ""
	}
	return syscall.UTF16ToString(buf)
}
