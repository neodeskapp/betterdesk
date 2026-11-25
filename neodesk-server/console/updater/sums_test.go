package updater

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"strings"
	"testing"
)

// A release's SHA256SUMS.sig signs its SHA256SUMS behind a line that
// names what it is, so the signature of a file list never passes for a
// manifest's and the other way round; SumOf reads the list as
// "sha256sum -c" writes it.
func TestTheFileListOfARelease(t *testing.T) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	sums := []byte(strings.Repeat("a", 64) + "  install.sh\n" + strings.Repeat("b", 64) + " *neodesk.sh\n")
	sig := []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(private, SumsPayload(sums))) + "\n")
	if !VerifySignature(SumsPayload(sums), sig, Keys{public}) {
		t.Error("the signature of the file list does not verify")
	}
	if VerifySignature(sums, sig, Keys{public}) {
		t.Error("the signature verifies without the line in front")
	}
	if !strings.HasPrefix(string(SumsPayload(sums)), "neodesk-release-files-v1\n") {
		t.Errorf("payload %q", SumsPayload(sums))
	}
	for name, want := range map[string]string{"install.sh": strings.Repeat("a", 64), "neodesk.sh": strings.Repeat("b", 64), "neodesk.ps1": "", "install": ""} {
		if got := SumOf(sums, name); got != want {
			t.Errorf("SumOf(%q) = %q, want %q", name, got, want)
		}
	}
}
