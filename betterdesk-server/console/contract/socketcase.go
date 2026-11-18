package contract

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// Socket makes a case a step on a websocket. The first case of a role's
// turn that names a socket opens it at the case's Path (with its Query,
// Headers and the role's cookies); later cases with the same Name and
// Path work on the open socket. Each step sends Send, then collects what
// the console sends until Expect messages came, the socket closed, or
// Wait ran out, and then for Quiet more, so a message too many shows. The
// sockets of a turn are closed when the turn ends.
//
// The answer of a step that opens a socket the console refuses is the
// refusal: its status, headers and body. Otherwise the status is 101 and
// the body is JSON:
//
//	{"protocol": "...",                       the subprotocol, when one was chosen
//	 "messages": [{"json": <value>, "order": "type a b{c}"},   a text message that is JSON
//	              {"text": "..."},             other text
//	              {"binary": {"length": 3, "sha256": "..."}}],
//	 "closed": {"code": 4403, "reason": "Invalid token"}}   once the console closed it
//
// "order" lists the keys of every object of a JSON message in the order
// the console wrote them, so a change of order shows although JSON is
// compared by value. A step on a socket that is not open (it was refused,
// or has closed) answers {"socket": "not open"} with the status the
// opening step got.
type Socket struct {
	// Name names the socket in the role's turn.
	Name string
	// Origin is sent as the Origin header: "" sends the console's own
	// origin, as a browser on the console does; "-" sends none.
	Origin string
	// Protocols are offered in Sec-WebSocket-Protocol.
	Protocols []string
	// Send are the messages this step sends: a string goes as text, a
	// SocketBinary as binary, anything else as its JSON text. Strings in
	// them may hold {name} and {secret:name}, as a Body does.
	Send []any
	// Expect is how many messages the step waits for.
	Expect int
	// Filter keeps only messages whose JSON has these top-level values
	// ("type": "device_id_changed"); others are dropped unseen. For
	// sockets where the console also pushes what other things cause.
	Filter map[string]string
	// Wait is the longest the step waits; 5 seconds when zero.
	Wait time.Duration
	// Quiet is how long the step listens after it has what it waited for;
	// 300 milliseconds when zero, none when negative.
	Quiet time.Duration
	// WaitClose waits for the console to close the socket.
	WaitClose bool
	// Close closes the socket from the suite's side (1000) after the step.
	Close bool
}

// SocketBinary is a binary message in Socket.Send.
type SocketBinary []byte

func (sk *Socket) check() error {
	if !variableName.MatchString(sk.Name) {
		return fmt.Errorf("socket name %q: letters, digits and _", sk.Name)
	}
	if sk.Expect < 0 {
		return errors.New("socket expect must not be negative")
	}
	for i, m := range sk.Send {
		switch m.(type) {
		case string, SocketBinary:
		default:
			if _, err := json.Marshal(m); err != nil {
				return fmt.Errorf("socket send %d cannot be sent as JSON: %w", i+1, err)
			}
		}
	}
	return nil
}

// notes describes the step for the golden's request.
func (sk *Socket) notes() []string {
	var out []string
	switch sk.Origin {
	case "":
		out = append(out, "websocket "+sk.Name+", own origin")
	case "-":
		out = append(out, "websocket "+sk.Name+", no Origin")
	default:
		out = append(out, "websocket "+sk.Name+", Origin "+sk.Origin)
	}
	if len(sk.Protocols) > 0 {
		out = append(out, "websocket protocols "+strings.Join(sk.Protocols, ", "))
	}
	for _, m := range sk.Send {
		switch v := m.(type) {
		case string:
			out = append(out, "websocket send text "+quote(v))
		case SocketBinary:
			out = append(out, fmt.Sprintf("websocket send %d binary bytes", len(v)))
		default:
			out = append(out, "websocket send "+string(encodeJSON(v)))
		}
	}
	if sk.Expect > 0 {
		out = append(out, fmt.Sprintf("websocket expect %d messages", sk.Expect))
	}
	if len(sk.Filter) > 0 {
		keys := make([]string, 0, len(sk.Filter))
		for k := range sk.Filter {
			keys = append(keys, k+"="+sk.Filter[k])
		}
		sort.Strings(keys)
		out = append(out, "websocket keep only "+strings.Join(keys, ", "))
	}
	if sk.WaitClose {
		out = append(out, "websocket wait for the close")
	}
	if sk.Close {
		out = append(out, "websocket closed by the suite")
	}
	return out
}

// turnSocket is a socket of a role's turn.
type turnSocket struct {
	conn *socketConn
	// status is what the opening step got: 101, or the refusal's status.
	status int
	// closeShown is set once a step's answer has shown the close.
	closeShown bool
}

// closeSockets closes the open sockets of a turn.
func (tn *turn) closeSockets() {
	for name, ts := range tn.sockets {
		if ts.conn != nil {
			ts.conn.close()
		}
		delete(tn.sockets, name)
	}
}

// socketStep runs a socket case for a browser and returns its answer.
func (s *Suite) socketStep(sess *Session, c *Case, tn *turn, request Request) Answer {
	sk := c.Socket
	n := s.Client.norm()
	ts := tn.sockets[sk.Name]
	if ts == nil {
		ctx, cancel := context.WithTimeout(context.Background(), s.Client.timeout)
		defer cancel()
		d, err := dialSocket(ctx, s.Client.base, request.Path, sess.socketHeaders(request, sk, s.Client))
		if err != nil {
			failed := "connection"
			if errors.Is(err, context.DeadlineExceeded) {
				failed = "timeout"
			}
			return Answer{Failed: failed, Body: Body{Kind: BodyEmpty}}
		}
		ts = &turnSocket{conn: d.conn, status: d.status}
		tn.sockets[sk.Name] = ts
		if d.conn == nil {
			answer := NewAnswer(d.status, d.header, d.body, false, n)
			answer.raw = &rawAnswer{status: d.status, header: d.header.Clone(), body: d.body}
			return answer
		}
		return s.socketAnswer(ts, sk, tn, d.protocol)
	}
	if ts.conn == nil || ts.closeShown {
		return socketNotOpen(ts.status, n)
	}
	return s.socketAnswer(ts, sk, tn, "")
}

func socketNotOpen(status int, n *Normalizer) Answer {
	body := []byte(`{"socket":"not open"}`)
	answer := NewAnswer(status, http.Header{"Content-Type": {"application/json"}}, body, false, n)
	delete(answer.Headers, "content-type")
	return answer
}

// socketHeaders are the headers of an opening handshake: the browser's
// cookies, the suite's name, the origin, the subprotocols and the case's
// own headers.
func (sess *Session) socketHeaders(r Request, sk *Socket, c *Client) http.Header {
	h := http.Header{}
	h.Set("User-Agent", userAgent)
	switch sk.Origin {
	case "":
		h.Set("Origin", c.base.Scheme+"://"+c.base.Host)
	case "-":
	default:
		h.Set("Origin", sk.Origin)
	}
	if len(sk.Protocols) > 0 {
		h.Set("Sec-WebSocket-Protocol", strings.Join(sk.Protocols, ", "))
	}
	if !r.NoCookies {
		var pairs []string
		for _, ck := range sess.jar {
			if r.NoSession && contains(sessionCookies, ck.Name) {
				continue
			}
			pairs = append(pairs, ck.Name+"="+ck.Value)
		}
		if len(pairs) > 0 {
			h.Set("Cookie", strings.Join(pairs, "; "))
		}
	}
	for name, values := range r.Header {
		h.Del(name)
		for _, v := range values {
			h.Add(name, v)
		}
	}
	return h
}

// socketAnswer sends the step's messages, collects what comes back and
// builds the answer.
func (s *Suite) socketAnswer(ts *turnSocket, sk *Socket, tn *turn, protocol string) Answer {
	n := s.Client.norm()
	for _, m := range sk.Send {
		var err error
		switch v := m.(type) {
		case SocketBinary:
			err = ts.conn.send(true, v)
		case string:
			text, ferr := tn.fill(v)
			if ferr != nil {
				err = ferr
				break
			}
			err = ts.conn.send(false, []byte(text))
		default:
			value, ferr := decodeJSON(encodeJSON(v))
			if ferr == nil {
				value, ferr = tn.fillJSON(value)
			}
			if ferr != nil {
				err = ferr
				break
			}
			err = ts.conn.send(false, encodeJSON(value))
		}
		if err != nil {
			break
		}
	}
	wait := sk.Wait
	if wait <= 0 {
		wait = 5 * time.Second
	}
	quiet := sk.Quiet
	if quiet == 0 {
		quiet = 300 * time.Millisecond
	}
	var kept []socketMessage
	closed, code, reason := false, 0, ""
	collect := func() {
		messages, isClosed, c, r := ts.conn.take()
		for _, m := range messages {
			if sk.keeps(m) {
				kept = append(kept, m)
			}
		}
		if isClosed {
			closed, code, reason = true, c, r
		}
	}
	done := func() bool {
		if closed {
			return true
		}
		if sk.WaitClose {
			return false
		}
		return len(kept) >= sk.Expect
	}
	deadline := time.Now().Add(wait)
	for collect(); !done() && time.Now().Before(deadline); collect() {
		select {
		case <-ts.conn.notify:
		case <-time.After(time.Until(deadline)):
		}
	}
	if !closed && quiet > 0 {
		until := time.Now().Add(quiet)
		for collect(); !closed && time.Now().Before(until); collect() {
			select {
			case <-ts.conn.notify:
			case <-time.After(time.Until(until)):
			}
		}
	}
	if sk.Close && !closed {
		ts.conn.close()
		collect()
	}
	ts.closeShown = closed
	body := socketBody(protocol, kept, closed, code, reason)
	answer := NewAnswer(http.StatusSwitchingProtocols, http.Header{"Content-Type": {"application/json"}}, body, false, n)
	delete(answer.Headers, "content-type")
	answer.raw = &rawAnswer{status: http.StatusSwitchingProtocols, header: http.Header{"Content-Type": {"application/json"}}, body: body}
	return answer
}

// keeps reports whether a message passes the step's filter.
func (sk *Socket) keeps(m socketMessage) bool {
	if len(sk.Filter) == 0 {
		return true
	}
	if m.binary {
		return false
	}
	var object map[string]any
	if json.Unmarshal(m.data, &object) != nil {
		return false
	}
	for key, want := range sk.Filter {
		if got, _ := object[key].(string); got != want {
			return false
		}
	}
	return true
}

// socketBody is the JSON body of a socket step's answer.
func socketBody(protocol string, messages []socketMessage, closed bool, code int, reason string) []byte {
	var b bytes.Buffer
	b.WriteByte('{')
	if protocol != "" {
		b.WriteString(`"protocol":`)
		b.Write(encodeJSON(protocol))
		b.WriteByte(',')
	}
	b.WriteString(`"messages":[`)
	for i, m := range messages {
		if i > 0 {
			b.WriteByte(',')
		}
		switch {
		case m.binary:
			sum := sha256.Sum256(m.data)
			fmt.Fprintf(&b, `{"binary":{"length":%d,"sha256":"%s"}}`, len(m.data), hex.EncodeToString(sum[:]))
		case json.Valid(m.data) && bytes.HasPrefix(bytes.TrimSpace(m.data), []byte("{")):
			b.WriteString(`{"json":`)
			b.Write(m.data)
			b.WriteString(`,"order":`)
			b.Write(encodeJSON(keyOrder(m.data)))
			b.WriteByte('}')
		case json.Valid(m.data):
			b.WriteString(`{"json":`)
			b.Write(m.data)
			b.WriteByte('}')
		default:
			b.WriteString(`{"text":`)
			b.Write(encodeJSON(strings.ToValidUTF8(string(m.data), "�")))
			b.WriteByte('}')
		}
	}
	b.WriteByte(']')
	if closed {
		fmt.Fprintf(&b, `,"closed":{"code":%d,"reason":%s}`, code, encodeJSON(reason))
	}
	b.WriteByte('}')
	return b.Bytes()
}

// keyOrder lists the keys of every object in a JSON text in the order
// they were written, with the nesting: `a b{c d} e[{f}]`.
func keyOrder(data []byte) string {
	dec := json.NewDecoder(bytes.NewReader(data))
	var b strings.Builder
	// objects holds, per open container, whether it is an object whose
	// next string is a key.
	type container struct{ object, expectKey bool }
	var stack []container
	valueDone := func() {
		if top := len(stack) - 1; top >= 0 && stack[top].object {
			stack[top].expectKey = true
		}
	}
	for {
		tok, err := dec.Token()
		if err != nil {
			if err != io.EOF {
				return b.String()
			}
			break
		}
		switch t := tok.(type) {
		case json.Delim:
			switch t {
			case '{', '[':
				if len(stack) > 0 {
					b.WriteRune(rune(t))
				}
				stack = append(stack, container{object: t == '{', expectKey: t == '{'})
			case '}', ']':
				stack = stack[:len(stack)-1]
				if len(stack) > 0 {
					b.WriteRune(rune(t))
				}
				valueDone()
			}
		case string:
			if top := len(stack) - 1; top >= 0 && stack[top].expectKey {
				if s := b.String(); s != "" && !strings.HasSuffix(s, "{") && !strings.HasSuffix(s, "[") {
					b.WriteByte(' ')
				}
				b.WriteString(t)
				stack[top].expectKey = false
				continue
			}
			valueDone()
		default:
			valueDone()
		}
	}
	return b.String()
}
