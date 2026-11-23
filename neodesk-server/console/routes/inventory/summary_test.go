package inventory

import (
	"math"
	"math/big"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/access"
	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
	"github.com/neodeskapp/neodesk/neodesk-server/console/jsbind"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/jsjson"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

func TestRound(t *testing.T) {
	for x, want := range map[float64]float64{
		0: 0, 0.4: 0, 0.5: 1, 0.49999999999999994: 0, 1.5: 2, 2.5: 3, -0.4: 0, -0.5: 0, -0.6: -1, -1.5: -1, -2.5: -2, -2.51: -3,
		4503599627370497: 4503599627370497, 9007199254740993: 9007199254740992, 1e300: 1e300, -1e300: -1e300, 5e-324: 0,
		math.Inf(1): math.Inf(1), math.Inf(-1): math.Inf(-1),
	} {
		if got := round(x); got != want {
			t.Errorf("Math.round(%v) = %v, want %v", x, got, want)
		}
	}
	if got := round(math.NaN()); !math.IsNaN(got) {
		t.Errorf("Math.round(NaN) = %v", got)
	}
	for n, want := range map[number]string{0: "0", 1.5: "1.5", 1e21: "1e+21", number(math.NaN()): "null", number(math.Inf(1)): "null", number(math.Copysign(0, -1)): "0"} {
		if got, _ := n.MarshalJSON(); string(got) != want {
			t.Errorf("the number %v in JSON: %s, want %s", float64(n), got, want)
		}
	}
}

func TestPageNumber(t *testing.T) {
	list := jsbind.Query("page=1&page=2", "page")
	for _, tt := range []struct {
		value jsbind.Value
		want  float64
		ok    bool
	}{
		{jsbind.Undefined, 7, true},
		{jsbind.String("1"), 1, true},
		{jsbind.String("0042"), 42, true},
		{jsbind.String("100"), 100, true},
		{jsbind.String("101"), 100, true},
		{jsbind.String("9007199254740991"), 100, true},
		{jsbind.String("9007199254740992"), 0, false},
		{jsbind.String(strings.Repeat("9", 400)), 0, false},
		{jsbind.String("0"), 0, false},
		{jsbind.String("000"), 0, false},
		{jsbind.String(""), 0, false},
		{jsbind.String("-1"), 0, false},
		{jsbind.String("+1"), 0, false},
		{jsbind.String("1.0"), 0, false},
		{jsbind.String("1e2"), 0, false},
		{jsbind.String(" 1"), 0, false},
		{jsbind.String("1\n"), 0, false},
		{jsbind.String("0x10"), 0, false},
		{jsbind.String("\xd9\xa1"), 0, false}, // a digit of another script
		{list, 0, false},
		{jsbind.Query("page[n]=1", "page"), 0, false},
	} {
		got, ok := pageNumber(tt.value, 7, 100)
		if ok != tt.ok || got != tt.want {
			t.Errorf("pageNumber(%s): %v %v, want %v %v", tt.value.JSON(), got, ok, tt.want, tt.ok)
		}
	}
	if got, ok := pageNumber(jsbind.String("9007199254740991"), 1, maxSafeInteger); !ok || got != 9007199254740991 {
		t.Errorf("the largest page: %v %v", got, ok)
	}
}

func parse(t *testing.T, text string) jsjson.Value {
	t.Helper()
	v, err := jsjson.Parse(text)
	if err != nil {
		t.Fatalf("%s: %v", text, err)
	}
	return v
}

func TestFullestDisk(t *testing.T) {
	const gb = 1 << 30
	for _, tt := range []struct {
		disks   string
		percent float64
		free    float64
		mount   string
		none    bool
		// unread is the number of sizes and names that cannot be read
		unread int
	}{
		{disks: `{"0":{"total_bytes":4,"used_bytes":1}}`, none: true},
		{disks: `"disks"`, none: true},
		{disks: `[]`, none: true},
		{disks: `[null,1,"x",true,[],[{"total_bytes":4,"used_bytes":1}]]`, none: true},
		{disks: `[{"total_bytes":4,"used_bytes":1,"mount_point":"/"}]`, percent: 25, free: 3, mount: "/"},
		{disks: `[{"total_gb":4,"used_gb":1,"device":"sda"}]`, percent: 25, free: 3 * gb, mount: "sda"},
		{disks: `[{"total_bytes":4,"total_gb":8,"used_gb":0.5,"name":"n"}]`, percent: 100, free: 0, mount: "n"},
		{disks: `[{"total_gb":8,"used_bytes":2147483648,"used_gb":7}]`, percent: 25, free: 6 * gb},
		{disks: `[{"total_bytes":"","total_gb":"1","available_bytes":"1073741820"}]`, percent: 4.0 / gb * 100, free: 1073741820},
		{disks: `[{"total_bytes":8,"used_bytes":null,"used_gb":"","available_bytes":null,"free_bytes":6}]`, percent: 25, free: 6},
		{disks: `[{"total_bytes":8,"available_bytes":2,"free_bytes":6}]`, percent: 75, free: 2},
		{disks: `[{"total_bytes":8,"used_bytes":"x","free_bytes":6}]`, none: true},
		{disks: `[{"total_bytes":8,"available_bytes":"x","free_bytes":6}]`, none: true},
		{disks: `[{"total_bytes":8}]`, none: true},
		{disks: `[{"total_bytes":0,"used_bytes":0},{"total_bytes":-5,"used_bytes":1},{"total_bytes":"x","total_gb":5,"used_bytes":1},{"used_bytes":1}]`, none: true},
		{disks: `[{"total_bytes":8,"used_bytes":4,"removable":true},{"total_bytes":8,"used_bytes":2,"removable":"yes"},{"total_bytes":8,"used_bytes":1,"removable":0}]`, percent: 12.5, free: 7},
		{disks: `[{"total_bytes":8,"used_bytes":80},{"total_bytes":8,"used_bytes":800,"mount_point":"second"}]`, percent: 100, free: 0},
		{disks: `[{"total_bytes":8,"used_bytes":-4,"mount_point":"under"}]`, percent: 0, free: 12, mount: "under"},
		{disks: `[{"total_bytes":8,"used_bytes":2,"mount_point":"a"},{"total_bytes":8,"used_bytes":2,"mount_point":"b"},{"total_bytes":8,"used_bytes":4,"mount_point":"c"}]`, percent: 50, free: 4, mount: "c"},
		{disks: `[{"total_bytes":8,"used_bytes":4,"mount_point":"a"},{"total_bytes":8,"used_bytes":4,"mount_point":"b"}]`, percent: 50, free: 4, mount: "a"},
		{disks: `[{"total_bytes":8,"used_bytes":4,"mount_point":0,"device":"","name":["n",1]}]`, percent: 50, free: 4, mount: "n,1"},
		{disks: `[{"total_bytes":8,"used_bytes":4,"mount_point":{"a":1}}]`, percent: 50, free: 4, mount: "[object Object]"},
		{disks: `[{"total_bytes":1e400,"used_bytes":4}]`, percent: 0, free: math.Inf(1)},
		{disks: `[{"total_bytes":8,"used_bytes":1e400}]`, none: true},
		{disks: `[{"total_bytes":true,"used_bytes":[0.5]}]`, percent: 50, free: 0.5},
		{disks: `[{"total_bytes":{"toString":1},"used_bytes":1}]`, none: true, unread: 1},
		{disks: `[{"total_bytes":8,"used_bytes":{"toString":1}}]`, none: true, unread: 1},
		{disks: `[{"total_bytes":8,"free_bytes":[{"toString":1}]}]`, none: true, unread: 1},
		{disks: `[{"total_bytes":8,"used_bytes":1,"name":{"toString":1}}]`, percent: 12.5, free: 7, mount: "", unread: 1},
		{disks: `[{"total_gb":{"toString":1}},{"total_bytes":4,"used_bytes":1,"mount_point":"/"},{"total_bytes":4,"used_bytes":[{"toString":1}]}]`, percent: 25, free: 3, mount: "/", unread: 2},
		{disks: `[{"total_bytes":8,"used_bytes":2,"name":"ok"},{"total_bytes":8,"used_bytes":1,"name":{"toString":1}}]`, percent: 25, free: 6, mount: "ok"},
		{disks: `[{"total_bytes":8,"used_bytes":1,"free_bytes":{"toString":1},"removable":{"toString":1}}]`, none: true},
		{disks: `[{"total_bytes":8,"used_bytes":1,"mount_point":"` + strings.Repeat("m", 130) + `"}]`, percent: 12.5, free: 7, mount: strings.Repeat("m", 120)},
	} {
		got, unread := fullestDisk(parse(t, tt.disks))
		switch {
		case len(unread) != tt.unread:
			t.Errorf("%s: errors %v, want %d", tt.disks, unread, tt.unread)
		case (got == nil) != tt.none:
			t.Errorf("%s: %+v, want none: %v", tt.disks, got, tt.none)
		case got != nil && (got.percent != tt.percent || got.free != tt.free || got.mount != tt.mount):
			t.Errorf("%s: %+v, want %v %v %q", tt.disks, *got, tt.percent, tt.free, tt.mount)
		}
	}
}

// everyDevice is the scope of an account that sees every device.
func everyDevice(*http.Request) (*access.DeviceScope, error) { return nil, nil }

// While no device has reported, the list shows the devices the API
// server knows, in the form of the list and with its paging.
func TestTheListFallsBackToThePeerList(t *testing.T) {
	answer := `[{"id":"A1","hostname":"host-a","os":"Windows 11","last_online":"2026-01-02T03:04:05Z","live_online":true},
		{"id":"A2"}, {"id":"A3","hostname":"","os":"","last_online":""}, {"id":"A4","hostname":"<b>","os":"Linux"}]`
	asked := ""
	api := goapi.New(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = r.Method + " " + r.URL.RequestURI()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(answer))
	}))
	h := &handlers{db: storetest.Migrated(t), api: api, log: kit.NewLogger("silent", true), scope: everyDevice}
	get := func(target string) (int, string) {
		rec := httptest.NewRecorder()
		h.list(rec, httptest.NewRequest(http.MethodGet, target, nil))
		return rec.Code, rec.Body.String()
	}
	peer := func(id, hostname, os, lastSeen string) string {
		return `{"device_id":"` + id + `","hostname":"` + hostname + `","os":"` + os + `","cpu":"","cpu_cores":0,"cpu_usage":null,"memory_total_mb":0,"memory_used_mb":0,` +
			`"disk_count":0,"software_count":0,"last_seen":` + lastSeen + `,"collected_at":null,"source":"peer_list"}`
	}
	a1, a2 := peer("A1", "host-a", "Windows 11", `"2026-01-02T03:04:05Z"`), peer("A2", "A2", "", "null")
	a3, a4 := peer("A3", "A3", "", "null"), peer("A4", "<b>", "Linux", "null")
	for target, want := range map[string]string{
		"/api/inventory":                `{"devices":[` + a1 + `,` + a2 + `,` + a3 + `,` + a4 + `],"total":4,"page":1,"limit":50,"total_pages":1}`,
		"/api/inventory?limit=3&page=2": `{"devices":[` + a4 + `],"total":4,"page":2,"limit":3,"total_pages":2}`,
		"/api/inventory?limit=2&page=3": `{"devices":[],"total":4,"page":3,"limit":2,"total_pages":2}`,
	} {
		if status, body := get(target); status != 200 || body != want {
			t.Errorf("GET %s: %d %s\nwant 200 %s", target, status, body, want)
		}
	}
	if asked != "GET /api/peers" {
		t.Errorf("the API server was asked %q, want the list of peers", asked)
	}

	// With one report there, the list is the reports
	if _, err := h.db.Exec(t.Context(), `INSERT INTO device_inventory (device_id, hardware) VALUES ('R1', '{"hostname":"reported"}')`); err != nil {
		t.Fatal(err)
	}
	if status, body := get("/api/inventory"); status != 200 || !strings.Contains(body, `"hostname":"reported"`) || strings.Contains(body, "peer_list") || !strings.Contains(body, `"total":1,`) {
		t.Errorf("the list with a report: %d %s", status, body)
	}

	// An API server that gives no list: an empty list, not an error
	answer = `{"error":"down"}`
	if _, err := h.db.Exec(t.Context(), `DELETE FROM device_inventory`); err != nil {
		t.Fatal(err)
	}
	if status, body := get("/api/inventory"); status != 200 || body != `{"devices":[],"total":0,"page":1,"limit":50,"total_pages":0}` {
		t.Errorf("the list without the peer list: %d %s", status, body)
	}
}

// The group is mounted at /api/nd too, for the routes of the desktop
// agent. There the two read routes step aside.
func TestTheReadRoutesStepAsideAtTheDeviceMount(t *testing.T) {
	router := kit.NewRouter()
	Register((&kit.Deps{Router: router, Log: kit.NewLogger("silent", true)}).At("/api/nd"))
	if len(router.Routes()) != 5 {
		t.Fatalf("%d routes registered, want the three device routes and the two read routes", len(router.Routes()))
	}
	for _, path := range []string{"/api/nd", "/api/nd/commands"} {
		rec := httptest.NewRecorder()
		if router.Serve(rec, httptest.NewRequest(http.MethodGet, path, nil)) {
			t.Errorf("GET %s was answered with %d: the route has to pass the request on", path, rec.Code)
		}
	}
}

// FuzzPageNumber holds pageNumber against the rule written out with big
// numbers: digits only, from 1 to the largest exact whole number, cut to
// the maximum.
func FuzzPageNumber(f *testing.F) {
	for _, seed := range []string{"1", "50", "0", "007", "9007199254740991", "9007199254740992", "1e3", " 5", "5 ", "-1", "", "12a", "99999999999999999999999"} {
		f.Add(seed)
	}
	digits := regexp.MustCompile(`^[0-9]+$`)
	limit := big.NewInt(maxSafeInteger)
	f.Fuzz(func(t *testing.T, text string) {
		got, ok := pageNumber(jsbind.String(text), 3, 100)
		n, isNumber := new(big.Int).SetString(text, 10)
		want := digits.MatchString(text) && isNumber && n.Sign() > 0 && n.Cmp(limit) <= 0
		if ok != want {
			t.Fatalf("pageNumber(%q) taken: %v, want %v", text, ok, want)
		}
		if ok && got != math.Min(float64(n.Int64()), 100) {
			t.Fatalf("pageNumber(%q) = %v, want %v cut to 100", text, got, n)
		}
	})
}
