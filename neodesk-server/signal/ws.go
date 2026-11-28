package signal

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/neodeskapp/neodesk/neodesk-server/codec"
	"github.com/neodeskapp/neodesk/neodesk-server/config"
	"github.com/neodeskapp/neodesk/neodesk-server/db"
	"github.com/neodeskapp/neodesk/neodesk-server/peer"
	pb "github.com/neodeskapp/neodesk/neodesk-server/proto"
)

// Package-level keepalive timings use atomic nanoseconds so tests can override
// them without racing concurrent wsSignalKeepAlive goroutines (-race).
var wsSignalKeepAliveIntervalNs = int64(time.Duration(config.HeartbeatSuggestion) * time.Second / 2)

// Delayed empty keepalive for long-lived register sessions that idle after HTTP
// 101 before RegisterPk (RustDesk desktop ~1s). Must stay below
// typical proxy idle cuts but above ephemeral RequestRelay RTT.
var wsSignalIdleKeepAliveDelayNs = int64(800 * time.Millisecond)

func wsSignalKeepAliveInterval() time.Duration {
	return time.Duration(atomic.LoadInt64(&wsSignalKeepAliveIntervalNs))
}

func wsSignalIdleKeepAliveDelay() time.Duration {
	return time.Duration(atomic.LoadInt64(&wsSignalIdleKeepAliveDelayNs))
}

func setWSSignalKeepAliveInterval(d time.Duration) (old time.Duration) {
	old = time.Duration(atomic.SwapInt64(&wsSignalKeepAliveIntervalNs, int64(d)))
	return old
}

func setWSSignalIdleKeepAliveDelay(d time.Duration) (old time.Duration) {
	old = time.Duration(atomic.SwapInt64(&wsSignalIdleKeepAliveDelayNs, int64(d)))
	return old
}

// Read deadlines of the WebSocket signal port and the NAT test port. A
// WebSocket client must send its first frame within wsSignalFirstReadNs and
// then a frame (keepalives count; the server sends one every few seconds and
// clients answer) within wsSignalIdleReadNs. A NAT port client sends its one
// request right after connecting.
var (
	wsSignalFirstReadNs = int64(30 * time.Second)
	wsSignalIdleReadNs  = int64(2 * time.Minute)
	natReadNs           = int64(10 * time.Second)
)

func wsSignalFirstReadTimeout() time.Duration {
	return time.Duration(atomic.LoadInt64(&wsSignalFirstReadNs))
}

func wsSignalIdleReadTimeout() time.Duration {
	return time.Duration(atomic.LoadInt64(&wsSignalIdleReadNs))
}

func natReadTimeout() time.Duration {
	return time.Duration(atomic.LoadInt64(&natReadNs))
}

// serveWS starts the WebSocket signal listener (e.g., port 21118).
// RustDesk web clients connect here for the same signal protocol,
// using raw protobuf in binary WS frames (no 2-byte TCP header).
// Phase 3: Supports WSS when TLS is enabled for signal server.
func (s *Server) serveWS() {
	defer s.wg.Done()

	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleWSUpgrade)

	addr := config.ListenAddr(s.cfg.WSSignalPort())
	s.wsHTTP = &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: config.WSConnTimeout,
		BaseContext: func(l net.Listener) context.Context {
			return s.ctx
		},
	}

	if s.cfg.SignalTLSEnabled() {
		tlsCfg, err := config.LoadTLSConfig(s.cfg.TLSCertFile, s.cfg.TLSKeyFile)
		if err != nil {
			log.Printf("[signal] WSS TLS config error: %v", err)
			return
		}
		s.wsHTTP.TLSConfig = tlsCfg
		log.Printf("[signal] WSS listening on %s (TLS enabled)", addr)
		if err := s.wsHTTP.ListenAndServeTLS("", ""); err != nil && err != http.ErrServerClosed {
			log.Printf("[signal] WSS server error: %v", err)
		}
	} else {
		if len(s.cfg.GetAllowedWSOrigins()) == 0 {
			log.Printf("[signal] DEBUG: WS origin allowlist not configured: only localhost browser origins are accepted")
		}
		log.Printf("[signal] WS listening on %s", addr)
		if err := s.wsHTTP.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("[signal] WS server error: %v", err)
		}
	}
}

// handleWSUpgrade upgrades an HTTP request to a WebSocket connection
// and enters the signal message loop.
func (s *Server) handleWSUpgrade(w http.ResponseWriter, r *http.Request) {
	opts := &websocket.AcceptOptions{}

	// Secure-by-default origin validation:
	// - if WS_ALLOWED_ORIGINS is set, use the explicit allowlist
	// - otherwise, only allow browser origins from localhost/127.0.0.1
	// - non-browser/native clients without Origin are still accepted
	allowed := s.cfg.GetAllowedWSOrigins()
	if len(allowed) > 0 {
		opts.OriginPatterns = allowed
	} else if origin := r.Header.Get("Origin"); origin != "" && !isLoopbackOrigin(origin) {
		http.Error(w, "forbidden origin", http.StatusForbidden)
		return
	}

	remoteAddr := wsEffectiveRemoteAddr(r, s.cfg)
	host := hostFromAddrString(remoteAddr)
	if !s.wsConns.acquire(host) {
		log.Printf("[signal] WS connection from %s refused: too many open connections", remoteAddr)
		http.Error(w, "too many connections", http.StatusServiceUnavailable)
		return
	}
	defer s.wsConns.release(host)

	ws, err := websocket.Accept(w, r, opts)
	if err != nil {
		log.Printf("[signal] WS upgrade error: %v", err)
		return
	}

	log.Printf("[signal] WS upgrade remote=%s effective=%s path=%s origin=%q ua=%q xff=%q xri=%q",
		r.RemoteAddr, remoteAddr, r.URL.Path,
		r.Header.Get("Origin"), r.Header.Get("User-Agent"),
		r.Header.Get("X-Forwarded-For"), r.Header.Get("X-Real-IP"))

	// Increase read limit for file transfer signaling
	ws.SetReadLimit(256 * 1024)

	wsc := codec.NewWSConn(ws, s.ctx, remoteAddr)

	// Persistent connection — read messages in a loop until close or error.
	s.wsSignalLoop(wsc)
}

// wsEffectiveRemoteAddr returns the client address for WS signal registration.
// Behind a trusted proxy (TRUST_PROXY and TRUSTED_PROXIES) it is the
// forwarded entry the API's rule picks (config's ForwardedClient); the
// socket's address otherwise. Forwarded values are parsed with
// net.SplitHostPort / net.ParseIP so IP-only entries keep the proxy
// connection port (never synthesise :0) and IP:port entries are not
// double-wrapped into malformed [IP:port]:0 keys.
func wsEffectiveRemoteAddr(r *http.Request, cfg *config.Config) string {
	if r == nil {
		return ""
	}
	fwd := cfg.ForwardedClient(r)
	if fwd == "" {
		return r.RemoteAddr
	}
	return joinForwardedClientAddr(fwd, r.RemoteAddr)
}

// joinForwardedClientAddr builds a host:port session key from a forwarded
// client address and the direct RemoteAddr (used for the port when the
// forwarded value is IP-only). Non-IP hostnames and port 0 are rejected.
func joinForwardedClientAddr(fwd, remoteAddr string) string {
	if host, port, err := net.SplitHostPort(fwd); err == nil {
		if port == "" || port == "0" {
			return remoteAddr
		}
		ip := net.ParseIP(host)
		if ip == nil {
			return remoteAddr
		}
		return net.JoinHostPort(ip.String(), port)
	}
	// Bracketed IPv6 without port: "[2001:db8::1]"
	if len(fwd) >= 2 && fwd[0] == '[' && fwd[len(fwd)-1] == ']' {
		fwd = fwd[1 : len(fwd)-1]
	}
	if ip := net.ParseIP(fwd); ip != nil {
		_, port, err := net.SplitHostPort(remoteAddr)
		if err != nil || port == "" || port == "0" {
			return remoteAddr
		}
		return net.JoinHostPort(ip.String(), port)
	}
	return remoteAddr
}

func bindPeerWSConn(s *Server, peerID string, wsc *codec.WSConn) {
	if peerID == "" {
		return
	}
	s.peers.BindWS(peerID, wsc)
}

func isLoopbackOrigin(origin string) bool {
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	host := u.Hostname()
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

// wsSignalLoop reads protobuf messages from a WS connection and dispatches them.
// Unlike TCP (single request-response), WS connections stay open for streaming
// heartbeats and bi-directional signaling.
func (s *Server) wsSignalLoop(wsc *codec.WSConn) {
	defer wsc.Close()
	defer s.unregisterWSPunchConn(wsc.RemoteAddr(), wsc)

	remoteAddr := wsc.RemoteAddr()
	peerID := ""
	wsc.SetKeepAliveHandler(func() {
		s.peers.TouchWSHeartbeat(wsc)
	})
	keepAliveDone := make(chan struct{})
	registered := make(chan struct{})
	var registerOnce sync.Once
	notifyRegistered := func() {
		registerOnce.Do(func() { close(registered) })
	}
	go s.wsSignalKeepAlive(wsc, keepAliveDone, registered)
	defer close(keepAliveDone)

	wsc.SetReadTimeout(wsSignalFirstReadTimeout())
	for {
		msg, err := wsc.ReadMessage()
		if err != nil {
			// Normal close or context cancelled — not an error
			select {
			case <-s.ctx.Done():
			default:
				if websocket.CloseStatus(err) == -1 {
					since, readFrames, writeFrames, readAny, writeAny := wsc.SessionSummary()
					log.Printf("[signal] WS read from %s: %v (uptime=%s read_frames=%d write_frames=%d read_any=%v write_any=%v peer=%q)",
						remoteAddr, err, since.Round(time.Millisecond), readFrames, writeFrames, readAny, writeAny, peerID)
				}
			}
			return
		}
		wsc.SetReadTimeout(wsSignalIdleReadTimeout())

		switch {
		case msg.GetRegisterPeer() != nil:
			registerPeer := msg.GetRegisterPeer()
			resp, accepted := s.handleRegisterPeerWS(registerPeer, remoteAddr)
			if accepted {
				peerID = registerPeer.Id
				bindPeerWSConn(s, peerID, wsc)
				s.registerWSPunchConn(remoteAddr, wsc)
				notifyRegistered()
			}
			if resp != nil {
				wsc.WriteMessage(resp)
			}

		case msg.GetRegisterPk() != nil:
			registerPK := msg.GetRegisterPk()
			resp := s.processRegisterPk(registerPK, remoteAddr)
			if resp != nil {
				if registerPkAccepted(registerPK, resp) {
					peerID = registerPK.Id
					bindPeerWSConn(s, peerID, wsc)
					s.registerWSPunchConn(remoteAddr, wsc)
					notifyRegistered()
				}
				wsc.WriteMessage(resp)
			}

		case msg.GetPunchHoleRequest() != nil:
			fakeAddr, err := net.ResolveUDPAddr("udp", remoteAddr)
			if err != nil || fakeAddr == nil {
				log.Printf("[signal] WS PunchHoleRequest: invalid remote addr %q: %v", remoteAddr, err)
				continue
			}
			s.registerWSPunchConn(remoteAddr, wsc)
			resp := s.handlePunchHoleRequestTCPWithHint(msg.GetPunchHoleRequest(), fakeAddr, peer.ConnWS)
			if resp != nil {
				wsc.WriteMessage(resp)
			}

		case msg.GetTestNatRequest() != nil:
			// NAT test over WS — extract port from remote address (limited value)
			fakeAddr, err := net.ResolveTCPAddr("tcp", remoteAddr)
			if err != nil || fakeAddr == nil {
				log.Printf("[signal] WS TestNatRequest: invalid remote addr %q: %v", remoteAddr, err)
				continue
			}
			resp := s.handleTestNat(msg.GetTestNatRequest(), fakeAddr)
			if resp != nil {
				wsc.WriteMessage(resp)
			}

		case msg.GetOnlineRequest() != nil:
			resp := s.handleOnlineRequest(msg.GetOnlineRequest())
			if resp != nil {
				wsc.WriteMessage(resp)
			}

		case msg.GetRequestRelay() != nil:
			// Use the TCP handler which returns an immediate RelayResponse with
			// signed PK — the UDP handler would send the response via UDP which
			// the WebSocket client cannot receive.
			fakeAddr, err := net.ResolveUDPAddr("udp", remoteAddr)
			if err != nil || fakeAddr == nil {
				log.Printf("[signal] WS RequestRelay: invalid remote addr %q: %v", remoteAddr, err)
				continue
			}
			s.registerWSPunchConn(remoteAddr, wsc)
			resp := s.handleRequestRelayTCP(msg.GetRequestRelay(), fakeAddr, peer.ConnWS)
			if resp != nil {
				wsc.WriteMessage(resp)
			}

		case msg.GetFetchLocalAddr() != nil:
			logClientFetchLocalAddr("WS", remoteAddr)

		case msg.GetLocalAddr() != nil:
			fakeAddr, err := net.ResolveUDPAddr("udp", remoteAddr)
			if err != nil || fakeAddr == nil {
				log.Printf("[signal] WS LocalAddr: invalid remote addr %q: %v", remoteAddr, err)
				continue
			}
			s.handleLocalAddr(msg.GetLocalAddr(), fakeAddr)

		case msg.GetRelayResponse() != nil:
			s.registerWSPunchConn(remoteAddr, wsc)
			fakeAddr, err := net.ResolveUDPAddr("udp", remoteAddr)
			if err != nil || fakeAddr == nil {
				log.Printf("[signal] WS RelayResponse: invalid remote addr %q: %v", remoteAddr, err)
				continue
			}
			s.handleRelayResponseForward(msg, fakeAddr)

		case msg.GetPunchHoleSent() != nil:
			s.registerWSPunchConn(remoteAddr, wsc)
			fakeAddr, err := net.ResolveUDPAddr("udp", remoteAddr)
			if err != nil || fakeAddr == nil {
				log.Printf("[signal] WS PunchHoleSent: invalid remote addr %q: %v", remoteAddr, err)
				continue
			}
			s.handlePunchHoleSent(msg.GetPunchHoleSent(), fakeAddr, false)

		case msg.GetHc() != nil:
			resp := &pb.RendezvousMessage{
				Union: &pb.RendezvousMessage_Hc{
					Hc: &pb.HealthCheck{Token: msg.GetHc().Token},
				},
			}
			wsc.WriteMessage(resp)

		case msg.GetIceCandidate() != nil:
			// WebRTC trickle ICE between two new peers (WS transport).
			fakeAddr, _ := net.ResolveUDPAddr("udp", remoteAddr)
			s.handleIceCandidate(msg.GetIceCandidate(), fakeAddr)

		case msg.GetHttpProxyRequest() != nil:
			req := msg.GetHttpProxyRequest()
			log.Printf("[signal] WS HttpProxyRequest from %s (method=%q path=%q)", remoteAddr, req.GetMethod(), req.GetPath())
			wsc.WriteMessage(&pb.RendezvousMessage{
				Union: &pb.RendezvousMessage_HttpProxyResponse{
					HttpProxyResponse: &pb.HttpProxyResponse{
						Error: "not supported",
					},
				},
			})

		default:
			log.Printf("[signal] WS: unhandled message from %s", remoteAddr)
		}
	}
}

// wsSignalKeepAlive sends empty binary keepalive frames on long-lived register
// sessions. It must NOT send an immediate empty frame after HTTP 101: ephemeral
// WebSocket RequestRelay connections treat the first binary frame as a
// RendezvousMessage and disconnect on union:None.
//
// Keepalives start after RegisterPeer/RegisterPk, or after a short idle delay
// when the client has not sent any frame yet (desktop RegisterPk delay).
func (s *Server) wsSignalKeepAlive(wsc *codec.WSConn, done <-chan struct{}, registered <-chan struct{}) {
	interval := wsSignalKeepAliveInterval()
	if interval <= 0 {
		return
	}

	idleTimer := time.NewTimer(wsSignalIdleKeepAliveDelay())
	defer idleTimer.Stop()

	registeredOK := false
	for !registeredOK {
		select {
		case <-s.ctx.Done():
			return
		case <-done:
			return
		case <-registered:
			registeredOK = true
		case <-idleTimer.C:
			// Client already exchanged real frames (e.g. RequestRelay) — never
			// inject empty keepalive on this ephemeral session.
			if wsc.FramesRead() > 0 {
				select {
				case <-s.ctx.Done():
					return
				case <-done:
					return
				case <-registered:
					registeredOK = true
				}
				continue
			}
			// Still idle before register — one empty frame for proxies.
			if err := wsc.WriteKeepAlive(); err != nil {
				if !isNormalClose(err) {
					log.Printf("[signal] WS idle keepalive write to %s: %v", wsc.RemoteAddr(), err)
				}
				return
			}
			select {
			case <-s.ctx.Done():
				return
			case <-done:
				return
			case <-registered:
				registeredOK = true
			}
		}
	}

	// First keepalive right after registration (or continue periodic after idle).
	if err := wsc.WriteKeepAlive(); err != nil {
		if !isNormalClose(err) {
			log.Printf("[signal] WS post-register keepalive write to %s: %v", wsc.RemoteAddr(), err)
		}
		return
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-s.ctx.Done():
			return
		case <-done:
			return
		case <-ticker.C:
			if err := wsc.WriteKeepAlive(); err != nil {
				if !isNormalClose(err) {
					log.Printf("[signal] WS keepalive write to %s: %v", wsc.RemoteAddr(), err)
				}
				return
			}
		}
	}
}

// handleRegisterPeerWS processes a heartbeat over WebSocket.
// Similar to handleRegisterPeer but uses the WS remote address. accepted
// reports whether the WebSocket now carries the peer; a response that only
// asks for the key is not accepted.
func (s *Server) handleRegisterPeerWS(msg *pb.RegisterPeer, remoteAddr string) (*pb.RendezvousMessage, bool) {
	id := msg.Id
	if id == "" {
		return nil, false
	}
	clientHost := hostFromAddrString(remoteAddr)

	if !isValidPeerID(id) {
		log.Printf("[signal] Rejected invalid WS peer ID format: %q from %s", id, clientHost)
		return nil, false
	}

	if s.blocklist != nil {
		if s.blocklist.IsIPBlocked(clientHost) {
			log.Printf("[signal] Blocked IP %s tried WS registration", clientHost)
			return nil, false
		}
		if s.blocklist.IsIDBlocked(id) {
			log.Printf("[signal] Blocked ID %s tried WS registration", id)
			return nil, false
		}
	}

	if effectiveID, ok := s.resolveRegistrationPeerID(id, clientHost, nil, nil); !ok {
		return nil, false
	} else if effectiveID != id {
		id = effectiveID
	}

	existing := s.peers.Get(id)
	knownPeer := existing != nil
	var dbPeer *db.Peer
	if !knownPeer {
		if loaded, err := s.db.GetPeer(id); err == nil && loaded != nil {
			dbPeer = loaded
			knownPeer = true
		}
	}
	if !s.allowRegistration(clientHost, id, knownPeer) {
		if knownPeer {
			log.Printf("[signal] Rate limited WS registration from %s for peer %s", clientHost, id)
		} else {
			log.Printf("[signal] Rate limited new WS registration from %s for peer %s", clientHost, id)
		}
		return nil, false
	}
	if existing != nil {
		// Reject banned peers — do not heartbeat or respond
		if existing.Banned {
			log.Printf("[signal] Rejected banned WS peer heartbeat: %s from %s", id, remoteAddr)
			s.revokeBannedPeerAccess(id, nil)
			return nil, false
		}
		if s.rejectIfPeerSoftDeleted(id, clientHost) {
			return nil, false
		}
		if banned, err := s.db.IsPeerBanned(id); err != nil || banned {
			if err != nil {
				log.Printf("[signal] Failed ban check for WS peer heartbeat %s: %v", id, err)
				return nil, false
			}
			log.Printf("[signal] Rejected banned WS peer heartbeat: %s from %s", id, remoteAddr)
			s.revokeBannedPeerAccess(id, nil)
			return nil, false
		}

		if s.keyedPeerMoves(id, existing, net.ParseIP(clientHost), peer.ConnWS) {
			log.Printf("[signal] WS heartbeat for %s from new address %s: asking for its key before moving it", id, remoteAddr)
			return registerPeerResponse(true), false
		}
		if s.peers.KeyRefused(id) {
			return registerPeerResponse(true), false
		}

		// Update heartbeat (WS has no real UDP addr)
		hasPK, ok := s.peers.WSHeartbeat(id, msg.Serial, remoteAddr)
		if !ok {
			return nil, false
		}
		s.db.UpdatePeerStatus(id, "ONLINE", remoteAddr, "signal")

		return registerPeerResponse(!hasPK), true
	}

	if s.rejectIfPeerSoftDeleted(id, clientHost) {
		return nil, false
	}

	if !s.checkEnrollmentPermission(id, clientHost) {
		s.enrollmentLogf(id, "Rejected new WS peer %s from %s (enrollment policy)", id, clientHost)
		if s.isPendingEnrollment(id) {
			// Keep the authenticated WS transport alive while approval is
			// pending. Session delivery remains blocked by
			// targetAcceptsInboundSession until the DB peer row exists.
			s.retainPendingSignalPeer(id, remoteAddr, nil, msg.Serial, peer.ConnWS)
			return registerPeerResponse(false), true
		}
		return nil, false
	}

	// Check if this peer is banned in the database (e.g. removed from memory
	// map after ban but trying to re-register via WS)
	if banned, err := s.db.IsPeerBanned(id); err != nil || banned {
		if err != nil {
			log.Printf("[signal] Failed ban check for WS peer registration %s: %v", id, err)
			return nil, false
		}
		log.Printf("[signal] Rejected banned WS peer registration: %s from %s", id, remoteAddr)
		s.revokeBannedPeerAccess(id, nil)
		return nil, false
	}

	if storedPeerMoves(dbPeer, net.ParseIP(clientHost)) {
		log.Printf("[signal] WS heartbeat for stored peer %s from new address %s: asking for its key before registering it", id, remoteAddr)
		return registerPeerResponse(true), false
	}

	// New peer via WS
	entry := &peer.Entry{
		ID:       id,
		IP:       remoteAddr,
		Serial:   msg.Serial,
		ConnType: peer.ConnWS,
		LastReg:  time.Now(),
	}
	s.peers.Put(entry)

	log.Printf("[signal] New WS peer registered: %s from %s", id, remoteAddr)
	s.db.UpdatePeerStatus(id, "ONLINE", remoteAddr, "signal")
	s.publishPeerOnline(id)

	return registerPeerResponse(true), true
}

// sendToWSPeer sends a protobuf message to a peer connected via WebSocket.
func (s *Server) sendToWSPeer(id string, msg *pb.RendezvousMessage) error {
	route, found := s.peers.Route(id)
	if !found || route.ConnType != peer.ConnWS || route.WSConn == nil {
		return fmt.Errorf("peer %s not connected via WS", id)
	}
	wsc, ok := route.WSConn.(*codec.WSConn)
	if !ok {
		return fmt.Errorf("peer %s has invalid WS connection", id)
	}
	return wsc.WriteMessage(msg)
}

// sendToPeer delivers a protobuf message on the best available transport.
// WebSocket peers stay on WS. Native peers with a UDP heartbeat address are
// sent UDP even when ConnType was stamped TCP by RegisterPk. Returns
// true if a transport accepted the message.
func (s *Server) sendToPeer(id string, msg *pb.RendezvousMessage) bool {
	entry, found := s.peers.Route(id)
	if !found {
		log.Printf("[signal] sendToPeer: peer %s not in map", id)
		return false
	}

	if entry.ConnType == peer.ConnWS {
		if entry.WSConn != nil {
			wsc, ok := entry.WSConn.(*codec.WSConn)
			if ok {
				if err := wsc.WriteMessage(msg); err != nil {
					log.Printf("[signal] WS send to %s: %v", id, err)
					return false
				}
				return true
			}
		}
		log.Printf("[signal] sendToPeer: no WS transport for %s", id)
		return false
	}

	if entry.UDPAddr != nil {
		if s.sendUDP(msg, entry.UDPAddr) {
			return true
		}
		log.Printf("[signal] sendToPeer: UDP send failed for %s (%s)", id, entry.UDPAddr)
		return false
	}

	if pc := s.tcpPunchConnForPeer(id); pc != nil {
		if err := pc.writeProto(msg); err != nil {
			log.Printf("[signal] TCP punch send to %s: %v", id, err)
			return false
		}
		return true
	}

	if entry.TCPConn != nil {
		if err := codec.WriteRawProto(entry.TCPConn, msg); err != nil {
			log.Printf("[signal] TCP send to %s: %v", id, err)
			return false
		}
		return true
	}

	log.Printf("[signal] sendToPeer: no transport for %s (connType=%s)", id, entry.ConnType)
	return false
}
