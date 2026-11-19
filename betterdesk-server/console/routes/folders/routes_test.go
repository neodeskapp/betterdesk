package folders_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/config"
	"github.com/unitronix/betterdesk-server/console/routes/internal/routetest"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// devices is what the made-up API server lists: A, B and C. Z is no
// device.
const devices = `{"peers":[{"id":"A","hostname":"a","online":true},{"id":"B","hostname":"b"},{"id":"C","hostname":"c","tags":"lab"}]}`

func newWorld(t *testing.T, db *store.DB) *routetest.World {
	t.Helper()
	api := routetest.NewAPI()
	api.Fallback = func(c routetest.Call) routetest.Reply {
		switch {
		case c.Target == "/api/peers":
			return routetest.Reply{Status: 200, Body: devices}
		case strings.HasPrefix(c.Target, "/api/peers/"):
			id := strings.TrimPrefix(c.Target, "/api/peers/")
			if strings.Contains("ABC", id) && len(id) == 1 {
				return routetest.Reply{Status: 200, Body: `{"id":"` + id + `","hostname":"` + strings.ToLower(id) + `"}`}
			}
		case strings.HasPrefix(c.Target, "/api/remote-targets"):
			return routetest.Reply{Status: 200, Body: `{"targets":[]}`}
		}
		return routetest.Reply{Status: 404, Body: `{"error":"not found"}`}
	}
	return routetest.New(t, routetest.Options{DB: db, API: api, Config: realLanguages})
}

// langDir holds the console's language files.
var langDir = filepath.Join("..", "..", "static", "files", "lang")

// realLanguages gives the console its own language files.
func realLanguages(cfg *config.Config) { cfg.LangDir = langDir }

func want(t *testing.T, what string, got routetest.Answer, status int, body string) {
	t.Helper()
	if got.Status != status || got.Body != body {
		t.Errorf("%s:\n got %d %s\nwant %d %s", what, got.Status, got.Body, status, body)
	}
}

// rows reads one text column per row.
func rows(t *testing.T, db *store.DB, query string) string {
	t.Helper()
	res, err := db.Query(context.Background(), query)
	if err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	defer res.Close()
	var out []string
	for res.Next() {
		var s string
		if err := res.Scan(&s); err != nil {
			t.Fatal(err)
		}
		out = append(out, s)
	}
	return strings.Join(out, ",")
}

const (
	required    = `{"success":false,"error":"Folder name is required"}`
	tooLong     = `{"success":false,"error":"Folder name is too long (max 50 characters)"}`
	serverError = `{"success":false,"error":"Server error"}`
	noFolder    = `{"success":false,"error":"Folder not found"}`
	noDevice    = `{"success":false,"error":"Device not found"}`
	forbidden   = `{"success":false,"error":"You don't have access to this device."}`
	invalidID   = `{"success":false,"error":"Invalid folder id"}`
	ok          = `{"success":true}`
)

func TestFolderRoutes(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := newWorld(t, db)
		admin, operator, viewer := w.As("admin"), w.As("operator"), w.As("viewer")

		// Who may
		if got := w.SignedOut().Do("GET", "/api/folders", ""); got.Status != http.StatusUnauthorized {
			t.Errorf("signed out: %d %s", got.Status, got.Body)
		}
		if got := viewer.Do("POST", "/api/folders", `{"name":"x"}`); got.Status != http.StatusForbidden {
			t.Errorf("a viewer creates: %d %s", got.Status, got.Body)
		}

		// Creating, and what a name must be
		want(t, "no name", admin.Do("POST", "/api/folders", `{"color":"#fff"}`), 400, required)
		want(t, "blank name", admin.Do("POST", "/api/folders", `{"name":" \t "}`), 400, required)
		want(t, "long name", admin.Do("POST", "/api/folders", `{"name":" `+strings.Repeat("x", 50)+`"}`), 400, tooLong)
		want(t, "name that is a number", admin.Do("POST", "/api/folders", `{"name":5}`), 400, required)
		want(t, "name that is a list", admin.Do("POST", "/api/folders", `{"name":["x"]}`), 400, required)
		want(t, "create", admin.Do("POST", "/api/folders", `{"name":"  Lab ","color":"#123","allowed_users":["e2e_viewer","ghost"," e2e_viewer"]}`),
			200, `{"success":true,"data":{"id":1,"name":"Lab","color":"#123","icon":"folder","allowed_users":["e2e_viewer"],"allowed_groups":[]}}`)
		want(t, "create with a color that is a number", admin.Do("POST", "/api/folders", `{"name":"Odd","color":5,"icon":""}`),
			400, `{"success":false,"error":"Invalid folder color"}`)
		want(t, "create with an empty icon", admin.Do("POST", "/api/folders", `{"name":"Odd","color":"#555","icon":""}`),
			200, `{"success":true,"data":{"id":2,"name":"Odd","color":"#555","icon":"folder","allowed_users":[],"allowed_groups":[]}}`)
		if got := rows(t, db, `SELECT guid FROM device_groups WHERE guid LIKE 'folder_%' ORDER BY guid`); got != "folder_1" {
			t.Errorf("folder groups after creating: %q", got)
		}
		if got := rows(t, db, `SELECT color FROM folders WHERE id = 2`); got != "#555" {
			t.Errorf("stored color: %q", got)
		}

		// Devices into the folder only e2e_viewer may see
		want(t, "assign nothing", admin.Do("POST", "/api/folders/1/devices", `{"deviceIds":[]}`), 400, `{"success":false,"error":"No devices selected"}`)
		want(t, "assign to no folder", admin.Do("POST", "/api/folders/99/devices", `{"deviceIds":["A"]}`), 404, noFolder)
		want(t, "assign an unknown device", admin.Do("POST", "/api/folders/1/devices", `{"deviceIds":["A","Z"]}`), 404, `{"success":false,"error":"Device not found","id":"Z"}`)
		want(t, "assign", admin.Do("POST", "/api/folders/1/devices", `{"deviceIds":["A","B"]}`), 200, `{"success":true,"data":{"count":2}}`)
		want(t, "assign what the operator may not see", operator.Do("POST", "/api/folders/2/devices", `{"deviceIds":["C","A"]}`), 403, forbidden)

		// The counts follow the scope of the account
		count := func(a *routetest.Agent) string {
			body := a.Do("GET", "/api/folders", "").Body
			start := strings.Index(body, `"device_count":`)
			if start < 0 {
				return body
			}
			return body[start : start+len(`"device_count":`)+1]
		}
		for who, a := range map[string]*routetest.Agent{"admin": admin, "viewer": viewer, "operator": operator} {
			wanted := map[string]string{"admin": `"device_count":2`, "viewer": `"device_count":2`, "operator": `"device_count":0`}[who]
			if got := count(a); got != wanted {
				t.Errorf("count of the first folder for %s: %s, want %s", who, got, wanted)
			}
		}
		list := admin.Do("GET", "/api/folders", "").Body
		if !strings.HasPrefix(list, `{"success":true,"data":{"folders":[{"id":1,"name":"Lab","color":"#123","icon":"folder","sort_order":0,"created_at":"`) ||
			!strings.HasSuffix(list, `"device_count":0,"allowed_users":[],"allowed_groups":[]}],"total":2}}`) {
			t.Errorf("list: %s", list)
		}

		// One device
		want(t, "move an unknown device", admin.Do("PATCH", "/api/devices/Z/folder", `{"folderId":1}`), 404, noDevice)
		want(t, "move what the operator may not see", operator.Do("PATCH", "/api/devices/A/folder", `{"folderId":null}`), 403, forbidden)
		want(t, "move into a folder that is text", admin.Do("PATCH", "/api/devices/C/folder", `{"folderId":"abc"}`), 400, invalidID)
		want(t, "move into no folder", admin.Do("PATCH", "/api/devices/C/folder", `{"folderId":99}`), 404, noFolder)
		want(t, "move", operator.Do("PATCH", "/api/devices/C/folder", `{"folderId":2}`), 200, ok)
		want(t, "move out", admin.Do("PATCH", "/api/devices/A/folder", `{}`), 200, ok)
		want(t, "out with folder 0", admin.Do("POST", "/api/folders/0/devices", `{"deviceIds":["B"]}`), 200, `{"success":true,"data":{"count":1}}`)
		if got := rows(t, db, `SELECT device_id FROM device_folder_assignments ORDER BY device_id`); got != "C" {
			t.Errorf("assignments: %q", got)
		}

		// Changing
		want(t, "change an unknown folder", admin.Do("PATCH", "/api/folders/99", `{"name":"x"}`), 404, noFolder)
		want(t, "change folder abc", admin.Do("PATCH", "/api/folders/abc", `{"name":"x"}`), 400, invalidID)
		want(t, "a null name", admin.Do("PATCH", "/api/folders/1", `{"name":null}`), 400, required)
		want(t, "a blank name", admin.Do("PATCH", "/api/folders/1", `{"name":"  "}`), 400, required)
		want(t, "change", admin.Do("PATCH", "/api/folders/1", `{"name":" Lab 2 ","icon":"box","allowed_users":[]}`), 200, ok)
		want(t, "change the second", admin.Do("PATCH", "/api/folders/2", `{}`), 200, ok)
		if got := rows(t, db, `SELECT name || '|' || note FROM device_groups WHERE guid LIKE 'folder_%' ORDER BY guid`); got != "Lab 2|BetterDesk folder access scope,Odd|BetterDesk folder access scope" {
			t.Errorf("folder groups after changing: %q", got)
		}

		// Deleting
		want(t, "delete an unknown folder", admin.Do("DELETE", "/api/folders/99", ""), 404, noFolder)
		want(t, "delete", admin.Do("DELETE", "/api/folders/2", ""), 200, ok)
		if got := rows(t, db, `SELECT guid FROM device_groups WHERE guid LIKE 'folder_%'`) + "|" + rows(t, db, `SELECT device_id FROM device_folder_assignments`); got != "folder_1|" {
			t.Errorf("after deleting: %q", got)
		}

		var audit []string
		for _, row := range w.AuditRows() {
			if strings.HasPrefix(row, "folder_") || strings.HasPrefix(row, "devices_") {
				audit = append(audit, row)
			}
		}
		if got := strings.Join(audit, "\n"); got != strings.Join([]string{
			"folder_created|Created folder:   Lab |127.0.0.1|1",
			"folder_created|Created folder: Odd|127.0.0.1|1",
			"devices_moved|Moved 2 device(s) to folder ID: 1|127.0.0.1|1",
			"devices_moved|Moved 1 device(s) to folder ID: 0|127.0.0.1|1",
			"folder_updated|Updated folder:  Lab 2 |127.0.0.1|1",
			"folder_updated|Updated folder: Odd|127.0.0.1|1",
			"folder_deleted|Deleted folder: Odd|127.0.0.1|1",
		}, "\n") {
			t.Errorf("audit:\n%s", got)
		}
	})
}

// TestTheDeviceScopeCheckComesFirst checks that PATCH /api/devices/{id}/folder
// is behind the device group's scope check, which looks for deleted
// devices too: for E, a deleted device in a group only e2e_viewer may see,
// the operator gets the check's 403 where the route alone, which finds no
// active E, would answer 404.
func TestTheDeviceScopeCheckComesFirst(t *testing.T) {
	db := storetest.Migrated(t)
	api := routetest.NewAPI()
	api.Fallback = func(c routetest.Call) routetest.Reply {
		switch c.Target {
		case "/api/peers?include_deleted=true":
			return routetest.Reply{Status: 200, Body: `{"peers":[{"id":"A","hostname":"a"},{"id":"E","hostname":"e","soft_deleted":true}]}`}
		case "/api/peers/A":
			return routetest.Reply{Status: 200, Body: `{"id":"A","hostname":"a"}`}
		}
		return routetest.Reply{Status: 404, Body: `{"error":"not found"}`}
	}
	w := routetest.New(t, routetest.Options{DB: db, API: api, Config: realLanguages})
	ctx := context.Background()
	for _, q := range []string{
		`INSERT INTO device_groups (guid, name, note, team_id, source_type, tag_filter) VALUES ('g-viewer', 'Viewer only', '', '', 'manual', '')`,
		`INSERT INTO device_group_user_access (device_group_id, user_id) SELECT id, 6 FROM device_groups WHERE guid = 'g-viewer'`,
		`INSERT INTO device_group_members (device_group_id, peer_id) SELECT id, 'E' FROM device_groups WHERE guid = 'g-viewer'`,
	} {
		if _, err := db.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	operator, admin := w.As("operator"), w.As("admin")
	api.Calls()
	want(t, "operator, deleted E", operator.Do("PATCH", "/api/devices/E/folder", `{"folderId":null}`), http.StatusForbidden, forbidden)
	if calls := strings.Join(api.Calls(), ","); calls != "GET /api/peers/E,GET /api/peers?include_deleted=true" {
		t.Errorf("operator, deleted E: calls %s", calls)
	}
	want(t, "admin, deleted E", admin.Do("PATCH", "/api/devices/E/folder", `{"folderId":null}`), http.StatusNotFound, `{"success":false,"error":"Device not found"}`)
	want(t, "operator, A", operator.Do("PATCH", "/api/devices/A/folder", `{"folderId":null}`), http.StatusOK, `{"success":true}`)
}

// TestScopeRefusalInEveryLanguage: the refusal of a device outside the
// account's scope is a sentence in the language of the request, in every
// language the console has, never the name of a text.
func TestScopeRefusalInEveryLanguage(t *testing.T) {
	w := newWorld(t, storetest.Migrated(t))
	admin, operator := w.As("admin"), w.As("operator")
	want(t, "create", admin.Do("POST", "/api/folders", `{"name":"Lab","allowed_users":["e2e_viewer"]}`),
		200, `{"success":true,"data":{"id":1,"name":"Lab","color":"#6366f1","icon":"folder","allowed_users":["e2e_viewer"],"allowed_groups":[]}}`)
	want(t, "assign", admin.Do("POST", "/api/folders/1/devices", `{"deviceIds":["A"]}`), 200, `{"success":true,"data":{"count":1}}`)
	files, err := os.ReadDir(langDir)
	if err != nil {
		t.Fatal(err)
	}
	english := ""
	for _, f := range files {
		code, isJSON := strings.CutSuffix(f.Name(), ".json")
		if !isJSON {
			continue
		}
		got := operator.Do("POST", "/api/folders/0/devices", `{"deviceIds":["A"]}`, "Accept-Language", code)
		var answer struct {
			Error string `json:"error"`
		}
		if err := json.Unmarshal([]byte(got.Body), &answer); err != nil || got.Status != http.StatusForbidden {
			t.Fatalf("%s: %d %s", code, got.Status, got.Body)
		}
		switch {
		case answer.Error == "" || answer.Error == "errors.forbidden":
			t.Errorf("%s: the refusal is %q", code, answer.Error)
		case code == "en":
			english = answer.Error
		}
		if code != "en" && english != "" && answer.Error == english {
			t.Errorf("%s: the refusal is the English text", code)
		}
	}
	if english != "You don't have access to this device." {
		t.Errorf("the English refusal is %q", english)
	}
}

// TestAssignStoresTheDevicesID: a device id the body gives as a number is
// stored as the device's id, so the device is in the folder, on SQLite
// too, where the number was stored as 900100021.0.
func TestAssignStoresTheDevicesID(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		api := routetest.NewAPI()
		api.Fallback = func(c routetest.Call) routetest.Reply {
			switch {
			case c.Target == "/api/peers":
				return routetest.Reply{Status: 200, Body: `{"peers":[{"id":"900100021","hostname":"n"}]}`}
			case c.Target == "/api/peers/900100021":
				return routetest.Reply{Status: 200, Body: `{"id":"900100021","hostname":"n"}`}
			case strings.HasPrefix(c.Target, "/api/remote-targets"):
				return routetest.Reply{Status: 200, Body: `{"targets":[]}`}
			}
			return routetest.Reply{Status: 404, Body: `{"error":"not found"}`}
		}
		admin := routetest.New(t, routetest.Options{DB: db, API: api, Config: realLanguages}).As("admin")
		want(t, "create", admin.Do("POST", "/api/folders", `{"name":"Lab"}`),
			200, `{"success":true,"data":{"id":1,"name":"Lab","color":"#6366f1","icon":"folder","allowed_users":[],"allowed_groups":[]}}`)
		want(t, "assign a number", admin.Do("POST", "/api/folders/1/devices", `{"deviceIds":[900100021]}`), 200, `{"success":true,"data":{"count":1}}`)
		if got := rows(t, db, `SELECT device_id FROM device_folder_assignments`); got != "900100021" {
			t.Errorf("stored device ids: %q", got)
		}
	})
}

// TestNameThatIsNoText: a folder name of any JSON type but text is
// refused like a missing one, when a folder is made and when it is
// changed, and nothing is stored.
func TestNameThatIsNoText(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		admin := newWorld(t, db).As("admin")
		for _, name := range []string{`5`, `true`, `["x"]`, `{"a":"x"}`} {
			want(t, "create with "+name, admin.Do("POST", "/api/folders", `{"name":`+name+`}`), 400, required)
		}
		want(t, "create", admin.Do("POST", "/api/folders", `{"name":"Lab"}`),
			200, `{"success":true,"data":{"id":1,"name":"Lab","color":"#6366f1","icon":"folder","allowed_users":[],"allowed_groups":[]}}`)
		for _, name := range []string{`null`, `5`, `false`, `[]`, `{}`} {
			want(t, "change to "+name, admin.Do("PATCH", "/api/folders/1", `{"name":`+name+`}`), 400, required)
		}
		if got := rows(t, db, `SELECT name FROM folders`); got != "Lab" {
			t.Errorf("stored names: %q", got)
		}
	})
}

// TestFolderIDsAreWholeNumbers: a folder id in the address or the body
// that is no whole number from 0 to 2147483647 is refused with 400 on
// both databases, before any query; one in range that names no folder is
// not found.
func TestFolderIDsAreWholeNumbers(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		admin := newWorld(t, db).As("admin")
		want(t, "create", admin.Do("POST", "/api/folders", `{"name":"Lab"}`),
			200, `{"success":true,"data":{"id":1,"name":"Lab","color":"#6366f1","icon":"folder","allowed_users":[],"allowed_groups":[]}}`)
		for _, id := range []string{"abc", "1.5", "-1", "1e3", "%201", "2147483648", "99999999999"} {
			want(t, "change folder "+id, admin.Do("PATCH", "/api/folders/"+id, `{"name":"x"}`), 400, invalidID)
			want(t, "delete folder "+id, admin.Do("DELETE", "/api/folders/"+id, ""), 400, invalidID)
			want(t, "assign to folder "+id, admin.Do("POST", "/api/folders/"+id+"/devices", `{"deviceIds":["A"]}`), 400, invalidID)
		}
		for _, id := range []string{`"abc"`, `1.5`, `-1`, `"1.0"`, `" 1"`, `2147483648`, `true`, `[1]`, `{}`} {
			want(t, "move into folder "+id, admin.Do("PATCH", "/api/devices/A/folder", `{"folderId":`+id+`}`), 400, invalidID)
		}
		want(t, "change folder 2147483647", admin.Do("PATCH", "/api/folders/2147483647", `{"name":"x"}`), 404, noFolder)
		want(t, "move into folder 2147483647", admin.Do("PATCH", "/api/devices/A/folder", `{"folderId":2147483647}`), 404, noFolder)
		want(t, "move into folder \"1\"", admin.Do("PATCH", "/api/devices/A/folder", `{"folderId":"1"}`), 200, ok)
		want(t, "move into folder 1.0", admin.Do("PATCH", "/api/devices/B/folder", `{"folderId":1.0}`), 200, ok)
		if got := rows(t, db, `SELECT device_id FROM device_folder_assignments ORDER BY device_id`); got != "A,B" {
			t.Errorf("assignments: %q", got)
		}
	})
}

// TestAccessListsOfMoreThan100: a folder whose access list names more than
// 100 different accounts or user groups is refused with 400 when it is
// made or changed, and nothing is written, where the first 100 were kept
// and the rest left out without a word; 100 are saved (BUGS PA10).
func TestAccessListsOfMoreThan100(t *testing.T) {
	list := func(n int, prefix string) string {
		items := make([]string, n)
		for i := range items {
			items[i] = `"` + prefix + strconv.Itoa(i) + `"`
		}
		return "[" + strings.Join(items, ",") + "]"
	}
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		admin := newWorld(t, db).As("admin")
		users := `{"success":false,"error":"allowed_users can hold at most 100 entries"}`
		groups := `{"success":false,"error":"allowed_groups can hold at most 100 entries"}`
		want(t, "create with 101 accounts", admin.Do("POST", "/api/folders", `{"name":"Lab","allowed_users":`+list(101, "user")+`}`), 400, users)
		want(t, "create with 101 user groups", admin.Do("POST", "/api/folders", `{"name":"Lab","allowed_groups":`+list(101, "ug-")+`}`), 400, groups)
		if got := rows(t, db, `SELECT name FROM folders`); got != "" {
			t.Errorf("folders after the refused creates: %q", got)
		}
		want(t, "create with 100 accounts", admin.Do("POST", "/api/folders", `{"name":"Lab","allowed_users":`+list(100, "user")+`}`),
			200, `{"success":true,"data":{"id":1,"name":"Lab","color":"#6366f1","icon":"folder","allowed_users":[],"allowed_groups":[]}}`)
		want(t, "change with 101 user groups", admin.Do("PATCH", "/api/folders/1", `{"name":"Renamed","allowed_groups":`+list(101, "ug-")+`}`), 400, groups)
		want(t, "change with 101 accounts", admin.Do("PATCH", "/api/folders/1", `{"name":"Renamed","allowed_users":`+list(101, "user")+`}`), 400, users)
		if got := rows(t, db, `SELECT name FROM folders`); got != "Lab" {
			t.Errorf("folders after the refused changes: %q", got)
		}
	})
}

// TestColorAndIconAreText: a folder color or icon that is no text is
// refused when a folder is made or changed, so what is stored is what the
// answer says on both databases.
func TestColorAndIconAreText(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		admin := newWorld(t, db).As("admin")
		color, icon := `{"success":false,"error":"Invalid folder color"}`, `{"success":false,"error":"Invalid folder icon"}`
		for _, body := range []string{`{"name":"x","color":5}`, `{"name":"x","color":["#fff"]}`, `{"name":"x","color":true}`, `{"name":"x","color":{}}`} {
			want(t, "create with "+body, admin.Do("POST", "/api/folders", body), 400, color)
		}
		for _, body := range []string{`{"name":"x","icon":5}`, `{"name":"x","icon":["box"]}`, `{"name":"x","color":"#fff","icon":false}`} {
			want(t, "create with "+body, admin.Do("POST", "/api/folders", body), 400, icon)
		}
		want(t, "create", admin.Do("POST", "/api/folders", `{"name":"Lab","color":null,"icon":"box"}`),
			200, `{"success":true,"data":{"id":1,"name":"Lab","color":"#6366f1","icon":"box","allowed_users":[],"allowed_groups":[]}}`)
		want(t, "change the color to a number", admin.Do("PATCH", "/api/folders/1", `{"color":7}`), 400, color)
		want(t, "change the icon to a list", admin.Do("PATCH", "/api/folders/1", `{"icon":["a"]}`), 400, icon)
		want(t, "change", admin.Do("PATCH", "/api/folders/1", `{"color":"#123"}`), 200, ok)
		if got := rows(t, db, `SELECT name || '|' || color || '|' || icon FROM folders`); got != "Lab|#123|box" {
			t.Errorf("stored folder: %q", got)
		}
	})
}
