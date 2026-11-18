package servermgmt

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

func TestParseProcFiles(t *testing.T) {
	total, available, swap := parseMeminfo("MemTotal:       16384 kB\nMemFree:  100 kB\nMemAvailable:   4096 kB\nBuffers: x kB\nSwapTotal: 8 kB\nSwapFree: 2 kB\n")
	if total != 16384*1024 || available != 4096*1024 || swap != (Swap{Total: 8192, Used: 6144, Free: 2048}) {
		t.Errorf("parseMeminfo = %v, %v, %+v", total, available, swap)
	}
	if got := parseCPUModel("processor\t: 0\nvendor_id\t: x\nmodel name\t: Fast CPU @ 3GHz\nmodel name\t: second\n"); got != "Fast CPU @ 3GHz" {
		t.Errorf("model %q", got)
	}
	if got := parseCPUModel("Processor : ARMv7\n"); got != "" {
		t.Errorf("no model name: %q", got)
	}
	if got := parseLoadavg("0.52 0.58 0.59 2/412 12345\n"); !reflect.DeepEqual(got, []float64{0.52, 0.58, 0.59}) {
		t.Errorf("load %v", got)
	}
	if got := parseLoadavg("1 2"); got != nil {
		t.Errorf("short load %v", got)
	}
	if got := parseUptime("350735.47 234388.90\n"); got != 350735.47 {
		t.Errorf("uptime %v", got)
	}
}
