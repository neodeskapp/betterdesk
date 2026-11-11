package smtpwire

// dataStream turns a message into what is sent after DATA: every line end
// becomes CRLF (a bare CR and a bare LF each count as a line end), a dot at
// the start of a line is doubled, and the end is a line with a single dot.
// A receiver that takes a lone CR or LF for a line end therefore never sees
// a line with only a dot inside the message.
type dataStream struct {
	// last is the byte before the next one, -1 before the first byte
	last int
	// in and out count the bytes taken and produced
	in, out int64
}

func newDataStream() *dataStream { return &dataStream{last: -1} }

// transform encodes the next piece of the message and appends it to dst.
func (d *dataStream) transform(dst, chunk []byte) []byte {
	start := len(dst)
	for _, b := range chunk {
		switch {
		case d.last == '\r' && b != '\n':
			// a bare CR ends a line; a dot after it starts one
			dst = append(dst, '\n')
			if b == '.' {
				dst = append(dst, '.')
			}
		case b == '\n' && d.last != '\r':
			dst = append(dst, '\r')
		case b == '.' && (d.last == '\n' || d.last == -1):
			dst = append(dst, '.')
		}
		dst = append(dst, b)
		d.last = int(b)
	}
	d.in += int64(len(chunk))
	d.out += int64(len(dst) - start)
	return dst
}

// finish appends the end of the data: the line end the message lacks, then
// the dot line.
func (d *dataStream) finish(dst []byte) []byte {
	start := len(dst)
	switch d.last {
	case '\n':
		dst = append(dst, ".\r\n"...)
	case '\r':
		dst = append(dst, "\n.\r\n"...)
	default:
		dst = append(dst, "\r\n.\r\n"...)
	}
	d.out += int64(len(dst) - start)
	return dst
}
