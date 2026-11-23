// Package apitest builds an API server for tests, wired the way the server
// binary wires it, on a new SQLite database and without any listener.
package apitest

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/api"
	"github.com/neodeskapp/neodesk/neodesk-server/audit"
	"github.com/neodeskapp/neodesk/neodesk-server/auth"
	"github.com/neodeskapp/neodesk/neodesk-server/billing"
	"github.com/neodeskapp/neodesk/neodesk-server/cdap"
	"github.com/neodeskapp/neodesk/neodesk-server/config"
	"github.com/neodeskapp/neodesk/neodesk-server/crypto"
	"github.com/neodeskapp/neodesk/neodesk-server/db"
	"github.com/neodeskapp/neodesk/neodesk-server/events"
	"github.com/neodeskapp/neodesk/neodesk-server/meshcentral"
	"github.com/neodeskapp/neodesk/neodesk-server/metrics"
	"github.com/neodeskapp/neodesk/neodesk-server/peer"
	"github.com/neodeskapp/neodesk/neodesk-server/ratelimit"
	"github.com/neodeskapp/neodesk/neodesk-server/relay"
	"github.com/neodeskapp/neodesk/neodesk-server/security"
	"github.com/neodeskapp/neodesk/neodesk-server/timesync"
)

// Version is the version the server reports.
const Version = "test"

// AdminUser is the account the server creates on an empty database.
const AdminUser = "tadmin"

// AdminPassword is the made-up password of AdminUser.
const AdminPassword = "Made-Up-Pass-1"

// Server is an API server with the parts tests reach into.
type Server struct {
	API    *api.Server
	DB     db.Database
	DBPath string
	Bus    *events.Bus
	Peers  *peer.Map
	Config *config.Config
}

// New builds the server. Everything is closed when the test ends.
//
// The working directory becomes a new temporary directory for the rest of
// the test, as it is where the server puts the files it names without a
// directory (the certificate for mesh agents). A test that reads files of
// its own by a relative path has to resolve the path before.
func New(t testing.TB) *Server {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)
	cfg := config.DefaultConfig()
	cfg.DBPath = filepath.Join(dir, "db.sqlite3")
	cfg.KeyFile = filepath.Join(dir, "id_ed25519")

	kp, err := crypto.LoadOrGenerateKeyPair(cfg.KeyFile)
	if err != nil {
		t.Fatalf("keypair: %v", err)
	}
	database, err := db.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	if err := database.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := database.EnsureClientSessionsSchema(); err != nil {
		t.Fatalf("client sessions schema: %v", err)
	}

	blocklist := security.NewBlocklist()
	bwLimiter := ratelimit.NewBandwidthLimiter(config.DefaultTotalBandwidth, config.DefaultSingleBandwidth)
	jwtSecret, err := auth.GenerateRandomString(32)
	if err != nil {
		t.Fatalf("jwt secret: %v", err)
	}
	jwtManager := auth.NewJWTManager(jwtSecret, 24*time.Hour)

	hash, err := auth.HashPassword(AdminPassword)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	if err := database.CreateUser(&db.User{Username: AdminUser, PasswordHash: hash, Role: auth.RoleAdmin}); err != nil {
		t.Fatalf("create admin: %v", err)
	}

	auditLogger := audit.NewLogger(cfg.AuditLogFile)
	t.Cleanup(func() { auditLogger.Close() })
	auditLogger.Log(audit.ActionServerStart, "system", "", map[string]string{"version": Version, "mode": cfg.Mode})
	timeSync := timesync.NewService(database, timesync.Config{
		Servers:         cfg.GetNTPServers(),
		Interval:        60 * time.Second,
		QueryTimeout:    5 * time.Second,
		MaxSkew:         time.Duration(cfg.BillingMaxClockSkewMS) * time.Millisecond,
		RequireSync:     cfg.BillingRequireSyncedClock,
		TrustOSNTP:      cfg.BillingTrustOSNTP,
		BillingTimezone: cfg.BillingTimezone,
	})
	billingSvc := billing.NewService(database, timeSync, cfg.BillingRoundingMinutes, cfg.BillingRequireWorkReport)

	peers := peer.NewMap()
	bus := events.NewBus()
	relaySrv := relay.New(cfg)
	relaySrv.SetBandwidthLimiter(bwLimiter)

	srv := api.New(cfg, database, peers, relaySrv, Version)
	srv.SetBlocklist(blocklist)
	srv.SetBandwidthLimiter(bwLimiter)
	srv.SetAuditLogger(auditLogger)
	srv.SetEventBus(bus)
	srv.SetMetrics(metrics.NewCollector())
	srv.SetJWTManager(jwtManager)
	srv.SetKeyPair(kp)
	srv.SetTimeSyncService(timeSync)
	srv.SetBillingService(billingSvc)
	if err := srv.InitPeerCredentialVault(jwtSecret); err != nil {
		t.Fatalf("peer credential vault: %v", err)
	}
	srv.InitLDAP()
	srv.InitOIDC()

	cdapGw := cdap.New(cfg, database, peers, bus)
	cdapGw.SetBlocklist(blocklist)
	cdapGw.SetAuditLogger(auditLogger)
	cdapGw.SetJWTManager(jwtManager)
	if err := cdapGw.SetSessionGrantPrivateKey(kp.PrivateKey); err != nil {
		t.Fatalf("session grant key: %v", err)
	}
	srv.SetCDAPGateway(cdapGw)

	meshGw, err := meshcentral.NewGateway(cfg, database, peers, bus, jwtSecret)
	if err != nil {
		t.Fatalf("mesh gateway: %v", err)
	}
	meshGw.SetBlocklist(blocklist)
	meshGw.SetAuditLogger(auditLogger)
	meshGw.SetJWTManager(jwtManager)
	srv.SetMeshGateway(meshGw)

	return &Server{API: srv, DB: database, DBPath: cfg.DBPath, Bus: bus, Peers: peers, Config: cfg}
}
