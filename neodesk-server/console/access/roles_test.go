package access

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/auth"
	"github.com/unitronix/betterdesk-server/db"
)

// memoryOverrides is an OverrideStore in memory.
type memoryOverrides struct {
	rows     map[string]map[string]bool
	failList bool
	failSet  bool
}

func (m *memoryOverrides) ListRolePermissions(role string) ([]*db.RolePermission, error) {
	if m.failList {
		return nil, errors.New("database is down")
	}
	var out []*db.RolePermission
	for permission, granted := range m.rows[role] {
		out = append(out, &db.RolePermission{Role: role, Permission: permission, Granted: granted})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Permission < out[j].Permission })
	return out, nil
}

func (m *memoryOverrides) SetRolePermission(role, permission string, granted bool) error {
	if m.failSet {
		return errors.New("database is down")
	}
	if m.rows == nil {
		m.rows = map[string]map[string]bool{}
	}
	if m.rows[role] == nil {
		m.rows[role] = map[string]bool{}
	}
	m.rows[role][permission] = granted
	return nil
}

func (m *memoryOverrides) DeleteRolePermission(role, permission string) error {
	if m.failSet {
		return errors.New("database is down")
	}
	delete(m.rows[role], permission)
	return nil
}

func sortedStrings(in []string) []string {
	out := append([]string{}, in...)
	sort.Strings(out)
	return out
}

func TestDBRoleSourceDefaults(t *testing.T) {
	ctx := context.Background()
	source := NewDBRoleSource(&memoryOverrides{})
	for role, want := range consoleRoleMap {
		got, err := source.RolePermissions(ctx, role)
		if err != nil {
			t.Fatalf("%s: %v", role, err)
		}
		if !reflect.DeepEqual(sortedStrings(got), sortedStrings(want)) {
			t.Errorf("%s:\n got  %v\n want %v", role, sortedStrings(got), sortedStrings(want))
		}
	}
	for _, role := range []string{"admin", "super_admin"} {
		got, err := source.RolePermissions(ctx, role)
		if err != nil || !reflect.DeepEqual(got, auth.AllPermissions) {
			t.Errorf("%s: %v, %v; want every permission", role, got, err)
		}
	}
	// Without a store the built-in map answers alone.
	if got, err := NewDBRoleSource(nil).RolePermissions(ctx, "viewer"); err != nil || len(got) != len(consoleRoleMap["viewer"]) {
		t.Errorf("no store: %v, %v", got, err)
	}
}

func TestDBRoleSourceOverrides(t *testing.T) {
	ctx := context.Background()
	store := &memoryOverrides{rows: map[string]map[string]bool{
		"viewer": {"server.config": true, "audit.view": false, "made.up": true, "also.made_up": false},
		"pro":    {"device.view": true, "chat.access": true},
	}}
	source := NewDBRoleSource(store)

	got, err := source.RolePermissions(ctx, "viewer")
	if err != nil {
		t.Fatal(err)
	}
	// Built-in order first, names the server does not know after them.
	want := []string{"device.view", "remote_target.view", "server.config", "metrics.view", "cdap.view", "chat.access", "made.up"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("viewer with overrides:\n got  %v\n want %v", got, want)
	}
	// The role's own overrides only.
	if got, _ := source.RolePermissions(ctx, "operator"); !reflect.DeepEqual(sortedStrings(got), sortedStrings(consoleRoleMap["operator"])) {
		t.Errorf("operator picked up another role's overrides: %v", got)
	}
	// The effective list carries what was granted to pro; the pro rule is
	// applied by the checks.
	if got, _ := source.RolePermissions(ctx, "pro"); !reflect.DeepEqual(got, []string{"device.view", "chat.access"}) {
		t.Errorf("pro: %v", got)
	}
	// The role is trimmed like the path segment it once was.
	if got, err := source.RolePermissions(ctx, " viewer "); err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("padded role: %v, %v", got, err)
	}

}

// PA6: while the overrides cannot be read, a permission an override took
// away must not come back. The lookup fails, so the checks refuse with
// their 503 and the guard with its own.
func TestRolePermissionsFailWhenOverridesCannotBeRead(t *testing.T) {
	ctx := context.Background()
	store := &memoryOverrides{rows: map[string]map[string]bool{"operator": {"device.connect": false}}, failList: true}
	source := NewDBRoleSource(store)
	if got, err := source.RolePermissions(ctx, "operator"); err == nil {
		t.Fatalf("failed override read answered %v", got)
	}

	s := &Service{roles: newRoleCache(source, time.Now), now: time.Now}
	s.data = &fakeData{}
	c := s.Load(ctx, Session{UserID: 7, HasUser: true, Role: "operator"})
	if c.PermissionErr == nil || c.Permissions != nil || c.PolicyErr == nil {
		t.Fatalf("load with unreadable overrides: permissions %v (%v), policy error %v", c.Permissions, c.PermissionErr, c.PolicyErr)
	}
	if d := c.RequirePermission("device.connect", "/api/remote/desk"); d.Status != 503 || d.Body == nil || d.Body.Code != CodePermissionService {
		t.Errorf("device.connect: %+v, want the 503 of the permission service", d)
	}
	if got := c.Guard("GET", "/api/devices"); got.Verdict != VerdictUnavailable || got.Decision.Status != 503 {
		t.Errorf("guard: %+v, want 503", got)
	}

	// Once the overrides can be read again, the override holds.
	store.failList = false
	s.ClearRoleCache()
	if ok, err := s.RoleHasEffectivePermission(ctx, "operator", "device.connect"); err != nil || ok {
		t.Errorf("device.connect after the database is back: %v, %v; the override takes it away", ok, err)
	}
}

func TestDBRoleSourceRefusals(t *testing.T) {
	ctx := context.Background()
	source := NewDBRoleSource(&memoryOverrides{})
	for _, role := range []string{"nobody", "device", "Admin", "made_up_role"} {
		_, err := source.RolePermissions(ctx, role)
		var re *RoleError
		if !errors.As(err, &re) || re.Status != 400 || re.Message != "Invalid role" || re.ClientMessage() != "Request failed with status code 400" {
			t.Errorf("role %q: %v, want the API's 400", role, err)
		}
	}
	for _, role := range []string{"", "  ", ".", "..", "a/b", "a b", "role?x=1", "ä"} {
		_, err := source.RolePermissions(ctx, role)
		var ne *InvalidNameError
		if !errors.As(err, &ne) || err.Error() != "Invalid role" {
			t.Errorf("role %q: %v, want an invalid name", role, err)
		}
	}
}

func TestRoleCatalogueLists(t *testing.T) {
	ctx := context.Background()
	store := &memoryOverrides{rows: map[string]map[string]bool{
		"viewer":       {"server.config": true},
		"operator":     {"device.delete": true, "chat.access": false},
		"guest_issuer": {"chat.access": true},
	}}
	source := NewDBRoleSource(store)

	list, err := source.ListRoles(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, role := range list.Roles {
		names = append(names, role.Name)
	}
	// guest_issuer takes overrides, so it is listed (PA7).
	if want := []string{"super_admin", "admin", "server_admin", "global_admin", "operator", "viewer", "guest_issuer", "pro"}; !reflect.DeepEqual(names, want) {
		t.Errorf("roles: %v, want %v", names, want)
	}
	if !reflect.DeepEqual(list.AllPermissions, auth.AllPermissions) {
		t.Error("all_permissions is not the server's list")
	}
	for _, role := range list.Roles {
		switch role.Name {
		case "super_admin", "admin":
			if role.Level != 5 || !role.IsSuperAdmin || !role.IsServerLevel || len(role.Permissions) != len(auth.AllPermissions) {
				t.Errorf("%s: %+v", role.Name, role)
			}
		case "viewer":
			// The list shows the built-in set, without overrides.
			if role.Level != 1 || role.IsSuperAdmin || role.IsServerLevel || !reflect.DeepEqual(sortedStrings(role.Permissions), sortedStrings(consoleRoleMap["viewer"])) {
				t.Errorf("viewer: %+v", role)
			}
		case "pro":
			if role.Permissions == nil || len(role.Permissions) != 0 {
				t.Errorf("pro: permissions %v, want an empty list", role.Permissions)
			}
		}
	}
	if got := jsJSON(t, list.Roles[6]); got != `{"name":"guest_issuer","level":1,"is_super_admin":false,"is_server_level":false,"permissions":["device.view","guest.create"]}` {
		t.Errorf("role JSON: %s", got)
	}
	if got := jsJSON(t, list.Roles[7]); got != `{"name":"pro","level":0,"is_super_admin":false,"is_server_level":false,"permissions":[]}` {
		t.Errorf("role JSON: %s", got)
	}

	overrides, err := source.ListOverrides(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	// In the order of the listed roles, guest_issuer's among them (PA7).
	want := `[{"role":"operator","permission":"chat.access","granted":false},{"role":"operator","permission":"device.delete","granted":true},{"role":"viewer","permission":"server.config","granted":true},{"role":"guest_issuer","permission":"chat.access","granted":true}]`
	if got := jsJSON(t, overrides); got != want {
		t.Errorf("overrides:\n got  %s\n want %s", got, want)
	}
	if got, _ := source.ListOverrides(ctx, "viewer"); len(got) != 1 || got[0].Role != "viewer" {
		t.Errorf("overrides of viewer: %v", got)
	}
	if got, _ := source.ListOverrides(ctx, "nobody"); got == nil || len(got) != 0 {
		t.Errorf("overrides of an unknown role: %v, want an empty list", got)
	}
	store.failList = true
	if got, err := source.ListOverrides(ctx, ""); err != nil || len(got) != 0 {
		t.Errorf("unreadable overrides: %v, %v; want an empty list", got, err)
	}
}

func TestRoleCatalogueWrites(t *testing.T) {
	ctx := context.Background()
	store := &memoryOverrides{}
	source := NewDBRoleSource(store)

	if err := source.SetOverride(ctx, " operator ", "device.delete", true); err != nil {
		t.Fatal(err)
	}
	if !store.rows["operator"]["device.delete"] {
		t.Fatal("the override was not stored under the trimmed role")
	}
	if got, _ := source.RolePermissions(ctx, "operator"); !newPermissionSet(got).Has("device.delete") {
		t.Error("the override does not show in the effective set")
	}
	if err := source.DeleteOverride(ctx, "operator", "device.delete"); err != nil {
		t.Fatal(err)
	}
	if got, _ := source.RolePermissions(ctx, "operator"); newPermissionSet(got).Has("device.delete") {
		t.Error("the deleted override still shows")
	}

	refusals := []struct {
		role, permission string
		status           int
		message          string
	}{
		{"nobody", "device.view", 400, "Invalid role"},
		{"operator", "made.up", 400, "Invalid permission"},
		{"admin", "device.view", 400, "Cannot override super admin permissions"},
		{"super_admin", "device.view", 400, "Cannot override super admin permissions"},
	}
	for _, c := range refusals {
		err := source.SetOverride(ctx, c.role, c.permission, true)
		var re *RoleError
		if !errors.As(err, &re) || re.Status != c.status || re.Message != c.message {
			t.Errorf("SetOverride(%q, %q): %v, want %d %q", c.role, c.permission, err, c.status, c.message)
		}
	}
	if len(store.rows["admin"]) != 0 || len(store.rows["nobody"]) != 0 {
		t.Errorf("a refused override was stored: %v", store.rows)
	}
	// Deleting has no super admin rule, as the API has none.
	if err := source.DeleteOverride(ctx, "admin", "device.view"); err != nil {
		t.Errorf("DeleteOverride for admin: %v", err)
	}
	var ne *InvalidNameError
	if err := source.SetOverride(ctx, "a/b", "device.view", true); !errors.As(err, &ne) || ne.Label != "role" {
		t.Errorf("unsafe role: %v", err)
	}
	if err := source.DeleteOverride(ctx, "operator", "a b"); !errors.As(err, &ne) || ne.Label != "permission" {
		t.Errorf("unsafe permission: %v", err)
	}

	store.failSet = true
	var re *RoleError
	if err := source.SetOverride(ctx, "operator", "device.delete", true); !errors.As(err, &re) || re.Status != 500 || re.Message != "Failed to set permission" {
		t.Errorf("failed write: %v", err)
	}
	if err := source.DeleteOverride(ctx, "operator", "device.delete"); !errors.As(err, &re) || re.Status != 500 || re.Message != "Failed to delete override" {
		t.Errorf("failed delete: %v", err)
	}
}

// The role catalogue on the server's real role_permissions table.
func TestDBRoleSourceOnServerDatabase(t *testing.T) {
	database, err := db.OpenSQLite(filepath.Join(t.TempDir(), "db_v2.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := database.Migrate(); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	source := NewDBRoleSource(database)
	if err := source.SetOverride(ctx, "viewer", "server.config", true); err != nil {
		t.Fatal(err)
	}
	if err := source.SetOverride(ctx, "viewer", "audit.view", false); err != nil {
		t.Fatal(err)
	}
	got, err := source.RolePermissions(ctx, "viewer")
	if err != nil {
		t.Fatal(err)
	}
	set := newPermissionSet(got)
	if !set.Has("server.config") || set.Has("audit.view") || !set.Has("device.view") {
		t.Errorf("viewer after overrides: %v", got)
	}
	overrides, _ := source.ListOverrides(ctx, "viewer")
	if len(overrides) != 2 {
		t.Errorf("overrides: %v", overrides)
	}
	if err := source.DeleteOverride(ctx, "viewer", "audit.view"); err != nil {
		t.Fatal(err)
	}
	if got, _ := source.RolePermissions(ctx, "viewer"); !newPermissionSet(got).Has("audit.view") {
		t.Error("audit.view did not come back after the override was deleted")
	}
}

// clock is a clock tests move by hand.
type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

// Jest: rolePermissionService.test.js.
func TestRoleCache(t *testing.T) {
	ctx := context.Background()
	roles := &fakeRoles{permissions: map[string][]string{"operator": {"device.view", "user.view"}}}
	clk := &clock{now: time.Unix(1_800_000_000, 0)}
	cache := newRoleCache(roles, clk.Now)

	first, err := cache.get(ctx, "operator")
	if err != nil {
		t.Fatal(err)
	}
	second, _ := cache.get(ctx, "operator")
	if roles.calls != 1 || !second.Has("device.view") || !second.Has("user.view") || len(second) != 2 {
		t.Fatalf("second lookup: %d calls, %v", roles.calls, second)
	}
	// Each caller owns its copy: the one that looked the role up and the
	// one that was served from the cache.
	delete(first, "device.view")
	first["server.config"] = struct{}{}
	delete(second, "user.view")
	second["server.keys"] = struct{}{}
	if again, _ := cache.get(ctx, "operator"); len(again) != 2 || !again.Has("device.view") || !again.Has("user.view") {
		t.Fatalf("a caller's change reached the cache: %v", again)
	}

	// Still cached just before a second is over, looked up again at one second.
	clk.Advance(time.Second - time.Millisecond)
	if _, _ = cache.get(ctx, "operator"); roles.calls != 1 {
		t.Fatalf("%d calls within the lifetime", roles.calls)
	}
	clk.Advance(time.Millisecond)
	roles.permissions["operator"] = []string{"user.view"}
	if got, _ := cache.get(ctx, "operator"); roles.calls != 2 || got.Has("device.view") {
		t.Fatalf("after the lifetime: %d calls, %v", roles.calls, got)
	}

	// Invalidate drops one role, clear drops all.
	roles.permissions["viewer"] = []string{"device.view"}
	_, _ = cache.get(ctx, "viewer")
	roles.permissions["operator"] = []string{}
	cache.invalidate("operator")
	if got, _ := cache.get(ctx, "operator"); roles.calls != 4 || got.Has("user.view") {
		t.Fatalf("after invalidate: %d calls, %v", roles.calls, got)
	}
	if _, _ = cache.get(ctx, "viewer"); roles.calls != 4 {
		t.Fatalf("invalidate dropped another role: %d calls", roles.calls)
	}
	cache.clear()
	_, _ = cache.get(ctx, "viewer")
	_, _ = cache.get(ctx, "operator")
	if roles.calls != 6 {
		t.Fatalf("after clear: %d calls", roles.calls)
	}

	// Failures are answered and not kept.
	if _, err := cache.get(ctx, "nobody"); err == nil {
		t.Fatal("an unknown role resolved")
	}
	if _, err := cache.get(ctx, "nobody"); err == nil || roles.calls != 8 {
		t.Fatalf("a failure was cached: %v, %d calls", err, roles.calls)
	}
	var re *RoleError
	if _, err := cache.get(ctx, "nobody"); !errors.As(err, &re) {
		t.Fatalf("the source's error is not in the chain: %v", err)
	}
	if _, err := cache.get(ctx, ""); !errors.Is(err, errNoRole) {
		t.Fatalf("no role: %v", err)
	}
}

// A lookup that waits on another caller's lookup gives up with its own
// context.
func TestRoleCacheWaiterContext(t *testing.T) {
	roles := &fakeRoles{permissions: map[string][]string{"viewer": {"device.view"}}, gate: make(chan struct{})}
	cache := newRoleCache(roles, time.Now)
	leader := make(chan error, 1)
	go func() {
		_, err := cache.get(context.Background(), "viewer")
		leader <- err
	}()
	// Wait until the leader's lookup is under way.
	deadline := time.Now().Add(5 * time.Second)
	for {
		cache.mu.Lock()
		waiting := len(cache.inFlight) == 1
		cache.mu.Unlock()
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the leader never started")
		}
		time.Sleep(time.Millisecond)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := cache.get(ctx, "viewer"); !errors.Is(err, context.Canceled) {
		t.Fatalf("waiter: %v, want its context's error", err)
	}
	close(roles.gate)
	if err := <-leader; err != nil {
		t.Fatalf("leader: %v", err)
	}
}

func TestServiceRoleLookups(t *testing.T) {
	ctx := context.Background()
	roles := &fakeRoles{permissions: map[string][]string{"viewer": {"device.view"}, "pro": {"device.view", "chat.access"}}}
	s := fakeService(&fakeData{}, roles)
	cases := []struct {
		role, permission string
		want             bool
		wantErr          bool
		calls            int
	}{
		{"pro", "device.view", false, false, 0}, // the pro rule, without a lookup
		{"pro", "chat.access", true, false, 1},
		{"admin", "anything.at_all", true, false, 0},
		{"super_admin", "device.view", true, false, 0},
		{"viewer", "device.view", true, false, 1},
		{"viewer", "device.edit", false, false, 1},
		{"nobody", "device.view", false, true, 1}, // a failed lookup refuses the socket
	}
	for _, c := range cases {
		s.ClearRoleCache()
		before := roles.calls
		got, err := s.RoleHasEffectivePermission(ctx, c.role, c.permission)
		if got != c.want || (err != nil) != c.wantErr || roles.calls-before != c.calls {
			t.Errorf("RoleHasEffectivePermission(%q, %q) = %v, %v with %d lookups; want %v, error %v, %d lookups",
				c.role, c.permission, got, err, roles.calls-before, c.want, c.wantErr, c.calls)
		}
	}
	// InvalidateRole reaches the cache.
	s.ClearRoleCache()
	_, _ = s.EffectivePermissions(ctx, "viewer")
	roles.permissions["viewer"] = []string{}
	if got, _ := s.EffectivePermissions(ctx, "viewer"); !got.Has("device.view") {
		t.Fatal("the cached set was not reused")
	}
	s.InvalidateRole("viewer")
	if got, _ := s.EffectivePermissions(ctx, "viewer"); got.Has("device.view") {
		t.Fatal("InvalidateRole did not drop the cached set")
	}
}
