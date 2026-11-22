package clientrelease

import "github.com/unitronix/betterdesk-server/console/updater"

// signingKey is the public key of the release signing key (DECISIONS
// A152): the 32 raw bytes of the ed25519 key in base64. The same key signs
// the server's releases and the client fork's checksums. This is the one
// place the console names it for client packages.
const signingKey = "iwissiXvSjeFFsG8SdKEZq11uAtRLWCrD7Ky1vfY2n4="

// EnvPublicKey adds keys (a fork's own) to the compiled one, in the form
// UPDATE_MANIFEST_PUBLIC_KEY takes: base64 of the raw key, several
// separated by commas or white space.
const EnvPublicKey = "BETTERDESK_CLIENT_RELEASE_PUBLIC_KEY"

// Keys returns the compiled key and the ones extra names.
func Keys(extra string) updater.Keys {
	keys := updater.ParseKeys(signingKey)
	return append(keys, updater.ParseKeys(extra)...)
}
