package generator_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/config"
	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/replay"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// The made-up keys of the recording (testdata/README.md).
var (
	testSeed      = base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{9}, 32))
	testPublicKey = base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{3}, 32))
)

// recordedStep is a step of the recording with what this group's script
// kept beside it: a binary body, and the headers and bytes of a download.
type recordedStep struct {
	replay.Step
	BodyBase64 *string `json:"bodyBase64"`
	Download   bool    `json:"download"`
}

type recording struct {
	Dialect string         `json:"dialect"`
	Steps   []recordedStep `json:"steps"`
}

// buildIDs are the ids of build rows in an answer.
var buildIDs = regexp.MustCompile(`"id":[0-9]+,"branding_hash"`)

// counter gives the bundle ids 000000000001, 000000000002, ... as the
// recording's stub of crypto.randomBytes did.
type counter struct{ n int }

func (c *counter) Read(p []byte) (int, error) {
	if len(p) != 6 {
		return 0, os.ErrInvalid
	}
	c.n++
	copy(p, []byte{0, 0, 0, 0, 0, byte(c.n)})
	return 6, nil
}

// TestNodeAnswers replays what the Node console's generator routes
// answered (testdata/node_sqlite.json): the module installed from an
// uploaded template archive, bundles created, changed, rebuilt, revoked
// and deleted, the download portal with a ready build, previews, the old
// toml generator. Every answer must match byte for byte; downloads also
// in their headers. A step of fixes must give the fix's answer instead.
func TestNodeAnswers(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		raw, err := os.ReadFile(filepath.Join("testdata", "node_"+db.Dialect().String()+".json"))
		if err != nil {
			t.Skipf("no recording for %s", db.Dialect())
		}
		var rec recording
		if err := json.Unmarshal(raw, &rec); err != nil {
			t.Fatal(err)
		}
		keyFile := filepath.Join(t.TempDir(), "id_ed25519.pub")
		if err := os.WriteFile(keyFile, []byte(testPublicKey+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		var dataDir string
		w := replay.NewWorld(t, db, func(cfg *config.Config) {
			cfg.APIURL = "http://desk.example.test:21114/api"
			cfg.PubKeyPath = keyFile
			cfg.AgentArtifactDir = filepath.Join(cfg.DataDir, "agent-builds")
			// the console is not started, so the worker job does not run
			cfg.AgentBuildWorker = true
			dataDir = cfg.DataDir
		})
		deps := w.Server.Deps()
		deps.Generator.UseRandom(&counter{})
		if err := os.WriteFile(filepath.Join(dataDir, "custom-client-signing.seed"), []byte(testSeed+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		artifacts := filepath.Join(dataDir, "agent-builds")
		if err := os.MkdirAll(artifacts, 0o755); err != nil {
			t.Fatal(err)
		}
		artifact := filepath.Join(artifacts, "neodesk-support-win.zip")
		if err := os.WriteFile(artifact, []byte("zip-content"), 0o644); err != nil {
			t.Fatal(err)
		}
		when := time.Date(2024, 5, 6, 7, 8, 9, 0, time.UTC)
		if err := os.Chtimes(artifact, when, when); err != nil {
			t.Fatal(err)
		}
		w.Vars["<ARTIFACTS>"] = artifacts
		for _, row := range [][2]string{{"agentBgType", "color"}, {"agentBgColor", "#102030"}, {"agentShowPoweredBy", "false"}, {"appName", "Panel Name"}} {
			if _, err := db.Exec(context.Background(), "INSERT INTO branding_config (key, value) VALUES (?, ?)", row[0], row[1]); err != nil {
				t.Fatal(err)
			}
		}
		deps.Branding.LoadBranding(context.Background())

		scrub := func(s string) string { return replay.Normalize(strings.ReplaceAll(s, dataDir, "<DATA>")) }
		if db.IsPostgres() {
			// A changed bundle's builds were queued after the old route had
			// answered, and on PostgreSQL two such queues could run at the
			// same time: each insert takes the next number of the id
			// sequence, also one that ends as an update, so the numbers of
			// build rows depended on timing. Here the queue runs before the
			// answer. The ids of build rows are compared by form only.
			plain := scrub
			scrub = func(s string) string { return buildIDs.ReplaceAllString(plain(s), `"id":<id>,"branding_hash"`) }
		}
		failed := 0
		hashes := brandingHashes{}
		for i, step := range rec.Steps {
			if step.SQL != "" {
				if strings.HasPrefix(step.SQL, "SELECT") {
					if have := w.Rows(step.SQL, step.Params); have != step.Rows {
						t.Errorf("step %d %q: %s\n node %s\n   go %s", i, step.Name, step.SQL, step.Rows, have)
					}
				} else {
					w.Exec(step.SQL, step.Params)
				}
				continue
			}
			req := replay.Request{Role: step.Role, Method: step.Method, Path: step.Path, Body: step.Body, Headers: step.Headers}
			if step.BodyBase64 != nil {
				b, err := base64.StdEncoding.DecodeString(*step.BodyBase64)
				if err != nil {
					t.Fatal(err)
				}
				req.Body = replay.Text(string(b))
			}
			got := w.Do(req)
			if step.Download {
				if problem := compareDownload(step, got); problem != "" {
					failed++
					t.Errorf("step %d %q: %s", i, step.Name, problem)
				}
				continue
			}
			hashes.learn(t, step.Answer)
			status, want, have := step.Status, hashes.translate(clientRepoFix.Replace(scrub(step.Answer))), scrub(got.Body)
			if f, fixed := fixes[step.Name]; fixed {
				status, want = f.status, f.answer
			}
			if got.Status != status || want != have {
				failed++
				if failed <= 25 {
					t.Errorf("step %d %q as %q\n want %d %s\n   go %d %s", i, step.Name, step.Role, status, want, got.Status, have)
				}
				continue
			}
			if cc, ok := step.Extra["cache-control"]; ok {
				var want string
				_ = json.Unmarshal(cc, &want)
				if have := got.Header.Get("Cache-Control"); have != want {
					t.Errorf("step %d %q: Cache-Control node %q, go %q", i, step.Name, want, have)
				}
			}
		}
		if failed > 25 {
			t.Errorf("%d more steps differ", failed-25)
		}
	})
}

// compareDownload compares the headers a download sends and its bytes.
func compareDownload(step recordedStep, got replay.Answer) string {
	if got.Status != step.Status {
		return "status: node " + strconv.Itoa(step.Status) + ", go " + strconv.Itoa(got.Status)
	}
	var problems []string
	for _, name := range []string{"content-type", "content-length", "content-disposition", "content-range", "accept-ranges", "etag", "last-modified"} {
		want := ""
		if raw, ok := step.Extra[name]; ok {
			_ = json.Unmarshal(raw, &want)
		}
		if have := got.Header.Get(name); have != want {
			problems = append(problems, name+": node "+want+", go "+have)
		}
	}
	var body string
	if raw, ok := step.Extra["bodyBase64"]; ok {
		_ = json.Unmarshal(raw, &body)
	}
	if base64.StdEncoding.EncodeToString([]byte(got.Body)) != body {
		problems = append(problems, "body differs")
	}
	return strings.Join(problems, "; ")
}

// TestGuards answers signed-out visitors and accounts without the admin
// role before anything else, on every route that needs them.
func TestGuards(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := replay.NewWorld(t, db, nil)
		for _, c := range []struct{ role, method, path string }{
			{"", "GET", "/api/generator/config"},
			{"", "POST", "/api/generator/generate-config"},
			{"", "GET", "/api/generator/bundles"},
			{"viewer", "GET", "/api/generator/bundles"},
			{"operator", "POST", "/api/generator/module/install-local"},
			{"viewer", "GET", "/api/generator/module/status"},
		} {
			got := w.Do(replay.Request{Role: c.role, Method: c.method, Path: c.path, Body: replay.Text(`{"serverHost":"h"}`)})
			want := 401
			if c.role != "" {
				want = 403
			}
			if got.Status != want {
				t.Errorf("%s %s as %q: %d %s", c.method, c.path, c.role, got.Status, got.Body)
			}
		}
	})
}

// TestSlugFromTheName gives a new bundle without a usable slug a free one
// made from its name, else from its id, with -2, -3 and so on while
// taken (BUGS GN1: the old route never answered).
func TestSlugFromTheName(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		var dataDir string
		w := replay.NewWorld(t, db, func(cfg *config.Config) { dataDir = cfg.DataDir })
		ready(t, w, dataDir)
		for _, c := range []struct{ body, slug string }{
			{`{"name":"Acme","branding":{"server_host":"h.example"}}`, "acme"},
			{`{"name":"Acme","slug":"!!","branding":{"server_host":"h.example"}}`, "acme-2"},
			{`{"name":"Acme","slug":null,"branding":{"server_host":"h.example"}}`, "acme-3"},
			{`{"name":"!","branding":{"server_host":"h.example"}}`, "<bundle id>"},
		} {
			got := w.Do(replay.Request{Role: "admin", Method: "POST", Path: "/api/generator/bundles", Body: replay.Text(c.body)})
			slug, id := bundleSlug(t, got)
			if c.slug == "<bundle id>" && slug == id || slug == c.slug {
				continue
			}
			t.Errorf("%s: %d %s, want the slug %s", c.body, got.Status, got.Body, c.slug)
		}
		if rows := w.Rows("SELECT COUNT(*) AS n FROM agent_bundles", nil); rows != `[{"n":4}]` {
			t.Errorf("bundles written: %s", rows)
		}
	})
}

// bundleSlug is the slug and the id of the bundle an answer names.
func bundleSlug(t *testing.T, got replay.Answer) (slug, id string) {
	t.Helper()
	var answer struct {
		Data struct {
			Bundle struct {
				Slug     string `json:"slug"`
				BundleID string `json:"bundle_id"`
			} `json:"bundle"`
		} `json:"data"`
	}
	if got.Status != http.StatusOK || json.Unmarshal([]byte(got.Body), &answer) != nil {
		return "", ""
	}
	return answer.Data.Bundle.Slug, answer.Data.Bundle.BundleID
}

// ready installs a module with a template tree from a recording's
// archive, as the recorded steps do.
func ready(t *testing.T, w *replay.World, dataDir string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "node_sqlite.json"))
	if err != nil {
		t.Fatal(err)
	}
	var rec recording
	if err := json.Unmarshal(raw, &rec); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "custom-client-signing.seed"), []byte(testSeed+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := w.Do(replay.Request{Role: "admin", Method: "POST", Path: "/api/generator/module/accept-terms", Body: replay.Text("{}")}); got.Status != 200 {
		t.Fatal(got.Body)
	}
	for _, step := range rec.Steps {
		if step.BodyBase64 == nil || !strings.Contains(step.Path, "install-local") {
			continue
		}
		b, _ := base64.StdEncoding.DecodeString(*step.BodyBase64)
		got := w.Do(replay.Request{Role: "admin", Method: "POST", Path: step.Path, Body: replay.Text(string(b)), Headers: step.Headers})
		if got.Status != 200 {
			t.Fatalf("install: %d %s", got.Status, got.Body)
		}
		entries, _ := os.ReadDir(filepath.Join(dataDir, ".generator-uploads"))
		if len(entries) != 0 {
			t.Fatalf("uploads left: %v", entries)
		}
		return
	}
	t.Fatal("no install step in the recording")
}

// TestChangedBundleWithoutSlugGetsOne gives a bundle that has no slug a
// free one made from its name when a change names none, and when the
// name is taken one with -2 (BUGS GN1: the old route never answered). A
// bundle's own slug counts as free for it.
func TestChangedBundleWithoutSlugGetsOne(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		var dataDir string
		w := replay.NewWorld(t, db, func(cfg *config.Config) { dataDir = cfg.DataDir })
		ready(t, w, dataDir)
		w.Exec(`INSERT INTO agent_bundles (bundle_id, slug, name, branding, branding_hash) VALUES ('noslug00001', NULL, 'Old', '{"server_host":"h.example"}', 'h')`, nil)
		w.Exec(`INSERT INTO agent_bundles (bundle_id, slug, name, branding, branding_hash) VALUES ('noslug00002', NULL, 'Old', '{"server_host":"h.example"}', 'h')`, nil)
		for _, c := range []struct{ id, body, slug string }{
			{"noslug00001", `{"name":"New"}`, "new"},
			{"noslug00002", `{"name":"New","slug":""}`, "new-2"},
			{"noslug00001", `{"slug":"!!"}`, "new"},
		} {
			got := w.Do(replay.Request{Role: "admin", Method: "PUT", Path: "/api/generator/bundles/" + c.id, Body: replay.Text(c.body)})
			if slug, _ := bundleSlug(t, got); slug != c.slug {
				t.Errorf("%s %s: %d %s, want the slug %s", c.id, c.body, got.Status, got.Body, c.slug)
			}
		}
		if rows := w.Rows("SELECT bundle_id, name, slug FROM agent_bundles ORDER BY bundle_id", nil); rows != `[{"bundle_id":"noslug00001","name":"New","slug":"new"},{"bundle_id":"noslug00002","name":"New","slug":"new-2"}]` {
			t.Errorf("bundles: %s", rows)
		}
	})
}

// TestUpdateBundleWithNullProfile starts a change that brings no profile
// from the default profile when the stored one is JSON but no object, as
// for stored text that is no JSON, so the server's host is asked for
// (BUGS GN3: a stored null answered 500). With a profile the change goes
// through.
func TestUpdateBundleWithNullProfile(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		var dataDir string
		w := replay.NewWorld(t, db, func(cfg *config.Config) { dataDir = cfg.DataDir })
		ready(t, w, dataDir)
		const hostRequired = `{"success":false,"error":"Validation failed","errors":["server_host_required"],"details":["server_host_required"]}`
		for i, stored := range []string{"null", "[]", "5", `"text"`, "no json"} {
			id := "nullbr0000" + strconv.Itoa(i)
			w.Exec(`INSERT INTO agent_bundles (bundle_id, slug, name, branding, branding_hash) VALUES (?, ?, 'Null', ?, '')`, []any{id, "null-" + strconv.Itoa(i), stored})
			if got := w.Do(replay.Request{Role: "admin", Method: "PUT", Path: "/api/generator/bundles/" + id, Body: replay.Text(`{"name":"Null changed"}`)}); got.Status != 400 || got.Body != hostRequired {
				t.Errorf("stored %s: %d %s", stored, got.Status, got.Body)
			}
		}
		got := w.Do(replay.Request{Role: "admin", Method: "PUT", Path: "/api/generator/bundles/nullbr00000",
			Body: replay.Text(`{"name":"Null changed","branding":{"server_host":"h.example"}}`)})
		if slug, _ := bundleSlug(t, got); slug != "null-0" {
			t.Errorf("with a profile: %d %s", got.Status, got.Body)
		}
	})
}

// TestUpdateAnswersAQueueFailure answers a changed profile whose builds
// cannot be queued with the queue's error, as a new bundle is answered;
// the change itself is saved (BUGS GN4: the answer said success).
func TestUpdateAnswersAQueueFailure(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		var dataDir string
		w := replay.NewWorld(t, db, func(cfg *config.Config) { dataDir = cfg.DataDir })
		ready(t, w, dataDir)
		created := w.Do(replay.Request{Role: "admin", Method: "POST", Path: "/api/generator/bundles",
			Body: replay.Text(`{"name":"Acme","slug":"acme","branding":{"server_host":"h.example"}}`)})
		slug, id := bundleSlug(t, created)
		if slug != "acme" {
			t.Fatalf("create: %d %s", created.Status, created.Body)
		}
		w.Exec("DROP TABLE agent_bundle_builds", nil)
		got := w.Do(replay.Request{Role: "admin", Method: "PUT", Path: "/api/generator/bundles/" + id,
			Body: replay.Text(`{"name":"Acme two","branding":{"server_host":"other.example"}}`)})
		if got.Status != http.StatusServiceUnavailable || !strings.HasPrefix(got.Body, `{"success":false,"error":`) {
			t.Errorf("queue failed: %d %s", got.Status, got.Body)
		}
		if rows := w.Rows("SELECT name FROM agent_bundles WHERE slug = 'acme'", nil); rows != `[{"name":"Acme two"}]` {
			t.Errorf("change not saved: %s", rows)
		}
	})
}
