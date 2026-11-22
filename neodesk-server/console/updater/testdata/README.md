# Recordings of the old console for console/updater

The file below is a fixed record of what upstream's signed update manifest
module (`web-nodejs/lib/updateManifestSignature.js` of upstream dev)
answered, made while the Node console still existed. It can't be recorded
again. The Go tests replay it and need no Node. Don't edit it: when a fix
changes an answer on purpose, the test lists that vector as an intended
difference with the row id.

| File | What it holds | Replayed by |
|---|---|---|
| `manifest_signature_vectors.json` | manifests, the canonical text upstream built from each, the signature of that text by a fixed test key, and what upstream's verifier answered for good, foreign, changed and malformed signatures and keys | `TestCanonicalPayloadAndSignatureMatchUpstream`, `TestVerifySignatureMatchesUpstream`, `TestEvaluateTrustMatchesUpstream` (`signature_test.go`) |

The test key was made for these vectors and signs nothing else.
