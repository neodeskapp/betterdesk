package devicegroups

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/logging"
)

// The console keeps its own copy of the devices in the table peer. The
// signal server's table peers is the original; reading the copy first
// brings it up to date (at most every 30 seconds), and a change of a
// device id is carried into the console's tables.

// serverPeerSyncInterval is how long a copy of the server's devices is
// good for.
const serverPeerSyncInterval = 30 * time.Second

// sqlitePeerCopy copies every device of the server that is not deleted
// into peer: new ones whole, known ones with their online state, last
// contact, tags and, where peer has none, the system information.
const sqlitePeerCopy = `
                INSERT INTO peer (id, uuid, pk, info, ip, "user", status_online, last_online, created_at,
                                  is_deleted, is_banned, banned_at, banned_reason, tags)
                SELECT
                    p.id,
                    COALESCE(p.uuid, ''),
                    p.pk,
                    json_object(
                        'hostname', COALESCE(p.hostname, ''),
                        'os',       COALESCE(p.os, ''),
                        'platform', COALESCE(p.os, ''),
                        'version',  COALESCE(p.version, '')
                    ),
                    COALESCE(p.ip, ''),
                    COALESCE(p."user", ''),
                    CASE WHEN p.status = 'ONLINE' THEN 1 ELSE 0 END,
                    p.last_online,
                    COALESCE(p.created_at, datetime('now')),
                    0,
                    CASE WHEN p.banned THEN 1 ELSE 0 END,
                    p.banned_at,
                    COALESCE(p.ban_reason, ''),
                    COALESCE(p.tags, '')
                FROM peers p
                WHERE NOT p.soft_deleted
                ON CONFLICT(id) DO UPDATE SET
                    status_online = excluded.status_online,
                    last_online   = COALESCE(excluded.last_online, last_online),
                    info          = CASE WHEN info IS NULL OR info = '{}' OR info = '' THEN excluded.info ELSE info END,
                    is_deleted    = 0,
                    tags          = COALESCE(NULLIF(excluded.tags, ''), tags)`

// postgresPeerCopy is sqlitePeerCopy for PostgreSQL, where peer has no
// tags.
const postgresPeerCopy = `
                INSERT INTO peer (id, uuid, pk, info, ip, "user", status_online, last_online, created_at,
                                  is_deleted, is_banned, banned_at, banned_reason)
                SELECT
                    p.id,
                    COALESCE(p.uuid, ''),
                    p.pk,
                    json_build_object(
                        'hostname', COALESCE(p.hostname, ''),
                        'os',       COALESCE(p.os, ''),
                        'platform', COALESCE(p.os, ''),
                        'version',  COALESCE(p.version, '')
                    )::jsonb,
                    COALESCE(p.ip, ''),
                    COALESCE(p."user", ''),
                    (p.status = 'ONLINE'),
                    p.last_online,
                    COALESCE(p.created_at, NOW()),
                    FALSE,
                    COALESCE(p.banned, FALSE),
                    p.banned_at,
                    COALESCE(p.ban_reason, '')
                FROM peers p
                WHERE NOT p.soft_deleted
                ON CONFLICT(id) DO UPDATE SET
                    status_online = EXCLUDED.status_online,
                    last_online   = COALESCE(EXCLUDED.last_online, peer.last_online),
                    info          = CASE WHEN peer.info IS NULL OR peer.info = '{}' THEN EXCLUDED.info ELSE peer.info END,
                    is_deleted    = FALSE`

// ghostPeers deletes the copies of devices whose id was changed away and
// that the server no longer has under the old id.
const ghostPeers = `
                DELETE FROM peer WHERE id IN (
                    SELECT h.old_id FROM id_change_history h
                    LEFT JOIN peers p ON p.id = h.old_id AND NOT p.soft_deleted
                    WHERE p.id IS NULL
                )`

// SyncServerPeers brings the console's device table up to date with the
// server's: what the old console did before it read peer for a list or
// the device counts. It runs at most once every 30 seconds, counted from
// the start of the last run, whether that run worked or not; a failure is
// logged unless the server's tables are missing, and is no error for the
// caller.
func (s *Service) SyncServerPeers(ctx context.Context) {
	now := s.now()
	s.syncMu.Lock()
	if !s.lastSync.IsZero() && now.Sub(s.lastSync) < serverPeerSyncInterval {
		s.syncMu.Unlock()
		return
	}
	s.lastSync = now
	s.syncMu.Unlock()
	if s.db.IsPostgres() {
		s.syncPostgres(ctx)
		return
	}
	s.syncSQLite(ctx)
}

// sqliteTableExists reports whether the SQLite file has the table.
func (s *Service) sqliteTableExists(ctx context.Context, name string) (bool, error) {
	var found string
	err := s.db.QueryRow(ctx, `SELECT name FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func (s *Service) syncSQLite(ctx context.Context) {
	err := func() error {
		if ok, err := s.sqliteTableExists(ctx, "peers"); err != nil || !ok {
			return err
		}
		if _, err := s.db.Exec(ctx, sqlitePeerCopy); err != nil {
			return err
		}
		history, err := s.sqliteTableExists(ctx, "id_change_history")
		if err != nil {
			return err
		}
		if history {
			result, err := s.db.Exec(ctx, ghostPeers)
			if err != nil {
				return err
			}
			if n, _ := result.RowsAffected(); n > 0 {
				logging.Logf("DB", "syncGoPeersSqlite: cleaned up %d ghost peer(s) from ID changes", n)
			}
		}
		s.renamesSQLite(ctx)
		return nil
	}()
	if err != nil && !strings.Contains(err.Error(), "no such table") {
		logging.Logf("DB", "WARN: syncGoPeersSqlite error: %v", err)
	}
}

// renamesSQLite carries the id changes the server made into the console's
// tables, for the old ids peer still has.
func (s *Service) renamesSQLite(ctx context.Context) {
	err := func() error {
		history, err := s.sqliteTableExists(ctx, "id_change_history")
		if err != nil {
			return err
		}
		peers, err := s.sqliteTableExists(ctx, "peers")
		if err != nil {
			return err
		}
		if !history || !peers {
			return nil
		}
		return s.cascadeRenames(ctx, `
                SELECT h.old_id, h.new_id FROM id_change_history h
                INNER JOIN peers p ON p.id = h.new_id AND (p.soft_deleted IS NULL OR p.soft_deleted = 0)
                WHERE EXISTS (SELECT 1 FROM peer WHERE id = h.old_id)`)
	}()
	if err != nil && !strings.Contains(err.Error(), "no such table") {
		logging.Logf("DB", "WARN: syncPanelPeerIdRenamesFromGo: %v", err)
	}
}

func (s *Service) syncPostgres(ctx context.Context) {
	err := func() error {
		if _, err := s.db.Exec(ctx, postgresPeerCopy); err != nil {
			return err
		}
		if _, err := s.db.Exec(ctx, ghostPeers); err != nil {
			return err
		}
		s.renamesPostgres(ctx)
		return nil
	}()
	if err != nil && !strings.Contains(err.Error(), "does not exist") {
		logging.Logf("DB", "WARN: syncGoPeers error: %v", err)
	}
}

func (s *Service) renamesPostgres(ctx context.Context) {
	err := s.cascadeRenames(ctx, `
                SELECT h.old_id, h.new_id FROM id_change_history h
                INNER JOIN peers p ON p.id = h.new_id AND NOT p.soft_deleted
                WHERE EXISTS (SELECT 1 FROM peer WHERE id = h.old_id)`)
	if err != nil && !strings.Contains(err.Error(), "does not exist") {
		logging.Logf("DB", "WARN: syncPanelPeerIdRenamesFromGo: %v", err)
	}
}

// cascadeRenames reads pairs of old and new ids and carries each change.
func (s *Service) cascadeRenames(ctx context.Context, query string) error {
	rows, err := s.db.Query(ctx, query)
	if err != nil {
		return err
	}
	type rename struct{ old, new string }
	var renames []rename
	for rows.Next() {
		var r rename
		if err := rows.Scan(&r.old, &r.new); err != nil {
			rows.Close()
			return err
		}
		renames = append(renames, r)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, r := range renames {
		s.CascadePeerIDChange(ctx, r.old, r.new)
	}
	return nil
}

// CascadePeerIDChange carries a changed device id into the console's
// tables: its row in peer, its system information and metrics, its
// folder, its access tokens and its audit rows. The device's manual
// device groups are moved by the API server's own cascade (package db). Each statement stands on
// its own: one that fails is logged (unless the table or column is not
// there) and the next one runs.
func (s *Service) CascadePeerIDChange(ctx context.Context, oldID, newID string) {
	if oldID == "" || newID == "" || oldID == newID {
		return
	}
	if _, err := s.db.Exec(ctx, `UPDATE peer SET id = ? WHERE id = ?`, newID, oldID); err != nil {
		logging.Logf("DB", "WARN: cascadePeerIdChange peer table: %v", err)
	}
	statements := []string{
		`UPDATE peer_sysinfo SET peer_id = ? WHERE peer_id = ?`,
		`UPDATE peer_metrics SET peer_id = ? WHERE peer_id = ?`,
		`UPDATE device_folder_assignments SET device_id = ? WHERE device_id = ?`,
		`UPDATE device_folder_assignments SET peer_id = ? WHERE peer_id = ?`,
		`UPDATE access_tokens SET client_id = ? WHERE client_id = ?`,
		`UPDATE audit_connections SET peer_id = ? WHERE peer_id = ?`,
		`UPDATE audit_files SET peer_id = ? WHERE peer_id = ?`,
		`UPDATE audit_alarms SET peer_id = ? WHERE peer_id = ?`,
	}
	missing := []string{"no such table", "no such column"}
	if s.db.IsPostgres() {
		// The PostgreSQL list has no second folder statement
		statements = append(statements[:3:3], statements[4:]...)
		missing = []string{"does not exist"}
	}
	for _, query := range statements {
		_, err := s.db.Exec(ctx, query, newID, oldID)
		if err == nil {
			continue
		}
		quiet := false
		for _, m := range missing {
			quiet = quiet || strings.Contains(err.Error(), m)
		}
		if !quiet {
			logging.Logf("DB", "WARN: cascadePeerIdChange: %v", err)
		}
	}
}

// CleanupStaleOnlinePeers sets the copies of devices offline that count as
// online in the console's table but whose last contact is more than
// thresholdSeconds ago, and returns how many it changed. The threshold is
// bound as a floating-point number, as the old console's driver bound
// every JavaScript number: on SQLite the time modifier reads "-90.0
// seconds".
//
// On SQLite, where the column is text, the last contact is compared as a
// time: in any form datetime() reads (with a space or a T, a date
// alone), digits alone as Unix seconds. Text that datetime() cannot read
// is compared as text.
func (s *Service) CleanupStaleOnlinePeers(ctx context.Context, thresholdSeconds float64) (int64, error) {
	query := s.db.Pick(`
                UPDATE peer SET status_online = 0
                WHERE status_online = 1
                  AND last_online IS NOT NULL
                  AND COALESCE(CASE WHEN last_online NOT GLOB '*[^0-9]*' THEN datetime(last_online, 'unixepoch') ELSE datetime(last_online) END, last_online)
                      < datetime('now', '-' || ? || ' seconds')`, `
                UPDATE peer SET status_online = FALSE
                WHERE status_online = TRUE
                  AND last_online IS NOT NULL
                  AND last_online < NOW() - INTERVAL '1 second' * ?`)
	res, err := s.db.Exec(ctx, query, thresholdSeconds)
	if err != nil {
		return 0, fmt.Errorf("set stale devices offline: %w", err)
	}
	return res.RowsAffected()
}

// PeerStats counts the devices of the console's table that are not
// deleted, after bringing it up to date: {total, online, banned,
// offline}.
func (s *Service) PeerStats(ctx context.Context) (*goapi.Object, error) {
	s.SyncServerPeers(ctx)
	query := s.db.Pick(
		`SELECT
			(SELECT COUNT(*) FROM peer WHERE is_deleted = 0),
			(SELECT COUNT(*) FROM peer WHERE is_deleted = 0 AND status_online = 1),
			(SELECT COUNT(*) FROM peer WHERE is_deleted = 0 AND is_banned = 1)`,
		`SELECT
			COUNT(*) FILTER (WHERE NOT is_deleted),
			COUNT(*) FILTER (WHERE NOT is_deleted AND status_online),
			COUNT(*) FILTER (WHERE NOT is_deleted AND is_banned)
		FROM peer`,
	)
	var total, online, banned int64
	if err := s.db.QueryRow(ctx, query).Scan(&total, &online, &banned); err != nil {
		return nil, fmt.Errorf("count the devices: %w", err)
	}
	return goapi.NewObject(
		"total", float64(total),
		"online", float64(online),
		"banned", float64(banned),
		"offline", float64(total-online),
	), nil
}

// AllPeers returns the devices of the console's table that are not
// deleted, ordered by id, after bringing it up to date, in the shape the
// old console gave them (parsePeer of the adapter).
func (s *Service) AllPeers(ctx context.Context) ([]*goapi.Object, error) {
	s.SyncServerPeers(ctx)
	rows, err := store.Select(ctx, s.db, `SELECT * FROM peer WHERE `+s.db.Pick(`is_deleted = 0`, `NOT is_deleted`)+` ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("load devices: %w", err)
	}
	defer rows.Close()
	types, err := rows.ColumnTypes()
	if err != nil {
		return nil, err
	}
	out := []*goapi.Object{}
	for rows.Next() {
		dest := make([]any, len(types))
		for i, t := range types {
			dest[i] = peerColumn(t.DatabaseTypeName())
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, fmt.Errorf("load devices: %w", err)
		}
		row := map[string]any{}
		for i, t := range types {
			row[t.Name()] = dest[i]
		}
		peer, err := s.parsePeer(row)
		if err != nil {
			return nil, fmt.Errorf("load devices: %w", err)
		}
		out = append(out, peer)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("load devices: %w", err)
	}
	return out, nil
}

// peerColumn is what a column of peer is scanned into: times as the old
// console's driver gave them, JSON columns as their text, the rest as the
// Go driver gives it.
func peerColumn(typeName string) any {
	switch strings.ToUpper(typeName) {
	case "TIMESTAMPTZ", "TIMESTAMP", "DATE", "DATETIME":
		return new(store.Time)
	case "JSON", "JSONB":
		return new(sql.NullString)
	}
	return new(any)
}

// cell is a scanned column of peer as a plain JSON value: nil, bool,
// float64, string, a store.Time, or []byte for a blob.
func cell(row map[string]any, name string) any {
	switch v := row[name].(type) {
	case *store.Time:
		if v.IsNull() {
			return nil
		}
		return *v
	case *sql.NullString:
		if !v.Valid {
			return nil
		}
		parsed, err := goapi.Parse([]byte(v.String))
		if err != nil {
			return nil
		}
		return jsonColumn{parsed}
	case *any:
		switch x := (*v).(type) {
		case int64:
			return float64(x)
		case []byte:
			return append([]byte(nil), x...)
		default:
			return x
		}
	}
	return nil
}

// jsonColumn is a JSON column's value as node-postgres parsed it.
type jsonColumn struct{ value any }

// buffer is a blob as JSON.stringify writes a Node Buffer.
func buffer(b []byte) *goapi.Object {
	data := make([]any, len(b))
	for i, c := range b {
		data[i] = float64(c)
	}
	return goapi.NewObject("type", "Buffer", "data", data)
}

// parsePeer is the old console's parsePeer: a row of peer in the shape
// its lists used. The SQLite adapter read info as JSON text, counted a
// device online and banned only for the number 1 and split the tags; the
// PostgreSQL adapter took info as parsed JSON and the flags as truthy, and
// its table has no tags.
func (s *Service) parsePeer(row map[string]any) (*goapi.Object, error) {
	postgres := s.db.IsPostgres()
	// The value of info: text on SQLite, the parsed JSON on PostgreSQL
	raw := cell(row, "info")
	if v, ok := raw.(jsonColumn); ok {
		raw = v.value
	}
	var info any = goapi.NewObject()
	switch v := raw.(type) {
	case *goapi.Object, []any:
		if v != nil {
			info = v
		}
	case string:
		if v != "" {
			if parsed, err := goapi.Parse([]byte(v)); err == nil {
				info = parsed
			}
		}
	case float64, bool:
		if !postgres && jsTruthy(v) {
			info = v
		}
	}
	// The info the list carries: on PostgreSQL an object (and null) as JSON
	// text, anything else as it is
	infoValue := raw
	if postgres {
		switch raw.(type) {
		case *goapi.Object, []any, nil:
			text, err := json.Marshal(raw)
			if err != nil {
				return nil, err
			}
			infoValue = string(text)
		}
	}
	if info == nil {
		return nil, errors.New("Cannot read properties of null (reading 'hostname')")
	}
	member := func(name string) any {
		if o, ok := info.(*goapi.Object); ok {
			return o.Get(name)
		}
		return nil
	}
	or := func(values ...any) any {
		for _, v := range values[:len(values)-1] {
			if jsTruthy(v) {
				return v
			}
		}
		return values[len(values)-1]
	}
	flag := func(name string) bool {
		v := cell(row, name)
		if postgres {
			return jsTruthy(v)
		}
		return v == float64(1)
	}
	pk := cell(row, "pk")
	if b, ok := pk.([]byte); ok {
		pk = buffer(b)
	}
	user, _ := cell(row, "user").(string)
	tags := []any{}
	if text, ok := cell(row, "tags").(string); ok && text != "" {
		for _, tag := range strings.Split(text, ",") {
			if tag != "" {
				tags = append(tags, tag)
			}
		}
	}
	return goapi.NewObject(
		"id", cell(row, "id"),
		"uuid", or(cell(row, "uuid"), ""),
		"pk", or(pk, nil),
		"hostname", or(cell(row, "note"), member("hostname"), ""),
		"username", user,
		"platform", or(member("os"), member("platform"), ""),
		"ip", or(member("ip"), cell(row, "ip"), ""),
		"note", or(cell(row, "note"), ""),
		"online", flag("status_online"),
		"banned", flag("is_banned"),
		"created_at", cell(row, "created_at"),
		"last_online", cell(row, "last_online"),
		"ban_reason", or(cell(row, "banned_reason"), ""),
		"folder_id", or(cell(row, "folder_id"), nil),
		"info", or(infoValue, ""),
		"tags", tags,
	), nil
}
