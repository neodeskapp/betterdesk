package attestation

import (
	"context"
	"crypto/tls"
	"errors"
	"net/http"
	"time"

	"github.com/coder/websocket"
	"google.golang.org/protobuf/proto"

	pb "github.com/neodeskapp/neodesk/neodesk-server/proto"
)

// Dialer opens the run's websocket connections.
type Dialer interface {
	// Dial opens a websocket to url and gives up after timeout
	// (connectWs). ctx ends the attempt too.
	Dial(ctx context.Context, url string, timeout time.Duration) (Conn, error)
}

// Conn is one open websocket.
type Conn interface {
	// Send sends one binary message.
	Send(payload []byte) error
	// AwaitMessage waits at most timeout for the first message from the
	// other side. A failure of the connection or the timeout is an error.
	AwaitMessage(ctx context.Context, timeout time.Duration) error
	// Drain reads and drops what the other side sends until the
	// connection closes, so it stays healthy while the run holds it.
	Drain()
	// Close starts to close the connection and returns at once.
	Close()
}

// healthCheck is the signal server's health check: a RendezvousMessage
// with hc {token: "bd-bench"}, without a length prefix.
func healthCheck() []byte {
	return marshal(&pb.RendezvousMessage{Union: &pb.RendezvousMessage_Hc{Hc: &pb.HealthCheck{Token: "bd-bench"}}})
}

// requestRelay asks the relay to pair the connections that send the same
// uuid: a RendezvousMessage with request_relay {id: "bd-bench", uuid}.
func requestRelay(uuid string) []byte {
	return marshal(&pb.RendezvousMessage{Union: &pb.RendezvousMessage_RequestRelay{RequestRelay: &pb.RequestRelay{Id: "bd-bench", Uuid: uuid}}})
}

func marshal(m *pb.RendezvousMessage) []byte {
	b, err := proto.Marshal(m)
	if err != nil {
		// a message of fixed strings always encodes
		panic(err)
	}
	return b
}

// WebSocketDialer dials with package websocket. AllowSelfSigned accepts
// any certificate on wss.
type WebSocketDialer struct {
	AllowSelfSigned bool
}

// Dial opens the connection. The handshake has timeout to finish.
func (d WebSocketDialer) Dial(ctx context.Context, url string, timeout time.Duration) (Conn, error) {
	dialCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: d.AllowSelfSigned} //nolint:gosec // ALLOW_SELF_SIGNED_CERTS asks for it
	c, res, err := websocket.Dial(dialCtx, url, &websocket.DialOptions{HTTPClient: &http.Client{Transport: transport}})
	if res != nil && res.Body != nil {
		_ = res.Body.Close()
	}
	if err != nil {
		if dialCtx.Err() != nil && ctx.Err() == nil {
			return nil, errors.New("connect timeout")
		}
		return nil, err
	}
	c.SetReadLimit(1 << 20)
	return &wsConn{c: c, ctx: ctx}, nil
}

type wsConn struct {
	c *websocket.Conn
	// ctx is the run's: its end ends the reads of Drain
	ctx context.Context
}

func (w *wsConn) Send(payload []byte) error {
	ctx, cancel := context.WithTimeout(w.ctx, connectTimeout)
	defer cancel()
	return w.c.Write(ctx, websocket.MessageBinary, payload)
}

func (w *wsConn) AwaitMessage(ctx context.Context, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	_, _, err := w.c.Read(ctx)
	return err
}

func (w *wsConn) Drain() {
	go func() {
		for {
			if _, _, err := w.c.Read(w.ctx); err != nil {
				return
			}
		}
	}()
}

func (w *wsConn) Close() {
	go func() { _ = w.c.Close(websocket.StatusNormalClosure, "") }()
}
