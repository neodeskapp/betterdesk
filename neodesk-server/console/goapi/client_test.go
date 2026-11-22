package goapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/api"
)

func TestRequestCarriesWhatTheAPIServerSaw(t *testing.T) {
	var got *http.Request
	var gotBody string
	var gotIdentity api.Identity
	var hasIdentity bool
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		got, gotBody = r, string(body)
		gotIdentity, hasIdentity = api.IdentityFromContext(r.Context())
		io.WriteString(w, `{}`)
	})
	c := New(handler, WithHost("10.1.2.3:21121"))
	_, err := c.Post(context.Background(), "/org/o 1/users?x=1", Query("limit", 5), Query("skip", nil), Query("tag", []any{"a", "b"}),
		Body(map[string]any{"name": "n"}), Header("X-Extra", "1"))
	if err != nil {
		t.Fatalf("Post: %v", err)
	}

	if got.Method != "POST" || got.RequestURI != "/api/org/o%201/users?x=1&limit=5&tag%5B%5D=a&tag%5B%5D=b" {
		t.Errorf("request line = %s %s", got.Method, got.RequestURI)
	}
	if got.URL.Path != "/api/org/o 1/users" || got.URL.Query().Get("limit") != "5" {
		t.Errorf("URL = %#v", got.URL)
	}
	if got.Host != "10.1.2.3:21121" {
		t.Errorf("Host = %q, want the configured host", got.Host)
	}
	if got.RemoteAddr != "127.0.0.1:0" {
		t.Errorf("RemoteAddr = %q, want loopback", got.RemoteAddr)
	}
	if !hasIdentity || gotIdentity != (api.Identity{Username: "apikey:console-bridge", Role: "admin"}) {
		t.Errorf("identity = %+v (present %v), want the console's API key", gotIdentity, hasIdentity)
	}
	if gotBody != `{"name":"n"}` || got.ContentLength != int64(len(gotBody)) || got.Header.Get("Content-Length") != "12" {
		t.Errorf("body = %s, ContentLength %d, header %q", gotBody, got.ContentLength, got.Header.Get("Content-Length"))
	}
	if got.Header.Get("Content-Type") != "application/json" || got.Header.Get("X-Extra") != "1" {
		t.Errorf("headers = %v", got.Header)
	}
	if got.Header.Get("X-API-Key") != "" || got.Header.Get("Authorization") != "" {
		t.Errorf("a credential header was sent: %v", got.Header)
	}

	// A GET has the JSON content type too, and no body.
	if _, err := c.Get(context.Background(), "/peers"); err != nil {
		t.Fatal(err)
	}
	if got.Header.Get("Content-Type") != "application/json" || gotBody != "" || got.ContentLength != 0 {
		t.Errorf("GET: Content-Type %q, body %q, ContentLength %d", got.Header.Get("Content-Type"), gotBody, got.ContentLength)
	}
}

// TestAsActsAsAnotherIdentityForOneCall checks the As option: the call
// carries the identity it names, a redirect it follows too, and the calls
// before and after it carry the console's API key.
func TestAsActsAsAnotherIdentityForOneCall(t *testing.T) {
	var seen []api.Identity
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, _ := api.IdentityFromContext(r.Context())
		seen = append(seen, id)
		if r.URL.Path == "/api/old" {
			http.Redirect(w, r, "/api/new", http.StatusFound)
			return
		}
		io.WriteString(w, `{}`)
	})
	c := New(handler)
	operator := api.Identity{Username: "e2e_operator", Role: "operator"}
	ctx := context.Background()
	for _, call := range []func() error{
		func() error { _, err := c.Get(ctx, "/peers"); return err },
		func() error {
			_, err := c.Put(ctx, "/panel/device-groups/g1/peers", Body(map[string]any{}), As(operator))
			return err
		},
		func() error { _, err := c.Get(ctx, "/old", As(operator)); return err },
		func() error { _, err := c.Get(ctx, "/peers"); return err },
	} {
		if err := call(); err != nil {
			t.Fatal(err)
		}
	}
	bridge := api.Identity{Username: BridgeUsername, Role: BridgeRole}
	want := []api.Identity{bridge, operator, operator, operator, bridge}
	if len(seen) != len(want) {
		t.Fatalf("the handler ran %d times: %+v", len(seen), seen)
	}
	for i := range want {
		if seen[i] != want[i] {
			t.Errorf("call %d acted as %+v, want %+v", i, seen[i], want[i])
		}
	}
}

func TestClientDefaultsAndOptions(t *testing.T) {
	var got *http.Request
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { got = r })
	if _, err := New(handler).Get(context.Background(), "/health"); err != nil {
		t.Fatal(err)
	}
	if got.Host != "127.0.0.1:21114" || got.RemoteAddr != "127.0.0.1:0" {
		t.Errorf("defaults: Host %q, RemoteAddr %q", got.Host, got.RemoteAddr)
	}
	if _, err := New(handler, WithRemoteAddr("192.0.2.1:9"), WithHost("")).Get(context.Background(), "/health"); err != nil {
		t.Fatal(err)
	}
	if got.Host != "127.0.0.1:21114" || got.RemoteAddr != "192.0.2.1:9" {
		t.Errorf("options: Host %q, RemoteAddr %q", got.Host, got.RemoteAddr)
	}
}

func TestStatusesBecomeErrorsLikeAxios(t *testing.T) {
	cases := []struct {
		status   int
		wantErr  string
		wantCode string
	}{
		{200, "", ""},
		{201, "", ""},
		{202, "", ""},
		{299, "", ""},
		{199, "Request failed with status code 199", ""},
		{302, "Request failed with status code 302", ""},
		{400, "Request failed with status code 400", "ERR_BAD_REQUEST"},
		{404, "Request failed with status code 404", "ERR_BAD_REQUEST"},
		{429, "Request failed with status code 429", "ERR_BAD_REQUEST"},
		{500, "Request failed with status code 500", "ERR_BAD_RESPONSE"},
		{503, "Request failed with status code 503", "ERR_BAD_RESPONSE"},
	}
	for _, c := range cases {
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(c.status)
			io.WriteString(w, `{"error":"e"}`)
		})
		resp, err := New(handler).Get(context.Background(), "/x")
		if resp == nil || resp.Status != c.status {
			t.Errorf("status %d: response = %+v", c.status, resp)
			continue
		}
		if c.wantErr == "" {
			if err != nil {
				t.Errorf("status %d: error %v, want none", c.status, err)
			}
			continue
		}
		e := AsError(err)
		if e == nil || e.Message != c.wantErr || e.Error() != c.wantErr || e.Code != c.wantCode {
			t.Errorf("status %d: error = %+v, want message %q code %q", c.status, e, c.wantErr, c.wantCode)
			continue
		}
		if e.Response != resp || e.Status() != c.status || toJSON(t, e.Data()) != `{"error":"e"}` {
			t.Errorf("status %d: error response = %+v", c.status, e.Response)
		}
	}
}

func TestResponseDataParsesJSONWhateverTheContentType(t *testing.T) {
	cases := []struct {
		name        string
		contentType string
		body        string
		want        string
	}{
		{"json", "application/json", `{"b":1,"a":[true,null,"x"]}`, `{"b":1,"a":[true,null,"x"]}`},
		{"json sent as text", "text/plain; charset=utf-8", `{"error":"not found"}` + "\n", `{"error":"not found"}`},
		{"plain words", "text/plain; charset=utf-8", "Invalid token ID\n", `"Invalid token ID\n"`},
		{"empty", "", "", `""`},
		{"number", "text/plain", "42", `42`},
		{"null", "application/json", "null", `null`},
		{"two values", "application/json", `{} {}`, `"{} {}"`},
		{"byte order mark", "application/json", "\xef\xbb\xbf" + `{"a":1}`, `{"a":1}`},
		{"html", "text/html", "<h1>x</h1>", `"<h1>x</h1>"`},
	}
	for _, c := range cases {
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if c.contentType != "" {
				w.Header().Set("Content-Type", c.contentType)
			}
			io.WriteString(w, c.body)
		})
		resp, err := New(handler).Get(context.Background(), "/x")
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got := toJSON(t, resp.Data()); got != c.want {
			t.Errorf("%s: Data() = %s, want %s", c.name, got, c.want)
		}
	}
}

func TestResponseHelpers(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Disposition", `attachment; filename="x.csv"`)
		w.WriteHeader(201)
		io.WriteString(w, `{"id":7}`)
	})
	resp, err := New(handler).Post(context.Background(), "/x")
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		ID int `json:"id"`
	}
	if err := resp.Decode(&out); err != nil || out.ID != 7 {
		t.Errorf("Decode: %v, %+v", err, out)
	}
	if resp.Text() != `{"id":7}` || string(resp.Body) != `{"id":7}` || resp.Status != 201 {
		t.Errorf("response = %+v", resp)
	}
	if resp.Header.Get("Content-Disposition") != `attachment; filename="x.csv"` {
		t.Errorf("headers = %v", resp.Header)
	}
	// The listener names the type of a body the handler did not name.
	if got := resp.Header.Get("Content-Type"); got != "text/plain; charset=utf-8" {
		t.Errorf("Content-Type = %q, want the detected type", got)
	}
}

func TestHeadersSetAfterTheStatusAreNotSent(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Before", "1")
		w.WriteHeader(404)
		w.Header().Set("X-After", "1")
		w.WriteHeader(200)
		io.WriteString(w, "x")
	})
	resp, _ := New(handler).Get(context.Background(), "/x")
	if resp.Status != 404 || resp.Header.Get("X-Before") != "1" || resp.Header.Get("X-After") != "" {
		t.Errorf("response = %d %v", resp.Status, resp.Header)
	}
}

func TestTimeoutGivesTheAxiosError(t *testing.T) {
	released := make(chan struct{})
	var once sync.Once
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
		once.Do(func() { close(released) })
	})
	c := New(handler, WithTimeout(30*time.Millisecond))
	start := time.Now()
	resp, err := c.Get(context.Background(), "/slow")
	e := AsError(err)
	if resp != nil || e == nil || e.Message != "timeout of 30ms exceeded" || e.Code != "ECONNABORTED" || e.Response != nil || e.Status() != 0 || e.Data() != nil {
		t.Fatalf("response %v, error %+v", resp, e)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("the call took %v", elapsed)
	}
	select {
	case <-released:
	case <-time.After(2 * time.Second):
		t.Error("the handler's context did not end with the call")
	}

	// A call option replaces the client's timeout.
	_, err = c.Get(context.Background(), "/slow", Timeout(10*time.Millisecond))
	if e := AsError(err); e == nil || e.Message != "timeout of 10ms exceeded" {
		t.Errorf("with a call timeout: %+v", e)
	}
}

func TestHandlerThatIgnoresItsContextCannotHoldTheCaller(t *testing.T) {
	block := make(chan struct{})
	defer close(block)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-block })
	start := time.Now()
	_, err := New(handler, WithTimeout(20*time.Millisecond)).Get(context.Background(), "/stuck")
	if e := AsError(err); e == nil || e.Code != "ECONNABORTED" {
		t.Fatalf("error = %+v", e)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("the call took %v", elapsed)
	}
}

func TestNoTimeoutWhenZero(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, has := r.Context().Deadline(); has {
			t.Error("the request has a deadline")
		}
	})
	if _, err := New(handler, WithTimeout(0)).Get(context.Background(), "/x"); err != nil {
		t.Fatal(err)
	}
}

func TestCanceledContextGivesCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cancel()
		<-r.Context().Done()
	})
	_, err := New(handler).Get(ctx, "/x")
	if e := AsError(err); e == nil || e.Message != "canceled" || e.Code != "ERR_CANCELED" || e.Response != nil {
		t.Errorf("error = %+v", e)
	}
}

func TestPanicInHandlerIsAHangUp(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { panic("boom") })
	resp, err := New(handler).Get(context.Background(), "/x")
	if e := AsError(err); resp != nil || e == nil || e.Message != "socket hang up" || e.Code != "ECONNRESET" || e.Response != nil {
		t.Errorf("response %v, error %+v", resp, e)
	}
}

func TestPathGuard(t *testing.T) {
	called := false
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true })
	c := New(handler)
	cases := []struct{ path, want string }{
		{"", "API path is required"},
		{"/a\\b", "Invalid API path"},
		{"/a\x00b", "Invalid API path"},
		{"http://evil.example/x", "Absolute API URLs are not allowed"},
		{"HTTPS://evil.example/x", "Absolute API URLs are not allowed"},
		{"//evil.example/x", "Absolute API URLs are not allowed"},
		{"peers", "API path must be relative to the configured Go API base URL"},
		{"/peers/../keys", "API path must not contain parent-directory segments"},
		{"/peers/a..b", "API path must not contain parent-directory segments"},
		{"/peers/./x", "Invalid API path segment"},
		{"/peers?x=1\ny", "Invalid query string"},
	}
	for _, tc := range cases {
		resp, err := c.Get(context.Background(), tc.path)
		e := AsError(err)
		if resp != nil || e == nil || e.Message != tc.want || e.Response != nil || e.Code != "" {
			t.Errorf("path %q: response %v, error %+v, want %q", tc.path, resp, e, tc.want)
		}
	}
	if called {
		t.Error("a refused path reached the handler")
	}
	// Dots in a query and single dots in a name are fine.
	for _, path := range []string{"/peers?x=..", "/config/a.b", "/peers/x?next=http://a/b"} {
		if _, err := c.Get(context.Background(), path); err != nil {
			t.Errorf("path %q: %v", path, err)
		}
	}
}

func TestTargetIsEscapedLikeNodeDidIt(t *testing.T) {
	var uri string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { uri = r.RequestURI })
	c := New(handler)
	cases := []struct{ path, want string }{
		{"/peers/a%2Fb", "/api/peers/a%2Fb"},
		{"/peers/a b", "/api/peers/a%20b"},
		{"/peers/ä", "/api/peers/%C3%A4"},
		{`/peers/"<>{}` + "`", "/api/peers/%22%3C%3E%7B%7D%60"},
		{"/peers/x#frag", "/api/peers/x"},
		{"/peers/x?a=b c#frag", "/api/peers/x?a=b%20c"},
		{"/peers/x\t\r", "/api/peers/x"},
		{"/peers/x ", "/api/peers/x"},
		{"/peers?q='", "/api/peers?q=%27"},
		{"/peers/x?y=1", "/api/peers/x?y=1"},
		// An escaped dot segment stays an id: it is not resolved to the parent
		// path, where /org/%2e%2e/users would reach /api/users.
		{"/org/%2e%2e/users", "/api/org/%2e%2e/users"},
		{"/org/%2E./users", "/api/org/%2E./users"},
	}
	for _, tc := range cases {
		if _, err := c.Get(context.Background(), tc.path); err != nil {
			t.Errorf("path %q: %v", tc.path, err)
			continue
		}
		if uri != tc.want {
			t.Errorf("path %q: request target = %q, want %q", tc.path, uri, tc.want)
		}
	}
	// Query options follow a query in the path.
	if _, err := c.Get(context.Background(), "/peers?a=1", Query("b", "x y")); err != nil || uri != "/api/peers?a=1&b=x+y" {
		t.Errorf("target = %q, err %v", uri, err)
	}
}

func TestBrokenEscapeIsThe400OfTheListener(t *testing.T) {
	called := false
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true })
	resp, err := New(handler).Get(context.Background(), "/peers/%zz")
	e := AsError(err)
	if resp == nil || resp.Status != 400 || string(resp.Body) != "400 Bad Request" || e == nil || e.Message != "Request failed with status code 400" {
		t.Errorf("response %+v, error %+v", resp, e)
	}
	if called {
		t.Error("the handler was called")
	}
}

func TestEncodedSlashStaysInItsSegment(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/peers/{id}", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"id":"`+r.PathValue("id")+`"}`)
	})
	resp, err := New(mux).Get(context.Background(), "/peers/"+EncodeURIComponent("a/b"))
	if err != nil || toJSON(t, resp.Data()) != `{"id":"a/b"}` {
		t.Errorf("response %+v, err %v", resp, err)
	}
}

func TestRedirectsOnTheAPIServerAreFollowed(t *testing.T) {
	mux := http.NewServeMux()
	var seen []string
	record := func(r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		seen = append(seen, r.Method+" "+r.RequestURI+" "+string(body)+" "+r.Header.Get("Content-Type"))
	}
	mux.HandleFunc("/api/moved", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		http.Redirect(w, r, "/api/target?x=1", http.StatusMovedPermanently)
	})
	mux.HandleFunc("/api/kept", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		http.Redirect(w, r, "/api/target", http.StatusTemporaryRedirect)
	})
	mux.HandleFunc("/api/elsewhere", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		http.Redirect(w, r, "https://idp.example/authorize", http.StatusFound)
	})
	mux.HandleFunc("/api/loop", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/api/loop", http.StatusFound)
	})
	mux.HandleFunc("/api/target", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		io.WriteString(w, `{"at":"target"}`)
	})
	c := New(mux)
	ctx := context.Background()

	resp, err := c.Post(ctx, "/moved", Body(map[string]any{"a": 1}))
	if err != nil || toJSON(t, resp.Data()) != `{"at":"target"}` {
		t.Fatalf("POST over a 301: %+v, %v", resp, err)
	}
	if want := []string{`POST /api/moved {"a":1} application/json`, `GET /api/target?x=1  `}; strings.Join(seen, "|") != strings.Join(want, "|") {
		t.Errorf("POST over a 301 made %q, want %q", seen, want)
	}

	seen = nil
	if _, err := c.Put(ctx, "/kept", Body(map[string]any{"a": 1})); err != nil {
		t.Fatal(err)
	}
	if want := []string{`PUT /api/kept {"a":1} application/json`, `PUT /api/target {"a":1} application/json`}; strings.Join(seen, "|") != strings.Join(want, "|") {
		t.Errorf("PUT over a 307 made %q, want %q", seen, want)
	}

	// A redirect to another host is returned, not followed.
	resp, err = c.Get(ctx, "/elsewhere")
	if e := AsError(err); resp == nil || resp.Status != 302 || e == nil || e.Message != "Request failed with status code 302" {
		t.Errorf("redirect to another host: %+v, %+v", resp, e)
	}

	seen = nil
	resp, err = c.Post(ctx, "/moved", NoRedirects())
	if e := AsError(err); resp == nil || resp.Status != 301 || resp.Header.Get("Location") != "/api/target?x=1" || e == nil || len(seen) != 1 {
		t.Errorf("NoRedirects: %+v, %+v, requests %q", resp, e, seen)
	}

	if _, err := c.Get(ctx, "/loop"); err == nil || AsError(err).Message != "Maximum number of redirects exceeded" {
		t.Errorf("redirect loop: %v", err)
	}
}

func TestValidateStatus(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(404) })
	resp, err := New(handler).Get(context.Background(), "/x", ValidateStatus(func(int) bool { return true }))
	if err != nil || resp.Status != 404 {
		t.Errorf("all statuses valid: %+v, %v", resp, err)
	}
	handler = func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }
	_, err = New(handler).Get(context.Background(), "/x", ValidateStatus(func(s int) bool { return s == 204 }))
	if e := AsError(err); e == nil || e.Message != "Request failed with status code 200" {
		t.Errorf("only 204 valid: %+v", e)
	}
}

func TestBodyFollowsAxios(t *testing.T) {
	var got string
	var length int64
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got, length = string(b), r.ContentLength
	})
	c := New(handler)
	cases := []struct {
		name string
		opt  CallOption
		want string
	}{
		{"object", Body(NewObject("b", 1.0, "a", "<x>")), `{"b":1,"a":"<x>"}`},
		{"map", Body(map[string]any{"a": []string{"x"}}), `{"a":["x"]}`},
		{"struct", Body(struct {
			A int `json:"a"`
		}{3}), `{"a":3}`},
		{"true", Body(true), `true`},
		{"nil", Body(nil), `null`},
		{"text that is JSON", Body(` {"a":1} `), `{"a":1}`},
		{"text that is not JSON", Body("not an object"), `"not an object"`},
		{"raw", RawBody([]byte("a=1")), `a=1`},
	}
	for _, tc := range cases {
		if _, err := c.Post(context.Background(), "/x", tc.opt); err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		if got != tc.want || length != int64(len(tc.want)) {
			t.Errorf("%s: body = %s (length %d), want %s", tc.name, got, length, tc.want)
		}
	}
	if _, err := c.Post(context.Background(), "/x", Body(make(chan int))); err == nil || AsError(err).Response != nil {
		t.Errorf("a body that cannot be encoded: %v", err)
	}
}

func TestQueryParamsFollowAxios(t *testing.T) {
	var uri string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { uri = r.RequestURI })
	c := New(handler)
	cases := []struct {
		name   string
		params any
		want   string
	}{
		{"object", NewObject("b", "x y", "a", 2.0, "skip", nil, "flag", false, "empty", ""), "/api/q?b=x+y&a=2&flag=false&empty="},
		{"list and nested", NewObject("ids", []any{"1", 2.0}, "f", NewObject("k", "v", "n", nil)), "/api/q?ids%5B%5D=1&ids%5B%5D=2&f%5Bk%5D=v&f%5Bn%5D="},
		{"map", map[string]any{"b": 1.5, "a": "x"}, "/api/q?a=x&b=1.5"},
		{"values", url.Values{"a": {"1"}, "m": {"x", "y"}}, "/api/q?a=1&m%5B%5D=x&m%5B%5D=y"},
		{"nil", nil, "/api/q"},
	}
	for _, tc := range cases {
		if _, err := c.Get(context.Background(), "/q", QueryParams(tc.params)); err != nil {
			t.Fatal(err)
		}
		if uri != tc.want {
			t.Errorf("%s: target = %q, want %q", tc.name, uri, tc.want)
		}
	}
}

func TestAsErrorOnOtherErrors(t *testing.T) {
	if AsError(nil) != nil || AsError(errors.New("x")) != nil {
		t.Error("AsError made an *Error of something else")
	}
	var e *Error
	if e.Status() != 0 || e.Data() != nil {
		t.Error("a nil *Error has a status or data")
	}
}

func TestHandlerForUpgradesCarriesIdentityHostAndAddress(t *testing.T) {
	var got *http.Request
	var id api.Identity
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r
		id, _ = api.IdentityFromContext(r.Context())
		w.WriteHeader(http.StatusSwitchingProtocols)
	})
	h := New(inner, WithHost("10.1.2.3:21121")).Handler()
	req := httptest.NewRequest("GET", "/api/cdap/devices/D1/terminal?x=1", nil)
	req.Host = "console.example"
	req.RemoteAddr = "203.0.113.5:4000"
	req.Header.Set("Sec-WebSocket-Protocol", "cdap-terminal")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusSwitchingProtocols {
		t.Errorf("the inner handler did not write to the caller's writer: %d", rec.Code)
	}
	if got.Host != "10.1.2.3:21121" || got.RemoteAddr != "127.0.0.1:0" {
		t.Errorf("Host %q, RemoteAddr %q", got.Host, got.RemoteAddr)
	}
	if id.Username != BridgeUsername || id.Role != BridgeRole {
		t.Errorf("identity = %+v", id)
	}
	if got.URL.RequestURI() != "/api/cdap/devices/D1/terminal?x=1" || got.Header.Get("Sec-WebSocket-Protocol") != "cdap-terminal" {
		t.Errorf("request = %s %v", got.URL, got.Header)
	}
	if req.Host != "console.example" || req.RemoteAddr != "203.0.113.5:4000" {
		t.Error("the caller's request was changed")
	}
}

// ForwardHandler keeps a caller address the request brings and gives a
// request without one the client's, as Handler does.
func TestForwardHandlerKeepsTheCallersAddress(t *testing.T) {
	var got *http.Request
	var id api.Identity
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r
		id, _ = api.IdentityFromContext(r.Context())
	})
	h := New(inner, WithHost("10.1.2.3:21121")).ForwardHandler()
	for remote, want := range map[string]string{
		"203.0.113.5:0":   "203.0.113.5:0",
		"[2001:db8::5]:0": "[2001:db8::5]:0",
		"pipe":            "127.0.0.1:0",
		"":                "127.0.0.1:0",
	} {
		req := httptest.NewRequest("GET", "/agent.ashx?id=1", nil)
		req.RemoteAddr = remote
		h.ServeHTTP(httptest.NewRecorder(), req)
		if got.RemoteAddr != want || got.Host != "10.1.2.3:21121" || got.URL.RequestURI() != "/agent.ashx?id=1" {
			t.Errorf("RemoteAddr %q: the handler saw RemoteAddr %q, Host %q, %s", remote, got.RemoteAddr, got.Host, got.URL)
		}
		if id.Username != BridgeUsername || id.Role != BridgeRole {
			t.Errorf("RemoteAddr %q: identity = %+v", remote, id)
		}
	}
}

// WithActor names the person of the call's context, or of the request
// for Handler and ForwardHandler, next to the client's identity; a call
// made As another identity keeps that identity as it is.
func TestActorComesFromTheContext(t *testing.T) {
	type key struct{}
	var id api.Identity
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, _ = api.IdentityFromContext(r.Context())
		_, _ = io.WriteString(w, `{}`)
	})
	c := New(inner, WithActor(func(ctx context.Context) string {
		name, _ := ctx.Value(key{}).(string)
		return name
	}))
	ctx := context.WithValue(context.Background(), key{}, "alice")
	if _, err := c.Get(ctx, "/x"); err != nil || id != (api.Identity{Username: BridgeUsername, Role: BridgeRole, Actor: "alice"}) {
		t.Errorf("a call: %+v, %v", id, err)
	}
	if _, _ = c.Get(context.Background(), "/x"); id.Actor != "" {
		t.Errorf("a call without an account: %+v", id)
	}
	operator := api.Identity{Username: "op", Role: "operator"}
	if _, _ = c.Get(ctx, "/x", As(operator)); id != operator {
		t.Errorf("a call as another identity: %+v", id)
	}
	for name, h := range map[string]http.Handler{"Handler": c.Handler(), "ForwardHandler": c.ForwardHandler()} {
		id = api.Identity{}
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/x", nil).WithContext(ctx))
		if id.Actor != "alice" || id.Username != BridgeUsername {
			t.Errorf("%s: %+v", name, id)
		}
	}
}

// ActingFor names the account in a context that has no session, such as
// a socket's dial; the name wins over what WithActor finds, also for a
// client without WithActor.
func TestActingForNamesTheAccount(t *testing.T) {
	var id api.Identity
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, _ = api.IdentityFromContext(r.Context())
		_, _ = io.WriteString(w, `{}`)
	})
	fromSession := New(inner, WithActor(func(context.Context) string { return "from-the-session" }))
	for name, c := range map[string]*Client{"with WithActor": fromSession, "without": New(inner)} {
		for kind, h := range map[string]http.Handler{"Handler": c.Handler(), "ForwardHandler": c.ForwardHandler()} {
			id = api.Identity{}
			h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/x", nil).WithContext(ActingFor(context.Background(), "anna")))
			if id.Actor != "anna" || id.Username != BridgeUsername {
				t.Errorf("%s, %s: %+v", name, kind, id)
			}
		}
		if _, err := c.Get(ActingFor(context.Background(), "anna"), "/x"); err != nil || id.Actor != "anna" {
			t.Errorf("%s, a call: %+v, %v", name, id, err)
		}
	}
	if _, _ = fromSession.Get(context.Background(), "/x"); id.Actor != "from-the-session" {
		t.Errorf("a call without ActingFor: %+v", id)
	}
}
