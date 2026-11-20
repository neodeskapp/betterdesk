package cdap_test

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/goapipath/proxytest"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/restart"
	"github.com/unitronix/betterdesk-server/console/routes/cdap"
)

// vector is a proxytest vector with what the vectors of this group add:
// the restart transaction file after a request to the gateway switch.
type vector struct {
	proxytest.Vector
	State *string `json:"state"`
}

// The id of a transaction and its times are those of the run.
var (
	transactionID = regexp.MustCompile(`"id": ?"[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}"`)
	nowISO        = regexp.MustCompile(`"(createdAt|updatedAt)": ?"20[0-9]{2}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}\.[0-9]{3}Z"`)
)

func normalize(text string) string {
	text = transactionID.ReplaceAllString(text, `"id":"<id>"`)
	return nowISO.ReplaceAllString(text, `"$1":"<now>"`)
}

func describe(text *string) string {
	if text == nil {
		return "(no file)"
	}
	return normalize(*text)
}

// TestVectors replays what routes/cdap.routes.js of the Node console
// answered, asked the API server and wrote into the restart transaction
// file (testdata/nodeoracle/run.js).
func TestVectors(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "vectors.json"))
	if err != nil {
		t.Fatal(err)
	}
	var vectors []vector
	if err := json.Unmarshal(raw, &vectors); err != nil {
		t.Fatal(err)
	}
	if len(vectors) < 1900 {
		t.Fatalf("%d vectors, the file should hold more than 1900", len(vectors))
	}

	dataDir := filepath.Join(t.TempDir(), "data")
	statePath := filepath.Join(dataDir, ".restart-transaction.json")
	// The device layer knows dev-1 and keeps it outside the scope of the
	// viewer and the operator
	scoped := withScope(t, nil)
	w := proxytest.NewWorld(t, func(d *kit.Deps) {
		d.Restart = restart.New(restart.Options{DataDir: dataDir, GoAPI: d.GoAPI})
		scoped(d)
	})

	failures, spelledDifferently, fixed := 0, 0, map[string]int{}
	for i, v := range vectors {
		for bug, changes := range fixes {
			if changes(&v) {
				fixed[bug]++
			}
		}
		if v.Request.Method == "STATE" {
			if v.Request.Body == nil {
				if err := os.Remove(statePath); err != nil && !os.IsNotExist(err) {
					t.Fatal(err)
				}
				continue
			}
			if err := os.MkdirAll(dataDir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(statePath, []byte(*v.Request.Body), 0o600); err != nil {
				t.Fatal(err)
			}
			continue
		}
		rec, calls := w.Do(v.Request, v.API)
		var problems []string
		if rec.Code != v.Status {
			problems = append(problems, "status "+strconv.Itoa(rec.Code)+", want "+strconv.Itoa(v.Status))
		}
		if got := rec.Header().Get("Content-Type"); got != v.Type {
			problems = append(problems, "Content-Type "+strconv.Quote(got)+", want "+strconv.Quote(v.Type))
		}
		if got, want := normalize(rec.Body.String()), normalize(v.Body); got != want {
			problems = append(problems, "body "+strconv.Quote(got)+"\n      want "+strconv.Quote(want))
		}
		if got, want := describeCalls(calls), describeCalls(v.Calls); got != want {
			problems = append(problems, "API calls "+got+"\n      want "+want)
		} else if len(calls) > 0 && calls[0].URL != v.Calls[0].URL {
			spelledDifferently++
		}
		if strings.HasPrefix(strings.ToLower(v.Request.Path), "/api/cdap/toggle") {
			var have *string
			if file, err := os.ReadFile(statePath); err == nil {
				text := string(file)
				have = &text
			}
			if describe(have) != describe(v.State) {
				problems = append(problems, "transaction file:\n"+describe(have)+"\n      want:\n"+describe(v.State))
			}
		}
		if len(problems) > 0 {
			failures++
			if failures <= 20 {
				body := ""
				if v.Request.Body != nil {
					body = " " + *v.Request.Body
				}
				api, _ := json.Marshal(v.API)
				t.Errorf("vector %d: %s %s%s (API answers %s):\n  %s", i, v.Request.Method, v.Request.Path, body, api, strings.Join(problems, "\n  "))
			}
		}
	}
	if failures > 20 {
		t.Errorf("%d of %d vectors differ; the first 20 are shown", failures, len(vectors))
	}
	// Only hand-made queries of the alerts route are spelled differently
	if spelledDifferently > 10 {
		t.Errorf("%d calls reached the API server in another spelling than the Node console's", spelledDifferently)
	}
	t.Logf("%d vectors, %d of them with a query for the API server spelled differently", len(vectors), spelledDifferently)
	for bug := range fixes {
		if fixed[bug] == 0 {
			t.Errorf("the fix of %s changes no vector", bug)
		}
		t.Logf("%d vectors changed by the fix of %s", fixed[bug], bug)
	}
}

// fixes are the changes a bug fix made on purpose to what the Node
// console answered. Each turns a vector it changes into the Go console's
// answer and reports that it did.
var fixes = map[string]func(v *vector) bool{
	// The routes of one device check the device scope after the sign-in
	// and the permission: a viewer's or an operator's request for dev-1
	// that Node let through is refused with 403 before the API server is
	// asked.
	"RC8": func(v *vector) bool {
		session := v.Request.Session
		if session == nil || (session.User.Role != "viewer" && session.User.Role != "operator") ||
			v.Status == 401 || v.Status == 403 || !deviceRoute.MatchString(v.Request.Path) {
			return false
		}
		v.Status, v.Type, v.Body, v.Calls = 403, "application/json; charset=utf-8", `{"success":false,"error":"errors.forbidden"}`, nil
		return true
	},
	// The gateway switch joins the restart transaction before it saves:
	// where Node saved the setting and then failed to toggle, the console
	// reads the setting, is refused and saves nothing. A pending
	// transaction that is not the operator's is a 409.
	"RC4": func(v *vector) bool {
		if !strings.HasPrefix(strings.ToLower(v.Request.Path), "/api/cdap/toggle") || v.Body != `{"success":false,"error":"Failed to toggle CDAP"}` {
			return false
		}
		v.Calls = v.Calls[:1]
		if foreignPending(v.State, v.Request.Session.UserID) {
			v.Status = 409
		}
		return true
	},
}

// foreignPending reports whether a transaction file holds a pending
// transaction that is not the account's: its userId is not the account's
// id as text.
func foreignPending(state *string, userID int64) bool {
	if state == nil {
		return false
	}
	var parsed struct {
		Phase  string `json:"phase"`
		UserID any    `json:"userId"`
	}
	if json.Unmarshal([]byte(*state), &parsed) != nil || parsed.Phase != "pending" {
		return false
	}
	owner, isText := parsed.UserID.(string)
	return !isText || owner != strconv.FormatInt(userID, 10)
}

// deviceRoute is the address of a route of the device dev-1.
var deviceRoute = regexp.MustCompile(`(?i)^/api/cdap/devices/dev-1(/(manifest|state|linked|command|link))?/?(\?.*)?$`)

var listIndex = regexp.MustCompile(`\[[0-9]+\]`)

// describeCalls writes the calls the API server got, with the query of
// each as the API server reads it. The client in package goapi spells two
// things differently from the Node console's client, which change nothing
// for the API server: it escapes a comma, and it writes a list that holds
// an object as name[] where the Node client wrote name[0].
func describeCalls(calls []proxytest.Call) string {
	out := make([]proxytest.Call, len(calls))
	for i, call := range calls {
		out[i] = call
		path, query, hasQuery := strings.Cut(call.URL, "?")
		if !hasQuery {
			continue
		}
		var pairs []string
		for _, pair := range strings.Split(query, "&") {
			name, value, _ := strings.Cut(pair, "=")
			name, _ = url.QueryUnescape(name)
			value, _ = url.QueryUnescape(value)
			pairs = append(pairs, strconv.Quote(listIndex.ReplaceAllString(name, "[]"))+"="+strconv.Quote(value))
		}
		out[i].URL = path + "?" + strings.Join(pairs, "&")
	}
	text, _ := json.Marshal(out)
	return string(text)
}

func TestRegisteredRoutes(t *testing.T) {
	d := &kit.Deps{Router: kit.NewRouter(), Log: kit.NewLogger("error", true)}
	cdap.Register(d)
	var got []string
	for _, route := range d.Router.Routes() {
		got = append(got, route.Method+" "+route.Path)
	}
	want := []string{
		"GET /api/cdap/status",
		"GET /api/cdap/devices",
		"GET /api/cdap/devices/{id}",
		"GET /api/cdap/devices/{id}/manifest",
		"GET /api/cdap/devices/{id}/state",
		"POST /api/cdap/devices/{id}/command",
		"POST /api/cdap/toggle",
		"GET /api/cdap/alerts",
		"GET /api/cdap/devices/{id}/linked",
		"POST /api/cdap/devices/{id}/link",
	}
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("registered routes:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
