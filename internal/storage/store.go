package storage

import (
	"fmt"
	"kula-szpiegula/internal/collector"
	"kula-szpiegula/internal/config"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Store manages the tiered storage system.
type Store struct {
	mu         sync.RWMutex
	tiers      []*Tier
	configs    []config.TierConfig
	dir        string

	// Aggregation state
	tier1Count int
	tier1Buf   []*collector.Sample
	tier2Count int
	tier2Buf   []*AggregatedSample
}

func NewStore(cfg config.StorageConfig) (*Store, error) {
	if err := os.MkdirAll(cfg.Directory, 0755); err != nil {
		return nil, fmt.Errorf("creating storage directory: %w", err)
	}

	s := &Store{
		dir:     cfg.Directory,
		configs: cfg.Tiers,
	}

	for i, tc := range cfg.Tiers {
		path := filepath.Join(cfg.Directory, fmt.Sprintf("tier_%d.dat", i))
		tier, err := OpenTier(path, tc.MaxBytes)
		if err != nil {
			// Close already opened tiers
			for _, t := range s.tiers {
				t.Close()
			}
			return nil, fmt.Errorf("opening tier %d: %w", i, err)
		}
		s.tiers = append(s.tiers, tier)
	}

	return s, nil
}

// WriteSample writes a raw sample to tier 1 and triggers aggregation.
func (s *Store) WriteSample(sample *collector.Sample) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Write to tier 1 (1-second)
	as := &AggregatedSample{
		Timestamp: sample.Timestamp,
		Duration:  time.Second,
		Data:      sample,
	}

	if len(s.tiers) > 0 {
		if err := s.tiers[0].Write(as); err != nil {
			return fmt.Errorf("writing tier 0: %w", err)
		}
	}

	// Aggregate for tier 2 (every 60 samples = 1 minute)
	s.tier1Buf = append(s.tier1Buf, sample)
	s.tier1Count++

	if s.tier1Count >= 60 && len(s.tiers) > 1 {
		agg := s.aggregateSamples(s.tier1Buf, time.Minute)
		if err := s.tiers[1].Write(agg); err != nil {
			return fmt.Errorf("writing tier 1: %w", err)
		}
		s.tier2Buf = append(s.tier2Buf, agg)
		s.tier2Count++
		s.tier1Buf = nil
		s.tier1Count = 0

		// Aggregate for tier 3 (every 5 tier-2 samples = 5 minutes)
		if s.tier2Count >= 5 && len(s.tiers) > 2 {
			agg3 := s.aggregateAggregated(s.tier2Buf, 5*time.Minute)
			if err := s.tiers[2].Write(agg3); err != nil {
				return fmt.Errorf("writing tier 2: %w", err)
			}
			s.tier2Buf = nil
			s.tier2Count = 0
		}
	}

	return nil
}

// QueryRange returns samples for a time range, choosing the best tier.
func (s *Store) QueryRange(from, to time.Time) ([]*AggregatedSample, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	duration := to.Sub(from)

	// Choose tier based on requested time range
	tierIdx := 0
	if duration > 30*time.Minute && len(s.tiers) > 1 {
		tierIdx = 1
	}
	if duration > 6*time.Hour && len(s.tiers) > 2 {
		tierIdx = 2
	}

	if tierIdx >= len(s.tiers) {
		tierIdx = len(s.tiers) - 1
	}

	return s.tiers[tierIdx].ReadRange(from, to)
}

// QueryLatest returns the latest sample from tier 1.
func (s *Store) QueryLatest() (*AggregatedSample, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if len(s.tiers) == 0 {
		return nil, fmt.Errorf("no tiers configured")
	}

	samples, err := s.tiers[0].ReadLatest(1)
	if err != nil {
		return nil, err
	}
	if len(samples) == 0 {
		return nil, nil
	}
	return samples[0], nil
}

func (s *Store) Close() error {
	for _, t := range s.tiers {
		if err := t.Flush(); err != nil {
			return err
		}
		if err := t.Close(); err != nil {
			return err
		}
	}
	return nil
}

// aggregateSamples creates an aggregated sample from raw samples.
// Uses the last sample's values (for gauges) and averages for rates.
func (s *Store) aggregateSamples(samples []*collector.Sample, dur time.Duration) *AggregatedSample {
	if len(samples) == 0 {
		return nil
	}

	// Use the last sample as the base (most gauges are "current value")
	last := samples[len(samples)-1]

	// Average CPU usage across the window
	avg := *last
	if len(samples) > 1 {
		var totalCPU float64
		for _, s := range samples {
			totalCPU += s.CPU.Total.Usage
		}
		avg.CPU.Total.Usage = totalCPU / float64(len(samples))

		// Average network rates
		for i := range avg.Network.Interfaces {
			var rxSum, txSum float64
			count := 0
			for _, s := range samples {
				for _, iface := range s.Network.Interfaces {
					if iface.Name == avg.Network.Interfaces[i].Name {
						rxSum += iface.RxMbps
						txSum += iface.TxMbps
						count++
					}
				}
			}
			if count > 0 {
				avg.Network.Interfaces[i].RxMbps = rxSum / float64(count)
				avg.Network.Interfaces[i].TxMbps = txSum / float64(count)
			}
		}
	}

	return &AggregatedSample{
		Timestamp: last.Timestamp,
		Duration:  dur,
		Data:      &avg,
	}
}

func (s *Store) aggregateAggregated(samples []*AggregatedSample, dur time.Duration) *AggregatedSample {
	if len(samples) == 0 {
		return nil
	}

	raw := make([]*collector.Sample, 0, len(samples))
	for _, s := range samples {
		if s.Data != nil {
			raw = append(raw, s.Data)
		}
	}
	return s.aggregateSamples(raw, dur)
}
