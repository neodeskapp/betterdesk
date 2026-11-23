package ws

import (
	"errors"
	"fmt"
	"net/http"
	"regexp"

	"github.com/neodeskapp/neodesk/neodesk-server/console/jsbind"
)

// refusal is an answer to an upgrade that is not taken: the bytes that go
// out on the connection before it is closed.
type refusal string

// statusLine is the answer the old services wrote by hand for a refused
// upgrade: "HTTP/1.1 401 Unauthorized" and an empty line, nothing else.
func statusLine(status int) refusal {
	return refusal(fmt.Sprintf("HTTP/1.1 %d %s\r\n\r\n", status, http.StatusText(status)))
}

// abortHandshake is the answer of the websocket library (ws 8) for a
// handshake it refuses: a status line, three headers, an extra one where
// given, and the message as an HTML body.
func abortHandshake(status int, message string, extra ...string) refusal {
	if message == "" {
		message = http.StatusText(status)
	}
	text := fmt.Sprintf("HTTP/1.1 %d %s\r\nConnection: close\r\nContent-Type: text/html\r\nContent-Length: %d",
		status, http.StatusText(status), len(message))
	for i := 0; i+1 < len(extra); i += 2 {
		text += "\r\n" + extra[i] + ": " + extra[i+1]
	}
	return refusal(text + "\r\n\r\n" + message)
}

// keyPattern is a Sec-WebSocket-Key the library accepts: 16 bytes in
// base64.
var keyPattern = regexp.MustCompile(`^[+/0-9A-Za-z]{22}==$`)

// handshake is what checkHandshake found in an upgrade that may go on.
type handshake struct {
	// protocol is the subprotocol the answer names: the first one the
	// client offered, "" for none.
	protocol string
}

// checkHandshake applies the checks of the websocket library to an
// upgrade, in its order, and returns its answer for the first that fails:
//
//	method other than GET                     405 Invalid HTTP method
//	Upgrade not "websocket" (any case)        400 Invalid Upgrade header
//	Sec-WebSocket-Key not 16 bytes of base64  400 Missing or invalid Sec-WebSocket-Key header
//	Sec-WebSocket-Version not 13 or 8         400 Missing or invalid Sec-WebSocket-Version header
//	Sec-WebSocket-Protocol not a token list   400 Invalid Sec-WebSocket-Protocol header
//
// Headers sent more than once count as their values joined with ", ", as
// Node joined them. Extensions are not negotiated: the old sockets had
// compression off.
func checkHandshake(r *http.Request) (handshake, refusal) {
	if r.Method != http.MethodGet {
		return handshake{}, abortHandshake(http.StatusMethodNotAllowed, "Invalid HTTP method")
	}
	upgrade := r.Header.Values("Upgrade")
	if len(upgrade) == 0 || asciiLower(headerText(r, "Upgrade")) != "websocket" {
		return handshake{}, abortHandshake(http.StatusBadRequest, "Invalid Upgrade header")
	}
	if len(r.Header.Values("Sec-WebSocket-Key")) == 0 || !keyPattern.MatchString(headerText(r, "Sec-WebSocket-Key")) {
		return handshake{}, abortHandshake(http.StatusBadRequest, "Missing or invalid Sec-WebSocket-Key header")
	}
	version := jsbind.ParseNumber(headerText(r, "Sec-WebSocket-Version"))
	if len(r.Header.Values("Sec-WebSocket-Version")) == 0 || (version != 13 && version != 8) {
		return handshake{}, abortHandshake(http.StatusBadRequest, "Missing or invalid Sec-WebSocket-Version header",
			"Sec-WebSocket-Version", "13, 8")
	}
	var h handshake
	if len(r.Header.Values("Sec-WebSocket-Protocol")) > 0 {
		protocols, err := parseSubprotocols(headerText(r, "Sec-WebSocket-Protocol"))
		if err != nil {
			return handshake{}, abortHandshake(http.StatusBadRequest, "Invalid Sec-WebSocket-Protocol header")
		}
		h.protocol = protocols[0]
	}
	return h, ""
}

// asciiLower lowers A to Z only. Node read header bytes one by one, so no
// letter outside ASCII ever lowered to one inside it.
func asciiLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 'a' - 'A'
		}
	}
	return string(b)
}

// tokenChar reports the characters of an HTTP token (RFC 7230).
func tokenChar(c byte) bool {
	if c <= 0x20 || c >= 0x7f {
		return false
	}
	switch c {
	case '"', '(', ')', ',', '/', ':', ';', '<', '=', '>', '?', '@', '[', '\\', ']', '{', '}':
		return false
	}
	return true
}

var errSubprotocols = errors.New("invalid subprotocol list")

// parseSubprotocols reads Sec-WebSocket-Protocol as the library did:
// tokens separated by commas with optional spaces and tabs after a token,
// no empty entry, no token twice. The result keeps the order given.
func parseSubprotocols(header string) ([]string, error) {
	var protocols []string
	seen := map[string]bool{}
	add := func(p string) error {
		if seen[p] {
			return errSubprotocols
		}
		seen[p] = true
		protocols = append(protocols, p)
		return nil
	}
	start, end := -1, -1
	i := 0
	for ; i < len(header); i++ {
		c := header[i]
		switch {
		case end == -1 && tokenChar(c):
			if start == -1 {
				start = i
			}
		case i != 0 && (c == ' ' || c == '\t'):
			if end == -1 && start != -1 {
				end = i
			}
		case c == ',':
			if start == -1 {
				return nil, errSubprotocols
			}
			if end == -1 {
				end = i
			}
			if err := add(header[start:end]); err != nil {
				return nil, err
			}
			start, end = -1, -1
		default:
			return nil, errSubprotocols
		}
	}
	if start == -1 || end != -1 {
		return nil, errSubprotocols
	}
	if err := add(header[start:i]); err != nil {
		return nil, err
	}
	return protocols, nil
}
