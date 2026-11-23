package devicegroups

import (
	"context"
	"math"
	"regexp"
	"sync"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/access"
	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
	"github.com/neodeskapp/neodesk/neodesk-server/console/jsbind"
	"github.com/neodeskapp/neodesk/neodesk-server/console/session"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
)

// Options are the settings of a Service. The old console had none for
// folders and groups.
type Options struct {
	// DB is the console's database.
	DB *store.DB
	// Access decides who may see which device and holds the queries on
	// device groups it shares with this package.
	Access *access.Service
	// Now is the clock of the copy of the server's devices into the
	// console's device table (SyncServerPeers); time.Now when nil.
	Now func() time.Time
}

// Service reads and writes folders, device groups and user groups, and
// answers which devices of a list an account may see. It is safe for
// concurrent use.
type Service struct {
	db     *store.DB
	access *access.Service
	now    func() time.Time

	// syncMu guards lastSync, the time of the last copy of the server's
	// devices (SyncServerPeers).
	syncMu   sync.Mutex
	lastSync time.Time
}

// New returns a Service.
func New(opts Options) *Service {
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &Service{db: opts.DB, access: opts.Access, now: now}
}

var folderGroupGUID = regexp.MustCompile(`^(?i)folder_(\d+)$`)

// FolderIDFromGroupGUID returns the folder whose access list a device
// group carries: the number in a guid "folder_<n>", in any letter case
// and with white space around it. ok is false for every other guid.
func FolderIDFromGroupGUID(guid any) (id float64, ok bool) {
	m := folderGroupGUID.FindStringSubmatch(jsbind.Trim(textOr(guid, "")))
	if m == nil {
		return 0, false
	}
	id = jsbind.ParseNumber(m[1])
	return id, !math.IsInf(id, 0)
}

// ScopeUser is the signed-in account as the scope rules read it.
func ScopeUser(user *session.User) *access.ScopeUser {
	if user == nil {
		return nil
	}
	return &access.ScopeUser{ID: user.ID, Username: user.Username, Role: user.Role}
}

// ScopeDevices turns devices as the device layer lists them into what the
// scope rules read: the id as text, the folder as parseInt read it, the
// tags.
func ScopeDevices(devices []*goapi.Object) []access.Device {
	out := make([]access.Device, 0, len(devices))
	for _, device := range devices {
		if device == nil {
			continue
		}
		d := access.Device{ID: jsText(device.Get("id")), Tags: access.NormalizeTags(Plain(device.Get("tags")))}
		if folder, ok := parseIntOf(device.Get("folder_id")); ok && folder >= -(1<<63) && folder < 1<<63 {
			id := int64(folder)
			d.FolderID = &id
		}
		out = append(out, d)
	}
	return out
}

// ScopeGroup is a device group row as the scope rules read it.
func ScopeGroup(row *goapi.Object) access.DeviceGroup {
	g := access.DeviceGroup{
		GUID:      textOr(row.Get("guid"), ""),
		TagFilter: textOr(row.Get("tag_filter"), ""),
	}
	g.SourceType = textOr(row.Get("source_type"), "manual")
	g.AllowedUsers = access.StoredUsernames(Plain(row.Get("allowed_users")))
	g.AllowedGroups = access.StoredGroupGUIDs(Plain(orValue(row.Get("allowed_groups"), row.Get("allowed_user_groups"))))
	return g
}

// DeviceScopeForUser returns the devices of the list the account may see,
// or nil when there is no limit (deviceGroupService.getDeviceScopeForUser).
func (s *Service) DeviceScopeForUser(ctx context.Context, user *session.User, devices []*goapi.Object) (*access.DeviceScope, error) {
	return s.access.DeviceScopeForUser(ctx, ScopeUser(user), ScopeDevices(devices))
}

// LimitedScope returns the devices the account may see, or nil when it
// sees every device. Whether there is a limit is decided without a device
// list; only for an account that has one is the scope read over the list
// of every device that all returns, which knows their folders and tags.
func (s *Service) LimitedScope(ctx context.Context, user *session.User, all func() []*goapi.Object) (*access.DeviceScope, error) {
	scope, err := s.DeviceScopeForUser(ctx, user, nil)
	if err != nil || scope == nil {
		return scope, err
	}
	return s.DeviceScopeForUser(ctx, user, all())
}

// UserCanAccessDevice reports whether the account may see the device, with
// the scope computed over that device alone.
func (s *Service) UserCanAccessDevice(ctx context.Context, user *session.User, device *goapi.Object) (bool, error) {
	var one *access.Device
	if device != nil {
		one = &ScopeDevices([]*goapi.Object{device})[0]
	}
	return s.access.UserCanAccessDevice(ctx, ScopeUser(user), one, nil)
}

// EnrichGroups returns copies of device group rows with the kind, the tag
// filter and the access lists filled in and member_count set to the
// number of devices of the list that are in the group
// (deviceGroupService.enrichGroups).
func (s *Service) EnrichGroups(ctx context.Context, groups []*goapi.Object, devices []*goapi.Object) []*goapi.Object {
	scoped := ScopeDevices(devices)
	out := make([]*goapi.Object, 0, len(groups))
	for _, group := range groups {
		members := s.access.GroupPeerIDs(ctx, ScopeGroup(group), scoped)
		row := group.Clone()
		row.Set("source_type", orValue(row.Get("source_type"), "manual"))
		row.Set("tag_filter", orValue(row.Get("tag_filter"), ""))
		if list, ok := row.Get("allowed_users").([]any); ok {
			row.Set("allowed_users", list)
		} else {
			row.Set("allowed_users", stringsOf(access.NormalizeUsernames(Plain(row.Get("allowed_users")))))
		}
		if list, ok := row.Get("allowed_groups").([]any); ok {
			row.Set("allowed_groups", list)
		} else {
			row.Set("allowed_groups", stringsOf(access.NormalizeGroupGUIDs(Plain(row.Get("allowed_groups")))))
		}
		if list, ok := row.Get("allowed_user_groups").([]any); ok {
			row.Set("allowed_user_groups", list)
		} else {
			row.Set("allowed_user_groups", []any{})
		}
		row.Set("member_count", float64(len(members)))
		out = append(out, row)
	}
	return out
}

// stringsOf is a list of texts as a JSON list.
func stringsOf(items []string) []any {
	out := make([]any, len(items))
	for i, item := range items {
		out[i] = item
	}
	return out
}
