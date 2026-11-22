package access

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"sync"
	"time"

	"github.com/unitronix/betterdesk-server/auth"
	"github.com/unitronix/betterdesk-server/db"
)

// RoleSource answers which permissions a role has in effect: the built-in
// map of package auth with the stored overrides applied.
type RoleSource interface {
	RolePermissions(ctx context.Context, role string) ([]string, error)
}

// RoleCatalogue is a RoleSource that can also list roles and overrides and
// change overrides. The permissions page works through it.
type RoleCatalogue interface {
	RoleSource
	ListRoles(ctx context.Context) (RoleList, error)
	ListOverrides(ctx context.Context, role string) ([]RoleOverride, error)
	SetOverride(ctx context.Context, role, permission string, granted bool) error
	DeleteOverride(ctx context.Context, role, permission string) error
}

// OverrideStore is the part of the server's database the role catalogue
// needs: the role_permissions table.
type OverrideStore interface {
	ListRolePermissions(role string) ([]*db.RolePermission, error)
	SetRolePermission(role, permission string, granted bool) error
	DeleteRolePermission(role, permission string) error
}

// RoleError is a refused or failed role catalogue call. Status and Message
// are what the API server answered such a call with.
type RoleError struct {
	Status  int
	Message string
}

func (e *RoleError) Error() string { return e.Message }

// ClientMessage is the error text the console's routes pass on for the
// failure: the HTTP client's words for the status, not the server's message.
func (e *RoleError) ClientMessage() string {
	return fmt.Sprintf("Request failed with status code %d", e.Status)
}

// RoleInfo describes one role in the list the permissions page loads.
type RoleInfo struct {
	Name          string   `json:"name"`
	Level         int      `json:"level"`
	IsSuperAdmin  bool     `json:"is_super_admin"`
	IsServerLevel bool     `json:"is_server_level"`
	Permissions   []string `json:"permissions"`
}

// RoleList is the answer to "list roles": the roles with their built-in
// permissions and every permission name.
type RoleList struct {
	Roles          []RoleInfo `json:"roles"`
	AllPermissions []string   `json:"all_permissions"`
}

// RoleOverride is one stored override: a permission granted to or taken
// from a role.
type RoleOverride struct {
	Role       string `json:"role"`
	Permission string `json:"permission"`
	Granted    bool   `json:"granted"`
}

// listedRoles are the roles the role list and the override list cover:
// every role that takes overrides.
var listedRoles = []string{
	auth.RoleSuperAdmin, auth.RoleAdmin, auth.RoleServerAdmin,
	auth.RoleGlobalAdmin, auth.RoleOperator, auth.RoleViewer, auth.RoleGuestIssuer, auth.RolePro,
}

var safeAPISegment = regexp.MustCompile(`^[a-zA-Z0-9._:@%+-]{1,256}$`)

// InvalidNameError is a role or permission name that is not one plain path
// segment. The routes answer it with status 400 and its text.
type InvalidNameError struct {
	// Label says what the name was meant to be: "role" or "permission".
	Label string
}

func (e *InvalidNameError) Error() string { return "Invalid " + e.Label }

// SafeAPIID trims a role or permission name and checks it is one path
// segment of plain characters. label names the value in the error.
func SafeAPIID(value, label string) (string, error) {
	v := jsTrim(value)
	if v == "" || v == "." || v == ".." || !safeAPISegment.MatchString(v) {
		return "", &InvalidNameError{Label: label}
	}
	return v, nil
}

// FixedPermission is a permission whose tick on a role takes no effect,
// whatever the role's overrides say, because a rule in the code decides
// it. Reason names the rule: "pro_devices" (a Pro license account never
// gets device and remote target permissions, nor assigns devices to
// organizations) or "assigns_no_role" (a role that can give no role
// creates and deletes no account).
type FixedPermission struct {
	Permission string
	Reason     string
}

// FixedPermissions lists the permissions of a role that the code fixes,
// in the order of auth.AllPermissions. The super admin roles have none:
// they have every permission and take no overrides. A role the server
// does not know has none either.
func FixedPermissions(role string) []FixedPermission {
	if auth.IsSuperAdminRole(role) || !auth.ValidRole(role) {
		return nil
	}
	givesARole := false
	for _, target := range listedRoles {
		givesARole = givesARole || auth.CanAssignRole(role, target)
	}
	var out []FixedPermission
	for _, p := range auth.AllPermissions {
		switch {
		case auth.IsProRole(role) && auth.ProRoleBlocksPermission(p):
			out = append(out, FixedPermission{p, "pro_devices"})
		case !givesARole && (p == auth.PermUserCreate || p == auth.PermUserDelete):
			out = append(out, FixedPermission{p, "assigns_no_role"})
		}
	}
	return out
}

// DBRoleSource is the role catalogue on the server's own database.
type DBRoleSource struct {
	store OverrideStore
}

// NewDBRoleSource returns the role catalogue for the server's database. A
// nil store gives the built-in map without overrides.
func NewDBRoleSource(store OverrideStore) *DBRoleSource {
	return &DBRoleSource{store: store}
}

// defaultPermissions returns the built-in permissions of a role in the
// order of auth.AllPermissions.
func defaultPermissions(role string) []string {
	if auth.IsSuperAdminRole(role) {
		return append([]string(nil), auth.AllPermissions...)
	}
	defaults := auth.DefaultRolePermissions[role]
	out := make([]string, 0, len(defaults))
	for _, p := range auth.AllPermissions {
		if defaults[p] {
			out = append(out, p)
		}
	}
	return out
}

// RolePermissions returns the effective permissions of a role: its built-in
// set with each stored override applied. A role the server does not know is
// an error, and so is a failure to read the overrides: the built-in set
// alone would give back what an override took away.
func (s *DBRoleSource) RolePermissions(_ context.Context, role string) ([]string, error) {
	role, err := SafeAPIID(role, "role")
	if err != nil {
		return nil, err
	}
	if !auth.ValidRole(role) {
		return nil, &RoleError{Status: 400, Message: "Invalid role"}
	}
	effective := make(map[string]bool)
	for _, p := range defaultPermissions(role) {
		effective[p] = true
	}
	if s.store != nil {
		overrides, err := s.store.ListRolePermissions(role)
		if err != nil {
			return nil, fmt.Errorf("read the permission overrides of role %s: %w", role, err)
		}
		for _, o := range overrides {
			effective[o.Permission] = o.Granted
		}
	}
	out := make([]string, 0, len(effective))
	for _, p := range auth.AllPermissions {
		if effective[p] {
			out = append(out, p)
			delete(effective, p)
		}
	}
	var extra []string
	for p, granted := range effective {
		if granted {
			extra = append(extra, p)
		}
	}
	sort.Strings(extra)
	return append(out, extra...), nil
}

// ListRoles returns the listed roles with their built-in permissions.
func (s *DBRoleSource) ListRoles(_ context.Context) (RoleList, error) {
	list := RoleList{Roles: make([]RoleInfo, 0, len(listedRoles)), AllPermissions: auth.AllPermissions}
	for _, role := range listedRoles {
		list.Roles = append(list.Roles, RoleInfo{
			Name:          role,
			Level:         auth.RoleLevel(role),
			IsSuperAdmin:  auth.IsSuperAdminRole(role),
			IsServerLevel: auth.IsServerLevel(role),
			Permissions:   defaultPermissions(role),
		})
	}
	return list, nil
}

// ListOverrides returns the stored overrides of the listed roles, or of one
// of them when role is not empty. Roles whose overrides cannot be read are
// left out.
func (s *DBRoleSource) ListOverrides(_ context.Context, role string) ([]RoleOverride, error) {
	out := []RoleOverride{}
	if s.store == nil {
		return out, nil
	}
	for _, listed := range listedRoles {
		if role != "" && listed != role {
			continue
		}
		overrides, err := s.store.ListRolePermissions(listed)
		if err != nil {
			continue
		}
		for _, o := range overrides {
			out = append(out, RoleOverride{Role: listed, Permission: o.Permission, Granted: o.Granted})
		}
	}
	return out, nil
}

// SetOverride grants a permission to a role or takes it away. Super admin
// roles cannot be overridden.
func (s *DBRoleSource) SetOverride(_ context.Context, role, permission string, granted bool) error {
	role, permission, err := safeRoleAndPermission(role, permission)
	if err != nil {
		return err
	}
	if auth.IsSuperAdminRole(role) {
		return &RoleError{Status: 400, Message: "Cannot override super admin permissions"}
	}
	if s.store == nil || s.store.SetRolePermission(role, permission, granted) != nil {
		return &RoleError{Status: 500, Message: "Failed to set permission"}
	}
	return nil
}

// DeleteOverride removes a stored override, so the built-in map decides
// again.
func (s *DBRoleSource) DeleteOverride(_ context.Context, role, permission string) error {
	role, permission, err := safeRoleAndPermission(role, permission)
	if err != nil {
		return err
	}
	if s.store == nil || s.store.DeleteRolePermission(role, permission) != nil {
		return &RoleError{Status: 500, Message: "Failed to delete override"}
	}
	return nil
}

func safeRoleAndPermission(role, permission string) (string, string, error) {
	role, err := SafeAPIID(role, "role")
	if err != nil {
		return "", "", err
	}
	permission, err = SafeAPIID(permission, "permission")
	if err != nil {
		return "", "", err
	}
	if !auth.ValidRole(role) {
		return "", "", &RoleError{Status: 400, Message: "Invalid role"}
	}
	if !auth.ValidPermission(permission) {
		return "", "", &RoleError{Status: 400, Message: "Invalid permission"}
	}
	return role, permission, nil
}

// roleCacheTTL is how long a role's effective permissions are reused.
const roleCacheTTL = time.Second

type cachedRole struct {
	permissions PermissionSet
	expiresAt   time.Time
}

type roleLookup struct {
	done        chan struct{}
	permissions PermissionSet
	err         error
}

// roleCache keeps each role's effective permissions for roleCacheTTL and
// lets lookups of the same role that arrive while one is under way share
// its result. Failures are not kept.
type roleCache struct {
	source RoleSource
	now    func() time.Time

	mu       sync.Mutex
	entries  map[string]cachedRole
	inFlight map[string]*roleLookup
}

func newRoleCache(source RoleSource, now func() time.Time) *roleCache {
	return &roleCache{
		source:   source,
		now:      now,
		entries:  make(map[string]cachedRole),
		inFlight: make(map[string]*roleLookup),
	}
}

var errNoRole = errors.New("cannot resolve permissions without a role")

func (c *roleCache) get(ctx context.Context, role string) (PermissionSet, error) {
	if role == "" {
		return nil, errNoRole
	}
	c.mu.Lock()
	if cached, ok := c.entries[role]; ok && cached.expiresAt.After(c.now()) {
		c.mu.Unlock()
		return cached.permissions.clone(), nil
	}
	if lookup, ok := c.inFlight[role]; ok {
		c.mu.Unlock()
		select {
		case <-lookup.done:
		case <-ctx.Done():
			return nil, fmt.Errorf("resolve effective permissions for role %s: %w", role, ctx.Err())
		}
		if lookup.err != nil {
			return nil, lookup.err
		}
		return lookup.permissions.clone(), nil
	}
	lookup := &roleLookup{done: make(chan struct{})}
	c.inFlight[role] = lookup
	c.mu.Unlock()

	permissions, err := c.source.RolePermissions(ctx, role)
	c.mu.Lock()
	if err != nil {
		lookup.err = fmt.Errorf("resolve effective permissions for role %s: %w", role, err)
	} else {
		lookup.permissions = newPermissionSet(permissions)
		c.entries[role] = cachedRole{permissions: lookup.permissions, expiresAt: c.now().Add(roleCacheTTL)}
	}
	delete(c.inFlight, role)
	c.mu.Unlock()
	close(lookup.done)

	if lookup.err != nil {
		return nil, lookup.err
	}
	return lookup.permissions.clone(), nil
}

func (c *roleCache) invalidate(role string) {
	c.mu.Lock()
	delete(c.entries, role)
	c.mu.Unlock()
}

func (c *roleCache) clear() {
	c.mu.Lock()
	c.entries = make(map[string]cachedRole)
	c.mu.Unlock()
}
