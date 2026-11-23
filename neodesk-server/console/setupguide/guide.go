// Package setupguide keeps the first-install guide of the console: the
// checklist a new admin finds on the overview after the first sign-in on
// a fresh install.
//
// Who sees it: on a new install (store.DB.NewInstall) Begin opens the
// guide for the first admin account, once. Nobody else gets it by
// itself, and neither does anyone on an install that was set up before,
// so an update never brings it. Anyone may hide it and bring it back.
//
// Each step is done by what the server sees, never by a click: the first
// password replaced, two-factor sign-in on, an address clients can reach,
// HTTPS in front of the console, a computer added, a remote session
// opened, a second account or directory sign-in, a backup made. An
// account gets only the steps it may do itself. What
// happened once (a session, a backup, the password changed) is noted when
// it is first seen, so it stays done when its trace ages out of the logs.
//
// The state lives in the console's settings table:
//
//	setup_guide               {"first_admin": <id>, "started_at": "<ISO time>"}, written once by Begin
//	setup_guide.user.<id>     {"open": true|false, "password": "<SHA-256 of the password hash>", "password_changed": true}
//	setup_guide.seen.session  the time a remote session was first seen
//	setup_guide.seen.backup   the time a backup was first seen
//
// The password field is a fingerprint of the stored hash at the time the
// guide opened, so a changed password shows without keeping the hash.
package setupguide

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
)

// The keys of the guide's rows in the settings table.
const (
	installKey     = "setup_guide"
	userKeyPrefix  = "setup_guide.user."
	seenSessionKey = "setup_guide.seen.session"
	seenBackupKey  = "setup_guide.seen.backup"
)

// The steps, in the order the guide lists them: what blocks a new admin
// first comes first.
const (
	StepPassword  = "password"
	StepTwoFactor = "two_factor"
	StepAddress   = "address"
	StepHTTPS     = "https"
	StepComputer  = "computer"
	StepSession   = "session"
	StepPeople    = "people"
	StepBackup    = "backup"
)

// Options are what a Service reads the server's state from.
type Options struct {
	// DB is the console's database.
	DB *store.DB
	// Address returns the host clients are given when no request says
	// which (serverinfo.Service.ResolveClientFacingHost with no request).
	Address func() string
	// ServesHTTPS reports whether the console listens with TLS itself.
	ServesHTTPS func() bool
	// Devices returns how many devices the server has.
	Devices func(ctx context.Context) (int, error)
	// DirectorySignIn reports whether single sign-on or LDAP is on.
	DirectorySignIn func(ctx context.Context) bool
	// Now is the clock; nil is time.Now.
	Now func() time.Time
}

// Service answers what the guide shows and keeps its state.
type Service struct {
	o Options
}

// New returns a Service. Functions left nil count as "not seen".
func New(o Options) *Service {
	if o.Now == nil {
		o.Now = time.Now
	}
	return &Service{o: o}
}

// Viewer is the account a status is for.
type Viewer struct {
	UserID int64
	// Can reports whether the account holds a permission; the guide lists
	// only the steps it may act on.
	Can func(permission string) bool
	// Secure says the request came over HTTPS.
	Secure bool
	// SuperAdmin says the account's role holds every permission: only
	// such an account may download or schedule a full backup.
	SuperAdmin bool
}

// Step is one line of the checklist.
type Step struct {
	ID   string `json:"id"`
	Done bool   `json:"done"`
}

// Status is what the overview shows: whether the guide is open for the
// account, and its steps.
type Status struct {
	Visible bool   `json:"visible"`
	Steps   []Step `json:"steps"`
}

type installState struct {
	FirstAdmin int64  `json:"first_admin"`
	StartedAt  string `json:"started_at"`
}

type userState struct {
	Open            bool   `json:"open"`
	Password        string `json:"password,omitempty"`
	PasswordChanged bool   `json:"password_changed,omitempty"`
}

// fingerprint stands for a password hash in the guide's state.
func fingerprint(hash string) string {
	sum := sha256.Sum256([]byte(hash))
	return hex.EncodeToString(sum[:])
}

// Begin opens the guide for the first admin of a new install. It runs at
// every start and does something only on the start that set the console
// up, and only once: an install that has the guide's row keeps it.
func (s *Service) Begin(ctx context.Context) error {
	if s.o.DB == nil || !s.o.DB.NewInstall() {
		return nil
	}
	if _, found, err := s.read(ctx, installKey); err != nil || found {
		return err
	}
	id, hash, err := firstAdmin(ctx, s.o.DB)
	if err != nil || id == 0 {
		return err
	}
	install := installState{FirstAdmin: id, StartedAt: store.ISOTime(s.o.Now())}
	if err := s.write(ctx, installKey, install); err != nil {
		return err
	}
	return s.write(ctx, userKey(id), userState{Open: true, Password: fingerprint(hash)})
}

// SetOpen shows the guide to the account or hides it. What the guide
// noted for the account stays.
func (s *Service) SetOpen(ctx context.Context, userID int64, open bool) error {
	state, _, err := s.user(ctx, userID)
	if err != nil {
		return err
	}
	state.Open = open
	return s.write(ctx, userKey(userID), state)
}

// Status works out the guide for an account.
func (s *Service) Status(ctx context.Context, v Viewer) (Status, error) {
	account, err := accountOf(ctx, s.o.DB, v.UserID)
	if err != nil || account == nil {
		return Status{Steps: []Step{}}, err
	}
	state, found, err := s.user(ctx, v.UserID)
	if err != nil {
		return Status{}, err
	}
	can := v.Can
	if can == nil {
		can = func(string) bool { return false }
	}
	steps := make([]Step, 0, 8)
	add := func(id string, done bool) { steps = append(steps, Step{ID: id, Done: done}) }

	if account.local {
		// Only the account the guide opened for knows its first password
		if state.Password != "" {
			changed := state.PasswordChanged || fingerprint(account.hash) != state.Password
			if changed && !state.PasswordChanged {
				state.PasswordChanged = true
				if err := s.write(ctx, userKey(v.UserID), state); err != nil {
					return Status{}, err
				}
			}
			add(StepPassword, changed)
		}
		add(StepTwoFactor, account.twoFactor)
	}
	add(StepAddress, reachableHost(s.address()))
	add(StepHTTPS, v.Secure || (s.o.ServesHTTPS != nil && s.o.ServesHTTPS()))
	if can("enrollment.approve") {
		add(StepComputer, s.devices(ctx) > 0)
	}
	if can("device.connect") {
		done, err := s.seen(ctx, seenSessionKey, s.sessionSeen)
		if err != nil {
			return Status{}, err
		}
		add(StepSession, done)
	}
	if can("user.create") {
		done, err := s.people(ctx, v.UserID)
		if err != nil {
			return Status{}, err
		}
		add(StepPeople, done)
	}
	if v.SuperAdmin {
		done, err := s.seen(ctx, seenBackupKey, s.backupSeen)
		if err != nil {
			return Status{}, err
		}
		add(StepBackup, done)
	}
	return Status{Visible: found && state.Open, Steps: steps}, nil
}

func (s *Service) address() string {
	if s.o.Address == nil {
		return ""
	}
	return s.o.Address()
}

func (s *Service) devices(ctx context.Context) int {
	if s.o.Devices == nil {
		return 0
	}
	n, err := s.o.Devices(ctx)
	if err != nil {
		return 0
	}
	return n
}

// reachableHost reports whether clients elsewhere can reach host: it is
// set, and it is neither this machine's own name or address (localhost,
// a loopback address, 0.0.0.0, a name without a dot such as a container's)
// nor a name only the local network knows (".local").
func reachableHost(host string) bool {
	h := strings.ToLower(strings.TrimSpace(host))
	h = strings.TrimSuffix(strings.TrimPrefix(h, "["), "]")
	if h == "" {
		return false
	}
	if addr, err := netip.ParseAddr(h); err == nil {
		return !addr.IsLoopback() && !addr.IsUnspecified()
	}
	return strings.Contains(h, ".") && !strings.HasSuffix(h, ".local")
}

// seen reports whether something happened once: noted before, or found
// now by look, and then noted.
func (s *Service) seen(ctx context.Context, key string, look func(ctx context.Context) (bool, error)) (bool, error) {
	if _, found, err := s.read(ctx, key); err != nil || found {
		return found, err
	}
	happened, err := look(ctx)
	if err != nil || !happened {
		return false, err
	}
	return true, s.write(ctx, key, store.ISOTime(s.o.Now()))
}

// sessionSeen looks for a remote session: one the signal server saw
// (peer_connection_sessions, browser sessions included) or one a client
// reported (audit_connections).
func (s *Service) sessionSeen(ctx context.Context) (bool, error) {
	for _, table := range []string{"peer_connection_sessions", "audit_connections"} {
		if !s.o.DB.Shape().Table(table).Exists() {
			continue
		}
		if found, err := exists(ctx, s.o.DB, `SELECT 1 FROM `+table+` LIMIT 1`); err != nil || found {
			return found, err
		}
	}
	return false, nil
}

// backupSeen looks for a backup: a download or a run made by hand (the
// audit log's backup_created), or a scheduled run that succeeded.
func (s *Service) backupSeen(ctx context.Context) (bool, error) {
	found, err := exists(ctx, s.o.DB, `SELECT 1 FROM audit_log WHERE action = 'backup_created' LIMIT 1`)
	if err != nil || found {
		return found, err
	}
	raw, ok, err := readText(ctx, s.o.DB, "scheduled_backup_status")
	if err != nil || !ok {
		return false, err
	}
	var status struct {
		LastSuccessAt any `json:"lastSuccessAt"`
	}
	if json.Unmarshal([]byte(raw), &status) != nil {
		return false, nil
	}
	text, _ := status.LastSuccessAt.(string)
	return text != "", nil
}

// people reports whether the account is not alone: another account that
// may sign in, or single sign-on or LDAP that lets people in.
func (s *Service) people(ctx context.Context, self int64) (bool, error) {
	rows, err := store.Select(ctx, s.o.DB, `SELECT id, disabled FROM users`)
	if err != nil {
		return false, fmt.Errorf("read the accounts: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id store.Int64String
		var disabled store.Bool
		if err := rows.Scan(&id, &disabled); err != nil {
			return false, fmt.Errorf("read the accounts: %w", err)
		}
		if id.V != self && !disabled.True() {
			return true, nil
		}
	}
	if err := rows.Err(); err != nil {
		return false, fmt.Errorf("read the accounts: %w", err)
	}
	return s.o.DirectorySignIn != nil && s.o.DirectorySignIn(ctx), nil
}

func userKey(id int64) string { return fmt.Sprintf("%s%d", userKeyPrefix, id) }

// user reads the account's state; found is false when the guide never
// opened for it.
func (s *Service) user(ctx context.Context, id int64) (userState, bool, error) {
	var state userState
	raw, found, err := s.read(ctx, userKey(id))
	if err != nil || !found {
		return state, found, err
	}
	if err := json.Unmarshal([]byte(raw), &state); err != nil {
		return userState{}, true, nil
	}
	return state, true, nil
}

func (s *Service) read(ctx context.Context, key string) (string, bool, error) {
	return readText(ctx, s.o.DB, key)
}

func (s *Service) write(ctx context.Context, key string, value any) error {
	text, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("store %s: %w", key, err)
	}
	now := store.NowSQL(s.o.DB.Dialect())
	_, err = s.o.DB.Exec(ctx, `INSERT INTO settings (key, value, updated_at) VALUES (?, ?, `+now+`)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = `+now, key, string(text))
	if err != nil {
		return fmt.Errorf("store %s: %w", key, err)
	}
	return nil
}

func readText(ctx context.Context, db *store.DB, key string) (string, bool, error) {
	var value sql.NullString
	err := db.QueryRow(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read %s: %w", key, err)
	}
	return value.String, true, nil
}

func exists(ctx context.Context, db *store.DB, query string, args ...any) (bool, error) {
	var one int
	err := db.QueryRow(ctx, query, args...).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

type account struct {
	hash      string
	local     bool
	twoFactor bool
}

func accountOf(ctx context.Context, db *store.DB, id int64) (*account, error) {
	var hash, provider sql.NullString
	var twoFactor store.Bool
	err := store.SelectRow(ctx, db, `SELECT password_hash, auth_provider, totp_enabled FROM users WHERE id = ?`, id).
		Scan(&hash, &provider, &twoFactor)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read account %d: %w", id, err)
	}
	p := strings.ToLower(strings.TrimSpace(provider.String))
	return &account{hash: hash.String, local: p == "" || p == "local", twoFactor: twoFactor.True()}, nil
}

// firstAdmin is the account a new install made first: the lowest id with
// an admin role that may sign in, and its password hash.
func firstAdmin(ctx context.Context, db *store.DB) (int64, string, error) {
	rows, err := store.Select(ctx, db, `SELECT id, role, disabled, password_hash FROM users ORDER BY id`)
	if err != nil {
		return 0, "", fmt.Errorf("find the first admin: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id store.Int64String
		var role, hash sql.NullString
		var disabled store.Bool
		if err := rows.Scan(&id, &role, &disabled, &hash); err != nil {
			return 0, "", fmt.Errorf("find the first admin: %w", err)
		}
		if r := role.String; (r == "admin" || r == "super_admin") && !disabled.True() {
			return id.V, hash.String, nil
		}
	}
	return 0, "", rows.Err()
}
