package bdapi

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/unitronix/betterdesk-server/console/jsbind"
	"github.com/unitronix/betterdesk-server/console/store"
)

// The queries of the group on the console's tables, as the old adapters
// of both databases ran them. The device table peer and the signal
// server's peers live in the same database; the signal server's table may
// be missing until the server has run, and its rows have their own types
// (the key as bytes).

// regState is getPeerRegistrationState: what a registration needs to know
// of a device id before it writes, deleted rows included.
type regState struct {
	uuid    jsbind.Value
	pk      any
	banned  bool
	deleted bool
}

// registrationState returns the device's row, nil when there is none.
func registrationState(ctx context.Context, db *store.DB, id string) (*regState, error) {
	if trim(id) == "" {
		return nil, nil
	}
	var rowID, uuid, banned, deleted, reason jsbind.Cell
	var pk any
	err := db.QueryRow(ctx, "SELECT id, uuid, pk, is_banned, is_deleted, banned_reason FROM peer WHERE id = ?", trim(id)).
		Scan(&rowID, &uuid, &pk, &banned, &deleted, &reason)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read the registration state of %s: %w", id, err)
	}
	state := &regState{uuid: uuid.Value().Or(jsbind.String("")), banned: oneOrTrue(banned.Value()), deleted: oneOrTrue(deleted.Value())}
	// row.pk || null: text, or bytes (a Buffer, also when empty)
	switch v := pk.(type) {
	case string:
		if v != "" {
			state.pk = v
		}
	case []byte:
		state.pk = v
	case int64:
		if v != 0 {
			state.pk = float64(v)
		}
	case float64:
		if v != 0 {
			state.pk = v
		}
	}
	return state, nil
}

// oneOrTrue is v === 1 || v === true.
func oneOrTrue(v jsbind.Value) bool {
	return v.IsNumber() && v.ToNumber() == 1 || v.JSON() == "true"
}

// upsertPeer is upsertPeer({id, uuid, pk, info, ip}) of both adapters:
// a new row online, or the row of the id with what the registration says
// and no longer deleted. A registration without a uuid keeps the stored
// one on both databases. The values go to the database as the drivers
// bound them.
func upsertPeer(ctx context.Context, db *store.DB, id string, uuid, pk jsbind.Value, info, ip string) error {
	if !uuid.Truthy() {
		uuid = jsbind.String("")
	}
	if !pk.Truthy() {
		pk = jsbind.Null
	}
	var query string
	if db.IsPostgres() {
		if info == "" {
			info = "{}"
		}
		query = `INSERT INTO peer (id, uuid, pk, info, ip, status_online, created_at)
                VALUES (?, ?, ?, ?::jsonb, ?, TRUE, NOW())
                ON CONFLICT(id) DO UPDATE SET
                    uuid = COALESCE(NULLIF(EXCLUDED.uuid, ''), peer.uuid),
                    pk = COALESCE(EXCLUDED.pk, peer.pk),
                    info = COALESCE(NULLIF(EXCLUDED.info, '{}'::jsonb), peer.info),
                    ip = COALESCE(NULLIF(EXCLUDED.ip, ''), peer.ip),
                    status_online = TRUE,
                    last_online = NOW(),
                    is_deleted = FALSE`
	} else {
		query = `INSERT INTO peer (id, uuid, pk, info, ip, status_online, created_at)
                VALUES (?, ?, ?, ?, ?, 1, datetime('now'))
                ON CONFLICT(id) DO UPDATE SET
                    uuid = COALESCE(NULLIF(excluded.uuid, ''), uuid),
                    pk = COALESCE(excluded.pk, pk),
                    info = COALESCE(excluded.info, info),
                    ip = COALESCE(excluded.ip, ip),
                    status_online = 1,
                    last_online = datetime('now'),
                    is_deleted = 0`
	}
	args, err := jsbind.Args(db.Dialect(), 5, jsbind.String(id), uuid, pk, jsbind.String(info), jsbind.String(ip))
	if err != nil {
		return fmt.Errorf("register device %s: %w", id, err)
	}
	if _, err := db.Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("register device %s: %w", id, err)
	}
	return nil
}

// updateOnlineStatus is updatePeerOnlineStatus(id): the device is online
// and was seen now. The id goes to the database as the driver bound it.
func updateOnlineStatus(ctx context.Context, db *store.DB, id jsbind.Value) error {
	args, err := jsbind.Args(db.Dialect(), 1, id)
	if err != nil {
		return err
	}
	_, err = db.Exec(ctx, "UPDATE peer SET "+db.Pick("status_online = 1, last_online = datetime('now')", "status_online = TRUE, last_online = NOW()")+" WHERE id = ?", args...)
	return err
}

// tableExists reports whether the database has the table, for the SQLite
// adapter's checks of sqlite_master.
func tableExists(ctx context.Context, db *store.DB, name string) (bool, error) {
	var found string
	err := db.QueryRow(ctx, "SELECT name FROM sqlite_master WHERE type='table' AND name=?", name).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// successor is the row of the id a device was renamed to.
type successor struct {
	pk, uuid any
	ip       jsbind.Value
}

// renameCheck is shouldRejectRenamedPeerRegistration(oldId, {uuid, pk,
// ip}): an id the signal server renamed is refused with the new id,
// unless the device proves to be the renamed one by its key, its uuid, or
// (with neither) its address; then it registers under the new id.
type renameCheck struct {
	reject     bool
	newID      string
	redirectID string
}

func checkRename(ctx context.Context, db *store.DB, oldID string, uuid, pk any, ip string) (renameCheck, error) {
	if db.IsPostgres() {
		// The PostgreSQL adapter's guard called a query helper that does
		// not exist; the ReferenceError made it answer "go on" for every
		// id (BUGS BD5)
		return renameCheck{}, nil
	}
	return checkRenameSQLite(ctx, db, oldID, uuid, pk, ip)
}

func checkRenameSQLite(ctx context.Context, db *store.DB, oldID string, uuid, pk any, ip string) (renameCheck, error) {
	if isCurrentIDSQLite(ctx, db, oldID) {
		return renameCheck{}, nil
	}
	newID := ""
	if ok, err := tableExists(ctx, db, "id_change_history"); err == nil && ok {
		var v sql.NullString
		if err := db.QueryRow(ctx, "SELECT new_id FROM id_change_history WHERE old_id = ? ORDER BY rowid DESC LIMIT 1", oldID).Scan(&v); err == nil {
			newID = v.String
		}
	}
	if newID == "" {
		return renameCheck{}, nil
	}
	var next *successor
	if ok, err := tableExists(ctx, db, "peers"); err == nil && ok {
		next = readSuccessor(ctx, db, "SELECT pk, uuid, ip FROM peers WHERE id = ? AND NOT soft_deleted", newID)
	}
	if next == nil {
		next = readSuccessor(ctx, db, "SELECT pk, uuid, ip FROM peer WHERE id = ? AND is_deleted = 0", newID)
	}
	return decideRename(next, newID, uuid, pk, ip)
}

// isCurrentIDSQLite reports whether a device has the id now: in the
// signal server's table when there is one, else in the console's.
func isCurrentIDSQLite(ctx context.Context, db *store.DB, id string) bool {
	if ok, err := tableExists(ctx, db, "peers"); err == nil && ok {
		return rowExists(ctx, db, "SELECT 1 FROM peers WHERE id = ? LIMIT 1", id) == nil
	}
	return rowExists(ctx, db, "SELECT 1 FROM peer WHERE id = ? LIMIT 1", id) == nil
}

// rowExists returns nil when the query found a row.
func rowExists(ctx context.Context, db *store.DB, query string, args ...any) error {
	var one any
	return db.QueryRow(ctx, query, args...).Scan(&one)
}

// readSuccessor reads the key, uuid and address of the new id; nil when
// there is no row or the query fails.
func readSuccessor(ctx context.Context, db *store.DB, query, id string) *successor {
	var pk, uuid any
	var ip jsbind.Cell
	if err := db.QueryRow(ctx, query, id).Scan(&pk, &uuid, &ip); err != nil {
		return nil
	}
	return &successor{pk: pk, uuid: uuid, ip: ip.Value()}
}

func decideRename(next *successor, newID string, uuid, pk any, ip string) (renameCheck, error) {
	if next == nil {
		return renameCheck{}, nil
	}
	pkValue := orValue(pk, "")
	uuidValue := orValue(uuid, "")
	if truthyColumn(pkValue) && truthyColumn(next.pk) && keysEqual(pkValue, next.pk) {
		return renameCheck{redirectID: newID}, nil
	}
	if truthyColumn(uuidValue) && truthyColumn(next.uuid) {
		same, err := uuidsEqual(uuidValue, next.uuid)
		if err != nil {
			return renameCheck{}, err
		}
		if same {
			return renameCheck{redirectID: newID}, nil
		}
	}
	if !truthyColumn(pkValue) && !truthyColumn(uuidValue) && ip != "" {
		stored := ""
		if next.ip.Truthy() {
			stored = next.ip.ToString()
		}
		if ipMatches(ip, stored) {
			return renameCheck{redirectID: newID}, nil
		}
	}
	return renameCheck{reject: true, newID: newID}, nil
}

// truthyColumn is the truth of a value of the request or of a column:
// bytes are a Buffer, which is always truthy.
func truthyColumn(v any) bool {
	switch t := v.(type) {
	case []byte:
		return true
	case int64:
		return t != 0
	}
	return truthyValue(v)
}

// columnText is String(v) of a column value: bytes as UTF-8 text.
func columnText(v any) (string, error) {
	switch t := v.(type) {
	case []byte:
		return strings.ToValidUTF8(string(t), "�"), nil
	case int64:
		return fmt.Sprint(t), nil
	}
	return text(v)
}

// keysEqual is pkEqual: two keys as bytes, text of hex digits read as hex
// and other text as UTF-8. Anything that throws is not equal.
func keysEqual(a, b any) bool {
	bytesOf := func(v any) ([]byte, bool) {
		if raw, ok := v.([]byte); ok {
			return raw, true
		}
		s, err := columnText(v)
		if err != nil {
			return nil, false
		}
		if s != "" && strings.Trim(strings.ToLower(s), "0123456789abcdef") == "" {
			return nodeHex(s), true
		}
		return []byte(s), true
	}
	x, okA := bytesOf(a)
	y, okB := bytesOf(b)
	return okA && okB && string(x) == string(y)
}

// nodeHex is Buffer.from(s, 'hex'): pairs of hex digits up to the first
// pair that is not one; an odd digit at the end is dropped.
func nodeHex(s string) []byte {
	out := make([]byte, 0, len(s)/2)
	for i := 0; i+1 < len(s); i += 2 {
		hi, lo := hexDigit(s[i]), hexDigit(s[i+1])
		if hi < 0 || lo < 0 {
			break
		}
		out = append(out, byte(hi<<4|lo))
	}
	return out
}

func hexDigit(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10
	}
	return -1
}

// uuidsEqual is uuidEqual: both as text, without dashes, in lower case.
func uuidsEqual(a, b any) (bool, error) {
	x, err := columnText(a)
	if err != nil {
		return false, err
	}
	y, err := columnText(b)
	if err != nil {
		return false, err
	}
	norm := func(s string) string { return jsbind.ToLower(strings.ReplaceAll(s, "-", "")) }
	return norm(x) == norm(y), nil
}

// ipMatches is peerIPMatches: the client's address against a stored
// address that may carry a port.
func ipMatches(client, stored string) bool {
	if client == "" || stored == "" {
		return false
	}
	host := stored
	if i := strings.LastIndex(stored, ":"); i > 0 && dottedQuad(stored[:i]) {
		host = stored[:i]
	}
	return client == host || strings.HasPrefix(stored, client+":")
}

// dottedQuad is /^\d+\.\d+\.\d+\.\d+$/.
func dottedQuad(s string) bool {
	parts := strings.Split(s, ".")
	if len(parts) != 4 {
		return false
	}
	for _, p := range parts {
		if p == "" || strings.Trim(p, "0123456789") != "" {
			return false
		}
	}
	return true
}

// enrollmentTemplate is getAgentTemplateByToken: whether there is a
// template with the token, and the device it was redeemed for. Any
// failure reads as no template, as the old lookup caught everything; a
// SQLite table from before the binding columns is read without them.
func enrollmentTemplate(ctx context.Context, db *store.DB, token string) (found bool, boundTo jsbind.Value) {
	var bound jsbind.Cell
	err := db.QueryRow(ctx, "SELECT enrolled_device_id FROM agent_templates WHERE enrollment_token = ?", token).Scan(&bound)
	if err == nil {
		return true, bound.Value()
	}
	if errors.Is(err, sql.ErrNoRows) || db.IsPostgres() || !strings.Contains(jsbind.Message(err), "no such column") {
		return false, jsbind.Null
	}
	if rowExists(ctx, db, "SELECT id, name, config_json, enrollment_token FROM agent_templates WHERE enrollment_token = ?", token) == nil {
		return true, jsbind.Null
	}
	return false, jsbind.Null
}

// bindTemplate is bindAgentTemplateDevice: the template of the token is
// redeemed for the device unless it was before. It reports whether the
// binding counts: a table without the binding columns admits without
// one, any other failure does not.
func bindTemplate(ctx context.Context, db *store.DB, token, deviceID string) bool {
	_, err := db.Exec(ctx, `UPDATE agent_templates SET enrolled_device_id = ?, enrolled_at = `+db.Pick("datetime('now')", "NOW()")+`
                    WHERE enrollment_token = ? AND (enrolled_device_id IS NULL OR enrolled_device_id = '')`, deviceID, token)
	if err == nil {
		return true
	}
	message := jsbind.Message(err)
	if db.IsPostgres() {
		return strings.Contains(message, "does not exist") || strings.Contains(message, "no such column")
	}
	return strings.Contains(message, "no such column")
}

// readNotificationIDs is getReadNotificationIds: the ids the account
// marked read.
func readNotificationIDs(ctx context.Context, db *store.DB, userID int64) (map[string]bool, error) {
	rows, err := db.Query(ctx, "SELECT notification_id FROM notification_reads WHERE user_id = ?", userID)
	if err != nil {
		return nil, fmt.Errorf("load the read notifications of user %d: %w", userID, err)
	}
	defer rows.Close()
	ids := map[string]bool{}
	for rows.Next() {
		var id jsbind.Cell
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("load the read notifications of user %d: %w", userID, err)
		}
		ids[id.Value().ToString()] = true
	}
	return ids, rows.Err()
}

// markRead is markNotificationRead and markAllNotificationsRead: the ids
// are marked read for the account (on SQLite in one transaction), and
// when the account has more than 500 marks, the 400 newest stay.
func markRead(ctx context.Context, db *store.DB, userID int64, ids []string) error {
	insert := db.Pick(
		`INSERT OR IGNORE INTO notification_reads (user_id, notification_id, read_at) VALUES (?, ?, datetime('now'))`,
		`INSERT INTO notification_reads (user_id, notification_id, read_at) VALUES (?, ?, NOW()) ON CONFLICT (user_id, notification_id) DO NOTHING`)
	if db.IsPostgres() {
		for _, id := range ids {
			if _, err := db.Exec(ctx, insert, userID, id); err != nil {
				return fmt.Errorf("mark notification read: %w", err)
			}
		}
	} else if err := db.InTx(ctx, func(tx *store.Tx) error {
		for _, id := range ids {
			if _, err := tx.Exec(ctx, insert, userID, id); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return fmt.Errorf("mark notifications read: %w", err)
	}
	var count int64
	if err := db.QueryRow(ctx, "SELECT COUNT(*) FROM notification_reads WHERE user_id = ?", userID).Scan(&count); err != nil {
		return fmt.Errorf("count read notifications: %w", err)
	}
	if count <= 500 {
		return nil
	}
	prune := db.Pick(
		`DELETE FROM notification_reads
                    WHERE user_id = ? AND rowid NOT IN (
                        SELECT rowid FROM notification_reads
                        WHERE user_id = ?
                        ORDER BY read_at DESC
                        LIMIT 400
                    )`,
		`DELETE FROM notification_reads
                     WHERE user_id = ?
                       AND (user_id, notification_id) NOT IN (
                           SELECT user_id, notification_id
                           FROM notification_reads
                           WHERE user_id = ?
                           ORDER BY read_at DESC
                           LIMIT 400
                       )`)
	if _, err := db.Exec(ctx, prune, userID, userID); err != nil {
		return fmt.Errorf("prune read notifications: %w", err)
	}
	return nil
}

// connection is insertAuditConnection's data.
type connection struct {
	hostID, hostUUID, peerID, peerName, action, sessionID, ip string
	connType                                                  int
}

func insertConnection(ctx context.Context, db *store.DB, c connection) error {
	_, err := db.Exec(ctx, `INSERT INTO audit_connections (host_id, host_uuid, peer_id, peer_name, action, conn_type, session_id, ip)
                VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, c.hostID, c.hostUUID, c.peerID, c.peerName, c.action, c.connType, c.sessionID, c.ip)
	if err != nil {
		return fmt.Errorf("record a connection: %w", err)
	}
	return nil
}

// auditEntry is a row of audit_connections as the session history reads
// it.
type auditEntry struct {
	id                                     jsbind.Value
	hostID, peerID, peerName, action, sess jsbind.Value
	createdAt                              store.Time
}

// auditConnections is getAuditConnections({limit, offset: 0}): the newest
// rows first.
func auditConnections(ctx context.Context, db *store.DB, limit int) ([]auditEntry, error) {
	rows, err := store.Select(ctx, db, "SELECT id, host_id, peer_id, peer_name, action, session_id, created_at FROM audit_connections WHERE 1=1 ORDER BY created_at DESC LIMIT ? OFFSET ?", limit, 0)
	if err != nil {
		return nil, fmt.Errorf("load the connection audit: %w", err)
	}
	defer rows.Close()
	var out []auditEntry
	for rows.Next() {
		var id, host, peer, name, action, sess jsbind.Cell
		var e auditEntry
		if err := rows.Scan(&id, &host, &peer, &name, &action, &sess, &e.createdAt); err != nil {
			return nil, fmt.Errorf("load the connection audit: %w", err)
		}
		e.id, e.hostID, e.peerID, e.peerName, e.action, e.sess = id.Value(), host.Value(), peer.Value(), name.Value(), action.Value(), sess.Value()
		out = append(out, e)
	}
	return out, rows.Err()
}

// createdText is String(entry.created_at): the text SQLite keeps, or a
// PostgreSQL time as Date.prototype.toString writes it.
func createdText(db *store.DB, t store.Time) string {
	if t.IsNull() {
		return ""
	}
	if instant, ok := t.Instant(); ok && db.IsPostgres() {
		return jsDateString(instant.In(time.Local))
	}
	return t.String()
}

// jsDateString is Date.prototype.toString: "Tue Nov 10 2026 01:05:51
// GMT+0100 (Central European Standard Time)". UTC has the name JavaScript
// prints; other zones get their abbreviation.
func jsDateString(t time.Time) string {
	t = t.Truncate(time.Millisecond)
	year := t.Year()
	yearText := fmt.Sprintf("%04d", year)
	if year < 0 {
		yearText = fmt.Sprintf("-%06d", -year)
	}
	name, offset := t.Zone()
	if offset == 0 && (name == "UTC" || name == "GMT" || name == "") {
		name = "Coordinated Universal Time"
	}
	return t.Format("Mon Jan 02 ") + yearText + t.Format(" 15:04:05 GMT-0700") + " (" + name + ")"
}
