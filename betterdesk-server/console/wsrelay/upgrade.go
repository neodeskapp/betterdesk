package wsrelay

import (
	"context"
	"net/http"
	"regexp"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/jsbind"
	"github.com/unitronix/betterdesk-server/console/ws"
)

// peerIDPattern is a device id a guest may name.
var peerIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{3,64}$`)

// serve runs the checks of an upgrade to /ws/rendezvous (label
// "rendezvous") or /ws/relay ("relay") in the old order, then bridges the
// socket:
//
//  1. the origin check (403);
//  2. a signed-in session, or else a guest token: with a peer_id that is
//     not 3 to 64 letters, digits, _ and -, or a guest_session that is
//     empty or longer than 128, 403; a token the API server does not find
//     good for the device counts as none;
//  3. neither: 401;
//  4. a signed-in account needs device.connect (403; a failed lookup
//     counts as no);
//  5. the handshake, then the bridge's own checks (the address's limit).
func (s *Service) serve(u *ws.Upgrade, label string) {
	pathname := u.Path
	if !u.CheckOrigin("ws-proxy " + pathname) {
		return
	}
	sess := u.Session()
	hasUser := sess.SignedIn()
	var guest *guestGrant
	if !hasUser {
		token, peerID := s.guestToken(u)
		if token != "" {
			raw, _ := u.SearchParam("guest_session")
			sessionID := jsbind.Trim(raw)
			if peerID == "" || !peerIDPattern.MatchString(peerID) || sessionID == "" || jsLength(sessionID) > 128 {
				logf("WARN: WS proxy: rejected guest upgrade without valid peer_id for %s", pathname)
				u.Refuse(http.StatusForbidden)
				return
			}
			data, err := s.validate(u.Context(), token, peerID)
			if err != nil {
				logf("WARN: WS proxy: guest token validation failed for %s: %s", pathname, err.Error())
			} else if goapi.Truthy(goapi.Dig(data, "valid")) {
				guest = &guestGrant{data: data, token: token, peerID: peerID, sessionID: sessionID}
			}
		}
	}
	if !hasUser && guest == nil {
		logf("WARN: WS proxy: Rejected upgrade to %s — no authenticated session (ip: %s)", pathname, u.SocketAddr())
		u.Refuse(http.StatusUnauthorized)
		return
	}
	if hasUser {
		role := sess.UserRole()
		allowed, err := u.RoleHasPermission(role, "device.connect")
		if err != nil {
			logf("WARN: WS proxy: permission lookup failed: %s", err.Error())
			allowed = false
		}
		if !allowed {
			if role == "" {
				role = "unknown"
			}
			logf("WARN: WS proxy: Rejected %s upgrade for non-operator role %s", pathname, role)
			u.Refuse(http.StatusForbidden)
			return
		}
	}
	target := s.signalAddr
	messageTransport := false
	if label == "relay" {
		target = s.relayAddr
		transport, ok := u.SearchParam("transport")
		messageTransport = ok && transport == "message"
	}
	conn, ok := u.Accept(ws.AcceptOptions{})
	if !ok {
		return
	}
	defer conn.Wait()
	s.bridge(conn, u.ClientAddr(), label, target, messageTransport, guest)
}

// guestToken reads a guest's token and device: ?guest=, else ?t=, else
// the first guest cookie (GuestCookie, bd.guest, betterdesk.guest) that
// holds a token, and ?peer_id=, each without the white space around it. A
// cookie value whose escapes are not UTF-8 makes the token "", as the old
// service's catch did.
func (s *Service) guestToken(u *ws.Upgrade) (token, peerID string) {
	value, _ := u.SearchParam("guest")
	if value == "" {
		value, _ = u.SearchParam("t")
	}
	token = jsbind.Trim(value)
	rawPeer, _ := u.SearchParam("peer_id")
	peerID = jsbind.Trim(rawPeer)
	if token != "" {
		return token, peerID
	}
	return cookieToken(cookieHeader(u.R), s.opts.GuestCookie), peerID
}

// cookieHeader is req.headers.cookie: the Cookie headers joined with "; ",
// each byte read as a character as Node read header bytes.
func cookieHeader(r *http.Request) string {
	return latin1(strings.Join(r.Header.Values("Cookie"), "; "))
}

// latin1 reads every byte as the character of that number.
func latin1(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			runes := make([]rune, len(s))
			for j := 0; j < len(s); j++ {
				runes[j] = rune(s[j])
			}
			return string(runes)
		}
	}
	return s
}

// cookieToken is the old service's own cookie reader: the header split at
// ";", each part without white space around it, and for each name in
// turn the first part that starts with name=, its value
// decodeURIComponent'ed and trimmed; the first name with a token wins. A
// value decodeURIComponent throws on ends the search with "".
func cookieToken(header, guestCookie string) string {
	parts := strings.Split(header, ";")
	for i, p := range parts {
		parts[i] = jsbind.Trim(p)
	}
	for _, name := range []string{guestCookie, "bd.guest", "betterdesk.guest"} {
		for _, p := range parts {
			if !strings.HasPrefix(p, name+"=") {
				continue
			}
			decoded, ok := decodeURIComponent(p[len(name)+1:])
			if !ok {
				return ""
			}
			if token := jsbind.Trim(decoded); token != "" {
				return token
			}
			break
		}
	}
	return ""
}

// decodeURIComponent works like the JavaScript function: each %XX run
// must spell out whole UTF-8 characters, else ok is false (URIError).
func decodeURIComponent(s string) (string, bool) {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		if s[i] != '%' {
			b.WriteByte(s[i])
			i++
			continue
		}
		first, ok := hexByte(s, i)
		if !ok {
			return "", false
		}
		i += 3
		if first < 0x80 {
			b.WriteByte(first)
			continue
		}
		size := 0
		switch {
		case first&0xE0 == 0xC0:
			size = 2
		case first&0xF0 == 0xE0:
			size = 3
		case first&0xF8 == 0xF0:
			size = 4
		default:
			return "", false
		}
		seq := []byte{first}
		for k := 1; k < size; k++ {
			next, ok := hexByte(s, i)
			if !ok || next&0xC0 != 0x80 {
				return "", false
			}
			seq = append(seq, next)
			i += 3
		}
		// One whole character in its shortest form, not a surrogate
		if !utf8.Valid(seq) {
			return "", false
		}
		b.Write(seq)
	}
	return b.String(), true
}

// hexByte reads the %XX at s[i:].
func hexByte(s string, i int) (byte, bool) {
	if i+2 >= len(s) || s[i] != '%' {
		return 0, false
	}
	hi, ok1 := unhex(s[i+1])
	lo, ok2 := unhex(s[i+2])
	return hi<<4 | lo, ok1 && ok2
}

func unhex(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}

// jsLength is a text's length in UTF-16 code units.
func jsLength(s string) int {
	return len(utf16.Encode([]rune(s)))
}

// validate asks the API server whether a guest's token is good for the
// device and returns its answer (response.data).
func (s *Service) validate(ctx context.Context, token, peerID string) (any, error) {
	ctx, cancel := context.WithTimeout(ctx, validateTimeout)
	defer cancel()
	resp, err := s.opts.GoAPI.Get(ctx, "/guest/access-links/validate",
		goapi.Query("token", token), goapi.Query("peer_id", peerID), goapi.Timeout(validateTimeout))
	if err != nil {
		return nil, err
	}
	return resp.Data(), nil
}
