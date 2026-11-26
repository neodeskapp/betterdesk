package backup

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// partEntries are the archive names of each part, as the contents panel
// and the restore choices name them. An archive entry no part claims, or a
// part the contents answer leaves out, fails the tests below.
var partEntries = map[string][]string{
	"database":         {"console/data.json"},
	"server_data":      {"goserver/data.json"},
	"database_file":    {"console/auth.db", "goserver/db_v2.sqlite3"},
	"env":              {"console/.env"},
	"session_secret":   {"console/.session_secret"},
	"server_keys":      {"goserver/id_ed25519", "goserver/id_ed25519.pub", "goserver/.api_key"},
	"mesh_cert":        {"goserver/mesh_agent_server.pem"},
	"uploads":          {"console/uploads/"},
	"chat_files":       {"console/chat-files/"},
	"attachments":      {"console/attachments/"},
	"fonts":            {"console/fonts/"},
	"public_endpoints": {"console/public-endpoints.env"},
	"tls":              {"console/tls/"},
}

// partOf is the part an archive entry belongs to, "" for none.
func partOf(name string) string {
	for id, names := range partEntries {
		for _, n := range names {
			if name == n || (strings.HasSuffix(n, "/") && strings.HasPrefix(name, n)) {
				return id
			}
		}
	}
	return ""
}

// contentsAndArchive checks that the contents answer and a backup made
// right after it agree: every part is listed once, an included part has
// exactly the files it counts in the archive, a part left out has none
// and says why. It returns the parts by id.
func contentsAndArchive(t *testing.T, s *Service) map[string]Part {
	t.Helper()
	ctx := context.Background()
	parts := map[string]Part{}
	for _, p := range s.FullBackupStats(ctx).Parts {
		if _, dup := parts[p.ID]; dup {
			t.Errorf("part %s listed twice", p.ID)
		}
		parts[p.ID] = p
	}
	for id := range partEntries {
		if _, ok := parts[id]; !ok {
			t.Errorf("the contents answer does not list %s", id)
		}
	}
	archive, _, err := s.CreateFullBackup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	inArchive := map[string]int{}
	for _, e := range tarEntries(t, archive) {
		if e.name == "manifest.json" || e.name == "README.txt" {
			continue
		}
		id := partOf(e.name)
		if id == "" {
			t.Errorf("archive entry %s belongs to no part", e.name)
			continue
		}
		inArchive[id]++
	}
	for id, p := range parts {
		got := inArchive[id]
		switch {
		case p.Included && got == 0:
			t.Errorf("%s: the contents say included, the archive has none of it", id)
		case !p.Included && got > 0:
			t.Errorf("%s: the contents say not included (%s), the archive has %d files of it", id, p.Reason, got)
		case !p.Included && p.Reason == "":
			t.Errorf("%s: left out without a reason", id)
		case p.Included && p.Reason != "":
			t.Errorf("%s: included with a reason %q", id, p.Reason)
		}
		// the dumps are made when the backup is: one file each
		if p.Included && p.Files != got {
			t.Errorf("%s: the contents count %d files, the archive has %d", id, p.Files, got)
		}
	}
	return parts
}

// "Create a backup" said the archive holds the environment file while "In
// a backup now" said it does not, on a Docker install (BUGS L557). The
// contents answer and the archive are one list: on a native install the
// .env is in both, on Docker in neither, with the reason, also when the
// console wrote a copy of the public endpoints into a .env of its own.
func TestContentsAnswerIsWhatTheArchiveHolds(t *testing.T) {
	dialects(t, func(t *testing.T, dialect string) {
		in := newInstall(t, dialect)
		in.seed(t)

		parts := contentsAndArchive(t, in.service(t, "x"))
		if !parts["env"].Included || !parts["server_keys"].Included || parts["uploads"].Files != 3 {
			t.Errorf("native install: env %+v keys %+v uploads %+v", parts["env"], parts["server_keys"], parts["uploads"])
		}
		if want := dialect == "sqlite"; parts["database_file"].Included != want {
			t.Errorf("database file %+v on %s", parts["database_file"], dialect)
		} else if !want && parts["database_file"].Reason != "postgres" {
			t.Errorf("database file on PostgreSQL: reason %q, want postgres", parts["database_file"].Reason)
		}
		if p := parts["tls"]; p.Included || p.Reason != "not_configured" {
			t.Errorf("tls without paths: %+v", p)
		}
		for _, id := range []string{"database", "env", "session_secret", "server_keys", "tls"} {
			if !parts[id].Secret {
				t.Errorf("%s is not marked as holding secrets", id)
			}
		}
		if parts["uploads"].Secret || parts["fonts"].Secret {
			t.Error("uploads or fonts marked as secrets")
		}

		docker := in.service(t, "x")
		docker.o.Docker = true
		parts = contentsAndArchive(t, docker)
		if p := parts["env"]; p.Included || p.Reason != "docker" {
			t.Errorf("Docker install with the console's own .env: %+v, want left out because of Docker", p)
		}
		if err := os.Remove(in.envFile); err != nil {
			t.Fatal(err)
		}
		if p := contentsAndArchive(t, docker)["env"]; p.Included || p.Reason != "docker" {
			t.Errorf("Docker install without .env: %+v", p)
		}
		if p := contentsAndArchive(t, in.service(t, "x"))["env"]; p.Included || p.Reason != "missing" {
			t.Errorf("native install without .env: %+v", p)
		}

		if err := os.Remove(filepath.Join(in.dataDir, ".session_secret")); err != nil {
			t.Fatal(err)
		}
		fromEnv := in.service(t, "x")
		fromEnv.o.SessionSecretFromEnv = true
		if p := contentsAndArchive(t, fromEnv)["session_secret"]; p.Included || p.Reason != "environment" {
			t.Errorf("session secret from SESSION_SECRET: %+v", p)
		}
	})
}

// A restore on Docker leaves the .env of an archive out: nothing reads a
// .env in the container, the settings are the container's.
func TestDockerRestoreSkipsTheEnvFile(t *testing.T) {
	in := newInstall(t, "sqlite")
	in.seed(t)
	archive, _, err := in.service(t, "x").CreateFullBackup(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(in.envFile); err != nil {
		t.Fatal(err)
	}
	docker := in.service(t, "x")
	docker.o.Docker = true
	res, err := docker.RestoreFullBackup(context.Background(), archive, RestoreOptions{Env: true})
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(res.Restored, ".env") || !slices.Contains(res.Skipped, ".env") || len(res.Warnings) != 1 {
		t.Errorf("Docker restore of the .env: %+v", res)
	}
	if _, err := os.Stat(in.envFile); err == nil {
		t.Error("the restore wrote a .env on Docker")
	}
}
