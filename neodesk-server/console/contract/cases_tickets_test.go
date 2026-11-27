//go:build contract

package contract_test

import (
	"bytes"
	"compress/gzip"
	"net/url"
	"strings"

	"github.com/neodeskapp/neodesk/neodesk-server/console/contract"
)

// Tickets: the routes people use, and at the end the two device routes
// under /api/tickets/nd (ticketsDeviceCases).
//
// In every role's turn admin sets up one ticket, assigned to the viewer
// account, with a public and an internal comment and an attachment. The
// role reads it, writes to it and works on a ticket of its own; admin
// deletes what is left. So a viewer sees the one ticket assigned to it
// without the internal comment, and the lists and counts are the same in
// every turn.
func init() {
	const base64Hello = "aGVsbG8gdGlja2V0" // "hello ticket"
	megabytes := func(n int) string { return strings.Repeat("A", n<<20) }
	// The large bodies go out compressed: the console counts a body after
	// unpacking it, and the recording keeps what was sent.
	packed := func(body string) string {
		var buf bytes.Buffer
		zw := gzip.NewWriter(&buf)
		_, _ = zw.Write([]byte(body))
		_ = zw.Close()
		return buf.String()
	}
	gzipped := map[string]string{"Content-Encoding": "gzip"}

	contract.Group("tickets", append([]contract.Case{
		// Setup
		contract.Case{
			Name: "setup-create", Method: "POST", Path: "/api/tickets", As: "admin",
			Body: map[string]any{
				"title": "  Contract ticket  ", "description": "Made by the contract cases", "priority": "high",
				"category": "network", "device_id": "900100001", "assigned_to": "e2e_viewer",
			},
			Capture: map[string]string{"ticket": "ticket.id"},
		},
		contract.Case{
			Name: "setup-comment", Method: "POST", Path: "/api/tickets/{ticket}/comments", As: "admin",
			Body: map[string]any{"body": "  A public note  "},
		},
		contract.Case{
			Name: "setup-internal-comment", Method: "POST", Path: "/api/tickets/{ticket}/comments", As: "admin",
			Body: map[string]any{"body": "An internal note", "is_internal": true},
		},
		contract.Case{
			Name: "setup-attach", Method: "POST", Path: "/api/tickets/{ticket}/attachments", As: "admin",
			Body:    map[string]any{"filename": "re port\U0001F4CE.txt", "data": base64Hello, "mimetype": "text/plain"},
			Capture: map[string]string{"attachment": "attachment.id"},
		},

		// Lists and counts
		contract.Case{Name: "list-search", Path: "/api/tickets", Query: url.Values{"search": {"contract TICKET"}}},
		contract.Case{
			Name: "list-filters", Path: "/api/tickets",
			Query: url.Values{"status": {"open"}, "priority": {"high"}, "category": {"network"}, "assigned_to": {"e2e_viewer"}, "device_id": {"900100001"}},
		},
		contract.Case{Name: "list-search-wildcards", Path: "/api/tickets", Query: url.Values{"search": {"%_\\"}}},
		contract.Case{Name: "list-no-match", Path: "/api/tickets", Query: url.Values{"status": {"closed"}, "search": {"Contract ticket"}}},
		contract.Case{Name: "list-status-twice", Path: "/api/tickets", Query: url.Values{"status": {"open", "closed"}}},
		contract.Case{Name: "list-status-in-brackets", Path: "/api/tickets", Query: url.Values{"status[]": {"open"}, "search": {"Contract ticket"}}},
		contract.Case{Name: "list-status-object", Path: "/api/tickets", Query: url.Values{"status[is]": {"open"}}},
		contract.Case{Name: "stats", Path: "/api/tickets/stats"},
		contract.Case{Name: "stats-other-spelling", Path: "/API/Tickets/Stats/"},

		// Create
		contract.Case{
			Name: "create", Method: "POST", Path: "/api/tickets",
			Body:    map[string]any{"title": "Role ticket"},
			Capture: map[string]string{"own": "ticket.id"},
		},
		contract.Case{Name: "create-without-title", Method: "POST", Path: "/api/tickets", Body: map[string]any{"description": "no title"}},
		contract.Case{Name: "create-blank-title", Method: "POST", Path: "/api/tickets", Body: map[string]any{"title": " \t "}},
		contract.Case{Name: "create-title-number", Method: "POST", Path: "/api/tickets", Body: map[string]any{"title": 5}},
		contract.Case{Name: "create-bad-priority", Method: "POST", Path: "/api/tickets", Body: map[string]any{"title": "x", "priority": "urgent"}},
		contract.Case{Name: "create-priority-number", Method: "POST", Path: "/api/tickets", Body: map[string]any{"title": "x", "priority": 1}},
		contract.Case{Name: "create-bad-category", Method: "POST", Path: "/api/tickets", Body: map[string]any{"title": "x", "category": "Hardware"}},
		contract.Case{Name: "create-description-flag", Method: "POST", Path: "/api/tickets", Body: map[string]any{"title": "x", "description": true}},
		contract.Case{Name: "create-without-csrf-token", Method: "POST", Path: "/api/tickets", Body: map[string]any{"title": "x"}, NoCSRF: true},
		contract.Case{
			Name: "create-as-form", Method: "POST", Path: "/api/tickets",
			RawBody: "title=Form+ticket", ContentType: "application/x-www-form-urlencoded",
		},

		// Read
		contract.Case{Name: "read", Path: "/api/tickets/{ticket}"},
		contract.Case{Name: "read-own", Path: "/api/tickets/{own}"},
		contract.Case{Name: "read-missing", Path: "/api/tickets/999999999"},
		contract.Case{Name: "read-id-with-zeros", Path: "/api/tickets/000{ticket}"},
		contract.Case{Name: "read-id-too-long", Path: "/api/tickets/99999999999999999999999"},
		contract.Case{Name: "read-head", Method: "HEAD", Path: "/api/tickets/{ticket}"},

		// Comments
		contract.Case{
			Name: "comment", Method: "POST", Path: "/api/tickets/{ticket}/comments",
			Body: map[string]any{"body": " From the role ", "is_internal": 1},
		},
		contract.Case{Name: "comment-empty", Method: "POST", Path: "/api/tickets/{ticket}/comments", Body: map[string]any{"body": "  "}},
		contract.Case{Name: "comment-number", Method: "POST", Path: "/api/tickets/{ticket}/comments", Body: map[string]any{"body": 7}},
		contract.Case{Name: "comment-missing-ticket", Method: "POST", Path: "/api/tickets/999999999/comments", Body: map[string]any{"body": "x"}},
		contract.Case{Name: "comments", Path: "/api/tickets/{ticket}/comments"},
		contract.Case{Name: "comments-missing-ticket", Path: "/api/tickets/999999999/comments"},

		// Attachments
		contract.Case{
			Name: "attach", Method: "POST", Path: "/api/tickets/{own}/attachments",
			Body:    map[string]any{"filename": "role.bin", "data": "AAEC/w=="},
			Capture: map[string]string{"ownAttachment": "attachment.id"},
		},
		contract.Case{Name: "attach-without-data", Method: "POST", Path: "/api/tickets/{own}/attachments", Body: map[string]any{"filename": "a.txt"}},
		contract.Case{Name: "attach-without-filename", Method: "POST", Path: "/api/tickets/{own}/attachments", Body: map[string]any{"data": base64Hello}},
		contract.Case{Name: "attach-filename-number", Method: "POST", Path: "/api/tickets/{own}/attachments", Body: map[string]any{"filename": 5, "data": base64Hello}},
		contract.Case{Name: "attach-data-number", Method: "POST", Path: "/api/tickets/{own}/attachments", Body: map[string]any{"filename": "n.bin", "data": 5}},
		contract.Case{Name: "attach-data-list", Method: "POST", Path: "/api/tickets/{own}/attachments", Body: map[string]any{"filename": "list.bin", "data": []any{104, 105, 300, "33", nil}}},
		contract.Case{Name: "attach-data-not-base64", Method: "POST", Path: "/api/tickets/{own}/attachments", Body: map[string]any{"filename": "odd.bin", "data": "a G-k_!=Zm9v"}},
		contract.Case{Name: "attach-missing-ticket", Method: "POST", Path: "/api/tickets/999999999/attachments", Body: map[string]any{"filename": "a.txt", "data": base64Hello}},
		contract.Case{
			Name: "attach-as-text", Method: "POST", Path: "/api/tickets/{own}/attachments",
			RawBody: `{"filename":"a.txt","data":"aGk="}`, ContentType: "text/plain",
		},
		contract.Case{
			Name: "attach-malformed-json", Method: "POST", Path: "/api/tickets/{own}/attachments",
			RawBody: `{"filename":"a.txt","data":`, ContentType: "application/json",
		},
		contract.Case{Name: "attach-without-csrf-token", Method: "POST", Path: "/api/tickets/{own}/attachments", Body: map[string]any{"filename": "a.txt", "data": base64Hello}, NoCSRF: true},
		// 3 MB of JSON: more than any other route reads
		contract.Case{
			Name: "attach-three-megabytes", Method: "POST", Path: "/api/tickets/{own}/attachments",
			RawBody: packed(`{"filename":"three.bin","data":"` + megabytes(3) + `"}`), ContentType: "application/json", Headers: gzipped,
			Roles: contract.Roles("viewer", "admin"),
		},
		// 34 MB of JSON holding a file of more than 25 MB
		contract.Case{
			Name: "attach-file-too-large", Method: "POST", Path: "/api/tickets/{own}/attachments",
			RawBody: packed(`{"filename":"big.bin","data":"` + megabytes(34) + `"}`), ContentType: "application/json", Headers: gzipped,
			Roles: contract.Roles("admin"),
		},
		// 36 MB of JSON: over the limit of this route
		contract.Case{
			Name: "attach-body-over-the-limit", Method: "POST", Path: "/api/tickets/{own}/attachments",
			RawBody: packed(`{"filename":"huge.bin","data":"` + megabytes(36) + `"}`), ContentType: "application/json", Headers: gzipped,
			Roles: contract.Roles("signed-out", "viewer", "admin"),
		},
		// The same 3 MB on the route next to it stay over the console's limit
		contract.Case{
			Name: "comment-three-megabytes", Method: "POST", Path: "/api/tickets/{own}/comments",
			RawBody: packed(`{"body":"` + megabytes(3) + `"}`), ContentType: "application/json", Headers: gzipped,
			Roles: contract.Roles("signed-out", "admin"),
		},
		contract.Case{Name: "attachments", Path: "/api/tickets/{ticket}/attachments"},
		contract.Case{Name: "attachments-own", Path: "/api/tickets/{own}/attachments"},
		contract.Case{Name: "attachments-missing-ticket", Path: "/api/tickets/999999999/attachments"},
		contract.Case{Name: "download", Path: "/api/tickets/attachments/{attachment}"},
		contract.Case{Name: "download-head", Method: "HEAD", Path: "/api/tickets/attachments/{attachment}"},
		contract.Case{Name: "download-range", Path: "/api/tickets/attachments/{attachment}", Headers: map[string]string{"Range": "bytes=6-"}},
		contract.Case{Name: "download-range-beyond", Path: "/api/tickets/attachments/{attachment}", Headers: map[string]string{"Range": "bytes=50-60"}},
		contract.Case{Name: "download-not-modified", Path: "/api/tickets/attachments/{attachment}", Headers: map[string]string{"If-None-Match": "*"}},
		contract.Case{Name: "download-own", Path: "/api/tickets/attachments/{ownAttachment}"},
		contract.Case{Name: "download-missing", Path: "/api/tickets/attachments/999999999"},

		// Update
		contract.Case{
			Name: "update", Method: "PATCH", Path: "/api/tickets/{own}",
			Body: map[string]any{
				"title": "  Role ticket, renamed ", "description": "now with text", "status": "in_progress", "priority": "low",
				"category": "software", "assigned_to": "e2e_operator", "sla_due_at": "2031-05-06T07:08:09.000Z", "device_id": " 900100002 ",
			},
		},
		contract.Case{Name: "read-own-after-update", Path: "/api/tickets/{own}"},
		contract.Case{Name: "update-resolve", Method: "PATCH", Path: "/api/tickets/{own}", Body: map[string]any{"status": "resolved", "device_id": "  ", "assigned_to": nil}},
		contract.Case{Name: "update-close", Method: "PATCH", Path: "/api/tickets/{own}", Body: map[string]any{"status": "closed", "device_id": nil}},
		contract.Case{Name: "read-own-after-close", Path: "/api/tickets/{own}"},
		contract.Case{Name: "update-nothing", Method: "PATCH", Path: "/api/tickets/{own}", Body: map[string]any{"id": 1, "created_by": "nobody"}},
		contract.Case{Name: "update-bad-status", Method: "PATCH", Path: "/api/tickets/{own}", Body: map[string]any{"status": "done"}},
		contract.Case{Name: "update-bad-priority", Method: "PATCH", Path: "/api/tickets/{own}", Body: map[string]any{"priority": "urgent"}},
		contract.Case{Name: "update-device-number", Method: "PATCH", Path: "/api/tickets/{own}", Body: map[string]any{"device_id": 900100001}},
		contract.Case{Name: "update-device-too-long", Method: "PATCH", Path: "/api/tickets/{own}", Body: map[string]any{"device_id": strings.Repeat("d", 65)}},
		contract.Case{Name: "update-title-null", Method: "PATCH", Path: "/api/tickets/{own}", Body: map[string]any{"title": nil}},
		contract.Case{Name: "update-status-empty", Method: "PATCH", Path: "/api/tickets/{own}", Body: map[string]any{"status": "", "priority": nil}},
		contract.Case{Name: "update-status-flag", Method: "PATCH", Path: "/api/tickets/{own}", Body: map[string]any{"status": false}},
		contract.Case{Name: "read-own-after-odd-updates", Path: "/api/tickets/{own}"},
		contract.Case{Name: "update-missing", Method: "PATCH", Path: "/api/tickets/999999999", Body: map[string]any{"status": "open"}},
		contract.Case{Name: "update-without-csrf-token", Method: "PATCH", Path: "/api/tickets/{own}", Body: map[string]any{"status": "open"}, NoCSRF: true},
		contract.Case{Name: "stats-after-updates", Path: "/api/tickets/stats"},

		// Delete
		contract.Case{Name: "delete-missing", Method: "DELETE", Path: "/api/tickets/999999999"},
		contract.Case{Name: "delete-without-csrf-token", Method: "DELETE", Path: "/api/tickets/{own}", NoCSRF: true},
		contract.Case{Name: "delete", Method: "DELETE", Path: "/api/tickets/{own}"},
		contract.Case{Name: "delete-again", Method: "DELETE", Path: "/api/tickets/{own}"},
		contract.Case{Name: "download-after-delete", Path: "/api/tickets/attachments/{ownAttachment}"},
		contract.Case{Name: "comments-after-delete", Path: "/api/tickets/{own}/comments"},
		contract.Case{Name: "cleanup-own", Method: "DELETE", Path: "/api/tickets/{own}", As: "admin"},
		contract.Case{Name: "cleanup", Method: "DELETE", Path: "/api/tickets/{ticket}", As: "admin"},
		contract.Case{Name: "stats-after-cleanup", Path: "/api/tickets/stats"},
	}, ticketsDeviceCases()...)...)
}

// ticketsDeviceCases are the two routes of the desktop agent,
// /api/tickets/nd. First what comes before the route: no token, a token
// nobody issued, and the CSRF gate, which checks every write here, also
// one without a session (the mount is not below /api/nd/), with the panel
// access guard in front of it for the roles without device.edit. Then the
// instance's own device (the token of 900100001 in
// /stack/secrets/device_token, of 900100002 in device_token_other) opens
// a ticket and lists its tickets, and another device lists its own.
//
// Only admin's turn, the last, opens a ticket, and admin deletes it at
// the end: the goldens of the cases above hold the ids of the tickets each
// turn makes, and a ticket made in an earlier turn would move them. The
// roles the panel access guard refuses try too; nothing is written for
// them.
//
// The cases with the instance's token were added after the routes had
// moved to Go, so their goldens could not be recorded from Node. They were
// recorded from Go with CONTRACT_RECORD_NEW=1, after their answers on a
// front instance had been checked against the Node recordings that
// routes/tickets replays (testdata/node_device_sqlite.json and
// node_device_postgres.json) for the same requests with the same bodies
// (the ticket "Priority high", the list of the device, the list of a
// device without tickets, a title of blanks): the same status, the keys in
// the same order, values of the same types and forms, and the same values
// apart from ids, times, the device id and the rows the databases hold.
//
// The routes of the receiver app (A277: one ticket, replies, files, read
// marks) came with the Go console, so Node never answered them. Their
// cases were recorded from Go with CONTRACT_RECORD_NEW=1, and the answers
// that gained source, reporter, from, the computer's unread marks and
// the new ticket in the create answer were rewritten with
// CONTRACT_REWRITE; a computer lists the tickets it opened, no longer the
// ones staff opened about it.
func ticketsDeviceCases() []contract.Case {
	ticket := map[string]any{"title": "From the agent", "priority": "high"}
	unknown := map[string]string{"Authorization": "Bearer contract-no-such-token"}
	device := map[string]string{"Authorization": "Bearer {secret:device_token}"}
	other := map[string]string{"Authorization": "Bearer {secret:device_token_other}"}
	return []contract.Case{
		{Name: "device-create-without-token", Method: "POST", Path: "/api/tickets/nd", Body: ticket},
		{Name: "device-create-unknown-token", Method: "POST", Path: "/api/tickets/nd", Body: ticket, Headers: unknown},
		{Name: "device-create-without-csrf-token", Method: "POST", Path: "/api/tickets/nd", Body: ticket, Headers: unknown, NoCSRF: true},
		{
			Name: "device-create-as-a-device", Method: "POST", Path: "/api/tickets/nd", Body: ticket, Headers: unknown,
			NoCSRF: true, NoCookies: true, Roles: contract.Roles("signed-out"),
		},

		// The instance's device
		{
			Name: "device-token-create-as-a-device", Method: "POST", Path: "/api/tickets/nd", Body: ticket, Headers: device,
			NoCSRF: true, NoCookies: true, Roles: contract.Roles("signed-out"),
		},
		{
			Name: "device-token-create-without-title", Method: "POST", Path: "/api/tickets/nd", Headers: device,
			Body: map[string]any{"title": "   "}, Roles: contract.Roles("signed-out", "admin"),
		},
		{
			Name: "device-token-create", Method: "POST", Path: "/api/tickets/nd", Headers: device,
			Body:    map[string]any{"title": "Priority high", "priority": "high", "category": "network"},
			Capture: map[string]string{"agentTicket": "ticket_id"},
			Roles:   contract.Roles("guest_issuer", "viewer", "server_admin", "admin"),
		},
		{Name: "device-token-list", Path: "/api/tickets/nd", Headers: device},
		{Name: "device-token-list-other-device", Path: "/api/tickets/nd", Headers: other},
		{Name: "device-token-list-without-token", Path: "/api/tickets/nd", Roles: contract.Roles("signed-out", "admin")},

		// The receiver app's routes (A277): the computer reads its ticket,
		// replies with a file, reads it again and fetches the file; staff
		// answer in public and in an internal note, the computer sees the
		// answer unread and marks the ticket read. The other computer gets
		// 404 for all of it, as for a ticket that isn't there. In the turns
		// before admin's there is no ticket: every route answers 404. The
		// computer reads its ticket before staff answer, because the
		// staff member's name in the answer is whatever earlier groups left
		// on the account; and the size of computer-info.json depends on the
		// telemetry the instance holds.
		{
			Name: "device-token-read", Path: "/api/tickets/nd/{agentTicket}", Headers: device,
			Volatile: []string{"attachments[0].size"}, Roles: contract.Roles("signed-out", "admin"),
		},
		{Name: "device-token-read-other-device", Path: "/api/tickets/nd/{agentTicket}", Headers: other, Roles: contract.Roles("admin")},
		{
			Name: "device-token-reply", Method: "POST", Path: "/api/tickets/nd/{agentTicket}/comments", Headers: device,
			Body:  map[string]any{"body": "It works again", "files": []any{map[string]any{"name": "note.txt", "type": "text/plain", "data_b64": "aGVsbG8gdGlja2V0"}}},
			Roles: contract.Roles("signed-out", "admin"),
		},
		{
			Name: "device-token-reply-other-device", Method: "POST", Path: "/api/tickets/nd/{agentTicket}/comments", Headers: other,
			Body: map[string]any{"body": "Not my ticket"}, Roles: contract.Roles("admin"),
		},
		{
			Name: "device-token-read-after-reply", Path: "/api/tickets/nd/{agentTicket}", Headers: device,
			Capture:  map[string]string{"agentFile": "attachments[1].id"},
			Volatile: []string{"attachments[0].size"}, Roles: contract.Roles("admin"),
		},
		{Name: "device-token-download", Path: "/api/tickets/nd/{agentTicket}/attachments/{agentFile}", Headers: device, Roles: contract.Roles("signed-out", "admin")},
		{Name: "device-token-download-other-device", Path: "/api/tickets/nd/{agentTicket}/attachments/{agentFile}", Headers: other, Roles: contract.Roles("admin")},
		{
			Name: "device-token-staff-answer", Method: "POST", Path: "/api/tickets/{agentTicket}/comments", As: "admin",
			Body: map[string]any{"body": "Please restart the printer"}, Roles: contract.Roles("admin"),
		},
		{
			Name: "device-token-staff-note", Method: "POST", Path: "/api/tickets/{agentTicket}/comments", As: "admin",
			Body: map[string]any{"body": "Toner is low, order more", "is_internal": true}, Roles: contract.Roles("admin"),
		},
		{Name: "device-token-list-unread", Path: "/api/tickets/nd", Headers: device, Roles: contract.Roles("admin")},
		{Name: "device-token-staff-list-from-computers", Path: "/api/tickets", Query: url.Values{"source": {"device"}}, Roles: contract.Roles("admin")},
		{Name: "device-token-mark-read", Method: "POST", Path: "/api/tickets/nd/{agentTicket}/read", Headers: device, Body: map[string]any{}, Roles: contract.Roles("signed-out", "admin")},
		{Name: "device-token-mark-read-other-device", Method: "POST", Path: "/api/tickets/nd/{agentTicket}/read", Headers: other, Body: map[string]any{}, Roles: contract.Roles("admin")},
		{Name: "device-token-list-read", Path: "/api/tickets/nd", Headers: device, Roles: contract.Roles("admin")},
		{Name: "device-token-cleanup", Method: "DELETE", Path: "/api/tickets/{agentTicket}", As: "admin", Roles: contract.Roles("admin")},
	}
}
