package proxy

import (
	"bytes"
	"errors"
	"io"
	"net/http"
)

// RecordLimit is how much of a request body a Recording keeps. It is
// above the 2 MB the console's body step reads at most.
const RecordLimit = 2<<20 + 4096

// ErrBodyTooLarge is what Rewind returns when more of the body was read
// than a Recording keeps.
var ErrBodyTooLarge = errors.New("request body was read past what can be forwarded")

// Recording keeps what was read of a request body, so a request the Go
// console started to handle can still go to the Node console whole.
type Recording struct {
	src      io.ReadCloser
	buf      bytes.Buffer
	overflow bool
}

// Record gives the request a body that remembers what is read from it.
func Record(r *http.Request) *Recording {
	rec := &Recording{src: r.Body}
	if r.Body != nil && r.Body != http.NoBody {
		r.Body = rec
	}
	return rec
}

// Read reads from the request and keeps a copy.
func (rec *Recording) Read(p []byte) (int, error) {
	n, err := rec.src.Read(p)
	if n > 0 && !rec.overflow {
		if rec.buf.Len()+n > RecordLimit {
			rec.overflow = true
			rec.buf.Reset()
		} else {
			rec.buf.Write(p[:n])
		}
	}
	return n, err
}

// Close closes the request's body.
func (rec *Recording) Close() error { return rec.src.Close() }

// Rewind returns the body from its first byte: what was read so far,
// then the rest of the request.
func (rec *Recording) Rewind() (io.ReadCloser, error) {
	if rec.overflow {
		return nil, ErrBodyTooLarge
	}
	if rec.src == nil || rec.src == http.NoBody {
		return http.NoBody, nil
	}
	return &rewound{Reader: io.MultiReader(bytes.NewReader(rec.buf.Bytes()), rec.src), src: rec.src}, nil
}

type rewound struct {
	io.Reader
	src io.Closer
}

func (b *rewound) Close() error { return b.src.Close() }
