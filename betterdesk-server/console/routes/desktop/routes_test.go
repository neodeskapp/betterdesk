package desktop_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/config"
	"github.com/unitronix/betterdesk-server/console/routes/internal/routetest"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// The test account of admin has id 1, the viewer's 6.

const empty = `{"success":true,"data":{"widgets":[],"wallpaper":""}}`

func eachDB(t *testing.T, fn func(t *testing.T, w *routetest.World)) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		fn(t, routetest.New(t, routetest.Options{DB: db}))
	})
}

// setting reads a row of the settings table, "(none)" when there is none.
func setting(t *testing.T, w *routetest.World, key string) string {
	t.Helper()
	var value string
	err := w.DB.QueryRow(context.Background(), "SELECT value FROM settings WHERE key = ?", key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "(none)"
	}
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestGuards(t *testing.T) {
	w := routetest.New(t, routetest.Options{})
	signedOut := `{"success":false,"error":"Unauthorized. Please log in.","code":"session_required"}`
	for _, route := range []struct{ method, target, body string }{
		{"POST", "/api/desktop/layout", `{"widgets":[]}`},
		{"GET", "/api/desktop/layout", ""},
		{"GET", "/api/desktop/wallpapers", ""},
	} {
		if got := w.SignedOut().Do(route.method, route.target, route.body); got.Status != 401 || got.Body != signedOut {
			t.Errorf("%s %s signed out: %d %s", route.method, route.target, got.Status, got.Body)
		}
		for _, role := range []string{"guest_issuer", "viewer", "admin"} {
			if got := w.As(role).Do(route.method, route.target, route.body); got.Status != 200 {
				t.Errorf("%s %s as %s: %d %s", route.method, route.target, role, got.Status, got.Body)
			}
		}
	}
	if got := setting(t, w, "desktop_layout_0"); got != "(none)" {
		t.Errorf("a layout was stored for nobody: %s", got)
	}
}

func TestSaveAndLoad(t *testing.T) {
	eachDB(t, func(t *testing.T, w *routetest.World) {
		admin, viewer := w.As("admin"), w.As("viewer")
		if got := admin.Do("GET", "/api/desktop/layout", ""); got.Status != 200 || got.Body != empty {
			t.Fatalf("before: %d %s", got.Status, got.Body)
		}

		// Stored as JavaScript writes JSON: numbers in their shortest form,
		// a name sent twice once with its last value, whole numbers as
		// names first
		body := `{"widgets":[{"id":"w1","type":"clock","x":1.0,"y":2e1,"w":3,"h":4,"h":5,"b":{"z":"<Ä>","10":1,"9":2}}],"wallpaper":"/wallpapers/3.png"}`
		stored := `[{"id":"w1","type":"clock","x":1,"y":20,"w":3,"h":5,"b":{"9":2,"10":1,"z":"<Ä>"}}]`
		if got := admin.Do("POST", "/api/desktop/layout", body); got.Status != 200 || got.Body != `{"success":true}` {
			t.Fatalf("save: %d %s", got.Status, got.Body)
		}
		if got := setting(t, w, "desktop_layout_1"); got != stored {
			t.Errorf("stored layout: %s", got)
		}
		if got := setting(t, w, "desktop_wallpaper_1"); got != "/wallpapers/3.png" {
			t.Errorf("stored wallpaper: %s", got)
		}
		if got := admin.Do("GET", "/api/desktop/layout", ""); got.Body != `{"success":true,"data":{"widgets":`+stored+`,"wallpaper":"/wallpapers/3.png"}}` {
			t.Errorf("load: %s", got.Body)
		}
		// Each account has its own
		if got := viewer.Do("GET", "/api/desktop/layout", ""); got.Body != empty {
			t.Errorf("another account: %s", got.Body)
		}
		if got := setting(t, w, "desktop_layout_6"); got != "(none)" {
			t.Errorf("layout of the other account: %s", got)
		}

		// A wallpaper alone empties the layout; widgets alone keep the wallpaper
		if got := admin.Do("POST", "/api/desktop/layout", `{"wallpaper":"solid:#1a2B3c"}`); got.Status != 200 {
			t.Fatalf("save the wallpaper alone: %d %s", got.Status, got.Body)
		}
		if got := admin.Do("GET", "/api/desktop/layout", ""); got.Body != `{"success":true,"data":{"widgets":[],"wallpaper":"solid:#1a2B3c"}}` {
			t.Errorf("after the wallpaper alone: %s", got.Body)
		}
		if got := admin.Do("POST", "/api/desktop/layout", `{"widgets":[{"id":"w2","type":"note","x":0,"y":0,"w":1,"h":1}]}`); got.Status != 200 {
			t.Fatalf("save widgets alone: %d %s", got.Status, got.Body)
		}
		if got := admin.Do("GET", "/api/desktop/layout", ""); got.Body != `{"success":true,"data":{"widgets":[{"id":"w2","type":"note","x":0,"y":0,"w":1,"h":1}],"wallpaper":"solid:#1a2B3c"}}` {
			t.Errorf("after widgets alone: %s", got.Body)
		}
		// A list as body has no widgets and no wallpaper
		if got := admin.Do("POST", "/api/desktop/layout", `[{"id":"w3","type":"note","x":0,"y":0,"w":1,"h":1}]`); got.Status != 200 {
			t.Fatalf("save a list: %d %s", got.Status, got.Body)
		}
		if got := setting(t, w, "desktop_layout_1"); got != "[]" {
			t.Errorf("stored layout after a list as body: %s", got)
		}
		if got := admin.Do("POST", "/api/desktop/layout", `{"widgets":[],"wallpaper":""}`); got.Status != 200 {
			t.Fatalf("reset: %d %s", got.Status, got.Body)
		}
		if got := admin.Do("GET", "/api/desktop/layout", ""); got.Body != empty {
			t.Errorf("after the reset: %s", got.Body)
		}
		if got := setting(t, w, "desktop_wallpaper_1"); got != "" {
			t.Errorf("stored wallpaper after the reset: %q", got)
		}
	})
}

func TestSaveRefuses(t *testing.T) {
	// More saves than the limit of 30 a minute allows by default
	w := routetest.New(t, routetest.Options{Env: map[string]string{"PANEL_PREFERENCE_RATE_LIMIT_MAX": "1000"}})
	admin := w.As("admin")
	widget := `{"id":"w","type":"clock","x":0,"y":0,"w":1,"h":1}`
	many := "[" + strings.TrimSuffix(strings.Repeat(widget+",", 101), ",") + "]"
	most := "[" + strings.TrimSuffix(strings.Repeat(widget+",", 100), ",") + "]"
	id40, type60 := strings.Repeat("i", 40), strings.Repeat("t", 60)
	// Two letters outside the basic plane count as four
	wide := strings.Repeat("😀", 20)
	layout := `{"success":false,"error":"Invalid widget layout"}`
	wallpaper := `{"success":false,"error":"Invalid wallpaper path"}`

	for _, tc := range []struct {
		name, body string
		status     int
		want       string
	}{
		{"widgets as text", `{"widgets":"w"}`, 400, layout},
		{"widgets null", `{"widgets":null}`, 400, layout},
		{"widgets as an object", `{"widgets":{"0":` + widget + `}}`, 400, layout},
		{"101 widgets", `{"widgets":` + many + `}`, 400, layout},
		{"100 widgets", `{"widgets":` + most + `}`, 200, `{"success":true}`},
		{"a widget without an id", `{"widgets":[{"type":"clock","x":0,"y":0,"w":1,"h":1}]}`, 400, layout},
		{"an id that is a number", `{"widgets":[{"id":1,"type":"clock","x":0,"y":0,"w":1,"h":1}]}`, 400, layout},
		{"an id of 40", `{"widgets":[{"id":"` + id40 + `","type":"clock","x":0,"y":0,"w":1,"h":1}]}`, 200, `{"success":true}`},
		{"an id of 41", `{"widgets":[{"id":"` + id40 + `x","type":"clock","x":0,"y":0,"w":1,"h":1}]}`, 400, layout},
		{"an id of 20 wide letters", `{"widgets":[{"id":"` + wide + `","type":"clock","x":0,"y":0,"w":1,"h":1}]}`, 200, `{"success":true}`},
		{"an id of 20 wide letters and one more", `{"widgets":[{"id":"` + wide + `x","type":"clock","x":0,"y":0,"w":1,"h":1}]}`, 400, layout},
		{"a type of 60", `{"widgets":[{"id":"w","type":"` + type60 + `","x":0,"y":0,"w":1,"h":1}]}`, 200, `{"success":true}`},
		{"a type of 61", `{"widgets":[{"id":"w","type":"` + type60 + `x","x":0,"y":0,"w":1,"h":1}]}`, 400, layout},
		{"a type that is null", `{"widgets":[{"id":"w","type":null,"x":0,"y":0,"w":1,"h":1}]}`, 400, layout},
		{"x as text", `{"widgets":[{"id":"w","type":"clock","x":"0","y":0,"w":1,"h":1}]}`, 400, layout},
		{"y missing", `{"widgets":[{"id":"w","type":"clock","x":0,"w":1,"h":1}]}`, 400, layout},
		{"w null", `{"widgets":[{"id":"w","type":"clock","x":0,"y":0,"w":null,"h":1}]}`, 400, layout},
		{"h true", `{"widgets":[{"id":"w","type":"clock","x":0,"y":0,"w":1,"h":true}]}`, 400, layout},
		{"a widget that is a list", `{"widgets":[["w"]]}`, 400, layout},
		{"a widget that is null", `{"widgets":[null]}`, 400, layout},
		{"a widget that is text", `{"widgets":["w"]}`, 400, layout},
		{"a wallpaper file", `{"wallpaper":"/wallpapers/1234.png"}`, 200, `{"success":true}`},
		{"a wallpaper color of three", `{"wallpaper":"solid:#fff"}`, 200, `{"success":true}`},
		{"a wallpaper color of eight", `{"wallpaper":"solid:#12345678"}`, 200, `{"success":true}`},
		{"an empty wallpaper", `{"wallpaper":""}`, 200, `{"success":true}`},
		{"a wallpaper outside the folder", `{"wallpaper":"/wallpapers/../x.png"}`, 400, wallpaper},
		{"a wallpaper with five digits", `{"wallpaper":"/wallpapers/12345.png"}`, 400, wallpaper},
		{"a wallpaper without a number", `{"wallpaper":"/wallpapers/a.png"}`, 400, wallpaper},
		{"a wallpaper of another type", `{"wallpaper":"/wallpapers/1.jpg"}`, 400, wallpaper},
		{"a wallpaper with a line break behind", `{"wallpaper":"/wallpapers/1.png\n"}`, 400, wallpaper},
		{"a wallpaper color of two", `{"wallpaper":"solid:#12"}`, 400, wallpaper},
		{"a wallpaper color of nine", `{"wallpaper":"solid:#123456789"}`, 400, wallpaper},
		{"a wallpaper color that is none", `{"wallpaper":"solid:#ggg"}`, 400, wallpaper},
		{"a wallpaper that is a number", `{"wallpaper":3}`, 400, wallpaper},
		{"a wallpaper that is false", `{"wallpaper":false}`, 400, wallpaper},
		{"a wallpaper that is a list", `{"wallpaper":["/wallpapers/1.png"]}`, 400, wallpaper},
		{"the widgets are checked first", `{"widgets":"x","wallpaper":3}`, 400, layout},
	} {
		if got := admin.Do("POST", "/api/desktop/layout", tc.body); got.Status != tc.status || got.Body != tc.want {
			t.Errorf("%s: %d %s, want %d %s", tc.name, got.Status, got.Body, tc.status, tc.want)
		}
	}

	// A refused save stores nothing
	before := setting(t, w, "desktop_layout_1")
	if got := admin.Do("POST", "/api/desktop/layout", `{"widgets":[`+widget+`],"wallpaper":"nowhere"}`); got.Status != 400 {
		t.Errorf("bad wallpaper with good widgets: %d %s", got.Status, got.Body)
	}
	if after := setting(t, w, "desktop_layout_1"); after != before {
		t.Errorf("a refused save changed the layout from %s to %s", before, after)
	}
}

func TestSaveRefusesALayoutOverHalfAMegabyte(t *testing.T) {
	admin := routetest.New(t, routetest.Options{}).As("admin")
	// The stored text is [{"id":"w","type":"clock","x":0,"y":0,"w":1,"h":1,"blob":"…"}]
	frame := len(`[{"id":"w","type":"clock","x":0,"y":0,"w":1,"h":1,"blob":""}]`)
	save := func(blob string) routetest.Answer {
		return admin.Do("POST", "/api/desktop/layout", `{"widgets":[{"id":"w","type":"clock","x":0,"y":0,"w":1,"h":1,"blob":"`+blob+`"}]}`)
	}
	if got := save(strings.Repeat("a", 512*1024-frame)); got.Status != 200 {
		t.Errorf("exactly half a megabyte: %d %s", got.Status, got.Body)
	}
	if got := save(strings.Repeat("a", 512*1024-frame+1)); got.Status != 413 || got.Body != `{"success":false,"error":"Layout too large"}` {
		t.Errorf("one more: %d %s", got.Status, got.Body)
	}
	// The size is counted in the units JavaScript counts text in: a letter
	// of three bytes is one
	if got := save(strings.Repeat("€", 512*1024-frame)); got.Status != 200 {
		t.Errorf("half a megabyte of letters of three bytes: %d %s", got.Status, got.Body)
	}
}

func TestTheFileStandsInForTheDatabase(t *testing.T) {
	w := routetest.New(t, routetest.Options{})
	admin := w.As("admin")
	dir := filepath.Join(w.DataDir, "desktop-layouts")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, "1.json"), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	load := func() string { return admin.Do("GET", "/api/desktop/layout", "").Body }
	data := func(inner string) string { return `{"success":true,"data":` + inner + `}` }

	write(`{"widgets":[{"id":"f"}],"wallpaper":"/wallpapers/7.png","updated_at":"2026-01-01T00:00:00.000Z"}`)
	if got := load(); got != data(`{"widgets":[{"id":"f"}],"wallpaper":"/wallpapers/7.png"}`) {
		t.Errorf("from the file: %s", got)
	}
	for content, want := range map[string]string{
		`{"widgets":"none","wallpaper":5}`: `{"widgets":[],"wallpaper":5}`,
		`{"wallpaper":null}`:               `{"widgets":[],"wallpaper":""}`,
		`not json`:                         `{"widgets":[],"wallpaper":""}`,
		`null`:                             `{"widgets":[],"wallpaper":""}`,
		`[1]`:                              `{"widgets":[],"wallpaper":""}`,
		`7`:                                `{"widgets":[],"wallpaper":""}`,
	} {
		write(content)
		if got := load(); got != data(want) {
			t.Errorf("file %s: %s, want %s", content, got, data(want))
		}
	}

	// The wallpaper of the database goes before the file's
	write(`{"widgets":[{"id":"f"}],"wallpaper":"/wallpapers/7.png"}`)
	if _, err := w.DB.Exec(context.Background(), "INSERT INTO settings (key, value) VALUES ('desktop_wallpaper_1', 'solid:#000')"); err != nil {
		t.Fatal(err)
	}
	if got := load(); got != data(`{"widgets":[{"id":"f"}],"wallpaper":"solid:#000"}`) {
		t.Errorf("wallpaper from the database: %s", got)
	}

	// A stored layout wins over the file, whatever it is
	for stored, want := range map[string]string{
		`[{"id":"d"}]`: `[{"id":"d"}]`,
		`5`:            `5`,
		`{"a":1}`:      `{"a":1}`,
		`null`:         `null`,
		`not json`:     `[]`,
		` [ 1 , 2 ] `:  `[1,2]`,
	} {
		if _, err := w.DB.Exec(context.Background(), "DELETE FROM settings WHERE key = 'desktop_layout_1'"); err != nil {
			t.Fatal(err)
		}
		if _, err := w.DB.Exec(context.Background(), "INSERT INTO settings (key, value) VALUES ('desktop_layout_1', ?)", stored); err != nil {
			t.Fatal(err)
		}
		if got := load(); got != data(`{"widgets":`+want+`,"wallpaper":"solid:#000"}`) {
			t.Errorf("stored %s: %s", stored, got)
		}
	}
}

func TestWithoutTheSettingsTable(t *testing.T) {
	w := routetest.New(t, routetest.Options{})
	admin := w.As("admin")
	if _, err := w.DB.Exec(context.Background(), "DROP TABLE settings"); err != nil {
		t.Fatal(err)
	}
	widgets := `[{"id":"w","type":"clock","x":0,"y":0,"w":1,"h":1}]`
	if got := admin.Do("POST", "/api/desktop/layout", `{"widgets":`+widgets+`,"wallpaper":"solid:#abc"}`); got.Status != 200 || got.Body != `{"success":true}` {
		t.Fatalf("save: %d %s", got.Status, got.Body)
	}
	var file struct {
		Widgets   json.RawMessage `json:"widgets"`
		Wallpaper string          `json:"wallpaper"`
	}
	path := filepath.Join(w.DataDir, "desktop-layouts", "1.json")
	raw, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(raw, &file) != nil || string(file.Widgets) != widgets || file.Wallpaper != "solid:#abc" {
		t.Errorf("fallback file: %v %s", err, raw)
	}
	if info, err := os.Stat(path); err == nil && runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Errorf("fallback file mode %v", info.Mode().Perm())
	}
	if got := admin.Do("GET", "/api/desktop/layout", ""); got.Body != `{"success":true,"data":{"widgets":`+widgets+`,"wallpaper":"solid:#abc"}}` {
		t.Errorf("load: %s", got.Body)
	}

	// When the file cannot be written either, the save fails
	if runtime.GOOS == "windows" || os.Getuid() == 0 {
		// A folder in the way does it for every user
		if err := os.RemoveAll(filepath.Join(w.DataDir, "desktop-layouts")); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(w.DataDir, "desktop-layouts"), []byte("in the way"), 0o600); err != nil {
			t.Fatal(err)
		}
	} else if err := os.Chmod(filepath.Join(w.DataDir, "desktop-layouts"), 0o500); err != nil {
		t.Fatal(err)
	}
	if got := admin.Do("POST", "/api/desktop/layout", `{"widgets":[]}`); got.Status != 500 || got.Body != `{"success":false,"error":"Failed to save layout"}` {
		t.Errorf("save without database and file: %d %s", got.Status, got.Body)
	}
}

func TestWallpapers(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "wallpapers")
	w := routetest.New(t, routetest.Options{Config: func(cfg *config.Config) { cfg.WallpapersDir = dir }})
	admin := w.As("admin")
	list := func() routetest.Answer { return admin.Do("GET", "/api/desktop/wallpapers", "") }

	if got := list(); got.Status != 200 || got.Body != `{"success":true,"data":[]}` {
		t.Errorf("without the folder: %d %s", got.Status, got.Body)
	}
	if err := os.MkdirAll(filepath.Join(dir, "7.png"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := list(); got.Body != `{"success":true,"data":["/wallpapers/7.png"]}` {
		t.Errorf("a folder with a wallpaper's name: %s", got.Body)
	}
	for _, name := range []string{"10.png", "2.png", "1.PNG", "x.png", "03.png", "3.png", "4.jpg", "5.png.bak", "-1.png", "1e3.png", "999999999999999999999999.png", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// By number; two files with one number stay in the order of their names
	want := `{"success":true,"data":["/wallpapers/1.PNG","/wallpapers/2.png","/wallpapers/03.png","/wallpapers/3.png","/wallpapers/7.png","/wallpapers/10.png","/wallpapers/999999999999999999999999.png"]}`
	if got := list(); got.Body != want {
		t.Errorf("list:\n%s\nwant\n%s", got.Body, want)
	}

	// A file in the folder's place cannot be listed
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := list(); got.Status != 500 || got.Body != `{"success":false,"error":"Failed to list wallpapers"}` {
		t.Errorf("a file in the folder's place: %d %s", got.Status, got.Body)
	}
}

// TestWallpaperNullClearsIt: a wallpaper of null is stored as "", which
// takes the wallpaper away, and the save reaches the database.
func TestWallpaperNullClearsIt(t *testing.T) {
	eachDB(t, func(t *testing.T, w *routetest.World) {
		admin := w.As("admin")
		if got := admin.Do("POST", "/api/desktop/layout", `{"wallpaper":"solid:#abc"}`); got.Status != 200 {
			t.Fatal(got.Body)
		}
		widgets := `[{"id":"w","type":"clock","x":0,"y":0,"w":1,"h":1}]`
		if got := admin.Do("POST", "/api/desktop/layout", `{"widgets":`+widgets+`,"wallpaper":null}`); got.Status != 200 || got.Body != `{"success":true}` {
			t.Fatalf("save: %d %s", got.Status, got.Body)
		}
		if got := setting(t, w, "desktop_wallpaper_1"); got != "" {
			t.Errorf("stored wallpaper: %q", got)
		}
		if _, err := os.Stat(filepath.Join(w.DataDir, "desktop-layouts", "1.json")); !os.IsNotExist(err) {
			t.Errorf("a fallback file was written: %v", err)
		}
		if got := admin.Do("GET", "/api/desktop/layout", ""); got.Body != `{"success":true,"data":{"widgets":`+widgets+`,"wallpaper":""}}` {
			t.Errorf("load: %s", got.Body)
		}
	})
}
