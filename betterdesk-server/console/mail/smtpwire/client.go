package smtpwire

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// client is one connection and the session on it.
//
// The session is driven by the server's replies: every command that expects
// a reply puts a handler on the list of actions, and replies are handed to
// the handlers in order. A reply therefore answers the oldest command still
// waiting, whatever the server meant it for, which is what makes the order
// of commands the same as nodemailer's for a server that sends too many or
// too few replies.
type client struct {
	ctx  context.Context
	opts Options

	host string
	port int
	// name is announced with EHLO and HELO
	name     string
	useCreds bool

	connectionTimeout time.Duration
	greetingTimeout   time.Duration
	socketTimeout     time.Duration
	dnsTimeout        time.Duration
	resolver          Resolver
	families          func() (bool, bool)
	shuffle           func(int, func(i, j int))
	now               func() time.Time

	// plain is the TCP connection, sock the same or TLS on top of it
	plain net.Conn
	sock  net.Conn
	buf   []byte

	replies replyQueue
	// due says that waiting replies are to be handled before more is read
	due     bool
	actions []func(reply string)

	// secure says the session runs over TLS
	secure bool
	// closing says close ran; nothing is read or written after it
	closing bool
	ehlo    ehloInfo
	// smtpUTF8 says a MAIL FROM of this session asked for SMTPUTF8
	smtpUTF8 bool

	// connectionDeadline ends the time for setting the session up. The
	// phase deadline is the limit of the step going on (greeting, STARTTLS
	// handshake) and phaseText the failure when it passes.
	connectionDeadline time.Time
	phaseDeadline      time.Time
	phaseText          string
	// lastActivity is when bytes were last read or written (Unix nanos)
	lastActivity atomic.Int64
	// writeErr is a failed write waiting to be reported
	writeErr error

	// what a call sends: the step after the login, and for a message its
	// envelope, bytes and progress
	afterLogin func()
	envelope   *Envelope
	message    []byte
	env        *envelopeState
	// sending says a message is on its way and has no result yet
	sending bool
	body    *bodyWriter

	// returned says the call has its result
	returned bool
	info     *Info
	err      error

	mu sync.Mutex
	// cancelled says the context ended the connection
	cancelled bool
	// bodyErr is a write error of the message waiting to be reported
	bodyErr error
}

func newClient(ctx context.Context, opts Options) *client {
	c := &client{
		ctx:               ctx,
		opts:              opts,
		host:              opts.Host,
		port:              opts.Port,
		useCreds:          opts.User != "" && opts.Pass != "",
		connectionTimeout: opts.ConnectionTimeout,
		greetingTimeout:   opts.GreetingTimeout,
		socketTimeout:     opts.SocketTimeout,
		dnsTimeout:        opts.DNSTimeout,
		resolver:          opts.Resolver,
		families:          opts.Families,
		shuffle:           opts.Shuffle,
		now:               opts.Now,
		secure:            opts.Secure,
		buf:               make([]byte, 64*1024),
	}
	if c.host == "" {
		c.host = "localhost"
	}
	if c.port == 0 {
		c.port = 587
		if opts.Secure {
			c.port = 465
		}
	}
	if c.connectionTimeout <= 0 {
		c.connectionTimeout = DefaultConnectionTimeout
	}
	if c.greetingTimeout <= 0 {
		c.greetingTimeout = DefaultGreetingTimeout
	}
	if c.socketTimeout <= 0 {
		c.socketTimeout = DefaultSocketTimeout
	}
	if c.dnsTimeout <= 0 {
		c.dnsTimeout = DefaultDNSTimeout
	}
	if c.resolver == nil {
		c.resolver = net.DefaultResolver
	}
	if c.families == nil {
		c.families = defaultFamilies
	}
	if c.shuffle == nil {
		c.shuffle = defaultShuffle
	}
	if c.now == nil {
		c.now = time.Now
	}
	if c.opts.Dial == nil {
		c.opts.Dial = (&net.Dialer{}).DialContext
	}
	hostname := os.Hostname
	if opts.Hostname != nil {
		hostname = opts.Hostname
	}
	c.name = clientName(hostname)
	return c
}

// clientName is the name for EHLO: the machine's host name when it has a
// dot, with an IPv4 address in brackets, and "[127.0.0.1]" otherwise. Line
// breaks are removed so the name cannot carry a second command.
func clientName(hostname func() (string, error)) string {
	name, err := hostname()
	if err != nil || !strings.Contains(name, ".") {
		name = "[127.0.0.1]"
	}
	if isDottedQuad(name) {
		name = "[" + name + "]"
	}
	return strings.NewReplacer("\r", "", "\n", "").Replace(name)
}

// isDottedQuad reports whether s is four groups of one to three digits.
func isDottedQuad(s string) bool {
	groups := strings.Split(s, ".")
	if len(groups) != 4 {
		return false
	}
	for _, group := range groups {
		if len(group) < 1 || len(group) > 3 || !allDigits(group) {
			return false
		}
	}
	return true
}

// run connects, sets the session up and logs in, then does afterLogin and
// handles replies until the call has its result.
func (c *client) run(afterLogin func()) {
	c.afterLogin = afterLogin
	if err := c.connect(); err != nil {
		c.returned = true
		c.err = err
		return
	}
	stop := context.AfterFunc(c.ctx, c.cancel)
	c.lastActivity.Store(time.Now().UnixNano())
	c.startPhase(c.remainingSetupTime(), "Greeting never received")
	c.expect(c.onGreeting)
	c.pump(false)
	stop()
	if !c.closing {
		// QUIT is out and the call has its result. The connection stays
		// until the server answers, closes or the socket timeout passes.
		go c.pump(true)
	}
}

// connect opens the TCP connection and, for implicit TLS, runs the
// handshake.
func (c *client) connect() error {
	c.connectionDeadline = time.Now().Add(c.connectionTimeout)
	t, err := c.resolve()
	if err != nil {
		return err
	}
	conn, err := c.dial(t)
	if err != nil {
		return err
	}
	c.plain, c.sock = conn, conn
	if !c.opts.Secure {
		return nil
	}
	secure := tls.Client(conn, c.tlsConfig())
	_ = conn.SetDeadline(c.connectionDeadline)
	err = secure.HandshakeContext(c.ctx)
	_ = conn.SetDeadline(time.Time{})
	if err != nil {
		_ = conn.Close()
		if c.ctx.Err() != nil {
			return context.Cause(c.ctx)
		}
		if isTimeout(err) {
			return &Error{Message: "Connection timeout", Code: "ETIMEDOUT", Command: "CONN"}
		}
		return handshakeError(err)
	}
	c.sock = secure
	return nil
}

// handshakeError is the failure of a TLS handshake.
func handshakeError(err error) *Error {
	var refused *certError
	text := socketErrorText("read", err, "")
	switch {
	case errors.As(err, &refused):
		text = refused.text
	case isEOF(err):
		text = "Client network socket disconnected before secure TLS connection was established"
	}
	return &Error{Message: text, Code: "ESOCKET", Command: "CONN", cause: err}
}

// cancel ends the connection when the context is done.
func (c *client) cancel() {
	c.mu.Lock()
	c.cancelled = true
	c.mu.Unlock()
	_ = c.plain.Close()
}

func (c *client) isCancelled() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cancelled
}

// pump handles replies and reads more until the call has its result, or in
// the background until the connection is closed.
func (c *client) pump(background bool) {
	for !c.closing && (background || !c.returned) {
		switch {
		case c.writeErr != nil:
			err := c.writeErr
			c.writeErr = nil
			c.onSocketError("write", err)
		case c.due:
			c.due = c.handleReply()
		default:
			c.read()
		}
	}
	if c.body != nil {
		<-c.body.done
	}
}

// read waits for bytes from the server, up to the nearest deadline.
func (c *client) read() {
	deadline := time.Unix(0, c.lastActivity.Load()).Add(c.socketTimeout)
	if !c.phaseDeadline.IsZero() && c.phaseDeadline.Before(deadline) {
		deadline = c.phaseDeadline
	}
	c.mu.Lock()
	if c.cancelled {
		c.mu.Unlock()
		c.abort()
		return
	}
	if err := c.bodyErr; err != nil {
		c.bodyErr = nil
		c.mu.Unlock()
		c.onSocketError("write", err)
		return
	}
	_ = c.sock.SetReadDeadline(deadline)
	c.mu.Unlock()

	n, err := c.sock.Read(c.buf)
	if n > 0 {
		c.lastActivity.Store(time.Now().UnixNano())
		due, tooLarge := c.replies.add(c.buf[:n])
		if tooLarge {
			c.onError(newError("Server response exceeds maximum allowed size", "EPROTOCOL", "", "CONN"))
			return
		}
		c.due = due
		return
	}
	switch {
	case err == nil:
	case c.isCancelled():
		c.abort()
	case isTimeout(err):
		c.onDeadline()
	case isEOF(err):
		c.onEnd()
	default:
		c.onSocketError("read", err)
	}
}

// onDeadline runs when a read ran into its deadline: a phase took too long,
// the connection was silent for the socket timeout, or the message writer
// asked for attention.
func (c *client) onDeadline() {
	now := time.Now()
	switch {
	case !c.phaseDeadline.IsZero() && !now.Before(c.phaseDeadline):
		c.onError(newError(c.phaseText, "ETIMEDOUT", "", "CONN"))
	case now.Sub(time.Unix(0, c.lastActivity.Load())) >= c.socketTimeout:
		c.onError(newError("Timeout", "ETIMEDOUT", "", "CONN"))
	}
}

// onEnd runs when the server closed the connection. A refusal the server
// sent without a line end is added to the error.
func (c *client) onEnd() {
	failure := ""
	if rest := trimReplyBytes(string(c.replies.remainder)); rest != "" {
		if text := decodeReply(rest); isFailureReply(text) {
			failure = text
		}
	}
	c.onError(newError("Connection closed unexpectedly", "ECONNECTION", failure, "CONN"))
}

func (c *client) onSocketError(call string, err error) {
	if isTimeout(err) {
		c.onError(newError("Timeout", "ETIMEDOUT", "", "CONN"))
		return
	}
	c.onError(&Error{Message: socketErrorText(call, err, ""), Code: "ESOCKET", Command: "CONN", cause: err})
}

// abort ends a call whose context is done.
func (c *client) abort() {
	if !c.returned {
		c.returned = true
		c.err = context.Cause(c.ctx)
	}
	c.close()
}

// onError ends the session after a failure of the connection or of a step
// every later step depends on.
func (c *client) onError(err *Error) {
	c.finish(nil, err)
	c.close()
}

// finish gives the call its result and closes the connection. Only the
// first result counts.
func (c *client) finish(info *Info, err *Error) {
	if c.returned {
		return
	}
	c.returned = true
	c.sending = false
	c.info = info
	if err != nil {
		c.err = err
	}
	c.close()
}

// close ends the connection: nothing more is read or written, and the
// socket is closed the way that lets the server see everything written.
func (c *client) close() {
	c.clearPhase()
	c.actions = nil
	if c.closing {
		return
	}
	c.closing = true
	if c.body != nil && c.body.abort() {
		// the message is still being written: cut it off
		_ = c.plain.Close()
		return
	}
	go closeGracefully(c.sock, c.plain)
}

// closeGracefully closes the sending side and waits for the server to close
// its side, so that the server reads what was written before it sees the
// end. A server that keeps its side open is dropped after closeTimeout.
func closeGracefully(sock, plain net.Conn) {
	defer plain.Close()
	type halfCloser interface{ CloseWrite() error }
	if secure, ok := sock.(*tls.Conn); ok {
		_ = plain.SetWriteDeadline(time.Now().Add(closeTimeout))
		if secure.CloseWrite() != nil {
			return
		}
	}
	half, ok := plain.(halfCloser)
	if !ok || half.CloseWrite() != nil {
		return
	}
	_ = plain.SetReadDeadline(time.Now().Add(closeTimeout))
	_, _ = io.Copy(io.Discard, plain)
}

func (c *client) expect(action func(reply string)) {
	c.actions = append(c.actions, action)
}

// handleReply gives the oldest waiting reply to the oldest waiting action.
// It reports whether a reply was taken, which means the next one is due.
func (c *client) handleReply() bool {
	raw, ok := c.replies.next()
	if !ok {
		return false
	}
	if trimReplyBytes(raw) == "" {
		// an empty line between replies
		return true
	}
	reply := decodeReply(raw)
	if len(c.actions) == 0 {
		c.onError(newError("Unexpected Response", "EPROTOCOL", reply, "CONN"))
		return false
	}
	action := c.actions[0]
	c.actions = c.actions[1:]
	// the command gets its own error first, then a 421 ends the session
	action(reply)
	if isClosingReply(reply) {
		c.onServerClosing(reply)
	}
	return true
}

// onServerClosing ends the session after a 421 reply. The replies to the
// commands sent along with the answered one are not going to come, so a
// message on its way fails here.
func (c *client) onServerClosing(reply string) {
	if c.closing {
		return
	}
	sending, env := c.sending, c.env
	c.close()
	if !sending {
		return
	}
	if env != nil && env.mailError != nil {
		c.finish(nil, env.mailError)
		return
	}
	c.finish(nil, newError("Server closed the connection", "ECONNECTION", reply, "CONN"))
}

// sendCommand writes one command line.
func (c *client) sendCommand(command string) {
	c.write([]byte(command + "\r\n"))
}

func (c *client) write(data []byte) {
	if c.closing || c.writeErr != nil {
		return
	}
	_ = c.sock.SetWriteDeadline(time.Now().Add(c.socketTimeout))
	if _, err := c.sock.Write(data); err != nil {
		c.writeErr = err
		return
	}
	c.lastActivity.Store(time.Now().UnixNano())
}

func (c *client) startPhase(limit time.Duration, text string) {
	c.phaseDeadline = time.Now().Add(limit)
	c.phaseText = text
}

func (c *client) clearPhase() {
	c.phaseDeadline = time.Time{}
}

// remainingSetupTime is the time the greeting or a STARTTLS handshake may
// take: the greeting timeout, cut short by what is left of the connection
// timeout.
func (c *client) remainingSetupTime() time.Duration {
	return min(c.greetingTimeout, max(time.Until(c.connectionDeadline), time.Millisecond))
}

// ---- setting the session up

func (c *client) onGreeting(reply string) {
	c.clearPhase()
	if !strings.HasPrefix(reply, "220") {
		c.onError(newError("Invalid greeting. response="+reply, "EPROTOCOL", reply, "CONN"))
		return
	}
	c.expect(c.onEHLO)
	c.sendCommand("EHLO " + c.name)
}

// What an EHLO reply leads to.
const (
	ehloClosing  = "closing"  // 421: the session ends
	ehloRefused  = "refused"  // not a 2xx reply: HELO is tried
	ehloNoTLS    = "notls"    // not a 2xx reply where TLS is required: the session ends
	ehloStartTLS = "starttls" // TLS is offered, or required, and not on yet: STARTTLS
	ehloAccepted = "accepted" // the session is set up
)

// afterEhlo decides the step after an EHLO reply. TLS is started whenever
// the server offers it on a connection that is not secure yet, and also
// without the offer when it is required.
func afterEhlo(reply string, secure, requireTLS bool) string {
	switch {
	case strings.HasPrefix(reply, "421"):
		return ehloClosing
	case !strings.HasPrefix(reply, "2") && requireTLS && !secure:
		return ehloNoTLS
	case !strings.HasPrefix(reply, "2"):
		return ehloRefused
	case !secure && (requireTLS || hasKeyword(reply, "STARTTLS")):
		return ehloStartTLS
	}
	return ehloAccepted
}

func (c *client) onEHLO(reply string) {
	switch afterEhlo(reply, c.secure, c.opts.RequireTLS && c.useCreds) {
	case ehloClosing:
		c.onError(newError("Server terminates connection. response="+reply, "ECONNECTION", reply, "EHLO"))
	case ehloNoTLS:
		c.onError(newError("EHLO failed but HELO does not support required STARTTLS. response="+reply, "ECONNECTION", reply, "EHLO"))
	case ehloRefused:
		c.expect(c.onHELO)
		c.sendCommand("HELO " + c.name)
	case ehloStartTLS:
		// what the server announced before the upgrade is not used
		c.sendCommand("STARTTLS")
		c.expect(c.onSTARTTLS)
	default:
		c.ehlo = parseEhlo(reply)
		c.ready()
	}
}

func (c *client) onHELO(reply string) {
	if !strings.HasPrefix(reply, "2") {
		c.onError(newError("Invalid HELO. response="+reply, "EPROTOCOL", reply, "HELO"))
		return
	}
	// a HELO reply names no extensions; a login is tried anyway
	c.ehlo.allowsAuth = true
	c.ready()
}

func (c *client) onSTARTTLS(reply string) {
	if !strings.HasPrefix(reply, "2") {
		c.onError(newError("Error upgrading connection with STARTTLS", "ETLS", reply, "STARTTLS"))
		return
	}
	// Whatever arrived in plain text after the reply is dropped: it did not
	// come over the protected connection.
	c.replies.reset()
	c.startPhase(c.remainingSetupTime(), "TLS handshake timed out")
	deadline := time.Now().Add(c.socketTimeout)
	if c.phaseDeadline.Before(deadline) {
		deadline = c.phaseDeadline
	}
	secure := tls.Client(c.plain, c.tlsConfig())
	_ = c.plain.SetDeadline(deadline)
	err := secure.HandshakeContext(c.ctx)
	_ = c.plain.SetDeadline(time.Time{})
	switch {
	case err == nil:
	case c.isCancelled() || c.ctx.Err() != nil:
		c.abort()
		return
	case isTimeout(err):
		if time.Now().Before(c.phaseDeadline) {
			c.onError(newError("Timeout", "ETIMEDOUT", "", "CONN"))
		} else {
			c.onError(newError(c.phaseText, "ETIMEDOUT", "", "CONN"))
		}
		return
	default:
		c.onError(handshakeError(err))
		return
	}
	c.clearPhase()
	c.secure = true
	c.sock = secure
	c.lastActivity.Store(time.Now().UnixNano())
	c.expect(c.onEHLO)
	c.sendCommand("EHLO " + c.name)
}

// ready runs when the session is set up: it logs in when there are
// credentials and the server allows a login, then goes on with the call.
func (c *client) ready() {
	if !c.useCreds || !c.ehlo.allowsAuth {
		c.afterLogin()
		return
	}
	c.login(func(err *Error) {
		if err != nil {
			c.finish(nil, err)
			return
		}
		c.afterLogin()
	})
}

// quit ends a Verify call: QUIT is written and the call succeeds without
// waiting for the reply.
func (c *client) quit() {
	if c.returned {
		return
	}
	c.returned = true
	c.sendCommand("QUIT")
	c.expect(func(string) { c.close() })
}

// ---- login

// login uses the first mechanism of PLAIN, LOGIN and CRAM-MD5 the server
// offers, and PLAIN when it offers none of them.
func (c *client) login(done func(*Error)) {
	method := "PLAIN"
	if len(c.ehlo.mechanisms) > 0 {
		method = c.ehlo.mechanisms[0]
	}
	user, pass := c.opts.User, c.opts.Pass
	switch method {
	case "LOGIN":
		c.expect(func(reply string) {
			if !isContinueReply(reply) {
				done(newError(`Invalid login sequence while waiting for "334 VXNlcm5hbWU6"`, "EAUTH", reply, "AUTH LOGIN"))
				return
			}
			c.expect(func(reply string) {
				if !isContinueReply(reply) {
					done(newError(`Invalid login sequence while waiting for "334 UGFzc3dvcmQ6"`, "EAUTH", reply, "AUTH LOGIN"))
					return
				}
				c.expect(func(reply string) { c.onAuthResult(reply, method, done) })
				c.sendCommand(encodeBase64(pass))
			})
			c.sendCommand(encodeBase64(user))
		})
		c.sendCommand("AUTH LOGIN")
	case "CRAM-MD5":
		c.expect(func(reply string) {
			challenge, ok := cramChallenge(reply)
			if !ok {
				done(newError("Invalid login sequence while waiting for server challenge string", "EAUTH", reply, "AUTH CRAM-MD5"))
				return
			}
			c.expect(func(reply string) {
				if !isCramSuccess(reply) {
					done(newError(`Invalid login sequence while waiting for "235"`, "EAUTH", reply, "AUTH CRAM-MD5"))
					return
				}
				done(nil)
			})
			c.sendCommand(cramResponse(user, pass, challenge))
		})
		c.sendCommand("AUTH CRAM-MD5")
	default:
		c.expect(func(reply string) { c.onAuthResult(reply, method, done) })
		c.sendCommand(plainAuthCommand(user, pass))
	}
}

// onAuthResult handles the reply that decides a PLAIN or LOGIN login. A
// server that asks for more (334) gets an empty line.
func (c *client) onAuthResult(reply, method string, done func(*Error)) {
	if strings.HasPrefix(reply, "334") {
		c.expect(func(reply string) { c.onAuthResult(reply, method, done) })
		c.sendCommand("")
		return
	}
	if !strings.HasPrefix(reply, "2") {
		done(newError("Invalid login", "EAUTH", reply, "AUTH "+method))
		return
	}
	done(nil)
}

// isContinueReply reports whether a reply is a 334 with a separator.
func isContinueReply(reply string) bool {
	return len(reply) >= 4 && reply[:3] == "334" && (reply[3] == ' ' || reply[3] == '-')
}

// isCramSuccess reports whether a reply is 235 followed by white space.
func isCramSuccess(reply string) bool {
	if !strings.HasPrefix(reply, "235") {
		return false
	}
	for _, r := range reply[3:] {
		return isJSSpace(r)
	}
	return false
}

// ---- sending a message

func (c *client) sendMessage() {
	if c.ehlo.maxSize != 0 && float64(c.envelope.Size) > c.ehlo.maxSize {
		c.finish(nil, newError("Message size larger than allowed "+jsNumber(c.ehlo.maxSize), "EMESSAGE", "", "MAIL FROM"))
		return
	}
	c.sending = true
	started := time.Now()
	c.sendEnvelope(func(err *Error) {
		if err != nil {
			c.finish(nil, err)
			return
		}
		accepted := time.Now()
		c.body = startBody(c, c.message)
		c.expect(func(reply string) {
			if !strings.HasPrefix(reply, "2") {
				c.finish(nil, newError("Message failed", "EMESSAGE", reply, "DATA"))
				return
			}
			info := &Info{
				Accepted:        c.env.accepted,
				Rejected:        c.env.rejected,
				RejectedErrors:  c.env.rejectedErrors,
				Response:        reply,
				EnvelopeFrom:    c.env.from,
				EnvelopeTo:      c.env.to,
				Ehlo:            c.ehlo.lines,
				MessageSize:     c.body.size.Load(),
				EnvelopeTime:    accepted.Sub(started),
				MessageTime:     time.Since(accepted),
				ServerMessageID: serverMessageID(reply, c.opts.Host),
			}
			c.finish(info, nil)
		})
	})
}

// sendEnvelope sends MAIL FROM, RCPT TO for every recipient and DATA. done
// runs with nil when the server is ready for the message. With PIPELINING
// all of it is written at once and the replies are read afterwards;
// without, every command waits for the reply to the one before.
func (c *client) sendEnvelope(done func(*Error)) {
	env, err := newEnvelopeState(c.envelope)
	if err != nil {
		done(err)
		return
	}
	c.env = env
	env.pipelined = c.ehlo.pipelining
	command, smtpUTF8 := mailCommand(env, c.envelope, &c.ehlo)
	if smtpUTF8 {
		c.smtpUTF8 = true
	}
	c.expect(func(reply string) { c.onMAIL(reply, done) })
	if !env.pipelined {
		c.sendCommand(command)
		return
	}
	batch := []byte(command + "\r\n")
	for len(env.unsent) > 0 {
		batch = append(batch, c.nextRcpt(done)...)
	}
	c.expect(func(reply string) { c.onDATA(reply, done) })
	batch = append(batch, "DATA\r\n"...)
	c.write(batch)
}

// nextRcpt takes the next recipient and returns its RCPT TO line.
func (c *client) nextRcpt(done func(*Error)) string {
	recipient := c.env.unsent[0]
	c.env.unsent = c.env.unsent[1:]
	c.env.awaiting = append(c.env.awaiting, recipient)
	c.expect(func(reply string) { c.onRCPT(reply, done) })
	return "RCPT TO:<" + recipient + ">\r\n"
}

func (c *client) onMAIL(reply string, done func(*Error)) {
	if !strings.HasPrefix(reply, "2") {
		text := "Mail command failed"
		if c.smtpUTF8 && strings.HasPrefix(reply, "550 ") && hasNonASCII(c.env.from) {
			text = "Internationalized mailbox name not allowed"
		}
		c.env.mailError = newError(text, "EENVELOPE", reply, "MAIL FROM")
	}
	c.advanceEnvelope(reply, done)
}

func (c *client) onRCPT(reply string, done func(*Error)) {
	env := c.env
	recipient := ""
	if len(env.awaiting) > 0 {
		recipient = env.awaiting[0]
		env.awaiting = env.awaiting[1:]
	}
	if strings.HasPrefix(reply, "2") {
		env.accepted = append(env.accepted, recipient)
	} else {
		// a refused recipient does not stop the message while another one
		// is accepted
		text := "Recipient command failed"
		if c.smtpUTF8 && strings.HasPrefix(reply, "553 ") && hasNonASCII(recipient) {
			text = "Internationalized mailbox name not allowed"
		}
		err := newError(text, "EENVELOPE", reply, "RCPT TO")
		err.Recipient = recipient
		env.rejected = append(env.rejected, recipient)
		env.rejectedErrors = append(env.rejectedErrors, err)
	}
	c.advanceEnvelope(reply, done)
}

// advanceEnvelope moves on after a reply to MAIL FROM or RCPT TO when the
// commands go one at a time: the next recipient, or once every reply is in,
// DATA or the error that ends the message. A pipelined envelope is decided
// by the reply to DATA.
func (c *client) advanceEnvelope(reply string, done func(*Error)) {
	env := c.env
	if env.pipelined {
		return
	}
	if env.mailError != nil {
		done(env.mailError)
		return
	}
	if len(env.unsent) > 0 {
		c.write([]byte(c.nextRcpt(done)))
		return
	}
	if len(env.awaiting) > 0 {
		return
	}
	if err := env.failure(reply); err != nil {
		done(err)
		return
	}
	c.expect(func(reply string) { c.onDATA(reply, done) })
	c.sendCommand("DATA")
}

func (c *client) onDATA(reply string, done func(*Error)) {
	env := c.env
	if env.pipelined {
		if err := env.failure(reply); err != nil {
			if !strings.HasPrefix(reply, "3") {
				done(err)
				return
			}
			if env.mailError != nil {
				// The server refused the sender and took DATA anyway. There
				// is no transaction to end: the call fails and its
				// connection is closed without another command.
				done(err)
				return
			}
			// The server took DATA without a recipient. The empty message
			// is ended, it has nobody to go to.
			c.expect(func(string) { done(err) })
			c.sendCommand(".")
			return
		}
	}
	// 354 is the answer to expect, but a server that says 250 means the same
	if !strings.HasPrefix(reply, "2") && !strings.HasPrefix(reply, "3") {
		done(newError("Data command failed", "EENVELOPE", reply, "DATA"))
		return
	}
	done(nil)
}

// firstPiece is how much of a message is written before the next reply is
// looked at, and firstPieceWait how long the session waits for that. A
// message up to this size is on its way in full when a server answers it
// early, as it is with nodemailer, whose message stream has that much ready
// when DATA is accepted.
const (
	firstPiece     = 16 * 1024
	firstPieceWait = time.Second
)

// bodyWriter writes the message after DATA while the session keeps reading,
// so that a reply the server sends before it has everything is seen.
type bodyWriter struct {
	// size counts the bytes handed to the connection so far, the end line
	// included
	size atomic.Int64
	// stopped is set when the connection is closed under the writer
	stopped atomic.Bool
	// first is closed when the first piece is written, done when the writer
	// is finished
	first chan struct{}
	done  chan struct{}
}

// abort tells the writer that its connection is going away. It reports
// whether the writer was still at work.
func (b *bodyWriter) abort() bool {
	b.stopped.Store(true)
	select {
	case <-b.done:
		return false
	default:
		return true
	}
}

func startBody(c *client, message []byte) *bodyWriter {
	body := &bodyWriter{first: make(chan struct{}), done: make(chan struct{})}
	sock := c.sock
	// no deadline of its own: the session notices a connection that takes
	// nothing for the socket timeout and closes it
	_ = sock.SetWriteDeadline(time.Time{})
	go func() {
		defer close(body.done)
		stream := newDataStream()
		rest := message
		for piece, started := firstPiece, false; ; piece = 64 * 1024 {
			n := min(len(rest), piece)
			out := stream.transform(nil, rest[:n])
			rest = rest[n:]
			if len(rest) == 0 {
				out = stream.finish(out)
			}
			body.size.Store(stream.out)
			_, err := sock.Write(out)
			if !started {
				started = true
				close(body.first)
			}
			if err != nil {
				if !body.stopped.Load() {
					c.mu.Lock()
					c.bodyErr = err
					_ = sock.SetReadDeadline(time.Now())
					c.mu.Unlock()
				}
				return
			}
			c.lastActivity.Store(time.Now().UnixNano())
			if len(rest) == 0 || body.stopped.Load() {
				return
			}
		}
	}()
	wait := time.NewTimer(firstPieceWait)
	defer wait.Stop()
	select {
	case <-body.first:
	case <-wait.C:
	}
	return body
}
