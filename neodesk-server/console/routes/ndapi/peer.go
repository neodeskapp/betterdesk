package ndapi

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/jsbind"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
)

// The console keeps its own table of devices, peer, beside the signal
// server's table, peers. Looking a device up by its id (db.getDeviceById)
// reads peer, and for a device that is not there, or is marked deleted
// there, copies the signal server's row into peer first. So a lookup can
// write: a device the signal server knows shows up in peer, and one that
// was deleted in the console comes back.
//
// The routes read the id, the note and the ban of the device the lookup
// gave. A deleted device is not found, unless the copy brings it back.

// device is what the routes read of a device the console knows.
type device struct {
	id     jsbind.Cell
	note   string
	banned bool
}

// deviceByID returns the device with the id, nil when neither table has
// it. The id goes to the database as the driver bound it. What goes wrong
// while copying is logged and the device counts as unknown; a missing
// table of the signal server quietly. A lookup that cannot bind the id
// fails.
func (h *handlers) deviceByID(ctx context.Context, id jsbind.Value) (*device, error) {
	db := h.d.Store
	args, err := jsbind.Args(db.Dialect(), 1, id)
	if err != nil {
		return nil, fmt.Errorf("look device up: %w", err)
	}
	d, err := readDevice(ctx, db, args)
	if err != nil || d != nil {
		return d, err
	}
	copied, err := copyFromSignalServer(ctx, db, args)
	if err == nil && copied {
		d, err = readDevice(ctx, db, args)
	}
	if err != nil {
		if message := jsbind.Message(err); !strings.Contains(message, "no such table") && !strings.Contains(message, "does not exist") {
			h.log.Warn("[DB] Fallback peers lookup error:", message)
		}
		return nil, nil
	}
	return d, nil
}

// readDevice reads the id, the note and the ban of the device's row in
// the console's table, unless the row is marked deleted.
func readDevice(ctx context.Context, db *store.DB, args []any) (*device, error) {
	var id, note, banned jsbind.Cell
	err := db.QueryRow(ctx, "SELECT id, note, is_banned FROM peer WHERE id = ? AND "+db.Pick("is_deleted = 0", "NOT is_deleted"), args...).Scan(&id, &note, &banned)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load device: %w", err)
	}
	d := &device{id: id, banned: oneOrTrue(banned.Value())}
	if v := note.Value(); v.Truthy() {
		d.note = v.ToString()
	}
	return d, nil
}

// copyFromSignalServer writes the signal server's row of a device into
// the console's table and reports whether there was one. A row that is
// there already (marked deleted) gets the signal server's values where
// they say something, and is no longer deleted. Its note stays.
func copyFromSignalServer(ctx context.Context, db *store.DB, args []any) (bool, error) {
	if db.IsPostgres() {
		return copyOnPostgres(ctx, db, args)
	}
	return copyOnSQLite(ctx, db, args)
}

const signalServerColumns = `id, uuid, pk, hostname, os, version, ip, "user", status, last_online, created_at, banned, banned_at, ban_reason`

// infoOf is the info the copy gets: JSON.stringify({ hostname, os,
// platform, version }).
func infoOf(hostname, os, version jsbind.Value) string {
	return `{"hostname":` + hostname.JSON() + `,"os":` + os.JSON() + `,"platform":` + os.JSON() + `,"version":` + version.JSON() + `}`
}

func copyOnSQLite(ctx context.Context, db *store.DB, lookup []any) (bool, error) {
	var gid, uuid, hostname, os, version, ip, user, status, lastOnline, createdAt, banned, bannedAt, banReason, tags jsbind.Cell
	var pk any
	err := db.QueryRow(ctx, "SELECT "+signalServerColumns+", tags FROM peers WHERE id = ? AND NOT soft_deleted", lookup...).
		Scan(&gid, &uuid, &pk, &hostname, &os, &version, &ip, &user, &status, &lastOnline, &createdAt, &banned, &bannedAt, &banReason, &tags)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("look the device up in the signal server's table: %w", err)
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
		return false, fmt.Errorf("copy the device: %w", err)
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
		return false, fmt.Errorf("copy the device: %w", err)
	}
	return true, nil
}

func copyOnPostgres(ctx context.Context, db *store.DB, lookup []any) (bool, error) {
	var gid string
	var uuid, hostname, os, version, ip, user, status, banReason sql.NullString
	var pk []byte
	var lastOnline, createdAt, bannedAt sql.NullTime
	var banned sql.NullBool
	err := db.QueryRow(ctx, "SELECT "+signalServerColumns+" FROM peers WHERE id = ? AND NOT soft_deleted", lookup...).
		Scan(&gid, &uuid, &pk, &hostname, &os, &version, &ip, &user, &status, &lastOnline, &createdAt, &banned, &bannedAt, &banReason)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("look the device up in the signal server's table: %w", err)
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
		return false, fmt.Errorf("copy the device: %w", err)
	}
	return true, nil
}
