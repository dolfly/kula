# CLI Reference

Kula is a single binary with a handful of subcommands. Running it with no command defaults to
`serve`.

```
kula [flags] [command]
```

## Commands

| Command | Description |
|---------|-------------|
| `serve` | Start the monitoring daemon with the web UI (default). |
| `tui` | Launch the terminal UI dashboard. |
| `hash-password` | Generate an Argon2id password hash + salt for config. |
| `inspect` | Display information about the storage tier files. |

### `kula serve`

The default. Starts the collector, storage engine, Landlock sandbox, web server, and
collection loop. Writes one sample per second (or per `collection.interval`) and broadcasts to
WebSocket clients.

```bash
kula              # same as: kula serve
kula serve
```

Graceful shutdown on `SIGINT` / `SIGTERM` (flushes and closes storage with a 5-second
timeout).

### `kula tui`

Self-contained terminal dashboard. Does not require `serve` to be running and does not touch
the storage files. See [Terminal UI](06-tui.md).

```bash
kula tui
```

### `kula hash-password`

Prompts for a password (masked with asterisks) and prints an Argon2id hash and salt using the
parameters from `web.auth.argon2` in your config. Paste the output into `config.yaml`. See
[Authentication](07-authentication.md).

```bash
kula hash-password
```

### `kula inspect`

Prints per-tier statistics for the storage files in `storage.directory`:

```bash
kula inspect
```

For each `tier_N.dat` it reports: format version, data size used vs. max (with percentage),
write offset, total records, oldest and newest timestamps, whether the ring buffer has
wrapped, and the total time range covered.

## Flags

| Flag | Description |
|------|-------------|
| `-config <path>` | Path to the configuration file (default `config.yaml`). If the path is given explicitly but does not exist, Kula seeds it from the packaged example config (logging a warning) instead of failing. |
| `-version`, `-v` | Print version and exit. |
| `-h`, `--help` | Show usage. |

## Environment variables

These override the corresponding config fields (see [Configuration](04-configuration.md)):

| Variable | Overrides |
|----------|-----------|
| `KULA_DIRECTORY` | `storage.directory` |
| `KULA_LOGLEVEL` | `web.logging.level` |
| `KULA_LISTEN` | `web.listen` |
| `KULA_PORT` | `web.port` |
| `KULA_UNIX_SOCKET` | `web.unix_socket` |
| `KULA_MOUNTS_DETECTION` | `collection.mounts_detection` |
| `KULA_BASE_PATH` | `web.base_path` |
| `KULA_POSTGRES_PASSWORD` | `applications.postgres.password` |

## Examples

```bash
# Start with a custom config
kula -config /etc/kula/config.yaml

# Quick run on a specific address/port without editing config
KULA_LISTEN=127.0.0.1 KULA_PORT=9000 kula

# Inspect storage health
kula inspect

# Generate a password hash
kula hash-password
```

## Bash completion & man page

- Bash completion: [`addons/bash-completion/kula`](../../addons/bash-completion/kula).
- Man page: [`addons/man/kula.1`](../../addons/man/kula.1) (installed by the distro packages).

Next: [Service Management](15-service-management.md).
