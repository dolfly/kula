package collector

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

type cpuRaw struct {
	id                                                     string
	user, nice, system, idle, iowait, irq, softirq, steal uint64
	guest, guestNice                                       uint64
}

func parseProcStat() []cpuRaw {
	f, err := os.Open("/proc/stat")
	if err != nil {
		return nil
	}
	defer f.Close()

	var result []cpuRaw
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "cpu") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 8 {
			continue
		}
		r := cpuRaw{id: fields[0]}
		r.user, _ = strconv.ParseUint(fields[1], 10, 64)
		r.nice, _ = strconv.ParseUint(fields[2], 10, 64)
		r.system, _ = strconv.ParseUint(fields[3], 10, 64)
		r.idle, _ = strconv.ParseUint(fields[4], 10, 64)
		if len(fields) > 5 {
			r.iowait, _ = strconv.ParseUint(fields[5], 10, 64)
		}
		if len(fields) > 6 {
			r.irq, _ = strconv.ParseUint(fields[6], 10, 64)
		}
		if len(fields) > 7 {
			r.softirq, _ = strconv.ParseUint(fields[7], 10, 64)
		}
		if len(fields) > 8 {
			r.steal, _ = strconv.ParseUint(fields[8], 10, 64)
		}
		if len(fields) > 9 {
			r.guest, _ = strconv.ParseUint(fields[9], 10, 64)
		}
		if len(fields) > 10 {
			r.guestNice, _ = strconv.ParseUint(fields[10], 10, 64)
		}
		result = append(result, r)
	}
	return result
}

func (r cpuRaw) total() uint64 {
	return r.user + r.nice + r.system + r.idle + r.iowait + r.irq + r.softirq + r.steal
}

func calcCorePct(prev, cur cpuRaw) CPUCoreStats {
	totalDelta := float64(cur.total() - prev.total())
	if totalDelta == 0 {
		return CPUCoreStats{ID: cur.id}
	}

	pct := func(prevVal, curVal uint64) float64 {
		return float64(curVal-prevVal) / totalDelta * 100.0
	}

	cs := CPUCoreStats{
		ID:      cur.id,
		User:    pct(prev.user, cur.user),
		Nice:    pct(prev.nice, cur.nice),
		System:  pct(prev.system, cur.system),
		Idle:    pct(prev.idle, cur.idle),
		IOWait:  pct(prev.iowait, cur.iowait),
		IRQ:     pct(prev.irq, cur.irq),
		SoftIRQ: pct(prev.softirq, cur.softirq),
		Steal:   pct(prev.steal, cur.steal),
		Guest:   pct(prev.guest, cur.guest),
		GuestNi: pct(prev.guestNice, cur.guestNice),
	}
	cs.Usage = 100.0 - cs.Idle
	return cs
}

func (c *Collector) collectCPU(_ float64) CPUStats {
	current := parseProcStat()
	if current == nil {
		return CPUStats{}
	}

	result := CPUStats{}

	if len(c.prevCPU) == len(current) {
		for i, cur := range current {
			cs := calcCorePct(c.prevCPU[i], cur)
			if cur.id == "cpu" {
				result.Total = cs
			} else {
				// Rename cpu0 -> 0 etc. for display
				cs.ID = strings.TrimPrefix(cs.ID, "cpu")
				result.Cores = append(result.Cores, cs)
			}
		}
	} else {
		// First collection — no delta yet
		for _, cur := range current {
			cs := CPUCoreStats{ID: cur.id}
			if cur.id == "cpu" {
				result.Total = cs
			} else {
				cs.ID = strings.TrimPrefix(cs.ID, "cpu")
				result.Cores = append(result.Cores, cs)
			}
		}
	}

	c.prevCPU = current
	return result
}

func collectLoadAvg() LoadAvg {
	data, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return LoadAvg{}
	}
	fields := strings.Fields(string(data))
	if len(fields) < 5 {
		return LoadAvg{}
	}
	la := LoadAvg{}
	la.Load1, _ = strconv.ParseFloat(fields[0], 64)
	la.Load5, _ = strconv.ParseFloat(fields[1], 64)
	la.Load15, _ = strconv.ParseFloat(fields[2], 64)

	parts := strings.Split(fields[3], "/")
	if len(parts) == 2 {
		la.Running, _ = strconv.Atoi(parts[0])
		la.Total, _ = strconv.Atoi(parts[1])
	}
	return la
}

func collectMemory() MemoryStats {
	m := parseMemInfo()
	mem := MemoryStats{
		Total:        m["MemTotal"],
		Free:         m["MemFree"],
		Available:    m["MemAvailable"],
		Buffers:      m["Buffers"],
		Cached:       m["Cached"],
		SReclaimable: m["SReclaimable"],
		SUnreclaim:   m["SUnreclaim"],
		Shmem:        m["Shmem"],
		Dirty:        m["Dirty"],
		Writeback:    m["Writeback"],
		Mapped:       m["Mapped"],
	}
	mem.Used = mem.Total - mem.Free - mem.Buffers - mem.Cached - mem.SReclaimable
	if mem.Total > 0 {
		mem.UsedPercent = float64(mem.Used) / float64(mem.Total) * 100.0
	}
	return mem
}

func collectSwap() SwapStats {
	m := parseMemInfo()
	s := SwapStats{
		Total:  m["SwapTotal"],
		Free:   m["SwapFree"],
		Cached: m["SwapCached"],
	}
	s.Used = s.Total - s.Free
	if s.Total > 0 {
		s.UsedPercent = float64(s.Used) / float64(s.Total) * 100.0
	}
	return s
}

func parseMemInfo() map[string]uint64 {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return nil
	}
	defer f.Close()

	result := make(map[string]uint64)
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		valStr := strings.TrimSpace(parts[1])
		valStr = strings.TrimSuffix(valStr, " kB")
		val, err := strconv.ParseUint(strings.TrimSpace(valStr), 10, 64)
		if err != nil {
			continue
		}
		// Convert kB to bytes
		result[key] = val * 1024
	}
	return result
}

// formatUptime converts seconds to human-readable uptime.
func formatUptime(secs float64) string {
	d := int(secs) / 86400
	h := (int(secs) % 86400) / 3600
	m := (int(secs) % 3600) / 60
	if d > 0 {
		return fmt.Sprintf("%dd %dh %dm", d, h, m)
	}
	if h > 0 {
		return fmt.Sprintf("%dh %dm", h, m)
	}
	return fmt.Sprintf("%dm", m)
}
