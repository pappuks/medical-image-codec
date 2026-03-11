// Copyright 2024 Kuldeep Singh
// This source code is licensed under a MIT-style
// license that can be found in the LICENSE file.

package mic

import (
	"fmt"
	"sync"
	"testing"
)

// ---------------------------------------------------------------------------
// Correctness tests
// ---------------------------------------------------------------------------

// TestFSE2StateRoundtrip verifies that FSECompressU16TwoState + FSEDecompressU16TwoState
// is a lossless round-trip on all standard test images.
func TestFSE2StateRoundtrip(t *testing.T) {
	for _, tf := range testFiles {
		t.Run(tf.name, func(t *testing.T) {
			_, shortData, _, _, _ := SetupTests(tf)

			var sc ScratchU16
			compressed, err := FSECompressU16TwoState(shortData, &sc)
			if err != nil {
				t.Fatalf("compress: %v", err)
			}

			var sd ScratchU16
			got, err := FSEDecompressU16TwoState(compressed, &sd)
			if err != nil {
				t.Fatalf("decompress: %v", err)
			}

			if len(got) != len(shortData) {
				t.Fatalf("length mismatch: got %d want %d", len(got), len(shortData))
			}
			for i, v := range shortData {
				if got[i] != v {
					t.Errorf("mismatch at [%d]: got %d want %d", i, got[i], v)
					break
				}
			}
		})
	}
}

// TestFSE2StateAutoDetect verifies that FSEDecompressU16Auto correctly routes
// two-state streams to the two-state decoder.
func TestFSE2StateAutoDetect(t *testing.T) {
	for _, tf := range testFiles {
		t.Run(tf.name, func(t *testing.T) {
			_, shortData, _, _, _ := SetupTests(tf)

			var sc ScratchU16
			compressed, err := FSECompressU16TwoState(shortData, &sc)
			if err != nil {
				t.Fatalf("compress: %v", err)
			}

			// FSEDecompressU16Auto must detect the [0xFF,0x02] magic and route
			// to the two-state decoder without the caller specifying which to use.
			var sd ScratchU16
			got, err := FSEDecompressU16Auto(compressed, &sd)
			if err != nil {
				t.Fatalf("auto-decompress: %v", err)
			}

			if len(got) != len(shortData) {
				t.Fatalf("length mismatch: got %d want %d", len(got), len(shortData))
			}
			for i, v := range shortData {
				if got[i] != v {
					t.Errorf("mismatch at [%d]: got %d want %d", i, got[i], v)
					break
				}
			}
		})
	}
}

// TestFSE1StateAutoDetect verifies backward compatibility: single-state streams
// produced by FSECompressU16 are transparently decoded by FSEDecompressU16Auto.
func TestFSE1StateAutoDetect(t *testing.T) {
	for _, tf := range testFiles {
		t.Run(tf.name, func(t *testing.T) {
			_, shortData, _, _, _ := SetupTests(tf)

			var sc ScratchU16
			compressed, err := FSECompressU16(shortData, &sc)
			if err != nil {
				t.Fatalf("compress: %v", err)
			}

			var sd ScratchU16
			got, err := FSEDecompressU16Auto(compressed, &sd)
			if err != nil {
				t.Fatalf("auto-decompress: %v", err)
			}

			if len(got) != len(shortData) {
				t.Fatalf("length mismatch: got %d want %d", len(got), len(shortData))
			}
			for i, v := range shortData {
				if got[i] != v {
					t.Errorf("mismatch at [%d]: got %d want %d", i, got[i], v)
					break
				}
			}
		})
	}
}

// TestFSE2StateMagicBytes checks that the output stream carries the expected
// [0xFF, 0x02] magic prefix and that tampering with it is rejected.
func TestFSE2StateMagicBytes(t *testing.T) {
	_, shortData, _, _, _ := SetupTests(testFiles[1]) // CT — large enough to compress

	var sc ScratchU16
	compressed, err := FSECompressU16TwoState(shortData, &sc)
	if err != nil {
		t.Fatalf("compress: %v", err)
	}

	if len(compressed) < 2 {
		t.Fatal("compressed output too short")
	}
	if compressed[0] != twoStateMagic0 || compressed[1] != twoStateMagic1 {
		t.Errorf("wrong magic: got [%#x, %#x] want [%#x, %#x]",
			compressed[0], compressed[1], twoStateMagic0, twoStateMagic1)
	}

	// Corrupting the magic must cause an error.
	bad := make([]byte, len(compressed))
	copy(bad, compressed)
	bad[0] = 0x00
	var sd ScratchU16
	if _, err := FSEDecompressU16TwoState(bad, &sd); err == nil {
		t.Error("expected error for corrupted magic, got nil")
	}
}

// TestFSE2StateDeltaRLERoundtrip tests the full Delta+RLE+FSE2State pipeline
// as used by CompressSingleFrame / DecompressSingleFrame, verifying lossless
// reconstruction on every test image.
func TestFSE2StateDeltaRLERoundtrip(t *testing.T) {
	for _, tf := range testFiles {
		t.Run(tf.name, func(t *testing.T) {
			_, shortData, maxShort, cols, rows := SetupTests(tf)

			// Compress with the full pipeline (uses two-state FSE internally).
			compressed, err := CompressSingleFrame(shortData, cols, rows, maxShort)
			if err != nil {
				t.Fatalf("CompressSingleFrame: %v", err)
			}

			// Decompress (auto-detects two-state vs single-state).
			got, err := DecompressSingleFrame(compressed, cols, rows)
			if err != nil {
				t.Fatalf("DecompressSingleFrame: %v", err)
			}

			if len(got) != len(shortData) {
				t.Fatalf("length mismatch: got %d want %d", len(got), len(shortData))
			}
			for i, v := range shortData {
				if got[i] != v {
					t.Errorf("mismatch at [%d]: got %d want %d", i, got[i], v)
					break
				}
			}
		})
	}
}

// TestFSE2StateEdgeCases covers small and degenerate inputs.
func TestFSE2StateEdgeCases(t *testing.T) {
	t.Run("single_value_returns_ErrUseRLE", func(t *testing.T) {
		data := make([]uint16, 1024)
		for i := range data {
			data[i] = 42
		}
		var s ScratchU16
		_, err := FSECompressU16TwoState(data, &s)
		if err != ErrUseRLE {
			t.Errorf("expected ErrUseRLE, got %v", err)
		}
	})

	t.Run("two_element_input_returns_error", func(t *testing.T) {
		data := []uint16{1, 2}
		var s ScratchU16
		_, err := FSECompressU16TwoState(data, &s)
		if err == nil {
			t.Error("expected error for 2-element input, got nil")
		}
	})

	t.Run("odd_length_roundtrip", func(t *testing.T) {
		// Build data with 2+ distinct values and an odd length.
		data := make([]uint16, 999)
		for i := range data {
			data[i] = uint16(i % 17)
		}
		var sc ScratchU16
		compressed, err := FSECompressU16TwoState(data, &sc)
		if err != nil {
			t.Fatalf("compress: %v", err)
		}
		var sd ScratchU16
		got, err := FSEDecompressU16TwoState(compressed, &sd)
		if err != nil {
			t.Fatalf("decompress: %v", err)
		}
		if len(got) != len(data) {
			t.Fatalf("length mismatch: got %d want %d", len(got), len(data))
		}
		for i, v := range data {
			if got[i] != v {
				t.Errorf("mismatch at [%d]: got %d want %d", i, got[i], v)
				break
			}
		}
	})

	t.Run("not_divisible_by_4_roundtrip", func(t *testing.T) {
		// Use a small symbol alphabet (8 values) so data compresses regardless
		// of length. The key property being tested is alignment handling when
		// len(data) % 4 != 0.
		for _, n := range []int{101, 102, 103, 1001, 1002, 1003} {
			data := make([]uint16, n)
			for i := range data {
				data[i] = uint16(i % 8)
			}
			var sc ScratchU16
			compressed, err := FSECompressU16TwoState(data, &sc)
			if err != nil {
				t.Fatalf("n=%d compress: %v", n, err)
			}
			var sd ScratchU16
			got, err := FSEDecompressU16TwoState(compressed, &sd)
			if err != nil {
				t.Fatalf("n=%d decompress: %v", n, err)
			}
			if len(got) != n {
				t.Fatalf("n=%d length mismatch: got %d", n, len(got))
			}
			for i, v := range data {
				if got[i] != v {
					t.Errorf("n=%d mismatch at [%d]", n, i)
					break
				}
			}
		}
	})
}

// ---------------------------------------------------------------------------
// Benchmarks: single-state vs two-state FSE, isolated decompression only
// ---------------------------------------------------------------------------

// BenchmarkFSEDecompress runs back-to-back single-state and two-state FSE
// decompression on each test image so the numbers appear in the same output
// and are trivially comparable.
//
// Input data: Delta+RLE residuals (the stream that FSE actually decodes).
// Throughput is reported in MB/s over the *uncompressed* RLE symbol stream.
func BenchmarkFSEDecompress(b *testing.B) {
	for _, tf := range testFiles {
		_, shortData, maxShort, cols, rows := SetupTests(tf)
		var drc DeltaRleCompressU16
		rleData, err := drc.Compress(shortData, cols, rows, maxShort)
		if err != nil {
			b.Skipf("%s: delta+RLE compress: %v", tf.name, err)
		}
		uncompressedBytes := int64(len(rleData) * 2)

		var s1 ScratchU16
		comp1, err := FSECompressU16(rleData, &s1)
		if err != nil {
			b.Skipf("%s: FSE1 compress: %v", tf.name, err)
		}

		var s2 ScratchU16
		comp2, err := FSECompressU16TwoState(rleData, &s2)
		if err != nil {
			b.Skipf("%s: FSE2 compress: %v", tf.name, err)
		}

		ratio1 := float64(len(rleData)*2) / float64(len(comp1))
		ratio2 := float64(len(rleData)*2) / float64(len(comp2))

		b.Run(tf.name+"/1state", func(b *testing.B) {
			b.SetBytes(uncompressedBytes)
			b.ReportMetric(ratio1, "ratio")
			b.ResetTimer()
			var wg sync.WaitGroup
			for i := 0; i < b.N; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					var sd ScratchU16
					if _, err := FSEDecompressU16(comp1, &sd); err != nil {
						b.Error(err)
					}
				}()
			}
			wg.Wait()
		})

		b.Run(tf.name+"/2state", func(b *testing.B) {
			b.SetBytes(uncompressedBytes)
			b.ReportMetric(ratio2, "ratio")
			b.ResetTimer()
			var wg sync.WaitGroup
			for i := 0; i < b.N; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					var sd ScratchU16
					if _, err := FSEDecompressU16TwoState(comp2, &sd); err != nil {
						b.Error(err)
					}
				}()
			}
			wg.Wait()
		})
	}
}

// BenchmarkFSECompressCompare compares compression throughput and ratio between
// the single-state and two-state encoders on Delta+RLE residual streams.
func BenchmarkFSECompressCompare(b *testing.B) {
	for _, tf := range testFiles {
		_, shortData, maxShort, cols, rows := SetupTests(tf)
		var drc DeltaRleCompressU16
		rleData, err := drc.Compress(shortData, cols, rows, maxShort)
		if err != nil {
			b.Skipf("%s: delta+RLE compress: %v", tf.name, err)
		}
		uncompressedBytes := int64(len(rleData) * 2)

		b.Run(tf.name+"/1state", func(b *testing.B) {
			b.SetBytes(uncompressedBytes)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				var sc ScratchU16
				if _, err := FSECompressU16(rleData, &sc); err != nil {
					b.Fatal(err)
				}
			}
		})

		b.Run(tf.name+"/2state", func(b *testing.B) {
			b.SetBytes(uncompressedBytes)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				var sc ScratchU16
				if _, err := FSECompressU16TwoState(rleData, &sc); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkDeltaRLEFSEDecompress benchmarks the full decompression pipeline
// (FSE → RLE → Delta) for both FSE variants.  This reflects end-to-end
// frame decompression latency as seen by callers of DecompressSingleFrame.
func BenchmarkDeltaRLEFSEDecompress(b *testing.B) {
	for _, tf := range testFiles {
		byteData, shortData, maxShort, cols, rows := SetupTests(tf)
		pixelBytes := int64(len(byteData))

		// Build both compressed representations once.
		var drc DeltaRleCompressU16
		rleData, _ := drc.Compress(shortData, cols, rows, maxShort)

		var s1 ScratchU16
		comp1, err := FSECompressU16(rleData, &s1)
		if err != nil {
			b.Skipf("%s: FSE1: %v", tf.name, err)
		}

		var s2 ScratchU16
		comp2, err := FSECompressU16TwoState(rleData, &s2)
		if err != nil {
			b.Skipf("%s: FSE2: %v", tf.name, err)
		}

		ratio1 := float64(len(byteData)) / float64(len(comp1))
		ratio2 := float64(len(byteData)) / float64(len(comp2))

		b.Run(tf.name+"/1state", func(b *testing.B) {
			b.SetBytes(pixelBytes)
			b.ReportMetric(ratio1, "ratio")
			b.ResetTimer()
			var wg sync.WaitGroup
			for i := 0; i < b.N; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					var sd ScratchU16
					rle, _ := FSEDecompressU16(comp1, &sd)
					var drd DeltaRleDecompressU16
					drd.Decompress(rle, cols, rows)
				}()
			}
			wg.Wait()
		})

		b.Run(tf.name+"/2state", func(b *testing.B) {
			b.SetBytes(pixelBytes)
			b.ReportMetric(ratio2, "ratio")
			b.ResetTimer()
			var wg sync.WaitGroup
			for i := 0; i < b.N; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					var sd ScratchU16
					rle, _ := FSEDecompressU16TwoState(comp2, &sd)
					var drd DeltaRleDecompressU16
					drd.Decompress(rle, cols, rows)
				}()
			}
			wg.Wait()
		})
	}
}

// BenchmarkFSE2StateSummary prints a human-readable comparison table when run
// with -v. It measures decompression throughput across all test images and
// prints the speedup of two-state over single-state for each.
func BenchmarkFSE2StateSummary(b *testing.B) {
	type result struct {
		name        string
		mbps1, mbps2 float64
		ratio1, ratio2 float64
	}
	var results []result

	for _, tf := range testFiles {
		_, shortData, maxShort, cols, rows := SetupTests(tf)

		var drc DeltaRleCompressU16
		rleData, _ := drc.Compress(shortData, cols, rows, maxShort)
		uncompressedMB := float64(len(rleData)*2) / (1 << 20)

		var s1 ScratchU16
		comp1, err := FSECompressU16(rleData, &s1)
		if err != nil {
			continue
		}
		var s2 ScratchU16
		comp2, err := FSECompressU16TwoState(rleData, &s2)
		if err != nil {
			continue
		}

		var dur1, dur2 float64

		b.Run(tf.name+"/1state", func(b *testing.B) {
			b.ResetTimer()
			var wg sync.WaitGroup
			for i := 0; i < b.N; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					var sd ScratchU16
					FSEDecompressU16(comp1, &sd)
				}()
			}
			wg.Wait()
			dur1 = b.Elapsed().Seconds() / float64(b.N)
		})
		b.Run(tf.name+"/2state", func(b *testing.B) {
			b.ResetTimer()
			var wg sync.WaitGroup
			for i := 0; i < b.N; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					var sd ScratchU16
					FSEDecompressU16TwoState(comp2, &sd)
				}()
			}
			wg.Wait()
			dur2 = b.Elapsed().Seconds() / float64(b.N)
		})

		if dur1 > 0 && dur2 > 0 {
			results = append(results, result{
				name:   tf.name,
				mbps1:  uncompressedMB / dur1,
				mbps2:  uncompressedMB / dur2,
				ratio1: float64(len(rleData)*2) / float64(len(comp1)),
				ratio2: float64(len(rleData)*2) / float64(len(comp2)),
			})
		}
	}

	if len(results) == 0 {
		return
	}

	fmt.Printf("\n%-6s  %8s  %8s  %8s  %7s  %7s\n",
		"Image", "1state", "2state", "speedup", "ratio1", "ratio2")
	fmt.Printf("%-6s  %8s  %8s  %8s  %7s  %7s\n",
		"------", "MB/s", "MB/s", "", "x", "x")
	for _, r := range results {
		speedup := r.mbps2 / r.mbps1
		indicator := ""
		if speedup >= 1.1 {
			indicator = " ↑"
		} else if speedup <= 0.9 {
			indicator = " ↓"
		}
		fmt.Printf("%-6s  %8.1f  %8.1f  %7.2fx%s  %7.2f  %7.2f\n",
			r.name, r.mbps1, r.mbps2, speedup, indicator, r.ratio1, r.ratio2)
	}
}
