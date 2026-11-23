//go:build contract

package contract_test

import (
	"net/url"

	"github.com/neodeskapp/neodesk/neodesk-server/console/contract"
)

// The web remote client's socket to the relay, /ws/relay: the console
// bridges it to the relay's TCP port. By default the bytes pass as they
// are; with ?transport=message each message is one payload, which the
// console frames towards the relay and unframes towards the browser. The
// relay answers a health check and closes; it closes on a request for a
// relay nobody announced. A guest link holder (no session) must start
// with a relay request for the link's device, sent as a bare payload.
// The link is made and deleted by routes the Go console answers
// (cases/guest pins them as Node answered); those steps were recorded
// with CONTRACT_RECORD_NEW=1.
func init() {
	const path = "/ws/relay"
	const links = "/api/guest/access-links"
	signedOut := contract.Roles("signed-out")
	guestRoles := contract.Roles("signed-out", "admin")
	admin := contract.Roles("admin")
	message := url.Values{"transport": {"message"}}
	sock := func(name string, s contract.Socket) *contract.Socket {
		s.Name = name
		return &s
	}
	guestQuery := func(session string, extra url.Values) url.Values {
		q := url.Values{"guest": {"{secret:glink}"}, "peer_id": {"900100001"}, "guest_session": {session}}
		for k, v := range extra {
			q[k] = v
		}
		return q
	}
	guest := func(name string, q url.Values, s *contract.Socket) contract.Case {
		return contract.Case{Name: name, Path: path, Query: q, NoCookies: true, Roles: signedOut, Socket: s}
	}
	bin := func(b []byte) contract.SocketBinary { return contract.SocketBinary(b) }

	contract.Group("ws-relay",
		contract.Case{Name: "open", Path: path, Socket: sock("r", contract.Socket{Close: true})},
		contract.Case{Name: "foreign-origin", Path: path, Socket: sock("o", contract.Socket{Origin: "https://evil.example"})},
		contract.Case{Name: "without-session", Path: path, NoSession: true, Socket: sock("w", contract.Socket{Close: true})},
		contract.Case{Name: "health-check", Path: path, Socket: sock("h", contract.Socket{Send: []any{bin(rmFrame(rmHealthCheck("contract")))}, WaitClose: true})},
		contract.Case{Name: "health-check-in-two-parts", Path: path, Roles: admin, Socket: sock("h2", contract.Socket{
			Send: []any{bin(rmFrame(rmHealthCheck("contract-parts"))[:3]), bin(rmFrame(rmHealthCheck("contract-parts"))[3:])}, WaitClose: true,
		})},
		contract.Case{Name: "health-check-message", Path: path, Query: message, Socket: sock("m", contract.Socket{Send: []any{bin(rmHealthCheck("contract"))}, WaitClose: true})},
		contract.Case{Name: "health-check-message-as-text", Path: path, Query: message, Roles: admin, Socket: sock("mt", contract.Socket{Send: []any{string(rmHealthCheck("contract-text"))}, WaitClose: true})},
		contract.Case{Name: "health-check-message-framed-twice", Path: path, Query: message, Roles: admin, Socket: sock("m2", contract.Socket{Send: []any{bin(rmFrame(rmHealthCheck("contract")))}, WaitClose: true})},
		contract.Case{Name: "health-check-transport-other", Path: path, Query: url.Values{"transport": {"Message"}}, Roles: admin, Socket: sock("m3", contract.Socket{Send: []any{bin(rmFrame(rmHealthCheck("contract")))}, WaitClose: true})},
		contract.Case{Name: "request-relay-unknown", Path: path, Socket: sock("u", contract.Socket{Send: []any{bin(rmFrame(rmRequestRelay("900100001", "contract-no-such-uuid")))}, WaitClose: true})},

		// A guest link
		contract.Case{
			Name: "guest-link", Method: "POST", Path: links, As: "admin", Roles: guestRoles,
			Body:          map[string]any{"peer_ids": []any{"900100001"}, "ttl_minutes": 10, "label": "Contract relay"},
			Capture:       map[string]string{"link": "id"},
			CaptureSecret: map[string]string{"glink": "token"},
		},
		guest("guest-without-session", url.Values{"guest": {"{secret:glink}"}, "peer_id": {"900100001"}}, sock("g1", contract.Socket{})),
		guest("guest-other-peer", guestQuery("contract-a", url.Values{"peer_id": {"900100002"}}), sock("g2", contract.Socket{})),
		guest("guest-other-device", guestQuery("contract-b", message), sock("g3", contract.Socket{
			Send: []any{bin(rmRequestRelay("900100002", "contract-no-such-uuid"))}, WaitClose: true,
		})),
		guest("guest-not-a-relay-request", guestQuery("contract-c", message), sock("g4", contract.Socket{
			Send: []any{bin(rmHealthCheck("contract"))}, WaitClose: true,
		})),
		guest("guest-own-device", guestQuery("contract-d", message), sock("g5", contract.Socket{
			Send: []any{bin(rmRequestRelay("900100001", "contract-no-such-uuid"))}, WaitClose: true,
		})),
		guest("guest-framed-request", guestQuery("contract-e", nil), sock("g6", contract.Socket{
			Send: []any{bin(rmFrame(rmRequestRelay("900100001", "contract-no-such-uuid")))}, Close: true,
		})),
		contract.Case{
			Name: "guest-with-session", Path: path, Roles: guestRoles, Query: guestQuery("contract-f", message),
			Socket: sock("gs", contract.Socket{Send: []any{bin(rmHealthCheck("contract"))}, WaitClose: true}),
		},
		contract.Case{Name: "guest-link-deleted", Method: "DELETE", Path: links + "/{link}", As: "admin", Roles: guestRoles},
		guest("guest-after-delete", guestQuery("contract-g", message), sock("g7", contract.Socket{})),
	)
}
