package api

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/audit"
	"github.com/neodeskapp/neodesk/neodesk-server/config"
	"github.com/neodeskapp/neodesk/neodesk-server/db"
	"github.com/neodeskapp/neodesk/neodesk-server/peer"
)

// deployMachine is what `rustdesk --deploy` sends for one computer: its
// machine uuid and its Ed25519 public key.
type deployMachine struct {
	uuid []byte
	pk   []byte
}

func newDeployMachine(seed byte) deployMachine {
	return deployMachine{
		uuid: []byte("4c4c4544-0042-3510-8052-b" + strings.Repeat(string(rune('0'+seed%10)), 11)),
		pk:   bytes.Repeat([]byte{seed}, 32),
	}
}

func deployFixture(t *testing.T, mode string) (*Server, *audit.Logger) {
	t.Helper()
	database := testSetupDB(t)
	t.Cleanup(func() { database.Close() })
	cfg := config.DefaultConfig()
	cfg.EnrollmentMode = mode
	srv := New(cfg, database, peer.NewMap(), nil, "test")
	logger := audit.NewLogger("")
	srv.SetAuditLogger(logger)
	return srv, logger
}

// addDeployToken stores a device token as the console's Device tokens
// page makes it and returns its value.
func addDeployToken(t *testing.T, srv *Server, edit func(*db.DeviceToken)) string {
	t.Helper()
	value, err := generateSecureToken(32)
	if err != nil {
		t.Fatal(err)
	}
	tok := &db.DeviceToken{
		Token:     value,
		TokenHash: hashToken(value),
		Name:      "rollout",
		Status:    db.TokenStatusPending,
		MaxUses:   1,
	}
	if edit != nil {
		edit(tok)
	}
	if err := srv.db.CreateDeviceToken(tok); err != nil {
		t.Fatal(err)
	}
	return value
}

func deploy(t *testing.T, srv *Server, token, id string, m deployMachine) (int, string) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{
		"id":   id,
		"uuid": base64.StdEncoding.EncodeToString(m.uuid),
		"pk":   base64.StdEncoding.EncodeToString(m.pk),
	})
	req := httptest.NewRequest(http.MethodPost, "/api/devices/deploy", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.RemoteAddr = "198.51.100.20:50000"
	rec := httptest.NewRecorder()
	srv.ListenerHandler().ServeHTTP(rec, req)
	var answer struct {
		Result string `json:"result"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &answer)
	return rec.Code, answer.Result
}

func getPeer(t *testing.T, srv *Server, id string) *db.Peer {
	t.Helper()
	p, err := srv.db.GetPeer(id)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// A waiting RustDesk client deployed with a device token is approved: the
// peer row binds the key and uuid it sent, the token is bound to the id
// and used once, the request leaves Join requests and the audit log names
// the token.
func TestDeployApprovesAWaitingDeviceAndBindsItsKey(t *testing.T) {
	for _, mode := range []string{config.EnrollmentModeManaged, config.EnrollmentModeLocked} {
		t.Run(mode, func(t *testing.T) {
			srv, logger := deployFixture(t, mode)
			token := addDeployToken(t, srv, nil)
			m := newDeployMachine(1)
			if err := srv.db.SetConfig(pendingDevicePrefix+"123456789", `{"device_id":"123456789","hostname":"office-pc"}`); err != nil {
				t.Fatal(err)
			}

			if code, result := deploy(t, srv, token, "123456789", m); code != http.StatusOK || result != deployOK {
				t.Fatalf("deploy = %d %q, want 200 OK", code, result)
			}
			p := getPeer(t, srv, "123456789")
			if p == nil {
				t.Fatal("deployed device has no peer row")
			}
			if !bytes.Equal(p.PK, m.pk) {
				t.Fatalf("peer pk = %x, want the deployed key %x", p.PK, m.pk)
			}
			if p.UUID != hex.EncodeToString(m.uuid) {
				t.Fatalf("peer uuid = %q, want %q", p.UUID, hex.EncodeToString(m.uuid))
			}
			if p.Hostname != "office-pc" {
				t.Fatalf("peer hostname = %q, want the waiting request's office-pc", p.Hostname)
			}
			if raw, _ := srv.db.GetConfig(pendingDevicePrefix + "123456789"); raw != "" {
				t.Fatalf("the waiting request stayed: %s", raw)
			}
			tok, _ := srv.db.GetDeviceTokenByHash(hashToken(token))
			if tok.PeerID != "123456789" || tok.Status != db.TokenStatusActive || tok.UseCount != 1 {
				t.Fatalf("token peer %q status %q uses %d, want bound, active, 1", tok.PeerID, tok.Status, tok.UseCount)
			}
			events := logger.RecentByAction("device_enrolled", 5)
			if len(events) != 1 || events[0].Target != "123456789" || events[0].Details["method"] != "deploy" || events[0].Details["token_name"] != "rollout" {
				t.Fatalf("audit = %+v, want one device_enrolled for 123456789 by deploy with token rollout", events)
			}
		})
	}
}

// A token can't take an id that another machine holds: a different key
// or uuid answers ID_TAKEN and the stored identity stays.
func TestDeployRefusesAnIDBoundToAnotherMachine(t *testing.T) {
	srv, logger := deployFixture(t, config.EnrollmentModeManaged)
	owner := newDeployMachine(2)
	if err := srv.db.UpsertPeer(&db.Peer{ID: "VICTIM01", UUID: hex.EncodeToString(owner.uuid), PK: owner.pk, Status: "ONLINE"}); err != nil {
		t.Fatal(err)
	}
	attacker := newDeployMachine(3)
	sameUUIDOtherKey := deployMachine{uuid: owner.uuid, pk: attacker.pk}
	sameKeyOtherUUID := deployMachine{uuid: attacker.uuid, pk: owner.pk}

	for name, m := range map[string]deployMachine{"other machine": attacker, "other key": sameUUIDOtherKey, "other uuid": sameKeyOtherUUID} {
		t.Run(name, func(t *testing.T) {
			token := addDeployToken(t, srv, nil)
			if code, result := deploy(t, srv, token, "VICTIM01", m); code != http.StatusConflict || result != deployIDTaken {
				t.Fatalf("deploy = %d %q, want 409 ID_TAKEN", code, result)
			}
			p := getPeer(t, srv, "VICTIM01")
			if !bytes.Equal(p.PK, owner.pk) || p.UUID != hex.EncodeToString(owner.uuid) {
				t.Fatalf("the stored identity changed: pk %x uuid %q", p.PK, p.UUID)
			}
			if tok, _ := srv.db.GetDeviceTokenByHash(hashToken(token)); tok.UseCount != 0 || tok.PeerID != "" {
				t.Fatalf("a refused deploy used the token: uses %d, peer %q", tok.UseCount, tok.PeerID)
			}
		})
	}
	if got := logger.RecentByAction(audit.ActionPeerRegistrationRejected, 10); len(got) != 3 || got[0].Details["reason"] != "id_taken" {
		t.Fatalf("audit = %+v, want three id_taken refusals", got)
	}
}

// An id waiting for approval from another machine is taken as well.
func TestDeployRefusesAnIDWaitingFromAnotherMachine(t *testing.T) {
	srv, _ := deployFixture(t, config.EnrollmentModeManaged)
	waiting := newDeployMachine(4)
	request := `{"device_id":"WAITING01","register_uuid":"` + hex.EncodeToString(waiting.uuid) + `"}`
	if err := srv.db.SetConfig(pendingDevicePrefix+"WAITING01", request); err != nil {
		t.Fatal(err)
	}
	token := addDeployToken(t, srv, nil)
	if code, result := deploy(t, srv, token, "WAITING01", newDeployMachine(5)); code != http.StatusConflict || result != deployIDTaken {
		t.Fatalf("deploy from another machine = %d %q, want 409 ID_TAKEN", code, result)
	}
	if p := getPeer(t, srv, "WAITING01"); p != nil {
		t.Fatal("a refused deploy created the peer")
	}
	if code, result := deploy(t, srv, token, "WAITING01", waiting); code != http.StatusOK || result != deployOK {
		t.Fatalf("deploy from the waiting machine = %d %q, want 200 OK", code, result)
	}
}

// A known device with the same identity answers OK without using the
// token again.
func TestDeployOfAKnownDeviceChangesNothing(t *testing.T) {
	srv, _ := deployFixture(t, config.EnrollmentModeManaged)
	m := newDeployMachine(6)
	if err := srv.db.UpsertPeer(&db.Peer{ID: "KNOWN001", UUID: hex.EncodeToString(m.uuid), PK: m.pk, Status: "ONLINE"}); err != nil {
		t.Fatal(err)
	}
	token := addDeployToken(t, srv, func(tok *db.DeviceToken) { tok.MaxUses = 0 })
	if code, result := deploy(t, srv, token, "KNOWN001", m); code != http.StatusOK || result != deployOK {
		t.Fatalf("deploy = %d %q, want 200 OK", code, result)
	}
	if tok, _ := srv.db.GetDeviceTokenByHash(hashToken(token)); tok.UseCount != 0 {
		t.Fatalf("token uses = %d, want 0", tok.UseCount)
	}
}

// One token deploys one id, as it joins one device at registration.
func TestDeployTokenWorksForOneIDOnly(t *testing.T) {
	srv, _ := deployFixture(t, config.EnrollmentModeManaged)
	token := addDeployToken(t, srv, func(tok *db.DeviceToken) { tok.MaxUses = 0 })
	if code, result := deploy(t, srv, token, "FIRST0001", newDeployMachine(7)); code != http.StatusOK || result != deployOK {
		t.Fatalf("first deploy = %d %q, want 200 OK", code, result)
	}
	if code, _ := deploy(t, srv, token, "SECOND001", newDeployMachine(8)); code != http.StatusForbidden {
		t.Fatalf("second id = %d, want 403", code)
	}
	if p := getPeer(t, srv, "SECOND001"); p != nil {
		t.Fatal("the token deployed a second id")
	}

	// An active token without a device (not made by the console) binds
	// nothing, so it deploys nothing.
	loose := addDeployToken(t, srv, func(tok *db.DeviceToken) { tok.Status = db.TokenStatusActive; tok.MaxUses = 0 })
	if code, _ := deploy(t, srv, loose, "THIRD0001", newDeployMachine(9)); code != http.StatusForbidden {
		t.Fatalf("active unbound token = %d, want 403", code)
	}
	if p := getPeer(t, srv, "THIRD0001"); p != nil {
		t.Fatal("an active unbound token deployed a device")
	}
}

// Without a usable token nothing is written.
func TestDeployRefusesTokensThatDontWork(t *testing.T) {
	srv, logger := deployFixture(t, config.EnrollmentModeManaged)
	past := time.Now().Add(-time.Hour)
	tokens := map[string]string{
		"none":            "",
		"unknown":         "not-a-token",
		"revoked":         addDeployToken(t, srv, func(tok *db.DeviceToken) { tok.Status = db.TokenStatusRevoked }),
		"expired":         addDeployToken(t, srv, func(tok *db.DeviceToken) { tok.ExpiresAt = &past }),
		"used up":         addDeployToken(t, srv, func(tok *db.DeviceToken) { tok.UseCount = 1 }),
		"bound elsewhere": addDeployToken(t, srv, func(tok *db.DeviceToken) { tok.Status = db.TokenStatusActive; tok.PeerID = "OTHER0001" }),
	}
	for name, token := range tokens {
		t.Run(name, func(t *testing.T) {
			code, result := deploy(t, srv, token, "NEWDEV001", newDeployMachine(10))
			if code != http.StatusUnauthorized && code != http.StatusForbidden {
				t.Fatalf("deploy = %d %q, want 401 or 403", code, result)
			}
			if result == deployOK {
				t.Fatal("deploy answered OK")
			}
			if p := getPeer(t, srv, "NEWDEV001"); p != nil {
				t.Fatal("a refused deploy created the peer")
			}
		})
	}
	if got := logger.RecentByAction(audit.ActionPeerRegistrationRejected, 10); len(got) != len(tokens) {
		t.Fatalf("audit has %d refusals, want %d", len(got), len(tokens))
	}
}

// Removed, banned and rejected ids stay out.
func TestDeployKeepsTheAdminsDecisions(t *testing.T) {
	srv, _ := deployFixture(t, config.EnrollmentModeManaged)
	m := newDeployMachine(11)
	for _, id := range []string{"REMOVED01", "BANNED001"} {
		if err := srv.db.UpsertPeer(&db.Peer{ID: id, UUID: hex.EncodeToString(m.uuid), PK: m.pk, Status: "OFFLINE"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := srv.db.DeletePeer("REMOVED01"); err != nil {
		t.Fatal(err)
	}
	if err := srv.db.BanPeer("BANNED001", "test"); err != nil {
		t.Fatal(err)
	}
	if err := srv.db.SetConfig(rejectedDevicePrefix+"REJECTED1", `{"device_id":"REJECTED1"}`); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"REMOVED01", "BANNED001", "REJECTED1"} {
		token := addDeployToken(t, srv, nil)
		if code, result := deploy(t, srv, token, id, m); code != http.StatusForbidden {
			t.Fatalf("%s: deploy = %d %q, want 403", id, code, result)
		}
	}
	if p := getPeer(t, srv, "REJECTED1"); p != nil {
		t.Fatal("a rejected id was deployed")
	}
}

// Open mode admits every device, so a deploy isn't needed.
func TestDeployInOpenModeIsNotEnabled(t *testing.T) {
	srv, _ := deployFixture(t, config.EnrollmentModeOpen)
	token := addDeployToken(t, srv, nil)
	if code, result := deploy(t, srv, token, "OPENDEV01", newDeployMachine(12)); code != http.StatusOK || result != deployNotEnabled {
		t.Fatalf("deploy = %d %q, want 200 NOT_ENABLED", code, result)
	}
	if tok, _ := srv.db.GetDeviceTokenByHash(hashToken(token)); tok.UseCount != 0 || tok.PeerID != "" {
		t.Fatal("open mode used the token")
	}
}

// Ids the signal server refuses and keys that aren't Ed25519 keys are
// INVALID_INPUT, and the token stays unused.
func TestDeployRefusesInvalidInput(t *testing.T) {
	srv, _ := deployFixture(t, config.EnrollmentModeManaged)
	token := addDeployToken(t, srv, nil)
	good := newDeployMachine(13)
	cases := map[string]struct {
		id string
		m  deployMachine
	}{
		"short id":  {"12345", good},
		"markup id": {"<b>123456", good},
		"short key": {"VALID0001", deployMachine{uuid: good.uuid, pk: good.pk[:16]}},
		"no uuid":   {"VALID0001", deployMachine{pk: good.pk}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if code, result := deploy(t, srv, token, c.id, c.m); code != http.StatusBadRequest || result != deployInvalidInput {
				t.Fatalf("deploy = %d %q, want 400 INVALID_INPUT", code, result)
			}
		})
	}
	if tok, _ := srv.db.GetDeviceTokenByHash(hashToken(token)); tok.UseCount != 0 || tok.PeerID != "" {
		t.Fatal("invalid input used the token")
	}
}

// The signal server stores a RegisterPk's uuid as hex of its bytes, after
// decoding a uuid that is itself hex (a Linux machine id).
func TestRendezvousUUIDHexMatchesTheSignalServer(t *testing.T) {
	linux := "0f1e2d3c4b5a69788796a5b4c3d2e1f0"
	if got := rendezvousUUIDHex([]byte(linux)); got != linux {
		t.Fatalf("linux machine id = %q, want %q", got, linux)
	}
	windows := "4C4C4544-0042-3510-8052-B4C04F4E3632"
	if got := rendezvousUUIDHex([]byte(windows)); got != hex.EncodeToString([]byte(windows)) {
		t.Fatalf("windows uuid = %q, want hex of its text", got)
	}
}
