package attestation

import (
	"math"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/servermgmt"
)

// HostSnapshot reads the machine through a server management snapshot
// (serverManagement.getResourceSnapshot, which the run called) and keeps
// the part the run uses. The console passes the snapshot of its one
// servermgmt.Service, so the run and the resources page share the
// processor history as they shared it before. A number the host could not
// read counts as 0.
func HostSnapshot(read func() servermgmt.Snapshot) func() Snapshot {
	return func() Snapshot { return fromHostSnapshot(read()) }
}

// ownHost is the snapshot of a host of the run's own, for a service made
// without one.
func ownHost() func() Snapshot {
	host, started := servermgmt.NewHost(), time.Now()
	return HostSnapshot(func() servermgmt.Snapshot { return host.Snapshot(started) })
}

func fromHostSnapshot(s servermgmt.Snapshot) Snapshot {
	disks := make([]Disk, 0, len(s.Disks))
	for _, d := range s.Disks {
		disks = append(disks, Disk{Size: number(d.Size), Used: number(d.Used)})
	}
	return Snapshot{
		CPU: number(s.CPU), MemTotal: number(s.Mem.Total), MemPercent: number(s.Mem.Percent),
		Disks: disks, Hostname: s.Hostname, CPUCount: s.CPUCount,
	}
}

// number is a reading as the run counts it: NaN is 0.
func number(n servermgmt.Num) float64 {
	if math.IsNaN(float64(n)) {
		return 0
	}
	return float64(n)
}
