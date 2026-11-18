//go:build contract

package contract_test

import (
	"net/url"

	"github.com/unitronix/betterdesk-server/console/contract"
)

// The registration routes (routes/registration.routes.js), mounted at the
// root and at /api/bd: the two routes devices call after LAN discovery
// (register-request, register-status), the registration list for people
// with its approval and rejection, the enrollment queue of the API server,
// and the help requests.
//
// Each role's turn makes its own LAN registrations as a device and
// removes them at the end. The approval chain uses the seeded device
// 900100021 (no note, no display name, tag kiosk, no folder, no group): a
// role that may approve gives it a display name, two tags, a folder and a
// device group of the turn, and the turn puts the device back and deletes
// the folder and the group. Rejected enrollments of the turn are cleared
// again. The seeded help request 2 ends resolved: the console cannot set a
// help request back to pending, and cannot open one either (the device
// route POST /api/bd/help-request answers with a made-up id, because the
// API server has no route to create one).
//
// console_url in an approval says the console's port. In the test stack
// Node listens on 5001 behind the Go front door on 5000 (DECISIONS A18),
// so the value depends on which process answers; the marks with ~ leave
// only its type to compare.
func init() {
	const bd = "/api/bd"
	device := func(c contract.Case) contract.Case {
		c.NoCSRF, c.NoCookies, c.Roles = true, true, contract.Roles("signed-out")
		return c
	}
	// setupDevice is a device request made in every role's turn
	setupDevice := func(c contract.Case) contract.Case {
		c.NoCSRF, c.NoCookies, c.As = true, true, "signed-out"
		return c
	}
	request := func(id, hostname string) map[string]any {
		return map[string]any{
			"device_id": id, "hostname": hostname, "platform": "Windows", "version": "1.4.2",
			"public_key": "contract-public-key", "uuid": "contract-uuid",
		}
	}
	status := func(name, id string) contract.Case {
		return setupDevice(contract.Case{Name: name, Path: bd + "/register-status", Query: url.Values{"device_id": {id}}, Volatile: []string{"~config.console_url"}})
	}
	pending := map[string]string{"type": "registration_pending"}
	changed := map[string]string{"type": "registration_changed"}
	approval := []string{"~data.console_url"}

	contract.Group("registration",
		// The device routes: what they refuse, once, as a device
		device(contract.Case{Name: "device-request-empty", Method: "POST", Path: bd + "/register-request", Body: map[string]any{}}),
		device(contract.Case{Name: "device-request-short-id", Method: "POST", Path: bd + "/register-request", Body: map[string]any{"device_id": "ab"}}),
		device(contract.Case{Name: "device-request-number-id", Method: "POST", Path: bd + "/register-request", Body: map[string]any{"device_id": 900100099}}),
		device(contract.Case{Name: "device-request-list-id", Method: "POST", Path: bd + "/register-request", Body: map[string]any{"device_id": []any{"contract-list"}}}),
		device(contract.Case{Name: "device-request-list-body", Method: "POST", Path: bd + "/register-request", RawBody: `["contract-list"]`, ContentType: "application/json"}),
		device(contract.Case{Name: "device-request-malformed", Method: "POST", Path: bd + "/register-request", RawBody: `{"device_id":`, ContentType: "application/json"}),
		device(contract.Case{
			Name: "device-request-number-hostname", Method: "POST", Path: bd + "/register-request",
			Body: map[string]any{"device_id": "contract-odd", "hostname": 12},
		}),
		device(contract.Case{
			Name: "device-request-text", Method: "POST", Path: bd + "/register-request",
			RawBody: "device_id=contract-text", ContentType: "text/plain",
		}),
		device(contract.Case{
			Name: "device-request-form", Method: "POST", Path: bd + "/register-request",
			RawBody: "device_id=contract-form", ContentType: "application/x-www-form-urlencoded",
		}),
		device(contract.Case{Name: "device-status-without-id", Path: bd + "/register-status"}),
		device(contract.Case{Name: "device-status-empty-id", Path: bd + "/register-status", Query: url.Values{"device_id": {""}}}),
		device(contract.Case{Name: "device-status-unknown", Path: bd + "/register-status", Query: url.Values{"device_id": {"contract-none"}}}),
		device(contract.Case{Name: "device-status-two-ids", Path: bd + "/register-status", Query: url.Values{"device_id": {"contract-a", "contract-b"}}}),
		device(contract.Case{Name: "device-status-id-list", Path: bd + "/register-status", Query: url.Values{"device_id[]": {"contract-none"}}}),
		device(contract.Case{Name: "device-status-seeded", Path: bd + "/register-status", Query: url.Values{"device_id": {"900300001"}}}),
		device(contract.Case{Name: "root-request-without-csrf-token", Method: "POST", Path: "/register-request", Body: map[string]any{"device_id": "ab"}}),
		device(contract.Case{Name: "root-status", Path: "/register-status", Query: url.Values{"device_id": {"contract-none"}}}),

		// A request, its event on the panel socket, the list for people, and
		// the approval with a folder, a device group, a display name and tags
		contract.Case{Name: "panel-open", Path: "/ws/panel-events", As: "admin", Socket: &contract.Socket{Name: "panel"}},
		setupDevice(contract.Case{
			Name: "device-request", Method: "POST", Path: bd + "/register-request",
			Body: request("900100021", "contract-lan-kiosk"), Capture: map[string]string{"registration": "id"},
		}),
		contract.Case{Name: "panel-pending", Path: "/ws/panel-events", As: "admin", Socket: &contract.Socket{Name: "panel", Expect: 1, Filter: pending}},
		setupDevice(contract.Case{Name: "device-request-again", Method: "POST", Path: bd + "/register-request", Body: request("900100021", "contract-lan-kiosk-renamed")}),
		contract.Case{Name: "panel-pending-again", Path: "/ws/panel-events", As: "admin", Socket: &contract.Socket{Name: "panel", Expect: 1, Filter: pending}},
		status("device-status-pending", "900100021"),
		contract.Case{Name: "list", Path: "/api/registrations"},
		contract.Case{Name: "list-bd", Path: bd + "/api/registrations"},
		contract.Case{Name: "list-pending", Path: "/api/registrations", Query: url.Values{"status": {"pending"}}},
		contract.Case{Name: "list-approved", Path: "/api/registrations", Query: url.Values{"status": {"approved"}}},
		contract.Case{Name: "list-search-letter-case", Path: "/api/registrations", Query: url.Values{"search": {"LAN-KIOSK"}}},
		contract.Case{Name: "list-search-wildcard", Path: "/api/registrations", Query: url.Values{"search": {"demo_lan"}}},
		contract.Case{Name: "list-search-address", Path: "/api/registrations", Query: url.Values{"search": {"10.0.9.1"}}},
		contract.Case{Name: "list-search-nothing", Path: "/api/registrations", Query: url.Values{"search": {"contract-nothing"}, "status": {"pending"}}},
		contract.Case{Name: "count", Path: "/api/registrations/count"},
		contract.Case{Name: "count-bd", Path: bd + "/api/registrations/count"},
		contract.Case{Name: "enrollment-ui", Path: "/api/registrations/enrollment-ui"},
		contract.Case{Name: "enrollment-ui-bd", Path: bd + "/api/registrations/enrollment-ui"},
		contract.Case{Name: "one", Path: "/api/registrations/{registration}"},
		contract.Case{Name: "one-bd", Path: bd + "/api/registrations/{registration}"},
		contract.Case{Name: "one-missing", Path: "/api/registrations/999999999"},
		contract.Case{Name: "one-not-a-number", Path: "/api/registrations/contract"},
		contract.Case{Name: "one-number-then-text", Path: "/api/registrations/{registration}abc"},
		contract.Case{
			Name: "setup-folder", Method: "POST", Path: "/api/folders", As: "admin",
			Body: map[string]any{"name": "Contract registration folder"}, Capture: map[string]string{"folder": "data.id"},
			// ids depend on what the groups before this one created
			Volatile: []string{"data.id"},
		},
		contract.Case{
			Name: "setup-group", Method: "POST", Path: "/api/device-groups", As: "admin",
			Body:     map[string]any{"name": "Contract registration group"},
			Capture:  map[string]string{"group": "data.guid"},
			Volatile: []string{"data.id"},
		},
		contract.Case{
			Name: "approve-text-body", Method: "PUT", Path: "/api/registrations/{registration}/approve",
			RawBody: "folder_id=1", ContentType: "text/plain",
		},
		contract.Case{
			Name: "approve", Method: "PUT", Path: "/api/registrations/{registration}/approve",
			Body: map[string]any{
				"folder_id": "{folder}", "group_guids": []any{"{group}", "not a guid!", 7},
				"display_name": "  Contract kiosk  ", "tags": " contract-a, ,contract-b ",
			},
			Volatile: approval,
		},
		contract.Case{Name: "panel-changed", Path: "/ws/panel-events", As: "admin", Socket: &contract.Socket{Name: "panel", Expect: 1, Filter: changed, Close: true}},
		contract.Case{Name: "approve-again", Method: "PUT", Path: "/api/registrations/{registration}/approve", Body: map[string]any{}},
		status("device-status-approved", "900100021"),
		setupDevice(contract.Case{Name: "device-request-after-approval", Method: "POST", Path: bd + "/register-request", Body: request("900100021", "contract-lan-kiosk-late")}),
		contract.Case{Name: "approved-device", Path: "/api/devices/900100021", As: "admin", Volatile: []string{"data.folder_id", "data.groups[*].id"}},
		contract.Case{
			Name: "approved-device-groups", Path: "/api/devices/900100021/groups", As: "admin",
			Volatile: []string{"data.groups[*].id", "data.memberships[*].id"},
		},
		contract.Case{Name: "cleanup-registration", Method: "DELETE", Path: "/api/registrations/{registration}", As: "admin"},
		contract.Case{Name: "restore-device", Method: "PATCH", Path: "/api/devices/900100021", As: "admin", Body: map[string]any{"note": "", "display_name": ""}},
		contract.Case{Name: "restore-tags", Method: "PUT", Path: "/api/devices/900100021/tags", As: "admin", Body: map[string]any{"tags": []any{"kiosk"}}},
		contract.Case{Name: "restore-folder", Method: "PATCH", Path: "/api/devices/900100021/folder", As: "admin", Body: map[string]any{"folderId": nil}},
		contract.Case{Name: "cleanup-group", Method: "DELETE", Path: "/api/device-groups/{group}", As: "admin"},
		contract.Case{Name: "cleanup-folder", Method: "DELETE", Path: "/api/folders/{folder}", As: "admin"},
		contract.Case{Name: "restored-device", Path: "/api/devices/900100021", As: "admin"},

		// A rejection at /api/bd, and a rejected device asking again
		setupDevice(contract.Case{
			Name: "device-request-to-reject", Method: "POST", Path: bd + "/register-request",
			Body: request("contract-reject", "contract-reject-box"), Capture: map[string]string{"rejected": "id"},
		}),
		contract.Case{Name: "reject-without-csrf-token", Method: "PUT", Path: "/api/registrations/{rejected}/reject", Body: map[string]any{"reason": "x"}, NoCSRF: true},
		contract.Case{Name: "reject-bd-without-csrf-token", Method: "PUT", Path: bd + "/api/registrations/{rejected}/reject", Body: map[string]any{"reason": "x"}, NoCSRF: true},
		contract.Case{Name: "reject-number-reason", Method: "PUT", Path: bd + "/api/registrations/{rejected}/reject", Body: map[string]any{"reason": 5}},
		contract.Case{Name: "reject-bd", Method: "PUT", Path: bd + "/api/registrations/{rejected}/reject", Body: map[string]any{"reason": "Not one of ours"}},
		contract.Case{Name: "reject-again", Method: "PUT", Path: "/api/registrations/{rejected}/reject", Body: map[string]any{}},
		contract.Case{Name: "approve-rejected", Method: "PUT", Path: bd + "/api/registrations/{rejected}/approve", Body: map[string]any{}},
		status("device-status-rejected", "contract-reject"),
		setupDevice(contract.Case{Name: "device-request-after-rejection", Method: "POST", Path: bd + "/register-request", Body: request("contract-reject", "contract-reject-box")}),
		status("device-status-asked-again", "contract-reject"),
		contract.Case{Name: "reject-missing", Method: "PUT", Path: "/api/registrations/999999999/reject", Body: map[string]any{"reason": "x"}},
		contract.Case{Name: "approve-missing", Method: "PUT", Path: bd + "/api/registrations/999999999/approve", Body: map[string]any{}},
		contract.Case{Name: "cleanup-rejected", Method: "DELETE", Path: bd + "/api/registrations/{rejected}", As: "admin"},

		// Deleting
		setupDevice(contract.Case{
			Name: "device-request-to-delete", Method: "POST", Path: bd + "/register-request",
			Body: request("contract-delete", "contract-delete-box"), Capture: map[string]string{"doomed": "id"},
		}),
		contract.Case{Name: "delete", Method: "DELETE", Path: "/api/registrations/{doomed}"},
		contract.Case{Name: "delete-again", Method: "DELETE", Path: bd + "/api/registrations/{doomed}"},
		contract.Case{Name: "delete-missing", Method: "DELETE", Path: "/api/registrations/999999999"},
		contract.Case{Name: "cleanup-doomed", Method: "DELETE", Path: "/api/registrations/{doomed}", As: "admin"},

		// The device routes at the root mount, and with a session at /api/bd,
		// where the CSRF gate answers first
		contract.Case{
			Name: "root-request", Method: "POST", Path: "/register-request",
			Body: request("contract-root", "contract-root-box"), Capture: map[string]string{"rooted": "id"},
		},
		contract.Case{Name: "cleanup-rooted", Method: "DELETE", Path: "/api/registrations/{rooted}", As: "admin"},
		contract.Case{
			Name: "root-request-form", Method: "POST", Path: "/register-request",
			RawBody: "device_id=contract-form&hostname=contract-form-box&platform=Linux", ContentType: "application/x-www-form-urlencoded",
			Capture: map[string]string{"formed": "id"},
		},
		contract.Case{Name: "cleanup-formed", Method: "DELETE", Path: "/api/registrations/{formed}", As: "admin"},
		contract.Case{Name: "bd-request-without-csrf-token", Method: "POST", Path: bd + "/register-request", Body: map[string]any{"device_id": "ab"}, NoCSRF: true},
		contract.Case{Name: "bd-status-with-session", Path: bd + "/register-status", Query: url.Values{"device_id": {"contract-none"}}},

		// The API server's enrollment queue
		contract.Case{Name: "enrollment-pending", Path: "/api/enrollment/pending"},
		contract.Case{Name: "enrollment-pending-bd", Path: bd + "/api/enrollment/pending"},
		contract.Case{Name: "enrollment-history", Path: "/api/enrollment/history"},
		contract.Case{Name: "enrollment-history-rejected", Path: bd + "/api/enrollment/history", Query: url.Values{"status": {"rejected"}}},
		contract.Case{Name: "enrollment-history-bad-status", Path: "/api/enrollment/history", Query: url.Values{"status": {"maybe"}}},
		contract.Case{Name: "enrollment-history-status-list", Path: "/api/enrollment/history", Query: url.Values{"status[]": {"rejected"}}},
		contract.Case{
			Name: "enrollment-approve-unknown", Method: "POST", Path: "/api/enrollment/approve/contract-enroll-none",
			Body: map[string]any{"display_name": "Contract", "sync_mode": "standard", "tags": "a,b", "folder_id": 1, "group_guids": []any{"x"}},
		},
		contract.Case{Name: "enrollment-approve-unknown-bd", Method: "POST", Path: bd + "/api/enrollment/approve/contract-enroll-none", Body: map[string]any{}},
		contract.Case{Name: "enrollment-reject", Method: "POST", Path: "/api/enrollment/reject/contract-enroll-x", Body: map[string]any{"ban": false}},
		contract.Case{Name: "enrollment-history-after-reject", Path: "/api/enrollment/history", Query: url.Values{"status": {"rejected"}}, Volatile: []string{"data[*].decided_at"}},
		contract.Case{Name: "enrollment-clear-rejection", Method: "POST", Path: "/api/enrollment/clear-rejection/contract-enroll-x", Body: map[string]any{}},
		contract.Case{Name: "enrollment-clear-rejection-again", Method: "POST", Path: "/api/enrollment/clear-rejection/contract-enroll-x", Body: map[string]any{}},
		contract.Case{Name: "enrollment-reject-ban-bd", Method: "POST", Path: bd + "/api/enrollment/reject/contract-enroll-y", Body: map[string]any{"ban": "yes"}},
		contract.Case{Name: "enrollment-clear-ban-bd", Method: "POST", Path: bd + "/api/enrollment/clear-rejection/contract-enroll-y", Body: map[string]any{}},
		contract.Case{Name: "cleanup-enrollment-x", Method: "POST", Path: "/api/enrollment/clear-rejection/contract-enroll-x", As: "admin", Body: map[string]any{}},
		contract.Case{Name: "cleanup-enrollment-y", Method: "POST", Path: "/api/enrollment/clear-rejection/contract-enroll-y", As: "admin", Body: map[string]any{}},
		contract.Case{Name: "cleanup-check-devices", Path: "/api/devices", As: "admin", Query: url.Values{"search": {"contract-enroll"}, "includeDeleted": {"true"}}},
		contract.Case{Name: "cleanup-check-history", Path: "/api/enrollment/history", As: "admin", Query: url.Values{"status": {"rejected"}}},

		// Help requests: the seeded request 2 (pending at the start) is
		// accepted and resolved in every turn of a role that may, at both
		// mounts, so the names come from the one cache of both mounts
		contract.Case{Name: "help-list", Path: "/api/help/requests"},
		contract.Case{Name: "help-list-filtered", Path: bd + "/api/help/requests", Query: url.Values{"status": {"pending"}, "device_id": {"900100017"}}},
		contract.Case{Name: "help-accept", Method: "POST", Path: "/api/help/requests/2/accept", Body: map[string]any{}},
		contract.Case{Name: "help-list-accepted", Path: "/api/help/requests", Query: url.Values{"status": {"acknowledged"}}},
		contract.Case{Name: "help-list-accepted-bd", Path: bd + "/api/help/requests", Query: url.Values{"status": {"acknowledged"}}},
		contract.Case{Name: "help-resolve-bd", Method: "POST", Path: bd + "/api/help/requests/2/resolve", Body: map[string]any{}},
		contract.Case{Name: "help-list-resolved", Path: "/api/help/requests", Query: url.Values{"status": {"resolved"}}},
		contract.Case{Name: "help-accept-missing", Method: "POST", Path: bd + "/api/help/requests/999999999/accept", Body: map[string]any{}},
		contract.Case{Name: "help-accept-zero", Method: "POST", Path: "/api/help/requests/0/accept", Body: map[string]any{}},
		contract.Case{Name: "help-resolve-bad-id", Method: "POST", Path: "/api/help/requests/x%20y/resolve", Body: map[string]any{}},
	)
}
