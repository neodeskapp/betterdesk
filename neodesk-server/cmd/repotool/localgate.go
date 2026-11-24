package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// localGate is "local-gate": the complete local check of a developer
// checkout, which the Git hooks in .githooks run before a commit and a
// push once scripts/setup-local-gates enabled them. GitHub Actions never
// runs it. It fails closed: a missing tool, a required test that was
// skipped and a failed check all end with exit code 1. The report goes to
// .local-test-results (latest.json, latest.md), or to --report-dir.
func localGate(root string, args []string, stdout, stderr io.Writer) int {
	opts, err := parseGateArgs(args, os.Getenv, root)
	if err != nil {
		fmt.Fprintf(stderr, "LOCAL GATE FAILED: %v\n", err)
		return 1
	}
	if opts.help {
		fmt.Fprint(stdout, gateUsage)
		return 0
	}
	g := &gate{root: root, stdout: stdout, stderr: stderr, getenv: os.Getenv, lookPath: exec.LookPath}
	report := g.run(opts)
	if report.Overall != "PASS" {
		return 1
	}
	return 0
}

const gateUsage = `Usage: go run ./cmd/repotool local-gate [options]

Runs the complete local-only NeoDesk diagnostic gate.

Options:
  --dry-run             Print the planned command categories without running them
  --preflight-only      Validate local tools and versions without running application tests
  --report-dir DIR      Write JSON/Markdown reports to DIR
  --help                Show this help

The gate fails closed: missing required tools, skipped required tests and failed
checks return a non-zero exit code.
`

const (
	maxCapture       = 512 * 1024
	commandTimeout   = 30 * time.Minute
	securityTimeout  = 20 * time.Minute
	postgresPassword = "NeoDeskLocalGateOnly2026"
)

var composeFiles = []string{
	"docker-compose.single.yml",
	"docker-compose.quick.single.yml",
	"docker-compose.quick.single.macvlan.yml",
}

var bashInstallerFiles = []string{
	"install.sh",
	"neodesk.sh",
	"neodesk-docker.sh",
	"scripts/test-native-installer.sh",
	"scripts/test-go-installer.sh",
	"scripts/check-go-toolchain-pins.sh",
	"scripts/test-docker-installer.sh",
	"scripts/check-supervisor-env.sh",
	"scripts/test-postgresql-hba.sh",
	"scripts/test-docker-hardening.sh",
	"scripts/local-gate.sh",
}

// outOfScope are what the gate cannot check from a checkout; the report
// names them.
type scopeEntry struct {
	ID     string `json:"id"`
	Area   string `json:"area"`
	Reason string `json:"reason"`
}

var outOfScope = []scopeEntry{
	{"native-host-lifecycle", "Installer lifecycle on the host", "Requires a disposable Linux/Windows VM and can change services or permissions."},
	{"external-rustdesk-client", "External RustDesk client and physical devices", "Requires a real client/device pair and is not reproducible from this checkout alone."},
	{"missing-desktop-trees", "Desktop/agent trees absent from this checkout", "The documented desktop and agent source directories are not tracked here."},
	{"platform-specific-linux-tests", "Linux-only tests on a Windows host", "Systemd/privileged Linux checks cannot run natively on Windows without changing a host or VM."},
}

type gateOptions struct {
	dryRun        bool
	preflightOnly bool
	reportDir     string
	help          bool
}

// parseGateArgs reads the options. The report folder is --report-dir,
// else NEODESK_LOCAL_GATE_REPORT_DIR, else .local-test-results; a
// relative folder is taken from the repository's root, where the
// wrappers and hooks run the gate.
func parseGateArgs(args []string, getenv func(string) string, root string) (gateOptions, error) {
	opts := gateOptions{reportDir: filepath.Join(root, ".local-test-results")}
	if dir := getenv("NEODESK_LOCAL_GATE_REPORT_DIR"); dir != "" {
		opts.reportDir = fromRoot(root, dir)
	}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--dry-run":
			opts.dryRun = true
		case "--preflight-only":
			opts.preflightOnly = true
		case "--report-dir":
			value := ""
			if i+1 < len(args) {
				i++
				value = args[i]
			}
			opts.reportDir = fromRoot(root, value)
		case "--help", "-h":
			opts.help = true
		default:
			return opts, fmt.Errorf("Unknown option: %s", args[i])
		}
	}
	return opts, nil
}

func fromRoot(root, p string) string {
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Join(root, p)
}

// gateReport is the JSON report, field for field.
type gateReport struct {
	SchemaVersion int           `json:"schemaVersion"`
	LocalOnly     bool          `json:"localOnly"`
	StartedAt     string        `json:"startedAt"`
	FinishedAt    *string       `json:"finishedAt"`
	Repository    repoState     `json:"repository"`
	Environment   gateEnv       `json:"environment"`
	Results       []*gateResult `json:"results"`
	OutOfScope    []scopeEntry  `json:"outOfScope"`
	Overall       string        `json:"overall"`
	DryRun        bool          `json:"dryRun,omitempty"`
}

type repoState struct {
	Branch string `json:"branch"`
	SHA    string `json:"sha"`
	Dirty  bool   `json:"dirty"`
}

type gateEnv struct {
	Platform string `json:"platform"`
	Release  string `json:"release"`
	Arch     string `json:"arch"`
	Node     string `json:"node"`
}

// gateResult is one check of the report.
type gateResult struct {
	ID         string  `json:"id"`
	Label      string  `json:"label"`
	Status     string  `json:"status"`
	Required   bool    `json:"required"`
	Command    string  `json:"command"`
	Cwd        string  `json:"cwd"`
	StartedAt  string  `json:"startedAt"`
	DurationMs int64   `json:"durationMs"`
	ExitCode   *int    `json:"exitCode"`
	Signal     *string `json:"signal"`
	Detail     string  `json:"detail"`
	Stdout     string  `json:"stdout"`
	Stderr     string  `json:"stderr"`
}

// gate runs the checks and keeps the report.
type gate struct {
	root           string
	stdout, stderr io.Writer
	getenv         func(string) string
	lookPath       func(string) (string, error)
	report         *gateReport
}

func isoNow() string { return isoTime(time.Now()) }

// isoTime is Date.prototype.toISOString.
func isoTime(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000Z") }

func (g *gate) run(opts gateOptions) *gateReport {
	g.report = &gateReport{
		SchemaVersion: 1,
		LocalOnly:     true,
		StartedAt:     isoNow(),
		Repository:    g.repoState(),
		Environment:   g.environment(),
		Results:       []*gateResult{},
		OutOfScope:    outOfScope,
		Overall:       "BLOCKED",
	}
	if opts.dryRun {
		fmt.Fprint(g.stdout, `Dry run: full local gate categories:
- preflight and version checks
- npm install/audit, frontend/protocol/i18n/provenance and the web app's tests
- Go vet/tests/race/vulnerability scan/build/cross-build
- Python compile/tests/pip-audit
- Rust tests and cargo-audit when Rust manifests are present
- gitleaks, sensitive path scan and Trivy
- Bash/ShellCheck/PowerShell installer checks
- Compose validation, local image builds, PostgreSQL and runtime smoke
`)
		g.report.Overall = "PASS"
		g.report.DryRun = true
		return g.report
	}

	var postgres postgresInstance
	func() {
		defer func() {
			if p := recover(); p != nil {
				g.addInternal("local-gate-fatal", "Local gate orchestration", "FAIL", fmt.Sprint(p), internalExtra{})
			}
		}()
		if g.getenv("GITHUB_ACTIONS") == "true" {
			g.addInternal("local-only-guard", "Local-only execution guard", "BLOCKED",
				"The local gate is intentionally disabled inside GitHub Actions.", internalExtra{})
			return
		}
		env := g.preflight()
		if opts.preflightOnly || !env.ok {
			return
		}
		g.runVersionAndDiffChecks()
		g.runNodeChecks(env.tools)
		g.runPythonChecks(env.tools, env.manifests)
		g.runRustChecks(env.tools, env.manifests)
		g.runInstallerChecks(env.tools)
		g.runSecurityChecks(env.tools)
		builds := g.runComposeConfigChecks(env.tools)
		postgres = g.startPostgres(env.tools)
		g.runGoChecks(env.tools, postgres.dsn)
		g.runFullNodeTests(env.tools)
		g.runDockerRuntimeChecks(env.tools, postgres, builds)
	}()
	if postgres.name != "" {
		if docker := g.executable("docker"); docker != "" {
			cmd := exec.Command(docker, "rm", "--force", postgres.name)
			cmd.Dir = g.root
			_ = cmd.Run()
		}
	}
	finished := isoNow()
	g.report.FinishedAt = &finished
	g.report.Overall = calculateOverall(g.report.Results)

	paths, err := writeReports(g.report, opts.reportDir)
	if err != nil {
		fmt.Fprintf(g.stderr, "LOCAL GATE FAILED: %v\n", err)
		g.report.Overall = "FAIL"
		return g.report
	}
	g.printSummary(paths)
	return g.report
}

func (g *gate) repoState() repoState {
	git := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = g.root
		out, _ := cmd.Output()
		return string(out)
	}
	state := repoState{
		Branch: strings.TrimSpace(git("branch", "--show-current")),
		SHA:    strings.TrimSpace(git("rev-parse", "HEAD")),
		Dirty:  git("status", "--porcelain") != "",
	}
	if state.Branch == "" {
		state.Branch = "unknown"
	}
	if state.SHA == "" {
		state.SHA = "unknown"
	}
	return state
}

// environment names the host the way Node does (win32, x64).
func (g *gate) environment() gateEnv {
	platform := runtime.GOOS
	if platform == "windows" {
		platform = "win32"
	}
	arch := map[string]string{"amd64": "x64", "386": "ia32", "arm64": "arm64", "arm": "arm"}[runtime.GOARCH]
	if arch == "" {
		arch = runtime.GOARCH
	}
	release := ""
	if out, err := exec.Command("uname", "-r").Output(); err == nil {
		release = strings.TrimSpace(string(out))
	}
	node := ""
	if path := g.executable("node"); path != "" {
		node = g.commandVersion(path, "--version")
	}
	return gateEnv{Platform: platform, Release: release, Arch: arch, Node: node}
}

// ---- Running commands ------------------------------------------------

var (
	reRedactDSN     = regexp.MustCompile(`(?i)(postgres(?:ql)?://[^:]+:)[^@/` + jsSpaceClass + `]+(@)`)
	reRedactSecret  = regexp.MustCompile(`(?i)((?:["']?(?:password|passwd|token|secret|api[_-]?key|private[_-]?key)["']?` + jsSpace + `*[:=]` + jsSpace + `*["']?))[^"',}` + jsSpaceClass + `]+`)
	reRedactBearer  = regexp.MustCompile(`(?i)(authorization` + jsSpace + `*:` + jsSpace + `*bearer` + jsSpace + `+)[^` + jsSpaceClass + `]+`)
	reFormatDSN     = regexp.MustCompile(`(?i)(postgres(?:ql)?://[^:]+:)([^@]+)(@)`)
	reNeedsQuotes   = regexp.MustCompile(`[` + jsSpaceClass + `"]`)
	reCmdArgPlain   = regexp.MustCompile(`^[\w./:=+-]+$`)
	reCmdArgSpecial = regexp.MustCompile(`(["^&|<>])`)
	unexpectedSkips = []*regexp.Regexp{
		regexp.MustCompile(`(?i)\bSKIP(?:PED)?` + jsSpace + `*:`),
		regexp.MustCompile(`(?i)(?:^|` + jsSpace + `)[1-9]\d*` + jsSpace + `+skipped\b`),
		regexp.MustCompile(`(?i)\btests?` + jsSpace + `+skipped\b`),
		regexp.MustCompile(`(?i)\bno tests? to run\b`),
	}
)

// jsSpaceClass is jsSpace without its brackets, to put into other classes.
var jsSpaceClass = jsSpace[1 : len(jsSpace)-1]

// redactSensitive hides database passwords, secrets and bearer tokens in
// output the report keeps.
func redactSensitive(s string) string {
	s = reRedactDSN.ReplaceAllString(s, "${1}[REDACTED]${2}")
	s = reRedactSecret.ReplaceAllString(s, "${1}[REDACTED]")
	return reRedactBearer.ReplaceAllString(s, "${1}[REDACTED]")
}

// formatCommand is the command line the report shows, with database
// passwords hidden.
func formatCommand(command string, args []string) string {
	parts := make([]string, 0, len(args)+1)
	for _, v := range append([]string{command}, args...) {
		if reNeedsQuotes.MatchString(v) {
			v = `"` + strings.ReplaceAll(v, `"`, `\"`) + `"`
		}
		parts = append(parts, v)
	}
	return reFormatDSN.ReplaceAllString(strings.Join(parts, " "), "${1}[REDACTED]${3}")
}

func quoteCmdArg(v string) string {
	if reCmdArgPlain.MatchString(v) {
		return v
	}
	return `"` + reCmdArgSpecial.ReplaceAllString(v, "^$1") + `"`
}

// processInvocation runs a Windows .cmd shim through cmd.exe.
func processInvocation(goos, command string, args []string) (string, []string) {
	if goos == "windows" && strings.HasSuffix(strings.ToLower(command), ".cmd") {
		quoted := make([]string, 0, len(args)+1)
		for _, v := range append([]string{command}, args...) {
			quoted = append(quoted, quoteCmdArg(v))
		}
		return "cmd.exe", []string{"/d", "/s", "/c", strings.Join(quoted, " ")}
	}
	return command, args
}

func containsUnexpectedSkip(text string) bool {
	for _, re := range unexpectedSkips {
		if re.MatchString(text) {
			return true
		}
	}
	return false
}

// procResult is what a command did.
type procResult struct {
	code     *int
	signal   *string
	stdout   string
	stderr   string
	timedOut bool
	notFound bool
	err      error
}

type statusOptions struct {
	allowSkip    bool
	expectedSkip bool
}

func resultStatus(r procResult, o statusOptions) string {
	switch {
	case r.notFound:
		return "BLOCKED"
	case r.err != nil, r.timedOut, r.code == nil || *r.code != 0:
		return "FAIL"
	}
	if !o.allowSkip && containsUnexpectedSkip(r.stdout+"\n"+r.stderr) {
		if o.expectedSkip {
			return "OUT_OF_SCOPE"
		}
		return "BLOCKED"
	}
	return "PASS"
}

// capture keeps what a command writes, redacted, up to maxCapture bytes,
// and passes it on.
type capture struct {
	mu   *sync.Mutex
	buf  strings.Builder
	full bool
	echo io.Writer
}

func (c *capture) Write(p []byte) (int, error) {
	safe := redactSensitive(string(p))
	c.mu.Lock()
	defer c.mu.Unlock()
	fmt.Fprint(c.echo, safe)
	if !c.full {
		if c.buf.Len()+len(safe) <= maxCapture {
			c.buf.WriteString(safe)
		} else {
			c.buf.WriteString(safe[:maxCapture-c.buf.Len()])
			c.buf.WriteString("\n...[output truncated]")
			c.full = true
		}
	}
	return len(p), nil
}

type runOptions struct {
	cwd     string
	env     map[string]string
	timeout time.Duration
	statusOptions
	required *bool
}

func (g *gate) runProcess(command string, args []string, o runOptions) procResult {
	name, argv := processInvocation(runtime.GOOS, command, args)
	timeout := o.timeout
	if timeout == 0 {
		timeout = commandTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, argv...)
	cmd.Dir = o.cwd
	if cmd.Dir == "" {
		cmd.Dir = g.root
	}
	cmd.Env = os.Environ()
	for k, v := range o.env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	cmd.Cancel = func() error { return killTree(cmd) }
	// A grandchild that keeps the output open does not hold the gate
	cmd.WaitDelay = 30 * time.Second
	var mu sync.Mutex
	out := &capture{mu: &mu, echo: g.stdout}
	errOut := &capture{mu: &mu, echo: g.stderr}
	cmd.Stdout, cmd.Stderr = out, errOut

	err := cmd.Run()
	r := procResult{stdout: out.buf.String(), stderr: errOut.buf.String(), timedOut: ctx.Err() == context.DeadlineExceeded}
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		r.code = intPtr(0)
	case errors.As(err, &exitErr):
		if code := exitErr.ExitCode(); code >= 0 {
			r.code = intPtr(code)
		} else if sig := exitSignal(exitErr); sig != "" {
			r.signal = &sig
		}
	case errors.Is(err, exec.ErrNotFound), errors.Is(err, fs.ErrNotExist):
		r.notFound = true
		r.err = err
	default:
		r.err = err
	}
	return r
}

func intPtr(n int) *int { return &n }

// killTree ends a command that ran out of time: on Windows with its
// child processes (taskkill /t), elsewhere with SIGTERM.
func killTree(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	if runtime.GOOS == "windows" {
		return exec.Command("taskkill.exe", "/pid", strconv.Itoa(cmd.Process.Pid), "/t", "/f").Run()
	}
	return terminate(cmd.Process)
}

func (g *gate) runCheck(id, label, command string, args []string, o runOptions) *gateResult {
	commandText := formatCommand(command, args)
	fmt.Fprintf(g.stdout, "\n=== %s ===\n$ %s\n", label, commandText)
	started := time.Now()
	r := g.runProcess(command, args, o)
	status := resultStatus(r, o.statusOptions)
	timeout := o.timeout
	if timeout == 0 {
		timeout = commandTimeout
	}
	var detail string
	switch {
	case r.notFound:
		detail = "Required command not found: " + command
	case r.timedOut:
		detail = fmt.Sprintf("Timed out after %d ms", timeout.Milliseconds())
	case r.err != nil:
		detail = r.err.Error()
	case r.code != nil && *r.code == 0:
		detail = "Completed successfully"
	case r.code != nil:
		detail = fmt.Sprintf("Exit code %d", *r.code)
	default:
		detail = "Exit code null"
	}
	cwd := o.cwd
	if cwd == "" {
		cwd = g.root
	}
	rec := &gateResult{
		ID:         id,
		Label:      label,
		Status:     status,
		Required:   (o.required == nil || *o.required) && status != "OUT_OF_SCOPE",
		Command:    commandText,
		Cwd:        cwd,
		StartedAt:  isoTime(started),
		DurationMs: time.Since(started).Milliseconds(),
		ExitCode:   r.code,
		Signal:     r.signal,
		Detail:     detail,
		Stdout:     r.stdout,
		Stderr:     r.stderr,
	}
	g.report.Results = append(g.report.Results, rec)
	fmt.Fprintf(g.stdout, "[%s] %s (%d ms): %s\n", status, label, rec.DurationMs, detail)
	return rec
}

// runRepotool runs one of repotool's own commands as a check.
func (g *gate) runRepotool(id, label string, args []string) *gateResult {
	command := "repotool"
	commandText := formatCommand(command, args)
	fmt.Fprintf(g.stdout, "\n=== %s ===\n$ %s\n", label, commandText)
	started := time.Now()
	var mu sync.Mutex
	out := &capture{mu: &mu, echo: g.stdout}
	errOut := &capture{mu: &mu, echo: g.stderr}
	code := run(append([]string{"--root", g.root}, args...), out, errOut)
	r := procResult{code: &code, stdout: out.buf.String(), stderr: errOut.buf.String()}
	status := resultStatus(r, statusOptions{})
	detail := "Completed successfully"
	if code != 0 {
		detail = fmt.Sprintf("Exit code %d", code)
	}
	rec := &gateResult{
		ID: id, Label: label, Status: status, Required: status != "OUT_OF_SCOPE",
		Command: commandText, Cwd: g.root, StartedAt: isoTime(started),
		DurationMs: time.Since(started).Milliseconds(), ExitCode: r.code,
		Detail: detail, Stdout: r.stdout, Stderr: r.stderr,
	}
	g.report.Results = append(g.report.Results, rec)
	fmt.Fprintf(g.stdout, "[%s] %s (%d ms): %s\n", status, label, rec.DurationMs, detail)
	return rec
}

type internalExtra struct {
	command  string
	notReq   bool
	exitCode *int
}

func (g *gate) addInternal(id, label, status, detail string, extra internalExtra) *gateResult {
	rec := &gateResult{
		ID: id, Label: label, Status: status, Required: !extra.notReq,
		Command: extra.command, Cwd: g.root, StartedAt: isoNow(),
		ExitCode: extra.exitCode, Detail: detail,
	}
	g.report.Results = append(g.report.Results, rec)
	fmt.Fprintf(g.stdout, "[%s] %s: %s\n", status, label, detail)
	return rec
}

// ---- Tools -------------------------------------------------------------

func commandCandidates(goos, name string) []string {
	if goos != "windows" {
		return []string{name}
	}
	if strings.HasSuffix(name, ".exe") || strings.HasSuffix(name, ".cmd") {
		return []string{name}
	}
	return []string{name + ".exe", name + ".cmd", name}
}

// executable is the name to run a tool by, or "" when it is not on the
// PATH. On Windows bash is Git's own bash when Git is installed.
func (g *gate) executable(name string) string {
	if runtime.GOOS == "windows" && name == "bash" {
		var roots []string
		for _, v := range []string{g.getenv("ProgramFiles"), g.getenv("ProgramFiles(x86)")} {
			if v != "" {
				roots = append(roots, filepath.Join(v, "Git"))
			}
		}
		roots = append(roots, `C:\Program Files\Git`)
		for _, root := range unique(roots) {
			candidate := filepath.Join(root, "bin", "bash.exe")
			if _, err := os.Stat(candidate); err == nil {
				return candidate
			}
		}
	}
	for _, candidate := range commandCandidates(runtime.GOOS, name) {
		if _, err := g.lookPath(candidate); err == nil {
			return candidate
		}
	}
	return ""
}

// commandVersion is what "tool --version" prints, or "".
func (g *gate) commandVersion(command string, args ...string) string {
	name, argv := processInvocation(runtime.GOOS, command, args)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, argv...)
	cmd.Dir = g.root
	var out, errOut strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Run(); err != nil {
		return ""
	}
	if out.Len() > 0 {
		return strings.TrimSpace(out.String())
	}
	return strings.TrimSpace(errOut.String())
}

type semver struct {
	Major int `json:"major"`
	Minor int `json:"minor"`
	Patch int `json:"patch"`
}

var reVersion = regexp.MustCompile(`(\d+)\.(\d+)(?:\.(\d+))?`)

func parseVersion(s string) *semver {
	m := reVersion.FindStringSubmatch(s)
	if m == nil {
		return nil
	}
	v := &semver{}
	v.Major, _ = strconv.Atoi(m[1])
	v.Minor, _ = strconv.Atoi(m[2])
	v.Patch, _ = strconv.Atoi(m[3])
	return v
}

func atLeastVersion(actual *semver, required semver) bool {
	switch {
	case actual == nil:
		return false
	case actual.Major != required.Major:
		return actual.Major > required.Major
	case actual.Minor != required.Minor:
		return actual.Minor > required.Minor
	}
	return actual.Patch >= required.Patch
}

// skippedNames are folders the file search never enters.
var skippedNames = []string{".git", ".cargo-cache", ".cache", ".local-test-results", ".venv", "__pycache__", "build", "dist", "node_modules", "target"}

// listFiles is every file under root, outside skippedNames and folders
// ending in _old, that keep says yes to.
func listFiles(root string, keep func(string) bool) []string {
	var out []string
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	for _, e := range entries {
		if slices.Contains(skippedNames, e.Name()) || strings.HasSuffix(e.Name(), "_old") {
			continue
		}
		full := filepath.Join(root, e.Name())
		if e.IsDir() {
			out = append(out, listFiles(full, keep)...)
		} else if e.Type().IsRegular() && keep(full) {
			out = append(out, full)
		}
	}
	return out
}

type securityManifests struct {
	pythonRequirements []string
	pythonProjects     []string
	rustProjects       []string
}

var reRequirements = regexp.MustCompile(`(?i)(^|[\\/])requirements[^\\/]*\.txt$`)

// discoverSecurityManifests finds the dependency manifests the audits
// read, in one walk of the tree.
func discoverSecurityManifests(root string) securityManifests {
	var m securityManifests
	listFiles(root, func(f string) bool {
		if reRequirements.MatchString(f) {
			m.pythonRequirements = append(m.pythonRequirements, f)
		}
		if strings.ToLower(filepath.Base(f)) == "pyproject.toml" {
			m.pythonProjects = append(m.pythonProjects, f)
		}
		if filepath.Base(f) == "Cargo.toml" {
			m.rustProjects = append(m.rustProjects, f)
		}
		return false
	})
	return m
}

var (
	reDependencies = regexp.MustCompile(`dependencies` + jsSpace + `*=` + jsSpace + `*\[((?s:.)*?)\]`)
	reQuoted       = regexp.MustCompile(`["']([^"']+)["']`)
)

// extractPyprojectDependencies is the project's dependency list of a
// pyproject.toml, for pip-audit.
func extractPyprojectDependencies(file string) ([]string, error) {
	text, err := readText(file)
	if err != nil {
		return nil, err
	}
	deps := []string{}
	m := reDependencies.FindStringSubmatch(text)
	if m == nil {
		return deps, nil
	}
	for _, line := range regexp.MustCompile(`\r?\n`).Split(m[1], -1) {
		if q := reQuoted.FindStringSubmatch(line); q != nil {
			deps = append(deps, q[1])
		}
	}
	return deps, nil
}

// ---- Stages --------------------------------------------------------------

type gateTools map[string]string

type preflightResult struct {
	ok        bool
	tools     gateTools
	manifests securityManifests
}

func (g *gate) preflight() preflightResult {
	tools := gateTools{}
	var missing []string
	manifests := discoverSecurityManifests(g.root)
	first := func(names ...string) string {
		for _, n := range names {
			if p := g.executable(n); p != "" {
				return p
			}
		}
		return ""
	}
	type tool struct{ name, command string }
	required := []tool{
		{"node", g.executable("node")},
		{"npm", g.executable("npm")},
		{"go", g.executable("go")},
		{"govulncheck", g.executable("govulncheck")},
		{"docker", g.executable("docker")},
		{"gitleaks", g.executable("gitleaks")},
		{"rg", g.executable("rg")},
		{"bash", g.executable("bash")},
		{"shellcheck", g.executable("shellcheck")},
		{"trivy", g.executable("trivy")},
		{"powershell", first("pwsh", "powershell")},
	}
	if len(manifests.pythonRequirements) > 0 || len(manifests.pythonProjects) > 0 {
		required = append(required, tool{"python", first("python", "python3")}, tool{"pip-audit", g.executable("pip-audit")})
	}
	if len(manifests.rustProjects) > 0 {
		required = append(required, tool{"cargo", g.executable("cargo")}, tool{"cargo-audit", g.executable("cargo-audit")})
	}
	for _, t := range required {
		if t.command == "" {
			missing = append(missing, t.name)
			g.addInternal("preflight-"+t.name, "Required tool: "+t.name, "BLOCKED", "Command not found", internalExtra{})
			continue
		}
		tools[t.name] = t.command
		versionArg := "--version"
		if t.name == "go" {
			versionArg = "version"
		}
		g.addInternal("preflight-"+t.name, "Required tool: "+t.name, "PASS", g.commandVersion(t.command, versionArg), internalExtra{})
	}

	if docker := tools["docker"]; docker != "" {
		if v := g.commandVersion(docker, "compose", "version"); v == "" {
			missing = append(missing, "docker compose")
			g.addInternal("preflight-docker-compose", "Docker Compose plugin", "BLOCKED", "docker compose version failed", internalExtra{})
		} else {
			g.addInternal("preflight-docker-compose", "Docker Compose plugin", "PASS", v, internalExtra{})
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		info := exec.CommandContext(ctx, docker, "info")
		info.Dir = g.root
		err := info.Run()
		cancel()
		if err != nil {
			missing = append(missing, "Docker daemon")
			g.addInternal("preflight-docker-daemon", "Docker daemon", "BLOCKED", "docker info failed", internalExtra{})
		} else {
			g.addInternal("preflight-docker-daemon", "Docker daemon", "PASS", "Docker daemon is available", internalExtra{})
		}
	}

	if node := tools["node"]; node != "" {
		version := g.commandVersion(node, "--version")
		if !atLeastVersion(parseVersion(version), semver{22, 0, 0}) {
			missing = append(missing, "Node.js >= 22")
			g.addInternal("preflight-node-version", "Node.js version", "BLOCKED", version+"; required >= 22.0.0", internalExtra{})
		} else {
			g.addInternal("preflight-node-version", "Node.js version", "PASS", version, internalExtra{})
		}
	}

	if goTool := tools["go"]; goTool != "" {
		version := parseVersion(g.commandVersion(goTool, "version"))
		text := "unknown"
		if version != nil {
			b, _ := json.Marshal(version)
			text = string(b)
		}
		if !atLeastVersion(version, semver{1, 25, 0}) {
			missing = append(missing, "Go >= 1.25")
			g.addInternal("preflight-go-version", "Go version", "BLOCKED", text+"; required >= 1.25", internalExtra{})
		} else {
			g.addInternal("preflight-go-version", "Go version", "PASS", text, internalExtra{})
		}
	}

	if len(missing) > 0 {
		g.addInternal("preflight-summary", "Required local environment", "BLOCKED",
			"Missing or unavailable: "+strings.Join(unique(missing), ", "), internalExtra{})
	} else {
		g.addInternal("preflight-summary", "Required local environment", "PASS", "All required tools and services are available", internalExtra{})
	}
	return preflightResult{ok: len(missing) == 0, tools: tools, manifests: manifests}
}

func (g *gate) path(parts ...string) string {
	return filepath.Join(append([]string{g.root}, parts...)...)
}

func (g *gate) runVersionAndDiffChecks() {
	g.runRepotool("version-parity", "Verify repository version parity", []string{"bump-version", "--verify"})
	g.runCheck("git-diff-check", "Check whitespace and patch errors", "git", []string{"diff", "--check"}, runOptions{timeout: time.Minute})
}

func (g *gate) runNodeChecks(tools gateTools) {
	install := []string{"ci", "--no-audit", "--no-fund"}
	g.runCheck("node-web-ui-install", "Install web app Node dependencies", tools["npm"], install, runOptions{cwd: g.path("web-ui")})
	g.runCheck("node-sdk-install", "Install Node SDK dependencies", tools["npm"], install, runOptions{cwd: g.path("sdks", "nodejs")})
	for _, a := range []struct{ id, dir, label string }{
		{"npm-audit-web-ui", g.path("web-ui"), "Audit web app npm dependencies"},
		{"npm-audit-sdk", g.path("sdks", "nodejs"), "Audit Node SDK dependencies"},
	} {
		g.runCheck(a.id, a.label, tools["npm"], []string{"audit", "--audit-level=moderate"}, runOptions{cwd: a.dir, timeout: securityTimeout})
	}
	g.runRepotool("i18n-parity", "Verify all locale keys and translations", []string{"i18n-check", "--system", "console"})
	g.runRepotool("provenance-policy", "Verify clean-room provenance policy", []string{"check-provenance"})
}

func (g *gate) runGoChecks(tools gateTools, postgresDSN string) {
	server := g.path("neodesk-server")
	var goEnv map[string]string
	if postgresDSN != "" {
		goEnv = map[string]string{"NEODESK_TEST_POSTGRES_DSN": postgresDSN}
	}
	g.runCheck("go-vet", "Go static analysis", tools["go"], []string{"vet", "./..."}, runOptions{cwd: server, env: goEnv})
	g.runCheck("go-tests-race", "Go tests with race detector", tools["go"], []string{"test", "-race", "-count=1", "./..."}, runOptions{cwd: server, env: goEnv})
	g.runCheck("go-vulnerability-scan", "Go dependency vulnerability scan", tools["govulncheck"], []string{"./..."}, runOptions{cwd: server, timeout: securityTimeout})

	exe := ""
	if runtime.GOOS == "windows" {
		exe = ".exe"
	}
	binary := filepath.Join(os.TempDir(), fmt.Sprintf("neodesk-server-local-gate-%d%s", os.Getpid(), exe))
	g.runCheck("go-build", "Build NeoDesk server binary", tools["go"], []string{"build", "-o", binary, "."}, runOptions{cwd: server})
	if _, err := os.Stat(binary); err == nil {
		g.runCheck("go-binary-help", "Verify NeoDesk server binary help", binary, []string{"--help"}, runOptions{cwd: server, timeout: 15 * time.Second})
	} else {
		g.addInternal("go-binary-help", "Verify NeoDesk server binary help", "BLOCKED", "Build did not create the expected binary", internalExtra{})
	}
	_ = os.Remove(binary)

	for _, target := range [][2]string{{"linux", "amd64"}, {"linux", "arm64"}, {"windows", "amd64"}} {
		goos, goarch := target[0], target[1]
		suffix := ""
		if goos == "windows" {
			suffix = ".exe"
		}
		output := filepath.Join(os.TempDir(), fmt.Sprintf("neodesk-server-%s-%s-%d%s", goos, goarch, os.Getpid(), suffix))
		g.runCheck("go-cross-"+goos+"-"+goarch, "Cross-compile Go server for "+goos+"/"+goarch, tools["go"],
			[]string{"build", "-o", output, "."}, runOptions{cwd: server, env: map[string]string{"GOOS": goos, "GOARCH": goarch}})
		_ = os.Remove(output)
	}

	if postgresDSN != "" {
		g.runCheck("go-postgres-tests", "Go PostgreSQL integration tests", tools["go"],
			[]string{"test", "./db/...", "-run", "Postgres", "-count=1"},
			runOptions{cwd: server, env: map[string]string{"NEODESK_TEST_POSTGRES_DSN": postgresDSN}})
	} else {
		g.addInternal("go-postgres-tests", "Go PostgreSQL integration tests", "BLOCKED", "No temporary PostgreSQL DSN was created", internalExtra{})
	}
}

func (g *gate) runPythonChecks(tools gateTools, m securityManifests) {
	python, sdk := tools["python"], g.path("sdks", "python")
	g.runCheck("python-compile", "Compile Python bridges and SDK", python,
		[]string{"-m", "compileall", "-q", g.path("bridges"), sdk}, runOptions{})
	g.runCheck("python-sdk-tests", "Run Python SDK tests", python,
		[]string{"-m", "unittest", "discover", "-s", filepath.Join(sdk, "tests"), "-p", "test_*.py"}, runOptions{cwd: g.root})
	for _, req := range m.pythonRequirements {
		g.runCheck("pip-audit-"+filepath.Base(filepath.Dir(req)), "Audit Python requirements "+relPath(g.root, req), tools["pip-audit"],
			[]string{"-r", req}, runOptions{cwd: g.root, timeout: securityTimeout})
	}
	for _, project := range m.pythonProjects {
		id := "pip-audit-project-" + filepath.Base(filepath.Dir(project))
		label := "Audit Python project " + relPath(g.root, project)
		deps, _ := extractPyprojectDependencies(project)
		if len(deps) == 0 {
			g.addInternal(id, label, "BLOCKED", "No project dependencies could be extracted", internalExtra{})
			continue
		}
		temp := filepath.Join(os.TempDir(), fmt.Sprintf("neodesk-python-deps-%d.txt", os.Getpid()))
		if err := os.WriteFile(temp, []byte(strings.Join(deps, "\n")+"\n"), 0o600); err != nil {
			g.addInternal(id, label, "FAIL", err.Error(), internalExtra{})
			continue
		}
		g.runCheck(id, label, tools["pip-audit"], []string{"-r", temp}, runOptions{cwd: g.root, timeout: securityTimeout})
		_ = os.Remove(temp)
	}
}

func (g *gate) runRustChecks(tools gateTools, m securityManifests) {
	var dirs []string
	for _, f := range m.rustProjects {
		if d := filepath.Dir(f); !slices.Contains(dirs, d) {
			dirs = append(dirs, d)
		}
	}
	for _, dir := range dirs {
		rel := relPath(g.root, dir)
		g.runCheck("cargo-audit-"+rel, "Audit Rust dependencies in "+rel, tools["cargo"], []string{"audit"}, runOptions{cwd: dir, timeout: securityTimeout})
		g.runCheck("cargo-test-"+rel, "Run Rust tests in "+rel, tools["cargo"], []string{"test", "--locked"}, runOptions{cwd: dir})
	}
}

func (g *gate) runInstallerChecks(tools gateTools) {
	files := slices.Clone(bashInstallerFiles)
	for _, f := range listFiles(g.root, func(f string) bool { return strings.HasSuffix(strings.ToLower(f), ".sh") }) {
		files = append(files, filepath.ToSlash(relPath(g.root, f)))
	}
	slices.Sort(files)
	files = slices.Compact(files)
	g.runCheck("bash-syntax", "Parse Bash installers and local wrappers", tools["bash"], append([]string{"-n"}, files...), runOptions{timeout: time.Minute})
	g.runCheck("shellcheck", "Run ShellCheck on installers and wrappers", tools["shellcheck"], append([]string{"--severity=error"}, files...), runOptions{timeout: 2 * time.Minute})

	for _, c := range [][3]string{
		{"installer-go-upgrade", "scripts/test-go-installer.sh", "Test the native installer's functions"},
		{"installer-go-toolchain", "scripts/check-go-toolchain-pins.sh", "Check the Go pins of the installers against go.mod"},
		{"installer-docker-update", "scripts/test-docker-installer.sh", "Test the Docker manager update and backup"},
		{"installer-supervisor", "scripts/check-supervisor-env.sh", "Check that the systemd unit and the image name their supervisor"},
		{"installer-native-recovery", "scripts/test-native-installer.sh", "Test native installer recovery"},
		{"installer-postgres-hba", "scripts/test-postgresql-hba.sh", "Test PostgreSQL HBA setup"},
	} {
		g.runCheck(c[0], c[2], tools["bash"], []string{c[1]}, runOptions{timeout: 2 * time.Minute})
	}
	for _, c := range [][3]string{
		{"installer-help", "install.sh", "Check install.sh help path"},
		{"neodesk-help", "neodesk.sh", "Check neodesk.sh help path"},
		{"docker-installer-help", "neodesk-docker.sh", "Check neodesk-docker.sh help path"},
	} {
		g.runCheck(c[0], c[2], tools["bash"], []string{c[1], "--help"}, runOptions{timeout: time.Minute})
	}
	g.runCheck("powershell-syntax", "Parse PowerShell installers", tools["powershell"],
		[]string{"-NoProfile", "-NonInteractive", "-File", g.path("scripts", "check-powershell-syntax.ps1")}, runOptions{timeout: time.Minute})
}

func (g *gate) runSecurityChecks(tools gateTools) {
	g.runCheck("gitleaks", "Scan repository for secrets with gitleaks", tools["gitleaks"], []string{
		"detect", "--source", ".", "--config", ".gitleaks.toml",
		"--exclude", ".local-test-results/**",
		"--exclude", "**/node_modules/**",
		"--exclude", "**/*_old/**",
		"--exclude", ".cargo-cache/**",
	}, runOptions{cwd: g.root, timeout: securityTimeout})
	g.runCheck("sensitive-paths", "Scan for operator-specific sensitive paths", tools["bash"],
		[]string{"scripts/check-no-sensitive-paths.sh"}, runOptions{cwd: g.root, timeout: 2 * time.Minute})
	g.runCheck("trivy-filesystem", "Scan source tree for vulnerabilities, secrets and misconfiguration", tools["trivy"], []string{
		"fs",
		"--scanners", "vuln,secret,misconfig",
		"--severity", "HIGH,CRITICAL",
		"--exit-code", "1",
		"--skip-dirs", ".git",
		"--skip-dirs", "node_modules",
		"--skip-dirs", ".local-test-results",
		"--skip-dirs", "*_old",
		"--skip-dirs", ".cargo-cache",
		".",
	}, runOptions{cwd: g.root, timeout: securityTimeout})
}

type dockerBuilds struct{ singleBuilt bool }

func (g *gate) runComposeConfigChecks(tools gateTools) dockerBuilds {
	for _, file := range composeFiles {
		full := g.path(file)
		if _, err := os.Stat(full); err != nil {
			g.addInternal("compose-config-"+file, "Validate "+file, "BLOCKED", "Compose file is missing", internalExtra{})
			continue
		}
		g.runCheck("compose-config-"+file, "Validate "+file, tools["docker"], []string{"compose", "-f", full, "config", "--quiet"}, runOptions{
			cwd:     g.root,
			env:     map[string]string{"PG_PASSWORD": postgresPassword, "MACVLAN_IPV4": "192.0.2.51"},
			timeout: 2 * time.Minute,
		})
	}
	single := g.runCheck("docker-build-single", "Build local single-container image", tools["docker"],
		[]string{"compose", "-f", g.path("docker-compose.single.yml"), "build", "neodesk"}, runOptions{cwd: g.root})
	g.runCheck("docker-build-server", "Build local server-only image", tools["docker"],
		[]string{"build", "-f", g.path("Dockerfile.server"), "-t", "neodesk-server:local", g.root}, runOptions{cwd: g.root})
	// There is no console image: the server serves the web console
	return dockerBuilds{singleBuilt: single.Status == "PASS"}
}

type postgresInstance struct{ name, dsn, containerDSN string }

var (
	reMappedPort = regexp.MustCompile(`(?m):(\d+)` + jsSpace + `*$`)
	reNotAlnum   = regexp.MustCompile(`(?i)[^a-z0-9]+`)
)

func (g *gate) startPostgres(tools gateTools) postgresInstance {
	p := postgresInstance{name: fmt.Sprintf("neodesk-local-gate-postgres-%d-%d", os.Getpid(), time.Now().UnixMilli())}
	start := g.runCheck("postgres-start", "Start isolated PostgreSQL container", tools["docker"], []string{
		"run", "--detach",
		"--name", p.name,
		"--label", "com.neodesk.local-gate=true",
		"--publish", "127.0.0.1::5432",
		"--env", "POSTGRES_USER=neodesk",
		"--env", "POSTGRES_DB=neodesk",
		"--env", "POSTGRES_PASSWORD",
		"postgres:16-alpine",
	}, runOptions{cwd: g.root, env: map[string]string{"POSTGRES_PASSWORD": postgresPassword}})
	if start.Status != "PASS" {
		return p
	}
	deadline := time.Now().Add(2 * time.Minute)
	ready := false
	for time.Now().Before(deadline) {
		probe := g.runProcess(tools["docker"], []string{"exec", p.name, "pg_isready", "-U", "neodesk", "-d", "neodesk"}, runOptions{timeout: 10 * time.Second})
		if probe.code != nil && *probe.code == 0 {
			ready = true
			break
		}
		time.Sleep(time.Second)
	}
	if !ready {
		g.addInternal("postgres-ready", "Wait for isolated PostgreSQL", "FAIL", "pg_isready did not succeed within 120 seconds", internalExtra{})
		return p
	}
	cmd := exec.Command(tools["docker"], "port", p.name, "5432/tcp")
	cmd.Dir = g.root
	out, _ := cmd.Output()
	m := reMappedPort.FindStringSubmatch(string(out))
	if m == nil {
		g.addInternal("postgres-ready", "Wait for isolated PostgreSQL", "FAIL", "Could not determine the mapped PostgreSQL port", internalExtra{})
		return p
	}
	p.dsn = "postgres://neodesk:" + postgresPassword + "@127.0.0.1:" + m[1] + "/neodesk?sslmode=disable"
	p.containerDSN = "postgres://neodesk:" + postgresPassword + "@host.docker.internal:" + m[1] + "/neodesk?sslmode=disable"
	g.addInternal("postgres-ready", "Wait for isolated PostgreSQL", "PASS", "PostgreSQL is ready on an ephemeral local port "+m[1], internalExtra{})
	return p
}

func (g *gate) runDockerRuntimeChecks(tools gateTools, p postgresInstance, builds dockerBuilds) {
	if !builds.singleBuilt {
		g.addInternal("docker-runtime-prerequisite", "Local Docker runtime prerequisite", "BLOCKED",
			"The single-container image build failed; runtime checks will not use a stale local image.", internalExtra{})
		return
	}
	g.runRepotool("docker-smoke-sqlite", "Smoke-test local Docker image with SQLite", []string{"docker-smoke", "--database-type", "sqlite"})
	if p.containerDSN != "" {
		g.runRepotool("docker-smoke-postgres", "Smoke-test local Docker image with PostgreSQL",
			[]string{"docker-smoke", "--database-type", "postgres", "--database-url", p.containerDSN})
	} else {
		g.addInternal("docker-smoke-postgres", "Smoke-test local Docker image with PostgreSQL", "BLOCKED", "PostgreSQL container was not ready", internalExtra{})
	}
	for _, image := range []string{"neodesk:local", "neodesk-server:local"} {
		g.runCheck("trivy-image-"+reNotAlnum.ReplaceAllString(image, "-"), "Scan local image "+image, tools["trivy"], []string{
			"image",
			"--scanners", "vuln,secret,misconfig",
			"--severity", "HIGH,CRITICAL",
			"--exit-code", "1",
			image,
		}, runOptions{cwd: g.root, timeout: securityTimeout})
	}
	g.runCheck("docker-hardening", "Test hardened Docker bind mounts and restart behavior", tools["bash"],
		[]string{"scripts/test-docker-hardening.sh"},
		runOptions{cwd: g.root, env: map[string]string{"NEODESK_HARDENING_IMAGE": "neodesk:local"}})
}

func (g *gate) runFullNodeTests(tools gateTools) {
	g.runCheck("web-ui-tests", "Run the web app unit tests", tools["npm"], []string{"run", "test"}, runOptions{cwd: g.path("web-ui")})
	g.runCheck("web-ui-check", "Type-check the web app", tools["npm"], []string{"run", "check"}, runOptions{cwd: g.path("web-ui")})
	g.runCheck("node-sdk-tests", "Run Node.js SDK tests", tools["npm"], []string{"test"}, runOptions{cwd: g.path("sdks", "nodejs"), timeout: 2 * time.Minute})
}

// ---- Report --------------------------------------------------------------

type reportPaths struct{ jsonPath, markdownPath, latestJSON, latestMarkdown string }

func writeReports(r *gateReport, dir string) (reportPaths, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return reportPaths{}, err
	}
	stamp := strings.NewReplacer(":", "-", ".", "-").Replace(isoNow())
	p := reportPaths{
		jsonPath:       filepath.Join(dir, "local-gate-"+stamp+".json"),
		markdownPath:   filepath.Join(dir, "local-gate-"+stamp+".md"),
		latestJSON:     filepath.Join(dir, "latest.json"),
		latestMarkdown: filepath.Join(dir, "latest.md"),
	}
	var buf strings.Builder
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(r); err != nil {
		return p, err
	}
	finished := "in progress"
	if r.FinishedAt != nil {
		finished = *r.FinishedAt
	}
	lines := []string{
		"# NeoDesk local gate",
		"",
		"- Overall: **" + r.Overall + "**",
		"- Branch: `" + r.Repository.Branch + "`",
		"- Commit: `" + r.Repository.SHA + "`",
		"- Host: `" + r.Environment.Platform + " " + r.Environment.Release + "`",
		"- Started: " + r.StartedAt,
		"- Finished: " + finished,
		"",
		"## Checks",
		"",
	}
	for _, res := range r.Results {
		lines = append(lines, "- **"+res.Status+"** `"+res.ID+"` — "+res.Label+": "+res.Detail)
	}
	lines = append(lines, "", "## Explicit boundaries", "")
	for _, e := range r.OutOfScope {
		lines = append(lines, "- **OUT_OF_SCOPE** "+e.Area+": "+e.Reason)
	}
	lines = append(lines, "",
		"The report is local-only. It does not prove external client, physical-device, or disposable-host lifecycle behavior.",
		"")
	markdown := strings.Join(lines, "\n")
	for path, text := range map[string]string{p.jsonPath: buf.String(), p.markdownPath: markdown, p.latestJSON: buf.String(), p.latestMarkdown: markdown} {
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			return p, err
		}
	}
	return p, nil
}

// calculateOverall is FAIL when a required check failed, else BLOCKED
// when one was blocked, else PASS.
func calculateOverall(results []*gateResult) string {
	overall := "PASS"
	for _, r := range results {
		if !r.Required {
			continue
		}
		switch r.Status {
		case "FAIL":
			return "FAIL"
		case "BLOCKED":
			overall = "BLOCKED"
		}
	}
	return overall
}

func (g *gate) printSummary(p reportPaths) {
	var order []string
	counts := map[string]int{}
	for _, r := range g.report.Results {
		if counts[r.Status] == 0 {
			order = append(order, r.Status)
		}
		counts[r.Status]++
	}
	parts := make([]string, len(order))
	for i, s := range order {
		parts[i] = fmt.Sprintf("%s=%d", s, counts[s])
	}
	fmt.Fprintf(g.stdout, "\nLOCAL GATE: %s\n%s\nJSON report: %s\nMarkdown report: %s\n",
		g.report.Overall, strings.Join(parts, " "), p.latestJSON, p.latestMarkdown)
}

// unique is list without repeated values, in first-seen order.
func unique(list []string) []string {
	var out []string
	for _, v := range list {
		if !slices.Contains(out, v) {
			out = append(out, v)
		}
	}
	return out
}
