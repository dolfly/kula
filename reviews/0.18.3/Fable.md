# Kula — Security & Code-Quality Review

**Version reviewed:** 0.18.3   
**Date:** 2026-06-12   
**Reviewer:** Fable (independent full-codebase audit)   
**Scope:** `cmd/`, `internal/` (web, auth, sandbox, collector, storage, config, backup, i18n, tui), embedded frontend JS, packaging   
**Build state at review:** `go build ./...` ✅ · `go vet ./...` ✅ · `go test ./internal/{web,config,storage}` ✅ · `govulncheck ./...` → **No vulnerabilities found** ✅   
**Codebase size:** ~14.8k LOC non-test (Go), plus embedded dashboard JS

---

## 1. Executive summary

Kula is a single-binary Linux monitoring agent: it reads `/proc` and `/sys`, stores samples in an embedded tiered ring-buffer, and exposes them over an HTTP/WebSocket dashboard, a Prometheus endpoint, an optional Ollama AI proxy, and a TUI. This is a mature, security-aware codebase. The hardening already in place is real and broad: Argon2id password hashing with a constant-time username/hash compare, SHA-256-hashed session tokens at rest, CSRF defense (Origin/Referer **plus** synchronizer token, **on by default**), memory-bounded per-IP/per-user rate limiting, a Landlock filesystem+network sandbox derived from the actual config, CSP with per-request nonces, SRI for embedded JS, an SSRF-locked Ollama proxy, and disciplined output encoding on both the Go and JS sides.

The two diffs since 0.17.3 — scheduled tier backups (0.18.0) and config seeding (0.18.2) — are well-constructed: the backup path uses temp-dir + atomic rename, copies under the per-tier read lock, fsyncs, and prunes only directories matching its own timestamp layout. The login timing side-channel flagged in the 0.17.3 review is **confirmed fixed** ([auth.go:204-211](../../internal/web/auth.go#L204)).

**No Critical, High, or Medium severity issues were found.** Every item below is defense-in-depth hardening or code-quality. The most security-relevant residual item is the unauthenticated-by-default Prometheus endpoint (a deliberate, documented trade-off), followed by a non-security correctness bug in the OpenAI-compatible tool-call accumulator.

### Scorecard

| Category | Score | Notes |
|---|---|---|
| Authentication & session mgmt | 9.0 / 10 | Argon2id, hashed tokens, sliding expiry, timing oracle closed. No absolute session lifetime cap. |
| Web/API hardening | 9.5 / 10 | CSRF on by default, CORS w/ `Vary: Origin`, CSP+nonce, SRI, body-size caps, JSON-marshalled errors. |
| Transport / network exposure | 8.0 / 10 | No native TLS (proxy-dependent); Prometheus unauthenticated by default. |
| Input handling / injection | 10 / 10 | No command execution; constant/parameterized SQL; escaped libpq DSN; file/socket-only custom metrics; SSRF-locked Ollama; allow-listed i18n + model regex. |
| Sandboxing / least privilege | 9.0 / 10 | Landlock V5 best-effort, config-derived FS+net rules. No seccomp / explicit privilege-drop guidance. |
| Storage & backup robustness | 9.0 / 10 | `0600` files / `0750` dirs, length-prefix validated against tier max, atomic temp+rename, fsync on backup, lock-safe snapshots. |
| Frontend (XSS surface) | 9.0 / 10 | `escapeHTML` before every `innerHTML`; markdown renderer escapes first; DOM `option` values set via `.value`/`.textContent`. |
| Dependency & supply chain | 9.0 / 10 | Few, current deps; govulncheck clean; CI runs vuln scan + CodeQL + pinned action SHAs (per prior review). |
| Code quality / maintainability | 8.5 / 10 | Clean, heavily commented; minor tool-call indexing bug; a few ignored-error nits. |
| **Overall** | **9.0 / 10 (A)** | Strong posture; only a small hardening/quality backlog remains. |

---

## 2. What's already done well (keep it)

- **Password storage & login** — `argon2.IDKey` with config-tunable params; `subtle.ConstantTimeCompare` on both username and hash; a throwaway Argon2 computation on the username-miss path closes the enumeration timing oracle ([auth.go:187-212](../../internal/web/auth.go#L187)).
- **Sessions** — 32-byte CSPRNG tokens stored **SHA-256-hashed** in memory and on disk; `sessions.json` written `0600`; sliding expiry; expired entries filtered on load/save ([auth.go:214-393](../../internal/web/auth.go#L214)).
- **CSRF** — Origin/Referer validation **plus** synchronizer token (`X-CSRF-Token`, constant-time compared); `origin_validation` defaults **true** ([auth.go:406-464](../../internal/web/auth.go#L406)).
- **Rate limiting** — per-IP and per-username login limiters, **memory-capped and fail-closed** at 16384 keys ([auth.go:42-65](../../internal/web/auth.go#L42)); separate per-IP/min limiters for Ollama chat (10/min) and meta (60/min), purged by a background ticker ([ollama.go:45-110](../../internal/web/ollama.go#L45), [server.go:454-466](../../internal/web/server.go#L454)).
- **SSRF defense** — `validateOllamaURL` restricts the backend to loopback only (`localhost`/`127.0.0.1`/`::1`) at config load ([config.go:515-528](../../internal/config/config.go#L515)); the client-supplied model override is validated against a strict regex and body-size capped ([ollama.go:24,306-312](../../internal/web/ollama.go#L24)).
- **Sandbox** — Landlock V5 best-effort over `/proc`,`/sys`, config (ro), storage (rw) plus scoped TCP bind/connect and runtime-socket rules derived from the active config; the Ollama connect rule is added only for the configured port ([sandbox.go](../../internal/sandbox/sandbox.go)).
- **No command execution** — Kula never spawns an external process for monitoring; the only `os/exec` reference is an existence probe for `nvidia-smi`. Custom metrics are intentionally **socket-only** ([custom.go](../../internal/collector/custom.go)).
- **SQL** — all queries are constant strings; PostgreSQL interpolates only via placeholders; the libpq DSN password is single-quote/backslash escaped ([postgres.go:56-63](../../internal/collector/postgres.go#L56)); connections are capped to 1 open/idle with a lifetime.
- **WebSocket** — `CheckOrigin` parses the Origin with `net/url` and enforces same-host or allow-listed; `SetReadLimit(4096)`, read/write deadlines + ping/pong, per-IP (default 5) and global (default 100) connection caps with leak-safe `sync.Once` unregister ([websocket.go](../../internal/web/websocket.go)).
- **Backups (new in 0.18.0)** — temp-dir + atomic rename so a crash never leaves a "complete"-looking partial; raw copy under the per-tier read lock; gzip outside the lock; fsync on both raw and gz; pruning restricted to directories that parse against the `20060102-150405` layout, so foreign files are never deleted ([backup.go](../../internal/backup/backup.go)).
- **Frontend encoding** — `escapeHTML` ([state.js:138](../../internal/web/static/js/app/state.js#L138)) is applied before interpolating any server/AI value into `innerHTML`; `renderMarkdownLite` escapes the whole string *first*, then applies formatting to the already-escaped text ([ollama.js:566-567](../../internal/web/static/js/app/ollama.js#L566)); device/model/interface names go into `<option>` via `.value`/`.textContent`, not string-built HTML.
- **Request safety** — `http.MaxBytesReader` on login (4 KiB) and Ollama (32 KiB); `jsonError` marshals error bodies so user/log strings can't break JSON framing or inject markup ([server.go:200-207](../../internal/web/server.go#L200)).
- **Base-path hardening** — `normalizeBasePath` rejects `//`/`/\` prefixes (CWE-601 open-redirect via protocol-relative URL) and `.`/`..` segments before the value reaches a `Location` header or `<base href>` ([config.go:535-575](../../internal/config/config.go#L535)).

---

## 3. Findings

Severity legend: 🟠 Medium · 🟡 Low · ⚪ Info. **None Critical or High.**

### 3.1 🟡 Prometheus `/metrics` is unauthenticated by default
**File:** [server.go:400-408](../../internal/web/server.go#L400), [prometheus.go:18-39](../../internal/web/prometheus.go#L18)

When `prometheus_metrics.enabled: true` but no `token` is configured, the endpoint serves the full host metrics surface (hostname, per-interface throughput, disk mounts, load, container/app stats) with no auth — and unlike `/api/*`, it is *not* behind `AuthMiddleware`, only the optional bearer-token check. The code logs a warning in this state ("enabled … **without authentication**"), so it's a conscious trade-off, but the default-off-token combined with default-no-TLS means an operator who flips the feature on without reading the log exposes telemetry to anyone who can reach the port.

**Why it's only Low:** opt-in feature, off by default; emits an explicit startup warning; the data is monitoring telemetry, not secrets.

**Recommendation:** keep the warning, but consider documenting in `config.example.yaml` (inline next to `prometheus_metrics`) that the endpoint should be bound to loopback / a Unix socket or fronted by a proxy when no token is set. Optionally, refuse to serve on a non-loopback `listen` address when `enabled && token == ""` unless an explicit `insecure: true` is set.

### 3.2 🟡 No absolute session lifetime — sliding expiry can extend a session indefinitely
**File:** [auth.go:240-259](../../internal/web/auth.go#L240)

`ValidateSession` refreshes `expiresAt = now + SessionTimeout` on every authenticated request. A token that is used at least once per `SessionTimeout` window (default 24h) never expires, even if the original login was weeks ago. There is no `createdAt + maxLifetime` ceiling and no server-side "log out everywhere." A stolen-but-actively-used token therefore outlives any reasonable rotation expectation.

**Why it's only Low:** tokens are 256-bit CSPRNG, hashed at rest, `HttpOnly`, and `Secure` over TLS; theft already requires a meaningful compromise.

**Recommendation:** enforce an absolute cap using the already-stored `createdAt`: in `ValidateSession`, reject (and delete) when `now.After(sess.createdAt.Add(maxLifetime))` regardless of sliding expiry. A configurable `absolute_session_timeout` (e.g. 7d) covers it.

### 3.3 🟡 `trust_proxy` trusts `X-Forwarded-For` from any peer, with no proxy allow-list
**File:** [server.go:1043-1059](../../internal/web/server.go#L1043)

With `trust_proxy: true`, `getClientIP` takes the **rightmost** XFF entry (correctly the value appended by the immediate upstream, which is the secure choice and should be kept). However, the trust is unconditional on the TCP peer: if Kula is ever reachable directly (misconfiguration, a second listener, container networking) while `trust_proxy` is on, a client can set `X-Forwarded-For` and control the IP used for login rate-limiting and access logs — diluting the per-IP limiter and poisoning logs.

**Why it's only Low:** requires `trust_proxy` to be enabled *and* a path that bypasses the intended proxy; the rightmost-hop choice already blunts the most common spoof.

**Recommendation:** gate XFF parsing on the TCP peer (`r.RemoteAddr`) being in a configurable trusted-proxy CIDR set (default: loopback + RFC1918), so a direct connection from an untrusted address falls back to `RemoteAddr` even when `trust_proxy` is on. The 0.17.3 GLM review raised the same theme (M-03); it remains open.

### 3.4 🟡 OpenAI-compatible tool-call accumulator indexes a slice by stream index (correctness)
**File:** [ollama.go:652-664](../../internal/web/ollama.go#L652)

```go
toolCalls := make([]ollamaToolCall, len(accumMap))
for i, a := range accumMap {        // i is tc.Index from the wire, not 0..n-1
    if i < len(toolCalls) { ... }   // out-of-range indices silently dropped
}
```

`accumMap` is keyed by the provider-supplied `tool_calls[].index`. The result slice is sized `len(accumMap)` but written at key `i`. If a backend emits a single tool call at a non-zero index, or sparse/non-contiguous indices, slots are left as zero-value `ollamaToolCall{}` (empty `Name`), which downstream resolves to `"unknown tool: "`, and legitimately-indexed calls past `len-1` are dropped. Only the well-behaved contiguous-from-zero case works.

**Impact:** not a security issue — the backend is loopback-locked and the tool is read-only metrics. It's a latent functional bug that surfaces as broken AI tool-calls against some OpenAI-compatible servers.

**Recommendation:** collect deterministically by sorting the map keys and appending, e.g. build a sorted `[]int` of keys then `append` each `accumMap[k]`, dropping the index-as-slot assumption entirely.

### 3.5 ⚪ CSP nonce ignores `crypto/rand` error
**File:** [server.go:215-220](../../internal/web/server.go#L215)

```go
b := make([]byte, 16)
_, _ = rand.Read(b)          // error ignored
nonce := base64.StdEncoding.EncodeToString(b)
```

If `rand.Read` ever failed, the nonce would be an all-zero, fully predictable value, weakening the script-src nonce for that response. In practice `crypto/rand` does not fail on Linux, so this is informational.

**Recommendation:** on error, either fail the request (500) or fall back to a per-process random seed mixed with a counter; at minimum log it. Same pattern is worth a glance in `securityMiddleware`.

### 3.6 ⚪ Custom-metrics Unix socket is group-writable (`0660`)
**File:** [custom.go:47-55](../../internal/collector/custom.go#L47), [collector.go:129](../../internal/collector/collector.go#L129)

The custom-metrics ingestion socket (`<storage>/kula.sock`) is created `0660`, so any process in Kula's group can push metric values. Input is well-constrained — only **configured** group/metric names are accepted and values are coerced to `float64` ([custom.go:131-167](../../internal/collector/custom.go#L131)) — so the blast radius is "inject plausible numbers into already-enabled custom charts," not code/DoS. Worth noting for threat models where the group boundary matters.

**Recommendation:** document the trust boundary (anyone in the group can write custom metrics) next to the `custom:` config section; optionally make the socket mode configurable like `unix_socket_mode` already is for the web socket.

### 3.7 ⚪ Sliding-window rate limiters rebuild a slice per call
**File:** [auth.go:130-142](../../internal/web/auth.go#L130), [ollama.go:73-83](../../internal/web/ollama.go#L73)

Each `Allow` allocates a fresh `recent` slice by filtering the key's timestamp list. Functionally correct and memory-bounded (16384-key cap, periodic purge), but it allocates on every login/chat attempt. Negligible at a self-hosted monitor's request volume; flagged only as a micro-optimization opportunity (e.g. in-place compaction).

---

## 4. Verification of prior (0.17.3) findings

| Prior finding | Status in 0.18.3 |
|---|---|
| Login timing side-channel → username enumeration | ✅ **Fixed** — throwaway Argon2 + constant-time compare on miss ([auth.go:204-211](../../internal/web/auth.go#L204)). |
| Rate-limiter unbounded memory | ✅ **Fixed** — `maxRateLimiterKeys = 16384`, fail-closed ([auth.go:42-65](../../internal/web/auth.go#L42)). |
| OOB panic on truncated/corrupt records | ✅ Length prefix validated against `t.maxData` before use ([tier.go](../../internal/storage/tier.go)); fuzz tests present. |
| libpq DSN password escaping (GLM H-01) | ✅ Escaped ([postgres.go:60-62](../../internal/collector/postgres.go#L60)). MySQL DSN still uses raw `fmt.Sprintf` — acceptable as the values come from an admin-controlled config, but `mysql.Config.FormatDSN` would be more robust ([mysql.go:38-45](../../internal/collector/mysql.go#L38)). |
| Trusted-proxy CIDR (GLM M-03) | ⚠️ **Open** — see §3.3. |
| Prometheus unauth by default | ⚠️ **Open** (by design) — see §3.1. |

---

## 5. Methodology & coverage

- **Manual review** of the full web layer (server/auth/websocket/ollama/prometheus), all collectors (custom, postgres, mysql, containers, gpu, network, disk), storage (store/tier/codec snapshots & framing), backup scheduler + cron parser, config loader/validators, sandbox, `cmd/kula/main.go`, and the embedded dashboard JS (DOM-injection sinks and escaping).
- **Static/dynamic checks:** `go build ./...`, `go vet ./...`, `go test ./internal/{web,config,storage}` (all green), and `govulncheck ./...` (**no known-vulnerable symbols**).
- **Injection sweep:** grepped for `exec`/`innerHTML`/`eval`/SQL concatenation; confirmed no command execution and that every `innerHTML` write of dynamic data is preceded by `escapeHTML` or built from constants.

### Bottom line

0.18.3 is a well-engineered, defense-in-depth codebase with no Critical/High/Medium issues found. The new backup and config-seeding code is solid. Prioritized backlog: (1) document/optionally enforce Prometheus exposure expectations (§3.1), (2) add an absolute session lifetime (§3.2), (3) gate `trust_proxy` on a proxy CIDR allow-list (§3.3), (4) fix the OpenAI tool-call indexing bug (§3.4).
