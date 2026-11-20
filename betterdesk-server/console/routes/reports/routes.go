// Package reports holds the report routes under /api/reports: the report
// types, reports made on demand as JSON or CSV, and saved reports.
//
// Every route needs a session and audit.view. Saving and deleting then
// check the role: super_admin, admin and global_admin may, everyone else
// gets 403. GET /api/reports/trends is another group's, registered in
// front.
package reports

import (
	"errors"
	"net/http"
	"time"

	"github.com/unitronix/betterdesk-server/console/access"
	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/httpx"
	"github.com/unitronix/betterdesk-server/console/jsbind"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/middleware"
	engine "github.com/unitronix/betterdesk-server/console/reports"
	"github.com/unitronix/betterdesk-server/console/store"
)

type handlers struct {
	db     *store.DB
	engine *engine.Engine
	log    *kit.Logger
	now    func() time.Time
}

// Register adds the group's routes below the mount (/api/reports). The
// engine writes times of PostgreSQL into a CSV in the zone of the process.
func Register(d *kit.Deps) {
	opts := engine.Options{DB: d.Store}
	if d.DeviceGroups != nil {
		opts.SyncPeers = d.DeviceGroups.SyncServerPeers
	}
	h := &handlers{db: d.Store, engine: engine.New(opts), log: d.Log.Child("Reports"), now: time.Now}
	r := d.Router
	r.Get("/types", h.types, requireSession)
	r.Post("/generate", h.generate, requireSession)
	r.Post("/generate/csv", h.generateCSV, requireSession)
	r.Get("/saved", h.listSaved, requireSession)
	r.Get("/saved/{id}", h.getSaved, requireSession)
	r.Post("/save", h.save, requireSession, requireAdmin)
	r.Delete("/saved/{id}", h.deleteSaved, requireSession, requireAdmin)
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

// number is Number(req.params[name]).
func number(r *http.Request, name string) jsbind.Value {
	return jsbind.Number(jsbind.ParseNumber(r.PathValue(name)))
}

// types answers GET /api/reports/types.
func (h *handlers) types(w http.ResponseWriter, r *http.Request) {
	kit.JSON(w, r, http.StatusOK, engine.ListTypes())
}

// generated is the report the body asks for. It answers itself and
// returns nil when there is none: 400 without a type, for a type there is
// no report of and for a range that is no time (the engine's text), 500
// with failed for anything else.
func (h *handlers) generated(w http.ResponseWriter, r *http.Request, body middleware.JSONObject, logPrefix, failed string) *engine.Report {
	typ := jsbind.Of(body.Get("type"))
	if !typ.Truthy() {
		fail(w, r, http.StatusBadRequest, "Report type is required")
		return nil
	}
	report, err := h.engine.Generate(r.Context(), typ, filtersOf(body))
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
	filename := "betterdesk_" + report.Type.Str() + "_" + store.ISOTime(h.now())[:10] + ".csv"
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	// res.send: an ETag, never compressed (only JSON answers are)
	httpx.WriteBody(w, r, http.StatusOK, "text/csv; charset=utf-8", []byte(csv))
}

// listSaved answers GET /api/reports/saved: every saved report, newest
// first, its columns as the database gave them.
func (h *handlers) listSaved(w http.ResponseWriter, r *http.Request) {
	rows, err := savedReports(r.Context(), h.db)
	if err != nil {
		h.log.Error("get saved error:", err)
		fail(w, r, http.StatusInternalServerError, "Failed to fetch saved reports")
		return
	}
	list := make([]any, len(rows))
	for i, row := range rows {
		list[i] = row
	}
	kit.JSON(w, r, http.StatusOK, value{list})
}

// getSaved answers GET /api/reports/saved/{id}: the saved report with its
// payload parsed. A payload that is text and no JSON is 500.
func (h *handlers) getSaved(w http.ResponseWriter, r *http.Request) {
	report, err := savedReportByID(r.Context(), h.db, number(r, "id"))
	if err != nil {
		h.log.Error("get saved/:id error:", err)
		fail(w, r, http.StatusInternalServerError, "Failed to fetch report")
		return
	}
	if report == nil {
		fail(w, r, http.StatusNotFound, "Saved report not found")
		return
	}
	// typeof payload === 'string' ? JSON.parse(payload) : (payload || {})
	payload := report.Get("payload")
	if text, isText := payload.(string); isText {
		parsed, err := engine.ParseJSON(text)
		if err != nil {
			h.log.Error("get saved/:id error:", err)
			fail(w, r, http.StatusInternalServerError, "Failed to fetch report")
			return
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
// <time>" when the body has none.
func (h *handlers) save(w http.ResponseWriter, r *http.Request) {
	body := kit.Body(r)
	report := h.generated(w, r, body, "save error:", "Failed to save report")
	if report == nil {
		return
	}
	title := jsbind.Of(body.Get("title")).Or(jsbind.String(report.Label + " — " + report.GeneratedAt))
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
	removed, err := deleteSavedReport(r.Context(), h.db, number(r, "id"))
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
