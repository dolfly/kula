# Kula Security, Code Quality, and Performance Audit

## Scope

I reviewed the project-authored:

- **Go files**
- **JavaScript files**
- **HTML files**

I also noted the presence of vendored/minified third-party JS bundles under `internal/web/static/js/chartjs/`; I treated them as static dependencies rather than deep-auditing their minified source.

## Scores

| Area | Score | Assessment |
|---|---:|---|
| Security | 7.2/10 | Good baseline hardening, but a few medium-risk exposure and trust-boundary issues remain |
| Performance | 6.9/10 | Fine for small/medium deployments, but several full-scan and allocation-heavy paths will scale poorly |
| Code quality | 8.1/10 | Clear structure, good separation of concerns, and generally readable code |
| Overall | 7.4/10 | Solid and fairly well-hardened, but not yet “internet-hardened” |

**Risk level:** Medium

## Executive Summary

The project has a lot of good security hygiene already:

- **CSP nonce + SRI** on the dashboard templates
- **`html/template`** for server-side rendering
- **Argon2id** password hashing
- **Hashed session tokens**
- **Session file permissions set to `0600`**
- **WebSocket origin checks**
- **Connection limits**
- **Landlock sandboxing**
- **HTTP timeouts**
- **Explicit caps** on history range and points
- **Avoidance of obvious XSS sinks** in most client code

The main weaknesses are not classic exploit chains, but rather:

- **Information exposure** through unauthenticated endpoints
- **Proxy- and IP-dependent session handling**
- **Expensive collection/aggregation paths** that will hurt at scale
- **A few hand-rolled browser-side security primitives** that are good today but brittle over time

## Findings

- **[Medium] Unauthenticated `/metrics` leaks useful reconnaissance data**
  - The Prometheus endpoint is intentionally unauthenticated.
  - It exposes host-level telemetry such as CPU, memory, disks, network, process counts, uptime, entropy, and possibly GPU state.
  - That is acceptable for trusted scrape environments, but risky if the service is reachable from untrusted networks.
  - **Recommendation:** bind metrics to a trusted interface, restrict it with firewall/IP allowlists, or make authentication optional/configurable for that endpoint.

- **[Medium] Session validation is brittle because it binds to IP + User-Agent**
  - `ValidateSession` ties sessions to both IP and User-Agent.
  - This will break for mobile clients, NAT churn, reverse proxies, and some browser/network changes.
  - When `TrustProxy` is enabled, bad proxy configuration can also make IP-based trust weaker than intended.
  - **Recommendation:** make session fingerprinting configurable, avoid treating User-Agent as a strong security factor, and document the proxy trust assumptions very explicitly.

- **[Medium] [/proc](cci:9://file:///proc:0:0-0:0) collection and history aggregation are allocation-heavy**
  - [collectProcesses()](cci:1://file:///home/c0m4r/ai/kula/internal/collector/process.go:9:0-62:1) scans every PID and every task directory each cycle.
  - [ReadRange()](cci:1://file:///home/c0m4r/ai/kula/internal/storage/tier.go:225:0-339:1) and [aggregateSamples()](cci:1://file:///home/c0m4r/ai/kula/internal/storage/store.go:600:0-739:1) perform many per-record allocations and nested loops.
  - On hosts with many processes, threads, or long history windows, this will become CPU- and GC-heavy.
  - **Recommendation:** cache or sample process/thread data less frequently, reduce per-record allocations in storage reads, and use indexed maps for sensor/device aggregation where practical.

- **[Medium] CSRF protection relies on Origin/Referer checks rather than tokens**
  - `CSRFMiddleware` blocks non-GET requests unless Origin/Referer matches `Host`.
  - This is decent defense-in-depth, but it is still weaker than explicit CSRF tokens for state-changing browser actions.
  - It can also be brittle behind proxies or in unusual browser/network conditions.
  - **Recommendation:** keep the origin check, but add token-based CSRF protection for login/logout and other state-changing routes.

- **[Low] Hand-rolled HTML sanitization in [landing.js](cci:7://file:///home/c0m4r/ai/kula/landing/landing.js:0:0-0:0) should be treated as security-sensitive**
  - The translation HTML path uses `DOMParser` plus a custom allowlist.
  - It currently looks reasonably constrained, but custom sanitizers tend to regress over time.
  - **Recommendation:** prefer text-only translations where possible, or replace the sanitizer with a well-tested library such as DOMPurify.

- **[Low] UTF-8 handling in TUI padding helpers is byte-based, not rune-based**
  - [padLeft](cci:1://file:///home/c0m4r/ai/kula/internal/tui/view.go:674:0-679:1) and [padRight](cci:1://file:///home/c0m4r/ai/kula/internal/tui/view.go:667:0-672:1) slice strings by byte length.
  - This can truncate multibyte characters in hostnames, GPU names, or labels and produce misaligned UI output.
  - **Recommendation:** use rune-aware width handling or a library that understands display width.

## Notable Strengths

- **[Strong CSP posture]**
  - The dashboard uses a per-request nonce and strict `script-src`.
  - This materially reduces XSS risk.

- **[Good XSS avoidance in browser code]**
  - Most DOM updates use `textContent`.
  - Where `innerHTML` is used, the content is either static or escaped/sanitized.

- **[Good auth storage hygiene]**
  - Session tokens are hashed before persistence.
  - The session file is written with restrictive permissions.

- **[Operational hardening]**
  - Landlock sandboxing is a strong move for a monitoring agent.
  - The server uses read/write/idle timeouts.
  - WebSocket upgrades are protected by origin checks and connection limits.

- **[Reasonable resource limits]**
  - History range and point counts are capped.
  - WebSocket message sizes are bounded.
  - This prevents a lot of trivial DoS abuse.

## Overall Assessment

The codebase is **reasonably secure and well-structured** for a monitoring application, especially when deployed behind a trusted reverse proxy on a private network.

The biggest practical risks are:

- **public exposure of telemetry endpoints**
- **proxy/session trust assumptions**
- **scalability bottlenecks on busy hosts**

If this is meant for **internal/trusted deployment**, the current posture is fairly good.  
If it may face **untrusted networks**, I would harden the medium-severity items before calling it production-ready.

## Priority Recommendations

1. **Protect `/metrics`**
   - Add allowlisting, authentication, or network binding controls.

2. **Rework session trust**
   - Make IP/User-Agent binding optional and proxy-safe.

3. **Reduce [/proc](cci:9://file:///proc:0:0-0:0) and storage hot-path cost**
   - Especially process scanning and storage range reads.

4. **Add token-based CSRF**
   - Keep origin checks, but don’t rely on them alone.

5. **Replace custom sanitizer if feasible**
   - Lower long-term maintenance risk.

## Final Verdict

- **Security:** Good baseline, medium-risk exposure points remain
- **Performance:** Acceptable now, but not ideal at scale
- **Code Quality:** Strong overall
- **Overall Risk:** **Medium**

**Task complete:** I reviewed the Go/JS/HTML surface and produced a scored markdown audit with severity labels and recommendations.
