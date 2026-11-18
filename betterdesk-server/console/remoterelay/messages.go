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
// wrote it again, everything else is dropped. The JSON null (frame.type
// threw) and a message the old relay could not write out again
// (JSON.stringify ran out of stack) ended Node.
func agentMessage(text string) (verdict, string) {
	msg, ok := readMessage(text)
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

// viewerMessage decides what happens to a viewer's text message: type
// stop stops the stream; type input goes to the agent as the message with
// its type replaced by event_type, else kind, else "unknown", and without
// event_type; everything else is dropped. The JSON null ended Node.
func viewerMessage(text string) (verdict, string) {
	msg, ok := readMessage(text)
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
		event := v.withType(v.get("event_type").or(v.get("kind")).or(jsValue{kind: jsString, text: "unknown"}))
		return forward, event.stringify()
	}
	return ignore, ""
}
