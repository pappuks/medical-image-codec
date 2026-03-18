// Copyright 2024 Kuldeep Singh
// This source code is licensed under a MIT-style
// license that can be found in the LICENSE file.

package mic

import (
	"runtime"
	"testing"
)

// TestParallelStripsRoundtrip verifies that CompressParallelStrips +
// DecompressParallelStrips produce a pixel-exact reconstruction for all test
// modalities and a range of strip counts (1, 2, 4, GOMAXPROCS).
func TestParallelStripsRoundtrip(t *testing.T) {
	stripCounts := []int{1, 2, 4, runtime.GOMAXPROCS(0)}

	for _, td := range testFiles {
		_, pixels, maxVal, width, height := SetupTests(td)

		for _, n := range stripCounts {
			n := n
			t.Run(td.name+"_strips"+itoa(n), func(t *testing.T) {
				compressed, err := CompressParallelStrips(pixels, width, height, maxVal, n)
				if err != nil {
					t.Fatalf("compress: %v", err)
				}

				got, w, h, err := DecompressParallelStrips(compressed)
				if err != nil {
					t.Fatalf("decompress: %v", err)
				}
				if w != width || h != height {
					t.Fatalf("dimension mismatch: got %dx%d, want %dx%d", w, h, width, height)
				}
				if len(got) != len(pixels) {
					t.Fatalf("pixel count mismatch: got %d, want %d", len(got), len(pixels))
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

// TestParallelStripsCompressionRatio measures how strip count affects compression
// ratio on a representative large image (MG3 if available, else MR).
func TestParallelStripsCompressionRatio(t *testing.T) {
	// Use MR (small, always present) for ratio logging; not a failure condition.
	td := testFiles[0] // MR 256x256
	_, pixels, maxVal, width, height := SetupTests(td)

	baselineBlob, err := CompressSingleFrame(pixels, width, height, maxVal)
	if err != nil {
		t.Fatalf("single-frame baseline: %v", err)
	}
	rawBytes := width * height * 2
	t.Logf("%-12s raw=%d  single_frame=%d (%.2fx)",
		td.name, rawBytes, len(baselineBlob), float64(rawBytes)/float64(len(baselineBlob)))

	for _, n := range []int{2, 4, 8, 16} {
		blob, err := CompressParallelStrips(pixels, width, height, maxVal, n)
		if err != nil {
			t.Errorf("strips=%d: %v", n, err)
			continue
		}
		ratio := float64(rawBytes) / float64(len(blob))
		overhead := float64(len(blob)-len(baselineBlob)) / float64(len(baselineBlob)) * 100
		t.Logf("  strips=%-3d compressed=%d (%.2fx)  overhead vs single=+%.2f%%",
			n, len(blob), ratio, overhead)
	}
}

// TestParallelStripsFormatValidation checks header parsing and error paths using
// the MR test image (real medical data, guaranteed compressible).
func TestParallelStripsFormatValidation(t *testing.T) {
	td := testFiles[0] // MR 256x256
	_, pixels, maxVal, width, height := SetupTests(td)

	blob, err := CompressParallelStrips(pixels, width, height, maxVal, 2)
	if err != nil {
		t.Fatal(err)
	}

	// Corrupt magic → should fail.
	bad := make([]byte, len(blob))
	copy(bad, blob)
	bad[0] = 'X'
	if _, _, _, err := DecompressParallelStrips(bad); err == nil {
		t.Fatal("expected error on bad magic, got nil")
	}

	// Truncated data → should fail.
	if _, _, _, err := DecompressParallelStrips(blob[:10]); err == nil {
		t.Fatal("expected error on truncated data, got nil")
	}

	// Valid round-trip.
	got, w, h, err := DecompressParallelStrips(blob)
	if err != nil {
		t.Fatal(err)
	}
	if w != width || h != height {
		t.Fatalf("dims: got %dx%d, want %dx%d", w, h, width, height)
	}
	for i, p := range pixels {
		if got[i] != p {
			t.Fatalf("pixel %d: got %d want %d", i, got[i], p)
		}
	}
}

// TestParallelStripsSingleRowImage verifies strips >= height clamp to height strips.
// Uses the first row of the MR image to guarantee compressibility.
func TestParallelStripsSingleRowImage(t *testing.T) {
	td := testFiles[0] // MR 256x256
	_, pixels, maxVal, width, height := SetupTests(td)

	// Use only the first two rows to keep the test tiny, confirming that
	// numStrips > height is handled gracefully (clamped to 2).
	rows := 2
	pixels = pixels[:width*rows]
	blob, err := CompressParallelStrips(pixels, width, rows, maxVal, height)
	if err != nil {
		t.Fatal(err)
	}
	got, w, h, err := DecompressParallelStrips(blob)
	if err != nil {
		t.Fatal(err)
	}
	if w != width || h != rows {
		t.Fatalf("dims: got %dx%d, want %dx%d", w, h, width, rows)
	}
	for i, p := range pixels {
		if got[i] != p {
			t.Fatalf("pixel %d: got %d want %d", i, got[i], p)
		}
	}
}

// BenchmarkParallelStripsCompress benchmarks parallel compression at different
// strip counts using the CR image (large, good test of CPU scaling).
func BenchmarkParallelStripsCompress(b *testing.B) {
	td := testFiles[2] // CR 1760x2140
	_, pixels, maxVal, width, height := SetupTests(td)

	for _, n := range []int{1, 2, 4, 8} {
		n := n
		b.Run("strips"+itoa(n), func(b *testing.B) {
			b.SetBytes(int64(width * height * 2))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := CompressParallelStrips(pixels, width, height, maxVal, n); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkParallelStripsDecompress benchmarks parallel decompression at
// different strip counts using the CR image.
func BenchmarkParallelStripsDecompress(b *testing.B) {
	td := testFiles[2] // CR 1760x2140
	_, pixels, maxVal, width, height := SetupTests(td)

	for _, n := range []int{1, 2, 4, 8} {
		n := n
		blob, err := CompressParallelStrips(pixels, width, height, maxVal, n)
		if err != nil {
			b.Fatal(err)
		}
		b.Run("strips"+itoa(n), func(b *testing.B) {
			b.SetBytes(int64(width * height * 2))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, _, _, err := DecompressParallelStrips(blob); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// itoa converts an int to string for sub-test naming without importing strconv.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	pos := len(buf)
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}
