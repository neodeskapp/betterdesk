package backup

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/unitronix/betterdesk-server/console/branding"
	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/httpx"
	"github.com/unitronix/betterdesk-server/console/store"
)

// The format names and versions an archive and a settings snapshot carry.
const (
	FormatName        = "betterdesk-backup"
	FormatVersion     = 1
	FullFormatName    = "betterdesk-full-backup"
	FullFormatVersion = 1
)

// DefaultMaxArchiveBytes is the largest unpacked archive Restore and the
// checks accept: 2 GiB.
const DefaultMaxArchiveBytes = 2 << 30

// Branding is the part of the branding service a backup reads and writes.
type Branding interface {
	Refresh(ctx context.Context)
	GetBranding() *branding.Object
	SaveBranding(ctx context.Context, updates any) error
}

// Groups lists folders and groups as the console's device group service
// does.
type Groups interface {
	AllFolders(ctx context.Context) ([]*goapi.Object, error)
	AllUserGroups(ctx context.Context) ([]*goapi.Object, error)
	AllDeviceGroups(ctx context.Context) ([]*goapi.Object, error)
}

// GoServer is what a backup asks the API server for.
type GoServer interface {
	GetAllPeers(ctx context.Context, includeDeleted bool) []*goapi.Object
	GetBlocklist(ctx context.Context) *goapi.Object
	GetAuditEvents(ctx context.Context, limit any) *goapi.Object
	GetHealth(ctx context.Context) *goapi.Object
}

// Options are the settings of a Service. The paths are those of the old
// console's config.js; the lead's wiring fills them from config.Config.
type Options struct {
	Store    *store.DB
	Branding Branding
	Groups   Groups
	GoAPI    GoServer

	// DataDir is DATA_DIR: .session_secret and uploads/ are below it.
	DataDir string
	// EnvPath is the old console's .env (web-nodejs/.env).
	EnvPath string
	// KeysPath is KEYS_PATH (RUSTDESK_DIR, RUSTDESK_PATH): id_ed25519 and
	// mesh_agent_server.pem are in it.
	KeysPath string
	// PubKeyPath is PUB_KEY_PATH (default KeysPath/id_ed25519.pub),
	// APIKeyPath is API_KEY_PATH (default KeysPath/.api_key).
	PubKeyPath string
	APIKeyPath string
	// DBPath is DB_PATH (default KeysPath/db_v2.sqlite3): the SQLite file
	// a full backup copies twice, as the console's and as the server's
	// database. Ignored on PostgreSQL for the console's copy.
	DBPath string
	// AppVersion is the product version the archive names.
	AppVersion string
	// Now is the clock; nil is time.Now.
	Now func() time.Time
	// MaxArchiveBytes caps the unpacked size of an archive that is read;
	// 0 is DefaultMaxArchiveBytes.
	MaxArchiveBytes int
}

// Service makes and restores backups (services/backupService.js).
type Service struct {
	o Options
}

// New returns a Service.
func New(o Options) *Service {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.MaxArchiveBytes <= 0 {
		o.MaxArchiveBytes = DefaultMaxArchiveBytes
	}
	return &Service{o: o}
}

// paths are the files a full backup holds, resolveBackupPaths.
type paths struct {
	env, sessionSecret, uploadsDir, consoleDB, goPrivKey, goPubKey, goAPIKey, goDB, meshAgentCert string
}

func (s *Service) paths() paths {
	p := paths{
		env:           s.o.EnvPath,
		sessionSecret: filepath.Join(s.o.DataDir, ".session_secret"),
		uploadsDir:    filepath.Join(s.o.DataDir, "uploads"),
		goPrivKey:     filepath.Join(s.o.KeysPath, "id_ed25519"),
		goPubKey:      s.o.PubKeyPath,
		goAPIKey:      s.o.APIKeyPath,
		goDB:          s.o.DBPath,
		meshAgentCert: filepath.Join(s.o.KeysPath, "mesh_agent_server.pem"),
	}
	if !s.o.Store.IsPostgres() {
		p.consoleDB = s.o.DBPath
	}
	return p
}

// dbType is config.dbType.
func (s *Service) dbType() string { return s.o.Store.Dialect().String() }

// isoNow is new Date().toISOString().
func (s *Service) isoNow() string { return store.ISOTime(s.o.Now()) }

// ---- Statistics ----

// Stats are the row counts getBackupStats gave.
type Stats struct {
	Users        int64 `json:"users"`
	Settings     int64 `json:"settings"`
	Folders      int64 `json:"folders"`
	UserGroups   int64 `json:"userGroups"`
	DeviceGroups int64 `json:"deviceGroups"`
	Strategies   int64 `json:"strategies"`
	AddressBooks int64 `json:"addressBooks"`
}

// FullStats is the answer of getFullBackupStats: the counts, the backend,
// the database and which parts a full backup would find.
type FullStats struct {
	Stats
	Backend    string     `json:"backend"`
	DBType     string     `json:"dbType"`
	Components Components `json:"components"`
}

// Components says which files exist, and how many uploads there are.
type Components struct {
	Env           bool `json:"env"`
	SessionSecret bool `json:"sessionSecret"`
	ConsoleDB     bool `json:"consoleDb"`
	Uploads       int  `json:"uploads"`
	GoPrivKey     bool `json:"goPrivKey"`
	GoPubKey      bool `json:"goPubKey"`
	GoAPIKey      bool `json:"goApiKey"`
	GoDB          bool `json:"goDb"`
	MeshAgentCert bool `json:"meshAgentCert"`
}

// BackupStats is getBackupStats: the counts and the backend.
func (s *Service) BackupStats(ctx context.Context) (*goapi.Object, error) {
	st := backupStats(ctx, s.o.Store)
	raw, err := json.Marshal(st)
	if err != nil {
		return nil, err
	}
	v, err := goapi.Parse(raw)
	if err != nil {
		return nil, err
	}
	o, _ := v.(*goapi.Object)
	return o.Set("backend", "betterdesk"), nil
}

// FullBackupStats is getFullBackupStats.
func (s *Service) FullBackupStats(ctx context.Context) FullStats {
	p := s.paths()
	exists := func(path string) bool {
		if path == "" {
			return false
		}
		_, err := os.Stat(path)
		return err == nil
	}
	uploads := 0
	if exists(p.uploadsDir) {
		_ = walkFiles(p.uploadsDir, "", func(string, string) { uploads++ })
	}
	return FullStats{
		Stats:   backupStats(ctx, s.o.Store),
		Backend: "betterdesk",
		DBType:  s.dbType(),
		Components: Components{
			Env:           exists(p.env),
			SessionSecret: exists(p.sessionSecret),
			ConsoleDB:     exists(p.consoleDB),
			Uploads:       uploads,
			GoPrivKey:     exists(p.goPrivKey),
			GoPubKey:      exists(p.goPubKey),
			GoAPIKey:      exists(p.goAPIKey),
			GoDB:          exists(p.goDB),
			MeshAgentCert: exists(p.meshAgentCert),
		},
	}
}

// walkFiles visits the files below dir (following links), names sorted
// per folder, with each file's path relative to dir. It stops at the
// first entry that cannot be read, as the old walk did, after visiting
// what came before. A name that is not UTF-8 counts as unreadable: Node
// read it as other text, which then named no file.
func walkFiles(dir, rel string, visit func(rel, abs string)) error {
	abs := filepath.Join(dir, rel)
	entries, err := os.ReadDir(abs)
	if err != nil {
		return err
	}
	for _, e := range entries {
		name := e.Name()
		if nodeUTF8([]byte(name)) != name {
			return fmt.Errorf("unreadable name in %s", abs)
		}
		childRel := name
		if rel != "" {
			childRel = filepath.Join(rel, name)
		}
		childAbs := filepath.Join(dir, childRel)
		info, err := os.Stat(childAbs)
		if err != nil {
			return err
		}
		switch {
		case info.IsDir():
			if err := walkFiles(dir, childRel, visit); err != nil {
				return err
			}
		case info.Mode().IsRegular():
			visit(childRel, childAbs)
		}
	}
	return nil
}

// ---- Full backup ----

// Manifest is manifest.json of an archive.
type Manifest struct {
	Format         string   `json:"_format"`
	Version        int      `json:"_version"`
	Created        string   `json:"_created"`
	ConsoleVersion string   `json:"_console_version"`
	Backend        string   `json:"_backend"`
	DBType         string   `json:"dbType"`
	Components     []string `json:"components"`
}

// CreateFullBackup makes the full disaster-recovery archive and its file
// name, betterdesk-backup-YYYY-MM-DD-HH-MM-SS.tar.gz (UTC). It holds, in
// this order:
//
//	manifest.json                    format, version, time, product version, backend, database, components
//	console/data.json                the logical dump of every table (DumpAllTables)
//	goserver/data.json               devices, blocklist, the last 500 audit events and the health of the API server
//	console/.env                     the old console's .env
//	console/.session_secret          DATA_DIR/.session_secret
//	console/auth.db                  the SQLite file (DB_PATH), on SQLite only
//	console/uploads/...              every file below DATA_DIR/uploads
//	goserver/id_ed25519(.pub)        the server's key pair
//	goserver/.api_key                the API key
//	goserver/db_v2.sqlite3           DB_PATH again, as the server's database
//	goserver/mesh_agent_server.pem   the mesh agent certificate
//	README.txt                       how to restore by hand
//
// A part whose file is missing or unreadable is left out; the dump is
// left out when it fails. Files are mode 0600 in the archive, uploads and
// the rest 0644.
func (s *Service) CreateFullBackup(ctx context.Context) ([]byte, string, error) {
	timestamp := s.isoNow()
	p := s.paths()
	var entries []Entry
	var present []string

	if dump, err := DumpAllTables(ctx, s.o.Store); err == nil {
		entries = append(entries, Entry{Name: "console/data.json", Data: dump})
		present = append(present, "console/data.json")
	}
	if goServer := s.goServerData(ctx); goServer != nil {
		raw, err := httpx.Marshal(goServer)
		if err != nil {
			return nil, "", err
		}
		entries = append(entries, Entry{Name: "goserver/data.json", Data: raw})
		present = append(present, "goserver/data.json")
	}

	addFile := func(name, path string) {
		if path == "" {
			return
		}
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			return
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return
		}
		entries = append(entries, Entry{Name: name, Data: data, Mode: 0o600})
		present = append(present, name)
	}
	addFile("console/.env", p.env)
	addFile("console/.session_secret", p.sessionSecret)
	addFile("console/auth.db", p.consoleDB)
	if info, err := os.Stat(p.uploadsDir); err == nil && info.IsDir() {
		_ = walkFiles(p.uploadsDir, "", func(rel, abs string) {
			data, err := os.ReadFile(abs)
			if err != nil {
				return
			}
			name := "console/uploads/" + filepath.ToSlash(rel)
			entries = append(entries, Entry{Name: name, Data: data, Mode: 0o644})
			present = append(present, name)
		})
	}
	addFile("goserver/id_ed25519", p.goPrivKey)
	addFile("goserver/id_ed25519.pub", p.goPubKey)
	addFile("goserver/.api_key", p.goAPIKey)
	addFile("goserver/db_v2.sqlite3", p.goDB)
	addFile("goserver/mesh_agent_server.pem", p.meshAgentCert)

	if present == nil {
		present = []string{}
	}
	manifest := Manifest{
		Format:         FullFormatName,
		Version:        FullFormatVersion,
		Created:        timestamp,
		ConsoleVersion: s.o.AppVersion,
		Backend:        "betterdesk",
		DBType:         s.dbType(),
		Components:     present,
	}
	manifestJSON, err := indentJSON(manifest)
	if err != nil {
		return nil, "", err
	}
	entries = append([]Entry{{Name: "manifest.json", Data: manifestJSON}}, entries...)
	entries = append(entries, Entry{Name: "README.txt", Data: []byte(recoveryReadme(manifest))})

	archive, err := CreateTarGz(entries, s.o.Now().Unix())
	if err != nil {
		return nil, "", err
	}
	stamp := strings.NewReplacer(":", "-", "T", "-").Replace(timestamp[:19])
	return archive, "betterdesk-backup-" + stamp + ".tar.gz", nil
}

// goServerData is fetchGoServerData: what the API server says, each call
// falling back on its own.
func (s *Service) goServerData(ctx context.Context) *goapi.Object {
	if s.o.GoAPI == nil {
		return nil
	}
	peers := s.o.GoAPI.GetAllPeers(ctx, false)
	if peers == nil {
		peers = []*goapi.Object{}
	}
	return goapi.NewObject(
		"peers", peers,
		"blocklist", s.o.GoAPI.GetBlocklist(ctx),
		"auditEvents", s.o.GoAPI.GetAuditEvents(ctx, float64(500)),
		"serverHealth", s.o.GoAPI.GetHealth(ctx),
	)
}

// indentJSON is JSON.stringify(v, null, 2).
func indentJSON(v any) ([]byte, error) {
	raw, err := httpx.Marshal(v)
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	if err := json.Indent(&out, raw, "", "  "); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func recoveryReadme(m Manifest) string {
	return strings.Join([]string{
		"BetterDesk Console \u2014 Full Disaster-Recovery Backup",
		"==================================================",
		"",
		"Created:          " + m.Created,
		"Console version:  " + m.ConsoleVersion,
		"Database engine:  " + m.DBType,
		"Components:        " + strings.Join(m.Components, ", "),
		"",
		"WARNING: This archive contains SECRETS (Ed25519 server keys, .env with",
		"SESSION_SECRET / API key / database credentials, user password hashes and",
		"2FA secrets). Store it securely and never share it.",
		"",
		"How to restore on a fresh machine",
		"---------------------------------",
		"1. Install BetterDesk using the ALL-IN-ONE installer (betterdesk.sh / .ps1).",
		"2. Stop the console and Go server services.",
		"3. Open the web console Settings page and use \"Restore from backup\",",
		"   OR extract this archive manually with: tar -xzf <backup>.tar.gz",
		"   and copy each component back to its location:",
		"     console/.env            -> web-nodejs/.env",
		"     console/.session_secret -> <dataDir>/.session_secret",
		"     console/auth.db         -> <dataDir>/auth.db   (SQLite backend only)",
		"     console/uploads/*       -> <dataDir>/uploads/",
		"     goserver/id_ed25519     -> <keysPath>/id_ed25519",
		"     goserver/id_ed25519.pub -> <keysPath>/id_ed25519.pub",
		"     goserver/.api_key       -> <keysPath>/.api_key",
		"     goserver/mesh_agent_server.pem -> <keysPath>/mesh_agent_server.pem",
		"     goserver/db_v2.sqlite3  -> Go server DB path (SQLite backend only)",
		"4. For PostgreSQL backends, the logical dump in console/data.json is",
		"   re-imported through the console Restore action.",
		"5. Restart both services. Clients keep trusting the server because the",
		"   Ed25519 identity key was preserved.",
		"",
	}, "\n")
}

// Validation is validateFullBackup's answer.
type Validation struct {
	Valid    bool
	Errors   []string
	Manifest any
	Files    *Files
}

// ValidateFullBackup unpacks an archive and checks its manifest: it must
// be JSON with _format "betterdesk-full-backup" and a numeric _version of
// at most 1. The error is a JavaScript TypeError the old check threw
// instead of answering (a manifest that is null, a version that cannot be
// turned into text).
func (s *Service) ValidateFullBackup(buf []byte) (Validation, error) {
	files, err := ExtractTarGz(buf, s.o.MaxArchiveBytes)
	if err != nil {
		return Validation{Errors: []string{"Not a valid gzip/tar archive: " + err.Error()}}, nil
	}
	raw, ok := files.Get("manifest.json")
	if !ok {
		return Validation{Errors: []string{"Archive is missing manifest.json"}}, nil
	}
	manifest, err := branding.ParseJSON(raw)
	if err != nil {
		return Validation{Errors: []string{"manifest.json is not valid JSON: " + errorText(err)}}, nil
	}
	var errs []string
	format, err := readProp(manifest, "_format")
	if err != nil {
		return Validation{}, err
	}
	if format != FullFormatName {
		errs = append(errs, "Archive is not a BetterDesk full backup (wrong _format)")
	}
	version := prop(manifest, "_version")
	if n, isNumber := version.(float64); !isNumber || n > FullFormatVersion {
		text, err := jsString(version)
		if err != nil {
			return Validation{}, err
		}
		errs = append(errs, "Unsupported full-backup version: "+text)
	}
	return Validation{Valid: len(errs) == 0, Errors: errs, Manifest: manifest, Files: files}, nil
}

// RestoreOptions say which parts of an archive Restore puts back.
type RestoreOptions struct {
	Database bool // the logical dump; the route's default is on
	Uploads  bool // the uploaded pictures; default on
	Secrets  bool // session secret, server keys, API key, mesh certificate; default off
	Env      bool // the .env; default off
	GoDB     bool // the SQLite file of the server; default off
}

// RestoreResult is what Restore did.
type RestoreResult struct {
	Restored        []string
	Skipped         []string
	Warnings        []string
	RequiresRestart bool
}

// ErrInvalidArchive is the error of a restore that the check refused
// before anything was written.
type ErrInvalidArchive struct{ Errors []string }

func (e *ErrInvalidArchive) Error() string {
	return "Invalid backup archive: " + strings.Join(e.Errors, "; ")
}

// RestoreFullBackup puts an archive back (restoreFullBackup). The whole
// archive is unpacked and checked first; an archive that fails the check
// changes nothing. Then, in this order and each part on its own, so that
// a part that fails leaves a warning and the next one still runs:
//
//  1. the logical dump, table by table (ImportAllTables),
//  2. the uploads, below DATA_DIR/uploads,
//  3. the secrets, each to its own path,
//  4. the .env,
//  5. the server's SQLite file.
//
// Parts 3 to 5 take effect at the next start. A file is written over the
// old one in place; a new file gets the mode of its part.
func (s *Service) RestoreFullBackup(ctx context.Context, buf []byte, o RestoreOptions) (RestoreResult, error) {
	res := RestoreResult{Restored: []string{}, Skipped: []string{}, Warnings: []string{}}
	v, err := s.ValidateFullBackup(buf)
	if err != nil {
		return res, err
	}
	if !v.Valid {
		return res, &ErrInvalidArchive{Errors: v.Errors}
	}
	files := v.Files
	p := s.paths()

	if dumpRaw, ok := files.Get("console/data.json"); o.Database && ok {
		if err := s.importDump(ctx, dumpRaw, &res); err != nil {
			res.Warnings = append(res.Warnings, "Database restore failed: "+driverMessage(err))
		}
	} else {
		res.Skipped = append(res.Skipped, "database")
	}

	if o.Uploads {
		count, err := s.restoreUploads(files, p.uploadsDir)
		switch {
		case err != nil:
			res.Warnings = append(res.Warnings, "Uploads restore failed: "+err.Error())
		case count > 0:
			res.Restored = append(res.Restored, fmt.Sprintf("uploads (%d files)", count))
		default:
			res.Skipped = append(res.Skipped, "uploads")
		}
	} else {
		res.Skipped = append(res.Skipped, "uploads")
	}

	if o.Secrets {
		restoredAny := false
		for _, f := range []struct {
			name, target string
			mode         os.FileMode
		}{
			{"console/.session_secret", p.sessionSecret, 0o600},
			{"goserver/id_ed25519", p.goPrivKey, 0o600},
			{"goserver/id_ed25519.pub", p.goPubKey, 0o644},
			{"goserver/.api_key", p.goAPIKey, 0o600},
			{"goserver/mesh_agent_server.pem", p.meshAgentCert, 0o600},
		} {
			data, ok := files.Get(f.name)
			if !ok || f.target == "" {
				continue
			}
			if err := writeFile(f.target, data, f.mode); err != nil {
				res.Warnings = append(res.Warnings, f.name+" restore failed: "+err.Error())
				continue
			}
			restoredAny = true
		}
		if restoredAny {
			res.Restored = append(res.Restored, "secrets/keys")
			res.RequiresRestart = true
		} else {
			res.Skipped = append(res.Skipped, "secrets/keys")
		}
	} else {
		res.Skipped = append(res.Skipped, "secrets/keys")
	}

	if data, ok := files.Get("console/.env"); o.Env && ok {
		if err := writeFile(p.env, data, 0o600); err != nil {
			res.Warnings = append(res.Warnings, ".env restore failed: "+err.Error())
		} else {
			res.Restored = append(res.Restored, ".env")
			res.RequiresRestart = true
		}
	} else {
		res.Skipped = append(res.Skipped, ".env")
	}

	if data, ok := files.Get("goserver/db_v2.sqlite3"); o.GoDB && ok && p.goDB != "" {
		if err := writeFile(p.goDB, data, 0o600); err != nil {
			res.Warnings = append(res.Warnings, "Go server DB restore failed: "+err.Error())
		} else {
			res.Restored = append(res.Restored, "goserver-db")
			res.RequiresRestart = true
		}
	} else {
		res.Skipped = append(res.Skipped, "goserver-db")
	}
	return res, nil
}

func (s *Service) importDump(ctx context.Context, raw []byte, res *RestoreResult) error {
	dump, err := branding.ParseJSON(raw)
	if err != nil {
		return err
	}
	r, err := ImportAllTables(ctx, s.o.Store, dump)
	if err != nil {
		return err
	}
	res.Restored = append(res.Restored, fmt.Sprintf("database (%d tables)", len(r.Restored)))
	res.Warnings = append(res.Warnings, r.Warnings...)
	return nil
}

// restoreUploads writes the archive's console/uploads/ files below dir.
// An entry whose name is empty there, or holds "..", is skipped, as
// before; so is one that safepath refuses (an absolute name, a "." part,
// a link that leads out of the folder). A failed write ends the part.
func (s *Service) restoreUploads(files *Files, dir string) (int, error) {
	count := 0
	for _, name := range files.Names() {
		rel, ok := strings.CutPrefix(name, "console/uploads/")
		if !ok || rel == "" || strings.Contains(rel, "..") {
			continue
		}
		if !uploadNameAllowed(dir, rel) {
			continue
		}
		data, _ := files.Get(name)
		if err := writeFile(filepath.Join(dir, rel), data, 0o644); err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}

// writeFile is the old console's writeFile: the folder is made when
// missing, the file is replaced in place, and mode is used for a new file.
func writeFile(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o777); err != nil {
		return NodeFSError(err, "mkdir", dir)
	}
	if err := os.WriteFile(path, data, mode); err != nil {
		return NodeFSError(err, "open", path)
	}
	return nil
}
