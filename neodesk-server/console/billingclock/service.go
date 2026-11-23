package billingclock

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"strings"
	"syscall"

	"github.com/neodeskapp/neodesk/neodesk-server/console/advancedconfig"
	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
	"github.com/neodeskapp/neodesk/neodesk-server/console/serverinfo"
)

// The keys of the settings in the console's .env file.
const (
	keyNTPServers         = "NTP_SERVERS"
	keyMaxSkewMS          = "BILLING_MAX_CLOCK_SKEW_MS"
	keyRequireSyncedClock = "BILLING_REQUIRE_SYNCED_CLOCK"
	keyTrustOSNTP         = "BILLING_TRUST_OS_NTP"
	keyBillingTimezone    = "BILLING_TIMEZONE"
)

// apiConfigPath is where the server keeps the settings in the Docker
// layout with two containers.
const apiConfigPath = "/timesync/config"

// Options are the settings of a Service.
type Options struct {
	// EnvPath is the console's .env file (web-nodejs/.env,
	// config.EnvPath).
	EnvPath string
	// Docker says the console runs in a container (config.isDocker:
	// DOCKER=true or /.dockerenv).
	Docker bool
	// Getenv reads NEODESK_DOCKER_LAYOUT; "split" with Docker is the
	// layout with two containers. Nil means it is not set.
	Getenv func(string) string
	// Linux says the console runs on Linux (process.platform ===
	// 'linux'), which makes trust_os_ntp default to true.
	Linux bool
	// GoAPI reads and writes the settings in the layout with two
	// containers.
	GoAPI *goapi.Client
	// ServiceConfig brings the server's service definition up to date
	// after a save outside that layout, so that the server reads the .env
	// file at its next start, and returns what it did
	// (updateService.sanitizeGoServerServiceConfig, package servicedef).
	// Nil answers null.
	ServiceConfig func(ctx context.Context) any
	// HostZone is the time zone of the machine as
	// Intl.DateTimeFormat().resolvedOptions().timeZone reported it, and
	// TZ the value of the TZ variable. Nil means SystemHostZone and
	// os.Getenv.
	HostZone func() string
	TZ       func() string
}

// Service reads and writes the clock settings.
type Service struct {
	opts Options
}

// New returns a Service.
func New(opts Options) *Service {
	if opts.Getenv == nil {
		opts.Getenv = func(string) string { return "" }
	}
	if opts.HostZone == nil {
		opts.HostZone = SystemHostZone
	}
	if opts.TZ == nil {
		opts.TZ = func() string { return os.Getenv("TZ") }
	}
	return &Service{opts: opts}
}

// IsDockerSplitDeployment reports the Docker layout with one container
// for the console and one for the server.
func (s *Service) IsDockerSplitDeployment() bool {
	return s.opts.Docker && strings.ToLower(jsTrim(s.opts.Getenv("NEODESK_DOCKER_LAYOUT"))) == "split"
}

// DetectOSTimezone is the machine's time zone when Intl takes it, else
// the TZ variable when Intl takes that, else "UTC".
func (s *Service) DetectOSTimezone() string {
	if tz := s.opts.HostZone(); tz != "" && IsValidIANATimezone(tz) {
		return tz
	}
	if tz := jsTrim(s.opts.TZ()); tz != "" && IsValidIANATimezone(tz) {
		return tz
	}
	return DefaultBillingTimezone
}

// ParseEnvFile reads KEY=value lines: blank lines and lines that start
// with # are skipped, and so is a line without = after its first
// character. Key and value are trimmed; quotes stay. A key set twice
// keeps its last value.
func ParseEnvFile(content string) map[string]string {
	out := map[string]string{}
	for _, line := range splitLines(content) {
		trimmed := jsTrim(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		eq := strings.IndexByte(trimmed, '=')
		if eq <= 0 {
			continue
		}
		out[jsTrim(trimmed[:eq])] = jsTrim(trimmed[eq+1:])
	}
	return out
}

// splitLines is content.split(/\r?\n/).
func splitLines(content string) []string {
	lines := strings.Split(content, "\n")
	for i, line := range lines[:len(lines)-1] {
		lines[i] = strings.TrimSuffix(line, "\r")
	}
	return lines
}

// EnvToBool reads a flag of the file: Y, YES, 1, TRUE and ON in any
// letter case are true, N, NO, 0, FALSE and OFF are false, and anything
// else, a missing key (ok false) and an empty value are the fallback.
func EnvToBool(value string, ok bool, fallback bool) bool {
	if !ok || value == "" {
		return fallback
	}
	switch jsUpper(jsTrim(value)) {
	case "Y", "YES", "1", "TRUE", "ON":
		return true
	case "N", "NO", "0", "FALSE", "OFF":
		return false
	}
	return fallback
}

// boolToEnv writes a flag into the file.
func boolToEnv(v bool) string {
	if v {
		return "1"
	}
	return "0"
}

// SettingsFromEnvContent is the settings the text of a .env file holds,
// with the defaults for what it does not, and the machine's time zone
// for a missing or empty BILLING_TIMEZONE.
func (s *Service) SettingsFromEnvContent(content string) (Settings, error) {
	env := ParseEnvFile(content)
	raw := goapi.NewObject()
	raw.Set("ntp_servers", orDefault(env[keyNTPServers], DefaultNTPServers))
	if skew := env[keyMaxSkewMS]; skew != "" {
		raw.Set("max_skew_ms", skew)
	} else {
		raw.Set("max_skew_ms", float64(DefaultMaxSkewMS))
	}
	required, has := env[keyRequireSyncedClock]
	raw.Set("require_synced_clock", EnvToBool(required, has, true))
	trust, has := env[keyTrustOSNTP]
	raw.Set("trust_os_ntp", EnvToBool(trust, has, s.opts.Linux))
	zone := env[keyBillingTimezone]
	if zone == "" {
		zone = s.DetectOSTimezone()
	}
	raw.Set("billing_timezone", zone)
	return s.NormalizeSettings(raw)
}

func orDefault(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

// readEnv is the text of the .env file, "" when there is none.
func (s *Service) readEnv() (string, error) {
	if _, err := os.Stat(s.opts.EnvPath); err != nil {
		return "", nil
	}
	data, err := os.ReadFile(s.opts.EnvPath)
	if err != nil {
		return "", readError(err, s.opts.EnvPath)
	}
	return advancedconfig.DecodeUTF8(data), nil
}

// GetClockSettings is the settings in the .env file.
func (s *Service) GetClockSettings() (Settings, error) {
	content, err := s.readEnv()
	if err != nil {
		return Settings{}, err
	}
	return s.SettingsFromEnvContent(content)
}

// GetRuntimeClockSettings is the settings in effect: those of the .env
// file, and in the layout with two containers those the server reports,
// with the file's time zone when the server has none.
func (s *Service) GetRuntimeClockSettings(ctx context.Context) (Settings, error) {
	local, err := s.GetClockSettings()
	if err != nil {
		return Settings{}, err
	}
	if !s.IsDockerSplitDeployment() {
		return local, nil
	}
	resp, err := s.opts.GoAPI.Get(ctx, apiConfigPath)
	if err != nil {
		return Settings{}, err
	}
	raw := goapi.NewObject()
	remote := resp.Data()
	if o, ok := remote.(*goapi.Object); ok && goapi.Truthy(remote) {
		raw = o.Clone()
	}
	zone := member(remote, "billing_timezone")
	if zone == Undefined || !goapi.Truthy(zone) {
		zone = local.BillingTimezone
	}
	raw.Set("billing_timezone", zone)
	return s.NormalizeSettings(raw)
}

// writeEnv writes settings into the .env file: each key replaces the
// lines that set it, or goes to the end. A file that is created gets mode
// 0600.
func (s *Service) writeEnv(settings Settings) error {
	content, err := s.readEnv()
	if err != nil {
		return err
	}
	content = serverinfo.UpsertEnvKey(content, keyNTPServers, settings.NTPServers)
	content = serverinfo.UpsertEnvKey(content, keyMaxSkewMS, numberText(settings.MaxSkewMS))
	content = serverinfo.UpsertEnvKey(content, keyRequireSyncedClock, boolToEnv(settings.RequireSyncedClock))
	content = serverinfo.UpsertEnvKey(content, keyTrustOSNTP, boolToEnv(settings.TrustOSNTP))
	content = serverinfo.UpsertEnvKey(content, keyBillingTimezone, settings.BillingTimezone)
	if err := os.WriteFile(s.opts.EnvPath, []byte(content), 0o600); err != nil {
		return openError(err, s.opts.EnvPath)
	}
	return nil
}

// RestoreClockSettings writes settings back into the .env file, as a
// rollback of the restart transaction does. They are the settings the
// file held before a save, so they go back as they were read, without the
// checks of a save: a time zone, skew or server name an older console
// took, or someone wrote by hand, is put back too.
func (s *Service) RestoreClockSettings(raw any) (Settings, error) {
	settings, err := s.NormalizeSettings(raw)
	if err == nil {
		err = s.writeEnv(settings)
	}
	if err != nil {
		return Settings{}, err
	}
	return settings, nil
}

// Rollback undoes a save for the restart transaction. The descriptor is
// {"type": "billing-clock", "settings": <the settings before the save>}.
func (s *Service) Rollback(_ context.Context, descriptor *goapi.Object) error {
	_, err := s.RestoreClockSettings(member(descriptor, "settings"))
	return err
}

// SaveResult is what a save did.
type SaveResult struct {
	// Settings are the settings as saved.
	Settings Settings
	// ServiceConfig is what ServiceConfig reported, null in the layout
	// with two containers.
	ServiceConfig any
	// Status is the server's clock status after a save in the layout with
	// two containers, null otherwise.
	Status any
	// DockerMode says the server took the settings at once, without a
	// restart.
	DockerMode bool
}

// SaveClockSettings validates the settings in raw (a request body) and
// saves them. In the layout with two containers the server takes them
// through its API, and they are also written into the .env file where
// that works; an answer with an error refuses the save with that text.
// Otherwise they go into the .env file and the server's service
// definition is brought up to date; they take effect when the server
// restarts.
func (s *Service) SaveClockSettings(ctx context.Context, raw any) (SaveResult, error) {
	settings, err := s.ValidateSettings(raw)
	if err != nil {
		return SaveResult{}, err
	}
	if s.IsDockerSplitDeployment() {
		resp, err := s.opts.GoAPI.Put(ctx, apiConfigPath, goapi.Body(settings.Object()))
		if err != nil {
			return SaveResult{}, err
		}
		data := resp.Data()
		if !goapi.Truthy(data) {
			data = goapi.NewObject()
		}
		if refusal := member(data, "error"); refusal != Undefined && goapi.Truthy(refusal) {
			text, err := toText(refusal)
			if err != nil {
				return SaveResult{}, err
			}
			return SaveResult{}, errors.New(text)
		}
		_ = s.writeEnv(settings)
		saved := settings
		if config := member(data, "config"); config != Undefined && goapi.Truthy(config) {
			if saved, err = s.NormalizeSettings(config); err != nil {
				return SaveResult{}, err
			}
		}
		var status any
		if st := member(data, "status"); st != Undefined && goapi.Truthy(st) {
			status = st
		}
		return SaveResult{Settings: saved, Status: status, DockerMode: true}, nil
	}

	if err := s.writeEnv(settings); err != nil {
		return SaveResult{}, err
	}
	var serviceConfig any
	if s.opts.ServiceConfig != nil {
		serviceConfig = s.opts.ServiceConfig(ctx)
	}
	return SaveResult{Settings: settings, ServiceConfig: serviceConfig}, nil
}

// readError and openError give the message Node's fs functions gave for
// a failed read or write of the file, which the routes put into their
// answer: "EACCES: permission denied, open '<path>'".
func readError(err error, path string) error {
	if errors.Is(err, syscall.EISDIR) {
		return &fileError{msg: "EISDIR: illegal operation on a directory, read", err: err}
	}
	return openError(err, path)
}

func openError(err error, path string) error {
	code, text := "", ""
	var errno syscall.Errno
	switch {
	case errors.Is(err, fs.ErrNotExist):
		code, text = "ENOENT", "no such file or directory"
	case errors.As(err, &errno) && errno == syscall.EPERM:
		code, text = "EPERM", "operation not permitted"
	case errors.Is(err, fs.ErrPermission):
		code, text = "EACCES", "permission denied"
	case errors.Is(err, syscall.EISDIR):
		code, text = "EISDIR", "illegal operation on a directory"
	case errors.Is(err, syscall.ENOTDIR):
		code, text = "ENOTDIR", "not a directory"
	case errors.Is(err, syscall.EROFS):
		code, text = "EROFS", "read-only file system"
	case errors.Is(err, syscall.ENOSPC):
		code, text = "ENOSPC", "no space left on device"
	default:
		return err
	}
	return &fileError{msg: code + ": " + text + ", open '" + path + "'", err: err}
}

// fileError is a failed file operation with Node's message.
type fileError struct {
	msg string
	err error
}

func (e *fileError) Error() string { return e.msg }
func (e *fileError) Unwrap() error { return e.err }
