// Package tenants holds the tenant routes under /api/tenants: tenants,
// and the devices and users assigned to them.
//
// The routes take what the request gives them without checking its type
// and leave the rest to the database, so an answer can depend on the
// database: an id that is not a whole number is "not found" on SQLite and
// an error on PostgreSQL. Package jsbind keeps both behaviours.
package tenants

import (
	"errors"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"

	"github.com/unitronix/betterdesk-server/console/access"
	"github.com/unitronix/betterdesk-server/console/jsbind"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/middleware"
	"github.com/unitronix/betterdesk-server/console/store"
)

type handlers struct {
	db  *store.DB
	log *kit.Logger
}

// Register adds the group's routes below the mount (/api/tenants).
func Register(d *kit.Deps) {
	h := &handlers{db: d.Store, log: d.Log.Child("Tenants")}
	r := d.Router
	r.Get("/", h.list, d.RequireAuth, requireAdmin)
	r.Get("/{id}", h.get, d.RequireAuth, requireAdmin)
	r.Post("/", h.create, d.RequireAuth, requireAdmin)
	r.Patch("/{id}", h.update, d.RequireAuth, requireAdmin)
	r.Delete("/{id}", h.remove, d.RequireAuth, requireAdmin)
	r.Get("/{id}/devices", h.devices, d.RequireAuth, requireAdmin)
	r.Post("/{id}/devices", h.assignDevices, d.RequireAuth, requireAdmin)
	r.Delete("/{id}/devices/{deviceId}", h.removeDevice, d.RequireAuth, requireAdmin)
	r.Get("/{id}/users", h.users, d.RequireAuth, requireAdmin)
	r.Post("/{id}/users", h.assignUser, d.RequireAuth, requireAdmin)
	r.Delete("/{id}/users/{userId}", h.removeUser, d.RequireAuth, requireAdmin)
	r.Get("/{id}/stats", h.stats, d.RequireAuth, requireAdmin)
}

// failure is the group's error answer: {"error":"..."}.
type failure struct {
	Error string `json:"error"`
}

func fail(w http.ResponseWriter, r *http.Request, status int, message string) {
	kit.JSON(w, r, status, failure{Error: message})
}

// done is {"ok":true} or {"ok":false}.
type done struct {
	OK bool `json:"ok"`
}

// requireAdmin lets super_admin, admin and global_admin through. Every
// other signed-in account gets 403; d.RequireAuth in front of it answers
// someone who is not signed in.
func requireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		role := ""
		if user := kit.User(r); user != nil {
			role = user.Role
		}
		if access.IsSuperAdminRole(role) || role == "global_admin" {
			next.ServeHTTP(w, r)
			return
		}
		fail(w, r, http.StatusForbidden, "Admin access required")
	})
}

// The parts of a path below /api/tenants where the ids are.
const (
	tenantPart = 3 // /api/tenants/{id}
	userPart   = 5 // /api/tenants/{id}/users/{userId}
)

// pathID reads the id in a part of the path: the digits of a positive
// 32-bit number as the client wrote them, without escapes. ok is false
// for anything else.
func pathID(r *http.Request, name string, part int) (id jsbind.Value, ok bool) {
	value := r.PathValue(name)
	parts := strings.Split(strings.TrimSuffix(r.URL.EscapedPath(), "/"), "/")
	if part >= len(parts) || parts[part] != value || value == "" || len(value) > 10 {
		return jsbind.Undefined, false
	}
	for _, c := range value {
		if c < '0' || c > '9' {
			return jsbind.Undefined, false
		}
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil || n < 1 || n > math.MaxInt32 {
		return jsbind.Undefined, false
	}
	return jsbind.Number(float64(n)), true
}

// tenantFromPath reads the tenant id of the path and answers 404 with message
// when it is none.
func tenantFromPath(w http.ResponseWriter, r *http.Request, message string) (jsbind.Value, bool) {
	id, ok := pathID(r, "id", tenantPart)
	if !ok {
		fail(w, r, http.StatusNotFound, message)
	}
	return id, ok
}

// list answers GET /api/tenants.
func (h *handlers) list(w http.ResponseWriter, r *http.Request) {
	tenants, err := listTenants(r.Context(), h.db)
	if err != nil {
		h.log.Error("GET / error:", err)
		fail(w, r, http.StatusInternalServerError, "Failed to fetch tenants")
		return
	}
	kit.JSON(w, r, http.StatusOK, tenants)
}

// get answers GET /api/tenants/{id}.
func (h *handlers) get(w http.ResponseWriter, r *http.Request) {
	id, ok := tenantFromPath(w, r, "Tenant not found")
	if !ok {
		return
	}
	t, err := tenantByID(r.Context(), h.db, id)
	if err != nil {
		h.log.Error("GET /:id error:", err)
		fail(w, r, http.StatusInternalServerError, "Failed to fetch tenant")
		return
	}
	if t == nil {
		fail(w, r, http.StatusNotFound, "Tenant not found")
		return
	}
	kit.JSON(w, r, http.StatusOK, t)
}

var (
	notSlugChars = regexp.MustCompile(`[^a-z0-9]+`)
	slugEdges    = regexp.MustCompile(`^-|-$`)
)

// slugOf makes a slug from a tenant's name: lower case, every run of
// other characters than a to z and 0 to 9 a dash, no dash at the ends.
func slugOf(name string) string {
	return slugEdges.ReplaceAllString(notSlugChars.ReplaceAllString(jsbind.ToLower(name), "-"), "")
}

// create answers POST /api/tenants with the body
// {name, slug?, contact_name?, contact_email?, max_devices?, notes?}: a
// name that is text, the other texts text or null (null and empty take the
// default) and max_devices a whole number from 0. A slug that is taken is
// 409.
func (h *handlers) create(w http.ResponseWriter, r *http.Request) {
	body := kit.Body(r)
	name, isText := body.String("name")
	name = jsbind.Trim(name)
	if !isText || name == "" {
		fail(w, r, http.StatusBadRequest, "Tenant name is required")
		return
	}
	if !checkMembers(w, r, true) {
		return
	}
	empty := jsbind.String("")
	t, err := createTenant(r.Context(), h.db, newTenant{
		Name:         jsbind.String(name),
		Slug:         jsbind.Of(body.Get("slug")).Or(jsbind.String(slugOf(name))),
		ContactName:  jsbind.Of(body.Get("contact_name")).Or(empty),
		ContactEmail: jsbind.Of(body.Get("contact_email")).Or(empty),
		MaxDevices:   jsbind.Of(body.Get("max_devices")).Or(jsbind.Number(0)),
		Notes:        jsbind.Of(body.Get("notes")).Or(empty),
	})
	if err != nil {
		h.log.Error("POST / error:", err)
		if isUniqueViolation(err) {
			fail(w, r, http.StatusConflict, "Tenant slug already exists")
			return
		}
		fail(w, r, http.StatusInternalServerError, "Failed to create tenant")
		return
	}
	kit.JSON(w, r, http.StatusCreated, t)
}

// textMembers are the members of a tenant that hold text, in the order
// they are checked.
var textMembers = []string{"slug", "contact_name", "contact_email", "max_devices", "notes"}

// checkMembers answers 400 for the first member of the body that is not
// what a tenant takes: texts are text or null (a slug only text in a
// change), max_devices a whole number from 0 (or null on create), and in a
// change active a flag.
func checkMembers(w http.ResponseWriter, r *http.Request, creating bool) bool {
	body := kit.Body(r)
	for _, member := range textMembers {
		v := jsbind.Of(body.Get(member))
		ok := v.IsUndefined() || v.IsString() || (v.IsNull() && (creating || member != "slug"))
		if member == "max_devices" {
			ok = v.IsUndefined() || (v.IsNull() && creating) || wholeNumber(v)
		}
		if !ok {
			fail(w, r, http.StatusBadRequest, "Invalid "+member)
			return false
		}
	}
	if active := jsbind.Of(body.Get("active")); !creating && !active.IsUndefined() {
		if _, ok := flag(active); !ok {
			fail(w, r, http.StatusBadRequest, "Invalid active")
			return false
		}
	}
	return true
}

// wholeNumber reports whether v is a whole number from 0 that an INTEGER
// column holds.
func wholeNumber(v jsbind.Value) bool {
	n := v.ToNumber()
	return v.IsNumber() && n >= 0 && n <= math.MaxInt32 && n == math.Trunc(n)
}

// flag reads a flag from a body: true or false, also written 1 or 0. A
// text has quotes in its JSON, so "true" as text is no flag.
func flag(v jsbind.Value) (on, ok bool) {
	switch v.JSON() {
	case "true", "1":
		return true, true
	case "false", "0":
		return false, true
	}
	return false, false
}

// isUniqueViolation reports whether err is a unique constraint the write
// broke: SQLite's codes for a unique or primary key constraint, or
// PostgreSQL's 23505.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23505"
	}
	var sqliteErr *sqlite.Error
	if errors.As(err, &sqliteErr) {
		code := sqliteErr.Code()
		return code == sqlite3.SQLITE_CONSTRAINT_UNIQUE || code == sqlite3.SQLITE_CONSTRAINT_PRIMARYKEY
	}
	return false
}

// update answers PATCH /api/tenants/{id}: every member of the body that
// names a column is written, each checked: a name that is text with
// something in it, the other texts text (also null, but for the slug),
// max_devices a whole number from 0, active a flag. The answer is the row.
func (h *handlers) update(w http.ResponseWriter, r *http.Request) {
	id, ok := tenantFromPath(w, r, "Tenant not found")
	if !ok {
		return
	}
	existing, err := tenantByID(r.Context(), h.db, id)
	if err == nil && existing == nil {
		fail(w, r, http.StatusNotFound, "Tenant not found")
		return
	}
	if err == nil {
		body := kit.Body(r)
		if name, isText := body.String("name"); body.Has("name") && (!isText || jsbind.Trim(name) == "") {
			fail(w, r, http.StatusBadRequest, "Tenant name is required")
			return
		}
		if !checkMembers(w, r, false) {
			return
		}
	}
	var updated *tenant
	if err == nil {
		updated, err = updateTenant(r.Context(), h.db, id, h.changes(r))
	}
	if isUniqueViolation(err) {
		fail(w, r, http.StatusConflict, "Tenant slug already exists")
		return
	}
	if err != nil {
		h.log.Error("PATCH /:id error:", err)
		fail(w, r, http.StatusInternalServerError, "Failed to update tenant")
		return
	}
	kit.JSON(w, r, http.StatusOK, updated)
}

// changes lists the columns a PATCH body sets, in the order the update
// statement names them, the name trimmed. active is written as 1 or 0 on
// SQLite, true or false on PostgreSQL.
func (h *handlers) changes(r *http.Request) []tenantChange {
	body := kit.Body(r)
	var changes []tenantChange
	for _, column := range []string{"name", "slug", "contact_name", "contact_email", "max_devices", "notes"} {
		if body.Has(column) {
			value := jsbind.Of(body.Get(column))
			if column == "name" {
				value = jsbind.String(jsbind.Trim(value.Str()))
			}
			changes = append(changes, tenantChange{column, value})
		}
	}
	if body.Has("active") {
		active, _ := flag(jsbind.Of(body.Get("active")))
		value := jsbind.Bool(active)
		if !h.db.IsPostgres() {
			value = jsbind.Number(0)
			if active {
				value = jsbind.Number(1)
			}
		}
		changes = append(changes, tenantChange{"active", value})
	}
	return changes
}

// remove answers DELETE /api/tenants/{id}.
func (h *handlers) remove(w http.ResponseWriter, r *http.Request) {
	id, ok := tenantFromPath(w, r, "Tenant not found")
	if !ok {
		return
	}
	ok, err := deleteTenant(r.Context(), h.db, id)
	if err != nil {
		h.log.Error("DELETE /:id error:", err)
		fail(w, r, http.StatusInternalServerError, "Failed to delete tenant")
		return
	}
	if !ok {
		fail(w, r, http.StatusNotFound, "Tenant not found")
		return
	}
	kit.JSON(w, r, http.StatusOK, done{OK: true})
}

// devices answers GET /api/tenants/{id}/devices. A tenant that does not
// exist has no devices.
func (h *handlers) devices(w http.ResponseWriter, r *http.Request) {
	id, ok := tenantFromPath(w, r, "Tenant not found")
	if !ok {
		return
	}
	devices, err := tenantDevices(r.Context(), h.db, id)
	if err != nil {
		h.log.Error("GET /:id/devices error:", err)
		fail(w, r, http.StatusInternalServerError, "Failed to fetch tenant devices")
		return
	}
	kit.JSON(w, r, http.StatusOK, devices)
}

type assigned struct {
	OK       bool `json:"ok"`
	Assigned int  `json:"assigned"`
}

// assignDevices answers POST /api/tenants/{id}/devices with the body
// {device_id} or {device_ids: [...]}. It counts the assignments that went
// through, also those that were there before.
func (h *handlers) assignDevices(w http.ResponseWriter, r *http.Request) {
	id, ok := tenantFromPath(w, r, "Tenant not found")
	if !ok {
		return
	}
	t, err := tenantByID(r.Context(), h.db, id)
	if err == nil && t == nil {
		fail(w, r, http.StatusNotFound, "Tenant not found")
		return
	}
	var ids []jsbind.Value
	var none bool
	if err == nil {
		ids, none, err = deviceIDs(kit.Body(r).Get("device_ids"), kit.Body(r).Get("device_id"))
	}
	if err != nil {
		h.log.Error("POST /:id/devices error:", err)
		fail(w, r, http.StatusInternalServerError, "Failed to assign devices")
		return
	}
	if none {
		fail(w, r, http.StatusBadRequest, "device_id or device_ids required")
		return
	}
	count := 0
	for _, device := range ids {
		if assignDevice(r.Context(), h.db, id, device) {
			count++
		}
	}
	kit.JSON(w, r, http.StatusOK, assigned{OK: true, Assigned: count})
}

// errNotIterable is the TypeError of a for...of loop over a value that is
// no list and no text.
type errNotIterable struct{ value string }

func (e errNotIterable) Error() string { return e.value + " is not iterable" }

// deviceIDs is what the route loops over:
//
//	ids = body.device_ids || (body.device_id ? [body.device_id] : [])
//
// device_ids is taken when it is anything but empty. A list gives its
// elements and a text its characters. none is true when ids.length is 0:
// an empty list, or an object with a member "length" of 0. Any other
// value cannot be looped over, which is an error.
func deviceIDs(many, single middleware.Value) (ids []jsbind.Value, none bool, err error) {
	list := jsbind.Of(many)
	if !list.Truthy() {
		if one := jsbind.Of(single); one.Truthy() {
			return []jsbind.Value{one}, false, nil
		}
		return nil, true, nil
	}
	switch {
	case list.IsArray():
		return list.Items(), len(list.Items()) == 0, nil
	case list.IsString():
		for _, char := range list.Str() {
			ids = append(ids, jsbind.String(string(char)))
		}
		return ids, false, nil
	case list.IsObject():
		if length := list.Member("length"); length.IsNumber() && length.ToNumber() == 0 {
			return nil, true, nil
		}
	}
	return nil, false, errNotIterable{list.ToString()}
}

// removeDevice answers DELETE /api/tenants/{id}/devices/{deviceId}.
func (h *handlers) removeDevice(w http.ResponseWriter, r *http.Request) {
	id, ok := tenantFromPath(w, r, "Assignment not found")
	if !ok {
		return
	}
	ok, err := removeDevice(r.Context(), h.db, id, r.PathValue("deviceId"))
	if err != nil {
		h.log.Error("DELETE /:id/devices/:deviceId error:", err)
		fail(w, r, http.StatusInternalServerError, "Failed to remove device")
		return
	}
	if !ok {
		fail(w, r, http.StatusNotFound, "Assignment not found")
		return
	}
	kit.JSON(w, r, http.StatusOK, done{OK: true})
}

// users answers GET /api/tenants/{id}/users.
func (h *handlers) users(w http.ResponseWriter, r *http.Request) {
	id, ok := tenantFromPath(w, r, "Tenant not found")
	if !ok {
		return
	}
	users, err := tenantUsers(r.Context(), h.db, id)
	if err != nil {
		h.log.Error("GET /:id/users error:", err)
		fail(w, r, http.StatusInternalServerError, "Failed to fetch tenant users")
		return
	}
	kit.JSON(w, r, http.StatusOK, users)
}

// assignUser answers POST /api/tenants/{id}/users with the body
// {user_id}. The answer says whether the row was written: {"ok":false}
// for a tenant that does not exist or an id the database does not take.
func (h *handlers) assignUser(w http.ResponseWriter, r *http.Request) {
	userID := jsbind.Of(kit.Body(r).Get("user_id"))
	if !userID.Truthy() {
		fail(w, r, http.StatusBadRequest, "user_id required")
		return
	}
	id, found := tenantFromPath(w, r, "Tenant not found")
	if !found {
		return
	}
	ok := assignUser(r.Context(), h.db, id, jsbind.Number(userID.ToNumber()))
	kit.JSON(w, r, http.StatusOK, done{OK: ok})
}

// removeUser answers DELETE /api/tenants/{id}/users/{userId}.
func (h *handlers) removeUser(w http.ResponseWriter, r *http.Request) {
	id, ok := tenantFromPath(w, r, "Assignment not found")
	userID, userOK := pathID(r, "userId", userPart)
	if ok && !userOK {
		fail(w, r, http.StatusNotFound, "Assignment not found")
	}
	if !ok || !userOK {
		return
	}
	ok, err := removeUser(r.Context(), h.db, id, userID)
	if err != nil {
		h.log.Error("DELETE /:id/users/:userId error:", err)
		fail(w, r, http.StatusInternalServerError, "Failed to remove user")
		return
	}
	if !ok {
		fail(w, r, http.StatusNotFound, "Assignment not found")
		return
	}
	kit.JSON(w, r, http.StatusOK, done{OK: true})
}

type stats struct {
	TenantID      jsbind.Cell `json:"tenant_id"`
	TenantName    string      `json:"tenant_name"`
	TotalDevices  int         `json:"total_devices"`
	OnlineDevices int         `json:"online_devices"`
	TotalUsers    int         `json:"total_users"`
	MaxDevices    jsbind.Cell `json:"max_devices"`
}

// stats answers GET /api/tenants/{id}/stats.
func (h *handlers) stats(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := tenantFromPath(w, r, "Tenant not found")
	if !ok {
		return
	}
	t, err := tenantByID(r.Context(), h.db, tenantID)
	if err == nil && t == nil {
		fail(w, r, http.StatusNotFound, "Tenant not found")
		return
	}
	var devices []tenantDevice
	var users []tenantUser
	if err == nil {
		devices, err = tenantDevices(r.Context(), h.db, tenantID)
	}
	if err == nil {
		users, err = tenantUsers(r.Context(), h.db, tenantID)
	}
	if err != nil {
		h.log.Error("GET /:id/stats error:", err)
		fail(w, r, http.StatusInternalServerError, "Failed to fetch tenant stats")
		return
	}
	online := 0
	for _, device := range devices {
		if device.Online {
			online++
		}
	}
	// tenant.max_devices || 0
	maxDevices := t.MaxDevices
	if !maxDevices.Value().Truthy() {
		maxDevices = jsbind.CellOf(int64(0))
	}
	kit.JSON(w, r, http.StatusOK, stats{
		TenantID:      jsbind.CellOf(tenantID.ToNumber()),
		TenantName:    t.Name,
		TotalDevices:  len(devices),
		OnlineDevices: online,
		TotalUsers:    len(users),
		MaxDevices:    maxDevices,
	})
}
