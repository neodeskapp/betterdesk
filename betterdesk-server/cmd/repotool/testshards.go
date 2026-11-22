package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// testTimesFile is where the measured test times live, from the root.
const testTimesFile = ".github/test-times.json"

// Packages and tests the times file does not know count this long.
const unknownPackageSeconds = 10.0

// The times file keeps the test times of packages that take at least this
// long. Only those can be split, and the file stays small.
const keepTestsOver = 30.0

// testShards is "test-shards": it deals the tests of a module over CI
// runners by the times they took before, runs one runner's share and
// keeps the times of this run for the next layout.
//
//	test-shards plan  --set NAME --shards N [--split-over S] [--under TEST] [--packages P,...] [-- go test flags]
//	test-shards run   --set NAME --shards N --shard K [--split-over S] [--under TEST] [--packages P,...] [--times-out FILE] -- go test flags
//	test-shards merge --out FILE TIMES...
//
// The times are in .github/test-times.json: per set (a database, a suite)
// the seconds of each package and, for packages of 30 seconds and more,
// of each top-level test. Packages go to the runner with the least time
// so far, the longest first. A package longer than --split-over is cut
// into parts by its top-level tests; the last part runs every test the
// times do not name, so a new test always runs once. With --under TEST
// the one package is cut into one part per runner by the subtests of
// TEST (the contract suite's case groups).
//
// run starts one go test -json per part and one for the whole packages,
// side by side in --dir (default betterdesk-server), prints the results
// of the packages and the output of failed tests, and writes the times of
// this run to --times-out. merge adds such files up into a times file.
func testShards(root string, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: repotool test-shards plan|run|merge [flags]")
		return 2
	}
	switch args[0] {
	case "plan", "run":
		return testShardsPlanOrRun(root, args[0], args[1:], stdout, stderr)
	case "merge":
		return testShardsMerge(root, args[1:], stdout, stderr)
	}
	fmt.Fprintf(stderr, "repotool test-shards: unknown action %q (plan, run or merge)\n", args[0])
	return 2
}

// testTimes is the times file: per set, the packages by name.
type testTimes struct {
	About string                         `json:"about,omitempty"`
	Sets  map[string]map[string]*pkgTime `json:"sets"`
}

// pkgTime is what one package took: all of it, and its top-level tests
// (or, for a set cut with --under, the subtests of that test).
type pkgTime struct {
	Seconds float64            `json:"seconds"`
	Tests   map[string]float64 `json:"tests,omitempty"`
}

func readTestTimes(path string) (*testTimes, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var t testTimes
	if err := json.Unmarshal(data, &t); err != nil {
		return nil, fmt.Errorf("%s: %v", path, err)
	}
	if t.Sets == nil {
		t.Sets = map[string]map[string]*pkgTime{}
	}
	return &t, nil
}

func writeTestTimes(path string, t *testTimes) error {
	for _, set := range t.Sets {
		for _, p := range set {
			p.Seconds = round1(p.Seconds)
			for name, s := range p.Tests {
				p.Tests[name] = round1(s)
			}
		}
	}
	data, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

func round1(s float64) float64 { return math.Round(s*10) / 10 }

// shardPart is one go test run of a shard: a package, or the share of a
// package the times file cut into parts.
type shardPart struct {
	pkg     string
	seconds float64
	// names are the tests of this part. The rest part runs everything
	// but the names of the package's other parts (others).
	names  []string
	rest   bool
	others []string
	split  bool
	under  string
}

// args are go test's -run and -skip for the part; none for a whole package.
func (p shardPart) args() []string {
	if !p.split {
		return nil
	}
	prefix := ""
	if p.under != "" {
		prefix = "^" + regexp.QuoteMeta(p.under) + "$/"
	}
	if !p.rest {
		return []string{"-run", prefix + namesPattern(p.names)}
	}
	var args []string
	if p.under != "" {
		args = append(args, "-run", "^"+regexp.QuoteMeta(p.under)+"$")
	}
	if len(p.others) > 0 {
		args = append(args, "-skip", prefix+namesPattern(p.others))
	}
	return args
}

func namesPattern(names []string) string {
	quoted := make([]string, len(names))
	for i, n := range names {
		quoted[i] = regexp.QuoteMeta(n)
	}
	sort.Strings(quoted)
	return "^(" + strings.Join(quoted, "|") + ")$"
}

// splitPackage cuts a package into k parts by its tests, the longest test
// first into the part with the least time so far. The part with the least
// time is the rest part. A part's seconds are the package's seconds in the
// share of its tests.
func splitPackage(pkg string, pt *pkgTime, k int, under string) []shardPart {
	names := make([]string, 0, len(pt.Tests))
	total := 0.0
	for n, s := range pt.Tests {
		names = append(names, n)
		total += s
	}
	sort.Slice(names, func(i, j int) bool {
		a, b := pt.Tests[names[i]], pt.Tests[names[j]]
		if a != b {
			return a > b
		}
		return names[i] < names[j]
	})
	bins := make([][]string, k)
	loads := make([]float64, k)
	for _, n := range names {
		best := 0
		for b := 1; b < k; b++ {
			if loads[b] < loads[best] {
				best = b
			}
		}
		bins[best] = append(bins[best], n)
		loads[best] += pt.Tests[n]
	}
	rest := 0
	for b := 1; b < k; b++ {
		if loads[b] <= loads[rest] {
			rest = b
		}
	}
	var parts []shardPart
	for b := 0; b < k; b++ {
		if b != rest && len(bins[b]) == 0 {
			continue
		}
		share := 1.0 / float64(k)
		if total > 0 {
			share = loads[b] / total
		}
		part := shardPart{pkg: pkg, seconds: pt.Seconds * share, names: bins[b], rest: b == rest, split: true, under: under}
		if part.rest {
			for o := 0; o < k; o++ {
				if o != rest {
					part.others = append(part.others, bins[o]...)
				}
			}
			sort.Strings(part.others)
			// The rest part also stands for the tests that are not measured yet
			if part.seconds < unknownPackageSeconds {
				part.seconds = unknownPackageSeconds
			}
		}
		parts = append(parts, part)
	}
	return parts
}

// planOptions shape a layout.
type planOptions struct {
	shards int
	// splitOver cuts a package longer than this many seconds; 0 never does.
	splitOver float64
	// under cuts every package into one part per shard by the subtests of
	// this top-level test.
	under string
}

// planShards deals the packages over the shards: the longest part first,
// each to the shard with the least time so far.
func planShards(times map[string]*pkgTime, pkgs []string, o planOptions) [][]shardPart {
	var parts []shardPart
	for _, pkg := range pkgs {
		pt := times[pkg]
		if pt == nil {
			pt = &pkgTime{Seconds: unknownPackageSeconds}
		}
		k := 1
		switch {
		case o.under != "":
			k = o.shards
		case o.splitOver > 0 && pt.Seconds > o.splitOver && len(pt.Tests) > 1:
			k = int(math.Ceil(pt.Seconds / o.splitOver))
			if k > len(pt.Tests) {
				k = len(pt.Tests)
			}
		}
		if k <= 1 && o.under == "" {
			parts = append(parts, shardPart{pkg: pkg, seconds: pt.Seconds})
			continue
		}
		parts = append(parts, splitPackage(pkg, pt, k, o.under)...)
	}
	sort.SliceStable(parts, func(i, j int) bool {
		if parts[i].seconds != parts[j].seconds {
			return parts[i].seconds > parts[j].seconds
		}
		return parts[i].pkg < parts[j].pkg
	})
	shards := make([][]shardPart, o.shards)
	loads := make([]float64, o.shards)
	for _, p := range parts {
		best := 0
		for s := 1; s < o.shards; s++ {
			if loads[s] < loads[best] {
				best = s
			}
		}
		shards[best] = append(shards[best], p)
		loads[best] += p.seconds
	}
	for s := range shards {
		shards[s] = joinParts(shards[s])
	}
	return shards
}

// joinParts makes one part of the parts of a package that landed in the
// same shard, so its test binary starts once.
func joinParts(parts []shardPart) []shardPart {
	var out []shardPart
	index := map[string]int{}
	for _, p := range parts {
		i, seen := index[p.pkg]
		if !seen || !p.split {
			index[p.pkg] = len(out)
			out = append(out, p)
			continue
		}
		q := &out[i]
		q.seconds += p.seconds
		switch {
		case q.rest || p.rest:
			mine := map[string]bool{}
			if !q.rest {
				q.others = p.others
			}
			for _, n := range append(append([]string{}, q.names...), p.names...) {
				mine[n] = true
			}
			var others []string
			for _, n := range q.others {
				if !mine[n] {
					others = append(others, n)
				}
			}
			q.others, q.names, q.rest = others, nil, true
		default:
			q.names = append(q.names, p.names...)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].pkg < out[j].pkg })
	return out
}

func shardSeconds(parts []shardPart) float64 {
	s := 0.0
	for _, p := range parts {
		s += p.seconds
	}
	return s
}

// shardFlags are the flags plan and run share.
type shardFlags struct {
	times, set, dir, packages, under, timesOut string
	shards, shard                              int
	splitOver                                  float64
	goFlags                                    []string
}

func parseShardFlags(action string, args []string, stderr io.Writer) (*shardFlags, bool) {
	fs := flag.NewFlagSet("test-shards "+action, flag.ContinueOnError)
	fs.SetOutput(stderr)
	f := &shardFlags{}
	fs.StringVar(&f.times, "times", testTimesFile, "the times file, from the repository's root")
	fs.StringVar(&f.set, "set", "", "the set of times to use (go-race-sqlite, contract-cases, ...)")
	fs.StringVar(&f.dir, "dir", "betterdesk-server", "the module to test, from the repository's root")
	fs.StringVar(&f.packages, "packages", "", "comma-separated packages (default: every package of the module that has tests)")
	fs.StringVar(&f.under, "under", "", "cut by the subtests of this top-level test, one part per shard")
	fs.StringVar(&f.timesOut, "times-out", "", "write the times of this run to this file")
	fs.IntVar(&f.shards, "shards", 0, "the number of shards")
	fs.IntVar(&f.shard, "shard", 0, "the shard to run, from 1")
	fs.Float64Var(&f.splitOver, "split-over", 0, "cut a package that takes longer than this many seconds")
	if err := fs.Parse(args); err != nil {
		return nil, false
	}
	f.goFlags = fs.Args()
	switch {
	case f.set == "":
		fmt.Fprintf(stderr, "test-shards %s: --set is required\n", action)
	case f.shards < 1:
		fmt.Fprintf(stderr, "test-shards %s: --shards must be 1 or more\n", action)
	case action == "run" && (f.shard < 1 || f.shard > f.shards):
		fmt.Fprintf(stderr, "test-shards run: --shard must be between 1 and %d\n", f.shards)
	default:
		for _, a := range f.goFlags {
			if a == "-run" || a == "-skip" || strings.HasPrefix(a, "-run=") || strings.HasPrefix(a, "-skip=") || a == "-json" {
				fmt.Fprintf(stderr, "test-shards %s: the shards set -run, -skip and -json themselves\n", action)
				return nil, false
			}
		}
		return f, true
	}
	return nil, false
}

func testShardsPlanOrRun(root, action string, args []string, stdout, stderr io.Writer) int {
	f, ok := parseShardFlags(action, args, stderr)
	if !ok {
		return 2
	}
	times, err := readTestTimes(filepath.Join(root, filepath.FromSlash(f.times)))
	if err != nil {
		fmt.Fprintf(stderr, "test-shards %s: %v\n", action, err)
		return 1
	}
	dir := filepath.Join(root, filepath.FromSlash(f.dir))
	var pkgs []string
	if f.packages != "" {
		pkgs = strings.Split(f.packages, ",")
	} else if pkgs, err = packagesWithTests(dir, f.goFlags); err != nil {
		fmt.Fprintf(stderr, "test-shards %s: %v\n", action, err)
		return 1
	}
	shards := planShards(times.Sets[f.set], pkgs, planOptions{shards: f.shards, splitOver: f.splitOver, under: f.under})
	if action == "plan" {
		for i, parts := range shards {
			printShard(stdout, i+1, len(shards), parts)
		}
		return 0
	}
	parts := shards[f.shard-1]
	printShard(stdout, f.shard, f.shards, parts)
	return runShard(dir, f, parts, stdout, stderr)
}

func printShard(w io.Writer, shard, shards int, parts []shardPart) {
	fmt.Fprintf(w, "shard %d of %d: %d packages, about %.0f seconds of tests\n", shard, shards, len(parts), shardSeconds(parts))
	for _, p := range parts {
		line := fmt.Sprintf("  %7.1fs  %s", p.seconds, p.pkg)
		if a := p.args(); len(a) > 0 {
			line += fmt.Sprintf(" (%s)", partSummary(p))
		}
		fmt.Fprintln(w, line)
	}
}

func partSummary(p shardPart) string {
	if p.rest {
		return fmt.Sprintf("all but %d tests", len(p.others))
	}
	return fmt.Sprintf("%d tests", len(p.names))
}

// packagesWithTests lists the packages of the module in dir that have test
// files, as go list names them, with the module's path cut to ".".
func packagesWithTests(dir string, goFlags []string) ([]string, error) {
	args := []string{"list", "-f", "{{if or .TestGoFiles .XTestGoFiles}}{{.ImportPath}}{{end}}"}
	for i, a := range goFlags {
		switch {
		case strings.HasPrefix(a, "-tags="):
			args = append(args, a)
		case a == "-tags" && i+1 < len(goFlags):
			args = append(args, a, goFlags[i+1])
		}
	}
	out, err := goOutput(dir, append(args, "./...")...)
	if err != nil {
		return nil, err
	}
	module, err := goOutput(dir, "list", "-m")
	if err != nil {
		return nil, err
	}
	var pkgs []string
	for _, line := range strings.Split(out, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			pkgs = append(pkgs, shortPackage(line, strings.TrimSpace(module)))
		}
	}
	return pkgs, nil
}

func shortPackage(importPath, module string) string {
	if importPath == module {
		return "."
	}
	if strings.HasPrefix(importPath, module+"/") {
		return "./" + strings.TrimPrefix(importPath, module+"/")
	}
	return importPath
}

func goOutput(dir string, args ...string) (string, error) {
	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	var errOut strings.Builder
	cmd.Stderr = &errOut
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("go %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(errOut.String()))
	}
	return string(out), nil
}

// runShard runs the parts of a shard: one go test for the whole packages
// and one per cut package, side by side.
func runShard(dir string, f *shardFlags, parts []shardPart, stdout, stderr io.Writer) int {
	var whole []string
	var runs [][]string
	for _, p := range parts {
		if a := p.args(); len(a) > 0 {
			runs = append(runs, append(append([]string{}, a...), p.pkg))
		} else {
			whole = append(whole, p.pkg)
		}
	}
	if len(whole) > 0 {
		runs = append(runs, whole)
	}
	module, err := goOutput(dir, "list", "-m")
	if err != nil {
		fmt.Fprintln(stderr, "test-shards run:", err)
		return 1
	}
	out := newTestOutput(stdout, strings.TrimSpace(module), f.under)
	out.annotate = os.Getenv("GITHUB_ACTIONS") == "true"
	var wg sync.WaitGroup
	failed := make([]bool, len(runs))
	for i, r := range runs {
		args := append(append([]string{"test", "-json"}, f.goFlags...), r...)
		cmd := exec.Command("go", args...)
		cmd.Dir = dir
		cmd.Stderr = stderr
		pipe, err := cmd.StdoutPipe()
		if err == nil {
			err = cmd.Start()
		}
		if err != nil {
			fmt.Fprintf(stderr, "test-shards run: go %s: %v\n", strings.Join(args, " "), err)
			failed[i] = true
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			out.read(pipe)
			if err := cmd.Wait(); err != nil {
				failed[i] = true
			}
		}()
	}
	wg.Wait()
	code := out.summary()
	for _, bad := range failed {
		if bad {
			code = 1
		}
	}
	if f.timesOut != "" {
		t := &testTimes{Sets: map[string]map[string]*pkgTime{f.set: out.times}}
		if err := writeTestTimes(f.timesOut, t); err != nil {
			fmt.Fprintln(stderr, "test-shards run:", err)
			return 1
		}
	}
	return code
}

// testEvent is one line of go test -json (cmd/test2json).
type testEvent struct {
	Time       time.Time
	Action     string
	Package    string
	ImportPath string
	Test       string
	Elapsed    float64
	Output     string
}

// testOutput turns go test -json into what a person reads: the result line
// of each package, the output of each failed test, and what a package
// printed outside its tests (build errors, panics, a suite's summary).
type testOutput struct {
	mu     sync.Mutex
	w      io.Writer
	module string
	under  string
	// held is the output of tests still running, by package and test.
	held   map[string][]string
	times  map[string]*pkgTime
	failed []string
	passed int
	broken []string
	// annotate marks failed tests for GitHub Actions as well
	annotate bool
}

func newTestOutput(w io.Writer, module, under string) *testOutput {
	return &testOutput{w: w, module: module, under: under, held: map[string][]string{}, times: map[string]*pkgTime{}}
}

func (o *testOutput) read(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		o.line(sc.Bytes())
	}
	if err := sc.Err(); err != nil {
		o.mu.Lock()
		fmt.Fprintf(o.w, "test-shards: reading go test: %v\n", err)
		o.broken = append(o.broken, "reading go test: "+err.Error())
		o.mu.Unlock()
	}
}

func (o *testOutput) line(b []byte) {
	o.mu.Lock()
	defer o.mu.Unlock()
	var e testEvent
	if len(b) == 0 || b[0] != '{' || json.Unmarshal(b, &e) != nil {
		fmt.Fprintf(o.w, "%s\n", b)
		return
	}
	key := e.Package + " " + e.Test
	switch {
	case e.Action == "build-output":
		fmt.Fprint(o.w, e.Output)
	case e.Action == "output" && e.Test == "":
		if strings.TrimSpace(e.Output) != "PASS" {
			fmt.Fprint(o.w, e.Output)
		}
	case e.Action == "output":
		o.held[key] = append(o.held[key], e.Output)
	case e.Test != "" && (e.Action == "pass" || e.Action == "fail" || e.Action == "skip"):
		if e.Action == "fail" {
			for _, l := range o.held[key] {
				fmt.Fprint(o.w, l)
			}
			o.failed = append(o.failed, o.short(e.Package)+" "+e.Test)
		}
		delete(o.held, key)
		o.testTime(e)
	case e.Test == "" && (e.Action == "pass" || e.Action == "fail"):
		pkg := o.short(e.Package)
		o.pkgTime(pkg).Seconds += e.Elapsed
		if e.Action == "pass" {
			o.passed++
			return
		}
		// Tests that never ended: the package panicked or timed out
		prefix := e.Package + " "
		var open []string
		for k := range o.held {
			if strings.HasPrefix(k, prefix) {
				open = append(open, k)
			}
		}
		sort.Strings(open)
		for _, k := range open {
			for _, l := range o.held[k] {
				fmt.Fprint(o.w, l)
			}
			delete(o.held, k)
		}
		o.broken = append(o.broken, pkg)
	}
}

func (o *testOutput) short(pkg string) string { return shortPackage(pkg, o.module) }

func (o *testOutput) pkgTime(pkg string) *pkgTime {
	pt := o.times[pkg]
	if pt == nil {
		pt = &pkgTime{}
		o.times[pkg] = pt
	}
	return pt
}

// testTime keeps the time of a top-level test, or of a subtest right
// below the --under test.
func (o *testOutput) testTime(e testEvent) {
	name := e.Test
	if o.under != "" {
		rest, ok := strings.CutPrefix(name, o.under+"/")
		if !ok {
			return
		}
		name = rest
	}
	if strings.Contains(name, "/") {
		return
	}
	pt := o.pkgTime(o.short(e.Package))
	if pt.Tests == nil {
		pt.Tests = map[string]float64{}
	}
	pt.Tests[name] += e.Elapsed
}

// summary prints what failed and returns the exit status.
func (o *testOutput) summary() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	fmt.Fprintf(o.w, "\n%d packages passed, %d failed\n", o.passed, len(o.broken))
	for _, p := range o.broken {
		fmt.Fprintf(o.w, "FAIL %s\n", p)
	}
	// Only the deepest failures: a parent test fails with its subtest
	leaves := leafTests(o.failed)
	for _, t := range leaves {
		fmt.Fprintf(o.w, "--- FAIL %s\n", t)
		if o.annotate {
			fmt.Fprintf(o.w, "::error title=go test::%s failed\n", t)
		}
	}
	if len(o.broken) > 0 || len(leaves) > 0 {
		return 1
	}
	return 0
}

func leafTests(failed []string) []string {
	sorted := append([]string{}, failed...)
	sort.Strings(sorted)
	var leaves []string
	for i, t := range sorted {
		if i+1 < len(sorted) && strings.HasPrefix(sorted[i+1], t+"/") {
			continue
		}
		leaves = append(leaves, t)
	}
	return leaves
}

// testShardsMerge adds up the times of runs into a times file: the parts
// of a package that ran on different shards add up to the package. Sets
// the runs have replace the file's sets of that name; others stay.
func testShardsMerge(root string, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("test-shards merge", flag.ContinueOnError)
	fs.SetOutput(stderr)
	outPath := fs.String("out", "", "the times file to write (its other sets are kept)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *outPath == "" || fs.NArg() == 0 {
		fmt.Fprintln(stderr, "usage: repotool test-shards merge --out FILE TIMES...")
		return 2
	}
	merged := map[string]map[string]*pkgTime{}
	for _, path := range fs.Args() {
		t, err := readTestTimes(path)
		if err != nil {
			fmt.Fprintln(stderr, "test-shards merge:", err)
			return 1
		}
		addTimes(merged, t.Sets)
	}
	out, err := readTestTimes(*outPath)
	if errors.Is(err, os.ErrNotExist) {
		out, err = &testTimes{Sets: map[string]map[string]*pkgTime{}}, nil
	}
	if err != nil {
		fmt.Fprintln(stderr, "test-shards merge:", err)
		return 1
	}
	names := make([]string, 0, len(merged))
	for name, set := range merged {
		for _, p := range set {
			if p.Seconds < keepTestsOver {
				p.Tests = nil
			}
		}
		out.Sets[name] = set
		names = append(names, name+" ("+strconv.Itoa(len(set))+" packages)")
	}
	sort.Strings(names)
	if err := writeTestTimes(*outPath, out); err != nil {
		fmt.Fprintln(stderr, "test-shards merge:", err)
		return 1
	}
	fmt.Fprintf(stdout, "wrote %s: %s\n", *outPath, strings.Join(names, ", "))
	return 0
}

func addTimes(into map[string]map[string]*pkgTime, sets map[string]map[string]*pkgTime) {
	for name, set := range sets {
		if into[name] == nil {
			into[name] = map[string]*pkgTime{}
		}
		for pkg, p := range set {
			q := into[name][pkg]
			if q == nil {
				q = &pkgTime{}
				into[name][pkg] = q
			}
			q.Seconds += p.Seconds
			for t, s := range p.Tests {
				if q.Tests == nil {
					q.Tests = map[string]float64{}
				}
				q.Tests[t] += s
			}
		}
	}
}
