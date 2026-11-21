package devices

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/unitronix/betterdesk-server/console/devicegroups"
	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/store"
)

// The queries of the access rules for RustDesk clients (strategies) and
// their assignments, as both adapters of the old console ran them.

// plainRows turns the values of rows into plain JSON values.
func plainRows(rows []*goapi.Object) []*goapi.Object {
	for _, row := range rows {
		for _, key := range row.Keys() {
			row.Set(key, devicegroups.Plain(row.Get(key)))
		}
	}
	return rows
}

// strategyRows reads rules with their permissions parsed, as
// getAllStrategies and getStrategyByGuid gave them.
func strategyRows(ctx context.Context, db *store.DB, query string, args ...any) ([]*goapi.Object, error) {
	rows, err := devicegroups.Rows(ctx, db, query, args...)
	if err != nil {
		return nil, err
	}
	for _, row := range plainRows(rows) {
		row.Set("permissions", safeJSONParse(db, row.Get("permissions"), goapi.NewObject()))
	}
	return rows, nil
}

// allStrategies returns every rule, ordered by name.
func allStrategies(ctx context.Context, db *store.DB) ([]*goapi.Object, error) {
	rows, err := strategyRows(ctx, db, `SELECT * FROM strategies ORDER BY name ASC`)
	if err != nil {
		return nil, fmt.Errorf("load strategies: %w", err)
	}
	return rows, nil
}

// strategyByGUID returns the rule with the guid, nil when there is none.
func strategyByGUID(ctx context.Context, db *store.DB, guid string) (*goapi.Object, error) {
	rows, err := strategyRows(ctx, db, `SELECT * FROM strategies WHERE guid = ?`, guid)
	if err != nil {
		return nil, fmt.Errorf("load strategy %q: %w", guid, err)
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return rows[0], nil
}

// flagArg is a flag as the adapter bound it: 1 or 0 on SQLite, a boolean
// on PostgreSQL.
func flagArg(db *store.DB, b bool) any {
	if db.IsPostgres() {
		return b
	}
	if b {
		return 1
	}
	return 0
}

// returned is the row of a write with its permissions parsed: on
// PostgreSQL what RETURNING * gave (the column is JSONB or, in the table
// the signal server created, TEXT), on SQLite the rule read again.
func returned(ctx context.Context, db *store.DB, query, guid string, args ...any) (*goapi.Object, error) {
	if db.IsPostgres() {
		rows, err := devicegroups.Rows(ctx, db, query+` RETURNING *`, args...)
		if err != nil {
			return nil, err
		}
		if len(rows) == 0 {
			return nil, nil
		}
		row := plainRows(rows)[0]
		row.Set("permissions", safeJSONParse(db, row.Get("permissions"), goapi.NewObject()))
		return row, nil
	}
	if _, err := db.Exec(ctx, query, args...); err != nil {
		return nil, err
	}
	return strategyByGUID(ctx, db, guid)
}

// createStrategy stores a rule under a random guid and returns its row.
func createStrategy(ctx context.Context, db *store.DB, p strategyPayload) (*goapi.Object, error) {
	guid, err := randomUUID()
	if err != nil {
		return nil, err
	}
	permissions, err := p.Permissions.MarshalJSON()
	if err != nil {
		return nil, err
	}
	row, err := returned(ctx, db, `INSERT INTO strategies (guid, name, user_group_guid, device_group_guid, enabled, permissions)
		VALUES (?, ?, ?, ?, ?, ?)`, guid,
		guid, p.Name, p.UserGroupGUID, p.DeviceGroupGUID, flagArg(db, p.Enabled), string(permissions))
	if err != nil {
		return nil, fmt.Errorf("create strategy: %w", err)
	}
	return row, nil
}

// updateStrategy writes every field of a rule and returns its row.
func updateStrategy(ctx context.Context, db *store.DB, guid string, p strategyPayload) (*goapi.Object, error) {
	permissions, err := p.Permissions.MarshalJSON()
	if err != nil {
		return nil, err
	}
	query := `UPDATE strategies SET name = ?, user_group_guid = ?, device_group_guid = ?, enabled = ?, permissions = ?, updated_at = ` +
		store.NowSQL(db.Dialect()) + ` WHERE guid = ?`
	row, err := returned(ctx, db, query, guid,
		p.Name, p.UserGroupGUID, p.DeviceGroupGUID, flagArg(db, p.Enabled), string(permissions), guid)
	if err != nil {
		return nil, fmt.Errorf("update strategy %q: %w", guid, err)
	}
	return row, nil
}

// deleteStrategy deletes a rule and its assignments.
func deleteStrategy(ctx context.Context, db *store.DB, guid string) error {
	if _, err := db.Exec(ctx, `DELETE FROM strategy_assignments WHERE strategy_guid = ?`, guid); err != nil {
		return fmt.Errorf("delete the assignments of strategy %q: %w", guid, err)
	}
	if _, err := db.Exec(ctx, `DELETE FROM strategies WHERE guid = ?`, guid); err != nil {
		return fmt.Errorf("delete strategy %q: %w", guid, err)
	}
	return nil
}

// isUUID is the adapter's test for a uuid it can take as a guid: 36
// characters, four of them dashes.
func isUUID(s string) bool {
	return length(s) == 36 && strings.Count(s, "-") == 4
}

// peerIDOfReference returns the id of the device among the server's with
// the id, guid or uuid key, and refuses an empty key or one that names no
// device.
func peerIDOfReference(ctx context.Context, db *store.DB, key string) (string, error) {
	if key == "" {
		return "", refusal("empty peer reference")
	}
	var id string
	err := db.QueryRow(ctx, `SELECT id FROM peers WHERE id = ? OR guid = ? OR uuid = ? LIMIT 1`, key, key, key).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", refusal("peer not found")
	}
	return id, err
}

// resolvePeerAssignmentKey returns the guid a device's assignments are
// stored under, the device found among the server's by id, guid or uuid.
// With write, a device without a guid gets its uuid in lower case when
// that looks like one, else a random one, and keeps it; without, its key
// is "".
func resolvePeerAssignmentKey(ctx context.Context, db *store.DB, key string, write bool) (string, error) {
	id, err := peerIDOfReference(ctx, db, key)
	if err != nil {
		return "", err
	}
	var uuid, guid string
	err = db.QueryRow(ctx, `SELECT COALESCE(uuid, '') AS uuid, COALESCE(guid, '') AS guid FROM peers WHERE id = ?`, id).Scan(&uuid, &guid)
	if errors.Is(err, sql.ErrNoRows) {
		return "", refusal("peer not found")
	}
	if err != nil {
		return "", err
	}
	if guid != "" || !write {
		return guid, nil
	}
	if isUUID(uuid) {
		guid = strings.ToLower(uuid)
	} else if guid, err = randomUUID(); err != nil {
		return "", err
	}
	if _, err := db.Exec(ctx, `UPDATE peers SET guid = ? WHERE id = ?`, guid, id); err != nil {
		return "", err
	}
	return guid, nil
}

// resolveUserAssignmentKey returns the guid an account's assignments are
// stored under, the account found by guid, username or id. With write,
// an account without a guid gets a random one and keeps it; without, its
// key is "".
func resolveUserAssignmentKey(ctx context.Context, db *store.DB, key string, write bool) (string, error) {
	id, guid, err := assignmentUser(ctx, db, key)
	if err != nil {
		return "", err
	}
	if guid != "" || !write {
		return guid, nil
	}
	if guid, err = randomUUID(); err != nil {
		return "", err
	}
	if _, err := db.Exec(ctx, `UPDATE users SET guid = ? WHERE id = ?`, guid, id); err != nil {
		return "", err
	}
	return guid, nil
}

// assignmentUser finds the account a users reference of an assignment
// names, by guid, username or id, and returns its id and its guid ("" when
// it has none).
func assignmentUser(ctx context.Context, db *store.DB, key string) (int64, string, error) {
	if key == "" {
		return 0, "", refusal("empty user reference")
	}
	var id int64
	var guid string
	query := db.Pick(
		`SELECT id, COALESCE(guid, '') AS guid FROM users WHERE guid = ? OR username = ? OR CAST(id AS TEXT) = ? LIMIT 1`,
		`SELECT id, COALESCE(guid, '') AS guid FROM users WHERE guid = ? OR username = ? OR id::text = ? LIMIT 1`)
	err := db.QueryRow(ctx, query, key, key, key).Scan(&id, &guid)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, "", refusal("user not found")
	}
	if err != nil {
		return 0, "", err
	}
	return id, guid, nil
}

// resolveDeviceGroupAssignmentKey returns the guid of the device group
// with the guid or the name. It writes nothing, with write or without.
func resolveDeviceGroupAssignmentKey(ctx context.Context, db *store.DB, key string, _ bool) (string, error) {
	if key == "" {
		return "", refusal("empty device group reference")
	}
	var guid string
	err := db.QueryRow(ctx, `SELECT guid FROM device_groups WHERE guid = ? OR name = ? LIMIT 1`, key, key).Scan(&guid)
	if errors.Is(err, sql.ErrNoRows) {
		return "", refusal("device group not found")
	}
	if err != nil {
		return "", err
	}
	return guid, nil
}

// assign makes the rule the direct assignment of the targets, or with an
// empty guid removes their direct assignments.
func assign(ctx context.Context, db *store.DB, strategyGUID string, keys resolved) error {
	strategyGUID = jsTrim(strategyGUID)
	if strategyGUID != "" {
		existing, err := strategyByGUID(ctx, db, strategyGUID)
		if err != nil {
			return err
		}
		if existing == nil {
			return refusal("strategy not found")
		}
	}
	upsert := `INSERT INTO strategy_assignments (target_type, target_key, strategy_guid, updated_at)
		VALUES (?, ?, ?, ` + store.NowSQL(db.Dialect()) + `)
		ON CONFLICT (target_type, target_key) DO UPDATE SET
			strategy_guid = excluded.strategy_guid,
			updated_at = excluded.updated_at`
	for _, set := range []struct {
		kind string
		keys []string
	}{{"peer", keys.peers}, {"user", keys.users}, {"device_group", keys.groups}} {
		for _, raw := range set.keys {
			key := jsTrim(raw)
			if key == "" {
				continue
			}
			var err error
			if strategyGUID == "" {
				_, err = db.Exec(ctx, `DELETE FROM strategy_assignments WHERE target_type = ? AND target_key = ?`, set.kind, key)
			} else {
				_, err = db.Exec(ctx, upsert, set.kind, key, strategyGUID)
			}
			if err != nil {
				return fmt.Errorf("assign strategy: %w", err)
			}
		}
	}
	return nil
}

// assignmentSummary counts and lists the direct assignments of a rule.
func assignmentSummary(ctx context.Context, db *store.DB, strategyGUID string) (*goapi.Object, error) {
	rows, err := db.Query(ctx, `SELECT target_type, target_key FROM strategy_assignments
		WHERE strategy_guid = ? ORDER BY target_type, target_key`, strategyGUID)
	if err != nil {
		return nil, fmt.Errorf("load the assignments of strategy %q: %w", strategyGUID, err)
	}
	defer rows.Close()
	peers, users, groups := []any{}, []any{}, []any{}
	for rows.Next() {
		var kind, key string
		if err := rows.Scan(&kind, &key); err != nil {
			return nil, fmt.Errorf("load the assignments of strategy %q: %w", strategyGUID, err)
		}
		switch kind {
		case "peer":
			peers = append(peers, key)
		case "user":
			users = append(users, key)
		case "device_group":
			groups = append(groups, key)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("load the assignments of strategy %q: %w", strategyGUID, err)
	}
	return goapi.NewObject(
		"peer_count", float64(len(peers)), "user_count", float64(len(users)), "device_group_count", float64(len(groups)),
		"peers", peers, "users", users, "groups", groups,
	), nil
}

// lookupText is the text of the first row of a query with one column, ""
// when there is no row or its value is null.
func lookupText(ctx context.Context, db *store.DB, query string, args ...any) (string, error) {
	var text sql.NullString
	err := db.QueryRow(ctx, query, args...).Scan(&text)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return text.String, err
}

// assignmentDisplayRefs is the summary with the ids of the devices and the
// names of the accounts behind the guids. On SQLite a failed device
// lookup ends the device list quietly; on PostgreSQL it fails the call.
func assignmentDisplayRefs(ctx context.Context, db *store.DB, strategyGUID string) (*goapi.Object, error) {
	summary, err := assignmentSummary(ctx, db, strategyGUID)
	if err != nil {
		return nil, err
	}
	peerIDs, userNames := []any{}, []any{}
	for _, guid := range summary.Get("peers").([]any) {
		var id sql.NullString
		err := db.QueryRow(ctx, `SELECT id FROM peers WHERE guid = ? LIMIT 1`, guid).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			if db.IsPostgres() {
				return nil, fmt.Errorf("load the devices of strategy %q: %w", strategyGUID, err)
			}
			break
		}
		if id.Valid && (!db.IsPostgres() || id.String != "") {
			peerIDs = append(peerIDs, id.String)
		}
	}
	for _, guid := range summary.Get("users").([]any) {
		var name sql.NullString
		err := db.QueryRow(ctx, `SELECT username FROM users WHERE guid = ? LIMIT 1`, guid).Scan(&name)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("load the accounts of strategy %q: %w", strategyGUID, err)
		}
		if name.Valid && name.String != "" {
			userNames = append(userNames, name.String)
		}
	}
	out := summary.Clone()
	out.Set("peer_ids", peerIDs)
	out.Set("user_names", userNames)
	out.Set("group_guids", summary.Get("groups"))
	return out, nil
}
