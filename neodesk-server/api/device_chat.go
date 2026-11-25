package api

import (
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

// A computer's support chat at the API port (L526). Computers send all
// their calls to the server's API port (api-server, or the API port next
// to the ID server), but the chat lives in the console: the chat socket of
// version 2 (/ws/chat/<id>?v=2) and the computer's chat files
// (/api/chat/device/files, /api/chat/device/files/<id>). The API's
// listener hands exactly these requests to the console in this process,
// before its own credential check, which knows no device tokens and
// refused them with 401. The console checks them as on its own port: the
// device token, "Chat with computers", the socket and frame limits, the
// rate limits and the origin rule. Nothing else of the console answers
// here. Without a console (CONSOLE_PORT=0, or a console that did not
// start) these paths answer 404: this server has no chat.

// deviceChatHandler holds the console's handler for a computer's chat.
type deviceChatHandler struct {
	h atomic.Pointer[http.Handler]
}

// SetDeviceChat names the handler of a computer's support chat: the
// console in this process; nil while there is none.
func (s *Server) SetDeviceChat(h http.Handler) {
	if h == nil {
		s.deviceChat.h.Store(nil)
		return
	}
	s.deviceChat.h.Store(&h)
}

// The paths of a computer's chat.
const (
	deviceChatSocket = "/ws/chat/"
	deviceChatFiles  = "/api/chat/device/files"
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
	if path == deviceChatFiles {
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
		if h == nil || socket && r.URL.Query().Get("v") != "2" {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
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
