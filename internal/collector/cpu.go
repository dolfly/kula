package collector

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type cpuRaw struct {
	id                                                    string
	user, nice, system, idle, iowait, irq, softirq, steal uint64
	guest, guestNice                                      uint64
}

type sysSensor struct {
	Name string
	Path string
}

var (
	// Cached sensors for CPU temperature so we don't scan on every tick.
	// Nil means not yet initialized. Empty means initialized but not found.
	sysTempSensors []sysSensor
)

func parseProcStat() []cpuRaw {
	f, err := os.Open(filepath.Join(procPath, "stat"))
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()

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
		r.user = parseUint(fields[1], 10, 64, "cpu.user")
		r.nice = parseUint(fields[2], 10, 64, "cpu.nice")
		r.system = parseUint(fields[3], 10, 64, "cpu.system")
		r.idle = parseUint(fields[4], 10, 64, "cpu.idle")
		if len(fields) > 5 {
			r.iowait = parseUint(fields[5], 10, 64, "cpu.iowait")
		}
		if len(fields) > 6 {
			r.irq = parseUint(fields[6], 10, 64, "cpu.irq")
		}
		if len(fields) > 7 {
			r.softirq = parseUint(fields[7], 10, 64, "cpu.softirq")
		}
		if len(fields) > 8 {
			r.steal = parseUint(fields[8], 10, 64, "cpu.steal")
		}
		if len(fields) > 9 {
			r.guest = parseUint(fields[9], 10, 64, "cpu.guest")
		}
		if len(fields) > 10 {
			r.guestNice = parseUint(fields[10], 10, 64, "cpu.guest_nice")
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
		return CPUCoreStats{}
	}

	pct := func(prevVal, curVal uint64) float64 {
		return round2(float64(curVal-prevVal) / totalDelta * 100.0)
	}

	idlePct := pct(prev.idle, cur.idle)
	cs := CPUCoreStats{
		User:    pct(prev.user, cur.user),
		System:  pct(prev.system, cur.system),
		IOWait:  pct(prev.iowait, cur.iowait),
		IRQ:     pct(prev.irq, cur.irq),
		SoftIRQ: pct(prev.softirq, cur.softirq),
		Steal:   pct(prev.steal, cur.steal),
	}
	cs.Usage = round2(100.0 - idlePct)
	return cs
}

func (c *Collector) collectCPU(_ float64) CPUStats {
	current := parseProcStat()
	if current == nil {
		return CPUStats{}
	}

	result := CPUStats{}
	var numCores int

	if len(c.prevCPU) == len(current) {
		for i, cur := range current {
			if cur.id == "cpu" {
				result.Total = calcCorePct(c.prevCPU[i], cur)
			} else {
				numCores++
			}
		}
	} else {
		// First collection — no delta yet
		for _, cur := range current {
			if cur.id == "cpu" {
				result.Total = CPUCoreStats{}
			} else {
				numCores++
			}
		}
	}

	result.NumCores = numCores
	c.prevCPU = current
	return result
}

func collectLoadAvg() LoadAvg {
	data, err := os.ReadFile(filepath.Join(procPath, "loadavg"))
	if err != nil {
		return LoadAvg{}
	}
	fields := strings.Fields(string(data))
	if len(fields) < 5 {
		return LoadAvg{}
	}
	la := LoadAvg{}
	la.Load1 = parseFloat(fields[0], 64, "loadavg.1")
	la.Load5 = parseFloat(fields[1], 64, "loadavg.5")
	la.Load15 = parseFloat(fields[2], 64, "loadavg.15")

	parts := strings.Split(fields[3], "/")
	if len(parts) == 2 {
		la.Running, _ = strconv.Atoi(parts[0])
		la.Total, _ = strconv.Atoi(parts[1])
	}
	return la
}

// collectCPUTemperature reads the CPU temperature from sysfs.
func collectCPUTemperature() (float64, []CPUTempSensor) {
	if sysTempSensors == nil {
		sysTempSensors = discoverCPUTempPath()
	}

	if len(sysTempSensors) == 0 {
		return 0, nil // No temperature sensors found
	}

	var primaryTemp float64
	var sensors []CPUTempSensor

	for _, sensor := range sysTempSensors {
		data, err := os.ReadFile(sensor.Path)
		if err != nil {
			continue
		}

		valStr := strings.TrimSpace(string(data))
		tempMilliC := parseUint(valStr, 10, 64, "cpu.temp")
		if tempMilliC == 0 && valStr != "0" {
			continue
		}

		tempC := round2(float64(tempMilliC) / 1000.0)

		sensors = append(sensors, CPUTempSensor{
			Name:  sensor.Name,
			Value: tempC,
		})
	}

	// Filter out synthetic Tctl (thermal pressure) if we have physical sensors like Tccd or Tdie
	var hasPhysicalAMD bool
	for _, s := range sensors {
		if strings.HasPrefix(s.Name, "Tccd") || s.Name == "Tdie" {
			hasPhysicalAMD = true
			break
		}
	}

	if hasPhysicalAMD {
		var filtered []CPUTempSensor
		for _, s := range sensors {
			if s.Name != "Tctl" {
				filtered = append(filtered, s)
			}
		}
		sensors = filtered
	}

	// Make the first sensor (often Tctl or temp1_input on others) the primary temperature
	if primaryTemp == 0 && len(sensors) > 0 {
		// Prefer a package/control/die temperature for primary if multiple exist, otherwise just use the first
		for _, s := range sensors {
			sNameLow := strings.ToLower(s.Name)
			if s.Name == "Tctl" || s.Name == "Tdie" || strings.Contains(sNameLow, "package") {
				primaryTemp = s.Value
				break
			}
		}
		if primaryTemp == 0 {
			primaryTemp = sensors[0].Value
		}
	}

	return primaryTemp, sensors
}

// discoverCPUTempPath attempts to find files containing CPU temperatures.
func discoverCPUTempPath() []sysSensor {
	var sensors []sysSensor

	// 1. Try hwmon (usually more reliable on x86, e.g. coretemp, k10temp, zenpower)
	hwmonPath := filepath.Join(sysPath, "class", "hwmon")
	entries, err := os.ReadDir(hwmonPath)
	if err == nil {
		for _, entry := range entries {
			if !entry.IsDir() && entry.Type()&os.ModeSymlink == 0 {
				continue
			}

			dir := filepath.Join(hwmonPath, entry.Name())

			// Some systems nest hwmon under device module
			nameFile := filepath.Join(dir, "name")
			nameData, err := os.ReadFile(nameFile)
			if err != nil {
				continue
			}
			name := strings.TrimSpace(string(nameData))

			// Common CPU temperature drivers
			if name == "coretemp" || name == "k10temp" || name == "zenpower" || name == "cpu_thermal" {
				// We can just scan for all temp*_input
				inputs, _ := filepath.Glob(filepath.Join(dir, "temp*_input"))
				for _, input := range inputs {
					sensorName := name
					prefix := strings.TrimSuffix(filepath.Base(input), "_input")

					// Look for corresponding label to get names like Tctl, Tccd1
					labelFile := filepath.Join(dir, prefix+"_label")
					if labelData, err := os.ReadFile(labelFile); err == nil {
						lbl := strings.TrimSpace(string(labelData))
						if lbl != "" {
							sensorName = lbl
						}
					} else {
						// If no label, use Name + prefix (e.g. coretemp_temp1)
						sensorName = fmt.Sprintf("%s_%s", name, prefix)
					}

					sensors = append(sensors, sysSensor{
						Name: sensorName,
						Path: input,
					})
				}
				if len(sensors) > 0 {
					return sensors // Found our sensors
				}
			}
		}
	}

	// 2. Try thermal_zone (Common on ARM/Raspberry Pi)
	thermalPath := filepath.Join(sysPath, "class", "thermal")
	entries, err = os.ReadDir(thermalPath)
	if err == nil {
		for _, entry := range entries {
			if !strings.HasPrefix(entry.Name(), "thermal_zone") {
				continue
			}

			dir := filepath.Join(thermalPath, entry.Name())
			typeFile := filepath.Join(dir, "type")
			typeData, err := os.ReadFile(typeFile)
			if err != nil {
				continue
			}

			typ := strings.TrimSpace(string(typeData))
			// Usually named something like "cpu-thermal", "cpu_thermal", "x86_pkg_temp"
			if strings.Contains(strings.ToLower(typ), "cpu") || strings.Contains(strings.ToLower(typ), "pkg_temp") {
				tempFile := filepath.Join(dir, "temp")
				if _, err := os.Stat(tempFile); err == nil {
					sensors = append(sensors, sysSensor{
						Name: typ,
						Path: tempFile,
					})
				}
			}
		}

		if len(sensors) > 0 {
			return sensors
		}

		// Fallback: If no explicit 'cpu' type is found, thermal_zone0 is often the main CPU temp
		temp0 := filepath.Join(thermalPath, "thermal_zone0", "temp")
		if _, err := os.Stat(temp0); err == nil {
			sensors = append(sensors, sysSensor{
				Name: "thermal_zone0",
				Path: temp0,
			})
			return sensors
		}
	}

	// Ensure we return an initialized slice so we don't try to detect over and over if none found
	return make([]sysSensor, 0)
}

// DetectTjMax returns the maximum critical temperature in Celsius, or 0 if undetected.
func DetectTjMax() float64 {
	if sysTempSensors == nil {
		sysTempSensors = discoverCPUTempPath()
	}

	var maxCrit float64
	for _, sensor := range sysTempSensors {
		critPath := strings.TrimSuffix(sensor.Path, "_input") + "_crit"
		data, err := os.ReadFile(critPath)
		if err == nil {
			valStr := strings.TrimSpace(string(data))
			tempMilliC := parseUint(valStr, 10, 64, "cpu.temp_crit")
			if tempMilliC > 0 {
				val := float64(tempMilliC) / 1000.0
				if val > maxCrit {
					maxCrit = val
				}
			}
		}
	}
	return maxCrit
}

func collectMemory() MemoryStats {
	m := parseMemInfo()
	mem := MemoryStats{
		Total:     m["MemTotal"],
		Free:      m["MemFree"],
		Available: m["MemAvailable"],
		Buffers:   m["Buffers"],
		Cached:    m["Cached"],
		Shmem:     m["Shmem"],
	}
	mem.Used = mem.Total - mem.Free - mem.Buffers - mem.Cached
	if mem.Total > 0 {
		mem.UsedPercent = round2(float64(mem.Used) / float64(mem.Total) * 100.0)
	}
	return mem
}

func collectSwap() SwapStats {
	m := parseMemInfo()
	s := SwapStats{
		Total: m["SwapTotal"],
		Free:  m["SwapFree"],
	}
	s.Used = s.Total - s.Free
	if s.Total > 0 {
		s.UsedPercent = round2(float64(s.Used) / float64(s.Total) * 100.0)
	}
	return s
}

func parseMemInfo() map[string]uint64 {
	f, err := os.Open(filepath.Join(procPath, "meminfo"))
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()

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
