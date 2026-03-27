// Copyright 2021 Kuldeep Singh
// This source code is licensed under a MIT-style
// license that can be found in the LICENSE file.

//go:build cgo_ojph

// Fair HTJ2K Comparison Framework (In-Process)
//
// Compares MIC (Delta+RLE+FSE two-state) against HTJ2K (lossless) using OpenJPH
// as an in-process library via CGO. This eliminates the subprocess launch and
// file I/O overhead that inflated HTJ2K timings in the original comparison.
//
// Run with:
//
//	go test -v -run TestHTJ2KFairComparison ./ojph/ -timeout 300s
//	go test -run=^$ -bench=BenchmarkHTJ2KFairDecomp ./ojph/ -benchtime=10x
package ojph

import (
	"encoding/binary"
	"fmt"
	"math"
	"math/bits"
	"os"
	"testing"
	"time"

	mic "mic"

	"github.com/suyashkumar/dicom"
	"github.com/suyashkumar/dicom/pkg/tag"
)

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
	// DICOM conformance test images (NEMA compsamples)
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
	// US1_UNC and VL1–VL6_UNC are RGB (samples=3) — not yet supported; see roadmap
}

func loadImage(ti testImage) ([]byte, []uint16, uint16, int, int) {
	if ti.isBinary {
		byteData, _ := os.ReadFile(ti.fileName)
		cols, rows := ti.cols, ti.rows
		shortData := make([]uint16, cols*rows)
		var maxShort uint16
		for i := 0; i < len(byteData); i += 2 {
			v := binary.LittleEndian.Uint16(byteData[i:])
			shortData[i/2] = v
			if v > maxShort {
				maxShort = v
			}
		}
		return byteData, shortData, maxShort, cols, rows
	}
	// DICOM file
	dataset, err := dicom.ParseFile(ti.fileName, nil)
	if err != nil {
		return nil, nil, 0, 0, 0
	}
	pixelDataElement, _ := dataset.FindElementByTag(tag.PixelData)
	pixelDataInfo := dicom.MustGetPixelDataInfo(pixelDataElement.Value)
	fr := pixelDataInfo.Frames[0]
	nativeFrame, _ := fr.GetNativeFrame()
	shortData := make([]uint16, nativeFrame.Cols*nativeFrame.Rows)
	byteData := make([]byte, nativeFrame.Cols*nativeFrame.Rows*2)
	var maxShort uint16
	for j := 0; j < len(nativeFrame.Data); j++ {
		shortData[j] = uint16(nativeFrame.Data[j][0])
		if shortData[j] > maxShort {
			maxShort = shortData[j]
		}
		byteData[j*2] = byte(shortData[j])
		byteData[(j*2)+1] = byte(shortData[j] >> 8)
	}
	return byteData, shortData, maxShort, nativeFrame.Cols, nativeFrame.Rows
}

type compResult struct {
	name          string
	width, height int
	origBytes     int
	micBytes      int
	htj2kBytes    int
	micCompMs     float64
	htj2kCompMs   float64
	micDecompMs   float64
	htj2kDecompMs float64
}

// TestHTJ2KFairComparison runs MIC and HTJ2K in-process on all test images.
// Both codecs are called as library functions — no subprocess or file I/O overhead.
func TestHTJ2KFairComparison(t *testing.T) {
	const decompRuns = 10

	var results []compResult

	for _, ti := range testImages {
		byteData, shortData, maxShort, cols, rows := loadImage(ti)
		if len(shortData) == 0 {
			t.Logf("Skipping %s: could not load", ti.name)
			continue
		}
		origBytes := len(byteData)
		bitDepth := bits.Len16(maxShort)
		if bitDepth == 0 {
			bitDepth = 1
		}

		// --- MIC compress ---
		micCompStart := time.Now()
		var drc mic.DeltaRleCompressU16
		deltaComp, err := drc.Compress(shortData, cols, rows, maxShort)
		if err != nil {
			t.Errorf("%s: MIC delta+rle compress: %v", ti.name, err)
			continue
		}
		var s mic.ScratchU16
		fseComp, err := mic.FSECompressU16TwoState(deltaComp, &s)
		if err != nil {
			t.Errorf("%s: MIC FSE compress: %v", ti.name, err)
			continue
		}
		micCompMs := float64(time.Since(micCompStart).Microseconds()) / 1000.0

		// --- HTJ2K compress (in-process) ---
		htj2kCompStart := time.Now()
		htj2kComp, err := CompressU16(shortData, cols, rows, bitDepth)
		if err != nil {
			t.Errorf("%s: HTJ2K compress: %v", ti.name, err)
			continue
		}
		htj2kCompMs := float64(time.Since(htj2kCompStart).Microseconds()) / 1000.0

		// --- MIC decompress (best of N) ---
		micMinDecomp := time.Duration(math.MaxInt64)
		for i := 0; i < decompRuns; i++ {
			start := time.Now()
			var s2 mic.ScratchU16
			rleData, _ := mic.FSEDecompressU16TwoState(fseComp, &s2)
			var drd mic.DeltaRleDecompressU16
			drd.Decompress(rleData, cols, rows)
			elapsed := time.Since(start)
			if elapsed < micMinDecomp {
				micMinDecomp = elapsed
			}
		}

		// --- HTJ2K decompress (in-process, best of N) ---
		htj2kMinDecomp := time.Duration(math.MaxInt64)
		for i := 0; i < decompRuns; i++ {
			start := time.Now()
			_, err := DecompressU16(htj2kComp, cols, rows)
			if err != nil {
				t.Errorf("%s: HTJ2K decompress: %v", ti.name, err)
				break
			}
			elapsed := time.Since(start)
			if elapsed < htj2kMinDecomp {
				htj2kMinDecomp = elapsed
			}
		}

		results = append(results, compResult{
			name:          ti.name,
			width:         cols,
			height:        rows,
			origBytes:     origBytes,
			micBytes:      len(fseComp),
			htj2kBytes:    len(htj2kComp),
			micCompMs:     micCompMs,
			htj2kCompMs:   htj2kCompMs,
			micDecompMs:   float64(micMinDecomp.Microseconds()) / 1000.0,
			htj2kDecompMs: float64(htj2kMinDecomp.Microseconds()) / 1000.0,
		})
	}

	// Print formatted comparison table.
	fmt.Println()
	fmt.Println("=== MIC vs HTJ2K Fair Comparison (Both In-Process, No Subprocess Overhead) ===")
	fmt.Println()
	fmt.Printf("Both MIC and HTJ2K timings are in-process library calls.\n")
	fmt.Printf("HTJ2K uses OpenJPH via CGO (mem_infile/mem_outfile, no disk I/O).\n")
	fmt.Printf("Decompression is best of %d runs.\n", decompRuns)
	fmt.Println()

	fmt.Printf("%-6s  %9s  %10s  %7s  %7s  %10s  %10s  %10s  %10s  %8s  %8s  %8s\n",
		"Image", "Orig (MB)", "WxH",
		"MIC-r", "HTJ2K-r",
		"MIC-c(ms)", "HTJ2K-c(ms)",
		"MIC-d(ms)", "HTJ2K-d(ms)",
		"MIC GB/s", "HTJ2K GB/s", "Speedup",
	)
	sep := "------  ---------  ----------  -------  -------  ----------  ----------  ----------  ----------  --------  ----------  -------"
	fmt.Println(sep)

	var geoSpeedup float64
	var count int

	for _, r := range results {
		origMB := float64(r.origBytes) / (1 << 20)
		micRatio := float64(r.origBytes) / float64(r.micBytes)
		htj2kRatio := float64(r.origBytes) / float64(r.htj2kBytes)
		micGBs := (float64(r.origBytes) / (1 << 30)) / (r.micDecompMs / 1000.0)
		htj2kGBs := (float64(r.origBytes) / (1 << 30)) / (r.htj2kDecompMs / 1000.0)
		speedup := micGBs / htj2kGBs

		fmt.Printf("%-6s  %9.2f  %4dx%-4d  %7.2f  %7.2f  %10.1f  %10.1f  %10.2f  %10.2f  %8.2f  %10.2f  %7.2fx\n",
			r.name, origMB, r.width, r.height,
			micRatio, htj2kRatio,
			r.micCompMs, r.htj2kCompMs,
			r.micDecompMs, r.htj2kDecompMs,
			micGBs, htj2kGBs, speedup,
		)

		geoSpeedup += math.Log(speedup)
		count++
	}

	fmt.Println(sep)
	if count > 0 {
		fmt.Printf("\nGeometric mean decompression speedup (MIC / HTJ2K): %.2fx\n",
			math.Exp(geoSpeedup/float64(count)))
	}

	// LaTeX table for paper.
	fmt.Println()
	fmt.Println("=== LaTeX Table (Fair In-Process Comparison) ===")
	fmt.Println()
	fmt.Println(`\begin{table*}[t]`)
	fmt.Println(`\centering`)
	fmt.Println(`\caption{Lossless compression comparison: MIC vs HTJ2K (OpenJPH).`)
	fmt.Println(`         Both codecs are benchmarked as in-process library calls`)
	fmt.Println(`         with in-memory I/O (no subprocess or disk overhead).`)
	fmt.Println(`         Decompression throughput is best of 10 runs.}`)
	fmt.Println(`\label{tab:htj2k-fair-comparison}`)
	fmt.Println(`\begin{tabular}{lrrrrrrr}`)
	fmt.Println(`\hline`)
	fmt.Println(`\textbf{Image} & \textbf{Size (MB)} & \textbf{MIC ratio} & \textbf{HTJ2K ratio} & \textbf{MIC decomp (ms)} & \textbf{HTJ2K decomp (ms)} & \textbf{MIC (GB/s)} & \textbf{Speedup} \\`)
	fmt.Println(`\hline`)
	for _, r := range results {
		origMB := float64(r.origBytes) / (1 << 20)
		micRatio := float64(r.origBytes) / float64(r.micBytes)
		htj2kRatio := float64(r.origBytes) / float64(r.htj2kBytes)
		micGBs := (float64(r.origBytes) / (1 << 30)) / (r.micDecompMs / 1000.0)
		htj2kGBs := (float64(r.origBytes) / (1 << 30)) / (r.htj2kDecompMs / 1000.0)
		speedup := micGBs / htj2kGBs
		fmt.Printf("%-6s & %.2f & %.2f$\\times$ & %.2f$\\times$ & %.2f & %.2f & %.2f & %.2f$\\times$ \\\\\n",
			r.name, origMB, micRatio, htj2kRatio, r.micDecompMs, r.htj2kDecompMs, micGBs, speedup)
	}
	fmt.Println(`\hline`)
	fmt.Println(`\end{tabular}`)
	fmt.Println(`\end{table*}`)
}

// BenchmarkHTJ2KFairDecomp runs Go benchmarks for both MIC and HTJ2K decompression
// using in-process library calls for both.
func BenchmarkHTJ2KFairDecomp(b *testing.B) {
	for _, ti := range testImages {
		byteData, shortData, maxShort, cols, rows := loadImage(ti)
		if len(shortData) == 0 {
			continue
		}
		origBytes := len(byteData)
		bitDepth := bits.Len16(maxShort)
		if bitDepth == 0 {
			bitDepth = 1
		}

		// Pre-compress with both codecs.
		var drc mic.DeltaRleCompressU16
		deltaComp, _ := drc.Compress(shortData, cols, rows, maxShort)
		var s mic.ScratchU16
		fseComp, _ := mic.FSECompressU16TwoState(deltaComp, &s)

		htj2kComp, err := CompressU16(shortData, cols, rows, bitDepth)
		if err != nil {
			b.Logf("Skipping %s: HTJ2K compress error: %v", ti.name, err)
			continue
		}

		micRatio := float64(origBytes) / float64(len(fseComp))
		htj2kRatio := float64(origBytes) / float64(len(htj2kComp))

		b.Run("MIC/"+ti.name, func(b *testing.B) {
			b.SetBytes(int64(origBytes))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				var s2 mic.ScratchU16
				rleData, _ := mic.FSEDecompressU16TwoState(fseComp, &s2)
				var drd mic.DeltaRleDecompressU16
				drd.Decompress(rleData, cols, rows)
			}
			b.ReportMetric(micRatio, "ratio")
			b.ReportMetric(float64(origBytes)/(1<<20), "orig-MB")
		})

		b.Run("HTJ2K/"+ti.name, func(b *testing.B) {
			b.SetBytes(int64(origBytes))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				DecompressU16(htj2kComp, cols, rows)
			}
			b.ReportMetric(htj2kRatio, "ratio")
			b.ReportMetric(float64(origBytes)/(1<<20), "orig-MB")
		})
	}
}
