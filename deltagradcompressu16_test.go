// Copyright 2026 Kuldeep Singh
// This source code is licensed under a MIT-style
// license that can be found in the LICENSE file.
package mic

import (
	"fmt"
	"testing"
)

// TestGradDeltaCompress verifies the standalone gradient-adaptive delta roundtrip.
func TestGradDeltaCompress(t *testing.T) {
	in := []uint16{
		100, 110, 120,
		105, 115, 125,
		110, 120, 130,
	}
	out, err := GradDeltaCompressU16(in, 3, 3, 8000)
	if err != nil {
		t.Fatalf("GradDeltaCompressU16: %v", err)
	}
	inAgain := GradDeltaDecompressU16(out, 3, 3)
	for i := range in {
		if in[i] != inAgain[i] {
			t.Errorf("mismatch at %d: want %d got %d", i, in[i], inAgain[i])
		}
	}
}

// TestGradDeltaRleFSECompress verifies the full gradient-adaptive Delta+RLE+FSE pipeline
// on all test images.
func TestGradDeltaRleFSECompress(t *testing.T) {
	for _, tf := range testFiles {
		t.Run(tf.name, func(t *testing.T) {
			_, shortData, maxShort, cols, rows := SetupTests(tf)
			if shortData == nil {
				t.Skip("test data not available")
			}

			compressed, err := CompressSingleFrameGrad(shortData, cols, rows, maxShort)
			if err != nil {
				t.Fatalf("CompressSingleFrameGrad: %v", err)
			}

			decompressed, err := DecompressSingleFrameGrad(compressed, cols, rows)
			if err != nil {
				t.Fatalf("DecompressSingleFrameGrad: %v", err)
			}

			if len(decompressed) != len(shortData) {
				t.Fatalf("length mismatch: want %d got %d", len(shortData), len(decompressed))
			}

			for i := range shortData {
				if shortData[i] != decompressed[i] {
					t.Fatalf("mismatch at pixel %d: want %d got %d", i, shortData[i], decompressed[i])
				}
			}

			ratio := float64(len(shortData)*2) / float64(len(compressed))
			t.Logf("%s: %d bytes -> %d bytes (%.2f:1)", tf.name, len(shortData)*2, len(compressed), ratio)
		})
	}
}

// TestGradVsAvgCompression compares gradient-adaptive vs standard avg predictor ratios.
func TestGradVsAvgCompression(t *testing.T) {
	for _, tf := range testFiles {
		t.Run(tf.name, func(t *testing.T) {
			byteData, shortData, maxShort, cols, rows := SetupTests(tf)
			if shortData == nil {
				t.Skip("test data not available")
			}

			avgComp, err := CompressSingleFrame(shortData, cols, rows, maxShort)
			if err != nil {
				t.Fatalf("CompressSingleFrame: %v", err)
			}

			gradComp, err := CompressSingleFrameGrad(shortData, cols, rows, maxShort)
			if err != nil {
				t.Fatalf("CompressSingleFrameGrad: %v", err)
			}

			rawSize := len(byteData)
			avgRatio := float64(rawSize) / float64(len(avgComp))
			gradRatio := float64(rawSize) / float64(len(gradComp))
			improvement := (gradRatio - avgRatio) / avgRatio * 100

			fmt.Printf("%-4s  avg=%.3f×  grad=%.3f×  change=%+.1f%%\n",
				tf.name, avgRatio, gradRatio, improvement)
		})
	}
}

// BenchmarkGradDeltaRLEFSECompress benchmarks the gradient-adaptive pipeline.
func BenchmarkGradDeltaRLEFSECompress(b *testing.B) {
	for _, tf := range testFiles {
		b.Run(tf.name, func(b *testing.B) {
			byteData, shortData, maxShort, cols, rows := SetupTests(tf)
			if shortData == nil {
				b.Skip("test data not available")
			}

			compressed, err := CompressSingleFrameGrad(shortData, cols, rows, maxShort)
			if err != nil {
				b.Fatalf("CompressSingleFrameGrad: %v", err)
			}

			b.SetBytes(int64(len(byteData)))
			b.ResetTimer()
			b.ReportMetric(float64(len(byteData))/float64(len(compressed)), "ratio")
			for i := 0; i < b.N; i++ {
				DecompressSingleFrameGrad(compressed, cols, rows)
			}
		})
	}
}
