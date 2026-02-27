/* ============================================================
   Kula-Szpiegula Dashboard Application
   WebSocket-driven live monitoring with Chart.js
   ============================================================ */

(function () {
    'use strict';

    // ---- State ----
    const state = {
        ws: null,
        paused: false,
        connected: false,
        charts: {},
        timeRange: 300, // seconds, null when custom range
        customFrom: null,
        customTo: null,
        dataBuffer: [],
        maxBufferSize: 3600, // 1 hour of 1s data
        reconnectDelay: 1000,
        reconnectTimer: null,
        historyLoaded: false,
        loadingHistory: false,
        alerts: [],
        alertDropdownOpen: false,
        layoutMode: localStorage.getItem('kula_layout') || 'grid',
        lastSample: null,
    };

    // ---- Color Palette ----
    const colors = {
        blue: '#3b82f6',
        cyan: '#06b6d4',
        green: '#10b981',
        yellow: '#f59e0b',
        orange: '#f97316',
        red: '#ef4444',
        purple: '#8b5cf6',
        pink: '#ec4899',
        teal: '#14b8a6',
        lime: '#84cc16',
        blueAlpha: 'rgba(59, 130, 246, 0.15)',
        cyanAlpha: 'rgba(6, 182, 212, 0.15)',
        greenAlpha: 'rgba(16, 185, 129, 0.15)',
        redAlpha: 'rgba(239, 68, 68, 0.15)',
        purpleAlpha: 'rgba(139, 92, 246, 0.15)',
        yellowAlpha: 'rgba(245, 158, 11, 0.15)',
        orangeAlpha: 'rgba(249, 115, 22, 0.15)',
        pinkAlpha: 'rgba(236, 72, 153, 0.15)',
        tealAlpha: 'rgba(20, 184, 166, 0.15)',
        limeAlpha: 'rgba(132, 204, 22, 0.15)',
    };

    // ---- Chart.js Global Config ----
    Chart.defaults.color = '#94a3b8';
    Chart.defaults.borderColor = 'rgba(55, 65, 81, 0.3)';
    Chart.defaults.font.family = "'Inter', sans-serif";
    Chart.defaults.font.size = 11;
    Chart.defaults.animation = false; // disable all animations for performance
    Chart.defaults.plugins.legend.labels.usePointStyle = true;
    Chart.defaults.plugins.legend.labels.pointStyleWidth = 8;
    Chart.defaults.plugins.legend.labels.boxHeight = 6;

    // ---- Gauge Drawing ----
    function drawGauge(canvasId, value, max, color) {
        const canvas = document.getElementById(canvasId);
        if (!canvas) return;
        const ctx = canvas.getContext('2d');
        const w = canvas.width;
        const h = canvas.height;
        const cx = w / 2;
        const cy = h - 5;
        const radius = Math.min(cx - 10, cy - 10);

        ctx.clearRect(0, 0, w, h);

        // Background arc
        ctx.beginPath();
        ctx.arc(cx, cy, radius, Math.PI, 2 * Math.PI);
        ctx.strokeStyle = 'rgba(30, 41, 59, 0.8)';
        ctx.lineWidth = 12;
        ctx.lineCap = 'round';
        ctx.stroke();

        // Value arc
        const pct = Math.min(value / max, 1);
        const endAngle = Math.PI + pct * Math.PI;

        // Gradient
        const grad = ctx.createLinearGradient(cx - radius, cy, cx + radius, cy);
        if (typeof color === 'string') {
            grad.addColorStop(0, color);
            grad.addColorStop(1, color);
        } else {
            color.forEach((c, i) => grad.addColorStop(i / (color.length - 1), c));
        }

        ctx.beginPath();
        ctx.arc(cx, cy, radius, Math.PI, endAngle);
        ctx.strokeStyle = grad;
        ctx.lineWidth = 12;
        ctx.lineCap = 'round';
        ctx.stroke();

        // Glow effect
        ctx.beginPath();
        ctx.arc(cx, cy, radius, Math.PI, endAngle);
        ctx.strokeStyle = grad;
        ctx.lineWidth = 12;
        ctx.lineCap = 'round';
        ctx.filter = 'blur(6px)';
        ctx.globalAlpha = 0.3;
        ctx.stroke();
        ctx.filter = 'none';
        ctx.globalAlpha = 1;
    }

    // ---- Bar Gauge Drawing (alternative layout) ----
    function drawBarGauge(containerId, value, max, color) {
        const container = document.getElementById(containerId);
        if (!container) return;
        const pct = Math.min((value / max) * 100, 100);
        let fill = container.querySelector('.bar-gauge-fill');
        if (!fill) {
            container.innerHTML = `<div class="bar-gauge-container"><div class="bar-gauge-track"><div class="bar-gauge-fill"></div></div></div>`;
            fill = container.querySelector('.bar-gauge-fill');
        }
        fill.style.width = pct + '%';
        // Set gradient
        if (Array.isArray(color)) {
            fill.style.background = `linear-gradient(90deg, ${color.join(', ')})`;
        } else {
            fill.style.background = color;
        }
    }

    function updateGauges(sample) {
        const cpuPct = sample.cpu?.total?.usage || 0;
        const ramPct = sample.mem?.used_pct || 0;
        const swapPct = sample.swap?.used_pct || 0;
        const lavg = sample.lavg?.load1 || 0;
        const numCores = (sample.cpu?.cores?.length || 1);

        // Sum network across non-lo interfaces
        let dlMbps = 0, ulMbps = 0;
        if (sample.net?.ifaces) {
            sample.net.ifaces.forEach(i => {
                if (i.name !== 'lo') { dlMbps += i.rx_mbps || 0; ulMbps += i.tx_mbps || 0; }
            });
        }

        if (state.layoutMode === 'list') {
            // Bar gauges for CPU and Network in list mode
            drawBarGauge('gauge-cpu-canvas', cpuPct, 100, [colors.green, colors.yellow, colors.red]);
            document.getElementById('gauge-cpu-value').textContent = cpuPct.toFixed(1) + '%';

            drawBarGauge('gauge-ram-canvas', ramPct, 100, [colors.cyan, colors.blue, colors.purple]);
            document.getElementById('gauge-ram-value').textContent = ramPct.toFixed(1) + '%';

            drawBarGauge('gauge-swap-canvas', swapPct, 100, [colors.teal, colors.orange, colors.red]);
            document.getElementById('gauge-swap-value').textContent = swapPct.toFixed(1) + '%';

            drawBarGauge('gauge-lavg-canvas', lavg, numCores * 2, [colors.green, colors.yellow, colors.red]);
            document.getElementById('gauge-lavg-value').textContent = lavg.toFixed(2);

            const maxNet = Math.max(dlMbps, ulMbps, 1);
            drawBarGauge('gauge-dl-canvas', dlMbps, Math.max(maxNet * 1.5, 10), [colors.cyan, colors.blue]);
            document.getElementById('gauge-dl-value').textContent = formatMbps(dlMbps);

            drawBarGauge('gauge-ul-canvas', ulMbps, Math.max(maxNet * 1.5, 10), [colors.pink, colors.purple]);
            document.getElementById('gauge-ul-value').textContent = formatMbps(ulMbps);
        } else {
            drawGauge('gauge-cpu-canvas', cpuPct, 100, [colors.green, colors.yellow, colors.red]);
            document.getElementById('gauge-cpu-value').textContent = cpuPct.toFixed(1) + '%';

            drawGauge('gauge-ram-canvas', ramPct, 100, [colors.cyan, colors.blue, colors.purple]);
            document.getElementById('gauge-ram-value').textContent = ramPct.toFixed(1) + '%';

            drawGauge('gauge-swap-canvas', swapPct, 100, [colors.teal, colors.orange, colors.red]);
            document.getElementById('gauge-swap-value').textContent = swapPct.toFixed(1) + '%';

            drawGauge('gauge-lavg-canvas', lavg, numCores * 2, [colors.green, colors.yellow, colors.red]);
            document.getElementById('gauge-lavg-value').textContent = lavg.toFixed(2);

            const maxNet = Math.max(dlMbps, ulMbps, 1);
            drawGauge('gauge-dl-canvas', dlMbps, Math.max(maxNet * 1.5, 10), [colors.cyan, colors.blue]);
            document.getElementById('gauge-dl-value').textContent = formatMbps(dlMbps);

            drawGauge('gauge-ul-canvas', ulMbps, Math.max(maxNet * 1.5, 10), [colors.pink, colors.purple]);
            document.getElementById('gauge-ul-value').textContent = formatMbps(ulMbps);
        }
    }

    // ---- Chart Initialization ----
    function createTimeSeriesChart(canvasId, datasets, yConfig = {}) {
        const ctx = document.getElementById(canvasId);
        if (!ctx) return null;

        const chart = new Chart(ctx, {
            type: 'line',
            data: { datasets },
            options: {
                responsive: true,
                maintainAspectRatio: false,
                interaction: { mode: 'index', intersect: false },
                spanGaps: false,
                plugins: {
                    legend: { position: 'top', align: 'end' },
                    zoom: {
                        pan: { enabled: true, mode: 'x' },
                        zoom: {
                            drag: { enabled: true, backgroundColor: 'rgba(59,130,246,0.1)', borderColor: colors.blue, borderWidth: 1 },
                            mode: 'x',
                            onZoom: ({ chart }) => syncZoom(chart),
                        },
                    },
                },
                scales: {
                    x: {
                        type: 'time',
                        time: { tooltipFormat: 'HH:mm:ss', displayFormats: { second: 'HH:mm:ss', minute: 'HH:mm', hour: 'HH:mm' } },
                        grid: { display: false },
                        ticks: { maxTicksLimit: 8 },
                    },
                    y: {
                        beginAtZero: true,
                        ...yConfig,
                        grid: { color: 'rgba(55, 65, 81, 0.2)' },
                    },
                },
                elements: {
                    point: { radius: 0, hoverRadius: 3 },
                    line: { tension: 0.3, borderWidth: 1.5 },
                },
            },
        });

        return chart;
    }

    function destroyAllCharts() {
        Object.keys(state.charts).forEach(key => {
            if (state.charts[key]) {
                state.charts[key].destroy();
                state.charts[key] = null;
            }
        });
    }

    function initCharts() {
        destroyAllCharts();

        // CPU
        state.charts.cpu = createTimeSeriesChart('chart-cpu', [
            { label: 'User', borderColor: colors.blue, backgroundColor: colors.blueAlpha, fill: true, data: [] },
            { label: 'System', borderColor: colors.red, backgroundColor: colors.redAlpha, fill: true, data: [] },
            { label: 'IOWait', borderColor: colors.yellow, backgroundColor: colors.yellowAlpha, fill: true, data: [] },
            { label: 'Steal', borderColor: colors.purple, backgroundColor: colors.purpleAlpha, fill: true, data: [] },
            { label: 'Total', borderColor: colors.cyan, data: [], fill: false, borderWidth: 2 },
        ], { max: 100, ticks: { callback: v => v + '%' } });

        // Load Average
        state.charts.loadavg = createTimeSeriesChart('chart-loadavg', [
            { label: '1 min', borderColor: colors.red, data: [], fill: false, borderWidth: 2 },
            { label: '5 min', borderColor: colors.yellow, data: [], fill: false },
            { label: '15 min', borderColor: colors.green, data: [], fill: false },
        ]);

        // Memory — with Free and Available datasets, max set dynamically
        state.charts.memory = createTimeSeriesChart('chart-memory', [
            { label: 'Used', borderColor: colors.blue, backgroundColor: colors.blueAlpha, fill: true, data: [] },
            { label: 'Buffers', borderColor: colors.cyan, backgroundColor: colors.cyanAlpha, fill: true, data: [] },
            { label: 'Cached', borderColor: colors.green, backgroundColor: colors.greenAlpha, fill: true, data: [] },
            { label: 'Free', borderColor: colors.teal, data: [], fill: false, borderDash: [4, 2] },
            { label: 'Available', borderColor: colors.lime, data: [], fill: false, borderDash: [4, 2] },
        ], { ticks: { callback: v => formatBytesShort(v) } });

        // Swap — with Free dataset, max set dynamically
        state.charts.swap = createTimeSeriesChart('chart-swap', [
            { label: 'Used', borderColor: colors.orange, backgroundColor: colors.orangeAlpha, fill: true, data: [] },
            { label: 'Free', borderColor: colors.teal, data: [], fill: false, borderDash: [4, 2] },
        ], { min: 0, ticks: { callback: v => formatBytesShort(v) } });

        // Network
        state.charts.network = createTimeSeriesChart('chart-network', [
            { label: '↓ RX', borderColor: colors.cyan, backgroundColor: colors.cyanAlpha, fill: true, data: [] },
            { label: '↑ TX', borderColor: colors.pink, backgroundColor: colors.pinkAlpha, fill: true, data: [] },
        ], { ticks: { callback: v => v.toFixed(1) + ' Mbps' } });

        // Packets per second
        state.charts.pps = createTimeSeriesChart('chart-pps', [
            { label: '↓ RX pps', borderColor: colors.green, backgroundColor: colors.greenAlpha, fill: true, data: [] },
            { label: '↑ TX pps', borderColor: colors.orange, backgroundColor: colors.orangeAlpha, fill: true, data: [] },
        ], { ticks: { callback: v => formatPPS(v) } });

        // Connections
        state.charts.connections = createTimeSeriesChart('chart-connections', [
            { label: 'TCP', borderColor: colors.blue, data: [], fill: false },
            { label: 'UDP', borderColor: colors.green, data: [], fill: false },
            { label: 'TIME_WAIT', borderColor: colors.yellow, data: [], fill: false },
            { label: 'Established', borderColor: colors.cyan, data: [], fill: false },
        ]);

        // Disk I/O
        state.charts.diskio = createTimeSeriesChart('chart-disk-io', [
            { label: 'Read', borderColor: colors.green, backgroundColor: colors.greenAlpha, fill: true, data: [] },
            { label: 'Write', borderColor: colors.orange, backgroundColor: colors.orangeAlpha, fill: true, data: [] },
        ], { ticks: { callback: v => formatBytesShort(v) + '/s' } });

        // Disk Utilization
        state.charts.diskutil = createTimeSeriesChart('chart-disk-util', [
            { label: 'Utilization', borderColor: colors.purple, backgroundColor: colors.purpleAlpha, fill: true, data: [] },
        ], { max: 100, ticks: { callback: v => v + '%' } });

        // Disk Space (bar chart)
        state.charts.diskspace = new Chart(document.getElementById('chart-disk-space'), {
            type: 'bar',
            data: {
                labels: [], datasets: [
                    { label: 'Used', backgroundColor: colors.blue, data: [] },
                    { label: 'Available', backgroundColor: 'rgba(55, 65, 81, 0.4)', data: [] },
                ]
            },
            options: {
                responsive: true,
                maintainAspectRatio: false,
                indexAxis: 'y',
                plugins: { legend: { display: false } },
                scales: {
                    x: { stacked: true, ticks: { callback: v => formatBytesShort(v) } },
                    y: { stacked: true, grid: { display: false } },
                },
            },
        });

        // Processes
        state.charts.processes = createTimeSeriesChart('chart-processes', [
            { label: 'Running', borderColor: colors.green, data: [], fill: false },
            { label: 'Sleeping', borderColor: colors.blue, data: [], fill: false },
            { label: 'Blocked', borderColor: colors.red, data: [], fill: false },
            { label: 'Zombie', borderColor: colors.yellow, data: [], fill: false },
            { label: 'Total', borderColor: colors.cyan, data: [], fill: false, borderDash: [4, 2] },
        ]);

        // Entropy
        state.charts.entropy = createTimeSeriesChart('chart-entropy', [
            { label: 'Entropy', borderColor: colors.green, backgroundColor: colors.greenAlpha, fill: true, data: [] },
        ]);

        // Self monitoring
        state.charts.self = createTimeSeriesChart('chart-self', [
            { label: 'CPU %', borderColor: colors.cyan, data: [], fill: false, yAxisID: 'y' },
            { label: 'RSS MB', borderColor: colors.purple, data: [], fill: false, yAxisID: 'y1' },
        ], {});

        // Reconfigure self chart for dual axes
        if (state.charts.self) {
            state.charts.self.options.scales.y1 = {
                position: 'right',
                beginAtZero: true,
                grid: { display: false },
                ticks: { callback: v => v.toFixed(0) + ' MB' },
            };
            state.charts.self.update('none');
        }
    }

    // ---- Set x-axis bounds for full time window ----
    function setChartTimeRange() {
        const now = Date.now();
        let xMin, xMax;

        if (state.timeRange !== null) {
            xMin = now - state.timeRange * 1000;
            xMax = now;
        } else if (state.customFrom && state.customTo) {
            xMin = state.customFrom.getTime();
            xMax = state.customTo.getTime();
        } else {
            return;
        }

        Object.values(state.charts).forEach(chart => {
            if (!chart?.options?.scales?.x || chart.config?.type === 'bar') return;
            chart.options.scales.x.min = xMin;
            chart.options.scales.x.max = xMax;
        });
    }

    // ---- Data Update ----
    function addSampleToCharts(s, ts) {
        const point = (v) => ({ x: ts, y: v });

        // CPU
        if (state.charts.cpu && s.cpu?.total) {
            state.charts.cpu.data.datasets[0].data.push(point(s.cpu.total.user));
            state.charts.cpu.data.datasets[1].data.push(point(s.cpu.total.system));
            state.charts.cpu.data.datasets[2].data.push(point(s.cpu.total.iowait));
            state.charts.cpu.data.datasets[3].data.push(point(s.cpu.total.steal));
            state.charts.cpu.data.datasets[4].data.push(point(s.cpu.total.usage));
        }

        // Load Average
        if (state.charts.loadavg && s.lavg) {
            state.charts.loadavg.data.datasets[0].data.push(point(s.lavg.load1));
            state.charts.loadavg.data.datasets[1].data.push(point(s.lavg.load5));
            state.charts.loadavg.data.datasets[2].data.push(point(s.lavg.load15));
        }

        // Memory — with Free and Available
        if (state.charts.memory && s.mem) {
            state.charts.memory.data.datasets[0].data.push(point(s.mem.used));
            state.charts.memory.data.datasets[1].data.push(point(s.mem.buffers));
            state.charts.memory.data.datasets[2].data.push(point(s.mem.cached));
            state.charts.memory.data.datasets[3].data.push(point(s.mem.free));
            state.charts.memory.data.datasets[4].data.push(point(s.mem.available));
            // Set max to total RAM
            if (s.mem.total > 0) {
                state.charts.memory.options.scales.y.max = s.mem.total;
            }
        }

        // Swap — with Free
        if (state.charts.swap && s.swap) {
            state.charts.swap.data.datasets[0].data.push(point(s.swap.used || 0));
            state.charts.swap.data.datasets[1].data.push(point(s.swap.free || 0));
            // Set max to total swap
            if (s.swap.total > 0) {
                state.charts.swap.options.scales.y.max = s.swap.total;
            }
        }

        // Network (sum non-lo)
        if (state.charts.network && s.net?.ifaces) {
            let rx = 0, tx = 0;
            s.net.ifaces.forEach(i => { if (i.name !== 'lo') { rx += i.rx_mbps || 0; tx += i.tx_mbps || 0; } });
            state.charts.network.data.datasets[0].data.push(point(rx));
            state.charts.network.data.datasets[1].data.push(point(tx));
        }

        // Packets per second (sum non-lo)
        if (state.charts.pps && s.net?.ifaces) {
            let rxPps = 0, txPps = 0;
            s.net.ifaces.forEach(i => { if (i.name !== 'lo') { rxPps += i.rx_pps || 0; txPps += i.tx_pps || 0; } });
            state.charts.pps.data.datasets[0].data.push(point(rxPps));
            state.charts.pps.data.datasets[1].data.push(point(txPps));
        }

        // Connections
        if (state.charts.connections && s.net?.sockets) {
            state.charts.connections.data.datasets[0].data.push(point(s.net.sockets.tcp_inuse));
            state.charts.connections.data.datasets[1].data.push(point(s.net.sockets.udp_inuse));
            state.charts.connections.data.datasets[2].data.push(point(s.net.sockets.tcp_tw));
            state.charts.connections.data.datasets[3].data.push(point(s.net?.tcp?.curr_estab || 0));
        }

        // Disk I/O (sum all devices)
        if (state.charts.diskio && s.disk?.devices) {
            let rBps = 0, wBps = 0;
            s.disk.devices.forEach(d => { rBps += d.read_bps || 0; wBps += d.write_bps || 0; });
            state.charts.diskio.data.datasets[0].data.push(point(rBps));
            state.charts.diskio.data.datasets[1].data.push(point(wBps));
        }

        // Disk Utilization (max across devices)
        if (state.charts.diskutil && s.disk?.devices) {
            let maxUtil = 0;
            s.disk.devices.forEach(d => { if (d.util_pct > maxUtil) maxUtil = d.util_pct; });
            state.charts.diskutil.data.datasets[0].data.push(point(maxUtil));
        }

        // Disk Space (update bar chart — overwrite, not append)
        if (state.charts.diskspace && s.disk?.filesystems) {
            const labels = [];
            const used = [];
            const avail = [];
            s.disk.filesystems.forEach(f => {
                labels.push(f.mount);
                used.push(f.used);
                avail.push(f.available);
            });
            state.charts.diskspace.data.labels = labels;
            state.charts.diskspace.data.datasets[0].data = used;
            state.charts.diskspace.data.datasets[1].data = avail;
        }

        // Processes
        if (state.charts.processes && s.proc) {
            state.charts.processes.data.datasets[0].data.push(point(s.proc.running));
            state.charts.processes.data.datasets[1].data.push(point(s.proc.sleeping));
            state.charts.processes.data.datasets[2].data.push(point(s.proc.blocked));
            state.charts.processes.data.datasets[3].data.push(point(s.proc.zombie));
            state.charts.processes.data.datasets[4].data.push(point(s.proc.total));
        }

        // Entropy
        if (state.charts.entropy && s.sys) {
            state.charts.entropy.data.datasets[0].data.push(point(s.sys.entropy));
        }

        // Self
        if (state.charts.self && s.self) {
            state.charts.self.data.datasets[0].data.push(point(s.self.cpu_pct));
            state.charts.self.data.datasets[1].data.push(point((s.self.mem_rss || 0) / 1048576));
        }
    }

    // Batch-update all charts at once
    function updateAllCharts() {
        setChartTimeRange();
        Object.values(state.charts).forEach(chart => {
            if (chart) chart.update('none');
        });
    }

    // Push a single live sample — adds data + updates charts immediately
    function pushLiveSample(sample) {
        const ts = new Date(sample.ts);
        state.dataBuffer.push(sample);
        state.lastSample = sample;
        if (state.dataBuffer.length > state.maxBufferSize) {
            state.dataBuffer.shift();
        }

        updateGauges(sample);
        updateHeader(sample);
        addSampleToCharts(sample, ts);
        trimChartsToTimeRange();
        updateAllCharts();
        updateSubtitles(sample);
        evaluateAlerts(sample);
    }

    function trimChartsToTimeRange() {
        if (state.timeRange === null) return; // custom range — don't trim
        const cutoff = new Date(Date.now() - state.timeRange * 1000);

        Object.values(state.charts).forEach(chart => {
            if (!chart || !chart.data?.datasets) return;
            chart.data.datasets.forEach(ds => {
                if (!Array.isArray(ds.data)) return;
                while (ds.data.length > 0 && ds.data[0].x && ds.data[0].x < cutoff) {
                    ds.data.shift();
                }
            });
        });
    }

    function clearAllChartData() {
        Object.values(state.charts).forEach(chart => {
            if (!chart?.data?.datasets) return;
            chart.data.datasets.forEach(ds => {
                if (Array.isArray(ds.data)) ds.data = [];
            });
        });
    }

    function syncZoom(sourceChart) {
        const { min, max } = sourceChart.scales.x;
        Object.values(state.charts).forEach(chart => {
            if (!chart || chart === sourceChart || !chart.options?.scales?.x) return;
            chart.options.scales.x.min = min;
            chart.options.scales.x.max = max;
            chart.update('none');
        });
    }

    function resetZoomAll() {
        Object.values(state.charts).forEach(chart => {
            if (!chart?.options?.scales?.x) return;
            delete chart.options.scales.x.min;
            delete chart.options.scales.x.max;
            chart.update('none');
        });
    }

    // ---- Alert System ----
    function evaluateAlerts(sample) {
        const alerts = [];
        const numCores = sample.cpu?.cores?.length || 1;

        // Clock not synced
        if (sample.sys?.clock_synced === false) {
            alerts.push({
                icon: '⏱',
                title: 'Clock not synchronized',
                detail: 'Source: ' + (sample.sys.clock_source || 'unknown'),
            });
        }

        // Low entropy
        if (sample.sys?.entropy !== undefined && sample.sys.entropy < 256) {
            alerts.push({
                icon: '🎲',
                title: 'Low entropy',
                detail: `Current: ${sample.sys.entropy} (min recommended: 256)`,
            });
        }

        // Load average exceeds core count
        if (sample.lavg?.load1 > numCores) {
            alerts.push({
                icon: '🔥',
                title: 'Load exceeds core count',
                detail: `Load1: ${sample.lavg.load1.toFixed(2)}, Cores: ${numCores}`,
            });
        }

        state.alerts = alerts;
        updateAlertUI();
    }

    function updateAlertUI() {
        const badge = document.getElementById('alert-badge');
        const btn = document.getElementById('btn-alerts');
        const list = document.getElementById('alert-list');

        if (state.alerts.length > 0) {
            badge.textContent = state.alerts.length;
            badge.classList.remove('hidden');
            btn.classList.add('has-alerts');
        } else {
            badge.classList.add('hidden');
            btn.classList.remove('has-alerts');
        }

        // Render alert items
        if (state.alerts.length === 0) {
            list.innerHTML = '<div class="alert-empty">No active alerts</div>';
        } else {
            list.innerHTML = state.alerts.map(a => `
                <div class="alert-item">
                    <span class="alert-icon">${a.icon}</span>
                    <div class="alert-item-body">
                        <div class="alert-item-title">${a.title}</div>
                        <div class="alert-item-detail">${a.detail}</div>
                    </div>
                </div>
            `).join('');
        }
    }

    function toggleAlertDropdown() {
        state.alertDropdownOpen = !state.alertDropdownOpen;
        const dropdown = document.getElementById('alert-dropdown');
        if (state.alertDropdownOpen) {
            dropdown.classList.remove('hidden');
        } else {
            dropdown.classList.add('hidden');
        }
    }

    // ---- Header / Subtitles ----
    function updateHeader(s) {
        const el = (id) => document.getElementById(id);
        if (s.sys?.hostname) el('hostname').textContent = s.sys.hostname;
        if (s.sys?.uptime_human) el('uptime').textContent = '⏱ ' + s.sys.uptime_human;
        el('clock').textContent = new Date(s.ts).toLocaleTimeString();

        // System info footer
        const sysInfo = [];
        if (s.sys?.clock_synced !== undefined) sysInfo.push('clock: ' + (s.sys.clock_synced ? '✓ synced' : '✗ not synced'));
        if (s.sys?.clock_source) sysInfo.push('source: ' + s.sys.clock_source);
        if (s.sys?.entropy) sysInfo.push('entropy: ' + s.sys.entropy);
        if (s.sys?.user_count !== undefined) sysInfo.push('users: ' + s.sys.user_count);
        if (s.self) sysInfo.push('self: ' + s.self.cpu_pct.toFixed(1) + '% cpu, ' + formatBytesShort(s.self.mem_rss) + ' rss');
        el('sys-info').textContent = sysInfo.join('  │  ');
    }

    function updateSubtitles(s) {
        const el = (id, text) => { const e = document.getElementById(id); if (e) e.textContent = text; };

        if (s.cpu?.total) {
            el('cpu-subtitle', `usr:${s.cpu.total.user.toFixed(1)}% sys:${s.cpu.total.system.toFixed(1)}% io:${s.cpu.total.iowait.toFixed(1)}% ${s.cpu.cores?.length || 0} cores`);
        }
        if (s.lavg) el('lavg-subtitle', `${s.lavg.load1.toFixed(2)} / ${s.lavg.load5.toFixed(2)} / ${s.lavg.load15.toFixed(2)}`);
        // Memory — with % appended
        if (s.mem) {
            const memPct = s.mem.used_pct !== undefined ? s.mem.used_pct.toFixed(1) : '0.0';
            el('mem-subtitle', `${formatBytesShort(s.mem.used)} / ${formatBytesShort(s.mem.total)} (${memPct}%)`);
        }
        // Swap — with % appended
        if (s.swap) {
            const swapPct = s.swap.used_pct !== undefined ? s.swap.used_pct.toFixed(1) : '0.0';
            el('swap-subtitle', `${formatBytesShort(s.swap.used || 0)} / ${formatBytesShort(s.swap.total || 0)} (${swapPct}%)`);
        }
        if (s.net?.ifaces) {
            let rx = 0, tx = 0, rxPps = 0, txPps = 0;
            s.net.ifaces.forEach(i => {
                if (i.name !== 'lo') {
                    rx += i.rx_mbps || 0; tx += i.tx_mbps || 0;
                    rxPps += i.rx_pps || 0; txPps += i.tx_pps || 0;
                }
            });
            el('net-subtitle', `↓${formatMbps(rx)} ↑${formatMbps(tx)}`);
            el('pps-subtitle', `↓${formatPPS(rxPps)} ↑${formatPPS(txPps)}`);
        }
        if (s.disk?.devices) {
            let r = 0, w = 0;
            s.disk.devices.forEach(d => { r += d.read_bps || 0; w += d.write_bps || 0; });
            el('diskio-subtitle', `R:${formatBytesShort(r)}/s W:${formatBytesShort(w)}/s`);
        }
        if (s.proc) el('proc-subtitle', `${s.proc.total} total, ${s.proc.running} running`);
        if (s.self) el('self-subtitle', `${s.self.cpu_pct.toFixed(1)}% cpu, ${formatBytesShort(s.self.mem_rss)} rss`);
    }

    // ---- WebSocket ----
    function connectWS() {
        const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
        const wsUrl = `${proto}//${location.host}/ws`;

        try {
            state.ws = new WebSocket(wsUrl);
        } catch (e) {
            scheduleReconnect();
            return;
        }

        state.ws.onopen = () => {
            state.connected = true;
            state.reconnectDelay = 1000;
            updateConnectionStatus(true);
            // Load history for the current time window on first connect
            if (!state.historyLoaded) {
                state.historyLoaded = true;
                fetchHistory(state.timeRange);
            }
        };

        state.ws.onmessage = (evt) => {
            if (state.loadingHistory) return; // skip live samples while loading history
            try {
                const sample = JSON.parse(evt.data);
                pushLiveSample(sample);
            } catch (e) {
                console.error('Parse error:', e);
            }
        };

        state.ws.onclose = () => {
            state.connected = false;
            updateConnectionStatus(false);
            scheduleReconnect();
        };

        state.ws.onerror = () => {
            state.ws.close();
        };
    }

    function scheduleReconnect() {
        if (state.reconnectTimer) return;
        state.reconnectTimer = setTimeout(() => {
            state.reconnectTimer = null;
            connectWS();
        }, state.reconnectDelay);
        state.reconnectDelay = Math.min(state.reconnectDelay * 1.5, 30000);
    }

    function updateConnectionStatus(connected) {
        const dot = document.getElementById('connection-status');
        if (dot) {
            dot.className = 'status-dot ' + (connected ? 'connected' : 'disconnected');
            dot.title = connected ? 'Connected' : 'Disconnected';
        }
    }

    // ---- Pause/Resume ----
    function togglePause() {
        state.paused = !state.paused;
        const btn = document.getElementById('btn-pause');
        btn.textContent = state.paused ? '▶' : '⏸';
        btn.classList.toggle('paused', state.paused);

        if (state.ws?.readyState === WebSocket.OPEN) {
            state.ws.send(JSON.stringify({ action: state.paused ? 'pause' : 'resume' }));
        }
    }

    // ---- Layout Toggle ----
    function toggleLayout() {
        state.layoutMode = state.layoutMode === 'grid' ? 'list' : 'grid';
        localStorage.setItem('kula_layout', state.layoutMode);
        applyLayout();
    }

    function applyLayout() {
        const dashboard = document.getElementById('dashboard');
        const btn = document.getElementById('btn-layout');

        if (state.layoutMode === 'list') {
            dashboard.classList.add('layout-list');
            btn.classList.add('layout-active');
            btn.textContent = '⊟';
            btn.title = 'Switch to grid layout';
            // Replace gauge canvases with divs for bar gauges
            replaceGaugesForBarMode();
        } else {
            dashboard.classList.remove('layout-list');
            btn.classList.remove('layout-active');
            btn.textContent = '⊞';
            btn.title = 'Switch to list layout';
            // Restore gauge canvases
            restoreGaugesForArcMode();
        }

        // Re-init charts for new layout
        initCharts();
        // Reload data
        if (state.lastSample) {
            // Reload history
            if (state.timeRange !== null) {
                fetchHistory(state.timeRange);
            } else if (state.customFrom && state.customTo) {
                fetchCustomHistory(state.customFrom, state.customTo);
            }
        }
    }

    function replaceGaugesForBarMode() {
        const gaugeCanvasIds = ['gauge-cpu-canvas', 'gauge-ram-canvas', 'gauge-swap-canvas', 'gauge-lavg-canvas', 'gauge-dl-canvas', 'gauge-ul-canvas'];
        gaugeCanvasIds.forEach(id => {
            const el = document.getElementById(id);
            if (el && el.tagName === 'CANVAS') {
                const div = document.createElement('div');
                div.id = id;
                el.replaceWith(div);
            }
        });
    }

    function restoreGaugesForArcMode() {
        const gaugeCanvasIds = ['gauge-cpu-canvas', 'gauge-ram-canvas', 'gauge-swap-canvas', 'gauge-lavg-canvas', 'gauge-dl-canvas', 'gauge-ul-canvas'];
        gaugeCanvasIds.forEach(id => {
            const el = document.getElementById(id);
            if (el && el.tagName !== 'CANVAS') {
                const canvas = document.createElement('canvas');
                canvas.id = id;
                canvas.width = 160;
                canvas.height = 100;
                el.replaceWith(canvas);
            }
        });
    }

    // ---- Time Range ----
    function setTimeRange(seconds) {
        state.timeRange = seconds;
        state.customFrom = null;
        state.customTo = null;
        document.querySelectorAll('.time-btn[data-range]').forEach(b => b.classList.remove('active'));
        document.querySelector(`.time-btn[data-range="${seconds}"]`)?.classList.add('active');
        document.getElementById('btn-custom-range')?.classList.remove('active');

        const labels = {
            300: 'Last 5 minutes', 900: 'Last 15 minutes', 1800: 'Last 30 minutes',
            3600: 'Last 1 hour', 10800: 'Last 3 hours', 21600: 'Last 6 hours',
            86400: 'Last 24 hours', 259200: 'Last 3 days', 604800: 'Last 7 days'
        };
        document.getElementById('time-range-display').textContent = labels[seconds] || `Last ${seconds}s`;

        resetZoomAll();
        fetchHistory(seconds);
    }

    function fetchHistory(rangeSeconds) {
        if (state.loadingHistory) return;
        state.loadingHistory = true;

        const to = new Date().toISOString();
        const from = new Date(Date.now() - rangeSeconds * 1000).toISOString();
        fetch(`/api/history?from=${from}&to=${to}`)
            .then(r => r.json())
            .then(data => {
                if (!Array.isArray(data) || data.length === 0) {
                    // Even with no data, set the time bounds so charts show empty regions
                    clearAllChartData();
                    state.dataBuffer = [];
                    setChartTimeRange();
                    updateAllCharts();
                    state.loadingHistory = false;
                    return;
                }

                // Clear all chart data before loading history
                clearAllChartData();
                state.dataBuffer = [];

                // Batch add all historical points WITHOUT chart.update() per sample
                data.forEach(item => {
                    const sample = item.data || item;
                    const ts = new Date(sample.ts);
                    state.dataBuffer.push(sample);
                    addSampleToCharts(sample, ts);
                });

                // Trim buffer
                if (state.dataBuffer.length > state.maxBufferSize) {
                    state.dataBuffer = state.dataBuffer.slice(-state.maxBufferSize);
                }

                // Single batch update of all charts
                trimChartsToTimeRange();
                updateAllCharts();

                // Update gauges/header with latest sample
                const lastSample = data[data.length - 1];
                const s = lastSample.data || lastSample;
                state.lastSample = s;
                updateGauges(s);
                updateHeader(s);
                updateSubtitles(s);
                evaluateAlerts(s);

                state.loadingHistory = false;
            })
            .catch(e => {
                console.error('History fetch error:', e);
                state.loadingHistory = false;
            });
    }

    function fetchCustomHistory(fromDate, toDate) {
        if (state.loadingHistory) return;
        state.loadingHistory = true;

        const from = fromDate.toISOString();
        const to = toDate.toISOString();
        fetch(`/api/history?from=${from}&to=${to}`)
            .then(r => r.json())
            .then(data => {
                clearAllChartData();
                state.dataBuffer = [];

                if (Array.isArray(data) && data.length > 0) {
                    data.forEach(item => {
                        const sample = item.data || item;
                        const ts = new Date(sample.ts);
                        state.dataBuffer.push(sample);
                        addSampleToCharts(sample, ts);
                    });

                    if (state.dataBuffer.length > state.maxBufferSize) {
                        state.dataBuffer = state.dataBuffer.slice(-state.maxBufferSize);
                    }

                    const lastSample = data[data.length - 1];
                    const s = lastSample.data || lastSample;
                    state.lastSample = s;
                    updateGauges(s);
                    updateHeader(s);
                    updateSubtitles(s);
                    evaluateAlerts(s);
                }

                setChartTimeRange();
                updateAllCharts();
                state.loadingHistory = false;
            })
            .catch(e => {
                console.error('Custom history fetch error:', e);
                state.loadingHistory = false;
            });
    }

    // ---- Custom Time Range ----
    function toggleCustomTimePicker() {
        const customEl = document.getElementById('time-custom');
        const isHidden = customEl.classList.contains('hidden');
        if (isHidden) {
            customEl.classList.remove('hidden');
            document.getElementById('btn-custom-range').classList.add('active');
            // Set default values
            const now = new Date();
            const from = new Date(now.getTime() - 3600000); // 1 hour ago
            document.getElementById('custom-from').value = toLocalISOString(from);
            document.getElementById('custom-to').value = toLocalISOString(now);
        } else {
            customEl.classList.add('hidden');
            document.getElementById('btn-custom-range').classList.remove('active');
        }
    }

    function applyCustomRange() {
        const fromVal = document.getElementById('custom-from').value;
        const toVal = document.getElementById('custom-to').value;
        if (!fromVal || !toVal) return;

        const fromDate = new Date(fromVal);
        const toDate = new Date(toVal);
        if (fromDate >= toDate) return;

        state.timeRange = null;
        state.customFrom = fromDate;
        state.customTo = toDate;

        // Deselect preset buttons
        document.querySelectorAll('.time-btn[data-range]').forEach(b => b.classList.remove('active'));

        const fmt = d => d.toLocaleString(undefined, { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' });
        document.getElementById('time-range-display').textContent = `${fmt(fromDate)} → ${fmt(toDate)}`;

        resetZoomAll();
        fetchCustomHistory(fromDate, toDate);
    }

    function toLocalISOString(date) {
        const pad = n => String(n).padStart(2, '0');
        return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}`;
    }

    // ---- Auth ----
    function checkAuth() {
        fetch('/api/auth/status')
            .then(r => r.json())
            .then(data => {
                if (data.auth_required && !data.authenticated) {
                    document.getElementById('login-overlay').classList.remove('hidden');
                    document.getElementById('dashboard').style.filter = 'blur(8px)';
                } else {
                    document.getElementById('login-overlay').classList.add('hidden');
                    document.getElementById('dashboard').style.filter = '';
                    connectWS();
                }
            })
            .catch(() => connectWS()); // If auth check fails, try connecting anyway
    }

    function handleLogin(e) {
        e.preventDefault();
        const user = document.getElementById('login-user').value;
        const pass = document.getElementById('login-pass').value;
        const errorEl = document.getElementById('login-error');

        fetch('/api/login', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ username: user, password: pass }),
        })
            .then(r => {
                if (!r.ok) throw new Error('Invalid credentials');
                return r.json();
            })
            .then(() => {
                document.getElementById('login-overlay').classList.add('hidden');
                document.getElementById('dashboard').style.filter = '';
                errorEl.classList.add('hidden');
                connectWS();
            })
            .catch(err => {
                errorEl.textContent = err.message;
                errorEl.classList.remove('hidden');
            });
    }

    // ---- Utility ----
    function formatBytesShort(bytes) {
        if (bytes === 0 || bytes === undefined || bytes === null || isNaN(bytes)) return '0 B';
        if (Math.abs(bytes) < 1) return '0 B';
        const units = ['B', 'KB', 'MB', 'GB', 'TB'];
        const i = Math.floor(Math.log(Math.abs(bytes)) / Math.log(1024));
        const idx = Math.max(0, Math.min(i, units.length - 1));
        return (bytes / Math.pow(1024, idx)).toFixed(idx > 0 ? 1 : 0) + ' ' + units[idx];
    }

    function formatMbps(v) {
        if (v < 1) return (v * 1000).toFixed(0) + ' Kbps';
        return v.toFixed(2) + ' Mbps';
    }

    function formatPPS(v) {
        if (v === undefined || v === null || isNaN(v)) return '0 pps';
        if (v >= 1000000) return (v / 1000000).toFixed(1) + ' Mpps';
        if (v >= 1000) return (v / 1000).toFixed(1) + ' Kpps';
        return Math.round(v) + ' pps';
    }

    // ---- Init ----
    function init() {
        // Apply stored layout
        applyLayout();

        // Event listeners
        document.getElementById('btn-pause').addEventListener('click', togglePause);
        document.getElementById('btn-layout').addEventListener('click', toggleLayout);
        document.getElementById('btn-alerts').addEventListener('click', toggleAlertDropdown);
        document.getElementById('login-form').addEventListener('submit', handleLogin);
        document.getElementById('btn-custom-range').addEventListener('click', toggleCustomTimePicker);
        document.getElementById('btn-apply-custom').addEventListener('click', applyCustomRange);

        document.querySelectorAll('.time-btn[data-range]').forEach(btn => {
            btn.addEventListener('click', () => setTimeRange(parseInt(btn.dataset.range)));
        });

        // Double-click on any chart to reset zoom
        document.querySelectorAll('.chart-body canvas').forEach(canvas => {
            canvas.addEventListener('dblclick', resetZoomAll);
        });

        // Close alert dropdown when clicking outside
        document.addEventListener('click', (e) => {
            if (state.alertDropdownOpen && !e.target.closest('.alert-container')) {
                state.alertDropdownOpen = false;
                document.getElementById('alert-dropdown').classList.add('hidden');
            }
        });

        checkAuth();
    }

    if (document.readyState === 'loading') {
        document.addEventListener('DOMContentLoaded', init);
    } else {
        init();
    }
})();
