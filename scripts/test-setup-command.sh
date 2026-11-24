#!/usr/bin/env bash
# The installer's commands on a real install (L431): neodesk-setup is the
# installer's manager, and neodesk is the command line, a link to the
# server binary that hands the installer's options to neodesk-setup. A
# fresh install with the server built from this checkout, the installer's
# options and a bare `sudo neodesk` through the new name, an update by
# neodesk-setup, the update of an install whose /usr/local/sbin/neodesk is
# the old manager (3.6.0-rc.4), and the uninstall. The backups go to
# /opt/neodesk-backups, and the old manager's backups in
# /opt/rustdesk-backups move there (L446).
#
# It installs NeoDesk on this machine and removes it again, so it runs as
# root on a machine that can be thrown away (CI, a sandbox), with systemd
# and the network for the packages:
#
#   sudo bash scripts/test-setup-command.sh
#
# NEODESK_TEST_BINARY: a neodesk-server built from this checkout (else go
# builds one). OLD_MANAGER: the old manager's file (else 3.6.0-rc.4's
# neodesk.sh is downloaded).
set -euo pipefail

SCRIPT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SETUP=/usr/local/sbin/neodesk-setup
CLI=/usr/local/bin/neodesk
OLD=/usr/local/sbin/neodesk
BIN=/opt/neodesk/neodesk-server
BACKUPS=/opt/neodesk-backups
OLD_BACKUPS=/opt/rustdesk-backups
OLD_MANAGER_URL=https://raw.githubusercontent.com/neodeskapp/neodesk/v3.6.0-rc.4/neodesk.sh

fail() {
    printf 'FAIL: %s\n' "$*" >&2
    exit 1
}

[ "$(id -u)" = 0 ] || fail "run this as root, on a machine that can be thrown away"
[ -d /run/systemd/system ] || fail "this needs systemd"
if [ -e "$BIN" ] || [ -e "$SETUP" ] || [ -e "$OLD" ] || [ -e "$BACKUPS" ] || [ -e "$OLD_BACKUPS" ]; then
    fail "NeoDesk is installed here already; this test needs a machine without it"
fi

WORK=$(mktemp -d)
export NO_COLOR=1

# The server binary of this checkout
if [ -n "${NEODESK_TEST_BINARY:-}" ]; then
    cp "$NEODESK_TEST_BINARY" "$WORK/neodesk-server"
else
    (cd "$SCRIPT_ROOT/neodesk-server" && CGO_ENABLED=0 go build -o "$WORK/neodesk-server" .) \
        || fail "neodesk-server did not build"
fi
chmod 755 "$WORK/neodesk-server"

# The old manager: neodesk.sh of 3.6.0-rc.4, which installed itself as
# /usr/local/sbin/neodesk
if [ -n "${OLD_MANAGER:-}" ]; then
    cp "$OLD_MANAGER" "$WORK/old-manager"
else
    curl -fsSL --retry 3 -o "$WORK/old-manager" "$OLD_MANAGER_URL" || fail "could not download $OLD_MANAGER_URL"
fi
grep -qx 'MANAGER_COMMAND="/usr/local/sbin/neodesk"' "$WORK/old-manager" || fail "$WORK/old-manager is not the old manager"

# What an update saves as the manager: this checkout's neodesk.sh with
# its top made longer, so a copy written over a running script would
# break that script's run
{
    head -n 1 "$SCRIPT_ROOT/neodesk.sh"
    for _ in $(seq 1 300); do printf '# The newest installer, as the update fetches it\n'; done
    tail -n +2 "$SCRIPT_ROOT/neodesk.sh"
} > "$WORK/newest.sh"

# curl: the newest installer for the address the manager fetches itself
# from, the real curl for everything else
mkdir -p "$WORK/stub"
cat > "$WORK/stub/curl" <<EOF
#!/bin/bash
url="\${!#}"
case "\$url" in
    https://raw.githubusercontent.com/*/neodesk.sh)
        dest="" prev=""
        for a in "\$@"; do [ "\$prev" = -o ] && dest="\$a"; prev="\$a"; done
        printf '%s\n' "\$url" >> "$WORK/fetched"
        cp "$WORK/newest.sh" "\$dest"
        exit 0
        ;;
esac
exec "$(command -v curl)" "\$@"
EOF
chmod 755 "$WORK/stub/curl"
export PATH="$WORK/stub:$PATH"

# ran_cleanly NAME LOG: LOG has no sign of a script that broke while it ran
ran_cleanly() {
    if grep -nE 'syntax error|unexpected end of file|command not found|line [0-9]+: .*: No such file' "$2"; then
        fail "$1 did not run cleanly (lines above); its log: $2"
    fi
}

# new_layout WHEN NEWEST: neodesk-setup is NEWEST, neodesk is the link to
# the server binary, and nothing is at /usr/local/sbin/neodesk
new_layout() {
    [ -x "$SETUP" ] || fail "$1: no $SETUP"
    cmp -s "$SETUP" "$2" || fail "$1: $SETUP is not $2"
    [ "$(readlink "$CLI")" = "$BIN" ] || fail "$1: $CLI is not a link to $BIN: $(ls -l "$CLI" 2>&1)"
    [ ! -e "$OLD" ] || fail "$1: $OLD is still there: $(head -c 300 "$OLD")"
    [ "$(command -v neodesk)" = "$CLI" ] || fail "$1: neodesk is $(command -v neodesk)"
}

echo "== fresh install"
RELAY_SERVERS=127.0.0.1 bash "$SCRIPT_ROOT/neodesk.sh" --auto --binary "$WORK/neodesk-server" \
    < /dev/null > "$WORK/install.log" 2>&1 || fail "the install failed: $(tail -n 40 "$WORK/install.log")"
ran_cleanly "the install" "$WORK/install.log"
hash -r
new_layout "after a fresh install" "$SCRIPT_ROOT/neodesk.sh"

echo "== the command line and the installer's options through neodesk"
out=$(neodesk --version 2>&1) || fail "neodesk --version: $out"
case "$out" in "neodesk "*) ;; *) fail "neodesk --version is not the command line's: $out" ;; esac
out=$(neodesk --update --help 2>&1) || fail "neodesk --update --help: $out"
case "$out" in *"Usage: sudo neodesk-setup"*"--reset-password"*) ;; *) fail "neodesk --update --help is not the installer's help: $out" ;; esac
out=$(neodesk --check-permissions < /dev/null 2>&1) || fail "neodesk --check-permissions failed: $out"
case "$out" in *"management capabilities are ready"*) ;; *) fail "neodesk --check-permissions did not reach the installer: $out" ;; esac
code=0
out=$(neodesk --set-config 2>&1) || code=$?
[ "$code" = 1 ] && [ "$out" = "ERROR: --set-config requires KEY=VALUE" ] || fail "neodesk --set-config: exit $code: $out"
code=0
out=$(neodesk < /dev/null 2>&1) || code=$?
case "$code $out" in "2 "*"neodesk-setup"*) ;; *) fail "a bare neodesk without a terminal: exit $code: $out" ;; esac
out=$(printf '0\n' | NEODESK_CLASSIC_MENU=1 script -qec neodesk /dev/null 2>&1 | tr -d '\r') || true
case "$out" in *"MAIN MENU"*"Goodbye"*) ;; *) fail "a bare sudo neodesk on a terminal did not open the menu: $out" ;; esac

echo "== update by neodesk-setup"
inode=$(stat -c %i "$SETUP")
neodesk --update --binary "$WORK/neodesk-server" < /dev/null > "$WORK/update.log" 2>&1 \
    || fail "neodesk --update failed: $(tail -n 40 "$WORK/update.log")"
ran_cleanly "neodesk --update" "$WORK/update.log"
grep -qE 'neodesk-server is up to date|Updated to' "$WORK/update.log" || fail "the update did not finish: $(tail -n 20 "$WORK/update.log")"
[ "$(stat -c %i "$SETUP")" != "$inode" ] || fail "neodesk-setup was written over while it ran"
new_layout "after neodesk --update" "$WORK/newest.sh"
ls "$BACKUPS"/neodesk_backup_*.tar.gz >/dev/null 2>&1 || fail "the update left no backup in $BACKUPS"
[ ! -e "$OLD_BACKUPS" ] || fail "$OLD_BACKUPS is there after a fresh install and an update"

echo "== update of an install with the old manager (3.6.0-rc.4)"
rm -f "$SETUP" "$CLI"
install -m 0755 "$WORK/old-manager" "$OLD"
hash -r
[ "$(command -v neodesk)" = "$OLD" ] || fail "the old manager is not the neodesk command: $(command -v neodesk)"
neodesk --update --binary "$WORK/neodesk-server" < /dev/null > "$WORK/old-update.log" 2>&1 \
    || fail "the old manager's update failed: $(tail -n 40 "$WORK/old-update.log")"
ran_cleanly "the old manager's update" "$WORK/old-update.log"
grep -qE 'neodesk-server is up to date|Updated to' "$WORK/old-update.log" || fail "the old manager's update did not finish"
cmp -s "$OLD" "$WORK/newest.sh" || fail "the old manager did not save the newest installer"
old_backup=$(ls -1t "$OLD_BACKUPS"/neodesk_backup_*.tar.gz 2>/dev/null | head -n 1)
[ -n "$old_backup" ] || fail "the old manager made no backup in $OLD_BACKUPS"
# The newest installer at the old place moves itself at its first run and
# hands the call on: the installer's option to neodesk-setup
hash -r
out=$(neodesk --check-permissions < /dev/null 2>&1) || fail "neodesk --check-permissions after the old manager's update: $out"
case "$out" in *"management capabilities are ready"*) ;; *) fail "the first call after the old manager's update: $out" ;; esac
hash -r
new_layout "after the first call" "$WORK/newest.sh"
# The old manager's backups moved, and their old path still reads
[ "$(readlink "$OLD_BACKUPS")" = "$BACKUPS" ] || fail "$OLD_BACKUPS is no link to $BACKUPS: $(ls -ld "$OLD_BACKUPS" 2>&1)"
[ -f "$BACKUPS/$(basename "$old_backup")" ] || fail "the old manager's backup did not move to $BACKUPS"
tar -tzf "$old_backup" >/dev/null || fail "the old manager's backup cannot be read at its old path"
# The command line's own calls work from then on
out=$(neodesk --version 2>&1) || fail "neodesk --version after the move: $out"
case "$out" in "neodesk "*) ;; *) fail "neodesk --version after the move: $out" ;; esac

echo "== uninstall"
neodesk --uninstall --purge < /dev/null > "$WORK/uninstall.log" 2>&1 \
    || fail "neodesk --uninstall --purge failed: $(tail -n 40 "$WORK/uninstall.log")"
ran_cleanly "the uninstall" "$WORK/uninstall.log"
for f in "$SETUP" "$CLI" "$OLD" "$BIN"; do
    [ ! -e "$f" ] && [ ! -L "$f" ] || fail "the uninstall left $f"
done

rm -rf "$WORK"
echo "PASS: the installer's commands on a real install"
