// Copyright 2021 Kuldeep Singh
// This source code is licensed under a MIT-style
// license that can be found in the LICENSE file.

//go:build cgo_ojph

// JPEG-LS Comparison Framework (In-Process)
//
// Compares MIC variants (Delta+RLE+FSE 2-state and 4-state) against JPEG-LS
// (lossless) using CharLS as an in-process library via CGO. This provides a
// fair apples-to-apples comparison — both codecs are invoked as library calls
// with no subprocess or file I/O overhead.
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

// TestJPEGLSComparison runs a full comparison of MIC (2-state and 4-state) vs
// JPEG-LS (CharLS) across all test images, reporting compression ratio and
// decompression speed.
func TestJPEGLSComparison(t *testing.T) {
	type result struct {
		name          string
		width, height int
		origBytes     int
		micRatio      float64
		mic4Ratio     float64
		jplsRatio     float64
		micDecompMs   float64
		mic4DecompMs  float64
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

			// --- MIC 2-state compress ---
			micCompressed, err := mic.CompressSingleFrame(shortData, cols, rows, maxShort)
			if err != nil {
				t.Fatalf("MIC compress failed: %v", err)
			}
			micRatio := float64(origBytes) / float64(len(micCompressed))

			// --- MIC 4-state compress ---
			var drc mic.DeltaRleCompressU16
			deltaComp, err := drc.Compress(shortData, cols, rows, maxShort)
			if err != nil {
				t.Fatalf("Delta+RLE compress failed: %v", err)
			}
			var s4 mic.ScratchU16
			mic4Compressed, err := mic.FSECompressU16FourState(deltaComp, &s4)
			if err != nil {
				t.Fatalf("MIC-4state compress failed: %v", err)
			}
			mic4Ratio := float64(origBytes) / float64(len(mic4Compressed))

			// --- JPEG-LS compress ---
			jplsCompressed, err := CharlsCompressU16(shortData, cols, rows, bitDepth)
			if err != nil {
				t.Fatalf("JPEG-LS compress failed: %v", err)
			}
			jplsRatio := float64(origBytes) / float64(len(jplsCompressed))

			const iters = 10

			// --- MIC 2-state decompress (warmup + timed) ---
			for i := 0; i < 3; i++ {
				_, _ = mic.DecompressSingleFrame(micCompressed, cols, rows)
			}
			micStart := time.Now()
			for i := 0; i < iters; i++ {
				_, err = mic.DecompressSingleFrame(micCompressed, cols, rows)
				if err != nil {
					t.Fatalf("MIC decompress failed: %v", err)
				}
			}
			micDecompMs := float64(time.Since(micStart).Microseconds()) / float64(iters) / 1000.0

			// --- MIC 4-state decompress (warmup + timed) ---
			for i := 0; i < 3; i++ {
				var sd mic.ScratchU16
				rleData, _ := mic.FSEDecompressU16FourState(mic4Compressed, &sd)
				var drd mic.DeltaRleDecompressU16
				drd.Decompress(rleData, cols, rows)
			}
			mic4Start := time.Now()
			for i := 0; i < iters; i++ {
				var sd mic.ScratchU16
				rleData, err := mic.FSEDecompressU16FourState(mic4Compressed, &sd)
				if err != nil {
					t.Fatalf("MIC-4state decompress failed: %v", err)
				}
				var drd mic.DeltaRleDecompressU16
				drd.Decompress(rleData, cols, rows)
			}
			mic4DecompMs := float64(time.Since(mic4Start).Microseconds()) / float64(iters) / 1000.0

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
			mic4MBs := float64(origBytes) / mic4DecompMs / 1000.0
			jplsMBs := float64(origBytes) / jplsDecompMs / 1000.0

			t.Logf("%-4s %4dx%-4d  MIC: %.2fx  MIC-4state: %.2fx  JPEG-LS: %.2fx  MIC: %.1f MB/s  MIC-4state: %.1f MB/s  JPEG-LS: %.1f MB/s",
				ti.name, cols, rows, micRatio, mic4Ratio, jplsRatio, micMBs, mic4MBs, jplsMBs)

			results = append(results, result{
				name: ti.name, width: cols, height: rows, origBytes: origBytes,
				micRatio: micRatio, mic4Ratio: mic4Ratio, jplsRatio: jplsRatio,
				micDecompMs: micDecompMs, mic4DecompMs: mic4DecompMs, jplsDecompMs: jplsDecompMs,
			})
		})
	}

	// Summary table
	fmt.Println("\n=== MIC vs MIC-4state vs JPEG-LS (CharLS) Comparison ===")
	fmt.Printf("%-6s %11s %13s %11s %14s %16s %14s %9s %9s\n",
		"Image", "MIC ratio", "MIC-4s ratio", "JPLS ratio", "MIC MB/s", "MIC-4state MB/s", "JPLS MB/s", "MIC/JPLS", "4s/JPLS")
	fmt.Println("------  -----------  -------------  -----------  --------------  ---------------  --------------  ---------  ---------")
	for _, r := range results {
		micMBs := float64(r.origBytes) / r.micDecompMs / 1000.0
		mic4MBs := float64(r.origBytes) / r.mic4DecompMs / 1000.0
		jplsMBs := float64(r.origBytes) / r.jplsDecompMs / 1000.0
		micSpeedup := fmt.Sprintf("%.2fx", micMBs/jplsMBs)
		mic4Speedup := fmt.Sprintf("%.2fx", mic4MBs/jplsMBs)
		fmt.Printf("%-6s %10.2fx %12.2fx %10.2fx %13.0f %15.0f %13.0f %10s %10s\n",
			r.name, r.micRatio, r.mic4Ratio, r.jplsRatio, micMBs, mic4MBs, jplsMBs, micSpeedup, mic4Speedup)
	}
}

// BenchmarkJPEGLSDecomp benchmarks JPEG-LS decompression via CharLS (in-process)
// alongside MIC 2-state and 4-state variants.
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

		// Pre-compress with all codecs
		micCompressed, err := mic.CompressSingleFrame(shortData, cols, rows, maxShort)
		if err != nil {
			b.Fatalf("MIC compress failed: %v", err)
		}

		var drc mic.DeltaRleCompressU16
		deltaComp, _ := drc.Compress(shortData, cols, rows, maxShort)
		var s4 mic.ScratchU16
		mic4Compressed, _ := mic.FSECompressU16FourState(deltaComp, &s4)

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

		b.Run(ti.name+"/MIC-4state", func(b *testing.B) {
			b.SetBytes(int64(origBytes))
			for i := 0; i < b.N; i++ {
				var sd mic.ScratchU16
				rleData, _ := mic.FSEDecompressU16FourState(mic4Compressed, &sd)
				var drd mic.DeltaRleDecompressU16
				drd.Decompress(rleData, cols, rows)
			}
		})

		b.Run(ti.name+"/MIC-4state-C", func(b *testing.B) {
			b.SetBytes(int64(origBytes))
			for i := 0; i < b.N; i++ {
				MICDecompressFourStateC(mic4Compressed, cols, rows)
			}
		})

		b.Run(ti.name+"/MIC-4state-SIMD", func(b *testing.B) {
			b.SetBytes(int64(origBytes))
			for i := 0; i < b.N; i++ {
				MICDecompressFourStateSIMD(mic4Compressed, cols, rows)
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
