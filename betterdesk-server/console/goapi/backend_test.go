package goapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
)

// fakeStore is the console's own tables for Backend.
type fakeStore struct {
	stats       any
	folders     map[string]any
	sysinfo     []*Object
	settings    map[string]string
	updated     []string
	failFolders bool
	failSysinfo bool
	failUpdate  bool
	failStats   bool
}

func (s *fakeStore) GetStats(context.Context) (any, error) {
	if s.failStats {
		return nil, errors.New("no table")
	}
	return s.stats, nil
}

func (s *fakeStore) GetAllFolderAssignments(context.Context) (map[string]any, error) {
	if s.failFolders {
		return nil, errors.New("no table")
	}
	return s.folders, nil
}

func (s *fakeStore) GetAllPeerSysinfo(context.Context) ([]*Object, error) {
	if s.failSysinfo {
		return nil, errors.New("no table")
	}
	return s.sysinfo, nil
}

func (s *fakeStore) UpdateDevice(_ context.Context, id string, data *Object) error {
	if s.failUpdate {
		return errors.New("locked")
	}
	out, _ := data.MarshalJSON()
	s.updated = append(s.updated, id+" "+string(out))
	return nil
}

func (s *fakeStore) SetSetting(_ context.Context, key, value string) error {
	if s.settings == nil {
		s.settings = map[string]string{}
	}
	s.settings[key] = value
	return nil
}

// routedAPI answers by path, and 404 for a path it does not know.
func routedAPI(answers map[string]string) *fakeAPI {
	return &fakeAPI{answer: func(w http.ResponseWriter, r *http.Request) {
		if body, ok := answers[r.Method+" "+r.URL.Path]; ok {
			answerJSON(200, body)(w, r)
			return
		}
		answerJSON(404, `{"error":"Peer not found"}`)(w, r)
	}}
}

const devicePeers = `[
	{"id":"B2","hostname":"beta","user":"Bob","os":"-","note":"second","last_online":"2026-11-02T00:00:00Z","live_online":true},
	{"id":"A1","hostname":"","user":"alice","os":"Linux","version":"","note":" ","last_online":"2026-11-03T00:00:00Z"},
	{"id":"C3","hostname":"Gamma","user":"","note":"","banned":true,"last_online":"2026-11-01T00:00:00Z"}
]`

const deviceTargets = `{"targets":[{"id":"rt_9","name":"alpha rdp","protocol":"rdp","host":"10.0.0.9","port":3389,"enabled":true,"last_test_at":""}]}`

func deviceBackend(store *fakeStore) (*Backend, *fakeAPI) {
	api := routedAPI(map[string]string{
		"GET /api/peers":          devicePeers,
		"GET /api/remote-targets": deviceTargets,
	})
	return NewBackend(New(api), store), api
}

func ids(devices []*Object) string {
	out := make([]string, len(devices))
	for i, d := range devices {
		out[i] = d.Str("id")
	}
	return strings.Join(out, ",")
}

func TestGetAllDevicesMergesAndOverlays(t *testing.T) {
	store := &fakeStore{
		folders: map[string]any{"A1": "7", "B2": "not-a-number", "rt_9": 3.0, "ZZ": 1.0},
		sysinfo: []*Object{
			NewObject("peer_id", "A1", "hostname", "alpha-host", "platform", "Linux", "os_full", "Ubuntu 24.04", "version", "1.4.6"),
			NewObject("peer_id", "B2", "hostname", "ignored", "platform", "Windows", "os_full", "Windows 11", "version", "9.9"),
		},
	}
	b, api := deviceBackend(store)
	devices := b.GetAllDevices(context.Background(), DeviceFilters{})

	if got := strings.Join(api.requests, " | "); got != "GET /api/peers | GET /api/remote-targets?org_id=&include_disabled=false" {
		t.Errorf("requests = %s", got)
	}
	// Ordered by last_online, newest first; the target has none and comes last.
	if got := ids(devices); got != "A1,B2,C3,rt_9" {
		t.Fatalf("devices = %s", got)
	}
	byID := map[string]*Object{}
	for _, d := range devices {
		byID[d.Str("id")] = d
	}
	// Folder: a number when the stored value is one, else as stored.
	if got := toJSON(t, []any{byID["A1"].Get("folder_id"), byID["B2"].Get("folder_id"), byID["C3"].Get("folder_id"), byID["rt_9"].Get("folder_id")}); got != `[7,"not-a-number",null,3]` {
		t.Errorf("folder ids = %s", got)
	}
	// System information fills what the API server left empty or "-".
	a1 := byID["A1"]
	if a1.Str("hostname") != "alpha-host" || a1.Str("version") != "1.4.6" || a1.Str("os") != "Linux" || a1.Str("platform") != "Linux" {
		t.Errorf("A1 after the overlay = %s", toJSON(t, a1))
	}
	b2 := byID["B2"]
	if b2.Str("hostname") != "beta" || b2.Str("os") != "Windows 11" || b2.Str("platform") != "Windows" || b2.Str("version") != "9.9" {
		t.Errorf("B2 after the overlay = %s", toJSON(t, b2))
	}
	if !byID["rt_9"].Bool("remote_target") {
		t.Errorf("the target was not normalised: %s", toJSON(t, byID["rt_9"]))
	}
}

func TestGetAllDevicesFiltersAndSorts(t *testing.T) {
	cases := []struct {
		name    string
		filters DeviceFilters
		want    string
		request string
	}{
		{"default order", DeviceFilters{}, "A1,B2,C3,rt_9", ""},
		{"ascending", DeviceFilters{SortOrder: "asc"}, "rt_9,C3,B2,A1", ""},
		{"by hostname, no matter the case", DeviceFilters{SortBy: "hostname", SortOrder: "asc"}, "A1,rt_9,B2,C3", ""},
		{"by hostname descending", DeviceFilters{SortBy: "hostname"}, "C3,B2,rt_9,A1", ""},
		{"by username", DeviceFilters{SortBy: "username", SortOrder: "asc"}, "C3,rt_9,A1,B2", ""},
		{"search in the id", DeviceFilters{Search: "b2"}, "B2", ""},
		{"search in the username", DeviceFilters{Search: "ALI"}, "A1", ""},
		{"search in the hostname", DeviceFilters{Search: "alpha"}, "rt_9", ""},
		{"search in the note", DeviceFilters{Search: "10.0.0"}, "rt_9", ""},
		{"online", DeviceFilters{Status: "online"}, "B2", ""},
		{"offline is not online and not banned", DeviceFilters{Status: "offline"}, "A1,rt_9", ""},
		{"banned", DeviceFilters{Status: "banned"}, "C3", ""},
		{"another status keeps all", DeviceFilters{Status: "all"}, "A1,B2,C3,rt_9", ""},
		{"with a note", DeviceFilters{HasNotes: true}, "B2,rt_9", ""},
		{"with deleted", DeviceFilters{IncludeDeleted: true}, "A1,B2,C3,rt_9",
			"GET /api/peers?include_deleted=true | GET /api/remote-targets?org_id=&include_disabled=true"},
	}
	for _, c := range cases {
		b, api := deviceBackend(&fakeStore{})
		if got := ids(b.GetAllDevices(context.Background(), c.filters)); got != c.want {
			t.Errorf("%s: devices = %s, want %s", c.name, got, c.want)
		}
		if got := strings.Join(api.requests, " | "); c.request != "" && got != c.request {
			t.Errorf("%s: requests = %s", c.name, got)
		}
	}
}

func TestGetAllDevicesSurvivesAFailingStoreAndAPI(t *testing.T) {
	b, _ := deviceBackend(&fakeStore{failFolders: true, failSysinfo: true})
	if got := ids(b.GetAllDevices(context.Background(), DeviceFilters{})); got != "A1,B2,C3,rt_9" {
		t.Errorf("with a failing store: %s", got)
	}
	down := NewBackend(New(&fakeAPI{answer: noAnswer}), &fakeStore{})
	if got := toJSON(t, down.GetAllDevices(context.Background(), DeviceFilters{})); got != `[]` {
		t.Errorf("with the API server down: %s", got)
	}
}

func TestGetDeviceByID(t *testing.T) {
	api := routedAPI(map[string]string{
		"GET /api/peers/A1":            `{"id":"A1","hostname":"alpha"}`,
		"GET /api/peers":               `[{"id":"GONE1","soft_deleted":true}]`,
		"GET /api/remote-targets/rt_9": `{"id":"rt_9","name":"alpha rdp","protocol":"rdp"}`,
	})
	store := &fakeStore{folders: map[string]any{"A1": 4.0, "rt_9": 5.0, "GONE1": "x"}}
	b := NewBackend(New(api), store)
	ctx := context.Background()

	if got := b.GetDeviceByID(ctx, "A1", false); got.Str("hostname") != "alpha" || toJSON(t, got.Get("folder_id")) != "4" {
		t.Errorf("peer = %s", toJSON(t, got))
	}
	// A target is not given a folder.
	if got := b.GetDeviceByID(ctx, "rt_9", false); !got.Bool("remote_target") || got.Has("folder_id") {
		t.Errorf("target = %s", toJSON(t, got))
	}
	api.requests = nil
	if got := b.GetDeviceByID(ctx, "GONE1", false); got != nil {
		t.Errorf("deleted peer without includeDeleted = %s", toJSON(t, got))
	}
	if got := strings.Join(api.requests, " | "); got != "GET /api/peers/GONE1" {
		t.Errorf("requests = %s", got)
	}
	api.requests = nil
	if got := b.GetDeviceByID(ctx, "GONE1", true); got == nil || !got.Bool("soft_deleted") || got.Get("folder_id") != "x" {
		t.Errorf("deleted peer with includeDeleted = %s", toJSON(t, got))
	}
	if got := strings.Join(api.requests, " | "); got != "GET /api/peers/GONE1 | GET /api/peers?include_deleted=true" {
		t.Errorf("requests = %s", got)
	}
	if got := b.GetDeviceByID(ctx, "NOSUCH", true); got != nil {
		t.Errorf("unknown peer = %s", toJSON(t, got))
	}
	store.failFolders = true
	if got := b.GetDeviceByID(ctx, "A1", false); got == nil || toJSON(t, got.Get("folder_id")) != "null" {
		t.Errorf("peer with a failing store = %s", toJSON(t, got))
	}
}

func TestGetStats(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name  string
		stats string
		want  string
	}{
		{"the API server's names", `{"peers_total":10,"peers_online":4,"peers_banned":2,"peers_online_live":9}`,
			`{"total":10,"online":4,"offline":6,"banned":2,"withNotes":0}`},
		{"older names", `{"total_peers":5,"online_peers":1,"banned_peers":1,"with_notes":3}`,
			`{"total":5,"online":1,"offline":4,"banned":1,"withNotes":3}`},
		{"live count when the stored one is missing", `{"total":3,"peers_online_live":2,"banned":0}`,
			`{"total":3,"online":2,"offline":1,"banned":0,"withNotes":0}`},
		{"a zero is a value", `{"peers_total":0,"total_peers":8,"peers_online":0,"online":5}`,
			`{"total":0,"online":0,"offline":0,"banned":0,"withNotes":0}`},
		{"nothing", `{}`, `{"total":0,"online":0,"offline":0,"banned":0,"withNotes":0}`},
	}
	for _, c := range cases {
		b := NewBackend(New(&fakeAPI{answer: answerJSON(200, c.stats)}), &fakeStore{})
		got, err := b.GetStats(ctx)
		if err != nil || toJSON(t, got) != c.want {
			t.Errorf("%s: %s, %v; want %s", c.name, toJSON(t, got), err, c.want)
		}
	}

	local := NewObject("total", 2.0, "online", 0.0)
	for name, answer := range map[string]func(http.ResponseWriter, *http.Request){
		"failing": answerJSON(500, `{}`), "down": noAnswer, "error in a 200": answerJSON(200, `{"error":"x"}`), "empty": answerJSON(200, ``),
	} {
		b := NewBackend(New(&fakeAPI{answer: answer}), &fakeStore{stats: local})
		if got, err := b.GetStats(ctx); err != nil || got != any(local) {
			t.Errorf("%s API server: %v, %v; want the console's own counts", name, got, err)
		}
	}
	b := NewBackend(New(&fakeAPI{answer: noAnswer}), &fakeStore{failStats: true})
	if _, err := b.GetStats(ctx); err == nil {
		t.Error("no error when both sources fail")
	}
}

func TestUpdateDevice(t *testing.T) {
	ctx := context.Background()
	store := &fakeStore{}
	api := &fakeAPI{answer: answerJSON(200, `{"status":"updated"}`)}
	b := NewBackend(New(api), store)

	data := NewObject("note", 5.0, "display_name", "Desk", "folder_id", 3.0)
	if got := toJSON(t, b.UpdateDevice(ctx, "P1", data)); got != `{"changes":1}` {
		t.Errorf("result = %s", got)
	}
	if got := strings.Join(api.requests, "|") + " " + strings.Join(api.bodies, "|"); got != `PATCH /api/peers/P1 {"note":"5","display_name":"Desk"}` {
		t.Errorf("request = %s", got)
	}
	if got := strings.Join(store.updated, "|"); got != `P1 {"note":5,"display_name":"Desk","folder_id":3}` {
		t.Errorf("console row update = %s", got)
	}

	// Nothing for the API server: no call, the console's row is still written.
	api.requests, store.updated = nil, nil
	if got := toJSON(t, b.UpdateDevice(ctx, "P1", NewObject("folder_id", 3.0))); got != `{"changes":1}` || len(api.requests) != 0 || len(store.updated) != 1 {
		t.Errorf("without fields for the API server: %s, requests %q, updates %q", got, api.requests, store.updated)
	}

	// The API server refuses: its error, and the console's row is left alone.
	store.updated = nil
	refused := NewBackend(New(&fakeAPI{answer: answerJSON(400, `{"error":"No fields to update"}`)}), store)
	if got := toJSON(t, refused.UpdateDevice(ctx, "P1", NewObject("user", "u"))); got != `{"changes":0,"error":"No fields to update"}` || len(store.updated) != 0 {
		t.Errorf("refused: %s, updates %q", got, store.updated)
	}
	empty := NewBackend(New(&fakeAPI{answer: answerJSON(200, `{"error":""}`)}), store)
	if got := toJSON(t, empty.UpdateDevice(ctx, "P1", NewObject("user", "u"))); got != `{"changes":0,"error":"Failed to update peer"}` {
		t.Errorf("refused without a text: %s", got)
	}

	// A failing write to the console's row does not fail the update.
	store.failUpdate = true
	if got := toJSON(t, b.UpdateDevice(ctx, "P1", NewObject("note", "n"))); got != `{"changes":1}` {
		t.Errorf("with a failing store: %s", got)
	}
}

func TestBackendPassesThrough(t *testing.T) {
	ctx := context.Background()
	store := &fakeStore{}
	api := &fakeAPI{answer: answerJSON(200, okBody)}
	b := NewBackend(New(api), store)

	if b.GetActiveBackend() != "betterdesk" || !b.IsBetterDesk() {
		t.Error("the backend is not betterdesk")
	}
	if err := b.SetActiveBackend(ctx, "rustdesk"); err == nil || err.Error() != "Invalid backend: rustdesk. Only 'betterdesk' is supported." {
		t.Errorf("SetActiveBackend(rustdesk) = %v", err)
	}
	if err := b.SetActiveBackend(ctx, "betterdesk"); err != nil || store.settings["server_backend"] != "betterdesk" {
		t.Errorf("SetActiveBackend(betterdesk) = %v, settings %v", err, store.settings)
	}
	if got := toJSON(t, b.SyncOnlineStatus()); got != `{"synced":0,"skipped":true,"reason":"betterdesk_manages_state"}` {
		t.Errorf("SyncOnlineStatus = %s", got)
	}

	calls := []struct {
		name string
		call func()
		want string
		body string
	}{
		{"GetHealth", func() { b.GetHealth(ctx) }, "GET /api/health", ""},
		{"GetServerInfo", nil, "", ""},
		{"DeleteDevice", func() { b.DeleteDevice(ctx, "P1", DeleteOptions{Revoke: true}) }, "DELETE /api/peers/P1?revoke=true", ""},
		{"RestoreDevice", func() { b.RestoreDevice(ctx, "P1") }, "POST /api/peers/P1/restore", ""},
		{"SetBanStatus on", func() { b.SetBanStatus(ctx, "P1", true, "why") }, "POST /api/peers/P1/ban", `{"reason":"why"}`},
		{"SetBanStatus off", func() { b.SetBanStatus(ctx, "P1", false, "why") }, "POST /api/peers/P1/unban", ""},
		{"ChangePeerID", func() { b.ChangePeerID(ctx, "P1", "P2") }, "POST /api/peers/P1/change-id", `{"new_id":"P2"}`},
		{"GetStatusSummary", func() { b.GetStatusSummary(ctx) }, "GET /api/peers/status/summary", ""},
		{"GetBlocklist", func() { b.GetBlocklist(ctx) }, "GET /api/blocklist", ""},
		{"AddBlocklistEntry", func() { b.AddBlocklistEntry(ctx, "1.2.3.4") }, "POST /api/blocklist", `{"entry":"1.2.3.4"}`},
		{"RemoveBlocklistEntry", func() { b.RemoveBlocklistEntry(ctx, "1.2.3.4") }, "DELETE /api/blocklist/1.2.3.4", ""},
		{"SetPeerTags", func() { b.SetPeerTags(ctx, "P1", "a,b") }, "PUT /api/peers/P1/tags", `{"tags":["a","b"]}`},
		{"GetPeersByTag", func() { b.GetPeersByTag(ctx, "a") }, "GET /api/tags/a/peers", ""},
		{"GetAuditEvents", func() { b.GetAuditEvents(ctx, 5.0) }, "GET /api/audit/events?limit=5", ""},
	}
	for _, c := range calls {
		if c.call == nil {
			continue
		}
		api.requests, api.bodies = nil, nil
		c.call()
		if len(api.requests) != 1 || api.requests[0] != c.want || api.bodies[0] != c.body {
			t.Errorf("%s: requests %q bodies %q, want %s %s", c.name, api.requests, api.bodies, c.want, c.body)
		}
	}
	if got := toJSON(t, b.GetServerInfo(ctx)); got != `{"health":{"ok":true},"stats":{"ok":true},"backend":"betterdesk"}` {
		t.Errorf("GetServerInfo = %s", got)
	}
}
