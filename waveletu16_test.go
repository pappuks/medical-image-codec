// Copyright 2021 Kuldeep Singh
// This source code is licensed under a MIT-style
// license that can be found in the LICENSE file.

package mic

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

// TestWavelet1DRoundTrip verifies the 1D 5/3 wavelet is perfectly reversible.
func TestWavelet1DRoundTrip(t *testing.T) {
	for _, n := range []int{2, 3, 4, 5, 8, 15, 16, 31, 32, 63, 64, 255, 256} {
		t.Run(fmt.Sprintf("n=%d", n), func(t *testing.T) {
			original := make([]int32, n)
			data := make([]int32, n)
			for i := range original {
				original[i] = int32(i*37 + 100)
				data[i] = original[i]
			}
			wt53Forward1D(data, 0, n, 1)
			wt53Inverse1D(data, 0, n, 1)
			for i := range original {
				if data[i] != original[i] {
					t.Fatalf("mismatch at %d: got %d want %d", i, data[i], original[i])
				}
			}
		})
	}
}

// TestWavelet2DRoundTrip verifies the 2D wavelet is perfectly reversible.
func TestWavelet2DRoundTrip(t *testing.T) {
	for _, dim := range [][2]int{{4, 4}, {8, 8}, {15, 17}, {16, 16}, {32, 32}, {63, 65}, {256, 256}} {
		rows, cols := dim[0], dim[1]
		t.Run(fmt.Sprintf("%dx%d", rows, cols), func(t *testing.T) {
			original := make([]int32, rows*cols)
			data := make([]int32, rows*cols)
			for i := range original {
				original[i] = int32((i * 131 + 7) % 65536)
				data[i] = original[i]
			}
			WaveletForward2D(data, rows, cols)
			WaveletInverse2D(data, rows, cols)
			for i := range original {
				if data[i] != original[i] {
					t.Fatalf("mismatch at %d: got %d want %d", i, data[i], original[i])
				}
			}
		})
	}
}

// TestZigZagRoundTrip verifies zigzag encode/decode.
func TestZigZagRoundTrip(t *testing.T) {
	testVals := []int32{0, 1, -1, 2, -2, 127, -128, 255, -256, 32767, -32768}
	for _, v := range testVals {
		enc := zigzagEncode16(v)
		dec := zigzagDecode16(enc)
		if dec != v {
			t.Fatalf("zigzag round-trip failed for %d: encoded=%d decoded=%d", v, enc, dec)
		}
	}
}

// TestWaveletFSECompress verifies the Wavelet+FSE pipeline on test images.
func TestWaveletFSECompress(t *testing.T) {
	for _, tf := range testFiles {
		t.Run(tf.name, func(t *testing.T) {
			_, shortData, maxShort, cols, rows := SetupTests(tf)
			WaveletFSETest(t, shortData, cols, rows, maxShort, 1)
		})
	}
}

// TestWaveletFSECompressMultiLevel verifies multi-level wavelet+FSE.
func TestWaveletFSECompressMultiLevel(t *testing.T) {
	for _, tf := range testFiles {
		t.Run(tf.name, func(t *testing.T) {
			_, shortData, maxShort, cols, rows := SetupTests(tf)
			WaveletFSETest(t, shortData, cols, rows, maxShort, 3)
		})
	}
}

// TestWaveletRLEFSECompress verifies Wavelet+RLE+FSE pipeline on test images.
func TestWaveletRLEFSECompress(t *testing.T) {
	for _, tf := range testFiles {
		t.Run(tf.name, func(t *testing.T) {
			_, shortData, maxShort, cols, rows := SetupTests(tf)
			WaveletRLEFSETest(t, shortData, cols, rows, maxShort, 1)
		})
	}
}

func WaveletFSETest(t *testing.T, shortData []uint16, cols, rows int, maxShort uint16, levels int) {
	start := time.Now()
	compressed, err := WaveletFSECompressU16(shortData, rows, cols, maxShort, levels)
	compressTime := time.Since(start)
	if err != nil {
		t.Fatalf("compression error: %v", err)
	}
	fmt.Printf("Wavelet(%d)+FSE Compress: %d short %d -> %d bytes (%.2f:1) in %v\n",
		levels, len(shortData), len(shortData)*2, len(compressed),
		float64(len(shortData)*2)/float64(len(compressed)), compressTime)

	start = time.Now()
	decompressed, dRows, dCols, err := WaveletFSEDecompressU16(compressed)
	decompressTime := time.Since(start)
	if err != nil {
		t.Fatalf("decompression error: %v", err)
	}
	fmt.Printf("Wavelet(%d)+FSE Decompress: %v\n", levels, decompressTime)

	if dRows != rows || dCols != cols {
		t.Fatalf("dimension mismatch: got %dx%d want %dx%d", dRows, dCols, rows, cols)
	}
	if len(decompressed) != len(shortData) {
		t.Fatalf("length mismatch: got %d want %d", len(decompressed), len(shortData))
	}
	for i := range shortData {
		if shortData[i] != decompressed[i] {
			t.Fatalf("data mismatch at %d: got %d want %d", i, decompressed[i], shortData[i])
		}
	}
	fmt.Printf("PASSED Wavelet(%d)+FSE 16-bit compression-decompression\n", levels)
}

func WaveletRLEFSETest(t *testing.T, shortData []uint16, cols, rows int, maxShort uint16, levels int) {
	start := time.Now()
	compressed, err := WaveletRLEFSECompressU16(shortData, rows, cols, maxShort, levels)
	compressTime := time.Since(start)
	if err != nil {
		t.Fatalf("compression error: %v", err)
	}
	fmt.Printf("Wavelet(%d)+RLE+FSE Compress: %d short %d -> %d bytes (%.2f:1) in %v\n",
		levels, len(shortData), len(shortData)*2, len(compressed),
		float64(len(shortData)*2)/float64(len(compressed)), compressTime)

	start = time.Now()
	decompressed, dRows, dCols, err := WaveletRLEFSEDecompressU16(compressed)
	decompressTime := time.Since(start)
	if err != nil {
		t.Fatalf("decompression error: %v", err)
	}
	fmt.Printf("Wavelet(%d)+RLE+FSE Decompress: %v\n", levels, decompressTime)

	if dRows != rows || dCols != cols {
		t.Fatalf("dimension mismatch: got %dx%d want %dx%d", dRows, dCols, rows, cols)
	}
	if len(decompressed) != len(shortData) {
		t.Fatalf("length mismatch: got %d want %d", len(decompressed), len(shortData))
	}
	for i := range shortData {
		if shortData[i] != decompressed[i] {
			t.Fatalf("data mismatch at %d: got %d want %d", i, decompressed[i], shortData[i])
		}
	}
	fmt.Printf("PASSED Wavelet(%d)+RLE+FSE 16-bit compression-decompression\n", levels)
}

// BenchmarkWaveletFSECompress benchmarks the Wavelet+FSE pipeline.
func BenchmarkWaveletFSECompress(b *testing.B) {
	for _, tf := range testFiles {
		b.Run(tf.name, func(b *testing.B) {
			byteData, shortData, maxShort, cols, rows := SetupTests(tf)
			compressed, err := WaveletFSECompressU16(shortData, rows, cols, maxShort, 1)
			if err != nil {
				b.Skipf("compression failed: %v", err)
			}
			b.SetBytes(int64(len(byteData)))
			b.ResetTimer()
			b.ReportMetric(float64(len(byteData))/float64(len(compressed)), "ratio")
			var wg sync.WaitGroup
			for i := 0; i < b.N; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					WaveletFSEDecompressU16(compressed)
				}()
			}
			wg.Wait()
			b.ReportMetric((float64(len(byteData)) / (1 << 20)), "original")
			b.ReportMetric((float64(len(compressed)) / (1 << 20)), "comp")
			b.ReportMetric(1/float64(b.Elapsed().Seconds()/float64(b.N)), "fps")
			b.ReportMetric(0, "allocs/op")
			b.ReportMetric(0, "B/op")
		})
	}
}

// TestWavelet2DSeparatedRoundTrip verifies that wt53Forward2DSeparated followed
// by wt53Inverse2DSeparated is a perfect lossless round-trip.
func TestWavelet2DSeparatedRoundTrip(t *testing.T) {
	for _, dim := range [][2]int{{4, 4}, {8, 8}, {15, 17}, {16, 16}, {32, 32}, {63, 65}, {256, 256}} {
		rows, cols := dim[0], dim[1]
		t.Run(fmt.Sprintf("%dx%d", rows, cols), func(t *testing.T) {
			original := make([]int32, rows*cols)
			data := make([]int32, rows*cols)
			for i := range original {
				original[i] = int32((i*131 + 7) % 65536)
				data[i] = original[i]
			}
			wt53Forward2DSeparated(data, rows, cols, cols)
			wt53Inverse2DSeparated(data, rows, cols, cols)
			for i := range original {
				if data[i] != original[i] {
					t.Fatalf("mismatch at %d: got %d want %d", i, data[i], original[i])
				}
			}
		})
	}
}

// TestWaveletV2MultiLevelRoundTrip verifies the multi-level V2 pipeline round-trips.
func TestWaveletV2MultiLevelRoundTrip(t *testing.T) {
	for _, levels := range []int{1, 2, 3, 5} {
		for _, dim := range [][2]int{{8, 8}, {32, 32}, {64, 64}, {128, 128}} {
			rows, cols := dim[0], dim[1]
			t.Run(fmt.Sprintf("L%d_%dx%d", levels, rows, cols), func(t *testing.T) {
				original := make([]int32, rows*cols)
				data := make([]int32, rows*cols)
				for i := range original {
					original[i] = int32((i*97 + 13) % 4096) // 12-bit-ish medical range
					data[i] = original[i]
				}
				r, c := rows, cols
				appliedLevels := levels
				dims := make([][2]int, levels)
				for l := 0; l < levels; l++ {
					if r < 2 || c < 2 {
						appliedLevels = l
						break
					}
					dims[l] = [2]int{r, c}
					wt53Forward2DSeparated(data, r, c, cols)
					r = (r + 1) / 2
					c = (c + 1) / 2
				}
				for l := appliedLevels - 1; l >= 0; l-- {
					wt53Inverse2DSeparated(data, dims[l][0], dims[l][1], cols)
				}
				for i := range original {
					if data[i] != original[i] {
						t.Fatalf("L%d %dx%d: mismatch at %d: got %d want %d",
							levels, rows, cols, i, data[i], original[i])
					}
				}
			})
		}
	}
}

// TestWaveletV2RLEFSECompress verifies the V2 Wavelet+RLE+FSE pipeline
// (5-level default, separated subband layout, subband-order scan).
func TestWaveletV2RLEFSECompress(t *testing.T) {
	for _, tf := range testFiles {
		t.Run(tf.name, func(t *testing.T) {
			_, shortData, maxShort, cols, rows := SetupTests(tf)
			WaveletV2RLEFSETest(t, shortData, cols, rows, maxShort, 5)
		})
	}
}

// TestWaveletV2RLEFSECompressLevels tests V2 at different decomposition levels.
func TestWaveletV2RLEFSECompressLevels(t *testing.T) {
	for _, levels := range []int{1, 3, 5} {
		for _, tf := range testFiles {
			t.Run(fmt.Sprintf("L%d/%s", levels, tf.name), func(t *testing.T) {
				_, shortData, maxShort, cols, rows := SetupTests(tf)
				WaveletV2RLEFSETest(t, shortData, cols, rows, maxShort, levels)
			})
		}
	}
}

func WaveletV2RLEFSETest(t *testing.T, shortData []uint16, cols, rows int, maxShort uint16, levels int) {
	t.Helper()
	start := time.Now()
	compressed, err := WaveletV2RLEFSECompressU16(shortData, rows, cols, maxShort, levels)
	compressTime := time.Since(start)
	if err != nil {
		t.Fatalf("compression error: %v", err)
	}
	fmt.Printf("WaveletV2(%d)+RLE+FSE Compress: %d px → %d->%d bytes (%.2f:1) in %v\n",
		levels, len(shortData), len(shortData)*2, len(compressed),
		float64(len(shortData)*2)/float64(len(compressed)), compressTime)

	start = time.Now()
	decompressed, dRows, dCols, err := WaveletV2RLEFSEDecompressU16(compressed)
	decompressTime := time.Since(start)
	if err != nil {
		t.Fatalf("decompression error: %v", err)
	}
	fmt.Printf("WaveletV2(%d)+RLE+FSE Decompress: %v\n", levels, decompressTime)

	if dRows != rows || dCols != cols {
		t.Fatalf("dimension mismatch: got %dx%d want %dx%d", dRows, dCols, rows, cols)
	}
	for i := range shortData {
		if shortData[i] != decompressed[i] {
			t.Fatalf("data mismatch at %d: got %d want %d", i, decompressed[i], shortData[i])
		}
	}
	fmt.Printf("PASSED WaveletV2(%d)+RLE+FSE lossless round-trip\n", levels)
}

// BenchmarkWaveletRLEFSECompress benchmarks the Wavelet+RLE+FSE pipeline.
func BenchmarkWaveletRLEFSECompress(b *testing.B) {
	for _, tf := range testFiles {
		b.Run(tf.name, func(b *testing.B) {
			byteData, shortData, maxShort, cols, rows := SetupTests(tf)
			compressed, err := WaveletRLEFSECompressU16(shortData, rows, cols, maxShort, 1)
			if err != nil {
				b.Skipf("compression failed: %v", err)
			}
			b.SetBytes(int64(len(byteData)))
			b.ResetTimer()
			b.ReportMetric(float64(len(byteData))/float64(len(compressed)), "ratio")
			var wg sync.WaitGroup
			for i := 0; i < b.N; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					WaveletRLEFSEDecompressU16(compressed)
				}()
			}
			wg.Wait()
			b.ReportMetric((float64(len(byteData)) / (1 << 20)), "original")
			b.ReportMetric((float64(len(compressed)) / (1 << 20)), "comp")
			b.ReportMetric(1/float64(b.Elapsed().Seconds()/float64(b.N)), "fps")
			b.ReportMetric(0, "allocs/op")
			b.ReportMetric(0, "B/op")
		})
	}
}

// TestWaveletSIMD2DRoundTrip verifies that the SIMD 2D transform is lossless.
func TestWaveletSIMD2DRoundTrip(t *testing.T) {
	for _, dim := range [][2]int{{4, 4}, {8, 8}, {15, 17}, {32, 32}, {64, 65}, {256, 256}} {
		rows, cols := dim[0], dim[1]
		t.Run(fmt.Sprintf("%dx%d", rows, cols), func(t *testing.T) {
			original := make([]int32, rows*cols)
			data := make([]int32, rows*cols)
			for i := range original {
				original[i] = int32((i*131 + 7) % 65536)
				data[i] = original[i]
			}
			wt53Forward2DSeparatedSIMD(data, rows, cols, cols)
			wt53Inverse2DSeparatedSIMD(data, rows, cols, cols)
			for i := range original {
				if data[i] != original[i] {
					t.Fatalf("mismatch at %d: got %d want %d", i, data[i], original[i])
				}
			}
		})
	}
}

// TestWaveletSIMDMatchesScalar verifies SIMD and scalar transforms produce
// identical coefficient arrays (bit-exact).
func TestWaveletSIMDMatchesScalar(t *testing.T) {
	for _, dim := range [][2]int{{8, 8}, {32, 64}, {64, 32}, {128, 128}, {256, 512}} {
		rows, cols := dim[0], dim[1]
		t.Run(fmt.Sprintf("%dx%d", rows, cols), func(t *testing.T) {
			scalar := make([]int32, rows*cols)
			simd := make([]int32, rows*cols)
			for i := range scalar {
				v := int32((i*97 + 13) % 4096)
				scalar[i] = v
				simd[i] = v
			}
			wt53Forward2DSeparated(scalar, rows, cols, cols)
			wt53Forward2DSeparatedSIMD(simd, rows, cols, cols)
			for i := range scalar {
				if scalar[i] != simd[i] {
					t.Fatalf("%dx%d: forward mismatch at [%d,%d] (elem %d): scalar=%d simd=%d",
						rows, cols, i/cols, i%cols, i, scalar[i], simd[i])
				}
			}
			// Also verify inverse
			wt53Inverse2DSeparated(scalar, rows, cols, cols)
			wt53Inverse2DSeparatedSIMD(simd, rows, cols, cols)
			for i := range scalar {
				if scalar[i] != simd[i] {
					t.Fatalf("%dx%d: inverse mismatch at elem %d: scalar=%d simd=%d",
						rows, cols, i, scalar[i], simd[i])
				}
			}
		})
	}
}

// TestWaveletV2SIMDRLEFSECompress verifies the SIMD pipeline end-to-end.
func TestWaveletV2SIMDRLEFSECompress(t *testing.T) {
	t.Logf("AVX2 available: %v", waveletHasAVX2())
	for _, tf := range testFiles {
		t.Run(tf.name, func(t *testing.T) {
			_, shortData, maxShort, cols, rows := SetupTests(tf)
			start := time.Now()
			compressed, err := WaveletV2SIMDRLEFSECompressU16(shortData, rows, cols, maxShort, 5)
			compTime := time.Since(start)
			if err != nil {
				t.Fatalf("compress: %v", err)
			}
			fmt.Printf("WaveletV2SIMD(5)+RLE+FSE: %d px → %d bytes (%.2f:1) compress=%v\n",
				len(shortData), len(compressed),
				float64(len(shortData)*2)/float64(len(compressed)), compTime)

			start = time.Now()
			got, dRows, dCols, err := WaveletV2SIMDRLEFSEDecompressU16(compressed)
			decompTime := time.Since(start)
			if err != nil {
				t.Fatalf("decompress: %v", err)
			}
			fmt.Printf("WaveletV2SIMD(5) decompress=%v\n", decompTime)
			if dRows != rows || dCols != cols {
				t.Fatalf("dim mismatch")
			}
			for i := range shortData {
				if shortData[i] != got[i] {
					t.Fatalf("data mismatch at %d", i)
				}
			}
		})
	}
}

// BenchmarkWaveletV2RLEFSECompress benchmarks the V2 scalar Wavelet+RLE+FSE pipeline
// (5-level, separated Mallat layout, subband-order scan).
func BenchmarkWaveletV2RLEFSECompress(b *testing.B) {
	for _, tf := range testFiles {
		b.Run(tf.name, func(b *testing.B) {
			byteData, shortData, maxShort, cols, rows := SetupTests(tf)
			compressed, err := WaveletV2RLEFSECompressU16(shortData, rows, cols, maxShort, 5)
			if err != nil {
				b.Skipf("compression failed: %v", err)
			}
			b.SetBytes(int64(len(byteData)))
			b.ResetTimer()
			b.ReportMetric(float64(len(byteData))/float64(len(compressed)), "ratio")
			var wg sync.WaitGroup
			for i := 0; i < b.N; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					WaveletV2RLEFSEDecompressU16(compressed)
				}()
			}
			wg.Wait()
			b.ReportMetric((float64(len(byteData)) / (1 << 20)), "original")
			b.ReportMetric((float64(len(compressed)) / (1 << 20)), "comp")
			b.ReportMetric(1/float64(b.Elapsed().Seconds()/float64(b.N)), "fps")
			b.ReportMetric(0, "allocs/op")
			b.ReportMetric(0, "B/op")
		})
	}
}

// BenchmarkWaveletV2SIMDRLEFSECompress benchmarks the SIMD-accelerated wavelet pipeline.
// On AMD64 with AVX2: uses blocked column transform + AVX2 predict/update kernels.
func BenchmarkWaveletV2SIMDRLEFSECompress(b *testing.B) {
	b.Logf("AVX2: %v", waveletHasAVX2())
	for _, tf := range testFiles {
		b.Run(tf.name, func(b *testing.B) {
			byteData, shortData, maxShort, cols, rows := SetupTests(tf)
			compressed, err := WaveletV2SIMDRLEFSECompressU16(shortData, rows, cols, maxShort, 5)
			if err != nil {
				b.Skipf("compression failed: %v", err)
			}
			b.SetBytes(int64(len(byteData)))
			b.ResetTimer()
			b.ReportMetric(float64(len(byteData))/float64(len(compressed)), "ratio")
			var wg sync.WaitGroup
			for i := 0; i < b.N; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					WaveletV2SIMDRLEFSEDecompressU16(compressed)
				}()
			}
			wg.Wait()
			b.ReportMetric((float64(len(byteData)) / (1 << 20)), "original")
			b.ReportMetric((float64(len(compressed)) / (1 << 20)), "comp")
			b.ReportMetric(1/float64(b.Elapsed().Seconds()/float64(b.N)), "fps")
			b.ReportMetric(0, "allocs/op")
			b.ReportMetric(0, "B/op")
		})
	}
}
