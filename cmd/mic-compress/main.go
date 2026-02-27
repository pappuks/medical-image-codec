// mic-compress compresses 16-bit medical images using the Delta+RLE+FSE pipeline
// and writes .mic container files that can be decoded in a web browser.
//
// Usage:
//
//	mic-compress -input image.bin -width 512 -height 512 -output image.mic
//	mic-compress -testdata   # compress all test images to web/testdata/
package main

import (
	"encoding/binary"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"mic"
)

// .mic container format:
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

func compressImage(shortData []uint16, width, height int, maxValue uint16) ([]byte, error) {
	var drc mic.DeltaRleCompressU16
	deltaComp, err := drc.Compress(shortData, width, height, maxValue)
	if err != nil {
		return nil, fmt.Errorf("delta+RLE compress: %w", err)
	}

	var s mic.ScratchU16
	fseComp, err := mic.FSECompressU16(deltaComp, &s)
	if err != nil {
		return nil, fmt.Errorf("FSE compress: %w", err)
	}

	return fseComp, nil
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

func main() {
	inputFile := flag.String("input", "", "Input binary image file (raw uint16 LE pixels)")
	width := flag.Int("width", 0, "Image width in pixels")
	height := flag.Int("height", 0, "Image height in pixels")
	outputFile := flag.String("output", "", "Output .mic file")
	genTestData := flag.Bool("testdata", false, "Generate test .mic files from built-in test images")
	flag.Parse()

	if *genTestData {
		outDir := "web/testdata"
		os.MkdirAll(outDir, 0755)

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
		return
	}

	if *inputFile == "" || *width == 0 || *height == 0 || *outputFile == "" {
		fmt.Fprintln(os.Stderr, "Usage: mic-compress -input image.bin -width W -height H -output out.mic")
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
