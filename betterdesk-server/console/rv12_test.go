package console

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/console/kit"
)

// raw sends a request line as written, which net/http's client would
// escape anew, with the agent's cookies and CSRF token.
func (a *agent) raw(method, target string) answer {
	a.w.t.Helper()
	token := a.csrf()
	var pairs []string
	for name, value := range a.cookies {
		pairs = append(pairs, name+"="+value)
	}
	conn, err := net.DialTimeout("tcp", a.w.front.Listener.Addr().String(), 5*time.Second)
	if err != nil {
		a.w.t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(20 * time.Second))
	request := method + " " + target + " HTTP/1.1\r\nHost: " + a.w.front.Listener.Addr().String() +
		"\r\nCookie: " + strings.Join(pairs, "; ") + "\r\nX-CSRF-Token: " + token +
		"\r\nContent-Type: application/json\r\nContent-Length: 2\r\nConnection: close\r\n\r\n{}"
	if _, err := io.WriteString(conn, request); err != nil {
		a.w.t.Fatal(err)
	}
	res, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		a.w.t.Fatalf("%s %s: %v", method, target, err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	return answer{status: res.StatusCode, header: res.Header, body: string(body)}
}

// TestEscapedPathMissesTheRouteThePanelAccessGuardMisses: a path Go writes
// anew (it holds a quote) with an escaped letter in a fixed segment fits
// no route, so it cannot reach a route of a section the guard would have
// refused. Every step in front of the router reads the path as sent.
func TestEscapedPathMissesTheRouteThePanelAccessGuardMisses(t *testing.T) {
	w := newWorld(t, setup{})
	w.s.Router().Post("/api/users/{id}/rv12", probe)
	w.s.Router().Get("/api/rv12/{id}", func(rw http.ResponseWriter, r *http.Request) {
		kit.JSON(rw, r, http.StatusOK, map[string]string{"id": r.PathValue("id")})
	})
	a := w.agent().signIn("viewer")

	const denied = `{"success":false,"error":"Panel access denied: users","code":"panel_access_denied"}`
	if res := a.raw("POST", "/api/users/9/rv12"); res.status != 403 || res.body != denied {
		t.Fatalf("plain path: %d %s, want the guard's refusal", res.status, res.body)
	}
	for _, target := range []string{`/api/%75sers/9"/rv12`, `/api/users%2F9"/rv12`, `/%61pi/users/9"/rv12`, "/api/%75sers/9|/rv12"} {
		res := a.raw("POST", target)
		if res.status != 404 || strings.Contains(res.body, `"route"`) {
			t.Errorf("%s: %d %.200s, want 404 from no route", target, res.status, res.body)
		}
	}
	// A parameter that holds such a byte still reaches its route, decoded
	res := a.raw("GET", `/api/rv12/a"b%20c|`)
	if res.status != 200 || res.json(t)["id"] != `a"b c|` {
		t.Errorf("parameter with a quote: %d %s", res.status, res.body)
	}
}
