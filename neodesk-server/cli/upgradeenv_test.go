package cli

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/managedconfig"
	"github.com/neodeskapp/neodesk/neodesk-server/console/serverinfo"
)

// loadLikeTheServer reads env file text as the server's env loader does:
// the first value of a key that is not empty, trimmed.
func loadLikeTheServer(text string) map[string]string {
	values := map[string]string{}
	for _, line := range serverinfo.ParseEnvLines(text).Entries {
		if line.Type == serverinfo.EnvPair && values[line.Key] == "" {
			values[line.Key] = strings.TrimSpace(line.Value)
		}
	}
	return values
}

func settings(list []upgradeSetting) map[string]string {
	m := map[string]string{}
	for _, s := range list {
		m[s.Key] = s.Value
	}
	return m
}

// A Node console's .env as the installer wrote it, with what older
// installers and updaters added: the plan of spec 4.3.
func TestUpgradeEnvOnAnInstallersFile(t *testing.T) {
	subs, err := serverinfo.BuildEnvSubstitutions(serverinfo.EnvSubstitutionInput{Config: serverinfo.EnvConfig{RustdeskDir: "/opt/neodesk", DataDir: "/opt/NeoDeskConsole/data"}})
	if err != nil {
		t.Fatal(err)
	}
	old := serverinfo.ApplySubstitutions(managedconfig.Template(), subs) +
		"WEB_UI_DIR=/opt/NeoDeskConsole/web-ui/build\nNODE_EXTRA_CA_CERTS=/opt/neodesk/ssl/neodesk.crt\nGOCACHE=/var/cache/go\n" +
		"CONSOLE_NODE_UPSTREAM=http://127.0.0.1:5001\n# GOMODCACHE=/x\n"
	plan, err := planUpgrade(old, "/opt/NeoDeskConsole/.env", "systemd")
	if err != nil {
		t.Fatal(err)
	}
	added := settings(plan.Added)
	want := map[string]string{
		"CONSOLE_PORT": "5000", "SIGNAL_PORT": "21116", "RELAY_PORT": "21117",
		"CONSOLE_DIR": "/opt/NeoDeskConsole", "NEODESK_ENV_FILE": "/opt/NeoDeskConsole/.env", "NEODESK_SUPERVISOR": "systemd",
	}
	if runtime.GOOS == "windows" {
		want["CONSOLE_DIR"] = filepath.Dir("/opt/NeoDeskConsole/.env")
	}
	for k, v := range want {
		if added[k] != v {
			t.Errorf("added %s=%q, want %q", k, added[k], v)
		}
	}
	// The template sets HOST and GO_API_PORT: carried, not added
	for _, k := range []string{"HOST", "GO_API_PORT", "PORT", "SESSION_SECRET", "DB_PATH", "HTTPS_PORT"} {
		if _, ok := added[k]; ok {
			t.Errorf("%s was added", k)
		}
		if !slices.Contains(plan.Kept, k) && strings.Contains(old, "\n"+k+"=") {
			t.Errorf("%s is not kept", k)
		}
	}
	if plan.FromPort != "5000" {
		t.Errorf("CONSOLE_PORT from PORT: %q", plan.FromPort)
	}
	if !slices.Equal(plan.Commented, []string{"WEB_UI_DIR", "GOCACHE"}) {
		t.Errorf("commented %v", plan.Commented)
	}
	if !slices.Equal(plan.Removed, []string{"NODE_EXTRA_CA_CERTS", "CONSOLE_NODE_UPSTREAM"}) {
		t.Errorf("removed %v", plan.Removed)
	}
	after := loadLikeTheServer(plan.Text)
	before := loadLikeTheServer(old)
	for key, value := range before {
		_, commented := commentedKeys[key]
		_, removed := removedKeys[key]
		switch {
		case commented || removed:
			if _, ok := after[key]; ok {
				t.Errorf("%s is still set", key)
			}
		case after[key] != value:
			t.Errorf("%s: %q, before %q", key, after[key], value)
		}
	}
	// WEB_UI_DIR appears twice in the template file (empty) and the
	// addition; both lines are commented
	if strings.Count(plan.Text, "\n# WEB_UI_DIR=") != 2 {
		t.Errorf("WEB_UI_DIR lines:\n%s", plan.Text)
	}
	if again, _ := planUpgrade(plan.Text, "/opt/NeoDeskConsole/.env", "systemd"); again.Changed() {
		t.Errorf("a second run changes %+v", again)
	}
}

func TestUpgradeEnvRules(t *testing.T) {
	const file = "/srv/console/.env"
	for _, c := range []struct {
		name, in string
		port     string
		err      bool
		text     string
	}{
		{name: "port", in: "PORT=5100\n", port: "5100"},
		{name: "no port", in: "A=1\n", port: "5000"},
		{name: "port read like parseInt", in: "PORT= 5100abc\n", port: "5100"},
		{name: "port zero", in: "PORT=0\n", port: "5000"},
		{name: "port empty then set", in: "PORT=\nPORT=6000\n", port: "6000"},
		{name: "port twice", in: "PORT=6000\nPORT=7000\n", port: "6000"},
		{name: "port too big", in: "PORT=70000\n", err: true},
		{name: "port negative", in: "PORT=-1\n", err: true},
		{name: "console port kept", in: "PORT=70000\nCONSOLE_PORT=0\n", port: ""},
		{name: "crlf", in: "PORT=5000\r\nA=1\r\n", port: "5000",
			text: "PORT=5000\r\nA=1\r\n\r\n# Added by neodesk-server upgrade-env: the console runs inside the server\r\nCONSOLE_PORT=5000\r\nHOST=127.0.0.1\r\n"},
		{name: "no line end", in: "A=1", port: "5000", text: "A=1\n\n# Added by neodesk-server upgrade-env: the console runs inside the server\nCONSOLE_PORT=5000\n"},
		{name: "empty", in: "", port: "5000", text: "# Added by neodesk-server upgrade-env: the console runs inside the server\nCONSOLE_PORT=5000\n"},
		{name: "bom", in: byteOrderMark + "PORT=5200\n", port: "5200", text: byteOrderMark + "PORT=5200\n\n# Added"},
		{name: "bom before a commented key", in: byteOrderMark + "GOCACHE=x\n", port: "5000", text: byteOrderMark + "# Commented out by neodesk-server upgrade-env: "},
		{name: "bom alone", in: byteOrderMark, port: "5000", text: byteOrderMark + "# Added by neodesk-server upgrade-env: the console runs inside the server\n"},
		{name: "commented", in: "  GOCACHE=a\nGOCACHE=b\n# GOCACHE=c\n", port: "5000",
			text: "# Commented out by neodesk-server upgrade-env: only the old console's updater built the server from source\n#   GOCACHE=a\n" +
				"# Commented out by neodesk-server upgrade-env: only the old console's updater built the server from source\n# GOCACHE=b\n# GOCACHE=c\n"},
		{name: "removed", in: "NODE_EXTRA_CA_CERTS=x\nA=1\nNODE_EXTRA_CA_CERTS=y", port: "5000", text: "A=1\n\n# Added"},
	} {
		plan, err := planUpgrade(c.in, file, "nssm")
		if c.err {
			if !errors.Is(err, errNotAPort) {
				t.Errorf("%s: %v, want a refused port", c.name, err)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if plan.FromPort != c.port || (c.port != "" && settings(plan.Added)["CONSOLE_PORT"] != c.port) {
			t.Errorf("%s: console port %q (%v), want %q", c.name, plan.FromPort, plan.Added, c.port)
		}
		if c.text != "" && !strings.HasPrefix(plan.Text, c.text) {
			t.Errorf("%s:\n got %q\nwant %q...", c.name, plan.Text, c.text)
		}
		if settings(plan.Added)["NEODESK_SUPERVISOR"] != "nssm" {
			t.Errorf("%s: supervisor %v", c.name, plan.Added)
		}
		again, err := planUpgrade(plan.Text, file, "nssm")
		if err != nil || again.Changed() {
			t.Errorf("%s: a second run: %v %+v", c.name, err, again)
		}
	}
}

func TestUpgradeEnvCommand(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, ".env")
	original := "PORT=5100\nSESSION_SECRET=topsecret-value\nWEB_UI_DIR=/old/build\n"
	if err := os.WriteFile(file, []byte(original), 0o640); err != nil {
		t.Fatal(err)
	}
	run := func(vars map[string]string, args ...string) (int, string, string) {
		env, out, errOut := testEnv(vars)
		env.GOOS = "linux"
		return UpgradeEnv(args, env), out.String(), errOut.String()
	}

	status, out, _ := run(nil, "--from", file)
	if status != 0 || !strings.Contains(out, "map      PORT -> CONSOLE_PORT=5100") || !strings.Contains(out, "comment  WEB_UI_DIR") ||
		!strings.Contains(out, "keep     SESSION_SECRET") || !strings.Contains(out, "Nothing written") {
		t.Fatalf("plan: %d\n%s", status, out)
	}
	if strings.Contains(out, "topsecret") {
		t.Error("the plan shows a value it carries")
	}
	if raw, _ := os.ReadFile(file); string(raw) != original {
		t.Fatal("the plan changed the file")
	}
	if _, err := os.Stat(file + ".pre-go"); err == nil {
		t.Fatal("the plan made a backup")
	}

	status, out, _ = run(nil, "--from", file, "--write")
	if status != 0 || !strings.Contains(out, "Written: "+file) {
		t.Fatalf("write: %d\n%s", status, out)
	}
	backup, err := os.ReadFile(file + ".pre-go")
	if err != nil || string(backup) != original {
		t.Fatalf("backup %q %v", backup, err)
	}
	written, _ := os.ReadFile(file)
	plan, _ := planUpgrade(original, file, "systemd")
	if string(written) != plan.Text || !strings.Contains(string(written), "\nNEODESK_SUPERVISOR=systemd\n") {
		t.Errorf("written:\n%s", written)
	}
	if runtime.GOOS != "windows" {
		if info, _ := os.Stat(file); info.Mode().Perm() != 0o640 {
			t.Errorf("the env file's mode is %o now", info.Mode().Perm())
		}
		if info, _ := os.Stat(file + ".pre-go"); info.Mode().Perm() != 0o600 {
			t.Errorf("the backup's mode is %o", info.Mode().Perm())
		}
	}

	// Again: nothing to change, the backup stays the original
	status, out, _ = run(nil, "--from", file, "--write")
	if status != 0 || !strings.Contains(out, "Nothing to change.") {
		t.Errorf("second write: %d\n%s", status, out)
	}
	if raw, _ := os.ReadFile(file + ".pre-go"); string(raw) != original {
		t.Error("the second run replaced the backup")
	}

	// A file changed after an earlier run keeps that run's backup
	if err := os.WriteFile(file, []byte("PORT=5300\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	status, out, _ = run(map[string]string{"DOCKER": "true"}, "--from", file, "--write")
	if status != 0 || !strings.Contains(out, "from an earlier run is kept") {
		t.Errorf("earlier backup: %d\n%s", status, out)
	}
	if raw, _ := os.ReadFile(file + ".pre-go"); string(raw) != original {
		t.Error("the earlier backup was replaced")
	}
	if raw, _ := os.ReadFile(file); !strings.Contains(string(raw), "NEODESK_SUPERVISOR=docker\n") || !strings.Contains(string(raw), "CONSOLE_PORT=5300\n") {
		t.Errorf("docker run:\n%s", raw)
	}

	// A file that gets shorter (only a key taken out) has nothing of its
	// old end left
	short := filepath.Join(dir, "short.env")
	complete := "CONSOLE_PORT=5000\nHOST=127.0.0.1\nSIGNAL_PORT=21116\nRELAY_PORT=21117\nGO_API_PORT=21114\nCONSOLE_DIR=/c\nNEODESK_ENV_FILE=/c/.env\nNEODESK_SUPERVISOR=systemd\n"
	if err := os.WriteFile(short, []byte(complete+"NODE_EXTRA_CA_CERTS="+strings.Repeat("x", 200)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if status, _, _ := run(nil, "--from", short, "--write"); status != 0 {
		t.Fatalf("shorter: %d", status)
	}
	if raw, _ := os.ReadFile(short); string(raw) != complete {
		t.Errorf("shorter file:\n%q", raw)
	}

	status, _, _ = run(nil, "--from", file, "--write", "--supervisor", "task")
	if status != 0 {
		t.Errorf("--supervisor: %d", status)
	}

	for _, args := range [][]string{nil, {"--from"}, {"--from", ""}, {"--write"}, {"--from", file, "--bogus"}, {"--from", file, "--supervisor"}} {
		if status, _, errOut := run(nil, args...); status != 2 || !strings.HasPrefix(errOut, "Usage: neodesk-server upgrade-env") {
			t.Errorf("%q: %d %q", args, status, errOut)
		}
	}
	if status, _, errOut := run(nil, "--from", filepath.Join(dir, "missing")); status != 1 || !strings.Contains(errOut, "missing") {
		t.Errorf("missing file: %d %q", status, errOut)
	}
	bad := filepath.Join(dir, "bad.env")
	if err := os.WriteFile(bad, []byte("PORT=99999\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if status, _, errOut := run(nil, "--from", bad, "--write"); status != 1 || !strings.Contains(errOut, "set CONSOLE_PORT") {
		t.Errorf("bad port: %d %q", status, errOut)
	}
	if raw, _ := os.ReadFile(bad); string(raw) != "PORT=99999\n" {
		t.Error("a refused file was written")
	}
	if _, err := os.Stat(bad + ".pre-go"); err == nil {
		t.Error("a refused file got a backup")
	}
}

func TestUpgradeEnvSupervisorByPlatform(t *testing.T) {
	file := filepath.Join(t.TempDir(), ".env")
	for _, c := range []struct {
		goos   string
		docker string
		want   string
	}{{"linux", "", "systemd"}, {"windows", "", "nssm"}, {"linux", "true", "docker"}, {"windows", "true", "docker"},
		{"linux", "YES", "docker"}, {"linux", "1", "docker"}, {"linux", "off", "systemd"}} {
		if err := os.WriteFile(file, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		env, out, _ := testEnv(map[string]string{"DOCKER": c.docker})
		env.GOOS = c.goos
		if status := UpgradeEnv([]string{"--from", file}, env); status != 0 || !strings.Contains(out.String(), "add      NEODESK_SUPERVISOR="+c.want+"\n") {
			t.Errorf("%+v: %d\n%s", c, status, out)
		}
	}
}
