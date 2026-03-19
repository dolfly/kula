package main

import (
	"bufio"
	"flag"
	"fmt"
	"log"
	"math"
	"math/rand"
	"os"
	"strings"
	"time"

	"kula/internal/collector"
	"kula/internal/config"
	"kula/internal/storage"
)

func main() {
	days := flag.Int("days", 7, "number of days of generated data to simulate (1s resolution)")
	cfgPath := flag.String("config", "config.yaml", "path to configuration file")
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	fmt.Printf("WARNING: This will generate %d days of mock data into '%s'.\n", *days, cfg.Storage.Directory)
	fmt.Printf("This may overwrite or mix with your existing data.\n")
	fmt.Print("Are you sure you want to proceed? (y/N): ")

	reader := bufio.NewReader(os.Stdin)
	response, _ := reader.ReadString('\n')
	response = strings.TrimSpace(strings.ToLower(response))

	if response != "y" && response != "yes" {
		fmt.Println("Aborted by user.")
		os.Exit(0)
	}

	fmt.Printf("Initializing storage at %s\n", cfg.Storage.Directory)
	store, err := storage.NewStore(cfg.Storage)
	if err != nil {
		log.Fatalf("Failed to initialize storage: %v", err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			log.Printf("Error closing storage: %v", err)
		}
	}()

	totalSamples := *days * 24 * 60 * 60
	fmt.Printf("Generating %d samples (%d days of 1s resolution)...\n", totalSamples, *days)

	now := time.Now()
	startTime := now.Add(-time.Duration(totalSamples) * time.Second)

	const (
		memTotal  = uint64(8 * 1024 * 1024 * 1024) // 8 GB
		swapTotal = uint64(2 * 1024 * 1024 * 1024) // 2 GB
	)

	// Stateful values that random-walk between samples
	cpuBase := 15.0
	memUsed := uint64(1.5 * 1024 * 1024 * 1024)
	swapUsed := uint64(200 * 1024 * 1024)
	rxMbps := 5.0
	txMbps := 2.0
	diskUtil := [2]float64{5.0, 2.0}
	diskReadBps := [2]float64{1024 * 1024, 512 * 1024}
	diskWriteBps := [2]float64{512 * 1024, 256 * 1024}

	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	startGenTime := time.Now()

	for i := 0; i < totalSamples; i++ {
		ts := startTime.Add(time.Duration(i) * time.Second)

		// Diurnal baseline: business hours push a higher CPU load
		hourFrac := float64(ts.Hour())/24.0 + float64(ts.Minute())/1440.0
		diurnal := 10.0 + 30.0*math.Max(0, math.Sin((hourFrac-0.25)*math.Pi))

		// Random walk with drift toward diurnal baseline
		cpuBase += rng.Float64()*4 - 2
		cpuBase = clamp(cpuBase, 0, 100)
		cpuBase += (diurnal - cpuBase) * 0.002

		// Occasional CPU spikes (~1% of seconds)
		if rng.Float64() < 0.001 {
			cpuBase = math.Min(cpuBase+rng.Float64()*40, 100)
		}

		memUsed = uint64(clamp(float64(memUsed)+(rng.Float64()*20-10)*1024*1024,
			256*1024*1024, float64(memTotal-512*1024*1024)))
		swapUsed = uint64(clamp(float64(swapUsed)+(rng.Float64()*4-2)*1024*1024,
			0, float64(swapTotal)))

		rxMbps = clamp(rxMbps+(rng.Float64()*2-1), 0.1, 1000)
		txMbps = clamp(txMbps+(rng.Float64()*1-0.5), 0.1, 1000)
		txMbps += (rxMbps*0.3 - txMbps) * 0.01 // tx loosely tracks rx

		for d := 0; d < 2; d++ {
			diskUtil[d] = clamp(diskUtil[d]+(rng.Float64()*6-3), 0, 100)
			diskReadBps[d] = clamp(diskReadBps[d]+(rng.Float64()*200-100)*1024, 0, 500*1024*1024)
			diskWriteBps[d] = clamp(diskWriteBps[d]+(rng.Float64()*100-50)*1024, 0, 500*1024*1024)
		}

		memFree := memTotal - memUsed
		memCached := memTotal / 10
		memBuffers := memTotal / 20

		sample := &collector.Sample{
			Timestamp: ts,
			CPU: collector.CPUStats{
				Total: collector.CPUCoreStats{
					User:   round2(cpuBase * 0.6),
					System: round2(cpuBase * 0.25),
					IOWait: round2(cpuBase * 0.1),
					Usage:  round2(cpuBase),
				},
				NumCores: 4,
			},
			LoadAvg: collector.LoadAvg{
				Load1:   round2(cpuBase / 20.0),
				Load5:   round2(cpuBase / 25.0),
				Load15:  round2(cpuBase / 30.0),
				Running: 1 + int(cpuBase/25),
				Total:   120,
			},
			Memory: collector.MemoryStats{
				Total:       memTotal,
				Used:        memUsed,
				Free:        memFree,
				Available:   memFree + memCached,
				Cached:      memCached,
				Buffers:     memBuffers,
				Shmem:       memTotal / 50, // ~2% of RAM as shared mem
				UsedPercent: round2(float64(memUsed) / float64(memTotal) * 100),
			},
			Swap: collector.SwapStats{
				Total:       swapTotal,
				Used:        swapUsed,
				Free:        swapTotal - swapUsed,
				UsedPercent: round2(float64(swapUsed) / float64(swapTotal) * 100),
			},
			Network: collector.NetworkStats{
				Interfaces: []collector.NetInterface{
					{
						Name:    "eth0",
						RxBytes: uint64(rxMbps*1e6/8) * uint64(i),
						TxBytes: uint64(txMbps*1e6/8) * uint64(i),
						RxMbps:  round2(rxMbps),
						TxMbps:  round2(txMbps),
						RxPPS:   round2(rxMbps * 100),
						TxPPS:   round2(txMbps * 100),
					},
				},
				TCP: collector.TCPStats{
					CurrEstab: uint64(20 + int(cpuBase/5)),
					InErrs:    float64(int(cpuBase/50) % 3),
					OutRsts:   float64(int(cpuBase/20) % 10),
				},
				Sockets: collector.SocketStats{
					TCPInUse: 20 + int(cpuBase/5),
					UDPInUse: 5,
					TCPTw:    int(cpuBase / 10),
				},
			},
			Disks: collector.DiskStats{
				Devices: []collector.DiskDevice{
					{
						Name:         "sda",
						ReadsPerSec:  round2(diskUtil[0] * 10),
						WritesPerSec: round2(diskUtil[0] * 5),
						ReadBytesPS:  round2(diskReadBps[0]),
						WriteBytesPS: round2(diskWriteBps[0]),
					},
					{
						Name:         "sdb",
						ReadsPerSec:  round2(diskUtil[1] * 10),
						WritesPerSec: round2(diskUtil[1] * 5),
						ReadBytesPS:  round2(diskReadBps[1]),
						WriteBytesPS: round2(diskWriteBps[1]),
					},
				},
				FileSystems: []collector.FileSystemInfo{
					{
						Device:     "/dev/sda1",
						MountPoint: "/",
						FSType:     "ext4",
						Total:      100 * 1024 * 1024 * 1024,
						Used:       40*1024*1024*1024 + uint64(i)*1024, // slowly growing
						Available:  60 * 1024 * 1024 * 1024,
						UsedPct:    40.0,
					},
				},
			},
			System: collector.SystemStats{
				Hostname:  "mock-server",
				Uptime:    float64(i),
				ClockSync: true,
				Entropy:   3000,
			},
			Process: collector.ProcessStats{
				Total:    120 + int(cpuBase/10),
				Running:  1 + int(cpuBase/25),
				Sleeping: 100,
				Threads:  400 + int(cpuBase*2),
			},
			Self: collector.SelfStats{
				CPUPercent: round2(cpuBase * 0.01),
				MemRSS:     16 * 1024 * 1024,
				FDs:        20,
			},
		}

		if err := store.WriteSample(sample); err != nil {
			log.Fatalf("Failed writing sample at index %d: %v", i, err)
		}

		if i > 0 && i%100000 == 0 {
			fmt.Printf("Generated %d / %d samples (%.1f%%)...\n", i, totalSamples, float64(i)/float64(totalSamples)*100)
		}
	}

	elapsed := time.Since(startGenTime)
	fmt.Printf("Finished generating %d samples in %v (%.0f samples/sec).\n",
		totalSamples, elapsed, float64(totalSamples)/elapsed.Seconds())
	fmt.Println("You can now start kula to test the performance boundaries!")
}

func clamp(v, min, max float64) float64 {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

// round2 rounds a float to 2 decimal places
func round2(v float64) float64 {
	return math.Round(v*100) / 100
}
