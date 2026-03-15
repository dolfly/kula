package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"kula-szpiegula/internal/collector"
)

// ── Test helpers ──────────────────────────────────────────────────────────────

// newTestSample returns a fully-populated Sample so view functions never hit
// nil-pointer paths during tests.
func newTestSample() *collector.Sample {
	return &collector.Sample{
		Timestamp: time.Now(),
		CPU: collector.CPUStats{
			Total: collector.CPUCoreStats{
				User:    10.5,
				System:  5.2,
				IOWait:  0.3,
				IRQ:     0.1,
				SoftIRQ: 0.2,
				Steal:   0.0,
				Usage:   16.3,
			},
			NumCores:    4,
			Temperature: 55.0,
			Sensors:     []collector.CPUTempSensor{{Name: "core0", Value: 54.0}},
		},
		LoadAvg: collector.LoadAvg{Load1: 0.5, Load5: 0.8, Load15: 1.0, Running: 2, Total: 200},
		Memory: collector.MemoryStats{
			Total:       16 * 1024 * 1024 * 1024,
			Used:        8 * 1024 * 1024 * 1024,
			Free:        4 * 1024 * 1024 * 1024,
			Available:   5 * 1024 * 1024 * 1024,
			Cached:      2 * 1024 * 1024 * 1024,
			Buffers:     512 * 1024 * 1024,
			UsedPercent: 50.0,
		},
		Swap: collector.SwapStats{
			Total:       4 * 1024 * 1024 * 1024,
			Used:        1 * 1024 * 1024 * 1024,
			Free:        3 * 1024 * 1024 * 1024,
			UsedPercent: 25.0,
		},
		Network: collector.NetworkStats{
			Interfaces: []collector.NetInterface{
				{Name: "eth0", RxMbps: 10.5, TxMbps: 2.3, RxPPS: 500, TxPPS: 100},
			},
			TCP:     collector.TCPStats{CurrEstab: 42, InErrs: 0.01, OutRsts: 0.05},
			Sockets: collector.SocketStats{TCPInUse: 30, TCPTw: 5, UDPInUse: 10},
		},
		Disks: collector.DiskStats{
			Devices: []collector.DiskDevice{
				{Name: "sda", ReadsPerSec: 10, WritesPerSec: 5, Utilization: 30.0},
			},
			FileSystems: []collector.FileSystemInfo{
				{MountPoint: "/", Total: 100e9, Used: 40e9, UsedPct: 40.0},
				{MountPoint: "/home", Total: 500e9, Used: 200e9, UsedPct: 40.0},
			},
		},
		System: collector.SystemStats{
			Hostname:    "testhost",
			Uptime:      3600,
			UptimeHuman: "1h 0m",
			ClockSync:   true,
			ClockSource: "ntp",
			Entropy:     3500,
			UserCount:   2,
		},
		Process: collector.ProcessStats{
			Total:    200,
			Running:  2,
			Sleeping: 195,
			Zombie:   1,
			Blocked:  2,
			Threads:  800,
		},
		Self: collector.SelfStats{
			CPUPercent: 0.5,
			MemRSS:     10 * 1024 * 1024,
			FDs:        15,
		},
	}
}

// newTestModel returns a model pre-populated with a sample, ready for View().
func newTestModel(w, h int) model {
	return model{
		width:          w,
		height:         h,
		sample:         newTestSample(),
		now:            time.Now(),
		osName:         "Test Linux",
		kernelVersion:  "6.1.0-test",
		cpuArch:        "amd64",
		version:        "1.0.0",
		showSystemInfo: true,
		activeTab:      tabOverview,
		histCPU:        newRing(),
		histMem:        newRing(),
		histSwap:       newRing(),
		histNetRx:      newRing(),
		histNetTx:      newRing(),
		histDisk:       newRing(),
		histLoad:       newRing(),
		histRunning:    newRing(),
	}
}

// runeKey constructs a KeyMsg for a printable character.
func runeKey(r rune) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}
}

// ── metricRing ────────────────────────────────────────────────────────────────

func TestMetricRingPush(t *testing.T) {
	r := newRing()
	r.push(1.0)
	r.push(2.0)
	r.push(3.0)

	if r.len != 3 {
		t.Fatalf("expected len 3, got %d", r.len)
	}
	// Check the actual values in the buffer (not getAll since it's not full yet)
	if r.buf[0] != 1.0 || r.buf[2] != 3.0 {
		t.Errorf("unexpected values: %v", r.buf[:r.len])
	}
}

func TestMetricRingCap(t *testing.T) {
	r := newRing()
	for i := 0; i < histLen+10; i++ {
		r.push(float64(i))
	}
	if r.len != histLen {
		t.Fatalf("expected len %d, got %d", histLen, r.len)
	}
	// With circular buffer, after histLen+10 pushes, the oldest value should be 10
	// and the newest should be histLen+9
	all := r.getAll()
	if all[0] != float64(10) {
		t.Errorf("expected oldest value 10, got %f", all[0])
	}
	if all[histLen-1] != float64(histLen+9) {
		t.Errorf("expected newest value %d, got %f", histLen+9, all[histLen-1])
	}
}

func TestMetricRingPushNilSample(t *testing.T) {
	m := newTestModel(120, 40)
	// pushSample should be a no-op for nil — must not panic.
	m.pushSample(nil)
}

// ── Helper functions ──────────────────────────────────────────────────────────

func TestClampLines(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		n         int
		wantLines int
		wantEmpty bool
	}{
		{"zero n returns empty", "a\nb\nc", 0, 0, true},
		{"fewer lines than n", "a\nb", 5, 2, false},
		{"exact match", "a\nb\nc", 3, 3, false},
		{"truncates to n", "a\nb\nc\nd\ne", 3, 3, false},
		{"single line no trim", "hello", 5, 1, false},
		{"single line trim to 1", "hello", 1, 1, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := clampLines(tt.input, tt.n)
			if tt.wantEmpty {
				if got != "" {
					t.Errorf("expected empty string, got %q", got)
				}
				return
			}
			gotLines := strings.Split(got, "\n")
			if len(gotLines) != tt.wantLines {
				t.Errorf("expected %d lines, got %d (output=%q)", tt.wantLines, len(gotLines), got)
			}
			// Verify content starts from the top (first line preserved).
			firstInputLine := strings.Split(tt.input, "\n")[0]
			if gotLines[0] != firstInputLine {
				t.Errorf("expected first line %q, got %q", firstInputLine, gotLines[0])
			}
		})
	}
}

func TestFmtBytes(t *testing.T) {
	tests := []struct {
		input uint64
		want  string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1023, "1023 B"},
		{1024, "1.0 KiB"},
		{1536, "1.5 KiB"},
		{1024 * 1024, "1.0 MiB"},
		{1024 * 1024 * 1024, "1.0 GiB"},
		{1024 * 1024 * 1024 * 1024, "1.0 TiB"},
	}
	for _, tt := range tests {
		if got := fmtBytes(tt.input); got != tt.want {
			t.Errorf("fmtBytes(%d) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestFmtMbps(t *testing.T) {
	tests := []struct {
		input float64
		want  string
	}{
		{0.0, "0K"},
		{0.5, "500K"},
		{1.0, "1.0M"},
		{100.5, "100.5M"},
	}
	for _, tt := range tests {
		if got := fmtMbps(tt.input); got != tt.want {
			t.Errorf("fmtMbps(%f) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestPadRight(t *testing.T) {
	if got := padRight("ab", 5); got != "ab   " {
		t.Errorf("padRight pad: got %q", got)
	}
	if got := padRight("abcde", 3); got != "abc" {
		t.Errorf("padRight truncate: got %q", got)
	}
	if got := padRight("abc", 3); got != "abc" {
		t.Errorf("padRight exact: got %q", got)
	}
	if got := padRight("", 3); got != "   " {
		t.Errorf("padRight empty: got %q", got)
	}
}

func TestPadLeft(t *testing.T) {
	if got := padLeft("ab", 5); got != "   ab" {
		t.Errorf("padLeft pad: got %q", got)
	}
	if got := padLeft("abcde", 3); got != "abc" {
		t.Errorf("padLeft truncate: got %q", got)
	}
	if got := padLeft("abc", 3); got != "abc" {
		t.Errorf("padLeft exact: got %q", got)
	}
}

func TestClamp(t *testing.T) {
	tests := []struct{ v, lo, hi, want int }{
		{5, 0, 10, 5},
		{-5, 0, 10, 0},
		{15, 0, 10, 10},
		{0, 0, 0, 0},
		{10, 10, 10, 10},
	}
	for _, tt := range tests {
		if got := clamp(tt.v, tt.lo, tt.hi); got != tt.want {
			t.Errorf("clamp(%d,%d,%d) = %d, want %d", tt.v, tt.lo, tt.hi, got, tt.want)
		}
	}
}

func TestSumInts(t *testing.T) {
	if got := sumInts([]int{1, 2, 3, 4}); got != 10 {
		t.Errorf("sumInts = %d, want 10", got)
	}
	if got := sumInts(nil); got != 0 {
		t.Errorf("sumInts(nil) = %d, want 0", got)
	}
	if got := sumInts([]int{}); got != 0 {
		t.Errorf("sumInts([]) = %d, want 0", got)
	}
}

func TestBarW(t *testing.T) {
	tests := []struct {
		inner int
		want  int
	}{
		// inner=15: computed=clamp(-5,0,50)=0, 0<minBarWidth → 0
		{15, 0},
		// inner=25: computed=clamp(5,0,50)=5, 5<minBarWidth=10 → 0
		{25, 0},
		// inner=35: computed=clamp(15,0,50)=15, ≥10 → 15
		{35, 15},
		// inner=70: computed=clamp(50,0,50)=50 (capped at maxBarWidth)
		{70, 50},
		// inner=100: still capped at 50
		{100, 50},
	}
	for _, tt := range tests {
		if got := barW(tt.inner); got != tt.want {
			t.Errorf("barW(%d) = %d, want %d", tt.inner, got, tt.want)
		}
	}
}

// ── renderMetricBarFull ───────────────────────────────────────────────────────

func TestRenderMetricBarFull_TextOnlyWhenNarrow(t *testing.T) {
	// barW=0 → text-only: no bracket characters in output
	out := renderMetricBarFull("CPU", 50.0, 0, "")
	if strings.Contains(out, "[") || strings.Contains(out, "█") {
		t.Errorf("narrow mode should not render bar chars, got: %q", out)
	}
	if !strings.Contains(out, "50.0") {
		t.Errorf("narrow mode should still show percentage, got: %q", out)
	}
}

func TestRenderMetricBarFull_BarMode(t *testing.T) {
	out := renderMetricBarFull("CPU", 50.0, 20, "8 GiB / 16 GiB")
	if !strings.Contains(out, "[") {
		t.Errorf("bar mode should contain '[', got: %q", out)
	}
	if !strings.Contains(out, "50.0") {
		t.Errorf("bar mode should show percentage, got: %q", out)
	}
}

func TestRenderMetricBarFull_ClampsPct(t *testing.T) {
	// Values outside [0,100] should not panic or produce negative repeats.
	renderMetricBarFull("X", -10.0, 20, "")
	renderMetricBarFull("X", 110.0, 20, "")
}

func TestRenderMetricBarFull_Detail(t *testing.T) {
	out := renderMetricBarFull("RAM", 60.0, 0, "8 GiB / 16 GiB")
	if !strings.Contains(out, "8 GiB") {
		t.Errorf("detail string should appear in output, got: %q", out)
	}
}

// ── renderCPUBreakdown ────────────────────────────────────────────────────────

func TestRenderCPUBreakdown(t *testing.T) {
	c := collector.CPUCoreStats{
		User: 10.5, System: 5.0, IOWait: 0.3,
		IRQ: 0.1, SoftIRQ: 0.2, Steal: 0.0,
	}
	out := renderCPUBreakdown(c)
	for _, label := range []string{"usr", "sys", "io", "irq", "sirq", "stl"} {
		if !strings.Contains(out, label) {
			t.Errorf("breakdown missing label %q in: %q", label, out)
		}
	}
	if !strings.Contains(out, "10.5") {
		t.Errorf("breakdown missing usr value 10.5 in: %q", out)
	}
}

// ── Update: keyboard navigation ───────────────────────────────────────────────

func TestUpdate_TabForward(t *testing.T) {
	m := newTestModel(120, 40)
	m.activeTab = tabOverview

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if next.(model).activeTab != tabCPU {
		t.Errorf("Tab → expected tabCPU, got %d", next.(model).activeTab)
	}
}

func TestUpdate_TabBackward(t *testing.T) {
	m := newTestModel(120, 40)
	m.activeTab = tabCPU

	prev, _ := m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	if prev.(model).activeTab != tabOverview {
		t.Errorf("ShiftTab → expected tabOverview, got %d", prev.(model).activeTab)
	}
}

func TestUpdate_TabWrapsForward(t *testing.T) {
	m := newTestModel(120, 40)
	m.activeTab = tabGPU

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if next.(model).activeTab != tabOverview {
		t.Errorf("Tab wrap → expected tabOverview, got %d", next.(model).activeTab)
	}
}

func TestUpdate_TabWrapsBackward(t *testing.T) {
	m := newTestModel(120, 40)
	m.activeTab = tabOverview

	prev, _ := m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	if prev.(model).activeTab != tabGPU {
		t.Errorf("ShiftTab wrap → expected tabGPU, got %d", prev.(model).activeTab)
	}
}

func TestUpdate_DirectJump(t *testing.T) {
	tests := []struct {
		key  rune
		want tabID
	}{
		{'1', tabOverview},
		{'2', tabCPU},
		{'3', tabMemory},
		{'4', tabNetwork},
		{'5', tabDisk},
		{'6', tabProcesses},
		{'7', tabGPU},
	}
	for _, tt := range tests {
		m := newTestModel(120, 40)
		m.activeTab = tabProcesses // start somewhere different

		next, _ := m.Update(runeKey(tt.key))
		if got := next.(model).activeTab; got != tt.want {
			t.Errorf("key '%c' → expected tab %d, got %d", tt.key, tt.want, got)
		}
	}
}

func TestUpdate_RightLeftArrow(t *testing.T) {
	m := newTestModel(120, 40)
	m.activeTab = tabOverview

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRight})
	if next.(model).activeTab != tabCPU {
		t.Errorf("Right → expected tabCPU, got %d", next.(model).activeTab)
	}

	prev, _ := next.Update(tea.KeyMsg{Type: tea.KeyLeft})
	if prev.(model).activeTab != tabOverview {
		t.Errorf("Left → expected tabOverview, got %d", prev.(model).activeTab)
	}
}

func TestUpdate_HLKeys(t *testing.T) {
	m := newTestModel(120, 40)
	m.activeTab = tabOverview

	next, _ := m.Update(runeKey('l'))
	if next.(model).activeTab != tabCPU {
		t.Errorf("'l' → expected tabCPU, got %d", next.(model).activeTab)
	}

	prev, _ := next.Update(runeKey('h'))
	if prev.(model).activeTab != tabOverview {
		t.Errorf("'h' → expected tabOverview, got %d", prev.(model).activeTab)
	}
}

func TestUpdate_QuitReturnsCmd(t *testing.T) {
	m := newTestModel(120, 40)

	for _, k := range []rune{'q', 'Q'} {
		_, cmd := m.Update(runeKey(k))
		if cmd == nil {
			t.Errorf("key '%c' should return a quit cmd", k)
		}
	}
}

func TestUpdate_WindowSize(t *testing.T) {
	m := newTestModel(0, 0)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 160, Height: 50})
	nm := next.(model)
	if nm.width != 160 || nm.height != 50 {
		t.Errorf("WindowSizeMsg: expected 160×50, got %d×%d", nm.width, nm.height)
	}
}

func TestUpdate_UnknownKeyNoChange(t *testing.T) {
	m := newTestModel(120, 40)
	m.activeTab = tabMemory

	next, cmd := m.Update(runeKey('z'))
	if next.(model).activeTab != tabMemory {
		t.Errorf("unknown key should not change tab")
	}
	if cmd != nil {
		t.Errorf("unknown key should not return a cmd")
	}
}

// ── View: rendering ───────────────────────────────────────────────────────────

// viewLines returns the rendered View() split into lines (ANSI stripped by
// counting via lipgloss.Height for the total, raw split for content checks).
func viewHeight(m model) int {
	return lipgloss.Height(m.View())
}

func TestView_EmptyBeforeSize(t *testing.T) {
	m := newTestModel(0, 0)
	if got := m.View(); got != "" {
		t.Errorf("View() before WindowSizeMsg should be empty, got len=%d", len(got))
	}
}

func TestView_HeightEqualsTerminal(t *testing.T) {
	for _, h := range []int{20, 30, 40, 50} {
		m := newTestModel(120, h)
		got := viewHeight(m)
		if got != h {
			t.Errorf("height=%d: View() has %d lines, want %d", h, got, h)
		}
	}
}

func TestView_NeverExceedsHeight(t *testing.T) {
	// Very small terminals should not overflow.
	for _, h := range []int{5, 8, 10, 15} {
		m := newTestModel(80, h)
		got := viewHeight(m)
		if got > h {
			t.Errorf("height=%d: View() overflows with %d lines", h, got)
		}
	}
}

func TestView_AllTabsRenderWithoutPanic(t *testing.T) {
	for tab := tabID(0); tab < numTabs; tab++ {
		m := newTestModel(120, 40)
		m.activeTab = tab
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("tab %d panicked: %v", tab, r)
				}
			}()
			_ = m.View()
		}()
	}
}

func TestView_NarrowTerminal(t *testing.T) {
	// Very narrow: bars should be absent (text-only mode), no panic.
	m := newTestModel(40, 30)
	view := m.View()
	// The output must still be anchored to height.
	if got := viewHeight(m); got != m.height {
		t.Errorf("narrow: expected %d lines, got %d", m.height, got)
	}
	_ = view
}

func TestView_NilSampleShowsLoading(t *testing.T) {
	m := newTestModel(120, 40)
	m.sample = nil
	// Must not panic; loading message should appear.
	view := m.View()
	if !strings.Contains(view, "Collecting") {
		t.Errorf("nil sample should show loading message, got view of len %d", len(view))
	}
}

func TestView_WideLayoutUsedAboveThreshold(t *testing.T) {
	// Width >= narrowWidth should trigger two-column overview layout.
	m := newTestModel(narrowWidth+10, 40)
	m.activeTab = tabOverview
	view := m.View()
	// In wide mode both "◈ Resources" and "◈ System" panels appear side by side.
	if !strings.Contains(view, "Resources") {
		t.Errorf("wide layout missing Resources panel")
	}
	if !strings.Contains(view, "System") {
		t.Errorf("wide layout missing System panel")
	}
}

func TestView_ShowSystemInfo(t *testing.T) {
	m := newTestModel(120, 40)
	m.activeTab = tabOverview
	m.showSystemInfo = true
	view := m.View()
	if !strings.Contains(view, "testhost") {
		t.Errorf("showSystemInfo=true: hostname not in view")
	}
}

func TestView_HideSystemInfo(t *testing.T) {
	m := newTestModel(120, 40)
	m.activeTab = tabOverview
	m.showSystemInfo = false
	view := m.View()
	if strings.Contains(view, "testhost") {
		t.Errorf("showSystemInfo=false: hostname should not appear in view")
	}
}

func TestView_HeaderContainsTime(t *testing.T) {
	m := newTestModel(120, 40)
	m.now = time.Date(2026, 3, 13, 14, 30, 0, 0, time.UTC)
	view := m.View()
	if !strings.Contains(view, "14:30:00") {
		t.Errorf("header should contain time 14:30:00")
	}
}

func TestView_FooterContainsHints(t *testing.T) {
	m := newTestModel(120, 40)
	view := m.View()
	for _, hint := range []string{"quit", "next", "prev", "jump"} {
		if !strings.Contains(view, hint) {
			t.Errorf("footer missing hint %q", hint)
		}
	}
}

func TestView_CPUTabContent(t *testing.T) {
	m := newTestModel(120, 40)
	m.activeTab = tabCPU
	view := m.View()
	for _, want := range []string{"CPU", "usr", "sys", "io", "Load"} {
		if !strings.Contains(view, want) {
			t.Errorf("CPU tab missing %q", want)
		}
	}
}

func TestView_MemoryTabContent(t *testing.T) {
	m := newTestModel(120, 40)
	m.activeTab = tabMemory
	view := m.View()
	for _, want := range []string{"RAM", "Swap", "Cached", "Buffers"} {
		if !strings.Contains(view, want) {
			t.Errorf("Memory tab missing %q", want)
		}
	}
}

func TestView_NetworkTabContent(t *testing.T) {
	m := newTestModel(120, 40)
	m.activeTab = tabNetwork
	view := m.View()
	for _, want := range []string{"eth0", "TCP", "Established"} {
		if !strings.Contains(view, want) {
			t.Errorf("Network tab missing %q", want)
		}
	}
}

func TestView_DiskTabContent(t *testing.T) {
	m := newTestModel(120, 40)
	m.activeTab = tabDisk
	view := m.View()
	for _, want := range []string{"sda", "Filesystems", "/"} {
		if !strings.Contains(view, want) {
			t.Errorf("Disk tab missing %q", want)
		}
	}
}

func TestView_ProcessesTabContent(t *testing.T) {
	m := newTestModel(120, 40)
	m.activeTab = tabProcesses
	view := m.View()
	for _, want := range []string{"Processes", "Running", "Sleeping", "Kula"} {
		if !strings.Contains(view, want) {
			t.Errorf("Processes tab missing %q", want)
		}
	}
}

func TestView_GPUTabContent(t *testing.T) {
	m := newTestModel(120, 40)
	m.activeTab = tabGPU
	m.sample.GPU = []collector.GPUStats{
		{Name: "NVIDIA RTX 4090", Driver: "nvidia", LoadPct: 45.0, Temperature: 55.0},
	}
	view := m.View()
	for _, want := range []string{"NVIDIA", "driver", "Load", "Temp"} {
		if !strings.Contains(view, want) {
			t.Errorf("GPU tab missing %q", want)
		}
	}
}

// ── pushSample ────────────────────────────────────────────────────────────────

func TestPushSample_PopulatesHistory(t *testing.T) {
	m := newTestModel(120, 40)
	s := newTestSample()
	s.CPU.Total.Usage = 42.0

	m.pushSample(s)

	if m.histCPU.len != 1 {
		t.Fatalf("expected 1 CPU history entry, got %d", m.histCPU.len)
	}
	if m.histCPU.buf[0] != 42.0 {
		t.Errorf("expected CPU history 42.0, got %f", m.histCPU.buf[0])
	}
}

func TestPushSample_AggregatesNetwork(t *testing.T) {
	m := newTestModel(120, 40)
	s := newTestSample()
	s.Network.Interfaces = []collector.NetInterface{
		{RxMbps: 10.0, TxMbps: 5.0},
		{RxMbps: 3.0, TxMbps: 1.0},
	}
	m.pushSample(s)

	if m.histNetRx.len != 1 {
		t.Fatalf("expected 1 NetRx history entry, got %d", m.histNetRx.len)
	}
	if m.histNetRx.buf[0] != 13.0 {
		t.Errorf("NetRx aggregation: expected 13.0, got %f", m.histNetRx.buf[0])
	}
	if m.histNetTx.len != 1 {
		t.Fatalf("expected 1 NetTx history entry, got %d", m.histNetTx.len)
	}
	if m.histNetTx.buf[0] != 6.0 {
		t.Errorf("NetTx aggregation: expected 6.0, got %f", m.histNetTx.buf[0])
	}
}

func TestPushSample_AveragesDiskUtil(t *testing.T) {
	m := newTestModel(120, 40)
	s := newTestSample()
	s.Disks.Devices = []collector.DiskDevice{
		{Utilization: 40.0},
		{Utilization: 60.0},
	}
	m.pushSample(s)

	if m.histDisk.len != 1 {
		t.Fatalf("expected 1 Disk history entry, got %d", m.histDisk.len)
	}
	if m.histDisk.buf[0] != 50.0 {
		t.Errorf("Disk avg util: expected 50.0, got %f", m.histDisk.buf[0])
	}
}
