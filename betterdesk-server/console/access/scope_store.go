package access

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/unitronix/betterdesk-server/console/store"
)

// DeviceGroup is a device group with its access lists.
type DeviceGroup struct {
	ID         int64   `json:"id"`
	GUID       string  `json:"guid"`
	Name       string  `json:"name"`
	Note       *string `json:"note"`
	TeamID     *string `json:"team_id"`
	SourceType string  `json:"source_type"`
	TagFilter  string  `json:"tag_filter"`
	// CreatedAt is the creation time as each database gives it: SQLite's
	// "2006-01-02 15:04:05" text, PostgreSQL's time as an ISO string with
	// milliseconds.
	CreatedAt *string `json:"created_at"`
	// MemberCount is the number of stored members when the group came from
	// AllDeviceGroups, and the number of devices EnrichGroups found. A group
	// loaded on its own has none and leaves the key out.
	MemberCount *int `json:"member_count,omitempty"`
	// AllowedUsers are the usernames on the group's access list.
	AllowedUsers []string `json:"allowed_users"`
	// AllowedGroups are the guids of the user groups on the access list;
	// AllowedUserGroups are the same groups with their names.
	AllowedGroups     []string   `json:"allowed_groups"`
	AllowedUserGroups []GroupRef `json:"allowed_user_groups"`
	// FolderID makes the group stand for a folder whatever its guid says.
	// It is never stored.
	FolderID *int64 `json:"-"`
}

// Folder is a device folder.
type Folder struct {
	ID   int64
	Name string
}

// dbTime renders a timestamp column: text stays as it is, a time becomes
// an ISO string in UTC with milliseconds.
func dbTime(v any) *string {
	var out string
	switch x := v.(type) {
	case nil:
		return nil
	case time.Time:
		out = x.UTC().Format("2006-01-02T15:04:05.000Z")
	case string:
		out = x
	case []byte:
		out = string(x)
	default:
		out = fmt.Sprint(x)
	}
	return &out
}

func nullString(v sql.NullString) *string {
	if !v.Valid {
		return nil
	}
	return &v.String
}

const deviceGroupColumns = `id, guid, name, note, team_id, source_type, tag_filter, created_at`

func scanDeviceGroup(scan func(dest ...any) error) (DeviceGroup, error) {
	var g DeviceGroup
	var note, teamID, sourceType, tagFilter sql.NullString
	var createdAt any
	if err := scan(&g.ID, &g.GUID, &g.Name, &note, &teamID, &sourceType, &tagFilter, &createdAt); err != nil {
		return g, err
	}
	g.Note, g.TeamID, g.CreatedAt = nullString(note), nullString(teamID), dbTime(createdAt)
	g.SourceType, g.TagFilter = sourceType.String, tagFilter.String
	if g.SourceType == "" {
		g.SourceType = "manual"
	}
	g.AllowedUsers, g.AllowedGroups, g.AllowedUserGroups = []string{}, []string{}, []GroupRef{}
	return g, nil
}

// countMembers loads the number of stored members of a group.
func (s *Service) countMembers(ctx context.Context, g *DeviceGroup) error {
	var count int
	if err := s.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM device_group_members WHERE device_group_id = ?`, g.ID).Scan(&count); err != nil {
		return err
	}
	g.MemberCount = &count
	return nil
}

// fillAccessLists loads the access lists of a group.
func (s *Service) fillAccessLists(ctx context.Context, g *DeviceGroup) error {
	users, err := s.db.Query(ctx, `
		SELECT u.username FROM device_group_user_access a
		INNER JOIN users u ON u.id = a.user_id
		WHERE a.device_group_id = ?
		ORDER BY u.username ASC`, g.ID)
	if err != nil {
		return err
	}
	defer users.Close()
	for users.Next() {
		var name string
		if err := users.Scan(&name); err != nil {
			return err
		}
		g.AllowedUsers = append(g.AllowedUsers, name)
	}
	if err := users.Err(); err != nil {
		return err
	}
	groups, err := s.db.Query(ctx, `
		SELECT ug.guid, ug.name FROM device_group_user_group_access a
		INNER JOIN user_groups ug ON ug.id = a.user_group_id
		WHERE a.device_group_id = ?
		ORDER BY ug.name ASC`, g.ID)
	if err != nil {
		return err
	}
	defer groups.Close()
	for groups.Next() {
		var ref GroupRef
		if err := groups.Scan(&ref.GUID, &ref.Name); err != nil {
			return err
		}
		g.AllowedGroups = append(g.AllowedGroups, ref.GUID)
		g.AllowedUserGroups = append(g.AllowedUserGroups, ref)
	}
	return groups.Err()
}

// AllDeviceGroups returns every device group with its member count and
// access lists, ordered by name.
func (s *Service) AllDeviceGroups(ctx context.Context) ([]DeviceGroup, error) {
	rows, err := s.db.Query(ctx, `SELECT `+deviceGroupColumns+` FROM device_groups ORDER BY name ASC`)
	if err != nil {
		return nil, fmt.Errorf("load device groups: %w", err)
	}
	out := []DeviceGroup{}
	for rows.Next() {
		g, err := scanDeviceGroup(rows.Scan)
		if err != nil {
			rows.Close()
			return nil, fmt.Errorf("read device group: %w", err)
		}
		out = append(out, g)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, fmt.Errorf("load device groups: %w", err)
	}
	for i := range out {
		if err := s.countMembers(ctx, &out[i]); err != nil {
			return nil, fmt.Errorf("count members of device group %q: %w", out[i].GUID, err)
		}
		if err := s.fillAccessLists(ctx, &out[i]); err != nil {
			return nil, fmt.Errorf("load access lists of device group %q: %w", out[i].GUID, err)
		}
	}
	return out, nil
}

// DeviceGroupByGUID returns the device group with the guid and its access
// lists, or nil when there is none.
func (s *Service) DeviceGroupByGUID(ctx context.Context, guid string) (*DeviceGroup, error) {
	g, err := scanDeviceGroup(s.db.QueryRow(ctx, `SELECT `+deviceGroupColumns+` FROM device_groups WHERE guid = ?`, guid).Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load device group %q: %w", guid, err)
	}
	if err := s.fillAccessLists(ctx, &g); err != nil {
		return nil, fmt.Errorf("load access lists of device group %q: %w", guid, err)
	}
	return &g, nil
}

// DeviceGroupMembers returns the device ids stored as members of a group;
// none for an unknown group.
func (s *Service) DeviceGroupMembers(ctx context.Context, guid string) ([]string, error) {
	rows, err := s.db.Query(ctx, `
		SELECT m.peer_id FROM device_group_members m
		INNER JOIN device_groups g ON g.id = m.device_group_id
		WHERE g.guid = ?`, guid)
	if err != nil {
		return nil, fmt.Errorf("load members of device group %q: %w", guid, err)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("read member of device group %q: %w", guid, err)
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("load members of device group %q: %w", guid, err)
	}
	return out, nil
}

// createFolderMirrorGroup stores the device group that carries a folder's
// access list.
func (s *Service) createFolderMirrorGroup(ctx context.Context, guid, name string) (*DeviceGroup, error) {
	if _, err := s.db.Exec(ctx,
		`INSERT INTO device_groups (guid, name, note, team_id, source_type, tag_filter) VALUES (?, ?, ?, ?, ?, ?)`,
		guid, name, folderGroupNote, "", "manual", ""); err != nil {
		return nil, fmt.Errorf("create device group %q: %w", guid, err)
	}
	return s.DeviceGroupByGUID(ctx, guid)
}

// refreshFolderMirrorGroup gives a folder's mirror group the folder's
// current name and its fixed note and kind.
func (s *Service) refreshFolderMirrorGroup(ctx context.Context, guid, name string) error {
	if _, err := s.db.Exec(ctx,
		`UPDATE device_groups SET name = ?, note = ?, source_type = ?, tag_filter = ? WHERE guid = ?`,
		name, folderGroupNote, "manual", "", guid); err != nil {
		return fmt.Errorf("update device group %q: %w", guid, err)
	}
	return nil
}

// SetDeviceGroupUserAccess replaces the usernames on a device group's
// access list. Names are trimmed; repeats and names of no account are
// dropped. An unknown group is left alone.
func (s *Service) SetDeviceGroupUserAccess(ctx context.Context, guid string, usernames []string) error {
	var groupID int64
	err := s.db.QueryRow(ctx, `SELECT id FROM device_groups WHERE guid = ?`, guid).Scan(&groupID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("load device group %q: %w", guid, err)
	}
	names := uniqueTrimmed(usernames, len(usernames))
	err = s.db.InTx(ctx, func(tx *store.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM device_group_user_access WHERE device_group_id = ?`, groupID); err != nil {
			return err
		}
		insert := tx.Pick(
			`INSERT OR IGNORE INTO device_group_user_access (device_group_id, user_id) VALUES (?, ?)`,
			`INSERT INTO device_group_user_access (device_group_id, user_id) VALUES (?, ?) ON CONFLICT DO NOTHING`)
		for _, name := range names {
			var userID int64
			err := tx.QueryRow(ctx, `SELECT id FROM users WHERE username = ?`, name).Scan(&userID)
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, insert, groupID, userID); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("set user access of device group %q: %w", guid, err)
	}
	return nil
}

// AllFolders returns the device folders in display order.
func (s *Service) AllFolders(ctx context.Context) ([]Folder, error) {
	rows, err := s.db.Query(ctx, `SELECT id, name FROM folders ORDER BY sort_order, name`)
	if err != nil {
		return nil, fmt.Errorf("load folders: %w", err)
	}
	defer rows.Close()
	var out []Folder
	for rows.Next() {
		var f Folder
		if err := rows.Scan(&f.ID, &f.Name); err != nil {
			return nil, fmt.Errorf("read folder: %w", err)
		}
		out = append(out, f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("load folders: %w", err)
	}
	return out, nil
}

// UserPeerGrants returns the device ids granted to an account directly,
// sorted.
func (s *Service) UserPeerGrants(ctx context.Context, userID int64) ([]string, error) {
	rows, err := s.db.Query(ctx, `SELECT peer_id FROM user_peer_grants WHERE user_id = ? ORDER BY peer_id ASC`, userID)
	if err != nil {
		return nil, fmt.Errorf("load device grants of user %d: %w", userID, err)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("read device grant of user %d: %w", userID, err)
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("load device grants of user %d: %w", userID, err)
	}
	return out, nil
}

// setUserPeerGrants replaces an account's direct grants. On PostgreSQL the
// old grants go and the new ones come in one transaction. On SQLite the old
// grants are deleted first, on their own, and stay deleted when storing the
// new ones fails.
func (s *Service) setUserPeerGrants(ctx context.Context, userID int64, peerIDs []string) error {
	const remove = `DELETE FROM user_peer_grants WHERE user_id = ?`
	if !s.db.IsPostgres() {
		if _, err := s.db.Exec(ctx, remove, userID); err != nil {
			return fmt.Errorf("clear device grants of user %d: %w", userID, err)
		}
	}
	err := s.db.InTx(ctx, func(tx *store.Tx) error {
		if s.db.IsPostgres() {
			if _, err := tx.Exec(ctx, remove, userID); err != nil {
				return err
			}
		}
		for _, id := range peerIDs {
			if _, err := tx.Exec(ctx, `INSERT INTO user_peer_grants (user_id, peer_id) VALUES (?, ?)`, userID, id); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("store device grants of user %d: %w", userID, err)
	}
	return nil
}

// setting returns a stored console setting, "" when it is not set.
func (s *Service) setting(ctx context.Context, key string) (string, error) {
	var value string
	err := s.db.QueryRow(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("load setting %s: %w", key, err)
	}
	return value, nil
}
