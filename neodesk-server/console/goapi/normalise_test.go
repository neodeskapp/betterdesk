package goapi

import (
	"testing"
	"time"
)

var fixedNow = time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)

func mustParse(t *testing.T, s string) any {
	t.Helper()
	v, err := Parse([]byte(s))
	if err != nil {
		t.Fatalf("parse %s: %v", s, err)
	}
	return v
}

func TestNormalisePeerShape(t *testing.T) {
	// A peer as GET /api/peers sends it.
	in := `{"id":"PEERAAA1","uuid":"uuid-a","pk":"AAEC","ip":"10.0.0.5","user":"alice","hostname":"host-a","os":"Windows",
		"version":"1.4.6","status":1,"nat_type":2,"last_online":"2020-01-01T00:00:00Z","created_at":"2026-11-10T01:05:50Z",
		"disabled":false,"banned":false,"ban_reason":"","soft_deleted":false,"note":"n1","tags":"t1, t2,,","display_name":"Desk",
		"device_type":"","status_text":"OFFLINE","live_online":false,"signal_ready":false,"live_status":"offline",
		"platform":"Windows","cdap_connected":false,"mesh_connected":false}`
	want := `{"id":"PEERAAA1","hostname":"host-a","display_name":"Desk","username":"alice","platform":"Windows","os":"Windows",` +
		`"version":"1.4.6","ip":"10.0.0.5","note":"n1","online":false,"signal_ready":false,"banned":false,"no_signal":false,` +
		`"created_at":"2026-11-10T01:05:50Z","last_online":"2020-01-01T00:00:00Z","ban_reason":"","banned_at":null,"folder_id":null,` +
		`"tags":["t1","t2"],"status_tier":"offline","uuid":"uuid-a","nat_type":2,"disabled":false,"soft_deleted":false,` +
		`"deleted_at":null,"device_type":"rustdesk","client_type":"rustdesk","cdap_connected":false,"mesh_connected":false,` +
		`"mesh_node_id":"","linked_peer_id":""}`
	if got := toJSON(t, normalisePeer(mustParse(t, in), fixedNow)); got != want {
		t.Errorf("normalised peer =\n %s\nwant\n %s", got, want)
	}

	// Nothing but an id: every field is there with its default.
	wantEmpty := `{"id":"X","hostname":"","display_name":"","username":"","platform":"","os":"","version":"","ip":"","note":"",` +
		`"online":false,"signal_ready":false,"banned":false,"no_signal":false,"created_at":"","last_online":"","ban_reason":"",` +
		`"banned_at":null,"folder_id":null,"tags":[],"status_tier":"offline","uuid":"","nat_type":0,"disabled":false,` +
		`"soft_deleted":false,"deleted_at":null,"device_type":"rustdesk","client_type":"rustdesk","cdap_connected":false,` +
		`"mesh_connected":false,"mesh_node_id":"","linked_peer_id":""}`
	if got := toJSON(t, normalisePeer(mustParse(t, `{"id":"X"}`), fixedNow)); got != wantEmpty {
		t.Errorf("peer with only an id =\n %s\nwant\n %s", got, wantEmpty)
	}
}

func TestNormalisePeerFields(t *testing.T) {
	recent := fixedNow.Add(-2 * time.Minute).Format(time.RFC3339)
	old := fixedNow.Add(-6 * time.Minute).Format(time.RFC3339)
	cases := []struct {
		name string
		in   string
		want map[string]string
	}{
		{"online", `{"live_online":true,"live_status":"degraded","last_online":"` + recent + `"}`,
			map[string]string{"online": "true", "signal_ready": "true", "no_signal": "false", "status_tier": `"degraded"`}},
		{"online without signal", `{"live_online":true,"signal_ready":false}`,
			map[string]string{"online": "true", "signal_ready": "false", "status_tier": `"online"`}},
		{"status tier falls back to the stored status", `{"status_text":"OFFLINE"}`, map[string]string{"status_tier": `"OFFLINE"`}},
		{"seen two minutes ago", `{"last_online":"` + recent + `"}`, map[string]string{"online": "false", "no_signal": "true"}},
		{"seen six minutes ago", `{"last_online":"` + old + `"}`, map[string]string{"no_signal": "false"}},
		{"seen in the future", `{"last_online":"2030-01-01T00:00:00Z"}`, map[string]string{"no_signal": "true"}},
		// The signal server refuses a blocked ID: no "no signal", the reason instead (L550)
		{"recent but blocked", `{"last_online":"` + recent + `","blocked":"device_revoked"}`,
			map[string]string{"no_signal": "false", "online": "false", "blocked": `"device_revoked"`}},
		{"recent but banned", `{"last_online":"` + recent + `","banned":true}`, map[string]string{"no_signal": "false", "banned": "true"}},
		{"recent agent on CDAP", `{"last_online":"` + recent + `","device_type":"os_agent","cdap_connected":true}`,
			map[string]string{"no_signal": "false", "device_type": `"os_agent"`, "client_type": `"os_agent"`, "cdap_connected": "true"}},
		{"recent agent off CDAP", `{"last_online":"` + recent + `","device_type":"os_agent"}`, map[string]string{"no_signal": "true"}},
		{"recent mesh agent", `{"last_online":"` + recent + `","device_type":"mesh_agent","mesh_connected":true,"mesh_node_id":"n1"}`,
			map[string]string{"no_signal": "false", "mesh_connected": "true", "mesh_node_id": `"n1"`}},
		{"last online in milliseconds", `{"last_online":` + itoa(int(fixedNow.Add(-time.Minute).UnixMilli())) + `}`, map[string]string{"no_signal": "true"}},
		{"last online without a zone", `{"last_online":"` + fixedNow.Add(-time.Minute).In(time.Local).Format("2006-01-02 15:04:05") + `"}`, map[string]string{"no_signal": "true"}},
		{"last online that is no date", `{"last_online":"yesterday"}`, map[string]string{"no_signal": "false", "last_online": `"yesterday"`}},
		{"device type is trimmed and lowered", `{"device_type":"  NeoDesk "}`, map[string]string{"device_type": `"neodesk"`}},
		{"tags as a list", `{"tags":["a",1]}`, map[string]string{"tags": `["a",1]`}},
		{"tags as a JSON list in text", `{"tags":"[\"a\",\"b\"]"}`, map[string]string{"tags": `["a","b"]`}},
		{"tags as JSON that is no list", `{"tags":"123"}`, map[string]string{"tags": `["123"]`}},
		{"tags empty", `{"tags":""}`, map[string]string{"tags": `[]`}},
		{"tags null", `{"tags":null}`, map[string]string{"tags": `[]`}},
		{"soft deleted counts as disabled", `{"soft_deleted":true,"deleted_at":"2026-01-01T00:00:00Z"}`,
			map[string]string{"disabled": "true", "soft_deleted": "true", "deleted_at": `"2026-01-01T00:00:00Z"`}},
		{"disabled", `{"disabled":true}`, map[string]string{"disabled": "true", "soft_deleted": "false"}},
		{"banned", `{"banned":true,"ban_reason":"r","banned_at":"2026-01-01T00:00:00Z"}`,
			map[string]string{"banned": "true", "ban_reason": `"r"`, "banned_at": `"2026-01-01T00:00:00Z"`}},
		{"folder and link", `{"folder_id":3,"linked_peer_id":"P2","user":"bob"}`,
			map[string]string{"folder_id": "3", "linked_peer_id": `"P2"`, "username": `"bob"`}},
		{"os feeds platform, not Go's platform", `{"os":"Linux","platform":"ignored"}`, map[string]string{"platform": `"Linux"`, "os": `"Linux"`}},
	}
	for _, c := range cases {
		got := normalisePeer(mustParse(t, c.in), fixedNow)
		for key, want := range c.want {
			if g := toJSON(t, got.Get(key)); g != want {
				t.Errorf("%s: %s = %s, want %s", c.name, key, g, want)
			}
		}
		for _, dropped := range []string{"pk", "status", "status_text", "user", "live_online", "live_status"} {
			if got.Has(dropped) {
				t.Errorf("%s: the result has %q", c.name, dropped)
			}
		}
	}
}

func TestNormalisePeerNull(t *testing.T) {
	for _, in := range []any{nil, "", false, 0.0} {
		if got := normalisePeer(in, fixedNow); got != nil {
			t.Errorf("normalisePeer(%#v) = %s, want nil", in, toJSON(t, got))
		}
		if got := NormalisePeer(in); got != nil {
			t.Errorf("NormalisePeer(%#v) = %#v, want nil", in, got)
		}
	}
	if got, ok := NormalisePeer(mustParse(t, `{"id":"X"}`)).(*Object); !ok || got.Str("id") != "X" {
		t.Errorf("NormalisePeer of a peer = %#v", got)
	}
	list := normalisePeers([]any{mustParse(t, `{"id":"A"}`), nil}, fixedNow)
	if len(list) != 2 || list[0].Str("id") != "A" || list[1] != nil {
		t.Errorf("normalisePeers = %s", toJSON(t, list))
	}
}

func TestNormaliseRemoteTarget(t *testing.T) {
	in := `{"id":"rt_1","org_id":"","name":"Server","protocol":"RDP","host":"10.0.0.9","port":3389,"enabled":true,
		"platform":"windows","last_test_at":"2026-11-01T00:00:00Z","credential_mode":"prompt"}`
	want := `{"id":"rt_1","org_id":"","name":"Server","protocol":"RDP","host":"10.0.0.9","port":3389,"enabled":true,` +
		`"platform":"windows","last_test_at":"2026-11-01T00:00:00Z","credential_mode":"prompt","hostname":"Server",` +
		`"display_name":"Server","os":"windows","device_type":"rdp","remote_target":true,"remote_protocol":"rdp",` +
		`"soft_deleted":false,"online":false,"status":"OFFLINE","last_online":"2026-11-01T00:00:00Z","note":"10.0.0.9:3389"}`
	if got := toJSON(t, normaliseRemoteTarget(mustParse(t, in))); got != want {
		t.Errorf("normalised target =\n %s\nwant\n %s", got, want)
	}

	cases := []struct {
		name string
		in   string
		want map[string]string
	}{
		{"disabled", `{"enabled":false}`, map[string]string{"soft_deleted": "true", "status": `"DISABLED"`}},
		{"no name", `{"host":"h.example"}`, map[string]string{"hostname": `"h.example"`, "display_name": `"h.example"`, "note": `"h.example:"`}},
		{"nothing", `{}`, map[string]string{"id": `""`, "hostname": `""`, "platform": `""`, "os": `""`, "device_type": `""`,
			"last_online": `""`, "note": `""`, "status": `"OFFLINE"`, "soft_deleted": "false", "remote_target": "true"}},
		{"numeric id", `{"id":12}`, map[string]string{"id": `"12"`}},
		{"null", `null`, map[string]string{"id": `""`, "status": `"OFFLINE"`}},
	}
	for _, c := range cases {
		got := normaliseRemoteTarget(mustParse(t, c.in))
		for key, want := range c.want {
			if g := toJSON(t, got.Get(key)); g != want {
				t.Errorf("%s: %s = %s, want %s", c.name, key, g, want)
			}
		}
	}
}

func TestJSDate(t *testing.T) {
	utc := func(s string) float64 {
		parsed, err := time.Parse(time.RFC3339Nano, s)
		if err != nil {
			t.Fatal(err)
		}
		return float64(parsed.UnixMilli())
	}
	local := func(s string) float64 {
		parsed, err := time.ParseInLocation("2006-01-02 15:04:05", s, time.Local)
		if err != nil {
			t.Fatal(err)
		}
		return float64(parsed.UnixMilli())
	}
	cases := []struct {
		in   any
		want float64
	}{
		{"2026-11-10T01:05:50Z", utc("2026-11-10T01:05:50Z")},
		{"2026-11-10T01:05:50.123456789Z", utc("2026-11-10T01:05:50.123Z")},
		{"2026-11-10T01:05:50+02:00", utc("2026-11-09T23:05:50Z")},
		{"2026-11-10T01:05Z", utc("2026-11-10T01:05:00Z")},
		{"2026-11-10", utc("2026-11-10T00:00:00Z")},
		{"2026-11-10 01:05:50", local("2026-11-10 01:05:50")},
		{"2026-11-10T01:05:50", local("2026-11-10 01:05:50")},
		{1794272750000.0, 1794272750000},
		{1.5, 1},
	}
	for _, c := range cases {
		if got, ok := jsDate(c.in); !ok || got != c.want {
			t.Errorf("jsDate(%#v) = %v, %v; want %v", c.in, got, ok, c.want)
		}
	}
	for _, bad := range []any{"", "never", "2026-13-40", 9e15 * 10, NewObject(), nil} {
		if _, ok := jsDate(bad); ok {
			t.Errorf("jsDate(%#v) is a date", bad)
		}
	}
}
