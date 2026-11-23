package updater

// releaseKeys are the release public keys compiled into the binary: the
// 32 raw bytes of an ed25519 key in base64, as UPDATE_MANIFEST_PUBLIC_KEY
// takes them. During a rotation both keys are listed. With a key here,
// every install checks the manifest's signature and refuses a release
// without a valid one; the environment can add keys (a fork's) but not
// take these away.
//
// The key of the releases of neodeskapp/neodesk (DECISIONS A152);
// release-go.yml signs with its private half. The client packages the
// console serves are checked against the same keys (clientrelease.Keys).
var releaseKeys = []string{"iwissiXvSjeFFsG8SdKEZq11uAtRLWCrD7Ky1vfY2n4="}

// CompiledKeys returns the keys compiled into the binary.
func CompiledKeys() Keys {
	var keys Keys
	for _, text := range releaseKeys {
		keys = append(keys, ParseKeys(text)...)
	}
	return keys
}
