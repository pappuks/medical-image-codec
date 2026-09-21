// Copyright 2024 Kuldeep Singh
// This source code is licensed under a MIT-style
// license that can be found in the LICENSE file.

package mic

import (
	"sync"
	"testing"
)

// TestRANS8StateRoundtrip verifies that RANSCompressU16EightState +
// RANSDecompressU16EightState is a lossless round-trip on all standard test images.
func TestRANS8StateRoundtrip(t *testing.T) {
	for _, tf := range testFiles {
		t.Run(tf.name, func(t *testing.T) {
			_, shortData, _, _, _ := SetupTests(tf)

			var sc ScratchU16
			compressed, err := RANSCompressU16EightState(shortData, &sc)
			if err != nil {
				t.Fatalf("compress: %v", err)
			}

			var sd ScratchU16
			got, err := RANSDecompressU16EightState(compressed, &sd)
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

// TestRANS8StateAutoDetect verifies that FSEDecompressU16Auto correctly routes
// eight-state rANS streams to the rANS decoder.
func TestRANS8StateAutoDetect(t *testing.T) {
	for _, tf := range testFiles {
		t.Run(tf.name, func(t *testing.T) {
			_, shortData, _, _, _ := SetupTests(tf)

			var sc ScratchU16
			compressed, err := RANSCompressU16EightState(shortData, &sc)
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

// TestRANS8StateMagicBytes checks that the output stream carries the expected
// [0xFF, 0x08] magic prefix and that tampering with it is rejected.
func TestRANS8StateMagicBytes(t *testing.T) {
	_, shortData, _, _, _ := SetupTests(testFiles[1]) // CT — large enough to compress

	var sc ScratchU16
	compressed, err := RANSCompressU16EightState(shortData, &sc)
	if err != nil {
		t.Fatalf("compress: %v", err)
	}

	if len(compressed) < 2 {
		t.Fatal("compressed output too short")
	}
	if compressed[0] != eightStateMagic0 || compressed[1] != eightStateMagic1 {
		t.Errorf("wrong magic: got [%#x, %#x] want [%#x, %#x]",
			compressed[0], compressed[1], eightStateMagic0, eightStateMagic1)
	}

	bad := make([]byte, len(compressed))
	copy(bad, compressed)
	bad[0] = 0x00
	var sd ScratchU16
	if _, err := RANSDecompressU16EightState(bad, &sd); err == nil {
		t.Error("expected error for corrupted magic, got nil")
	}
}

// TestRANS8StateEdgeCases covers small and degenerate inputs.
func TestRANS8StateEdgeCases(t *testing.T) {
	t.Run("single_value_returns_ErrUseRLE", func(t *testing.T) {
		data := make([]uint16, 1024)
		for i := range data {
			data[i] = 42
		}
		var s ScratchU16
		_, err := RANSCompressU16EightState(data, &s)
		if err != ErrUseRLE {
			t.Errorf("expected ErrUseRLE, got %v", err)
		}
	})

	t.Run("lengths_not_divisible_by_8", func(t *testing.T) {
		for _, n := range []int{101, 102, 103, 104, 105, 106, 107, 1001, 1002, 1003, 1004, 1005, 1006, 1007, 9, 10, 11, 12, 13, 14, 15} {
			data := make([]uint16, n)
			for i := range data {
				// Use % 8 modulus so we have at least 8 distinct symbols,
				// ensuring good compressibility.
				data[i] = uint16(i % 8)
			}
			var sc ScratchU16
			compressed, err := RANSCompressU16EightState(data, &sc)
			if err != nil {
				t.Fatalf("n=%d compress: %v", n, err)
			}
			var sd ScratchU16
			got, err := RANSDecompressU16EightState(compressed, &sd)
			if err != nil {
				t.Fatalf("n=%d decompress: %v", n, err)
			}
			if len(got) != n {
				t.Fatalf("n=%d length mismatch: got %d", n, len(got))
			}
			for i, v := range data {
				if got[i] != v {
					t.Errorf("n=%d mismatch at [%d]: got %d want %d", n, i, got[i], v)
					break
				}
			}
		}
	})
}

// BenchmarkRANSDecompress8State compares 1-state, 2-state, 4-state FSE and
// 8-state rANS decompression speeds across standard test images.
func BenchmarkRANSDecompress8State(b *testing.B) {
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
		var s4 ScratchU16
		comp4, err := FSECompressU16FourState(rleData, &s4)
		if err != nil {
			b.Skipf("%s: FSE4 compress: %v", tf.name, err)
		}
		var s8 ScratchU16
		comp8, err := RANSCompressU16EightState(rleData, &s8)
		if err != nil {
			b.Skipf("%s: RANS8 compress: %v", tf.name, err)
		}

		ratio1 := float64(len(rleData)*2) / float64(len(comp1))
		ratio2 := float64(len(rleData)*2) / float64(len(comp2))
		ratio4 := float64(len(rleData)*2) / float64(len(comp4))
		ratio8 := float64(len(rleData)*2) / float64(len(comp8))

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

		b.Run(tf.name+"/4state", func(b *testing.B) {
			b.SetBytes(uncompressedBytes)
			b.ReportMetric(ratio4, "ratio")
			b.ResetTimer()
			var wg sync.WaitGroup
			for i := 0; i < b.N; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					var sd ScratchU16
					if _, err := FSEDecompressU16FourState(comp4, &sd); err != nil {
						b.Error(err)
					}
				}()
			}
			wg.Wait()
		})

		b.Run(tf.name+"/8state-rans", func(b *testing.B) {
			b.SetBytes(uncompressedBytes)
			b.ReportMetric(ratio8, "ratio")
			b.ResetTimer()
			var wg sync.WaitGroup
			for i := 0; i < b.N; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					var sd ScratchU16
					if _, err := RANSDecompressU16EightState(comp8, &sd); err != nil {
						b.Error(err)
					}
				}()
			}
			wg.Wait()
		})
	}
}
