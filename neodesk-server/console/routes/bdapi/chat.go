package bdapi

import (
	"math"
	"net/http"
	"strconv"

	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/jsreq"
)

// The chat of an agent with the operators. The messages live on the API
// server, in the conversation named by the device id, as the chat
// sockets keep them.

// chatSend answers POST /api/nd/chat/send {device_id, sender, content,
// timestamp} from a device: the message goes into the device's
// conversation, from the device, under the sender's name (the device id
// when sender is no text). 502 when the API server does not take it.
func (h *handlers) chatSend(w http.ResponseWriter, r *http.Request) {
	body := jsreq.Body(r)
	id, ok := jsreq.Prop(body, "device_id").(string)
	if !ok || id == "" {
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
	device := cut(id, 64)
	sender := device
	if s, isText := jsreq.Prop(body, "sender").(string); isText {
		sender = cut(trim(s), 128)
	}
	res, err := h.d.GoAPI.Post(r.Context(), "/chat/messages", goapi.Body(goapi.NewObject(
		"conversation_id", device,
		"from_id", device,
		"from_name", sender,
		"to_id", "",
		"text", cut(trim(content), 4096),
	)))
	if err != nil {
		h.log.Warn("Chat send Go proxy failed:", err.Error())
		fail(w, r, http.StatusBadGateway, "Failed to send message")
		return
	}
	messageID := ""
	data := res.Data()
	if v := orValue(jsreq.Prop(data, "id"), jsreq.Prop(data, "message_id")); goapi.Truthy(v) {
		messageID, _ = text(v)
	}
	if messageID == "" {
		messageID = strconv.FormatInt(h.now().UnixMilli(), 10)
	}
	kit.JSON(w, r, http.StatusOK, goapi.NewObject("success", true, "message_id", messageID))
}

// chatHistory answers GET /api/nd/chat/history?device_id=&limit= from a
// device: the newest messages of its conversation (100 unless limit says
// otherwise, at most 200), oldest first, as {id, device_id, sender,
// content, timestamp}. An empty list when the API server fails.
//
// The conversation is the one of the device the token is bound to. The
// query still has to name a device, but the guard in front takes the
// body's device_id before the query's, so the query's value decides
// nothing.
func (h *handlers) chatHistory(w http.ResponseWriter, r *http.Request) {
	id, err := textOr(jsreq.Query(r, "device_id"), "")
	if err != nil || cut(id, 64) == "" {
		fail(w, r, http.StatusBadRequest, "Missing device_id")
		return
	}
	device := cut(kit.Device(r).DeviceID, 64)
	limit := 100
	if n, ok := jsreq.ParseInt(jsreq.Query(r, "limit"), 10); ok && n >= 1 {
		limit = int(math.Min(n, 200))
	}
	res, err := h.d.GoAPI.Get(r.Context(), "/chat/history/"+goapi.EncodeURIComponent(device), goapi.Query("limit", limit))
	if err != nil {
		h.log.Warn("Chat history Go proxy failed:", err.Error())
		kit.JSON(w, r, http.StatusOK, []any{})
		return
	}
	messages, _ := goapi.Dig(res.Data(), "messages").([]any)
	history := make([]any, 0, len(messages))
	for _, m := range messages {
		if _, isObject := m.(*goapi.Object); !isObject {
			continue
		}
		messageID, _ := text(jsreq.Prop(m, "id"))
		history = append(history, goapi.NewObject(
			"id", messageID,
			"device_id", orValue(jsreq.Prop(m, "conversation_id"), device),
			"sender", orValue(jsreq.Prop(m, "from_name"), orValue(jsreq.Prop(m, "from_id"), "")),
			"content", orValue(jsreq.Prop(m, "text"), ""),
			"timestamp", orValue(jsreq.Prop(m, "created_at"), isoTime(float64(h.now().UnixMilli()))),
		))
	}
	if len(history) > limit {
		history = history[len(history)-limit:]
	}
	kit.JSON(w, r, http.StatusOK, history)
}
