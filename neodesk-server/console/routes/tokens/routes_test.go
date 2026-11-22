package tokens_test

import (
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/routes/internal/routetest"
)

const (
	signedOut = `{"success":false,"error":"Unauthorized. Please log in.","code":"session_required"}`
	denied    = `{"success":false,"error":"Permission denied: enrollment.manage"}`
)

// routes lists every route of the group with a request for it.
var routes = []struct{ method, target, body string }{
	{"GET", "/api/panel/tokens", ""},
	{"POST", "/api/panel/tokens", `{"name":"x"}`},
	{"POST", "/api/panel/tokens/generate-bulk", `{"count":1}`},
	{"PUT", "/api/panel/tokens/7", `{"name":"x"}`},
	{"DELETE", "/api/panel/tokens/7", ""},
	{"POST", "/api/panel/tokens/7/bind", `{"peer_id":"900100001"}`},
	{"GET", "/api/panel/enrollment/mode", ""},
	{"PUT", "/api/panel/enrollment/mode", `{"mode":"open"}`},
}

func TestGuards(t *testing.T) {
	api := routetest.NewAPI()
	for _, request := range []string{"GET /api/tokens", "POST /api/tokens", "POST /api/tokens/generate-bulk", "PUT /api/tokens/7",
		"DELETE /api/tokens/7", "POST /api/tokens/7/bind", "GET /api/enrollment/mode", "PUT /api/enrollment/mode"} {
		api.On(request, 200, `{}`)
	}
	w := routetest.New(t, routetest.Options{API: api})
	for _, route := range routes {
		name := route.method + " " + route.target
		if got := w.SignedOut().Do(route.method, route.target, route.body); got.Status != 401 || got.Body != signedOut {
			t.Errorf("%s signed out: %d %s", name, got.Status, got.Body)
		}
		for _, role := range []string{"guest_issuer", "viewer", "operator"} {
			if got := w.As(role).Do(route.method, route.target, route.body); got.Status != 403 || got.Body != denied {
				t.Errorf("%s as %s: %d %s", name, role, got.Status, got.Body)
			}
		}
		if calls := api.Calls(); len(calls) != 0 {
			t.Errorf("%s reached the API server for accounts that may not: %v", name, calls)
		}
		for _, role := range []string{"global_admin", "server_admin", "super_admin", "admin"} {
			if got := w.As(role).Do(route.method, route.target, route.body); got.Status != 200 {
				t.Errorf("%s as %s: %d %s", name, role, got.Status, got.Body)
			}
		}
		if calls := api.Calls(); len(calls) != 4 {
			t.Errorf("%s: %d calls for four accounts that may: %v", name, len(calls), calls)
		}
	}
}

func TestCallsAndAnswers(t *testing.T) {
	api := routetest.NewAPI()
	api.On("GET /api/tokens", 200, `{"tokens":null,"count":0}`)
	api.On("GET /api/tokens?include_revoked=true", 200, `{"tokens":[{"id":3,"status":"revoked"}],"count":1}`)
	api.On("POST /api/tokens", 201, `{"id":9,"token":"t","name":"x"}`)
	api.On("POST /api/tokens/generate-bulk", 201, `{"tokens":[{"id":1},{"id":2}],"count":2}`)
	api.On("PUT /api/tokens/7", 200, `{"id":7,"name":"renamed"}`)
	api.OnText("PUT /api/tokens/8", 404, "Token not found\n")
	api.On("PUT /api/tokens/9", 200, `{"error":"told in the body","id":9}`)
	api.On("DELETE /api/tokens/7", 200, `{"success":true,"message":"Token revoked"}`)
	api.OnText("DELETE /api/tokens/abc", 400, "Invalid token ID\n")
	api.On("POST /api/tokens/7/bind", 200, `{"success":true,"message":"Token bound to peer","token_id":7,"peer_id":"900100001"}`)
	api.OnText("POST /api/tokens/8/bind", 409, "Token has been revoked\n")
	api.On("GET /api/enrollment/mode", 200, `{"mode":"open","description":"d"}`)
	api.On("PUT /api/enrollment/mode", 200, `{"success":true,"mode":"managed","description":"m"}`)
	api.On("DELETE /api/tokens/5", 0, "")
	admin := routetest.New(t, routetest.Options{API: api}).As("admin")
	// The cases the API server refused, with its status; an id the console
	// refuses itself is a 400
	statuses := map[string]int{
		"a refusal has the API server's status and text": 404,
		"an id is escaped for the path":                  404,
		"revoke with a bad id":                           400,
		"revoke while the API server hangs up":           502,
		"bind sends true as text":                        409,
	}

	for _, tc := range []struct {
		name                 string
		method, target, body string
		want                 string
		calls                []string
	}{
		{"list", "GET", "/api/panel/tokens", "",
			`{"success":true,"data":{"tokens":null,"count":0}}`, []string{"GET /api/tokens"}},
		{"list with revoked", "GET", "/api/panel/tokens?include_revoked=true", "",
			`{"success":true,"data":{"tokens":[{"id":3,"status":"revoked"}],"count":1}}`, []string{"GET /api/tokens?include_revoked=true"}},
		{"another value does not ask for revoked", "GET", "/api/panel/tokens?include_revoked=1", "",
			`{"success":true,"data":{"tokens":null,"count":0}}`, []string{"GET /api/tokens"}},
		{"upper case does not", "GET", "/api/panel/tokens?include_revoked=TRUE", "",
			`{"success":true,"data":{"tokens":null,"count":0}}`, []string{"GET /api/tokens"}},
		{"sent twice it is a list, not true", "GET", "/api/panel/tokens?include_revoked=true&include_revoked=true", "",
			`{"success":true,"data":{"tokens":null,"count":0}}`, []string{"GET /api/tokens"}},
		{"in brackets it is a list", "GET", "/api/panel/tokens?include_revoked[]=true", "",
			`{"success":true,"data":{"tokens":null,"count":0}}`, []string{"GET /api/tokens"}},
		{"an escaped value is decoded", "GET", "/api/panel/tokens?include_revoked=%74rue", "",
			`{"success":true,"data":{"tokens":[{"id":3,"status":"revoked"}],"count":1}}`, []string{"GET /api/tokens?include_revoked=true"}},

		{"create passes the body on and keeps the key order of the answer", "POST", "/api/panel/tokens", `{"note":"n","name":"x","max_uses":3}`,
			`{"success":true,"data":{"id":9,"token":"t","name":"x"}}`, []string{`POST /api/tokens {"note":"n","name":"x","max_uses":3}`}},
		{"create writes numbers and repeated names as JavaScript does", "POST", "/api/panel/tokens", `{"name":"a","max_uses":2.0,"name":"b","expires_in":1e2}`,
			`{"success":true,"data":{"id":9,"token":"t","name":"x"}}`, []string{`POST /api/tokens {"name":"b","max_uses":2,"expires_in":100}`}},
		{"create passes a list on", "POST", "/api/panel/tokens", `[{"name":"x"}]`,
			`{"success":true,"data":{"id":9,"token":"t","name":"x"}}`, []string{`POST /api/tokens [{"name":"x"}]`}},
		{"bulk", "POST", "/api/panel/tokens/generate-bulk", `{"count":2,"name_prefix":"p"}`,
			`{"success":true,"data":{"tokens":[{"id":1},{"id":2}],"count":2}}`, []string{`POST /api/tokens/generate-bulk {"count":2,"name_prefix":"p"}`}},

		{"update", "PUT", "/api/panel/tokens/7", `{"name":"renamed"}`,
			`{"success":true,"data":{"id":7,"name":"renamed"}}`, []string{`PUT /api/tokens/7 {"name":"renamed"}`}},
		{"update writes numbers and repeated names as JavaScript does", "PUT", "/api/panel/tokens/7", `{"max_uses":5.0,"name":"a","name":"renamed"}`,
			`{"success":true,"data":{"id":7,"name":"renamed"}}`, []string{`PUT /api/tokens/7 {"max_uses":5,"name":"renamed"}`}},
		{"update trims the id", "PUT", "/api/panel/tokens/%207%20", `{"name":"renamed"}`,
			`{"success":true,"data":{"id":7,"name":"renamed"}}`, []string{`PUT /api/tokens/7 {"name":"renamed"}`}},
		{"a refusal has the API server's status and text", "PUT", "/api/panel/tokens/8", `{"name":"x"}`,
			`{"success":false,"error":"Token not found"}`, []string{`PUT /api/tokens/8 {"name":"x"}`}},
		{"an error key in a good answer is a failure", "PUT", "/api/panel/tokens/9", `{"name":"x"}`,
			`{"success":false,"error":"told in the body"}`, []string{`PUT /api/tokens/9 {"name":"x"}`}},
		{"an id with a slash never reaches the API server", "PUT", "/api/panel/tokens/1%2F2", `{"name":"x"}`,
			`{"success":false,"error":"Invalid tokenId"}`, nil},
		{"an id is escaped for the path", "PUT", "/api/panel/tokens/a%25b", `{"name":"x"}`,
			`{"success":false,"error":"not found"}`, []string{`PUT /api/tokens/a%25b {"name":"x"}`}},

		{"revoke", "DELETE", "/api/panel/tokens/7", "",
			`{"success":true,"data":{"success":true,"message":"Token revoked"}}`, []string{"DELETE /api/tokens/7"}},
		{"revoke with a bad id", "DELETE", "/api/panel/tokens/abc", "",
			`{"success":false,"error":"Invalid token ID"}`, []string{"DELETE /api/tokens/abc"}},
		{"revoke with an unsafe id", "DELETE", "/api/panel/tokens/a%20b", "",
			`{"success":false,"error":"Invalid tokenId"}`, nil},
		{"revoke while the API server hangs up", "DELETE", "/api/panel/tokens/5", "",
			`{"success":false,"error":"socket hang up"}`, []string{"DELETE /api/tokens/5"}},

		{"bind", "POST", "/api/panel/tokens/7/bind", `{"peer_id":"900100001"}`,
			`{"success":true,"data":{"success":true,"message":"Token bound to peer","token_id":7,"peer_id":"900100001"}}`, []string{`POST /api/tokens/7/bind {"peer_id":"900100001"}`}},
		{"bind sends a number as text", "POST", "/api/panel/tokens/7/bind", `{"peer_id":900100001,"more":1}`,
			`{"success":true,"data":{"success":true,"message":"Token bound to peer","token_id":7,"peer_id":"900100001"}}`, []string{`POST /api/tokens/7/bind {"peer_id":"900100001"}`}},
		{"bind trims the peer", "POST", "/api/panel/tokens/7/bind", `{"peer_id":" 900100001\n"}`,
			`{"success":true,"data":{"success":true,"message":"Token bound to peer","token_id":7,"peer_id":"900100001"}}`, []string{`POST /api/tokens/7/bind {"peer_id":"900100001"}`}},
		{"bind joins a list", "POST", "/api/panel/tokens/7/bind", `{"peer_id":["900100001"]}`,
			`{"success":true,"data":{"success":true,"message":"Token bound to peer","token_id":7,"peer_id":"900100001"}}`, []string{`POST /api/tokens/7/bind {"peer_id":"900100001"}`}},
		{"bind sends true as text", "POST", "/api/panel/tokens/8/bind", `{"peer_id":true}`,
			`{"success":false,"error":"Token has been revoked"}`, []string{`POST /api/tokens/8/bind {"peer_id":"true"}`}},
		{"bind without a peer", "POST", "/api/panel/tokens/7/bind", `{}`,
			`{"success":false,"error":"Invalid peerId"}`, nil},
		{"bind with peer null", "POST", "/api/panel/tokens/7/bind", `{"peer_id":null}`,
			`{"success":false,"error":"Invalid peerId"}`, nil},
		{"bind with peer zero", "POST", "/api/panel/tokens/7/bind", `{"peer_id":0}`,
			`{"success":false,"error":"Invalid peerId"}`, nil},
		{"bind with a list of two", "POST", "/api/panel/tokens/7/bind", `{"peer_id":["a","b"]}`,
			`{"success":false,"error":"Invalid peerId"}`, nil},
		{"bind with an object", "POST", "/api/panel/tokens/7/bind", `{"peer_id":{"id":"900100001"}}`,
			`{"success":false,"error":"Invalid peerId"}`, nil},
		{"bind with a list as body", "POST", "/api/panel/tokens/7/bind", `["900100001"]`,
			`{"success":false,"error":"Invalid peerId"}`, nil},
		{"bind checks the token id first", "POST", "/api/panel/tokens/a%20b/bind", `{}`,
			`{"success":false,"error":"Invalid tokenId"}`, nil},

		{"mode", "GET", "/api/panel/enrollment/mode", "",
			`{"success":true,"data":{"mode":"open","description":"d"}}`, []string{"GET /api/enrollment/mode"}},
		{"set mode sends the mode alone", "PUT", "/api/panel/enrollment/mode", `{"mode":"managed","other":1}`,
			`{"success":true,"data":{"success":true,"mode":"managed","description":"m"}}`, []string{`PUT /api/enrollment/mode {"mode":"managed"}`}},
		{"set mode sends another type as it is", "PUT", "/api/panel/enrollment/mode", `{"mode":["locked"]}`,
			`{"success":true,"data":{"success":true,"mode":"managed","description":"m"}}`, []string{`PUT /api/enrollment/mode {"mode":["locked"]}`}},
		{"set mode without a mode", "PUT", "/api/panel/enrollment/mode", `{}`,
			`{"success":true,"data":{"success":true,"mode":"managed","description":"m"}}`, []string{`PUT /api/enrollment/mode {"mode":null}`}},
	} {
		got := admin.Do(tc.method, tc.target, tc.body)
		status := http.StatusOK
		if s, ok := statuses[tc.name]; ok {
			status = s
		} else if strings.Contains(tc.want, `"error":"Invalid `) {
			status = http.StatusBadRequest
		}
		if got.Status != status || got.Body != tc.want {
			t.Errorf("%s: %d %s\nwant %d %s", tc.name, got.Status, got.Body, status, tc.want)
		}
		if got.Header.Get("Content-Type") != "application/json; charset=utf-8" {
			t.Errorf("%s: Content-Type %q", tc.name, got.Header.Get("Content-Type"))
		}
		if calls := api.Calls(); !reflect.DeepEqual(calls, tc.calls) {
			t.Errorf("%s: the API server was asked %q, want %q", tc.name, calls, tc.calls)
		}
	}
}

// TestARefusalKeepsTheAPIServersStatus: what the API server refuses comes
// back with its status and its reason, as text or as the error of its
// JSON, for the tokens and the enrollment mode alike.
func TestARefusalKeepsTheAPIServersStatus(t *testing.T) {
	api := routetest.NewAPI()
	api.OnText("POST /api/tokens/9/bind", 409, "Token already bound to different peer\n")
	api.OnText("PUT /api/enrollment/mode", 400, "Invalid mode (open, managed, locked)\n")
	api.On("POST /api/tokens", 403, `{"error":"Not allowed for this role"}`)
	api.On("GET /api/enrollment/mode", 500, `{}`)
	admin := routetest.New(t, routetest.Options{API: api}).As("admin")
	for _, tc := range []struct {
		method, target, body string
		status               int
		want                 string
	}{
		{"POST", "/api/panel/tokens/9/bind", `{"peer_id":"900100001"}`, 409, `{"success":false,"error":"Token already bound to different peer"}`},
		{"PUT", "/api/panel/enrollment/mode", `{"mode":"closed"}`, 400, `{"success":false,"error":"Invalid mode (open, managed, locked)"}`},
		{"POST", "/api/panel/tokens", `{"name":"x"}`, 403, `{"success":false,"error":"Not allowed for this role"}`},
		{"GET", "/api/panel/enrollment/mode", "", 500, `{"success":false,"error":"Request failed with status code 500"}`},
	} {
		if got := admin.Do(tc.method, tc.target, tc.body); got.Status != tc.status || got.Body != tc.want {
			t.Errorf("%s %s: %d %s, want %d %s", tc.method, tc.target, got.Status, got.Body, tc.status, tc.want)
		}
	}
}
