//go:build contract

package contract_test

import (
	"strings"

	"github.com/neodeskapp/neodesk/neodesk-server/console/contract"
)

// Relay nodes and assignment rules, at both addresses the group answers
// under, and the relay heartbeat. Each role's turn creates a relay and a
// rule, reads and changes them and deletes them again; a role that may
// not write gets the refusals and leaves nothing behind.
func init() {
	const (
		relays = "/api/panel/scaling/relays"
		rules  = "/api/panel/scaling/rules"
		second = "/api/nd"
	)
	send := func(name, method, path string, body any, roles ...contract.Role) contract.Case {
		return contract.Case{Name: name, Method: method, Path: path, Body: body, Roles: roles}
	}
	few := contract.Roles("signed-out", "viewer", "global_admin", "admin")
	admins := contract.Roles("server_admin", "admin")
	relay := func(changes map[string]any) map[string]any {
		body := map[string]any{"name": "Contract relay", "address": "relay.contract.example:21117", "location": "Lab 2", "max_sessions": 10, "max_bandwidth_mbps": 20}
		for key, value := range changes {
			if value == nil {
				delete(body, key)
			} else {
				body[key] = value
			}
		}
		return body
	}
	rule := func(changes map[string]any) map[string]any {
		body := map[string]any{"name": "Office", "match_type": "subnet", "match_value": "10.0.0.0/8", "target_relay": "r-1", "priority": 5}
		for key, value := range changes {
			if value == nil {
				delete(body, key)
			} else {
				body[key] = value
			}
		}
		return body
	}

	contract.Group("scaling",
		// Relay nodes
		contract.Case{Name: "relays-empty", Path: relays},
		contract.Case{Name: "capacity-empty", Path: "/api/panel/scaling/capacity"},
		contract.Case{
			Name: "relay-create", Method: "POST", Path: relays, Body: relay(nil),
			Capture: map[string]string{"relay": "data.id"},
		},
		send("relay-create-same-address", "POST", relays, relay(map[string]any{"name": "Twin", "address": " relay.contract.example:21117 "})),
		contract.Case{Name: "relay-read", Path: relays + "/{relay}"},
		contract.Case{Name: "relay-read-unknown", Path: relays + "/r-0000000000000000", Roles: few},
		contract.Case{Name: "relays-list", Path: relays},
		contract.Case{Name: "relay-health", Path: relays + "/{relay}/health"},
		contract.Case{Name: "relay-metrics", Path: relays + "/{relay}/metrics"},
		contract.Case{Name: "relay-metrics-head", Method: "HEAD", Path: relays + "/{relay}/metrics", Roles: few},
		contract.Case{Name: "capacity", Path: "/api/panel/scaling/capacity"},
		send("relay-update", "PUT", relays+"/{relay}", relay(map[string]any{"name": " Contract relay (EU) #2 ", "location": nil, "max_sessions": "250 seats", "max_bandwidth_mbps": "fast"})),
		contract.Case{Name: "relay-read-after-update", Path: relays + "/{relay}", Roles: few},
		contract.Case{Name: "capacity-after-update", Path: "/api/panel/scaling/capacity", Roles: few},
		contract.Case{
			Name: "relay-create-second", Method: "POST", Path: relays, Body: relay(map[string]any{"name": "Second", "address": "[2001:db8::7]:21117", "max_sessions": nil, "max_bandwidth_mbps": nil}),
			Capture: map[string]string{"relay2": "data.id"},
		},
		send("relay-update-to-a-taken-address", "PUT", relays+"/{relay2}", relay(map[string]any{"name": "Second"})),
		send("relay-update-unknown", "PUT", relays+"/r-0000000000000000", relay(nil), few...),
		send("relay-delete-second", "DELETE", relays+"/{relay2}", nil),

		send("relay-no-name", "POST", relays, relay(map[string]any{"name": nil})),
		send("relay-name-of-spaces", "POST", relays, relay(map[string]any{"name": "   "}), few...),
		send("relay-name-too-long", "POST", relays, relay(map[string]any{"name": strings.Repeat("n", 81)}), few...),
		send("relay-name-with-markup", "POST", relays, relay(map[string]any{"name": "<b>relay</b>"}), few...),
		contract.Case{
			Name: "relay-name-in-other-scripts", Method: "POST", Path: relays, Roles: admins,
			Body:    relay(map[string]any{"name": "中继 Rélais ١", "address": "10.9.8.7:1"}),
			Capture: map[string]string{"odd": "data.id"},
		},
		send("relay-name-is-a-number", "POST", relays, relay(map[string]any{"name": 7, "address": "10.9.8.7:1"}), admins...),
		send("relay-no-address", "POST", relays, relay(map[string]any{"address": nil}), few...),
		send("relay-address-without-port", "POST", relays, relay(map[string]any{"address": "relay.contract.example"}), few...),
		send("relay-address-with-path", "POST", relays, relay(map[string]any{"address": "relay.contract.example:21117/x"}), few...),
		send("relay-address-port-zero", "POST", relays, relay(map[string]any{"address": "relay.contract.example:0"}), few...),
		send("relay-address-port-too-high", "POST", relays, relay(map[string]any{"address": "relay.contract.example:65536"}), few...),
		send("relay-location-too-long", "POST", relays, relay(map[string]any{"location": strings.Repeat("l", 81)}), few...),
		send("relay-sessions-zero", "POST", relays, relay(map[string]any{"max_sessions": 0}), few...),
		send("relay-sessions-too-many", "POST", relays, relay(map[string]any{"max_sessions": "100001"}), few...),
		send("relay-bandwidth-too-high", "POST", relays, relay(map[string]any{"max_bandwidth_mbps": 1000001}), few...),
		send("relay-bandwidth-negative", "PUT", relays+"/{relay}", relay(map[string]any{"max_bandwidth_mbps": -1}), few...),
		contract.Case{Name: "relay-array-body", Method: "POST", Path: relays, RawBody: `[{"name":"x"}]`, ContentType: "application/json", Roles: few},
		contract.Case{Name: "relay-create-without-csrf-token", Method: "POST", Path: relays, Body: relay(nil), NoCSRF: true, Roles: few},
		contract.Case{
			Name: "relay-create-as-form", Method: "POST", Path: relays, Roles: few,
			RawBody: "name=Form+relay&address=10.9.8.6%3A21117&max_sessions=7", ContentType: "application/x-www-form-urlencoded",
			Capture: map[string]string{"form": "data.id"},
		},
		send("relay-delete-the-one-from-the-form", "DELETE", relays+"/{form}", nil, contract.Roles("admin")...),
		send("relay-delete-the-odd-one", "DELETE", relays+"/{odd}", nil, admins...),
		send("relay-delete-unknown", "DELETE", relays+"/r-0000000000000000", nil, few...),

		send("relay-delete", "DELETE", relays+"/{relay}", nil),
		send("relay-delete-again", "DELETE", relays+"/{relay}", nil),
		contract.Case{Name: "relay-read-after-delete", Path: relays + "/{relay}", Roles: few},

		// Assignment rules
		contract.Case{Name: "rules-empty", Path: rules},
		contract.Case{
			Name: "rule-create", Method: "POST", Path: rules, Body: rule(nil),
			Capture: map[string]string{"rule": "data.id"},
		},
		contract.Case{
			Name: "rule-create-switched-off", Method: "POST", Path: rules,
			Body:    rule(map[string]any{"name": " Guests ", "match_type": "tag", "match_value": "guest", "target_relay": nil, "fallback": "", "priority": "7th", "enabled": false}),
			Capture: map[string]string{"rule2": "data.id"},
		},
		contract.Case{Name: "rules-list", Path: rules},
		send("rule-update-keeps-the-switch", "PUT", rules+"/{rule2}", rule(map[string]any{"name": "Guests 2", "match_type": "tag", "match_value": "guest"})),
		send("rule-update-switch-on", "PUT", rules+"/{rule2}", rule(map[string]any{"name": "Guests 3", "enabled": true, "fallback": " direct ", "priority": nil})),
		send("rule-update-switch-off", "PUT", rules+"/{rule}", rule(map[string]any{"enabled": false})),
		send("rule-update-switch-as-text", "PUT", rules+"/{rule}", rule(map[string]any{"enabled": "false"}), few...),
		send("rule-update-switch-null", "PUT", rules+"/{rule}", rule(map[string]any{"enabled": []any{nil}}), few...),
		send("rule-update-unknown", "PUT", rules+"/r-0000000000000000", rule(nil), few...),
		contract.Case{Name: "rules-list-after-updates", Path: rules, Roles: few},

		send("rule-no-name", "POST", rules, rule(map[string]any{"name": nil})),
		send("rule-name-too-long", "POST", rules, rule(map[string]any{"name": strings.Repeat("n", 81)}), few...),
		send("rule-unknown-match-type", "POST", rules, rule(map[string]any{"match_type": "hostname"}), few...),
		send("rule-match-type-in-capitals", "POST", rules, rule(map[string]any{"match_type": "Subnet"}), few...),
		send("rule-no-match-value", "POST", rules, rule(map[string]any{"match_value": " "}), few...),
		send("rule-match-value-too-long", "POST", rules, rule(map[string]any{"match_value": strings.Repeat("v", 201)}), few...),
		send("rule-priority-negative", "POST", rules, rule(map[string]any{"priority": -1}), few...),
		send("rule-priority-too-high", "POST", rules, rule(map[string]any{"priority": "1001"}), few...),

		send("rule-delete", "DELETE", rules+"/{rule}", nil),
		send("rule-delete-second", "DELETE", rules+"/{rule2}", nil),
		send("rule-delete-again", "DELETE", rules+"/{rule}", nil, few...),

		// The heartbeat of a relay
		send("heartbeat", "POST", "/api/nd/scaling/relay-heartbeat", map[string]any{"relay": "r-1", "sessions": 3}),
		contract.Case{Name: "heartbeat-without-csrf-token", Method: "POST", Path: "/api/nd/scaling/relay-heartbeat", Body: map[string]any{}, NoCSRF: true, Roles: few},
		contract.Case{Name: "heartbeat-without-cookies", Method: "POST", Path: "/api/nd/scaling/relay-heartbeat", Body: map[string]any{}, NoCookies: true, NoCSRF: true, Roles: contract.Roles("signed-out", "admin")},
		contract.Case{Name: "heartbeat-not-json", Method: "POST", Path: "/api/nd/scaling/relay-heartbeat", RawBody: "alive", ContentType: "text/plain", Roles: few},

		// The same routes under the second address
		contract.Case{Name: "second-relays", Path: second + relays},
		contract.Case{Name: "second-capacity", Path: second + "/api/panel/scaling/capacity", Roles: few},
		contract.Case{Name: "second-rules", Path: second + rules, Roles: few},
		contract.Case{
			Name: "second-relay-create", Method: "POST", Path: second + relays, Body: relay(map[string]any{"address": "second.contract.example:21117"}),
			Capture: map[string]string{"relay3": "data.id"},
		},
		contract.Case{Name: "second-relay-read", Path: second + relays + "/{relay3}", Roles: few},
		contract.Case{Name: "second-relay-read-at-the-first-address", Path: relays + "/{relay3}", Roles: few},
		contract.Case{Name: "second-relay-health", Path: second + relays + "/{relay3}/health", Roles: few},
		contract.Case{Name: "second-relay-metrics", Path: second + relays + "/{relay3}/metrics", Roles: few},
		send("second-relay-update", "PUT", second+relays+"/{relay3}", relay(map[string]any{"address": "second.contract.example:21118"}), few...),
		send("second-relay-delete", "DELETE", second+relays+"/{relay3}", nil),
		contract.Case{
			Name: "second-rule-create", Method: "POST", Path: second + rules, Body: rule(map[string]any{"match_type": "device_id", "match_value": "900100001"}),
			Capture: map[string]string{"rule3": "data.id"},
		},
		send("second-rule-update", "PUT", second+rules+"/{rule3}", rule(map[string]any{"match_type": "country", "match_value": "DE"}), few...),
		send("second-rule-delete", "DELETE", second+rules+"/{rule3}", nil),
		send("second-heartbeat", "POST", second+"/api/nd/scaling/relay-heartbeat", map[string]any{}),
	)
}
