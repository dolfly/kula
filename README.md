# Kula-Szpiegula: Lightweight Linux Server Monitoring Tool

A standalone, self-contained server monitoring tool written in Go. No external databases — it uses an embedded tiered binary storage engine. Provides both a terminal-based TUI for live overview and a premium Web UI with detailed graphs and history.

## Architecture

```
Collectors → Ring-Buffer Storage → Web API / WebSocket → Dashboard
     ↓
    TUI
```

- **Collectors**: Read directly from `/proc` and `/sys` every second.
- **Ring-Buffer Storage**: Custom binary ring-buffer files per tier (1s, 1m, 5m resolution). Pre-allocated files with circular overwrites ensure predictable disk usage.
- **Web Backend**: HTTP/WebSocket server with session-based authentication using Whirlpool hashing with salt.
- **Web Frontend**: Premium dashboard SPA built with Chart.js, custom SVG gauges, and live WebSocket streaming.
- **TUI**: Modern terminal dashboard using `bubbletea` and `lipgloss`.

## Features & Metrics Collected

### Real-time Monitoring
- **CPU**: Per-core and total usage (user, nice, system, idle, iowait, irq, softirq, steal, guest).
- **Load Average**: 1, 5, and 15 minute averages; running and total tasks.
- **Memory**: Total, free, available, used, buffers, cached, reclaimable, shmem, dirty, mapped.
- **Swap**: Total, free, used, cached.
- **Network**: Per-interface throughput (Mbps), packets, errors, drops; TCP/UDP/ICMP counters (IPv4+IPv6); socket stats.
- **Disks**: Per-device I/O (reads/writes, utilization); filesystem usage and inode stats.
- **System**: Uptime, entropy, clock sync status, hostname, logged-in users.
- **Processes**: Task counts by state (running, sleeping, blocked, zombie); total threads.
- **Self-Monitoring**: Tool's own CPU%, RSS/VMS memory, threads, and file descriptors.

### Data Retention Tiers
Configurable storage limits for tiered data:
- **Tier 1**: 1-second resolution (default 100MB).
- **Tier 2**: 1-minute aggregation (default 200MB).
- **Tier 3**: 5-minute aggregation (default 200MB).

## Implementation Status

| Phase | Milestone | Status |
|-------|-----------|--------|
| **1** | Foundation (Structure, Config, Collectors, Storage) | [x] |
| **2** | TUI (Terminal Dashboard) | [x] |
| **3** | Web Backend (API, WebSocket, Whirlpool Auth) | [x] |
| **4** | Web Frontend (Gauges, History Graphs, Live Updates) | [x] |
| **5** | Verification (Build, API, Stability) | [x] |

*Current Binary Size: ~11MB (static ELF 64-bit)*

## Setup and Usage

### 1. Build from Source
```bash
go build -o kula ./cmd/kula/
```

### 2. Configuration
Copy the example configuration and adjust paths:
```bash
cp config.example.yaml config.yaml
# Edit config.yaml - ensure storage.directory is writable
```

### 3. Generate Password Hash (Optional)
If authentication is enabled in `config.yaml`:
```bash
./kula hash-password
# Follow prompts to generate hash and salt for your config
```

### 4. Run the Tool
**Serve Mode (Web UI + Collection):**
```bash
./kula --config=config.yaml serve
# Access at http://localhost:8080
```

**TUI Mode:**
```bash
./kula --config=config.yaml tui
```

## Project Structure

```
kula-szpiegula/
├── cmd/kula/main.go           # CLI Entry point
├── internal/
│   ├── config/                # YAML configuration logic
│   ├── collector/             # /proc and /sys metric collectors
│   ├── storage/               # Tiered ring-buffer storage engine
│   ├── tui/                   # bubbletea terminal dashboard
│   └── web/                   # HTTP server, WebSocket, and Auth (Whirlpool)
│       └── static/            # Embedded Dashboard SPA (HTML/CSS/JS)
├── config.example.yaml        # Template configuration
└── go.mod
```
