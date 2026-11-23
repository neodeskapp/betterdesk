package updater

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// releasePublicKeyPEM is the release key of neodeskapp/neodesk as
// the owner gave it (DECISIONS A152).
const releasePublicKeyPEM = `-----BEGIN PUBLIC KEY-----
MCowBQYDK2VwAyEAiwissiXvSjeFFsG8SdKEZq11uAtRLWCrD7Ky1vfY2n4=
-----END PUBLIC KEY-----
`

// TestTheReleaseKeyIsCompiledIn: the binary carries the owner's release
// key, so every install checks signatures and refuses an unsigned release
// (L92).
func TestTheReleaseKeyIsCompiledIn(t *testing.T) {
	block, _ := pem.Decode([]byte(releasePublicKeyPEM))
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	keys := CompiledKeys()
	if len(keys) != 1 || !bytes.Equal(keys[0], parsed.(ed25519.PublicKey)) {
		t.Fatalf("compiled keys %x, want the release key %x", keys, parsed)
	}
	r := newTestRelease("3.6.0", strings.Repeat("a", 40), BranchStable)
	if trust := EvaluateTrust(r.manifestText, nil, keys, false); trust.OK {
		t.Errorf("an unsigned release passes with the compiled key: %+v", trust)
	}
}

// TestVerifyFile: a binary of a release signed with the key verifies; a
// changed byte of the binary or of a signed field of the manifest, another
// key, a missing signature and a name the manifest lacks are refused
// (L92).
func TestVerifyFile(t *testing.T) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	other, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	r := newTestRelease("3.6.0", strings.Repeat("b", 40), BranchStable)
	r.sign(private)
	dir := t.TempDir()
	binary := filepath.Join(dir, "download")
	if err := os.WriteFile(binary, r.binaries[asset], 0o755); err != nil {
		t.Fatal(err)
	}
	keys := Keys{public}
	m, err := VerifyFile(r.manifestText, r.signature, binary, asset, keys)
	if err != nil || m.Version != "3.6.0" {
		t.Fatalf("a signed release: %v, %v", m, err)
	}

	changed := filepath.Join(dir, "changed")
	data := append([]byte{}, r.binaries[asset]...)
	data[len(data)-2] ^= 1
	if err := os.WriteFile(changed, data, 0o755); err != nil {
		t.Fatal(err)
	}
	tampered := bytes.Replace(r.manifestText, []byte(`"sha": "bbbb`), []byte(`"sha": "cbbb`), 1)
	if bytes.Equal(tampered, r.manifestText) {
		t.Fatal("the manifest has no sha to change")
	}
	for name, c := range map[string]struct {
		manifest, signature []byte
		file, asset         string
		keys                Keys
	}{
		"a changed byte of the binary":   {r.manifestText, r.signature, changed, asset, keys},
		"a changed byte of the manifest": {tampered, r.signature, binary, asset, keys},
		"another key":                    {r.manifestText, r.signature, binary, asset, Keys{other}},
		"no signature":                   {r.manifestText, nil, binary, asset, keys},
		"no key":                         {r.manifestText, r.signature, binary, asset, nil},
		"a name the manifest lacks":      {r.manifestText, r.signature, binary, "neodesk-server-linux-riscv64", keys},
	} {
		if _, err := VerifyFile(c.manifest, c.signature, c.file, c.asset, c.keys); err == nil {
			t.Errorf("%s passed", name)
		}
	}
}
