package contract

import (
	"net/http"
	"reflect"
	"testing"
)

func pbVarint(n uint64) []byte {
	var out []byte
	for n >= 0x80 {
		out = append(out, byte(n)|0x80)
		n >>= 7
	}
	return append(out, byte(n))
}

func pbBytes(field int, payload []byte) []byte {
	out := pbVarint(uint64(field<<3 | 2))
	out = append(out, pbVarint(uint64(len(payload)))...)
	return append(out, payload...)
}

func pbNumber(field int, n uint64) []byte {
	return append(pbVarint(uint64(field<<3)), pbVarint(n)...)
}

// pbFrame puts the BytesCodec length header in front of a payload of up
// to 16383 bytes.
func pbFrame(payload []byte) []byte {
	if n := len(payload); n <= 0x3f {
		return append([]byte{byte(n << 2)}, payload...)
	}
	v := len(payload)<<2 | 1
	return append([]byte{byte(v), byte(v >> 8)}, payload...)
}

// punchHoleResponse is a RendezvousMessage with punch_hole_response (11):
// failure (3) OFFLINE and relay_server (4).
func punchHoleResponse(relay string) []byte {
	return pbFrame(pbBytes(11, append(pbNumber(3, 2), pbBytes(4, []byte(relay))...)))
}

func TestDecodeFrames(t *testing.T) {
	key := make([]byte, 100)
	for i := range key {
		key[i] = byte(i)
	}
	data := append(punchHoleResponse("203.0.113.7:21117"), pbFrame(pbBytes(13, pbBytes(1, key)))...)
	data = append(data, pbFrame(append(pbNumber(1, 7), pbNumber(1, 8)...))...)
	got, ok := decodeFrames(data)
	if !ok {
		t.Fatal("frames not read")
	}
	want := `[{"11":{"3":2,"4":"203.0.113.7:21117"}},{"13":{"1":"hex:` + hexOf(key) + `"}},{"1":[7,8]}]`
	if text := string(encodeJSON(got)); text != want {
		t.Errorf("frames\n got %s\nwant %s", text, want)
	}

	for name, bad := range map[string][]byte{
		"cut frame":       punchHoleResponse("203.0.113.7:21117")[:10],
		"field zero":      pbFrame([]byte{0x00, 0x01}),
		"cut varint":      pbFrame([]byte{0x08, 0x80}),
		"group":           pbFrame([]byte{0x0b}),
		"empty":           nil,
		"length too long": pbFrame([]byte{0x0a, 0x05, 'a'}),
	} {
		if got, ok := decodeFrames(bad); ok {
			t.Errorf("%s: read as %s", name, encodeJSON(got))
		}
	}
}

func hexOf(b []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, 0, 2*len(b))
	for _, c := range b {
		out = append(out, digits[c>>4], digits[c&15])
	}
	return string(out)
}

// The signal server's answer to a punch hole for an unknown device holds
// the relay address it detects for its machine. Read as frames, with that
// one field marked, the answer of another machine passes and every other
// field is still compared.
func TestPunchHoleAnswerOfAnotherMachine(t *testing.T) {
	answer := func(message []byte) Answer {
		body := socketBody("", []socketMessage{{binary: true, data: message}}, true, 1000, "", true)
		return NewAnswer(101, http.Header{"Content-Type": {"application/json"}}, body, false, NewNormalizer("http://bdgo-c1:5000", ""))
	}
	recorded := answer(punchHoleResponse("203.0.113.7:21117"))
	if got := string(recorded.Body.JSON); got != `{"closed":{"code":1000,"reason":""},"messages":[{"frames":[{"11":{"3":2,"4":"203.0.113.7:21117"}}]}]}` {
		t.Fatalf("body = %s", got)
	}
	want := newGoldenAnswer(recorded, plainMarks([]string{`messages[0].frames[0]["11"]["4"]`}), nil)

	if diffs := CompareAnswers(want, answer(punchHoleResponse("198.51.100.200:21117"))); len(diffs) > 0 {
		t.Errorf("another machine's relay address: %q", diffStrings(diffs))
	}
	other := pbFrame(pbBytes(11, append(pbNumber(3, 0), pbBytes(4, []byte("198.51.100.200:21117"))...)))
	diffs := diffStrings(CompareAnswers(want, answer(other)))
	if !reflect.DeepEqual(diffs, []string{`body messages[0].frames[0]["11"]["3"]: expected 2, got 0`}) {
		t.Errorf("another failure: %q", diffs)
	}

	// Without Frames the message stays length and hash.
	body := string(socketBody("", []socketMessage{{binary: true, data: punchHoleResponse("x")}}, false, 0, "", false))
	if want := `{"messages":[{"binary":{"length":`; len(body) < len(want) || body[:len(want)] != want {
		t.Errorf("without frames: %s", body)
	}
}
