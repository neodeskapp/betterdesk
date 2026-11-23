package devicegroups

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"

	"golang.org/x/text/collate"
	"golang.org/x/text/language"

	"github.com/neodeskapp/neodesk/neodesk-server/console/access"
	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
	"github.com/neodeskapp/neodesk/neodesk-server/console/jsbind"
	"github.com/neodeskapp/neodesk/neodesk-server/console/session"
)

// What the RustDesk client API (console/routes/rustdeskapi) reads and
// writes of groups and address books: the dbAdapter methods its routes
// called, for both databases.

// ---- Scope helpers ----

// UserAccessContext is getUserAccessContext: the account with the guids
// of its user groups.
func (s *Service) UserAccessContext(ctx context.Context, user *session.User) *access.ScopeUser {
	return s.access.UserAccessContext(ctx, ScopeUser(user))
}

// GroupAllowedForUser is groupAllowedForUser for a device group row (or
// an object with allowed_users and allowed_groups).
func GroupAllowedForUser(group *goapi.Object, user *access.ScopeUser) bool {
	return access.GroupAllowedForUser(ScopeGroup(group), user)
}

// GroupPeerIDList is getGroupPeerIds in the order of its set: a group that
// stands for a folder has the devices of the list in that folder; any
// other has its stored members, then the devices of the list that carry
// its tag when it follows one. A failed read of the members is no member.
func (s *Service) GroupPeerIDList(ctx context.Context, group *goapi.Object, devices []*goapi.Object) []string {
	var ids []string
	seen := map[string]bool{}
	add := func(id string) {
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	folder, isFolder := groupFolder(group)
	scoped := ScopeDevices(devices)
	if isFolder {
		for _, d := range scoped {
			if d.FolderID != nil && float64(*d.FolderID) == folder {
				add(d.ID)
			}
		}
		return ids
	}
	if guid := group.Get("guid"); jsTruthy(guid) {
		if members, err := s.access.DeviceGroupMembers(ctx, jsText(guid)); err == nil {
			for _, id := range members {
				add(id)
			}
		}
	}
	tag := jsbind.ToLower(jsbind.Trim(textOr(group.Get("tag_filter"), "")))
	if textOr(group.Get("source_type"), "manual") == "tag" && jsTruthy(group.Get("tag_filter")) && tag != "" {
		for _, d := range scoped {
			for _, t := range d.Tags {
				if jsbind.ToLower(t) == tag {
					add(d.ID)
					break
				}
			}
		}
	}
	return ids
}

// groupFolder is getGroupFolderId: the group's folder_id as parseInt read
// it, else the number of a guid folder_<n>.
func groupFolder(group *goapi.Object) (float64, bool) {
	if explicit := Plain(group.Get("folder_id")); explicit != nil && explicit != "" {
		if id, ok := parseIntOf(explicit); ok && isFinite(id) {
			return id, true
		}
	}
	return FolderIDFromGroupGUID(Plain(group.Get("guid")))
}

// SortLikeLocaleCompare orders texts as Array.prototype.sort with
// String.prototype.localeCompare did.
func SortLikeLocaleCompare(texts []string) {
	c := collate.New(language.English)
	sort.SliceStable(texts, func(i, j int) bool {
		return c.CompareString(asciiDigits(texts[i]), asciiDigits(texts[j])) < 0
	})
}

// ---- Device groups ----

// ErrNotFound is the error a lookup by reference fails with when nothing
// matches; its text is what the old route answered.
type ErrNotFound struct{ Text string }

func (e ErrNotFound) Error() string { return e.Text }

// ResolveDeviceGroupAssignmentKey is resolveDeviceGroupAssignmentKey: the
// guid of the device group whose guid or name is ref (trimmed).
func (s *Service) ResolveDeviceGroupAssignmentKey(ctx context.Context, ref string) (string, error) {
	key := jsbind.Trim(ref)
	if key == "" {
		return "", ErrNotFound{Text: "empty device group reference"}
	}
	var guid string
	err := s.db.QueryRow(ctx, `SELECT guid FROM device_groups WHERE guid = ? OR name = ? LIMIT 1`, key, key).Scan(&guid)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound{Text: "device group not found"}
	}
	if err != nil {
		return "", fmt.Errorf("resolve device group %q: %w", key, err)
	}
	return guid, nil
}

// ---- Address books ----

// usesUsernameAddressBooks reports an address_books table keyed by user
// name (the signal server's) rather than by user id (the console's on
// SQLite). On PostgreSQL the old console only knew the first.
func (s *Service) usesUsernameAddressBooks() bool {
	return s.db.IsPostgres() || s.db.Shape().Table("address_books").Has("username")
}

func (s *Service) usernameOf(ctx context.Context, userID int64) (string, bool, error) {
	var name string
	err := s.db.QueryRow(ctx, `SELECT username FROM users WHERE id = ?`, userID).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("load user %d: %w", userID, err)
	}
	return name, true, nil
}

// AddressBook is getAddressBook: the data of the account's address book of
// the type ("legacy" or "personal"); found is false when there is none.
// data is the column as the driver gave it: text, or nil for NULL.
func (s *Service) AddressBook(ctx context.Context, userID int64, abType string) (data any, found bool, err error) {
	query, key := `SELECT data FROM address_books WHERE user_id = ? AND ab_type = ?`, any(userID)
	if s.usesUsernameAddressBooks() {
		name, ok, err := s.usernameOf(ctx, userID)
		if err != nil || !ok {
			return nil, false, err
		}
		query, key = `SELECT data FROM address_books WHERE username = ? AND ab_type = ?`, name
	}
	var cell jsbind.Cell
	err = s.db.QueryRow(ctx, query, key, abType).Scan(&cell)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("load the address book of user %d: %w", userID, err)
	}
	return Plain(cell), true, nil
}

// SaveAddressBook is saveAddressBook: it stores the data of the account's
// address book of the type. An account that is gone saves nothing where
// the table is keyed by name.
func (s *Service) SaveAddressBook(ctx context.Context, userID int64, abType, data string) error {
	if s.usesUsernameAddressBooks() {
		name, ok, err := s.usernameOf(ctx, userID)
		if err != nil || !ok {
			return err
		}
		_, err = s.db.Exec(ctx, s.db.Pick(`
                    INSERT INTO address_books (username, ab_type, data, updated_at)
                    VALUES (?, ?, ?, datetime('now'))
                    ON CONFLICT(username, ab_type) DO UPDATE SET data = excluded.data, updated_at = datetime('now')
                `, `INSERT INTO address_books (username, ab_type, data, updated_at) VALUES ($1, $2, $3, NOW())
                ON CONFLICT(username, ab_type) DO UPDATE SET data = $3, updated_at = NOW()`), name, abType, data)
		if err != nil {
			return fmt.Errorf("save the address book of user %d: %w", userID, err)
		}
		return nil
	}
	_, err := s.db.Exec(ctx, `
                INSERT INTO address_books (user_id, ab_type, data, updated_at)
                VALUES (?, ?, ?, datetime('now'))
                ON CONFLICT(user_id, ab_type) DO UPDATE SET data = excluded.data, updated_at = datetime('now')
            `, userID, abType, data)
	if err != nil {
		return fmt.Errorf("save the address book of user %d: %w", userID, err)
	}
	return nil
}

// AddressBookTags is getAddressBookTags: JSON.parse(data).tags || [] of
// the account's legacy address book, which may be any JSON value; [] when
// there is no book or its data is no JSON.
func (s *Service) AddressBookTags(ctx context.Context, userID int64) (any, error) {
	data, found, err := s.AddressBook(ctx, userID, "legacy")
	if err != nil || !found {
		return []any{}, err
	}
	text, ok := data.(string)
	if !ok {
		// JSON.parse(null) is null, and null.tags throws inside the try
		return []any{}, nil
	}
	parsed, err := goapi.Parse([]byte(strings.TrimFunc(text, isJSONSpace)))
	if err != nil {
		return []any{}, nil
	}
	obj, ok := parsed.(*goapi.Object)
	if !ok {
		// null.tags throws, the tags of a number or a text are undefined
		return []any{}, nil
	}
	if tags := obj.Get("tags"); jsTruthy(tags) {
		return tags, nil
	}
	return []any{}, nil
}

// isJSONSpace is the white space JSON.parse allows around a value.
func isJSONSpace(r rune) bool { return r == ' ' || r == '\t' || r == '\n' || r == '\r' }
