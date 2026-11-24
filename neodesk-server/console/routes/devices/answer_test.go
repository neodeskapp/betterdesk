package devices_test

import (
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	apiserver "github.com/neodeskapp/neodesk/neodesk-server/api"
	serverconfig "github.com/neodeskapp/neodesk/neodesk-server/config"
	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/routetest"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
	"github.com/neodeskapp/neodesk/neodesk-server/db"
	"github.com/neodeskapp/neodesk/neodesk-server/peer"
)

// TestConnectPasswordAnswerKeepsThePasswordOnTheServer: the browser viewer
// signs in with the device password the organization's address book
// stores through the answer to the device's question: the console hands
// out the answer, never the password, to an account that may use that
// address book's passwords, also for a device only the address book names;
// another account gets {"password_set":false}; the answer is not kept by
// the browser (BUGS L454).
func TestConnectPasswordAnswerKeepsThePasswordOnTheServer(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, sdb *store.DB) {
		gdb := storetest.GoServer(t, sdb)
		srv := apiserver.New(serverconfig.DefaultConfig(), gdb, peer.NewMap(), nil, "test")
		if err := srv.InitPeerCredentialVault("l454-vault-secret-0123456789abcdef"); err != nil {
			t.Fatal(err)
		}
		if err := srv.InitDeviceVault(t.TempDir()); err != nil {
			t.Fatal(err)
		}
		w := routetest.New(t, routetest.Options{DB: sdb, API: srv.InternalHandler(), Env: raisedLimits, Config: nodeLanguages})
		if err := gdb.UpsertPeer(&db.Peer{ID: "BOOKPEER1", UUID: "u-bookpeer1"}); err != nil {
			t.Fatal(err)
		}
		if err := gdb.CreateOrganization(&db.Organization{ID: "org-book", Name: "Book", Slug: "org-book", Settings: "{}"}); err != nil {
			t.Fatal(err)
		}
		// The device is a contact of the address book, not a device of the organization
		put := httptest.NewRequest(http.MethodPut, "/api/org/org-book/peer-credentials/BOOKPEER1", strings.NewReader(`{"password":"l454-book-pw"}`))
		put = put.WithContext(apiserver.WithIdentity(put.Context(), apiserver.Identity{Username: "setup", Role: "super_admin"}))
		rec := httptest.NewRecorder()
		srv.InternalHandler().ServeHTTP(rec, put)
		if rec.Code != http.StatusOK {
			t.Fatalf("store the password: %d %s", rec.Code, rec.Body.String())
		}
		for _, role := range []string{"operator", "super_admin"} {
			if err := gdb.CreateUser(&db.User{Username: "e2e_" + role, PasswordHash: "x", Role: role}); err != nil && !strings.Contains(strings.ToLower(err.Error()), "unique") {
				t.Fatal(err)
			}
		}

		ask := func(role string) routetest.Answer {
			return w.As(role).Do(http.MethodPost, "/api/devices/BOOKPEER1/connect-password/answer", `{"salt":"s4lt","challenge":"ch4llenge"}`)
		}
		if got := ask("operator"); got.Status != http.StatusOK || !strings.Contains(got.Body, `"password_set":false`) || strings.Contains(got.Body, "l454-book-pw") {
			t.Errorf("operator outside the organization: %d %s, want 200 password_set false", got.Status, got.Body)
		}
		operator, err := gdb.GetUser("e2e_operator")
		if err != nil || operator == nil {
			t.Fatalf("operator: %v", err)
		}
		if err := gdb.CreateOrgUser(&db.OrgUser{ID: "ou-book", OrgID: "org-book", ServerUserID: operator.ID, Username: "e2e_operator", Role: "user"}); err != nil {
			t.Fatal(err)
		}
		first := sha256.Sum256([]byte("l454-book-pw" + "s4lt"))
		sum := sha256.Sum256(append(first[:], "ch4llenge"...))
		want := base64.StdEncoding.EncodeToString(sum[:])
		for _, role := range []string{"operator", "super_admin"} {
			got := ask(role)
			if got.Status != http.StatusOK || !strings.Contains(got.Body, want) || strings.Contains(got.Body, "l454-book-pw") {
				t.Errorf("%s: %d %s, want the answer %s and not the password", role, got.Status, got.Body, want)
			}
			if cc := got.Header.Get("Cache-Control"); !strings.Contains(cc, "no-store") {
				t.Errorf("%s: Cache-Control %q", role, cc)
			}
		}
	})
}
