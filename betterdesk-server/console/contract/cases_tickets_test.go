//go:build contract

package contract_test

import (
	"bytes"
	"compress/gzip"
	"net/url"
	"strings"

	"github.com/unitronix/betterdesk-server/console/contract"
)

// Tickets: the routes people use (the two device routes under
// /api/tickets/bd take device tokens and are not part of these cases).
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

	contract.Group("tickets",
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
				"category": "anything goes", "assigned_to": "e2e_operator", "sla_due_at": "2031-05-06T07:08:09.000Z", "device_id": " 900100002 ",
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
	)
}
