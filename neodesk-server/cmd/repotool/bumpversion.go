package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// bumpVersion is "bump-version": the product version lives in VERSION
// and is repeated in the files of versionRules. --verify checks that they
// all name VERSION's version, --sync writes it into those that drifted,
// and --patch, --minor and --set X.Y.Z bump it everywhere and open its
// section in CHANGELOG.md. A version may carry a pre-release suffix
// (3.6.0-rc.1, for a release candidate tagged v3.6.0-rc.1); --patch and
// --minor bump its numbers and drop the suffix. --list-paths prints the files the bump writes,
// for "git add" in the bump workflows.
func bumpVersion(root string, args []string, stdout, stderr io.Writer) int {
	b := versionBump{root: root, stdout: stdout, stderr: stderr, now: time.Now, getenv: os.Getenv}
	code, err := b.run(args)
	if err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}
	return code
}

const bumpUsage = `Usage: go run ./cmd/repotool bump-version [--patch | --minor | --set X.Y.Z[-suffix]] [--dry-run] [--verify] [--list-paths]

  --patch       Bump patch (+0.0.1; 3.6.0-rc.1 becomes 3.6.1)
  --minor       Bump minor (+0.1.0, reset patch)
  --set X.Y.Z   Set explicit version, or X.Y.Z-suffix for a pre-release (3.6.0-rc.1)
  --verify      Exit 1 if Tier 1/2 files disagree with VERSION
  --sync        Align drifted Tier 1/2 files to canonical VERSION (no bump)
  --list-paths  Print managed file paths (one per line) for CI git add
  --dry-run     Print planned version without writing files
`

// reSemver is a version: X.Y.Z, with a pre-release suffix of semver's
// dot-separated identifiers (3.6.0-rc.1) or without.
var reSemver = regexp.MustCompile(`^(\d+)\.(\d+)\.(\d+)(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$`)

// versionRule is a file that carries the version: extract reads it (""
// when the file names none), apply writes another.
type versionRule struct {
	path    string
	extract func(content string) string
	apply   func(content, version string) string
}

var (
	reShellVersion      = regexp.MustCompile(`(?m)^VERSION="([^"]+)"`)
	rePowerShellVersion = regexp.MustCompile(`(?m)^\$script:VERSION = "([^"]+)"`)
	reLabelVersion      = regexp.MustCompile(`LABEL version="([^"]+)"`)
	reInstallVersion    = regexp.MustCompile(`NEODESK_VERSION="\$\{NEODESK_VERSION:-([^}]+)\}"`)
	reInstallBaseline   = regexp.MustCompile(`(Docker image tag / release baseline \(default: )[^)]+(\))`)
	reImageVersion      = regexp.MustCompile(`\$\{NEODESK_IMAGE_VERSION:-([^}]+)\}`)
)

// versionRules are the files besides VERSION that name the version.
// README.md is not one of them: its version badge is a picture
// (docs/assets/badges/*/version.svg) that is redrawn by hand for a
// release. Nor are the quick-start compose files, whose image tag is
// "latest" unless NEODESK_IMAGE_TAG pins a release.
var versionRules = []versionRule{
	{path: "VERSION", extract: jsTrim, apply: wholeFile},
	{path: "neodesk.sh", extract: firstGroup(reShellVersion), apply: managerVersion(reShellVersion, `VERSION="%s"`)},
	{path: "neodesk.ps1", extract: firstGroup(rePowerShellVersion), apply: managerVersion(rePowerShellVersion, `$script:VERSION = "%s"`)},
	{path: "neodesk-docker.sh", extract: firstGroup(reShellVersion), apply: managerVersion(reShellVersion, `VERSION="%s"`)},
	{path: "Dockerfile", extract: firstGroup(reLabelVersion), apply: replaceFirstOf(reLabelVersion, `LABEL version="%s"`)},
	{path: "Dockerfile.server", extract: firstGroup(reLabelVersion), apply: replaceFirstOf(reLabelVersion, `LABEL version="%s"`)},
	{path: "install.sh", extract: firstGroup(reInstallVersion), apply: installVersion},
	{path: "docker/entrypoint.sh", extract: firstGroup(reImageVersion), apply: replaceFirstOf(reImageVersion, `${NEODESK_IMAGE_VERSION:-%s}`)},
	{path: "neodesk-server/VERSION", extract: jsTrim, apply: wholeFile},
	{path: "neodesk-server/internal/productversion/VERSION", extract: jsTrim, apply: wholeFile},
}

const changelogPath = "CHANGELOG.md"

func wholeFile(_, version string) string { return version + "\n" }

func firstGroup(re *regexp.Regexp) func(string) string {
	return func(content string) string {
		if m := re.FindStringSubmatch(content); m != nil {
			return m[1]
		}
		return ""
	}
}

// managerVersion is the rule of the manager scripts: every copy of the
// version line and of the banner "NeoDesk Console Manager vX" changes.
func managerVersion(re *regexp.Regexp, line string) func(string, string) string {
	return func(content, version string) string {
		old := firstGroup(re)(content)
		if old == "" || old == version {
			return content
		}
		for _, pair := range [][2]string{
			{fmt.Sprintf(line, old), fmt.Sprintf(line, version)},
			{"NeoDesk Console Manager v" + old, "NeoDesk Console Manager v" + version},
		} {
			content = strings.ReplaceAll(content, pair[0], pair[1])
		}
		return content
	}
}

// replaceFirstOf changes the first text of the version (format with the
// old version) into the new one.
func replaceFirstOf(re *regexp.Regexp, text string) func(string, string) string {
	return func(content, version string) string {
		old := firstGroup(re)(content)
		if old == "" || old == version {
			return content
		}
		return strings.Replace(content, fmt.Sprintf(text, old), fmt.Sprintf(text, version), 1)
	}
}

// installVersion writes install.sh's default version and the one its help
// names, whatever they were.
func installVersion(content, version string) string {
	if loc := reInstallVersion.FindStringIndex(content); loc != nil {
		content = content[:loc[0]] + `NEODESK_VERSION="${NEODESK_VERSION:-` + version + `}"` + content[loc[1]:]
	}
	if m := reInstallBaseline.FindStringSubmatchIndex(content); m != nil {
		content = content[:m[0]] + content[m[2]:m[3]] + version + content[m[4]:m[5]] + content[m[1]:]
	}
	return content
}

// versionBump runs the command; now and getenv are the clock and the
// environment, which tests replace.
type versionBump struct {
	root           string
	stdout, stderr io.Writer
	now            func() time.Time
	getenv         func(string) string
}

func (b versionBump) run(args []string) (int, error) {
	dryRun := slices.Contains(args, "--dry-run")
	prefix := ""
	if dryRun {
		prefix = "[dry-run] "
	}

	if slices.Contains(args, "--list-paths") {
		for _, r := range versionRules {
			fmt.Fprintln(b.stdout, r.path)
		}
		fmt.Fprintln(b.stdout, changelogPath)
		return 0, nil
	}
	if slices.Contains(args, "--verify") {
		return b.verify()
	}
	if slices.Contains(args, "--sync") {
		canonical, err := b.canonical()
		if err != nil {
			return 1, err
		}
		changed, err := b.applyToFiles(canonical, dryRun)
		if err != nil {
			return 1, err
		}
		if len(changed) > 0 {
			fmt.Fprintf(b.stdout, "%sSynced drifted files to VERSION (%s):\n", prefix, canonical)
			for _, f := range changed {
				fmt.Fprintf(b.stdout, "  - %s\n", f)
			}
		} else {
			fmt.Fprintf(b.stdout, "%sAll Tier 1/2 files already match VERSION (%s).\n", prefix, canonical)
		}
		return 0, nil
	}

	next := ""
	switch {
	case slices.Contains(args, "--patch"), slices.Contains(args, "--minor"):
		canonical, err := b.canonical()
		if err != nil {
			return 1, err
		}
		m := reSemver.FindStringSubmatch(canonical)
		major, _ := strconv.Atoi(m[1])
		minor, _ := strconv.Atoi(m[2])
		patch, _ := strconv.Atoi(m[3])
		if slices.Contains(args, "--patch") {
			patch++
		} else {
			minor, patch = minor+1, 0
		}
		next = fmt.Sprintf("%d.%d.%d", major, minor, patch)
	default:
		if i := slices.Index(args, "--set"); i >= 0 && i+1 < len(args) && args[i+1] != "" {
			next = jsTrim(args[i+1])
			if !reSemver.MatchString(next) {
				fmt.Fprintln(b.stderr, "Invalid --set version (expected X.Y.Z or X.Y.Z-suffix):", next)
				return 1, nil
			}
		}
	}
	if next == "" {
		fmt.Fprint(b.stdout, bumpUsage)
		return 1, nil
	}

	old, changed, err := b.apply(next, dryRun)
	if err != nil {
		return 1, err
	}
	fmt.Fprintf(b.stdout, "%sVersion %s → %s\n", prefix, old, next)
	if len(changed) > 0 {
		fmt.Fprintf(b.stdout, "%sUpdated files:\n", prefix)
		for _, f := range changed {
			fmt.Fprintf(b.stdout, "  - %s\n", f)
		}
	} else {
		fmt.Fprintf(b.stdout, "%sNo file changes required.\n", prefix)
	}
	return 0, nil
}

// canonical is the version in VERSION.
func (b versionBump) canonical() (string, error) {
	data, err := os.ReadFile(filepath.Join(b.root, "VERSION"))
	if errors.Is(err, fs.ErrNotExist) {
		return "", errors.New("VERSION file not found")
	}
	if err != nil {
		return "", err
	}
	version := jsTrim(string(data))
	if !reSemver.MatchString(version) {
		return "", fmt.Errorf("Invalid VERSION file content: %s", version)
	}
	return version, nil
}

// readRule is a rule's file, or false when it does not exist.
func (b versionBump) readRule(r versionRule) (string, bool, error) {
	data, err := os.ReadFile(filepath.Join(b.root, filepath.FromSlash(r.path)))
	if errors.Is(err, fs.ErrNotExist) {
		return "", false, nil
	}
	return string(data), err == nil, err
}

func (b versionBump) verify() (int, error) {
	canonical, err := b.canonical()
	if err != nil {
		return 1, err
	}
	var mismatches []string
	for _, r := range versionRules {
		content, ok, err := b.readRule(r)
		switch {
		case err != nil:
			return 1, err
		case !ok:
			mismatches = append(mismatches, fmt.Sprintf("  - %s: expected %s, got (missing file)", r.path, canonical))
		default:
			if v := r.extract(content); v != canonical {
				if v == "" {
					v = "(not found)"
				}
				mismatches = append(mismatches, fmt.Sprintf("  - %s: expected %s, got %s", r.path, canonical, v))
			}
		}
	}
	if len(mismatches) > 0 {
		fmt.Fprintf(b.stderr, "Version mismatch detected (canonical VERSION = %s):\n", canonical)
		for _, m := range mismatches {
			fmt.Fprintln(b.stderr, m)
		}
		return 1, nil
	}
	fmt.Fprintf(b.stdout, "All Tier 1/2 version files match VERSION (%s).\n", canonical)
	return 0, nil
}

// applyToFiles writes version into every rule's file that exists and
// names another one, and returns those files.
func (b versionBump) applyToFiles(version string, dryRun bool) ([]string, error) {
	var changed []string
	for _, r := range versionRules {
		content, ok, err := b.readRule(r)
		if err != nil {
			return changed, err
		}
		if !ok {
			continue
		}
		if updated := r.apply(content, version); updated != content {
			changed = append(changed, r.path)
			if !dryRun {
				if err := os.WriteFile(filepath.Join(b.root, filepath.FromSlash(r.path)), []byte(updated), 0o644); err != nil {
					return changed, err
				}
			}
		}
	}
	return changed, nil
}

// apply bumps to next: the files, then CHANGELOG.md.
func (b versionBump) apply(next string, dryRun bool) (string, []string, error) {
	old, err := b.canonical()
	if err != nil {
		return "", nil, err
	}
	if old == next {
		fmt.Fprintf(b.stdout, "Version already %s; nothing to bump.\n", next)
		return old, nil, nil
	}
	changed, err := b.applyToFiles(next, dryRun)
	if err != nil {
		return old, changed, err
	}
	updated, err := b.updateChangelog(old, next, dryRun)
	if err != nil {
		return old, changed, err
	}
	if updated {
		changed = append(changed, changelogPath)
	}
	return old, changed, nil
}

// updateChangelog turns the Unreleased section into the new version's
// (or adds one), adds its compare link at the end and opens a new
// Unreleased section. A changelog that has the version's section already
// stays as it is.
func (b versionBump) updateChangelog(old, next string, dryRun bool) (bool, error) {
	path := filepath.Join(b.root, changelogPath)
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	content := string(data)
	today := b.now().UTC().Format("2006-01-02")
	const unreleased = "## [Unreleased]"
	header := "## [" + next + "] — " + today
	if strings.Contains(content, header) {
		return false, nil
	}
	if strings.Contains(content, unreleased) {
		content = strings.Replace(content, unreleased, header, 1)
	} else {
		content = header + "\n\n### Changed\n- Version bump to " + next + ".\n\n---\n\n" + content
	}
	// The compare link points at the repository the workflow runs in
	repository := b.getenv("GITHUB_REPOSITORY")
	if repository == "" {
		repository = "neodeskapp/neodesk"
	}
	if !strings.Contains(content, "["+next+"]:") {
		content = strings.TrimRightFunc(content, isJSSpace) + "\n[" + next + "]: https://github.com/" + repository + "/compare/v" + old + "...v" + next + "\n"
	}
	content = "## [Unreleased]\n\n### Changed\n- _(none yet)_\n\n---\n\n" + content
	if !dryRun {
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return false, err
		}
	}
	return true, nil
}
