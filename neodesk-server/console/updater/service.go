package updater

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/managedconfig"
	"github.com/neodeskapp/neodesk/neodesk-server/logging"
)

// DataBackup makes the full backup of the console's data before an
// install. console/backup's Service has this method (CreateFullBackup:
// the archive and its file name neodesk-backup-<time>.tar.gz).
type DataBackup interface {
	CreateFullBackup(ctx context.Context) ([]byte, string, error)
}

// Logger takes the package's log lines.
type Logger interface {
	Warn(args ...any)
	Info(args ...any)
}

// Options are the settings of a Service. The environment variables named
// below are read through Getenv when they are used, so a value the
// console writes into its environment counts at once.
type Options struct {
	// DataDir is the console's data folder (DATA_DIR): backups, the
	// downloads and the updater's markers.
	DataDir string
	// ConsoleDir is the console's folder (CONSOLE_DIR).
	ConsoleDir string
	// EnvFile is NEODESK_ENV_FILE. The channel is saved in it, else in
	// the .env of the console's folder; the capability report checks it,
	// else the env file managedconfig finds, as the config routes do.
	EnvFile string
	// Version and Commit are the running binary's (main.Version, and
	// main.Commit set by the release build).
	Version, Commit string
	// Docker is set inside a container (DOCKER=true or /.dockerenv).
	Docker bool
	// Getenv reads UPDATE_GITHUB_OWNER (neodeskapp), UPDATE_GITHUB_REPO
	// (neodesk), UPDATE_GITHUB_BRANCH (main), UPDATE_GITHUB_TOKEN or
	// GITHUB_TOKEN, UPDATE_GITHUB_CACHE_MS (120000), UPDATE_MIN_FREE_MB
	// (512), UPDATE_MANIFEST_PUBLIC_KEY, UPDATE_REQUIRE_SIGNED_MANIFEST,
	// NEODESK_SUPERVISOR, NEODESK_UPDATE_MODE, NEODESK_IMAGE_TAG,
	// NEODESK_DOCKER_LAYOUT and BACKUP_RETENTION_COUNT. Nil reads
	// nothing.
	Getenv func(string) string
	// Keys are the release public keys compiled in. Nil means
	// CompiledKeys().
	Keys Keys
	// Transport carries the requests to GitHub. Nil means a transport
	// with the system's proxy settings and timeouts.
	Transport http.RoundTripper
	// Executable finds the running binary. Nil means os.Executable.
	Executable func() (string, error)
	// GOOS and GOARCH name the platform whose binary is installed. Empty
	// means the running one's.
	GOOS, GOARCH string
	// RenameAside swaps the binary the Windows way. Nil means the way of
	// the running platform.
	RenameAside *bool
	// RunVersion runs "<binary> -version" and returns what it printed.
	// Nil runs it with a clean environment and a timeout of 10 seconds.
	RunVersion func(ctx context.Context, binary string) (string, error)
	// DataBackup makes the data backup before an install. Nil makes none.
	DataBackup DataBackup
	// Setting reads a console setting (backup_retention_count). Nil reads
	// none.
	Setting func(ctx context.Context, key string) (value string, found bool, err error)
	// FreeSpace says how many bytes are free at a path. Nil asks the
	// operating system.
	FreeSpace func(path string) (int64, error)
	// Now is the clock. Nil means time.Now.
	Now func() time.Time
	// PID names temporary files. 0 means the process id.
	PID int
	// Logger takes warnings. Nil logs through the logging package.
	Logger Logger
}

// Service is the in-app updater of one console process.
type Service struct {
	opts  Options
	gh    *githubClient
	files dataFiles
	log   Logger
	// managed is the env file's allowlisted part, for the capability
	// report.
	managed *managedconfig.Config
	// channelEnv writes the channel into Options.EnvFile.
	channelEnv *managedconfig.Config

	busy atomic.Bool
	// ops replaces the file calls of a swap in tests.
	ops fileOps

	mu     sync.Mutex
	branch string
}

// New returns a Service.
func New(opts Options) *Service {
	if opts.Getenv == nil {
		opts.Getenv = func(string) string { return "" }
	}
	if opts.Keys == nil {
		opts.Keys = CompiledKeys()
	}
	if opts.Executable == nil {
		opts.Executable = os.Executable
	}
	if opts.GOOS == "" {
		opts.GOOS = runtime.GOOS
	}
	if opts.GOARCH == "" {
		opts.GOARCH = runtime.GOARCH
	}
	if opts.RenameAside == nil {
		aside := renameAsideByDefault
		opts.RenameAside = &aside
	}
	if opts.RunVersion == nil {
		opts.RunVersion = runVersion
	}
	if opts.FreeSpace == nil {
		opts.FreeSpace = freeSpace
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.PID == 0 {
		opts.PID = os.Getpid()
	}
	if opts.Logger == nil {
		opts.Logger = packageLogger{}
	}
	if opts.Transport == nil {
		opts.Transport = defaultTransport()
	}
	// The capability report looks at the env file the management config
	// routes save to: NEODESK_ENV_FILE, else the one managedconfig finds
	// from NEODESK_CONSOLE_PATH. The channel is saved in the console
	// folder's .env without NEODESK_ENV_FILE.
	managed := managedconfig.New(managedconfig.Options{ConsoleDir: opts.ConsoleDir, EnvFile: opts.EnvFile, Getenv: opts.Getenv})
	if opts.EnvFile == "" {
		opts.EnvFile = filepath.Join(opts.ConsoleDir, ".env")
	}
	channelEnv := managedconfig.New(managedconfig.Options{EnvFile: opts.EnvFile, Now: opts.Now, PID: opts.PID})
	s := &Service{opts: opts, files: dataFiles{dir: opts.DataDir, now: opts.Now}, log: opts.Logger, managed: managed, channelEnv: channelEnv}
	s.gh = &githubClient{
		transport: opts.Transport,
		userAgent: "NeoDesk-Console/" + opts.Version,
		token:     firstNonEmpty(opts.Getenv("UPDATE_GITHUB_TOKEN"), opts.Getenv("GITHUB_TOKEN")),
		cacheTTL:  cacheTTL(opts.Getenv("UPDATE_GITHUB_CACHE_MS")),
		now:       opts.Now,
		cache:     map[string]cached{},
	}
	return s
}

func defaultTransport() http.RoundTripper {
	return &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 15 * time.Second}).DialContext,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: idleTimeout,
		MaxIdleConns:          4,
		IdleConnTimeout:       60 * time.Second,
	}
}

// cacheTTL is Number(UPDATE_GITHUB_CACHE_MS) || 120000: a number of
// milliseconds; 0, a negative number or text that is no number means the
// default, and a negative number turns the cache off.
func cacheTTL(raw string) time.Duration {
	ms, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil || ms == 0 || raw == "" {
		return 120 * time.Second
	}
	if ms < 0 {
		return 0
	}
	return time.Duration(ms * float64(time.Millisecond))
}

type packageLogger struct{}

func (packageLogger) Warn(args ...any) { logging.Logf("console", "WARN: %s", fmt.Sprint(args...)) }
func (packageLogger) Info(args ...any) { logging.Logf("console", "%s", fmt.Sprint(args...)) }

func (s *Service) getenv(key string) string { return s.opts.Getenv(key) }

func (s *Service) isoNow() string { return s.opts.Now().UTC().Format("2006-01-02T15:04:05.000Z") }

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// Owner and Repo are the repository releases come from.
func (s *Service) Owner() string {
	return firstNonEmpty(s.getenv("UPDATE_GITHUB_OWNER"), "neodeskapp")
}

// Repo is the repository's name.
func (s *Service) Repo() string { return firstNonEmpty(s.getenv("UPDATE_GITHUB_REPO"), "neodesk") }

// Branch is the branch of the update channel: what the channel was last
// switched to in this process, else UPDATE_GITHUB_BRANCH, else main.
func (s *Service) Branch() string {
	s.mu.Lock()
	branch := s.branch
	s.mu.Unlock()
	if branch == "" {
		branch = strings.TrimSpace(s.getenv("UPDATE_GITHUB_BRANCH"))
	}
	if branch == "" {
		return BranchStable
	}
	return branch
}

// Supervised reports that an installer said a supervisor starts the
// process again after it ended (NEODESK_SUPERVISOR).
func (s *Service) Supervised() bool { return Supervisor(s.getenv) != "" }

// Supervisor is the supervisor NEODESK_SUPERVISOR names, "" when none:
// empty, "0", "false", "no", "none" and "off" mean none.
func Supervisor(getenv func(string) string) string {
	value := strings.TrimSpace(getenv("NEODESK_SUPERVISOR"))
	switch strings.ToLower(value) {
	case "", "0", "false", "no", "none", "off":
		return ""
	}
	return value
}

// ImageMode reports a Docker install that updates by pulling an image
// (isImageBasedDockerDeployment): in a container, with
// NEODESK_UPDATE_MODE=image, or without "source" there and without the
// server's sources beside the console.
func (s *Service) ImageMode() bool {
	if !s.opts.Docker {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(s.getenv("NEODESK_UPDATE_MODE"))) {
	case "source":
		return false
	case "image":
		return true
	}
	return !exists(filepath.Join(s.opts.ConsoleDir, "..", "neodesk-server", "go.mod"))
}

// DockerUpdate is how a Docker install updates
// (getDockerUpdateInstructions).
type DockerUpdate struct {
	ChannelNote   string            `json:"channelNote"`
	SuggestedTags map[string]string `json:"suggestedTags"`
	CurrentTag    string            `json:"currentTag"`
	UpdateChannel string            `json:"updateChannel"`
	Summary       string            `json:"summary"`
	Commands      []string          `json:"commands"`
	Images        []string          `json:"images"`
	ComposeHint   string            `json:"composeHint"`
	Layout        string            `json:"layout"`
}

// DockerInstructions are the commands and the image for a Docker install.
// A Docker install with the console runs the one image
// ghcr.io/<owner>/neodesk, whatever NEODESK_DOCKER_LAYOUT says: the
// split images are not published.
func (s *Service) DockerInstructions() DockerUpdate {
	tag := firstNonEmpty(strings.TrimSpace(s.getenv("NEODESK_IMAGE_TAG")), "latest")
	owner := strings.ToLower(s.Owner())
	return DockerUpdate{
		ChannelNote:   "Panel “Update channel” does not change GHCR images. Use NEODESK_IMAGE_TAG=latest (stable) or NEODESK_IMAGE_TAG=dev (development), then pull/recreate.",
		SuggestedTags: map[string]string{"stable": "latest", "development": "dev"},
		CurrentTag:    tag,
		UpdateChannel: ChannelOf(s.Branch()),
		Summary:       "Pull and recreate the official all-in-one container image.",
		Commands:      []string{"docker compose pull", "docker compose up -d"},
		Images:        []string{"ghcr.io/" + owner + "/neodesk:" + tag},
		ComposeHint:   "docker-compose.quick.single.yml",
		Layout:        "single",
	}
}

// DockerHint is the commands of DockerInstructions joined with " && ".
func (s *Service) DockerHint() string {
	return strings.Join(s.DockerInstructions().Commands, " && ")
}

// ---- channel ----

// ChannelInfo is the update channel (getUpdateChannelInfo).
type ChannelInfo struct {
	Channel string `json:"channel"`
	Branch  string `json:"branch"`
	Label   string `json:"label"`
	Owner   string `json:"owner"`
	Repo    string `json:"repo"`
}

// Channel is the update channel and its repository.
func (s *Service) Channel() ChannelInfo {
	branch := s.Branch()
	channel := ChannelOf(branch)
	return ChannelInfo{Channel: channel, Branch: branch, Label: ChannelLabel(channel), Owner: s.Owner(), Repo: s.Repo()}
}

// ChannelSwitched is the answer of SetChannel.
type ChannelSwitched struct {
	Channel         string `json:"channel"`
	Branch          string `json:"branch"`
	Label           string `json:"label"`
	PreviousBranch  string `json:"previousBranch"`
	PreviousChannel string `json:"previousChannel"`
}

// ErrDockerChannel is the refusal to switch the channel of a Docker
// image install (code DOCKER_IMAGE_CHANNEL).
type ErrDockerChannel struct{ Message string }

func (e *ErrDockerChannel) Error() string { return e.Message }

// SetChannel switches the channel: UPDATE_GITHUB_BRANCH in the env file,
// written as a save of the managed configuration is (managedconfig.Apply:
// a backup, a temporary file renamed over the env file, mode 0600, one
// write at a time), and in this process. channel must be "stable" or
// "development".
func (s *Service) SetChannel(channel string) (ChannelSwitched, error) {
	branch := BranchOf(channel)
	if branch == "" {
		return ChannelSwitched{}, fmt.Errorf("Invalid update channel: %s", channel)
	}
	if s.ImageMode() {
		tag := "latest"
		if channel == ChannelDevelopment {
			tag = "dev"
		}
		return ChannelSwitched{}, &ErrDockerChannel{Message: "Docker image deployments cannot switch update channel from the panel. " +
			"Set NEODESK_IMAGE_TAG=" + tag + " (or a release version) in compose/.env, then run: docker compose pull && docker compose up -d"}
	}
	previous := s.Branch()
	if _, err := s.channelEnv.Apply([]managedconfig.Setting{{Key: "UPDATE_GITHUB_BRANCH", Value: branch}}); err != nil {
		return ChannelSwitched{}, fmt.Errorf("save the channel: %w", err)
	}
	s.mu.Lock()
	s.branch = branch
	s.mu.Unlock()
	return ChannelSwitched{
		Channel: channel, Branch: branch, Label: ChannelLabel(channel),
		PreviousBranch: previous, PreviousChannel: ChannelOf(previous),
	}, nil
}

// ---- what the panel reads besides a check ----

// ServerInfo is the answer of the server-info route in the shape the old
// console gave its Go toolchain facts. Updates install release binaries,
// so the tools are always "ready" and no toolchain is needed
// (DECISIONS A29, spec 4.6).
type ServerInfo struct {
	GoAvailable         bool    `json:"goAvailable"`
	GoVersion           string  `json:"goVersion"`
	GoVersionNumber     string  `json:"goVersionNumber"`
	GoPath              string  `json:"goPath"`
	GoSource            string  `json:"goSource"`
	GoMeetsMinimum      bool    `json:"goMeetsMinimum"`
	GoNeedsUpgrade      bool    `json:"goNeedsUpgrade"`
	GoMinimumVersion    string  `json:"goMinimumVersion"`
	VendoredGoInstalled bool    `json:"vendoredGoInstalled"`
	CanInstallGo        bool    `json:"canInstallGo"`
	BinaryPath          *string `json:"binaryPath"`
	SourcePresent       bool    `json:"sourcePresent"`
	CanAutoUpdate       bool    `json:"canAutoUpdate"`
	BuildMethod         string  `json:"buildMethod"`
}

// ServerInfo describes how this install gets a new server binary.
func (s *Service) ServerInfo() ServerInfo {
	goVersion := runtime.Version()
	number := strings.TrimPrefix(goVersion, "go")
	var binary *string
	if paths, err := ResolveBinaryPaths(s.opts.Executable); err == nil {
		binary = &paths.Binary
	}
	return ServerInfo{
		GoAvailable: true, GoVersion: goVersion, GoVersionNumber: number, GoSource: "path",
		GoMeetsMinimum: true, GoMinimumVersion: number, BinaryPath: binary, CanAutoUpdate: true, BuildMethod: "release",
	}
}

// BinaryStatus is the answer of the server-binary status route: whether
// the running binary was put back by an automatic rollback.
func (s *Service) BinaryStatus() any {
	if s.ImageMode() {
		return BinaryStatus{DockerMode: true}
	}
	return s.files.binaryStatus()
}

// InstallStatus is the answer of the install-status route.
type InstallStatus struct {
	InProgress bool         `json:"inProgress"`
	Marker     any          `json:"marker"`
	LocalSHA   *string      `json:"localSHA"`
	LastResult any          `json:"lastResult"`
	Pending    *PendingInfo `json:"pending,omitempty"`
}

// InstallStatus is whether an install runs now, the commit of the
// running binary, the stored last result and an update that waits for
// its first start. A marker of an install that is not running (a process
// that ended during one) is removed.
func (s *Service) InstallStatus() InstallStatus {
	out := InstallStatus{LocalSHA: nullable(s.opts.Commit), LastResult: s.files.readResult()}
	if s.busy.Load() {
		out.InProgress = true
		out.Marker = s.files.readInProgress()
	} else {
		s.files.clearInProgress()
	}
	if paths, err := ResolveBinaryPaths(s.opts.Executable); err == nil {
		if m, err := readMarker(paths.Pending); err == nil {
			out.Pending = &PendingInfo{Kind: m.Kind, From: m.From, To: m.To, Starts: m.Starts, At: m.At}
		}
	}
	return out
}

// LastResult is the stored result when it still has something to show
// (the last-result route). remoteSHA is the release's commit when the
// release could be looked up.
func (s *Service) LastResult(ctx context.Context) any {
	remote := ""
	if rel, err := s.lookupRelease(ctx, "", false); err == nil {
		remote = rel.Manifest.SHA
	}
	return s.files.resultForDisplay(s.opts.Commit, remote)
}

// PublicKeysConfigured reports whether a manifest signature is checked.
func (s *Service) PublicKeysConfigured() bool { return len(s.keys()) > 0 }
