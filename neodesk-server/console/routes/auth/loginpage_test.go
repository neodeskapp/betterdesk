package auth_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/branding"
	"github.com/neodeskapp/neodesk/neodesk-server/console/config"
	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/routetest"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

func loginPageWorld(t *testing.T, db *store.DB) *routetest.World {
	t.Helper()
	return routetest.New(t, routetest.Options{DB: db, Config: func(cfg *config.Config) {
		cfg.Version = "1.2.3"
	}})
}

func loginPage(t *testing.T, a *routetest.Agent, header ...string) (routetest.Answer, map[string]any) {
	t.Helper()
	res := a.Do("GET", "/api/auth/login-page", "", header...)
	var out map[string]any
	if err := json.Unmarshal([]byte(res.Body), &out); err != nil {
		t.Fatalf("login page: %d %s", res.Status, res.Body)
	}
	return res, out
}

// The sign-in page's data needs no session and holds no user data: the
// CSRF token, name and version, branding, languages and the badge.
func TestLoginPage(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := loginPageWorld(t, db)
		res, page := loginPage(t, w.SignedOut())
		if res.Status != http.StatusOK || res.Header.Get("Cache-Control") != "no-store" {
			t.Fatalf("login page: %d %v", res.Status, res.Header)
		}
		dec := json.NewDecoder(strings.NewReader(res.Body))
		var keys []string
		dec.Token()
		for dec.More() {
			tok, _ := dec.Token()
			keys = append(keys, tok.(string))
			var skip json.RawMessage
			_ = dec.Decode(&skip)
		}
		if got := strings.Join(keys, ","); got != "success,csrfToken,appName,appVersion,desktopMode,signedIn,branding,languages,attestation" {
			t.Errorf("keys = %s", got)
		}
		if page["appName"] != "NeoDesk" || page["appVersion"] != "1.2.3" || page["desktopMode"] != false ||
			page["signedIn"] != false || page["attestation"] != nil {
			t.Errorf("login page = %v", page)
		}
		if token, _ := page["csrfToken"].(string); len(token) < 32 {
			t.Errorf("csrfToken = %q", token)
		}
		languages, _ := json.Marshal(page["languages"])
		if string(languages) != `[{"code":"de","flag":"🇩🇪","name":"German","native":"Deutsch","rtl":false},{"code":"en","flag":"🇬🇧","name":"English","native":"English","rtl":false}]` {
			t.Errorf("languages = %s", languages)
		}

		// The old desktop mode's cookie, exactly "true"
		for value, want := range map[string]bool{"true": true, "TRUE": false, "1": false} {
			if _, page := loginPage(t, w.SignedOut(), "Cookie", "neodesk_desktop_mode="+value); page["desktopMode"] != want {
				t.Errorf("desktop mode cookie %q: %v", value, page["desktopMode"])
			}
		}
		if _, page := loginPage(t, w.As("viewer")); page["signedIn"] != true {
			t.Errorf("signed in as viewer: %v", page["signedIn"])
		}
		// A pro account may not use the console
		if _, page := loginPage(t, w.As("pro")); page["signedIn"] != false {
			t.Errorf("signed in as pro: %v", page["signedIn"])
		}

		// The branding's name and the capacity badge
		if _, err := db.Exec(context.Background(), "INSERT INTO branding_config (key, value, updated_at) VALUES ('appName', 'Our Console', '2026-01-02 03:04:05')"); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(context.Background(), "INSERT INTO settings (key, value) VALUES ('server_attestation_result', ?)", `{"tier":"titanium","maxConnections":250}`); err != nil {
			t.Fatal(err)
		}
		w.Server.Deps().Branding.LoadBranding(context.Background())
		_, page = loginPage(t, w.SignedOut())
		badge, _ := json.Marshal(page["attestation"])
		if page["appName"] != "Our Console" || string(badge) != `{"badgeTitle":"Our Console Server Attestation","brand":"Our Console","tier":"titanium","tierLabel":"TITANIUM"}` {
			t.Errorf("appName %v, attestation %s", page["appName"], badge)
		}
	})
}

// The sign-in page shows a save of the branding at once: it reads the
// cache every save reloads.
func TestLoginPageShowsASaveAtOnce(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := routetest.New(t, routetest.Options{DB: db})
		updates, err := branding.ParseJSON([]byte(`{"appName":"Saved"}`))
		if err != nil {
			t.Fatal(err)
		}
		if err := w.Server.Deps().Branding.SaveBranding(context.Background(), updates); err != nil {
			t.Fatal(err)
		}
		if _, page := loginPage(t, w.SignedOut()); page["appName"] != "Saved" {
			t.Errorf("appName = %v", page["appName"])
		}
	})
}
