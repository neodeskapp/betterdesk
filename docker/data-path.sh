#!/bin/sh
# The data volume of the image: the server's keys, its SQLite database and
# the files below, at /opt/neodesk/data. A compose file of 3.6.0-rc.4 or
# older mounts the volume at /opt/rustdesk, which the image does not have.
# The start then keeps the data where the volume is: a key or database made
# anew at /opt/neodesk/data would be gone with the container, and devices
# would no longer trust the server. neodesk-docker.sh update, or the compose
# file of this release, mounts the volume at /opt/neodesk/data.
#
# Loaded by the entrypoints, as root, before anything reads the volume:
#   . /docker/data-path.sh
#   choose_data_path
#   for arg do shift; set -- "$@" "$(on_data_path "$arg")"; done
NEW_DATA_PATH=/opt/neodesk/data
OLD_DATA_PATH=/opt/rustdesk

# data_file_there FILE: FILE exists, also where only neodesk may look
# (cap_drop: ALL takes root's right to read other users' folders)
data_file_there() {
    [ -e "$1" ] || su-exec neodesk test -e "$1" 2>/dev/null
}

# choose_data_path: DATA_PATH is NEW_DATA_PATH, or OLD_DATA_PATH when the
# volume is mounted there and NEW_DATA_PATH holds no data. Then the paths
# into NEW_DATA_PATH that the settings name lead there too.
choose_data_path() {
    DATA_PATH=$NEW_DATA_PATH
    { [ -d "$OLD_DATA_PATH" ] && [ ! -L "$OLD_DATA_PATH" ]; } || return 0
    if data_file_there "$NEW_DATA_PATH/db_v2.sqlite3" || data_file_there "$NEW_DATA_PATH/id_ed25519"; then
        return 0
    fi
    DATA_PATH=$OLD_DATA_PATH
    for name in RUSTDESK_PATH KEYS_PATH DB_PATH PUB_KEY_PATH API_KEY_PATH; do
        eval "value=\${$name:-}"
        # shellcheck disable=SC2154
        [ -z "$value" ] || export "$name=$(on_data_path "$value")"
    done
    echo "WARNING: The data volume is mounted at $OLD_DATA_PATH, where images up to 3.6.0-rc.4 kept it;"
    echo "         NeoDesk keeps its data there. Mount it at $NEW_DATA_PATH instead:"
    echo "         sudo bash neodesk-docker.sh update, or in the compose file change $OLD_DATA_PATH to $NEW_DATA_PATH."
}

# on_data_path ARG: ARG, with a path in NEW_DATA_PATH moved to DATA_PATH
on_data_path() {
    case "$1" in
        "$NEW_DATA_PATH"|"$NEW_DATA_PATH"/*) printf '%s\n' "${DATA_PATH:-$NEW_DATA_PATH}${1#"$NEW_DATA_PATH"}" ;;
        *) printf '%s\n' "$1" ;;
    esac
}
