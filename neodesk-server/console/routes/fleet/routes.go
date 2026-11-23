// Package fleet holds the fleet management routes of the console:
// resource mapping, scheduled tasks, workflows and compliance, and the two
// routes agents report to. Every route passes its call on to the API
// server and answers with what it said.
package fleet

import (
	"net/http"
	"strconv"

	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
	"github.com/neodeskapp/neodesk/neodesk-server/console/goapipath"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
)

type handlers struct {
	api *goapi.Client
}

// Register adds the group's routes.
func Register(d *kit.Deps) {
	h := &handlers{api: d.GoAPI}
	r := d.Router
	manage := []kit.Middleware{d.RequireAuth, d.RequirePermission("org.manage_devices")}

	r.Get("/api/panel/fleet/availability", h.availability, manage...)

	// Resource mapping
	r.Get("/api/panel/fleet/resources/{orgId}", h.pass("GET", "/fleet/resources/{orgId}"), manage...)
	r.Post("/api/panel/fleet/resources/{orgId}", h.passBody("POST", "/fleet/resources/{orgId}"), manage...)
	r.Put("/api/panel/fleet/resources/{orgId}/{resourceId}", h.passBody("PUT", "/fleet/resources/{orgId}/{resourceId}"), manage...)
	r.Delete("/api/panel/fleet/resources/{orgId}/{resourceId}", h.pass("DELETE", "/fleet/resources/{orgId}/{resourceId}"), manage...)

	// Task scheduler
	r.Get("/api/panel/fleet/tasks", h.list("/fleet/tasks",
		filter{param: "orgId", key: "org_id", id: true},
		filter{param: "status", key: "status"},
		filter{param: "deviceId", key: "device_id", id: true},
	), manage...)
	r.Get("/api/panel/fleet/tasks/{taskId}", h.pass("GET", "/fleet/tasks/{taskId}"), manage...)
	r.Post("/api/panel/fleet/tasks", h.create("/fleet/tasks"), manage...)
	r.Put("/api/panel/fleet/tasks/{taskId}", h.passBody("PUT", "/fleet/tasks/{taskId}"), manage...)
	r.Delete("/api/panel/fleet/tasks/{taskId}", h.pass("DELETE", "/fleet/tasks/{taskId}"), manage...)
	r.Get("/api/panel/fleet/tasks/{taskId}/output", h.pass("GET", "/fleet/tasks/{taskId}/output"), manage...)
	r.Post("/api/panel/fleet/tasks/{taskId}/retry", h.pass("POST", "/fleet/tasks/{taskId}/retry"), manage...)

	// Workflows
	r.Get("/api/panel/fleet/workflows", h.pass("GET", "/fleet/workflows"), manage...)
	r.Get("/api/panel/fleet/workflows/{workflowId}", h.pass("GET", "/fleet/workflows/{workflowId}"), manage...)
	r.Post("/api/panel/fleet/workflows", h.create("/fleet/workflows"), manage...)
	r.Put("/api/panel/fleet/workflows/{workflowId}", h.passBody("PUT", "/fleet/workflows/{workflowId}"), manage...)
	r.Delete("/api/panel/fleet/workflows/{workflowId}", h.pass("DELETE", "/fleet/workflows/{workflowId}"), manage...)
	r.Post("/api/panel/fleet/workflows/{workflowId}/execute", h.passBody("POST", "/fleet/workflows/{workflowId}/execute"), manage...)
	r.Get("/api/panel/fleet/workflows/{workflowId}/history", h.pass("GET", "/fleet/workflows/{workflowId}/history"), manage...)

	// Compliance
	r.Get("/api/panel/fleet/compliance", h.list("/fleet/compliance", filter{param: "orgId", key: "org_id", id: true}), manage...)
	r.Get("/api/panel/fleet/compliance/{deviceId}", h.pass("GET", "/fleet/compliance/{deviceId}"), manage...)
	r.Post("/api/panel/fleet/compliance/{deviceId}/scan", h.pass("POST", "/fleet/compliance/{deviceId}/scan"), manage...)
	r.Post("/api/panel/fleet/compliance/{deviceId}/remediate", h.passBody("POST", "/fleet/compliance/{deviceId}/remediate"), manage...)

	// Agents report back with their device token; the body goes on as it came
	device := []kit.Middleware{d.RequireDeviceToken, d.RequireTokenDeviceMatch}
	r.Post("/api/nd/fleet/task-result", h.passBody("POST", "/fleet/task-result"), device...)
	r.Post("/api/nd/fleet/software", h.passBody("POST", "/fleet/software"), device...)
}

type availability struct {
	Success   bool `json:"success"`
	Available bool `json:"available"`
}

// availability answers GET /api/panel/fleet/availability: whether the API
// server has fleet functions at all. One without them answers 404 for
// every fleet path. The fleet page asks here first, so it can say so once.
// Any other answer, and no answer, counts as available: the lists then
// show their own errors.
func (h *handlers) availability(w http.ResponseWriter, r *http.Request) {
	available := true
	if _, err := h.api.Get(r.Context(), "/fleet/tasks"); err != nil {
		available = goapi.AsError(err).Status() != http.StatusNotFound
	}
	kit.JSON(w, r, http.StatusOK, availability{Success: true, Available: available})
}

// pass is a route that calls the API path built from pattern and the
// route's parameters, without a body.
func (h *handlers) pass(method, pattern string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		goapipath.ProxyToGo(h.api, w, r, method, goapipath.PathOf(r, pattern))
	}
}

// passBody is pass with the request's body sent along.
func (h *handlers) passBody(method, pattern string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		goapipath.ProxyToGo(h.api, w, r, method, goapipath.PathOf(r, pattern), goapipath.Body(goapipath.RequestBody(r)))
	}
}

// create is a POST route that adds who is signed in to the body, as
// created_by, before it passes the call on.
func (h *handlers) create(path string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body := spread(goapipath.RequestBody(r))
		creator := "admin"
		if user := kit.User(r); user != nil && user.Username != "" {
			creator = user.Username
		}
		body.Set("created_by", creator)
		goapipath.ProxyToGo(h.api, w, r, http.MethodPost, goapipath.Fixed(path), goapipath.Body(body))
	}
}

// spread is {...body}: a copy of an object, and for a list an object
// with the indexes as keys.
func spread(body any) *goapi.Object {
	switch value := body.(type) {
	case *goapi.Object:
		return value.Clone()
	case []any:
		out := goapi.NewObject()
		for i, element := range value {
			out.Set(strconv.Itoa(i), element)
		}
		return out
	}
	return goapi.NewObject()
}

// filter is one value of a list route's query that goes on to the API
// server under another name.
type filter struct {
	// param is the name in the console's query, key the name in the API's.
	param, key string
	// id says the value has to pass as a path segment would. Other values
	// are cut to 64 characters.
	id bool
}

const maxFilterLength = 64

// list is a GET route that passes the filters of its query on. A filter
// that is missing or empty is left out. A filter that is no single text
// (orgId[]=a, orgId=a&orgId=b, orgId[x]=a), or no id where an id is
// needed, is answered with 400 "Invalid <param>" before any call.
func (h *handlers) list(path string, filters ...filter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var pairs []string
		for _, f := range filters {
			value := goapipath.Query(r, f.param)
			if !value.Truthy() {
				continue
			}
			text, ok := value.OneText()
			if !ok {
				kit.JSON(w, r, http.StatusBadRequest, goapipath.ErrorBody{Error: "Invalid " + f.param})
				return
			}
			if f.id {
				var err error
				if text, err = goapipath.AssertSafeAPIID(text, f.param); err != nil {
					kit.JSON(w, r, http.StatusBadRequest, goapipath.ErrorBody{Error: err.Error()})
					return
				}
			} else {
				text = goapipath.Slice(text, maxFilterLength)
			}
			pairs = append(pairs, f.key, text)
		}
		target := path
		if query := goapipath.FormEncode(pairs...); query != "" {
			target += "?" + query
		}
		goapipath.ProxyToGo(h.api, w, r, http.MethodGet, goapipath.Fixed(target))
	}
}
