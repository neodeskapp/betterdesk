package updater

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type vectorFile struct {
	Seed        string `json:"seed"`
	PublicKey   string `json:"publicKey"`
	ForeignSeed string `json:"foreignSeed"`
	Manifests   []struct {
		Manifest struct {
			Owner, Repo, Branch, SHA string
			Files                    []struct {
				Path   string `json:"path"`
				SHA256 string `json:"sha256"`
			} `json:"files"`
		} `json:"manifest"`
		Payload   string `json:"payload"`
		Signature string `json:"signature"`
	} `json:"manifests"`
	Verify []struct {
		Index     int    `json:"index"`
		Signature string `json:"signature"`
		Key       string `json:"key"`
		OK        bool   `json:"ok"`
	} `json:"verify"`
	Trust []struct {
		Key          string  `json:"key"`
		Required     string  `json:"required"`
		Signature    *string `json:"signature"`
		WithManifest bool    `json:"withManifest"`
		Result       Trust   `json:"result"`
	} `json:"trust"`
}

func loadVectors(t *testing.T) vectorFile {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "manifest_signature_vectors.json"))
	if err != nil {
		t.Fatal(err)
	}
	var v vectorFile
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatal(err)
	}
	if len(v.Manifests) < 1000 || len(v.Verify) < 1000 || len(v.Trust) != 256 {
		t.Fatalf("vectors: %d manifests, %d verifications, %d trust cases", len(v.Manifests), len(v.Verify), len(v.Trust))
	}
	return v
}

func signedFields(v vectorFile, i int) SignedFields {
	m := v.Manifests[i].Manifest
	f := SignedFields{Owner: m.Owner, Repo: m.Repo, Branch: m.Branch, SHA: m.SHA}
	for _, file := range m.Files {
		f.Files = append(f.Files, SignedFile{Path: file.Path, SHA256: file.SHA256})
	}
	return f
}

// The canonical text is upstream's byte for byte, and a signature made
// with the same key is the same signature.
func TestCanonicalPayloadAndSignatureMatchUpstream(t *testing.T) {
	v := loadVectors(t)
	seed, err := hex.DecodeString(v.Seed)
	if err != nil {
		t.Fatal(err)
	}
	private := ed25519.NewKeyFromSeed(seed)
	if got := base64.StdEncoding.EncodeToString(private.Public().(ed25519.PublicKey)); got != v.PublicKey {
		t.Fatalf("public key of the seed %s, upstream %s", got, v.PublicKey)
	}
	for i, vec := range v.Manifests {
		want, _ := base64.StdEncoding.DecodeString(vec.Payload)
		got := CanonicalPayload(signedFields(v, i))
		if !bytes.Equal(got, want) {
			t.Fatalf("manifest %d: payload\n%q\nupstream\n%q", i, got, want)
		}
		if sig := base64.StdEncoding.EncodeToString(ed25519.Sign(private, got)); sig != vec.Signature {
			t.Fatalf("manifest %d: signature %s, upstream %s", i, sig, vec.Signature)
		}
		if !VerifySignature(got, []byte(vec.Signature), ParseKeys(v.PublicKey)) {
			t.Fatalf("manifest %d: upstream's signature does not verify", i)
		}
	}
}

// Every signature and key form upstream's verifier was asked about gets
// the same answer.
func TestVerifySignatureMatchesUpstream(t *testing.T) {
	v := loadVectors(t)
	for _, vec := range v.Verify {
		payload := CanonicalPayload(signedFields(v, vec.Index))
		if got := VerifySignature(payload, []byte(vec.Signature), ParseKeys(vec.Key)); got != vec.OK {
			t.Errorf("manifest %d, signature %q, key %q: %v, upstream %v", vec.Index, vec.Signature, vec.Key, got, vec.OK)
		}
	}
}

// evaluateUpdateTrust gives the same answer for every combination of
// key, requirement, signature and manifest, except where a key is set and
// the signature is missing: upstream lets the update through (BUGS UPD1),
// A28 refuses it.
func TestEvaluateTrustMatchesUpstream(t *testing.T) {
	v := loadVectors(t)
	payload := CanonicalPayload(signedFields(v, 0))
	differ := 0
	for _, vec := range v.Trust {
		var sig []byte
		if vec.Signature != nil {
			sig = []byte(*vec.Signature)
		}
		p := payload
		if !vec.WithManifest {
			p = nil
		}
		keys := ParseKeys(vec.Key)
		got := EvaluateTrust(p, sig, keys, signingRequired(vec.Required))
		want := vec.Result
		if vec.WithManifest && len(keys) > 0 && sig == nil && !signingRequired(vec.Required) {
			differ++
			if got.OK || got.Code != TrustSignatureRequired {
				t.Errorf("key set, no signature: %+v, want a refusal", got)
			}
			if !want.OK || want.Code != TrustUnsignedAllowed {
				t.Errorf("upstream changed: key set, no signature: %+v", want)
			}
			continue
		}
		if !vec.WithManifest {
			if got.OK || got.Code != TrustManifestMissing {
				t.Errorf("no manifest: %+v", got)
			}
			continue
		}
		if got.OK != want.OK || got.Verified != want.Verified || got.Code != want.Code {
			t.Errorf("key %q required %q signature %v: %+v, upstream %+v", vec.Key, vec.Required, vec.Signature != nil, got, want)
		}
		if got.Code != TrustSignatureRequired && got.Reason != want.Reason {
			t.Errorf("reason %q, upstream %q", got.Reason, want.Reason)
		}
	}
	if differ == 0 {
		t.Error("no vector of the one known difference")
	}
}

// The Jest tests of updateManifestSignature.js: the text does not depend
// on the order of the files or the separators, and changes with a hash.
func TestCanonicalPayloadRules(t *testing.T) {
	base := SignedFields{Owner: "UNITRONIX", Repo: "BetterDesk", Branch: "dev", SHA: "abc1234", Files: []SignedFile{
		{Path: "web-nodejs/server.js", SHA256: strings.Repeat("a", 64)},
		{Path: "web-nodejs/routes/auth.routes.js", SHA256: strings.Repeat("b", 64)},
	}}
	reordered := base
	reordered.Files = []SignedFile{base.Files[1], base.Files[0]}
	if !bytes.Equal(CanonicalPayload(base), CanonicalPayload(reordered)) {
		t.Error("the order of the files changes the text")
	}
	windows := base
	windows.Files = []SignedFile{{Path: `web-nodejs\server.js`, SHA256: base.Files[0].SHA256}, {Path: `web-nodejs\routes\auth.routes.js`, SHA256: base.Files[1].SHA256}}
	if !bytes.Equal(CanonicalPayload(base), CanonicalPayload(windows)) {
		t.Error("backslashes change the text")
	}
	tampered := base
	tampered.Files = []SignedFile{{Path: base.Files[0].Path, SHA256: strings.Repeat("c", 64)}}
	if bytes.Equal(CanonicalPayload(base), CanonicalPayload(tampered)) {
		t.Error("a changed hash keeps the text")
	}
	want := "betterdesk-update-manifest-v1\nowner=UNITRONIX\nrepo=BetterDesk\nbranch=dev\nsha=abc1234\nfiles=2\n" +
		strings.Repeat("b", 64) + "  web-nodejs/routes/auth.routes.js\n" + strings.Repeat("a", 64) + "  web-nodejs/server.js\n"
	if got := string(CanonicalPayload(base)); got != want {
		t.Errorf("text\n%s\nwant\n%s", got, want)
	}
}

// The Jest tests of the verifier: the matching key verifies, another
// manifest or key does not, malformed signatures and short keys fail.
func TestVerifySignatureRules(t *testing.T) {
	seed := bytes.Repeat([]byte{7}, 32)
	private := ed25519.NewKeyFromSeed(seed)
	keyText := base64.StdEncoding.EncodeToString(private.Public().(ed25519.PublicKey))
	m := SignedFields{Owner: "o", Repo: "r", Branch: "main", SHA: "abc1234", Files: []SignedFile{{Path: "x", SHA256: strings.Repeat("a", 64)}}}
	payload := CanonicalPayload(m)
	sig := []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(private, payload)))
	if !VerifySignature(payload, sig, ParseKeys(keyText)) {
		t.Fatal("the matching key does not verify")
	}
	other := m
	other.SHA = "deadbee"
	if VerifySignature(CanonicalPayload(other), sig, ParseKeys(keyText)) {
		t.Error("a signature over another manifest verifies")
	}
	attacker := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{9}, 32))
	if VerifySignature(payload, []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(attacker, payload))), ParseKeys(keyText)) {
		t.Error("a signature by another key verifies")
	}
	for _, bad := range []string{"not-base64!!", base64.StdEncoding.EncodeToString(make([]byte, 4)), ""} {
		if VerifySignature(payload, []byte(bad), ParseKeys(keyText)) {
			t.Errorf("malformed signature %q verifies", bad)
		}
	}
	if keys := ParseKeys(base64.StdEncoding.EncodeToString(make([]byte, 16))); len(keys) != 0 {
		t.Error("a key of 16 bytes is accepted")
	}
	// Two keys during a rotation: either verifies.
	rotation := base64.StdEncoding.EncodeToString(attacker.Public().(ed25519.PublicKey)) + ", " + keyText
	if keys := ParseKeys(rotation); len(keys) != 2 || !VerifySignature(payload, sig, keys) {
		t.Errorf("rotation: %d keys", len(keys))
	}
}

func TestEvaluateTrustRules(t *testing.T) {
	private := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{3}, 32))
	keys := Keys{private.Public().(ed25519.PublicKey)}
	payload := []byte("payload")
	good := []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(private, payload)))
	for _, tc := range []struct {
		name     string
		sig      []byte
		keys     Keys
		required bool
		ok, ver  bool
		code     string
	}{
		{"signed and verified", good, keys, false, true, true, ""},
		{"tampered", []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(private, []byte("other")))), keys, false, false, false, TrustSignatureInvalid},
		{"key but no signature", nil, keys, false, false, false, TrustSignatureRequired},
		{"required without key", good, nil, true, false, false, TrustSignatureRequired},
		{"nothing configured", nil, nil, false, true, false, TrustUnsignedAllowed},
		{"signature without key", good, nil, false, true, false, TrustUnsignedAllowed},
	} {
		got := EvaluateTrust(payload, tc.sig, tc.keys, tc.required)
		if got.OK != tc.ok || got.Verified != tc.ver || got.Code != tc.code {
			t.Errorf("%s: %+v", tc.name, got)
		}
	}
	for value, want := range map[string]bool{"true": true, "1": true, "yes": true, " YES ": true, "no": false, "": false, "on": false} {
		if signingRequired(value) != want {
			t.Errorf("signingRequired(%q) != %v", value, want)
		}
	}
}

// JavaScript sorts strings by UTF-16 code units: a character outside the
// basic plane comes before U+FF5E although its code point is larger.
func TestLessUTF16(t *testing.T) {
	if !lessUTF16("\U0001F600", "～") || lessUTF16("～", "\U0001F600") {
		t.Error("surrogate pairs are not ordered by code unit")
	}
	if !lessUTF16("a", "ab") || lessUTF16("ab", "a") || lessUTF16("a", "a") {
		t.Error("prefixes")
	}
}
