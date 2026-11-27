// Package devicelogin holds the console's side of "Sign in with NeoDesk":
// the desktop app signs in through the web console in the browser
// instead of taking a password. The API server keeps the requests
// (api/console_device_login.go); the page /authorize-device/<request id>
// shows the person signed in here which computer asks, and Allow or Deny
// decides it for their account.
//
//	GET  /api/device-login/{id}          the request: state, device, address, age
//	POST /api/device-login/{id}/approve  sign the computer in as this account
//	POST /api/device-login/{id}/deny     refuse it
//
// All three need a browser session of an account with a console (an API
// token or a command line session can't decide a sign-in, and neither
// can an admin viewing NeoDesk as another account); the two writes pass
// the console's CSRF gate like every write on its route table. Each
// decision writes an audit entry.
package devicelogin

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/neodeskapp/neodesk/neodesk-server/console/admintoken"
	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
)

// Action is the audit log's action of a decision.
const (
	ActionAllowed = "console_device_login_allowed"
	ActionDenied  = "console_device_login_denied"
)

type handlers struct {
	d   *kit.Deps
	log *kit.Logger
}

// Register adds the group's routes.
func Register(d *kit.Deps) {
	h := &handlers{d: d, log: d.Log.Child("DEVICE-LOGIN")}
	r := d.Router
	r.Get("/api/device-login/{id}", h.get, d.RequireAuth)
	r.Post("/api/device-login/{id}/approve", h.decide(true), d.RequireAuth)
	r.Post("/api/device-login/{id}/deny", h.decide(false), d.RequireAuth)
}

// Request is a request as the page shows it.
type Request struct {
	State      string `json:"state"`
	DeviceID   string `json:"device_id"`
	DeviceName string `json:"device_name"`
	DeviceOS   string `json:"device_os"`
	DeviceType string `json:"device_type"`
	IP         string `json:"ip"`
	CreatedAt  string `json:"created_at"`
	ExpiresAt  string `json:"expires_at"`
}

// answer is every 200 of the group.
type answer struct {
	Success bool `json:"success"`
	Request
	// Account is who Allow signs the computer in as
	Account string `json:"account,omitempty"`
}

type refusal struct {
	Success bool   `json:"success"`
	Error   string `json:"error"`
	Code    string `json:"code"`
}

// fail answers the refusal code with its sentence in the request's
// language.
func fail(w http.ResponseWriter, r *http.Request, status int, code string) {
	key := "device_authorize.errors." + code
	text := kit.T(r, key, nil)
	if text == "" || text == key {
		text = code
	}
	kit.JSON(w, r, status, refusal{Error: text, Code: code})
}

// byBrowser reports whether a browser session of the account itself
// sent the request: no API token, no one-request session of the command
// line, no impersonation.
func byBrowser(w http.ResponseWriter, r *http.Request) bool {
	if admintoken.FromRequest(r) != nil || kit.Session(r).Ephemeral() {
		fail(w, r, http.StatusForbidden, "browser_only")
		return false
	}
	if kit.Impersonation(r) != nil {
		fail(w, r, http.StatusConflict, "impersonating")
		return false
	}
	return true
}

// validID reports whether id has the shape of a request id.
func validID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	return strings.Trim(id, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_") == ""
}

// fetch asks the API server; status is its answer's.
func (h *handlers) fetch(ctx context.Context, method, path string, body any) (int, map[string]any, error) {
	opts := []goapi.CallOption{goapi.ValidateStatus(func(int) bool { return true })}
	if body != nil {
		opts = append(opts, goapi.Body(body))
	}
	resp, err := h.d.GoAPI.Request(ctx, method, path, opts...)
	if err != nil {
		return 0, nil, fmt.Errorf("%s %s: %w", method, path, err)
	}
	var out map[string]any
	if err := resp.Decode(&out); err != nil {
		return 0, nil, fmt.Errorf("%s %s: answer %d is no JSON: %w", method, path, resp.Status, err)
	}
	return resp.Status, out, nil
}

func requestOf(m map[string]any) Request {
	s := func(k string) string {
		v, _ := m[k].(string)
		return v
	}
	return Request{
		State: s("state"), DeviceID: s("device_id"), DeviceName: s("device_name"), DeviceOS: s("device_os"),
		DeviceType: s("device_type"), IP: s("ip"), CreatedAt: s("created_at"), ExpiresAt: s("expires_at"),
	}
}

// get answers GET /api/device-login/{id}: the request with the account
// Allow signs in as, or {"state":"expired"} for a request that is
// unknown or over (the two look the same).
func (h *handlers) get(w http.ResponseWriter, r *http.Request) {
	if !byBrowser(w, r) {
		return
	}
	id := r.PathValue("id")
	account := ""
	if u := kit.User(r); u != nil {
		account = u.Username
	}
	if !validID(id) {
		kit.JSON(w, r, http.StatusOK, answer{Success: true, Request: Request{State: "expired"}, Account: account})
		return
	}
	status, out, err := h.fetch(r.Context(), http.MethodGet, "/console-device-login/"+url.PathEscape(id), nil)
	switch {
	case err != nil:
		h.log.Error("Desktop sign-in request:", err)
		fail(w, r, http.StatusBadGateway, "server_error")
	case status == http.StatusNotFound:
		kit.JSON(w, r, http.StatusOK, answer{Success: true, Request: Request{State: "expired"}, Account: account})
	case status != http.StatusOK:
		h.log.Error(fmt.Sprintf("Desktop sign-in request: the API server answered %d", status))
		fail(w, r, http.StatusBadGateway, "server_error")
	default:
		kit.JSON(w, r, http.StatusOK, answer{Success: true, Request: requestOf(out), Account: account})
	}
}

// describe names the computer of a request for the audit log.
func describe(req Request) string {
	name := req.DeviceName
	if name == "" {
		name = "a computer"
	}
	parts := []string{}
	if req.DeviceID != "" {
		parts = append(parts, "NeoDesk ID "+req.DeviceID)
	}
	if req.DeviceOS != "" {
		parts = append(parts, req.DeviceOS)
	}
	if req.IP != "" {
		parts = append(parts, "asked from "+req.IP)
	}
	if len(parts) == 0 {
		return name
	}
	return name + " (" + strings.Join(parts, ", ") + ")"
}

// decide answers POST /api/device-login/{id}/approve and /deny. 404
// not_found for a request that is unknown or over, 409 already_decided
// with its state for one that was decided, 409 with the account's code
// (account_disabled, totp_setup_required, password_change_required) when
// the account may not sign in to the desktop app now; the request then
// stays open.
func (h *handlers) decide(allow bool) http.HandlerFunc {
	verb, action := "deny", ActionDenied
	if allow {
		verb, action = "approve", ActionAllowed
	}
	return func(w http.ResponseWriter, r *http.Request) {
		if !byBrowser(w, r) {
			return
		}
		id := r.PathValue("id")
		if !validID(id) {
			fail(w, r, http.StatusNotFound, "not_found")
			return
		}
		ctx := r.Context()
		status, out, err := h.fetch(ctx, http.MethodPost, "/console-device-login/"+url.PathEscape(id)+"/"+verb,
			map[string]int64{"account": kit.UserID(r)})
		if err != nil {
			h.log.Error("Desktop sign-in decision:", err)
			fail(w, r, http.StatusBadGateway, "server_error")
			return
		}
		code, _ := out["error"].(string)
		switch {
		case status == http.StatusNotFound:
			fail(w, r, http.StatusNotFound, "not_found")
			return
		case status == http.StatusConflict && code == "not_pending":
			state, _ := out["state"].(string)
			kit.JSON(w, r, http.StatusConflict, struct {
				refusal
				State string `json:"state"`
			}{refusal{Error: kit.T(r, "device_authorize.errors.already_decided", nil), Code: "already_decided"}, state})
			return
		case status == http.StatusConflict && code == "account_refused":
			reason, _ := out["code"].(string)
			switch reason {
			case "account_disabled", "totp_setup_required", "password_change_required":
			default:
				reason = "account_refused"
			}
			fail(w, r, http.StatusConflict, reason)
			return
		case status != http.StatusOK:
			h.log.Error(fmt.Sprintf("Desktop sign-in decision: the API server answered %d %s", status, code))
			fail(w, r, http.StatusBadGateway, "server_error")
			return
		}
		req := requestOf(out)
		if h.d.Audit != nil {
			details := "Allowed the desktop sign-in of " + describe(req) + " to this account"
			if !allow {
				details = "Denied the desktop sign-in of " + describe(req)
			}
			if err := h.d.LogAction(r, action, details); err != nil {
				h.log.Warn("Audit entry "+action+" failed:", err)
			}
		}
		account := ""
		if u := kit.User(r); u != nil {
			account = u.Username
		}
		kit.JSON(w, r, http.StatusOK, answer{Success: true, Request: req, Account: account})
	}
}
