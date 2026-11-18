package wsrelay

import (
	"errors"
	"fmt"
	"strconv"
	"unicode/utf8"

	"google.golang.org/protobuf/reflect/protoreflect"

	pb "github.com/unitronix/betterdesk-server/proto"
)

// The guest check reads the browser's frames as hbb.RendezvousMessage the
// way the old console's protobuf library (protobufjs 7) decoded them,
// which is not how the signal server decodes them:
//
//   - A known field is read with its declared type whatever wire type its
//     tag says: a string field with a varint tag reads the varint as a
//     length.
//   - A string's length is cut to the end of the enclosing message instead
//     of failing; bytes and sub-messages past the end fail.
//   - Each occurrence of an embedded message replaces the earlier one
//     instead of merging into it, and the members of a oneof are not
//     cleared: a message with both punch_hole_request and request_relay
//     has both, and the check looks at punch_hole_request first.
//   - Varints are read with that library's shortcuts near the end of a
//     message (see uint32 and longVarint), and nesting (messages and
//     groups of unknown fields) stops at depth 100.
//
// decodeRendezvous returns what the check reads: whether the message
// decoded, and the last punch_hole_request and request_relay with their id.

// recursionLimit is the library's nesting limit.
const recursionLimit = 100

// rendezvousRequest is an embedded punch_hole_request or request_relay as
// the check reads it.
type rendezvousRequest struct {
	// ID is the request's id field (field 1), "" when it has none.
	ID string
}

// rendezvousMessage is what the check reads of a decoded
// hbb.RendezvousMessage.
type rendezvousMessage struct {
	PunchHoleRequest *rendezvousRequest
	RequestRelay     *rendezvousRequest
}

// pbField is a field of the schema as the library's decoder reads it.
type pbField struct {
	kind     protoreflect.Kind
	repeated bool
	message  *pbMessage
}

// pbMessage is a message type of the schema.
type pbMessage struct {
	fields map[uint32]*pbField
	// idField is set for the two request types, whose id the check reads.
	idField bool
}

// The top-level type and the field numbers of the two requests in it.
var (
	rendezvousType  = buildSchema(pb.File_rendezvous_proto.Messages().ByName("RendezvousMessage"))
	punchHoleField  = uint32(8)
	requestRelayFld = uint32(18)
)

// buildSchema turns the descriptors of the server's generated code into
// the decoder's schema. rendezvous.proto is the file the old console
// loaded; a map or group field would need a decoder this package does not
// have, so it panics on one.
func buildSchema(md protoreflect.MessageDescriptor) *pbMessage {
	seen := map[protoreflect.FullName]*pbMessage{}
	var build func(md protoreflect.MessageDescriptor) *pbMessage
	build = func(md protoreflect.MessageDescriptor) *pbMessage {
		if m, ok := seen[md.FullName()]; ok {
			return m
		}
		m := &pbMessage{fields: map[uint32]*pbField{}}
		seen[md.FullName()] = m
		name := md.Name()
		m.idField = name == "PunchHoleRequest" || name == "RequestRelay"
		fields := md.Fields()
		for i := 0; i < fields.Len(); i++ {
			fd := fields.Get(i)
			if fd.IsMap() || fd.Kind() == protoreflect.GroupKind {
				panic(fmt.Sprintf("wsrelay: %s.%s: map and group fields are not decoded", md.FullName(), fd.Name()))
			}
			f := &pbField{kind: fd.Kind(), repeated: fd.Cardinality() == protoreflect.Repeated}
			if fd.Kind() == protoreflect.MessageKind {
				f.message = build(fd.Message())
			}
			m.fields[uint32(fd.Number())] = f
		}
		return m
	}
	return build(md)
}

// decodeRendezvous decodes a payload as the old console did; err is the
// library's exception, where the old console got null.
func decodeRendezvous(payload []byte) (*rendezvousMessage, error) {
	r := &pbReader{buf: payload, len: len(payload)}
	out := &rendezvousMessage{}
	if _, err := r.message(rendezvousType, 0, false, 0, out); err != nil {
		return nil, err
	}
	return out, nil
}

// guestTargetMatches is the check on a rendezvous frame of a guest: a
// punch_hole_request, else a request_relay, must name the device of the
// link; a message with neither passes.
func guestTargetMatches(m *rendezvousMessage, peerID string) bool {
	if m == nil || peerID == "" {
		return false
	}
	req := m.PunchHoleRequest
	if req == nil {
		req = m.RequestRelay
	}
	return req == nil || req.ID == peerID
}

// pbReader is the library's buffer reader: pos and the current end len,
// which shrinks while an embedded message is read. Reads past len are
// errors, except where the library read on without looking (at), which
// sees the bytes of the enclosing message and zero past the buffer.
type pbReader struct {
	buf []byte
	pos int
	len int
}

// at is buf[i] as JavaScript reads a typed array: 0 (undefined & 127)
// past the end.
func (r *pbReader) at(i int) byte {
	if i < 0 || i >= len(r.buf) {
		return 0
	}
	return r.buf[i]
}

func (r *pbReader) outOfRange(write int) error {
	if write == 0 {
		write = 1
	}
	return fmt.Errorf("index out of range: %d + %d > %d", r.pos, write, r.len)
}

// errIndexOutOfRange is the generated decoder's own RangeError.
var errIndexOutOfRange = errors.New("index out of range")

var errNesting = errors.New("maximum nesting depth exceeded")

// uint32 reads a varint as an unsigned 32-bit value.
func (r *pbReader) uint32() (uint32, error) {
	if r.len-r.pos < 5 {
		if r.pos >= r.len {
			return 0, r.outOfRange(0)
		}
		if r.buf[r.pos] >= 128 {
			return r.varint32NearEnd()
		}
	}
	var value uint32
	for i := 0; i < 4; i++ {
		b := r.at(r.pos)
		value |= uint32(b&127) << (7 * i)
		r.pos++
		if b < 128 {
			return value, nil
		}
	}
	b := r.at(r.pos)
	value |= uint32(b&15) << 28
	r.pos++
	if b < 128 {
		return value, nil
	}
	// The five bytes after a fifth byte with its high bit set are skipped
	// unread: a ten-byte varint of a negative number.
	r.pos += 5
	if r.pos > r.len {
		r.pos = r.len
		return 0, r.outOfRange(10)
	}
	return value, nil
}

// varint32NearEnd reads at most four bytes, each checked against the end.
func (r *pbReader) varint32NearEnd() (uint32, error) {
	var value uint32
	for i := 0; i < 4; i++ {
		if r.pos >= r.len {
			return 0, r.outOfRange(0)
		}
		b := r.buf[r.pos]
		r.pos++
		value |= uint32(b&127) << (7 * i)
		if b < 128 {
			return value, nil
		}
	}
	return 0, r.outOfRange(0)
}

// longVarint reads a 64-bit varint (readLongVarint). With four bytes or
// fewer left it checks the first three against the end and takes the
// fourth unchecked, whatever its high bit says.
func (r *pbReader) longVarint() error {
	i := 0
	if r.len-r.pos > 4 {
		for ; i < 4; i++ {
			b := r.at(r.pos)
			r.pos++
			if b < 128 {
				return nil
			}
		}
		b := r.at(r.pos)
		r.pos++
		if b < 128 {
			return nil
		}
		i = 0
	} else {
		for ; i < 3; i++ {
			if r.pos >= r.len {
				return r.outOfRange(0)
			}
			b := r.buf[r.pos]
			r.pos++
			if b < 128 {
				return nil
			}
		}
		r.pos++
		return nil
	}
	if r.len-r.pos > 4 {
		for ; i < 5; i++ {
			b := r.at(r.pos)
			r.pos++
			if b < 128 {
				return nil
			}
		}
	} else {
		for ; i < 5; i++ {
			if r.pos >= r.len {
				return r.outOfRange(0)
			}
			b := r.buf[r.pos]
			r.pos++
			if b < 128 {
				return nil
			}
		}
	}
	return errors.New("invalid varint encoding")
}

// fixed skips n bytes of a fixed-size value; write is the length the
// library's message names (it says 4 for a double).
func (r *pbReader) fixed(n, write int) error {
	if r.pos+n > r.len {
		return r.outOfRange(write)
	}
	r.pos += n
	return nil
}

// bytes reads a length and that many bytes, which must all be there.
func (r *pbReader) bytes() error {
	length, err := r.uint32()
	if err != nil {
		return err
	}
	end := r.pos + int(length)
	if end > r.len {
		return r.outOfRange(int(length))
	}
	r.pos = end
	return nil
}

// string reads a length and the text, cut short at the end of the
// enclosing message.
func (r *pbReader) string() (string, error) {
	length, err := r.uint32()
	if err != nil {
		return "", err
	}
	start := r.pos
	end := r.pos + int(length)
	if end > r.len {
		end = r.len
	}
	r.pos = end
	return decodeUTF8(r.buf[start:end]), nil
}

// skip passes a varint.
func (r *pbReader) skipVarint() error {
	for {
		if r.pos >= r.len {
			return r.outOfRange(0)
		}
		b := r.buf[r.pos]
		r.pos++
		if b&128 == 0 {
			return nil
		}
	}
}

// skipType passes an unknown field of the given wire type; groups nest.
func (r *pbReader) skipType(wireType uint32, depth int) error {
	if depth > recursionLimit {
		return errNesting
	}
	switch wireType {
	case 0:
		return r.skipVarint()
	case 1:
		return r.fixed(8, 8)
	case 2:
		n, err := r.uint32()
		if err != nil {
			return err
		}
		return r.fixed(int(n), int(n))
	case 3:
		for {
			t, err := r.uint32()
			if err != nil {
				return err
			}
			if t&7 == 4 {
				return nil
			}
			if err := r.skipType(t&7, depth+1); err != nil {
				return err
			}
		}
	case 5:
		return r.fixed(4, 4)
	}
	return errors.New("invalid wire type " + strconv.Itoa(int(wireType)) + " at offset " + strconv.Itoa(r.pos))
}

// scalar reads one value of a field that is not a message.
func (r *pbReader) scalar(kind protoreflect.Kind) error {
	switch kind {
	case protoreflect.BoolKind, protoreflect.Int32Kind, protoreflect.Uint32Kind, protoreflect.Sint32Kind, protoreflect.EnumKind:
		_, err := r.uint32()
		return err
	case protoreflect.Int64Kind, protoreflect.Uint64Kind, protoreflect.Sint64Kind:
		return r.longVarint()
	case protoreflect.Fixed32Kind, protoreflect.Sfixed32Kind, protoreflect.FloatKind:
		return r.fixed(4, 4)
	case protoreflect.Fixed64Kind, protoreflect.Sfixed64Kind:
		return r.fixed(8, 8)
	case protoreflect.DoubleKind:
		return r.fixed(8, 4)
	case protoreflect.StringKind:
		_, err := r.string()
		return err
	case protoreflect.BytesKind:
		return r.bytes()
	}
	return fmt.Errorf("wsrelay: no decoder for %v", kind)
}

// packable reports the kinds the library reads packed when the wire type
// says so.
func packable(kind protoreflect.Kind) bool {
	switch kind {
	case protoreflect.StringKind, protoreflect.BytesKind, protoreflect.MessageKind:
		return false
	}
	return true
}

// message decodes one message of type m: the whole rest when hasLength is
// false (the top level), else length bytes. The id of a request type is
// returned; top, when not nil, gets the two requests of the top level.
func (r *pbReader) message(m *pbMessage, length uint32, hasLength bool, depth int, top *rendezvousMessage) (string, error) {
	if depth > recursionLimit {
		return "", errNesting
	}
	end := r.len
	saved := r.len
	if hasLength {
		end = r.pos + int(length)
		if end > r.len {
			return "", errIndexOutOfRange
		}
		r.len = end
	}
	id := ""
	for r.pos < end {
		t, err := r.uint32()
		if err != nil {
			return "", err
		}
		f := m.fields[t>>3]
		if f == nil {
			if err := r.skipType(t&7, depth); err != nil {
				return "", err
			}
			continue
		}
		switch {
		case f.kind == protoreflect.MessageKind:
			n, err := r.uint32()
			if err != nil {
				return "", err
			}
			subID, err := r.message(f.message, n, true, depth+1, nil)
			if err != nil {
				return "", err
			}
			if top != nil && !f.repeated {
				switch t >> 3 {
				case punchHoleField:
					top.PunchHoleRequest = &rendezvousRequest{ID: subID}
				case requestRelayFld:
					top.RequestRelay = &rendezvousRequest{ID: subID}
				}
			}
		case f.repeated && packable(f.kind) && t&7 == 2:
			n, err := r.uint32()
			if err != nil {
				return "", err
			}
			packedEnd := r.pos + int(n)
			if packedEnd > r.len {
				return "", errIndexOutOfRange
			}
			r.len = packedEnd
			for r.pos < packedEnd {
				if err := r.scalar(f.kind); err != nil {
					return "", err
				}
			}
			if r.pos != packedEnd {
				return "", errIndexOutOfRange
			}
			r.len = end
		case m.idField && t>>3 == 1 && f.kind == protoreflect.StringKind && !f.repeated:
			text, err := r.string()
			if err != nil {
				return "", err
			}
			id = text
		default:
			if err := r.scalar(f.kind); err != nil {
				return "", err
			}
		}
	}
	if hasLength {
		if r.pos != end {
			return "", errIndexOutOfRange
		}
		r.len = saved
	}
	return id, nil
}

// decodeUTF8 is Node's UTF-8 decoding of a buffer: each maximal part that
// is not a character becomes U+FFFD.
func decodeUTF8(b []byte) string {
	if utf8.Valid(b) {
		return string(b)
	}
	out := make([]rune, 0, len(b))
	for i := 0; i < len(b); {
		c := b[i]
		need, lower, upper := 0, byte(0x80), byte(0xBF)
		switch {
		case c < 0x80:
			out = append(out, rune(c))
			i++
			continue
		case c >= 0xC2 && c <= 0xDF:
			need = 1
		case c >= 0xE0 && c <= 0xEF:
			need = 2
			if c == 0xE0 {
				lower = 0xA0
			} else if c == 0xED {
				upper = 0x9F
			}
		case c >= 0xF0 && c <= 0xF4:
			need = 3
			if c == 0xF0 {
				lower = 0x90
			} else if c == 0xF4 {
				upper = 0x8F
			}
		default:
			out = append(out, utf8.RuneError)
			i++
			continue
		}
		j := i + 1
		ok := true
		for k := 0; k < need; k++ {
			if j >= len(b) || b[j] < lower || b[j] > upper {
				ok = false
				break
			}
			lower, upper = 0x80, 0xBF
			j++
		}
		if !ok {
			out = append(out, utf8.RuneError)
			i = j
			continue
		}
		r, _ := utf8.DecodeRune(b[i:j])
		out = append(out, r)
		i = j
	}
	return string(out)
}
