package wsrelay

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"google.golang.org/protobuf/reflect/protoreflect"

	pb "github.com/unitronix/betterdesk-server/proto"
)

// codecRecording is testdata/node_codec.json (nodeoracle/codec.js).
type codecRecording struct {
	Peer   string `json:"peer"`
	Max    int    `json:"max"`
	Encode []struct {
		Length int    `json:"length"`
		Header string `json:"header"`
		Total  int    `json:"total"`
		Error  string `json:"error"`
	} `json:"encode"`
	Decode  [][]decodeStep `json:"decode"`
	Vectors []struct {
		Hex   string `json:"hex"`
		Error string `json:"error"`
		OK    bool   `json:"ok"`
		Punch *struct {
			ID string `json:"id"`
		} `json:"punch"`
		Relay *struct {
			ID string `json:"id"`
		} `json:"relay"`
		Matches *bool `json:"matches"`
	} `json:"vectors"`
	Schema map[string][]struct {
		ID       int    `json:"id"`
		Name     string `json:"name"`
		Type     string `json:"type"`
		Repeated bool   `json:"repeated"`
		Map      bool   `json:"map"`
	} `json:"schema"`
}

type decodeStep struct {
	Chunk  json.RawMessage   `json:"chunk"`
	Frames []json.RawMessage `json:"frames"`
	Error  string            `json:"error"`
}

func loadCodec(t testing.TB) *codecRecording {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "node_codec.json"))
	if err != nil {
		t.Fatal(err)
	}
	var rec codecRecording
	if err := json.Unmarshal(raw, &rec); err != nil {
		t.Fatal(err)
	}
	return &rec
}

// chunkBytes is a chunk of the recording: hex, or {repeat, byte}.
func chunkBytes(t testing.TB, raw json.RawMessage) []byte {
	t.Helper()
	var text string
	if json.Unmarshal(raw, &text) == nil {
		b, err := hex.DecodeString(text)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	var big struct {
		Repeat int  `json:"repeat"`
		Byte   byte `json:"byte"`
	}
	if err := json.Unmarshal(raw, &big); err != nil {
		t.Fatal(err)
	}
	return bytes.Repeat([]byte{big.Byte}, big.Repeat)
}

// shown is a frame as the recording shows it: hex up to 256 bytes, else
// its length and SHA-256.
func shown(b []byte) string {
	if len(b) > 256 {
		sum := sha256.Sum256(b)
		return fmt.Sprintf(`{"length":%d,"sha256":"%s"}`, len(b), hex.EncodeToString(sum[:]))
	}
	return `"` + hex.EncodeToString(b) + `"`
}

func compact(raw json.RawMessage) string {
	var b bytes.Buffer
	if err := json.Compact(&b, raw); err != nil {
		return string(raw)
	}
	return b.String()
}

// TestNodeFrameHeaders compares the header EncodeRelayFrame writes, and
// its refusal over the limit, with Node's encodeRelayFrame.
func TestNodeFrameHeaders(t *testing.T) {
	rec := loadCodec(t)
	if rec.Max != MaxRelayFrameSize {
		t.Fatalf("limit: node %d, go %d", rec.Max, MaxRelayFrameSize)
	}
	for _, v := range rec.Encode {
		frame, err := EncodeRelayFrame(make([]byte, v.Length))
		if v.Error != "" {
			if err == nil || err.Error() != v.Error {
				t.Errorf("length %d: want error %q, got %v", v.Length, v.Error, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("length %d: %v", v.Length, err)
			continue
		}
		if got := hex.EncodeToString(frame[:len(frame)-v.Length]); got != v.Header || len(frame) != v.Total {
			t.Errorf("length %d: header node %s (%d), go %s (%d)", v.Length, v.Header, v.Total, got, len(frame))
		}
	}
}

// TestNodeFrameDecoder feeds every recorded stream, chunk by chunk, to the
// decoder and compares the frames each chunk finished and the errors.
func TestNodeFrameDecoder(t *testing.T) {
	rec := loadCodec(t)
	for i, steps := range rec.Decode {
		d := &frameDecoder{}
		for j, st := range steps {
			frames, err := d.feed(chunkBytes(t, st.Chunk))
			if st.Error != "" {
				if err == nil || err.Error() != st.Error {
					t.Errorf("stream %d chunk %d: want error %q, got %v", i, j, st.Error, err)
				}
				break
			}
			if err != nil {
				t.Errorf("stream %d chunk %d: %v", i, j, err)
				break
			}
			var want, got []string
			for _, f := range st.Frames {
				want = append(want, compact(f))
			}
			for _, f := range frames {
				got = append(got, shown(f))
			}
			if strings.Join(want, ",") != strings.Join(got, ",") {
				t.Errorf("stream %d chunk %d:\n node %v\n   go %v", i, j, want, got)
				break
			}
		}
	}
}

// TestNodeRendezvousDecoding decodes every recorded payload and compares
// with protobufjs: whether it decoded, its exception, the two requests
// and their ids, and the guest check for the recorded device.
func TestNodeRendezvousDecoding(t *testing.T) {
	rec := loadCodec(t)
	if len(rec.Vectors) < 5000 {
		t.Fatalf("only %d vectors", len(rec.Vectors))
	}
	for _, v := range rec.Vectors {
		payload, err := hex.DecodeString(v.Hex)
		if err != nil {
			t.Fatal(err)
		}
		msg, err := decodeRendezvous(payload)
		errText := ""
		if err != nil {
			errText = err.Error()
		}
		if errText != v.Error || (msg != nil) != v.OK {
			t.Errorf("%s: node ok=%v %q, go ok=%v %q", v.Hex, v.OK, v.Error, msg != nil, errText)
			continue
		}
		if msg == nil {
			continue
		}
		same := func(want *struct {
			ID string `json:"id"`
		}, got *rendezvousRequest) bool {
			if want == nil || got == nil {
				return want == nil && got == nil
			}
			return want.ID == got.ID
		}
		if !same(v.Punch, msg.PunchHoleRequest) || !same(v.Relay, msg.RequestRelay) {
			t.Errorf("%s: requests node %+v %+v, go %+v %+v", v.Hex, v.Punch, v.Relay, msg.PunchHoleRequest, msg.RequestRelay)
		}
		if v.Matches == nil || guestTargetMatches(msg, rec.Peer) != *v.Matches {
			t.Errorf("%s: guest check node %v, go %v", v.Hex, v.Matches, guestTargetMatches(msg, rec.Peer))
		}
	}
}

// TestSchemaIsNodes checks that the server's generated descriptors are the
// schema protobufjs read from web-nodejs/protos/rendezvous.proto: the same
// types, field numbers, names, types and repetition.
func TestSchemaIsNodes(t *testing.T) {
	rec := loadCodec(t)
	kinds := map[protoreflect.Kind]string{
		protoreflect.BoolKind: "bool", protoreflect.Int32Kind: "int32", protoreflect.Uint32Kind: "uint32",
		protoreflect.Int64Kind: "int64", protoreflect.Uint64Kind: "uint64", protoreflect.Sint32Kind: "sint32",
		protoreflect.Sint64Kind: "sint64", protoreflect.Fixed32Kind: "fixed32", protoreflect.Fixed64Kind: "fixed64",
		protoreflect.Sfixed32Kind: "sfixed32", protoreflect.Sfixed64Kind: "sfixed64", protoreflect.FloatKind: "float",
		protoreflect.DoubleKind: "double", protoreflect.StringKind: "string", protoreflect.BytesKind: "bytes",
		protoreflect.EnumKind: "enum",
	}
	got := map[string][]string{}
	var walk func(md protoreflect.MessageDescriptor)
	walk = func(md protoreflect.MessageDescriptor) {
		name := "." + string(md.FullName())
		if _, ok := got[name]; ok {
			return
		}
		got[name] = []string{}
		fields := md.Fields()
		var list []string
		for i := 0; i < fields.Len(); i++ {
			fd := fields.Get(i)
			typ := kinds[fd.Kind()]
			if fd.Kind() == protoreflect.MessageKind {
				typ = "." + string(fd.Message().FullName())
				walk(fd.Message())
			}
			list = append(list, fmt.Sprintf("%d %s %s %v %v", fd.Number(), fd.JSONName(), typ, fd.Cardinality() == protoreflect.Repeated, fd.IsMap()))
		}
		sort.Strings(list)
		got[name] = list
	}
	walk(pb.File_rendezvous_proto.Messages().ByName("RendezvousMessage"))
	want := map[string][]string{}
	for name, fields := range rec.Schema {
		var list []string
		for _, f := range fields {
			list = append(list, fmt.Sprintf("%d %s %s %v %v", f.ID, f.Name, f.Type, f.Repeated, f.Map))
		}
		sort.Strings(list)
		want[name] = list
	}
	if fmt.Sprint(want) != fmt.Sprint(got) {
		t.Errorf("schema differs:\n node %v\n   go %v", want, got)
	}
}
