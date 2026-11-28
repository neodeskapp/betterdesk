package relay

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/codec"
	"github.com/neodeskapp/neodesk/neodesk-server/config"
	pb "github.com/neodeskapp/neodesk/neodesk-server/proto"
)

func TestAuthorizeTicketRejectsReplayAndKeepsAShorterExpiry(t *testing.T) {
	r := NewAuthorizationRegistry()
	now := time.Unix(1_700_000_000, 0)
	r.now = func() time.Time { return now }
	ctx := context.Background()

	ticket := Ticket{UUID: "ticket-api", Initiator: "INIT", Target: "TGT", Account: 3, ConsoleSession: "h"}
	if err := r.Authorize(ctx, ticket); err != nil {
		t.Fatalf("first Authorize: %v", err)
	}
	if err := r.Authorize(ctx, ticket); err != nil {
		t.Fatalf("retry of the same ticket: %v", err)
	}
	other := ticket
	other.Initiator = "OTHER"
	if err := r.Authorize(ctx, other); !errors.Is(err, ErrTicketRejected) {
		t.Fatalf("other pair on a taken UUID: %v, want ErrTicketRejected", err)
	}
	if !r.Claim("ticket-api") || !r.Claim("ticket-api") {
		t.Fatal("both sides claim")
	}
	if err := r.Authorize(ctx, ticket); !errors.Is(err, ErrTicketRejected) {
		t.Fatalf("replay of a spent ticket: %v, want ErrTicketRejected", err)
	}

	short := Ticket{UUID: "ticket-short", Initiator: "INIT", Target: "TGT", Expires: now.Add(time.Second)}
	if err := r.Authorize(ctx, short); err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Second)
	if r.Admitted("ticket-short", "INIT", "TGT") {
		t.Fatal("a ticket outlived its own expiry")
	}
}

// reports records what a SessionReporter heard.
type reports struct {
	mu      sync.Mutex
	started []string
	ended   map[string]SessionStats
	done    chan struct{}
}

func (r *reports) RelayStarted(uuid string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.started = append(r.started, uuid)
}

func (r *reports) RelayEnded(uuid string, stats SessionStats) {
	r.mu.Lock()
	r.ended[uuid] = stats
	r.mu.Unlock()
	close(r.done)
}

// TestTheReporterHearsStartEndAndBytesOfATCPSession: the relay tells its
// SessionReporter when a pair starts and ends, with what each side sent.
func TestTheReporterHearsStartEndAndBytesOfATCPSession(t *testing.T) {
	cfg := config.DefaultConfig()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	cfg.RelayPort = port

	registry := NewAuthorizationRegistry()
	rep := &reports{ended: map[string]SessionStats{}, done: make(chan struct{})}
	srv := New(cfg)
	srv.SetTicketClaims(registry)
	srv.SetSessionReporter(rep)
	if err := srv.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()

	const uuid = "reporter-bytes-uuid"
	if !registry.AuthorizeFor(uuid, "INIT", "TGT", 0) {
		t.Fatal("authorize")
	}
	request := &pb.RendezvousMessage{Union: &pb.RendezvousMessage_RequestRelay{RequestRelay: &pb.RequestRelay{Uuid: uuid}}}
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	first, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if err := codec.WriteRawProto(first, request); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond) // first side waits in pending
	second, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if err := codec.WriteRawProto(second, request); err != nil {
		t.Fatal(err)
	}
	waitRelayPairing(t, srv)

	fromFirst := bytes.Repeat([]byte("a"), 1000)
	fromSecond := bytes.Repeat([]byte("b"), 300)
	if _, err := first.Write(fromFirst); err != nil {
		t.Fatal(err)
	}
	if _, err := second.Write(fromSecond); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(second, make([]byte, len(fromFirst))); err != nil {
		t.Fatalf("second side reads: %v", err)
	}
	if _, err := io.ReadFull(first, make([]byte, len(fromSecond))); err != nil {
		t.Fatalf("first side reads: %v", err)
	}
	first.Close()

	select {
	case <-rep.done:
	case <-time.After(3 * time.Second):
		t.Fatal("reporter never heard the end")
	}
	rep.mu.Lock()
	defer rep.mu.Unlock()
	if len(rep.started) != 1 || rep.started[0] != uuid {
		t.Fatalf("started %v, want [%s]", rep.started, uuid)
	}
	got := rep.ended[uuid]
	if got.BytesFromFirst != int64(len(fromFirst)) || got.BytesFromSecond != int64(len(fromSecond)) {
		t.Fatalf("stats %+v, want %d from first and %d from second", got, len(fromFirst), len(fromSecond))
	}
}
