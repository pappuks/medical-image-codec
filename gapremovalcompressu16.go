// Copyright 2026 Kuldeep Singh
// This source code is licensed under a MIT-style
// license that can be found in the LICENSE file.

package mic

import (
	"encoding/binary"
	"fmt"
)

// CompressSingleFrameGapRemoval compresses a single 16-bit frame using the
// Delta+RLE+GapRemoval+FSE pipeline.
//
// After Delta+RLE encoding, the intermediate uint16 stream is inspected for
// symbol sparsity.  When the overhead of storing a symbol mapping is less than
// 1/8 of the estimated savings from a denser FSE alphabet, the symbols are
// remapped to [0, numUsed-1] before FSE encoding.  Three map representations
// are supported and the smallest is chosen automatically:
//
//   - Bitmap (mode 0x02): one bit per symbol in [0, maxSym].  Best for moderate
//     sparsity with a small symbol range (e.g. MR, XR: 256–512 byte overhead).
//
//   - Delta-encoded list (mode 0x03): numUsed sorted symbol values stored as
//     differences.  Each gap ≤ 254 takes 1 byte; larger gaps take 3 bytes
//     (0xFF escape + uint16 LE).  Best for large sparse alphabets (e.g. CT with
//     16-bit range: ~1800 byte overhead vs 8192 bytes for a bitmap).
//
//   - Raw list (mode 0x01): numUsed × 2 bytes, used only when it beats both
//     bitmap and delta list (rare in practice; kept for completeness).
//
// Output format:
//
//	Byte 0: mode
//	  0x00 = no gap removal (bytes 1+ are a standard Delta+RLE+FSE stream)
//	  0x01 = raw expand map
//	         Bytes 1-2:  numSymbols (uint16 LE)
//	         Bytes 3 to 3+numSymbols*2-1: expandMap[i] (uint16 LE, sorted)
//	         Bytes after: FSE data with compact alphabet [0, numSymbols-1]
//	  0x02 = bitmap expand map
//	         Bytes 1-2:  maxSym (uint16 LE)
//	         Bytes 3 to 3+ceil((maxSym+1)/8)-1: bitmap, bit i=1 iff symbol i used
//	         Bytes after: FSE data with compact alphabet [0, numSymbols-1]
//	  0x03 = delta-encoded expand map
//	         Bytes 1-2:  numSymbols (uint16 LE)
//	         Bytes 3-4:  expandMap[0] (uint16 LE, first symbol raw value)
//	         Bytes 5+:   for i in [1, numSymbols-1]:
//	                       gap = expandMap[i] - expandMap[i-1] - 1
//	                       if gap ≤ 254: 1 byte (gap)
//	                       if gap ≥ 255: 0xFF + uint16 LE (gap) = 3 bytes
//	         Bytes after: FSE data with compact alphabet [0, numSymbols-1]
func CompressSingleFrameGapRemoval(pixels []uint16, width, height int, maxValue uint16) ([]byte, error) {
	// Step 1: Delta+RLE encode.
	var drc DeltaRleCompressU16
	rleOut, err := drc.Compress(pixels, width, height, maxValue)
	if err != nil {
		return nil, fmt.Errorf("gap removal: delta+RLE: %w", err)
	}

	// Step 2: Build histogram and find distinct symbols.
	var hist [maxSymbolValue + 1]uint32
	var maxSym uint16
	for _, v := range rleOut {
		hist[v]++
		if v > maxSym {
			maxSym = v
		}
	}
	symLen := uint32(maxSym) + 1

	// Build expandMap (compact index → original symbol value).
	expandMap := make([]uint16, 0, 64)
	for i := uint32(0); i < symLen; i++ {
		if hist[i] > 0 {
			expandMap = append(expandMap, uint16(i))
		}
	}
	numUsed := uint32(len(expandMap))
	eliminatedZeros := symLen - numUsed

	// Step 3: Compute overhead for each map representation.
	rawMapSize := 3 + numUsed*2 // 1 mode + 2 numSymbols + 2×numUsed
	bitmapSize := 3 + (uint32(maxSym)+8)/8
	deltaMapSize := computeDeltaMapSize(expandMap)

	// Choose the representation with the smallest overhead.
	type mapMode uint8
	const (
		modeRaw   mapMode = 0x01
		modeBitmap mapMode = 0x02
		modeDelta  mapMode = 0x03
	)
	minOverhead := rawMapSize
	chosenMode := modeRaw
	if bitmapSize < minOverhead {
		minOverhead = bitmapSize
		chosenMode = modeBitmap
	}
	if deltaMapSize < minOverhead {
		minOverhead = deltaMapSize
		chosenMode = modeDelta
	}

	// The previous0 run-length encoding in FSE's writeCount() costs roughly
	// 2 bits per zero-count symbol.  Apply gap removal only when the map
	// overhead is less than 1/8 of the bits saved by eliminating those zeros
	// (conservative threshold to ensure net positive benefit).
	// Also require at least 50% of symbols to be unused.
	applyGapRemoval := numUsed > 1 &&
		numUsed < symLen/2 &&
		minOverhead*8 < eliminatedZeros

	if !applyGapRemoval {
		fseData, err := compressRLEWithFSE(rleOut)
		if err != nil {
			return nil, err
		}
		out := make([]byte, 1+len(fseData))
		out[0] = 0x00
		copy(out[1:], fseData)
		return out, nil
	}

	// Step 4: Build reverse compact mapping and remap the RLE stream.
	var compactIdx [maxSymbolValue + 1]uint16
	for i, sym := range expandMap {
		compactIdx[sym] = uint16(i)
	}
	remapped := make([]uint16, len(rleOut))
	for i, v := range rleOut {
		remapped[i] = compactIdx[v]
	}

	// Step 5: FSE compress the remapped stream.
	fseData, err := compressRLEWithFSE(remapped)
	if err != nil {
		return nil, err
	}

	// Step 6: Assemble output with the chosen map representation.
	switch chosenMode {
	case modeRaw:
		n := len(expandMap)
		headerSize := 1 + 2 + n*2
		out := make([]byte, headerSize+len(fseData))
		out[0] = byte(modeRaw)
		binary.LittleEndian.PutUint16(out[1:3], uint16(n))
		for i, sym := range expandMap {
			binary.LittleEndian.PutUint16(out[3+i*2:], sym)
		}
		copy(out[headerSize:], fseData)
		return out, nil

	case modeBitmap:
		bitmapLen := int((uint32(maxSym) + 8) / 8)
		headerSize := 1 + 2 + bitmapLen
		out := make([]byte, headerSize+len(fseData))
		out[0] = byte(modeBitmap)
		binary.LittleEndian.PutUint16(out[1:3], maxSym)
		for _, sym := range expandMap {
			out[3+sym/8] |= 1 << (sym % 8)
		}
		copy(out[headerSize:], fseData)
		return out, nil

	default: // modeDelta
		header := buildDeltaMapHeader(expandMap)
		out := make([]byte, 1+len(header)+len(fseData))
		out[0] = byte(modeDelta)
		copy(out[1:], header)
		copy(out[1+len(header):], fseData)
		return out, nil
	}
}

// DecompressSingleFrameGapRemoval decompresses a stream produced by
// CompressSingleFrameGapRemoval.
func DecompressSingleFrameGapRemoval(compressed []byte, width, height int) ([]uint16, error) {
	if len(compressed) < 1 {
		return nil, fmt.Errorf("gap removal: empty input")
	}

	mode := compressed[0]

	switch mode {
	case 0x00:
		return DecompressSingleFrame(compressed[1:], width, height)

	case 0x01: // raw expand map
		if len(compressed) < 3 {
			return nil, fmt.Errorf("gap removal: header too short")
		}
		numSymbols := int(binary.LittleEndian.Uint16(compressed[1:3]))
		headerSize := 1 + 2 + numSymbols*2
		if len(compressed) < headerSize {
			return nil, fmt.Errorf("gap removal: data too short for raw expandMap")
		}
		expandMap := make([]uint16, numSymbols)
		for i := range expandMap {
			expandMap[i] = binary.LittleEndian.Uint16(compressed[3+i*2:])
		}
		return gapRemovalDecompressWithMap(compressed[headerSize:], expandMap, width, height)

	case 0x02: // bitmap
		if len(compressed) < 3 {
			return nil, fmt.Errorf("gap removal: bitmap header too short")
		}
		maxSym := int(binary.LittleEndian.Uint16(compressed[1:3]))
		bitmapLen := (maxSym + 8) / 8
		headerSize := 1 + 2 + bitmapLen
		if len(compressed) < headerSize {
			return nil, fmt.Errorf("gap removal: data too short for bitmap")
		}
		bitmap := compressed[3:headerSize]
		expandMap := make([]uint16, 0, 64)
		for sym := 0; sym <= maxSym; sym++ {
			if bitmap[sym/8]&(1<<(sym%8)) != 0 {
				expandMap = append(expandMap, uint16(sym))
			}
		}
		return gapRemovalDecompressWithMap(compressed[headerSize:], expandMap, width, height)

	case 0x03: // delta-encoded expand map
		if len(compressed) < 5 {
			return nil, fmt.Errorf("gap removal: delta header too short")
		}
		numSymbols := int(binary.LittleEndian.Uint16(compressed[1:3]))
		expandMap := make([]uint16, numSymbols)
		if numSymbols == 0 {
			return gapRemovalDecompressWithMap(compressed[5:], expandMap, width, height)
		}
		expandMap[0] = binary.LittleEndian.Uint16(compressed[3:5])
		p := 5
		for i := 1; i < numSymbols; i++ {
			if p >= len(compressed) {
				return nil, fmt.Errorf("gap removal: delta map truncated at symbol %d", i)
			}
			b := compressed[p]
			p++
			if b == 0xFF {
				if p+2 > len(compressed) {
					return nil, fmt.Errorf("gap removal: delta map escape truncated at symbol %d", i)
				}
				gap := uint32(binary.LittleEndian.Uint16(compressed[p:]))
				p += 2
				expandMap[i] = expandMap[i-1] + uint16(gap) + 1
			} else {
				expandMap[i] = expandMap[i-1] + uint16(b) + 1
			}
		}
		return gapRemovalDecompressWithMap(compressed[p:], expandMap, width, height)

	default:
		return nil, fmt.Errorf("gap removal: unknown mode byte 0x%02x", mode)
	}
}

// gapRemovalDecompressWithMap FSE-decompresses fseData, expands the compact
// symbols using expandMap, then Delta+RLE-decompresses to pixels.
func gapRemovalDecompressWithMap(fseData []byte, expandMap []uint16, width, height int) ([]uint16, error) {
	numSymbols := len(expandMap)

	var s ScratchU16
	compactSymbols, err := FSEDecompressU16Auto(fseData, &s)
	if err != nil {
		return nil, fmt.Errorf("gap removal: FSE decompress: %w", err)
	}

	rleSymbols := make([]uint16, len(compactSymbols))
	for i, c := range compactSymbols {
		if int(c) >= numSymbols {
			return nil, fmt.Errorf("gap removal: compact symbol %d out of range [0, %d)", c, numSymbols)
		}
		rleSymbols[i] = expandMap[c]
	}

	var drd DeltaRleDecompressU16
	drd.Decompress(rleSymbols, width, height)
	return drd.Out, nil
}

// computeDeltaMapSize returns the byte size of the delta-encoded header
// (excluding the mode byte, which is accounted for in the caller).
func computeDeltaMapSize(expandMap []uint16) uint32 {
	if len(expandMap) == 0 {
		return 4 // 2 numSymbols + 2 first
	}
	// 2 (numSymbols) + 2 (first symbol raw) + gaps for remaining symbols.
	size := uint32(4)
	for i := 1; i < len(expandMap); i++ {
		gap := uint32(expandMap[i]) - uint32(expandMap[i-1]) - 1
		if gap >= 255 {
			size += 3 // escape byte + uint16
		} else {
			size += 1
		}
	}
	return size + 1 // +1 for the mode byte itself
}

// buildDeltaMapHeader serialises the delta-encoded expand map (without mode byte).
func buildDeltaMapHeader(expandMap []uint16) []byte {
	size := computeDeltaMapSize(expandMap) - 1 // subtract mode byte
	buf := make([]byte, size)
	n := len(expandMap)
	binary.LittleEndian.PutUint16(buf[0:2], uint16(n))
	if n == 0 {
		return buf
	}
	binary.LittleEndian.PutUint16(buf[2:4], expandMap[0])
	p := 4
	for i := 1; i < n; i++ {
		gap := uint32(expandMap[i]) - uint32(expandMap[i-1]) - 1
		if gap >= 255 {
			buf[p] = 0xFF
			binary.LittleEndian.PutUint16(buf[p+1:], uint16(gap))
			p += 3
		} else {
			buf[p] = byte(gap)
			p++
		}
	}
	return buf
}

// compressRLEWithFSE FSE-compresses a uint16 RLE stream, attempting two-state
// FSE first and falling back to single-state.
func compressRLEWithFSE(rleData []uint16) ([]byte, error) {
	var s ScratchU16
	fseData, err := FSECompressU16TwoState(rleData, &s)
	if err != nil {
		s2 := ScratchU16{}
		fseData, err = FSECompressU16(rleData, &s2)
		if err != nil {
			return nil, fmt.Errorf("FSE compress: %w", err)
		}
	}
	return fseData, nil
}
