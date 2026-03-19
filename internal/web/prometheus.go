package web

import (
	"fmt"
	"net/http"
	"strings"

	"kula/internal/collector"
)

// handleMetrics serves Prometheus-compatible metrics in text exposition format.
// The endpoint is unauthenticated so Prometheus scrapers work without credentials.
// It reflects the latest collected sample; if no sample is available yet it
// returns an empty 200 response so scrapers don't alarm on startup.
func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	sample, err := s.store.QueryLatest()
	if err != nil || sample == nil || sample.Data == nil {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		return
	}

	d := sample.Data
	hostname := d.System.Hostname
	if hostname == "" {
		hostname = s.global.Hostname
	}

	var b strings.Builder
	b.Grow(4096)

	// Helper closures
	gauge := func(name, help, labels string, value float64) {
		fmt.Fprintf(&b, "# HELP %s %s\n# TYPE %s gauge\n", name, help, name)
		if labels != "" {
			fmt.Fprintf(&b, "%s{%s} %g\n", name, labels, value)
		} else {
			fmt.Fprintf(&b, "%s %g\n", name, value)
		}
	}
	counter := func(name, help, labels string, value float64) {
		fmt.Fprintf(&b, "# HELP %s %s\n# TYPE %s counter\n", name, help, name)
		if labels != "" {
			fmt.Fprintf(&b, "%s{%s} %g\n", name, labels, value)
		} else {
			fmt.Fprintf(&b, "%s %g\n", name, value)
		}
	}

	lbl := func(k, v string) string { return k + "=\"" + escapeLabel(v) + "\"" }
	host := lbl("host", hostname)

	// ---- CPU ---------------------------------------------------------------
	gauge("kula_cpu_usage_percent",
		"Total CPU usage percentage (100 - idle).",
		host, d.CPU.Total.Usage)
	gauge("kula_cpu_user_percent",
		"CPU time spent in user mode.",
		host, d.CPU.Total.User)
	gauge("kula_cpu_system_percent",
		"CPU time spent in kernel mode.",
		host, d.CPU.Total.System)
	gauge("kula_cpu_iowait_percent",
		"CPU time spent waiting for I/O.",
		host, d.CPU.Total.IOWait)
	gauge("kula_cpu_irq_percent",
		"CPU time spent servicing hardware interrupts.",
		host, d.CPU.Total.IRQ)
	gauge("kula_cpu_softirq_percent",
		"CPU time spent servicing software interrupts.",
		host, d.CPU.Total.SoftIRQ)
	gauge("kula_cpu_steal_percent",
		"CPU time stolen by the hypervisor.",
		host, d.CPU.Total.Steal)
	gauge("kula_cpu_cores",
		"Number of logical CPU cores.",
		host, float64(d.CPU.NumCores))
	if d.CPU.Temperature > 0 {
		gauge("kula_cpu_temperature_celsius",
			"CPU package temperature in degrees Celsius.",
			host, d.CPU.Temperature)
	}
	for _, sens := range d.CPU.Sensors {
		gauge("kula_cpu_sensor_temperature_celsius",
			"Per-sensor CPU temperature in degrees Celsius.",
			host+","+lbl("sensor", sens.Name), sens.Value)
	}

	// ---- Load average ------------------------------------------------------
	gauge("kula_load_average_1m",
		"System load average over the last 1 minute.",
		host, d.LoadAvg.Load1)
	gauge("kula_load_average_5m",
		"System load average over the last 5 minutes.",
		host, d.LoadAvg.Load5)
	gauge("kula_load_average_15m",
		"System load average over the last 15 minutes.",
		host, d.LoadAvg.Load15)
	gauge("kula_processes_running",
		"Number of processes currently running.",
		host, float64(d.LoadAvg.Running))
	gauge("kula_processes_total",
		"Total number of processes.",
		host, float64(d.LoadAvg.Total))

	// ---- Memory ------------------------------------------------------------
	gauge("kula_memory_total_bytes",
		"Total installed physical memory in bytes.",
		host, float64(d.Memory.Total))
	gauge("kula_memory_used_bytes",
		"Used physical memory in bytes.",
		host, float64(d.Memory.Used))
	gauge("kula_memory_free_bytes",
		"Free physical memory in bytes.",
		host, float64(d.Memory.Free))
	gauge("kula_memory_available_bytes",
		"Available memory (free + reclaimable caches) in bytes.",
		host, float64(d.Memory.Available))
	gauge("kula_memory_buffers_bytes",
		"Memory used for kernel buffers in bytes.",
		host, float64(d.Memory.Buffers))
	gauge("kula_memory_cached_bytes",
		"Memory used for page cache in bytes.",
		host, float64(d.Memory.Cached))
	gauge("kula_memory_shmem_bytes",
		"Shared memory in bytes.",
		host, float64(d.Memory.Shmem))
	gauge("kula_memory_used_percent",
		"Used physical memory as a percentage of total.",
		host, d.Memory.UsedPercent)

	// ---- Swap --------------------------------------------------------------
	gauge("kula_swap_total_bytes",
		"Total swap space in bytes.",
		host, float64(d.Swap.Total))
	gauge("kula_swap_used_bytes",
		"Used swap space in bytes.",
		host, float64(d.Swap.Used))
	gauge("kula_swap_free_bytes",
		"Free swap space in bytes.",
		host, float64(d.Swap.Free))
	gauge("kula_swap_used_percent",
		"Used swap as a percentage of total swap.",
		host, d.Swap.UsedPercent)

	// ---- Network -----------------------------------------------------------
	for _, iface := range d.Network.Interfaces {
		ifLbl := host + "," + lbl("interface", iface.Name)
		gauge("kula_network_rx_mbps",
			"Network receive throughput in Mbps.",
			ifLbl, iface.RxMbps)
		gauge("kula_network_tx_mbps",
			"Network transmit throughput in Mbps.",
			ifLbl, iface.TxMbps)
		gauge("kula_network_rx_packets_per_second",
			"Network receive packet rate.",
			ifLbl, iface.RxPPS)
		gauge("kula_network_tx_packets_per_second",
			"Network transmit packet rate.",
			ifLbl, iface.TxPPS)
		counter("kula_network_rx_bytes_total",
			"Total bytes received on the interface.",
			ifLbl, float64(iface.RxBytes))
		counter("kula_network_tx_bytes_total",
			"Total bytes transmitted on the interface.",
			ifLbl, float64(iface.TxBytes))
		counter("kula_network_rx_packets_total",
			"Total packets received on the interface.",
			ifLbl, float64(iface.RxPkts))
		counter("kula_network_tx_packets_total",
			"Total packets transmitted on the interface.",
			ifLbl, float64(iface.TxPkts))
		counter("kula_network_rx_errors_total",
			"Total receive errors on the interface.",
			ifLbl, float64(iface.RxErrs))
		counter("kula_network_tx_errors_total",
			"Total transmit errors on the interface.",
			ifLbl, float64(iface.TxErrs))
		counter("kula_network_rx_drops_total",
			"Total receive drops on the interface.",
			ifLbl, float64(iface.RxDrop))
		counter("kula_network_tx_drops_total",
			"Total transmit drops on the interface.",
			ifLbl, float64(iface.TxDrop))
	}
	gauge("kula_tcp_established",
		"Number of TCP connections in ESTABLISHED state.",
		host, float64(d.Network.TCP.CurrEstab))
	gauge("kula_tcp_errors_per_second",
		"TCP input errors per second.",
		host, d.Network.TCP.InErrs)
	gauge("kula_tcp_resets_per_second",
		"TCP resets sent per second.",
		host, d.Network.TCP.OutRsts)
	gauge("kula_sockets_tcp_in_use",
		"Number of TCP sockets in use.",
		host, float64(d.Network.Sockets.TCPInUse))
	gauge("kula_sockets_tcp_time_wait",
		"Number of TCP sockets in TIME_WAIT state.",
		host, float64(d.Network.Sockets.TCPTw))
	gauge("kula_sockets_udp_in_use",
		"Number of UDP sockets in use.",
		host, float64(d.Network.Sockets.UDPInUse))

	// ---- Disk I/O ----------------------------------------------------------
	for _, dev := range d.Disks.Devices {
		devLbl := host + "," + lbl("device", dev.Name)
		gauge("kula_disk_reads_per_second",
			"Disk read operations per second.",
			devLbl, dev.ReadsPerSec)
		gauge("kula_disk_writes_per_second",
			"Disk write operations per second.",
			devLbl, dev.WritesPerSec)
		gauge("kula_disk_read_bytes_per_second",
			"Disk read throughput in bytes per second.",
			devLbl, dev.ReadBytesPS)
		gauge("kula_disk_write_bytes_per_second",
			"Disk write throughput in bytes per second.",
			devLbl, dev.WriteBytesPS)
		gauge("kula_disk_utilization_percent",
			"Disk utilization as a percentage of time the device is busy.",
			devLbl, dev.Utilization)
		if dev.Temperature > 0 {
			gauge("kula_disk_temperature_celsius",
				"Disk drive temperature in degrees Celsius.",
				devLbl, dev.Temperature)
		}
	}

	// ---- Filesystems -------------------------------------------------------
	for _, fs := range d.Disks.FileSystems {
		fsLbl := host + "," + lbl("device", fs.Device) + "," + lbl("mountpoint", fs.MountPoint) + "," + lbl("fstype", fs.FSType)
		gauge("kula_filesystem_size_bytes",
			"Total filesystem size in bytes.",
			fsLbl, float64(fs.Total))
		gauge("kula_filesystem_used_bytes",
			"Used filesystem space in bytes.",
			fsLbl, float64(fs.Used))
		gauge("kula_filesystem_available_bytes",
			"Available filesystem space in bytes.",
			fsLbl, float64(fs.Available))
		gauge("kula_filesystem_used_percent",
			"Used filesystem space as a percentage of total.",
			fsLbl, fs.UsedPct)
	}

	// ---- System ------------------------------------------------------------
	gauge("kula_system_uptime_seconds",
		"System uptime in seconds.",
		host, d.System.Uptime)
	gauge("kula_system_entropy_available",
		"Available entropy in the kernel entropy pool.",
		host, float64(d.System.Entropy))
	clockSync := 0.0
	if d.System.ClockSync {
		clockSync = 1.0
	}
	gauge("kula_system_clock_synced",
		"1 if the system clock is synchronized (NTP/PTP), 0 otherwise.",
		host, clockSync)
	gauge("kula_system_logged_in_users",
		"Number of users currently logged in.",
		host, float64(d.System.UserCount))

	// ---- Processes ---------------------------------------------------------
	gauge("kula_processes_sleeping",
		"Number of sleeping processes.",
		host, float64(d.Process.Sleeping))
	gauge("kula_processes_zombie",
		"Number of zombie processes.",
		host, float64(d.Process.Zombie))
	gauge("kula_processes_blocked",
		"Number of processes blocked on I/O.",
		host, float64(d.Process.Blocked))
	gauge("kula_threads_total",
		"Total number of threads across all processes.",
		host, float64(d.Process.Threads))

	// ---- GPU ---------------------------------------------------------------
	for _, gpu := range d.GPU {
		gpuLbl := host + "," + lbl("index", fmt.Sprintf("%d", gpu.Index)) + "," + lbl("name", gpu.Name)
		if gpu.Temperature > 0 {
			gauge("kula_gpu_temperature_celsius",
				"GPU temperature in degrees Celsius.",
				gpuLbl, gpu.Temperature)
		}
		if gpu.LoadPct > 0 {
			gauge("kula_gpu_load_percent",
				"GPU compute load as a percentage.",
				gpuLbl, gpu.LoadPct)
		}
		if gpu.PowerW > 0 {
			gauge("kula_gpu_power_watts",
				"GPU power consumption in watts.",
				gpuLbl, gpu.PowerW)
		}
		if gpu.VRAMTotal > 0 {
			gauge("kula_gpu_vram_total_bytes",
				"Total GPU VRAM in bytes.",
				gpuLbl, float64(gpu.VRAMTotal))
			gauge("kula_gpu_vram_used_bytes",
				"Used GPU VRAM in bytes.",
				gpuLbl, float64(gpu.VRAMUsed))
			gauge("kula_gpu_vram_used_percent",
				"Used GPU VRAM as a percentage of total.",
				gpuLbl, gpu.VRAMUsedPct)
		}
	}

	// ---- Kula self-metrics -------------------------------------------------
	gauge("kula_self_cpu_percent",
		"Kula process CPU usage percentage.",
		host, d.Self.CPUPercent)
	gauge("kula_self_memory_rss_bytes",
		"Kula process RSS memory in bytes.",
		host, float64(d.Self.MemRSS))
	gauge("kula_self_open_fds",
		"Number of open file descriptors in the Kula process.",
		host, float64(d.Self.FDs))

	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(b.String()))
}

// escapeLabel escapes backslashes, double-quotes, and newlines in Prometheus label values.
func escapeLabel(v string) string {
	v = strings.ReplaceAll(v, `\`, `\\`)
	v = strings.ReplaceAll(v, `"`, `\"`)
	v = strings.ReplaceAll(v, "\n", `\n`)
	return v
}

// Ensure collector types are referenced (avoids import cycle if moved to its own file).
var _ *collector.Sample
