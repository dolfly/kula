# Kula — Security & Code-Quality Review

**Version reviewed:** 0.17.3
**Date:** 2026-05-30
**Reviewer:** Independent audit (full-codebase pass)
**Scope:** `cmd/`, `internal/` (web, auth, sandbox, collector, storage, config, tui, i18n), CI/packaging
**Build state at review:** `go build ./...` ✅ · `go vet ./...` ✅ · CI runs `-race`, `govulncheck`, `golangci-lint`, CodeQL
**Codebase size:** ~14.5k LOC non-test, ~6.8k LOC test

---

## 1. Executive summary

Kula is a self-contained Linux monitoring agent (single binary, embedded tiered storage, web/API/Prometheus surface). The code is **written by a security-aware author** and reflects real hardening work: Argon2id hashing, SHA-256-hashed session tokens at rest, CSRF (origin + synchronizer token, **on by default**), per-IP/per-user rate limiting with bounded memory, a Landlock filesystem+network sandbox, CSP with per-request nonces, SRI for embedded JS, an SSRF-locked Ollama proxy, and — notably — **no external command execution at all**. CI is strong (pinned SHAs, read-only token, vuln scanning).

**No Critical or High severity issues were found.** The items below are **defense-in-depth hardening** and **code-quality** notes. The single most actionable item is a login **timing side-channel** enabling username enumeration despite rate limiting.

### Scorecard

| Category | Score | Notes |
|---|---|---|
| Authentication & session mgmt | 8.5 / 10 | Argon2id, hashed tokens, sliding expiry. Timing oracle on unknown user; no absolute session cap. |
| Web/API hardening | 9.5 / 10 | CSRF on by default, CORS, CSP+nonce, SRI, body-size caps, generic errors. |
| Transport / network exposure | 8 / 10 | No native TLS (proxy-dependent); Prometheus unauth by default. |
| Input handling / injection | 10 / 10 | No command execution; constant SQL + parameterized `$1`; file-only custom metrics; SSRF-locked Ollama. |
| Sandboxing / least privilege | 9 / 10 | Landlock FS+net best-effort, config-derived. No seccomp/privilege-drop guidance. |
| Storage robustness | 8.5 / 10 | `0600` files / `0750` dir, atomic temp+rename, length bounded by tier max. No per-record CRC; no fsync. |
| Dependency & supply chain | 9 / 10 | Few, current deps; govulncheck + CodeQL in CI; SHA-pinned actions. |
| Code quality / maintainability | 8.5 / 10 | Clean, heavily commented; a few ignored-error / robustness nits. |
| **Overall** | **8.9 / 10 (A-)** | Strong posture; small hardening backlog. |

---

## 2. What's already done well (keep it)

- **Password storage** — `argon2.IDKey` with config-tunable params; `subtle.ConstantTimeCompare` on both username and hash ([auth.go:169](../internal/web/auth.go#L169)).
- **Sessions** — 32-byte CSPRNG tokens, **stored SHA-256-hashed** in memory and on disk; `sessions.json` written `0600`; sliding expiry; expired entries filtered on load/save ([auth.go:189-368](../internal/web/auth.go#L189)).
- **CSRF** — Origin/Referer validation **plus** synchronizer token (`X-CSRF-Token`), constant-time compared; `origin_validation` defaults **true** ([auth.go:410](../internal/web/auth.go#L410), [config.go:328](../internal/config/config.go#L328)).
- **Rate limiting** — per-IP and per-username login limiters, **memory-capped and fail-closed** at 16384 keys ([auth.go:42-143](../internal/web/auth.go#L42)); separate per-IP/min limiters for Ollama chat/meta.
- **Sandbox** — Landlock V5 best-effort over `/proc`,`/sys`,config,storage + scoped TCP bind/connect rules derived from the actual config ([sandbox.go](../internal/sandbox/sandbox.go)).
- **SSRF defense** — `validateOllamaURL` restricts the Ollama backend to loopback only (`localhost`/`127.0.0.1`/`::1`) ([config.go:463](../internal/config/config.go#L463)); proxied model name validated against a strict regex ([ollama.go:24,307](../internal/web/ollama.go#L24)).
- **Browser hardening** — CSP with per-request nonce, `X-Content-Type-Options`, `X-Frame-Options`, `Referrer-Policy`, `Permissions-Policy`, conditional HSTS, plus SRI hashes for embedded JS ([server.go:206-234](../internal/web/server.go#L206), [server.go:982](../internal/web/server.go#L982)).
- **No command execution** — Kula never runs an external process. The lone `os/exec` reference is `exec.LookPath("nvidia-smi")`, an *existence check* that only decides a discovery log line ([gpu.go:27](../internal/collector/gpu.go#L27)); the binary is never executed. NVIDIA stats come from sysfs or an external exporter's `nvidia.log`, which Kula reads and even perms-checks ([gpu_nvidia.go:30](../internal/collector/gpu_nvidia.go#L30)). Custom metrics are intentionally **file/socket-only** ([custom.go](../internal/collector/custom.go)).
- **SQL** — all queries are constant strings; the only value interpolation uses placeholders (`$1`) for the database name ([postgres.go:193](../internal/collector/postgres.go#L193)); the libpq password is correctly escaped ([postgres.go:60](../internal/collector/postgres.go#L60)).
- **WebSocket** — origin check (same-host or allow-listed), `SetReadLimit(4096)`, read/write deadlines + ping/pong, per-IP (default 5) and global (default 100) connection caps ([websocket.go](../internal/web/websocket.go)).
- **Storage** — record framing validates the length prefix against the tier's max size before allocating (`dataLen == 0 || > t.maxData → break`, [tier.go:300](../internal/storage/tier.go#L300), [tier.go:485](../internal/storage/tier.go#L485)); writes go through an atomic temp-file + rename ([tier.go:536](../internal/storage/tier.go#L536)); files `0600`, dir `0750`; per-section `need()` bounds checks throughout the decoder.
- **Request safety** — `http.MaxBytesReader` on login (4 KiB) and Ollama (32 KiB); `jsonError` marshals error bodies so log/user strings can't break JSON framing ([server.go:195](../internal/web/server.go#L195)).
- **CI/supply chain** — `go vet`, `-race`, `govulncheck`, `golangci-lint`, CodeQL; `permissions: contents: read`; `persist-credentials: false` with an explicit credential-leak assertion; third-party actions pinned by commit SHA ([.github/workflows/ci.yml](../.github/workflows/ci.yml)).

---

## 3. Findings

Severity legend: 🟡 Low · ⚪ Info. None Critical / High / Medium.

### 3.1 🟡 Login timing side-channel → username enumeration (✅ FIXED)
**File:** [internal/web/auth.go:169-187](../internal/web/auth.go#L169)

```go
func (a *AuthManager) ValidateCredentials(username, password string) bool {
    if !a.cfg.Enabled { return true }
    if subtle.ConstantTimeCompare([]byte(username), []byte(a.cfg.Username)) == 1 {
        hash := HashPassword(password, a.cfg.PasswordSalt, a.cfg.Argon2) // expensive
        return subtle.ConstantTimeCompare([]byte(hash), []byte(a.cfg.PasswordHash)) == 1
    }
    for _, u := range a.cfg.Users {
        if subtle.ConstantTimeCompare([]byte(username), []byte(u.Username)) == 1 {
            hash := HashPassword(password, u.PasswordSalt, a.cfg.Argon2) // expensive
            return subtle.ConstantTimeCompare([]byte(hash), []byte(u.PasswordHash)) == 1
        }
    }
    return false  // <-- returns immediately, no Argon2 work
}
```

A **known** username triggers an Argon2id computation (tens of ms at the default `memory=32 MB`, `time=3`); an **unknown** username returns in microseconds. The response-time delta is a reliable oracle for enumerating valid usernames. The per-IP/per-user limiters (5 / 5 min) slow but don't close it — an attacker probes one guess per username.

**Recommendation:** always compute one Argon2 hash against a fixed dummy salt/hash on the username-miss path before returning false:

```go
var dummySalt = "0000000000000000000000000000000000000000000000000000000000000000"
// on miss, after the loops:
_ = HashPassword(password, dummySalt, a.cfg.Argon2)
return false
```

*Low, not Medium:* requires the UI reachable and auth enabled, and yields only usernames (not credentials); rate limiting raises cost. Still a genuine, cheap-to-fix oracle.

---

### 3.2 ⚪ Prometheus `/metrics` is unauthenticated by default
**Files:** [server.go:400-408](../internal/web/server.go#L400), [prometheus.go:24-32](../internal/web/prometheus.go#L24)

`/metrics` is mounted **outside** `AuthMiddleware`; it is protected only if `prometheus_metrics.token` is set, and a missing token is merely logged as a warning. Exposed metrics leak host/topology detail (hostname, filesystems, container names, process counts) to anyone who can reach the port.

Two sub-notes:
- The bearer check uses `subtle.ConstantTimeCompare`, which short-circuits on length mismatch — it leaks token *length* (negligible).
- This is a deliberate scraper-friendly trade-off and it *is* logged at startup, so it's Info — but worth a louder docs warning, or requiring a token when both `prometheus_metrics.enabled` and `auth.enabled` are true.

---

### 3.3 ⚪ Session lifetime has no absolute cap
**File:** [internal/web/auth.go:230-231](../internal/web/auth.go#L230)

```go
// Sliding expiration
sess.expiresAt = time.Now().Add(a.cfg.SessionTimeout)
```

Every successful validation extends the session, so an actively-used session never ages out, regardless of when it was created. A stolen token stays valid indefinitely while exercised within the idle window.

**Recommendation:** `createdAt` is already stored — also enforce an absolute maximum (e.g. 7 days) alongside the sliding idle timeout in `ValidateSession`.

---

### 3.4 ⚪ No native TLS; transport security delegated to a proxy
**File:** [server.go:484-534](../internal/web/server.go#L484)

The server speaks plain HTTP (or a Unix socket); HTTPS is assumed to come from a reverse proxy. Cookie `Secure`/HSTS are set only when `r.TLS != nil` or `trust_proxy` + `X-Forwarded-Proto: https`. Reasonable for a single-binary tool, but a direct-to-internet deployment without a proxy sends the session cookie and Ollama traffic in clear text.

**Recommendation:** keep proxy-first, document it as a hard requirement for any non-loopback exposure, and consider an optional `tls: {cert, key}` block for standalone HTTPS.

---

### 3.5 ⚪ On-disk records have no integrity check (CRC)
**Files:** [tier.go:289-312](../internal/storage/tier.go#L289), [codec.go](../internal/storage/codec.go)

Record framing is a 4-byte length prefix; the length is sanity-checked against the tier's max size, and the decoder uses per-section `need()` bounds checks, so a corrupted file degrades gracefully (a bad record breaks the read loop rather than crashing or over-allocating). There is **no per-record checksum**, so silent bit-rot inside a record is not detected — it is decoded as plausible-but-wrong metric values. Threat is limited (local disk corruption; the data dir is `0600`/`0750` and Landlock-guarded), so this is Info.

**Recommendation:** optional — append a CRC32 per record (or per tier flush) and skip records that fail it. Note: this is a storage-format change; follow the backward-compat rules documented atop `codec.go`.

---

### 3.6 ⚪ Code-quality / robustness nits
- **Ignored `Sscanf` return values** — [server.go:657](../internal/web/server.go#L657) (`?points=`) and [config.go:626](../internal/config/config.go#L626) (`parseSize`). Malformed input silently falls back to defaults rather than reporting the operator's mistake. (`KULA_PORT` is already handled correctly via `strconv.ParseInt` + range check at [config.go:391](../internal/config/config.go#L391) — good; mirror that style here.)
- **MySQL DSN built by `fmt.Sprintf`** — [mysql.go:41-43](../internal/collector/mysql.go#L41). A password containing `@`, `:`, or `/` corrupts the DSN. Operator-controlled, so low impact, but `mysql.Config{}.FormatDSN()` would be more robust (Postgres already escapes its password correctly).
- **No `fsync` before atomic rename** — [tier.go:536](../internal/storage/tier.go#L536). Rename is atomic, but on power loss the freshly written tier may be lost; consider `f.Sync()` before rename for the persistence tiers.
- **`Authorization` scheme match is case-sensitive** — [auth.go:273](../internal/web/auth.go#L273) (`== "Bearer "`). RFC 7235 schemes are case-insensitive; minor interop only.
- **`sslmode: disable` default for Postgres** — [config.go:350](../internal/config/config.go#L350). Fine for a loopback socket; document that remote Postgres monitoring should set `require`/`verify-full`.

---

## 4. Threat-model notes (verified safe — no action)

- **Static-asset path traversal** ([server.go:1044](../internal/web/server.go#L1044)) — served from `embed.FS`; `net/http` path-cleans and `embed.FS` rejects `..`. Safe.
- **Cross-site WebSocket hijacking** ([websocket.go:30](../internal/web/websocket.go#L30)) — `CheckOrigin` enforces same-host / allow-list; empty-Origin (non-browser) allowed by design. Safe for the browser threat model.
- **Base-path open redirect** ([config.go:483](../internal/config/config.go#L483)) — `normalizeBasePath` rejects `//`, `/\`, control chars, and `.`/`..` segments specifically to prevent CWE-601. Good.
- **JSON error injection** — all error responses go through `json.Marshal` ([server.go:195](../internal/web/server.go#L195)); Prometheus labels escaped ([prometheus.go:679](../internal/web/prometheus.go#L679)).
- **Committed secrets** — tracked `config.yaml`/`config.example.yaml` carry only example hashes/salts; runtime `config.yaml`, `data/`, and built binaries are git-ignored. Clean.
- **`html/template`** auto-escapes; the only injected dynamic value is the CSPRNG nonce.

---

## 5. Prioritized remediation plan

| # | Priority | Item | Effort | File |
|---|---|---|---|---|
| 1 | P1 | Constant-time login: hash a dummy on username miss (§3.1) | XS | [auth.go:169](../internal/web/auth.go#L169) |
| 2 | P3 | Absolute session lifetime cap (§3.3) | S | [auth.go:230](../internal/web/auth.go#L230) |
| 3 | P3 | Require/loudly warn on tokenless Prometheus when auth on (§3.2) | S | [server.go:400](../internal/web/server.go#L400) |
| 4 | P3 | Document TLS/proxy requirement; optional native TLS (§3.4) | M | [server.go:484](../internal/web/server.go#L484) |
| 5 | P4 | Nits: `Sscanf` errors, MySQL DSN, fsync, Bearer case (§3.6) | S | multiple |
| 6 | P4 | Optional per-record CRC (§3.5) | M | [codec.go](../internal/storage/codec.go) |

**Operator-doc additions worth making:** require a reverse proxy + TLS for any non-loopback exposure; run under a dedicated unprivileged user with systemd hardening (`NoNewPrivileges=`, `ProtectSystem=strict`, `PrivateTmp=`, a sanitized `PATH`) to complement Landlock.

---

*Complements prior model reviews under `reviews/<version>/`. All findings reference `file:line` at v0.17.3; line numbers may drift with edits. Note: the codebase's defaults are hardened (CSRF/origin-validation on, security headers on, files `0600`), so most "insecure default" classes simply do not apply here.*
