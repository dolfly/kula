package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"kula-szpiegula/internal/collector"
)

// Layout constants.
const (
	maxBarWidth = 50 // progress bars never grow wider than this
	minBarWidth = 10 // bars narrower than this are replaced by text-only
	narrowWidth = 110 // below this: single-column overview layout
)

// ── Top-level View ────────────────────────────────────────────────────────────

func (m model) View() string {
	if m.width == 0 {
		return ""
	}
	header := m.renderHeader()
	tabBar := m.renderTabBar()
	footer := m.renderFooter()

	used := lipgloss.Height(header) + lipgloss.Height(tabBar) + lipgloss.Height(footer)
	contentH := m.height - used
	if contentH < 1 {
		contentH = 1
	}

	content := m.renderContent(m.width, contentH)
	return lipgloss.JoinVertical(lipgloss.Left, header, tabBar, content, footer)
}

// ── Header ────────────────────────────────────────────────────────────────────

func (m model) renderHeader() string {
	pipe := sHeaderPipe.Render(" │ ")
	left := sLogo.Render(" KULA ")

	if m.showSystemInfo && m.sample != nil {
		hostname := m.sample.System.Hostname
		if hostname == "" {
			hostname = "—"
		}
		left += pipe + sHeaderKey.Render("host ") + sHeaderVal.Render(hostname)
		if m.width >= 80 {
			left += pipe + sHeaderKey.Render("kernel ") + sHeaderVal.Render(m.kernelVersion)
		}
		if m.width >= 100 {
			left += pipe + sHeaderKey.Render("arch ") + sHeaderVal.Render(m.cpuArch)
		}
	}
	if m.sample != nil && m.sample.System.UptimeHuman != "" && m.width >= 70 {
		left += pipe + sHeaderKey.Render("up ") + sHeaderVal.Render(m.sample.System.UptimeHuman)
	}

	right := " " + sHeaderTime.Render(m.now.Format("15:04:05")) + " "
	padW := m.width - lipgloss.Width(left) - lipgloss.Width(right)
	if padW < 0 {
		padW = 0
	}
	return left + sHeaderBg.Render(strings.Repeat(" ", padW)) + right
}

// ── Tab bar ───────────────────────────────────────────────────────────────────

func (m model) renderTabBar() string {
	var tabs string
	for i := tabID(0); i < numTabs; i++ {
		num := fmt.Sprintf("%d", i+1)
		name := tabNames[i]
		if i == m.activeTab {
			tabs += sTabNumAct.Render(num) + sTabAct.Render(name)
		} else {
			tabs += sTabNum.Render(" "+num+" ") + sTabInact.Render(name)
		}
	}
	padW := m.width - lipgloss.Width(tabs)
	if padW > 0 {
		tabs += sTabBarBg.Render(strings.Repeat(" ", padW))
	}
	return tabs
}

// ── Footer ────────────────────────────────────────────────────────────────────

func (m model) renderFooter() string {
	type hint struct{ key, desc string }
	hints := []hint{{"Tab/→", "next"}, {"←", "prev"}, {"1-6", "jump"}, {"q", "quit"}}
	sep := sFooterSep.Render("  ")
	var parts []string
	for _, h := range hints {
		parts = append(parts, sFooterKey.Render(h.key)+" "+sFooterHint.Render(h.desc))
	}
	left := sep + strings.Join(parts, sep)
	padW := m.width - lipgloss.Width(left)
	if padW > 0 {
		left += sFooterBg.Render(strings.Repeat(" ", padW))
	}
	return left
}

// ── Content dispatcher ────────────────────────────────────────────────────────

func (m model) renderContent(w, h int) string {
	var body string
	switch m.activeTab {
	case tabOverview:
		body = m.viewOverview(w, h)
	case tabCPU:
		body = m.viewCPU(w, h)
	case tabMemory:
		body = m.viewMemory(w, h)
	case tabNetwork:
		body = m.viewNetwork(w, h)
	case tabDisk:
		body = m.viewDisk(w, h)
	case tabProcesses:
		body = m.viewProcesses(w, h)
	}
	// Clamp to available height BEFORE adding background fill,
	// so the total View() string never exceeds m.height lines.
	body = clampLines(body, h)
	return sContent.Width(w).Height(h).Render(body)
}

// barW computes a progress bar width for a given panel inner width.
// Returns 0 when the terminal is too narrow for a meaningful bar (→ text-only).
func barW(inner int) int {
	w := clamp(inner-20, 0, maxBarWidth)
	if w < minBarWidth {
		return 0
	}
	return w
}

// ── Overview tab ──────────────────────────────────────────────────────────────

func (m model) viewOverview(w, h int) string {
	if m.sample == nil {
		return centerText("Collecting data…", w, h)
	}

	if w >= narrowWidth {
		gap := 2
		leftW := (w - gap) / 2
		rightW := w - leftW - gap
		left := m.buildOverviewLeft(leftW)
		right := m.buildOverviewRight(rightW)
		return lipgloss.JoinHorizontal(lipgloss.Top, left, strings.Repeat(" ", gap), right)
	}
	return lipgloss.JoinVertical(lipgloss.Left, m.buildOverviewLeft(w), m.buildOverviewRight(w))
}

func (m model) buildOverviewLeft(colW int) string {
	s := m.sample
	inner := colW - 6
	bw := barW(inner)

	lines := []string{
		sPanelTitleAlt.Render("◈ Resources"),
		sDivider.Render(strings.Repeat("─", inner)),
		"",
		renderMetricBarFull("CPU   ", s.CPU.Total.Usage, bw, ""),
		renderMetricBarFull("Memory", s.Memory.UsedPercent, bw,
			fmtBytes(s.Memory.Used)+" / "+fmtBytes(s.Memory.Total)),
		renderMetricBarFull("Swap  ", s.Swap.UsedPercent, bw,
			fmtBytes(s.Swap.Used)+" / "+fmtBytes(s.Swap.Total)),
		"",
		sPanelTitle.Render("Load Average"),
		renderLoadRow(s.LoadAvg.Load1, s.LoadAvg.Load5, s.LoadAvg.Load15),
		"",
		renderLabelVal("Processes", fmt.Sprintf("%d total  %d running  %d zombie",
			s.Process.Total, s.Process.Running, s.Process.Zombie)),
	}
	return sPanel.Width(inner).Render(strings.Join(lines, "\n"))
}

func (m model) buildOverviewRight(colW int) string {
	s := m.sample
	inner := colW - 6
	var rows []string
	rows = append(rows,
		sPanelTitleAlt.Render("◈ System"),
		sDivider.Render(strings.Repeat("─", inner)),
		"",
	)
	if m.showSystemInfo {
		rows = append(rows,
			renderLabelVal("Hostname", s.System.Hostname),
			renderLabelVal("OS      ", m.osName),
			renderLabelVal("Kernel  ", m.kernelVersion),
			renderLabelVal("Arch    ", m.cpuArch),
			"",
		)
	}
	rows = append(rows,
		renderLabelVal("Uptime  ", s.System.UptimeHuman),
		renderLabelVal("Clock   ", clockStr(s.System.ClockSync, s.System.ClockSource)),
		renderLabelVal("Entropy ", fmt.Sprintf("%d bits", s.System.Entropy)),
		renderLabelVal("Users   ", fmt.Sprintf("%d", s.System.UserCount)),
	)
	if len(s.Network.Interfaces) > 0 {
		rows = append(rows, "", sPanelTitle.Render("Network"), "")
		for _, iface := range s.Network.Interfaces {
			rows = append(rows,
				sLabel.Render(padRight(iface.Name, 10))+" "+
					sGood.Render("↓"+fmtMbps(iface.RxMbps))+" "+
					sCrit.Render("↑"+fmtMbps(iface.TxMbps)),
			)
		}
	}
	return sPanel.Width(inner).Render(strings.Join(rows, "\n"))
}

// ── CPU tab ───────────────────────────────────────────────────────────────────

func (m model) viewCPU(w, h int) string {
	if m.sample == nil {
		return centerText("Collecting data…", w, h)
	}
	s := m.sample.CPU
	la := m.sample.LoadAvg
	compact := h < 18

	inner := w - 6
	bw := barW(inner)

	lines := []string{
		sPanelTitleAlt.Render("◈ CPU Usage"),
		sDivider.Render(strings.Repeat("─", inner)),
		"",
		renderMetricBarFull("Total", s.Total.Usage, bw, fmt.Sprintf("%d cores", s.NumCores)),
		"",
		sPanelTitle.Render("Breakdown"),
		"",
		renderCPUBreakdown(s.Total),
		"",
		sPanelTitle.Render("Load Average"),
		"",
		renderLoadRow(la.Load1, la.Load5, la.Load15),
		"",
		renderLabelVal("Threads", fmt.Sprintf("%d running / %d total", la.Running, la.Total)),
	}

	if !compact && s.Temperature > 0 {
		lines = append(lines, "", sPanelTitle.Render("Temperature"), "")
		lines = append(lines, renderLabelVal("Package", fmt.Sprintf("%.1f °C", s.Temperature)))
		for _, sen := range s.Sensors {
			lines = append(lines, renderLabelVal(padRight(sen.Name, 7), fmt.Sprintf("%.1f °C", sen.Value)))
		}
	}
	return sPanel.Width(inner).Render(strings.Join(lines, "\n"))
}

// renderCPUBreakdown renders all CPU time components on one compact text line.
func renderCPUBreakdown(c collector.CPUCoreStats) string {
	fields := []struct {
		label string
		val   float64
	}{
		{"usr", c.User}, {"sys", c.System}, {"io", c.IOWait},
		{"irq", c.IRQ}, {"sirq", c.SoftIRQ}, {"stl", c.Steal},
	}
	var parts []string
	for _, f := range fields {
		parts = append(parts,
			sLabel.Render(f.label+" ")+statusStyle(f.val).Render(fmt.Sprintf("%.1f%%", f.val)),
		)
	}
	return strings.Join(parts, sMuted.Render("  "))
}

// ── Memory tab ────────────────────────────────────────────────────────────────

func (m model) viewMemory(w, h int) string {
	if m.sample == nil {
		return centerText("Collecting data…", w, h)
	}
	_ = h
	mem := m.sample.Memory
	swap := m.sample.Swap
	inner := w - 6
	bw := barW(inner)

	lines := []string{
		sPanelTitleAlt.Render("◈ Memory"),
		sDivider.Render(strings.Repeat("─", inner)),
		"",
		renderMetricBarFull("RAM ", mem.UsedPercent, bw,
			fmtBytes(mem.Used)+" / "+fmtBytes(mem.Total)),
		"",
		sPanelTitle.Render("RAM Breakdown"),
		"",
		renderLabelVal("Used     ", fmtBytes(mem.Used)),
		renderLabelVal("Free     ", fmtBytes(mem.Free)),
		renderLabelVal("Available", fmtBytes(mem.Available)),
		renderLabelVal("Buffers  ", fmtBytes(mem.Buffers)),
		renderLabelVal("Cached   ", fmtBytes(mem.Cached)),
		renderLabelVal("Shared   ", fmtBytes(mem.Shmem)),
		"",
		sPanelTitle.Render("Swap"),
		"",
	}
	if swap.Total == 0 {
		lines = append(lines, sMuted.Render("  No swap configured"))
	} else {
		lines = append(lines,
			renderMetricBarFull("Swap", swap.UsedPercent, bw,
				fmtBytes(swap.Used)+" / "+fmtBytes(swap.Total)),
			"",
			renderLabelVal("Used ", fmtBytes(swap.Used)),
			renderLabelVal("Free ", fmtBytes(swap.Free)),
		)
	}
	return sPanel.Width(inner).Render(strings.Join(lines, "\n"))
}

// ── Network tab ───────────────────────────────────────────────────────────────

func (m model) viewNetwork(w, h int) string {
	if m.sample == nil {
		return centerText("Collecting data…", w, h)
	}
	_ = h
	net := m.sample.Network
	inner := w - 6

	showExtra := w >= 90
	cols := []int{12, 10, 10, 10, 10}
	if showExtra {
		cols = append(cols, 8, 8)
	}

	header := sTH.Render(padRight("Interface", cols[0])) +
		sTH.Render(padLeft("Rx Mbps", cols[1])) +
		sTH.Render(padLeft("Tx Mbps", cols[2])) +
		sTH.Render(padLeft("Rx Pkt/s", cols[3])) +
		sTH.Render(padLeft("Tx Pkt/s", cols[4]))
	if showExtra {
		header += sTH.Render(padLeft("RxDrop", cols[5])) +
			sTH.Render(padLeft("TxDrop", cols[6]))
	}

	lines := []string{
		sPanelTitleAlt.Render("◈ Network Interfaces"),
		sDivider.Render(strings.Repeat("─", inner)),
		"", header,
		sDivider.Render(strings.Repeat("─", sumInts(cols))),
	}
	for _, iface := range net.Interfaces {
		row := sTD.Render(padRight(iface.Name, cols[0])) +
			sTD.Render(padLeft(fmt.Sprintf("%.2f", iface.RxMbps), cols[1])) +
			sTD.Render(padLeft(fmt.Sprintf("%.2f", iface.TxMbps), cols[2])) +
			sTD.Render(padLeft(fmt.Sprintf("%.0f", iface.RxPPS), cols[3])) +
			sTD.Render(padLeft(fmt.Sprintf("%.0f", iface.TxPPS), cols[4]))
		if showExtra {
			row += sTDDim.Render(padLeft(fmt.Sprintf("%d", iface.RxDrop), cols[5])) +
				sTDDim.Render(padLeft(fmt.Sprintf("%d", iface.TxDrop), cols[6]))
		}
		lines = append(lines, row)
	}

	tcp := net.TCP
	sock := net.Sockets
	lines = append(lines,
		"", sPanelTitle.Render("TCP / Sockets"), "",
		renderLabelVal("Established", fmt.Sprintf("%d", tcp.CurrEstab)),
		renderLabelVal("TCP In Use ", fmt.Sprintf("%d", sock.TCPInUse)),
		renderLabelVal("TCP TW     ", fmt.Sprintf("%d", sock.TCPTw)),
		renderLabelVal("UDP In Use ", fmt.Sprintf("%d", sock.UDPInUse)),
		renderLabelVal("In Errors/s", fmt.Sprintf("%.2f", tcp.InErrs)),
		renderLabelVal("Out RSTs/s ", fmt.Sprintf("%.2f", tcp.OutRsts)),
	)
	return sPanel.Width(inner).Render(strings.Join(lines, "\n"))
}

// ── Disk tab ──────────────────────────────────────────────────────────────────

func (m model) viewDisk(w, h int) string {
	if m.sample == nil {
		return centerText("Collecting data…", w, h)
	}
	_ = h
	disks := m.sample.Disks
	inner := w - 6

	lines := []string{
		sPanelTitleAlt.Render("◈ Block Devices"),
		sDivider.Render(strings.Repeat("─", inner)),
		"",
	}

	if len(disks.Devices) > 0 {
		dcols := []int{10, 9, 10, 11, 11, 8}
		lines = append(lines,
			sTH.Render(padRight("Device", dcols[0]))+
				sTH.Render(padLeft("Reads/s", dcols[1]))+
				sTH.Render(padLeft("Writes/s", dcols[2]))+
				sTH.Render(padLeft("Read MB/s", dcols[3]))+
				sTH.Render(padLeft("Write MB/s", dcols[4]))+
				sTH.Render(padLeft("Util%", dcols[5])),
			sDivider.Render(strings.Repeat("─", sumInts(dcols))),
		)
		for _, dev := range disks.Devices {
			util := statusStyle(dev.Utilization).Render(
				padLeft(fmt.Sprintf("%.1f%%", dev.Utilization), dcols[5]))
			lines = append(lines,
				sTD.Render(padRight(dev.Name, dcols[0]))+
					sTD.Render(padLeft(fmt.Sprintf("%.1f", dev.ReadsPerSec), dcols[1]))+
					sTD.Render(padLeft(fmt.Sprintf("%.1f", dev.WritesPerSec), dcols[2]))+
					sTD.Render(padLeft(fmt.Sprintf("%.2f", dev.ReadBytesPS/1e6), dcols[3]))+
					sTD.Render(padLeft(fmt.Sprintf("%.2f", dev.WriteBytesPS/1e6), dcols[4]))+util,
			)
		}
		if disks.Devices[0].Temperature > 0 {
			lines = append(lines, "", sPanelTitle.Render("Temperatures"), "")
			for _, dev := range disks.Devices {
				if dev.Temperature > 0 {
					lines = append(lines, renderLabelVal(padRight(dev.Name, 10),
						fmt.Sprintf("%.1f °C", dev.Temperature)))
				}
			}
		}
	}

	bw := barW(clamp(inner-36, 0, maxBarWidth))
	lines = append(lines, "", sPanelTitle.Render("Filesystems"), "")
	for _, fs := range disks.FileSystems {
		lines = append(lines,
			renderMetricBarFull(padRight(fs.MountPoint, 16), fs.UsedPct, bw,
				fmtBytes(fs.Used)+" / "+fmtBytes(fs.Total)),
		)
	}
	return sPanel.Width(inner).Render(strings.Join(lines, "\n"))
}

// ── Processes tab ─────────────────────────────────────────────────────────────

func (m model) viewProcesses(w, h int) string {
	if m.sample == nil {
		return centerText("Collecting data…", w, h)
	}
	_ = h
	p := m.sample.Process
	inner := w - 6
	bw := clamp(40, minBarWidth, maxBarWidth)

	type stat struct {
		label string
		val   int
		style lipgloss.Style
	}
	stats := []stat{
		{"Running ", p.Running, sGood},
		{"Sleeping", p.Sleeping, sMuted},
		{"Blocked ", p.Blocked, sWarn},
		{"Zombie  ", p.Zombie, sCrit},
	}

	lines := []string{
		sPanelTitleAlt.Render("◈ Processes"),
		sDivider.Render(strings.Repeat("─", inner)),
		"",
		renderLabelVal("Total  ", fmt.Sprintf("%d", p.Total)),
		renderLabelVal("Threads", fmt.Sprintf("%d", p.Threads)),
		"",
	}
	for _, st := range stats {
		pct := 0.0
		if p.Total > 0 {
			pct = float64(st.val) / float64(p.Total) * 100
		}
		filled := int(pct / 100 * float64(bw))
		bar := "[" + st.style.Render(strings.Repeat("█", filled)) +
			sBarEmpty.Render(strings.Repeat("░", bw-filled)) + "]"
		lines = append(lines, sLabel.Render(st.label+"  ")+bar+" "+st.style.Render(fmt.Sprintf("%d", st.val)))
	}

	self := m.sample.Self
	lines = append(lines,
		"", sPanelTitle.Render("Kula Agent"), "",
		renderLabelVal("CPU       ", fmt.Sprintf("%.2f%%", self.CPUPercent)),
		renderLabelVal("RSS Memory", fmtBytes(self.MemRSS)),
		renderLabelVal("Open FDs  ", fmt.Sprintf("%d", self.FDs)),
	)
	return sPanel.Width(inner).Render(strings.Join(lines, "\n"))
}

// ── Render helpers ────────────────────────────────────────────────────────────

// renderMetricBarFull renders a labelled progress bar. When bw == 0 (narrow
// terminal), it falls back to a compact text-only representation.
func renderMetricBarFull(label string, pct float64, bw int, detail string) string {
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	pctStr := statusStyle(pct).Render(fmt.Sprintf("%5.1f%%", pct))

	if bw <= 0 {
		// Text-only mode for very narrow terminals.
		line := sLabel.Render(label) + " " + pctStr
		if detail != "" {
			line += "  " + sMuted.Render(detail)
		}
		return line
	}

	filled := int(pct / 100 * float64(bw))
	bar := "[" + barStyle(pct).Render(strings.Repeat("█", filled)) +
		sBarEmpty.Render(strings.Repeat("░", bw-filled)) + "]"
	line := sLabel.Render(label) + " " + bar + " " + pctStr
	if detail != "" {
		line += "  " + sMuted.Render(detail)
	}
	return line
}

func renderLabelVal(label, val string) string {
	return sLabel.Render(label+":  ") + sValue.Render(val)
}

func renderLoadRow(l1, l5, l15 float64) string {
	return sLabel.Render("  1m: ") + loadStyle(l1).Render(fmt.Sprintf("%.2f", l1)) +
		sLabel.Render("   5m: ") + loadStyle(l5).Render(fmt.Sprintf("%.2f", l5)) +
		sLabel.Render("   15m: ") + loadStyle(l15).Render(fmt.Sprintf("%.2f", l15))
}

func clockStr(synced bool, source string) string {
	if synced {
		return sGood.Render("✓ synced") + sMuted.Render(" ("+source+")")
	}
	return sWarn.Render("✗ not synced")
}

func barStyle(pct float64) lipgloss.Style {
	if pct >= 85 {
		return sBarCrit
	}
	if pct >= 65 {
		return sBarWarn
	}
	return sBarGood
}

func statusStyle(pct float64) lipgloss.Style {
	if pct >= 85 {
		return sCrit
	}
	if pct >= 65 {
		return sWarn
	}
	return sGood
}

func loadStyle(load float64) lipgloss.Style {
	if load >= 4 {
		return sCrit
	}
	if load >= 2 {
		return sWarn
	}
	return sGood
}

func fmtBytes(b uint64) string {
	const k = 1024
	switch {
	case b >= k*k*k*k:
		return fmt.Sprintf("%.1f TiB", float64(b)/(k*k*k*k))
	case b >= k*k*k:
		return fmt.Sprintf("%.1f GiB", float64(b)/(k*k*k))
	case b >= k*k:
		return fmt.Sprintf("%.1f MiB", float64(b)/(k*k))
	case b >= k:
		return fmt.Sprintf("%.1f KiB", float64(b)/k)
	default:
		return fmt.Sprintf("%d B", b)
	}
}

func fmtMbps(mbps float64) string {
	if mbps < 1 {
		return fmt.Sprintf("%.0fK", mbps*1000)
	}
	return fmt.Sprintf("%.1fM", mbps)
}

func padRight(s string, n int) string {
	if len(s) >= n {
		return s[:n]
	}
	return s + strings.Repeat(" ", n-len(s))
}

func padLeft(s string, n int) string {
	if len(s) >= n {
		return s[:n]
	}
	return strings.Repeat(" ", n-len(s)) + s
}

func sumInts(a []int) int {
	t := 0
	for _, v := range a {
		t += v
	}
	return t
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// clampLines truncates s to at most n lines, keeping content anchored to the
// top. This prevents the View() string from exceeding m.height lines, which
// would cause the terminal to show the bottom portion instead of the top.
func clampLines(s string, n int) string {
	if n <= 0 {
		return ""
	}
	lines := strings.Split(s, "\n")
	if len(lines) <= n {
		return s
	}
	return strings.Join(lines[:n], "\n")
}

func centerText(msg string, w, h int) string {
	return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, sMuted.Render(msg))
}
