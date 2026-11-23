package clientrelease

import "github.com/neodeskapp/neodesk/neodesk-server/console/updater"

// EnvPublicKey adds keys (a fork's own) to the compiled ones, in the form
// UPDATE_MANIFEST_PUBLIC_KEY takes: base64 of the raw key, several
// separated by commas or white space.
const EnvPublicKey = "NEODESK_CLIENT_RELEASE_PUBLIC_KEY"

// Keys returns the release keys compiled into the server and the ones
// extra names. The key that signs the server's releases also signs the
// client fork's checksums (DECISIONS A152, A158), so the compiled keys are
// the updater's (updater.CompiledKeys), named in one place.
func Keys(extra string) updater.Keys {
	return append(updater.CompiledKeys(), updater.ParseKeys(extra)...)
}
