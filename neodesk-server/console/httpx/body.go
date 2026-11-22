package httpx

import (
	"net/http"
	"strconv"
)

// WriteBody answers with a body that is not JSON: a page or plain text.
//
// contentType is set as given (an empty one leaves the header alone). The
// answer carries a weak ETag unless one is set already, and a GET or HEAD
// whose validators still hold (Fresh, with the ETag and Last-Modified of
// the answer) gets 304 without a body when the status is a 2xx. That is so
// even for an answer with Cache-Control: no-store. HEAD gets the headers of
// the answer and no body.
func WriteBody(w http.ResponseWriter, r *http.Request, status int, contentType string, body []byte) {
	h := w.Header()
	if contentType != "" {
		h.Set("Content-Type", contentType)
	}
	h.Set("Content-Length", strconv.Itoa(len(body)))
	if h.Get("ETag") == "" {
		h.Set("ETag", ETag(body))
	}
	if status >= 200 && status < 300 || status == http.StatusNotModified {
		lastModified, _ := http.ParseTime(h.Get("Last-Modified"))
		if Fresh(r, h.Get("ETag"), lastModified) {
			status = http.StatusNotModified
		}
	}
	switch status {
	case http.StatusNoContent, http.StatusNotModified:
		h.Del("Content-Type")
		h.Del("Content-Length")
		h.Del("Transfer-Encoding")
		w.WriteHeader(status)
		return
	case http.StatusResetContent:
		h.Set("Content-Length", "0")
		h.Del("Transfer-Encoding")
		w.WriteHeader(status)
		return
	}
	w.WriteHeader(status)
	if r.Method != http.MethodHead {
		_, _ = w.Write(body)
	}
}
