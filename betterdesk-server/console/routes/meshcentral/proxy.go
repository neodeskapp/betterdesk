package meshcentral

import (
	"net/http"
	"strings"

	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/httpx"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/middleware"
)

// apiFailure is the answer when the API server gave none to pass on.
type apiFailure struct {
	Error string `json:"error"`
}

const unreachable = "Go server unreachable"

// notText is the message of the error JavaScript raises when a value
// cannot be turned into text.
const notText = "Cannot convert object to primitive value"

// pass sends one call to the API server and hands its answer to the
// client: the same status and the same JSON. Without an answer the client
// gets 500 "Go server unreachable"; a call refused before it was sent with
// a message that starts with "Invalid " gets 400 with that message.
func (h *handlers) pass(w http.ResponseWriter, r *http.Request, method, path string, opts ...goapi.CallOption) {
	resp, err := h.api.Request(r.Context(), method, path, opts...)
	if err == nil {
		kit.JSON(w, r, resp.Status, resp.Data())
		return
	}
	failed := goapi.AsError(err)
	if failed == nil {
		kit.JSON(w, r, http.StatusInternalServerError, apiFailure{Error: unreachable})
		return
	}
	if strings.HasPrefix(failed.Message, "Invalid ") {
		kit.JSON(w, r, http.StatusBadRequest, apiFailure{Error: failed.Message})
		return
	}
	status := failed.Status()
	if status == 0 {
		status = http.StatusInternalServerError
	}
	if data := failed.Data(); goapi.Truthy(data) {
		kit.JSON(w, r, status, data)
		return
	}
	kit.JSON(w, r, status, apiFailure{Error: unreachable})
}

// requestBody is the request's JSON body as the call option that sends it
// on: the parsed value written out again, so the API server gets what
// JSON.stringify(req.body) gave it.
func requestBody(r *http.Request) goapi.CallOption {
	raw := middleware.BodyValue(r).Raw()
	value, err := goapi.Parse(raw)
	if err != nil {
		return goapi.RawBody(raw)
	}
	return goapi.Body(value)
}

// get answers a GET route that stands for one path of the API server.
func (h *handlers) get(path string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		h.pass(w, r, http.MethodGet, path)
	}
}

// devicePath is the API server's path for an action on a device. The id is
// escaped as one path segment.
func devicePath(id, action string) string {
	return "/mesh/devices/" + goapi.EncodeURIComponent(id) + "/" + action
}

// isDotSegment reports whether an id is "." or "..", which no device or
// recording has and which would name another path of the API server.
func isDotSegment(id string) bool { return id == "." || id == ".." }

// validDevice answers 400 for a device id that is "." or "..", and
// reports whether the id can go on.
func validDevice(w http.ResponseWriter, r *http.Request) bool {
	if isDotSegment(r.PathValue("id")) {
		kit.JSON(w, r, http.StatusBadRequest, apiFailure{Error: "Invalid device id"})
		return false
	}
	return true
}

// device answers POST /api/mesh/devices/{id}/<action>. withBody sends the
// request's JSON body along.
func (h *handlers) device(action string, withBody bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !validDevice(w, r) {
			return
		}
		var opts []goapi.CallOption
		if withBody {
			opts = append(opts, requestBody(r))
		}
		h.pass(w, r, http.MethodPost, devicePath(r.PathValue("id"), action), opts...)
	}
}

// meshOff is the answer of GET /api/mesh/status when the API server gave
// no status.
type meshOff struct {
	Enabled bool `json:"enabled"`
	Error   any  `json:"error"`
}

// status answers GET /api/mesh/status with the API server's status
// object. When the API server cannot be reached, refuses the call or
// answers with an error, the answer is 502 {"enabled":false,"error":...}.
func (h *handlers) status(w http.ResponseWriter, r *http.Request) {
	result := h.api.GetMeshStatus(r.Context())
	if !goapi.Truthy(result.Get("success")) {
		kit.JSON(w, r, http.StatusBadGateway, meshOff{Error: result.Get("error")})
		return
	}
	if data := result.Get("data"); goapi.Truthy(data) {
		kit.JSON(w, r, http.StatusOK, data)
		return
	}
	kit.JSON(w, r, http.StatusOK, result)
}

// saveGroups answers POST /api/mesh/groups.
func (h *handlers) saveGroups(w http.ResponseWriter, r *http.Request) {
	h.pass(w, r, http.MethodPost, "/mesh/groups", requestBody(r))
}

// download answers GET /api/mesh/download.msh: the settings file a mesh
// agent is installed with. The query goes to the API server (name,
// mesh_id, mesh_server). Whatever type the API server names, the file
// goes out as application/octet-stream under the name meshagents.msh.
func (h *handlers) download(w http.ResponseWriter, r *http.Request) {
	search, ok := requestQuery(r).search()
	if !ok {
		kit.JSON(w, r, http.StatusInternalServerError, apiFailure{Error: notText})
		return
	}
	resp, err := h.api.Get(r.Context(), withSearch("/mesh/download.msh", search))
	if err != nil {
		kit.JSON(w, r, http.StatusInternalServerError, apiFailure{Error: err.Error()})
		return
	}
	w.Header().Set("Content-Disposition", `attachment; filename="meshagents.msh"`)
	httpx.WriteBody(w, r, resp.Status, "application/octet-stream", resp.Body)
}

// validateShare answers GET /api/mesh/share/validate, which needs no
// session: the link's token is the credential.
func (h *handlers) validateShare(w http.ResponseWriter, r *http.Request) {
	search, ok := requestQuery(r).search()
	if !ok {
		kit.JSON(w, r, http.StatusInternalServerError, apiFailure{Error: unreachable})
		return
	}
	h.pass(w, r, http.MethodGet, withSearch("/mesh/share/validate", search))
}

// recording answers GET /api/mesh/recordings/{id}; "." and ".." are 400.
func (h *handlers) recording(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if isDotSegment(id) {
		kit.JSON(w, r, http.StatusBadRequest, apiFailure{Error: "Invalid recording id"})
		return
	}
	h.pass(w, r, http.MethodGet, "/mesh/recordings/"+goapi.EncodeURIComponent(id))
}

// withSearch adds a query string to a path when there is one.
func withSearch(path, search string) string {
	if search == "" {
		return path
	}
	return path + "?" + search
}
