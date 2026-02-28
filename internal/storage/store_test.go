package storage

import (
	"kula-szpiegula/internal/collector"
	"kula-szpiegula/internal/config"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	cfg := config.StorageConfig{
		Directory: dir,
		Tiers: []config.TierConfig{
			{Resolution: time.Second, MaxSize: "10MB", MaxBytes: 10 * 1024 * 1024},
		},
	}
	store, err := NewStore(cfg)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}
	return store
}

func makeSample(ts time.Time) *collector.Sample {
	return &collector.Sample{
		Timestamp: ts,
		CPU: collector.CPUStats{
			Total: collector.CPUCoreStats{Usage: 42.0},
		},
		LoadAvg: collector.LoadAvg{Load1: 1.0, Load5: 0.8, Load15: 0.5},
		Memory:  collector.MemoryStats{Total: 1024, Used: 512},
		System:  collector.SystemStats{Hostname: "test"},
	}
}

func TestNewStore(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()

	if len(store.tiers) != 1 {
		t.Errorf("Tier count = %d, want 1", len(store.tiers))
	}
}

func TestWriteAndQuerySample(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()

	now := time.Now()
	sample := makeSample(now)

	if err := store.WriteSample(sample); err != nil {
		t.Fatalf("WriteSample() error: %v", err)
	}

	from := now.Add(-time.Minute)
	to := now.Add(time.Minute)
	results, err := store.QueryRange(from, to)
	if err != nil {
		t.Fatalf("QueryRange() error: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("QueryRange() returned no results")
	}
	if results[0].Data.CPU.Total.Usage != 42.0 {
		t.Errorf("CPU Usage = %f, want 42.0", results[0].Data.CPU.Total.Usage)
	}
}

func TestWriteMultipleSamples(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()

	base := time.Now()
	for i := 0; i < 10; i++ {
		ts := base.Add(time.Duration(i) * time.Second)
		if err := store.WriteSample(makeSample(ts)); err != nil {
			t.Fatalf("WriteSample(%d) error: %v", i, err)
		}
	}

	results, err := store.QueryRange(base.Add(-time.Second), base.Add(11*time.Second))
	if err != nil {
		t.Fatalf("QueryRange() error: %v", err)
	}
	if len(results) != 10 {
		t.Errorf("QueryRange() returned %d results, want 10", len(results))
	}
}

func TestQueryLatest(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()

	now := time.Now()
	store.WriteSample(makeSample(now))
	store.WriteSample(makeSample(now.Add(time.Second)))

	latest, err := store.QueryLatest()
	if err != nil {
		t.Fatalf("QueryLatest() error: %v", err)
	}
	if latest == nil {
		t.Fatal("QueryLatest() returned nil")
	}
}

func TestRingBufferWrapRead(t *testing.T) {
	dir := t.TempDir()
	// Use a small tier (128KB) so we wrap after ~200 samples
	cfg := config.StorageConfig{
		Directory: dir,
		Tiers: []config.TierConfig{
			{Resolution: time.Second, MaxSize: "128KB", MaxBytes: 128 * 1024},
		},
	}
	store, err := NewStore(cfg)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}
	defer store.Close()

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	totalSamples := 500 // enough to wrap multiple times in 128KB

	for i := 0; i < totalSamples; i++ {
		ts := base.Add(time.Duration(i) * time.Second)
		if err := store.WriteSample(makeSample(ts)); err != nil {
			t.Fatalf("WriteSample(%d) error: %v", i, err)
		}
	}

	// Query the last 10 seconds
	queryFrom := base.Add(time.Duration(totalSamples-10) * time.Second)
	queryTo := base.Add(time.Duration(totalSamples) * time.Second)
	results, err := store.QueryRange(queryFrom, queryTo)
	if err != nil {
		t.Fatalf("QueryRange() error: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("QueryRange() returned 0 results after ring buffer wrap — this is the bug!")
	}
	if len(results) < 8 {
		t.Errorf("QueryRange() returned only %d results, expected ~10 recent samples", len(results))
	}
	t.Logf("QueryRange() returned %d results (expected ~10)", len(results))

	// Verify the results are from the expected time range
	for _, r := range results {
		if r.Timestamp.Before(queryFrom) || r.Timestamp.After(queryTo) {
			t.Errorf("Sample timestamp %v outside query range [%v, %v]", r.Timestamp, queryFrom, queryTo)
		}
	}
}

func TestQueryRangeEmpty(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()

	results, err := store.QueryRange(time.Now(), time.Now().Add(time.Minute))
	if err != nil {
		t.Fatalf("QueryRange() error: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("Empty store should return 0 results, got %d", len(results))
	}
}
