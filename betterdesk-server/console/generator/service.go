package generator

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/unitronix/betterdesk-server/console/serverinfo"
	"github.com/unitronix/betterdesk-server/console/ssrfguard"
	"github.com/unitronix/betterdesk-server/console/store"
)

// Logger takes the service's messages. *kit.Logger is one. Messages never
// hold a token, a seed or a profile.
type Logger interface {
	Info(args ...any)
	Warn(args ...any)
	Error(args ...any)
}

// ServerInfo is what the generator asks about this server: its public
// key and the addresses a client is given. *serverinfo.Service is one.
type ServerInfo interface {
	ResolvePublicKey(ctx context.Context) serverinfo.NullString
	ResolveClientFacingHost(req *serverinfo.RequestHost, queryHost string) string
	DefaultServerHost() string
	DefaultUseHTTPS() bool
	DefaultAPIPort() string
	DefaultRelayPort() string
}

// Options are the settings of a Service. The names in brackets are the
// environment variables the old console read; config.Load reads the first
// group, Env holds the rest.
type Options struct {
	DB         *store.DB
	ServerInfo ServerInfo

	// DataDir is the console's data folder (DATA_DIR). The module, the
	// client builder and the build cache live below it.
	DataDir string
	// ConsoleDir is the old console's folder (web-nodejs). A signing seed
	// placed there, or in ../res/betterdesk next to it, is taken over.
	ConsoleDir string
	// WorkDir is the process's working directory, the last place a
	// signing seed is looked for. os.Getwd when empty.
	WorkDir string
	// ArtifactDir holds the finished installers (AGENT_ARTIFACT_DIR,
	// default DataDir/agent-builds).
	ArtifactDir string
	// Embedded switches from the downloaded templates to builds of the
	// client's source on this machine
	// (BETTERDESK_CLIENT_GENERATOR_MODE=embedded; default "download").
	Embedded bool
	// WorkerEnabled is false with AGENT_BUILD_WORKER=off.
	WorkerEnabled bool
	// PollInterval is how often the worker looks for a queued build
	// (AGENT_BUILD_POLL_MS, default 5 seconds), Cooldown the pause after a
	// build (AGENT_BUILD_COOLDOWN_MS, default 1 second).
	PollInterval time.Duration
	Cooldown     time.Duration

	// Env is the process environment as os.Environ gives it. The client
	// builder reads its settings from it when it needs them (the
	// repository, the token, the toolchain paths), and hands it to the
	// build tools. Values from /etc/betterdesk/build.env
	// (BETTERDESK_BUILD_ENV_FILE) fill the names it lacks.
	Env func() []string

	// HTTP sends the requests to GitHub. Nil means http.DefaultTransport.
	HTTP http.RoundTripper
	// Lookup resolves the hosts of an artifact download for the outbound
	// guard. Nil means the system's resolver.
	Lookup ssrfguard.LookupFunc
	// Run starts the tools (tar, git, the build tools). Nil runs them.
	Run Runner
	// Now and Sleep are the clock; Rand gives bundle ids and new signing
	// seeds. Nil means the real ones.
	Now   func() time.Time
	Sleep func(ctx context.Context, d time.Duration) error
	Rand  io.Reader
	// GOOS and GOARCH are the machine the console runs on. Empty means
	// the running binary's.
	GOOS, GOARCH string
	// PID goes into the names of temporary files. os.Getpid when 0.
	PID int

	Logger Logger
}

// Service is the support client generator: bundle profiles, the
// template module, the client builder and the build queue. It is safe
// for concurrent use.
type Service struct {
	opts Options
	env  map[string]string

	installMu sync.Mutex
	install   *flight

	worker workerState
}

// New returns a Service. It reads the build environment file once, as
// the old service did when it was loaded.
func New(opts Options) *Service {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Sleep == nil {
		opts.Sleep = sleepContext
	}
	if opts.Rand == nil {
		opts.Rand = randomReader
	}
	if opts.Run == nil {
		opts.Run = ExecRunner{}
	}
	if opts.HTTP == nil {
		opts.HTTP = http.DefaultTransport
	}
	if opts.GOOS == "" {
		opts.GOOS = runtime.GOOS
	}
	if opts.GOARCH == "" {
		opts.GOARCH = runtime.GOARCH
	}
	if opts.PID == 0 {
		opts.PID = os.Getpid()
	}
	if opts.WorkDir == "" {
		opts.WorkDir, _ = os.Getwd()
	}
	if opts.Env == nil {
		opts.Env = os.Environ
	}
	if opts.ArtifactDir == "" {
		opts.ArtifactDir = filepath.Join(opts.DataDir, "agent-builds")
	}
	if opts.Logger == nil {
		opts.Logger = discardLogger{}
	}
	s := &Service{opts: opts}
	s.env = s.loadBuildEnvironment()
	return s
}

// Embedded reports the embedded build mode.
func (s *Service) Embedded() bool { return s.opts.Embedded }

type discardLogger struct{}

func (discardLogger) Info(...any)  {}
func (discardLogger) Warn(...any)  {}
func (discardLogger) Error(...any) {}

func sleepContext(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// environ returns the process environment as a map, with presence.
func (s *Service) environ() map[string]string {
	out := map[string]string{}
	for _, kv := range s.opts.Env() {
		if k, v, ok := strings.Cut(kv, "="); ok {
			if _, seen := out[k]; !seen {
				out[k] = v
			}
		}
	}
	return out
}

var buildEnvLine = regexp.MustCompile(`^[\t\n\v\f\r \x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}]*([A-Z_][A-Z0-9_]*)[\t\n\v\f\r \x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}]*=[\t\n\v\f\r \x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}]*(.*?)[\t\n\v\f\r \x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}]*$`)

// loadBuildEnvironment reads the installer's build environment file:
// NAME=value lines, of which only names the process environment lacks
// are taken.
func (s *Service) loadBuildEnvironment() map[string]string {
	process := s.environ()
	file, set := process["BETTERDESK_BUILD_ENV_FILE"]
	if !set || file == "" {
		file = "/etc/betterdesk/build.env"
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		if !os.IsNotExist(err) {
			s.opts.Logger.Warn("[clientBuilderService] could not load build env file:", nodeFSError(err, "open", file).Error())
		}
		return map[string]string{}
	}
	extra := map[string]string{}
	for _, line := range splitLines(decodeUTF8(raw)) {
		m := buildEnvLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		if _, inProcess := process[m[1]]; !inProcess {
			if _, seen := extra[m[1]]; !seen {
				extra[m[1]] = m[2]
			}
		}
	}
	return extra
}

// lookupEnv is process.env[name] with the build environment file's
// values for names the process does not have.
func (s *Service) lookupEnv(name string) (string, bool) {
	if v, ok := s.environ()[name]; ok {
		return v, true
	}
	v, ok := s.env[name]
	return v, ok
}

// getenv is process.env[name] || "".
func (s *Service) getenv(name string) string {
	v, _ := s.lookupEnv(name)
	return v
}

// childEnv is the environment a tool starts with: the process
// environment with the build environment file's values added, and extra
// on top.
func (s *Service) childEnv(extra map[string]string) []string {
	merged := s.environ()
	for k, v := range s.env {
		if _, ok := merged[k]; !ok {
			merged[k] = v
		}
	}
	for k, v := range extra {
		merged[k] = v
	}
	out := make([]string, 0, len(merged))
	for k, v := range merged {
		out = append(out, k+"="+v)
	}
	return out
}

// splitLines is text.split(/\r?\n/).
func splitLines(text string) []string {
	return regexp.MustCompile(`\r?\n`).Split(text, -1)
}

// relayPort is the port a relay address without one gets: the server info
// service's (RELAY_PORT, WS_HBBR_PORT, HBBR_PORT), else the generator's
// own environment.
func (s *Service) relayPort() string {
	if s.opts.ServerInfo != nil {
		return s.opts.ServerInfo.DefaultRelayPort()
	}
	return serverinfo.DefaultRelayPortOf(s.getenv)
}
