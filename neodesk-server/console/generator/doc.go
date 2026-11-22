// Package generator is the support client generator of the console: the
// profiles of support bundles and their records, the template module
// downloaded from the BetterDesk-Client releases, the client builder that
// compiles the client from source in embedded mode, and the build queue
// with its worker. It does what services/agentBundleService.js,
// agentBundleConnection.js (through package serverinfo),
// supportGeneratorModule.js, clientBuilderService.js, localClientBuilder.js,
// clientTemplateWorker.js, customTxtBuilder.js, windowsSupportInstaller.js,
// platformSupportInstaller.js and lib/generatorBuildTypes.js did, with the
// same files below the data folder and the same rows.
//
// The services are one package because they call each other in a circle
// (the builder queues through the worker, the worker builds through the
// builder and the module), and they share the JavaScript value rules of
// jsvalue.go: the bytes of a stored profile, its hash and a signed
// custom.txt are what clients in the field check.
//
// What a client installs, by mode:
//
//   - download mode (the default): a copy of the platform's template with
//     custom.txt, the signed configuration (customtxt.go), and the
//     installer scripts (installers.go), packed as a zip for Windows and a
//     tar.gz otherwise, named
//     betterdesk-support-<first 12 of the hash>-<platform>-<arch>.<ext>.
//   - embedded mode: what the build provider made with the signed
//     configuration compiled in, checked against its manifest and copied
//     under the same names.
//
// Tools (tar, git, the build tools) run through a Runner, GitHub is reached
// through Options.HTTP, so tests need neither.
package generator
