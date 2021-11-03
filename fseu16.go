// Copyright 2021 Kuldeep Singh
// This source code is licensed under a MIT-style
// license that can be found in the LICENSE file.
// Based on work Copyright 2018 Klaus Post, released user BSD License.
// Based on work Copyright (c) 2013, Yann Collet, released under BSD License.

package mic

import (
	"errors"
	"fmt"
	"math/bits"
)

const (
	/*!MEMORY_USAGE :
	 *  Memory usage formula : N->2^N Bytes (examples : 10 -> 1KB; 12 -> 4KB ; 16 -> 64KB; 20 -> 1MB; etc.)
	 *  Increasing memory usage improves compression ratio
	 *  Reduced memory usage can improve speed, due to cache effect
	 *  Recommended max value is 14, for 16KB, which nicely fits into Intel x86 L1 cache */
	maxMemoryUsage     = 18
	defaultMemoryUsage = 13

	maxTableLog     = maxMemoryUsage - 2
	maxTablesize    = 1 << maxTableLog
	defaultTablelog = defaultMemoryUsage - 2
	minTablelog     = 5
	maxSymbolValue  = 65535
)

var (
	// ErrIncompressible is returned when input is judged to be too hard to compress.
	ErrIncompressible = errors.New("input is not compressible")

	// ErrUseRLE is returned from the compressor when the input is a single byte value repeated.
	ErrUseRLE = errors.New("input is single value repeated")
)

// symbolTransform contains the state transform for a symbol.
type symbolTransformU16 struct {
	deltaFindState int32
	deltaNbBits    uint32
}

// decSymbol contains information about a state entry,
// Including the state offset base, the output symbol and
// the number of bits to read for the low part of the destination state.
type decSymbolU16 struct {
	newState uint32
	symbol   uint16
	nbBits   uint8
}

// cTable contains tables used for compression.
type cTableU16 struct {
	tableSymbol []uint16
	stateTable  []uint32
	symbolTT    []symbolTransformU16
}

// Scratch provides temporary storage for compression and decompression.
type ScratchU16 struct {
	// Private
	count       [maxSymbolValue + 1]uint32
	norm        [maxSymbolValue + 1]int32
	br          byteReaderU16
	brForDecomp byteReader
	bits        bitReader
	bw          bitWriter
	ct          cTableU16      // Compression tables.
	decTable    []decSymbolU16 // Decompression table.
	maxCount    int            // count of the most probable symbol

	// Per block parameters.
	// These can be used to override compression parameters of the block.
	// Do not touch, unless you know what you are doing.

	// Out is output buffer.
	// If the scratch is re-used before the caller is done processing the output,
	// set this field to nil.
	// Otherwise the output buffer will be re-used for next Compression/Decompression step
	// and allocation will be avoided.
	Out    []byte
	OutU16 []uint16

	// DecompressLimit limits the maximum decoded size acceptable.
	// If > 0 decompression will stop when approximately this many bytes
	// has been decoded.
	// If 0, maximum size will be 2GB.
	DecompressLimit int

	symbolLen      uint32 // Length of active part of the symbol table.
	actualTableLog uint8  // Selected tablelog.
	zeroBits       bool   // no bits has prob > 50%.
	clearCount     bool   // clear count

	// MaxSymbolValue will override the maximum symbol value of the next block.
	MaxSymbolValue uint16

	// TableLog will attempt to override the tablelog for the next block.
	TableLog uint8
}

// Histogram allows to populate the histogram and skip that step in the compression,
// It otherwise allows to inspect the histogram when compression is done.
// To indicate that you have populated the histogram call HistogramFinished
// with the value of the highest populated symbol, as well as the number of entries
// in the most populated entry. These are accepted at face value.
// The returned slice will always be length 256.
func (s *ScratchU16) Histogram() []uint32 {
	return s.count[:]
}

// HistogramFinished can be called to indicate that the histogram has been populated.
// maxSymbol is the index of the highest set symbol of the next data segment.
// maxCount is the number of entries in the most populated entry.
// These are accepted at face value.
func (s *ScratchU16) HistogramFinished(maxSymbol uint8, maxCount int) {
	s.maxCount = maxCount
	s.symbolLen = uint32(maxSymbol) + 1
	s.clearCount = maxCount != 0
}

// prepare will prepare and allocate scratch tables used for both compression and decompression.
func (s *ScratchU16) prepare(inForComp []uint16, inForDecomp []byte) (*ScratchU16, error) {
	if s == nil {
		s = &ScratchU16{}
	}
	if s.MaxSymbolValue == 0 {
		s.MaxSymbolValue = 65535
	}
	if s.TableLog == 0 {
		s.TableLog = defaultTablelog
	}
	if s.TableLog > maxTableLog {
		return nil, fmt.Errorf("tableLog (%d) > maxTableLog (%d)", s.TableLog, maxTableLog)
	}
	if inForComp != nil {
		if cap(s.Out) == 0 {
			s.Out = make([]byte, 0, len(inForComp)*2)
		}
		s.br.init(inForComp)
	} else if inForDecomp != nil {
		if cap(s.OutU16) == 0 {
			s.OutU16 = make([]uint16, 0, len(inForDecomp)/2)
		}
		s.brForDecomp.init(inForDecomp)
	}
	if s.clearCount && s.maxCount == 0 {
		for i := range s.count {
			s.count[i] = 0
		}
		s.clearCount = false
	}

	if s.DecompressLimit == 0 {
		// Max size 2GB.
		s.DecompressLimit = (2 << 30) - 1
	}

	return s, nil
}

// tableStep returns the next table index.
func tableStep(tableSize uint32) uint32 {
	return (tableSize >> 1) + (tableSize >> 3) + 3
}

func highBits(val uint32) (n uint32) {
	return uint32(bits.Len32(val) - 1)
}
