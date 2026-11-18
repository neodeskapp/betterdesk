package servermgmt

import (
	"encoding/json"
	"math"
	"runtime"
	"testing"
	"time"
)

// The processor load is the busy share since the last reading, 0 at the
// first and when the processors changed; the memory is the share in use,
// both with one decimal, and null without a total. Every reading goes
// into the history, which keeps the last 120.
func TestHostSnapshot(t *testing.T) {
	readings := []hostReading{
		{cpus: []cpuTimes{{idle: 100, total: 200}, {idle: 100, total: 200}}, memTotal: 3000, free: 1000, hostname: "h", disks: []Disk{{Mount: "/", Size: 10, Used: 5}}, cpuModel: "Model X"},
		{cpus: []cpuTimes{{idle: 150, total: 300}, {idle: 120, total: 300}}, memTotal: 3000, free: 2999},
		{cpus: []cpuTimes{{idle: 150, total: 300}}, memTotal: 0},
	}
	n := 0
	now := time.UnixMilli(1700000000000)
	h := &Host{read: func() hostReading { r := readings[n%len(readings)]; n++; return r }, now: func() time.Time { return now }}
	started := now.Add(-90 * time.Second)

	first := h.Snapshot(started)
	if first.CPU != 0 || first.Mem.Percent != 66.7 || first.CPUCount != 2 || first.Hostname != "h" || first.Mem.Total != 3000 || first.Mem.Used != 2000 ||
		len(first.Disks) != 1 || first.CPUModel != "Model X" || first.TS != 1700000000000 || first.NodeUptime != 90 || first.NodeVersion != runtime.Version() {
		t.Errorf("first %+v", first)
	}
	if len(first.Load) != 3 || len(first.History) != 1 || first.History[0] != (Sample{TS: 1700000000000, CPU: 0, Mem: 66.7}) {
		t.Errorf("first load %v history %v", first.Load, first.History)
	}
	// busy (200 - 70) of 200
	if second := h.Snapshot(started); second.CPU != 65 || second.Mem.Percent != 0 || second.CPUModel != "unknown" || len(second.History) != 2 {
		t.Errorf("second %+v", second)
	}
	third := h.Snapshot(started)
	if third.CPU != 0 || !math.IsNaN(float64(third.Mem.Percent)) || third.CPUCount != 1 {
		t.Errorf("third %+v", third)
	}
	b, _ := json.Marshal(third.Mem)
	if string(b) != `{"total":0,"free":0,"used":0,"percent":null}` {
		t.Errorf("memory without a total: %s", b)
	}
	for i := 0; i < 200; i++ {
		h.Snapshot(started)
	}
	if got := h.Snapshot(started); len(got.History) != historyMax {
		t.Errorf("history keeps %d", len(got.History))
	}
	if got := cpuPercent([]cpuTimes{{0, 100}}, []cpuTimes{{0, 100}}); got != 0 {
		t.Errorf("no time passed: %v", got)
	}
}

// Numbers go out as JSON.stringify wrote them.
func TestNum(t *testing.T) {
	cases := map[float64]string{math.NaN(): "null", math.Inf(1): "null", math.Inf(-1): "null", math.Copysign(0, -1): "0", 1.5: "1.5", 1e21: "1e+21", 1024: "1024"}
	for in, want := range cases {
		if b, _ := json.Marshal(Num(in)); string(b) != want {
			t.Errorf("Num(%v) = %s, want %s", in, b, want)
		}
	}
}

// A file time is rounded to the millisecond as Node rounded it.
func TestIsoTime(t *testing.T) {
	cases := map[time.Time]string{
		time.Date(2024, 1, 1, 0, 0, 0, 999600000, time.UTC): "2024-01-01T00:00:01.000Z",
		time.Date(2024, 1, 1, 0, 0, 0, 123499900, time.UTC): "2024-01-01T00:00:00.124Z",
		time.Date(2024, 1, 1, 0, 0, 0, 123400000, time.UTC): "2024-01-01T00:00:00.123Z",
	}
	for in, want := range cases {
		if got := isoTime(in); got != want {
			t.Errorf("isoTime(%v) = %s, want %s", in, got, want)
		}
	}
}
