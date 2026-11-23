// Package remoterelay is the remote desktop relay between the NeoDesk
// agent and the viewer in the browser (services/remoteRelay.js of the old
// console): the agent's socket /ws/remote-agent/<id> sends screen frames,
// the viewers' sockets /ws/remote-viewer/<id> get them and send input.
// It also keeps the single-use tokens POST /api/nd/remote-agent-token
// gives out for the agent's socket. Sessions and tokens live in this
// process only, as they did in Node. A session goes once it has neither
// an agent nor a viewer, a token when its time is up and another is
// issued.
//
// Where a message made the Node process end (a text message that is the
// JSON null, a message the relay writes out again that is nested too
// deeply for JSON.stringify), the relay drops that one connection instead
// (DECISIONS A19); an agent path whose escapes spell no text is refused
// with 400.
package remoterelay

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
	"github.com/neodeskapp/neodesk/neodesk-server/console/session"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/ws"
	"github.com/neodeskapp/neodesk/neodesk-server/logging"
)

// The limits of the old relay.
const (
	// MaxBinaryFrame is the largest screen frame passed to the viewers; a
	// bigger one is dropped and the socket stays open.
	MaxBinaryFrame = 2 << 20
	// MaxViewers is how many viewers one device may have.
	MaxViewers = 5
	// PingInterval is how often both kinds of socket are pinged.
	PingInterval = 20 * time.Second
	// AgentIdleTTL closes an agent that has had no viewer for this long.
	AgentIdleTTL = 90 * time.Second
	// AgentTokenTTL is how long a single-use agent token is good for.
	AgentTokenTTL = 60 * time.Second
	// SessionCheck is how often a viewer's session, permission and device
	// scope are looked at again while its socket is open.
	SessionCheck = 10 * time.Second
	// MaxViewerMessage is the largest message the viewer socket takes; a
	// bigger one closes it with 1009. A viewer's input is a few hundred
	// bytes.
	MaxViewerMessage = 64 << 10
	// MaxAgentText is the largest text message of an agent the relay
	// reads; a bigger one is dropped unread and the socket stays open.
	// Screen frames are binary and keep MaxBinaryFrame.
	MaxAgentText = 1 << 20
)

// Clock is the time the relay sees: the old code's Date.now and
// setTimeout.
type Clock interface {
	Now() time.Time
	// AfterFunc calls f once after d in its own goroutine, unless the
	// timer is stopped first.
	AfterFunc(d time.Duration, f func()) Timer
}

// Timer is a pending AfterFunc.
type Timer interface {
	// Stop prevents the call; it reports whether it did.
	Stop() bool
}

type realClock struct{}

func (realClock) Now() time.Time                            { return time.Now() }
func (realClock) AfterFunc(d time.Duration, f func()) Timer { return time.AfterFunc(d, f) }

// Options configure the service.
type Options struct {
	// VerifyDevice checks a device token the agent presents instead of a
	// single-use token (verifyDeviceWsAuth; authn.Service.VerifyDeviceWSAuth).
	// An error answers the upgrade with 500.
	VerifyDevice func(ctx context.Context, deviceID, token string) (bool, error)
	// DB is the console's database, where Device looks devices up
	// (db.getPeerById).
	DB *store.DB
	// Devices looks a device up for the viewer's scope check; nil uses
	// Device.
	Devices func(ctx context.Context, id string) (*goapi.Object, error)
	// CanAccess is the account's device scope
	// (deviceGroupService.userCanAccessDevice;
	// devicegroups.Service.UserCanAccessDevice).
	CanAccess func(ctx context.Context, user *session.User, device *goapi.Object) (bool, error)
	// FeaturesOff returns the client features the account may not use
	// (access.Service.ClientFeaturesOff). A viewer whose account may not
	// use remote_control watches without input. nil turns no feature off.
	FeaturesOff func(ctx context.Context, userID int64) ([]string, error)
	// Clock is real time when nil.
	Clock Clock
	// Random is crypto/rand when nil: the agent tokens.
	Random io.Reader
	// PingInterval overrides PingInterval (tests).
	PingInterval time.Duration
	// SessionCheck overrides SessionCheck (tests).
	SessionCheck time.Duration
}

// Service holds the remote sessions and the agents' pending tokens.
type Service struct {
	opts  Options
	clock Clock

	mu       sync.Mutex
	sessions map[string]*remoteSession
	// order lists the device ids of sessions in the order they were made.
	order  []string
	tokens map[string]agentToken
}

// remoteSession is one device's session: its agent, its viewers in the
// order they joined, whether the agent streams, the viewers that stopped
// the stream for themselves while it runs, and the timer that closes an
// agent without viewers.
type remoteSession struct {
	agent     *ws.Conn
	viewers   []*ws.Conn
	streaming bool
	paused    map[*ws.Conn]bool
	idle      Timer
}

type agentToken struct {
	token     string
	expiresAt int64
}

// New returns the service. Register adds its sockets.
func New(opts Options) *Service {
	if opts.Clock == nil {
		opts.Clock = realClock{}
	}
	if opts.Random == nil {
		opts.Random = rand.Reader
	}
	if opts.PingInterval <= 0 {
		opts.PingInterval = PingInterval
	}
	if opts.SessionCheck <= 0 {
		opts.SessionCheck = SessionCheck
	}
	s := &Service{opts: opts, clock: opts.Clock, sessions: map[string]*remoteSession{}, tokens: map[string]agentToken{}}
	if s.opts.Devices == nil {
		s.opts.Devices = s.Device
	}
	return s
}

func (s *Service) nowMillis() int64 { return s.clock.Now().UnixMilli() }

// IssueRemoteAgentToken makes the single-use token of a device's agent
// socket, good for a minute, and replaces the device's earlier one
// (issueRemoteAgentToken): 32 random bytes as hex, and the lifetime in
// seconds. The tokens whose time is up go at the same time.
func (s *Service) IssueRemoteAgentToken(deviceID string) (token string, expiresIn int) {
	b := make([]byte, 32)
	if _, err := io.ReadFull(s.opts.Random, b); err != nil {
		panic(fmt.Sprintf("remoterelay: random source failed: %v", err))
	}
	token = hex.EncodeToString(b)
	s.mu.Lock()
	now := s.nowMillis()
	for id, entry := range s.tokens {
		if now > entry.expiresAt {
			delete(s.tokens, id)
		}
	}
	s.tokens[deviceID] = agentToken{token: token, expiresAt: now + AgentTokenTTL.Milliseconds()}
	s.mu.Unlock()
	return token, int(AgentTokenTTL / time.Second)
}

// consumeToken is consumeRemoteAgentToken: the device's pending token,
// once. An expired one is dropped when it is tried; a wrong one leaves
// the pending token as it is.
func (s *Service) consumeToken(deviceID, token string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.tokens[deviceID]
	if !ok || token == "" {
		return false
	}
	if s.nowMillis() > entry.expiresAt {
		delete(s.tokens, deviceID)
		return false
	}
	if len(entry.token) != len(token) || subtle.ConstantTimeCompare([]byte(entry.token), []byte(token)) != 1 {
		return false
	}
	delete(s.tokens, deviceID)
	return true
}

// SessionState is a remote session as the admin API shows it
// (getSessionState).
type SessionState struct {
	AgentConnected bool `json:"agentConnected"`
	ViewerCount    int  `json:"viewerCount"`
	Streaming      bool `json:"streaming"`
}

// SessionState returns the session of a device, nil when there is none.
func (s *Service) SessionState(deviceID string) *SessionState {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[deviceID]
	if !ok {
		return nil
	}
	return &SessionState{
		AgentConnected: sess.agent != nil && sess.agent.Open(),
		ViewerCount:    len(sess.viewers),
		Streaming:      sess.streaming,
	}
}

// ActiveSessions lists the device ids that have a session, in the order
// the sessions were made (getActiveSessions).
func (s *Service) ActiveSessions() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string{}, s.order...)
}

// sessionLocked is getSession: the device's session, made when it has
// none.
func (s *Service) sessionLocked(deviceID string) *remoteSession {
	sess, ok := s.sessions[deviceID]
	if !ok {
		sess = &remoteSession{}
		s.sessions[deviceID] = sess
		s.order = append(s.order, deviceID)
	}
	return sess
}

// dropIfEmptyLocked removes a session that has neither an agent nor a
// viewer, with its idle timer.
func (s *Service) dropIfEmptyLocked(sess *remoteSession, deviceID string) {
	if sess.agent != nil || len(sess.viewers) > 0 || s.sessions[deviceID] != sess {
		return
	}
	if sess.idle != nil {
		sess.idle.Stop()
		sess.idle = nil
	}
	delete(s.sessions, deviceID)
	for i, id := range s.order {
		if id == deviceID {
			s.order = append(s.order[:i:i], s.order[i+1:]...)
			break
		}
	}
}

// broadcastLocked sends a message to every viewer that is open.
func (sess *remoteSession) broadcastLocked(binary bool, data []byte) {
	for _, v := range sess.viewers {
		v.Send(binary, data)
	}
}

func (sess *remoteSession) broadcastText(text string) { sess.broadcastLocked(false, []byte(text)) }

// sendFrameLocked sends a screen frame to every viewer that has not
// stopped the stream for itself.
func (sess *remoteSession) sendFrameLocked(data []byte) {
	for _, v := range sess.viewers {
		if !sess.paused[v] {
			v.Send(true, data)
		}
	}
}

// startStreamLocked is startAgentStream: the agent is told to start and
// the viewers that it did; every viewer watches again.
func (s *Service) startStreamLocked(sess *remoteSession, deviceID string) {
	if sess.agent == nil || sess.streaming {
		return
	}
	sess.streaming = true
	sess.paused = nil
	if sess.idle != nil {
		sess.idle.Stop()
		sess.idle = nil
	}
	logf("[RemoteRelay] Session %s: sending START to agent", deviceID)
	sess.agent.SendText(`{"type":"start"}`)
	sess.broadcastText(`{"type":"stream-started"}`)
}

// stopStreamLocked is stopAgentStream: the agent is told to stop and the
// viewers that it did.
func (s *Service) stopStreamLocked(sess *remoteSession, deviceID, reason string) {
	if !sess.streaming {
		return
	}
	sess.streaming = false
	sess.paused = nil
	logf("[RemoteRelay] Session %s: sending STOP to agent (%s)", deviceID, reason)
	if sess.agent != nil {
		sess.agent.SendText(`{"type":"stop"}`)
	}
	sess.broadcastText(`{"type":"stream-stopped","reason":` + string(appendQuoted(nil, reason)) + `}`)
}

// pauseViewerLocked is a viewer's stop: that viewer is told the stream
// stopped and gets no more frames, the others keep watching. Once no
// viewer watches, the agent is told to stop.
func (s *Service) pauseViewerLocked(sess *remoteSession, deviceID string, viewer *ws.Conn) {
	if !sess.streaming || sess.paused[viewer] {
		return
	}
	if sess.paused == nil {
		sess.paused = map[*ws.Conn]bool{}
	}
	sess.paused[viewer] = true
	viewer.SendText(`{"type":"stream-stopped","reason":"operator requested stop"}`)
	s.stopIfNobodyWatchesLocked(sess, deviceID, "operator requested stop")
}

// stopIfNobodyWatchesLocked tells the agent to stop when every viewer
// stopped the stream for itself; each was told so when it stopped.
func (s *Service) stopIfNobodyWatchesLocked(sess *remoteSession, deviceID, reason string) {
	if !sess.streaming || len(sess.viewers) == 0 {
		return
	}
	for _, v := range sess.viewers {
		if !sess.paused[v] {
			return
		}
	}
	sess.streaming = false
	sess.paused = nil
	logf("[RemoteRelay] Session %s: sending STOP to agent (%s)", deviceID, reason)
	if sess.agent != nil {
		sess.agent.SendText(`{"type":"stop"}`)
	}
}

// scheduleIdleCloseLocked is scheduleIdleClose: after AgentIdleTTL the
// agent the session has then is closed, unless a viewer came.
func (s *Service) scheduleIdleCloseLocked(sess *remoteSession, deviceID string) {
	if sess.idle != nil {
		sess.idle.Stop()
	}
	sess.idle = s.clock.AfterFunc(AgentIdleTTL, func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if len(sess.viewers) == 0 && sess.agent != nil {
			logf("[RemoteRelay] Session %s: idle timeout — closing agent", deviceID)
			sess.agent.Close(ws.CloseNormal, "Idle timeout")
		}
	})
}

func logf(format string, args ...any) { logging.Logf("console", format, args...) }
