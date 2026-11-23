package signal

import (
	"fmt"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/config"
	"github.com/neodeskapp/neodesk/neodesk-server/db"
	"github.com/neodeskapp/neodesk/neodesk-server/peer"
	pb "github.com/neodeskapp/neodesk/neodesk-server/proto"
)

// configReadCounter counts the reads of server_config rows.
type configReadCounter struct {
	db.Database
	reads int
}

func (c *configReadCounter) GetConfig(key string) (string, error) {
	c.reads++
	return c.Database.GetConfig(key)
}

func (c *configReadCounter) ListConfigByPrefix(prefix string) ([]db.ServerConfig, error) {
	c.reads++
	return c.Database.ListConfigByPrefix(prefix)
}

func TestOnlineRequestReadsPendingQueueOnce(t *testing.T) {
	srv, database := newTestSignalServer(t, config.EnrollmentModeManaged)
	counter := &configReadCounter{Database: database}
	srv.db = counter
	ids := make([]string, 200)
	for i := range ids {
		ids[i] = fmt.Sprintf("ONLINE%04d", i)
		srv.peers.Put(&peer.Entry{ID: ids[i], LastReg: time.Now()})
	}
	if err := database.SetConfig("pending_device_ONLINE0007", `{"device_id":"ONLINE0007"}`); err != nil {
		t.Fatal(err)
	}

	states := srv.handleOnlineRequest(&pb.OnlineRequest{Peers: ids}).GetOnlineResponse().GetStates()

	if counter.reads > 1 {
		t.Fatalf("one OnlineRequest of %d ids read server_config %d times", len(ids), counter.reads)
	}
	if states[0] != 0xFE || states[1] != 0xFF {
		t.Fatalf("states = %08b %08b, want the pending ONLINE0007 alone offline", states[0], states[1])
	}
}

func TestOnlineRequestAnswersAtMostTheCap(t *testing.T) {
	srv, _ := newTestSignalServer(t, config.EnrollmentModeOpen)
	ids := make([]string, maxOnlineRequestPeers+100)
	for i := range ids {
		ids[i] = fmt.Sprintf("CAP%06d", i)
	}

	states := srv.handleOnlineRequest(&pb.OnlineRequest{Peers: ids}).GetOnlineResponse().GetStates()
	if want := (maxOnlineRequestPeers + 7) / 8; len(states) != want {
		t.Fatalf("answer for %d ids has %d bytes, want %d", len(ids), len(states), want)
	}
}
