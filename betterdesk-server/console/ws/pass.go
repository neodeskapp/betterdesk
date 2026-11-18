package ws

import (
	"context"
	"sync"
)

// CloseNoStatus stands for a close frame without a status code (1005): the
// old proxies closed sockets that way, and Close sends it as an empty
// close frame.
const CloseNoStatus = 1005

// passQueue is how many messages of the socket may wait for the upstream;
// a socket that sends more is not read until there is room.
const passQueue = 1024

// PassOptions say how Pass carries messages between an accepted socket
// and the upstream socket it opens, the way the old console's proxies to
// the API server did.
type PassOptions struct {
	// Dial opens the upstream. Pass calls it once, right after the socket
	// was accepted; ctx ends when the socket does.
	Dial func(ctx context.Context) (*Upstream, error)
	// Queue keeps the messages that arrive while Dial runs and sends them,
	// in order, once the upstream is open (the CDAP proxies). Without it
	// they are dropped, as when nothing listened for them yet (the
	// MeshCentral proxy).
	Queue bool
	// Binary sends every message as binary, both ways (the CDAP terminal,
	// whose old proxy dropped the kind of each message).
	Binary bool
	// FailText is sent to the socket as text when the upstream fails (the
	// dial, a message over the limit, text that is not UTF-8, a protocol
	// error), before the socket is closed. "" sends nothing.
	FailText string
	// OnFail is told why the upstream failed, for the log.
	OnFail func(err error)
}

// Pass carries messages between conn and an upstream until both have
// ended, and returns then. Messages keep their kind (text or binary)
// unless Binary is set. When the upstream ends, conn is closed without a
// status code; when conn ends, the upstream is closed the same way (or
// its dial given up). Close codes are not passed on.
func Pass(conn *Conn, opts PassOptions) {
	p := &passing{conn: conn, opts: opts}
	p.cond = sync.NewCond(&p.mu)
	ctx, cancel := context.WithCancel(conn.Context())
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		p.connect(ctx)
	}()
	for {
		binary, data, err := conn.Read()
		if err != nil {
			break
		}
		p.push(passMessage{binary: binary || opts.Binary, data: data})
	}
	// The socket has ended: the upstream goes too, or its dial is given up.
	p.mu.Lock()
	p.socketGone = true
	up := p.up
	p.cond.Broadcast()
	p.mu.Unlock()
	if up != nil {
		up.Close()
	} else {
		cancel()
	}
	wg.Wait()
}

type passMessage struct {
	binary bool
	data   []byte
}

type passing struct {
	conn *Conn
	opts PassOptions

	mu   sync.Mutex
	cond *sync.Cond
	// up is set once the upstream is open
	up *Upstream
	// queue holds the socket's messages for the upstream
	queue      []passMessage
	upGone     bool
	socketGone bool
}

// push hands a message of the socket to the upstream: queued while the
// upstream is open, and while the dial runs when Queue is set; dropped
// otherwise.
func (p *passing) push(m passMessage) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for len(p.queue) >= passQueue && !p.upGone && !p.socketGone {
		p.cond.Wait()
	}
	switch {
	case p.upGone:
	case p.up != nil:
		p.queue = append(p.queue, m)
		p.cond.Broadcast()
	case p.opts.Queue:
		p.queue = append(p.queue, m)
	}
}

// connect dials the upstream, then sends the queue to it and its messages
// to the socket until either ends.
func (p *passing) connect(ctx context.Context) {
	up, err := p.opts.Dial(ctx)
	p.mu.Lock()
	if err != nil {
		p.gone()
		p.mu.Unlock()
		p.fail(err)
		return
	}
	if p.socketGone {
		p.gone()
		p.mu.Unlock()
		up.Close()
		up.Wait()
		return
	}
	p.up = up
	p.cond.Broadcast()
	p.mu.Unlock()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		p.write(up)
	}()
	for {
		binary, data, err := up.Read()
		if err != nil {
			p.mu.Lock()
			p.gone()
			p.mu.Unlock()
			if ProtocolError(err) {
				p.fail(err)
			} else {
				p.conn.Close(CloseNoStatus, "")
			}
			break
		}
		p.conn.Send(binary || p.opts.Binary, data)
	}
	up.Wait()
	wg.Wait()
}

// gone marks the upstream as ended and drops what waits for it.
func (p *passing) gone() {
	p.upGone = true
	p.queue = nil
	p.cond.Broadcast()
}

// write sends the queued messages to the upstream in order.
func (p *passing) write(up *Upstream) {
	for {
		p.mu.Lock()
		for len(p.queue) == 0 && !p.upGone {
			p.cond.Wait()
		}
		if p.upGone {
			p.mu.Unlock()
			return
		}
		m := p.queue[0]
		p.queue = p.queue[1:]
		p.cond.Broadcast()
		p.mu.Unlock()
		if err := up.Send(m.binary, m.data); err != nil {
			return
		}
	}
}

// fail tells the socket that the upstream failed and closes it.
func (p *passing) fail(err error) {
	if p.opts.OnFail != nil {
		p.opts.OnFail(err)
	}
	if p.opts.FailText != "" && p.conn.Open() {
		p.conn.SendText(p.opts.FailText)
	}
	p.conn.Close(CloseNoStatus, "")
}
