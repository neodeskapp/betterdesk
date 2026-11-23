package goapipath

import (
	"bytes"
	"net/http"
	"regexp"
	"strings"

	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
	"github.com/neodeskapp/neodesk/neodesk-server/console/httpx"
	"github.com/neodeskapp/neodesk/neodesk-server/console/middleware"
)

// Path gives the path of a call on the API server, relative to /api, or
// the reason it cannot be built from this request.
type Path func() (string, error)

// Fixed is a path that does not depend on the request.
func Fixed(path string) Path {
	return func() (string, error) { return path, nil }
}

var patternParam = regexp.MustCompile(`\{([A-Za-z0-9_]+)\}`)

// PathOf builds a path from a pattern and the route's parameters: every
// {name} becomes SafeSegment(r.PathValue(name), name). The first
// parameter that is refused, from the left, is the error.
//
//	PathOf(r, "/fleet/resources/{orgId}/{resourceId}")
func PathOf(r *http.Request, pattern string) Path {
	return func() (string, error) {
		var failed error
		path := patternParam.ReplaceAllStringFunc(pattern, func(match string) string {
			if failed != nil {
				return ""
			}
			name := match[1 : len(match)-1]
			segment, err := SafeSegment(r.PathValue(name), name)
			if err != nil {
				failed = err
			}
			return segment
		})
		if failed != nil {
			return "", failed
		}
		return path, nil
	}
}

// RequestBody is req.body as a value for Body: the JSON the client
// sent after JSON.parse, an object or an array, and the empty object when
// the request has no body the console reads.
func RequestBody(r *http.Request) any {
	value, err := goapi.Parse(middleware.BodyValue(r).Raw())
	if err != nil {
		return goapi.NewObject()
	}
	return value
}

// Body is the body of a call for the value v, a parsed JSON value
// (RequestBody, goapi.Parse), written as JSON.stringify writes it, with
// every member it has.
func Body(v any) goapi.CallOption {
	body, err := stringify(Plain(v))
	if err != nil {
		return goapi.Body(v)
	}
	return goapi.RawBody(body)
}

// Plain returns a parsed JSON value with every negative zero as 0, which
// is how JSON.stringify writes one. WriteJSON does it for a whole answer;
// a route that picks values out of an answer uses it on those.
func Plain(v any) any {
	switch t := v.(type) {
	case float64:
		if t == 0 {
			return float64(0)
		}
	case []any:
		out := make([]any, len(t))
		for i, element := range t {
			out[i] = Plain(element)
		}
		return out
	case *goapi.Object:
		if t == nil {
			return v
		}
		out := goapi.NewObject()
		for _, key := range t.Keys() {
			out.Set(key, Plain(t.Get(key)))
		}
		return out
	}
	return v
}

// WriteJSON answers with v as JSON, written the way JSON.stringify writes
// it: res.status(status).json(v) for a value that came from the API
// server or holds text from a request. It differs from kit.JSON in two
// characters, the line separator U+2028 and the paragraph separator
// U+2029, which go out as they are and not as escapes, and in a negative
// zero, which is 0.
func WriteJSON(w http.ResponseWriter, r *http.Request, status int, v any) {
	body, err := stringify(Plain(v))
	if err != nil {
		httpx.WriteJSON(w, r, status, v)
		return
	}
	httpx.WriteJSONBytes(w, r, status, body)
}

// stringify is JSON.stringify(v) for a value whose negative zeros are
// gone.
func stringify(v any) ([]byte, error) {
	body, err := httpx.Marshal(v)
	if err != nil {
		return nil, err
	}
	return rawSeparators(body), nil
}

// rawSeparators replaces the escapes for U+2028 and U+2029 in JSON text by
// the characters themselves.
func rawSeparators(body []byte) []byte {
	if !bytes.Contains(body, []byte(`\u202`)) {
		return body
	}
	out := make([]byte, 0, len(body))
	for i := 0; i < len(body); i++ {
		if body[i] != '\\' || i+1 >= len(body) {
			out = append(out, body[i])
			continue
		}
		// A backslash starts an escape of two characters, or of six for
		// \uXXXX; an escaped backslash is skipped as a pair.
		if code := body[i+1:]; len(code) >= 5 && code[0] == 'u' && (string(code[1:5]) == "2028" || string(code[1:5]) == "2029") {
			out = append(out, 0xE2, 0x80, 0xA8+code[4]-'8')
			i += 5
			continue
		}
		out = append(out, body[i], body[i+1])
		i++
	}
	return out
}

// ErrorBody is the answer {"error": text} of the proxy routes.
type ErrorBody struct {
	Error string `json:"error"`
}

const unreachable = "Go server unreachable"

// ProxyToGo calls the API server and answers with its status and its
// body as JSON. method is the HTTP method in upper case; opts carry the
// body (Body(RequestBody(r))) when the route sends one.
//
//	the path is refused ("Invalid ...")   400 {"error": the text}, no call
//	the API server answers 2xx            its status and its body
//	the API server answers otherwise      its status and its body; when the body is
//	                                      empty, false, 0 or null: {"error":"Go server unreachable"}
//	no answer (timeout, a broken path)    500 {"error":"Go server unreachable"}
//
// A body of the API server that is not JSON is answered as a JSON string.
func ProxyToGo(api *goapi.Client, w http.ResponseWriter, r *http.Request, method string, path Path, opts ...goapi.CallOption) {
	target, err := path()
	var resp *goapi.Response
	if err == nil {
		resp, err = api.Request(r.Context(), method, target, opts...)
	}
	if err == nil {
		WriteJSON(w, r, resp.Status, resp.Data())
		return
	}
	if IsInvalid(err) {
		httpx.WriteJSON(w, r, http.StatusBadRequest, ErrorBody{Error: err.Error()})
		return
	}
	status, data := FailureOf(err, unreachable)
	WriteJSON(w, r, status, data)
}

// FailureOf is what the proxy routes answer for a failed call: the API
// server's status and body, with 500 for a call without an answer and
// {"error": fallback} for a body that is empty, false, 0 or null. It is
// the pair
//
//	err.response?.status || 500
//	err.response?.data || { error: fallback }
func FailureOf(err error, fallback string) (status int, data any) {
	failed := goapi.AsError(err)
	status = failed.Status()
	if status == 0 {
		status = http.StatusInternalServerError
	}
	data = failed.Data()
	if !goapi.Truthy(data) {
		data = ErrorBody{Error: fallback}
	}
	return status, data
}

var hasCharset = regexp.MustCompile(`;\s*charset\s*=`)

var textLike = regexp.MustCompile(`^text/|^application/(javascript|json)`)

// ProxyBinaryToGo calls the API server and answers with its status and
// its body as the bytes they are, with the Content-Type and
// Content-Disposition it sent. A text type, JSON or JavaScript without a
// charset gets "; charset=utf-8"; no type at all is
// application/octet-stream.
//
//	the path is refused ("Invalid ...")   400 {"error": the text}, no call
//	the API server answers 2xx            its status, type, disposition and body
//	the API server answers otherwise      its status with its body as JSON when the body is JSON,
//	                                      else {"error":"Go server unreachable"}
//	no answer                             500 {"error":"Go server unreachable"}
func ProxyBinaryToGo(api *goapi.Client, w http.ResponseWriter, r *http.Request, method string, path Path) {
	target, err := path()
	var resp *goapi.Response
	if err == nil {
		resp, err = api.Request(r.Context(), method, target)
	}
	if err == nil {
		contentType := resp.Header.Get("Content-Type")
		switch {
		case contentType == "":
			contentType = "application/octet-stream"
		case !hasCharset.MatchString(contentType):
			mediaType, _, _ := strings.Cut(contentType, ";")
			if textLike.MatchString(mediaType) {
				contentType += "; charset=utf-8"
			}
		}
		if disposition := resp.Header.Get("Content-Disposition"); disposition != "" {
			w.Header().Set("Content-Disposition", disposition)
		}
		httpx.WriteBody(w, r, resp.Status, contentType, resp.Body)
		return
	}
	if IsInvalid(err) {
		httpx.WriteJSON(w, r, http.StatusBadRequest, ErrorBody{Error: err.Error()})
		return
	}
	failed := goapi.AsError(err)
	status := failed.Status()
	if status == 0 {
		status = http.StatusInternalServerError
	}
	if failed != nil && failed.Response != nil {
		if value, parseErr := goapi.Parse(failed.Response.Body); parseErr == nil {
			WriteJSON(w, r, status, value)
			return
		}
	}
	httpx.WriteJSON(w, r, status, ErrorBody{Error: unreachable})
}
