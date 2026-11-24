package signal

import (
	"net"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/config"
	cryptopkg "github.com/neodeskapp/neodesk/neodesk-server/crypto"
	"github.com/neodeskapp/neodesk/neodesk-server/peer"
	pb "github.com/neodeskapp/neodesk/neodesk-server/proto"
	"github.com/neodeskapp/neodesk/neodesk-server/relay"
)

// TestRelayTicketNamesThePanelAccount: the relay ticket of a session the
// panel's web client opens for a signed-in account names that account,
// and the session is live under it once both sides claimed the ticket. A
// saved sign-in is handed out only to the account whose own session is
// live (L469).
func TestRelayTicketNamesThePanelAccount(t *testing.T) {
	srv, _ := newTestSignalServer(t, config.EnrollmentModeOpen)
	initiatorAddr := udpAddr("127.0.0.1", 51857)
	targetAddr := udpAddr("198.51.100.98", 58012)
	putOnlinePeer(srv, "TGTSIGNIN1", targetAddr.IP.String(), targetAddr.Port, peer.ConnTCP)

	client, server := net.Pipe()
	t.Cleanup(func() { client.Close(); server.Close() })
	go func() {
		buf := make([]byte, 64*1024)
		for {
			if _, err := client.Read(buf); err != nil {
				return
			}
		}
	}()
	srv.tcpPunchConns.Store(normalizeAddrKey(initiatorAddr.String()), &tcpPunchConn{conn: server, createdAt: time.Now()})
	srv.panelConns.Store(normalizeAddrKey(initiatorAddr.String()), &panelConn{conn: server, userID: 42})

	const relayUUID = "l469-panel-account-uuid"
	srv.handleRelayResponseForward(&pb.RendezvousMessage{
		Union: &pb.RendezvousMessage_RelayResponse{
			RelayResponse: &pb.RelayResponse{
				SocketAddr: cryptopkg.EncodeAddr(initiatorAddr),
				Uuid:       relayUUID,
				Union:      &pb.RelayResponse_Id{Id: "TGTSIGNIN1"},
			},
		},
	}, targetAddr)

	if !relay.ClaimRelayPair(relayUUID) || !relay.ClaimRelayPair(relayUUID) {
		t.Fatal("both sides claim the ticket")
	}
	pair, ok := relay.New(nil).Live(relayUUID)
	if !ok || pair.Account != 42 || pair.Target != "TGTSIGNIN1" || pair.Initiator != panelWebRemoteInitiatorID {
		t.Fatalf("live session %+v %v, want account 42 to TGTSIGNIN1 from %s", pair, ok, panelWebRemoteInitiatorID)
	}
}
