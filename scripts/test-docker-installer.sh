#!/usr/bin/env bash
# Checks of neodesk-docker.sh without Docker: the manager's functions
# run in a shell of their own with the script's own settings (set -Eeuo
# pipefail), loaded without its main, and docker, curl, ss and id are
# stand-ins that keep their state in files of a temporary folder. At the
# end the checks of the image and compose files run
# (test-docker-entrypoint.sh).
#
#   bash scripts/test-docker-installer.sh
set -euo pipefail

SCRIPT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

fail() {
    printf 'FAIL: %s\n' "$*" >&2
    exit 1
}

# load: a temporary folder with the install folder in it, and prelude.sh,
# which loads the manager's functions with Docker, the network and the
# clock replaced. The test's shell loads it too (for env_get, env_set).
load() {
    set --
    TEST_DIR=$(mktemp -d)
    : > "$TEST_DIR/calls"
    printf 'sha256:old\n' > "$TEST_DIR/good-image"
    printf 'sha256:old\n' > "$TEST_DIR/pulled"
    {
        printf 'NEODESK_DOCKER_SOURCED=1\nsource %q\n' "$SCRIPT_ROOT/install/neodesk-docker.sh"
        printf 'TEST_DIR=%q\nNEODESK_DIR=%q\nCALLS=%q\n' "$TEST_DIR" "$TEST_DIR/install" "$TEST_DIR/calls"
        printf 'HEALTH_TIMEOUT=4\nRED="" GREEN="" YELLOW="" CYAN="" BOLD="" NC=""\n'
        printf 'sleep() { :; }\nid() { printf "0\\n"; }\n'
        declare -f docker curl ss command_docker
    } > "$TEST_DIR/prelude.sh"
    # shellcheck source=/dev/null
    source "$TEST_DIR/prelude.sh"
    set +eu
}

# mgr CODE: runs CODE (manager functions, settings, stand-ins of its own)
# in a new shell, with the manager's set -Eeuo pipefail, as the script runs
mgr() {
    bash -c 'source "$1"; eval "$2"' mgr "$TEST_DIR/prelude.sh" "$1"
}

# docker: what the manager asks of Docker, recorded in $CALLS. State:
# exists, running (the container), current-image, pulled (the image the
# tag names), dir (the container's compose folder).
docker() {
    printf '%s\n' "$*" >> "$CALLS"
    local arg format="" prev="" out="" file=""
    for arg in "$@"; do
        [ "$prev" = -f ] && format="$arg"
        [ "$prev" = -v ] && [[ "$arg" == *:/out ]] && out="${arg%:/out}"
        [[ "$arg" == /out/* ]] && file="${arg#/out/}"
        prev="$arg"
    done
    case "$1" in
        info) return 0 ;;
        compose)
            case "$2" in
                version) [ "${3:-}" = --short ] && printf '2.29.1\n'; return 0 ;;
                pull)
                    # missing-<tag>: ghcr.io has no image of that tag
                    local tag
                    tag=$(grep -E '^NEODESK_IMAGE_TAG=' "$NEODESK_DIR/.env" 2>/dev/null | tail -n 1 | cut -d= -f2)
                    if [ -f "$TEST_DIR/pull-fails" ] || [ -f "$TEST_DIR/missing-${tag:-latest}" ]; then printf 'manifest unknown
' >&2; return 1; fi
                    return 0 ;;
                up)
                    touch "$TEST_DIR/exists" "$TEST_DIR/running"
                    cp "$TEST_DIR/pulled" "$TEST_DIR/current-image" ;;
                stop|down) rm -f "$TEST_DIR/running" ;;
                start) touch "$TEST_DIR/running" ;;
                config) [ "${3:-}" = --hash ] && printf 'neodesk newhash\n' ;;
                run)
                    printf '%s\n' "${DATABASE_URL:-}" > "$TEST_DIR/run-env"
                    if [ -f "$TEST_DIR/move-fails" ]; then printf 'ERROR: the move broke\n' >&2; return 1; fi
                    printf 'Migration complete: 12 rows in 30 tables moved to PostgreSQL\n' ;;
            esac
            return 0 ;;
        container)
            if [ "${!#}" = neodesk-postgres ]; then
                [[ "$format" != *Health* ]] || printf 'healthy\n'
                return 0
            fi
            [ -f "$TEST_DIR/exists" ] && [ "${!#}" = neodesk ] || return 1
            case "$format" in
                *State.Running*) if [ -f "$TEST_DIR/running" ]; then printf 'true\n'; else printf 'false\n'; fi ;;
                *working_dir*) cat "$TEST_DIR/dir" 2>/dev/null || printf '%s\n' "$NEODESK_DIR" ;;
                *config-hash*) printf 'oldhash\n' ;;
                *Config.Image*) printf 'ghcr.io/neodeskapp/neodesk:latest\n' ;;
                *Image*) cat "$TEST_DIR/current-image" ;;
            esac
            return 0 ;;
        image)
            [ "$2" = inspect ] || return 0
            # image-env: the image's environment (one of this release
            # keeps its data in /opt/neodesk/data)
            case "$format" in
                *Config.Env*) cat "$TEST_DIR/image-env" 2>/dev/null || printf 'RUSTDESK_PATH=/opt/neodesk/data\n' ;;
                ?*) cat "$TEST_DIR/pulled" ;;
            esac
            return 0 ;;
        exec)
            case "$*" in
                *-version*) printf 'neodesk-server 3.5.270 (built test)\n' ;;
                *.admin_credentials*)
                    [ ! -f "$TEST_DIR/first-password" ] || printf 'Admin Username: admin\nAdmin Password: %s\n' "$(cat "$TEST_DIR/first-password")" ;;
                *id_ed25519.pub*) printf 'PUBKEYbase64=\n' ;;
                *reset-password*) cat > /dev/null ;;
            esac
            return 0 ;;
        run)
            [ -z "$out" ] || [ -z "$file" ] || : > "$out/$file"
            return 0 ;;
        tag) cut -d' ' -f1 <<< "$2" > "$TEST_DIR/pulled"; return 0 ;;
        volume) return 1 ;;
        logs) printf 'the log\n'; return 0 ;;
    esac
    return 0
}

# curl: ghcr.io answers, the public address is 203.0.113.10, and the health
# URLs answer while the container runs the good image
curl() {
    local url="${!#}"
    case "$url" in
        *ghcr.io/v2/*) return 0 ;;
        *checkip*|*ipify*) printf '203.0.113.10\n' ;;
        *health*)
            [ -f "$TEST_DIR/running" ] && [ "$(cat "$TEST_DIR/current-image" 2>/dev/null)" = "$(cat "$TEST_DIR/good-image")" ] ;;
        *) return 1 ;;
    esac
}

# ss: a port is taken when $TEST_DIR/taken-<port> holds the program's name
ss() {
    local port="${2##*:}"
    [ -f "$TEST_DIR/taken-$port" ] || return 0
    printf 'LISTEN 0 4096 0.0.0.0:%s 0.0.0.0:* users:(("%s",pid=7,fd=3))\n' "$port" "$(cat "$TEST_DIR/taken-$port")"
}

# The stand-in for Docker, callable from a test's own docker()
eval "command_docker() $(declare -f docker | tail -n +2)"

# A fresh install writes the settings and the release's compose file,
# starts the container, waits for it and prints the console address, the
# first password once and the client settings. No file of the install
# folder holds the password, and running the script again prints none.
test_fresh_install() (
    load
    printf 'first-pass-0123\n' > "$TEST_DIR/first-password"
    local out
    out=$(mgr 'COMMAND=install; do_install' 2>&1) || fail "a fresh install failed: $out"
    grep -qx 'RELAY_SERVERS=203.0.113.10' "$NEODESK_DIR/.env" || fail ".env has no relay address"
    cmp -s "$NEODESK_DIR/docker-compose.yml" "$SCRIPT_ROOT/install/docker-compose.quick.single.yml" || fail "the compose file is not the release's"
    grep -qx 'compose up -d --remove-orphans' "$CALLS" || fail "the container was not started: $(cat "$CALLS")"
    printf '%s' "$out" | grep -q 'Web console: *http://203.0.113.10:5000' || fail "no console address in: $out"
    printf '%s' "$out" | grep -q 'ID server: *203.0.113.10' || fail "no ID server in: $out"
    printf '%s' "$out" | grep -q 'Key: *PUBKEYbase64=' || fail "no key in: $out"
    printf '%s' "$out" | grep -q 'docker compose logs -f neodesk' || fail "no logs hint in: $out"
    [ "$(printf '%s' "$out" | grep -c 'first-pass-0123')" = 1 ] || fail "the first password is not printed exactly once: $out"
    if grep -rq 'first-pass-0123' "$NEODESK_DIR"; then fail "a file in the install folder holds the password"; fi

    out=$(mgr 'COMMAND=install; do_install' 2>&1) || fail "the second run failed: $out"
    if printf '%s' "$out" | grep -q 'first-pass-0123'; then fail "the second run printed the password again"; fi
    rm -rf "$TEST_DIR"
)

# L254: before any release, ghcr.io has no latest image. Without a tag
# given or saved the install runs the development image dev and says so,
# as the native installers take the development build; a tag given stays
# and fails with what to do.
test_no_release_image_runs_dev() (
    load
    : > "$TEST_DIR/missing-latest"
    local out
    out=$(mgr 'COMMAND=install; do_install' 2>&1) || fail "an install without a release image failed: $out"
    grep -qx 'NEODESK_IMAGE_TAG=dev' "$NEODESK_DIR/.env" || fail "the install does not run dev: $(cat "$NEODESK_DIR/.env")"
    printf '%s' "$out" | grep -q 'no release image yet' || fail "the install does not say it took the development image: $out"
    rm -rf "$NEODESK_DIR"
    if out=$(mgr 'NEODESK_IMAGE_TAG_SET=latest; COMMAND=install; do_install' 2>&1); then fail "an install with latest given ran another image"; fi
    printf '%s' "$out" | grep -q 'was not found on ghcr.io' || fail "a missing given tag is not said: $out"
    rm -rf "$TEST_DIR"
)

# An update backs the volumes up, with the container stopped, before the
# new container starts; when the new image does not get healthy, the
# previous one runs again.
test_update_goes_back_when_unhealthy() (
    load
    mgr 'COMMAND=install; do_install' > /dev/null 2>&1 || fail "the install failed"
    : > "$CALLS"
    local out stop_line backup_line
    if out=$(mgr 'docker() {
            if [ "$1" = compose ] && [ "$2" = pull ]; then
                printf "%s\n" "$*" >> "$CALLS"
                printf "sha256:new\n" > "$TEST_DIR/pulled"
                return 0
            fi
            command_docker "$@"
        }
        COMMAND=update; do_update' 2>&1); then fail "an update to an unhealthy image reported success: $out"; fi
    printf '%s' "$out" | grep -q 'runs the previous version again' || fail "no word of the way back: $out"
    grep -q '^tag sha256:old ' "$CALLS" || fail "the previous image was not tagged again: $(cat "$CALLS")"
    grep -q 'compose up -d --force-recreate neodesk' "$CALLS" || fail "the container was not made again from the previous image"
    [ "$(cat "$TEST_DIR/current-image")" = sha256:old ] || fail "the container does not run the previous image"
    ls "$NEODESK_DIR"/backups/neodesk-*.tar.gz > /dev/null 2>&1 || fail "no backup before the update"
    stop_line=$(grep -n 'compose stop' "$CALLS" | head -n 1 | cut -d: -f1)
    backup_line=$(grep -n '^run .*tar' "$CALLS" | head -n 1 | cut -d: -f1)
    { [ -n "$stop_line" ] && [ -n "$backup_line" ] && [ "$stop_line" -lt "$backup_line" ]; } \
        || fail "the backup was not taken with the container stopped"
    rm -rf "$TEST_DIR"
)

# L460: an update's backup holds .env and docker-compose.yml as they were
# before the update, not the files it writes. When the new container does
# not get healthy, the way back puts those files back and gives the
# previous image its own tag again, not the new release's.
test_update_backs_up_the_files_from_before() (
    load
    mgr 'NEODESK_IMAGE_TAG_SET=3.6.0-rc.3; COMMAND=install; do_install' > /dev/null 2>&1 || fail "the install failed"
    # The compose file of the release before, as its manager wrote it
    printf '# the compose file of 3.6.0-rc.3\n' >> "$NEODESK_DIR/docker-compose.yml"
    sha256sum "$NEODESK_DIR/docker-compose.yml" | cut -d' ' -f1 > "$NEODESK_DIR/.compose.sha256"
    cp "$NEODESK_DIR/.env" "$TEST_DIR/env-before"
    cp "$NEODESK_DIR/docker-compose.yml" "$TEST_DIR/compose-before"
    # docker: the pull brings sha256:new; the container runs 3.6.0-rc.3
    local update='docker() {
            case "$1 ${2:-}" in
                "compose pull")
                    printf "%s\n" "$*" >> "$CALLS"
                    printf "sha256:new\n" > "$TEST_DIR/pulled"
                    return 0 ;;
                "container inspect")
                    case "$*" in *Config.Image*) printf "ghcr.io/neodeskapp/neodesk:3.6.0-rc.3\n"; return 0 ;; esac ;;
            esac
            command_docker "$@"
        }
        NEODESK_IMAGE_TAG_SET=3.6.0-rc.4; COMMAND=update; do_update'
    : > "$CALLS"
    local out backup
    if out=$(mgr "$update" 2>&1); then fail "an update to an unhealthy image reported success: $out"; fi
    backup=$(ls "$NEODESK_DIR"/backups/neodesk-*.env 2>/dev/null | head -n 1)
    [ -n "$backup" ] || fail "the backup has no .env"
    cmp -s "$backup" "$TEST_DIR/env-before" \
        || fail "the backup's .env is not the one from before the update: $(grep IMAGE_TAG "$backup")"
    cmp -s "${backup%.env}.docker-compose.yml" "$TEST_DIR/compose-before" \
        || fail "the backup's docker-compose.yml is not the one from before the update"
    grep -qx 'tag sha256:old ghcr.io/neodeskapp/neodesk:3.6.0-rc.3' "$CALLS" \
        || fail "the previous image did not get its own tag back: $(grep '^tag' "$CALLS")"
    if grep -q '^tag .*3\.6\.0-rc\.4' "$CALLS"; then fail "the previous image was tagged as the new release: $(grep '^tag' "$CALLS")"; fi
    cmp -s "$NEODESK_DIR/.env" "$TEST_DIR/env-before" \
        || fail "the way back did not put .env back: $(grep IMAGE_TAG "$NEODESK_DIR/.env")"
    cmp -s "$NEODESK_DIR/docker-compose.yml" "$TEST_DIR/compose-before" || fail "the way back did not put docker-compose.yml back"
    printf '%s' "$out" | grep -q 'runs the previous version again' || fail "no word of the way back: $out"

    # A good update keeps the files from before in its backup too
    rm -f "$NEODESK_DIR"/backups/neodesk-*
    printf 'sha256:new\n' > "$TEST_DIR/good-image"
    out=$(mgr "$update" 2>&1) || fail "the update failed: $out"
    backup=$(ls "$NEODESK_DIR"/backups/neodesk-*.env 2>/dev/null | head -n 1)
    [ -n "$backup" ] && cmp -s "$backup" "$TEST_DIR/env-before" \
        || fail "the backup of a good update has another .env: $(grep IMAGE_TAG "$backup")"
    cmp -s "${backup%.env}.docker-compose.yml" "$TEST_DIR/compose-before" \
        || fail "the backup of a good update has another docker-compose.yml"
    grep -qx 'NEODESK_IMAGE_TAG=3.6.0-rc.4' "$NEODESK_DIR/.env" || fail "the update did not keep the new tag: $(cat "$NEODESK_DIR/.env")"
    rm -rf "$TEST_DIR"
)

# L446: the data volume is mounted at /opt/neodesk/data, where the image
# keeps its data; no line of the compose files uses /opt/rustdesk.
test_data_volume_at_the_neodesk_path() (
    load
    mgr 'COMMAND=install; do_install' > /dev/null 2>&1 || fail "the install failed"
    grep -qF -- '- neodesk-data:${NEODESK_DATA_PATH:-/opt/neodesk/data}' "$NEODESK_DIR/docker-compose.yml" \
        || fail "the compose file mounts the data volume elsewhere: $(grep 'neodesk-data:' "$NEODESK_DIR/docker-compose.yml")"
    local f
    for f in install/docker-compose.quick.single.yml install/docker-compose.quick.single.macvlan.yml install/docker-compose.single.yml; do
        if grep -nE '^[^#]*/opt/rustdesk' "$SCRIPT_ROOT/$f"; then fail "$f uses /opt/rustdesk"; fi
    done
    if grep -q '^NEODESK_DATA_PATH=' "$NEODESK_DIR/.env"; then fail "an image of this release got NEODESK_DATA_PATH"; fi
    rm -rf "$TEST_DIR"
)

# L446: an update of an install from before (3.6.0-rc.4: the data volume
# at /opt/rustdesk) gets the release's compose file, with the same volume
# at /opt/neodesk/data. The image has nothing at /opt/rustdesk any more, so
# the update rewrites the old path where the install names it: in .env, in
# a compose file changed by hand (which stays, with the user's changes) and
# in docker-compose.override.yml, where it becomes the data path the
# release's compose file mounts at. The files as they were are in the
# update's backup, and a failed update puts them back. An image pinned from
# before keeps its data path (NEODESK_DATA_PATH=/opt/rustdesk) and its
# files, and an image of this release takes the setting away again.
test_update_from_the_old_data_path() (
    load
    mgr 'NEODESK_IMAGE_TAG_SET=3.6.0-rc.4; COMMAND=install; do_install' > /dev/null 2>&1 || fail "the install failed"
    # The compose file of 3.6.0-rc.4, as its manager wrote it
    sed -e 's|${NEODESK_DATA_PATH:-/opt/neodesk/data}|/opt/rustdesk|g' "$SCRIPT_ROOT/install/docker-compose.quick.single.yml" \
        > "$NEODESK_DIR/docker-compose.yml"
    sha256sum "$NEODESK_DIR/docker-compose.yml" | cut -d' ' -f1 > "$NEODESK_DIR/.compose.sha256"
    printf 'SSL_CERT_PATH=/opt/rustdesk/ssl/console.pem\n' >> "$NEODESK_DIR/.env"
    printf 'services:\n  neodesk:\n    volumes:\n      - /srv/neodesk:/opt/rustdesk\n' > "$NEODESK_DIR/docker-compose.override.yml"
    cp "$NEODESK_DIR/.env" "$TEST_DIR/env-before"
    cp "$NEODESK_DIR/docker-compose.override.yml" "$TEST_DIR/override-before"
    printf 'sha256:new\n' > "$TEST_DIR/good-image"
    local pull='docker() {
            if [ "$1 ${2:-}" = "compose pull" ]; then printf "sha256:new\n" > "$TEST_DIR/pulled"; return 0; fi
            command_docker "$@"
        }'
    local out backup
    out=$(mgr "$pull; NEODESK_IMAGE_TAG_SET=latest; COMMAND=update; do_update" 2>&1) || fail "the update failed: $out"
    cmp -s "$NEODESK_DIR/docker-compose.yml" "$SCRIPT_ROOT/install/docker-compose.quick.single.yml" || fail "the update kept the old compose file"
    grep -qx 'SSL_CERT_PATH=/opt/neodesk/data/ssl/console.pem' "$NEODESK_DIR/.env" \
        || fail "a setting that names the old path was not rewritten: $(grep SSL_CERT_PATH "$NEODESK_DIR/.env")"
    grep -qxF '      - /srv/neodesk:${NEODESK_DATA_PATH:-/opt/neodesk/data}' "$NEODESK_DIR/docker-compose.override.yml" \
        || fail "docker-compose.override.yml still mounts at the old path: $(cat "$NEODESK_DIR/docker-compose.override.yml")"
    if grep -q '^NEODESK_DATA_PATH=' "$NEODESK_DIR/.env"; then fail "an image of this release got NEODESK_DATA_PATH"; fi
    printf '%s' "$out" | grep -q '/opt/neodesk/data' || fail "the update does not say it rewrote the old path: $out"
    backup=$(ls -1t "$NEODESK_DIR"/backups/neodesk-*.env 2>/dev/null | head -n 1)
    cmp -s "$backup" "$TEST_DIR/env-before" || fail "the update's backup does not hold .env as it was"
    cmp -s "${backup%.env}.docker-compose.override.yml" "$TEST_DIR/override-before" \
        || fail "the update's backup does not hold docker-compose.override.yml as it was"

    # A compose file of 3.6.0-rc.4 changed by hand: the user's change stays,
    # the old path goes
    sed -e 's|${NEODESK_DATA_PATH:-/opt/neodesk/data}|/opt/rustdesk|g' "$SCRIPT_ROOT/install/docker-compose.quick.single.yml" \
        > "$NEODESK_DIR/docker-compose.yml"
    printf '# my change\n' >> "$NEODESK_DIR/docker-compose.yml"
    cp "$NEODESK_DIR/docker-compose.yml" "$TEST_DIR/compose-before"
    printf 'sha256:newer\n' > "$TEST_DIR/good-image"
    out=$(mgr 'docker() {
            if [ "$1 ${2:-}" = "compose pull" ]; then printf "sha256:newer\n" > "$TEST_DIR/pulled"; return 0; fi
            command_docker "$@"
        }
        NEODESK_IMAGE_TAG_SET=latest; COMMAND=update; do_update' 2>&1) || fail "the update of a changed compose file failed: $out"
    grep -q '^# my change' "$NEODESK_DIR/docker-compose.yml" || fail "the update dropped the change by hand"
    if grep -nE '^[^#]*/opt/rustdesk' "$NEODESK_DIR/docker-compose.yml"; then fail "the compose file changed by hand still uses the old path"; fi
    grep -qF -- '- neodesk-data:${NEODESK_DATA_PATH:-/opt/neodesk/data}' "$NEODESK_DIR/docker-compose.yml" \
        || fail "the compose file changed by hand mounts the data volume at: $(grep 'neodesk-data:' "$NEODESK_DIR/docker-compose.yml")"
    backup=$(ls -1t "$NEODESK_DIR"/backups/neodesk-*.docker-compose.yml 2>/dev/null | head -n 1)
    cmp -s "$backup" "$TEST_DIR/compose-before" || fail "the update's backup does not hold the changed compose file as it was"

    # A failed update puts the files with the old path back, for the image
    # from before
    cp "$TEST_DIR/compose-before" "$NEODESK_DIR/docker-compose.yml"
    cp "$TEST_DIR/env-before" "$NEODESK_DIR/.env"
    cp "$TEST_DIR/override-before" "$NEODESK_DIR/docker-compose.override.yml"
    if out=$(mgr 'docker() {
            if [ "$1 ${2:-}" = "compose pull" ]; then printf "sha256:broken\n" > "$TEST_DIR/pulled"; return 0; fi
            command_docker "$@"
        }
        NEODESK_IMAGE_TAG_SET=latest; COMMAND=update; do_update' 2>&1); then fail "an update to an unhealthy image reported success: $out"; fi
    cmp -s "$NEODESK_DIR/.env" "$TEST_DIR/env-before" || fail "the way back did not put .env back"
    cmp -s "$NEODESK_DIR/docker-compose.yml" "$TEST_DIR/compose-before" || fail "the way back did not put docker-compose.yml back"
    cmp -s "$NEODESK_DIR/docker-compose.override.yml" "$TEST_DIR/override-before" \
        || fail "the way back did not put docker-compose.override.yml back"

    # An image pinned from before the rename keeps its data where it looks
    rm -f "$NEODESK_DIR/docker-compose.override.yml"
    printf 'PATH=/usr/bin\nRUSTDESK_PATH=/opt/rustdesk\n' > "$TEST_DIR/image-env"
    printf 'sha256:old\n' > "$TEST_DIR/good-image"
    out=$(mgr 'docker() {
            if [ "$1 ${2:-}" = "compose pull" ]; then printf "sha256:old\n" > "$TEST_DIR/pulled"; return 0; fi
            command_docker "$@"
        }
        NEODESK_IMAGE_TAG_SET=3.6.0-rc.4; COMMAND=update; do_update' 2>&1) || fail "the update to a pinned older image failed: $out"
    grep -qx 'NEODESK_DATA_PATH=/opt/rustdesk' "$NEODESK_DIR/.env" \
        || fail "an older image's data path is not in .env: $(cat "$NEODESK_DIR/.env")"
    grep -qx 'SSL_CERT_PATH=/opt/rustdesk/ssl/console.pem' "$NEODESK_DIR/.env" \
        || fail "a setting for an older image was rewritten: $(grep SSL_CERT_PATH "$NEODESK_DIR/.env")"
    rm -f "$TEST_DIR/image-env"
    printf 'sha256:new\n' > "$TEST_DIR/good-image"
    out=$(mgr "$pull; NEODESK_IMAGE_TAG_SET=latest; COMMAND=update; do_update" 2>&1) || fail "the update back to this release failed: $out"
    if grep -q '^NEODESK_DATA_PATH=' "$NEODESK_DIR/.env"; then fail "NEODESK_DATA_PATH stayed with an image of this release"; fi
    rm -rf "$TEST_DIR"
)

# L446: a backup holds the folders neodesk (the data volume) and console;
# one from before the rename (rustdesk and console) goes back into the same
# volumes.
test_old_backup_restores() (
    load
    mgr 'COMMAND=install; do_install' > /dev/null 2>&1 || fail "the install failed"
    : > "$CALLS"
    mgr 'backup_volumes' > /dev/null 2>&1 || fail "the backup failed"
    grep -q '^run .*-v neodesk-data:/backup/neodesk:ro .*czf /out/neodesk-[0-9-]*\.tar\.gz -C /backup neodesk console' "$CALLS" \
        || fail "a backup does not hold the folders neodesk and console: $(grep '^run' "$CALLS")"
    : > "$CALLS"
    mgr "restore_volumes '$NEODESK_DIR/backups/neodesk-20260101-000000.tar.gz'" > /dev/null 2>&1 || fail "the restore failed"
    grep -q '^run .*-v neodesk-data:/backup/neodesk .*-v neodesk-console-data:/backup/console .*tar xzf' "$CALLS" \
        || fail "the restore does not put a backup's folder neodesk into the data volume: $(cat "$CALLS")"
    grep -q '^run .*-v neodesk-data:/backup/rustdesk .*tar xzf' "$CALLS" \
        || fail "the restore does not put an older backup's folder rustdesk into the data volume: $(cat "$CALLS")"
    rm -rf "$TEST_DIR"
)

# L125: a compose file changed by hand stays on update; the release's file
# goes next to it.
test_update_keeps_compose_edits() (
    load
    mgr 'COMMAND=install; do_install' > /dev/null 2>&1 || fail "the install failed"
    printf '# my change\n' >> "$NEODESK_DIR/docker-compose.yml"
    mgr 'write_compose_file' 2> /dev/null
    grep -q '^# my change' "$NEODESK_DIR/docker-compose.yml" || fail "the update overwrote a changed compose file"
    cmp -s "$NEODESK_DIR/docker-compose.yml.new" "$SCRIPT_ROOT/install/docker-compose.quick.single.yml" \
        || fail "the release's compose file is not next to it"
    rm -rf "$TEST_DIR"
)

# L125: a new install with --postgres runs on PostgreSQL: the settings name
# it, and the made-up password is printed nowhere.
test_fresh_install_on_postgres() (
    load
    local out pass
    out=$(mgr 'USE_POSTGRES=true; COMMAND=install; do_install' 2>&1) || fail "a PostgreSQL install failed: $out"
    pass=$(env_get POSTGRES_PASSWORD)
    [ "${#pass}" -ge 32 ] || fail "no PostgreSQL password made"
    [ "$(env_get DATABASE_URL)" = "postgres://neodesk:${pass}@postgres:5432/neodesk?sslmode=disable" ] \
        || fail "DATABASE_URL is $(env_get DATABASE_URL)"
    [ "$(env_get COMPOSE_PROFILES)" = postgres ] || fail "the postgres service is not on"
    # L70: the address alone names the database
    if grep -q '^DB_TYPE=' "$NEODESK_DIR/.env"; then fail "the settings name the database type besides DATABASE_URL"; fi
    if printf '%s' "$out" | grep -qF "$pass"; then fail "the PostgreSQL password was printed"; fi
    grep -qE '^  postgres:' "$NEODESK_DIR/docker-compose.yml" || fail "the compose file has no postgres service"
    rm -rf "$TEST_DIR"
)

# NeoDesk from another install (an older manager, a split install) is
# not touched: the error says how to take it over.
test_foreign_install_is_left_alone() (
    load
    touch "$TEST_DIR/exists" "$TEST_DIR/running"
    printf '/root/old-neodesk\n' > "$TEST_DIR/dir"
    local out
    if out=$(mgr 'COMMAND=install; do_install' 2>&1); then fail "an install over another install's container went on"; fi
    printf '%s' "$out" | grep -q -- '--adopt' || fail "the error does not name --adopt: $out"
    [ ! -e "$NEODESK_DIR/.env" ] || fail "the install folder was written although another install runs"
    if grep -qE '^compose (up|stop|down)|^stop |^rm ' "$CALLS"; then fail "the other install's container was touched"; fi
    rm -rf "$TEST_DIR"
)

# Errors say what to do: a port taken, Compose v1 only, no Docker, no
# sudo, no internet.
test_errors_say_what_to_do() (
    load
    local out
    printf 'python3\n' > "$TEST_DIR/taken-5000"
    if out=$(mgr 'COMMAND=install; do_install' 2>&1); then fail "an install with port 5000 taken went on"; fi
    printf '%s' "$out" | grep -q 'TCP 5000: python3' || fail "the port error does not name the program: $out"
    printf '%s' "$out" | grep -q -- '--console-port' || fail "the port error does not name --console-port: $out"
    rm -f "$TEST_DIR/taken-5000"
    printf 'hbbs\n' > "$TEST_DIR/taken-21116"
    if out=$(mgr 'COMMAND=install; do_install' 2>&1); then fail "an install with 21116 taken went on"; fi
    printf '%s' "$out" | grep -q 'RustDesk server' || fail "the error does not name the RustDesk server: $out"
    rm -f "$TEST_DIR/taken-21116"
    # L225: the compose file publishes the console's HTTPS port too
    printf 'nginx\n' > "$TEST_DIR/taken-5443"
    if out=$(mgr 'COMMAND=install; do_install' 2>&1); then fail "an install with 5443 taken went on"; fi
    printf '%s' "$out" | grep -q 'TCP 5443: nginx' || fail "the port error does not name 5443: $out"
    rm -f "$TEST_DIR/taken-5443"

    if out=$(mgr 'docker() { case "$1" in info) return 0 ;; *) return 1 ;; esac; }
              docker-compose() { :; }
              check_docker' 2>&1); then fail "Compose v1 alone passed the check"; fi
    printf '%s' "$out" | grep -q 'docker-compose-plugin' || fail "the Compose error does not say how to install v2: $out"

    if out=$(mgr 'unset -f docker; PATH="$TEST_DIR/empty"; INSTALL_DOCKER=false; check_docker' 2>&1); then
        fail "a missing Docker passed the check"
    fi
    printf '%s' "$out" | grep -q 'get.docker.com' || fail "the Docker error does not say how to install it: $out"

    if out=$(mgr 'id() { printf "1000\n"; }; docker() { return 1; }; need_root_or_docker' 2>&1); then
        fail "a user without root went on"
    fi
    printf '%s' "$out" | grep -q 'sudo' || fail "the root error does not name sudo: $out"

    if out=$(mgr 'curl() { return 6; }; check_internet' 2>&1); then fail "no internet passed the check"; fi
    printf '%s' "$out" | grep -q -- '--no-pull' || fail "the internet error does not say what to do: $out"
    rm -rf "$TEST_DIR"
)

# Uninstall says what it leaves; --purge without --yes and without a
# terminal deletes nothing.
test_uninstall_says_what_it_leaves() (
    load
    mgr 'COMMAND=install; do_install' > /dev/null 2>&1 || fail "the install failed"
    local out
    out=$(mgr 'do_uninstall' 2>&1) || fail "uninstall failed: $out"
    printf '%s' "$out" | grep -q 'neodesk-data' || fail "uninstall does not name the volumes it leaves: $out"
    printf '%s' "$out" | grep -q 'docker image rm' || fail "uninstall does not name the image it leaves: $out"
    if grep -q 'down -v' "$CALLS"; then fail "uninstall without --purge deleted the volumes"; fi
    if out=$(mgr 'PURGE=true; do_uninstall' 2>&1 < /dev/null); then fail "--purge without --yes went on"; fi
    if grep -q 'down -v' "$CALLS"; then fail "--purge without --yes deleted the volumes"; fi
    rm -rf "$TEST_DIR"
)

# L206: --purge of an install on PostgreSQL dumps the database before the
# volumes go: only NeoDesk stops for the backup, the postgres service
# keeps running for pg_dump.
test_purge_dumps_postgres_first() (
    load
    mgr 'USE_POSTGRES=true; COMMAND=install; do_install' > /dev/null 2>&1 || fail "the PostgreSQL install failed"
    : > "$CALLS"
    local out stop dump down
    out=$(mgr 'PURGE=true; ASSUME_YES=true; do_uninstall' 2>&1) || fail "--purge --yes failed: $out"
    if grep -qx 'compose stop -t 30' "$CALLS"; then fail "--purge stopped the postgres service before its dump: $(cat "$CALLS")"; fi
    stop=$(grep -n '^compose stop -t 30 neodesk$' "$CALLS" | head -n 1 | cut -d: -f1)
    dump=$(grep -n '^compose exec -T postgres pg_dump' "$CALLS" | head -n 1 | cut -d: -f1)
    down=$(grep -n 'down -v' "$CALLS" | head -n 1 | cut -d: -f1)
    { [ -n "$stop" ] && [ -n "$dump" ] && [ -n "$down" ] && [ "$stop" -lt "$dump" ] && [ "$dump" -lt "$down" ]; } \
        || fail "--purge did not stop NeoDesk, dump PostgreSQL and then delete the volumes, in that order: $(cat "$CALLS")"
    rm -rf "$TEST_DIR"
)

# --help names every option the script takes.
test_help_lists_every_option() (
    local opt help
    help=$(bash "$SCRIPT_ROOT/install/neodesk-docker.sh" --help)
    for opt in $(sed -n '/^parse_args() {/,/^}/p' "$SCRIPT_ROOT/install/neodesk-docker.sh" | grep -oE '^ +--[a-z-]+' | tr -d ' ' | sort -u); do
        printf '%s' "$help" | grep -q -- "$opt" || fail "--help does not list $opt"
    done
)

# L43: the switch to PostgreSQL stops NeoDesk, backs the volumes up,
# starts the postgres service, moves the data with the server's sqlite2pg
# and starts NeoDesk on PostgreSQL. The database address reaches the
# tool through the environment, never the command line.
test_postgres_switch_moves_the_data() (
    load
    mgr 'COMMAND=install; do_install' > /dev/null 2>&1 || fail "the install failed"
    env_set COMPOSE_PROFILES guacd
    # An older .env names its type
    env_set DB_TYPE sqlite
    : > "$CALLS"
    local out pass stop_line run_line
    out=$(mgr 'ASSUME_YES=true; do_postgres' 2>&1) || fail "the switch failed: $out"
    # L70: the address alone names the database
    if grep -q '^DB_TYPE=' "$NEODESK_DIR/.env"; then fail "after the switch .env still names a type: $(grep '^DB_TYPE=' "$NEODESK_DIR/.env")"; fi
    pass=$(env_get POSTGRES_PASSWORD)
    [ "${#pass}" -ge 32 ] || fail "no PostgreSQL password made"
    [ "$(env_get DATABASE_URL)" = "postgres://neodesk:${pass}@postgres:5432/neodesk?sslmode=disable" ] \
        || fail "DATABASE_URL after the switch: $(env_get DATABASE_URL)"
    [ "$(env_get COMPOSE_PROFILES)" = guacd,postgres ] || fail "COMPOSE_PROFILES after the switch: $(env_get COMPOSE_PROFILES)"
    # L446: the SQLite file where the image keeps it (DB_PATH)
    grep -qF 'compose run --rm --no-deps -T --user 10001:10001 --entrypoint sh neodesk -c exec /usr/local/bin/neodesk-server migrate -mode sqlite2pg -src "$DB_PATH"' "$CALLS" \
        || fail "the data was not moved with sqlite2pg: $(cat "$CALLS")"
    [ "$(cat "$TEST_DIR/run-env")" = "$(env_get DATABASE_URL)" ] || fail "the tool did not get the address in its environment"
    if grep -qF "$pass" "$CALLS"; then fail "the PostgreSQL password is on a command line"; fi
    if printf '%s' "$out" | grep -qF "$pass"; then fail "the PostgreSQL password was printed"; fi
    stop_line=$(grep -n 'compose stop -t 30 neodesk' "$CALLS" | head -n 1 | cut -d: -f1)
    run_line=$(grep -n 'compose run' "$CALLS" | head -n 1 | cut -d: -f1)
    { [ -n "$stop_line" ] && [ "$stop_line" -lt "$run_line" ]; } || fail "the data was moved while NeoDesk ran"
    grep -q '^run .*tar' "$CALLS" || fail "no backup before the move"
    printf '%s' "$out" | grep -q 'Migration complete: 12 rows' || fail "the tool's result is not shown: $out"
    rm -rf "$TEST_DIR"
)

# L43, decision 26: declining the move keeps the install on SQLite and
# changes nothing.
test_postgres_declined_keeps_sqlite() (
    load
    mgr 'COMMAND=install; do_install' > /dev/null 2>&1 || fail "the install failed"
    : > "$CALLS"
    local out
    out=$(mgr 'MOVE_DATA=no; do_postgres' 2>&1) || fail "declining failed: $out"
    printf '%s' "$out" | grep -q 'stays on SQLite' || fail "declining does not say the install stays on SQLite: $out"
    [ -z "$(env_get DATABASE_URL)" ] || fail "declining set DATABASE_URL"
    if grep -qE '^compose (stop|run|--profile postgres up)' "$CALLS"; then fail "declining touched the containers: $(cat "$CALLS")"; fi
    rm -rf "$TEST_DIR"
)

# L43: a move that fails leaves the install on SQLite and starts it again.
test_postgres_failed_move_keeps_sqlite() (
    load
    mgr 'COMMAND=install; do_install' > /dev/null 2>&1 || fail "the install failed"
    touch "$TEST_DIR/move-fails"
    local out
    if out=$(mgr 'ASSUME_YES=true; do_postgres' 2>&1); then fail "a failed move reported success: $out"; fi
    printf '%s' "$out" | grep -q 'stays on SQLite' || fail "the error does not say the install stays on SQLite: $out"
    [ -z "$(env_get DATABASE_URL)" ] || fail "a failed move left DATABASE_URL set"
    [ -z "$(env_get COMPOSE_PROFILES)" ] || fail "a failed move left the postgres service on"
    [ -f "$TEST_DIR/running" ] || fail "NeoDesk was not started again on SQLite"
    rm -rf "$TEST_DIR"
)

# A first install that stopped before the container ran (here: the image
# was not found) is a fresh install when run again: it starts the
# container and prints the first password.
test_failed_first_install_resumes() (
    load
    printf 'first-pass-0123
' > "$TEST_DIR/first-password"
    touch "$TEST_DIR/pull-fails"
    local out
    if out=$(mgr 'COMMAND=install; do_install' 2>&1); then fail "an install without its image reported success"; fi
    rm -f "$TEST_DIR/pull-fails"
    out=$(mgr 'COMMAND=install; do_install' 2>&1) || fail "the second try failed: $out"
    printf '%s' "$out" | grep -q 'first-pass-0123' || fail "the second try did not print the first password: $out"
    if printf '%s' "$out" | grep -q 'updating it'; then fail "the second try took the half-made install for one to update"; fi
    rm -rf "$TEST_DIR"
)

# Running the script again with the same image and the same settings
# leaves the running container alone; a changed .env makes it again.
test_rerun_without_changes_keeps_the_container() (
    load
    mgr 'COMMAND=install; do_install' > /dev/null 2>&1 || fail "the install failed"
    : > "$CALLS"
    local out
    out=$(mgr 'COMMAND=install; do_install' 2>&1) || fail "the second run failed: $out"
    printf '%s' "$out" | grep -q 'up to date' || fail "the second run does not say NeoDesk is up to date: $out"
    if grep -q 'compose stop' "$CALLS"; then fail "the second run stopped the container: $(cat "$CALLS")"; fi
    env_set TRUST_PROXY 1
    : > "$CALLS"
    out=$(mgr 'COMMAND=install; do_install' 2>&1) || fail "the run after a change of .env failed: $out"
    grep -q 'compose up -d --remove-orphans' "$CALLS" || fail "a changed .env did not reach the container"
    rm -rf "$TEST_DIR"
)

# install.sh's Docker path runs neodesk-docker.sh in the compose folder
# of the install folder with the options it was given (a split install is
# taken over with --adopt), and its uninstall runs it for the same folder.
test_install_sh_runs_the_manager() (
    local dir
    dir=$(mktemp -d)
    # install_sh ARGS...: install.sh's main with ARGS; the download of the
    # manager gives a stand-in that records how it ran
    install_sh() {
        (
            INSTALL_DIR="$dir/opt"
            NEODESK_RAW_BASE="https://raw.githubusercontent.com/example/fork/feature-x"
            # shellcheck disable=SC1091
            source "$SCRIPT_ROOT/install/install.sh"
            require_root() { :; }
            configure_firewall() { :; }
            fetch_url_to_file() {
                printf '#!/bin/sh\n# NEODESK_DOCKER_SOURCED stand-in\nprintf "%%s\\n" "$*" > "%s/manager-args"\nprintf "%%s\\n" "$ADMIN_PASSWORD|$NEODESK_RAW_BASE" > "%s/manager-env"\n' "$dir" "$dir" > "$2"
            }
            main
        ) > "$dir/out" 2>&1
    }
    ADMIN_PASSWORD='Docker-Admin-1' install_sh || fail "install.sh's Docker path failed: $(cat "$dir/out")"
    [ -x "$dir/opt/docker/neodesk-docker.sh" ] || fail "the manager is not in the compose folder"
    [ "$(cat "$dir/manager-args")" = "install --dir $dir/opt/docker --yes --relay-mode auto" ] \
        || fail "the manager ran as: $(cat "$dir/manager-args")"
    [ "$(cat "$dir/manager-env")" = "Docker-Admin-1|https://raw.githubusercontent.com/example/fork/feature-x" ] \
        || fail "the manager did not get the admin password and the branch's files: $(cat "$dir/manager-env")"
    install_sh --relay-servers 203.0.113.9 --version 3.6.0 --skip-docker-install || fail "install.sh with options failed: $(cat "$dir/out")"
    [ "$(cat "$dir/manager-args")" = "install --dir $dir/opt/docker --yes --relay 203.0.113.9 --tag 3.6.0 --no-install-docker" ] \
        || fail "the manager ran with the options as: $(cat "$dir/manager-args")"
    printf 'NEODESK_DOCKER_LAYOUT=split\n' > "$dir/opt/docker/.env"
    install_sh || fail "install.sh on a split install failed: $(cat "$dir/out")"
    case "$(cat "$dir/manager-args")" in *" --adopt"*) ;; *) fail "a split install is not taken over: $(cat "$dir/manager-args")" ;; esac
    install_sh --uninstall --purge || fail "install.sh's uninstall failed: $(cat "$dir/out")"
    [ "$(cat "$dir/manager-args")" = "uninstall --dir $dir/opt/docker --purge --yes" ] \
        || fail "the uninstall ran the manager as: $(cat "$dir/manager-args")"
    rm -rf "$dir"
)

# L225: install.sh's firewall for Docker opens the ports the compose file
# publishes, the console's HTTPS port (5443) among them
test_install_sh_opens_the_published_ports() (
    local dir
    dir=$(mktemp -d)
    (
        # shellcheck disable=SC1091
        source "$SCRIPT_ROOT/install/install.sh"
        ufw() { printf '%s\n' "$*" >> "$dir/ufw"; [ "$1" != status ] || echo "Status: active"; }
        configure_firewall
    ) > /dev/null 2>&1 || fail "install.sh's firewall step failed"
    local port
    for port in 5000 5443 21115 21116 21117 21121 21122; do
        grep -q "^allow ${port}/tcp" "$dir/ufw" || fail "install.sh does not open ${port}/tcp: $(tr '\n' ';' < "$dir/ufw")"
    done
    grep -q '^allow 21116/udp' "$dir/ufw" || fail "install.sh does not open 21116/udp"
    rm -rf "$dir"
)

# L307: an installed ufw that is off gets no rules from install.sh: they
# would go live with the next "ufw enable"
test_install_sh_leaves_an_inactive_ufw_alone() (
    local dir
    dir=$(mktemp -d)
    (
        # shellcheck disable=SC1091
        source "$SCRIPT_ROOT/install/install.sh"
        ufw() { printf '%s\n' "$*" >> "$dir/ufw"; [ "$1" != status ] || echo "Status: inactive"; }
        systemctl() { return 3; }
        configure_firewall
    ) > "$dir/out" 2>&1 || fail "install.sh's firewall step failed: $(cat "$dir/out")"
    if grep -v '^status' "$dir/ufw" | grep -q .; then
        fail "install.sh changed an inactive ufw: $(tr '\n' ';' < "$dir/ufw")"
    fi
    grep -q 'No active UFW/firewalld' "$dir/out" || fail "install.sh does not say it skips the firewall: $(cat "$dir/out")"
    rm -rf "$dir"
)

# --domain with --email: Traefik in front of the console with Let's
# Encrypt. .env gets the profile, the domain, the contact, the console's
# port on this host only and the proxy trust; the summary names the HTTPS
# address and ports 80 and 443. --no-traefik takes all of it back and
# removes the Traefik container.
test_traefik_with_letsencrypt() (
    load
    local out
    out=$(mgr 'getent() { return 2; }
               DOMAIN_SET=console.example.com; LETSENCRYPT_EMAIL_SET=admin@example.com; TRAEFIK_ACTION=on
               COMMAND=install; do_install' 2>&1) || fail "an install with --domain failed: $out"
    local line
    for line in COMPOSE_PROFILES=traefik NEODESK_DOMAIN=console.example.com LETSENCRYPT_EMAIL=admin@example.com \
        NEODESK_CONSOLE_BIND=127.0.0.1 TRUST_PROXY=1 PANEL_PUBLIC_URL=https://console.example.com; do
        grep -qx "$line" "$NEODESK_DIR/.env" || fail ".env has no $line: $(cat "$NEODESK_DIR/.env")"
    done
    if grep -q '^LETSENCRYPT_CA_SERVER=' "$NEODESK_DIR/.env"; then fail "a production install uses another CA server"; fi
    printf '%s' "$out" | grep -q 'Web console: *https://console.example.com' || fail "no HTTPS console address in: $out"
    printf '%s' "$out" | grep -q 'TCP 80, 443, 21115' || fail "the firewall line does not name 80 and 443: $out"
    printf '%s' "$out" | grep -q 'has its certificate' || fail "the certificate was not checked: $out"
    printf '%s' "$out" | grep -q 'does not resolve' || fail "a name that does not resolve gives no warning: $out"

    out=$(mgr 'DOMAIN_SET=console.example.com; LETSENCRYPT_STAGING=true; TRAEFIK_ACTION=on; getent() { return 2; }
               COMMAND=update; do_install' 2>&1) || fail "the switch to the staging server failed: $out"
    grep -qx 'LETSENCRYPT_CA_SERVER=https://acme-staging-v02.api.letsencrypt.org/directory' "$NEODESK_DIR/.env" \
        || fail ".env has not the staging server: $(cat "$NEODESK_DIR/.env")"
    grep -qx 'LETSENCRYPT_EMAIL=admin@example.com' "$NEODESK_DIR/.env" || fail "the contact of the first run was lost"

    : > "$CALLS"
    out=$(mgr 'TRAEFIK_ACTION=off; COMMAND=update; do_install' 2>&1) || fail "--no-traefik failed: $out"
    grep -q 'rm -sf traefik' "$CALLS" || fail "--no-traefik left the Traefik container: $(cat "$CALLS")"
    for line in COMPOSE_PROFILES NEODESK_DOMAIN NEODESK_CONSOLE_BIND TRUST_PROXY PANEL_PUBLIC_URL LETSENCRYPT_CA_SERVER; do
        if grep -q "^$line=" "$NEODESK_DIR/.env"; then fail "--no-traefik left $line: $(cat "$NEODESK_DIR/.env")"; fi
    done
    printf '%s' "$out" | grep -q 'Web console: *http://203.0.113.10:5000' || fail "no plain console address after --no-traefik: $out"
    rm -rf "$TEST_DIR"
)

# --domain needs a DNS name and --email; ports 80 and 443 must be free
# for Traefik, and the error says what to do.
test_traefik_errors_say_what_to_do() (
    load
    local out
    if out=$(mgr 'parse_args --domain console.example.com' 2>&1); then fail "--domain without --email went on"; fi
    printf '%s' "$out" | grep -q -- '--email' || fail "the error does not name --email: $out"
    if out=$(mgr 'parse_args --domain 203.0.113.10 --email admin@example.com' 2>&1); then fail "an IP address passed as --domain"; fi
    printf '%s' "$out" | grep -q 'DNS name' || fail "the --domain error does not say what it needs: $out"
    if out=$(mgr 'parse_args --domain console.example.com --email nobody' 2>&1); then fail "a bad e-mail address passed"; fi
    if out=$(mgr 'parse_args --domain console.example.com --email a@example.com --no-traefik' 2>&1); then
        fail "--domain with --no-traefik went on"
    fi
    out=$(mgr 'parse_args --domain Console.Example.com --email a@example.com; printf "%s %s\n" "$DOMAIN_SET" "$TRAEFIK_ACTION"' 2>&1) \
        || fail "a valid --domain was refused: $out"
    [ "$out" = "console.example.com on" ] || fail "--domain was read as: $out"

    printf 'apache2\n' > "$TEST_DIR/taken-80"
    if out=$(mgr 'DOMAIN_SET=console.example.com; LETSENCRYPT_EMAIL_SET=a@example.com; TRAEFIK_ACTION=on
                  COMMAND=install; do_install' 2>&1); then fail "an install with port 80 taken went on"; fi
    printf '%s' "$out" | grep -q 'TCP 80: apache2' || fail "the port error does not name the program: $out"
    printf '%s' "$out" | grep -q 'Traefik needs ports 80 and 443' || fail "the port error does not say why: $out"
    rm -f "$TEST_DIR/taken-80"

    mgr 'COMMAND=install; do_install' > /dev/null 2>&1 || fail "the plain install failed"
    printf 'nginx\n' > "$TEST_DIR/taken-443"
    if out=$(mgr 'DOMAIN_SET=console.example.com; LETSENCRYPT_EMAIL_SET=a@example.com; TRAEFIK_ACTION=on
                  COMMAND=update; do_install' 2>&1); then fail "turning Traefik on with port 443 taken went on"; fi
    printf '%s' "$out" | grep -q 'TCP 443: nginx' || fail "the update's port error does not name the program: $out"
    if grep -q '^NEODESK_DOMAIN=' "$NEODESK_DIR/.env"; then fail "the refused switch changed .env"; fi
    rm -rf "$TEST_DIR"
)

# install.sh passes --domain, --letsencrypt-email and
# --letsencrypt-staging to the manager, and its firewall opens 80 and 443
# instead of the console's own ports then.
test_install_sh_passes_the_domain() (
    local dir
    dir=$(mktemp -d)
    (
        INSTALL_DIR="$dir/opt"
        # shellcheck disable=SC1091
        source "$SCRIPT_ROOT/install/install.sh" --domain console.example.com --letsencrypt-email admin@example.com --letsencrypt-staging
        require_root() { :; }
        configure_firewall() { printf '%s\n' "$DOCKER_PORTS" > "$dir/ports"; }
        fetch_url_to_file() {
            printf '#!/bin/sh\n# NEODESK_DOCKER_SOURCED stand-in\nprintf "%%s\\n" "$*" > "%s/manager-args"\n' "$dir" > "$2"
        }
        main
    ) > "$dir/out" 2>&1 || fail "install.sh with --domain failed: $(cat "$dir/out")"
    [ "$(cat "$dir/manager-args")" = "install --dir $dir/opt/docker --yes --relay-mode auto --domain console.example.com --email admin@example.com --letsencrypt-staging" ] \
        || fail "the manager ran with the domain as: $(cat "$dir/manager-args")"
    local port
    for port in 80 443; do
        case " $(cat "$dir/ports") " in
            *" $port "*) ;;
            *) fail "install.sh's firewall does not open $port: $(cat "$dir/ports")" ;;
        esac
    done
    case " $(cat "$dir/ports") " in
        *" 5000 "*|*" 5443 "*) fail "install.sh's firewall opens the console's own ports behind Traefik: $(cat "$dir/ports")" ;;
    esac
    rm -rf "$dir"
)

test_fresh_install
test_no_release_image_runs_dev
test_update_goes_back_when_unhealthy
test_update_backs_up_the_files_from_before
test_data_volume_at_the_neodesk_path
test_update_from_the_old_data_path
test_old_backup_restores
test_update_keeps_compose_edits
test_fresh_install_on_postgres
test_foreign_install_is_left_alone
test_errors_say_what_to_do
test_uninstall_says_what_it_leaves
test_purge_dumps_postgres_first
test_help_lists_every_option
test_postgres_switch_moves_the_data
test_postgres_declined_keeps_sqlite
test_postgres_failed_move_keeps_sqlite
test_failed_first_install_resumes
test_rerun_without_changes_keeps_the_container
test_install_sh_runs_the_manager
test_install_sh_opens_the_published_ports
test_install_sh_leaves_an_inactive_ufw_alone
test_traefik_with_letsencrypt
test_traefik_errors_say_what_to_do
test_install_sh_passes_the_domain
echo "Docker installer checks passed"
bash "$SCRIPT_ROOT/scripts/test-docker-entrypoint.sh"
