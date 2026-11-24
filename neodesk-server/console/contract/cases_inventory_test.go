//go:build contract

package contract_test

import (
	"encoding/json"
	"net/url"

	"github.com/neodeskapp/neodesk/neodesk-server/console/contract"
)

// Inventory: the two routes people read, the list of devices with their
// hardware in short and one device's full report with its last telemetry.
// The three routes of the desktop agent (inventory and telemetry upload,
// reading its own report) take a device token; they are the group
// inventory-device below, which runs after this one, so its uploads do
// not reach the answers here.
//
// The demo data has reports for 14 devices. Their times count back from
// the moment an instance starts, and so do the install dates of the
// programs in a report.
func init() {
	times := []string{"devices[*].last_seen", "devices[*].collected_at"}
	report := []string{"collected_at", "received_at", "telemetry.timestamp", "telemetry.received_at", "software.apps[*].installed_at"}
	few := contract.Roles("signed-out", "guest_issuer", "viewer", "admin")
	page := func(name string, query url.Values, roles ...contract.Role) contract.Case {
		return contract.Case{Name: name, Path: "/api/inventory", Query: query, Volatile: times, Roles: roles}
	}

	contract.Group("inventory",
		// The list and its pages
		page("list", nil),
		contract.Case{Name: "list-head", Method: "HEAD", Path: "/api/inventory"},
		contract.Case{Name: "list-other-spelling", Path: "/API/Inventory/", Volatile: times, Roles: few},
		page("list-first-page-of-five", url.Values{"page": {"1"}, "limit": {"5"}}),
		page("list-second-page-of-five", url.Values{"page": {"2"}, "limit": {"5"}}, few...),
		page("list-last-page-of-five", url.Values{"page": {"3"}, "limit": {"5"}}, few...),
		page("list-page-after-the-last", url.Values{"page": {"4"}, "limit": {"5"}}, few...),
		page("list-page-only", url.Values{"page": {"2"}}, few...),
		page("list-limit-only", url.Values{"limit": {"3"}}, few...),
		page("list-limit-one-hundred", url.Values{"limit": {"100"}}, few...),
		page("list-limit-above-the-cap", url.Values{"limit": {"1000000"}}, few...),
		page("list-limit-beyond-integers", url.Values{"limit": {"99999999999999999999"}}, few...),
		page("list-page-with-zeros-in-front", url.Values{"page": {"0002"}, "limit": {"5"}}, few...),
		page("list-largest-page", url.Values{"page": {"9007199254740991"}}, few...),
		page("list-page-beyond-integers", url.Values{"page": {"9007199254740992"}}, few...),
		page("list-page-zero", url.Values{"page": {"0"}}, few...),
		page("list-limit-zero", url.Values{"limit": {"0"}}, few...),
		page("list-page-negative", url.Values{"page": {"-1"}}, few...),
		page("list-limit-fraction", url.Values{"limit": {"1.5"}}, few...),
		page("list-limit-not-a-number", url.Values{"limit": {"abc"}}, few...),
		page("list-limit-exponent", url.Values{"limit": {"1e1"}}, few...),
		page("list-limit-with-space", url.Values{"limit": {" 5"}}, few...),
		page("list-page-empty", url.Values{"page": {""}}, few...),
		page("list-limit-empty", url.Values{"limit": {""}}, few...),
		page("list-page-twice", url.Values{"page": {"1", "2"}}, few...),
		page("list-limit-in-brackets", url.Values{"limit[]": {"5"}}, few...),
		page("list-page-object", url.Values{"page[n]": {"1"}}, few...),
		page("list-bad-page-good-limit", url.Values{"page": {"x"}, "limit": {"5"}}, few...),

		// One device: a Windows desktop, a Mac, a Linux server, a
		// Windows server and a Raspberry Pi
		contract.Case{Name: "detail", Path: "/api/inventory/900100001", Volatile: report},
		contract.Case{Name: "detail-head", Method: "HEAD", Path: "/api/inventory/900100001", Roles: few},
		contract.Case{Name: "detail-other-spelling", Path: "/API/Inventory/900100001/", Volatile: report, Roles: few},
		contract.Case{Name: "detail-mac", Path: "/api/inventory/900100005", Volatile: report, Roles: few},
		contract.Case{Name: "detail-linux-server", Path: "/api/inventory/900100012", Volatile: report, Roles: few},
		contract.Case{Name: "detail-windows-server", Path: "/api/inventory/900100026", Volatile: report, Roles: few},
		contract.Case{Name: "detail-small-computer", Path: "/api/inventory/900100024", Volatile: report, Roles: few},
		contract.Case{Name: "detail-offline-with-a-query", Path: "/api/inventory/900100016", Query: url.Values{"page": {"x"}, "limit": {"0"}}, Volatile: report, Roles: few},
		contract.Case{Name: "detail-device-without-report", Path: "/api/inventory/900100007"},
		contract.Case{Name: "detail-unknown-device", Path: "/api/inventory/no%20such%2Fdevice"},
		// The agent's routes are POST /api/inventory/inventory and
		// /telemetry and GET /api/inventory/inventory/:id; read without an
		// id, the two words are device ids
		contract.Case{Name: "detail-of-the-upload-address", Path: "/api/inventory/inventory"},
		contract.Case{Name: "detail-of-the-telemetry-address", Path: "/api/inventory/telemetry"},
	)
}

// inventory-device: the three routes of the desktop agent, at /api/nd and
// at /api/inventory. They check the token themselves (not with the
// device guards of the other groups): a Bearer token bound to any device,
// and then the device the request names, compared with the token's
// device as it is, so 900100001 as a number is another device. The body
// is checked before that comparison.
//
// The device is the instance's own: the token of 900100001 in
// /stack/secrets/device_token, that of 900100002 for another device. As a
// device (signed out, no cookies) it uploads its inventory and reads it
// back, sends telemetry, and sends what the routes refuse or store
// oddly. Then each role's session in front of a device's request: the
// CSRF gate, which checks every write at /api/inventory and a signed-in
// write at /api/nd, and the panel access guard.
//
// Every upload keeps the host name, the operating system and the memory
// the demo data gave 900100001, and the last upload of a turn is the
// usual one: the reports group reads the inventory after this group and
// counts systems and memory. Times are in 2011, outside every window the
// reports cases ask for.
func init() {
	const nd = "/api/nd"
	const own = "/api/inventory"
	device := map[string]string{"Authorization": "Bearer {secret:device_token}"}
	other := map[string]string{"Authorization": "Bearer {secret:device_token_other}"}
	unknown := map[string]string{"Authorization": "Bearer contract-no-such-token"}
	basic := map[string]string{"Authorization": "Basic {secret:device_token}"}
	lower := map[string]string{"Authorization": "bearer {secret:device_token}"}
	named := map[string]string{"X-Device-Id": "900100001"}
	none := map[string]string{}

	disks := []any{map[string]any{"device": "C:", "mount_point": "C:\\", "filesystem": "NTFS", "total_gb": 951.6, "used_gb": 890.7}}
	hardware := func(disks any) map[string]any {
		return map[string]any{
			"hostname": "demo-reception-01", "os_name": "Windows 11 Pro", "os_version": "23H2 (22631.4460)", "architecture": "x86_64",
			"cpu":    map[string]any{"brand": "Intel(R) Core(TM) i5-12500", "logical_cores": 8, "usage_percent": 10.4},
			"memory": map[string]any{"total_bytes": 34359738368, "used_bytes": 14317376441},
			"disks":  disks,
		}
	}
	software := map[string]any{"apps": []any{map[string]any{"name": "Contract Editor", "version": "1.0", "publisher": "Contract"}}}
	// changed is a body with some members changed; a member set to nil is
	// left out
	changed := func(body, changes map[string]any) map[string]any {
		for key, value := range changes {
			if value == nil {
				delete(body, key)
				continue
			}
			body[key] = value
		}
		return body
	}
	report := func(changes map[string]any) map[string]any {
		return changed(map[string]any{
			"device_id": "900100001", "hardware": hardware(disks), "software": software, "collected_at": "2011-03-04T10:00:00.000Z",
		}, changes)
	}
	telemetry := func(changes map[string]any) map[string]any {
		return changed(map[string]any{
			"device_id": "900100001", "cpu_usage_percent": 12.5, "memory_used_bytes": 14317376441, "memory_total_bytes": 34359738368,
			"uptime_secs": 26400, "timestamp": "2011-03-04T10:05:00.000Z",
		}, changes)
	}
	// asDevice sends a case the way a device does: no session, no cookies,
	// no CSRF token, the device's token unless the case names other headers
	asDevice := func(c contract.Case) contract.Case {
		c.NoCSRF, c.NoCookies, c.Roles = true, true, contract.Roles("signed-out")
		if c.Headers == nil {
			c.Headers = device
		}
		if c.Body != nil || c.RawBody != "" {
			c.Method = "POST"
			if c.Path == "" {
				c.Path = nd + "/inventory"
			}
		}
		if c.Path == "" {
			c.Path = nd + "/inventory/900100001"
		}
		return c
	}
	sent := func(name string, changes map[string]any) contract.Case {
		return asDevice(contract.Case{Name: name, Body: report(changes)})
	}
	read := func(name string) contract.Case { return asDevice(contract.Case{Name: name}) }
	measured := func(name string, changes map[string]any) contract.Case {
		return asDevice(contract.Case{Name: name, Path: nd + "/telemetry", Body: telemetry(changes)})
	}
	// null is a member that is null in the JSON body
	null := json.RawMessage("null")

	contract.Group("inventory-device",
		// Upload and read back
		sent("upload", nil),
		read("read"),
		asDevice(contract.Case{Name: "read-at-inventory", Path: own + "/inventory/900100001"}),
		asDevice(contract.Case{Name: "read-other-spelling", Path: "/API/ND/Inventory/900100001/"}),
		asDevice(contract.Case{Name: "read-other-device", Path: nd + "/inventory/900100002"}),
		asDevice(contract.Case{Name: "read-as-other-device", Headers: other}),
		// The seed dates the apps from the day it runs, as the detail cases
		// of the inventory group know
		asDevice(contract.Case{Name: "read-other-device-as-it", Path: nd + "/inventory/900100002", Headers: other,
			Volatile: []string{"software.apps[*].installed_at"}}),
		asDevice(contract.Case{Name: "read-without-token", Headers: none}),
		asDevice(contract.Case{Name: "read-unknown-token", Headers: unknown}),
		asDevice(contract.Case{Name: "read-basic", Headers: basic}),
		asDevice(contract.Case{Name: "read-lower-case-bearer", Headers: lower}),
		asDevice(contract.Case{Name: "read-device-id-alone", Headers: named}),
		asDevice(contract.Case{Name: "read-at-inventory-without-token", Path: own + "/inventory/900100001", Headers: none}),

		// The body is checked first, then the device it names
		sent("upload-without-device-id", map[string]any{"device_id": nil}),
		sent("upload-without-hardware", map[string]any{"hardware": nil}),
		sent("upload-hardware-zero", map[string]any{"hardware": 0}),
		sent("upload-hardware-empty-text", map[string]any{"hardware": ""}),
		sent("upload-hardware-null", map[string]any{"hardware": null}),
		sent("upload-device-id-empty", map[string]any{"device_id": ""}),
		sent("upload-device-id-number", map[string]any{"device_id": 900100001}),
		sent("upload-device-id-in-a-list", map[string]any{"device_id": []any{"900100001"}}),
		sent("upload-other-device", map[string]any{"device_id": "900100002"}),
		asDevice(contract.Case{Name: "upload-as-other-device", Body: report(nil), Headers: other}),
		asDevice(contract.Case{Name: "upload-as-other-device-without-hardware", Body: report(map[string]any{"hardware": nil}), Headers: other}),
		asDevice(contract.Case{Name: "upload-without-token", Body: report(nil), Headers: none}),
		asDevice(contract.Case{Name: "upload-unknown-token", Body: report(nil), Headers: unknown}),
		asDevice(contract.Case{Name: "upload-unknown-token-without-hardware", Body: report(map[string]any{"hardware": nil}), Headers: unknown}),
		asDevice(contract.Case{Name: "upload-device-id-alone", Body: report(nil), Headers: named}),
		asDevice(contract.Case{Name: "upload-at-inventory", Path: own + "/inventory", Body: report(nil)}),
		asDevice(contract.Case{Name: "upload-array-body", RawBody: `[{"device_id":"900100001","hardware":{}}]`, ContentType: "application/json"}),
		asDevice(contract.Case{Name: "upload-form", RawBody: "device_id=900100001&hardware=x", ContentType: "application/x-www-form-urlencoded"}),

		// What is stored as it came
		sent("upload-hardware-text", map[string]any{"hardware": "not an object", "software": null, "collected_at": ""}),
		read("read-hardware-text"),
		sent("upload-collected-at-number", map[string]any{"collected_at": 5}),
		read("read-collected-at-number"),
		// The route fails after the report is stored: the fullest disk and
		// the log line make text and numbers of what the report holds
		sent("upload-disk-size-no-number", map[string]any{"hardware": hardware([]any{map[string]any{"mount_point": "C:\\", "total_bytes": map[string]any{"toString": 1}, "used_bytes": 1}})}),
		read("read-disk-size-no-number"),
		sent("upload-app-count-no-text", map[string]any{"software": map[string]any{"apps": map[string]any{"length": map[string]any{"toString": 1}}}}),
		read("read-app-count-no-text"),
		sent("upload-again", nil),
		read("read-again"),

		// Telemetry
		measured("telemetry", nil),
		asDevice(contract.Case{Name: "telemetry-at-inventory", Path: own + "/telemetry", Body: telemetry(nil)}),
		measured("telemetry-only-device-id", map[string]any{"cpu_usage_percent": nil, "memory_used_bytes": nil, "memory_total_bytes": nil, "uptime_secs": nil, "timestamp": "2011-03-04T10:06:00.000Z"}),
		measured("telemetry-odd-values", map[string]any{"cpu_usage_percent": "hot", "memory_used_bytes": 1.5, "uptime_secs": "long"}),
		measured("telemetry-without-device-id", map[string]any{"device_id": nil}),
		measured("telemetry-device-id-number", map[string]any{"device_id": 900100001}),
		measured("telemetry-other-device", map[string]any{"device_id": "900100002"}),
		asDevice(contract.Case{Name: "telemetry-as-other-device", Path: nd + "/telemetry", Body: telemetry(nil), Headers: other}),
		asDevice(contract.Case{Name: "telemetry-without-token", Path: nd + "/telemetry", Body: telemetry(nil), Headers: none}),
		asDevice(contract.Case{Name: "telemetry-unknown-token", Path: nd + "/telemetry", Body: telemetry(nil), Headers: unknown}),
		asDevice(contract.Case{Name: "telemetry-device-id-alone", Path: nd + "/telemetry", Body: telemetry(nil), Headers: named}),
		measured("telemetry-again", nil),

		// A device's request with each role's browser: the session, its
		// cookies and its CSRF token come along
		contract.Case{Name: "upload-with-session", Method: "POST", Path: nd + "/inventory", Body: report(nil), Headers: device},
		contract.Case{Name: "upload-with-session-without-csrf-token", Method: "POST", Path: nd + "/inventory", Body: report(nil), Headers: device, NoCSRF: true},
		contract.Case{Name: "upload-at-inventory-with-session", Method: "POST", Path: own + "/inventory", Body: report(nil), Headers: device},
		contract.Case{Name: "telemetry-with-session", Method: "POST", Path: nd + "/telemetry", Body: telemetry(nil), Headers: device},
		contract.Case{Name: "telemetry-at-inventory-with-session", Method: "POST", Path: own + "/telemetry", Body: telemetry(nil), Headers: device},
		contract.Case{Name: "read-with-session", Path: nd + "/inventory/900100001", Headers: device},
		contract.Case{Name: "read-at-inventory-with-session", Path: own + "/inventory/900100001", Headers: device},
		contract.Case{Name: "read-with-session-without-token", Path: nd + "/inventory/900100001"},
		contract.Case{Name: "upload-with-session-without-token", Method: "POST", Path: nd + "/inventory", Body: report(nil)},
	)
}
