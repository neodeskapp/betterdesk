#!/usr/bin/env bash
# The main process of a CI test instance (see up.sh): one BetterDesk
# install on fresh data, the Go server with its console on port 5000,
# seeded with the demo data and one e2e account per role. It is set up as
# the local test stack sets up its instances, because the contract suite's
# goldens hold their paths (/opt/rustdesk, /app/data, the checkout at /repo
# with the console's folder /repo/web-nodejs beside it) and their settings.
#
# The server and devseed come prebuilt in /usr/local/bin. Inside the
# instance: console on 5000 with GET /__bd/routing (CONSOLE_DEBUG_ROUTING),
# Go API on 21121, secrets in /stack/secrets (admin_password,
# e2e_password, the device tokens), logs in /stack/logs. /stack/ready
# exists once the instance is seeded and the console knows which demo
# devices are online.
set -euo pipefail

SECRETS=/stack/secrets
LOGS=/stack/logs
mkdir -p /opt/rustdesk /app/data "$SECRETS" "$LOGS"
chmod 700 "$SECRETS"

apt-get update -qq >"$LOGS/apt.log" 2>&1
apt-get install -y -qq --no-install-recommends sqlite3 curl ca-certificates >>"$LOGS/apt.log" 2>&1

export RUSTDESK_PATH=/opt/rustdesk DATA_DIR=/app/data
export DB_PATH=/opt/rustdesk/db_v2.sqlite3 PUB_KEY_PATH=/opt/rustdesk/id_ed25519.pub API_KEY_PATH=/opt/rustdesk/.api_key
export NODE_ENV=production DOCKER=true SERVER_BACKEND=betterdesk API_ENABLED=false
export HBBS_API_URL=http://127.0.0.1:21121/api BETTERDESK_API_URL=http://127.0.0.1:21121/api
export ENCRYPTED_ONLY=1 SIGNAL_PORT=21116 SIGNAL_RATE_LIMIT_PER_IP=0 HOST=0.0.0.0 API_HOST=0.0.0.0
export NTP_SERVERS=pool.ntp.org BILLING_MAX_CLOCK_SKEW_MS=2000 BILLING_REQUIRE_SYNCED_CLOCK=0 BILLING_TRUST_OS_NTP=Y
export AGENT_BUILD_WORKER=off
# Test suites send everything from one address: no rate limit may stop them
export RATE_LIMIT_MAX=1000000 PANEL_READ_RATE_LIMIT_MAX=1000000 WIDGET_RATE_LIMIT_MAX=1000000
export LOGIN_RATE_LIMIT_MAX=1000000 UPLOAD_RATE_LIMIT_MAX=1000000 FILE_ACCESS_RATE_LIMIT_MAX=1000000
export PANEL_PREFERENCE_RATE_LIMIT_MAX=1000000 RDCLIENT_PAGE_RATE_LIMIT_MAX=1000000 BD_REGISTER_RATE_LIMIT_MAX=1000000
export SESSION_MAX_PER_USER=1

rand() { head -c 24 /dev/urandom | base64 | tr -d '/+=' | head -c 28; }
[ -f "$SECRETS/admin_password" ] || printf 'Adm1n-%s!x' "$(rand)" > "$SECRETS/admin_password"
[ -f "$SECRETS/e2e_password" ] || printf 'E2e-%s!x7' "$(rand)" > "$SECRETS/e2e_password"
[ -f "$SECRETS/session_secret" ] || printf '%s%s' "$(rand)" "$(rand)" > "$SECRETS/session_secret"
head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n' > /opt/rustdesk/.api_key
chmod 600 /opt/rustdesk/.api_key
ADMIN_PW="$(cat "$SECRETS/admin_password")"
SESSION_SECRET="$(cat "$SECRETS/session_secret")"
export SESSION_SECRET
export DEFAULT_ADMIN_USERNAME=admin DEFAULT_ADMIN_PASSWORD="$ADMIN_PW" INIT_ADMIN_USER=admin INIT_ADMIN_PASS="$ADMIN_PW" ADMIN_PASSWORD="$ADMIN_PW"
export DB_TYPE=sqlite DB_URL="$DB_PATH" SQLITE_AUTH_DB_MODE=
export CONSOLE_DIR=/repo/web-nodejs CONSOLE_DEBUG_ROUTING=1

pids=()
stop() { kill "${pids[@]}" 2>/dev/null || true; wait 2>/dev/null || true; exit 0; }
trap stop TERM INT

(cd /opt/rustdesk && CONSOLE_PORT=5000 exec /usr/local/bin/betterdesk-server -mode all -api-port 21121 -key-file /opt/rustdesk/id_ed25519) >"$LOGS/go.log" 2>&1 &
pids+=($!)

wait_http() {
    local n=0
    until curl -sf -o /dev/null "$1"; do
        n=$((n + 1))
        if [ "$n" -gt 120 ]; then echo "[instance] $1 did not come up"; tail -n 40 "$LOGS/go.log"; exit 1; fi
        sleep 1
    done
}
wait_http http://127.0.0.1:21121/api/health
wait_http http://127.0.0.1:5000/health
echo "[instance] the server is up"

# devseed writes only into a database with this marker, which no real
# install has
sqlite3 -cmd ".timeout 5000" "$DB_PATH" \
    "INSERT INTO server_config (key, value) VALUES ('devseed_test_stack', '1') ON CONFLICT (key) DO UPDATE SET value = excluded.value" \
    >"$LOGS/seed.log" 2>&1 || { echo "[instance] writing the devseed marker failed"; tail -n 30 "$LOGS/seed.log"; exit 1; }
# The admin's password comes from ADMIN_PASSWORD, never from the command line
devseed demo --panel http://127.0.0.1:5000 >>"$LOGS/seed.log" 2>&1 \
    || { echo "[instance] seed failed"; tail -n 30 "$LOGS/seed.log"; exit 1; }
# The seed saves four reports one after the other, and their list is
# ordered by the second each was saved in. One time for all four gives
# every instance the same list.
sqlite3 -cmd ".timeout 5000" "$DB_PATH" \
    "UPDATE saved_reports SET created_at = (SELECT MIN(created_at) FROM saved_reports)" >>"$LOGS/seed.log" 2>&1 \
    || { echo "[instance] giving the saved reports one time failed"; tail -n 30 "$LOGS/seed.log"; exit 1; }
# The role accounts, and the device tokens of 900100001 and 900100002 in
# $SECRETS/device_token and $SECRETS/device_token_other
devseed accounts "$SECRETS/e2e_password" >>"$LOGS/seed.log" 2>&1 \
    || { echo "[instance] creating the role accounts failed"; tail -n 30 "$LOGS/seed.log"; exit 1; }

if [ -f /app/data/dev-online-ids.json ]; then
    devseed heartbeats >"$LOGS/heartbeats.log" 2>&1 &
    pids+=($!)
    # The console learns who is online from a status sync every 15
    # seconds. On a fast machine the suites are through their first
    # requests before the first sync after the heartbeats, so wait for two.
    sleep 40
fi

touch /stack/ready
echo "[instance] ready"
wait -n "${pids[@]}" || true
echo "[instance] a process ended"
tail -n 40 "$LOGS"/*.log
exit 1
