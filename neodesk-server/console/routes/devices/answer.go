package devices

import (
	"encoding/json"
	"net/http"
	"net/url"

	"github.com/neodeskapp/neodesk/neodesk-server/api"
	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
)

// POST /api/devices/{id}/connect-password/answer {salt, challenge}: the
// answer to the device's password question made from the device password
// an organization stored (BUGS L454). The viewer signs in with it and never
// holds the password; the API server decides who may use which stored
// password and writes the audit entry.

// connectPasswordAnswer answers POST /api/devices/{id}/connect-password/answer.
func (h *handlers) connectPasswordAnswer(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.foundOr(w, r, h.failWith(w, r, "Connect password answer error:", "Failed to answer with the connect password")); !ok {
		return
	}
	var body struct {
		Salt      string `json:"salt"`
		Challenge string `json:"challenge"`
	}
	if err := kit.Bind(r, &body); err != nil {
		answer(w, r, http.StatusBadRequest, failure{Error: "invalid_json"})
		return
	}
	a, err := h.askAsAccount(r, http.MethodPost, "/peers/"+url.PathEscape(r.PathValue("id"))+"/connect-password/answer", body)
	if err != nil {
		h.serverError(w, r, "Connect password answer error:", err)
		return
	}
	switch a.status {
	case http.StatusOK:
		w.Header().Set("Cache-Control", "no-store")
		passOn(w, r, a)
	case http.StatusNotFound:
		answer(w, r, http.StatusOK, passwordUnset{})
	case http.StatusForbidden:
		answer(w, r, http.StatusForbidden, forbidden{Error: "forbidden"})
	default:
		passOn(w, r, a)
	}
}

// identity is the signed-in account as the API server sees it.
func identity(r *http.Request) (api.Identity, bool) {
	user := kit.User(r)
	if user == nil {
		return api.Identity{}, false
	}
	return api.Identity{Username: user.Username, Role: user.Role, Actor: user.Username}, true
}

// apiAnswer is what the API server answered: its status and its body.
type apiAnswer struct {
	status int
	body   []byte
}

func (a apiAnswer) code() string {
	var out struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(a.body, &out)
	return out.Error
}

// askAsAccount asks the API server as the signed-in account, which decides
// by that account's role, organizations and device scope.
func (h *handlers) askAsAccount(r *http.Request, method, path string, body any) (apiAnswer, error) {
	id, ok := identity(r)
	if !ok {
		return apiAnswer{status: http.StatusUnauthorized}, nil
	}
	opts := []goapi.CallOption{goapi.As(id), goapi.ValidateStatus(func(int) bool { return true })}
	if body != nil {
		opts = append(opts, goapi.Body(body))
	}
	resp, err := h.api.Request(r.Context(), method, path, opts...)
	if err != nil {
		return apiAnswer{}, err
	}
	return apiAnswer{status: resp.Status, body: resp.Body}, nil
}

// passOn answers with the API server's status and body; an answer with an
// error code is {"success":false,"error":code}.
func passOn(w http.ResponseWriter, r *http.Request, a apiAnswer) {
	if a.status >= 200 && a.status < 300 {
		var data any
		if err := json.Unmarshal(a.body, &data); err != nil {
			answer(w, r, http.StatusBadGateway, failure{Error: "bad_answer"})
			return
		}
		answer(w, r, a.status, data)
		return
	}
	code := a.code()
	if code == "" {
		code = "failed"
	}
	answer(w, r, a.status, failure{Error: code})
}
