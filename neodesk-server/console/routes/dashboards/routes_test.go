package dashboards_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/routetest"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

const nothing = `{"own":null,"shared":[],"canPublish":false}`

func data(t *testing.T, res routetest.Answer) string {
	t.Helper()
	if res.Status != http.StatusOK {
		t.Fatalf("status %d: %s", res.Status, res.Body)
	}
	var out struct {
		Success bool            `json:"success"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal([]byte(res.Body), &out); err != nil || !out.Success {
		t.Fatalf("answer %s (%v)", res.Body, err)
	}
	return string(out.Data)
}

func errorOf(t *testing.T, res routetest.Answer) string {
	t.Helper()
	var out struct {
		Success bool   `json:"success"`
		Error   string `json:"error"`
	}
	if err := json.Unmarshal([]byte(res.Body), &out); err != nil || out.Success {
		t.Fatalf("answer %s (%v)", res.Body, err)
	}
	return out.Error
}

// Every account keeps its own boards: none until it saves, the canonical
// layout once it did (names and titles trimmed, an empty config and title
// left out, hidden always a list), and one account's boards are not
// another's. Reset goes back to none.
func TestEveryAccountKeepsItsOwnBoards(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := routetest.New(t, routetest.Options{DB: db})
		for _, role := range []string{"guest_issuer", "viewer", "operator"} {
			a := w.As(role)
			if got := data(t, a.Do("GET", "/api/dashboards", "")); got != nothing {
				t.Errorf("%s before saving: %s", role, got)
			}
			body := `{"boards":[{"id":"main","name":"  Main ` + role + ` ","widgets":[` +
				`{"id":"w1","type":"device-status","size":"m","title":"  ","config":{}},` +
				`{"id":"w2","type":"alerts","size":"full","title":" Open alerts ","config":{"limit": 5, "group":"a"}}]}],` +
				`"active":"main"}`
			want := `{"boards":[{"id":"main","name":"Main ` + role + `","widgets":[` +
				`{"id":"w1","type":"device-status","size":"m"},` +
				`{"id":"w2","type":"alerts","size":"full","title":"Open alerts","config":{"limit":5,"group":"a"}}]}],` +
				`"active":"main","hidden":[]}`
			if got := data(t, a.Do("PUT", "/api/dashboards", body)); got != want {
				t.Errorf("%s saved:\n got %s\nwant %s", role, got, want)
			}
			if got := data(t, a.Do("GET", "/api/dashboards", "")); got != `{"own":`+want+`,"shared":[],"canPublish":false}` {
				t.Errorf("%s read back: %s", role, got)
			}
		}
		other := w.As("admin")
		if got := data(t, other.Do("GET", "/api/dashboards", "")); !strings.HasPrefix(got, `{"own":null,`) {
			t.Errorf("another account sees the boards: %s", got)
		}

		v := w.As("viewer")
		if got := data(t, v.Do("DELETE", "/api/dashboards", "")); got != `{"own":null}` {
			t.Errorf("reset: %s", got)
		}
		if got := data(t, v.Do("GET", "/api/dashboards", "")); got != nothing {
			t.Errorf("after the reset: %s", got)
		}
		if got := data(t, w.As("operator").Do("GET", "/api/dashboards", "")); !strings.Contains(got, `"name":"Main operator"`) {
			t.Errorf("the reset of one account took another's: %s", got)
		}
	})
}

// A layout or shared boards that break a rule are refused with a sentence
// naming what is wrong, and nothing is stored.
func TestRefusedLayouts(t *testing.T) {
	w := routetest.New(t, routetest.Options{})
	a := w.As("admin")
	saved := `{"boards":[{"id":"keep","name":"Keep","widgets":[]}],"active":"","hidden":[]}`
	data(t, a.Do("PUT", "/api/dashboards", saved))
	data(t, a.Do("PUT", "/api/dashboards/shared", `{"boards":[{"id":"team","name":"Team","widgets":[]}]}`))
	sharedWant := `[{"id":"team","name":"Team","widgets":[],"roles":[],"locked":false,"home":false}]`

	board := func(widgets string) string {
		return `{"boards":[{"id":"b","name":"B","widgets":[` + widgets + `]}]}`
	}
	repeat := func(n int, f func(i int) string) string {
		parts := make([]string, n)
		for i := range parts {
			parts[i] = f(i)
		}
		return strings.Join(parts, ",")
	}
	id := func(i int) string {
		return "x" + strings.Repeat("a", i%3) + string(rune('a'+i%26)) + string(rune('a'+i/26))
	}
	long := strings.Repeat("é", 81)
	cases := []struct {
		name, path, body, says string
	}{
		{"not an object", "/api/dashboards", `[]`, "must be a JSON object"},
		{"no boards", "/api/dashboards", `{}`, `needs "boards"`},
		{"boards null", "/api/dashboards", `{"boards":null}`, `needs "boards"`},
		{"unknown key", "/api/dashboards", `{"boards":[],"layout":1}`, `unknown key "layout"`},
		{"unknown board key", "/api/dashboards", `{"boards":[{"id":"b","name":"B","widgets":[],"roles":[]}]}`, `unknown key "roles"`},
		{"unknown widget key", "/api/dashboards", board(`{"id":"w","type":"t","size":"s","x":1}`), `unknown key "x"`},
		{"too many boards", "/api/dashboards", `{"boards":[` + repeat(21, func(i int) string { return `{"id":"` + id(i) + `","name":"B","widgets":[]}` }) + `]}`, "more than 20 boards"},
		{"bad board id", "/api/dashboards", `{"boards":[{"id":"Main","name":"B","widgets":[]}]}`, `the id "Main"`},
		{"board id too long", "/api/dashboards", `{"boards":[{"id":"` + strings.Repeat("a", 41) + `","name":"B","widgets":[]}]}`, "the id"},
		{"board id a number", "/api/dashboards", `{"boards":[{"id":1,"name":"B","widgets":[]}]}`, `"id" must be a string`},
		{"two boards one id", "/api/dashboards", `{"boards":[{"id":"b","name":"B","widgets":[]},{"id":"b","name":"C","widgets":[]}]}`, `two boards with the id "b"`},
		{"empty name", "/api/dashboards", `{"boards":[{"id":"b","name":"   ","widgets":[]}]}`, "needs a name"},
		{"long name", "/api/dashboards", `{"boards":[{"id":"b","name":"` + long + `","widgets":[]}]}`, "longer than 80 characters"},
		{"widgets null", "/api/dashboards", `{"boards":[{"id":"b","name":"B","widgets":null}]}`, `needs "widgets"`},
		{"too many widgets", "/api/dashboards", board(repeat(41, func(i int) string { return `{"id":"` + id(i) + `","type":"t","size":"s"}` })), "more than 40 widgets"},
		{"bad widget id", "/api/dashboards", board(`{"id":"-w","type":"t","size":"s"}`), `the id "-w"`},
		{"two widgets one id", "/api/dashboards", board(`{"id":"w","type":"t","size":"s"},{"id":"w","type":"u","size":"s"}`), `two widgets with the id "w"`},
		{"bad type", "/api/dashboards", board(`{"id":"w","type":"1chart","size":"s"}`), `the type "1chart"`},
		{"bad size", "/api/dashboards", board(`{"id":"w","type":"t","size":"xl"}`), `the size "xl"`},
		{"size null", "/api/dashboards", board(`{"id":"w","type":"t","size":null}`), `needs "size"`},
		{"long title", "/api/dashboards", board(`{"id":"w","type":"t","size":"s","title":"` + long + `"}`), "longer than 80 characters"},
		{"config not an object", "/api/dashboards", board(`{"id":"w","type":"t","size":"s","config":[1]}`), "config must be a JSON object"},
		{"config too large", "/api/dashboards", board(`{"id":"w","type":"t","size":"s","config":{"a":"` + strings.Repeat("x", 8190) + `"}}`), "larger than 8192 bytes"},
		{"bad active", "/api/dashboards", `{"boards":[],"active":"Main"}`, `the id "Main"`},
		{"too many hidden", "/api/dashboards", `{"boards":[],"hidden":[` + repeat(51, func(i int) string { return `"` + id(i) + `"` }) + `]}`, "more than 50 boards"},
		{"bad hidden", "/api/dashboards", `{"boards":[],"hidden":["ok","Not ok"]}`, `the id "Not ok"`},
		{"hidden not strings", "/api/dashboards", `{"boards":[],"hidden":[1]}`, "list of strings"},
		{"body too large", "/api/dashboards", `{"boards":[],"active":"","hidden":[],"pad":"` + strings.Repeat("x", 256<<10) + `"}`, "larger than 256 KB"},
		{"shared unknown key", "/api/dashboards/shared", `{"boards":[],"active":""}`, `unknown key "active"`},
		{"shared too many", "/api/dashboards/shared", `{"boards":[` + repeat(21, func(i int) string { return `{"id":"` + id(i) + `","name":"B","widgets":[]}` }) + `]}`, "more than 20 shared boards"},
		{"shared two ids", "/api/dashboards/shared", `{"boards":[{"id":"b","name":"B","widgets":[]},{"id":"b","name":"C","widgets":[]}]}`, `two shared boards with the id "b"`},
		{"too many roles", "/api/dashboards/shared", `{"boards":[{"id":"b","name":"B","widgets":[],"roles":[` + repeat(21, func(i int) string { return `"r` + id(i) + `"` }) + `]}]}`, "more than 20 roles"},
		{"empty role", "/api/dashboards/shared", `{"boards":[{"id":"b","name":"B","widgets":[],"roles":[""]}]}`, "1 to 40 characters"},
		{"long role", "/api/dashboards/shared", `{"boards":[{"id":"b","name":"B","widgets":[],"roles":["` + strings.Repeat("r", 41) + `"]}]}`, "1 to 40 characters"},
		{"locked not a flag", "/api/dashboards/shared", `{"boards":[{"id":"b","name":"B","widgets":[],"locked":"yes"}]}`, "true or false"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := a.Do("PUT", c.path, c.body)
			if res.Status != http.StatusBadRequest {
				t.Fatalf("%d %s", res.Status, res.Body)
			}
			if msg := errorOf(t, res); !strings.Contains(msg, c.says) {
				t.Errorf("error %q does not say %q", msg, c.says)
			}
		})
	}
	got := data(t, a.Do("GET", "/api/dashboards", ""))
	if want := `{"own":` + saved + `,"shared":` + sharedWant + `,"canPublish":true}`; got != want {
		t.Errorf("after the refused changes:\n got %s\nwant %s", got, want)
	}

	// The limits themselves are allowed
	full := `{"boards":[` + repeat(20, func(i int) string {
		return `{"id":"` + id(i) + `","name":"` + strings.Repeat("é", 80) + `","widgets":[]}`
	}) + `]}`
	if res := a.Do("PUT", "/api/dashboards", full); res.Status != http.StatusOK {
		t.Errorf("20 boards with names of 80 characters: %d %s", res.Status, res.Body)
	}
	if res := a.Do("PUT", "/api/dashboards", board(repeat(40, func(i int) string { return `{"id":"` + id(i) + `","type":"t","size":"l"}` }))); res.Status != http.StatusOK {
		t.Errorf("40 widgets: %d %s", res.Status, res.Body)
	}

	out := w.SignedOut()
	if res := out.Do("GET", "/api/dashboards", ""); res.Status != http.StatusUnauthorized {
		t.Errorf("signed out GET: %d %s", res.Status, res.Body)
	}
	if res := a.Do("PUT", "/api/dashboards", saved, "X-CSRF-Token", "wrong"); res.Status != http.StatusForbidden {
		t.Errorf("PUT without the right CSRF token: %d %s", res.Status, res.Body)
	}
}

// Shared boards are published by an account with server.config only. An
// account sees the boards for everyone and those naming its role; one
// that may publish sees all of them and is told it may.
func TestSharedBoardsByRole(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := routetest.New(t, routetest.Options{DB: db})
		body := `{"boards":[` +
			`{"id":"all","name":"Everyone","widgets":[{"id":"w","type":"devices","size":"s"}],"roles":[],"home":true},` +
			`{"id":"ops","name":"Operators","widgets":[],"roles":["operator"],"locked":true},` +
			`{"id":"look","name":"Viewers","widgets":[],"roles":["viewer"," admin "]}]}`
		for _, role := range []string{"viewer", "operator"} {
			res := w.As(role).Do("PUT", "/api/dashboards/shared", body)
			if res.Status != http.StatusForbidden {
				t.Errorf("%s publishes: %d %s", role, res.Status, res.Body)
			}
		}
		if got := data(t, w.As("operator").Do("GET", "/api/dashboards", "")); got != nothing {
			t.Errorf("a refused publish was stored: %s", got)
		}
		if w.AuditRows() != nil {
			t.Errorf("audit after the refused publishes: %v", w.AuditRows())
		}

		all := `{"id":"all","name":"Everyone","widgets":[{"id":"w","type":"devices","size":"s"}],"roles":[],"locked":false,"home":true}`
		ops := `{"id":"ops","name":"Operators","widgets":[],"roles":["operator"],"locked":true,"home":false}`
		look := `{"id":"look","name":"Viewers","widgets":[],"roles":["viewer","admin"],"locked":false,"home":false}`
		if got := data(t, w.As("admin").Do("PUT", "/api/dashboards/shared", body)); got != `{"boards":[`+all+`,`+ops+`,`+look+`]}` {
			t.Errorf("published: %s", got)
		}
		audit := w.AuditRows()
		if len(audit) != 1 || !strings.HasPrefix(audit[0], "dashboards_shared_changed|") || !strings.HasSuffix(audit[0], "|1") {
			t.Errorf("audit: %v", audit)
		}

		for role, want := range map[string]string{
			"viewer":       `[` + all + `,` + look + `]`,
			"operator":     `[` + all + `,` + ops + `]`,
			"guest_issuer": `[` + all + `]`,
		} {
			if got := data(t, w.As(role).Do("GET", "/api/dashboards", "")); got != `{"own":null,"shared":`+want+`,"canPublish":false}` {
				t.Errorf("%s sees %s", role, got)
			}
		}
		if got := data(t, w.As("super_admin").Do("GET", "/api/dashboards", "")); got != `{"own":null,"shared":[`+all+`,`+ops+`,`+look+`],"canPublish":true}` {
			t.Errorf("a publisher sees %s", got)
		}

		// Publishing none takes them all away
		if got := data(t, w.As("admin").Do("PUT", "/api/dashboards/shared", `{"boards":[]}`)); got != `{"boards":[]}` {
			t.Errorf("published none: %s", got)
		}
		if got := data(t, w.As("viewer").Do("GET", "/api/dashboards", "")); got != nothing {
			t.Errorf("after publishing none: %s", got)
		}
	})
}

// What the database holds and cannot be read counts as nothing saved.
func TestUnreadableStoredLayouts(t *testing.T) {
	w := routetest.New(t, routetest.Options{})
	ctx := t.Context()
	if _, err := w.DB.Exec(ctx, `INSERT INTO user_dashboards (user_id, boards) VALUES (?, 'not json')`, routetest.RoleIDs["viewer"]); err != nil {
		t.Fatal(err)
	}
	if _, err := w.DB.Exec(ctx, `INSERT INTO settings (key, value) VALUES ('dashboards.shared', '{"broken":')`); err != nil {
		t.Fatal(err)
	}
	if got := data(t, w.As("viewer").Do("GET", "/api/dashboards", "")); got != nothing {
		t.Errorf("unreadable rows: %s", got)
	}
	// A key a later version added is skipped
	if _, err := w.DB.Exec(ctx, `INSERT INTO user_dashboards (user_id, boards) VALUES (?, '{"boards":[],"active":"","hidden":[],"later":1}')`, routetest.RoleIDs["operator"]); err != nil {
		t.Fatal(err)
	}
	if got := data(t, w.As("operator").Do("GET", "/api/dashboards", "")); got != `{"own":{"boards":[],"active":"","hidden":[]},"shared":[],"canPublish":false}` {
		t.Errorf("a row with a later key: %s", got)
	}
}
