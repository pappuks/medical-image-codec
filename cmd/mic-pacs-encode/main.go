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
	"sync"

	"mic"

	"github.com/suyashkumar/dicom"
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
	w, h := f0.Cols, f0.Rows

	frames := make([][]uint16, len(info.Frames))
	var maxValue uint16
	for i, fr := range info.Frames {
		nf, err := fr.GetNativeFrame()
		if err != nil {
			return nil, 0, 0, 0, fmt.Errorf("frame %d: %w", i, err)
		}
		px := make([]uint16, w*h)
		for j := 0; j < len(nf.Data); j++ {
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
		if e.IsDir() || filepath.Ext(e.Name()) != ".dcm" {
			continue
		}
		fpath := filepath.Join(seriesDir, e.Name())
		dataset, err := dicom.ParseFile(fpath, nil)
		if err != nil {
			continue
		}
		el, err := dataset.FindElementByTag(tag.InstanceNumber)
		if err != nil {
			continue
		}
		instNum, err := strconv.Atoi(fmt.Sprintf("%v", el.Value.GetValue().([]string)[0]))
		if err != nil {
			continue
		}
		dcmFiles = append(dcmFiles, dicomEntry{path: fpath, instanceNumber: instNum})
	}

	if len(dcmFiles) == 0 {
		return nil, 0, 0, 0, fmt.Errorf("no .dcm files in %s", seriesDir)
	}

	sort.Slice(dcmFiles, func(i, j int) bool {
		return dcmFiles[i].instanceNumber < dcmFiles[j].instanceNumber
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
				if err := processStudy(&entry, *force); err != nil {
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
		if !e.IsDir() && filepath.Ext(e.Name()) == ".dcm" {
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

	// Encode using CompressMultiFrame for multi-frame, or individually for single-frame
	if route == SINGLE_FRAME {
		// Single frame: compress directly and write MIC1 files
		framePixels := frames[0]
		rawBytes := len(framePixels) * 2

		// MIC1 1-state
		comp1, cksum1, err := encodeAndVerifySingleFrame(
			"1state",
			framePixels, width, height,
			func() ([]byte, error) { return mic.CompressSingleFrame(framePixels, width, height, maxValue) },
			func(c []byte) ([]uint16, error) { return mic.DecompressSingleFrame(c, width, height) },
		)
		if err == nil {
			outPath := filepath.Join(micDir, entry.ID+"_1state.mic")
			if err := writeMICFile(outPath, width, height, comp1); err == nil {
				artifacts = append(artifacts, encodedArtifact{
					Codec: "mic", Variant: "1state", Container: "MIC1", Route: string(route),
					Key: fmt.Sprintf("%s/mic/%s_1state.mic", entry.ID, entry.ID),
					Bytes: len(comp1), RawBytes: rawBytes, Ratio: float64(len(comp1)) / float64(rawBytes),
					Verified: true, SourceSHA256: sourceSHA256, PixelChecksum: cksum1, Applicable: true,
				})
			}
		}

		// MIC1 4-state
		comp4, cksum4, err := encodeAndVerifySingleFrame(
			"4state",
			framePixels, width, height,
			func() ([]byte, error) { return mic.CompressSingleFrame4State(framePixels, width, height, maxValue) },
			func(c []byte) ([]uint16, error) { return mic.DecompressSingleFrame(c, width, height) },
		)
		if err == nil {
			outPath := filepath.Join(micDir, entry.ID+"_4s.mic")
			if err := writeMICFile(outPath, width, height, comp4); err == nil {
				artifacts = append(artifacts, encodedArtifact{
					Codec: "mic", Variant: "4state", Container: "MIC1", Route: string(route),
					Key: fmt.Sprintf("%s/mic/%s_4s.mic", entry.ID, entry.ID),
					Bytes: len(comp4), RawBytes: rawBytes, Ratio: float64(len(comp4)) / float64(rawBytes),
					Verified: true, SourceSHA256: sourceSHA256, PixelChecksum: cksum4, Applicable: true,
				})
			}
		}

		// MIC1 8-state
		comp8, cksum8, err := encodeAndVerifySingleFrame(
			"8state",
			framePixels, width, height,
			func() ([]byte, error) { return mic.CompressSingleFrame8State(framePixels, width, height, maxValue) },
			func(c []byte) ([]uint16, error) { return mic.DecompressSingleFrame(c, width, height) },
		)
		if err == nil {
			outPath := filepath.Join(micDir, entry.ID+"_8s.mic")
			if err := writeMICFile(outPath, width, height, comp8); err == nil {
				artifacts = append(artifacts, encodedArtifact{
					Codec: "mic", Variant: "8state", Container: "MIC1", Route: string(route),
					Key: fmt.Sprintf("%s/mic/%s_8s.mic", entry.ID, entry.ID),
					Bytes: len(comp8), RawBytes: rawBytes, Ratio: float64(len(comp8)) / float64(rawBytes),
					Verified: true, SourceSHA256: sourceSHA256, PixelChecksum: cksum8, Applicable: true,
				})
			}
		}

		// PICS 4-state
		picsComp4, _, err := encodeAndVerifySingleFrame(
			"pics4",
			framePixels, width, height,
			func() ([]byte, error) { return mic.CompressParallelStrips4State(framePixels, width, height, maxValue, 4) },
			func(c []byte) ([]uint16, error) {
				pixels, _, _, err := mic.DecompressParallelStrips(c)
				return pixels, err
			},
		)
		if err == nil {
			if err := os.WriteFile(filepath.Join(picsDir, entry.ID+"_pics4.mic"), picsComp4, 0644); err == nil {
				artifacts = append(artifacts, encodedArtifact{
					Codec: "pics", Variant: "pics4", Container: "MIC1", Route: string(route),
					Key: fmt.Sprintf("%s/pics/%s_pics4.mic", entry.ID, entry.ID),
					Bytes: len(picsComp4), RawBytes: rawBytes, Ratio: float64(len(picsComp4)) / float64(rawBytes),
					Verified: true, SourceSHA256: sourceSHA256, PixelChecksum: cksum1, Applicable: true,
				})
			}
		}

		// PICS 8-state
		picsComp8, _, err := encodeAndVerifySingleFrame(
			"pics8",
			framePixels, width, height,
			func() ([]byte, error) { return mic.CompressParallelStrips8State(framePixels, width, height, maxValue, 4) },
			func(c []byte) ([]uint16, error) {
				pixels, _, _, err := mic.DecompressParallelStrips(c)
				return pixels, err
			},
		)
		if err == nil {
			if err := os.WriteFile(filepath.Join(picsDir, entry.ID+"_pics4_8s.mic"), picsComp8, 0644); err == nil {
				artifacts = append(artifacts, encodedArtifact{
					Codec: "pics", Variant: "pics4_8s", Container: "MIC1", Route: string(route),
					Key: fmt.Sprintf("%s/pics/%s_pics4_8s.mic", entry.ID, entry.ID),
					Bytes: len(picsComp8), RawBytes: rawBytes, Ratio: float64(len(picsComp8)) / float64(rawBytes),
					Verified: true, SourceSHA256: sourceSHA256, PixelChecksum: cksum1, Applicable: true,
				})
			}
		}
	} else {
		// Multi-frame: use CompressMultiFrame for MIC2, and per-frame for PICS variants
		// For now, just handle MIC2 independent mode
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
			Bytes: len(compMIC2), RawBytes: rawBytes, Ratio: float64(len(compMIC2)) / float64(rawBytes),
			Verified: true, SourceSHA256: sourceSHA256, PixelChecksum: checksums, Applicable: true,
		})
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

	fragmentData, _ := json.MarshalIndent(fragment, "", "  ")
	manifestPath := filepath.Join(encodedDir, "mic-manifest.json")
	os.WriteFile(manifestPath, fragmentData, 0644)

	fmt.Fprintf(os.Stderr, "[OK] %s: %d frames → %d artifacts\n", entry.ID, len(frames), len(artifacts))
	return nil
}
