package storage

import (
	"encoding/binary"
	"fmt"
	"os"
	"testing"
	"time"

	"kula/internal/collector"
)

// makeVarSample builds a sample whose ENCODED SIZE grows with nIfaces. Real
// systems do this naturally over their lifetime: a NIC/container/disk/GPU
// appears or disappears, an app's metrics toggle, mock data is replaced by
// real data, etc. The ring buffer must tolerate records of differing lengths
// across a wrap.
func makeVarSample(ts time.Time, nIfaces int) *collector.Sample {
	s := &collector.Sample{
		Timestamp: ts,
		CPU:       collector.CPUStats{Total: collector.CPUCoreStats{Usage: 42}},
		Memory:    collector.MemoryStats{Total: 1024, Used: 512},
		System:    collector.SystemStats{Hostname: "host"},
	}
	for i := 0; i < nIfaces; i++ {
		s.Network.Interfaces = append(s.Network.Interfaces, collector.NetInterface{
			Name:   fmt.Sprintf("eth%d", i),
			RxMbps: float64(i),
		})
	}
	return s
}

func varSample(ts time.Time, nIfaces int) *AggregatedSample {
	return &AggregatedSample{Timestamp: ts, Duration: time.Second, Data: makeVarSample(ts, nIfaces)}
}

// fullRead returns ReadRange over the whole history up to newest and asserts
// the result is a single contiguous (1s-spaced) run ending at newest with its
// oldest matching OldestTimestamp(). It returns the record count so callers can
// detect history collapse.
func fullReadChecked(t *testing.T, tier *Tier, base, newest time.Time) int {
	t.Helper()
	got, err := tier.ReadRange(base, newest.Add(time.Second))
	if err != nil {
		t.Fatalf("ReadRange: %v", err)
	}
	if len(got) == 0 {
		t.Fatalf("ReadRange returned nothing (newest=%s)", newest.Format(time.RFC3339))
	}
	if !got[len(got)-1].Timestamp.Equal(newest) {
		t.Fatalf("ReadRange last=%s, want newest=%s",
			got[len(got)-1].Timestamp.Format(time.RFC3339), newest.Format(time.RFC3339))
	}
	for j := 1; j < len(got); j++ {
		if gap := got[j].Timestamp.Sub(got[j-1].Timestamp); gap != time.Second {
			t.Fatalf("history hole/disorder at %d: %s -> %s (gap %s)",
				j, got[j-1].Timestamp.Format(time.RFC3339),
				got[j].Timestamp.Format(time.RFC3339), gap)
		}
	}
	if o := tier.OldestTimestamp(); !o.Equal(got[0].Timestamp) {
		t.Fatalf("OldestTimestamp()=%s but oldest readable record=%s",
			o.Format(time.RFC3339), got[0].Timestamp.Format(time.RFC3339))
	}
	return len(got)
}

// TestWrapMisalignment is the original repro: fill+wrap with small records,
// then write a few LARGER ones. Before the fix this dropped on-disk history
// (full read collapsed 187 -> 2 records) and reported a nonsense oldest
// timestamp. After the fix the tail offset is tracked, so neither happens.
func TestWrapMisalignment(t *testing.T) {
	dir := t.TempDir()
	tier, err := OpenTier(dir+"/tier_0.dat", 64*1024)
	if err != nil {
		t.Fatalf("OpenTier: %v", err)
	}
	defer func() { _ = tier.Close() }()

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	i := 0
	for ; i < 5000 && !tier.wrapped; i++ {
		if err := tier.Write(varSample(base.Add(time.Duration(i)*time.Second), 1)); err != nil {
			t.Fatalf("Write small(%d): %v", i, err)
		}
	}
	if !tier.wrapped {
		t.Fatal("precondition: tier did not wrap during phase 1")
	}
	wrapAt := i
	baseN := fullReadChecked(t, tier, base, base.Add(time.Duration(i-1)*time.Second))
	t.Logf("baseline after wrap (small records): full read = %d contiguous records", baseN)

	for ; i < wrapAt+8; i++ {
		ts := base.Add(time.Duration(i) * time.Second)
		if err := tier.Write(varSample(ts, 8)); err != nil {
			t.Fatalf("Write large(%d): %v", i, err)
		}
		n := fullReadChecked(t, tier, base, ts)
		// A few larger writes drop a few old records, never most of them.
		if n < baseN-2*(i-wrapAt+1) {
			t.Errorf("history collapsed after %d larger writes: %d records (baseline %d)",
				i-wrapAt+1, n, baseN)
		}
	}
}

// TestWrapTailTrackingVariableSize hammers the ring through many wraps with a
// constantly changing record size and checks the read/oldest invariants after
// every write.
func TestWrapTailTrackingVariableSize(t *testing.T) {
	dir := t.TempDir()
	tier, err := OpenTier(dir+"/tier_0.dat", 64*1024)
	if err != nil {
		t.Fatalf("OpenTier: %v", err)
	}
	defer func() { _ = tier.Close() }()

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	const N = 5000
	minReturned := 1 << 30
	wrapped := false
	for i := 0; i < N; i++ {
		ts := base.Add(time.Duration(i) * time.Second)
		// Size churns between 1 and 10 interfaces; coprime stride avoids any
		// periodic re-alignment that could mask the bug.
		if err := tier.Write(varSample(ts, 1+(i*7)%10)); err != nil {
			t.Fatalf("Write(%d): %v", i, err)
		}
		if tier.wrapped {
			wrapped = true
		}
		if !wrapped {
			continue
		}
		n := fullReadChecked(t, tier, base, ts)
		if n < minReturned {
			minReturned = n
		}
	}
	if !wrapped {
		t.Fatal("precondition: tier never wrapped")
	}
	// With correct tail tracking the buffer always holds a healthy slice of
	// history; the bug collapsed it toward 1-2 records.
	if minReturned < 20 {
		t.Errorf("history collapsed across wraps: min full-read = %d records", minReturned)
	}
	t.Logf("survived %d writes / many wraps; min full-read = %d records", N, minReturned)
}

// TestWrapTailPersistsAcrossReopen verifies the tail offset + wrap state + oldest
// timestamp survive a close/reopen and that reads return the same data.
func TestWrapTailPersistsAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/tier_0.dat"
	tier, err := OpenTier(path, 64*1024)
	if err != nil {
		t.Fatalf("OpenTier: %v", err)
	}

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	const N = 3000
	for i := 0; i < N; i++ {
		if err := tier.Write(varSample(base.Add(time.Duration(i)*time.Second), 1+i%6)); err != nil {
			t.Fatalf("Write(%d): %v", i, err)
		}
	}
	if !tier.wrapped {
		t.Fatal("precondition: tier did not wrap")
	}
	newest := base.Add(time.Duration(N-1) * time.Second)
	wantOldest := tier.OldestTimestamp()
	wantOff := tier.oldestOff
	before := fullReadChecked(t, tier, base, newest)
	if err := tier.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	tier2, err := OpenTier(path, 64*1024)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer func() { _ = tier2.Close() }()

	if !tier2.wrapped {
		t.Error("wrapped state not persisted across reopen")
	}
	if tier2.oldestOff != wantOff {
		t.Errorf("oldestOff = %d after reopen, want %d", tier2.oldestOff, wantOff)
	}
	if !tier2.OldestTimestamp().Equal(wantOldest) {
		t.Errorf("OldestTimestamp() = %s after reopen, want %s",
			tier2.OldestTimestamp().Format(time.RFC3339), wantOldest.Format(time.RFC3339))
	}
	if after := fullReadChecked(t, tier2, base, newest); after != before {
		t.Errorf("full read returned %d records after reopen, want %d", after, before)
	}
}

// TestSelfHealPreFixWrappedFile simulates opening a file written by the OLD
// code: full-size, no tail metadata, and a poisoned 1970-style oldest
// timestamp. The fix must self-heal — drop the unrecoverable wrapped tail, keep
// the contiguous [0, writeOff) records, and recompute a sane oldest timestamp.
func TestSelfHealPreFixWrappedFile(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/tier_0.dat"

	tier, err := OpenTier(path, 64*1024)
	if err != nil {
		t.Fatalf("OpenTier: %v", err)
	}
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	const N = 3000
	for i := 0; i < N; i++ {
		if err := tier.Write(varSample(base.Add(time.Duration(i)*time.Second), 1+i%5)); err != nil {
			t.Fatalf("Write(%d): %v", i, err)
		}
	}
	if !tier.wrapped {
		t.Fatal("precondition: tier did not wrap")
	}
	newest := base.Add(time.Duration(N-1) * time.Second)
	if err := tier.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Rewrite the header to look pre-fix: clear header flags, zero oldestOff,
	// and poison oldestTS with the classic garbage (1970-01-01T...).
	f, err := os.OpenFile(path, os.O_RDWR, 0600)
	if err != nil {
		t.Fatalf("open for poisoning: %v", err)
	}
	hdr := make([]byte, headerSize)
	if _, err := f.ReadAt(hdr, 0); err != nil {
		t.Fatalf("read header: %v", err)
	}
	binary.LittleEndian.PutUint32(hdr[4:8], 0)               // clear hasTail/wrapped flags
	binary.LittleEndian.PutUint64(hdr[56:64], 0)             // clear oldestOff
	binary.LittleEndian.PutUint64(hdr[40:48], 5497558139648) // 1970-01-01T... garbage
	if _, err := f.WriteAt(hdr, 0); err != nil {
		t.Fatalf("write poisoned header: %v", err)
	}
	_ = f.Close()

	tier2, err := OpenTier(path, 64*1024)
	if err != nil {
		t.Fatalf("reopen pre-fix file: %v", err)
	}
	defer func() { _ = tier2.Close() }()

	if tier2.wrapped {
		t.Error("self-heal should clear the unverifiable wrapped state")
	}
	if got := tier2.OldestTimestamp(); got.Year() < 2020 {
		t.Errorf("oldest timestamp still garbage after self-heal: %s", got.Format(time.RFC3339))
	}
	// The surviving contiguous block [0, writeOff) must still read back cleanly
	// and still reach the true newest record.
	n := fullReadChecked(t, tier2, base, newest)
	if n == 0 {
		t.Fatal("no records survived self-heal")
	}
	t.Logf("self-heal kept %d contiguous records, oldest now %s",
		n, tier2.OldestTimestamp().Format(time.RFC3339))

	// A subsequent write must persist proper tail metadata again.
	if err := tier2.Write(varSample(newest.Add(time.Second), 3)); err != nil {
		t.Fatalf("post-heal write: %v", err)
	}
	info, err := InspectTierFile(path)
	if err != nil {
		t.Fatalf("InspectTierFile: %v", err)
	}
	if info.OldestTS.Year() < 2020 {
		t.Errorf("inspect still shows garbage oldest: %s", info.OldestTS.Format(time.RFC3339))
	}
}
