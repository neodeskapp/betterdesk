//go:build contract

package contract_test

import (
	"encoding/base64"
	"net/url"

	"github.com/neodeskapp/neodesk/neodesk-server/console/contract"
)

// File transfers as people start and watch them, under /api/files and
// under /api/nd, where the same routes answer too. A transfer is a record
// in the console's memory; the file itself comes from the device, through
// the two routes that take a device token (fileTransferDeviceCases). In
// the cases for people no transfer gets past "pending", and a download
// ends at "not completed yet"; fileTransferChainCases take one transfer
// from start to download.
//
// Nothing deletes a transfer: it leaves the list an hour after it was
// cancelled or completed. Each turn cancels what it started, and the
// lists ask for pending transfers only, so one turn does not see the
// transfers of another.
func init() {
	const files = "/api/files"
	const bd = "/api/nd"
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

		// The same under /api/nd
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
	cases = append(cases, fileTransferDeviceCases()...)
	cases = append(cases, fileTransferChainCases()...)
	contract.Group("filetransfer", cases...)
}

// fileTransferDeviceCases are the two routes a device calls with its
// token, POST /file-transfer and GET /file-transfer/{id}, at both mounts.
// The device is the instance's own: the token of 900100001 in
// /stack/secrets/device_token, and that of 900100002 for another device.
// As a device (signed out, no cookies) it announces a file, reads how far
// the transfer is, sends the file in two chunks, and another device writes
// into the transfer and cancels it (BUGS FT2). Then what comes before the
// routes: no token, a token nobody issued, another device named in the
// body, the CSRF gate of /api/files, and each role's session in front of
// a device's request.
func fileTransferDeviceCases() []contract.Case {
	const files = "/api/files"
	const bd = "/api/nd"
	device := map[string]string{"Authorization": "Bearer {secret:device_token}"}
	other := map[string]string{"Authorization": "Bearer {secret:device_token_other}"}
	unknown := map[string]string{"Authorization": "Bearer contract-no-such-token"}
	none := map[string]string{}
	// asDevice sends a case the way a device does: no session, no cookies,
	// no CSRF token, the device's token unless the case names other headers
	asDevice := func(c contract.Case) contract.Case {
		c.NoCSRF, c.NoCookies, c.Roles = true, true, contract.Roles("signed-out")
		if c.Headers == nil {
			c.Headers = device
		}
		if c.Body != nil {
			c.Method = "POST"
			if c.Path == "" {
				c.Path = bd + "/file-transfer"
			}
		}
		return c
	}
	chunk := func(index int, text string) map[string]any {
		return map[string]any{"action": "chunk", "transfer_id": "{announced}", "chunk_index": index, "data": base64.StdEncoding.EncodeToString([]byte(text))}
	}
	browse := map[string]any{"action": "browse", "path": "C:\\Users"}
	return []contract.Case{
		asDevice(contract.Case{
			Name: "device-init", Body: map[string]any{"action": "init", "filename": "contract report.txt", "size": 11, "mime_type": "text/plain"},
			Capture: map[string]string{"announced": "transfer_id"},
		}),
		asDevice(contract.Case{Name: "device-read", Path: bd + "/file-transfer/{announced}"}),
		asDevice(contract.Case{Name: "device-read-at-files", Path: files + "/file-transfer/{announced}"}),
		asDevice(contract.Case{Name: "device-read-other-device", Path: bd + "/file-transfer/{announced}", Headers: other}),
		asDevice(contract.Case{Name: "device-read-unknown-transfer", Path: bd + "/file-transfer/no-such-transfer"}),
		asDevice(contract.Case{Name: "device-read-without-token", Path: bd + "/file-transfer/{announced}", Headers: none}),
		asDevice(contract.Case{Name: "device-read-unknown-token", Path: bd + "/file-transfer/{announced}", Headers: unknown}),
		asDevice(contract.Case{Name: "device-chunk", Body: chunk(0, "hello ")}),
		asDevice(contract.Case{Name: "device-chunk-rest", Body: chunk(1, "world")}),
		asDevice(contract.Case{Name: "device-read-completed", Path: bd + "/file-transfer/{announced}"}),
		asDevice(contract.Case{Name: "other-device-writes", Body: chunk(2, "!"), Headers: other}),
		asDevice(contract.Case{Name: "other-device-cancels", Body: map[string]any{"action": "cancel", "transfer_id": "{announced}"}, Headers: other}),
		asDevice(contract.Case{Name: "device-read-cancelled", Path: bd + "/file-transfer/{announced}"}),
		asDevice(contract.Case{Name: "device-chunk-after-cancel", Body: chunk(3, "late")}),
		asDevice(contract.Case{Name: "device-browse", Body: browse}),
		asDevice(contract.Case{Name: "device-unknown-action", Body: map[string]any{"action": "nope"}}),
		asDevice(contract.Case{Name: "device-names-other-device", Body: map[string]any{"action": "browse", "device_id": "900100002"}}),
		asDevice(contract.Case{Name: "device-names-itself", Body: map[string]any{"action": "browse", "device_id": "900100001"}}),
		asDevice(contract.Case{Name: "device-message-without-token", Body: browse, Headers: none}),
		asDevice(contract.Case{Name: "device-message-unknown-token", Body: browse, Headers: unknown}),
		asDevice(contract.Case{Name: "device-message-at-files", Path: files + "/file-transfer", Body: browse}),
		asDevice(contract.Case{Name: "device-message-form", RawBody: "action=browse", ContentType: "application/x-www-form-urlencoded", Method: "POST", Path: bd + "/file-transfer"}),

		// A device's request with each role's browser: the session, its
		// cookies and its CSRF token come along
		{Name: "device-message-with-session", Method: "POST", Path: bd + "/file-transfer", Body: browse, Headers: device},
		{Name: "device-message-with-session-without-csrf-token", Method: "POST", Path: bd + "/file-transfer", Body: browse, Headers: device, NoCSRF: true},
		{Name: "device-message-at-files-with-session", Method: "POST", Path: files + "/file-transfer", Body: browse, Headers: device},
		{Name: "device-read-at-files-with-session", Path: files + "/file-transfer/no-such-transfer", Headers: device},
		{Name: "device-read-with-session-without-token", Path: bd + "/file-transfer/no-such-transfer"},
	}
}

// fileTransferChainCases take transfers from start to download, with
// admin at the console and the instance's device (900100001) on the other
// side: admin starts a transfer that the device reads, sends in two chunks
// and admin downloads; the device announces a transfer that admin sees.
// Both sides work on the one registry of the console (BUGS FT1).
//
// These cases need both sides in one console, which the Node console
// behind the Go console never was: its device routes had their own
// registry. They were recorded from Go with CONTRACT_RECORD_NEW=1, after
// their answers had been checked against the Node console with both sides
// in one process (routes/filetransfer/testdata/device_vectors.json, the
// transfer for log.txt and the announced q3 report, with the same file
// names, sizes and chunks): the same status, keys in the same order,
// values of the same types, the same values apart from ids, times, the
// device id, the folder of the file and the account, and the same
// downloaded bytes.
func fileTransferChainCases() []contract.Case {
	const files = "/api/files"
	const bd = "/api/nd"
	device := map[string]string{"Authorization": "Bearer {secret:device_token}"}
	once := contract.Roles("signed-out")
	// asDevice sends a case the way a device does: no session, no cookies,
	// no CSRF token
	asDevice := func(c contract.Case) contract.Case {
		c.NoCSRF, c.NoCookies, c.Roles, c.Headers = true, true, once, device
		if c.Body != nil {
			c.Method, c.Path = "POST", bd+"/file-transfer"
		}
		return c
	}
	chunk := func(transfer string, index int, text string) map[string]any {
		return map[string]any{"action": "chunk", "transfer_id": "{" + transfer + "}", "chunk_index": index, "data": base64.StdEncoding.EncodeToString([]byte(text))}
	}
	return []contract.Case{
		{
			Name: "chain-start", Method: "POST", Path: files + "/transfer", As: "admin", Roles: once,
			Body:    map[string]any{"direction": "download", "device_id": "900100001", "filename": "log.txt", "size": 5},
			Capture: map[string]string{"chain": "id"},
		},
		asDevice(contract.Case{Name: "chain-device-reads", Path: bd + "/file-transfer/{chain}"}),
		asDevice(contract.Case{Name: "chain-device-sends", Body: chunk("chain", 0, "hel")}),
		{Name: "chain-progress", Path: files + "/transfers/{chain}", As: "admin", Roles: once},
		asDevice(contract.Case{Name: "chain-device-sends-the-rest", Body: chunk("chain", 1, "lo")}),
		asDevice(contract.Case{Name: "chain-device-reads-completed", Path: bd + "/file-transfer/{chain}"}),
		{Name: "chain-progress-completed", Path: bd + "/transfers/{chain}", As: "admin", Roles: once},
		{Name: "chain-download", Path: files + "/transfers/{chain}/download", As: "admin", Roles: once},
		{
			Name: "chain-list-completed", Path: files + "/transfers", As: "admin", Roles: once,
			Query: url.Values{"status": {"completed"}, "device_id": {"900100001"}},
		},
		{Name: "chain-cancel", Method: "POST", Path: files + "/transfers/{chain}/cancel", Body: map[string]any{}, As: "admin", Roles: once},
		{Name: "chain-download-after-cancel", Path: files + "/transfers/{chain}/download", As: "admin", Roles: once},

		asDevice(contract.Case{
			Name: "chain-device-announces", Body: map[string]any{"action": "init", "filename": "../reports/q3 report.pdf", "size": 12, "mime_type": "application/pdf"},
			Capture: map[string]string{"announcedByDevice": "transfer_id"},
		}),
		{Name: "chain-announced-progress", Path: files + "/transfers/{announcedByDevice}", As: "admin", Roles: once},
		{
			Name: "chain-announced-listed", Path: files + "/transfers", As: "admin", Roles: once,
			Query: url.Values{"status": {"pending"}, "device_id": {"900100001"}, "direction": {"download"}},
		},
		{Name: "chain-announced-cancel", Method: "POST", Path: files + "/transfers/{announcedByDevice}/cancel", Body: map[string]any{}, As: "admin", Roles: once},
	}
}
