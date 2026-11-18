These scripts are fixtures that go away with the Node console. They load
upstream's signed update manifest module (`web-nodejs/lib/updateManifestSignature.js`
of upstream dev; its path is the first argument, since this branch does
not have the file yet) and write the vectors the Go tests replay:

    node manifest-signature.js <path of updateManifestSignature.js> ../manifest_signature_vectors.json

`manifest_signature_vectors.json` holds manifests, the canonical text
upstream builds from each, the signature of that text by a fixed test
key, and what upstream's verifier answers for good, foreign, changed and
malformed signatures and keys. The test key is made for these vectors and
signs nothing else.
