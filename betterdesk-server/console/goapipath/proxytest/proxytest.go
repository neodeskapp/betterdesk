// Package proxytest replays vectors that were recorded from route files
// of the Node console: requests to the routes, the answers the client
// got, and the calls the API server received meanwhile. The scripts that
// write the vectors are in console/goapipath/testdata/nodeoracle
// (harness.js) and in the route groups' own testdata folders.
//
// The package is for tests only.
package proxytest

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/unitronix/betterdesk-server/console/auditlog"
	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/httpx"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/limits"
	"github.com/unitronix/betterdesk-server/console/middleware"
	"github.com/unitronix/betterdesk-server/console/session"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
	"github.com/unitronix/betterdesk-server/console/webui"
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

// Replay sends every vector of the file to the routes register adds and
// compares status, Content-Type, Content-Disposition and body with what
// the Node console answered, and the calls to the API server with the
// calls it made.
func Replay(t *testing.T, file string, register func(d *kit.Deps)) {
	t.Helper()
	vectors := Load(t, file)
	w := NewWorld(t, register)
	failures := 0
	for i, v := range vectors {
		rec, calls := w.Do(v.Request, v.API)
		var problems []string
		if rec.Code != v.Status {
			problems = append(problems, "status "+strconv.Itoa(rec.Code)+", want "+strconv.Itoa(v.Status))
		}
		if got := rec.Header().Get("Content-Type"); got != v.Type {
			problems = append(problems, "Content-Type "+strconv.Quote(got)+", want "+strconv.Quote(v.Type))
		}
		if got := rec.Header().Get("Content-Disposition"); got != v.Disposition {
			problems = append(problems, "Content-Disposition "+strconv.Quote(got)+", want "+strconv.Quote(v.Disposition))
		}
		if v.Base64 != nil {
			if got := base64.StdEncoding.EncodeToString(rec.Body.Bytes()); got != *v.Base64 {
				problems = append(problems, "body (base64) "+got+", want "+*v.Base64)
			}
		} else if got := rec.Body.String(); got != v.Body {
			problems = append(problems, "body "+strconv.Quote(got)+", want "+strconv.Quote(v.Body))
		}
		if len(calls) != len(v.Calls) {
			problems = append(problems, "API calls "+describe(calls)+", want "+describe(v.Calls))
		} else {
			for n := range calls {
				if calls[n] != v.Calls[n] {
					problems = append(problems, "API call "+describe(calls[n:n+1])+", want "+describe(v.Calls[n:n+1]))
				}
			}
		}
		if len(problems) > 0 {
			failures++
			if failures <= 25 {
				t.Errorf("vector %d: %s %s (API answers %s):\n  %s", i, v.Request.Method, v.Request.Path, describeAnswer(v.API), strings.Join(problems, "\n  "))
			}
		}
	}
	if failures > 25 {
		t.Errorf("%d of %d vectors differ; the first 25 are shown", failures, len(vectors))
	}
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
