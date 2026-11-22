package access

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/unitronix/betterdesk-server/logging"
)

// Which devices an account may see. Admin roles see everything. For the
// rest, device groups with an access list (allowed usernames or allowed
// user groups) and per-account grants decide, on top of the default mode:
// "open" shows every device that is not in a group closed to the account,
// "restricted" shows only what the account was given.

const (
	scopeDefaultSetting  = "device_scope_default"
	scopeDefaultCacheTTL = 30 * time.Second
	scopeRestricted      = "restricted"
	maxAccessListEntries = 100
	maxFolderIDs         = 200
	maxPeerGrants        = 500
	folderGroupNote      = "BetterDesk folder access scope"
	maxUnknownPeerLogs   = 20
)

// ScopeUser is the account a device scope is computed for.
type ScopeUser struct {
	ID       int64
	Username string
	Role     string
	// UserGroups holds the guids of the account's user groups. nil means
	// they are not known yet and are looked up; an empty list means the
	// account is in no group.
	UserGroups []string
}

// Device is what the scope rules read of a device.
type Device struct {
	ID string
	// FolderID is the folder the device is in, nil for none.
	FolderID *int64
	// Tags are the device's tags, as NormalizeTags returns them.
	Tags []string
}

// DeviceScope is the set of device ids an account may see. A nil
// *DeviceScope means there is no limit.
type DeviceScope struct {
	ids map[string]struct{}
}

func newDeviceScope() *DeviceScope { return &DeviceScope{ids: make(map[string]struct{})} }

// Unlimited reports whether the scope has no limit.
func (s *DeviceScope) Unlimited() bool { return s == nil }

// Has reports whether the device id is in the scope. A scope without a
// limit has every id.
func (s *DeviceScope) Has(id string) bool {
	if s == nil {
		return true
	}
	_, ok := s.ids[id]
	return ok
}

// Size is the number of ids in a limited scope.
func (s *DeviceScope) Size() int {
	if s == nil {
		return 0
	}
	return len(s.ids)
}

// IDs returns the ids of a limited scope, sorted.
func (s *DeviceScope) IDs() []string {
	if s == nil {
		return nil
	}
	out := make([]string, 0, len(s.ids))
	for id := range s.ids {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func (s *DeviceScope) add(id string) { s.ids[id] = struct{}{} }

// scopeExempt reports whether a role sees every device.
func scopeExempt(role string) bool { return IsAdminRole(role) }

// NormalizeTags reads a device's tags from a list, a JSON list in text or
// comma-separated text. Tags are trimmed and empty ones dropped.
func NormalizeTags(value any) []string {
	clean := func(items []string) []string {
		out := make([]string, 0, len(items))
		for _, item := range items {
			if item = jsTrim(item); item != "" {
				out = append(out, item)
			}
		}
		return out
	}
	fromList := func(list []any) []string {
		items := make([]string, len(list))
		for i, item := range list {
			items[i] = jsString(item)
		}
		return clean(items)
	}
	switch x := value.(type) {
	case []string:
		return clean(x)
	case []any:
		return fromList(x)
	case string:
		if x == "" {
			return []string{}
		}
		var parsed any
		if json.Unmarshal([]byte(x), &parsed) == nil {
			if list, ok := parsed.([]any); ok {
				return fromList(list)
			}
		}
		return clean(strings.Split(x, ","))
	}
	return []string{}
}

// NormalizeUsernames reads a submitted access list of usernames from a
// list or comma-separated text: trimmed, without repeats, at most 100.
func NormalizeUsernames(value any) []string {
	return uniqueTrimmed(listOrSplit(value), maxAccessListEntries)
}

// StoredUsernames reads a stored access list of usernames the way
// NormalizeUsernames does, keeping every entry: a check must see the
// whole list.
func StoredUsernames(value any) []string {
	return uniqueTrimmed(listOrSplit(value), 0)
}

// NormalizeGroupGUIDs reads a submitted list of user group guids from a
// list (of guids or of objects with a guid) or comma-separated text:
// trimmed, without repeats, at most 100.
func NormalizeGroupGUIDs(value any) []string {
	return groupGUIDs(value, maxAccessListEntries)
}

// StoredGroupGUIDs reads a stored list of user group guids the way
// NormalizeGroupGUIDs does, keeping every entry.
func StoredGroupGUIDs(value any) []string {
	return groupGUIDs(value, 0)
}

// groupGUIDs reads a list of user group guids, at most limit of them (0
// for every one).
func groupGUIDs(value any, limit int) []string {
	list, ok := value.([]any)
	if !ok {
		return uniqueTrimmed(listOrSplit(value), limit)
	}
	items := make([]string, len(list))
	for i, item := range list {
		switch x := item.(type) {
		case map[string]any:
			items[i] = jsStringOrEmpty(x["guid"])
		case []any:
			// A list has no guid.
		default:
			items[i] = jsStringOrEmpty(item)
		}
	}
	return uniqueTrimmed(items, limit)
}

var folderGroupGUID = regexp.MustCompile(`^(?i)folder_(\d+)$`)

// FolderGroupGUID is the guid of the device group that mirrors a folder
// and carries the folder's access list.
func FolderGroupGUID(folderID int64) string {
	return "folder_" + strconv.FormatInt(folderID, 10)
}

// folderIDFromGroupGUID returns the folder a mirror group's guid names.
func folderIDFromGroupGUID(guid string) (float64, bool) {
	m := folderGroupGUID.FindStringSubmatch(jsTrim(guid))
	if m == nil {
		return 0, false
	}
	return jsParseInt(m[1])
}

// groupFolderID returns the folder a device group stands for: its explicit
// folder, else the one its guid names.
func groupFolderID(group DeviceGroup) (float64, bool) {
	if group.FolderID != nil {
		return float64(*group.FolderID), true
	}
	return folderIDFromGroupGUID(group.GUID)
}

// hasTag reports whether the device carries the tag, ignoring letter case.
func hasTag(device Device, tag string) bool {
	expected := strings.ToLower(jsTrim(tag))
	if expected == "" {
		return false
	}
	for _, t := range device.Tags {
		if strings.ToLower(t) == expected {
			return true
		}
	}
	return false
}

// GroupAllowedForUser reports whether a device group is open to an account:
// the group has no access list, names the account, or names one of the
// account's user groups. Admin roles and a nil user pass. The whole lists
// count, however long.
func GroupAllowedForUser(group DeviceGroup, user *ScopeUser) bool {
	if user == nil || scopeExempt(user.Role) {
		return true
	}
	allowedUsers := uniqueTrimmed(group.AllowedUsers, 0)
	allowedGroups := uniqueTrimmed(group.AllowedGroups, 0)
	if len(allowedUsers) == 0 && len(allowedGroups) == 0 {
		return true
	}
	for _, name := range allowedUsers {
		if name == user.Username {
			return true
		}
	}
	mine := make(map[string]struct{})
	for _, guid := range uniqueTrimmed(user.UserGroups, 0) {
		mine[guid] = struct{}{}
	}
	for _, guid := range allowedGroups {
		if _, ok := mine[guid]; ok {
			return true
		}
	}
	return false
}

// hasAccessList reports whether a device group limits who may see it.
func hasAccessList(group DeviceGroup) bool {
	return len(uniqueTrimmed(group.AllowedUsers, 0)) > 0 ||
		len(uniqueTrimmed(group.AllowedGroups, 0)) > 0
}

// UserAccessContext returns the account with its user groups filled in.
// Admin roles and accounts whose groups are already known come back as they
// are; so does the account when the lookup fails.
func (s *Service) UserAccessContext(ctx context.Context, user *ScopeUser) *ScopeUser {
	filled, err := s.withUserGroups(ctx, user)
	if err != nil {
		return user
	}
	return filled
}

// withUserGroups is UserAccessContext with the failed lookup as an error.
func (s *Service) withUserGroups(ctx context.Context, user *ScopeUser) (*ScopeUser, error) {
	if user == nil || user.ID == 0 || scopeExempt(user.Role) || user.UserGroups != nil {
		return user, nil
	}
	groups, err := s.data.UserGroupsForUser(ctx, user.ID)
	if err != nil {
		return nil, fmt.Errorf("read the user groups of account %d: %w", user.ID, err)
	}
	filled := *user
	filled.UserGroups = make([]string, 0, len(groups))
	for _, group := range groups {
		if group.GUID != "" {
			filled.UserGroups = append(filled.UserGroups, group.GUID)
		}
	}
	return &filled, nil
}

// GroupPeerIDs returns the ids of the devices in a device group. A group
// that mirrors a folder has the devices of that folder and nothing else.
// Any other group has its stored members and, when it follows a tag, every
// device carrying the tag. A failed read of the stored members counts as
// none here, where the answer is only counted.
func (s *Service) GroupPeerIDs(ctx context.Context, group DeviceGroup, devices []Device) map[string]struct{} {
	ids, _ := s.groupPeerIDs(ctx, group, devices)
	return ids
}

// groupPeerIDs is GroupPeerIDs with a failed read of the stored members as
// an error.
func (s *Service) groupPeerIDs(ctx context.Context, group DeviceGroup, devices []Device) (map[string]struct{}, error) {
	ids := make(map[string]struct{})
	if folderID, ok := groupFolderID(group); ok {
		for _, device := range devices {
			if device.FolderID != nil && float64(*device.FolderID) == folderID {
				ids[device.ID] = struct{}{}
			}
		}
		return ids, nil
	}
	var err error
	if group.GUID != "" {
		var members []string
		if members, err = s.data.DeviceGroupMembers(ctx, group.GUID); err != nil {
			err = fmt.Errorf("read the members of device group %s: %w", group.GUID, err)
		}
		for _, id := range members {
			ids[id] = struct{}{}
		}
	}
	if group.SourceType == "tag" && group.TagFilter != "" {
		for _, device := range devices {
			if hasTag(device, group.TagFilter) {
				ids[device.ID] = struct{}{}
			}
		}
	}
	return ids, err
}

// EnrichGroups returns the groups with MemberCount set to the number of
// devices GroupPeerIDs finds for each.
func (s *Service) EnrichGroups(ctx context.Context, groups []DeviceGroup, devices []Device) []DeviceGroup {
	out := make([]DeviceGroup, len(groups))
	for i, group := range groups {
		count := len(s.GroupPeerIDs(ctx, group, devices))
		group.MemberCount = &count
		out[i] = group
	}
	return out
}

// DeviceScopeRestrictedDefault reports whether devices nobody was given
// are hidden: the stored setting device_scope_default when it has a value,
// else the DEVICE_SCOPE_DEFAULT option. The answer is reused for 30
// seconds; a failed read answers the option and is not kept.
func (s *Service) DeviceScopeRestrictedDefault(ctx context.Context) bool {
	s.scopeMu.Lock()
	if s.scopeCached && s.now().Sub(s.scopeAt) < scopeDefaultCacheTTL {
		restricted := s.scopeRestricted
		s.scopeMu.Unlock()
		return restricted
	}
	s.scopeMu.Unlock()

	now := s.now()
	stored, err := s.data.setting(ctx, scopeDefaultSetting)
	if err != nil {
		return s.scopeEnvRestricted
	}
	restricted := s.scopeEnvRestricted
	if stored != "" {
		restricted = strings.ToLower(stored) == scopeRestricted
	}
	s.scopeMu.Lock()
	s.scopeCached, s.scopeRestricted, s.scopeAt = true, restricted, now
	s.scopeMu.Unlock()
	return restricted
}

// InvalidateDeviceScopeDefault drops the cached default mode. Call it after
// the setting was written.
func (s *Service) InvalidateDeviceScopeDefault() {
	s.scopeMu.Lock()
	s.scopeCached = false
	s.scopeMu.Unlock()
}

// DeviceScopeEnvRestricted reports whether the DEVICE_SCOPE_DEFAULT option
// alone is "restricted".
func (s *Service) DeviceScopeEnvRestricted() bool { return s.scopeEnvRestricted }

// DeviceScopeForUser returns the devices an account may see among devices,
// or nil when there is no limit.
//
// No account, or an admin role: no limit. Otherwise the device groups with
// an access list and the account's grants decide. With neither, the default
// mode decides: no limit when open, nothing when restricted. With some, a
// restricted default gives the devices of the groups open to the account
// plus its grants; an open default gives every listed device that is not
// in a group closed to the account, plus those in a group open to it or
// granted to it.
//
// A failed read of the device groups, a group's members, the account's
// user groups or its grants is an error: without them the devices of a
// group closed to the account could show.
func (s *Service) DeviceScopeForUser(ctx context.Context, user *ScopeUser, devices []Device) (*DeviceScope, error) {
	if user == nil || user.ID == 0 || scopeExempt(user.Role) {
		return nil, nil
	}
	restrictedDefault := s.DeviceScopeRestrictedDefault(ctx)
	accessUser, err := s.withUserGroups(ctx, user)
	if err != nil {
		return nil, err
	}
	groups, err := s.data.AllDeviceGroups(ctx)
	if err != nil {
		return nil, err
	}
	var listed []DeviceGroup
	for _, group := range groups {
		if hasAccessList(group) {
			listed = append(listed, group)
		}
	}
	grants, err := s.data.UserPeerGrants(ctx, user.ID)
	if err != nil {
		return nil, fmt.Errorf("read the device grants of account %d: %w", user.ID, err)
	}
	if len(listed) == 0 && len(grants) == 0 {
		if restrictedDefault {
			return newDeviceScope(), nil
		}
		return nil, nil
	}

	allowed := newDeviceScope()
	closed := make(map[string]struct{})
	for _, group := range listed {
		ids, err := s.groupPeerIDs(ctx, group, devices)
		if err != nil {
			return nil, err
		}
		open := GroupAllowedForUser(group, accessUser)
		for id := range ids {
			if open {
				allowed.add(id)
			} else {
				closed[id] = struct{}{}
			}
		}
	}
	for _, id := range grants {
		allowed.add(id)
	}
	if restrictedDefault {
		return allowed, nil
	}
	visible := newDeviceScope()
	for _, device := range devices {
		if device.ID == "" {
			continue
		}
		if _, isClosed := closed[device.ID]; !isClosed || allowed.Has(device.ID) {
			visible.add(device.ID)
		}
	}
	return visible, nil
}

// GroupRights is what an account may do with a device group, a folder's
// group included.
type GroupRights struct {
	// Change is renaming or restyling the group (or its folder), and
	// changing its members.
	Change bool
	// ChangeAccess is changing the group's access lists.
	ChangeAccess bool
	// Delete is deleting the group (or its folder): Change, and for a
	// group with an access list also ChangeAccess, because its devices
	// open to everyone when the list goes with it.
	Delete bool
	// Limited says the account's device scope is limited: it makes no tag
	// group, whose devices would join its scope.
	Limited bool
}

// GroupRightsForUser returns what an account may do with a device group;
// group is nil for one that does not exist yet.
//
// Change: an account the device scope does not limit (DeviceScopeForUser)
// may change every group. An account with a limited scope may change a
// group that is open to it: one whose access list names it or one of its
// user groups, or one without an access list while the default mode is
// open.
//
// ChangeAccess needs the permission user.edit (the admin roles) and a
// scope without a limit: an access list opens devices to accounts and
// closes them to others, which is managing accounts. An account with
// device.edit alone changes names and members only.
//
// Delete is Change for a group without an access list, and Change with
// ChangeAccess for a group with one.
func (s *Service) GroupRightsForUser(ctx context.Context, user *ScopeUser, group *DeviceGroup) (GroupRights, error) {
	rights, err := s.groupRights(ctx, user, group)
	listed := group != nil && hasAccessList(*group)
	rights.Delete = rights.Change && (rights.ChangeAccess || !listed)
	return rights, err
}

// groupRights is GroupRightsForUser without Delete.
func (s *Service) groupRights(ctx context.Context, user *ScopeUser, group *DeviceGroup) (GroupRights, error) {
	scope, err := s.DeviceScopeForUser(ctx, user, nil)
	if err != nil {
		return GroupRights{}, err
	}
	if scope == nil {
		manages := false
		if user != nil {
			if manages, err = s.RoleHasEffectivePermission(ctx, user.Role, "user.edit"); err != nil {
				return GroupRights{}, err
			}
		}
		return GroupRights{Change: true, ChangeAccess: manages}, nil
	}
	if group == nil || !hasAccessList(*group) {
		return GroupRights{Change: !s.DeviceScopeRestrictedDefault(ctx), Limited: true}, nil
	}
	accessUser, err := s.withUserGroups(ctx, user)
	if err != nil {
		return GroupRights{}, err
	}
	return GroupRights{Change: GroupAllowedForUser(*group, accessUser), Limited: true}, nil
}

// FilterByScope returns the items whose id is in the scope; all of them
// when the scope has no limit.
func FilterByScope[T any](items []T, scope *DeviceScope, id func(T) string) []T {
	if scope == nil {
		return items
	}
	out := make([]T, 0, len(items))
	for _, item := range items {
		if scope.Has(id(item)) {
			out = append(out, item)
		}
	}
	return out
}

// UserCanAccessDevice reports whether an account may see a device. The
// scope is computed over allDevices, or over the device alone when
// allDevices is nil (an empty, non-nil list is used as given). A nil device
// passes only without a limit.
func (s *Service) UserCanAccessDevice(ctx context.Context, user *ScopeUser, device *Device, allDevices []Device) (bool, error) {
	if allDevices == nil && device != nil {
		allDevices = []Device{*device}
	}
	scope, err := s.DeviceScopeForUser(ctx, user, allDevices)
	if err != nil {
		return false, err
	}
	if scope == nil {
		return true, nil
	}
	return device != nil && scope.Has(device.ID), nil
}

// ScopeCount is the size of an account's device scope for the users page.
type ScopeCount struct {
	Count      int  `json:"count"`
	Restricted bool `json:"restricted"`
}

// CountEffectiveScope counts the devices an account may see: all listed
// devices without a limit, else the ids in its scope (which may include
// granted ids that are not listed).
func (s *Service) CountEffectiveScope(ctx context.Context, user *ScopeUser, devices []Device) (ScopeCount, error) {
	scope, err := s.DeviceScopeForUser(ctx, user, devices)
	if err != nil {
		return ScopeCount{}, err
	}
	if scope == nil {
		return ScopeCount{Count: len(devices)}, nil
	}
	return ScopeCount{Count: scope.Size(), Restricted: true}, nil
}

// NormalizeFolderIDs reads a list of folder ids from a submitted value: a
// list whose items are read as whole numbers (text may carry the number at
// its start), without repeats, at most 200. Anything that is not a list is
// empty.
func NormalizeFolderIDs(value any) []int64 {
	list, _ := value.([]any)
	out := make([]int64, 0, len(list))
	seen := make(map[float64]struct{}, len(list))
	kept := 0
	for _, item := range list {
		f, ok := jsParseInt(item)
		if !ok {
			continue
		}
		if _, dup := seen[f]; dup {
			continue
		}
		seen[f] = struct{}{}
		if kept++; kept > maxFolderIDs {
			break
		}
		// Ids beyond the range of a stored id match no folder.
		if f >= -(1<<63) && f < 1<<63 {
			out = append(out, int64(f))
		}
	}
	return out
}

// NormalizePeerIDs reads a list of device ids from a list or
// comma-separated text: trimmed, without repeats, at most 500.
func NormalizePeerIDs(value any) []string {
	return uniqueTrimmed(listOrSplit(value), maxPeerGrants)
}

// UserFolderIDs returns the folders an account was given: those whose
// mirror device group names the username.
func (s *Service) UserFolderIDs(ctx context.Context, username string) ([]int64, error) {
	out := []int64{}
	if username == "" {
		return out, nil
	}
	folders, err := s.AllFolders(ctx)
	if err != nil {
		return nil, err
	}
	for _, folder := range folders {
		group, err := s.DeviceGroupByGUID(ctx, FolderGroupGUID(folder.ID))
		if err != nil {
			return nil, err
		}
		if group == nil {
			continue
		}
		for _, name := range uniqueTrimmed(group.AllowedUsers, 0) {
			if name == username {
				out = append(out, folder.ID)
				break
			}
		}
	}
	return out, nil
}

// ensureFolderMirrorGroup creates or refreshes the device group that
// carries a folder's access list and returns it.
func (s *Service) ensureFolderMirrorGroup(ctx context.Context, folder Folder) (*DeviceGroup, error) {
	guid := FolderGroupGUID(folder.ID)
	group, err := s.DeviceGroupByGUID(ctx, guid)
	if err != nil {
		return nil, err
	}
	if group == nil {
		return s.createFolderMirrorGroup(ctx, guid, folder.Name)
	}
	if err := s.refreshFolderMirrorGroup(ctx, guid, folder.Name); err != nil {
		return nil, err
	}
	return s.DeviceGroupByGUID(ctx, guid)
}

// SyncUserFolderAccess gives an account exactly the folders in folderIDs (a
// submitted list, read by NormalizeFolderIDs): its username is added to the
// access list of each selected folder's mirror group and removed from the
// others. Every folder gets its mirror group on the way. It returns the
// selected ids.
func (s *Service) SyncUserFolderAccess(ctx context.Context, username string, folderIDs any) ([]int64, error) {
	if username == "" {
		return []int64{}, nil
	}
	selected := NormalizeFolderIDs(folderIDs)
	want := make(map[int64]struct{}, len(selected))
	for _, id := range selected {
		want[id] = struct{}{}
	}
	folders, err := s.AllFolders(ctx)
	if err != nil {
		return nil, err
	}
	for _, folder := range folders {
		group, err := s.ensureFolderMirrorGroup(ctx, folder)
		if err != nil {
			return nil, err
		}
		if group == nil {
			continue
		}
		current := uniqueTrimmed(group.AllowedUsers, 0)
		has := false
		without := make([]string, 0, len(current))
		for _, name := range current {
			if name == username {
				has = true
				continue
			}
			without = append(without, name)
		}
		_, wanted := want[folder.ID]
		switch {
		case wanted && !has:
			err = s.SetDeviceGroupUserAccess(ctx, group.GUID, append(current, username))
		case !wanted && has:
			err = s.SetDeviceGroupUserAccess(ctx, group.GUID, without)
		}
		if err != nil {
			return nil, err
		}
	}
	return selected, nil
}

// UserPeerGrantIDs returns the device ids granted to an account directly.
func (s *Service) UserPeerGrantIDs(ctx context.Context, userID int64) ([]string, error) {
	if userID == 0 {
		return []string{}, nil
	}
	return s.UserPeerGrants(ctx, userID)
}

// SyncUserPeerGrants replaces an account's direct grants with peerIDs (a
// submitted list or comma-separated text, read by NormalizePeerIDs) and
// returns what was stored. Grants for device ids nobody knows are stored
// and logged.
func (s *Service) SyncUserPeerGrants(ctx context.Context, userID int64, peerIDs any) ([]string, error) {
	if userID == 0 {
		return []string{}, nil
	}
	normalized := NormalizePeerIDs(peerIDs)
	if err := s.setUserPeerGrants(ctx, userID, normalized); err != nil {
		return nil, err
	}
	s.warnUnknownPeerIDs(ctx, normalized)
	return normalized, nil
}

// warnUnknownPeerIDs logs the granted ids that are no known device, up to
// 20 lines. It never fails.
func (s *Service) warnUnknownPeerIDs(ctx context.Context, ids []string) {
	if len(ids) == 0 || s.peerExists == nil {
		return
	}
	warned := 0
	for _, id := range ids {
		if warned >= maxUnknownPeerLogs {
			logging.Logf("userScope", "WARN: peer grant lookup capped after %d warnings (%d remaining unchecked)",
				maxUnknownPeerLogs, len(ids)-maxUnknownPeerLogs)
			return
		}
		known, err := s.peerExists(ctx, id)
		switch {
		case err != nil:
			logging.Logf("userScope", "WARN: peer id lookup failed for %s: %v", id, err)
			warned++
		case !known:
			logging.Logf("userScope", "WARN: peer grant references unknown device id: %s", id)
			warned++
		}
	}
}
