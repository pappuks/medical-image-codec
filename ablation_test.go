// Copyright 2026 Kuldeep Singh
// This source code is licensed under a MIT-style
// license that can be found in the LICENSE file.

package mic

import (
	"bytes"
	"encoding/binary"
	"encoding/csv"
	"fmt"
	"math/bits"
	"os"
	"os/exec"
	"strconv"
	"testing"
	"time"
)

// ── helpers ──────────────────────────────────────────────────────────────────

// fseCompressWithForcedTableLog runs the full FSE compression pipeline on
// already-RLE-encoded data, overriding the adaptive tableLog selection.
// The forced value is clamped to [minTablelog, naturalMax] so it is always valid.
func fseCompressWithForcedTableLog(rleData []uint16, forcedTL uint8) ([]byte, error) {
	var s ScratchU16
	s.TableLog = forcedTL

	s2, err := s.prepare(rleData, nil)
	if err != nil {
		return nil, err
	}

	maxCount := s2.countSimple(rleData)
	s2.clearCount = true
	s2.maxCount = 0

	if maxCount == len(rleData) {
		return nil, ErrUseRLE
	}
	if maxCount == 1 || maxCount < (len(rleData)>>15) {
		return nil, ErrIncompressible
	}

	// Let optimalTableLog compute valid min/max limits, then override with
	// the requested value (clamped so normalizeCount never sees an illegal log).
	s2.optimalTableLog()
	naturalMax := s2.actualTableLog // upper bound set by data size
	minBits := s2.minTableLog()
	tl := forcedTL
	if tl < minBits {
		tl = minBits
	}
	if tl > naturalMax {
		tl = naturalMax
	}
	s2.actualTableLog = tl

	if err := s2.normalizeCount(); err != nil {
		return nil, err
	}
	if err := s2.writeCount(); err != nil {
		return nil, err
	}
	if err := s2.buildCTable(); err != nil {
		return nil, err
	}
	if err := s2.compress(rleData); err != nil {
		return nil, err
	}

	out := s2.bw.out
	if len(out) >= len(rleData)*2 {
		return nil, ErrIncompressible
	}
	return out, nil
}

// measureDecompSpeed times FSE decompression of a compressed blob over N iterations.
func measureDecompSpeed(compressed []byte, rawBytes int, n int) float64 {
	start := time.Now()
	for i := 0; i < n; i++ {
		var sd ScratchU16
		FSEDecompressU16(compressed, &sd)
	}
	elapsed := time.Since(start)
	perOp := elapsed / time.Duration(n)
	mbPerSec := float64(rawBytes) / (1 << 20) / perOp.Seconds()
	return mbPerSec
}

// ── TableLog ablation ─────────────────────────────────────────────────────────

// TestTableLogAblation compresses each test image with forced tableLog = 11, 12, 13
// and with the adaptive selection, reporting compression ratio and decompression
// throughput for each configuration.
//
// Run with:
//
//	go test -run TestTableLogAblation -v
func TestTableLogAblation(t *testing.T) {
	const iters = 20 // decompression timing iterations per cell

	fmt.Println()
	fmt.Printf("TableLog Ablation — Compression Ratio (×) and Decompression Throughput (MB/s)\n")
	fmt.Printf("Image  | TL=11 ratio | TL=12 ratio | TL=13 ratio | Adap ratio | TL=11 MB/s | TL=12 MB/s | TL=13 MB/s | Adap MB/s\n")
	fmt.Printf("-------|-------------|-------------|-------------|------------|------------|------------|------------|----------\n")

	for _, tf := range testFiles {
		t.Run(tf.name, func(t *testing.T) {
			byteData, shortData, maxShort, cols, rows := SetupTests(tf)
			if shortData == nil {
				t.Skip("test data not available")
			}
			rawBytes := len(byteData)

			// Build RLE stream (shared input for all tableLog variants)
			var drc DeltaRleCompressU16
			rleData, err := drc.Compress(shortData, cols, rows, maxShort)
			if err != nil {
				t.Fatalf("delta+RLE: %v", err)
			}
			rleBytes := len(rleData) * 2

			type result struct {
				ratio float64
				mbps  float64
			}
			var res [4]result // TL11, TL12, TL13, Adaptive

			for i, tl := range []uint8{11, 12, 13, 0 /* 0 = adaptive */} {
				var comp []byte
				if tl == 0 {
					// Adaptive: standard FSECompressU16
					var s ScratchU16
					c, err := FSECompressU16(rleData, &s)
					if err != nil {
						t.Logf("adaptive FSE: %v", err)
						continue
					}
					comp = c
				} else {
					c, err := fseCompressWithForcedTableLog(rleData, tl)
					if err != nil {
						t.Logf("TL=%d FSE: %v", tl, err)
						continue
					}
					comp = c
				}
				res[i].ratio = float64(rawBytes) / float64(len(comp))
				res[i].mbps = measureDecompSpeed(comp, rleBytes, iters)
			}

			fmt.Printf("%-6s | %11.3f | %11.3f | %11.3f | %10.3f | %10.0f | %10.0f | %10.0f | %9.0f\n",
				tf.name,
				res[0].ratio, res[1].ratio, res[2].ratio, res[3].ratio,
				res[0].mbps, res[1].mbps, res[2].mbps, res[3].mbps,
			)
		})
	}
}

// ── Predictor ablation ────────────────────────────────────────────────────────

// leftOnlyDeltaCompressU16 applies a left-neighbor-only predictor.
// For the first column, the predictor is 0 (no left neighbor).
func leftOnlyDeltaCompressU16(in []uint16, width, height int, maxValue uint16) ([]uint16, error) {
	pixelDepth := bits.Len16(maxValue)
	deltaThreshold := uint16((1 << (pixelDepth - 1)) - 1)
	delimiterForOverflow := uint16((1 << pixelDepth) - 1)
	out := make([]uint16, 0, width*height*2)
	out = append(out, maxValue)

	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			index := y*width + x

			var predicted int32
			if x > 0 {
				predicted = int32(in[index-1])
			}

			inputVal := in[index]
			diff := int32(inputVal) - predicted

			if uint16(abs(diff)) >= deltaThreshold {
				out = append(out, delimiterForOverflow)
				out = append(out, inputVal)
			} else {
				out = append(out, uint16(int32(deltaThreshold)+diff))
			}
		}
	}
	return out, nil
}

// leftOnlyDeltaDecompressU16 inverts leftOnlyDeltaCompressU16.
func leftOnlyDeltaDecompressU16(in []uint16, width, height int) []uint16 {
	maxValue := in[0]
	pixelDepth := bits.Len16(maxValue)
	deltaThreshold := uint16((1 << (pixelDepth - 1)) - 1)
	delimiterForOverflow := uint16((1 << pixelDepth) - 1)
	out := make([]uint16, width*height)
	ic := 1

	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			index := y*width + x
			var predicted int32
			if x > 0 {
				predicted = int32(out[index-1])
			}
			v := in[ic]
			ic++
			if v == delimiterForOverflow {
				out[index] = in[ic]
				ic++
			} else {
				out[index] = uint16(int32(v) - int32(deltaThreshold) + predicted)
			}
		}
	}
	return out
}

// paethPredict returns the Paeth predictor value (PNG spec).
// a = left, b = top, c = top-left.  Always returns one of {a, b, c}.
func paethPredict(a, b, c int32) int32 {
	p := a + b - c
	pa := p - a
	if pa < 0 {
		pa = -pa
	}
	pb := p - b
	if pb < 0 {
		pb = -pb
	}
	pc := p - c
	if pc < 0 {
		pc = -pc
	}
	if pa <= pb && pa <= pc {
		return a
	}
	if pb <= pc {
		return b
	}
	return c
}

// paethDeltaCompressU16 applies the Paeth predictor to 16-bit pixel data.
func paethDeltaCompressU16(in []uint16, width, height int, maxValue uint16) ([]uint16, error) {
	pixelDepth := bits.Len16(maxValue)
	deltaThreshold := uint16((1 << (pixelDepth - 1)) - 1)
	delimiterForOverflow := uint16((1 << pixelDepth) - 1)
	out := make([]uint16, 0, width*height*2)
	out = append(out, maxValue)

	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			index := y*width + x

			var predicted int32
			if x == 0 && y == 0 {
				predicted = 0
			} else if y == 0 {
				predicted = int32(in[index-1]) // left only
			} else if x == 0 {
				predicted = int32(in[index-width]) // top only
			} else {
				a := int32(in[index-1])
				b := int32(in[index-width])
				c := int32(in[index-width-1])
				predicted = paethPredict(a, b, c)
			}

			inputVal := in[index]
			diff := int32(inputVal) - predicted

			if uint16(abs(diff)) >= deltaThreshold {
				out = append(out, delimiterForOverflow)
				out = append(out, inputVal)
			} else {
				out = append(out, uint16(int32(deltaThreshold)+diff))
			}
		}
	}
	return out, nil
}

// paethDeltaDecompressU16 inverts paethDeltaCompressU16.
func paethDeltaDecompressU16(in []uint16, width, height int) []uint16 {
	maxValue := in[0]
	pixelDepth := bits.Len16(maxValue)
	deltaThreshold := uint16((1 << (pixelDepth - 1)) - 1)
	delimiterForOverflow := uint16((1 << pixelDepth) - 1)
	out := make([]uint16, width*height)
	ic := 1

	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			index := y*width + x

			var predicted int32
			if x == 0 && y == 0 {
				predicted = 0
			} else if y == 0 {
				predicted = int32(out[index-1])
			} else if x == 0 {
				predicted = int32(out[index-width])
			} else {
				a := int32(out[index-1])
				b := int32(out[index-width])
				c := int32(out[index-width-1])
				predicted = paethPredict(a, b, c)
			}

			v := in[ic]
			ic++
			if v == delimiterForOverflow {
				out[index] = in[ic]
				ic++
			} else {
				out[index] = uint16(int32(v) - int32(deltaThreshold) + predicted)
			}
		}
	}
	return out
}

// compressPredictorPipeline runs Delta(predictor)+RLE+FSE and returns (ratio, compSize).
func compressPredictorPipeline(deltaStream []uint16, rawBytes int) (float64, int) {
	var rle RleCompressU16
	rle.Init(0, 0, deltaStream[0]) // maxValue in first element
	rleOut := rle.Compress(deltaStream)

	var s ScratchU16
	fseOut, err := FSECompressU16(rleOut, &s)
	if err != nil {
		return 0, 0
	}
	ratio := float64(rawBytes) / float64(len(fseOut))
	return ratio, len(fseOut)
}

// TestPredictorAblation compares four predictors through the full RLE+FSE pipeline:
// left-only, avg (current MIC), Paeth, and MED (JPEG-LS).
//
// Run with:
//
//	go test -run TestPredictorAblation -v
func TestPredictorAblation(t *testing.T) {
	fmt.Println()
	fmt.Printf("Predictor Ablation — Compression Ratio (×)\n")
	fmt.Printf("%-6s | %10s | %10s | %10s | %10s | %10s | %10s\n",
		"Image", "Left-only", "Avg(MIC)", "Paeth", "MED(JLS)", "Avg→Paeth", "Avg→MED")
	fmt.Printf("-------|-----------|-----------|-----------|-----------|-----------|----------\n")

	for _, tf := range testFiles {
		t.Run(tf.name, func(t *testing.T) {
			byteData, shortData, maxShort, cols, rows := SetupTests(tf)
			if shortData == nil {
				t.Skip("test data not available")
			}
			rawBytes := len(byteData)

			// ── Left-only ──
			leftDelta, err := leftOnlyDeltaCompressU16(shortData, cols, rows, maxShort)
			if err != nil {
				t.Fatalf("left-only delta: %v", err)
			}
			// Verify roundtrip
			leftOut := leftOnlyDeltaDecompressU16(leftDelta, cols, rows)
			for i, v := range shortData {
				if leftOut[i] != v {
					t.Fatalf("left-only roundtrip fail at %d: want %d got %d", i, v, leftOut[i])
				}
			}
			leftRatio, _ := compressPredictorPipeline(leftDelta, rawBytes)

			// ── Avg (standard MIC) ──
			var drc DeltaRleCompressU16
			avgRleData, err := drc.Compress(shortData, cols, rows, maxShort)
			if err != nil {
				t.Fatalf("avg delta+RLE: %v", err)
			}
			var s1 ScratchU16
			avgComp, err := FSECompressU16(avgRleData, &s1)
			var avgRatio float64
			if err == nil {
				avgRatio = float64(rawBytes) / float64(len(avgComp))
			}

			// ── Paeth ──
			paethDelta, err := paethDeltaCompressU16(shortData, cols, rows, maxShort)
			if err != nil {
				t.Fatalf("paeth delta: %v", err)
			}
			// Verify roundtrip
			paethOut := paethDeltaDecompressU16(paethDelta, cols, rows)
			for i, v := range shortData {
				if paethOut[i] != v {
					t.Fatalf("paeth roundtrip fail at %d: want %d got %d", i, v, paethOut[i])
				}
			}
			paethRatio, _ := compressPredictorPipeline(paethDelta, rawBytes)

			// ── MED (JPEG-LS) ──
			medDelta, err := MEDDeltaCompressU16(shortData, cols, rows, maxShort)
			if err != nil {
				t.Fatalf("MED delta: %v", err)
			}
			medRatio, _ := compressPredictorPipeline(medDelta, rawBytes)

			avgToPaeth := 0.0
			if avgRatio > 0 {
				avgToPaeth = (paethRatio - avgRatio) / avgRatio * 100
			}
			avgToMED := 0.0
			if avgRatio > 0 {
				avgToMED = (medRatio - avgRatio) / avgRatio * 100
			}

			fmt.Printf("%-6s | %9.3f× | %9.3f× | %9.3f× | %9.3f× | %+9.2f%% | %+9.2f%%\n",
				tf.name,
				leftRatio, avgRatio, paethRatio, medRatio,
				avgToPaeth, avgToMED,
			)
		})
	}
}

// TestPredictorAblationDecompSpeed benchmarks decompression throughput for
// each predictor variant through the full RLE+FSE pipeline.
//
// Run with:
//
//	go test -run TestPredictorAblationDecompSpeed -v
func TestPredictorAblationDecompSpeed(t *testing.T) {
	const iters = 20

	fmt.Println()
	fmt.Printf("Predictor Decompression Speed (MB/s, full RLE+FSE pipeline on RLE stream)\n")
	fmt.Printf("%-6s | %10s | %10s | %10s | %10s\n",
		"Image", "Left-only", "Avg(MIC)", "Paeth", "MED(JLS)")
	fmt.Printf("-------|-----------|-----------|-----------|----------\n")

	for _, tf := range testFiles {
		t.Run(tf.name, func(t *testing.T) {
			byteData, shortData, maxShort, cols, rows := SetupTests(tf)
			if shortData == nil {
				t.Skip("test data not available")
			}
			rawBytes := len(byteData)

			type compResult struct {
				comp     []byte
				rleBytes int
			}

			buildComp := func(deltaStream []uint16) compResult {
				var rle RleCompressU16
				rle.Init(0, 0, deltaStream[0])
				rleOut := rle.Compress(deltaStream)
				var s ScratchU16
				c, err := FSECompressU16(rleOut, &s)
				if err != nil {
					return compResult{}
				}
				return compResult{c, len(rleOut) * 2}
			}

			leftDelta, _ := leftOnlyDeltaCompressU16(shortData, cols, rows, maxShort)
			leftCR := buildComp(leftDelta)

			var drc DeltaRleCompressU16
			avgRle, _ := drc.Compress(shortData, cols, rows, maxShort)
			var s1 ScratchU16
			avgComp, _ := FSECompressU16(avgRle, &s1)
			avgRleBytes := len(avgRle) * 2

			paethDelta, _ := paethDeltaCompressU16(shortData, cols, rows, maxShort)
			paethCR := buildComp(paethDelta)

			medDelta, _ := MEDDeltaCompressU16(shortData, cols, rows, maxShort)
			medCR := buildComp(medDelta)

			_ = rawBytes

			leftMBs := measureDecompSpeed(leftCR.comp, leftCR.rleBytes, iters)
			avgMBs := measureDecompSpeed(avgComp, avgRleBytes, iters)
			paethMBs := measureDecompSpeed(paethCR.comp, paethCR.rleBytes, iters)
			medMBs := measureDecompSpeed(medCR.comp, medCR.rleBytes, iters)

			fmt.Printf("%-6s | %9.0f  | %9.0f  | %9.0f  | %9.0f\n",
				tf.name, leftMBs, avgMBs, paethMBs, medMBs)
		})
	}
}

// TestDumpHistogramCSV writes delta-residual histograms for a representative set of
// images to paper/figures/histogram_data.csv for use in figure generation.
//
// Run with:
//
//	go test -run TestDumpHistogramCSV -v
func TestDumpHistogramCSV(t *testing.T) {
	// Images to include: one per distinct modality group
	targets := map[string]bool{"MR": true, "CT": true, "MG1": true, "CR": true, "XA1": true}

	if err := os.MkdirAll("paper/figures", 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	f, err := os.Create("paper/figures/histogram_data.csv")
	if err != nil {
		t.Fatalf("create csv: %v", err)
	}
	defer f.Close()

	w := csv.NewWriter(f)
	w.Write([]string{"image", "residual", "count"})

	for _, tf := range testFiles {
		if !targets[tf.name] {
			continue
		}
		t.Run(tf.name, func(t *testing.T) {
			_, shortData, maxShort, cols, rows := SetupTests(tf)
			if shortData == nil {
				t.Skip("test data not available")
			}

			pixelDepth := bits.Len16(maxShort)
			deltaThreshold := int32((1 << (pixelDepth - 1)) - 1)

			// Build a histogram of signed residuals, clamped to [-128, 128] for display
			hist := make(map[int]int)
			for y := 0; y < rows; y++ {
				for x := 0; x < cols; x++ {
					idx := y*cols + x
					var pred int32
					if x > 0 && y > 0 {
						pred = (int32(shortData[idx-1]) + int32(shortData[idx-cols])) >> 1
					} else if x > 0 {
						pred = int32(shortData[idx-1])
					} else if y > 0 {
						pred = int32(shortData[idx-cols])
					}
					diff := int32(shortData[idx]) - pred
					// Skip overflow-delimiter values
					if abs(diff) >= deltaThreshold {
						diff = 0 // count as zero for histogram clarity
					}
					// Clamp to [-255, 255] for the CSV
					if diff < -255 {
						diff = -255
					}
					if diff > 255 {
						diff = 255
					}
					hist[int(diff)]++
				}
			}

			// Write sorted rows
			for r := -255; r <= 255; r++ {
				if c, ok := hist[r]; ok && c > 0 {
					w.Write([]string{tf.name, strconv.Itoa(r), strconv.Itoa(c)})
				}
			}
			t.Logf("%s: %d pixels, histogram written", tf.name, rows*cols)
		})
	}
	w.Flush()
	if err := w.Error(); err != nil {
		t.Fatalf("csv flush: %v", err)
	}
	t.Logf("wrote paper/figures/histogram_data.csv")
}

// TestDeltaZstdRatio compresses each test image with the MIC delta predictor
// (avg of top+left) followed by zstd at level 19, reporting the compression ratio.
// This provides the Delta+Zstandard baseline used in Table VIII of the paper.
//
// Run with:
//
//	go test -run TestDeltaZstdRatio -v
func TestDeltaZstdRatio(t *testing.T) {
	if _, err := exec.LookPath("zstd"); err != nil {
		t.Skip("zstd not found in PATH")
	}

	fmt.Println()
	fmt.Printf("Delta+Zstandard (level 19) Compression Ratios\n")
	fmt.Printf("%-6s | Raw (MB) | Delta+Zstd-19 ratio\n", "Image")
	fmt.Printf("-------|----------|--------------------\n")

	for _, tf := range testFiles {
		t.Run(tf.name, func(t *testing.T) {
			byteData, shortData, maxShort, cols, rows := SetupTests(tf)
			if shortData == nil {
				t.Skip("test data not available")
			}
			rawBytes := len(byteData)

			// Apply MIC delta encoding (avg predictor + overflow delimiter).
			deltaOut, err := DeltaCompressU16(shortData, cols, rows, maxShort)
			if err != nil {
				t.Fatalf("delta: %v", err)
			}

			// Serialize delta residuals as little-endian uint16 bytes.
			buf := make([]byte, len(deltaOut)*2)
			for i, v := range deltaOut {
				binary.LittleEndian.PutUint16(buf[i*2:], v)
			}

			// Compress with zstd -19 via stdin→stdout pipe.
			cmd := exec.Command("zstd", "-19", "--no-progress", "-c")
			cmd.Stdin = bytes.NewReader(buf)
			var out bytes.Buffer
			cmd.Stdout = &out
			if err := cmd.Run(); err != nil {
				t.Fatalf("zstd: %v", err)
			}

			compressedSize := out.Len()
			ratio := float64(rawBytes) / float64(compressedSize)
			fmt.Printf("%-6s | %8.2f | %.2f×\n",
				tf.name, float64(rawBytes)/(1<<20), ratio)
		})
	}
}
