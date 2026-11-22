// Package config provides configuration management for BetterDesk server.
// Supports CLI flags, environment variables, and sensible defaults.
package config

import (
	"fmt"
	"log"
	"net"
	"net/url"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/unitronix/betterdesk-server/internal/envvar"
)

// Enrollment mode constants for Dual Key System.
const (
	EnrollmentModeOpen    = "open"    // Accept all device registrations (backward compatible)
	EnrollmentModeManaged = "managed" // New devices need approval or valid token
	EnrollmentModeLocked  = "locked"  // Only devices with valid tokens can register
)

// Config holds all server configuration.
type Config struct {
	// Network
	SignalPort int // UDP+TCP signal port (default 21116)
	RelayPort  int // TCP relay port (default 21117)
	APIPort    int // HTTP API / RustDesk client-API port (default 21114, signal_port-2)

	// Mode
	Mode string // "all"; the start refuses every other mode

	// Database
	DBPath     string // Database DSN: file path for SQLite, postgres:// URI for PostgreSQL (DATABASE_URL, else DB_URL, else -db)
	AuthDBPath string // Console auth.db (device groups, folders) — AUTH_DB_PATH

	// Crypto
	KeyFile string // Ed25519 key file path (without extension)

	// Servers
	RelayServers      string // Comma-separated relay server addresses
	RendezvousServers string // Comma-separated rendezvous server addresses
	// publicRelay is the relay the console's settings name ("Public relay
	// server"), which devices are given before RelayServers
	// (SetPublicRelayServer). relayMu guards it.
	relayMu     sync.RWMutex
	publicRelay string

	// Network mask
	Mask string // LAN mask (e.g. "192.168.0.0/24")

	// Relay options
	AlwaysUseRelay bool // Force relay for all connections

	// Security
	BlocklistFile string // Path to blocklist file (IP/ID/CIDR)

	// Audit
	AuditLogFile string // Path to audit log file (JSON lines)

	// TLS
	TLSCertFile string // Path to TLS certificate file
	TLSKeyFile  string // Path to TLS key file

	// Logging
	LogFormat string // "text" or "json"
	LogLevel  string // error | warn | info | debug

	// Admin
	AdminPort int // TCP admin interface port (0 = disabled)

	// Security — Authentication
	JWTSecret string // Secret key for JWT signing (auto-generated if empty)
	// OrgPeerVaultKey encrypts recoverable org peer passwords at rest (#367).
	// Falls back to JWT secret when empty (installers should set a dedicated key).
	OrgPeerVaultKey string
	// RemoteTargetVaultKey encrypts RDP/VNC credentials. It is deliberately
	// independent from JWT and the org peer vault; no fallback is allowed.
	RemoteTargetVaultKey string
	// GuacdAddress is the loopback address of the managed Guacamole daemon.
	GuacdAddress            string
	JWTExpiry               int    // JWT token expiry in hours (default 24)
	ClientSessionExpiryDays int    // RustDesk client session TTL in days (default 7)
	ClientSessionSliding    bool   // Extend client session on activity (default true)
	ClientSessionMaxDays    int    // Max sliding session lifetime from login (default 30)
	AdminPassword           string // Password for admin TCP interface (required when AdminPort is enabled)
	ForceHTTPS              bool   // Reject non-TLS API requests (except behind reverse proxy)
	TrustProxy              bool   // Trust X-Forwarded-For / X-Real-IP headers from reverse proxy
	// TrustedProxies is the CIDR allowlist of reverse proxies that may set
	// X-Forwarded-For / X-Real-IP. Required when TrustProxy is true — empty
	// means forwarded headers are ignored (security-first, issue #276).
	TrustedProxies []*net.IPNet
	// PanelSignalProxyCIDRs are the source addresses the console's bridge
	// for the web client (/ws/rendezvous) may reach the signal server from.
	// A TCP connection is the panel's web client only when its first frame
	// is the panel proof the signal server made at start and it comes from
	// one of these addresses; the address alone counts for nothing.
	// Default: loopback.
	PanelSignalProxyCIDRs []*net.IPNet
	RelayMaxConnsIP       int    // Max relay connections per IP (0 = unlimited)
	InitAdminUser         string // Initial admin username (created on first start)
	InitAdminPass         string // Initial admin password (auto-generated if empty)

	// Signal rate limiting (registrations per IP per minute).
	// Issue #122: large NAT deployments may need to raise or disable this
	// (set to 0 to disable rate limiting entirely).
	SignalRateLimitPerIP int

	// AllowLegacyOutbound permits controller-only stock RustDesk clients to
	// start sessions without first registering a local device identity. This is
	// deliberately opt-in because the server cannot apply initiator device
	// policy to an anonymous controller. It is honored only in open enrollment.
	AllowLegacyOutbound bool

	// SameNATRelay forces relay fallback when both peers connect from the
	// same public IP (i.e. they sit behind the same NAT gateway).  Many
	// consumer routers refuse hairpin NAT, so the LAN-address exchange that
	// normally works in this case can silently fail and the connection
	// times out (issue #121).  Default: enabled.
	SameNATRelay bool

	// AllowSharedNATInitiator authorizes PunchHole/RequestRelay when multiple
	// live peers share the initiator's public IP and no stronger identity
	// (token, exact addr, udp_port) is available (#399). Uses synthetic
	// initiator id "shared-nat-initiator" — does not inherit a peer identity
	// (#302). Default: disabled.
	AllowSharedNATInitiator bool

	// LoggedInOnlyInitiator requires a valid BetterDesk RustDesk client
	// session token for outbound PunchHole/RequestRelay initiation. The
	// panel Web Remote proxy remains an explicit exception. Default: disabled.
	LoggedInOnlyInitiator bool
	// OperatorOnlyOutbound requires a valid BetterDesk client session whose
	// user has device.connect permission for outbound PunchHole/RequestRelay.
	// It also disables address-based initiator fallbacks. Default: disabled.
	OperatorOnlyOutbound bool

	// P2PFirst enables the classic RustDesk hole-punching handshake: instead
	// of immediately answering the initiator with the target's (still
	// un-punched) address, the server forwards PunchHole to the target and
	// waits for its PunchHoleSent before delivering the genuine
	// PunchHoleResponse. This gives direct P2P a real chance to succeed
	// (issue #157). If the target does not complete hole punching within
	// P2PFallbackMs, a best-effort response is delivered so the client can
	// fall back to relay instead of hanging. Default: enabled.
	P2PFirst bool

	// P2PFallbackMs is the grace period (milliseconds) the server waits for a
	// target's PunchHoleSent before sending the relay-capable fallback
	// response. Only used when P2PFirst is enabled. Default: 2000.
	P2PFallbackMs int

	// WebSocket security (M3)
	AllowedWSOrigins    string // Comma-separated allowed WebSocket origins (empty = same-host only)
	APIAllowedWSOrigins string // Comma-separated allowed WebSocket origins for HTTP API events endpoint

	// Metrics endpoint access control (audit fix H-03, 2026-04-10)
	// MetricsAllowlist: comma-separated list of IP / CIDR allowed to call /metrics.
	//   Empty + MetricsPublic=false => /metrics requires authentication.
	//   Non-empty                  => /metrics is open to listed IPs only, no auth.
	// MetricsPublic: when true, /metrics is reachable without auth from anywhere
	//   (legacy behavior). Off by default.
	MetricsAllowlist string
	MetricsPublic    bool

	// TLS for signal/relay/api (Phase 3 + Phase 21)
	TLSSignal bool // Enable TLS on TCP signal (:21116) and WS signal (:21118)
	TLSRelay  bool // Enable TLS on TCP relay (:21117) and WS relay (:21119)
	TLSApi    bool // Enable TLS on HTTP API (:21114) — opt-in, not automatic

	// Dual Key System - Enrollment Mode
	// "open" (default) - Accept all device registrations (backward compatible)
	// "managed" - New devices need to be approved or have a valid token
	// "locked" - Only devices with valid tokens can register
	// ENROLLMENT_MODE is the first default: it applies while the database
	// holds no mode, and the start stores it. A mode saved in the console
	// wins over it, unless ENROLLMENT_MODE_ENV_OVERRIDE=Y forces it.
	EnrollmentMode            string
	EnrollmentModeFromEnv     bool // EnrollmentMode came from ENROLLMENT_MODE
	EnrollmentModeEnvOverride bool // ENROLLMENT_MODE_ENV_OVERRIDE=Y: the environment wins at every start

	// CDAP Gateway
	CDAPPort        int  // WebSocket gateway port (default 21122)
	CDAPEnabled     bool // Enable CDAP gateway (default true)
	CDAPTLS         bool // Enable TLS on CDAP port
	CDAPTLSRequired bool // Reject plaintext connections on the CDAP port
	CDAPRateLimit   int  // Max requests per minute per IP (default 30)

	// MeshCentral compatibility layer
	MeshCentralEnabled bool   // MESH_ENABLED
	MeshCoreVersion    string // MESH_CORE_VERSION pin
	MeshAgentCertFile  string // MESH_AGENT_CERT_FILE RSA-3072 agent-server key
	MeshWebCertFile    string // MESH_WEB_CERT_FILE public TLS cert agents see (proxy LE); overrides TLS_CERT for webHash
	MeshAssetsDir      string // optional override for meshcore assets
	MeshRateLimit      int    // WS upgrade rate limit per IP per minute

	// Time sync / billing (commercialization module)
	NTPServers                string // Comma-separated NTP servers
	BillingMaxClockSkewMS     int    // Max allowed clock offset vs NTP (default 2000)
	BillingRequireSyncedClock bool   // Block billable sessions when clock unsynced
	BillingTrustOSNTP         bool   // Trust OS NTP (timedatectl) when direct queries fail
	BillingRoundingMinutes    int    // Billable minute rounding (1, 10, 15)
	BillingRequireWorkReport  bool   // Require technician report before session close
	BillingTimezone           string // IANA timezone for billing month boundaries (e.g. Europe/Warsaw)

	connectionMu sync.RWMutex
}

const (
	// PersistedConnectionSettingsKey is the shared server_config key used by
	// the panel's split-Docker connection strategy API.
	PersistedConnectionSettingsKey = "connection_strategy"
	// MaxP2PFallbackMs bounds panel-managed runtime changes. Environment and
	// CLI values retain their historical parsing behavior for compatibility.
	MaxP2PFallbackMs = 30000
)

// ConnectionSettings is the runtime P2P/relay policy exposed to the panel.
// Mode is derived from P2PFirst and AlwaysUseRelay when read from Config.
type ConnectionSettings struct {
	Mode                    string `json:"mode"`
	P2PFallbackMs           int    `json:"p2p_fallback_ms"`
	SameNATRelay            bool   `json:"same_nat_relay"`
	AllowSharedNATInitiator bool   `json:"allow_shared_nat_initiator"`
	LoggedInOnlyInitiator   bool   `json:"logged_in_only_initiator"`
	OperatorOnlyOutbound    bool   `json:"operator_only_outbound"`
	P2PFirst                bool   `json:"p2p_first,omitempty"`
	AlwaysUseRelay          bool   `json:"always_use_relay,omitempty"`
}

// ConnectionSettings returns a consistent snapshot of the live connection
// policy. Callers handling signal traffic must use this instead of reading
// the individual mutable fields directly.
func (c *Config) ConnectionSettings() ConnectionSettings {
	if c == nil {
		return ConnectionSettings{}
	}
	c.connectionMu.RLock()
	defer c.connectionMu.RUnlock()

	mode := "p2p_first"
	if c.AlwaysUseRelay || !c.P2PFirst {
		mode = "relay_only"
	}
	return ConnectionSettings{
		Mode:                    mode,
		P2PFallbackMs:           c.P2PFallbackMs,
		SameNATRelay:            c.SameNATRelay,
		AllowSharedNATInitiator: c.AllowSharedNATInitiator,
		LoggedInOnlyInitiator:   c.LoggedInOnlyInitiator,
		OperatorOnlyOutbound:    c.OperatorOnlyOutbound,
		P2PFirst:                c.P2PFirst,
		AlwaysUseRelay:          c.AlwaysUseRelay,
	}
}

// ApplyConnectionSettings atomically updates the live connection policy.
// Validation is intentionally performed by the API/startup boundary so this
// method can also preserve existing environment/CLI values during startup.
func (c *Config) ApplyConnectionSettings(settings ConnectionSettings) {
	if c == nil {
		return
	}
	c.connectionMu.Lock()
	defer c.connectionMu.Unlock()

	c.P2PFallbackMs = settings.P2PFallbackMs
	c.SameNATRelay = settings.SameNATRelay
	c.AllowSharedNATInitiator = settings.AllowSharedNATInitiator
	c.LoggedInOnlyInitiator = settings.LoggedInOnlyInitiator
	c.OperatorOnlyOutbound = settings.OperatorOnlyOutbound
	c.P2PFirst = settings.Mode != "relay_only"
	c.AlwaysUseRelay = settings.Mode == "relay_only"
}

// ValidateConnectionSettings validates panel-persisted connection policy.
func (s ConnectionSettings) Validate() error {
	if s.Mode != "p2p_first" && s.Mode != "relay_only" {
		return fmt.Errorf("mode must be p2p_first or relay_only")
	}
	if s.P2PFallbackMs < 0 || s.P2PFallbackMs > MaxP2PFallbackMs {
		return fmt.Errorf("p2p_fallback_ms must be between 0 and %d", MaxP2PFallbackMs)
	}
	return nil
}

// DefaultPanelSignalProxyCIDRs are the addresses the console's bridge
// connects from when it dials the signal server on localhost.
const DefaultPanelSignalProxyCIDRs = "127.0.0.0/8,::1/128"

// DefaultConfig returns a Config with sensible defaults.
func DefaultConfig() *Config {
	panelCIDRs, _ := ParseTrustedProxies(DefaultPanelSignalProxyCIDRs)
	return &Config{
		SignalPort:                21116,
		RelayPort:                 21117,
		APIPort:                   DefaultAPIPort,
		Mode:                      "all",
		DBPath:                    "./db_v2.sqlite3",
		KeyFile:                   "id_ed25519",
		JWTExpiry:                 24,
		ClientSessionExpiryDays:   7,
		ClientSessionSliding:      true,
		ClientSessionMaxDays:      30,
		RelayMaxConnsIP:           20,
		EnrollmentMode:            EnrollmentModeOpen, // Backward compatible default
		PanelSignalProxyCIDRs:     panelCIDRs,
		CDAPPort:                  21122,
		CDAPEnabled:               true, // Enabled by default; set CDAP_ENABLED=N for minimal installs
		CDAPRateLimit:             30,
		MeshCentralEnabled:        true, // default on; set MESH_ENABLED=N to disable
		MeshCoreVersion:           "1.2.0",
		MeshAgentCertFile:         "mesh_agent_server.pem",
		MeshRateLimit:             30,
		GuacdAddress:              "127.0.0.1:4822",
		SignalRateLimitPerIP:      IPRateLimitRegistrations,
		SameNATRelay:              true,  // issue #121: auto-fallback to relay on shared public IP
		AllowSharedNATInitiator:   false, // issue #399: opt-in stock multi-NAT initiator
		LoggedInOnlyInitiator:     false, // issue #414: require client login for stock initiators
		OperatorOnlyOutbound:      false, // issue #425: restrict outbound initiation to connected operators
		P2PFirst:                  true,  // issue #157: give direct P2P a real chance before relay
		P2PFallbackMs:             2000,  // grace period for target hole punch before relay fallback
		LogLevel:                  "info",
		BillingMaxClockSkewMS:     2000,
		BillingRequireSyncedClock: true,
		BillingTrustOSNTP:         runtime.GOOS == "linux",
		BillingRoundingMinutes:    1,
		BillingTimezone:           "", // empty → resolve from OS / UTC at use site
	}
}

// readSwitch sets *field from the on/off variable name when it holds an
// on/off value (envvar.Switch), so a variable also turns off what a flag
// turned on. Another value is logged and leaves the field as it is.
func readSwitch(name string, field *bool) {
	value := os.Getenv(name)
	if strings.TrimSpace(value) == "" {
		return
	}
	on, ok := envvar.Switch(value)
	if !ok {
		log.Printf("[config] %s=%q is not an on/off value (%s): it stays %v", name, value, envvar.SwitchValues, *field)
		return
	}
	*field = on
}

// LoadEnv overrides config values from environment variables.
// Environment variables take precedence over CLI flags.
func (c *Config) LoadEnv() {
	connection := c.ConnectionSettings()

	// The ID server's port is SIGNAL_PORT. PORT is the old console's port
	// and moves nothing; a start that still has it says so.
	if v := os.Getenv("SIGNAL_PORT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.SignalPort = n
		}
	} else if v := os.Getenv("PORT"); v != "" {
		log.Printf("[config] PORT=%s is not read: the ID server listens on SIGNAL_PORT (now %d) and the console on CONSOLE_PORT", v, c.SignalPort)
	}
	if v := os.Getenv("RELAY_PORT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.RelayPort = n
		}
	}
	// GO_API_PORT takes precedence over API_PORT.
	// Shared .env sets API_PORT=21121 for the Node Client API compat proxy; Go handlers
	// stay on GO_API_PORT (default 21114). Without this, LoadEnv would override -api-port.
	if v := os.Getenv("GO_API_PORT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.APIPort = n
		}
	} else if v := os.Getenv("API_PORT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.APIPort = n
		}
	}
	if v := os.Getenv("MODE"); v != "" {
		c.Mode = strings.ToLower(v)
	}
	// DATABASE_URL names the database; DB_URL and DB_TYPE are read for
	// older installs
	database := envvar.DatabaseFrom(os.Getenv)
	if database.DSN != "" {
		c.DBPath = database.DSN
	}
	for _, warning := range database.Warnings {
		log.Printf("[config] %s", warning)
	}
	if v := os.Getenv("AUTH_DB_PATH"); v != "" {
		c.AuthDBPath = v
	}
	if v := os.Getenv("KEY_FILE"); v != "" {
		c.KeyFile = v
	}
	if v := os.Getenv("RELAY_SERVERS"); v != "" {
		c.RelayServers = v
	}
	if v := os.Getenv("RENDEZVOUS_SERVERS"); v != "" {
		c.RendezvousServers = v
	}
	if v := os.Getenv("MASK"); v != "" {
		c.Mask = v
	}
	readSwitch("ALWAYS_USE_RELAY", &connection.AlwaysUseRelay)
	if v := os.Getenv("BLOCKLIST_FILE"); v != "" {
		c.BlocklistFile = v
	}
	if v := os.Getenv("AUDIT_LOG_FILE"); v != "" {
		c.AuditLogFile = v
	}
	if v := os.Getenv("TLS_CERT"); v != "" {
		c.TLSCertFile = v
	}
	if v := os.Getenv("TLS_KEY"); v != "" {
		c.TLSKeyFile = v
	}
	if v := os.Getenv("LOG_FORMAT"); v != "" {
		// GO-H6: validate against whitelist to prevent silent misconfiguration
		lv := strings.ToLower(v)
		if lv == "text" || lv == "json" {
			c.LogFormat = lv
		}
	}
	if v := os.Getenv("LOG_LEVEL"); v != "" {
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "error", "fatal", "warn", "warning", "info", "debug":
			c.LogLevel = strings.ToLower(strings.TrimSpace(v))
		}
	}
	if v := os.Getenv("ADMIN_PORT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.AdminPort = n
		}
	}
	if v := os.Getenv("JWT_SECRET"); v != "" {
		c.JWTSecret = v
	}
	if v := os.Getenv("ORG_PEER_VAULT_KEY"); v != "" {
		c.OrgPeerVaultKey = v
	}
	if v := os.Getenv("REMOTE_TARGET_VAULT_KEY"); v != "" {
		c.RemoteTargetVaultKey = v
	}
	if v := os.Getenv("GUACD_ADDRESS"); v != "" {
		c.GuacdAddress = v
	}
	if v := os.Getenv("JWT_EXPIRY_HOURS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.JWTExpiry = n
		}
	}
	if v := os.Getenv("CLIENT_SESSION_EXPIRY_DAYS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			c.ClientSessionExpiryDays = n
		}
	}
	readSwitch("CLIENT_SESSION_SLIDING", &c.ClientSessionSliding)
	if v := os.Getenv("CLIENT_SESSION_MAX_DAYS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			c.ClientSessionMaxDays = n
		}
	}
	if v := os.Getenv("ADMIN_PASSWORD"); v != "" {
		c.AdminPassword = v
	}
	readSwitch("FORCE_HTTPS", &c.ForceHTTPS)
	// TRUST_PROXY also holds the console's hop counts and address lists
	// (console/httpx), which leave the server's switch as it is
	if on, ok := envvar.Switch(os.Getenv("TRUST_PROXY")); ok {
		c.TrustProxy = on
	}
	if v := os.Getenv("TRUSTED_PROXIES"); v != "" {
		nets, err := ParseTrustedProxies(v)
		if err != nil {
			log.Printf("[config] TRUSTED_PROXIES parse error: %v — forwarded headers will not be honored", err)
		} else {
			c.TrustedProxies = nets
		}
	}
	// Source addresses of the console's web client bridge. Unset keeps the
	// loopback default; set it when WS_HBBS_HOST names another address of
	// this machine.
	if v := os.Getenv("PANEL_SIGNAL_PROXY_CIDRS"); v != "" {
		nets, err := ParseTrustedProxies(v)
		if err != nil {
			log.Printf("[config] PANEL_SIGNAL_PROXY_CIDRS parse error: %v — keeping previous allowlist", err)
		} else {
			c.PanelSignalProxyCIDRs = nets
		}
	}
	if v := os.Getenv("RELAY_MAX_CONNS_PER_IP"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.RelayMaxConnsIP = n
		}
	}
	// Issue #122: allow tuning the per-IP signal/registration rate limit
	// without recompiling. Useful for large NAT deployments where many
	// devices share the same public IP. Set to 0 to disable entirely.
	if v := os.Getenv("SIGNAL_RATE_LIMIT_PER_IP"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			c.SignalRateLimitPerIP = n
		}
	}
	readSwitch("ALLOW_LEGACY_OUTBOUND", &c.AllowLegacyOutbound)
	// Issue #121: when both peers share the same public IP, the LAN
	// hole-punch path requires NAT hairpinning, which many consumer
	// routers (and most cellular gateways) silently drop.  Enabled by
	// default; set SAME_NAT_RELAY=N to fall back to the legacy LAN-exchange
	// behavior.
	readSwitch("SAME_NAT_RELAY", &connection.SameNATRelay)
	// Issue #399: allow PunchHole/RequestRelay from shared public IPs with
	// multiple live peers when stock clients cannot prove initiator identity
	// (no token / udp_port). Default off — preserves #302 enrollment gate.
	readSwitch("ALLOW_SHARED_NAT_INITIATOR", &connection.AllowSharedNATInitiator)
	// Issue #414: require a valid BetterDesk client login token for stock
	// PunchHole/RequestRelay initiators. The console's web client, which
	// proves itself to the signal server, remains allowed.
	readSwitch("LOGGED_IN_ONLY_INITIATOR", &connection.LoggedInOnlyInitiator)
	// Issue #425: require a valid client session belonging to a user with
	// device.connect permission for stock PunchHole/RequestRelay initiators.
	// The console's web client, which proves itself to the signal server,
	// remains the authenticated Web Remote exception.
	readSwitch("OPERATOR_ONLY_OUTBOUND", &connection.OperatorOnlyOutbound)
	// Issue #157: P2P-first hole punching. Enabled by default so direct
	// connections are attempted before relay. Set P2P_FIRST=N to restore the
	// legacy behavior of answering the initiator immediately (always relay).
	readSwitch("P2P_FIRST", &connection.P2PFirst)
	if v := os.Getenv("P2P_FALLBACK_MS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			connection.P2PFallbackMs = n
		}
	}
	if connection.AlwaysUseRelay || !connection.P2PFirst {
		connection.Mode = "relay_only"
	} else {
		connection.Mode = "p2p_first"
	}
	c.ApplyConnectionSettings(connection)
	if v := os.Getenv("INIT_ADMIN_USER"); v != "" {
		c.InitAdminUser = v
	}
	if v := os.Getenv("INIT_ADMIN_PASS"); v != "" {
		c.InitAdminPass = v
	}
	if v := os.Getenv("WS_ALLOWED_ORIGINS"); v != "" {
		c.AllowedWSOrigins = v
	}
	if v := os.Getenv("API_WS_ALLOWED_ORIGINS"); v != "" {
		c.APIAllowedWSOrigins = v
	}
	if v := os.Getenv("METRICS_IP_ALLOWLIST"); v != "" {
		c.MetricsAllowlist = v
	}
	readSwitch("METRICS_PUBLIC", &c.MetricsPublic)
	readSwitch("TLS_SIGNAL", &c.TLSSignal)
	readSwitch("TLS_RELAY", &c.TLSRelay)
	readSwitch("TLS_API", &c.TLSApi)
	c.EnrollmentModeFromEnv, c.EnrollmentModeEnvOverride = false, false
	if v := os.Getenv("ENROLLMENT_MODE"); v != "" {
		mode := strings.ToLower(v)
		if mode == "open" || mode == "managed" || mode == "locked" {
			c.EnrollmentMode = mode
			c.EnrollmentModeFromEnv = true
			marker := strings.ToUpper(strings.TrimSpace(os.Getenv("ENROLLMENT_MODE_ENV_OVERRIDE")))
			c.EnrollmentModeEnvOverride = marker == "Y" || marker == "YES" || marker == "TRUE" || marker == "1"
		}
	}
	if v := os.Getenv("CDAP_PORT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.CDAPPort = n
		}
	}
	readSwitch("CDAP_ENABLED", &c.CDAPEnabled)
	readSwitch("CDAP_TLS", &c.CDAPTLS)
	// Required TLS turns TLS on; turning the requirement off leaves
	// CDAP_TLS as it is
	readSwitch("CDAP_TLS_REQUIRED", &c.CDAPTLSRequired)
	if c.CDAPTLSRequired {
		c.CDAPTLS = true
	}
	if v := os.Getenv("CDAP_RATE_LIMIT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			c.CDAPRateLimit = n
		}
	}
	readSwitch("MESH_ENABLED", &c.MeshCentralEnabled)
	if v := os.Getenv("MESH_CORE_VERSION"); v != "" {
		c.MeshCoreVersion = v
	}
	if v := os.Getenv("MESH_AGENT_CERT_FILE"); v != "" {
		c.MeshAgentCertFile = v
	}
	if v := os.Getenv("MESH_WEB_CERT_FILE"); v != "" {
		c.MeshWebCertFile = v
	}
	if v := os.Getenv("MESH_ASSETS_DIR"); v != "" {
		c.MeshAssetsDir = v
	}
	if v := os.Getenv("MESH_RATE_LIMIT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			c.MeshRateLimit = n
		}
	}
	if v := os.Getenv("NTP_SERVERS"); v != "" {
		c.NTPServers = v
	}
	if v := os.Getenv("BILLING_MAX_CLOCK_SKEW_MS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			c.BillingMaxClockSkewMS = n
		}
	}
	readSwitch("BILLING_REQUIRE_SYNCED_CLOCK", &c.BillingRequireSyncedClock)
	readSwitch("BILLING_TRUST_OS_NTP", &c.BillingTrustOSNTP)
	if v := os.Getenv("BILLING_ROUNDING_MINUTES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			c.BillingRoundingMinutes = n
		}
	}
	readSwitch("BILLING_REQUIRE_WORK_REPORT", &c.BillingRequireWorkReport)
	if v := strings.TrimSpace(os.Getenv("BILLING_TIMEZONE")); v != "" {
		if _, err := time.LoadLocation(v); err == nil {
			c.BillingTimezone = v
		}
	}
}

// BillingLocation returns the IANA location used for monthly billing boundaries.
// Falls back to the process local zone, then UTC.
func (c *Config) BillingLocation() *time.Location {
	if c != nil {
		if tz := strings.TrimSpace(c.BillingTimezone); tz != "" {
			if loc, err := time.LoadLocation(tz); err == nil {
				return loc
			}
		}
	}
	if loc := time.Local; loc != nil {
		return loc
	}
	return time.UTC
}

// ValidateAdminInterface rejects an enabled admin TCP listener without a
// password. The listener provides privileged server management commands, so
// binding it without authentication is not permitted.
func (c *Config) ValidateAdminInterface() error {
	if c != nil && c.AdminPort != 0 && strings.TrimSpace(c.AdminPassword) == "" {
		return fmt.Errorf("ADMIN_PASSWORD is required when ADMIN_PORT is enabled")
	}
	return nil
}

// GetNTPServers returns configured NTP server hostnames.
func (c *Config) GetNTPServers() []string {
	if c.NTPServers == "" {
		return []string{"pool.ntp.org", "time.google.com", "time.cloudflare.com"}
	}
	parts := strings.Split(c.NTPServers, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// NATTestPort returns the NAT test port (signal port - 1).
func (c *Config) NATTestPort() int {
	return c.SignalPort - 1
}

// WSSignalPort returns the WebSocket signal port (signal port + 2).
func (c *Config) WSSignalPort() int {
	return c.SignalPort + 2
}

// WSRelayPort returns the WebSocket relay port (relay port + 2).
func (c *Config) WSRelayPort() int {
	return c.RelayPort + 2
}

// SetPublicRelayServer sets the relay the console's settings name
// ("Public relay server"): a host with a port or without, "" for none.
// Devices are given it in place of RELAY_SERVERS, which is the first
// default (DECISIONS A105). The console calls it at start and after a save.
func (c *Config) SetPublicRelayServer(relay string) {
	c.relayMu.Lock()
	defer c.relayMu.Unlock()
	c.publicRelay = strings.TrimSpace(relay)
}

// PublicRelayServer is the relay SetPublicRelayServer set.
func (c *Config) PublicRelayServer() string {
	c.relayMu.RLock()
	defer c.relayMu.RUnlock()
	return c.publicRelay
}

// GetRelayServers returns the relays devices are given: the relay the
// console's settings name (SetPublicRelayServer), else the comma-separated
// RelayServers list. Each entry gets the default RelayPort when it names
// no port. Entries that cannot be a host (a single character, a malformed
// address) are left out, as relay addresses like "a:21117" make every
// relay connection fail.
func (c *Config) GetRelayServers() []string {
	if relay, ok := c.relayAddress(c.PublicRelayServer()); ok {
		return []string{relay}
	}
	if c.RelayServers == "" {
		return nil
	}
	servers := strings.Split(c.RelayServers, ",")
	result := make([]string, 0, len(servers))
	for _, s := range servers {
		if relay, ok := c.relayAddress(s); ok {
			result = append(result, relay)
		}
	}
	return result
}

// relayAddress is one relay entry with its port, and false for an empty
// or invalid one (logged).
func (c *Config) relayAddress(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", false
	}
	// net.SplitHostPort handles IPv6 bracketed addresses correctly.
	if _, _, err := net.SplitHostPort(s); err != nil {
		s = net.JoinHostPort(s, strconv.Itoa(c.RelayPort))
	}
	host, _, err := net.SplitHostPort(s)
	if err != nil || len(host) < 2 {
		log.Printf("[config] WARNING: Ignoring invalid relay server %q (host too short or malformed)", s)
		return "", false
	}
	return s, true
}

// GetRendezvousServers parses the configured rendezvous (ID) server list for
// ConfigUpdate advertisements. Entries get the signal port when it is missing.
//
// ConfigUpdate.rendezvous_servers tells the client which ID server to talk to,
// so it must never be populated with relay addresses: a RustDesk client that
// honours ConfigUpdate would reconnect its signal socket to the relay port
// (21117), stop answering punch/relay requests, and every session would fail
// with a relay pair timeout.
func (c *Config) GetRendezvousServers() []string {
	if c.RendezvousServers == "" {
		return nil
	}
	servers := strings.Split(c.RendezvousServers, ",")
	result := make([]string, 0, len(servers))
	for _, s := range servers {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if _, _, err := net.SplitHostPort(s); err != nil {
			s = net.JoinHostPort(s, strconv.Itoa(c.SignalPort))
		}
		host, _, err := net.SplitHostPort(s)
		if err != nil || len(host) < 2 {
			log.Printf("[config] WARNING: Ignoring invalid rendezvous server %q (host too short or malformed)", s)
			continue
		}
		result = append(result, s)
	}
	return result
}

// GetAllowedWSOrigins parses the comma-separated list of allowed WebSocket origins.
// Returns nil when unset so websocket.Accept uses its safe same-host default.
// Supports glob patterns accepted by nhooyr.io/websocket:
//   - "*" matches any origin
//   - "*.example.com" matches subdomains
//   - "https://app.example.com" matches exact origin
func (c *Config) GetAllowedWSOrigins() []string {
	if c.AllowedWSOrigins == "" {
		return nil
	}
	origins := strings.Split(c.AllowedWSOrigins, ",")
	result := make([]string, 0, len(origins))
	for _, o := range origins {
		o = strings.TrimSpace(o)
		if o != "" {
			result = append(result, o)
		}
	}
	return result
}

// GetAPIAllowedWSOrigins parses the comma-separated list of allowed WebSocket origins
// for the HTTP API events endpoint. Returns nil if empty (defaults to safe same-origin
// behavior enforced by the WebSocket library).
func (c *Config) GetAPIAllowedWSOrigins() []string {
	if c.APIAllowedWSOrigins == "" {
		return nil
	}
	origins := strings.Split(c.APIAllowedWSOrigins, ",")
	result := make([]string, 0, len(origins))
	for _, o := range origins {
		o = strings.TrimSpace(o)
		if o != "" {
			result = append(result, o)
		}
	}
	return result
}

// GetMetricsAllowlist returns the parsed list of IP / CIDR allowed to call /metrics.
// Used together with MetricsPublic to decide whether to require authentication.
func (c *Config) GetMetricsAllowlist() []string {
	if c.MetricsAllowlist == "" {
		return nil
	}
	parts := strings.Split(c.MetricsAllowlist, ",")
	result := make([]string, 0, len(parts))
	for _, o := range parts {
		o = strings.TrimSpace(o)
		if o != "" {
			result = append(result, o)
		}
	}
	return result
}

// HasTLSCert returns true if both TLS certificate and key files are configured.
func (c *Config) HasTLSCert() bool {
	return c.TLSCertFile != "" && c.TLSKeyFile != ""
}

// SignalTLSEnabled returns true if TLS should be used for signal server
// TCP and WebSocket listeners. Requires both --tls-signal flag and valid cert/key.
func (c *Config) SignalTLSEnabled() bool {
	return c.TLSSignal && c.HasTLSCert()
}

// RelayTLSEnabled returns true if TLS should be used for relay server
// TCP and WebSocket listeners. Requires both --tls-relay flag and valid cert/key.
func (c *Config) RelayTLSEnabled() bool {
	return c.TLSRelay && c.HasTLSCert()
}

// APITLSEnabled returns true if TLS should be used for the HTTP API server.
// Requires explicit --tls-api flag and valid cert/key.
// NOTE: --force-https does NOT imply API TLS because RustDesk desktop clients
// always send plain HTTP to signal_port-2 (the API port). Enabling TLS on the
// API would return HTTP 400 to every client and prevent device registration.
func (c *Config) APITLSEnabled() bool {
	return c.TLSApi && c.HasTLSCert()
}

// CDAPTLSEnabled returns true if TLS should be used for the CDAP WebSocket gateway.
func (c *Config) CDAPTLSEnabled() bool {
	return (c.CDAPTLS || c.CDAPTLSRequired) && c.HasTLSCert()
}

// CDAPTLSRequiredEnabled returns true when the CDAP gateway must reject
// plaintext connections instead of accepting the backwards-compatible
// dual-mode protocol.
func (c *Config) CDAPTLSRequiredEnabled() bool {
	return c.CDAPTLSRequired && c.HasTLSCert()
}

// RedactedDBPath returns a loggable form of DBPath without credentials.
//
// The startup banner prints it (GHSA-qxwx-jg77-5v67). A PostgreSQL DSN keeps
// its scheme, host, port, database name and query options; the user name and
// the password go, in the user part and in the query. SQLite file paths
// contain no credentials and are returned unchanged.
func RedactedDBPath(dsn string) string {
	if dsn == "" {
		return ""
	}
	// Only URI-style DSNs carry credentials. Plain filesystem paths do not.
	if !strings.Contains(dsn, "://") {
		return dsn
	}
	parsed, err := url.Parse(dsn)
	if err != nil {
		// Unparseable: avoid echoing anything that might contain a secret.
		return "<unparseable database DSN>"
	}
	parsed.User = nil
	if parsed.RawQuery != "" {
		query := parsed.Query()
		removed := false
		for _, key := range []string{"user", "password", "passwd", "pwd"} {
			if query.Has(key) {
				query.Del(key)
				removed = true
			}
		}
		if removed {
			parsed.RawQuery = query.Encode()
		}
	}
	return parsed.String()
}
