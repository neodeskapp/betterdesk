package devicegroups

import (
	"context"
	"fmt"

	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
)

// The stored members of device groups: which devices an account put into
// a manual group.

// Rows runs a query and returns its rows as the old console's driver
// handed them to JSON, the columns in the order the statement gave them.
func Rows(ctx context.Context, q store.Querier, query string, args ...any) ([]*goapi.Object, error) {
	return readRows(ctx, q, query, args...)
}

// AddDeviceToGroup makes a device a stored member of the device group with
// the guid. An unknown guid does nothing, and so does a device that is a
// member already.
func (s *Service) AddDeviceToGroup(ctx context.Context, groupGUID, peerID string) error {
	id, ok, err := s.groupID(ctx, s.db, "device_groups", groupGUID)
	if err != nil {
		return fmt.Errorf("load device group %q: %w", groupGUID, err)
	}
	if !ok {
		return nil
	}
	insert := s.db.Pick(
		`INSERT OR IGNORE INTO device_group_members (device_group_id, peer_id) VALUES (?, ?)`,
		`INSERT INTO device_group_members (device_group_id, peer_id) VALUES (?, ?) ON CONFLICT DO NOTHING`)
	if _, err := s.db.Exec(ctx, insert, id, peerID); err != nil {
		return fmt.Errorf("add device %s to device group %q: %w", peerID, groupGUID, err)
	}
	return nil
}

// RemoveDeviceFromGroup ends a device's stored membership of the device
// group with the guid. An unknown guid does nothing.
func (s *Service) RemoveDeviceFromGroup(ctx context.Context, groupGUID, peerID string) error {
	id, ok, err := s.groupID(ctx, s.db, "device_groups", groupGUID)
	if err != nil {
		return fmt.Errorf("load device group %q: %w", groupGUID, err)
	}
	if !ok {
		return nil
	}
	if _, err := s.db.Exec(ctx, `DELETE FROM device_group_members WHERE device_group_id = ? AND peer_id = ?`, id, peerID); err != nil {
		return fmt.Errorf("remove device %s from device group %q: %w", peerID, groupGUID, err)
	}
	return nil
}

// DeviceGroupsForPeer returns the device groups a device is a stored
// member of, ordered by name: their rows as they are, without access
// lists.
func (s *Service) DeviceGroupsForPeer(ctx context.Context, peerID string) ([]*goapi.Object, error) {
	rows, err := readRows(ctx, s.db, `
		SELECT dg.* FROM device_groups dg
		INNER JOIN device_group_members dgm ON dg.id = dgm.device_group_id
		WHERE dgm.peer_id = ?
		ORDER BY dg.name ASC`, peerID)
	if err != nil {
		return nil, fmt.Errorf("load the device groups of device %s: %w", peerID, err)
	}
	return rows, nil
}

// DeviceGroupsForPeers is DeviceGroupsForPeer for many devices in one
// query: the groups of each device by its id, for the devices that are a
// member of any. The device list uses it, so it reads every membership
// once instead of asking per device.
func (s *Service) DeviceGroupsForPeers(ctx context.Context, peerIDs []string) (map[string][]*goapi.Object, error) {
	out := map[string][]*goapi.Object{}
	wanted := make(map[string]bool, len(peerIDs))
	for _, id := range peerIDs {
		wanted[id] = true
	}
	if len(wanted) == 0 {
		return out, nil
	}
	rows, err := readRows(ctx, s.db, `
		SELECT dgm.peer_id AS member_peer_id, dg.* FROM device_groups dg
		INNER JOIN device_group_members dgm ON dg.id = dgm.device_group_id
		ORDER BY dg.name ASC`)
	if err != nil {
		return nil, fmt.Errorf("load the device groups of the devices: %w", err)
	}
	for _, row := range rows {
		peer := jsText(row.Get("member_peer_id"))
		if !wanted[peer] {
			continue
		}
		row.Delete("member_peer_id")
		out[peer] = append(out[peer], row)
	}
	return out, nil
}
