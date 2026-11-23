package console

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

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

// A database the console set up before (here: migrated once more, as an
// update does) opens the guide for nobody.
func TestStartOpensNoSetupGuideAfterAnUpdate(t *testing.T) {
	db := storetest.Migrated(t)
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	w := newWorld(t, setup{db: db})
	if err := w.s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if readGuide(t, w.agent().signIn("admin").get("/api/setup-guide")).Data.Visible {
		t.Error("an update opened the guide")
	}
}
