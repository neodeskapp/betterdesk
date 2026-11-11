//go:build !linux && !windows

package attestation

import "runtime"

// readHost reads what the standard library knows on other systems: the
// host's name and the number of processors. Load, memory and disks read
// as zero.
func readHost() hostReading {
	in := hostReading{hostname: hostname()}
	for i := 0; i < runtime.NumCPU(); i++ {
		in.cpus = append(in.cpus, cpuTimes{})
	}
	return in
}
