package session

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/unitronix/betterdesk-server/console/store"
)

// table is the panel_sessions table: one row per session, kept after the
// session ended so the account's history stays visible.
type table struct {
	db     *store.DB
	policy Policy
	now    func() time.Time
}

// ensureSchema creates the table and adds the columns and indexes an older
// table lacks.
func (t *table) ensureSchema(ctx context.Context) error {
	create := t.db.Pick(
		`CREATE TABLE IF NOT EXISTS panel_sessions (
			sid TEXT PRIMARY KEY,
			session_json TEXT NOT NULL,
			expires_at INTEGER NOT NULL,
			updated_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE TABLE IF NOT EXISTS panel_sessions (
			sid TEXT PRIMARY KEY,
			session_json TEXT NOT NULL,
			expires_at TIMESTAMPTZ NOT NULL,
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`)
	if _, err := t.db.Exec(ctx, create); err != nil {
		return fmt.Errorf("create panel_sessions: %w", err)
	}
	if _, err := t.db.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_panel_sessions_expires ON panel_sessions (expires_at)`); err != nil {
		return fmt.Errorf("index panel_sessions: %w", err)
	}

	columns := []struct{ name, sqlite, postgres string }{
		{"user_id", "INTEGER", "INTEGER"},
		{"username", "TEXT", "TEXT"},
		{"ip_address", "TEXT", "TEXT"},
		{"user_agent", "TEXT", "TEXT"},
		{"client_fingerprint", "TEXT", "TEXT"},
		{"created_at", "INTEGER", "BIGINT"},
		{"last_activity_at", "INTEGER", "BIGINT"},
		{"ended_at", "INTEGER", "BIGINT"},
		{"end_reason", "TEXT", "TEXT"},
		{"is_active", "INTEGER NOT NULL DEFAULT 1", "BOOLEAN NOT NULL DEFAULT TRUE"},
	}
	existing := map[string]bool{}
	if !t.db.IsPostgres() {
		names, err := t.sqliteColumns(ctx)
		if err != nil {
			return err
		}
		existing = names
	}
	for _, col := range columns {
		if existing[col.name] {
			continue
		}
		stmt := t.db.Pick(
			"ALTER TABLE panel_sessions ADD COLUMN "+col.name+" "+col.sqlite,
			"ALTER TABLE panel_sessions ADD COLUMN IF NOT EXISTS "+col.name+" "+col.postgres)
		if _, err := t.db.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("add panel_sessions.%s: %w", col.name, err)
		}
	}
	for _, stmt := range []string{
		`CREATE INDEX IF NOT EXISTS idx_panel_sessions_user_active ON panel_sessions (user_id, is_active)`,
		`CREATE INDEX IF NOT EXISTS idx_panel_sessions_active ON panel_sessions (is_active, expires_at)`,
	} {
		if _, err := t.db.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("index panel_sessions: %w", err)
		}
	}
	return nil
}

func (t *table) sqliteColumns(ctx context.Context) (map[string]bool, error) {
	rows, err := t.db.Query(ctx, `SELECT name FROM pragma_table_info('panel_sessions')`)
	if err != nil {
		return nil, fmt.Errorf("read panel_sessions columns: %w", err)
	}
	defer rows.Close()
	names := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("read panel_sessions columns: %w", err)
		}
		names[name] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read panel_sessions columns: %w", err)
	}
	return names, nil
}

// SQL that differs between the two databases.
func (t *table) sqlNow() string   { return t.db.Pick("datetime('now')", "NOW()") }
func (t *table) sqlTrue() string  { return t.db.Pick("1", "TRUE") }
func (t *table) sqlFalse() string { return t.db.Pick("0", "FALSE") }

// sqlTime is the placeholder for an expires_at value given in milliseconds.
func (t *table) sqlTime() string { return t.db.Pick("?", "to_timestamp(? / 1000.0)") }

// sqlActive selects is_active as 1 or 0.
func (t *table) sqlActive() string {
	return t.db.Pick("CASE WHEN is_active = 1 THEN 1 ELSE 0 END", "CASE WHEN is_active THEN 1 ELSE 0 END")
}

func (t *table) nowMs() int64 { return t.now().UnixMilli() }

// storedRow is what get needs of a row.
type storedRow struct {
	json           string
	expiresAt      int64
	createdAt      int64
	lastActivityAt int64
	endReason      string
	active         bool
}

// expired says whether a row no longer carries a session, and why. The
// order matters: an ended row keeps its reason, the absolute lifetime comes
// before the idle time, and the cookie's expiry is asked last.
func (t *table) expired(row storedRow, now int64) (bool, string) {
	if !row.active {
		if row.endReason != "" {
			return true, row.endReason
		}
		return true, ReasonRevoked
	}
	created := row.createdAt
	last := row.lastActivityAt
	if last == 0 {
		last = created
	}
	if last == 0 {
		last = now
	}
	if created != 0 && now-created > t.policy.MaxAgeMs {
		return true, ReasonMaxAge
	}
	if last != 0 && now-last > t.policy.IdleTimeoutMs {
		return true, ReasonIdle
	}
	if row.expiresAt <= now {
		return true, ReasonIdle
	}
	return false, ""
}

// get returns the stored JSON of a live session. found is false when there
// is no row, or when the row has ended or is past a limit; a row found past
// a limit is marked as ended on the way.
func (t *table) get(ctx context.Context, sid string) (text string, found bool, err error) {
	var row storedRow
	var expires, created, last any
	var reason sql.NullString
	var active int64
	err = t.db.QueryRow(ctx,
		`SELECT session_json, expires_at, created_at, last_activity_at, end_reason, `+t.sqlActive()+`
		 FROM panel_sessions WHERE sid = ?`, sid,
	).Scan(&row.json, &expires, &created, &last, &reason, &active)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("load session: %w", err)
	}
	row.expiresAt = columnMillis(expires)
	row.createdAt = columnMillis(created)
	row.lastActivityAt = columnMillis(last)
	row.endReason = reason.String
	row.active = active == 1

	if gone, why := t.expired(row, t.nowMs()); gone {
		if row.active {
			if err := t.markEnded(ctx, sid, why); err != nil {
				return "", false, err
			}
		}
		return "", false, nil
	}
	return row.json, true, nil
}

// columnMillis reads a time column: milliseconds as a number, or a
// timestamp.
func columnMillis(v any) int64 {
	switch x := v.(type) {
	case int64:
		return x
	case float64:
		return toInt64(x)
	case time.Time:
		return x.UnixMilli()
	case []byte:
		n, _ := strconv.ParseFloat(strings.TrimSpace(string(x)), 64)
		return toInt64(n)
	case string:
		n, _ := strconv.ParseFloat(strings.TrimSpace(x), 64)
		return toInt64(n)
	}
	return 0
}

// markEnded ends an active row. A reason the row already has stays.
func (t *table) markEnded(ctx context.Context, sid, reason string) error {
	_, err := t.db.Exec(ctx,
		`UPDATE panel_sessions
		 SET is_active = `+t.sqlFalse()+`, ended_at = ?, end_reason = COALESCE(end_reason, ?), updated_at = `+t.sqlNow()+`
		 WHERE sid = ? AND is_active = `+t.sqlTrue(),
		t.nowMs(), reason, sid)
	if err != nil {
		return fmt.Errorf("end session: %w", err)
	}
	return nil
}

// record is a session as the table stores it.
type record struct {
	json           string
	expiresAt      int64
	createdAt      int64
	lastActivityAt int64
	userID         any
	username       string
	ip             string
	userAgent      string
	fingerprint    string
}

// set writes a session: a new row, or new values for an existing one. On
// an existing row the account, the address, the user agent and the
// fingerprint are only replaced by values that are not empty, created_at
// stays once set, and an ended row stays ended.
func (t *table) set(ctx context.Context, sid string, rec record) error {
	_, err := t.db.Exec(ctx,
		`INSERT INTO panel_sessions (
			sid, session_json, expires_at, updated_at,
			user_id, username, ip_address, user_agent, client_fingerprint,
			created_at, last_activity_at, ended_at, end_reason, is_active
		) VALUES (?, ?, `+t.sqlTime()+`, `+t.sqlNow()+`, ?, ?, ?, ?, ?, ?, ?, NULL, NULL, `+t.sqlTrue()+`)
		ON CONFLICT(sid) DO UPDATE SET
			session_json = excluded.session_json,
			expires_at = excluded.expires_at,
			updated_at = `+t.sqlNow()+`,
			user_id = COALESCE(excluded.user_id, panel_sessions.user_id),
			username = COALESCE(excluded.username, panel_sessions.username),
			ip_address = COALESCE(NULLIF(excluded.ip_address, ''), panel_sessions.ip_address),
			user_agent = COALESCE(NULLIF(excluded.user_agent, ''), panel_sessions.user_agent),
			client_fingerprint = COALESCE(NULLIF(excluded.client_fingerprint, ''), panel_sessions.client_fingerprint),
			created_at = COALESCE(panel_sessions.created_at, excluded.created_at),
			last_activity_at = excluded.last_activity_at,
			is_active = CASE
				WHEN panel_sessions.is_active = `+t.sqlFalse()+` THEN `+t.sqlFalse()+`
				ELSE `+t.sqlTrue()+`
			END`,
		sid, rec.json, rec.expiresAt, rec.userID, nullIfEmpty(rec.username),
		nullIfEmpty(rec.ip), nullIfEmpty(rec.userAgent), nullIfEmpty(rec.fingerprint),
		rec.createdAt, rec.lastActivityAt)
	if err != nil {
		return fmt.Errorf("save session: %w", err)
	}
	return nil
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// touch moves the expiry and the last activity of an active row.
func (t *table) touch(ctx context.Context, sid string, expiresAt, lastActivityAt int64) error {
	_, err := t.db.Exec(ctx,
		`UPDATE panel_sessions
		 SET expires_at = `+t.sqlTime()+`, last_activity_at = ?, updated_at = `+t.sqlNow()+`
		 WHERE sid = ? AND is_active = `+t.sqlTrue(),
		expiresAt, lastActivityAt, sid)
	if err != nil {
		return fmt.Errorf("touch session: %w", err)
	}
	return nil
}

// destroy ends a row with the reason "destroyed" unless it has one, and
// empties its stored session. The row stays.
func (t *table) destroy(ctx context.Context, sid string) error {
	if err := t.markEnded(ctx, sid, ReasonDestroyed); err != nil {
		return err
	}
	_, err := t.db.Exec(ctx,
		`UPDATE panel_sessions
		 SET session_json = '{}', end_reason = COALESCE(end_reason, ?), updated_at = `+t.sqlNow()+`
		 WHERE sid = ?`,
		ReasonDestroyed, sid)
	if err != nil {
		return fmt.Errorf("destroy session: %w", err)
	}
	return nil
}

// end ends a row with a reason and empties its stored session.
func (t *table) end(ctx context.Context, sid, reason string) error {
	if err := t.markEnded(ctx, sid, reason); err != nil {
		return err
	}
	_, err := t.db.Exec(ctx,
		`UPDATE panel_sessions SET session_json = '{}', updated_at = `+t.sqlNow()+` WHERE sid = ?`, sid)
	if err != nil {
		return fmt.Errorf("end session: %w", err)
	}
	return nil
}

// endAccount ends the active rows of an account but keep with the reason
// "revoked" and empties their stored sessions.
func (t *table) endAccount(ctx context.Context, userID int64, keep string) (int, error) {
	result, err := t.db.Exec(ctx,
		`UPDATE panel_sessions
		 SET is_active = `+t.sqlFalse()+`, ended_at = ?, end_reason = ?, session_json = '{}', updated_at = `+t.sqlNow()+`
		 WHERE user_id = ? AND is_active = `+t.sqlTrue()+` AND sid <> ?`,
		t.nowMs(), ReasonRevoked, userID, keep)
	if err != nil {
		return 0, fmt.Errorf("end the sessions of account %d: %w", userID, err)
	}
	ended, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("end the sessions of account %d: %w", userID, err)
	}
	return int(ended), nil
}

// replacedSession is a session a newer sign-in ended.
type replacedSession struct {
	sid       string
	ip        string
	userAgent string
}

// activate makes sid the account's session after sign-in and applies the
// per-account limit, in one transaction. With a limit of N, this session
// and the N-1 other sessions of the account that were active most recently
// stay; the rest end with the reason "replaced". A limit of 0 ends none.
func (t *table) activate(ctx context.Context, sid string, userID int64, username string, meta Meta, at int64) ([]replacedSession, error) {
	now := t.nowMs()
	expiresAt := min(at+t.policy.MaxAgeMs, now+t.policy.IdleTimeoutMs)
	var replaced []replacedSession
	err := t.db.InTx(ctx, func(tx *store.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT sid, ip_address, user_agent
			 FROM panel_sessions
			 WHERE user_id = ? AND is_active = `+t.sqlTrue()+` AND sid <> ?
			 ORDER BY COALESCE(last_activity_at, created_at, 0) DESC, sid`+t.db.Pick("", " FOR UPDATE"),
			userID, sid)
		if err != nil {
			return err
		}
		var others []replacedSession
		for rows.Next() {
			var other replacedSession
			var ip, ua sql.NullString
			if err := rows.Scan(&other.sid, &ip, &ua); err != nil {
				_ = rows.Close()
				return err
			}
			other.ip, other.userAgent = ip.String, ua.String
			others = append(others, other)
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return err
		}
		if limit := t.policy.MaxSessionsPerUser; limit > 0 && len(others) > limit-1 {
			replaced = others[limit-1:]
		}
		for _, old := range replaced {
			if _, err := tx.Exec(ctx,
				`UPDATE panel_sessions
				 SET is_active = `+t.sqlFalse()+`, ended_at = ?, end_reason = ?, updated_at = `+t.sqlNow()+`
				 WHERE sid = ?`,
				now, ReasonReplaced, old.sid); err != nil {
				return err
			}
		}
		_, err = tx.Exec(ctx,
			`UPDATE panel_sessions
			 SET user_id = ?, username = ?, ip_address = ?, user_agent = ?, client_fingerprint = ?,
			     created_at = ?, last_activity_at = ?, expires_at = `+t.sqlTime()+`,
			     ended_at = NULL, end_reason = NULL, is_active = `+t.sqlTrue()+`, updated_at = `+t.sqlNow()+`
			 WHERE sid = ?`,
			userID, nullIfEmpty(username), nullIfEmpty(cleanIP(meta.IP)), nullIfEmpty(cleanUserAgent(meta.UserAgent)),
			nullIfEmpty(cleanFingerprint(meta.Fingerprint)), at, at, expiresAt, sid)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("activate session: %w", err)
	}
	return replaced, nil
}

// cleanup ends active rows that are past their expiry, the absolute
// lifetime or the idle time, and deletes rows that never belonged to an
// account one day after they expired. Rows of accounts are kept.
func (t *table) cleanup(ctx context.Context) error {
	now := t.nowMs()
	var err error
	if t.db.IsPostgres() {
		_, err = t.db.Exec(ctx,
			`UPDATE panel_sessions
			 SET is_active = FALSE, ended_at = COALESCE(ended_at, ?), end_reason = COALESCE(end_reason, ?), updated_at = NOW()
			 WHERE is_active = TRUE AND (
			     expires_at <= NOW()
			     OR (created_at IS NOT NULL AND ? - created_at > ?)
			     OR (last_activity_at IS NOT NULL AND ? - last_activity_at > ?)
			 )`,
			now, ReasonIdle, now, t.policy.MaxAgeMs, now, t.policy.IdleTimeoutMs)
	} else {
		_, err = t.db.Exec(ctx,
			`UPDATE panel_sessions
			 SET is_active = 0, ended_at = COALESCE(ended_at, ?), end_reason = COALESCE(end_reason, ?), updated_at = datetime('now')
			 WHERE is_active = 1 AND (
			     expires_at <= ?
			     OR (created_at IS NOT NULL AND ? - created_at > ?)
			     OR (last_activity_at IS NOT NULL AND ? - last_activity_at > ?)
			 )`,
			now, ReasonIdle, now, now, t.policy.MaxAgeMs, now, t.policy.IdleTimeoutMs)
	}
	if err != nil {
		return fmt.Errorf("end stale sessions: %w", err)
	}
	if t.db.IsPostgres() {
		_, err = t.db.Exec(ctx,
			`DELETE FROM panel_sessions WHERE user_id IS NULL AND expires_at <= NOW() - INTERVAL '1 day'`)
	} else {
		_, err = t.db.Exec(ctx,
			`DELETE FROM panel_sessions WHERE user_id IS NULL AND expires_at <= ?`, now-24*60*60*1000)
	}
	if err != nil {
		return fmt.Errorf("delete old anonymous sessions: %w", err)
	}
	return nil
}

// list returns an account's sessions, the most recently active first.
func (t *table) list(ctx context.Context, userID int64, includeEnded bool, limit int) ([]Info, error) {
	if limit <= 0 {
		limit = 50
	}
	limit = min(200, limit)
	where := "user_id = ?"
	if !includeEnded {
		where += " AND is_active = " + t.sqlTrue()
	}
	rows, err := t.db.Query(ctx,
		`SELECT sid, username, ip_address, user_agent, client_fingerprint,
		        created_at, last_activity_at, ended_at, end_reason, `+t.sqlActive()+`, expires_at
		 FROM panel_sessions
		 WHERE `+where+`
		 ORDER BY COALESCE(last_activity_at, created_at) DESC`+t.db.Pick("", " NULLS LAST")+`
		 LIMIT ?`,
		userID, limit)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	defer rows.Close()
	list := []Info{}
	for rows.Next() {
		var info Info
		var username, ip, ua, fp, reason sql.NullString
		var active int64
		var created, last, ended, expires any
		if err := rows.Scan(&info.ID, &username, &ip, &ua, &fp, &created, &last, &ended, &reason, &active, &expires); err != nil {
			return nil, fmt.Errorf("list sessions: %w", err)
		}
		info.Username, info.IPAddress, info.UserAgent = username.String, ip.String, ua.String
		info.ClientFingerprint, info.EndReason = fp.String, reason.String
		info.CreatedAt, info.LastActivityAt, info.EndedAt = columnMillis(created), columnMillis(last), columnMillis(ended)
		info.Active = active == 1
		info.ExpiresAt = columnMillis(expires)
		info.wideAsText = t.db.IsPostgres()
		info.hasCreated, info.hasLast, info.hasEnded = created != nil, last != nil, ended != nil
		list = append(list, info)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	return list, nil
}
