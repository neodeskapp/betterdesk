package receiverapp_test

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/routetest"
	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/receiverapp"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

const msiBytes = "MSI of the generic support release"

// receiverWorld is a console on SQLite and PostgreSQL with the enrollment
// tokens of the signal server's table, a folder, and, with release, an
// imported generic Support Agent release with an x64 MSI.
func receiverWorld(t *testing.T, release bool, test func(t *testing.T, w *routetest.World)) {
	storetest.Each(t, func(t *testing.T, sdb *store.DB) {
		storetest.GoServer(t, sdb)
		ctx := context.Background()
		for _, row := range [][]any{
			{receiverapp.HashToken("0123456789abcdef0123456789abcdef"), "pending", "", 1, 0},
			{receiverapp.HashToken("usedusedusedusedusedusedusedused"), "active", "523456781", 1, 1},
			{receiverapp.HashToken("revokedrevokedrevokedrevokedrevo"), "revoked", "", 1, 0},
		} {
			if _, err := sdb.Exec(ctx, `INSERT INTO device_tokens (token_hash, token_prefix, name, status, peer_id, max_uses, use_count) VALUES (?, 'x', 'test', ?, ?, ?, ?)`, row...); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := sdb.Exec(ctx, `INSERT INTO folders (id, name) VALUES (7, 'Front office')`); err != nil {
			t.Fatal(err)
		}
		w := routetest.New(t, routetest.Options{DB: sdb})
		if release {
			dir := filepath.Join(w.DataDir, "modules", "neodesk-support-releases", "support-v1.2.0")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			manifest := `{"release_tag":"support-v1.2.0","assets":[
				{"name":"neodesk-support-generic-windows-x86_64.zip","platform":"windows","arch":"x86_64","format":"zip"},
				{"name":"neodesk-support-generic-windows-x86_64.msi","platform":"windows","arch":"x86_64","format":"msi"}]}`
			for name, content := range map[string]string{
				"manifest.json": manifest,
				"neodesk-support-generic-windows-x86_64.msi": msiBytes,
				"neodesk-support-generic-windows-x86_64.zip": "zip",
			} {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
					t.Fatal(err)
				}
			}
		}
		test(t, w)
	})
}

// The setup names the server's values and the release's MSI; it is for
// accounts with enrollment.manage.
func TestReceiverSetupNamesTheServerAndTheMSI(t *testing.T) {
	receiverWorld(t, true, func(t *testing.T, w *routetest.World) {
		got := w.As("admin").Do(http.MethodGet, "/api/panel/receiver/setup", "")
		var answer struct {
			Success bool               `json:"success"`
			Server  receiverapp.Server `json:"server"`
			Release *receiverapp.Release
		}
		if err := json.Unmarshal([]byte(got.Body), &answer); err != nil || got.Status != http.StatusOK || !answer.Success {
			t.Fatalf("setup: %d %s", got.Status, got.Body)
		}
		if answer.Release == nil || answer.Release.Tag != "support-v1.2.0" || len(answer.Release.Packages) != 1 ||
			answer.Release.Packages[0] != (receiverapp.Package{Arch: "x86_64", Name: "neodesk-support-generic-windows-x86_64.msi", Size: int64(len(msiBytes))}) {
			t.Errorf("release = %+v, want the x64 MSI of support-v1.2.0", answer.Release)
		}
		if answer.Server.IDServer == "" {
			t.Errorf("server = %+v, want the ID server", answer.Server)
		}
		for _, role := range []string{"viewer", "operator"} {
			if got := w.As(role).Do(http.MethodGet, "/api/panel/receiver/setup", ""); got.Status != http.StatusForbidden {
				t.Errorf("%s: %d, want 403", role, got.Status)
			}
		}
	})
}

// Without an imported release the setup says so and the download is 409.
func TestReceiverInstallerNeedsARelease(t *testing.T) {
	receiverWorld(t, false, func(t *testing.T, w *routetest.World) {
		if got := w.As("admin").Do(http.MethodGet, "/api/panel/receiver/setup", ""); !strings.Contains(got.Body, `"release":null`) {
			t.Errorf("setup without a release: %s", got.Body)
		}
		got := w.As("admin").Do(http.MethodPost, "/api/panel/receiver/installer", `{"token":"0123456789abcdef0123456789abcdef"}`)
		if got.Status != http.StatusConflict || !strings.Contains(got.Body, `"code":"no_release"`) {
			t.Errorf("download without a release: %d %s", got.Status, got.Body)
		}
	})
}

// The download is the release's MSI as neodesk-support.msi with an
// install.cmd that installs it silently with the token and the group.
func TestReceiverInstallerCarriesTheProperties(t *testing.T) {
	receiverWorld(t, true, func(t *testing.T, w *routetest.World) {
		got := w.As("admin").Do(http.MethodPost, "/api/panel/receiver/installer", `{"token":"0123456789abcdef0123456789abcdef","group":7}`)
		if got.Status != http.StatusOK || got.Header.Get("Content-Type") != "application/zip" {
			t.Fatalf("download: %d %s %.200s", got.Status, got.Header.Get("Content-Type"), got.Body)
		}
		if cd := got.Header.Get("Content-Disposition"); !strings.HasPrefix(cd, `attachment; filename="neodesk-support-`) {
			t.Errorf("Content-Disposition = %q", cd)
		}
		files := unzip(t, got.Body)
		if files[receiverapp.MSIName] != msiBytes {
			t.Errorf("%s = %q, want the release's MSI", receiverapp.MSIName, files[receiverapp.MSIName])
		}
		cmd := files["install.cmd"]
		for _, want := range []string{
			`msiexec /i "%~dp0neodesk-support.msi" /qn SERVER=`,
			" TOKEN=0123456789abcdef0123456789abcdef GROUP=7\r\n",
			"net session >nul 2>&1\r\n",
		} {
			if !strings.Contains(cmd, want) {
				t.Errorf("install.cmd lacks %q:\n%s", want, cmd)
			}
		}
		if !strings.Contains(cmd, " KEY=") && !strings.Contains(cmd, " API=") {
			t.Errorf("install.cmd names neither KEY nor API:\n%s", cmd)
		}
	})
}

// What can't make a working install is refused before anything is sent.
func TestReceiverInstallerRefusesWhatCantWork(t *testing.T) {
	receiverWorld(t, true, func(t *testing.T, w *routetest.World) {
		for body, code := range map[string]string{
			`{}`:                "invalid_token",
			`{"token":"short"}`: "invalid_token",
			`{"token":"0123456789abcdef0123456789abcdee"}`:              "token_unknown",
			`{"token":"revokedrevokedrevokedrevokedrevo"}`:              "token_unknown",
			`{"token":"usedusedusedusedusedusedusedused"}`:              "token_used",
			`{"token":"0123456789abcdef0123456789abcdef","group":99}`:   "invalid_group",
			`{"token":"0123456789abcdef0123456789abcdef","group":"x"}`:  "invalid_group",
			`{"token":"0123456789abcdef0123456789abcdef","arch":"arm"}`: "no_release",
		} {
			got := w.As("admin").Do(http.MethodPost, "/api/panel/receiver/installer", body)
			if got.Status < 400 || !strings.Contains(got.Body, `"code":"`+code+`"`) {
				t.Errorf("%s: %d %s, want %s", body, got.Status, got.Body, code)
			}
		}
		if got := w.As("viewer").Do(http.MethodPost, "/api/panel/receiver/installer", `{"token":"0123456789abcdef0123456789abcdef"}`); got.Status != http.StatusForbidden {
			t.Errorf("viewer: %d, want 403", got.Status)
		}
	})
}

func unzip(t *testing.T, body string) map[string]string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader([]byte(body)), int64(len(body)))
	if err != nil {
		t.Fatalf("no zip: %v", err)
	}
	out := map[string]string{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(rc)
		rc.Close()
		out[f.Name] = string(data)
	}
	return out
}

// The command and install.cmd quote what msiexec would split, and the
// batch file doubles a percent sign.
func TestReceiverCommandQuotesValues(t *testing.T) {
	p := receiverapp.Properties{
		Server: receiverapp.Server{IDServer: "support.example.com:21116", Key: "abc+/=", APIServer: "https://support.example.com/a b"},
		Token:  "tok%en", Group: 12,
	}
	want := `msiexec /i neodesk-support.msi /qn SERVER=support.example.com:21116 KEY=abc+/= API="https://support.example.com/a b" TOKEN=tok%en GROUP=12`
	if got := receiverapp.Command(p); got != want {
		t.Errorf("Command:\n got  %s\n want %s", got, want)
	}
	if cmd := receiverapp.InstallCmd(p); !strings.Contains(cmd, ` TOKEN=tok%%en GROUP=12`) {
		t.Errorf("install.cmd does not double the percent sign:\n%s", cmd)
	}
	if got := receiverapp.Command(receiverapp.Properties{Server: receiverapp.Server{IDServer: "h", Key: "k"}, Token: "t"}); got != "msiexec /i neodesk-support.msi /qn SERVER=h KEY=k TOKEN=t" {
		t.Errorf("without API and GROUP: %s", got)
	}
}
