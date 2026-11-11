package attestation

import (
	"reflect"
	"testing"
)

const procStat = `cpu  500 0 300 9000 10 5 7 0 0 0
cpu0 200 0 100 4000 5 3 4 0 0 0
cpu1 300 0 200 5000 5 2 3 0 0 0
intr 12345
ctxt 999
cpufreq 1 2 3 4 5 6 7
`

func TestParseProcStat(t *testing.T) {
	got := parseProcStat(procStat)
	want := []cpuTimes{{idle: 4000, total: 200 + 100 + 4000 + 3}, {idle: 5000, total: 300 + 200 + 5000 + 2}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseProcStat = %+v, want %+v", got, want)
	}
}

func TestParseMeminfo(t *testing.T) {
	total, available := parseMeminfo("MemTotal:       16384 kB\nMemFree:  100 kB\nMemAvailable:   4096 kB\nBuffers: x kB\n")
	if total != 16384*1024 || available != 4096*1024 {
		t.Errorf("parseMeminfo = %v, %v", total, available)
	}
}
