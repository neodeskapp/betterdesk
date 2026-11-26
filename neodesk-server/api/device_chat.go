package api

import (
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	eventsModule "github.com/neodeskapp/neodesk/neodesk-server/events"
)

// A computer's support chat at the API port (L526). Computers send all
// their calls to the server's API port (api-server, or the API port next
// to the ID server), but the chat lives in the console: the chat socket of
// version 2 (/ws/chat/<id>?v=2) and the computer's chat files
// (/api/chat/device/files, /api/chat/device/files/<id>) and the picture
// of the person who helps (/api/chat/device/helper/picture). The API's
// listener hands exactly these requests to the console in this process,
// before its own credential check, which knows no device tokens and
// refused them with 401. The console checks them as on its own port: the
// device token, "Chat with computers", the socket and frame limits, the
// rate limits and the origin rule. Nothing else of the console answers
// here.
//
// The API port listens before the console is up (L536). Until the console
// is there, and while it stops, these paths answer 503 with Retry-After of
// a few seconds and X-NeoDesk-Chat: starting, so a computer asks again
// soon. Only a server without a console (CONSOLE_PORT=0, or a console
// that could not start) answers 404: it has no chat.

// deviceChatHandler holds the console's handler for a computer's chat.
type deviceChatHandler struct {
	h atomic.Pointer[http.Handler]
	// none: this server has no console
	none atomic.Bool
}

// deviceChatRetry is the Retry-After (seconds) while the console starts.
const deviceChatRetry = "5"

// SetDeviceChat names the handler of a computer's support chat: the
// console in this process. nil means it is not up (yet, or any more).
func (s *Server) SetDeviceChat(h http.Handler) {
	s.deviceChat.none.Store(false)
	if h == nil {
		s.deviceChat.h.Store(nil)
		return
	}
	s.deviceChat.h.Store(&h)
}

// NoDeviceChat says that this server runs without a console: a computer's
// chat answers 404.
func (s *Server) NoDeviceChat() {
	s.deviceChat.h.Store(nil)
	s.deviceChat.none.Store(true)
}

// The paths of a computer's chat.
const (
	deviceChatSocket = "/ws/chat/"
	deviceChatFiles  = "/api/chat/device/files"
	deviceChatHelper = "/api/chat/device/helper/picture"
)

// oneSegment reports whether rest is one non-empty path segment.
func oneSegment(rest string) bool {
	return rest != "" && !strings.Contains(rest, "/")
}

// deviceChatRequest says whether a request is a computer's chat, and
// whether it is its socket.
func deviceChatRequest(r *http.Request) (chat, socket bool) {
	path := r.URL.Path
	if rest, ok := strings.CutPrefix(path, deviceChatSocket); ok && oneSegment(rest) {
		return true, true
	}
	if path == deviceChatFiles || path == deviceChatHelper {
		return true, false
	}
	if rest, ok := strings.CutPrefix(path, deviceChatFiles+"/"); ok && oneSegment(rest) {
		return true, false
	}
	return false, false
}

// deviceChatGate hands a computer's chat to the console and everything
// else to next. The socket answers only for version 2: the chat of
// version 1 never was on this port.
func (s *Server) deviceChatGate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		chat, socket := deviceChatRequest(r)
		if !chat {
			next.ServeHTTP(w, r)
			return
		}
		h := s.deviceChat.h.Load()
		if socket && r.URL.Query().Get("v") != "2" || h == nil && s.deviceChat.none.Load() {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
			return
		}
		if h == nil {
			w.Header().Set("Retry-After", deviceChatRetry)
			w.Header().Set("X-NeoDesk-Chat", "starting")
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "starting", "code": "starting"})
			return
		}
		if socket {
			// The listener's ReadTimeout set a deadline for reading the
			// request; a socket lives on after it
			_ = http.NewResponseController(w).SetReadDeadline(time.Time{})
		}
		(*h).ServeHTTP(w, r)
	})
}

// credentialsEnded tells the console that a device's credentials ended, so
// it closes the chat sockets that lost theirs (L539). deviceID may be ""
// when the device is not known.
func (s *Server) credentialsEnded(deviceID string) {
	if s.eventBus == nil {
		return
	}
	data := map[string]string{}
	if deviceID != "" {
		data["id"] = deviceID
	}
	s.eventBus.Publish(eventsModule.Event{Type: eventsModule.EventDeviceCredentialsEnded, Data: data})
}
