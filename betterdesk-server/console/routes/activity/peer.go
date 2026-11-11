package activity

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/unitronix/betterdesk-server/console/jsbind"
	"github.com/unitronix/betterdesk-server/console/routes/internal/jsjson"
	"github.com/unitronix/betterdesk-server/console/store"
)

// The console keeps its own table of devices, peer, beside the signal
// server's table, peers. Looking a device up by its id (db.getPeerById)
// reads peer, and for a device that is not there, or is marked deleted
// there, copies the signal server's row into peer first. So a lookup can
// write: a device the signal server knows shows up in peer, and one that
// was deleted in the console comes back.

// peer is what the routes read of a device the console knows.
type peer struct {
	note string
	// info is the device's info as its column holds it. Not valid stands
	// for NULL.
	info sql.NullString
	// document says the column is a PostgreSQL JSON column, which holds a
	// JSON value. A text column holds text that should be JSON.
	document bool
}

// parsedInfo is the info as the routes saw it after JSON.parse. It fails
// where JSON.parse threw.
//
// From a text column, no info is an empty object and the rest is parsed.
// From a JSON column, a text is parsed like the text of a text column,
// NULL is null, and every other value is itself (a number or a flag has
// no members, like an empty object).
func (p *peer) parsedInfo() (jsjson.Value, error) {
	text := p.info.String
	if p.document {
		if !p.info.Valid {
			return jsjson.NullValue, nil
		}
		value, err := jsjson.Parse(text)
		if err != nil {
			return jsjson.Value{}, fmt.Errorf("read the info document: %w", err)
		}
		if value.Kind() != jsjson.String {
			return value, nil
		}
		text = value.Str()
	}
	if text == "" {
		return jsjson.Parse("{}")
	}
	value, err := jsjson.Parse(text)
	if err == nil && value.Kind() == jsjson.Null {
		// The lookup itself read members of what the text holds
		err = errNullInfo
	}
	return value, err
}

// peerByID returns the device with the id as the console knows it, nil
// when neither table has it. A device only the signal server's table has
// is copied into the console's table first. What goes wrong while
// copying is logged and the device counts as unknown.
func (h *handlers) peerByID(ctx context.Context, id string) (*peer, error) {
	p, err := readPeer(ctx, h.db, id)
	if err != nil || p != nil {
		return p, err
	}
	copied, err := copyFromSignalServer(ctx, h.db, id)
	if err == nil && copied {
		p, err = readPeer(ctx, h.db, id)
	}
	if err != nil {
		// The signal server's table is missing until the server has run
		if message := jsbind.Message(err); !strings.Contains(message, "no such table") && !strings.Contains(message, "does not exist") {
			h.log.Warn("Fallback peers lookup error:", err)
		}
		return nil, nil
	}
	return p, nil
}

// readPeer reads the device's row in the console's table, unless it is
// marked deleted.
func readPeer(ctx context.Context, db *store.DB, id string) (*peer, error) {
	rows, err := db.Query(ctx, "SELECT note, info FROM peer WHERE id = ? AND "+db.Pick("is_deleted = 0", "NOT is_deleted"), id)
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
	var note sql.NullString
	p := &peer{document: strings.HasPrefix(strings.ToUpper(types[1].DatabaseTypeName()), "JSON")}
	if err := rows.Scan(&note, &p.info); err != nil {
		return nil, fmt.Errorf("load device %s: %w", id, err)
	}
	p.note = note.String
	return p, rows.Close()
}

// copyFromSignalServer writes the signal server's row of a device into
// the console's table and reports whether there was one. A row that is
// there already (marked deleted) gets the signal server's values where
// they say something, and is no longer deleted. Its note stays.
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

// errNullInfo is the TypeError of reading a member of null: the info of
// a device was the text "null".
var errNullInfo = errors.New("Cannot read properties of null")
