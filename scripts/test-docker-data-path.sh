#!/usr/bin/env bash
# The Docker image's data folder on a real Docker (L446): an image of this
# checkout keeps its data in /opt/neodesk/data, and /opt/rustdesk is a link
# to it. An install of 3.6.0-rc.4 (its data volume at /opt/rustdesk, with
# data in it) is updated with this checkout's neodesk-docker.sh to the
# image of this checkout and keeps its key, its keys files, its database
# and its files; a backup that rc.4's manager took before the update goes
# back into the volumes afterwards.
#
# It installs NeoDesk with Docker on this machine and removes it again, so
# it runs on a machine that can be thrown away (CI, a sandbox), as root or
# a member of the docker group, with the network (ghcr.io, GitHub):
#
#   sudo bash scripts/test-docker-data-path.sh
#
# NEODESK_TEST_BINARY: a neodesk-server built from this checkout; the
# image is then the Dockerfile's last stage around it. Without it the
# whole Dockerfile is built.
set -euo pipefail

SCRIPT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OLD_TAG=3.6.0-rc.4
OLD_MANAGER_URL="https://raw.githubusercontent.com/neodeskapp/neodesk/v${OLD_TAG}/neodesk-docker.sh"
IMAGE=ghcr.io/neodeskapp/neodesk
export NO_COLOR=1 NEODESK_HEALTH_TIMEOUT="${NEODESK_HEALTH_TIMEOUT:-240}"

fail() {
    printf 'FAIL: %s\n' "$*" >&2
    exit 1
}

docker info >/dev/null 2>&1 || fail "Docker is not usable here"
if docker container inspect neodesk >/dev/null 2>&1 || docker volume inspect neodesk-data >/dev/null 2>&1; then
    fail "a NeoDesk container or volume is here already; this test needs a machine without one"
fi

WORK=$(mktemp -d)
DIR="$WORK/install"
cleanup() {
    (cd "$DIR" 2>/dev/null && docker compose down -v --remove-orphans >/dev/null 2>&1) || true
    docker volume rm neodesk-data neodesk-console-data >/dev/null 2>&1 || true
}
trap cleanup EXIT

# in_container CMD: CMD in the running container, as its user
in_container() { docker exec -u neodesk neodesk sh -c "$1"; }

# data_mount: where the container mounts the data volume
data_mount() {
    docker container inspect -f '{{range .Mounts}}{{if eq .Name "neodesk-data"}}{{.Destination}}{{end}}{{end}}' neodesk
}

echo "== the image of this checkout ($IMAGE:local)"
if [ -n "${NEODESK_TEST_BINARY:-}" ]; then
    mkdir -p "$WORK/context"
    cp -a "$SCRIPT_ROOT/docker" "$WORK/context/"
    cp "$NEODESK_TEST_BINARY" "$WORK/context/neodesk-server.bin"
    sed -n '/^# ============= Runtime/,$p' "$SCRIPT_ROOT/Dockerfile" \
        | sed 's|^COPY --from=go-builder /neodesk-server /usr/local/bin/neodesk-server$|COPY neodesk-server.bin /usr/local/bin/neodesk-server|' \
        > "$WORK/context/Dockerfile"
    grep -q '^COPY neodesk-server.bin ' "$WORK/context/Dockerfile" || fail "the Dockerfile's last stage has no binary to replace"
    docker build -q -t "$IMAGE:local" "$WORK/context" >/dev/null || fail "the image did not build"
else
    docker build -q -t "$IMAGE:local" "$SCRIPT_ROOT" >/dev/null || fail "the image did not build"
fi

echo "== an install of $OLD_TAG with data"
curl -fsSL --retry 3 -o "$WORK/old-docker.sh" "$OLD_MANAGER_URL" || fail "could not download $OLD_MANAGER_URL"
bash "$WORK/old-docker.sh" install --dir "$DIR" --tag "$OLD_TAG" --relay 127.0.0.1 --yes --no-install-docker \
    > "$WORK/old-install.log" 2>&1 || fail "the install of $OLD_TAG failed: $(tail -n 30 "$WORK/old-install.log")"
[ "$(data_mount)" = /opt/rustdesk ] || fail "$OLD_TAG mounts its data at $(data_mount)"
key=$(in_container 'cat /opt/rustdesk/id_ed25519.pub') || fail "$OLD_TAG has no public key"
files=$(in_container 'cd /opt/rustdesk && sha256sum id_ed25519 .api_key .remote_target_vault_key db_v2.sqlite3 | cut -c1-64')     || fail "$OLD_TAG lacks one of its key, API key, vault key and database"
users=$(in_container 'sqlite3 /opt/rustdesk/db_v2.sqlite3 "SELECT COUNT(*) FROM users"') || fail "$OLD_TAG's database cannot be read"
in_container 'echo before > /opt/rustdesk/marker' || fail "the data volume of $OLD_TAG cannot be written"
[ -n "$key" ] && [ "$users" -ge 1 ] || fail "the install of $OLD_TAG has no key or no account"
bash "$WORK/old-docker.sh" backup --dir "$DIR" > "$WORK/old-backup.log" 2>&1 \
    || fail "$OLD_TAG's backup failed: $(tail -n 20 "$WORK/old-backup.log")"
old_backup=$(ls -1t "$DIR"/backups/neodesk-*.tar.gz | head -n 1)
[ -f "$old_backup" ] || fail "$OLD_TAG's manager left no backup"

echo "== the update with this checkout's manager"
bash "$SCRIPT_ROOT/neodesk-docker.sh" update --dir "$DIR" --tag local --no-pull --yes \
    > "$WORK/update.log" 2>&1 || fail "the update failed: $(tail -n 40 "$WORK/update.log")"
[ "$(data_mount)" = /opt/neodesk/data ] || fail "after the update the data volume is mounted at $(data_mount)"
[ "$(in_container 'readlink /opt/rustdesk')" = /opt/neodesk/data ] || fail "the image has no link from /opt/rustdesk"
[ "$(in_container 'cat /opt/neodesk/data/id_ed25519.pub')" = "$key" ] || fail "the update changed the server's key"
[ "$(in_container 'cd /opt/neodesk/data && sha256sum id_ed25519 .api_key .remote_target_vault_key db_v2.sqlite3 | cut -c1-64')" = "$files" ] \
    || fail "the key, the API key, the vault key or the database changed at the update"
[ "$(in_container 'sqlite3 /opt/neodesk/data/db_v2.sqlite3 "SELECT COUNT(*) FROM users"')" = "$users" ] || fail "the accounts changed at the update"
[ "$(in_container 'cat /opt/neodesk/data/marker')" = before ] || fail "a file of the data volume is gone after the update"
if grep -q '^NEODESK_DATA_PATH=' "$DIR/.env"; then fail "the image of this checkout got NEODESK_DATA_PATH: $(grep NEODESK_DATA_PATH "$DIR/.env")"; fi

echo "== the backup from before the update goes back"
in_container 'echo after > /opt/neodesk/data/marker-after'
(
    # The manager's own functions, in the install folder (the variables are
    # the manager's)
    # shellcheck disable=SC2034
    NEODESK_DOCKER_SOURCED=1
    # shellcheck source=/dev/null
    source "$SCRIPT_ROOT/neodesk-docker.sh"
    # shellcheck disable=SC2034
    NEODESK_DIR="$DIR"
    # shellcheck disable=SC2034
    NEODESK_IMAGE_TAG_SET=""
    compose stop -t 30 neodesk >/dev/null 2>&1
    restore_volumes "$old_backup"
    compose_quiet up -d neodesk
    wait_healthy
) > "$WORK/restore.log" 2>&1 || fail "the restore of $OLD_TAG's backup failed: $(tail -n 20 "$WORK/restore.log")"
[ "$(in_container 'cat /opt/neodesk/data/marker')" = before ] || fail "the restore did not bring the backup's files back"
if in_container 'test -e /opt/neodesk/data/marker-after'; then fail "the restore kept a file made after the backup"; fi
[ "$(in_container 'cat /opt/neodesk/data/id_ed25519.pub')" = "$key" ] || fail "the restore changed the server's key"

echo "== uninstall"
bash "$SCRIPT_ROOT/neodesk-docker.sh" uninstall --dir "$DIR" --purge --yes > "$WORK/uninstall.log" 2>&1 \
    || fail "the uninstall failed: $(tail -n 20 "$WORK/uninstall.log")"
rm -rf "$WORK"
echo "PASS: the Docker image's data folder"
