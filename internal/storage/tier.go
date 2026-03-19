package storage

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

// Tier implements a single ring-buffer storage tier.
// File format:
//   Header (64 bytes):
//     [0:4]   magic "KULA"
//     [4:8]   reserved
//     [8:16]  version (uint64)
//     [16:24] max data size (uint64)
//     [24:32] write offset within data region (uint64)
//     [32:40] total records written (uint64)
//     [40:48] oldest timestamp (int64, unix nano)
//     [48:56] newest timestamp (int64, unix nano)
//     [56:64] reserved
//   Data region:
//     Sequence of: [length uint32][data []byte]
//     When write wraps around, it overwrites from the beginning.

const (
	headerSize    = 64
	magicString   = "KULA"
	version       = 1
	codecVersion2 = 2
)

type Tier struct {
	mu       sync.RWMutex
	file     *os.File
	path     string
	maxData  int64
	writeOff int64
	count    uint64
	oldestTS time.Time
	newestTS time.Time
	wrapped  bool
	codecVer uint64 // 1 = legacy JSON, 2 = binary
}

func OpenTier(path string, maxSize int64) (*Tier, error) {
	maxData := maxSize - headerSize
	if maxData < 1024 {
		return nil, fmt.Errorf("max_size too small for tier: %d", maxSize)
	}

	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		return nil, fmt.Errorf("opening tier file: %w", err)
	}

	t := &Tier{
		file:    f,
		path:    path,
		maxData: maxData,
	}

	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}

	if info.Size() >= headerSize {
		if err := t.readHeader(); err != nil {
			// Corrupted header — reinitialize
			t.writeOff = 0
			t.count = 0
			if err := t.writeHeader(); err != nil {
				_ = f.Close()
				return nil, err
			}
		}
	} else {
		t.writeOff = 0
		t.count = 0
		t.codecVer = codecVersion2 // new files start binary
		if err := t.writeHeader(); err != nil {
			_ = f.Close()
			return nil, err
		}
	}

	return t, nil
}

func (t *Tier) readHeader() error {
	buf := make([]byte, headerSize)
	if _, err := t.file.ReadAt(buf, 0); err != nil {
		return err
	}

	magic := string(buf[0:4])
	if magic != magicString {
		return fmt.Errorf("invalid magic: %q", magic)
	}

	v := binary.LittleEndian.Uint64(buf[8:16])
	if v == 0 {
		v = 1
	}
	t.codecVer = v
	t.maxData = int64(binary.LittleEndian.Uint64(buf[16:24]))
	t.writeOff = int64(binary.LittleEndian.Uint64(buf[24:32]))
	t.count = binary.LittleEndian.Uint64(buf[32:40])

	oldestNano := int64(binary.LittleEndian.Uint64(buf[40:48]))
	newestNano := int64(binary.LittleEndian.Uint64(buf[48:56]))
	if oldestNano > 0 {
		t.oldestTS = time.Unix(0, oldestNano)
	}
	if newestNano > 0 {
		t.newestTS = time.Unix(0, newestNano)
	}

	if t.writeOff > 0 && t.count > 0 {
		// Check if we've wrapped
		fileInfo, _ := t.file.Stat()
		if fileInfo != nil && fileInfo.Size() >= headerSize+t.maxData {
			t.wrapped = true
		}
	}

	return nil
}

func (t *Tier) writeHeader() error {
	buf := make([]byte, headerSize)
	copy(buf[0:4], magicString)
	binary.LittleEndian.PutUint64(buf[8:16], t.codecVer)
	binary.LittleEndian.PutUint64(buf[16:24], uint64(t.maxData))
	binary.LittleEndian.PutUint64(buf[24:32], uint64(t.writeOff))
	binary.LittleEndian.PutUint64(buf[32:40], t.count)

	if !t.oldestTS.IsZero() {
		binary.LittleEndian.PutUint64(buf[40:48], uint64(t.oldestTS.UnixNano()))
	}
	if !t.newestTS.IsZero() {
		binary.LittleEndian.PutUint64(buf[48:56], uint64(t.newestTS.UnixNano()))
	}

	_, err := t.file.WriteAt(buf, 0)
	return err
}

// Write stores a sample in the ring buffer.
func (t *Tier) Write(s *AggregatedSample) error {
	payload, err := encodeSampleV(s)
	if err != nil {
		return err
	}

	// Prepend the record kind byte so readers can identify the format
	// deterministically without inspecting timestamp bytes.
	data := make([]byte, 1+len(payload))
	data[0] = recordKindBinary
	copy(data[1:], payload)

	recordLen := 4 + len(data) // length prefix + data
	if int64(recordLen) > t.maxData {
		return fmt.Errorf("sample too large: %d > %d", recordLen, t.maxData)
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	// Check if we need to wrap
	if t.writeOff+int64(recordLen) > t.maxData {
		// Write a zero sentinel to mark end-of-segment so ReadRange
		// knows there are no more records in this tail region.
		if t.writeOff+4 <= t.maxData {
			sentinel := make([]byte, 4)
			// sentinel is already all zeros (dataLen == 0)
			_, _ = t.file.WriteAt(sentinel, headerSize+t.writeOff)
		}
		t.writeOff = 0
		t.wrapped = true
	}
	lenBuf := make([]byte, 4)
	binary.LittleEndian.PutUint32(lenBuf, uint32(len(data)))

	fileOff := headerSize + t.writeOff
	if _, err := t.file.WriteAt(lenBuf, fileOff); err != nil {
		return err
	}
	if _, err := t.file.WriteAt(data, fileOff+4); err != nil {
		return err
	}

	t.writeOff += int64(recordLen)
	t.count++
	t.newestTS = s.Timestamp
	if t.oldestTS.IsZero() {
		t.oldestTS = s.Timestamp
	}

	// When the ring buffer has wrapped, oldestTS must track the actual oldest
	// surviving record, which is the one now sitting at writeOff (the next
	// slot we will overwrite). Refresh it on every write so that
	// QueryRangeWithMeta always gets an accurate lower bound.
	if t.wrapped {
		if ts, err := t.readTimestampAt(t.writeOff % t.maxData); err == nil {
			t.oldestTS = ts
		}
	}

	// Bump codec version to binary on first write to a legacy JSON file.
	if t.codecVer < codecVersion2 {
		t.codecVer = codecVersion2
		_ = t.writeHeader()
	}

	// Update header periodically (every 10 writes to reduce I/O)
	if t.count%10 == 0 {
		return t.writeHeader()
	}
	return nil
}

// ReadRange returns all samples within [from, to].
func (t *Tier) ReadRange(from, to time.Time) ([]*AggregatedSample, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	if t.count == 0 {
		return nil, nil
	}

	var samples []*AggregatedSample

	// Build list of (start, size) segments to scan in chronological order.
	//
	// When wrapped two segments exist:
	//   1. writeOff..maxData  — older records from the previous ring pass
	//   2. 0..writeOff        — newer records written after the wrap
	// When not wrapped: one segment 0..writeOff.
	//
	// For v2 binary files and wrapped tiers, we check whether 'from' is past
	// the start of segment 2; if so we skip segment 1 entirely (safe because
	// each segment always starts on a record boundary).
	type segment struct{ start, size int64 }
	var segments []segment

	if t.wrapped {
		seg1 := segment{t.writeOff, t.maxData - t.writeOff}
		seg2 := segment{0, t.writeOff}

		if t.codecVer >= codecVersion2 && t.writeOff > 0 {
			// Peek at the oldest record in segment 2 (data region offset 0).
			// If 'from' is at or after that timestamp we can skip segment 1 entirely.
			seg2Oldest, err := t.readTimestampAt(0)
			if err == nil && !from.Before(seg2Oldest) {
				segments = []segment{seg2}
			} else {
				segments = []segment{seg1, seg2}
			}
		} else {
			segments = []segment{seg1, seg2}
		}
	} else {
		segments = []segment{{0, t.writeOff}}
	}

	for _, seg := range segments {
		bytesRead := int64(0)

		// Use buffered reader for drastic performance improvement over thousands of reads.
		sr := io.NewSectionReader(t.file, headerSize+seg.start, seg.size)
		br := bufio.NewReaderSize(sr, 1024*1024)

		for bytesRead < seg.size {
			if seg.size-bytesRead < 4 {
				break
			}

			lenBuf := make([]byte, 4)
			if _, err := io.ReadFull(br, lenBuf); err != nil {
				break
			}
			dataLen := binary.LittleEndian.Uint32(lenBuf)

			if dataLen == 0 || int64(dataLen) > t.maxData {
				break
			}

			recordLen := int64(4 + dataLen)
			if bytesRead+recordLen > seg.size {
				break
			}

			data := make([]byte, dataLen)
			if _, err := io.ReadFull(br, data); err != nil {
				break
			}

			ts, err := extractTimestamp(data)
			if err != nil {
				// Fallback: full decode for JSON records or unreadable binary.
				sample, decErr := t.readRecord(data)
				if decErr == nil {
					if sample.Timestamp.After(to) {
						break
					}
					if !sample.Timestamp.Before(from) {
						samples = append(samples, sample)
					}
				}
				bytesRead += recordLen
				continue
			}

			// Records are chronological within a segment: past the window → done.
			if ts.After(to) {
				break
			}

			if ts.Before(from) {
				bytesRead += recordLen
				continue
			}

			sample, err := t.readRecord(data)
			if err != nil {
				bytesRead += recordLen
				continue
			}

			samples = append(samples, sample)
			bytesRead += recordLen
		}
	}

	return samples, nil
}

// ReadLatest returns the n most recent samples.
func (t *Tier) ReadLatest(n int) ([]*AggregatedSample, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	if t.count == 0 {
		return nil, nil
	}

	type segment struct{ start, size int64 }
	var segments []segment

	if t.wrapped {
		segments = append(segments, segment{t.writeOff, t.maxData - t.writeOff})
		segments = append(segments, segment{0, t.writeOff})
	} else {
		segments = append(segments, segment{0, t.writeOff})
	}

	// First pass: find the offsets of all records
	type recordLoc struct {
		offset int64
		length uint32
	}
	locs := make([]recordLoc, 0, n)

	for _, seg := range segments {
		bytesRead := int64(0)
		sr := io.NewSectionReader(t.file, headerSize+seg.start, seg.size)
		br := bufio.NewReaderSize(sr, 1024*1024)

		for bytesRead < seg.size {
			if seg.size-bytesRead < 4 {
				break
			}
			lenBuf := make([]byte, 4)
			if _, err := io.ReadFull(br, lenBuf); err != nil {
				break
			}
			dataLen := binary.LittleEndian.Uint32(lenBuf)
			if dataLen == 0 || int64(dataLen) > t.maxData {
				break
			}

			recordLen := int64(4 + dataLen)
			if bytesRead+recordLen > seg.size {
				break
			}

			loc := recordLoc{
				offset: headerSize + seg.start + bytesRead,
				length: dataLen,
			}
			if len(locs) < n {
				locs = append(locs, loc)
			} else {
				copy(locs, locs[1:])
				locs[len(locs)-1] = loc
			}

			if _, err := br.Discard(int(dataLen)); err != nil {
				break
			}
			bytesRead += recordLen
		}
	}

	var samples []*AggregatedSample
	for _, loc := range locs {
		data := make([]byte, loc.length)
		if _, err := t.file.ReadAt(data, loc.offset+4); err != nil {
			continue
		}
		sample, err := t.readRecord(data)
		if err == nil {
			samples = append(samples, sample)
		}
	}

	return samples, nil
}

func (t *Tier) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := t.writeHeader(); err != nil {
		return err
	}
	return t.file.Close()
}

func (t *Tier) Flush() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.writeHeader()
}

// readRecord decodes a payload using per-record format detection.
//
//   - recordKindBinary (0x02): kind-tagged binary written by current code
//   - '{' (0x7B):              legacy JSON record
//   - anything else:           legacy binary record (no kind byte)
func (t *Tier) readRecord(data []byte) (*AggregatedSample, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("empty record")
	}
	switch data[0] {
	case recordKindBinary:
		return decodeSample(data[1:])
	case '{':
		return decodeSampleJSON(data)
	default:
		// Legacy binary record written before the kind-byte format.
		return decodeSample(data)
	}
}

// readTimestampAt reads the timestamp of the record at the given data-region
// offset. Returns an error if the record is invalid. Must be called under at
// least a read lock (Write holds the write lock, which is sufficient).
//
// Format detection uses the explicit kind byte for new binary records and falls
// back to content sniffing for legacy formats (JSON: '{'; binary: no kind byte).
func (t *Tier) readTimestampAt(dataOffset int64) (time.Time, error) {
	// 4-byte length prefix + 1 kind byte + 8-byte timestamp = 13 bytes covers all
	// three formats in a single ReadAt call.
	var buf [13]byte
	if _, err := t.file.ReadAt(buf[:], headerSize+dataOffset); err != nil {
		return time.Time{}, err
	}
	dataLen := binary.LittleEndian.Uint32(buf[0:4])
	if dataLen == 0 || int64(dataLen) > t.maxData {
		return time.Time{}, fmt.Errorf("invalid record length %d at offset %d", dataLen, dataOffset)
	}

	switch buf[4] {
	case recordKindBinary:
		// Kind-tagged binary record: timestamp follows the kind byte.
		if dataLen < 9 {
			return time.Time{}, fmt.Errorf("binary record too short for timestamp: %d bytes", dataLen)
		}
		return time.Unix(0, int64(binary.LittleEndian.Uint64(buf[5:13]))), nil
	case '{':
		// Legacy JSON record: must fully decode to get timestamp.
		data := make([]byte, dataLen)
		if _, err := t.file.ReadAt(data, headerSize+dataOffset+4); err != nil {
			return time.Time{}, err
		}
		s, err := decodeSampleJSON(data)
		if err != nil {
			return time.Time{}, err
		}
		return s.Timestamp, nil
	default:
		// Legacy binary record (no kind byte): timestamp at start of payload.
		if dataLen < 8 {
			return time.Time{}, fmt.Errorf("legacy binary record too short: %d bytes", dataLen)
		}
		return time.Unix(0, int64(binary.LittleEndian.Uint64(buf[4:12]))), nil
	}
}

// OldestTimestamp returns the oldest sample timestamp in this tier.
func (t *Tier) OldestTimestamp() time.Time {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.oldestTS
}

// NewestTimestamp returns the newest sample timestamp in this tier.
func (t *Tier) NewestTimestamp() time.Time {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.newestTS
}

// Count returns the total number of records written to this tier.
func (t *Tier) Count() uint64 {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.count
}

// TierInfo holds metadata about a tier file, extracted without locking or loading the full file.
type TierInfo struct {
	Version  uint64
	MaxData  int64
	WriteOff int64
	Count    uint64
	OldestTS time.Time
	NewestTS time.Time
	Wrapped  bool
}

// InspectTierFile reads only the header of a tier file and returns metadata.
func InspectTierFile(path string) (*TierInfo, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	buf := make([]byte, headerSize)
	if _, err := io.ReadFull(f, buf); err != nil {
		return nil, fmt.Errorf("reading header: %w", err)
	}

	magic := string(buf[0:4])
	if magic != magicString {
		return nil, fmt.Errorf("invalid magic: %q", magic)
	}

	info := &TierInfo{
		Version:  binary.LittleEndian.Uint64(buf[8:16]),
		MaxData:  int64(binary.LittleEndian.Uint64(buf[16:24])),
		WriteOff: int64(binary.LittleEndian.Uint64(buf[24:32])),
		Count:    binary.LittleEndian.Uint64(buf[32:40]),
	}

	oldestNano := int64(binary.LittleEndian.Uint64(buf[40:48]))
	newestNano := int64(binary.LittleEndian.Uint64(buf[48:56]))
	if oldestNano > 0 {
		info.OldestTS = time.Unix(0, oldestNano)
	}
	if newestNano > 0 {
		info.NewestTS = time.Unix(0, newestNano)
	}

	if info.WriteOff > 0 && info.Count > 0 {
		fileInfo, _ := f.Stat()
		if fileInfo != nil && fileInfo.Size() >= headerSize+info.MaxData {
			info.Wrapped = true
		}
	}

	return info, nil
}
