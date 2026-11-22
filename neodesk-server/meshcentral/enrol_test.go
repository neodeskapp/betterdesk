package meshcentral

import (
	"context"
	"crypto/rsa"
	"encoding/binary"
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/unitronix/betterdesk-server/db"
)

// agentHandshake runs the agent side of the handshake with a new agent key,
// a mesh id and a host name. It returns the open socket and true when the
// server confirmed the agent, or false when it closed the socket instead.
func agentHandshake(t *testing.T, ctx context.Context, url string, meshRaw []byte, hostname string) (*websocket.Conn, bool) {
	t.Helper()
	agentKey, agentCert, _ := generateAgentKeyAndCert(t)
	return agentHandshakeAs(t, ctx, url, agentKey, agentCert, meshRaw, hostname)
}

// agentHandshakeAs is agentHandshake with the agent key and certificate of
// an agent the test made.
func agentHandshakeAs(t *testing.T, ctx context.Context, url string, agentKey *rsa.PrivateKey, agentCert, meshRaw []byte, hostname string) (*websocket.Conn, bool) {
	t.Helper()
	conn, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	conn.SetReadLimit(-1)
	t.Cleanup(func() { conn.CloseNow() })
	read := func(want uint16) ([]byte, bool) {
		for {
			typ, data, err := conn.Read(ctx)
			if err != nil {
				return nil, false
			}
			if typ == websocket.MessageBinary && len(data) >= 2 && readU16(data, 0) == want {
				return data, true
			}
		}
	}
	webHash := make([]byte, sha384Size)
	agentNonce := random48()
	authReq := make([]byte, 98)
	binary.BigEndian.PutUint16(authReq[0:2], cmdAuthRequest)
	copy(authReq[50:98], agentNonce)
	if err := conn.Write(ctx, websocket.MessageBinary, authReq); err != nil {
		t.Fatal(err)
	}
	resp, ok := read(cmdAuthRequest)
	if !ok {
		t.Fatal("no auth request answer")
	}
	serverNonce := append([]byte(nil), resp[50:98]...)
	if _, ok := read(cmdAuthVerify); !ok {
		t.Fatal("no auth verify")
	}
	sig := signAgent(agentKey, webHash, serverNonce, agentNonce)
	verify := make([]byte, 4+len(agentCert)+len(sig))
	binary.BigEndian.PutUint16(verify[0:2], cmdAuthVerify)
	binary.BigEndian.PutUint16(verify[2:4], uint16(len(agentCert)))
	copy(verify[4:], agentCert)
	copy(verify[4+len(agentCert):], sig)
	if err := conn.Write(ctx, websocket.MessageBinary, verify); err != nil {
		t.Fatal(err)
	}
	if err := conn.Write(ctx, websocket.MessageBinary, buildAuthInfoPacket(meshRaw, hostname)); err != nil {
		t.Fatal(err)
	}
	_, ok = read(cmdAuthConfirm)
	return conn, ok
}

// approveAgent creates the device of an agent as approving its pending
// request does.
func approveAgent(t *testing.T, gw *Gateway, nodeID string) {
	t.Helper()
	if err := gw.db.UpsertPeer(&db.Peer{ID: meshPeerID(nodeID), UUID: nodeID, DeviceType: "mesh_agent", OS: "mesh", Status: "OFFLINE"}); err != nil {
		t.Fatal(err)
	}
}

// meshAgents lists the peer rows of mesh agents.
func meshAgents(t *testing.T, gw *Gateway) []*db.Peer {
	t.Helper()
	peers, err := gw.db.ListPeers(false)
	if err != nil {
		t.Fatal(err)
	}
	var out []*db.Peer
	for _, p := range peers {
		if p.DeviceType == "mesh_agent" {
			out = append(out, p)
		}
	}
	return out
}

// TestAgentNeedsTheMeshIDOfAGroup: an agent comes in only with the mesh id
// of a stored group (the one its .msh file carries). Its host name is
// bounded, it is linked to no device of the same host name, and a big
// message is not stored (BUGS RV156).
func TestAgentNeedsTheMeshIDOfAGroup(t *testing.T) {
	gw, srv := newTestGateway(t)
	groupID, err := NewMeshIDHex()
	if err != nil {
		t.Fatal(err)
	}
	if err := gw.SaveGroups([]MeshGroup{{ID: "default", Name: "Default", MeshID: groupID}}); err != nil {
		t.Fatal(err)
	}
	if err := gw.db.UpsertPeer(&db.Peer{ID: "900100002", Hostname: "office-pc", Status: "ONLINE"}); err != nil {
		t.Fatal(err)
	}
	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/agent.ashx"
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	other, _ := NewMeshIDHex()
	otherRaw, _ := hex.DecodeString(other)
	if _, ok := agentHandshake(t, ctx, url, otherRaw, "office-pc"); ok {
		t.Error("an agent with a mesh id of no group was confirmed")
	}
	legacy := make([]byte, 48)
	copy(legacy, otherRaw[:16])
	if _, ok := agentHandshake(t, ctx, url, legacy, "office-pc"); ok {
		t.Error("an agent with a short mesh id of no group was confirmed")
	}
	if got := meshAgents(t, gw); len(got) != 0 {
		t.Fatalf("refused agents left rows: %+v", got[0])
	}

	raw, _ := hex.DecodeString(groupID)
	key, cert, nodeID := generateAgentKeyAndCert(t)
	approveAgent(t, gw, nodeID)
	conn, ok := agentHandshakeAs(t, ctx, url, key, cert, raw, "office-pc"+strings.Repeat("x", 300))
	if !ok {
		t.Fatal("an agent with the group's mesh id was refused")
	}
	got := meshAgents(t, gw)
	if len(got) != 1 {
		t.Fatalf("%d mesh agent rows, want 1", len(got))
	}
	agent := got[0]
	if len(agent.Hostname) > maxHostname || !strings.HasPrefix(agent.Hostname, "office-pc") {
		t.Errorf("host name %q (%d bytes), want at most %d bytes", agent.Hostname[:20], len(agent.Hostname), maxHostname)
	}
	if agent.LinkedPeerID != "" {
		t.Errorf("the agent was linked to %s", agent.LinkedPeerID)
	}

	small := `{"action":"netinfo","mac":"00:11:22:33:44:55"}`
	big := `{"action":"netinfo","pad":"` + strings.Repeat("a", 1<<20) + `"}`
	for _, msg := range []string{small, big} {
		if err := conn.Write(ctx, websocket.MessageText, []byte(msg)); err != nil {
			t.Fatal(err)
		}
	}
	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"action":"ping"}`)); err != nil {
		t.Fatal(err)
	}
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) == `{"action":"pong"}` {
			break
		}
	}
	stored, _ := gw.db.GetConfig("mesh_last_msg_" + agent.ID)
	if !strings.Contains(stored, "00:11:22:33:44:55") {
		t.Errorf("stored message %.80q, want the small one", stored)
	}
}

// TestKnownMeshWantsZerosAfterAShortID: a group's 32-byte mesh id matches
// the 48 bytes an agent sends only when the rest are zeros (BUGS RV156).
func TestKnownMeshWantsZerosAfterAShortID(t *testing.T) {
	gw, _ := newTestGateway(t)
	short := strings.Repeat("AB", 32)
	if err := gw.SaveGroups([]MeshGroup{{ID: "default", Name: "Default", MeshID: short}}); err != nil {
		t.Fatal(err)
	}
	raw := make([]byte, 48)
	id, _ := hex.DecodeString(short)
	copy(raw, id)
	if !gw.knownMesh(raw) {
		t.Fatal("the group's id followed by zeros was refused")
	}
	raw[40] = 1
	if gw.knownMesh(raw) {
		t.Fatal("the group's id followed by other bytes was taken")
	}
}
