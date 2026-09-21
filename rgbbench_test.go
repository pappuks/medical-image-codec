// Copyright 2024 Kuldeep Singh
// This source code is licensed under a MIT-style
// license that can be found in the LICENSE file.

package mic

// RGB benchmark suite for natural-color medical images (Ultrasound, Visible Light).
//
// Each benchmark applies YCoCg-R to decompose the image into Y/Co/Cg planes,
// then compresses each plane with the specified pipeline variant. Metrics:
//   - ratio:    (raw bytes) / (total compressed bytes across all 3 planes)
//   - original: raw uncompressed MB
//   - comp:     compressed MB
//   - fps:      decompression frames per second

import (
	"encoding/binary"
	"os"
	"sync"
	"testing"
)

// rgbTestData describes a TIFF RGB test image.
type rgbTestData struct {
	name     string
	fileName string
}

var rgbTestFiles = []rgbTestData{
	{name: "US1", fileName: "testdata/compsamples_refanddir/images/ref/US1_UNC"},
	{name: "VL1", fileName: "testdata/compsamples_refanddir/images/ref/VL1_UNC"},
	{name: "VL2", fileName: "testdata/compsamples_refanddir/images/ref/VL2_UNC"},
	{name: "VL3", fileName: "testdata/compsamples_refanddir/images/ref/VL3_UNC"},
	{name: "VL4", fileName: "testdata/compsamples_refanddir/images/ref/VL4_UNC"},
	{name: "VL5", fileName: "testdata/compsamples_refanddir/images/ref/VL5_UNC"},
	{name: "VL6", fileName: "testdata/compsamples_refanddir/images/ref/VL6_UNC"},
}

// ReadTIFFRGB reads an uncompressed 8-bit RGB TIFF and returns interleaved RGB bytes.
//
// The NEMA compsamples TIFFs use BitsPerSample with count=1 (a non-standard
// shorthand meaning "all channels share this depth"), which most TIFF libraries
// reject. We parse the IFD directly and copy raw strip data instead.
func ReadTIFFRGB(fileName string) ([]byte, int, int) {
	data, err := os.ReadFile(fileName)
	if err != nil {
		return nil, 0, 0
	}
	if len(data) < 8 {
		return nil, 0, 0
	}

	// Determine byte order.
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
		readLong := func() uint32 { return readU32(valueOff) }

		readShortArray := func() []uint32 {
			out := make([]uint32, count)
			if count == 1 {
				if ftype == 3 { // SHORT
					out[0] = uint32(readU16(valueOff))
				} else { // LONG or other
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
		case 278:
			_ = readLong() // rowsPerStrip — unused
		}
		pos += 12
	}

	if compression != 1 || samplesPerPixel != 3 || width == 0 || height == 0 {
		return nil, 0, 0
	}

	// Concatenate strips into one RGB buffer.
	rgb := make([]byte, 0, width*height*3)
	for i, off := range stripOffsets {
		end := int(off) + int(stripByteCounts[i])
		if end > len(data) {
			return nil, 0, 0
		}
		rgb = append(rgb, data[off:end]...)
	}
	if len(rgb) != width*height*3 {
		return nil, 0, 0
	}
	return rgb, width, height
}

// SetupRGBTest loads an RGB test image, applies YCoCg-R, and returns the three planes.
// rawBytes is the uncompressed size = width*height*3.
func SetupRGBTest(td rgbTestData) (yPlane, coPlane, cgPlane []uint16, width, height, rawBytes int) {
	rgb, w, h := ReadTIFFRGB(td.fileName)
	if rgb == nil {
		return nil, nil, nil, 0, 0, 0
	}
	y, co, cg := YCoCgRForward(rgb, w, h)
	return y, co, cg, w, h, len(rgb)
}

// compressedSizeRGBHuff compresses all 3 planes with DeltaRLE+Huffman and returns sizes.
func compressPlanesDeltaRLEHuff(y, co, cg []uint16, w, h int) (yBlob, coBlob, cgBlob []byte) {
	compress := func(plane []uint16, maxVal uint16) []byte {
		var drc DeltaRleCompressU16
		rle, _ := drc.Compress(plane, w, h, maxVal)
		var c CanHuffmanCompressU16
		c.Init(rle)
		c.Compress()
		return c.Out
	}
	yBlob = compress(y, 255)
	coBlob = compress(co, 510)
	cgBlob = compress(cg, 510)
	return
}

func decompressPlanesDeltaRLEHuff(yBlob, coBlob, cgBlob []byte, w, h int) {
	decompress := func(blob []byte) {
		var d CanHuffmanDecompressU16
		d.Init(blob)
		d.ReadTable()
		d.Decompress()
		var drd DeltaRleDecompressU16
		drd.Decompress(d.Out, w, h)
	}
	decompress(yBlob)
	decompress(coBlob)
	decompress(cgBlob)
}

func BenchmarkRGBDeltaRLEHuffCompress(b *testing.B) {
	for _, tf := range rgbTestFiles {
		b.Run(tf.name, func(b *testing.B) {
			yPlane, coPlane, cgPlane, w, h, rawBytes := SetupRGBTest(tf)
			if rawBytes == 0 {
				b.Skipf("could not load %s", tf.fileName)
			}
			yBlob, coBlob, cgBlob := compressPlanesDeltaRLEHuff(yPlane, coPlane, cgPlane, w, h)
			totalComp := len(yBlob) + len(coBlob) + len(cgBlob)
			b.SetBytes(int64(rawBytes))
			b.ResetTimer()
			b.ReportMetric(float64(rawBytes)/float64(totalComp), "ratio")
			var wg sync.WaitGroup
			for i := 0; i < b.N; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					decompressPlanesDeltaRLEHuff(yBlob, coBlob, cgBlob, w, h)
				}()
			}
			wg.Wait()
			b.ReportMetric(float64(rawBytes)/(1<<20), "original")
			b.ReportMetric(float64(totalComp)/(1<<20), "comp")
			b.ReportMetric(1/float64(b.Elapsed().Seconds()/float64(b.N)), "fps")
			b.ReportMetric(0, "allocs/op")
			b.ReportMetric(0, "B/op")
		})
	}
}

func BenchmarkRGBDeltaRLEFSECompress(b *testing.B) {
	for _, tf := range rgbTestFiles {
		b.Run(tf.name, func(b *testing.B) {
			yPlane, coPlane, cgPlane, w, h, rawBytes := SetupRGBTest(tf)
			if rawBytes == 0 {
				b.Skipf("could not load %s", tf.fileName)
			}
			compress := func(plane []uint16, maxVal uint16) []byte {
				var drc DeltaRleCompressU16
				rle, _ := drc.Compress(plane, w, h, maxVal)
				var s ScratchU16
				out, _ := FSECompressU16(rle, &s)
				return out
			}
			yBlob := compress(yPlane, 255)
			coBlob := compress(coPlane, 510)
			cgBlob := compress(cgPlane, 510)
			totalComp := len(yBlob) + len(coBlob) + len(cgBlob)
			b.SetBytes(int64(rawBytes))
			b.ResetTimer()
			b.ReportMetric(float64(rawBytes)/float64(totalComp), "ratio")
			var wg sync.WaitGroup
			for i := 0; i < b.N; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					decompress := func(blob []byte) {
						var s ScratchU16
						rle, _ := FSEDecompressU16(blob, &s)
						var drd DeltaRleDecompressU16
						drd.Decompress(rle, w, h)
					}
					decompress(yBlob)
					decompress(coBlob)
					decompress(cgBlob)
				}()
			}
			wg.Wait()
			b.ReportMetric(float64(rawBytes)/(1<<20), "original")
			b.ReportMetric(float64(totalComp)/(1<<20), "comp")
			b.ReportMetric(1/float64(b.Elapsed().Seconds()/float64(b.N)), "fps")
			b.ReportMetric(0, "allocs/op")
			b.ReportMetric(0, "B/op")
		})
	}
}

func BenchmarkRGBDeltaZZRLEHuffCompress(b *testing.B) {
	for _, tf := range rgbTestFiles {
		b.Run(tf.name, func(b *testing.B) {
			yPlane, coPlane, cgPlane, w, h, rawBytes := SetupRGBTest(tf)
			if rawBytes == 0 {
				b.Skipf("could not load %s", tf.fileName)
			}
			compress := func(plane []uint16, maxVal uint16) []byte {
				var dzz DeltaRleZZU16
				rle, _ := dzz.Compress(plane, w, h, maxVal)
				var c CanHuffmanCompressU16
				c.Init(rle)
				c.Compress()
				return c.Out
			}
			yBlob := compress(yPlane, 255)
			coBlob := compress(coPlane, 510)
			cgBlob := compress(cgPlane, 510)
			totalComp := len(yBlob) + len(coBlob) + len(cgBlob)
			b.SetBytes(int64(rawBytes))
			b.ResetTimer()
			b.ReportMetric(float64(rawBytes)/float64(totalComp), "ratio")
			for i := 0; i < b.N; i++ {
				decompress := func(blob []byte) {
					var d CanHuffmanDecompressU16
					d.Init(blob)
					d.ReadTable()
					d.Decompress()
					var dzzd DeltaRleZZU16
					dzzd.Decompress(d.Out, w, h)
				}
				decompress(yBlob)
				decompress(coBlob)
				decompress(cgBlob)
			}
			b.ReportMetric(float64(rawBytes)/(1<<20), "original")
			b.ReportMetric(float64(totalComp)/(1<<20), "comp")
		})
	}
}

func BenchmarkRGBRLEHuffCompress(b *testing.B) {
	for _, tf := range rgbTestFiles {
		b.Run(tf.name, func(b *testing.B) {
			yPlane, coPlane, cgPlane, w, h, rawBytes := SetupRGBTest(tf)
			if rawBytes == 0 {
				b.Skipf("could not load %s", tf.fileName)
			}
			compress := func(plane []uint16, maxVal uint16) []byte {
				var rle RleCompressU16
				rle.Init(w, h, maxVal)
				rleData := rle.Compress(plane)
				var c CanHuffmanCompressU16
				c.Init(rleData)
				c.Compress()
				return c.Out
			}
			yBlob := compress(yPlane, 255)
			coBlob := compress(coPlane, 510)
			cgBlob := compress(cgPlane, 510)
			totalComp := len(yBlob) + len(coBlob) + len(cgBlob)
			b.SetBytes(int64(rawBytes))
			b.ResetTimer()
			b.ReportMetric(float64(rawBytes)/float64(totalComp), "ratio")
			for i := 0; i < b.N; i++ {
				decompress := func(blob []byte) {
					var d CanHuffmanDecompressU16
					d.Init(blob)
					d.ReadTable()
					d.Decompress()
					var rled RleDecompressU16
					rled.Init(d.Out)
					rled.Decompress()
				}
				decompress(yBlob)
				decompress(coBlob)
				decompress(cgBlob)
			}
			b.ReportMetric(float64(rawBytes)/(1<<20), "original")
			b.ReportMetric(float64(totalComp)/(1<<20), "comp")
		})
	}
}

func BenchmarkRGBDeltaZZRLEFSECompress(b *testing.B) {
	for _, tf := range rgbTestFiles {
		b.Run(tf.name, func(b *testing.B) {
			yPlane, coPlane, cgPlane, w, h, rawBytes := SetupRGBTest(tf)
			if rawBytes == 0 {
				b.Skipf("could not load %s", tf.fileName)
			}
			compress := func(plane []uint16, maxVal uint16) []byte {
				var dzz DeltaRleZZU16
				rle, _ := dzz.Compress(plane, w, h, maxVal)
				var s ScratchU16
				out, _ := FSECompressU16(rle, &s)
				return out
			}
			yBlob := compress(yPlane, 255)
			coBlob := compress(coPlane, 510)
			cgBlob := compress(cgPlane, 510)
			totalComp := len(yBlob) + len(coBlob) + len(cgBlob)
			b.SetBytes(int64(rawBytes))
			b.ResetTimer()
			b.ReportMetric(float64(rawBytes)/float64(totalComp), "ratio")
			for i := 0; i < b.N; i++ {
				decompress := func(blob []byte) {
					var s ScratchU16
					rle, _ := FSEDecompressU16(blob, &s)
					var dzzd DeltaRleZZU16
					dzzd.Decompress(rle, w, h)
				}
				decompress(yBlob)
				decompress(coBlob)
				decompress(cgBlob)
			}
			b.ReportMetric(float64(rawBytes)/(1<<20), "original")
			b.ReportMetric(float64(totalComp)/(1<<20), "comp")
		})
	}
}

func BenchmarkRGBRLEFSECompress(b *testing.B) {
	for _, tf := range rgbTestFiles {
		b.Run(tf.name, func(b *testing.B) {
			yPlane, coPlane, cgPlane, w, h, rawBytes := SetupRGBTest(tf)
			if rawBytes == 0 {
				b.Skipf("could not load %s", tf.fileName)
			}
			compress := func(plane []uint16, maxVal uint16) []byte {
				var rle RleCompressU16
				rle.Init(w, h, maxVal)
				rleData := rle.Compress(plane)
				var s ScratchU16
				out, _ := FSECompressU16(rleData, &s)
				return out
			}
			yBlob := compress(yPlane, 255)
			coBlob := compress(coPlane, 510)
			cgBlob := compress(cgPlane, 510)
			totalComp := len(yBlob) + len(coBlob) + len(cgBlob)
			b.SetBytes(int64(rawBytes))
			b.ResetTimer()
			b.ReportMetric(float64(rawBytes)/float64(totalComp), "ratio")
			for i := 0; i < b.N; i++ {
				decompress := func(blob []byte) {
					var s ScratchU16
					rleData, _ := FSEDecompressU16(blob, &s)
					var rled RleDecompressU16
					rled.Init(rleData)
					rled.Decompress()
				}
				decompress(yBlob)
				decompress(coBlob)
				decompress(cgBlob)
			}
			b.ReportMetric(float64(rawBytes)/(1<<20), "original")
			b.ReportMetric(float64(totalComp)/(1<<20), "comp")
		})
	}
}

func BenchmarkRGBDeltaZZFSECompress(b *testing.B) {
	for _, tf := range rgbTestFiles {
		b.Run(tf.name, func(b *testing.B) {
			yPlane, coPlane, cgPlane, w, h, rawBytes := SetupRGBTest(tf)
			if rawBytes == 0 {
				b.Skipf("could not load %s", tf.fileName)
			}
			compress := func(plane []uint16, maxVal uint16) []byte {
				var dzz DeltaZZU16
				rle, _ := dzz.Compress(plane, w, h, maxVal)
				var s ScratchU16
				out, _ := FSECompressU16(rle, &s)
				return out
			}
			yBlob := compress(yPlane, 255)
			coBlob := compress(coPlane, 510)
			cgBlob := compress(cgPlane, 510)
			totalComp := len(yBlob) + len(coBlob) + len(cgBlob)
			b.SetBytes(int64(rawBytes))
			b.ResetTimer()
			b.ReportMetric(float64(rawBytes)/float64(totalComp), "ratio")
			for i := 0; i < b.N; i++ {
				decompress := func(blob []byte) {
					var s ScratchU16
					data, _ := FSEDecompressU16(blob, &s)
					var dzzd DeltaZZU16
					dzzd.Decompress(data, w, h)
				}
				decompress(yBlob)
				decompress(coBlob)
				decompress(cgBlob)
			}
			b.ReportMetric(float64(rawBytes)/(1<<20), "original")
			b.ReportMetric(float64(totalComp)/(1<<20), "comp")
		})
	}
}

func BenchmarkRGBFSECompress(b *testing.B) {
	for _, tf := range rgbTestFiles {
		b.Run(tf.name, func(b *testing.B) {
			yPlane, coPlane, cgPlane, _, _, rawBytes := SetupRGBTest(tf)
			if rawBytes == 0 {
				b.Skipf("could not load %s", tf.fileName)
			}
			compress := func(plane []uint16) []byte {
				var s ScratchU16
				out, _ := FSECompressU16(plane, &s)
				return out
			}
			yBlob := compress(yPlane)
			coBlob := compress(coPlane)
			cgBlob := compress(cgPlane)
			totalComp := len(yBlob) + len(coBlob) + len(cgBlob)
			b.SetBytes(int64(rawBytes))
			b.ResetTimer()
			b.ReportMetric(float64(rawBytes)/float64(totalComp), "ratio")
			for i := 0; i < b.N; i++ {
				decompress := func(blob []byte) {
					var s ScratchU16
					FSEDecompressU16(blob, &s)
				}
				decompress(yBlob)
				decompress(coBlob)
				decompress(cgBlob)
			}
			b.ReportMetric(float64(rawBytes)/(1<<20), "original")
			b.ReportMetric(float64(totalComp)/(1<<20), "comp")
		})
	}
}

// TestRGBRoundtrip verifies CompressRGB/DecompressRGB produce a pixel-exact roundtrip
// for all RGB test images.
func TestRGBRoundtrip(t *testing.T) {
	for _, tf := range rgbTestFiles {
		t.Run(tf.name, func(t *testing.T) {
			rgb, w, h := ReadTIFFRGB(tf.fileName)
			if rgb == nil {
				t.Skipf("could not load %s", tf.fileName)
			}

			compressed, err := CompressRGB(rgb, w, h)
			if err != nil {
				t.Fatalf("CompressRGB: %v", err)
			}

			got, err := DecompressRGB(compressed, w, h)
			if err != nil {
				t.Fatalf("DecompressRGB: %v", err)
			}

			if len(got) != len(rgb) {
				t.Fatalf("length mismatch: got %d, want %d", len(got), len(rgb))
			}
			for i := range rgb {
				if got[i] != rgb[i] {
					t.Fatalf("pixel mismatch at byte %d (pixel %d channel %d): got %d, want %d",
						i, i/3, i%3, got[i], rgb[i])
				}
			}

			ratio := float64(len(rgb)) / float64(len(compressed))
			t.Logf("%s: %dx%d  raw=%d  compressed=%d  ratio=%.2fx",
				tf.name, w, h, len(rgb), len(compressed), ratio)
		})
	}
}
