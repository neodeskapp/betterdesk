package chat

import (
	"net/http"
	"strings"
	"unicode/utf16"

	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/kit"
)

// found is the answer to a search.
type found struct {
	Success  bool `json:"success"`
	Messages any  `json:"messages"`
}

// search answers GET /api/chat/search?q=<text>&conversation_id=<id>. It
// asks the API server for up to 50 messages that have the first 200
// characters of the text, and answers an empty list when the text is
// empty and whenever the call does not succeed.
//
// The call goes to /api/api/chat/search, a path the API server does not
// have, so today every search answers the empty list.
func (h *handlers) search(w http.ResponseWriter, r *http.Request) {
	nothing := found{Success: true, Messages: []any{}}
	query := r.URL.Query()
	text, whole := firstUnits(strings.Join(query["q"], ","), 200)
	if text == "" || !whole {
		kit.JSON(w, r, http.StatusOK, nothing)
		return
	}
	var conversation any = ""
	switch ids := query["conversation_id"]; {
	case len(ids) == 1:
		conversation = ids[0]
	case len(ids) > 1:
		conversation = ids
	}
	resp, err := h.api.Get(r.Context(), "/api/chat/search",
		goapi.Query("q", text), goapi.Query("conversation_id", conversation), goapi.Query("limit", 50))
	if err != nil {
		kit.JSON(w, r, http.StatusOK, nothing)
		return
	}
	messages := goapi.Dig(resp.Data(), "messages")
	if !goapi.Truthy(messages) {
		messages = []any{}
	}
	kit.JSON(w, r, http.StatusOK, found{Success: true, Messages: messages})
}

// firstUnits is s.slice(0, n): the first n characters as JavaScript
// counts them, where a character outside the basic plane is two. whole
// is false when the cut goes through such a character; the text then
// cannot be sent on.
func firstUnits(s string, n int) (text string, whole bool) {
	units := utf16.Encode([]rune(s))
	if len(units) <= n {
		return s, true
	}
	units = units[:n]
	if utf16.IsSurrogate(rune(units[n-1])) && units[n-1] < 0xDC00 {
		return string(utf16.Decode(units)), false
	}
	return string(utf16.Decode(units)), true
}
