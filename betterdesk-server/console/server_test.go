package console

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/unitronix/betterdesk-server/console/config"
	"github.com/unitronix/betterdesk-server/console/httpx"
)

// fakeNode stands in for the Node console: it reports what it received.
func fakeNode(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/ws/echo", func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("upstream accept: %v", err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		typ, msg, err := conn.Read(r.Context())
		if err != nil {
			return
		}
		_ = conn.Write(r.Context(), typ, append([]byte("node:"), msg...))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Add("Set-Cookie", "bd.sid=abc; Path=/; HttpOnly")
		w.WriteHeader(http.StatusTeapot)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"server": "node",
			"method": r.Method,
			"uri":    r.RequestURI,
			"host":   r.Host,
			"xff":    r.Header.Get("X-Forwarded-For"),
			"proto":  r.Header.Get("X-Forwarded-Proto"),
			"xfhost": r.Header.Get("X-Forwarded-Host"),
			"realip": r.Header.Get("X-Real-IP"),
			"cookie": r.Header.Get("Cookie"),
			"body":   string(body),
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func frontDoor(t *testing.T, upstream string, trustProxy string) (*Server, *httptest.Server) {
	t.Helper()
	cfg := &config.Config{Host: "127.0.0.1"}
	if upstream != "" {
		u, err := url.Parse(upstream)
		if err != nil {
			t.Fatal(err)
		}
		cfg.NodeUpstream = u
	}
	trust, err := httpx.ParseTrustProxy(trustProxy)
	if err != nil {
		t.Fatal(err)
	}
	cfg.TrustProxy = trust
	s := New(cfg)
	front := httptest.NewServer(s)
	t.Cleanup(front.Close)
	return s, front
}

func getJSON(t *testing.T, req *http.Request) (*http.Response, map[string]string) {
	t.Helper()
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var body map[string]string
	_ = json.NewDecoder(res.Body).Decode(&body)
	return res, body
}

func TestUnportedRequestGoesToNode(t *testing.T) {
	node := fakeNode(t)
	_, front := frontDoor(t, node.URL, "")

	req, _ := http.NewRequest("POST", front.URL+"/api/devices/A%2Fb?x=1&y=%zz", strings.NewReader(`{"a":1}`))
	req.Host = "panel.example:5000"
	req.Header.Set("Cookie", "bd.sid=s%3Aabc.sig")
	req.Header.Set("X-Forwarded-For", "203.0.113.50")
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("X-Real-IP", "203.0.113.51")
	res, body := getJSON(t, req)

	if res.StatusCode != http.StatusTeapot {
		t.Errorf("status = %d, want Node's 418", res.StatusCode)
	}
	if got := res.Header.Get("Set-Cookie"); !strings.HasPrefix(got, "bd.sid=abc") {
		t.Errorf("Set-Cookie = %q, want Node's cookie", got)
	}
	want := map[string]string{
		"server": "node",
		"method": "POST",
		"uri":    "/api/devices/A%2Fb?x=1&y=%zz",
		"host":   "panel.example:5000",
		"xff":    "127.0.0.1",
		"proto":  "http",
		"xfhost": "panel.example:5000",
		"realip": "",
		"cookie": "bd.sid=s%3Aabc.sig",
		"body":   `{"a":1}`,
	}
	for k, v := range want {
		if body[k] != v {
			t.Errorf("node saw %s = %q, want %q", k, body[k], v)
		}
	}
}

func TestTrustedProxyHeadersReachNode(t *testing.T) {
	node := fakeNode(t)
	_, front := frontDoor(t, node.URL, "loopback")

	req, _ := http.NewRequest("GET", front.URL+"/api/x", nil)
	req.Header.Set("X-Forwarded-For", "203.0.113.50, 127.0.0.1")
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("X-Forwarded-Host", "panel.example")
	_, body := getJSON(t, req)

	if body["xff"] != "203.0.113.50" || body["proto"] != "https" || body["host"] != "panel.example" {
		t.Errorf("node saw xff=%q proto=%q host=%q, want the client behind the trusted proxy", body["xff"], body["proto"], body["host"])
	}
}

func TestGoRouteAnswersBeforeNode(t *testing.T) {
	node := fakeNode(t)
	s, front := frontDoor(t, node.URL, "")
	s.Router().Get("/api/ported/{id}", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"server": "go", "id": r.PathValue("id")})
	})

	req, _ := http.NewRequest("GET", front.URL+"/API/Ported/7/", nil)
	res, body := getJSON(t, req)
	if res.StatusCode != http.StatusOK || body["server"] != "go" || body["id"] != "7" {
		t.Errorf("status %d body %v, want the Go route", res.StatusCode, body)
	}

	// Another method on the same path is not a Go route
	req, _ = http.NewRequest("DELETE", front.URL+"/api/ported/7", nil)
	_, body = getJSON(t, req)
	if body["server"] != "node" {
		t.Errorf("DELETE answered by %q, want node", body["server"])
	}
}

func TestWebsocketGoesToNode(t *testing.T) {
	node := fakeNode(t)
	_, front := frontDoor(t, node.URL, "")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(front.URL, "http")+"/ws/echo", nil)
	if err != nil {
		t.Fatalf("dial through the front door: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	if err := conn.Write(ctx, websocket.MessageText, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	_, msg, err := conn.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if string(msg) != "node:hello" {
		t.Errorf("got %q, want node:hello", msg)
	}
}

func TestNodeDownAnswers502(t *testing.T) {
	node := fakeNode(t)
	_, front := frontDoor(t, node.URL, "")
	node.Close()

	req, _ := http.NewRequest("GET", front.URL+"/api/x", nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var body map[string]any
	_ = json.NewDecoder(res.Body).Decode(&body)
	if res.StatusCode != http.StatusBadGateway || body["success"] != false {
		t.Errorf("status %d body %v, want 502 with success false", res.StatusCode, body)
	}
}

func TestWithoutNodeUnknownPathIs404(t *testing.T) {
	_, front := frontDoor(t, "", "")
	req, _ := http.NewRequest("GET", front.URL+"/api/nothing", nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var body map[string]any
	_ = json.NewDecoder(res.Body).Decode(&body)
	if res.StatusCode != http.StatusNotFound || body["error"] != "Not Found" {
		t.Errorf("status %d body %v, want 404 Not Found", res.StatusCode, body)
	}
}

func TestStartAndStop(t *testing.T) {
	s := New(&config.Config{Host: "127.0.0.1", Port: 0})
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	res, err := http.Get("http://" + s.Addr() + "/nothing")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", res.StatusCode)
	}
	s.Stop()
	if _, err := http.Get("http://" + s.Addr() + "/nothing"); err == nil {
		t.Error("server still answers after Stop")
	}
}
