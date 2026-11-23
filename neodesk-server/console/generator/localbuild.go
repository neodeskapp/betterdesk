package generator

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
)

// runLocalBuilder runs one build request with the local provider and
// answers as its process did: exit code 0, or 1 with the error on
// stderr; the progress lines on stdout.
func (s *Service) runLocalBuilder(ctx context.Context, requestPath, sourceDir, outputDir string, env []string, progress Progress) Result {
	envMap := map[string]string{}
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok {
			envMap[k] = v
		}
	}
	timeout := s.buildTimeout()
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	b := s.newLocalBuilder(envMap)
	var stdout strings.Builder
	report := func(percent float64, phase string) {
		stdout.WriteString("NEODESK_BUILD_PROGRESS|" + jsString(percent) + "|" + phase + "\n")
		progress(percent, phase)
	}
	err := b.main(ctx, s.absPath(requestPath), s.absPath(sourceDir), s.absPath(outputDir), report)
	res := Result{Stdout: stdout.String()}
	if ctx.Err() == context.DeadlineExceeded {
		res.TimedOut = true
	}
	if err != nil {
		res.Code = 1
		res.Stderr = errorName(err) + ": " + err.Error() + "\n"
	}
	return res
}

// errorName is the name of the JavaScript error class the old provider
// printed in front of a message.
func errorName(err error) string {
	var te typeError
	if errors.As(err, &te) {
		return "TypeError"
	}
	if errors.Is(err, errJSON) {
		return "SyntaxError"
	}
	return "Error"
}

func (b *localBuilder) main(ctx context.Context, requestPath, sourceDir, outputDir string, progress Progress) error {
	if !exists(requestPath) || !exists(filepath.Join(sourceDir, "build.py")) {
		return codeError("local_builder_source_or_request_missing")
	}
	raw, err := os.ReadFile(requestPath)
	if err != nil {
		return nodeFSError(err, "open", requestPath)
	}
	request, err := parseJSON(strings.TrimPrefix(decodeUTF8(raw), byteOrderMark))
	if err != nil {
		return err
	}
	if request == nil {
		return typeError("Cannot read properties of null (reading 'product_sku')")
	}
	if prop(request, "product_sku") != ProductSupport || !truthy(prop(request, "signed_config_b64")) {
		return codeError("local_builder_support_payload_required")
	}
	requested, _ := prop(request, "targets").([]any)
	if len(requested) == 0 {
		return codeError("local_builder_targets_required")
	}
	hostPlatform, hostArch := b.hostTarget()
	var targets []*goapi.Object
	for _, c := range requested {
		if prop(c, "platform") == hostPlatform && prop(c, "arch") == hostArch {
			t := spread(goapi.NewObject(), c)
			t.Set("format", or(prop(c, "format"), "portable"))
			targets = append(targets, t)
		}
	}
	if len(targets) == 0 {
		first := requested[0]
		return codeError("local_builder_target_unsupported:" + jsString(or(prop(first, "platform"), "unknown")) + "/" + jsString(or(prop(first, "arch"), "unknown")))
	}
	available := map[string]bool{}
	caps := b.capabilities()
	list, _ := caps.Get("targets").([]any)
	for _, t := range list {
		available[jsString(prop(t, "platform"))+"/"+jsString(prop(t, "arch"))+"/"+jsString(prop(t, "format"))] = true
	}
	for _, t := range targets {
		if !available[jsString(t.Get("platform"))+"/"+jsString(t.Get("arch"))+"/"+jsString(t.Get("format"))] {
			return codeError("local_builder_format_unsupported:" + jsString(or(t.Get("format"), "portable")))
		}
	}
	if err := os.MkdirAll(outputDir, 0o777); err != nil {
		return nodeFSError(err, "mkdir", outputDir)
	}
	if err := b.ensureBuildCapacity(ctx, outputDir); err != nil {
		return err
	}
	toolchain, err := b.buildEnvironment()
	if err != nil {
		return err
	}
	if toolchain, err = b.loadVisualStudioEnvironment(ctx, toolchain); err != nil {
		return err
	}
	progress(5, "preparing")
	if err := b.ensureFlutterRustBridge(ctx, sourceDir, toolchain); err != nil {
		return err
	}
	python := b.toolPath(b.pythonName(), toolchain["NEODESK_CLIENT_PYTHON"])
	args := []string{"build.py", "--flutter"}
	if strings.ToLower(jsTrim(b.get("NEODESK_CLIENT_BUILD_HWCODEC"))) == "on" && toolchain["VCPKG_ROOT"] != "" {
		args = append(args, "--hwcodec")
	}
	if b.windows {
		args = append(args, "--skip-portable-pack")
	}
	progress(10, "compiling")
	compileEnv := map[string]string{}
	for k, v := range toolchain {
		compileEnv[k] = v
	}
	compileEnv["NEODESK_SUPPORT_CONFIG_B64"] = jsString(prop(request, "signed_config_b64"))
	compileEnv["NEODESK_FORCE_LOCAL_CARGO_TARGET"] = "1"
	build, err := b.run(ctx, python, args, sourceDir, compileEnv)
	if err != nil {
		return err
	}
	if build.Code != 0 {
		return codeError("local_builder_compile_failed:" + lastChars(build.Stderr, 4000))
	}
	progress(78, "packaging")
	type artifact struct {
		target *goapi.Object
		path   string
	}
	var artifacts []artifact
	for _, t := range targets {
		p, err := b.packageTarget(ctx, request, t, sourceDir, outputDir)
		if err != nil {
			return err
		}
		artifacts = append(artifacts, artifact{t, p})
	}
	progress(94, "verifying")
	assets := []any{}
	for _, a := range artifacts {
		sum, err := sha256File(a.path)
		if err != nil {
			return err
		}
		info, err := os.Stat(a.path)
		if err != nil {
			return nodeFSError(err, "stat", a.path)
		}
		assets = append(assets, goapi.NewObject(
			"name", filepath.Base(a.path),
			"sha256", sum,
			"size", float64(info.Size()),
			"platform", a.target.Get("platform"),
			"arch", providerArch(jsString(a.target.Get("arch"))),
			"format", or(a.target.Get("format"), "portable"),
		))
	}
	manifest := goapi.NewObject(
		"schema_version", float64(1),
		"sku", ProductSupport,
		"generation_id", prop(request, "generationId"),
		"client_commit", or(prop(request, "clientCommit"), nil),
		"external_config", false,
		"assets", assets,
		"builder", "neodesk-local",
	)
	if err := writeFileMode(filepath.Join(outputDir, "manifest.json"), []byte(dbText(stringifyIndent(manifest)+"\n")), 0o600); err != nil {
		return err
	}
	progress(100, "complete")
	return nil
}

// ensureBuildCapacity needs NEODESK_CLIENT_BUILD_MIN_FREE_MB
// (minFreeMB) free below the output folder.
func (b *localBuilder) ensureBuildCapacity(ctx context.Context, outputDir string) error {
	free, err := freeBytes(outputDir)
	if err != nil {
		if !b.windows {
			return nodeFSError(err, "statfs", outputDir)
		}
		// The fallback asks PowerShell, which is not on the list of tools
		if _, err := b.run(ctx, "powershell.exe", nil, outputDir, nil); err != nil {
			return err
		}
	}
	minimum := minFreeMB(b.get("NEODESK_CLIENT_BUILD_MIN_FREE_MB"))
	freeMB := free / (1024 * 1024)
	if freeMB < minimum {
		return codeError("local_builder_disk_space_insufficient:" + jsString(float64(freeMB)) + "MB<" + jsString(float64(minimum)) + "MB")
	}
	return nil
}

// minFreeMB is the space in MB a build needs, from the setting's text:
// at least 512, and 3072 when the setting is empty or no number.
func minFreeMB(raw string) int64 {
	n, ok := jsParseInt(raw)
	if !ok {
		return 3072
	}
	return max(512, n)
}

// loadVisualStudioEnvironment would take the Visual Studio environment
// from NEODESK_CLIENT_VSDEVCMD on Windows. The command shell is not on
// the provider's list of tools, so the step refuses (BUGS).
func (b *localBuilder) loadVisualStudioEnvironment(ctx context.Context, env map[string]string) (map[string]string, error) {
	if !b.windows {
		return env, nil
	}
	vsDevCmd := env["NEODESK_CLIENT_VSDEVCMD"]
	if vsDevCmd == "" {
		vsDevCmd = b.get("NEODESK_CLIENT_VSDEVCMD")
	}
	if vsDevCmd == "" {
		return env, nil
	}
	if !exists(vsDevCmd) {
		return nil, codeError("local_builder_toolchain_invalid:NEODESK_CLIENT_VSDEVCMD")
	}
	shell := b.get("ComSpec")
	if shell == "" {
		shell = "cmd.exe"
	}
	if _, err := b.run(ctx, shell, nil, filepath.Dir(vsDevCmd), env); err != nil {
		return nil, err
	}
	return env, nil
}

// ensureFlutterRustBridge generates the Rust and Dart bridge files when
// the source lacks them. The generator itself is not on the provider's
// list of tools, so the last step refuses (BUGS).
func (b *localBuilder) ensureFlutterRustBridge(ctx context.Context, sourceDir string, env map[string]string) error {
	bridgeRust := filepath.Join(sourceDir, "src", "bridge_generated.rs")
	bridgeDart := filepath.Join(sourceDir, "flutter", "lib", "generated_bridge.dart")
	if exists(bridgeRust) && exists(bridgeDart) {
		return nil
	}
	codegenRoot := b.get("NEODESK_CLIENT_BRIDGE_CODEGEN_ROOT")
	if codegenRoot == "" {
		codegenRoot = filepath.Join(b.toolchainRoot(), "bridge-codegen")
	}
	codegen := b.toolPath("flutter_rust_bridge_codegen", filepath.Join(codegenRoot, "bin", "flutter_rust_bridge_codegen"))
	cargo := b.toolPath("cargo", env["NEODESK_CLIENT_CARGO"])
	flutter := b.toolPath("flutter", env["NEODESK_CLIENT_FLUTTER"])
	dart := b.toolPath("dart", filepath.Join(filepath.Dir(flutter), "cache", "dart-sdk", "bin", "dart"))
	if err := os.MkdirAll(codegenRoot, 0o777); err != nil {
		return nodeFSError(err, "mkdir", codegenRoot)
	}
	if !exists(codegen) {
		install, err := b.run(ctx, cargo, []string{"install", "flutter_rust_bridge_codegen", "--version", "1.80.1", "--features", "uuid", "--locked", "--root", codegenRoot}, sourceDir, env)
		if err != nil {
			return err
		}
		if install.Code != 0 {
			return codeError("local_builder_bridge_codegen_install_failed:" + lastChars(install.Stderr, 4000))
		}
	}
	pub, err := b.run(ctx, flutter, []string{"pub", "get"}, filepath.Join(sourceDir, "flutter"), env)
	if err != nil {
		return err
	}
	if pub.Code != 0 {
		return codeError("local_builder_flutter_pub_get_failed:" + lastChars(pub.Stderr, 4000))
	}
	ffigen, err := b.run(ctx, dart, []string{"pub", "global", "activate", "ffigen", "--version", "5.0.1"}, sourceDir, env)
	if err != nil {
		return err
	}
	if ffigen.Code != 0 {
		return codeError("local_builder_ffigen_install_failed:" + lastChars(ffigen.Stderr, 4000))
	}
	args := []string{"--rust-input", "./src/flutter_ffi.rs", "--dart-output", "./flutter/lib/generated_bridge.dart", "--c-output", "./flutter/macos/Runner/bridge_generated.h"}
	if b.windows && env["LIBCLANG_PATH"] != "" {
		args = append(args, "--llvm-path", filepath.Dir(env["LIBCLANG_PATH"]))
	} else if exists("/usr/lib/llvm-18") {
		args = append(args, "--llvm-path", "/usr/lib/llvm-18")
	}
	generated, err := b.run(ctx, codegen, args, sourceDir, env)
	if err != nil {
		return err
	}
	if generated.Code != 0 {
		return codeError("local_builder_bridge_codegen_failed:" + lastChars(generated.Stderr, 4000))
	}
	macHeader := filepath.Join(sourceDir, "flutter", "macos", "Runner", "bridge_generated.h")
	if exists(macHeader) {
		if err := copyFile(macHeader, filepath.Join(sourceDir, "flutter", "ios", "Runner", "bridge_generated.h")); err != nil {
			return err
		}
	}
	if !exists(bridgeRust) || !exists(bridgeDart) {
		return codeError("local_builder_bridge_codegen_incomplete")
	}
	return nil
}

// assertNoExternalConfig refuses a tree with a custom.txt (any case) or a
// marker for one: a Support build carries its configuration inside.
func assertNoExternalConfig(root string) error {
	stack := []string{root}
	for len(stack) > 0 {
		current := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		entries, err := os.ReadDir(current)
		if err != nil {
			return nodeFSError(err, "scandir", current)
		}
		for _, e := range entries {
			full := filepath.Join(current, e.Name())
			if e.IsDir() {
				stack = append(stack, full)
			} else if strings.ToLower(e.Name()) == "custom.txt" || e.Name() == ".custom-txt-here" {
				return codeError("local_builder_external_config_present")
			}
		}
	}
	return nil
}

// packageVersion is 0.0.0.<first 12 hex digits of the commit>, or
// 0.0.0.local.
func packageVersion(request any) string {
	commit := strings.ToLower(jsString(or(prop(request, "clientCommit"), "")))
	commit = jsSlice(commit, 12)
	var b strings.Builder
	for _, c := range commit {
		if (c >= 'a' && c <= 'f') || (c >= '0' && c <= '9') {
			b.WriteRune(c)
		}
	}
	if b.Len() == 0 {
		return "0.0.0.local"
	}
	return "0.0.0." + b.String()
}

type linuxBundle struct {
	arch, targetName, stage string
}

func (b *localBuilder) prepareLinuxBundle(source, outputDir string, target *goapi.Object) (linuxBundle, error) {
	arch := providerArch(jsString(target.Get("arch")))
	targetName := "linux-" + arch + "-" + jsString(target.Get("format"))
	stage := filepath.Join(outputDir, "stage", targetName)
	_ = os.RemoveAll(stage)
	if err := os.MkdirAll(stage, 0o777); err != nil {
		return linuxBundle{}, nodeFSError(err, "mkdir", stage)
	}
	if err := copyTree(source, filepath.Join(stage, "bundle")); err != nil {
		return linuxBundle{}, err
	}
	if err := assertNoExternalConfig(stage); err != nil {
		return linuxBundle{}, err
	}
	return linuxBundle{arch: arch, targetName: targetName, stage: stage}, nil
}

func generationOf(request any) string { return jsString(prop(request, "generationId")) }

func (b *localBuilder) packageLinuxDeb(ctx context.Context, request any, target *goapi.Object, bundle linuxBundle, outputDir string) (string, error) {
	root := filepath.Join(outputDir, "packages", bundle.targetName+"-deb")
	_ = os.RemoveAll(root)
	for _, dir := range []string{filepath.Join(root, "DEBIAN"), filepath.Join(root, "opt", "neodesk-support")} {
		if err := os.MkdirAll(dir, 0o777); err != nil {
			return "", nodeFSError(err, "mkdir", dir)
		}
	}
	if err := copyTree(filepath.Join(bundle.stage, "bundle"), filepath.Join(root, "opt", "neodesk-support")); err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Join(root, "usr", "bin"), 0o777); err != nil {
		return "", err
	}
	if err := os.Symlink("/opt/neodesk-support/neodesk", filepath.Join(root, "usr", "bin", "neodesk")); err != nil {
		return "", nodeFSError(err, "symlink", "/opt/neodesk-support/neodesk")
	}
	debArch := "amd64"
	if target.Get("arch") == "arm64" {
		debArch = "arm64"
	}
	control := strings.Join([]string{
		"Package: neodesk-support",
		"Version: " + packageVersion(request),
		"Architecture: " + debArch,
		"Section: net",
		"Priority: optional",
		"Maintainer: NeoDesk",
		"Description: NeoDesk Support client",
		" Embedded support client with signed server configuration.",
		"",
	}, "\n")
	if err := os.WriteFile(filepath.Join(root, "DEBIAN", "control"), []byte(control), 0o666); err != nil {
		return "", err
	}
	artifact := filepath.Join(outputDir, "neodesk-support-"+generationOf(request)+"-linux-"+bundle.arch+".deb")
	res, err := b.run(ctx, "dpkg-deb", []string{"--build", root, artifact}, "", nil)
	if err != nil {
		return "", err
	}
	if res.Code != 0 {
		return "", codeError("local_builder_deb_failed:" + lastChars(res.Stderr, 2000))
	}
	return artifact, nil
}

func (b *localBuilder) packageLinuxRpm(ctx context.Context, request any, target *goapi.Object, bundle linuxBundle, outputDir string) (string, error) {
	top := filepath.Join(outputDir, "packages", bundle.targetName+"-rpm")
	specDir, sources := filepath.Join(top, "SPECS"), filepath.Join(top, "SOURCES")
	_ = os.RemoveAll(top)
	for _, dir := range []string{specDir, sources} {
		if err := os.MkdirAll(dir, 0o777); err != nil {
			return "", nodeFSError(err, "mkdir", dir)
		}
	}
	if err := copyTree(bundle.stage, filepath.Join(sources, "bundle")); err != nil {
		return "", err
	}
	version := packageVersion(request)
	rpmArch := "x86_64"
	if target.Get("arch") == "arm64" {
		rpmArch = "aarch64"
	}
	spec := strings.Join([]string{
		"Name: neodesk-support",
		"Version: " + version,
		"Release: 1",
		"Summary: NeoDesk Support client",
		"License: AGPL-3.0",
		"BuildArch: " + rpmArch,
		"%description",
		"NeoDesk Support client with signed server configuration.",
		"%prep",
		"%build",
		"%install",
		"mkdir -p %{buildroot}/opt/neodesk-support",
		"cp -a %{_sourcedir}/bundle/bundle/. %{buildroot}/opt/neodesk-support/",
		"mkdir -p %{buildroot}/usr/bin",
		"ln -s /opt/neodesk-support/neodesk %{buildroot}/usr/bin/neodesk",
		"%files",
		"/opt/neodesk-support",
		"/usr/bin/neodesk",
		"%changelog",
		"* Mon Oct 05 2026 NeoDesk <support@neodesk.local> - 0.0.0-1",
		"- Build signed NeoDesk Support client.",
		"",
	}, "\n")
	specPath := filepath.Join(specDir, "neodesk-support.spec")
	if err := os.WriteFile(specPath, []byte(spec), 0o666); err != nil {
		return "", err
	}
	res, err := b.run(ctx, "rpmbuild", []string{"--define", "_topdir " + top, "-bb", specPath}, "", nil)
	if err != nil {
		return "", err
	}
	if res.Code != 0 {
		return "", codeError("local_builder_rpm_failed:" + lastChars(res.Stderr, 2000))
	}
	artifact := filepath.Join(outputDir, "neodesk-support-"+generationOf(request)+"-linux-"+bundle.arch+".rpm")
	built := filepath.Join(top, "RPMS", rpmArch, "neodesk-support-"+version+"-1."+rpmArch+".rpm")
	if !exists(built) {
		return "", codeError("local_builder_rpm_output_missing:" + built)
	}
	if err := os.Rename(built, artifact); err != nil {
		return "", nodeFSError(err, "rename", built)
	}
	return artifact, nil
}

func (b *localBuilder) packageLinuxArch(ctx context.Context, request any, target *goapi.Object, bundle linuxBundle, outputDir string) (string, error) {
	root := filepath.Join(outputDir, "packages", bundle.targetName+"-arch")
	_ = os.RemoveAll(root)
	if err := os.MkdirAll(filepath.Join(root, "opt", "neodesk-support"), 0o777); err != nil {
		return "", err
	}
	if err := copyTree(filepath.Join(bundle.stage, "bundle"), filepath.Join(root, "opt", "neodesk-support")); err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Join(root, "usr", "bin"), 0o777); err != nil {
		return "", err
	}
	if err := os.Symlink("/opt/neodesk-support/neodesk", filepath.Join(root, "usr", "bin", "neodesk")); err != nil {
		return "", nodeFSError(err, "symlink", "/opt/neodesk-support/neodesk")
	}
	arch := "x86_64"
	if target.Get("arch") == "arm64" {
		arch = "aarch64"
	}
	info := strings.Join([]string{
		"pkgname = neodesk-support",
		"pkgbase = neodesk-support",
		"pkgver = 0.0.0-1",
		"pkgdesc = NeoDesk Support client",
		"url = https://neodesk.eu",
		"builddate = 1791158400",
		"packager = NeoDesk",
		"arch = " + arch,
		"",
	}, "\n")
	if err := os.WriteFile(filepath.Join(root, ".PKGINFO"), []byte(info), 0o666); err != nil {
		return "", err
	}
	artifact := filepath.Join(outputDir, "neodesk-support-"+generationOf(request)+"-linux-"+bundle.arch+".pkg.tar.zst")
	res, err := b.run(ctx, "tar", []string{"--zstd", "-cf", artifact, "."}, root, nil)
	if err != nil {
		return "", err
	}
	if res.Code != 0 {
		return "", codeError("local_builder_arch_failed:" + lastChars(res.Stderr, 2000))
	}
	return artifact, nil
}

func (b *localBuilder) packageTarget(ctx context.Context, request any, target *goapi.Object, sourceDir, outputDir string) (string, error) {
	platform, archName, format := jsString(target.Get("platform")), jsString(target.Get("arch")), jsString(target.Get("format"))
	arch := providerArch(archName)
	targetName := platform + "-" + arch + "-" + format
	stage := filepath.Join(outputDir, "stage", targetName)
	_ = os.RemoveAll(stage)
	if err := os.MkdirAll(stage, 0o777); err != nil {
		return "", nodeFSError(err, "mkdir", stage)
	}
	if platform == "windows" {
		source := filepath.Join(sourceDir, "flutter", "build", "windows", archName, "runner", "Release")
		if !exists(source) {
			return "", codeError("local_builder_output_missing:" + source)
		}
		if err := copyTree(source, stage); err != nil {
			return "", err
		}
		if err := assertNoExternalConfig(stage); err != nil {
			return "", err
		}
		zipPath := filepath.Join(outputDir, "neodesk-support-"+generationOf(request)+"-"+targetName+".zip")
		if err := writeFolderZip(stage, targetName, zipPath); err != nil {
			return "", err
		}
		return zipPath, nil
	}
	var source string
	if platform == "linux" {
		candidates := []string{
			filepath.Join(sourceDir, "flutter", "build", "linux", archName, "release", "bundle"),
			// the client's build script names the Linux folder x64 also on
			// an ARM64 host
			filepath.Join(sourceDir, "flutter", "build", "linux", "x64", "release", "bundle"),
		}
		source = candidates[0]
		for _, c := range candidates {
			if exists(c) {
				source = c
				break
			}
		}
	} else {
		source = filepath.Join(sourceDir, "flutter", "build", "macos", "Build", "Products", "Release", "NeoDesk Client.app")
	}
	if !exists(source) {
		return "", codeError("local_builder_output_missing:" + source)
	}
	if platform == "linux" && format != "portable" {
		bundle, err := b.prepareLinuxBundle(source, outputDir, target)
		if err != nil {
			return "", err
		}
		switch format {
		case "deb":
			return b.packageLinuxDeb(ctx, request, target, bundle, outputDir)
		case "rpm":
			return b.packageLinuxRpm(ctx, request, target, bundle, outputDir)
		case "arch":
			return b.packageLinuxArch(ctx, request, target, bundle, outputDir)
		}
		return "", codeError("local_builder_format_unsupported:" + format)
	}
	if err := copyTree(source, filepath.Join(stage, filepath.Base(source))); err != nil {
		return "", err
	}
	if err := assertNoExternalConfig(stage); err != nil {
		return "", err
	}
	archivePath := filepath.Join(outputDir, "neodesk-support-"+generationOf(request)+"-"+targetName+".tar.gz")
	res, err := b.run(ctx, "tar", []string{"-czf", archivePath, targetName}, filepath.Dir(stage), nil)
	if err != nil {
		return "", err
	}
	if res.Code != 0 {
		return "", codeError("local_builder_tar_failed:" + lastChars(res.Stderr, 2000))
	}
	return archivePath, nil
}
