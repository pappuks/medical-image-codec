package mic

import (
	"bytes"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"testing"

	"github.com/suyashkumar/dicom"
	"github.com/suyashkumar/dicom/pkg/tag"
)

func TestTemporalDeltaRoundtrip(t *testing.T) {
	// Create two frames with small differences
	n := 256 * 256
	frame0 := make([]uint16, n)
	frame1 := make([]uint16, n)
	rng := rand.New(rand.NewSource(42))
	for i := range frame0 {
		frame0[i] = uint16(rng.Intn(1024))
		frame1[i] = uint16(int32(frame0[i]) + int32(rng.Intn(11)) - 5) // small diff
	}

	// Frame 0: encode with nil prev (passthrough)
	enc0 := TemporalDeltaEncode(frame0, nil)
	dec0 := TemporalDeltaDecode(enc0, nil)
	for i := range frame0 {
		if dec0[i] != frame0[i] {
			t.Fatalf("frame0 mismatch at %d: got %d, want %d", i, dec0[i], frame0[i])
		}
	}

	// Frame 1: encode with frame0 as prev
	enc1 := TemporalDeltaEncode(frame1, frame0)
	dec1 := TemporalDeltaDecode(enc1, frame0)
	for i := range frame1 {
		if dec1[i] != frame1[i] {
			t.Fatalf("frame1 mismatch at %d: got %d, want %d", i, dec1[i], frame1[i])
		}
	}
}

func TestTemporalDeltaEdgeCases(t *testing.T) {
	// Test with large differences (near uint16 boundaries)
	frame0 := []uint16{0, 65535, 32768, 100}
	frame1 := []uint16{65535, 0, 32769, 99}

	enc := TemporalDeltaEncode(frame1, frame0)
	dec := TemporalDeltaDecode(enc, frame0)
	for i := range frame1 {
		if dec[i] != frame1[i] {
			t.Fatalf("edge case mismatch at %d: got %d, want %d", i, dec[i], frame1[i])
		}
	}
}

func TestMIC2HeaderRoundtrip(t *testing.T) {
	hdr := MIC2Header{
		Width:      1890,
		Height:     2457,
		FrameCount: 3,
		Temporal:   true,
	}
	frameBlobs := [][]byte{
		{1, 2, 3, 4, 5},
		{6, 7, 8},
		{9, 10, 11, 12},
	}

	var buf bytes.Buffer
	if err := WriteMIC2(&buf, hdr, frameBlobs); err != nil {
		t.Fatal(err)
	}
	data := buf.Bytes()

	hdr2, entries, dataOffset, err := ReadMIC2Header(data)
	if err != nil {
		t.Fatal(err)
	}

	if hdr2.Width != hdr.Width || hdr2.Height != hdr.Height ||
		hdr2.FrameCount != hdr.FrameCount || hdr2.Temporal != hdr.Temporal {
		t.Fatalf("header mismatch: got %+v, want %+v", hdr2, hdr)
	}

	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}

	// Verify frame extraction
	for i, expected := range frameBlobs {
		got, err := ExtractFrame(data, entries, dataOffset, i)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != len(expected) {
			t.Fatalf("frame %d length mismatch: got %d, want %d", i, len(got), len(expected))
		}
		for j := range got {
			if got[j] != expected[j] {
				t.Fatalf("frame %d byte %d mismatch", i, j)
			}
		}
	}
}

// makeSmoothFrames generates synthetic frames with spatial correlation (smooth gradients
// with noise) and optional small inter-frame differences. This is needed because
// pure random data is not compressible by the Delta+RLE+FSE pipeline.
func makeSmoothFrames(width, height, nFrames int, seed int64) ([][]uint16, uint16) {
	rng := rand.New(rand.NewSource(seed))
	frames := make([][]uint16, nFrames)
	var maxValue uint16

	// Frame 0: smooth gradient + noise
	frames[0] = make([]uint16, width*height)
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			base := uint16((y*300)/height + (x*200)/width)
			noise := uint16(rng.Intn(20))
			v := base + noise
			frames[0][y*width+x] = v
			if v > maxValue {
				maxValue = v
			}
		}
	}

	// Subsequent frames: small perturbations of previous frame
	for f := 1; f < nFrames; f++ {
		frames[f] = make([]uint16, width*height)
		for i := range frames[f] {
			diff := int32(rng.Intn(11)) - 5
			v := uint16(int32(frames[f-1][i]) + diff)
			frames[f][i] = v
			if v > maxValue {
				maxValue = v
			}
		}
	}
	return frames, maxValue
}

func TestMultiFrameIndependentRoundtrip(t *testing.T) {
	width, height := 128, 128
	nFrames := 5
	frames, maxValue := makeSmoothFrames(width, height, nFrames, 123)

	// Compress independently
	compressed, err := CompressMultiFrame(frames, width, height, maxValue, false)
	if err != nil {
		t.Fatal(err)
	}

	// Decompress all
	decoded, hdr, err := DecompressMultiFrame(compressed)
	if err != nil {
		t.Fatal(err)
	}

	if hdr.FrameCount != nFrames || hdr.Temporal {
		t.Fatalf("unexpected header: %+v", hdr)
	}

	for f := 0; f < nFrames; f++ {
		for i := range frames[f] {
			if decoded[f][i] != frames[f][i] {
				t.Fatalf("frame %d pixel %d mismatch: got %d, want %d", f, i, decoded[f][i], frames[f][i])
			}
		}
	}

	// Test single-frame random access
	for _, idx := range []int{0, 2, 4} {
		single, _, err := DecompressFrame(compressed, idx)
		if err != nil {
			t.Fatal(err)
		}
		for i := range frames[idx] {
			if single[i] != frames[idx][i] {
				t.Fatalf("single frame %d pixel %d mismatch", idx, i)
			}
		}
	}
}

func TestMultiFrameTemporalRoundtrip(t *testing.T) {
	width, height := 128, 128
	nFrames := 5
	frames, maxValue := makeSmoothFrames(width, height, nFrames, 456)

	// Compress with temporal prediction
	compressed, err := CompressMultiFrame(frames, width, height, maxValue, true)
	if err != nil {
		t.Fatal(err)
	}

	// Decompress all
	decoded, hdr, err := DecompressMultiFrame(compressed)
	if err != nil {
		t.Fatal(err)
	}

	if !hdr.Temporal {
		t.Fatal("expected temporal flag")
	}

	for f := 0; f < nFrames; f++ {
		for i := range frames[f] {
			if decoded[f][i] != frames[f][i] {
				t.Fatalf("frame %d pixel %d mismatch: got %d, want %d", f, i, decoded[f][i], frames[f][i])
			}
		}
	}

	// Test single-frame decode (temporal mode - sequential)
	for _, idx := range []int{0, 2, 4} {
		single, _, err := DecompressFrame(compressed, idx)
		if err != nil {
			t.Fatal(err)
		}
		for i := range frames[idx] {
			if single[i] != frames[idx][i] {
				t.Fatalf("single frame %d pixel %d mismatch", idx, i)
			}
		}
	}

	// Compare compression ratio: temporal should be better
	indepCompressed, err := CompressMultiFrame(frames, width, height, maxValue, false)
	if err != nil {
		t.Fatal(err)
	}

	rawSize := nFrames * width * height * 2
	t.Logf("Temporal: %d bytes (%.2f:1)", len(compressed), float64(rawSize)/float64(len(compressed)))
	t.Logf("Independent: %d bytes (%.2f:1)", len(indepCompressed), float64(rawSize)/float64(len(indepCompressed)))
}

// ReadDicomMultiFrame reads all frames from a multiframe DICOM file.
func ReadDicomMultiFrame(fileName string) ([][]uint16, int, int, uint16, error) {
	dataset, err := dicom.ParseFile(fileName, nil)
	if err != nil {
		return nil, 0, 0, 0, fmt.Errorf("parse DICOM: %w", err)
	}

	pixelDataElement, err := dataset.FindElementByTag(tag.PixelData)
	if err != nil {
		return nil, 0, 0, 0, fmt.Errorf("find pixel data: %w", err)
	}

	pixelDataInfo := dicom.MustGetPixelDataInfo(pixelDataElement.Value)
	if len(pixelDataInfo.Frames) == 0 {
		return nil, 0, 0, 0, fmt.Errorf("no frames in DICOM file")
	}

	// Get dimensions from first frame
	firstFrame, err := pixelDataInfo.Frames[0].GetNativeFrame()
	if err != nil {
		return nil, 0, 0, 0, fmt.Errorf("get first frame: %w", err)
	}
	width := firstFrame.Cols
	height := firstFrame.Rows

	var maxValue uint16
	frames := make([][]uint16, len(pixelDataInfo.Frames))

	for f, fr := range pixelDataInfo.Frames {
		nativeFrame, err := fr.GetNativeFrame()
		if err != nil {
			return nil, 0, 0, 0, fmt.Errorf("get frame %d: %w", f, err)
		}
		pixels := make([]uint16, width*height)
		for j := 0; j < len(nativeFrame.Data); j++ {
			v := uint16(nativeFrame.Data[j][0])
			pixels[j] = v
			if v > maxValue {
				maxValue = v
			}
		}
		frames[f] = pixels
	}

	return frames, width, height, maxValue, nil
}

func TestMultiFrameTomoCompress(t *testing.T) {
	fileName := "testdata/Series 73200000 [MG - R CC Breast Tomosynthesis Image]/1.3.6.1.4.1.5962.99.1.2280943358.716200484.1363785608958.647.0.dcm"
	if _, err := os.Stat(fileName); errors.Is(err, os.ErrNotExist) {
		t.Skip("Skipping: multiframe DICOM test file not present")
	}

	frames, width, height, maxValue, err := ReadDicomMultiFrame(fileName)
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("Loaded %d frames, %dx%d, maxValue=%d", len(frames), width, height, maxValue)

	rawSize := len(frames) * width * height * 2

	// Compress independently
	indepComp, err := CompressMultiFrame(frames, width, height, maxValue, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Independent: %d bytes (%.2f:1)", len(indepComp), float64(rawSize)/float64(len(indepComp)))

	// Verify independent roundtrip (spot check first, middle, last frame)
	for _, idx := range []int{0, len(frames) / 2, len(frames) - 1} {
		decoded, _, err := DecompressFrame(indepComp, idx)
		if err != nil {
			t.Fatalf("decompress frame %d: %v", idx, err)
		}
		for i := range frames[idx] {
			if decoded[i] != frames[idx][i] {
				t.Fatalf("independent frame %d pixel %d mismatch: got %d, want %d", idx, i, decoded[i], frames[idx][i])
			}
		}
	}

	// Compress with temporal prediction
	tempComp, err := CompressMultiFrame(frames, width, height, maxValue, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Temporal: %d bytes (%.2f:1)", len(tempComp), float64(rawSize)/float64(len(tempComp)))

	// Verify temporal roundtrip
	decodedAll, _, err := DecompressMultiFrame(tempComp)
	if err != nil {
		t.Fatal(err)
	}
	for f := range frames {
		for i := range frames[f] {
			if decodedAll[f][i] != frames[f][i] {
				t.Fatalf("temporal frame %d pixel %d mismatch: got %d, want %d", f, i, decodedAll[f][i], frames[f][i])
			}
		}
	}

	t.Logf("Temporal improvement: %.1f%%", (1.0-float64(len(tempComp))/float64(len(indepComp)))*100)
}
