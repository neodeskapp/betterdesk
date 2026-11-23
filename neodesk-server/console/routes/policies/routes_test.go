package policies_test

import (
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/goapipath/proxytest"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/policies"
)

// register mounts the group the way console/routes.go does.
func register(d *kit.Deps) {
	policies.Register(d)
	policies.Register(d.At("/api/nd"))
}

// fixes are the vectors of vectors.json whose answer a bug fix of
// BUGS.md changed on purpose (proxytest.Replay).
var fixes = slices.Concat(proxytest.Fixes("FL4",
	// A device id with two dots in it is refused with 400 before any call.
	818, 819,
), proxytest.Fixes("FL1",
	// A limit or an offset that is no single text is refused with 400
	// before any call.
	891, 892, 893, 894, 895, 896, 897, 898, 984, 1011, 1090, 1126, 1261, 1296, 1361,
	1385, 1390,
), proxytest.Fixes("FL3",
	// An organization or device id that cannot go into a path, one with two
	// dots in it as well (FL4), is refused with 400 and the check's text.
	422, 423, 432, 433, 436, 437, 438, 439, 440, 441, 442, 443, 444, 445, 456,
	457, 458, 459, 460, 461, 466, 467, 476, 477, 480, 481, 482, 483, 484, 485,
	486, 487, 488, 489, 500, 501, 502, 503, 504, 505, 510, 511, 520, 521, 524,
	525, 526, 527, 528, 529, 530, 531, 532, 533, 544, 545, 546, 547, 548, 549,
	554, 555, 564, 565, 568, 569, 570, 571, 572, 573, 574, 575, 576, 577, 588,
	589, 590, 591, 592, 593, 598, 599, 608, 609, 612, 613, 614, 615, 616, 617,
	618, 619, 620, 621, 632, 633, 634, 635, 636, 637, 642, 643, 652, 653, 656,
	657, 658, 659, 660, 661, 662, 663, 664, 665, 676, 677, 678, 679, 680, 681,
	688, 689, 690, 691, 708, 709, 710, 711, 716, 717, 718, 719, 720, 721, 722,
	723, 724, 725, 726, 727, 728, 729, 730, 731, 732, 733, 734, 735, 756, 757,
	758, 759, 760, 761, 762, 763, 764, 765, 766, 767, 774, 775, 784, 785, 788,
	789, 790, 791, 792, 793, 794, 795, 796, 797, 808, 809, 810, 811, 812, 813,
	860,
), proxytest.Fixes("FL5",
	// Only the organization's own policies are 200 {} when the API server
	// answers 404; the effective policy of a device named policy is not.
	743,
), proxytest.Fixes("FL8",
	// The attestation limit goes on held to 1 to 200, the offset held to 0 to
	// 2^53-1, both as plain digits.
	879, 899, 900, 901, 902, 904, 905, 906,
), proxytest.Fixes("FL9",
	// The policy writes check the session first: signed out is 401 with
	// session_required, a pro account is refused as on the other routes.
	16, 17, 24, 25, 32, 33, 40, 41, 48, 49, 56, 57, 64, 65, 72, 73, 80, 81, 88, 89,
), proxytest.Fixes("FL6",
	// The body goes to the API server with every member, also __proto__,
	// constructor and prototype.
	868,
), proxytest.Fixes("RV68",
	// A server admin, who may not edit organizations, writes no policies:
	// 403 for org.edit before any call.
	20, 28, 36, 44, 52, 60, 68, 76, 84, 92,
))

// TestVectors replays what routes/policies.routes.js of the Node console
// answered and asked the API server (testdata/vectors.json).
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
		"GET /api/nd/device-policy",
		"POST /api/nd/attestation",
	}
	var want []string
	for _, route := range routes {
		method, pattern, _ := strings.Cut(route, " ")
		want = append(want, route, method+" /api/nd"+pattern)
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

// TestBadIDsAre400: an organization or device id that cannot go into a
// path is refused with 400 and the check's text before any call, as the
// fleet and resource control routes refuse it (BUGS.md FL3).
func TestBadIDsAre400(t *testing.T) {
	w := proxytest.NewWorld(t, register)
	policy := &proxytest.Answer{Status: 200, Type: "application/json", Body: `{"ok":true}`}
	body := `{"mode":"attended"}`
	for _, c := range []struct {
		method, path, want string
	}{
		{"GET", "/api/panel/policies/bad%20id", "Invalid orgId"},
		{"GET", "/api/panel/policies/a..b", "Invalid orgId"},
		{"GET", "/api/panel/policies/bad%20id/audit", "Invalid orgId"},
		{"GET", "/api/panel/policies/bad%20id/effective/900100001", "Invalid orgId"},
		{"GET", "/api/panel/policies/org-1/effective/bad%20id", "Invalid deviceId"},
		{"GET", "/api/panel/policies/bad%20id/effective/bad%20id", "Invalid deviceId"},
		{"PUT", "/api/panel/policies/bad%20id/connection", "Invalid orgId"},
		{"PUT", "/api/panel/policies/%2E%2E/security", "Invalid orgId"},
		{"GET", "/api/nd/api/panel/policies/bad%20id", "Invalid orgId"},
	} {
		req := proxytest.Request{Method: c.method, Path: c.path, Session: adminSession()}
		if c.method == "PUT" {
			req.Type, req.Body = "application/json", &body
		}
		rec, calls := w.Do(req, policy)
		if rec.Code != 400 || rec.Body.String() != `{"error":"`+c.want+`"}` || len(calls) != 0 {
			t.Errorf("%s %s: %d %s after %d calls, want 400 %s", c.method, c.path, rec.Code, rec.Body, len(calls), c.want)
		}
	}
}

// TestEffectivePolicyOfADeviceNamedPolicy: only the organization's own
// policies answer 200 {} when the API server has no policy routes; the
// effective policy of a device named "policy" gets the API server's 404
// like any other device (BUGS.md FL5).
func TestEffectivePolicyOfADeviceNamedPolicy(t *testing.T) {
	w := proxytest.NewWorld(t, register)
	missing := &proxytest.Answer{Status: 404, Type: "application/json", Body: `{"error":"not found"}`}
	for path, want := range map[string]string{
		"/api/panel/policies/org-1/effective/policy":  `404 {"error":"not found"}`,
		"/api/panel/policies/org-1/effective/dev-1":   `404 {"error":"not found"}`,
		"/api/panel/policies/policy/effective/policy": `404 {"error":"not found"}`,
		"/api/panel/policies/org-1/audit":             `404 {"error":"not found"}`,
		"/api/panel/policies/org-1":                   `200 {}`,
		"/api/panel/policies/policy":                  `200 {}`,
	} {
		rec, calls := w.Do(proxytest.Request{Path: path, Session: adminSession()}, missing)
		if got := strconv.Itoa(rec.Code) + " " + rec.Body.String(); got != want || len(calls) != 1 {
			t.Errorf("GET %s: %s after %d calls, want %s", path, got, len(calls), want)
		}
	}
}

// TestAttestationPagingIsClamped: limit goes on as a whole number from 1
// to 200 and offset as one from 0, both written as plain digits (BUGS.md
// FL8).
func TestAttestationPagingIsClamped(t *testing.T) {
	w := proxytest.NewWorld(t, register)
	records := &proxytest.Answer{Status: 200, Type: "application/json", Body: `{"records":[],"total":0}`}
	for query, sent := range map[string]string{
		"limit=-5":                           "limit=1&offset=0",
		"limit=-0":                           "limit=50&offset=0",
		"limit=500&offset=20":                "limit=200&offset=20",
		"limit=7.9&offset=3.5":               "limit=7&offset=3",
		"offset=-3":                          "limit=50&offset=0",
		"offset=1234567890123456789012345":   "limit=50&offset=9007199254740991",
		"limit=1e3&offset=99999999999999999": "limit=1&offset=9007199254740991",
		"offset=9007199254740990":            "limit=50&offset=9007199254740990",
	} {
		rec, calls := w.Do(proxytest.Request{Path: "/api/panel/attestation?" + query, Session: adminSession()}, records)
		if rec.Code != 200 || len(calls) != 1 || calls[0].URL != "/api/attestation?"+sent {
			t.Errorf("?%s: %d after %+v, want a call with %s", query, rec.Code, calls, sent)
		}
	}
}

// TestPolicyWritesNeedASession: the five policy writes check the session
// first, as the group's other routes do: signed out is 401 with the code
// the web app looks for, a pro account is refused as everywhere else in
// the console (BUGS.md FL9).
func TestPolicyWritesNeedASession(t *testing.T) {
	w := proxytest.NewWorld(t, register)
	ok := &proxytest.Answer{Status: 200, Type: "application/json", Body: `{"ok":true}`}
	body := `{"mode":"attended"}`
	pro := &proxytest.Account{UserID: 8}
	pro.User.Username, pro.User.Role = "e2e_pro", "pro"
	for _, category := range []string{"connection", "features", "security", "network", "update"} {
		for _, prefix := range []string{"", "/api/nd"} {
			path := prefix + "/api/panel/policies/org-1/" + category
			rec, calls := w.Do(proxytest.Request{Method: "PUT", Path: path, Type: "application/json", Body: &body}, ok)
			if rec.Code != 401 || rec.Body.String() != `{"success":false,"error":"Unauthorized. Please log in.","code":"session_required"}` || len(calls) != 0 {
				t.Errorf("PUT %s signed out: %d %s after %d calls", path, rec.Code, rec.Body, len(calls))
			}
			rec, calls = w.Do(proxytest.Request{Method: "PUT", Path: path, Type: "application/json", Body: &body, Session: pro}, ok)
			if rec.Code != 403 || rec.Body.String() != `{"success":false,"error":"Pro accounts can only access the RustDesk desktop client API"}` || len(calls) != 0 {
				t.Errorf("PUT %s as pro: %d %s after %d calls", path, rec.Code, rec.Body, len(calls))
			}
		}
	}
}

// TestPolicyWritesNeedOrgEdit: the five policy writes ask org.edit, which
// the API server asks for the same write: a server admin is refused before
// any call and still reads the policies; a global admin writes them (BUGS
// RV68, A83).
func TestPolicyWritesNeedOrgEdit(t *testing.T) {
	w := proxytest.NewWorld(t, register)
	ok := &proxytest.Answer{Status: 200, Type: "application/json", Body: `{"ok":true}`}
	body := `{"mode":"attended"}`
	account := func(id int64, role string) *proxytest.Account {
		a := &proxytest.Account{UserID: id}
		a.User.Username, a.User.Role = "e2e_"+role, role
		return a
	}
	serverAdmin, globalAdmin := account(3, "server_admin"), account(4, "global_admin")
	for _, category := range []string{"connection", "features", "security", "network", "update"} {
		path := "/api/panel/policies/org-1/" + category
		rec, calls := w.Do(proxytest.Request{Method: "PUT", Path: path, Type: "application/json", Body: &body, Session: serverAdmin}, ok)
		if rec.Code != 403 || rec.Body.String() != `{"success":false,"error":"Permission denied: org.edit"}` || len(calls) != 0 {
			t.Errorf("PUT %s as server_admin: %d %s after %d calls", path, rec.Code, rec.Body, len(calls))
		}
		rec, calls = w.Do(proxytest.Request{Method: "PUT", Path: path, Type: "application/json", Body: &body, Session: globalAdmin}, ok)
		if rec.Code != 200 || len(calls) != 1 {
			t.Errorf("PUT %s as global_admin: %d %s after %d calls", path, rec.Code, rec.Body, len(calls))
		}
	}
	if rec, calls := w.Do(proxytest.Request{Method: "GET", Path: "/api/panel/policies/org-1", Session: serverAdmin}, ok); rec.Code != 200 || len(calls) != 1 {
		t.Errorf("GET as server_admin: %d %s after %d calls", rec.Code, rec.Body, len(calls))
	}
}
