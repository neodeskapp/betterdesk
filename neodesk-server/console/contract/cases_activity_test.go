//go:build contract

package contract_test

import (
	"encoding/json"
	"fmt"
	"net/url"

	"github.com/unitronix/betterdesk-server/console/contract"
)

// Activity: the three routes people read (the summaries of all devices,
// one device's sessions and summaries, its most used apps). The upload
// route of the desktop agent takes a device token; it is the group
// activity-device below, which runs after this one, so its reports do not
// reach the answers here.
//
// The demo data spreads a week of app usage over working hours, counted
// back from the moment an instance starts. So which sessions exist, their
// numbers and their order change with the day and the hour, and two
// recordings made minutes apart agree on them by chance. The cases mark
// those values by hand and pin down what does not move: the keys and
// types of a row, the form of every time, what a limit cuts off, and the
// answers for windows and devices that have nothing.
func init() {
	// A session row and a summary row of a device, value by value
	session := func(list string) []string {
		return []string{
			list + "[*].id", "~" + list + "[*].app_name", "~" + list + "[*].window_title", "~" + list + "[*].category",
			list + "[*].started_at", list + "[*].ended_at", list + "[*].duration_secs", list + "[*].created_at",
		}
	}
	// Every session and every summary of a device: how many there are
	// changes too
	whole := []string{"~sessions", "~summaries", "session_count"}
	// A list cut to its first rows, with all summaries
	cut := append(session("sessions"), "~summaries")
	app := []string{"~top_apps[*].app_name", "~top_apps[*].category", "top_apps[*].total_secs", "top_apps[*].session_count"}
	few := contract.Roles("signed-out", "guest_issuer", "viewer", "admin")
	const device = "/api/activity/900100001"

	contract.Group("activity",
		// The summaries of all devices
		contract.Case{Name: "list", Path: "/api/activity", Volatile: []string{"~summaries", "total"}},
		contract.Case{Name: "list-head", Method: "HEAD", Path: "/api/activity"},
		contract.Case{Name: "list-other-spelling", Path: "/API/Activity/", Volatile: []string{"~summaries", "total"}, Roles: few},
		contract.Case{Name: "list-window-later", Path: "/api/activity", Query: url.Values{"from": {"2100-01-01"}}},
		contract.Case{Name: "list-window-earlier", Path: "/api/activity", Query: url.Values{"to": {"2000-01-01"}}},
		contract.Case{
			Name: "list-window-around", Path: "/api/activity", Query: url.Values{"from": {"2000-01-01"}, "to": {"2100-01-01"}},
			Volatile: []string{"~summaries", "total"},
		},
		contract.Case{Name: "list-window-upside-down", Path: "/api/activity", Query: url.Values{"from": {"2100-01-01"}, "to": {"2000-01-01"}}},
		contract.Case{Name: "list-from-empty", Path: "/api/activity", Query: url.Values{"from": {""}, "to": {"2000-01-01"}}},
		contract.Case{Name: "list-from-not-a-time", Path: "/api/activity", Query: url.Values{"from": {"zzz"}}},
		contract.Case{Name: "list-from-twice", Path: "/api/activity", Query: url.Values{"from": {"2100-01-01", "2100-01-02"}}},
		contract.Case{Name: "list-from-in-brackets", Path: "/api/activity", Query: url.Values{"from[]": {"2100-01-01"}}},
		contract.Case{Name: "list-from-object", Path: "/api/activity", Query: url.Values{"from[day]": {"2100-01-01"}}},
		contract.Case{Name: "list-to-twice", Path: "/api/activity", Query: url.Values{"to": {"2000-01-01", "2000-01-02"}}},

		// One device
		contract.Case{Name: "detail", Path: device, Volatile: whole},
		contract.Case{Name: "detail-head", Method: "HEAD", Path: device, Roles: few},
		contract.Case{Name: "detail-other-spelling", Path: "/API/ACTIVITY/900100001/", Volatile: whole, Roles: few},
		contract.Case{Name: "detail-limit-two", Path: device, Query: url.Values{"limit": {"2"}}, Volatile: cut},
		contract.Case{Name: "detail-limit-fraction", Path: device, Query: url.Values{"limit": {"1.9"}}, Volatile: cut},
		contract.Case{Name: "detail-limit-with-text", Path: device, Query: url.Values{"limit": {" 3 rows"}}, Volatile: cut},
		contract.Case{Name: "detail-limit-twice", Path: device, Query: url.Values{"limit": {"1", "2"}}, Volatile: cut},
		contract.Case{Name: "detail-limit-zero", Path: device, Query: url.Values{"limit": {"0"}}, Volatile: whole},
		contract.Case{Name: "detail-limit-empty", Path: device, Query: url.Values{"limit": {""}}, Volatile: whole},
		contract.Case{Name: "detail-limit-not-a-number", Path: device, Query: url.Values{"limit": {"abc"}}, Volatile: whole},
		contract.Case{Name: "detail-limit-negative", Path: device, Query: url.Values{"limit": {"-1"}}, Volatile: whole},
		contract.Case{Name: "detail-limit-hex", Path: device, Query: url.Values{"limit": {"0x10"}}, Volatile: whole},
		contract.Case{Name: "detail-limit-too-large", Path: device, Query: url.Values{"limit": {"99999999999999999999"}}},
		contract.Case{Name: "detail-limit-object", Path: device, Query: url.Values{"limit[rows]": {"1"}}, Volatile: whole},
		contract.Case{Name: "detail-window-later", Path: device, Query: url.Values{"from": {"2100-01-01"}}},
		contract.Case{Name: "detail-window-earlier", Path: device, Query: url.Values{"to": {"2000-01-01"}, "limit": {"5"}}},
		contract.Case{
			Name: "detail-window-around", Path: device, Query: url.Values{"from": {"2000-01-01"}, "to": {"2100-01-01"}, "limit": {"1"}},
			Volatile: cut,
		},
		contract.Case{Name: "detail-from-twice", Path: device, Query: url.Values{"from": {"2100-01-01", "2100-01-02"}}},
		contract.Case{Name: "detail-from-in-brackets", Path: device, Query: url.Values{"from[]": {"2100-01-01"}}},
		contract.Case{Name: "detail-from-object", Path: device, Query: url.Values{"from[day]": {"2100-01-01"}}},
		contract.Case{Name: "detail-without-activity", Path: "/api/activity/900100002"},
		contract.Case{Name: "detail-unknown-device", Path: "/api/activity/no%20such%2Fdevice"},
		// The upload route of the agent is POST /api/activity/activity;
		// read, the same address is the device named "activity"
		contract.Case{Name: "detail-of-the-upload-address", Path: "/api/activity/activity"},

		// The most used apps of a device
		contract.Case{Name: "top", Path: device + "/top", Volatile: []string{"~top_apps"}},
		contract.Case{Name: "top-head", Method: "HEAD", Path: device + "/top", Roles: few},
		contract.Case{Name: "top-other-spelling", Path: "/Api/Activity/900100001/Top/", Volatile: []string{"~top_apps"}, Roles: few},
		contract.Case{Name: "top-limit-one", Path: device + "/top", Query: url.Values{"limit": {"1"}}, Volatile: app},
		contract.Case{Name: "top-limit-three", Path: device + "/top", Query: url.Values{"limit": {"3"}}, Volatile: app},
		contract.Case{Name: "top-limit-twice", Path: device + "/top", Query: url.Values{"limit": {"2", "9"}}, Volatile: app},
		contract.Case{Name: "top-limit-zero", Path: device + "/top", Query: url.Values{"limit": {"0"}}, Volatile: []string{"~top_apps"}},
		contract.Case{Name: "top-limit-not-a-number", Path: device + "/top", Query: url.Values{"limit": {"abc"}}, Volatile: []string{"~top_apps"}},
		contract.Case{Name: "top-limit-negative", Path: device + "/top", Query: url.Values{"limit": {"-5"}}, Volatile: []string{"~top_apps"}},
		contract.Case{Name: "top-limit-exponent", Path: device + "/top", Query: url.Values{"limit": {"1e30"}, "from": {"2000-01-01"}}, Volatile: app},
		contract.Case{Name: "top-limit-beyond-integers", Path: device + "/top", Query: url.Values{"limit": {"99999999999999999999"}}},
		contract.Case{Name: "top-window-later", Path: device + "/top", Query: url.Values{"from": {"2100-01-01"}}},
		contract.Case{Name: "top-window-earlier", Path: device + "/top", Query: url.Values{"to": {"2000-01-01"}}},
		contract.Case{
			Name: "top-window-around", Path: device + "/top", Query: url.Values{"from": {"2000-01-01"}, "to": {"2100-01-01"}, "limit": {"2"}},
			Volatile: app,
		},
		contract.Case{Name: "top-to-twice", Path: device + "/top", Query: url.Values{"to": {"2000-01-01", "2000-01-02"}}},
		contract.Case{Name: "top-to-object", Path: device + "/top", Query: url.Values{"to[day]": {"2000-01-01"}}},
		contract.Case{Name: "top-without-activity", Path: "/api/activity/900100002/top"},
		contract.Case{Name: "top-unknown-device", Path: "/api/activity/no%20such%2Fdevice/top"},
	)
}

// activity-device: the route the desktop agent reports its app usage
// with, POST /activity at /api/bd and at /api/activity, behind the device
// guards: a Bearer token bound to a device, then the device the request
// names (the body's device_id, else the query's, else X-Device-Id)
// compared as text. The route compares the body's device_id once more,
// as it is, so a number passes the guard and is refused by the route.
//
// The device is the instance's own: the token of 900100001 in
// /stack/secrets/device_token, that of 900100002 for another device. As a
// device (signed out, no cookies) it reports, and sends what the route
// refuses or stores oddly; then each role's session in front of a
// device's report. Every report is for a day in 2011, outside every
// window the reports group asks for, and the sessions and summaries it
// leaves are older than anything the activity cases above read.
func init() {
	const bd = "/api/bd"
	const own = "/api/activity"
	device := map[string]string{"Authorization": "Bearer {secret:device_token}"}
	other := map[string]string{"Authorization": "Bearer {secret:device_token_other}"}
	unknown := map[string]string{"Authorization": "Bearer contract-no-such-token"}
	basic := map[string]string{"Authorization": "Basic {secret:device_token}"}
	named := map[string]string{"X-Device-Id": "900100001"}
	none := map[string]string{}
	with := func(headers map[string]string, name, value string) map[string]string {
		out := map[string]string{name: value}
		for k, v := range headers {
			out[k] = v
		}
		return out
	}

	session := func(minute int) map[string]any {
		return map[string]any{
			"app_name": "Contract Editor", "window_title": "notes.txt", "category": "productivity",
			"started_at": fmt.Sprintf("2011-03-04T09:%02d:00.000Z", minute), "ended_at": fmt.Sprintf("2011-03-04T09:%02d:30.000Z", minute),
			"duration_secs": 30,
		}
	}
	report := func(changes map[string]any) map[string]any {
		body := map[string]any{"device_id": "900100001", "sessions": []any{session(1), session(2)}, "idle_seconds": 120, "timestamp": "2011-03-04T10:00:00.000Z"}
		for key, value := range changes {
			if value == nil {
				delete(body, key)
				continue
			}
			body[key] = value
		}
		return body
	}
	// asDevice sends a case the way a device does: no session, no cookies,
	// no CSRF token, the device's token unless the case names other headers
	asDevice := func(c contract.Case) contract.Case {
		c.NoCSRF, c.NoCookies, c.Roles = true, true, contract.Roles("signed-out")
		c.Method = "POST"
		if c.Headers == nil {
			c.Headers = device
		}
		if c.Path == "" {
			c.Path = bd + "/activity"
		}
		return c
	}
	sent := func(name string, changes map[string]any) contract.Case {
		return asDevice(contract.Case{Name: name, Body: report(changes)})
	}
	null := json.RawMessage("null")

	contract.Group("activity-device",
		sent("report", nil),
		asDevice(contract.Case{Name: "report-other-spelling", Path: "/API/BD/Activity/", Body: report(map[string]any{"timestamp": "2011-03-04T10:01:00.000Z"})}),
		asDevice(contract.Case{Name: "report-at-activity", Path: own + "/activity", Body: report(nil)}),

		// Who may report
		asDevice(contract.Case{Name: "report-without-token", Body: report(nil), Headers: none}),
		asDevice(contract.Case{Name: "report-unknown-token", Body: report(nil), Headers: unknown}),
		asDevice(contract.Case{Name: "report-basic", Body: report(nil), Headers: basic}),
		asDevice(contract.Case{Name: "report-device-id-alone", Body: report(nil), Headers: named}),
		asDevice(contract.Case{Name: "report-as-other-device", Body: report(nil), Headers: other}),
		asDevice(contract.Case{Name: "report-as-other-device-without-device-id", Body: report(map[string]any{"device_id": nil}), Headers: other}),

		// The device the request names: the guard, then the route
		sent("report-other-device", map[string]any{"device_id": "900100002"}),
		sent("report-device-id-number", map[string]any{"device_id": 900100001}),
		sent("report-device-id-in-a-list", map[string]any{"device_id": []any{"900100001"}}),
		sent("report-device-id-no-text", map[string]any{"device_id": map[string]any{"toString": 1}}),
		sent("report-without-device-id", map[string]any{"device_id": nil}),
		sent("report-device-id-empty", map[string]any{"device_id": ""}),
		asDevice(contract.Case{Name: "report-other-device-in-the-query", Query: url.Values{"device_id": {"900100002"}}, Body: report(nil)}),
		asDevice(contract.Case{Name: "report-other-device-in-the-header", Body: report(nil), Headers: with(device, "X-Device-Id", "900100002")}),
		asDevice(contract.Case{Name: "report-own-device-only-in-the-query", Query: url.Values{"device_id": {"900100001"}}, Body: report(map[string]any{"device_id": nil})}),
		asDevice(contract.Case{Name: "report-other-device-only-in-the-query", Query: url.Values{"device_id": {"900100002"}}, Body: report(map[string]any{"device_id": nil})}),
		asDevice(contract.Case{Name: "report-other-device-only-in-the-header", Body: report(map[string]any{"device_id": nil}), Headers: with(device, "X-Device-Id", "900100002")}),
		asDevice(contract.Case{Name: "report-empty-header-only", Body: report(map[string]any{"device_id": nil}), Headers: with(device, "X-Device-Id", "")}),

		// What the route makes of the report
		sent("report-without-sessions", map[string]any{"sessions": nil, "idle_seconds": nil, "timestamp": "2011-03-04T10:02:00.000Z"}),
		sent("report-sessions-not-a-list", map[string]any{"sessions": "many", "timestamp": "2011-03-04T10:03:00.000Z"}),
		sent("report-sessions-without-times", map[string]any{"sessions": []any{map[string]any{"app_name": "No times", "duration_secs": 5}, 7, "x"}, "timestamp": "2011-03-04T10:04:00.000Z"}),
		sent("report-session-null", map[string]any{"sessions": []any{null}, "timestamp": "2011-03-04T10:05:00.000Z"}),
		sent("report-odd-values", map[string]any{
			"sessions":     []any{map[string]any{"started_at": "2011-03-04T09:20:00.000Z", "ended_at": "2011-03-04T09:21:00.000Z", "duration_secs": "60", "app_name": 5}},
			"idle_seconds": "idle", "timestamp": "2011-03-04T10:06:00.000Z",
		}),
		sent("report-same-time-again", map[string]any{"idle_seconds": 300}),
		asDevice(contract.Case{Name: "report-array-body", RawBody: `[{"device_id":"900100001"}]`, ContentType: "application/json"}),
		asDevice(contract.Case{Name: "report-form", RawBody: "device_id=900100001", ContentType: "application/x-www-form-urlencoded"}),

		// A device's report with each role's browser: the session, its
		// cookies and its CSRF token come along
		contract.Case{Name: "report-with-session", Method: "POST", Path: bd + "/activity", Body: report(nil), Headers: device},
		contract.Case{Name: "report-with-session-without-csrf-token", Method: "POST", Path: bd + "/activity", Body: report(nil), Headers: device, NoCSRF: true},
		contract.Case{Name: "report-at-activity-with-session", Method: "POST", Path: own + "/activity", Body: report(nil), Headers: device},
		contract.Case{Name: "report-with-session-without-token", Method: "POST", Path: bd + "/activity", Body: report(nil)},
	)
}
