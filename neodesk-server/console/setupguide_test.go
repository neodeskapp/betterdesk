package console

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/config"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// guideAnswer is the data of GET /api/setup-guide.
type guideAnswer struct {
	Success bool `json:"success"`
	Data    struct {
		Visible bool `json:"visible"`
		Steps   []struct {
			ID   string `json:"id"`
			Done bool   `json:"done"`
		} `json:"steps"`
	} `json:"data"`
}

func readGuide(t *testing.T, a answer) guideAnswer {
	t.Helper()
	if a.status != 200 {
		t.Fatalf("status %d %s", a.status, a.body)
	}
	var g guideAnswer
	if err := json.Unmarshal([]byte(a.body), &g); err != nil {
		t.Fatal(err)
	}
	return g
}

func guideStep(t *testing.T, g guideAnswer, id string) bool {
	t.Helper()
	for _, s := range g.Data.Steps {
		if s.ID == id {
			return s.Done
		}
	}
	t.Fatalf("no step %s", id)
	return false
}

// The start of a new install opens the guide for its first admin, and
// only for it. The routes need server.config: a viewer gets the guard's
// refusal and nothing of the server's state. Hiding and bringing back
// stay with the account.
func TestStartOpensTheSetupGuideOnANewInstall(t *testing.T) {
	w := newWorld(t, setup{trustProxy: "loopback"})
	if err := w.s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	admin := w.agent().signIn("admin")
	g := readGuide(t, admin.get("/api/setup-guide"))
	if !g.Data.Visible || len(g.Data.Steps) != 8 || g.Data.Steps[0].ID != "password" {
		t.Fatalf("the first admin's guide: %+v", g.Data)
	}
	if guideStep(t, g, "https") {
		t.Error("plain HTTP ticks the HTTPS step")
	}
	if !guideStep(t, readGuide(t, admin.get("/api/setup-guide", "X-Forwarded-Proto", "https")), "https") {
		t.Error("HTTPS through a trusted proxy does not tick")
	}

	second := w.agent().signIn("super_admin")
	if readGuide(t, second.get("/api/setup-guide")).Data.Visible {
		t.Error("a second admin sees the guide without asking")
	}
	// A server admin has server.config but may not make a full backup (L387)
	for _, step := range readGuide(t, w.agent().signIn("server_admin").get("/api/setup-guide")).Data.Steps {
		if step.ID == "backup" || step.ID == "computer" || step.ID == "people" {
			t.Errorf("a server admin gets the step %s", step.ID)
		}
	}
	for _, role := range []string{"viewer", "operator", "global_admin"} {
		a := w.agent().signIn(role)
		got := a.get("/api/setup-guide")
		if got.status != 403 || strings.Contains(got.body, "steps") {
			t.Errorf("%s: %d %s", role, got.status, got.body)
		}
		if res := a.post("/api/setup-guide/show", "{}"); res.status != 403 {
			t.Errorf("%s may bring the guide back: %d", role, res.status)
		}
	}
	if res := w.agent().get("/api/setup-guide"); res.status != 401 {
		t.Errorf("signed out: %d %s", res.status, res.body)
	}

	if readGuide(t, admin.post("/api/setup-guide/hide", "{}")).Data.Visible {
		t.Error("hide answered a visible guide")
	}
	if readGuide(t, admin.get("/api/setup-guide")).Data.Visible {
		t.Error("the guide is back after hide")
	}
	if !readGuide(t, second.post("/api/setup-guide/show", "{}")).Data.Visible {
		t.Error("the second admin could not bring the guide up")
	}
	if !readGuide(t, admin.post("/api/setup-guide/show", "{}")).Data.Visible {
		t.Error("the first admin could not bring the guide back")
	}
}

// A database the console set up and someone used before (here: migrated
// once more, as an update does) opens the guide for nobody.
func TestStartOpensNoSetupGuideAfterAnUpdate(t *testing.T) {
	db := storetest.Migrated(t)
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	w := newWorld(t, setup{db: db})
	if _, err := db.Exec(context.Background(), `UPDATE users SET last_login = datetime('now')`); err != nil {
		t.Fatal(err)
	}
	if err := w.s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if readGuide(t, w.agent().signIn("admin").get("/api/setup-guide")).Data.Visible {
		t.Error("an update opened the guide")
	}
}

// The address step ticks when the Docker installer wrote the server's
// address to RELAY_SERVERS, the address devices get their relay at, and
// stays open while the address clients get is only this machine's
// (setup audit DOCKER-QUICK-2). The status says whether the console runs
// in a container, where the HTTPS step leads to the install script's way.
func TestSetupGuideAddressStepTakesTheInstallersAddress(t *testing.T) {
	for _, c := range []struct {
		name   string
		env    map[string]string
		docker bool
		done   bool
	}{
		{"RELAY_SERVERS", map[string]string{"RELAY_SERVERS": "203.0.113.10"}, true, true},
		{"RELAY_SERVERS with a port", map[string]string{"RELAY_SERVERS": "desk.example.com:21117"}, false, true},
		{"loopback RELAY_SERVERS", map[string]string{"RELAY_SERVERS": "127.0.0.1"}, true, false},
		{"nothing set", nil, false, false},
	} {
		w := newWorld(t, setup{env: c.env, configure: func(cfg *config.Config) {
			cfg.APIURL = "http://127.0.0.1:21114/api"
			cfg.Docker = c.docker
		}})
		if err := w.s.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		answer := w.agent().signIn("admin").get("/api/setup-guide")
		g := readGuide(t, answer)
		if done := guideStep(t, g, "address"); done != c.done {
			t.Errorf("%s: address step done %v, want %v", c.name, done, c.done)
		}
		if docker := strings.Contains(answer.body, `"docker":true`); docker != c.docker {
			t.Errorf("%s: docker %v in %s", c.name, docker, answer.body)
		}
	}
}
