package devicegroups

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/neodeskapp/neodesk/neodesk-server/console/access"
	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
	"github.com/neodeskapp/neodesk/neodesk-server/console/jsbind"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
)

// Who hears of a device: the folder it is in, and the accounts on the
// access lists of that folder and of the device groups it belongs to. The
// help request mail (package helpmail) asks, with the console's database.

// ErrAssignmentsNotIterable is the error the old console threw from these
// lookups for every device (BUGS PA8). The recorded runs of the help
// request mail replay it (package helpmail, TestEventVectors).
var ErrAssignmentsNotIterable = errors.New("(assignments || []) is not iterable")

// lookups is a Service on db for the lookups below.
func lookups(db *store.DB) *Service {
	return New(Options{DB: db, Access: access.New(db, nil, access.Options{})})
}

// folderOf is the folder of a device as its assignment holds it (see
// folderValue); ok is false when the device is in no folder.
func (s *Service) folderOf(ctx context.Context, deviceID string) (folder any, ok bool, err error) {
	row, err := readRow(ctx, s.db, `SELECT folder_id FROM device_folder_assignments WHERE device_id = ?`, deviceID)
	if err != nil {
		return nil, false, fmt.Errorf("load the folder of device %s: %w", deviceID, err)
	}
	if row == nil {
		return nil, false, nil
	}
	return folderValue(row.Get("folder_id")), true, nil
}

// folderValue is a stored folder id as a number, or the stored value when
// it is none.
func folderValue(stored any) any {
	raw := Plain(stored)
	if folder := valueOf(raw).ToNumber(); isFinite(folder) {
		return folder
	}
	return raw
}

// ResolveFolderNameForDevice names the folder a device is in; "" for a
// device in no folder and for an empty id.
func ResolveFolderNameForDevice(ctx context.Context, db *store.DB, deviceID string) (string, error) {
	id := jsTrim(deviceID)
	if id == "" {
		return "", nil
	}
	s := lookups(db)
	folder, ok, err := s.folderOf(ctx, id)
	if err != nil || !ok {
		return "", err
	}
	key := jsbind.String(jsText(folder))
	if n, isNumber := folder.(float64); isNumber {
		key = jsbind.Number(n)
	}
	row, err := s.FolderByID(ctx, key)
	if err != nil || row == nil {
		return "", err
	}
	if name := Plain(row.Get("name")); goapi.Truthy(name) {
		return jsText(name), nil
	}
	return "", nil
}

// ResolveOperatorUsernamesForDevice names the accounts on the access
// lists of a device's folder and of the device groups it belongs to (by
// folder, as a stored member or by tag), each once: the folder's list
// first, then the groups by name; of each list the accounts it names,
// then the members of the user groups it names. An empty id has none.
func ResolveOperatorUsernamesForDevice(ctx context.Context, db *store.DB, deviceID string) ([]string, error) {
	id := jsTrim(deviceID)
	names := &nameSet{seen: map[string]bool{}, list: []string{}}
	if id == "" {
		return names.list, nil
	}
	s := lookups(db)
	device := access.Device{ID: id}
	folder, inFolder, err := s.folderOf(ctx, id)
	if err != nil {
		return nil, err
	}
	if inFolder {
		group, err := s.access.DeviceGroupByGUID(ctx, "folder_"+jsText(folder))
		if err != nil {
			return nil, err
		}
		if group != nil {
			if err := s.collectAccessList(ctx, *group, names); err != nil {
				return nil, err
			}
		}
		if n, isNumber := folder.(float64); isNumber && n == math.Trunc(n) && math.Abs(n) < 1<<53 {
			number := int64(n)
			device.FolderID = &number
		}
	}
	if device.Tags, err = s.deviceTags(ctx, id); err != nil {
		return nil, err
	}
	groups, err := s.access.AllDeviceGroups(ctx)
	if err != nil {
		return nil, err
	}
	for _, group := range groups {
		if _, member := s.access.GroupPeerIDs(ctx, group, []access.Device{device})[id]; !member {
			continue
		}
		if err := s.collectAccessList(ctx, group, names); err != nil {
			return nil, err
		}
	}
	return names.list, nil
}

// deviceTags are the tags of a device as the server's device table peers
// holds them, which has them on both databases (the console's copy peer
// has none on PostgreSQL). A deleted device, one the table does not have,
// and a database without the table have none.
func (s *Service) deviceTags(ctx context.Context, deviceID string) ([]string, error) {
	row, err := readRow(ctx, s.db, `SELECT tags FROM peers WHERE id = ? AND NOT soft_deleted`, deviceID)
	if err != nil {
		if message := err.Error(); strings.Contains(message, "no such table") || strings.Contains(message, "does not exist") {
			return nil, nil
		}
		return nil, fmt.Errorf("load the tags of device %s: %w", deviceID, err)
	}
	if row == nil {
		return nil, nil
	}
	return access.NormalizeTags(Plain(row.Get("tags"))), nil
}

// nameSet keeps account names in the order they were first added.
type nameSet struct {
	seen map[string]bool
	list []string
}

func (n *nameSet) add(name string) {
	if !n.seen[name] {
		n.seen[name] = true
		n.list = append(n.list, name)
	}
}

// collectAccessList adds the accounts a device group's access list names,
// then the members of the user groups it names, each group's members by
// name.
func (s *Service) collectAccessList(ctx context.Context, group access.DeviceGroup, names *nameSet) error {
	for _, name := range access.NormalizeUsernames(group.AllowedUsers) {
		names.add(name)
	}
	for _, guid := range access.NormalizeGroupGUIDs(group.AllowedGroups) {
		members, err := readRows(ctx, s.db, `
			SELECT u.username FROM users u
			INNER JOIN user_group_members ugm ON ugm.user_id = u.id
			INNER JOIN user_groups ug ON ug.id = ugm.user_group_id
			WHERE ug.guid = ?
			ORDER BY u.username ASC`, guid)
		if err != nil {
			return fmt.Errorf("load the members of user group %q: %w", guid, err)
		}
		for _, member := range members {
			names.add(jsText(member.Get("username")))
		}
	}
	return nil
}

// OperatorEmail is an account with its mail address.
type OperatorEmail struct {
	Username string `json:"username"`
	Email    string `json:"email"`
}

// ResolveOperatorEmailsForDevice is ResolveOperatorUsernamesForDevice with
// the mail addresses of the accounts, in the same order: accounts without
// an address are left out, and an address goes once, with the first
// account that has it.
func ResolveOperatorEmailsForDevice(ctx context.Context, db *store.DB, deviceID string) ([]OperatorEmail, error) {
	names, err := ResolveOperatorUsernamesForDevice(ctx, db, deviceID)
	if err != nil {
		return nil, err
	}
	out := []OperatorEmail{}
	if len(names) == 0 {
		return out, nil
	}
	args := make([]any, len(names))
	for i, name := range names {
		args[i] = name
	}
	rows, err := readRows(ctx, db, `SELECT username, email FROM users
		WHERE username IN (`+strings.TrimSuffix(strings.Repeat("?,", len(names)), ",")+`) AND email IS NOT NULL AND email != ''`, args...)
	if err != nil {
		return nil, fmt.Errorf("load the mail addresses of the operators: %w", err)
	}
	addresses := make(map[string]string, len(rows))
	for _, row := range rows {
		addresses[jsText(row.Get("username"))] = jsTrim(jsText(row.Get("email")))
	}
	seen := map[string]bool{}
	for _, name := range names {
		email := addresses[name]
		if email == "" || seen[email] {
			continue
		}
		seen[email] = true
		out = append(out, OperatorEmail{Username: name, Email: email})
	}
	return out, nil
}
