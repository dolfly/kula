package storage

import (
	"encoding/json"
	"kula-szpiegula/internal/collector"
	"time"
)

// AggregatedSample holds a time-aggregated metric sample.
// For tier 1 (1s), this is just a wrapper around the raw sample.
// For higher tiers, min/max/avg are computed during aggregation.
type AggregatedSample struct {
	Timestamp time.Time          `json:"ts"`
	Duration  time.Duration      `json:"dur"`
	Data      *collector.Sample  `json:"data"`
}

func encodeSample(s *AggregatedSample) ([]byte, error) {
	return json.Marshal(s)
}

func decodeSample(data []byte) (*AggregatedSample, error) {
	s := &AggregatedSample{}
	err := json.Unmarshal(data, s)
	return s, err
}
