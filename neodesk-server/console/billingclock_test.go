package console

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/config"
	"github.com/neodeskapp/neodesk/neodesk-server/console/httpx"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
	"github.com/neodeskapp/neodesk/neodesk-server/console/session"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// clockWorld is a console of newWorld's kind whose .env file is in a
// temp folder.
func clockWorld(t *testing.T) (*world, string) {
	t.Helper()
	db := storetest.Migrated(t)
	for role, id := range roleIDs {
		if _, err := db.Exec(context.Background(), "INSERT INTO users (id, username, password_hash, role) VALUES (?, ?, 'x', ?)", id, "e2e_"+role, role); err != nil {
			t.Fatalf("insert user: %v", err)
		}
	}
	data := t.TempDir()
	envPath := filepath.Join(t.TempDir(), ".env")
	cfg := &config.Config{
		Host: "127.0.0.1", NodeEnv: "development", DataDir: data, UploadsDir: filepath.Join(data, "uploads"),
		LangDir: filepath.Join("static", "files", "lang"), WebUIDir: filepath.Join(data, "no-build"),
		DefaultLanguage: "en", EnvPath: envPath,
		Getenv: func(key string) string { return map[string]string{"SESSION_SECRET": testSecret}[key] },
	}
	s, err := New(context.Background(), Options{Config: cfg, Store: db, API: http.NotFoundHandler()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(s.Stop)
	d := s.Deps()
	s.Router().Get("/__test/signin/{role}", func(rw http.ResponseWriter, r *http.Request) {
		role := r.PathValue("role")
		sess := kit.Session(r)
		if err := sess.Regenerate(); err != nil {
			t.Errorf("regenerate: %v", err)
		}
		if _, err := sess.Establish(session.User{ID: roleIDs[role], Username: "e2e_" + role, Role: role}, d.Sessions.ClientMeta(r, "")); err != nil {
			t.Errorf("establish: %v", err)
		}
		kit.OK(rw, r, nil)
	})
	s.Router().Get("/__test/token", func(rw http.ResponseWriter, r *http.Request) {
		kit.JSON(rw, r, http.StatusOK, map[string]string{"token": kit.CSRFToken(r)})
	})
	w := &world{t: t, s: s, db: db, front: httptest.NewServer(s)}
	t.Cleanup(w.front.Close)
	return w, envPath
}

// TestBillingClockChangeIsCanceledThroughTheRestartRoutes saves the clock
// settings, which writes the .env file and starts a restart transaction,
// and cancels that transaction with the restart route of the settings
// group: the .env file holds the settings from before the save again
// (BUGS SE8: Go had no rollback for "billing-clock").
func TestBillingClockChangeIsCanceledThroughTheRestartRoutes(t *testing.T) {
	w, envPath := clockWorld(t)
	before := "PORT=5000\nNTP_SERVERS=old.example\nBILLING_MAX_CLOCK_SKEW_MS=1500\nBILLING_REQUIRE_SYNCED_CLOCK=0\nBILLING_TRUST_OS_NTP=0\nBILLING_TIMEZONE=Europe/Berlin\n"
	if err := os.WriteFile(envPath, []byte(before), 0o600); err != nil {
		t.Fatal(err)
	}
	a := w.agent().signIn("admin")

	saved := a.do("PUT", "/api/panel/billing/clock/settings",
		`{"ntp_servers":"new.example","max_skew_ms":3000,"require_synced_clock":true,"trust_os_ntp":true,"billing_timezone":"Asia/Tokyo"}`,
		"Content-Type", "application/json", "X-CSRF-Token", a.csrf())
	if saved.status != http.StatusOK {
		t.Fatalf("save: %d %s", saved.status, saved.body)
	}
	var answer struct {
		RestartRequired struct {
			ID      string `json:"id"`
			Phase   string `json:"phase"`
			Changes []struct {
				Key string `json:"key"`
			} `json:"changes"`
		} `json:"restartRequired"`
	}
	if err := json.Unmarshal([]byte(saved.body), &answer); err != nil {
		t.Fatal(err)
	}
	tx := answer.RestartRequired
	if tx.ID == "" || tx.Phase != "pending" || len(tx.Changes) != 1 || tx.Changes[0].Key != "billing-clock" {
		t.Fatalf("the save started no restart transaction: %s", saved.body)
	}
	if content, _ := os.ReadFile(envPath); !strings.Contains(string(content), "NTP_SERVERS=new.example") {
		t.Fatalf("the save did not write the file:\n%s", content)
	}

	canceled := a.post("/api/settings/restart/cancel", `{"id":"`+tx.ID+`"}`)
	if canceled.status != http.StatusOK || !strings.Contains(canceled.body, `"canceled":true`) {
		t.Fatalf("cancel: %d %s", canceled.status, canceled.body)
	}
	content, _ := os.ReadFile(envPath)
	for _, line := range []string{"NTP_SERVERS=old.example", "BILLING_MAX_CLOCK_SKEW_MS=1500", "BILLING_REQUIRE_SYNCED_CLOCK=0",
		"BILLING_TRUST_OS_NTP=0", "BILLING_TIMEZONE=Europe/Berlin"} {
		if !strings.Contains(string(content), line+"\n") {
			t.Errorf("after the cancel the file lacks %q:\n%s", line, content)
		}
	}
	read := a.get("/api/panel/billing/clock/settings")
	want := `{"success":true,"settings":{"ntp_servers":"old.example","max_skew_ms":1500,"require_synced_clock":false,"trust_os_ntp":false,"billing_timezone":"Europe/Berlin"},"dockerMode":false}`
	if read.body != want {
		t.Errorf("after the cancel the settings read\n%s\nwant\n%s", read.body, want)
	}
	if pending := a.get("/api/settings/restart/pending"); !strings.Contains(pending.body, `"data":null`) {
		t.Errorf("a transaction is still pending: %s", pending.body)
	}
}

// TestHelpRequestMailRuns pins that the help request mail is a job of the
// console.
func TestHelpRequestMailRuns(t *testing.T) {
	cfg := &config.Config{
		Host: "127.0.0.1", DataDir: t.TempDir(), UploadsDir: t.TempDir(), LangDir: filepath.Join("static", "files", "lang"),
		DefaultLanguage: "en", TrustProxy: httpx.TrustProxy{},
		Getenv: func(key string) string { return map[string]string{"SESSION_SECRET": testSecret}[key] },
	}
	s, err := New(context.Background(), Options{Config: cfg, Store: storetest.Migrated(t), API: http.NotFoundHandler()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Stop)
	if names := s.Jobs().Names(); !slices.Contains(names, "help request mail") {
		t.Errorf("the job must run, jobs = %q", names)
	}
}
