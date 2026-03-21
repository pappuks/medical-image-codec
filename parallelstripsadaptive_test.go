// Copyright 2026 Kuldeep Singh
// This source code is licensed under a MIT-style
// license that can be found in the LICENSE file.

package mic

import (
	"runtime"
	"testing"
)

// TestParallelStripsAdaptiveRoundtrip verifies pixel-exact roundtrip for all
// test modalities with 1, 2, 4, and GOMAXPROCS strips.
func TestParallelStripsAdaptiveRoundtrip(t *testing.T) {
	stripCounts := []int{1, 2, 4, runtime.GOMAXPROCS(0)}

	for _, td := range testFiles {
		_, pixels, maxVal, width, height := SetupTests(td)

		for _, n := range stripCounts {
			n := n
			t.Run(td.name+"_strips"+itoa(n), func(t *testing.T) {
				compressed, err := CompressParallelStripsAdaptive(pixels, width, height, maxVal, n)
				if err != nil {
					t.Fatalf("compress: %v", err)
				}

				got, w, h, err := DecompressParallelStripsAdaptive(compressed)
				if err != nil {
					t.Fatalf("decompress: %v", err)
				}
				if w != width || h != height {
					t.Fatalf("dimension mismatch: got %dx%d, want %dx%d", w, h, width, height)
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

// TestParallelStripsAdaptiveRatio compares compression ratio of adaptive PICA
// vs standard PICS for each modality, logging which strips chose grad predictor.
func TestParallelStripsAdaptiveRatio(t *testing.T) {
	const numStrips = 4

	for _, td := range testFiles {
		_, pixels, maxVal, width, height := SetupTests(td)
		rawBytes := width * height * 2

		picaBlob, err := CompressParallelStripsAdaptive(pixels, width, height, maxVal, numStrips)
		if err != nil {
			t.Errorf("%s: pica compress: %v", td.name, err)
			continue
		}
		picsBlob, err := CompressParallelStrips(pixels, width, height, maxVal, numStrips)
		if err != nil {
			t.Errorf("%s: pics compress: %v", td.name, err)
			continue
		}

		picaRatio := float64(rawBytes) / float64(len(picaBlob))
		picsRatio := float64(rawBytes) / float64(len(picsBlob))
		diff := (picaRatio - picsRatio) / picsRatio * 100

		// Count how many strips used the grad predictor.
		gradCount := picaGradCount(picaBlob)

		t.Logf("%-12s  PICS=%.3fx  PICA=%.3fx  delta=%+.2f%%  grad_strips=%d/%d",
			td.name, picsRatio, picaRatio, diff, gradCount, numStrips)
	}
}

// BenchmarkParallelStripsAdaptive benchmarks PICA compression with 1/2/4/8 strips.
func BenchmarkParallelStripsAdaptive(b *testing.B) {
	td := testFiles[0] // MR 256x256 — always present
	_, pixels, maxVal, width, height := SetupTests(td)

	for _, n := range []int{1, 2, 4, 8} {
		n := n
		b.Run("strips"+itoa(n), func(b *testing.B) {
			b.ReportAllocs()
			var lastLen int
			for i := 0; i < b.N; i++ {
				blob, err := CompressParallelStripsAdaptive(pixels, width, height, maxVal, n)
				if err != nil {
					b.Fatal(err)
				}
				lastLen = len(blob)
			}
			b.SetBytes(int64(width * height * 2))
			_ = lastLen
		})
	}
}

// picaGradCount parses a PICA blob and returns how many strips used the grad predictor.
func picaGradCount(blob []byte) int {
	if len(blob) < picaHdrSize {
		return 0
	}
	numStrips := int(blob[12]) | int(blob[13])<<8 | int(blob[14])<<16 | int(blob[15])<<24
	count := 0
	for i := 0; i < numStrips; i++ {
		off := picaHdrSize + i*picaEntrySize + 12 // flags field
		if off+4 > len(blob) {
			break
		}
		flags := uint32(blob[off]) | uint32(blob[off+1])<<8 | uint32(blob[off+2])<<16 | uint32(blob[off+3])<<24
		if flags&picaFlagGradPredictor != 0 {
			count++
		}
	}
	return count
}
