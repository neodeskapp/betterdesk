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
// the test of this name (TestA, or TestA/sub). As in package testing, a
// pattern is cut at its slashes and each level of the name must match the
// level of the pattern, if the pattern has it; -skip leaves out a test
// only when the pattern has no more levels than the name.
func runs(t *testing.T, args []string, name string) bool {
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
	levels := strings.Split(name, "/")
	matches := func(pattern string) (ok, partial bool) {
		pats := strings.Split(pattern, "/")
		for i, level := range levels {
			if i >= len(pats) {
				break
			}
			if !regexp.MustCompile(pats[i]).MatchString(level) {
				return false, false
			}
		}
		return true, len(levels) < len(pats)
	}
	if run != "" {
		if ok, _ := matches(run); !ok {
			return false
		}
	}
	if skip != "" {
		if ok, partial := matches(skip); ok && !partial {
			return false
		}
	}
	return true
}

// runsOnce fails the test unless name runs in exactly one part of pkg.
func runsOnce(t *testing.T, shards [][]shardPart, pkg string, name string) {
	t.Helper()
	n := 0
	for _, s := range shards {
		for _, p := range s {
			if p.pkg != pkg {
				continue
			}
			// A subtest runs where its test runs and it is let through
			top, _, _ := strings.Cut(name, "/")
			if runs(t, p.args(), top) && runs(t, p.args(), name) {
				n++
			}
		}
	}
	if n != 1 {
		t.Errorf("%s runs %d times, want 1", name, n)
	}
}

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
	shards := planShards(times, []string{"./e", "./d", "./c", "./b", "./a", "./new"}, 2, 0)
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
	// first of two equal shards (155). Each shard lists the longest first.
	want := [][]string{{"./a", "./d", "./new", "./e"}, {"./b", "./c"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("layout %v, want %v", got, want)
	}
	if s0, s1 := shardSeconds(shards[0]), shardSeconds(shards[1]); s0 != 155 || s1 != 150 {
		t.Errorf("shard seconds %v and %v, want 155 and 150", s0, s1)
	}
	// One go test for the whole packages, which starts them in that order
	wantRun := append([]string{"test", "-json", "-race", "-p", "4"}, want[0]...)
	if runs := shardRuns(shards[0], []string{"-race"}, 4); !reflect.DeepEqual(runs, [][]string{wantRun}) {
		t.Errorf("go test runs %q, want %q", runs, wantRun)
	}
}

func TestShardRunsShareTheCPUs(t *testing.T) {
	parts := []shardPart{
		{pkg: "./big", seconds: 200, split: true, names: []string{"TestA"}},
		{pkg: "./users", seconds: 150, split: true, test: "TestLong", names: []string{"sqlite"}},
		{pkg: "./a", seconds: 100},
		{pkg: "./b", seconds: 50},
	}
	want := [][]string{
		{"test", "-json", "-race", "-run", "^(TestA)$", "./big"},
		{"test", "-json", "-race", "-run", "^TestLong$/^(sqlite)$", "./users"},
		// Two CPUs are left for the whole packages
		{"test", "-json", "-race", "-p", "2", "./a", "./b"},
	}
	if runs := shardRuns(parts, []string{"-race"}, 4); !reflect.DeepEqual(runs, want) {
		t.Errorf("go test runs %q, want %q", runs, want)
	}
	// Never fewer than one, and a -p of the caller stays
	if runs := shardRuns(parts, nil, 2); runs[2][3] != "1" {
		t.Errorf("whole packages run with %q", runs[2])
	}
	if runs := shardRuns(parts, []string{"-p=8"}, 4); !reflect.DeepEqual(runs[2], []string{"test", "-json", "-p=8", "./a", "./b"}) {
		t.Errorf("whole packages run with %q", runs[2])
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
	shards := planShards(times, []string{"./big", "./small"}, 4, 250)
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
	for _, name := range append(sortedKeys(tests), "TestNew") {
		runsOnce(t, shards, "./big", name)
	}
}

func TestPlanJoinsPartsOfAPackageInOneShard(t *testing.T) {
	tests := map[string]float64{"TestA": 50, "TestB": 40, "TestC": 30, "TestD": 20}
	times := map[string]*pkgTime{"./big": timesOf(140, tests)}
	// Three parts on two shards: two of them share one
	shards := planShards(times, []string{"./big"}, 2, 50)
	for i, s := range shards {
		if len(s) != 1 {
			t.Fatalf("shard %d runs %d parts of ./big, want 1", i+1, len(s))
		}
	}
	for _, name := range []string{"TestA", "TestB", "TestC", "TestD", "TestNew"} {
		runsOnce(t, shards, "./big", name)
	}
}

func TestPlanKeepsThePartsOfAPackageApart(t *testing.T) {
	times := map[string]*pkgTime{
		"./big": timesOf(200, map[string]float64{"TestA": 100, "TestB": 100}),
		"./x":   {Seconds: 150},
	}
	// x 150 | big 100; the second part of big goes beside x, not beside
	// the first one, where it would wait for it
	shards := planShards(times, []string{"./big", "./x"}, 2, 150)
	for i, s := range shards {
		n := 0
		for _, p := range s {
			if p.pkg == "./big" {
				n++
			}
		}
		if n != 1 {
			t.Errorf("shard %d has %d parts of ./big, want 1", i+1, n)
		}
	}
}

func TestPlanCutsALongTestBySubtests(t *testing.T) {
	pt := timesOf(480, map[string]float64{"TestLong": 400, "TestX": 50, "TestY": 30})
	pt.Subtests = map[string]map[string]float64{
		"TestLong": {"sqlite": 100, "sqlite-go": 100, "postgres": 100, "postgres-go": 100},
		"TestX":    {"one": 25, "two": 25},
	}
	shards := planShards(map[string]*pkgTime{"./users": pt}, []string{"./users"}, 3, 250)
	// TestLong in two parts of 200 seconds, the other tests in one of 80
	var seconds []float64
	for _, s := range shards {
		for _, p := range s {
			seconds = append(seconds, p.seconds)
		}
	}
	if !reflect.DeepEqual(seconds, []float64{200, 200, 80}) {
		t.Errorf("parts of %v seconds, want 200, 200 and 80", seconds)
	}
	for _, name := range []string{"TestX", "TestY", "TestNew", "TestX/one",
		"TestLong/sqlite", "TestLong/sqlite-go", "TestLong/postgres", "TestLong/postgres-go", "TestLong/new"} {
		runsOnce(t, shards, "./users", name)
	}
	if got := shards[0][0].args(); !reflect.DeepEqual(got, []string{"-run", `^TestLong$/^(postgres|sqlite)$`}) {
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
	o := newTestOutput(&buf, "example.com/m")
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
	want2 := &pkgTime{Seconds: 1, Tests: map[string]float64{"TestBroken": 0.2}, Subtests: map[string]map[string]float64{"TestBroken": {"sub": 0.1}}}
	if got := o.times["./bad"]; !reflect.DeepEqual(got, want2) {
		t.Errorf("times of ./bad: %+v, want %+v", got, want2)
	}
}

func TestOutputPassesWithoutFailures(t *testing.T) {
	var buf bytes.Buffer
	o := newTestOutput(&buf, "example.com/m")
	o.read(strings.NewReader(strings.SplitAfter(testJSON, `"Elapsed":2.6}`+"\n")[0]))
	if code := o.summary(); code != 0 {
		t.Errorf("exit status %d, want 0:\n%s", code, buf.String())
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
		"go-race-sqlite":   {"./gone": {Seconds: 5}},
		"go-race-postgres": {"./console": {Seconds: 600}},
	}})
	big := func(seconds float64, tests map[string]float64, subs map[string]map[string]float64) *pkgTime {
		return &pkgTime{Seconds: seconds, Tests: tests, Subtests: subs}
	}
	a := write("a.json", &testTimes{Sets: map[string]map[string]*pkgTime{
		"go-race-sqlite": {
			"./big":  big(100, map[string]float64{"TestA": 90}, map[string]map[string]float64{"TestA": {"x": 50, "y": 40}}),
			"./tiny": timesOf(3, map[string]float64{"TestT": 3}),
		},
	}})
	b := write("b.json", &testTimes{Sets: map[string]map[string]*pkgTime{
		"go-race-sqlite": {
			"./big": big(50, map[string]float64{"TestA": 10, "TestB": 40}, map[string]map[string]float64{"TestA": {"x": 10}, "TestB": {"z": 40}}),
		},
	}})
	r := runCommand(testShards, dir, "merge", "--out", out, a, b)
	if r.code != 0 {
		t.Fatalf("merge: %d %s", r.code, r.stderr)
	}
	got, err := readTestTimes(out)
	if err != nil {
		t.Fatal(err)
	}
	// TestB is under a minute: its subtests are left out
	want := &testTimes{About: "kept", Sets: map[string]map[string]*pkgTime{
		"go-race-sqlite": {
			"./big":  big(150, map[string]float64{"TestA": 100, "TestB": 40}, map[string]map[string]float64{"TestA": {"x": 60, "y": 40}}),
			"./tiny": {Seconds: 3},
		},
		"go-race-postgres": {"./console": {Seconds: 600}},
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
