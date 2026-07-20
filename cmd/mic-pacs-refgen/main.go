// mic-pacs-refgen generates reference-codec artifacts (HTJ2K .jph, JPEG-LS
// .jls, JPEG-XL .jxl) for the ingested PACS corpus, so the browser dashboard
// can compare MIC/PICS against the reference codecs on identical pixels.
//
// This is the cgo half of the pair described in docs/pacs-encode-design.md:
// cmd/mic-pacs-encode produces MIC/PICS artifacts with no native dependencies,
// while this binary requires libopenjph, libcharls and libjxl.
//
// Usage:
//
//	go run -tags cgo_ojph ./cmd/mic-pacs-refgen [-workers 4] [-only id,id] [-limit N]
//
// Every codestream is natively roundtrip-decoded and compared against the
// source pixels BEFORE it is written; a mismatch refuses the write rather than
// emitting a file the dashboard would later trust as ground truth. Mirrors
// encodeVerifyWrite in cmd/mic-refgen.
//
//go:build cgo_ojph

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math/bits"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/suyashkumar/dicom"
	"github.com/suyashkumar/dicom/pkg/tag"

	"mic/ojph"
)

// maxRefFrames caps the per-frame fan-out for deep volumetric series. The
// reference codecs have no multi-frame container (unlike MIC2), so a 658-slice
// MR series would otherwise emit 658x3 objects. Sampling evenly across the
// stack keeps anatomical variety for benchmarking without exploding the object
// count.
const maxRefFrames = 16

type manifestEntry struct {
	ID             string `json:"id"`
	Tier           string `json:"tier"`
	License        string `json:"license"`
	Attribution    string `json:"attribution"`
	Representative struct {
		Modality    string `json:"modality"`
		Rows        int    `json:"rows"`
		Cols        int    `json:"cols"`
		Frames      int    `json:"frames"`
		Photometric string `json:"photometric"`
	} `json:"representative"`
}

type manifest struct {
	Entries []manifestEntry `json:"entries"`
}

// codecEntry is one reference codec's result for one frame.
type codecEntry struct {
	File              string  `json:"file"`
	Bytes             int     `json:"bytes"`
	Ratio             float64 `json:"ratio"`
	NativeRoundtripOK bool    `json:"nativeRoundtripOK"`
}

type frameManifest struct {
	Frame    int         `json:"frame"`
	Width    int         `json:"width"`
	Height   int         `json:"height"`
	BitDepth int         `json:"bitDepth"`
	HTJ2K    *codecEntry `json:"htj2k,omitempty"`
	JLS      *codecEntry `json:"jls,omitempty"`
	JXL      *codecEntry `json:"jxl,omitempty"`
}

type studyManifest struct {
	ID          string `json:"id"`
	Modality    string `json:"modality"`
	License     string `json:"license"`
	Attribution string `json:"attribution"`
	Applicable  bool   `json:"applicable"`
	// Reason is set when Applicable is false so the dashboard can explain a
	// gap rather than silently showing nothing.
	Reason        string          `json:"reason,omitempty"`
	SourceFrames  int             `json:"sourceFrames"`
	SampledFrames int             `json:"sampledFrames"`
	Frames        []frameManifest `json:"frames,omitempty"`
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

// encodeVerifyWrite encodes, natively decodes, compares, and only then writes.
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
		return nil, fmt.Errorf("%s native roundtrip MISMATCH (%dx%d) — refusing to write %s",
			label, cols, rows, outPath)
	}
	if err := os.WriteFile(outPath, comp, 0644); err != nil {
		return nil, fmt.Errorf("%s write %s: %w", label, outPath, err)
	}
	raw := len(src) * 2
	return &codecEntry{
		File:              filepath.Base(outPath),
		Bytes:             len(comp),
		Ratio:             float64(raw) / float64(len(comp)),
		NativeRoundtripOK: true,
	}, nil
}

// encodeFrameRefs encodes one frame with all three reference codecs.
func encodeFrameRefs(dirs map[string]string, name string, pixels []uint16, cols, rows, frameIdx int) (frameManifest, int) {
	bitDepth := bits.Len16(maxU16(pixels))
	// Floor at 9 bits: CharLS packs <=8-bit samples as one byte while our
	// wrapper feeds uint16 (roundtrip mismatch), and libjxl rejects
	// bits_per_sample=8 with UINT16 input. Same rationale as cmd/mic-refgen.
	if bitDepth < 9 {
		bitDepth = 9
	}
	rec := frameManifest{Frame: frameIdx, Width: cols, Height: rows, BitDepth: bitDepth}
	failures := 0

	if e, err := encodeVerifyWrite("HTJ2K", filepath.Join(dirs["htj2k"], name+".jph"), pixels, cols, rows,
		func() ([]byte, error) { return ojph.CompressU16(pixels, cols, rows, bitDepth) },
		func(c []byte) ([]uint16, error) { return ojph.DecompressU16(c, cols, rows) },
	); err != nil {
		fmt.Fprintf(os.Stderr, "    %v\n", err)
		failures++
	} else {
		rec.HTJ2K = e
	}

	if e, err := encodeVerifyWrite("JPEG-LS", filepath.Join(dirs["jls"], name+".jls"), pixels, cols, rows,
		func() ([]byte, error) { return ojph.CharlsCompressU16(pixels, cols, rows, bitDepth) },
		func(c []byte) ([]uint16, error) { return ojph.CharlsDecompressU16(c, cols, rows) },
	); err != nil {
		fmt.Fprintf(os.Stderr, "    %v\n", err)
		failures++
	} else {
		rec.JLS = e
	}

	if e, err := encodeVerifyWrite("JPEG-XL", filepath.Join(dirs["jxl"], name+".jxl"), pixels, cols, rows,
		func() ([]byte, error) {
			return ojph.JXLCompressU16(pixels, cols, rows, bitDepth, ojph.JXLDefaultEffort)
		},
		func(c []byte) ([]uint16, error) { return ojph.JXLDecompressU16(c, cols, rows) },
	); err != nil {
		fmt.Fprintf(os.Stderr, "    %v\n", err)
		failures++
	} else {
		rec.JXL = e
	}

	return rec, failures
}

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
		for j := 0; j < len(nf.Data) && j < len(px); j++ {
			px[j] = uint16(nf.Data[j][0])
		}
		frames[i] = px
	}
	return frames, w, h, nil
}

// readFrames loads a study's frames, whether it is one multi-frame file or a
// directory of single-frame instances ordered by InstanceNumber.
func readFrames(studyDir string) ([][]uint16, int, int, error) {
	var files []string
	err := filepath.Walk(studyDir, func(p string, fi os.FileInfo, err error) error {
		if err != nil || fi.IsDir() || strings.HasPrefix(filepath.Base(p), ".") {
			return nil
		}
		files = append(files, p)
		return nil
	})
	if err != nil {
		return nil, 0, 0, err
	}
	if len(files) == 0 {
		return nil, 0, 0, fmt.Errorf("no files in %s", studyDir)
	}
	if len(files) == 1 {
		return readDicomFrames(files[0])
	}

	type inst struct {
		path string
		num  int
	}
	insts := make([]inst, 0, len(files))
	for _, p := range files {
		ds, err := dicom.ParseFile(p, nil)
		if err != nil {
			return nil, 0, 0, fmt.Errorf("parse %s: %w", filepath.Base(p), err)
		}
		n := 0
		if el, err := ds.FindElementByTag(tag.InstanceNumber); err == nil {
			fmt.Sscanf(strings.Trim(fmt.Sprint(el.Value), "[]"), "%d", &n)
		}
		insts = append(insts, inst{p, n})
	}
	sort.Slice(insts, func(i, j int) bool { return insts[i].num < insts[j].num })

	var frames [][]uint16
	var w, h int
	for _, in := range insts {
		fr, fw, fh, err := readDicomFrames(in.path)
		if err != nil {
			return nil, 0, 0, err
		}
		if w == 0 {
			w, h = fw, fh
		} else if fw != w || fh != h {
			// Mixed geometry within one series (localizers/scouts). Skip the
			// odd instance rather than abort the whole study.
			continue
		}
		frames = append(frames, fr...)
	}
	if len(frames) == 0 {
		return nil, 0, 0, fmt.Errorf("no frames of consistent geometry")
	}
	return frames, w, h, nil
}

// sampleFrames picks up to maxRefFrames evenly spaced indices.
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

func writeManifest(outBase, manPath string, sm studyManifest) error {
	if err := os.MkdirAll(outBase, 0755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(sm, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(manPath, b, 0644)
}

func processStudy(e manifestEntry, force bool) error {
	studyDir := filepath.Join("pacs-data/raw-src", e.ID)
	outBase := filepath.Join("pacs-data/encoded", e.ID)
	manPath := filepath.Join(outBase, "ref-manifest.json")

	if !force {
		if _, err := os.Stat(manPath); err == nil {
			fmt.Printf("[skip] %s (already encoded)\n", e.ID)
			return nil
		}
	}

	sm := studyManifest{
		ID: e.ID, Modality: e.Representative.Modality,
		License: e.License, Attribution: e.Attribution, Applicable: true,
	}

	// The ojph/CharLS/libjxl wrappers are single-component uint16 only. Rather
	// than silently omit these codecs for colour studies, record why.
	photo := strings.ToUpper(strings.TrimSpace(e.Representative.Photometric))
	if photo != "MONOCHROME1" && photo != "MONOCHROME2" {
		sm.Applicable = false
		sm.Reason = fmt.Sprintf("reference codecs are grayscale-only; PhotometricInterpretation=%s", photo)
		fmt.Printf("[n/a]  %-30s %s\n", e.ID, sm.Reason)
		return writeManifest(outBase, manPath, sm)
	}

	frames, w, h, err := readFrames(studyDir)
	if err != nil {
		sm.Applicable = false
		sm.Reason = fmt.Sprintf("unreadable: %v", err)
		if werr := writeManifest(outBase, manPath, sm); werr != nil {
			return werr
		}
		return fmt.Errorf("%s: %w", e.ID, err)
	}

	dirs := map[string]string{
		"htj2k": filepath.Join(outBase, "htj2k"),
		"jls":   filepath.Join(outBase, "jls"),
		"jxl":   filepath.Join(outBase, "jxl"),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0755); err != nil {
			return err
		}
	}

	sm.SourceFrames = len(frames)
	idxs := sampleFrames(len(frames))
	sm.SampledFrames = len(idxs)

	totalFail := 0
	// `out` is the OUTPUT frame index and must be contiguous 0..N-1, matching
	// web/pacs-model.mjs cineFrameName() -- `${id}_f${String(i).padStart(3,'0')}`
	// -- which the dashboard generates contiguously. Sparse numbering would
	// 404. srcIdx is the true source slice, recorded in the manifest.
	// This numbering MUST match cmd/mic-pacs-encode exactly, or MIC and the
	// reference codecs would label different slices with the same frame name.
	for out, srcIdx := range idxs {
		name := fmt.Sprintf("%s_f%03d", e.ID, out)
		rec, fails := encodeFrameRefs(dirs, name, frames[srcIdx], w, h, srcIdx)
		totalFail += fails
		sm.Frames = append(sm.Frames, rec)
	}

	if err := writeManifest(outBase, manPath, sm); err != nil {
		return err
	}
	fmt.Printf("[ok]   %-30s %s %dx%d  %d/%d frames  %d codec failures\n",
		e.ID, sm.Modality, w, h, len(idxs), len(frames), totalFail)
	if totalFail > 0 {
		return fmt.Errorf("%s: %d codec failures", e.ID, totalFail)
	}
	return nil
}

func main() {
	workers := flag.Int("workers", 4, "number of worker goroutines")
	force := flag.Bool("force", false, "re-encode even if ref-manifest.json exists")
	only := flag.String("only", "", "comma-separated study IDs (default: all Tier-A)")
	limit := flag.Int("limit", 0, "process at most N studies (0 = no limit)")
	flag.Parse()

	data, err := os.ReadFile("pacs-data/manifest.json")
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: read manifest: %v\n", err)
		os.Exit(1)
	}
	var m manifest
	if err := json.Unmarshal(data, &m); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: parse manifest: %v\n", err)
		os.Exit(1)
	}

	onlySet := map[string]bool{}
	for _, s := range strings.Split(*only, ",") {
		if s = strings.TrimSpace(s); s != "" {
			onlySet[s] = true
		}
	}

	var todo []manifestEntry
	for _, e := range m.Entries {
		// Tier B is lossy; never reference-encode it as ground truth.
		if e.Tier != "A" {
			continue
		}
		if len(onlySet) > 0 && !onlySet[e.ID] {
			continue
		}
		todo = append(todo, e)
		if *limit > 0 && len(todo) >= *limit {
			break
		}
	}

	fmt.Fprintf(os.Stderr, "reference-encoding %d Tier-A studies, %d workers\n", len(todo), *workers)

	ch := make(chan manifestEntry)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var failures []string

	for i := 0; i < *workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for e := range ch {
				if err := processStudy(e, *force); err != nil {
					mu.Lock()
					failures = append(failures, err.Error())
					mu.Unlock()
					fmt.Fprintf(os.Stderr, "[ERROR] %v\n", err)
				}
			}
		}()
	}
	for _, e := range todo {
		ch <- e
	}
	close(ch)
	wg.Wait()

	fmt.Fprintf(os.Stderr, "\ndone: %d studies, %d failed\n", len(todo), len(failures))
	if len(failures) > 0 {
		os.Exit(1)
	}
}
