package settings_test

import (
	"context"
	"testing"

	apiserver "github.com/neodeskapp/neodesk/neodesk-server/api"
	serverconfig "github.com/neodeskapp/neodesk/neodesk-server/config"
	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/routetest"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
	"github.com/neodeskapp/neodesk/neodesk-server/peer"
)

// TestSessionDurationOutlivesARestart: the Session duration saved on the
// Sign-in tab is the lifetime of the tokens of the console's client API
// from the start on, not the API_TOKEN_EXPIRY_DAYS the console was
// started with: a console started on a server that keeps 3 days issues
// 3-day tokens, also without an .env and with the variable set to 7
// (L354).
func TestSessionDurationOutlivesARestart(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, sdb *store.DB) {
		gdb := storetest.GoServer(t, sdb)
		if err := gdb.SetConfig("client_session_expiry_days", "3"); err != nil {
			t.Fatal(err)
		}
		srv := apiserver.New(serverconfig.DefaultConfig(), gdb, peer.NewMap(), nil, "test")
		w := routetest.New(t, routetest.Options{DB: sdb, API: srv.InternalHandler(), Env: map[string]string{"API_TOKEN_EXPIRY_DAYS": "7"}})
		if got := w.Server.Deps().Authn.TokenLifetimeDays(context.Background()); got != 3 {
			t.Errorf("a console started on a server that keeps 3 days issues %d-day tokens, want 3", got)
		}
	})
}
