package console

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/httpx"
)

// The bridge lets the Node console reach what moved to Go and lives in
// this process's memory: the agents' sockets (bd-api's relay routes ask
// for sessions and online devices) and the panel event sockets (the
// registration routes publish to them). It exists only while the Node
// console runs behind this one, and goes when those routes move.
//
// The front door names the bridge to Node on every request it passes on,
// in the header X-BD-Bridge: the loopback address of this console and a
// token made at start. A client cannot set the header: the front door
// replaces it. Node calls POST /__bd/node/<what> with the token in
// X-BD-Bridge-Token. A call is taken only from a loopback address and only
// with the token; any other request to the path goes on to Node like any
// request Go has no route for, so the path answers as if it did not
// exist.
const (
	bridgePrefix      = "/__bd/node/"
	bridgeHeader      = "X-BD-Bridge"
	bridgeTokenHeader = "X-BD-Bridge-Token"
	// bridgeMaxBody is the most a bridge call may send.
	bridgeMaxBody = 1 << 20
)

type nodeBridge struct {
	s     *Server
	token string
}

func newNodeBridge(s *Server) *nodeBridge {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("console: random source failed: " + err.Error())
	}
	return &nodeBridge{s: s, token: hex.EncodeToString(b)}
}

// header is the value of X-BD-Bridge: "<url> <token>", or "" before the
// console listens.
func (b *nodeBridge) header() string {
	if b.s.listener == nil {
		return ""
	}
	addr, ok := b.s.listener.Addr().(*net.TCPAddr)
	if !ok {
		return ""
	}
	host := "127.0.0.1"
	switch {
	case addr.IP.IsLoopback() && addr.IP.To4() == nil:
		host = "[::1]"
	case !addr.IP.IsUnspecified() && !addr.IP.IsLoopback():
		host = addr.IP.String()
		if addr.IP.To4() == nil {
			host = "[" + host + "]"
		}
	}
	return "http://" + host + ":" + strconv.Itoa(addr.Port) + " " + b.token
}

// takes reports whether r is a bridge call this console answers.
func (b *nodeBridge) takes(r *http.Request) bool {
	if r.Method != http.MethodPost || !strings.HasPrefix(r.URL.Path, bridgePrefix) {
		return false
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if ip := net.ParseIP(host); err != nil || ip == nil || !ip.IsLoopback() {
		return false
	}
	token := r.Header.Get(bridgeTokenHeader)
	return subtle.ConstantTimeCompare([]byte(token), []byte(b.token)) == 1
}

// ServeHTTP answers a bridge call.
func (b *nodeBridge) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, bridgeMaxBody+1))
	if err != nil || len(body) > bridgeMaxBody {
		http.Error(w, "body too large", http.StatusRequestEntityTooLarge)
		return
	}
	switch strings.TrimPrefix(r.URL.Path, bridgePrefix) {
	case "panel-event":
		// The message as Node's JSON.stringify wrote it goes out as it is.
		b.s.deps.StatusPush.PublishPanelText(string(body))
		w.WriteHeader(http.StatusNoContent)
	case "relay":
		b.relay(w, body)
	default:
		http.NotFound(w, r)
	}
}

// relay runs one call of bd-api's relay routes:
//
//	{"call":"create","args":[initiator, target]}  {sessionId, initiatorToken, targetToken}
//	{"call":"get","args":[id]}                    the session without tokens, or null
//	{"call":"delete","args":[id]}                 whether there was one
//	{"call":"notify","args":[target, payload]}    whether the target's socket was open
//	{"call":"online"}                             the ids with an open signal socket
//	{"call":"isOnline","args":[id]}               {"online": bool}, {} for a device never seen
//	{"call":"pending","args":[target]}            the sessions waiting for the target
func (b *nodeBridge) relay(w http.ResponseWriter, body []byte) {
	var req struct {
		Call string            `json:"call"`
		Args []json.RawMessage `json:"args"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "bad call", http.StatusBadRequest)
		return
	}
	text := func(i int) string {
		var s string
		if i < len(req.Args) {
			_ = json.Unmarshal(req.Args[i], &s)
		}
		return s
	}
	relay := b.s.deps.Relay
	var out any
	switch req.Call {
	case "create":
		id, it, tt := relay.CreateRelaySession(text(0), text(1))
		out = goapi.NewObject("sessionId", id, "initiatorToken", it, "targetToken", tt)
	case "get":
		if info := relay.GetRelaySession(text(0)); info != nil {
			out = info
		}
	case "delete":
		out = relay.DeleteSession(text(0))
	case "notify":
		var payload any = nil
		if len(req.Args) > 1 {
			payload, _ = goapi.Parse(req.Args[1])
		}
		out = relay.NotifyTarget(text(0), payload)
	case "online":
		out = relay.OnlineDeviceIDs()
	case "isOnline":
		online, known := relay.IsDeviceOnline(text(0))
		if known {
			out = goapi.NewObject("online", online)
		} else {
			out = goapi.NewObject()
		}
	case "pending":
		out = relay.PendingFor(text(0))
	default:
		http.Error(w, "unknown call", http.StatusBadRequest)
		return
	}
	data, err := httpx.Marshal(out)
	if err != nil {
		http.Error(w, "encode", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(data)
}
