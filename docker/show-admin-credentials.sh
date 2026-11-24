#!/bin/sh
# Print bootstrap admin credentials from the shared credentials file.
# Docker quick-start: .admin_credentials is mode 0600 and owned by neodesk
# (UID from PUID, default 10001). docker compose exec runs as root, but
# cap_drop:ALL removes CAP_DAC_OVERRIDE, so root cannot read the file.
# Re-exec as neodesk when invoked as root.
set -e

if [ "$(id -u)" = "0" ]; then
    if command -v su-exec >/dev/null 2>&1; then
        exec su-exec neodesk "$0" "$@"
    fi
    # All-in-one image historically lacked su-exec; busybox su works with SETUID.
    exec su -s /bin/sh neodesk -c 'exec "$0" "$@"' -- "$0" "$@"
fi

for creds_file in /opt/neodesk/data/.admin_credentials /app/data/.admin_credentials; do
    if [ -f "$creds_file" ] && [ -r "$creds_file" ]; then
        cat "$creds_file"
        exit 0
    fi
done

# The console's password change and the reset-password command remove the
# first password and leave this note without the password.
for note in /opt/neodesk/data/.admin_credentials.removed /app/data/.admin_credentials.removed; do
    if [ -f "$note" ] && [ -r "$note" ]; then
        cat "$note" >&2
        echo "To set a new password for admin, on the Docker host:" >&2
        echo "  sudo bash neodesk-docker.sh reset-password      (prints a new random password)" >&2
        echo "  or pipe one in: printf '%s\\n' 'the new password' | docker compose exec -T -u neodesk neodesk neodesk-server reset-password admin" >&2
        exit 1
    fi
done

echo "No readable admin credentials file found." >&2
echo "  Checked: /opt/neodesk/data/.admin_credentials, /app/data/.admin_credentials" >&2
echo "  Wait for the first start to finish, then retry. Its log: docker compose logs neodesk" >&2
exit 1
