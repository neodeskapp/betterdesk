package api

import (
	"net/http"
	"sync"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/peer"
)

// TestUnbanWritesThePeerUnderTheMapLock: unbanning a connected peer clears
// its banned flag through the peer map, while other goroutines read the
// peer under the map's lock (run with -race; RV152).
func TestUnbanWritesThePeerUnderTheMapLock(t *testing.T) {
	srv, database := newInternalServer(t, nil)
	if err := database.BanPeer("PEERAAA1", "test"); err != nil {
		t.Fatal(err)
	}
	srv.peers.Put(&peer.Entry{ID: "PEERAAA1", Banned: true})

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				srv.peers.GetSnapshot("PEERAAA1", 2, 4)
			}
		}
	}()
	status, body := asConsole(t, srv, http.MethodPost, "/api/peers/PEERAAA1/unban", "")
	close(stop)
	wg.Wait()
	if status != http.StatusOK {
		t.Fatalf("unban: %d %s", status, body)
	}
	if snap, ok := srv.peers.GetSnapshot("PEERAAA1", 2, 4); !ok || snap.Banned {
		t.Errorf("live peer after unban: %+v, %v; want it there and not banned", snap, ok)
	}
}
