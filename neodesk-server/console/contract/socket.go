package contract

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

// A websocket client written for the suite with the standard library
// (RFC 6455, client side): the handshake, frames in both directions,
// fragmented messages, ping and close. It keeps what the console sends so
// a case can look at it, and never compresses.

// maxSocketMessage is the largest message the suite reads.
const maxSocketMessage = 64 << 20

// socketGUID is the value RFC 6455 adds to the key for the accept hash.
const socketGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

// socketMessage is one message the console sent.
type socketMessage struct {
	binary bool
	data   []byte
}

// socketConn is an open socket.
type socketConn struct {
	conn net.Conn
	br   *bufio.Reader

	writeMu sync.Mutex

	mu       sync.Mutex
	messages []socketMessage
	// closed is set when the console closed the socket (its close frame,
	// or the connection ending without one: code 1006).
	closed      bool
	closeCode   int
	closeReason string
	notify      chan struct{}
	done        chan struct{}
}

// socketDial is the outcome of opening a socket: the handshake's answer,
// and the socket when the console took it.
type socketDial struct {
	status int
	header http.Header
	// body is the answer's body when the console refused the upgrade.
	body []byte
	conn *socketConn
	// protocol is the subprotocol the console chose.
	protocol string
}

// dialSocket sends the opening handshake for target (path and query) to
// the console at base, with the extra headers, and reads the answer. A
// refusal is not an error: its status, headers and body are returned.
func dialSocket(ctx context.Context, base *url.URL, target string, header http.Header) (*socketDial, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", base.Host)
	if err != nil {
		return nil, err
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	keyBytes := make([]byte, 16)
	if _, err := rand.Read(keyBytes); err != nil {
		conn.Close()
		return nil, err
	}
	key := base64.StdEncoding.EncodeToString(keyBytes)
	all := http.Header{}
	all.Set("Upgrade", "websocket")
	all.Set("Connection", "Upgrade")
	all.Set("Sec-WebSocket-Key", key)
	all.Set("Sec-WebSocket-Version", "13")
	for name, values := range header {
		all[http.CanonicalHeaderKey(name)] = values
	}
	if values := all.Values("Sec-WebSocket-Key"); len(values) == 1 {
		key = values[0]
	}
	var b strings.Builder
	fmt.Fprintf(&b, "GET %s HTTP/1.1\r\nHost: %s\r\n", target, base.Host)
	names := make([]string, 0, len(all))
	for name := range all {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		for _, value := range all[name] {
			fmt.Fprintf(&b, "%s: %s\r\n", name, value)
		}
	}
	b.WriteString("\r\n")
	if _, err := io.WriteString(conn, b.String()); err != nil {
		conn.Close()
		return nil, err
	}
	br := bufio.NewReader(conn)
	req, _ := http.NewRequest(http.MethodGet, base.String()+target, nil)
	res, err := http.ReadResponse(br, req)
	if err != nil {
		conn.Close()
		return nil, err
	}
	out := &socketDial{status: res.StatusCode, header: res.Header}
	if res.StatusCode != http.StatusSwitchingProtocols {
		out.body, _ = io.ReadAll(io.LimitReader(res.Body, 1<<20))
		res.Body.Close()
		conn.Close()
		return out, nil
	}
	sum := sha1.Sum([]byte(key + socketGUID))
	if res.Header.Get("Sec-WebSocket-Accept") != base64.StdEncoding.EncodeToString(sum[:]) {
		conn.Close()
		return nil, errors.New("the console's Sec-WebSocket-Accept does not match the key")
	}
	_ = conn.SetDeadline(time.Time{})
	out.protocol = res.Header.Get("Sec-WebSocket-Protocol")
	sc := &socketConn{conn: conn, br: br, notify: make(chan struct{}, 1), done: make(chan struct{})}
	go sc.read()
	out.conn = sc
	return out, nil
}

// read takes frames until the socket ends: messages are kept, pings
// answered, a close frame answered with the same code.
func (sc *socketConn) read() {
	defer close(sc.done)
	var partial []byte
	partialBinary := false
	inMessage := false
	for {
		fin, opcode, payload, err := sc.readFrame()
		if err != nil {
			sc.finish(1006, "")
			return
		}
		switch opcode {
		case 0x0: // continuation
			if !inMessage {
				sc.finish(1006, "")
				return
			}
			partial = append(partial, payload...)
		case 0x1, 0x2:
			partial, partialBinary, inMessage = append([]byte(nil), payload...), opcode == 0x2, true
		case 0x8:
			code, reason := 1005, ""
			if len(payload) >= 2 {
				code = int(binary.BigEndian.Uint16(payload))
				reason = string(payload[2:])
			}
			echo := []byte{}
			if code != 1005 {
				echo = payload[:2]
			}
			_ = sc.writeFrame(0x8, echo)
			sc.finish(code, reason)
			return
		case 0x9:
			_ = sc.writeFrame(0xA, payload)
			continue
		case 0xA:
			continue
		default:
			sc.finish(1006, "")
			return
		}
		if len(partial) > maxSocketMessage {
			sc.finish(1006, "")
			return
		}
		if fin && inMessage {
			sc.mu.Lock()
			sc.messages = append(sc.messages, socketMessage{binary: partialBinary, data: partial})
			sc.mu.Unlock()
			sc.wake()
			partial, inMessage = nil, false
		}
	}
}

func (sc *socketConn) wake() {
	select {
	case sc.notify <- struct{}{}:
	default:
	}
}

// finish notes how the socket ended and drops the connection.
func (sc *socketConn) finish(code int, reason string) {
	sc.mu.Lock()
	if !sc.closed {
		sc.closed, sc.closeCode, sc.closeReason = true, code, reason
	}
	sc.mu.Unlock()
	sc.conn.Close()
	sc.wake()
}

func (sc *socketConn) readFrame() (fin bool, opcode byte, payload []byte, err error) {
	var head [2]byte
	if _, err = io.ReadFull(sc.br, head[:]); err != nil {
		return
	}
	fin = head[0]&0x80 != 0
	opcode = head[0] & 0x0F
	masked := head[1]&0x80 != 0
	length := uint64(head[1] & 0x7F)
	switch length {
	case 126:
		var ext [2]byte
		if _, err = io.ReadFull(sc.br, ext[:]); err != nil {
			return
		}
		length = uint64(binary.BigEndian.Uint16(ext[:]))
	case 127:
		var ext [8]byte
		if _, err = io.ReadFull(sc.br, ext[:]); err != nil {
			return
		}
		length = binary.BigEndian.Uint64(ext[:])
	}
	if length > maxSocketMessage {
		return false, 0, nil, errors.New("frame too large")
	}
	var mask [4]byte
	if masked {
		if _, err = io.ReadFull(sc.br, mask[:]); err != nil {
			return
		}
	}
	payload = make([]byte, length)
	if _, err = io.ReadFull(sc.br, payload); err != nil {
		return
	}
	if masked {
		for i := range payload {
			payload[i] ^= mask[i%4]
		}
	}
	return fin, opcode, payload, nil
}

// writeFrame sends one masked frame, as a client must.
func (sc *socketConn) writeFrame(opcode byte, payload []byte) error {
	sc.writeMu.Lock()
	defer sc.writeMu.Unlock()
	frame := []byte{0x80 | opcode}
	n := len(payload)
	switch {
	case n < 126:
		frame = append(frame, 0x80|byte(n))
	case n <= 0xFFFF:
		frame = append(frame, 0x80|126, byte(n>>8), byte(n))
	default:
		frame = append(frame, 0x80|127)
		frame = binary.BigEndian.AppendUint64(frame, uint64(n))
	}
	var mask [4]byte
	if _, err := rand.Read(mask[:]); err != nil {
		return err
	}
	frame = append(frame, mask[:]...)
	start := len(frame)
	frame = append(frame, payload...)
	for i := range payload {
		frame[start+i] ^= mask[i%4]
	}
	_ = sc.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	_, err := sc.conn.Write(frame)
	return err
}

// send sends a text or binary message.
func (sc *socketConn) send(binaryMessage bool, data []byte) error {
	opcode := byte(0x1)
	if binaryMessage {
		opcode = 0x2
	}
	return sc.writeFrame(opcode, data)
}

// take removes and returns the messages the console sent so far, and
// whether the socket has ended with its code and reason.
func (sc *socketConn) take() ([]socketMessage, bool, int, string) {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	messages := sc.messages
	sc.messages = nil
	return messages, sc.closed, sc.closeCode, sc.closeReason
}

// state reports how many messages wait and whether the socket ended.
func (sc *socketConn) state() (int, bool) {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	return len(sc.messages), sc.closed
}

// close sends a close frame with code 1000 and waits a moment for the
// console's answer, then drops the connection.
func (sc *socketConn) close() {
	sc.mu.Lock()
	closed := sc.closed
	sc.mu.Unlock()
	if !closed {
		payload := binary.BigEndian.AppendUint16(nil, 1000)
		if sc.writeFrame(0x8, payload) == nil {
			select {
			case <-sc.done:
			case <-time.After(2 * time.Second):
			}
		}
	}
	sc.conn.Close()
	<-sc.done
}
