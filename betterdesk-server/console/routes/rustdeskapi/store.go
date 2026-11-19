package rustdeskapi

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/jsbind"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/store"
)

// The dbAdapter methods of the client API whose tables no service package
// holds: the console's device table and its copy from the signal server's,
// system information and metrics, the client audit records, strategies.
// Each sends the statement of its adapter; where the two adapters
// differed, both are kept. Values from a request are bound the way the
// old drivers bound them (package jsbind).

type clientStore struct {
	db  *store.DB
	log *kit.Logger
	now func() time.Time

	// The copy of the signal server's devices into peer runs at most every
	// 30 seconds (syncGoPeers).
	syncMu   sync.Mutex
	lastSync time.Time
}

const peerSyncInterval = 30 * time.Second

// args binds request values for a statement with n placeholders.
func (s *clientStore) args(n int, values ...jsbind.Value) ([]any, error) {
	return jsbind.Args(s.db.Dialect(), n, values...)
}

// boundText is the text a TEXT column holds for a statement parameter the
// old driver bound: text as it is, NULL as "", and on SQLite a number in
// SQLite's own form of a REAL ("5.0").
func (s *clientStore) boundText(ctx context.Context, arg any) (string, error) {
	switch v := arg.(type) {
	case nil:
		return "", nil
	case string:
		return v, nil
	case float64:
		var text string
		if err := s.db.QueryRow(ctx, `SELECT CAST(? AS TEXT)`, v).Scan(&text); err != nil {
			return "", fmt.Errorf("the text of a number: %w", err)
		}
		return text, nil
	}
	return fmt.Sprint(arg), nil
}

// ---- Rows ----

// readRows runs a query and returns its rows as the old drivers gave them:
// the columns in order, times and big integers as their value types.
func readRows(ctx context.Context, q store.Querier, query string, args ...any) ([]*goapi.Object, error) {
	rows, err := store.Select(ctx, q, query, args...)
	if err != nil {
		return nil, err
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
			dest[i] = columnValue(q.Dialect(), t.DatabaseTypeName())
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, err
		}
		row := goapi.NewObject()
		for i, t := range types {
			row.Set(t.Name(), deref(dest[i]))
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func readRow(ctx context.Context, q store.Querier, query string, args ...any) (*goapi.Object, error) {
	rows, err := readRows(ctx, q, query, args...)
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	return rows[0], nil
}

func columnValue(dialect store.Dialect, typeName string) any {
	switch strings.ToUpper(typeName) {
	case "TIMESTAMPTZ", "TIMESTAMP", "DATE", "DATETIME":
		return new(store.Time)
	case "INT8", "NUMERIC":
		if dialect == store.Postgres {
			return new(store.Int64String)
		}
	case "JSON", "JSONB":
		if dialect == store.Postgres {
			return new(store.JSONText)
		}
	}
	return new(jsbind.Cell)
}

func deref(v any) any {
	switch x := v.(type) {
	case *store.Time:
		return *x
	case *store.Int64String:
		return *x
	case *store.JSONText:
		return *x
	case *jsbind.Cell:
		return *x
	}
	return v
}

// plain is a row value as JavaScript held it: nil, bool, float64, string,
// []any or *goapi.Object.
func plain(v any) any {
	switch x := v.(type) {
	case nil, bool, float64, string, []any, *goapi.Object:
		return v
	case interface{ MarshalJSON() ([]byte, error) }:
		raw, err := x.MarshalJSON()
		if err != nil {
			return nil
		}
		parsed, err := goapi.Parse(raw)
		if err != nil {
			return nil
		}
		return parsed
	}
	return v
}

// ---- Devices (the console's peer table) ----

// peerRow is what the routes read of parsePeer(row).
type peerRow struct {
	id     any
	pk     any
	banned bool
}

// peerByID is getPeerById: the device of the console's table, and when it
// is not there the signal server's device of that id, copied into the
// console's table first. A failure of the copy is logged and ignored (a
// missing peers table quietly).
func (s *clientStore) peerByID(ctx context.Context, id string) (*peerRow, error) {
	query := s.db.Pick(`SELECT id, pk, is_banned FROM peer WHERE id = ? AND is_deleted = 0`, `SELECT id, pk, is_banned FROM peer WHERE id = ? AND NOT is_deleted`)
	row, err := s.readPeer(ctx, query, id)
	if err != nil || row != nil {
		return row, err
	}
	if err := s.copyPeer(ctx, id); err != nil {
		missing := "no such table"
		if s.db.IsPostgres() {
			missing = "does not exist"
		}
		if !strings.Contains(jsbind.Message(err), missing) {
			s.log.Warn("[DB] Fallback peers lookup error:", jsbind.Message(err))
		}
		return nil, nil
	}
	return s.readPeer(ctx, query, id)
}

func (s *clientStore) readPeer(ctx context.Context, query, id string) (*peerRow, error) {
	var p peerRow
	var banned any
	err := s.db.QueryRow(ctx, query, id).Scan(&p.id, &p.pk, &banned)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load device %q: %w", id, err)
	}
	if s.db.IsPostgres() {
		p.banned = goapi.Truthy(driverValue(banned))
	} else {
		p.banned = driverValue(banned) == float64(1)
	}
	return &p, nil
}

// driverValue is a scanned value as JavaScript had it from the driver.
func driverValue(v any) any {
	switch x := v.(type) {
	case int64:
		return float64(x)
	case int32:
		return float64(x)
	case []byte:
		return x
	}
	return v
}

// jsTruthyDriver is !!value for a value the driver returned; a Buffer is
// truthy even when empty.
func jsTruthyDriver(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case []byte:
		return true
	case time.Time:
		return true
	case int64:
		return x != 0
	case float64:
		return x != 0 && !math.IsNaN(x)
	case string:
		return x != ""
	case bool:
		return x
	}
	return true
}

func orDriver(v, fallback any) any {
	if jsTruthyDriver(v) {
		return v
	}
	return fallback
}

// copyPeer is the fallback of getPeerById: the signal server's device of
// the id, if it has one that is not deleted, into the console's table.
func (s *clientStore) copyPeer(ctx context.Context, id string) error {
	rows, err := s.db.Query(ctx, s.db.Pick(
		`SELECT id, uuid, pk, hostname, os, version, ip, "user", status, last_online, created_at, banned, banned_at, ban_reason, tags FROM peers WHERE id = ? AND NOT soft_deleted`,
		`SELECT id, uuid, pk, hostname, os, version, ip, "user", status, last_online, created_at, banned, banned_at, ban_reason FROM peers WHERE id = ? AND NOT soft_deleted`,
	), id)
	if err != nil {
		return err
	}
	var g struct {
		id, uuid, pk, hostname, os, version, ip, user, status, lastOnline, createdAt, banned, bannedAt, banReason, tags any
	}
	found := rows.Next()
	if found {
		dest := []any{&g.id, &g.uuid, &g.pk, &g.hostname, &g.os, &g.version, &g.ip, &g.user, &g.status, &g.lastOnline, &g.createdAt, &g.banned, &g.bannedAt, &g.banReason}
		if !s.db.IsPostgres() {
			dest = append(dest, &g.tags)
		}
		err = rows.Scan(dest...)
	}
	if closeErr := rows.Close(); err == nil {
		err = closeErr
	}
	if err != nil || !found {
		return err
	}
	info := goapi.NewObject(
		"hostname", plain(driverValue(orDriver(g.hostname, ""))),
		"os", plain(driverValue(orDriver(g.os, ""))),
		"platform", plain(driverValue(orDriver(g.os, ""))),
		"version", plain(driverValue(orDriver(g.version, ""))),
	)
	online := asText(g.status) == "ONLINE"
	if s.db.IsPostgres() {
		// The parameters are numbered as in Node, so each one has a single
		// type where it comes twice
		_, err = s.db.Exec(ctx, `
                            INSERT INTO peer (id, uuid, pk, info, ip, "user", status_online, last_online, created_at, is_deleted, is_banned, banned_at, banned_reason)
                            VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, FALSE, $10, $11, $12)
                            ON CONFLICT(id) DO UPDATE SET
                                uuid = COALESCE(NULLIF($2, ''), peer.uuid),
                                pk = COALESCE($3, peer.pk),
                                info = COALESCE(NULLIF($4, '{}'), peer.info),
                                ip = COALESCE(NULLIF($5, ''), peer.ip),
                                "user" = COALESCE(NULLIF($6, ''), peer."user"),
                                status_online = $7,
                                last_online = $8,
                                is_deleted = FALSE
                        `, g.id, orDriver(g.uuid, ""), orDriver(g.pk, nil), stringify(info), orDriver(g.ip, ""), orDriver(g.user, ""),
			online, orDriver(g.lastOnline, nil), orDriver(g.createdAt, s.now()), jsTruthyDriver(g.banned), orDriver(g.bannedAt, nil), orDriver(g.banReason, ""))
		return err
	}
	onlineFlag, bannedFlag := 0, 0
	if online {
		onlineFlag = 1
	}
	if jsTruthyDriver(g.banned) {
		bannedFlag = 1
	}
	_, err = s.db.Exec(ctx, `
                            INSERT INTO peer (id, uuid, pk, info, ip, "user", status_online, last_online, created_at, is_deleted, is_banned, banned_at, banned_reason, tags)
                            VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?, ?, ?)
                            ON CONFLICT(id) DO UPDATE SET
                                uuid = COALESCE(NULLIF(excluded.uuid, ''), uuid),
                                pk = COALESCE(excluded.pk, pk),
                                info = COALESCE(NULLIF(excluded.info, '{}'), info),
                                ip = COALESCE(NULLIF(excluded.ip, ''), ip),
                                "user" = COALESCE(NULLIF(excluded."user", ''), "user"),
                                status_online = excluded.status_online,
                                last_online = COALESCE(excluded.last_online, last_online),
                                is_deleted = 0,
                                tags = COALESCE(NULLIF(excluded.tags, ''), tags)
                        `, g.id, orDriver(g.uuid, ""), orDriver(g.pk, nil), stringify(info), orDriver(g.ip, ""), orDriver(g.user, ""),
		onlineFlag, orDriver(g.lastOnline, nil), orDriver(g.createdAt, store.ISOTime(s.now())), bannedFlag, orDriver(g.bannedAt, nil), orDriver(g.banReason, ""), orDriver(g.tags, ""))
	return err
}

func asText(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case []byte:
		return string(x)
	}
	return ""
}

// The texts parsePeer threw on a row (BUGS RP4).
var (
	errNullInfo    = errors.New("Cannot read properties of null (reading 'hostname')")
	errTagsNotText = errors.New("row.tags.split is not a function")
)

// listedPeer is what GET /api/devices reads of a device of getAllPeers().
type listedPeer struct {
	id   any
	uuid any
}

// allPeers is getAllPeers({}): after the copy of the signal server's
// devices, the devices of the console's table that are not deleted, by
// id. A row parsePeer threw on fails the whole call.
func (s *clientStore) allPeers(ctx context.Context) ([]listedPeer, error) {
	s.syncGoPeers(ctx)
	rows, err := s.db.Query(ctx, s.db.Pick(
		`SELECT id, uuid, info, tags FROM peer WHERE is_deleted = 0 ORDER BY id`,
		`SELECT id, uuid, info, NULL FROM peer WHERE NOT is_deleted ORDER BY id`,
	))
	if err != nil {
		return nil, fmt.Errorf("load the devices: %w", err)
	}
	defer rows.Close()
	out := []listedPeer{}
	for rows.Next() {
		var p listedPeer
		var info, tags any
		if err := rows.Scan(&p.id, &p.uuid, &info, &tags); err != nil {
			return nil, fmt.Errorf("load the devices: %w", err)
		}
		if err := s.checkParsePeer(info, tags); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// checkParsePeer fails where parsePeer threw: info that is the JSON null,
// and on SQLite tags that are no text.
func (s *clientStore) checkParsePeer(info, tags any) error {
	if s.db.IsPostgres() {
		raw, ok := info.([]byte)
		if !ok {
			if text, isText := info.(string); isText {
				raw = []byte(text)
			} else {
				return nil
			}
		}
		parsed, err := goapi.Parse(raw)
		if err != nil {
			return nil
		}
		// jsonb holding a string is parsed a second time
		if inner, isText := parsed.(string); isText {
			again, err := goapi.Parse([]byte(inner))
			if err == nil && again == nil {
				return errNullInfo
			}
		}
		return nil
	}
	if jsTruthyDriver(info) {
		if parsed, err := goapi.Parse([]byte(asText(info))); err == nil && parsed == nil {
			return errNullInfo
		}
	}
	if jsTruthyDriver(tags) {
		if _, isText := tags.(string); !isText {
			return errTagsNotText
		}
	}
	return nil
}

// syncGoPeers copies the signal server's devices into the console's peer
// table, removes the old ids of renamed devices and carries renames over
// to the console's tables; at most every 30 seconds. Failures are logged
// (a missing table quietly) and never returned.
func (s *clientStore) syncGoPeers(ctx context.Context) {
	s.syncMu.Lock()
	now := s.now()
	if now.Sub(s.lastSync) < peerSyncInterval {
		s.syncMu.Unlock()
		return
	}
	s.lastSync = now
	s.syncMu.Unlock()
	missing := "no such table"
	if s.db.IsPostgres() {
		missing = "does not exist"
	}
	if err := s.copyGoPeers(ctx); err != nil && !strings.Contains(jsbind.Message(err), missing) {
		s.log.Warn("[DB] syncGoPeers error:", jsbind.Message(err))
	}
}

func (s *clientStore) tableExists(ctx context.Context, name string) (bool, error) {
	var found string
	err := s.db.QueryRow(ctx, `SELECT name FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func (s *clientStore) copyGoPeers(ctx context.Context) error {
	if s.db.IsPostgres() {
		if _, err := s.db.Exec(ctx, `
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
                    is_deleted    = FALSE
            `); err != nil {
			return err
		}
		if _, err := s.db.Exec(ctx, `
                DELETE FROM peer WHERE id IN (
                    SELECT h.old_id FROM id_change_history h
                    LEFT JOIN peers p ON p.id = h.old_id AND NOT p.soft_deleted
                    WHERE p.id IS NULL
                )
            `); err != nil {
			return err
		}
		s.carryRenames(ctx, `
                SELECT h.old_id, h.new_id FROM id_change_history h
                INNER JOIN peers p ON p.id = h.new_id AND NOT p.soft_deleted
                WHERE EXISTS (SELECT 1 FROM peer WHERE id = h.old_id)
            `)
		return nil
	}
	if ok, err := s.tableExists(ctx, "peers"); err != nil || !ok {
		return err
	}
	if _, err := s.db.Exec(ctx, `
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
                    tags          = COALESCE(NULLIF(excluded.tags, ''), tags)
            `); err != nil {
		return err
	}
	history, err := s.tableExists(ctx, "id_change_history")
	if err != nil || !history {
		return err
	}
	result, err := s.db.Exec(ctx, `
                    DELETE FROM peer WHERE id IN (
                        SELECT h.old_id FROM id_change_history h
                        LEFT JOIN peers p ON p.id = h.old_id AND NOT p.soft_deleted
                        WHERE p.id IS NULL
                    )
                `)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n > 0 {
		s.log.Info(fmt.Sprintf("[DB] syncGoPeersSqlite: cleaned up %d ghost peer(s) from ID changes", n))
	}
	s.carryRenames(ctx, `
                SELECT h.old_id, h.new_id FROM id_change_history h
                INNER JOIN peers p ON p.id = h.new_id AND (p.soft_deleted IS NULL OR p.soft_deleted = 0)
                WHERE EXISTS (SELECT 1 FROM peer WHERE id = h.old_id)
            `)
	return nil
}

// carryRenames is syncPanelPeerIdRenamesFromGo: every rename of the
// signal server whose old id the console's table still has moves to the
// new id in the console's tables.
func (s *clientStore) carryRenames(ctx context.Context, query string) {
	missing := []string{"no such table"}
	if s.db.IsPostgres() {
		missing = []string{"does not exist"}
	}
	quiet := func(err error, also ...string) bool {
		msg := jsbind.Message(err)
		for _, m := range append(missing, also...) {
			if strings.Contains(msg, m) {
				return true
			}
		}
		return false
	}
	rows, err := s.db.Query(ctx, query)
	if err != nil {
		if !quiet(err) {
			s.log.Warn("[DB] syncPanelPeerIdRenamesFromGo:", jsbind.Message(err))
		}
		return
	}
	type rename struct{ from, to any }
	var renames []rename
	for rows.Next() {
		var r rename
		if err := rows.Scan(&r.from, &r.to); err != nil {
			break
		}
		renames = append(renames, r)
	}
	_ = rows.Close()
	statements := []string{
		`UPDATE peer_sysinfo SET peer_id = ? WHERE peer_id = ?`,
		`UPDATE peer_metrics SET peer_id = ? WHERE peer_id = ?`,
		`UPDATE device_folder_assignments SET device_id = ? WHERE device_id = ?`,
	}
	if !s.db.IsPostgres() {
		statements = append(statements, `UPDATE device_folder_assignments SET peer_id = ? WHERE peer_id = ?`)
	}
	statements = append(statements,
		`UPDATE device_group_peers SET peer_id = ? WHERE peer_id = ?`,
		`UPDATE access_tokens SET client_id = ? WHERE client_id = ?`,
		`UPDATE audit_connections SET peer_id = ? WHERE peer_id = ?`,
		`UPDATE audit_files SET peer_id = ? WHERE peer_id = ?`,
		`UPDATE audit_alarms SET peer_id = ? WHERE peer_id = ?`,
	)
	for _, r := range renames {
		if !jsTruthyDriver(r.from) || !jsTruthyDriver(r.to) || asText(r.from) == asText(r.to) {
			continue
		}
		if _, err := s.db.Exec(ctx, `UPDATE peer SET id = ? WHERE id = ?`, r.to, r.from); err != nil {
			s.log.Warn("[DB] cascadePeerIdChange peer table:", jsbind.Message(err))
		}
		for _, statement := range statements {
			if _, err := s.db.Exec(ctx, statement, r.to, r.from); err != nil && !quiet(err, "no such column") {
				s.log.Warn("[DB] cascadePeerIdChange:", jsbind.Message(err))
			}
		}
	}
}

// resolvePeerAssignmentKey is resolvePeerAssignmentKey: the guid of the
// signal server's device whose id, guid or UUID is ref (trimmed). A
// device without a guid gets its UUID in lower case when that has the
// form of one, else a random one. Node added the guid column to peers when
// it was missing; the column has been part of peers for long, and the
// console does not change the signal server's tables, so without it the
// lookup fails.
func (s *clientStore) resolvePeerAssignmentKey(ctx context.Context, ref string) (string, error) {
	key := jsTrim(ref)
	if key == "" {
		return "", errors.New("empty peer reference")
	}
	var id string
	err := s.db.QueryRow(ctx, `
                SELECT id FROM peers
                WHERE id = ? OR guid = ? OR uuid = ?
                LIMIT 1
            `, key, key, key).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", errors.New("peer not found")
	}
	if err != nil {
		return "", err
	}
	var uuid, guid string
	err = s.db.QueryRow(ctx, `SELECT COALESCE(uuid, '') AS uuid, COALESCE(guid, '') AS guid FROM peers WHERE id = ?`, id).Scan(&uuid, &guid)
	if errors.Is(err, sql.ErrNoRows) {
		return "", errors.New("peer not found")
	}
	if err != nil {
		return "", err
	}
	if guid != "" {
		return guid, nil
	}
	if utf16Len(uuid) == 36 && strings.Count(uuid, "-") == 4 {
		guid = jsbind.ToLower(uuid)
	} else if guid, err = randomUUID(); err != nil {
		return "", err
	}
	if _, err := s.db.Exec(ctx, `UPDATE peers SET guid = ? WHERE id = ?`, guid, id); err != nil {
		return "", err
	}
	return guid, nil
}

// randomUUID is crypto.randomUUID.
func randomUUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("make a guid: %w", err)
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

// ---- Folder assignments ----

// folderAssignment is one entry of getAllFolderAssignments(): the folder
// as Number() made it, or the stored value when that is no number.
type folderAssignment struct {
	deviceID string
	folderID any
}

// folderAssignments is getAllFolderAssignments() in the order
// Object.entries lists its keys: ids that are array indexes first, by
// value, then the others in the order of the rows. A later row of the
// same device replaces the folder and keeps the place.
func (s *clientStore) folderAssignments(ctx context.Context) ([]folderAssignment, error) {
	rows, err := s.db.Query(ctx, `SELECT device_id, folder_id FROM device_folder_assignments`)
	if err != nil {
		return nil, fmt.Errorf("load folder assignments: %w", err)
	}
	defer rows.Close()
	at := map[string]int{}
	var out []folderAssignment
	for rows.Next() {
		var device, folder any
		if err := rows.Scan(&device, &folder); err != nil {
			return nil, fmt.Errorf("load folder assignments: %w", err)
		}
		key := strOr(plain(driverValue(device)))
		value := plain(driverValue(folder))
		if n := numberOf(value); isFinite(n) {
			value = n
		}
		if i, ok := at[key]; ok {
			out[i].folderID = value
			continue
		}
		at[key] = len(out)
		out = append(out, folderAssignment{deviceID: key, folderID: value})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sortObjectKeys(out)
	return out, nil
}

// numberOf is Number(v).
func numberOf(v any) float64 {
	switch x := v.(type) {
	case nil:
		return 0
	case bool:
		if x {
			return 1
		}
		return 0
	case float64:
		return x
	case string:
		return jsbind.ParseNumber(x)
	}
	return math.NaN()
}

var arrayIndexKey = regexp.MustCompile(`^(0|[1-9][0-9]*)$`)

func arrayIndex(key string) (uint64, bool) {
	if !arrayIndexKey.MatchString(key) {
		return 0, false
	}
	n, err := strconv.ParseUint(key, 10, 64)
	if err != nil || n >= 1<<32-1 {
		return 0, false
	}
	return n, true
}

// sortObjectKeys puts the entries in the order a JavaScript object keeps
// its keys.
func sortObjectKeys(entries []folderAssignment) {
	var indexes, others []folderAssignment
	for _, e := range entries {
		if _, ok := arrayIndex(e.deviceID); ok {
			indexes = append(indexes, e)
		} else {
			others = append(others, e)
		}
	}
	for i := 1; i < len(indexes); i++ {
		for j := i; j > 0; j-- {
			a, _ := arrayIndex(indexes[j-1].deviceID)
			b, _ := arrayIndex(indexes[j].deviceID)
			if a <= b {
				break
			}
			indexes[j-1], indexes[j] = indexes[j], indexes[j-1]
		}
	}
	copy(entries, append(indexes, others...))
}

// ---- System information and metrics ----

// sysinfoUpdatedAt is getPeerSysinfo(id).updated_at as new Date() read it;
// found is false when the device has no row, ok is false for a time that
// does not read (NaN).
func (s *clientStore) sysinfoUpdatedAt(ctx context.Context, id string) (at time.Time, found, ok bool, err error) {
	var updated store.Time
	err = store.SelectRow(ctx, s.db, `SELECT updated_at FROM peer_sysinfo WHERE peer_id = ?`, id).Scan(&updated)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, false, false, nil
	}
	if err != nil {
		return time.Time{}, false, false, fmt.Errorf("load the system information of %q: %w", id, err)
	}
	if s.db.IsPostgres() {
		at, ok = updated.Instant()
		return at, true, ok, nil
	}
	at, ok = parseDate(updated.String())
	return at, true, ok, nil
}

// sysinfoRaw is the stored system information of a device as one JSON
// text (raw_json); found is false when the device has none.
func (s *clientStore) sysinfoRaw(ctx context.Context, id string) (raw string, found bool, err error) {
	var text sql.NullString
	err = store.SelectRow(ctx, s.db, `SELECT raw_json FROM peer_sysinfo WHERE peer_id = ?`, id).Scan(&text)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("load the system information of %q: %w", id, err)
	}
	return text.String, text.Valid, nil
}

var (
	dateOnly   = regexp.MustCompile(`^(\d{4})-(\d{2})-(\d{2})$`)
	dateTimeJS = regexp.MustCompile(`^(\d{4})-(\d{2})-(\d{2})[T ](\d{2}):(\d{2})(?::(\d{2})(?:\.(\d{1,3})\d*)?)?(Z|[+-]\d{2}:\d{2})?$`)
)

// parseDate is new Date(text) for the forms the column holds: a date alone
// is UTC midnight, a date and time with Z or an offset is that instant,
// one without is local time (so "2026-11-10 01:02:03" from datetime('now'),
// which is UTC, is read as the process's local time).
func parseDate(text string) (time.Time, bool) {
	if m := dateOnly.FindStringSubmatch(text); m != nil {
		t, err := time.Parse("2006-01-02", text)
		return t, err == nil
	}
	m := dateTimeJS.FindStringSubmatch(text)
	if m == nil {
		return time.Time{}, false
	}
	num := func(s string) int {
		n, _ := strconv.Atoi(s)
		return n
	}
	ms := 0
	if m[7] != "" {
		ms = num((m[7] + "00")[:3])
	}
	loc := time.Local
	switch zone := m[8]; {
	case zone == "Z":
		loc = time.UTC
	case zone != "":
		sign := 1
		if zone[0] == '-' {
			sign = -1
		}
		loc = time.FixedZone("", sign*(num(zone[1:3])*3600+num(zone[4:6])*60))
	}
	t := time.Date(num(m[1]), time.Month(num(m[2])), num(m[3]), num(m[4]), num(m[5]), num(m[6]), ms*int(time.Millisecond), loc)
	if t.Month() != time.Month(num(m[2])) || t.Day() != num(m[3]) || num(m[4]) > 24 || num(m[5]) > 59 || num(m[6]) > 59 {
		return time.Time{}, false
	}
	return t, true
}

// sysinfo is the system information a client reported, in the order of
// the route's object.
type sysinfo struct {
	hostname, username, platform, version, cpuName string
	cpuCores, cpuFreqGHz, memoryGB                 float64
	osFull                                         string
	displays, encoding                             []any
	features, platformAdditions                    any
}

// object is the sysinfo object as JSON.stringify wrote it into raw_json.
func (si sysinfo) object() *goapi.Object {
	return goapi.NewObject(
		"hostname", si.hostname, "username", si.username, "platform", si.platform, "version", si.version,
		"cpu_name", si.cpuName, "cpu_cores", si.cpuCores, "cpu_freq_ghz", si.cpuFreqGHz, "memory_gb", si.memoryGB,
		"os_full", si.osFull, "displays", si.displays, "encoding", si.encoding,
		"features", si.features, "platform_additions", si.platformAdditions,
	)
}

// upsertSysinfo is upsertPeerSysinfo.
func (s *clientStore) upsertSysinfo(ctx context.Context, id string, si sysinfo) error {
	args, err := s.args(15,
		jsbind.String(id), jsbind.String(si.hostname), jsbind.String(si.username), jsbind.String(si.platform), jsbind.String(si.version),
		jsbind.String(si.cpuName), jsbind.Number(si.cpuCores), jsbind.Number(si.cpuFreqGHz), jsbind.Number(si.memoryGB),
		jsbind.String(si.osFull),
		jsbind.String(stringify(si.displays)), jsbind.String(stringify(si.encoding)),
		jsbind.String(stringify(si.features)), jsbind.String(stringify(si.platformAdditions)),
		jsbind.String(stringify(si.object())),
	)
	if err != nil {
		return err
	}
	now := store.NowSQL(s.db.Dialect())
	excluded := s.db.Pick("excluded", "EXCLUDED")
	_, err = s.db.Exec(ctx, `
                INSERT INTO peer_sysinfo (peer_id, hostname, username, platform, version,
                    cpu_name, cpu_cores, cpu_freq_ghz, memory_gb, os_full,
                    displays, encoding, features, platform_additions, raw_json, updated_at)
                VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, `+now+`)
                ON CONFLICT(peer_id) DO UPDATE SET
                    hostname = `+excluded+`.hostname, username = `+excluded+`.username,
                    platform = `+excluded+`.platform, version = `+excluded+`.version,
                    cpu_name = `+excluded+`.cpu_name, cpu_cores = `+excluded+`.cpu_cores,
                    cpu_freq_ghz = `+excluded+`.cpu_freq_ghz, memory_gb = `+excluded+`.memory_gb,
                    os_full = `+excluded+`.os_full, displays = `+excluded+`.displays,
                    encoding = `+excluded+`.encoding, features = `+excluded+`.features,
                    platform_additions = `+excluded+`.platform_additions,
                    raw_json = `+excluded+`.raw_json, updated_at = `+now+`
            `, args...)
	return err
}

// sysinfoSummary is what the device list reads of a system information
// row.
type sysinfoSummary struct {
	hostname, username, platform, version any
}

// allSysinfo is getAllPeerSysinfo() by peer id.
func (s *clientStore) allSysinfo(ctx context.Context) (map[string]sysinfoSummary, error) {
	rows, err := s.db.Query(ctx, `SELECT peer_id, hostname, username, platform, version FROM peer_sysinfo`)
	if err != nil {
		return nil, fmt.Errorf("load the system information: %w", err)
	}
	defer rows.Close()
	out := map[string]sysinfoSummary{}
	for rows.Next() {
		var id any
		var si sysinfoSummary
		if err := rows.Scan(&id, &si.hostname, &si.username, &si.platform, &si.version); err != nil {
			return nil, fmt.Errorf("load the system information: %w", err)
		}
		si.hostname, si.username = plain(driverValue(si.hostname)), plain(driverValue(si.username))
		si.platform, si.version = plain(driverValue(si.platform)), plain(driverValue(si.version))
		out[strOr(plain(driverValue(id)))] = si
	}
	return out, rows.Err()
}

// insertMetric is insertPeerMetric.
func (s *clientStore) insertMetric(ctx context.Context, id string, cpu, memory, disk float64) error {
	args, err := s.args(4, jsbind.String(id), jsbind.Number(cpu), jsbind.Number(memory), jsbind.Number(disk))
	if err != nil {
		return err
	}
	_, err = s.db.Exec(ctx, `INSERT INTO peer_metrics (peer_id, cpu_usage, memory_usage, disk_usage) VALUES (?, ?, ?, ?)`, args...)
	return err
}

// markOnline is updatePeerOnlineStatus.
func (s *clientStore) markOnline(ctx context.Context, id string) error {
	_, err := s.db.Exec(ctx, s.db.Pick(
		`UPDATE peer SET status_online = 1, last_online = datetime('now') WHERE id = ?`,
		`UPDATE peer SET status_online = TRUE, last_online = NOW() WHERE id = ?`), id)
	return err
}

// ---- Client audit records ----

func (s *clientStore) exec(ctx context.Context, query string, n int, values ...jsbind.Value) error {
	args, err := s.args(n, values...)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(ctx, query, args...)
	return err
}

// auditFilter is one condition of an audit list: a column and the value
// of the query as the route took it.
type auditFilter struct {
	column string
	value  jsbind.Value
	// always adds the condition when the value is not undefined (the alarm
	// type); the others only when it is truthy.
	always bool
}

// auditRows is getAuditConnections, getAuditFiles or getAuditAlarms:
// the rows of the table under the conditions, newest first.
func (s *clientStore) auditRows(ctx context.Context, table string, filters []auditFilter, limit, offset float64) ([]*goapi.Object, error) {
	where, values := s.auditWhere(filters)
	values = append(values, jsbind.Number(limit), jsbind.Number(offset))
	args, err := s.args(strings.Count(where, "?")+2, values...)
	if err != nil {
		return nil, err
	}
	return readRows(ctx, s.db, `SELECT * FROM `+table+` WHERE 1=1`+where+` ORDER BY created_at DESC LIMIT ? OFFSET ?`, args...)
}

// auditCount counts the rows of the table under the conditions.
func (s *clientStore) auditCount(ctx context.Context, table string, filters []auditFilter) (float64, error) {
	where, values := s.auditWhere(filters)
	args, err := s.args(strings.Count(where, "?"), values...)
	if err != nil {
		return 0, err
	}
	var n int64
	err = s.db.QueryRow(ctx, s.db.Pick(`SELECT COUNT(*) as count FROM `, `SELECT COUNT(*)::INTEGER AS count FROM `)+table+` WHERE 1=1`+where, args...).Scan(&n)
	return float64(n), err
}

func (s *clientStore) auditWhere(filters []auditFilter) (string, []jsbind.Value) {
	var where strings.Builder
	var values []jsbind.Value
	for _, f := range filters {
		if (f.always && !f.value.IsUndefined()) || (!f.always && f.value.Truthy()) {
			where.WriteString(" AND " + f.column + " = ?")
			values = append(values, f.value)
		}
	}
	return where.String(), values
}

// ---- Strategies ----

// parsePermissions is safeJsonParse(permissions, {}) as getAllStrategies
// and getStrategyByGuid applied it: on PostgreSQL a jsonb object, list or
// null stays as it is (typeof 'object').
func (s *clientStore) parsePermissions(row *goapi.Object) {
	raw := row.Get("permissions")
	if text, ok := raw.(store.JSONText); ok {
		value := plain(text)
		switch value.(type) {
		case *goapi.Object, []any, nil:
			return
		}
		row.Set("permissions", safeJSONParse(value))
		return
	}
	row.Set("permissions", safeJSONParse(plain(raw)))
}

// safeJSONParse is safeJsonParse(v, {}): JSON.parse(String(v)), {} when
// it throws; JSON.parse(null) is null.
func safeJSONParse(v any) any {
	if v == nil {
		return nil
	}
	text, err := str(v)
	if err != nil {
		return goapi.NewObject()
	}
	parsed, err := goapi.Parse([]byte(text))
	if err != nil {
		return goapi.NewObject()
	}
	return parsed
}

// allStrategies is getAllStrategies().
func (s *clientStore) allStrategies(ctx context.Context) ([]*goapi.Object, error) {
	rows, err := readRows(ctx, s.db, `SELECT * FROM strategies ORDER BY name ASC`)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		s.parsePermissions(row)
	}
	return rows, nil
}

// strategyByGUID is getStrategyByGuid; nil when there is none.
func (s *clientStore) strategyByGUID(ctx context.Context, guid string) (*goapi.Object, error) {
	row, err := readRow(ctx, s.db, `SELECT * FROM strategies WHERE guid = ?`, guid)
	if err != nil || row == nil {
		return nil, err
	}
	s.parsePermissions(row)
	return row, nil
}

// strategyData are the fields of a strategy the routes write. enabled is
// the body's value; undefined leaves the column alone on an update.
type strategyData struct {
	name, userGroupGUID, deviceGroupGUID string
	enabled                              jsbind.Value
	permissions                          any
}

// createStrategy is createStrategy: on SQLite the row read back with the
// permissions parsed, on PostgreSQL the row the INSERT returned.
func (s *clientStore) createStrategy(ctx context.Context, data strategyData) (*goapi.Object, error) {
	guid, err := randomUUID()
	if err != nil {
		return nil, err
	}
	permissions := data.permissions
	if !truthy(permissions) {
		permissions = goapi.NewObject()
	}
	enabled := data.enabled
	if s.db.IsPostgres() {
		if enabled.IsUndefined() {
			enabled = jsbind.Bool(true)
		}
	} else if enabled.IsUndefined() || enabled.Truthy() {
		enabled = jsbind.Number(1)
	} else {
		enabled = jsbind.Number(0)
	}
	args, err := s.args(6, jsbind.String(guid), jsbind.String(data.name), jsbind.String(data.userGroupGUID),
		jsbind.String(data.deviceGroupGUID), enabled, jsbind.String(stringify(permissions)))
	if err != nil {
		return nil, err
	}
	const insert = `
                INSERT INTO strategies (guid, name, user_group_guid, device_group_guid, enabled, permissions)
                VALUES (?, ?, ?, ?, ?, ?)`
	if s.db.IsPostgres() {
		return readRow(ctx, s.db, insert+` RETURNING *`, args...)
	}
	if _, err := s.db.Exec(ctx, insert, args...); err != nil {
		return nil, err
	}
	return s.strategyByGUID(ctx, guid)
}

// updateStrategy is updateStrategy with the fields the routes set; nil for
// an unknown guid.
func (s *clientStore) updateStrategy(ctx context.Context, guid string, data strategyData, onlyEnabled bool) (*goapi.Object, error) {
	var sets []string
	var values []jsbind.Value
	if !onlyEnabled {
		sets = append(sets, "name = ?", "user_group_guid = ?", "device_group_guid = ?")
		values = append(values, jsbind.String(data.name), jsbind.String(data.userGroupGUID), jsbind.String(data.deviceGroupGUID))
	}
	if !data.enabled.IsUndefined() {
		sets = append(sets, "enabled = ?")
		if s.db.IsPostgres() {
			values = append(values, jsbind.Bool(data.enabled.Truthy()))
		} else if data.enabled.Truthy() {
			values = append(values, jsbind.Number(1))
		} else {
			values = append(values, jsbind.Number(0))
		}
	}
	if !onlyEnabled {
		sets = append(sets, "permissions = ?")
		values = append(values, jsbind.String(stringify(data.permissions)))
	}
	if len(sets) == 0 {
		return nil, nil
	}
	sets = append(sets, "updated_at = "+store.NowSQL(s.db.Dialect()))
	values = append(values, jsbind.String(guid))
	args, err := s.args(len(values), values...)
	if err != nil {
		return nil, err
	}
	update := `UPDATE strategies SET ` + strings.Join(sets, ", ") + ` WHERE guid = ?`
	if s.db.IsPostgres() {
		return readRow(ctx, s.db, update+` RETURNING *`, args...)
	}
	if _, err := s.db.Exec(ctx, update, args...); err != nil {
		return nil, err
	}
	return s.strategyByGUID(ctx, guid)
}

// deleteStrategy is deleteStrategy: its assignments, then the strategy.
func (s *clientStore) deleteStrategy(ctx context.Context, guid string) error {
	if _, err := s.db.Exec(ctx, `DELETE FROM strategy_assignments WHERE strategy_guid = ?`, guid); err != nil {
		return err
	}
	_, err := s.db.Exec(ctx, `DELETE FROM strategies WHERE guid = ?`, guid)
	return err
}

// strategyTargets are the resolved keys a strategy is assigned to.
type strategyTargets struct {
	peers, users, groups []string
}

// assignStrategy is assignStrategy: the targets get the strategy, or with
// an empty guid lose theirs.
func (s *clientStore) assignStrategy(ctx context.Context, strategyGUID string, targets strategyTargets) error {
	strategyGUID = jsTrim(strategyGUID)
	if strategyGUID != "" {
		st, err := s.strategyByGUID(ctx, strategyGUID)
		if err != nil {
			return err
		}
		if st == nil {
			return errors.New("strategy not found")
		}
	}
	upsert := func(targetType string, keys []string) error {
		for _, raw := range keys {
			key := jsTrim(raw)
			if key == "" {
				continue
			}
			if strategyGUID == "" {
				if _, err := s.db.Exec(ctx, `DELETE FROM strategy_assignments WHERE target_type = ? AND target_key = ?`, targetType, key); err != nil {
					return err
				}
				continue
			}
			_, err := s.db.Exec(ctx, `
                        INSERT INTO strategy_assignments (target_type, target_key, strategy_guid, updated_at)
                        VALUES (?, ?, ?, `+store.NowSQL(s.db.Dialect())+`)
                        ON CONFLICT(target_type, target_key) DO UPDATE SET
                            strategy_guid = `+s.db.Pick("excluded", "EXCLUDED")+`.strategy_guid,
                            updated_at = `+s.db.Pick("excluded", "EXCLUDED")+`.updated_at
                    `, targetType, key, strategyGUID)
			if err != nil {
				return err
			}
		}
		return nil
	}
	if err := upsert("peer", targets.peers); err != nil {
		return err
	}
	if err := upsert("user", targets.users); err != nil {
		return err
	}
	return upsert("device_group", targets.groups)
}

// assignmentSummary is getStrategyAssignmentSummary.
type assignmentSummary struct {
	PeerCount        int      `json:"peer_count"`
	UserCount        int      `json:"user_count"`
	DeviceGroupCount int      `json:"device_group_count"`
	Peers            []string `json:"peers"`
	Users            []string `json:"users"`
	Groups           []string `json:"groups"`
}

func (s *clientStore) assignmentSummary(ctx context.Context, strategyGUID string) (assignmentSummary, error) {
	summary := assignmentSummary{Peers: []string{}, Users: []string{}, Groups: []string{}}
	rows, err := s.db.Query(ctx, `
                SELECT target_type, target_key FROM strategy_assignments
                WHERE strategy_guid = ? ORDER BY target_type, target_key
            `, strategyGUID)
	if err != nil {
		return summary, err
	}
	defer rows.Close()
	for rows.Next() {
		var kind, key string
		if err := rows.Scan(&kind, &key); err != nil {
			return summary, err
		}
		switch kind {
		case "peer":
			summary.PeerCount++
			summary.Peers = append(summary.Peers, key)
		case "user":
			summary.UserCount++
			summary.Users = append(summary.Users, key)
		case "device_group":
			summary.DeviceGroupCount++
			summary.Groups = append(summary.Groups, key)
		}
	}
	return summary, rows.Err()
}

// col is a column of a row as JavaScript held it.
func col(row *goapi.Object, key string) any { return plain(row.Get(key)) }
