// mic-compress compresses 16-bit medical images using the Delta+RLE+FSE pipeline
// and writes .mic container files that can be decoded in a web browser.
//
// Usage:
//
//	mic-compress -input image.bin -width 512 -height 512 -output image.mic
//	mic-compress -dicom study.dcm -output study.mic [-temporal]
//	mic-compress -testdata   # compress all test images to web/testdata/
package main

import (
	"encoding/binary"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"

	"mic"

	"github.com/suyashkumar/dicom"
	"github.com/suyashkumar/dicom/pkg/tag"
)

// .mic container format (MIC1 - single frame):
//
//	Bytes 0-3:   Magic "MIC1" (0x4D 0x49 0x43 0x31)
//	Bytes 4-7:   Width  (uint32 LE)
//	Bytes 8-11:  Height (uint32 LE)
//	Bytes 12-15: Pipeline type (uint32 LE): 1=Delta+RLE+FSE
//	Bytes 16-19: Compressed data length (uint32 LE)
//	Bytes 20+:   FSE compressed data
func writeMicFile(filename string, width, height int, compressed []byte) error {
	f, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer f.Close()

	header := make([]byte, 20)
	// Magic "MIC1"
	header[0] = 'M'
	header[1] = 'I'
	header[2] = 'C'
	header[3] = '1'
	binary.LittleEndian.PutUint32(header[4:8], uint32(width))
	binary.LittleEndian.PutUint32(header[8:12], uint32(height))
	binary.LittleEndian.PutUint32(header[12:16], 1) // pipeline: Delta+RLE+FSE
	binary.LittleEndian.PutUint32(header[16:20], uint32(len(compressed)))

	if _, err := f.Write(header); err != nil {
		return err
	}
	if _, err := f.Write(compressed); err != nil {
		return err
	}
	return nil
}

// writeMICRFile writes a MICR single-frame RGB container file.
//
// MICR format:
//
//	Bytes 0-3:   Magic "MICR" (0x5243494D)
//	Bytes 4-7:   Width  (uint32 LE)
//	Bytes 8-11:  Height (uint32 LE)
//	Bytes 12+:   CompressRGB blob ([Y_len][Co_len][Cg_len][Y_data][Co_data][Cg_data])
func writeMICRFile(filename string, width, height int, blob []byte) error {
	f, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer f.Close()

	header := make([]byte, 12)
	header[0] = 'M'
	header[1] = 'I'
	header[2] = 'C'
	header[3] = 'R'
	binary.LittleEndian.PutUint32(header[4:8], uint32(width))
	binary.LittleEndian.PutUint32(header[8:12], uint32(height))

	if _, err := f.Write(header); err != nil {
		return err
	}
	if _, err := f.Write(blob); err != nil {
		return err
	}
	return nil
}

func compressImage(shortData []uint16, width, height int, maxValue uint16) ([]byte, error) {
	return mic.CompressSingleFrame(shortData, width, height, maxValue)
}

func compressImage4State(shortData []uint16, width, height int, maxValue uint16) ([]byte, error) {
	return mic.CompressSingleFrame4State(shortData, width, height, maxValue)
}

// readDicomMultiFrame reads all frames from a multiframe DICOM file.
func readDicomMultiFrame(fileName string) ([][]uint16, int, int, uint16, error) {
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

// readDicomSeries reads all single-frame DICOM files from a directory,
// orders them by InstanceNumber, and returns the assembled frames.
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
			return nil, 0, 0, 0, fmt.Errorf("parse %s: %w", e.Name(), err)
		}
		el, err := dataset.FindElementByTag(tag.InstanceNumber)
		if err != nil {
			return nil, 0, 0, 0, fmt.Errorf("no InstanceNumber in %s: %w", e.Name(), err)
		}
		instNum, err := strconv.Atoi(fmt.Sprintf("%v", el.Value.GetValue().([]string)[0]))
		if err != nil {
			return nil, 0, 0, 0, fmt.Errorf("parse InstanceNumber in %s: %w", e.Name(), err)
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
	var maxValue uint16
	frames := make([][]uint16, len(dcmFiles))

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
}

// Multiframe DICOM test images (single multiframe DICOM file)
var dicomTestImages = []struct {
	name string
	file string
}{
	{
		name: "MG_TOMO",
		file: "testdata/Series 73200000 [MG - R CC Breast Tomosynthesis Image]/1.3.6.1.4.1.5962.99.1.2280943358.716200484.1363785608958.647.0.dcm",
	},
}

// Multiframe DICOM series (directory of individual DICOM files)
var dicomSeriesImages = []struct {
	name string
	dir  string
}{
	{
		name: "CT_MULTI",
		dir:  "testdata/0acbebb8d463b4b9ca88cf38431aac69",
	},
}

// PICS parallel strip test images
var picsTestImages = []struct {
	srcName   string
	numStrips int
	outName   string
}{
	{srcName: "MR", numStrips: 4, outName: "MR_pics4"},
	{srcName: "CT", numStrips: 4, outName: "CT_pics4"},
	{srcName: "CR", numStrips: 8, outName: "CR_pics8"},
	{srcName: "MG1", numStrips: 8, outName: "MG1_pics8"},
	{srcName: "MG2", numStrips: 8, outName: "MG2_pics8"},
	{srcName: "MG3", numStrips: 8, outName: "MG3_pics8"},
}

// WSI test images (raw RGB files)
var wsiTestImages = []struct {
	name     string
	file     string
	width    int
	height   int
	channels int
}{
	{name: "WSI_TISSUE", file: "testdata/wsi_tissue_512x384.rgb", width: 512, height: 384, channels: 3},
}

// RGB TIFF test images (Ultrasound and Visible Light from NEMA compsamples)
var rgbTIFFTestImages = []struct {
	name string
	file string
}{
	{name: "US1", file: "testdata/compsamples_refanddir/images/ref/US1_UNC"},
	{name: "VL1", file: "testdata/compsamples_refanddir/images/ref/VL1_UNC"},
	{name: "VL2", file: "testdata/compsamples_refanddir/images/ref/VL2_UNC"},
	{name: "VL3", file: "testdata/compsamples_refanddir/images/ref/VL3_UNC"},
	{name: "VL4", file: "testdata/compsamples_refanddir/images/ref/VL4_UNC"},
	{name: "VL5", file: "testdata/compsamples_refanddir/images/ref/VL5_UNC"},
	{name: "VL6", file: "testdata/compsamples_refanddir/images/ref/VL6_UNC"},
}

// readTIFFRGB reads an uncompressed 8-bit RGB TIFF and returns interleaved RGB bytes.
// The NEMA compsamples TIFFs use BitsPerSample with count=1 (a non-standard shorthand),
// so we parse the IFD directly and copy raw strip data.
func readTIFFRGB(fileName string) ([]byte, int, int, error) {
	data, err := os.ReadFile(fileName)
	if err != nil {
		return nil, 0, 0, err
	}
	if len(data) < 8 {
		return nil, 0, 0, fmt.Errorf("file too small")
	}

	var order binary.ByteOrder
	if data[0] == 'I' {
		order = binary.LittleEndian
	} else {
		order = binary.BigEndian
	}

	readU16 := func(off int) uint16 { return order.Uint16(data[off:]) }
	readU32 := func(off int) uint32 { return order.Uint32(data[off:]) }

	ifdOffset := int(readU32(4))
	numEntries := int(readU16(ifdOffset))

	var width, height, compression, samplesPerPixel int
	var stripOffsets, stripByteCounts []uint32

	pos := ifdOffset + 2
	for i := 0; i < numEntries; i++ {
		tag := readU16(pos)
		ftype := readU16(pos + 2)
		count := int(readU32(pos + 4))
		valueOff := pos + 8

		readShort := func() uint16 {
			if ftype == 3 {
				return readU16(valueOff)
			}
			return uint16(readU32(valueOff))
		}

		readShortArray := func() []uint32 {
			out := make([]uint32, count)
			if count == 1 {
				if ftype == 3 {
					out[0] = uint32(readU16(valueOff))
				} else {
					out[0] = readU32(valueOff)
				}
				return out
			}
			offset := int(readU32(valueOff))
			for k := 0; k < count; k++ {
				if ftype == 3 {
					out[k] = uint32(readU16(offset + k*2))
				} else {
					out[k] = readU32(offset + k*4)
				}
			}
			return out
		}

		switch tag {
		case 256:
			width = int(readShort())
		case 257:
			height = int(readShort())
		case 259:
			compression = int(readShort())
		case 273:
			stripOffsets = readShortArray()
		case 277:
			samplesPerPixel = int(readShort())
		case 279:
			stripByteCounts = readShortArray()
		}
		pos += 12
	}

	if compression != 1 || samplesPerPixel != 3 || width == 0 || height == 0 {
		return nil, 0, 0, fmt.Errorf("unsupported TIFF (compression=%d, spp=%d, %dx%d)", compression, samplesPerPixel, width, height)
	}

	rgb := make([]byte, 0, width*height*3)
	for i, off := range stripOffsets {
		end := int(off) + int(stripByteCounts[i])
		if end > len(data) {
			return nil, 0, 0, fmt.Errorf("strip out of bounds")
		}
		rgb = append(rgb, data[off:end]...)
	}
	if len(rgb) != width*height*3 {
		return nil, 0, 0, fmt.Errorf("unexpected RGB size: got %d, want %d", len(rgb), width*height*3)
	}
	return rgb, width, height, nil
}

func main() {
	inputFile := flag.String("input", "", "Input binary image file (raw uint16 LE pixels)")
	dicomFile := flag.String("dicom", "", "Input DICOM file (reads pixel data and dimensions automatically)")
	temporal := flag.Bool("temporal", false, "Use inter-frame temporal prediction (multiframe only)")
	width := flag.Int("width", 0, "Image width in pixels")
	height := flag.Int("height", 0, "Image height in pixels")
	outputFile := flag.String("output", "", "Output .mic file")
	genTestData := flag.Bool("testdata", false, "Generate test .mic files from built-in test images")
	flag.Parse()

	if *genTestData {
		outDir := "web/testdata"
		os.MkdirAll(outDir, 0755)

		// Compress single-frame test images (MIC1)
		for _, img := range testImages {
			fmt.Printf("Compressing %s (%dx%d)...\n", img.name, img.cols, img.rows)

			byteData, err := os.ReadFile(img.file)
			if err != nil {
				fmt.Fprintf(os.Stderr, "  skip %s: %v\n", img.name, err)
				continue
			}

			shortData := make([]uint16, img.cols*img.rows)
			var maxValue uint16
			for i := 0; i < len(byteData); i += 2 {
				v := uint16(byteData[i]) | (uint16(byteData[i+1]) << 8)
				shortData[i/2] = v
				if v > maxValue {
					maxValue = v
				}
			}

			compressed, err := compressImage(shortData, img.cols, img.rows, maxValue)
			if err != nil {
				fmt.Fprintf(os.Stderr, "  error compressing %s: %v\n", img.name, err)
				continue
			}

			outPath := filepath.Join(outDir, img.name+".mic")
			if err := writeMicFile(outPath, img.cols, img.rows, compressed); err != nil {
				fmt.Fprintf(os.Stderr, "  error writing %s: %v\n", outPath, err)
				continue
			}

			ratio := float64(len(byteData)) / float64(len(compressed))
			fmt.Printf("  %s: %d bytes -> %d bytes (%.2f:1) -> %s\n",
				img.name, len(byteData), len(compressed), ratio, outPath)
		}

		// Compress single-frame test images with 4-state FSE (MIC1, suffix _4s)
		for _, img := range testImages {
			fmt.Printf("Compressing %s 4-state (%dx%d)...\n", img.name, img.cols, img.rows)

			byteData, err := os.ReadFile(img.file)
			if err != nil {
				fmt.Fprintf(os.Stderr, "  skip %s: %v\n", img.name, err)
				continue
			}

			shortData := make([]uint16, img.cols*img.rows)
			var maxValue uint16
			for i := 0; i < len(byteData); i += 2 {
				v := uint16(byteData[i]) | (uint16(byteData[i+1]) << 8)
				shortData[i/2] = v
				if v > maxValue {
					maxValue = v
				}
			}

			compressed, err := compressImage4State(shortData, img.cols, img.rows, maxValue)
			if err != nil {
				fmt.Fprintf(os.Stderr, "  error compressing %s: %v\n", img.name, err)
				continue
			}

			outPath := filepath.Join(outDir, img.name+"_4s.mic")
			if err := writeMicFile(outPath, img.cols, img.rows, compressed); err != nil {
				fmt.Fprintf(os.Stderr, "  error writing %s: %v\n", outPath, err)
				continue
			}

			ratio := float64(len(byteData)) / float64(len(compressed))
			fmt.Printf("  %s: %d bytes -> %d bytes (%.2f:1) -> %s\n",
				img.name, len(byteData), len(compressed), ratio, outPath)
		}

		// Compress PICS parallel-strip test images (4-state FSE per strip)
		srcMap := make(map[string]testImage)
		for _, img := range testImages {
			srcMap[img.name] = img
		}
		for _, p := range picsTestImages {
			img, ok := srcMap[p.srcName]
			if !ok {
				continue
			}
			fmt.Printf("Compressing PICS %s (%d strips, 4-state)...\n", p.outName, p.numStrips)

			byteData, err := os.ReadFile(img.file)
			if err != nil {
				fmt.Fprintf(os.Stderr, "  skip %s: %v\n", p.outName, err)
				continue
			}

			shortData := make([]uint16, img.cols*img.rows)
			var maxValue uint16
			for i := 0; i < len(byteData); i += 2 {
				v := uint16(byteData[i]) | (uint16(byteData[i+1]) << 8)
				shortData[i/2] = v
				if v > maxValue {
					maxValue = v
				}
			}

			compressed, err := mic.CompressParallelStrips4State(shortData, img.cols, img.rows, maxValue, p.numStrips)
			if err != nil {
				fmt.Fprintf(os.Stderr, "  error compressing %s: %v\n", p.outName, err)
				continue
			}

			outPath := filepath.Join(outDir, p.outName+".mic")
			if err := os.WriteFile(outPath, compressed, 0644); err != nil {
				fmt.Fprintf(os.Stderr, "  error writing %s: %v\n", outPath, err)
				continue
			}

			ratio := float64(len(byteData)) / float64(len(compressed))
			fmt.Printf("  %s: %d bytes -> %d bytes (%.2f:1) -> %s\n",
				p.outName, len(byteData), len(compressed), ratio, outPath)
		}

		// Compress multiframe DICOM test images (MIC2)
		for _, img := range dicomTestImages {
			fmt.Printf("Compressing multiframe %s...\n", img.name)

			if _, err := os.Stat(img.file); os.IsNotExist(err) {
				fmt.Fprintf(os.Stderr, "  skip %s: file not found\n", img.name)
				continue
			}

			frames, w, h, maxVal, err := readDicomMultiFrame(img.file)
			if err != nil {
				fmt.Fprintf(os.Stderr, "  error reading %s: %v\n", img.name, err)
				continue
			}

			rawSize := len(frames) * w * h * 2
			fmt.Printf("  %d frames, %dx%d, maxValue=%d, raw=%d bytes\n",
				len(frames), w, h, maxVal, rawSize)

			// Compress independently (MIC2)
			compressed, err := mic.CompressMultiFrame(frames, w, h, maxVal, false)
			if err != nil {
				fmt.Fprintf(os.Stderr, "  error compressing %s: %v\n", img.name, err)
				continue
			}

			outPath := filepath.Join(outDir, img.name+".mic")
			if err := os.WriteFile(outPath, compressed, 0644); err != nil {
				fmt.Fprintf(os.Stderr, "  error writing %s: %v\n", outPath, err)
				continue
			}

			ratio := float64(rawSize) / float64(len(compressed))
			fmt.Printf("  %s: %d bytes -> %d bytes (%.2f:1) -> %s\n",
				img.name, rawSize, len(compressed), ratio, outPath)
		}

		// Compress multiframe DICOM series (directory of individual files, MIC2)
		for _, img := range dicomSeriesImages {
			fmt.Printf("Compressing series %s...\n", img.name)

			if _, err := os.Stat(img.dir); os.IsNotExist(err) {
				fmt.Fprintf(os.Stderr, "  skip %s: directory not found\n", img.name)
				continue
			}

			frames, w, h, maxVal, err := readDicomSeries(img.dir)
			if err != nil {
				fmt.Fprintf(os.Stderr, "  error reading %s: %v\n", img.name, err)
				continue
			}

			rawSize := len(frames) * w * h * 2
			fmt.Printf("  %d frames, %dx%d, maxValue=%d, raw=%d bytes\n",
				len(frames), w, h, maxVal, rawSize)

			compressed, err := mic.CompressMultiFrame(frames, w, h, maxVal, false)
			if err != nil {
				fmt.Fprintf(os.Stderr, "  error compressing %s: %v\n", img.name, err)
				continue
			}

			outPath := filepath.Join(outDir, img.name+".mic")
			if err := os.WriteFile(outPath, compressed, 0644); err != nil {
				fmt.Fprintf(os.Stderr, "  error writing %s: %v\n", outPath, err)
				continue
			}

			ratio := float64(rawSize) / float64(len(compressed))
			fmt.Printf("  %s: %d bytes -> %d bytes (%.2f:1) -> %s\n",
				img.name, rawSize, len(compressed), ratio, outPath)
		}

		// Compress RGB TIFF test images as MICR (US, VL modalities)
		for _, img := range rgbTIFFTestImages {
			fmt.Printf("Compressing RGB TIFF %s...\n", img.name)

			rgb, w, h, err := readTIFFRGB(img.file)
			if err != nil {
				fmt.Fprintf(os.Stderr, "  skip %s: %v\n", img.name, err)
				continue
			}

			compressed, err := mic.CompressRGB(rgb, w, h)
			if err != nil {
				fmt.Fprintf(os.Stderr, "  error compressing %s: %v\n", img.name, err)
				continue
			}

			outPath := filepath.Join(outDir, img.name+".mic")
			if err := writeMICRFile(outPath, w, h, compressed); err != nil {
				fmt.Fprintf(os.Stderr, "  error writing %s: %v\n", outPath, err)
				continue
			}

			rawSize := len(rgb)
			ratio := float64(rawSize) / float64(len(compressed))
			fmt.Printf("  %s: %dx%d  %d bytes -> %d bytes (%.2f:1) -> %s\n",
				img.name, w, h, rawSize, len(compressed), ratio, outPath)
		}

		// Compress WSI test images (MIC3)
		for _, img := range wsiTestImages {
			fmt.Printf("Compressing WSI %s (%dx%d)...\n", img.name, img.width, img.height)

			byteData, err := os.ReadFile(img.file)
			if err != nil {
				fmt.Fprintf(os.Stderr, "  skip %s: %v\n", img.name, err)
				continue
			}

			compressed, err := mic.CompressWSI(byteData, img.width, img.height, img.channels, 8, mic.WSIOptions{})
			if err != nil {
				fmt.Fprintf(os.Stderr, "  error compressing %s: %v\n", img.name, err)
				continue
			}

			outPath := filepath.Join(outDir, img.name+".mic")
			if err := os.WriteFile(outPath, compressed, 0644); err != nil {
				fmt.Fprintf(os.Stderr, "  error writing %s: %v\n", outPath, err)
				continue
			}

			ratio := float64(len(byteData)) / float64(len(compressed))
			fmt.Printf("  %s: %d bytes -> %d bytes (%.2f:1) -> %s\n",
				img.name, len(byteData), len(compressed), ratio, outPath)
		}
		return
	}

	// DICOM input mode
	if *dicomFile != "" {
		if *outputFile == "" {
			fmt.Fprintln(os.Stderr, "Usage: mic-compress -dicom study.dcm -output out.mic [-temporal]")
			os.Exit(1)
		}

		frames, w, h, maxVal, err := readDicomMultiFrame(*dicomFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading DICOM: %v\n", err)
			os.Exit(1)
		}

		rawSize := len(frames) * w * h * 2
		fmt.Printf("Read %d frames, %dx%d, maxValue=%d\n", len(frames), w, h, maxVal)

		if len(frames) == 1 {
			// Single frame: write MIC1
			compressed, err := compressImage(frames[0], w, h, maxVal)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Compression error: %v\n", err)
				os.Exit(1)
			}
			if err := writeMicFile(*outputFile, w, h, compressed); err != nil {
				fmt.Fprintf(os.Stderr, "Error writing: %v\n", err)
				os.Exit(1)
			}
			ratio := float64(rawSize) / float64(len(compressed))
			fmt.Printf("Compressed: %d bytes -> %d bytes (%.2f:1) -> %s\n",
				rawSize, len(compressed), ratio, *outputFile)
		} else {
			// Multi-frame: write MIC2
			mode := "independent"
			if *temporal {
				mode = "temporal"
			}
			fmt.Printf("Compressing %d frames (%s mode)...\n", len(frames), mode)

			compressed, err := mic.CompressMultiFrame(frames, w, h, maxVal, *temporal)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Compression error: %v\n", err)
				os.Exit(1)
			}
			if err := os.WriteFile(*outputFile, compressed, 0644); err != nil {
				fmt.Fprintf(os.Stderr, "Error writing: %v\n", err)
				os.Exit(1)
			}
			ratio := float64(rawSize) / float64(len(compressed))
			fmt.Printf("Compressed: %d bytes -> %d bytes (%.2f:1) -> %s\n",
				rawSize, len(compressed), ratio, *outputFile)
		}
		return
	}

	// Raw binary input mode
	if *inputFile == "" || *width == 0 || *height == 0 || *outputFile == "" {
		fmt.Fprintln(os.Stderr, "Usage: mic-compress -input image.bin -width W -height H -output out.mic")
		fmt.Fprintln(os.Stderr, "       mic-compress -dicom study.dcm -output out.mic [-temporal]")
		fmt.Fprintln(os.Stderr, "       mic-compress -testdata")
		flag.PrintDefaults()
		os.Exit(1)
	}

	byteData, err := os.ReadFile(*inputFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading %s: %v\n", *inputFile, err)
		os.Exit(1)
	}

	expectedBytes := (*width) * (*height) * 2
	if len(byteData) != expectedBytes {
		fmt.Fprintf(os.Stderr, "File size %d does not match %dx%dx2=%d\n",
			len(byteData), *width, *height, expectedBytes)
		os.Exit(1)
	}

	shortData := make([]uint16, (*width)*(*height))
	var maxValue uint16
	for i := 0; i < len(byteData); i += 2 {
		v := uint16(byteData[i]) | (uint16(byteData[i+1]) << 8)
		shortData[i/2] = v
		if v > maxValue {
			maxValue = v
		}
	}

	compressed, err := compressImage(shortData, *width, *height, maxValue)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Compression error: %v\n", err)
		os.Exit(1)
	}

	if err := writeMicFile(*outputFile, *width, *height, compressed); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing %s: %v\n", *outputFile, err)
		os.Exit(1)
	}

	ratio := float64(len(byteData)) / float64(len(compressed))
	fmt.Printf("Compressed: %d bytes -> %d bytes (%.2f:1) -> %s\n",
		len(byteData), len(compressed), ratio, *outputFile)
}
