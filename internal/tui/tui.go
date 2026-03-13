package tui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"kula-szpiegula/internal/collector"
)

const histLen = 120 // number of samples to keep (~2 min at 1s)

// metricRing is a fixed-capacity rolling buffer for sparkline history.
type metricRing struct {
	buf []float64
	cap int
}

func newRing() metricRing {
	return metricRing{buf: make([]float64, 0, histLen), cap: histLen}
}

func (r *metricRing) push(v float64) {
	if len(r.buf) >= r.cap {
		r.buf = r.buf[1:]
	}
	r.buf = append(r.buf, v)
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
	numTabs
)

var tabNames = [numTabs]string{
	"Overview", "CPU", "Memory", "Network", "Disk", "Processes",
}

type tickMsg time.Time

type model struct {
	coll           *collector.Collector
	refreshRate    time.Duration
	osName         string
	kernelVersion  string
	cpuArch        string
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
	osName, kernelVersion, cpuArch string,
	showSystemInfo bool,
) error {
	sample := coll.Collect()
	m := model{
		coll:           coll,
		refreshRate:    refreshRate,
		osName:         osName,
		kernelVersion:  kernelVersion,
		cpuArch:        cpuArch,
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
		m.width = msg.Width
		m.height = msg.Height
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
			m.activeTab = tabOverview
		case "2":
			m.activeTab = tabCPU
		case "3":
			m.activeTab = tabMemory
		case "4":
			m.activeTab = tabNetwork
		case "5":
			m.activeTab = tabDisk
		case "6":
			m.activeTab = tabProcesses
		}
	}
	return m, nil
}
