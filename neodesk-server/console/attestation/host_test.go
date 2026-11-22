package attestation

import (
	"context"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/console/branding"
	"github.com/unitronix/betterdesk-server/console/servermgmt"
)

// The run takes the processor, the memory, the disks, the host's name and
// the number of processors from the server management snapshot; numbers
// the host could not read (NaN) count as 0.
func TestHostSnapshot(t *testing.T) {
	nan := servermgmt.Num(math.NaN())
	read := func() servermgmt.Snapshot {
		return servermgmt.Snapshot{
			CPU: 12.5, Mem: servermgmt.Memory{Total: 3000, Percent: 66.7}, Hostname: "h", CPUCount: 2,
			Disks: []servermgmt.Disk{{Mount: "/", Size: 100, Used: 95}, {Mount: "/x", Size: 10, Used: nan}},
		}
	}
	got := HostSnapshot(read)()
	want := Snapshot{CPU: 12.5, MemTotal: 3000, MemPercent: 66.7, Hostname: "h", CPUCount: 2, Disks: []Disk{{Size: 100, Used: 95}, {Size: 10, Used: 0}}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("HostSnapshot = %+v, want %+v", got, want)
	}
	if maxDiskPercent(got.Disks) != 95 {
		t.Errorf("fullest disk %v", maxDiskPercent(got.Disks))
	}
	empty := HostSnapshot(func() servermgmt.Snapshot { return servermgmt.Snapshot{Mem: servermgmt.Memory{Percent: nan}} })()
	if empty.MemPercent != 0 || empty.Disks == nil {
		t.Errorf("unreadable memory: %+v", empty)
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

// A host reports the processor's load since its reading before, and 0 at
// its first reading. The test reads the host when it starts, so the
// baseline after the wait measures the processor over the wait (BUGS
// SA6).
func TestTheBaselineMeasuresTheProcessor(t *testing.T) {
	readings := 0
	host := func() Snapshot {
		readings++
		if readings == 1 {
			return Snapshot{CPU: 0, MemTotal: 1 << 30, MemPercent: 20, CPUCount: 2, Hostname: "h"}
		}
		return Snapshot{CPU: 40, MemTotal: 1 << 30, MemPercent: 20, CPUCount: 2, Hostname: "h"}
	}
	s := New(Options{
		Settings: newMapSettings(), Clock: &fakeClock{now: time.UnixMilli(1_700_000_000_000)},
		Snapshot: host, Warm: func() { host() }, Dialer: &scriptedDialer{script: []bool{false}},
	})
	var baseline any
	s.onProgress = func() {
		if p, ok := s.progress.Get("baseline"); ok && baseline == nil {
			baseline = p
		}
	}
	if !s.Start() {
		t.Fatal("did not start")
	}
	if _, err := s.execute(context.Background(), <-s.starts); err != nil {
		t.Fatal(err)
	}
	if cpu := branding.Field(baseline, "cpu"); cpu != float64(40) {
		t.Errorf("the baseline's processor load is %v, want 40", cpu)
	}
}
