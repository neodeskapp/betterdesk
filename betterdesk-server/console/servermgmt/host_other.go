//go:build !linux && !windows

package servermgmt

import "runtime"

// readHost reads what the standard library knows on other systems: the
// host's name, the number of processors and the network. Load, memory,
// swap and disks read as zero.
func readHost() hostReading {
	in := hostReading{hostname: hostname(), platform: nodePlatform(runtime.GOOS), goArch: Arch(), load: []float64{0, 0, 0}}
	for i := 0; i < runtime.NumCPU(); i++ {
		in.cpus = append(in.cpus, cpuTimes{})
	}
	in.network = networkAddresses()
	return in
}
