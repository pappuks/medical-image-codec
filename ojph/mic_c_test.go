// Copyright 2021 Kuldeep Singh
// This source code is licensed under a MIT-style
// license that can be found in the LICENSE file.

//go:build cgo_ojph

// mic_c_test.go — Correctness test and 3-way benchmark: MIC-Go vs MIC-C vs HTJ2K.
package ojph

import (
	"encoding/binary"
	"fmt"
	"math"
	"math/bits"
	"os"
	"testing"
	"time"

	mic "mic"

	"github.com/suyashkumar/dicom"
	"github.com/suyashkumar/dicom/pkg/tag"
)

// loadTestImage loads a test image (same as htj2k_fair_comparison_test.go).
func loadTestImage(ti testImage) ([]byte, []uint16, uint16, int, int) {
	if ti.isBinary {
		byteData, _ := os.ReadFile(ti.fileName)
		cols, rows := ti.cols, ti.rows
		shortData := make([]uint16, cols*rows)
		var maxShort uint16
		for i := 0; i < len(byteData); i += 2 {
			v := binary.LittleEndian.Uint16(byteData[i:])
			shortData[i/2] = v
			if v > maxShort {
				maxShort = v
			}
		}
		return byteData, shortData, maxShort, cols, rows
	}
	dataset, err := dicom.ParseFile(ti.fileName, nil)
	if err != nil {
		return nil, nil, 0, 0, 0
	}
	pixelDataElement, _ := dataset.FindElementByTag(tag.PixelData)
	pixelDataInfo := dicom.MustGetPixelDataInfo(pixelDataElement.Value)
	fr := pixelDataInfo.Frames[0]
	nativeFrame, _ := fr.GetNativeFrame()
	shortData := make([]uint16, nativeFrame.Cols*nativeFrame.Rows)
	byteData := make([]byte, nativeFrame.Cols*nativeFrame.Rows*2)
	var maxShort uint16
	for j := 0; j < len(nativeFrame.Data); j++ {
		shortData[j] = uint16(nativeFrame.Data[j][0])
		if shortData[j] > maxShort {
			maxShort = shortData[j]
		}
		byteData[j*2] = byte(shortData[j])
		byteData[(j*2)+1] = byte(shortData[j] >> 8)
	}
	return byteData, shortData, maxShort, nativeFrame.Cols, nativeFrame.Rows
}

// TestMICCorrectnessSIMD verifies the SIMD decompression matches original pixels.
func TestMICCorrectnessSIMD(t *testing.T) {
	for _, ti := range testImages {
		t.Run(ti.name, func(t *testing.T) {
			_, shortData, maxShort, cols, rows := loadTestImage(ti)
			if len(shortData) == 0 {
				t.Skip("could not load image")
			}

			// Compress with Go
			var drc mic.DeltaRleCompressU16
			deltaComp, err := drc.Compress(shortData, cols, rows, maxShort)
			if err != nil {
				t.Fatalf("compress: %v", err)
			}
			var s mic.ScratchU16
			fseComp, err := mic.FSECompressU16TwoState(deltaComp, &s)
			if err != nil {
				t.Fatalf("FSE compress: %v", err)
			}

			// Decompress with SIMD
			simdPixels, err := MICDecompressTwoStateSIMD(fseComp, cols, rows)
			if err != nil {
				t.Fatalf("SIMD decompress: %v", err)
			}

			// Compare against original
			if len(simdPixels) != len(shortData) {
				t.Fatalf("length mismatch: SIMD=%d, orig=%d", len(simdPixels), len(shortData))
			}
			mismatches := 0
			for i := range shortData {
				if simdPixels[i] != shortData[i] {
					if mismatches < 10 {
						t.Errorf("pixel %d: SIMD=%d, orig=%d", i, simdPixels[i], shortData[i])
					}
					mismatches++
				}
			}
			if mismatches > 0 {
				t.Fatalf("%d pixel mismatches out of %d", mismatches, len(shortData))
			}
			t.Logf("OK: %s (%dx%d) — all %d pixels match", ti.name, cols, rows, len(shortData))
		})
	}
}

// TestMICCorrrectnessC verifies the C decompression matches Go decompression.
func TestMICCorrectnessC(t *testing.T) {
	for _, ti := range testImages {
		t.Run(ti.name, func(t *testing.T) {
			_, shortData, maxShort, cols, rows := loadTestImage(ti)
			if len(shortData) == 0 {
				t.Skip("could not load image")
			}

			// Compress with Go
			var drc mic.DeltaRleCompressU16
			deltaComp, err := drc.Compress(shortData, cols, rows, maxShort)
			if err != nil {
				t.Fatalf("compress: %v", err)
			}
			var s mic.ScratchU16
			fseComp, err := mic.FSECompressU16TwoState(deltaComp, &s)
			if err != nil {
				t.Fatalf("FSE compress: %v", err)
			}

			// Decompress with Go
			var s2 mic.ScratchU16
			rleData, err := mic.FSEDecompressU16TwoState(fseComp, &s2)
			if err != nil {
				t.Fatalf("Go FSE decompress: %v", err)
			}
			var drd mic.DeltaRleDecompressU16
			drd.Decompress(rleData, cols, rows)
			goPixels := drd.Out

			// Decompress with C
			cPixels, err := MICDecompressTwoStateC(fseComp, cols, rows)
			if err != nil {
				t.Fatalf("C decompress: %v", err)
			}

			// Compare
			if len(goPixels) != len(cPixels) {
				t.Fatalf("length mismatch: Go=%d, C=%d", len(goPixels), len(cPixels))
			}
			mismatches := 0
			for i := range goPixels {
				if goPixels[i] != cPixels[i] {
					if mismatches < 10 {
						t.Errorf("pixel %d: Go=%d, C=%d", i, goPixels[i], cPixels[i])
					}
					mismatches++
				}
			}
			if mismatches > 0 {
				t.Fatalf("%d pixel mismatches out of %d", mismatches, len(goPixels))
			}

			// Also verify against original
			for i := range shortData {
				if cPixels[i] != shortData[i] {
					t.Fatalf("C pixel %d: got %d, want %d (original)", i, cPixels[i], shortData[i])
				}
			}
			t.Logf("OK: %s (%dx%d) — all %d pixels match", ti.name, cols, rows, len(goPixels))
		})
	}
}

// TestMICCorrectnessFourStateC verifies four-state C decompression matches original pixels.
func TestMICCorrectnessFourStateC(t *testing.T) {
	for _, ti := range testImages {
		t.Run(ti.name, func(t *testing.T) {
			_, shortData, maxShort, cols, rows := loadTestImage(ti)
			if len(shortData) == 0 {
				t.Skip("could not load image")
			}

			var drc mic.DeltaRleCompressU16
			deltaComp, err := drc.Compress(shortData, cols, rows, maxShort)
			if err != nil {
				t.Fatalf("compress: %v", err)
			}
			var s mic.ScratchU16
			fse4Comp, err := mic.FSECompressU16FourState(deltaComp, &s)
			if err != nil {
				t.Fatalf("FSE4 compress: %v", err)
			}

			cPixels, err := MICDecompressFourStateC(fse4Comp, cols, rows)
			if err != nil {
				t.Fatalf("C 4-state decompress: %v", err)
			}
			simdPixels, err := MICDecompressFourStateSIMD(fse4Comp, cols, rows)
			if err != nil {
				t.Fatalf("C 4-state SIMD decompress: %v", err)
			}

			for i := range shortData {
				if cPixels[i] != shortData[i] {
					t.Fatalf("C pixel %d: got %d, want %d", i, cPixels[i], shortData[i])
				}
				if simdPixels[i] != shortData[i] {
					t.Fatalf("SIMD pixel %d: got %d, want %d", i, simdPixels[i], shortData[i])
				}
			}
			t.Logf("OK: %s (%dx%d) — all %d pixels match", ti.name, cols, rows, len(shortData))
		})
	}
}

// TestFourWayComparison prints a side-by-side comparison table: MIC-Go vs MIC-C vs MIC-SIMD vs HTJ2K.
func TestFourWayComparison(t *testing.T) {
	const decompRuns = 10

	type result struct {
		name          string
		width, height int
		origBytes     int
		micRatio      float64
		htj2kRatio    float64
		goDecompMs    float64
		cDecompMs     float64
		simdDecompMs  float64
		htj2kDecompMs float64
	}

	var results []result

	for _, ti := range testImages {
		byteData, shortData, maxShort, cols, rows := loadTestImage(ti)
		if len(shortData) == 0 {
			continue
		}
		origBytes := len(byteData)
		bitDepth := bits.Len16(maxShort)
		if bitDepth == 0 {
			bitDepth = 1
		}

		// Compress with MIC (Go)
		var drc mic.DeltaRleCompressU16
		deltaComp, _ := drc.Compress(shortData, cols, rows, maxShort)
		var s mic.ScratchU16
		fseComp, _ := mic.FSECompressU16TwoState(deltaComp, &s)

		// Compress with HTJ2K
		htj2kComp, err := CompressU16(shortData, cols, rows, bitDepth)
		if err != nil {
			t.Logf("Skipping HTJ2K for %s: %v", ti.name, err)
			continue
		}

		micRatio := float64(origBytes) / float64(len(fseComp))
		htj2kRatio := float64(origBytes) / float64(len(htj2kComp))

		// Benchmark MIC-Go decompress
		goMin := time.Duration(math.MaxInt64)
		for i := 0; i < decompRuns; i++ {
			start := time.Now()
			var s2 mic.ScratchU16
			rleData, _ := mic.FSEDecompressU16TwoState(fseComp, &s2)
			var drd mic.DeltaRleDecompressU16
			drd.Decompress(rleData, cols, rows)
			elapsed := time.Since(start)
			if elapsed < goMin {
				goMin = elapsed
			}
		}

		// Benchmark MIC-C (scalar) decompress
		cMin := time.Duration(math.MaxInt64)
		for i := 0; i < decompRuns; i++ {
			start := time.Now()
			MICDecompressTwoStateC(fseComp, cols, rows)
			elapsed := time.Since(start)
			if elapsed < cMin {
				cMin = elapsed
			}
		}

		// Benchmark MIC-C-SIMD decompress
		simdMin := time.Duration(math.MaxInt64)
		for i := 0; i < decompRuns; i++ {
			start := time.Now()
			MICDecompressTwoStateSIMD(fseComp, cols, rows)
			elapsed := time.Since(start)
			if elapsed < simdMin {
				simdMin = elapsed
			}
		}

		// Benchmark HTJ2K decompress
		htj2kMin := time.Duration(math.MaxInt64)
		for i := 0; i < decompRuns; i++ {
			start := time.Now()
			DecompressU16(htj2kComp, cols, rows)
			elapsed := time.Since(start)
			if elapsed < htj2kMin {
				htj2kMin = elapsed
			}
		}

		results = append(results, result{
			name:          ti.name,
			width:         cols,
			height:        rows,
			origBytes:     origBytes,
			micRatio:      micRatio,
			htj2kRatio:    htj2kRatio,
			goDecompMs:    float64(goMin.Microseconds()) / 1000.0,
			cDecompMs:     float64(cMin.Microseconds()) / 1000.0,
			simdDecompMs:  float64(simdMin.Microseconds()) / 1000.0,
			htj2kDecompMs: float64(htj2kMin.Microseconds()) / 1000.0,
		})
	}

	fmt.Println()
	fmt.Println("=== Four-Way Comparison: MIC-Go vs MIC-C vs MIC-SIMD vs HTJ2K (All In-Process) ===")
	fmt.Println()
	fmt.Printf("Decompression: best of %d runs. All codecs in-process.\n", decompRuns)
	fmt.Println()

	fmt.Printf("%-6s  %9s  %7s  %7s  %10s  %10s  %10s  %10s  %9s  %9s  %9s  %9s  %8s  %8s\n",
		"Image", "Orig(MB)", "MIC-r", "HTJ2K-r",
		"Go(ms)", "C(ms)", "SIMD(ms)", "HTJ2K(ms)",
		"Go GB/s", "C GB/s", "SIMD GB/s", "HTJ2K GB/s",
		"SIMD/C", "SIMD/HTJ2K")
	sep := "------  ---------  -------  -------  ----------  ----------  ----------  ----------  ---------  ---------  ---------  ----------  --------  --------"
	fmt.Println(sep)

	var geoSimdC, geoSimdHTJ2K float64
	count := 0

	for _, r := range results {
		origMB := float64(r.origBytes) / (1 << 20)
		goGBs := (float64(r.origBytes) / (1 << 30)) / (r.goDecompMs / 1000.0)
		cGBs := (float64(r.origBytes) / (1 << 30)) / (r.cDecompMs / 1000.0)
		simdGBs := (float64(r.origBytes) / (1 << 30)) / (r.simdDecompMs / 1000.0)
		htj2kGBs := (float64(r.origBytes) / (1 << 30)) / (r.htj2kDecompMs / 1000.0)
		simdOverC := simdGBs / cGBs
		simdOverHTJ2K := simdGBs / htj2kGBs

		fmt.Printf("%-6s  %9.2f  %7.2f  %7.2f  %10.2f  %10.2f  %10.2f  %10.2f  %9.2f  %9.2f  %9.2f  %10.2f  %8.2fx  %8.2fx\n",
			r.name, origMB, r.micRatio, r.htj2kRatio,
			r.goDecompMs, r.cDecompMs, r.simdDecompMs, r.htj2kDecompMs,
			goGBs, cGBs, simdGBs, htj2kGBs,
			simdOverC, simdOverHTJ2K)

		geoSimdC += math.Log(simdOverC)
		geoSimdHTJ2K += math.Log(simdOverHTJ2K)
		count++
	}
	fmt.Println(sep)
	if count > 0 {
		fmt.Printf("\nGeometric mean speedups:\n")
		fmt.Printf("  MIC-SIMD vs MIC-C-scalar:  %.2fx\n", math.Exp(geoSimdC/float64(count)))
		fmt.Printf("  MIC-SIMD vs HTJ2K:         %.2fx\n", math.Exp(geoSimdHTJ2K/float64(count)))
	}
}

// BenchmarkAllCodecs runs Go benchmarks for all decompressors: MIC-Go, MIC-4state,
// MIC-4state-C, MIC-4state-SIMD, MIC-C, MIC-SIMD, HTJ2K, JPEG-LS, and PICS-N
// (parallel strips with N=2/4/8).
func BenchmarkAllCodecs(b *testing.B) {
	for _, ti := range testImages {
		byteData, shortData, maxShort, cols, rows := loadTestImage(ti)
		if len(shortData) == 0 {
			continue
		}
		origBytes := len(byteData)
		bitDepth := bits.Len16(maxShort)
		if bitDepth < 2 {
			bitDepth = 2
		}

		var drc mic.DeltaRleCompressU16
		deltaComp, _ := drc.Compress(shortData, cols, rows, maxShort)
		var s mic.ScratchU16
		fseComp, _ := mic.FSECompressU16TwoState(deltaComp, &s)
		var s4 mic.ScratchU16
		fse4Comp, _ := mic.FSECompressU16FourState(deltaComp, &s4)

		htj2kComp, err := CompressU16(shortData, cols, rows, bitDepth)
		if err != nil {
			continue
		}

		jplsComp, err := CharlsCompressU16(shortData, cols, rows, bitDepth)
		if err != nil {
			continue
		}

		micRatio := float64(origBytes) / float64(len(fseComp))
		mic4Ratio := float64(origBytes) / float64(len(fse4Comp))
		htj2kRatio := float64(origBytes) / float64(len(htj2kComp))
		jplsRatio := float64(origBytes) / float64(len(jplsComp))

		b.Run("MIC-Go/"+ti.name, func(b *testing.B) {
			b.SetBytes(int64(origBytes))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				var s2 mic.ScratchU16
				rleData, _ := mic.FSEDecompressU16TwoState(fseComp, &s2)
				var drd mic.DeltaRleDecompressU16
				drd.Decompress(rleData, cols, rows)
			}
			b.ReportMetric(micRatio, "ratio")
		})

		b.Run("MIC-4state/"+ti.name, func(b *testing.B) {
			b.SetBytes(int64(origBytes))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				var sd mic.ScratchU16
				rleData, _ := mic.FSEDecompressU16FourState(fse4Comp, &sd)
				var drd mic.DeltaRleDecompressU16
				drd.Decompress(rleData, cols, rows)
			}
			b.ReportMetric(mic4Ratio, "ratio")
		})

		b.Run("MIC-4state-C/"+ti.name, func(b *testing.B) {
			b.SetBytes(int64(origBytes))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				MICDecompressFourStateC(fse4Comp, cols, rows)
			}
			b.ReportMetric(mic4Ratio, "ratio")
		})

		b.Run("MIC-4state-SIMD/"+ti.name, func(b *testing.B) {
			b.SetBytes(int64(origBytes))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				MICDecompressFourStateSIMD(fse4Comp, cols, rows)
			}
			b.ReportMetric(mic4Ratio, "ratio")
		})

		b.Run("MIC-C/"+ti.name, func(b *testing.B) {
			b.SetBytes(int64(origBytes))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				MICDecompressTwoStateC(fseComp, cols, rows)
			}
			b.ReportMetric(micRatio, "ratio")
		})

		b.Run("MIC-SIMD/"+ti.name, func(b *testing.B) {
			b.SetBytes(int64(origBytes))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				MICDecompressTwoStateSIMD(fseComp, cols, rows)
			}
			b.ReportMetric(micRatio, "ratio")
		})

		b.Run("HTJ2K/"+ti.name, func(b *testing.B) {
			b.SetBytes(int64(origBytes))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				DecompressU16(htj2kComp, cols, rows)
			}
			b.ReportMetric(htj2kRatio, "ratio")
		})

		b.Run("JPEGLS/"+ti.name, func(b *testing.B) {
			b.SetBytes(int64(origBytes))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				CharlsDecompressU16(jplsComp, cols, rows)
			}
			b.ReportMetric(jplsRatio, "ratio")
		})

		// PICS — Parallel Image Compressed Strips (2, 4, 8 strips)
		// PICS-N:   Go goroutines + Go decoder
		// PICS-C-N: C pthreads  + C SIMD auto-detect decoder (same blob)
		for _, strips := range []int{2, 4, 8} {
			strips := strips
			picsComp, err := mic.CompressParallelStrips(shortData, cols, rows, maxShort, strips)
			if err != nil {
				continue
			}
			picsRatio := float64(origBytes) / float64(len(picsComp))
			b.Run(fmt.Sprintf("PICS-%d/%s", strips, ti.name), func(b *testing.B) {
				b.SetBytes(int64(origBytes))
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					mic.DecompressParallelStrips(picsComp)
				}
				b.ReportMetric(picsRatio, "ratio")
			})
			b.Run(fmt.Sprintf("PICS-C-%d/%s", strips, ti.name), func(b *testing.B) {
				b.SetBytes(int64(origBytes))
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					MICDecompressParallelC(picsComp, cols, rows, strips)
				}
				b.ReportMetric(picsRatio, "ratio")
			})
		}
	}
}

// TestMICFullPipelineC verifies that the C encoder + C decoder round-trips correctly.
// Compresses with the C four-state encoder and decompresses with the C four-state
// decoder (both scalar and SIMD), checking all pixels match the original.
func TestMICFullPipelineC(t *testing.T) {
	for _, ti := range testImages {
		t.Run(ti.name, func(t *testing.T) {
			_, shortData, _, cols, rows := loadTestImage(ti)
			if len(shortData) == 0 {
				t.Skip("could not load image")
			}

			// C compress (four-state).
			comp, err := MICCompressFourStateC(shortData, cols, rows)
			if err != nil {
				t.Fatalf("C compress: %v", err)
			}

			// C decompress (scalar four-state).
			got, err := MICDecompressFourStateC(comp, cols, rows)
			if err != nil {
				t.Fatalf("C scalar decompress: %v", err)
			}
			for i := range shortData {
				if got[i] != shortData[i] {
					t.Fatalf("scalar pixel %d: got %d, want %d", i, got[i], shortData[i])
				}
			}

			// C decompress (SIMD four-state).
			gotSIMD, err := MICDecompressFourStateSIMD(comp, cols, rows)
			if err != nil {
				t.Fatalf("C SIMD decompress: %v", err)
			}
			for i := range shortData {
				if gotSIMD[i] != shortData[i] {
					t.Fatalf("SIMD pixel %d: got %d, want %d", i, gotSIMD[i], shortData[i])
				}
			}

			origBytes := cols * rows * 2
			ratio := float64(origBytes) / float64(len(comp))
			t.Logf("OK: %s (%dx%d) compressed %d→%d bytes (%.2fx)",
				ti.name, cols, rows, origBytes, len(comp), ratio)
		})
	}
}

// BenchmarkMICFullCPipelineVsHTJ2K benchmarks the complete C pipeline
// (C compress + C decompress) against HTJ2K (in-process OpenJPH).
// This gives the fairest comparison: both codecs entirely in C/C++.
func BenchmarkMICFullCPipelineVsHTJ2K(b *testing.B) {
	const decompRuns = 10
	for _, ti := range testImages {
		byteData, shortData, maxShort, cols, rows := loadTestImage(ti)
		if len(shortData) == 0 {
			continue
		}
		origBytes := len(byteData)
		bitDepth := bits.Len16(maxShort)
		if bitDepth < 2 {
			bitDepth = 2
		}

		// Pre-compress with C four-state.
		micComp, err := MICCompressFourStateC(shortData, cols, rows)
		if err != nil {
			b.Logf("Skipping %s: C compress error: %v", ti.name, err)
			continue
		}

		// Pre-compress with HTJ2K.
		htj2kComp, err := CompressU16(shortData, cols, rows, bitDepth)
		if err != nil {
			b.Logf("Skipping %s: HTJ2K compress error: %v", ti.name, err)
			continue
		}

		micRatio := float64(origBytes) / float64(len(micComp))
		htj2kRatio := float64(origBytes) / float64(len(htj2kComp))

		b.Run("MIC-4state-C/"+ti.name, func(b *testing.B) {
			b.SetBytes(int64(origBytes))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				MICDecompressFourStateSIMD(micComp, cols, rows)
			}
			b.ReportMetric(micRatio, "ratio")
		})

		b.Run("HTJ2K/"+ti.name, func(b *testing.B) {
			b.SetBytes(int64(origBytes))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				DecompressU16(htj2kComp, cols, rows)
			}
			b.ReportMetric(htj2kRatio, "ratio")
		})
		_ = decompRuns
	}
}

// TestMICFullCPipelineSummary prints a human-readable comparison table of
// the complete C encoder+decoder pipeline vs HTJ2K.
func TestMICFullCPipelineSummary(t *testing.T) {
	const decompRuns = 10

	type result struct {
		name                  string
		w, h                  int
		origBytes             int
		micRatio, htj2kRatio  float64
		micCompMs, htj2kCompMs float64
		micDecompMs, htj2kDecompMs float64
	}

	var results []result

	for _, ti := range testImages {
		byteData, shortData, maxShort, cols, rows := loadTestImage(ti)
		if len(shortData) == 0 {
			t.Logf("Skipping %s: could not load", ti.name)
			continue
		}
		origBytes := len(byteData)
		bitDepth := bits.Len16(maxShort)
		if bitDepth < 2 {
			bitDepth = 2
		}

		// C compress (four-state).
		cCompStart := time.Now()
		micComp, err := MICCompressFourStateC(shortData, cols, rows)
		if err != nil {
			t.Logf("Skipping %s: C compress: %v", ti.name, err)
			continue
		}
		micCompMs := float64(time.Since(cCompStart).Microseconds()) / 1000.0

		// HTJ2K compress.
		htj2kCompStart := time.Now()
		htj2kComp, err := CompressU16(shortData, cols, rows, bitDepth)
		if err != nil {
			t.Logf("Skipping %s: HTJ2K compress: %v", ti.name, err)
			continue
		}
		htj2kCompMs := float64(time.Since(htj2kCompStart).Microseconds()) / 1000.0

		// MIC decompress (C SIMD, best of N).
		micMin := time.Duration(math.MaxInt64)
		for i := 0; i < decompRuns; i++ {
			start := time.Now()
			MICDecompressFourStateSIMD(micComp, cols, rows)
			if d := time.Since(start); d < micMin {
				micMin = d
			}
		}

		// HTJ2K decompress (best of N).
		htj2kMin := time.Duration(math.MaxInt64)
		for i := 0; i < decompRuns; i++ {
			start := time.Now()
			DecompressU16(htj2kComp, cols, rows)
			if d := time.Since(start); d < htj2kMin {
				htj2kMin = d
			}
		}

		results = append(results, result{
			name:         ti.name,
			w: cols, h: rows,
			origBytes:    origBytes,
			micRatio:     float64(origBytes) / float64(len(micComp)),
			htj2kRatio:   float64(origBytes) / float64(len(htj2kComp)),
			micCompMs:    micCompMs,
			htj2kCompMs:  htj2kCompMs,
			micDecompMs:  float64(micMin.Microseconds()) / 1000.0,
			htj2kDecompMs: float64(htj2kMin.Microseconds()) / 1000.0,
		})
	}

	fmt.Println()
	fmt.Println("=== MIC (Full C 4-State Pipeline) vs HTJ2K — In-Process, No Subprocess ===")
	fmt.Println()
	fmt.Printf("MIC encoder: C (Delta→RLE→FSE 4-state). MIC decoder: C SIMD.\n")
	fmt.Printf("HTJ2K: OpenJPH via CGO. Decompression = best of %d runs.\n", decompRuns)
	fmt.Println()
	fmt.Printf("%-6s  %8s  %10s  %7s  %7s  %9s  %9s  %9s  %9s  %8s  %10s  %8s\n",
		"Image", "Orig(MB)", "WxH",
		"MIC-r", "HTJ2K-r",
		"MIC-c(ms)", "HTJ2K-c(ms)",
		"MIC-d(ms)", "HTJ2K-d(ms)",
		"MIC GB/s", "HTJ2K GB/s", "Speedup")
	sep := "------  --------  ----------  -------  -------  ---------  -----------  ---------  -----------  --------  ----------  --------"
	fmt.Println(sep)

	var geoSpeedup float64
	count := 0
	for _, r := range results {
		origMB := float64(r.origBytes) / (1 << 20)
		micGBs := (float64(r.origBytes) / (1 << 30)) / (r.micDecompMs / 1000.0)
		htj2kGBs := (float64(r.origBytes) / (1 << 30)) / (r.htj2kDecompMs / 1000.0)
		speedup := micGBs / htj2kGBs
		fmt.Printf("%-6s  %8.2f  %4dx%-4d  %7.2f  %7.2f  %9.1f  %11.1f  %9.2f  %11.2f  %8.2f  %10.2f  %7.2fx\n",
			r.name, origMB, r.w, r.h,
			r.micRatio, r.htj2kRatio,
			r.micCompMs, r.htj2kCompMs,
			r.micDecompMs, r.htj2kDecompMs,
			micGBs, htj2kGBs, speedup)
		geoSpeedup += math.Log(speedup)
		count++
	}
	fmt.Println(sep)
	if count > 0 {
		fmt.Printf("\nGeometric mean decompression speedup (MIC-C-4state-SIMD / HTJ2K): %.2fx\n",
			math.Exp(geoSpeedup/float64(count)))
	}
}
