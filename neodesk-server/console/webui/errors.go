package webui

import (
	"errors"
	"io/fs"
	"net/http"
	"strings"

	"github.com/neodeskapp/neodesk/neodesk-server/console/httpx"
	"github.com/neodeskapp/neodesk/neodesk-server/logging"
)

// failure is the JSON of an error answer.
type failure struct {
	Success bool   `json:"success"`
	Error   string `json:"error"`
}

const plainText = "text/plain; charset=utf-8"

// wantsJSON reports whether a request is answered in JSON whatever it
// accepts: the API, and requests that send JSON.
func wantsJSON(r *http.Request, path string) bool {
	return strings.HasPrefix(path, "/api/") || strings.Contains(strings.Join(r.Header.Values("Content-Type"), ", "), "application/json")
}

func acceptsHTML(r *http.Request) bool {
	return httpx.Accepts(r, "text/html") != ""
}

// NotFound answers what no route and no page of the app answered: other
// methods, missing files, the API and sockets. A browser gets the app's
// not-found page (plain text without a build); the API, sockets and
// clients that do not take HTML get JSON.
//
// Unmatched paths under /api/ and /ws/ are logged with the client's
// address (Options.ClientIP) and the first 80 characters of the user
// agent, quoted with Go's escapes so that it cannot add to the line.
func (h *Host) NotFound(w http.ResponseWriter, r *http.Request) {
	req := parseRequest(r)
	if strings.HasPrefix(req.original, "/api/") || strings.HasPrefix(req.original, "/ws/") {
		var from string
		if h.clientIP != nil {
			from = h.clientIP(r)
		} else {
			from = httpx.SocketIP(r)
		}
		if from == "" {
			from = "unknown"
		}
		agent := []rune(httpx.Latin1(r.Header.Get("User-Agent")))
		if len(agent) > 80 {
			agent = agent[:80]
		}
		logging.Logf("panel", `404 %s %s from %s ua=%q`, r.Method, req.original, from, string(agent))
	}
	if !isServerPath(req.path) && acceptsHTML(r) {
		if !h.ErrorPage(w, r, http.StatusNotFound, ErrorOptions{}) {
			httpx.WriteBody(w, r, http.StatusNotFound, plainText, []byte("Not Found"))
		}
		return
	}
	writeJSON(w, r, http.StatusNotFound, failure{Error: "Not Found"})
}

// ServerError answers a request that failed. status is the status of the
// answer; 0 means 500. The answer says what went wrong (err's message). In
// production it does so only below 500, where the messages come from the
// console's own parsers and checks and are written for the client: a
// failure of the server (500 and above) says "Internal Server Error", and
// an error of the file system, whose message names a path on the server,
// says the status's text ("Not Found").
//
// The API and requests that send JSON get JSON, a browser gets the app's
// error page (plain text without a build), and everyone else JSON.
//
// A refused CSRF token is answered by CSRFError, which the CSRF gate calls
// itself; what an error's message says does not change the answer.
func (h *Host) ServerError(w http.ResponseWriter, r *http.Request, err error, status int) {
	logging.Logf("panel", "Server error: %v", err)
	if status == 0 {
		status = http.StatusInternalServerError
	}
	detail := ""
	switch {
	case err == nil:
	case !h.production:
		detail = err.Error()
	case status < http.StatusInternalServerError && fromFileSystem(err):
		detail = http.StatusText(status)
	case status < http.StatusInternalServerError:
		detail = err.Error()
	}
	shown := detail
	if shown == "" {
		shown = "Internal Server Error"
	}
	switch {
	case wantsJSON(r, parseRequest(r).path):
		writeJSON(w, r, status, failure{Error: shown})
	case acceptsHTML(r):
		if !h.ErrorPage(w, r, http.StatusInternalServerError, ErrorOptions{Status: status, Message: detail}) {
			httpx.WriteBody(w, r, status, plainText, []byte(shown))
		}
	default:
		writeJSON(w, r, status, failure{Error: shown})
	}
}

// fromFileSystem reports whether err is or wraps an error of the file
// system (a failed stat or open), whose message holds the path it failed
// on.
func fromFileSystem(err error) bool {
	var path *fs.PathError
	return errors.As(err, &path)
}

// CSRFError answers a request whose CSRF token is missing or wrong, with
// status 403. When the console runs without HTTPS and the request did not
// come over HTTPS either, the message adds a hint: a browser that still
// holds the cookies of an earlier HTTPS setup does not send them.
func (h *Host) CSRFError(w http.ResponseWriter, r *http.Request) {
	hint := ""
	if !h.https && !h.isSecure(r) {
		hint = " If you recently disabled SSL, clear your browser cookies for this site and reload."
	}
	switch {
	case wantsJSON(r, parseRequest(r).path):
		writeJSON(w, r, http.StatusForbidden, failure{Error: "Invalid CSRF token. Please refresh the page and try again." + hint})
	case acceptsHTML(r):
		message := "Invalid or missing CSRF token. Please refresh the page and try again." + hint
		if !h.ErrorPage(w, r, http.StatusForbidden, ErrorOptions{Message: message}) {
			httpx.WriteBody(w, r, http.StatusForbidden, plainText, []byte(message))
		}
	default:
		writeJSON(w, r, http.StatusForbidden, failure{Error: "Invalid CSRF token" + hint})
	}
}

func (h *Host) isSecure(r *http.Request) bool {
	if h.secure != nil {
		return h.secure(r)
	}
	return r.TLS != nil
}

// fail answers a request Handle could not serve.
func (h *Host) fail(w http.ResponseWriter, r *http.Request, err *httpError) {
	h.ServerError(w, r, err, err.status)
}

// writeJSON answers with JSON. A Content-Type that is set already stays,
// with its charset set to utf-8: an error answer for a file keeps the
// file's type.
func writeJSON(w http.ResponseWriter, r *http.Request, status int, v any) {
	if current := w.Header().Get("Content-Type"); current != "" {
		mediaType, _, _ := strings.Cut(current, ";")
		w.Header().Set("Content-Type", strings.ToLower(strings.TrimSpace(mediaType))+"; charset=utf-8")
	}
	httpx.WriteJSON(w, r, status, v)
}

// endEmpty answers with a status and no body.
func endEmpty(w http.ResponseWriter, r *http.Request, status int) {
	if r.Method != http.MethodHead {
		w.Header().Set("Content-Length", "0")
	}
	w.WriteHeader(status)
}
