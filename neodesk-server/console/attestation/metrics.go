package attestation

import (
	"context"
	"math"

	"github.com/neodeskapp/neodesk/neodesk-server/console/branding"
)

// Snapshot is what the run reads of the host (the part of
// serverManagement.getResourceSnapshot it uses).
type Snapshot struct {
	// CPU is the share of the processors that was busy since the last
	// snapshot, in percent with one decimal; 0 at the first.
	CPU float64
	// MemTotal is the memory in bytes and MemPercent the share in use, in
	// percent with one decimal.
	MemTotal   float64
	MemPercent float64
	// Disks are the mounted file systems.
	Disks []Disk
	// Hostname is the host's name and CPUCount the number of processors.
	Hostname string
	CPUCount int
}

// Disk is the size of a file system and the bytes in use, as df counts
// them.
type Disk struct {
	Size, Used float64
}

// tierThresholds are the tiers by the number of connections the host
// carried, highest first.
var tierThresholds = []struct {
	tier string
	min  float64
}{
	{"obsidian", 401},
	{"titanium", 201},
	{"platinum", 76},
	{"iron", 26},
	{"bronze", 1},
}

// DetermineTier is the tier for a number of connections (determineTier):
// nil for none.
func DetermineTier(connections float64) any {
	if math.IsNaN(connections) {
		connections = 0
	}
	n := math.Max(0, math.Floor(connections))
	if n == 0 {
		return nil
	}
	for _, t := range tierThresholds {
		if n >= t.min {
			return t.tier
		}
	}
	return "bronze"
}

// maxDiskPercent is the fullest file system in percent with one decimal
// (getMaxDiskPercent). File systems of size 0 are left out.
func maxDiskPercent(disks []Disk) float64 {
	highest := 0.0
	for _, d := range disks {
		if d.Size == 0 || math.IsNaN(d.Size) {
			continue
		}
		if pct := (d.Used / d.Size) * 100; pct > highest {
			highest = pct
		}
	}
	return jsRound(highest*10) / 10
}

// jsRound is Math.round: the nearest integer, halves up.
func jsRound(x float64) float64 {
	if math.IsNaN(x) || math.IsInf(x, 0) {
		return x
	}
	r := math.Floor(x)
	if x-r >= 0.5 {
		r++
	}
	return r
}

// metrics is one reading the run took (collectMetrics).
type metrics struct {
	ts          int64
	cpu         float64
	memPercent  float64
	diskPercent float64
	hostname    string
	cpuCount    int
	memTotal    float64
	// goStats is the data of the API server's statistics, nil when the
	// call failed
	goStats any
}

// object is the reading as the result and the progress hold it.
func (m *metrics) object() *branding.Object {
	o := branding.NewObject()
	o.Set("ts", float64(m.ts))
	o.Set("cpu", m.cpu)
	o.Set("memPercent", m.memPercent)
	o.Set("diskPercent", m.diskPercent)
	o.Set("hostname", m.hostname)
	o.Set("cpuCount", float64(m.cpuCount))
	o.Set("memTotal", m.memTotal)
	o.Set("goStats", m.goStats)
	return o
}

// fromSnapshot is metricsFromSnapshot: a reading without the API's
// statistics.
func fromSnapshot(snap Snapshot, now int64) *metrics {
	return &metrics{
		ts:          now,
		cpu:         snap.CPU,
		memPercent:  snap.MemPercent,
		diskPercent: maxDiskPercent(snap.Disks),
		hostname:    snap.Hostname,
		cpuCount:    snap.CPUCount,
		memTotal:    snap.MemTotal,
	}
}

// collectMetrics reads the host, then the API server's statistics.
func (s *Service) collectMetrics(ctx context.Context) *metrics {
	snap := s.opts.Snapshot()
	m := fromSnapshot(snap, s.nowMillis())
	m.goStats = s.opts.ServerStats(ctx)
	return m
}

// estimateCapacity guesses the connections the host would carry from the
// headroom the baseline left (estimateCapacity), for a run that could not
// put load on the server.
func estimateCapacity(baseline *metrics, snap *metrics) float64 {
	var cpuBase, memBase, diskBase float64
	if baseline != nil {
		cpuBase, memBase, diskBase = baseline.cpu, baseline.memPercent, baseline.diskPercent
	}
	ignoreDisk := diskBase >= 75
	cpuHead := jsMax(0, Threshold-cpuBase)
	memHead := jsMax(0, Threshold-memBase)
	diskHead := 100.0
	if !ignoreDisk {
		diskHead = jsMax(0, Threshold-diskBase)
	}
	totalMem := 0.0
	cpuCount := 0
	if snap != nil {
		totalMem, cpuCount = snap.memTotal, snap.cpuCount
	}
	fromCPU := math.Floor(cpuHead / 0.35)
	var fromMem float64
	if totalMem > 0 {
		fromMem = math.Floor((memHead / 100) * totalMem / (8 * 1024 * 1024))
	} else {
		fromMem = math.Floor(memHead / 2)
	}
	fromDisk := math.Floor(diskHead / 0.5)
	if ignoreDisk {
		fromDisk = fromMem
	}
	if cpuCount == 0 {
		cpuCount = 1
	}
	coreBased := jsMax(1, float64(cpuCount)) * 12
	return jsMax(0, jsMin(jsMin(fromCPU, fromMem), jsMin(fromDisk, coreBased*4)))
}

// jsMax and jsMin are Math.max and Math.min of two numbers: NaN wins.
func jsMax(a, b float64) float64 {
	if math.IsNaN(a) || math.IsNaN(b) {
		return math.NaN()
	}
	return math.Max(a, b)
}

func jsMin(a, b float64) float64 {
	if math.IsNaN(a) || math.IsNaN(b) {
		return math.NaN()
	}
	return math.Min(a, b)
}

// overThreshold says the host is at 80 percent of a processor, memory or
// disk (isOverThreshold). The disk does not count when it was at 75
// percent before the test.
func overThreshold(m *metrics, baseline *metrics) bool {
	if m.cpu >= Threshold || m.memPercent >= Threshold {
		return true
	}
	if baseline != nil && baseline.diskPercent >= 75 {
		return false
	}
	return m.diskPercent >= Threshold
}

// calm says the host is below 70 percent again (isUnderStabilize).
func calm(m *metrics, baseline *metrics) bool {
	if m.cpu >= stabilizeThreshold || m.memPercent >= stabilizeThreshold {
		return false
	}
	if baseline != nil && baseline.diskPercent >= 75 {
		return true
	}
	return m.diskPercent < stabilizeThreshold
}
