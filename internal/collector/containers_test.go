package collector

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestContainerCollector(t *testing.T) {
	sockPath := "/tmp/container_test.sock"
	defer func() { _ = os.Remove(sockPath) }()

	// Mock Docker/Podman socket server
	listener, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("Failed to listen on unix socket: %v", err)
	}
	defer func() { _ = listener.Close() }()

	go func() {
		mux := http.NewServeMux()
		// List containers
		mux.HandleFunc("/containers/json", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `[{"Id":"c1234567890abcdef","Names":["/test-container"],"State":"running"}]`)
		})
		// Container inspect (for PID)
		mux.HandleFunc("/containers/c1234567890abcdef/json", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"State":{"Pid":123}}`)
		})
		server := http.Server{Handler: mux}
		_ = server.Serve(listener)
	}()

	cfg := ContainersCollectorConfig{
		Enabled:    true,
		SocketPath: sockPath,
	}

	cc := newContainerCollector(cfg)
	// Mock mode to socket since resolveSocket might fail if stat doesn't work on /tmp socket immediately
	cc.socket = sockPath
	cc.mode = containerModeSocket

	// Perform collection with retries since the mock server is async
	var stats []ContainerStats
	for i := 0; i < 10; i++ {
		cc.collect()
		stats = cc.Latest()
		if len(stats) > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if len(stats) == 0 {
		t.Fatal("No container stats collected")
	}

	s := stats[0]
	if s.Name != "test-container" {
		t.Errorf("Expected test-container, got %s", s.Name)
	}
	if !strings.HasPrefix("c1234567890abcdef", s.ID) { //nolint:gocritic // argOrder: intentionally checks s.ID is a prefix of the mock ID
		t.Errorf("Expected s.ID to be a prefix of the mock ID, got %s", s.ID)
	}
}

func TestReadContainerMemory(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Point host-meminfo lookup at a fixture so the fallback is deterministic.
	origProc := procPath
	procPath = filepath.Join(dir, "proc")
	if err := os.MkdirAll(procPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(procPath, "meminfo"), []byte("MemTotal: 4000000 kB\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	defer func() { procPath = origProc }()
	const hostTotal = 4000000 * 1024

	t.Run("unlimited subtracts cache and falls back to host total", func(t *testing.T) {
		// memory.current includes a large reclaimable file cache.
		write("memory.current", "5599784960\n")
		write("memory.stat", "anon 565674000\ninactive_file 5034110976\nactive_file 64589824\n")
		write("memory.max", "max\n")

		used, limit := readContainerMemory(dir)
		if want := uint64(5599784960 - 5034110976); used != want {
			t.Errorf("used = %d, want %d", used, want)
		}
		if limit != hostTotal {
			t.Errorf("limit = %d, want host total %d", limit, hostTotal)
		}
	})

	t.Run("explicit limit is used directly", func(t *testing.T) {
		write("memory.current", "32145408\n")
		write("memory.stat", "inactive_file 1048576\n")
		write("memory.max", "104857600\n")

		used, limit := readContainerMemory(dir)
		if want := uint64(32145408 - 1048576); used != want {
			t.Errorf("used = %d, want %d", used, want)
		}
		if limit != 104857600 {
			t.Errorf("limit = %d, want 104857600", limit)
		}
	})
}

func TestContainerCollectorCgroupDetect(t *testing.T) {
	// Temporarily clear auto-detect paths to force cgroup fallback
	oldPaths := knownSocketPaths
	knownSocketPaths = nil
	defer func() { knownSocketPaths = oldPaths }()

	cfg := ContainersCollectorConfig{
		Enabled:    true,
		SocketPath: "/nonexistent/socket",
	}
	cc := newContainerCollector(cfg)
	// On most systems /sys/fs/cgroup exists, so it should be modeCgroup or modeNone
	// We just want to ensure it doesn't crash and handles the nonexistent socket.
	if cc.mode == containerModeSocket {
		t.Error("Should not be in socket mode for nonexistent socket")
	}
}
