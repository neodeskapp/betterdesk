package devicegroups

import (
	"context"
	"fmt"

	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/jsbind"
	"github.com/unitronix/betterdesk-server/console/store"
)

// DeviceStore is what the device layer (goapi.Backend) reads and writes in
// the console's own tables: the folder assignments, the system
// information, the console's copy of a device row and its settings.
type DeviceStore struct {
	Groups *Service
}

var _ goapi.Store = DeviceStore{}

func (d DeviceStore) db() *store.DB { return d.Groups.db }

// GetStats counts the devices in the console's table that are not
// deleted: {total, online, banned, offline}. The device layer falls back
// on it when the server gives no statistics.
func (d DeviceStore) GetStats(ctx context.Context) (any, error) {
	db := d.db()
	query := db.Pick(
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
	if err := db.QueryRow(ctx, query).Scan(&total, &online, &banned); err != nil {
		return nil, fmt.Errorf("count the devices: %w", err)
	}
	return goapi.NewObject(
		"total", float64(total),
		"online", float64(online),
		"banned", float64(banned),
		"offline", float64(total-online),
	), nil
}

// GetAllFolderAssignments is Service.AllFolderAssignments.
func (d DeviceStore) GetAllFolderAssignments(ctx context.Context) (map[string]any, error) {
	return d.Groups.AllFolderAssignments(ctx)
}

// GetAllPeerSysinfo returns the system information rows with the fields
// the device layer reads: peer_id, hostname, platform, os_full, version.
func (d DeviceStore) GetAllPeerSysinfo(ctx context.Context) ([]*goapi.Object, error) {
	rows, err := readRows(ctx, d.db(), `SELECT * FROM peer_sysinfo`)
	if err != nil {
		return nil, fmt.Errorf("load system information: %w", err)
	}
	out := make([]*goapi.Object, len(rows))
	for i, row := range rows {
		out[i] = goapi.NewObject(
			"peer_id", Plain(row.Get("peer_id")),
			"hostname", Plain(row.Get("hostname")),
			"platform", Plain(row.Get("platform")),
			"version", Plain(row.Get("version")),
			"os_full", Plain(row.Get("os_full")),
		)
	}
	return out, nil
}

// UpdateDevice writes note, user and info of data, each that is set, to
// the console's own device row, one statement each.
func (d DeviceStore) UpdateDevice(ctx context.Context, id string, data *goapi.Object) error {
	db := d.db()
	for _, column := range []string{"note", "user", "info"} {
		if !data.Has(column) {
			continue
		}
		args, err := jsbind.Args(db.Dialect(), 2, valueOf(data.Get(column)), jsbind.String(id))
		if err != nil {
			return err
		}
		if _, err := db.Exec(ctx, `UPDATE peer SET "`+column+`" = ? WHERE id = ?`, args...); err != nil {
			return fmt.Errorf("update the console's row of device %s: %w", id, err)
		}
	}
	return nil
}

// SetSetting stores a console setting.
func (d DeviceStore) SetSetting(ctx context.Context, key, value string) error {
	db := d.db()
	now := store.NowSQL(db.Dialect())
	_, err := db.Exec(ctx, `INSERT INTO settings (key, value, updated_at) VALUES (?, ?, `+now+`)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = `+now, key, value)
	if err != nil {
		return fmt.Errorf("store setting %s: %w", key, err)
	}
	return nil
}
