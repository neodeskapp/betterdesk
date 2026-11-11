package attestation

import (
	"reflect"
	"testing"
)

func TestParseDF(t *testing.T) {
	raw := "Mounted on Type 1B-blocks Used Avail\n" +
		"/ ext4 100000000000 25000000000 75000000000\n" +
		"/dev/shm tmpfs 8000000000 0 8000000000\n" +
		"/small ext4 1000 10 990\n" +
		"/data xfs 2000000000000 1900000000000 100000000000\n" +
		"/broken ext4 x y z\n" +
		"short line\n"
	got := parseDF(raw)
	want := []Disk{{Size: 1e11, Used: 2.5e10}, {Size: 2e12, Used: 1.9e12}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseDF = %+v, want %+v", got, want)
	}
	if maxDiskPercent(got) != 95 {
		t.Errorf("fullest disk %v", maxDiskPercent(got))
	}
}

// The processor load is the busy share since the last reading, 0 at the
// first and when the processors changed; the memory is the share in use,
// both with one decimal.
func TestHostSnapshot(t *testing.T) {
	readings := []hostReading{
		{cpus: []cpuTimes{{idle: 100, total: 200}, {idle: 100, total: 200}}, memTotal: 3000, free: 1000, hostname: "h", disks: []Disk{{Size: 10, Used: 5}}},
		{cpus: []cpuTimes{{idle: 150, total: 300}, {idle: 120, total: 300}}, memTotal: 3000, free: 2999},
		{cpus: []cpuTimes{{idle: 150, total: 300}}, memTotal: 0},
	}
	n := 0
	h := &Host{read: func() hostReading { r := readings[n]; n++; return r }}
	first := h.Snapshot()
	if first.CPU != 0 || first.MemPercent != 66.7 || first.CPUCount != 2 || first.Hostname != "h" || first.MemTotal != 3000 || len(first.Disks) != 1 {
		t.Errorf("first %+v", first)
	}
	// busy (200 - 70) of 200
	if second := h.Snapshot(); second.CPU != 65 || second.MemPercent != 0 {
		t.Errorf("second %+v", second)
	}
	if third := h.Snapshot(); third.CPU != 0 || third.MemPercent != 0 || third.CPUCount != 1 {
		t.Errorf("third %+v", third)
	}
	if got := cpuPercent([]cpuTimes{{0, 100}}, []cpuTimes{{0, 100}}); got != 0 {
		t.Errorf("no time passed: %v", got)
	}
}

func TestJSRound(t *testing.T) {
	cases := map[float64]float64{0.5: 1, 1.5: 2, 2.5: 3, -0.5: 0, -1.5: -1, 0.49999999999999994: 0, 666.6666: 667}
	for in, want := range cases {
		if got := jsRound(in); got != want {
			t.Errorf("jsRound(%v) = %v, want %v", in, got, want)
		}
	}
}
