//go:build contract

package contract_test

import (
	"net/url"

	"github.com/unitronix/betterdesk-server/console/contract"
)

// File transfers as people start and watch them, under /api/files and
// under /api/bd, where the same routes answer too. A transfer is a record
// in the console's memory; the file itself comes from the device, through
// routes that take a device token and are not part of these cases. So no
// transfer here gets past "pending", and a download ends at "not
// completed yet".
//
// Nothing deletes a transfer: it leaves the list an hour after it was
// cancelled. Each turn cancels what it started, and the lists ask for
// pending transfers only, so one turn does not see the transfers of
// another.
func init() {
	const files = "/api/files"
	const bd = "/api/bd"
	few := contract.Roles("signed-out", "viewer", "operator", "admin")
	ends := contract.Roles("signed-out", "admin")
	admin := contract.Roles("admin")
	some := contract.Roles("viewer", "operator", "admin")
	pending := url.Values{"status": {"pending"}}
	empty := map[string]any{}
	transfer := map[string]any{"direction": "download", "device_id": "900100001", "filename": "../../etc/pass wd?.txt", "size": 600000, "mime_type": "text/plain"}
	start := func(name string, body any, roles ...contract.Role) contract.Case {
		return contract.Case{Name: name, Method: "POST", Path: files + "/transfer", Body: body, Roles: roles}
	}
	// started is a transfer that is cancelled right away, by the role and
	// then by admin for a role that may start but the cancel failed for.
	started := func(name string, body any, roles ...contract.Role) []contract.Case {
		return []contract.Case{
			{Name: name, Method: "POST", Path: files + "/transfer", Body: body, Roles: roles, Capture: map[string]string{"extra": "id"}},
			{Name: name + "-cancelled", Method: "POST", Path: files + "/transfers/{extra}/cancel", Body: empty, As: "admin", Roles: roles},
		}
	}

	cases := []contract.Case{
		{Name: "list-empty", Path: files + "/transfers", Query: pending},
		{Name: "list-head", Method: "HEAD", Path: files + "/transfers", Query: pending, Roles: ends},
		{Name: "list-other-spelling", Path: "/API/Files/Transfers/", Query: pending, Roles: few},
		{Name: "bd-list-empty", Path: bd + "/transfers", Query: pending},

		// What a transfer needs
		start("start-without-device", map[string]any{"direction": "download", "filename": "a.txt"}),
		start("start-without-filename", map[string]any{"direction": "download", "device_id": "900100001", "filename": ""}, some...),
		start("start-without-direction", map[string]any{"device_id": "900100001", "filename": "a.txt"}, some...),
		start("start-unknown-direction", map[string]any{"direction": "sideways", "device_id": "900100001", "filename": "a.txt"}, some...),
		start("start-direction-as-list", map[string]any{"direction": []any{"upload"}, "device_id": "900100001", "filename": "a.txt"}, admin...),
		start("start-too-large", map[string]any{"direction": "upload", "device_id": "900100001", "filename": "a.iso", "size": 4294967297}, some...),
		start("start-too-large-as-text", map[string]any{"direction": "upload", "device_id": "900100001", "filename": "a.iso", "size": "4294967297 bytes"}, admin...),
		start("start-filename-that-cannot-be-text", map[string]any{"direction": "upload", "device_id": "900100001", "filename": map[string]any{"toString": 1}}, ends...),
		{
			Name: "start-list-body", Method: "POST", Path: files + "/transfer",
			RawBody: `[1, 2]`, ContentType: "application/json", Roles: admin,
		},
		{
			Name: "start-without-csrf-token", Method: "POST", Path: files + "/transfer",
			Body: transfer, NoCSRF: true, Roles: few,
		},
		{
			Name: "start-as-form", Method: "POST", Path: files + "/transfer",
			RawBody: "direction=download&device_id=900100001&filename=a.txt", ContentType: "application/x-www-form-urlencoded", Roles: ends,
		},

		// start, watch, cancel
		{
			Name: "start", Method: "POST", Path: files + "/transfer", Body: transfer,
			Capture: map[string]string{"transfer": "id"},
		},
		{Name: "progress", Path: files + "/transfers/{transfer}"},
		{Name: "progress-head", Method: "HEAD", Path: files + "/transfers/{transfer}", Roles: ends},
		{Name: "bd-progress", Path: bd + "/transfers/{transfer}"},
		{Name: "list-pending", Path: files + "/transfers", Query: pending},
		{Name: "bd-list-pending", Path: bd + "/transfers", Query: pending, Roles: some},
		{
			Name: "list-of-the-device", Path: files + "/transfers",
			Query: url.Values{"status": {"pending"}, "device_id": {"900100001"}, "direction": {"download"}}, Roles: some,
		},
		{
			Name: "list-of-another-device", Path: files + "/transfers",
			Query: url.Values{"status": {"pending"}, "device_id": {"900100002"}}, Roles: some,
		},
		{
			Name: "list-other-direction", Path: files + "/transfers",
			Query: url.Values{"status": {"pending"}, "direction": {"upload"}}, Roles: some,
		},
		{
			Name: "list-status-as-list", Path: files + "/transfers",
			Query: url.Values{"status": {"pending", "active"}}, Roles: some,
		},
		{
			Name: "list-device-as-object", Path: files + "/transfers",
			Query: url.Values{"status": {"pending"}, "device_id[x]": {"900100001"}}, Roles: admin,
		},
		{
			Name: "list-empty-filters", Path: files + "/transfers",
			Query: url.Values{"status": {"pending"}, "device_id": {""}, "direction": {""}}, Roles: admin,
		},
		{Name: "download-not-completed", Path: files + "/transfers/{transfer}/download"},
		{Name: "bd-download-not-completed", Path: bd + "/transfers/{transfer}/download", Roles: some},
		{Name: "cancel", Method: "POST", Path: files + "/transfers/{transfer}/cancel", Body: empty},
		{Name: "cancel-as-admin", Method: "POST", Path: files + "/transfers/{transfer}/cancel", Body: empty, As: "admin"},
		{Name: "progress-after-cancel", Path: files + "/transfers/{transfer}"},
		{Name: "download-after-cancel", Path: files + "/transfers/{transfer}/download", Roles: some},
		{Name: "bd-cancel-again", Method: "POST", Path: bd + "/transfers/{transfer}/cancel", Body: empty, Roles: some},
		{Name: "list-after-cancel", Path: files + "/transfers", Query: pending, Roles: some},
		{
			Name: "list-cancelled-of-the-device", Path: files + "/transfers",
			Query: url.Values{"status": {"cancelled"}, "device_id": {"no-such-device"}}, Roles: some,
		},

		// The same under /api/bd
		{
			Name: "bd-start", Method: "POST", Path: bd + "/transfer",
			Body:    map[string]any{"direction": "upload", "device_id": "900100002", "filename": "report.pdf", "size": "262145"},
			Capture: map[string]string{"second": "id"},
		},
		{Name: "bd-start-progress", Path: files + "/transfers/{second}", Roles: some},
		{Name: "bd-cancel", Method: "POST", Path: bd + "/transfers/{second}/cancel", Body: empty},
		{Name: "bd-cancel-as-admin", Method: "POST", Path: bd + "/transfers/{second}/cancel", Body: empty, As: "admin"},

		// Without a transfer
		{Name: "progress-unknown", Path: files + "/transfers/no-such-transfer"},
		{Name: "cancel-unknown", Method: "POST", Path: files + "/transfers/no-such-transfer/cancel", Body: empty},
		{Name: "download-unknown", Path: files + "/transfers/no-such-transfer/download"},
		{Name: "bd-download-unknown", Path: bd + "/transfers/no-such-transfer/download", Roles: few},
		{Name: "cancel-without-body", Method: "POST", Path: files + "/transfers/no-such-transfer/cancel", Roles: few},
		{Name: "cancel-without-csrf-token", Method: "POST", Path: files + "/transfers/no-such-transfer/cancel", Body: empty, NoCSRF: true, Roles: few},
		{Name: "progress-id-bad-escape", Path: files + "/transfers/%ff", Roles: ends},
	}

	// The values of a transfer as the record keeps them
	odd := [][]contract.Case{
		started("start-plain", map[string]any{"direction": "download", "device_id": "900100003", "filename": "plain"}, some...),
		started("start-odd-values", map[string]any{"direction": "upload", "device_id": 5, "filename": []any{"a", "b c"}, "size": "12abc", "mime_type": map[string]any{"x": 1}}, some...),
		started("start-size-below-zero", map[string]any{"direction": "upload", "device_id": "900100003", "filename": "/", "size": -5, "mime_type": 0}, admin...),
		started("start-largest", map[string]any{"direction": "download", "device_id": "900100003", "filename": "big.bin", "size": 4294967296}, admin...),
		started("start-fraction", map[string]any{"direction": "download", "device_id": "900100003", "filename": "..", "size": 262144.5}, admin...),
	}
	for _, pair := range odd {
		cases = append(cases, pair...)
	}
	contract.Group("filetransfer", cases...)
}
