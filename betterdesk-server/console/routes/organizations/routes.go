// Package organizations holds the organization routes under
// /api/panel/org: organizations, their users and members, invitations,
// devices, settings, the shared address book and the peer credential
// vault, all kept by the API server, and the device and user groups the
// console links to an organization.
//
// Most routes pass the request on to the API server and answer with what
// it said. Reading needs user.view, which the roles of the pages that read
// organizations have (DECISIONS A39); the devices with a stored password
// need org.manage_devices, and writing the permission the route names.
package organizations

import (
	"net/http"

	"github.com/unitronix/betterdesk-server/console/devicegroups"
	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/goapipath"
	"github.com/unitronix/betterdesk-server/console/kit"
)

type handlers struct {
	api     *goapi.Client
	groups  *devicegroups.Service
	backend *goapi.Backend
	log     *kit.Logger
}

// Register adds the group's routes.
func Register(d *kit.Deps) {
	h := &handlers{api: d.GoAPI, groups: d.DeviceGroups, backend: d.Backend, log: d.Log}
	r := d.Router
	auth := d.RequireAuth
	can := d.RequirePermission

	// Organizations
	r.Get("/api/panel/org", h.pass("GET", fixed("/org"), false), auth, can("user.view"))
	r.Post("/api/panel/org", h.create, auth, can("org.create"))
	r.Get("/api/panel/org/{id}", h.pass("GET", orgPath(""), false), auth, can("user.view"))
	r.Put("/api/panel/org/{id}", h.pass("PUT", orgPath(""), true), auth, can("org.edit"))
	r.Delete("/api/panel/org/{id}", h.remove, auth, can("org.delete"))

	// Users of an organization
	r.Get("/api/panel/org/{id}/users", h.pass("GET", orgPath("/users"), false), auth, can("user.view"), can("org.manage_users"))
	r.Post("/api/panel/org/{id}/users", h.pass("POST", orgPath("/users"), true), auth, can("org.manage_users"))
	r.Put("/api/panel/org/{id}/users/{uid}", h.pass("PUT", orgItemPath("/users/", "uid", "userId"), true), auth, can("org.manage_users"))
	r.Delete("/api/panel/org/{id}/users/{uid}", h.pass("DELETE", orgItemPath("/users/", "uid", "userId"), false), auth, can("org.manage_users"))

	// Console accounts as members
	r.Get("/api/panel/org/{id}/available-users", h.pass("GET", orgPath("/available-users"), false), auth, can("org.manage_users"))
	r.Post("/api/panel/org/{id}/members", h.pass("POST", orgPath("/members"), true), auth, can("org.manage_users"))
	r.Delete("/api/panel/org/{id}/members/{userId}", h.removeMember, auth, can("org.manage_users"))

	// Invitations
	r.Post("/api/panel/org/{id}/invite", h.pass("POST", orgPath("/invite"), true), auth, can("org.manage_users"))
	r.Get("/api/panel/org/{id}/invitations", h.pass("GET", orgPath("/invitations"), false), auth, can("org.manage_users"))

	// Devices
	r.Post("/api/panel/org/{id}/devices", h.assignDevice, auth, can("org.manage_devices"))
	r.Get("/api/panel/org/{id}/devices", h.listDevices, auth, can("user.view"))
	r.Delete("/api/panel/org/{id}/devices/{did}", h.unassignDevice, auth, can("org.manage_devices"))

	// Settings
	r.Get("/api/panel/org/{id}/settings", h.pass("GET", orgPath("/settings"), false), auth, can("user.view"))
	r.Put("/api/panel/org/{id}/settings", h.pass("PUT", orgPath("/settings"), true), auth, can("org.edit"))

	// The shared address book
	r.Get("/api/panel/org/{id}/address-book", h.pass("GET", orgPath("/address-book"), false), auth, can("user.view"))
	r.Put("/api/panel/org/{id}/address-book", h.pass("PUT", orgPath("/address-book"), true), auth, can("org.edit"))

	// The peer credential vault
	r.Get("/api/panel/org/{id}/peer-credentials", h.listCredentials, auth, can("org.manage_devices"))
	r.Put("/api/panel/org/{id}/peer-credentials/{peerId}", h.setCredential, auth, can("org.edit"))
	r.Delete("/api/panel/org/{id}/peer-credentials/{peerId}", h.setCredential, auth, can("org.edit"))

	r.Get("/api/panel/org/{id}/device-groups", h.deviceGroups, auth, can("user.view"))
}

// unreachable is the answer's error when the API server gave no body.
const unreachable = "Go server unreachable"

// path builds the API path of a call from the request; an error whose
// text starts with "Invalid " is answered with 400.
type path func(r *http.Request) (string, error)

func fixed(p string) path {
	return func(*http.Request) (string, error) { return p, nil }
}

// orgPath is /org/<id><suffix>, the id checked by goapipath.SafeSegment.
func orgPath(suffix string) path {
	return func(r *http.Request) (string, error) {
		id, err := goapipath.SafeSegment(r.PathValue("id"), "orgId")
		if err != nil {
			return "", err
		}
		return "/org/" + id + suffix, nil
	}
}

// orgItemPath is /org/<id><infix><item>, both checked; the item's
// parameter is named label in the refusal.
func orgItemPath(infix, param, label string) path {
	return func(r *http.Request) (string, error) {
		org, err := orgPath("")(r)
		if err != nil {
			return "", err
		}
		item, err := goapipath.SafeSegment(r.PathValue(param), label)
		if err != nil {
			return "", err
		}
		return org + infix + item, nil
	}
}

// credentialPath is /org/<id>/peer-credentials/<peerId>, both ids checked.
var credentialPath = orgItemPath("/peer-credentials/", "peerId", "peerId")

// pass is a route that calls the API server at the path and answers with
// its status and body; withBody sends req.body along.
func (h *handlers) pass(method string, build path, withBody bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		target, err := build(r)
		if err != nil {
			invalid(w, r, err)
			return
		}
		h.proxy(w, r, method, target, withBody)
	}
}

// invalid answers 400 {"error": the refusal}.
func invalid(w http.ResponseWriter, r *http.Request, err error) {
	goapipath.WriteJSON(w, r, http.StatusBadRequest, goapipath.ErrorBody{Error: err.Error()})
}

// proxy calls the API server. Its answer goes out with its status and
// body; a call that failed answers the API server's status (500 without
// an answer) and body, or {"error":"Go server unreachable"} without one.
// A path the API client refuses is such a failure.
func (h *handlers) proxy(w http.ResponseWriter, r *http.Request, method, target string, withBody bool) {
	var opts []goapi.CallOption
	if withBody {
		opts = append(opts, goapipath.Body(goapipath.RequestBody(r)))
	}
	resp, err := h.api.Request(r.Context(), method, target, opts...)
	if err != nil {
		status, data := goapipath.FailureOf(err, unreachable)
		goapipath.WriteJSON(w, r, status, data)
		return
	}
	goapipath.WriteJSON(w, r, resp.Status, resp.Data())
}
