# check=skip=SecretsUsedInArgOrEnv
# BetterDesk — one image, one process
# ====================================
# betterdesk-server serves the signal and relay server, the API and the web
# console (DECISIONS A32). The web app is built into the binary.
#
# Build:  docker build -t betterdesk:local .
# Run:    docker compose -f docker-compose.single.yml up -d
#
# Ports:
#   5000  - Web console (5443 with HTTPS_ENABLED)
#   21121 - HTTP API (RustDesk client API + REST)
#   21115 - NAT type test
#   21116 - Signal TCP/UDP
#   21117 - Relay TCP
#   21118 - WebSocket Signal
#   21119 - WebSocket Relay

# ============= Web app build (only the build machine needs Node.js) =============
FROM node:22.23.2-alpine3.24 AS web-ui-builder

WORKDIR /web-ui

# npm 10 (bundled with Node 22) fails to resolve this dependency tree (web-ui-ci.yml)
COPY web-ui/package.json web-ui/package-lock.json ./
RUN npx -y npm@11.6.2 ci --no-audit --no-fund

COPY web-ui/ .
RUN npm run build

# ============= Go server with the web app built in =============
FROM golang:1.26.11-alpine AS go-builder
ARG TARGETARCH

# Retry apk in case of transient DNS failures (common on AlmaLinux/CentOS Docker)
RUN apk add --no-cache git || { sleep 2 && apk add --no-cache git; }

WORKDIR /src
COPY betterdesk-server/go.mod betterdesk-server/go.sum ./
RUN go mod download

COPY betterdesk-server/ .

# The embed step copies the web app build into the module before go build
# (DECISIONS A25), as the release binaries are built
COPY --from=web-ui-builder /web-ui/build /web-ui-build
RUN go run ./cmd/embed-webui -from /web-ui-build

ARG BETTERDESK_PRODUCT_VERSION=dev
ARG BETTERDESK_COMMIT_SHA=

RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} go build -trimpath \
    -ldflags="-s -w -X main.Version=${BETTERDESK_PRODUCT_VERSION} -X main.Commit=${BETTERDESK_COMMIT_SHA}" \
    -tags "netgo osusergo" \
    -o /betterdesk-server .

# ============= The console's language files, themes and images =============
# Copied into /app when the checkout has them (web-nodejs); the server reads
# them there unless its binary carries them.
FROM alpine:3.22 AS console-static
RUN --mount=type=bind,target=/ctx \
    mkdir -p /static && \
    if [ -f /ctx/web-nodejs/lang/en.json ]; then \
        for d in lang public themes protos wallpapers config; do \
            if [ -d "/ctx/web-nodejs/$d" ]; then cp -a "/ctx/web-nodejs/$d" /static/; fi; \
        done; \
    fi

# ============= Runtime =============
FROM alpine:3.22

LABEL maintainer="UNITRONIX"
LABEL description="BetterDesk — server and web console in one process"
LABEL version="3.5.264"

# Runtime packages (retry for transient DNS failures). sqlite stays for the
# admin credential bootstrap, which counts users with the sqlite3 CLI.
RUN apk add --no-cache ca-certificates curl sqlite tini su-exec shadow openssl \
    || { sleep 2 && apk add --no-cache ca-certificates curl sqlite tini su-exec shadow openssl; }

# Create betterdesk user and directories
RUN addgroup -g 10001 -S betterdesk && \
    adduser -u 10001 -S -G betterdesk betterdesk && \
    mkdir -p /opt/rustdesk /app/data && \
    chown -R betterdesk:betterdesk /opt/rustdesk /app/data

COPY --from=go-builder /betterdesk-server /usr/local/bin/betterdesk-server
COPY --from=console-static /static/ /app/

ARG BETTERDESK_IMAGE_VERSION=unknown
ENV BETTERDESK_IMAGE_VERSION=${BETTERDESK_IMAGE_VERSION}

COPY docker/ensure-app-user.sh /ensure-app-user.sh
COPY docker/entrypoint.sh /entrypoint.sh
COPY docker/bootstrap-admin-credentials.sh /docker/bootstrap-admin-credentials.sh
COPY docker/show-admin-credentials.sh /usr/local/bin/betterdesk-show-admin-credentials
RUN chmod +x /ensure-app-user.sh /entrypoint.sh /docker/bootstrap-admin-credentials.sh /usr/local/bin/betterdesk-show-admin-credentials

# The console on 5000 (DECISIONS A27), the API on 21121. The console's own
# client API listener stays off: the API serves RustDesk clients on 21121.
# BETTERDESK_SUPERVISOR: Docker's restart policy starts the process again
# after it ends with status 3 (a restart, an update or a restore from the
# panel, DECISIONS A30).
ENV NODE_ENV=production \
    CONSOLE_PORT=5000 \
    SIGNAL_PORT=21116 \
    SIGNAL_RATE_LIMIT_PER_IP=20 \
    HOST=0.0.0.0 \
    API_ENABLED=false \
    DOCKER=true \
    CONSOLE_DIR=/app \
    DATA_DIR=/app/data \
    RUSTDESK_PATH=/opt/rustdesk \
    KEYS_PATH=/opt/rustdesk \
    DB_PATH=/opt/rustdesk/db_v2.sqlite3 \
    PUB_KEY_PATH=/opt/rustdesk/id_ed25519.pub \
    API_KEY_PATH=/opt/rustdesk/.api_key \
    HBBS_API_URL=http://127.0.0.1:21121/api \
    BETTERDESK_API_URL=http://127.0.0.1:21121/api \
    SERVER_BACKEND=betterdesk \
    ENCRYPTED_ONLY=1 \
    RELAY_SERVERS= \
    NTP_SERVERS=pool.ntp.org,time.google.com,time.cloudflare.com \
    BILLING_MAX_CLOCK_SKEW_MS=2000 \
    BILLING_REQUIRE_SYNCED_CLOCK=1 \
    BILLING_TRUST_OS_NTP=Y \
    BETTERDESK_UPDATE_MODE=image \
    BETTERDESK_SUPERVISOR=docker

EXPOSE 5000 21115 21116/tcp 21116/udp 21117 21118 21119 21121

# The server writes files of its own (mesh_agent_server.pem) in its folder
WORKDIR /opt/rustdesk

# The API and the web console of the one process
HEALTHCHECK --interval=30s --timeout=10s --start-period=30s --retries=3 \
    CMD curl -sf http://localhost:21121/api/health && curl -sf http://localhost:5000/health || exit 1

ENTRYPOINT ["/sbin/tini", "--", "/entrypoint.sh"]
CMD ["/usr/local/bin/betterdesk-server", "-mode", "all", "-api-port", "21121", "-key-file", "/opt/rustdesk/id_ed25519"]
