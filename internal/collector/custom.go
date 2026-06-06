package collector

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"sync"

	"kula/internal/config"
)

// customCollector listens on a Unix socket for JSON-encoded custom metrics.
//
// Clients connect, send a single JSON line, and disconnect. The expected format:
//
//	{"custom": {"cpu_fans": [{"fan1": 4423}, {"fan2": 8512}]}}
//
// Each top-level key under "custom" is a chart group. Each array element is an
// object with a single key (metric name) → numeric value. The collector maps
// incoming metric names to the configured CustomMetricConfig entries to validate
// and store values.
type customCollector struct {
	mu        sync.RWMutex
	latest    map[string][]CustomMetricValue
	configs   map[string][]config.CustomMetricConfig
	sockPath  string
	listener  net.Listener
	debug     bool
	debugDone bool
}

// customMessage is the expected JSON envelope from socket clients.
type customMessage struct {
	Custom map[string][]map[string]float64 `json:"custom"`
}

// newCustomCollector creates a new collector and starts listening on the socket.
func newCustomCollector(ctx context.Context, sockPath string, configs map[string][]config.CustomMetricConfig, debug bool) (*customCollector, error) {
	// Remove any stale socket file
	_ = os.Remove(sockPath)

	listener, err := net.Listen("unix", sockPath)
	if err != nil {
		return nil, fmt.Errorf("custom metrics socket: %w", err)
	}

	// Make the socket writable by owner + group
	if err := os.Chmod(sockPath, 0660); err != nil {
		log.Printf("[custom] warning: chmod socket: %v", err)
	}

	cc := &customCollector{
		latest:   make(map[string][]CustomMetricValue),
		configs:  configs,
		sockPath: sockPath,
		listener: listener,
		debug:    debug,
	}

	go cc.acceptLoop(ctx)

	log.Printf("[custom] listening on %s (%d chart groups configured)", sockPath, len(configs))
	return cc, nil
}

func (cc *customCollector) debugf(format string, args ...any) {
	if cc.debug && !cc.debugDone {
		log.Printf(format, args...)
	}
}

// acceptLoop handles incoming connections.
func (cc *customCollector) acceptLoop(ctx context.Context) {
	for {
		conn, err := cc.listener.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return
			default:
				// Listener closed or transient error
				if errors.Is(err, net.ErrClosed) {
					return
				}
				log.Printf("[custom] accept error: %v", err)
				continue
			}
		}
		if cc.debug && !cc.debugDone {
			log.Printf("[custom] new connection from %v", conn.RemoteAddr())
		}
		go cc.handleConn(conn)
	}
}

// handleConn reads a single JSON message from a connection.
func (cc *customCollector) handleConn(conn net.Conn) {
	defer func() { _ = conn.Close() }()

	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 64*1024), 64*1024) // 64KB max message

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var msg customMessage
		if err := json.Unmarshal(line, &msg); err != nil {
			log.Printf("[custom] invalid JSON from %v: %v", conn.RemoteAddr(), err)
			continue
		}

		cc.debugf("[custom] received from %v: %s", conn.RemoteAddr(), string(line))

		if msg.Custom == nil {
			continue
		}

		cc.processMessage(msg.Custom)
	}
}

// processMessage converts incoming raw metrics into validated CustomMetricValues.
func (cc *customCollector) processMessage(data map[string][]map[string]float64) {
	cc.mu.Lock()
	defer cc.mu.Unlock()

	for group, entries := range data {
		// Only accept groups that are configured
		cfgs, ok := cc.configs[group]
		if !ok {
			continue
		}

		// Build a lookup of configured metric names for validation
		configuredNames := make(map[string]bool, len(cfgs))
		for _, c := range cfgs {
			configuredNames[c.Name] = true
		}

		var values []CustomMetricValue
		for _, entry := range entries {
			for name, val := range entry {
				if !configuredNames[name] {
					continue // skip unconfigured metrics
				}
				values = append(values, CustomMetricValue{
					Name:  name,
					Value: val,
				})
			}
		}

		if len(values) > 0 {
			cc.latest[group] = values
			cc.debugDone = true
		} else {
			cc.debugf("[custom] discarded message for group %q: no configured metrics matched", group)
		}
	}
}

// Latest returns the most recently received custom metrics.
func (cc *customCollector) Latest() map[string][]CustomMetricValue {
	cc.mu.RLock()
	defer cc.mu.RUnlock()

	if len(cc.latest) == 0 {
		return nil
	}

	// Return a copy to avoid races
	result := make(map[string][]CustomMetricValue, len(cc.latest))
	for k, v := range cc.latest {
		cp := make([]CustomMetricValue, len(v))
		copy(cp, v)
		result[k] = cp
	}
	return result
}

// Close stops listening and removes the socket file.
func (cc *customCollector) Close() {
	if cc.listener != nil {
		_ = cc.listener.Close()
	}
	_ = os.Remove(cc.sockPath)
}
