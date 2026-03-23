# Security Review of `c0m4r/kula`

**Scope:** current `main` branch, with emphasis on `internal/web/*`, `internal/storage/*`, `internal/collector/*`, and the embedded frontend in `internal/web/static/app.js`.

## Executive summary

`kula` has a strong security baseline for a self-contained monitoring tool. The code already uses Argon2id for password hashing, hashes session tokens before persistence, writes session state with `0600` permissions, enforces SameSite cookie semantics, applies CSP nonces and standard hardening headers, and limits WebSocket reads to 4 KiB.

The main remaining risks are operational and deployment-sensitive: cookie security depends on TLS or trusted proxy configuration, CSRF protection is strict enough to reject some legitimate non-browser clients, login throttling is IP-only, and WebSocket origin handling intentionally accepts empty `Origin` headers for CLI clients. There are also a couple of lower-severity integrity issues in the storage layer and query cache.

**Overall score: 7.5 / 10**

## What is already done well

The authentication path is materially better than average. Password verification uses Argon2id, session tokens are generated from cryptographically secure randomness, tokens are hashed before being stored in memory and on disk, and expired sessions are cleaned up periodically.

The session cookie is also constrained with `HttpOnly`, `SameSite: Strict`, and a bounded lifetime. That is a good baseline against script access and ambient browser leakage.

On the UI side, the app uses a nonce-based CSP, `X-Frame-Options: DENY`, `X-Content-Type-Options: nosniff`, and `Permissions-Policy` restrictions. The frontend also escapes alert text before inserting it into `innerHTML`.

The WebSocket layer is also reasonably defensive: it rate-limits connections per IP, caps the number of live sockets, and sets a 4096-byte read limit for inbound JSON commands.

## Findings

### 1) Session cookie `Secure` depends on TLS or trusted proxy headers
**Severity: High**

The login cookie is marked `Secure` only when the request is already TLS-terminated or when `TrustProxy` is enabled and `X-Forwarded-Proto` is exactly `https`. The server also prints a warning that `TrustProxy` must only be used behind a trusted reverse proxy that handles `X-Forwarded-For`.

**Why it matters:** if an operator terminates TLS upstream but misconfigures `TrustProxy`, the session cookie can be issued without `Secure`. That is not a code exploit by itself, but it creates a sharp deployment failure mode where a valid session can travel over plaintext HTTP.

**Recommendation:** make secure-cookie behavior explicit and safer by default, or fail loudly when auth is enabled and the deployment path cannot guarantee TLS semantics. A dedicated `ForceSecureCookies` option would remove ambiguity.

### 2) CSRF middleware is strict enough to block legitimate non-browser clients
**Severity: Medium**

`ValidateOrigin()` rejects requests that do not provide `Origin` or `Referer`, and the CSRF middleware applies that check to all state-modifying requests. At the same time, `AuthMiddleware()` accepts bearer tokens via `Authorization: Bearer ...`, which is good for API clients but does not bypass CSRF origin checks.

**Why it matters:** browser-based flows are covered, but non-browser clients such as scripts, CLI tools, or automation can be rejected even when they are properly authenticated. That is not a vulnerability, but it is an interoperability trap that often leads to ad hoc workarounds.

**Recommendation:** keep the origin check for browser sessions, but provide a clear authenticated API path that does not depend on `Origin`/`Referer`, or make the behavior configurable.

### 3) Login throttling is IP-only and fairly small in scope
**Severity: Medium**

The rate limiter allows five login attempts per IP in a rolling five-minute window. It tracks timestamps in memory and purges stale entries during session cleanup.

**Why it matters:** IP-only throttling is a useful first fence, but it is weak against distributed attacks, shared NAT environments, and reverse-proxy setups where client identity is not stable. If `TrustProxy` is enabled, client IP extraction also depends on a trusted `X-Forwarded-For` chain, which means the operator must get the proxy boundary exactly right.

**Recommendation:** add per-username throttling or exponential backoff, and document the proxy assumptions clearly.

### 4) WebSocket origin policy intentionally allows empty `Origin`
**Severity: Low to Medium**

The WebSocket upgrader accepts empty `Origin` headers so non-browser clients can connect. When `Origin` is present, the code parses it with `url.ParseRequestURI` and requires exact host equality with `r.Host`. 

**Why it matters:** this is a conscious usability tradeoff. It is fine for local tooling, but on exposed deployments it broadens the set of clients that can attempt an upgrade without an Origin header. The socket layer does add some compensating controls, including per-IP counters and a strict read limit.

**Recommendation:** keep the behavior if CLI tooling is important, but document the tradeoff and consider a stricter mode for internet-facing deployments.

### 5) Storage format detection still relies on content sniffing for legacy records
**Severity: Low to Medium**

The storage layer now writes a one-byte kind tag for new binary records, which is good because it makes the current format deterministic. Legacy JSON is identified by `'{'`, while legacy binary records are still detected by falling through the default case. `readTimestampAt()` similarly peeks at the first bytes of the record to decide how to decode it.

**Why it matters:** the new tagged format is solid, but the fallback path for legacy binary files is still heuristic. A legacy binary payload whose first byte collides with the tag byte or `'{'` could be misclassified. That is mostly a data-integrity risk rather than a remote security bug, but it is still worth tightening if long-lived mixed files are expected.

**Recommendation:** if you expect mixed files to exist for a while, prefer an explicit per-record marker that also covers the old binary path, or restrict the heuristic fallback to a clearly documented migration window.

### 6) Query cache still returns shared sample objects
**Severity: Low**

`QueryRangeWithMeta()` now clones the `HistoryResult` wrapper before returning a cache hit, which avoids sharing the slice header. But it still returns the same `*AggregatedSample` pointers, so a caller that mutates a sample can still poison the cached object.

**Why it matters:** this is a correctness and integrity concern more than a direct security issue. It is fine if the API contract is “treat results as immutable,” but that contract should be explicit.

**Recommendation:** document the immutability expectation, or deep-copy samples before returning cached results.

## Risk rating summary

| Area | Rating | Notes |
|---|---:|---|
| Auth/session hardening | Good | Argon2id, hashed session tokens, strict cookies, `0600` storage |
| Proxy/TLS deployment safety | Medium | Cookie `Secure` depends on correct proxy configuration |
| CSRF / browser vs API ergonomics | Medium | Strong for browser flows, awkward for non-browser clients |
| WebSocket abuse resistance | Good | Origin checks, connection caps, read limits |
| Storage integrity | Medium | New tagged format is good, legacy heuristics still exist |
| Frontend XSS posture | Good | Escaping is used, no obvious eval-style hazards in the reviewed file |

## Recommendations, prioritized

1. Make the session cookie `Secure` behavior unambiguous and safer by default. 
2. Separate browser-origin CSRF enforcement from bearer-token API access. 
3. Add per-user throttling or backoff in addition to IP rate limiting. 
4. Decide whether empty-Origin WebSocket clients are acceptable for your threat model and document the choice. 
5. Tighten legacy storage record detection if mixed-format files will live for a while.

## Final assessment

This is a well-built early-stage monitoring system with a respectable security baseline. The code is already doing many of the right things, and the remaining issues are mostly about deployment boundaries, origin policy, and long-tail correctness rather than glaring exploit primitives. In practice, that is a good place to be: the castle has walls, but a few drawbridges still need labels.
