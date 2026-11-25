// Package admintoken keeps the console's API tokens: named tokens a
// signed-in admin makes for scripts, the command line (neodesk) and the
// MCP server, so they can use the console's API without a browser.
//
// # What a token is
//
// A token is "ndt_", a public id of 12 lower-case letters and digits, "_"
// and a secret of 40 letters and digits:
//
//	ndt_<id>_<secret>
//
// The prefix lets secret scanners find tokens in code and logs
// (.github/gitleaks.toml has the rule). The console stores the SHA-256 of the
// whole token, never the token: it is shown once, when it is made.
//
// A token is sent only as "Authorization: Bearer <token>" to the console's
// port. A token in the address, a cookie or a form field is never read,
// and the API server's port (21121) does not take one.
//
// # What a token may do
//
// A token acts as the account that made it, with less:
//
//   - its permissions are a set chosen at creation, each one the creator
//     held then; at every use a permission counts only while the creator
//     still holds it (the role's permissions with overrides, as for a
//     browser), so a creator who loses a permission, is disabled or is
//     deleted takes it from the token at once;
//   - the panel sections follow the same set: a section's write level
//     needs its write permission in the token, its read level its read
//     permission, besides the creator's own level there;
//   - a read-only token makes GET and HEAD requests only;
//   - checks by role (admin pages, the role permission editor) and every
//     change to accounts, roles, panel access and sign-in settings need a
//     token that carries every permission its creator holds;
//   - some routes are never open to a token: making tokens, the
//     account's own password, second factor, sessions and profile, and
//     showing the console's API key in full;
//   - an optional allow list of addresses and networks;
//   - an expiry of at most a year, and revocation;
//   - what it made that outlives a request (guest links, device tokens,
//     support sessions, codes, share links, organization invitations)
//     ends with it: at a revoke, an expiry or the deletion of its row
//     (ends.go).
//
// The routes, the pipeline and the access checks in console, console/kit
// and console/access apply these rules; this package keeps the rows,
// makes and checks tokens and counts failures for the rate limit.
package admintoken

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/netip"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
)

// Prefix starts every token.
const Prefix = "ndt_"

const (
	idLength     = 12
	secretLength = 40
	// tokenLength is the length of a whole token.
	tokenLength = len(Prefix) + idLength + 1 + secretLength
	// idAlphabet leaves out letters and digits that look alike.
	idAlphabet     = "abcdefghijkmnpqrstuvwxyz23456789"
	secretAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
)

// ServerConfig is the permission a token gets only together with all of
// its creator's (Create; the pipeline drops it at use from a token that
// is not full).
const ServerConfig = "server.config"

// Limits of what a token may hold.
const (
	MaxNameLength = 80
	MaxLifetime   = 366 * 24 * time.Hour
	MaxAllowList  = 32
	MaxPerAccount = 50
)

// Token is a stored token without its secret.
type Token struct {
	// ID is the public id, the 12 characters after "ndt_".
	ID          string
	Name        string
	UserID      int64
	Username    string
	Permissions []string
	ReadOnly    bool
	AllowList   []string
	CreatedAt   time.Time
	ExpiresAt   time.Time
	LastUsedAt  time.Time
	LastUsedIP  string
	RevokedAt   time.Time
	RevokedBy   string
}

// Hint is how lists show the token: "ndt_<id>_…".
func (t *Token) Hint() string { return Prefix + t.ID + "_…" }

// Has reports whether the token was given the permission.
func (t *Token) Has(permission string) bool {
	for _, p := range t.Permissions {
		if p == permission {
			return true
		}
	}
	return false
}

// State is "active", "expired" or "revoked" at the time now.
func (t *Token) State(now time.Time) string {
	switch {
	case !t.RevokedAt.IsZero():
		return "revoked"
	case !now.Before(t.ExpiresAt):
		return "expired"
	}
	return "active"
}

// Options configure a Service.
type Options struct {
	DB *store.DB
	// Now is the clock; nil means time.Now.
	Now func() time.Time
	// FailureLimit is how many failed attempts one address may make in
	// FailureWindow before every token request from it is refused until
	// the window ends. Zero means 20 in five minutes.
	FailureLimit  int
	FailureWindow time.Duration
	// RequestLimit is how many requests one token may make per minute.
	// Zero means 1200.
	RequestLimit int
}

// Service keeps the tokens. It is safe for concurrent use.
type Service struct {
	db  *store.DB
	now func() time.Time

	failures *window
	requests *window

	mu      sync.Mutex
	touched map[string]time.Time
	// onEnd are called after a token's end is committed (OnEnd)
	onEnd []func(context.Context, *Ending)
	// wake tells Run that a token was made, whose expiry may come first
	wake chan struct{}
}

// New returns a Service and creates its table when it is missing.
func New(ctx context.Context, opts Options) (*Service, error) {
	if opts.DB == nil {
		return nil, errors.New("admintoken: no database")
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	if opts.FailureLimit <= 0 {
		opts.FailureLimit = 20
	}
	if opts.FailureWindow <= 0 {
		opts.FailureWindow = 5 * time.Minute
	}
	if opts.RequestLimit <= 0 {
		opts.RequestLimit = 1200
	}
	s := &Service{
		db:       opts.DB,
		now:      now,
		failures: newWindow(opts.FailureLimit, opts.FailureWindow, now),
		requests: newWindow(opts.RequestLimit, time.Minute, now),
		touched:  map[string]time.Time{},
		wake:     make(chan struct{}, 1),
	}
	if err := s.ensureSchema(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Service) ensureSchema(ctx context.Context) error {
	create := s.db.Pick(
		`CREATE TABLE IF NOT EXISTS admin_tokens (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			public_id TEXT NOT NULL UNIQUE,
			token_hash TEXT NOT NULL,
			name TEXT NOT NULL,
			user_id INTEGER NOT NULL,
			permissions TEXT NOT NULL DEFAULT '[]',
			read_only INTEGER NOT NULL DEFAULT 1,
			allow_list TEXT NOT NULL DEFAULT '[]',
			created_at INTEGER NOT NULL,
			expires_at INTEGER NOT NULL,
			last_used_at INTEGER,
			last_used_ip TEXT,
			revoked_at INTEGER,
			revoked_by TEXT,
			ended_at INTEGER
		)`,
		`CREATE TABLE IF NOT EXISTS admin_tokens (
			id BIGSERIAL PRIMARY KEY,
			public_id TEXT NOT NULL UNIQUE,
			token_hash TEXT NOT NULL,
			name TEXT NOT NULL,
			user_id BIGINT NOT NULL,
			permissions TEXT NOT NULL DEFAULT '[]',
			read_only BOOLEAN NOT NULL DEFAULT TRUE,
			allow_list TEXT NOT NULL DEFAULT '[]',
			created_at BIGINT NOT NULL,
			expires_at BIGINT NOT NULL,
			last_used_at BIGINT,
			last_used_ip TEXT,
			revoked_at BIGINT,
			revoked_by TEXT,
			ended_at BIGINT
		)`)
	if _, err := s.db.Exec(ctx, create); err != nil {
		return fmt.Errorf("create admin_tokens: %w", err)
	}
	if _, err := s.db.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_admin_tokens_user ON admin_tokens (user_id)`); err != nil {
		return fmt.Errorf("index admin_tokens: %w", err)
	}
	// ended_at: when what the token made was ended (ends.go). A table
	// from before it gets the column with the tokens that had ended
	// marked: what they made was made before tokens were recorded on it.
	has, err := hasColumn(ctx, s.db, "admin_tokens", "ended_at")
	if err != nil {
		return fmt.Errorf("read admin_tokens: %w", err)
	}
	if !has {
		return s.db.InTx(ctx, func(tx *store.Tx) error {
			if _, err := tx.Exec(ctx, tx.Pick(`ALTER TABLE admin_tokens ADD COLUMN ended_at INTEGER`, `ALTER TABLE admin_tokens ADD COLUMN IF NOT EXISTS ended_at BIGINT`)); err != nil {
				return fmt.Errorf("add admin_tokens.ended_at: %w", err)
			}
			now := s.now().UnixMilli()
			_, err := tx.Exec(ctx, `UPDATE admin_tokens SET ended_at = ? WHERE ended_at IS NULL AND (revoked_at IS NOT NULL OR expires_at <= ?)`, now, now)
			return err
		})
	}
	return nil
}

// Creator is the account that makes a token, as it is at that moment.
type Creator struct {
	ID       int64
	Username string
	// Holds reports whether the account has a permission now.
	Holds func(permission string) bool
}

// Input is what a new token is made of.
type Input struct {
	Name        string
	Permissions []string
	ReadOnly    bool
	ExpiresAt   time.Time
	AllowList   []string
}

// InputError is a field of Input that cannot be used, with the reason in
// plain words.
type InputError struct {
	Field   string
	Message string
}

func (e *InputError) Error() string { return e.Field + ": " + e.Message }

func inputError(field, format string, args ...any) error {
	return &InputError{Field: field, Message: fmt.Sprintf(format, args...)}
}

// Create makes a token for the creator and returns it with its whole
// value, which is never shown again. known lists every permission a token
// can be given.
func (s *Service) Create(ctx context.Context, creator Creator, in Input, known []string) (string, *Token, error) {
	if creator.ID == 0 || creator.Holds == nil {
		return "", nil, errors.New("admintoken: no creator")
	}
	name, err := cleanName(in.Name)
	if err != nil {
		return "", nil, err
	}
	permissions, err := cleanPermissions(in.Permissions, known, creator.Holds)
	if err != nil {
		return "", nil, err
	}
	now := s.now()
	expires := in.ExpiresAt
	switch {
	case expires.IsZero():
		return "", nil, inputError("expires_at", "Choose when the token expires.")
	case !expires.After(now):
		return "", nil, inputError("expires_at", "The expiry must be in the future.")
	case expires.Sub(now) > MaxLifetime:
		return "", nil, inputError("expires_at", "A token can last at most a year.")
	}
	allow, err := CleanAllowList(in.AllowList)
	if err != nil {
		return "", nil, err
	}
	var count int64
	if err := s.db.QueryRow(ctx, `SELECT COUNT(*) FROM admin_tokens WHERE user_id = ? AND revoked_at IS NULL AND expires_at > ?`,
		creator.ID, now.UnixMilli()).Scan(&count); err != nil {
		return "", nil, fmt.Errorf("count tokens: %w", err)
	}
	if count >= MaxPerAccount {
		return "", nil, inputError("name", "An account can have at most %d active tokens. Revoke one first.", MaxPerAccount)
	}

	var id, secret string
	for attempt := 0; ; attempt++ {
		if id, err = randomText(idAlphabet, idLength); err != nil {
			return "", nil, err
		}
		if secret, err = randomText(secretAlphabet, secretLength); err != nil {
			return "", nil, err
		}
		value := Prefix + id + "_" + secret
		perms, _ := json.Marshal(permissions)
		allowJSON, _ := json.Marshal(allow)
		_, err = s.db.Exec(ctx, `INSERT INTO admin_tokens (public_id, token_hash, name, user_id, permissions, read_only, allow_list, created_at, expires_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			id, hashToken(value), name, creator.ID, string(perms), s.flag(in.ReadOnly), string(allowJSON), now.UnixMilli(), expires.UnixMilli())
		if err == nil {
			tok := &Token{ID: id, Name: name, UserID: creator.ID, Username: creator.Username, Permissions: permissions,
				ReadOnly: in.ReadOnly, AllowList: allow, CreatedAt: msTime(now.UnixMilli()), ExpiresAt: msTime(expires.UnixMilli())}
			select {
			case s.wake <- struct{}{}:
			default:
			}
			return value, tok, nil
		}
		// A public id that is taken already: try another one
		if attempt >= 3 || !strings.Contains(strings.ToLower(err.Error()), "unique") {
			return "", nil, fmt.Errorf("store the token: %w", err)
		}
	}
}

// flag is a boolean as the database's column takes it.
func (s *Service) flag(v bool) any {
	if s.db.IsPostgres() {
		return v
	}
	if v {
		return 1
	}
	return 0
}

func cleanName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	switch {
	case name == "":
		return "", inputError("name", "Give the token a name.")
	case !utf8.ValidString(name):
		return "", inputError("name", "The name is not valid text.")
	case utf8.RuneCountInString(name) > MaxNameLength:
		return "", inputError("name", "The name can be at most %d characters long.", MaxNameLength)
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return "", inputError("name", "The name cannot hold line breaks or control characters.")
		}
	}
	return name, nil
}

func cleanPermissions(asked, known []string, holds func(string) bool) ([]string, error) {
	isKnown := map[string]bool{}
	for _, p := range known {
		isKnown[p] = true
	}
	seen := map[string]bool{}
	var out []string
	for _, raw := range asked {
		p := strings.TrimSpace(raw)
		if p == "" || seen[p] {
			continue
		}
		if !isKnown[p] {
			return nil, inputError("permissions", "There is no permission %q.", p)
		}
		if !holds(p) {
			return nil, inputError("permissions", "You can only give permissions you have. You don't have %q.", p)
		}
		seen[p] = true
		out = append(out, p)
	}
	if len(out) == 0 {
		return nil, inputError("permissions", "Choose at least one permission.")
	}
	// server.config reaches the server's files and secrets: a token gets
	// it only with every other permission of its creator
	if seen[ServerConfig] {
		for _, p := range known {
			if holds(p) && !seen[p] {
				return nil, inputError("permissions", "A token gets server.config only together with all of your permissions: it reaches the server's files and secrets. Pick them all, or leave server.config out.")
			}
		}
	}
	sort.Strings(out)
	return out, nil
}

// CleanAllowList checks an allow list of addresses and networks
// (192.0.2.10, 198.51.100.0/24, 2001:db8::/32) and writes each in its
// short form. An empty list allows every address.
func CleanAllowList(entries []string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	for _, raw := range entries {
		entry := strings.TrimSpace(raw)
		if entry == "" {
			continue
		}
		var norm string
		if strings.Contains(entry, "/") {
			prefix, err := netip.ParsePrefix(entry)
			if err != nil {
				return nil, inputError("allow_list", "%q is no address or network.", entry)
			}
			norm = prefix.Masked().String()
		} else {
			addr, err := netip.ParseAddr(entry)
			if err != nil {
				return nil, inputError("allow_list", "%q is no address or network.", entry)
			}
			norm = addr.Unmap().String()
		}
		if !seen[norm] {
			seen[norm] = true
			out = append(out, norm)
		}
	}
	if len(out) > MaxAllowList {
		return nil, inputError("allow_list", "The allow list can hold at most %d entries.", MaxAllowList)
	}
	if out == nil {
		out = []string{}
	}
	return out, nil
}

// Allows reports whether the address ip may use the token.
func (t *Token) Allows(ip string) bool {
	if len(t.AllowList) == 0 {
		return true
	}
	addr, err := netip.ParseAddr(strings.TrimSpace(ip))
	if err != nil {
		return false
	}
	addr = addr.Unmap()
	for _, entry := range t.AllowList {
		if strings.Contains(entry, "/") {
			if prefix, err := netip.ParsePrefix(entry); err == nil && prefix.Contains(addr) {
				return true
			}
			continue
		}
		if other, err := netip.ParseAddr(entry); err == nil && other.Unmap() == addr {
			return true
		}
	}
	return false
}

// randomText is n characters of alphabet, each picked evenly.
func randomText(alphabet string, n int) (string, error) {
	limit := big.NewInt(int64(len(alphabet)))
	out := make([]byte, n)
	for i := range out {
		k, err := rand.Int(rand.Reader, limit)
		if err != nil {
			return "", fmt.Errorf("random token: %w", err)
		}
		out[i] = alphabet[k.Int64()]
	}
	return string(out), nil
}

func hashToken(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

// dummyHash is compared against when no row has the id, so a missing id
// costs the same comparison as a wrong secret.
var dummyHash = hashToken(Prefix + "unknown")

// Parse splits a token into its public id. ok is false for anything that
// is not shaped like a token; no row is read for it.
func Parse(value string) (id string, ok bool) {
	if len(value) != tokenLength || !strings.HasPrefix(value, Prefix) {
		return "", false
	}
	rest := value[len(Prefix):]
	id, secret := rest[:idLength], rest[idLength:]
	if secret[0] != '_' {
		return "", false
	}
	for i := 0; i < len(id); i++ {
		if !strings.ContainsRune(idAlphabet, rune(id[i])) {
			return "", false
		}
	}
	for i := 1; i < len(secret); i++ {
		if !strings.ContainsRune(secretAlphabet, rune(secret[i])) {
			return "", false
		}
	}
	return id, true
}

// FromHeader returns the token of an "Authorization: Bearer ndt_..."
// header. ok is false when the request carries no such header; a bearer
// token of another kind (a device's, a RustDesk client's) is not ours.
func FromHeader(r *http.Request) (string, bool) {
	values := r.Header.Values("Authorization")
	if len(values) == 0 {
		return "", false
	}
	scheme, rest, found := strings.Cut(strings.TrimSpace(values[0]), " ")
	if !found || !strings.EqualFold(scheme, "Bearer") {
		return "", false
	}
	value := strings.TrimSpace(rest)
	if !strings.HasPrefix(value, Prefix) {
		return "", false
	}
	return value, true
}

// Why a token was refused. The answer to the client is the same for all
// of them; the audit log tells them apart.
const (
	RefusedMalformed = "malformed"
	RefusedUnknown   = "unknown"
	RefusedSecret    = "wrong secret"
	RefusedRevoked   = "revoked"
	RefusedExpired   = "expired"
	RefusedAddress   = "address not allowed"
	RefusedAccount   = "account disabled or deleted"
	RefusedLimited   = "rate limited"
)

// Refusal is why Authenticate refused a token.
type Refusal struct {
	Reason string
	// Token is the row when the id was found, for the audit log.
	Token *Token
	// RetryAfter is set for RefusedLimited.
	RetryAfter time.Duration
}

func (r *Refusal) Error() string { return "token refused: " + r.Reason }

// Authenticate checks a token sent from the address ip: its shape, its
// row, the secret, revocation, expiry and the allow list, and the rate
// limits. It answers the row, or a *Refusal. The creator's account is
// the caller's to check.
func (s *Service) Authenticate(ctx context.Context, value, ip string) (*Token, error) {
	id, ok := Parse(value)
	if !ok {
		return nil, s.fail(ip, &Refusal{Reason: RefusedMalformed})
	}
	tok, hash, err := s.load(ctx, id)
	if err != nil {
		return nil, err
	}
	want := dummyHash
	if tok != nil {
		want = hash
	}
	matches := subtle.ConstantTimeCompare([]byte(hashToken(value)), []byte(want)) == 1
	switch {
	case tok == nil:
		return nil, s.fail(ip, &Refusal{Reason: RefusedUnknown})
	case !matches:
		return nil, s.fail(ip, &Refusal{Reason: RefusedSecret, Token: tok})
	case !tok.RevokedAt.IsZero():
		return nil, s.fail(ip, &Refusal{Reason: RefusedRevoked, Token: tok})
	case !s.now().Before(tok.ExpiresAt):
		return nil, s.fail(ip, &Refusal{Reason: RefusedExpired, Token: tok})
	case !tok.Allows(ip):
		return nil, s.fail(ip, &Refusal{Reason: RefusedAddress, Token: tok})
	}
	if wait := s.requests.hit(tok.ID); wait > 0 {
		return nil, &Refusal{Reason: RefusedLimited, Token: tok, RetryAfter: wait}
	}
	return tok, nil
}

// fail counts a failed attempt of the address and answers the refusal,
// or the rate limit once the address made too many: then the answer no
// longer says which kind of failure it was.
func (s *Service) fail(ip string, refusal *Refusal) *Refusal {
	if wait := s.failures.blocked(ip); wait > 0 {
		return &Refusal{Reason: RefusedLimited, Token: refusal.Token, RetryAfter: wait}
	}
	s.failures.add(ip)
	return refusal
}

// NoteFailure counts a refusal the caller made after Authenticate (the
// creator's account), toward the address's limit.
func (s *Service) NoteFailure(ip string) { s.failures.add(ip) }

const columns = `t.public_id, t.name, t.user_id, COALESCE(u.username, ''), t.permissions, t.read_only, t.allow_list,
	t.created_at, t.expires_at, t.last_used_at, COALESCE(t.last_used_ip, ''), t.revoked_at, COALESCE(t.revoked_by, '')`

const from = ` FROM admin_tokens t LEFT JOIN users u ON u.id = t.user_id`

type scanner interface{ Scan(dest ...any) error }

func scanToken(row scanner, hash *string) (*Token, error) {
	var (
		tok               Token
		perms, allow      string
		readOnly          store.Bool
		created, expires  int64
		lastUsed, revoked sql.NullInt64
		dest              []any
	)
	dest = []any{&tok.ID, &tok.Name, &tok.UserID, &tok.Username, &perms, &readOnly, &allow,
		&created, &expires, &lastUsed, &tok.LastUsedIP, &revoked, &tok.RevokedBy}
	if hash != nil {
		dest = append(dest, hash)
	}
	if err := row.Scan(dest...); err != nil {
		return nil, err
	}
	tok.ReadOnly = readOnly.True()
	if err := json.Unmarshal([]byte(perms), &tok.Permissions); err != nil {
		return nil, fmt.Errorf("token %s: permissions: %w", tok.ID, err)
	}
	if err := json.Unmarshal([]byte(allow), &tok.AllowList); err != nil {
		return nil, fmt.Errorf("token %s: allow list: %w", tok.ID, err)
	}
	if tok.AllowList == nil {
		tok.AllowList = []string{}
	}
	tok.CreatedAt, tok.ExpiresAt = msTime(created), msTime(expires)
	if lastUsed.Valid {
		tok.LastUsedAt = msTime(lastUsed.Int64)
	}
	if revoked.Valid {
		tok.RevokedAt = msTime(revoked.Int64)
	}
	return &tok, nil
}

func msTime(ms int64) time.Time { return time.UnixMilli(ms).UTC() }

// load returns the row with the public id and its hash, nil when there is
// none.
func (s *Service) load(ctx context.Context, id string) (*Token, string, error) {
	var hash string
	tok, err := scanToken(s.db.QueryRow(ctx, `SELECT `+columns+`, t.token_hash`+from+` WHERE t.public_id = ?`, id), &hash)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", fmt.Errorf("load token: %w", err)
	}
	return tok, hash, nil
}

// Get returns the token with the public id, nil when there is none.
func (s *Service) Get(ctx context.Context, id string) (*Token, error) {
	tok, _, err := s.load(ctx, id)
	return tok, err
}

// List returns the tokens of an account, or of every account for userID
// 0, the newest first.
func (s *Service) List(ctx context.Context, userID int64) ([]*Token, error) {
	query := `SELECT ` + columns + from
	var args []any
	if userID != 0 {
		query += ` WHERE t.user_id = ?`
		args = append(args, userID)
	}
	rows, err := s.db.Query(ctx, query+` ORDER BY t.created_at DESC, t.id DESC`, args...)
	if err != nil {
		return nil, fmt.Errorf("list tokens: %w", err)
	}
	defer rows.Close()
	out := []*Token{}
	for rows.Next() {
		tok, err := scanToken(rows, nil)
		if err != nil {
			return nil, err
		}
		out = append(out, tok)
	}
	return out, rows.Err()
}

// touchEvery is how often a token's last use is written: at most once a
// minute per token.
const touchEvery = time.Minute

// Touch notes a use of the token from the address ip.
func (s *Service) Touch(ctx context.Context, tok *Token, ip string) {
	now := s.now()
	s.mu.Lock()
	last, seen := s.touched[tok.ID]
	if seen && now.Sub(last) < touchEvery && !tok.LastUsedAt.IsZero() && tok.LastUsedIP == ip {
		s.mu.Unlock()
		return
	}
	s.touched[tok.ID] = now
	s.mu.Unlock()
	_, _ = s.db.Exec(ctx, `UPDATE admin_tokens SET last_used_at = ?, last_used_ip = ? WHERE public_id = ?`, now.UnixMilli(), ip, tok.ID)
}

// window counts hits per key in a fixed window.
type window struct {
	max    int
	length time.Duration
	now    func() time.Time

	mu    sync.Mutex
	hits  map[string]int
	start time.Time
}

func newWindow(max int, length time.Duration, now func() time.Time) *window {
	return &window{max: max, length: length, now: now, hits: map[string]int{}}
}

// roll starts a new window when the current one is over. Callers hold mu.
func (w *window) roll() time.Time {
	now := w.now()
	if w.start.IsZero() || now.Sub(w.start) >= w.length {
		w.start, w.hits = now, map[string]int{}
	}
	return now
}

func (w *window) wait(now time.Time) time.Duration {
	wait := w.start.Add(w.length).Sub(now)
	if wait < time.Second {
		wait = time.Second
	}
	return wait
}

// add counts a hit for key.
func (w *window) add(key string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.roll()
	w.hits[key]++
}

// blocked returns how long key has to wait, 0 when it may go on.
func (w *window) blocked(key string) time.Duration {
	w.mu.Lock()
	defer w.mu.Unlock()
	now := w.roll()
	if w.hits[key] < w.max {
		return 0
	}
	return w.wait(now)
}

// hit counts a hit for key and returns how long it has to wait when it is
// over the limit.
func (w *window) hit(key string) time.Duration {
	w.mu.Lock()
	defer w.mu.Unlock()
	now := w.roll()
	w.hits[key]++
	if w.hits[key] <= w.max {
		return 0
	}
	return w.wait(now)
}

type requestKey struct{}

// Use is a request a token authenticated: the token and what the
// pipeline worked out about it.
type Use struct {
	Token *Token
	// Full says the token carries every permission its creator holds now.
	Full bool
}

// WithUse returns ctx with the token use.
func WithUse(ctx context.Context, use *Use) context.Context {
	return context.WithValue(ctx, requestKey{}, use)
}

// FromContext returns the token use of a request, nil for a request
// without a token.
func FromContext(ctx context.Context) *Use {
	use, _ := ctx.Value(requestKey{}).(*Use)
	return use
}

// FromRequest is FromContext of the request's context.
func FromRequest(r *http.Request) *Use { return FromContext(r.Context()) }
