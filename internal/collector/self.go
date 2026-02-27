package collector

import (
	"os"
	"runtime"
	"strconv"
	"strings"
)

type selfRaw struct {
	utime  uint64
	stime  uint64
}

func (c *Collector) collectSelf(elapsed float64) SelfStats {
	s := SelfStats{}

	// Read /proc/self/stat for CPU
	data, err := os.ReadFile("/proc/self/stat")
	if err == nil {
		content := string(data)
		// Find fields after the command name (enclosed in parens)
		idx := strings.LastIndex(content, ")")
		if idx >= 0 && idx+2 < len(content) {
			fields := strings.Fields(content[idx+2:])
			// utime is field index 11 (0-based from after state), stime is 12
			if len(fields) > 12 {
				cur := selfRaw{}
				cur.utime, _ = strconv.ParseUint(fields[11], 10, 64)
				cur.stime, _ = strconv.ParseUint(fields[12], 10, 64)

				if c.prevSelf.utime > 0 && elapsed > 0 {
					// Clock ticks per second is typically 100
					const clockTick = 100
					uDelta := cur.utime - c.prevSelf.utime
					sDelta := cur.stime - c.prevSelf.stime
					totalDelta := float64(uDelta+sDelta) / clockTick
					s.CPUPercent = totalDelta / elapsed * 100.0
				}
				c.prevSelf = cur
			}
		}
	}

	// Read /proc/self/status for memory
	statusData, err := os.ReadFile("/proc/self/status")
	if err == nil {
		for _, line := range strings.Split(string(statusData), "\n") {
			if strings.HasPrefix(line, "VmRSS:") {
				val := parseStatusKB(line)
				s.MemRSS = val * 1024
			} else if strings.HasPrefix(line, "VmSize:") {
				val := parseStatusKB(line)
				s.MemVMS = val * 1024
			} else if strings.HasPrefix(line, "Threads:") {
				parts := strings.Fields(line)
				if len(parts) >= 2 {
					s.NumThreads, _ = strconv.Atoi(parts[1])
				}
			}
		}
	}

	// Count FDs
	if fds, err := os.ReadDir("/proc/self/fd"); err == nil {
		s.FDs = len(fds)
	}

	_ = runtime.NumGoroutine() // Could add goroutine count

	return s
}

func parseStatusKB(line string) uint64 {
	parts := strings.Fields(line)
	if len(parts) >= 2 {
		val, _ := strconv.ParseUint(parts[1], 10, 64)
		return val
	}
	return 0
}
