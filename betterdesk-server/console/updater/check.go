package updater

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"regexp"
	"slices"
	"strings"
)

// Component is one part of BetterDesk a changed file belongs to, as the
// panel groups them (COMPONENTS of the old updater).
type Component struct {
	Changed    bool   `json:"changed"`
	FileCount  int    `json:"fileCount"`
	Label      string `json:"label"`
	AutoUpdate bool   `json:"autoUpdate"`
}

var componentMeta = map[string]struct {
	prefix, label string
	auto          bool
}{
	"console": {"web-nodejs/", "Web Console", true},
	"webui":   {"web-ui/", "Web Console (web-ui)", true},
	"server":  {"betterdesk-server/", "Go Server", true},
	"agent":   {"betterdesk-agent/", "Agent library", false},
	"scripts": {"", "Scripts & Docker", true},
}

// componentOrder is the order in which classifyFile tries the prefixes.
var componentOrder = []string{"console", "webui", "server", "agent", "scripts"}

var scriptFiles = []string{
	"install.sh", "betterdesk.sh", "betterdesk.ps1", "betterdesk-docker.sh",
	"docker-compose.yml", "docker-compose.single.yml", "docker-compose.quick.yml",
	"docker-compose.quick.single.yml", "docker-compose.quick.single.macvlan.yml",
	"Dockerfile", "Dockerfile.server", "Dockerfile.console",
	"docker-entrypoint.sh", "docker/entrypoint.sh",
	"docker/server-entrypoint.sh", "docker/console-entrypoint.sh",
	"docker/supervisord.conf", "scripts/installer-protocol-check.js",
}

// classifyFile names the component of a repository path.
func classifyFile(path string) string {
	if slices.Contains(scriptFiles, path) {
		return "scripts"
	}
	for _, name := range componentOrder {
		if prefix := componentMeta[name].prefix; prefix != "" && strings.HasPrefix(path, prefix) {
			return name
		}
	}
	return "other"
}

// excludePatterns are the paths the old updater never counted: runtime
// state, documentation, binaries.
var excludePatterns = []*regexp.Regexp{
	regexp.MustCompile(`^\.github/`), regexp.MustCompile(`^archive/`), regexp.MustCompile(`^docs/`),
	regexp.MustCompile(`^screenshots/`), regexp.MustCompile(`^dev_modules/`), regexp.MustCompile(`^tasks/`),
	regexp.MustCompile(`^sdks/`), regexp.MustCompile(`^bridges/`), regexp.MustCompile(`node_modules/`),
	regexp.MustCompile(`\.exe$`), regexp.MustCompile(`^betterdesk-server/betterdesk-server`),
	regexp.MustCompile(`^web-nodejs/data/`), regexp.MustCompile(`(^|/)data/`),
	regexp.MustCompile(`\.sqlite3?$`), regexp.MustCompile(`\.sqlite3?-(shm|wal|journal)$`),
	regexp.MustCompile(`\.db$`), regexp.MustCompile(`\.db-(shm|wal|journal)$`),
	regexp.MustCompile(`(^|/)\.session_secret$`), regexp.MustCompile(`(^|/)\.update_sha$`),
	regexp.MustCompile(`(^|/)\.api_key$`), regexp.MustCompile(`(^|/)\.admin_credentials$`),
	regexp.MustCompile(`(^|/)\.force_password_update$`), regexp.MustCompile(`(^|/)\.env(\.|$)`),
}

var (
	webUILocalDirs = []string{"node_modules", "build", ".build-prev", ".svelte-kit", "test-results", "playwright-report", "coverage"}
	webUITestDirs  = []string{"tests", "e2e"}
)

func isWebUISyncPath(local string) bool {
	parts := strings.Split(strings.ReplaceAll(local, `\`, "/"), "/")
	if parts[0] == "" || slices.Contains(parts, "..") || slices.Contains(parts, "node_modules") {
		return false
	}
	return !slices.Contains(webUILocalDirs, parts[0]) && !slices.Contains(webUITestDirs, parts[0])
}

func isExcluded(path string) bool {
	if rest, ok := strings.CutPrefix(path, "web-ui/"); ok {
		return !isWebUISyncPath(rest)
	}
	for _, re := range excludePatterns {
		if re.MatchString(path) {
			return true
		}
	}
	return false
}

// compareFile and compareCommit are the parts of GitHub's comparison the
// updater reads.
type compareFile struct {
	Filename         string `json:"filename"`
	Status           string `json:"status"`
	SHA              string `json:"sha"`
	PreviousFilename string `json:"previous_filename"`
}

type compareCommit struct {
	SHA    string `json:"sha"`
	Commit struct {
		Message   string `json:"message"`
		Committer struct {
			Date string `json:"date"`
		} `json:"committer"`
		Author struct {
			Name string `json:"name"`
		} `json:"author"`
	} `json:"commit"`
}

type comparison struct {
	TotalCommits int             `json:"total_commits"`
	Commits      []compareCommit `json:"commits"`
	Files        []compareFile   `json:"files"`
}

func (s *Service) compare(ctx context.Context, from, to string) (*comparison, error) {
	path := "/repos/" + url.PathEscape(s.Owner()) + "/" + url.PathEscape(s.Repo()) + "/compare/" + from + "..." + to
	body, err := s.gh.apiGet(ctx, path, false)
	if err != nil {
		return nil, err
	}
	var c comparison
	if err := json.Unmarshal(body, &c); err != nil {
		return nil, errors.New("Invalid JSON from GitHub API")
	}
	return &c, nil
}

func (c *comparison) commitsBehind() int {
	if c.TotalCommits != 0 {
		return c.TotalCommits
	}
	return len(c.Commits)
}

// CheckResult is the answer of a check, with the keys in the order the
// old console sent them. The Docker fields are only there for an image
// install.
type CheckResult struct {
	LocalVersion         string               `json:"localVersion"`
	LocalSHA             *string              `json:"localSHA"`
	RemoteSHA            string               `json:"remoteSHA"`
	UpdateAvailable      bool                 `json:"updateAvailable"`
	CommitsBehind        int                  `json:"commitsBehind"`
	LatestMessage        string               `json:"latestMessage"`
	LatestDate           string               `json:"latestDate"`
	LatestAuthor         string               `json:"latestAuthor"`
	Components           map[string]Component `json:"components"`
	DockerShaUnknown     bool                 `json:"dockerShaUnknown,omitempty"`
	CompareError         string               `json:"compareError,omitempty"`
	LatestVersion        string               `json:"latestVersion"`
	ReleaseTag           string               `json:"releaseTag"`
	Signature            Trust                `json:"signature"`
	UpdateChannel        string               `json:"updateChannel"`
	GithubBranch         string               `json:"githubBranch"`
	GithubOwner          string               `json:"githubOwner"`
	GithubRepo           string               `json:"githubRepo"`
	DeploymentMode       string               `json:"deploymentMode"`
	DockerImageMode      bool                 `json:"dockerImageMode,omitempty"`
	ImageSHA             *string              `json:"imageSHA,omitempty"`
	DockerUpdate         *DockerUpdate        `json:"dockerUpdate,omitempty"`
	InAppUpdateSupported *bool                `json:"inAppUpdateSupported,omitempty"`
}

// Check compares the running binary with the newest release of the
// channel. The release's commit is the one to compare: an update is
// available when it is another commit and its version is not older than
// the running one. The components come from GitHub's comparison of the
// two commits, as the old updater counted them.
func (s *Service) Check(ctx context.Context) (*CheckResult, error) {
	rel, err := s.lookupRelease(ctx, "", false)
	if err != nil {
		return nil, err
	}
	local := s.opts.Commit
	out := &CheckResult{
		LocalVersion: s.opts.Version, LocalSHA: nullable(local), RemoteSHA: rel.Manifest.SHA,
		LatestMessage: headline(firstNonEmpty(rel.Name, rel.Tag)), LatestDate: rel.PublishedAt, LatestAuthor: rel.Author,
		Components: map[string]Component{}, LatestVersion: rel.Manifest.Version, ReleaseTag: rel.Tag, Signature: rel.Trust,
	}
	s.withDeployment(out)
	newer := CompareVersions(rel.Manifest.Version, s.opts.Version) >= 0
	switch {
	case local == "" && s.ImageMode():
		out.DockerShaUnknown = true
	case local == "":
		out.CompareError = "This build does not know its commit (it was built without -X main.Commit), so it is not updated in the app"
	case shaMatches(local, rel.Manifest.SHA):
		_ = s.files.resultForDisplay(local, rel.Manifest.SHA, s.opts.ConsoleDir)
	default:
		cmp, err := s.compare(ctx, local, rel.Manifest.SHA)
		if err != nil {
			out.UpdateAvailable = newer
			out.CommitsBehind = -1
			out.CompareError = err.Error()
			break
		}
		for _, f := range cmp.Files {
			if isExcluded(f.Filename) {
				continue
			}
			name := classifyFile(f.Filename)
			c, ok := out.Components[name]
			if !ok {
				meta, known := componentMeta[name]
				c = Component{Changed: true, Label: "Other"}
				if known {
					c.Label, c.AutoUpdate = meta.label, meta.auto
				}
			}
			c.FileCount++
			out.Components[name] = c
		}
		out.UpdateAvailable = newer
		out.CommitsBehind = cmp.commitsBehind()
	}
	return out, nil
}

func (s *Service) withDeployment(out *CheckResult) {
	ch := s.Channel()
	out.UpdateChannel, out.GithubBranch, out.GithubOwner, out.GithubRepo = ch.Channel, ch.Branch, ch.Owner, ch.Repo
	if !s.ImageMode() {
		out.DeploymentMode = "native"
		return
	}
	no := false
	docker := s.DockerInstructions()
	out.DeploymentMode, out.DockerImageMode, out.ImageSHA = "docker-image", true, nullable(s.opts.Commit)
	out.DockerUpdate, out.InAppUpdateSupported = &docker, &no
}

// ChangedFile is a file of the comparison.
type ChangedFile struct {
	Path      string `json:"path"`
	Status    string `json:"status"`
	Component string `json:"component"`
}

// GroupedFile is a file in its component's group.
type GroupedFile struct {
	Path         string `json:"path"`
	Status       string `json:"status"`
	SHA          string `json:"sha"`
	Component    string `json:"component"`
	LocalPath    string `json:"localPath,omitempty"`
	PreviousPath string `json:"previousPath,omitempty"`
}

// CommitInfo is a commit of the comparison.
type CommitInfo struct {
	SHA     string `json:"sha"`
	Message string `json:"message"`
	Date    string `json:"date"`
	Author  string `json:"author"`
}

// Grouped are the changed files by component, in the old console's
// order.
type Grouped struct {
	Console []GroupedFile `json:"console"`
	WebUI   []GroupedFile `json:"webui"`
	Server  []GroupedFile `json:"server"`
	Agent   []GroupedFile `json:"agent"`
	Scripts []GroupedFile `json:"scripts"`
	Other   []GroupedFile `json:"other"`
}

func (g *Grouped) add(name string, f GroupedFile) {
	switch name {
	case "console":
		g.Console = append(g.Console, f)
	case "webui":
		g.WebUI = append(g.WebUI, f)
	case "server":
		g.Server = append(g.Server, f)
	case "agent":
		g.Agent = append(g.Agent, f)
	case "scripts":
		g.Scripts = append(g.Scripts, f)
	default:
		g.Other = append(g.Other, f)
	}
}

// Changes is the answer of the changes route.
type Changes struct {
	Files            []ChangedFile `json:"files"`
	Grouped          Grouped       `json:"grouped"`
	TotalFiles       int           `json:"totalFiles"`
	CompareTruncated bool          `json:"compareTruncated"`
	CommitsBehind    int           `json:"commitsBehind"`
	Commits          []CommitInfo  `json:"commits"`
}

// RequestError is a request the updater refuses; Status is the HTTP
// status the routes answer.
type RequestError struct {
	Status  int
	Message string
}

func (e *RequestError) Error() string { return e.Message }

// Changes lists what changed between the running binary's commit and the
// release's (getChangedFiles). sha must be the release's commit.
func (s *Service) Changes(ctx context.Context, sha string) (*Changes, error) {
	if s.opts.Commit == "" {
		return nil, errNoBaseline
	}
	rel, err := s.lookupRelease(ctx, "", false)
	if err != nil {
		return nil, err
	}
	if !shaMatches(sha, rel.Manifest.SHA) {
		return nil, &RequestError{Status: 400, Message: "The commit is not the one of the newest release of the update channel; check for updates again"}
	}
	cmp, err := s.compare(ctx, s.opts.Commit, rel.Manifest.SHA)
	if err != nil {
		return nil, err
	}
	out := &Changes{Files: []ChangedFile{}, Commits: []CommitInfo{}}
	out.Grouped = Grouped{Console: []GroupedFile{}, WebUI: []GroupedFile{}, Server: []GroupedFile{}, Agent: []GroupedFile{}, Scripts: []GroupedFile{}, Other: []GroupedFile{}}
	for _, f := range cmp.Files {
		if isExcluded(f.Filename) {
			continue
		}
		name := classifyFile(f.Filename)
		status := firstNonEmpty(f.Status, "modified")
		entry := GroupedFile{Path: f.Filename, Status: status, SHA: f.SHA, Component: name}
		switch name {
		case "console":
			entry.LocalPath = strings.TrimPrefix(f.Filename, "web-nodejs/")
		case "webui":
			entry.LocalPath = strings.TrimPrefix(f.Filename, "web-ui/")
			if prev, ok := strings.CutPrefix(f.PreviousFilename, "web-ui/"); ok {
				entry.PreviousPath = prev
			}
		case "scripts":
			entry.LocalPath = f.Filename
		}
		out.Grouped.add(name, entry)
		out.Files = append(out.Files, ChangedFile{Path: f.Filename, Status: status, Component: name})
	}
	for _, f := range cmp.Files {
		prev := f.PreviousFilename
		if !strings.HasPrefix(prev, "web-ui/") || isExcluded(prev) {
			continue
		}
		if classifyFile(f.Filename) == "webui" && !isExcluded(f.Filename) {
			continue
		}
		out.Grouped.add("webui", GroupedFile{Path: prev, Status: "removed", Component: "webui", LocalPath: strings.TrimPrefix(prev, "web-ui/")})
	}
	out.TotalFiles = len(out.Files)
	out.CompareTruncated = out.TotalFiles >= 300
	out.CommitsBehind = cmp.commitsBehind()
	commits := cmp.Commits
	if len(commits) > 30 {
		commits = commits[len(commits)-30:]
	}
	for i := len(commits) - 1; i >= 0; i-- {
		c := commits[i]
		out.Commits = append(out.Commits, CommitInfo{SHA: prefix7(c.SHA), Message: headline(c.Commit.Message), Date: c.Commit.Committer.Date, Author: c.Commit.Author.Name})
	}
	return out, nil
}

// errNoBaseline refuses the changes and the install for a build that does
// not know its commit (a plain "go build"), with the old console's text
// for an install without a known commit. Release builds always know it.
var errNoBaseline = errors.New("No local baseline SHA — run update check first")
