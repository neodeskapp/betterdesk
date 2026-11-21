# BetterDesk — Encryption Specification

> **Version:** 1.0
> **Last Updated:** 2026-04-01

## Overview

BetterDesk uses layered encryption to protect data in transit and at rest across all communication channels.

## Algorithms

| Purpose | Algorithm | Key Size | Notes |
|---------|-----------|----------|-------|
| Server identity | Ed25519 | 256-bit | Sign peer registrations, verify server identity |
| Key exchange (P2P) | X25519 (Curve25519 DH) | 256-bit | Derive shared secret between peers |
| Symmetric encryption (P2P) | XSalsa20-Poly1305 (NaCl box) | 256-bit key, 192-bit nonce | Authenticated encryption for peer-to-peer |
| TLS transport | TLS 1.2+ (auto-negotiated) | Depends on cipher suite | Signal, relay, API server connections |
| Password hashing (console accounts) | PBKDF2-HMAC-SHA256 | 600,000 iterations, 16-byte salt | Web console accounts; bcrypt hashes from older versions still verify |
| Password hashing (org users) | bcrypt | Cost factor 10 (Go default) | Organization user accounts |
| JWT signing | HMAC-SHA256 | 256-bit | API authentication tokens |
| TOTP 2FA | HMAC-SHA1 (RFC 6238) | 160-bit shared secret | 6-digit codes, 30-second step |
| Session secret | Random bytes | 256-bit | Signs the console session cookie (HMAC-SHA256) and CSRF token |
| API key | Cryptographically random hex | 256-bit (32 bytes) | Go API authentication (`X-API-Key`); the built-in console acts as this key |
| CSRF token | Double-submit cookie | 512-bit (64 random bytes) | Cross-site request forgery prevention |

## Connection Encryption Matrix

| Connection | Transport | Application Layer | Forward Secrecy |
|------------|-----------|-------------------|-----------------|
| Client ↔ Signal Server (TCP) | Optional TLS (`--tls-signal`) | NaCl secure channel (key exchange) | Yes (ephemeral DH) |
| Client ↔ Signal Server (UDP) | None (UDP) | Protobuf (unencrypted metadata) | No |
| Client ↔ Relay Server (TCP) | Optional TLS (`--tls-relay`) | Transparent relay (E2E between peers) | Via P2P layer |
| Client ↔ Client (P2P) | Direct TCP | NaCl box (X25519 + XSalsa20-Poly1305) | Yes (per-session DH) |
| Browser ↔ Web Console | HTTPS (recommended) | Session cookie + CSRF token | Via TLS |
| Console ↔ Go Server API | In process (no network) | Calls act as the API key | Not applicable |
| CDAP Agent ↔ Gateway | WebSocket (WSS recommended) | API key authentication | Via TLS |

## Key Management

### Ed25519 Server Keys
- Generated on first server startup
- Stored in `id_ed25519` / `id_ed25519.pub` files
- File permissions: `0600` (owner read/write only)
- No automatic rotation (server identity key)
- Clients verify server public key against stored value

### API Keys
- Auto-generated on first run (32 bytes, hex-encoded)
- Stored in `.api_key` file + synced to `server_config` database table
- Transmitted via `X-API-Key` HTTP header (never in URL)
- Manual rotation via admin panel or file replacement

### Session Secrets
- Taken from `SESSION_SECRET` when it is set
- Otherwise read from `.session_secret` in the console data folder, or generated on first start (32 random bytes as 64 hex characters) and saved there with mode `0600`
- Signs the session cookie (HMAC-SHA256) and the CSRF token

### TOTP Secrets
- Per-user, generated during 2FA enrollment
- Stored in the database as-is (`totp_secret` column)
- Recovery codes: 8 single-use codes generated at enrollment, stored as bcrypt hashes

## TLS Configuration

### Server-side (Go)
- Dual-mode listeners: auto-detect plain TCP vs TLS on same port (first-byte `0x16` detection)
- Minimum TLS version: 1.2
- Flags: `--tls-cert`, `--tls-key`, `--tls-signal`, `--tls-relay`, `--tls-api`
- Self-signed certificates supported (client must trust CA)

### Client-side (Tauri/Rust)
- Trusts system certificate store by default
- Certificate pinning planned for Phase 8
- WebSocket connections use `wss://` when server HTTPS detected

## Data at Rest

| Data | Encryption | Location |
|------|-----------|----------|
| Database (SQLite) | None (filesystem permissions) | `db_v2.sqlite3` |
| Database (PostgreSQL) | TLS in transit, at-rest depends on PG config | PostgreSQL server |
| Ed25519 private key | None (file permissions `0600`) | `id_ed25519` |
| Console password hash | PBKDF2-SHA256, or bcrypt from older versions (irreversible) | `auth.db` |
| TOTP secrets | Stored as-is in DB | `auth.db` / `users` table |
| Session data | Server-side storage (not in cookie) | `panel_sessions` table |
