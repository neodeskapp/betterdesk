// Package policies holds the console's routes for the policies of an
// organization (connection, features, security, network, update) and for
// the attestation records of devices, with the two routes devices call:
// GET /api/bd/device-policy and POST /api/bd/attestation. The routes pass
// their calls on to the API server.
package policies

import (
	"math"
	"net/http"
	"strings"

	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/goapipath"
	"github.com/unitronix/betterdesk-server/console/kit"
)

type handlers struct {
	api *goapi.Client
	log *kit.Logger
}

// categories are the policies an organization has, each with its own
// write route.
var categories = []string{"connection", "features", "security", "network", "update"}

// Register adds the group's routes. Reads and writes are for the admin
// roles; the write routes ask for nothing else, so someone without a
// session gets their answer there.
func Register(d *kit.Deps) {
	h := &handlers{api: d.GoAPI, log: d.Log.Child("Policies")}
	r := d.Router

	r.Get("/api/panel/policies/{orgId}", h.read(""), d.RequireAuth, d.RequireAdmin)
	for _, category := range categories {
		r.Put("/api/panel/policies/{orgId}/"+category, h.write("/"+category), d.RequireAdmin)
	}
	r.Get("/api/panel/policies/{orgId}/effective/{deviceId}", h.effective, d.RequireAuth, d.RequireAdmin)
	r.Get("/api/panel/policies/{orgId}/audit", h.read("/audit"), d.RequireAuth, d.RequireAdmin)

	r.Get("/api/bd/device-policy", h.devicePolicy, d.RequireDeviceToken, d.RequireTokenDeviceMatch)

	r.Get("/api/panel/attestation", h.attestations, d.RequireAuth, d.RequireAdmin)
	r.Get("/api/panel/attestation/{deviceId}", h.attestation, d.RequireAuth, d.RequireAdmin)

	r.Post("/api/bd/attestation", h.report, d.RequireDeviceToken, d.RequireTokenDeviceMatch)
}

// policyPath is the API path of an organization's policies with a suffix.
// An organization id that cannot go into a path is an error.
func policyPath(r *http.Request, suffix string) (string, error) {
	org, err := goapipath.AssertSafeAPIID(r.PathValue("orgId"), "orgId")
	if err != nil {
		return "", err
	}
	return "/org/" + goapi.EncodeURIComponent(org) + "/policy" + suffix, nil
}

// read answers GET /api/panel/policies/{orgId} (suffix "") and
// /api/panel/policies/{orgId}/audit.
func (h *handlers) read(suffix string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path, err := policyPath(r, suffix)
		if err != nil {
			refuse(w, r, err)
			return
		}
		h.proxy(w, r, http.MethodGet, path)
	}
}

// write answers PUT /api/panel/policies/{orgId}/{category} with the
// policy as the body.
func (h *handlers) write(suffix string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path, err := policyPath(r, suffix)
		if err != nil {
			refuse(w, r, err)
			return
		}
		h.proxy(w, r, http.MethodPut, path, goapipath.Body(goapipath.RequestBody(r)))
	}
}

// effective answers GET /api/panel/policies/{orgId}/effective/{deviceId}:
// the policies that hold for one device. The device id is checked before
// the organization id.
func (h *handlers) effective(w http.ResponseWriter, r *http.Request) {
	device, err := goapipath.AssertSafeAPIID(r.PathValue("deviceId"), "deviceId")
	if err != nil {
		refuse(w, r, err)
		return
	}
	path, err := policyPath(r, "")
	if err != nil {
		refuse(w, r, err)
		return
	}
	h.proxy(w, r, http.MethodGet, path+"/effective/"+goapi.EncodeURIComponent(device))
}

// refuse answers an id that cannot go into a path with 400 and the
// check's text ("Invalid orgId", "Invalid deviceId").
func refuse(w http.ResponseWriter, r *http.Request, err error) {
	kit.JSON(w, r, http.StatusBadRequest, goapipath.ErrorBody{Error: err.Error()})
}

// proxy calls the API server and answers with its status and body.
//
//	no answer                                500 {"error":"Go server unreachable"}
//	404 for a GET whose path ends in /policy 200 {}, so the page shows its defaults
//	                                         with an API server that has no policies
//	any other failure                        its status and body; {"error":"Go server unreachable"}
//	                                         for a body that is empty, false, 0 or null
func (h *handlers) proxy(w http.ResponseWriter, r *http.Request, method, path string, opts ...goapi.CallOption) {
	resp, err := h.api.Request(r.Context(), method, path, opts...)
	if err == nil {
		goapipath.WriteJSON(w, r, resp.Status, resp.Data())
		return
	}
	status, data := goapipath.FailureOf(err, "Go server unreachable")
	if status == http.StatusNotFound && method == http.MethodGet && strings.HasSuffix(path, "/policy") {
		h.log.Warn("The API server answered 404 for " + path + ": answering empty defaults. Its build has no policy routes.")
		kit.JSON(w, r, http.StatusOK, struct{}{})
		return
	}
	goapipath.WriteJSON(w, r, status, data)
}

const (
	defaultLimit = 50
	maxLimit     = 200
)

type noRecords struct {
	Records []any `json:"records"`
	Total   int   `json:"total"`
}

// attestations answers GET /api/panel/attestation?limit=&offset=: the
// attestation records. limit is 50 when the query gives none or 0 and at
// most 200; offset is 0 when it gives none. Each is read from one text: a
// list or an object (limit[]=1, limit[toString]=1) is answered with 400
// "Invalid limit" or "Invalid offset" before any call. An API server
// without attestation answers 404, which is the empty list here.
func (h *handlers) attestations(w http.ResponseWriter, r *http.Request) {
	limit, ok := queryInt(r, "limit", defaultLimit)
	if !ok {
		kit.JSON(w, r, http.StatusBadRequest, goapipath.ErrorBody{Error: "Invalid limit"})
		return
	}
	limit = math.Min(limit, maxLimit)
	offset, ok := queryInt(r, "offset", 0)
	if !ok {
		kit.JSON(w, r, http.StatusBadRequest, goapipath.ErrorBody{Error: "Invalid offset"})
		return
	}
	path := "/attestation?limit=" + goapipath.NumberText(limit) + "&offset=" + goapipath.NumberText(offset)
	resp, err := h.api.Get(r.Context(), path)
	if err == nil {
		goapipath.WriteJSON(w, r, http.StatusOK, resp.Data())
		return
	}
	if missing(err) {
		kit.JSON(w, r, http.StatusOK, noRecords{Records: []any{}})
		return
	}
	status, data := goapipath.FailureOf(err, "Attestation fetch failed")
	goapipath.WriteJSON(w, r, status, data)
}

// queryInt is parseInt(req.query[name]) || fallback: the whole number the
// value starts with, and the fallback when the query has none, or the
// value starts with none or with 0. ok is false for a value that is no
// single text.
func queryInt(r *http.Request, name string, fallback float64) (n float64, ok bool) {
	value := goapipath.Query(r, name)
	if !value.Exists() {
		return fallback, true
	}
	text, ok := value.OneText()
	if !ok {
		return 0, false
	}
	n = goapipath.ParseInt(text)
	if n == 0 || math.IsNaN(n) {
		return fallback, true
	}
	return n, true
}

// missing reports a call the API server has no route for, or that found
// nobody listening.
func missing(err error) bool {
	failed := goapi.AsError(err)
	return failed.Status() == http.StatusNotFound || (failed != nil && failed.Code == "ECONNREFUSED")
}

type unknownDevice struct {
	Status   string `json:"status"`
	DeviceID string `json:"device_id"`
}

// attestation answers GET /api/panel/attestation/{deviceId}: the record
// of one device, and {"status":"unknown","device_id":...} when the API
// server has none or no attestation at all.
func (h *handlers) attestation(w http.ResponseWriter, r *http.Request) {
	device, err := goapipath.AssertSafeAPIID(r.PathValue("deviceId"), "deviceId")
	if err != nil {
		kit.JSON(w, r, http.StatusBadRequest, goapipath.ErrorBody{Error: err.Error()})
		return
	}
	resp, err := h.api.Get(r.Context(), "/attestation/"+goapi.EncodeURIComponent(device))
	if err == nil {
		goapipath.WriteJSON(w, r, http.StatusOK, resp.Data())
		return
	}
	if missing(err) {
		kit.JSON(w, r, http.StatusOK, unknownDevice{Status: "unknown", DeviceID: r.PathValue("deviceId")})
		return
	}
	status, data := goapipath.FailureOf(err, "Attestation fetch failed")
	goapipath.WriteJSON(w, r, status, data)
}
