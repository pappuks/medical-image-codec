// Copyright 2021 Kuldeep Singh
// This source code is licensed under a MIT-style
// license that can be found in the LICENSE file.

// HTJ2K Comparison Framework
//
// Compares MIC (Delta+RLE+FSE two-state) against HTJ2K (lossless) using OpenJPH.
// Requires ojph_compress and ojph_expand in PATH with DYLD_LIBRARY_PATH set.
//
// Run with:
//
//	go test -run TestHTJ2KComparison -v
package mic

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// ojphLibPath is the default location for the OpenJPH shared library on macOS.
// On Linux this is typically not needed (libraries are in standard paths).
const ojphLibPath = "/usr/local/lib"

// htj2kResult holds timing and size metrics for one codec run on one image.
type htj2kResult struct {
	name             string
	originalBytes    int
	compressedBytes  int
	ratio            float64
	compressMs       float64
	decompressMs     float64
	decompressGBs    float64
}

// writeU16PGM writes a 16-bit grayscale PGM file (P5 format, big-endian).
// This is the input format accepted by ojph_compress.
func writeU16PGM(path string, pixels []uint16, width, height int, maxVal uint16) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	fmt.Fprintf(f, "P5\n%d %d\n%d\n", width, height, maxVal)

	buf := make([]byte, len(pixels)*2)
	for i, p := range pixels {
		binary.BigEndian.PutUint16(buf[i*2:], p)
	}
	_, err = f.Write(buf)
	return err
}

// ojphEnv returns the environment with DYLD_LIBRARY_PATH set for OpenJPH on macOS.
func ojphEnv() []string {
	env := os.Environ()
	// Only add DYLD_LIBRARY_PATH if the library is in a non-standard location.
	// On Linux with standard install this is typically not needed.
	if _, err := os.Stat(ojphLibPath); err == nil {
		env = append(env, "DYLD_LIBRARY_PATH="+ojphLibPath)
	}
	return env
}

// checkOJPHAvailable returns an error if ojph_compress/ojph_expand are not usable.
func checkOJPHAvailable() error {
	cmd := exec.Command("ojph_compress")
	cmd.Env = ojphEnv()
	// ojph_compress exits non-zero with no args but prints usage — that's OK.
	err := cmd.Run()
	_ = err
	// If the binary is missing, Run returns exec.ErrNotFound.
	if _, lookErr := exec.LookPath("ojph_compress"); lookErr != nil {
		return fmt.Errorf("ojph_compress not found in PATH")
	}
	if _, lookErr := exec.LookPath("ojph_expand"); lookErr != nil {
		return fmt.Errorf("ojph_expand not found in PATH")
	}
	return nil
}

// benchHTJ2K runs lossless HTJ2K compression and decompression on a single image.
// decompRuns controls how many decompression iterations are averaged for timing.
func benchHTJ2K(pixels []uint16, width, height int, maxVal uint16, decompRuns int, tmpDir string) (compMs, decompMs float64, compressedBytes int, err error) {
	pgmIn := filepath.Join(tmpDir, "input.pgm")
	jphOut := filepath.Join(tmpDir, "compressed.jph")
	pgmOut := filepath.Join(tmpDir, "decompressed.pgm")

	if err = writeU16PGM(pgmIn, pixels, width, height, maxVal); err != nil {
		return
	}

	// Compression (single run — wall-clock includes I/O, but so does decomp).
	compStart := time.Now()
	cmd := exec.Command("ojph_compress",
		"-i", pgmIn,
		"-o", jphOut,
		"-reversible", "true",
	)
	cmd.Env = ojphEnv()
	if out, cmdErr := cmd.CombinedOutput(); cmdErr != nil {
		err = fmt.Errorf("ojph_compress: %v\n%s", cmdErr, out)
		return
	}
	compMs = float64(time.Since(compStart).Microseconds()) / 1000.0

	fi, statErr := os.Stat(jphOut)
	if statErr != nil {
		err = statErr
		return
	}
	compressedBytes = int(fi.Size())

	// Decompression — run decompRuns times, report minimum (best-case throughput).
	minDecomp := time.Duration(math.MaxInt64)
	for i := 0; i < decompRuns; i++ {
		_ = os.Remove(pgmOut)
		start := time.Now()
		cmd = exec.Command("ojph_expand",
			"-i", jphOut,
			"-o", pgmOut,
		)
		cmd.Env = ojphEnv()
		if out, cmdErr := cmd.CombinedOutput(); cmdErr != nil {
			err = fmt.Errorf("ojph_expand: %v\n%s", cmdErr, out)
			return
		}
		elapsed := time.Since(start)
		if elapsed < minDecomp {
			minDecomp = elapsed
		}
	}
	decompMs = float64(minDecomp.Microseconds()) / 1000.0

	// Cleanup temp files.
	os.Remove(pgmIn)
	os.Remove(jphOut)
	os.Remove(pgmOut)
	return
}

// benchMIC runs MIC (Delta+RLE+FSE two-state) compression and decompression.
// decompRuns controls how many decompression iterations are averaged for timing.
func benchMIC(pixels []uint16, byteLen, width, height int, maxVal uint16, decompRuns int) (compMs, decompMs float64, compressedBytes int, err error) {
	// Compression.
	compStart := time.Now()
	var drc DeltaRleCompressU16
	deltaComp, compErr := drc.Compress(pixels, width, height, maxVal)
	if compErr != nil {
		err = compErr
		return
	}
	var s ScratchU16
	fseComp, compErr := FSECompressU16TwoState(deltaComp, &s)
	if compErr != nil {
		err = compErr
		return
	}
	compMs = float64(time.Since(compStart).Microseconds()) / 1000.0
	compressedBytes = len(fseComp)

	// Decompression — run decompRuns times, report minimum.
	minDecomp := time.Duration(math.MaxInt64)
	for i := 0; i < decompRuns; i++ {
		start := time.Now()
		var s2 ScratchU16
		rleData, _ := FSEDecompressU16TwoState(fseComp, &s2)
		var drd DeltaRleDecompressU16
		drd.Decompress(rleData, width, height)
		elapsed := time.Since(start)
		if elapsed < minDecomp {
			minDecomp = elapsed
		}
	}
	decompMs = float64(minDecomp.Microseconds()) / 1000.0

	return
}

// TestHTJ2KComparison runs MIC and HTJ2K on all test images and prints a comparison table.
// This is intended to generate data for the paper.
//
// Run with:
//
//	go test -run TestHTJ2KComparison -v -timeout 300s
func TestHTJ2KComparison(t *testing.T) {
	if err := checkOJPHAvailable(); err != nil {
		t.Skipf("Skipping HTJ2K comparison: %v", err)
	}

	tmpDir, err := os.MkdirTemp("", "mic-htj2k-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	const decompRuns = 5

	type row struct {
		name         string
		width, height int
		origMB       float64
		micRatio     float64
		htj2kRatio   float64
		micCompMs    float64
		htj2kCompMs  float64
		micDecompMs  float64
		htj2kDecompMs float64
		micGBs       float64
		htj2kGBs     float64
	}

	var results []row

	for _, tf := range testFiles {
		byteData, shortData, maxShort, cols, rows := SetupTests(tf)
		origBytes := len(byteData)

		micComp, micDecomp, micCompBytes, micErr := benchMIC(shortData, origBytes, cols, rows, maxShort, decompRuns)
		if micErr != nil {
			t.Errorf("%s: MIC error: %v", tf.name, micErr)
			continue
		}

		htj2kComp, htj2kDecomp, htj2kBytes, htj2kErr := benchHTJ2K(shortData, cols, rows, maxShort, decompRuns, tmpDir)
		if htj2kErr != nil {
			t.Errorf("%s: HTJ2K error: %v", tf.name, htj2kErr)
			continue
		}

		origMB := float64(origBytes) / (1 << 20)
		micGBs := (float64(origBytes) / (1 << 30)) / (micDecomp / 1000.0)
		htj2kGBs := (float64(origBytes) / (1 << 30)) / (htj2kDecomp / 1000.0)

		results = append(results, row{
			name:          tf.name,
			width:         cols,
			height:        rows,
			origMB:        origMB,
			micRatio:      float64(origBytes) / float64(micCompBytes),
			htj2kRatio:    float64(origBytes) / float64(htj2kBytes),
			micCompMs:     micComp,
			htj2kCompMs:   htj2kComp,
			micDecompMs:   micDecomp,
			htj2kDecompMs: htj2kDecomp,
			micGBs:        micGBs,
			htj2kGBs:      htj2kGBs,
		})
	}

	// Print formatted comparison table.
	fmt.Println()
	fmt.Println("=== MIC vs HTJ2K Comparison (Lossless) ===")
	fmt.Println()
	fmt.Printf("Note: MIC timings are in-process (no I/O). HTJ2K timings include process startup + I/O.\n")
	fmt.Printf("Decompression is best of %d runs.\n", decompRuns)
	fmt.Println()

	// Header.
	fmt.Printf("%-6s  %9s  %8s  %7s  %7s  %10s  %10s  %10s  %10s  %8s  %8s\n",
		"Image", "Orig (MB)", "WxH",
		"MIC-r", "HTJ2K-r",
		"MIC-c(ms)", "HTJ2K-c(ms)",
		"MIC-d(ms)", "HTJ2K-d(ms)",
		"MIC GB/s", "HTJ2K GB/s",
	)
	sep := "------  ---------  --------  -------  -------  ----------  ----------  ----------  ----------  --------  ----------"
	fmt.Println(sep)

	for _, r := range results {
		fmt.Printf("%-6s  %9.2f  %4dx%-4d  %7.2f  %7.2f  %10.1f  %10.1f  %10.2f  %10.2f  %8.2f  %10.2f\n",
			r.name, r.origMB, r.width, r.height,
			r.micRatio, r.htj2kRatio,
			r.micCompMs, r.htj2kCompMs,
			r.micDecompMs, r.htj2kDecompMs,
			r.micGBs, r.htj2kGBs,
		)
	}

	fmt.Println(sep)
	fmt.Println()
	fmt.Println("Columns:")
	fmt.Println("  MIC-r    : MIC compression ratio (higher = better)")
	fmt.Println("  HTJ2K-r  : HTJ2K lossless compression ratio (higher = better)")
	fmt.Println("  MIC-c    : MIC compression time (ms)")
	fmt.Println("  HTJ2K-c  : HTJ2K compression time including process startup (ms)")
	fmt.Println("  MIC-d    : MIC decompression time (ms)")
	fmt.Println("  HTJ2K-d  : HTJ2K decompression time including process startup (ms)")
	fmt.Println("  MIC GB/s : MIC decompression throughput")
	fmt.Println("  HTJ2K GB/s: HTJ2K decompression throughput (includes process startup overhead)")

	// Also print LaTeX table for direct inclusion in paper.
	fmt.Println()
	fmt.Println("=== LaTeX Table ===")
	fmt.Println()
	fmt.Println(`\begin{table}[h]`)
	fmt.Println(`\centering`)
	fmt.Println(`\caption{Lossless compression comparison: MIC vs HTJ2K (OpenJPH) on medical images.`)
	fmt.Println(`         MIC timings are in-process; HTJ2K timings include process startup and I/O.`)
	fmt.Println(`         Decompression throughput is best of 5 runs.}`)
	fmt.Println(`\label{tab:htj2k-comparison}`)
	fmt.Println(`\begin{tabular}{lrrrrrr}`)
	fmt.Println(`\hline`)
	fmt.Println(`\textbf{Image} & \textbf{Size (MB)} & \textbf{MIC ratio} & \textbf{HTJ2K ratio} & \textbf{MIC decomp (GB/s)} & \textbf{HTJ2K decomp (GB/s)} & \textbf{MIC advantage} \\`)
	fmt.Println(`\hline`)
	for _, r := range results {
		advantage := r.micGBs / r.htj2kGBs
		fmt.Printf("%-6s & %.2f & %.2f$\\times$ & %.2f$\\times$ & %.2f & %.2f & %.1f$\\times$ \\\\\n",
			r.name, r.origMB, r.micRatio, r.htj2kRatio, r.micGBs, r.htj2kGBs, advantage)
	}
	fmt.Println(`\hline`)
	fmt.Println(`\end{tabular}`)
	fmt.Println(`\end{table}`)
}

// BenchmarkMICvsHTJ2K runs Go benchmark timing for MIC decompression
// alongside a single HTJ2K compress+decompress for ratio comparison.
// This gives more accurate MIC timing than TestHTJ2KComparison.
//
// Run with:
//
//	go test -run=^$ -bench=BenchmarkMICvsHTJ2K -benchtime=10x -v
func BenchmarkMICvsHTJ2K(b *testing.B) {
	if err := checkOJPHAvailable(); err != nil {
		b.Skipf("Skipping: %v", err)
	}

	tmpDir, err := os.MkdirTemp("", "mic-htj2k-bench-*")
	if err != nil {
		b.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	for _, tf := range testFiles {
		b.Run(tf.name, func(b *testing.B) {
			byteData, shortData, maxShort, cols, rows := SetupTests(tf)
			origBytes := len(byteData)

			// Pre-compress MIC (two-state FSE).
			var drc DeltaRleCompressU16
			deltaComp, _ := drc.Compress(shortData, cols, rows, maxShort)
			var s ScratchU16
			fseComp, _ := FSECompressU16TwoState(deltaComp, &s)

			// Pre-compress HTJ2K (once, for ratio).
			htj2kComp, _, htj2kBytes, htj2kErr := benchHTJ2K(shortData, cols, rows, maxShort, 1, tmpDir)
			if htj2kErr != nil {
				b.Skipf("HTJ2K error: %v", htj2kErr)
			}

			micRatio := float64(origBytes) / float64(len(fseComp))
			htj2kRatio := float64(origBytes) / float64(htj2kBytes)

			b.SetBytes(int64(origBytes))
			b.ResetTimer()

			// Benchmark MIC decompression (two-state FSE).
			for i := 0; i < b.N; i++ {
				var s2 ScratchU16
				rleData, _ := FSEDecompressU16TwoState(fseComp, &s2)
				var drd DeltaRleDecompressU16
				drd.Decompress(rleData, cols, rows)
			}

			b.ReportMetric(micRatio, "MIC-ratio")
			b.ReportMetric(htj2kRatio, "HTJ2K-ratio")
			b.ReportMetric(htj2kComp, "HTJ2K-comp-ms")
			b.ReportMetric(float64(origBytes)/(1<<20), "orig-MB")
		})
	}
}
