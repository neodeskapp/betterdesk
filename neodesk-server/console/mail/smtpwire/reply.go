package smtpwire

import "bytes"

// maxResponseSize is how many bytes a single server reply may take while it
// is being received.
const maxResponseSize = 1024 * 1024

// replyQueue collects what the server sends into replies. A reply is one
// line, or several lines when all but the last are continuations
// ("250-..."). Lines of a reply are joined with "\n", their line ends
// removed. Replies stay raw bytes here and are decoded when they are used.
type replyQueue struct {
	// remainder holds the bytes after the last line end
	remainder []byte
	// current is the reply being received, partial says its last line was a
	// continuation
	current []byte
	partial bool
	// complete are the replies not yet handled, queued their size in bytes
	complete []string
	queued   int
}

// add takes the bytes of one read. due says that the bytes ended a reply and
// left none half received, which is when replies are handled. tooLarge says
// that the server sent more than a reply may hold; the queue is empty then.
//
// A line without an end counts against the limit as soon as it is longer,
// and a reply of continuation lines after each line. Complete replies are
// not handled while a continued reply is half received; the ones waiting
// then count against the limit too. (nodemailer has no limit for those: a
// server that ends every packet inside a continued reply can fill its
// memory.)
func (q *replyQueue) add(chunk []byte) (due, tooLarge bool) {
	if bytes.IndexByte(chunk, '\n') < 0 {
		q.remainder = append(q.remainder, chunk...)
		if len(q.remainder) > maxResponseSize {
			q.reset()
			return false, true
		}
		return false, false
	}
	data := chunk
	if len(q.remainder) > 0 {
		data = append(q.remainder, chunk...)
	}
	for {
		end := bytes.IndexByte(data, '\n')
		if end < 0 {
			break
		}
		line := data[:end]
		data = data[end+1:]
		if len(line) > 0 && line[len(line)-1] == '\r' {
			line = line[:len(line)-1]
		}
		if q.partial {
			q.current = append(q.current, '\n')
		} else {
			q.current = q.current[:0]
		}
		q.current = append(q.current, line...)
		q.partial = isPartialLine(line)
		if !q.partial {
			q.complete = append(q.complete, string(q.current))
			q.queued += len(q.current)
			q.current = q.current[:0]
		} else if len(q.current) > maxResponseSize {
			q.reset()
			return false, true
		}
	}
	q.remainder = append([]byte(nil), data...)
	if len(q.remainder) > maxResponseSize || (q.partial && q.queued > maxResponseSize) {
		q.reset()
		return false, true
	}
	return !q.partial, false
}

// next takes the oldest complete reply.
func (q *replyQueue) next() (raw string, ok bool) {
	if len(q.complete) == 0 {
		return "", false
	}
	raw = q.complete[0]
	q.complete = q.complete[1:]
	q.queued -= len(raw)
	return raw, true
}

// reset forgets everything received so far.
func (q *replyQueue) reset() {
	*q = replyQueue{}
}

// isPartialLine reports whether a reply line is a continuation: digits
// followed by a dash.
func isPartialLine(line []byte) bool {
	i := 0
	for i < len(line) && isDigit(line[i]) {
		i++
	}
	return i > 0 && i < len(line) && line[i] == '-'
}

// replyCode is the number a reply starts with, 0 when it starts with none.
func replyCode(reply string) int {
	const max = int(^uint(0) >> 1)
	n := 0
	for i := 0; i < len(reply) && isDigit(reply[i]); i++ {
		d := int(reply[i] - '0')
		if n > (max-d)/10 {
			return max
		}
		n = n*10 + d
	}
	return n
}

// isClosingReply reports whether the server says it is about to close (421).
func isClosingReply(reply string) bool {
	return len(reply) >= 4 && reply[:3] == "421" && (reply[3] == ' ' || reply[3] == '-')
}

// isFailureReply reports whether text starts like a complete refusal: a
// three-digit 4xx or 5xx code and a separator.
func isFailureReply(reply string) bool {
	return len(reply) >= 4 && (reply[0] == '4' || reply[0] == '5') && isDigit(reply[1]) && isDigit(reply[2]) &&
		(reply[3] == ' ' || reply[3] == '-')
}
