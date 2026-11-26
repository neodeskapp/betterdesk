package impersonate_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/accessibility"
)

// While an admin views the console as another account, the console keeps
// the admin's own accessibility settings: the bootstrap and GET
// /api/accessibility answer the admin's, and PUT /api/accessibility (the
// one write the impersonation may make besides its own routes) changes the
// admin's row only, never the viewed account's.
func TestViewingAsKeepsTheAdminsOwnAccessibilitySettings(t *testing.T) {
	w, _ := newWorld(t, options{})
	ctx := context.Background()
	admin := w.As("super_admin")
	if res := admin.Do("PUT", "/api/accessibility", `{"moreContrast":"on","underlineLinks":true}`); res.Status != 200 {
		t.Fatalf("the admin's own save: %d %s", res.Status, res.Body)
	}
	// The viewed account chose otherwise
	if err := accessibility.Save(ctx, w.DB, idOperator, accessibility.Settings{ReduceMotion: "on", MoreContrast: "off", ReadableFont: true}); err != nil {
		t.Fatal(err)
	}

	a := start(t, w, "super_admin", idOperator)

	var boot struct {
		User struct {
			Username      string                  `json:"username"`
			Accessibility *accessibility.Settings `json:"accessibility"`
		} `json:"user"`
	}
	res := a.Do("GET", "/api/console/bootstrap", "")
	if res.Status != 200 || json.Unmarshal([]byte(res.Body), &boot) != nil {
		t.Fatalf("bootstrap: %d %s", res.Status, res.Body)
	}
	if boot.User.Username != "e2e_operator" {
		t.Fatalf("the bootstrap is not the viewed account's: %s", boot.User.Username)
	}
	if s := boot.User.Accessibility; s == nil || s.MoreContrast != "on" || !s.UnderlineLinks || s.ReadableFont || s.ReduceMotion != "system" {
		t.Errorf("bootstrap accessibility = %+v, want the admin's own", s)
	}

	res = a.Do("GET", "/api/accessibility", "")
	var got struct {
		Data accessibility.Settings `json:"data"`
	}
	if res.Status != 200 || json.Unmarshal([]byte(res.Body), &got) != nil || got.Data.MoreContrast != "on" || got.Data.ReadableFont {
		t.Errorf("GET while viewing as: %d %s, want the admin's own", res.Status, res.Body)
	}

	res = a.Do("PUT", "/api/accessibility", `{"pauseLiveUpdates":true}`)
	if res.Status != 200 {
		t.Fatalf("PUT while viewing as: %d %s", res.Status, res.Body)
	}
	own, _, err := accessibility.Load(ctx, w.DB, idSuper)
	if err != nil || !own.PauseLiveUpdates || own.MoreContrast != "on" {
		t.Errorf("the admin's row after the save: %+v %v", own, err)
	}
	viewed, _, err := accessibility.Load(ctx, w.DB, idOperator)
	want := accessibility.Settings{ReduceMotion: "on", MoreContrast: "off", ReadableFont: true}
	if err != nil || viewed != want {
		t.Errorf("the viewed account's row changed: %+v %v", viewed, err)
	}
	// The impersonation goes on
	if name, _ := whoAmI(t, a); name != "e2e_operator" {
		t.Errorf("after the save the console is %s", name)
	}
}
