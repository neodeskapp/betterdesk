package signal

import (
	"bytes"
	"sync"
	"testing"

	"github.com/unitronix/betterdesk-server/config"
	"github.com/unitronix/betterdesk-server/db"
	pb "github.com/unitronix/betterdesk-server/proto"
)

// The tests below write a live peer from a connection's goroutine while
// another goroutine reads the peer map under its lock, as the heartbeat
// checker and the API do. Run with -race, they fail when a write goes
// through the entry without the map's lock.

// readPeerMapWhile reads every entry of srv's peer map under the map's lock
// until write returns.
func readPeerMapWhile(srv *Server, write func()) {
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-done:
				return
			default:
				srv.peers.GetAllSnapshots(config.DegradedThreshold, config.CriticalThreshold)
				srv.peers.GetStats(config.DegradedThreshold, config.CriticalThreshold)
			}
		}
	}()
	write()
	close(done)
	wg.Wait()
}

func TestRegisterPkWritesThePeerUnderTheMapLock(t *testing.T) {
	srv, _ := newTestSignalServer(t, config.EnrollmentModeOpen)
	msg := newRegisterPk("LOCKPK01")
	if got := registerPkResult(srv.processRegisterPk(msg, "203.0.113.70:41000")); got != pb.RegisterPkResponse_OK {
		t.Fatalf("first RegisterPk = %v", got)
	}
	readPeerMapWhile(srv, func() {
		for i := 0; i < 20; i++ {
			srv.processRegisterPk(newRegisterPk("LOCKPK01"), "203.0.113.70:41000")
		}
	})
	if e := srv.peers.Get("LOCKPK01"); e == nil || e.IP != "203.0.113.70:41000" || !bytes.Equal(e.PK, msg.Pk) {
		t.Fatalf("entry after RegisterPk = %+v", e)
	}
}

func TestWebSocketRegistrationWritesThePeerUnderTheMapLock(t *testing.T) {
	srv, _ := newTestSignalServer(t, config.EnrollmentModeOpen)
	reg := &pb.RegisterPeer{Id: "LOCKWS01", Serial: 1}
	if _, ok := srv.handleRegisterPeerWS(reg, "203.0.113.71:41000"); !ok {
		t.Fatal("first WebSocket registration refused")
	}
	readPeerMapWhile(srv, func() {
		for i := 0; i < 20; i++ {
			srv.handleRegisterPeerWS(&pb.RegisterPeer{Id: "LOCKWS01", Serial: int32(i)}, "203.0.113.71:41000")
			bindPeerWSConn(srv, "LOCKWS01", nil)
		}
	})
	if e := srv.peers.Get("LOCKWS01"); e == nil || e.Serial != 19 {
		t.Fatalf("entry after WebSocket heartbeats = %+v", e)
	}
}

func TestIDChangeWritesThePeerUnderTheMapLock(t *testing.T) {
	srv, database := newTestSignalServer(t, config.EnrollmentModeOpen)
	storedPK := bytes.Repeat([]byte{0x42}, 32)
	if err := database.UpsertPeer(&db.Peer{
		ID: "LOCKOLD1", Status: "ONLINE", PK: storedPK, UUID: "aabbccddeeff00112233445566778899",
	}); err != nil {
		t.Fatal(err)
	}
	putOnlinePeer(srv, "LOCKOLD1", "203.0.113.72", 41000, 0)

	readPeerMapWhile(srv, func() {
		resp := srv.processIDChange(&pb.RegisterPk{
			Id: "LOCKNEW1", OldId: "LOCKOLD1", Uuid: []byte("aabbccddeeff00112233445566778899"),
		}, "")
		if got := registerPkResult(resp); got != pb.RegisterPkResponse_OK {
			t.Errorf("ID change = %v", got)
		}
	})
	if e := srv.peers.Get("LOCKNEW1"); e == nil || !bytes.Equal(e.PK, storedPK) {
		t.Fatalf("entry after ID change = %+v", e)
	}
}
