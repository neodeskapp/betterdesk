package updater

// releaseKeys are the release public keys compiled into the binary: the
// 32 raw bytes of an ed25519 key in base64, as UPDATE_MANIFEST_PUBLIC_KEY
// takes them. During a rotation both keys are listed. With a key here,
// every install checks the manifest's signature; the environment can add
// keys (a fork's) but not take these away.
//
// Empty until the owner creates the release key (spec
// updater-installers.md 5.2).
var releaseKeys = []string{}

// CompiledKeys returns the keys compiled into the binary.
func CompiledKeys() Keys {
	var keys Keys
	for _, text := range releaseKeys {
		keys = append(keys, ParseKeys(text)...)
	}
	return keys
}
