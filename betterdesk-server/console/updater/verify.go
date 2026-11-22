package updater

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
)

// VerifyFile checks a downloaded binary the way the updater checks the one
// it installs: the manifest's form, its signature by one of keys, and the
// file's SHA-256 and size against the manifest's entry named name. It
// returns the manifest. The installers call it through "verify-release"
// on the installed binary, with the keys compiled into it.
func VerifyFile(manifest, signature []byte, file, name string, keys Keys) (*Manifest, error) {
	if len(keys) == 0 {
		return nil, errors.New("no release key to check the signature with")
	}
	if len(signature) == 0 {
		return nil, errors.New("the release has no signature (manifest.sig)")
	}
	// The form is checked against the owner, repository and branch the
	// manifest names: the signature is what ties it to this product
	var named Manifest
	if err := json.Unmarshal(manifest, &named); err != nil {
		return nil, fmt.Errorf("manifest.json: %w", err)
	}
	m, err := ParseManifest(manifest, ManifestRules{Owner: named.Owner, Repo: named.Repo, Branch: named.Branch})
	if err != nil {
		return nil, err
	}
	if !VerifySignature(CanonicalPayload(m.Signed()), signature, keys) {
		return nil, errors.New("the signature of the manifest does not verify against the release key")
	}
	var entry *ManifestFile
	for i := range m.Files {
		if m.Files[i].Path == name {
			entry = &m.Files[i]
		}
	}
	if entry == nil {
		return nil, fmt.Errorf("the manifest names no file %s", name)
	}
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	h := sha256.New()
	size, err := io.Copy(h, f)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", file, err)
	}
	if hex.EncodeToString(h.Sum(nil)) != entry.SHA256 || (entry.Size != 0 && size != entry.Size) {
		return nil, fmt.Errorf("%s is not the %s the manifest names", file, name)
	}
	return m, nil
}
