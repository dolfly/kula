# Kula-Szpiegula: Lightweight Linux Server Monitoring Tool

A standalone, self-contained server monitoring tool written in Go. No external databases — uses an embedded tiered binary storage engine. Provides both a TUI for live overview and a Web UI with detailed graphs and history.

## User Review Required

> [!IMPORTANT]
> **Technology Choice: Go (Golang)** — Compiles to a single static binary, minimal memory footprint (~10-20MB), excellent `/proc` and `/sys` parsing, built-in HTTP server, goroutine-based concurrency. No runtime dependencies.

> [!IMPORTANT]
> **Storage Engine** — Custom binary ring-buffer files per tier. No SQLite, no InfluxDB. Each metric sample is a compact binary record. Storage files are pre-allocated and overwritten in a circular fashion. This gives predictable disk usage matching your tier size limits (100MB / 200MB / 200MB).

> [!IMPORTANT]
> **Web UI Charts** — Using **uPlot** (lightweight ~35KB) for time-series graphs and a custom SVG gauge component. No heavy chart libraries. All assets embedded in the Go binary via `embed`.

> [!WARNING]
> **Auth** — Simple token-based auth with bcrypt password hashing. Config file stores the bcrypt hash. Not full OAuth/OIDC — just username/password login with a session cookie. Is this sufficient?

## Architecture

```
┌─────────────┐     ┌──────────────┐     ┌─────────────┐
│  Collectors  │────▶│  Ring Store   │────▶│  Web API    │──▶ WebSocket/REST
│  (1s tick)   │     │  (3 tiers)   │     │  + Auth     │
└─────────────┘     └──────────────┘     └─────────────┘
       │                                        │
       ▼                                        ▼
 ┌───────────┐                           ┌────────────┐
 │   TUI     │                           │  Web UI    │
 │ (bubbletea)│                           │ (embedded) │
 └───────────┘                           └────────────┘
```

## Proposed Changes

### Project Structure

```
kula-szpiegula/
├── cmd/kula/main.go           # Entry point
├── internal/
│   ├── config/config.go       # YAML config parsing
│   ├── collector/
│   │   ├── collector.go       # Collector orchestrator
│   │   ├── cpu.go             # CPU stats from /proc/stat
│   │   ├── memory.go          # RAM from /proc/meminfo
│   │   ├── swap.go            # Swap from /proc/meminfo + /proc/swaps
│   │   ├── loadavg.go         # Load average from /proc/loadavg
│   │   ├── network.go         # Network from /proc/net/dev, /proc/net/snmp, etc.
│   │   ├── disk.go            # Disk from /proc/diskstats, /proc/mounts, statvfs
│   │   ├── process.go         # Process stats from /proc/[pid]/stat
│   │   ├── system.go          # Uptime, entropy, clock sync, users
│   │   └── self.go            # Self-monitoring (own CPU/mem)
│   ├── storage/
│   │   ├── store.go           # Tiered ring-buffer storage engine
│   │   ├── tier.go            # Single tier implementation
│   │   └── codec.go           # Binary encoding/decoding
│   ├── tui/
│   │   └── tui.go             # Terminal UI with bubbletea
│   └── web/
│       ├── server.go          # HTTP server, routes, middleware
│       ├── api.go             # REST API handlers
│       ├── websocket.go       # WebSocket live streaming
│       └── auth.go            # Authentication
├── web/
│   ├── index.html             # Dashboard SPA
│   ├── app.js                 # Frontend logic
│   └── style.css              # Styles
├── config.example.yaml        # Example config
├── go.mod
└── go.sum
```

---

### Configuration

#### [NEW] config.example.yaml

YAML config with sections for:
- **Storage**: tier sizes (100MB/200MB/200MB), data directory path
- **Web**: listen address, port, auth enable/disable, username, bcrypt password hash
- **TUI**: refresh rate
- **Collection**: interval (default 1s), enabled collectors

#### [NEW] config.go

Parse YAML config, apply defaults, validate settings.

---

### Metric Collectors

All collectors read directly from `/proc` and `/sys` — no shelling out, no cgo.

#### [NEW] collector.go

Orchestrator that ticks every second, calls all sub-collectors, and produces a unified `Sample` struct.

#### [NEW] cpu.go

Parses `/proc/stat` for per-core and total CPU: user, nice, system, idle, iowait, irq, softirq, steal, guest.

#### [NEW] memory.go

Parses `/proc/meminfo` for: MemTotal, MemFree, MemAvailable, Buffers, Cached, SReclaimable, SUnreclaim, SwapTotal, SwapFree, SwapCached, Dirty, Writeback, Shmem, etc.

#### [NEW] network.go

- `/proc/net/dev` — per-interface rx/tx bytes, packets, errors, drops
- `/proc/net/snmp` — TCP/UDP/ICMP counters
- `/proc/net/snmp6` — IPv6 counters
- `/proc/net/sockstat` and `/proc/net/sockstat6` — socket counts

#### [NEW] disk.go

- `/proc/diskstats` — reads, writes, io time, weighted io time
- `/proc/mounts` + `syscall.Statfs` — filesystem usage (total, used, available, inodes)

#### [NEW] system.go

- `/proc/uptime` — uptime
- `/proc/sys/kernel/random/entropy_avail` — entropy
- `adjtimex` syscall or `/sys/devices/system/clocksource` — clock sync status
- `/var/run/utmp` — logged-in users

#### [NEW] process.go

- `/proc/stat` — processes running, blocked
- `/proc/loadavg` — running/total tasks
- Count processes by state (R, S, D, Z, T, etc.)

#### [NEW] self.go

Read own `/proc/self/stat` and `/proc/self/status` for self CPU and memory.

---

### Storage Engine

#### [NEW]  store.go

Manages three tiers. On each new sample:
1. Write to Tier 1 (1-second resolution)
2. Every 60 samples, aggregate and write to Tier 2 (1-minute)
3. Every 5 Tier-2 samples, aggregate and write to Tier 3 (5-minute)

Each tier is a ring-buffer file with a fixed max size. Aggregation computes min/max/avg for numeric fields.

#### [NEW] tier.go

Single tier: memory-mapped or sequential file with header (write position, count) and data region. Records have a fixed-size header (timestamp + length) and variable-length gob/msgpack-encoded payload.

#### [NEW] codec.go

Binary serialization. Uses Go's `encoding/binary` + simple compression. Each sample serialized to ~500-2000 bytes depending on number of cores/interfaces/disks.

---

### TUI

#### [NEW] tui.go

Uses `bubbletea` + `lipgloss` for a clean terminal dashboard showing:
- CPU bars (total + per-core)
- Memory/Swap bars
- Load average
- Network throughput (rx/tx Mbps)
- Disk I/O
- Top processes
- Uptime, users, entropy

Updates every second from the collector.

---

### Web Backend

#### [NEW] server.go

HTTP server using `net/http`. Embeds the `web/` static assets via `go:embed`. Serves the dashboard SPA.

#### [NEW] api.go

REST endpoints:
- `GET /api/current` — latest sample
- `GET /api/history?from=&to=&resolution=` — historical data from appropriate tier
- `GET /api/config` — non-sensitive config info (retention, etc.)

#### [NEW] websocket.go

WebSocket endpoint at `/ws` for live streaming. Sends new samples every second to connected clients. Supports pause/resume commands from clients.

#### [NEW] auth.go

Optional authentication:
- `POST /api/login` — validate credentials, set session cookie
- Middleware to check session on all API/WS endpoints
- Sessions stored in memory (simple map with expiry)

---

### Web Frontend (Embedded SPA)

#### [NEW] index.html

Single-page dashboard with:
- **Top row**: Gauge charts for CPU%, RAM%, SWAP%, Load Avg, Network DL/UP speed
- **Below**: Detailed time-series graphs for all metrics
- Login form (shown when auth enabled)
- Time range controls with presets

#### [NEW] app.js

- WebSocket connection management with auto-reconnect
- uPlot chart initialization and live updates
- Gauge rendering (custom SVG)
- Time range selection (presets + graph brush selection synced across all charts)
- Pause/resume toggle
- Auth: login form, session management

#### [NEW] style.css

Dark theme, responsive grid layout, subtle animations. Premium look with:
- Dark background with subtle gradients
- Color-coded metrics
- Smooth chart transitions

---

### Entry Point

#### [NEW] main.go

CLI entry point with subcommands:
- `kula` or `kula serve` — start collector + web server (headless/daemon mode)
- `kula tui` — start collector + TUI
- `kula hash-password` — generate bcrypt hash for config

Uses flag parsing (stdlib). Initializes config, storage, collectors, and starts appropriate UI.

---

## Verification Plan

### Build Verification
```bash
go build -o kula ./cmd/kula/
```
Must compile without errors.

### Runtime Smoke Test
```bash
# Run collector and check output
./kula serve &
sleep 3
curl http://localhost:8080/api/current | python3 -m json.tool
# Should return valid JSON with CPU, memory, network data
kill %1
```

### TUI Verification
- Launch `./kula tui` in terminal and verify metrics display
- Verify it updates every second

### Web UI Browser Test
- Launch `./kula serve` and open `http://localhost:8080` in browser
- Verify gauge charts render with live data
- Verify history graphs populate
- Test time range presets
- Test pause/resume
- Test graph brush selection propagates to all charts

### Storage Tier Verification
- Run for >60 seconds, verify Tier 2 aggregation occurs
- Check data directory for storage files, verify sizes are within limits

### Manual User Testing
- Deploy on a Debian-based server
- Verify all metrics match `htop`, `free`, `df`, `ip -s link` output
- Stress test with `stress-ng` and verify graphs reflect load
