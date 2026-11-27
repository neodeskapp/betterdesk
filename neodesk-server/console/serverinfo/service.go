// Package serverinfo answers how a client reaches this server: the public
// key and the API key, the host names and addresses a RustDesk or
// NeoDesk client is given, the configuration string and link a client
// imports, the public endpoints an operator can set, the server's
// connection mode, the product version, and the rewriting of ".env" files
// that several of these need.
//
// The strings are a contract with clients in the field. Every function
// gives the result the Node console gives for the same input.
package serverinfo

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
	"github.com/neodeskapp/neodesk/neodesk-server/logging"
)

// Logger takes the warnings and errors of the service. *kit.Logger is one.
type Logger interface {
	Warn(args ...any)
	Error(args ...any)
}

// Options are the settings of a Service. Each field names the setting of
// the Node console it stands for.
type Options struct {
	// PubKeyPath is the server's public key file (config.pubKeyPath):
	// PUB_KEY_PATH, else id_ed25519.pub in the keys folder.
	PubKeyPath string
	// APIKeyPath is the API key file (config.apiKeyPath): API_KEY_PATH,
	// else .api_key in the keys folder.
	APIKeyPath string
	// DBPath is the database file (config.dbPath): DB_PATH, else
	// db_v2.sqlite3 in the keys folder. It is only reported.
	DBPath string
	// APIURL is the address of the server's API as configured
	// (config.hbbsApiUrl): NEODESK_API_URL, else HBBS_API_URL, else
	// "http://127.0.0.1:21114/api". Its host is the client-facing host of
	// last resort, and its scheme decides between http and https in the
	// API address given to clients.
	APIURL string
	// GoAPIPort is the port of the server's API (config.goApiPort):
	// GO_API_PORT, else the port of APIURL, else 21114. Zero falls back
	// on APIPort.
	GoAPIPort int
	// APIPort is the client API port (config.apiPort): API_PORT, default
	// 21121. Zero falls back on 21114.
	APIPort int
	// ClientAPIPort is the client API port while the console listens on
	// it (API_PORT while API_ENABLED is on), 0 when it does not. Clients
	// are then given this port as the API address on every install type,
	// at https only when ClientAPIHTTPS says the port has TLS.
	ClientAPIPort  int
	ClientAPIHTTPS bool
	// DataDir is the console's data folder (config.dataDir): DATA_DIR. The
	// public endpoints file lives in it.
	DataDir string
	// EnvPath is the console's own ".env" file, in which the public
	// endpoints are mirrored.
	EnvPath string
	// OnPublicRelay is told the public relay server in force after each
	// save of the public endpoints ("" for none), so the signal server
	// gives devices that relay. Nil tells nobody.
	OnPublicRelay func(relay string)
	// IsDocker says the console runs in a container (config.isDocker): the
	// file /.dockerenv exists or DOCKER is "true".
	IsDocker bool
	// Getenv reads the process environment: PUBLIC_SERVER_ID,
	// PUBLIC_RELAY_SERVER, PUBLIC_API_URL, PANEL_PUBLIC_HOST, RELAY_SERVERS,
	// DOCKER and NEODESK_DOCKER_LAYOUT. The Node console has the values of its
	// ".env" file in its environment for variables that are not set, so
	// Getenv should give those too. Nil means os.Getenv.
	Getenv func(string) string

	// SystemdUnit is the server's systemd unit. Default
	// /etc/systemd/system/neodesk-server.service.
	SystemdUnit string
	// SystemdDropIn is the drop-in the privileged helper writes the
	// connection mode to. Default
	// /etc/systemd/system/neodesk-server.service.d/50-neodesk-connection.conf.
	SystemdDropIn string
	// ComposePath is the compose file whose "server" service carries the
	// connection mode: docker-compose.yml in the folder above the console.
	// "docker compose" runs in its folder.
	ComposePath string
	// DockerEnvFile marks a container. Default /.dockerenv.
	DockerEnvFile string
	// ServerServiceName is the name of the server's system service
	// (updateService.COMPONENTS.server.service): "NeoDeskServer" on
	// Windows, else "neodesk-server", which is the default.
	ServerServiceName string

	// CanUsePrivileged reports whether the privileged update helper can be
	// used (lib/privilegedUpdateHelper canUsePrivilegedUpdate). Nil means
	// it cannot.
	CanUsePrivileged func() bool
	// WritePrivilegedEnv has the privileged helper write the connection
	// variables to the drop-in (its "write_connection_env" action) and
	// returns the path the helper reports, or "".
	WritePrivilegedEnv func(ctx context.Context, vars EnvVars) (path string, err error)
	// DaemonReload reloads the systemd units and returns the object of
	// updateService.daemonReload: {success}, {success, skipped} or
	// {success, error}.
	DaemonReload func(ctx context.Context) *goapi.Object
	// RestartService restarts a system service and returns the object of
	// updateService.restartService: {success, service} and, on failure,
	// error, nonCritical and hint.
	RestartService func(ctx context.Context, service string) *goapi.Object
	// RunShell runs a command line in a folder with the system's shell and
	// returns an error whose text is the message Node's
	// child_process.execSync gives for the failure. Nil means the real
	// shell, with two minutes for the command.
	RunShell func(ctx context.Context, dir, command string) error

	// GoAPI calls the server's API.
	GoAPI *goapi.Client
	// QR renders text as a PNG data URL. The codes the console shows are
	// drawn with error correction level M, a width of 256 and a margin of
	// 2: qr.PNGDataURL with those settings.
	QR func(text string) (string, error)
	// FS is the file access. Nil means OSFS.
	FS FS
	// Now is the clock for the cache of the server's key. Nil means
	// time.Now.
	Now func() time.Time
	// Logger takes warnings and errors. Nil logs through the logging
	// package.
	Logger Logger
}

// Paths of the systemd files on a Linux install.
const (
	DefaultSystemdUnit   = "/etc/systemd/system/neodesk-server.service"
	DefaultSystemdDropIn = "/etc/systemd/system/neodesk-server.service.d/50-neodesk-connection.conf"
	defaultDockerEnvFile = "/.dockerenv"
	defaultServerService = "neodesk-server"
)

// Service gives the answers of this package for one console. It is safe
// for concurrent use.
type Service struct {
	opts   Options
	fs     FS
	now    func() time.Time
	log    Logger
	getenv func(string) string

	keyMu   sync.Mutex
	goKey   string
	goKeyAt int64 // Unix milliseconds

	// envMu guards the values saved over the process environment.
	envMu    sync.Mutex
	envSaved map[string]*string

	// fileMu lets one request at a time rewrite the env files, the systemd
	// unit or the compose file, and keeps readers of those files out
	// meanwhile, so that no request sees half of a save.
	fileMu sync.RWMutex
}

// New returns a Service.
func New(opts Options) *Service {
	s := &Service{opts: opts, fs: opts.FS, now: opts.Now, log: opts.Logger, getenv: opts.Getenv}
	if s.fs == nil {
		s.fs = OSFS{}
	}
	if s.now == nil {
		s.now = time.Now
	}
	if s.log == nil {
		s.log = packageLogger{}
	}
	if s.getenv == nil {
		s.getenv = os.Getenv
	}
	if s.opts.SystemdUnit == "" {
		s.opts.SystemdUnit = DefaultSystemdUnit
	}
	if s.opts.SystemdDropIn == "" {
		s.opts.SystemdDropIn = DefaultSystemdDropIn
	}
	if s.opts.DockerEnvFile == "" {
		s.opts.DockerEnvFile = defaultDockerEnvFile
	}
	if s.opts.ServerServiceName == "" {
		s.opts.ServerServiceName = defaultServerService
	}
	if s.opts.RunShell == nil {
		s.opts.RunShell = runShell
	}
	return s
}

// env reads a variable of the process environment, or the value a save
// put in its place.
func (s *Service) env(key string) string {
	return s.envValues([]string{key})[key]
}

// envValues reads several variables at one moment: a save that replaces
// them does so for all of them or for none.
func (s *Service) envValues(keys []string) map[string]string {
	values := make(map[string]string, len(keys))
	s.envMu.Lock()
	defer s.envMu.Unlock()
	for _, key := range keys {
		saved, ok := s.envSaved[key]
		switch {
		case !ok:
			values[key] = s.getenv(key)
		case saved != nil:
			values[key] = *saved
		default:
			values[key] = ""
		}
	}
	return values
}

// durableEnvPath is the public endpoints file in the data folder.
func (s *Service) durableEnvPath() string {
	return filepath.Join(s.opts.DataDir, DurableBasename)
}

type packageLogger struct{}

func (packageLogger) Warn(args ...any) {
	logging.Logf("console", "WARN: %s", strings.TrimSuffix(fmt.Sprintln(args...), "\n"))
}

func (packageLogger) Error(args ...any) {
	logging.Logf("console", "ERROR: %s", strings.TrimSuffix(fmt.Sprintln(args...), "\n"))
}
