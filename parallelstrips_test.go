// Copyright 2024 Kuldeep Singh
// This source code is licensed under a MIT-style
// license that can be found in the LICENSE file.

package mic

import (
	"fmt"
	"runtime"
	"testing"
	"time"
)

// TestParallelStripsRoundtrip verifies that CompressParallelStrips +
// DecompressParallelStrips produce a pixel-exact reconstruction for all test
// modalities and a range of strip counts (1, 2, 4, GOMAXPROCS).
func TestParallelStripsRoundtrip(t *testing.T) {
	stripCounts := []int{1, 2, 4, runtime.GOMAXPROCS(0)}

	for _, td := range testFiles {
		_, pixels, maxVal, width, height := SetupTests(td)

		for _, n := range stripCounts {
			n := n
			t.Run(td.name+"_strips"+itoa(n), func(t *testing.T) {
				compressed, err := CompressParallelStrips(pixels, width, height, maxVal, n)
				if err != nil {
					t.Fatalf("compress: %v", err)
				}

				got, w, h, err := DecompressParallelStrips(compressed)
				if err != nil {
					t.Fatalf("decompress: %v", err)
				}
				if w != width || h != height {
					t.Fatalf("dimension mismatch: got %dx%d, want %dx%d", w, h, width, height)
				}
				if len(got) != len(pixels) {
					t.Fatalf("pixel count mismatch: got %d, want %d", len(got), len(pixels))
				}
				for i := range pixels {
					if got[i] != pixels[i] {
						t.Fatalf("pixel mismatch at index %d (row %d col %d): got %d, want %d",
							i, i/width, i%width, got[i], pixels[i])
					}
				}
			})
		}
	}
}

// TestParallelStripsCompressionRatio measures how strip count affects compression
// ratio on a representative large image (MG3 if available, else MR).
func TestParallelStripsCompressionRatio(t *testing.T) {
	// Use MR (small, always present) for ratio logging; not a failure condition.
	td := testFiles[0] // MR 256x256
	_, pixels, maxVal, width, height := SetupTests(td)

	baselineBlob, err := CompressSingleFrame(pixels, width, height, maxVal)
	if err != nil {
		t.Fatalf("single-frame baseline: %v", err)
	}
	rawBytes := width * height * 2
	t.Logf("%-12s raw=%d  single_frame=%d (%.2fx)",
		td.name, rawBytes, len(baselineBlob), float64(rawBytes)/float64(len(baselineBlob)))

	for _, n := range []int{2, 4, 8, 16} {
		blob, err := CompressParallelStrips(pixels, width, height, maxVal, n)
		if err != nil {
			t.Errorf("strips=%d: %v", n, err)
			continue
		}
		ratio := float64(rawBytes) / float64(len(blob))
		overhead := float64(len(blob)-len(baselineBlob)) / float64(len(baselineBlob)) * 100
		t.Logf("  strips=%-3d compressed=%d (%.2fx)  overhead vs single=+%.2f%%",
			n, len(blob), ratio, overhead)
	}
}

// TestParallelStripsFormatValidation checks header parsing and error paths using
// the MR test image (real medical data, guaranteed compressible).
func TestParallelStripsFormatValidation(t *testing.T) {
	td := testFiles[0] // MR 256x256
	_, pixels, maxVal, width, height := SetupTests(td)

	blob, err := CompressParallelStrips(pixels, width, height, maxVal, 2)
	if err != nil {
		t.Fatal(err)
	}

	// Corrupt magic → should fail.
	bad := make([]byte, len(blob))
	copy(bad, blob)
	bad[0] = 'X'
	if _, _, _, err := DecompressParallelStrips(bad); err == nil {
		t.Fatal("expected error on bad magic, got nil")
	}

	// Truncated data → should fail.
	if _, _, _, err := DecompressParallelStrips(blob[:10]); err == nil {
		t.Fatal("expected error on truncated data, got nil")
	}

	// Valid round-trip.
	got, w, h, err := DecompressParallelStrips(blob)
	if err != nil {
		t.Fatal(err)
	}
	if w != width || h != height {
		t.Fatalf("dims: got %dx%d, want %dx%d", w, h, width, height)
	}
	for i, p := range pixels {
		if got[i] != p {
			t.Fatalf("pixel %d: got %d want %d", i, got[i], p)
		}
	}
}

// TestParallelStripsSingleRowImage verifies strips >= height clamp to height strips.
// Uses the first row of the MR image to guarantee compressibility.
func TestParallelStripsSingleRowImage(t *testing.T) {
	td := testFiles[0] // MR 256x256
	_, pixels, maxVal, width, height := SetupTests(td)

	// Use only the first two rows to keep the test tiny, confirming that
	// numStrips > height is handled gracefully (clamped to 2).
	rows := 2
	pixels = pixels[:width*rows]
	blob, err := CompressParallelStrips(pixels, width, rows, maxVal, height)
	if err != nil {
		t.Fatal(err)
	}
	got, w, h, err := DecompressParallelStrips(blob)
	if err != nil {
		t.Fatal(err)
	}
	if w != width || h != rows {
		t.Fatalf("dims: got %dx%d, want %dx%d", w, h, width, rows)
	}
	for i, p := range pixels {
		if got[i] != p {
			t.Fatalf("pixel %d: got %d want %d", i, got[i], p)
		}
	}
}

// BenchmarkParallelStripsCompress benchmarks parallel compression at different
// strip counts using the CR image (large, good test of CPU scaling).
func BenchmarkParallelStripsCompress(b *testing.B) {
	td := testFiles[2] // CR 1760x2140
	_, pixels, maxVal, width, height := SetupTests(td)

	for _, n := range []int{1, 2, 4, 8} {
		n := n
		b.Run("strips"+itoa(n), func(b *testing.B) {
			b.SetBytes(int64(width * height * 2))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := CompressParallelStrips(pixels, width, height, maxVal, n); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkParallelStripsDecompress benchmarks parallel decompression at
// different strip counts using the CR image.
func BenchmarkParallelStripsDecompress(b *testing.B) {
	td := testFiles[2] // CR 1760x2140
	_, pixels, maxVal, width, height := SetupTests(td)

	for _, n := range []int{1, 2, 4, 8} {
		n := n
		blob, err := CompressParallelStrips(pixels, width, height, maxVal, n)
		if err != nil {
			b.Fatal(err)
		}
		b.Run("strips"+itoa(n), func(b *testing.B) {
			b.SetBytes(int64(width * height * 2))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, _, _, err := DecompressParallelStrips(blob); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkPICSVsAllCodecs compares PICS-4 and PICS-8 decompression against
// MIC-Go (2-state) and MIC-4state across all test images.  Runs without any
// CGO dependency — use BenchmarkAllCodecs (ojph/) for the full HTJ2K/JPEG-LS
// comparison when those libraries are available.
func BenchmarkPICSVsAllCodecs(b *testing.B) {
	for _, td := range testFiles {
		_, shortData, maxShort, cols, rows := SetupTests(td)
		if len(shortData) == 0 {
			continue
		}
		origBytes := cols * rows * 2

		// Pre-compress with each codec.
		micComp, _ := CompressSingleFrame(shortData, cols, rows, maxShort)

		var drc DeltaRleCompressU16
		deltaComp, _ := drc.Compress(shortData, cols, rows, maxShort)
		var s4 ScratchU16
		mic4Comp, _ := FSECompressU16FourState(deltaComp, &s4)

		picsComps := map[int][]byte{}
		for _, n := range []int{1, 2, 4, 8} {
			blob, _ := CompressParallelStrips(shortData, cols, rows, maxShort, n)
			picsComps[n] = blob
		}

		micRatio := float64(origBytes) / float64(len(micComp))
		mic4Ratio := float64(origBytes) / float64(len(mic4Comp))

		b.Run("MIC-Go/"+td.name, func(b *testing.B) {
			b.SetBytes(int64(origBytes))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				DecompressSingleFrame(micComp, cols, rows)
			}
			b.ReportMetric(micRatio, "ratio")
		})

		b.Run("MIC-4state/"+td.name, func(b *testing.B) {
			b.SetBytes(int64(origBytes))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				var sd ScratchU16
				rleData, _ := FSEDecompressU16FourState(mic4Comp, &sd)
				var drd DeltaRleDecompressU16
				drd.Decompress(rleData, cols, rows)
			}
			b.ReportMetric(mic4Ratio, "ratio")
		})

		for _, n := range []int{1, 2, 4, 8} {
			n := n
			blob := picsComps[n]
			picsRatio := float64(origBytes) / float64(len(blob))
			b.Run(fmt.Sprintf("PICS-%d/%s", n, td.name), func(b *testing.B) {
				b.SetBytes(int64(origBytes))
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					DecompressParallelStrips(blob)
				}
				b.ReportMetric(picsRatio, "ratio")
			})
		}
	}
}

// TestPICSComparisonTable prints a human-readable comparison table of all
// pure-Go codecs (MIC-Go, MIC-4state, PICS-1/2/4/8) across all test images.
func TestPICSComparisonTable(t *testing.T) {
	const iters = 10

	type row struct {
		image                string
		origMB               float64
		micRatio, mic4Ratio  float64
		picsRatio            [5]float64 // index = strips: 1,2,4,8 at [0..3]
		micGBs, mic4GBs      float64
		picsGBs              [4]float64
	}

	stripCounts := []int{1, 2, 4, 8}
	var results []row

	for _, td := range testFiles {
		_, shortData, maxShort, cols, rows := SetupTests(td)
		if len(shortData) == 0 {
			t.Logf("skip %s: load failed", td.name)
			continue
		}
		origBytes := cols * rows * 2

		// Compress.
		micComp, _ := CompressSingleFrame(shortData, cols, rows, maxShort)
		var drc DeltaRleCompressU16
		deltaComp, _ := drc.Compress(shortData, cols, rows, maxShort)
		var s4 ScratchU16
		mic4Comp, _ := FSECompressU16FourState(deltaComp, &s4)

		picsBlobs := make([][]byte, len(stripCounts))
		for i, n := range stripCounts {
			picsBlobs[i], _ = CompressParallelStrips(shortData, cols, rows, maxShort, n)
		}

		// Time MIC-Go decompress.
		for i := 0; i < 3; i++ {
			DecompressSingleFrame(micComp, cols, rows)
		}
		start := time.Now()
		for i := 0; i < iters; i++ {
			DecompressSingleFrame(micComp, cols, rows)
		}
		micMs := float64(time.Since(start).Microseconds()) / float64(iters) / 1000.0

		// Time MIC-4state decompress.
		for i := 0; i < 3; i++ {
			var sd ScratchU16
			r, _ := FSEDecompressU16FourState(mic4Comp, &sd)
			var drd DeltaRleDecompressU16
			drd.Decompress(r, cols, rows)
		}
		start = time.Now()
		for i := 0; i < iters; i++ {
			var sd ScratchU16
			r, _ := FSEDecompressU16FourState(mic4Comp, &sd)
			var drd DeltaRleDecompressU16
			drd.Decompress(r, cols, rows)
		}
		mic4Ms := float64(time.Since(start).Microseconds()) / float64(iters) / 1000.0

		// Time PICS decompress for each strip count.
		var picsMs [4]float64
		for i, blob := range picsBlobs {
			for j := 0; j < 3; j++ {
				DecompressParallelStrips(blob)
			}
			start = time.Now()
			for j := 0; j < iters; j++ {
				DecompressParallelStrips(blob)
			}
			picsMs[i] = float64(time.Since(start).Microseconds()) / float64(iters) / 1000.0
		}

		gbDiv := float64(origBytes) / (1 << 30)
		r := row{
			image:    td.name,
			origMB:   float64(origBytes) / (1 << 20),
			micRatio: float64(origBytes) / float64(len(micComp)),
			mic4Ratio: float64(origBytes) / float64(len(mic4Comp)),
			micGBs:   gbDiv / (micMs / 1000.0),
			mic4GBs:  gbDiv / (mic4Ms / 1000.0),
		}
		for i, n := range stripCounts {
			_ = n
			r.picsRatio[i] = float64(origBytes) / float64(len(picsBlobs[i]))
			r.picsGBs[i] = gbDiv / (picsMs[i] / 1000.0)
		}
		results = append(results, r)
	}

	// Print header.
	fmt.Println()
	fmt.Println("=== PICS vs MIC-Go vs MIC-4state — Decompression Throughput (all images) ===")
	fmt.Printf("Platform: %s/%s  GOMAXPROCS=%d  iterations=%d\n\n",
		"linux", "amd64", runtime.GOMAXPROCS(0), iters)

	hdr := fmt.Sprintf("%-6s %6s  %7s %7s %7s %7s %7s %7s",
		"Image", "MB", "MIC-Go", "MIC-4s", "PICS-1", "PICS-2", "PICS-4", "PICS-8")
	sep := "------  ------  -------  -------  -------  -------  -------  -------"
	fmt.Println(hdr + "   (GB/s decompression)")
	fmt.Println(sep)

	for _, r := range results {
		fmt.Printf("%-6s %6.2f  %7.2f  %7.2f  %7.2f  %7.2f  %7.2f  %7.2f\n",
			r.image, r.origMB,
			r.micGBs, r.mic4GBs,
			r.picsGBs[0], r.picsGBs[1], r.picsGBs[2], r.picsGBs[3])
	}

	fmt.Println(sep)
	fmt.Println()

	// Speedup table: PICS-4 and PICS-8 vs MIC-Go and MIC-4state.
	fmt.Println("=== Speedup: PICS-4 and PICS-8 relative to single-threaded codecs ===")
	fmt.Println()
	hdr2 := fmt.Sprintf("%-6s %6s  %12s %12s  %12s %12s",
		"Image", "MB", "PICS-4/MIC-Go", "PICS-4/MIC-4s", "PICS-8/MIC-Go", "PICS-8/MIC-4s")
	sep2 := "------  ------  ------------  ------------  ------------  ------------"
	fmt.Println(hdr2)
	fmt.Println(sep2)
	for _, r := range results {
		p4go := r.picsGBs[2] / r.micGBs
		p4mic4 := r.picsGBs[2] / r.mic4GBs
		p8go := r.picsGBs[3] / r.micGBs
		p8mic4 := r.picsGBs[3] / r.mic4GBs
		fmt.Printf("%-6s %6.2f  %11.2fx  %11.2fx  %11.2fx  %11.2fx\n",
			r.image, r.origMB, p4go, p4mic4, p8go, p8mic4)
	}
	fmt.Println(sep2)
	fmt.Println()

	// Ratio table.
	fmt.Println("=== Compression ratio: MIC-Go vs MIC-4state vs PICS-1/2/4/8 ===")
	fmt.Println()
	hdr3 := fmt.Sprintf("%-6s %7s %7s %7s %7s %7s %7s",
		"Image", "MIC-Go", "MIC-4s", "PICS-1", "PICS-2", "PICS-4", "PICS-8")
	sep3 := "------  -------  -------  -------  -------  -------  -------"
	fmt.Println(hdr3)
	fmt.Println(sep3)
	for _, r := range results {
		fmt.Printf("%-6s %7.2fx %7.2fx %7.2fx %7.2fx %7.2fx %7.2fx\n",
			r.image,
			r.micRatio, r.mic4Ratio,
			r.picsRatio[0], r.picsRatio[1], r.picsRatio[2], r.picsRatio[3])
	}
	fmt.Println(sep3)
}

// itoa converts an int to string for sub-test naming without importing strconv.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	pos := len(buf)
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}
