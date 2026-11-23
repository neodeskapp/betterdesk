package remoterelay

// verdict is what the relay does with a text message.
type verdict int

const (
	// ignore drops the message.
	ignore verdict = iota
	// forward passes the text on: an agent's message to the viewers, a
	// viewer's input event to the agent.
	forward
	// stop is a viewer's request to stop the stream.
	stop
	// tooDeep is a viewer's input event nested too deeply for
	// JSON.stringify; it ended the Node process when an agent was there to
	// get it.
	tooDeep
	// drop ends the connection: the message ended the Node process.
	drop
)

// agentMessage decides what happens to an agent's text message: JSON
// whose type is pong or status goes to the viewers as JSON.stringify
// wrote it again, everything else is dropped, also any text over
// MaxAgentText, unread. The JSON null (frame.type threw) and a message
// the old relay could not write out again (JSON.stringify ran out of
// stack) ended Node.
func agentMessage(text string) (verdict, string) {
	if len(text) > MaxAgentText {
		return ignore, ""
	}
	msg, ok := readMessage(text, func(typ jsValue) bool { return typ.isText("pong") || typ.isText("status") })
	if !ok {
		return ignore, ""
	}
	if msg.isNull() {
		return drop, ""
	}
	if typ := msg.typeMember(); !typ.isText("pong") && !typ.isText("status") {
		return ignore, ""
	}
	if msg.depth > stringifyDepth {
		return drop, ""
	}
	return forward, msg.value.stringify()
}

// inputKinds are the kinds of input the agent handles (the NeoDesk
// agent's InputEvent: mouse and key events).
var inputKinds = map[string]bool{
	"mouse_move": true, "mouse_down": true, "mouse_up": true, "wheel": true,
	"key_down": true, "key_up": true, "key_press": true,
}

// viewerMessage decides what happens to a viewer's text message: type
// stop stops the stream for that viewer; type input whose event_type,
// else kind, is one of the inputKinds goes to the agent as the message
// with its type replaced by that kind and without event_type; everything
// else is dropped, also input that would name the relay's own start or
// stop. The JSON null ended Node.
func viewerMessage(text string) (verdict, string) {
	msg, ok := readMessage(text, func(typ jsValue) bool { return typ.isText("stop") || typ.isText("input") })
	if !ok {
		return ignore, ""
	}
	if msg.isNull() {
		return drop, ""
	}
	switch typ := msg.typeMember(); {
	case typ.isText("stop"):
		return stop, ""
	case typ.isText("input"):
		if msg.depth > stringifyDepth {
			return tooDeep, ""
		}
		v := msg.value
		kind := v.get("event_type").or(v.get("kind"))
		if kind.kind != jsString || !inputKinds[kind.text] {
			return ignore, ""
		}
		return forward, v.withType(kind).stringify()
	}
	return ignore, ""
}
