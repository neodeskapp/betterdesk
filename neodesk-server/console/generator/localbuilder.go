package generator

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
)

// The local build provider (services/localClientBuilder.js): it compiles
// the client source on this machine with its toolchain (cargo, flutter,
// python, vcpkg; on Windows also Visual Studio, the Windows SDK and LLVM)
// and packs the result for the host's platform: a zip on Windows, a
// tar.gz on Linux and macOS, and on Linux also a .deb, .rpm and Arch
// package when dpkg-deb, rpmbuild and zstd are there. The signed Support
// configuration goes to the compiler in NEODESK_SUPPORT_CONFIG_B64. It
// never builds without one.
//
// The old console ran it as a separate Node process; here it runs in the
// console with the environment that process had, and the tools it starts
// get the same command lines, folders and environment.

// localBuilder is one run, or one look at the toolchain, of the provider
// with an environment.
type localBuilder struct {
	s       *Service
	env     map[string]string
	windows bool
}

func (s *Service) newLocalBuilder(env map[string]string) *localBuilder {
	return &localBuilder{s: s, env: env, windows: s.opts.GOOS == "windows"}
}

// processEnv is the console's environment with the build environment
// file's values, as the old process.env held it.
func (s *Service) processEnv() map[string]string {
	merged := s.environ()
	for k, v := range s.env {
		if _, ok := merged[k]; !ok {
			merged[k] = v
		}
	}
	return merged
}

func (b *localBuilder) get(name string) string { return b.env[name] }

func (b *localBuilder) delimiter() string {
	if b.windows {
		return ";"
	}
	return ":"
}

var executableSuffix = regexp.MustCompile(`(?i)\.(exe|bat|cmd)$`)

func (b *localBuilder) executableNames(name string) []string {
	if !b.windows || executableSuffix.MatchString(name) {
		return []string{name}
	}
	return []string{name + ".exe", name + ".bat", name + ".cmd", name}
}

// toolPath finds a tool: the configured path, then the toolchain folders,
// then PATH. "" when it is nowhere.
func (b *localBuilder) toolPath(name, configured string) string {
	var candidates []string
	if configured != "" {
		candidates = append(candidates, configured)
	}
	roots := []string{b.get("NEODESK_CLIENT_TOOLCHAIN_BIN")}
	if b.windows {
		root := ""
		if r := b.get("NEODESK_CLIENT_TOOLCHAIN_ROOT"); r != "" {
			root = filepath.Join(r, "bin")
		}
		roots = append(roots, root, `C:\tools\flutter-3.24.5\bin`, `C:\tools\LLVM-15.0.6\bin`)
	} else {
		roots = append(roots, "/opt/neodesk-toolchain/bin", "/opt/neodesk-toolchain/cargo/bin", "/opt/flutter-3.24.5/bin")
	}
	for _, p := range strings.Split(b.get("PATH"), b.delimiter()) {
		roots = append(roots, p)
	}
	for _, root := range roots {
		if root == "" {
			continue
		}
		for _, exe := range b.executableNames(name) {
			candidates = append(candidates, filepath.Join(root, exe))
		}
	}
	for _, c := range candidates {
		if exists(c) {
			return c
		}
	}
	return ""
}

var vcpkgExecutable = regexp.MustCompile(`(?i)(?:^|[\\/])vcpkg(?:\.exe)?$`)

func (b *localBuilder) vcpkgName() string {
	if b.windows {
		return "vcpkg.exe"
	}
	return "vcpkg"
}

func (b *localBuilder) resolveVcpkgRoot() string {
	if configured := jsTrim(b.get("VCPKG_ROOT")); configured != "" {
		if vcpkgExecutable.MatchString(configured) {
			return filepath.Dir(configured)
		}
		return configured
	}
	conventional := "/opt/vcpkg"
	if b.windows {
		conventional = `C:\vcpkg`
	}
	if exists(filepath.Join(conventional, b.vcpkgName())) {
		return conventional
	}
	return ""
}

func (b *localBuilder) pythonName() string {
	if b.windows {
		return "python"
	}
	return "python3"
}

func (b *localBuilder) toolchainRoot() string {
	if r := b.get("NEODESK_CLIENT_TOOLCHAIN_ROOT"); r != "" {
		return r
	}
	if b.windows {
		return `C:\ProgramData\NeoDesk\toolchain`
	}
	return "/opt/neodesk-toolchain"
}

// buildEnvironment is the environment of the compile step: the toolchain
// folders first on PATH, VCPKG_ROOT, PUB_CACHE, LIBCLANG_PATH when found.
func (b *localBuilder) buildEnvironment() (map[string]string, error) {
	cargo := b.toolPath("cargo", b.get("NEODESK_CLIENT_CARGO"))
	flutter := b.toolPath("flutter", b.get("NEODESK_CLIENT_FLUTTER"))
	python := b.toolPath(b.pythonName(), b.get("NEODESK_CLIENT_PYTHON"))
	switch {
	case cargo == "":
		return nil, codeError("local_builder_toolchain_missing:cargo")
	case flutter == "":
		return nil, codeError("local_builder_toolchain_missing:flutter")
	case python == "":
		return nil, codeError("local_builder_toolchain_missing:python")
	}
	vcpkgRoot := b.resolveVcpkgRoot()
	if vcpkgRoot == "" {
		return nil, codeError("local_builder_toolchain_missing:VCPKG_ROOT")
	}
	if !exists(filepath.Join(vcpkgRoot, b.vcpkgName())) {
		return nil, codeError("local_builder_toolchain_invalid:VCPKG_ROOT")
	}
	binDir, flutterDir := filepath.Dir(cargo), filepath.Dir(flutter)
	dartDir := filepath.Join(flutterDir, "cache", "dart-sdk", "bin")
	pubCache := b.get("PUB_CACHE")
	if pubCache == "" {
		pubCache = filepath.Join(b.toolchainRoot(), "flutter-pub-cache")
	}
	pubBin := filepath.Join(pubCache, "bin")
	configuredLibclang := jsTrim(b.get("LIBCLANG_PATH"))
	defaultLibclang := ""
	if b.windows {
		root := b.get("NEODESK_CLIENT_LLVM_ROOT")
		if root == "" {
			root = `C:\tools\LLVM-15.0.6`
		}
		defaultLibclang = filepath.Join(root, "bin")
	} else if exists("/usr/lib/llvm-18/lib") {
		defaultLibclang = "/usr/lib/llvm-18/lib"
	}
	ready := func(p string) bool {
		if b.windows {
			return exists(filepath.Join(p, "libclang.dll"))
		}
		return p != "" && exists(p)
	}
	libclang := ""
	if b.windows {
		if ready(defaultLibclang) {
			libclang = defaultLibclang
		} else if ready(configuredLibclang) {
			libclang = configuredLibclang
		}
	} else {
		if ready(configuredLibclang) {
			libclang = configuredLibclang
		} else if ready(defaultLibclang) {
			libclang = defaultLibclang
		}
	}
	env := map[string]string{}
	for k, v := range b.env {
		env[k] = v
	}
	env["VCPKG_ROOT"] = vcpkgRoot
	env["PUB_CACHE"] = pubCache
	env["NEODESK_CLIENT_PYTHON"] = python
	if libclang != "" {
		env["LIBCLANG_PATH"] = libclang
	}
	var parts []string
	add := func(p string) {
		if p != "" {
			parts = append(parts, p)
		}
	}
	add(binDir)
	add(flutterDir)
	add(filepath.Dir(python))
	if libclang != "" {
		if b.windows {
			add(libclang)
		} else {
			add(filepath.Dir(libclang))
		}
	}
	if exists(dartDir) {
		add(dartDir)
	}
	add(pubBin)
	add(b.get("PATH"))
	env["PATH"] = strings.Join(parts, b.delimiter())
	env["NEODESK_CLIENT_SUPPORT_BUILD"] = "1"
	return env, nil
}

// LocalCapabilities is what the local provider can build on this machine.
func (s *Service) LocalCapabilities() *goapi.Object {
	return s.newLocalBuilder(s.processEnv()).capabilities()
}

func (b *localBuilder) hostTarget() (platform, arch string) {
	switch b.s.opts.GOOS {
	case "windows":
		platform = "windows"
	case "darwin":
		platform = "macos"
	default:
		platform = "linux"
	}
	arch = "x64"
	if b.s.opts.GOARCH == "arm64" {
		arch = "arm64"
	}
	return platform, arch
}

// providerArch is the architecture's name in artifact names.
func providerArch(arch string) string {
	if arch == "x64" {
		return "x86_64"
	}
	return "aarch64"
}

func (b *localBuilder) commandAvailable(name string) bool { return b.toolPath(name, "") != "" }

func (b *localBuilder) linuxPackageFormats() []any {
	if b.s.opts.GOOS != "linux" {
		return []any{}
	}
	formats := []any{"portable"}
	if b.commandAvailable("dpkg-deb") {
		formats = append(formats, "deb")
	}
	if b.commandAvailable("rpmbuild") {
		formats = append(formats, "rpm")
	}
	if b.commandAvailable("tar") && b.commandAvailable("zstd") {
		formats = append(formats, "arch")
	}
	return formats
}

func (b *localBuilder) toolchainStatus() *goapi.Object {
	missing := []any{}
	if b.toolPath("cargo", b.get("NEODESK_CLIENT_CARGO")) == "" {
		missing = append(missing, "cargo")
	}
	if b.toolPath("flutter", b.get("NEODESK_CLIENT_FLUTTER")) == "" {
		missing = append(missing, "flutter")
	}
	if b.toolPath(b.pythonName(), b.get("NEODESK_CLIENT_PYTHON")) == "" {
		missing = append(missing, "python")
	}
	if root := b.resolveVcpkgRoot(); root == "" || !exists(filepath.Join(root, b.vcpkgName())) {
		missing = append(missing, "VCPKG_ROOT")
	}
	if b.windows {
		vsDevCmd := b.get("NEODESK_CLIENT_VSDEVCMD")
		msvc := b.toolPath("cl", b.get("NEODESK_CLIENT_CL")) != "" || b.get("VCINSTALLDIR") != "" || b.get("VSINSTALLDIR") != "" ||
			(vsDevCmd != "" && exists(vsDevCmd))
		if !msvc {
			missing = append(missing, "msvc")
		}
		var sdkRoots []string
		for _, r := range []string{b.get("NEODESK_CLIENT_WINDOWS_SDK_DIR"), b.get("WindowsSdkDir")} {
			if r != "" {
				sdkRoots = append(sdkRoots, r)
			}
		}
		if pf := b.get("ProgramFiles(x86)"); pf != "" {
			sdkRoots = append(sdkRoots, filepath.Join(pf, "Windows Kits", "10"))
		}
		if pf := b.get("ProgramFiles"); pf != "" {
			sdkRoots = append(sdkRoots, filepath.Join(pf, "Windows Kits", "10"))
		}
		sdk := false
		for _, r := range sdkRoots {
			if exists(filepath.Join(r, "Include")) {
				sdk = true
				break
			}
		}
		if !sdk {
			missing = append(missing, "windows-sdk")
		}
		llvmRoot := b.get("NEODESK_CLIENT_LLVM_ROOT")
		if llvmRoot == "" && b.get("ProgramFiles") != "" {
			llvmRoot = filepath.Join(b.get("ProgramFiles"), "LLVM")
		}
		var bins []string
		if p := b.get("LIBCLANG_PATH"); p != "" {
			bins = append(bins, p)
		}
		if llvmRoot != "" {
			bins = append(bins, filepath.Join(llvmRoot, "bin"))
		}
		bins = append(bins, filepath.Join(`C:\tools\LLVM-15.0.6`, "bin"))
		llvm := false
		for _, bin := range bins {
			if exists(filepath.Join(bin, "clang.exe")) && exists(filepath.Join(bin, "libclang.dll")) {
				llvm = true
				break
			}
		}
		if !llvm {
			missing = append(missing, "llvm")
		}
	}
	return goapi.NewObject("ready", len(missing) == 0, "missing", missing, "packageFormats", b.linuxPackageFormats())
}

func (b *localBuilder) capabilities() *goapi.Object {
	platform, arch := b.hostTarget()
	toolchain := b.toolchainStatus()
	var formats []any
	if platform == "linux" {
		formats, _ = toolchain.Get("packageFormats").([]any)
	} else {
		formats = []any{"portable"}
	}
	targets := []any{}
	if truthy(toolchain.Get("ready")) {
		for _, f := range formats {
			targets = append(targets, goapi.NewObject("platform", platform, "arch", arch, "format", f))
		}
	}
	return goapi.NewObject(
		"mode", "local",
		"host", goapi.NewObject("platform", platform, "arch", arch),
		"toolchain", toolchain,
		"targets", targets,
	)
}

// allowedBuilderTools are the only tools the provider starts, by base
// name without .exe, .bat or .cmd.
var allowedBuilderTools = map[string]bool{
	"cargo": true, "rustc": true, "flutter": true, "dart": true, "python": true, "python3": true, "py": true,
	"dpkg-deb": true, "rpmbuild": true, "tar": true, "zip": true, "unzip": true, "strip": true, "objcopy": true,
}

func toolBase(command string) string {
	if command == "" {
		return ""
	}
	base := filepath.Base(command)
	if base == "." || base == string(filepath.Separator) {
		base = ""
	}
	return strings.ToLower(executableSuffix.ReplaceAllString(base, ""))
}

// run starts an allowed tool; a .bat or .cmd on Windows goes through the
// shell. env nil inherits the provider's environment.
func (b *localBuilder) run(ctx context.Context, command string, args []string, dir string, env map[string]string) (Result, error) {
	base := toolBase(command)
	if !allowedBuilderTools[base] {
		if base == "" {
			base = "(empty)"
		}
		return Result{}, fmt.Errorf("Refusing to spawn non-allowlisted builder tool: %s", base)
	}
	if env == nil {
		env = b.env
	}
	c := Command{Name: command, Args: args, Dir: dir, Env: envList(env)}
	if b.windows && regexp.MustCompile(`(?i)\.(bat|cmd)$`).MatchString(command) {
		c = Command{Name: "cmd.exe", Args: append([]string{"/d", "/s", "/c", command}, args...), Dir: dir, Env: envList(env)}
	}
	return b.s.opts.Run.Run(ctx, c)
}

func envList(env map[string]string) []string {
	out := make([]string, 0, len(env))
	for k, v := range env {
		out = append(out, k+"="+v)
	}
	return out
}
