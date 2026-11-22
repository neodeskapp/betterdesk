package contract

import (
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"unicode/utf8"
)

// The signal server's messages on /ws/rendezvous are protobuf messages in
// the frames of RustDesk's BytesCodec. Kept as length and hash, a message
// that carries one address of the machine (the relay server the signal
// server detects) would differ on every machine. decodeFrames writes such
// a message field by field instead, without the .proto files: every byte
// is still compared, and a case can mark the one field volatile.

// decodeFrames reads data as BytesCodec frames, each a protobuf message,
// and returns the decoded messages (decodeMessage). ok is false when data
// is not whole frames of whole messages.
func decodeFrames(data []byte) (messages []any, ok bool) {
	for len(data) > 0 {
		// The low two bits of the first byte say how many more bytes the
		// length takes; the length is the header shifted right by two.
		size := int(data[0]&3) + 1
		if len(data) < size {
			return nil, false
		}
		var header uint32
		for i := size - 1; i >= 0; i-- {
			header = header<<8 | uint32(data[i])
		}
		n := int(header >> 2)
		data = data[size:]
		if len(data) < n {
			return nil, false
		}
		message, ok := decodeMessage(data[:n])
		if !ok {
			return nil, false
		}
		messages = append(messages, message)
		data = data[n:]
	}
	return messages, len(messages) > 0
}

// decodeMessage reads a protobuf message without its schema: an object of
// its fields by number ("4"), a field that comes more than once as an
// array. A varint or fixed number is a number, a length-delimited field a
// string when it is printable UTF-8, else a message when it reads as one,
// else its bytes as "hex:..." (a group, wire types 3 and 4, is no message
// here). An empty message is an empty object.
func decodeMessage(data []byte) (map[string]any, bool) {
	fields := map[string]any{}
	for len(data) > 0 {
		tag, n := binary.Uvarint(data)
		if n <= 0 || tag>>3 == 0 {
			return nil, false
		}
		data = data[n:]
		var value any
		switch tag & 7 {
		case 0:
			v, n := binary.Uvarint(data)
			if n <= 0 {
				return nil, false
			}
			value, data = json.Number(strconv.FormatUint(v, 10)), data[n:]
		case 1:
			if len(data) < 8 {
				return nil, false
			}
			value, data = json.Number(strconv.FormatUint(binary.LittleEndian.Uint64(data), 10)), data[8:]
		case 5:
			if len(data) < 4 {
				return nil, false
			}
			value, data = json.Number(strconv.FormatUint(uint64(binary.LittleEndian.Uint32(data)), 10)), data[4:]
		case 2:
			size, n := binary.Uvarint(data)
			if n <= 0 || uint64(len(data)-n) < size {
				return nil, false
			}
			payload := data[n : n+int(size)]
			data = data[n+int(size):]
			value = decodeBytes(payload)
		default:
			return nil, false
		}
		key := strconv.FormatUint(tag>>3, 10)
		switch earlier := fields[key].(type) {
		case nil:
			fields[key] = value
		case repeated:
			fields[key] = append(earlier, value)
		default:
			fields[key] = repeated{earlier, value}
		}
	}
	for key, value := range fields {
		if list, ok := value.(repeated); ok {
			fields[key] = []any(list)
		}
	}
	return fields, true
}

// repeated collects the values of a field that comes more than once.
type repeated []any

// decodeBytes reads a length-delimited field (see decodeMessage).
func decodeBytes(payload []byte) any {
	if printable(payload) {
		return string(payload)
	}
	if message, ok := decodeMessage(payload); ok && len(payload) > 0 {
		return message
	}
	return "hex:" + hex.EncodeToString(payload)
}

// printable reports whether b is UTF-8 text without control characters.
func printable(b []byte) bool {
	if !utf8.Valid(b) {
		return false
	}
	for _, r := range string(b) {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}
