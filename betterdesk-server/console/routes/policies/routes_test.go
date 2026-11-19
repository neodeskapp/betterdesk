package policies_test

import (
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/goapipath/proxytest"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/routes/policies"
)

// register mounts the group the way console/routes.go does.
func register(d *kit.Deps) {
	policies.Register(d)
	policies.Register(d.At("/api/bd"))
}

// fixes are the vectors of vectors.json whose answer a bug fix of
// BUGS.md changed on purpose (proxytest.Replay).
var fixes = append(proxytest.Fixes("FL4",
	// An id with two dots in it is refused by the route's own check, before
	// any call.
	422, 423, 466, 467, 510, 511, 554, 555, 598, 599, 642, 643, 688, 689, 690, 691,
	774, 775, 818, 819,
), proxytest.Fixes("FL1",
	// A limit or an offset that is no single text is refused with 400
	// before any call.
	891, 892, 893, 894, 895, 896, 897, 898, 984, 1011, 1090, 1126, 1261, 1296, 1361,
	1385, 1390,
)...)

// TestVectors replays what routes/policies.routes.js of the Node console
// answered and asked the API server (testdata/nodeoracle/run.js).
func TestVectors(t *testing.T) {
	proxytest.Replay(t, filepath.Join("testdata", "vectors.json"), register, fixes...)
}

func TestRegisteredRoutes(t *testing.T) {
	d := &kit.Deps{Router: kit.NewRouter(), Log: kit.NewLogger("error", true)}
	register(d)
	var got []string
	for _, route := range d.Router.Routes() {
		got = append(got, route.Method+" "+route.Path)
	}
	sort.Strings(got)
	routes := []string{
		"GET /api/panel/policies/{orgId}",
		"PUT /api/panel/policies/{orgId}/connection",
		"PUT /api/panel/policies/{orgId}/features",
		"PUT /api/panel/policies/{orgId}/security",
		"PUT /api/panel/policies/{orgId}/network",
		"PUT /api/panel/policies/{orgId}/update",
		"GET /api/panel/policies/{orgId}/effective/{deviceId}",
		"GET /api/panel/policies/{orgId}/audit",
		"GET /api/panel/attestation",
		"GET /api/panel/attestation/{deviceId}",
		"GET /api/bd/device-policy",
		"POST /api/bd/attestation",
	}
	var want []string
	for _, route := range routes {
		method, pattern, _ := strings.Cut(route, " ")
		want = append(want, route, method+" /api/bd"+pattern)
	}
	sort.Strings(want)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("registered routes:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// adminSession is a signed-in admin for proxytest requests.
func adminSession() *proxytest.Account {
	a := &proxytest.Account{UserID: 1}
	a.User.Username, a.User.Role = "admin", "admin"
	return a
}

// TestAttestationPagingThatIsNoText: limit and offset are each read from
// one text. A list or an object is refused with 400 before any call, where
// Go answered 500 and the old console ended (BUGS.md FL1).
func TestAttestationPagingThatIsNoText(t *testing.T) {
	w := proxytest.NewWorld(t, register)
	records := &proxytest.Answer{Status: 200, Type: "application/json", Body: `{"records":[],"total":0}`}
	for query, want := range map[string]string{
		"limit[toString]=1":         `{"error":"Invalid limit"}`,
		"limit[]=1":                 `{"error":"Invalid limit"}`,
		"limit=1&limit=2":           `{"error":"Invalid limit"}`,
		"limit[x]=1&offset=2":       `{"error":"Invalid limit"}`,
		"offset[toString]=1":        `{"error":"Invalid offset"}`,
		"limit=5&offset[]=1":        `{"error":"Invalid offset"}`,
		"limit=abc&offset[x][y]=1":  `{"error":"Invalid offset"}`,
		"limit[toString]=1&offset=": `{"error":"Invalid limit"}`,
	} {
		rec, calls := w.Do(proxytest.Request{Path: "/api/panel/attestation?" + query, Session: adminSession()}, records)
		if rec.Code != 400 || rec.Body.String() != want || len(calls) != 0 {
			t.Errorf("?%s: %d %s after %d calls, want 400 %s", query, rec.Code, rec.Body, len(calls), want)
		}
	}
	for query, sent := range map[string]string{
		"":                  "/api/attestation?limit=50&offset=0",
		"limit=&offset=":    "/api/attestation?limit=50&offset=0",
		"limit=20&offset=5": "/api/attestation?limit=20&offset=5",
		"limit=abc":         "/api/attestation?limit=50&offset=0",
	} {
		rec, calls := w.Do(proxytest.Request{Path: "/api/panel/attestation?" + query, Session: adminSession()}, records)
		if rec.Code != 200 || len(calls) != 1 || calls[0].URL != sent {
			t.Errorf("?%s: %d %s after %+v, want a call to %s", query, rec.Code, rec.Body, calls, sent)
		}
	}
}
