package collector

import (
	"os"
	"path/filepath"
	"strings"
)

func (c *Collector) collectSysfsGPUStats(info GPUInfo, s *GPUStats, elapsed float64) {
	// 1. Temperature from Hwmon
	if info.HwmonPath != "" {
		tempMilliC := c.parseInt(readSysfsFile(filepath.Join(info.HwmonPath, "temp1_input")), 10, 64, "gpu.temp")
		if tempMilliC > 0 {
			s.Temperature = float64(tempMilliC) / 1000.0
			c.debugf("gpu[%d]: temp = %.1f C", s.Index, s.Temperature)
		}

		// Power from Hwmon (AMDGPU usually power1_average, others power1_input)
		powerMicroW := c.parseInt(readSysfsFile(filepath.Join(info.HwmonPath, "power1_average")), 10, 64, "gpu.power.avg")
		if powerMicroW == 0 {
			powerMicroW = c.parseInt(readSysfsFile(filepath.Join(info.HwmonPath, "power1_input")), 10, 64, "gpu.power.input")
		}
		if powerMicroW > 0 {
			s.PowerW = float64(powerMicroW) / 1e6
			c.debugf("gpu[%d]: power = %.2f W", s.Index, s.PowerW)
		}

		// Energy derivation for i915 (Intel)
		energyMicroJString := readSysfsFile(filepath.Join(info.HwmonPath, "energy1_input"))
		if energyMicroJString != "" {
			energyMicroJ := c.parseUint(energyMicroJString, 10, 64, "gpu.energy")
			if energyMicroJ > 0 {
				key := "energy:" + info.DRMPath
				if prev, ok := c.prevEnergy[key]; ok && elapsed > 0 {
					if energyMicroJ < prev {
						// Counter decreased — driver reload, device reset, or suspend/resume.
						// Do not calculate a delta: the reading is not a counter wrap.
						c.debugf("gpu[%d]: energy counter reset (prev=%d, now=%d), skipping sample", s.Index, prev, energyMicroJ)
					} else {
						delta := energyMicroJ - prev
						// If s.PowerW was set from power1_input/average, only override if it's 0 or we prefer energy derivation
						// Usually energy imputation is more accurate on older Intel
						if s.PowerW == 0 {
							s.PowerW = float64(delta) / 1e6 / elapsed
							c.debugf("gpu[%d]: derived power = %.2f W (delta: %d, dt: %.2fs)", s.Index, s.PowerW, delta, elapsed)
						}
					}
				}
				c.prevEnergy[key] = energyMicroJ
			}
		}
	}

	// 2. VRAM usage (AMD)
	vramTotal := c.parseUint(readSysfsFile(filepath.Join(info.DRMPath, "device/mem_info_vram_total")), 10, 64, "gpu.vram.total")
	vramUsed := c.parseUint(readSysfsFile(filepath.Join(info.DRMPath, "device/mem_info_vram_used")), 10, 64, "gpu.vram.used")
	if vramTotal > 0 {
		s.VRAMTotal = vramTotal
		s.VRAMUsed = vramUsed
		s.VRAMUsedPct = round2(float64(vramUsed) / float64(vramTotal) * 100.0)
		c.debugf("gpu[%d]: vram = %d/%d (%.1f%%)", s.Index, vramUsed, vramTotal, s.VRAMUsedPct)
	}

	// 3. GPU Load (AMD)
	loadPctStr := readSysfsFile(filepath.Join(info.DRMPath, "device/gpu_busy_percent"))
	if loadPctStr != "" {
		loadPct := c.parseUint(loadPctStr, 10, 64, "gpu.load")
		if loadPct > 0 {
			s.LoadPct = float64(loadPct)
			c.debugf("gpu[%d]: load = %.1f%%", s.Index, s.LoadPct)
		}
	}
}

func readSysfsFile(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}
