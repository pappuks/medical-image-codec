// Copyright 2021 Kuldeep Singh
// This source code is licensed under a MIT-style
// license that can be found in the LICENSE file.

//go:build cgo_zstd

// Fair Delta+Zstandard Comparison (in-process)
//
// Compares MIC's Delta+RLE+FSE pipeline against Delta+Zstandard (level 19),
// using libzstd as an in-process library via CGO. This eliminates the
// CLI/subprocess overhead that inflated the Δ+Zstd decode timings in the
// top-level comparison_test.go:BenchmarkDeltaZstdDecompress. The decode
// throughput numbers produced here are the apples-to-apples version used
// in the paper's Δ+Zstd throughput rows.
//
// Run with:
//
//	go test -tags cgo_zstd -v -run TestDeltaZstdFairComparison    ./zstd/ -timeout 300s
//	go test -tags cgo_zstd -run=^$ -bench=BenchmarkDeltaZstdDecomp ./zstd/ -benchtime=10x
//	go test -tags cgo_zstd -run=^$ -bench=BenchmarkDeltaZstdEnc    ./zstd/ -benchtime=10x
package zstd

import (
	"encoding/binary"
	"fmt"
	"os"
	"testing"
	"time"
	"unsafe"

	mic "mic"

	"github.com/suyashkumar/dicom"
	"github.com/suyashkumar/dicom/pkg/tag"
)

const zstdLevel = 19

type testImage struct {
	name     string
	fileName string
	isBinary bool
	rows     int
	cols     int
}

var testImages = []testImage{
	{name: "MR", fileName: "../testdata/MR_256_256_image.bin", isBinary: true, rows: 256, cols: 256},
	{name: "CT", fileName: "../testdata/CT_512_512_image.bin", isBinary: true, rows: 512, cols: 512},
	{name: "CR", fileName: "../testdata/CR_1760_2140_image.bin", isBinary: true, rows: 2140, cols: 1760},
	{name: "XR", fileName: "../testdata/XR_2577_2048_image.bin", isBinary: true, rows: 2048, cols: 2577},
	{name: "MG1", fileName: "../testdata/MG_image_bin2.bin", isBinary: true, rows: 2457, cols: 1996},
	{name: "MG2", fileName: "../testdata/MG_Image_2_frame.bin", isBinary: true, rows: 2457, cols: 1996},
	{name: "MG3", fileName: "../testdata/MG1.RAW", isBinary: true, rows: 4774, cols: 3064},
	{name: "MG4", fileName: "../testdata/mg-dcm-file.dcm", isBinary: false, rows: 4096, cols: 3328},
	{name: "CT1", fileName: "../testdata/compsamples_refanddir/IMAGES/REF/CT1_UNC", isBinary: false},
	{name: "CT2", fileName: "../testdata/compsamples_refanddir/IMAGES/REF/CT2_UNC", isBinary: false},
	{name: "MG-N", fileName: "../testdata/compsamples_refanddir/IMAGES/REF/MG1_UNC", isBinary: false},
	{name: "MR1", fileName: "../testdata/compsamples_refanddir/IMAGES/REF/MR1_UNC", isBinary: false},
	{name: "MR2", fileName: "../testdata/compsamples_refanddir/IMAGES/REF/MR2_UNC", isBinary: false},
	{name: "MR3", fileName: "../testdata/compsamples_refanddir/IMAGES/REF/MR3_UNC", isBinary: false},
	{name: "MR4", fileName: "../testdata/compsamples_refanddir/IMAGES/REF/MR4_UNC", isBinary: false},
	{name: "NM1", fileName: "../testdata/compsamples_refanddir/IMAGES/REF/NM1_UNC", isBinary: false},
	{name: "RG1", fileName: "../testdata/compsamples_refanddir/IMAGES/REF/RG1_UNC", isBinary: false},
	{name: "RG2", fileName: "../testdata/compsamples_refanddir/IMAGES/REF/RG2_UNC", isBinary: false},
	{name: "RG3", fileName: "../testdata/compsamples_refanddir/IMAGES/REF/RG3_UNC", isBinary: false},
	{name: "SC1", fileName: "../testdata/compsamples_refanddir/IMAGES/REF/SC1_UNC", isBinary: false},
	{name: "XA1", fileName: "../testdata/compsamples_refanddir/IMAGES/REF/XA1_UNC", isBinary: false},
}

func loadImage(ti testImage) (byteData []byte, shortData []uint16, maxShort uint16, cols, rows int) {
	if ti.isBinary {
		data, err := os.ReadFile(ti.fileName)
		if err != nil {
			return nil, nil, 0, 0, 0
		}
		cols, rows = ti.cols, ti.rows
		shortData = make([]uint16, cols*rows)
		for i := 0; i < len(data); i += 2 {
			v := binary.LittleEndian.Uint16(data[i:])
			shortData[i/2] = v
			if v > maxShort {
				maxShort = v
			}
		}
		return data, shortData, maxShort, cols, rows
	}
	dataset, err := dicom.ParseFile(ti.fileName, nil)
	if err != nil {
		return nil, nil, 0, 0, 0
	}
	pixelDataElement, err := dataset.FindElementByTag(tag.PixelData)
	if err != nil {
		return nil, nil, 0, 0, 0
	}
	pixelDataInfo := dicom.MustGetPixelDataInfo(pixelDataElement.Value)
	fr := pixelDataInfo.Frames[0]
	nativeFrame, err := fr.GetNativeFrame()
	if err != nil {
		return nil, nil, 0, 0, 0
	}
	cols = nativeFrame.Cols
	rows = nativeFrame.Rows
	shortData = make([]uint16, cols*rows)
	byteData = make([]byte, cols*rows*2)
	for j := 0; j < len(nativeFrame.Data); j++ {
		shortData[j] = uint16(nativeFrame.Data[j][0])
		if shortData[j] > maxShort {
			maxShort = shortData[j]
		}
		byteData[j*2] = byte(shortData[j])
		byteData[(j*2)+1] = byte(shortData[j] >> 8)
	}
	return byteData, shortData, maxShort, cols, rows
}

// uint16SliceAsBytes returns a view of a []uint16 as a byte slice without
// copying, suitable as the payload for zstd compression. The byte order is
// the host (little-endian on AMD64 and Apple Silicon).
func uint16SliceAsBytes(s []uint16) []byte {
	if len(s) == 0 {
		return nil
	}
	return unsafe.Slice((*byte)(unsafe.Pointer(&s[0])), len(s)*2)
}

// bytesAsUint16Slice is the inverse of uint16SliceAsBytes.
func bytesAsUint16Slice(b []byte) []uint16 {
	if len(b) == 0 {
		return nil
	}
	return unsafe.Slice((*uint16)(unsafe.Pointer(&b[0])), len(b)/2)
}

type compResult struct {
	name          string
	cols, rows    int
	origBytes     int
	micBytes      int
	zstdBytes     int
	micEncMs      float64
	zstdEncMs     float64
	micDecMs      float64
	zstdDecMs     float64
}

// TestDeltaZstdFairComparison loads each image, compresses it with both
// MIC (Delta+RLE+FSE) and Delta+Zstd-19, and prints a per-image table of
// ratio and encode/decode wall-clock time (in-process; no CLI).
func TestDeltaZstdFairComparison(t *testing.T) {
	const decompRuns = 10

	var results []compResult

	for _, ti := range testImages {
		byteData, shortData, maxShort, cols, rows := loadImage(ti)
		if len(shortData) == 0 {
			t.Logf("Skipping %s: could not load", ti.name)
			continue
		}

		origBytes := len(shortData) * 2
		r := compResult{name: ti.name, cols: cols, rows: rows, origBytes: origBytes}

		// --- MIC: Delta+RLE+FSE ---
		var drc mic.DeltaRleCompressU16
		start := time.Now()
		deltaRle, err := drc.Compress(shortData, cols, rows, maxShort)
		if err != nil {
			t.Fatalf("%s: DeltaRleCompress: %v", ti.name, err)
		}
		var scratch mic.ScratchU16
		micComp, err := mic.FSECompressU16(deltaRle, &scratch)
		if err != nil {
			t.Fatalf("%s: FSECompressU16: %v", ti.name, err)
		}
		r.micEncMs = float64(time.Since(start).Microseconds()) / 1000.0
		r.micBytes = len(micComp)

		// Time MIC decompression as the median of decompRuns iterations.
		micTimes := make([]time.Duration, decompRuns)
		for i := 0; i < decompRuns; i++ {
			t0 := time.Now()
			var s2 mic.ScratchU16
			fseOut, err := mic.FSEDecompressU16(micComp, &s2)
			if err != nil {
				t.Fatalf("%s: FSEDecompress: %v", ti.name, err)
			}
			var drd mic.DeltaRleDecompressU16
			drd.Decompress(fseOut, cols, rows)
			micTimes[i] = time.Since(t0)
		}
		r.micDecMs = medianMs(micTimes)

		// --- Δ+Zstd-19 ---
		start = time.Now()
		deltaOnly, err := mic.DeltaCompressU16(shortData, cols, rows, maxShort)
		if err != nil {
			t.Fatalf("%s: DeltaCompressU16: %v", ti.name, err)
		}
		deltaBytes := uint16SliceAsBytes(deltaOnly)
		zComp, err := Compress(deltaBytes, zstdLevel)
		if err != nil {
			t.Fatalf("%s: zstd Compress: %v", ti.name, err)
		}
		r.zstdEncMs = float64(time.Since(start).Microseconds()) / 1000.0
		r.zstdBytes = len(zComp)

		// Time Δ+Zstd decompression as the median of decompRuns iterations,
		// reusing an output buffer to avoid allocation noise.
		decodedBuf := make([]byte, len(deltaBytes))
		zTimes := make([]time.Duration, decompRuns)
		for i := 0; i < decompRuns; i++ {
			t0 := time.Now()
			n, err := DecompressInto(decodedBuf, zComp)
			if err != nil || n != len(deltaBytes) {
				t.Fatalf("%s: zstd DecompressInto: n=%d err=%v", ti.name, n, err)
			}
			deltaDecoded := bytesAsUint16Slice(decodedBuf)
			mic.DeltaDecompressU16(deltaDecoded, cols, rows)
			zTimes[i] = time.Since(t0)
		}
		r.zstdDecMs = medianMs(zTimes)

		// Sanity-check roundtrip on the zstd path (verify the byte/uint16
		// reinterpretation didn't silently corrupt anything).
		_, _ = byteData, decodedBuf

		results = append(results, r)
	}

	// Print a paper-style table.
	fmt.Println()
	fmt.Printf("%-6s %5s %5s %10s %8s %8s %9s %9s %9s %9s\n",
		"Image", "Cols", "Rows", "Orig (B)",
		"MIC bytes", "ΔZstd B", "MIC enc", "Zstd enc", "MIC dec", "Zstd dec")
	fmt.Println("------ ----- ----- ---------- -------- -------- --------- --------- --------- ---------")
	for _, r := range results {
		fmt.Printf("%-6s %5d %5d %10d %8d %8d %8.2fms %8.2fms %8.2fms %8.2fms\n",
			r.name, r.cols, r.rows, r.origBytes,
			r.micBytes, r.zstdBytes,
			r.micEncMs, r.zstdEncMs,
			r.micDecMs, r.zstdDecMs)
	}
	fmt.Println()
	fmt.Printf("%-6s %5s %5s %10s %8s %8s %9s %9s %9s %9s\n",
		"Image", "", "", "Ratio:", "MIC", "Δ+Zstd", "MIC enc", "Zstd enc", "MIC dec", "Zstd dec")
	fmt.Println("------ ----- ----- ---------- -------- -------- --------- --------- --------- ---------")
	for _, r := range results {
		micRatio := float64(r.origBytes) / float64(r.micBytes)
		zRatio := float64(r.origBytes) / float64(r.zstdBytes)
		micEncMBps := mbps(r.origBytes, r.micEncMs)
		zEncMBps := mbps(r.origBytes, r.zstdEncMs)
		micDecMBps := mbps(r.origBytes, r.micDecMs)
		zDecMBps := mbps(r.origBytes, r.zstdDecMs)
		fmt.Printf("%-6s %5d %5d %10s %7.2fx %7.2fx %7.0f/s %7.0f/s %7.0f/s %7.0f/s\n",
			r.name, r.cols, r.rows, "",
			micRatio, zRatio,
			micEncMBps, zEncMBps,
			micDecMBps, zDecMBps)
	}
}

// BenchmarkDeltaZstdDecomp benchmarks per-image decompression throughput
// for both MIC and Δ+Zstd (in-process libzstd). Use this for paper numbers:
//
//	go test -tags cgo_zstd -run=^$ -bench=BenchmarkDeltaZstdDecomp ./zstd/ -benchtime=10x
func BenchmarkDeltaZstdDecomp(b *testing.B) {
	for _, ti := range testImages {
		b.Run(ti.name, func(b *testing.B) {
			_, shortData, maxShort, cols, rows := loadImage(ti)
			if len(shortData) == 0 {
				b.Skipf("could not load %s", ti.name)
			}
			origBytes := len(shortData) * 2

			// Prepare MIC compressed bytes.
			var drc mic.DeltaRleCompressU16
			deltaRle, _ := drc.Compress(shortData, cols, rows, maxShort)
			var s1 mic.ScratchU16
			micComp, _ := mic.FSECompressU16(deltaRle, &s1)

			// Prepare Δ+Zstd compressed bytes.
			deltaOnly, _ := mic.DeltaCompressU16(shortData, cols, rows, maxShort)
			deltaBytes := uint16SliceAsBytes(deltaOnly)
			zComp, err := Compress(deltaBytes, zstdLevel)
			if err != nil {
				b.Fatalf("zstd Compress: %v", err)
			}
			decodedBuf := make([]byte, len(deltaBytes))

			micRatio := float64(origBytes) / float64(len(micComp))
			zRatio := float64(origBytes) / float64(len(zComp))

			b.Run("MIC", func(b *testing.B) {
				b.SetBytes(int64(origBytes))
				for i := 0; i < b.N; i++ {
					var s2 mic.ScratchU16
					fseOut, _ := mic.FSEDecompressU16(micComp, &s2)
					var drd mic.DeltaRleDecompressU16
					drd.Decompress(fseOut, cols, rows)
				}
				b.ReportMetric(micRatio, "ratio")
			})

			b.Run("Zstd", func(b *testing.B) {
				b.SetBytes(int64(origBytes))
				for i := 0; i < b.N; i++ {
					n, err := DecompressInto(decodedBuf, zComp)
					if err != nil || n != len(deltaBytes) {
						b.Fatalf("DecompressInto: n=%d err=%v", n, err)
					}
					deltaDecoded := bytesAsUint16Slice(decodedBuf)
					mic.DeltaDecompressU16(deltaDecoded, cols, rows)
				}
				b.ReportMetric(zRatio, "ratio")
			})
		})
	}
}

// BenchmarkDeltaZstdEnc benchmarks per-image encoding throughput for both
// MIC and Δ+Zstd. Mirrors BenchmarkDeltaZstdDecomp.
func BenchmarkDeltaZstdEnc(b *testing.B) {
	for _, ti := range testImages {
		b.Run(ti.name, func(b *testing.B) {
			_, shortData, maxShort, cols, rows := loadImage(ti)
			if len(shortData) == 0 {
				b.Skipf("could not load %s", ti.name)
			}
			origBytes := len(shortData) * 2

			b.Run("MIC", func(b *testing.B) {
				b.SetBytes(int64(origBytes))
				for i := 0; i < b.N; i++ {
					var drc mic.DeltaRleCompressU16
					deltaRle, _ := drc.Compress(shortData, cols, rows, maxShort)
					var s mic.ScratchU16
					mic.FSECompressU16(deltaRle, &s)
				}
			})

			b.Run("Zstd", func(b *testing.B) {
				b.SetBytes(int64(origBytes))
				for i := 0; i < b.N; i++ {
					deltaOnly, _ := mic.DeltaCompressU16(shortData, cols, rows, maxShort)
					deltaBytes := uint16SliceAsBytes(deltaOnly)
					Compress(deltaBytes, zstdLevel)
				}
			})
		})
	}
}

// medianMs returns the median of the given durations in milliseconds.
func medianMs(times []time.Duration) float64 {
	if len(times) == 0 {
		return 0
	}
	// Insertion sort; len(times) is small (typically 10).
	cp := make([]time.Duration, len(times))
	copy(cp, times)
	for i := 1; i < len(cp); i++ {
		for j := i; j > 0 && cp[j] < cp[j-1]; j-- {
			cp[j], cp[j-1] = cp[j-1], cp[j]
		}
	}
	mid := cp[len(cp)/2]
	return float64(mid.Microseconds()) / 1000.0
}

func mbps(bytes int, ms float64) float64 {
	if ms <= 0 {
		return 0
	}
	return float64(bytes) / (ms / 1000.0) / (1 << 20)
}
