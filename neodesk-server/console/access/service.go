package access

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/unitronix/betterdesk-server/console/store"
)

// Options configure a Service.
type Options struct {
	// DeviceScopeDefault is the DEVICE_SCOPE_DEFAULT setting: "restricted"
	// hides devices nobody was given, anything else shows them.
	DeviceScopeDefault string
	// PeerExists reports whether a device id is known. It is used only to
	// log grants for unknown devices; nil turns that off.
	PeerExists func(ctx context.Context, id string) (bool, error)
	// Now is the clock; nil means time.Now.
	Now func() time.Time
}

// Service loads and decides access for the console: role permissions with
// their short cache, panel access policies and the device scope. It is safe
// for concurrent use.
type Service struct {
	db *store.DB
	// data is where the decisions read stored rows from: the Service's own
	// queries on db.
	data       reader
	roles      *roleCache
	now        func() time.Time
	peerExists func(ctx context.Context, id string) (bool, error)

	scopeEnvRestricted bool
	scopeMu            sync.Mutex
	scopeCached        bool
	scopeRestricted    bool
	scopeAt            time.Time
}

// New returns a Service on the console's database that takes role
// permissions from roles.
func New(db *store.DB, roles RoleSource, opts Options) *Service {
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	s := &Service{
		db:                 db,
		roles:              newRoleCache(roles, now),
		now:                now,
		peerExists:         opts.PeerExists,
		scopeEnvRestricted: strings.ToLower(opts.DeviceScopeDefault) == "restricted",
	}
	s.data = s
	return s
}

// reader is the stored data the decisions read.
type reader interface {
	PanelAccessPolicies(ctx context.Context, subjectType, subjectID string) ([]SectionRow, error)
	ClientFeaturePolicies(ctx context.Context, subjectType, subjectID string) ([]FeatureRow, error)
	UserGroupsForUser(ctx context.Context, userID int64) ([]UserGroup, error)
	AllDeviceGroups(ctx context.Context) ([]DeviceGroup, error)
	DeviceGroupMembers(ctx context.Context, guid string) ([]string, error)
	UserPeerGrants(ctx context.Context, userID int64) ([]string, error)
	setting(ctx context.Context, key string) (string, error)
}

// EffectivePermissions returns the effective permissions of a role: the
// built-in map with overrides. The answer is reused for one second per
// role, and lookups of a role that arrive while one is under way share its
// result. The caller owns the returned set.
func (s *Service) EffectivePermissions(ctx context.Context, role string) (PermissionSet, error) {
	return s.roles.get(ctx, role)
}

// InvalidateRole drops a role's cached permissions. Call it after writing
// an override of the role.
func (s *Service) InvalidateRole(role string) {
	if role != "" {
		s.roles.invalidate(role)
	}
}

// ClearRoleCache drops every cached role.
func (s *Service) ClearRoleCache() { s.roles.clear() }

// RoleHasEffectivePermission checks a permission for a role outside a
// request, as websocket upgrades do: pro accounts never have device
// permissions, super admins have everything, any other role is looked up
// with its overrides. A failed lookup is an error; the caller refuses.
func (s *Service) RoleHasEffectivePermission(ctx context.Context, role, permission string) (bool, error) {
	if ProRoleBlocksPermission(role, permission) {
		return false, nil
	}
	if IsSuperAdminRole(role) {
		return true, nil
	}
	permissions, err := s.EffectivePermissions(ctx, role)
	if err != nil {
		return false, err
	}
	return permissions.Has(permission), nil
}

// Session is what the access checks need to know about a request's session.
type Session struct {
	// UserID is the id of the signed-in account, 0 without one.
	UserID int64
	// HasUser reports whether the session carries its user record.
	HasUser bool
	// Role is the role in the session's user record.
	Role string
}

// Load builds the permission context of a request, in this order: the
// effective permissions of the session's role (not for super admins, who
// need none), then the account's panel access policy. A failure of either
// is kept in the context and the request goes on: the checks that need the
// missing part refuse.
func (s *Service) Load(ctx context.Context, session Session) *Context {
	c := &Context{UserID: session.UserID, HasUser: session.HasUser, Loaded: true}
	if session.HasUser {
		c.Role = session.Role
	}
	if c.Role != "" && !IsSuperAdminRole(c.Role) {
		permissions, err := s.EffectivePermissions(ctx, c.Role)
		if err != nil {
			c.PermissionErr = err
		} else {
			c.Permissions = permissions
		}
	}
	if c.UserID != 0 && c.HasUser {
		policy, err := s.ResolveForUser(ctx, PolicyUser{ID: c.UserID, Role: c.Role})
		if err != nil {
			c.PolicyErr = err
		} else {
			c.Policy = policy
		}
	}
	return c
}
