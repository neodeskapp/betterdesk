package codec

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/coder/websocket"
	pb "github.com/neodeskapp/neodesk/neodesk-server/proto"
	"google.golang.org/protobuf/proto"
)

// TestWSConnCountsAcrossGoroutines: the signal server reads a connection
// in one goroutine and writes its keepalives in another, and each looks at
// what the other counts (FramesRead in the keepalive loop, SessionSummary
// after a read fails). Under -race this fails while the counts are plain
// fields (BUGS L433).
func TestWSConnCountsAcrossGoroutines(t *testing.T) {
	t.Setenv("WS_DEBUG_FRAMES", "1")
	const writes = 50
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer ws.CloseNow()
		data, _ := proto.Marshal(&pb.RendezvousMessage{Union: &pb.RendezvousMessage_RegisterPeer{RegisterPeer: &pb.RegisterPeer{Id: "RACE1"}}})
		if ws.Write(r.Context(), websocket.MessageBinary, data) != nil {
			return
		}
		for {
			if _, _, err := ws.Read(r.Context()); err != nil {
				return
			}
		}
	}))
	defer srv.Close()

	ctx := t.Context()
	ws, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	wsc := NewWSConn(ws, ctx, "test")
	defer wsc.Close()

	written := make(chan struct{})
	go func() {
		// The keepalive loop: writes, and asks whether the client sent anything
		defer close(written)
		for range writes {
			if err := wsc.WriteKeepAlive(); err != nil {
				t.Error(err)
				return
			}
			wsc.FramesRead()
		}
	}()
	// The read loop: reads, and reports the connection's numbers
	if _, err := wsc.ReadMessage(); err != nil {
		t.Fatal(err)
	}
	for range writes {
		wsc.SessionSummary()
	}
	<-written

	_, reads, sent, readAny, sentAny := wsc.SessionSummary()
	if reads != 1 || sent != writes || !readAny || !sentAny || wsc.FramesRead() != 1 {
		t.Errorf("summary: %d read (%v), %d written (%v), FramesRead %d", reads, readAny, sent, sentAny, wsc.FramesRead())
	}
}
