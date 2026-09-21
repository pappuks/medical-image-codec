// mic-pacs-encode compresses PACS studies using MIC and PICS codecs.
//
// This encoder processes 80 Tier-A studies from pacs-data/raw-src/<id>/,
// producing MIC1/MIC2/MIC3/MICR and PICS artifacts. Each artifact is
// encode → decode → verify before writing, ensuring roundtrip correctness
// per the design in docs/pacs-encode-design.md.
//
// Phase 1: grayscale paths only (SINGLE_FRAME, SERIES_DIR, MULTIFRAME_FILE).
//
// Usage:
//
//	go build ./cmd/mic-pacs-encode
//	./mic-pacs-encode [-workers 4] [-force]
package main

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"mic"

	"github.com/suyashkumar/dicom"
	"github.com/suyashkumar/dicom/pkg/frame"
	"github.com/suyashkumar/dicom/pkg/tag"
)

// ===== TYPES =====

type manifestEntry struct {
	ID           string `json:"id"`
	Tier         string `json:"tier"`
	License      string `json:"license"`
	Attribution  string `json:"attribution"`
	Bytes        int    `json:"bytes"`
	SHA256Rep    string `json:"sha256_representative"`
}

type manifest struct {
	Entries []manifestEntry `json:"entries"`
}

type encodedArtifact struct {
	Codec          string      `json:"codec"`
	Variant        string      `json:"variant"`
	Container      string      `json:"container"`
	Route          string      `json:"route"`
	Key            string      `json:"key"`
	Bytes          int         `json:"bytes"`
	RawBytes       int         `json:"rawBytes"`
	Ratio          float64     `json:"ratio"`
	Verified       bool        `json:"verified"`
	SourceSHA256   string      `json:"sourceSha256"`
	PixelChecksum  interface{} `json:"pixelChecksum"` // string or []string
	Applicable     bool        `json:"applicable"`
	Reason         string      `json:"reason,omitempty"`
	// Frame is the source frame index for per-frame artifacts, or -1 for
	// whole-study artifacts (single-frame images and MIC2 containers).
	Frame int `json:"frame"`
}

// picsStripCounts mirrors cmd/mic-compress cineDatasetPICSStrips. The dashboard
// registry (web/pacs-model.mjs CODEC_REGISTRY) has rows for `_pics4` and
// `_pics8`, so both strip counts must be emitted.
var picsStripCounts = []int{4, 8}

// micVariant is one emitted MIC/PICS file. Suffixes MUST match
// CODEC_REGISTRY in web/pacs-model.mjs, which fetches `<name><suffix>.mic`:
// '' (1-state), '_4s', '_8s', '_pics4', '_pics8'. A mismatch is a silent 404.
type micVariant struct {
	codec   string // "mic" or "pics" -> S3 prefix
	variant string
	suffix  string
	pics    int // 0 = not PICS, else strip count
	state   int // 1, 4 or 8
}

func micVariants() []micVariant {
	vs := []micVariant{
		{"mic", "1state", "", 0, 1},
		{"mic", "4state", "_4s", 0, 4},
		{"mic", "8state", "_8s", 0, 8},
	}
	for _, n := range picsStripCounts {
		vs = append(vs,
			micVariant{"pics", fmt.Sprintf("pics%d", n), fmt.Sprintf("_pics%d", n), n, 4},
			micVariant{"pics", fmt.Sprintf("pics%d_8s", n), fmt.Sprintf("_pics%d_8s", n), n, 8},
		)
	}
	return vs
}

// sampleFrames picks up to maxRefFrames evenly spaced frame indices.
//
// MUST stay identical to cmd/mic-pacs-refgen's copy: MIC and the reference
// codecs have to cover the SAME frames, or the head-to-head comparison is
// silently invalid. (Deliberate duplication -- the two binaries are split by
// the cgo boundary, same precedent as readDicomSeries in mic-refgen.)
const maxRefFrames = 16

func sampleFrames(n int) []int {
	if n <= maxRefFrames {
		idx := make([]int, n)
		for i := range idx {
			idx[i] = i
		}
		return idx
	}
	idx := make([]int, maxRefFrames)
	for i := range idx {
		idx[i] = i * n / maxRefFrames
	}
	return idx
}

// encodeFrameVariants encodes one frame into every MIC/PICS variant, verifying
// each roundtrip before the file is written. frameIdx is -1 for whole-image
// artifacts. Returns the artifacts produced plus any per-variant errors.
func encodeFrameVariants(
	entryID, name, micDir, picsDir string,
	px []uint16, width, height int, maxValue uint16,
	route, sourceSHA256 string, frameIdx int,
) ([]encodedArtifact, []string) {
	var out []encodedArtifact
	var errs []string
	rawBytes := len(px) * 2
	cksum := fmt.Sprintf("fnv1a32:%08x", fnv1a32LE(px))

	// An all-zero frame (blank slices are common at the ends of MR/CT volumes)
	// gives maxValue==0, so the codec's pixelDepth = bits.Len16(0) = 0 and
	// `1 << (pixelDepth-1)` shifts by -1 -> panic. Clamping to 1 makes the
	// depth 1 bit, which round-trips the zeros correctly (verified). This is a
	// latent core-codec bug (deltarlecompressu16.go:26); guarded here rather
	// than changing shared codec semantics mid-run.
	if maxValue == 0 {
		maxValue = 1
	}

	for _, v := range micVariants() {
		dir, file := micDir, name+v.suffix+".mic"
		if v.pics > 0 {
			dir = picsDir
		}
		key := fmt.Sprintf("%s/%s/%s", entryID, v.codec, file)

		var comp []byte
		var err error
		switch {
		case v.pics > 0 && v.state == 8:
			comp, err = mic.CompressParallelStrips8State(px, width, height, maxValue, v.pics)
		case v.pics > 0:
			comp, err = mic.CompressParallelStrips4State(px, width, height, maxValue, v.pics)
		case v.state == 8:
			comp, err = mic.CompressSingleFrame8State(px, width, height, maxValue)
		case v.state == 4:
			comp, err = mic.CompressSingleFrame4State(px, width, height, maxValue)
		default:
			comp, err = mic.CompressSingleFrame(px, width, height, maxValue)
		}

		// Verify the roundtrip before writing: an unverified artifact must
		// never reach the bucket, where it would be treated as ground truth.
		if err == nil {
			var dec []uint16
			if v.pics > 0 {
				dec, _, _, err = mic.DecompressParallelStrips(comp)
			} else {
				dec, err = mic.DecompressSingleFrame(comp, width, height)
			}
			if err == nil && !equalU16(px, dec) {
				err = fmt.Errorf("roundtrip MISMATCH")
			}
		}
		if err == nil {
			if v.pics > 0 {
				err = os.WriteFile(filepath.Join(dir, file), comp, 0644)
			} else {
				err = writeMICFile(filepath.Join(dir, file), width, height, comp)
			}
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "  [FAIL] %s %s: %v\n", name, v.variant, err)
			errs = append(errs, fmt.Sprintf("%s/%s: %v", name, v.variant, err))
			out = append(out, encodedArtifact{
				Codec: v.codec, Variant: v.variant, Container: "MIC1", Route: route,
				Key: key, RawBytes: rawBytes, SourceSHA256: sourceSHA256,
				Verified: false, Applicable: false, Reason: err.Error(), Frame: frameIdx,
			})
			continue
		}
		out = append(out, encodedArtifact{
			Codec: v.codec, Variant: v.variant, Container: "MIC1", Route: route,
			Key: key, Bytes: len(comp), RawBytes: rawBytes,
			// raw/compressed, matching the repo convention and mic-pacs-refgen.
			Ratio: float64(rawBytes) / float64(len(comp)),
			Verified: true, SourceSHA256: sourceSHA256,
			PixelChecksum: cksum, Applicable: true, Frame: frameIdx,
		})
	}
	return out, errs
}

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

type studyManifestFragment struct {
	ID          string            `json:"id"`
	Tier        string            `json:"tier"`
	License     string            `json:"license"`
	Attribution string            `json:"attribution"`
	Artifacts   []encodedArtifact `json:"artifacts"`
	Notes       map[string]string `json:"notes"`
}

type routingBucket string

const (
	SINGLE_FRAME     routingBucket = "SINGLE_FRAME"
	SERIES_DIR       routingBucket = "SERIES_DIR"
	MULTIFRAME_FILE  routingBucket = "MULTIFRAME_FILE"
)

// ===== UTILITY FUNCTIONS =====

func fnv1a32LE(pixels []uint16) uint32 {
	const (
		offset = uint32(2166136261)
		prime  = uint32(16777619)
	)
	h := offset
	for _, p := range pixels {
		h ^= uint32(p & 0xff)
		h *= prime
		h ^= uint32(p >> 8)
		h *= prime
	}
	return h
}

func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

// ===== DICOM READING =====

// requireSingleSample rejects multi-sample (RGB / YBR / palette colour) frames.
//
// NativeFrame.Data holds one slice per pixel with one entry per sample, so the
// grayscale read path's Data[j][0] would silently keep only the first channel.
// The resulting artifact still passes the codec roundtrip check (the codec is
// lossless with respect to the truncated buffer), so a colour study would be
// published as verified ground truth while containing one channel of the image.
// Colour belongs on the CompressRGB / CompressWSI path; until that is wired up,
// fail loudly instead.
func requireSingleSample(nf *frame.NativeFrame) error {
	if len(nf.Data) == 0 {
		return fmt.Errorf("empty frame data")
	}
	if spp := len(nf.Data[0]); spp != 1 {
		return fmt.Errorf("unsupported colour data: SamplesPerPixel=%d "+
			"(grayscale-only path; use CompressRGB/CompressWSI)", spp)
	}
	return nil
}

func readDicomMultiFrame(fileName string) ([][]uint16, int, int, uint16, error) {
	ds, err := dicom.ParseFile(fileName, nil)
	if err != nil {
		return nil, 0, 0, 0, fmt.Errorf("parse DICOM: %w", err)
	}

	pde, err := ds.FindElementByTag(tag.PixelData)
	if err != nil {
		return nil, 0, 0, 0, fmt.Errorf("find pixel data: %w", err)
	}
	info := dicom.MustGetPixelDataInfo(pde.Value)
	if len(info.Frames) == 0 {
		return nil, 0, 0, 0, fmt.Errorf("no frames")
	}

	f0, err := info.Frames[0].GetNativeFrame()
	if err != nil {
		return nil, 0, 0, 0, fmt.Errorf("frame 0: %w", err)
	}
	// Reject multi-sample (RGB/YBR/palette) pixel data. NativeFrame.Data[j] has
	// one entry per sample, so taking Data[j][0] on a colour image would keep
	// only the first channel and produce an artifact that still passes the
	// codec roundtrip check -- silently wrong pixels labelled as ground truth.
	// Colour studies belong on the CompressRGB/CompressWSI path instead.
	if err := requireSingleSample(f0); err != nil {
		return nil, 0, 0, 0, err
	}
	w, h := f0.Cols, f0.Rows

	frames := make([][]uint16, len(info.Frames))
	var maxValue uint16
	for i, fr := range info.Frames {
		nf, err := fr.GetNativeFrame()
		if err != nil {
			return nil, 0, 0, 0, fmt.Errorf("frame %d: %w", i, err)
		}
		if err := requireSingleSample(nf); err != nil {
			return nil, 0, 0, 0, fmt.Errorf("frame %d: %w", i, err)
		}
		px := make([]uint16, w*h)
		for j := 0; j < len(nf.Data) && j < len(px); j++ {
			px[j] = uint16(nf.Data[j][0])
			if px[j] > maxValue {
				maxValue = px[j]
			}
		}
		frames[i] = px
	}
	return frames, w, h, maxValue, nil
}

func readDicomSeries(seriesDir string) ([][]uint16, int, int, uint16, error) {
	entries, err := os.ReadDir(seriesDir)
	if err != nil {
		return nil, 0, 0, 0, fmt.Errorf("read directory: %w", err)
	}

	type dicomEntry struct {
		path           string
		instanceNumber int
	}
	var dcmFiles []dicomEntry

	for _, e := range entries {
		if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), ".dcm") {
			continue
		}
		fpath := filepath.Join(seriesDir, e.Name())
		// Silently dropping a slice here would yield a MIC2 container with
		// fewer frames than the real study, still marked verified -- a short
		// volume masquerading as ground truth. Fail loudly instead, matching
		// cmd/mic-compress and cmd/mic-refgen.
		dataset, err := dicom.ParseFile(fpath, nil)
		if err != nil {
			return nil, 0, 0, 0, fmt.Errorf("parse %s: %w", e.Name(), err)
		}
		el, err := dataset.FindElementByTag(tag.InstanceNumber)
		if err != nil {
			return nil, 0, 0, 0, fmt.Errorf("%s: missing InstanceNumber: %w", e.Name(), err)
		}
		instNum, err := strconv.Atoi(fmt.Sprintf("%v", el.Value.GetValue().([]string)[0]))
		if err != nil {
			return nil, 0, 0, 0, fmt.Errorf("%s: bad InstanceNumber %v: %w",
				e.Name(), el.Value.GetValue(), err)
		}
		dcmFiles = append(dcmFiles, dicomEntry{path: fpath, instanceNumber: instNum})
	}

	if len(dcmFiles) == 0 {
		return nil, 0, 0, 0, fmt.Errorf("no .dcm files in %s", seriesDir)
	}

	// SliceStable + path tiebreak: duplicate InstanceNumbers must not reorder
	// non-deterministically between runs, or the same study would produce
	// different pixel checksums on re-encode.
	sort.SliceStable(dcmFiles, func(i, j int) bool {
		if dcmFiles[i].instanceNumber != dcmFiles[j].instanceNumber {
			return dcmFiles[i].instanceNumber < dcmFiles[j].instanceNumber
		}
		return dcmFiles[i].path < dcmFiles[j].path
	})

	var width, height int
	frames := make([][]uint16, len(dcmFiles))
	var maxValue uint16

	for f, de := range dcmFiles {
		dataset, err := dicom.ParseFile(de.path, nil)
		if err != nil {
			return nil, 0, 0, 0, fmt.Errorf("parse frame %d: %w", f, err)
		}
		pixelDataElement, err := dataset.FindElementByTag(tag.PixelData)
		if err != nil {
			return nil, 0, 0, 0, fmt.Errorf("no pixel data in frame %d: %w", f, err)
		}
		pixelDataInfo := dicom.MustGetPixelDataInfo(pixelDataElement.Value)
		nativeFrame, err := pixelDataInfo.Frames[0].GetNativeFrame()
		if err != nil {
			return nil, 0, 0, 0, fmt.Errorf("get native frame %d: %w", f, err)
		}
		if err := requireSingleSample(nativeFrame); err != nil {
			return nil, 0, 0, 0, fmt.Errorf("frame %d: %w", f, err)
		}

		if f == 0 {
			width = nativeFrame.Cols
			height = nativeFrame.Rows
		} else if nativeFrame.Cols != width || nativeFrame.Rows != height {
			return nil, 0, 0, 0, fmt.Errorf("frame %d dimension mismatch: %dx%d vs %dx%d",
				f, nativeFrame.Cols, nativeFrame.Rows, width, height)
		}

		pixels := make([]uint16, width*height)
		for j := 0; j < len(nativeFrame.Data); j++ {
			pixels[j] = uint16(nativeFrame.Data[j][0])
			if pixels[j] > maxValue {
				maxValue = pixels[j]
			}
		}
		frames[f] = pixels
	}

	return frames, width, height, maxValue, nil
}

// ===== COMPRESSION & WRITING =====

func writeMICFile(filename string, width, height int, compressed []byte) error {
	f, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer f.Close()

	header := make([]byte, 20)
	header[0] = 'M'
	header[1] = 'I'
	header[2] = 'C'
	header[3] = '1'
	binary.LittleEndian.PutUint32(header[4:8], uint32(width))
	binary.LittleEndian.PutUint32(header[8:12], uint32(height))
	binary.LittleEndian.PutUint32(header[12:16], 1)
	binary.LittleEndian.PutUint32(header[16:20], uint32(len(compressed)))

	if _, err := f.Write(header); err != nil {
		return err
	}
	if _, err := f.Write(compressed); err != nil {
		return err
	}
	return nil
}

func encodeAndVerifySingleFrame(
	label string,
	pixels []uint16, width, height int,
	encode func() ([]byte, error),
	decode func([]byte) ([]uint16, error),
) ([]byte, string, error) {
	comp, err := encode()
	if err != nil {
		return nil, "", fmt.Errorf("%s encode: %w", label, err)
	}

	decoded, err := decode(comp)
	if err != nil {
		return nil, "", fmt.Errorf("%s decode: %w", label, err)
	}

	if len(decoded) != len(pixels) {
		return nil, "", fmt.Errorf("%s roundtrip length: %d vs %d", label, len(decoded), len(pixels))
	}
	for i := range pixels {
		if decoded[i] != pixels[i] {
			return nil, "", fmt.Errorf("%s pixel mismatch at %d: %d vs %d", label, i, decoded[i], pixels[i])
		}
	}

	checksum := fmt.Sprintf("fnv1a32:%08x", fnv1a32LE(pixels))
	return comp, checksum, nil
}

// ===== MAIN PIPELINE =====

func main() {
	workers := flag.Int("workers", 4, "number of worker goroutines")
	force := flag.Bool("force", false, "re-encode even if artifact already exists")
	flag.Parse()

	manifestData, err := os.ReadFile("pacs-data/manifest.json")
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: failed to read manifest: %v\n", err)
		os.Exit(1)
	}

	var m manifest
	if err := json.Unmarshal(manifestData, &m); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: failed to parse manifest: %v\n", err)
		os.Exit(1)
	}

	// Filter to Tier A only
	var tierA []manifestEntry
	for _, e := range m.Entries {
		if e.Tier == "A" {
			tierA = append(tierA, e)
		}
	}

	fmt.Fprintf(os.Stderr, "Processing %d Tier-A studies with %d workers\n", len(tierA), *workers)

	studyChan := make(chan manifestEntry, len(tierA))
	var wg sync.WaitGroup
	var failuresMu sync.Mutex
	var failures []string

	for i := 0; i < *workers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for entry := range studyChan {
				// Isolate each study: a panic in the codec on one pathological
				// study must not tear down the whole batch (79 good studies).
				err := func() (err error) {
					defer func() {
						if r := recover(); r != nil {
							err = fmt.Errorf("panic: %v", r)
						}
					}()
					return processStudy(&entry, *force)
				}()
				if err != nil {
					failuresMu.Lock()
					failures = append(failures, fmt.Sprintf("%s: %v", entry.ID, err))
					failuresMu.Unlock()
					fmt.Fprintf(os.Stderr, "[ERROR] %s: %v\n", entry.ID, err)
				}
			}
		}(i)
	}

	for i, entry := range tierA {
		fmt.Fprintf(os.Stderr, "[%d/%d] starting %s\n", i+1, len(tierA), entry.ID)
		studyChan <- entry
	}
	close(studyChan)
	wg.Wait()

	if len(failures) > 0 {
		fmt.Fprintf(os.Stderr, "\nFAILURES: %d studies failed\n", len(failures))
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "Encoding complete.\n")
}

func processStudy(entry *manifestEntry, force bool) error {
	rawDir := filepath.Join("pacs-data/raw-src", entry.ID)
	encodedDir := filepath.Join("pacs-data/encoded", entry.ID)

	if err := os.MkdirAll(encodedDir, 0755); err != nil {
		return fmt.Errorf("create encoded dir: %w", err)
	}

	entries, err := os.ReadDir(rawDir)
	if err != nil {
		return fmt.Errorf("read raw dir: %w", err)
	}

	var dcmFiles []string
	for _, e := range entries {
		// Case-insensitive: IDC uses .dcm, but other sources ship .DCM
		// (e.g. Rubomedical MRBRAIN.DCM), which a ==".dcm" check would miss.
		if !e.IsDir() && strings.EqualFold(filepath.Ext(e.Name()), ".dcm") {
			dcmFiles = append(dcmFiles, filepath.Join(rawDir, e.Name()))
		}
	}

	if len(dcmFiles) == 0 {
		return fmt.Errorf("no DICOM files found")
	}

	// Read and classify
	var frames [][]uint16
	var width, height int
	var maxValue uint16
	var route routingBucket
	var sourceFile string

	if len(dcmFiles) == 1 {
		sourceFile = dcmFiles[0]
		frames, width, height, maxValue, err = readDicomMultiFrame(sourceFile)
		if err != nil {
			return fmt.Errorf("read multiframe: %w", err)
		}
		if len(frames) == 1 {
			route = SINGLE_FRAME
		} else {
			route = MULTIFRAME_FILE
		}
	} else {
		route = SERIES_DIR
		sourceFile = dcmFiles[0]
		frames, width, height, maxValue, err = readDicomSeries(rawDir)
		if err != nil {
			return fmt.Errorf("read series: %w", err)
		}
	}

	if len(frames) == 0 {
		return fmt.Errorf("no frames decoded")
	}

	sourceSHA256, _ := sha256File(sourceFile)

	// Create output directories
	micDir := filepath.Join(encodedDir, "mic")
	picsDir := filepath.Join(encodedDir, "pics")
	if err := os.MkdirAll(micDir, 0755); err != nil {
		return err
	}
	if err := os.MkdirAll(picsDir, 0755); err != nil {
		return err
	}

	artifacts := []encodedArtifact{}
	// Collected so a study whose artifacts all failed cannot report [OK].
	var artifactErrs []string

	// Encode using CompressMultiFrame for multi-frame, or individually for single-frame
	if route == SINGLE_FRAME {
		framePixels := frames[0]
		var mv uint16
		for _, v := range framePixels {
			if v > mv {
				mv = v
			}
		}
		// Same helper as the per-frame path, so single-frame and multi-frame
		// studies produce identically-named, identically-verified variants.
		arts, errs := encodeFrameVariants(entry.ID, entry.ID, micDir, picsDir,
			framePixels, width, height, mv, string(route), sourceSHA256, -1)
		artifacts = append(artifacts, arts...)
		artifactErrs = append(artifactErrs, errs...)
	} else {
		// Multi-frame: MIC2 container (random access) + per-frame files below.
		// Same all-zero guard as encodeFrameVariants: a volume that is entirely
		// blank would give maxValue==0 and panic the codec on a negative shift.
		if maxValue == 0 {
			maxValue = 1
		}
		compMIC2, err := mic.CompressMultiFrame(frames, width, height, maxValue, false)
		if err != nil {
			return fmt.Errorf("compress MIC2: %w", err)
		}

		// Verify MIC2 by decompressing all frames
		decompFrames, _, err := mic.DecompressMultiFrame(compMIC2)
		if err != nil {
			return fmt.Errorf("decompress MIC2 verify: %w", err)
		}
		if len(decompFrames) != len(frames) {
			return fmt.Errorf("frame count mismatch: %d vs %d", len(decompFrames), len(frames))
		}
		for fi := range frames {
			if len(decompFrames[fi]) != len(frames[fi]) {
				return fmt.Errorf("frame %d size mismatch", fi)
			}
			for j := range frames[fi] {
				if decompFrames[fi][j] != frames[fi][j] {
					return fmt.Errorf("frame %d pixel mismatch at %d", fi, j)
				}
			}
		}

		// Write MIC2 artifact
		rawBytes := len(frames) * len(frames[0]) * 2
		micFile := filepath.Join(micDir, entry.ID+".mic")
		if err := os.WriteFile(micFile, compMIC2, 0644); err != nil {
			return fmt.Errorf("write MIC2: %w", err)
		}

		// Create pixel checksums for all frames
		var checksums []string
		for _, f := range frames {
			checksums = append(checksums, fmt.Sprintf("fnv1a32:%08x", fnv1a32LE(f)))
		}

		artifacts = append(artifacts, encodedArtifact{
			Codec: "mic", Variant: "1state", Container: "MIC2", Route: string(route),
			Key: fmt.Sprintf("%s/mic/%s.mic", entry.ID, entry.ID),
			// raw/compressed, matching the repo convention and mic-pacs-refgen.
			Bytes: len(compMIC2), RawBytes: rawBytes, Ratio: float64(rawBytes) / float64(len(compMIC2)),
			Verified: true, SourceSHA256: sourceSHA256, PixelChecksum: checksums, Applicable: true,
			// -1: whole-study container, not a single frame. Without this the
			// Go zero value (0) would collide with per-frame index f000.
			Frame: -1,
		})

		// Per-frame single-frame artifacts.
		//
		// The MIC2 container above is a random-access artifact, but it is NOT
		// what the browser benchmark consumes: web/pacs-model.mjs drives the
		// cine section off independent single-frame files named by
		// cineFrameName() -- `${id}_f${String(i).padStart(3,'0')}` -- so the
		// full codec matrix can run per frame and report frames/s. The
		// reference codecs (HTJ2K/JPEG-LS/JPEG-XL) have no multi-frame
		// container at all, so per-frame files are also the only way MIC and
		// the reference codecs measure the same thing.
		//
		// sampleFrames() MUST stay identical to cmd/mic-pacs-refgen's copy, or
		// MIC and the reference codecs would cover different frames and the
		// comparison would be silently invalid.
		// out is the OUTPUT frame index and must be contiguous 0..N-1: the
		// dashboard builds its frame list with
		//   Array.from({length: ds.frames}, (_, i) => cineFrameName(id, i))
		// so a sparse numbering (f000, f004, f008, ...) would 404 on f001.
		// srcIdx is the true source slice, preserved in the manifest.
		for out, srcIdx := range sampleFrames(len(frames)) {
			px := frames[srcIdx]
			var mv uint16
			for _, v := range px {
				if v > mv {
					mv = v
				}
			}
			name := fmt.Sprintf("%s_f%03d", entry.ID, out)
			fi := srcIdx
			arts, errs := encodeFrameVariants(entry.ID, name, micDir, picsDir,
				px, width, height, mv, string(route)+"/perframe", sourceSHA256, fi)
			artifacts = append(artifacts, arts...)
			artifactErrs = append(artifactErrs, errs...)
		}
	}

	// Write manifest fragment
	fragment := studyManifestFragment{
		ID:          entry.ID,
		Tier:        entry.Tier,
		License:     entry.License,
		Attribution: entry.Attribution,
		Artifacts:   artifacts,
		Notes:       make(map[string]string),
	}

	fragmentData, err := json.MarshalIndent(fragment, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal manifest: %w", err)
	}
	manifestPath := filepath.Join(encodedDir, "mic-manifest.json")
	if err := os.WriteFile(manifestPath, fragmentData, 0644); err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}

	// A study with zero usable artifacts is a failure, not a success -- without
	// this it would log [OK] and exit 0 while producing nothing.
	ok := 0
	for _, a := range artifacts {
		if a.Verified {
			ok++
		}
	}
	if ok == 0 {
		return fmt.Errorf("%s: no artifacts verified (%d attempts): %s",
			entry.ID, len(artifacts), strings.Join(artifactErrs, "; "))
	}
	fmt.Fprintf(os.Stderr, "[OK] %s: %d frames → %d/%d artifacts verified\n",
		entry.ID, len(frames), ok, len(artifacts))
	if len(artifactErrs) > 0 {
		fmt.Fprintf(os.Stderr, "  (%d variant failures: %s)\n",
			len(artifactErrs), strings.Join(artifactErrs, "; "))
	}
	return nil
}
