//go:build contract

package contract_test

import (
	"github.com/neodeskapp/neodesk/neodesk-server/console/contract"
)

// Resource control. The API server has no resource policies: a call that
// gets there comes back as 404 {"error":"not found"}, and the compliance
// summary falls back to zeros. The device list comes from the API
// server's devices.
func init() {
	const device = "/api/panel/resource-control/policies/900100001"
	usb := map[string]any{"mode": "allowlist", "allowed": []map[string]string{{"vendor": "046d", "product": "c52b"}}}
	contract.Group("resource-control", append([]contract.Case{
		contract.Case{
			// Whether a seeded device is online changes from second to
			// second.
			Name: "devices", Path: "/api/panel/resource-control/devices", Volatile: []string{"~devices[*].status"},
		},
		contract.Case{Name: "devices-head", Method: "HEAD", Path: "/api/panel/resource-control/devices"},
		contract.Case{Name: "devices-other-spelling", Path: "/API/Panel/Resource-Control/Devices/", Volatile: []string{"~devices[*].status"}},

		contract.Case{Name: "device-policy", Path: device},
		contract.Case{Name: "device-policy-with-escapes", Path: "/api/panel/resource-control/policies/a%3Ab%40c%2Bd"},
		contract.Case{Name: "device-policy-bad-id", Path: "/api/panel/resource-control/policies/bad%20id"},
		contract.Case{Name: "device-policy-two-dots", Path: "/api/panel/resource-control/policies/%2E%2E"},
		contract.Case{Name: "device-policy-dots-inside", Path: "/api/panel/resource-control/policies/a..b"},
		contract.Case{Name: "set-usb", Method: "POST", Path: device + "/usb", Body: usb},
		contract.Case{Name: "set-usb-bad-id", Method: "POST", Path: "/api/panel/resource-control/policies/bad%20id/usb", Body: usb},
		contract.Case{Name: "set-optical", Method: "POST", Path: device + "/optical", Body: map[string]any{"mode": "block"}},
		contract.Case{Name: "set-optical-bad-id", Method: "POST", Path: "/api/panel/resource-control/policies/bad%20id/optical", Body: map[string]any{}},
		contract.Case{Name: "set-monitors", Method: "POST", Path: device + "/monitors", Body: map[string]any{"max": 2}},
		contract.Case{Name: "set-monitors-bad-id", Method: "POST", Path: "/api/panel/resource-control/policies/bad%20id/monitors", Body: map[string]any{}},
		contract.Case{Name: "set-disks", Method: "POST", Path: device + "/disks", Body: map[string]any{"read_only": []string{"D:"}}},
		contract.Case{Name: "set-disks-bad-id", Method: "POST", Path: "/api/panel/resource-control/policies/bad%20id/disks", Body: map[string]any{}},
		contract.Case{Name: "set-quotas", Method: "POST", Path: device + "/quotas", Body: map[string]any{"users": map[string]int{"demo.front": 500}}},
		contract.Case{Name: "set-quotas-bad-id", Method: "POST", Path: "/api/panel/resource-control/policies/bad%20id/quotas", Body: map[string]any{}},
		contract.Case{Name: "set-usb-list", Method: "POST", Path: device + "/usb", RawBody: `["a"]`, ContentType: "application/json"},
		contract.Case{Name: "set-usb-without-csrf-token", Method: "POST", Path: device + "/usb", Body: usb, NoCSRF: true},
		contract.Case{Name: "set-usb-as-form", Method: "POST", Path: device + "/usb", RawBody: "mode=block", ContentType: "application/x-www-form-urlencoded"},
		contract.Case{Name: "set-usb-malformed-json", Method: "POST", Path: device + "/usb", RawBody: `{"mode":`, ContentType: "application/json"},

		contract.Case{Name: "compliance", Path: "/api/panel/resource-control/compliance"},
		contract.Case{Name: "compliance-not-modified", Path: "/api/panel/resource-control/compliance", Headers: map[string]string{"If-None-Match": "*"}},
		contract.Case{Name: "org-policy", Path: "/api/panel/resource-control/org-policy"},
		contract.Case{Name: "org-policy-set", Method: "PUT", Path: "/api/panel/resource-control/org-policy", Body: map[string]any{"usb": usb}},
		contract.Case{Name: "org-policy-set-without-csrf-token", Method: "PUT", Path: "/api/panel/resource-control/org-policy", Body: map[string]any{}, NoCSRF: true},

		// The second mount
		contract.Case{Name: "second-mount-devices", Path: "/api/nd/api/panel/resource-control/devices", Volatile: []string{"~devices[*].status"}},
		contract.Case{Name: "second-mount-device-policy", Path: "/api/nd/api/panel/resource-control/policies/bad%20id"},
		contract.Case{Name: "second-mount-set-usb", Method: "POST", Path: "/api/nd/api/panel/resource-control/policies/900100001/usb", Body: usb},
		contract.Case{Name: "second-mount-set-usb-without-csrf-token", Method: "POST", Path: "/api/nd/api/panel/resource-control/policies/900100001/usb", Body: usb, NoCSRF: true},
		contract.Case{
			Name: "second-mount-set-usb-without-session", Method: "POST", Path: "/api/nd/api/panel/resource-control/policies/900100001/usb", Body: usb,
			NoCSRF: true, NoCookies: true, Roles: contract.Roles("signed-out", "admin"),
		},
		contract.Case{Name: "second-mount-compliance", Path: "/api/nd/api/panel/resource-control/compliance"},
		contract.Case{Name: "second-mount-org-policy-set", Method: "PUT", Path: "/api/nd/api/panel/resource-control/org-policy", Body: map[string]any{}},
	}, resourceControlDeviceCases()...)...)
}

// resourceControlDeviceCases are the route devices fetch their resource
// policy from, at both mounts. It takes a device token, which the suite
// has none of: the headers of a case are written into its golden. So the
// cases show what comes before the route. The answers with a token are
// pinned by routes/resourcecontrol/testdata/device_vectors.json.
func resourceControlDeviceCases() []contract.Case {
	unknown := map[string]string{"Authorization": "Bearer contract-no-such-token"}
	var cases []contract.Case
	for _, mount := range []struct{ name, prefix string }{{"device", ""}, {"second-mount-device", "/api/nd"}} {
		at := func(what string) string { return mount.name + "-policy-" + what }
		path := mount.prefix + "/api/nd/resource-policy"
		cases = append(cases,
			contract.Case{Name: at("without-token"), Path: path},
			contract.Case{Name: at("unknown-token"), Path: path, Headers: unknown},
			contract.Case{Name: at("device-id-alone"), Path: path, Headers: map[string]string{"X-Device-Id": "900100001"}},
			contract.Case{Name: at("other-scheme"), Path: path, Headers: map[string]string{"Authorization": "Basic contract"}},
			contract.Case{Name: at("as-a-device"), Path: path, Headers: unknown, NoCookies: true, Roles: contract.Roles("signed-out")},
		)
	}
	return cases
}
