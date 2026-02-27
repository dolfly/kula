package web

// Whirlpool hash function implementation.
// Based on the reference implementation by Paulo S.L.M. Barreto and Vincent Rijmen.

import (
	"encoding/binary"
	"hash"
)

const (
	whirlpoolBlockSize = 64
	whirlpoolDigestSize = 64
	whirlpoolRounds    = 10
)

type whirlpool struct {
	bitLength [32]byte
	buffer    [whirlpoolBlockSize]byte
	bufferPos int
	hash      [8]uint64
}

func NewWhirlpool() hash.Hash {
	return &whirlpool{}
}

func (w *whirlpool) Size() int      { return whirlpoolDigestSize }
func (w *whirlpool) BlockSize() int { return whirlpoolBlockSize }

func (w *whirlpool) Reset() {
	w.bitLength = [32]byte{}
	w.buffer = [whirlpoolBlockSize]byte{}
	w.bufferPos = 0
	w.hash = [8]uint64{}
}

func (w *whirlpool) Write(source []byte) (int, error) {
	n := len(source)
	sourceBits := uint64(n) * 8
	sourcePos := 0
	sourceGap := (8 - int(sourceBits&7)) & 7
	bufferRem := w.bufferPos & 7

	// Update bit length
	carry := uint32(0)
	bitsLen := [4]uint64{sourceBits, 0, 0, 0}
	for i := 31; i >= 0 && (bitsLen[0] != 0 || carry != 0); i-- {
		carry += uint32(w.bitLength[i]) + uint32(bitsLen[0]&0xff)
		w.bitLength[i] = byte(carry)
		carry >>= 8
		bitsLen[0] >>= 8
	}

	// Process data
	for sourceBits > 8 {
		b := ((uint(source[sourcePos]) << uint(sourceGap)) & 0xff) |
			((uint(source[sourcePos+1]) >> uint(8-sourceGap)) & 0xff)

		w.buffer[w.bufferPos>>3] |= byte(b >> uint(bufferRem))
		w.bufferPos += 8 - bufferRem

		if w.bufferPos == whirlpoolBlockSize*8 {
			w.processBuffer()
			w.bufferPos = 0
		}

		w.buffer[w.bufferPos>>3] = byte(b << uint(8-bufferRem))
		w.bufferPos += bufferRem

		sourceBits -= 8
		sourcePos++
	}

	if sourceBits > 0 {
		b := (uint(source[sourcePos]) << uint(sourceGap)) & 0xff
		w.buffer[w.bufferPos>>3] |= byte(b >> uint(bufferRem))
		w.bufferPos += int(sourceBits) - bufferRem
		if w.bufferPos >= 0 && bufferRem > 0 {
			if w.bufferPos == whirlpoolBlockSize*8 {
				w.processBuffer()
				w.bufferPos = 0
			}
			w.buffer[w.bufferPos>>3] = byte(b << uint(8-bufferRem))
			w.bufferPos += bufferRem
		} else {
			w.bufferPos += bufferRem
		}
	}

	return n, nil
}

func (w *whirlpool) Sum(in []byte) []byte {
	// Clone the state
	w0 := *w

	// Pad
	w0.buffer[w0.bufferPos>>3] |= 0x80 >> uint(w0.bufferPos&7)
	w0.bufferPos++

	if w0.bufferPos > (whirlpoolBlockSize-32)*8 {
		if w0.bufferPos < whirlpoolBlockSize*8 {
			for i := w0.bufferPos >> 3; i < whirlpoolBlockSize; i++ {
				w0.buffer[i] = 0
			}
		}
		w0.processBuffer()
		w0.bufferPos = 0
	}

	if w0.bufferPos < (whirlpoolBlockSize-32)*8 {
		for i := w0.bufferPos >> 3; i < whirlpoolBlockSize-32; i++ {
			w0.buffer[i] = 0
		}
	}
	w0.bufferPos = (whirlpoolBlockSize - 32) * 8

	copy(w0.buffer[whirlpoolBlockSize-32:], w0.bitLength[:])
	w0.processBuffer()

	digest := make([]byte, whirlpoolDigestSize)
	for i := 0; i < 8; i++ {
		binary.BigEndian.PutUint64(digest[i*8:], w0.hash[i])
	}

	return append(in, digest...)
}

func (w *whirlpool) processBuffer() {
	var K, state, L [8]uint64
	var block [8]uint64

	for i := 0; i < 8; i++ {
		block[i] = binary.BigEndian.Uint64(w.buffer[i*8:])
		K[i] = w.hash[i]
		state[i] = block[i] ^ K[i]
	}

	for r := 0; r < whirlpoolRounds; r++ {
		for i := 0; i < 8; i++ {
			L[i] = sboxLookup(0, byte(K[(i)%8]>>56)) ^
				sboxLookup(1, byte(K[(8+i-1)%8]>>48)) ^
				sboxLookup(2, byte(K[(8+i-2)%8]>>40)) ^
				sboxLookup(3, byte(K[(8+i-3)%8]>>32)) ^
				sboxLookup(4, byte(K[(8+i-4)%8]>>24)) ^
				sboxLookup(5, byte(K[(8+i-5)%8]>>16)) ^
				sboxLookup(6, byte(K[(8+i-6)%8]>>8)) ^
				sboxLookup(7, byte(K[(8+i-7)%8]))
		}
		L[0] ^= rc[r]
		K = L

		for i := 0; i < 8; i++ {
			L[i] = sboxLookup(0, byte(state[(i)%8]>>56)) ^
				sboxLookup(1, byte(state[(8+i-1)%8]>>48)) ^
				sboxLookup(2, byte(state[(8+i-2)%8]>>40)) ^
				sboxLookup(3, byte(state[(8+i-3)%8]>>32)) ^
				sboxLookup(4, byte(state[(8+i-4)%8]>>24)) ^
				sboxLookup(5, byte(state[(8+i-5)%8]>>16)) ^
				sboxLookup(6, byte(state[(8+i-6)%8]>>8)) ^
				sboxLookup(7, byte(state[(8+i-7)%8])) ^
				K[i]
		}
		state = L
	}

	for i := 0; i < 8; i++ {
		w.hash[i] ^= state[i] ^ block[i]
	}

	// Clear buffer
	for i := range w.buffer {
		w.buffer[i] = 0
	}
}

func sboxLookup(tableIdx int, input byte) uint64 {
	return sboxTables[tableIdx][input]
}

// Round constants
var rc [whirlpoolRounds]uint64

// S-box based lookup tables (8 tables for circulant multiplication)
var sboxTables [8][256]uint64

// The Whirlpool S-box
var sbox = [256]byte{
	0x18, 0x23, 0xc6, 0xe8, 0x87, 0xb8, 0x01, 0x4f,
	0x36, 0xa6, 0xd2, 0xf5, 0x79, 0x6f, 0x91, 0x52,
	0x60, 0xbc, 0x9b, 0x8e, 0xa3, 0x0c, 0x7b, 0x35,
	0x1d, 0xe0, 0xd7, 0xc2, 0x2e, 0x4b, 0xfe, 0x57,
	0x15, 0x77, 0x37, 0xe5, 0x9f, 0xf0, 0x4a, 0xda,
	0x58, 0xc9, 0x29, 0x0a, 0xb1, 0xa0, 0x6b, 0x85,
	0xbd, 0x5d, 0x10, 0xf4, 0xcb, 0x3e, 0x05, 0x67,
	0xe4, 0x27, 0x41, 0x8b, 0xa7, 0x7d, 0x95, 0xd8,
	0xfb, 0xee, 0x7c, 0x66, 0xdd, 0x17, 0x47, 0x9e,
	0xca, 0x2d, 0xbf, 0x07, 0xad, 0x5a, 0x83, 0x33,
	0x63, 0x02, 0xaa, 0x71, 0xc8, 0x19, 0x49, 0xd9,
	0xf2, 0xe3, 0x5b, 0x88, 0x9a, 0x26, 0x32, 0xb0,
	0xe9, 0x0f, 0xd5, 0x80, 0xbe, 0xcd, 0x34, 0x48,
	0xff, 0x7a, 0x90, 0x5f, 0x20, 0x68, 0x1a, 0xae,
	0xb4, 0x54, 0x93, 0x22, 0x64, 0xf1, 0x73, 0x12,
	0x40, 0x08, 0xc3, 0xec, 0xdb, 0xa1, 0x8d, 0x3d,
	0x97, 0x00, 0xcf, 0x2b, 0x76, 0x82, 0xd6, 0x1b,
	0xb5, 0xaf, 0x6a, 0x50, 0x45, 0xf3, 0x30, 0xef,
	0x3f, 0x55, 0xa2, 0xea, 0x65, 0xba, 0x2f, 0xc0,
	0xde, 0x1c, 0xfd, 0x4d, 0x92, 0x75, 0x06, 0x8a,
	0xb2, 0xe6, 0x0e, 0x1f, 0x62, 0xd4, 0xa8, 0x96,
	0xf9, 0xc5, 0x25, 0x59, 0x84, 0x72, 0x39, 0x4c,
	0x5e, 0x78, 0x38, 0x8c, 0xd1, 0xa5, 0xe2, 0x61,
	0xb3, 0x21, 0x9c, 0x1e, 0x43, 0xc7, 0xfc, 0x04,
	0x51, 0x99, 0x6d, 0x0d, 0xfa, 0xdf, 0x7e, 0x24,
	0x3b, 0xab, 0xce, 0x11, 0x8f, 0x4e, 0xb7, 0xeb,
	0x3c, 0x81, 0x94, 0xf7, 0xb9, 0x13, 0x2c, 0xd3,
	0xe7, 0x6e, 0xc4, 0x03, 0x56, 0x44, 0x7f, 0xa9,
	0x2a, 0xbb, 0xc1, 0x53, 0xdc, 0x0b, 0x9d, 0x6c,
	0x31, 0x74, 0xf6, 0x46, 0xac, 0x89, 0x14, 0xe1,
	0x16, 0x3a, 0x69, 0x09, 0x70, 0xb6, 0xd0, 0xed,
	0xcc, 0x42, 0x98, 0xa4, 0x28, 0x5c, 0xf8, 0x86,
}

func init() {
	// Build lookup tables from S-box
	// The circulant MDS matrix for Whirlpool
	c := [8]byte{0x01, 0x01, 0x04, 0x01, 0x08, 0x05, 0x02, 0x09}

	for x := 0; x < 256; x++ {
		v := sbox[x]
		var val uint64
		for t := 0; t < 8; t++ {
			product := gfMul(v, c[t])
			val |= uint64(product) << uint(56-t*8)
		}
		sboxTables[0][x] = val
	}

	// Build rotated tables
	for t := 1; t < 8; t++ {
		for x := 0; x < 256; x++ {
			sboxTables[t][x] = (sboxTables[0][x] >> uint(t*8)) | (sboxTables[0][x] << uint(64-t*8))
		}
	}

	// Compute round constants
	for r := 0; r < whirlpoolRounds; r++ {
		rc[r] = 0
		for j := 0; j < 8; j++ {
			rc[r] ^= sboxTables[j][8*r+j]
		}
	}
}

// GF(2^8) multiplication with the Whirlpool reduction polynomial
func gfMul(a, b byte) byte {
	var result byte
	aa := a
	bb := b
	for bb != 0 {
		if bb&1 != 0 {
			result ^= aa
		}
		// x^8 + x^4 + x^3 + x^2 + x + 1 = 0x11D
		if aa&0x80 != 0 {
			aa = (aa << 1) ^ 0x1D
		} else {
			aa <<= 1
		}
		bb >>= 1
	}
	return result
}
