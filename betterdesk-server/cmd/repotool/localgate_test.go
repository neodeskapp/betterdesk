package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

// The test binary doubles as the command the gate runs: with
// REPOTOOL_TEST_HELPER set it acts out that case and exits.
func TestMain(m *testing.M) {
	switch os.Getenv("REPOTOOL_TEST_HELPER") {
	case "":
		os.Exit(m.Run())
	case "secret":
		fmt.Println("connect postgres://user:hunter2@db:5432/x password=plain-secret")
		fmt.Fprintln(os.Stderr, "Authorization: Bearer bearer-secret")
		os.Exit(3)
	case "skip":
		fmt.Println("--- SKIP: Docker is not available")
	case "sleep":
		time.Sleep(time.Minute)
	case "big":
		fmt.Print(strings.Repeat("x", maxCapture+100))
	}
	os.Exit(0)
}

func newTestGate(t *testing.T, root string) (*gate, *bytes.Buffer) {
	t.Helper()
	var out bytes.Buffer
	g := &gate{
		root:     root,
		stdout:   &out,
		stderr:   &out,
		getenv:   func(string) string { return "" },
		lookPath: func(string) (string, error) { return "", errors.New("not found") },
		report:   &gateReport{Results: []*gateResult{}},
	}
	return g, &out
}

func helper(mode string, o runOptions) runOptions {
	o.env = map[string]string{"REPOTOOL_TEST_HELPER": mode}
	return o
}

func TestParseGateArgs(t *testing.T) {
	root := t.TempDir()
	noEnv := func(string) string { return "" }
	opts, err := parseGateArgs([]string{"--dry-run", "--report-dir", filepath.Join(".local-test-results", "test")}, noEnv, root)
	if err != nil || !opts.dryRun || opts.preflightOnly {
		t.Fatalf("got %+v, %v", opts, err)
	}
	if !filepath.IsAbs(opts.reportDir) || !strings.HasSuffix(opts.reportDir, filepath.Join("local-test-results", "test")) {
		t.Errorf("report dir %q", opts.reportDir)
	}
	if opts, _ := parseGateArgs(nil, noEnv, root); opts.reportDir != filepath.Join(root, ".local-test-results") {
		t.Errorf("default report dir %q", opts.reportDir)
	}
	env := func(k string) string {
		if k == "BETTERDESK_LOCAL_GATE_REPORT_DIR" {
			return "reports"
		}
		return ""
	}
	if opts, _ := parseGateArgs(nil, env, root); opts.reportDir != filepath.Join(root, "reports") {
		t.Errorf("report dir from the environment %q", opts.reportDir)
	}
	if opts, _ := parseGateArgs([]string{"--preflight-only", "-h"}, noEnv, root); !opts.preflightOnly || !opts.help {
		t.Errorf("got %+v", opts)
	}
	if _, err := parseGateArgs([]string{"--fast"}, noEnv, root); err == nil || err.Error() != "Unknown option: --fast" {
		t.Errorf("unknown option: %v", err)
	}
	r := runCommand(localGate, root, "--fast")
	r.want(t, 1, "", "LOCAL GATE FAILED: Unknown option: --fast\n")
}

func TestFormatCommandRedactsCredentials(t *testing.T) {
	got := formatCommand("repotool", []string{"docker-smoke", "--database-url", "postgres://user:password@example.test:5432/db?sslmode=disable"})
	if want := "repotool docker-smoke --database-url postgres://user:[REDACTED]@example.test:5432/db?sslmode=disable"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if got := formatCommand("go", []string{"build", "-o", `C:\Program Files\x "y"`}); got != `go build -o "C:\Program Files\x \"y\""` {
		t.Errorf("quoting: %q", got)
	}
}

func TestRedactSensitive(t *testing.T) {
	got := redactSensitive("password=plain-secret token:plain-token Authorization: Bearer bearer-secret \"api_key\": \"k1\" postgres://u:pw@h/db")
	for _, secret := range []string{"plain-secret", "plain-token", "bearer-secret", "k1", ":pw@"} {
		if strings.Contains(got, secret) {
			t.Errorf("%q left in %q", secret, got)
		}
	}
	if want := `password=[REDACTED] token:[REDACTED] Authorization: Bearer [REDACTED] "api_key": "[REDACTED]" postgres://u:[REDACTED]@h/db`; got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
}

func TestProcessInvocation(t *testing.T) {
	name, args := processInvocation("windows", "npm.cmd", []string{"--version", "a b", "x&y"})
	if name != "cmd.exe" || !reflect.DeepEqual(args, []string{"/d", "/s", "/c", `npm.cmd --version "a b" "x^&y"`}) {
		t.Errorf("windows: %s %q", name, args)
	}
	if name, args := processInvocation("windows", "go.exe", []string{"version"}); name != "go.exe" || len(args) != 1 {
		t.Errorf("windows exe: %s %q", name, args)
	}
	if name, _ := processInvocation("linux", "npm.cmd", nil); name != "npm.cmd" {
		t.Errorf("linux: %s", name)
	}
	if got := commandCandidates("windows", "npm"); !reflect.DeepEqual(got, []string{"npm.exe", "npm.cmd", "npm"}) {
		t.Errorf("candidates %q", got)
	}
}

func TestSkipsBlockUnlessAllowed(t *testing.T) {
	if !containsUnexpectedSkip("--- SKIP: Docker is not available") || containsUnexpectedSkip("Tests: 12 passed, 0 skipped") {
		t.Error("skip detection")
	}
	for _, text := range []string{"3 skipped", "tests skipped", "no tests to run", "SKIPPED : x"} {
		if !containsUnexpectedSkip(text) {
			t.Errorf("%q is a skip", text)
		}
	}
	zero := 0
	skipped := procResult{code: &zero, stdout: "SKIP: unavailable"}
	for _, c := range []struct {
		r    procResult
		o    statusOptions
		want string
	}{
		{skipped, statusOptions{}, "BLOCKED"},
		{skipped, statusOptions{allowSkip: true}, "PASS"},
		{procResult{code: &zero, stdout: "1 skipped"}, statusOptions{expectedSkip: true}, "OUT_OF_SCOPE"},
		{procResult{notFound: true}, statusOptions{}, "BLOCKED"},
		{procResult{code: intPtr(2)}, statusOptions{}, "FAIL"},
		{procResult{}, statusOptions{}, "FAIL"},
		{procResult{code: &zero, timedOut: true}, statusOptions{}, "FAIL"},
		{procResult{code: &zero}, statusOptions{}, "PASS"},
	} {
		if got := resultStatus(c.r, c.o); got != c.want {
			t.Errorf("resultStatus(%+v, %+v) = %s, want %s", c.r, c.o, got, c.want)
		}
	}
}

func TestVersions(t *testing.T) {
	if v := parseVersion("go version go1.26.6 windows/amd64"); !reflect.DeepEqual(v, &semver{1, 26, 6}) {
		t.Errorf("go version: %+v", v)
	}
	if v := parseVersion("v22.1"); !reflect.DeepEqual(v, &semver{22, 1, 0}) {
		t.Errorf("v22.1: %+v", v)
	}
	if !atLeastVersion(parseVersion("v22.1.0"), semver{22, 0, 0}) || atLeastVersion(parseVersion("v21.9.0"), semver{22, 0, 0}) {
		t.Error("node versions")
	}
	if atLeastVersion(nil, semver{}) || !atLeastVersion(&semver{1, 25, 0}, semver{1, 25, 0}) || atLeastVersion(&semver{1, 24, 9}, semver{1, 25, 0}) {
		t.Error("edges")
	}
}

func TestSecurityManifestsOfTheRepository(t *testing.T) {
	root := repoRoot(t)
	m := discoverSecurityManifests(root)
	has := func(list []string, rel string) bool {
		for _, f := range list {
			if f == filepath.Join(root, filepath.FromSlash(rel)) {
				return true
			}
		}
		return false
	}
	if !has(m.pythonRequirements, "bridges/modbus/requirements.txt") || !has(m.pythonProjects, "sdks/python/pyproject.toml") {
		t.Errorf("manifests: %+v", m)
	}
	if len(m.rustProjects) != 0 {
		t.Errorf("rust projects: %v", m.rustProjects)
	}
	deps, err := extractPyprojectDependencies(filepath.Join(root, "sdks", "python", "pyproject.toml"))
	if err != nil || !reflect.DeepEqual(deps, []string{"websockets>=12.0"}) {
		t.Errorf("dependencies %v, %v", deps, err)
	}
}

func TestListFilesSkipsBuildFolders(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"a/requirements.txt":                   "",
		"b/requirements-dev.TXT":               "",
		"node_modules/x/requirements.txt":      "",
		"build/requirements.txt":               "",
		"old_old/requirements.txt":             "",
		".cache/requirements.txt":              "",
		"c/target/requirements.txt":            "",
		"d/requirements/notes.txt":             "",
		"e/Cargo.toml":                         "",
		"f/PyProject.toml":                     "",
		"g/sub/dist/pyproject.toml":            "",
		"h/__pycache__/requirements.txt":       "",
		".local-test-results/requirements.txt": "",
	})
	m := discoverSecurityManifests(root)
	rel := func(list []string) []string {
		out := []string{}
		for _, f := range list {
			out = append(out, filepath.ToSlash(relPath(root, f)))
		}
		return out
	}
	if got := rel(m.pythonRequirements); !reflect.DeepEqual(got, []string{"a/requirements.txt", "b/requirements-dev.TXT"}) {
		t.Errorf("requirements %v", got)
	}
	if got := rel(m.pythonProjects); !reflect.DeepEqual(got, []string{"f/PyProject.toml"}) {
		t.Errorf("projects %v", got)
	}
	if got := rel(m.rustProjects); !reflect.DeepEqual(got, []string{"e/Cargo.toml"}) {
		t.Errorf("rust %v", got)
	}
}

func TestCalculateOverall(t *testing.T) {
	results := []*gateResult{{Status: "PASS", Required: true}, {Status: "OUT_OF_SCOPE"}, {Status: "FAIL"}}
	if got := calculateOverall(results); got != "PASS" {
		t.Errorf("got %s", got)
	}
	results = append(results, &gateResult{Status: "BLOCKED", Required: true})
	if got := calculateOverall(results); got != "BLOCKED" {
		t.Errorf("got %s", got)
	}
	results = append(results, &gateResult{Status: "FAIL", Required: true})
	if got := calculateOverall(results); got != "FAIL" {
		t.Errorf("got %s", got)
	}
	// A failure counts over a later blocked check
	results = append(results, &gateResult{Status: "BLOCKED", Required: true})
	if got := calculateOverall(results); got != "FAIL" {
		t.Errorf("failed then blocked: got %s", got)
	}
}

func TestGateDryRun(t *testing.T) {
	root := t.TempDir()
	g, out := newTestGate(t, root)
	report := g.run(gateOptions{dryRun: true, reportDir: filepath.Join(root, "reports")})
	if !report.DryRun || report.Overall != "PASS" || len(report.Results) != 0 {
		t.Errorf("report %+v", report)
	}
	if !strings.HasPrefix(out.String(), "Dry run: full local gate categories:\n- preflight and version checks\n") {
		t.Errorf("output:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(root, "reports")); err == nil {
		t.Error("a dry run wrote reports")
	}
	runCommand(localGate, root, "--dry-run").want(t, 0, out.String(), "")
}

func TestRunCheck(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	g, out := newTestGate(t, t.TempDir())

	rec := g.runCheck("secret", "A failing check", self, []string{"-test.run=^$"}, helper("secret", runOptions{}))
	if rec.Status != "FAIL" || rec.Detail != "Exit code 3" || rec.ExitCode == nil || *rec.ExitCode != 3 || !rec.Required {
		t.Errorf("record %+v", rec)
	}
	if rec.Stdout != "connect postgres://user:[REDACTED]@db:5432/x password=[REDACTED]\n" || rec.Stderr != "Authorization: Bearer [REDACTED]\n" {
		t.Errorf("captured %q %q", rec.Stdout, rec.Stderr)
	}
	for _, secret := range []string{"hunter2", "plain-secret", "bearer-secret"} {
		if strings.Contains(out.String(), secret) {
			t.Errorf("%s reached the output", secret)
		}
	}
	if !strings.Contains(out.String(), "\n=== A failing check ===\n$ "+formatCommand(self, []string{"-test.run=^$"})+"\n") ||
		!strings.Contains(out.String(), "[FAIL] A failing check (") {
		t.Errorf("output:\n%s", out)
	}

	if rec := g.runCheck("skip", "Skipped", self, nil, helper("skip", runOptions{})); rec.Status != "BLOCKED" || !rec.Required {
		t.Errorf("skip: %+v", rec)
	}
	if rec := g.runCheck("skip", "Skipped", self, nil, helper("skip", runOptions{statusOptions: statusOptions{expectedSkip: true}})); rec.Status != "OUT_OF_SCOPE" || rec.Required {
		t.Errorf("expected skip: %+v", rec)
	}
	no := false
	if rec := g.runCheck("ok", "Fine", self, nil, helper("ok", runOptions{required: &no})); rec.Status != "PASS" || rec.Detail != "Completed successfully" || rec.Required {
		t.Errorf("ok: %+v", rec)
	}
	if rec := g.runCheck("missing", "Missing", "no-such-tool-here", nil, runOptions{}); rec.Status != "BLOCKED" || rec.Detail != "Required command not found: no-such-tool-here" || rec.ExitCode != nil {
		t.Errorf("missing: %+v", rec)
	}

	rec = g.runCheck("slow", "Slow", self, nil, helper("sleep", runOptions{timeout: 300 * time.Millisecond}))
	if rec.Status != "FAIL" || rec.Detail != "Timed out after 300 ms" || rec.ExitCode != nil {
		t.Errorf("timeout: %+v", rec)
	}
	if runtime.GOOS != "windows" && (rec.Signal == nil || *rec.Signal != "SIGTERM") {
		t.Errorf("timeout signal %v", rec.Signal)
	}

	rec = g.runCheck("big", "Big", self, nil, helper("big", runOptions{}))
	if len(rec.Stdout) != maxCapture+len("\n...[output truncated]") || !strings.HasSuffix(rec.Stdout, "x\n...[output truncated]") {
		t.Errorf("big output kept %d bytes", len(rec.Stdout))
	}
	if len(g.report.Results) != 7 {
		t.Errorf("%d results", len(g.report.Results))
	}
}

func TestRunRepotool(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, provenanceTree())
	g, out := newTestGate(t, root)
	rec := g.runRepotool("provenance-policy", "Verify clean-room provenance policy", []string{"check-provenance"})
	if rec.Status != "PASS" || rec.Command != "repotool check-provenance" || rec.Stdout != "Clean-room provenance policy check passed.\n" || *rec.ExitCode != 0 {
		t.Errorf("record %+v", rec)
	}
	if !strings.Contains(out.String(), "Clean-room provenance policy check passed.\n[PASS] Verify clean-room provenance policy (") {
		t.Errorf("output:\n%s", out)
	}
	writeTree(t, root, map[string]string{"THIRD_PARTY_NOTICES.md": "x"})
	if rec := g.runRepotool("provenance-policy", "Verify", []string{"check-provenance"}); rec.Status != "FAIL" || rec.Detail != "Exit code 1" {
		t.Errorf("failing: %+v", rec)
	}
}

func TestPreflightWithoutTools(t *testing.T) {
	root := t.TempDir()
	g, _ := newTestGate(t, root)
	if p := g.preflight(); p.ok {
		t.Fatal("preflight passed without tools")
	}
	last := g.report.Results[len(g.report.Results)-1]
	if last.ID != "preflight-summary" || last.Status != "BLOCKED" ||
		last.Detail != "Missing or unavailable: node, npm, go, govulncheck, docker, gitleaks, rg, bash, shellcheck, trivy, powershell" {
		t.Errorf("summary %+v", last)
	}
	if first := g.report.Results[0]; first.ID != "preflight-node" || first.Label != "Required tool: node" || first.Detail != "Command not found" {
		t.Errorf("first %+v", first)
	}

	// Python manifests make Python and pip-audit required
	writeTree(t, root, map[string]string{"bridges/x/requirements.txt": ""})
	g, _ = newTestGate(t, root)
	g.preflight()
	if last := g.report.Results[len(g.report.Results)-1]; !strings.HasSuffix(last.Detail, "powershell, python, pip-audit") {
		t.Errorf("summary %+v", last)
	}
}

func TestGateRefusesGitHubActions(t *testing.T) {
	root := t.TempDir()
	g, out := newTestGate(t, root)
	g.getenv = func(k string) string {
		if k == "GITHUB_ACTIONS" {
			return "true"
		}
		return ""
	}
	dir := filepath.Join(root, "reports")
	report := g.run(gateOptions{reportDir: dir})
	if report.Overall != "BLOCKED" || len(report.Results) != 1 || report.Results[0].ID != "local-only-guard" {
		t.Fatalf("report %+v", report)
	}
	if !strings.Contains(out.String(), "[BLOCKED] Local-only execution guard: The local gate is intentionally disabled inside GitHub Actions.\n\nLOCAL GATE: BLOCKED\nBLOCKED=1\nJSON report: "+filepath.Join(dir, "latest.json")+"\nMarkdown report: "+filepath.Join(dir, "latest.md")+"\n") {
		t.Errorf("output:\n%s", out)
	}

	data, err := os.ReadFile(filepath.Join(dir, "latest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var written map[string]json.RawMessage
	if err := json.Unmarshal(data, &written); err != nil {
		t.Fatal(err)
	}
	var keys []string
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.Token()
	for dec.More() {
		k, _ := dec.Token()
		keys = append(keys, k.(string))
		var skip json.RawMessage
		dec.Decode(&skip)
	}
	if want := []string{"schemaVersion", "localOnly", "startedAt", "finishedAt", "repository", "environment", "results", "outOfScope", "overall"}; !reflect.DeepEqual(keys, want) {
		t.Errorf("report keys %v", keys)
	}
	if !strings.Contains(string(data), `"exitCode": null,
      "signal": null,`) {
		t.Errorf("report:\n%s", data)
	}

	md, err := os.ReadFile(filepath.Join(dir, "latest.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"# BetterDesk local gate\n\n- Overall: **BLOCKED**\n- Branch: `unknown`\n",
		"## Checks\n\n- **BLOCKED** `local-only-guard` — Local-only execution guard: The local gate is intentionally disabled inside GitHub Actions.\n\n## Explicit boundaries\n\n",
		"- **OUT_OF_SCOPE** Linux-only tests on a Windows host: Systemd/privileged Linux checks cannot run natively on Windows without changing a host or VM.\n\nThe report is local-only.",
	} {
		if !strings.Contains(string(md), want) {
			t.Errorf("markdown lacks %q:\n%s", want, md)
		}
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 4 {
		t.Errorf("report files: %v", entries)
	}
}

func TestVersionCheckRunsTheScriptWithNode(t *testing.T) {
	g, _ := newTestGate(t, t.TempDir())
	g.runVersionAndDiffChecks(gateTools{"node": "no-such-node-here", "npm": "no-such-npm-here"})
	rec := g.report.Results[0]
	if rec.ID != "version-parity" || rec.Command != "no-such-node-here scripts/bump-version.js --verify" || rec.Status != "BLOCKED" {
		t.Errorf("version check %+v", rec)
	}
	if rec := g.report.Results[1]; rec.ID != "git-diff-check" || rec.Command != "git diff --check" {
		t.Errorf("diff check %+v", rec)
	}
}
