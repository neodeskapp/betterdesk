package middleware

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/console/httpx"
)

// The vectors in testdata were recorded by
// .ui-rework/go/tools/vectors/middleware.js from the Node console's own
// middleware (helmet 7.2.0, body-parser 1.20.9, multer 2.5.0, serve-static
// 1.16.3), mounted in the order of server.js. Each test replays the same
// bytes against the Go pipeline over a socket.

type vectorError struct {
	Status  int    `json:"status"`
	Type    string `json:"type"`
	Message string `json:"message"`
	Code    string `json:"code"`
	Field   string `json:"field"`
}

type vectorAnswer struct {
	Status  int         `json:"status"`
	Headers [][2]string `json:"headers"`
	Body    string      `json:"body"`
}

type vector struct {
	ID      string      `json:"id"`
	Name    string      `json:"name"`
	Method  string      `json:"method"`
	Path    string      `json:"path"`
	Headers [][2]string `json:"headers"`
	Body    *string     `json:"body"`
	Chunks  []string    `json:"chunks"`
	Gen     *struct {
		Prefix string `json:"prefix"`
		Fill   string `json:"fill"`
		Count  int    `json:"count"`
		Suffix string `json:"suffix"`
	} `json:"gen"`
	ChunkSize  int           `json:"chunkSize"`
	AbortAfter *int          `json:"abortAfter"`
	Answer     *vectorAnswer `json:"answer"`
	Error      *vectorError  `json:"error"`
}

type vectorFile struct {
	Mode     string            `json:"mode"`
	Env      map[string]string `json:"env"`
	Fixtures []struct {
		Path    string `json:"path"`
		Content string `json:"content"`
		MtimeNs string `json:"mtimeNs"`
	} `json:"fixtures"`
	Vectors []vector `json:"vectors"`
}

func loadVectors(t testing.TB, mode string) vectorFile {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "vectors-"+mode+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var file vectorFile
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	return file
}

// requestBytes builds the request exactly as the recording script sent it.
func (v vector) requestBytes(t testing.TB) []byte {
	var body []byte
	var chunks [][]byte
	hasBody := false
	switch {
	case v.Gen != nil:
		body = append([]byte(v.Gen.Prefix), bytes.Repeat([]byte(v.Gen.Fill), v.Gen.Count)...)
		body = append(body, v.Gen.Suffix...)
		hasBody = true
		if v.ChunkSize > 0 {
			for i := 0; i < len(body); i += v.ChunkSize {
				chunks = append(chunks, body[i:min(i+v.ChunkSize, len(body))])
			}
			hasBody = false
		}
	case v.Chunks != nil:
		chunks = [][]byte{}
		for _, c := range v.Chunks {
			decoded, err := base64.StdEncoding.DecodeString(c)
			if err != nil {
				t.Fatal(err)
			}
			chunks = append(chunks, decoded)
		}
	case v.Body != nil:
		decoded, err := base64.StdEncoding.DecodeString(*v.Body)
		if err != nil {
			t.Fatal(err)
		}
		body, hasBody = decoded, true
	}
	var b bytes.Buffer
	fmt.Fprintf(&b, "%s %s HTTP/1.1\r\nHost: vectors.test\r\n", v.Method, v.Path)
	hasLength := false
	for _, h := range v.Headers {
		fmt.Fprintf(&b, "%s: %s\r\n", h[0], h[1])
		if strings.EqualFold(h[0], "Content-Length") {
			hasLength = true
		}
	}
	switch {
	case chunks != nil:
		b.WriteString("Transfer-Encoding: chunked\r\nConnection: close\r\n\r\n")
		for _, c := range chunks {
			if len(c) == 0 {
				continue
			}
			fmt.Fprintf(&b, "%x\r\n", len(c))
			b.Write(c)
			b.WriteString("\r\n")
		}
		b.WriteString("0\r\n\r\n")
	case hasBody:
		if !hasLength {
			fmt.Fprintf(&b, "Content-Length: %d\r\n", len(body))
		}
		b.WriteString("Connection: close\r\n\r\n")
		b.Write(body)
	default:
		b.WriteString("Connection: close\r\n\r\n")
	}
	return b.Bytes()
}

// exchange sends raw bytes and reads the whole answer.
func exchange(t testing.TB, addr string, raw []byte, method string, abortAfter *int) (*http.Response, []byte) {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if abortAfter != nil {
		_, _ = conn.Write(raw[:min(*abortAfter, len(raw))])
		time.Sleep(30 * time.Millisecond)
		return nil, nil
	}
	go func() { _, _ = conn.Write(raw) }()
	_ = conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	resp, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: method})
	if err != nil {
		t.Fatalf("read answer: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read answer body: %v", err)
	}
	return resp, body
}

// pipeline is the Go side of the recording app.
type pipeline struct {
	t          testing.TB
	production bool
	uploadTmp  string
	mu         sync.Mutex
	errs       map[string]vectorError
}

func (p *pipeline) record(r *http.Request, status int, err error) {
	rec := vectorError{Status: status, Message: err.Error()}
	var be *BodyError
	if errors.As(err, &be) {
		rec.Type = be.Type
	}
	var ue *UploadError
	if errors.As(err, &ue) {
		rec.Code, rec.Field = ue.Code, ue.Field
	}
	p.mu.Lock()
	p.errs[r.Header.Get("X-Vec-Id")] = rec
	p.mu.Unlock()
}

func (p *pipeline) recorded(id string) (vectorError, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	rec, ok := p.errs[id]
	return rec, ok
}

type failure struct {
	Success bool   `json:"success"`
	Error   string `json:"error"`
}

func sendText(w http.ResponseWriter, r *http.Request, status int, body string) {
	h := w.Header()
	h.Set("Content-Type", "text/plain; charset=utf-8")
	h.Set("Content-Length", strconv.Itoa(len(body)))
	if h.Get("ETag") == "" {
		h.Set("ETag", httpx.ETag([]byte(body)))
	}
	w.WriteHeader(status)
	if r.Method != http.MethodHead {
		_, _ = io.WriteString(w, body)
	}
}

// onError is the console's error handler (lib/mountRoutes.js) as far as
// these tests need it: JSON for the API and for JSON requests, text for
// the rest, and no detail in production.
func (p *pipeline) onError(w http.ResponseWriter, r *http.Request, status int, err error) {
	p.record(r, status, err)
	detail := "Internal Server Error"
	if !p.production && err.Error() != "" {
		detail = err.Error()
	}
	wantsJSON := strings.HasPrefix(httpx.RawPath(r), "/api/") || strings.Contains(r.Header.Get("Content-Type"), "application/json")
	accept := r.Header.Get("Accept")
	acceptsHTML := accept == "" || strings.Contains(accept, "text/html") || strings.Contains(accept, "*/*")
	if wantsJSON || !acceptsHTML {
		httpx.WriteJSON(w, r, status, failure{Error: detail})
		return
	}
	sendText(w, r, status, detail)
}

var kindNames = map[Kind]string{KindNull: "null", KindBool: "boolean", KindNumber: "number", KindString: "string", KindArray: "array", KindObject: "object"}

// described is what the recording app says about one member of a body.
type described struct {
	T      string `json:"t"`
	Truthy bool   `json:"truthy"`
	Str    string `json:"str"`
	Int    int64  `json:"int"`
	Bool0  bool   `json:"bool0"`
	Bool1  bool   `json:"bool1"`
}

func describeValue(v Value) described {
	return described{T: kindNames[v.Kind()], Truthy: v.Truthy(), Str: v.Text("<fallback>"), Int: v.Int(-7), Bool0: v.Flag(false), Bool1: v.Flag(true)}
}

type echoed struct {
	Method  string          `json:"method"`
	Path    string          `json:"path"`
	IsArray bool            `json:"isArray"`
	Keys    []string        `json:"keys"`
	Info    [][2]any        `json:"info"`
	Body    json.RawMessage `json:"body"`
}

func (p *pipeline) echo(w http.ResponseWriter, r *http.Request) {
	body := BodyValue(r)
	obj := Body(r)
	out := echoed{Method: r.Method, Path: httpx.RawPath(r), IsArray: body.Kind() == KindArray, Info: [][2]any{}, Body: body.Raw()}
	if body.Kind() == KindObject {
		out.Keys = append([]string{}, obj.Keys()...)
		for _, key := range obj.Keys() {
			if !obj.Has(key) {
				p.t.Errorf("Has(%q) is false for a listed key", key)
			}
			out.Info = append(out.Info, [2]any{key, describeValue(obj.Get(key))})
		}
	}
	httpx.WriteJSON(w, r, http.StatusOK, out)
}

func (p *pipeline) echoSize(w http.ResponseWriter, r *http.Request) {
	out := struct {
		Size int      `json:"size"`
		Keys []string `json:"keys"`
	}{Size: -1, Keys: []string{}}
	if data, ok := Body(r).String("data"); ok {
		out.Size = len(data)
	}
	if items, ok := BodyValue(r).Array(); ok {
		for i := range items {
			out.Keys = append(out.Keys, strconv.Itoa(i))
		}
	} else {
		out.Keys = append(out.Keys, Body(r).Keys()...)
	}
	httpx.WriteJSON(w, r, http.StatusOK, out)
}

func (p *pipeline) leftovers() []string {
	entries, _ := os.ReadDir(p.uploadTmp)
	names := []string{}
	for _, e := range entries {
		names = append(names, e.Name())
		_ = os.Remove(filepath.Join(p.uploadTmp, e.Name()))
	}
	sort.Strings(names)
	return names
}

func (p *pipeline) uploadAnswer(w http.ResponseWriter, r *http.Request, up *Upload) {
	var file any
	if f := up.File; f != nil {
		content := f.Buffer
		if f.Path != "" {
			content, _ = os.ReadFile(f.Path)
		}
		sum := sha256.Sum256(content)
		info := map[string]any{"fieldname": f.FieldName, "originalname": f.OriginalName, "mimetype": f.MIMEType, "size": f.Size,
			"sha256": hex.EncodeToString(sum[:]), "filename": nil, "stored": f.Path != ""}
		if f.Filename != "" {
			info["filename"] = f.Filename
		}
		file = info
	}
	// A long field is echoed as its length, as the recording app does
	fields := map[string]any{}
	for _, key := range up.Fields.Keys() {
		fields[key] = up.Fields.Raw(key)
		if s, ok := up.Fields.String(key); ok && len(s) > 1000 {
			fields[key] = fmt.Sprintf("<%d chars>", len(s))
		}
	}
	httpx.WriteJSON(w, r, http.StatusOK, map[string]any{"file": file, "body": fields, "left": p.leftovers()})
}

var imageTypes = regexp.MustCompile(`^image/(png|jpeg|gif|webp|svg\+xml)$`)

func (p *pipeline) upload(opts UploadOptions) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		up, err := ParseUpload(r, opts)
		if err != nil {
			p.onError(w, r, http.StatusInternalServerError, err)
			return
		}
		p.uploadAnswer(w, r, up)
	}
}

// uploadDisk is a route that answers upload errors itself, as the
// settings routes do.
func (p *pipeline) uploadDisk(w http.ResponseWriter, r *http.Request) {
	up, err := ParseUpload(r, UploadOptions{
		Field: "logo", MaxFileSize: 2048, Dir: p.uploadTmp,
		Filename: func(f *UploadedFile) (string, error) {
			ext := strings.ToLower(filepath.Ext(f.OriginalName))
			if ext == "" {
				ext = ".bin"
			}
			return "up" + ext, nil
		},
		Filter: func(f *UploadedFile) (bool, error) {
			if imageTypes.MatchString(f.MIMEType) {
				return true, nil
			}
			return false, errors.New("Invalid file type")
		},
	})
	if err != nil {
		msg := err.Error()
		if UploadErrorCode(err) == UploadFileTooLarge {
			msg = "File too large (max 2 KB)"
		}
		field := ""
		var ue *UploadError
		if errors.As(err, &ue) {
			field = ue.Field
		}
		httpx.WriteJSON(w, r, http.StatusBadRequest, map[string]any{"success": false, "error": msg, "code": UploadErrorCode(err), "field": field, "left": p.leftovers()})
		return
	}
	p.uploadAnswer(w, r, up)
}

var attachmentRoute = regexp.MustCompile(`^/api/tickets/[0-9]+/attachments$`)

// routes is the recording app's router: Express matches in any letter
// case and with or without one trailing slash.
func (p *pipeline) routes() http.Handler {
	pad := func(letter string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			httpx.WriteJSON(w, r, http.StatusOK, struct {
				Pad string `json:"pad"`
			}{strings.Repeat(letter, 2000)})
		}
	}
	attachment := AttachmentBody(p.onError)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		out := struct {
			Parsed bool `json:"parsed"`
			Size   int  `json:"size"`
		}{Size: -1}
		if data, ok := Body(r).String("data"); ok {
			out.Parsed, out.Size = true, len(data)
		}
		httpx.WriteJSON(w, r, http.StatusOK, out)
	}))
	memory := p.upload(UploadOptions{Field: "file", MaxFileSize: 1024})
	jsonOnly := p.upload(UploadOptions{Field: "file", MaxFileSize: 1024, Filter: func(f *UploadedFile) (bool, error) {
		if f.MIMEType == "application/json" {
			return true, nil
		}
		return false, errors.New("Only JSON files allowed")
	}})
	skipText := p.upload(UploadOptions{Field: "file", MaxFileSize: 1024, Filter: func(f *UploadedFile) (bool, error) {
		return f.MIMEType != "text/plain", nil
	}})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := httpx.RawPath(r)
		if _, ok := decodeURIComponent(raw); !ok {
			p.onError(w, r, http.StatusBadRequest, errors.New("Failed to decode param '"+raw+"'"))
			return
		}
		route := strings.ToLower(raw)
		if len(route) > 1 {
			route = strings.TrimSuffix(route, "/")
		}
		get := r.Method == http.MethodGet || r.Method == http.MethodHead
		post := r.Method == http.MethodPost
		switch {
		case post && attachmentRoute.MatchString(route):
			attachment.ServeHTTP(w, r)
		case route == "/api/echo-size" || route == "/echo-size":
			p.echoSize(w, r)
		case get && (route == "/api/big" || route == "/big"):
			pad("x")(w, r)
		case route == "/api/big":
			pad("y")(w, r)
		case post && route == "/up/mem":
			memory(w, r)
		case post && route == "/up/json":
			jsonOnly(w, r)
		case post && route == "/up/skip":
			skipText(w, r)
		case post && route == "/up/disk":
			p.uploadDisk(w, r)
		default:
			p.echo(w, r)
		}
	})
}

func startPipeline(t testing.TB, file vectorFile) (*pipeline, *httptest.Server) {
	t.Helper()
	site := t.TempDir()
	for _, f := range file.Fixtures {
		full := filepath.Join(site, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		content, err := base64.StdEncoding.DecodeString(f.Content)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, content, 0o644); err != nil {
			t.Fatal(err)
		}
		ns, _ := strconv.ParseInt(f.MtimeNs, 10, 64)
		if err := os.Chtimes(full, time.Unix(0, ns), time.Unix(0, ns)); err != nil {
			t.Fatal(err)
		}
	}
	p := &pipeline{t: t, production: file.Env["NODE_ENV"] == "production", uploadTmp: filepath.Join(site, "upload-tmp"), errs: map[string]vectorError{}}
	if err := os.MkdirAll(p.uploadTmp, 0o755); err != nil {
		t.Fatal(err)
	}
	https := file.Env["HTTPS_ENABLED"] == "true"
	handler := RequireJSON()(p.routes())
	handler = MarkAPICompression(nil)(handler)
	handler = NoStoreForNonFiles()(handler)
	handler = StaticFolders(StaticFoldersOptions{
		PublicDir: filepath.Join(site, "public"), ProtosDir: filepath.Join(site, "protos"),
		UploadsDir: filepath.Join(site, "data", "uploads"), WallpapersDir: filepath.Join(site, "wallpapers"),
		Production: p.production, OnError: p.onError,
	})(handler)
	handler = Bodies(BodyOptions{OnError: p.onError})(handler)
	handler = CORS()(handler)
	handler = SecurityHeaders(SecurityOptions{HTTPS: https, HSTS: HSTSEnabled(https, file.Env["HSTS_ENABLED"], file.Env["ALLOW_SELF_SIGNED_CERTS"] == "true")})(handler)
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return p, server
}

func headerMap(pairs [][2]string) map[string]string {
	out := map[string]string{}
	for _, h := range pairs {
		name := strings.ToLower(h[0])
		switch name {
		case "date", "connection", "keep-alive", "transfer-encoding":
			continue
		}
		if prev, ok := out[name]; ok {
			out[name] = prev + ", " + h[1]
		} else {
			out[name] = h[1]
		}
	}
	return out
}

func responseHeaders(resp *http.Response) map[string]string {
	var pairs [][2]string
	for name, values := range resp.Header {
		for _, v := range values {
			pairs = append(pairs, [2]string{name, v})
		}
	}
	return headerMap(pairs)
}

func diffHeaders(want, got map[string]string, skip ...string) string {
	skipped := map[string]bool{}
	for _, s := range skip {
		skipped[s] = true
	}
	var lines []string
	for name, w := range want {
		if skipped[name] {
			continue
		}
		if g, ok := got[name]; !ok {
			lines = append(lines, fmt.Sprintf("missing %s: %s", name, w))
		} else if g != w {
			lines = append(lines, fmt.Sprintf("%s: got %q want %q", name, g, w))
		}
	}
	for name, g := range got {
		if _, ok := want[name]; !ok && !skipped[name] {
			lines = append(lines, fmt.Sprintf("extra %s: %s", name, g))
		}
	}
	sort.Strings(lines)
	return strings.Join(lines, "; ")
}

func decodeJSON(raw []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	return v, nil
}

// sameJS compares what Node echoed with what Go echoed. Node writes
// numbers the way JavaScript holds them (a float64, and null when it is
// infinite); Go echoes the digits the client sent.
func sameJS(node, got any) bool {
	switch n := node.(type) {
	case nil:
		if g, ok := got.(json.Number); ok {
			f, _ := g.Float64()
			return math.IsInf(f, 0)
		}
		return got == nil
	case json.Number:
		g, ok := got.(json.Number)
		if !ok {
			return false
		}
		nf, _ := n.Float64()
		gf, _ := g.Float64()
		return nf == gf
	case []any:
		g, ok := got.([]any)
		if !ok || len(g) != len(n) {
			return false
		}
		for i := range n {
			if !sameJS(n[i], g[i]) {
				return false
			}
		}
		return true
	case map[string]any:
		g, ok := got.(map[string]any)
		if !ok || len(g) != len(n) {
			return false
		}
		for k, v := range n {
			gv, has := g[k]
			if !has || !sameJS(v, gv) {
				return false
			}
		}
		return true
	}
	return reflect.DeepEqual(node, got)
}

func gunzip(t testing.TB, b []byte) []byte {
	zr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("not gzip: %v", err)
	}
	out, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("gunzip: %v", err)
	}
	return out
}

// difference is a vector where Go differs from Node, on purpose or by
// what its HTTP server does before any middleware runs, with what Go
// answers instead.
type difference struct {
	reason string
	status int
	// body is a text Go's answer contains
	body string
}

func knownDifference(v vector) (difference, bool) {
	switch {
	case strings.Contains(v.Path, "%zz") || strings.HasSuffix(v.Path, "%E0%A4%A"):
		return difference{"Go's HTTP server refuses a path with a broken % escape with its own 400", 400, "400 Bad Request"}, true
	case strings.HasPrefix(v.Name, "json valid 22"):
		return difference{"encoding/json stops at 10000 levels of nesting, V8 has no such limit", 400, "Internal Server Error"}, true
	case strings.Contains(v.Name, "utf-7"):
		return difference{"UTF-7 bodies are not supported: 415 instead of being parsed", 415, ""}, true
	case strings.Contains(v.Name, "body in file crlf"):
		return difference{"a boundary followed by other text ends the part in busboy; Go keeps reading the file", 200, "\"size\":18"}, true
	case strings.HasPrefix(v.Name, "rule ") && strings.HasPrefix(v.Path, "/API/"):
		return difference{"M2: the JSON-only rule takes /API/ as /api/, as the router does", 415, "Content-Type must be application/json"}, true
	case strings.HasPrefix(v.Name, "rule ") && v.Path == "/api/Upload":
		return difference{"M2: the JSON-only rule takes /Upload as /upload, as the router does", 200, `"path":"/api/Upload"`}, true
	}
	return difference{}, false
}

// clientFileError reports a vector where Node passed a file's failed
// precondition (412) or range (416) to its error handler (M6).
func clientFileError(v vector) bool {
	return strings.HasPrefix(v.Name, "static ") && v.Error != nil &&
		(v.Error.Status == http.StatusPreconditionFailed || v.Error.Status == http.StatusRequestedRangeNotSatisfiable)
}

// fileCacheControl is the Cache-Control of an answer of the static
// folders, where Node sent the security headers' rule (S15): the max-age
// of the folder that holds the file. production is NODE_ENV=production.
func fileCacheControl(path string, production bool) string {
	week, month := "604800", "2592000"
	if !production {
		week, month = "0", "0"
	}
	if _, _, ok := httpx.StripMount(path, "/wallpapers"); ok {
		return "public, max-age=" + month + ", immutable"
	}
	if _, _, ok := httpx.StripMount(path, "/uploads"); ok {
		return "public, max-age=" + month
	}
	return "public, max-age=" + week
}

// bugFix matches the reason of a difference a bug fix makes: it starts
// with the bug's id from BUGS.md.
var bugFix = regexp.MustCompile(`^[A-Z]+[0-9]+: `)

// messageTypes are the error types whose text is the same in both
// servers. Syntax errors of JSON and of compressed data are worded by the
// runtime.
var messageTypes = map[string]bool{ErrTypeTooLarge: true, ErrTypeCharsetUnsupported: true, ErrTypeEncodingUnsupported: true, ErrTypeTooManyParameters: true, ErrTypeAborted: true}

func replay(t *testing.T, mode string, minimum int) {
	file := loadVectors(t, mode)
	if len(file.Vectors) < minimum {
		t.Fatalf("%d vectors, expected at least %d", len(file.Vectors), minimum)
	}
	p, server := startPipeline(t, file)
	addr := server.Listener.Addr().String()
	skipped := map[string]int{}
	aborts, fileErrors, fixedHeaders := 0, 0, 0
	for _, v := range file.Vectors {
		name := v.ID + " " + v.Name
		resp, body := exchange(t, addr, v.requestBytes(t), v.Method, v.AbortAfter)
		if d, known := knownDifference(v); known {
			// Not Node's answer, but the one this port is known to give
			if resp.StatusCode != d.status || !strings.Contains(string(body), d.body) {
				t.Errorf("%s: %d %.200q, want %d with %q (%s)", name, resp.StatusCode, body, d.status, d.body, d.reason)
			}
			skipped[d.reason]++
			continue
		}

		if v.AbortAfter != nil {
			// The client is gone; what counts is how the server saw it
			deadline := time.Now().Add(3 * time.Second)
			for time.Now().Before(deadline) {
				if _, ok := p.recorded(v.ID); ok {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			got, ok := p.recorded(v.ID)
			if v.Error != nil && (!ok || got.Status != v.Error.Status || got.Type != v.Error.Type) {
				t.Errorf("%s: error %+v (seen %v), want %+v", name, got, ok, *v.Error)
			}
			aborts++
			continue
		}
		if v.Answer == nil {
			t.Errorf("%s: no recorded answer", name)
			continue
		}

		want := *v.Answer
		wantBody, _ := base64.StdEncoding.DecodeString(want.Body)
		if resp.StatusCode != want.Status {
			t.Errorf("%s: status %d, want %d (body %.200q)", name, resp.StatusCode, want.Status, body)
			continue
		}
		wantHeaders, gotHeaders := headerMap(want.Headers), responseHeaders(resp)
		if clientFileError(v) {
			// M6: the static step answers these itself, without a body
			v.Error, wantBody = nil, nil
			delete(wantHeaders, "content-type")
			wantHeaders["content-length"] = "0"
			fileErrors++
		}
		if _, file := wantHeaders["last-modified"]; file && strings.HasPrefix(v.Name, "static ") {
			wantHeaders["cache-control"] = fileCacheControl(v.Path, p.production)
			fixedHeaders++
		}
		var skipHeaders []string
		if _, has := wantHeaders["content-length"]; !has {
			// Node streamed the answer; whether Go knows the length is its server's business
			skipHeaders = append(skipHeaders, "content-length")
		}

		got, hasErr := p.recorded(v.ID)
		// zlib's error codes are Node's own; Go names no code for them
		wantCode := ""
		if v.Error != nil && !strings.HasPrefix(v.Error.Code, "Z_") {
			wantCode = v.Error.Code
		}
		runtimeWorded := v.Error != nil && !messageTypes[v.Error.Type] && wantCode == "" && (v.Error.Type != "" || v.Error.Status == 400)
		switch {
		case v.Error == nil && hasErr:
			t.Errorf("%s: OnError called with %+v, Node had no error", name, got)
		case v.Error != nil && !hasErr:
			t.Errorf("%s: OnError not called, Node had %+v", name, *v.Error)
		case v.Error != nil:
			if got.Status != v.Error.Status || got.Type != v.Error.Type || got.Code != wantCode || got.Field != v.Error.Field {
				t.Errorf("%s: error %+v, want %+v", name, got, *v.Error)
			}
			if !runtimeWorded && got.Message != v.Error.Message {
				t.Errorf("%s: error message %q, want %q", name, got.Message, v.Error.Message)
			}
		}

		switch {
		case v.Method == http.MethodHead:
			if len(body) != 0 {
				t.Errorf("%s: a body of %d bytes for HEAD", name, len(body))
			}
		case wantHeaders["content-encoding"] == "gzip" && gotHeaders["content-encoding"] == "gzip":
			if !bytes.Equal(gunzip(t, body), gunzip(t, wantBody)) {
				t.Errorf("%s: compressed bodies differ", name)
			}
		case bytes.Equal(body, wantBody):
		case bytes.HasPrefix(wantBody, []byte(`{"method":`)) || bytes.HasPrefix(wantBody, []byte(`{"file":`)) || bytes.Contains(wantBody, []byte(`,"left":[`)):
			// Echo answers: the same value, written by two JSON encoders
			nodeValue, err1 := decodeJSON(wantBody)
			goValue, err2 := decodeJSON(body)
			if err1 != nil || err2 != nil || !sameJS(nodeValue, goValue) {
				t.Errorf("%s: body\n got  %.600s\n want %.600s", name, body, wantBody)
			}
			skipHeaders = append(skipHeaders, "content-length", "etag")
		case runtimeWorded && !p.production:
			// Outside production the answer carries the runtime's own words
			skipHeaders = append(skipHeaders, "content-length", "etag")
		default:
			t.Errorf("%s: body\n got  %.300q\n want %.300q", name, body, wantBody)
		}
		if d := diffHeaders(wantHeaders, gotHeaders, skipHeaders...); d != "" {
			t.Errorf("%s: headers: %s", name, d)
		}
	}
	for reason, n := range skipped {
		t.Logf("%d vectors not compared: %s", n, reason)
	}
	t.Logf("%d failed preconditions and ranges of files compared as the static step's own answer (M6)", fileErrors)
	t.Logf("%d file answers compared with the folder's Cache-Control in place of Node's (S15)", fixedHeaders)
	// A difference a bug fix makes on purpose names its bug first; those are
	// counted apart from what Go cannot answer as Node did
	total, fixed := 0, 0
	for reason, n := range skipped {
		if bugFix.MatchString(reason) {
			fixed += n
		} else {
			total += n
		}
	}
	if mode == "http" && aborts < 2 {
		t.Errorf("%d aborted requests replayed, expected the recorded ones", aborts)
	}
	if total > 12 {
		t.Errorf("%d vectors skipped as known differences; that list should stay short", total)
	}
	if fixed > 24 {
		t.Errorf("%d vectors skipped as differences of bug fixes; a fix that changes this many needs its own check", fixed)
	}
}

func TestAgainstNodeHTTP(t *testing.T)                  { replay(t, "http", 800) }
func TestAgainstNodeHTTPDevelopment(t *testing.T)       { replay(t, "http-dev", 150) }
func TestAgainstNodeHTTPS(t *testing.T)                 { replay(t, "https", 60) }
func TestAgainstNodeHTTPSSelfSigned(t *testing.T)       { replay(t, "https-selfsigned", 60) }
func TestAgainstNodeHTTPSHSTSOff(t *testing.T)          { replay(t, "https-hsts-off", 60) }
func TestAgainstNodeHTTPSSelfSignedHSTSOn(t *testing.T) { replay(t, "https-selfsigned-hsts-on", 60) }
