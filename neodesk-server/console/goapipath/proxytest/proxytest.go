// Package proxytest replays vectors that were recorded from route files
// of the Node console: requests to the routes, the answers the client
// got, and the calls the API server received meanwhile. The vectors are
// fixed recordings in the testdata folders of console/goapipath and of
// the route groups; they cannot be recorded again.
//
// The package is for tests only.
package proxytest

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/auditlog"
	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
	"github.com/neodeskapp/neodesk/neodesk-server/console/httpx"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
	"github.com/neodeskapp/neodesk/neodesk-server/console/limits"
	"github.com/neodeskapp/neodesk/neodesk-server/console/middleware"
	"github.com/neodeskapp/neodesk/neodesk-server/console/session"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
	"github.com/neodeskapp/neodesk/neodesk-server/console/webui"
)

// Answer is what the API server says to every call during one request.
type Answer struct {
	Status      int    `json:"status"`
	Type        string `json:"type"`
	Body        string `json:"body"`
	Base64      string `json:"base64"`
	Disposition string `json:"disposition"`
	// Hang ends the call without an answer.
	Hang bool `json:"hang"`
}

// Call is one request the API server received.
type Call struct {
	Method string `json:"method"`
	URL    string `json:"url"`
	Type   string `json:"type"`
	Body   string `json:"body"`
}

// Account is the session of a request.
type Account struct {
	UserID int64 `json:"userId"`
	User   struct {
		Username string `json:"username"`
		Role     string `json:"role"`
	} `json:"user"`
}

// Request is what the client sent.
type Request struct {
	Method  string            `json:"method"`
	Path    string            `json:"path"`
	Session *Account          `json:"session"`
	Type    string            `json:"type"`
	Body    *string           `json:"body"`
	Headers map[string]string `json:"headers"`
}

// Vector is one recorded request with its answer.
type Vector struct {
	Request Request `json:"request"`
	// API is the API server's answer; nil is 404 {"error":"not found"}.
	API         *Answer `json:"api"`
	Status      int     `json:"status"`
	Type        string  `json:"type"`
	Body        string  `json:"body"`
	Base64      *string `json:"base64"`
	Disposition string  `json:"disposition"`
	Calls       []Call  `json:"calls"`
}

// Load reads a vector file.
func Load(t testing.TB, file string) []Vector {
	t.Helper()
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("read the vectors: %v", err)
	}
	var vectors []Vector
	if err := json.Unmarshal(data, &vectors); err != nil {
		t.Fatalf("parse %s: %v", file, err)
	}
	if len(vectors) == 0 {
		t.Fatalf("%s holds no vectors", file)
	}
	return vectors
}

// World is a route table with what a thin route group needs around it: a
// scripted API server behind the client, sessions, and the steps of the
// pipeline that read the body and load the session.
type World struct {
	t     *testing.T
	Deps  *kit.Deps
	steps http.Handler

	mu     sync.Mutex
	answer *Answer
	calls  []Call

	cookies map[string]string
}

// NewWorld builds a World and lets register add the routes.
func NewWorld(t *testing.T, register func(d *kit.Deps)) *World {
	t.Helper()
	w := &World{t: t, cookies: map[string]string{}}
	db := storetest.Migrated(t)
	sessions, err := session.New(context.Background(), session.Options{DB: db, Secret: "proxytest-secret-0123456789abcdef0123456789"})
	if err != nil {
		t.Fatalf("sessions: %v", err)
	}
	w.Deps = &kit.Deps{
		Router:   kit.NewRouter(),
		Store:    db,
		Sessions: sessions,
		GoAPI:    goapi.New(http.HandlerFunc(w.api)),
		Audit:    auditlog.New(db),
		Log:      kit.NewLogger("error", true),
		Limits:   limits.NewSet(limits.Options{ClientIP: httpx.SocketIP, UserKey: func(*http.Request) string { return "" }}),
		WebUI:    webui.New(webui.Options{}),
	}
	register(w.Deps)
	w.Deps.Router.Get("/__proxytest/signin", func(rw http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		id, _ := strconv.ParseInt(q.Get("id"), 10, 64)
		s := kit.Session(r)
		if err := s.Regenerate(); err != nil {
			t.Errorf("regenerate: %v", err)
		}
		if _, err := s.Establish(session.User{ID: id, Username: q.Get("username"), Role: q.Get("role")}, session.Meta{}); err != nil {
			t.Errorf("establish: %v", err)
		}
	})
	w.steps = middleware.Bodies(middleware.BodyOptions{})(sessions.Middleware(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if !w.Deps.Router.Serve(rw, kit.Begin(r, httpx.TrustProxy{}, nil)) {
			httpx.WriteBody(rw, r, http.StatusNotFound, "text/plain; charset=utf-8", []byte("no route"))
		}
	})))
	return w
}

// api is the scripted API server.
func (w *World) api(rw http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	w.mu.Lock()
	w.calls = append(w.calls, Call{Method: r.Method, URL: r.RequestURI, Type: r.Header.Get("Content-Type"), Body: string(body)})
	answer := w.answer
	w.mu.Unlock()
	if answer == nil {
		answer = &Answer{Status: http.StatusNotFound, Type: "application/json", Body: `{"error":"not found"}`}
	}
	if answer.Hang {
		panic(http.ErrAbortHandler)
	}
	if answer.Type != "" {
		rw.Header().Set("Content-Type", answer.Type)
	}
	if answer.Disposition != "" {
		rw.Header().Set("Content-Disposition", answer.Disposition)
	}
	rw.WriteHeader(answer.Status)
	if answer.Base64 != "" {
		raw, _ := base64.StdEncoding.DecodeString(answer.Base64)
		_, _ = rw.Write(raw)
		return
	}
	_, _ = io.WriteString(rw, answer.Body)
}

// cookie signs the account in and returns its session cookie. An account
// keeps its session for the whole replay, except a pro account: the
// guards end its session when they refuse it.
func (w *World) cookie(a *Account) string {
	key := strconv.FormatInt(a.UserID, 10) + "|" + a.User.Username + "|" + a.User.Role
	if pair, ok := w.cookies[key]; ok {
		return pair
	}
	q := url.Values{"id": {strconv.FormatInt(a.UserID, 10)}, "username": {a.User.Username}, "role": {a.User.Role}}
	rec := httptest.NewRecorder()
	w.steps.ServeHTTP(rec, httptest.NewRequest("GET", "/__proxytest/signin?"+q.Encode(), nil))
	pair, _, _ := strings.Cut(rec.Header().Get("Set-Cookie"), ";")
	if pair == "" {
		w.t.Fatalf("signing in as %s set no cookie", a.User.Username)
	}
	if a.User.Role != "pro" {
		w.cookies[key] = pair
	}
	return pair
}

// Do sends the request of a vector, with the API server answering as the
// vector says, and returns the answer and the calls the API server got.
func (w *World) Do(req Request, api *Answer) (*httptest.ResponseRecorder, []Call) {
	w.mu.Lock()
	w.answer, w.calls = api, nil
	w.mu.Unlock()

	var body io.Reader
	if req.Body != nil {
		body = strings.NewReader(*req.Body)
	}
	method := req.Method
	if method == "" {
		method = http.MethodGet
	}
	// Characters of the path stand for the bytes the client sent
	target := []byte{}
	for _, c := range req.Path {
		target = append(target, byte(c))
	}
	u, err := url.ParseRequestURI(string(target))
	if err != nil {
		w.t.Fatalf("the path %q of a vector is no request target: %v", req.Path, err)
	}
	r := httptest.NewRequest(method, "/", body)
	r.URL, r.RequestURI = u, string(target)
	if req.Type != "" {
		r.Header.Set("Content-Type", req.Type)
	}
	for name, value := range req.Headers {
		r.Header.Set(name, value)
	}
	if req.Session != nil {
		r.Header.Set("Cookie", w.cookie(req.Session))
	}
	rec := httptest.NewRecorder()
	w.steps.ServeHTTP(rec, r)

	w.mu.Lock()
	defer w.mu.Unlock()
	return rec, append([]Call(nil), w.calls...)
}

// Fix names a vector whose answer a bug fix of BUGS.md changed on
// purpose: its index in the vector file and the bug.
type Fix struct {
	Vector int
	Bug    string
}

// Fixes names the vectors, by their index, that the fix of one bug
// changed.
func Fixes(bug string, vectors ...int) []Fix {
	out := make([]Fix, len(vectors))
	for i, v := range vectors {
		out[i] = Fix{Vector: v, Bug: bug}
	}
	return out
}

// fixedVector is the console's answer to a fixed vector, as the file next
// to the recording holds it. Request repeats the vector's method and path,
// so the file cannot drift from the recording.
type fixedVector struct {
	Vector      int     `json:"vector"`
	Bug         string  `json:"bug"`
	Request     string  `json:"request"`
	Status      int     `json:"status"`
	Type        string  `json:"type"`
	Body        string  `json:"body"`
	Base64      *string `json:"base64,omitempty"`
	Disposition string  `json:"disposition,omitempty"`
	Calls       []Call  `json:"calls"`
}

// fixedFile is the file of the fixed vectors of a recording:
// vectors.json has vectors_fixed.json.
func fixedFile(file string) string {
	return strings.TrimSuffix(file, ".json") + "_fixed.json"
}

func requestText(r Request) string {
	method := r.Method
	if method == "" {
		method = http.MethodGet
	}
	return method + " " + r.Path
}

// Replay sends every vector of the file to the routes register adds and
// compares status, Content-Type, Content-Disposition and body with what
// the Node console answered, and the calls to the API server with the
// calls it made.
//
// The vectors that fixes name are compared with the console's answer in
// the file next to the recording (vectors_fixed.json for vectors.json)
// instead. PROXYTEST_RECORD_FIXED=1 writes that file from the console's
// answers to them, and fails for a listed vector that still answers as
// Node did. Look at the file's diff before a commit: only what the fixes
// change may differ from Node's answer.
func Replay(t *testing.T, file string, register func(d *kit.Deps), fixes ...Fix) {
	t.Helper()
	vectors := Load(t, file)
	recording := os.Getenv("PROXYTEST_RECORD_FIXED") == "1"
	fixed := loadFixed(t, file, vectors, fixes, recording)
	var written []fixedVector
	w := NewWorld(t, register)
	failures := 0
	for i, v := range vectors {
		rec, calls := w.Do(v.Request, v.API)
		got := answerOf(rec, calls, v.Base64 != nil)
		want := fixedVector{Status: v.Status, Type: v.Type, Body: v.Body, Base64: v.Base64, Disposition: v.Disposition, Calls: v.Calls}
		if entry, ok := fixed[i]; ok {
			if recording {
				if len(compare(got, want)) == 0 {
					t.Errorf("vector %d: %s is listed for %s but answers as Node did", i, entry.Request, entry.Bug)
				}
				got.Vector, got.Bug, got.Request = i, entry.Bug, entry.Request
				written = append(written, got)
				continue
			}
			want = entry
		}
		if problems := compare(got, want); len(problems) > 0 {
			failures++
			if failures <= 25 {
				t.Errorf("vector %d: %s (API answers %s):\n  %s", i, requestText(v.Request), describeAnswer(v.API), strings.Join(problems, "\n  "))
			}
		}
	}
	if failures > 25 {
		t.Errorf("%d of %d vectors differ; the first 25 are shown", failures, len(vectors))
	}
	if recording {
		writeFixed(t, file, written)
	}
}

// answerOf is what the console answered, in the form of a fixed vector.
func answerOf(rec *httptest.ResponseRecorder, calls []Call, binary bool) fixedVector {
	got := fixedVector{
		Status:      rec.Code,
		Type:        rec.Header().Get("Content-Type"),
		Disposition: rec.Header().Get("Content-Disposition"),
		Calls:       calls,
	}
	if binary {
		encoded := base64.StdEncoding.EncodeToString(rec.Body.Bytes())
		got.Base64 = &encoded
	} else {
		got.Body = rec.Body.String()
	}
	if got.Calls == nil {
		got.Calls = []Call{}
	}
	return got
}

// compare lists how an answer differs from the one it should be.
func compare(got, want fixedVector) []string {
	var problems []string
	if got.Status != want.Status {
		problems = append(problems, "status "+strconv.Itoa(got.Status)+", want "+strconv.Itoa(want.Status))
	}
	if got.Type != want.Type {
		problems = append(problems, "Content-Type "+strconv.Quote(got.Type)+", want "+strconv.Quote(want.Type))
	}
	if got.Disposition != want.Disposition {
		problems = append(problems, "Content-Disposition "+strconv.Quote(got.Disposition)+", want "+strconv.Quote(want.Disposition))
	}
	switch {
	case want.Base64 != nil:
		if got.Base64 == nil || *got.Base64 != *want.Base64 {
			problems = append(problems, "body (base64) differs, want "+*want.Base64)
		}
	case got.Body != want.Body:
		problems = append(problems, "body "+strconv.Quote(got.Body)+", want "+strconv.Quote(want.Body))
	}
	if len(got.Calls) != len(want.Calls) {
		return append(problems, "API calls "+describe(got.Calls)+", want "+describe(want.Calls))
	}
	for n := range got.Calls {
		if got.Calls[n] != want.Calls[n] {
			problems = append(problems, "API call "+describe(got.Calls[n:n+1])+", want "+describe(want.Calls[n:n+1]))
		}
	}
	return problems
}

// loadFixed checks the list of fixes against the recording and, unless the
// file is being written, reads the answers of the fixed vectors. It stops
// the test when the list and the file name other vectors or bugs.
func loadFixed(t *testing.T, file string, vectors []Vector, fixes []Fix, recording bool) map[int]fixedVector {
	t.Helper()
	listed := map[int]fixedVector{}
	for _, f := range fixes {
		if f.Vector < 0 || f.Vector >= len(vectors) {
			t.Fatalf("the fixed vector %d (%s) is not in %s", f.Vector, f.Bug, file)
		}
		if _, twice := listed[f.Vector]; twice {
			t.Fatalf("the vector %d is listed twice", f.Vector)
		}
		listed[f.Vector] = fixedVector{Vector: f.Vector, Bug: f.Bug, Request: requestText(vectors[f.Vector].Request)}
	}
	if recording {
		return listed
	}
	data, err := os.ReadFile(fixedFile(file))
	if os.IsNotExist(err) && len(fixes) == 0 {
		return listed
	}
	if err != nil {
		t.Fatalf("read the fixed vectors: %v", err)
	}
	var entries []fixedVector
	if err := json.Unmarshal(data, &entries); err != nil {
		t.Fatalf("parse %s: %v", fixedFile(file), err)
	}
	answers := map[int]fixedVector{}
	var problems []string
	for _, entry := range entries {
		want, ok := listed[entry.Vector]
		switch {
		case !ok:
			problems = append(problems, "vector "+strconv.Itoa(entry.Vector)+" is in "+fixedFile(file)+" but not in the list of fixes")
		case entry.Bug != want.Bug || entry.Request != want.Request:
			problems = append(problems, "vector "+strconv.Itoa(entry.Vector)+" is "+want.Request+" of "+want.Bug+", the file has "+entry.Request+" of "+entry.Bug)
		}
		answers[entry.Vector] = entry
	}
	for vector, want := range listed {
		if _, ok := answers[vector]; !ok {
			problems = append(problems, "vector "+strconv.Itoa(vector)+" ("+want.Bug+") has no answer in "+fixedFile(file))
		}
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		t.Fatalf("%s", strings.Join(problems, "\n"))
	}
	return answers
}

// writeFixed writes the answers to the fixed vectors, or removes the file
// when there are none.
func writeFixed(t *testing.T, file string, entries []fixedVector) {
	t.Helper()
	if len(entries) == 0 {
		if err := os.Remove(fixedFile(file)); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		return
	}
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", " ")
	if err := enc.Encode(entries); err != nil {
		t.Fatalf("encode the fixed vectors: %v", err)
	}
	if err := os.WriteFile(fixedFile(file), out.Bytes(), 0o644); err != nil {
		t.Fatalf("write the fixed vectors: %v", err)
	}
	t.Logf("wrote %d fixed vectors to %s", len(entries), fixedFile(file))
}

func describe(calls []Call) string {
	text, _ := json.Marshal(calls)
	return string(text)
}

func describeAnswer(a *Answer) string {
	if a == nil {
		return "404 not found"
	}
	text, _ := json.Marshal(a)
	return string(text)
}
