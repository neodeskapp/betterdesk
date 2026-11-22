# Recordings of the old console for console/generator

The JSON files here are fixed records of what the Node services of the
support client generator returned, made while they still existed. They
can't be recorded again. The Go tests replay every vector and compare byte
for byte; they need no Node. Don't edit them: when a fix changes a result
on purpose, the test lists that vector as an intended difference with the
row id. Every seed in them is made up for the test.

| File | What it holds | Replayed by |
|---|---|---|
| `validate_branding.json` | the branding checks of `agentBundleService` | `TestValidateBrandingMatchesNode` |
| `slugs.json` | profile slugs | `TestSlugsMatchNode` |
| `build_types.json` | `lib/generatorBuildTypes` | `TestBuildTypesMatchNode` |
| `custom_txt.json` | `customTxtBuilder`: the JSON of custom.txt and the signed bytes | `TestCustomTxtMatchesNode` |
| `base64.json` | Node's base64 reading of seeds and the Ed25519 public key of a seed | `TestBase64AndKeysMatchNode` |
| `installers.json` | the installer scripts | `TestInstallerScriptsMatchNode` |
| `generation_keys.json` | generation keys | `TestGenerationKeysMatchNode` |
| `luminance.json` | the channel values of the download page's contrast rule | `TestLuminanceMatchesNode` |
| `urls.json` | the URL rules | `TestURLsMatchNode` |
| `json.json` | JSON text as the services wrote it | `TestJSONMatchesNode` |
| `zip.json` | the zip adm-zip wrote for a folder | `TestFolderZipMatchesAdmZip` (`worker_vectors_test.go`) |
| `embedded_platforms.json` | the build capabilities and platform rules of embedded mode for provider settings | `TestEmbeddedPlatformsMatchNode` |
| `embedded_manifests.json` | the checks of a provider's manifest | `TestEmbeddedManifestsMatchNode` |
