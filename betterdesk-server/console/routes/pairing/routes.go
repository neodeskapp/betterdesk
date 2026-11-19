// Package pairing is the operator's side of short-code pairing: it makes,
// lists and revokes the one-time codes a device owner types into a client.
// The client redeems a code at the API server itself.
//
// The code is in the answer that makes it and nowhere else: the server
// keeps a hash, and neither the audit log nor a log line gets the code.
package pairing

import (
	"net/http"
	"regexp"

	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/serverinfo"
)

// codeID is the form of a code's id: 8 to 32 hex digits.
var codeID = regexp.MustCompile(`^(?i:[a-f0-9]{8,32})$`)

type handlers struct {
	d   *kit.Deps
	log *kit.Logger
}

// Register adds the group's routes.
func Register(d *kit.Deps) {
	h := &handlers{d: d, log: d.Log.Child("PAIRING")}
	r := d.Router
	approve := d.RequirePermission("enrollment.approve")
	r.Get("/api/panel/pairing/host", h.host, d.RequireAuth, approve)
	r.Get("/api/panel/pairing/codes", h.list, d.RequireAuth, approve)
	r.Post("/api/panel/pairing/codes", h.create, d.RequireAuth, approve)
	r.Delete("/api/panel/pairing/codes/{id}", h.revoke, d.RequireAuth, approve)
}

type hostAnswer struct {
	Success bool   `json:"success"`
	Host    string `json:"host"`
}

// host answers GET /api/panel/pairing/host: the address to suggest in the
// "Server address" field, the host clients are given for this request
// (serverinfo ResolveClientFacingHost, as the dashboard's client
// configuration finds it).
func (h *handlers) host(w http.ResponseWriter, r *http.Request) {
	host := h.d.ServerInfo.ResolveClientFacingHost(serverinfo.RequestHostOf(r), "")
	kit.JSON(w, r, http.StatusOK, hostAnswer{Success: true, Host: host})
}

// refusal is the answer when the API server did not do what was asked.
type refusal struct {
	Success bool `json:"success"`
	Error   any  `json:"error"`
}

// upstream answers with the error of a failed call into the API server.
func upstream(w http.ResponseWriter, r *http.Request, status int, result *goapi.Object) {
	message := result.Get("error")
	if !goapi.Truthy(message) {
		message = "upstream_error"
	}
	kit.JSON(w, r, status, refusal{Error: message})
}

type listAnswer struct {
	Success bool `json:"success"`
	Codes   any  `json:"codes"`
}

// list answers GET /api/panel/pairing/codes: the codes that can still be
// redeemed, without the codes themselves.
func (h *handlers) list(w http.ResponseWriter, r *http.Request) {
	result := h.d.GoAPI.ListPairingCodes(r.Context())
	if !result.Bool("success") {
		upstream(w, r, http.StatusBadGateway, result)
		return
	}
	codes := goapi.Dig(result.Get("data"), "codes")
	if !goapi.Truthy(codes) {
		codes = []any{}
	}
	kit.JSON(w, r, http.StatusOK, listAnswer{Success: true, Codes: codes})
}

// create answers POST /api/panel/pairing/codes with the body
// {"label":"..."}: it makes one code. The label is cut to 64 characters.
func (h *handlers) create(w http.ResponseWriter, r *http.Request) {
	name := labelOf(kit.Body(r).Get("label"))
	result := h.d.GoAPI.CreatePairingCode(r.Context(), name.String())
	if !result.Bool("success") {
		upstream(w, r, http.StatusBadGateway, result)
		return
	}
	data := result.Object("data")

	// The server sends the id as text. An entry that cannot be written
	// must not hide a code the operator has not seen yet.
	details := "Created pairing code " + data.Str("id")
	if len(name) > 0 {
		details += " (" + name.String() + ")"
	}
	if err := h.d.LogAction(r, "pairing_code_created", details); err != nil {
		h.log.Warn("Audit entry for a new pairing code failed:", err)
	}

	answer := goapi.NewObject("success", true)
	copyField(answer, data, "code")
	copyField(answer, data, "id")
	if host := data.Get("host"); goapi.Truthy(host) {
		answer.Set("host", host)
	} else {
		answer.Set("host", "")
	}
	answer.Set("label", name)
	copyField(answer, data, "expires_at")
	copyField(answer, data, "expires_in")
	kit.JSON(w, r, http.StatusCreated, answer)
}

// copyField puts a field of the server's answer into the route's answer.
// A field the server did not send is left out.
func copyField(answer, data *goapi.Object, key string) {
	if data.Has(key) {
		answer.Set(key, data.Get(key))
	}
}

type revokeAnswer struct {
	Success bool   `json:"success"`
	ID      string `json:"id"`
}

// revoke answers DELETE /api/panel/pairing/codes/{id}: it takes a code
// back before it is used.
func (h *handlers) revoke(w http.ResponseWriter, r *http.Request) {
	id := trim(r.PathValue("id"))
	if !codeID.MatchString(id) {
		kit.Fail(w, r, http.StatusBadRequest, "invalid_id")
		return
	}
	result := h.d.GoAPI.RevokePairingCode(r.Context(), id)
	if !result.Bool("success") {
		status := http.StatusBadGateway
		if code, ok := result.Number("status"); ok && code == http.StatusNotFound {
			status = http.StatusNotFound
		}
		upstream(w, r, status, result)
		return
	}
	if err := h.d.LogAction(r, "pairing_code_revoked", "Revoked pairing code "+id); err != nil {
		h.log.Warn("Audit entry for a revoked pairing code failed:", err)
	}
	kit.JSON(w, r, http.StatusOK, revokeAnswer{Success: true, ID: id})
}
