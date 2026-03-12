// Copyright 2021 Kuldeep Singh
// This source code is licensed under a MIT-style
// license that can be found in the LICENSE file.

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

// TestThreeWayComparison prints a side-by-side comparison table: MIC-Go vs MIC-C vs HTJ2K.
func TestThreeWayComparison(t *testing.T) {
	const decompRuns = 10

	type result struct {
		name          string
		width, height int
		origBytes     int
		micRatio      float64
		htj2kRatio    float64
		goDecompMs    float64
		cDecompMs     float64
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

		// Benchmark MIC-C decompress
		cMin := time.Duration(math.MaxInt64)
		for i := 0; i < decompRuns; i++ {
			start := time.Now()
			MICDecompressTwoStateC(fseComp, cols, rows)
			elapsed := time.Since(start)
			if elapsed < cMin {
				cMin = elapsed
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
			htj2kDecompMs: float64(htj2kMin.Microseconds()) / 1000.0,
		})
	}

	fmt.Println()
	fmt.Println("=== Three-Way Comparison: MIC-Go vs MIC-C vs HTJ2K (All In-Process) ===")
	fmt.Println()
	fmt.Printf("Decompression: best of %d runs. All codecs in-process, no subprocess or I/O overhead.\n", decompRuns)
	fmt.Println()

	fmt.Printf("%-6s  %9s  %7s  %7s  %10s  %10s  %10s  %9s  %9s  %9s  %7s  %7s\n",
		"Image", "Orig(MB)", "MIC-r", "HTJ2K-r",
		"Go-d(ms)", "C-d(ms)", "HTJ2K-d(ms)",
		"Go GB/s", "C GB/s", "HTJ2K GB/s",
		"C/Go", "C/HTJ2K")
	sep := "------  ---------  -------  -------  ----------  ----------  ----------  ---------  ---------  ----------  -------  -------"
	fmt.Println(sep)

	var geoGoC, geoCHTJ2K float64
	count := 0

	for _, r := range results {
		origMB := float64(r.origBytes) / (1 << 20)
		goGBs := (float64(r.origBytes) / (1 << 30)) / (r.goDecompMs / 1000.0)
		cGBs := (float64(r.origBytes) / (1 << 30)) / (r.cDecompMs / 1000.0)
		htj2kGBs := (float64(r.origBytes) / (1 << 30)) / (r.htj2kDecompMs / 1000.0)
		cOverGo := cGBs / goGBs
		cOverHTJ2K := cGBs / htj2kGBs

		fmt.Printf("%-6s  %9.2f  %7.2f  %7.2f  %10.2f  %10.2f  %10.2f  %9.2f  %9.2f  %10.2f  %7.2fx  %7.2fx\n",
			r.name, origMB, r.micRatio, r.htj2kRatio,
			r.goDecompMs, r.cDecompMs, r.htj2kDecompMs,
			goGBs, cGBs, htj2kGBs,
			cOverGo, cOverHTJ2K)

		geoGoC += math.Log(cOverGo)
		geoCHTJ2K += math.Log(cOverHTJ2K)
		count++
	}
	fmt.Println(sep)
	if count > 0 {
		fmt.Printf("\nGeometric mean speedups:\n")
		fmt.Printf("  MIC-C vs MIC-Go:  %.2fx\n", math.Exp(geoGoC/float64(count)))
		fmt.Printf("  MIC-C vs HTJ2K:   %.2fx\n", math.Exp(geoCHTJ2K/float64(count)))
	}
}

// BenchmarkThreeWay runs Go benchmarks for all three decompressors.
func BenchmarkThreeWay(b *testing.B) {
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

		var drc mic.DeltaRleCompressU16
		deltaComp, _ := drc.Compress(shortData, cols, rows, maxShort)
		var s mic.ScratchU16
		fseComp, _ := mic.FSECompressU16TwoState(deltaComp, &s)

		htj2kComp, err := CompressU16(shortData, cols, rows, bitDepth)
		if err != nil {
			continue
		}

		micRatio := float64(origBytes) / float64(len(fseComp))
		htj2kRatio := float64(origBytes) / float64(len(htj2kComp))

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

		b.Run("MIC-C/"+ti.name, func(b *testing.B) {
			b.SetBytes(int64(origBytes))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				MICDecompressTwoStateC(fseComp, cols, rows)
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
	}
}
