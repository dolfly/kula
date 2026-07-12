# Kula code quality, performance, and security review

**Target:** Kula `0.18.6`, commit `0401b589e294` (`2026-07-11`)  
**Review date:** 2026-07-12  
**Scope:** Go services, web UI, storage, sandboxing, collectors, deployment and
release automation, Docker, Ansible, and installer scripts. I intentionally did
not open or use any file in `reviews/`.

## Executive summary

Kula has substantial deliberate security engineering: Argon2id password
hashing, hashed session tokens at rest, CSRF and origin checks, a nonce-based
CSP, WebSocket limits, request-size limits, fuzz tests, a Landlock layer, and a
well-hardened systemd unit. The code is generally readable and the test suite is
substantial.

The largest problem is the deployment posture, not a single parser bug. A
missing configuration starts a TCP service on all IPv4 and IPv6 interfaces with
authentication disabled. The checked-in Docker and Ansible templates preserve
that posture. Therefore an ordinary installation can expose detailed host,
filesystem, network, process, and container telemetry without credentials.

The second major issue is distribution trust: Ansible downloads an old release
without a checksum and disables RPM signature verification, while the public
installation path promotes `curl | bash` from a mutable branch. These paths can
turn a release-hosting compromise into code execution on monitored servers.

There is no demonstrated unauthenticated remote code execution in the web
server in this review. Fixing the high-severity deployment and proxy-boundary
issues should nevertheless be treated as a release priority.

## Scores

| Area | Score | Rationale |
|---|---:|---|
| Security design | 5.5 / 10 | Strong in-process controls, but unsafe exposure defaults, proxy trust, and installer trust undermine them. |
| Code quality | 7.0 / 10 | Clear package boundaries and comments; several configuration and persistence edge cases remain. |
| Performance and resilience | 6.0 / 10 | Efficient hot paths and storage cache exist, but unbounded history work and container fan-out can cause availability problems. |
| Testing and verification | 8.5 / 10 | Race tests, fuzz seeds, runtime security tests, and linting are all present and passed. Important deployment/default paths need tests. |
| Supply-chain and deployment hygiene | 3.5 / 10 | Pinned container base-image digests are good, but unsigned/unverified installer and Ansible flows are high risk. |
| **Overall** | **5.8 / 10** | A promising, well-tested monitor that needs hardening before it is safe to expose beyond a trusted local network. |

### Severity scale

- **Critical:** direct, broadly exploitable compromise with little precondition.
- **High:** material confidentiality, integrity, availability, or installation
  compromise under a realistic deployment condition.
- **Medium:** significant defense-in-depth or conditional weakness.
- **Low:** limited impact, hardening, or reliability defect.

## Findings at a glance

| ID | Severity | Area | Title |
|---|---|---|---|
| KULA-SEC-01 | **High** | Exposure | Missing configuration launches an unauthenticated wildcard service. |
| KULA-SEC-02 | **High** | Supply chain | Deployment automation installs unauthenticated, stale artifacts as root. |
| KULA-SEC-03 | **High** | Proxy trust | `trust_proxy` trusts attacker-controlled `X-Forwarded-For` from any peer. |
| KULA-SEC-04 | **High, conditional** | Local security | An insecure storage directory permits persisted-session forgery and symlink attacks. |
| KULA-SEC-05 | **Medium** | SSRF | Ollama loopback validation is bypassable through HTTP redirects. |
| KULA-SEC-06 | **Medium** | Configuration | Unknown YAML and unsafe values are accepted without security validation. |
| KULA-SEC-07 | **Medium** | Availability | History requests have no concurrency/rate limit and block storage writes while scanning. |
| KULA-SEC-08 | **Medium** | Transport security | Remote database monitoring defaults to, or cannot configure, authenticated TLS. |
| KULA-SEC-09 | **Medium** | Privilege/performance | Default container monitoring grants a Docker-equivalent socket and has unbounded fan-out. |
| KULA-SEC-10 | **Medium** | Parser robustness | Tier-file metadata and count fields can drive excessive allocation after local corruption. |
| KULA-QUAL-01 | **Medium** | Correctness | Storage accepts arbitrary tier counts but writes only the first three tiers. |
| KULA-QUAL-02 | **Low** | Secrets/automation | Password fallback can echo input; shell scripts contain verified correctness defects. |

## Detailed findings

### KULA-SEC-01 — High: Missing configuration launches an unauthenticated wildcard service

**Evidence**

- `cmd/kula/main.go:57-82` uses `config.Load` when `-config` was not
  explicitly supplied. A missing default `config.yaml` is therefore accepted.
- `internal/config/config.go:327-345` defaults web serving to enabled, UI to
  enabled, `listen` to `""`, and authentication to disabled.
- `internal/web/server.go:537-548` interprets an empty listen address as both
  `0.0.0.0:<port>` and `[::]:<port>`.
- `config.example.yaml:76-86,196-198`,
  `addons/ansible/roles/kula/templates/config.yaml.j2:44-55,115-127`, and
  `addons/docker/Dockerfile:30-34` retain the same public/unauthenticated
  posture. The Docker Compose example uses host networking.

**Impact**

Any network peer able to reach the port can retrieve `/api/current`,
`/api/history`, `/api/config`, and a live WebSocket stream. These expose host
and kernel information, process counts, mount points, network interfaces,
filesystem capacity, and potentially container names and resource use. The
default container collector is enabled. A firewall may reduce reachability, but
the application itself makes no safe assumption.

**Recommendation**

Make public exposure an explicit opt-in. The safest compatibility path is:

1. Bind to loopback by default (`127.0.0.1` and `::1`) or use a Unix socket.
2. Require a readable config file for `serve`; do not silently start defaults.
3. Refuse a non-loopback TCP listener unless authentication is configured, or
   require an explicit `web.allow_unauthenticated_remote: true` acknowledgement.
4. Make the Docker and Ansible templates loopback-only unless the operator
   explicitly configures reverse-proxy/TLS/authentication settings.

For example, validate the effective configuration before listener creation:

```go
func validateWebExposure(w WebConfig) error {
	if !w.Enabled || w.UnixSocket != "" || isLoopbackListen(w.Listen) {
		return nil
	}
	if !w.Auth.Enabled {
		return errors.New("refusing unauthenticated non-loopback web listener; " +
			"enable web.auth, bind to loopback, or explicitly acknowledge remote exposure")
	}
	return nil
}
```

Add integration tests for a missing `config.yaml`, an empty `web.listen`, the
Docker default, and a public listener with auth disabled.

---

### KULA-SEC-02 — High: Deployment automation installs unauthenticated, stale artifacts as root

**Evidence**

- `addons/ansible/populate_files.sh:4-5` downloads hard-coded `0.16.0` DEB and
  RPM files with no checksum or signature verification. The repository version
  is `0.18.6`.
- `addons/ansible/roles/kula/tasks/main.yaml:15-22` installs the RPM as root
  with `disable_gpg_check: true`.
- `README.md:143-147` and `landing/index.html:145` promote executing an
  installer fetched from the mutable `main` branch.
- `addons/install.sh:87-139` calculates and displays a hash but does not
  compare it with an expected trusted value. `install_v2.sh` compares release
  artifacts with a checksum downloaded from the same release location, which
  detects accidental corruption but is not an independent authenticity anchor.

**Impact**

Compromise of the release account, release asset, raw GitHub branch, or a
trusted TLS endpoint can result in execution as root during Ansible/package
installation. Deploying an outdated binary also bypasses fixes made after the
hard-coded release.

**Recommendation**

- Publish signed release artifacts and signed checksums (for example,
  Minisign, GPG with a pinned fingerprint, or Sigstore/cosign with issuer and
  identity policy).
- Pin installation to an immutable release tag/commit and verify the installer
  before executing it. Do not make `curl | bash` the primary documented path.
- Replace `populate_files.sh` with Ansible `get_url` using a pinned SHA-256 or
  a verified signature. Keep version and checksums in reviewed role defaults.
- Sign RPMs or install from a signed repository; remove
  `disable_gpg_check: true`.
- Delete or clearly deprecate `addons/install.sh` so users do not select its
  weaker verification flow by filename.

An Ansible artifact download should fail closed, for example:

```yaml
- name: Fetch the reviewed Kula RPM
  ansible.builtin.get_url:
    url: "{{ kula_rpm_url }}"
    dest: /var/tmp/kula.rpm
    mode: "0644"
    checksum: "sha256:{{ kula_rpm_sha256 }}"

# Prefer a repository/package signature. Do not disable RPM verification.
- name: Install verified Kula package
  ansible.builtin.dnf:
    name: /var/tmp/kula.rpm
    state: present
```

---

### KULA-SEC-03 — High: `trust_proxy` accepts spoofed `X-Forwarded-For` from every peer

**Evidence**

`internal/web/server.go:1059-1074` uses the rightmost `X-Forwarded-For` value
whenever `web.trust_proxy` is true. It does not first establish that the TCP
peer is a configured, trusted proxy. This value controls:

- the login IP rate limiter (`internal/web/server.go:811-836`),
- Ollama chat/meta rate limiters (`internal/web/ollama.go:245-250,727-732`),
- WebSocket per-IP limits (`internal/web/websocket.go:68-86`).

The project’s own scanner contains a specific XFF-bypass check
(`cmd/kula-scan/checks_bypass.go`), confirming this is a recognized boundary.

**Impact**

If the service remains directly reachable while `trust_proxy: true`, one client
can rotate an XFF value to bypass IP throttles and per-IP WebSocket limits. The
per-username limiter still slows attacks against one known username, but random
usernames can consume Argon2 work and a single peer can occupy the global
WebSocket allocation. This is a practical availability issue and weakens login
abuse protection.

**Recommendation**

Replace the Boolean with an explicit trusted-proxy CIDR list. Use forwarding
headers only when the immediate `RemoteAddr` belongs to that list; parse proxy
chains from right to left and select the first non-trusted hop. Direct peers
must always use their socket address.

```go
func clientIP(r *http.Request, trusted []*net.IPNet) net.IP {
	peer := parseRemoteAddr(r.RemoteAddr)
	if !contains(trusted, peer) {
		return peer // Never accept XFF from a direct client.
	}
	for _, ip := range xffRightToLeft(r.Header.Values("X-Forwarded-For")) {
		if !contains(trusted, ip) {
			return ip
		}
	}
	return peer
}
```

Document that the Kula port must be firewalled to the reverse proxy, and add an
integration test proving that a direct peer cannot change its limiter key with
XFF.

---

### KULA-SEC-04 — High, conditional: Writable storage permits persisted-session forgery and file-target attacks

**Evidence**

- `internal/config/config.go:779-796` only creates/checks the special default
  directory. A custom storage directory is not checked for ownership, symlinks,
  or group/world writability.
- `internal/storage/store.go:92-125` creates tier files using path-based
  operations. `internal/storage/tier.go:69` opens them with
  `os.O_RDWR|os.O_CREATE` and follows symlinks.
- `internal/web/auth.go:337-364` loads arbitrary `sessions.json` entries into
  the authenticated-session map. It accepts a stored token hash as authoritative.
- `internal/web/auth.go:367-392` saves through `os.WriteFile`, which follows
  an existing symlink and is not atomic.

**Impact**

This requires a local attacker who can write the selected storage directory,
not the normal default installation with a protected directory. In that
condition, the attacker can write a `sessions.json` record containing the hash
of a token they chose and a future expiry, then obtain an authenticated web
session after restart. Path-following writes also enable corruption or clobber
of files accessible to the Kula process via symlink races. The impact is higher
when Kula is run manually as root.

**Recommendation**

Treat the storage root as a security boundary:

1. `Lstat` it and reject non-directories, symlinks, group/world writable modes,
   and unexpected owners before opening any child.
2. Use descriptor-relative, no-follow opens (`openat2` with
   `RESOLVE_BENEATH|RESOLVE_NO_SYMLINKS` on Linux) for tiers, sessions, and
   backups.
3. Write sessions atomically: secure temporary file, `fsync`, `rename`, then
   `fsync` the parent directory. Explicitly `Chmod(0600)` existing files.
4. Separate the writable custom-metrics socket directory from the private data
   directory if a group must write to the socket.

```go
func writeSessionsAtomically(dir string, data []byte) error {
	f, err := os.CreateTemp(dir, ".sessions-*")
	if err != nil { return err }
	tmp := f.Name()
	defer os.Remove(tmp)
	if err := f.Chmod(0o600); err != nil { f.Close(); return err }
	if _, err := f.Write(data); err != nil { f.Close(); return err }
	if err := f.Sync(); err != nil { f.Close(); return err }
	if err := f.Close(); err != nil { return err }
	return os.Rename(tmp, filepath.Join(dir, "sessions.json"))
}
```

Add tests for insecure directory modes, session-file symlinks, malformed session
records, and a session write interrupted between replacement steps.

---

### KULA-SEC-05 — Medium: Ollama loopback SSRF validation does not survive redirects

**Evidence**

`internal/config/config.go:588-604` validates the configured Ollama URL’s
initial hostname as `localhost`, `127.0.0.1`, or `::1`. However,
`internal/web/ollama.go:447-448`, `552`, `734`, and `776` construct ordinary
`http.Client` values without a `CheckRedirect` policy. Go follows redirects by
default. A loopback service can return a 307/308 redirect to another host,
causing Kula to resend the chat request (including user prompt and metrics
context) outside loopback.

The Apache collector explicitly disables redirects at
`internal/collector/apache2.go:33-38`; the Ollama and Nginx clients do not.

**Impact**

The advertised SSRF protection can be bypassed by a malicious or compromised
local AI backend. On kernels where Landlock network restrictions are unavailable
or where the redirect uses an allowed port, this can reach internal services or
exfiltrate the proxied chat body.

**Recommendation**

Reject redirects for the Ollama client and defend against DNS/transport changes
at connection time as well as parse time. Reuse one hardened client builder for
all local-only integrations.

```go
func newLocalAIClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}
```

Return a controlled upstream error for 3xx responses. Add tests with a loopback
test server that redirects to a second server and assert it receives no request.

---

### KULA-SEC-06 — Medium: Configuration parsing and validation can silently create unsafe deployments

**Evidence**

- `internal/config/config.go:428-432` uses `yaml.Unmarshal`, which ignores
  unknown fields. A typo such as `enable: true` under `web.auth` is silently
  ignored, leaving authentication disabled by default.
- YAML `web.port` is not constrained to `1..65535`. `port: 0` creates an
  ephemeral listener while `internal/sandbox/sandbox.go:45-48,105-112` treats
  port zero as no TCP binding rule.
- Authentication settings have no validation for a nonempty hash/salt or safe
  Argon2 parameters. Session duration and WebSocket limits also lack bounds.

**Impact**

Typos and unsafe values become runtime behavior instead of startup failures.
This is especially risky because the secure deployment settings are opt-in and
the default web service is public and unauthenticated.

**Recommendation**

Decode with known-field enforcement and validate the effective configuration
after environment overrides:

```go
dec := yaml.NewDecoder(bytes.NewReader(data))
dec.KnownFields(true)
if err := dec.Decode(cfg); err != nil {
	return nil, fmt.Errorf("parsing config: %w", err)
}

if cfg.Web.Port < 1 || cfg.Web.Port > 65535 {
	return nil, fmt.Errorf("web.port must be 1..65535")
}
if cfg.Web.Auth.Enabled &&
	(cfg.Web.Auth.PasswordHash == "" || cfg.Web.Auth.PasswordSalt == "" ||
	 cfg.Web.Auth.Argon2.Time == 0 || cfg.Web.Auth.Argon2.Threads == 0) {
	return nil, errors.New("enabled web.auth requires a complete, safe Argon2 configuration")
}
```

Validate all security-sensitive URLs, socket modes, CIDRs, resource limits, and
database transport settings. Add negative tests for every validation rule.

---

### KULA-SEC-07 — Medium: History queries have an avoidable availability amplification path

**Evidence**

- `internal/web/server.go:664-730` accepts a 31-day window and up to 5,000
  target points but has no API-wide concurrency limit or rate limit.
- `internal/storage/store.go:315-431` holds the store read lock during tier
  selection, disk scanning, decoding, aggregation, and cache handling.
- A concurrent `WriteSample` requires the store write lock
  (`internal/storage/store.go:203-272`), so long history reads delay
  collection/storage writes.
- Varying `from`, `to`, or `points` bypasses the short-lived query cache. The
  optional gzip middleware also spends CPU compressing the response.

**Impact**

An unauthenticated default deployment, or any authenticated but untrusted
dashboard user, can issue distinct expensive history requests concurrently.
This can consume CPU and memory, cause disk I/O, delay the collection path, and
degrade every dashboard user. The current point cap does not bound scan cost or
concurrent work.

**Recommendation**

- Add a bounded semaphore for history work, with small per-IP/user and global
  quotas.
- Enforce a response-size and decoded-record budget, not only a requested
  point count.
- Snapshot the tier references under the store lock and release the store lock
  before a long read where consistency permits.
- Add `ReadHeaderTimeout` and a deliberately small `MaxHeaderBytes` to the
  HTTP server; `ReadTimeout` alone leaves slow header connections alive for
  30 seconds.
- Cache normalized windows and return `429`/`503` promptly when saturated.

```go
select {
case historySem <- struct{}{}:
	defer func() { <-historySem }()
default:
	jsonError(w, "history service busy", http.StatusTooManyRequests)
	return
}
```

Load-test concurrent, cache-bypassing history requests and assert collection
latency remains below the configured interval.

---

### KULA-SEC-08 — Medium: Remote database monitoring lacks secure transport defaults

**Evidence**

- PostgreSQL defaults to `sslmode: "disable"`
  (`internal/config/config.go:383-390`).
- MySQL configuration has no TLS fields. `internal/collector/mysql.go:38-45`
  constructs a DSN with only `timeout` and `readTimeout`.
- Both integrations accept a TCP host/port configuration, so they are not
  restricted to loopback deployments.

**Impact**

When an operator points either collector at a remote database, credentials and
monitoring traffic can traverse the network without authenticated encryption.
This is conditional on remote use; default modules are disabled and point to
localhost.

**Recommendation**

- Default TCP PostgreSQL to `verify-full`; make plaintext explicit and loudly
  acknowledged only for loopback/private Unix sockets.
- Add MySQL TLS mode, CA, client certificate, and server-name configuration
  through `mysql.Config`/registered TLS configs rather than concatenating a
  DSN string.
- Use structured driver configuration for both database clients to avoid
  reserved-character/parameter-injection bugs in user, host, database, and
  password fields.

---

### KULA-SEC-09 — Medium: Container integration expands privilege and has unbounded per-container work

**Evidence**

- Container monitoring is enabled by default
  (`internal/config/config.go:379-382`).
- `internal/sandbox/sandbox.go:143-162` permits read/write access to the
  Docker/Podman socket. Docker’s normal API socket is effectively root-equivalent
  even when this code presently makes only GET requests.
- `internal/collector/containers.go:274-290` processes every running container
  and `readNetIO` makes an individual inspect request for each one
  (`internal/collector/containers.go:451-472`). There is no configured maximum,
  batching, or bounded worker pool.
- `c.ID[:12]` at `internal/collector/containers.go:280` trusts a response to
  contain at least 12 bytes and can panic on a malformed Unix-socket response.

**Impact**

Any future code-execution flaw in Kula has a straightforward path to full host
control if the Docker socket is available. Separately, a large container fleet
or a slow runtime can make a collection pass take roughly one timeout per
container, create oversized samples, and overwhelm the UI with three charts per
container.

**Recommendation**

- Make socket-based container monitoring opt-in; prefer cgroup-only mode by
  default.
- If names/API data are required, use a narrowly scoped read-only Docker API
  proxy rather than the host Docker socket.
- Cap monitored containers, validate all daemon response fields, and use a
  bounded worker pool or a batched inspect strategy.
- Guard ID shortening: `short := id[:min(12, len(id))]`.

This is primarily an attack-surface reduction finding, not a claim that the
current GET requests themselves escape the Docker API.

---

### KULA-SEC-10 — Medium: Corrupt tier metadata can cause excessive allocations

**Evidence**

- `internal/storage/tier.go:118-127` replaces the configured `maxData` with
  the value stored in the on-disk header without checking it against the
  configured size, file size, or safe integer range.
- `internal/storage/tier.go:456-461` allocates `dataLen` bytes after a header
  check based on that potentially untrusted `maxData`.
- `internal/storage/codec.go:814-818`, `862-864`, `883-885`, and similar
  sections allocate slice capacity from `uint16` count fields before proving
  enough bytes remain for even the minimum representation.

**Impact**

A corrupted or locally attacker-controlled tier can trigger large allocations
or repeated expensive parsing during startup/history queries. This does not
create a remote upload path by itself; it compounds KULA-SEC-04 and affects
recovery from disk corruption.

**Recommendation**

Validate file metadata against the configured tier size before accepting it:

- require `header.maxData == configuredMaxData` (or an explicit migration
  policy),
- check every offset is within `[0, maxData]`,
- ensure record length fits both the file and a strict per-record maximum,
- bound each count by `remainingBytes / minimumEntrySize` before allocating,
- add corruption tests for oversized `maxData`, offsets, record lengths, and
  every variable-section count.

The existing codec fuzz tests are a good foundation; extend them with
allocation-budget assertions.

---

### KULA-QUAL-01 — Medium: Config permits more tiers than the write path supports

**Evidence**

`internal/config/config.go:632-711` validates any ascending tier hierarchy.
However, `internal/storage/store.go:32-45` stores only `ratio1` and `ratio2`,
and `WriteSample` at `internal/storage/store.go:224-266` only aggregates tier
0 to tier 1 and tier 1 to tier 2. `NewStore` still creates every configured
tier file.

**Impact**

An operator can configure a fourth tier that is never written. This is a data
retention/capacity correctness defect and makes configuration validation
misleading.

**Recommendation**

Either reject configurations with more than three tiers, or replace the fixed
fields with a generic per-tier aggregation state machine and test four-plus
tier writes, restart reconstruction, and queries.

---

### KULA-QUAL-02 — Low: Secret-input fallback and shell automation need cleanup

**Evidence**

- `cmd/kula/main.go:262-267` falls back to `bufio.Reader` if terminal raw mode
  cannot be entered. On an interactive terminal in that state, the password
  may echo.
- ShellCheck reports `SC2145` for `addons/docker/run.sh:31`: `"$@"` is joined
  to `--name` due to the line continuation, so forwarded Docker arguments can
  be malformed.
- ShellCheck reports `SC2164` in the three Ansible helper scripts because an
  unchecked `cd` can leave commands executing in an unintended directory.

**Recommendation**

Refuse interactive password entry when echo cannot be disabled, or use a
well-tested no-echo terminal read API. Fix the Docker invocation and fail
closed on directory changes:

```bash
cd "$(dirname "$0")/../.." || exit 1
docker run --rm -it "$@" \
  --name kula \
  --pid host \
  --network host \
  -v /proc:/proc:ro \
  "$IMAGE"
```

## Positive controls worth preserving

- Argon2id password hashing, random salt/token generation, hash-only session
  persistence, and constant-time comparison are implemented in
  `internal/web/auth.go`.
- State-changing browser requests receive origin/referrer validation and a
  synchronizer CSRF token by default.
- Templates use `html/template`; the base-path and game-score URL validators
  explicitly address browser/parser ambiguity. The game score request omits
  credentials, redirects, and referrer information.
- CSP nonces, SRI attributes, anti-framing headers, WebSocket size/connection
  controls, request body caps, and static-path handling are all present.
- Storage format tests cover migrations, wrapping, unclean shutdown recovery,
  concurrency, fuzzing, and corrupt headers. The code correctly refuses to
  overwrite an unparseable tier header.
- The systemd unit runs as a dedicated unprivileged user with a strong set of
  hardening directives. Landlock is a useful additional layer when the kernel
  supports it; retain clear logging that network restrictions require ABI v4+.

## Recommended remediation order

1. **Before the next release:** resolve KULA-SEC-01 and KULA-SEC-02. A secure
   default listener and a verifiable distribution path have the greatest risk
   reduction.
2. **Next:** resolve KULA-SEC-03 and KULA-SEC-04. These protect abuse limits
   and the authentication boundary in realistic reverse-proxy/local-user
   deployments.
3. **Then:** address KULA-SEC-05 through KULA-SEC-10 and add regression tests.
   Prioritize history-query admission control where the dashboard is exposed.
4. **Maintenance sprint:** fix tier-count correctness, automate ShellCheck,
   remove/deprecate insecure installer paths, and add a deployment-security
   test matrix for bare metal, systemd, Docker, and Ansible.

## Verification performed

The repository was clean at the start of review. The following completed
successfully after allowing Go tooling to use its normal external build cache:

```text
./addons/check.sh
  - govulncheck
  - gofmt
  - go vet
  - go test -v -race ./...
  - golangci-lint run ./...
```

`govulncheck` found **zero vulnerabilities reachable by this code**.

I also ran ShellCheck across repository shell scripts. It found the one
`SC2145` error and three `SC2164` warnings described in KULA-QUAL-02. The
review did not run a live attack scan against a production endpoint and did not
inspect any real `config.yaml` or historical review document.
