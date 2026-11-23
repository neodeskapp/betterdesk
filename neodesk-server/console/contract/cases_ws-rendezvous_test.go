//go:build contract

package contract_test

import (
	"net/url"
	"strings"

	"github.com/neodeskapp/neodesk/neodesk-server/console/contract"
)

// The web remote client's socket to the signal server, /ws/rendezvous:
// the console bridges it to the signal server's TCP port and passes the
// bytes on. A session needs device.connect; a guest link holder (no
// session) needs a link for the device named in peer_id, and the console
// reads the guest's frames and closes the bridge on a request for another
// device. The signal server sends its signed key first; the key differs
// per instance, so the hash of that message is volatile.
//
// The guest flow runs signed out, without cookies, on a link admin makes
// in the turn. A bridge on a link that admin deletes is closed: by the
// Node console at its next check of the link, within 15 seconds, by the
// Go console at once. Then admin fills the per-address limit of five
// bridges, which /ws/relay shares, and a sixth is closed.
//
// The link is made and deleted by routes the Go console answers
// (cases/guest pins them as Node answered); those steps were recorded
// with CONTRACT_RECORD_NEW=1.
func init() {
	const path = "/ws/rendezvous"
	const links = "/api/guest/access-links"
	signedOut := contract.Roles("signed-out")
	guestRoles := contract.Roles("signed-out", "viewer", "admin")
	admin := contract.Roles("admin")
	sock := func(name string, s contract.Socket) *contract.Socket {
		s.Name = name
		return &s
	}
	guestQuery := func(peer, session string) url.Values {
		q := url.Values{"guest": {"{secret:glink}"}}
		if peer != "" {
			q.Set("peer_id", peer)
		}
		if session != "" {
			q.Set("guest_session", session)
		}
		return q
	}
	// guest is a step of a link holder: no session, no cookies
	guest := func(name string, q url.Values, s *contract.Socket) contract.Case {
		return contract.Case{Name: name, Path: path, Query: q, NoCookies: true, Roles: signedOut, Socket: s}
	}

	cases := []contract.Case{
		{Name: "open", Path: path, Socket: sock("r", contract.Socket{Expect: 1, Close: true})},
		{Name: "foreign-origin", Path: path, Socket: sock("o", contract.Socket{Origin: "https://evil.example"})},
		{Name: "no-origin", Path: path, Socket: sock("n", contract.Socket{Origin: "-", Expect: 1, Close: true})},
		{Name: "without-session", Path: path, NoSession: true, Socket: sock("w", contract.Socket{Close: true})},
		// A step that sends waits for the signal server's key first: TCP may
		// carry the key and the answer in one chunk, which the console passes on
		// as one message
		{Name: "health-check-key", Path: path, Socket: sock("h", contract.Socket{Expect: 1})},
		{Name: "health-check", Path: path, Socket: sock("h", contract.Socket{Send: []any{contract.SocketBinary(rmFrame(rmHealthCheck("contract")))}, WaitClose: true})},
		{Name: "punch-unknown-device-key", Path: path, Socket: sock("p", contract.Socket{Expect: 1})},
		// The answer, punch_hole_response (11) with failure (3) OFFLINE,
		// carries in relay_server (4) the relay address the signal server
		// detects for its machine (its public address, or its own when it
		// finds none), so that field is free and the rest is compared
		{
			Name: "punch-unknown-device", Path: path,
			Socket: sock("p", contract.Socket{
				Send: []any{contract.SocketBinary(rmFrame(rmPunchHole("900199999")))}, Expect: 1, Wait: 10e9, Close: true, Frames: true,
			}),
			Volatile: []string{`~messages[0].frames[0]["11"]["4"]`},
		},
		{Name: "not-a-frame-key", Path: path, Socket: sock("x", contract.Socket{Expect: 1}), Roles: admin},
		{Name: "not-a-frame", Path: path, Socket: sock("x", contract.Socket{Send: []any{"not a frame"}, Close: true}), Roles: admin},

		// A guest link
		{
			Name: "guest-link", Method: "POST", Path: links, As: "admin", Roles: guestRoles,
			Body:          map[string]any{"peer_ids": []any{"900100001"}, "ttl_minutes": 10, "label": "Contract rendezvous"},
			Capture:       map[string]string{"link": "id"},
			CaptureSecret: map[string]string{"glink": "token"},
		},
		guest("guest-without-peer", guestQuery("", "contract-session"), sock("g1", contract.Socket{})),
		guest("guest-odd-peer", guestQuery("9001 00001", "contract-session"), sock("g2", contract.Socket{})),
		guest("guest-short-peer", guestQuery("ab", "contract-session"), sock("g3", contract.Socket{})),
		guest("guest-without-session", guestQuery("900100001", ""), sock("g4", contract.Socket{})),
		guest("guest-long-session", guestQuery("900100001", strings.Repeat("s", 129)), sock("g5", contract.Socket{})),
		guest("guest-unknown-link", url.Values{"guest": {"contract-no-such-link"}, "peer_id": {"900100001"}, "guest_session": {"contract-session"}}, sock("g6", contract.Socket{})),
		guest("guest-other-device", guestQuery("900100002", "contract-session"), sock("g7", contract.Socket{})),
		guest("guest-t-parameter", url.Values{"t": {"{secret:glink}"}, "peer_id": {"900100001"}, "guest_session": {"contract-t"}}, sock("g8", contract.Socket{Expect: 1, Close: true})),
		{
			Name: "guest-from-cookie", Path: path, NoCookies: true, Roles: signedOut,
			Query:   url.Values{"peer_id": {"900100001"}, "guest_session": {"contract-cookie"}},
			Headers: map[string]string{"Cookie": "nd.guest={secret:glink}"},
			Socket:  sock("g9", contract.Socket{Expect: 1, Close: true}),
		},
		{
			Name: "guest-with-session", Path: path, Roles: guestRoles,
			Query:  guestQuery("900100001", "contract-session"),
			Socket: sock("gs", contract.Socket{Expect: 1, Close: true}),
		},
		guest("guest", guestQuery("900100001", "contract-session"), sock("guest", contract.Socket{Expect: 1})),
		guest("guest-punch-other-device", guestQuery("900100001", "contract-session"), sock("guest", contract.Socket{
			Send: []any{contract.SocketBinary(rmFrame(rmPunchHole("900100002")))}, WaitClose: true,
		})),
		guest("guest-not-a-frame-key", guestQuery("900100001", "contract-other"), sock("gx", contract.Socket{Expect: 1})),
		guest("guest-not-a-frame", guestQuery("900100001", "contract-other"), sock("gx", contract.Socket{
			Send: []any{contract.SocketBinary(rmFrame([]byte{0xff}))}, WaitClose: true,
		})),
		guest("guest-revoked-open", guestQuery("900100001", "contract-revoked"), sock("gr", contract.Socket{Expect: 1})),
		{Name: "guest-link-deleted", Method: "DELETE", Path: links + "/{link}", As: "admin", Roles: guestRoles},
		guest("guest-revoked-closed", guestQuery("900100001", "contract-revoked"), sock("gr", contract.Socket{WaitClose: true, Wait: 20e9})),
		guest("guest-after-delete", guestQuery("900100001", "contract-after"), sock("ga", contract.Socket{})),

		// The per-address limit, shared with /ws/relay
		{Name: "limit-state", Path: "/api/remote/sessions", Roles: admin},
	}
	for _, name := range []string{"l1", "l2", "l3", "l4", "l5"} {
		cases = append(cases, contract.Case{Name: "limit-" + name, Path: path, Roles: admin, Socket: sock(name, contract.Socket{Expect: 1})})
	}
	cases = append(cases, contract.Case{Name: "limit-sixth-over-relay", Path: "/ws/relay", Roles: admin, Socket: sock("l6", contract.Socket{WaitClose: true})})
	for _, name := range []string{"l1", "l2", "l3", "l4", "l5"} {
		cases = append(cases, contract.Case{Name: "limit-" + name + "-closed", Path: path, Roles: admin, Socket: sock(name, contract.Socket{Close: true})})
	}
	contract.Group("ws-rendezvous", cases...)
}

// rmVarint is a protobuf varint.
func rmVarint(n int) []byte {
	var out []byte
	for n >= 0x80 {
		out = append(out, byte(n)|0x80)
		n >>= 7
	}
	return append(out, byte(n))
}

// rmField is a length-delimited protobuf field.
func rmField(field int, payload []byte) []byte {
	out := rmVarint(field<<3 | 2)
	out = append(out, rmVarint(len(payload))...)
	return append(out, payload...)
}

// rmFrame puts the length header of RustDesk's BytesCodec in front of a
// payload.
func rmFrame(payload []byte) []byte {
	n := len(payload)
	var head []byte
	switch {
	case n <= 0x3F:
		head = []byte{byte(n << 2)}
	case n <= 0x3FFF:
		v := n<<2 | 1
		head = []byte{byte(v), byte(v >> 8)}
	default:
		v := n<<2 | 2
		head = []byte{byte(v), byte(v >> 8), byte(v >> 16)}
	}
	return append(head, payload...)
}

// rmHealthCheck is a RendezvousMessage with hc{token}.
func rmHealthCheck(token string) []byte {
	return rmField(26, rmField(1, []byte(token)))
}

// rmPunchHole is a RendezvousMessage with punch_hole_request{id}.
func rmPunchHole(id string) []byte {
	return rmField(8, rmField(1, []byte(id)))
}

// rmRequestRelay is a RendezvousMessage with request_relay{id, uuid}.
func rmRequestRelay(id, uuid string) []byte {
	return rmField(18, append(rmField(1, []byte(id)), rmField(2, []byte(uuid))...))
}
