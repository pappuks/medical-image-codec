// Copyright 2026 Kuldeep Singh
// This source code is licensed under a MIT-style
// license that can be found in the LICENSE file.

package mic

import (
	"sync"
	"testing"
)

// TestFSE8StateRoundtrip verifies lossless round-trip on all standard test
// images.
func TestFSE8StateRoundtrip(t *testing.T) {
	for _, tf := range testFiles {
		t.Run(tf.name, func(t *testing.T) {
			_, shortData, _, _, _ := SetupTests(tf)

			var sc ScratchU16
			compressed, err := FSECompressU16EightState(shortData, &sc)
			if err != nil {
				t.Fatalf("compress: %v", err)
			}

			var sd ScratchU16
			got, err := FSEDecompressU16EightState(compressed, &sd)
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

// TestFSE8StateAutoDetect verifies FSEDecompressU16Auto routes 8-state-FSE
// streams to the correct decoder.
func TestFSE8StateAutoDetect(t *testing.T) {
	for _, tf := range testFiles {
		t.Run(tf.name, func(t *testing.T) {
			_, shortData, _, _, _ := SetupTests(tf)

			var sc ScratchU16
			compressed, err := FSECompressU16EightState(shortData, &sc)
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

// TestFSE8StateMagicBytes confirms the [0xFF, 0x84] header is present and
// that corrupting it produces an error.
func TestFSE8StateMagicBytes(t *testing.T) {
	_, shortData, _, _, _ := SetupTests(testFiles[1]) // CT — large enough to compress

	var sc ScratchU16
	compressed, err := FSECompressU16EightState(shortData, &sc)
	if err != nil {
		t.Fatalf("compress: %v", err)
	}

	if len(compressed) < 2 {
		t.Fatal("compressed output too short")
	}
	if compressed[0] != eightStateFSEMagic0 || compressed[1] != eightStateFSEMagic1 {
		t.Errorf("wrong magic: got [%#x, %#x] want [%#x, %#x]",
			compressed[0], compressed[1], eightStateFSEMagic0, eightStateFSEMagic1)
	}

	bad := make([]byte, len(compressed))
	copy(bad, compressed)
	bad[0] = 0x00
	var sd ScratchU16
	if _, err := FSEDecompressU16EightState(bad, &sd); err == nil {
		t.Error("expected error for corrupted magic, got nil")
	}
}

// TestFSE8StateEdgeCases covers small and not-divisible-by-8 inputs to ensure
// the tail-alignment switch handles every residue.
func TestFSE8StateEdgeCases(t *testing.T) {
	t.Run("single_value_returns_ErrUseRLE", func(t *testing.T) {
		data := make([]uint16, 1024)
		for i := range data {
			data[i] = 42
		}
		var s ScratchU16
		_, err := FSECompressU16EightState(data, &s)
		if err != ErrUseRLE {
			t.Errorf("expected ErrUseRLE, got %v", err)
		}
	})

	t.Run("lengths_not_divisible_by_8", func(t *testing.T) {
		// Cover every residue class mod 8 (1..7) plus a couple of larger
		// sizes that exercise both tail and main-loop paths.
		for _, n := range []int{9, 10, 11, 12, 13, 14, 15, 100, 101, 102, 103, 104, 105, 106, 107, 1001, 1007} {
			data := make([]uint16, n)
			for i := range data {
				data[i] = uint16(i % 4)
			}
			var sc ScratchU16
			compressed, err := FSECompressU16EightState(data, &sc)
			if err != nil {
				t.Fatalf("n=%d compress: %v", n, err)
			}
			var sd ScratchU16
			got, err := FSEDecompressU16EightState(compressed, &sd)
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

// BenchmarkFSEDecompress8State compares 4-state and 8-state FSE decompression
// on RLE-encoded inputs. Useful for measuring whether eight independent state
// chains expose more ILP than four — the precondition for an AVX-512 gather
// kernel paying off on a c8i / Granite Rapids host.
func BenchmarkFSEDecompress8State(b *testing.B) {
	for _, tf := range testFiles {
		_, shortData, maxShort, cols, rows := SetupTests(tf)
		var drc DeltaRleCompressU16
		rleData, err := drc.Compress(shortData, cols, rows, maxShort)
		if err != nil {
			b.Skipf("%s: delta+RLE compress: %v", tf.name, err)
		}
		uncompressedBytes := int64(len(rleData) * 2)

		var s4 ScratchU16
		comp4, err := FSECompressU16FourState(rleData, &s4)
		if err != nil {
			b.Skipf("%s: FSE4 compress: %v", tf.name, err)
		}
		var s8 ScratchU16
		comp8, err := FSECompressU16EightState(rleData, &s8)
		if err != nil {
			b.Skipf("%s: FSE8 compress: %v", tf.name, err)
		}

		ratio4 := float64(len(rleData)*2) / float64(len(comp4))
		ratio8 := float64(len(rleData)*2) / float64(len(comp8))

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

		b.Run(tf.name+"/8state", func(b *testing.B) {
			b.SetBytes(uncompressedBytes)
			b.ReportMetric(ratio8, "ratio")
			b.ResetTimer()
			var wg sync.WaitGroup
			for i := 0; i < b.N; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					var sd ScratchU16
					if _, err := FSEDecompressU16EightState(comp8, &sd); err != nil {
						b.Error(err)
					}
				}()
			}
			wg.Wait()
		})
	}
}
