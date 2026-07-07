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
	"sort"
	"strconv"
	"time"

	"github.com/suyashkumar/dicom"
	"github.com/suyashkumar/dicom/pkg/tag"

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

// cineDataset mirrors cmd/mic-compress/main.go's cineDatasets: multi-frame DICOM
// sources whose every frame is reference-encoded as an independent single-frame
// image (<id>_f<NNN>.{jph,jls,jxl}). Duplicated on purpose to keep this
// cgo-tagged command decoupled from mic-compress (same rationale as testImages).
type cineDataset struct {
	id        string
	file      string
	dir       string
	maxFrames int // 0 = all frames
}

var cineDatasets = []cineDataset{
	{id: "CINE_MRCARD", file: "testdata/multiframe/MR-MONO2-8-16x-heart.dcm"}, // cardiac cine MR, 16f
	{id: "CINE_XA", file: "testdata/multiframe/XA-MONO2-8-12x-catheter.dcm"},  // XA coronary angiography, 12f
	{id: "CINE_NM", file: "testdata/multiframe/NM-MONO2-16-13x-heart.dcm"},    // nuclear medicine gated heart, 13f
	{id: "CINE_EMR", file: "testdata/multiframe/emri_small.dcm"},              // enhanced/volumetric MR, 10f
	{id: "CINE_ECT", file: "testdata/multiframe/eCT_Supplemental.dcm"},        // enhanced CT, 2f
	{id: "CINE_TOMO", file: "testdata/Series 73200000 [MG - R CC Breast Tomosynthesis Image]/1.3.6.1.4.1.5962.99.1.2280943358.716200484.1363785608958.647.0.dcm", maxFrames: 16},
	{id: "CINE_CTMULTI", dir: "testdata/0acbebb8d463b4b9ca88cf38431aac69", maxFrames: 16},
}

// readDicomFrames extracts every native (uncompressed) frame from a multi-frame
// DICOM as little-endian uint16 pixels. Mirrors readDicomMultiFrame in
// cmd/mic-compress/main.go.
func readDicomFrames(fileName string) ([][]uint16, int, int, error) {
	ds, err := dicom.ParseFile(fileName, nil)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("parse DICOM: %w", err)
	}
	pde, err := ds.FindElementByTag(tag.PixelData)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("find pixel data: %w", err)
	}
	info := dicom.MustGetPixelDataInfo(pde.Value)
	if len(info.Frames) == 0 {
		return nil, 0, 0, fmt.Errorf("no frames")
	}
	f0, err := info.Frames[0].GetNativeFrame()
	if err != nil {
		return nil, 0, 0, fmt.Errorf("frame 0: %w", err)
	}
	w, h := f0.Cols, f0.Rows
	frames := make([][]uint16, len(info.Frames))
	for i, fr := range info.Frames {
		nf, err := fr.GetNativeFrame()
		if err != nil {
			return nil, 0, 0, fmt.Errorf("frame %d: %w", i, err)
		}
		px := make([]uint16, w*h)
		for j := 0; j < len(nf.Data); j++ {
			px[j] = uint16(nf.Data[j][0])
		}
		frames[i] = px
	}
	return frames, w, h, nil
}

// readDicomSeries reads all single-frame DICOM files from a directory, ordered
// by InstanceNumber. Mirrors cmd/mic-compress/main.go's readDicomSeries
// (duplicated on purpose — see the package doc comment above).
func readDicomSeries(seriesDir string) ([][]uint16, int, int, error) {
	entries, err := os.ReadDir(seriesDir)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("read directory: %w", err)
	}

	type dicomEntry struct {
		path           string
		instanceNumber int
	}
	var dcmFiles []dicomEntry

	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".dcm" {
			continue
		}
		fpath := filepath.Join(seriesDir, e.Name())
		dataset, err := dicom.ParseFile(fpath, nil)
		if err != nil {
			return nil, 0, 0, fmt.Errorf("parse %s: %w", e.Name(), err)
		}
		el, err := dataset.FindElementByTag(tag.InstanceNumber)
		if err != nil {
			return nil, 0, 0, fmt.Errorf("no InstanceNumber in %s: %w", e.Name(), err)
		}
		instNum, err := strconv.Atoi(fmt.Sprintf("%v", el.Value.GetValue().([]string)[0]))
		if err != nil {
			return nil, 0, 0, fmt.Errorf("parse InstanceNumber in %s: %w", e.Name(), err)
		}
		dcmFiles = append(dcmFiles, dicomEntry{path: fpath, instanceNumber: instNum})
	}

	if len(dcmFiles) == 0 {
		return nil, 0, 0, fmt.Errorf("no .dcm files in %s", seriesDir)
	}

	sort.Slice(dcmFiles, func(i, j int) bool {
		return dcmFiles[i].instanceNumber < dcmFiles[j].instanceNumber
	})

	var width, height int
	frames := make([][]uint16, len(dcmFiles))

	for f, de := range dcmFiles {
		dataset, err := dicom.ParseFile(de.path, nil)
		if err != nil {
			return nil, 0, 0, fmt.Errorf("parse frame %d: %w", f, err)
		}
		pixelDataElement, err := dataset.FindElementByTag(tag.PixelData)
		if err != nil {
			return nil, 0, 0, fmt.Errorf("no pixel data in frame %d: %w", f, err)
		}
		pixelDataInfo := dicom.MustGetPixelDataInfo(pixelDataElement.Value)
		nativeFrame, err := pixelDataInfo.Frames[0].GetNativeFrame()
		if err != nil {
			return nil, 0, 0, fmt.Errorf("get native frame %d: %w", f, err)
		}

		if f == 0 {
			width = nativeFrame.Cols
			height = nativeFrame.Rows
		} else if nativeFrame.Cols != width || nativeFrame.Rows != height {
			return nil, 0, 0, fmt.Errorf("frame %d dimension mismatch: %dx%d vs %dx%d",
				f, nativeFrame.Cols, nativeFrame.Rows, width, height)
		}

		pixels := make([]uint16, width*height)
		for j := 0; j < len(nativeFrame.Data); j++ {
			pixels[j] = uint16(nativeFrame.Data[j][0])
		}
		frames[f] = pixels
	}

	return frames, width, height, nil
}

func cineSourcePath(ds cineDataset) string {
	if ds.dir != "" {
		return ds.dir
	}
	return ds.file
}

func readCineFrames(ds cineDataset) ([][]uint16, int, int, error) {
	if ds.dir != "" {
		return readDicomSeries(ds.dir)
	}
	return readDicomFrames(ds.file)
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

// encodeImageRefs reference-encodes one image/frame with all three codecs,
// natively roundtrip-verifying each before writing <name>.{jph,jls,jxl}. Returns
// the manifest record and the number of codec failures encountered.
func encodeImageRefs(name string, pixels []uint16, cols, rows int) (imageManifest, int) {
	bitDepth := bits.Len16(maxU16(pixels))
	// Floor the declared depth at 9 bits. CharLS packs <=8-bit samples as one
	// byte while our wrapper feeds uint16 (→ roundtrip mismatch), and libjxl
	// rejects bits_per_sample=8 with UINT16 input (→ encode rc=-1). The 8-bit
	// cine frames (cardiac MR, XA, NM) have values that fit in 9 bits, so
	// declaring 9-bit keeps every reference codestream bit-exact lossless. The
	// single-frame corpus is all >=12-bit, so this is a no-op there.
	if bitDepth < 9 {
		bitDepth = 9
	}
	rec := imageManifest{Width: cols, Height: rows, BitDepth: bitDepth}
	failures := 0

	if e, err := encodeVerifyWrite("HTJ2K", filepath.Join(outDir, name+".jph"), pixels, cols, rows,
		func() ([]byte, error) { return ojph.CompressU16(pixels, cols, rows, bitDepth) },
		func(c []byte) ([]uint16, error) { return ojph.DecompressU16(c, cols, rows) },
	); err != nil {
		fmt.Fprintf(os.Stderr, "  %v\n", err)
		failures++
	} else {
		rec.HTJ2K = e
		fmt.Printf("  HTJ2K:   %7d bytes -> %s.jph\n", e.Bytes, name)
	}

	if e, err := encodeVerifyWrite("JPEG-LS", filepath.Join(outDir, name+".jls"), pixels, cols, rows,
		func() ([]byte, error) { return ojph.CharlsCompressU16(pixels, cols, rows, bitDepth) },
		func(c []byte) ([]uint16, error) { return ojph.CharlsDecompressU16(c, cols, rows) },
	); err != nil {
		fmt.Fprintf(os.Stderr, "  %v\n", err)
		failures++
	} else {
		rec.JPEGLS = e
		fmt.Printf("  JPEG-LS: %7d bytes -> %s.jls\n", e.Bytes, name)
	}

	if e, err := encodeVerifyWrite("JPEG-XL", filepath.Join(outDir, name+".jxl"), pixels, cols, rows,
		func() ([]byte, error) {
			return ojph.JXLCompressU16(pixels, cols, rows, bitDepth, ojph.JXLDefaultEffort)
		},
		func(c []byte) ([]uint16, error) { return ojph.JXLDecompressU16(c, cols, rows) },
	); err != nil {
		fmt.Fprintf(os.Stderr, "  %v\n", err)
		failures++
	} else {
		rec.JXL = e
		fmt.Printf("  JPEG-XL: %7d bytes -> %s.jxl\n", e.Bytes, name)
	}

	return rec, failures
}

func maxU16(px []uint16) uint16 {
	var m uint16
	for _, v := range px {
		if v > m {
			m = v
		}
	}
	return m
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

		rec, f := encodeImageRefs(img.name, pixels, img.cols, img.rows)
		failures += f
		m.Images[img.name] = rec
	}

	// Cine datasets: reference-encode every frame of each multi-frame DICOM as
	// an independent single-frame image (<id>_f<NNN>), so the browser benchmark
	// can decode HTJ2K/JPEG-LS live per frame alongside MIC (JXL informational).
	for _, ds := range cineDatasets {
		src := cineSourcePath(ds)
		if _, err := os.Stat(src); os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "  skip cine %s: %s not found (run testdata/multiframe/fetch-cine-sources.sh)\n", ds.id, src)
			continue
		}
		frames, w, h, err := readCineFrames(ds)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  error reading cine %s: %v\n", ds.id, err)
			continue
		}
		n := len(frames)
		if ds.maxFrames > 0 && n > ds.maxFrames {
			n = ds.maxFrames
		}
		fmt.Printf("Reference-encoding cine %s: %d frames %dx%d...\n", ds.id, n, w, h)
		for i := 0; i < n; i++ {
			name := fmt.Sprintf("%s_f%03d", ds.id, i)
			rec, f := encodeImageRefs(name, frames[i], w, h)
			failures += f
			m.Images[name] = rec
		}
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
