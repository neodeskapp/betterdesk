package remoterelay

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
	"github.com/neodeskapp/neodesk/neodesk-server/console/jsbind"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/logging"
)

// The console keeps its own table of devices, peer, beside the signal
// server's table, peers. db.getPeerById read peer and, for a device that
// is not there or is marked deleted there, copied the signal server's row
// into peer first, so a lookup can write. parsePeer then shaped the row.

// errNullInfo is the TypeError parsePeer threw for a device whose info
// is the JSON text null: it read info.hostname, or info.os when the note
// came first.
var errNullInfo = errors.New("Cannot read properties of null (reading 'hostname')")

var errNullInfoOS = errors.New("Cannot read properties of null (reading 'os')")

// Device is db.getPeerById(id) as parsePeer shaped it: the device of the
// console's table, copied from the signal server's table first when only
// that one has it; nil when neither has it. A failed copy is logged and
// counts as no device.
func (s *Service) Device(ctx context.Context, id string) (*goapi.Object, error) {
	if s.opts.DB == nil {
		return nil, nil
	}
	return PeerByID(ctx, s.opts.DB, id)
}

// PeerByID is Device for a database.
func PeerByID(ctx context.Context, db *store.DB, id string) (*goapi.Object, error) {
	row, err := readPeer(ctx, db, id)
	if err != nil || row != nil {
		if err != nil {
			return nil, err
		}
		return row.parse(db.IsPostgres())
	}
	copied, err := copyFromSignalServer(ctx, db, id)
	if err == nil && copied {
		row, err = readPeer(ctx, db, id)
	}
	if err != nil {
		missing := "no such table"
		if db.IsPostgres() {
			missing = "does not exist"
		}
		if message := jsbind.Message(err); !strings.Contains(message, missing) {
			logging.Logf("console", "WARN: [DB] Fallback peers lookup error: %s", message)
		}
		return nil, nil
	}
	if row == nil {
		return nil, nil
	}
	return row.parse(db.IsPostgres())
}

// peerRow is a row of peer: each column's value as the driver gave it,
// and the info column apart.
type peerRow struct {
	cols map[string]any
	info sql.NullString
	// document says info is a PostgreSQL JSON column, which holds a JSON
	// value; a text column holds text that should be JSON.
	document bool
}

func readPeer(ctx context.Context, db *store.DB, id string) (*peerRow, error) {
	rows, err := db.Query(ctx, "SELECT * FROM peer WHERE id = ? AND "+db.Pick("is_deleted = 0", "NOT is_deleted"), id)
	if err != nil {
		return nil, fmt.Errorf("load device %s: %w", id, err)
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("load device %s: %w", id, err)
		}
		return nil, nil
	}
	types, err := rows.ColumnTypes()
	if err != nil {
		return nil, fmt.Errorf("load device %s: %w", id, err)
	}
	row := &peerRow{cols: map[string]any{}}
	dest := make([]any, len(types))
	values := make([]any, len(types))
	for i, t := range types {
		if t.Name() == "info" {
			dest[i] = &row.info
			row.document = strings.HasPrefix(strings.ToUpper(t.DatabaseTypeName()), "JSON")
			continue
		}
		dest[i] = &values[i]
	}
	if err := rows.Scan(dest...); err != nil {
		return nil, fmt.Errorf("load device %s: %w", id, err)
	}
	for i, t := range types {
		if t.Name() != "info" {
			row.cols[t.Name()] = values[i]
		}
	}
	return row, rows.Close()
}

// parse is parsePeer of the adapter: the fields the relay and the remote
// routes read, with the values JavaScript had.
func (p *peerRow) parse(postgres bool) (*goapi.Object, error) {
	info, err := p.parsedInfo(postgres)
	if err != nil {
		if jsTruthy(driverValue(p.cols["note"])) {
			return nil, errNullInfoOS
		}
		return nil, err
	}
	col := func(name string) any { return driverValue(p.cols[name]) }
	or := func(v, fallback any) any {
		if jsTruthy(v) {
			return v
		}
		return fallback
	}
	username := ""
	if text, ok := col("user").(string); ok {
		username = text
	}
	online, banned := col("status_online") == float64(1), col("is_banned") == float64(1)
	if postgres {
		online, banned = jsTruthy(col("status_online")), jsTruthy(col("is_banned"))
	}
	tags := []any{}
	if text, ok := col("tags").(string); ok && text != "" {
		for _, tag := range strings.Split(text, ",") {
			if tag != "" {
				tags = append(tags, tag)
			}
		}
	}
	var infoText any = p.info.String
	if !p.info.Valid {
		infoText = ""
	}
	return goapi.NewObject(
		"id", col("id"),
		"uuid", or(col("uuid"), ""),
		"pk", or(col("pk"), nil),
		"hostname", or(col("note"), or(info.get("hostname").plain(), "")),
		"username", username,
		"platform", or(info.get("os").plain(), or(info.get("platform").plain(), "")),
		"ip", or(info.get("ip").plain(), or(col("ip"), "")),
		"note", or(col("note"), ""),
		"online", online,
		"banned", banned,
		"created_at", col("created_at"),
		"last_online", col("last_online"),
		"ban_reason", or(col("banned_reason"), ""),
		"folder_id", or(col("folder_id"), nil),
		"info", or(infoText, ""),
		"tags", tags,
	), nil
}

// parsedInfo is the info as parsePeer read it. From a text column no
// info is an empty object and the rest goes through JSON.parse, whose
// failure is an empty object too. A PostgreSQL JSON column holds the
// value itself: an object or a list is taken, a text is parsed like the
// text of a text column, anything else is an empty object. Info that is
// null after parsing made parsePeer throw.
func (p *peerRow) parsedInfo(postgres bool) (jsValue, error) {
	empty := jsValue{kind: jsObject}
	if !p.info.Valid || p.info.String == "" {
		return empty, nil
	}
	text := p.info.String
	if p.document {
		doc, err := parseJSON(text)
		if err != nil {
			return empty, nil
		}
		switch doc.kind {
		case jsObject, jsArray:
			return doc, nil
		case jsString:
			if doc.text == "" {
				return empty, nil
			}
			text = doc.text
		default:
			return empty, nil
		}
	}
	v, err := parseJSON(text)
	if err != nil {
		return empty, nil
	}
	if v.kind == jsNull {
		return jsValue{}, errNullInfo
	}
	return v, nil
}

// parseJSON is JSON.parse for a stored text, which may nest deeper than a
// relay message: past stringifyDepth it counts as no JSON, which the
// callers read as the empty object JSON.parse's failure gave.
func parseJSON(text string) (jsValue, error) {
	depth, ok := scanJSON(text)
	if !ok || depth > stringifyDepth {
		return jsValue{}, errSyntax
	}
	return parseJS(text)
}

// plain is the value as the values goapi.Parse returns.
func (v jsValue) plain() any {
	switch v.kind {
	case jsNull, jsUndefined:
		return nil
	case jsBool:
		return v.flag
	case jsNumber:
		return v.num
	case jsString:
		return v.text
	case jsArray:
		out := make([]any, len(v.items))
		for i, item := range v.items {
			out[i] = item.plain()
		}
		return out
	}
	o := goapi.NewObject()
	for _, key := range v.keys {
		o.Set(key, v.members[key].plain())
	}
	return o
}

// driverValue is a scanned value as JavaScript had it from the driver:
// numbers as doubles, text as text, bytes as bytes.
func driverValue(v any) any {
	switch x := v.(type) {
	case int64:
		return float64(x)
	case int32:
		return float64(x)
	}
	return v
}

// jsTruthy is !!value for a value the driver returned; a Buffer and a
// Date are truthy even when empty.
func jsTruthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case []byte, time.Time:
		return true
	case float64:
		return x != 0 && x == x
	case string:
		return x != ""
	case bool:
		return x
	}
	return true
}

// copyFromSignalServer writes the signal server's row of a device into
// the console's table and reports whether there was one.
func copyFromSignalServer(ctx context.Context, db *store.DB, id string) (bool, error) {
	if db.IsPostgres() {
		return copyOnPostgres(ctx, db, id)
	}
	return copyOnSQLite(ctx, db, id)
}

const signalServerColumns = `id, uuid, pk, hostname, os, version, ip, "user", status, last_online, created_at, banned, banned_at, ban_reason`

// infoOf is the info the copy gets: JSON.stringify({ hostname, os,
// platform, version }).
func infoOf(hostname, os, version jsbind.Value) string {
	return `{"hostname":` + hostname.JSON() + `,"os":` + os.JSON() + `,"platform":` + os.JSON() + `,"version":` + version.JSON() + `}`
}

func copyOnSQLite(ctx context.Context, db *store.DB, id string) (bool, error) {
	var gid, uuid, hostname, os, version, ip, user, status, lastOnline, createdAt, banned, bannedAt, banReason, tags jsbind.Cell
	var pk any
	err := db.QueryRow(ctx, "SELECT "+signalServerColumns+", tags FROM peers WHERE id = ? AND NOT soft_deleted", id).
		Scan(&gid, &uuid, &pk, &hostname, &os, &version, &ip, &user, &status, &lastOnline, &createdAt, &banned, &bannedAt, &banReason, &tags)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("look device %s up in the signal server's table: %w", id, err)
	}
	empty := jsbind.String("")
	flag := func(on bool) jsbind.Value {
		if on {
			return jsbind.Number(1)
		}
		return jsbind.Number(0)
	}
	values := []jsbind.Value{
		gid.Value(),
		uuid.Value().Or(empty),
		jsbind.Null, // the key, see below
		jsbind.String(infoOf(hostname.Value().Or(empty), os.Value().Or(empty), version.Value().Or(empty))),
		ip.Value().Or(empty),
		user.Value().Or(empty),
		flag(status.Value().IsString() && status.Value().Str() == "ONLINE"),
		lastOnline.Value().Or(jsbind.Null),
		createdAt.Value().Or(jsbind.String(store.ISOTime(time.Now()))),
		flag(banned.Value().Truthy()),
		bannedAt.Value().Or(jsbind.Null),
		banReason.Value().Or(empty),
		tags.Value().Or(empty),
	}
	args, err := jsbind.Args(store.SQLite, len(values), values...)
	if err != nil {
		return false, fmt.Errorf("copy device %s: %w", id, err)
	}
	// A key is bytes and goes over as bytes, also when it is empty; any
	// other value there goes by its truth
	switch key := pk.(type) {
	case []byte:
		args[2] = key
	case string:
		if key != "" {
			args[2] = key
		}
	case int64:
		if key != 0 {
			args[2] = float64(key)
		}
	case float64:
		if key != 0 {
			args[2] = key
		}
	}
	_, err = db.Exec(ctx, `
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
                        `, args...)
	if err != nil {
		return false, fmt.Errorf("copy device %s: %w", id, err)
	}
	return true, nil
}

func copyOnPostgres(ctx context.Context, db *store.DB, id string) (bool, error) {
	var gid string
	var uuid, hostname, os, version, ip, user, status, banReason sql.NullString
	var pk []byte
	var lastOnline, createdAt, bannedAt sql.NullTime
	var banned sql.NullBool
	err := db.QueryRow(ctx, "SELECT "+signalServerColumns+" FROM peers WHERE id = ? AND NOT soft_deleted", id).
		Scan(&gid, &uuid, &pk, &hostname, &os, &version, &ip, &user, &status, &lastOnline, &createdAt, &banned, &bannedAt, &banReason)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("look device %s up in the signal server's table: %w", id, err)
	}
	moment := func(t sql.NullTime) any {
		if !t.Valid {
			return nil
		}
		return t.Time
	}
	created := time.Now()
	if createdAt.Valid {
		created = createdAt.Time
	}
	var key any
	if pk != nil {
		key = pk
	}
	info := infoOf(jsbind.String(hostname.String), jsbind.String(os.String), jsbind.String(version.String))
	// The statement names its parameters by number, several of them twice
	_, err = db.Exec(ctx, `
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
                        `, gid, uuid.String, key, info, ip.String, user.String, status.String == "ONLINE", moment(lastOnline), created,
		banned.Bool, moment(bannedAt), banReason.String)
	if err != nil {
		return false, fmt.Errorf("copy device %s: %w", id, err)
	}
	return true, nil
}
