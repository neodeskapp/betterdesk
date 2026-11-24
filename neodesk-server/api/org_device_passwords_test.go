package api

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/config"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
	"github.com/neodeskapp/neodesk/neodesk-server/crypto/devicevault"
	"github.com/neodeskapp/neodesk/neodesk-server/crypto/peervault"
	"github.com/neodeskapp/neodesk/neodesk-server/db"
	"github.com/neodeskapp/neodesk/neodesk-server/peer"
)

// The old key of organizations' device passwords: in a real install
// ORG_PEER_VAULT_KEY or the JWT secret, which the database holds.
const oldOrgVaultSecret = "jwt-secret-in-the-database-0123456789abcdef"

// TestOrgDevicePasswordsMoveToTheKeyFile: at start the server moves the
// device passwords organizations stored with the old derived key to the
// key file, sealed for their organization and device; a second start
// changes nothing; a row the old key does not open stays as it was. After
// the move neither the old key (which a database copy or a full backup
// holds) nor another server's key file opens them, the right key file
// does, a value moved to another device does not open, and the viewer's
// answer and the RustDesk address book still work. On SQLite and
// PostgreSQL.
func TestOrgDevicePasswordsMoveToTheKeyFile(t *testing.T) {
	storetest.Each(t, func(t *testing.T, sdb *store.DB) {
		database := storetest.GoServer(t, sdb)
		for _, org := range []string{"org-x", "org-y"} {
			if err := database.CreateOrganization(&db.Organization{ID: org, Name: org, Slug: org, CreatedAt: time.Now().UTC()}); err != nil {
				t.Fatal(err)
			}
		}
		old, err := peervault.New(oldOrgVaultSecret)
		if err != nil {
			t.Fatal(err)
		}
		seedOld := func(org, peerID, pw string) {
			t.Helper()
			n, c, kid, err := old.Seal(pw)
			if err != nil {
				t.Fatal(err)
			}
			if err := database.SaveOrgPeerCredential(&db.OrgPeerCredential{OrgID: org, PeerID: peerID, Ciphertext: c, Nonce: n, KeyID: kid, UpdatedBy: "before"}); err != nil {
				t.Fatal(err)
			}
		}
		seedOld("org-x", "MOVEPEER1", "old-key-pass-1")
		seedOld("org-y", "MOVEPEER2", "old-key-pass-2")
		// Sealed with a key nobody has any more: it stays as it is
		other, _ := peervault.New("a-key-this-server-never-had-0123456789")
		n, c, kid, _ := other.Seal("lost-pass")
		if err := database.SaveOrgPeerCredential(&db.OrgPeerCredential{OrgID: "org-x", PeerID: "LOSTPEER1", Ciphertext: c, Nonce: n, KeyID: kid}); err != nil {
			t.Fatal(err)
		}

		keyDir := t.TempDir()
		start := func() *Server {
			srv := New(config.DefaultConfig(), database, peer.NewMap(), nil, "1.0.0-test")
			if err := srv.InitDeviceVault(keyDir); err != nil {
				t.Fatal(err)
			}
			if err := srv.InitPeerCredentialVault(oldOrgVaultSecret); err != nil {
				t.Fatal(err)
			}
			return srv
		}
		srv := start()
		moved, left, err := srv.MigrateOrgPeerCredentials()
		if err != nil || moved != 2 || left != 1 {
			t.Fatalf("first start: moved %d, left %d, %v; want 2, 1", moved, left, err)
		}
		row := func(org, peerID string) *db.OrgPeerCredential {
			t.Helper()
			r, err := database.GetOrgPeerCredential(org, peerID)
			if err != nil || r == nil {
				t.Fatalf("row %s/%s: %+v %v", org, peerID, r, err)
			}
			return r
		}
		x := row("org-x", "MOVEPEER1")
		if x.KeyID != devicevault.KeyID || x.UpdatedBy != "before" {
			t.Errorf("moved row: key id %q, updated by %q", x.KeyID, x.UpdatedBy)
		}
		if lost := row("org-x", "LOSTPEER1"); lost.KeyID != "v1" || lost.Ciphertext != c {
			t.Errorf("the row the old key does not open changed: %+v", lost)
		}

		// A second start moves nothing and changes nothing
		again := start()
		if moved, left, err := again.MigrateOrgPeerCredentials(); err != nil || moved != 0 || left != 1 {
			t.Errorf("second start: moved %d, left %d, %v; want 0, 1", moved, left, err)
		}
		if y := row("org-x", "MOVEPEER1"); y.Ciphertext != x.Ciphertext || y.Nonce != x.Nonce {
			t.Error("a second start sealed the row again")
		}

		// What a database copy or a full backup holds does not open it
		if _, err := old.Open(x.Nonce, x.Ciphertext, x.KeyID); err == nil {
			t.Error("the old key (in the database) opens a moved password")
		}
		stranger, err := devicevault.Load(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := stranger.OpenOrgPassword("org-x", "MOVEPEER1", x.Nonce, x.Ciphertext, x.KeyID); err == nil {
			t.Error("another server's key file opens a moved password")
		}
		right, err := devicevault.Load(keyDir)
		if err != nil {
			t.Fatal(err)
		}
		if got, err := right.OpenOrgPassword("org-x", "MOVEPEER1", x.Nonce, x.Ciphertext, x.KeyID); err != nil || got != "old-key-pass-1" {
			t.Errorf("the key file opens %q, %v", got, err)
		}
		if _, err := right.OpenOrgPassword("org-y", "MOVEPEER1", x.Nonce, x.Ciphertext, x.KeyID); err == nil {
			t.Error("a moved password opens for another organization")
		}
		if _, err := right.OpenOrgPassword("org-x", "MOVEPEER2", x.Nonce, x.Ciphertext, x.KeyID); err == nil {
			t.Error("a moved password opens for another device")
		}

		// Without the old key a moved password still works: the server needs only the key file
		later := New(config.DefaultConfig(), database, peer.NewMap(), nil, "1.0.0-test")
		if err := later.InitDeviceVault(keyDir); err != nil {
			t.Fatal(err)
		}
		if got, err := later.openOrgPassword(x); err != nil || got != "old-key-pass-1" {
			t.Errorf("opened without the old key: %q, %v", got, err)
		}
		// A new password is sealed with the key file
		if status, body := asConsole(t, later, http.MethodPut, "/api/org/org-y/peer-credentials/NEWPEER1", `{"password":"new-pass-1"}`); status != http.StatusOK {
			t.Fatalf("set: %d %s", status, body)
		}
		if n := row("org-y", "NEWPEER1"); n.KeyID != devicevault.KeyID || strings.Contains(n.Ciphertext, "new-pass") {
			t.Errorf("a new password: key id %q", n.KeyID)
		}
	})
}
