package session

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
)

// An account may be signed in on several browsers at once (DECISIONS
// A239). Each of them is a "signed-in device" the account sees in its
// list and can sign out; someone who manages accounts sees and ends them
// on the user's page. The list names a session by a handle, never by its
// id: the id is what the cookie carries.

// SingleSessionKey is the key in the server's configuration (server_config)
// of Settings, Sign-in, "One session per account": "true" gives every
// account a limit of one session, as the console had before A239.
// SESSION_MAX_PER_USER, when it is set, decides over it.
const SingleSessionKey = "console_single_session"

// The sources of an account's session limit (Limit.Source).
const (
	LimitFromEnv     = "env"
	LimitFromSetting = "setting"
	LimitDefault     = "default"
)

// Limit is how many sessions an account may have at once, and what
// decides it.
type Limit struct {
	// Max is the number; 0 means any number.
	Max int
	// Source is LimitFromEnv, LimitFromSetting or LimitDefault.
	Source string
	// Single is the setting "One session per account".
	Single bool
	// EnvValue is SESSION_MAX_PER_USER as it is set, "" when it is not.
	EnvValue string
}

// Limit reads the session limit in force: SESSION_MAX_PER_USER when it is
// set, else 1 with "One session per account" on, else the policy's cap.
// A setting that can't be read counts as on: a failure never gives more
// sessions than the server allows.
func (m *Manager) Limit(ctx context.Context) (Limit, error) {
	single, err := m.SingleSession(ctx)
	if err != nil {
		single = true
	}
	l := Limit{Max: m.policy.MaxSessionsPerUser, Source: LimitDefault, Single: single, EnvValue: m.policy.MaxPerUserEnv}
	switch {
	case l.EnvValue != "":
		l.Source = LimitFromEnv
	case single:
		l.Max, l.Source = 1, LimitFromSetting
	}
	return l, err
}

// SingleSession reads the setting "One session per account". A database
// without the configuration table has it off.
func (m *Manager) SingleSession(ctx context.Context) (bool, error) {
	db := m.table.db
	var value sql.NullString
	err := db.QueryRow(ctx, `SELECT value FROM server_config WHERE key = ?`, SingleSessionKey).Scan(&value)
	switch {
	case err == nil:
		return strings.EqualFold(strings.TrimSpace(value.String), "true"), nil
	case errors.Is(err, sql.ErrNoRows):
		return false, nil
	case strings.Contains(err.Error(), db.Pick("no such table", "does not exist")):
		return false, nil
	}
	return false, fmt.Errorf("read %s: %w", SingleSessionKey, err)
}

// SetSingleSession saves the setting "One session per account". It takes
// effect at the next sign-in; the sessions open now stay.
func (m *Manager) SetSingleSession(ctx context.Context, on bool) error {
	db := m.table.db
	create := db.Pick(`CREATE TABLE IF NOT EXISTS server_config (key TEXT PRIMARY KEY, value TEXT DEFAULT '')`,
		`CREATE TABLE IF NOT EXISTS server_config (key TEXT PRIMARY KEY, value TEXT NOT NULL DEFAULT '')`)
	if _, err := db.Exec(ctx, create); err != nil {
		return fmt.Errorf("save %s: %w", SingleSessionKey, err)
	}
	value := "false"
	if on {
		value = "true"
	}
	if _, err := db.Exec(ctx, `INSERT INTO server_config (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, SingleSessionKey, value); err != nil {
		return fmt.Errorf("save %s: %w", SingleSessionKey, err)
	}
	return nil
}

// Handle is the name of a session in the lists: the first 32 hex digits
// of a SHA-256 of its id. It can't be turned back into the id.
func Handle(sid string) string {
	sum := sha256.Sum256([]byte("neodesk-session-handle\x00" + sid))
	return hex.EncodeToString(sum[:16])
}

// Device is a session as the lists show it: one browser or device the
// account is signed in on.
type Device struct {
	// ID is the session's handle (Handle).
	ID string `json:"id"`
	// Current marks the session that asked for the list.
	Current bool `json:"current"`
	// Browser and System are read from the user agent ("Firefox",
	// "Windows"); "" when it does not say.
	Browser   string `json:"browser"`
	System    string `json:"system"`
	IPAddress string `json:"ipAddress"`
	UserAgent string `json:"userAgent"`
	// CreatedAt (the sign-in), LastActivityAt and ExpiresAt are in
	// milliseconds.
	CreatedAt      int64 `json:"createdAt"`
	LastActivityAt int64 `json:"lastActivityAt"`
	ExpiresAt      int64 `json:"expiresAt"`

	sid string
}

// Describe is the device in plain words for the audit log: "Firefox on
// Windows, 203.0.113.5".
func (d Device) Describe() string {
	what := d.Browser
	switch {
	case what == "" && d.System == "":
		what = "unknown browser"
	case what == "":
		what = "a browser on " + d.System
	case d.System != "":
		what += " on " + d.System
	}
	if d.IPAddress != "" {
		what += ", " + d.IPAddress
	}
	return what
}

// Short is the start of the handle, for the audit log.
func (d Device) Short() string {
	if len(d.ID) > 8 {
		return d.ID[:8]
	}
	return d.ID
}

func newDevice(sid, ip, ua string, created, last, expires int64) Device {
	browser, system := DescribeUserAgent(ua)
	return Device{ID: Handle(sid), sid: sid, Browser: browser, System: system, IPAddress: ip, UserAgent: ua,
		CreatedAt: created, LastActivityAt: last, ExpiresAt: expires}
}

// Errors of EndDevice.
var (
	ErrSessionNotFound = errors.New("session not found")
	ErrCurrentSession  = errors.New("the session of this browser")
)

// Devices lists the account's live sessions, the most recently used
// first. current is the id of the session that asks ("" for none, as for
// an API token).
func (m *Manager) Devices(ctx context.Context, userID int64, current string) ([]Device, error) {
	now := m.now().UnixMilli()
	rows, err := m.table.db.Query(ctx,
		`SELECT sid, ip_address, user_agent, created_at, last_activity_at, expires_at, end_reason, `+m.table.sqlActive()+`
		 FROM panel_sessions
		 WHERE user_id = ? AND is_active = `+m.table.sqlTrue()+`
		 ORDER BY COALESCE(last_activity_at, created_at, 0) DESC, sid`, userID)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	defer rows.Close()
	list := []Device{}
	for rows.Next() {
		var sid string
		var ip, ua, reason sql.NullString
		var created, last, expires any
		var active int64
		if err := rows.Scan(&sid, &ip, &ua, &created, &last, &expires, &reason, &active); err != nil {
			return nil, fmt.Errorf("list sessions: %w", err)
		}
		row := storedRow{expiresAt: columnMillis(expires), createdAt: columnMillis(created), lastActivityAt: columnMillis(last), endReason: reason.String, active: active == 1}
		// A session past a limit that the sweep has not ended yet is gone
		if gone, _ := m.table.expired(row, now); gone {
			continue
		}
		d := newDevice(sid, ip.String, ua.String, row.createdAt, row.lastActivityAt, row.expiresAt)
		d.Current = current != "" && sid == current
		list = append(list, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	return list, nil
}

// EndDevice ends the account's session with the handle, for the reason
// ReasonSignedOut or ReasonEndedByAdmin. The session current names (the
// caller's own) is not ended here: ErrCurrentSession. A handle the
// account has no live session for is ErrSessionNotFound.
func (m *Manager) EndDevice(ctx context.Context, userID int64, handle, current, reason string) (Device, error) {
	list, err := m.Devices(ctx, userID, current)
	if err != nil {
		return Device{}, err
	}
	for _, d := range list {
		if d.ID != handle {
			continue
		}
		if d.Current {
			return Device{}, ErrCurrentSession
		}
		ended, err := m.table.endSessions(ctx, userID, []string{d.sid}, "", reason)
		if err != nil {
			return Device{}, err
		}
		if len(ended) == 0 {
			return Device{}, ErrSessionNotFound
		}
		m.ended(ended)
		return ended[0], nil
	}
	return Device{}, ErrSessionNotFound
}

// EndDevices ends every live session of the account but keep ("" ends
// all) with a reason, and returns them.
func (m *Manager) EndDevices(ctx context.Context, userID int64, keep, reason string) ([]Device, error) {
	ended, err := m.table.endSessions(ctx, userID, nil, keep, reason)
	if err != nil {
		return nil, err
	}
	m.ended(ended)
	return ended, nil
}

// OnEnd registers what to do with the ids of sessions that ended here: the
// console closes their sockets. It is called after the rows are written.
func (m *Manager) OnEnd(fn func(sids []string)) {
	m.endMu.Lock()
	defer m.endMu.Unlock()
	m.onEnd = append(m.onEnd, fn)
}

// endHooks holds the OnEnd functions.
type endHooks struct {
	endMu sync.Mutex
	onEnd []func(sids []string)
}

// ended tells the OnEnd functions about sessions that ended.
func (m *Manager) ended(list []Device) {
	if len(list) == 0 {
		return
	}
	sids := make([]string, len(list))
	for i, d := range list {
		sids[i] = d.sid
	}
	m.endedIDs(sids)
}

func (m *Manager) endedIDs(sids []string) {
	if len(sids) == 0 {
		return
	}
	m.endMu.Lock()
	hooks := append([]func([]string){}, m.onEnd...)
	m.endMu.Unlock()
	for _, fn := range hooks {
		fn(sids)
	}
}
