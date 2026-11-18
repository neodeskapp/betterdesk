package bdapi

import (
	"errors"
	"net/http"

	"github.com/unitronix/betterdesk-server/console/routes/internal/jsreq"
)

// The chat of an agent with the operators. The route file called two
// functions of the API client that do not exist (sendChatMessage and
// getChatHistory), so a message that passes the checks is never sent, and
// a history request that passes them ended the console's process (BUGS
// BD1, BD2). Both stay as they were: the send answers 500, and the history
// request is dropped without an answer (DECISIONS A19).

// errNoChatClient is the TypeError of the missing function.
var errNoChatClient = errors.New("betterdeskApi.sendChatMessage is not a function")

// chatSend answers POST /api/bd/chat/send {device_id, sender, content,
// timestamp} from a device.
func (h *handlers) chatSend(w http.ResponseWriter, r *http.Request) {
	body := jsreq.Body(r)
	if id, ok := jsreq.Prop(body, "device_id").(string); !ok || id == "" {
		fail(w, r, http.StatusBadRequest, "Missing device_id")
		return
	}
	content, ok := jsreq.Prop(body, "content").(string)
	if !ok || content == "" || length(trim(content)) == 0 {
		fail(w, r, http.StatusBadRequest, "Missing content")
		return
	}
	if length(content) > 4096 {
		fail(w, r, http.StatusBadRequest, "Message too long (max 4096 chars)")
		return
	}
	h.serverError(w, r, "Chat send error:", errNoChatClient, "Failed to send message")
}

// chatHistory answers GET /api/bd/chat/history?device_id=&limit= from a
// device: 400 without a device id, and no answer at all otherwise.
func (h *handlers) chatHistory(w http.ResponseWriter, r *http.Request) {
	id, err := textOr(jsreq.Query(r, "device_id"), "")
	if err != nil {
		h.log.Error("Chat history error:", err)
		panic(http.ErrAbortHandler)
	}
	if cut(id, 64) == "" {
		fail(w, r, http.StatusBadRequest, "Missing device_id")
		return
	}
	h.log.Error("Chat history error:", errors.New("betterdeskApi.getChatHistory is not a function"))
	panic(http.ErrAbortHandler)
}
