package signal

import (
	"net"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/billing"
	"github.com/unitronix/betterdesk-server/config"
	cryptopkg "github.com/unitronix/betterdesk-server/crypto"
	"github.com/unitronix/betterdesk-server/db"
	"github.com/unitronix/betterdesk-server/peer"
	pb "github.com/unitronix/betterdesk-server/proto"
	"github.com/unitronix/betterdesk-server/timesync"
)

// addDeviceContract gives deviceID an active billing contract.
func addDeviceContract(t *testing.T, database db.Database, deviceID string) {
	t.Helper()
	if err := database.CreateBillingPackage(&db.BillingPackage{
		ID: "pkg-" + deviceID, Name: "Support", IncludedMinutes: 60, Currency: "EUR",
	}); err != nil {
		t.Fatalf("CreateBillingPackage: %v", err)
	}
	if err := database.CreateBillingContract(&db.BillingContract{
		ID: "contract-" + deviceID, TargetType: db.BillingTargetDevice, TargetKey: deviceID,
		PackageID: "pkg-" + deviceID, Status: billing.ContractActive, RemainingMinutes: 60, Currency: "EUR",
	}); err != nil {
		t.Fatalf("CreateBillingContract: %v", err)
	}
}

// refusingBilling refuses every session to a device with a contract: the
// clock must be synced and has not been checked yet.
func refusingBilling(database db.Database) *billing.Service {
	clock := timesync.NewService(nil, timesync.Config{RequireSync: true})
	return billing.NewService(database, clock, 1, false)
}

func TestBillingRefusesRelayRequestsOverTCPAndWebSocket(t *testing.T) {
	for _, hint := range []peer.ConnType{peer.ConnTCP, peer.ConnWS} {
		t.Run(hint.String(), func(t *testing.T) {
			srv, database := newTestSignalServer(t, config.EnrollmentModeOpen)
			addDeviceContract(t, database, "BILLTGT01")
			srv.SetBillingService(refusingBilling(database))
			putOnlinePeer(srv, "BILLTGT01", "198.51.100.61", 52000, peer.ConnUDP)
			putOnlinePeer(srv, "BILLINI01", "203.0.113.61", 41000, peer.ConnTCP)

			resp := srv.handleRequestRelayTCP(
				&pb.RequestRelay{Id: "BILLTGT01", Uuid: "billing-refused-relay-" + hint.String()},
				udpAddr("203.0.113.61", 41000), hint,
			)
			rr := resp.GetRelayResponse()
			if rr == nil || rr.RefuseReason != "Billing suspended" {
				t.Fatalf("RequestRelay over %s = %+v, want refused for billing", hint, resp)
			}
		})
	}
}

func TestBillingRefusesPunchHoleOverTCP(t *testing.T) {
	srv, database := newTestSignalServer(t, config.EnrollmentModeOpen)
	srv.cfg.P2PFirst = false
	addDeviceContract(t, database, "BILLTGT02")
	srv.SetBillingService(refusingBilling(database))
	putOnlinePeer(srv, "BILLTGT02", "198.51.100.62", 52000, peer.ConnUDP)
	putOnlinePeer(srv, "BILLINI02", "203.0.113.62", 41000, peer.ConnTCP)

	resp := srv.handlePunchHoleRequestTCPWithHint(
		&pb.PunchHoleRequest{Id: "BILLTGT02"}, udpAddr("203.0.113.62", 41000), peer.ConnWS,
	)
	phr := resp.GetPunchHoleResponse()
	if phr == nil || phr.Failure != pb.PunchHoleResponse_OFFLINE {
		t.Fatalf("PunchHole over WebSocket = %+v, want OFFLINE for billing", resp)
	}
}

func TestBillingMetersRelaySessionsWhateverSignalledThem(t *testing.T) {
	t.Run("forced relay of a udp punch", func(t *testing.T) {
		srv, database := newTestSignalServer(t, config.EnrollmentModeOpen)
		addDeviceContract(t, database, "BILLTGT03")
		b := billing.NewService(database, nil, 1, false)
		srv.SetBillingService(b)
		_, targetAddr := attachTestUDPPair(t, srv)
		srv.peers.Put(&peer.Entry{
			ID: "BILLTGT03", IP: targetAddr.String(), UDPAddr: targetAddr,
			ConnType: peer.ConnUDP, LastReg: time.Now(), StatusTier: peer.StatusOnline,
		})
		initiator, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { initiator.Close() })
		initiatorAddr := initiator.LocalAddr().(*net.UDPAddr)
		putOnlinePeer(srv, "BILLINI03", "127.0.0.1", initiatorAddr.Port, peer.ConnUDP)

		srv.handlePunchHoleRequest(&pb.PunchHoleRequest{Id: "BILLTGT03", ForceRelay: true}, initiatorAddr)
		if rr := readUDPRendezvous(t, initiator).GetRelayResponse(); rr == nil || rr.Uuid == "" {
			t.Fatalf("forced relay answered %+v, want a RelayResponse with a uuid", rr)
		}
		if got := b.PendingRelayCount(); got != 1 {
			t.Fatalf("%d sessions waiting for the relay, want 1", got)
		}
	})

	t.Run("tcp request relay", func(t *testing.T) {
		srv, database := newTestSignalServer(t, config.EnrollmentModeOpen)
		addDeviceContract(t, database, "BILLTGT04")
		b := billing.NewService(database, nil, 1, false)
		srv.SetBillingService(b)
		putOnlinePeer(srv, "BILLTGT04", "198.51.100.64", 52000, peer.ConnUDP)
		putOnlinePeer(srv, "BILLINI04", "203.0.113.64", 41000, peer.ConnTCP)

		resp := srv.handleRequestRelayTCP(
			&pb.RequestRelay{Id: "BILLTGT04", Uuid: "billing-metered-tcp-relay"},
			udpAddr("203.0.113.64", 41000), peer.ConnTCP,
		)
		if rr := resp.GetRelayResponse(); rr == nil || rr.RefuseReason != "" {
			t.Fatalf("RequestRelay = %+v", resp)
		}
		if got := b.PendingRelayCount(); got != 1 {
			t.Fatalf("%d sessions waiting for the relay, want 1", got)
		}
	})

	t.Run("relay response from the target", func(t *testing.T) {
		srv, database := newTestSignalServer(t, config.EnrollmentModeOpen)
		addDeviceContract(t, database, "BILLTGT05")
		b := billing.NewService(database, nil, 1, false)
		srv.SetBillingService(b)
		initiatorAddr := udpAddr("198.51.100.65", 58600)
		targetAddr := udpAddr("203.0.113.65", 59000)
		putOnlinePeer(srv, "BILLINI05", initiatorAddr.IP.String(), initiatorAddr.Port, peer.ConnTCP)
		putOnlinePeer(srv, "BILLTGT05", targetAddr.IP.String(), targetAddr.Port, peer.ConnTCP)

		srv.handleRelayResponseForward(&pb.RendezvousMessage{
			Union: &pb.RendezvousMessage_RelayResponse{
				RelayResponse: &pb.RelayResponse{
					SocketAddr: cryptopkg.EncodeAddr(initiatorAddr),
					Uuid:       "billing-metered-target-relay",
					Union:      &pb.RelayResponse_Id{Id: "BILLTGT05"},
				},
			},
		}, targetAddr)
		if got := b.PendingRelayCount(); got != 1 {
			t.Fatalf("%d sessions waiting for the relay, want 1", got)
		}
	})
}

// A billing session the console's web client opens names the signed-in
// account as its operator (operator_name), so the work report rule "own
// reports only" can tell whose session it is; a guest's session names no
// account (BUGS L157, DECISIONS A168).
func TestBillingSessionsNameTheConsoleAccount(t *testing.T) {
	srv, database := newTestSignalServer(t, config.EnrollmentModeManaged)
	_, targetAddr := attachTestUDPPair(t, srv)
	b := billing.NewService(database, nil, 1, false)
	srv.SetBillingService(b)
	operator := &db.User{Username: "anna", PasswordHash: "x", Role: "operator"}
	if err := database.CreateUser(operator); err != nil || operator.ID == 0 {
		t.Fatalf("CreateUser: %v (id %d)", err, operator.ID)
	}
	addr := serveTCPOnLoopback(t, srv)

	for _, c := range []struct {
		target string
		proof  []byte
		want   string
	}{
		{"BILLWEB01", srv.PanelProofFor(operator.ID), "anna"},
		{"BILLWEB02", srv.PanelProof(), ""},
	} {
		putOnlinePeer(srv, c.target, targetAddr.IP.String(), targetAddr.Port, peer.ConnUDP)
		addDeviceContract(t, database, c.target)
		if rr, err := relayRequestOverTCP(t, addr, c.proof, c.target); err != nil || rr.RefuseReason != "" {
			t.Fatalf("%s: relay request %+v, %v", c.target, rr, err)
		}
		b.ActivateRelay("panel-proof-" + c.target)
		sess, err := database.GetBillingSessionByRelayUUID("panel-proof-" + c.target)
		if err != nil || sess == nil {
			t.Fatalf("%s: no billing session (%v)", c.target, err)
		}
		if sess.OperatorName != c.want || sess.OperatorID != panelWebRemoteInitiatorID {
			t.Errorf("%s: operator %q, name %q; want %q, %q", c.target, sess.OperatorID, sess.OperatorName, panelWebRemoteInitiatorID, c.want)
		}
	}
}
