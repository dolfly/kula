package storage

import (
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
//     [0:8]   magic "KULASPIE"
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
	headerSize  = 64
	magicString = "KULASPIE"
	version     = 1
)

type Tier struct {
	mu        sync.RWMutex
	file      *os.File
	path      string
	maxData   int64
	writeOff  int64
	count     uint64
	oldestTS  time.Time
	newestTS  time.Time
	wrapped   bool
}

func OpenTier(path string, maxSize int64) (*Tier, error) {
	maxData := maxSize - headerSize
	if maxData < 1024 {
		return nil, fmt.Errorf("max_size too small for tier: %d", maxSize)
	}

	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0644)
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
		f.Close()
		return nil, err
	}

	if info.Size() >= headerSize {
		if err := t.readHeader(); err != nil {
			// Corrupted header — reinitialize
			t.writeOff = 0
			t.count = 0
			if err := t.writeHeader(); err != nil {
				f.Close()
				return nil, err
			}
		}
	} else {
		t.writeOff = 0
		t.count = 0
		if err := t.writeHeader(); err != nil {
			f.Close()
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

	magic := string(buf[0:8])
	if magic != magicString {
		return fmt.Errorf("invalid magic: %s", magic)
	}

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
	copy(buf[0:8], magicString)
	binary.LittleEndian.PutUint64(buf[8:16], version)
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
	data, err := encodeSample(s)
	if err != nil {
		return err
	}

	recordLen := 4 + len(data) // length prefix + data
	if int64(recordLen) > t.maxData {
		return fmt.Errorf("sample too large: %d > %d", recordLen, t.maxData)
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	// Check if we need to wrap
	if t.writeOff+int64(recordLen) > t.maxData {
		t.writeOff = 0
		t.wrapped = true
	}

	// Write length prefix
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

	// Read all records by scanning the data region
	scanSize := t.writeOff
	if t.wrapped {
		scanSize = t.maxData
	}

	// We scan from start of data region up to either writeOff (not wrapped) or full region (wrapped)
	var startOff int64
	if t.wrapped {
		// Start reading from writeOff (oldest data) and wrap around
		startOff = t.writeOff
	} else {
		startOff = 0
	}

	bytesRead := int64(0)
	for bytesRead < scanSize {
		readOff := (startOff + bytesRead) % t.maxData
		fileOff := headerSize + readOff

		// Read length
		lenBuf := make([]byte, 4)
		if _, err := t.file.ReadAt(lenBuf, fileOff); err != nil {
			break
		}
		dataLen := binary.LittleEndian.Uint32(lenBuf)
		if dataLen == 0 || int64(dataLen) > t.maxData {
			break
		}

		// Read data
		data := make([]byte, dataLen)
		if _, err := t.file.ReadAt(data, fileOff+4); err != nil {
			if err == io.EOF {
				break
			}
			break
		}

		sample, err := decodeSample(data)
		if err != nil {
			bytesRead += int64(4 + dataLen)
			continue
		}

		if !sample.Timestamp.Before(from) && !sample.Timestamp.After(to) {
			samples = append(samples, sample)
		}

		bytesRead += int64(4 + dataLen)
	}

	return samples, nil
}

// ReadLatest returns the n most recent samples.
func (t *Tier) ReadLatest(n int) ([]*AggregatedSample, error) {
	to := time.Now()
	from := time.Time{} // epoch
	all, err := t.ReadRange(from, to)
	if err != nil {
		return nil, err
	}

	if len(all) <= n {
		return all, nil
	}
	return all[len(all)-n:], nil
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
