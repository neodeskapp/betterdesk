package chat

import (
	"context"
	"errors"

	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/jsjson"
	"github.com/neodeskapp/neodesk/neodesk-server/logging"
)

// The chat's messages, contacts, groups and read marks are kept by the API
// server (api/chat_handlers.go). The relay of version 1 reaches
// them through these calls, as the old console's routes and relay did
// through its API client.

// errNoURI is the URIError of encodeURIComponent for half of a surrogate
// pair.
var errNoURI = errors.New("URI malformed")

// pathSegment is encodeURIComponent(v): v as text, escaped for a path.
// It fails where the old code threw: an object whose member toString
// hides the function, half of a surrogate pair.
func pathSegment(v jsjson.Value) (string, error) {
	text, err := v.Text()
	if err != nil {
		return "", err
	}
	if hasLoneSurrogate(text) {
		return "", errNoURI
	}
	return goapi.EncodeURIComponent(text), nil
}

// apiData is response.data as axios handed it on: the parsed JSON when
// the body is JSON, else the text.
func apiData(resp *goapi.Response) jsjson.Value {
	text := resp.Text()
	if v, err := jsjson.Parse(text); err == nil {
		return v
	}
	return jsjson.OfText(text)
}

// apiGet sends GET path and returns response.data; ok is false when the
// call failed (no answer, a status outside 200 to 299, the path guard).
func apiGet(ctx context.Context, api *goapi.Client, path string) (jsjson.Value, bool) {
	resp, err := api.Get(ctx, path)
	if err != nil {
		return jsjson.Value{}, false
	}
	return apiData(resp), true
}

// apiPost sends body (JSON text) to path and returns response.data.
func apiPost(ctx context.Context, api *goapi.Client, path string, body []byte) (jsjson.Value, bool) {
	resp, err := api.Post(ctx, path, goapi.RawBody(body))
	if err != nil {
		return jsjson.Value{}, false
	}
	return apiData(resp), true
}

// loadHistory is GET /chat/history/<conversation>?limit=100: the last 100
// messages of a conversation. ok is false when the call failed or the id
// cannot go into a path.
func loadHistory(ctx context.Context, api *goapi.Client, conversation jsjson.Value) (jsjson.Value, bool) {
	segment, err := pathSegment(conversation)
	if err != nil {
		return jsjson.Value{}, false
	}
	data, ok := apiGet(ctx, api, "/chat/history/"+segment+"?limit=100")
	if !ok {
		logging.Logf("console", "WARN: [Chat] Failed to load chat history")
	}
	return data, ok
}

// loadContacts is GET /chat/contacts/<id>: the devices and operators a
// device can write to.
func loadContacts(ctx context.Context, api *goapi.Client, id jsjson.Value) (jsjson.Value, bool) {
	segment, err := pathSegment(id)
	if err != nil {
		return jsjson.Value{}, false
	}
	data, ok := apiGet(ctx, api, "/chat/contacts/"+segment)
	if !ok {
		logging.Logf("console", "WARN: [Chat] Failed to load chat contacts")
	}
	return data, ok
}

// loadGroups is GET /chat/groups/<id>: the groups a member is in.
func loadGroups(ctx context.Context, api *goapi.Client, id jsjson.Value) (jsjson.Value, bool) {
	segment, err := pathSegment(id)
	if err != nil {
		return jsjson.Value{}, false
	}
	data, ok := apiGet(ctx, api, "/chat/groups/"+segment)
	if !ok {
		logging.Logf("console", "WARN: [Chat] Failed to load chat groups")
	}
	return data, ok
}

// saveMessage is POST /chat/messages with the body of a message.
func saveMessage(ctx context.Context, api *goapi.Client, body []byte) {
	if _, ok := apiPost(ctx, api, "/chat/messages", body); !ok {
		logging.Logf("console", "WARN: [Chat] Failed to persist chat message")
	}
}

// markRead is POST /chat/read.
func markRead(ctx context.Context, api *goapi.Client, body []byte) {
	apiPost(ctx, api, "/chat/read", body)
}

// createGroup is POST /chat/groups and returns the group the API server
// made.
func createGroup(ctx context.Context, api *goapi.Client, body []byte) (jsjson.Value, bool) {
	return apiPost(ctx, api, "/chat/groups", body)
}
