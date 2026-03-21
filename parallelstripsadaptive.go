// Copyright 2026 Kuldeep Singh
// This source code is licensed under a MIT-style
// license that can be found in the LICENSE file.

package mic

import (
	"encoding/binary"
	"fmt"
	"runtime"
	"sync"
)

// PICA — Parallel Image Compressed Adaptive
//
// Extends PICS with two improvements:
//
//  1. Per-strip predictor selection: each strip independently chooses avg or
//     gradient-adaptive predictor by trying both and keeping the smaller result.
//
//  2. Content-adaptive strip partitioning: strip boundaries are placed at
//     entropy transitions (equal-cost partitioning on inter-row variance)
//     rather than equal-height splits. Strips with uniform content get better
//     per-strip FSE tables.
//
// Binary format:
//
//	Bytes  0-3:  Magic "PICA"
//	Bytes  4-7:  Width           (uint32 LE)
//	Bytes  8-11: Total height    (uint32 LE)
//	Bytes 12-15: NumStrips       (uint32 LE)
//	Bytes 16+:   Offset table    (NumStrips × 16 bytes)
//	             [y0_u32, offset_u32, length_u32, flags_u32]
//	After table: Concatenated compressed strip blobs
//
// flags bits:
//
//	bit 0: picaFlagGradPredictor — strip was encoded with gradient-adaptive predictor
const (
	picaMagic     = "PICA"
	picaEntrySize = 16 // y0(4) + offset(4) + length(4) + flags(4)
	picaHdrSize   = 16 // magic(4) + width(4) + height(4) + numStrips(4)
)

// picaFlagGradPredictor indicates the strip was compressed with
// CompressSingleFrameGrad (CALIC-style predictor) rather than CompressSingleFrame.
const picaFlagGradPredictor = uint32(1 << 0)

// CompressParallelStripsAdaptive compresses pixels using content-adaptive strip
// boundaries and per-strip predictor selection (tries both avg and grad, keeps
// the smaller). numStrips <= 0 selects GOMAXPROCS automatically.
//
// The resulting PICA blob can be decoded with DecompressParallelStripsAdaptive.
func CompressParallelStripsAdaptive(pixels []uint16, width, height int, maxValue uint16, numStrips int) ([]byte, error) {
	if len(pixels) != width*height {
		return nil, fmt.Errorf("pica: pixel count %d != width*height %d", len(pixels), width*height)
	}
	if numStrips <= 0 {
		numStrips = runtime.GOMAXPROCS(0)
	}
	if numStrips > height {
		numStrips = height
	}
	if numStrips < 1 {
		numStrips = 1
	}

	// Compute content-adaptive strip start rows.
	starts := adaptiveStripBoundaries(pixels, width, height, numStrips)
	actual := len(starts)

	results := make([][]byte, actual)
	stripFlags := make([]uint32, actual)
	errs := make([]error, actual)

	var wg sync.WaitGroup
	for s := 0; s < actual; s++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			y0 := starts[idx]
			y1 := height
			if idx+1 < actual {
				y1 = starts[idx+1]
			}
			sh := y1 - y0
			strip := pixels[y0*width : y1*width]

			// Try avg predictor.
			blobAvg, err1 := CompressSingleFrame(strip, width, sh, maxValue)
			// Try gradient-adaptive predictor.
			blobGrad, err2 := CompressSingleFrameGrad(strip, width, sh, maxValue)

			// Keep whichever is smaller (or the one that succeeded).
			if err2 == nil && (err1 != nil || len(blobGrad) <= len(blobAvg)) {
				results[idx] = blobGrad
				stripFlags[idx] = picaFlagGradPredictor
				errs[idx] = nil
			} else {
				results[idx] = blobAvg
				stripFlags[idx] = 0
				errs[idx] = err1
			}
		}(s)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			return nil, fmt.Errorf("pica: strip %d: %w", i, err)
		}
	}

	// Build output: header + offset table + blobs.
	headerSize := picaHdrSize + actual*picaEntrySize
	totalData := 0
	for _, r := range results {
		totalData += len(r)
	}

	out := make([]byte, headerSize+totalData)
	copy(out[0:4], picaMagic)
	binary.LittleEndian.PutUint32(out[4:8], uint32(width))
	binary.LittleEndian.PutUint32(out[8:12], uint32(height))
	binary.LittleEndian.PutUint32(out[12:16], uint32(actual))

	dataOff := 0
	for s, r := range results {
		eOff := picaHdrSize + s*picaEntrySize
		binary.LittleEndian.PutUint32(out[eOff:eOff+4], uint32(starts[s]))
		binary.LittleEndian.PutUint32(out[eOff+4:eOff+8], uint32(dataOff))
		binary.LittleEndian.PutUint32(out[eOff+8:eOff+12], uint32(len(r)))
		binary.LittleEndian.PutUint32(out[eOff+12:eOff+16], stripFlags[s])
		copy(out[headerSize+dataOff:], r)
		dataOff += len(r)
	}
	return out, nil
}

// DecompressParallelStripsAdaptive recovers an image from a PICA blob.
// All strips are decompressed concurrently using the pipeline recorded per strip.
func DecompressParallelStripsAdaptive(compressed []byte) (pixels []uint16, width, height int, err error) {
	if len(compressed) < picaHdrSize || string(compressed[0:4]) != picaMagic {
		return nil, 0, 0, fmt.Errorf("pica: invalid magic")
	}

	width = int(binary.LittleEndian.Uint32(compressed[4:8]))
	height = int(binary.LittleEndian.Uint32(compressed[8:12]))
	numStrips := int(binary.LittleEndian.Uint32(compressed[12:16]))

	headerSize := picaHdrSize + numStrips*picaEntrySize
	if len(compressed) < headerSize {
		return nil, 0, 0, fmt.Errorf("pica: truncated header")
	}
	if width <= 0 || height <= 0 || numStrips <= 0 {
		return nil, 0, 0, fmt.Errorf("pica: invalid dimensions")
	}

	type entry struct {
		y0, offset, length int
		flags              uint32
	}
	entries := make([]entry, numStrips)
	for i := 0; i < numStrips; i++ {
		off := picaHdrSize + i*picaEntrySize
		entries[i] = entry{
			y0:     int(binary.LittleEndian.Uint32(compressed[off : off+4])),
			offset: int(binary.LittleEndian.Uint32(compressed[off+4 : off+8])),
			length: int(binary.LittleEndian.Uint32(compressed[off+8 : off+12])),
			flags:  binary.LittleEndian.Uint32(compressed[off+12 : off+16]),
		}
	}

	out := make([]uint16, width*height)
	decErrs := make([]error, numStrips)

	var wg sync.WaitGroup
	for s := 0; s < numStrips; s++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			e := entries[idx]
			y1 := height
			if idx+1 < numStrips {
				y1 = entries[idx+1].y0
			}
			sh := y1 - e.y0

			start := headerSize + e.offset
			end := start + e.length
			if start < 0 || end > len(compressed) || start > end {
				decErrs[idx] = fmt.Errorf("strip %d: offset out of bounds", idx)
				return
			}

			var stripPixels []uint16
			var decErr error
			if e.flags&picaFlagGradPredictor != 0 {
				stripPixels, decErr = DecompressSingleFrameGrad(compressed[start:end], width, sh)
			} else {
				stripPixels, decErr = DecompressSingleFrame(compressed[start:end], width, sh)
			}
			if decErr != nil {
				decErrs[idx] = fmt.Errorf("strip %d: %w", idx, decErr)
				return
			}
			copy(out[e.y0*width:], stripPixels)
		}(s)
	}
	wg.Wait()

	for _, e := range decErrs {
		if e != nil {
			return nil, 0, 0, e
		}
	}
	return out, width, height, nil
}

// adaptiveStripBoundaries computes content-adaptive strip start rows using
// equal-cost partitioning on inter-row absolute-delta variance.
//
// Each strip gets a similar "complexity budget", which means strips over smooth
// regions are wider (more rows) while strips over high-variance regions are
// narrower. This gives per-strip FSE tables a more uniform symbol distribution,
// improving compression on mixed-content images.
func adaptiveStripBoundaries(pixels []uint16, width, height, numStrips int) []int {
	if numStrips >= height {
		starts := make([]int, height)
		for i := range starts {
			starts[i] = i
		}
		return starts
	}
	if numStrips == 1 {
		return []int{0}
	}

	// Row cost = mean absolute vertical delta (proxy for row entropy change).
	rowCost := make([]float64, height)
	for y := 1; y < height; y++ {
		var sum uint64
		for x := 0; x < width; x++ {
			d := int32(pixels[y*width+x]) - int32(pixels[(y-1)*width+x])
			if d < 0 {
				d = -d
			}
			sum += uint64(d)
		}
		rowCost[y] = float64(sum)
	}

	// Cumulative cost.
	cum := make([]float64, height+1)
	for y := 0; y < height; y++ {
		cum[y+1] = cum[y] + rowCost[y]
	}
	total := cum[height]

	starts := make([]int, numStrips)
	starts[0] = 0

	if total == 0 {
		// Uniform image: equal-height fallback.
		for i := 1; i < numStrips; i++ {
			starts[i] = i * height / numStrips
		}
		return starts
	}

	for i := 1; i < numStrips; i++ {
		target := total * float64(i) / float64(numStrips)
		// Binary search: first row where cumulative cost >= target.
		lo, hi := starts[i-1]+1, height
		for lo < hi {
			mid := (lo + hi) >> 1
			if cum[mid] < target {
				lo = mid + 1
			} else {
				hi = mid
			}
		}
		if lo >= height {
			lo = height - 1
		}
		starts[i] = lo
	}
	return starts
}
