package mic

import (
	"fmt"
	"testing"
)

func TestHuffmanCompression(t *testing.T) {

	var c CanHuffmanCompressU16

	in := []uint16{256, 256, 256, 1025, 457, 457, 457, 8000, 1, 65534}

	c.Init(in)
	c.Compress()

	fmt.Printf("Delimiter %d PixelDepth %d Compressed len %d\n", c.delimiterForCompressDecompress, c.pixelDepth, len(c.Out))

	fmt.Println(in)
	fmt.Println(c.symbolsOfInterestList)
	fmt.Println(c.canHuffmanTable)
	fmt.Println(c.Out)

	var d CanHuffmanDecompressU16

	d.Init(c.Out)
	d.ReadTable()

	fmt.Println(d.c.symbolsOfInterestList)
	fmt.Println(d.c.canHuffmanTable)
	//fmt.Println(d.codeLengthToSymbolTable)

	d.Decompress()
	fmt.Println(d.Out)

	if len(in) != len(d.Out) {
		t.Errorf("Size mismatch\n")
	}

	for i, v := range in {
		if v != d.Out[i] {
			t.Errorf("Value mismatch at %d values in %d out %d\n", i, v, d.Out[i])
		}
	}

}

func TestDeltaRLEHuffmanCompression(t *testing.T) {

	in := []uint16{256, 256, 256, 1025, 457, 457, 457, 8000, 1, 65534}

	var drc DeltaRleCompressU16
	deltaComp, _ := drc.Compress(in, 5, 2, 65534)

	var c CanHuffmanCompressU16

	c.Init(deltaComp)
	c.Compress()

	fmt.Printf("Delimiter %d PixelDepth %d Compressed len %d\n", c.delimiterForCompressDecompress, c.pixelDepth, len(c.Out))

	fmt.Println(in)
	fmt.Println(deltaComp)
	fmt.Println(c.symbolsOfInterestList)
	fmt.Println(c.canHuffmanTable)
	fmt.Printf("{%d %d}\n", c.delimiterForCompressDecompress, c.pixelDepth)
	fmt.Println(c.Out)

	var d CanHuffmanDecompressU16

	d.Init(c.Out)
	d.ReadTable()

	fmt.Println(d.c.symbolsOfInterestList)
	fmt.Println(d.c.canHuffmanTable)
	//fmt.Println(d.codeLengthToSymbolTable)
	fmt.Printf("{%d %d}\n", d.c.delimiterForCompressDecompress, d.c.pixelDepth)

	d.Decompress()
	fmt.Println(d.Out)

	var drd DeltaRleDecompressU16
	drd.Decompress(d.Out, 5, 2)

	deltaOutput := drd.Out

	fmt.Println(deltaOutput)

	if len(in) != len(deltaOutput) {
		t.Errorf("Size mismatch\n")
	}

	for i, v := range in {
		if v != deltaOutput[i] {
			t.Errorf("Value mismatch at %d values in %d out %d\n", i, v, deltaOutput[i])
		}
	}

	for i, v := range deltaComp {
		if v != d.Out[i] {
			t.Errorf("Value mismatch at %d values in %d out %d\n", i, v, d.Out[i])
		}
	}

}
