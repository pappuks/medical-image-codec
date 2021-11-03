// Copyright 2021 Kuldeep Singh
// This source code is licensed under a MIT-style
// license that can be found in the LICENSE file.
package mic

import (
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

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
	{name: "MR", fileName: "testdata/MR.2564.1", isBinary: false},
	{name: "CT", fileName: "testdata/CT.3985.1", isBinary: false},
	{name: "CR", fileName: "testdata/CR.4509.1", isBinary: false},
	{name: "XR", fileName: "testdata/xr_hands.dcm", isBinary: false},
	{name: "MG1", fileName: "testdata/MG_image_bin2.bin", isBinary: true, rows: 2457, cols: 1996},
	{name: "MG2", fileName: "testdata/MG_Image_2_frame.bin", isBinary: true, rows: 2457, cols: 1996},
}

func ReadBinaryFile(fileName string, cols int, rows int) ([]byte, []uint16, uint16) {
	byteData, _ := os.ReadFile(fileName)
	shortData := make([]uint16, cols*rows)
	var maxShort uint16 = 0

	for i := 0; i < len(byteData); {
		shortData[i/2] = uint16(byteData[i+1])<<8 + uint16(byteData[i])
		if shortData[i/2] > maxShort {
			maxShort = shortData[i/2]
		}
		i += 2
	}
	return byteData, shortData, maxShort
}

func ReadDicomFile(fileName string) ([]byte, []uint16, uint16, int, int) {
	dataset, _ := dicom.ParseFile(fileName, nil)
	pixelDataElement, _ := dataset.FindElementByTag(tag.PixelData)
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
		return ReadDicomFile(td.fileName)
	}
}

func BenchmarkDeltaRLEHuffCompress(b *testing.B) {
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
			for i := 0; i < b.N; i++ {
				var s4 ScratchU16
				deltaDecompFSE, _ := FSEDecompressU16(deltaFSEComp, &s4)
				var drd DeltaRleDecompressU16
				drd.Decompress(deltaDecompFSE, cols, rows)
			}
		})
	}
}

func TestCompressDCMFile(t *testing.T) {
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

func TestCompressDCMImages(t *testing.T) {
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
