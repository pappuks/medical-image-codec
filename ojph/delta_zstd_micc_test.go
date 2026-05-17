// Copyright 2021 Kuldeep Singh
// This source code is licensed under a MIT-style
// license that can be found in the LICENSE file.

//go:build cgo_ojph && cgo_zstd

// MIC-C (4-state) vs Δ+Zstandard fair throughput comparison.
//
// This complements zstd/delta_zstd_bench_test.go, which compares MIC's
// pure-Go path against in-process libzstd. The numbers there are not
// directly comparable to the paper's headline tables, which use MIC's
// four-state C decoder. This file runs MIC's four-state C path against
// in-process libzstd so the comparison is C-vs-C on both sides.
//
// Build with: go test -tags "cgo_ojph cgo_zstd" -v ./ojph/
//
// Run with:
//
//	go test -tags "cgo_ojph cgo_zstd" -run TestMICCDeltaZstdFair    ./ojph/ -timeout 300s
//	go test -tags "cgo_ojph cgo_zstd" -run=^$ -bench=BenchmarkMICCDeltaZstd ./ojph/ -benchtime=10x
package ojph

import (
	"fmt"
	"testing"
	"time"
	"unsafe"

	mic "mic"
	miczstd "mic/zstd"
)

const zstdLevel = 19

func uint16ToBytes(s []uint16) []byte {
	if len(s) == 0 {
		return nil
	}
	return unsafe.Slice((*byte)(unsafe.Pointer(&s[0])), len(s)*2)
}

func bytesToUint16(b []byte) []uint16 {
	if len(b) == 0 {
		return nil
	}
	return unsafe.Slice((*uint16)(unsafe.Pointer(&b[0])), len(b)/2)
}

func medianDur(times []time.Duration) time.Duration {
	if len(times) == 0 {
		return 0
	}
	cp := make([]time.Duration, len(times))
	copy(cp, times)
	for i := 1; i < len(cp); i++ {
		for j := i; j > 0 && cp[j] < cp[j-1]; j-- {
			cp[j], cp[j-1] = cp[j-1], cp[j]
		}
	}
	return cp[len(cp)/2]
}

func mbpsFromDuration(bytes int, d time.Duration) float64 {
	if d <= 0 {
		return 0
	}
	return float64(bytes) / d.Seconds() / (1 << 20)
}

// TestMICCDeltaZstdFair runs MIC's 4-state C decoder against in-process
// Δ+Zstandard-19 across the full 21-image dataset and prints a paper-style
// table of compression ratio and encode/decode throughput.
func TestMICCDeltaZstdFair(t *testing.T) {
	const decompRuns = 10

	type row struct {
		name                                                 string
		cols, rows, origBytes                                int
		micBytes, zstdBytes                                  int
		micEnc, zstdEnc, micDec, zstdDec                     time.Duration
	}
	var rows []row

	for _, ti := range testImages {
		_, shortData, maxShort, cols, rowsN := loadImage(ti)
		if len(shortData) == 0 {
			t.Logf("Skipping %s: could not load", ti.name)
			continue
		}
		origBytes := len(shortData) * 2

		// --- MIC 4-state C compress ---
		t0 := time.Now()
		micComp, err := MICCompressFourStateC(shortData, cols, rowsN)
		if err != nil {
			t.Fatalf("%s: MICCompressFourStateC: %v", ti.name, err)
		}
		micEnc := time.Since(t0)

		// --- MIC 4-state C decompress (median of decompRuns) ---
		micTimes := make([]time.Duration, decompRuns)
		for i := 0; i < decompRuns; i++ {
			s := time.Now()
			_, err := MICDecompressFourStateC(micComp, cols, rowsN)
			if err != nil {
				t.Fatalf("%s: MICDecompressFourStateC: %v", ti.name, err)
			}
			micTimes[i] = time.Since(s)
		}
		micDec := medianDur(micTimes)

		// --- Δ+Zstd-19 compress ---
		t0 = time.Now()
		deltaOnly, err := mic.DeltaCompressU16(shortData, cols, rowsN, maxShort)
		if err != nil {
			t.Fatalf("%s: DeltaCompressU16: %v", ti.name, err)
		}
		deltaBytes := uint16ToBytes(deltaOnly)
		zComp, err := miczstd.Compress(deltaBytes, zstdLevel)
		if err != nil {
			t.Fatalf("%s: zstd Compress: %v", ti.name, err)
		}
		zstdEnc := time.Since(t0)

		// --- Δ+Zstd-19 decompress (median of decompRuns) ---
		decodedBuf := make([]byte, len(deltaBytes))
		zTimes := make([]time.Duration, decompRuns)
		for i := 0; i < decompRuns; i++ {
			s := time.Now()
			n, err := miczstd.DecompressInto(decodedBuf, zComp)
			if err != nil || n != len(deltaBytes) {
				t.Fatalf("%s: zstd DecompressInto: n=%d err=%v", ti.name, n, err)
			}
			deltaDecoded := bytesToUint16(decodedBuf)
			mic.DeltaDecompressU16(deltaDecoded, cols, rowsN)
			zTimes[i] = time.Since(s)
		}
		zstdDec := medianDur(zTimes)

		rows = append(rows, row{
			name: ti.name, cols: cols, rows: rowsN, origBytes: origBytes,
			micBytes: len(micComp), zstdBytes: len(zComp),
			micEnc: micEnc, zstdEnc: zstdEnc, micDec: micDec, zstdDec: zstdDec,
		})
	}

	fmt.Println()
	fmt.Printf("%-6s %5s %5s %10s %10s %10s %10s %10s %10s %10s\n",
		"Image", "Cols", "Rows", "MIC ratio", "Zstd ratio",
		"MIC enc/s", "Zstd enc/s", "MIC dec/s", "Zstd dec/s", "MIC speedup")
	fmt.Println("------ ----- ----- ---------- ---------- ---------- ---------- ---------- ---------- -----------")
	for _, r := range rows {
		micR := float64(r.origBytes) / float64(r.micBytes)
		zR := float64(r.origBytes) / float64(r.zstdBytes)
		micEnc := mbpsFromDuration(r.origBytes, r.micEnc)
		zEnc := mbpsFromDuration(r.origBytes, r.zstdEnc)
		micDec := mbpsFromDuration(r.origBytes, r.micDec)
		zDec := mbpsFromDuration(r.origBytes, r.zstdDec)
		decSpeedup := micDec / zDec
		fmt.Printf("%-6s %5d %5d %9.2fx %9.2fx %9.0f  %9.0f  %9.0f  %9.0f  %9.2fx\n",
			r.name, r.cols, r.rows, micR, zR,
			micEnc, zEnc, micDec, zDec, decSpeedup)
	}
}

// BenchmarkMICCDeltaZstdDecomp gives per-image decoding throughput for the
// MIC 4-state C decoder vs in-process Δ+Zstandard-19. This is the variant
// that goes into the paper's Δ+Zstd throughput row.
func BenchmarkMICCDeltaZstdDecomp(b *testing.B) {
	for _, ti := range testImages {
		b.Run(ti.name, func(b *testing.B) {
			_, shortData, maxShort, cols, rowsN := loadImage(ti)
			if len(shortData) == 0 {
				b.Skipf("could not load %s", ti.name)
			}
			origBytes := len(shortData) * 2

			micComp, err := MICCompressFourStateC(shortData, cols, rowsN)
			if err != nil {
				b.Fatalf("MICCompressFourStateC: %v", err)
			}

			deltaOnly, err := mic.DeltaCompressU16(shortData, cols, rowsN, maxShort)
			if err != nil {
				b.Fatalf("DeltaCompressU16: %v", err)
			}
			deltaBytes := uint16ToBytes(deltaOnly)
			zComp, err := miczstd.Compress(deltaBytes, zstdLevel)
			if err != nil {
				b.Fatalf("zstd Compress: %v", err)
			}
			decodedBuf := make([]byte, len(deltaBytes))

			micRatio := float64(origBytes) / float64(len(micComp))
			zRatio := float64(origBytes) / float64(len(zComp))

			b.Run("MIC-4state-C", func(b *testing.B) {
				b.SetBytes(int64(origBytes))
				for i := 0; i < b.N; i++ {
					_, err := MICDecompressFourStateC(micComp, cols, rowsN)
					if err != nil {
						b.Fatalf("MICDecompressFourStateC: %v", err)
					}
				}
				b.ReportMetric(micRatio, "ratio")
			})

			b.Run("Zstd-19", func(b *testing.B) {
				b.SetBytes(int64(origBytes))
				for i := 0; i < b.N; i++ {
					n, err := miczstd.DecompressInto(decodedBuf, zComp)
					if err != nil || n != len(deltaBytes) {
						b.Fatalf("zstd DecompressInto: n=%d err=%v", n, err)
					}
					deltaDecoded := bytesToUint16(decodedBuf)
					mic.DeltaDecompressU16(deltaDecoded, cols, rowsN)
				}
				b.ReportMetric(zRatio, "ratio")
			})
		})
	}
}

// BenchmarkMICCDeltaZstdEnc gives per-image encoding throughput for the
// MIC 4-state C encoder vs in-process Δ+Zstandard-19. Compresses with
// level 19 to match the paper's Δ+Zstd-19 ratio column. Level 3 (zstd's
// default) would be roughly two orders of magnitude faster on the
// encoding side at materially worse ratio.
func BenchmarkMICCDeltaZstdEnc(b *testing.B) {
	for _, ti := range testImages {
		b.Run(ti.name, func(b *testing.B) {
			_, shortData, maxShort, cols, rowsN := loadImage(ti)
			if len(shortData) == 0 {
				b.Skipf("could not load %s", ti.name)
			}
			origBytes := len(shortData) * 2

			b.Run("MIC-4state-C", func(b *testing.B) {
				b.SetBytes(int64(origBytes))
				for i := 0; i < b.N; i++ {
					_, err := MICCompressFourStateC(shortData, cols, rowsN)
					if err != nil {
						b.Fatalf("MICCompressFourStateC: %v", err)
					}
				}
			})

			b.Run("Zstd-19", func(b *testing.B) {
				b.SetBytes(int64(origBytes))
				for i := 0; i < b.N; i++ {
					deltaOnly, _ := mic.DeltaCompressU16(shortData, cols, rowsN, maxShort)
					deltaBytes := uint16ToBytes(deltaOnly)
					miczstd.Compress(deltaBytes, zstdLevel)
				}
			})
		})
	}
}
