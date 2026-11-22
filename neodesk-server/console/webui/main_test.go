package webui

import (
	"bytes"
	"io"
	"log"
	"os"
	"sync"
	"testing"
)

// The host logs every error it answers. Tests keep the log quiet and read
// it through captureLog when the lines matter.
func TestMain(m *testing.M) {
	log.SetOutput(io.Discard)
	os.Exit(m.Run())
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// captureLog collects what is logged until the test ends.
func captureLog(t *testing.T) *syncBuffer {
	t.Helper()
	out := &syncBuffer{}
	log.SetOutput(out)
	t.Cleanup(func() { log.SetOutput(io.Discard) })
	return out
}
