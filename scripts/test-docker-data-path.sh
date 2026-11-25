#!/usr/bin/env bash
# The Docker image's data folder on a real Docker (L446): an image of this
# checkout keeps its data in /opt/neodesk/data and has nothing at
# /opt/rustdesk. An install of 3.6.0-rc.4 (its data volume at /opt/rustdesk,
# with data in it) keeps its key, its key files, its database and its files
# when its own compose file starts the image of this checkout (a plain
# `docker compose pull && docker compose up -d`), and when this checkout's
# neodesk-docker.sh updates it, which mounts the volume at
# /opt/neodesk/data. A backup that rc.4's manager took before the update
# goes back into the volumes afterwards, and so does one made after it.
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

# restore BACKUP: the manager's restore of BACKUP into the volumes of the
# install, with the container stopped, and the start after it
restore() {
    (
        # The manager's own functions, in the install folder (the variables
        # are the manager's)
        # shellcheck disable=SC2034
        NEODESK_DOCKER_SOURCED=1
        # shellcheck source=/dev/null
        source "$SCRIPT_ROOT/install/neodesk-docker.sh"
        # shellcheck disable=SC2034
        NEODESK_DIR="$DIR"
        # shellcheck disable=SC2034
        NEODESK_IMAGE_TAG_SET=""
        compose stop -t 30 neodesk >/dev/null 2>&1
        restore_volumes "$1"
        compose_quiet up -d neodesk
        wait_healthy
    )
}

# wait_up: the container answers on the API's health URL
wait_up() {
    local i
    for i in $(seq 1 120); do
        curl -fs -o /dev/null http://127.0.0.1:21121/api/health && return 0
        sleep 2
    done
    return 1
}

echo "== the image of this checkout ($IMAGE:local)"
bash "$SCRIPT_ROOT/scripts/build-test-image.sh" local >/dev/null
docker run --rm --entrypoint sh "$IMAGE:local" -c 'test ! -e /opt/rustdesk && test ! -L /opt/rustdesk' \
    || fail "the image of this checkout has something at /opt/rustdesk: $(docker run --rm --entrypoint ls "$IMAGE:local" -ld /opt/rustdesk 2>&1)"

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

echo "== $OLD_TAG's compose file starts the image of this checkout"
cp "$DIR/.env" "$WORK/env-rc4"
sed -i 's/^NEODESK_IMAGE_TAG=.*/NEODESK_IMAGE_TAG=local/' "$DIR/.env"
(cd "$DIR" && docker compose up -d --pull never) > "$WORK/compose-up.log" 2>&1 \
    || fail "$OLD_TAG's compose file did not start the image of this checkout: $(tail -n 20 "$WORK/compose-up.log")"
wait_up || fail "the image of this checkout under $OLD_TAG's compose file did not come up: $(docker logs --tail 40 neodesk 2>&1)"
[ "$(data_mount)" = /opt/rustdesk ] || fail "$OLD_TAG's compose file mounts the data at $(data_mount)"
[ "$(in_container 'cat /opt/rustdesk/id_ed25519.pub')" = "$key" ] || fail "the image under $OLD_TAG's compose file has another key"
[ "$(in_container 'cd /opt/rustdesk && sha256sum id_ed25519 .api_key .remote_target_vault_key db_v2.sqlite3 | cut -c1-64')" = "$files" ] \
    || fail "under $OLD_TAG's compose file the key, the API key, the vault key or the database changed"
if in_container 'test -e /opt/neodesk/data/id_ed25519 || test -e /opt/neodesk/data/db_v2.sqlite3'; then
    fail "under $OLD_TAG's compose file the server made a key or database outside the data volume"
fi
docker logs neodesk > "$WORK/start.log" 2>&1
grep -q 'mounted at /opt/rustdesk' "$WORK/start.log" || fail "the start does not say the data volume is at the old path: $(head -n 20 "$WORK/start.log")"
cp "$WORK/env-rc4" "$DIR/.env"

echo "== the update with this checkout's manager"
bash "$SCRIPT_ROOT/install/neodesk-docker.sh" update --dir "$DIR" --tag local --no-pull --yes \
    > "$WORK/update.log" 2>&1 || fail "the update failed: $(tail -n 40 "$WORK/update.log")"
[ "$(data_mount)" = /opt/neodesk/data ] || fail "after the update the data volume is mounted at $(data_mount)"
if in_container 'test -e /opt/rustdesk || test -L /opt/rustdesk'; then fail "after the update the container has something at /opt/rustdesk"; fi
[ "$(in_container 'cat /opt/neodesk/data/id_ed25519.pub')" = "$key" ] || fail "the update changed the server's key"
[ "$(in_container 'cd /opt/neodesk/data && sha256sum id_ed25519 .api_key .remote_target_vault_key db_v2.sqlite3 | cut -c1-64')" = "$files" ] \
    || fail "the key, the API key, the vault key or the database changed at the update"
[ "$(in_container 'sqlite3 /opt/neodesk/data/db_v2.sqlite3 "SELECT COUNT(*) FROM users"')" = "$users" ] || fail "the accounts changed at the update"
[ "$(in_container 'cat /opt/neodesk/data/marker')" = before ] || fail "a file of the data volume is gone after the update"
if grep -q '^NEODESK_DATA_PATH=' "$DIR/.env"; then fail "the image of this checkout got NEODESK_DATA_PATH: $(grep NEODESK_DATA_PATH "$DIR/.env")"; fi

echo "== the backup from before the update goes back"
in_container 'echo after > /opt/neodesk/data/marker-after'
restore "$old_backup" > "$WORK/restore.log" 2>&1 || fail "the restore of $OLD_TAG's backup failed: $(tail -n 20 "$WORK/restore.log")"
[ "$(in_container 'cat /opt/neodesk/data/marker')" = before ] || fail "the restore did not bring the backup's files back"
if in_container 'test -e /opt/neodesk/data/marker-after'; then fail "the restore kept a file made after the backup"; fi
[ "$(in_container 'cat /opt/neodesk/data/id_ed25519.pub')" = "$key" ] || fail "the restore changed the server's key"

echo "== a backup made after the update goes back"
in_container 'echo second > /opt/neodesk/data/marker'
bash "$SCRIPT_ROOT/install/neodesk-docker.sh" backup --dir "$DIR" > "$WORK/backup.log" 2>&1 \
    || fail "the backup after the update failed: $(tail -n 20 "$WORK/backup.log")"
new_backup=$(ls -1t "$DIR"/backups/neodesk-*.tar.gz | head -n 1)
[ "$new_backup" != "$old_backup" ] || fail "the backup after the update made no new file"
# The listing goes to a file first: grep -q stops reading at its first
# match, and with pipefail tar's broken pipe would fail the check
tar -tzf "$new_backup" > "$WORK/new-backup.list" || fail "the backup after the update can't be listed"
grep -qx 'neodesk/marker' "$WORK/new-backup.list" || fail "the backup after the update has no folder neodesk: $(head -n 5 "$WORK/new-backup.list")"
if grep -q '^rustdesk/' "$WORK/new-backup.list"; then fail "the backup after the update has a folder rustdesk"; fi
in_container 'echo third > /opt/neodesk/data/marker && echo after > /opt/neodesk/data/marker-after'
restore "$new_backup" > "$WORK/restore-new.log" 2>&1 || fail "the restore of the new backup failed: $(tail -n 20 "$WORK/restore-new.log")"
[ "$(in_container 'cat /opt/neodesk/data/marker')" = second ] || fail "the restore of the new backup did not bring its files back"
if in_container 'test -e /opt/neodesk/data/marker-after'; then fail "the restore of the new backup kept a file made after it"; fi
[ "$(in_container 'cat /opt/neodesk/data/id_ed25519.pub')" = "$key" ] || fail "the restore of the new backup changed the server's key"

echo "== uninstall"
bash "$SCRIPT_ROOT/install/neodesk-docker.sh" uninstall --dir "$DIR" --purge --yes > "$WORK/uninstall.log" 2>&1 \
    || fail "the uninstall failed: $(tail -n 20 "$WORK/uninstall.log")"
rm -rf "$WORK"
echo "PASS: the Docker image's data folder"
