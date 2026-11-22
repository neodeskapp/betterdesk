//go:build contract

package contract_test

import (
	"net/url"

	"github.com/unitronix/betterdesk-server/console/contract"
)

// Data guard: the policy and event routes people use, at both addresses
// the group is mounted at, /api/dataguard and /api/bd, and at the end of
// each the two device routes (dlp-policies, dlp-events). These take a
// device token, which the suite has none of: the headers of a case are
// written into its golden. So their cases show what comes before the
// route, the event list stays empty, and the answers with a token are
// pinned by routes/dataguard/testdata/node_device_*.json.
//
// A role's turn creates three policies under each address, works on the
// first and deletes them.
func init() {
	var cases []contract.Case
	event := map[string]any{"event_source": "usb", "event_type": "violation", "details": map[string]any{"path": "E:\\"}}
	unknown := map[string]string{"Authorization": "Bearer contract-no-such-token"}
	for _, mount := range []struct{ name, path string }{{"dataguard", "/api/dataguard"}, {"bd", "/api/bd"}} {
		policy := "policy_" + mount.name
		second := "second_" + mount.name
		third := "third_" + mount.name
		at := func(name string) string { return mount.name + "-" + name }
		cases = append(cases,
			contract.Case{Name: at("list-empty"), Path: mount.path + "/policies"},
			contract.Case{
				Name: at("create"), Method: "POST", Path: mount.path + "/policies",
				Body: map[string]any{
					"name": "  Block USB sticks ", "description": "No mass storage", "policy_type": "usb", "action": "block", "scope": "all",
					"enabled": true, "rules": []any{map[string]any{"rule_type": "usb", "action": "block", "filter": "*"}},
				},
				Capture: map[string]string{policy: "id"},
			},
			contract.Case{
				Name: at("create-with-defaults"), Method: "POST", Path: mount.path + "/policies",
				Body:    map[string]any{"name": "Defaults"},
				Capture: map[string]string{second: "id"},
			},
			contract.Case{
				Name: at("create-other-types"), Method: "POST", Path: mount.path + "/policies",
				Body: map[string]any{
					"name": "Types", "description": 5, "policy_type": 0, "action": "", "scope": 1.5, "enabled": 0,
					"rules": []any{1.0, "text", nil, true, []any{2, []any{}}, map[string]any{"b": 1, "10": "x", "2": "y", "a": map[string]any{}}},
				},
				Capture: map[string]string{third: "id"},
			},
			contract.Case{Name: at("create-without-name"), Method: "POST", Path: mount.path + "/policies", Body: map[string]any{"description": "nameless"}},
			contract.Case{Name: at("create-blank-name"), Method: "POST", Path: mount.path + "/policies", Body: map[string]any{"name": "   "}},
			contract.Case{Name: at("create-name-number"), Method: "POST", Path: mount.path + "/policies", Body: map[string]any{"name": 7}},
			contract.Case{Name: at("create-rules-object"), Method: "POST", Path: mount.path + "/policies", Body: map[string]any{"name": "x", "rules": map[string]any{}}},
			contract.Case{Name: at("create-rules-text"), Method: "POST", Path: mount.path + "/policies", Body: map[string]any{"name": "x", "rules": "[]"}},
			contract.Case{Name: at("create-rules-null"), Method: "POST", Path: mount.path + "/policies", Body: map[string]any{"name": "x", "rules": nil}},
			contract.Case{Name: at("create-flag-in-a-text-field"), Method: "POST", Path: mount.path + "/policies", Body: map[string]any{"name": "x", "description": true}},
			contract.Case{Name: at("create-without-csrf-token"), Method: "POST", Path: mount.path + "/policies", Body: map[string]any{"name": "x"}, NoCSRF: true},
			contract.Case{
				Name: at("create-as-form"), Method: "POST", Path: mount.path + "/policies",
				RawBody: "name=Form+policy", ContentType: "application/x-www-form-urlencoded",
			},

			contract.Case{Name: at("read"), Path: mount.path + "/policies/{" + policy + "}"},
			contract.Case{Name: at("read-other-types"), Path: mount.path + "/policies/{" + third + "}"},
			contract.Case{Name: at("read-id-with-fraction"), Path: mount.path + "/policies/{" + policy + "}.0"},
			contract.Case{Name: at("read-id-not-a-number"), Path: mount.path + "/policies/abc"},
			contract.Case{Name: at("read-missing"), Path: mount.path + "/policies/999999999"},
			contract.Case{Name: at("list"), Path: mount.path + "/policies"},
			contract.Case{Name: at("list-head"), Method: "HEAD", Path: mount.path + "/policies"},

			contract.Case{
				Name: at("update"), Method: "PATCH", Path: mount.path + "/policies/{" + policy + "}",
				Body: map[string]any{"name": "Watch USB sticks", "description": "", "action": "log", "enabled": false, "rules": []any{}},
			},
			contract.Case{Name: at("update-enabled-by-truth"), Method: "PATCH", Path: mount.path + "/policies/{" + policy + "}", Body: map[string]any{"enabled": "no"}},
			contract.Case{Name: at("update-nothing"), Method: "PATCH", Path: mount.path + "/policies/{" + policy + "}", Body: map[string]any{"unknown": 1}},
			contract.Case{Name: at("update-rules-object"), Method: "PATCH", Path: mount.path + "/policies/{" + policy + "}", Body: map[string]any{"rules": map[string]any{"a": 1}}},
			contract.Case{Name: at("update-name-null"), Method: "PATCH", Path: mount.path + "/policies/{" + policy + "}", Body: map[string]any{"name": nil}},
			contract.Case{Name: at("update-flag-in-a-text-field"), Method: "PATCH", Path: mount.path + "/policies/{" + policy + "}", Body: map[string]any{"scope": true}},
			contract.Case{Name: at("update-missing"), Method: "PATCH", Path: mount.path + "/policies/999999999", Body: map[string]any{"name": "x"}},
			contract.Case{Name: at("update-missing-with-bad-rules"), Method: "PATCH", Path: mount.path + "/policies/999999999", Body: map[string]any{"rules": 1}},
			contract.Case{Name: at("read-after-updates"), Path: mount.path + "/policies/{" + policy + "}"},

			contract.Case{Name: at("events"), Path: mount.path + "/events"},
			contract.Case{
				Name: at("events-filtered"), Path: mount.path + "/events",
				Query: url.Values{"device_id": {"900100001"}, "event_source": {"usb"}, "event_type": {"info"}, "limit": {"5"}, "from": {"2000-01-01"}, "to": {"2100-01-01"}},
			},
			contract.Case{Name: at("events-limit-not-a-number"), Path: mount.path + "/events", Query: url.Values{"limit": {"abc"}}},
			contract.Case{Name: at("events-limit-half"), Path: mount.path + "/events", Query: url.Values{"limit": {"1.5"}}},
			contract.Case{Name: at("events-limit-negative"), Path: mount.path + "/events", Query: url.Values{"limit": {"-1"}}},
			contract.Case{Name: at("events-device-twice"), Path: mount.path + "/events", Query: url.Values{"device_id": {"a", "b"}}},
			contract.Case{Name: at("events-device-in-brackets"), Path: mount.path + "/events", Query: url.Values{"device_id[]": {"a"}}},
			contract.Case{Name: at("events-device-object"), Path: mount.path + "/events", Query: url.Values{"device_id[x]": {"a"}}},
			contract.Case{Name: at("stats"), Path: mount.path + "/stats"},

			contract.Case{Name: at("delete-without-csrf-token"), Method: "DELETE", Path: mount.path + "/policies/{" + policy + "}", NoCSRF: true},
			contract.Case{Name: at("delete"), Method: "DELETE", Path: mount.path + "/policies/{" + policy + "}"},
			contract.Case{Name: at("delete-again"), Method: "DELETE", Path: mount.path + "/policies/{" + policy + "}"},
			contract.Case{Name: at("delete-id-not-a-number"), Method: "DELETE", Path: mount.path + "/policies/abc"},
			contract.Case{Name: at("delete-second"), Method: "DELETE", Path: mount.path + "/policies/{" + second + "}"},
			contract.Case{Name: at("delete-third"), Method: "DELETE", Path: mount.path + "/policies/{" + third + "}"},
			contract.Case{Name: at("list-after-deletes"), Path: mount.path + "/policies"},

			// The device routes. The CSRF gate lets a write without a session
			// through below /api/bd/ and checks every other write.
			contract.Case{Name: at("device-policies-without-token"), Path: mount.path + "/dlp-policies"},
			contract.Case{Name: at("device-policies-unknown-token"), Path: mount.path + "/dlp-policies", Headers: unknown},
			contract.Case{Name: at("device-policies-device-id-alone"), Path: mount.path + "/dlp-policies", Headers: map[string]string{"X-Device-Id": "900100001"}},
			contract.Case{Name: at("device-policies-as-a-device"), Path: mount.path + "/dlp-policies", Headers: unknown, NoCookies: true, Roles: contract.Roles("signed-out")},
			contract.Case{Name: at("device-event-without-token"), Method: "POST", Path: mount.path + "/dlp-events", Body: event},
			contract.Case{Name: at("device-event-unknown-token"), Method: "POST", Path: mount.path + "/dlp-events", Body: event, Headers: unknown},
			contract.Case{Name: at("device-event-without-csrf-token"), Method: "POST", Path: mount.path + "/dlp-events", Body: event, Headers: unknown, NoCSRF: true},
			contract.Case{
				Name: at("device-event-as-a-device"), Method: "POST", Path: mount.path + "/dlp-events", Body: event, Headers: unknown,
				NoCSRF: true, NoCookies: true, Roles: contract.Roles("signed-out"),
			},
		)
	}
	contract.Group("dataguard", cases...)
}
