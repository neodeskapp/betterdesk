#!/bin/bash
set -euo pipefail
echo '=== NeoDesk Go Server Setup ==='

# Directories
mkdir -p /opt/neodesk-go/data /opt/neodesk-go/backups /var/log/neodesk-go

# Backup
TS=$(date +%Y%m%d_%H%M%S)
echo "[1/8] Backup (timestamp: ${TS})..."
cp /opt/rustdesk/db_v2.sqlite3 "/opt/neodesk-go/backups/db_v2.sqlite3.${TS}"
[ -f /opt/rustdesk/db_v2.sqlite3-wal ] && cp /opt/rustdesk/db_v2.sqlite3-wal "/opt/neodesk-go/backups/db_v2.sqlite3-wal.${TS}" || true
[ -f /opt/rustdesk/db_v2.sqlite3-shm ] && cp /opt/rustdesk/db_v2.sqlite3-shm "/opt/neodesk-go/backups/db_v2.sqlite3-shm.${TS}" || true
cp /opt/rustdesk/id_ed25519 "/opt/neodesk-go/backups/id_ed25519.${TS}"
cp /opt/rustdesk/id_ed25519.pub "/opt/neodesk-go/backups/id_ed25519.pub.${TS}"
echo '    Backup done.'

# Install binary
echo '[2/8] Installing binary...'
cp /tmp/neodesk-server-linux-amd64 /opt/neodesk-go/neodesk-server
chmod +x /opt/neodesk-go/neodesk-server
cp /tmp/migrate-linux-amd64 /opt/neodesk-go/migrate
chmod +x /opt/neodesk-go/migrate

# Copy keys
echo '[3/8] Copying Ed25519 keys...'
cp /opt/rustdesk/id_ed25519 /opt/neodesk-go/data/
cp /opt/rustdesk/id_ed25519.pub /opt/neodesk-go/data/

# Migrate database
echo '[4/8] Migrating database...'
/opt/neodesk-go/migrate -src /opt/rustdesk/db_v2.sqlite3 -dst /opt/neodesk-go/data/db_v2.sqlite3

# Stop old services
echo '[5/8] Stopping old hbbs/hbbr...'
systemctl stop rustdesksignal 2>/dev/null || true
systemctl stop rustdeskrelay 2>/dev/null || true
systemctl disable rustdesksignal 2>/dev/null || true
systemctl disable rustdeskrelay 2>/dev/null || true

# Create systemd service
echo '[6/8] Creating systemd service...'
cat > /etc/systemd/system/neodesk-go.service << 'SVC'
[Unit]
Description=NeoDesk Go Server (Signal + Relay + API)
After=network.target
Wants=network-online.target

[Service]
Type=simple
User=root
WorkingDirectory=/opt/neodesk-go/data
ExecStart=/opt/neodesk-go/neodesk-server -mode all -db /opt/neodesk-go/data/db_v2.sqlite3 -key-file /opt/neodesk-go/data/id_ed25519 -port 21116 -relay-port 21117 -api-port 21114 -admin-port 21000 -log-format text -log-level info
Restart=always
RestartSec=5
LimitNOFILE=65535
# The console in the binary is on by default on port 5000; it stays off
# where the Node console (or nothing) serves the panel
Environment=CONSOLE_PORT=0

[Install]
WantedBy=multi-user.target
SVC
systemctl daemon-reload

# Start
echo '[7/8] Starting NeoDesk Go...'
systemctl enable neodesk-go
systemctl start neodesk-go
sleep 3

# Verify
echo '[8/8] Verifying...'
if systemctl is-active --quiet neodesk-go; then
    echo 'SUCCESS: NeoDesk Go is running!'
else
    echo 'FAILED: Service did not start.'
    journalctl -u neodesk-go -n 30 --no-pager
    exit 1
fi
ss -tlnp | grep -E '2111[4-9]|21000' || true
ss -ulnp | grep 21116 || true
echo ''
echo '============================================'
echo '  ROLLBACK command:'
echo '  sudo systemctl stop neodesk-go && sudo systemctl enable --now rustdesksignal rustdeskrelay'
echo '============================================'
