package middleware

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"

	"github.com/neodeskapp/neodesk/neodesk-server/console/httpx"
)

const (
	// DefaultBodyLimit is the largest JSON or form body the console reads,
	// counted after decompression: 2 MB.
	DefaultBodyLimit = 2 << 20
	// AttachmentBodyLimit is the limit of the ticket attachment route:
	// 35 MB, which is a 25 MB file as base64 with its JSON around it.
	AttachmentBodyLimit = 35 << 20
	// GeneratorBodyLimit is the limit of the generator's bundle and
	// preview routes: 16 MB, a logo of 10 MB as base64 in the branding.
	GeneratorBodyLimit = 16 << 20
	// PairingBodyLimit is the limit of POST /api/panel/pairing/codes.
	PairingBodyLimit = 4 << 10
	// UIShellBodyLimit is the limit of POST /api/ui-shell.
	UIShellBodyLimit = 2 << 10
)

// The types of a BodyError, as the console's error handler and logs know
// them.
const (
	ErrTypeTooLarge            = "entity.too.large"
	ErrTypeParseFailed         = "entity.parse.failed"
	ErrTypeCharsetUnsupported  = "charset.unsupported"
	ErrTypeEncodingUnsupported = "encoding.unsupported"
	ErrTypeAborted             = "request.aborted"
	ErrTypeTooManyParameters   = "parameters.too.many"
)

// BodyError is why a request body was refused.
type BodyError struct {
	// Status is the HTTP status the error asks for: 400, 413 or 415.
	Status int
	// Type names the cause; it is empty for a body whose compression is
	// broken.
	Type string
	// Message is the text the console shows outside production.
	Message string
	// Err is the underlying error, if there is one.
	Err error
}

func (e *BodyError) Error() string { return e.Message }

func (e *BodyError) Unwrap() error { return e.Err }

// BodyOptions configure the body parsers.
type BodyOptions struct {
	// Limit is the largest body in bytes after decompression. Zero means
	// DefaultBodyLimit.
	Limit int64
	// RouteBodies makes Bodies leave the JSON of the routes with a limit of
	// their own unread (the generator's bundles and preview, pairing codes,
	// /api/ui-shell; see readsItsOwnBody): each of them reads it with
	// RouteJSON and its limit after its checks.
	RouteBodies bool
	// OnError answers a request whose body was refused.
	OnError ErrorHandler
}

func (o BodyOptions) limit() int64 {
	if o.Limit > 0 {
		return o.Limit
	}
	return DefaultBodyLimit
}

type bodyKey struct{}

// bodyState is what the parsers leave for the handlers of one request.
type bodyState struct {
	// parsed says a parser read the body
	parsed bool
	// text is the body as JSON: what the client sent for a JSON body, an
	// object of strings for a form. Empty means {}.
	text []byte

	// object is text read as an object, made when a handler first asks
	mu     sync.Mutex
	object *JSONObject
}

// setText stores a body that was read.
func (s *bodyState) setText(text []byte) {
	s.mu.Lock()
	s.text, s.object = text, nil
	s.mu.Unlock()
}

func stateOf(r *http.Request) *bodyState {
	state, _ := r.Context().Value(bodyKey{}).(*bodyState)
	return state
}

// withState gives the request a body state if it has none.
func withState(r *http.Request) (*http.Request, *bodyState) {
	if state := stateOf(r); state != nil {
		return r, state
	}
	state := &bodyState{}
	return r.WithContext(context.WithValue(r.Context(), bodyKey{}, state)), state
}

var attachmentPath = regexp.MustCompile(`^/api/tickets/[0-9]+/attachments/?$`)

// ownBodyRoutes are the routes that read their JSON body themselves, with
// a limit of their own, by method and path in any letter case.
var ownBodyRoutes = []struct {
	method string
	path   *regexp.Regexp
}{
	{http.MethodPost, regexp.MustCompile(`(?i)^/api/generator/bundles/?$`)},
	{http.MethodPut, regexp.MustCompile(`(?i)^/api/generator/bundles/[^/]+/?$`)},
	{http.MethodPost, regexp.MustCompile(`(?i)^/api/generator/preview/?$`)},
	{http.MethodPost, regexp.MustCompile(`(?i)^/api/panel/pairing/codes/?$`)},
	{http.MethodPost, regexp.MustCompile(`(?i)^/api/ui-shell/?$`)},
}

// readsItsOwnBody reports whether r goes to a route of ownBodyRoutes.
func readsItsOwnBody(r *http.Request) bool {
	p := httpx.RawPath(r)
	for _, route := range ownBodyRoutes {
		if r.Method == route.method && route.path.MatchString(p) {
			return true
		}
	}
	return false
}

// Bodies reads the body of a request that says it is JSON
// (application/json) or a form (application/x-www-form-urlencoded), up to
// the limit, and keeps it for Body and Bind. Other types are not read.
//
// JSON has to be an object or an array; an empty body counts as {}. The
// charset may be UTF-8 or UTF-16, the Content-Encoding gzip, deflate or
// identity. A form has flat keys and at most 1000 pairs.
//
// POST /api/tickets/{id}/attachments is not read here when it is JSON:
// its route does that with AttachmentBody. With RouteBodies the same goes
// for the routes with a limit of their own.
func Bodies(opts BodyOptions) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r, state := withState(r)
			skipJSON := r.Method == http.MethodPost && attachmentPath.MatchString(httpx.RawPath(r)) ||
				opts.RouteBodies && readsItsOwnBody(r)
			if !skipJSON {
				if err := parseJSONBody(r, state, opts.limit()); err != nil {
					opts.OnError.handle(w, r, err.Status, err)
					return
				}
			}
			if err := parseFormBody(r, state, opts.limit()); err != nil {
				opts.OnError.handle(w, r, err.Status, err)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RouteJSON is a JSON parser with its own limit for one route. It reads
// the body only if no parser has read it before.
func RouteJSON(opts BodyOptions) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r, state := withState(r)
			if err := parseJSONBody(r, state, opts.limit()); err != nil {
				opts.OnError.handle(w, r, err.Status, err)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// AttachmentBody is the parser of POST /api/tickets/{id}/attachments: JSON
// up to 35 MB. It goes after the route's sign-in and permission checks.
func AttachmentBody(onError ErrorHandler) func(http.Handler) http.Handler {
	return RouteJSON(BodyOptions{Limit: AttachmentBodyLimit, OnError: onError})
}

func parseJSONBody(r *http.Request, state *bodyState, limit int64) *BodyError {
	if state.parsed {
		return nil
	}
	mt, ok := requestMediaType(r)
	if !ok || mt.full != "application/json" {
		return nil
	}
	name := "utf-8"
	if param := strings.ToLower(latin1(mt.params["charset"])); param != "" {
		name = param
	}
	if !strings.HasPrefix(name, "utf-") {
		return unsupportedCharset(name)
	}
	text, err := readBody(r, state, limit, name)
	if err != nil {
		return err
	}
	if len(text) == 0 {
		return nil
	}
	text = bytes.Trim(text, " \t\n\r")
	if len(text) == 0 || (text[0] != '{' && text[0] != '[') {
		return &BodyError{Status: http.StatusBadRequest, Type: ErrTypeParseFailed, Message: "invalid JSON: the body has to be an object or an array"}
	}
	if !json.Valid(text) {
		var probe json.RawMessage
		syntax := json.Unmarshal(text, &probe)
		return &BodyError{Status: http.StatusBadRequest, Type: ErrTypeParseFailed, Message: "invalid JSON: " + syntax.Error(), Err: syntax}
	}
	state.setText(text)
	return nil
}

func parseFormBody(r *http.Request, state *bodyState, limit int64) *BodyError {
	if state.parsed {
		return nil
	}
	mt, ok := requestMediaType(r)
	if !ok || mt.full != "application/x-www-form-urlencoded" {
		return nil
	}
	name := "utf-8"
	if param := strings.ToLower(latin1(mt.params["charset"])); param != "" {
		name = param
	}
	if name != "utf-8" {
		return unsupportedCharset(name)
	}
	raw, err := readBody(r, state, limit, name)
	if err != nil {
		return err
	}
	if len(raw) == 0 {
		return nil
	}
	text := string(raw)
	if tooManyParameters(text) {
		return &BodyError{Status: http.StatusRequestEntityTooLarge, Type: ErrTypeTooManyParameters, Message: "too many parameters"}
	}
	obj := JSONObject{values: map[string]Value{}}
	for _, field := range parseForm(text) {
		if len(field.values) == 1 {
			obj.set(field.key, stringValue(field.values[0]))
			continue
		}
		raw, _ := httpx.Marshal(field.values)
		obj.set(field.key, rawValue(raw))
	}
	obj.sortKeys()
	encoded, _ := obj.MarshalJSON()
	state.setText(encoded)
	return nil
}

func unsupportedCharset(name string) *BodyError {
	return &BodyError{Status: http.StatusUnsupportedMediaType, Type: ErrTypeCharsetUnsupported,
		Message: `unsupported charset "` + strings.ToUpper(name) + `"`}
}

func tooLarge() *BodyError {
	return &BodyError{Status: http.StatusRequestEntityTooLarge, Type: ErrTypeTooLarge, Message: "request entity too large"}
}

// trackingReader remembers a read error of the request itself, so a
// broken connection can be told from broken compression.
type trackingReader struct {
	r   io.Reader
	err error
}

func (t *trackingReader) Read(p []byte) (int, error) {
	n, err := t.r.Read(p)
	if err != nil && err != io.EOF {
		t.err = err
	}
	return n, err
}

// readBody reads the request body up to limit bytes, undoes its
// Content-Encoding and decodes its charset. Once it has started, the body
// counts as read, also when it fails.
func readBody(r *http.Request, state *bodyState, limit int64, charsetName string) ([]byte, *BodyError) {
	state.parsed = true

	encoding := strings.ToLower(headerValue(r.Header, "Content-Encoding"))
	if encoding == "" {
		encoding = "identity"
	}
	if encoding != "identity" && encoding != "gzip" && encoding != "deflate" {
		return nil, &BodyError{Status: http.StatusUnsupportedMediaType, Type: ErrTypeEncodingUnsupported,
			Message: `unsupported content encoding "` + encoding + `"`}
	}
	// A refused body is read to its end before the answer goes out, so a
	// client that is still sending gets the answer and not a reset.
	fail := func(err *BodyError) ([]byte, *BodyError) {
		_, _ = io.Copy(io.Discard, r.Body)
		return nil, err
	}
	if encoding == "identity" && r.ContentLength > limit {
		return fail(tooLarge())
	}
	cs := lookupCharset(charsetName)
	if cs == charsetUnsupported {
		return fail(unsupportedCharset(charsetName))
	}

	source := &trackingReader{r: r.Body}
	var reader io.Reader = source
	var openErr error
	switch encoding {
	case "gzip":
		reader, openErr = newGzipMembers(source)
	case "deflate":
		reader, openErr = zlib.NewReader(source)
	}
	var data []byte
	if openErr == nil {
		data, openErr = io.ReadAll(io.LimitReader(reader, limit+1))
	}
	switch {
	case source.err != nil:
		return nil, &BodyError{Status: http.StatusBadRequest, Type: ErrTypeAborted, Message: "request aborted", Err: source.err}
	case openErr != nil:
		return fail(&BodyError{Status: http.StatusBadRequest, Message: inflateMessage(openErr), Err: openErr})
	case int64(len(data)) > limit:
		return fail(tooLarge())
	}
	return decodeBody(data, cs), nil
}

// gzipMembers reads a gzip body: one compressed member or several in a
// row. Bytes that do not start a member are an error where zlib reports
// one: at the first byte that is not gzip's.
type gzipMembers struct {
	src *bufio.Reader
	zr  *gzip.Reader
}

func newGzipMembers(source io.Reader) (*gzipMembers, error) {
	g := &gzipMembers{src: bufio.NewReader(source)}
	if err := g.checkMagic(); err != nil {
		return nil, err
	}
	zr, err := gzip.NewReader(g.src)
	if err != nil {
		return nil, err
	}
	zr.Multistream(false)
	g.zr = zr
	return g, nil
}

// checkMagic looks at the next two bytes without taking them.
func (g *gzipMembers) checkMagic() error {
	magic, err := g.src.Peek(2)
	switch {
	case len(magic) == 0:
		if err == nil || err == io.EOF {
			return io.ErrUnexpectedEOF
		}
		return err
	case magic[0] != 0x1f, len(magic) == 2 && magic[1] != 0x8b:
		return gzip.ErrHeader
	}
	return nil
}

func (g *gzipMembers) Read(p []byte) (int, error) {
	for {
		n, err := g.zr.Read(p)
		if err != io.EOF {
			return n, err
		}
		if n > 0 {
			return n, nil
		}
		// The member is complete. More bytes have to be another one.
		if _, err := g.src.Peek(1); err != nil {
			return 0, err
		}
		if err := g.checkMagic(); err != nil {
			return 0, err
		}
		if err := g.zr.Reset(g.src); err != nil {
			return 0, err
		}
		g.zr.Multistream(false)
	}
}

// inflateMessage is the text for a body whose compression is broken, in
// zlib's words where they are known.
func inflateMessage(err error) string {
	switch {
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return "unexpected end of file"
	case errors.Is(err, gzip.ErrHeader), errors.Is(err, zlib.ErrHeader):
		return "incorrect header check"
	case errors.Is(err, gzip.ErrChecksum), errors.Is(err, zlib.ErrChecksum):
		return "incorrect data check"
	}
	return "invalid compressed data"
}

// Body is req.body as an object: the members of a JSON object, the fields
// of a form. It is empty when the request had no body the console reads,
// and also when the body is a JSON array (BodyValue has that), which is
// what plainBodyObject(req.body) of lib/bodyScalars.js gave.
func Body(r *http.Request) JSONObject {
	state := stateOf(r)
	if state == nil {
		return JSONObject{}
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.object == nil {
		obj := JSONObject{}
		if len(state.text) > 0 && state.text[0] == '{' {
			obj = parseObject(state.text)
		}
		state.object = &obj
	}
	return *state.object
}

// WithJSONBody is r with text as the JSON body its handlers read (Body,
// BodyValue, Bind), for a route that runs another route's code once per
// item of its own body. r itself keeps its body.
func WithJSONBody(r *http.Request, text []byte) *http.Request {
	state := &bodyState{parsed: true, text: append([]byte(nil), text...)}
	return r.WithContext(context.WithValue(r.Context(), bodyKey{}, state))
}

// BodyValue is req.body whatever it is: an object or, for a JSON body,
// possibly an array. Without a body it is the empty object.
func BodyValue(r *http.Request) Value {
	if state := stateOf(r); state != nil {
		state.mu.Lock()
		text := state.text
		state.mu.Unlock()
		if len(text) > 0 {
			return rawValue(text)
		}
	}
	return rawValue([]byte("{}"))
}

// BodyParsed reports whether a parser read the request's body. It is
// false for a request without a body and for types the console does not
// read.
func BodyParsed(r *http.Request) bool {
	state := stateOf(r)
	return state != nil && state.parsed
}

// Bind decodes req.body into v with encoding/json, for handlers whose body
// has a fixed shape. Without a body it decodes {}.
func Bind(r *http.Request, v any) error {
	return json.Unmarshal(BodyValue(r).Raw(), v)
}
