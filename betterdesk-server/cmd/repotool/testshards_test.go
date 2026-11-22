package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// runs tells whether go test, called with a part's -run and -skip, runs
// the test path (TestA, or TestCases/devices for a test cut with under).
func runs(t *testing.T, args []string, test string) bool {
	t.Helper()
	run, skip := "", ""
	for i := 0; i < len(args); i += 2 {
		switch args[i] {
		case "-run":
			run = args[i+1]
		case "-skip":
			skip = args[i+1]
		default:
			t.Fatalf("unexpected argument %q", args[i])
		}
	}
	// Each level of a pattern matches the same level of the name
	matches := func(pattern string) bool {
		levels := strings.Split(pattern, "/")
		names := strings.Split(test, "/")
		for i, level := range levels {
			if i >= len(names) {
				return true
			}
			if !regexp.MustCompile(level).MatchString(names[i]) {
				return false
			}
		}
		return true
	}
	if run != "" && !matches(run) {
		return false
	}
	return skip == "" || !matches(skip)
}

// timesOf makes a package's times from test seconds.
func timesOf(seconds float64, tests map[string]float64) *pkgTime {
	return &pkgTime{Seconds: seconds, Tests: tests}
}

func TestPlanDealsLongestFirstToTheEmptiestShard(t *testing.T) {
	times := map[string]*pkgTime{
		"./a": {Seconds: 100},
		"./b": {Seconds: 90},
		"./c": {Seconds: 60},
		"./d": {Seconds: 40},
		"./e": {Seconds: 5},
	}
	shards := planShards(times, []string{"./e", "./d", "./c", "./b", "./a", "./new"}, planOptions{shards: 2})
	got := make([][]string, len(shards))
	for i, parts := range shards {
		for _, p := range parts {
			if p.args() != nil {
				t.Errorf("%s was cut without --split-over", p.pkg)
			}
			got[i] = append(got[i], p.pkg)
		}
	}
	// a 100 | b 90; c to b (150); d to a (140); new to a (150); e to the
	// first of two equal shards (155)
	want := [][]string{{"./a", "./d", "./e", "./new"}, {"./b", "./c"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("layout %v, want %v", got, want)
	}
	if s0, s1 := shardSeconds(shards[0]), shardSeconds(shards[1]); s0 != 155 || s1 != 150 {
		t.Errorf("shard seconds %v and %v, want 155 and 150", s0, s1)
	}
}

func TestPlanCutsALongPackageAndRunsEveryTestOnce(t *testing.T) {
	tests := map[string]float64{}
	for i := 0; i < 40; i++ {
		tests[fmt.Sprintf("Test%02d", i)] = float64(i + 1)
	}
	sum := 0.0
	for _, s := range tests {
		sum += s
	}
	times := map[string]*pkgTime{
		"./big":   timesOf(sum, tests),
		"./small": {Seconds: 30},
	}
	shards := planShards(times, []string{"./big", "./small"}, planOptions{shards: 4, splitOver: 250})
	// 820 seconds over 250 is four parts, one per shard
	parts := 0
	for _, s := range shards {
		for _, p := range s {
			if p.pkg == "./big" {
				parts++
				if p.seconds > 260 {
					t.Errorf("a part of ./big takes %.0f seconds, more than the 250 it was cut for", p.seconds)
				}
			}
		}
	}
	if parts != 4 {
		t.Fatalf("./big in %d parts, want 4", parts)
	}
	// Every measured test and a test the times do not know yet run once
	names := append(sortedKeys(tests), "TestNew")
	for _, name := range names {
		n := 0
		for _, s := range shards {
			for _, p := range s {
				if p.pkg == "./big" && runs(t, p.args(), name) {
					n++
				}
			}
		}
		if n != 1 {
			t.Errorf("%s runs %d times, want 1", name, n)
		}
	}
}

func TestPlanJoinsPartsOfAPackageInOneShard(t *testing.T) {
	tests := map[string]float64{"TestA": 50, "TestB": 40, "TestC": 30, "TestD": 20}
	times := map[string]*pkgTime{"./big": timesOf(140, tests)}
	// Three parts on two shards: two of them share one
	shards := planShards(times, []string{"./big"}, planOptions{shards: 2, splitOver: 50})
	for i, s := range shards {
		if len(s) != 1 {
			t.Fatalf("shard %d runs %d parts of ./big, want 1", i+1, len(s))
		}
	}
	for _, name := range []string{"TestA", "TestB", "TestC", "TestD", "TestNew"} {
		n := 0
		for _, s := range shards {
			if runs(t, s[0].args(), name) {
				n++
			}
		}
		if n != 1 {
			t.Errorf("%s runs %d times, want 1", name, n)
		}
	}
}

func TestPlanUnderCutsTheSubtestsOfOneTest(t *testing.T) {
	groups := map[string]float64{"devices": 300, "users": 200, "auth-basics": 150, "i18n": 100, "phase4-5": 50}
	times := map[string]*pkgTime{"./console/contract": timesOf(800, groups)}
	shards := planShards(times, []string{"./console/contract"}, planOptions{shards: 3, under: "TestCases"})
	for i, s := range shards {
		if len(s) != 1 {
			t.Fatalf("shard %d has %d parts, want 1", i+1, len(s))
		}
		for _, other := range []string{"TestMatrix", "TestEveryRouteIsCovered"} {
			if runs(t, s[0].args(), other) {
				t.Errorf("shard %d runs %s", i+1, other)
			}
		}
	}
	for _, g := range append(sortedKeys(groups), "new-group") {
		n := 0
		for _, s := range shards {
			if runs(t, s[0].args(), "TestCases/"+g) {
				n++
			}
		}
		if n != 1 {
			t.Errorf("group %s runs on %d shards, want 1", g, n)
		}
	}
	if got := shards[0][0].args(); !reflect.DeepEqual(got, []string{"-run", `^TestCases$/^(devices)$`}) {
		t.Errorf("shard 1 runs %q", got)
	}
}

func sortedKeys(m map[string]float64) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// test2json lines of a run with a passing and a failing package.
const testJSON = `{"Action":"start","Package":"example.com/m/ok"}
{"Action":"run","Package":"example.com/m/ok","Test":"TestFine"}
{"Action":"output","Package":"example.com/m/ok","Test":"TestFine","Output":"=== RUN   TestFine\n"}
{"Action":"output","Package":"example.com/m/ok","Test":"TestFine","Output":"    fine_test.go:9: quiet\n"}
{"Action":"output","Package":"example.com/m/ok","Test":"TestFine","Output":"--- PASS: TestFine (2.50s)\n"}
{"Action":"pass","Package":"example.com/m/ok","Test":"TestFine","Elapsed":2.5}
{"Action":"output","Package":"example.com/m/ok","Output":"PASS\n"}
{"Action":"output","Package":"example.com/m/ok","Output":"ok  \texample.com/m/ok\t2.6s\n"}
{"Action":"pass","Package":"example.com/m/ok","Elapsed":2.6}
{"Action":"run","Package":"example.com/m/bad","Test":"TestBroken"}
{"Action":"output","Package":"example.com/m/bad","Test":"TestBroken","Output":"=== RUN   TestBroken\n"}
{"Action":"run","Package":"example.com/m/bad","Test":"TestBroken/sub"}
{"Action":"output","Package":"example.com/m/bad","Test":"TestBroken/sub","Output":"    bad_test.go:12: got 1, want 2\n"}
{"Action":"output","Package":"example.com/m/bad","Test":"TestBroken/sub","Output":"--- FAIL: TestBroken/sub (0.10s)\n"}
{"Action":"fail","Package":"example.com/m/bad","Test":"TestBroken/sub","Elapsed":0.1}
{"Action":"output","Package":"example.com/m/bad","Test":"TestBroken","Output":"--- FAIL: TestBroken (0.20s)\n"}
{"Action":"fail","Package":"example.com/m/bad","Test":"TestBroken","Elapsed":0.2}
{"Action":"run","Package":"example.com/m/bad","Test":"TestHangs"}
{"Action":"output","Package":"example.com/m/bad","Test":"TestHangs","Output":"=== RUN   TestHangs\n"}
{"Action":"output","Package":"example.com/m/bad","Output":"panic: test timed out after 1s\n"}
{"Action":"output","Package":"example.com/m/bad","Output":"FAIL\texample.com/m/bad\t1.0s\n"}
{"Action":"fail","Package":"example.com/m/bad","Elapsed":1.0}
`

func TestOutputShowsFailuresAndKeepsTimes(t *testing.T) {
	var buf bytes.Buffer
	o := newTestOutput(&buf, "example.com/m", "")
	o.read(strings.NewReader(testJSON))
	code := o.summary()
	if code != 1 {
		t.Errorf("exit status %d, want 1", code)
	}
	want := "ok  \texample.com/m/ok\t2.6s\n" +
		"    bad_test.go:12: got 1, want 2\n" +
		"--- FAIL: TestBroken/sub (0.10s)\n" +
		"=== RUN   TestBroken\n" +
		"--- FAIL: TestBroken (0.20s)\n" +
		"panic: test timed out after 1s\n" +
		"FAIL\texample.com/m/bad\t1.0s\n" +
		"=== RUN   TestHangs\n" +
		"\n1 packages passed, 1 failed\n" +
		"FAIL ./bad\n" +
		"--- FAIL ./bad TestBroken/sub\n"
	if got := buf.String(); got != want {
		t.Errorf("output:\n%s\nwant:\n%s", got, want)
	}
	if got := o.times["./ok"]; got == nil || got.Seconds != 2.6 || got.Tests["TestFine"] != 2.5 {
		t.Errorf("times of ./ok: %+v", got)
	}
	if got := o.times["./bad"]; got == nil || len(got.Tests) != 1 || got.Tests["TestBroken"] != 0.2 {
		t.Errorf("times of ./bad: %+v, want only the top-level test", got)
	}
}

func TestOutputPassesWithoutFailures(t *testing.T) {
	var buf bytes.Buffer
	o := newTestOutput(&buf, "example.com/m", "")
	o.read(strings.NewReader(strings.SplitAfter(testJSON, `"Elapsed":2.6}`+"\n")[0]))
	if code := o.summary(); code != 0 {
		t.Errorf("exit status %d, want 0:\n%s", code, buf.String())
	}
}

func TestOutputUnderKeepsTheGroups(t *testing.T) {
	lines := `{"Action":"pass","Package":"example.com/m/contract","Test":"TestCases/devices/admin","Elapsed":3}
{"Action":"pass","Package":"example.com/m/contract","Test":"TestCases/devices","Elapsed":12}
{"Action":"pass","Package":"example.com/m/contract","Test":"TestCases/users","Elapsed":7}
{"Action":"pass","Package":"example.com/m/contract","Test":"TestCases","Elapsed":19}
{"Action":"pass","Package":"example.com/m/contract","Elapsed":20}
`
	o := newTestOutput(&bytes.Buffer{}, "example.com/m", "TestCases")
	o.read(strings.NewReader(lines))
	want := map[string]float64{"devices": 12, "users": 7}
	if got := o.times["./contract"]; got == nil || !reflect.DeepEqual(got.Tests, want) || got.Seconds != 20 {
		t.Errorf("times %+v, want tests %v", got, want)
	}
}

func TestMergeAddsPartsAndReplacesSets(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, tt *testTimes) string {
		path := filepath.Join(dir, name)
		if err := writeTestTimes(path, tt); err != nil {
			t.Fatal(err)
		}
		return path
	}
	out := write("times.json", &testTimes{About: "kept", Sets: map[string]map[string]*pkgTime{
		"go-race-sqlite": {"./gone": {Seconds: 5}},
		"contract-cases": {"./console/contract": {Seconds: 600}},
	}})
	a := write("a.json", &testTimes{Sets: map[string]map[string]*pkgTime{
		"go-race-sqlite": {"./big": timesOf(100, map[string]float64{"TestA": 90}), "./tiny": timesOf(3, map[string]float64{"TestT": 3})},
	}})
	b := write("b.json", &testTimes{Sets: map[string]map[string]*pkgTime{
		"go-race-sqlite": {"./big": timesOf(50, map[string]float64{"TestB": 45})},
	}})
	r := runCommand(testShards, dir, "merge", "--out", out, a, b)
	if r.code != 0 {
		t.Fatalf("merge: %d %s", r.code, r.stderr)
	}
	got, err := readTestTimes(out)
	if err != nil {
		t.Fatal(err)
	}
	want := &testTimes{About: "kept", Sets: map[string]map[string]*pkgTime{
		"go-race-sqlite": {
			"./big":  timesOf(150, map[string]float64{"TestA": 90, "TestB": 45}),
			"./tiny": {Seconds: 3},
		},
		"contract-cases": {"./console/contract": {Seconds: 600}},
	}}
	if !reflect.DeepEqual(got, want) {
		gotJSON, _ := os.ReadFile(out)
		t.Errorf("merged:\n%s", gotJSON)
	}
}

func TestShardFlagsRefuseRunAndSkip(t *testing.T) {
	r := runCommand(testShards, t.TempDir(), "run", "--set", "x", "--shards", "2", "--shard", "1", "--", "-race", "-run", "TestA")
	if r.code != 2 || !strings.Contains(r.stderr, "set -run, -skip and -json themselves") {
		t.Errorf("got %d %q", r.code, r.stderr)
	}
	r = runCommand(testShards, t.TempDir(), "run", "--set", "x", "--shards", "2", "--shard", "3")
	if r.code != 2 || !strings.Contains(r.stderr, "--shard must be between 1 and 2") {
		t.Errorf("got %d %q", r.code, r.stderr)
	}
}
