package jsreq

import (
	"net/http"
	"strings"

	"github.com/unitronix/betterdesk-server/console/access"
	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/httpx"
)

// SafeSegment is safeSegment(value, label) of lib/goApiProxy.js: the value
// trimmed, checked to be one path segment of plain characters
// (assertSafeApiId: letters, digits and . _ : @ % + -, at most 256) and
// escaped for a path. The error reads "Invalid <label>".
func SafeSegment(value, label string) (string, error) {
	safe, err := access.SafeAPIID(value, label)
	if err != nil {
		return "", err
	}
	return goapi.EncodeURIComponent(safe), nil
}

type proxyError struct {
	Error string `json:"error"`
}

// ProxyToGo is proxyToGo of lib/goApiProxy.js: it sends one request to
// the API server and passes the answer on, status and JSON. path builds
// the path below /api and may refuse a part of it.
//
//	path refused, or the client refused the path   400 {"error":"Invalid ..."}
//	the API server answered an error               its status and its body
//	no answer, or an error without a body          the status or 500, {"error":"Go server unreachable"}
func ProxyToGo(w http.ResponseWriter, r *http.Request, c *goapi.Client, method string, path func() (string, error), opts ...goapi.CallOption) {
	target, err := path()
	if err != nil {
		answerProxyError(w, r, err.Error(), nil)
		return
	}
	resp, err := c.Request(r.Context(), method, target, opts...)
	if err != nil {
		answerProxyError(w, r, err.Error(), goapi.AsError(err))
		return
	}
	httpx.WriteJSON(w, r, resp.Status, resp.Data())
}

func answerProxyError(w http.ResponseWriter, r *http.Request, message string, e *goapi.Error) {
	if strings.HasPrefix(message, "Invalid ") {
		httpx.WriteJSON(w, r, http.StatusBadRequest, proxyError{Error: message})
		return
	}
	status := http.StatusInternalServerError
	if s := e.Status(); s != 0 {
		status = s
	}
	if data := e.Data(); goapi.Truthy(data) {
		httpx.WriteJSON(w, r, status, data)
		return
	}
	httpx.WriteJSON(w, r, status, proxyError{Error: "Go server unreachable"})
}
