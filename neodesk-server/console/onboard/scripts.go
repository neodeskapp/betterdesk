package onboard

import "strings"

// The Linux scripts of a code. base is the console's address (Console.Base:
// a scheme, a host name or address and a port, nothing a shell reads as
// code) and code a normalized code, so both go into single quotes as they
// are.

// InstallScript installs the client package of an invite as root (deb
// with apt-get, rpm with dnf, zypper or yum) and joins the server with
// neodesk --join <link>.
func InstallScript(base, code string) string {
	return fill(installScript, base, code)
}

// RunScript starts quick support from the portable package in the
// person's cache folder, without root and without installing anything:
// neodesk --join <support link>.
func RunScript(base, code string) string {
	return fill(runScript, base, code)
}

func fill(script, base, code string) string {
	return strings.NewReplacer("@BASE@", base, "@CODE@", code).Replace(script)
}

const archCase = `case "$(uname -m)" in
	x86_64 | amd64) arch=x64 ;;
	aarch64 | arm64) arch=arm64 ;;
	*)
		echo "There is no NeoDesk package for $(uname -m)." >&2
		exit 1
		;;
esac
`

const installScript = `#!/bin/sh
# Installs NeoDesk and joins it to @BASE@ with an invite.
# The script works as long as the invite does.
set -eu

BASE='@BASE@'
CODE='@CODE@'

if [ "$(id -u)" != "0" ]; then
	echo "Run this as root: curl -fsSL '$BASE/join/$CODE/install.sh' | sudo sh" >&2
	exit 1
fi

` + archCase + `
if command -v apt-get >/dev/null 2>&1; then
	format=deb
elif command -v dnf >/dev/null 2>&1 || command -v zypper >/dev/null 2>&1 || command -v yum >/dev/null 2>&1; then
	format=rpm
else
	echo "This system has no apt-get, dnf, zypper or yum to install NeoDesk with." >&2
	exit 1
fi

dir=$(mktemp -d)
trap 'rm -rf "$dir"' EXIT
chmod 755 "$dir"
package="$dir/neodesk.$format"
echo "Downloading NeoDesk..."
curl -fsSL -o "$package" "$BASE/api/onboard/public/$CODE/download?os=linux&arch=$arch&format=$format"

echo "Installing NeoDesk..."
# The offered package is installed whatever version is there: the builds
# after a release candidate count as older than it (1.5.0~rc.6 comes
# before 1.5.0), and the same version is installed again.
case "$format" in
	deb) DEBIAN_FRONTEND=noninteractive apt-get install -y --reinstall --allow-downgrades "$package" ;;
	rpm)
		want=$(rpm -qp --qf '%{NAME}-%{VERSION}-%{RELEASE}' "$package")
		name=$(rpm -qp --qf '%{NAME}' "$package")
		installed() { rpm -q --qf '%{NAME}-%{VERSION}-%{RELEASE}' "$name" 2>/dev/null || true; }
		if command -v zypper >/dev/null 2>&1 && ! command -v dnf >/dev/null 2>&1; then
			zypper --non-interactive install --allow-unsigned-rpm --oldpackage --force "$package"
		else
			tool=yum
			if command -v dnf >/dev/null 2>&1; then
				tool=dnf
			fi
			if [ "$(installed)" = "$want" ]; then
				"$tool" reinstall -y "$package"
			else
				"$tool" install -y "$package"
				if [ "$(installed)" != "$want" ]; then
					"$tool" downgrade -y "$package"
				fi
			fi
		fi
		;;
esac

echo "Joining $BASE..."
neodesk --join "$BASE/join/$CODE"
`

const runScript = `#!/bin/sh
# Starts NeoDesk Quick Support for @BASE@. Nothing is installed: the
# app runs from a folder in your cache and asks you before anyone sees
# your screen.
set -eu

BASE='@BASE@'
CODE='@CODE@'

` + archCase + `
dir="${XDG_CACHE_HOME:-$HOME/.cache}/neodesk-quick-support"
rm -rf "$dir"
mkdir -p "$dir"
echo "Downloading NeoDesk Quick Support..."
curl -fsSL -o "$dir/package.tar.gz" "$BASE/api/onboard/public/$CODE/download?os=linux&arch=$arch&format=tar.gz"
tar -xzf "$dir/package.tar.gz" -C "$dir"
rm -f "$dir/package.tar.gz"
exec "$dir/neodesk" --join "$BASE/s/$CODE"
`
