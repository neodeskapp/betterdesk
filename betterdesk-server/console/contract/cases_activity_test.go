//go:build contract

package contract_test

import (
	"net/url"

	"github.com/unitronix/betterdesk-server/console/contract"
)

// Activity: the three routes people read (the summaries of all devices,
// one device's sessions and summaries, its most used apps). The upload
// route of the desktop agent takes a device token and is not part of
// these cases.
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
