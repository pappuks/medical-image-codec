// Copyright 2024 Kuldeep Singh
// This source code is licensed under a MIT-style
// license that can be found in the LICENSE file.

package mic

import (
	"encoding/binary"
	"fmt"
	"runtime"
	"sync"
)

// PICS — Parallel Image Compressed Strips
//
// A single image is divided into N horizontal strip bands, each compressed
// independently via the standard Delta+RLE+FSE pipeline.  Because the strips
// share no data, all N compress or decompress concurrently on separate CPU cores.
//
// Binary format:
//
//	Bytes  0-3:  Magic "PICS"
//	Bytes  4-7:  Width           (uint32 LE)
//	Bytes  8-11: Total height    (uint32 LE)
//	Bytes 12-15: NumStrips       (uint32 LE)
//	Bytes 16-19: StripHeight     (uint32 LE) — rows per strip; last strip may be shorter
//	Bytes 20+:   Offset table    (NumStrips × [offset_u32, length_u32])
//	After table: Concatenated compressed strip blobs (each a CompressSingleFrame output)
//
// Compression ratio impact
//
// The only accuracy loss is at strip boundaries: the first row of each non-zero
// strip cannot use the previous strip's last row as a top-neighbour predictor, so
// the vertical predictor falls back to 0 for those rows (same as the first image
// row).  For typical medical images:
//   - 2 strips  → 1 boundary row / total rows → ≈ 0.1 % ratio loss
//   - 4 strips  → 3 boundary rows / total rows → ≈ 0.2–0.3 % ratio loss
//   - 8 strips  → 7 boundary rows / total rows → ≈ 0.4–0.6 % ratio loss
//   - 16 strips → 15 boundary rows / total rows → ≈ 0.8–1.2 % ratio loss
//
// All strip blobs are valid standalone MIC streams, so decompression is also
// fully parallel and trivially load-balanced across cores.

const (
	picsMagic      = "PICS"
	picsHeaderBase = 20 // 4+4+4+4+4 bytes before offset table
)

// CompressParallelStrips compresses pixels using numStrips goroutines, one per
// horizontal strip.  numStrips <= 0 selects GOMAXPROCS automatically.
//
// Each strip is encoded with CompressSingleFrame (Delta+RLE+FSE two-state,
// falling back to single-state).  The resulting PICS blob can be decoded with
// DecompressParallelStrips, which recovers the full image in parallel.
func CompressParallelStrips(pixels []uint16, width, height int, maxValue uint16, numStrips int) ([]byte, error) {
	if len(pixels) != width*height {
		return nil, fmt.Errorf("parallelstrips: pixel count %d != width*height %d", len(pixels), width*height)
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

	// Nominal strip height — last strip may be shorter.
	stripH := (height + numStrips - 1) / numStrips
	// Recompute actual strip count in case stripH rounds down.
	actual := (height + stripH - 1) / stripH

	results := make([][]byte, actual)
	errs := make([]error, actual)

	var wg sync.WaitGroup
	for s := 0; s < actual; s++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			y0 := idx * stripH
			y1 := y0 + stripH
			if y1 > height {
				y1 = height
			}
			sh := y1 - y0
			blob, err := CompressSingleFrame(pixels[y0*width:y1*width], width, sh, maxValue)
			results[idx] = blob
			errs[idx] = err
		}(s)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			return nil, fmt.Errorf("parallelstrips: strip %d: %w", i, err)
		}
	}

	// Build output: fixed header + offset table + strip blobs.
	headerSize := picsHeaderBase + actual*8
	totalData := 0
	for _, r := range results {
		totalData += len(r)
	}

	out := make([]byte, headerSize+totalData)
	copy(out[0:4], picsMagic)
	binary.LittleEndian.PutUint32(out[4:8], uint32(width))
	binary.LittleEndian.PutUint32(out[8:12], uint32(height))
	binary.LittleEndian.PutUint32(out[12:16], uint32(actual))
	binary.LittleEndian.PutUint32(out[16:20], uint32(stripH))

	offset := 0
	for s, r := range results {
		tblOff := picsHeaderBase + s*8
		binary.LittleEndian.PutUint32(out[tblOff:tblOff+4], uint32(offset))
		binary.LittleEndian.PutUint32(out[tblOff+4:tblOff+8], uint32(len(r)))
		copy(out[headerSize+offset:], r)
		offset += len(r)
	}
	return out, nil
}

// DecompressParallelStrips recovers an image from a PICS blob produced by
// CompressParallelStrips.  All strips are decompressed concurrently.
// Returns pixels (row-major, uint16), width, height.
func DecompressParallelStrips(compressed []byte) (pixels []uint16, width, height int, err error) {
	if len(compressed) < picsHeaderBase || string(compressed[0:4]) != picsMagic {
		return nil, 0, 0, fmt.Errorf("parallelstrips: invalid magic")
	}

	width = int(binary.LittleEndian.Uint32(compressed[4:8]))
	height = int(binary.LittleEndian.Uint32(compressed[8:12]))
	numStrips := int(binary.LittleEndian.Uint32(compressed[12:16]))
	stripH := int(binary.LittleEndian.Uint32(compressed[16:20]))

	headerSize := picsHeaderBase + numStrips*8
	if len(compressed) < headerSize {
		return nil, 0, 0, fmt.Errorf("parallelstrips: truncated header")
	}
	if width <= 0 || height <= 0 || numStrips <= 0 || stripH <= 0 {
		return nil, 0, 0, fmt.Errorf("parallelstrips: invalid dimensions")
	}

	out := make([]uint16, width*height)
	errs := make([]error, numStrips)

	var wg sync.WaitGroup
	for s := 0; s < numStrips; s++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			tblOff := picsHeaderBase + idx*8
			stripOffset := int(binary.LittleEndian.Uint32(compressed[tblOff : tblOff+4]))
			stripLen := int(binary.LittleEndian.Uint32(compressed[tblOff+4 : tblOff+8]))

			start := headerSize + stripOffset
			end := start + stripLen
			if start < 0 || end > len(compressed) || start > end {
				errs[idx] = fmt.Errorf("strip %d: offset out of bounds", idx)
				return
			}

			y0 := idx * stripH
			y1 := y0 + stripH
			if y1 > height {
				y1 = height
			}
			sh := y1 - y0

			stripPixels, decErr := DecompressSingleFrame(compressed[start:end], width, sh)
			if decErr != nil {
				errs[idx] = fmt.Errorf("strip %d: %w", idx, decErr)
				return
			}
			copy(out[y0*width:], stripPixels)
		}(s)
	}
	wg.Wait()

	for _, e := range errs {
		if e != nil {
			return nil, 0, 0, e
		}
	}
	return out, width, height, nil
}
