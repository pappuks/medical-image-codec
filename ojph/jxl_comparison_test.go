// Copyright 2021 Kuldeep Singh
// This source code is licensed under a MIT-style
// license that can be found in the LICENSE file.

//go:build cgo_ojph

// JPEG XL Comparison Framework (In-Process)
//
// Compares MIC variants (Delta+RLE+FSE 2-state and 4-state) against JPEG XL
// (lossless, modular mode) using libjxl as an in-process library via CGO. As
// with the JPEG-LS and HTJ2K frameworks, both codecs are invoked as library
// calls with no subprocess or file I/O overhead, giving a fair apples-to-apples
// comparison.
//
// Prereq: libjxl installed (brew install jpeg-xl; headers in
// /opt/homebrew/include/jxl, lib in /opt/homebrew/lib).
//
// Run with:
//
//	go test -tags cgo_ojph -v -run TestJXLComparison ./ojph/ -timeout 600s
//	go test -tags cgo_ojph -run=^$ -bench=BenchmarkJXLDecomp ./ojph/ -benchtime=10x
package ojph

import (
	"fmt"
	"math/bits"
	"testing"
	"time"

	mic "mic"
)

// TestJXLRoundtrip verifies JPEG XL lossless roundtrip on all test images.
func TestJXLRoundtrip(t *testing.T) {
	for _, ti := range testImages {
		t.Run(ti.name, func(t *testing.T) {
			_, shortData, maxShort, cols, rows := loadImage(ti)
			if shortData == nil {
				t.Skip("could not load image")
			}
			bitDepth := bits.Len16(maxShort)
			if bitDepth < 1 {
				bitDepth = 1
			}

			compressed, err := JXLCompressU16(shortData, cols, rows, bitDepth, JXLDefaultEffort)
			if err != nil {
				t.Fatalf("compress failed: %v", err)
			}

			decoded, err := JXLDecompressU16(compressed, cols, rows)
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
			t.Logf("%-4s %dx%d: JPEG XL ratio = %.2fx (%d -> %d bytes)",
				ti.name, cols, rows, ratio, len(shortData)*2, len(compressed))
		})
	}
}

// TestJXLComparison runs a full comparison of MIC (2-state and 4-state) vs
// JPEG XL (libjxl) across all test images, reporting compression ratio and
// decompression speed.
func TestJXLComparison(t *testing.T) {
	type result struct {
		name          string
		width, height int
		origBytes     int
		micRatio      float64
		mic4Ratio     float64
		jxlRatio      float64
		micDecompMs   float64
		mic4DecompMs  float64
		jxlDecompMs   float64
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
			if bitDepth < 1 {
				bitDepth = 1
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

			// --- JPEG XL compress ---
			jxlCompressed, err := JXLCompressU16(shortData, cols, rows, bitDepth, JXLDefaultEffort)
			if err != nil {
				t.Fatalf("JPEG XL compress failed: %v", err)
			}
			jxlRatio := float64(origBytes) / float64(len(jxlCompressed))

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

			// --- JPEG XL decompress (warmup + timed) ---
			for i := 0; i < 3; i++ {
				_, _ = JXLDecompressU16(jxlCompressed, cols, rows)
			}
			jxlStart := time.Now()
			for i := 0; i < iters; i++ {
				_, err = JXLDecompressU16(jxlCompressed, cols, rows)
				if err != nil {
					t.Fatalf("JPEG XL decompress failed: %v", err)
				}
			}
			jxlDecompMs := float64(time.Since(jxlStart).Microseconds()) / float64(iters) / 1000.0

			// --- Verify lossless ---
			jxlDecoded, err := JXLDecompressU16(jxlCompressed, cols, rows)
			if err != nil {
				t.Fatalf("JPEG XL decompress verification failed: %v", err)
			}
			for i := range shortData {
				if shortData[i] != jxlDecoded[i] {
					t.Fatalf("JPEG XL roundtrip mismatch at pixel %d: want %d got %d", i, shortData[i], jxlDecoded[i])
				}
			}

			micMBs := float64(origBytes) / micDecompMs / 1000.0
			mic4MBs := float64(origBytes) / mic4DecompMs / 1000.0
			jxlMBs := float64(origBytes) / jxlDecompMs / 1000.0

			t.Logf("%-4s %4dx%-4d  MIC: %.2fx  MIC-4state: %.2fx  JXL: %.2fx  MIC: %.1f MB/s  MIC-4state: %.1f MB/s  JXL: %.1f MB/s",
				ti.name, cols, rows, micRatio, mic4Ratio, jxlRatio, micMBs, mic4MBs, jxlMBs)

			results = append(results, result{
				name: ti.name, width: cols, height: rows, origBytes: origBytes,
				micRatio: micRatio, mic4Ratio: mic4Ratio, jxlRatio: jxlRatio,
				micDecompMs: micDecompMs, mic4DecompMs: mic4DecompMs, jxlDecompMs: jxlDecompMs,
			})
		})
	}

	// Summary table
	fmt.Println("\n=== MIC vs MIC-4state vs JPEG XL (libjxl, effort 7) Comparison ===")
	fmt.Printf("%-6s %11s %13s %10s %14s %16s %13s %9s %9s\n",
		"Image", "MIC ratio", "MIC-4s ratio", "JXL ratio", "MIC MB/s", "MIC-4state MB/s", "JXL MB/s", "MIC/JXL", "4s/JXL")
	fmt.Println("------  -----------  -------------  ----------  --------------  ---------------  -------------  ---------  ---------")
	var sumMic, sumMic4, sumJxl float64
	for _, r := range results {
		micMBs := float64(r.origBytes) / r.micDecompMs / 1000.0
		mic4MBs := float64(r.origBytes) / r.mic4DecompMs / 1000.0
		jxlMBs := float64(r.origBytes) / r.jxlDecompMs / 1000.0
		micSpeedup := fmt.Sprintf("%.2fx", micMBs/jxlMBs)
		mic4Speedup := fmt.Sprintf("%.2fx", mic4MBs/jxlMBs)
		fmt.Printf("%-6s %10.2fx %12.2fx %9.2fx %13.0f %15.0f %12.0f %10s %10s\n",
			r.name, r.micRatio, r.mic4Ratio, r.jxlRatio, micMBs, mic4MBs, jxlMBs, micSpeedup, mic4Speedup)
		sumMic += r.micRatio
		sumMic4 += r.mic4Ratio
		sumJxl += r.jxlRatio
	}
	if n := float64(len(results)); n > 0 {
		fmt.Println("------  -----------  -------------  ----------")
		fmt.Printf("%-6s %10.2fx %12.2fx %9.2fx  (mean compression ratio, n=%d)\n",
			"MEAN", sumMic/n, sumMic4/n, sumJxl/n, len(results))
	}
}

// BenchmarkJXLDecomp benchmarks JPEG XL decompression via libjxl (in-process)
// alongside MIC 2-state and 4-state variants.
func BenchmarkJXLDecomp(b *testing.B) {
	for _, ti := range testImages {
		_, shortData, maxShort, cols, rows := loadImage(ti)
		if shortData == nil {
			continue
		}
		origBytes := len(shortData) * 2
		bitDepth := bits.Len16(maxShort)
		if bitDepth < 1 {
			bitDepth = 1
		}

		// Pre-compress with all codecs.
		micCompressed, err := mic.CompressSingleFrame(shortData, cols, rows, maxShort)
		if err != nil {
			b.Fatalf("MIC compress failed: %v", err)
		}

		var drc mic.DeltaRleCompressU16
		deltaComp, _ := drc.Compress(shortData, cols, rows, maxShort)
		var s4 mic.ScratchU16
		mic4Compressed, _ := mic.FSECompressU16FourState(deltaComp, &s4)

		jxlCompressed, err := JXLCompressU16(shortData, cols, rows, bitDepth, JXLDefaultEffort)
		if err != nil {
			b.Fatalf("JPEG XL compress failed: %v", err)
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

		b.Run(ti.name+"/JXL", func(b *testing.B) {
			b.SetBytes(int64(origBytes))
			for i := 0; i < b.N; i++ {
				_, err := JXLDecompressU16(jxlCompressed, cols, rows)
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkJXLEncode benchmarks JPEG XL lossless encoding via libjxl.
func BenchmarkJXLEncode(b *testing.B) {
	for _, ti := range testImages {
		_, shortData, maxShort, cols, rows := loadImage(ti)
		if shortData == nil {
			continue
		}
		origBytes := len(shortData) * 2
		bitDepth := bits.Len16(maxShort)
		if bitDepth < 1 {
			bitDepth = 1
		}

		b.Run(ti.name+"/JXL", func(b *testing.B) {
			b.SetBytes(int64(origBytes))
			for i := 0; i < b.N; i++ {
				_, err := JXLCompressU16(shortData, cols, rows, bitDepth, JXLDefaultEffort)
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
