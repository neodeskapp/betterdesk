package signinvault

import (
	"bytes"
	"encoding/base64"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestSealedValuesOpenOnlyForTheirDeviceAndField: a value opens for the
// device and field it was sealed for, and for no other device, no other
// field, no other key and not after a change of one byte.
func TestSealedValuesOpenOnlyForTheirDeviceAndField(t *testing.T) {
	v, err := FromKey(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	const secret = "Gl0b@l-ä€\\pass"
	nonce, ct, err := v.Seal("DEVICE1", FieldPassword, secret)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(ct, secret) {
		t.Fatal("the ciphertext holds the value")
	}
	if got, err := v.Open("DEVICE1", FieldPassword, nonce, ct, KeyID); err != nil || got != secret {
		t.Fatalf("open: %q %v", got, err)
	}
	if _, err := v.Open("DEVICE2", FieldPassword, nonce, ct, KeyID); err == nil {
		t.Error("a password sealed for DEVICE1 opens for DEVICE2")
	}
	if _, err := v.Open("DEVICE1", FieldUsername, nonce, ct, KeyID); err == nil {
		t.Error("a password opens as the account name")
	}
	if _, err := v.Open("DEVICE1", FieldPassword, nonce, ct, "v1"); err == nil {
		t.Error("a row with another key id opens")
	}
	other, _ := FromKey(bytes.Repeat([]byte{8}, 32))
	if _, err := other.Open("DEVICE1", FieldPassword, nonce, ct, KeyID); err == nil {
		t.Error("another key opens the value")
	}
	raw, _ := base64.RawURLEncoding.DecodeString(ct)
	raw[0] ^= 1
	if _, err := v.Open("DEVICE1", FieldPassword, nonce, base64.RawURLEncoding.EncodeToString(raw), KeyID); err == nil {
		t.Error("a changed ciphertext opens")
	}
	if n2, ct2, _ := v.Seal("DEVICE1", FieldPassword, secret); n2 == nonce || ct2 == ct {
		t.Error("two seals of the same value are the same")
	}
}

// TestLoadMakesTheKeyFileOnceAndKeepsIt: the first Load makes the key file
// with mode 0600, later loads read the same key, and a file that holds no
// key refuses to load.
func TestLoadMakesTheKeyFileOnceAndKeepsIt(t *testing.T) {
	dir := t.TempDir()
	first, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, KeyFileName)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Errorf("key file mode %v, want 0600", info.Mode().Perm())
	}
	nonce, ct, err := first.Seal("D", FieldUsername, "admin")
	if err != nil {
		t.Fatal(err)
	}
	second, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := second.Open("D", FieldUsername, nonce, ct, KeyID); err != nil || got != "admin" {
		t.Fatalf("a second load does not open the first one's value: %q %v", got, err)
	}
	if err := os.WriteFile(path, []byte("not a key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err == nil {
		t.Error("a key file without a key loads")
	}
}
