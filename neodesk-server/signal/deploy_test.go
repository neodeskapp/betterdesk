package signal

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/api"
	"github.com/neodeskapp/neodesk/neodesk-server/config"
	"github.com/neodeskapp/neodesk/neodesk-server/db"
	"github.com/neodeskapp/neodesk/neodesk-server/peer"
	pb "github.com/neodeskapp/neodesk/neodesk-server/proto"
)

// A RustDesk client that waits for deployment, then runs
// `rustdesk --deploy --token <device token>`, is accepted at its next
// RegisterPk with its own key, and the id refuses any other key, as for a
// device approved in the console.
func TestDeployedDeviceRegistersWithItsKeyOnly(t *testing.T) {
	machines := map[string][]byte{
		"windows uuid":     []byte("4C4C4544-0042-3510-8052-B4C04F4E3632"),
		"linux machine id": []byte("0f1e2d3c4b5a69788796a5b4c3d2e1f0"),
	}
	for name, uuid := range machines {
		t.Run(name, func(t *testing.T) {
			srv, database := newTestSignalServer(t, config.EnrollmentModeManaged)
			apiCfg := config.DefaultConfig()
			apiCfg.EnrollmentMode = config.EnrollmentModeManaged
			apiSrv := api.New(apiCfg, database, peer.NewMap(), nil, "test")

			const id, addr = "DEPLOY001", "198.51.100.90:51000"
			pk := bytes.Repeat([]byte{0x5a}, 32)
			register := &pb.RegisterPk{Id: id, Uuid: uuid, Pk: pk}
			if got := registerPkResult(srv.processRegisterPk(register, addr)); got != pb.RegisterPkResponse_NOT_DEPLOYED {
				t.Fatalf("RegisterPk before the deploy = %v, want NOT_DEPLOYED", got)
			}

			token := "deploy-token-for-the-signal-test-0001"
			sum := sha256.Sum256([]byte(token))
			if err := database.CreateDeviceToken(&db.DeviceToken{
				Token: token, TokenHash: hex.EncodeToString(sum[:]), Name: "rollout", Status: db.TokenStatusPending, MaxUses: 1,
			}); err != nil {
				t.Fatal(err)
			}
			body, _ := json.Marshal(map[string]string{
				"id":   id,
				"uuid": base64.StdEncoding.EncodeToString(uuid),
				"pk":   base64.StdEncoding.EncodeToString(pk),
			})
			req := httptest.NewRequest(http.MethodPost, "/api/devices/deploy", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+token)
			req.RemoteAddr = "198.51.100.90:52000"
			rec := httptest.NewRecorder()
			apiSrv.ListenerHandler().ServeHTTP(rec, req)
			if rec.Code != http.StatusOK || !bytes.Contains(rec.Body.Bytes(), []byte(`"result":"OK"`)) {
				t.Fatalf("deploy = %d %s, want 200 OK", rec.Code, rec.Body.String())
			}

			// Another machine that knows the id and uuid comes first: the
			// deploy bound the key, so it is refused.
			other := &pb.RegisterPk{Id: id, Uuid: uuid, Pk: bytes.Repeat([]byte{0x6b}, 32)}
			if got := registerPkResult(srv.processRegisterPk(other, "203.0.113.7:40000")); got == pb.RegisterPkResponse_OK {
				t.Fatal("RegisterPk with another key was accepted for the deployed id")
			}
			if got := registerPkResult(srv.processRegisterPk(&pb.RegisterPk{Id: id, Uuid: uuid, Pk: pk}, addr)); got != pb.RegisterPkResponse_OK {
				t.Fatalf("RegisterPk after the deploy = %v, want OK", got)
			}
			otherUUID := &pb.RegisterPk{Id: id, Uuid: []byte("another-machine-uuid-0000000000000"), Pk: pk}
			if got := registerPkResult(srv.processRegisterPk(otherUUID, "203.0.113.8:40000")); got != pb.RegisterPkResponse_UUID_MISMATCH {
				t.Fatalf("RegisterPk with another uuid = %v, want UUID_MISMATCH", got)
			}
		})
	}
}
