// Package ws holds the console's websockets: a registry of socket paths,
// the checks a socket runs before its handshake, and the accepted socket.
//
// # Registering a socket
//
// A service registers each path once, with a handler that runs the checks
// in the order the old service ran them and then answers the handshake:
//
//	reg.Handle("/ws/panel-events", func(u *ws.Upgrade) {
//		s := u.Session()
//		if !s.SignedIn() {
//			u.Refuse(http.StatusUnauthorized) // "HTTP/1.1 401 Unauthorized", then close
//			return
//		}
//		allowed, err := u.RoleHasPermission(s.UserRole(), "enrollment.approve")
//		if err != nil {
//			u.Refuse(http.StatusServiceUnavailable)
//			return
//		}
//		if !allowed {
//			u.Refuse(http.StatusForbidden)
//			return
//		}
//		conn, ok := u.Accept(ws.AcceptOptions{})
//		if !ok {
//			return // the handshake itself was refused, with the library's answer
//		}
//		defer conn.Wait()
//		conn.KeepAlive(30 * time.Second)
//		_ = conn.Discard()
//	})
//
// The front door (console/server.go) asks Serves whether an upgrade is for
// a registered path. Those go to ServeHTTP and never through the request
// pipeline, as Node's upgrade listener ran outside Express: no security
// headers, no session renewal, no rate limiter, no CSRF gate. Every other
// upgrade goes to the Node console while it is behind this one.
//
// # Paths
//
// A path matches the pathname of new URL(req.url, "http://" + host), as the
// old upgrade router compared it: dot segments resolved, a backslash read
// as a slash, a target that starts with "//" read as a host, no decoding
// of escapes, letter case kept. Patterns have literal segments and at most
// one parameter, {name} or {name:rule}.
//
// # Refusals
//
// The old services refused before the handshake by writing a bare status
// line on the socket and closing it ("HTTP/1.1 403 Forbidden\r\n\r\n");
// Refuse does the same. The websocket library refused a malformed
// handshake with its own answer (status, Connection: close, an HTML body
// that names the problem); Accept does that. A check that ran after the
// handshake closes the socket with a code and a reason (Conn.Close).
//
// # Accepted sockets
//
// A Conn sends through a queue and closes in order with what was queued.
// Read applies the socket's size limit (100 MiB unless the service set
// one) and closes with 1009 when a message is over it, and with 1007 for
// text that is not UTF-8. KeepAlive pings at an interval and, like the old
// services, never acts on a missing answer; Heartbeat drops a peer that
// left two pings in a row unanswered. Every goroutine of a socket
// ends when the socket ends or Registry.Close is called; a handler calls
// Conn.Wait before it returns.
package ws
