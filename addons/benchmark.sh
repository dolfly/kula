#!/usr/bin/env bash
# addons/benchmark.sh — Run the storage engine benchmark suite and pretty-print results.
#
# Usage:
#   bash addons/benchmark.sh [benchtime]
#
# Examples:
#   bash addons/benchmark.sh          # default: 3s per benchmark
#   bash addons/benchmark.sh 5s       # longer run for tighter numbers
#   bash addons/benchmark.sh 10s      # CI / regression baseline

set -euo pipefail

BENCHTIME="${1:-3s}"

# ── colours ──────────────────────────────────────────────────────────────────
BOLD="\033[1m"
DIM="\033[2m"
CYAN="\033[0;36m"
GREEN="\033[0;32m"
YELLOW="\033[0;33m"
BLUE="\033[0;34m"
MAGENTA="\033[0;35m"
RESET="\033[0m"

# ── helpers ───────────────────────────────────────────────────────────────────
cd "$(dirname "$0")/.."

header() {
    local title="$1"
    local width=70
    local pad=$(( (width - ${#title} - 2) / 2 ))
    local line
    line=$(printf '─%.0s' $(seq 1 $width))
    echo -e "\n${CYAN}┌${line}┐${RESET}"
    printf "${CYAN}│${RESET}%*s${BOLD}${YELLOW} %s ${RESET}%*s${CYAN}│${RESET}\n" \
        "$pad" "" "$title" "$pad" ""
    echo -e "${CYAN}└${line}┘${RESET}\n"
}

subheader() {
    echo -e "${BLUE}${BOLD}  ▸ $1${RESET}"
}

divider() {
    echo -e "${DIM}  $(printf '┄%.0s' $(seq 1 66))${RESET}"
}

# Formats a raw `go test -bench -benchmem` line into an aligned, coloured row.
# Handles the optional MB/s column that appears when SetBytes is used.
format_bench_line() {
    local line="$1"
    [[ "$line" =~ ^Benchmark ]] || return 0

    # Use awk to robustly locate the key fields regardless of column count.
    # go test -benchmem output columns:
    #   Name  Iterations  <val> ns/op  [<val> MB/s]  <val> B/op  <val> allocs/op
    local parsed
    parsed=$(echo "$line" | awk '
    {
        name = $1
        # Strip goroutine suffix (-32, -8, …)
        sub(/-[0-9]+$/, "", name)

        ns_val   = "?"
        b_op     = "?"
        alloc_op = "?"

        for (i = 2; i <= NF; i++) {
            if ($i == "ns/op")   { ns_val   = $(i-1) }
            if ($i == "B/op")    { b_op     = $(i-1) }
            if ($i == "allocs/op") { alloc_op = $(i-1) }
        }
        printf "%s\t%s\t%s\t%s\n", name, ns_val, b_op, alloc_op
    }')

    local name ns_val b_op alloc_op
    IFS=$'\t' read -r name ns_val b_op alloc_op <<< "$parsed"

    # Colour the ns/op value by order of magnitude
    local ns_color
    local ns_num
    ns_num=$(echo "$ns_val" | tr -d ',' | cut -d. -f1)
    if [[ "$ns_num" =~ ^[0-9]+$ ]] && (( ns_num < 200 )); then
        ns_color="${GREEN}"
    elif [[ "$ns_num" =~ ^[0-9]+$ ]] && (( ns_num < 50000 )); then
        ns_color="${YELLOW}"
    else
        ns_color="${MAGENTA}"
    fi

    printf "  ${BOLD}%-50s${RESET}  ${ns_color}%12s ns/op${RESET}  ${DIM}%10s B/op  %5s allocs${RESET}\n" \
        "$name" "$ns_val" "$b_op" "$alloc_op"
}

# Run benchmarks for a package, filter by pattern, and pretty-print.
run_benchmarks() {
    local pkg="$1"
    local pattern="$2"
    local tmp
    tmp=$(mktemp)

    go test \
        -bench="$pattern" \
        -benchmem \
        -benchtime="$BENCHTIME" \
        -count=1 \
        -run='^$' \
        "$pkg" 2>&1 | tee "$tmp" | grep -v '^ok\|^goos\|^goarch\|^pkg\|^cpu' || true

    rm -f "$tmp"
}

pretty_run() {
    local pkg="$1"
    local pattern="$2"

    local raw
    raw=$(go test \
        -bench="$pattern" \
        -benchmem \
        -benchtime="$BENCHTIME" \
        -count=1 \
        -run='^$' \
        "$pkg" 2>/dev/null)

    while IFS= read -r line; do
        format_bench_line "$line"
    done <<< "$raw"
}

# ── banner ────────────────────────────────────────────────────────────────────
echo -e "\n${BOLD}${CYAN}  kula — Storage Engine Benchmark Suite${RESET}"
echo -e "${DIM}  benchtime=${BENCHTIME}   pkg=kula/internal/storage${RESET}"
echo -e "${DIM}  $(go version)${RESET}"

PKG="kula/internal/storage"

# ═══════════════════════════════════════════════════════════════════
# 1. CODEC
# ═══════════════════════════════════════════════════════════════════
header "Codec — Encode / Decode"

subheader "Sample serialisation (JSON marshal/unmarshal)"
divider
pretty_run "$PKG" "BenchmarkEncodeSample|BenchmarkDecodeSample"
divider

subheader "Fast timestamp extraction vs full decode"
divider
pretty_run "$PKG" "BenchmarkExtractVsFullDecode|BenchmarkExtractTimestamp"
divider

# ═══════════════════════════════════════════════════════════════════
# 2. WRITE PATH
# ═══════════════════════════════════════════════════════════════════
header "Write Path — Ring Buffer"

subheader "Sequential write throughput"
divider
pretty_run "$PKG" "BenchmarkWrite$"
divider

subheader "Write throughput into a wrapping (small) ring buffer"
divider
pretty_run "$PKG" "BenchmarkWriteWrapping"
divider

subheader "Concurrent writes — lock contention"
divider
pretty_run "$PKG" "BenchmarkWriteParallel"
divider

# ═══════════════════════════════════════════════════════════════════
# 3. READ / QUERY PATH
# ═══════════════════════════════════════════════════════════════════
header "Read Path — QueryRange"

subheader "Small range query (last 60 s of 300 samples)"
divider
pretty_run "$PKG" "BenchmarkQueryRange_Small"
divider

subheader "Large range query (full 3 600 samples — triggers downsampler)"
divider
pretty_run "$PKG" "BenchmarkQueryRange_Large"
divider

subheader "Query after ring buffer has wrapped multiple times"
divider
pretty_run "$PKG" "BenchmarkQueryRange_Wrapped"
divider

# ═══════════════════════════════════════════════════════════════════
# 4. QUERYLATEST HOT PATH
# ═══════════════════════════════════════════════════════════════════
header "QueryLatest — In-Memory Cache vs Cold Disk"

subheader "Warm-cache path  (steady state — called every second)"
divider
pretty_run "$PKG" "BenchmarkQueryLatest_Cache"
divider

subheader "Cold-disk path  (once at startup — warmLatestCache)"
divider
pretty_run "$PKG" "BenchmarkQueryLatest_ColdDisk"
divider

# ═══════════════════════════════════════════════════════════════════
# 5. AGGREGATION
# ═══════════════════════════════════════════════════════════════════
header "Aggregation"

subheader "Multi-tier aggregation (60-sample → 1-min window)"
divider
pretty_run "$PKG" "BenchmarkAggregateSamples"
divider

subheader "Inline downsampler (>800 samples → ~450 points)"
divider
pretty_run "$PKG" "BenchmarkDownsampling"
divider

# ═══════════════════════════════════════════════════════════════════
# DONE
# ═══════════════════════════════════════════════════════════════════
echo -e "\n🎉  Benchmark run ${GREEN}${BOLD}complete${RESET}${GREEN}.${RESET}  (benchtime=${BENCHTIME})\n"
