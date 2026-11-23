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
	"runtime"
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

// The times file keeps the tests of packages that take at least
// keepTestsOver seconds (half of what CI cuts at), and the subtests of
// tests that take at least keepSubtestsOver. Only those can be cut, and
// the file stays small.
const (
	keepTestsOver    = 120.0
	keepSubtestsOver = 60.0
)

// testShards is "test-shards": it deals the tests of a module over CI
// runners by the times they took before, runs one runner's share and
// keeps the times of this run for the next layout.
//
//	test-shards plan  --set NAME --shards N [--split-over S] [--packages P,...] [-- go test flags]
//	test-shards run   --set NAME --shards N --shard K [--split-over S] [--packages P,...] [--times-out FILE] -- go test flags
//	test-shards merge --out FILE TIMES...
//
// The times are in .github/test-times.json: per set (a database) the
// seconds of each package, of the top-level tests of packages of two
// minutes and more, and of the subtests of tests of a minute and more.
// Packages go to the runner with the least time so far, the longest
// first. A package longer than --split-over seconds is cut into parts by
// its top-level tests, and a test longer than that by its subtests (which
// must then not need each other). The last part of each cut runs
// everything the times do not name, so a new test always runs once.
//
// run starts one go test -json per package or part in --dir (default
// neodesk-server), the longest first and one per CPU at a time,
// prints the results of the packages and the output of failed tests, and
// writes the times of this run to --times-out. merge adds such files up
// into a times file.
func testShards(root string, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: repotool test-shards plan|run|merge [flags]")
		return 2
	}
	switch args[0] {
	case "plan", "run":
		return testShardsPlanOrRun(root, args[0], args[1:], stdout, stderr)
	case "merge":
		return testShardsMerge(args[1:], stdout, stderr)
	}
	fmt.Fprintf(stderr, "repotool test-shards: unknown action %q (plan, run or merge)\n", args[0])
	return 2
}

// testTimes is the times file: per set, the packages by name.
type testTimes struct {
	About string                         `json:"about,omitempty"`
	Sets  map[string]map[string]*pkgTime `json:"sets"`
}

// pkgTime is what one package took: all of it, its top-level tests and
// the subtests right below them.
type pkgTime struct {
	Seconds  float64                       `json:"seconds"`
	Tests    map[string]float64            `json:"tests,omitempty"`
	Subtests map[string]map[string]float64 `json:"subtests,omitempty"`
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
			for _, subs := range p.Subtests {
				for name, s := range subs {
					subs[name] = round1(s)
				}
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

// shardPart is one go test run of a shard: a whole package, or a share
// of a package the times cut into parts.
type shardPart struct {
	pkg     string
	seconds float64
	split   bool
	// test is the top-level test whose subtests the part runs; empty for
	// a part of top-level tests.
	test string
	// names are what the part runs. The rest part runs everything but
	// others instead.
	names  []string
	rest   bool
	others []string
}

// args are go test's -run and -skip for the part; none for a whole package.
func (p shardPart) args() []string {
	if !p.split {
		return nil
	}
	prefix := ""
	if p.test != "" {
		prefix = "^" + regexp.QuoteMeta(p.test) + "$/"
	}
	if !p.rest {
		return []string{"-run", prefix + namesPattern(p.names)}
	}
	var args []string
	if p.test != "" {
		args = append(args, "-run", "^"+regexp.QuoteMeta(p.test)+"$")
	}
	if len(p.others) > 0 {
		args = append(args, "-skip", prefix+namesPattern(p.others))
	}
	return args
}

func (p shardPart) label() string {
	if !p.split {
		return ""
	}
	what, n := "tests", len(p.names)
	if p.test != "" {
		what = p.test + " subtests"
	}
	if p.rest {
		return fmt.Sprintf("all %s but %d", what, len(p.others))
	}
	return fmt.Sprintf("%d %s", n, what)
}

func namesPattern(names []string) string {
	quoted := make([]string, len(names))
	for i, n := range names {
		quoted[i] = regexp.QuoteMeta(n)
	}
	sort.Strings(quoted)
	return "^(" + strings.Join(quoted, "|") + ")$"
}

// cutNames cuts tests (or the subtests of test) that take seconds in all
// into k parts: the longest first into the part with the least time so
// far. The part with the least time is the rest part. A part's seconds
// are the share of its names in seconds.
func cutNames(pkg, test string, times map[string]float64, seconds float64, k int) []shardPart {
	names := make([]string, 0, len(times))
	total := 0.0
	for n, s := range times {
		names = append(names, n)
		total += s
	}
	sort.Slice(names, func(i, j int) bool {
		a, b := times[names[i]], times[names[j]]
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
		loads[best] += times[n]
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
		part := shardPart{pkg: pkg, test: test, seconds: seconds * share, names: bins[b], rest: b == rest, split: true}
		if part.rest {
			for o := 0; o < k; o++ {
				if o != rest {
					part.others = append(part.others, bins[o]...)
				}
			}
			sort.Strings(part.others)
			// The rest part also stands for what is not measured yet
			part.seconds = math.Max(part.seconds, unknownPackageSeconds)
		}
		parts = append(parts, part)
	}
	return parts
}

func partsFor(seconds, over float64, most int) int {
	k := int(math.Ceil(seconds / over))
	return max(1, min(k, most))
}

// splitPackage cuts a package into parts of about splitOver seconds: its
// long tests by their subtests, the rest of it by its top-level tests.
func splitPackage(pkg string, pt *pkgTime, splitOver float64) []shardPart {
	var long []string
	longSeconds := 0.0
	short := map[string]float64{}
	for n, s := range pt.Tests {
		if s > splitOver && len(pt.Subtests[n]) > 1 {
			long = append(long, n)
			longSeconds += s
		} else {
			short[n] = s
		}
	}
	sort.Strings(long)
	var parts []shardPart
	for _, n := range long {
		subs := pt.Subtests[n]
		parts = append(parts, cutNames(pkg, n, subs, pt.Tests[n], partsFor(pt.Tests[n], splitOver, len(subs)))...)
	}
	remaining := math.Max(pt.Seconds-longSeconds, 0)
	top := cutNames(pkg, "", short, remaining, partsFor(remaining, splitOver, len(short)))
	// The long tests have parts of their own
	for i := range top {
		if top[i].rest && len(long) > 0 {
			top[i].others = append(top[i].others, long...)
			sort.Strings(top[i].others)
		}
	}
	return append(parts, top...)
}

// planShards deals the packages over the shards: the longest part first,
// each to the shard with the least time so far that has no other part of
// its package (two test binaries of one package side by side could share
// a port or a file).
func planShards(times map[string]*pkgTime, pkgs []string, shards int, splitOver float64) [][]shardPart {
	var parts []shardPart
	for _, pkg := range pkgs {
		pt := times[pkg]
		if pt == nil {
			pt = &pkgTime{Seconds: unknownPackageSeconds}
		}
		if splitOver <= 0 || pt.Seconds <= splitOver || len(pt.Tests) < 2 {
			parts = append(parts, shardPart{pkg: pkg, seconds: pt.Seconds})
			continue
		}
		parts = append(parts, splitPackage(pkg, pt, splitOver)...)
	}
	sort.SliceStable(parts, func(i, j int) bool {
		if parts[i].seconds != parts[j].seconds {
			return parts[i].seconds > parts[j].seconds
		}
		return parts[i].pkg < parts[j].pkg
	})
	out := make([][]shardPart, shards)
	loads := make([]float64, shards)
	has := make([]map[string]bool, shards)
	for s := range has {
		has[s] = map[string]bool{}
	}
	for _, p := range parts {
		best, free := 0, false
		for s := 0; s < shards; s++ {
			ok := !has[s][p.pkg]
			if (ok && !free) || (ok == free && loads[s] < loads[best]) {
				best, free = s, ok
			}
		}
		out[best] = append(out[best], p)
		loads[best] += p.seconds
		has[best][p.pkg] = true
	}
	for s := range out {
		out[s] = joinParts(out[s])
	}
	return out
}

// joinParts makes one part of the parts of a cut that landed in the same
// shard, so its test binary starts once for them. The longest part comes
// first.
func joinParts(parts []shardPart) []shardPart {
	var out []shardPart
	index := map[string]int{}
	for _, p := range parts {
		key := p.pkg + "\x00" + p.test
		i, seen := index[key]
		if !seen || !p.split {
			index[key] = len(out)
			out = append(out, p)
			continue
		}
		q := &out[i]
		q.seconds += p.seconds
		if !q.rest && !p.rest {
			q.names = append(q.names, p.names...)
			continue
		}
		if !q.rest {
			q.others = p.others
		}
		mine := map[string]bool{}
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
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].seconds != out[j].seconds {
			return out[i].seconds > out[j].seconds
		}
		return out[i].pkg < out[j].pkg
	})
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
	times, set, dir, packages, timesOut string
	shards, shard                       int
	splitOver                           float64
	goFlags                             []string
}

func parseShardFlags(action string, args []string, stderr io.Writer) (*shardFlags, bool) {
	fs := flag.NewFlagSet("test-shards "+action, flag.ContinueOnError)
	fs.SetOutput(stderr)
	f := &shardFlags{}
	fs.StringVar(&f.times, "times", testTimesFile, "the times file, from the repository's root")
	fs.StringVar(&f.set, "set", "", "the set of times to use (go-race-sqlite, go-race-postgres)")
	fs.StringVar(&f.dir, "dir", "neodesk-server", "the module to test, from the repository's root")
	fs.StringVar(&f.packages, "packages", "", "comma-separated packages (default: every package of the module that has tests)")
	fs.StringVar(&f.timesOut, "times-out", "", "write the times of this run to this file")
	fs.IntVar(&f.shards, "shards", 0, "the number of shards")
	fs.IntVar(&f.shard, "shard", 0, "the shard to run, from 1")
	fs.Float64Var(&f.splitOver, "split-over", 0, "cut a package or test that takes longer than this many seconds")
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
	shards := planShards(times.Sets[f.set], pkgs, f.shards, f.splitOver)
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
	longest := 0.0
	for _, p := range parts {
		longest = math.Max(longest, p.seconds)
	}
	fmt.Fprintf(w, "shard %d of %d: %d parts, %.0f seconds of tests, the longest %.0f\n", shard, shards, len(parts), shardSeconds(parts), longest)
	for _, p := range parts {
		line := fmt.Sprintf("  %7.1fs  %s", p.seconds, p.pkg)
		if l := p.label(); l != "" {
			line += " (" + l + ")"
		}
		fmt.Fprintln(w, line)
	}
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

// shardRuns are the go arguments of a shard's calls, the longest first:
// one go test per whole package and per part of a cut package.
func shardRuns(parts []shardPart, goFlags []string) [][]string {
	base := append([]string{"test", "-json"}, goFlags...)
	runs := make([][]string, 0, len(parts))
	for _, p := range parts {
		runs = append(runs, append(append(append([]string{}, base...), p.args()...), p.pkg))
	}
	return runs
}

// inPool calls do(i) for i from 0 to n-1 in that order, at most slots of
// them at a time.
func inPool(n, slots int, do func(i int)) {
	free := make(chan struct{}, max(1, slots))
	var wg sync.WaitGroup
	for i := range n {
		free <- struct{}{}
		wg.Add(1)
		go func() {
			defer func() { <-free; wg.Done() }()
			do(i)
		}()
	}
	wg.Wait()
}

// runShard runs the go test calls of a shard, longest first and one test
// binary per CPU at a time, as go test runs the packages of one call.
func runShard(dir string, f *shardFlags, parts []shardPart, stdout, stderr io.Writer) int {
	runs := shardRuns(parts, f.goFlags)
	module, err := goOutput(dir, "list", "-m")
	if err != nil {
		fmt.Fprintln(stderr, "test-shards run:", err)
		return 1
	}
	out := newTestOutput(stdout, strings.TrimSpace(module))
	out.annotate = os.Getenv("GITHUB_ACTIONS") == "true"
	failed := make([]bool, len(runs))
	inPool(len(runs), runtime.NumCPU(), func(i int) {
		cmd := exec.Command("go", runs[i]...)
		cmd.Dir = dir
		cmd.Stderr = stderr
		pipe, err := cmd.StdoutPipe()
		if err == nil {
			err = cmd.Start()
		}
		if err != nil {
			fmt.Fprintf(stderr, "test-shards run: go %s: %v\n", strings.Join(runs[i], " "), err)
			failed[i] = true
			return
		}
		out.read(pipe)
		if err := cmd.Wait(); err != nil {
			failed[i] = true
		}
	})
	code := out.summary()
	for _, bad := range failed {
		if bad {
			code = 1
		}
	}
	if f.timesOut != "" {
		dropShortSubtests(out.times)
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
	Time    time.Time
	Action  string
	Package string
	Test    string
	Elapsed float64
	Output  string
}

// testOutput turns go test -json into what a person reads: the result line
// of each package, the output of each failed test, and what a package
// printed outside its tests (build errors, panics).
type testOutput struct {
	mu     sync.Mutex
	w      io.Writer
	module string
	// held is the output of tests still running, by package and test.
	held   map[string][]string
	times  map[string]*pkgTime
	failed []string
	passed int
	broken []string
	// annotate marks failed tests for GitHub Actions as well
	annotate bool
}

func newTestOutput(w io.Writer, module string) *testOutput {
	return &testOutput{w: w, module: module, held: map[string][]string{}, times: map[string]*pkgTime{}}
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

// testTime keeps the time of a top-level test or of a subtest right
// below one.
func (o *testOutput) testTime(e testEvent) {
	pt := o.pkgTime(o.short(e.Package))
	top, sub, isSub := strings.Cut(e.Test, "/")
	switch {
	case !isSub:
		if pt.Tests == nil {
			pt.Tests = map[string]float64{}
		}
		pt.Tests[top] += e.Elapsed
	case !strings.Contains(sub, "/"):
		if pt.Subtests == nil {
			pt.Subtests = map[string]map[string]float64{}
		}
		if pt.Subtests[top] == nil {
			pt.Subtests[top] = map[string]float64{}
		}
		pt.Subtests[top][sub] += e.Elapsed
	}
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
func testShardsMerge(args []string, stdout, stderr io.Writer) int {
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
		dropShortSubtests(set)
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

// dropShortSubtests keeps the subtests of the tests that take a minute or
// more, the only ones a test is cut by.
func dropShortSubtests(set map[string]*pkgTime) {
	for _, p := range set {
		for test := range p.Subtests {
			if p.Tests[test] < keepSubtestsOver {
				delete(p.Subtests, test)
			}
		}
		if len(p.Subtests) == 0 {
			p.Subtests = nil
		}
	}
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
			for t, subs := range p.Subtests {
				if q.Subtests == nil {
					q.Subtests = map[string]map[string]float64{}
				}
				if q.Subtests[t] == nil {
					q.Subtests[t] = map[string]float64{}
				}
				for s, secs := range subs {
					q.Subtests[t][s] += secs
				}
			}
		}
	}
}
