#!/bin/sh
# Remap the neodesk user/group to PUID/PGID at container start.
# Sourced from entrypoints while running as root (before chown / su-exec).
# Defaults match the image build identity (10001:10001).

ensure_neodesk_user() {
    if [ "$(id -u)" != "0" ]; then
        return 0
    fi

    PUID="${PUID:-10001}"
    PGID="${PGID:-10001}"

    case "$PUID" in
        ''|*[!0-9]*)
            echo "ERROR: PUID must be a positive integer (got: ${PUID})" >&2
            exit 1
            ;;
    esac
    case "$PGID" in
        ''|*[!0-9]*)
            echo "ERROR: PGID must be a positive integer (got: ${PGID})" >&2
            exit 1
            ;;
    esac

    if [ "$PUID" = "0" ] || [ "$PGID" = "0" ]; then
        echo "ERROR: PUID/PGID must not be 0 (root)" >&2
        exit 1
    fi

    if ! id neodesk >/dev/null 2>&1; then
        echo "ERROR: neodesk user not found in image" >&2
        exit 1
    fi

    current_uid=$(id -u neodesk)
    current_gid=$(id -g neodesk)

    if [ "$current_uid" = "$PUID" ] && [ "$current_gid" = "$PGID" ]; then
        echo "App user: neodesk (uid=${PUID} gid=${PGID})"
        return 0
    fi

    if ! command -v groupmod >/dev/null 2>&1 || ! command -v usermod >/dev/null 2>&1; then
        echo "ERROR: usermod/groupmod not found (shadow package required for PUID/PGID)" >&2
        exit 1
    fi

    if [ "$current_gid" != "$PGID" ]; then
        existing_group=$(getent group "$PGID" 2>/dev/null | cut -d: -f1)
        if [ -n "$existing_group" ] && [ "$existing_group" != "neodesk" ]; then
            echo "ERROR: PGID ${PGID} already used by group '${existing_group}'" >&2
            exit 1
        fi
        groupmod -g "$PGID" neodesk || {
            echo "ERROR: failed to set neodesk GID to ${PGID}" >&2
            exit 1
        }
    fi

    existing_user=$(getent passwd "$PUID" 2>/dev/null | cut -d: -f1)
    if [ -n "$existing_user" ] && [ "$existing_user" != "neodesk" ]; then
        echo "ERROR: PUID ${PUID} already used by user '${existing_user}'" >&2
        exit 1
    fi

    usermod -u "$PUID" -g "$PGID" neodesk || {
        echo "ERROR: failed to set neodesk UID/GID to ${PUID}:${PGID}" >&2
        exit 1
    }

    echo "App user: neodesk remapped to uid=${PUID} gid=${PGID}"
}
