// Copyright 2021 Kuldeep Singh
// This source code is licensed under a MIT-style
// license that can be found in the LICENSE file.

//go:build cgo_ojph

// JPEG-LS Comparison Framework (In-Process)
//
// Compares MIC (Delta+RLE+FSE) against JPEG-LS (lossless) using CharLS
// as an in-process library via CGO. This provides a fair apples-to-apples
// comparison — both codecs are invoked as library calls with no subprocess
// or file I/O overhead.
//
// Run with:
//
//	go test -tags cgo_ojph -v -run TestJPEGLSComparison ./ojph/ -timeout 300s
//	go test -tags cgo_ojph -run=^$ -bench=BenchmarkJPEGLSDecomp ./ojph/ -benchtime=10x
package ojph

import (
	"fmt"
	"math/bits"
	"testing"
	"time"

	mic "mic"
)

// TestJPEGLSComparison runs a full comparison of MIC vs JPEG-LS (CharLS)
// across all test images, reporting compression ratio and decompression speed.
func TestJPEGLSComparison(t *testing.T) {
	type result struct {
		name          string
		width, height int
		origBytes     int
		micRatio      float64
		jplsRatio     float64
		micDecompMs   float64
		jplsDecompMs  float64
	}

	var results []result

	for _, ti := range testImages {
		t.Run(ti.name, func(t *testing.T) {
			_, shortData, maxShort, cols, rows := loadImage(ti)
			if shortData == nil {
				t.Skip("could not load image")
			}
			origBytes := len(shortData) * 2
			bitDepth := bits.Len16(maxShort)
			if bitDepth < 2 {
				bitDepth = 2 // CharLS requires bits_per_sample >= 2
			}

			// --- MIC compress ---
			micCompressed, err := mic.CompressSingleFrame(shortData, cols, rows, maxShort)
			if err != nil {
				t.Fatalf("MIC compress failed: %v", err)
			}
			micRatio := float64(origBytes) / float64(len(micCompressed))

			// --- JPEG-LS compress ---
			jplsCompressed, err := CharlsCompressU16(shortData, cols, rows, bitDepth)
			if err != nil {
				t.Fatalf("JPEG-LS compress failed: %v", err)
			}
			jplsRatio := float64(origBytes) / float64(len(jplsCompressed))

			// --- MIC decompress (warmup + timed) ---
			for i := 0; i < 3; i++ {
				_, _ = mic.DecompressSingleFrame(micCompressed, cols, rows)
			}
			const iters = 10
			micStart := time.Now()
			for i := 0; i < iters; i++ {
				_, err = mic.DecompressSingleFrame(micCompressed, cols, rows)
				if err != nil {
					t.Fatalf("MIC decompress failed: %v", err)
				}
			}
			micDecompMs := float64(time.Since(micStart).Microseconds()) / float64(iters) / 1000.0

			// --- JPEG-LS decompress (warmup + timed) ---
			for i := 0; i < 3; i++ {
				_, _ = CharlsDecompressU16(jplsCompressed, cols, rows)
			}
			jplsStart := time.Now()
			for i := 0; i < iters; i++ {
				_, err = CharlsDecompressU16(jplsCompressed, cols, rows)
				if err != nil {
					t.Fatalf("JPEG-LS decompress failed: %v", err)
				}
			}
			jplsDecompMs := float64(time.Since(jplsStart).Microseconds()) / float64(iters) / 1000.0

			// --- Verify lossless ---
			jplsDecoded, err := CharlsDecompressU16(jplsCompressed, cols, rows)
			if err != nil {
				t.Fatalf("JPEG-LS decompress verification failed: %v", err)
			}
			for i := range shortData {
				if shortData[i] != jplsDecoded[i] {
					t.Fatalf("JPEG-LS roundtrip mismatch at pixel %d: want %d got %d", i, shortData[i], jplsDecoded[i])
				}
			}

			micMBs := float64(origBytes) / micDecompMs / 1000.0
			jplsMBs := float64(origBytes) / jplsDecompMs / 1000.0

			t.Logf("%-4s %4dx%-4d  MIC: %.2fx  JPEG-LS: %.2fx  MIC decomp: %.1f MB/s  JPEG-LS decomp: %.1f MB/s",
				ti.name, cols, rows, micRatio, jplsRatio, micMBs, jplsMBs)

			results = append(results, result{
				name: ti.name, width: cols, height: rows, origBytes: origBytes,
				micRatio: micRatio, jplsRatio: jplsRatio,
				micDecompMs: micDecompMs, jplsDecompMs: jplsDecompMs,
			})
		})
	}

	// Summary table
	fmt.Println("\n=== MIC vs JPEG-LS (CharLS) Comparison ===")
	fmt.Printf("%-6s %12s %12s %12s %15s %15s %10s\n",
		"Image", "MIC ratio", "JPLS ratio", "MIC win", "MIC MB/s", "JPLS MB/s", "Speed")
	fmt.Println("------  ------------  ------------  ------------  ---------------  ---------------  ----------")
	for _, r := range results {
		micMBs := float64(r.origBytes) / r.micDecompMs / 1000.0
		jplsMBs := float64(r.origBytes) / r.jplsDecompMs / 1000.0
		ratioWin := fmt.Sprintf("%+.1f%%", (r.micRatio/r.jplsRatio-1)*100)
		speedup := fmt.Sprintf("%.2fx", micMBs/jplsMBs)
		fmt.Printf("%-6s %11.2fx %11.2fx %12s %14.0f %14.0f %10s\n",
			r.name, r.micRatio, r.jplsRatio, ratioWin, micMBs, jplsMBs, speedup)
	}
}

// BenchmarkJPEGLSDecomp benchmarks JPEG-LS decompression via CharLS (in-process).
func BenchmarkJPEGLSDecomp(b *testing.B) {
	for _, ti := range testImages {
		_, shortData, maxShort, cols, rows := loadImage(ti)
		if shortData == nil {
			continue
		}
		origBytes := len(shortData) * 2
		bitDepth := bits.Len16(maxShort)
		if bitDepth < 2 {
			bitDepth = 2
		}

		// Pre-compress with both codecs
		micCompressed, err := mic.CompressSingleFrame(shortData, cols, rows, maxShort)
		if err != nil {
			b.Fatalf("MIC compress failed: %v", err)
		}
		jplsCompressed, err := CharlsCompressU16(shortData, cols, rows, bitDepth)
		if err != nil {
			b.Fatalf("JPEG-LS compress failed: %v", err)
		}

		b.Run(ti.name+"/MIC", func(b *testing.B) {
			b.SetBytes(int64(origBytes))
			for i := 0; i < b.N; i++ {
				_, err := mic.DecompressSingleFrame(micCompressed, cols, rows)
				if err != nil {
					b.Fatal(err)
				}
			}
		})

		b.Run(ti.name+"/JPEGLS", func(b *testing.B) {
			b.SetBytes(int64(origBytes))
			for i := 0; i < b.N; i++ {
				_, err := CharlsDecompressU16(jplsCompressed, cols, rows)
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// TestJPEGLSRoundtrip verifies JPEG-LS lossless roundtrip on all test images.
func TestJPEGLSRoundtrip(t *testing.T) {
	for _, ti := range testImages {
		t.Run(ti.name, func(t *testing.T) {
			_, shortData, maxShort, cols, rows := loadImage(ti)
			if shortData == nil {
				t.Skip("could not load image")
			}
			bitDepth := bits.Len16(maxShort)
			if bitDepth < 2 {
				bitDepth = 2
			}

			compressed, err := CharlsCompressU16(shortData, cols, rows, bitDepth)
			if err != nil {
				t.Fatalf("compress failed: %v", err)
			}

			decoded, err := CharlsDecompressU16(compressed, cols, rows)
			if err != nil {
				t.Fatalf("decompress failed: %v", err)
			}

			if len(decoded) != len(shortData) {
				t.Fatalf("length mismatch: want %d got %d", len(shortData), len(decoded))
			}

			for i := range shortData {
				if shortData[i] != decoded[i] {
					t.Fatalf("mismatch at pixel %d: want %d got %d", i, shortData[i], decoded[i])
				}
			}

			ratio := float64(len(shortData)*2) / float64(len(compressed))
			t.Logf("%-4s %dx%d: JPEG-LS ratio = %.2fx (%d -> %d bytes)",
				ti.name, cols, rows, ratio, len(shortData)*2, len(compressed))
		})
	}
}
