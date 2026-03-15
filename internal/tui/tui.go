package tui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"kula-szpiegula/internal/collector"
)

const (
	// histLen defines the number of samples to keep in rolling history buffers.
	// At 1-second refresh rate, this provides ~2 minutes of historical data
	// for sparkline graphs and trend analysis.
	histLen = 120
)

// metricRing is a fixed-capacity circular buffer for sparkline history.
type metricRing struct {
	buf []float64
	cap int
	pos int
	len int
}

func newRing() metricRing {
	return metricRing{buf: make([]float64, histLen), cap: histLen}
}

func (r *metricRing) push(v float64) {
	r.buf[r.pos] = v
	r.pos = (r.pos + 1) % r.cap
	if r.len < r.cap {
		r.len++
	}
}

// getAll returns all values in chronological order (oldest to newest)
func (r *metricRing) getAll() []float64 {
	if r.len == 0 {
		return nil
	}
	if r.len < r.cap {
		return r.buf[:r.len]
	}
	// Return buffer starting from oldest element
	result := make([]float64, r.len)
	copy(result, r.buf[r.pos:])
	copy(result[r.cap-r.pos:], r.buf[:r.pos])
	return result
}

// tabID identifies the active dashboard tab.
type tabID int

const (
	tabOverview tabID = iota
	tabCPU
	tabMemory
	tabNetwork
	tabDisk
	tabProcesses
	tabGPU
	numTabs
)

var tabNames = [numTabs]string{
	"Overview", "CPU", "Memory", "Network", "Disk", "Processes", "GPU",
}

type tickMsg time.Time

type model struct {
	coll           *collector.Collector
	refreshRate    time.Duration
	osName         string
	kernelVersion  string
	cpuArch        string
	version        string
	showSystemInfo bool

	activeTab tabID
	width     int
	height    int
	sample    *collector.Sample
	now       time.Time

	// rolling metric histories for sparkline graphs
	histCPU     metricRing
	histMem     metricRing
	histSwap    metricRing
	histNetRx   metricRing
	histNetTx   metricRing
	histDisk    metricRing
	histLoad    metricRing
	histRunning metricRing
}

// RunHeadless launches the full-screen BubbleTea TUI.
func RunHeadless(
	coll *collector.Collector,
	refreshRate time.Duration,
	osName, kernelVersion, cpuArch, version string,
	showSystemInfo bool,
) error {
	sample := coll.Collect()
	m := model{
		coll:           coll,
		refreshRate:    refreshRate,
		osName:         osName,
		kernelVersion:  kernelVersion,
		cpuArch:        cpuArch,
		version:        version,
		showSystemInfo: showSystemInfo,
		sample:         sample,
		now:            time.Now(),
		histCPU:        newRing(),
		histMem:        newRing(),
		histSwap:       newRing(),
		histNetRx:      newRing(),
		histNetTx:      newRing(),
		histDisk:       newRing(),
		histLoad:       newRing(),
		histRunning:    newRing(),
	}
	m.pushSample(sample)
	p := tea.NewProgram(m, tea.WithAltScreen())
	_, err := p.Run()
	return err
}

// pushSample records all relevant metrics from a sample into rolling histories.
func (m *model) pushSample(s *collector.Sample) {
	if s == nil {
		return
	}
	m.histCPU.push(s.CPU.Total.Usage)
	m.histMem.push(s.Memory.UsedPercent)
	m.histSwap.push(s.Swap.UsedPercent)

	var totalRx, totalTx float64
	for _, iface := range s.Network.Interfaces {
		totalRx += iface.RxMbps
		totalTx += iface.TxMbps
	}
	m.histNetRx.push(totalRx)
	m.histNetTx.push(totalTx)

	var totalUtil float64
	for _, dev := range s.Disks.Devices {
		totalUtil += dev.Utilization
	}
	if n := len(s.Disks.Devices); n > 0 {
		totalUtil /= float64(n)
	}
	m.histDisk.push(totalUtil)
	m.histLoad.push(s.LoadAvg.Load1)
	m.histRunning.push(float64(s.Process.Running))
}

func doTick(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m model) Init() tea.Cmd { return doTick(m.refreshRate) }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		// Validate window size to prevent overflow
		if msg.Width > 0 && msg.Height > 0 {
			m.width = msg.Width
			m.height = msg.Height
		}
	case tickMsg:
		m.now = time.Time(msg)
		m.sample = m.coll.Collect()
		m.pushSample(m.sample)
		return m, doTick(m.refreshRate)
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "Q", "ctrl+c":
			return m, tea.Quit
		case "tab", "right", "l":
			m.activeTab = (m.activeTab + 1) % numTabs
		case "shift+tab", "left", "h":
			m.activeTab = (m.activeTab - 1 + numTabs) % numTabs
		case "1":
			if tabOverview < numTabs {
				m.activeTab = tabOverview
			}
		case "2":
			if tabCPU < numTabs {
				m.activeTab = tabCPU
			}
		case "3":
			if tabMemory < numTabs {
				m.activeTab = tabMemory
			}
		case "4":
			if tabNetwork < numTabs {
				m.activeTab = tabNetwork
			}
		case "5":
			if tabDisk < numTabs {
				m.activeTab = tabDisk
			}
		case "6":
			if tabProcesses < numTabs {
				m.activeTab = tabProcesses
			}
		case "7":
			if tabGPU < numTabs {
				m.activeTab = tabGPU
			}
		}
	}
	return m, nil
}
