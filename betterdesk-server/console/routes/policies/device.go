package policies

import (
	"net/http"

	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/goapipath"
	"github.com/unitronix/betterdesk-server/console/kit"
)

// devicePolicy answers GET /api/bd/device-policy: the policy of a device
// as the API server has it. The device is the query's device_id, else the
// header X-Device-Id, else the token's device. The match guard in front
// looks at the body first, so a request that names its own device in the
// body may name any other one in the query or the header (BUGS DV1).
//
//	the device_id has no text, or is no id   400 {"error":"..."}
//	no answer from the API server            500 {"error":"Policy fetch failed"}
//	any other failure                        its status and body
func (h *handlers) devicePolicy(w http.ResponseWriter, r *http.Request) {
	raw := kit.Device(r).DeviceID
	if query := goapipath.Query(r, "device_id"); query.Truthy() {
		text, err := query.Text()
		if err != nil {
			kit.JSON(w, r, http.StatusBadRequest, goapipath.ErrorBody{Error: err.Error()})
			return
		}
		raw = text
	} else if header, _ := kit.DeviceIDHeader(r); header != "" {
		raw = header
	}
	id, err := goapipath.AssertSafeAPIID(raw, "device_id")
	if err != nil {
		kit.JSON(w, r, http.StatusBadRequest, goapipath.ErrorBody{Error: err.Error()})
		return
	}
	resp, err := h.api.Get(r.Context(), "/peers/"+goapi.EncodeURIComponent(id)+"/policy")
	if err != nil {
		status, data := goapipath.FailureOf(err, "Policy fetch failed")
		goapipath.WriteJSON(w, r, status, data)
		return
	}
	goapipath.WriteJSON(w, r, http.StatusOK, resp.Data())
}

// report answers POST /api/bd/attestation with {device_id, fingerprint,
// platform_data?}: the device's attestation goes to the API server as
// {fingerprint, platform_data}. The match guard in front made the text of
// device_id the token's device id, which is always an id the API path
// takes.
//
//	device_id or fingerprint missing or empty   400 {"error":"device_id and fingerprint required"}
//	no answer from the API server               500 {"error":"Attestation report failed"}
//	any other failure                           its status and body
func (h *handlers) report(w http.ResponseWriter, r *http.Request) {
	body := kit.Body(r)
	if !body.Get("device_id").Truthy() || !body.Get("fingerprint").Truthy() {
		kit.JSON(w, r, http.StatusBadRequest, goapipath.ErrorBody{Error: "device_id and fingerprint required"})
		return
	}
	data := goapi.NewObject("fingerprint", parsed(body.Raw("fingerprint")))
	if body.Has("platform_data") {
		data.Set("platform_data", parsed(body.Raw("platform_data")))
	}
	path := "/peers/" + goapi.EncodeURIComponent(kit.Device(r).DeviceID) + "/attestation"
	resp, err := h.api.Request(r.Context(), http.MethodPost, path, goapipath.Body(data))
	if err != nil {
		status, data := goapipath.FailureOf(err, "Attestation report failed")
		goapipath.WriteJSON(w, r, status, data)
		return
	}
	goapipath.WriteJSON(w, r, http.StatusOK, resp.Data())
}

// parsed is a member of the body as JSON.parse gave it.
func parsed(raw []byte) any {
	value, err := goapi.Parse(raw)
	if err != nil {
		return nil
	}
	return value
}
