// Copyright 2021 Kuldeep Singh
// This source code is licensed under a MIT-style
// license that can be found in the LICENSE file.
package mic

import (
	"errors"
	"fmt"
	"image"
	"image/png"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/klauspost/compress/fse"
	"github.com/suyashkumar/dicom/pkg/tag"

	"github.com/suyashkumar/dicom"
)

type testData struct {
	name     string
	fileName string
	isBinary bool
	rows     int
	cols     int
}

var testFiles = []testData{
	{name: "MR", fileName: "testdata/MR_256_256_image.bin", isBinary: true, rows: 256, cols: 256},
	{name: "CT", fileName: "testdata/CT_512_512_image.bin", isBinary: true, rows: 512, cols: 512},
	{name: "CR", fileName: "testdata/CR_1760_2140_image.bin", isBinary: true, rows: 2140, cols: 1760},
	{name: "XR", fileName: "testdata/XR_2577_2048_image.bin", isBinary: true, rows: 2048, cols: 2577},
	{name: "MG1", fileName: "testdata/MG_image_bin2.bin", isBinary: true, rows: 2457, cols: 1996},
	{name: "MG2", fileName: "testdata/MG_Image_2_frame.bin", isBinary: true, rows: 2457, cols: 1996},
}

func ReadBinaryFile(fileName string, cols int, rows int) ([]byte, []uint16, uint16) {
	byteData, _ := os.ReadFile(fileName)
	shortData := make([]uint16, cols*rows)
	var maxShort uint16 = 0
	const export = false
	for i := 0; i < len(byteData); {
		shortData[i/2] = uint16(byteData[i+1])<<8 + uint16(byteData[i])
		if shortData[i/2] > maxShort {
			maxShort = shortData[i/2]
		}
		if export {
			byteData[i+1], byteData[i] = byteData[i], byteData[i+1]
		}
		i += 2
	}
	if export {
		img := image.NewGray16(image.Rect(0, 0, cols, rows))
		img.Pix = byteData
		f, _ := os.Create(fileName + ".png")
		png.Encode(f, img)
		f.Close()
	}
	return byteData, shortData, maxShort
}

func ReadDicomFile(tb testing.TB, fileName string) ([]byte, []uint16, uint16, int, int) {
	dataset, err := dicom.ParseFile(fileName, nil)
	if err != nil {
		tb.Error(err)
		return nil, nil, 0, 0, 0
	}
	pixelDataElement, err := dataset.FindElementByTag(tag.PixelData)
	if err != nil {
		return nil, nil, 0, 0, 0
		tb.Error(err)
	}
	pixelDataInfo := dicom.MustGetPixelDataInfo(pixelDataElement.Value)
	fr := pixelDataInfo.Frames[0]
	nativeFrame, _ := fr.GetNativeFrame()
	shortData := make([]uint16, nativeFrame.Cols*nativeFrame.Rows)
	byteData := make([]byte, nativeFrame.Cols*nativeFrame.Rows*2)
	var maxShort uint16 = 0
	for j := 0; j < len(nativeFrame.Data); j++ {
		shortData[j] = uint16(nativeFrame.Data[j][0])
		if shortData[j] > maxShort {
			maxShort = shortData[j]
		}
		byteData[j*2] = byte(shortData[j])
		byteData[(j*2)+1] = byte(shortData[j] >> 8)
	}
	return byteData, shortData, maxShort, nativeFrame.Cols, nativeFrame.Rows
}

func SetupTests(td testData) ([]byte, []uint16, uint16, int, int) {
	if td.isBinary {
		a, b, c := ReadBinaryFile(td.fileName, td.cols, td.rows)
		return a, b, c, td.cols, td.rows
	} else {
		return ReadDicomFile(nil, td.fileName)
	}
}

func WalkTestDir(tb testing.TB, path string, fn func(name string, imgData []uint16, width, height, orgSize int)) {
	filepath.Walk(path, func(path string, info fs.FileInfo, err error) error {
		if info.IsDir() {
			return nil
		}
		_, imgData, _, width, height := ReadDicomFile(tb, path)
		if len(imgData) > 0 {
			fn(filepath.Base(info.Name()), imgData, width, height, int(info.Size()))
		}
		return nil
	})
}

func BenchmarkDeltaRLEHuffCompress(b *testing.B) {
	for _, tf := range testFiles {
		b.Run(tf.name, func(b *testing.B) {
			byteData, shortData, maxShort, cols, rows := SetupTests(tf)
			// if !tf.isBinary {
			// 	f, _ := os.Create(tf.name + "_" + fmt.Sprint(cols) + "_" + fmt.Sprint(rows) + "_image.bin")
			// 	f.Write(byteData)
			// 	_ = f.Close()
			// }
			var drc DeltaRleCompressU16
			deltaComp, _ := drc.Compress(shortData, cols, rows, maxShort)
			var c CanHuffmanCompressU16
			c.Init(deltaComp)
			c.Compress()
			b.SetBytes(int64(len(byteData)))
			b.ResetTimer()
			b.ReportMetric(float64(len(byteData))/float64(len(c.Out)), "ratio")
			for i := 0; i < b.N; i++ {
				var d CanHuffmanDecompressU16
				d.Init(c.Out)
				d.ReadTable()
				d.Decompress()
				var drd DeltaRleDecompressU16
				drd.Decompress(d.Out, cols, rows)
			}
		})
	}

}

func BenchmarkDeltaRLEHuffCompress2(b *testing.B) {
	for _, tf := range testFiles {
		b.Run(tf.name, func(b *testing.B) {
			byteData, shortData, maxShort, cols, rows := SetupTests(tf)
			var drc DeltaRleCompressU16
			deltaComp, _ := drc.Compress(shortData, cols, rows, maxShort)
			var c CanHuffmanCompressU16
			c.Init(deltaComp)
			c.Compress()
			b.SetBytes(int64(len(byteData)))
			b.ResetTimer()
			b.ReportMetric(float64(len(byteData))/float64(len(c.Out)), "ratio")
			for i := 0; i < b.N; i++ {
				var d DeltaRleHuffDecompressU16
				d.Decompress(c.Out, cols, rows)
			}
		})
	}
}

func BenchmarkDeltaRLEFSECompress(b *testing.B) {
	for _, tf := range testFiles {
		b.Run(tf.name, func(b *testing.B) {
			byteData, shortData, maxShort, cols, rows := SetupTests(tf)
			var drc DeltaRleCompressU16
			deltaComp, _ := drc.Compress(shortData, cols, rows, maxShort)
			var s3 ScratchU16
			deltaFSEComp, _ := FSECompressU16(deltaComp, &s3)
			b.SetBytes(int64(len(byteData)))
			b.ResetTimer()
			b.ReportMetric(float64(len(byteData))/float64(len(deltaFSEComp)), "ratio")
			for i := 0; i < b.N; i++ {
				var s4 ScratchU16
				deltaDecompFSE, _ := FSEDecompressU16(deltaFSEComp, &s4)
				var drd DeltaRleDecompressU16
				drd.Decompress(deltaDecompFSE, cols, rows)
			}
		})
	}
}

func BenchmarkDeltaZZRLEHuffCompress(b *testing.B) {
	for _, tf := range testFiles {
		b.Run(tf.name, func(b *testing.B) {
			byteData, shortData, maxShort, cols, rows := SetupTests(tf)
			var dzz DeltaRleZZU16
			dzzComp, _ := dzz.Compress(shortData, cols, rows, maxShort)
			var c CanHuffmanCompressU16
			c.Init(dzzComp)
			c.Compress()
			b.SetBytes(int64(len(byteData)))
			b.ResetTimer()
			b.ReportMetric(float64(len(byteData))/float64(len(c.Out)), "ratio")
			for i := 0; i < b.N; i++ {
				var d CanHuffmanDecompressU16
				d.Init(c.Out)
				d.ReadTable()
				d.Decompress()
				var dzzd DeltaRleZZU16
				dzzd.Decompress(d.Out, cols, rows)
			}
		})
	}
}

func BenchmarkDeltaZZRLEFSECompress(b *testing.B) {
	for _, tf := range testFiles {
		b.Run(tf.name, func(b *testing.B) {
			byteData, shortData, maxShort, cols, rows := SetupTests(tf)
			var dzz DeltaZZU16
			dzzComp, _ := dzz.Compress(shortData, cols, rows, maxShort)
			var rleC RleCompressU16
			rleC.Init(cols, rows, (dzz.upperThreshold<<1)+1)
			deltaComp := rleC.Compress(dzzComp)
			var s3 ScratchU16
			deltaFSEComp, _ := FSECompressU16(deltaComp, &s3)
			b.SetBytes(int64(len(byteData)))
			b.ResetTimer()
			b.ReportMetric(float64(len(byteData))/float64(len(deltaFSEComp)), "ratio")
			for i := 0; i < b.N; i++ {
				var s4 ScratchU16
				deltaDecompFSE, _ := FSEDecompressU16(deltaFSEComp, &s4)
				var rleD RleDecompressU16
				rleD.Init(deltaDecompFSE)
				rleDecompressed := rleD.Decompress()

				var dzzd DeltaZZU16
				dzzd.Decompress(rleDecompressed, cols, rows)
			}
		})
	}
}

func BenchmarkDeltaZZFSECompress(b *testing.B) {
	for _, tf := range testFiles {
		b.Run(tf.name, func(b *testing.B) {
			byteData, shortData, maxShort, cols, rows := SetupTests(tf)
			var dzz DeltaZZU16
			dzzComp, _ := dzz.Compress(shortData, cols, rows, maxShort)
			var s3 ScratchU16
			deltaFSEComp, _ := FSECompressU16(dzzComp, &s3)
			b.SetBytes(int64(len(byteData)))
			b.ResetTimer()
			b.ReportMetric(float64(len(byteData))/float64(len(deltaFSEComp)), "ratio")
			for i := 0; i < b.N; i++ {
				var s4 ScratchU16
				deltaDecompFSE, _ := FSEDecompressU16(deltaFSEComp, &s4)
				var dzzd DeltaZZU16
				dzzd.Decompress(deltaDecompFSE, cols, rows)
			}
		})
	}
}

func BenchmarkFSECompress(b *testing.B) {
	for _, tf := range testFiles {
		b.Run(tf.name, func(b *testing.B) {
			byteData, shortData, _, _, _ := SetupTests(tf)
			var s3 ScratchU16
			deltaFSEComp, _ := FSECompressU16(shortData, &s3)
			b.SetBytes(int64(len(byteData)))
			b.ResetTimer()
			b.ReportMetric(float64(len(byteData))/float64(len(deltaFSEComp)), "ratio")
			for i := 0; i < b.N; i++ {
				var s4 ScratchU16
				FSEDecompressU16(deltaFSEComp, &s4)
			}
		})
	}
}

func BenchmarkHuffCompress(b *testing.B) {
	for _, tf := range testFiles {
		b.Run(tf.name, func(b *testing.B) {
			byteData, shortData, _, _, _ := SetupTests(tf)
			var c CanHuffmanCompressU16
			c.Init(shortData)
			c.Compress()
			b.SetBytes(int64(len(byteData)))
			b.ResetTimer()
			b.ReportMetric(float64(len(byteData))/float64(len(c.Out)), "ratio")
			for i := 0; i < b.N; i++ {
				var d CanHuffmanDecompressU16
				d.Init(c.Out)
				d.ReadTable()
				d.Decompress()
			}
		})
	}
}

func TestDeltaRleHuffCompress(t *testing.T) {
	for _, tf := range testFiles {
		t.Run(tf.name, func(t *testing.T) {
			_, shortData, maxShort, cols, rows := SetupTests(tf)
			DeltaRLEHuffTest(t, shortData, cols, rows, maxShort)
		})
	}
}

func TestDeltaRleHuffCompress2(t *testing.T) {
	for _, tf := range testFiles {
		t.Run(tf.name, func(t *testing.T) {
			_, shortData, maxShort, cols, rows := SetupTests(tf)
			DeltaRLEHuffTest2(t, shortData, cols, rows, maxShort)
		})
	}
}

func TestDeltaZZRleHuffCompress(t *testing.T) {
	for _, tf := range testFiles {
		t.Run(tf.name, func(t *testing.T) {
			_, shortData, maxShort, cols, rows := SetupTests(tf)
			DeltaZZRLEHuffTest(t, shortData, cols, rows, maxShort)
		})
	}
}

func TestDeltaZZRleCombHuffCompress(t *testing.T) {
	for _, tf := range testFiles {
		t.Run(tf.name, func(t *testing.T) {
			_, shortData, maxShort, cols, rows := SetupTests(tf)
			DeltaZZRLECombHuffTest(t, shortData, cols, rows, maxShort)
		})
	}
}

func TestDeltaZZRleFSECompress(t *testing.T) {
	for _, tf := range testFiles {
		t.Run(tf.name, func(t *testing.T) {
			_, shortData, maxShort, cols, rows := SetupTests(tf)
			DeltaZZRLEFSETest(t, shortData, cols, rows, maxShort)
		})
	}
}

func TestKlausCompress(t *testing.T) {
	for _, tf := range testFiles {
		t.Run(tf.name, func(t *testing.T) {
			_, shortData, _, cols, rows := SetupTests(tf)
			DeltaKlausTest(t, shortData, cols, rows)
		})
	}
}

func TestKlausCompressExtra(t *testing.T) {
	before := int64(0)
	klaus := int64(0)
	rle2 := int64(0)
	rle3 := int64(0)
	WalkTestDir(t, "testdata/dcim", func(name string, imgData []uint16, width, height, orgSize int) {
		t.Run(name, func(t *testing.T) {

			t.Run("klaus", func(t *testing.T) {
				//DeltaKlausTest(t, imgData, width, height)
				data := append([]uint16{}, imgData...)
				start := time.Now()
				comp := CompressKlaus(data, width, height)
				elapsedFile := time.Since(start)
				t.Logf(name+": Klaus compress took %v, Output Size: %v (%.2f:1)", elapsedFile, len(comp), float64(len(imgData)*2)/float64(len(comp)))
				before += int64(len(imgData) * 2)
				klaus += int64(len(comp))
			})
			t.Run("rle", func(t *testing.T) {
				var drc DeltaRleCompressU16
				start := time.Now()
				maxShort := uint16(0)
				for _, v := range imgData {
					if v > maxShort {
						maxShort = v
					}
				}
				deltaComp, _ := drc.Compress(imgData, width, height, maxShort)
				var c CanHuffmanCompressU16
				c.Init(deltaComp)
				c.Compress()
				comp := c.Out
				elapsedFile := time.Since(start)
				t.Logf(name+": DeltaRleCompressU16 compress took %v, Output Size: %v (%.2f:1)", elapsedFile, len(comp), float64(len(imgData)*2)/float64(len(comp)))
				rle2 += int64(len(comp))
			})
			t.Run("rle-gap", func(t *testing.T) {
				imgData := imgData
				var drc DeltaRleCompressU16
				start := time.Now()
				maxShort := uint16(0)
				for _, v := range imgData {
					if v > maxShort {
						maxShort = v
					}
				}
				var bm []byte
				if true {
					var bitmap [65536]byte
					max := uint16(0)
					for off := range imgData {
						v := imgData[off]
						bitmap[v] = 1
						if v > max {
							max = v
						}
					}
					gaps := 0
					valLen := int(max) + 1
					for _, f := range bitmap[:valLen] {
						gaps += 1 - int(f)
					}

					// If one in every 4 or more pixels are gaps.
					// TODO: Could include bitmap size for metric, but best would be try one with gap removal
					// and one without and compare sizes.
					if gaps*4 > int(max) {
						in := append([]uint16{}, imgData...)

						var inToOut [65536]uint16
						out := uint16(0)
						var s fse.Scratch
						hist := s.Histogram()
						hist = hist[:256]

						for i, f := range bitmap[:valLen] {
							hist[f]++
							if f == 1 {
								inToOut[i] = out
								out++
							}
						}

						maxCnt := hist[0]
						if hist[1] > maxCnt {
							maxCnt = hist[1]
						}
						maxShort = out - 1
						s.HistogramFinished(1, int(maxCnt))
						bm, _ = fse.Compress(bitmap[:valLen], &s)
						for off := range in {
							in[off] = inToOut[in[off]]
						}
						imgData = in
						//fmt.Println("out:", out)
					}
				}
				deltaComp, _ := drc.Compress(imgData, width, height, maxShort)
				var c CanHuffmanCompressU16
				c.Init(deltaComp)
				c.Compress()
				comp := c.Out
				elapsedFile := time.Since(start)
				t.Logf(name+": Gap+DeltaRleCompressU16 compress took %v, Output Size: %v (%.2f:1)", elapsedFile, len(comp)+len(bm), float64(len(imgData)*2)/float64(len(comp)))
				rle3 += int64(len(comp) + len(bm))
			})
		})
	})
	t.Logf("klaus: %d/%d (%.2f:1)", klaus, before, float64(before)/float64(klaus))
	t.Logf("rle: %d/%d (%.2f:1)", rle2, before, float64(before)/float64(rle2))
	t.Logf("rle+gap: %d/%d (%.2f:1)", rle3, before, float64(before)/float64(rle3))
}

func TestDeltaRleFSECompress(t *testing.T) {
	for _, tf := range testFiles {
		t.Run(tf.name, func(t *testing.T) {
			_, shortData, maxShort, cols, rows := SetupTests(tf)
			DeltaRleFSETest(t, shortData, cols, rows, maxShort)
		})
	}
}

func TestDeltaFSECompress(t *testing.T) {
	for _, tf := range testFiles {
		t.Run(tf.name, func(t *testing.T) {
			_, shortData, maxShort, cols, rows := SetupTests(tf)
			DeltaFSECompress(t, shortData, cols, rows, maxShort)
		})
	}
}

func TestDeltaHuffCompress(t *testing.T) {
	for _, tf := range testFiles {
		t.Run(tf.name, func(t *testing.T) {
			_, shortData, maxShort, cols, rows := SetupTests(tf)
			DeltaHuffCompress(t, shortData, cols, rows, maxShort)
		})
	}
}

func TestFSECompress(t *testing.T) {
	for _, tf := range testFiles {
		t.Run(tf.name, func(t *testing.T) {
			_, shortData, _, _, _ := SetupTests(tf)
			FSE16bitCompress(t, shortData)
		})
	}
}

func TestHuffCompress(t *testing.T) {
	for _, tf := range testFiles {
		t.Run(tf.name, func(t *testing.T) {
			_, shortData, _, _, _ := SetupTests(tf)
			HuffTest(t, shortData)
		})
	}
}

func IgnoreTestCompressDCMFile(t *testing.T) {
	dataset, _ := dicom.ParseFile("testdata/CT.3985.1", nil)
	pixelDataElement, _ := dataset.FindElementByTag(tag.PixelData)
	pixelDataInfo := dicom.MustGetPixelDataInfo(pixelDataElement.Value)
	fr := pixelDataInfo.Frames[0]
	nativeFrame, _ := fr.GetNativeFrame()
	fmt.Printf("rows : %d columns: %d bitspersample: %d\n", nativeFrame.Rows, nativeFrame.Cols, nativeFrame.BitsPerSample)
	shortData := make([]uint16, nativeFrame.Cols*nativeFrame.Rows)
	byteData := make([]byte, nativeFrame.Cols*nativeFrame.Rows*2)
	var maxShort uint16 = 0
	for j := 0; j < len(nativeFrame.Data); j++ {
		shortData[j] = uint16(nativeFrame.Data[j][0])
		if shortData[j] > maxShort {
			maxShort = shortData[j]
		}
		byteData[j*2] = byte(shortData[j])
		byteData[(j*2)+1] = byte(shortData[j] >> 8)
	}

	fmt.Printf("Max short %d\n", maxShort)
	fmt.Printf("Byte length: %d\n", len(byteData))

	DeltaRLEHuffTest(t, shortData, nativeFrame.Cols, nativeFrame.Rows, maxShort)
	FSE16bitCompress(t, shortData)
	DeltaFSECompress(t, shortData, nativeFrame.Cols, nativeFrame.Rows, maxShort)
	DeltaRleFSETest(t, shortData, nativeFrame.Cols, nativeFrame.Rows, maxShort)
	DeltaRLEHuffTest(t, shortData, nativeFrame.Cols, nativeFrame.Rows, maxShort)
	DeltaRLEHuffTest2(t, shortData, nativeFrame.Cols, nativeFrame.Rows, maxShort)

}

func IgnoreTestCompressDCMImages(t *testing.T) {
	fileName := "../1.3.6.1.4.1.5962.99.1.2280943358.716200484.1363785608958.400.0.dcm"
	if _, err := os.Stat(fileName); errors.Is(err, os.ErrNotExist) {
		t.Skip("Skipping test as file does not exist")
	}
	dataset, _ := dicom.ParseFile(fileName, nil)
	pixelDataElement, _ := dataset.FindElementByTag(tag.PixelData)
	pixelDataInfo := dicom.MustGetPixelDataInfo(pixelDataElement.Value)

	var elapsed1 time.Duration
	var elapsed2 time.Duration
	var elapsed3 time.Duration

	for _, fr := range pixelDataInfo.Frames {
		nativeFrame, _ := fr.GetNativeFrame()
		shortData := make([]uint16, nativeFrame.Cols*nativeFrame.Rows)
		byteData := make([]byte, nativeFrame.Cols*nativeFrame.Rows*2)
		var maxShort uint16 = 0
		for j := 0; j < len(nativeFrame.Data); j++ {
			shortData[j] = uint16(nativeFrame.Data[j][0])
			if shortData[j] > maxShort {
				maxShort = shortData[j]
			}
			byteData[j*2] = byte(shortData[j])
			byteData[(j*2)+1] = byte(shortData[j] >> 8)
		}
		fmt.Printf("Max short %d\n", maxShort)
		fmt.Printf("Byte length: %d\n", len(byteData))

		compressedHuff := DeltaRLEHuffCompress(shortData, nativeFrame.Cols, nativeFrame.Rows, maxShort)
		compressedFSE := DeltaRLEFSECompress(shortData, nativeFrame.Cols, nativeFrame.Rows, maxShort)

		elapsed1 += DeltaRLEHuffDecompress(t, shortData, compressedHuff, nativeFrame.Cols, nativeFrame.Rows, true)
		elapsed2 += DeltaRLEHuffDecompress2(t, shortData, compressedHuff, nativeFrame.Cols, nativeFrame.Rows, true)
		elapsed3 += DeltaRLEFSEDecompress(t, shortData, compressedFSE, nativeFrame.Cols, nativeFrame.Rows, true)
	}

	fmt.Println("Decompress Huff 1:", elapsed1, "Decompress Huff 2:", elapsed2, "Decompress FSE:", elapsed3)

}

func DeltaFSECompress(t *testing.T, shortData []uint16, cols int, rows int, maxShort uint16) {
	startTime := time.Now()
	deltaComp, _ := DeltaCompressU16(shortData, cols, rows, maxShort)
	elapsedTime := time.Since(startTime)
	fmt.Println("Delta compress", elapsedTime)

	var s3 ScratchU16

	deltaFSEComp, errDelta := FSECompressU16(deltaComp, &s3)

	if errDelta != nil {
		fmt.Printf("got error %v (%T)\n", errDelta, errDelta)
	}

	fmt.Printf("Delta FSE Compress: %d short %d -> %d bytes (%.2f:1)\n", len(shortData), len(shortData)*2, len(deltaFSEComp), float64(len(shortData)*2)/float64(len(deltaFSEComp)))

	var s4 ScratchU16
	deltaDecompFSE, errDeltaDecomp := FSEDecompressU16(deltaFSEComp, &s4)

	if errDeltaDecomp != nil {
		fmt.Printf("got error %v (%T)\n", errDeltaDecomp, errDeltaDecomp)
	}

	startTime = time.Now()

	deltaOutput := DeltaDecompressU16(deltaDecompFSE, cols, rows)

	elapsedTime = time.Since(startTime)
	fmt.Println("Delta Decompress", elapsedTime)

	var passed = true

	for i := 0; i < len(shortData); i++ {
		if shortData[i] != deltaOutput[i] {
			fmt.Printf("***** Delta FSE Different at location %d value in original %d in decomp %d\n", i, shortData[i], deltaOutput[i])
			passed = false
			break
		}
	}
	if len(deltaOutput) != len(shortData) {
		fmt.Printf(" Delta FSE Failed to decompress. Original length %d Decomp length %d\n", len(shortData), len(deltaOutput))
		passed = false
	}
	if passed {
		fmt.Printf("Delta FSE PASSED 16-bit compression-decompression\n")
	} else {
		t.Errorf("Delta FSE 16-bit compression-decompression FAILED")
	}
}

func FSE16bitCompress(t *testing.T, shortData []uint16) {
	var s ScratchU16

	b, err := FSECompressU16(shortData, &s)

	if err != nil {
		fmt.Printf("got error %v (%T)\n", err, err)
	}

	fmt.Printf("FSE Compress: %d short %d -> %d bytes (%.2f:1)\n", len(shortData), len(shortData)*2, len(b), float64(len(shortData)*2)/float64(len(b)))

	var s1 ScratchU16
	decomp, err1 := FSEDecompressU16(b, &s1)

	if err1 != nil {
		fmt.Printf("Got decomp error %v (%T)\n", err1, err1)
	}

	var passed = true

	if len(decomp) != len(shortData) {
		fmt.Printf("Failed to decompress. Original length %d Decomp length %d\n", len(shortData), len(decomp))
		passed = false
	}

	for i := 0; i < len(shortData); i++ {
		if shortData[i] != decomp[i] {
			fmt.Printf("*** Different at location %d value in original %d in decomp %d\n", i, shortData[i], decomp[i])
			passed = false
			break
		}
	}
	if passed {
		fmt.Printf("FSE PASSED 16-bit compression-decompression\n")
	} else {
		t.Errorf("FSE 16-bit compression-decompression FAILED")
	}
}

func DeltaRLEFSECompress(shortData []uint16, cols int, rows int, maxShort uint16) []byte {
	var drc DeltaRleCompressU16
	deltaComp, _ := drc.Compress(shortData, cols, rows, maxShort)

	var s3 ScratchU16
	deltaFSEComp, errDelta := FSECompressU16(deltaComp, &s3)
	if errDelta != nil {
		fmt.Printf("got error %v (%T)\n", errDelta, errDelta)
	}
	fmt.Printf("Delta RLE FSE Compress: %d short %d -> %d bytes (%.2f:1)\n", len(shortData), len(shortData)*2, len(deltaFSEComp), float64(len(shortData)*2)/float64(len(deltaFSEComp)))
	return deltaFSEComp
}

func DeltaRLEFSEDecompress(t *testing.T, shortData []uint16, deltaFSEComp []byte, cols int, rows int, verify bool) time.Duration {
	var s4 ScratchU16
	start := time.Now()
	deltaDecompFSE, errDeltaDecomp := FSEDecompressU16(deltaFSEComp, &s4)
	if errDeltaDecomp != nil {
		fmt.Printf("got error %v (%T)\n", errDeltaDecomp, errDeltaDecomp)
	}
	var drd DeltaRleDecompressU16
	drd.Decompress(deltaDecompFSE, cols, rows)
	deltaOutput := drd.Out
	elapsedTime := time.Since(start)

	if verify {
		passed := true
		for i := 0; i < len(shortData); i++ {
			if shortData[i] != deltaOutput[i] {
				fmt.Printf("*** Different at location %d value in original %d in decomp %d\n", i, shortData[i], deltaOutput[i])
				passed = false
				break
			}
		}
		if len(deltaOutput) != len(shortData) {
			fmt.Printf("Failed to decompress. Original length %d Decomp length %d\n", len(shortData), len(deltaOutput))
			passed = false
		}
		if passed {
			fmt.Printf("PASSED Delta RLE FSE 16-bit compression-decompression\n")
		} else {
			t.Errorf("Delta RLE FSE 16-bit compression-decompression FAILED")
		}
	}
	return elapsedTime
}

func DeltaRleFSETest(t *testing.T, shortData []uint16, cols int, rows int, maxShort uint16) {
	start := time.Now()
	var drc DeltaRleCompressU16
	deltaComp, _ := drc.Compress(shortData, cols, rows, maxShort)

	elapsedFile := time.Since(start)
	fmt.Println("Delta RLE FSE - Delta compress took ", elapsedFile)

	var s3 ScratchU16
	deltaFSEComp, errDelta := FSECompressU16(deltaComp, &s3)
	elapsedFile = time.Since(start)
	fmt.Println("Delta RLE FSE - FSE compress took ", elapsedFile)
	if errDelta != nil {
		fmt.Printf("got error %v (%T)\n", errDelta, errDelta)
	}
	fmt.Printf("Delta RLE FSE Compress: %d short %d -> %d bytes (%.2f:1)\n", len(shortData), len(shortData)*2, len(deltaFSEComp), float64(len(shortData)*2)/float64(len(deltaFSEComp)))
	var s4 ScratchU16
	start = time.Now()
	deltaDecompFSE, errDeltaDecomp := FSEDecompressU16(deltaFSEComp, &s4)

	if len(deltaDecompFSE) != len(deltaComp) {
		t.Errorf("Error in FSE decompression, Orig Len %d Decomp Len %d\n", len(deltaComp), len(deltaDecompFSE))
	}

	for i, v := range deltaDecompFSE {
		if v != deltaComp[i] {
			t.Errorf("*** Different for FSE at location %d value in original %d in decomp %d\n", i, deltaComp[i], v)
			break
		}
	}

	elapsedFile = time.Since(start)
	fmt.Println("Delta RLE FSE - FSE decompress took ", elapsedFile)
	if errDeltaDecomp != nil {
		fmt.Printf("got error %v (%T)\n", errDeltaDecomp, errDeltaDecomp)
	}
	var drd DeltaRleDecompressU16
	drd.Decompress(deltaDecompFSE, cols, rows)
	deltaOutput := drd.Out
	elapsedFile = time.Since(start)
	fmt.Println("Delta RLE FSE - Delta decompress took ", elapsedFile)
	passed := true
	for i := 0; i < len(shortData); i++ {
		if shortData[i] != deltaOutput[i] {
			fmt.Printf("*** Different at location %d value in original %d in decomp %d\n", i, shortData[i], deltaOutput[i])
			passed = false
			break
		}
	}
	if len(deltaOutput) != len(shortData) {
		fmt.Printf("Failed to decompress. Original length %d Decomp length %d\n", len(shortData), len(deltaOutput))
		passed = false
	}
	if passed {
		fmt.Printf("PASSED Delta RLE FSE 16-bit compression-decompression\n")
	} else {
		t.Errorf("Delta RLE FSE 16-bit compression-decompression FAILED")
	}
}

func DeltaRLEHuffCompress(shortData []uint16, cols int, rows int, maxShort uint16) []byte {
	start := time.Now()
	var drc DeltaRleCompressU16
	deltaComp, _ := drc.Compress(shortData, cols, rows, maxShort)
	elapsedFile := time.Since(start)
	fmt.Println("Delta RLE Huff - Delta compress took ", elapsedFile)
	var c CanHuffmanCompressU16
	c.Init(deltaComp)
	c.Compress()
	elapsedFile = time.Since(start)
	fmt.Println("Delta RLE Huff - Huff compress took ", elapsedFile)
	fmt.Printf("Delta RLE Huff Compress: %d short %d -> %d bytes (%.2f:1)\n", len(shortData), len(shortData)*2, len(c.Out), float64(len(shortData)*2)/float64(len(c.Out)))
	return c.Out
}

func DeltaRLEHuffDecompress(t *testing.T, shortData []uint16, input []byte, cols int, rows int, verify bool) time.Duration {
	start := time.Now()

	var d CanHuffmanDecompressU16
	d.Init(input)
	d.ReadTable()
	d.Decompress()
	var drd DeltaRleDecompressU16
	drd.Decompress(d.Out, cols, rows)
	deltaOutput := drd.Out

	elapsedTime := time.Since(start)

	if verify {
		passed := true
		for i := 0; i < len(shortData); i++ {
			if shortData[i] != deltaOutput[i] {
				fmt.Printf("*** Different at location %d value in original %d in decomp %d\n", i, shortData[i], deltaOutput[i])
				passed = false
				break
			}
		}
		if len(deltaOutput) != len(shortData) {
			fmt.Printf("Failed to decompress. Original length %d Decomp length %d\n", len(shortData), len(deltaOutput))
			passed = false
		}
		if passed {
			fmt.Printf("PASSED Delta RLE HUFF 16-bit compression-decompression\n")
		} else {
			t.Errorf("Delta RLE HUFF 16-bit compression-decompression FAILED")
		}
	}

	return elapsedTime
}

func DeltaRLEHuffDecompress2(t *testing.T, shortData []uint16, input []byte, cols int, rows int, verify bool) time.Duration {
	start := time.Now()

	var d DeltaRleHuffDecompressU16
	d.Decompress(input, cols, rows)

	deltaOutput := d.Out

	elapsedTime := time.Since(start)

	if verify {
		passed := true
		for i := 0; i < len(shortData); i++ {
			if shortData[i] != deltaOutput[i] {
				fmt.Printf("*** Different at location %d value in original %d in decomp %d\n", i, shortData[i], deltaOutput[i])
				passed = false
				break
			}
		}
		if len(deltaOutput) != len(shortData) {
			fmt.Printf("Failed to decompress. Original length %d Decomp length %d\n", len(shortData), len(deltaOutput))
			passed = false
		}
		if passed {
			fmt.Printf("PASSED Delta RLE HUFF 2 16-bit compression-decompression\n")
		} else {
			t.Errorf("Delta RLE HUFF 2 16-bit compression-decompression FAILED")
		}
	}
	return elapsedTime
}

func DeltaRLEHuffTest(t *testing.T, shortData []uint16, cols int, rows int, maxShort uint16) {
	start := time.Now()
	var drc DeltaRleCompressU16
	deltaComp, _ := drc.Compress(shortData, cols, rows, maxShort)
	elapsedFile := time.Since(start)
	fmt.Println("Delta RLE Huff - Delta compress took ", elapsedFile)
	var c CanHuffmanCompressU16
	c.Init(deltaComp)
	c.Compress()
	elapsedFile = time.Since(start)
	fmt.Println("Delta RLE Huff - Huff compress took ", elapsedFile)
	fmt.Printf("Delta RLE Huff Compress: %d short %d -> %d bytes (%.2f:1)\n", len(shortData), len(shortData)*2, len(c.Out), float64(len(shortData)*2)/float64(len(c.Out)))
	var d CanHuffmanDecompressU16

	start = time.Now()

	d.Init(c.Out)
	d.ReadTable()
	elapsedFile = time.Since(start)
	fmt.Println("Delta RLE Huff - Huff ReadTable decompress took ", elapsedFile)
	d.Decompress()

	elapsedFile = time.Since(start)
	fmt.Println("Delta RLE Huff - Huff decompress took ", elapsedFile)

	var drd DeltaRleDecompressU16
	drd.Decompress(d.Out, cols, rows)

	deltaOutput := drd.Out

	elapsedFile = time.Since(start)
	fmt.Println("Delta RLE Huff - Delta decompress took ", elapsedFile)
	fmt.Println("symbols of interest", len(d.c.symbolsOfInterestList), "maxCodeLength", d.c.maxCodeLength)

	for i, v := range deltaComp {
		if v != d.Out[i] {
			t.Errorf("Value mismatch at %d values in %d out %d\n", i, v, d.Out[i])
		}
	}

	passed := true
	for i := 0; i < len(shortData); i++ {
		if shortData[i] != deltaOutput[i] {
			fmt.Printf("*** Different at location %d value in original %d in decomp %d\n", i, shortData[i], deltaOutput[i])
			passed = false
			break
		}
	}
	if len(deltaOutput) != len(shortData) {
		fmt.Printf("Failed to decompress. Original length %d Decomp length %d\n", len(shortData), len(deltaOutput))
		passed = false
	}
	if passed {
		fmt.Printf("PASSED Delta RLE HUFF 16-bit compression-decompression\n")
	} else {
		t.Errorf("Delta RLE HUFF 16-bit compression-decompression FAILED")
	}
}

func DeltaRLEHuffTest2(t *testing.T, shortData []uint16, cols int, rows int, maxShort uint16) {
	start := time.Now()
	var drc DeltaRleCompressU16
	deltaComp, _ := drc.Compress(shortData, cols, rows, maxShort)
	elapsedFile := time.Since(start)
	fmt.Println("Delta RLE Huff - Delta compress took ", elapsedFile)
	var c CanHuffmanCompressU16
	c.Init(deltaComp)
	c.Compress()
	elapsedFile = time.Since(start)
	fmt.Println("Delta RLE Huff - Huff compress took ", elapsedFile)
	fmt.Printf("Delta RLE Huff Compress: %d short %d -> %d bytes (%.2f:1)\n", len(shortData), len(shortData)*2, len(c.Out), float64(len(shortData)*2)/float64(len(c.Out)))

	var d DeltaRleHuffDecompressU16
	start = time.Now()
	d.Decompress(c.Out, cols, rows)
	elapsedFile = time.Since(start)
	fmt.Println("Delta RLE Huff 2 - Decompress took ", elapsedFile)

	deltaOutput := d.Out

	passed := true
	for i := 0; i < len(shortData); i++ {
		if shortData[i] != deltaOutput[i] {
			fmt.Printf("*** Different at location %d value in original %d in decomp %d\n", i, shortData[i], deltaOutput[i])
			passed = false
			break
		}
	}
	if len(deltaOutput) != len(shortData) {
		fmt.Printf("Failed to decompress. Original length %d Decomp length %d\n", len(shortData), len(deltaOutput))
		passed = false
	}
	if passed {
		fmt.Printf("PASSED Delta RLE HUFF 2 16-bit compression-decompression\n")
	} else {
		t.Errorf("Delta RLE HUFF 2 16-bit compression-decompression FAILED")
	}
}

func DeltaZZRLEHuffTest(t *testing.T, shortData []uint16, cols int, rows int, maxShort uint16) {
	start := time.Now()
	var dzz DeltaZZU16
	dzzComp, _ := dzz.Compress(shortData, cols, rows, maxShort)
	var rleC RleCompressU16
	rleC.Init(cols, rows, (dzz.upperThreshold<<1)+1)
	deltaComp := rleC.Compress(dzzComp)
	elapsedFile := time.Since(start)
	fmt.Println("Delta ZZ RLE Huff - Delta ZZ Rle compress took ", elapsedFile, "Rle Size", len(deltaComp), "Delta Size", len(dzzComp))
	var c CanHuffmanCompressU16
	c.Init(deltaComp)
	c.Compress()
	elapsedFile = time.Since(start)
	fmt.Println("Delta ZZ RLE Huff - Huff compress took ", elapsedFile)
	fmt.Printf("Delta ZZ RLE Huff Compress: %d short %d -> %d bytes (%.2f:1)\n", len(shortData), len(shortData)*2, len(c.Out), float64(len(shortData)*2)/float64(len(c.Out)))
	var d CanHuffmanDecompressU16
	start = time.Now()
	d.Init(c.Out)
	d.ReadTable()
	elapsedFile = time.Since(start)
	fmt.Println("Delta ZZ RLE Huff - Huff ReadTable decompress took ", elapsedFile)
	d.Decompress()

	elapsedFile = time.Since(start)
	fmt.Println("Delta ZZ RLE Huff - Huff decompress took ", elapsedFile)

	var rleD RleDecompressU16
	rleD.Init(d.Out)
	rleDecompressed := rleD.Decompress()

	elapsedFile = time.Since(start)
	fmt.Println("Delta ZZ RLE Huff - Rle decompress took ", elapsedFile, "Huff size", len(d.Out), "Rle Size", len(rleDecompressed))

	var dzzd DeltaZZU16
	deltaOutput := dzzd.Decompress(rleDecompressed, cols, rows)

	elapsedFile = time.Since(start)
	fmt.Println("Delta ZZ RLE Huff - Delta Rle ZZ decompress took ", elapsedFile)
	fmt.Println("symbols of interest", len(d.c.symbolsOfInterestList), "maxCodeLength", d.c.maxCodeLength)

	passed := true
	if len(deltaOutput) != len(shortData) {
		fmt.Printf("Failed to decompress. Original length %d Decomp length %d\n", len(shortData), len(deltaOutput))
		passed = false
	}
	for i := 0; i < len(shortData); i++ {
		if shortData[i] != deltaOutput[i] {
			fmt.Printf("*** Different at location %d value in original %d in decomp %d\n", i, shortData[i], deltaOutput[i])
			passed = false
			break
		}
	}

	if passed {
		fmt.Printf("PASSED Delta ZZ RLE HUFF 16-bit compression-decompression\n")
	} else {
		t.Errorf("Delta ZZ RLE HUFF 16-bit compression-decompression FAILED")
	}
}

func DeltaZZRLECombHuffTest(t *testing.T, shortData []uint16, cols int, rows int, maxShort uint16) {
	start := time.Now()
	var dzz DeltaRleZZU16
	dzzComp, _ := dzz.Compress(shortData, cols, rows, maxShort)
	elapsedFile := time.Since(start)
	fmt.Println("Delta ZZ RLE Huff - Delta ZZ Rle compress took ", elapsedFile, "Delta Size", len(dzzComp))
	var c CanHuffmanCompressU16
	c.Init(dzzComp)
	c.Compress()
	elapsedFile = time.Since(start)
	fmt.Println("Delta ZZ RLE Huff - Huff compress took ", elapsedFile)
	fmt.Printf("Delta ZZ RLE Huff Compress: %d short %d -> %d bytes (%.2f:1)\n", len(shortData), len(shortData)*2, len(c.Out), float64(len(shortData)*2)/float64(len(c.Out)))
	var d CanHuffmanDecompressU16
	start = time.Now()
	d.Init(c.Out)
	d.ReadTable()
	elapsedFile = time.Since(start)
	fmt.Println("Delta ZZ RLE Huff - Huff ReadTable decompress took ", elapsedFile)
	d.Decompress()

	elapsedFile = time.Since(start)
	fmt.Println("Delta ZZ RLE Huff - Huff decompress took ", elapsedFile)

	var dzzd DeltaRleZZU16
	deltaOutput := dzzd.Decompress(d.Out, cols, rows)

	elapsedFile = time.Since(start)
	fmt.Println("Delta ZZ RLE Huff - Delta Rle ZZ decompress took ", elapsedFile)
	fmt.Println("symbols of interest", len(d.c.symbolsOfInterestList), "maxCodeLength", d.c.maxCodeLength)

	passed := true
	if len(deltaOutput) != len(shortData) {
		fmt.Printf("Failed to decompress. Original length %d Decomp length %d\n", len(shortData), len(deltaOutput))
		passed = false
	}
	for i := 0; i < len(shortData); i++ {
		if shortData[i] != deltaOutput[i] {
			fmt.Printf("*** Different at location %d value in original %d in decomp %d\n", i, shortData[i], deltaOutput[i])
			passed = false
			break
		}
	}

	if passed {
		fmt.Printf("PASSED Delta ZZ RLE Combined HUFF 16-bit compression-decompression\n")
	} else {
		t.Errorf("Delta ZZ RLE Combined HUFF 16-bit compression-decompression FAILED")
	}
}

func DeltaKlausTest(t *testing.T, shortData []uint16, cols int, rows int) {
	start := time.Now()
	comp := CompressKlaus(shortData, cols, rows)
	elapsedFile := time.Since(start)
	t.Logf("Klaus compress took %v, Output Size: %v (%.2f:1)", elapsedFile, len(comp), float64(len(shortData)*2)/float64(len(comp)))
}

func DeltaZZRLEFSETest(t *testing.T, shortData []uint16, cols int, rows int, maxShort uint16) {
	start := time.Now()
	var dzz DeltaZZU16
	dzzComp, _ := dzz.Compress(shortData, cols, rows, maxShort)
	var rleC RleCompressU16
	rleC.Init(cols, rows, (dzz.upperThreshold<<1)+1)
	deltaComp := rleC.Compress(dzzComp)
	elapsedFile := time.Since(start)
	fmt.Println("Delta ZZ RLE FSE - Delta ZZ Rle compress took ", elapsedFile, "Rle Size", len(deltaComp), "Delta Size", len(dzzComp))
	var s3 ScratchU16
	deltaFSEComp, errDelta := FSECompressU16(deltaComp, &s3)
	elapsedFile = time.Since(start)
	fmt.Println("Delta RLE FSE - FSE compress took ", elapsedFile)
	if errDelta != nil {
		fmt.Printf("got error %v (%T)\n", errDelta, errDelta)
	}
	fmt.Printf("Delta RLE FSE Compress: %d short %d -> %d bytes (%.2f:1)\n", len(shortData), len(shortData)*2, len(deltaFSEComp), float64(len(shortData)*2)/float64(len(deltaFSEComp)))
	var s4 ScratchU16
	start = time.Now()
	deltaDecompFSE, _ := FSEDecompressU16(deltaFSEComp, &s4)

	if len(deltaDecompFSE) != len(deltaComp) {
		t.Errorf("Error in FSE decompression, Orig Len %d Decomp Len %d\n", len(deltaComp), len(deltaDecompFSE))
	}
	elapsedFile = time.Since(start)
	fmt.Println("Delta ZZ RLE FSE - FSE decompress took ", elapsedFile)

	var rleD RleDecompressU16
	rleD.Init(deltaDecompFSE)
	rleDecompressed := rleD.Decompress()

	elapsedFile = time.Since(start)
	fmt.Println("Delta ZZ RLE FSE - Rle decompress took ", elapsedFile, "Rle Size", len(rleDecompressed))

	var dzzd DeltaZZU16
	deltaOutput := dzzd.Decompress(rleDecompressed, cols, rows)

	elapsedFile = time.Since(start)
	fmt.Println("Delta ZZ RLE FSE - Delta Rle ZZ decompress took ", elapsedFile)

	passed := true
	if len(deltaOutput) != len(shortData) {
		fmt.Printf("Failed to decompress. Original length %d Decomp length %d\n", len(shortData), len(deltaOutput))
		passed = false
	}
	for i := 0; i < len(shortData); i++ {
		if shortData[i] != deltaOutput[i] {
			fmt.Printf("*** Different at location %d value in original %d in decomp %d\n", i, shortData[i], deltaOutput[i])
			passed = false
			break
		}
	}

	if passed {
		fmt.Printf("PASSED Delta ZZ RLE FSE 16-bit compression-decompression\n")
	} else {
		t.Errorf("Delta ZZ RLE FSE 16-bit compression-decompression FAILED")
	}
}

func HuffTest(t *testing.T, shortData []uint16) {
	start := time.Now()
	var c CanHuffmanCompressU16
	c.Init(shortData)
	c.Compress()
	elapsedFile := time.Since(start)
	fmt.Println("Huff - Huff compress took ", elapsedFile)
	fmt.Printf("Huff Compress: %d short %d -> %d bytes (%.2f:1)\n", len(shortData), len(shortData)*2, len(c.Out), float64(len(shortData)*2)/float64(len(c.Out)))
	var d CanHuffmanDecompressU16
	start = time.Now()
	d.Init(c.Out)
	d.ReadTable()
	elapsedFile = time.Since(start)
	fmt.Println("Huff - Huff ReadTable decompress took ", elapsedFile)
	d.Decompress()

	elapsedFile = time.Since(start)
	fmt.Println("Huff - Huff decompress took ", elapsedFile)
	deltaOutput := d.Out

	elapsedFile = time.Since(start)
	fmt.Println("Huff - Delta Rle ZZ decompress took ", elapsedFile)
	fmt.Println("symbols of interest", len(d.c.symbolsOfInterestList), "maxCodeLength", d.c.maxCodeLength)

	passed := true
	if len(deltaOutput) != len(shortData) {
		fmt.Printf("Failed to decompress. Original length %d Decomp length %d\n", len(shortData), len(deltaOutput))
		passed = false
	}
	for i := 0; i < len(shortData); i++ {
		if shortData[i] != deltaOutput[i] {
			fmt.Printf("*** Different at location %d value in original %d in decomp %d\n", i, shortData[i], deltaOutput[i])
			passed = false
			break
		}
	}

	if passed {
		fmt.Printf("PASSED HUFF 16-bit compression-decompression\n")
	} else {
		t.Errorf("HUFF 16-bit compression-decompression FAILED")
	}
}

func DeltaHuffCompress(t *testing.T, shortData []uint16, cols int, rows int, maxShort uint16) {
	startTime := time.Now()
	deltaComp, _ := DeltaCompressU16(shortData, cols, rows, maxShort)
	elapsedTime := time.Since(startTime)
	fmt.Println("Delta compress", elapsedTime, "Delta size", len(deltaComp))

	var c CanHuffmanCompressU16
	c.Init(deltaComp)
	c.Compress()

	fmt.Printf("Delta Huff Compress: %d short %d -> %d bytes (%.2f:1)\n", len(shortData), len(shortData)*2, len(c.Out), float64(len(shortData)*2)/float64(len(c.Out)))

	var d CanHuffmanDecompressU16
	startTime = time.Now()
	d.Init(c.Out)
	d.ReadTable()
	elapsedTime = time.Since(startTime)
	fmt.Println("Huff - Huff ReadTable decompress took ", elapsedTime)
	d.Decompress()

	deltaOutput := DeltaDecompressU16(d.Out, cols, rows)

	elapsedTime = time.Since(startTime)
	fmt.Println("Delta Decompress", elapsedTime)

	var passed = true

	for i := 0; i < len(shortData); i++ {
		if shortData[i] != deltaOutput[i] {
			fmt.Printf("***** Delta FSE Different at location %d value in original %d in decomp %d\n", i, shortData[i], deltaOutput[i])
			passed = false
			break
		}
	}
	if len(deltaOutput) != len(shortData) {
		fmt.Printf(" Delta FSE Failed to decompress. Original length %d Decomp length %d\n", len(shortData), len(deltaOutput))
		passed = false
	}
	if passed {
		fmt.Printf("Delta FSE PASSED 16-bit compression-decompression\n")
	} else {
		t.Errorf("Delta FSE 16-bit compression-decompression FAILED")
	}
}
