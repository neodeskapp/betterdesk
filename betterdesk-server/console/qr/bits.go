package qr

import "strings"

// bitBuffer collects bits, the highest bit of every value first, and packs
// them into bytes from the top bit down.
type bitBuffer struct {
	bytes  []byte
	length int // bits written
}

// put appends the lowest n bits of value, highest of them first.
func (b *bitBuffer) put(value, n int) {
	for i := n - 1; i >= 0; i-- {
		if b.length%8 == 0 {
			b.bytes = append(b.bytes, 0)
		}
		if value>>i&1 == 1 {
			b.bytes[b.length/8] |= 0x80 >> (b.length % 8)
		}
		b.length++
	}
}

// putSegment appends the characters of a segment in its mode (ISO/IEC
// 18004:2015, 7.4.3 to 7.4.5).
func (b *bitBuffer) putSegment(s segment) {
	data := s.data
	switch s.mode {
	case numeric:
		// Groups of three digits are written as a number in 10 bits. A last
		// group of two digits takes 7 bits, a single digit 4.
		for len(data) > 0 {
			n := min(3, len(data))
			value := 0
			for _, digit := range []byte(data[:n]) {
				value = value*10 + int(digit-'0')
			}
			b.put(value, n*3+1)
			data = data[n:]
		}
	case alphanumeric:
		// Pairs are written as first*45 + second in 11 bits, a last single
		// character in 6 bits.
		for len(data) >= 2 {
			first := strings.IndexByte(alphanumericChars, data[0])
			second := strings.IndexByte(alphanumericChars, data[1])
			b.put(first*45+second, 11)
			data = data[2:]
		}
		if len(data) == 1 {
			b.put(strings.IndexByte(alphanumericChars, data[0]), 6)
		}
	default:
		for _, c := range []byte(data) {
			b.put(int(c), 8)
		}
	}
}

// dataCodewords writes the segments into the data codewords of a version at
// a level and fills what is left (ISO/IEC 18004:2015, 7.4.1 and 7.4.9 to
// 7.4.10).
//
// Every segment is a four-bit mode indicator, the number of characters and
// the characters. After the last one comes a terminator of four zero bits
// (fewer when less room is left), zero bits up to the next full byte, and
// then the two pad bytes 0xEC and 0x11 in turn until the room is used up.
func dataCodewords(segments []segment, version int, level Level) []byte {
	var b bitBuffer
	for _, s := range segments {
		b.put(int(s.mode), 4)
		b.put(len(s.data), charCountBits(s.mode, version))
		b.putSegment(s)
	}

	room := dataCapacityBits(version, level)
	b.put(0, min(4, room-b.length))
	if rest := b.length % 8; rest != 0 {
		b.put(0, 8-rest)
	}
	for i := 0; b.length < room; i++ {
		if i%2 == 0 {
			b.put(0xEC, 8)
		} else {
			b.put(0x11, 8)
		}
	}
	return b.bytes
}
