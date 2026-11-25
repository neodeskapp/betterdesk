package api

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/db"
)

// joinKey is a device's key pair and its public key in base64.
type joinKey struct {
	b64     string
	private ed25519.PrivateKey
}

func newJoinKey(t *testing.T) joinKey {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return joinKey{b64: base64.StdEncoding.EncodeToString(pub), private: priv}
}

// proofOf is the key proof a client sends with its join, as the console
// hands it on: made by key for the device id over path at now. The nonce
// gets a random end: the nonces of good proofs are kept process-wide.
func proofOf(key joinKey, deviceID, path, nonce string, at time.Time) map[string]any {
	end := make([]byte, 8)
	_, _ = rand.Read(end)
	nonce += "-" + hex.EncodeToString(end)
	timestamp := at.UTC().Format(time.RFC3339)
	signature := ed25519.Sign(key.private, enrollmentProofPayload(http.MethodPost, path, deviceID, key.b64, timestamp, nonce))
	return map[string]any{
		"path": path, "timestamp": timestamp, "nonce": nonce,
		"signature": base64.StdEncoding.EncodeToString(signature),
	}
}

// joinAs redeems a join code for the device 289485454 with these fields.
func joinAs(t *testing.T, srv *Server, code string, device map[string]any) (int, onboardAnswer) {
	t.Helper()
	device["device_id"] = "289485454"
	return onboardCall(t, srv, http.MethodPost, "/api/onboard/codes/redeem", map[string]any{"code": code, "kind": "join", "device": device})
}

// enrolledLabComputer joins the device 289485454 with key, as the first
// join of a new computer does: without a proof, as older clients join.
func enrolledLabComputer(t *testing.T, srv *Server, key joinKey) {
	t.Helper()
	code := makeOnboardCode(t, srv, map[string]any{"kind": "join", "name": "Anna's laptop", "folder_id": 3}).Code
	status, out := joinAs(t, srv, code, map[string]any{"uuid": "machine-1", "hostname": "anna", "public_key": key.b64})
	if status != http.StatusOK || !out.Device.Created {
		t.Fatalf("first join without a proof: %d %+v", status, out)
	}
}

// TestAJoinWithAnotherDevicesIDAndNoProofIsRefused: anyone with an invite
// who knows an enrolled computer's ID could join under it, which ends the
// computer's device token and moves it into the invite's folder. A join
// for a known ID must prove the key the server bound to it; the invite is
// not spent.
func TestAJoinWithAnotherDevicesIDAndNoProofIsRefused(t *testing.T) {
	onboardStores(t, func(t *testing.T, srv *Server) {
		key := newJoinKey(t)
		enrolledLabComputer(t, srv, key)
		stranger := newJoinKey(t)
		code := makeOnboardCode(t, srv, map[string]any{"kind": "join", "name": "Taken over"}).Code
		now := time.Now()

		for name, device := range map[string]map[string]any{
			"empty key and uuid": {},
			"empty key":          {"uuid": "machine-1"},
			"empty uuid":         {"public_key": key.b64, "proof": proofOf(key, "289485454", "/api/onboard/join", "n-1", now)},
			"its key, no proof":  {"uuid": "machine-1", "public_key": key.b64},
			"its key, signed by another": {"uuid": "machine-1", "public_key": key.b64,
				"proof": proofOf(joinKey{b64: key.b64, private: stranger.private}, "289485454", "/api/onboard/join", "n-2", now)},
			"a proof for another route": {"uuid": "machine-1", "public_key": key.b64,
				"proof": proofOf(key, "289485454", "/api/devices/register", "n-3", now)},
			"an old proof": {"uuid": "machine-1", "public_key": key.b64,
				"proof": proofOf(key, "289485454", "/api/onboard/join", "n-4", now.Add(-10*time.Minute))},
		} {
			status, out := joinAs(t, srv, code, device)
			if status != http.StatusForbidden || out.Error != "device_refused" || out.Message == "" {
				t.Errorf("%s: %d %+v, want 403 device_refused", name, status, out)
			}
		}
		if status, out := onboardCall(t, srv, http.MethodPost, "/api/onboard/codes/lookup", map[string]any{"code": code}); status != http.StatusOK || out.Record.UsesLeft != 1 {
			t.Fatalf("a refused join spent the invite: %d %+v", status, out)
		}
		if p, _ := srv.db.GetPeer("289485454"); p == nil || p.DisplayName != "Anna's laptop" {
			t.Fatalf("a refused join renamed the computer: %+v", p)
		}
		if req := srv.loadDeviceKeyRequest("289485454"); req != nil {
			t.Fatalf("a join with the bound key left a key request: %+v", req)
		}
	})
}

// TestTheComputerItselfRejoinsWithItsProof: the enrolled computer proves
// its key and joins again (another folder, a new name); the same proof a
// second time is a replay.
func TestTheComputerItselfRejoinsWithItsProof(t *testing.T) {
	onboardStores(t, func(t *testing.T, srv *Server) {
		key := newJoinKey(t)
		enrolledLabComputer(t, srv, key)
		code := makeOnboardCode(t, srv, map[string]any{"kind": "join", "name": "Anna's new name", "max_uses": 2}).Code
		proof := proofOf(key, "289485454", "/api/onboard/join", "n-1", time.Now())
		status, out := joinAs(t, srv, code, map[string]any{"uuid": "machine-1", "public_key": key.b64, "proof": proof})
		if status != http.StatusOK || out.Device.Created {
			t.Fatalf("rejoin with its proof: %d %+v", status, out)
		}
		status, out = joinAs(t, srv, code, map[string]any{"uuid": "machine-1", "public_key": key.b64, "proof": proof})
		if status != http.StatusForbidden || out.Error != "device_refused" {
			t.Fatalf("the same proof again: %d %+v", status, out)
		}
	})
}

// TestAKnownComputerWithoutABoundKeyWaitsForAnApproval: a device the
// server knows without a key it could check (registered without one)
// cannot prove anything; its join leaves a key request for an
// administrator instead of getting in.
func TestAKnownComputerWithoutABoundKeyWaitsForAnApproval(t *testing.T) {
	onboardStores(t, func(t *testing.T, srv *Server) {
		if err := srv.db.UpsertPeer(&db.Peer{ID: "289485454", UUID: "machine-1"}); err != nil {
			t.Fatal(err)
		}
		key := newJoinKey(t)
		code := makeOnboardCode(t, srv, map[string]any{"kind": "join"}).Code
		status, out := joinAs(t, srv, code, map[string]any{"uuid": "machine-1", "public_key": key.b64,
			"proof": proofOf(key, "289485454", "/api/onboard/join", "n-1", time.Now())})
		if status != http.StatusForbidden || out.Error != "device_refused" || out.Message != keyChangedRefusal {
			t.Fatalf("join without a bound key: %d %+v", status, out)
		}
		if req := srv.loadDeviceKeyRequest("289485454"); req == nil || req.PublicKey != key.b64 {
			t.Fatalf("no key request: %+v", req)
		}
	})
}

// TestTheRefusalTellsAnOlderClientWhatToDo: the text an older client shows
// after "did not accept this computer:".
func TestTheRefusalTellsAnOlderClientWhatToDo(t *testing.T) {
	onboardStores(t, func(t *testing.T, srv *Server) {
		key := newJoinKey(t)
		enrolledLabComputer(t, srv, key)
		code := makeOnboardCode(t, srv, map[string]any{"kind": "join"}).Code
		_, out := joinAs(t, srv, code, map[string]any{"uuid": "machine-1", "public_key": key.b64})
		if out.Message != joinProofRefusal || !strings.Contains(out.Message, "Update NeoDesk") {
			t.Fatalf("refusal text %q", out.Message)
		}
	})
}
