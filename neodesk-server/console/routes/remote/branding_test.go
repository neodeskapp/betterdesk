package remote

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/unitronix/betterdesk-server/console/branding"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// The remote sign-in and guest pages read the console's branding as it
// is now: a name saved after the start shows at once, with no restart
// (BR17). The pages read d.Branding, the cache every branding write
// reloads.
func TestRemotePagesShowTheSavedBranding(t *testing.T) {
	db := storetest.Migrated(t)
	w := newWorld(t, db, "1.0.0", false)
	svc, err := branding.New(branding.Options{DB: db})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	svc.LoadBranding(ctx)
	w.deps.Branding = svc
	w.deps.Router, w.deps.Early = kit.NewRouter(), kit.NewRouter()
	src := w.sources("1.0.0")
	src.branding = defaultSources(w.deps).branding
	register(w.deps, src)
	appName := func(path string) string {
		rec := w.do(&vector{Request: request{Method: "GET", Path: path}})
		var page struct {
			AppName string `json:"appName"`
			Page    struct {
				AppName string `json:"appName"`
			} `json:"page"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
			t.Fatalf("%s: %d %s", path, rec.Code, rec.Body.String())
		}
		if page.AppName != "" {
			return page.AppName
		}
		return page.Page.AppName
	}
	if got := appName("/api/remote/login-info"); got != "BetterDesk" {
		t.Fatalf("before the save the sign-in page says %q", got)
	}
	update, err := branding.ParseJSON([]byte(`{"appName":"Front Desk Remote"}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.SaveBranding(ctx, update); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/remote/login-info", "/api/remote/guest-info"} {
		if got := appName(path); got != "Front Desk Remote" {
			t.Errorf("%s says %q after the save, want the saved name", path, got)
		}
	}
}
