package meshcentral

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/auth"
)

// TestControlSocketRefusesADisabledAccount: the token of a disabled account
// opens no control socket; enabled again, the account's token does (L107).
func TestControlSocketRefusesADisabledAccount(t *testing.T) {
	gw, url, jm := controlGateway(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	anna, err := gw.db.GetUser("anna")
	if err != nil || anna == nil {
		t.Fatalf("anna: %v %v", anna, err)
	}
	tok := token(t, jm, "anna", auth.RoleOperator)
	if err := gw.db.SetUserDisabled(anna.ID, true); err != nil {
		t.Fatal(err)
	}
	if c, resp, err := dialControl(ctx, url, tok); err == nil {
		c.CloseNow()
		t.Error("the token of a disabled account opened the socket")
	} else if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("disabled account: got %v, want 401", resp)
	}
	if err := gw.db.SetUserDisabled(anna.ID, false); err != nil {
		t.Fatal(err)
	}
	if c, _, err := dialControl(ctx, url, tok); err != nil {
		t.Errorf("the account enabled again: %v", err)
	} else {
		c.CloseNow()
	}
}
