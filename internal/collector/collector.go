package collector

import (
	"kula/internal/config"
	"log"
	"sync"
	"time"
)

var (
	procPath   = "/proc"
	sysPath    = "/sys"
	runPath    = "/run"
	varRunPath = "/var/run"
)

// Collector orchestrates all metric sub-collectors.
type Collector struct {
	mu        sync.RWMutex
	cfg       config.GlobalConfig
	collCfg   config.CollectionConfig
	latest    *Sample
	prevCPU   []cpuRaw
	prevNet   map[string]netRaw
	prevDisk  map[string]diskRaw
	prevSelf  selfRaw
	prevTCP   tcpRaw
	prevEnergy map[string]uint64 // for Intel energy derivation
	gpus      []GPUInfo
	storageDir string
	prevTime  time.Time
	debugDone bool // set after the first Collect(); suppresses repeated debug logs
}

func New(cfg config.GlobalConfig, collCfg config.CollectionConfig, storageDir string) *Collector {
	return &Collector{
		cfg:        cfg,
		collCfg:    collCfg,
		storageDir: storageDir,
		prevNet:    make(map[string]netRaw),
		prevDisk:   make(map[string]diskRaw),
		prevEnergy: make(map[string]uint64),
	}
}

// debugf logs a formatted message only when web.logging.level = "debug" is set
// AND only during the first collection cycle. Subsequent calls are no-ops.
func (c *Collector) debugf(format string, args ...any) {
	if c.collCfg.DebugLog && !c.debugDone {
		log.Printf(format, args...)
	}
}

// Collect gathers all metrics and returns a Sample.
func (c *Collector) Collect() *Sample {
	now := time.Now()
	var elapsed float64
	if c.prevTime.IsZero() {
		elapsed = 1
	} else {
		elapsed = now.Sub(c.prevTime).Seconds()
		if elapsed <= 0 {
			elapsed = 1
		}
	}
	c.prevTime = now

	s := &Sample{
		Timestamp: now,
	}

	s.CPU = c.collectCPU(elapsed)
	s.CPU.Temperature, s.CPU.Sensors = c.collectCPUTemperature()
	s.LoadAvg = c.collectLoadAvg()
	s.Memory = collectMemory()
	s.Swap = collectSwap()
	s.Network = c.collectNetwork(elapsed)
	s.Disks = c.collectDisks(elapsed)
	s.System = c.collectSystem()
	s.Process = collectProcesses()
	s.Self = c.collectSelf(elapsed)
	s.GPU = c.collectGPUs(elapsed)

	c.mu.Lock()
	c.latest = s
	c.mu.Unlock()

	// Suppress debug logs after the first collection cycle — devices and
	// interfaces don't change at runtime, so repeating them every second
	// would flood the log.
	c.debugDone = true

	return s
}

// Latest returns the most recently collected sample.
func (c *Collector) Latest() *Sample {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.latest
}
