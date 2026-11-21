# BetterDesk — Threat Model

> **Methodology:** STRIDE (Spoofing, Tampering, Repudiation, Information Disclosure, Denial of Service, Elevation of Privilege)
> **Last Updated:** 2026-04-01
> **Scope:** BetterDesk Go Server (signal, relay, API and web console), Desktop Client, CDAP Agent

---

## System Overview

```
                    Internet / WAN
                         |
        +----------------+----------------+
        |                |                |
   RustDesk Client   Web Browser    CDAP Agent
        |                |                |
   [Signal TCP/UDP]  [HTTPS :5000]  [WSS :21122]
   [Relay TCP/WS]    [API :21121]       |
        |                |                |
   +----+----+     +-----+-----+    +----+----+
   | Go Server|     | Go Server |    | Go Server|
   | :21114-19|     | Console   |    | CDAP GW  |
   +----+----+     +-----+-----+    +----+----+
        |                |                |
        +--------+-------+-------+--------+
                 |               |
            [SQLite/PG]     [auth.db]
```

All three Go Server boxes are the same `betterdesk-server` process.

## STRIDE Analysis

### S — Spoofing

| Threat | Component | Mitigation | Status |
|--------|-----------|------------|--------|
| Device ID spoofing | Signal server | Ed25519 public key verification on registration | ✅ Done |
| Admin impersonation | Web console | PBKDF2 password hashing (bcrypt for older hashes) + TOTP 2FA | ✅ Done |
| API key theft | Go server API | Key stored in file with 0600 permissions, auto-regenerated | ✅ Done |
| Org user impersonation | Org system | PBKDF2/bcrypt password hashing + JWT with short TTL | ✅ Done |
| Relay UUID spoofing | Relay server | Server-generated UUIDs, pending UUID tracking map | ✅ Done |

### T — Tampering

| Threat | Component | Mitigation | Status |
|--------|-----------|------------|--------|
| Message modification in transit | Signal/Relay | NaCl authenticated encryption (XSalsa20-Poly1305) | ✅ Done |
| Database tampering | SQLite/PG | File permissions, PostgreSQL row-level locking | ✅ Done |
| Config file tampering | Client | Organization policy enforcement on startup | 🔲 Phase 4 |
| Audit log tampering | Go server | Ring-buffer audit log | ⚠️ Add HMAC chain |

### R — Repudiation

| Threat | Component | Mitigation | Status |
|--------|-----------|------------|--------|
| Deny remote session | Web console | Audit log with connection start/end, operator ID, device ID | ✅ Done |
| Deny admin actions | Web console | Audit log for login, config changes, device operations | ✅ Done |
| Deny file transfer | Client | Session recording planned | 🔲 Phase 3 |

### I — Information Disclosure

| Threat | Component | Mitigation | Status |
|--------|-----------|------------|--------|
| Private key exposure | Go server | Ed25519 keys in separate directory, 0600 permissions | ✅ Done |
| Password in logs | All | Passwords never logged, masked in error messages | ✅ Done |
| API key in URL | Go server API | Keys accepted only in the X-API-Key header, never in URL params | ✅ Done |
| Error stack traces | Web console | HTTP error responses never include a stack trace | ✅ Done |
| Database path disclosure | Go server | Generic error messages for DB failures | ✅ Done |

### D — Denial of Service

| Threat | Component | Mitigation | Status |
|--------|-----------|------------|--------|
| TCP connection flood | Signal server | Per-IP rate limiting, connection cap (10K) | ✅ Done |
| Login brute force | Go server API | IP-based rate limiter on /api/auth/login | ✅ Done |
| Relay stale sessions | Relay server | Idle timeout wrapper (io.Copy), 2-min TTL | ✅ Done |
| WebSocket exhaustion | Signal/Relay WS | Origin validation, connection limits | ✅ Done |
| Large request body | Web console | Body limit 2 MB; per-path limits on the client API port 21121 (1 KB unless the path allows more) | ✅ Done |

### E — Elevation of Privilege

| Threat | Component | Mitigation | Status |
|--------|-----------|------------|--------|
| Operator → Admin escalation | Web console | Role and permission guards on console routes | ✅ Done |
| User → Operator escalation | Org system | JWT claims include role, verified on every request | ✅ Done |
| Banned device reconnect | Signal server | IsPeerBanned + IsPeerSoftDeleted checks | ✅ Done |
| SQL injection | All DB queries | Parameterized queries throughout, LIKE escape for wildcards | ✅ Done |

## Trust Boundaries

1. **Internet ↔ Go Server** — TLS required for production (`--tls-signal`, `--tls-relay`)
2. **Browser ↔ Web Console** — HTTPS recommended, session cookies with Secure/HttpOnly/SameSite
3. **Web Console ↔ Go Server API** — In-process calls inside the same binary, acting as the API key; no network hop
4. **Desktop Client ↔ Signal Server** — NaCl key exchange, then encrypted channel
5. **Peer ↔ Peer (P2P)** — NaCl box encryption (X25519 + XSalsa20-Poly1305)
6. **CDAP Agent ↔ Gateway** — WebSocket with API key authentication

## Open Risks

| Risk | Severity | Planned Mitigation |
|------|----------|-------------------|
| No API key rotation mechanism | Medium | Auto-rotation with grace period (Phase 8) |
| Audit log not tamper-proof | Low | HMAC chain signing (Phase 8) |
| No certificate pinning in client | Medium | Pin server certificate in Tauri (Phase 8) |
| Credentials not zeroized in memory | Low | Use zeroize crate for Rust, explicit clearing in Go (Phase 8) |
