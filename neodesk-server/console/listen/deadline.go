package listen

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"time"
)

// Deadlines bound how long a request may take to send its body and how
// long a client may leave its answer unread, without a limit on the size
// of either: the deadlines move with the bytes, so a long upload or
// download that keeps going passes, and one that stalls is cut off. A
// connection that is hijacked (a socket) loses them, as net/http clears
// a connection's deadlines when it hands it over.
//
// http.Server's ReadTimeout and WriteTimeout cannot do this: they cover a
// whole request and its answer, whatever their size.
type Deadlines struct {
	// BodyStall is the longest wait for the next bytes of a request body.
	BodyStall time.Duration
	// BodyGrace and BodyRate bound a body as a whole: it has to have
	// arrived BodyGrace after the request started, plus one second for
	// every BodyRate bytes read so far. A body that keeps coming at
	// BodyRate bytes a second or faster never runs out of time. Zero
	// BodyRate leaves the whole body unbounded.
	BodyGrace time.Duration
	BodyRate  int64
	// WriteStall is the longest a piece of the answer of at most
	// writeChunk bytes may take to reach the client. Zero leaves writes
	// without a deadline.
	WriteStall time.Duration
}

// writeChunk is the most an answer writes under one deadline.
const writeChunk = 64 << 10

// Wrap applies the deadlines to the requests h serves. Zero fields leave
// that side without a deadline.
func (d Deadlines) Wrap(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rc := http.NewResponseController(w)
		var body *deadlineBody
		if d.BodyStall > 0 && r.Body != nil && r.Body != http.NoBody {
			body = &deadlineBody{ReadCloser: r.Body, rc: rc, d: d, start: time.Now()}
			body.arm()
			r.Body = body
		}
		var dw *deadlineWriter
		if d.WriteStall > 0 {
			dw = &deadlineWriter{ResponseWriter: w, rc: rc, stall: d.WriteStall}
			dw.extend()
			w = dw
		}
		h.ServeHTTP(w, r)
		// net/http reads off what is left of the body and writes the rest
		// of the answer after the handler, under deadlines of their own
		if body != nil && !body.done {
			body.arm()
		}
		if dw != nil {
			dw.extend()
		}
	})
}

// deadlineBody sets the connection's read deadline before every read of
// the body and clears it when the body ends, so that the server's own
// read of the idle connection after the body does not run into it (that
// read cancels the request's context when it fails).
type deadlineBody struct {
	io.ReadCloser
	rc    *http.ResponseController
	d     Deadlines
	start time.Time
	read  int64
	done  bool
}

// arm sets the deadline for the next bytes of the body.
func (b *deadlineBody) arm() {
	deadline := time.Now().Add(b.d.BodyStall)
	if b.d.BodyRate > 0 {
		whole := b.start.Add(b.d.BodyGrace + time.Duration(b.read)*time.Second/time.Duration(b.d.BodyRate))
		if whole.Before(deadline) {
			deadline = whole
		}
	}
	_ = b.rc.SetReadDeadline(deadline)
}

func (b *deadlineBody) Read(p []byte) (int, error) {
	b.arm()
	n, err := b.ReadCloser.Read(p)
	b.read += int64(n)
	// After a timeout the deadline stays, so that net/http's own read of
	// the rest of the body fails as well and the connection closes
	if err == io.EOF {
		b.done = true
		_ = b.rc.SetReadDeadline(time.Time{})
	}
	return n, err
}

// deadlineWriter moves the connection's write deadline forward before
// every piece of the answer.
type deadlineWriter struct {
	http.ResponseWriter
	rc    *http.ResponseController
	stall time.Duration
}

func (w *deadlineWriter) extend() { _ = w.rc.SetWriteDeadline(time.Now().Add(w.stall)) }

func (w *deadlineWriter) WriteHeader(status int) {
	w.extend()
	w.ResponseWriter.WriteHeader(status)
}

func (w *deadlineWriter) Write(p []byte) (int, error) {
	written := 0
	for len(p) > 0 {
		piece := p[:min(len(p), writeChunk)]
		w.extend()
		n, err := w.ResponseWriter.Write(piece)
		written += n
		if err != nil {
			return written, err
		}
		p = p[len(piece):]
	}
	return written, nil
}

// ReadFrom copies in pieces of writeChunk bytes, each through the
// server's own ReadFrom where it has one.
func (w *deadlineWriter) ReadFrom(src io.Reader) (int64, error) {
	var total int64
	for {
		w.extend()
		n, err := io.CopyN(w.ResponseWriter, src, writeChunk)
		total += n
		if err == io.EOF {
			return total, nil
		}
		if err != nil {
			return total, err
		}
	}
}

func (w *deadlineWriter) Flush() {
	w.extend()
	_ = w.rc.Flush()
}

// Hijack hands the connection over without deadlines.
func (w *deadlineWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) { return w.rc.Hijack() }

// Unwrap lets http.ResponseController reach the server's writer.
func (w *deadlineWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
