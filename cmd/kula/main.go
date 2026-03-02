package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	"kula-szpiegula"
	"kula-szpiegula/internal/collector"
	"kula-szpiegula/internal/config"
	"kula-szpiegula/internal/sandbox"
	"kula-szpiegula/internal/storage"
	"kula-szpiegula/internal/tui"
	"kula-szpiegula/internal/web"

	"github.com/charmbracelet/x/term"
)

var version = kula.Version

func printUsage() {
	fmt.Fprintf(os.Stderr, `Kula-Szpiegula v%s — Lightweight Linux Server Monitor

Usage:
  kula [flags] [command]

Commands:
  serve          Start the monitoring daemon with web UI (default)
  tui            Launch the terminal UI dashboard
  hash-password  Generate an Argon2 password hash for config

Flags:
  -config string  Path to configuration file (default "config.yaml")
  -h, --help      Show this help message

Examples:
  kula                              Start with default config
  kula -config /etc/kula/config.yaml serve
  kula tui
  kula hash-password

`, version)
}

func main() {
	var showVersion bool
	var showVersionShort bool

	flag.Usage = printUsage
	flag.BoolVar(&showVersion, "version", false, "Print version and exit")
	flag.BoolVar(&showVersionShort, "v", false, "Print version and exit")
	configPath := flag.String("config", "config.yaml", "path to configuration file")
	flag.Parse()

	if showVersion || showVersionShort {
		fmt.Printf("Kula-Szpiegula v%s — Lightweight Linux Server Monitor\n", version)
		os.Exit(0)
	}

	osName := getOSName()
	kernelVersion := getKernelVersion()
	cpuArch := runtime.GOARCH

	cmd := "serve"
	if flag.NArg() > 0 {
		cmd = flag.Arg(0)
	}

	// Handle hash-password command first (doesn't need config)
	if cmd == "hash-password" {
		password := readPasswordWithAsterisks()
		web.PrintHashedPassword(password)
		return
	}

	// Load config
	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	switch cmd {
	case "serve":
		runServe(cfg, *configPath, osName, kernelVersion, cpuArch)
	case "tui":
		runTUI(cfg, osName, kernelVersion, cpuArch)
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\nUsage: kula [serve|tui|hash-password]\n", cmd)
		os.Exit(1)
	}
}

func runServe(cfg *config.Config, configPath string, osName, kernelVersion, cpuArch string) {
	cfg.Web.Version = version
	cfg.Web.OS = osName
	cfg.Web.Kernel = kernelVersion
	cfg.Web.Arch = cpuArch
	coll := collector.New()

	store, err := storage.NewStore(cfg.Storage)
	if err != nil {
		log.Fatalf("Failed to initialize storage: %v", err)
	}
	defer func() { _ = store.Close() }()

	// Enforce Landlock sandbox: restrict filesystem and network access
	// to only what Kula needs. Non-fatal on unsupported kernels.
	if err := sandbox.Enforce(configPath, cfg.Storage.Directory, cfg.Web.Port); err != nil {
		log.Printf("Warning: Landlock sandbox not enforced: %v", err)
	}

	server := web.NewServer(cfg.Web, coll, store)

	// Signal handling with Context
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Collection loop
	go func() {
		ticker := time.NewTicker(cfg.Collection.Interval)
		defer ticker.Stop()

		// Initial collection
		sample := coll.Collect()
		if err := store.WriteSample(sample); err != nil {
			log.Printf("Storage write error: %v", err)
		}
		server.BroadcastSample(sample)

		for {
			select {
			case <-ticker.C:
				sample := coll.Collect()
				if err := store.WriteSample(sample); err != nil {
					log.Printf("Storage write error: %v", err)
				}
				server.BroadcastSample(sample)
			case <-ctx.Done():
				return
			}
		}
	}()

	// Start web server
	go func() {
		if err := server.Start(); err != nil {
			log.Fatalf("Web server error: %v", err)
		}
	}()

	log.Printf("Kula-Szpiegula v%s started (collecting every %s)", version, cfg.Collection.Interval)
	log.Printf("OS: %s, Kernel: %s, Arch: %s", osName, kernelVersion, cpuArch)
	<-ctx.Done()

	log.Println("Shutting down...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("Web server shutdown error: %v", err)
	}
}

func runTUI(cfg *config.Config, osName, kernelVersion, cpuArch string) {
	coll := collector.New()
	if err := tui.RunHeadless(coll, cfg.TUI.RefreshRate, osName, kernelVersion, cpuArch); err != nil {
		log.Fatalf("TUI error: %v", err)
	}
}

func readPasswordWithAsterisks() string {
	fmt.Print("Enter password: ")
	fd := uintptr(syscall.Stdin)
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		// Fallback to basic bufio if not running in a proper terminal
		reader := bufio.NewReader(os.Stdin)
		password, _ := reader.ReadString('\n')
		return strings.TrimSpace(password)
	}
	defer func() { _ = term.Restore(fd, oldState) }()

	var password []byte
	b := make([]byte, 1)
	for {
		n, err := os.Stdin.Read(b)
		if err != nil || n == 0 {
			break
		}

		if b[0] == '\n' || b[0] == '\r' {
			fmt.Print("\n\r")
			break
		}

		if b[0] == 3 { // Ctrl+C
			_ = term.Restore(fd, oldState)
			os.Exit(1)
		}

		if b[0] == 127 || b[0] == '\b' { // Backspace
			if len(password) > 0 {
				password = password[:len(password)-1]
				fmt.Print("\b \b")
			}
			continue
		}

		password = append(password, b[0])
		fmt.Print("*")
	}
	return string(password)
}
