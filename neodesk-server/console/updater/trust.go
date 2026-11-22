package updater

import (
	"crypto/ed25519"
	"strings"
)

// The settings that decide how a manifest's signature is judged, with
// upstream's names.
const (
	EnvPublicKey      = "UPDATE_MANIFEST_PUBLIC_KEY"
	EnvRequireSigning = "UPDATE_REQUIRE_SIGNED_MANIFEST"
)

// Trust is how a release's manifest was judged. Code and Reason are
// upstream's (evaluateUpdateTrust).
type Trust struct {
	OK       bool   `json:"ok"`
	Verified bool   `json:"verified"`
	Code     string `json:"code,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

// The codes of a Trust.
const (
	TrustManifestMissing   = "manifest_missing"
	TrustSignatureInvalid  = "signature_invalid"
	TrustSignatureRequired = "signature_required"
	TrustUnsignedAllowed   = "unsigned_allowed"
)

// Keys are the release public keys a signature is checked against.
type Keys []ed25519.PublicKey

// ParseKeys reads public keys as upstream wrote one: the 32 raw bytes in
// base64. Several keys (during a rotation) are separated by commas or
// white space. A part that is not 32 bytes is left out.
func ParseKeys(text string) Keys {
	var keys Keys
	for _, part := range strings.FieldsFunc(text, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t' || r == '\n' || r == '\r'
	}) {
		if raw := decodeBase64(part); len(raw) == ed25519.PublicKeySize {
			keys = append(keys, ed25519.PublicKey(raw))
		}
	}
	return keys
}

// VerifySignature reports whether signature (the file manifest.sig: the
// 64-byte signature in base64, white space around it allowed) is a
// signature of payload by one of the keys. Base64 is read as Node's
// Buffer.from reads it, as upstream's verifier does.
func VerifySignature(payload []byte, signature []byte, keys Keys) bool {
	sig := decodeBase64(strings.TrimSpace(string(signature)))
	if len(sig) != ed25519.SignatureSize {
		return false
	}
	for _, key := range keys {
		if len(key) == ed25519.PublicKeySize && ed25519.Verify(key, payload, sig) {
			return true
		}
	}
	return false
}

// EvaluateTrust decides whether a release may be installed
// (upstream's evaluateUpdateTrust, with one difference). signature is nil
// when the release has no manifest.sig. required is
// UPDATE_REQUIRE_SIGNED_MANIFEST.
//
//   - With a key and a signature, the signature must verify.
//   - With a key and no signature, the release is refused. Upstream lets
//     such an update through unless signing is required (BUGS UPD1); a
//     key that is compiled in or configured means signatures are checked
//     (DECISIONS A28).
//   - Without a key, a release is refused when signing is required and
//     allowed otherwise, unverified; the checksums are still checked.
func EvaluateTrust(payload []byte, signature []byte, keys Keys, required bool) Trust {
	if payload == nil {
		return Trust{Code: TrustManifestMissing, Reason: "A signed update manifest is required but none was provided."}
	}
	if len(keys) > 0 {
		if signature != nil && VerifySignature(payload, signature, keys) {
			return Trust{OK: true, Verified: true}
		}
		if signature != nil {
			return Trust{Code: TrustSignatureInvalid, Reason: "Update manifest signature did not verify against the configured public key."}
		}
		return Trust{Code: TrustSignatureRequired, Reason: "A release public key is configured but the release carries no manifest.sig."}
	}
	if required {
		return Trust{Code: TrustSignatureRequired, Reason: "UPDATE_REQUIRE_SIGNED_MANIFEST is enabled but the update carried no valid signature."}
	}
	return Trust{OK: true, Code: TrustUnsignedAllowed, Reason: "Update manifest was not signed; proceeding because signatures are not required."}
}

// signingRequired is upstream's isSignatureRequired: "true", "1" or
// "yes" in any case, white space around it allowed.
func signingRequired(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true", "1", "yes":
		return true
	}
	return false
}

// decodeBase64 is Buffer.from(text, 'base64'): both alphabets, every
// character that is no base64 character skipped, the end at the first
// "=", and a last group of two or three characters giving one or two
// bytes. The same reader as console/agentreleases.
func decodeBase64(text string) []byte {
	var out []byte
	var group [4]byte
	n := 0
	for i := 0; i < len(text) && text[i] != '='; i++ {
		v, ok := base64Value(text[i])
		if !ok {
			continue
		}
		group[n] = v
		n++
		if n == 4 {
			out = append(out, group[0]<<2|group[1]>>4, group[1]<<4|group[2]>>2, group[2]<<6|group[3])
			n = 0
		}
	}
	switch n {
	case 2:
		out = append(out, group[0]<<2|group[1]>>4)
	case 3:
		out = append(out, group[0]<<2|group[1]>>4, group[1]<<4|group[2]>>2)
	}
	return out
}

func base64Value(c byte) (byte, bool) {
	switch {
	case c >= 'A' && c <= 'Z':
		return c - 'A', true
	case c >= 'a' && c <= 'z':
		return c - 'a' + 26, true
	case c >= '0' && c <= '9':
		return c - '0' + 52, true
	case c == '+' || c == '-':
		return 62, true
	case c == '/' || c == '_':
		return 63, true
	}
	return 0, false
}
