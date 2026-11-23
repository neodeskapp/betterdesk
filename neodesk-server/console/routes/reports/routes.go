// Package reports holds the report routes under /api/reports: the report
// types, reports made on demand as JSON or CSV, and saved reports.
//
// Every route needs a session and audit.view. Saving and deleting then
// check the role: super_admin, admin and global_admin may, everyone else
// gets 403. GET /api/reports/trends is another group's, registered in
// front.
//
// An account limited to some devices gets reports of those devices (see
// engine.Filters: their tickets and the tickets of no device, the audit
// entries that name no device outside its scope), and reads the saved
// reports it saved itself.
package reports

import (
	"errors"
	"math"
	"net/http"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/access"
	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
	"github.com/neodeskapp/neodesk/neodesk-server/console/httpx"
	"github.com/neodeskapp/neodesk/neodesk-server/console/jsbind"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
	"github.com/neodeskapp/neodesk/neodesk-server/console/middleware"
	engine "github.com/neodeskapp/neodesk/neodesk-server/console/reports"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
)

// scopeFunc is the device scope of the account of a request, nil when
// it sees every device.
type scopeFunc func(r *http.Request) (*access.DeviceScope, error)

// outsideFunc is the set of ids of the devices outside a limited scope,
// deleted ones included.
type outsideFunc func(r *http.Request, scope *access.DeviceScope) map[string]bool

type handlers struct {
	db      *store.DB
	engine  *engine.Engine
	scope   scopeFunc
	outside outsideFunc
	log     *kit.Logger
	now     func() time.Time
}

// Register adds the group's routes below the mount (/api/reports).
func Register(d *kit.Deps) {
	opts := engine.Options{DB: d.Store}
	scope := func(*http.Request) (*access.DeviceScope, error) { return nil, nil }
	outside := func(*http.Request, *access.DeviceScope) map[string]bool { return nil }
	if d.DeviceGroups != nil {
		opts.SyncPeers = d.DeviceGroups.SyncServerPeers
		scope, outside = scopeOf(d), outsideOf(d)
	}
	h := &handlers{db: d.Store, engine: engine.New(opts), scope: scope, outside: outside, log: d.Log.Child("Reports"), now: time.Now}
	r := d.Router
	r.Get("/types", h.types, requireSession)
	r.Post("/generate", h.generate, requireSession)
	r.Post("/generate/csv", h.generateCSV, requireSession)
	r.Get("/saved", h.listSaved, requireSession)
	r.Get("/saved/{id}", h.getSaved, requireSession)
	r.Post("/save", h.save, requireSession, requireAdmin)
	r.Delete("/saved/{id}", h.deleteSaved, requireSession, requireAdmin)
}

// scopeOf reads the scope over every device of the device layer, which
// knows their folders and tags, and only for an account that has a limit,
// as the trends do.
func scopeOf(d *kit.Deps) scopeFunc {
	return func(r *http.Request) (*access.DeviceScope, error) {
		ctx, user := r.Context(), kit.User(r)
		scope, err := d.DeviceGroups.DeviceScopeForUser(ctx, user, nil)
		if err != nil || scope == nil {
			return scope, err
		}
		return d.DeviceGroups.DeviceScopeForUser(ctx, user, d.Backend.GetAllDevices(ctx, goapi.DeviceFilters{}))
	}
}

// outsideOf lists the devices of the device layer, deleted ones included,
// that a limited scope leaves out; nil for a scope without a limit.
func outsideOf(d *kit.Deps) outsideFunc {
	return func(r *http.Request, scope *access.DeviceScope) map[string]bool {
		if scope.Unlimited() {
			return nil
		}
		out := map[string]bool{}
		for _, device := range d.Backend.GetAllDevices(r.Context(), goapi.DeviceFilters{IncludeDeleted: true}) {
			if id := device.Str("id"); id != "" && !scope.Has(id) {
				out[id] = true
			}
		}
		return out
	}
}

// readsSaved reports whether the account of the request may read a saved
// report that createdBy saved: without a scope limit it reads them all,
// with one those it saved.
func readsSaved(r *http.Request, scope *access.DeviceScope, createdBy any) bool {
	if scope.Unlimited() {
		return true
	}
	name, _ := createdBy.(string)
	return name != "" && name == kit.User(r).Username
}

// failure is the group's error answer: {"error":"..."}.
type failure struct {
	Error string `json:"error"`
}

func fail(w http.ResponseWriter, r *http.Request, status int, message string) {
	kit.JSON(w, r, status, failure{Error: message})
}

// value is a JavaScript value of the engine, written as JSON.stringify
// writes it.
type value struct {
	v any
}

func (v value) MarshalJSON() ([]byte, error) { return engine.Stringify(v.v), nil }

// requireSession lets a signed-in account with audit.view through:
// 401 {"error":"Authentication required"} without a session, 403
// {"success":false,"error":"Permission denied: audit.view"} without the
// permission.
func requireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if kit.User(r) == nil {
			fail(w, r, http.StatusUnauthorized, "Authentication required")
			return
		}
		if !kit.HasPermission(r, "audit.view") {
			kit.Fail(w, r, http.StatusForbidden, "Permission denied: audit.view")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// requireAdmin lets super_admin, admin and global_admin through by the
// role in the session. Everyone else gets 403 {"error":"Admin access
// required"}.
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

// filtersOf are {from, to} of the body.
func filtersOf(body middleware.JSONObject) engine.Filters {
	return engine.Filters{From: jsbind.Of(body.Get("from")), To: jsbind.Of(body.Get("to"))}
}

// maxID is the largest id an id column holds: SERIAL on PostgreSQL.
const maxID = 1<<31 - 1

// savedID is Number(req.params.id). ok is false for a value that is no
// whole number or lies beyond what an id column holds, which names no
// row on either database.
func savedID(r *http.Request) (id jsbind.Value, ok bool) {
	n := jsbind.ParseNumber(r.PathValue("id"))
	if n != math.Trunc(n) || math.Abs(n) > maxID {
		return jsbind.Undefined, false
	}
	return jsbind.Number(n), true
}

// types answers GET /api/reports/types.
func (h *handlers) types(w http.ResponseWriter, r *http.Request) {
	kit.JSON(w, r, http.StatusOK, engine.ListTypes())
}

// generated is the report the body asks for, of the devices the account
// may see. It answers itself and returns nil when there is none: 400
// without a type, for a type there is no report of and for a range that
// is no time (the engine's text), 500 with failed for anything else.
func (h *handlers) generated(w http.ResponseWriter, r *http.Request, body middleware.JSONObject, logPrefix, failed string) *engine.Report {
	typ := jsbind.Of(body.Get("type"))
	if !typ.Truthy() {
		fail(w, r, http.StatusBadRequest, "Report type is required")
		return nil
	}
	filters := filtersOf(body)
	scope, err := h.scope(r)
	if err != nil {
		h.log.Error(logPrefix, err)
		fail(w, r, http.StatusInternalServerError, failed)
		return nil
	}
	filters.Scope = scope
	if typ.IsString() && typ.Str() == "security" {
		// only the security report reads the audit log
		filters.Outside = h.outside(r, scope)
	}
	report, err := h.engine.Generate(r.Context(), typ, filters)
	if err != nil {
		h.log.Error(logPrefix, err)
		var unknown *engine.UnknownTypeError
		var badRange *engine.RangeError
		if errors.As(err, &unknown) || errors.As(err, &badRange) {
			fail(w, r, http.StatusBadRequest, err.Error())
			return nil
		}
		fail(w, r, http.StatusInternalServerError, failed)
		return nil
	}
	return report
}

// generate answers POST /api/reports/generate with the body {type, from?,
// to?}.
func (h *handlers) generate(w http.ResponseWriter, r *http.Request) {
	if report := h.generated(w, r, kit.Body(r), "generate error:", "Failed to generate report"); report != nil {
		kit.JSON(w, r, http.StatusOK, report)
	}
}

// generateCSV answers POST /api/reports/generate/csv with the body of
// generate and section, the list in the report to export. Without one,
// the top-level values that are not objects are the one row.
func (h *handlers) generateCSV(w http.ResponseWriter, r *http.Request) {
	body := kit.Body(r)
	report := h.generated(w, r, body, "CSV export error:", "Failed to export CSV")
	if report == nil {
		return
	}
	csv := h.engine.ToCSV(report.CSVRows(jsbind.Of(body.Get("section"))))
	// The date of the export in UTC, as toISOString().slice(0, 10)
	filename := "neodesk_" + report.Type.Str() + "_" + store.ISOTime(h.now())[:10] + ".csv"
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	// res.send: an ETag, never compressed (only JSON answers are)
	httpx.WriteBody(w, r, http.StatusOK, "text/csv; charset=utf-8", []byte(csv))
}

// listSaved answers GET /api/reports/saved: every saved report the account
// may read, newest first, its columns as the database gave them.
func (h *handlers) listSaved(w http.ResponseWriter, r *http.Request) {
	rows, scope, err := h.savedReports(r)
	if err != nil {
		h.log.Error("get saved error:", err)
		fail(w, r, http.StatusInternalServerError, "Failed to fetch saved reports")
		return
	}
	list := []any{}
	for _, row := range rows {
		if readsSaved(r, scope, row.Get("created_by")) {
			list = append(list, row)
		}
	}
	kit.JSON(w, r, http.StatusOK, value{list})
}

// savedReports reads the scope of the account and every saved report.
func (h *handlers) savedReports(r *http.Request) ([]*goapi.Object, *access.DeviceScope, error) {
	scope, err := h.scope(r)
	if err != nil {
		return nil, nil, err
	}
	rows, err := savedReports(r.Context(), h.db)
	return rows, scope, err
}

// savedReport reads the scope of the account and the saved report id.
func (h *handlers) savedReport(r *http.Request, id jsbind.Value) (*goapi.Object, *access.DeviceScope, error) {
	scope, err := h.scope(r)
	if err != nil {
		return nil, nil, err
	}
	report, err := savedReportByID(r.Context(), h.db, id)
	return report, scope, err
}

// getSaved answers GET /api/reports/saved/{id}: the saved report with its
// payload parsed. A payload that is text and no JSON is {}. A report the
// account may not read is not found.
func (h *handlers) getSaved(w http.ResponseWriter, r *http.Request) {
	id, ok := savedID(r)
	if !ok {
		fail(w, r, http.StatusNotFound, "Saved report not found")
		return
	}
	report, scope, err := h.savedReport(r, id)
	if err != nil {
		h.log.Error("get saved/:id error:", err)
		fail(w, r, http.StatusInternalServerError, "Failed to fetch report")
		return
	}
	if report == nil || !readsSaved(r, scope, report.Get("created_by")) {
		fail(w, r, http.StatusNotFound, "Saved report not found")
		return
	}
	// typeof payload === 'string' ? JSON.parse(payload) : (payload || {})
	payload := report.Get("payload")
	if text, isText := payload.(string); isText {
		parsed, err := engine.ParseJSON(text)
		if err != nil {
			parsed = goapi.NewObject()
		}
		payload = parsed
	} else if !engine.Truthy(payload) {
		payload = goapi.NewObject()
	}
	report.Set("payload", payload)
	kit.JSON(w, r, http.StatusOK, value{report})
}

// save answers POST /api/reports/save with the body {type, title?, from?,
// to?}: it generates the report and stores it, with the title "<label> —
// <time>" when the body has none, null or "". A title that is no text is
// 400.
func (h *handlers) save(w http.ResponseWriter, r *http.Request) {
	body := kit.Body(r)
	report := h.generated(w, r, body, "save error:", "Failed to save report")
	if report == nil {
		return
	}
	title := jsbind.Of(body.Get("title"))
	if !title.IsUndefined() && !title.IsNull() && !title.IsString() {
		fail(w, r, http.StatusBadRequest, "Title must be text")
		return
	}
	title = title.Or(jsbind.String(report.Label + " — " + report.GeneratedAt))
	createdBy := kit.User(r).Username
	if createdBy == "" {
		createdBy = "admin"
	}
	saved, err := createSavedReport(r.Context(), h.db, newSavedReport{
		title:      title,
		reportType: report.Type,
		filters:    report.Filters.JSON(),
		payload:    string(engine.Stringify(report.Data)),
		createdBy:  createdBy,
	})
	if err != nil {
		h.log.Error("save error:", err)
		fail(w, r, http.StatusInternalServerError, "Failed to save report")
		return
	}
	// a row that cannot be read back is null, as on SQLite
	kit.JSON(w, r, http.StatusCreated, value{saved})
}

// deleted is {"ok":true}.
type deleted struct {
	OK bool `json:"ok"`
}

// deleteSaved answers DELETE /api/reports/saved/{id}.
func (h *handlers) deleteSaved(w http.ResponseWriter, r *http.Request) {
	id, ok := savedID(r)
	if !ok {
		fail(w, r, http.StatusNotFound, "Report not found")
		return
	}
	removed, err := deleteSavedReport(r.Context(), h.db, id)
	if err != nil {
		h.log.Error("delete saved error:", err)
		fail(w, r, http.StatusInternalServerError, "Failed to delete report")
		return
	}
	if !removed {
		fail(w, r, http.StatusNotFound, "Report not found")
		return
	}
	kit.JSON(w, r, http.StatusOK, deleted{OK: true})
}
