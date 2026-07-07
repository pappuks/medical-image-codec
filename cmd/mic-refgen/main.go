// Copyright 2021 Kuldeep Singh
// This source code is licensed under a MIT-style
// license that can be found in the LICENSE file.

//go:build cgo_ojph

// mic-refgen generates reference-codec test files (HTJ2K .jph, JPEG-LS .jls,
// JPEG-XL .jxl) for the single-frame test corpus, so the browser PACS
// benchmark dashboard (web/pacs-dashboard.html) can decode them LIVE with
// vendored WASM decoders and compare against MIC.
//
// It is a separate, explicitly opt-in step from `mic-compress -testdata`:
// mic-compress intentionally has no cgo build tag so it stays buildable
// without native libs installed, whereas mic-refgen requires libopenjph,
// libcharls, and libjxl (same opt-in-cgo pattern as ojph.BenchmarkAllCodecs).
//
// Usage (from repo root):
//
//	go run -tags cgo_ojph ./cmd/mic-refgen
//
// Prereqs: libopenjph, libcharls, libjxl installed (see CLAUDE.md Build & Test).
// Every reference file is bit-exact roundtrip-verified natively BEFORE it is
// written — a broken reference file must never reach web/testdata/, because
// the browser has no independent way to know it's wrong.
package main

import (
	"encoding/json"
	"fmt"
	"math/bits"
	"os"
	"path/filepath"
	"time"

	"mic/ojph"
)

// testImage mirrors the single-frame corpus in cmd/mic-compress/main.go.
//
// TODO(mic-refgen): this list is intentionally duplicated from
// cmd/mic-compress/main.go's `testImages` to keep this cgo-tagged command fully
// decoupled from mic-compress's internals. If the two lists ever disagree,
// extract cmd/internal/testimages and import it from both commands.
type testImage struct {
	name string
	file string
	cols int
	rows int
}

var testImages = []testImage{
	{name: "MR", file: "testdata/MR_256_256_image.bin", cols: 256, rows: 256},
	{name: "CT", file: "testdata/CT_512_512_image.bin", cols: 512, rows: 512},
	{name: "CR", file: "testdata/CR_1760_2140_image.bin", cols: 1760, rows: 2140},
	{name: "MG1", file: "testdata/MG_image_bin2.bin", cols: 1996, rows: 2457},
	{name: "MG2", file: "testdata/MG_Image_2_frame.bin", cols: 1996, rows: 2457},
	{name: "MG3", file: "testdata/MG1.RAW", cols: 3064, rows: 4774},
	{name: "DX_HAND", file: "testdata/expanded/DX_HAND_1410_1480_image.bin", cols: 1410, rows: 1480},
	{name: "PET1", file: "testdata/expanded/PET_NSCLC1_256_256_image.bin", cols: 256, rows: 256},
}

const outDir = "web/testdata"

// codecEntry is one reference codec's result for one image in the manifest.
type codecEntry struct {
	File              string `json:"file"`
	Bytes             int    `json:"bytes"`
	NativeRoundtripOK bool   `json:"nativeRoundtripOK"`
}

// imageManifest is the per-image manifest record.
type imageManifest struct {
	Width    int         `json:"width"`
	Height   int         `json:"height"`
	BitDepth int         `json:"bitDepth"`
	HTJ2K    *codecEntry `json:"htj2k,omitempty"`
	JPEGLS   *codecEntry `json:"jpegls,omitempty"`
	JXL      *codecEntry `json:"jxl,omitempty"`
}

type manifest struct {
	GeneratedAt string                   `json:"generatedAt"`
	Effort      int                      `json:"jxlEffort"`
	Images      map[string]imageManifest `json:"images"`
}

// loadU16LE reads a raw little-endian uint16 image and returns the pixels and
// the maximum value (used to derive the effective bit depth).
func loadU16LE(path string, cols, rows int) ([]uint16, uint16, error) {
	byteData, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, err
	}
	want := cols * rows * 2
	// Tolerate a short raw file by zero-padding the tail, matching how
	// cmd/mic-compress reads these same .bin files (it iterates over available
	// bytes only). This keeps the reference files consistent with the .mic
	// files: both encode the identical (possibly zero-padded) pixel buffer.
	n := len(byteData)
	if n > want {
		n = want
	} else if n < want {
		fmt.Fprintf(os.Stderr, "  note: %s is short (%d of %d bytes) — zero-padding tail to match mic-compress\n", path, n, want)
	}
	pixels := make([]uint16, cols*rows)
	var maxValue uint16
	for i := 0; i+1 < n; i += 2 {
		v := uint16(byteData[i]) | (uint16(byteData[i+1]) << 8)
		pixels[i/2] = v
		if v > maxValue {
			maxValue = v
		}
	}
	return pixels, maxValue, nil
}

// equalU16 reports whether two pixel slices are bit-for-bit identical.
func equalU16(a, b []uint16) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// encodeVerifyWrite compresses with encode, verifies the native decode is
// bit-exact against src, and writes the codestream to outPath. Returns the
// manifest entry. Any roundtrip mismatch is a hard error — nothing is written.
func encodeVerifyWrite(
	label, outPath string,
	src []uint16, cols, rows int,
	encode func() ([]byte, error),
	decode func(comp []byte) ([]uint16, error),
) (*codecEntry, error) {
	comp, err := encode()
	if err != nil {
		return nil, fmt.Errorf("%s encode: %w", label, err)
	}
	decoded, err := decode(comp)
	if err != nil {
		return nil, fmt.Errorf("%s native roundtrip decode: %w", label, err)
	}
	if !equalU16(src, decoded) {
		return nil, fmt.Errorf("%s native roundtrip MISMATCH (%dx%d) — refusing to write %s", label, cols, rows, outPath)
	}
	if err := os.WriteFile(outPath, comp, 0644); err != nil {
		return nil, fmt.Errorf("%s write %s: %w", label, outPath, err)
	}
	return &codecEntry{
		File:              filepath.Base(outPath),
		Bytes:             len(comp),
		NativeRoundtripOK: true,
	}, nil
}

func main() {
	if err := os.MkdirAll(outDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "mkdir %s: %v\n", outDir, err)
		os.Exit(1)
	}

	m := manifest{
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Effort:      ojph.JXLDefaultEffort,
		Images:      map[string]imageManifest{},
	}

	failures := 0
	for _, img := range testImages {
		pixels, maxValue, err := loadU16LE(img.file, img.cols, img.rows)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  skip %s: %v\n", img.name, err)
			continue
		}
		bitDepth := bits.Len16(maxValue)
		if bitDepth < 1 {
			bitDepth = 1
		}
		fmt.Printf("Reference-encoding %s (%dx%d, %d-bit)...\n", img.name, img.cols, img.rows, bitDepth)

		rec := imageManifest{Width: img.cols, Height: img.rows, BitDepth: bitDepth}

		// HTJ2K (.jph)
		if e, err := encodeVerifyWrite("HTJ2K", filepath.Join(outDir, img.name+".jph"), pixels, img.cols, img.rows,
			func() ([]byte, error) { return ojph.CompressU16(pixels, img.cols, img.rows, bitDepth) },
			func(c []byte) ([]uint16, error) { return ojph.DecompressU16(c, img.cols, img.rows) },
		); err != nil {
			fmt.Fprintf(os.Stderr, "  %v\n", err)
			failures++
		} else {
			rec.HTJ2K = e
			fmt.Printf("  HTJ2K:   %7d bytes -> %s.jph\n", e.Bytes, img.name)
		}

		// JPEG-LS (.jls)
		if e, err := encodeVerifyWrite("JPEG-LS", filepath.Join(outDir, img.name+".jls"), pixels, img.cols, img.rows,
			func() ([]byte, error) { return ojph.CharlsCompressU16(pixels, img.cols, img.rows, bitDepth) },
			func(c []byte) ([]uint16, error) { return ojph.CharlsDecompressU16(c, img.cols, img.rows) },
		); err != nil {
			fmt.Fprintf(os.Stderr, "  %v\n", err)
			failures++
		} else {
			rec.JPEGLS = e
			fmt.Printf("  JPEG-LS: %7d bytes -> %s.jls\n", e.Bytes, img.name)
		}

		// JPEG-XL (.jxl)
		if e, err := encodeVerifyWrite("JPEG-XL", filepath.Join(outDir, img.name+".jxl"), pixels, img.cols, img.rows,
			func() ([]byte, error) {
				return ojph.JXLCompressU16(pixels, img.cols, img.rows, bitDepth, ojph.JXLDefaultEffort)
			},
			func(c []byte) ([]uint16, error) { return ojph.JXLDecompressU16(c, img.cols, img.rows) },
		); err != nil {
			fmt.Fprintf(os.Stderr, "  %v\n", err)
			failures++
		} else {
			rec.JXL = e
			fmt.Printf("  JPEG-XL: %7d bytes -> %s.jxl\n", e.Bytes, img.name)
		}

		m.Images[img.name] = rec
	}

	manifestPath := filepath.Join(outDir, "refcodecs-manifest.json")
	blob, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "marshal manifest: %v\n", err)
		os.Exit(1)
	}
	if err := os.WriteFile(manifestPath, blob, 0644); err != nil {
		fmt.Fprintf(os.Stderr, "write %s: %v\n", manifestPath, err)
		os.Exit(1)
	}
	fmt.Printf("\nWrote %s (%d images)\n", manifestPath, len(m.Images))

	if failures > 0 {
		fmt.Fprintf(os.Stderr, "\n%d reference-codec encode/roundtrip failure(s) — see messages above.\n", failures)
		os.Exit(1)
	}
}
