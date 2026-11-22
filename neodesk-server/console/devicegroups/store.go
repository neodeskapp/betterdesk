package devicegroups

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/jsbind"
	"github.com/unitronix/betterdesk-server/console/store"
)

// The queries of folders, device groups and user groups. Values that came
// from a request are jsbind values and are bound as the old console's
// driver bound them, so a value of the wrong type fails or matches where it
// did: an id that is no number finds nothing on SQLite and is an error on
// PostgreSQL. Rows are read with SELECT * and keep the columns in the
// order of the table.

// args binds request values for a statement with n placeholders.
func (s *Service) args(n int, values ...jsbind.Value) ([]any, error) {
	return jsbind.Args(s.db.Dialect(), n, values...)
}

// ---- Folders ----

// AllFolders returns every folder, ordered by sort_order and name.
func (s *Service) AllFolders(ctx context.Context) ([]*goapi.Object, error) {
	rows, err := readRows(ctx, s.db, `SELECT * FROM folders ORDER BY sort_order, name`)
	if err != nil {
		return nil, fmt.Errorf("load folders: %w", err)
	}
	return rows, nil
}

// FolderByID returns the folder with the id, or nil.
func (s *Service) FolderByID(ctx context.Context, id jsbind.Value) (*goapi.Object, error) {
	args, err := s.args(1, id)
	if err != nil {
		return nil, err
	}
	row, err := readRow(ctx, s.db, `SELECT * FROM folders WHERE id = ?`, args...)
	if err != nil {
		return nil, fmt.Errorf("load folder: %w", err)
	}
	return row, nil
}

// CreateFolder stores a folder and returns its id. A color or icon that
// is empty gets the default, and the folder goes first in the order.
func (s *Service) CreateFolder(ctx context.Context, name, color, icon jsbind.Value) (store.Int64String, error) {
	args, err := s.args(4, name, color.Or(jsbind.String("#6366f1")), icon.Or(jsbind.String("folder")), jsbind.Number(0))
	if err != nil {
		return store.Int64String{}, err
	}
	id, err := s.db.Insert(ctx, `INSERT INTO folders (name, color, icon, sort_order) VALUES (?, ?, ?, ?)`, args...)
	if err != nil {
		return store.Int64String{}, fmt.Errorf("create folder: %w", err)
	}
	return store.NewInt64String(id, s.db.Shape().Table("folders").Type("id")), nil
}

// UpdateFolder writes the fields of a folder that are not undefined.
// Without one nothing is written.
func (s *Service) UpdateFolder(ctx context.Context, id, name, color, icon jsbind.Value) error {
	var sets []string
	var values []jsbind.Value
	for _, field := range []struct {
		column string
		value  jsbind.Value
	}{{"name", name}, {"color", color}, {"icon", icon}} {
		if !field.value.IsUndefined() {
			sets = append(sets, field.column+" = ?")
			values = append(values, field.value)
		}
	}
	if len(sets) == 0 {
		return nil
	}
	args, err := s.args(len(values)+1, append(values, id)...)
	if err != nil {
		return err
	}
	if _, err := s.db.Exec(ctx, `UPDATE folders SET `+strings.Join(sets, ", ")+` WHERE id = ?`, args...); err != nil {
		return fmt.Errorf("update folder: %w", err)
	}
	return nil
}

// DeleteFolder deletes a folder and the assignments of devices to it.
func (s *Service) DeleteFolder(ctx context.Context, id jsbind.Value) error {
	args, err := s.args(1, id)
	if err != nil {
		return err
	}
	if _, err := s.db.Exec(ctx, `DELETE FROM device_folder_assignments WHERE folder_id = ?`, args...); err != nil {
		return fmt.Errorf("delete folder assignments: %w", err)
	}
	if _, err := s.db.Exec(ctx, `DELETE FROM folders WHERE id = ?`, args...); err != nil {
		return fmt.Errorf("delete folder: %w", err)
	}
	return nil
}

// assignQuery puts a device into a folder: device id, folder id, folder id.
func assignQuery(q store.Querier) string {
	return `INSERT INTO device_folder_assignments (device_id, folder_id) VALUES (?, ?)
		ON CONFLICT(device_id) DO UPDATE SET folder_id = ?, assigned_at = ` + store.NowSQL(q.Dialect())
}

// AssignDeviceToFolder puts a device into a folder, or takes it out of
// its folder when folderID is null or undefined.
func (s *Service) AssignDeviceToFolder(ctx context.Context, deviceID, folderID jsbind.Value) error {
	query, args, err := s.assignment(s.db, deviceID, folderID)
	if err != nil {
		return err
	}
	if _, err := s.db.Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("assign device to folder: %w", err)
	}
	return nil
}

// assignment is the statement that puts one device into a folder or takes
// it out of its folder.
func (s *Service) assignment(q store.Querier, deviceID, folderID jsbind.Value) (string, []any, error) {
	if folderID.IsNull() || folderID.IsUndefined() {
		args, err := s.args(1, deviceID)
		return `DELETE FROM device_folder_assignments WHERE device_id = ?`, args, err
	}
	args, err := s.args(3, deviceID, folderID, folderID)
	return assignQuery(q), args, err
}

// AssignDevicesToFolder puts devices into a folder, or takes them out of
// their folder when folderID is null, in one transaction.
func (s *Service) AssignDevicesToFolder(ctx context.Context, deviceIDs []jsbind.Value, folderID jsbind.Value) error {
	err := s.db.InTx(ctx, func(tx *store.Tx) error {
		for _, id := range deviceIDs {
			query, args, err := s.assignment(tx, id, folderID)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, query, args...); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("assign devices to folder: %w", err)
	}
	return nil
}

// UnassignDevicesFromFolder takes every device out of a folder.
func (s *Service) UnassignDevicesFromFolder(ctx context.Context, folderID jsbind.Value) error {
	args, err := s.args(1, folderID)
	if err != nil {
		return err
	}
	if _, err := s.db.Exec(ctx, `DELETE FROM device_folder_assignments WHERE folder_id = ?`, args...); err != nil {
		return fmt.Errorf("unassign devices from folder: %w", err)
	}
	return nil
}

// AllFolderAssignments returns the folder of every device that has one,
// by device id: a number, or the stored value when it is none.
func (s *Service) AllFolderAssignments(ctx context.Context) (map[string]any, error) {
	rows, err := readRows(ctx, s.db, `SELECT device_id, folder_id FROM device_folder_assignments`)
	if err != nil {
		return nil, fmt.Errorf("load folder assignments: %w", err)
	}
	out := make(map[string]any, len(rows))
	for _, row := range rows {
		out[jsText(row.Get("device_id"))] = folderValue(row.Get("folder_id"))
	}
	return out, nil
}

// ---- Device groups ----

// accessLists adds to a device group row what its access lists hold:
// allowed_users (usernames), allowed_groups (user group guids) and
// allowed_user_groups (guid and name), each ordered by name.
func (s *Service) accessLists(ctx context.Context, group *goapi.Object) error {
	id := argOf(group.Get("id"))
	users, err := readRows(ctx, s.db, `
		SELECT u.username FROM device_group_user_access a
		INNER JOIN users u ON u.id = a.user_id
		WHERE a.device_group_id = ?
		ORDER BY u.username ASC`, id)
	if err != nil {
		return err
	}
	names := make([]any, len(users))
	for i, u := range users {
		names[i] = Plain(u.Get("username"))
	}
	groups, err := readRows(ctx, s.db, `
		SELECT ug.guid, ug.name FROM device_group_user_group_access a
		INNER JOIN user_groups ug ON ug.id = a.user_group_id
		WHERE a.device_group_id = ?
		ORDER BY ug.name ASC`, id)
	if err != nil {
		return err
	}
	guids := make([]any, len(groups))
	refs := make([]any, len(groups))
	for i, g := range groups {
		guids[i] = Plain(g.Get("guid"))
		refs[i] = goapi.NewObject("guid", Plain(g.Get("guid")), "name", Plain(g.Get("name")))
	}
	group.Set("allowed_users", names)
	group.Set("allowed_groups", guids)
	group.Set("allowed_user_groups", refs)
	return nil
}

// kindDefaults gives a group without a kind the kind "manual" and an empty
// tag filter.
func kindDefaults(group *goapi.Object) {
	group.Set("source_type", orValue(group.Get("source_type"), "manual"))
	group.Set("tag_filter", orValue(group.Get("tag_filter"), ""))
}

// countOf runs a COUNT(*) query and returns the number.
func (s *Service) countOf(ctx context.Context, query string, args ...any) (float64, error) {
	var n int64
	if err := s.db.QueryRow(ctx, query, args...).Scan(&n); err != nil {
		return 0, err
	}
	return float64(n), nil
}

// AllDeviceGroups returns every device group, ordered by name, with
// member_count (its stored members), its kind and its access lists.
func (s *Service) AllDeviceGroups(ctx context.Context) ([]*goapi.Object, error) {
	groups, err := readRows(ctx, s.db, `SELECT * FROM device_groups ORDER BY name ASC`)
	if err != nil {
		return nil, fmt.Errorf("load device groups: %w", err)
	}
	for _, g := range groups {
		count, err := s.countOf(ctx, `SELECT COUNT(*) FROM device_group_members WHERE device_group_id = ?`, argOf(g.Get("id")))
		if err != nil {
			return nil, fmt.Errorf("count members of a device group: %w", err)
		}
		g.Set("member_count", count)
		kindDefaults(g)
		if err := s.accessLists(ctx, g); err != nil {
			return nil, fmt.Errorf("load access lists of a device group: %w", err)
		}
	}
	return groups, nil
}

// DeviceGroupByGUID returns the device group with the guid, its kind and
// its access lists, or nil.
func (s *Service) DeviceGroupByGUID(ctx context.Context, guid string) (*goapi.Object, error) {
	group, err := readRow(ctx, s.db, `SELECT * FROM device_groups WHERE guid = ?`, guid)
	if err != nil {
		return nil, fmt.Errorf("load device group %q: %w", guid, err)
	}
	if group == nil {
		return nil, nil
	}
	kindDefaults(group)
	if err := s.accessLists(ctx, group); err != nil {
		return nil, fmt.Errorf("load access lists of device group %q: %w", guid, err)
	}
	return group, nil
}

// GroupData are the fields of a device group to write. An undefined field
// is not written by UpdateDeviceGroup.
type GroupData struct {
	GUID, Name, Note, TeamID, SourceType, TagFilter jsbind.Value
}

// newGUID is a random version 4 UUID, as crypto.randomUUID writes it.
func newGUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

// isTag reports data.source_type === 'tag'.
func isTag(v jsbind.Value) bool { return v.IsString() && v.Str() == "tag" }

// CreateDeviceGroup stores a device group and returns its row. Without a
// guid it gets a random one; a kind other than "tag" is "manual", and only
// a tag group keeps its tag filter.
func (s *Service) CreateDeviceGroup(ctx context.Context, data GroupData) (*goapi.Object, error) {
	guid := data.GUID
	if !guid.Truthy() {
		random, err := newGUID()
		if err != nil {
			return nil, fmt.Errorf("make a guid: %w", err)
		}
		guid = jsbind.String(random)
	}
	sourceType, tagFilter := jsbind.String("manual"), jsbind.String("")
	if isTag(data.SourceType) {
		sourceType, tagFilter = jsbind.String("tag"), data.TagFilter.Or(jsbind.String(""))
	}
	args, err := s.args(6, guid, data.Name, data.Note.Or(jsbind.String("")), data.TeamID.Or(jsbind.String("")), sourceType, tagFilter)
	if err != nil {
		return nil, err
	}
	const insert = `INSERT INTO device_groups (guid, name, note, team_id, source_type, tag_filter) VALUES (?, ?, ?, ?, ?, ?)`
	if s.db.IsPostgres() {
		row, err := readRow(ctx, s.db, insert+` RETURNING *`, args...)
		if err != nil {
			return nil, fmt.Errorf("create device group: %w", err)
		}
		return row, nil
	}
	if _, err := s.db.Exec(ctx, insert, args...); err != nil {
		return nil, fmt.Errorf("create device group: %w", err)
	}
	return readRow(ctx, s.db, `SELECT * FROM device_groups WHERE guid = ?`, args[0])
}

// UpdateDeviceGroup writes the fields of data that are not undefined and
// returns the group's row; nil when no field was given or the group is
// gone. A kind other than "tag" is "manual"; the tag filter is written
// as text cut to 50 characters.
func (s *Service) UpdateDeviceGroup(ctx context.Context, guid string, data GroupData) (*goapi.Object, error) {
	var sets []string
	var values []jsbind.Value
	add := func(column string, v jsbind.Value) {
		sets = append(sets, column+" = ?")
		values = append(values, v)
	}
	if !data.Name.IsUndefined() {
		add("name", data.Name)
	}
	if !data.Note.IsUndefined() {
		add("note", data.Note)
	}
	if !data.TeamID.IsUndefined() {
		add("team_id", data.TeamID)
	}
	if !data.SourceType.IsUndefined() {
		kind := jsbind.String("manual")
		if isTag(data.SourceType) {
			kind = jsbind.String("tag")
		}
		add("source_type", kind)
	}
	if !data.TagFilter.IsUndefined() {
		filter := ""
		if data.TagFilter.Truthy() {
			filter = data.TagFilter.ToString()
		}
		add("tag_filter", jsbind.String(cut(filter, 50)))
	}
	if len(sets) == 0 {
		return nil, nil
	}
	args, err := s.args(len(values)+1, append(values, jsbind.String(guid))...)
	if err != nil {
		return nil, err
	}
	update := `UPDATE device_groups SET ` + strings.Join(sets, ", ") + ` WHERE guid = ?`
	if s.db.IsPostgres() {
		row, err := readRow(ctx, s.db, update+` RETURNING *`, args...)
		if err != nil {
			return nil, fmt.Errorf("update device group %q: %w", guid, err)
		}
		return row, nil
	}
	if _, err := s.db.Exec(ctx, update, args...); err != nil {
		return nil, fmt.Errorf("update device group %q: %w", guid, err)
	}
	return readRow(ctx, s.db, `SELECT * FROM device_groups WHERE guid = ?`, guid)
}

// groupID is the id of the device group with the guid; ok is false when
// there is none.
func (s *Service) groupID(ctx context.Context, q store.Querier, table, guid string) (id int64, ok bool, err error) {
	err = q.QueryRow(ctx, `SELECT id FROM `+table+` WHERE guid = ?`, guid).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	return id, err == nil, err
}

// DeleteDeviceGroup deletes a device group with its members and access
// lists. An unknown guid is no error.
func (s *Service) DeleteDeviceGroup(ctx context.Context, guid string) error {
	id, ok, err := s.groupID(ctx, s.db, "device_groups", guid)
	if err != nil {
		return fmt.Errorf("load device group %q: %w", guid, err)
	}
	if !ok {
		return nil
	}
	for _, query := range []string{
		`DELETE FROM device_group_members WHERE device_group_id = ?`,
		`DELETE FROM device_group_user_access WHERE device_group_id = ?`,
		`DELETE FROM device_group_user_group_access WHERE device_group_id = ?`,
	} {
		if _, err := s.db.Exec(ctx, query, id); err != nil {
			return fmt.Errorf("delete device group %q: %w", guid, err)
		}
	}
	if _, err := s.db.Exec(ctx, `DELETE FROM device_groups WHERE guid = ?`, guid); err != nil {
		return fmt.Errorf("delete device group %q: %w", guid, err)
	}
	return nil
}

// SetDeviceGroupUserAccess replaces the usernames on a device group's
// access list (console/access does the writing) and returns the group;
// nil for an unknown guid.
func (s *Service) SetDeviceGroupUserAccess(ctx context.Context, guid string, usernames []string) (*goapi.Object, error) {
	if _, ok, err := s.groupID(ctx, s.db, "device_groups", guid); err != nil || !ok {
		if err != nil {
			return nil, fmt.Errorf("load device group %q: %w", guid, err)
		}
		return nil, nil
	}
	if err := s.access.SetDeviceGroupUserAccess(ctx, guid, usernames); err != nil {
		return nil, err
	}
	return s.DeviceGroupByGUID(ctx, guid)
}

// SetDeviceGroupUserGroupAccess replaces the user groups on a device
// group's access list, in one transaction, and returns the group; nil for
// an unknown guid. Guids are trimmed, without empty ones and repeats, at
// most 100; guids of no user group are left out.
func (s *Service) SetDeviceGroupUserGroupAccess(ctx context.Context, guid string, groupGUIDs []string) (*goapi.Object, error) {
	id, ok, err := s.groupID(ctx, s.db, "device_groups", guid)
	if err != nil {
		return nil, fmt.Errorf("load device group %q: %w", guid, err)
	}
	if !ok {
		return nil, nil
	}
	unique := uniqueTrimmed(groupGUIDs, 100)
	err = s.db.InTx(ctx, func(tx *store.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM device_group_user_group_access WHERE device_group_id = ?`, id); err != nil {
			return err
		}
		insert := tx.Pick(
			`INSERT OR IGNORE INTO device_group_user_group_access (device_group_id, user_group_id) VALUES (?, ?)`,
			`INSERT INTO device_group_user_group_access (device_group_id, user_group_id) VALUES (?, ?) ON CONFLICT DO NOTHING`)
		for _, g := range unique {
			userGroup, found, err := s.groupID(ctx, tx, "user_groups", g)
			if err != nil {
				return err
			}
			if !found {
				continue
			}
			if _, err := tx.Exec(ctx, insert, id, userGroup); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("set user group access of device group %q: %w", guid, err)
	}
	return s.DeviceGroupByGUID(ctx, guid)
}

// uniqueTrimmed trims each item, drops empty ones and repeats and keeps at
// most limit.
func uniqueTrimmed(items []string, limit int) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, item := range items {
		item = jsbind.Trim(item)
		if item == "" || seen[item] {
			continue
		}
		seen[item] = true
		out = append(out, item)
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// ---- User groups ----

// AllUserGroups returns every user group, ordered by name, with
// member_count.
func (s *Service) AllUserGroups(ctx context.Context) ([]*goapi.Object, error) {
	groups, err := readRows(ctx, s.db, `SELECT * FROM user_groups ORDER BY name ASC`)
	if err != nil {
		return nil, fmt.Errorf("load user groups: %w", err)
	}
	for _, g := range groups {
		count, err := s.countOf(ctx, `SELECT COUNT(*) FROM user_group_members WHERE user_group_id = ?`, argOf(g.Get("id")))
		if err != nil {
			return nil, fmt.Errorf("count members of a user group: %w", err)
		}
		g.Set("member_count", count)
	}
	return groups, nil
}

// UserGroupByGUID returns the user group with the guid, or nil.
func (s *Service) UserGroupByGUID(ctx context.Context, guid string) (*goapi.Object, error) {
	row, err := readRow(ctx, s.db, `SELECT * FROM user_groups WHERE guid = ?`, guid)
	if err != nil {
		return nil, fmt.Errorf("load user group %q: %w", guid, err)
	}
	return row, nil
}

// UserGroupData are the fields of a user group to write. An undefined field
// is not written by UpdateUserGroup.
type UserGroupData struct {
	GUID, Name, Note, TeamID jsbind.Value
}

// CreateUserGroup stores a user group and returns its row. Without a guid
// it gets a random one; a note or team id that is empty is stored as "".
func (s *Service) CreateUserGroup(ctx context.Context, data UserGroupData) (*goapi.Object, error) {
	guid := data.GUID
	if !guid.Truthy() {
		random, err := newGUID()
		if err != nil {
			return nil, fmt.Errorf("make a guid: %w", err)
		}
		guid = jsbind.String(random)
	}
	args, err := s.args(4, guid, data.Name, data.Note.Or(jsbind.String("")), data.TeamID.Or(jsbind.String("")))
	if err != nil {
		return nil, err
	}
	const insert = `INSERT INTO user_groups (guid, name, note, team_id) VALUES (?, ?, ?, ?)`
	if s.db.IsPostgres() {
		row, err := readRow(ctx, s.db, insert+` RETURNING *`, args...)
		if err != nil {
			return nil, fmt.Errorf("create user group: %w", err)
		}
		return row, nil
	}
	if _, err := s.db.Exec(ctx, insert, args...); err != nil {
		return nil, fmt.Errorf("create user group: %w", err)
	}
	return readRow(ctx, s.db, `SELECT * FROM user_groups WHERE guid = ?`, args[0])
}

// UpdateUserGroup writes the fields of data that are not undefined and
// returns the group's row; nil when no field was given or the group is
// gone.
func (s *Service) UpdateUserGroup(ctx context.Context, guid string, data UserGroupData) (*goapi.Object, error) {
	var sets []string
	var values []jsbind.Value
	for _, field := range []struct {
		column string
		value  jsbind.Value
	}{{"name", data.Name}, {"note", data.Note}, {"team_id", data.TeamID}} {
		if !field.value.IsUndefined() {
			sets = append(sets, field.column+" = ?")
			values = append(values, field.value)
		}
	}
	if len(sets) == 0 {
		return nil, nil
	}
	args, err := s.args(len(values)+1, append(values, jsbind.String(guid))...)
	if err != nil {
		return nil, err
	}
	update := `UPDATE user_groups SET ` + strings.Join(sets, ", ") + ` WHERE guid = ?`
	if s.db.IsPostgres() {
		row, err := readRow(ctx, s.db, update+` RETURNING *`, args...)
		if err != nil {
			return nil, fmt.Errorf("update user group %q: %w", guid, err)
		}
		return row, nil
	}
	if _, err := s.db.Exec(ctx, update, args...); err != nil {
		return nil, fmt.Errorf("update user group %q: %w", guid, err)
	}
	return readRow(ctx, s.db, `SELECT * FROM user_groups WHERE guid = ?`, guid)
}

// DeleteUserGroup deletes a user group with its members and the device
// group access lists that name it. An unknown guid is no error.
func (s *Service) DeleteUserGroup(ctx context.Context, guid string) error {
	id, ok, err := s.groupID(ctx, s.db, "user_groups", guid)
	if err != nil {
		return fmt.Errorf("load user group %q: %w", guid, err)
	}
	if !ok {
		return nil
	}
	for _, query := range []string{
		`DELETE FROM user_group_members WHERE user_group_id = ?`,
		`DELETE FROM device_group_user_group_access WHERE user_group_id = ?`,
	} {
		if _, err := s.db.Exec(ctx, query, id); err != nil {
			return fmt.Errorf("delete user group %q: %w", guid, err)
		}
	}
	if _, err := s.db.Exec(ctx, `DELETE FROM user_groups WHERE guid = ?`, guid); err != nil {
		return fmt.Errorf("delete user group %q: %w", guid, err)
	}
	return nil
}

// UserGroupsForUser returns the rows of the user groups an account is a
// member of, ordered by name.
func (s *Service) UserGroupsForUser(ctx context.Context, userID int64) ([]*goapi.Object, error) {
	rows, err := readRows(ctx, s.db, `
		SELECT ug.* FROM user_groups ug
		INNER JOIN user_group_members ugm ON ug.id = ugm.user_group_id
		WHERE ugm.user_id = ?
		ORDER BY ug.name ASC`, userID)
	if err != nil {
		return nil, fmt.Errorf("load user groups of user %d: %w", userID, err)
	}
	return rows, nil
}

// SetUserGroupMemberships makes an account a member of exactly the user
// groups with the guids, in one transaction, and returns its groups. Guids
// are trimmed, without empty ones and repeats, at most 100; guids of no
// user group are left out. An unknown account is left alone and has no
// groups.
func (s *Service) SetUserGroupMemberships(ctx context.Context, userID int64, groupGUIDs []string) ([]*goapi.Object, error) {
	var id int64
	err := s.db.QueryRow(ctx, `SELECT id FROM users WHERE id = ?`, userID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return []*goapi.Object{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load user %d: %w", userID, err)
	}
	unique := uniqueTrimmed(groupGUIDs, 100)
	err = s.db.InTx(ctx, func(tx *store.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM user_group_members WHERE user_id = ?`, id); err != nil {
			return err
		}
		insert := tx.Pick(
			`INSERT OR IGNORE INTO user_group_members (user_group_id, user_id) VALUES (?, ?)`,
			`INSERT INTO user_group_members (user_group_id, user_id) VALUES (?, ?) ON CONFLICT DO NOTHING`)
		for _, guid := range unique {
			group, found, err := s.groupID(ctx, tx, "user_groups", guid)
			if err != nil {
				return err
			}
			if !found {
				continue
			}
			if _, err := tx.Exec(ctx, insert, group, id); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("set user groups of user %d: %w", userID, err)
	}
	return s.UserGroupsForUser(ctx, id)
}

// ---- Accounts ----

// UserIDByID returns the id of the account with the id, which the caller
// read as a number; ok is false when there is none.
func (s *Service) UserIDByID(ctx context.Context, id float64) (userID int64, ok bool, err error) {
	args, err := s.args(1, jsbind.Number(id))
	if err != nil {
		return 0, false, err
	}
	err = s.db.QueryRow(ctx, `SELECT id FROM users WHERE id = ?`, args...).Scan(&userID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("load user: %w", err)
	}
	return userID, true, nil
}
