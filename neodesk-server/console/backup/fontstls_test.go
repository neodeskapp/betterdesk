package backup

import (
	"context"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// withFontsAndTLS is the service of an install with installed fonts, TLS
// files and saved public endpoints at their usual places.
func (in *install) withFontsAndTLS(t *testing.T) *Service {
	t.Helper()
	s := in.service(t, "x")
	s.o.FontsDir = filepath.Join(in.dataDir, "fonts")
	tls := filepath.Join(in.keysPath, "ssl")
	s.o.CertFile, s.o.KeyFile, s.o.CAFile = filepath.Join(tls, "neodesk.crt"), filepath.Join(tls, "neodesk.key"), filepath.Join(tls, "ca.crt")
	s.o.EndpointsFile = filepath.Join(in.dataDir, "public-endpoints.env")
	return s
}

// TestFullBackupCarriesFontsTLSAndEndpoints: a full backup holds the
// installed fonts, the console's TLS certificate, key and CA and the
// public endpoints file, and a restore puts each back: the fonts with the
// uploads, the TLS files with the secrets (the key as 0600), the
// endpoints with the console data (L121).
func TestFullBackupCarriesFontsTLSAndEndpoints(t *testing.T) {
	ctx := context.Background()
	src := newInstall(t, "sqlite")
	s := src.withFontsAndTLS(t)
	fonts := map[string]string{"inter/inter-400.woff2": "font bytes", "fonts.css": "@font-face {}"}
	writeFiles(t, s.o.FontsDir, fonts)
	tls := map[string]string{"neodesk.crt": "CERT", "neodesk.key": "KEY", "ca.crt": "CA"}
	writeFiles(t, filepath.Dir(s.o.CertFile), tls)
	if err := os.WriteFile(s.o.EndpointsFile, []byte("PUBLIC_SERVER_ID=desk.example.org\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	archive, _, err := s.CreateFullBackup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	files, err := ExtractTarGz(archive, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"console/fonts/inter/inter-400.woff2": "font bytes", "console/fonts/fonts.css": "@font-face {}",
		"console/tls/certificate.pem": "CERT", "console/tls/private-key.pem": "KEY", "console/tls/ca.pem": "CA",
		"console/public-endpoints.env": "PUBLIC_SERVER_ID=desk.example.org\n",
	} {
		if data, ok := files.Get(name); !ok || string(data) != content {
			t.Errorf("%s in the archive: %q %v", name, data, ok)
		}
	}

	dst := newInstall(t, "sqlite")
	d := dst.withFontsAndTLS(t)
	res, err := d.RestoreFullBackup(ctx, archive, RestoreOptions{Database: true, Uploads: true, Secrets: true})
	if err != nil || len(res.Warnings) != 0 {
		t.Fatalf("restore: %+v %v", res, err)
	}
	for _, part := range []string{"fonts (2 files)", "public endpoints", "secrets/keys"} {
		if !slices.Contains(res.Restored, part) {
			t.Errorf("restored %q, want %q among them", res.Restored, part)
		}
	}
	if got := filesBelow(t, d.o.FontsDir); !maps.Equal(got, fonts) {
		t.Errorf("fonts after the restore: %q", got)
	}
	if got := filesBelow(t, filepath.Dir(d.o.CertFile)); !maps.Equal(got, tls) {
		t.Errorf("TLS files after the restore: %q", got)
	}
	if info, err := os.Stat(d.o.KeyFile); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("the restored key: %v %v", info, err)
	}
	if data, err := os.ReadFile(d.o.EndpointsFile); err != nil || string(data) != "PUBLIC_SERVER_ID=desk.example.org\n" {
		t.Errorf("the restored endpoints: %q %v", data, err)
	}

	// Without the secrets the TLS files stay out; without the uploads the
	// fonts do
	other := newInstall(t, "sqlite")
	o := other.withFontsAndTLS(t)
	if _, err := o.RestoreFullBackup(ctx, archive, RestoreOptions{Database: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(o.o.KeyFile); err == nil {
		t.Error("the TLS key was restored without the secrets")
	}
	if got := filesBelow(t, o.o.FontsDir); len(got) != 0 {
		t.Errorf("fonts restored without the uploads: %q", got)
	}
}
