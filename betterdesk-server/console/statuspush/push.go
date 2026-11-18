// Package statuspush pushes what happens on the server to the browsers:
// the status of devices on /ws/device-status and registrations and help
// requests on /ws/panel-events (services/deviceStatusPush.js of the old
// console). It reads the API server's event bus in this process.
//
// The messages are the bytes the old console sent: the event as the API
// server's event socket writes it, read as JSON.parse read it and written
// as JSON.stringify writes it, with the same keys in the same order.
// Nothing is filtered by role or device scope and nothing is throttled:
// every open socket gets every message of its kind.
package statuspush

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/ws"
	"github.com/unitronix/betterdesk-server/logging"
)

// PingInterval is how often an open socket is pinged.
const PingInterval = 30 * time.Second

// Options configure the service.
type Options struct {
	// Events subscribes to the API server's event bus
	// (goapi.Client.Events). nil leaves the push off: the sockets take
	// browsers and never send anything, as when the old console had no
	// API address or key.
	Events func(ctx context.Context, filter string) (<-chan *goapi.Object, error)
	// Cascade carries a changed device id into the console's tables
	// (devicegroups.Service.CascadePeerIDChange); it logs its own errors.
	Cascade func(ctx context.Context, oldID, newID string)
	// Now stands in for Date.now() where an event has no timestamp.
	// Default time.Now.
	Now func() time.Time
	// PingInterval overrides PingInterval (tests).
	PingInterval time.Duration
}

// Service holds the open sockets.
type Service struct {
	opts Options

	mu     sync.Mutex
	status map[*ws.Conn]struct{}
	panel  map[*ws.Conn]struct{}
}

// New returns the service. Register adds its sockets; Run reads the
// event bus.
func New(opts Options) *Service {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.PingInterval <= 0 {
		opts.PingInterval = PingInterval
	}
	return &Service{opts: opts, status: map[*ws.Conn]struct{}{}, panel: map[*ws.Conn]struct{}{}}
}

// Register adds /ws/device-status and /ws/panel-events.
//
// Both take only a signed-in session ("HTTP/1.1 401 Unauthorized"
// otherwise). The panel socket also needs enrollment.approve ("403
// Forbidden"; "503 Service Unavailable" when the permission lookup
// fails). Neither checks the Origin, unlike every other socket of the
// console. Messages the browser sends are read and dropped.
func (s *Service) Register(reg *ws.Registry) {
	reg.Handle("/ws/device-status", func(u *ws.Upgrade) { s.serve(u, s.status, nil) })
	reg.Handle("/ws/panel-events", func(u *ws.Upgrade) {
		s.serve(u, s.panel, func(session ws.Session) (bool, error) {
			return u.RoleHasPermission(session.UserRole(), "enrollment.approve")
		})
	})
}

// serve runs the checks of one socket and keeps it in clients while it
// is open.
func (s *Service) serve(u *ws.Upgrade, clients map[*ws.Conn]struct{}, authorize func(ws.Session) (bool, error)) {
	session := u.Session()
	if !session.SignedIn() {
		u.Refuse(http.StatusUnauthorized)
		return
	}
	if authorize != nil {
		allowed, err := authorize(session)
		if err != nil {
			u.Refuse(http.StatusServiceUnavailable)
			return
		}
		if !allowed {
			u.Refuse(http.StatusForbidden)
			return
		}
	}
	conn, ok := u.Accept(ws.AcceptOptions{})
	if !ok {
		return
	}
	defer conn.Wait()
	s.mu.Lock()
	clients[conn] = struct{}{}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(clients, conn)
		s.mu.Unlock()
	}()
	conn.KeepAlive(s.opts.PingInterval)
	_ = conn.Discard()
}

// Clients counts the open sockets: device status and panel events.
func (s *Service) Clients() (status, panel int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.status), len(s.panel)
}

// broadcast sends text to every socket of a kind; SendText skips one that
// is closing, as the old service skipped a socket that was not open.
func (s *Service) broadcast(clients map[*ws.Conn]struct{}, text string) {
	s.mu.Lock()
	open := make([]*ws.Conn, 0, len(clients))
	for c := range clients {
		open = append(open, c)
	}
	s.mu.Unlock()
	for _, c := range open {
		c.SendText(text)
	}
}

// PublishPanelText sends a message that is already JSON text to every
// panel socket, as publishPanelEvent did for events the console made
// itself (a LAN registration request, its approval).
func (s *Service) PublishPanelText(text string) {
	s.broadcast(s.panel, text)
}

// PublishPanelEvent sends v, written as JSON.stringify writes it, to
// every panel socket.
func (s *Service) PublishPanelEvent(v any) error {
	text, err := stringify(v)
	if err != nil {
		return err
	}
	s.PublishPanelText(text)
	return nil
}

// Run reads the event bus until ctx ends and pushes each event.
func (s *Service) Run(ctx context.Context) {
	if s.opts.Events == nil {
		logging.Logf("console", "WARN: [DeviceStatus] Go API URL or API key not configured, device status push disabled")
		return
	}
	frames, err := s.opts.Events(ctx, "")
	if err != nil {
		logging.Logf("console", "WARN: [DeviceStatus] device status push disabled: %v", err)
		return
	}
	for frame := range frames {
		s.Handle(ctx, frame)
	}
}

// Handle pushes one event of the bus: the frame the API server's event
// socket would have sent, parsed.
func (s *Service) Handle(ctx context.Context, frame any) {
	out, ok := transform(frame, s.opts.Now)
	if !ok {
		return
	}
	if out.cascade != nil && s.opts.Cascade != nil {
		s.opts.Cascade(ctx, out.cascade[0], out.cascade[1])
	}
	text, err := stringify(out.message)
	if err != nil {
		return
	}
	if out.panel {
		s.broadcast(s.panel, text)
	} else {
		s.broadcast(s.status, text)
	}
}
