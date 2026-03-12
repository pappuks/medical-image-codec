// Copyright 2024 Kuldeep Singh
// This source code is licensed under a MIT-style
// license that can be found in the LICENSE file.

package mic

import (
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"testing"
	"time"
)

// TestDeltaZstdComparison compresses each test image with:
//  1. MIC pipeline: Delta+RLE+FSE
//  2. Delta+zstd: delta-encode the uint16 stream, then compress the byte
//     representation with zstd (via CLI, levels 1, 3, and 19).
//
// This establishes whether MIC's custom RLE+FSE stages add value beyond
// feeding the same delta-encoded residuals to a general-purpose compressor.
func TestDeltaZstdComparison(t *testing.T) {
	if _, err := exec.LookPath("zstd"); err != nil {
		t.Skip("zstd CLI not found, skipping comparison")
	}

	fmt.Println()
	fmt.Printf("%-6s  %12s  %10s  %10s  %10s  %10s  %10s\n",
		"Image", "Original", "MIC", "d+zstd-1", "d+zstd-3", "d+zstd-19", "raw-zstd-3")
	fmt.Printf("%-6s  %12s  %10s  %10s  %10s  %10s  %10s\n",
		"", "(bytes)", "(ratio)", "(ratio)", "(ratio)", "(ratio)", "(ratio)")
	fmt.Println("------  ------------  ----------  ----------  ----------  ----------  ----------")

	for _, tf := range testFiles {
		t.Run(tf.name, func(t *testing.T) {
			byteData, shortData, maxShort, cols, rows := SetupTests(tf)
			originalBytes := len(shortData) * 2

			// --- MIC pipeline: Delta+RLE+FSE ---
			var drc DeltaRleCompressU16
			deltaComp, err := drc.Compress(shortData, cols, rows, maxShort)
			if err != nil {
				t.Fatalf("delta+RLE compress: %v", err)
			}
			var s ScratchU16
			micComp, err := FSECompressU16(deltaComp, &s)
			if err != nil {
				t.Fatalf("FSE compress: %v", err)
			}
			micRatio := float64(originalBytes) / float64(len(micComp))

			// --- Delta encode, then convert to bytes for zstd ---
			deltaOnly, err := DeltaCompressU16(shortData, cols, rows, maxShort)
			if err != nil {
				t.Fatalf("delta compress: %v", err)
			}
			deltaBytes := uint16SliceToBytes(deltaOnly)

			// Compress with zstd at different levels
			zstdRatios := make([]float64, 0, 3)
			for _, level := range []int{1, 3, 19} {
				compSize, err := compressWithZstd(deltaBytes, level)
				if err != nil {
					t.Fatalf("zstd level %d: %v", level, err)
				}
				zstdRatios = append(zstdRatios, float64(originalBytes)/float64(compSize))
			}

			// --- Raw (no delta) + zstd-3 for reference ---
			rawSize, err := compressWithZstd(byteData, 3)
			if err != nil {
				t.Fatalf("raw zstd: %v", err)
			}
			rawZstdRatio := float64(originalBytes) / float64(rawSize)

			fmt.Printf("%-6s  %12d  %10.2f  %10.2f  %10.2f  %10.2f  %10.2f\n",
				tf.name, originalBytes, micRatio, zstdRatios[0], zstdRatios[1], zstdRatios[2], rawZstdRatio)
		})
	}
}

// BenchmarkDeltaZstdDecompress benchmarks decompression speed for Delta+zstd
// vs MIC's Delta+RLE+FSE pipeline. Note: zstd decompression uses the CLI
// (subprocess), so timings include process launch overhead.
func BenchmarkDeltaZstdDecompress(b *testing.B) {
	if _, err := exec.LookPath("zstd"); err != nil {
		b.Skip("zstd CLI not found")
	}

	for _, tf := range testFiles {
		b.Run(tf.name, func(b *testing.B) {
			byteData, shortData, maxShort, cols, rows := SetupTests(tf)

			// Prepare MIC compressed data
			var drc DeltaRleCompressU16
			deltaComp, _ := drc.Compress(shortData, cols, rows, maxShort)
			var s ScratchU16
			micComp, _ := FSECompressU16(deltaComp, &s)

			// Prepare zstd compressed data (level 3)
			deltaOnly, _ := DeltaCompressU16(shortData, cols, rows, maxShort)
			deltaBytes := uint16SliceToBytes(deltaOnly)
			zstdFile := writeTempZstd(b, deltaBytes, 3)
			defer os.Remove(zstdFile)

			b.SetBytes(int64(len(byteData)))
			b.ResetTimer()

			b.Run("MIC", func(b *testing.B) {
				b.SetBytes(int64(len(byteData)))
				for i := 0; i < b.N; i++ {
					var s2 ScratchU16
					fseOut, _ := FSEDecompressU16(micComp, &s2)
					var drd DeltaRleDecompressU16
					drd.Decompress(fseOut, cols, rows)
				}
			})

			b.Run("zstd-3", func(b *testing.B) {
				b.SetBytes(int64(len(byteData)))
				for i := 0; i < b.N; i++ {
					decompressWithZstdFile(zstdFile)
				}
			})
		})
	}
}

// TestMEDPredictorComparison compares the MED predictor against the standard
// avg-of-neighbors predictor used in MIC, measuring compression ratio through
// the full Delta+RLE+FSE pipeline for each.
func TestMEDPredictorComparison(t *testing.T) {
	fmt.Println()
	fmt.Printf("%-6s  %12s  %10s  %10s  %8s\n",
		"Image", "Original", "Avg", "MED", "Diff")
	fmt.Printf("%-6s  %12s  %10s  %10s  %8s\n",
		"", "(bytes)", "(ratio)", "(ratio)", "(%)")
	fmt.Println("------  ------------  ----------  ----------  --------")

	for _, tf := range testFiles {
		t.Run(tf.name, func(t *testing.T) {
			_, shortData, maxShort, cols, rows := SetupTests(tf)
			originalBytes := len(shortData) * 2

			// --- Standard avg predictor: Delta+RLE+FSE ---
			avgRatio, avgCompTime, avgDecompTime := benchmarkPipeline(shortData, cols, rows, maxShort, false)

			// --- MED predictor: MEDDelta+RLE+FSE ---
			medRatio, medCompTime, medDecompTime := benchmarkPipeline(shortData, cols, rows, maxShort, true)

			diff := (medRatio - avgRatio) / avgRatio * 100

			fmt.Printf("%-6s  %12d  %10.3f  %10.3f  %+7.2f%%\n",
				tf.name, originalBytes, avgRatio, medRatio, diff)
			fmt.Printf("  compress:   avg=%v  med=%v\n", avgCompTime, medCompTime)
			fmt.Printf("  decompress: avg=%v  med=%v\n", avgDecompTime, medDecompTime)

			// Verify MED roundtrip
			medDelta, err := MEDDeltaCompressU16(shortData, cols, rows, maxShort)
			if err != nil {
				t.Fatalf("MED compress: %v", err)
			}
			medOut := MEDDeltaDecompressU16(medDelta, cols, rows)
			for i := range shortData {
				if shortData[i] != medOut[i] {
					t.Fatalf("MED roundtrip mismatch at %d: got %d want %d", i, medOut[i], shortData[i])
				}
			}
		})
	}
}

// BenchmarkMEDPredictor benchmarks MED vs avg predictor decompression throughput.
func BenchmarkMEDPredictor(b *testing.B) {
	for _, tf := range testFiles {
		b.Run(tf.name, func(b *testing.B) {
			byteData, shortData, maxShort, cols, rows := SetupTests(tf)

			// Prepare avg predictor data
			var drc DeltaRleCompressU16
			avgDelta, _ := drc.Compress(shortData, cols, rows, maxShort)
			var s1 ScratchU16
			avgComp, _ := FSECompressU16(avgDelta, &s1)

			// Prepare MED predictor data
			medDeltaRaw, _ := MEDDeltaCompressU16(shortData, cols, rows, maxShort)
			var rle RleCompressU16
			rle.Init(cols, rows, medDeltaRaw[0]) // maxValue stored as first element
			// For MED, we use standalone delta then RLE+FSE (no combined DeltaRle struct)
			medRleOut := rle.Compress(medDeltaRaw)
			var s2 ScratchU16
			medComp, _ := FSECompressU16(medRleOut, &s2)

			b.SetBytes(int64(len(byteData)))
			b.ResetTimer()

			b.Run("Avg", func(b *testing.B) {
				b.SetBytes(int64(len(byteData)))
				for i := 0; i < b.N; i++ {
					var s ScratchU16
					fseOut, _ := FSEDecompressU16(avgComp, &s)
					var drd DeltaRleDecompressU16
					drd.Decompress(fseOut, cols, rows)
				}
			})

			b.Run("MED", func(b *testing.B) {
				b.SetBytes(int64(len(byteData)))
				for i := 0; i < b.N; i++ {
					var s ScratchU16
					fseOut, _ := FSEDecompressU16(medComp, &s)
					var rleD RleDecompressU16
					rleD.Init(fseOut)
					rleDecomp := rleD.Decompress()
					MEDDeltaDecompressU16(rleDecomp, cols, rows)
				}
			})
		})
	}
}

// benchmarkPipeline runs the full compress+decompress cycle and returns
// the compression ratio and timing for both compression and decompression.
func benchmarkPipeline(data []uint16, cols, rows int, maxShort uint16, useMED bool) (ratio float64, compTime, decompTime time.Duration) {
	originalBytes := len(data) * 2

	if useMED {
		start := time.Now()
		medDelta, _ := MEDDeltaCompressU16(data, cols, rows, maxShort)
		// Use RLE on the MED delta output
		var rle RleCompressU16
		rle.Init(cols, rows, medDelta[0])
		rleOut := rle.Compress(medDelta)
		var s ScratchU16
		comp, err := FSECompressU16(rleOut, &s)
		compTime = time.Since(start)
		if err != nil {
			return 0, compTime, 0
		}
		ratio = float64(originalBytes) / float64(len(comp))

		start = time.Now()
		var s2 ScratchU16
		fseOut, _ := FSEDecompressU16(comp, &s2)
		var rleD RleDecompressU16
		rleD.Init(fseOut)
		rleDecomp := rleD.Decompress()
		MEDDeltaDecompressU16(rleDecomp, cols, rows)
		decompTime = time.Since(start)
	} else {
		start := time.Now()
		var drc DeltaRleCompressU16
		deltaComp, _ := drc.Compress(data, cols, rows, maxShort)
		var s ScratchU16
		comp, err := FSECompressU16(deltaComp, &s)
		compTime = time.Since(start)
		if err != nil {
			return 0, compTime, 0
		}
		ratio = float64(originalBytes) / float64(len(comp))

		start = time.Now()
		var s2 ScratchU16
		fseOut, _ := FSEDecompressU16(comp, &s2)
		var drd DeltaRleDecompressU16
		drd.Decompress(fseOut, cols, rows)
		decompTime = time.Since(start)
	}
	return
}

// uint16SliceToBytes converts a []uint16 to []byte in little-endian order.
func uint16SliceToBytes(data []uint16) []byte {
	out := make([]byte, len(data)*2)
	for i, v := range data {
		binary.LittleEndian.PutUint16(out[i*2:], v)
	}
	return out
}

// compressWithZstd compresses data with zstd at the given level and returns
// the compressed size. Uses the zstd CLI tool.
func compressWithZstd(data []byte, level int) (int, error) {
	inFile, err := os.CreateTemp("", "mic-zstd-in-*.bin")
	if err != nil {
		return 0, err
	}
	defer os.Remove(inFile.Name())

	if _, err := inFile.Write(data); err != nil {
		inFile.Close()
		return 0, err
	}
	inFile.Close()

	outFile := inFile.Name() + ".zst"
	defer os.Remove(outFile)

	cmd := exec.Command("zstd", fmt.Sprintf("-%d", level), "-f", "-q", "-o", outFile, inFile.Name())
	if err := cmd.Run(); err != nil {
		return 0, fmt.Errorf("zstd compress: %w", err)
	}

	info, err := os.Stat(outFile)
	if err != nil {
		return 0, err
	}
	return int(info.Size()), nil
}

// writeTempZstd writes data compressed with zstd to a temp file and returns the path.
func writeTempZstd(tb testing.TB, data []byte, level int) string {
	inFile, err := os.CreateTemp("", "mic-zstd-bench-*.bin")
	if err != nil {
		tb.Fatal(err)
	}
	defer os.Remove(inFile.Name())

	if _, err := inFile.Write(data); err != nil {
		inFile.Close()
		tb.Fatal(err)
	}
	inFile.Close()

	outFile := inFile.Name() + ".zst"
	cmd := exec.Command("zstd", fmt.Sprintf("-%d", level), "-f", "-o", outFile, inFile.Name())
	if err := cmd.Run(); err != nil {
		tb.Fatalf("zstd compress: %v", err)
	}
	return outFile
}

// decompressWithZstdFile decompresses a .zst file using the zstd CLI.
func decompressWithZstdFile(path string) {
	cmd := exec.Command("zstd", "-d", "-f", "--stdout", path)
	cmd.Stdout = nil // discard output
	cmd.Run()
}
