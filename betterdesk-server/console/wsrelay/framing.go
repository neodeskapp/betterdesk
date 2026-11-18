package wsrelay

import (
	"errors"
	"fmt"
)

// MaxRelayFrameSize is the largest payload of one RustDesk frame the
// bridge takes in either direction: desktop frames can be large.
const MaxRelayFrameSize = 64 << 20

// EncodeRelayFrame puts the RustDesk BytesCodec header in front of a
// payload: the length times four plus the header's own length minus one,
// little-endian, in one to four bytes. An empty payload is the single byte
// 0, a valid frame. A payload over MaxRelayFrameSize is an error.
func EncodeRelayFrame(payload []byte) ([]byte, error) {
	n := len(payload)
	if n > MaxRelayFrameSize {
		return nil, fmt.Errorf("invalid relay frame size: %d", n)
	}
	var header []byte
	switch {
	case n <= 0x3F:
		header = []byte{byte(n << 2)}
	case n <= 0x3FFF:
		v := uint16(n<<2) | 0x01
		header = []byte{byte(v), byte(v >> 8)}
	case n <= 0x3FFFFF:
		v := uint32(n)*4 + 0x02
		header = []byte{byte(v), byte(v >> 8), byte(v >> 16)}
	default:
		v := uint32(n)*4 + 0x03
		header = []byte{byte(v), byte(v >> 8), byte(v >> 16), byte(v >> 24)}
	}
	out := make([]byte, 0, len(header)+n)
	out = append(out, header...)
	return append(out, payload...), nil
}

// errFrameBuffer is the failure of a decoder that would hold more than one
// frame of the largest size and its header.
var errFrameBuffer = errors.New("relay frame buffer exceeds maximum size")

// frameDecoder cuts a byte stream into BytesCodec frames. It keeps what
// is left of an unfinished frame for the next call.
type frameDecoder struct {
	buf []byte
}

// feed adds bytes of the stream and returns the payloads of the frames
// they finish, in order. It fails when the bytes held would exceed a
// frame of the largest size with its header, or when a header announces
// a payload over MaxRelayFrameSize; the decoder is then of no further
// use.
func (d *frameDecoder) feed(chunk []byte) ([][]byte, error) {
	if len(d.buf)+len(chunk) > MaxRelayFrameSize+4 {
		return nil, errFrameBuffer
	}
	d.buf = append(d.buf, chunk...)
	var frames [][]byte
	offset := 0
	for offset < len(d.buf) {
		headerLen := int(d.buf[offset]&0x03) + 1
		if len(d.buf)-offset < headerLen {
			break
		}
		var encoded uint64
		for i := 0; i < headerLen; i++ {
			encoded |= uint64(d.buf[offset+i]) << (8 * i)
		}
		payloadLen := encoded / 4
		if payloadLen > MaxRelayFrameSize {
			return nil, fmt.Errorf("invalid relay payload length: %d", payloadLen)
		}
		if uint64(len(d.buf)-offset-headerLen) < payloadLen {
			break
		}
		start := offset + headerLen
		end := start + int(payloadLen)
		frames = append(frames, append([]byte{}, d.buf[start:end]...))
		offset = end
	}
	if offset > 0 {
		rest := len(d.buf) - offset
		if rest == 0 {
			d.buf = nil
		} else {
			d.buf = append([]byte{}, d.buf[offset:]...)
		}
	}
	return frames, nil
}
